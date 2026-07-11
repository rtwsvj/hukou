package provenance

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/rtwsvj/hukou/internal/scan"
)

type CargoDetector struct {
	home   string
	bin    string
	crates map[string]cargoCrate
}

type cargoCrate struct {
	Package  string
	Version  string
	Upstream string
}

type crates2File struct {
	Installs map[string]struct {
		Bins []string `json:"bins"`
	} `json:"installs"`
}

func NewCargoDetector() *CargoDetector { return &CargoDetector{} }

func (d *CargoDetector) Name() string { return "cargo" }

func (d *CargoDetector) Load(env Env) error {
	d.home = env.CargoHome
	d.crates = make(map[string]cargoCrate)
	if d.home == "" {
		return nil
	}
	d.bin = filepath.Join(d.home, "bin")
	data, err := os.ReadFile(filepath.Join(d.home, ".crates2.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var file crates2File
	if err := json.Unmarshal(data, &file); err != nil {
		return err
	}
	for key, install := range file.Installs {
		crate := parseCargoCrateKey(key)
		for _, bin := range install.Bins {
			if bin != "" {
				d.crates[bin] = crate
			}
		}
	}
	return nil
}

func (d *CargoDetector) Match(b scan.Binary) *Attribution {
	if d.bin == "" {
		return nil
	}
	inCargoBin := false
	for _, p := range binaryPaths(b) {
		if pathInDir(p, d.bin) {
			inCargoBin = true
			break
		}
	}
	if !inCargoBin {
		return nil
	}
	if crate, ok := d.crates[b.Name]; ok && crate.Package != "" {
		return &Attribution{
			Source:     "cargo",
			Package:    crate.Package,
			Version:    crate.Version,
			Upstream:   crate.Upstream,
			Confidence: "exact",
			Evidence:   "~/.cargo/.crates2.json bin " + b.Name,
		}
	}
	if isRustupProxy(b.Name) {
		return nil
	}
	return &Attribution{
		Source:     "cargo",
		Package:    b.Name,
		Confidence: "inferred",
		Evidence:   "path prefix " + d.bin,
	}
}

func parseCargoCrateKey(key string) cargoCrate {
	out := cargoCrate{}
	left := key
	if open := strings.LastIndex(key, "("); open >= 0 && strings.HasSuffix(key, ")") {
		left = strings.TrimSpace(key[:open])
		out.Upstream = strings.TrimSpace(key[open+1 : len(key)-1])
	}
	fields := strings.Fields(left)
	if len(fields) > 0 {
		out.Package = fields[0]
	}
	if len(fields) > 1 {
		out.Version = fields[1]
	}
	return out
}
