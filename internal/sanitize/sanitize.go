// Package sanitize strips control characters from untrusted text before it
// reaches a terminal. It is a leaf package (no hukou imports) so every layer
// — the GitHub client at ingestion, the output renderers, the cmd layer —
// shares ONE implementation instead of growing per-package copies.
package sanitize

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Terminal strips C0/C1 control characters from untrusted multi-line text
// (release notes, server error bodies) so ANSI/OSC escape sequences cannot
// spoof trusted output, clear the screen, or reach the clipboard via OSC 52.
// Newline and tab survive; every other non-printable rune, ESC included,
// becomes '?'.
func Terminal(s string) string {
	if s == "" {
		return s
	}
	return strings.Map(func(r rune) rune {
		if r == utf8.RuneError {
			return '?'
		}
		if r == '\n' || r == '\t' {
			return r
		}
		if !unicode.IsPrint(r) {
			return '?'
		}
		return r
	}, s)
}
