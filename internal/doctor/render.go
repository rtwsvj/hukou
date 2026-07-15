package doctor

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode"
)

// WriteJSON renders the same Report model used by WriteText.
func WriteJSON(w io.Writer, report Report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(report)
}

// WriteText renders a concise deterministic diagnostic report.
func WriteText(w io.Writer, report Report) error {
	if _, err := fmt.Fprintf(w, "hukou doctor: %s\n", strings.ToUpper(string(report.Status))); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "data root: %s\n", safeText(report.DataRoot)); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "mode: %s\n", report.Mode); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "complete: %t\n", report.Complete); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "summary: %d error(s), %d warning(s), %d info\n", report.Summary.Errors, report.Summary.Warnings, report.Summary.Info); err != nil {
		return err
	}
	if len(report.Findings) == 0 {
		_, err := fmt.Fprintln(w, "No problems found.")
		return err
	}
	for _, finding := range report.Findings {
		location := safeText(finding.Subject)
		if finding.Path != "" {
			if location != "" {
				location += " "
			}
			location += safeText(finding.Path)
		}
		if location != "" {
			location = " (" + location + ")"
		}
		if _, err := fmt.Fprintf(w, "[%s] %s%s: %s\n", strings.ToUpper(string(finding.Severity)), safeText(finding.Code), location, safeText(finding.Message)); err != nil {
			return err
		}
	}
	return nil
}

func safeText(value string) string {
	if strings.IndexFunc(value, unicode.IsControl) < 0 {
		return value
	}
	return strconv.QuoteToASCII(value)
}
