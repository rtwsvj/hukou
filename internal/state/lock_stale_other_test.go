//go:build !darwin && !linux && !freebsd && !netbsd && !openbsd

package state

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// TestAcquireReclaimsStaleLock: a lock directory whose recorded owner pid is
// dead is reclaimed instead of wedging every later Acquire. On Windows the
// zero-signal probe is unsupported (Signal accepts only Kill), so
// processAlive conservatively reports every owner as alive and the test
// skips — reclamation never fires there by design.
func TestAcquireReclaimsStaleLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.lock")
	lockDir := path + ".d"
	if err := os.Mkdir(lockDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// A pid that cannot be alive (far above any plausible allocator range).
	deadPid := 1 << 22
	if processAlive(deadPid) {
		t.Skip("the liveness probe is unsupported on this platform; reclamation is disabled by design")
	}
	if err := os.WriteFile(filepath.Join(lockDir, lockPidFileName), []byte(strconv.Itoa(deadPid)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	lock, err := Acquire(path)
	if err != nil {
		t.Fatalf("stale lock was not reclaimed: %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatal(err)
	}
}

// TestAcquireKeepsLiveLock: a lock directory whose recorded owner is the
// current process (demonstrably alive) is never stolen.
func TestAcquireKeepsLiveLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.lock")
	lockDir := path + ".d"
	if err := os.Mkdir(lockDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lockDir, lockPidFileName), []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Acquire(path)
	if !errors.Is(err, ErrLocked) {
		t.Fatalf("want ErrLocked for a live owner, got %v", err)
	}
	if _, statErr := os.Lstat(lockDir); statErr != nil {
		t.Fatalf("live lock directory was removed: %v", statErr)
	}
}
