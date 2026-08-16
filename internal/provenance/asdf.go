package provenance

import (
	"path/filepath"

	"github.com/rtwsvj/hukou/internal/scan"
)

type AsdfDetector struct {
	dir      string
	shims    string
	installs string
}

func NewAsdfDetector() *AsdfDetector { return &AsdfDetector{} }

func (d *AsdfDetector) Name() string { return "asdf" }

func (d *AsdfDetector) Load(env Env) error {
	d.dir = env.AsdfDir
	if env.AsdfDir != "" {
		d.shims = filepath.Join(env.AsdfDir, "shims")
		d.installs = filepath.Join(env.AsdfDir, "installs")
	}
	return nil
}

func (d *AsdfDetector) Match(b scan.Binary) *Attribution {
	for _, p := range binaryPaths(b) {
		if d.installs != "" {
			rel, ok := pathRelUnder(p, d.installs)
			if ok {
				parts := pathParts(rel)
				if len(parts) >= 3 {
					return &Attribution{
						Source:     "asdf",
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
				Source:     "asdf",
				Package:    b.Name,
				Confidence: "inferred",
				Evidence:   "shim in " + d.shims,
			}
		}
	}
	return nil
}
