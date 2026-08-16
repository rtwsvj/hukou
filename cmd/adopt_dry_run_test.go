package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rtwsvj/hukou/internal/manifest"
	"github.com/rtwsvj/hukou/internal/output"
	"github.com/rtwsvj/hukou/internal/provenance"
	"github.com/rtwsvj/hukou/internal/store"
)

func TestAdoptRecordsAdoptionAnchor(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("HUKOU_DATA_DIR", dataDir)
	live := writeExecutable(t, t.TempDir(), "anchored", "v1\n")
	var stdout, stderr bytes.Buffer
	if err := doAdopt(&stdout, &stderr, live, "owner/repo", false, "v1.0.0", false, ""); err != nil {
		t.Fatal(err)
	}
	m, err := manifest.Load(filepath.Join(dataDir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	e := m.Get("anchored")
	if e.AdoptedSHA256 == "" || e.AdoptedSHA256 != e.SHA256 {
		t.Fatalf("adoption anchor not recorded: sha=%s anchor=%s", e.SHA256, e.AdoptedSHA256)
	}
}

func TestAdoptRejectsWhitespaceNameWithoutStoreResidue(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("HUKOU_DATA_DIR", dataDir)
	live := writeExecutable(t, t.TempDir(), "   ", "v1\n")
	var stdout, stderr bytes.Buffer
	err := doAdopt(&stdout, &stderr, live, "", true, "", false, "")
	if err == nil || !strings.Contains(err.Error(), "whitespace only") {
		t.Fatalf("expected whitespace-name rejection, got %v", err)
	}
	storeDir := filepath.Join(dataDir, "store")
	entries, readErr := os.ReadDir(storeDir)
	if readErr != nil {
		if os.IsNotExist(readErr) {
			return // zero store residue is the success case
		}
		t.Fatal(readErr)
	}
	for _, e := range entries {
		if strings.TrimSpace(e.Name()) == "" {
			t.Fatalf("rejected adopt left store residue: %q", e.Name())
		}
	}
}

func TestAdoptRejectsExeWithLocal(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "missing-data-root")
	t.Setenv("HUKOU_DATA_DIR", dataDir)
	live := writeExecutable(t, t.TempDir(), "exe-local", "v1\n")
	gate := func(string) (*provenance.Attribution, error) {
		return &provenance.Attribution{Source: "unknown", Confidence: "exact", Evidence: "fixture"}, nil
	}
	var stdout bytes.Buffer
	err := doAdoptDryRun(&stdout, live, "", true, "local", false, "inner", false, gate)
	if err == nil || !strings.Contains(err.Error(), "--exe requires a release repository") {
		t.Fatalf("expected --exe/--local rejection, got %v", err)
	}
}

func TestAdoptRejectsInvalidExe(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "missing-data-root")
	t.Setenv("HUKOU_DATA_DIR", dataDir)
	live := writeExecutable(t, t.TempDir(), "bad-exe", "v1\n")
	gate := func(string) (*provenance.Attribution, error) {
		return &provenance.Attribution{Source: "unknown", Confidence: "exact", Evidence: "fixture"}, nil
	}
	var stdout bytes.Buffer
	err := doAdoptDryRun(&stdout, live, "owner/repo", false, "v1", false, "dir/name", false, gate)
	if err == nil || !strings.Contains(err.Error(), "invalid --exe") {
		t.Fatalf("expected --exe component rejection, got %v", err)
	}
}

func TestAdoptRejectsReservedOriginalNameWithoutStoreResidue(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("HUKOU_DATA_DIR", dataDir)
	live := writeExecutable(t, t.TempDir(), "original", "v1\n")
	var stdout, stderr bytes.Buffer
	err := doAdopt(&stdout, &stderr, live, "", true, "", false, "")
	if err == nil || !strings.Contains(err.Error(), "reserved immutable backup namespace") {
		t.Fatalf("expected reserved-name rejection, got %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("rejected adopt wrote stdout: %s", stdout.String())
	}
	storeDir := filepath.Join(dataDir, "store")
	entries, readErr := os.ReadDir(storeDir)
	if readErr != nil {
		if os.IsNotExist(readErr) {
			return // zero store residue is the success case
		}
		t.Fatal(readErr)
	}
	for _, e := range entries {
		if strings.EqualFold(e.Name(), "original") {
			t.Fatalf("rejected adopt left store residue: %s", e.Name())
		}
	}
}

func TestAdoptDryRunIsStrictlyZeroWrite(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "missing-data-root")
	t.Setenv("HUKOU_DATA_DIR", dataDir)
	live := writeExecutable(t, t.TempDir(), "dry-adopt", "v1\n")
	gate := func(string) (*provenance.Attribution, error) {
		return &provenance.Attribution{Source: "unknown", Confidence: "inferred", Evidence: "test"}, nil
	}
	var stdout bytes.Buffer
	if err := doAdoptDryRun(&stdout, live, "owner/repo", false, "v1", false, "", false, gate); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "Would adopt dry-adopt") {
		t.Fatalf("unexpected output: %s", stdout.String())
	}
	if _, err := os.Lstat(dataDir); !os.IsNotExist(err) {
		t.Fatalf("dry-run created data root: %v", err)
	}
}

func TestAdoptDryRunJSON(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "missing-data-root")
	t.Setenv("HUKOU_DATA_DIR", dataDir)
	live := writeExecutable(t, t.TempDir(), "dry-json", "v1\n")
	gate := func(string) (*provenance.Attribution, error) {
		return &provenance.Attribution{Source: "unknown", Confidence: "exact", Evidence: "fixture"}, nil
	}
	var stdout bytes.Buffer
	if err := doAdoptDryRun(&stdout, live, "owner/repo", false, "v1", false, "", true, gate); err != nil {
		t.Fatal(err)
	}
	var plan output.AdoptPlan
	if err := json.Unmarshal(stdout.Bytes(), &plan); err != nil {
		t.Fatalf("decode plan: %v\n%s", err, stdout.String())
	}
	if plan.SchemaVersion != 1 || plan.Name != "dry-json" || plan.Repo != "owner/repo" || len(plan.PlannedWrites) != 6 {
		t.Fatalf("unexpected plan: %+v", plan)
	}
	if !bytes.Contains(stdout.Bytes(), []byte(`"source"`)) || bytes.Contains(stdout.Bytes(), []byte(`"Source"`)) {
		t.Fatalf("attribution fields are not stable snake_case JSON: %s", stdout.String())
	}
	if _, err := os.Lstat(dataDir); !os.IsNotExist(err) {
		t.Fatalf("dry-run created data root: %v", err)
	}
}

func TestAdoptDryRunRejectsInvalidRepositoryWithoutWriting(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "missing-data-root")
	t.Setenv("HUKOU_DATA_DIR", dataDir)
	live := writeExecutable(t, t.TempDir(), "bad-repo", "v1\n")
	gate := func(string) (*provenance.Attribution, error) {
		return &provenance.Attribution{Source: "unknown", Confidence: "exact", Evidence: "fixture"}, nil
	}
	var stdout bytes.Buffer
	err := doAdoptDryRun(&stdout, live, "owner/repo/extra", false, "v1", false, "", false, gate)
	if err == nil || !strings.Contains(err.Error(), "invalid repository") {
		t.Fatalf("expected repository rejection, got %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("rejected dry-run wrote output: %s", stdout.String())
	}
	if _, statErr := os.Lstat(dataDir); !os.IsNotExist(statErr) {
		t.Fatalf("rejected dry-run created data root: %v", statErr)
	}
}

func TestAdoptDryRunRejectsExistingOrHostileStoreTopologyWithoutWriting(t *testing.T) {
	gate := func(string) (*provenance.Attribution, error) {
		return &provenance.Attribution{Source: "unknown", Confidence: "exact", Evidence: "fixture"}, nil
	}

	t.Run("existing original", func(t *testing.T) {
		dataDir := t.TempDir()
		t.Setenv("HUKOU_DATA_DIR", dataDir)
		live := writeExecutable(t, t.TempDir(), "occupied", "live\n")
		original := filepath.Join(dataDir, "store", "occupied", "original", "occupied")
		if err := os.MkdirAll(filepath.Dir(original), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(original, []byte("existing\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		var stdout bytes.Buffer
		err := doAdoptDryRun(&stdout, live, "owner/repo", false, "v1", false, "", false, gate)
		if err == nil || !strings.Contains(err.Error(), "original backup namespace") {
			t.Fatalf("expected original conflict, got %v", err)
		}
		if got, readErr := os.ReadFile(original); readErr != nil || string(got) != "existing\n" {
			t.Fatalf("dry-run changed existing original: %q err=%v", got, readErr)
		}
	})

	t.Run("symlinked tool namespace", func(t *testing.T) {
		dataDir := t.TempDir()
		t.Setenv("HUKOU_DATA_DIR", dataDir)
		live := writeExecutable(t, t.TempDir(), "redirected", "live\n")
		outside := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dataDir, "store"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(dataDir, "store", "redirected")); err != nil {
			t.Fatal(err)
		}
		var stdout bytes.Buffer
		err := doAdoptDryRun(&stdout, live, "owner/repo", false, "v1", false, "", false, gate)
		if err == nil || !strings.Contains(err.Error(), "must not be a symlink") {
			t.Fatalf("expected hostile topology rejection, got %v", err)
		}
		entries, readErr := os.ReadDir(outside)
		if readErr != nil || len(entries) != 0 {
			t.Fatalf("dry-run wrote through hostile symlink: entries=%v err=%v", entries, readErr)
		}
	})
}

func TestAdoptDryRunStillEnforcesOwnershipGate(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "missing-data-root")
	t.Setenv("HUKOU_DATA_DIR", dataDir)
	live := writeExecutable(t, t.TempDir(), "brew-owned", "v1\n")
	gate := func(string) (*provenance.Attribution, error) {
		return &provenance.Attribution{Source: "brew", Confidence: "exact", Evidence: "Cellar"}, nil
	}
	var stdout bytes.Buffer
	err := doAdoptDryRun(&stdout, live, "owner/repo", false, "v1", false, "", false, gate)
	if err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("expected ownership rejection, got %v", err)
	}
	if _, statErr := os.Lstat(dataDir); !os.IsNotExist(statErr) {
		t.Fatalf("rejected dry-run created data root: %v", statErr)
	}
}

func TestRealAdoptReinspectsAfterDryRun(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("HUKOU_DATA_DIR", dataDir)
	live := writeExecutable(t, t.TempDir(), "reinspect", "before\n")
	gate := func(string) (*provenance.Attribution, error) {
		return &provenance.Attribution{Source: "unknown", Confidence: "exact", Evidence: "fixture"}, nil
	}
	var outputBuffer bytes.Buffer
	if err := doAdoptDryRun(&outputBuffer, live, "", true, "local", false, "", false, gate); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(live, []byte("after\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := doAdoptWithDeps(&outputBuffer, &outputBuffer, live, "", true, "local", false, "", gate, saveManifest); err != nil {
		t.Fatal(err)
	}
	m, err := manifest.Load(filepath.Join(dataDir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	wantSHA, err := store.SHA256File(live)
	if err != nil {
		t.Fatal(err)
	}
	if entry := m.Get("reinspect"); entry == nil || entry.SHA256 != wantSHA {
		t.Fatalf("real adopt reused stale dry-run state: %+v", entry)
	}
}
