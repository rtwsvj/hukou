// Package doctor audits hukou's local state without changing it.
package doctor

import (
	"fmt"
	"sort"
)

const ReportSchemaVersion = 1

type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
	SeverityInfo    Severity = "info"
)

type Status string

const (
	StatusHealthy    Status = "healthy"
	StatusDegraded   Status = "degraded"
	StatusBroken     Status = "broken"
	StatusIncomplete Status = "incomplete"
)

// Options controls a read-only audit. DataRoot is required. Deep adds hashes
// for retained versions and inspects hukou-owned temporary-name prefixes next
// to registered live paths.
type Options struct {
	DataRoot string
	Deep     bool
}

// Finding is a deterministic, machine-readable diagnostic.
type Finding struct {
	Code     string   `json:"code"`
	Severity Severity `json:"severity"`
	Scope    string   `json:"scope"`
	Subject  string   `json:"subject,omitempty"`
	Path     string   `json:"path,omitempty"`
	Message  string   `json:"message"`
}

type Summary struct {
	Errors   int `json:"errors"`
	Warnings int `json:"warnings"`
	Info     int `json:"info"`
}

// Report is the shared model rendered by both the human and JSON outputs.
// It deliberately contains no timestamps so identical state yields identical
// output.
type Report struct {
	SchemaVersion int       `json:"schema_version"`
	Mode          string    `json:"mode"`
	Status        Status    `json:"status"`
	Complete      bool      `json:"complete"`
	DataRoot      string    `json:"data_root"`
	ManifestPath  string    `json:"manifest_path"`
	Summary       Summary   `json:"summary"`
	Findings      []Finding `json:"findings"`
}

func newReport(opts Options) Report {
	mode := "standard"
	if opts.Deep {
		mode = "deep"
	}
	return Report{
		SchemaVersion: ReportSchemaVersion,
		Mode:          mode,
		Status:        StatusHealthy,
		Complete:      true,
		DataRoot:      opts.DataRoot,
		Findings:      make([]Finding, 0),
	}
}

func (r *Report) add(severity Severity, code, scope, subject, path, format string, args ...any) {
	r.Findings = append(r.Findings, Finding{
		Code:     code,
		Severity: severity,
		Scope:    scope,
		Subject:  subject,
		Path:     path,
		Message:  fmt.Sprintf(format, args...),
	})
}

func (r *Report) incomplete(code, scope, subject, path, format string, args ...any) {
	r.Complete = false
	r.add(SeverityError, code, scope, subject, path, format, args...)
}

func (r *Report) finalize() {
	sort.SliceStable(r.Findings, func(i, j int) bool {
		a, b := r.Findings[i], r.Findings[j]
		if severityRank(a.Severity) != severityRank(b.Severity) {
			return severityRank(a.Severity) < severityRank(b.Severity)
		}
		if a.Code != b.Code {
			return a.Code < b.Code
		}
		if a.Scope != b.Scope {
			return a.Scope < b.Scope
		}
		if a.Subject != b.Subject {
			return a.Subject < b.Subject
		}
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.Message < b.Message
	})

	r.Summary = Summary{}
	for _, finding := range r.Findings {
		switch finding.Severity {
		case SeverityError:
			r.Summary.Errors++
		case SeverityWarning:
			r.Summary.Warnings++
		case SeverityInfo:
			r.Summary.Info++
		}
	}

	switch {
	case !r.Complete:
		r.Status = StatusIncomplete
	case r.Summary.Errors > 0:
		r.Status = StatusBroken
	case r.Summary.Warnings > 0:
		r.Status = StatusDegraded
	default:
		r.Status = StatusHealthy
	}
}

func severityRank(severity Severity) int {
	switch severity {
	case SeverityError:
		return 0
	case SeverityWarning:
		return 1
	default:
		return 2
	}
}

// Healthy reports whether the audit completed without warnings or errors.
func (r Report) Healthy() bool { return r.Status == StatusHealthy }
