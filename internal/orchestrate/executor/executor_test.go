package executor

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/rtwsvj/hukou/internal/orchestrate"
)

// writeScript writes an executable shell script into dir and returns its path.
func writeScript(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatalf("write script %s: %v", name, err)
	}
	return p
}

func skipOnWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture managers are POSIX shell scripts")
	}
}

// lockedBuf is a concurrency-safe buffer for capturing streamed output from the
// executor's stdout/stderr goroutines.
type lockedBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuf) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}
func (b *lockedBuf) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestRunManager_SuccessStreamsPrefixedOutput(t *testing.T) {
	skipOnWindows(t)
	dir := t.TempDir()
	ok := writeScript(t, dir, "ok.sh", "#!/bin/sh\necho hello-stdout\necho hello-stderr 1>&2\nexit 0\n")

	var out, errb lockedBuf
	e := New(&out, &errb)
	e.Timeout = 5 * time.Second

	res := e.RunManager(context.Background(), "brew", [][]string{{ok}})
	if res.Status != orchestrate.StatusOK {
		t.Fatalf("status = %s, want ok (err=%v)", res.Status, res.Err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit = %d, want 0", res.ExitCode)
	}
	if !strings.Contains(out.String(), "[brew] hello-stdout") {
		t.Fatalf("stdout missing prefixed line:\n%s", out.String())
	}
	if !strings.Contains(errb.String(), "[brew] hello-stderr") {
		t.Fatalf("stderr missing prefixed line:\n%s", errb.String())
	}
}

func TestRunManager_FailingCommandStopsChainAndReportsExit(t *testing.T) {
	skipOnWindows(t)
	dir := t.TempDir()
	fail := writeScript(t, dir, "fail.sh", "#!/bin/sh\necho boom 1>&2\nexit 7\n")
	sentinel := writeScript(t, dir, "sentinel.sh", "#!/bin/sh\necho SHOULD-NOT-RUN\nexit 0\n")

	var out, errb lockedBuf
	e := New(&out, &errb)
	e.Timeout = 5 * time.Second

	// Two-step manager: the first fails, so the second must never run (&& chain).
	res := e.RunManager(context.Background(), "npm", [][]string{{fail}, {sentinel}})
	if res.Status != orchestrate.StatusFailed {
		t.Fatalf("status = %s, want failed", res.Status)
	}
	if res.ExitCode != 7 {
		t.Fatalf("exit = %d, want 7", res.ExitCode)
	}
	if res.Err == nil {
		t.Fatal("expected a non-nil error on failure")
	}
	if strings.Contains(out.String(), "SHOULD-NOT-RUN") {
		t.Fatalf("second command ran after the first failed:\n%s", out.String())
	}
	if !strings.Contains(errb.String(), "[npm] boom") {
		t.Fatalf("stderr missing failing command output:\n%s", errb.String())
	}
}

func TestRunManager_TimeoutKillsHungManager(t *testing.T) {
	skipOnWindows(t)
	dir := t.TempDir()
	// Sleeps far longer than the per-manager timeout; must be killed.
	slow := writeScript(t, dir, "slow.sh", "#!/bin/sh\nsleep 30\n")

	var out, errb lockedBuf
	e := New(&out, &errb)
	e.Timeout = 150 * time.Millisecond
	e.TermGrace = 200 * time.Millisecond
	e.KillGrace = 200 * time.Millisecond

	start := time.Now()
	res := e.RunManager(context.Background(), "rustup", [][]string{{slow}})
	elapsed := time.Since(start)

	if res.Status != orchestrate.StatusTimeout {
		t.Fatalf("status = %s, want timeout (err=%v)", res.Status, res.Err)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("timeout did not kill promptly: took %s", elapsed)
	}
	if res.Err == nil {
		t.Fatal("expected a timeout error")
	}
}

// TestRunManager_TimeoutKillsWholeProcessTree is the process-tree acceptance:
// a fake manager spawns a background grandchild that ignores SIGTERM, keeps
// the inherited output pipes open (as real manager helpers do), and writes a
// heartbeat file forever. The per-manager timeout must kill the ENTIRE process
// group — SIGTERM first, then the SIGKILL escalation, which fires pre-reap
// because the grandchild's open pipe keeps Wait draining (TermGrace <
// KillGrace) — so the heartbeat stops. Killing only the direct child would
// leave the grandchild beating and fail this test.
func TestRunManager_TimeoutKillsWholeProcessTree(t *testing.T) {
	skipOnWindows(t)
	dir := t.TempDir()
	hb := filepath.Join(dir, "heartbeat")
	// The grandchild ignores TERM (trap '' TERM) and inherits the script's
	// stdout/stderr pipes (no redirect), so only the group-wide SIGKILL
	// escalation can stop it — and the held pipe guarantees that escalation
	// happens before the child is reaped (see proc_unix.go).
	body := fmt.Sprintf(`#!/bin/sh
( trap '' TERM; while :; do echo beat >> %q; sleep 0.05; done ) &
sleep 30
`, hb)
	tree := writeScript(t, dir, "treeslow.sh", body)

	var out, errb lockedBuf
	e := New(&out, &errb)
	// Generous timeout: the first exec of a freshly written script can take
	// hundreds of ms on macOS (syspolicyd scan); the grandchild must have time
	// to start beating before the timeout fires. TermGrace < KillGrace so the
	// group SIGKILL fires while Wait is still draining the held pipe.
	e.Timeout = 1500 * time.Millisecond
	e.TermGrace = 250 * time.Millisecond
	e.KillGrace = 800 * time.Millisecond

	res := e.RunManager(context.Background(), "brew", [][]string{{tree}})
	if res.Status != orchestrate.StatusTimeout {
		t.Fatalf("status = %s, want timeout (err=%v)", res.Status, res.Err)
	}

	// The grandchild must have run at all (heartbeats were written)...
	if size := fileSize(t, hb); size == 0 {
		t.Fatal("grandchild never produced a heartbeat; fixture is broken")
	}
	// ...and must now be dead: the heartbeat file stops growing. Poll until it
	// freezes (kill delivery is asynchronous), then require it to stay frozen.
	deadline := time.Now().Add(5 * time.Second)
	for {
		s1 := fileSize(t, hb)
		time.Sleep(300 * time.Millisecond)
		if fileSize(t, hb) == s1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("grandchild kept writing heartbeats after the process-group kill")
		}
	}
	stable := fileSize(t, hb)
	time.Sleep(400 * time.Millisecond)
	if got := fileSize(t, hb); got != stable {
		t.Fatalf("heartbeat resumed after apparent stop: %d -> %d bytes", stable, got)
	}
}

func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatal(err)
	}
	return info.Size()
}

func TestRunManager_MultiStepChainRunsInOrder(t *testing.T) {
	skipOnWindows(t)
	dir := t.TempDir()
	stamp := filepath.Join(dir, "stamp")
	first := writeScript(t, dir, "first.sh", "#!/bin/sh\necho first >> "+stamp+"\n")
	second := writeScript(t, dir, "second.sh", "#!/bin/sh\necho second >> "+stamp+"\n")

	var out, errb lockedBuf
	e := New(&out, &errb)
	e.Timeout = 5 * time.Second

	res := e.RunManager(context.Background(), "brew", [][]string{{first}, {second}})
	if res.Status != orchestrate.StatusOK {
		t.Fatalf("status = %s, want ok (err=%v)", res.Status, res.Err)
	}
	data, err := os.ReadFile(stamp)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(data)); got != "first\nsecond" {
		t.Fatalf("commands ran out of order: %q", got)
	}
}

func TestRunManager_ParentCancellationIsFailure(t *testing.T) {
	skipOnWindows(t)
	dir := t.TempDir()
	slow := writeScript(t, dir, "slow.sh", "#!/bin/sh\nsleep 30\n")

	var out, errb lockedBuf
	e := New(&out, &errb)
	e.Timeout = 10 * time.Second
	e.KillGrace = 200 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	res := e.RunManager(ctx, "uv", [][]string{{slow}})
	// A cancelled parent is a failure, not a per-manager timeout.
	if res.Status != orchestrate.StatusFailed {
		t.Fatalf("status = %s, want failed on parent cancel", res.Status)
	}
}

func TestNew_NilWritersDoNotPanic(t *testing.T) {
	skipOnWindows(t)
	dir := t.TempDir()
	ok := writeScript(t, dir, "ok.sh", "#!/bin/sh\necho hi\n")
	e := New(nil, nil)
	e.Timeout = 5 * time.Second
	if res := e.RunManager(context.Background(), "brew", [][]string{{ok}}); res.Status != orchestrate.StatusOK {
		t.Fatalf("status = %s, want ok", res.Status)
	}
}

// TestRunManager_GroupKillReapRaceStress hammers the cancellation path with
// short-lived children so the escalation-timer callback and the Wait/reap
// path collide as often as possible (TermGrace of 1ms, timeout close to the
// children's own lifetimes), while a sentinel process sits in its own process
// group for the whole run. Afterwards the sentinel must still be alive and
// un-exited: a stray or mis-addressed group signal (stale pgid variable,
// signaling after reap, pgid 0/-1 bugs) would have hit it or shown up under
// the race detector.
//
// Verification boundary, stated honestly: the sentinel occupies one fixed
// process group — it cannot be made to occupy a pgid the kernel just recycled
// from a reaped child, so this test detects gross stray-signal and data-race
// bugs, not kernel-level pgid reuse itself. Reuse safety is instead the
// protocol invariant reviewed in proc_unix.go: no code path signals -pgid
// after the child is reaped.
func TestRunManager_GroupKillReapRaceStress(t *testing.T) {
	skipOnWindows(t)
	dir := t.TempDir()
	quick := writeScript(t, dir, "quick.sh", "#!/bin/sh\nexit 0\n")
	brief := writeScript(t, dir, "brief.sh", "#!/bin/sh\nsleep 0.02\n")
	slow := writeScript(t, dir, "slow.sh", "#!/bin/sh\nsleep 1\n")

	sentinel := exec.Command("sleep", "30")
	sentinel.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := sentinel.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = sentinel.Process.Kill()
		_, _ = sentinel.Process.Wait()
	})

	for i := 0; i < 12; i++ {
		for _, script := range []string{quick, brief, slow} {
			var out, errb lockedBuf
			e := New(&out, &errb)
			e.Timeout = 25 * time.Millisecond
			e.TermGrace = time.Millisecond
			e.KillGrace = 100 * time.Millisecond
			res := e.RunManager(context.Background(), "stress", [][]string{{script}})
			switch res.Status {
			case orchestrate.StatusOK, orchestrate.StatusFailed, orchestrate.StatusTimeout:
			default:
				t.Fatalf("iteration %d: unexpected status %q", i, res.Status)
			}
		}
	}

	// The sentinel must be alive AND un-exited. syscall.Kill(pid, 0) alone
	// would also succeed for a zombie, so use a non-blocking wait: WNOHANG
	// returning 0 means the child has not exited.
	var ws syscall.WaitStatus
	wpid, err := syscall.Wait4(sentinel.Process.Pid, &ws, syscall.WNOHANG, nil)
	if err != nil {
		t.Fatalf("probe sentinel: %v", err)
	}
	if wpid != 0 {
		t.Fatalf("sentinel process exited during the stress run (status %v): a stray group signal killed it", ws)
	}
}
