package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/rtwsvj/hukou/internal/orchestrate"
	"github.com/rtwsvj/hukou/internal/output"
	"github.com/rtwsvj/hukou/internal/provenance"
	"github.com/rtwsvj/hukou/internal/scan"
)

// fakeExecutor records which managers were run and returns preset results.
type fakeExecutor struct {
	results map[string]orchestrate.StepResult
	calls   []string
}

func (f *fakeExecutor) RunManager(_ context.Context, name string, _ [][]string) orchestrate.StepResult {
	f.calls = append(f.calls, name)
	if r, ok := f.results[name]; ok {
		r.Name = name
		return r
	}
	return orchestrate.StepResult{Name: name, Status: orchestrate.StatusOK}
}

// sequenceInventory returns each report once, repeating the last thereafter, so
// a single inventory seam can supply distinct pre and post snapshots.
func sequenceInventory(reports ...output.Report) func() (output.Report, error) {
	i := 0
	return func() (output.Report, error) {
		r := reports[i]
		if i < len(reports)-1 {
			i++
		}
		return r, nil
	}
}

func row(name, path, source, version string) output.Row {
	return output.Row{
		Binary:      scan.Binary{Name: name, Path: path},
		Attribution: provenance.Attribution{Source: source, Version: version},
	}
}

// stubRunDeps builds a real-run upDeps around injected seams: a fake executor,
// a recorded hukou step, sequenced pre/post inventories, a no-op hasher, a fixed
// clock, and a temp data root.
func stubRunDeps(t *testing.T, lookPath orchestrate.LookPathFunc, exec orchestrate.StepExecutor, hukouCalled *bool, pre, post output.Report) upDeps {
	t.Helper()
	return upDeps{
		lookPath:  lookPath,
		inventory: sequenceInventory(pre, post),
		exec:      exec,
		hukouStep: func(io.Writer, io.Writer) error {
			*hukouCalled = true
			return nil
		},
		hasher:   func(string) string { return "" },
		now:      func() time.Time { return time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC) },
		dataRoot: func() string { return t.TempDir() },
		baseContext: func() (context.Context, context.CancelFunc) {
			return context.WithCancel(context.Background())
		},
	}
}

func TestUp_realRunExecutesManagersDiffsAndPersists(t *testing.T) {
	binDir := t.TempDir()
	writeExecutable(t, binDir, "brew", "#!/bin/sh\n")
	writeExecutable(t, binDir, "npm", "#!/bin/sh\n")

	pre := output.Report{Rows: []output.Row{
		row("brew", "/b/brew", "brew", "4.0"),
		row("tsc", "/n/tsc", "npm", "5.3.0"),
	}}
	post := output.Report{Rows: []output.Row{
		row("brew", "/b/brew", "brew", "4.0"),
		row("tsc", "/n/tsc", "npm", "5.4.0"),       // version changed
		row("newtool", "/n/newtool", "npm", "1.0"), // added
	}}

	fx := &fakeExecutor{results: map[string]orchestrate.StepResult{
		"brew": {Status: orchestrate.StatusOK},
		"npm":  {Status: orchestrate.StatusOK},
	}}
	var hukouCalled bool
	dataDir := t.TempDir()
	deps := stubRunDeps(t, fakeLookPath(binDir), fx, &hukouCalled, pre, post)
	deps.dataRoot = func() string { return dataDir }

	var out, errb bytes.Buffer
	if err := doUpExecute(&out, &errb, upOptions{json: true}, deps); err != nil {
		t.Fatalf("real run returned error: %v\nstderr: %s", err, errb.String())
	}

	// External managers ran in registry order; the internal hukou step ran once.
	if got := strings.Join(fx.calls, ","); got != "brew,npm" {
		t.Fatalf("executor calls = %q, want brew,npm", got)
	}
	if !hukouCalled {
		t.Fatal("internal hukou step was never invoked")
	}

	var doc upRunJSON
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("decode run json: %v\n%s", err, out.String())
	}
	if len(doc.Managers) != 3 {
		t.Fatalf("managers = %d, want 3 (brew,npm,hukou): %+v", len(doc.Managers), doc.Managers)
	}
	for _, m := range doc.Managers {
		if m.Status != "ok" {
			t.Fatalf("manager %s status = %s, want ok", m.Name, m.Status)
		}
	}
	if doc.SnapshotError != "" {
		t.Fatalf("unexpected snapshot_error: %q", doc.SnapshotError)
	}
	// Diff carries the version change and the addition.
	if len(doc.Diff.Changed) != 1 || doc.Diff.Changed[0].Name != "tsc" {
		t.Fatalf("diff.changed = %+v, want [tsc]", doc.Diff.Changed)
	}
	if len(doc.Diff.Added) != 1 || doc.Diff.Added[0].Name != "newtool" {
		t.Fatalf("diff.added = %+v, want [newtool]", doc.Diff.Added)
	}

	// Snapshot triple landed on disk under a timestamped dir.
	if doc.SnapshotDir == "" {
		t.Fatal("run json has empty snapshot_dir")
	}
	for _, f := range []string{"pre.json", "post.json", "diff.json", "run.json"} {
		if _, err := os.Stat(filepath.Join(doc.SnapshotDir, f)); err != nil {
			t.Fatalf("missing snapshot file %s: %v", f, err)
		}
	}
	// diff.json on disk matches the reported diff.
	var onDisk orchestrate.Diff
	readJSON(t, filepath.Join(doc.SnapshotDir, "diff.json"), &onDisk)
	if len(onDisk.Added) != 1 || onDisk.Added[0].Name != "newtool" {
		t.Fatalf("persisted diff.json added = %+v", onDisk.Added)
	}
	// run.json on disk carries schema_version 1 and every manager result.
	var runDoc upRunDoc
	readJSON(t, filepath.Join(doc.SnapshotDir, "run.json"), &runDoc)
	if runDoc.SchemaVersion != 1 {
		t.Fatalf("run.json schema_version = %d, want 1", runDoc.SchemaVersion)
	}
	if len(runDoc.Managers) != 3 {
		t.Fatalf("run.json managers = %d, want 3 (brew,npm,hukou): %+v", len(runDoc.Managers), runDoc.Managers)
	}
	if runDoc.Time == "" {
		t.Fatal("run.json time stamp is empty")
	}
}

func TestUp_realRunTableShowsChangesAndRollbackHints(t *testing.T) {
	binDir := t.TempDir()
	writeExecutable(t, binDir, "npm", "#!/bin/sh\n")

	pre := output.Report{Rows: []output.Row{
		row("tsc", "/n/tsc", "npm", "5.3.0"),
		row("mytool", "/h/mytool", "hukou", "1.0.0"),
	}}
	post := output.Report{Rows: []output.Row{
		row("tsc", "/n/tsc", "npm", "5.4.0"),         // foreign change -> npm downgrade hint
		row("mytool", "/h/mytool", "hukou", "2.0.0"), // hukou change -> hukou rollback hint
	}}

	fx := &fakeExecutor{}
	var hukouCalled bool
	deps := stubRunDeps(t, fakeLookPath(binDir), fx, &hukouCalled, pre, post)

	var out, errb bytes.Buffer
	if err := doUpExecute(&out, &errb, upOptions{}, deps); err != nil {
		t.Fatalf("run error: %v", err)
	}
	s := out.String()
	for _, want := range []string{
		"upgrade results:",
		"inventory changes:",
		"NAME", "SOURCE", "BEFORE", "AFTER",
		"tsc", "npm", "5.3.0", "5.4.0",
		"rollback options",
		"hukou rollback mytool",
		"npm i -g tsc@5.3.0",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("real-run table missing %q:\n%s", want, s)
		}
	}
}

func TestUp_realRunAggregateExitOnManagerFailure(t *testing.T) {
	binDir := t.TempDir()
	writeExecutable(t, binDir, "brew", "#!/bin/sh\n")
	writeExecutable(t, binDir, "npm", "#!/bin/sh\n")

	same := output.Report{Rows: []output.Row{row("brew", "/b/brew", "brew", "4.0")}}
	fx := &fakeExecutor{results: map[string]orchestrate.StepResult{
		"brew": {Status: orchestrate.StatusFailed, ExitCode: 3, Err: fmt.Errorf("brew blew up")},
		"npm":  {Status: orchestrate.StatusOK},
	}}
	var hukouCalled bool
	deps := stubRunDeps(t, fakeLookPath(binDir), fx, &hukouCalled, same, same)

	var out, errb bytes.Buffer
	err := doUpExecute(&out, &errb, upOptions{}, deps)
	if err == nil {
		t.Fatal("expected aggregate failure error")
	}
	if ExitCode(err) != 1 {
		t.Fatalf("ExitCode = %d, want 1", ExitCode(err))
	}
	// The report is still printed even though the run failed.
	if !strings.Contains(out.String(), "upgrade results:") {
		t.Fatalf("report not printed on failure:\n%s", out.String())
	}
	// npm still ran after brew failed (a failing manager does not stop the rest).
	if got := strings.Join(fx.calls, ","); got != "brew,npm" {
		t.Fatalf("executor calls = %q, want brew,npm (rest must still run)", got)
	}
	if !strings.Contains(errb.String(), "manager brew failed") && !strings.Contains(errb.String(), "brew failed") {
		t.Fatalf("stderr missing per-manager failure note:\n%s", errb.String())
	}
	if !strings.Contains(errb.String(), "1 manager(s) failed: brew") {
		t.Fatalf("stderr missing aggregate summary:\n%s", errb.String())
	}
}

// cancelAfterExecutor wraps a fakeExecutor and cancels the run's context the
// moment a chosen manager runs — simulating a Ctrl-C arriving mid-run.
type cancelAfterExecutor struct {
	fake     *fakeExecutor
	cancelOn string
	cancel   context.CancelFunc
}

func (c *cancelAfterExecutor) RunManager(ctx context.Context, name string, cmds [][]string) orchestrate.StepResult {
	res := c.fake.RunManager(ctx, name, cmds)
	if name == c.cancelOn {
		c.cancel()
	}
	return res
}

// TestUp_interruptStopsSubsequentManagersAndSkipsHukou drives the interruption
// fix: an injected context is canceled right after the first manager (brew)
// runs. The loop's per-iteration ctx check must then stop launching further
// managers — npm must not run and the internal hukou step must be skipped — and
// the run must exit non-zero with a canceled marker in the report.
func TestUp_interruptStopsSubsequentManagersAndSkipsHukou(t *testing.T) {
	binDir := t.TempDir()
	writeExecutable(t, binDir, "brew", "#!/bin/sh\n")
	writeExecutable(t, binDir, "npm", "#!/bin/sh\n")

	same := output.Report{Rows: []output.Row{row("brew", "/b/brew", "brew", "4.0")}}
	fx := &fakeExecutor{}
	var hukouCalled bool
	deps := stubRunDeps(t, fakeLookPath(binDir), nil, &hukouCalled, same, same)

	// Wire a context the wrapper can cancel, and inject the canceling executor.
	ctx, cancel := context.WithCancel(context.Background())
	deps.baseContext = func() (context.Context, context.CancelFunc) { return ctx, cancel }
	deps.exec = &cancelAfterExecutor{fake: fx, cancelOn: "brew", cancel: cancel}

	var out, errb bytes.Buffer
	err := doUpExecute(&out, &errb, upOptions{}, deps)
	if err == nil {
		t.Fatal("interrupted run must exit non-zero")
	}
	if ExitCode(err) != 1 {
		t.Fatalf("ExitCode = %d, want 1", ExitCode(err))
	}
	// brew ran; npm did NOT (canceled before it); hukou step was skipped.
	if got := strings.Join(fx.calls, ","); got != "brew" {
		t.Fatalf("executor calls = %q, want only brew (npm must not run after cancel)", got)
	}
	if hukouCalled {
		t.Fatal("internal hukou step ran despite cancellation before it")
	}
	// The report marks the run canceled and stderr names the not-completed set.
	if !strings.Contains(out.String(), "canceled") {
		t.Fatalf("report does not mark the run canceled:\n%s", out.String())
	}
	if !strings.Contains(errb.String(), "run canceled") {
		t.Fatalf("stderr missing cancellation note:\n%s", errb.String())
	}
}

// TestUp_interruptDuringInternalHukouStepIsNonZero drives the post-step
// boundary check: the run's root ctx is canceled WHILE the internal hukou step
// runs (the in-process, WAL-protected step cannot be interrupted mid-flight,
// so it completes and returns nil). The step's result must be reclassified
// canceled — not ok — and the run must exit non-zero. Before this fix an
// interrupt arriving during the internal step was silently lost and the run
// reported ok / exit 0.
func TestUp_interruptDuringInternalHukouStepIsNonZero(t *testing.T) {
	binDir := t.TempDir() // no external managers on the fake PATH; hukou is internal
	same := output.Report{Rows: []output.Row{row("brew", "/b/brew", "brew", "4.0")}}
	fx := &fakeExecutor{}
	var hukouCalled bool
	deps := stubRunDeps(t, fakeLookPath(binDir), fx, &hukouCalled, same, same)

	ctx, cancel := context.WithCancel(context.Background())
	deps.baseContext = func() (context.Context, context.CancelFunc) { return ctx, cancel }
	// The step observes the interrupt only at its boundary: it finishes its
	// (simulated) work, the cancel lands mid-step, and it returns nil.
	deps.hukouStep = func(io.Writer, io.Writer) error {
		hukouCalled = true
		cancel()
		return nil
	}

	var out, errb bytes.Buffer
	err := doUpExecute(&out, &errb, upOptions{json: true, only: []string{"hukou"}}, deps)
	if err == nil {
		t.Fatal("run interrupted during the internal hukou step reported success (exit 0); must be non-zero")
	}
	if ExitCode(err) != 1 {
		t.Fatalf("ExitCode = %d, want 1", ExitCode(err))
	}
	if !hukouCalled {
		t.Fatal("internal hukou step never ran; fixture is broken")
	}

	var doc upRunJSON
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("decode run json: %v\n%s", err, out.String())
	}
	if len(doc.Managers) != 1 || doc.Managers[0].Name != "hukou" {
		t.Fatalf("managers = %+v, want exactly the hukou step", doc.Managers)
	}
	if got := doc.Managers[0].Status; got != "canceled" {
		t.Fatalf("internal step status = %q after mid-step interrupt, want canceled (not ok)", got)
	}
}

// TestUp_snapshotPersistFailureIsNonZeroAndReported drives P1-4: when the
// snapshot history cannot be written, the run must exit non-zero even though
// every manager succeeded, and the report must record the failure honestly
// (snapshot_error in JSON, empty snapshot_dir; FAILED marker in the table).
func TestUp_snapshotPersistFailureIsNonZeroAndReported(t *testing.T) {
	binDir := t.TempDir()
	writeExecutable(t, binDir, "brew", "#!/bin/sh\n")

	same := output.Report{Rows: []output.Row{row("brew", "/b/brew", "brew", "4.0")}}
	fx := &fakeExecutor{}
	var hukouCalled bool
	deps := stubRunDeps(t, fakeLookPath(binDir), fx, &hukouCalled, same, same)

	// dataRoot resolves beneath a regular FILE, so MkdirAll on <root>/snapshots
	// must fail: an unwritable snapshot destination.
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("not a dir"), 0o644); err != nil {
		t.Fatal(err)
	}
	deps.dataRoot = func() string { return filepath.Join(blocker, "data") }

	var out, errb bytes.Buffer
	err := doUpExecute(&out, &errb, upOptions{json: true, only: []string{"brew"}}, deps)
	if err == nil {
		t.Fatal("snapshot persistence failure must make the run fail")
	}
	if ExitCode(err) != 1 {
		t.Fatalf("ExitCode = %d, want 1", ExitCode(err))
	}

	var doc upRunJSON
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("decode run json: %v\n%s", err, out.String())
	}
	if doc.SnapshotError == "" {
		t.Fatalf("snapshot_error missing from JSON report:\n%s", out.String())
	}
	if doc.SnapshotDir != "" {
		t.Fatalf("snapshot_dir should be empty on persist failure, got %q", doc.SnapshotDir)
	}
	// All managers were fine; only the snapshot failed.
	for _, m := range doc.Managers {
		if m.Status != "ok" {
			t.Fatalf("manager %s status = %s, want ok", m.Name, m.Status)
		}
	}
	if !strings.Contains(errb.String(), "failed to persist snapshot history") {
		t.Fatalf("stderr missing snapshot failure note:\n%s", errb.String())
	}

	// Same failure in table mode is reported in the report body too.
	deps2 := stubRunDeps(t, fakeLookPath(binDir), &fakeExecutor{}, &hukouCalled, same, same)
	deps2.dataRoot = func() string { return filepath.Join(blocker, "data") }
	out.Reset()
	errb.Reset()
	if err := doUpExecute(&out, &errb, upOptions{only: []string{"brew"}}, deps2); err == nil {
		t.Fatal("table-mode snapshot failure must also fail the run")
	}
	if !strings.Contains(out.String(), "snapshot: FAILED") {
		t.Fatalf("table report does not mark the snapshot failure:\n%s", out.String())
	}
}

func TestPersistSnapshotHistory_AtomicAndPruned(t *testing.T) {
	root := t.TempDir()
	snapsDir := filepath.Join(root, "snapshots")

	base := time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)
	pre := upSnapshot{Time: "pre"}
	post := upSnapshot{Time: "post"}
	diff := orchestrate.Diff{}

	var lastDir string
	for i := 0; i < 12; i++ {
		now := base.Add(time.Duration(i) * time.Minute)
		dir, err := persistSnapshotHistory(root, now, pre, post, diff, nil)
		if err != nil {
			t.Fatalf("persist %d: %v", i, err)
		}
		lastDir = dir
		// No staging directory may survive a successful persist.
		assertNoTmpStaging(t, snapsDir)
	}

	entries, err := os.ReadDir(snapsDir)
	if err != nil {
		t.Fatal(err)
	}
	dirs := 0
	for _, e := range entries {
		if e.IsDir() {
			dirs++
		}
	}
	if dirs != snapshotRetention {
		t.Fatalf("kept %d snapshot dirs, want %d", dirs, snapshotRetention)
	}
	if _, err := os.Stat(lastDir); err != nil {
		t.Fatalf("most recent snapshot was pruned: %v", err)
	}
}

func TestPersistSnapshotHistory_NeverPrunesCurrentEvenIfOldest(t *testing.T) {
	root := t.TempDir()
	snapsDir := filepath.Join(root, "snapshots")
	if err := os.MkdirAll(snapsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Pre-seed 11 dirs whose names sort AFTER the run we are about to write, so
	// the just-written run is lexically the oldest and would be a prune target.
	for i := 0; i < 11; i++ {
		name := fmt.Sprintf("2027-01-%02dT00:00:00Z", i+1)
		if err := os.MkdirAll(filepath.Join(snapsDir, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	oldNow := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	dir, err := persistSnapshotHistory(root, oldNow, upSnapshot{}, upSnapshot{}, orchestrate.Diff{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Even though it is the lexically-oldest entry, the run just written survives.
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("current run was pruned despite the never-delete-current guard: %v", err)
	}
	for _, f := range []string{"pre.json", "post.json", "diff.json", "run.json"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Fatalf("current run missing %s after prune: %v", f, err)
		}
	}
}

// e2eSandbox builds the standard sandbox for full-wiring runs: an isolated
// HOME/PATH/HUKOU_DATA_DIR with an existing "widget" tool and a fake brew whose
// upgrade appends to it (builtins only — the sandbox PATH has no coreutils).
func e2eSandbox(t *testing.T) (fakeBin, dataDir string) {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	dataDir = filepath.Join(root, "data")
	fakeBin = filepath.Join(home, "bin")
	for _, d := range []string{fakeBin, filepath.Join(home, ".local", "share"), dataDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeExecutable(t, fakeBin, "widget", "#!/bin/sh\necho widget v1\n")
	widget := filepath.Join(fakeBin, "widget")
	brewBody := fmt.Sprintf("#!/bin/sh\necho '# upgraded by fake brew' >> %q\necho brew done\n", widget)
	writeExecutable(t, fakeBin, "brew", brewBody)

	t.Setenv("HOME", home)
	t.Setenv("HUKOU_DATA_DIR", dataDir)
	t.Setenv("PATH", fakeBin)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("GOPATH", filepath.Join(home, "go"))
	t.Setenv("GOBIN", "")
	return fakeBin, dataDir
}

// TestUp_e2eSandboxRealExecutorProducesSnapshots drives the fully real wiring
// (real read-only scan, real constrained executor) inside a HUKOU_DATA_DIR
// sandbox: `up --only brew` runs a fake brew that mutates an existing tool on
// PATH, and the pre/post/diff snapshot triple must land recording the resulting
// content (sha256) change. This is the U2 end-to-end acceptance run.
func TestUp_e2eSandboxRealExecutorProducesSnapshots(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture brew is a POSIX shell script")
	}
	_, dataDir := e2eSandbox(t)

	var out, errb bytes.Buffer
	// Fully production wiring via the real-run entry; --only brew keeps the run
	// offline (hukou excluded).
	err := runUpExecute(&out, &errb, upOptions{only: []string{"brew"}})
	if err != nil {
		t.Fatalf("e2e run error: %v\nstdout:\n%s\nstderr:\n%s", err, out.String(), errb.String())
	}

	// The real executor streamed the fake brew's output with a [brew] prefix.
	if !strings.Contains(out.String(), "[brew] brew done") {
		t.Fatalf("expected streamed [brew] output:\n%s", out.String())
	}
	// The report mentions the changed widget.
	if !strings.Contains(out.String(), "widget") {
		t.Fatalf("diff report did not mention the changed widget:\n%s", out.String())
	}

	// The snapshot triple landed under HUKOU_DATA_DIR/snapshots/<ts>/.
	runDir := singleSnapshotRun(t, dataDir)
	for _, f := range []string{"pre.json", "post.json", "diff.json"} {
		if _, err := os.Stat(filepath.Join(runDir, f)); err != nil {
			t.Fatalf("missing %s in %s: %v", f, runDir, err)
		}
	}
	var diff orchestrate.Diff
	readJSON(t, filepath.Join(runDir, "diff.json"), &diff)
	foundChanged := false
	for _, c := range diff.Changed {
		if c.Name == "widget" {
			foundChanged = true
			if !containsStr(c.Reasons, "sha256") {
				t.Fatalf("widget change reasons = %v, want sha256", c.Reasons)
			}
		}
	}
	if !foundChanged {
		t.Fatalf("persisted diff.json did not record widget as changed: %+v", diff)
	}
}

// TestUp_e2eJSONStdoutIsPureJSON drives P1-3 with the fully real wiring: in
// --json mode stdout must be exactly one parseable JSON document, while all
// streamed manager output (and the internal hukou step, included via --only
// brew,hukou against the empty sandbox manifest) appears on stderr instead.
func TestUp_e2eJSONStdoutIsPureJSON(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture brew is a POSIX shell script")
	}
	e2eSandbox(t)

	var out, errb bytes.Buffer
	// hukou is included: with the sandbox's empty manifest the internal step
	// prints "No adopted tools" without any network access — and that line must
	// land on stderr, not stdout.
	err := runUpExecute(&out, &errb, upOptions{json: true, only: []string{"brew", "hukou"}})
	if err != nil {
		t.Fatalf("e2e json run error: %v\nstdout:\n%s\nstderr:\n%s", err, out.String(), errb.String())
	}

	// stdout is pure JSON: it must unmarshal as-is.
	var doc upRunJSON
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("stdout is not pure JSON: %v\n%s", err, out.String())
	}
	if doc.SchemaVersion != 1 || len(doc.Managers) != 2 {
		t.Fatalf("unexpected run document: %+v", doc)
	}
	// No streamed output leaked onto stdout.
	if strings.Contains(out.String(), "[brew]") || strings.Contains(out.String(), "No adopted tools") {
		t.Fatalf("streamed output leaked into stdout:\n%s", out.String())
	}
	// The streams landed on stderr, still live and prefixed.
	if !strings.Contains(errb.String(), "[brew] brew done") {
		t.Fatalf("manager stream missing from stderr:\n%s", errb.String())
	}
	if !strings.Contains(errb.String(), "No adopted tools") {
		t.Fatalf("internal hukou step output missing from stderr:\n%s", errb.String())
	}
	// And the snapshot trail still landed.
	if doc.SnapshotError != "" {
		t.Fatalf("unexpected snapshot error: %s", doc.SnapshotError)
	}
	if doc.SnapshotDir == "" {
		t.Fatal("snapshot_dir empty in json report")
	}
	if _, err := os.Stat(filepath.Join(doc.SnapshotDir, "diff.json")); err != nil {
		t.Fatalf("reported snapshot dir missing diff.json: %v", err)
	}
}

// singleSnapshotRun returns the lone persisted run directory under
// <dataDir>/snapshots, failing the test if none exists.
func singleSnapshotRun(t *testing.T, dataDir string) string {
	t.Helper()
	snapsDir := filepath.Join(dataDir, "snapshots")
	runs, err := os.ReadDir(snapsDir)
	if err != nil {
		t.Fatalf("read snapshots dir: %v", err)
	}
	var runDir string
	for _, e := range runs {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".tmp-snap-") {
			runDir = filepath.Join(snapsDir, e.Name())
		}
	}
	if runDir == "" {
		t.Fatalf("no persisted snapshot run under %s", snapsDir)
	}
	return runDir
}

func containsStr(s []string, want string) bool {
	for _, v := range s {
		if v == want {
			return true
		}
	}
	return false
}

func assertNoTmpStaging(t *testing.T, snapsDir string) {
	t.Helper()
	entries, err := os.ReadDir(snapsDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-snap-") {
			t.Fatalf("leftover staging directory after persist: %s", e.Name())
		}
	}
}

func readJSON(t *testing.T, path string, v any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
}
