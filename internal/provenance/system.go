package provenance

import (
	"path/filepath"
	"runtime"
	"strings"

	"github.com/rtwsvj/hukou/internal/scan"
)

// NewSystemDetector creates a system-path detector. On darwin the prefix set
// additionally covers /System, /Library/Apple, and the Xcode Command Line
// Tools; on other GOOSes only the portable Unix core prefixes are registered,
// so macOS-only inputs never enter the chain.
type SystemDetector struct {
	prefixes []string
	goos     string // test hook: defaults to runtime.GOOS when empty
}

func NewSystemDetector() *SystemDetector {
	return &SystemDetector{}
}

func (d *SystemDetector) Name() string { return "system" }

func (d *SystemDetector) Load(env Env) error {
	goos := d.goos
	if goos == "" {
		goos = runtime.GOOS
	}
	if goos == "windows" {
		// Windows has neither the Unix core prefixes nor the Apple roots;
		// keep the detector inert until a Windows prefix model exists.
		d.prefixes = nil
		return nil
	}
	// Portable Unix core prefixes (darwin, linux, and the other Unix-likes).
	d.prefixes = []string{
		"/bin",
		"/sbin",
		"/usr/bin",
		"/usr/sbin",
		"/usr/libexec",
	}
	if goos == "darwin" {
		d.prefixes = append(d.prefixes,
			"/System",
			"/Library/Apple",
		)
		if env.XcodeCLT != "" {
			d.prefixes = append(d.prefixes, env.XcodeCLT)
		} else {
			d.prefixes = append(d.prefixes, "/Library/Developer/CommandLineTools")
		}
	}
	return nil
}

func (d *SystemDetector) Match(b scan.Binary) *Attribution {
	goos := d.goos
	if goos == "" {
		goos = runtime.GOOS
	}
	candidates := []string{b.RealPath, b.Path}

	for _, p := range candidates {
		if p == "" {
			continue
		}
		// Darwin only: /System/Volumes/* is user data (Data volume), not OS
		// system files.
		if goos == "darwin" && pathHasPrefix(p, "/System/Volumes") {
			continue
		}
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
