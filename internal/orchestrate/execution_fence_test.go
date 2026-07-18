package orchestrate

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// executorPkgRel is the one package permitted to use execution primitives; the
// fence excludes it (and only it) from the repo-wide scan.
const executorPkgRel = "internal/orchestrate/executor"

// execCmdMethods are the *exec.Cmd run methods. Detecting them is belt-and-
// suspenders: you cannot obtain an *exec.Cmd without exec.Command /
// exec.CommandContext / an exec.Cmd literal, all of which the fence already
// flags — so the constructor/type checks are the load-bearing part.
var execCmdMethods = map[string]bool{
	"Start": true, "Run": true, "Output": true, "CombinedOutput": true,
}

// scanExecViolations returns human-readable "pos: primitive" strings for every
// process-execution primitive used in f: exec.Command / exec.CommandContext /
// the exec.Cmd type, os.StartProcess, syscall.Exec / syscall.ForkExec, and —
// only in files that import os/exec, to bound false positives — calls to the
// *exec.Cmd run methods. It is the single scanner shared by the repo-wide fence
// and the synthetic negative test, so the negative test proves the exact code
// the fence relies on.
func scanExecViolations(fset *token.FileSet, f *ast.File) []string {
	importsOsExec := false
	for _, imp := range f.Imports {
		if strings.Trim(imp.Path.Value, `"`) == "os/exec" {
			importsOsExec = true
		}
	}

	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pos := fset.Position(sel.Pos())
		x, ok := sel.X.(*ast.Ident)
		if !ok {
			// Receiver is not a bare identifier (e.g. deps.exec.RunManager);
			// only the bare-ident method form is considered below.
			return true
		}
		switch x.Name {
		case "exec":
			if sel.Sel.Name == "Command" || sel.Sel.Name == "CommandContext" || sel.Sel.Name == "Cmd" {
				out = append(out, fmt.Sprintf("%s: exec.%s", pos, sel.Sel.Name))
			}
		case "os":
			if sel.Sel.Name == "StartProcess" {
				out = append(out, fmt.Sprintf("%s: os.StartProcess", pos))
			}
		case "syscall":
			if sel.Sel.Name == "Exec" || sel.Sel.Name == "ForkExec" {
				out = append(out, fmt.Sprintf("%s: syscall.%s", pos, sel.Sel.Name))
			}
		default:
			if importsOsExec && execCmdMethods[sel.Sel.Name] {
				out = append(out, fmt.Sprintf("%s: (os/exec-importing file) .%s", pos, sel.Sel.Name))
			}
		}
		return true
	})
	return out
}

// moduleRoot walks up from the test's working directory to the directory that
// holds go.mod.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found walking up from the test directory")
		}
		dir = parent
	}
}

// TestNoExecutionPrimitivesOutsideExecutor is the PRIMARY U2 executor-boundary
// guard (docs/09-decision-log.md, 2026-07-18): a repo-wide go/ast scan asserting
// that no non-_test.go file outside internal/orchestrate/executor uses any
// process-execution primitive. Command execution lives in exactly one package;
// everything else — including the whole dry-run call chain — is mechanically
// proven incapable of launching a subprocess.
func TestNoExecutionPrimitivesOutsideExecutor(t *testing.T) {
	root := moduleRoot(t)
	execDir := filepath.Join(root, filepath.FromSlash(executorPkgRel))
	vendorDir := filepath.Join(root, "vendor")

	fset := token.NewFileSet()
	scanned := 0
	var violations []string

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path == vendorDir || path == execDir {
				return filepath.SkipDir // the one package allowed to execute
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return fmt.Errorf("parse %s: %w", path, perr)
		}
		scanned++
		for _, v := range scanExecViolations(fset, f) {
			rel, _ := filepath.Rel(root, path)
			violations = append(violations, rel+" -> "+v)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if scanned == 0 {
		t.Fatal("fence scanned no files; it would be vacuous")
	}
	if len(violations) != 0 {
		t.Fatalf("execution primitives found outside %s (they belong only there):\n  %s",
			executorPkgRel, strings.Join(violations, "\n  "))
	}
}

// TestExecutionFenceCatchesSyntheticViolations proves the fence bites, with real
// compilable snippets rather than a claim: each is parsed and fed to the SAME
// scanExecViolations the repo-wide fence uses, and must be flagged; a clean
// snippet (exec.LookPath — allowed, it only stats) must not be.
func TestExecutionFenceCatchesSyntheticViolations(t *testing.T) {
	fset := token.NewFileSet()
	parse := func(src string) *ast.File {
		t.Helper()
		f, err := parser.ParseFile(fset, "synthetic.go", src, 0)
		if err != nil {
			t.Fatalf("parse synthetic: %v", err)
		}
		return f
	}

	violating := map[string]string{
		"exec.Command": `package p
import "os/exec"
func f() { _ = exec.Command("ls") }`,
		"exec.CommandContext": `package p
import (
	"context"
	"os/exec"
)
func f(ctx context.Context) { _ = exec.CommandContext(ctx, "ls") }`,
		"exec.Cmd literal": `package p
import "os/exec"
func f() { _ = &exec.Cmd{} }`,
		"os.StartProcess": `package p
import "os"
func f() { _, _ = os.StartProcess("/bin/ls", nil, nil) }`,
		"syscall.ForkExec": `package p
import "syscall"
func f() { _, _ = syscall.ForkExec("/bin/ls", nil, nil) }`,
		"Cmd.Run method in os/exec-importing file": `package p
import "os/exec"
func f(c interface{ Run() error }) { _ = exec.ErrNotFound; _ = c.Run() }`,
	}
	for name, src := range violating {
		if got := scanExecViolations(fset, parse(src)); len(got) == 0 {
			t.Errorf("fence failed to flag a %s violation", name)
		}
	}

	// Allowed: exec.LookPath only stats the filesystem; a file that never imports
	// os/exec calling an unrelated .Run() must not trip the method heuristic.
	clean := map[string]string{
		"exec.LookPath": `package p
import "os/exec"
func f() { _, _ = exec.LookPath("go") }`,
		"unrelated .Run without os/exec": `package p
func f(c interface{ Run() error }) { _ = c.Run() }`,
	}
	for name, src := range clean {
		if got := scanExecViolations(fset, parse(src)); len(got) != 0 {
			t.Errorf("fence over-reported a clean %s snippet: %v", name, got)
		}
	}
}
