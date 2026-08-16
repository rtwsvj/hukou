package doctor

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode"

	"github.com/rtwsvj/hukou/internal/i18n"
)

// WriteJSON renders the same Report model used by WriteText.
func WriteJSON(w io.Writer, report Report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(report)
}

// WriteText renders a concise deterministic diagnostic report.
func WriteText(w io.Writer, report Report) error {
	if _, err := fmt.Fprintf(w, i18n.T("hukou doctor: %s"), i18n.T(strings.ToUpper(string(report.Status)))); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, i18n.T("data root: %s"), safeText(report.DataRoot)); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, i18n.T("mode: %s"), i18n.T(report.Mode)); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, i18n.T("complete: %t"), report.Complete); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, i18n.T("summary: %d error(s), %d warning(s), %d info"), report.Summary.Errors, report.Summary.Warnings, report.Summary.Info); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	if len(report.Findings) == 0 {
		_, err := fmt.Fprintln(w, i18n.T("No problems found."))
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
		// Localize at render time from the template (the stored Message stays
		// canonical English so doctor --json is byte-identical in every
		// locale).
		message := finding.Message
		if finding.MessageTemplate != "" {
			message = i18n.T(finding.MessageTemplate, finding.MessageArgs...)
		}
		if _, err := fmt.Fprintf(w, "[%s] %s%s: %s", strings.ToUpper(string(finding.Severity)), safeText(finding.Code), location, safeText(message)); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(w); err != nil {
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
