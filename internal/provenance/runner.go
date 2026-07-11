package provenance

import "github.com/rtwsvj/hukou/internal/scan"

// Runner runs a detector responsibility chain: first non-nil Match wins.
//
// Spec order: path-prefix detectors → symlink-resolution → buildinfo (gobin)
// → system → unknown. Skeleton wires system + unknown; gobin slot is reserved
// for a future go detector that calls ReadGoBinary from gobin.go.
type Runner struct {
	detectors []Detector
}

// NewRunner builds a chain from the given detectors in order.
func NewRunner(detectors ...Detector) *Runner {
	return &Runner{detectors: detectors}
}

// DefaultRunner returns the Phase-1 skeleton chain:
//
//	[/* Tier-1 detectors reserved */ system, unknown]
//
// Slot for go/buildinfo: insert a Detector that uses ReadGoBinary / GoBinDir
// before system. Other Tier-1 detectors insert before system as well.
func DefaultRunner() *Runner {
	return NewRunner(
		// --- reserved: path-prefix / symlink / buildinfo detectors ---
		// e.g. brew, cargo, go (uses gobin.go), npm, ...
		NewSystemDetector(),
		NewUnknownDetector(),
	)
}

// Load calls Load(env) on every detector. Stops on first error.
func (r *Runner) Load(env Env) error {
	for _, d := range r.detectors {
		if err := d.Load(env); err != nil {
			return err
		}
	}
	return nil
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
