package archive

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func makeTarGz(t *testing.T, entries []struct {
	name string
	body string
	mode int64
}) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "archive.tar.gz")

	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	gz := gzip.NewWriter(f)
	defer gz.Close()

	w := tar.NewWriter(gz)
	defer w.Close()

	for _, e := range entries {
		h := &tar.Header{
			Name:     e.name,
			Mode:     e.mode,
			Size:     int64(len(e.body)),
			Typeflag: tar.TypeReg,
		}
		if err := w.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func makeZip(t *testing.T, entries []struct {
	name string
	body string
	mode int64
}) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "archive.zip")

	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	w := zip.NewWriter(f)
	defer w.Close()

	for _, e := range entries {
		h := &zip.FileHeader{
			Name:   e.name,
			Method: zip.Deflate,
		}
		h.SetMode(os.FileMode(e.mode))
		fw, err := w.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func makeGz(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "single.gz")

	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	gz := gzip.NewWriter(f)
	if _, err := gz.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestExtractTarGzExactMatch(t *testing.T) {
	archive := makeTarGz(t, []struct {
		name string
		body string
		mode int64
	}{
		{"README.md", "docs", 0o644},
		{"mytool", "binary data", 0o755},
	})

	dest := t.TempDir()
	out, err := Extract(archive, dest, "mytool")
	if err != nil {
		t.Fatal(err)
	}
	if got := filepath.Base(out); got != "mytool" {
		t.Fatalf("want mytool, got %s", got)
	}
	if got := readFile(t, out); got != "binary data" {
		t.Fatalf("unexpected content: %q", got)
	}
}

func TestExtractTarGzExactExe(t *testing.T) {
	archive := makeTarGz(t, []struct {
		name string
		body string
		mode int64
	}{
		{"mytool.exe", "windows binary", 0o755},
	})

	dest := t.TempDir()
	out, err := Extract(archive, dest, "mytool")
	if err != nil {
		t.Fatal(err)
	}
	if got := filepath.Base(out); got != "mytool.exe" {
		t.Fatalf("want mytool.exe, got %s", got)
	}
}

func TestExtractTarGzHeuristicExecutable(t *testing.T) {
	archive := makeTarGz(t, []struct {
		name string
		body string
		mode int64
	}{
		{"README.md", "docs", 0o644},
		{"bin/some-cli", "cli data", 0o755},
	})

	dest := t.TempDir()
	out, err := Extract(archive, dest, "wanted")
	if err != nil {
		t.Fatal(err)
	}
	if got := filepath.Base(out); got != "some-cli" {
		t.Fatalf("want some-cli, got %s", got)
	}
	if got := readFile(t, out); got != "cli data" {
		t.Fatalf("unexpected content: %q", got)
	}
}

func TestExtractTarGzHeuristicNoExtension(t *testing.T) {
	archive := makeTarGz(t, []struct {
		name string
		body string
		mode int64
	}{
		{"README.md", "docs", 0o644},
		{"mybinary", "data", 0o644},
	})

	dest := t.TempDir()
	out, err := Extract(archive, dest, "wanted")
	if err != nil {
		t.Fatal(err)
	}
	if got := filepath.Base(out); got != "mybinary" {
		t.Fatalf("want mybinary, got %s", got)
	}
}

func TestExtractTarGzMultipleCandidates(t *testing.T) {
	archive := makeTarGz(t, []struct {
		name string
		body string
		mode int64
	}{
		{"bin/a", "a", 0o755},
		{"bin/b", "b", 0o755},
	})

	dest := t.TempDir()
	_, err := Extract(archive, dest, "wanted")
	if err == nil {
		t.Fatal("expected error for multiple candidates")
	}
	if !strings.Contains(err.Error(), "multiple executable candidates") {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(err.Error(), "bin/a") || !strings.Contains(err.Error(), "bin/b") {
		t.Fatalf("expected candidate names in error, got: %v", err)
	}
}

func TestExtractTarGzPathTraversalRejected(t *testing.T) {
	archive := makeTarGz(t, []struct {
		name string
		body string
		mode int64
	}{
		{"../../escape", "bad", 0o755},
	})

	dest := t.TempDir()
	_, err := Extract(archive, dest, "wanted")
	if err == nil {
		t.Fatal("expected path traversal error")
	}
	if !strings.Contains(err.Error(), "path traversal") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExtractTarGzAbsolutePathRejected(t *testing.T) {
	archive := makeTarGz(t, []struct {
		name string
		body string
		mode int64
	}{
		{"/etc/passwd", "bad", 0o755},
	})

	dest := t.TempDir()
	_, err := Extract(archive, dest, "wanted")
	if err == nil {
		t.Fatal("expected absolute path error")
	}
	if !strings.Contains(err.Error(), "absolute archive entry") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExtractZipExactMatch(t *testing.T) {
	archive := makeZip(t, []struct {
		name string
		body string
		mode int64
	}{
		{"mytool", "zip binary", 0o755},
	})

	dest := t.TempDir()
	out, err := Extract(archive, dest, "mytool")
	if err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, out); got != "zip binary" {
		t.Fatalf("unexpected content: %q", got)
	}
}

func TestExtractZipMultipleCandidates(t *testing.T) {
	archive := makeZip(t, []struct {
		name string
		body string
		mode int64
	}{
		{"a", "a", 0o755},
		{"b", "b", 0o755},
	})

	dest := t.TempDir()
	_, err := Extract(archive, dest, "wanted")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "multiple executable candidates") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExtractZipPathTraversalRejected(t *testing.T) {
	archive := makeZip(t, []struct {
		name string
		body string
		mode int64
	}{
		{"../escape", "bad", 0o755},
	})

	dest := t.TempDir()
	_, err := Extract(archive, dest, "wanted")
	if err == nil {
		t.Fatal("expected path traversal error")
	}
	if !strings.Contains(err.Error(), "path traversal") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExtractGzRenames(t *testing.T) {
	archive := makeGz(t, "gzipped binary")
	dest := t.TempDir()

	out, err := Extract(archive, dest, "renamed-tool")
	if err != nil {
		t.Fatal(err)
	}
	if got := filepath.Base(out); got != "renamed-tool" {
		t.Fatalf("want renamed-tool, got %s", got)
	}
	if got := readFile(t, out); got != "gzipped binary" {
		t.Fatalf("unexpected content: %q", got)
	}
}

func TestExtractBareFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "raw")
	if err := os.WriteFile(src, []byte("plain binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	dest := t.TempDir()
	out, err := Extract(src, dest, "rawtool")
	if err != nil {
		t.Fatal(err)
	}
	if got := filepath.Base(out); got != "rawtool" {
		t.Fatalf("want rawtool, got %s", got)
	}
	if got := readFile(t, out); got != "plain binary" {
		t.Fatalf("unexpected content: %q", got)
	}
}

func TestExtractTarXzUnsupported(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "archive.tar.xz")
	if err := os.WriteFile(path, []byte("not real xz"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Extract(path, t.TempDir(), "tool")
	if err == nil {
		t.Fatal("expected xz error")
	}
	if !strings.Contains(err.Error(), "xz 暂不支持") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSafeJoin(t *testing.T) {
	dest := t.TempDir()
	cases := []struct {
		name    string
		target  string
		wantErr bool
	}{
		{"plain", "foo/bar", false},
		{"dotdot inside", "foo/../bar", false},
		{"escape", "../bar", true},
		{"absolute", "/etc/passwd", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := safeJoin(dest, tc.target)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q", tc.target)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !strings.HasPrefix(got, dest) {
				t.Fatalf("result %q not under dest", got)
			}
		})
	}
}

func TestExtractDoesNotAllowZipSlip(t *testing.T) {
	// zip slip: archive contains "../foo" and we must reject extraction.
	archive := makeZip(t, []struct {
		name string
		body string
		mode int64
	}{
		{"../foo", "bad", 0o755},
	})
	_, err := Extract(archive, t.TempDir(), "foo")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestExtractTarGzNoBinary(t *testing.T) {
	archive := makeTarGz(t, []struct {
		name string
		body string
		mode int64
	}{
		{"README.md", "docs", 0o644},
	})

	dest := t.TempDir()
	_, err := Extract(archive, dest, "wanted")
	if err == nil {
		t.Fatal("expected no binary error")
	}
	if !strings.Contains(err.Error(), "no binary found") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// Ensure non-regular tar entries are skipped without confusion.
func TestExtractTarGzSkipsNonRegular(t *testing.T) {
	t.Helper()

	// Build archive with a directory entry before the file.
	dir := t.TempDir()
	path := filepath.Join(dir, "archive.tar.gz")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	w := tar.NewWriter(gz)

	dh := &tar.Header{
		Name:     "dir/",
		Mode:     0o755,
		Typeflag: tar.TypeDir,
	}
	if err := w.WriteHeader(dh); err != nil {
		t.Fatal(err)
	}

	fh := &tar.Header{
		Name:     "dir/myfile",
		Mode:     0o755,
		Size:     int64(len("content")),
		Typeflag: tar.TypeReg,
	}
	if err := w.WriteHeader(fh); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("content")); err != nil {
		t.Fatal(err)
	}
	w.Close()
	gz.Close()
	f.Close()

	dest := t.TempDir()
	out, err := Extract(path, dest, "wanted")
	if err != nil {
		t.Fatal(err)
	}
	if got := filepath.Base(out); got != "myfile" {
		t.Fatalf("want myfile, got %s", got)
	}
}

// compile-time check that error handling around readers stays clean.
func TestExtractGzNotGzip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "not.gz")
	if err := os.WriteFile(path, []byte("not gzipped"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Extract(path, t.TempDir(), "tool")
	if err == nil {
		t.Fatal("expected gzip error")
	}
}

func TestBaseMatches(t *testing.T) {
	if !baseMatches("tool", "tool") {
		t.Fatal("expected match")
	}
	if !baseMatches("tool.exe", "tool") {
		t.Fatal("expected exe match")
	}
	if baseMatches("tool.exe", "other") {
		t.Fatal("expected no match")
	}
}

func TestIsExecutableLike(t *testing.T) {
	if !isExecutableLike("bin", 0o644) {
		t.Fatal("no-extension file should be executable-like")
	}
	if !isExecutableLike("bin.sh", 0o755) {
		t.Fatal("executable bit should count")
	}
	if isExecutableLike("bin.sh", 0o644) {
		t.Fatal("script without exec bit should not count")
	}
}

// drain helper to satisfy any unused-import checks (io used by tests).
var _ = io.ReadAll
var _ = errors.New
var _ = bytes.NewReader
