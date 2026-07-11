package provenance

import (
	"path/filepath"

	"github.com/rtwsvj/hukou/internal/scan"
)

type PnpmDetector struct {
	home string
}

func NewPnpmDetector() *PnpmDetector { return &PnpmDetector{} }

func (d *PnpmDetector) Name() string { return "pnpm" }

func (d *PnpmDetector) Load(env Env) error {
	d.home = env.PnpmHome
	return nil
}

func (d *PnpmDetector) Match(b scan.Binary) *Attribution {
	if d.home == "" {
		return nil
	}
	for _, p := range binaryPaths(b) {
		if _, ok := pathRelUnder(p, d.home); !ok {
			continue
		}
		pkg, ver := pnpmPackageVersion(p)
		if pkg == "" {
			pkg = b.Name
		}
		return &Attribution{
			Source:     "pnpm",
			Package:    pkg,
			Version:    ver,
			Confidence: "inferred",
			Evidence:   "path prefix " + filepath.Clean(d.home),
		}
	}
	return nil
}
