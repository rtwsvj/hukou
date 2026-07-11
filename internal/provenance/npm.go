package provenance

import (
	"path/filepath"

	"github.com/rtwsvj/hukou/internal/scan"
)

type NpmDetector struct {
	prefixes []string
}

func NewNpmDetector() *NpmDetector { return &NpmDetector{} }

func (d *NpmDetector) Name() string { return "npm" }

func (d *NpmDetector) Load(env Env) error {
	prefixes := env.NpmPrefixes
	if len(prefixes) == 0 && env.NpmPrefix != "" {
		prefixes = []string{env.NpmPrefix}
	}
	d.prefixes = uniquePaths(prefixes)
	return nil
}

func (d *NpmDetector) Match(b scan.Binary) *Attribution {
	for _, prefix := range d.prefixes {
		nodeModules := filepath.Join(prefix, "lib", "node_modules")
		dotBin := filepath.Join(nodeModules, ".bin")
		for _, p := range binaryPaths(b) {
			pkg := nodePackageFromPath(p, nodeModules)
			if pkg == "" && !pathInDir(p, dotBin) {
				continue
			}
			if pkg == "" {
				pkg = b.Name
			}
			return &Attribution{
				Source:     "npm",
				Package:    pkg,
				Confidence: "inferred",
				Evidence:   "npm prefix " + filepath.Clean(prefix),
			}
		}
	}
	return nil
}
