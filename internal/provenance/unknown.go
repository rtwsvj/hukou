package provenance

import "github.com/rtwsvj/hukou/internal/scan"

// UnknownDetector is the fallback: always matches.
type UnknownDetector struct{}

// NewUnknownDetector creates the unknown fallback detector.
func NewUnknownDetector() *UnknownDetector {
	return &UnknownDetector{}
}

func (d *UnknownDetector) Name() string { return "unknown" }

func (d *UnknownDetector) Load(env Env) error { return nil }

func (d *UnknownDetector) Match(b scan.Binary) *Attribution {
	evidence := "no prior detector matched"
	if b.RealPath != "" {
		evidence = "no prior detector matched; realpath=" + b.RealPath
	} else if b.Path != "" {
		evidence = "no prior detector matched; path=" + b.Path
	}
	return &Attribution{
		Source:     "unknown",
		Package:    b.Name,
		Confidence: "inferred",
		Evidence:   evidence,
	}
}
