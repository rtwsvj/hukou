package cmd

import (
	"errors"
	"fmt"
	"io"

	"github.com/rtwsvj/hukou/internal/activation"
	"github.com/rtwsvj/hukou/internal/manifest"
	"github.com/rtwsvj/hukou/internal/store"
	statejournal "github.com/rtwsvj/hukou/internal/transaction"
	"github.com/spf13/cobra"
)

var rollbackTo string

var rollbackCmd = &cobra.Command{
	Use:   "rollback <name>",
	Short: "Roll back to the logical parent activation or a named ancestor",
	Long: `rollback atomically replaces the live file with a retained version.
Without --to it follows activation history to the logical parent. --to selects
an exact retained ancestor; --to original explicitly restores the immutable
adoption-time backup.`,
	Args: cobra.ExactArgs(1),
	RunE: runRollback,
}

func init() {
	rollbackCmd.Flags().StringVar(&rollbackTo, "to", "", "target ancestor tag, or original")
	rootCmd.AddCommand(rollbackCmd)
}

func runRollback(cmd *cobra.Command, args []string) error {
	return doRollback(cmd.OutOrStdout(), cmd.ErrOrStderr(), args[0], rollbackTo)
}

func doRollback(stdout, stderr io.Writer, name, to string) error {
	return doRollbackWithSave(stdout, stderr, name, to, saveManifest)
}

func doRollbackWithSave(stdout, stderr io.Writer, name, to string, save func(*manifest.Manifest) error) error {
	return doRollbackWithDeps(stdout, stderr, name, to, save, nil)
}

func doRollbackWithDeps(stdout, stderr io.Writer, name, to string, save func(*manifest.Manifest) error, snapshotLive func(string) (*store.LiveSnapshot, error)) error {
	lock, err := acquireMutationLock(stderr)
	if err != nil {
		return fail(fmt.Errorf("acquire state lock: %w", err))
	}
	defer releaseMutationLock(lock, stderr)

	m, err := loadManifest()
	if err != nil {
		return fail(err)
	}
	e := m.Get(name)
	if e == nil {
		return fail(fmt.Errorf("adopted tool %q not found", name))
	}

	currentSHA, err := store.SHA256File(e.Path)
	if err != nil {
		return fail(fmt.Errorf("read current file: %w", err))
	}
	if currentSHA != e.SHA256 {
		return fail(fmt.Errorf("current file SHA-256 does not match the manifest"))
	}

	s := newStore()
	target := to
	targetActivationID := ""
	restoreOriginal := false
	if target == "" {
		previous, previousErr := activation.Previous(*e)
		if previousErr != nil {
			return fail(previousErr)
		}
		target = previous.Tag
		targetActivationID = previous.ID
	} else if target == "original" {
		restoreOriginal = true
	} else if target != e.Tag {
		ancestor, ancestorErr := activation.FindAncestorByTag(*e, target)
		if ancestorErr != nil {
			return fail(ancestorErr)
		}
		targetActivationID = ancestor.ID
	}

	if target == e.Tag {
		fmt.Fprintf(stdout, "%s is already at %s\n", name, target)
		return nil
	}

	oldEntry := manifest.PrepareEntry(*e)
	targetSource, err := s.ActivationSource(name, target)
	if err != nil {
		return fail(fmt.Errorf("resolve rollback source %s: %w", target, err))
	}
	newSHA, err := store.SHA256File(targetSource)
	if err != nil {
		return fail(fmt.Errorf("sha256 rollback source: %w", err))
	}
	newEntry := manifest.PrepareEntry(oldEntry)
	eventID, err := activation.NewID()
	if err != nil {
		return fail(fmt.Errorf("prepare rollback activation: %w", err))
	}
	activatedAt := rfc3339Now()
	if restoreOriginal {
		err = activation.RecordRestoreOriginal(&newEntry, eventID, newSHA, activatedAt)
	} else {
		err = activation.RecordRollback(&newEntry, eventID, targetActivationID, activatedAt)
		if err == nil && newEntry.SHA256 != newSHA {
			err = fmt.Errorf("rollback source SHA-256 does not match activation history: got %s want %s", newSHA, newEntry.SHA256)
		}
	}
	if err != nil {
		return fail(fmt.Errorf("prepare rollback activation: %w", err))
	}
	newEntry.AssetName = ""
	newEntry.AssetSHA256 = ""
	newEntry.ChecksumAsset = ""
	newEntry.ChecksumVerified = false
	afterManifest := cloneManifest(m)
	afterManifest.Put(newEntry)
	manifestBytes, err := encodeManifest(afterManifest)
	if err != nil {
		return fail(fmt.Errorf("encode rollback manifest: %w", err))
	}
	tx, err := statejournal.Begin(dataRoot(), "rollback", name, []statejournal.Spec{
		{Role: "live", Path: e.Path, After: statejournal.RegularFile(targetSource)},
		{Role: "manifest", Path: manifestPath(), After: statejournal.RegularBytes(manifestBytes, 0o600)},
	})
	if err != nil {
		return fail(fmt.Errorf("prepare rollback transaction: %w", err))
	}
	if err := validateTransactionStateSHA(tx, "live", oldEntry.SHA256, false); err != nil {
		return fail(abortStateTransaction(tx, fmt.Errorf("current file changed while preparing the transaction; refusing overwrite: %w", err)))
	}
	if err := validateTransactionStateSHA(tx, "live", newSHA, true); err != nil {
		return fail(abortStateTransaction(tx, err))
	}
	if snapshotLive != nil {
		// Test-only compatibility seam: existing adversarial tests can mutate the
		// live path after PREPARED. The durable journal remains authoritative.
		probe, probeErr := snapshotLive(e.Path)
		if probeErr != nil {
			return fail(abortStateTransaction(tx, probeErr))
		}
		if cleanupErr := probe.Commit(); cleanupErr != nil {
			return fail(abortStateTransaction(tx, cleanupErr))
		}
	}
	if err := tx.Apply("live"); err != nil {
		return fail(abortStateTransaction(tx, fmt.Errorf("current file changed before activation or the target could not be activated; refusing overwrite: activate %s: %w", target, err)))
	}
	if err := tx.Verify("manifest", false); err != nil {
		return fail(abortStateTransaction(tx, fmt.Errorf("manifest changed during rollback; refusing overwrite: %w", err)))
	}
	*e = newEntry
	m.Put(newEntry)
	if err := save(m); err != nil {
		*e = oldEntry
		m.Put(oldEntry)
		return fail(abortStateTransaction(tx, fmt.Errorf("save manifest: %w", err)))
	}
	if err := commitStateTransaction(tx, stderr); err != nil {
		refreshErr := refreshManifest(m)
		if current := m.Get(name); current != nil {
			*e = *current
		}
		return fail(errors.Join(fmt.Errorf("commit rollback transaction: %w", err), refreshErr))
	}
	finalizeStateTransaction(tx, stderr, name, "rollback")

	fmt.Fprintf(stdout, "Rolled back %s to %s\n", name, target)
	_ = stderr // reserved for future diagnostics
	return nil
}
