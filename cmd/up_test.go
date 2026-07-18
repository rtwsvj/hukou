package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

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

func TestUp_realRunIsPlaceholderExitTwo(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := doUp(&stdout, &stderr, upOptions{dryRun: false}, nil, fixtureInventory)
	if !errors.Is(err, errRealRun) {
		t.Fatalf("error = %v, want errRealRun", err)
	}
	if ExitCode(err) != 2 {
		t.Fatalf("ExitCode = %d, want 2", ExitCode(err))
	}
	if !strings.Contains(stderr.String(), "real execution lands in a later slice; use --dry-run") {
		t.Fatalf("stderr missing placeholder notice: %q", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("placeholder path wrote to stdout: %q", stdout.String())
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
	if err := doUp(&stdout, &stderr, upOptions{dryRun: true}, lookPath, fixtureInventory); err != nil {
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
	if err := doUp(&stdout, &stderr, upOptions{dryRun: true, json: true}, fakeLookPath(dir), fixtureInventory); err != nil {
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
	if err := doUp(&out, &bytes.Buffer{}, upOptions{dryRun: true, json: true, only: []string{"brew", "hukou"}}, fakeLookPath(dir), fixtureInventory); err != nil {
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
	if err := doUp(&out, &bytes.Buffer{}, upOptions{dryRun: true, json: true, skip: []string{"brew"}}, fakeLookPath(dir), fixtureInventory); err != nil {
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
	err := doUp(&bytes.Buffer{}, &bytes.Buffer{}, upOptions{dryRun: true, only: []string{"bogus"}}, nil, fixtureInventory)
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
	if err := doUp(&stdout, &stderr, upOptions{dryRun: true}, nil, defaultInventory); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "dry run: nothing was executed or written") {
		t.Fatalf("unexpected dry-run output:\n%s", stdout.String())
	}
	if _, err := os.Lstat(dataDir); !os.IsNotExist(err) {
		t.Fatalf("dry-run created the data root: %v", err)
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
	if ExitCode(errRealRun) != 2 {
		t.Fatalf("ExitCode(errRealRun) = %d, want 2", ExitCode(errRealRun))
	}
	if ExitCode(errors.New("boom")) != 1 {
		t.Fatalf("ExitCode(other) = %d, want 1", ExitCode(errors.New("boom")))
	}
}
