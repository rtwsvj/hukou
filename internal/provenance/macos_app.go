package provenance

import (
	"path/filepath"
	"strings"

	"github.com/rtwsvj/hukou/internal/scan"
)

// MacOSAppDetector attributes binaries living inside .app bundles under
// /Applications or ~/Applications.
type MacOSAppDetector struct {
	appsDirs []string
}

func NewMacOSAppDetector() *MacOSAppDetector { return &MacOSAppDetector{} }

func (d *MacOSAppDetector) Name() string { return "macos-app" }

func (d *MacOSAppDetector) Load(env Env) error {
	d.appsDirs = uniquePaths(env.ApplicationsDirs)
	return nil
}

func (d *MacOSAppDetector) Match(b scan.Binary) *Attribution {
	for _, p := range binaryPaths(b) {
		for _, appsDir := range d.appsDirs {
			if appsDir == "" {
				continue
			}
			rel, ok := pathRelUnder(p, appsDir)
			if !ok {
				continue
			}
			parts := pathParts(rel)
			if len(parts) < 1 {
				continue
			}
			// First component must be <App>.app
			if !strings.HasSuffix(parts[0], ".app") {
				continue
			}
			appBundle := parts[0]
			pkg := strings.TrimSuffix(appBundle, ".app")
			if pkg == "" {
				continue
			}
			appPath := filepath.Join(appsDir, appBundle)
			return &Attribution{
				Source:     "macos-app",
				Package:    pkg,
				Confidence: "exact",
				Evidence:   "realpath under " + appPath,
			}
		}
	}
	return nil
}
