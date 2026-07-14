package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/rtwsvj/hukou/internal/manifest"
	"github.com/rtwsvj/hukou/internal/store"
	statejournal "github.com/rtwsvj/hukou/internal/transaction"
	"github.com/spf13/cobra"
)

var rollbackTo string

var rollbackCmd = &cobra.Command{
	Use:   "rollback <name>",
	Short: "回滚到上一版本或指定版本",
	Long: `rollback 原子替换活跃文件为 store 中保存的旧版本。
不带 --to 时自动选择上一个版本（按修改时间），包含 original 备份。`,
	Args: cobra.ExactArgs(1),
	RunE: runRollback,
}

func init() {
	rollbackCmd.Flags().StringVar(&rollbackTo, "to", "", "目标版本标签（或 original）")
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
	lock, err := acquireMutationLock()
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
		return fail(fmt.Errorf("未找到 %s", name))
	}

	currentSHA, err := store.SHA256File(e.Path)
	if err != nil {
		return fail(fmt.Errorf("无法读取当前文件: %w", err))
	}
	if currentSHA != e.SHA256 {
		return fail(fmt.Errorf("当前文件 sha256 与 manifest 不一致"))
	}

	s := newStore()
	target := to
	if target == "" {
		target, err = previousVersion(s, name, e.Tag)
		if err != nil {
			return fail(err)
		}
	}

	if target == e.Tag {
		fmt.Fprintf(stdout, "%s 当前已是 %s\n", name, target)
		return nil
	}

	oldEntry := *e
	targetSource, err := s.ActivationSource(name, target)
	if err != nil {
		return fail(fmt.Errorf("resolve rollback source %s: %w", target, err))
	}
	newSHA, err := store.SHA256File(targetSource)
	if err != nil {
		return fail(fmt.Errorf("sha256 rollback source: %w", err))
	}
	newEntry := oldEntry
	newEntry.Tag = target
	newEntry.SHA256 = newSHA
	newEntry.UpdatedAt = rfc3339Now()
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
		return fail(abortStateTransaction(tx, fmt.Errorf("当前文件在创建事务日志时被外部修改；拒绝覆盖: %w", err)))
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
		return fail(abortStateTransaction(tx, fmt.Errorf("当前文件在事务应用前被外部修改或无法激活；拒绝覆盖: activate %s: %w", target, err)))
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
	if err := commitStateTransaction(tx); err != nil {
		refreshErr := refreshManifest(m)
		if current := m.Get(name); current != nil {
			*e = *current
		}
		return fail(errors.Join(fmt.Errorf("commit rollback transaction: %w", err), refreshErr))
	}
	finalizeStateTransaction(tx, stderr, name, "回滚")

	fmt.Fprintf(stdout, "已回滚 %s → %s\n", name, target)
	_ = stderr // reserved for future diagnostics
	return nil
}

func previousVersion(s *store.Store, name, current string) (string, error) {
	versions, err := s.Versions(name)
	if err != nil {
		return "", err
	}

	type version struct {
		tag   string
		mtime time.Time
	}
	var list []version
	for _, tag := range versions {
		if tag == current {
			continue
		}
		info, err := os.Stat(filepath.Join(s.Root, name, tag))
		if err != nil {
			continue
		}
		list = append(list, version{tag: tag, mtime: info.ModTime()})
	}

	origPath := filepath.Join(s.Root, name, "original", name)
	if info, err := os.Stat(origPath); err == nil {
		if current != "original" {
			list = append(list, version{tag: "original", mtime: info.ModTime()})
		}
	}

	if len(list) == 0 {
		return "", fmt.Errorf("没有可回滚的版本")
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].mtime.After(list[j].mtime)
	})
	return list[0].tag, nil
}
