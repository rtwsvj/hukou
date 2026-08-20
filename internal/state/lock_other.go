//go:build !darwin && !linux && !freebsd && !netbsd && !openbsd

package state

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

var ErrLocked = errors.New("hukou state is locked by another process")

// Lock uses an atomic directory fallback on platforms without flock support.
// Unlike flock, a crashed process cannot leave the directory behind for the
// OS to clean up, so Acquire writes its pid inside the lock directory and,
// on contention, reclaims it when that pid is demonstrably dead.
type Lock struct {
	path string
}

const lockPidFileName = "pid"

func Acquire(path string) (*Lock, error) {
	lockDir := path + ".d"
	for attempt := 0; attempt < 2; attempt++ {
		err := os.Mkdir(lockDir, 0o700)
		if err == nil {
			// Best effort: the pid file powers stale-lock reclamation on the
			// next contention; a lock without it is still a valid lock.
			_ = os.WriteFile(filepath.Join(lockDir, lockPidFileName), []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600)
			return &Lock{path: lockDir}, nil
		}
		if !os.IsExist(err) {
			return nil, err
		}
		if attempt == 0 && reclaimStaleLock(lockDir) {
			continue
		}
		return nil, fmt.Errorf("%w (lock directory: %s; remove it manually if the owning process is gone)", ErrLocked, lockDir)
	}
	return nil, fmt.Errorf("%w (lock directory: %s; remove it manually if the owning process is gone)", ErrLocked, lockDir)
}

// reclaimStaleLock removes the lock directory only when its recorded owner
// pid is demonstrably dead. Any doubt — missing or unparsable pid file, an
// unreliable probe — counts as "alive": the fallback lock errs toward
// blocking, never toward stealing a live lock.
func reclaimStaleLock(lockDir string) bool {
	data, err := os.ReadFile(filepath.Join(lockDir, lockPidFileName))
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return false
	}
	if processAlive(pid) {
		return false
	}
	return os.RemoveAll(lockDir) == nil
}

// processAlive probes pid with the zero-signal idiom: Signal(0) returns nil
// for a live process and ESRCH for a dead one. Every OTHER error — notably
// EWINDOWS on Windows, where FindProcess always succeeds and Signal supports
// only Kill — means "probe unsupported": treat the owner as ALIVE. The
// conservative direction is deliberate: a stale lock is merely annoying
// (manual cleanup, per the contention error message), a stolen live lock is
// data corruption. On Windows stale-lock reclamation therefore never fires.
func processAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = p.Signal(syscall.Signal(0))
	return err == nil || !errors.Is(err, syscall.ESRCH)
}

func (l *Lock) Release() error {
	if l == nil || l.path == "" {
		return nil
	}
	// Acquire wrote its pid file inside the lock directory, so the directory
	// is non-empty: remove the pid file first, then the directory itself.
	// Both errors are reported (never swallowed); the directory removal is
	// what decides whether Release is idempotent.
	pidErr := os.Remove(filepath.Join(l.path, lockPidFileName))
	if errors.Is(pidErr, os.ErrNotExist) {
		pidErr = nil
	}
	dirErr := os.Remove(l.path)
	if dirErr == nil {
		l.path = ""
	}
	return errors.Join(pidErr, dirErr)
}
