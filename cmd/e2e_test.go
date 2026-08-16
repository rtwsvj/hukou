package cmd

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/rtwsvj/hukou/internal/activation"
	"github.com/rtwsvj/hukou/internal/doctor"
	"github.com/rtwsvj/hukou/internal/ghrelease"
	"github.com/rtwsvj/hukou/internal/manifest"
	"github.com/rtwsvj/hukou/internal/provenance"
	"github.com/rtwsvj/hukou/internal/store"
	"github.com/rtwsvj/hukou/internal/verify"
	"github.com/rtwsvj/hukou/internal/versionpolicy"
)

func TestE2E_AdoptLocalAndRepo(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("HUKOU_DATA_DIR", dataDir)

	binDir := t.TempDir()
	binPath := writeExecutable(t, binDir, "fakebin", "v1.0.0-body\n")

	var out bytes.Buffer

	// adopt with explicit repo
	if err := doAdopt(&out, &out, binPath, "owner/repo", false, "v1.0.0", false, ""); err != nil {
		t.Fatalf("adopt repo: %v\n%s", err, out.String())
	}

	// manifest and original backup exist
	if _, err := os.Stat(filepath.Join(dataDir, "manifest.json")); err != nil {
		t.Fatalf("manifest missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "store", "fakebin", "original", "fakebin")); err != nil {
		t.Fatalf("original backup missing: %v", err)
	}

	// list
	out.Reset()
	if err := doList(&out); err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(out.String(), "fakebin") {
		t.Fatalf("list output missing fakebin:\n%s", out.String())
	}

	// setup fake GitHub release
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

	// upgrade --dry-run
	out.Reset()
	if err := doUpgrade(&out, &out, []string{"fakebin"}, false, true, "", client, false); err != nil {
		t.Fatalf("upgrade dry-run: %v\n%s", err, out.String())
	}
	want := fmt.Sprintf("Would upgrade fakebin: v1.0.0 -> v2.0.0 using asset %s", assetName)
	if !strings.Contains(out.String(), want) {
		t.Fatalf("dry-run output mismatch:\n%s", out.String())
	}
	// path should still be a regular file
	if info, err := os.Lstat(binPath); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("dry-run changed binPath")
	}

	// real upgrade
	out.Reset()
	if err := doUpgrade(&out, &out, []string{"fakebin"}, false, false, "", client, false); err != nil {
		t.Fatalf("upgrade: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "Upgraded fakebin: v1.0.0 -> v2.0.0") {
		t.Fatalf("upgrade output mismatch:\n%s", out.String())
	}

	info, err := os.Lstat(binPath)
	if err != nil {
		t.Fatalf("lstat after upgrade: %v", err)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("binPath is not a regular file after upgrade")
	}
	got, err := os.ReadFile(binPath)
	if err != nil {
		t.Fatalf("read after upgrade: %v", err)
	}
	if string(got) != "v2.0.0-body\n" {
		t.Fatalf("after upgrade content = %q", got)
	}

	// rollback
	out.Reset()
	if err := doRollback(&out, &out, "fakebin", ""); err != nil {
		t.Fatalf("rollback: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "Rolled back fakebin to v1.0.0") {
		t.Fatalf("rollback output mismatch:\n%s", out.String())
	}
	got, err = os.ReadFile(binPath)
	if err != nil {
		t.Fatalf("read after rollback: %v", err)
	}
	if string(got) != "v1.0.0-body\n" {
		t.Fatalf("after rollback content = %q", got)
	}

	// manifest rewritten after rollback
	m, err := manifest.Load(filepath.Join(dataDir, "manifest.json"))
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	e := m.Get("fakebin")
	if e == nil || e.Tag != "v1.0.0" {
		t.Fatalf("manifest tag after rollback = %+v", e)
	}
	sha, err := store.SHA256File(binPath)
	if err != nil {
		t.Fatal(err)
	}
	if e.SHA256 != sha {
		t.Fatalf("manifest sha256 not updated after rollback")
	}
}

func TestE2E_AdoptLocalSkipsUpgrade(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("HUKOU_DATA_DIR", dataDir)

	binDir := t.TempDir()
	binPath := writeExecutable(t, binDir, "localbin", "local-body\n")

	var out bytes.Buffer
	if err := doAdopt(&out, &out, binPath, "", true, "local", false, ""); err != nil {
		t.Fatalf("adopt local: %v\n%s", err, out.String())
	}

	client := &ghrelease.Client{BaseURL: "https://example.test", Sleep: func(time.Duration) {}}
	out.Reset()
	if err := doUpgrade(&out, &out, []string{"localbin"}, false, false, "", client, false); err != nil {
		t.Fatalf("upgrade local: %v", err)
	}
	if !strings.Contains(out.String(), "Skipped localbin: local entry") {
		t.Fatalf("local skip output mismatch:\n%s", out.String())
	}
}

func TestE2E_AdoptRejectsInvalidTagsBeforeStateWrite(t *testing.T) {
	for _, tag := range []string{"original", "Original", "../escape", "release/v1"} {
		t.Run(tag, func(t *testing.T) {
			dataDir := t.TempDir()
			t.Setenv("HUKOU_DATA_DIR", dataDir)
			binPath := writeExecutable(t, t.TempDir(), "fakebin", "body\n")
			var out bytes.Buffer
			err := doAdopt(&out, &out, binPath, "owner/repo", false, tag, false, "")
			if err == nil || !strings.Contains(err.Error(), "invalid adoption tag") {
				t.Fatalf("tag %q: expected validation error, got %v", tag, err)
			}
			if _, err := os.Stat(filepath.Join(dataDir, "manifest.json")); !os.IsNotExist(err) {
				t.Fatalf("tag %q created manifest: %v", tag, err)
			}
			if _, err := os.Stat(filepath.Join(dataDir, "store")); !os.IsNotExist(err) {
				t.Fatalf("tag %q created store: %v", tag, err)
			}
		})
	}
}

func TestE2E_AssetDownload404(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("HUKOU_DATA_DIR", dataDir)
	binPath := writeExecutable(t, t.TempDir(), "fakebin", "v1\n")
	var out bytes.Buffer
	if err := doAdopt(&out, &out, binPath, "owner/repo", false, "v1.0.0", false, ""); err != nil {
		t.Fatal(err)
	}

	assetName := platformAssetName("fakebin")
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/owner/repo/releases/latest", "/repos/owner/repo/releases":
			writeFakeReleaseMetadata(t, w, r, map[string]any{
				"tag_name": "v2.0.0",
				"assets": []map[string]any{
					{
						"name":                 assetName,
						"browser_download_url": server.URL + "/assets/" + assetName,
						"size":                 10,
					},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := testGHClient(server)
	before, _ := os.ReadFile(binPath)
	mBefore, _ := manifest.Load(filepath.Join(dataDir, "manifest.json"))
	tagBefore := mBefore.Get("fakebin").Tag

	err := doUpgrade(&out, &out, []string{"fakebin"}, false, false, "", client, false)
	if err == nil {
		t.Fatal("expected 404 failure")
	}
	after, _ := os.ReadFile(binPath)
	if string(after) != string(before) {
		t.Fatalf("PATH binary changed after 404")
	}
	mAfter, _ := manifest.Load(filepath.Join(dataDir, "manifest.json"))
	if mAfter.Get("fakebin").Tag != tagBefore {
		t.Fatalf("manifest changed after 404")
	}
}

func TestE2E_UnknownBareAssetMustHaveExecutableMagic(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("HUKOU_DATA_DIR", dataDir)
	binPath := writeExecutable(t, t.TempDir(), "fakebin", "v1-body\n")
	var out bytes.Buffer
	if err := doAdopt(&out, &out, binPath, "owner/repo", false, "v1.0.0", false, ""); err != nil {
		t.Fatal(err)
	}

	assetName := fmt.Sprintf("fakebin-%s-%s.weird", runtime.GOOS, runtime.GOARCH)
	assetData := []byte("container bytes, not an executable")
	checksum := fmt.Sprintf("%x  %s\n", sha256.Sum256(assetData), assetName)
	server := fakeGitHubServer(t, assetName, assetData, checksum, "v2.0.0")
	defer server.Close()
	err := doUpgrade(&out, &out, []string{"fakebin"}, false, false, "", testGHClient(server), false)
	if err == nil || !strings.Contains(err.Error(), "not a recognized executable") {
		t.Fatalf("expected bare executable validation failure, err=%v output=%s", err, out.String())
	}
	got, readErr := os.ReadFile(binPath)
	if readErr != nil || string(got) != "v1-body\n" {
		t.Fatalf("live binary changed after invalid bare asset: body=%q err=%v", got, readErr)
	}
}

func TestE2E_ChecksumMismatchAborts(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("HUKOU_DATA_DIR", dataDir)
	binPath := writeExecutable(t, t.TempDir(), "fakebin", "original-body\n")
	var out bytes.Buffer
	if err := doAdopt(&out, &out, binPath, "owner/repo", false, "v1.0.0", false, ""); err != nil {
		t.Fatal(err)
	}

	assetName := platformAssetName("fakebin")
	assetData := makeTarGz(t, "fakebin", []byte("evil-body\n"))
	badChecksum := fmt.Sprintf("%x  %s\n", sha256.Sum256([]byte("not-the-asset")), assetName)
	server := fakeGitHubServer(t, assetName, assetData, badChecksum, "v2.0.0")
	defer server.Close()

	before, _ := os.ReadFile(binPath)
	mBefore, _ := manifest.Load(filepath.Join(dataDir, "manifest.json"))
	shaBefore := mBefore.Get("fakebin").SHA256
	tagBefore := mBefore.Get("fakebin").Tag

	err := doUpgrade(&out, &out, []string{"fakebin"}, false, false, "", testGHClient(server), false)
	if err == nil {
		t.Fatal("expected verify failure")
	}
	after, _ := os.ReadFile(binPath)
	if string(after) != string(before) {
		t.Fatalf("PATH binary changed after checksum mismatch")
	}
	mAfter, _ := manifest.Load(filepath.Join(dataDir, "manifest.json"))
	e := mAfter.Get("fakebin")
	if e.Tag != tagBefore || e.SHA256 != shaBefore {
		t.Fatalf("manifest changed: tag=%s sha=%s", e.Tag, e.SHA256)
	}
	// still regular file, not half-switched
	info, err := os.Lstat(binPath)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() {
		t.Fatal("live path is not regular after aborted upgrade")
	}
}

func TestE2E_ChecksumAssetMissingChosenEntryAborts(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("HUKOU_DATA_DIR", dataDir)
	binPath := writeExecutable(t, t.TempDir(), "fakebin", "original-body\n")
	var out bytes.Buffer
	if err := doAdopt(&out, &out, binPath, "owner/repo", false, "v1.0.0", false, ""); err != nil {
		t.Fatal(err)
	}

	assetName := platformAssetName("fakebin")
	assetData := makeTarGz(t, "fakebin", []byte("v2-body\n"))
	checksum := fmt.Sprintf("%x  another-asset.tar.gz\n", sha256.Sum256(assetData))
	server := fakeGitHubServer(t, assetName, assetData, checksum, "v2.0.0")
	defer server.Close()

	before, _ := os.ReadFile(binPath)
	err := doUpgrade(&out, &out, []string{"fakebin"}, false, false, "", testGHClient(server), false)
	if err == nil || !errors.Is(err, verify.ErrNoChecksum) || !strings.Contains(out.String(), "no checksum entry") {
		t.Fatalf("expected missing checksum entry failure, err=%v output=%s", err, out.String())
	}
	after, _ := os.ReadFile(binPath)
	if !bytes.Equal(after, before) {
		t.Fatal("binary changed after checksum file omitted selected asset")
	}
	m, err := manifest.Load(filepath.Join(dataDir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got := m.Get("fakebin").Tag; got != "v1.0.0" {
		t.Fatalf("manifest tag changed to %s", got)
	}
	if info, err := os.Lstat(binPath); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("live path topology changed: info=%v err=%v", info, err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "store", "fakebin", "v2.0.0")); !os.IsNotExist(err) {
		t.Fatalf("new version stored before checksum verification: %v", err)
	}
}

func TestE2E_ExactChecksumSidecarAcceptsBareDigest(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("HUKOU_DATA_DIR", dataDir)
	binPath := writeExecutable(t, t.TempDir(), "fakebin", "v1-body\n")
	var out bytes.Buffer
	if err := doAdopt(&out, &out, binPath, "owner/repo", false, "v1.0.0", false, ""); err != nil {
		t.Fatal(err)
	}

	assetName := platformAssetName("fakebin")
	assetData := makeTarGz(t, "fakebin", []byte("v2-body\n"))
	checksumName := assetName + ".sha256"
	checksum := fmt.Sprintf("%x\n", sha256.Sum256(assetData))
	server := fakeGitHubServerWithChecksumAsset(t, assetName, assetData, checksumName, checksum, "v2.0.0")
	defer server.Close()
	if err := doUpgrade(&out, &out, []string{"fakebin"}, false, false, "", testGHClient(server), false); err != nil {
		t.Fatalf("upgrade with exact sidecar: %v\n%s", err, out.String())
	}

	m, err := manifest.Load(filepath.Join(dataDir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	e := m.Get("fakebin")
	if e.ChecksumAsset != checksumName || !e.ChecksumVerified || e.AssetSHA256 == "" {
		t.Fatalf("checksum provenance not recorded: %+v", e)
	}
	// Wire-form assertion: verified success must appear in serialized JSON, not
	// only as a decoded bool true (pairs with the bypass false raw check).
	manifestRaw, err := os.ReadFile(filepath.Join(dataDir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(manifestRaw), `"checksum_verified": true`) {
		t.Fatalf("verified path JSON must contain checksum_verified true:\n%s", manifestRaw)
	}
}

func TestE2E_UpgradeSaveFailureRestoresLiveInstall(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("HUKOU_DATA_DIR", dataDir)
	binPath := writeExecutable(t, t.TempDir(), "fakebin", "v1-body\n")
	var out bytes.Buffer
	if err := doAdopt(&out, &out, binPath, "owner/repo", false, "v1.0.0", false, ""); err != nil {
		t.Fatal(err)
	}

	assetName := platformAssetName("fakebin")
	assetData := makeTarGz(t, "fakebin", []byte("v2-body\n"))
	checksum := fmt.Sprintf("%x  %s\n", sha256.Sum256(assetData), assetName)
	server := fakeGitHubServer(t, assetName, assetData, checksum, "v2.0.0")
	defer server.Close()

	injected := errors.New("injected manifest write failure")
	err := doUpgradeWithSave(&out, &out, []string{"fakebin"}, false, false, "", testGHClient(server), false, func(*manifest.Manifest) error {
		return injected
	})
	if err == nil || !strings.Contains(out.String(), injected.Error()) {
		t.Fatalf("expected injected save failure, err=%v output=%s", err, out.String())
	}
	got, err := os.ReadFile(binPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "v1-body\n" {
		t.Fatalf("live content after restore = %q", got)
	}
	if info, err := os.Lstat(binPath); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("live topology was not restored to a regular file: info=%v err=%v", info, err)
	}
	m, err := manifest.Load(filepath.Join(dataDir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if e := m.Get("fakebin"); e.Tag != "v1.0.0" || e.AssetName != "" {
		t.Fatalf("persisted manifest changed after failed save: %+v", e)
	}
}

func TestE2E_ActivateFailureKeepsInstall(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("HUKOU_DATA_DIR", dataDir)

	// Put binary in a directory we will make read-only after adopt, so Activate
	// (which needs to write a temporary regular file in the same dir) fails.
	binDir := t.TempDir()
	binPath := writeExecutable(t, binDir, "fakebin", "v1-body\n")
	var out bytes.Buffer
	if err := doAdopt(&out, &out, binPath, "owner/repo", false, "v1.0.0", false, ""); err != nil {
		t.Fatal(err)
	}

	assetName := platformAssetName("fakebin")
	assetData := makeTarGz(t, "fakebin", []byte("v2-body\n"))
	checksum := assetChecksumLine(assetName, assetData)
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/owner/repo/releases/latest", "/repos/owner/repo/releases":
			writeFakeReleaseMetadata(t, w, r, map[string]any{
				"tag_name": "v2.0.0",
				"assets": []map[string]any{
					{
						"name":                 assetName,
						"browser_download_url": server.URL + "/assets/" + assetName,
						"size":                 len(assetData),
					},
					{
						"name":                 "checksums.txt",
						"browser_download_url": server.URL + "/assets/checksums.txt",
						"size":                 len(checksum),
					},
				},
			})
		case "/assets/" + assetName:
			_, _ = w.Write(assetData)
		case "/assets/checksums.txt":
			_, _ = w.Write([]byte(checksum))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	// Make binDir read-only so creation/rename of the activation temp fails.
	if err := os.Chmod(binDir, 0o555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(binDir, 0o755)

	before, _ := os.ReadFile(binPath)
	err := doUpgrade(&out, &out, []string{"fakebin"}, false, false, "", testGHClient(server), false)
	if err == nil {
		// On some filesystems chmod does not block the owner; this fixture cannot
		// force the intended failure there.
		t.Skip("filesystem did not block Activate under read-only dir")
	}

	// Original install still readable with same content.
	_ = os.Chmod(binDir, 0o755)
	after, err := os.ReadFile(binPath)
	if err != nil {
		t.Fatalf("original install unreadable: %v", err)
	}
	if string(after) != string(before) {
		t.Fatalf("path content changed after failed transaction: got %q, want %q", after, before)
	}
	// At minimum, store should still have original backup.
	orig := filepath.Join(dataDir, "store", "fakebin", "original", "fakebin")
	if _, err := os.Stat(orig); err != nil {
		t.Fatalf("original backup missing: %v", err)
	}
}

func TestE2E_RollbackOriginalThenUpgradeKeepsOriginalImmutable(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("HUKOU_DATA_DIR", dataDir)

	binPath := writeExecutable(t, t.TempDir(), "fakebin", "original-body\n")
	if err := os.Chmod(binPath, 0o751); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := doAdopt(&out, &out, binPath, "owner/repo", false, "v1.0.0", false, ""); err != nil {
		t.Fatal(err)
	}
	originalPath := filepath.Join(dataDir, "store", "fakebin", "original", "fakebin")
	originalBytes, err := os.ReadFile(originalPath)
	if err != nil {
		t.Fatal(err)
	}
	originalInfo, err := os.Stat(originalPath)
	if err != nil {
		t.Fatal(err)
	}

	upgrade := func(tag, body string) {
		t.Helper()
		assetName := platformAssetName("fakebin")
		assetData := makeTarGz(t, "fakebin", []byte(body))
		server := fakeGitHubServer(t, assetName, assetData, assetChecksumLine(assetName, assetData), tag)
		defer server.Close()
		if err := doUpgrade(&out, &out, []string{"fakebin"}, false, false, "", testGHClient(server), false); err != nil {
			t.Fatalf("upgrade %s: %v\n%s", tag, err, out.String())
		}
	}

	upgrade("v2.0.0", "v2-body\n")
	if err := doRollback(&out, &out, "fakebin", "original"); err != nil {
		t.Fatalf("rollback original: %v\n%s", err, out.String())
	}
	if got, err := os.ReadFile(binPath); err != nil || !bytes.Equal(got, originalBytes) {
		t.Fatalf("live original mismatch after rollback: content=%q err=%v", got, err)
	}
	upgrade("v3.0.0", "v3-body\n")

	got, err := os.ReadFile(originalPath)
	if err != nil || !bytes.Equal(got, originalBytes) {
		t.Fatalf("original backup was rewritten: content=%q err=%v", got, err)
	}
	gotInfo, err := os.Stat(originalPath)
	if err != nil {
		t.Fatal(err)
	}
	if gotInfo.Mode().Perm() != originalInfo.Mode().Perm() {
		t.Fatalf("original mode changed: got %v want %v", gotInfo.Mode().Perm(), originalInfo.Mode().Perm())
	}
}

func TestE2E_UpgradeDetectsExternalChangeBeforeActivation(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("HUKOU_DATA_DIR", dataDir)
	binPath := writeExecutable(t, t.TempDir(), "fakebin", "v1-body\n")
	var out bytes.Buffer
	if err := doAdopt(&out, &out, binPath, "owner/repo", false, "v1.0.0", false, ""); err != nil {
		t.Fatal(err)
	}

	assetName := platformAssetName("fakebin")
	assetData := makeTarGz(t, "fakebin", []byte("v2-body\n"))
	checksum := assetChecksumLine(assetName, assetData)
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/owner/repo/releases/latest", "/repos/owner/repo/releases":
			writeFakeReleaseMetadata(t, w, r, map[string]any{
				"tag_name": "v2.0.0",
				"assets": []map[string]any{
					{
						"name":                 assetName,
						"browser_download_url": server.URL + "/assets/" + assetName,
						"size":                 len(assetData),
					},
					{
						"name":                 "checksums.txt",
						"browser_download_url": server.URL + "/assets/checksums.txt",
						"size":                 len(checksum),
					},
				},
			})
		case "/assets/" + assetName:
			if err := os.WriteFile(binPath, []byte("external-body\n"), 0o755); err != nil {
				t.Errorf("external update fixture: %v", err)
				http.Error(w, "fixture failed", http.StatusInternalServerError)
				return
			}
			_, _ = w.Write(assetData)
		case "/assets/checksums.txt":
			_, _ = w.Write([]byte(checksum))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	err := doUpgrade(&out, &out, []string{"fakebin"}, false, false, "", testGHClient(server), false)
	if err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("expected external-change rejection, err=%v output=%s", err, out.String())
	}
	got, readErr := os.ReadFile(binPath)
	if readErr != nil || string(got) != "external-body\n" {
		t.Fatalf("external update was overwritten: body=%q err=%v", got, readErr)
	}
	if info, statErr := os.Lstat(binPath); statErr != nil || !info.Mode().IsRegular() {
		t.Fatalf("live path was activated despite drift: info=%v err=%v", info, statErr)
	}
	m, loadErr := manifest.Load(filepath.Join(dataDir, "manifest.json"))
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if e := m.Get("fakebin"); e.Tag != "v1.0.0" {
		t.Fatalf("manifest changed after drift rejection: %+v", e)
	}
}

func TestE2E_AllPartialFailureNonZero(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("HUKOU_DATA_DIR", dataDir)

	bin1 := writeExecutable(t, t.TempDir(), "goodbin", "g1\n")
	bin2 := writeExecutable(t, t.TempDir(), "badbin", "b1\n")
	var out bytes.Buffer
	if err := doAdopt(&out, &out, bin1, "owner/good", false, "v1.0.0", false, ""); err != nil {
		t.Fatal(err)
	}
	if err := doAdopt(&out, &out, bin2, "owner/bad", false, "v1.0.0", false, ""); err != nil {
		t.Fatal(err)
	}

	assetNameGood := platformAssetName("goodbin")
	assetDataGood := makeTarGz(t, "goodbin", []byte("g2\n"))
	checksumGood := assetChecksumLine(assetNameGood, assetDataGood)

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/owner/good/releases/latest", "/repos/owner/good/releases":
			writeFakeReleaseMetadata(t, w, r, map[string]any{
				"tag_name": "v2.0.0",
				"assets": []map[string]any{
					{
						"name":                 assetNameGood,
						"browser_download_url": server.URL + "/assets/" + assetNameGood,
						"size":                 len(assetDataGood),
					},
					{
						"name":                 "checksums.txt",
						"browser_download_url": server.URL + "/assets/checksums.txt",
						"size":                 len(checksumGood),
					},
				},
			})
		case "/repos/owner/bad/releases/latest", "/repos/owner/bad/releases":
			http.Error(w, "boom", http.StatusInternalServerError)
		case "/assets/" + assetNameGood:
			_, _ = w.Write(assetDataGood)
		case "/assets/checksums.txt":
			_, _ = w.Write([]byte(checksumGood))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	err := doUpgrade(&stdout, &stderr, nil, true, false, "", testGHClient(server), false)
	if err == nil {
		t.Fatal("expected non-nil error for partial failure")
	}
	if !strings.Contains(err.Error(), "failed") {
		t.Fatalf("err=%v", err)
	}
	if !strings.Contains(stderr.String(), "upgrade(s) failed") {
		t.Fatalf("stderr missing summary:\n%s", stderr.String())
	}
}

func TestE2E_AdoptOriginalBranchActivatesNewVersion(t *testing.T) {
	// Simulate missing original/ so upgrade takes AdoptOriginal then Activate.
	dataDir := t.TempDir()
	t.Setenv("HUKOU_DATA_DIR", dataDir)

	binPath := writeExecutable(t, t.TempDir(), "fakebin", "v1-body\n")
	sha, err := store.SHA256File(binPath)
	if err != nil {
		t.Fatal(err)
	}

	// Write manifest without creating original/ backup (unlike doAdopt).
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	m := &manifest.Manifest{SchemaVersion: 1}
	m.Put(manifest.Entry{
		Name:      "fakebin",
		Path:      binPath,
		Repo:      "owner/repo",
		Tag:       "v1.0.0",
		SHA256:    sha,
		AdoptedAt: time.Now().Format(time.RFC3339),
		UpdatedAt: time.Now().Format(time.RFC3339),
	})
	if err := m.Save(filepath.Join(dataDir, "manifest.json")); err != nil {
		t.Fatal(err)
	}

	assetName := platformAssetName("fakebin")
	assetData := makeTarGz(t, "fakebin", []byte("v2-body\n"))
	server := fakeGitHubServer(t, assetName, assetData, assetChecksumLine(assetName, assetData), "v2.0.0")
	defer server.Close()

	var out bytes.Buffer
	if err := doUpgrade(&out, &out, []string{"fakebin"}, false, false, "", testGHClient(server), false); err != nil {
		t.Fatalf("upgrade: %v\n%s", err, out.String())
	}

	info, err := os.Lstat(binPath)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() {
		t.Fatal("expected regular live file")
	}
	got, err := os.ReadFile(binPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "v2-body\n" {
		t.Fatalf("content=%q", got)
	}
}

func TestE2E_RollbackThenUpgradePruneKeepsActive(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("HUKOU_DATA_DIR", dataDir)

	binPath := writeExecutable(t, t.TempDir(), "fakebin", "v1-body\n")
	var out bytes.Buffer
	if err := doAdopt(&out, &out, binPath, "owner/repo", false, "v1.0.0", false, ""); err != nil {
		t.Fatal(err)
	}

	// Build several versions via upgrade.
	for _, tag := range []string{"v2.0.0", "v3.0.0", "v4.0.0"} {
		assetName := platformAssetName("fakebin")
		assetData := makeTarGz(t, "fakebin", []byte(tag+"-body\n"))
		server := fakeGitHubServer(t, assetName, assetData, assetChecksumLine(assetName, assetData), tag)
		if err := doUpgrade(&out, &out, []string{"fakebin"}, false, false, "", testGHClient(server), false); err != nil {
			server.Close()
			t.Fatalf("upgrade %s: %v", tag, err)
		}
		server.Close()
	}

	s := &store.Store{Root: filepath.Join(dataDir, "store")}
	versions, err := s.Versions("fakebin")
	if err != nil || len(versions) < 2 {
		t.Fatalf("need ≥2 versions for rollback, got %v err=%v", versions, err)
	}
	// Pick an older non-current version that still exists after Prune.
	m, err := manifest.Load(filepath.Join(dataDir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	current := m.Get("fakebin").Tag
	var oldTag string
	for _, v := range versions {
		if v != current {
			oldTag = v
			break
		}
	}
	if oldTag == "" {
		t.Fatal("no older version to roll back to")
	}

	if err := doRollback(&out, &out, "fakebin", oldTag); err != nil {
		t.Fatalf("rollback to %s: %v\n%s", oldTag, err, out.String())
	}
	m, err = manifest.Load(filepath.Join(dataDir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if e := m.Get("fakebin"); e == nil || e.Tag != oldTag {
		t.Fatalf("manifest after rollback: %+v want tag %s", e, oldTag)
	}
	sha, err := store.SHA256File(binPath)
	if err != nil {
		t.Fatal(err)
	}
	if m.Get("fakebin").SHA256 != sha {
		t.Fatal("manifest sha256 not rewritten after rollback")
	}

	// Upgrade again; Prune runs with active = new tag.
	assetName := platformAssetName("fakebin")
	assetData := makeTarGz(t, "fakebin", []byte("v5-body\n"))
	server := fakeGitHubServer(t, assetName, assetData, assetChecksumLine(assetName, assetData), "v5.0.0")
	defer server.Close()
	if err := doUpgrade(&out, &out, []string{"fakebin"}, false, false, "", testGHClient(server), false); err != nil {
		t.Fatalf("upgrade v5: %v\n%s", err, out.String())
	}
	got, err := os.ReadFile(binPath)
	if err != nil {
		t.Fatalf("active unreadable after upgrade: %v", err)
	}
	if string(got) != "v5-body\n" {
		t.Fatalf("content=%q", got)
	}

	// Force-activate an older remaining version and prune keep=1: active must survive.
	versions, _ = s.Versions("fakebin")
	var protect string
	for _, v := range versions {
		if v != "v5.0.0" {
			protect = v
			break
		}
	}
	if protect == "" {
		// Only v5 left — seed an extra older version to protect.
		src := writeExecutable(t, t.TempDir(), "fakebin", "old-body\n")
		if err := s.Put("fakebin", "v-old", src); err != nil {
			t.Fatal(err)
		}
		protect = "v-old"
	}
	if err := s.Activate("fakebin", protect, binPath); err != nil {
		t.Fatalf("activate %s: %v", protect, err)
	}
	storeRoot := filepath.Join(dataDir, "store", "fakebin")
	entries, _ := os.ReadDir(storeRoot)
	for i, e := range entries {
		if !e.IsDir() || e.Name() == "original" {
			continue
		}
		day := 2 + i
		if e.Name() == protect {
			day = 1 // oldest mtime
		}
		mtime := time.Date(2021, 1, day, 0, 0, 0, 0, time.UTC)
		_ = os.Chtimes(filepath.Join(storeRoot, e.Name()), mtime, mtime)
	}
	protectedSHA, err := store.SHA256File(binPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Prune("fakebin", 1, protect, protectedSHA); err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if _, err := os.Stat(filepath.Join(storeRoot, protect)); err != nil {
		t.Fatalf("active version %s pruned: %v", protect, err)
	}
	if _, err := os.ReadFile(binPath); err != nil {
		t.Fatalf("active unreadable after prune: %v", err)
	}
}

func TestE2E_RepeatedRollbackFollowsActivationLineageNotMtime(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("HUKOU_DATA_DIR", dataDir)

	binPath := writeExecutable(t, t.TempDir(), "lineage-tool", "v1-body\n")
	var output bytes.Buffer
	if err := doAdopt(&output, &output, binPath, "owner/lineage-tool", false, "v1.0.0", false, ""); err != nil {
		t.Fatal(err)
	}

	for _, version := range []struct {
		tag  string
		body string
	}{
		{tag: "v2.0.0", body: "v2-body\n"},
		{tag: "v3.0.0", body: "v3-body\n"},
	} {
		assetName := platformAssetName("lineage-tool")
		assetData := makeTarGz(t, "lineage-tool", []byte(version.body))
		server := fakeGitHubServer(t, assetName, assetData, assetChecksumLine(assetName, assetData), version.tag)
		err := doUpgrade(&output, &output, []string{"lineage-tool"}, false, false, "", testGHClient(server), false)
		server.Close()
		if err != nil {
			t.Fatalf("upgrade %s: %v\n%s", version.tag, err, output.String())
		}
	}

	// Deliberately make filesystem time disagree with logical history. Rollback
	// must still walk v3 -> v2 -> v1 by activation parent IDs.
	toolStore := filepath.Join(dataDir, "store", "lineage-tool")
	mtimes := map[string]time.Time{
		"v1.0.0": time.Date(2030, 1, 3, 0, 0, 0, 0, time.UTC),
		"v2.0.0": time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
		"v3.0.0": time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC),
	}
	for tag, changedAt := range mtimes {
		path := filepath.Join(toolStore, tag)
		if err := os.Chtimes(path, changedAt, changedAt); err != nil {
			t.Fatalf("change %s mtime: %v", tag, err)
		}
	}

	for _, want := range []struct {
		tag  string
		body string
	}{
		{tag: "v2.0.0", body: "v2-body\n"},
		{tag: "v1.0.0", body: "v1-body\n"},
	} {
		output.Reset()
		if err := doRollback(&output, &output, "lineage-tool", ""); err != nil {
			t.Fatalf("rollback to %s: %v\n%s", want.tag, err, output.String())
		}
		if got, err := os.ReadFile(binPath); err != nil || string(got) != want.body {
			t.Fatalf("live after rollback to %s = %q, err=%v", want.tag, got, err)
		}
		m, err := manifest.Load(filepath.Join(dataDir, "manifest.json"))
		if err != nil {
			t.Fatal(err)
		}
		entry := m.Get("lineage-tool")
		if entry == nil || entry.Tag != want.tag {
			t.Fatalf("manifest after rollback = %+v, want %s", entry, want.tag)
		}
	}

	m, err := manifest.Load(filepath.Join(dataDir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := activation.Previous(*m.Get("lineage-tool")); !errors.Is(err, activation.ErrNoPreviousActivation) {
		t.Fatalf("rollback cursor did not terminate at the root: %v", err)
	}
}

func TestE2E_SymlinkAdoptUpgradeAndImplicitRollback(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("HUKOU_DATA_DIR", dataDir)

	target := writeExecutable(t, t.TempDir(), "real-tool", "v1-target\n")
	live := filepath.Join(t.TempDir(), "symlink-tool")
	if err := os.Symlink(target, live); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := doAdopt(&output, &output, live, "owner/symlink-tool", false, "v1.0.0", false, ""); err != nil {
		t.Fatalf("adopt symlink: %v\n%s", err, output.String())
	}

	assetName := platformAssetName("symlink-tool")
	assetData := makeTarGz(t, "symlink-tool", []byte("v2-target\n"))
	server := fakeGitHubServer(t, assetName, assetData, assetChecksumLine(assetName, assetData), "v2.0.0")
	err := doUpgrade(&output, &output, []string{"symlink-tool"}, false, false, "", testGHClient(server), false)
	server.Close()
	if err != nil {
		t.Fatalf("upgrade symlink entry: %v\n%s", err, output.String())
	}
	if info, err := os.Lstat(live); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("upgraded live path is not regular: info=%v err=%v", info, err)
	}

	output.Reset()
	if err := doRollback(&output, &output, "symlink-tool", ""); err != nil {
		t.Fatalf("implicit rollback: %v\n%s", err, output.String())
	}
	if got, err := os.ReadFile(live); err != nil || string(got) != "v1-target\n" {
		t.Fatalf("rolled back live bytes = %q, err=%v", got, err)
	}
	m, err := manifest.Load(filepath.Join(dataDir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if entry := m.Get("symlink-tool"); entry == nil || entry.Tag != "v1.0.0" {
		t.Fatalf("manifest after symlink rollback: %+v", entry)
	}
}

func TestE2E_RollbackSaveFailureRestoresLiveInstall(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("HUKOU_DATA_DIR", dataDir)
	binPath := writeExecutable(t, t.TempDir(), "fakebin", "v1-body\n")
	var out bytes.Buffer
	if err := doAdopt(&out, &out, binPath, "owner/repo", false, "v1.0.0", false, ""); err != nil {
		t.Fatal(err)
	}

	assetName := platformAssetName("fakebin")
	assetData := makeTarGz(t, "fakebin", []byte("v2-body\n"))
	checksum := fmt.Sprintf("%x  %s\n", sha256.Sum256(assetData), assetName)
	server := fakeGitHubServer(t, assetName, assetData, checksum, "v2.0.0")
	if err := doUpgrade(&out, &out, []string{"fakebin"}, false, false, "", testGHClient(server), false); err != nil {
		server.Close()
		t.Fatal(err)
	}
	server.Close()

	before, err := os.ReadFile(binPath)
	if err != nil {
		t.Fatal(err)
	}
	injected := errors.New("injected rollback save failure")
	err = doRollbackWithSave(&out, &out, "fakebin", "original", func(*manifest.Manifest) error {
		return injected
	})
	if err == nil || !errors.Is(err, injected) {
		t.Fatalf("expected injected save failure, err=%v output=%s", err, out.String())
	}
	got, err := os.ReadFile(binPath)
	if err != nil || !bytes.Equal(got, before) || string(got) != "v2-body\n" {
		t.Fatalf("live content after rollback restore = %q err=%v", got, err)
	}
	if info, statErr := os.Lstat(binPath); statErr != nil || !info.Mode().IsRegular() {
		t.Fatalf("live topology after rollback restore: info=%v err=%v", info, statErr)
	}
	m, err := manifest.Load(filepath.Join(dataDir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if e := m.Get("fakebin"); e.Tag != "v2.0.0" || !e.ChecksumVerified {
		t.Fatalf("persisted manifest changed after failed rollback: %+v", e)
	}
}

func TestE2E_RollbackRejectsPostSnapshotExternalChange(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("HUKOU_DATA_DIR", dataDir)
	binPath := writeExecutable(t, t.TempDir(), "fakebin", "v1-body\n")
	var out bytes.Buffer
	if err := doAdopt(&out, &out, binPath, "owner/repo", false, "v1.0.0", false, ""); err != nil {
		t.Fatal(err)
	}

	saveCalled := false
	err := doRollbackWithDeps(&out, &out, "fakebin", "original", func(*manifest.Manifest) error {
		saveCalled = true
		return nil
	}, func(path string) (*store.LiveSnapshot, error) {
		snapshot, err := store.SnapshotLive(path)
		if err != nil {
			return nil, err
		}
		if err := os.WriteFile(path, []byte("external-body\n"), 0o755); err != nil {
			return nil, err
		}
		return snapshot, nil
	})
	if err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("expected post-snapshot drift rejection, err=%v output=%s", err, out.String())
	}
	if saveCalled {
		t.Fatal("manifest save ran after post-snapshot drift")
	}
	got, readErr := os.ReadFile(binPath)
	if readErr != nil || string(got) != "external-body\n" {
		t.Fatalf("external update was overwritten: content=%q err=%v", got, readErr)
	}
	m, loadErr := manifest.Load(filepath.Join(dataDir, "manifest.json"))
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if e := m.Get("fakebin"); e.Tag != "v1.0.0" {
		t.Fatalf("manifest changed after drift rejection: %+v", e)
	}
	if matches, globErr := filepath.Glob(filepath.Join(filepath.Dir(binPath), ".hukou-rollback-*")); globErr != nil || len(matches) != 0 {
		t.Fatalf("rollback snapshot not discarded: matches=%v err=%v", matches, globErr)
	}
}

func TestE2E_DownloadOversizeAborted(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("HUKOU_DATA_DIR", dataDir)
	binPath := writeExecutable(t, t.TempDir(), "fakebin", "v1\n")
	var out bytes.Buffer
	if err := doAdopt(&out, &out, binPath, "owner/repo", false, "v1.0.0", false, ""); err != nil {
		t.Fatal(err)
	}

	assetName := platformAssetName("fakebin")
	// Claim tiny size but serve large body → size mismatch / exceed.
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/owner/repo/releases/latest", "/repos/owner/repo/releases":
			writeFakeReleaseMetadata(t, w, r, map[string]any{
				"tag_name": "v2.0.0",
				"assets": []map[string]any{{
					"name":                 assetName,
					"browser_download_url": server.URL + "/assets/" + assetName,
					"size":                 4, // expect 4 bytes
				}},
			})
		case "/assets/" + assetName:
			_, _ = w.Write(bytes.Repeat([]byte("x"), 100))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	before, _ := os.ReadFile(binPath)
	err := doUpgrade(&out, &out, []string{"fakebin"}, false, false, "", testGHClient(server), false)
	if err == nil {
		t.Fatal("expected oversize failure")
	}
	after, _ := os.ReadFile(binPath)
	if string(after) != string(before) {
		t.Fatal("binary changed after oversize abort")
	}
}

func TestE2E_DryRunDoesNotWriteState(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("HUKOU_DATA_DIR", dataDir)
	binPath := writeExecutable(t, t.TempDir(), "fakebin", "v1-body\n")
	sha, err := store.SHA256File(binPath)
	if err != nil {
		t.Fatal(err)
	}
	m := &manifest.Manifest{SchemaVersion: 1}
	m.Put(manifest.Entry{
		Name:      "fakebin",
		Path:      binPath,
		Repo:      "owner/repo",
		Tag:       "v1.0.0",
		SHA256:    sha,
		AdoptedAt: "2026-07-14T00:00:00Z",
		UpdatedAt: "2026-07-14T00:00:00Z",
	})
	manifestFile := filepath.Join(dataDir, "manifest.json")
	if err := m.Save(manifestFile); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(manifestFile)
	if err != nil {
		t.Fatal(err)
	}
	beforeInfo, err := os.Stat(manifestFile)
	if err != nil {
		t.Fatal(err)
	}
	beforeEntries := directoryEntryNames(t, dataDir)

	assetName := platformAssetName("fakebin")
	assetData := makeTarGz(t, "fakebin", []byte("v2-body\n"))
	server := fakeGitHubServer(t, assetName, assetData, assetChecksumLine(assetName, assetData), "v2.0.0")
	defer server.Close()
	var out bytes.Buffer
	if err := doUpgrade(&out, &out, []string{"fakebin"}, false, true, "", testGHClient(server), false); err != nil {
		t.Fatal(err)
	}

	after, err := os.ReadFile(manifestFile)
	if err != nil {
		t.Fatal(err)
	}
	afterInfo, err := os.Stat(manifestFile)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) || !afterInfo.ModTime().Equal(beforeInfo.ModTime()) {
		t.Fatal("dry-run rewrote manifest")
	}
	if got := directoryEntryNames(t, dataDir); strings.Join(got, "\x00") != strings.Join(beforeEntries, "\x00") {
		t.Fatalf("dry-run changed data directory: before=%v after=%v", beforeEntries, got)
	}
	for _, p := range []string{filepath.Join(dataDir, "state.lock"), filepath.Join(dataDir, "store")} {
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			t.Fatalf("dry-run created %s (err=%v)", p, err)
		}
	}
}

func TestE2E_SemverShorthandCurrentNeverDownloadsOrMutates(t *testing.T) {
	for _, current := range []string{"v1", "v1.2"} {
		t.Run(current, func(t *testing.T) {
			dataDir := t.TempDir()
			t.Setenv("HUKOU_DATA_DIR", dataDir)
			binPath := writeExecutable(t, t.TempDir(), "fakebin", "current-body\n")
			var output bytes.Buffer
			if err := doAdopt(&output, &output, binPath, "owner/repo", false, current, false, ""); err != nil {
				t.Fatal(err)
			}

			manifestPath := filepath.Join(dataDir, "manifest.json")
			beforeManifest, err := os.ReadFile(manifestPath)
			if err != nil {
				t.Fatal(err)
			}
			beforeLive, err := os.ReadFile(binPath)
			if err != nil {
				t.Fatal(err)
			}

			assetName := platformAssetName("fakebin")
			assetData := makeTarGz(t, "fakebin", []byte("replacement-body\n"))
			metadataCalls, assetCalls := 0, 0
			var server *httptest.Server
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/repos/owner/repo/releases":
					metadataCalls++
					writeFakeReleaseMetadata(t, w, r, map[string]any{
						"tag_name": "v2.0.0",
						"assets": []map[string]any{{
							"name":                 assetName,
							"browser_download_url": server.URL + "/assets/" + assetName,
							"size":                 len(assetData),
						}},
					})
				case "/assets/" + assetName:
					assetCalls++
					_, _ = w.Write(assetData)
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()

			output.Reset()
			err = doUpgrade(&output, &output, []string{"fakebin"}, false, false, "", testGHClient(server), false)
			if !errors.Is(err, versionpolicy.ErrCurrentNotSemver) {
				t.Fatalf("error=%v\n%s", err, output.String())
			}
			if metadataCalls != 1 || assetCalls != 0 {
				t.Fatalf("metadata calls=%d asset downloads=%d", metadataCalls, assetCalls)
			}
			afterManifest, manifestErr := os.ReadFile(manifestPath)
			afterLive, liveErr := os.ReadFile(binPath)
			if manifestErr != nil || liveErr != nil {
				t.Fatalf("read after rejection: manifest=%v live=%v", manifestErr, liveErr)
			}
			if !bytes.Equal(afterManifest, beforeManifest) || !bytes.Equal(afterLive, beforeLive) {
				t.Fatal("shorthand baseline rejection changed manifest or live binary")
			}
		})
	}
}

func TestE2E_DryRunDoesNotCreateMissingDataRoot(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "does-not-exist")
	t.Setenv("HUKOU_DATA_DIR", dataDir)
	var out bytes.Buffer
	if err := doUpgrade(&out, &out, nil, true, true, "", &ghrelease.Client{}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(dataDir); !os.IsNotExist(err) {
		t.Fatalf("dry-run created missing data root: %v", err)
	}
}

func TestE2E_UpgradeRejectsNamesWithAllWithoutWriting(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "does-not-exist")
	t.Setenv("HUKOU_DATA_DIR", dataDir)
	var out bytes.Buffer
	err := doUpgrade(&out, &out, []string{"one"}, true, false, "", &ghrelease.Client{}, false)
	if err == nil || !strings.Contains(err.Error(), "--all") {
		t.Fatalf("expected names/--all conflict, got %v", err)
	}
	if _, statErr := os.Lstat(dataDir); !os.IsNotExist(statErr) {
		t.Fatalf("invalid invocation created data root: %v", statErr)
	}
}

func TestE2E_AdoptRejectsDuplicateBasename(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("HUKOU_DATA_DIR", dataDir)
	first := writeExecutable(t, t.TempDir(), "samebin", "first\n")
	second := writeExecutable(t, t.TempDir(), "samebin", "second\n")
	var out bytes.Buffer
	if err := doAdopt(&out, &out, first, "owner/first", false, "v1", false, ""); err != nil {
		t.Fatal(err)
	}
	if err := doAdopt(&out, &out, second, "owner/second", false, "v2", false, ""); err == nil {
		t.Fatal("expected duplicate basename rejection")
	}
	m, err := manifest.Load(filepath.Join(dataDir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Entries) != 1 || m.Entries[0].Path != first || m.Entries[0].Repo != "owner/first" {
		t.Fatalf("existing entry was overwritten: %+v", m.Entries)
	}
	backup, err := os.ReadFile(filepath.Join(dataDir, "store", "samebin", "original", "samebin"))
	if err != nil || string(backup) != "first\n" {
		t.Fatalf("original backup was overwritten: %q err=%v", backup, err)
	}
}

func TestE2E_AdoptLocalStillRunsOwnershipGate(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("HUKOU_DATA_DIR", dataDir)
	binPath := writeExecutable(t, t.TempDir(), "managed", "body\n")
	var out bytes.Buffer
	called := false
	err := doAdoptWithDeps(&out, &out, binPath, "", true, "local", false, "", func(got string) (*provenance.Attribution, error) {
		called = true
		if got != binPath {
			t.Fatalf("gate path = %s, want %s", got, binPath)
		}
		return &provenance.Attribution{Source: "brew", Evidence: "test ownership"}, nil
	}, saveManifest)
	if !called || err == nil {
		t.Fatalf("local adoption bypassed ownership gate: called=%v err=%v", called, err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "manifest.json")); !os.IsNotExist(err) {
		t.Fatalf("manifest created despite rejection: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "store")); !os.IsNotExist(err) {
		t.Fatalf("store created despite rejection: %v", err)
	}
}

func TestFindChecksumAssetSkipsSig(t *testing.T) {
	assets := []ghrelease.Asset{
		{Name: "checksums.txt.sig"},
		{Name: "checksums.txt.asc"},
		{Name: "checksums.pem"},
		{Name: "checksums.txt"},
	}
	got := findChecksumAsset("tool.tar.gz", assets)
	if got != "checksums.txt" {
		t.Fatalf("got %q", got)
	}
}

func TestFindChecksumAssetPrefersExactSidecar(t *testing.T) {
	assets := []ghrelease.Asset{
		{Name: "checksums.txt"},
		{Name: "tool.tar.gz.SHA256"},
	}
	if got := findChecksumAsset("tool.tar.gz", assets); got != "tool.tar.gz.SHA256" {
		t.Fatalf("got %q", got)
	}
}

func directoryEntryNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, len(entries))
	for i, entry := range entries {
		names[i] = entry.Name()
	}
	return names
}

func writeExecutable(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o755); err != nil {
		t.Fatalf("write executable: %v", err)
	}
	return p
}

func platformAssetName(binName string) string {
	goos := runtime.GOOS
	goarch := runtime.GOARCH
	ext := "tar.gz"
	if goos == "windows" {
		ext = "zip"
	}
	return fmt.Sprintf("%s-%s-%s.%s", binName, goos, goarch, ext)
}

func makeTarGz(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	hdr := &tar.Header{
		Name: name,
		Mode: 0o755,
		Size: int64(len(content)),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatalf("tar header: %v", err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatalf("tar write: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

func assetChecksumLine(assetName string, assetData []byte) string {
	return fmt.Sprintf("%x  %s\n", sha256.Sum256(assetData), assetName)
}

func fakeGitHubServer(t *testing.T, assetName string, assetData []byte, checksum string, tag string) *httptest.Server {
	t.Helper()
	if tag == "" {
		tag = "v2.0.0"
	}
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Accept legacy latest lookup and the V0.3 bounded release-list lookup.
		if strings.HasSuffix(r.URL.Path, "/releases/latest") || strings.HasSuffix(r.URL.Path, "/releases") {
			assets := []map[string]any{
				{
					"name":                 assetName,
					"browser_download_url": server.URL + "/assets/" + assetName,
					"size":                 len(assetData),
				},
			}
			if checksum != "" {
				assets = append(assets, map[string]any{
					"name":                 "checksums.txt",
					"browser_download_url": server.URL + "/assets/checksums.txt",
					"size":                 len(checksum),
				})
			}
			writeFakeReleaseMetadata(t, w, r, map[string]any{
				"tag_name": tag,
				"assets":   assets,
			})
			return
		}
		switch r.URL.Path {
		case "/assets/" + assetName:
			_, _ = w.Write(assetData)
		case "/assets/checksums.txt":
			_, _ = w.Write([]byte(checksum))
		default:
			http.NotFound(w, r)
		}
	}))
	return server
}

func fakeGitHubServerWithChecksumAsset(t *testing.T, assetName string, assetData []byte, checksumName, checksum, tag string) *httptest.Server {
	t.Helper()
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/releases/latest") || strings.HasSuffix(r.URL.Path, "/releases") {
			writeFakeReleaseMetadata(t, w, r, map[string]any{
				"tag_name": tag,
				"assets": []map[string]any{
					{"name": assetName, "browser_download_url": server.URL + "/assets/" + assetName, "size": len(assetData)},
					{"name": checksumName, "browser_download_url": server.URL + "/assets/" + checksumName, "size": len(checksum)},
				},
			})
			return
		}
		switch r.URL.Path {
		case "/assets/" + assetName:
			_, _ = w.Write(assetData)
		case "/assets/" + checksumName:
			_, _ = w.Write([]byte(checksum))
		default:
			http.NotFound(w, r)
		}
	}))
	return server
}

func writeFakeReleaseMetadata(t *testing.T, w http.ResponseWriter, r *http.Request, release any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	value := release
	if strings.HasSuffix(r.URL.Path, "/releases") {
		value = []any{release}
	}
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Errorf("encode release metadata: %v", err)
	}
}

func testGHClient(server *httptest.Server) *ghrelease.Client {
	return &ghrelease.Client{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
		Sleep:      func(time.Duration) {},
	}
}

// makeTarGzMulti builds a tar.gz holding multiple executable members.
func makeTarGzMulti(t *testing.T, members map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	names := make([]string, 0, len(members))
	for name := range members {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		content := members[name]
		hdr := &tar.Header{
			Name: name,
			Mode: 0o755,
			Size: int64(len(content)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("tar header: %v", err)
		}
		if _, err := tw.Write(content); err != nil {
			t.Fatalf("tar write: %v", err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

func TestE2E_UpgradeUsesArchiveExeWhenNamesDiffer(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("HUKOU_DATA_DIR", dataDir)
	binPath := writeExecutable(t, t.TempDir(), "renamed-tool", "v1-body\n")
	var out bytes.Buffer
	if err := doAdopt(&out, &out, binPath, "owner/repo", false, "v1.0.0", false, "realtool"); err != nil {
		t.Fatal(err)
	}
	m, err := manifest.Load(filepath.Join(dataDir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got := m.Get("renamed-tool").ArchiveExe; got != "realtool" {
		t.Fatalf("archive_exe not recorded: %q", got)
	}

	// The archive carries two executables: the real tool and a helper. Without
	// the recorded archive_exe this is the "multiple executable candidates"
	// fail-closed case; with it, the upgrade must select "realtool" exactly.
	assetName := platformAssetName("renamed-tool")
	assetData := makeTarGzMulti(t, map[string][]byte{
		"realtool": []byte("v2-body\n"),
		"helper":   []byte("helper-body\n"),
	})
	checksum := assetChecksumLine(assetName, assetData)
	server := fakeGitHubServer(t, assetName, assetData, checksum, "v2.0.0")
	defer server.Close()

	if err := doUpgrade(&out, &out, []string{"renamed-tool"}, false, false, "", testGHClient(server), false); err != nil {
		t.Fatalf("upgrade with archive_exe failed: %v\noutput: %s", err, out.String())
	}
	got, err := os.ReadFile(binPath)
	if err != nil || string(got) != "v2-body\n" {
		t.Fatalf("live binary wrong after upgrade: body=%q err=%v", got, err)
	}
	// The store artifact must sit under the TOOL name (not the archive-internal
	// name), so doctor's store checks and rollback lookups stay consistent.
	report := doctor.Scan(doctor.Options{DataRoot: dataDir})
	if !report.Healthy() {
		var msgs []string
		for _, f := range report.Findings {
			msgs = append(msgs, f.Code+": "+f.Message)
		}
		t.Fatalf("doctor unhealthy after archive_exe upgrade: %s", strings.Join(msgs, "; "))
	}
	storeArtifact := filepath.Join(dataDir, "store", "renamed-tool", "v2.0.0", "renamed-tool")
	if _, err := os.Stat(storeArtifact); err != nil {
		t.Fatalf("store artifact not under tool name: %v", err)
	}
}

func TestE2E_DryRunShowsReleaseNotesAndMajorWarning(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("HUKOU_DATA_DIR", dataDir)
	binPath := writeExecutable(t, t.TempDir(), "fakebin", "v1-body\n")
	var out bytes.Buffer
	if err := doAdopt(&out, &out, binPath, "owner/repo", false, "v1.0.0", false, ""); err != nil {
		t.Fatal(err)
	}
	assetName := platformAssetName("fakebin")
	assetData := makeTarGz(t, "fakebin", []byte("v2-body\n"))
	checksum := assetChecksumLine(assetName, assetData)
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/releases/latest") || strings.HasSuffix(r.URL.Path, "/releases") {
			writeFakeReleaseMetadata(t, w, r, map[string]any{
				"tag_name": "v2.0.0",
				"body":     "### What's changed\n\n- feature A\n- fix B\n",
				"assets": []map[string]any{
					{"name": assetName, "browser_download_url": server.URL + "/assets/" + assetName, "size": len(assetData)},
					{"name": "checksums.txt", "browser_download_url": server.URL + "/assets/checksums.txt", "size": len(checksum)},
				},
			})
			return
		}
		switch r.URL.Path {
		case "/assets/" + assetName:
			_, _ = w.Write(assetData)
		case "/assets/checksums.txt":
			_, _ = w.Write([]byte(checksum))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	var dry bytes.Buffer
	if err := doUpgrade(&dry, &dry, []string{"fakebin"}, false, true, "", testGHClient(server), false); err != nil {
		t.Fatalf("dry-run failed: %v\n%s", err, dry.String())
	}
	text := dry.String()
	if !strings.Contains(text, "release notes:") || !strings.Contains(text, "- feature A") {
		t.Fatalf("release notes missing:\n%s", text)
	}
	if !strings.Contains(text, "major version jump") {
		t.Fatalf("major warning missing:\n%s", text)
	}
	if !strings.Contains(text, "Would upgrade fakebin: v1.0.0 -> v2.0.0") {
		t.Fatalf("upgrade line missing:\n%s", text)
	}
}
