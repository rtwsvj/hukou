package provenance

import (
	"path/filepath"

	"github.com/rtwsvj/hukou/internal/scan"
)

// PipUserDetector attributes `pip install --user` scripts. Two layouts exist:
// darwin nests per interpreter version (<base>/<version>/bin/<name>, base
// ~/Library/Python); Linux installs flat into ~/.local/bin, modeled as
// <base>/bin/<name> with base ~/.local. The matcher accepts both shapes for
// either base so a detector stays correct if the layout moves.
type PipUserDetector struct {
	base string
}

func NewPipUserDetector() *PipUserDetector { return &PipUserDetector{} }

func (d *PipUserDetector) Name() string { return "pip-user" }

func (d *PipUserDetector) Load(env Env) error {
	d.base = env.PipUserBase
	return nil
}

func (d *PipUserDetector) Match(b scan.Binary) *Attribution {
	for _, p := range binaryPaths(b) {
		rel, ok := pathRelUnder(p, d.base)
		if !ok {
			continue
		}
		parts := pathParts(rel)
		switch {
		case len(parts) >= 3 && parts[1] == "bin":
			// Versioned layout: <base>/<version>/bin/<name>.
			return &Attribution{
				Source:     "pip-user",
				Package:    b.Name,
				Version:    parts[0],
				Confidence: "inferred",
				Evidence:   "path prefix " + filepath.Join(d.base, parts[0], "bin"),
			}
		case len(parts) >= 2 && parts[0] == "bin":
			// Flat layout: <base>/bin/<name> (no version component).
			return &Attribution{
				Source:     "pip-user",
				Package:    b.Name,
				Confidence: "inferred",
				Evidence:   "path prefix " + filepath.Join(d.base, "bin"),
			}
		}
	}
	return nil
}
