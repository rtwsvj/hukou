package provenance

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/rtwsvj/hukou/internal/scan"
)

func TestCurlInstaller_grokHermesCodex(t *testing.T) {
	root := t.TempDir()
	grokDir := filepath.Join(root, ".grok", "downloads")
	hermesDir := filepath.Join(root, ".hermes")
	codexPkg := filepath.Join(root, ".codex", "packages")
	codexBin := filepath.Join(codexPkg, "standalone", "releases", "0.142.3-aarch64-apple-darwin", "bin", "codex")

	env := Env{CurlInstallerRoots: []CurlInstallerRoot{
		{Dir: grokDir, Package: "grok"},
		{Dir: codexPkg, Package: "codex"},
		{Dir: hermesDir, Package: "hermes"},
	}}
	d := NewCurlInstallerDetector()
	if err := d.Load(env); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name    string
		bin     scan.Binary
		pkg     string
		version string
	}{
		{
			name: "grok",
			bin: scan.Binary{
				Name: "grok-macos-aarch64",
				Path: filepath.Join(grokDir, "grok-macos-aarch64"),
				RealPath: filepath.Join(grokDir, "grok-macos-aarch64"),
			},
			pkg: "grok",
		},
		{
			name: "hermes",
			bin: scan.Binary{
				Name: "uv",
				Path: filepath.Join(hermesDir, "bin", "uv"),
				RealPath: filepath.Join(hermesDir, "bin", "uv"),
			},
			pkg: "hermes",
		},
		{
			name: "codex-version",
			bin: scan.Binary{
				Name: "codex",
				Path: filepath.Join(root, ".codex", "bin", "codex"),
				RealPath: codexBin,
			},
			pkg:     "codex",
			version: "0.142.3",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := d.Match(tc.bin)
			if a == nil {
				t.Fatal("expected match")
			}
			if a.Source != "curl-installer" || a.Package != tc.pkg || a.Version != tc.version {
				t.Fatalf("got %+v, want package=%s version=%s", a, tc.pkg, tc.version)
			}
			if a.Confidence == "" || a.Evidence == "" {
				t.Fatalf("missing confidence/evidence: %+v", a)
			}
		})
	}
}

func TestCodexVersionFromReleaseDir(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"0.142.3-aarch64-apple-darwin", "0.142.3"},
		{"0.140.0-x86_64-unknown-linux-gnu", "0.140.0"},
		{"1.0.0", "1.0.0"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := codexVersionFromReleaseDir(tc.in); got != tc.want {
			t.Errorf("codexVersionFromReleaseDir(%q)=%q want %q", tc.in, got, tc.want)
		}
	}
}

func TestUV_cpythonPython(t *testing.T) {
	root := t.TempDir()
	localShare := filepath.Join(root, ".local", "share")
	pyDir := filepath.Join(localShare, "uv", "python", "cpython-3.11.14-macos-aarch64-none")
	pyBin := filepath.Join(pyDir, "bin", "python3.11")

	d := NewUVDetector()
	if err := d.Load(Env{LocalShare: localShare}); err != nil {
		t.Fatal(err)
	}
	a := d.Match(scan.Binary{
		Name: "python3.11",
		Path: filepath.Join(root, ".local", "bin", "python3.11"),
		RealPath: pyBin,
	})
	if a == nil {
		t.Fatal("expected match")
	}
	if a.Source != "uv" || a.Package != "cpython" || a.Version != "3.11.14" || a.Confidence != "exact" {
		t.Fatalf("got %+v", a)
	}
	if !strings.Contains(a.Evidence, "cpython-3.11.14") {
		t.Fatalf("evidence should mention cpython dir: %s", a.Evidence)
	}
}

func TestCpythonDirPackageVersion(t *testing.T) {
	pkg, ver := cpythonDirPackageVersion("cpython-3.12.13-macos-aarch64-none")
	if pkg != "cpython" || ver != "3.12.13" {
		t.Fatalf("got %s %s", pkg, ver)
	}
	pkg, ver = cpythonDirPackageVersion("not-cpython")
	if pkg != "" || ver != "" {
		t.Fatalf("want empty, got %s %s", pkg, ver)
	}
}

func TestMacOSAppDetector(t *testing.T) {
	root := t.TempDir()
	apps := filepath.Join(root, "Applications")
	userApps := filepath.Join(root, "UserApps")
	cursorBin := filepath.Join(apps, "Cursor.app", "Contents", "MacOS", "Cursor")
	userBin := filepath.Join(userApps, "MyTool.app", "Contents", "MacOS", "MyTool")

	d := NewMacOSAppDetector()
	if err := d.Load(Env{ApplicationsDirs: []string{apps, userApps}}); err != nil {
		t.Fatal(err)
	}

	a := d.Match(scan.Binary{Name: "Cursor", Path: cursorBin, RealPath: cursorBin})
	if a == nil || a.Source != "macos-app" || a.Package != "Cursor" || a.Confidence != "exact" {
		t.Fatalf("cursor: %+v", a)
	}
	if !strings.Contains(a.Evidence, "Cursor.app") {
		t.Fatalf("evidence should include .app path: %s", a.Evidence)
	}

	a = d.Match(scan.Binary{Name: "MyTool", Path: userBin, RealPath: userBin})
	if a == nil || a.Source != "macos-app" || a.Package != "MyTool" {
		t.Fatalf("user app: %+v", a)
	}

	// Loose binary under Applications (not inside .app) → nil
	loose := filepath.Join(apps, "loose-bin")
	if a = d.Match(scan.Binary{Name: "loose-bin", Path: loose, RealPath: loose}); a != nil {
		t.Fatalf("loose binary must not match: %+v", a)
	}
}

func TestLocalProjectDetector(t *testing.T) {
	root := t.TempDir()
	projects := filepath.Join(root, "Projects")
	bin := filepath.Join(projects, "open-design-tools", "bin", "odt")

	d := NewLocalProjectDetector()
	if err := d.Load(Env{ProjectsDir: projects}); err != nil {
		t.Fatal(err)
	}
	a := d.Match(scan.Binary{Name: "odt", Path: bin, RealPath: bin})
	if a == nil || a.Source != "local-project" || a.Package != "open-design-tools" || a.Confidence != "inferred" {
		t.Fatalf("got %+v", a)
	}
	if !strings.Contains(a.Evidence, "open-design-tools") {
		t.Fatalf("evidence: %s", a.Evidence)
	}

	// Outside projects → nil
	outside := filepath.Join(root, "elsewhere", "bin", "x")
	if a = d.Match(scan.Binary{Name: "x", Path: outside, RealPath: outside}); a != nil {
		t.Fatalf("outside projects must be nil: %+v", a)
	}
}

func TestBrewShareInferred(t *testing.T) {
	root := t.TempDir()
	prefix := filepath.Join(root, "homebrew")
	adb := filepath.Join(prefix, "share", "android-commandlinetools", "platform-tools", "adb")

	d := NewBrewDetector()
	if err := d.Load(Env{BrewPrefixes: []string{prefix}}); err != nil {
		t.Fatal(err)
	}
	a := d.Match(scan.Binary{Name: "adb", Path: adb, RealPath: adb})
	if a == nil || a.Source != "brew" || a.Package != "android-commandlinetools" {
		t.Fatalf("got %+v", a)
	}
	if a.Confidence != "inferred" {
		t.Fatalf("share tree must be inferred, got %s", a.Confidence)
	}
	if !strings.Contains(a.Evidence, "share") {
		t.Fatalf("evidence should note share tree: %s", a.Evidence)
	}

	// Cellar still exact and preferred over share
	cellarBin := filepath.Join(prefix, "Cellar", "fzf", "0.46.0", "bin", "fzf")
	a = d.Match(scan.Binary{Name: "fzf", Path: filepath.Join(prefix, "bin", "fzf"), RealPath: cellarBin})
	if a == nil || a.Package != "fzf" || a.Version != "0.46.0" || a.Confidence != "exact" {
		t.Fatalf("cellar: %+v", a)
	}
}

func TestRunner_macosAppAndLocalProjectOrder(t *testing.T) {
	r := DefaultRunner()
	names := make([]string, 0, len(r.Detectors()))
	for _, d := range r.Detectors() {
		names = append(names, d.Name())
	}
	curlIdx, macIdx, locIdx, goIdx := -1, -1, -1, -1
	for i, n := range names {
		switch n {
		case "curl-installer":
			curlIdx = i
		case "macos-app":
			macIdx = i
		case "local-project":
			locIdx = i
		case "go":
			goIdx = i
		}
	}
	if curlIdx < 0 || macIdx < 0 || locIdx < 0 || goIdx < 0 {
		t.Fatalf("missing detectors in chain: %v", names)
	}
	// macos-app and local-project after curl-installer, before go (gobin)
	if !(curlIdx < macIdx && macIdx < locIdx && locIdx < goIdx) {
		t.Fatalf("want curl-installer < macos-app < local-project < go, got %v", names)
	}
}
