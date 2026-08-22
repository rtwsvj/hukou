package cmd

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rtwsvj/hukou/internal/ghrelease"
	"github.com/rtwsvj/hukou/internal/manifest"
	statejournal "github.com/rtwsvj/hukou/internal/transaction"
)

// unknownTypeDirEntry is an os.DirEntry whose Type() reports zero type bits,
// the shape os.ReadDir produces on filesystems that answer readdir with
// DT_UNKNOWN. The real file type is only discoverable via Lstat.
type unknownTypeDirEntry struct {
	name string
}

func (e unknownTypeDirEntry) Name() string               { return e.name }
func (e unknownTypeDirEntry) IsDir() bool                { return false }
func (e unknownTypeDirEntry) Type() fs.FileMode          { return 0 }
func (e unknownTypeDirEntry) Info() (fs.FileInfo, error) { return nil, fs.ErrNotExist }

// TestDirEntryIsRegularIgnoresDirentTypeBits: the backup-namespace guards
// must confirm regularity with Lstat, not DirEntry.Type(), because
// DT_UNKNOWN filesystems yield zero type bits and would make every check
// spuriously fail (permanent upgrade/adoption refusal). A regular file with
// unknown dirent type must pass; a symlink must still be rejected because
// Lstat does not follow it; a vanished entry must fail closed.
func TestDirEntryIsRegularIgnoresDirentTypeBits(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "tool")
	if err := os.WriteFile(target, []byte("payload\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}

	if !dirEntryIsRegular(dir, unknownTypeDirEntry{name: "tool"}) {
		t.Fatal("regular file with DT_UNKNOWN-shaped DirEntry must be accepted")
	}
	if dirEntryIsRegular(dir, unknownTypeDirEntry{name: "link"}) {
		t.Fatal("symlink must be rejected even with unknown dirent type bits")
	}
	if dirEntryIsRegular(dir, unknownTypeDirEntry{name: "missing"}) {
		t.Fatal("missing entry must be rejected")
	}

	// The helper must agree with real os.ReadDir entries on an ordinary
	// filesystem, where both the dirent bits and Lstat say "regular".
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() == "tool" && !dirEntryIsRegular(dir, e) {
			t.Fatal("real DirEntry for a regular file must be accepted")
		}
		if e.Name() == "link" && dirEntryIsRegular(dir, e) {
			t.Fatal("real DirEntry for a symlink must be rejected")
		}
	}
}

// TestUpgradeRejectsAssetSwappedBetweenVerifyAndExtract drives the real
// upgrade flow end to end (adopt, fake GitHub release server, doUpgrade) and
// swaps the downloaded asset with different-but-valid archive bytes inside
// the exact verify→extract window: after publisher-checksum verification,
// before archive.Extract opens the file (injected via
// upgradeTestHookBeforeExtract). The post-extract re-hash must refuse the
// swap, the tampered payload must never reach the store, and neither the
// live binary nor the manifest may change.
func TestUpgradeRejectsAssetSwappedBetweenVerifyAndExtract(t *testing.T) {
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

	// A second, structurally valid archive whose extraction succeeds but
	// whose bytes differ from the verified asset: the swap must be caught by
	// the post-extract re-hash, not by a format error.
	evilData := makeTarGz(t, "fakebin", []byte("evil-v2-body\n"))
	swapped := false
	upgradeTestHookBeforeExtract = func(assetPath string) {
		if err := os.WriteFile(assetPath, evilData, 0o644); err != nil {
			t.Errorf("swap asset %s: %v", assetPath, err)
			return
		}
		swapped = true
	}
	t.Cleanup(func() { upgradeTestHookBeforeExtract = nil })

	out.Reset()
	err = doUpgrade(&out, &out, []string{"fakebin"}, false, false, "", client, false)
	if err == nil {
		t.Fatalf("upgrade with asset swapped after verification succeeded:\n%s", out.String())
	}
	if !swapped {
		t.Fatal("pre-extract tamper hook did not run")
	}
	if !strings.Contains(err.Error(), "changed between checksum verification and extraction") {
		t.Fatalf("upgrade error = %v, want the verify→extract mismatch refusal", err)
	}

	// The tampered payload never reached the immutable store.
	if _, statErr := os.Stat(filepath.Join(dataDir, "store", "fakebin", "v2.0.0")); !os.IsNotExist(statErr) {
		t.Fatalf("tampered payload reached the store: stat err=%v", statErr)
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
	// The aborted upgrade left no pending journal state behind.
	if cleanErr := statejournal.CheckClean(dataDir); cleanErr != nil {
		t.Fatalf("transaction state not clean after rejected upgrade: %v", cleanErr)
	}
}
