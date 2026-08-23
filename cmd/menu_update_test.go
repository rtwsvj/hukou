package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rtwsvj/hukou/internal/manifest"
)

// TestMenuQuickUpdateAction_UpgradesAdoptedTools drives the menu action end
// to end against a fake GitHub server: adopt a v1 tool, point
// HUKOU_GITHUB_BASE_URL at the fake, run the action, and assert the v2 tag
// landed in both the live binary and the manifest — proving the action runs
// the real upgrade engine (checksum asset verified, store committed).
func TestMenuQuickUpdateAction_UpgradesAdoptedTools(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("HUKOU_DATA_DIR", dataDir)
	binPath := writeExecutable(t, t.TempDir(), "fakebin", "v1-body\n")
	var out bytes.Buffer
	if err := doAdopt(&out, &out, binPath, "owner/repo", false, "v1.0.0", false, ""); err != nil {
		t.Fatal(err)
	}

	assetName := platformAssetName("fakebin")
	assetData := makeTarGz(t, "fakebin", []byte("v2-body\n"))
	server := fakeGitHubServer(t, assetName, assetData, assetChecksumLine(assetName, assetData), "v2.0.0")
	defer server.Close()
	// httptest serves plain http on 127.0.0.1, which ValidateBaseURL accepts.
	t.Setenv("HUKOU_GITHUB_BASE_URL", server.URL)

	out.Reset()
	var errb bytes.Buffer
	if err := menuQuickUpdateAction(&out, &errb); err != nil {
		t.Fatalf("menu quick update must swallow errors into stderr, got: %v\nstdout:\n%s\nstderr:\n%s", err, out.String(), errb.String())
	}
	got, err := os.ReadFile(binPath)
	if err != nil || string(got) != "v2-body\n" {
		t.Fatalf("live binary after quick update: body=%q err=%v", got, err)
	}
	m, err := manifest.Load(filepath.Join(dataDir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	e := m.Get("fakebin")
	if e == nil || e.Tag != "v2.0.0" {
		t.Fatalf("manifest after quick update: %+v", e)
	}
	if !e.ChecksumVerified {
		t.Fatal("publisher checksum must be verified by the shared engine")
	}
}

// TestMenuQuickUpdateAction_NoAdoptedTools asserts the zero-tool case prints
// the same line `hukou upgrade` prints and returns nil without any network
// activity.
func TestMenuQuickUpdateAction_NoAdoptedTools(t *testing.T) {
	t.Setenv("HUKOU_DATA_DIR", t.TempDir())

	var out, errb bytes.Buffer
	if err := menuQuickUpdateAction(&out, &errb); err != nil {
		t.Fatalf("zero adopted tools must not be an error, got: %v", err)
	}
	if !strings.Contains(out.String(), "No adopted tools") {
		t.Fatalf("stdout must carry the CLI's no-tools line, got:\n%s", out.String())
	}
	if errb.Len() != 0 {
		t.Fatalf("stderr must stay clean, got:\n%s", errb.String())
	}
}

// TestMenuQuickUpdateAction_InvalidBaseURLFailsClosed asserts an unusable
// HUKOU_GITHUB_BASE_URL surfaces as a printed error and a nil return — never
// a panic, and never a silent fallback to github.com.
func TestMenuQuickUpdateAction_InvalidBaseURLFailsClosed(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("HUKOU_DATA_DIR", dataDir)
	binPath := writeExecutable(t, t.TempDir(), "fakebin", "v1-body\n")
	var out bytes.Buffer
	if err := doAdopt(&out, &out, binPath, "owner/repo", false, "v1.0.0", false, ""); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HUKOU_GITHUB_BASE_URL", "http://mirror.example.com")

	before, err := os.ReadFile(binPath)
	if err != nil {
		t.Fatal(err)
	}
	out.Reset()
	var errb bytes.Buffer
	if err := menuQuickUpdateAction(&out, &errb); err != nil {
		t.Fatalf("menu action must keep the loop alive (nil return), got: %v", err)
	}
	if !strings.Contains(errb.String(), "GitHub base URL") {
		t.Fatalf("stderr must name the offending setting fail-closed style, got:\n%s", errb.String())
	}
	after, err := os.ReadFile(binPath)
	if err != nil || !bytes.Equal(after, before) {
		t.Fatalf("live binary must be untouched after client construction failure: body=%q err=%v", after, err)
	}
	m, err := manifest.Load(filepath.Join(dataDir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if e := m.Get("fakebin"); e == nil || e.Tag != "v1.0.0" {
		t.Fatalf("manifest must be untouched after failure: %+v", e)
	}
}
