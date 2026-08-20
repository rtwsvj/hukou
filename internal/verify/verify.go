// Package verify provides SHA-256 checksum parsing and asset verification.
package verify

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"github.com/rtwsvj/hukou/internal/i18n"
	"github.com/rtwsvj/hukou/internal/safeopen"
	"io"
	"strings"
)

// ErrNoChecksum is returned by VerifyAsset when the checksum map does not
// contain an entry for the requested asset.
var ErrNoChecksum = i18n.Errorf("no checksum entry for asset")

// SHA256File returns the hex-encoded SHA-256 digest of the file at path. The
// open goes through safeopen so a FIFO/device swapped in after a caller's
// stat fails closed instead of blocking the hash forever.
func SHA256File(path string) (string, error) {
	f, err := safeopen.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

// ParseChecksums reads a GNU/BSD-style checksums file and returns a map from
// file name to lowercase hex digest.
//
// Supported line formats:
//
//	<hex>  <filename>
//	<hex> *<filename>
//	SHA256 (<filename>) = <hex>
//
// Empty lines and lines starting with '#' are ignored. Every other line must
// contain a valid 64-character SHA-256 digest and a non-empty file name.
func ParseChecksums(r io.Reader) (map[string]string, error) {
	return parseChecksums(r, "")
}

// ParseChecksumSidecar accepts the strict named formats supported by
// ParseChecksums and, for an exact <asset>.sha256 sidecar only, a single bare
// 64-character digest. The caller supplies the asset name that the exact
// sidecar is already bound to.
func ParseChecksumSidecar(r io.Reader, assetName string) (map[string]string, error) {
	if strings.TrimSpace(assetName) == "" {
		return nil, i18n.Errorf("checksum sidecar asset name is empty")
	}
	return parseChecksums(r, assetName)
}

func parseChecksums(r io.Reader, digestOnlyAsset string) (map[string]string, error) {
	out := make(map[string]string)
	sc := bufio.NewScanner(r)
	lineNumber := 0
	bareDigestSeen := false
	for sc.Scan() {
		lineNumber++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		if bareDigestSeen {
			return nil, i18n.Errorf("checksums line %d: digest-only sidecar contains multiple entries", lineNumber)
		}

		if name, hash, ok := parseBSDLine(line); ok {
			if !validSHA256Hex(hash) {
				return nil, i18n.Errorf("checksums line %d: invalid SHA-256 digest %q", lineNumber, hash)
			}
			if err := putChecksum(out, name, strings.ToLower(hash), lineNumber); err != nil {
				return nil, err
			}
			continue
		}

		separator := strings.IndexAny(line, " \t")
		if separator < 0 {
			hash := strings.ToLower(line)
			if digestOnlyAsset != "" && validSHA256Hex(hash) && len(out) == 0 {
				out[digestOnlyAsset] = hash
				bareDigestSeen = true
				continue
			}
			return nil, i18n.Errorf("checksums line %d: missing file name", lineNumber)
		}
		hash := strings.ToLower(line[:separator])
		if !validSHA256Hex(hash) {
			return nil, i18n.Errorf("checksums line %d: invalid SHA-256 digest %q", lineNumber, line[:separator])
		}

		name := strings.TrimSpace(line[separator:])
		if strings.HasPrefix(name, "*") {
			name = strings.TrimSpace(name[1:])
		}
		if name == "" {
			return nil, i18n.Errorf("checksums line %d: missing file name", lineNumber)
		}
		if err := putChecksum(out, name, hash, lineNumber); err != nil {
			return nil, err
		}
	}
	if err := sc.Err(); err != nil {
		return nil, i18n.Wrapf("scan checksums: %w", err)
	}
	return out, nil
}

func parseBSDLine(line string) (name, hash string, ok bool) {
	const prefix = "SHA256 ("
	if len(line) < len(prefix) || !strings.EqualFold(line[:len(prefix)], prefix) {
		return "", "", false
	}
	separator := strings.LastIndex(line, ") = ")
	if separator < len(prefix) {
		return "", "", false
	}
	name = strings.TrimSpace(line[len(prefix):separator])
	hash = strings.TrimSpace(line[separator+len(") = "):])
	return name, hash, name != ""
}

func putChecksum(out map[string]string, name, hash string, lineNumber int) error {
	if name == "" {
		return i18n.Errorf("checksums line %d: missing file name", lineNumber)
	}
	if existing, ok := out[name]; ok && existing != hash {
		return i18n.Errorf("checksums line %d: conflicting SHA-256 digest for %q", lineNumber, name)
	}
	out[name] = hash
	return nil
}

func validSHA256Hex(digest string) bool {
	if len(digest) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(digest)
	return err == nil
}

// VerifyAsset checks assetPath against the checksum entry for assetName in the
// checksum map. It returns ErrNoChecksum if assetName is not present, or an
// error describing the mismatch if the digest does not match.
//
// Error priority is part of the contract and matches the original
// implementation exactly: a missing checksum entry (ErrNoChecksum) and an
// invalid published digest are both reported before the asset file is opened,
// so a file-read problem can never mask the map-level answer. Only after both
// map-level checks pass is the asset hashed, and only then can a read error or
// a content mismatch be returned.
func VerifyAsset(assetPath, assetName string, checksums map[string]string) error {
	want, ok := checksums[assetName]
	if !ok {
		return ErrNoChecksum
	}
	if !validSHA256Hex(want) {
		return i18n.Errorf("invalid SHA-256 digest for %s: %q", assetName, want)
	}
	got, err := SHA256File(assetPath)
	if err != nil {
		return err
	}
	return VerifyAssetDigest(got, assetName, checksums)
}

// VerifyAssetDigest performs the publisher-checksum comparison against a
// digest the caller has already computed for the asset. It exists so a caller
// that must also record the asset SHA-256 can hash the asset once and reuse the
// result here, instead of re-reading the whole file inside VerifyAsset. The
// fail-closed semantics match VerifyAsset's map-level contract: an absent entry
// returns ErrNoChecksum, an unparsable published digest is an error, and any
// mismatch is an error.
func VerifyAssetDigest(assetDigest, assetName string, checksums map[string]string) error {
	want, ok := checksums[assetName]
	if !ok {
		return ErrNoChecksum
	}
	if !validSHA256Hex(want) {
		return i18n.Errorf("invalid SHA-256 digest for %s: %q", assetName, want)
	}
	if !strings.EqualFold(assetDigest, want) {
		return i18n.Errorf("checksum mismatch for %s: got %s, want %s", assetName, assetDigest, want)
	}
	return nil
}
