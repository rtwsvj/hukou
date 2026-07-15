package store_test

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/rtwsvj/hukou/internal/store"
)

func hexSHA256(data string) string {
	sum := sha256.Sum256([]byte(data))
	return hex.EncodeToString(sum[:])
}

// TestPutWithDigestFreshVersion covers the fresh-copy path: the returned
// digest must be the content SHA-256 of both the source and the immutable
// artifact the store actually committed.
func TestPutWithDigestFreshVersion(t *testing.T) {
	root := t.TempDir()
	s := &store.Store{Root: root}
	const body = "fresh-version-body"
	src := writeFile(t, t.TempDir(), "mybin", body)

	digest, err := s.PutWithDigest("tool", "v1.0", src)
	if err != nil {
		t.Fatalf("PutWithDigest: %v", err)
	}
	if digest != hexSHA256(body) {
		t.Fatalf("digest = %s, want %s", digest, hexSHA256(body))
	}

	stored := filepath.Join(root, "tool", "v1.0", "mybin")
	storedSHA, err := store.SHA256File(stored)
	if err != nil {
		t.Fatalf("hash stored artifact: %v", err)
	}
	if storedSHA != digest {
		t.Fatalf("stored artifact digest = %s, returned digest = %s", storedSHA, digest)
	}
}

// TestPutWithDigestExistingVersion covers the idempotent existing-version
// path: a repeated Put of identical bytes succeeds and returns the digest of
// the artifact already in the store (computed from the destination, not
// assumed from the source).
func TestPutWithDigestExistingVersion(t *testing.T) {
	root := t.TempDir()
	s := &store.Store{Root: root}
	const body = "existing-version-body"
	src := writeFile(t, t.TempDir(), "mybin", body)

	first, err := s.PutWithDigest("tool", "v1.0", src)
	if err != nil {
		t.Fatalf("first PutWithDigest: %v", err)
	}
	same := writeFile(t, t.TempDir(), "mybin", body)
	second, err := s.PutWithDigest("tool", "v1.0", same)
	if err != nil {
		t.Fatalf("idempotent PutWithDigest: %v", err)
	}
	if second != first || second != hexSHA256(body) {
		t.Fatalf("existing-version digest = %s, want %s", second, first)
	}
}

// TestPutWithDigestExistingVersionDifferentContent proves the immutability
// guard is unchanged: conflicting bytes for an existing version fail closed
// and no digest is vouched for.
func TestPutWithDigestExistingVersionDifferentContent(t *testing.T) {
	root := t.TempDir()
	s := &store.Store{Root: root}
	src := writeFile(t, t.TempDir(), "mybin", "version-one")
	if _, err := s.PutWithDigest("tool", "v1.0", src); err != nil {
		t.Fatalf("PutWithDigest: %v", err)
	}
	other := writeFile(t, t.TempDir(), "mybin", "version-two")
	digest, err := s.PutWithDigest("tool", "v1.0", other)
	if err == nil {
		t.Fatal("conflicting content for an existing version must fail")
	}
	if digest != "" {
		t.Fatalf("failed PutWithDigest must not vouch for a digest, got %q", digest)
	}
	// The original artifact is untouched.
	stored := filepath.Join(root, "tool", "v1.0", "mybin")
	got, readErr := os.ReadFile(stored)
	if readErr != nil || string(got) != "version-one" {
		t.Fatalf("existing version damaged by rejected Put: %q err=%v", got, readErr)
	}
}
