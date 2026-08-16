package provenance

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Env injects HOME, PATH, and known package-manager roots into detectors.
// Detectors must not call os.Getenv or hard-code $HOME; all paths come from Env.
type Env struct {
	Home string
	Path string // raw PATH string

	// Go toolchain placement (for go detector / gobin.go).
	GoBin  string // $GOBIN
	GoPath string // $GOPATH

	// Homebrew prefixes to check (default /opt/homebrew and /usr/local on macOS).
	BrewPrefixes []string

	// Optional roots for package managers (filled by DefaultEnv / tests).
	CargoHome          string // ~/.cargo
	RustupHome         string // ~/.rustup
	LocalShare         string // ~/.local/share
	LocalBin           string // ~/.local/bin
	NpmPrefix          string
	NpmPrefixes        []string
	PnpmHome           string
	YarnBin            string
	BunHome            string
	VoltaHome          string
	DenoHome           string
	DotnetTools        string
	ComposerBin        string
	ComposerBins       []string
	KrewRoot           string
	MiseData           string
	AsdfDir            string
	NixStore           string // /nix/store
	MacPorts           string // /opt/local
	XcodeCLT           string // /Library/Developer/CommandLineTools
	PipxVenvs          []string
	UVTools            string
	PipUserBase        string
	GemRoots           []string
	GemBinDirs         []string
	CurlInstallerRoots []CurlInstallerRoot

	// macOS .app bundles and local project trees.
	ApplicationsDirs []string // /Applications, ~/Applications
	ProjectsDir      string   // ~/Projects

	HukouManifest string // Path to hukou's own manifest.json.
}

type CurlInstallerRoot struct {
	Dir     string
	Package string
}

// DefaultEnv builds Env from the process environment and fixed candidate paths.
// This is the only place os.Getenv is allowed for provenance configuration.
func DefaultEnv() Env {
	home, _ := os.UserHomeDir()
	if home == "" {
		home = os.Getenv("HOME")
	}
	xdgData := os.Getenv("XDG_DATA_HOME")
	if xdgData == "" && home != "" {
		xdgData = filepath.Join(home, ".local", "share")
	}
	xdgConfig := os.Getenv("XDG_CONFIG_HOME")
	if xdgConfig == "" && home != "" {
		xdgConfig = filepath.Join(home, ".config")
	}
	e := Env{
		Home:        home,
		Path:        os.Getenv("PATH"),
		GoBin:       os.Getenv("GOBIN"),
		GoPath:      os.Getenv("GOPATH"),
		NixStore:    "/nix/store",
		MacPorts:    "/opt/local",
		XcodeCLT:    "/Library/Developer/CommandLineTools",
		CargoHome:   filepath.Join(home, ".cargo"),
		RustupHome:  filepath.Join(home, ".rustup"),
		LocalShare:  xdgData,
		LocalBin:    filepath.Join(home, ".local", "bin"),
		VoltaHome:   filepath.Join(home, ".volta"),
		DenoHome:    filepath.Join(home, ".deno"),
		DotnetTools: filepath.Join(home, ".dotnet", "tools"),
		KrewRoot:    filepath.Join(home, ".krew"),
		MiseData:    filepath.Join(xdgData, "mise"),
		AsdfDir:     filepath.Join(home, ".asdf"),
		UVTools:     filepath.Join(xdgData, "uv", "tools"),
		PipUserBase: filepath.Join(home, "Library", "Python"),
	}
	if v := os.Getenv("HUKOU_DATA_DIR"); v != "" {
		e.HukouManifest = filepath.Join(v, "manifest.json")
	} else {
		e.HukouManifest = filepath.Join(xdgData, "hukou", "manifest.json")
	}
	if runtime.GOOS == "darwin" {
		e.BrewPrefixes = []string{"/opt/homebrew", "/usr/local"}
		e.PnpmHome = filepath.Join(home, "Library", "pnpm")
	} else {
		e.BrewPrefixes = []string{"/home/linuxbrew/.linuxbrew", "/usr/local"}
		e.PnpmHome = filepath.Join(xdgData, "pnpm")
	}
	e.YarnBin = filepath.Join(home, ".yarn", "bin")
	e.BunHome = filepath.Join(home, ".bun")
	if p := firstEnv("npm_config_prefix", "NPM_CONFIG_PREFIX"); p != "" {
		e.NpmPrefix = p
	}
	e.NpmPrefixes = uniquePaths(append([]string{e.NpmPrefix}, e.BrewPrefixes...))
	composerBins := []string{
		filepath.Join(xdgConfig, "composer", "vendor", "bin"),
		filepath.Join(home, ".composer", "vendor", "bin"),
	}
	e.ComposerBins = uniquePaths(composerBins)
	if len(e.ComposerBins) > 0 {
		e.ComposerBin = e.ComposerBins[0]
	}
	pipxVenvs := []string{
		filepath.Join(home, ".local", "pipx", "venvs"),
		filepath.Join(xdgData, "pipx", "venvs"),
	}
	if p := os.Getenv("PIPX_HOME"); p != "" {
		pipxVenvs = append([]string{filepath.Join(p, "venvs")}, pipxVenvs...)
	}
	e.PipxVenvs = uniquePaths(pipxVenvs)
	gemRoots := []string{
		filepath.Join(home, ".gem", "ruby"),
		"/Library/Ruby/Gems",
	}
	for _, prefix := range e.BrewPrefixes {
		gemRoots = append(gemRoots, filepath.Join(prefix, "lib", "ruby", "gems"))
	}
	e.GemRoots = uniquePaths(gemRoots)
	if gemHome := os.Getenv("GEM_HOME"); gemHome != "" {
		e.GemBinDirs = append(e.GemBinDirs, filepath.Join(gemHome, "bin"))
	}
	if gemPath := os.Getenv("GEM_PATH"); gemPath != "" {
		for _, p := range strings.Split(gemPath, string(os.PathListSeparator)) {
			if p != "" {
				e.GemBinDirs = append(e.GemBinDirs, filepath.Join(p, "bin"))
			}
		}
	}
	e.GemBinDirs = uniquePaths(e.GemBinDirs)
	e.CurlInstallerRoots = []CurlInstallerRoot{
		{Dir: filepath.Join(home, ".opencode", "bin"), Package: "opencode"},
		{Dir: filepath.Join(home, ".kimi-code", "bin"), Package: "kimi"},
		{Dir: filepath.Join(home, ".claude", "local"), Package: "claude"},
		{Dir: filepath.Join(home, ".codeium"), Package: "codeium"},
		{Dir: filepath.Join(home, ".foundry", "bin"), Package: "foundry"},
		{Dir: filepath.Join(home, ".bun", "install"), Package: "bun"},
		{Dir: filepath.Join(home, ".grok", "downloads"), Package: "grok"},
		{Dir: filepath.Join(home, ".codex", "packages"), Package: "codex"},
		{Dir: filepath.Join(home, ".hermes"), Package: "hermes"},
	}
	e.ApplicationsDirs = []string{
		"/Applications",
		filepath.Join(home, "Applications"),
	}
	e.ProjectsDir = filepath.Join(home, "Projects")
	return e
}

// PathDirs returns PATH split into directories.
func (e Env) PathDirs() []string {
	if e.Path == "" {
		return nil
	}
	parts := strings.Split(e.Path, string(os.PathListSeparator))
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func firstEnv(names ...string) string {
	for _, name := range names {
		if v := os.Getenv(name); v != "" {
			return v
		}
	}
	return ""
}

func uniquePaths(paths []string) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if p == "" {
			continue
		}
		p = filepath.Clean(p)
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	return out
}
