package provenance

import (
	"path/filepath"
	"strings"

	"github.com/rtwsvj/hukou/internal/scan"
)

// SystemDetector attributes OS / Xcode Command Line Tools binaries.
type SystemDetector struct {
	prefixes []string
}

// NewSystemDetector creates a system-path detector.
func NewSystemDetector() *SystemDetector {
	return &SystemDetector{}
}

func (d *SystemDetector) Name() string { return "system" }

func (d *SystemDetector) Load(env Env) error {
	// Fixed system prefixes + Xcode CLT from Env.
	d.prefixes = []string{
		"/bin",
		"/sbin",
		"/usr/bin",
		"/usr/sbin",
		"/usr/libexec",
		"/System",
		"/Library/Apple",
	}
	if env.XcodeCLT != "" {
		d.prefixes = append(d.prefixes, env.XcodeCLT)
	} else {
		d.prefixes = append(d.prefixes, "/Library/Developer/CommandLineTools")
	}
	return nil
}

func (d *SystemDetector) Match(b scan.Binary) *Attribution {
	candidates := []string{b.RealPath, b.Path}
	for _, p := range candidates {
		if p == "" {
			continue
		}
		// Clean for stable prefix checks.
		p = filepath.Clean(p)
		for _, prefix := range d.prefixes {
			if pathHasPrefix(p, prefix) {
				return &Attribution{
					Source:     "system",
					Package:    b.Name,
					Confidence: "exact",
					Evidence:   "path prefix " + prefix,
				}
			}
		}
	}
	return nil
}

// pathHasPrefix reports whether path is prefix or a path under prefix.
// Handles /System matching /System/Library/... and exact /bin etc.
func pathHasPrefix(path, prefix string) bool {
	prefix = filepath.Clean(prefix)
	if path == prefix {
		return true
	}
	// Ensure boundary: /usr/bin matches /usr/bin/ls, not /usr/binary.
	if strings.HasPrefix(path, prefix+string(filepath.Separator)) {
		return true
	}
	return false
}
