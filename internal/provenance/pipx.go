package provenance

import (
	"path/filepath"

	"github.com/rtwsvj/hukou/internal/scan"
)

type PipxDetector struct {
	venvs    []string
	localBin string
}

func NewPipxDetector() *PipxDetector { return &PipxDetector{} }

func (d *PipxDetector) Name() string { return "pipx" }

func (d *PipxDetector) Load(env Env) error {
	d.venvs = uniquePaths(env.PipxVenvs)
	d.localBin = env.LocalBin
	return nil
}

func (d *PipxDetector) Match(b scan.Binary) *Attribution {
	for _, root := range d.venvs {
		for _, p := range binaryPaths(b) {
			rel, ok := pathRelUnder(p, root)
			if !ok {
				continue
			}
			parts := pathParts(rel)
			if len(parts) >= 3 && parts[1] == "bin" {
				return &Attribution{
					Source:     "pipx",
					Package:    parts[0],
					Confidence: "exact",
					Evidence:   "realpath under " + filepath.Join(root, parts[0], "bin"),
				}
			}
		}
	}
	return nil
}
