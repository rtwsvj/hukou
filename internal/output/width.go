package output

import (
	"unicode"
	"unicode/utf8"
)

// DisplayWidth returns the terminal display width of s: East Asian wide and
// fullwidth runes count as two columns, combining marks and zero-width joiners
// count as zero, everything else counts as one. This is a pragmatic wcwidth
// subset covering the CJK alignment problem; it is intentionally conservative
// (unlisted runes count as one).
func DisplayWidth(s string) int {
	w := 0
	for _, r := range s {
		w += runeDisplayWidth(r)
	}
	return w
}

func runeDisplayWidth(r rune) int {
	switch {
	case r == utf8.RuneError:
		return 1
	case unicode.Is(unicode.Mn, r), unicode.Is(unicode.Me, r), r == '\u200d':
		return 0
	case isWideRune(r):
		return 2
	default:
		return 1
	}
}

// isWideRune reports East Asian Wide (W) / Fullwidth (F) runes using the
// standard EAW range table (the same simplification used by small wcwidth
// ports). Halfwidth forms are deliberately width 1.
func isWideRune(r rune) bool {
	return r >= 0x1100 && (r <= 0x115f ||
		(r >= 0x2e80 && r <= 0xa4cf && r != 0x303f) ||
		(r >= 0xac00 && r <= 0xd7a3) ||
		(r >= 0xf900 && r <= 0xfaff) ||
		(r >= 0xfe10 && r <= 0xfe19) ||
		(r >= 0xfe30 && r <= 0xfe6f) ||
		(r >= 0xff00 && r <= 0xff60) ||
		(r >= 0xffe0 && r <= 0xffe6) ||
		(r >= 0x20000 && r <= 0x2fffd) ||
		(r >= 0x30000 && r <= 0x3fffd))
}

// TruncateDisplay shortens s to at most max display columns without splitting
// a rune; shortened results end with "...". It replaces the byte-based
// truncate for table cells so CJK text gets the same visual budget.
func TruncateDisplay(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if DisplayWidth(s) <= max {
		return s
	}
	if max <= 3 {
		return fitDisplayWidth(s, max)
	}
	return fitDisplayWidth(s, max-3) + "..."
}

func fitDisplayWidth(s string, max int) string {
	if max <= 0 {
		return ""
	}
	w := 0
	for i, r := range s {
		rw := runeDisplayWidth(r)
		if w+rw > max {
			return s[:i]
		}
		w += rw
	}
	return s
}
