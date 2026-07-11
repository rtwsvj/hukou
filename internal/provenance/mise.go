package provenance

import (
	"path/filepath"

	"github.com/rtwsvj/hukou/internal/scan"
)

type MiseDetector struct {
	data     string
	shims    string
	installs string
}

func NewMiseDetector() *MiseDetector { return &MiseDetector{} }

func (d *MiseDetector) Name() string { return "mise" }

func (d *MiseDetector) Load(env Env) error {
	d.data = env.MiseData
	if env.MiseData != "" {
		d.shims = filepath.Join(env.MiseData, "shims")
		d.installs = filepath.Join(env.MiseData, "installs")
	}
	return nil
}

func (d *MiseDetector) Match(b scan.Binary) *Attribution {
	for _, p := range binaryPaths(b) {
		if d.installs != "" {
			rel, ok := pathRelUnder(p, d.installs)
			if ok {
				parts := pathParts(rel)
				if len(parts) >= 3 {
					return &Attribution{
						Source:     "mise",
						Package:    parts[0],
						Version:    parts[1],
						Confidence: "exact",
						Evidence:   "realpath under " + filepath.Join(d.installs, parts[0], parts[1]),
					}
				}
			}
		}
		if d.shims != "" && pathInDir(p, d.shims) {
			return &Attribution{
				Source:     "mise",
				Package:    b.Name,
				Confidence: "inferred",
				Evidence:   "shim in " + d.shims,
			}
		}
	}
	return nil
}
