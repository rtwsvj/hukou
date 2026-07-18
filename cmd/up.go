package cmd

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/rtwsvj/hukou/internal/orchestrate"
	"github.com/rtwsvj/hukou/internal/output"
	"github.com/rtwsvj/hukou/internal/provenance"
	"github.com/spf13/cobra"
)

var (
	upDryRun bool
	upJSON   bool
	upOnly   []string
	upSkip   []string
)

// errRealRun is returned when `up` runs without --dry-run in the U1 slice. Real
// execution lands in a later slice; ExitCode maps this to process status 2.
var errRealRun = errors.New("real execution lands in a later slice; use --dry-run")

var upCmd = &cobra.Command{
	Use:   "up",
	Short: "Plan or (later) run a full-machine upgrade of every known manager",
	Long: `Upgrade the package managers hukou knows about on this machine plus
hukou's own adopted tools.

This is the U1 slice, which implements --dry-run only. A dry run detects the
managers present on PATH, prints the exact commands that would run and a
read-only inventory summary, and is guaranteed to make zero writes and launch
zero subprocesses: no data root is created, no manager command is executed.

Running without --dry-run is not yet implemented; it prints a notice and exits
with status 2. Real execution, the before/after inventory snapshot, and the diff
report arrive in a later slice.`,
	Args:          cobra.NoArgs,
	SilenceErrors: true, // errors are printed once, here or via fail.
	RunE:          runUp,
}

func init() {
	upCmd.Flags().BoolVar(&upDryRun, "dry-run", false, "detect managers and print the upgrade plan without executing or writing anything")
	upCmd.Flags().BoolVar(&upJSON, "json", false, "emit the plan as JSON")
	upCmd.Flags().StringSliceVar(&upOnly, "only", nil, "restrict to these managers by registry name (repeatable or comma-separated)")
	upCmd.Flags().StringSliceVar(&upSkip, "skip", nil, "exclude these managers by registry name (repeatable or comma-separated)")
	rootCmd.AddCommand(upCmd)
}

func runUp(cmd *cobra.Command, _ []string) error {
	return doUp(cmd.OutOrStdout(), cmd.ErrOrStderr(), upOptions{
		dryRun: upDryRun,
		json:   upJSON,
		only:   upOnly,
		skip:   upSkip,
	}, nil, defaultInventory)
}

// upOptions captures one resolved `up` invocation.
type upOptions struct {
	dryRun bool
	json   bool
	only   []string
	skip   []string
}

// defaultInventory runs the shared read-only PATH inventory. It creates no data
// root and makes no network request (see collectInventory).
func defaultInventory() (output.Report, error) {
	return collectInventory(provenance.DefaultEnv(), nil)
}

// doUp is the testable core of `hukou up`. lookPath (nil = exec.LookPath)
// resolves manager binaries and inventory supplies the read-only scan; both are
// injectable so tests exercise the whole plan with a fake PATH and fixture
// inventory. The dry-run path never creates the data root, launches a
// subprocess, or holds a lock — it only reads and prints.
func doUp(stdout, stderr io.Writer, opts upOptions, lookPath orchestrate.LookPathFunc, inventory func() (output.Report, error)) error {
	if !opts.dryRun {
		// U1 placeholder: the contract is documented in --help.
		fmt.Fprintln(stderr, errRealRun.Error())
		return errRealRun
	}

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

// upManagerJSON is the stable JSON shape for one registry manager.
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

// ExitCode maps a top-level command error to a process exit status: the U1
// no-dry-run placeholder is status 2; anything else is the usual 1.
func ExitCode(err error) int {
	switch {
	case err == nil:
		return 0
	case errors.Is(err, errRealRun):
		return 2
	default:
		return 1
	}
}
