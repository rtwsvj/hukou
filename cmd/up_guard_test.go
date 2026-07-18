package cmd

import (
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// executorImportPath is the one package allowed to launch manager subprocesses.
const executorImportPath = "github.com/rtwsvj/hukou/internal/orchestrate/executor"

// TestOnlyUpExecImportsExecutor is an AUXILIARY, file-level check and nothing
// more: it asserts which cmd source files import the executor subpackage
// (exactly one — up_exec.go). It does not analyze calls or reachability; a
// file-level import list cannot prove what the dry-run path executes. The
// PRIMARY guard is package-level and lives in
// internal/orchestrate/plan/guard_test.go (TestPlanDepsExcludeExecutorAndOsExec):
// the plan package holding all dry-run assembly/rendering has a transitive
// dependency set free of both the executor subpackage and os/exec. Runtime
// depth comes from the U1 behavioral stub (TestUp_dryRunNeverInvokesRunner in
// up_test.go).
func TestOnlyUpExecImportsExecutor(t *testing.T) {
	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		checked++
		imports := false
		for _, imp := range f.Imports {
			if strings.Trim(imp.Path.Value, `"`) == executorImportPath {
				imports = true
			}
		}
		if name == "up_exec.go" {
			if !imports {
				t.Errorf("up_exec.go no longer imports the executor; this auxiliary check is stale")
			}
			continue
		}
		if imports {
			t.Errorf("%s imports %s; within cmd only up_exec.go may", name, executorImportPath)
		}
	}
	if checked == 0 {
		t.Fatal("no cmd source files parsed; check would be vacuous")
	}
}
