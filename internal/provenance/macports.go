package provenance

import (
	"path/filepath"

	"github.com/rtwsvj/hukou/internal/scan"
)

// MacPorts bins live under /opt/local/{bin,sbin,libexec} only — not the whole tree.
var macPortsSubdirs = []string{"bin", "sbin", "libexec"}

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
	if d.prefix == "" {
		return nil
	}
	for _, p := range binaryPaths(b) {
		p = filepath.Clean(p)
		for _, sub := range macPortsSubdirs {
			subPrefix := filepath.Join(d.prefix, sub)
			if pathHasPrefix(p, subPrefix) {
				return &Attribution{
					Source:     "macports",
					Package:    b.Name,
					Confidence: "inferred",
					Evidence:   "path prefix " + subPrefix,
				}
			}
		}
	}
	return nil
}
