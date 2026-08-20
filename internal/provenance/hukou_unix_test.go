//go:build unix

package provenance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rtwsvj/hukou/internal/scan"
	statejournal "github.com/rtwsvj/hukou/internal/transaction"
)

// While another writer's REAL Begin is in flight (parked mid-capture via the
// transaction package's TestBeforeCaptureHook seam — the old writerless-FIFO
// fixture no longer blocks now that the journal hashes via safeopen), its
// .building-* window must degrade the hukou detector exactly like pending
// residue. A single point-in-time check cannot cover the scan's read cycle,
// so an active writer's building journal is never treated as harmless.
func TestHukouDetectorFailsClosedDuringActiveBegin(t *testing.T) {
	root := t.TempDir()
	aux := t.TempDir()
	live := filepath.Join(aux, "live")
	if err := os.WriteFile(live, []byte("live"), 0o755); err != nil {
		t.Fatal(err)
	}

	entered := make(chan struct{})
	release := make(chan struct{})
	oldHook := statejournal.TestBeforeCaptureHook
	statejournal.TestBeforeCaptureHook = func(string) {
		close(entered)
		<-release
	}
	t.Cleanup(func() { statejournal.TestBeforeCaptureHook = oldHook })

	begun := make(chan error, 1)
	go func() {
		_, err := statejournal.Begin(root, "adopt", "tool", []statejournal.Spec{{
			Role: "live", Path: live, After: statejournal.Unchanged(),
		}})
		begun <- err
	}()

	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("Begin never reached the capture hook")
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

	// Unblock the capture: Begin publishes pending-*.
	close(release)
	if err := <-begun; err != nil {
		t.Fatalf("Begin should publish once the capture unblocks: %v", err)
	}

	// The writer has published pending-*: the detector must stay degraded.
	err = d.Load(Env{HukouManifest: filepath.Join(root, "manifest.json")})
	if err == nil || !strings.Contains(err.Error(), "unfinished transaction") {
		t.Fatalf("published pending journal must degrade the detector, got %v", err)
	}
}
