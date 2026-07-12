package provenance

import (
	"path/filepath"

	"github.com/rtwsvj/hukou/internal/manifest"
	"github.com/rtwsvj/hukou/internal/scan"
)

// HukouDetector attributes binaries that hukou itself has adopted, by
// consulting the hukou manifest. It runs first in the chain: an adopted
// binary's provenance is authoritative regardless of where the file lives.
type HukouDetector struct {
	byPath map[string]manifest.Entry
}

func NewHukouDetector() *HukouDetector { return &HukouDetector{} }

func (d *HukouDetector) Name() string { return "hukou" }

func (d *HukouDetector) Load(env Env) error {
	d.byPath = map[string]manifest.Entry{}
	if env.HukouManifest == "" {
		return nil
	}
	m, err := manifest.Load(env.HukouManifest)
	if err != nil {
		// A broken manifest must not fail the scan; the chain reports it
		// as a warning and the entries simply stay unattributed.
		return err
	}
	for _, e := range m.Entries {
		d.byPath[filepath.Clean(e.Path)] = e
	}
	return nil
}

func (d *HukouDetector) Match(b scan.Binary) *Attribution {
	for _, p := range binaryPaths(b) {
		e, ok := d.byPath[filepath.Clean(p)]
		if !ok {
			continue
		}
		return &Attribution{
			Source:     "hukou",
			Package:    e.Name,
			Version:    e.Tag,
			Upstream:   e.Repo,
			Confidence: "exact",
			Evidence:   "registered in hukou manifest",
		}
	}
	return nil
}
