package provenance

import (
	"path/filepath"

	"github.com/rtwsvj/hukou/internal/scan"
)

// LocalProjectDetector attributes binaries under <ProjectsDir>/<name>/.
type LocalProjectDetector struct {
	projectsDir string
}

func NewLocalProjectDetector() *LocalProjectDetector { return &LocalProjectDetector{} }

func (d *LocalProjectDetector) Name() string { return "local-project" }

func (d *LocalProjectDetector) Load(env Env) error {
	d.projectsDir = env.ProjectsDir
	return nil
}

func (d *LocalProjectDetector) Match(b scan.Binary) *Attribution {
	if d.projectsDir == "" {
		return nil
	}
	for _, p := range binaryPaths(b) {
		rel, ok := pathRelUnder(p, d.projectsDir)
		if !ok {
			continue
		}
		parts := pathParts(rel)
		if len(parts) < 1 {
			continue
		}
		pkg := parts[0]
		return &Attribution{
			Source:     "local-project",
			Package:    pkg,
			Confidence: "inferred",
			Evidence:   "realpath under " + filepath.Join(d.projectsDir, pkg),
		}
	}
	return nil
}
