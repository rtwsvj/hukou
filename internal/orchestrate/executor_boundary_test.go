package orchestrate

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	orchestratePkg = "github.com/rtwsvj/hukou/internal/orchestrate"
	executorPkg    = "github.com/rtwsvj/hukou/internal/orchestrate/executor"
)

// TestExecutorNotInOrchestrateDeps is the primary structural guard for the U2
// executor boundary (docs/09-decision-log.md, 2026-07-17; docs/specs/phase3-up.md
// U2 acceptance). `go list -deps` proves the orchestrate package — which is the
// entirety of the dry-run planning/diff computation — has no transitive
// dependency on the executor subpackage, the sole place a manager subprocess is
// launched. If the dry-run call chain can't even reach the executor package, it
// cannot execute a manager command.
func TestExecutorNotInOrchestrateDeps(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", orchestratePkg).CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps %s: %v\n%s", orchestratePkg, err, out)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.TrimSpace(line) == executorPkg {
			t.Fatalf("orchestrate package must not depend on the executor subpackage, "+
				"but %s appears in its transitive deps; the dry-run call chain would then "+
				"be able to reach command execution", executorPkg)
		}
	}
}

// TestOrchestratePackageHasNoCommandExecution statically parses every non-test
// source file of the orchestrate package (excluding the executor subdir, which
// this package does not recurse into) and asserts none of them invoke
// exec.Command or exec.CommandContext. Detection legitimately uses exec.LookPath
// — which only stats the filesystem — so the guard targets command *execution*,
// not the os/exec import. Command execution is confined to the executor package.
func TestOrchestratePackageHasNoCommandExecution(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	checked := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		checked++
		file, err := parser.ParseFile(fset, e.Name(), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", e.Name(), err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || pkg.Name != "exec" {
				return true
			}
			if sel.Sel.Name == "Command" || sel.Sel.Name == "CommandContext" {
				pos := fset.Position(sel.Pos())
				t.Errorf("%s: orchestrate must not launch subprocesses, found exec.%s "+
					"(command execution belongs only in the executor subpackage)", pos, sel.Sel.Name)
			}
			return true
		})
	}
	if checked == 0 {
		t.Fatal("no orchestrate source files were parsed; guard would be vacuous")
	}
}

// TestExecutorSubpackageExists sanity-checks that the guarded subpackage is the
// real one on disk, so the deps guard above is meaningful rather than guarding a
// package that was renamed away.
func TestExecutorSubpackageExists(t *testing.T) {
	if _, err := os.Stat(filepath.Join("executor", "executor.go")); err != nil {
		t.Fatalf("expected executor subpackage at ./executor/executor.go: %v", err)
	}
}
