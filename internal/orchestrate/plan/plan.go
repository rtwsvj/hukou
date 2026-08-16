// Package plan assembles and renders the dry-run (plan-only) report of
// `hukou up`. It is the package-level embodiment of the U2 executor boundary:
// its transitive dependencies contain neither the executor subpackage nor
// os/exec, and guard_test.go asserts exactly that with `go list -deps`. The
// package therefore CANNOT launch a subprocess — not by convention but by
// import graph. It deliberately does not import internal/orchestrate (whose
// detection layer depends on os/exec for LookPath); callers convert detection
// results into this package's own row type.
package plan

import (
	"fmt"
	"io"
	"strings"

	"github.com/rtwsvj/hukou/internal/i18n"
	"github.com/rtwsvj/hukou/internal/output"
)

// ManagerRow is one registry manager after detection, as the plan renders it.
// It mirrors orchestrate.Detected field-for-field without importing that
// package (see the package comment for why).
type ManagerRow struct {
	// Name is the stable registry key.
	Name string
	// Binary is the executable whose presence activated the manager; empty for
	// the internal hukou row.
	Binary string
	// Commands are the ordered upgrade steps, each a complete argv slice.
	Commands [][]string
	// Available reports whether the manager participates on this machine.
	Available bool
	// Internal marks hukou's own in-process step.
	Internal bool
}

// ManagerJSON is the stable JSON shape for one registry manager.
type ManagerJSON struct {
	Name      string     `json:"name"`
	Binary    string     `json:"binary"`
	Commands  [][]string `json:"commands"`
	Available bool       `json:"available"`
}

// Document is the `up --dry-run --json` document.
type Document struct {
	SchemaVersion    int            `json:"schema_version"`
	Managers         []ManagerJSON  `json:"managers"`
	InventorySummary output.Summary `json:"inventory_summary"`
}

// WriteJSON renders the dry-run plan document: the full filtered manager set
// (available or not) plus the shared inventory summary.
func WriteJSON(w io.Writer, rows []ManagerRow, summary output.Summary) error {
	doc := Document{
		SchemaVersion:    1,
		Managers:         make([]ManagerJSON, 0, len(rows)),
		InventorySummary: summary,
	}
	for _, r := range rows {
		doc.Managers = append(doc.Managers, ManagerJSON{
			Name:      r.Name,
			Binary:    r.Binary,
			Commands:  r.Commands,
			Available: r.Available,
		})
	}
	return output.WriteJSONValue(w, doc)
}

// WriteTable prints the detected-manager plan (available managers only), then
// the shared inventory summary, then the zero-effect trailer. The caller must
// have run output.Summarize on the report.
func WriteTable(w io.Writer, rows []ManagerRow, report output.Report) error {
	fmt.Fprintln(w, i18n.T("managers detected (dry run):"))
	t := output.NewTable(w, i18n.T("NAME"), i18n.T("SOURCE-BINARY"), i18n.T("COMMANDS"))
	shown := 0
	for _, r := range rows {
		if !r.Available {
			continue
		}
		source := r.Binary
		if r.Internal {
			source = "internal"
		}
		t.Row(r.Name, source, renderCommands(r.Commands))
		shown++
	}
	if shown == 0 {
		t.Row("(none)", "", "")
	}
	if err := t.Flush(); err != nil {
		return err
	}

	if err := output.WriteSummaryLine(w, report); err != nil {
		return err
	}

	_, err := fmt.Fprintln(w, i18n.T("dry run: nothing was executed or written"))
	return err
}

// renderCommands joins each argv with spaces and multiple steps with " && " for
// display only; the executable argv arrays are carried verbatim in JSON.
func renderCommands(cmds [][]string) string {
	steps := make([]string, 0, len(cmds))
	for _, argv := range cmds {
		steps = append(steps, strings.Join(argv, " "))
	}
	return strings.Join(steps, " && ")
}
