package archive

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"fmt"
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
	_, err := Extract(openArchive(t, path), dest, "tool")
	if err == nil || !strings.Contains(err.Error(), "size limit") {
		t.Fatalf("expected size-limit error, got %v", err)
	}
}

func TestExtractPoisonTraversalAndAbsolute(t *testing.T) {
	for _, name := range []string{"../evil", "/abs/evil", "a/../../evil"} {
		path := buildTarGzStream(t, []tar.Header{regularHeader(name, 4, 0o755)}, [][]byte{[]byte("evil")})
		dest := t.TempDir()
		if _, err := Extract(openArchive(t, path), dest, "evil"); err == nil {
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
	out, err := Extract(openArchive(t, path), dest, "tool")
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
	if _, err := Extract(openArchive(t, path), dir, "tool"); err == nil {
		t.Fatal("truncated gzip must fail")
	}
	// empty archive: no binary
	empty := buildTarGzStream(t, nil, nil)
	if _, err := Extract(openArchive(t, empty), dir, "tool"); err == nil || !strings.Contains(err.Error(), "no binary found") {
		t.Fatalf("empty archive: expected no-binary error, got %v", err)
	}
	// raw bytes that are neither gzip/zip/anything
	junk := filepath.Join(dir, "junk.gz")
	_ = os.WriteFile(junk, bytes.Repeat([]byte{0x00}, 64), 0o644)
	if _, err := Extract(openArchive(t, junk), dir, "tool"); err == nil {
		t.Fatal("junk stream must fail")
	}
}

func TestExtractPoisonTarMemberBudget(t *testing.T) {
	// 20001 zero-size regular entries: discovery must refuse on the member
	// budget instead of walking an unbounded table of contents.
	var headers []tar.Header
	for i := 0; i < MaxHeaderEntries+1; i++ {
		headers = append(headers, regularHeader(fmt.Sprintf("f%05d.txt", i), 0, 0o644))
	}
	path := buildTarGzStream(t, headers, nil)

	dest := t.TempDir()
	_, err := Extract(openArchive(t, path), dest, "tool")
	if err == nil || !strings.Contains(err.Error(), "member budget") {
		t.Fatalf("expected member-budget error, got %v", err)
	}
}

func TestExtractPoisonTarCandidateBudget(t *testing.T) {
	// More executable-like entries than the candidate cap: refuse instead of
	// materializing a giant ambiguity report.
	var headers []tar.Header
	for i := 0; i < MaxExecCandidates+1; i++ {
		headers = append(headers, regularHeader(fmt.Sprintf("bin%03d", i), 0, 0o755))
	}
	path := buildTarGzStream(t, headers, nil)

	dest := t.TempDir()
	_, err := Extract(openArchive(t, path), dest, "tool")
	if err == nil || !strings.Contains(err.Error(), "candidate budget") {
		t.Fatalf("expected candidate-budget error, got %v", err)
	}
}

// rawUstarHeader builds a single 512-byte ustar header block by hand, so a
// hostile archive can declare an arbitrary Size without ever writing the
// matching body (the standard library writer refuses to do that).
func rawUstarHeader(name string, size int64, mode int64) []byte {
	blk := make([]byte, 512)
	copy(blk[0:100], name)
	copy(blk[100:108], fmt.Sprintf("%07o\x00", mode))
	copy(blk[108:116], fmt.Sprintf("%07o\x00", 0)) // uid
	copy(blk[116:124], fmt.Sprintf("%07o\x00", 0)) // gid
	copy(blk[124:136], fmt.Sprintf("%011o\x00", size))
	copy(blk[136:148], fmt.Sprintf("%011o\x00", 0)) // mtime
	for i := 148; i < 156; i++ {
		blk[i] = ' ' // checksum placeholder
	}
	blk[156] = '0' // TypeReg
	copy(blk[257:263], "ustar\x00")
	copy(blk[263:265], "00")
	sum := 0
	for _, c := range blk {
		sum += int(c)
	}
	copy(blk[148:154], fmt.Sprintf("%06o", sum))
	return blk
}

func TestExtractPoisonTarDeclaredSizeBudget(t *testing.T) {
	// A header declaring more than the cumulative declared-size budget must
	// be refused from metadata alone, before any decompressed body byte is
	// read (no 5 GiB body exists behind the header at all).
	dir := t.TempDir()
	path := filepath.Join(dir, "hostile.tar.gz")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	if _, err := gz.Write(rawUstarHeader("tool", 5<<30, 0o755)); err != nil {
		t.Fatal(err)
	}
	if _, err := gz.Write(make([]byte, 1024)); err != nil { // end-of-archive marker
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	dest := t.TempDir()
	_, err = Extract(openArchive(t, path), dest, "tool")
	if err == nil || !strings.Contains(err.Error(), "declared-size budget") {
		t.Fatalf("expected declared-size-budget error, got %v", err)
	}
}

func TestExtractPoisonZipDeclaredSizeBudget(t *testing.T) {
	// A zip whose central directory declares a single 5 GiB entry must fail
	// the cumulative declared-size check before any body is read.
	dir := t.TempDir()
	path := filepath.Join(dir, "hostile.zip")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	h := &zip.FileHeader{Name: "tool", Method: zip.Store}
	h.CompressedSize64 = 0
	h.UncompressedSize64 = 5 << 30 // declared, not real
	if _, err := w.CreateRaw(h); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	dest := t.TempDir()
	_, err = Extract(openArchive(t, path), dest, "tool")
	if err == nil || !strings.Contains(err.Error(), "declared-size budget") {
		t.Fatalf("expected declared-size-budget error, got %v", err)
	}
}
