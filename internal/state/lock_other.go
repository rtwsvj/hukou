//go:build !darwin && !linux && !freebsd && !netbsd && !openbsd

package state

import (
	"errors"
	"os"
)

var ErrLocked = errors.New("hukou state is locked by another process")

// Lock uses an atomic directory fallback on platforms without flock support.
type Lock struct {
	path string
}

func Acquire(path string) (*Lock, error) {
	lockDir := path + ".d"
	if err := os.Mkdir(lockDir, 0o700); err != nil {
		if os.IsExist(err) {
			return nil, ErrLocked
		}
		return nil, err
	}
	return &Lock{path: lockDir}, nil
}

func (l *Lock) Release() error {
	if l == nil || l.path == "" {
		return nil
	}
	err := os.Remove(l.path)
	if err == nil {
		l.path = ""
	}
	return err
}
