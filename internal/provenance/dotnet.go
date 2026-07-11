package provenance

import (
	"path/filepath"

	"github.com/rtwsvj/hukou/internal/scan"
)

type DotnetDetector struct {
	tools string
}

func NewDotnetDetector() *DotnetDetector { return &DotnetDetector{} }

func (d *DotnetDetector) Name() string { return "dotnet" }

func (d *DotnetDetector) Load(env Env) error {
	d.tools = env.DotnetTools
	return nil
}

func (d *DotnetDetector) Match(b scan.Binary) *Attribution {
	for _, p := range binaryPaths(b) {
		if d.tools != "" && pathInDir(p, d.tools) {
			return &Attribution{Source: "dotnet", Package: b.Name, Confidence: "inferred", Evidence: "path prefix " + filepath.Clean(d.tools)}
		}
	}
	return nil
}
