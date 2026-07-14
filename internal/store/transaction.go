package store

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/rtwsvj/hukou/internal/durablefs"
)

// ActivationSource resolves the single immutable regular file backing a store
// version. Transaction preparation uses this before publishing PREPARED so the
// redo payload is captured independently of the store entry.
func (s *Store) ActivationSource(name, tag string) (string, error) {
	if err := validateNameTag("name", name); err != nil {
		return "", err
	}
	if err := validateActivationTag(tag); err != nil {
		return "", err
	}
	tagDir, err := s.storeDir(false, name, tag)
	if err != nil {
		return "", err
	}
	entries, err := os.ReadDir(tagDir)
	if err != nil {
		return "", err
	}
	var binary string
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}
		if binary != "" {
			return "", fmt.Errorf("version %s/%s contains multiple binaries", name, tag)
		}
		binary = filepath.Join(tagDir, entry.Name())
	}
	if binary == "" {
		return "", fmt.Errorf("no binary found in version %s/%s", name, tag)
	}
	resolved, err := s.ensureUnderRoot(binary)
	if err != nil {
		return "", fmt.Errorf("activation source: %w", err)
	}
	return resolved, nil
}

// PrepareOriginalPath creates and validates the immutable original namespace
// without writing the backup file itself. The transaction journal can then
// model original as an absent->regular mutation and recover an interrupted
// first adoption without leaving a retry-blocking orphan.
func (s *Store) PrepareOriginalPath(name, binaryName string) (string, error) {
	if err := validateNameTag("name", name); err != nil {
		return "", err
	}
	if binaryName == "" || filepath.Base(binaryName) != binaryName {
		return "", fmt.Errorf("invalid original binary name %q", binaryName)
	}
	dir, err := s.storeDir(true, name, "original")
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, binaryName), nil
}

// LiveSnapshot captures the filesystem topology and contents at a live binary
// path before an activation. Restore returns the path to the previous bytes and
// rwx mode (or the exact previous symlink target); Commit discards the snapshot
// after the manifest has been persisted successfully.
type LiveSnapshot struct {
	path       string
	backupPath string
	linkTarget string
	wasSymlink bool
	done       bool
}

// SnapshotLive captures path without changing it. Regular files are copied to
// an independent inode in the same directory so external in-place writes cannot
// mutate the rollback snapshot. Symlinks only need their original target recorded.
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

	if err := copySnapshot(path, backup, info.Mode()); err != nil {
		_ = os.Remove(backup)
		return nil, err
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
	if err := durablefs.Rename(s.backupPath, s.path); err != nil {
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
		if err := durablefs.Remove(s.backupPath); err != nil && !os.IsNotExist(err) {
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
	if err := durablefs.SyncFile(dst); err != nil {
		_ = dst.Close()
		_ = os.Remove(dstPath)
		return err
	}
	if err := dst.Close(); err != nil {
		return err
	}
	return durablefs.SyncParent(dstPath)
}
