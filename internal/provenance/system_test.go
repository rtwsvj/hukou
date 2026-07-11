package provenance

import (
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
		{"/Library/Apple/usr/bin/python3", true}, // Fix #11
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
	if err := r.Load(DefaultEnv()); err != nil {
		t.Fatal(err)
	}
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
	for i, n := range names {
		if n == "go" {
			goIdx = i
		}
		if n == "system" {
			sysIdx = i
		}
	}
	if goIdx < 0 || sysIdx < 0 || goIdx >= sysIdx {
		t.Fatalf("go should precede system in chain: %v", names)
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
