package store

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// LiveSnapshot captures the filesystem topology and contents at a live binary
// path before an activation. Restore returns the path to exactly the previous
// regular-file or symlink state; Commit discards the snapshot after the
// manifest has been persisted successfully.
type LiveSnapshot struct {
	path       string
	backupPath string
	linkTarget string
	wasSymlink bool
	done       bool
}

// SnapshotLive captures path without changing it. Regular files are preserved
// in the same directory so Restore can use an atomic rename. Symlinks only need
// their original target recorded.
func SnapshotLive(path string) (*LiveSnapshot, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}

	s := &LiveSnapshot{path: path}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(path)
		if err != nil {
			return nil, err
		}
		s.wasSymlink = true
		s.linkTarget = target
		return s, nil
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("cannot snapshot non-regular live path: %s", path)
	}

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".hukou-rollback-*")
	if err != nil {
		return nil, err
	}
	backup := tmp.Name()
	if err := tmp.Close(); err != nil {
		_ = os.Remove(backup)
		return nil, err
	}
	if err := os.Remove(backup); err != nil {
		return nil, err
	}

	// A hard link is cheap and preserves the exact bytes even if the live name
	// is moved later. Fall back to a copy on filesystems that disallow links.
	if err := os.Link(path, backup); err != nil {
		if err := copySnapshot(path, backup, info.Mode()); err != nil {
			_ = os.Remove(backup)
			return nil, err
		}
	}
	s.backupPath = backup
	return s, nil
}

// Restore atomically returns the live path to its captured state.
func (s *LiveSnapshot) Restore() error {
	if s == nil || s.done {
		return nil
	}
	if s.wasSymlink {
		if err := atomicSymlink(s.linkTarget, s.path, filepath.Dir(s.path)); err != nil {
			return err
		}
		s.done = true
		return nil
	}
	if s.backupPath == "" {
		return fmt.Errorf("regular-file snapshot has no backup")
	}
	if err := os.Rename(s.backupPath, s.path); err != nil {
		return fmt.Errorf("restore live path: %w", err)
	}
	s.done = true
	return nil
}

// Commit removes the rollback copy after the live path and manifest agree.
func (s *LiveSnapshot) Commit() error {
	if s == nil || s.done {
		return nil
	}
	if s.backupPath != "" {
		if err := os.Remove(s.backupPath); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	s.done = true
	return nil
}

func copySnapshot(srcPath, dstPath string, mode os.FileMode) error {
	src, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer src.Close()

	dst, err := os.OpenFile(dstPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode.Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, src); err != nil {
		_ = dst.Close()
		_ = os.Remove(dstPath)
		return err
	}
	if err := dst.Sync(); err != nil {
		_ = dst.Close()
		_ = os.Remove(dstPath)
		return err
	}
	return dst.Close()
}
