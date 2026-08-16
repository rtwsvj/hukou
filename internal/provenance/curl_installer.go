package provenance

import (
	"path/filepath"
	"strings"

	"github.com/rtwsvj/hukou/internal/scan"
)

type CurlInstallerDetector struct {
	roots []CurlInstallerRoot
}

func NewCurlInstallerDetector() *CurlInstallerDetector { return &CurlInstallerDetector{} }

func (d *CurlInstallerDetector) Name() string { return "curl-installer" }

func (d *CurlInstallerDetector) Load(env Env) error {
	d.roots = env.CurlInstallerRoots
	return nil
}

func (d *CurlInstallerDetector) Match(b scan.Binary) *Attribution {
	for _, root := range d.roots {
		if root.Dir == "" {
			continue
		}
		for _, p := range binaryPaths(b) {
			if _, ok := pathRelUnder(p, root.Dir); ok || pathInDir(p, root.Dir) {
				pkg := root.Package
				if pkg == "" {
					pkg = b.Name
				}
				a := &Attribution{
					Source:     "curl-installer",
					Package:    pkg,
					Confidence: "inferred",
					Evidence:   "known installer directory " + filepath.Clean(root.Dir),
				}
				if pkg == "codex" {
					if ver := codexVersionFromPath(p, root.Dir); ver != "" {
						a.Version = ver
					}
				}
				return a
			}
		}
	}
	return nil
}

// codexVersionFromPath extracts <ver> from a path under packages/:
// standalone/releases/<ver>-<platform>/bin/codex → <ver>
// e.g. .../packages/standalone/releases/0.142.3-aarch64-apple-darwin/bin/codex → "0.142.3"
func codexVersionFromPath(path, packagesRoot string) string {
	rel, ok := pathRelUnder(path, packagesRoot)
	if !ok {
		return ""
	}
	parts := pathParts(rel)
	for i := 0; i+1 < len(parts); i++ {
		if parts[i] == "releases" {
			return codexVersionFromReleaseDir(parts[i+1])
		}
	}
	return ""
}

// codexVersionFromReleaseDir splits "<ver>-<platform>" on the first '-' whose
// following character is non-digit (platform starts with a letter, e.g. aarch64).
func codexVersionFromReleaseDir(dir string) string {
	if dir == "" {
		return ""
	}
	for i := 0; i < len(dir); i++ {
		if dir[i] == '-' && i+1 < len(dir) && (dir[i+1] < '0' || dir[i+1] > '9') {
			return dir[:i]
		}
	}
	// No platform suffix; treat whole segment as version if it looks version-like.
	if strings.ContainsAny(dir, "0123456789") {
		return dir
	}
	return ""
}
