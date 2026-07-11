package provenance

import (
	"path/filepath"

	"github.com/rtwsvj/hukou/internal/scan"
)

type GemDetector struct {
	roots []string
	bins  []string
}

func NewGemDetector() *GemDetector { return &GemDetector{} }

func (d *GemDetector) Name() string { return "gem" }

func (d *GemDetector) Load(env Env) error {
	d.roots = uniquePaths(env.GemRoots)
	d.bins = uniquePaths(env.GemBinDirs)
	return nil
}

func (d *GemDetector) Match(b scan.Binary) *Attribution {
	for _, p := range binaryPaths(b) {
		for _, bin := range d.bins {
			if pathInDir(p, bin) {
				return &Attribution{Source: "gem", Package: b.Name, Confidence: "inferred", Evidence: "gem bindir " + filepath.Clean(bin)}
			}
		}
		for _, root := range d.roots {
			rel, ok := pathRelUnder(p, root)
			if !ok {
				continue
			}
			parts := pathParts(rel)
			if len(parts) >= 3 && parts[1] == "bin" {
				return &Attribution{Source: "gem", Package: b.Name, Version: parts[0], Confidence: "inferred", Evidence: "gem bindir " + filepath.Join(root, parts[0], "bin")}
			}
			if len(parts) >= 4 && parts[1] == "gems" {
				pkg, ver := splitNameVersion(parts[2])
				return &Attribution{Source: "gem", Package: sourcePackageFallback(pkg, b.Name), Version: ver, Confidence: "exact", Evidence: "realpath under " + filepath.Join(root, parts[0], "gems", parts[2])}
			}
		}
	}
	return nil
}
