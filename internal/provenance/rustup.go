package provenance

import (
	"path/filepath"

	"github.com/rtwsvj/hukou/internal/scan"
)

type RustupDetector struct {
	toolchains string
	cargoBin   string
}

func NewRustupDetector() *RustupDetector { return &RustupDetector{} }

func (d *RustupDetector) Name() string { return "rustup" }

func (d *RustupDetector) Load(env Env) error {
	if env.RustupHome != "" {
		d.toolchains = filepath.Join(env.RustupHome, "toolchains")
	}
	if env.CargoHome != "" {
		d.cargoBin = filepath.Join(env.CargoHome, "bin")
	}
	return nil
}

func (d *RustupDetector) Match(b scan.Binary) *Attribution {
	if d.toolchains != "" {
		for _, p := range binaryPaths(b) {
			rel, ok := pathRelUnder(p, d.toolchains)
			if !ok {
				continue
			}
			parts := pathParts(rel)
			if len(parts) >= 3 && parts[1] == "bin" {
				return &Attribution{
					Source:     "rustup",
					Package:    b.Name,
					Version:    parts[0],
					Confidence: "exact",
					Evidence:   "realpath under " + filepath.Join(d.toolchains, parts[0], "bin"),
				}
			}
		}
	}
	if d.cargoBin != "" && isRustupProxy(b.Name) {
		for _, p := range binaryPaths(b) {
			if pathInDir(p, d.cargoBin) {
				return &Attribution{
					Source:     "rustup",
					Package:    b.Name,
					Confidence: "inferred",
					Evidence:   "rustup proxy in " + d.cargoBin,
				}
			}
		}
	}
	return nil
}

func isRustupProxy(name string) bool {
	switch name {
	case "rustup", "rustc", "cargo", "rustdoc", "rustfmt", "cargo-fmt", "clippy-driver", "cargo-clippy", "rust-lldb", "rust-gdb", "rust-gdbgui", "rls", "rust-analyzer", "miri", "cargo-miri":
		return true
	default:
		return false
	}
}
