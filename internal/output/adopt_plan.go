package output

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/rtwsvj/hukou/internal/i18n"
)

type AdoptAttribution struct {
	Source     string `json:"source"`
	Package    string `json:"package,omitempty"`
	Version    string `json:"version,omitempty"`
	Upstream   string `json:"upstream,omitempty"`
	Confidence string `json:"confidence"`
	Evidence   string `json:"evidence,omitempty"`
}

// AdoptPlan describes a read-only adoption inspection. It is informational:
// a real adoption always repeats every check while holding the mutation lock.
type AdoptPlan struct {
	SchemaVersion int              `json:"schema_version"`
	Name          string           `json:"name"`
	Path          string           `json:"path"`
	SHA256        string           `json:"sha256"`
	Repo          string           `json:"repo,omitempty"`
	Tag           string           `json:"tag"`
	Local         bool             `json:"local"`
	ArchiveExe    string           `json:"archive_exe,omitempty"`
	Attribution   AdoptAttribution `json:"attribution"`
	PlannedWrites []string         `json:"planned_writes"`
}

func WriteAdoptPlanJSON(w io.Writer, plan AdoptPlan) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(plan)
}

func WriteAdoptPlan(w io.Writer, plan AdoptPlan) error {
	fmt.Fprintf(w, i18n.T("Would adopt %s (%s) -> %s"), sanitizeField(plan.Name), sanitizeField(plan.Tag), sanitizeField(plan.Path))
	fmt.Fprintln(w)
	if plan.Repo != "" {
		fmt.Fprintf(w, i18n.T("repo: %s"), sanitizeField(plan.Repo))
	} else {
		fmt.Fprintln(w, i18n.T("repo: local"))
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, i18n.T("sha256: %s"), sanitizeField(plan.SHA256))
	fmt.Fprintln(w)
	if plan.ArchiveExe != "" {
		fmt.Fprintf(w, i18n.T("archive exe: %s"), sanitizeField(plan.ArchiveExe))
		fmt.Fprintln(w)
	}
	fmt.Fprintf(w, i18n.T("source: %s (%s)"), sanitizeField(plan.Attribution.Source), sanitizeField(plan.Attribution.Confidence))
	fmt.Fprintln(w)
	if plan.Attribution.Evidence != "" {
		fmt.Fprintf(w, i18n.T("evidence: %s"), sanitizeField(plan.Attribution.Evidence))
		fmt.Fprintln(w)
	}
	fmt.Fprintln(w, i18n.T("planned writes:"))
	for _, path := range plan.PlannedWrites {
		fmt.Fprintf(w, "  - %s\n", sanitizeField(path))
	}
	return nil
}
