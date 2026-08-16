package provenance

import (
	"path/filepath"

	"github.com/rtwsvj/hukou/internal/scan"
)

type PipUserDetector struct {
	base string
}

func NewPipUserDetector() *PipUserDetector { return &PipUserDetector{} }

func (d *PipUserDetector) Name() string { return "pip-user" }

func (d *PipUserDetector) Load(env Env) error {
	d.base = env.PipUserBase
	return nil
}

func (d *PipUserDetector) Match(b scan.Binary) *Attribution {
	if d.base == "" {
		return nil
	}
	for _, p := range binaryPaths(b) {
		rel, ok := pathRelUnder(p, d.base)
		if !ok {
			continue
		}
		parts := pathParts(rel)
		if len(parts) >= 3 && parts[1] == "bin" {
			return &Attribution{
				Source:     "pip-user",
				Package:    b.Name,
				Version:    parts[0],
				Confidence: "inferred",
				Evidence:   "path prefix " + filepath.Join(d.base, parts[0], "bin"),
			}
		}
	}
	return nil
}
