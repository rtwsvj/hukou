// Portions of this file are adapted from gup (github.com/nao1215/gup,
// internal/goutil/pkginfo.go at commit 952fb83),
// Copyright 2022 CHIKAMATSU Naohiro, licensed under the Apache License,
// Version 2.0. See LICENSES/gup-APACHE-2.0.txt for the full license text.
//
// Modifications for hukou: removed printer/worker-pool dependencies,
// injectable environment instead of os.Getenv/exec, single-binary read API.
package provenance

import (
	"debug/buildinfo"
	"path/filepath"
	"strings"
)

// GoBinInfo is the provenance embedded in a Go binary at build time.
// It is readable from any "go install"-built binary — even one downloaded
// or copied by hand — via debug/buildinfo, with no subprocess involved.
type GoBinInfo struct {
	ImportPath string // main package import path (the "go install" argument)
	ModulePath string // main module path (basis for deriving the upstream repo)
	Version    string // module version; "(devel)" for local builds
	GoVersion  string // Go toolchain version that built the binary
}

// ReadGoBinary reads embedded build info from the binary at path.
// It returns ok=false for non-Go binaries, Go toolchain commands, and
// binaries without a recorded main module (see the filters below).
func ReadGoBinary(path string) (info *GoBinInfo, ok bool) {
	bi, err := buildinfo.ReadFile(path)
	if err != nil {
		return nil, false
	}
	if !shouldManageBinary(bi.Path, bi.Main.Path) {
		return nil, false
	}
	out := &GoBinInfo{
		ImportPath: bi.Path,
		ModulePath: bi.Main.Path,
		Version:    bi.Main.Version,
	}
	out.GoVersion, _, _ = strings.Cut(bi.GoVersion, " ")
	return out, true
}

// GoBinDir resolves the directory where "go install" places binaries,
// following the toolchain's own precedence: $GOBIN, then $GOPATH/bin,
// then the default $HOME/go/bin. All inputs are injected for testability;
// empty strings mean "unset".
func GoBinDir(gobin, gopath, home string) string {
	if gobin != "" {
		return gobin
	}
	if gopath != "" {
		return filepath.Join(gopath, "bin")
	}
	if home != "" {
		return filepath.Join(home, "go", "bin")
	}
	return ""
}

// isModuleBinary reports whether a binary was produced by
// "go install <module>@<version>" and therefore records a main module path.
// The argument is the main module path from the binary's build info
// (debug/buildinfo's Main.Path), not its import path.
//
// Standard library and toolchain binaries (e.g. cmd/gofmt) record no main
// module, as do GOPATH-mode or local "go build" binaries; those are skipped.
// Using the recorded module instead of a "dotless first import-path element"
// heuristic avoids misclassifying third-party binaries whose host has no dot,
// such as localhost/... or an internal registry hostname (gup issue #299).
func isModuleBinary(mainModulePath string) bool {
	return mainModulePath != ""
}

// isStandardLibraryCommand reports whether an import path is a Go standard
// library command such as "cmd/go" or "cmd/gofmt". These ship with the Go
// toolchain; version managers like mise place them in $GOBIN alongside
// user-installed binaries (gup issue #206). All Go commands live under the
// "cmd/" import-path prefix, and no third-party module can claim that prefix
// (its import path always starts with a host element), making this check
// unambiguous.
func isStandardLibraryCommand(importPath string) bool {
	return importPath == "cmd" || strings.HasPrefix(importPath, "cmd/")
}

// shouldManageBinary reports whether a binary carries usable Go provenance,
// given its import path and main module path from build info. Toolchain
// commands are attributed to the toolchain (not a module), and binaries with
// no recorded main module cannot be traced to an upstream.
func shouldManageBinary(importPath, mainModulePath string) bool {
	if isStandardLibraryCommand(importPath) {
		return false
	}
	return isModuleBinary(mainModulePath)
}
