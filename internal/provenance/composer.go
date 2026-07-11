package provenance

import (
	"path/filepath"

	"github.com/rtwsvj/hukou/internal/scan"
)

type ComposerDetector struct {
	bins []string
}

func NewComposerDetector() *ComposerDetector { return &ComposerDetector{} }

func (d *ComposerDetector) Name() string { return "composer" }

func (d *ComposerDetector) Load(env Env) error {
	bins := env.ComposerBins
	if len(bins) == 0 && env.ComposerBin != "" {
		bins = []string{env.ComposerBin}
	}
	d.bins = uniquePaths(bins)
	return nil
}

func (d *ComposerDetector) Match(b scan.Binary) *Attribution {
	for _, bin := range d.bins {
		vendor := filepath.Dir(bin)
		for _, p := range binaryPaths(b) {
			if pathInDir(p, bin) || pathHasPrefix(filepath.Clean(p), vendor) {
				pkg := composerPackageFromPath(p, vendor)
				if pkg == "" {
					pkg = b.Name
				}
				return &Attribution{Source: "composer", Package: pkg, Confidence: "inferred", Evidence: "composer vendor bin " + filepath.Clean(bin)}
			}
		}
	}
	return nil
}

func composerPackageFromPath(path, vendor string) string {
	rel, ok := pathRelUnder(path, vendor)
	if !ok {
		return ""
	}
	parts := pathParts(rel)
	if len(parts) >= 2 && parts[0] != "bin" {
		return parts[0] + "/" + parts[1]
	}
	return ""
}
