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
	first := strings.Repeat("a", 64)
	second := strings.Repeat("b", 64)
	path := writeChecksums(t, "# comment\n\n"+first+"  file.tar.gz\n"+second+"  other.zip\n")
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	got, err := ParseChecksums(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 entries, got %d: %v", len(got), got)
	}
	if got["file.tar.gz"] != first {
		t.Fatalf("unexpected hash: %v", got)
	}
	if got["other.zip"] != second {
		t.Fatalf("unexpected hash: %v", got)
	}
}

func TestParseChecksumsBSD(t *testing.T) {
	first := strings.Repeat("a", 64)
	second := strings.Repeat("b", 64)
	path := writeChecksums(t, "SHA256 (file.tar.gz) = "+first+"\n# ignored\nSHA256 (other.zip) = "+second+"\n")
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	got, err := ParseChecksums(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 entries, got %d: %v", len(got), got)
	}
	if got["file.tar.gz"] != first {
		t.Fatalf("unexpected hash: %v", got)
	}
	if got["other.zip"] != second {
		t.Fatalf("unexpected hash: %v", got)
	}
}

func TestParseChecksumsGNUStarMarker(t *testing.T) {
	hash := strings.Repeat("a", 64)
	got, err := ParseChecksums(strings.NewReader(hash + " *file.tar.gz\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got["file.tar.gz"] != hash {
		t.Fatalf("unexpected checksums: %v", got)
	}
}

func TestParseChecksumSidecarBareDigest(t *testing.T) {
	hash := strings.Repeat("a", 64)
	got, err := ParseChecksumSidecar(strings.NewReader("# generated\n"+hash+"\n"), "tool.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	if got["tool.tar.gz"] != hash {
		t.Fatalf("unexpected checksums: %v", got)
	}
}

func TestParseChecksumSidecarRejectsMultipleBareDigests(t *testing.T) {
	input := strings.Repeat("a", 64) + "\n" + strings.Repeat("b", 64) + "\n"
	if got, err := ParseChecksumSidecar(strings.NewReader(input), "tool.tar.gz"); err == nil {
		t.Fatalf("expected multiple-entry error, got %v", got)
	}
}

func TestParseChecksumsGenericRejectsBareDigest(t *testing.T) {
	if got, err := ParseChecksums(strings.NewReader(strings.Repeat("a", 64) + "\n")); err == nil {
		t.Fatalf("generic checksum file must retain a file name, got %v", got)
	}
}

func TestParseChecksumsLowercases(t *testing.T) {
	upper := strings.Repeat("ABCDEF12", 8)
	path := writeChecksums(t, upper+"  file\n")
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	got, err := ParseChecksums(f)
	if err != nil {
		t.Fatal(err)
	}
	if got["file"] != strings.ToLower(upper) {
		t.Fatalf("expected lowercased hash, got %v", got)
	}
}

func TestParseChecksumsRejectsInvalidDigest(t *testing.T) {
	for _, line := range []string{
		"abc123  file.tar.gz\n",
		strings.Repeat("g", 64) + "  file.tar.gz\n",
		strings.Repeat("a", 64) + "  \n",
	} {
		t.Run(strings.TrimSpace(line), func(t *testing.T) {
			if got, err := ParseChecksums(strings.NewReader(line)); err == nil {
				t.Fatalf("expected parse error, got %v", got)
			}
		})
	}
}

func TestParseChecksumsPropagatesScannerError(t *testing.T) {
	line := strings.Repeat("a", 64) + "  " + strings.Repeat("x", 70*1024) + "\n"
	if got, err := ParseChecksums(strings.NewReader(line)); err == nil {
		t.Fatalf("expected scanner error, got %v", got)
	} else if !strings.Contains(err.Error(), "scan checksums") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseChecksumsRejectsConflictingDuplicate(t *testing.T) {
	input := strings.Repeat("a", 64) + "  file.tar.gz\n" +
		strings.Repeat("b", 64) + "  file.tar.gz\n"
	if got, err := ParseChecksums(strings.NewReader(input)); err == nil {
		t.Fatalf("expected conflict error, got %v", got)
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

func TestVerifyAssetRejectsInvalidExpectedDigest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "asset.tar.gz")
	if err := os.WriteFile(path, []byte("asset body"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := VerifyAsset(path, "asset.tar.gz", map[string]string{"asset.tar.gz": "not-a-sha256"})
	if err == nil || !strings.Contains(err.Error(), "invalid SHA-256 digest") {
		t.Fatalf("expected invalid digest error, got %v", err)
	}
}
