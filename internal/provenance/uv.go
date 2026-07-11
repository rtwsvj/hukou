package provenance

import (
	"path/filepath"

	"github.com/rtwsvj/hukou/internal/scan"
)

type UVDetector struct {
	tools    string
	localBin string
}

func NewUVDetector() *UVDetector { return &UVDetector{} }

func (d *UVDetector) Name() string { return "uv" }

func (d *UVDetector) Load(env Env) error {
	d.tools = env.UVTools
	d.localBin = env.LocalBin
	return nil
}

func (d *UVDetector) Match(b scan.Binary) *Attribution {
	if d.tools == "" {
		return nil
	}
	for _, p := range binaryPaths(b) {
		rel, ok := pathRelUnder(p, d.tools)
		if !ok {
			continue
		}
		parts := pathParts(rel)
		if len(parts) >= 3 && parts[1] == "bin" {
			return &Attribution{
				Source:     "uv",
				Package:    parts[0],
				Confidence: "exact",
				Evidence:   "realpath under " + filepath.Join(d.tools, parts[0], "bin"),
			}
		}
	}
	return nil
}
