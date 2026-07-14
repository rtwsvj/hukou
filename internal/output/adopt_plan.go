package output

import (
	"encoding/json"
	"fmt"
	"io"
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
	Attribution   AdoptAttribution `json:"attribution"`
	PlannedWrites []string         `json:"planned_writes"`
}

func WriteAdoptPlanJSON(w io.Writer, plan AdoptPlan) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(plan)
}

func WriteAdoptPlan(w io.Writer, plan AdoptPlan) error {
	fmt.Fprintf(w, "Would adopt %s (%s) -> %s\n", sanitizeField(plan.Name), sanitizeField(plan.Tag), sanitizeField(plan.Path))
	if plan.Repo != "" {
		fmt.Fprintf(w, "repo: %s\n", sanitizeField(plan.Repo))
	} else {
		fmt.Fprintln(w, "repo: local")
	}
	fmt.Fprintf(w, "sha256: %s\n", sanitizeField(plan.SHA256))
	fmt.Fprintf(w, "source: %s (%s)\n", sanitizeField(plan.Attribution.Source), sanitizeField(plan.Attribution.Confidence))
	if plan.Attribution.Evidence != "" {
		fmt.Fprintf(w, "evidence: %s\n", sanitizeField(plan.Attribution.Evidence))
	}
	fmt.Fprintln(w, "planned writes:")
	for _, path := range plan.PlannedWrites {
		fmt.Fprintf(w, "  - %s\n", sanitizeField(path))
	}
	return nil
}
