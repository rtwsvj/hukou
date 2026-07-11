package provenance

import (
	"path/filepath"

	"github.com/rtwsvj/hukou/internal/scan"
)

type NixDetector struct {
	store string
}

func NewNixDetector() *NixDetector { return &NixDetector{} }

func (d *NixDetector) Name() string { return "nix" }

func (d *NixDetector) Load(env Env) error {
	d.store = env.NixStore
	return nil
}

func (d *NixDetector) Match(b scan.Binary) *Attribution {
	if d.store == "" {
		return nil
	}
	for _, p := range binaryPaths(b) {
		rel, ok := pathRelUnder(p, d.store)
		if !ok {
			continue
		}
		parts := pathParts(rel)
		if len(parts) == 0 {
			continue
		}
		entry := parts[0]
		if dash := stringsIndexByte(entry, '-'); dash >= 0 && dash+1 < len(entry) {
			entry = entry[dash+1:]
		}
		pkg, ver := splitNameVersion(entry)
		return &Attribution{
			Source:     "nix",
			Package:    sourcePackageFallback(pkg, b.Name),
			Version:    ver,
			Confidence: "exact",
			Evidence:   "realpath under " + filepath.Join(d.store, parts[0]),
		}
	}
	return nil
}

func stringsIndexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}
