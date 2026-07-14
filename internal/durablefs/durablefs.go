// Package durablefs provides small filesystem primitives that make both file
// contents and directory-entry changes durable before they return success.
package durablefs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// FileSystem is the production implementation of the durable filesystem
// primitives. Its zero value is ready for use. Methods exist so callers can
// depend on narrow interfaces and inject deterministic fakes in tests.
type FileSystem struct {
	// syncDirFn is an internal fault-injection seam for this package's tests.
	// Production callers always use the zero value and therefore the real
	// directory sync implementation below.
	syncDirFn func(string) error
}

var defaultFileSystem FileSystem

// SyncFile flushes the contents and metadata of an open file.
func SyncFile(file *os.File) error { return defaultFileSystem.SyncFile(file) }

// SyncDir flushes directory-entry changes below path.
func SyncDir(path string) error { return defaultFileSystem.SyncDir(path) }

// SyncParent flushes the directory containing path.
func SyncParent(path string) error { return defaultFileSystem.SyncParent(path) }

// Mkdir creates one directory and persists its entry in the parent directory.
func Mkdir(path string, mode os.FileMode) error { return defaultFileSystem.Mkdir(path, mode) }

// MkdirAll creates every missing directory, persists each new entry, and
// reaffirms the ancestor entries of an already-visible path on retries.
func MkdirAll(path string, mode os.FileMode) error {
	return defaultFileSystem.MkdirAll(path, mode)
}

// AtomicWriteFile writes a complete, synced temporary file beside path, then
// atomically renames it over path and persists the rename.
func AtomicWriteFile(path string, data []byte, mode os.FileMode) error {
	return defaultFileSystem.AtomicWriteFile(path, data, mode)
}

// Rename atomically renames oldPath to newPath in the same directory and then
// persists the changed directory entry.
func Rename(oldPath, newPath string) error {
	return defaultFileSystem.Rename(oldPath, newPath)
}

// Link creates newPath as a hard link and persists its parent directory.
func Link(oldPath, newPath string) error { return defaultFileSystem.Link(oldPath, newPath) }

// Remove removes path and persists its parent directory.
func Remove(path string) error { return defaultFileSystem.Remove(path) }

// RemoveAll removes path recursively and persists its parent directory.
func RemoveAll(path string) error { return defaultFileSystem.RemoveAll(path) }

func (FileSystem) SyncFile(file *os.File) error {
	if file == nil {
		return errors.New("sync file: nil file")
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync file %s: %w", file.Name(), err)
	}
	return nil
}

func (fs FileSystem) SyncDir(path string) error {
	if fs.syncDirFn != nil {
		return fs.syncDirFn(path)
	}
	dir, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open directory %s for sync: %w", path, err)
	}
	info, statErr := dir.Stat()
	if statErr == nil && !info.IsDir() {
		statErr = fmt.Errorf("path is not a directory: %s", path)
	}
	var syncErr error
	if statErr == nil {
		syncErr = dir.Sync()
		if syncErr != nil {
			syncErr = fmt.Errorf("sync directory %s: %w", path, syncErr)
		}
	}
	closeErr := dir.Close()
	if closeErr != nil {
		closeErr = fmt.Errorf("close directory %s: %w", path, closeErr)
	}
	return errors.Join(statErr, syncErr, closeErr)
}

func (fs FileSystem) SyncParent(path string) error {
	return fs.SyncDir(filepath.Dir(filepath.Clean(path)))
}

func (fs FileSystem) Mkdir(path string, mode os.FileMode) error {
	if err := os.Mkdir(path, mode); err != nil {
		return err
	}
	if err := fs.SyncParent(path); err != nil {
		return fmt.Errorf("persist directory %s: %w", path, err)
	}
	return nil
}

func (fs FileSystem) MkdirAll(path string, mode os.FileMode) error {
	clean := filepath.Clean(path)
	var missing []string
	current := clean
	for {
		info, err := os.Stat(current)
		if err == nil {
			if !info.IsDir() {
				return &os.PathError{Op: "mkdir", Path: current, Err: errors.New("not a directory")}
			}
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		missing = append(missing, current)
		parent := filepath.Dir(current)
		if parent == current {
			return err
		}
		current = parent
	}
	// The deepest directory that already exists may be the visible result of a
	// previous mkdir whose parent sync never completed. Re-sync every ancestor
	// entry from the filesystem root down before relying on that directory. This
	// makes a retry repair an indeterminate mkdir instead of merely observing it.
	if err := fs.syncAncestorEntries(current); err != nil {
		return fmt.Errorf("persist existing directory ancestry for %s: %w", clean, err)
	}

	for i := len(missing) - 1; i >= 0; i-- {
		if err := fs.Mkdir(missing[i], mode); err != nil {
			if !errors.Is(err, os.ErrExist) {
				return err
			}
			info, statErr := os.Stat(missing[i])
			if statErr != nil {
				return statErr
			}
			if !info.IsDir() {
				return &os.PathError{Op: "mkdir", Path: missing[i], Err: errors.New("not a directory")}
			}
			if syncErr := fs.SyncParent(missing[i]); syncErr != nil {
				return fmt.Errorf("persist concurrently created directory %s: %w", missing[i], syncErr)
			}
		}
	}
	return nil
}

// syncAncestorEntries persists every directory entry needed to reach path.
// Syncing root-to-leaf ensures that an interruption partway through still
// leaves the longest possible durable prefix for the next retry.
func (fs FileSystem) syncAncestorEntries(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	current := filepath.Clean(abs)
	var parents []string
	for {
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		parents = append(parents, parent)
		current = parent
	}
	for i := len(parents) - 1; i >= 0; i-- {
		if err := fs.SyncDir(parents[i]); err != nil {
			return err
		}
	}
	return nil
}

func (fs FileSystem) AtomicWriteFile(path string, data []byte, mode os.FileMode) (retErr error) {
	dir := filepath.Dir(filepath.Clean(path))
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	closed := false
	defer func() {
		if !closed {
			retErr = errors.Join(retErr, tmp.Close())
		}
		if removeErr := fs.Remove(tmpPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			retErr = errors.Join(retErr, fmt.Errorf("remove temporary file %s: %w", tmpPath, removeErr))
		}
	}()

	if err := tmp.Chmod(mode.Perm()); err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := fs.SyncFile(tmp); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		closed = true
		return err
	}
	closed = true
	if err := fs.Rename(tmpPath, path); err != nil {
		return err
	}
	return nil
}

func (fs FileSystem) Rename(oldPath, newPath string) error {
	oldDir := filepath.Clean(filepath.Dir(oldPath))
	newDir := filepath.Clean(filepath.Dir(newPath))
	if oldDir != newDir {
		return fmt.Errorf("durable rename requires the same parent directory: %s -> %s", oldPath, newPath)
	}
	if err := os.Rename(oldPath, newPath); err != nil {
		return err
	}
	if err := fs.SyncDir(newDir); err != nil {
		return fmt.Errorf("persist rename %s -> %s: %w", oldPath, newPath, err)
	}
	return nil
}

func (fs FileSystem) Link(oldPath, newPath string) error {
	if err := os.Link(oldPath, newPath); err != nil {
		return err
	}
	if err := fs.SyncParent(newPath); err != nil {
		return fmt.Errorf("persist link %s -> %s: %w", newPath, oldPath, err)
	}
	return nil
}

func (fs FileSystem) Remove(path string) error {
	if err := os.Remove(path); err != nil {
		return err
	}
	if err := fs.SyncParent(path); err != nil {
		return fmt.Errorf("persist removal of %s: %w", path, err)
	}
	return nil
}

func (fs FileSystem) RemoveAll(path string) error {
	if _, err := os.Lstat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// Absence may be the visible result of a prior removal interrupted
			// before its parent directory was synced. Reaffirm the parent before
			// reporting an idempotent retry as successful.
			if syncErr := fs.SyncParent(path); syncErr != nil {
				return fmt.Errorf("persist absence of %s: %w", path, syncErr)
			}
			return nil
		}
		return err
	}
	if err := os.RemoveAll(path); err != nil {
		return err
	}
	if err := fs.SyncParent(path); err != nil {
		return fmt.Errorf("persist recursive removal of %s: %w", path, err)
	}
	return nil
}
