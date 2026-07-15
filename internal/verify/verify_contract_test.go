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

func sha256Hex(data string) string {
	sum := sha256.Sum256([]byte(data))
	return fmt.Sprintf("%x", sum)
}

// TestVerifyAssetErrorPriority pins the exact error-ordering contract of the
// original implementation: both map-level answers (missing entry, invalid
// published digest) are decided before the asset file is ever opened, so a
// missing or unreadable file can never mask them. Only a valid map entry lets
// a file-read error or a content mismatch surface.
func TestVerifyAssetErrorPriority(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing-asset")
	payloadSHA := sha256Hex("payload")

	t.Run("missing file, missing checksum entry: ErrNoChecksum wins", func(t *testing.T) {
		err := VerifyAsset(missing, "asset", map[string]string{"other": payloadSHA})
		if !errors.Is(err, ErrNoChecksum) {
			t.Fatalf("err = %v, want ErrNoChecksum", err)
		}
		if errors.Is(err, os.ErrNotExist) {
			t.Fatalf("file-read error must not surface before the map-level answer: %v", err)
		}
	})

	t.Run("missing file, invalid checksum entry: invalid-digest wins", func(t *testing.T) {
		err := VerifyAsset(missing, "asset", map[string]string{"asset": "not-a-digest"})
		if err == nil {
			t.Fatal("invalid published digest must fail")
		}
		if errors.Is(err, ErrNoChecksum) || errors.Is(err, os.ErrNotExist) {
			t.Fatalf("wrong error class: %v", err)
		}
		if !strings.Contains(err.Error(), "invalid SHA-256 digest") {
			t.Fatalf("err = %v, want invalid SHA-256 digest error", err)
		}
	})

	t.Run("missing file, valid checksum entry: read error surfaces", func(t *testing.T) {
		err := VerifyAsset(missing, "asset", map[string]string{"asset": payloadSHA})
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("err = %v, want os.ErrNotExist", err)
		}
	})

	t.Run("readable file, valid checksum entry: mismatch surfaces", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "asset")
		if err := os.WriteFile(path, []byte("different"), 0o644); err != nil {
			t.Fatal(err)
		}
		err := VerifyAsset(path, "asset", map[string]string{"asset": payloadSHA})
		if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
			t.Fatalf("err = %v, want checksum mismatch", err)
		}
	})

	t.Run("readable file, matching digest: success", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "asset")
		if err := os.WriteFile(path, []byte("payload"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := VerifyAsset(path, "asset", map[string]string{"asset": strings.ToUpper(payloadSHA)}); err != nil {
			t.Fatalf("VerifyAsset = %v, want nil (case-insensitive match)", err)
		}
	})
}

// TestVerifyAssetDigestContract pins the map-level contract of the digest
// variant used by cmd/upgrade.go: identical sentinel and error classes as
// VerifyAsset for every case that does not involve reading the file.
func TestVerifyAssetDigestContract(t *testing.T) {
	payloadSHA := sha256Hex("payload")

	if err := VerifyAssetDigest(payloadSHA, "asset", map[string]string{"other": payloadSHA}); !errors.Is(err, ErrNoChecksum) {
		t.Fatalf("missing entry: err = %v, want ErrNoChecksum", err)
	}
	if err := VerifyAssetDigest(payloadSHA, "asset", map[string]string{"asset": "not-a-digest"}); err == nil ||
		errors.Is(err, ErrNoChecksum) || !strings.Contains(err.Error(), "invalid SHA-256 digest") {
		t.Fatalf("invalid entry: err = %v, want invalid SHA-256 digest error", err)
	}
	if err := VerifyAssetDigest(sha256Hex("different"), "asset", map[string]string{"asset": payloadSHA}); err == nil ||
		!strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("mismatch: err = %v, want checksum mismatch", err)
	}
	if err := VerifyAssetDigest(strings.ToUpper(payloadSHA), "asset", map[string]string{"asset": payloadSHA}); err != nil {
		t.Fatalf("case-insensitive match: err = %v, want nil", err)
	}
}
