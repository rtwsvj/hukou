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
	Rows        []Row            `json:"rows"`
	Skipped     int              `json:"skipped"`
	ScanErrors  []string         `json:"scan_errors,omitempty"`
	FileErrors  []scan.FileError `json:"file_errors,omitempty"` // per-file path+reason (JSON only)
	Warnings    []string         `json:"warnings,omitempty"`
	TotalWalked int              `json:"total_walked"` // binaries before source filters
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
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
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

	// Summary line (spec acceptance #5).
	fmt.Fprintf(w, "\nsummary: total=%d sources=%d unknown=%d shadowed=%d",
		r.Summary.Total, r.Summary.SourceN, r.Summary.Unknown, r.Summary.Shadowed)
	if r.Summary.Skipped > 0 {
		fmt.Fprintf(w, " skipped=%d", r.Summary.Skipped)
	}
	fmt.Fprintln(w)

	// Optional source breakdown (stable order).
	if len(r.Summary.Sources) > 0 {
		names := make([]string, 0, len(r.Summary.Sources))
		for k := range r.Summary.Sources {
			names = append(names, k)
		}
		sort.Strings(names)
		parts := make([]string, 0, len(names))
		for _, n := range names {
			parts = append(parts, fmt.Sprintf("%s=%d", n, r.Summary.Sources[n]))
		}
		fmt.Fprintf(w, "by source: %s\n", strings.Join(parts, " "))
	}
	return nil
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
