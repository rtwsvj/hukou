//go:build unix

package transaction

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// beginBlockedMidCapture starts a REAL Begin parked deterministically
// mid-capture via the TestBeforeCaptureHook seam: the .building-* journal
// directory exists but pending-* is not yet published. (The old fixture
// parked the capture on a writerless FIFO; sha256File now fails closed on
// non-regular targets via safeopen instead of blocking, so the hook is the
// parking mechanism.) The returned unblock releases the capture so Begin can
// publish.
func beginBlockedMidCapture(t *testing.T, root string) (result chan error, unblock func()) {
	t.Helper()
	aux := t.TempDir()
	live := filepath.Join(aux, "live")
	if err := os.WriteFile(live, []byte("live"), 0o755); err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	oldHook := TestBeforeCaptureHook
	TestBeforeCaptureHook = func(string) {
		close(entered)
		<-release
	}
	t.Cleanup(func() { TestBeforeCaptureHook = oldHook })
	result = make(chan error, 1)
	go func() {
		_, err := Begin(root, "adopt", "tool", []Spec{{
			Role: "live", Path: live, After: Unchanged(),
		}})
		result <- err
	}()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("Begin never reached the capture hook")
	}
	return result, func() { close(release) }
}

// waitForBuildingJournal polls until the journal inventory reports exactly one
// .building-* entry, failing the test on timeout.
func waitForBuildingJournal(t *testing.T, root string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		status, err := Inspect(root)
		if err != nil {
			t.Fatal(err)
		}
		if len(status.Building) == 1 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for a building journal, status=%+v", status)
		}
		time.Sleep(time.Millisecond)
	}
}

// The race rationale for keeping building-* fail-closed on the
// read path, exercised with a genuinely ACTIVE Begin held mid-capture in a
// goroutine. A point-in-time check cannot cover the caller's read cycle, so
// the .building-* window of a live writer must never be reported as harmless.
func TestCheckReadableFailsClosedDuringActiveBegin(t *testing.T) {
	root := t.TempDir()
	result, unblock := beginBlockedMidCapture(t, root)

	// Begin is deterministically parked inside its capture right now, so the
	// .building-* entry belongs to an active writer, not abandoned residue.
	notes, err := CheckReadable(root)
	var pendingErr *PendingError
	if !errors.As(err, &pendingErr) {
		t.Fatalf("active building journal must fail closed, got notes=%v err=%v", notes, err)
	}
	if notes != nil {
		t.Fatalf("blocked read must not return notes, got %v", notes)
	}

	unblock()
	if err := <-result; err != nil {
		t.Fatalf("Begin should publish once the capture unblocks: %v", err)
	}

	// The same writer has now published pending-*: still fail-closed.
	notes, err = CheckReadable(root)
	if !errors.As(err, &pendingErr) {
		t.Fatalf("published pending journal must fail closed, got notes=%v err=%v", notes, err)
	}
}

// TestBeginFailsClosedOnFIFOTarget: a participant path whose symlink target
// is a writerless FIFO can no longer park Begin forever — the capture fails
// closed immediately (safeopen), reporting the non-regular target.
func TestBeginFailsClosedOnFIFOTarget(t *testing.T) {
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

	done := make(chan error, 1)
	go func() {
		_, err := Begin(root, "adopt", "tool", []Spec{{
			Role: "live", Path: live, After: Unchanged(),
		}})
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "not a regular file") {
			t.Fatalf("err = %v, want the fail-closed not-a-regular-file refusal", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Begin blocked on a FIFO target for 5s; safeopen is not working")
	}
}

// A REAL Begin killed mid-flight (SIGKILL, so its cleanup defer
// never runs) leaves .building-* residue; the read path must stay fail-closed
// on it because such residue is indistinguishable from an active writer.
func TestCheckReadableFailsClosedAfterBeginCrash(t *testing.T) {
	root := t.TempDir()
	aux := t.TempDir()
	live := filepath.Join(aux, "live")
	if err := os.WriteFile(live, []byte("live"), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestBeginCrashHelper$")
	cmd.Env = append(os.Environ(),
		"HUKOU_TXN_BEGIN_CRASH_HELPER=1",
		"HUKOU_TXN_BEGIN_CRASH_ROOT="+root,
		"HUKOU_TXN_BEGIN_CRASH_LIVE="+live,
	)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	killed := false
	defer func() {
		if !killed {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()

	waitForBuildingJournal(t, root)

	// The helper is parked inside Begin's capture (TestBeforeCaptureHook);
	// SIGKILL prevents Begin's building-directory cleanup defer from running.
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	err := cmd.Wait()
	killed = true
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("helper was not killed: %v", err)
	}

	status, err := Inspect(root)
	if err != nil || len(status.Building) != 1 {
		t.Fatalf("expected abandoned building residue, got %+v err=%v", status, err)
	}
	notes, err := CheckReadable(root)
	var pendingErr *PendingError
	if !errors.As(err, &pendingErr) {
		t.Fatalf("crashed Begin residue must fail closed, got notes=%v err=%v", notes, err)
	}
	if notes != nil {
		t.Fatalf("blocked read must not return notes, got %v", notes)
	}
}

// TestBeginCrashHelper is launched as a subprocess above. It parks a real
// Begin inside its before-state capture (TestBeforeCaptureHook blocks
// forever) so the parent can SIGKILL it while the .building-* journal
// exists, reproducing a crash mid-Begin.
func TestBeginCrashHelper(t *testing.T) {
	if os.Getenv("HUKOU_TXN_BEGIN_CRASH_HELPER") != "1" {
		return
	}
	TestBeforeCaptureHook = func(string) { select {} }
	_, _ = Begin(
		os.Getenv("HUKOU_TXN_BEGIN_CRASH_ROOT"),
		"adopt", "tool",
		[]Spec{{
			Role:  "live",
			Path:  os.Getenv("HUKOU_TXN_BEGIN_CRASH_LIVE"),
			After: Unchanged(),
		}},
	)
	// Never reached in the crash scenario: the parent kills this process while
	// Begin is parked in the capture hook. Exit non-zero defensively if it is.
	os.Exit(3)
}
