package cmd

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/rtwsvj/hukou/internal/manifest"
	"github.com/rtwsvj/hukou/internal/orchestrate"
	"github.com/rtwsvj/hukou/internal/output"
	"github.com/rtwsvj/hukou/internal/provenance"
	"github.com/rtwsvj/hukou/internal/scan"
)

// fixtureInventory is a small deterministic report so dry-run tests never depend
// on the host machine's real PATH.
func fixtureInventory() (output.Report, error) {
	return output.Report{
		Rows: []output.Row{
			{Binary: scan.Binary{Name: "brew", Path: "/opt/homebrew/bin/brew"}, Attribution: provenance.Attribution{Source: "brew"}},
			{Binary: scan.Binary{Name: "foo", Path: "/usr/local/bin/foo"}, Attribution: provenance.Attribution{Source: "unknown"}},
		},
		TotalWalked: 2,
	}, nil
}

// forbidExecutor is a StepExecutor stub that fails the test the moment any
// dry-run code path tries to launch a manager command. Every dry-run doUp test
// injects it (via dryDeps), so "dry run launches zero subprocesses" is enforced
// mechanically on every covered path — the behavior-layer half of the executor
// boundary (the static half lives in the orchestrate package import-guard test).
type forbidExecutor struct{ t *testing.T }

func (f forbidExecutor) RunManager(_ context.Context, name string, commands [][]string) orchestrate.StepResult {
	f.t.Helper()
	f.t.Fatalf("dry run must never execute a manager command, but the executor ran: %s %v", name, commands)
	return orchestrate.StepResult{}
}

// dryDeps builds upDeps whose every real-run seam fails the test if touched, so
// the dry-run path is proven to only read (via inventory) and print.
func dryDeps(t *testing.T, lookPath orchestrate.LookPathFunc, inventory func() (output.Report, error)) upDeps {
	t.Helper()
	return upDeps{
		lookPath:  lookPath,
		inventory: inventory,
		exec:      forbidExecutor{t},
		hukouStep: func(io.Writer, io.Writer) error {
			t.Fatalf("dry run must never run the internal hukou step")
			return nil
		},
		hasher: func(string) string {
			t.Fatalf("dry run must never hash a binary")
			return ""
		},
		now: func() time.Time {
			t.Fatalf("dry run must never stamp a snapshot")
			return time.Time{}
		},
		dataRoot: func() string {
			t.Fatalf("dry run must never resolve the data root for writing")
			return ""
		},
	}
}

// fakeLookPath resolves executables from a fake PATH dir, mirroring exec.LookPath.
func fakeLookPath(dir string) orchestrate.LookPathFunc {
	return func(file string) (string, error) {
		p := filepath.Join(dir, file)
		info, err := os.Stat(p)
		if err != nil {
			return "", err
		}
		if info.IsDir() || info.Mode()&0o111 == 0 {
			return "", exec.ErrNotFound
		}
		return p, nil
	}
}

func TestUp_dryRunTableListsDetectedManagers(t *testing.T) {
	dir := t.TempDir()
	writeExecutable(t, dir, "brew", "#!/bin/sh\n")
	writeExecutable(t, dir, "npm", "#!/bin/sh\n")

	var probed []string
	lookPath := func(file string) (string, error) {
		probed = append(probed, file) // never launches a subprocess.
		return fakeLookPath(dir)(file)
	}

	var stdout, stderr bytes.Buffer
	if err := doUp(&stdout, &stderr, upOptions{dryRun: true}, dryDeps(t, lookPath, fixtureInventory)); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()

	// Detected managers appear with their exact commands; hukou is internal.
	for _, want := range []string{
		"NAME", "SOURCE-BINARY", "COMMANDS",
		"brew", "brew update && brew upgrade",
		"npm", "npm update -g",
		"hukou", "internal", "hukou upgrade --all",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("dry-run table missing %q:\n%s", want, out)
		}
	}
	// Undetected managers are absent from the human table.
	for _, absent := range []string{"pnpm", "rustup", "gh-extensions"} {
		if strings.Contains(out, absent) {
			t.Fatalf("undetected %q should not appear in the table:\n%s", absent, out)
		}
	}
	// Reused scan summary line + zero-effect trailer.
	if !strings.Contains(out, "summary: total=2") {
		t.Fatalf("dry-run missing inventory summary line:\n%s", out)
	}
	if !strings.Contains(out, "dry run: nothing was executed or written") {
		t.Fatalf("dry-run missing zero-effect trailer:\n%s", out)
	}
	// Detection only probes external managers via the injected lookPath.
	if len(probed) != 6 {
		t.Fatalf("lookPath probes = %d (%v), want 6 externals", len(probed), probed)
	}
}

func TestUp_dryRunJSONParses(t *testing.T) {
	dir := t.TempDir()
	writeExecutable(t, dir, "brew", "#!/bin/sh\n")

	var stdout, stderr bytes.Buffer
	if err := doUp(&stdout, &stderr, upOptions{dryRun: true, json: true}, dryDeps(t, fakeLookPath(dir), fixtureInventory)); err != nil {
		t.Fatal(err)
	}

	var plan upPlanJSON
	if err := json.Unmarshal(stdout.Bytes(), &plan); err != nil {
		t.Fatalf("decode plan: %v\n%s", err, stdout.String())
	}
	if plan.SchemaVersion != 1 {
		t.Fatalf("schema_version = %d, want 1", plan.SchemaVersion)
	}
	if len(plan.Managers) != 7 {
		t.Fatalf("managers len = %d, want the full registry of 7", len(plan.Managers))
	}
	byName := map[string]upManagerJSON{}
	for _, m := range plan.Managers {
		byName[m.Name] = m
	}
	if !byName["brew"].Available {
		t.Fatalf("brew should be available: %+v", byName["brew"])
	}
	if byName["npm"].Available {
		t.Fatalf("npm should be unavailable on this fake PATH: %+v", byName["npm"])
	}
	if !byName["hukou"].Available || byName["hukou"].Binary != "" {
		t.Fatalf("hukou should be available with empty binary: %+v", byName["hukou"])
	}
	if got := byName["brew"].Commands; len(got) != 2 || got[0][0] != "brew" {
		t.Fatalf("brew commands not carried as argv arrays: %+v", got)
	}
	if plan.InventorySummary.Total != 2 {
		t.Fatalf("inventory_summary.total = %d, want 2", plan.InventorySummary.Total)
	}
	// Fields must be stable snake_case.
	raw := stdout.String()
	for _, field := range []string{`"schema_version"`, `"inventory_summary"`, `"available"`, `"source_count"`} {
		if !strings.Contains(raw, field) {
			t.Fatalf("JSON missing stable field %s:\n%s", field, raw)
		}
	}
}

func TestUp_onlyAndSkipFilterThePlan(t *testing.T) {
	dir := t.TempDir()
	writeExecutable(t, dir, "brew", "#!/bin/sh\n")
	writeExecutable(t, dir, "npm", "#!/bin/sh\n")

	// --only brew,hukou keeps just those two.
	var out bytes.Buffer
	if err := doUp(&out, &bytes.Buffer{}, upOptions{dryRun: true, json: true, only: []string{"brew", "hukou"}}, dryDeps(t, fakeLookPath(dir), fixtureInventory)); err != nil {
		t.Fatal(err)
	}
	var plan upPlanJSON
	if err := json.Unmarshal(out.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	if len(plan.Managers) != 2 {
		t.Fatalf("--only kept %d managers, want 2: %+v", len(plan.Managers), plan.Managers)
	}

	// --skip removes named managers from the set.
	out.Reset()
	if err := doUp(&out, &bytes.Buffer{}, upOptions{dryRun: true, json: true, skip: []string{"brew"}}, dryDeps(t, fakeLookPath(dir), fixtureInventory)); err != nil {
		t.Fatal(err)
	}
	plan = upPlanJSON{}
	if err := json.Unmarshal(out.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	for _, m := range plan.Managers {
		if m.Name == "brew" {
			t.Fatalf("--skip brew still present: %+v", plan.Managers)
		}
	}
	if len(plan.Managers) != 6 {
		t.Fatalf("--skip left %d managers, want 6", len(plan.Managers))
	}
}

func TestUp_unknownManagerNameErrors(t *testing.T) {
	err := doUp(&bytes.Buffer{}, &bytes.Buffer{}, upOptions{dryRun: true, only: []string{"bogus"}}, dryDeps(t, nil, fixtureInventory))
	if err == nil {
		t.Fatal("expected error for unknown manager name")
	}
	var unknown *orchestrate.UnknownManagerError
	if !errors.As(err, &unknown) {
		t.Fatalf("error = %v, want UnknownManagerError", err)
	}
}

func TestUp_dryRunIsZeroWriteAgainstRealScan(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "missing-data-root")
	t.Setenv("HUKOU_DATA_DIR", dataDir)

	var stdout, stderr bytes.Buffer
	// nil lookPath uses the real exec.LookPath; defaultInventory runs the real
	// read-only scan. Neither may create the data root or spawn a process.
	if err := doUp(&stdout, &stderr, upOptions{dryRun: true}, dryDeps(t, nil, defaultInventory)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "dry run: nothing was executed or written") {
		t.Fatalf("unexpected dry-run output:\n%s", stdout.String())
	}
	if _, err := os.Lstat(dataDir); !os.IsNotExist(err) {
		t.Fatalf("dry-run created the data root: %v", err)
	}
}

// TestUp_dryRunNeverInvokesExecutor drives every dry-run code path of doUp with
// a stub executor (and stubbed hukou/hash/history seams) that fail the test on
// first invocation. Combined with the static import guard in the orchestrate
// package, this turns "zero subprocess execution on the dry-run path" from prose
// into a mechanical, commit-reproducible proof.
func TestUp_dryRunNeverInvokesExecutor(t *testing.T) {
	dir := t.TempDir()
	writeExecutable(t, dir, "brew", "#!/bin/sh\n")
	writeExecutable(t, dir, "gh", "#!/bin/sh\n")

	paths := []struct {
		name string
		opts upOptions
	}{
		{"dry-run table", upOptions{dryRun: true}},
		{"dry-run json", upOptions{dryRun: true, json: true}},
		{"dry-run only", upOptions{dryRun: true, only: []string{"brew", "hukou"}}},
		{"dry-run skip", upOptions{dryRun: true, json: true, skip: []string{"npm"}}},
		{"dry-run filter error", upOptions{dryRun: true, only: []string{"bogus"}}},
	}
	for _, tc := range paths {
		var stdout, stderr bytes.Buffer
		// The filter-error path returns an error; the only assertion here is that
		// none of the forbidding seams fire (they fail the test themselves).
		_ = doUp(&stdout, &stderr, tc.opts, dryDeps(t, fakeLookPath(dir), fixtureInventory))
	}
}

// treeSig is one filesystem node in a snapshot: everything that could reveal a
// write — type, permissions, size, mtime, symlink target, and a full content
// hash for regular files.
type treeSig struct {
	Mode    fs.FileMode
	Size    int64
	ModTime int64 // UnixNano
	Link    string
	SHA256  string
}

// snapshotTree records every node under root byte-for-byte (content hashed).
// Directory mtimes are included deliberately: they catch even a temp file that
// was created and deleted again inside the tree.
func snapshotTree(t *testing.T, root string) map[string]treeSig {
	t.Helper()
	sigs := map[string]treeSig{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		sig := treeSig{Mode: info.Mode(), Size: info.Size(), ModTime: info.ModTime().UnixNano()}
		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			sig.Link = link
		case info.Mode().IsRegular():
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(data)
			sig.SHA256 = hex.EncodeToString(sum[:])
		case info.IsDir():
			sig.Size = 0 // directory sizes are fs-dependent noise
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		sigs[rel] = sig
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot %s: %v", root, err)
	}
	return sigs
}

func diffTrees(before, after map[string]treeSig) []string {
	var diffs []string
	for path, b := range before {
		a, ok := after[path]
		if !ok {
			diffs = append(diffs, "removed: "+path)
			continue
		}
		if a != b {
			diffs = append(diffs, fmt.Sprintf("modified: %s (before %+v, after %+v)", path, b, a))
		}
	}
	for path := range after {
		if _, ok := before[path]; !ok {
			diffs = append(diffs, "added: "+path)
		}
	}
	sort.Strings(diffs)
	return diffs
}

// TestUp_dryRunSandboxTreesAreByteForByteUnchanged is the global zero-write
// proof: HOME and HUKOU_DATA_DIR both point into a sandbox under t.TempDir,
// PATH points at a fake bin dir inside that HOME, a full dry-run (table and
// JSON) runs against the REAL exec.LookPath and the REAL read-only scan, and
// afterwards both trees must match the pre-run snapshot node for node —
// modes, sizes, mtimes, symlink targets, and content hashes all included.
func TestUp_dryRunSandboxTreesAreByteForByteUnchanged(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	dataDir := filepath.Join(root, "data")
	fakeBin := filepath.Join(home, "bin")

	// A small but non-trivial HOME: fake PATH executables, XDG data, a dotfile.
	for _, dir := range []string{fakeBin, filepath.Join(home, ".local", "share"), dataDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeExecutable(t, fakeBin, "brew", "#!/bin/sh\nexit 0\n")
	writeExecutable(t, fakeBin, "straytool", "#!/bin/sh\nexit 0\n")
	if err := os.WriteFile(filepath.Join(home, ".zshrc"), []byte("# sandbox\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A real (empty) manifest so the hukou detector's read path is exercised.
	m := &manifest.Manifest{
		SchemaVersion: manifest.CurrentSchemaVersion,
		Retention:     manifest.DefaultRetentionPolicy(),
		Entries:       []manifest.Entry{},
	}
	if err := m.Save(filepath.Join(dataDir, "manifest.json")); err != nil {
		t.Fatal(err)
	}

	// Redirect every root the scan and detection may derive into the sandbox.
	t.Setenv("HOME", home)
	t.Setenv("HUKOU_DATA_DIR", dataDir)
	t.Setenv("PATH", fakeBin)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("GOPATH", filepath.Join(home, "go"))
	t.Setenv("GOBIN", "")

	homeBefore := snapshotTree(t, home)
	dataBefore := snapshotTree(t, dataDir)

	// Full dry-run, twice (human table + JSON), with the REAL LookPath (nil)
	// and the REAL read-only inventory scan.
	var stdout, stderr bytes.Buffer
	if err := doUp(&stdout, &stderr, upOptions{dryRun: true}, dryDeps(t, nil, defaultInventory)); err != nil {
		t.Fatal(err)
	}
	if err := doUp(&stdout, &stderr, upOptions{dryRun: true, json: true}, dryDeps(t, nil, defaultInventory)); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	if !strings.Contains(out, "dry run: nothing was executed or written") {
		t.Fatalf("missing zero-effect trailer:\n%s", out)
	}
	// The real LookPath found the sandbox brew; the real scan saw both fakes.
	if !strings.Contains(out, "brew update && brew upgrade") {
		t.Fatalf("sandbox brew was not detected via real LookPath:\n%s", out)
	}
	if !strings.Contains(out, "summary: total=2") {
		t.Fatalf("real scan did not cover the sandbox PATH:\n%s", out)
	}

	if diffs := diffTrees(homeBefore, snapshotTree(t, home)); len(diffs) != 0 {
		t.Fatalf("dry-run touched HOME:\n%s", strings.Join(diffs, "\n"))
	}
	if diffs := diffTrees(dataBefore, snapshotTree(t, dataDir)); len(diffs) != 0 {
		t.Fatalf("dry-run touched HUKOU_DATA_DIR:\n%s", strings.Join(diffs, "\n"))
	}
}

func TestUpCommandRegistered(t *testing.T) {
	command, _, err := rootCmd.Find([]string{"up"})
	if err != nil {
		t.Fatal(err)
	}
	if command != upCmd {
		t.Fatalf("up command not registered: %v", command)
	}
	for _, flag := range []string{"dry-run", "json", "only", "skip"} {
		if upCmd.Flags().Lookup(flag) == nil {
			t.Fatalf("up flag %q is not registered", flag)
		}
	}
}

func TestExitCode(t *testing.T) {
	if ExitCode(nil) != 0 {
		t.Fatalf("ExitCode(nil) = %d, want 0", ExitCode(nil))
	}
	if ExitCode(errUpManagersFailed) != 1 {
		t.Fatalf("ExitCode(errUpManagersFailed) = %d, want 1", ExitCode(errUpManagersFailed))
	}
	if ExitCode(errors.New("boom")) != 1 {
		t.Fatalf("ExitCode(other) = %d, want 1", ExitCode(errors.New("boom")))
	}
}
