package output

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"
	"unicode"
	"unicode/utf8"

	"github.com/rtwsvj/hukou/internal/provenance"
	"github.com/rtwsvj/hukou/internal/scan"
)

// Row pairs a scanned binary with its attribution.
type Row struct {
	Binary      scan.Binary            `json:"binary"`
	Attribution provenance.Attribution `json:"attribution"`
}

// Report is the full scan output (table or JSON).
type Report struct {
	Rows       []Row            `json:"rows"`
	Skipped    int              `json:"skipped"`
	ScanErrors []string         `json:"scan_errors,omitempty"`
	FileErrors []scan.FileError `json:"file_errors,omitempty"` // per-file path+reason (JSON only)
	Warnings   []string         `json:"warnings,omitempty"`
	// Notes are non-fatal advisories from detectors that loaded successfully
	// (e.g. verified stale journal residue). They are kept apart from Warnings
	// so gates keying on warnings are never tripped by routine advisories.
	Notes       []string `json:"notes,omitempty"`
	TotalWalked int      `json:"total_walked"` // binaries before source filters
	// Summary is filled by Summarize / Write* helpers.
	Summary Summary `json:"summary"`
}

// Summary aggregates counts for the footer / JSON.
type Summary struct {
	Total    int            `json:"total"`
	Sources  map[string]int `json:"sources"` // source name → count
	Unknown  int            `json:"unknown"`
	Shadowed int            `json:"shadowed"`
	SourceN  int            `json:"source_count"` // number of distinct sources
	Skipped  int            `json:"skipped"`
}

// Summarize fills report.Summary from Rows and Skipped.
func Summarize(r *Report) {
	src := make(map[string]int)
	unknown, shadowed := 0, 0
	for _, row := range r.Rows {
		s := row.Attribution.Source
		src[s]++
		if s == "unknown" {
			unknown++
		}
		if row.Binary.Shadowed {
			shadowed++
		}
	}
	r.Summary = Summary{
		Total:    len(r.Rows),
		Sources:  src,
		Unknown:  unknown,
		Shadowed: shadowed,
		SourceN:  len(src),
		Skipped:  r.Skipped,
	}
}

// WriteJSON writes the full report as indented JSON.
func WriteJSON(w io.Writer, r Report) error {
	Summarize(&r)
	return WriteJSONValue(w, r)
}

// WriteJSONValue writes any value as indented JSON in the house style (two-space
// indent, trailing newline), so every command's --json output matches.
func WriteJSONValue(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// WriteTable writes a human-readable table plus a summary footer line:
// total / source count / unknown count / shadowed count.
// Per-file error details are omitted (count only via skipped=N).
func WriteTable(w io.Writer, r Report) error {
	Summarize(&r)

	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tPATH\tKIND\tSOURCE\tPACKAGE\tVERSION\tSHADOWED\tEVIDENCE")
	for _, row := range r.Rows {
		b := row.Binary
		a := row.Attribution
		shadowed := ""
		if b.Shadowed {
			shadowed = "yes"
		}
		evidence := a.Evidence
		if evidence == "" && b.Evidence != "" {
			evidence = b.Evidence
		}
		// Sanitize free-text columns so control chars / ANSI cannot break the table.
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			sanitizeField(b.Name),
			sanitizeField(b.Path),
			b.Kind,
			a.Source,
			sanitizeField(a.Package),
			a.Version,
			shadowed,
			truncate(sanitizeField(evidence), 60),
		)
	}
	if err := tw.Flush(); err != nil {
		return err
	}

	// Summary line + source breakdown (spec acceptance #5). Shared with
	// `up --dry-run` via WriteSummaryLine so both read identically.
	if err := WriteSummaryLine(w, r); err != nil {
		return err
	}

	// Non-fatal warnings and notes, one per line, after the summary. Warnings
	// surface detector degradations (e.g. hukou removed from the chain due to
	// pending transaction residue) that would otherwise be visible only in
	// --json; notes carry advisories from detectors that still loaded. Style
	// matches the explain table (see WriteExplainTable). Write errors are
	// propagated: the first failure aborts rendering.
	for _, warning := range r.Warnings {
		if _, err := fmt.Fprintf(w, "warning: %s\n", sanitizeField(warning)); err != nil {
			return err
		}
	}
	for _, note := range r.Notes {
		if _, err := fmt.Fprintf(w, "note: %s\n", sanitizeField(note)); err != nil {
			return err
		}
	}
	return nil
}

// WriteSummaryLine renders the shared inventory footer: the one-line
// total/sources/unknown/shadowed[/skipped] summary followed by the optional
// per-source breakdown. Callers must run Summarize on the report first. It is
// shared by the scan table and `up --dry-run` so a machine's inventory summary
// reads identically in both. The leading newline separates it from whatever
// preceded it (a table, a manager plan).
func WriteSummaryLine(w io.Writer, r Report) error {
	if _, err := fmt.Fprintf(w, "\nsummary: total=%d sources=%d unknown=%d shadowed=%d",
		r.Summary.Total, r.Summary.SourceN, r.Summary.Unknown, r.Summary.Shadowed); err != nil {
		return err
	}
	if r.Summary.Skipped > 0 {
		if _, err := fmt.Fprintf(w, " skipped=%d", r.Summary.Skipped); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	if len(r.Summary.Sources) == 0 {
		return nil
	}
	names := make([]string, 0, len(r.Summary.Sources))
	for k := range r.Summary.Sources {
		names = append(names, k)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, n := range names {
		parts = append(parts, fmt.Sprintf("%s=%d", n, r.Summary.Sources[n]))
	}
	_, err := fmt.Fprintf(w, "by source: %s\n", strings.Join(parts, " "))
	return err
}

// sanitizeField replaces control characters (tab/newline/CR, ANSI ESC, other
// non-printable runes) with '?' so table columns cannot be shifted or spoofed.
// JSON output uses encoding/json and does not need this.
func sanitizeField(s string) string {
	if s == "" {
		return s
	}
	return strings.Map(func(r rune) rune {
		if r == utf8.RuneError {
			return '?'
		}
		if !unicode.IsPrint(r) {
			return '?'
		}
		return r
	}, s)
}

// truncate returns s shortened to at most n bytes without splitting a UTF-8 rune.
// If shortened, the result ends with "..." and total length is ≤ n.
func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	if n <= 3 {
		// Degenerate width: just fit what we can without splitting runes.
		return truncateRunes(s, n)
	}
	bodyBudget := n - 3
	return truncateRunes(s, bodyBudget) + "..."
}

func truncateRunes(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(s) <= maxBytes {
		return s
	}
	// Walk valid UTF-8 boundaries; never cut mid-rune.
	i := 0
	for i < len(s) {
		_, size := utf8.DecodeRuneInString(s[i:])
		if i+size > maxBytes {
			break
		}
		i += size
	}
	return s[:i]
}
