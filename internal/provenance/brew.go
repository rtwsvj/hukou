package provenance

import (
	"path/filepath"

	"github.com/rtwsvj/hukou/internal/scan"
)

type BrewDetector struct {
	prefixes []string
}

func NewBrewDetector() *BrewDetector { return &BrewDetector{} }

func (d *BrewDetector) Name() string { return "brew" }

func (d *BrewDetector) Load(env Env) error {
	d.prefixes = uniquePaths(env.BrewPrefixes)
	return nil
}

func (d *BrewDetector) Match(b scan.Binary) *Attribution {
	// brew binary itself at well-known prefix locations.
	for _, p := range binaryPaths(b) {
		for _, prefix := range d.prefixes {
			brewBin := filepath.Join(prefix, "bin", "brew")
			if filepath.Clean(p) == filepath.Clean(brewBin) {
				return &Attribution{
					Source:     "brew",
					Package:    "homebrew",
					Confidence: "exact",
					Evidence:   "brew executable at " + brewBin,
				}
			}
		}
	}

	// Caskroom: <prefix>/Caskroom/<cask>/...
	for _, p := range binaryPaths(b) {
		for _, prefix := range d.prefixes {
			caskroom := filepath.Join(prefix, "Caskroom")
			rel, ok := pathRelUnder(p, caskroom)
			if !ok {
				continue
			}
			parts := pathParts(rel)
			if len(parts) < 1 {
				continue
			}
			return &Attribution{
				Source:     "brew",
				Package:    parts[0],
				Confidence: "exact",
				Evidence:   "realpath under " + filepath.Join(caskroom, parts[0]),
			}
		}
	}

	// Cellar: <prefix>/Cellar/<formula>/<ver>/...
	for _, p := range binaryPaths(b) {
		for _, prefix := range d.prefixes {
			cellar := filepath.Join(prefix, "Cellar")
			rel, ok := pathRelUnder(p, cellar)
			if !ok {
				continue
			}
			parts := pathParts(rel)
			if len(parts) < 2 {
				continue
			}
			return &Attribution{
				Source:     "brew",
				Package:    parts[0],
				Version:    parts[1],
				Confidence: "exact",
				Evidence:   "realpath under " + filepath.Join(cellar, parts[0], parts[1]),
			}
		}
	}
	return nil
}
