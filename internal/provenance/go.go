package provenance

import (
	"path/filepath"

	"github.com/rtwsvj/hukou/internal/scan"
)

type GoDetector struct {
	bin string
}

func NewGoDetector() *GoDetector { return &GoDetector{} }

func (d *GoDetector) Name() string { return "go" }

func (d *GoDetector) Load(env Env) error {
	d.bin = GoBinDir(env.GoBin, env.GoPath, env.Home)
	return nil
}

func (d *GoDetector) Match(b scan.Binary) *Attribution {
	if d.bin != "" {
		for _, p := range binaryPaths(b) {
			if pathInDir(p, d.bin) {
				if a := d.fromBuildInfo(b, p); a != nil {
					a.Evidence = "go buildinfo; path prefix " + filepath.Clean(d.bin)
					return a
				}
				return &Attribution{
					Source:     "go",
					Package:    b.Name,
					Confidence: "inferred",
					Evidence:   "path prefix " + filepath.Clean(d.bin),
				}
			}
		}
	}
	for _, p := range binaryPaths(b) {
		if a := d.fromBuildInfo(b, p); a != nil {
			return a
		}
	}
	return nil
}

func (d *GoDetector) fromBuildInfo(b scan.Binary, path string) *Attribution {
	info, ok := ReadGoBinary(path)
	if !ok {
		return nil
	}
	pkg := info.ImportPath
	if pkg == "" {
		pkg = b.Name
	}
	return &Attribution{
		Source:     "go",
		Package:    pkg,
		Version:    info.Version,
		Upstream:   info.ModulePath,
		Confidence: "exact",
		Evidence:   "go buildinfo module " + info.ModulePath,
	}
}
