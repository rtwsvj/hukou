package cmd

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rtwsvj/hukou/internal/manifest"
)

const menuStatusFixtureTime = "2026-08-23T00:00:00Z"

// menuStatusFixture describes a data root whose manifest adopts two tools and
// whose store retains versions for both. tool-b's live file is overwritten
// with different bytes after adoption, so it reports DRIFTED while tool-a
// stays ok.
type menuStatusFixture struct {
	root      string
	liveOK    string
	liveDrift string
}

func newMenuStatusFixture(t *testing.T) menuStatusFixture {
	t.Helper()
	root := t.TempDir()
	fix := menuStatusFixture{
		root:      root,
		liveOK:    filepath.Join(root, "bin", "tool-a"),
		liveDrift: filepath.Join(root, "bin", "tool-b"),
	}

	writeMenuStatusBinary(t, fix.liveOK, "tool-a body v1\n")
	okSHA := hashMenuStatusBytes(t, "tool-a body v1\n")
	writeMenuStatusBinary(t, fix.liveDrift, "tool-b original\n")
	driftSHA := hashMenuStatusBytes(t, "tool-b original\n")

	m := &manifest.Manifest{
		SchemaVersion: manifest.CurrentSchemaVersion,
		Retention:     manifest.DefaultRetentionPolicy(),
	}
	m.Put(manifest.PrepareEntry(manifest.Entry{
		Name:      "tool-a",
		Path:      fix.liveOK,
		Tag:       "v1",
		SHA256:    okSHA,
		AdoptedAt: menuStatusFixtureTime,
		UpdatedAt: menuStatusFixtureTime,
	}))
	m.Put(manifest.PrepareEntry(manifest.Entry{
		Name:      "tool-b",
		Path:      fix.liveDrift,
		Tag:       "v2",
		SHA256:    driftSHA,
		AdoptedAt: menuStatusFixtureTime,
		UpdatedAt: menuStatusFixtureTime,
	}))
	if err := m.Save(filepath.Join(root, "manifest.json")); err != nil {
		t.Fatal(err)
	}

	// Store topology required by store.Versions: <store>/<name>/<tag>/ holds
	// exactly one regular file per retained version.
	writeMenuStatusVersion(t, root, "tool-a", "v1")
	writeMenuStatusVersion(t, root, "tool-b", "v1")
	writeMenuStatusVersion(t, root, "tool-b", "v2")

	// Drift tool-b only after the manifest records its SHA-256.
	writeMenuStatusBinary(t, fix.liveDrift, "tool-b tampered by an external updater\n")
	return fix
}

func writeMenuStatusBinary(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeMenuStatusVersion(t *testing.T, root, name, tag string) {
	t.Helper()
	dir := filepath.Join(root, "store", name, tag)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(name+" "+tag+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func hashMenuStatusBytes(t *testing.T, body string) string {
	t.Helper()
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}

func TestMenuStatusRendersTableAndSummary(t *testing.T) {
	fix := newMenuStatusFixture(t)
	var out bytes.Buffer
	if err := runMenuStatus(&out, fix.root); err != nil {
		t.Fatalf("runMenuStatus = %v\n%s", err, out.String())
	}

	text := out.String()
	for _, want := range []string{
		"NAME", "TAG", "DRIFT", "VERSIONS",
		"tool-a", "v1", "ok",
		"tool-b", "v2", "DRIFTED",
		"2 adopted tool(s): 1 up to date, 1 drifted",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("status output missing %q:\n%s", want, text)
		}
	}
	// The drifted marker must sit on tool-b's row, not tool-a's.
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "tool-a") && strings.Contains(line, "DRIFT") {
			t.Fatalf("healthy tool-a row wrongly marked drifted: %q", line)
		}
	}
}

func TestMenuStatusVersionsCountRetainedStoreEntries(t *testing.T) {
	fix := newMenuStatusFixture(t)
	var out bytes.Buffer
	if err := runMenuStatus(&out, fix.root); err != nil {
		t.Fatalf("runMenuStatus = %v\n%s", err, out.String())
	}
	var okRow, driftRow string
	for _, line := range strings.Split(out.String(), "\n") {
		switch {
		case strings.HasPrefix(line, "tool-a"):
			okRow = line
		case strings.HasPrefix(line, "tool-b"):
			driftRow = line
		}
	}
	// tool-a retains one version; tool-b retains two (v1 kept by retention,
	// v2 active).
	if !strings.HasSuffix(strings.TrimRight(okRow, " "), "1") {
		t.Fatalf("tool-a row should end with VERSIONS=1: %q", okRow)
	}
	if !strings.HasSuffix(strings.TrimRight(driftRow, " "), "2") {
		t.Fatalf("tool-b row should end with VERSIONS=2: %q", driftRow)
	}
}

func TestMenuStatusEmptyManifestPrintsNoToolsLine(t *testing.T) {
	root := t.TempDir()
	m := &manifest.Manifest{
		SchemaVersion: manifest.CurrentSchemaVersion,
		Retention:     manifest.DefaultRetentionPolicy(),
	}
	if err := m.Save(filepath.Join(root, "manifest.json")); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runMenuStatus(&out, root); err != nil {
		t.Fatalf("runMenuStatus = %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "No tools have been adopted.") {
		t.Fatalf("missing no-tools line:\n%s", out.String())
	}
}

func TestMenuStatusMissingManifestIsEmptyState(t *testing.T) {
	root := filepath.Join(t.TempDir(), "absent")
	var out bytes.Buffer
	if err := runMenuStatus(&out, root); err != nil {
		t.Fatalf("runMenuStatus = %v, want nil for a missing manifest\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "No tools have been adopted.") {
		t.Fatalf("a missing manifest should read as the empty state:\n%s", out.String())
	}
}

func TestMenuStatusManifestLoadErrorKeepsMenuAlive(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "manifest.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runMenuStatus(&out, root); err != nil {
		t.Fatalf("runMenuStatus = %v, want nil so the menu keeps running\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "error:") {
		t.Fatalf("a manifest load failure must print a localized error line:\n%s", out.String())
	}
}

func TestMenuStatusActionUsesDataRoot(t *testing.T) {
	fix := newMenuStatusFixture(t)
	t.Setenv("HUKOU_DATA_DIR", fix.root)
	var out, errBuf bytes.Buffer
	if err := menuStatusAction(&out, &errBuf); err != nil {
		t.Fatalf("menuStatusAction = %v\n%s", err, out.String())
	}
	if errBuf.Len() != 0 {
		t.Fatalf("action polluted stderr: %q", errBuf.String())
	}
	if !strings.Contains(out.String(), "tool-a") || !strings.Contains(out.String(), "DRIFTED") {
		t.Fatalf("action output missing table rows:\n%s", out.String())
	}
}
