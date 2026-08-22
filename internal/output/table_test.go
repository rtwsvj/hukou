package output

import (
	"bytes"
	"strings"
	"testing"
)

func TestDisplayWidth(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"abc", 3},
		{"我的工具", 8},
		{"a中b", 4},
		{"🦀crab", 5},   // emoji is unlisted: conservative width 1 per rune
		{"a\u0301", 1}, // combining acute over 'a' is zero-width
	}
	for _, tc := range cases {
		if got := DisplayWidth(tc.in); got != tc.want {
			t.Errorf("DisplayWidth(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestTruncateDisplay(t *testing.T) {
	// 你好世界 is 8 columns; a 4-column budget leaves 1 column of body, which
	// cannot hold a 2-column rune — the honest result is just the ellipsis.
	if got := TruncateDisplay("你好世界", 4); got != "..." {
		t.Fatalf("wide fit failed: %q", got)
	}
	if got := TruncateDisplay("hello世界", 8); got != "hello..." {
		t.Fatalf("wide ellipsis failed: %q", got)
	}
	if got := TruncateDisplay("abc", 3); got != "abc" {
		t.Fatalf("exact fit failed: %q", got)
	}
	if got := TruncateDisplay("abcdef", 0); got != "" {
		t.Fatalf("zero budget failed: %q", got)
	}
}

func TestTableAlignsCJKWideCells(t *testing.T) {
	var buf bytes.Buffer
	tr := NewTable(&buf, "名称", "版本")
	tr.Row("我的工具", "v1.0.0")
	tr.Row("rg", "v2.0.0")
	if err := tr.Flush(); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("want 3 lines, got %d: %q", len(lines), buf.String())
	}
	// Column 2 must start at the same display column in every line: header
	// "名称" is 4 columns wide, "我的工具" is 8, "rg" is 2 → data column
	// starts at 8+2=10 for all rows. Measure with DisplayWidth (byte indexes
	// are meaningless for CJK text).
	const wantCol = 10
	for i, line := range lines {
		marker := "版本"
		if i > 0 {
			marker = "v"
		}
		idx := strings.Index(line, marker)
		if idx < 0 {
			t.Fatalf("row %d marker %q missing: %q", i, marker, line)
		}
		if got := DisplayWidth(line[:idx]); got != wantCol {
			t.Fatalf("row %d column offset = %d, want %d: %q", i, got, wantCol, line)
		}
	}
}

func TestTableRowCountMismatchPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on cell count mismatch")
		}
	}()
	var buf bytes.Buffer
	tr := NewTable(&buf, "A", "B")
	tr.Row("only-one")
}

// Row sanitizes every cell by default: an ESC smuggled into a cell renders
// as '?' and cannot shift or spoof the column layout.
func TestTableRowSanitizesEscCell(t *testing.T) {
	var buf bytes.Buffer
	tr := NewTable(&buf, "NAME", "VER")
	tr.Row("\x1b[31mevil", "v1")
	tr.Row("ok-but-longer-name", "v2")
	if err := tr.Flush(); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.ContainsRune(out, '\x1b') {
		t.Fatalf("raw ESC reached output: %q", out)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("want 3 lines, got %d: %q", len(lines), out)
	}
	// "?[31mevil" is 9 display columns ('?' + "[31m" + "evil"),
	// "ok-but-longer-name" is 18; the second column must start at 18+2=20
	// on every line.
	const wantCol = 20
	markers := []string{"VER", "v1", "v2"}
	for i, line := range lines {
		idx := strings.Index(line, markers[i])
		if idx < 0 {
			t.Fatalf("row %d marker %q missing: %q", i, markers[i], line)
		}
		if got := DisplayWidth(line[:idx]); got != wantCol {
			t.Fatalf("row %d column offset = %d, want %d: %q", i, got, wantCol, line)
		}
	}
}

func TestTableRowSanitizationIsIdempotent(t *testing.T) {
	var buf bytes.Buffer
	tr := NewTable(&buf, "A")
	// sanitizeField swaps the ESC for '?' and leaves the now-harmless
	// "[31m" as printable text; a second pass must not change anything.
	pre := SanitizeField("pre\x1b[31msanitized")
	if pre != "pre?[31msanitized" {
		t.Fatalf("SanitizeField baseline changed: %q", pre)
	}
	tr.Row(pre)
	if err := tr.Flush(); err != nil {
		t.Fatal(err)
	}
	want := "A\npre?[31msanitized\n"
	if buf.String() != want {
		t.Fatalf("pre-sanitized cell changed by Row: %q, want %q", buf.String(), want)
	}
}
