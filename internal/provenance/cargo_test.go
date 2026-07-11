package provenance

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rtwsvj/hukou/internal/scan"
)

func TestCargoLoad_malformedJSON(t *testing.T) {
	root := t.TempDir()
	cargoHome := filepath.Join(root, ".cargo")
	if err := os.MkdirAll(filepath.Join(cargoHome, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cargoHome, ".crates2.json"), []byte("NOT JSON{{{"), 0o644); err != nil {
		t.Fatal(err)
	}

	d := NewCargoDetector()
	if err := d.Load(Env{CargoHome: cargoHome}); err != nil {
		t.Fatalf("Load must succeed on malformed JSON: %v", err)
	}
	// path-prefix inferred fallback
	a := d.Match(scan.Binary{
		Name: "rg", Path: filepath.Join(cargoHome, "bin", "rg"),
		RealPath: filepath.Join(cargoHome, "bin", "rg"),
	})
	if a == nil || a.Source != "cargo" || a.Confidence != "inferred" {
		t.Fatalf("want cargo inferred fallback, got %+v", a)
	}
	if a.Package != "rg" {
		t.Fatalf("package=%q want rg (bin name fallback)", a.Package)
	}
}

func TestCargoLoad_missingCrates2(t *testing.T) {
	root := t.TempDir()
	cargoHome := filepath.Join(root, ".cargo")
	if err := os.MkdirAll(filepath.Join(cargoHome, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	// no .crates2.json

	d := NewCargoDetector()
	if err := d.Load(Env{CargoHome: cargoHome}); err != nil {
		t.Fatalf("Load must succeed when .crates2.json missing: %v", err)
	}
	a := d.Match(scan.Binary{
		Name: "fd", Path: filepath.Join(cargoHome, "bin", "fd"),
		RealPath: filepath.Join(cargoHome, "bin", "fd"),
	})
	if a == nil || a.Source != "cargo" || a.Confidence != "inferred" {
		t.Fatalf("want cargo inferred fallback, got %+v", a)
	}
}

func TestCargoLoad_exactWhenValid(t *testing.T) {
	root := t.TempDir()
	cargoHome := filepath.Join(root, ".cargo")
	if err := os.MkdirAll(filepath.Join(cargoHome, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	crates := `{"installs":{"ripgrep 14.1.1 (registry+https://github.com/rust-lang/crates.io-index)":{"bins":["rg"]}}}`
	if err := os.WriteFile(filepath.Join(cargoHome, ".crates2.json"), []byte(crates), 0o644); err != nil {
		t.Fatal(err)
	}
	d := NewCargoDetector()
	if err := d.Load(Env{CargoHome: cargoHome}); err != nil {
		t.Fatal(err)
	}
	a := d.Match(scan.Binary{
		Name: "rg", Path: filepath.Join(cargoHome, "bin", "rg"),
		RealPath: filepath.Join(cargoHome, "bin", "rg"),
	})
	if a == nil || a.Package != "ripgrep" || a.Version != "14.1.1" || a.Confidence != "exact" {
		t.Fatalf("want exact ripgrep, got %+v", a)
	}
}
