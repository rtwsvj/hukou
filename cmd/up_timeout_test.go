package cmd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rtwsvj/hukou/internal/manifest"
	"github.com/rtwsvj/hukou/internal/orchestrate"
	"github.com/rtwsvj/hukou/internal/orchestrate/plan"
	"github.com/rtwsvj/hukou/internal/output"
	"github.com/rtwsvj/hukou/internal/state"
)

func TestResolveUpTimeout(t *testing.T) {
	env := func(v string) func(string) string {
		return func(string) string { return v }
	}

	// Flag wins over the environment.
	if d, err := resolveUpTimeout(30*time.Minute, env("99m")); err != nil || d != 30*time.Minute {
		t.Fatalf("flag over env = %s, %v; want 30m, nil", d, err)
	}
	// Environment supplies the value when the flag is unset.
	if d, err := resolveUpTimeout(0, env("45m")); err != nil || d != 45*time.Minute {
		t.Fatalf("env fallback = %s, %v; want 45m, nil", d, err)
	}
	// Neither set: 0 (unset), left for the entry points to normalize.
	if d, err := resolveUpTimeout(0, env("")); err != nil || d != 0 {
		t.Fatalf("unset = %s, %v; want 0, nil", d, err)
	}
	// Invalid explicit values are errors, never a silent default.
	if _, err := resolveUpTimeout(0, env("bogus")); err == nil {
		t.Fatal("expected error for unparsable HUKOU_UP_TIMEOUT")
	}
	if _, err := resolveUpTimeout(0, env("-5m")); err == nil {
		t.Fatal("expected error for negative HUKOU_UP_TIMEOUT")
	}
	if _, err := resolveUpTimeout(-time.Minute, env("")); err == nil {
		t.Fatal("expected error for negative --timeout")
	}
}

func TestParseManagerTimeouts(t *testing.T) {
	// Valid entries, repeatable.
	got, err := parseManagerTimeouts([]string{"brew=45m", "npm=90s"})
	if err != nil {
		t.Fatalf("valid entries: %v", err)
	}
	if got["brew"] != 45*time.Minute || got["npm"] != 90*time.Second {
		t.Fatalf("parsed = %v", got)
	}

	// Unknown names reuse the registry's UnknownManagerError (no silent typo).
	_, err = parseManagerTimeouts([]string{"brwe=45m"})
	var unknown *orchestrate.UnknownManagerError
	if !errors.As(err, &unknown) {
		t.Fatalf("unknown name error = %v, want UnknownManagerError", err)
	}

	// The internal hukou step never runs through the executor: reject it.
	if _, err := parseManagerTimeouts([]string{"hukou=45m"}); err == nil {
		t.Fatal("expected error for the internal hukou step")
	}

	// Malformed and non-positive durations error out.
	for _, entry := range []string{"brew45m", "brew=", "=45m", "brew=abc", "brew=0s", "brew=-5m"} {
		if _, err := parseManagerTimeouts([]string{entry}); err == nil {
			t.Fatalf("expected error for %q", entry)
		}
	}
}

func TestEffectiveTimeout(t *testing.T) {
	// Unset base normalizes to the executor default (15m).
	if d := effectiveTimeout(upOptions{}, "brew"); d != 15*time.Minute {
		t.Fatalf("default = %s, want 15m", d)
	}
	// Base applies to every external manager.
	opts := upOptions{timeout: 30 * time.Minute}
	if d := effectiveTimeout(opts, "npm"); d != 30*time.Minute {
		t.Fatalf("base = %s, want 30m", d)
	}
	// A per-name override wins over the base, only for that name.
	opts.managerTimeouts = map[string]time.Duration{"brew": 45 * time.Minute}
	if d := effectiveTimeout(opts, "brew"); d != 45*time.Minute {
		t.Fatalf("override = %s, want 45m", d)
	}
	if d := effectiveTimeout(opts, "npm"); d != 30*time.Minute {
		t.Fatalf("override leaked to another manager: %s, want 30m", d)
	}
}

// TestUp_planShowsEffectiveTimeout: the dry-run surfaces the timeout each
// manager would run under — per-name override in the table, base value in
// JSON, and nothing for the internal hukou row.
func TestUp_planShowsEffectiveTimeout(t *testing.T) {
	dir := t.TempDir()
	writeExecutable(t, dir, "brew", "#!/bin/sh\n")
	writeExecutable(t, dir, "npm", "#!/bin/sh\n")

	opts := upOptions{
		dryRun:          true,
		timeout:         30 * time.Minute,
		managerTimeouts: map[string]time.Duration{"brew": 45 * time.Minute},
	}

	var table bytes.Buffer
	if err := doUpPlan(&table, opts, fakeLookPath(dir), fixtureInventory, forbidRunner(t)); err != nil {
		t.Fatal(err)
	}
	out := table.String()
	if !strings.Contains(out, "TIMEOUT") {
		t.Fatalf("table missing the TIMEOUT column:\n%s", out)
	}
	if !strings.Contains(out, "45m0s") || !strings.Contains(out, "30m0s") {
		t.Fatalf("table missing effective timeouts (45m brew override, 30m base):\n%s", out)
	}

	var js bytes.Buffer
	opts.json = true
	if err := doUpPlan(&js, opts, fakeLookPath(dir), fixtureInventory, forbidRunner(t)); err != nil {
		t.Fatal(err)
	}
	var doc plan.Document
	if err := json.Unmarshal(js.Bytes(), &doc); err != nil {
		t.Fatalf("decode plan: %v\n%s", err, js.String())
	}
	byName := map[string]plan.ManagerJSON{}
	for _, m := range doc.Managers {
		byName[m.Name] = m
	}
	if byName["brew"].Timeout != "45m0s" {
		t.Fatalf("brew timeout = %q, want 45m0s", byName["brew"].Timeout)
	}
	if byName["npm"].Timeout != "30m0s" {
		t.Fatalf("npm timeout = %q, want 30m0s", byName["npm"].Timeout)
	}
	if byName["hukou"].Timeout != "" {
		t.Fatalf("internal hukou row carries a timeout %q; it never runs through the executor", byName["hukou"].Timeout)
	}
}

// TestUp_managerTimeoutUnknownNameFailsDispatch drives the REAL cobra
// dispatch: a bad --manager-timeout fails before any plan or execution.
// (Duration and validity parsing itself is covered by the unit tests above;
// this proves the flag is wired into runUp.)
func TestUp_managerTimeoutUnknownNameFailsDispatch(t *testing.T) {
	t.Setenv("HUKOU_DATA_DIR", t.TempDir())
	upDryRun, upJSON, upOnly, upSkip = false, false, nil, nil
	upRetry, upTimeout, upManagerTimeouts = 0, 0, nil
	t.Cleanup(func() {
		upDryRun, upJSON, upOnly, upSkip = false, false, nil, nil
		upRetry, upTimeout, upManagerTimeouts = 0, 0, nil
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(os.Stdout)
		rootCmd.SetErr(os.Stderr)
	})

	var out, errb bytes.Buffer
	rootCmd.SetArgs([]string{"up", "--dry-run", "--manager-timeout", "brwe=45m"})
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&errb)
	err := rootCmd.Execute()
	var unknown *orchestrate.UnknownManagerError
	if !errors.As(err, &unknown) {
		t.Fatalf("dispatch error = %v, want UnknownManagerError", err)
	}
	if strings.Contains(out.String(), "dry run: nothing was executed or written") {
		t.Fatalf("plan rendered despite the invalid flag:\n%s", out.String())
	}
}

// TestUp_envTimeoutFeedsThePlan: with no --timeout flag, HUKOU_UP_TIMEOUT is
// the base timeout the dry-run plan renders for every external manager.
func TestUp_envTimeoutFeedsThePlan(t *testing.T) {
	t.Setenv("HUKOU_DATA_DIR", t.TempDir())
	t.Setenv("HUKOU_UP_TIMEOUT", "20m")
	upDryRun, upJSON, upOnly, upSkip = false, false, nil, nil
	upRetry, upTimeout, upManagerTimeouts = 0, 0, nil
	t.Cleanup(func() {
		upDryRun, upJSON, upOnly, upSkip = false, false, nil, nil
		upRetry, upTimeout, upManagerTimeouts = 0, 0, nil
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(os.Stdout)
		rootCmd.SetErr(os.Stderr)
	})

	var out, errb bytes.Buffer
	rootCmd.SetArgs([]string{"up", "--dry-run", "--json"})
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&errb)
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("dry-run with HUKOU_UP_TIMEOUT failed: %v\nstderr: %s", err, errb.String())
	}
	var doc plan.Document
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("decode plan: %v\n%s", err, out.String())
	}
	for _, m := range doc.Managers {
		if m.Name == "hukou" {
			continue // internal step: never bounded by the executor
		}
		if m.Timeout != "20m0s" {
			t.Fatalf("%s timeout = %q, want 20m0s from HUKOU_UP_TIMEOUT", m.Name, m.Timeout)
		}
	}
}

// ctxRecordingExecutor counts RunManager calls and records each ctx deadline,
// proving the retry loop's shared-budget wiring.
type ctxRecordingExecutor struct {
	calls     int
	deadlines []time.Time
	status    orchestrate.StepStatus
}

func (r *ctxRecordingExecutor) RunManager(ctx context.Context, name string, _ [][]string) orchestrate.StepResult {
	r.calls++
	if d, ok := ctx.Deadline(); ok {
		r.deadlines = append(r.deadlines, d)
	}
	return orchestrate.StepResult{Name: name, Status: r.status, ExitCode: 1}
}

// TestUp_retrySharesOneTimeoutBudget: every attempt of a manager runs under
// the SAME deadline (a retry restarts the work, not the clock), and a
// manager that hit its timeout is never retried.
func TestUp_retrySharesOneTimeoutBudget(t *testing.T) {
	binDir := t.TempDir()
	writeExecutable(t, binDir, "brew", "#!/bin/sh\n")
	same := fixtureInventoryReport()

	// Failing (not timing-out) manager: all 1+3 attempts share one deadline.
	failing := &ctxRecordingExecutor{status: orchestrate.StatusFailed}
	var called bool
	deps := stubRunDeps(t, fakeLookPath(binDir), failing, &called, same, same)
	var out, errb bytes.Buffer
	_ = doUpExecute(&out, &errb, upOptions{json: true, only: []string{"brew"}, retries: 3}, deps)
	if failing.calls != 4 {
		t.Fatalf("calls = %d, want 1+3 attempts", failing.calls)
	}
	if len(failing.deadlines) != 4 {
		t.Fatalf("no deadline propagated to attempts: %v", failing.deadlines)
	}
	for i, d := range failing.deadlines {
		if !d.Equal(failing.deadlines[0]) {
			t.Fatalf("attempt %d got a fresh deadline %v, want the shared %v", i, d, failing.deadlines[0])
		}
	}

	// A timed-out manager: zero retries despite --retry 3.
	timingOut := &ctxRecordingExecutor{status: orchestrate.StatusTimeout}
	deps = stubRunDeps(t, fakeLookPath(binDir), timingOut, &called, same, same)
	out.Reset()
	errb.Reset()
	_ = doUpExecute(&out, &errb, upOptions{json: true, only: []string{"brew"}, retries: 3}, deps)
	if timingOut.calls != 1 {
		t.Fatalf("timeout result was retried: calls = %d, want 1", timingOut.calls)
	}
}

func fixtureInventoryReport() output.Report {
	r, _ := fixtureInventory()
	return r
}

// TestUp_realRunFailsWhenUpLockHeld: a second concurrent `hukou up` run
// fails immediately on the up lock instead of interleaving with the first.
// (The contender must be a real subprocess: flock is per-process on macOS,
// so an in-process double-acquire would not contend.)
func TestUp_realRunFailsWhenUpLockHeld(t *testing.T) {
	if os.Getenv("HUKOU_UP_LOCK_HELPER") == "1" {
		lock, err := state.Acquire(os.Getenv("HUKOU_UP_LOCK_PATH"))
		if err != nil {
			os.Exit(2)
		}
		fmt.Println("locked")
		_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
		_ = lock.Release()
		return
	}

	binDir := t.TempDir()
	writeExecutable(t, binDir, "brew", "#!/bin/sh\n")
	same := fixtureInventoryReport()
	var called bool
	deps := stubRunDeps(t, fakeLookPath(binDir), &fakeExecutor{}, &called, same, same)
	// Pin the data root: stubRunDeps' default closure mints a fresh TempDir
	// per call, which would put the helper's and the run's locks in different
	// directories.
	dataDir := t.TempDir()
	deps.dataRoot = func() string { return dataDir }
	lockPath := filepath.Join(dataDir, "up.lock")

	helper := exec.Command(os.Args[0], "-test.run=^TestUp_realRunFailsWhenUpLockHeld$")
	helper.Env = append(os.Environ(), "HUKOU_UP_LOCK_HELPER=1", "HUKOU_UP_LOCK_PATH="+lockPath)
	stdin, err := helper.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := helper.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := helper.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = helper.Process.Kill()
		_ = helper.Wait()
	}()
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "locked" {
		t.Fatalf("helper did not acquire the up lock: line=%q err=%v", line, err)
	}

	var out, errb bytes.Buffer
	err = doUpExecute(&out, &errb, upOptions{json: true}, deps)
	if err == nil || !strings.Contains(err.Error(), "another `hukou up` is already running") {
		t.Fatalf("err = %v, want the concurrent-up refusal", err)
	}
	_ = stdin.Close()
}

// TestRunInternalHukouBudgetExpiredIsCanceled: the internal step's soft
// budget reclassifies an over-running (but eventually successful) step as
// canceled — boundary semantics, never a mid-transaction interrupt.
func TestRunInternalHukouBudgetExpiredIsCanceled(t *testing.T) {
	step := func(ctx context.Context, stdout, stderr io.Writer) error {
		time.Sleep(200 * time.Millisecond)
		return nil
	}
	res := runInternalHukou(context.Background(), io.Discard, io.Discard, step, 50*time.Millisecond)
	if res.Status != orchestrate.StatusCanceled {
		t.Fatalf("status = %s, want canceled after the soft budget expired", res.Status)
	}
}

// TestDoUpgradeCtxBoundarySkipsRemainingTools: with an already-expired ctx,
// the upgrade batch reports every tool canceled at the boundary, touches
// nothing, and returns no failure (the up layer reclassifies the step).
func TestDoUpgradeCtxBoundarySkipsRemainingTools(t *testing.T) {
	entries := []manifest.Entry{
		policyFixtureEntry(t, "toola", manifest.DefaultUpdatePolicy()),
		policyFixtureEntry(t, "toolb", manifest.DefaultUpdatePolicy()),
	}
	root, _ := writePolicyFixture(t, entries)
	t.Setenv("HUKOU_DATA_DIR", root)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // budget already spent before the batch starts

	var out, errb bytes.Buffer
	err := doUpgradeCtx(ctx, &out, &errb, nil, true, false, "", nil, false)
	if err != nil {
		t.Fatalf("boundary cancellation must not be a batch failure: %v", err)
	}
	text := out.String()
	if !strings.Contains(text, "canceled toola") || !strings.Contains(text, "canceled toolb") {
		t.Fatalf("remaining tools not reported canceled:\n%s", text)
	}
}

// TestUp_unknownOnlyNameCreatesNothing: flag validation precedes the up lock,
// so a typo'd filter errors without creating the data root or a lock file.
func TestUp_unknownOnlyNameCreatesNothing(t *testing.T) {
	binDir := t.TempDir()
	writeExecutable(t, binDir, "brew", "#!/bin/sh\n")
	same := fixtureInventoryReport()
	var called bool
	deps := stubRunDeps(t, fakeLookPath(binDir), &fakeExecutor{}, &called, same, same)
	dataDir := filepath.Join(t.TempDir(), "data") // must never be created
	deps.dataRoot = func() string { return dataDir }

	var out, errb bytes.Buffer
	err := doUpExecute(&out, &errb, upOptions{only: []string{"bogus"}}, deps)
	var unknown *orchestrate.UnknownManagerError
	if !errors.As(err, &unknown) {
		t.Fatalf("err = %v, want UnknownManagerError", err)
	}
	if _, statErr := os.Lstat(dataDir); !os.IsNotExist(statErr) {
		t.Fatalf("data root created by a rejected invocation: %v", statErr)
	}
}
