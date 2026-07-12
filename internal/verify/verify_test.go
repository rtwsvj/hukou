package verify

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeChecksums(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "checksums.txt")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func shaOf(content string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(content)))
}

func TestParseChecksumsGNU(t *testing.T) {
	path := writeChecksums(t, "# comment\n\nabc123  file.tar.gz\ndef456  other.zip\n")
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	got := ParseChecksums(f)
	if len(got) != 2 {
		t.Fatalf("want 2 entries, got %d: %v", len(got), got)
	}
	if got["file.tar.gz"] != "abc123" {
		t.Fatalf("unexpected hash: %v", got)
	}
	if got["other.zip"] != "def456" {
		t.Fatalf("unexpected hash: %v", got)
	}
}

func TestParseChecksumsBSD(t *testing.T) {
	path := writeChecksums(t, "abc123 *file.tar.gz\n# ignored\ndef456  *other.zip\n")
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	got := ParseChecksums(f)
	if len(got) != 2 {
		t.Fatalf("want 2 entries, got %d: %v", len(got), got)
	}
	if got["file.tar.gz"] != "abc123" {
		t.Fatalf("unexpected hash: %v", got)
	}
	if got["other.zip"] != "def456" {
		t.Fatalf("unexpected hash: %v", got)
	}
}

func TestParseChecksumsLowercases(t *testing.T) {
	path := writeChecksums(t, "ABCDEF  file\n")
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	got := ParseChecksums(f)
	if got["file"] != "abcdef" {
		t.Fatalf("expected lowercased hash, got %v", got)
	}
}

func TestSHA256File(t *testing.T) {
	content := "hello world"
	path := filepath.Join(t.TempDir(), "asset")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := SHA256File(path)
	if err != nil {
		t.Fatal(err)
	}
	want := shaOf(content)
	if got != want {
		t.Fatalf("want %s, got %s", want, got)
	}
}

func TestVerifyAssetHit(t *testing.T) {
	content := "asset body"
	path := filepath.Join(t.TempDir(), "asset.tar.gz")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	checksums := map[string]string{
		"asset.tar.gz": shaOf(content),
	}
	if err := VerifyAsset(path, "asset.tar.gz", checksums); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyAssetMiss(t *testing.T) {
	content := "asset body"
	path := filepath.Join(t.TempDir(), "asset.tar.gz")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	checksums := map[string]string{
		"asset.tar.gz": strings.Repeat("0", 64),
	}
	err := VerifyAsset(path, "asset.tar.gz", checksums)
	if err == nil {
		t.Fatal("expected mismatch error")
	}
	if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestVerifyAssetNoEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "asset.tar.gz")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	checksums := map[string]string{}
	err := VerifyAsset(path, "asset.tar.gz", checksums)
	if !errors.Is(err, ErrNoChecksum) {
		t.Fatalf("want ErrNoChecksum, got %v", err)
	}
}

func TestVerifyAssetCaseInsensitive(t *testing.T) {
	content := "asset body"
	path := filepath.Join(t.TempDir(), "asset.tar.gz")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	checksums := map[string]string{
		"asset.tar.gz": strings.ToUpper(shaOf(content)),
	}
	if err := VerifyAsset(path, "asset.tar.gz", checksums); err != nil {
		t.Fatal(err)
	}
}
