package plan

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const (
	planPkgPath     = "github.com/rtwsvj/hukou/internal/orchestrate/plan"
	executorPkgPath = "github.com/rtwsvj/hukou/internal/orchestrate/executor"
	osExecPkgPath   = "os/exec"
)

// forbiddenDeps is the single assertion helper both guard tests share: it runs
// `go list -deps <pattern>` from dir and returns which of the forbidden import
// paths appear in the pattern's transitive dependency set. Package-level truth
// only — no source parsing, no call-graph heuristics.
func forbiddenDeps(t *testing.T, dir, pattern string, forbidden ...string) []string {
	t.Helper()
	cmd := exec.Command("go", "list", "-deps", pattern)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps %s (in %s): %v\n%s", pattern, dir, err, out)
	}
	deps := make(map[string]struct{})
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		deps[strings.TrimSpace(line)] = struct{}{}
	}
	var hit []string
	for _, f := range forbidden {
		if _, ok := deps[f]; ok {
			hit = append(hit, f)
		}
	}
	return hit
}

// TestPlanDepsExcludeExecutorAndOsExec is a defense-in-depth layer of the U2
// executor boundary (the primary guard is the repo-wide go/ast fence in
// internal/orchestrate/execution_fence_test.go): the plan package — the whole
// of the dry-run assembly/rendering logic — must have a transitive dependency
// set containing neither the executor subpackage (the only place that launches
// manager subprocesses) nor os/exec itself. A package that cannot import
// subprocess machinery cannot execute a command, whatever its call paths do.
func TestPlanDepsExcludeExecutorAndOsExec(t *testing.T) {
	if hit := forbiddenDeps(t, ".", planPkgPath, executorPkgPath, osExecPkgPath); len(hit) != 0 {
		t.Fatalf("the plan package must stay free of subprocess machinery, but its transitive deps contain: %v", hit)
	}
}

// TestGuardCatchesSyntheticViolations proves the guard actually bites, with a
// real runnable violation rather than a claim: it writes two compilable
// synthetic packages into temporary subdirectories of this package (inside the
// module, so `go list` resolves them exactly like production code), runs the
// SAME forbiddenDeps helper the primary guard uses, and requires the helper to
// flag them — one importing the executor subpackage, one importing os/exec
// directly.
func TestGuardCatchesSyntheticViolations(t *testing.T) {
	writeSynthetic := func(imports string) string {
		t.Helper()
		dir, err := os.MkdirTemp(".", "synthbad_")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(dir) })
		src := "// Package synthbad exists only inside TestGuardCatchesSyntheticViolations:\n" +
			"// a deliberately violating package used to prove the dependency guard fires.\n" +
			"package synthbad\n\nimport (\n" + imports + "\n)\n"
		if err := os.WriteFile(filepath.Join(dir, "bad.go"), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		return "./" + filepath.Base(dir)
	}

	// Violation 1: a plan-like package that imports the executor subpackage.
	// The helper must flag the executor path; os/exec is flagged too because
	// the executor itself depends on it — both prove the guard would have
	// caught the import.
	badExecutor := writeSynthetic("\t_ \"" + executorPkgPath + "\"")
	hit := forbiddenDeps(t, ".", badExecutor, executorPkgPath, osExecPkgPath)
	if !slices.Contains(hit, executorPkgPath) {
		t.Fatalf("guard failed to flag a synthetic package importing the executor: hits = %v", hit)
	}
	if !slices.Contains(hit, osExecPkgPath) {
		t.Fatalf("guard failed to flag os/exec pulled in via the executor: hits = %v", hit)
	}

	// Violation 2: a package that imports os/exec directly (no executor). The
	// helper must flag os/exec and must NOT flag the executor — proving it
	// reports precisely what is in the dependency set.
	badOsExec := writeSynthetic("\t_ \"os/exec\"")
	hit = forbiddenDeps(t, ".", badOsExec, executorPkgPath, osExecPkgPath)
	if !slices.Contains(hit, osExecPkgPath) {
		t.Fatalf("guard failed to flag a synthetic package importing os/exec: hits = %v", hit)
	}
	if slices.Contains(hit, executorPkgPath) {
		t.Fatalf("guard over-reported the executor for a package that never pulls it: hits = %v", hit)
	}
}
