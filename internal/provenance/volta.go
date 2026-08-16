package provenance

import (
	"path/filepath"

	"github.com/rtwsvj/hukou/internal/scan"
)

type VoltaDetector struct {
	bin string
}

func NewVoltaDetector() *VoltaDetector { return &VoltaDetector{} }

func (d *VoltaDetector) Name() string { return "volta" }

func (d *VoltaDetector) Load(env Env) error {
	if env.VoltaHome != "" {
		d.bin = filepath.Join(env.VoltaHome, "bin")
	}
	return nil
}

func (d *VoltaDetector) Match(b scan.Binary) *Attribution {
	for _, p := range binaryPaths(b) {
		if d.bin != "" && pathInDir(p, d.bin) {
			return &Attribution{Source: "volta", Package: b.Name, Confidence: "inferred", Evidence: "path prefix " + d.bin}
		}
	}
	return nil
}
