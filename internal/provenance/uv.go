package provenance

import (
	"path/filepath"
	"strings"

	"github.com/rtwsvj/hukou/internal/scan"
)

type UVDetector struct {
	tools      string
	localBin   string
	localShare string
}

func NewUVDetector() *UVDetector { return &UVDetector{} }

func (d *UVDetector) Name() string { return "uv" }

func (d *UVDetector) Load(env Env) error {
	d.tools = env.UVTools
	d.localBin = env.LocalBin
	d.localShare = env.LocalShare
	return nil
}

func (d *UVDetector) Match(b scan.Binary) *Attribution {
	// uv tools: <UVTools>/<pkg>/bin/...
	if d.tools != "" {
		for _, p := range binaryPaths(b) {
			rel, ok := pathRelUnder(p, d.tools)
			if !ok {
				continue
			}
			parts := pathParts(rel)
			if len(parts) >= 3 && parts[1] == "bin" {
				return &Attribution{
					Source:     "uv",
					Package:    parts[0],
					Confidence: "exact",
					Evidence:   "realpath under " + filepath.Join(d.tools, parts[0], "bin"),
				}
			}
		}
	}

	// uv-managed Python: <LocalShare>/uv/python/cpython-<ver>-<platform>/...
	if d.localShare != "" {
		pythonRoot := filepath.Join(d.localShare, "uv", "python")
		for _, p := range binaryPaths(b) {
			rel, ok := pathRelUnder(p, pythonRoot)
			if !ok {
				continue
			}
			parts := pathParts(rel)
			if len(parts) < 1 {
				continue
			}
			pkg, ver := cpythonDirPackageVersion(parts[0])
			if pkg == "" {
				continue
			}
			return &Attribution{
				Source:     "uv",
				Package:    pkg,
				Version:    ver,
				Confidence: "exact",
				Evidence:   "realpath under " + filepath.Join(pythonRoot, parts[0]),
			}
		}
	}
	return nil
}

// cpythonDirPackageVersion parses "cpython-3.11.14-macos-aarch64-none"
// → ("cpython", "3.11.14").
func cpythonDirPackageVersion(dir string) (packageName, version string) {
	const prefix = "cpython-"
	if !strings.HasPrefix(dir, prefix) {
		return "", ""
	}
	rest := dir[len(prefix):]
	if rest == "" {
		return "cpython", ""
	}
	// Version is the leading digit/dot run until a '-' followed by a non-digit
	// platform token (e.g. macos, aarch64, x86_64).
	for i := 0; i < len(rest); i++ {
		if rest[i] == '-' && i+1 < len(rest) && (rest[i+1] < '0' || rest[i+1] > '9') {
			return "cpython", rest[:i]
		}
	}
	return "cpython", rest
}
