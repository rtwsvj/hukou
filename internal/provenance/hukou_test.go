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
	statejournal "github.com/rtwsvj/hukou/internal/transaction"
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

// makeVerifiedCompletedResidue runs a REAL transaction lifecycle against root
// (Begin, Apply, Commit) and forces Finalize's directory removal to fail by
// revoking write permission on the journal directory. This is exactly the
// residue a crash between COMMIT and cleanup leaves behind: a verified
// completed-* journal with a COMMIT marker matching the id.
func makeVerifiedCompletedResidue(t *testing.T, root string) {
	t.Helper()
	target := filepath.Join(t.TempDir(), "mutated")
	if err := os.WriteFile(target, []byte("before"), 0o644); err != nil {
		t.Fatal(err)
	}
	tx, err := statejournal.Begin(root, "upgrade", "tool", []statejournal.Spec{{
		Role: "live", Path: target, After: statejournal.RegularBytes([]byte("after"), 0o644),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Apply("live"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	status, err := statejournal.Inspect(root)
	if err != nil || len(status.Pending) != 1 {
		t.Fatalf("expected one pending journal: status=%+v err=%v", status, err)
	}
	journalDir := filepath.Join(root, "transactions", status.Pending[0])
	if err := os.Chmod(journalDir, 0o500); err != nil {
		t.Fatal(err)
	}
	if err := tx.Finalize(); err == nil {
		t.Fatal("expected Finalize cleanup to fail")
	}
	status, err = statejournal.Inspect(root)
	if err != nil || len(status.Completed) != 1 {
		t.Fatalf("expected completed residue: status=%+v err=%v", status, err)
	}
	completed := filepath.Join(root, "transactions", status.Completed[0])
	t.Cleanup(func() { _ = os.Chmod(completed, 0o700) })
}

// saveAdoptedEntry writes a one-entry manifest into root for binPath and
// returns the manifest path.
func saveAdoptedEntry(t *testing.T, root, name, binPath string, content []byte) string {
	t.Helper()
	mpath := filepath.Join(root, "manifest.json")
	m := &manifest.Manifest{SchemaVersion: 1, Entries: []manifest.Entry{{
		Name:      name,
		Path:      binPath,
		Repo:      "owner/repo",
		Tag:       "v1.0.0",
		SHA256:    testSHA256(content),
		AdoptedAt: "2026-07-14T00:00:00Z",
		UpdatedAt: "2026-07-14T00:00:00Z",
	}}}
	if err := m.Save(mpath); err != nil {
		t.Fatal(err)
	}
	return mpath
}

func TestHukouDetectorRejectsPendingTransactionState(t *testing.T) {
	// A REAL published-but-unresolved transaction: Begin leaves a pending-*
	// journal until Commit+Finalize or Abort runs. Live state may be
	// mid-flight, so the detector must degrade: Load fails, the runner drops
	// it, and adopted binaries fall through to later detectors.
	root := t.TempDir()
	target := filepath.Join(t.TempDir(), "tool")
	if err := os.WriteFile(target, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := statejournal.Begin(root, "upgrade", "tool", []statejournal.Spec{{
		Role: "live", Path: target, After: statejournal.RegularBytes([]byte("y"), 0o755),
	}}); err != nil {
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
	if len(d.Notes()) != 0 {
		t.Fatalf("degraded load must not emit advisory notes, got %v", d.Notes())
	}
}

// Card A rework: ONLY a verified completed-* journal (real lifecycle, cleanup
// forced to fail) leaves adopted binaries consistent with the manifest; the
// detector keeps attributing and records a non-fatal advisory note.
func TestHukouDetectorToleratesVerifiedCompletedResidue(t *testing.T) {
	root := t.TempDir()
	makeVerifiedCompletedResidue(t, root)
	content := []byte("managed binary")
	binPath := filepath.Join(root, "longbridge")
	if err := os.WriteFile(binPath, content, 0o755); err != nil {
		t.Fatal(err)
	}
	mpath := saveAdoptedEntry(t, root, "longbridge", binPath, content)

	d := NewHukouDetector()
	if err := d.Load(Env{HukouManifest: mpath}); err != nil {
		t.Fatalf("verified completed residue must not fail hukou load: %v", err)
	}
	got := d.Match(scan.Binary{Name: "longbridge", Path: binPath, RealPath: binPath})
	if got == nil || got.Source != "hukou" || got.Confidence != "exact" {
		t.Fatalf("adopted binary must stay attributed, got %+v", got)
	}
	notes := d.Notes()
	if len(notes) != 1 || !strings.Contains(notes[0], "stale journal residue") {
		t.Fatalf("expected one stale-residue note, got %v", notes)
	}
}

// Card A rework: unknown residue is adversarial input (hand-crafted name
// forgery is the legitimate way to test it) and must degrade the detector,
// not produce a note.
func TestHukouDetectorDegradesOnUnknownResidue(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "transactions", "leftover-junk"), 0o700); err != nil {
		t.Fatal(err)
	}
	d := NewHukouDetector()
	err := d.Load(Env{HukouManifest: filepath.Join(root, "manifest.json")})
	if err == nil || !strings.Contains(err.Error(), "unfinished transaction") {
		t.Fatalf("expected fail-closed load error, got %v", err)
	}
	if len(d.Notes()) != 0 {
		t.Fatalf("degraded load must not emit advisory notes, got %v", d.Notes())
	}
}

// Card A rework: the runner surfaces a still-loaded detector's advisories on
// the dedicated NOTES channel — never as warnings — without dropping the
// detector from the chain. Security gates key on warnings, so this separation
// is what keeps a routine advisory from vetoing adopt.
func TestRunnerSurfacesDetectorNotes(t *testing.T) {
	root := t.TempDir()
	makeVerifiedCompletedResidue(t, root)
	content := []byte("managed binary")
	binPath := filepath.Join(root, "tool")
	if err := os.WriteFile(binPath, content, 0o755); err != nil {
		t.Fatal(err)
	}
	mpath := saveAdoptedEntry(t, root, "tool", binPath, content)

	r := NewRunner(NewHukouDetector())
	warnings, notes := r.Load(Env{HukouManifest: mpath})
	if len(warnings) != 0 {
		t.Fatalf("notes must not leak into the warnings channel, got %v", warnings)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "detector hukou: stale journal residue") {
		t.Fatalf("expected hukou residue note, got %v", notes)
	}
	if len(r.Detectors()) != 1 {
		t.Fatalf("detector must remain in the chain, got %d", len(r.Detectors()))
	}
	if got := r.Match(scan.Binary{Name: "tool", Path: binPath, RealPath: binPath}); got == nil || got.Source != "hukou" {
		t.Fatalf("adopted binary must stay attributed, got %+v", got)
	}
}
