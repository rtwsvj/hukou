package provenance

import (
	"os"
	"path/filepath"
	"testing"
)

// Tests for gobin.go helpers without modifying gobin.go itself.

func TestReadGoBinary_self(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// Resolve in case Executable returns a relative or symlink path.
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	info, ok := ReadGoBinary(exe)
	if !ok {
		t.Fatalf("ReadGoBinary(%q) ok=false; test binary should be a go-install module binary", exe)
	}
	const want = "github.com/rtwsvj/hukou"
	if info.ModulePath != want {
		t.Fatalf("ModulePath=%q want %q (ImportPath=%q Version=%q)",
			info.ModulePath, want, info.ImportPath, info.Version)
	}
}

func TestGoBinDir_priority(t *testing.T) {
	cases := []struct {
		name                string
		gobin, gopath, home string
		want                string
	}{
		{"GOBIN wins", "/custom/gobin", "/gopath", "/home", "/custom/gobin"},
		{"GOPATH/bin when no GOBIN", "", "/gopath", "/home", filepath.Join("/gopath", "bin")},
		{"HOME/go/bin default", "", "", "/home", filepath.Join("/home", "go", "bin")},
		{"all empty", "", "", "", ""},
		{"GOBIN over GOPATH and HOME", "/binA", "/gopath", "/home", "/binA"},
		{"GOPATH over HOME", "", "/gp", "/home", filepath.Join("/gp", "bin")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := GoBinDir(tc.gobin, tc.gopath, tc.home)
			if got != tc.want {
				t.Fatalf("GoBinDir(%q,%q,%q)=%q want %q",
					tc.gobin, tc.gopath, tc.home, got, tc.want)
			}
		})
	}
}
