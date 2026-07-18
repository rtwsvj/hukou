package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/rtwsvj/hukou/internal/ghrelease"
	"github.com/rtwsvj/hukou/internal/orchestrate"
	"github.com/rtwsvj/hukou/internal/orchestrate/executor"
	"github.com/rtwsvj/hukou/internal/output"
	"github.com/rtwsvj/hukou/internal/provenance"
	"github.com/rtwsvj/hukou/internal/verify"
	"github.com/spf13/cobra"
)

var (
	upDryRun bool
	upJSON   bool
	upOnly   []string
	upSkip   []string
)

// errUpManagersFailed is the aggregate error returned when at least one manager
// finished non-OK. It mirrors `upgrade --all`: the rest still run, the report is
// still written, and the process exits non-zero (status 1) at the end.
var errUpManagersFailed = errors.New("one or more managers failed")

// snapshotRetention is how many past snapshot runs are kept under
// <dataRoot>/snapshots; older runs are pruned after each real run (never the
// run just written).
const snapshotRetention = 10

var upCmd = &cobra.Command{
	Use:   "up",
	Short: "Upgrade every known manager on this machine and diff the inventory",
	Long: `Upgrade the package managers hukou knows about on this machine plus
hukou's own adopted tools, then report exactly what changed.

A dry run (--dry-run) detects the managers present on PATH, prints the exact
commands that would run and a read-only inventory summary, and is guaranteed to
make zero writes and launch zero subprocesses.

A real run takes a full-machine inventory snapshot, runs each manager's upgrade
command in registry order (a failing manager is reported and does not stop the
rest), takes a second snapshot, and prints a diff of every added, removed, or
changed binary grouped by source. The snapshot pair and diff are persisted under
<dataRoot>/snapshots/<timestamp>/. Every manager subprocess is launched through
a single constrained executor with streamed output and a per-manager timeout;
hukou never mutates another manager's state beyond invoking its upgrade command.
If any manager fails, the overall exit status is non-zero.`,
	Args:          cobra.NoArgs,
	SilenceErrors: true, // errors are printed once, here or via fail.
	RunE:          runUp,
}

func init() {
	upCmd.Flags().BoolVar(&upDryRun, "dry-run", false, "detect managers and print the upgrade plan without executing or writing anything")
	upCmd.Flags().BoolVar(&upJSON, "json", false, "emit the plan (dry-run) or the run report as JSON")
	upCmd.Flags().StringSliceVar(&upOnly, "only", nil, "restrict to these managers by registry name (repeatable or comma-separated)")
	upCmd.Flags().StringSliceVar(&upSkip, "skip", nil, "exclude these managers by registry name (repeatable or comma-separated)")
	rootCmd.AddCommand(upCmd)
}

func runUp(cmd *cobra.Command, _ []string) error {
	stdout := cmd.OutOrStdout()
	stderr := cmd.ErrOrStderr()
	return doUp(stdout, stderr, upOptions{
		dryRun: upDryRun,
		json:   upJSON,
		only:   upOnly,
		skip:   upSkip,
	}, productionUpDeps(stdout, stderr))
}

// upOptions captures one resolved `up` invocation.
type upOptions struct {
	dryRun bool
	json   bool
	only   []string
	skip   []string
}

// upDeps are the injectable seams of `hukou up`. Every side-effecting capability
// is a field so tests can drive the whole flow with a fake PATH, a fixture
// inventory, a stub executor, and a temp data root. The dry-run path touches
// only lookPath and inventory; the executor, hukouStep, hasher, and dataRoot
// seams belong exclusively to the real run.
type upDeps struct {
	// lookPath resolves manager binaries (nil = exec.LookPath).
	lookPath orchestrate.LookPathFunc
	// inventory runs the shared read-only PATH scan (pre and post snapshot).
	inventory func() (output.Report, error)
	// exec is the sole subprocess launcher for external managers.
	exec orchestrate.StepExecutor
	// hukouStep runs the internal, in-process `upgrade --all` (holds the normal
	// mutation lock for its own duration; no lock is held while external
	// managers run).
	hukouStep func(stdout, stderr io.Writer) error
	// hasher content-hashes a binary path for snapshot diffing ("" on failure).
	hasher func(path string) string
	// now stamps the snapshot history directory.
	now func() time.Time
	// dataRoot resolves the hukou data directory that owns snapshots/.
	dataRoot func() string
}

// productionUpDeps wires the real seams: the shared read-only inventory, the
// constrained subprocess executor, the in-process hukou upgrade step, real
// content hashing, and the real data root.
func productionUpDeps(stdout, stderr io.Writer) upDeps {
	return upDeps{
		lookPath:  nil,
		inventory: defaultInventory,
		exec:      executor.New(stdout, stderr),
		hukouStep: defaultHukouStep,
		hasher:    hashFile,
		now:       time.Now,
		dataRoot:  dataRoot,
	}
}

// defaultInventory runs the shared read-only PATH inventory. It creates no data
// root and makes no network request (see collectInventory).
func defaultInventory() (output.Report, error) {
	return collectInventory(provenance.DefaultEnv(), nil)
}

// defaultHukouStep runs hukou's own adopted-tool upgrade in-process (the same
// path as `hukou upgrade --all`), which acquires the normal mutation lock only
// for its own duration.
func defaultHukouStep(stdout, stderr io.Writer) error {
	client := ghrelease.New(firstEnv("GITHUB_TOKEN", "GH_TOKEN"))
	return doUpgrade(stdout, stderr, nil, true, false, "", client)
}

// hashFile returns the SHA-256 of path, or "" when the file cannot be read (an
// unknown hash never fabricates a diff; see orchestrate.changeReasons).
func hashFile(path string) string {
	if path == "" {
		return ""
	}
	sum, err := verify.SHA256File(path)
	if err != nil {
		return ""
	}
	return sum
}

// doUp is the testable core of `hukou up`. The dry-run branch only reads and
// prints (never creating the data root, launching a subprocess, or holding a
// lock); the real branch runs managers through the constrained executor and
// reports the inventory diff.
func doUp(stdout, stderr io.Writer, opts upOptions, deps upDeps) error {
	managers, err := orchestrate.Filter(orchestrate.Registry(), opts.only, opts.skip)
	if err != nil {
		return fail(err)
	}
	detected := orchestrate.Detect(managers, deps.lookPath)

	if opts.dryRun {
		return doUpDryRun(stdout, opts, detected, deps.inventory)
	}
	return doUpRun(stdout, stderr, opts, detected, deps)
}

// doUpDryRun prints the read-only plan. It never touches the execution, hashing,
// or persistence seams.
func doUpDryRun(stdout io.Writer, opts upOptions, detected []orchestrate.Detected, inventory func() (output.Report, error)) error {
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

// doUpRun performs the real upgrade: pre-snapshot, run each available manager in
// registry order, post-snapshot, diff, persist history, report, and aggregate
// the exit status.
func doUpRun(stdout, stderr io.Writer, opts upOptions, detected []orchestrate.Detected, deps upDeps) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	preReport, err := deps.inventory()
	if err != nil {
		return fail(err)
	}
	output.Summarize(&preReport)
	preItems := snapItems(preReport, deps.hasher)

	results := make([]orchestrate.StepResult, 0, len(detected))
	for _, d := range detected {
		if !d.Available {
			continue
		}
		var res orchestrate.StepResult
		if d.Internal {
			res = runInternalHukou(stdout, stderr, deps.hukouStep)
		} else {
			res = deps.exec.RunManager(ctx, d.Name, d.Commands)
		}
		if !res.OK() {
			fmt.Fprintf(stderr, "manager %s %s: %v\n", res.Name, res.Status, res.Err)
		}
		results = append(results, res)
	}

	postReport, err := deps.inventory()
	if err != nil {
		return fail(err)
	}
	output.Summarize(&postReport)
	postItems := snapItems(postReport, deps.hasher)

	diff := orchestrate.ComputeDiff(preItems, postItems)

	stamp := deps.now().UTC().Format(time.RFC3339)
	snapDir, perr := persistSnapshotHistory(deps.dataRoot(), deps.now(),
		upSnapshot{Time: stamp, Report: preReport, Items: preItems},
		upSnapshot{Time: stamp, Report: postReport, Items: postItems},
		diff)
	if perr != nil {
		// History is a durable convenience; failing to write it must not mask the
		// upgrade outcome. Surface it and continue to the report and exit policy.
		fmt.Fprintf(stderr, "warning: failed to persist snapshot history: %v\n", perr)
	}

	if opts.json {
		if err := fail(writeUpRunJSON(stdout, results, diff, snapDir)); err != nil {
			return err
		}
	} else if err := fail(writeUpRunTable(stdout, results, diff, snapDir)); err != nil {
		return err
	}

	return aggregateExit(stderr, results)
}

// runInternalHukou runs the in-process hukou upgrade step and captures it as a
// StepResult so it joins the aggregate report and exit policy like any manager.
func runInternalHukou(stdout, stderr io.Writer, step func(stdout, stderr io.Writer) error) orchestrate.StepResult {
	start := time.Now()
	res := orchestrate.StepResult{Name: "hukou", Status: orchestrate.StatusOK}
	if err := step(stdout, stderr); err != nil {
		res.Status = orchestrate.StatusFailed
		res.Err = err
		res.ExitCode = 1
	}
	res.Duration = time.Since(start)
	return res
}

// aggregateExit implements the `upgrade --all` policy: any non-OK manager makes
// the overall result non-zero and prints the list of failures to stderr.
func aggregateExit(stderr io.Writer, results []orchestrate.StepResult) error {
	var failed []string
	for _, r := range results {
		if !r.OK() {
			failed = append(failed, r.Name)
		}
	}
	if len(failed) == 0 {
		return nil
	}
	fmt.Fprintf(stderr, "%d manager(s) failed: %s\n", len(failed), strings.Join(failed, ", "))
	return errUpManagersFailed
}

// snapItems flattens a scan Report into diff-keyed items, content-hashing the
// resolved real path (falling back to the PATH entry) so a content change is
// detected even when the version string is unchanged.
func snapItems(r output.Report, hasher func(string) string) []orchestrate.SnapItem {
	items := make([]orchestrate.SnapItem, 0, len(r.Rows))
	for _, row := range r.Rows {
		hashPath := row.Binary.RealPath
		if hashPath == "" {
			hashPath = row.Binary.Path
		}
		items = append(items, orchestrate.SnapItem{
			Name:    row.Binary.Name,
			Path:    row.Binary.Path,
			Source:  row.Attribution.Source,
			Version: row.Attribution.Version,
			SHA256:  hasher(hashPath),
		})
	}
	return items
}

// upSnapshot is one persisted inventory snapshot: the full scan Report plus the
// diff-keyed items (with content hashes) captured at that moment.
type upSnapshot struct {
	Time   string                 `json:"time"`
	Report output.Report          `json:"report"`
	Items  []orchestrate.SnapItem `json:"items"`
}

// persistSnapshotHistory writes the pre/post/diff triple under
// <root>/snapshots/<RFC3339>/ atomically (built in a temp dir, then renamed into
// place so a reader never sees a half-written run), then prunes to the last N
// runs — never deleting the run just written. It returns the final directory.
func persistSnapshotHistory(root string, now time.Time, pre, post upSnapshot, diff orchestrate.Diff) (string, error) {
	snapsDir := filepath.Join(root, "snapshots")
	if err := os.MkdirAll(snapsDir, 0o755); err != nil {
		return "", err
	}

	stamp := now.UTC().Format(time.RFC3339)
	finalDir := filepath.Join(snapsDir, stamp)
	for i := 1; ; i++ {
		if _, err := os.Lstat(finalDir); errors.Is(err, os.ErrNotExist) {
			break
		} else if err != nil {
			return "", err
		}
		finalDir = filepath.Join(snapsDir, fmt.Sprintf("%s-%d", stamp, i))
	}

	tmpDir, err := os.MkdirTemp(snapsDir, ".tmp-snap-*")
	if err != nil {
		return "", err
	}
	writeErr := func() error {
		if err := writeJSONFile(filepath.Join(tmpDir, "pre.json"), pre); err != nil {
			return err
		}
		if err := writeJSONFile(filepath.Join(tmpDir, "post.json"), post); err != nil {
			return err
		}
		return writeJSONFile(filepath.Join(tmpDir, "diff.json"), diff)
	}()
	if writeErr != nil {
		_ = os.RemoveAll(tmpDir)
		return "", writeErr
	}
	if err := os.Rename(tmpDir, finalDir); err != nil {
		_ = os.RemoveAll(tmpDir)
		return "", err
	}

	if err := pruneSnapshots(snapsDir, filepath.Base(finalDir), snapshotRetention); err != nil {
		return finalDir, err
	}
	return finalDir, nil
}

// pruneSnapshots keeps the newest `keep` snapshot directories (RFC3339 names
// sort chronologically) and removes the rest, never removing keepBase (the run
// just written) and ignoring in-progress .tmp-snap-* staging directories.
func pruneSnapshots(snapsDir, keepBase string, keep int) error {
	entries, err := os.ReadDir(snapsDir)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".tmp-snap-") {
			continue
		}
		names = append(names, e.Name())
	}
	if len(names) <= keep {
		return nil
	}
	sort.Strings(names)
	for _, name := range names[:len(names)-keep] {
		if name == keepBase {
			continue // never delete the run just written
		}
		if err := os.RemoveAll(filepath.Join(snapsDir, name)); err != nil {
			return err
		}
	}
	return nil
}

// writeJSONFile writes v as house-style indented JSON to path.
func writeJSONFile(path string, v any) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := output.WriteJSONValue(f, v); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
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

// upRunManagerJSON is one manager's outcome in the real-run report.
type upRunManagerJSON struct {
	Name     string `json:"name"`
	Status   string `json:"status"`
	Duration string `json:"duration"`
	Exit     int    `json:"exit"`
}

// upRunJSON is the `up --json` (real run) document.
type upRunJSON struct {
	SchemaVersion int                `json:"schema_version"`
	Managers      []upRunManagerJSON `json:"managers"`
	Diff          orchestrate.Diff   `json:"diff"`
	SnapshotDir   string             `json:"snapshot_dir"`
}

func writeUpRunJSON(w io.Writer, results []orchestrate.StepResult, diff orchestrate.Diff, snapDir string) error {
	doc := upRunJSON{
		SchemaVersion: 1,
		Managers:      make([]upRunManagerJSON, 0, len(results)),
		Diff:          diff,
		SnapshotDir:   snapDir,
	}
	for _, r := range results {
		doc.Managers = append(doc.Managers, upRunManagerJSON{
			Name:     r.Name,
			Status:   string(r.Status),
			Duration: r.Duration.Round(time.Millisecond).String(),
			Exit:     r.ExitCode,
		})
	}
	return output.WriteJSONValue(w, doc)
}

// writeUpRunTable renders the human real-run report: a manager-results table,
// the classified inventory diff grouped by source, and the print-only rollback
// hints.
func writeUpRunTable(w io.Writer, results []orchestrate.StepResult, diff orchestrate.Diff, snapDir string) error {
	fmt.Fprintln(w, "upgrade results:")
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tSTATUS\tEXIT\tDURATION")
	for _, r := range results {
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s\n", r.Name, r.Status, r.ExitCode, r.Duration.Round(time.Millisecond))
	}
	if err := tw.Flush(); err != nil {
		return err
	}

	fmt.Fprintln(w, "\ninventory changes:")
	if diff.Empty() {
		fmt.Fprintln(w, "  none")
	} else {
		if err := writeDiffTable(w, diff); err != nil {
			return err
		}
	}

	if err := writeRollbackHints(w, diff); err != nil {
		return err
	}

	if snapDir != "" {
		fmt.Fprintf(w, "\nsnapshot: %s\n", snapDir)
	}
	return nil
}

// writeDiffTable prints changed entries (NAME/SOURCE/BEFORE->AFTER) followed by
// added and removed one-liners, all grouped by source order via the sorted diff.
func writeDiffTable(w io.Writer, diff orchestrate.Diff) error {
	if len(diff.Changed) > 0 {
		tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "NAME\tSOURCE\tBEFORE\tAFTER")
		for _, c := range diff.Changed {
			before, after := changeCells(c)
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", c.Name, c.Source, before, after)
		}
		if err := tw.Flush(); err != nil {
			return err
		}
	}
	for _, it := range diff.Added {
		fmt.Fprintf(w, "  + %s (%s) %s\n", it.Name, it.Source, it.Path)
	}
	for _, it := range diff.Removed {
		fmt.Fprintf(w, "  - %s (%s) %s\n", it.Name, it.Source, it.Path)
	}
	return nil
}

// writeRollbackHints prints the advisory rollback surface for changed entries:
// a real `hukou rollback` for hukou-owned tools, and the recorded prior version
// plus the manager's own downgrade command (where one exists) for foreign ones.
// hukou executes none of these; they are printed only.
func writeRollbackHints(w io.Writer, diff orchestrate.Diff) error {
	if len(diff.Changed) == 0 {
		return nil
	}
	fmt.Fprintln(w, "\nrollback options (printed only; hukou runs none of these):")
	for _, c := range diff.Changed {
		if c.Source == "hukou" {
			fmt.Fprintf(w, "  %s: hukou rollback %s\n", c.Name, c.Name)
			continue
		}
		prev := c.BeforeVersion
		suggestion := orchestrate.DowngradeSuggestion(c.Source, c.Name, prev)
		switch {
		case suggestion != "":
			fmt.Fprintf(w, "  %s: was %s; downgrade: %s\n", c.Name, orDash(prev), suggestion)
		case prev != "":
			fmt.Fprintf(w, "  %s: was %s; no standard downgrade command for source %q\n", c.Name, prev, c.Source)
		default:
			fmt.Fprintf(w, "  %s: prior version unknown; no downgrade suggestion for source %q\n", c.Name, c.Source)
		}
	}
	return nil
}

// changeCells renders the BEFORE/AFTER columns: the version pair when the
// version moved, otherwise a short content-hash pair (a content-only change).
func changeCells(c orchestrate.Change) (before, after string) {
	if c.BeforeVersion != c.AfterVersion {
		return orDash(c.BeforeVersion), orDash(c.AfterVersion)
	}
	return "sha:" + shortHash(c.BeforeSHA256), "sha:" + shortHash(c.AfterSHA256)
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func shortHash(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	if h == "" {
		return "-"
	}
	return h
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

// ExitCode maps a top-level command error to a process exit status. `up` now
// executes for real, so the former exit-2 placeholder is gone: success is 0 and
// every failure (including an aggregate manager failure) is the generic 1.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	return 1
}
