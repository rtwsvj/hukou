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
	"time"

	"github.com/rtwsvj/hukou/internal/durablefs"
	"github.com/rtwsvj/hukou/internal/ghrelease"
	"github.com/rtwsvj/hukou/internal/i18n"
	"github.com/rtwsvj/hukou/internal/orchestrate"
	"github.com/rtwsvj/hukou/internal/orchestrate/executor"
	"github.com/rtwsvj/hukou/internal/output"
	"github.com/rtwsvj/hukou/internal/state"
	"github.com/rtwsvj/hukou/internal/verify"
)

// errUpManagersFailed is the aggregate error returned when at least one manager
// finished non-OK. It mirrors `upgrade --all`: the rest still run, the report is
// still written, and the process exits non-zero (status 1) at the end.
var errUpManagersFailed = i18n.Errorf("one or more managers failed")

// errSnapshotPersist is returned (possibly joined with errUpManagersFailed)
// when the snapshot history could not be persisted. A run whose evidence trail
// failed to land is a failed run: the report still prints, but the exit status
// is non-zero and the snapshot failure is recorded in the report itself.
var errSnapshotPersist = i18n.Errorf("snapshot history persistence failed")

// errRunCanceled is returned when an interrupt (SIGINT/SIGTERM) stopped the run
// before it finished every manager. The report still prints; the exit is
// non-zero.
var errRunCanceled = i18n.Errorf("run canceled by signal")

// newStepExecutor constructs the production subprocess executor. It is a
// package var so a test can drive the REAL cobra dry-run dispatch with a
// fatal-on-call fake and prove the dry-run path never constructs or calls it
// (see cmd/up_dispatch_guard_test.go,
// TestDryRunDispatchNeverConstructsOrCallsExecutor). timeout and timeouts are
// the already-resolved upOptions values (0 = executor default).
var newStepExecutor = func(streamOut, stderr io.Writer, timeout time.Duration, timeouts map[string]time.Duration) orchestrate.StepExecutor {
	e := executor.New(streamOut, stderr)
	e.Timeout = timeout
	e.Timeouts = timeouts
	return e
}

// resolveUpTimeout resolves the base per-manager timeout for `up`: an explicit
// --timeout flag wins, then the HUKOU_UP_TIMEOUT environment variable; 0 means
// "unset", which the entry points normalize to the executor default. An
// explicit but invalid value is an error — a mistyped duration must never
// silently fall back to the default.
func resolveUpTimeout(flag time.Duration, getenv func(string) string) (time.Duration, error) {
	if flag != 0 {
		if flag < 0 {
			return 0, i18n.Errorf("--timeout must be positive, got %s", flag)
		}
		return flag, nil
	}
	if v := getenv("HUKOU_UP_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return 0, i18n.Errorf("invalid HUKOU_UP_TIMEOUT %q: must be a positive duration (e.g. 30m)", v)
		}
		return d, nil
	}
	return 0, nil
}

// parseManagerTimeouts parses the repeatable --manager-timeout name=duration
// flag into a per-manager override map. Names are validated against the
// registry so a typo errors out instead of silently matching nothing; the
// internal hukou step is rejected because it never runs through the executor.
func parseManagerTimeouts(entries []string) (map[string]time.Duration, error) {
	if len(entries) == 0 {
		return nil, nil
	}
	known := make(map[string]bool) // registry name → internal?
	for _, m := range orchestrate.Registry() {
		known[m.Name] = m.Internal
	}
	out := make(map[string]time.Duration, len(entries))
	for _, entry := range entries {
		name, value, ok := strings.Cut(entry, "=")
		if !ok || name == "" || value == "" {
			return nil, i18n.Errorf("invalid --manager-timeout %q: want name=duration (e.g. brew=45m)", entry)
		}
		internal, exists := known[name]
		if !exists {
			return nil, &orchestrate.UnknownManagerError{Name: name}
		}
		if internal {
			return nil, i18n.Errorf("--manager-timeout does not apply to the internal %s step", name)
		}
		d, err := time.ParseDuration(value)
		if err != nil || d <= 0 {
			return nil, i18n.Errorf("invalid --manager-timeout %q: must be a positive duration (e.g. 45m)", entry)
		}
		out[name] = d
	}
	return out, nil
}

// effectiveTimeout resolves the timeout one manager will actually run under:
// its per-name override, else the base timeout, else the executor default. It
// lives here (not in up_plan.go) so the plan path never imports the executor
// package, while the dry-run table can still show the enforced value.
func effectiveTimeout(opts upOptions, name string) time.Duration {
	if d, ok := opts.managerTimeouts[name]; ok && d > 0 {
		return d
	}
	if opts.timeout > 0 {
		return opts.timeout
	}
	return executor.DefaultTimeout
}

// snapshotRetention is how many past snapshot runs are kept under
// <dataRoot>/snapshots; older runs are pruned after each real run (never the
// run just written).
const snapshotRetention = 10

// tmpSnapStaleAge is how old a .tmp-snap-* staging directory must be before
// pruneSnapshots treats it as abandoned crash residue and removes it.
const tmpSnapStaleAge = 24 * time.Hour

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
	// managers run). The ctx carries the step's soft budget.
	hukouStep func(ctx context.Context, stdout, stderr io.Writer) error
	// hasher content-hashes a binary path for snapshot diffing ("" on failure).
	hasher func(path string) string
	// now stamps the snapshot history directory.
	now func() time.Time
	// dataRoot resolves the hukou data directory that owns snapshots/.
	dataRoot func() string
	// baseContext builds the run's root context and its stop func. Production
	// wires signal.NotifyContext so a terminal Ctrl-C cancels the run; tests
	// inject a context they can cancel to exercise interruption.
	baseContext func() (context.Context, context.CancelFunc)
}

// runUpExecute is the real-run entry dispatched from runUp. In --json mode the
// executor's streamed manager output is routed to stderr so stdout carries
// nothing but the final JSON document.
func runUpExecute(stdout, stderr io.Writer, opts upOptions) error {
	streamOut := stdout
	if opts.json {
		streamOut = stderr
	}
	return doUpExecute(stdout, stderr, opts, productionUpDeps(streamOut, stderr, opts))
}

// productionUpDeps wires the real seams: the shared read-only inventory, the
// constrained subprocess executor (streaming to streamOut/stderr, with the
// resolved timeout policy), the in-process hukou upgrade step, real content
// hashing, and the real data root.
func productionUpDeps(streamOut, stderr io.Writer, opts upOptions) upDeps {
	return upDeps{
		lookPath:  nil,
		inventory: defaultInventory,
		exec:      newStepExecutor(streamOut, stderr, opts.timeout, opts.managerTimeouts),
		hukouStep: defaultHukouStep,
		hasher:    hashFile,
		now:       time.Now,
		dataRoot:  dataRoot,
		baseContext: func() (context.Context, context.CancelFunc) {
			// On unix each manager runs in its OWN process group, so a terminal
			// Ctrl-C no longer reaches it via the tty; this NotifyContext cancel
			// is what delivers the interrupt, and the executor's Cancel hook then
			// terminates the manager's whole group. The same cancel also stops
			// the manager loop and the internal hukou step.
			return signal.NotifyContext(context.Background(), interruptSignals()...)
		},
	}
}

// defaultHukouStep runs hukou's own adopted-tool upgrade in-process (the same
// path as `hukou upgrade --all`), which acquires the normal mutation lock only
// for its own duration. ctx carries the step's soft budget, checked at tool
// boundaries inside doUpgradeCtx.
func defaultHukouStep(ctx context.Context, stdout, stderr io.Writer) error {
	client := ghrelease.New(firstEnv("GITHUB_TOKEN", "GH_TOKEN"))
	return doUpgradeCtx(ctx, stdout, stderr, nil, true, false, "", client, false)
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
// only the final JSON document. The whole run holds the cross-process up lock
// (dry-run takes none — see doUpPlan).
func doUpExecute(stdout, stderr io.Writer, opts upOptions, deps upDeps) error {
	managers, err := orchestrate.Filter(orchestrate.Registry(), opts.only, opts.skip)
	if err != nil {
		return fail(err)
	}
	detected := orchestrate.Detect(managers, deps.lookPath)

	// Lock ordering is unidirectional: up.lock first, the internal hukou
	// step's state.lock (acquireMutationLock) later and strictly nested, so
	// the two locks can never deadlock. The lock is taken only after flag
	// validation and detection, so `up --only typo` errors without creating
	// the data root or the lock file.
	upLock, err := acquireUpLock(deps.dataRoot())
	if err != nil {
		return fail(err)
	}
	defer func() {
		if err := upLock.Release(); err != nil {
			fmt.Fprintf(stderr, "warning: failed to release the hukou up lock: %v\n", err)
		}
	}()

	streamOut := stdout
	if opts.json {
		streamOut = stderr
	}

	ctx, stop := deps.baseContext()
	defer stop()

	preReport, err := deps.inventory()
	if err != nil {
		return fail(err)
	}
	output.Summarize(&preReport)
	preItems := snapItems(preReport, deps.hasher)

	results := make([]orchestrate.StepResult, 0, len(detected))
	for _, d := range detected {
		// Unavailable managers are reported as skipped, not silently dropped,
		// so the step trail explains itself line by line.
		if !d.Available {
			fmt.Fprintf(stderr, "%s\n", i18n.T("==> %s: skipped (not found on PATH)", d.Name))
			continue
		}
		// Interrupt check, evaluated before EACH manager — external or the
		// internal hukou step alike: once the run is canceled we stop launching
		// managers, record a canceled marker for the one we would have run next,
		// and fall through to still snapshot/diff/report what already happened.
		if ctx.Err() != nil {
			fmt.Fprintf(stderr, "%s\n", i18n.T("run canceled before manager %s", d.Name))
			results = append(results, orchestrate.StepResult{
				Name:   d.Name,
				Status: orchestrate.StatusCanceled,
				Err:    ctx.Err(),
			})
			break
		}
		if d.Internal {
			fmt.Fprintf(stderr, "%s\n", i18n.T("==> %s: in-process upgrade", d.Name))
		} else {
			fmt.Fprintf(stderr, "%s\n", i18n.T("==> %s: %s", d.Name, renderUpCommands(d.Commands)))
		}
		var res orchestrate.StepResult
		if d.Internal {
			res = runInternalHukou(ctx, streamOut, stderr, deps.hukouStep, effectiveTimeout(opts, d.Name))
			// Known limitation, deliberate (docs/05, spec): the internal hukou
			// step is an in-process, WAL-protected batch that cannot be safely
			// interrupted mid-transaction, so cancellation is observed only at
			// its boundaries — before it starts (the loop check above), at its
			// per-tool boundaries (its own soft budget, enforced inside
			// doUpgradeCtx), and here, after it returns. If the run was
			// canceled while it ran, an "ok" result is reclassified as
			// canceled so the run can never report success / exit 0 after an
			// interrupt.
			if ctx.Err() != nil && res.Status == orchestrate.StatusOK {
				res.Status = orchestrate.StatusCanceled
				res.Err = ctx.Err()
			}
		} else {
			// Retry policy: an external manager failure is retried up to
			// --retry times while the run context is still live; the internal
			// in-process step is deliberately never retried (its WAL semantics
			// make an immediate re-run deterministic, not corrective). All
			// attempts of one manager share ONE timeout budget (mgrCtx): a
			// retry restarts the work, not the clock, and a manager that hit
			// its timeout is never retried.
			mgrCtx, mgrCancel := context.WithTimeout(ctx, effectiveTimeout(opts, d.Name))
			res = deps.exec.RunManager(mgrCtx, d.Name, d.Commands)
			for attempt := 1; attempt <= opts.retries && !res.OK() && res.Status != orchestrate.StatusTimeout && mgrCtx.Err() == nil; attempt++ {
				fmt.Fprintf(stderr, "%s\n", i18n.T("   retry %d/%d for %s", attempt, opts.retries, d.Name))
				res = deps.exec.RunManager(mgrCtx, d.Name, d.Commands)
			}
			mgrCancel()
		}
		switch {
		case res.Status == orchestrate.StatusCanceled:
			fmt.Fprintf(stderr, "%s\n", i18n.T("   canceled %s", d.Name))
		case res.OK():
			fmt.Fprintf(stderr, "%s\n", i18n.T("   ok %s (%s)", d.Name, res.Duration.Round(time.Millisecond)))
		default:
			fmt.Fprintf(stderr, "%s\n", i18n.T("   FAILED %s (exit %d)", d.Name, res.ExitCode))
		}
		if !res.OK() {
			fmt.Fprintf(stderr, "%s\n", i18n.T("manager %s %s: %v", res.Name, res.Status, res.Err))
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
		diff, results, stderr)
	if snapErr != nil {
		// The failure is surfaced three ways: immediately on stderr, in the
		// report's snapshot field, and in the aggregate (non-zero) exit below.
		fmt.Fprintf(stderr, "%s\n", i18n.T("error: failed to persist snapshot history: %v", snapErr))
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

// acquireUpLock takes the cross-process `hukou up` lock (non-blocking): a
// second concurrent up run fails immediately instead of interleaving manager
// upgrades and snapshot history with the first. Dry-run never comes here.
func acquireUpLock(root string) (*state.Lock, error) {
	if err := durablefs.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	lockPath := filepath.Join(root, "up.lock")
	lock, err := state.Acquire(lockPath)
	if err != nil {
		if errors.Is(err, state.ErrLocked) {
			return nil, i18n.Errorf("another `hukou up` is already running (lock: %s)", lockPath)
		}
		return nil, err
	}
	return lock, nil
}

// runInternalHukou runs the in-process hukou upgrade step and captures it as a
// StepResult so it joins the aggregate report and exit policy like any
// manager. The step gets its own soft budget (same magnitude as an external
// manager's timeout): enforcement happens only at tool boundaries inside
// doUpgradeCtx — a running tool's WAL transaction is never interrupted — so
// an expired budget shows up here as a canceled result, mirroring the
// parent-cancel reclassification in doUpExecute.
func runInternalHukou(ctx context.Context, stdout, stderr io.Writer, step func(ctx context.Context, stdout, stderr io.Writer) error, budget time.Duration) orchestrate.StepResult {
	stepCtx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	start := time.Now()
	res := orchestrate.StepResult{Name: "hukou", Status: orchestrate.StatusOK}
	if err := step(stepCtx, stdout, stderr); err != nil {
		res.Status = orchestrate.StatusFailed
		res.Err = err
		res.ExitCode = 1
	}
	if stepCtx.Err() != nil && res.Status == orchestrate.StatusOK {
		res.Status = orchestrate.StatusCanceled
		res.Err = stepCtx.Err()
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
	var failed, canceled []string
	for _, r := range results {
		switch {
		case r.Status == orchestrate.StatusCanceled:
			canceled = append(canceled, r.Name)
		case !r.OK():
			failed = append(failed, r.Name)
		}
	}
	if len(failed) > 0 {
		fmt.Fprintf(stderr, "%s\n", i18n.T("%d manager(s) failed: %s", len(failed), strings.Join(failed, ", ")))
		errs = append(errs, errUpManagersFailed)
	}
	if len(canceled) > 0 {
		fmt.Fprintf(stderr, "%s\n", i18n.T("run canceled; not completed: %s", strings.Join(canceled, ", ")))
		errs = append(errs, errRunCanceled)
	}
	if snapErr != nil {
		errs = append(errs, i18n.Wrapf("%w: %v", errSnapshotPersist, snapErr))
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

// persistSnapshotHistory writes the pre/post/diff/run quadruple under
// <root>/snapshots/<RFC3339>/ atomically (built in a temp dir, then renamed into
// place so a reader never sees a half-written run), then prunes to the last N
// runs — never deleting the run just written. It returns the final directory.
// run.json (the manager results plus the run's stamp) joins the same atomic
// stage so `up history`/`up show` can re-render what a run did after the
// terminal has scrolled; results may be nil for a run with no managers. A
// prune failure after a successful persist is housekeeping, not a persistence
// failure: it is a stderr warning, never the returned error.
func persistSnapshotHistory(root string, now time.Time, pre, post upSnapshot, diff orchestrate.Diff, results []orchestrate.StepResult, stderr io.Writer) (string, error) {
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
	// run.json records the logical stamp (the RFC3339 that names the directory,
	// pre-collision-suffix), consistent with pre/post's Time field.
	run := upRunDoc{SchemaVersion: 1, Time: stamp, Managers: toManagerJSONs(results)}
	writeErr := func() error {
		if err := writeJSONFile(filepath.Join(tmpDir, "pre.json"), pre); err != nil {
			return err
		}
		if err := writeJSONFile(filepath.Join(tmpDir, "post.json"), post); err != nil {
			return err
		}
		if err := writeJSONFile(filepath.Join(tmpDir, "diff.json"), diff); err != nil {
			return err
		}
		return writeJSONFile(filepath.Join(tmpDir, "run.json"), run)
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
		// The snapshot itself landed; a prune failure downgrades to a warning
		// and must not poison the run's exit status (aggregateExit's snapErr).
		fmt.Fprintf(stderr, "%s\n", i18n.T("warning: failed to prune old snapshots: %v", err))
	}
	return finalDir, nil
}

// pruneSnapshots keeps the newest `keep` snapshot directories (RFC3339 names
// sort chronologically) and removes the rest, never removing keepBase (the run
// just written) and ignoring in-progress .tmp-snap-* staging directories.
// Only hukou-generated stamp directories participate: a foreign directory
// archived into snapshots/ by hand is never counted or deleted. Staging
// directories older than tmpSnapStaleAge are abandoned crash residue and are
// cleaned up here in passing.
func pruneSnapshots(snapsDir, keepBase string, keep int) error {
	entries, err := os.ReadDir(snapsDir)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if strings.HasPrefix(e.Name(), ".tmp-snap-") {
			// A staging dir that outlived its writer is residue; a fresh one
			// may belong to a persist in flight (impossible under the up
			// lock, but the 24h margin makes the race theoretical anyway).
			info, statErr := e.Info()
			if statErr == nil && time.Since(info.ModTime()) > tmpSnapStaleAge {
				if err := os.RemoveAll(filepath.Join(snapsDir, e.Name())); err != nil {
					return err
				}
			}
			continue
		}
		if !isSnapshotStampDir(e.Name()) {
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

// isSnapshotStampDir reports whether name is a hukou-generated snapshot
// directory name: an RFC3339 UTC stamp, optionally with the -N collision
// suffix persistSnapshotHistory appends on a same-second clash.
func isSnapshotStampDir(name string) bool {
	base := name
	if i := strings.LastIndexByte(name, '-'); i >= 0 && isDigits(name[i+1:]) {
		base = name[:i]
	}
	_, err := time.Parse(time.RFC3339, base)
	return err == nil
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
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

// upRunManagerJSON is one manager's outcome, shared by the live-run report and
// the persisted run.json (read back by `up show`/`up history`).
type upRunManagerJSON struct {
	Name     string `json:"name"`
	Status   string `json:"status"`
	Duration string `json:"duration"`
	Exit     int    `json:"exit"`
}

// upRunDoc is the persisted run.json: the manager results plus the run's stamp,
// staged atomically alongside pre/post/diff. schema_version is 1. Persisting the
// manager outcomes is what lets a past run's "what changed and how do I roll it
// back" survive the terminal scrolling; pre-U3 directories predate it and are
// tolerated as absent by the read-back surface.
type upRunDoc struct {
	SchemaVersion int                `json:"schema_version"`
	Time          string             `json:"time"`
	Managers      []upRunManagerJSON `json:"managers"`
}

// toManagerJSONs projects step results into the serialized manager shape shared
// by the live report, run.json, and the `up show` re-render (so the rounded
// duration string and status text read identically everywhere).
func toManagerJSONs(results []orchestrate.StepResult) []upRunManagerJSON {
	managers := make([]upRunManagerJSON, 0, len(results))
	for _, r := range results {
		managers = append(managers, upRunManagerJSON{
			Name:     r.Name,
			Status:   string(r.Status),
			Duration: r.Duration.Round(time.Millisecond).String(),
			Exit:     r.ExitCode,
		})
	}
	return managers
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
		Managers:      toManagerJSONs(results),
		Diff:          diff,
		SnapshotDir:   snapDir,
	}
	if snapErr != nil {
		doc.SnapshotError = snapErr.Error()
	}
	return output.WriteJSONValue(w, doc)
}

// writeUpRunTable renders the human real-run report: a manager-results table,
// the classified inventory diff grouped by source, the print-only rollback
// hints, and the snapshot location (or its persistence failure).
func writeUpRunTable(w io.Writer, results []orchestrate.StepResult, diff orchestrate.Diff, snapDir string, snapErr error) error {
	if err := writeManagerResultsTable(w, toManagerJSONs(results)); err != nil {
		return err
	}

	fmt.Fprintln(w, "\n"+i18n.T("inventory changes:"))
	if diff.Empty() {
		fmt.Fprintln(w, i18n.T("  none"))
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
		if _, err := fmt.Fprintf(w, "\n%s\n", i18n.T("snapshot: FAILED (%v)", snapErr)); err != nil {
			return err
		}
	case snapDir != "":
		if _, err := fmt.Fprintf(w, "\n%s\n", i18n.T("snapshot: %s", snapDir)); err != nil {
			return err
		}
	}
	return nil
}

// writeManagerResultsTable renders the NAME/STATUS/EXIT/DURATION table shared by
// the live real-run report and the `up show` re-render, so a run reads the same
// whether printed as it happens or replayed from run.json later.
func writeManagerResultsTable(w io.Writer, managers []upRunManagerJSON) error {
	fmt.Fprintln(w, i18n.T("upgrade results:"))
	t := output.NewTable(w, i18n.T("NAME"), i18n.T("STATUS"), i18n.T("EXIT"), i18n.T("DURATION"))
	for _, m := range managers {
		t.Row(m.Name, m.Status, fmt.Sprintf("%d", m.Exit), m.Duration)
	}
	return t.Flush()
}

// writeDiffTable prints changed entries (NAME/SOURCE/BEFORE->AFTER) followed by
// added and removed one-liners, all grouped by source order via the sorted diff.
func writeDiffTable(w io.Writer, diff orchestrate.Diff) error {
	if len(diff.Changed) > 0 {
		t := output.NewTable(w, i18n.T("NAME"), i18n.T("SOURCE"), i18n.T("BEFORE"), i18n.T("AFTER"))
		for _, c := range diff.Changed {
			before, after := changeCells(c)
			t.Row(c.Name, c.Source, before, after)
		}
		if err := t.Flush(); err != nil {
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
	fmt.Fprintln(w, "\n"+i18n.T("rollback options (printed only; hukou runs none of these):"))
	for _, c := range diff.Changed {
		if c.Source == "hukou" {
			fmt.Fprintf(w, "  %s: hukou rollback %s\n", c.Name, c.Name)
			continue
		}
		prev := c.BeforeVersion
		suggestion := orchestrate.DowngradeSuggestion(c.Source, c.Name, prev)
		switch {
		case suggestion != "":
			fmt.Fprintf(w, "%s\n", i18n.T("  %s: was %s; downgrade: %s", c.Name, orDash(prev), suggestion))
		case prev != "":
			fmt.Fprintf(w, "%s\n", i18n.T("  %s: was %s; no standard downgrade command for source %q", c.Name, prev, c.Source))
		default:
			fmt.Fprintf(w, "%s\n", i18n.T("  %s: prior version unknown; no downgrade suggestion for source %q", c.Name, c.Source))
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

// renderUpCommands joins each argv with spaces and multiple steps with " && "
// for display only (mirrors the plan renderer in internal/orchestrate/plan).
func renderUpCommands(cmds [][]string) string {
	parts := make([]string, 0, len(cmds))
	for _, argv := range cmds {
		parts = append(parts, strings.Join(argv, " "))
	}
	return strings.Join(parts, " && ")
}
