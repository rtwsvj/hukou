package provenance

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/rtwsvj/hukou/internal/manifest"
	"github.com/rtwsvj/hukou/internal/scan"
	"github.com/rtwsvj/hukou/internal/store"
	statejournal "github.com/rtwsvj/hukou/internal/transaction"
)

// HukouDetector attributes binaries that hukou itself has adopted, by
// consulting the hukou manifest. It runs first in the chain: an adopted
// binary's provenance is authoritative regardless of where the file lives.
type HukouDetector struct {
	byPath map[string]manifest.Entry
	notes  []string
}

func NewHukouDetector() *HukouDetector { return &HukouDetector{} }

func (d *HukouDetector) Name() string { return "hukou" }

// Notes returns non-fatal advisories collected during Load (e.g. verified
// stale completed journal residue). The Runner surfaces them on its dedicated
// notes channel while keeping this detector in the chain; they never enter the
// warnings channel that security gates key on.
func (d *HukouDetector) Notes() []string { return d.notes }

func (d *HukouDetector) Load(env Env) error {
	d.byPath = map[string]manifest.Entry{}
	d.notes = nil
	if env.HukouManifest == "" {
		return nil
	}
	// The read path tolerates exactly one journal residue class: a VERIFIED
	// completed-* journal (exact completed-<32-hex> name, real directory,
	// COMMIT marker matching the id). Such a transaction is committed and
	// converged, so adopted binaries stay consistent with the manifest and the
	// detector keeps attributing; the residue is only recorded as an advisory
	// note. Pending-*, building-* (potentially another process's active Begin
	// — see CheckReadable's race rationale), unknown, and malformed entries
	// all keep failing closed: Load errors, the runner drops the detector, and
	// registered binaries degrade to later detectors with a warning.
	notes, err := statejournal.CheckReadable(filepath.Dir(env.HukouManifest))
	if err != nil {
		return fmt.Errorf("hukou state may be inconsistent: %w", err)
	}
	d.notes = notes
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
		upstream := e.Upstream
		if upstream == "" {
			upstream = e.Repo
		}
		a := &Attribution{
			Source:     "hukou",
			Package:    e.Name,
			Version:    e.Tag,
			Upstream:   upstream,
			Confidence: "inferred",
		}
		actualSHA, err := store.SHA256File(p)
		switch {
		case err != nil:
			a.Evidence = fmt.Sprintf("registered in hukou manifest; sha256 unreadable: %v", err)
		case !strings.EqualFold(actualSHA, e.SHA256):
			a.Evidence = fmt.Sprintf("registered in hukou manifest; sha256 mismatch: got %s, want %s", actualSHA, e.SHA256)
		default:
			a.Confidence = "exact"
			a.Evidence = "registered in hukou manifest; sha256 verified"
		}
		return a
	}
	return nil
}
