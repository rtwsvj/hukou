// This file is the plan-only (dry-run) half of `hukou up`. Structural rule,
// enforced by TestUpDryRunChainCannotReachExecutor in up_guard_test.go: this
// file must NOT import the executor subpackage, and no function transitively
// reachable from doUpPlan may live in a file that does. That keeps the dry-run
// call chain statically incapable of launching a manager subprocess.
package cmd

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/rtwsvj/hukou/internal/orchestrate"
	"github.com/rtwsvj/hukou/internal/output"
	"github.com/rtwsvj/hukou/internal/provenance"
)

// doUpPlan is the dry-run entry point: it filters and detects the registry,
// runs the injected read-only inventory, and prints the plan (table or JSON).
// It only reads and prints — no data root, no lock, no subprocess, no network.
// The signature has no execution seam at all: nothing an accidental caller
// could pass would make this path run a command.
func doUpPlan(stdout io.Writer, opts upOptions, lookPath orchestrate.LookPathFunc, inventory func() (output.Report, error)) error {
	managers, err := orchestrate.Filter(orchestrate.Registry(), opts.only, opts.skip)
	if err != nil {
		return fail(err)
	}
	detected := orchestrate.Detect(managers, lookPath)

	report, err := inventory()
	if err != nil {
		return fail(err)
	}
	output.Summarize(&report)

	if opts.json {
		return fail(writeUpJSON(stdout, detected, report.Summary))
	}
	return fail(writeUpTable(stdout, detected, report))
}

// defaultInventory runs the shared read-only PATH inventory. It creates no data
// root and makes no network request (see collectInventory).
func defaultInventory() (output.Report, error) {
	return collectInventory(provenance.DefaultEnv(), nil)
}

// upManagerJSON is the stable JSON shape for one registry manager (dry-run plan).
type upManagerJSON struct {
	Name      string     `json:"name"`
	Binary    string     `json:"binary"`
	Commands  [][]string `json:"commands"`
	Available bool       `json:"available"`
}

// upPlanJSON is the `up --dry-run --json` document.
type upPlanJSON struct {
	SchemaVersion    int             `json:"schema_version"`
	Managers         []upManagerJSON `json:"managers"`
	InventorySummary output.Summary  `json:"inventory_summary"`
}

func writeUpJSON(w io.Writer, detected []orchestrate.Detected, summary output.Summary) error {
	plan := upPlanJSON{
		SchemaVersion:    1,
		Managers:         make([]upManagerJSON, 0, len(detected)),
		InventorySummary: summary,
	}
	for _, d := range detected {
		plan.Managers = append(plan.Managers, upManagerJSON{
			Name:      d.Name,
			Binary:    d.DetectBinary,
			Commands:  d.Commands,
			Available: d.Available,
		})
	}
	return output.WriteJSONValue(w, plan)
}

// writeUpTable prints the detected-manager plan (available managers only),
// then the shared inventory summary, then the zero-effect trailer.
func writeUpTable(w io.Writer, detected []orchestrate.Detected, report output.Report) error {
	fmt.Fprintln(w, "managers detected (dry run):")
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tSOURCE-BINARY\tCOMMANDS")
	shown := 0
	for _, d := range detected {
		if !d.Available {
			continue
		}
		source := d.DetectBinary
		if d.Internal {
			source = "internal"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", d.Name, source, renderCommands(d.Commands))
		shown++
	}
	if shown == 0 {
		fmt.Fprintln(tw, "(none)\t\t")
	}
	if err := tw.Flush(); err != nil {
		return err
	}

	if err := output.WriteSummaryLine(w, report); err != nil {
		return err
	}

	_, err := fmt.Fprintln(w, "dry run: nothing was executed or written")
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
