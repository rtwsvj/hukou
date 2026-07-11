package provenance

import (
	"path/filepath"

	"github.com/rtwsvj/hukou/internal/scan"
)

type CurlInstallerDetector struct {
	roots []CurlInstallerRoot
}

func NewCurlInstallerDetector() *CurlInstallerDetector { return &CurlInstallerDetector{} }

func (d *CurlInstallerDetector) Name() string { return "curl-installer" }

func (d *CurlInstallerDetector) Load(env Env) error {
	d.roots = env.CurlInstallerRoots
	return nil
}

func (d *CurlInstallerDetector) Match(b scan.Binary) *Attribution {
	for _, root := range d.roots {
		if root.Dir == "" {
			continue
		}
		for _, p := range binaryPaths(b) {
			if _, ok := pathRelUnder(p, root.Dir); ok || pathInDir(p, root.Dir) {
				pkg := root.Package
				if pkg == "" {
					pkg = b.Name
				}
				return &Attribution{Source: "curl-installer", Package: pkg, Confidence: "inferred", Evidence: "known installer directory " + filepath.Clean(root.Dir)}
			}
		}
	}
	return nil
}
