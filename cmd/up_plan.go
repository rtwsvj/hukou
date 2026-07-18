// This file is the thin dry-run shell of `hukou up`: it detects managers,
// runs the read-only inventory, and hands everything to the subprocess-free
// internal/orchestrate/plan package for assembly and rendering. The executor
// boundary is enforced at PACKAGE level: plan's guard test asserts its
// transitive deps contain neither the executor subpackage nor os/exec
// (internal/orchestrate/plan/guard_test.go). Within cmd, this file must not
// import the executor subpackage (up_guard_test.go), and the U1 behavioral
// stub is retained: doUpPlan declares a runner seam it never uses, and
// forbidRunner tests fail on any invocation.
package cmd

import (
	"io"

	"github.com/rtwsvj/hukou/internal/orchestrate"
	"github.com/rtwsvj/hukou/internal/orchestrate/plan"
	"github.com/rtwsvj/hukou/internal/output"
	"github.com/rtwsvj/hukou/internal/provenance"
)

// managerRunner launches one upgrade step (a single argv) for a named manager.
// The dry-run path declares the seam without using it — exactly the U1
// mechanism: no dry-run code path invokes the runner, and the tests inject a
// stub that fails the test on first call (TestUp_dryRunNeverInvokesRunner), so
// "dry run launches zero subprocesses" stays mechanically enforced at runtime
// in addition to the package-level dependency guard.
type managerRunner func(name string, argv []string) error

// doUpPlan is the dry-run entry point: filter and detect the registry, run the
// injected read-only inventory, convert to plan rows, and render via the
// subprocess-free plan package. It only reads and prints — no data root, no
// lock, no subprocess, no network.
func doUpPlan(stdout io.Writer, opts upOptions, lookPath orchestrate.LookPathFunc, inventory func() (output.Report, error), run managerRunner) error {
	// Dry-run invariant: `run` is deliberately unused on every path below.
	_ = run

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

	rows := planRows(detected)
	if opts.json {
		return fail(plan.WriteJSON(stdout, rows, report.Summary))
	}
	return fail(plan.WriteTable(stdout, rows, report))
}

// planRows converts detection results into the plan package's own row type;
// plan cannot import orchestrate (that would pull os/exec into its deps).
func planRows(detected []orchestrate.Detected) []plan.ManagerRow {
	rows := make([]plan.ManagerRow, 0, len(detected))
	for _, d := range detected {
		rows = append(rows, plan.ManagerRow{
			Name:      d.Name,
			Binary:    d.DetectBinary,
			Commands:  d.Commands,
			Available: d.Available,
			Internal:  d.Internal,
		})
	}
	return rows
}

// defaultInventory runs the shared read-only PATH inventory. It creates no data
// root and makes no network request (see collectInventory).
func defaultInventory() (output.Report, error) {
	return collectInventory(provenance.DefaultEnv(), nil)
}
