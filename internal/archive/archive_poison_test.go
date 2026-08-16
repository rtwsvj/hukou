package archive

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildTarGzStream writes entries to a gzipped tar with full control over
// headers (typeflag, linkname, size) — including hostile ones.
func buildTarGzStream(t *testing.T, entries []tar.Header, bodies [][]byte) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "hostile.tar.gz")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	w := tar.NewWriter(gz)
	for i, h := range entries {
		if err := w.WriteHeader(&h); err != nil {
			t.Fatal(err)
		}
		if i < len(bodies) && bodies[i] != nil {
			if _, err := w.Write(bodies[i]); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func regularHeader(name string, size int64, mode int64) tar.Header {
	return tar.Header{Name: name, Mode: mode, Size: size, Typeflag: tar.TypeReg}
}

func TestExtractPoisonArchiveBombs(t *testing.T) {
	// 600 MiB of zeros compresses to ~600 KiB; extraction must stop at the
	// 512 MiB total budget with a clean error, not OOM and not leave a giant
	// file behind.
	zero := make([]byte, 1<<20)
	// Rebuild with a streaming body writer: simplest is to write zeros directly.
	dir := t.TempDir()
	path := filepath.Join(dir, "bomb.tar.gz")
	f, _ := os.Create(path)
	gz := gzip.NewWriter(f)
	w := tar.NewWriter(gz)
	if err := w.WriteHeader(&tar.Header{Name: "tool", Mode: 0o755, Size: 600 << 20, Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 600; i++ {
		if _, err := w.Write(zero); err != nil {
			t.Fatal(err)
		}
	}
	_ = w.Close()
	_ = gz.Close()
	_ = f.Close()

	dest := t.TempDir()
	_, err := Extract(path, dest, "tool")
	if err == nil || !strings.Contains(err.Error(), "size limit") {
		t.Fatalf("expected size-limit error, got %v", err)
	}
}

func TestExtractPoisonTraversalAndAbsolute(t *testing.T) {
	for _, name := range []string{"../evil", "/abs/evil", "a/../../evil"} {
		path := buildTarGzStream(t, []tar.Header{regularHeader(name, 4, 0o755)}, [][]byte{[]byte("evil")})
		dest := t.TempDir()
		if _, err := Extract(path, dest, "evil"); err == nil {
			t.Fatalf("traversal %q: expected rejection", name)
		}
		if _, err := os.Stat(filepath.Join(dest, "evil")); !os.IsNotExist(err) {
			t.Fatalf("traversal %q wrote outside dest", name)
		}
	}
}

func TestExtractPoisonSymlinkHardlinkAndDirs(t *testing.T) {
	entries := []tar.Header{
		regularHeader("tool", 4, 0o755),
		{Name: "link", Mode: 0o755, Size: 0, Typeflag: tar.TypeLink, Linkname: "tool"},
		{Name: "sym", Mode: 0o777, Size: 0, Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"},
		{Name: "deep/" + strings.Repeat("d/", 99), Mode: 0o755, Size: 0, Typeflag: tar.TypeDir},
	}
	path := buildTarGzStream(t, entries, [][]byte{[]byte("tool")})
	dest := t.TempDir()
	out, err := Extract(path, dest, "tool")
	if err != nil {
		t.Fatalf("extract failed: %v", err)
	}
	got, _ := os.ReadFile(out)
	if string(got) != "tool" {
		t.Fatalf("wrong binary extracted: %q", got)
	}
	if _, err := os.Stat(filepath.Join(dest, "sym")); !os.IsNotExist(err) {
		t.Fatalf("symlink entry was materialized: %v", err)
	}
}

func TestExtractPoisonTruncatedAndEmpty(t *testing.T) {
	// truncated gzip
	dir := t.TempDir()
	path := filepath.Join(dir, "trunc.tar.gz")
	if err := os.WriteFile(path, []byte{0x1f, 0x8b, 0x08, 0x00, 0x01, 0x02}, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Extract(path, dir, "tool"); err == nil {
		t.Fatal("truncated gzip must fail")
	}
	// empty archive: no binary
	empty := buildTarGzStream(t, nil, nil)
	if _, err := Extract(empty, dir, "tool"); err == nil || !strings.Contains(err.Error(), "no binary found") {
		t.Fatalf("empty archive: expected no-binary error, got %v", err)
	}
	// raw bytes that are neither gzip/zip/anything
	junk := filepath.Join(dir, "junk.gz")
	_ = os.WriteFile(junk, bytes.Repeat([]byte{0x00}, 64), 0o644)
	if _, err := Extract(junk, dir, "tool"); err == nil {
		t.Fatal("junk stream must fail")
	}
}
