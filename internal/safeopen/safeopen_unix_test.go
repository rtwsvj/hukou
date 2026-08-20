//go:build unix

package safeopen

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestOpenRegularFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "bin")
	if err := os.WriteFile(p, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := Open(p)
	if err != nil {
		t.Fatalf("Open regular file: %v", err)
	}
	_ = f.Close()
}

// TestOpenFIFOFailsClosedPromptly: opening a FIFO must return an error
// immediately — a plain os.Open would block forever waiting for a writer.
func TestOpenFIFOFailsClosedPromptly(t *testing.T) {
	p := filepath.Join(t.TempDir(), "fifo")
	if err := syscall.Mkfifo(p, 0o600); err != nil {
		t.Fatal(err)
	}
	type result struct {
		f   *os.File
		err error
	}
	done := make(chan result, 1)
	go func() {
		f, err := Open(p)
		done <- result{f, err}
	}()
	select {
	case r := <-done:
		if r.err == nil {
			_ = r.f.Close()
			t.Fatal("Open on a FIFO succeeded; want fail-closed error")
		}
		if !strings.Contains(r.err.Error(), "not a regular file") {
			t.Fatalf("error = %v, want the not-a-regular-file refusal", r.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Open blocked on a FIFO for 5s; O_NONBLOCK is not working")
	}
}
