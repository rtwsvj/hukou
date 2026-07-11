package provenance

import (
	"path/filepath"

	"github.com/rtwsvj/hukou/internal/scan"
)

type DenoDetector struct {
	bin string
}

func NewDenoDetector() *DenoDetector { return &DenoDetector{} }

func (d *DenoDetector) Name() string { return "deno" }

func (d *DenoDetector) Load(env Env) error {
	if env.DenoHome != "" {
		d.bin = filepath.Join(env.DenoHome, "bin")
	}
	return nil
}

func (d *DenoDetector) Match(b scan.Binary) *Attribution {
	for _, p := range binaryPaths(b) {
		if d.bin != "" && pathInDir(p, d.bin) {
			return &Attribution{Source: "deno", Package: b.Name, Confidence: "inferred", Evidence: "path prefix " + d.bin}
		}
	}
	return nil
}
