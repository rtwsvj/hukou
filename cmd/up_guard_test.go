package cmd

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// executorImportPath is the one package allowed to launch manager subprocesses.
const executorImportPath = "github.com/rtwsvj/hukou/internal/orchestrate/executor"

// upDryRunEntry is the plan-only entry function runUp dispatches to for
// --dry-run; defaultInventory is the seam runUp passes it. Together they are
// the roots of the dry-run call chain.
var upDryRunRoots = []string{"doUpPlan", "defaultInventory"}

// parseCmdPackage parses every non-test source file of the cmd package and
// returns, per file name, its AST plus a map of package-level function
// declarations (methods keyed as Type.Name).
func parseCmdPackage(t *testing.T) (map[string]*ast.File, *token.FileSet) {
	t.Helper()
	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]*ast.File{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		files[name] = f
	}
	if len(files) == 0 {
		t.Fatal("no cmd source files parsed; guard would be vacuous")
	}
	return files, fset
}

// fileImports reports whether the file imports the given path.
func fileImports(f *ast.File, path string) bool {
	for _, imp := range f.Imports {
		if strings.Trim(imp.Path.Value, `"`) == path {
			return true
		}
	}
	return false
}

// TestUpDryRunChainCannotReachExecutor is the structural guard the U2 slice
// owes the 2026-07-17 deferral ruling, aimed at the actual requirement: the
// dry-run CALL CHAIN of `hukou up` must be unable to reach the executor
// subpackage. It parses the cmd package and asserts, for every package-level
// function transitively reachable from the dry-run roots (doUpPlan and the
// defaultInventory seam runUp hands it):
//
//  1. the defining file does not import the executor package (so no reachable
//     code can even name an executor symbol), and
//  2. consequently the whole reachable graph is free of executor references.
//
// Reachability is over-approximated: any identifier inside a reachable function
// body that names a package-level cmd function counts as a call. An
// over-approximation can only make the guard stricter, never let a real
// dependency slip through. The two orchestrate-level guards
// (internal/orchestrate/executor_boundary_test.go) remain as defense in depth.
func TestUpDryRunChainCannotReachExecutor(t *testing.T) {
	files, _ := parseCmdPackage(t)

	// Index package-level functions and methods by name -> (decl, file).
	type fnDecl struct {
		decl     *ast.FuncDecl
		file     *ast.File
		fileName string
	}
	funcs := map[string]fnDecl{}
	for name, f := range files {
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok {
				continue
			}
			// Methods are indexed by bare name too: the over-approximation treats
			// any matching identifier as reachable, which errs strict.
			funcs[fd.Name.Name] = fnDecl{fd, f, name}
		}
	}

	for _, root := range upDryRunRoots {
		if _, ok := funcs[root]; !ok {
			t.Fatalf("dry-run root %q not found in cmd package; the guard no longer covers the real entry", root)
		}
	}

	reached := map[string]bool{}
	queue := append([]string(nil), upDryRunRoots...)
	for _, r := range queue {
		reached[r] = true
	}
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		fn := funcs[name]

		if fileImports(fn.file, executorImportPath) {
			t.Errorf("dry-run-reachable function %s is defined in %s, which imports %s; "+
				"the dry-run call chain must stay in executor-free files",
				name, fn.fileName, executorImportPath)
		}

		if fn.decl.Body == nil {
			continue
		}
		ast.Inspect(fn.decl.Body, func(n ast.Node) bool {
			id, ok := n.(*ast.Ident)
			if !ok {
				return true
			}
			if _, isFn := funcs[id.Name]; isFn && !reached[id.Name] {
				reached[id.Name] = true
				queue = append(queue, id.Name)
			}
			return true
		})
	}

	// The guard is only meaningful if the cobra dispatch actually routes
	// --dry-run to the guarded entry: runUp must reference every root.
	runUp, ok := funcs["runUp"]
	if !ok {
		t.Fatal("runUp not found; dispatch wiring changed under the guard")
	}
	for _, root := range upDryRunRoots {
		found := false
		ast.Inspect(runUp.decl.Body, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok && id.Name == root {
				found = true
			}
			return true
		})
		if !found {
			t.Errorf("runUp does not reference dry-run root %q; the guarded entry is no longer the real dispatch target", root)
		}
	}

	// And the split must be real: the execution entry exists and is NOT part of
	// the dry-run reachable set.
	if _, ok := funcs["runUpExecute"]; !ok {
		t.Fatal("runUpExecute not found; the plan/execute entry split disappeared")
	}
	if reached["runUpExecute"] || reached["doUpExecute"] {
		t.Errorf("execution entry is reachable from the dry-run roots; reached set: %v", reached)
	}
}

// TestUpPlanFilesDoNotImportExecutor pins the file-level rule directly: the
// dispatch file (up.go) and the plan file (up_plan.go) must not import the
// executor subpackage, and within cmd only up_exec.go may.
func TestUpPlanFilesDoNotImportExecutor(t *testing.T) {
	files, _ := parseCmdPackage(t)
	for name, f := range files {
		imports := fileImports(f, executorImportPath)
		if name == "up_exec.go" {
			if !imports {
				t.Errorf("up_exec.go no longer imports the executor; the guard map is stale")
			}
			continue
		}
		if imports {
			t.Errorf("%s imports %s; only up_exec.go may", name, executorImportPath)
		}
	}
}
