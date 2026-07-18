// This file is the real-execution half of `hukou up` and the ONLY cmd file
// allowed to import the executor subpackage (enforced by up_guard_test.go):
// every function here is unreachable from the dry-run entry in up_plan.go.
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
	"github.com/rtwsvj/hukou/internal/verify"
)

// errUpManagersFailed is the aggregate error returned when at least one manager
// finished non-OK. It mirrors `upgrade --all`: the rest still run, the report is
// still written, and the process exits non-zero (status 1) at the end.
var errUpManagersFailed = errors.New("one or more managers failed")

// errSnapshotPersist is returned (possibly joined with errUpManagersFailed)
// when the snapshot history could not be persisted. A run whose evidence trail
// failed to land is a failed run: the report still prints, but the exit status
// is non-zero and the snapshot failure is recorded in the report itself.
var errSnapshotPersist = errors.New("snapshot history persistence failed")

// snapshotRetention is how many past snapshot runs are kept under
// <dataRoot>/snapshots; older runs are pruned after each real run (never the
// run just written).
const snapshotRetention = 10

// upDeps are the injectable seams of the real `up` run. Every side-effecting
// capability is a field so tests can drive the whole flow with a fake PATH, a
// fixture inventory, a stub executor, and a temp data root.
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

// runUpExecute is the real-run entry dispatched from runUp. In --json mode the
// executor's streamed manager output is routed to stderr so stdout carries
// nothing but the final JSON document.
func runUpExecute(stdout, stderr io.Writer, opts upOptions) error {
	streamOut := stdout
	if opts.json {
		streamOut = stderr
	}
	return doUpExecute(stdout, stderr, opts, productionUpDeps(streamOut, stderr))
}

// productionUpDeps wires the real seams: the shared read-only inventory, the
// constrained subprocess executor (streaming to streamOut/stderr), the
// in-process hukou upgrade step, real content hashing, and the real data root.
func productionUpDeps(streamOut, stderr io.Writer) upDeps {
	return upDeps{
		lookPath:  nil,
		inventory: defaultInventory,
		exec:      executor.New(streamOut, stderr),
		hukouStep: defaultHukouStep,
		hasher:    hashFile,
		now:       time.Now,
		dataRoot:  dataRoot,
	}
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

// doUpExecute is the testable core of a real `up` run: pre-snapshot, run each
// available manager in registry order, post-snapshot, diff, persist history,
// report, and aggregate the exit status. In --json mode all live output
// (manager streams and the internal hukou step) goes to stderr; stdout carries
// only the final JSON document.
func doUpExecute(stdout, stderr io.Writer, opts upOptions, deps upDeps) error {
	managers, err := orchestrate.Filter(orchestrate.Registry(), opts.only, opts.skip)
	if err != nil {
		return fail(err)
	}
	detected := orchestrate.Detect(managers, deps.lookPath)

	streamOut := stdout
	if opts.json {
		streamOut = stderr
	}

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
			res = runInternalHukou(streamOut, stderr, deps.hukouStep)
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
	snapDir, snapErr := persistSnapshotHistory(deps.dataRoot(), deps.now(),
		upSnapshot{Time: stamp, Report: preReport, Items: preItems},
		upSnapshot{Time: stamp, Report: postReport, Items: postItems},
		diff)
	if snapErr != nil {
		// The failure is surfaced three ways: immediately on stderr, in the
		// report's snapshot field, and in the aggregate (non-zero) exit below.
		fmt.Fprintf(stderr, "error: failed to persist snapshot history: %v\n", snapErr)
	}

	if opts.json {
		if err := fail(writeUpRunJSON(stdout, results, diff, snapDir, snapErr)); err != nil {
			return err
		}
	} else if err := fail(writeUpRunTable(stdout, results, diff, snapDir, snapErr)); err != nil {
		return err
	}

	return aggregateExit(stderr, results, snapErr)
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

// aggregateExit implements the `upgrade --all` policy extended to the snapshot
// trail: any non-OK manager, or a failed snapshot persistence, makes the
// overall result non-zero. Both failure kinds are summarized on stderr; both
// can be present at once (errors.Join).
func aggregateExit(stderr io.Writer, results []orchestrate.StepResult, snapErr error) error {
	var errs []error
	var failed []string
	for _, r := range results {
		if !r.OK() {
			failed = append(failed, r.Name)
		}
	}
	if len(failed) > 0 {
		fmt.Fprintf(stderr, "%d manager(s) failed: %s\n", len(failed), strings.Join(failed, ", "))
		errs = append(errs, errUpManagersFailed)
	}
	if snapErr != nil {
		errs = append(errs, fmt.Errorf("%w: %v", errSnapshotPersist, snapErr))
	}
	return errors.Join(errs...)
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

// upRunManagerJSON is one manager's outcome in the real-run report.
type upRunManagerJSON struct {
	Name     string `json:"name"`
	Status   string `json:"status"`
	Duration string `json:"duration"`
	Exit     int    `json:"exit"`
}

// upRunJSON is the `up --json` (real run) document. SnapshotError is empty on
// success; when persistence failed, SnapshotDir is empty and SnapshotError
// carries the reason (and the process exits non-zero).
type upRunJSON struct {
	SchemaVersion int                `json:"schema_version"`
	Managers      []upRunManagerJSON `json:"managers"`
	Diff          orchestrate.Diff   `json:"diff"`
	SnapshotDir   string             `json:"snapshot_dir"`
	SnapshotError string             `json:"snapshot_error,omitempty"`
}

func writeUpRunJSON(w io.Writer, results []orchestrate.StepResult, diff orchestrate.Diff, snapDir string, snapErr error) error {
	doc := upRunJSON{
		SchemaVersion: 1,
		Managers:      make([]upRunManagerJSON, 0, len(results)),
		Diff:          diff,
		SnapshotDir:   snapDir,
	}
	if snapErr != nil {
		doc.SnapshotError = snapErr.Error()
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
// the classified inventory diff grouped by source, the print-only rollback
// hints, and the snapshot location (or its persistence failure).
func writeUpRunTable(w io.Writer, results []orchestrate.StepResult, diff orchestrate.Diff, snapDir string, snapErr error) error {
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

	switch {
	case snapErr != nil:
		if _, err := fmt.Fprintf(w, "\nsnapshot: FAILED (%v)\n", snapErr); err != nil {
			return err
		}
	case snapDir != "":
		if _, err := fmt.Fprintf(w, "\nsnapshot: %s\n", snapDir); err != nil {
			return err
		}
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
