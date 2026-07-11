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

// Match only attributes binaries that live in vendor/bin (not the whole vendor tree).
// Package name may still be inferred from RealPath under the sibling vendor packages.
func (d *ComposerDetector) Match(b scan.Binary) *Attribution {
	for _, bin := range d.bins {
		if bin == "" {
			continue
		}
		inBin := false
		for _, p := range binaryPaths(b) {
			if pathInDir(p, bin) {
				inBin = true
				break
			}
		}
		if !inBin {
			continue
		}
		vendor := filepath.Dir(bin)
		pkg := ""
		for _, p := range binaryPaths(b) {
			if got := composerPackageFromPath(p, vendor); got != "" {
				pkg = got
				break
			}
		}
		if pkg == "" {
			pkg = b.Name
		}
		return &Attribution{
			Source:     "composer",
			Package:    pkg,
			Confidence: "inferred",
			Evidence:   "composer vendor bin " + filepath.Clean(bin),
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
	// vendor/<vendor>/<package>/... — not vendor/bin/...
	if len(parts) >= 2 && parts[0] != "bin" {
		return parts[0] + "/" + parts[1]
	}
	return ""
}
