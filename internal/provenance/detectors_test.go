package provenance

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rtwsvj/hukou/internal/scan"
)

func TestTier1Detectors(t *testing.T) {
	root := t.TempDir()
	cargoHome := filepath.Join(root, ".cargo")
	if err := os.MkdirAll(cargoHome, 0o755); err != nil {
		t.Fatal(err)
	}
	crates := `{"installs":{"ripgrep 14.1.1 (registry+https://github.com/rust-lang/crates.io-index)":{"bins":["rg"]}}}`
	if err := os.WriteFile(filepath.Join(cargoHome, ".crates2.json"), []byte(crates), 0o644); err != nil {
		t.Fatal(err)
	}

	brewPrefix := filepath.Join(root, "homebrew")
	macports := filepath.Join(root, "opt", "local")
	rustupHome := filepath.Join(root, ".rustup")
	goPath := filepath.Join(root, "gopath")
	npmPrefix := filepath.Join(root, "npm")
	pnpmHome := filepath.Join(root, "Library", "pnpm")
	yarnBin := filepath.Join(root, ".yarn", "bin")
	bunHome := filepath.Join(root, ".bun")
	pipxVenvs := filepath.Join(root, ".local", "pipx", "venvs")
	localBin := filepath.Join(root, ".local", "bin")
	uvTools := filepath.Join(root, ".local", "share", "uv", "tools")
	pipUserBase := filepath.Join(root, "Library", "Python")
	miseData := filepath.Join(root, ".local", "share", "mise")
	asdfDir := filepath.Join(root, ".asdf")
	gemRoot := filepath.Join(root, ".gem", "ruby")
	nixStore := filepath.Join(root, "nix", "store")
	voltaHome := filepath.Join(root, ".volta")
	denoHome := filepath.Join(root, ".deno")
	dotnetTools := filepath.Join(root, ".dotnet", "tools")
	composerBin := filepath.Join(root, ".composer", "vendor", "bin")
	krewRoot := filepath.Join(root, ".krew")
	curlRoot := filepath.Join(root, ".opencode", "bin")

	cases := []struct {
		name    string
		d       Detector
		env     Env
		bin     scan.Binary
		source  string
		pkg     string
		version string
	}{
		{
			name: "brew", d: NewBrewDetector(), env: Env{BrewPrefixes: []string{brewPrefix}},
			bin:    scan.Binary{Name: "fzf", Path: filepath.Join(brewPrefix, "bin", "fzf"), RealPath: filepath.Join(brewPrefix, "Cellar", "fzf", "0.46.0", "bin", "fzf")},
			source: "brew", pkg: "fzf", version: "0.46.0",
		},
		// Fix #10: brew binary itself → Package=homebrew
		{
			name: "brew-self", d: NewBrewDetector(), env: Env{BrewPrefixes: []string{brewPrefix}},
			bin:    scan.Binary{Name: "brew", Path: filepath.Join(brewPrefix, "bin", "brew"), RealPath: filepath.Join(brewPrefix, "bin", "brew")},
			source: "brew", pkg: "homebrew",
		},
		// Fix #10: Caskroom → Package=<cask>
		{
			name: "brew-cask", d: NewBrewDetector(), env: Env{BrewPrefixes: []string{brewPrefix}},
			bin:    scan.Binary{Name: "docker", Path: filepath.Join(brewPrefix, "bin", "docker"), RealPath: filepath.Join(brewPrefix, "Caskroom", "docker", "4.25.0", "docker")},
			source: "brew", pkg: "docker",
		},
		{
			name: "macports", d: NewMacPortsDetector(), env: Env{MacPorts: macports},
			bin:    scan.Binary{Name: "wget", Path: filepath.Join(macports, "bin", "wget"), RealPath: filepath.Join(macports, "bin", "wget")},
			source: "macports", pkg: "wget",
		},
		{
			name: "cargo", d: NewCargoDetector(), env: Env{CargoHome: cargoHome},
			bin:    scan.Binary{Name: "rg", Path: filepath.Join(cargoHome, "bin", "rg"), RealPath: filepath.Join(cargoHome, "bin", "rg")},
			source: "cargo", pkg: "ripgrep", version: "14.1.1",
		},
		{
			name: "rustup", d: NewRustupDetector(), env: Env{RustupHome: rustupHome, CargoHome: cargoHome},
			bin:    scan.Binary{Name: "rustc", Path: filepath.Join(cargoHome, "bin", "rustc"), RealPath: filepath.Join(rustupHome, "toolchains", "stable-aarch64-apple-darwin", "bin", "rustc")},
			source: "rustup", pkg: "rustc", version: "stable-aarch64-apple-darwin",
		},
		{
			name: "go", d: NewGoDetector(), env: Env{Home: root, GoPath: goPath},
			bin:    scan.Binary{Name: "gopls", Path: filepath.Join(goPath, "bin", "gopls"), RealPath: filepath.Join(goPath, "bin", "gopls")},
			source: "go", pkg: "gopls",
		},
		{
			name: "npm", d: NewNpmDetector(), env: Env{NpmPrefixes: []string{npmPrefix}},
			bin:    scan.Binary{Name: "eslint", Path: filepath.Join(npmPrefix, "bin", "eslint"), RealPath: filepath.Join(npmPrefix, "lib", "node_modules", "eslint", "bin", "eslint.js")},
			source: "npm", pkg: "eslint",
		},
		{
			name: "pnpm", d: NewPnpmDetector(), env: Env{PnpmHome: pnpmHome},
			bin:    scan.Binary{Name: "prettier", Path: filepath.Join(pnpmHome, "prettier"), RealPath: filepath.Join(pnpmHome, "global", "5", "node_modules", ".pnpm", "prettier@3.0.0", "node_modules", "prettier", "bin", "prettier.cjs")},
			source: "pnpm", pkg: "prettier", version: "3.0.0",
		},
		{
			name: "yarn", d: NewYarnDetector(), env: Env{YarnBin: yarnBin},
			bin:    scan.Binary{Name: "foo", Path: filepath.Join(yarnBin, "foo"), RealPath: filepath.Join(yarnBin, "foo")},
			source: "yarn", pkg: "foo",
		},
		{
			name: "bun", d: NewBunDetector(), env: Env{BunHome: bunHome},
			bin:    scan.Binary{Name: "bunx", Path: filepath.Join(bunHome, "bin", "bunx"), RealPath: filepath.Join(bunHome, "bin", "bunx")},
			source: "bun", pkg: "bunx",
		},
		{
			name: "pipx", d: NewPipxDetector(), env: Env{PipxVenvs: []string{pipxVenvs}, LocalBin: localBin},
			bin:    scan.Binary{Name: "black", Path: filepath.Join(localBin, "black"), RealPath: filepath.Join(pipxVenvs, "black", "bin", "black")},
			source: "pipx", pkg: "black",
		},
		{
			name: "uv", d: NewUVDetector(), env: Env{UVTools: uvTools, LocalBin: localBin},
			bin:    scan.Binary{Name: "ruff", Path: filepath.Join(localBin, "ruff"), RealPath: filepath.Join(uvTools, "ruff", "bin", "ruff")},
			source: "uv", pkg: "ruff",
		},
		{
			name: "pip-user", d: NewPipUserDetector(), env: Env{PipUserBase: pipUserBase},
			bin:    scan.Binary{Name: "flake8", Path: filepath.Join(pipUserBase, "3.12", "bin", "flake8"), RealPath: filepath.Join(pipUserBase, "3.12", "bin", "flake8")},
			source: "pip-user", pkg: "flake8", version: "3.12",
		},
		{
			name: "mise", d: NewMiseDetector(), env: Env{MiseData: miseData},
			bin:    scan.Binary{Name: "node", Path: filepath.Join(miseData, "shims", "node"), RealPath: filepath.Join(miseData, "installs", "node", "22.1.0", "bin", "node")},
			source: "mise", pkg: "node", version: "22.1.0",
		},
		{
			name: "asdf", d: NewAsdfDetector(), env: Env{AsdfDir: asdfDir},
			bin:    scan.Binary{Name: "python", Path: filepath.Join(asdfDir, "shims", "python"), RealPath: filepath.Join(asdfDir, "installs", "python", "3.12.4", "bin", "python")},
			source: "asdf", pkg: "python", version: "3.12.4",
		},
		{
			name: "gem", d: NewGemDetector(), env: Env{GemRoots: []string{gemRoot}},
			bin:    scan.Binary{Name: "rubocop", Path: filepath.Join(gemRoot, "3.3.0", "bin", "rubocop"), RealPath: filepath.Join(gemRoot, "3.3.0", "gems", "rubocop-1.64.0", "exe", "rubocop")},
			source: "gem", pkg: "rubocop", version: "1.64.0",
		},
		{
			name: "nix", d: NewNixDetector(), env: Env{NixStore: nixStore},
			bin:    scan.Binary{Name: "rg", Path: filepath.Join(nixStore, "abcd1234-ripgrep-14.1.1", "bin", "rg"), RealPath: filepath.Join(nixStore, "abcd1234-ripgrep-14.1.1", "bin", "rg")},
			source: "nix", pkg: "ripgrep", version: "14.1.1",
		},
		{
			name: "volta", d: NewVoltaDetector(), env: Env{VoltaHome: voltaHome},
			bin:    scan.Binary{Name: "node", Path: filepath.Join(voltaHome, "bin", "node"), RealPath: filepath.Join(voltaHome, "bin", "node")},
			source: "volta", pkg: "node",
		},
		{
			name: "deno", d: NewDenoDetector(), env: Env{DenoHome: denoHome},
			bin:    scan.Binary{Name: "deno", Path: filepath.Join(denoHome, "bin", "deno"), RealPath: filepath.Join(denoHome, "bin", "deno")},
			source: "deno", pkg: "deno",
		},
		{
			name: "dotnet", d: NewDotnetDetector(), env: Env{DotnetTools: dotnetTools},
			bin:    scan.Binary{Name: "dotnet-ef", Path: filepath.Join(dotnetTools, "dotnet-ef"), RealPath: filepath.Join(dotnetTools, "dotnet-ef")},
			source: "dotnet", pkg: "dotnet-ef",
		},
		{
			name: "composer", d: NewComposerDetector(), env: Env{ComposerBins: []string{composerBin}},
			bin:    scan.Binary{Name: "phpunit", Path: filepath.Join(composerBin, "phpunit"), RealPath: filepath.Join(root, ".composer", "vendor", "phpunit", "phpunit", "phpunit")},
			source: "composer", pkg: "phpunit/phpunit",
		},
		{
			name: "krew", d: NewKrewDetector(), env: Env{KrewRoot: krewRoot},
			bin:    scan.Binary{Name: "kubectl-ctx", Path: filepath.Join(krewRoot, "bin", "kubectl-ctx"), RealPath: filepath.Join(krewRoot, "bin", "kubectl-ctx")},
			source: "krew", pkg: "ctx",
		},
		{
			name: "curl-installer", d: NewCurlInstallerDetector(), env: Env{CurlInstallerRoots: []CurlInstallerRoot{{Dir: curlRoot, Package: "opencode"}}},
			bin:    scan.Binary{Name: "opencode", Path: filepath.Join(curlRoot, "opencode"), RealPath: filepath.Join(curlRoot, "opencode")},
			source: "curl-installer", pkg: "opencode",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.d.Load(tc.env); err != nil {
				t.Fatal(err)
			}
			a := tc.d.Match(tc.bin)
			if a == nil {
				t.Fatal("expected match")
			}
			if a.Source != tc.source || a.Package != tc.pkg || a.Version != tc.version {
				t.Fatalf("got %+v, want source=%s package=%s version=%s", a, tc.source, tc.pkg, tc.version)
			}
			if a.Confidence == "" || a.Evidence == "" {
				t.Fatalf("missing confidence/evidence: %+v", a)
			}
		})
	}
}
