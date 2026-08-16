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
