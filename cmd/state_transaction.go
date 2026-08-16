package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/rtwsvj/hukou/internal/i18n"
	"github.com/rtwsvj/hukou/internal/manifest"
	statejournal "github.com/rtwsvj/hukou/internal/transaction"
)

func cloneManifest(m *manifest.Manifest) *manifest.Manifest {
	return m.Clone()
}

// stressPause is an inert fault-injection seam for the stress-test suite
// (docs/audit/stress-*.md): when HUKOU_STRESS_PAUSE names the current phase,
// the process sleeps for stressPauseDuration so the harness can SIGKILL it at
// a deterministic transaction boundary. Without the variable, every call is a
// no-op and the seam costs nothing. Phases: "post-prepared" (PREPARED journal
// durable, business mutations begun), "pre-commit" (business state ready,
// COMMIT not yet written), "post-commit" (COMMIT durable, cleanup not yet
// run), "pre-activate" (store version ready, live file not yet replaced),
// "pre-finalize" (cleanup about to run).
func stressPause(phase string) {
	if os.Getenv("HUKOU_STRESS_PAUSE") != phase {
		return
	}
	// Marker file (when requested) lets the harness confirm deterministically
	// that the intended phase was reached before it kills the process — the
	// alternative (timing guesses) is flaky on slow networks.
	if marker := os.Getenv("HUKOU_STRESS_PAUSE_FILE"); marker != "" {
		_ = os.WriteFile(marker, []byte(phase+"\n"), 0o644)
	}
	time.Sleep(stressPauseDuration)
}

const stressPauseDuration = 30 * time.Second

func encodeManifest(m *manifest.Manifest) ([]byte, error) {
	toWrite := m.Clone()
	if toWrite == nil {
		return nil, i18n.Errorf("cannot encode a nil manifest")
	}
	if err := toWrite.Normalize(); err != nil {
		return nil, err
	}
	if err := toWrite.Validate(); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(toWrite); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func ensureDryRunTransactionClean() error {
	if err := statejournal.CheckClean(dataRoot()); err != nil {
		return i18n.Wrapf("dry-run cannot recover unfinished state without writing: %w", err)
	}
	return nil
}

func validateTransactionStateSHA(tx *statejournal.Transaction, role, want string, after bool) error {
	var (
		state statejournal.State
		ok    bool
	)
	if after {
		state, ok = tx.After(role)
	} else {
		state, ok = tx.Before(role)
	}
	if !ok {
		return i18n.Errorf("transaction role %q not found", role)
	}
	if state.SHA256 != want {
		return i18n.Errorf("transaction %s SHA-256 mismatch: got %s want %s", role, state.SHA256, want)
	}
	return nil
}

func abortStateTransaction(tx *statejournal.Transaction, operationErr error) error {
	if tx == nil {
		return operationErr
	}
	if abortErr := tx.Abort(); abortErr != nil {
		return errors.Join(operationErr, i18n.Wrapf("recover prepared transaction: %w", abortErr))
	}
	return operationErr
}

// commitStateTransaction handles the indeterminate edge where COMMIT creation
// became visible before a sync error. Recover treats the physical marker as the
// decision and converges to one complete side before returning the error.
func commitStateTransaction(tx *statejournal.Transaction, stderr io.Writer) error {
	stressPause("pre-commit")
	if err := tx.Commit(); err != nil {
		summary, recoverErr := statejournal.Recover(dataRoot())
		reportRecoverSummary(stderr, summary)
		return errors.Join(err, recoverErr)
	}
	stressPause("post-commit")
	return nil
}

func finalizeStateTransaction(tx *statejournal.Transaction, stderr io.Writer, name, operation string) {
	stressPause("pre-finalize")
	if err := tx.Finalize(); err != nil && stderr != nil {
		fmt.Fprintf(stderr, "warning: %s %s committed, but transaction journal cleanup failed: %v\n", name, operation, err)
	}
}

func refreshManifest(m *manifest.Manifest) error {
	loaded, err := loadManifest()
	if err != nil {
		return err
	}
	*m = *loaded
	return nil
}
