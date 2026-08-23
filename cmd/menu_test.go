package cmd

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

// fakeFileInfo is a minimal os.FileInfo whose Mode carries an arbitrary
// os.FileMode — used to stand in for a character-device stdout (a TTY) or a
// plain pipe without needing a real terminal in tests.
type fakeFileInfo struct{ mode os.FileMode }

func (f fakeFileInfo) Name() string       { return "stdout" }
func (f fakeFileInfo) Size() int64        { return 0 }
func (f fakeFileInfo) Mode() os.FileMode  { return f.mode }
func (f fakeFileInfo) ModTime() time.Time { return time.Time{} }
func (f fakeFileInfo) IsDir() bool        { return false }
func (f fakeFileInfo) Sys() any           { return nil }

const (
	ttyFileInfoMode  = os.ModeCharDevice | 0o620
	pipeFileInfoMode = os.FileMode(0o640) // no ModeCharDevice bit
)

func TestShouldEnterMenu(t *testing.T) {
	cleanEnv := func(string) string { return "" }
	noMenuEnv := func(key string) string {
		if key == "HUKOU_NO_MENU" {
			return "1"
		}
		return ""
	}
	emptyNoMenuEnv := func(key string) string {
		if key == "HUKOU_NO_MENU" {
			return ""
		}
		return ""
	}
	dumbTermEnv := func(key string) string {
		if key == "TERM" {
			return "dumb"
		}
		return ""
	}
	ttyOut, pipeOut := true, false
	ttyIn, pipeIn := true, false

	tests := []struct {
		name string
		args []string
		env  func(string) string
		outT bool
		inT  bool
		want bool
	}{
		{"interactive tty", nil, cleanEnv, ttyOut, ttyIn, true},
		{"nil getenv treated as clean", nil, nil, ttyOut, ttyIn, true},
		{"empty HUKOU_NO_MENU still enters", nil, emptyNoMenuEnv, ttyOut, ttyIn, true},
		{"HUKOU_NO_MENU set opts out", nil, noMenuEnv, ttyOut, ttyIn, false},
		{"dumb TERM opts out", nil, dumbTermEnv, ttyOut, ttyIn, false},
		{"piped stdout falls back to help", nil, cleanEnv, pipeOut, ttyIn, false},
		{"non-tty stdin falls back to help", nil, cleanEnv, ttyOut, pipeIn, false},
		{"positional args skip menu", []string{"list"}, cleanEnv, ttyOut, ttyIn, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldEnterMenu(tc.args, tc.env, tc.outT, tc.inT); got != tc.want {
				t.Fatalf("shouldEnterMenu(%q) = %v, want %v", tc.args, got, tc.want)
			}
		})
	}
}

func TestBareRootDispatch(t *testing.T) {
	origOutT, origInT, origRunner := stdoutIsTTY, stdinIsTTY, menuRunner
	origOut, origErr := rootCmd.OutOrStdout(), rootCmd.ErrOrStderr()
	defer func() {
		stdoutIsTTY, stdinIsTTY, menuRunner = origOutT, origInT, origRunner
		rootCmd.SetOut(origOut)
		rootCmd.SetErr(origErr)
	}()

	out, errb := &bytes.Buffer{}, &bytes.Buffer{}
	rootCmd.SetOut(out)
	rootCmd.SetErr(errb)
	// runRoot reads the real environment (HUKOU_NO_MENU / TERM), so pin both:
	// inherited dumb-terminal or opt-out settings would otherwise flip the
	// tty-path assertion off depending on where the suite runs.
	t.Setenv("HUKOU_NO_MENU", "")
	t.Setenv("TERM", "xterm-256color")
	var ran int
	menuRunner = func(_, _ io.Writer) error { ran++; return nil }

	// Interactive stdin+stdout + bare invocation -> menu runner called, no help.
	stdoutIsTTY = func() bool { return true }
	stdinIsTTY = func() bool { return true }
	if err := runRoot(rootCmd, nil); err != nil {
		t.Fatalf("runRoot (tty) = %v, want nil", err)
	}
	if ran != 1 {
		t.Fatalf("menu runner ran %d times on tty path, want 1", ran)
	}
	if out.Len() != 0 {
		t.Fatalf("help unexpectedly printed on tty path: %q", out.String())
	}

	// Piped stdout -> help, runner untouched.
	ran = 0
	stdoutIsTTY = func() bool { return false }
	if err := runRoot(rootCmd, nil); err != nil {
		t.Fatalf("runRoot (pipe) = %v, want nil", err)
	}
	if ran != 0 {
		t.Fatal("menu runner must not run when stdout is not a character device")
	}
	if !strings.Contains(out.String(), "Usage") {
		t.Fatalf("help output missing on pipe path: %q", out.String())
	}

	// Stdin not a terminal behaves like a non-terminal even with tty stdout.
	ran = 0
	out.Reset()
	stdoutIsTTY = func() bool { return true }
	stdinIsTTY = func() bool { return false }
	if err := runRoot(rootCmd, nil); err != nil {
		t.Fatalf("runRoot (stat error) = %v, want nil", err)
	}
	if ran != 0 || out.Len() == 0 {
		t.Fatalf("stat-error path: ran=%d helpLen=%d, want runner skipped and help printed", ran, out.Len())
	}

	// Positional args never reach the menu.
	ran = 0
	stdoutIsTTY = func() bool { return true }
	stdinIsTTY = func() bool { return true }
	if err := runRoot(rootCmd, []string{"list"}); err != nil {
		t.Fatalf("runRoot (with args) = %v, want nil", err)
	}
	if ran != 0 {
		t.Fatal("menu runner must not run when positional args are present")
	}
}

func TestMenuSubcommandRegistered(t *testing.T) {
	var menu *cobra.Command
	for _, sub := range rootCmd.Commands() {
		if sub.Name() == "menu" {
			menu = sub
			break
		}
	}
	if menu == nil {
		t.Fatal("`hukou menu` subcommand is not registered on the root command")
	}
	if menu.RunE == nil {
		t.Fatal("menu subcommand has no RunE")
	}
	if menu.Use != "menu" {
		t.Fatalf("menu Use = %q, want %q", menu.Use, "menu")
	}
}

func TestMenuCommandRequiresTTY(t *testing.T) {
	menuCmd.SetOut(&bytes.Buffer{})
	defer menuCmd.SetOut(nil)

	if err := menuCmd.RunE(menuCmd, nil); err == nil {
		t.Fatal("menu RunE must refuse a redirected (non-char-device) stdout")
	}
}

func TestMenuActionMapping(t *testing.T) {
	if menuAction("1") == nil || menuAction("2") == nil {
		t.Fatal("keys 1 and 2 must map to actions")
	}
	if menuAction("zz-unknown") != nil {
		t.Fatal("unknown keys must map to nil (no-op round)")
	}
}
