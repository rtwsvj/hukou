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
	CargoHome   string // ~/.cargo
	RustupHome  string // ~/.rustup
	LocalShare  string // ~/.local/share
	LocalBin    string // ~/.local/bin
	NpmPrefix   string
	PnpmHome    string
	YarnBin     string
	BunHome     string
	VoltaHome   string
	DenoHome    string
	DotnetTools string
	ComposerBin string
	KrewRoot    string
	MiseData    string
	AsdfDir     string
	NixStore    string // /nix/store
	MacPorts    string // /opt/local
	XcodeCLT    string // /Library/Developer/CommandLineTools
}

// DefaultEnv builds Env from the process environment and fixed candidate paths.
// This is the only place os.Getenv is allowed for provenance configuration.
func DefaultEnv() Env {
	home, _ := os.UserHomeDir()
	if home == "" {
		home = os.Getenv("HOME")
	}
	e := Env{
		Home:       home,
		Path:       os.Getenv("PATH"),
		GoBin:      os.Getenv("GOBIN"),
		GoPath:     os.Getenv("GOPATH"),
		NixStore:   "/nix/store",
		MacPorts:   "/opt/local",
		XcodeCLT:   "/Library/Developer/CommandLineTools",
		CargoHome:  filepath.Join(home, ".cargo"),
		RustupHome: filepath.Join(home, ".rustup"),
		LocalShare: filepath.Join(home, ".local", "share"),
		LocalBin:   filepath.Join(home, ".local", "bin"),
		VoltaHome:  filepath.Join(home, ".volta"),
		DenoHome:   filepath.Join(home, ".deno"),
		DotnetTools: filepath.Join(home, ".dotnet", "tools"),
		KrewRoot:   filepath.Join(home, ".krew"),
		MiseData:   filepath.Join(home, ".local", "share", "mise"),
		AsdfDir:    filepath.Join(home, ".asdf"),
	}
	if e.GoPath == "" && home != "" {
		// empty GoPath is fine; GoBinDir handles defaults via home
	}
	if runtime.GOOS == "darwin" {
		e.BrewPrefixes = []string{"/opt/homebrew", "/usr/local"}
		e.PnpmHome = filepath.Join(home, "Library", "pnpm")
	} else {
		e.BrewPrefixes = []string{"/home/linuxbrew/.linuxbrew", "/usr/local"}
		e.PnpmHome = filepath.Join(home, ".local", "share", "pnpm")
	}
	e.YarnBin = filepath.Join(home, ".yarn", "bin")
	e.BunHome = filepath.Join(home, ".bun")
	// Composer: XDG or ~/.composer
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		e.ComposerBin = filepath.Join(xdg, "composer", "vendor", "bin")
	} else {
		e.ComposerBin = filepath.Join(home, ".composer", "vendor", "bin")
	}
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
