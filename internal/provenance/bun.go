package provenance

import (
	"path/filepath"

	"github.com/rtwsvj/hukou/internal/scan"
)

type BunDetector struct {
	home string
	bin  string
}

func NewBunDetector() *BunDetector { return &BunDetector{} }

func (d *BunDetector) Name() string { return "bun" }

func (d *BunDetector) Load(env Env) error {
	d.home = env.BunHome
	if env.BunHome != "" {
		d.bin = filepath.Join(env.BunHome, "bin")
	}
	return nil
}

func (d *BunDetector) Match(b scan.Binary) *Attribution {
	if d.bin == "" {
		return nil
	}
	for _, p := range binaryPaths(b) {
		if _, ok := pathRelUnder(p, d.bin); !ok {
			continue
		}
		return &Attribution{
			Source:     "bun",
			Package:    b.Name,
			Confidence: "inferred",
			Evidence:   "path prefix " + filepath.Clean(d.bin),
		}
	}
	return nil
}
