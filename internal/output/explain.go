package output

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/rtwsvj/hukou/internal/i18n"
	"github.com/rtwsvj/hukou/internal/scan"
)

// ExplainMatch is deliberately independent from the scanner's internal Go
// structs so the versioned JSON contract is not changed by internal refactors.
type ExplainMatch struct {
	Name       string       `json:"name"`
	Path       string       `json:"path"`
	RealPath   string       `json:"real_path,omitempty"`
	Kind       scan.BinKind `json:"kind"`
	Source     string       `json:"source"`
	Package    string       `json:"package,omitempty"`
	Version    string       `json:"version,omitempty"`
	Confidence string       `json:"confidence"`
	Evidence   string       `json:"evidence,omitempty"`
	Shadowed   bool         `json:"shadowed"`
}

func NewExplainMatch(row Row) ExplainMatch {
	return ExplainMatch{
		Name:       row.Binary.Name,
		Path:       row.Binary.Path,
		RealPath:   row.Binary.RealPath,
		Kind:       row.Binary.Kind,
		Source:     row.Attribution.Source,
		Package:    row.Attribution.Package,
		Version:    row.Attribution.Version,
		Confidence: row.Attribution.Confidence,
		Evidence:   row.Attribution.Evidence,
		Shadowed:   row.Binary.Shadowed,
	}
}

// ExplainReport is the stable machine-readable model emitted by explain.
// Notes is an additive optional field (schema_version stays 1): non-fatal
// advisories from detectors that loaded successfully, kept apart from
// Warnings, which signal detector degradation.
type ExplainReport struct {
	SchemaVersion int            `json:"schema_version"`
	Query         string         `json:"query"`
	Active        *ExplainMatch  `json:"active,omitempty"`
	Matches       []ExplainMatch `json:"matches"`
	Warnings      []string       `json:"warnings,omitempty"`
	Notes         []string       `json:"notes,omitempty"`
}

func WriteExplainJSON(w io.Writer, report ExplainReport) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(report)
}

func WriteExplainTable(w io.Writer, report ExplainReport) error {
	if len(report.Matches) == 0 {
		return i18n.Errorf("no executable matched %q", report.Query)
	}
	t := NewTable(w,
		i18n.T("STATUS"), i18n.T("NAME"), i18n.T("PATH"), i18n.T("REAL PATH"),
		i18n.T("KIND"), i18n.T("SOURCE"), i18n.T("PACKAGE"), i18n.T("VERSION"),
		i18n.T("CONFIDENCE"), i18n.T("EVIDENCE"),
	)
	for _, match := range report.Matches {
		status := "candidate"
		if report.Active != nil && match.Path == report.Active.Path {
			status = "active"
		} else if match.Shadowed {
			status = "shadowed"
		}
		t.Row(
			status,
			sanitizeField(match.Name),
			sanitizeField(match.Path),
			sanitizeField(match.RealPath),
			string(match.Kind),
			sanitizeField(match.Source),
			sanitizeField(match.Package),
			sanitizeField(match.Version),
			sanitizeField(match.Confidence),
			TruncateDisplay(sanitizeField(match.Evidence), 80),
		)
	}
	if err := t.Flush(); err != nil {
		return err
	}
	for _, warning := range report.Warnings {
		if _, err := fmt.Fprintf(w, i18n.T("warning: %s"), sanitizeField(warning)); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
	}
	for _, note := range report.Notes {
		if _, err := fmt.Fprintf(w, i18n.T("note: %s"), sanitizeField(note)); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
	}
	return nil
}
