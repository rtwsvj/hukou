package cmd

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rtwsvj/hukou/internal/manifest"
	"github.com/rtwsvj/hukou/internal/verify"
)

func TestResolvePublisherChecksumMatch(t *testing.T) {
	assetName := "tool.tar.gz"
	payload := []byte("release-body")
	digest := fmt.Sprintf("%x", sha256.Sum256(payload))
	checksums := map[string]string{assetName: digest}

	verified, err := resolvePublisherChecksum(digest, assetName, "checksums.txt", checksums, false)
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if !verified {
		t.Fatal("expected verified=true on digest match")
	}
}

func TestResolvePublisherChecksumMismatch(t *testing.T) {
	assetName := "tool.tar.gz"
	got := strings.Repeat("a", 64)
	want := strings.Repeat("b", 64)
	checksums := map[string]string{assetName: want}

	verified, err := resolvePublisherChecksum(got, assetName, "checksums.txt", checksums, false)
	if err == nil {
		t.Fatal("expected mismatch error")
	}
	if verified {
		t.Fatal("mismatch must not report verified")
	}
	if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("error = %v, want checksum mismatch", err)
	}
	// --allow-unverified must not bypass a present-but-mismatched publisher digest.
	if _, err := resolvePublisherChecksum(got, assetName, "checksums.txt", checksums, true); err == nil {
		t.Fatal("allow-unverified must not bypass checksum mismatch")
	}
}

func TestResolvePublisherChecksumMissingEntry(t *testing.T) {
	assetName := "tool.tar.gz"
	digest := strings.Repeat("a", 64)
	checksums := map[string]string{"other.tar.gz": digest}

	verified, err := resolvePublisherChecksum(digest, assetName, "checksums.txt", checksums, false)
	if err == nil {
		t.Fatal("expected missing-entry error")
	}
	if verified {
		t.Fatal("missing entry must not report verified")
	}
	if !strings.Contains(err.Error(), "verify checksum from checksums.txt") {
		t.Fatalf("error = %v, want wrapped verify failure", err)
	}
	if !strings.Contains(err.Error(), verify.ErrNoChecksum.Error()) {
		t.Fatalf("error = %v, want ErrNoChecksum class", err)
	}
	// Present checksum asset with a missing selected-entry is still fail-closed.
	if _, err := resolvePublisherChecksum(digest, assetName, "checksums.txt", checksums, true); err == nil {
		t.Fatal("allow-unverified must not bypass missing entry in a present checksum asset")
	}
}

func TestResolvePublisherChecksumSourceMissingFailClosed(t *testing.T) {
	assetName := "tool.tar.gz"
	digest := strings.Repeat("c", 64)

	verified, err := resolvePublisherChecksum(digest, assetName, "", nil, false)
	if err == nil {
		t.Fatal("expected fail-closed error when no checksum asset")
	}
	if verified {
		t.Fatal("unverified path must not report verified")
	}
	if !strings.Contains(err.Error(), "no publisher checksum asset") {
		t.Fatalf("error = %v, want no publisher checksum asset", err)
	}
	if !strings.Contains(err.Error(), "--allow-unverified") {
		t.Fatalf("error = %v, want explicit bypass hint", err)
	}
}

func TestResolvePublisherChecksumSourceMissingAllowUnverified(t *testing.T) {
	assetName := "tool.tar.gz"
	digest := strings.Repeat("d", 64)

	verified, err := resolvePublisherChecksum(digest, assetName, "", nil, true)
	if err != nil {
		t.Fatalf("allow-unverified: %v", err)
	}
	if verified {
		t.Fatal("bypass must keep checksum_verified=false")
	}
}

// TestE2E_MissingChecksumAssetFailClosedAndBypass covers the end-to-end
// three-state policy for a release that publishes no checksum asset at all:
// refuse by default, and install only with --allow-unverified while recording
// asset_sha256 and checksum_verified=false in the manifest audit fields.
func TestE2E_MissingChecksumAssetFailClosedAndBypass(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("HUKOU_DATA_DIR", dataDir)
	binPath := writeExecutable(t, t.TempDir(), "fakebin", "v1-body\n")
	var out bytes.Buffer
	if err := doAdopt(&out, &out, binPath, "owner/repo", false, "v1.0.0", false, ""); err != nil {
		t.Fatal(err)
	}

	assetName := platformAssetName("fakebin")
	assetData := makeTarGz(t, "fakebin", []byte("v2-body\n"))
	// Empty checksum argument omits checksums.txt from the release entirely.
	server := fakeGitHubServer(t, assetName, assetData, "", "v2.0.0")
	defer server.Close()
	client := testGHClient(server)

	before, err := os.ReadFile(binPath)
	if err != nil {
		t.Fatal(err)
	}
	out.Reset()
	err = doUpgrade(&out, &out, []string{"fakebin"}, false, false, "", client, false)
	if err == nil {
		t.Fatalf("expected fail-closed refusal, got success:\n%s", out.String())
	}
	if !strings.Contains(err.Error(), "no publisher checksum asset") {
		t.Fatalf("error = %v, want no publisher checksum asset", err)
	}
	after, err := os.ReadFile(binPath)
	if err != nil || !bytes.Equal(after, before) {
		t.Fatalf("live binary changed after refusal: body=%q err=%v", after, err)
	}
	m, err := manifest.Load(filepath.Join(dataDir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if e := m.Get("fakebin"); e == nil || e.Tag != "v1.0.0" || e.ChecksumVerified {
		t.Fatalf("manifest after refusal: %+v", e)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "store", "fakebin", "v2.0.0")); !os.IsNotExist(err) {
		t.Fatalf("store must not keep unverified version without bypass: %v", err)
	}

	out.Reset()
	if err := doUpgrade(&out, &out, []string{"fakebin"}, false, false, "", client, true); err != nil {
		t.Fatalf("allow-unverified upgrade: %v\n%s", err, out.String())
	}
	combined := out.String()
	if !strings.Contains(combined, "UNVERIFIED") {
		t.Fatalf("bypass output must mark UNVERIFIED:\n%s", combined)
	}
	got, err := os.ReadFile(binPath)
	if err != nil || string(got) != "v2-body\n" {
		t.Fatalf("live after bypass: body=%q err=%v", got, err)
	}
	m, err = manifest.Load(filepath.Join(dataDir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	e := m.Get("fakebin")
	if e == nil || e.Tag != "v2.0.0" {
		t.Fatalf("manifest after bypass: %+v", e)
	}
	if e.ChecksumVerified {
		t.Fatal("checksum_verified must stay false on bypass")
	}
	if e.ChecksumAsset != "" {
		t.Fatalf("checksum_asset must be empty on missing-source bypass, got %q", e.ChecksumAsset)
	}
	if e.AssetSHA256 == "" || e.AssetName != assetName {
		t.Fatalf("asset provenance not recorded on bypass: %+v", e)
	}
	wantAssetSHA := fmt.Sprintf("%x", sha256.Sum256(assetData))
	if e.AssetSHA256 != wantAssetSHA {
		t.Fatalf("asset_sha256 = %s, want %s", e.AssetSHA256, wantAssetSHA)
	}
	// Assert the serialized wire form, not only the decoded Go zero value.
	// omitempty on bool would drop false, so the on-disk file would lack any
	// UNVERIFIED audit marker even though e.ChecksumVerified == false.
	manifestRaw, err := os.ReadFile(filepath.Join(dataDir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(manifestRaw), `"checksum_verified": false`) {
		t.Fatalf("bypass manifest JSON must contain checksum_verified false (not omitempty-swallowed):\n%s", manifestRaw)
	}
}
