package cmd

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rtwsvj/hukou/internal/manifest"
)

// fakeGitHubServerWithAPIDigest serves a release whose single asset carries
// GitHub API digest metadata but NO checksum asset. digest is the raw API
// value ("sha256:<hex>"); an empty digest omits the field entirely.
func fakeGitHubServerWithAPIDigest(t *testing.T, assetName string, assetData []byte, digest, tag string) *httptest.Server {
	t.Helper()
	if tag == "" {
		tag = "v2.0.0"
	}
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/releases/latest") || strings.HasSuffix(r.URL.Path, "/releases") {
			asset := map[string]any{
				"name":                 assetName,
				"browser_download_url": server.URL + "/assets/" + assetName,
				"size":                 len(assetData),
			}
			if digest != "" {
				asset["digest"] = digest
			}
			writeFakeReleaseMetadata(t, w, r, map[string]any{
				"tag_name": tag,
				"assets":   []map[string]any{asset},
			})
			return
		}
		switch r.URL.Path {
		case "/assets/" + assetName:
			_, _ = w.Write(assetData)
		default:
			http.NotFound(w, r)
		}
	}))
	return server
}

// adoptFakeBin registers binPath as owner/repo at v1.0.0 so doUpgrade can run.
func adoptFakeBin(t *testing.T, out *bytes.Buffer, binPath string) {
	t.Helper()
	if err := doAdopt(out, out, binPath, "owner/repo", false, "v1.0.0", false, ""); err != nil {
		t.Fatal(err)
	}
}

func loadEntry(t *testing.T, dataDir, name string) manifest.Entry {
	t.Helper()
	m, err := manifest.Load(filepath.Join(dataDir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	e := m.Get(name)
	if e == nil {
		t.Fatalf("manifest has no %s entry", name)
	}
	return *e
}

// TestE2E_APIDigestVerifiesWithoutChecksumAsset covers the new evidence
// source: a release that publishes no checksums.txt/sidecar but does publish
// a matching sha256 asset digest in its API metadata must install as
// checksum-verified, recording checksum_asset="(api-digest)" in the manifest.
func TestE2E_APIDigestVerifiesWithoutChecksumAsset(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("HUKOU_DATA_DIR", dataDir)
	binPath := writeExecutable(t, t.TempDir(), "fakebin", "v1-body\n")
	var out bytes.Buffer
	adoptFakeBin(t, &out, binPath)

	assetName := platformAssetName("fakebin")
	assetData := makeTarGz(t, "fakebin", []byte("v2-body\n"))
	apiDigest := "sha256:" + fmt.Sprintf("%x", sha256.Sum256(assetData))
	server := fakeGitHubServerWithAPIDigest(t, assetName, assetData, apiDigest, "v2.0.0")
	defer server.Close()

	out.Reset()
	// No --allow-unverified: api-digest evidence alone must suffice.
	if err := doUpgrade(&out, &out, []string{"fakebin"}, false, false, "", testGHClient(server), false); err != nil {
		t.Fatalf("api-digest verified upgrade: %v\n%s", err, out.String())
	}
	got, err := os.ReadFile(binPath)
	if err != nil || string(got) != "v2-body\n" {
		t.Fatalf("live after api-digest upgrade: body=%q err=%v", got, err)
	}
	e := loadEntry(t, dataDir, "fakebin")
	if !e.ChecksumVerified {
		t.Fatal("checksum_verified must be true on api-digest match")
	}
	if e.ChecksumAsset != "(api-digest)" {
		t.Fatalf("checksum_asset = %q, want (api-digest)", e.ChecksumAsset)
	}
	wantSHA := fmt.Sprintf("%x", sha256.Sum256(assetData))
	if e.AssetSHA256 != wantSHA || e.AssetName != assetName {
		t.Fatalf("asset provenance not recorded: %+v", e)
	}
	combined := out.String()
	if !strings.Contains(combined, "(api-digest)") {
		t.Fatalf("output must disclose api-digest provenance:\n%s", combined)
	}
	manifestRaw, err := os.ReadFile(filepath.Join(dataDir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(manifestRaw), `"checksum_asset": "(api-digest)"`) {
		t.Fatalf("manifest JSON must record the api-digest marker:\n%s", manifestRaw)
	}
}

// TestE2E_APIDigestMismatchRefusedEvenWithAllowUnverified is the stricter-
// than-before rule: when API digest evidence contradicts the downloaded
// asset, installation is refused outright — --allow-unverified only covers
// absent evidence, never contradictory evidence.
func TestE2E_APIDigestMismatchRefusedEvenWithAllowUnverified(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("HUKOU_DATA_DIR", dataDir)
	binPath := writeExecutable(t, t.TempDir(), "fakebin", "v1-body\n")
	var out bytes.Buffer
	adoptFakeBin(t, &out, binPath)

	assetName := platformAssetName("fakebin")
	assetData := makeTarGz(t, "fakebin", []byte("v2-body\n"))
	wrongDigest := "sha256:" + strings.Repeat("b", 64)
	server := fakeGitHubServerWithAPIDigest(t, assetName, assetData, wrongDigest, "v2.0.0")
	defer server.Close()

	for _, allowUnverified := range []bool{false, true} {
		out.Reset()
		err := doUpgrade(&out, &out, []string{"fakebin"}, false, false, "", testGHClient(server), allowUnverified)
		if err == nil {
			t.Fatalf("allowUnverified=%v: contradictory evidence must refuse install\n%s", allowUnverified, out.String())
		}
		if !strings.Contains(err.Error(), strings.Repeat("b", 64)) {
			t.Fatalf("allowUnverified=%v: error = %v, must name the published digest", allowUnverified, err)
		}
		if !strings.Contains(err.Error(), "digest mismatch") {
			t.Fatalf("allowUnverified=%v: error = %v, want digest mismatch", allowUnverified, err)
		}
		before, readErr := os.ReadFile(binPath)
		if readErr != nil || string(before) != "v1-body\n" {
			t.Fatalf("allowUnverified=%v: live binary changed after refusal: body=%q err=%v", allowUnverified, before, readErr)
		}
	}
	if e := loadEntry(t, dataDir, "fakebin"); e.Tag != "v1.0.0" || e.ChecksumVerified {
		t.Fatalf("manifest must stay untouched after refusals: %+v", e)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "store", "fakebin", "v2.0.0")); !os.IsNotExist(err) {
		t.Fatalf("store must not keep the contradicted version: %v", err)
	}
}

// TestE2E_NoEvidenceKeepsLegacyUnverifiedFlow pins the third state: neither
// checksum asset nor API digest behaves exactly as before — fail closed by
// default, UNVERIFIED bypass with empty checksum_asset under
// --allow-unverified.
func TestE2E_NoEvidenceKeepsLegacyUnverifiedFlow(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("HUKOU_DATA_DIR", dataDir)
	binPath := writeExecutable(t, t.TempDir(), "fakebin", "v1-body\n")
	var out bytes.Buffer
	adoptFakeBin(t, &out, binPath)

	assetName := platformAssetName("fakebin")
	assetData := makeTarGz(t, "fakebin", []byte("v2-body\n"))
	server := fakeGitHubServerWithAPIDigest(t, assetName, assetData, "", "v2.0.0")
	defer server.Close()

	out.Reset()
	err := doUpgrade(&out, &out, []string{"fakebin"}, false, false, "", testGHClient(server), false)
	if err == nil {
		t.Fatalf("expected fail-closed refusal, got success:\n%s", out.String())
	}
	if !strings.Contains(err.Error(), "no publisher checksum asset") {
		t.Fatalf("error = %v, want no publisher checksum asset", err)
	}

	out.Reset()
	if err := doUpgrade(&out, &out, []string{"fakebin"}, false, false, "", testGHClient(server), true); err != nil {
		t.Fatalf("allow-unverified upgrade: %v\n%s", err, out.String())
	}
	if got, err := os.ReadFile(binPath); err != nil || string(got) != "v2-body\n" {
		t.Fatalf("live after bypass: body=%q err=%v", got, err)
	}
	e := loadEntry(t, dataDir, "fakebin")
	if e.Tag != "v2.0.0" || e.ChecksumVerified {
		t.Fatalf("manifest after bypass: %+v", e)
	}
	if e.ChecksumAsset != "" {
		t.Fatalf("checksum_asset must stay empty when no evidence exists, got %q", e.ChecksumAsset)
	}
	if e.AssetSHA256 == "" {
		t.Fatal("asset_sha256 must still be recorded on bypass")
	}
}
