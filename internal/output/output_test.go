package output

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/rtwsvj/hukou/internal/provenance"
	"github.com/rtwsvj/hukou/internal/scan"
)

func sampleReport() Report {
	return Report{
		Rows: []Row{
			{
				Binary: scan.Binary{
					Name: "ls", Path: "/bin/ls", RealPath: "/bin/ls",
					Kind: scan.KindMachO, Shadowed: false,
				},
				Attribution: provenance.Attribution{
					Source: "system", Package: "ls", Confidence: "exact",
					Evidence: "path prefix /bin",
				},
			},
			{
				Binary: scan.Binary{
					Name: "foo", Path: "/opt/foo", RealPath: "/opt/foo",
					Kind: scan.KindOther, Shadowed: false,
				},
				Attribution: provenance.Attribution{
					Source: "unknown", Package: "foo", Confidence: "inferred",
					Evidence: "no prior detector matched",
				},
			},
			{
				Binary: scan.Binary{
					Name: "ls", Path: "/usr/local/bin/ls", RealPath: "/usr/local/bin/ls",
					Kind: scan.KindMachO, Shadowed: true,
				},
				Attribution: provenance.Attribution{
					Source: "unknown", Package: "ls", Confidence: "inferred",
					Evidence: "no prior detector matched",
				},
			},
		},
		Skipped:     1,
		TotalWalked: 3,
		FileErrors: []scan.FileError{
			{Path: "/tmp/pipe", Reason: "non-regular file (not opened): prw-"},
		},
		Warnings: []string{
			"empty PATH segment skipped (deliberate: not treated as current directory, unlike POSIX)",
		},
		Notes: []string{
			"detector hukou: stale journal residue; run a mutating command or repair to clean (completed=1)",
		},
	}
}

func TestWriteTable_summary(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteTable(&buf, sampleReport()); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "NAME") {
		t.Fatalf("missing header: %s", out)
	}
	if !strings.Contains(out, "summary: total=3 sources=2 unknown=2 shadowed=1") {
		t.Fatalf("missing/wrong summary line:\n%s", out)
	}
	if !strings.Contains(out, "skipped=1") {
		t.Fatalf("expected skipped in summary:\n%s", out)
	}
	if !strings.Contains(out, "system=1") || !strings.Contains(out, "unknown=2") {
		t.Fatalf("missing by-source breakdown:\n%s", out)
	}
	// Table shows count only — not per-file error paths.
	if strings.Contains(out, "/tmp/pipe") {
		t.Fatalf("table must not dump file error paths:\n%s", out)
	}
}

// WriteTable renders Report.Warnings and Report.Notes after the
// summary so degraded detectors (warnings) and non-fatal advisories (notes)
// are visible in the default table, not only in --json. The two channels keep
// distinct prefixes.
func TestWriteTable_rendersWarningsAndNotes(t *testing.T) {
	r := sampleReport()
	r.Warnings = []string{
		"detector hukou load failed: hukou state may be inconsistent",
		"empty PATH segment skipped",
	}
	r.Notes = []string{
		"detector hukou: stale journal residue; run a mutating command or repair to clean (completed=1)",
	}
	var buf bytes.Buffer
	if err := WriteTable(&buf, r); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, w := range r.Warnings {
		if !strings.Contains(out, "warning: "+w) {
			t.Fatalf("missing rendered warning %q in:\n%s", w, out)
		}
	}
	for _, n := range r.Notes {
		if !strings.Contains(out, "note: "+n) {
			t.Fatalf("missing rendered note %q in:\n%s", n, out)
		}
	}
	// Layout: summary line first, then warnings, then notes (explain-aligned).
	if strings.Index(out, "summary:") > strings.Index(out, "warning:") {
		t.Fatalf("warnings must render after the summary line:\n%s", out)
	}
	if strings.Index(out, "warning:") > strings.Index(out, "note:") {
		t.Fatalf("notes must render after warnings:\n%s", out)
	}
	// A note must never be rendered with the warning prefix.
	if strings.Contains(out, "warning: detector hukou: stale journal residue") {
		t.Fatalf("note leaked into the warning prefix:\n%s", out)
	}
}

// Warning and note text is sanitized like other free-text columns so
// control chars / ANSI cannot corrupt the terminal.
func TestWriteTable_sanitizesWarningsAndNotes(t *testing.T) {
	r := Report{
		Warnings: []string{"bad\x1b[31mwarning\twith\ncontrol"},
		Notes:    []string{"bad\x1b[31mnote\twith\ncontrol"},
	}
	var buf bytes.Buffer
	if err := WriteTable(&buf, r); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Contains(out, "\x1b") {
		t.Fatalf("ANSI escape leaked into output:\n%q", out)
	}
	// Only non-printable runes (ESC, tab, newline) become '?'; the printable
	// "[31m" tail survives, matching sanitizeField's contract.
	if !strings.Contains(out, "warning: bad?[31mwarning?with?control") {
		t.Fatalf("warning not sanitized as expected:\n%q", out)
	}
	if !strings.Contains(out, "note: bad?[31mnote?with?control") {
		t.Fatalf("note not sanitized as expected:\n%q", out)
	}
}

// The render loops must not swallow writer failures; the first
// write error is returned to the caller.
func TestWriteTable_propagatesWriteErrors(t *testing.T) {
	r := Report{
		Warnings: []string{"w1"},
		Notes:    []string{"n1"},
	}
	// Grow the limit until the failure lands inside the warning/note loops.
	full := false
	for limit := 1; limit < 1<<16; limit *= 2 {
		w := &limitedWriter{limit: limit}
		err := WriteTable(w, r)
		if err == nil {
			if !strings.Contains(w.buf.String(), "note: n1") {
				t.Fatalf("nil error but note missing at limit=%d:\n%s", limit, w.buf.String())
			}
			full = true
			break
		}
		if !strings.Contains(err.Error(), "writer full") {
			t.Fatalf("unexpected error at limit=%d: %v", limit, err)
		}
	}
	if !full {
		t.Fatal("WriteTable never succeeded even with a large writer")
	}
}

type limitedWriter struct {
	buf   bytes.Buffer
	limit int
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	if w.buf.Len()+len(p) > w.limit {
		return 0, errTestWriterFull
	}
	return w.buf.Write(p)
}

var errTestWriterFull = errors.New("writer full")

func TestWriteJSON_roundtrip(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteJSON(&buf, sampleReport()); err != nil {
		t.Fatal(err)
	}
	var decoded Report
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("json decode: %v\n%s", err, buf.String())
	}
	if decoded.Summary.Total != 3 {
		t.Fatalf("summary.total=%d want 3", decoded.Summary.Total)
	}
	if decoded.Summary.Unknown != 2 {
		t.Fatalf("summary.unknown=%d want 2", decoded.Summary.Unknown)
	}
	if decoded.Summary.Shadowed != 1 {
		t.Fatalf("summary.shadowed=%d want 1", decoded.Summary.Shadowed)
	}
	if decoded.Summary.SourceN != 2 {
		t.Fatalf("summary.source_count=%d want 2", decoded.Summary.SourceN)
	}
	if len(decoded.Rows) != 3 {
		t.Fatalf("rows=%d", len(decoded.Rows))
	}
	// Fix #8: JSON includes per-file error details.
	if len(decoded.FileErrors) != 1 || decoded.FileErrors[0].Path != "/tmp/pipe" {
		t.Fatalf("file_errors=%+v", decoded.FileErrors)
	}
	if decoded.FileErrors[0].Reason == "" {
		t.Fatal("file_errors reason empty")
	}
	if len(decoded.Warnings) != 1 {
		t.Fatalf("warnings=%v", decoded.Warnings)
	}
	// Notes travel on their own JSON field, apart from warnings.
	if len(decoded.Notes) != 1 || !strings.Contains(decoded.Notes[0], "stale journal residue") {
		t.Fatalf("notes=%v", decoded.Notes)
	}
}

func TestSummarize(t *testing.T) {
	r := sampleReport()
	Summarize(&r)
	if r.Summary.Total != 3 || r.Summary.Unknown != 2 || r.Summary.Shadowed != 1 {
		t.Fatalf("%+v", r.Summary)
	}
}

// Round2 C: control chars / ANSI in table fields become '?'.
func TestWriteTable_sanitizesControlChars(t *testing.T) {
	r := Report{
		Rows: []Row{
			{
				Binary: scan.Binary{
					Name: "evil\tname", Path: "/tmp/a\nb", Kind: scan.KindOther,
				},
				Attribution: provenance.Attribution{
					Source: "unknown", Package: "pkg\rX",
					Evidence: "has\x1b[31mANSI\x1b[0m",
				},
			},
		},
	}
	var buf bytes.Buffer
	if err := WriteTable(&buf, r); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	// Raw control/ANSI must not appear (tabwriter may still use tabs as padding).
	if strings.Contains(out, "evil\tname") || strings.Contains(out, "/tmp/a\nb") || strings.Contains(out, "pkg\rX") {
		t.Fatalf("raw control chars leaked into table:\n%q", out)
	}
	if strings.Contains(out, "\x1b") {
		t.Fatalf("ANSI escape leaked into table:\n%q", out)
	}
	if !strings.Contains(out, "evil?name") {
		t.Fatalf("expected sanitized name, got:\n%s", out)
	}
	if !strings.Contains(out, "pkg?X") {
		t.Fatalf("expected sanitized package, got:\n%s", out)
	}
	if !strings.Contains(out, "/tmp/a?b") {
		t.Fatalf("expected sanitized path, got:\n%s", out)
	}
}

func TestSanitizeField(t *testing.T) {
	cases := []struct{ in, want string }{
		{"plain", "plain"},
		{"a\tb", "a?b"},
		{"a\nb", "a?b"},
		{"a\rb", "a?b"},
		{"x\x1by", "x?y"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := sanitizeField(tc.in); got != tc.want {
			t.Errorf("sanitizeField(%q)=%q want %q", tc.in, got, tc.want)
		}
	}
}

// Fix #8: truncate must not split multi-byte UTF-8 runes.
func TestTruncate_utf8Safe(t *testing.T) {
	// Each Chinese rune is 3 bytes in UTF-8.
	s := "你好世界测试字符串内容更多"
	// n=10: body budget 7 after "..." → at most 2 runes (6 bytes) + "..."
	got := truncate(s, 10)
	if !utf8.ValidString(got) {
		t.Fatalf("invalid utf8: %q bytes=%v", got, []byte(got))
	}
	if strings.Contains(got, "\ufffd") {
		t.Fatalf("replacement char in truncate result: %q", got)
	}
	if len(got) > 10 {
		t.Fatalf("len=%d > 10: %q", len(got), got)
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("expected ellipsis: %q", got)
	}
	// Ensure we didn't cut mid-rune: stripping "..." should be valid and short.
	body := strings.TrimSuffix(got, "...")
	if !utf8.ValidString(body) || body == "" {
		t.Fatalf("body invalid: %q", body)
	}
	for _, r := range body {
		if r == utf8.RuneError {
			t.Fatalf("rune error in body %q", body)
		}
	}

	// Exact fit: no truncate
	if truncate("abc", 3) != "abc" {
		t.Fatal("short string should pass through")
	}
	// Single multi-byte at start with tiny budget
	got = truncate("世界", 4)
	if !utf8.ValidString(got) {
		t.Fatalf("invalid: %q", got)
	}
}
