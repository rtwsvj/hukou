// Package verify provides SHA-256 checksum parsing and asset verification.
package verify

import (
	"bufio"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// ErrNoChecksum is returned by VerifyAsset when the checksum map does not
// contain an entry for the requested asset.
var ErrNoChecksum = errors.New("no checksum entry for asset")

// SHA256File returns the hex-encoded SHA-256 digest of the file at path.
func SHA256File(path string) (string, error) {
	f, err := os.Open(path)
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
//   <hex>  <filename>
//   <hex> *<filename>
//
// Empty lines and lines starting with '#' are ignored.
func ParseChecksums(r io.Reader) map[string]string {
	out := make(map[string]string)
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		hash := strings.ToLower(fields[0])
		name := fields[1]
		if strings.HasPrefix(name, "*") {
			name = name[1:]
		}
		if hash == "" || name == "" {
			continue
		}
		out[name] = hash
	}
	return out
}

// VerifyAsset checks assetPath against the checksum entry for assetName in the
// checksum map. It returns ErrNoChecksum if assetName is not present, or an
// error describing the mismatch if the digest does not match.
func VerifyAsset(assetPath, assetName string, checksums map[string]string) error {
	want, ok := checksums[assetName]
	if !ok {
		return ErrNoChecksum
	}
	got, err := SHA256File(assetPath)
	if err != nil {
		return err
	}
	if !strings.EqualFold(got, want) {
		return fmt.Errorf("checksum mismatch for %s: got %s, want %s", assetName, got, want)
	}
	return nil
}
