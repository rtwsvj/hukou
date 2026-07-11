package provenance

import (
	"github.com/rtwsvj/hukou/internal/scan"
)

type MacPortsDetector struct {
	prefix string
}

func NewMacPortsDetector() *MacPortsDetector { return &MacPortsDetector{} }

func (d *MacPortsDetector) Name() string { return "macports" }

func (d *MacPortsDetector) Load(env Env) error {
	d.prefix = env.MacPorts
	return nil
}

func (d *MacPortsDetector) Match(b scan.Binary) *Attribution {
	for _, p := range binaryPaths(b) {
		if _, ok := pathRelUnder(p, d.prefix); ok {
			return &Attribution{
				Source:     "macports",
				Package:    b.Name,
				Confidence: "inferred",
				Evidence:   "path prefix " + d.prefix,
			}
		}
	}
	return nil
}
