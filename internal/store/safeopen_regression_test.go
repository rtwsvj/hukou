package store

import (
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// runWithTimeoutGuard runs fn on a goroutine and fails the test if it has not
// returned within the bound. A plain os.Open wedged on a FIFO would otherwise
// hang the whole suite instead of failing this one test.
func runWithTimeoutGuard(t *testing.T, name string, fn func() error) error {
	t.Helper()
	type result struct{ err error }
	done := make(chan result, 1)
	go func() { done <- result{fn()} }()
	select {
	case r := <-done:
		return r.err
	case <-time.After(5 * time.Second):
		t.Fatalf("%s blocked for 5s; the source open is not fail-closed", name)
		return nil
	}
}

// TestAtomicCopyFileFIFOSourceFailsFast: atomicCopyFileWithMode has no
// pre-open regular-file check of its own (callers race with a swap between
// their Lstat and the open), so a FIFO reaching the open must be rejected by
// safeopen instead of blocking forever while the mutation lock is held.
func TestAtomicCopyFileFIFOSourceFailsFast(t *testing.T) {
	root := t.TempDir()
	s := &Store{Root: root}
	fifo := filepath.Join(t.TempDir(), "swapped-bin")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "live")
	err := runWithTimeoutGuard(t, "atomicCopyFileWithMode", func() error {
		return atomicCopyFileWithMode(s.durability(), fifo, dst, filepath.Join(root, ".tmp"), true)
	})
	if err == nil {
		t.Fatal("atomicCopyFileWithMode on a FIFO source succeeded; want fail-closed error")
	}
	if !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("error = %v, want the not-a-regular-file refusal", err)
	}
}

// TestPutFIFOSourceFailsFast: Put's initial Stat rejects a FIFO outright; the
// assertion is that the rejection stays prompt even though the later source
// open now goes through safeopen, so a swap racing past the Stat cannot turn
// into a lock-held wedge.
func TestPutFIFOSourceFailsFast(t *testing.T) {
	s := &Store{Root: t.TempDir()}
	fifo := filepath.Join(t.TempDir(), "mybin")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	err := runWithTimeoutGuard(t, "PutWithDigest", func() error {
		_, err := s.PutWithDigest("tool", "v1.0", fifo)
		return err
	})
	if err == nil {
		t.Fatal("PutWithDigest on a FIFO source succeeded; want fail-closed error")
	}
	if !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("error = %v, want the not-a-regular-file refusal", err)
	}
}
