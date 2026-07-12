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
	server := fakeGitHubServer(t, assetName, assetData, checksum)
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
	if !strings.Contains(out.String(), "已升级 fakebin: v2.0.0 → v2.0.0") {
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

func fakeGitHubServer(t *testing.T, assetName string, assetData []byte, checksum string) *httptest.Server {
	t.Helper()
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/owner/repo/releases/latest":
			release := map[string]any{
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
			}
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(release); err != nil {
				t.Fatalf("encode release: %v", err)
			}
		case "/assets/" + assetName:
			if _, err := w.Write(assetData); err != nil {
				t.Fatalf("write asset: %v", err)
			}
		case "/assets/checksums.txt":
			if _, err := w.Write([]byte(checksum)); err != nil {
				t.Fatalf("write checksums: %v", err)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	return server
}
