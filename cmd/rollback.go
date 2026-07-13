package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/rtwsvj/hukou/internal/manifest"
	"github.com/rtwsvj/hukou/internal/store"
	"github.com/spf13/cobra"
)

var rollbackTo string

var rollbackCmd = &cobra.Command{
	Use:   "rollback <name>",
	Short: "回滚到上一版本或指定版本",
	Long: `rollback 切换软链到 store 中保存的旧版本。
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

	snapshot, err := store.SnapshotLive(e.Path)
	if err != nil {
		return fail(fmt.Errorf("snapshot current installation: %w", err))
	}
	oldEntry := *e

	if target == "original" {
		if err := activateOriginal(s, name, e.Path); err != nil {
			return fail(restoreLiveAfterError(snapshot, fmt.Errorf("activate original: %w", err)))
		}
	} else {
		if err := s.Activate(name, target, e.Path); err != nil {
			return fail(restoreLiveAfterError(snapshot, fmt.Errorf("activate %s: %w", target, err)))
		}
	}

	newSHA, err := store.SHA256File(e.Path)
	if err != nil {
		return fail(restoreLiveAfterError(snapshot, fmt.Errorf("读取回滚后文件失败: %w", err)))
	}

	// Rewrite manifest tag + sha256 to match the newly active binary.
	e.Tag = target
	e.SHA256 = newSHA
	e.UpdatedAt = rfc3339Now()
	e.AssetName = ""
	e.AssetSHA256 = ""
	e.ChecksumAsset = ""
	e.ChecksumVerified = false
	m.Put(*e)
	if err := save(m); err != nil {
		*e = oldEntry
		m.Put(oldEntry)
		return fail(restoreLiveAfterError(snapshot, fmt.Errorf("save manifest: %w", err)))
	}
	if err := snapshot.Commit(); err != nil {
		fmt.Fprintf(stderr, "警告: %s 回滚已完成，但清理事务快照失败: %v\n", name, err)
	}

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
