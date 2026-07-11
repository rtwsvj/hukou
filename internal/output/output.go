package output

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"

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
	Rows        []Row    `json:"rows"`
	Skipped     int      `json:"skipped"`
	ScanErrors  []string `json:"scan_errors,omitempty"`
	TotalWalked int      `json:"total_walked"` // binaries before source filters
	// Summary is filled by Summarize / Write* helpers.
	Summary Summary `json:"summary"`
}

// Summary aggregates counts for the footer / JSON.
type Summary struct {
	Total     int            `json:"total"`
	Sources   map[string]int `json:"sources"` // source name → count
	Unknown   int            `json:"unknown"`
	Shadowed  int            `json:"shadowed"`
	SourceN   int            `json:"source_count"` // number of distinct sources
	Skipped   int            `json:"skipped"`
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
// 总数 / 来源数 / unknown 数 / shadowed 数.
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
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			b.Name,
			b.Path,
			b.Kind,
			a.Source,
			a.Package,
			a.Version,
			shadowed,
			truncate(a.Evidence, 60),
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

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}
