package cmd

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rtwsvj/hukou/internal/ghrelease"
	"github.com/rtwsvj/hukou/internal/manifest"
	statejournal "github.com/rtwsvj/hukou/internal/transaction"
)

// TestUpgradeRejectsStoreArtifactTamperedAfterStorePut drives the real
// upgrade flow end to end (adopt, fake GitHub release server, doUpgrade) and
// tampers with the immutable store artifact inside the exact window the
// digest-threading change stopped re-hashing: after store.PutWithDigest
// committed the new version and
// returned its digest, before the transaction journal captures the activation
// source. Injection is deterministic via upgradeTestHookAfterStoreNewVersion,
// which fires at the top of that window; nothing re-reads the artifact until
// journal capture, so the tamper covers the whole window. The upgrade as a
// whole must fail (journal capture independently re-hashes the artifact and
// the pre-Apply digest check refuses the mismatch), and neither the live
// binary nor the manifest may change.
func TestUpgradeRejectsStoreArtifactTamperedAfterStorePut(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("HUKOU_DATA_DIR", dataDir)

	binDir := t.TempDir()
	binPath := writeExecutable(t, binDir, "fakebin", "v1.0.0-body\n")

	var out bytes.Buffer
	if err := doAdopt(&out, &out, binPath, "owner/repo", false, "v1.0.0", false, ""); err != nil {
		t.Fatalf("adopt: %v\n%s", err, out.String())
	}
	manifestPath := filepath.Join(dataDir, "manifest.json")
	manifestBefore, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest before upgrade: %v", err)
	}

	assetName := platformAssetName("fakebin")
	assetData := makeTarGz(t, "fakebin", []byte("v2.0.0-body\n"))
	checksum := fmt.Sprintf("%x  %s\n", sha256.Sum256(assetData), assetName)
	server := fakeGitHubServer(t, assetName, assetData, checksum, "v2.0.0")
	defer server.Close()
	client := &ghrelease.Client{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
		Sleep:      func(time.Duration) {},
	}

	tampered := false
	upgradeTestHookAfterStoreNewVersion = func(name, tag string) {
		artifact := filepath.Join(dataDir, "store", name, tag, name)
		if err := os.WriteFile(artifact, []byte("tampered-payload\n"), 0o755); err != nil {
			t.Errorf("tamper store artifact %s: %v", artifact, err)
			return
		}
		tampered = true
	}
	t.Cleanup(func() { upgradeTestHookAfterStoreNewVersion = nil })

	out.Reset()
	err = doUpgrade(&out, &out, []string{"fakebin"}, false, false, "", client, false)
	if err == nil {
		t.Fatalf("upgrade with tampered store artifact succeeded:\n%s", out.String())
	}
	if !tampered {
		t.Fatal("tamper hook did not run")
	}
	if !strings.Contains(err.Error(), "SHA-256 mismatch") {
		t.Fatalf("upgrade error = %v, want a SHA-256 mismatch rejection", err)
	}

	// The live binary was never touched.
	got, readErr := os.ReadFile(binPath)
	if readErr != nil || string(got) != "v1.0.0-body\n" {
		t.Fatalf("live binary disturbed: content=%q err=%v", got, readErr)
	}
	// The manifest is byte-for-byte unchanged and still records v1.0.0.
	manifestAfter, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest after failed upgrade: %v", err)
	}
	if !bytes.Equal(manifestBefore, manifestAfter) {
		t.Fatalf("manifest changed by a rejected upgrade:\nbefore=%s\nafter=%s", manifestBefore, manifestAfter)
	}
	m, err := manifest.Load(manifestPath)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	if e := m.Get("fakebin"); e == nil || e.Tag != "v1.0.0" {
		t.Fatalf("manifest entry after rejected upgrade = %+v", e)
	}
	// The aborted transaction left no pending journal state behind.
	if cleanErr := statejournal.CheckClean(dataDir); cleanErr != nil {
		t.Fatalf("transaction state not clean after rejected upgrade: %v", cleanErr)
	}
}
