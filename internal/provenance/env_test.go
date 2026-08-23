package provenance

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/rtwsvj/hukou/internal/scan"
)

// TestGoosEnv_matrix asserts the per-GOOS detector inputs of goosEnv on any
// host: darwin keeps its macOS-only roots and versioned pip-user layout,
// linux gets the flat ~/.local pip-user base, and windows leaves the
// macOS-only inputs unset so those detectors stay inert.
func TestGoosEnv_matrix(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("HUKOU_PROJECTS_DIR", "")
	t.Setenv("XDG_DATA_HOME", "")

	cases := []struct {
		name             string
		goos             string
		wantPipUserBase  string
		wantMacPorts     string
		wantXcodeCLT     string
		wantApplications []string
		wantBrewPrefixes []string
		wantPnpmHome     string
	}{
		{
			name:             "darwin",
			goos:             "darwin",
			wantPipUserBase:  filepath.Join(home, "Library", "Python"),
			wantMacPorts:     "/opt/local",
			wantXcodeCLT:     "/Library/Developer/CommandLineTools",
			wantApplications: []string{"/Applications", filepath.Join(home, "Applications")},
			wantBrewPrefixes: []string{"/opt/homebrew", "/usr/local"},
			wantPnpmHome:     filepath.Join(home, "Library", "pnpm"),
		},
		{
			name:             "linux",
			goos:             "linux",
			wantPipUserBase:  filepath.Join(home, ".local"),
			wantMacPorts:     "",
			wantXcodeCLT:     "",
			wantApplications: nil,
			wantBrewPrefixes: []string{"/home/linuxbrew/.linuxbrew", "/usr/local"},
			wantPnpmHome:     filepath.Join(home, ".local", "share", "pnpm"),
		},
		{
			name:             "windows",
			goos:             "windows",
			wantPipUserBase:  "", // detector intentionally inert
			wantMacPorts:     "",
			wantXcodeCLT:     "",
			wantApplications: nil,
			wantBrewPrefixes: []string{"/home/linuxbrew/.linuxbrew", "/usr/local"},
			wantPnpmHome:     filepath.Join(home, ".local", "share", "pnpm"),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := goosEnv(tc.goos)
			if env.Home != home {
				t.Errorf("Home = %q, want %q", env.Home, home)
			}
			if env.PipUserBase != tc.wantPipUserBase {
				t.Errorf("PipUserBase = %q, want %q", env.PipUserBase, tc.wantPipUserBase)
			}
			if env.MacPorts != tc.wantMacPorts {
				t.Errorf("MacPorts = %q, want %q", env.MacPorts, tc.wantMacPorts)
			}
			if env.XcodeCLT != tc.wantXcodeCLT {
				t.Errorf("XcodeCLT = %q, want %q", env.XcodeCLT, tc.wantXcodeCLT)
			}
			if len(env.ApplicationsDirs) != len(tc.wantApplications) {
				t.Fatalf("ApplicationsDirs = %v, want %v", env.ApplicationsDirs, tc.wantApplications)
			}
			for i := range tc.wantApplications {
				if env.ApplicationsDirs[i] != tc.wantApplications[i] {
					t.Fatalf("ApplicationsDirs = %v, want %v", env.ApplicationsDirs, tc.wantApplications)
				}
			}
			if len(env.BrewPrefixes) != len(tc.wantBrewPrefixes) {
				t.Fatalf("BrewPrefixes = %v, want %v", env.BrewPrefixes, tc.wantBrewPrefixes)
			}
			for i := range tc.wantBrewPrefixes {
				if env.BrewPrefixes[i] != tc.wantBrewPrefixes[i] {
					t.Fatalf("BrewPrefixes = %v, want %v", env.BrewPrefixes, tc.wantBrewPrefixes)
				}
			}
			if env.PnpmHome != tc.wantPnpmHome {
				t.Errorf("PnpmHome = %q, want %q", env.PnpmHome, tc.wantPnpmHome)
			}
			// GOOS-independent defaults must survive on every platform.
			if env.ProjectsDir != filepath.Join(home, "Projects") {
				t.Errorf("ProjectsDir = %q, want %q", env.ProjectsDir, filepath.Join(home, "Projects"))
			}
			if env.NixStore != "/nix/store" {
				t.Errorf("NixStore = %q", env.NixStore)
			}
			if len(env.CurlInstallerRoots) == 0 {
				t.Error("CurlInstallerRoots must stay registered on every GOOS")
			}
		})
	}
}

// TestGoosEnv_projectsDirOverride: HUKOU_PROJECTS_DIR replaces ~/Projects and
// is trusted as given — it is an operator override, so an absolute path
// outside HOME is honored verbatim rather than re-confined.
func TestGoosEnv_projectsDirOverride(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", "")
	outside := filepath.FromSlash("/srv/build-output")
	t.Setenv("HUKOU_PROJECTS_DIR", outside)
	for _, goos := range []string{"darwin", "linux"} {
		t.Run(goos, func(t *testing.T) {
			env := goosEnv(goos)
			if env.ProjectsDir != outside {
				t.Errorf("ProjectsDir = %q, want override %q", env.ProjectsDir, outside)
			}
			det := NewLocalProjectDetector()
			if err := det.Load(env); err != nil {
				t.Fatal(err)
			}
			bin := filepath.Join(outside, "mytool", "mytool")
			a := det.Match(scan.Binary{Name: "mytool", Path: bin, RealPath: bin})
			if a == nil || a.Source != "local-project" || a.Package != "mytool" {
				t.Fatalf("override dir not attributed: %+v", a)
			}
		})
	}
}

// TestGoosEnv_projectsDirEmptyFallback: an empty HUKOU_PROJECTS_DIR falls
// back to <home>/Projects on every supported GOOS.
func TestGoosEnv_projectsDirEmptyFallback(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("HUKOU_PROJECTS_DIR", "")
	for _, goos := range []string{"darwin", "linux", "windows"} {
		t.Run(goos, func(t *testing.T) {
			env := goosEnv(goos)
			if env.ProjectsDir != filepath.Join(home, "Projects") {
				t.Errorf("goos %s: ProjectsDir = %q, want %q", goos, env.ProjectsDir, filepath.Join(home, "Projects"))
			}
		})
	}
}

// runtimeGoos returns runtime.GOOS (indirection keeps the smoke test honest
// without importing runtime at every call site).
func runtimeGoos() string { return runtime.GOOS }

// TestDefaultEnv_smoke pins the runtime-GOOS wrapper to the matrix above:
// whatever this host is, DefaultEnv must agree with goosEnv(runtime.GOOS).
func TestDefaultEnv_smoke(t *testing.T) {
	t.Setenv("HUKOU_PROJECTS_DIR", "")
	t.Setenv("XDG_DATA_HOME", "")
	got := DefaultEnv()
	want := goosEnv(runtimeGoos())
	if got.PipUserBase != want.PipUserBase ||
		got.MacPorts != want.MacPorts ||
		got.XcodeCLT != want.XcodeCLT ||
		got.ProjectsDir != want.ProjectsDir ||
		got.PnpmHome != want.PnpmHome ||
		len(got.ApplicationsDirs) != len(want.ApplicationsDirs) {
		t.Fatalf("DefaultEnv %+v disagrees with goosEnv %+v", got, want)
	}
}
