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

func pnpmPackageVersion(path string) (string, string) {
	parts := pathParts(path)
	for i := 0; i+1 < len(parts); i++ {
		if parts[i] != ".pnpm" {
			continue
		}
		entry := parts[i+1]
		pkg := entry
		if strings.HasPrefix(entry, "@") {
			if at := strings.LastIndex(entry, "@"); at > 0 {
				pkg = strings.ReplaceAll(entry[:at], "+", "/")
				return pkg, entry[at+1:]
			}
			return strings.ReplaceAll(entry, "+", "/"), ""
		}
		if at := strings.LastIndex(entry, "@"); at > 0 {
			return entry[:at], entry[at+1:]
		}
		return pkg, ""
	}
	return "", ""
}

func splitNameVersion(s string) (string, string) {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] != '-' || i+1 >= len(s) {
			continue
		}
		c := s[i+1]
		if (c >= '0' && c <= '9') || c == 'v' {
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
