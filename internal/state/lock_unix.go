//go:build darwin || linux || freebsd || netbsd || openbsd

package state

import (
	"errors"
	"fmt"
	"github.com/rtwsvj/hukou/internal/i18n"
	"os"
	"syscall"
)

// ErrLocked reports that another hukou mutation currently owns the state lock.
var ErrLocked = i18n.Errorf("hukou state is locked by another process")

// Lock is an advisory process lock backed by flock on supported Unix systems.
type Lock struct {
	file *os.File
}

// Acquire obtains a non-blocking exclusive lock at path.
func Acquire(path string) (*Lock, error) {
	fd, err := syscall.Open(path, syscall.O_CREAT|syscall.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, err
	}
	syscall.CloseOnExec(fd)
	f := os.NewFile(uintptr(fd), path)
	if f == nil {
		_ = syscall.Close(fd)
		return nil, i18n.Errorf("open state lock: invalid file descriptor")
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() {
		_ = f.Close()
		return nil, i18n.Errorf("state lock is not a regular file: %s", path)
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, ErrLocked
		}
		return nil, err
	}
	if err := f.Truncate(0); err != nil {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
		return nil, err
	}
	if _, err := fmt.Fprintf(f, "%d\n", os.Getpid()); err != nil {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
		return nil, err
	}
	if err := f.Sync(); err != nil {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
		return nil, err
	}
	return &Lock{file: f}, nil
}

// Release unlocks and closes the state lock.
func (l *Lock) Release() error {
	if l == nil || l.file == nil {
		return nil
	}
	unlockErr := syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	closeErr := l.file.Close()
	l.file = nil
	return errors.Join(unlockErr, closeErr)
}
