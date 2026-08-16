package provenance

import (
	"path/filepath"
	"strings"

	"github.com/rtwsvj/hukou/internal/scan"
)

type KrewDetector struct {
	bin string
}

func NewKrewDetector() *KrewDetector { return &KrewDetector{} }

func (d *KrewDetector) Name() string { return "krew" }

func (d *KrewDetector) Load(env Env) error {
	if env.KrewRoot != "" {
		d.bin = filepath.Join(env.KrewRoot, "bin")
	}
	return nil
}

func (d *KrewDetector) Match(b scan.Binary) *Attribution {
	for _, p := range binaryPaths(b) {
		if d.bin != "" && pathInDir(p, d.bin) {
			pkg := strings.TrimPrefix(b.Name, "kubectl-")
			return &Attribution{Source: "krew", Package: pkg, Confidence: "inferred", Evidence: "path prefix " + d.bin}
		}
	}
	return nil
}
