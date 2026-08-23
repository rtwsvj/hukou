package verify

import (
	"strings"
	"testing"
)

// TestParseAPIDigest covers the fail-closed extraction contract for GitHub
// API asset digest metadata: only the "sha256:" prefix is recognized this
// round, the remainder must be a valid 64-character SHA-256 hex string, and
// every other shape yields ok=false so callers treat it as absent evidence.
func TestParseAPIDigest(t *testing.T) {
	validHex := strings.Repeat("ab", 32)
	tests := []struct {
		name string
		raw  string
		want string
		ok   bool
	}{
		{name: "valid sha256 lowercase", raw: "sha256:" + validHex, want: validHex, ok: true},
		{name: "valid sha256 mixed case normalized", raw: "SHA256:" + strings.ToUpper(validHex), want: validHex, ok: true},
		{name: "unknown algorithm prefix refused", raw: "md5:" + strings.Repeat("a", 32), want: "", ok: false},
		{name: "sha512 prefix refused this round", raw: "sha512:" + strings.Repeat("a", 128), want: "", ok: false},
		{name: "missing separator refused", raw: validHex, want: "", ok: false},
		{name: "empty input refused", raw: "", want: "", ok: false},
		{name: "prefix only refused", raw: "sha256:", want: "", ok: false},
		{name: "short hex refused", raw: "sha256:" + strings.Repeat("a", 63), want: "", ok: false},
		{name: "long hex refused", raw: "sha256:" + strings.Repeat("a", 65), want: "", ok: false},
		{name: "non-hex characters refused", raw: "sha256:" + strings.Repeat("z", 64), want: "", ok: false},
		{name: "whitespace inside refused", raw: "sha256: " + validHex, want: "", ok: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParseAPIDigest(tt.raw)
			if ok != tt.ok {
				t.Fatalf("ParseAPIDigest(%q) ok = %v, want %v", tt.raw, ok, tt.ok)
			}
			if got != tt.want {
				t.Fatalf("ParseAPIDigest(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}
