package provenance

import (
	"strings"
	"testing"

	"github.com/rtwsvj/hukou/internal/scan"
)

func TestSystemDetector(t *testing.T) {
	d := NewSystemDetector()
	env := Env{XcodeCLT: "/Library/Developer/CommandLineTools"}
	if err := d.Load(env); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		path string
		hit  bool
	}{
		{"/bin/ls", true},
		{"/usr/bin/env", true},
		{"/usr/sbin/cron", true},
		{"/usr/libexec/something", true},
		{"/System/Library/CoreServices/foo", true},
		{"/System/Volumes/Data/usr/local/bin/foo", false}, // /System/Volumes = user data
		{"/Library/Apple/usr/bin/python3", true},          // Fix #11
		{"/Library/Developer/CommandLineTools/usr/bin/clang", true},
		{"/opt/homebrew/bin/fzf", false},
		{"/usr/local/bin/custom", false},
		{"/usr/binary/evil", false}, // boundary: must not match /usr/bin prefix wrongly
	}
	for _, tc := range cases {
		a := d.Match(scan.Binary{Name: "x", Path: tc.path, RealPath: tc.path})
		if tc.hit && a == nil {
			t.Errorf("%s: expected system hit", tc.path)
		}
		if !tc.hit && a != nil {
			t.Errorf("%s: unexpected hit %+v", tc.path, a)
		}
		if a != nil && a.Source != "system" {
			t.Errorf("%s: source=%s", tc.path, a.Source)
		}
	}
}

func TestUnknownDetector(t *testing.T) {
	d := NewUnknownDetector()
	_ = d.Load(Env{})
	a := d.Match(scan.Binary{Name: "x", Path: "/tmp/x", RealPath: "/tmp/x"})
	if a == nil || a.Source != "unknown" {
		t.Fatalf("got %+v", a)
	}
}

func TestRunner_chain(t *testing.T) {
	r := DefaultRunner()
	_, _ = r.Load(DefaultEnv())
	// system first
	a := r.Match(scan.Binary{Name: "ls", Path: "/bin/ls", RealPath: "/bin/ls"})
	if a == nil || a.Source != "system" {
		t.Fatalf("system: %+v", a)
	}
	// falls through to unknown
	a = r.Match(scan.Binary{Name: "weird", Path: "/opt/weird", RealPath: "/opt/weird"})
	if a == nil || a.Source != "unknown" {
		t.Fatalf("unknown: %+v", a)
	}
	// chain ends with unknown — never nil from DefaultRunner
	names := make([]string, 0, len(r.Detectors()))
	for _, d := range r.Detectors() {
		names = append(names, d.Name())
	}
	if names[len(names)-1] != "unknown" {
		t.Fatalf("last detector should be unknown: %v", names)
	}
	// Fix #9: chain is fully wired (not skeleton); go before system, unknown last.
	if len(names) < 10 {
		t.Fatalf("expected full detector chain, got %v", names)
	}
	// go detector present before system
	goIdx, sysIdx := -1, -1
	miseIdx, asdfIdx, cargoIdx := -1, -1, -1
	for i, n := range names {
		switch n {
		case "go":
			goIdx = i
		case "system":
			sysIdx = i
		case "mise":
			miseIdx = i
		case "asdf":
			asdfIdx = i
		case "cargo":
			cargoIdx = i
		}
	}
	if goIdx < 0 || sysIdx < 0 || goIdx >= sysIdx {
		t.Fatalf("go should precede system in chain: %v", names)
	}
	// Round2 H: mise/asdf before language PMs (cargo)
	if miseIdx < 0 || asdfIdx < 0 || cargoIdx < 0 {
		t.Fatalf("missing mise/asdf/cargo in chain: %v", names)
	}
	if !(miseIdx < cargoIdx && asdfIdx < cargoIdx) {
		t.Fatalf("mise/asdf should precede cargo: %v", names)
	}
}

// failingDetector always errors on Load — used to test Runner resilience.
type failingDetector struct{ name string }

func (f *failingDetector) Name() string       { return f.name }
func (f *failingDetector) Load(env Env) error { return errLoadFailed }
func (f *failingDetector) Match(b scan.Binary) *Attribution {
	return &Attribution{Source: f.name, Package: "should-not-run"}
}

var errLoadFailed = errString("load boom")

type errString string

func (e errString) Error() string { return string(e) }

func TestRunner_LoadSkipsFailedDetector(t *testing.T) {
	r := NewRunner(
		&failingDetector{name: "bad"},
		NewUnknownDetector(),
	)
	warns, _ := r.Load(Env{})
	if len(warns) != 1 || !strings.Contains(warns[0], "bad") || !strings.Contains(warns[0], "load boom") {
		t.Fatalf("warnings=%v", warns)
	}
	// failed detector removed from chain
	for _, d := range r.Detectors() {
		if d.Name() == "bad" {
			t.Fatal("failed detector should be skipped/removed")
		}
	}
	a := r.Match(scan.Binary{Name: "x", Path: "/tmp/x", RealPath: "/tmp/x"})
	if a == nil || a.Source != "unknown" {
		t.Fatalf("expected unknown after skip: %+v", a)
	}
}

// Fix #10: well-known brew paths with default prefixes.
func TestBrewDetector_selfAndCask(t *testing.T) {
	d := NewBrewDetector()
	if err := d.Load(Env{BrewPrefixes: []string{"/opt/homebrew", "/usr/local"}}); err != nil {
		t.Fatal(err)
	}
	// /opt/homebrew/bin/brew
	a := d.Match(scan.Binary{
		Name: "brew", Path: "/opt/homebrew/bin/brew", RealPath: "/opt/homebrew/bin/brew",
	})
	if a == nil || a.Source != "brew" || a.Package != "homebrew" || a.Confidence != "exact" {
		t.Fatalf("opt brew self: %+v", a)
	}
	// /usr/local/bin/brew
	a = d.Match(scan.Binary{
		Name: "brew", Path: "/usr/local/bin/brew", RealPath: "/usr/local/bin/brew",
	})
	if a == nil || a.Source != "brew" || a.Package != "homebrew" {
		t.Fatalf("usr/local brew self: %+v", a)
	}
	// Caskroom
	a = d.Match(scan.Binary{
		Name: "app", Path: "/opt/homebrew/bin/app",
		RealPath: "/opt/homebrew/Caskroom/myapp/1.0.0/app",
	})
	if a == nil || a.Source != "brew" || a.Package != "myapp" || a.Confidence != "exact" {
		t.Fatalf("cask: %+v", a)
	}
}
