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
	if err := doUp(&out, &errb, upOptions{json: true}, deps); err != nil {
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
	for _, f := range []string{"pre.json", "post.json", "diff.json"} {
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
	if err := doUp(&out, &errb, upOptions{}, deps); err != nil {
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
	err := doUp(&out, &errb, upOptions{}, deps)
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
		dir, err := persistSnapshotHistory(root, now, pre, post, diff)
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
	dir, err := persistSnapshotHistory(root, oldNow, upSnapshot{}, upSnapshot{}, orchestrate.Diff{})
	if err != nil {
		t.Fatal(err)
	}
	// Even though it is the lexically-oldest entry, the run just written survives.
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("current run was pruned despite the never-delete-current guard: %v", err)
	}
	for _, f := range []string{"pre.json", "post.json", "diff.json"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Fatalf("current run missing %s after prune: %v", f, err)
		}
	}
}

// TestUp_e2eSandboxRealExecutorProducesSnapshots drives the fully real wiring
// (real read-only scan, real constrained executor) inside a HUKOU_DATA_DIR
// sandbox: `up --only brew` runs a fake brew that mutates an existing tool on
// PATH, and the pre/post/diff snapshot triple must land recording the resulting
// content (sha256) change. This is the U2 end-to-end acceptance run.
//
// The fake brew uses only shell builtins (echo + `>>` redirection): the sandbox
// PATH is fakeBin alone, so external coreutils like cat/chmod are unavailable,
// and mutating an already-executable file keeps it visible to the scan without
// needing chmod.
func TestUp_e2eSandboxRealExecutorProducesSnapshots(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture brew is a POSIX shell script")
	}
	root := t.TempDir()
	home := filepath.Join(root, "home")
	dataDir := filepath.Join(root, "data")
	fakeBin := filepath.Join(home, "bin")
	for _, d := range []string{fakeBin, filepath.Join(home, ".local", "share"), dataDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// An existing tool on PATH whose bytes the "upgrade" will change.
	writeExecutable(t, fakeBin, "widget", "#!/bin/sh\necho widget v1\n")
	widget := filepath.Join(fakeBin, "widget")

	// A fake brew that "upgrades" widget by appending a line (content -> new
	// sha256), keeping it executable. It targets an absolute path because argv[0]
	// under exec is the bare name, and uses only builtins so it needs no PATH.
	brewBody := fmt.Sprintf("#!/bin/sh\necho '# upgraded by fake brew' >> %q\necho brew done\n", widget)
	writeExecutable(t, fakeBin, "brew", brewBody)

	t.Setenv("HOME", home)
	t.Setenv("HUKOU_DATA_DIR", dataDir)
	t.Setenv("PATH", fakeBin)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("GOPATH", filepath.Join(home, "go"))
	t.Setenv("GOBIN", "")

	var out, errb bytes.Buffer
	// Fully production deps; --only brew keeps the run offline (hukou excluded).
	err := doUp(&out, &errb, upOptions{only: []string{"brew"}}, productionUpDeps(&out, &errb))
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
