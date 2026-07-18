package cmd

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/rtwsvj/hukou/internal/orchestrate"
)

// fatalExecutor fails the test if its RunManager is ever called.
type fatalExecutor struct{ t *testing.T }

func (f fatalExecutor) RunManager(context.Context, string, [][]string) orchestrate.StepResult {
	f.t.Helper()
	f.t.Fatal("dry-run dispatch invoked the executor; it must never")
	return orchestrate.StepResult{}
}

// TestDryRunDispatchNeverConstructsOrCallsExecutor drives the REAL cobra
// dry-run dispatch — rootCmd.Execute with `up --dry-run`, i.e. runUp itself,
// not a hand-rolled call — after swapping the production executor constructor
// (newStepExecutor) for one that both counts constructions and returns a
// fatal-on-call fake. Because the dry-run path routes through doUpPlan and
// never productionUpDeps, the constructor must fire zero times and the fake's
// RunManager must never run. This is the injectable-dispatch half of the U2
// boundary guarantee; the mechanical half is the repo-wide fence
// (internal/orchestrate/execution_fence_test.go).
func TestDryRunDispatchNeverConstructsOrCallsExecutor(t *testing.T) {
	// Sandbox the data dir; a dry run creates nothing, but keep the host clean.
	t.Setenv("HUKOU_DATA_DIR", filepath.Join(t.TempDir(), "data"))

	var constructed int32
	origNew := newStepExecutor
	newStepExecutor = func(streamOut, stderr io.Writer) orchestrate.StepExecutor {
		atomic.AddInt32(&constructed, 1)
		return fatalExecutor{t}
	}
	t.Cleanup(func() { newStepExecutor = origNew })

	// Reset the package-level up flags cobra may have left set by other tests,
	// and restore rootCmd's I/O afterwards.
	upDryRun, upJSON, upOnly, upSkip = false, false, nil, nil
	t.Cleanup(func() {
		upDryRun, upJSON, upOnly, upSkip = false, false, nil, nil
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(os.Stdout)
		rootCmd.SetErr(os.Stderr)
	})

	var out, errb bytes.Buffer
	rootCmd.SetArgs([]string{"up", "--dry-run"})
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&errb)

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("up --dry-run dispatch failed: %v\nstderr: %s", err, errb.String())
	}
	if n := atomic.LoadInt32(&constructed); n != 0 {
		t.Fatalf("dry-run dispatch constructed the executor %d time(s); it must construct it zero times", n)
	}
	// Proves the assertion ran through the real dry-run path, not an early exit.
	if !strings.Contains(out.String(), "dry run: nothing was executed or written") {
		t.Fatalf("real dry-run dispatch did not produce the plan:\n%s", out.String())
	}
}
