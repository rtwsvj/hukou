//go:build unix

package provenance

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rtwsvj/hukou/internal/scan"
	statejournal "github.com/rtwsvj/hukou/internal/transaction"
)

// Card A rework: while another writer's REAL Begin is in flight (held
// mid-capture on a writerless FIFO behind a symlink), its .building-* window
// must degrade the hukou detector exactly like pending residue. A single
// point-in-time check cannot cover the scan's read cycle, so an active
// writer's building journal is never treated as harmless.
func TestHukouDetectorFailsClosedDuringActiveBegin(t *testing.T) {
	root := t.TempDir()
	aux := t.TempDir()
	fifo := filepath.Join(aux, "fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	live := filepath.Join(aux, "live")
	if err := os.Symlink(fifo, live); err != nil {
		t.Fatal(err)
	}

	begun := make(chan error, 1)
	go func() {
		_, err := statejournal.Begin(root, "adopt", "tool", []statejournal.Spec{{
			Role: "live", Path: live, After: statejournal.Unchanged(),
		}})
		begun <- err
	}()

	deadline := time.Now().Add(10 * time.Second)
	for {
		status, err := statejournal.Inspect(root)
		if err != nil {
			t.Fatal(err)
		}
		if len(status.Building) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for a building journal, status=%+v", status)
		}
		time.Sleep(time.Millisecond)
	}

	// Begin is deterministically parked inside its capture right now.
	d := NewHukouDetector()
	err := d.Load(Env{HukouManifest: filepath.Join(root, "manifest.json")})
	if err == nil || !strings.Contains(err.Error(), "unfinished transaction") {
		t.Fatalf("active building journal must degrade the detector, got %v", err)
	}
	if d.Match(scan.Binary{Path: "/usr/local/bin/tool"}) != nil {
		t.Fatal("active building state must not produce hukou attribution")
	}
	if len(d.Notes()) != 0 {
		t.Fatalf("degraded load must not emit advisory notes, got %v", d.Notes())
	}

	// Unblock the capture: open and close the FIFO's write end (EOF).
	w, err := os.OpenFile(fifo, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	if err := <-begun; err != nil {
		t.Fatalf("Begin should publish once the capture unblocks: %v", err)
	}

	// The writer has published pending-*: the detector must stay degraded.
	err = d.Load(Env{HukouManifest: filepath.Join(root, "manifest.json")})
	if err == nil || !strings.Contains(err.Error(), "unfinished transaction") {
		t.Fatalf("published pending journal must degrade the detector, got %v", err)
	}
}
