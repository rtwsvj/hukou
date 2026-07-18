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

// osExecAllowlist are the only non-test files outside the executor package
// permitted to IMPORT os/exec. Today that is exactly one file: the LookPath
// wrapper. Allowlisted files are still scanned with the execution-primitive
// selector rules (Command/CommandContext/Cmd remain violations there), so the
// allowlist grants name resolution, never execution.
var osExecAllowlist = map[string]bool{
	"internal/lookpath/lookpath.go": true,
}

// importBindings is the per-file resolution of local identifiers to the import
// paths the fence cares about. Aliases are resolved here, so a violation cannot
// hide behind `import x "os/exec"` — matching is by bound import path, never by
// the literal identifier spelled in the source.
type importBindings struct {
	osExecNames  map[string]bool // local names bound to "os/exec"
	osNames      map[string]bool // local names bound to "os"
	syscallNames map[string]bool // local names bound to "syscall"
	dotOs        bool            // `import . "os"`
	dotSyscall   bool            // `import . "syscall"`
}

// scanExecViolations returns "pos: description" strings for every
// process-execution primitive in f. Rules, in order:
//
//  1. Importing "os/exec" in ANY form (named, aliased, dot, blank) is itself a
//     violation unless allowOsExecImport is set (the fence sets it only for the
//     osExecAllowlist file). os/exec's only reason to exist outside the
//     executor package would be execution; banning the import by PATH covers
//     every alias and dot form in one stroke.
//  2. A dot import of "os/exec" is a violation even where the plain import is
//     allowlisted (no file needs it).
//  3. "os" and "syscall" have many legitimate uses and cannot be banned, so
//     their local names are resolved from the import table and the selectors
//     .StartProcess (os) and .Exec/.ForkExec (syscall) are violations on any
//     identifier bound to those paths — whatever the alias. If "os" or
//     "syscall" is dot-imported, bare calls to StartProcess/Exec/ForkExec are
//     matched instead.
//
// No receiver-chain analysis is needed, and none is attempted: an *exec.Cmd
// can only come into existence through exec.Command, exec.CommandContext, or
// the exec.Cmd type — and every one of those construction points is sealed at
// the import-binding level above (outside the executor, os/exec cannot even be
// imported). With no way to construct or name the type, no chained
// .Start/.Run/.Output/.CombinedOutput call can exist on the guarded tree.
func scanExecViolations(fset *token.FileSet, f *ast.File, allowOsExecImport bool) []string {
	b := importBindings{
		osExecNames:  map[string]bool{},
		osNames:      map[string]bool{},
		syscallNames: map[string]bool{},
	}
	var out []string

	for _, imp := range f.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		pos := fset.Position(imp.Pos())
		local := ""
		if imp.Name != nil {
			local = imp.Name.Name
		}
		switch path {
		case "os/exec":
			if !allowOsExecImport {
				out = append(out, fmt.Sprintf(`%s: import "os/exec" (any form) is banned outside the executor package`, pos))
			} else if local == "." {
				out = append(out, fmt.Sprintf(`%s: dot import of "os/exec" is a violation even where the plain import is allowlisted`, pos))
			}
			switch local {
			case ".", "_":
			case "":
				b.osExecNames["exec"] = true
			default:
				b.osExecNames[local] = true
			}
		case "os":
			switch local {
			case ".":
				b.dotOs = true
			case "_":
			case "":
				b.osNames["os"] = true
			default:
				b.osNames[local] = true
			}
		case "syscall":
			switch local {
			case ".":
				b.dotSyscall = true
			case "_":
			case "":
				b.syscallNames["syscall"] = true
			default:
				b.syscallNames[local] = true
			}
		}
	}

	ast.Inspect(f, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.SelectorExpr:
			x, ok := node.X.(*ast.Ident)
			if !ok {
				return true
			}
			pos := fset.Position(node.Pos())
			sel := node.Sel.Name
			switch {
			case b.osExecNames[x.Name] && (sel == "Command" || sel == "CommandContext" || sel == "Cmd"):
				out = append(out, fmt.Sprintf("%s: %s.%s (bound to os/exec)", pos, x.Name, sel))
			case b.osNames[x.Name] && sel == "StartProcess":
				out = append(out, fmt.Sprintf("%s: %s.StartProcess (bound to os)", pos, x.Name))
			case b.syscallNames[x.Name] && (sel == "Exec" || sel == "ForkExec"):
				out = append(out, fmt.Sprintf("%s: %s.%s (bound to syscall)", pos, x.Name, sel))
			}
		case *ast.CallExpr:
			// Dot-imported os/syscall expose the primitives as bare
			// identifiers; match them in call position. (Dot-imported os/exec
			// needs no bare matching: the dot import itself is always flagged.)
			id, ok := node.Fun.(*ast.Ident)
			if !ok {
				return true
			}
			pos := fset.Position(node.Pos())
			switch {
			case b.dotOs && id.Name == "StartProcess":
				out = append(out, fmt.Sprintf("%s: StartProcess (dot-imported os)", pos))
			case b.dotSyscall && (id.Name == "Exec" || id.Name == "ForkExec"):
				out = append(out, fmt.Sprintf("%s: %s (dot-imported syscall)", pos, id.Name))
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
// guard (docs/09-decision-log.md, 2026-07-18): a repo-wide go/ast scan
// asserting that no non-_test.go file outside internal/orchestrate/executor
// can execute a process — importing os/exec at all is a violation (any alias
// or dot form, resolved by import path), except for the single allowlisted
// LookPath wrapper which is still denied every execution primitive; and
// os.StartProcess / syscall.Exec / syscall.ForkExec are matched through
// per-file import bindings so no alias can hide them.
func TestNoExecutionPrimitivesOutsideExecutor(t *testing.T) {
	root := moduleRoot(t)
	execDir := filepath.Join(root, filepath.FromSlash(executorPkgRel))
	vendorDir := filepath.Join(root, "vendor")

	// The allowlist must name real files, or it silently guards nothing.
	for rel := range osExecAllowlist {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			t.Fatalf("osExecAllowlist entry %s does not exist: %v", rel, err)
		}
	}

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
			// "synthbad_" is the reserved prefix for EPHEMERAL deliberately
			// violating packages created at runtime by the concurrent guard
			// tests (internal/orchestrate/plan/guard_test.go); the full suite
			// runs test binaries in parallel, so this walker can otherwise
			// catch one mid-existence and report a phantom violation. Skipping
			// the prefix does not weaken the fence: it guards against
			// accidental primitive use, and parking real code under a
			// directory named synthbad_* would be deliberate evasion, which no
			// in-repo static check can stop.
			if strings.HasPrefix(d.Name(), "synthbad_") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		f, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return fmt.Errorf("parse %s: %w", path, perr)
		}
		scanned++
		for _, v := range scanExecViolations(fset, f, osExecAllowlist[filepath.ToSlash(rel)]) {
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

// TestExecutionFenceCatchesSyntheticViolations proves the fence bites, with
// real parsed sources rather than a claim: each snippet is fed to the SAME
// scanExecViolations the repo-wide fence uses and must be flagged AT the
// expected line; clean snippets must pass. The three review-mandated bypass
// classes — aliased os/exec, dot-imported os/exec, aliased os.StartProcess —
// are all here.
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
	// requireHit asserts at least one violation was reported at the given line.
	requireHit := func(name string, got []string, line int) {
		t.Helper()
		frag := fmt.Sprintf("synthetic.go:%d:", line)
		for _, v := range got {
			if strings.Contains(v, frag) {
				return
			}
		}
		t.Errorf("fence failed to flag %s at line %d; got: %v", name, line, got)
	}

	violating := []struct {
		name string
		src  string
		line int // line the violation must be reported at
	}{
		{"aliased os/exec import (bypass class 1)", `package p
import x "os/exec"
func f() { _ = x.Command("ls") }`, 2},
		{"aliased os/exec selector (bypass class 1)", `package p
import x "os/exec"
func f() { _ = x.Command("ls") }`, 3},
		{"dot-imported os/exec (bypass class 2)", `package p
import . "os/exec"
func f() { _ = Command("ls") }`, 2},
		{"aliased os.StartProcess (bypass class 3)", `package p
import o "os"
func f() { _, _ = o.StartProcess("/bin/ls", nil, nil) }`, 3},
		{"plain exec.Command import ban", `package p
import "os/exec"
func f() { _ = exec.Command("ls") }`, 2},
		{"aliased exec.Cmd literal", `package p
import x "os/exec"
func f() { _ = &x.Cmd{} }`, 3},
		{"plain syscall.ForkExec", `package p
import "syscall"
func f() { _, _ = syscall.ForkExec("/bin/ls", nil, nil) }`, 3},
		{"aliased syscall.Exec", `package p
import sc "syscall"
func f() { _ = sc.Exec("/bin/ls", nil, nil) }`, 3},
		{"dot-imported syscall bare ForkExec", `package p
import . "syscall"
func f() { _, _ = ForkExec("/bin/ls", nil, nil) }`, 3},
		{"blank os/exec import still banned", `package p
import _ "os/exec"
`, 2},
	}
	for _, tc := range violating {
		got := scanExecViolations(fset, parse(tc.src), false)
		if len(got) == 0 {
			t.Errorf("fence failed to flag %s entirely", tc.name)
			continue
		}
		requireHit(tc.name, got, tc.line)
	}

	// Clean in default mode: no os/exec import, unrelated .Run receiver.
	cleanDefault := `package p
func f(c interface{ Run() error }) { _ = c.Run() }`
	if got := scanExecViolations(fset, parse(cleanDefault), false); len(got) != 0 {
		t.Errorf("fence over-reported a clean snippet: %v", got)
	}

	// Allowlist mode (the LookPath wrapper's regime): the plain import plus
	// LookPath is clean, but execution selectors and the dot form stay banned.
	lookPathSrc := `package p
import "os/exec"
func f() { _, _ = exec.LookPath("go") }`
	if got := scanExecViolations(fset, parse(lookPathSrc), true); len(got) != 0 {
		t.Errorf("allowlist mode over-reported the LookPath wrapper pattern: %v", got)
	}
	// The very same source is a violation outside the allowlist.
	if got := scanExecViolations(fset, parse(lookPathSrc), false); len(got) == 0 {
		t.Error("default mode failed to flag an os/exec import used only for LookPath")
	}
	allowedButExecuting := `package p
import x "os/exec"
func f() { _ = x.CommandContext(nil, "ls") }`
	got := scanExecViolations(fset, parse(allowedButExecuting), true)
	requireHit("execution selector inside an allowlisted file", got, 3)
	allowedButDot := `package p
import . "os/exec"
func f() { _, _ = LookPath("go") }`
	got = scanExecViolations(fset, parse(allowedButDot), true)
	requireHit("dot import inside an allowlisted file", got, 2)
}
