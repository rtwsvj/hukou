package provenance

import (
	"fmt"

	"github.com/rtwsvj/hukou/internal/scan"
)

// Runner runs a detector responsibility chain: first non-nil Match wins.
//
// DefaultRunner order (Phase-1, all wired):
// path-prefix package managers (brew/macports) → version managers (mise/asdf) →
// language package managers (cargo/npm/gem/…) → go (path + buildinfo) →
// system → unknown.
// First non-nil Match wins; unknown is always last and never returns nil.
type Runner struct {
	detectors []Detector
}

// NewRunner builds a chain from the given detectors in order.
func NewRunner(detectors ...Detector) *Runner {
	return &Runner{detectors: detectors}
}

// DefaultRunner returns the full Phase-1 detector chain (Tier-1 complete).
// Version managers (mise, asdf) precede language package managers so their
// directory trees are claimed first.
func DefaultRunner() *Runner {
	return NewRunner(
		NewHukouDetector(), // 自家户口本最权威,链首
		NewBrewDetector(),
		NewMacPortsDetector(),
		NewMiseDetector(),
		NewAsdfDetector(),
		NewCargoDetector(),
		NewRustupDetector(),
		NewNpmDetector(),
		NewPnpmDetector(),
		NewYarnDetector(),
		NewBunDetector(),
		NewPipUserDetector(),
		NewGemDetector(),
		NewNixDetector(),
		NewVoltaDetector(),
		NewDenoDetector(),
		NewDotnetDetector(),
		NewComposerDetector(),
		NewKrewDetector(),
		NewCurlInstallerDetector(),
		NewMacOSAppDetector(),
		NewLocalProjectDetector(),
		NewPipxDetector(),
		NewUVDetector(),
		NewGoDetector(), // includes buildinfo via gobin.go (unmodified vendor)
		NewSystemDetector(),
		NewUnknownDetector(),
	)
}

// Load calls Load(env) on every detector. A detector that returns error is
// skipped (removed from the chain) and recorded as a warning; remaining
// detectors continue. Never aborts the whole scan.
// Runner is not safe for concurrent use during Load or between Load and Match.
func (r *Runner) Load(env Env) (warnings []string) {
	loaded := make([]Detector, 0, len(r.detectors))
	for _, d := range r.detectors {
		if err := d.Load(env); err != nil {
			warnings = append(warnings, fmt.Sprintf("detector %s load failed: %v", d.Name(), err))
			continue
		}
		loaded = append(loaded, d)
	}
	r.detectors = loaded
	return warnings
}

// Match runs the chain until a detector returns non-nil Attribution.
func (r *Runner) Match(b scan.Binary) *Attribution {
	for _, d := range r.detectors {
		if a := d.Match(b); a != nil {
			return a
		}
	}
	return nil
}

// Detectors returns the chain (for tests / inspection).
func (r *Runner) Detectors() []Detector {
	return r.detectors
}
