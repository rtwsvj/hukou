package provenance

import "github.com/rtwsvj/hukou/internal/scan"

// Runner runs a detector responsibility chain: first non-nil Match wins.
//
// DefaultRunner order (Phase-1, all wired):
// path-prefix / package-manager detectors (brew → … → uv) →
// go (path prefix + debug/buildinfo via gobin) → system → unknown.
// First non-nil Match wins; unknown is always last and never returns nil.
type Runner struct {
	detectors []Detector
}

// NewRunner builds a chain from the given detectors in order.
func NewRunner(detectors ...Detector) *Runner {
	return &Runner{detectors: detectors}
}

// DefaultRunner returns the full Phase-1 detector chain (Tier-1 complete).
func DefaultRunner() *Runner {
	return NewRunner(
		NewBrewDetector(),
		NewMacPortsDetector(),
		NewCargoDetector(),
		NewRustupDetector(),
		NewNpmDetector(),
		NewPnpmDetector(),
		NewYarnDetector(),
		NewBunDetector(),
		NewPipUserDetector(),
		NewMiseDetector(),
		NewAsdfDetector(),
		NewGemDetector(),
		NewNixDetector(),
		NewVoltaDetector(),
		NewDenoDetector(),
		NewDotnetDetector(),
		NewComposerDetector(),
		NewKrewDetector(),
		NewCurlInstallerDetector(),
		NewPipxDetector(),
		NewUVDetector(),
		NewGoDetector(), // includes buildinfo via gobin.go (unmodified vendor)
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
