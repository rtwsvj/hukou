package executor

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
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

// TestRunManager_TimeoutKillsHungManager: a manager that sleeps far past its
// per-manager timeout is killed and reported as StatusTimeout, promptly. (On
// unix the kill covers the whole process group; that guarantee has its own
// test in procgroup_unix_test.go.)
func TestRunManager_TimeoutKillsHungManager(t *testing.T) {
	skipOnWindows(t)
	dir := t.TempDir()
	slow := writeScript(t, dir, "slow.sh", "#!/bin/sh\nexec sleep 30\n")

	var out, errb lockedBuf
	e := New(&out, &errb)
	e.Timeout = 150 * time.Millisecond

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

func TestTimeoutFor_ResolutionOrder(t *testing.T) {
	e := New(nil, nil)
	if got := e.TimeoutFor("brew"); got != DefaultTimeout {
		t.Fatalf("zero-value resolution = %s, want DefaultTimeout %s", got, DefaultTimeout)
	}
	e.Timeout = 5 * time.Minute
	if got := e.TimeoutFor("brew"); got != 5*time.Minute {
		t.Fatalf("executor-wide timeout = %s, want 5m", got)
	}
	e.Timeouts = map[string]time.Duration{"brew": 45 * time.Minute, "npm": 0}
	if got := e.TimeoutFor("brew"); got != 45*time.Minute {
		t.Fatalf("per-name override = %s, want 45m", got)
	}
	// A non-positive override is treated as unset and falls through.
	if got := e.TimeoutFor("npm"); got != 5*time.Minute {
		t.Fatalf("non-positive override = %s, want executor-wide 5m", got)
	}
	// Other names are untouched by the override map.
	if got := e.TimeoutFor("uv"); got != 5*time.Minute {
		t.Fatalf("unrelated manager = %s, want executor-wide 5m", got)
	}
}

// TestRunManager_PerNameTimeoutOverride: a manager-specific timeout bounds
// that manager alone — a hung brew with a 45m-style override dies on the
// override, not on the executor-wide budget.
func TestRunManager_PerNameTimeoutOverride(t *testing.T) {
	skipOnWindows(t)
	dir := t.TempDir()
	slow := writeScript(t, dir, "slow.sh", "#!/bin/sh\nexec sleep 30\n")

	var out, errb lockedBuf
	e := New(&out, &errb)
	e.Timeout = 10 * time.Second // would let the sleep run far longer
	e.Timeouts = map[string]time.Duration{"brew": 150 * time.Millisecond}

	start := time.Now()
	res := e.RunManager(context.Background(), "brew", [][]string{{slow}})
	if res.Status != orchestrate.StatusTimeout {
		t.Fatalf("status = %s, want timeout (err=%v)", res.Status, res.Err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("per-name override did not kill promptly: took %s", elapsed)
	}
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

// TestRunManager_ParentCancellationIsCanceled: a cancelled parent context
// (a Ctrl-C in production) surfaces as StatusCanceled, distinct from a
// per-manager timeout.
func TestRunManager_ParentCancellationIsCanceled(t *testing.T) {
	skipOnWindows(t)
	dir := t.TempDir()
	slow := writeScript(t, dir, "slow.sh", "#!/bin/sh\nexec sleep 30\n")

	var out, errb lockedBuf
	e := New(&out, &errb)
	e.Timeout = 10 * time.Second

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	res := e.RunManager(ctx, "uv", [][]string{{slow}})
	if res.Status != orchestrate.StatusCanceled {
		t.Fatalf("status = %s, want canceled on parent cancel", res.Status)
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

// TestRunManager_SharedDeadlineClassifiedAsTimeout: when the caller supplies
// the deadline (a retry loop sharing one budget across attempts), hitting it
// is a timeout, not a cancellation — classifyCtx distinguishes the two.
func TestRunManager_SharedDeadlineClassifiedAsTimeout(t *testing.T) {
	skipOnWindows(t)
	dir := t.TempDir()
	slow := writeScript(t, dir, "slow.sh", "#!/bin/sh\nexec sleep 30\n")

	var out, errb lockedBuf
	e := New(&out, &errb)
	e.Timeout = time.Minute // the caller's earlier deadline fires first

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	res := e.RunManager(ctx, "brew", [][]string{{slow}})
	if res.Status != orchestrate.StatusTimeout {
		t.Fatalf("status = %s, want timeout for a caller-supplied deadline (err=%v)", res.Status, res.Err)
	}
}
