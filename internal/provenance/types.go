package provenance

import "github.com/rtwsvj/hukou/internal/scan"

// Attribution is the install-source verdict for a binary.
type Attribution struct {
	Source     string // "brew" | "cargo" | "go" | ... | "system" | "unknown"
	Package    string // package / formula / module name
	Version    string // filled when cheaply available
	Upstream   string // filled when derivable (e.g. go module path)
	Confidence string // "exact" | "inferred"
	Evidence   string // human-readable basis
}

// Detector attributes binaries to an install source.
// Implementations must not call os.Getenv; use Env only.
type Detector interface {
	Name() string
	Load(env Env) error          // preload manifests once
	Match(b scan.Binary) *Attribution // nil if cannot attribute
}
