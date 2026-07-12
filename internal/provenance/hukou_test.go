package provenance

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rtwsvj/hukou/internal/scan"
)

func TestHukouDetector(t *testing.T) {
	dir := t.TempDir()
	mpath := filepath.Join(dir, "manifest.json")
	body := `{"schema_version":1,"entries":[{"name":"longbridge","path":"/opt/homebrew/bin/longbridge","repo":"longbridge/longbridge-terminal","tag":"adopted","sha256":"x","upstream":"","adopted_at":"2026-07-12T00:00:00Z","updated_at":"2026-07-12T00:00:00Z"}]}`
	if err := os.WriteFile(mpath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	d := NewHukouDetector()
	if err := d.Load(Env{HukouManifest: mpath}); err != nil {
		t.Fatalf("Load: %v", err)
	}

	got := d.Match(scan.Binary{Name: "longbridge", Path: "/opt/homebrew/bin/longbridge", RealPath: "/opt/homebrew/bin/longbridge"})
	if got == nil || got.Source != "hukou" || got.Package != "longbridge" || got.Confidence != "exact" {
		t.Fatalf("expected hukou attribution, got %+v", got)
	}

	if d.Match(scan.Binary{Name: "other", Path: "/usr/bin/other", RealPath: "/usr/bin/other"}) != nil {
		t.Fatal("unrelated binary must not match")
	}
}

func TestHukouDetectorMissingManifest(t *testing.T) {
	d := NewHukouDetector()
	if err := d.Load(Env{HukouManifest: filepath.Join(t.TempDir(), "none.json")}); err != nil {
		t.Fatalf("missing manifest must not error (empty registry): %v", err)
	}
	if d.Match(scan.Binary{Path: "/opt/homebrew/bin/longbridge"}) != nil {
		t.Fatal("expected no match with empty registry")
	}
}
