package cmd

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rtwsvj/hukou/internal/archive"
	"github.com/rtwsvj/hukou/internal/manifest"
	"github.com/rtwsvj/hukou/internal/sysproxy"
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

// TestExtractReadsOnlyThroughProvidedHandle: Extract must consume the asset
// exclusively through the caller's descriptor. A writer-side swap mid-read
// (the pipe turns to garbage after a valid gzip+tar prefix) surfaces as a
// read error inside extraction and aborts it; nothing is re-opened by any
// pathname, so there is no verify→extract window left to attack.
func TestExtractReadsOnlyThroughProvidedHandle(t *testing.T) {
	// Valid tar.gz prefix: real gzip stream with one complete member, so the
	// reader gets past format detection and into the tar body before the
	// sabotage kicks in.
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	hdr := &tar.Header{Name: "fakebin", Mode: 0o755, Size: int64(len("payload")), Typeflag: tar.TypeReg}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("payload")); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}

	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pr.Close()
	defer pw.Close()
	go func() {
		_, _ = pw.Write(buf.Bytes())
		// Mid-read tamper: everything after the valid archive is garbage.
		_, _ = pw.Write(bytes.Repeat([]byte{0xFF}, 4096))
		pw.Close()
	}()
	// os.File.Name is fixed at creation, so rebind the read end of the pipe
	// under an explicit .tar.gz name: DetectFormat routes it into the
	// multi-pass tar path while reads still come from the tampered pipe.
	pr = os.NewFile(pr.Fd(), "tampered.tar.gz")
	dest := t.TempDir()
	if _, err := archive.Extract(pr, dest, "fakebin"); err == nil {
		t.Fatal("extraction over a tampered handle succeeded; want a read error")
	}
}

// TestUpgradeRejectsAssetTamperedMidExtraction drives the real upgrade flow
// end to end (adopt, fake GitHub release server, doUpgrade) with the asset
// served over an HTTP body that is corrupted partway through. The download
// completes but the stored bytes cannot match what the publisher checksum
// demands, so verification refuses before anything is extracted or stored:
// the tampered payload never reaches the store, neither the live binary nor
// the manifest may change.
func TestUpgradeRejectsAssetTamperedMidExtraction(t *testing.T) {
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
	// The publisher checksum describes the intact asset; the server serves a
	// body whose tail was corrupted in flight.
	corrupt := append([]byte{}, assetData[:len(assetData)/2]...)
	corrupt = append(corrupt, bytes.Repeat([]byte{0xFF}, len(assetData)-len(corrupt))...)
	checksum := fmt.Sprintf("%x  %s\n", sha256.Sum256(assetData), assetName)
	// Pin the "system proxy" away: this host has a real macOS system proxy,
	// so a transient transport failure would otherwise trigger ghrelease's
	// proxy retry against 127.0.0.1:7890 (which refuses), turning the
	// expected checksum refusal into a connection error. Also close the
	// server only after the upgrade attempt finishes.
	origProxy := sysproxy.SystemProxyURL
	sysproxy.SystemProxyURL = func() *url.URL { return nil }
	t.Cleanup(func() { sysproxy.SystemProxyURL = origProxy })

	server := fakeGitHubServerWithBody(t, assetName, corrupt, checksum, "v2.0.0")
	t.Cleanup(server.Close)
	client := testGHClient(server)
	out.Reset()
	err = doUpgrade(&out, &out, []string{"fakebin"}, false, false, "", client, false)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("upgrade error = %v, want publisher-checksum mismatch refusal:\n%s", err, out.String())
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

// fakeGitHubServerWithBody serves one release whose asset body is supplied
// verbatim (so a test can corrupt it in flight) while the published checksum
// file stays independent of whatever was actually served. It mirrors
// fakeGitHubServer's routing; only the served asset bytes differ.
func fakeGitHubServerWithBody(t *testing.T, assetName string, body []byte, checksum string, tag string) *httptest.Server {
	t.Helper()
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/releases/latest") || strings.HasSuffix(r.URL.Path, "/releases") {
			assets := []map[string]any{
				{
					"name":                 assetName,
					"browser_download_url": server.URL + "/assets/" + assetName,
					"size":                 len(body),
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
			_, _ = w.Write(body)
		case "/assets/checksums.txt":
			_, _ = w.Write([]byte(checksum))
		default:
			http.NotFound(w, r)
		}
	}))
	return server
}
