package verify

import (
	"strings"
	"testing"
)

// Poison matrix for checksum parsing: publisher-controlled files that are
// malformed, hostile, or in alien formats must fail closed — never parse
// successfully by accident, never panic, never allocate unboundedly.
func TestParseChecksumsPoison(t *testing.T) {
	h1 := strings.Repeat("ab", 32)
	h2 := strings.Repeat("cd", 32)
	cases := []struct {
		name    string
		input   string
		wantErr string // substring; "" means must succeed
	}{
		{"plain", h1 + "  tool.tar.gz\n", ""},
		{"no-trailing-newline", h1 + "  tool.tar.gz", ""},
		{"crlf", h1 + "  tool.tar.gz\r\n", ""},
		{"tab-separator", h1 + "\ttool.tar.gz\n", ""},
		{"uppercase-hex", strings.ToUpper(h1) + "  tool.tar.gz\n", ""},
		{"blank-and-comment-lines", "\n# comment\n" + h1 + "  tool.tar.gz\n\n", ""},
		{"duplicate-same-digest", h1 + "  tool.tar.gz\n" + h1 + "  tool.tar.gz\n", ""},
		{"duplicate-conflicting", h1 + "  tool.tar.gz\n" + h2 + "  tool.tar.gz\n", "conflicting"},
		{"missing-filename", h1 + "\n", "missing file name"},
		{"gnu-format", "SHA256 (tool.tar.gz) = " + h1 + "\n", ""}, // GNU coreutils format is supported intentionally
		{"gnu-format-bad-digest", "SHA256 (tool.tar.gz) = zzzz\n", "invalid SHA-256 digest"},
		{"md5-format", "MD5 (tool.tar.gz) = " + h1 + "\n", "invalid SHA-256 digest"},
		{"bom-prefix", "\ufeff" + h1 + "  tool.tar.gz\n", "invalid SHA-256 digest"},
		{"huge-line", strings.Repeat("a", 1<<20) + "\n", "too long"}, // scanner token limit
		{"invalid-hex", strings.Repeat("g", 64) + "  tool.tar.gz\n", "invalid SHA-256 digest"},
		{"short-hex", "abc" + "  tool.tar.gz\n", "invalid SHA-256 digest"},
		{"empty-file", "", ""},
		{"only-comments", "# nothing here\n", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, err := ParseChecksums(strings.NewReader(tc.input))
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if len(m) == 0 && tc.input != "" && !strings.HasPrefix(strings.TrimSpace(tc.input), "#") && strings.TrimSpace(tc.input) != "" {
					t.Fatalf("empty result for non-empty input: %q", tc.input)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got success (%v)", tc.wantErr, m)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestParseChecksumSidecarPoison(t *testing.T) {
	h := strings.Repeat("ab", 32)
	if _, err := ParseChecksumSidecar(strings.NewReader(h), "tool.tar.gz"); err != nil {
		t.Fatalf("bare digest sidecar must parse: %v", err)
	}
	if _, err := ParseChecksumSidecar(strings.NewReader(h+"\n"+h), "tool.tar.gz"); err == nil {
		t.Fatal("multi-entry digest-only sidecar must fail")
	}
	if _, err := ParseChecksumSidecar(strings.NewReader(h+"  tool.tar.gz\n"), ""); err == nil {
		t.Fatal("empty sidecar asset name must fail")
	}
}
