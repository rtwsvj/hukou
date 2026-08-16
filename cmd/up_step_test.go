package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/rtwsvj/hukou/internal/orchestrate"
	"github.com/rtwsvj/hukou/internal/output"
)

// flakyExecutor fails failTimes times for a given manager, then succeeds.
type flakyExecutor struct {
	failTimes map[string]int
	calls     map[string]int
}

func (f *flakyExecutor) RunManager(_ context.Context, name string, _ [][]string) orchestrate.StepResult {
	f.calls[name]++
	if f.calls[name] <= f.failTimes[name] {
		return orchestrate.StepResult{Name: name, Status: orchestrate.StatusFailed, Err: errors.New("boom"), ExitCode: 1}
	}
	return orchestrate.StepResult{Name: name, Status: orchestrate.StatusOK}
}

func TestUpStepTrailAndSkipped(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("HUKOU_DATA_DIR", dataDir)
	pre := output.Report{Rows: []output.Row{row("x", "/tmp/x", "unknown", "v1")}}
	post := output.Report{Rows: []output.Row{row("x", "/tmp/x", "unknown", "v2")}}
	hukouCalled := false
	exec := &fakeExecutor{results: map[string]orchestrate.StepResult{}}
	deps := stubRunDeps(t, func(name string) (string, error) {
		if name == "npm" {
			return "/usr/bin/npm", nil
		}
		return "", errors.New("not found")
	}, exec, &hukouCalled, pre, post)

	var stdout, stderr bytes.Buffer
	opts := upOptions{only: []string{"npm", "gh-extensions", "hukou"}}
	err := doUpExecute(&stdout, &stderr, opts, deps)
	if err != nil {
		t.Fatalf("run failed: %v\n%s", err, stderr.String())
	}
	trail := stderr.String()
	if !strings.Contains(trail, "==> npm: npm update -g") {
		t.Fatalf("step header missing:\n%s", trail)
	}
	if !strings.Contains(trail, "ok npm") {
		t.Fatalf("ok line missing:\n%s", trail)
	}
	if !strings.Contains(trail, "==> gh-extensions: skipped") {
		t.Fatalf("skipped line missing:\n%s", trail)
	}
	if !strings.Contains(trail, "==> hukou: in-process upgrade") {
		t.Fatalf("internal header missing:\n%s", trail)
	}
}

func TestUpRetrySucceeds(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("HUKOU_DATA_DIR", dataDir)
	pre := output.Report{Rows: []output.Row{row("x", "/tmp/x", "unknown", "v1")}}
	post := output.Report{Rows: []output.Row{row("x", "/tmp/x", "unknown", "v2")}}
	hukouCalled := false
	exec := &flakyExecutor{failTimes: map[string]int{"npm": 1}, calls: map[string]int{}}
	deps := stubRunDeps(t, func(name string) (string, error) {
		if name == "npm" {
			return "/usr/bin/npm", nil
		}
		return "", errors.New("not found")
	}, exec, &hukouCalled, pre, post)

	var stdout, stderr bytes.Buffer
	opts := upOptions{only: []string{"npm"}, retries: 1}
	if err := doUpExecute(&stdout, &stderr, opts, deps); err != nil {
		t.Fatalf("run failed: %v\n%s", err, stderr.String())
	}
	if exec.calls["npm"] != 2 {
		t.Fatalf("expected 2 attempts, got %d", exec.calls["npm"])
	}
	if !strings.Contains(stderr.String(), "retry 1/1 for npm") {
		t.Fatalf("retry line missing:\n%s", stderr.String())
	}
	if !strings.Contains(stderr.String(), "ok npm") {
		t.Fatalf("final ok missing:\n%s", stderr.String())
	}
}

func TestUpRetryExhaustedFailsAggregate(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("HUKOU_DATA_DIR", dataDir)
	pre := output.Report{Rows: []output.Row{row("x", "/tmp/x", "unknown", "v1")}}
	post := output.Report{Rows: []output.Row{row("x", "/tmp/x", "unknown", "v2")}}
	hukouCalled := false
	exec := &flakyExecutor{failTimes: map[string]int{"npm": 99}, calls: map[string]int{}}
	deps := stubRunDeps(t, func(name string) (string, error) {
		if name == "npm" {
			return "/usr/bin/npm", nil
		}
		return "", errors.New("not found")
	}, exec, &hukouCalled, pre, post)

	var stdout, stderr bytes.Buffer
	opts := upOptions{only: []string{"npm"}, retries: 2}
	err := doUpExecute(&stdout, &stderr, opts, deps)
	if err == nil {
		t.Fatalf("expected aggregate failure; stderr:\n%s", stderr.String())
	}
	if exec.calls["npm"] != 3 {
		t.Fatalf("expected 3 attempts, got %d", exec.calls["npm"])
	}
	if !strings.Contains(stderr.String(), "FAILED npm (exit 1)") {
		t.Fatalf("FAILED line missing:\n%s", stderr.String())
	}
}

func TestUpStepTrailGoesToStderrInJSONMode(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("HUKOU_DATA_DIR", dataDir)
	pre := output.Report{Rows: []output.Row{row("x", "/tmp/x", "unknown", "v1")}}
	post := output.Report{Rows: []output.Row{row("x", "/tmp/x", "unknown", "v2")}}
	hukouCalled := false
	exec := &fakeExecutor{results: map[string]orchestrate.StepResult{}}
	deps := stubRunDeps(t, func(name string) (string, error) {
		if name == "npm" {
			return "/usr/bin/npm", nil
		}
		return "", errors.New("not found")
	}, exec, &hukouCalled, pre, post)

	var stdout, stderr bytes.Buffer
	opts := upOptions{only: []string{"npm"}, json: true}
	if err := doUpExecute(&stdout, &stderr, opts, deps); err != nil {
		t.Fatalf("run failed: %v\n%s", err, stderr.String())
	}
	var doc map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil {
		t.Fatalf("stdout is not pure JSON: %v\n%s", err, stdout.String())
	}
	if !strings.Contains(stderr.String(), "==> npm") {
		t.Fatalf("step trail should go to stderr in json mode:\n%s", stderr.String())
	}
}
