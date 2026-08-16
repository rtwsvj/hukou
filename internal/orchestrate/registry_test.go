package orchestrate

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

// dirLookPath returns a LookPathFunc backed by a real directory, mirroring
// exec.LookPath (stat + executable bit) against a fake PATH built from t.TempDir.
func dirLookPath(dir string) LookPathFunc {
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

func writeExe(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write placeholder %s: %v", name, err)
	}
}

func TestRegistry_v1TableMatchesSpec(t *testing.T) {
	got := Registry()

	wantNames := []string{"brew", "npm", "pnpm", "rustup", "uv", "gh-extensions", "hukou"}
	if len(got) != len(wantNames) {
		t.Fatalf("registry size = %d, want %d", len(got), len(wantNames))
	}
	for i, name := range wantNames {
		if got[i].Name != name {
			t.Fatalf("row %d = %q, want %q (order matters: it is execution order)", i, got[i].Name, name)
		}
	}

	byName := map[string]Manager{}
	for _, m := range got {
		byName[m.Name] = m
	}

	// brew runs two steps, in order.
	if want := [][]string{{"brew", "update"}, {"brew", "upgrade"}}; !reflect.DeepEqual(byName["brew"].Commands, want) {
		t.Fatalf("brew commands = %v, want %v", byName["brew"].Commands, want)
	}
	// gh-extensions detects via the `gh` binary, not its own name.
	if byName["gh-extensions"].DetectBinary != "gh" {
		t.Fatalf("gh-extensions DetectBinary = %q, want gh", byName["gh-extensions"].DetectBinary)
	}
	// hukou is internal: no detect binary, always in-process.
	hukou := byName["hukou"]
	if !hukou.Internal || hukou.DetectBinary != "" {
		t.Fatalf("hukou row = %+v, want internal with empty DetectBinary", hukou)
	}

	// Every command must be a non-empty argv slice (never a shell string).
	for _, m := range got {
		if len(m.Commands) == 0 {
			t.Fatalf("%s has no commands", m.Name)
		}
		for _, argv := range m.Commands {
			if len(argv) == 0 {
				t.Fatalf("%s has an empty argv", m.Name)
			}
		}
	}
}

func TestDetect_fakePATHExactMatch(t *testing.T) {
	dir := t.TempDir()
	// Only brew and npm are "installed" on this fake PATH.
	writeExe(t, dir, "brew")
	writeExe(t, dir, "npm")

	detected := Detect(Registry(), dirLookPath(dir))

	want := map[string]bool{
		"brew":          true,
		"npm":           true,
		"pnpm":          false,
		"rustup":        false,
		"uv":            false,
		"gh-extensions": false,
		"hukou":         true, // internal: always available.
	}
	for _, d := range detected {
		if d.Available != want[d.Name] {
			t.Fatalf("%s available = %v, want %v", d.Name, d.Available, want[d.Name])
		}
		if d.Name == "brew" && d.BinaryPath != filepath.Join(dir, "brew") {
			t.Fatalf("brew BinaryPath = %q, want %q", d.BinaryPath, filepath.Join(dir, "brew"))
		}
		if !d.Available && d.BinaryPath != "" {
			t.Fatalf("%s absent but BinaryPath = %q", d.Name, d.BinaryPath)
		}
	}
}

func TestDetect_internalNeverProbesPATH(t *testing.T) {
	var probed []string
	recording := func(file string) (string, error) {
		probed = append(probed, file)
		return "", exec.ErrNotFound
	}

	detected := Detect(Registry(), recording)

	// hukou is internal and must be available without any lookPath probe.
	var hukou Detected
	for _, d := range detected {
		if d.Name == "hukou" {
			hukou = d
		}
	}
	if !hukou.Available || hukou.BinaryPath != "" {
		t.Fatalf("internal hukou = %+v, want available with no binary path", hukou)
	}
	// lookPath is called once per external manager (6), never for hukou.
	if len(probed) != 6 {
		t.Fatalf("lookPath calls = %d (%v), want 6 externals only", len(probed), probed)
	}
	for _, name := range probed {
		if name == "" {
			t.Fatalf("lookPath probed hukou's empty binary: %v", probed)
		}
	}
}

func TestFilter_only(t *testing.T) {
	got, err := Filter(Registry(), []string{"brew", "hukou"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if names := managerNames(got); !reflect.DeepEqual(names, []string{"brew", "hukou"}) {
		t.Fatalf("only filter = %v, want [brew hukou]", names)
	}
}

func TestFilter_skip(t *testing.T) {
	got, err := Filter(Registry(), nil, []string{"npm", "pnpm"})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range got {
		if m.Name == "npm" || m.Name == "pnpm" {
			t.Fatalf("skip left %s in the set", m.Name)
		}
	}
	if len(got) != 5 {
		t.Fatalf("skip result size = %d, want 5", len(got))
	}
}

func TestFilter_onlyThenSkip(t *testing.T) {
	got, err := Filter(Registry(), []string{"brew", "npm", "hukou"}, []string{"npm"})
	if err != nil {
		t.Fatal(err)
	}
	if names := managerNames(got); !reflect.DeepEqual(names, []string{"brew", "hukou"}) {
		t.Fatalf("only+skip = %v, want [brew hukou]", names)
	}
}

func TestFilter_unknownName(t *testing.T) {
	if _, err := Filter(Registry(), []string{"bogus"}, nil); err == nil {
		t.Fatal("expected error for unknown --only name")
	}
	if _, err := Filter(Registry(), nil, []string{"nope"}); err == nil {
		t.Fatal("expected error for unknown --skip name")
	}
}

func managerNames(ms []Manager) []string {
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, m.Name)
	}
	return out
}
