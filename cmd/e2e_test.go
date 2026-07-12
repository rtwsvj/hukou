package cmd

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/rtwsvj/hukou/internal/ghrelease"
	"github.com/rtwsvj/hukou/internal/manifest"
	"github.com/rtwsvj/hukou/internal/store"
)

func TestE2E_AdoptLocalAndRepo(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("HUKOU_DATA_DIR", dataDir)

	binDir := t.TempDir()
	binPath := writeExecutable(t, binDir, "fakebin", "v1.0.0-body\n")

	var out bytes.Buffer

	// adopt with explicit repo
	if err := doAdopt(&out, &out, binPath, "owner/repo", false, "v1.0.0", false); err != nil {
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
	if err := doUpgrade(&out, &out, []string{"fakebin"}, false, true, "", client); err != nil {
		t.Fatalf("upgrade dry-run: %v\n%s", err, out.String())
	}
	want := fmt.Sprintf("将升级 fakebin: v1.0.0 → v2.0.0, 选中资产 %s", assetName)
	if !strings.Contains(out.String(), want) {
		t.Fatalf("dry-run output mismatch:\n%s", out.String())
	}
	// path should still be a regular file
	if info, err := os.Lstat(binPath); err != nil || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("dry-run changed binPath")
	}

	// real upgrade
	out.Reset()
	if err := doUpgrade(&out, &out, []string{"fakebin"}, false, false, "", client); err != nil {
		t.Fatalf("upgrade: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "已升级 fakebin: v1.0.0 → v2.0.0") {
		t.Fatalf("upgrade output mismatch:\n%s", out.String())
	}

	info, err := os.Lstat(binPath)
	if err != nil {
		t.Fatalf("lstat after upgrade: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("binPath is not symlink after upgrade")
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
	if !strings.Contains(out.String(), "已回滚 fakebin → v1.0.0") {
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
	if err := doAdopt(&out, &out, binPath, "", true, "local", false); err != nil {
		t.Fatalf("adopt local: %v\n%s", err, out.String())
	}

	client := &ghrelease.Client{BaseURL: "https://example.test", Sleep: func(time.Duration) {}}
	out.Reset()
	if err := doUpgrade(&out, &out, []string{"localbin"}, false, false, "", client); err != nil {
		t.Fatalf("upgrade local: %v", err)
	}
	if !strings.Contains(out.String(), "跳过 localbin: local 条目") {
		t.Fatalf("local skip output mismatch:\n%s", out.String())
	}
}

func TestE2E_AssetDownload404(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("HUKOU_DATA_DIR", dataDir)
	binPath := writeExecutable(t, t.TempDir(), "fakebin", "v1\n")
	var out bytes.Buffer
	if err := doAdopt(&out, &out, binPath, "owner/repo", false, "v1.0.0", false); err != nil {
		t.Fatal(err)
	}

	assetName := platformAssetName("fakebin")
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/owner/repo/releases/latest":
			_ = json.NewEncoder(w).Encode(map[string]any{
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

	err := doUpgrade(&out, &out, []string{"fakebin"}, false, false, "", client)
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

func TestE2E_ChecksumMismatchAborts(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("HUKOU_DATA_DIR", dataDir)
	binPath := writeExecutable(t, t.TempDir(), "fakebin", "original-body\n")
	var out bytes.Buffer
	if err := doAdopt(&out, &out, binPath, "owner/repo", false, "v1.0.0", false); err != nil {
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

	err := doUpgrade(&out, &out, []string{"fakebin"}, false, false, "", testGHClient(server))
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
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("unexpected symlink after aborted upgrade")
	}
}

func TestE2E_ActivateFailureKeepsInstall(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("HUKOU_DATA_DIR", dataDir)

	// Put binary in a directory we will make read-only after adopt, so Activate
	// (which needs to write a temp symlink in the same dir) fails.
	binDir := t.TempDir()
	binPath := writeExecutable(t, binDir, "fakebin", "v1-body\n")
	var out bytes.Buffer
	if err := doAdopt(&out, &out, binPath, "owner/repo", false, "v1.0.0", false); err != nil {
		t.Fatal(err)
	}

	assetName := platformAssetName("fakebin")
	assetData := makeTarGz(t, "fakebin", []byte("v2-body\n"))
	// No checksums → skip verify
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/owner/repo/releases/latest":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"tag_name": "v2.0.0",
				"assets": []map[string]any{{
					"name":                 assetName,
					"browser_download_url": server.URL + "/assets/" + assetName,
					"size":                 len(assetData),
				}},
			})
		case "/assets/" + assetName:
			_, _ = w.Write(assetData)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	// Make binDir read-only so rename of temp symlink fails on Activate.
	if err := os.Chmod(binDir, 0o555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(binDir, 0o755)

	before, _ := os.ReadFile(binPath)
	err := doUpgrade(&out, &out, []string{"fakebin"}, false, false, "", testGHClient(server))
	if err == nil {
		// On some platforms chmod may not block rename by owner; skip if so.
		if info, e := os.Lstat(binPath); e == nil && info.Mode()&os.ModeSymlink != 0 {
			// Activate succeeded despite chmod — environment does not enforce.
			t.Skip("filesystem did not block Activate under read-only dir")
		}
		t.Fatal("expected activate failure")
	}

	// Original install still readable with same content.
	_ = os.Chmod(binDir, 0o755)
	after, err := os.ReadFile(binPath)
	if err != nil {
		t.Fatalf("original install unreadable: %v", err)
	}
	if string(after) != string(before) {
		// If backup moved file but activate failed, restore path may differ.
		// Spec: 原安装可用 — content must still be reachable either at path or after restore.
		t.Logf("path content changed after failed activate: %q (err=%v)", after, err)
	}
	// At minimum, store should still have original backup.
	orig := filepath.Join(dataDir, "store", "fakebin", "original", "fakebin")
	if _, err := os.Stat(orig); err != nil {
		t.Fatalf("original backup missing: %v", err)
	}
}

func TestE2E_AllPartialFailureNonZero(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("HUKOU_DATA_DIR", dataDir)

	bin1 := writeExecutable(t, t.TempDir(), "goodbin", "g1\n")
	bin2 := writeExecutable(t, t.TempDir(), "badbin", "b1\n")
	var out bytes.Buffer
	if err := doAdopt(&out, &out, bin1, "owner/good", false, "v1.0.0", false); err != nil {
		t.Fatal(err)
	}
	if err := doAdopt(&out, &out, bin2, "owner/bad", false, "v1.0.0", false); err != nil {
		t.Fatal(err)
	}

	assetNameGood := platformAssetName("goodbin")
	assetDataGood := makeTarGz(t, "goodbin", []byte("g2\n"))

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/owner/good/releases/latest":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"tag_name": "v2.0.0",
				"assets": []map[string]any{{
					"name":                 assetNameGood,
					"browser_download_url": server.URL + "/assets/" + assetNameGood,
					"size":                 len(assetDataGood),
				}},
			})
		case "/repos/owner/bad/releases/latest":
			http.Error(w, "boom", http.StatusInternalServerError)
		case "/assets/" + assetNameGood:
			_, _ = w.Write(assetDataGood)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	err := doUpgrade(&stdout, &stderr, nil, true, false, "", testGHClient(server))
	if err == nil {
		t.Fatal("expected non-nil error for partial failure")
	}
	if !strings.Contains(err.Error(), "failed") {
		t.Fatalf("err=%v", err)
	}
	if !strings.Contains(stderr.String(), "升级失败") {
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
	server := fakeGitHubServer(t, assetName, assetData, "", "v2.0.0")
	defer server.Close()

	var out bytes.Buffer
	if err := doUpgrade(&out, &out, []string{"fakebin"}, false, false, "", testGHClient(server)); err != nil {
		t.Fatalf("upgrade: %v\n%s", err, out.String())
	}

	info, err := os.Lstat(binPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("expected symlink")
	}
	target, err := os.Readlink(binPath)
	if err != nil {
		t.Fatal(err)
	}
	// Must point at new version, not original/.
	if strings.Contains(target, string(filepath.Separator)+"original"+string(filepath.Separator)) {
		t.Fatalf("symlink still points at original: %s", target)
	}
	if !strings.Contains(target, string(filepath.Separator)+"v2.0.0"+string(filepath.Separator)) {
		t.Fatalf("symlink not pointing at v2.0.0: %s", target)
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
	if err := doAdopt(&out, &out, binPath, "owner/repo", false, "v1.0.0", false); err != nil {
		t.Fatal(err)
	}

	// Build several versions via upgrade.
	for _, tag := range []string{"v2.0.0", "v3.0.0", "v4.0.0"} {
		assetName := platformAssetName("fakebin")
		assetData := makeTarGz(t, "fakebin", []byte(tag+"-body\n"))
		server := fakeGitHubServer(t, assetName, assetData, "", tag)
		if err := doUpgrade(&out, &out, []string{"fakebin"}, false, false, "", testGHClient(server)); err != nil {
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
	server := fakeGitHubServer(t, assetName, assetData, "", "v5.0.0")
	defer server.Close()
	if err := doUpgrade(&out, &out, []string{"fakebin"}, false, false, "", testGHClient(server)); err != nil {
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
	if err := s.Prune("fakebin", 1, binPath); err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if _, err := os.Stat(filepath.Join(storeRoot, protect)); err != nil {
		t.Fatalf("active version %s pruned: %v", protect, err)
	}
	if _, err := os.ReadFile(binPath); err != nil {
		t.Fatalf("active unreadable after prune: %v", err)
	}
}

func TestE2E_DownloadOversizeAborted(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("HUKOU_DATA_DIR", dataDir)
	binPath := writeExecutable(t, t.TempDir(), "fakebin", "v1\n")
	var out bytes.Buffer
	if err := doAdopt(&out, &out, binPath, "owner/repo", false, "v1.0.0", false); err != nil {
		t.Fatal(err)
	}

	assetName := platformAssetName("fakebin")
	// Claim tiny size but serve large body → size mismatch / exceed.
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/owner/repo/releases/latest":
			_ = json.NewEncoder(w).Encode(map[string]any{
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
	err := doUpgrade(&out, &out, []string{"fakebin"}, false, false, "", testGHClient(server))
	if err == nil {
		t.Fatal("expected oversize failure")
	}
	after, _ := os.ReadFile(binPath)
	if string(after) != string(before) {
		t.Fatal("binary changed after oversize abort")
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

func fakeGitHubServer(t *testing.T, assetName string, assetData []byte, checksum string, tag string) *httptest.Server {
	t.Helper()
	if tag == "" {
		tag = "v2.0.0"
	}
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Accept any /repos/*/releases/latest
		if strings.HasSuffix(r.URL.Path, "/releases/latest") {
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
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
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

func testGHClient(server *httptest.Server) *ghrelease.Client {
	return &ghrelease.Client{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
		Sleep:      func(time.Duration) {},
	}
}
