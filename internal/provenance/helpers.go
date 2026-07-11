package provenance

import (
	"path/filepath"
	"strings"

	"github.com/rtwsvj/hukou/internal/scan"
)

func binaryPaths(b scan.Binary) []string {
	return uniquePaths([]string{b.RealPath, b.Path})
}

func pathRelUnder(path, prefix string) (string, bool) {
	if path == "" || prefix == "" {
		return "", false
	}
	path = filepath.Clean(path)
	prefix = filepath.Clean(prefix)
	if !pathHasPrefix(path, prefix) {
		return "", false
	}
	rel, err := filepath.Rel(prefix, path)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return "", false
	}
	return rel, true
}

func pathInDir(path, dir string) bool {
	if path == "" || dir == "" {
		return false
	}
	return filepath.Clean(filepath.Dir(path)) == filepath.Clean(dir)
}

func pathParts(rel string) []string {
	if rel == "" || rel == "." {
		return nil
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	out := parts[:0]
	for _, p := range parts {
		if p != "" && p != "." {
			out = append(out, p)
		}
	}
	return out
}

func nodePackageFromRel(rel string) string {
	parts := pathParts(rel)
	if len(parts) == 0 {
		return ""
	}
	if parts[0] == ".bin" {
		return ""
	}
	if strings.HasPrefix(parts[0], "@") && len(parts) >= 2 {
		return parts[0] + "/" + parts[1]
	}
	return parts[0]
}

func nodePackageFromPath(path, nodeModules string) string {
	rel, ok := pathRelUnder(path, nodeModules)
	if !ok {
		return ""
	}
	return nodePackageFromRel(rel)
}

// pnpmPackageVersion extracts package name and version from a path under a
// pnpm store (.pnpm/<entry>/...). Scoped packages use @scope+name@version;
// peer-dep encodings after '_' on the version are stripped.
//
// Version separator is the first '@' for unscoped names, or the first '@'
// after the leading scope marker for scoped names — never LastIndex, because
// peer-dep segments also contain '@' (e.g. react@18.2.0_react-dom@18.2.0).
func pnpmPackageVersion(path string) (string, string) {
	parts := pathParts(path)
	for i := 0; i+1 < len(parts); i++ {
		if parts[i] != ".pnpm" {
			continue
		}
		return parsePnpmEntry(parts[i+1])
	}
	return "", ""
}

func parsePnpmEntry(entry string) (string, string) {
	if entry == "" {
		return "", ""
	}
	if strings.HasPrefix(entry, "@") {
		// @scope+name@version[_peers...]
		rest := entry[1:]
		at := strings.IndexByte(rest, '@')
		if at < 0 {
			return strings.ReplaceAll(entry, "+", "/"), ""
		}
		pkg := "@" + strings.ReplaceAll(rest[:at], "+", "/")
		return pkg, stripPnpmPeerDeps(rest[at+1:])
	}
	// name@version[_peers...]
	at := strings.IndexByte(entry, '@')
	if at <= 0 {
		return entry, ""
	}
	return entry[:at], stripPnpmPeerDeps(entry[at+1:])
}

// stripPnpmPeerDeps removes the peer-dependency encoding suffix after '_'.
// e.g. "18.2.0_react-dom@18.2.0" → "18.2.0"
func stripPnpmPeerDeps(ver string) string {
	if i := strings.IndexByte(ver, '_'); i >= 0 {
		return ver[:i]
	}
	return ver
}

// splitNameVersion splits a nix-style name-version string: package name is
// everything before the first '-' that is followed by a digit; the rest is
// the version. No such hyphen → whole string is the name, empty version.
// e.g. glibc-2.39-5 → (glibc, 2.39-5); python3.11 → (python3.11, "").
func splitNameVersion(s string) (string, string) {
	for i := 0; i < len(s)-1; i++ {
		if s[i] == '-' && s[i+1] >= '0' && s[i+1] <= '9' {
			return s[:i], s[i+1:]
		}
	}
	return s, ""
}

func sourcePackageFallback(name, fallback string) string {
	if name != "" {
		return name
	}
	return fallback
}
