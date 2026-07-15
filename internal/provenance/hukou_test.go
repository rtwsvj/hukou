package provenance

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rtwsvj/hukou/internal/manifest"
	"github.com/rtwsvj/hukou/internal/scan"
)

func newLoadedHukouDetector(t *testing.T, entry manifest.Entry) *HukouDetector {
	t.Helper()
	if entry.AdoptedAt == "" {
		entry.AdoptedAt = "2026-07-14T00:00:00Z"
	}
	if entry.UpdatedAt == "" {
		entry.UpdatedAt = entry.AdoptedAt
	}
	mpath := filepath.Join(t.TempDir(), "manifest.json")
	m := &manifest.Manifest{SchemaVersion: 1, Entries: []manifest.Entry{entry}}
	if err := m.Save(mpath); err != nil {
		t.Fatal(err)
	}
	d := NewHukouDetector()
	if err := d.Load(Env{HukouManifest: mpath}); err != nil {
		t.Fatalf("Load: %v", err)
	}
	return d
}

func testSHA256(content []byte) string {
	sum := sha256.Sum256(content)
	return fmt.Sprintf("%x", sum)
}

func TestHukouDetectorExactSHAAndUpstream(t *testing.T) {
	content := []byte("managed binary")
	binPath := filepath.Join(t.TempDir(), "longbridge")
	if err := os.WriteFile(binPath, content, 0o755); err != nil {
		t.Fatal(err)
	}
	d := newLoadedHukouDetector(t, manifest.Entry{
		Name:     "longbridge",
		Path:     binPath,
		Repo:     "longbridge/longbridge-terminal",
		Upstream: "github.com/longbridge/longbridge-terminal/cmd/longbridge",
		Tag:      "v1.0.0",
		SHA256:   testSHA256(content),
	})

	got := d.Match(scan.Binary{Name: "longbridge", Path: binPath, RealPath: binPath})
	if got == nil || got.Source != "hukou" || got.Package != "longbridge" || got.Confidence != "exact" {
		t.Fatalf("expected hukou attribution, got %+v", got)
	}
	if got.Upstream != "github.com/longbridge/longbridge-terminal/cmd/longbridge" {
		t.Fatalf("Upstream=%q", got.Upstream)
	}
	if !strings.Contains(got.Evidence, "sha256 verified") {
		t.Fatalf("Evidence=%q", got.Evidence)
	}

	if d.Match(scan.Binary{Name: "other", Path: "/usr/bin/other", RealPath: "/usr/bin/other"}) != nil {
		t.Fatal("unrelated binary must not match")
	}
}

func TestHukouDetectorSHA256Drift(t *testing.T) {
	content := []byte("externally replaced")
	binPath := filepath.Join(t.TempDir(), "tool")
	if err := os.WriteFile(binPath, content, 0o755); err != nil {
		t.Fatal(err)
	}
	d := newLoadedHukouDetector(t, manifest.Entry{
		Name:   "tool",
		Path:   binPath,
		Repo:   "owner/repo",
		Tag:    "v1.0.0",
		SHA256: strings.Repeat("0", 64),
	})

	got := d.Match(scan.Binary{Name: "tool", Path: binPath, RealPath: binPath})
	if got == nil || got.Source != "hukou" || got.Confidence != "inferred" {
		t.Fatalf("expected inferred hukou attribution, got %+v", got)
	}
	if !strings.Contains(got.Evidence, "sha256 mismatch") {
		t.Fatalf("Evidence=%q", got.Evidence)
	}
}

func TestHukouDetectorUpstreamFallsBackToRepo(t *testing.T) {
	content := []byte("managed binary")
	binPath := filepath.Join(t.TempDir(), "tool")
	if err := os.WriteFile(binPath, content, 0o755); err != nil {
		t.Fatal(err)
	}
	d := newLoadedHukouDetector(t, manifest.Entry{
		Name:   "tool",
		Path:   binPath,
		Repo:   "owner/repo",
		Tag:    "v1.0.0",
		SHA256: testSHA256(content),
	})

	got := d.Match(scan.Binary{Name: "tool", Path: binPath, RealPath: binPath})
	if got == nil || got.Upstream != "owner/repo" {
		t.Fatalf("expected repo fallback, got %+v", got)
	}
}

func TestHukouDetectorUnreadableSHA256(t *testing.T) {
	binPath := filepath.Join(t.TempDir(), "missing-tool")
	d := newLoadedHukouDetector(t, manifest.Entry{
		Name:   "missing-tool",
		Path:   binPath,
		Repo:   "owner/repo",
		Tag:    "v1.0.0",
		SHA256: strings.Repeat("a", 64),
	})

	got := d.Match(scan.Binary{Name: "missing-tool", Path: binPath, RealPath: binPath})
	if got == nil || got.Source != "hukou" || got.Confidence != "inferred" {
		t.Fatalf("expected inferred hukou attribution, got %+v", got)
	}
	if !strings.Contains(got.Evidence, "sha256 unreadable") {
		t.Fatalf("Evidence=%q", got.Evidence)
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

func TestHukouDetectorRejectsPendingTransactionState(t *testing.T) {
	root := t.TempDir()
	pending := filepath.Join(root, "transactions", "pending-test")
	if err := os.MkdirAll(pending, 0o700); err != nil {
		t.Fatal(err)
	}
	d := NewHukouDetector()
	err := d.Load(Env{HukouManifest: filepath.Join(root, "manifest.json")})
	if err == nil || !strings.Contains(err.Error(), "unfinished transaction") {
		t.Fatalf("expected pending transaction error, got %v", err)
	}
	if d.Match(scan.Binary{Path: "/usr/local/bin/tool"}) != nil {
		t.Fatal("pending state must not produce hukou attribution")
	}
}
