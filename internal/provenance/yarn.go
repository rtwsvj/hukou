package provenance

import (
	"path/filepath"

	"github.com/rtwsvj/hukou/internal/scan"
)

type YarnDetector struct {
	bin string
}

func NewYarnDetector() *YarnDetector { return &YarnDetector{} }

func (d *YarnDetector) Name() string { return "yarn" }

func (d *YarnDetector) Load(env Env) error {
	d.bin = env.YarnBin
	return nil
}

func (d *YarnDetector) Match(b scan.Binary) *Attribution {
	if d.bin == "" {
		return nil
	}
	for _, p := range binaryPaths(b) {
		if _, ok := pathRelUnder(p, d.bin); !ok {
			continue
		}
		return &Attribution{
			Source:     "yarn",
			Package:    b.Name,
			Confidence: "inferred",
			Evidence:   "path prefix " + filepath.Clean(d.bin),
		}
	}
	return nil
}
