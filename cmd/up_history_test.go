package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/rtwsvj/hukou/internal/orchestrate"
)

// --- fixture helpers -------------------------------------------------------

// writeJSONFixture marshals v to path (house-agnostic; the readers only need
// valid JSON), failing the test on error.
func writeJSONFixture(t *testing.T, path string, v any) {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// seedRun creates a snapshot run directory <snapsDir>/<id>, writing diff.json
// when diff != nil and run.json when run != nil. A nil diff models an incomplete
// run; a nil run models a pre-U3 run.
func seedRun(t *testing.T, snapsDir, id string, diff *orchestrate.Diff, run *upRunDoc) {
	t.Helper()
	dir := filepath.Join(snapsDir, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if diff != nil {
		writeJSONFixture(t, filepath.Join(dir, "diff.json"), diff)
	}
	if run != nil {
		writeJSONFixture(t, filepath.Join(dir, "run.json"), run)
	}
}

func diffFixture(changed, added, removed int) *orchestrate.Diff {
	d := &orchestrate.Diff{}
	for i := 0; i < changed; i++ {
		d.Changed = append(d.Changed, orchestrate.Change{
			Name: "tsc", Path: "/n/tsc", Source: "npm",
			BeforeVersion: "5.3.0", AfterVersion: "5.4.0", Reasons: []string{"version"},
		})
	}
	for i := 0; i < added; i++ {
		d.Added = append(d.Added, orchestrate.SnapItem{Name: "newtool", Path: "/n/newtool", Source: "npm", Version: "1.0"})
	}
	for i := 0; i < removed; i++ {
		d.Removed = append(d.Removed, orchestrate.SnapItem{Name: "old", Path: "/n/old", Source: "npm", Version: "0.9"})
	}
	return d
}

func runDocFixture(managers ...upRunManagerJSON) *upRunDoc {
	return &upRunDoc{SchemaVersion: 1, Time: "2026-07-18T12:00:00Z", Managers: managers}
}

func mgr(name, status string) upRunManagerJSON {
	return upRunManagerJSON{Name: name, Status: status, Duration: "1s"}
}

// --- run.json persistence --------------------------------------------------

// TestPersistSnapshotHistory_WritesRunJSONRoundTrip proves run.json joins the
// atomic pre/post/diff stage and round-trips (schema_version, stamp, managers).
func TestPersistSnapshotHistory_WritesRunJSONRoundTrip(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	results := []orchestrate.StepResult{
		{Name: "brew", Status: orchestrate.StatusOK, Duration: 1200 * time.Millisecond},
		{Name: "npm", Status: orchestrate.StatusFailed, ExitCode: 2, Duration: 500 * time.Millisecond},
	}

	dir, err := persistSnapshotHistory(root, now, upSnapshot{Time: "pre"}, upSnapshot{Time: "post"}, orchestrate.Diff{}, results)
	if err != nil {
		t.Fatalf("persist: %v", err)
	}
	// run.json landed atomically with the triple; no staging dir survived.
	assertNoTmpStaging(t, filepath.Join(root, "snapshots"))
	for _, f := range []string{"pre.json", "post.json", "diff.json", "run.json"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Fatalf("missing %s: %v", f, err)
		}
	}

	var got upRunDoc
	readJSON(t, filepath.Join(dir, "run.json"), &got)
	if got.SchemaVersion != 1 {
		t.Fatalf("schema_version = %d, want 1", got.SchemaVersion)
	}
	if got.Time != "2026-07-18T12:00:00Z" {
		t.Fatalf("time = %q, want the run stamp / directory basename", got.Time)
	}
	if len(got.Managers) != 2 {
		t.Fatalf("managers = %d, want 2", len(got.Managers))
	}
	if got.Managers[0].Name != "brew" || got.Managers[0].Status != "ok" || got.Managers[0].Duration != "1.2s" {
		t.Fatalf("brew result round-trip = %+v", got.Managers[0])
	}
	if got.Managers[1].Status != "failed" || got.Managers[1].Exit != 2 {
		t.Fatalf("npm result round-trip = %+v", got.Managers[1])
	}
}

// --- up history ------------------------------------------------------------

// TestUpHistory_ListsRunsNewestFirst covers a mixed snapshots dir: a complete
// run, a pre-U3 run (no run.json), an incomplete run (no diff.json), a leftover
// .tmp-snap-* staging dir, and a stray regular file. Listing must show the three
// real runs correctly (newest first, including a "-N" collision name), and
// ignore the staging dir and stray file.
func TestUpHistory_ListsRunsNewestFirst(t *testing.T) {
	snapsDir := t.TempDir()

	// (a) complete: 2 ok / 1 failed.
	seedRun(t, snapsDir, "2026-07-18T12:00:02Z", diffFixture(1, 1, 0),
		runDocFixture(mgr("brew", "ok"), mgr("npm", "failed"), mgr("hukou", "ok")))
	// (b) pre-U3: diff present, no run.json.
	seedRun(t, snapsDir, "2026-07-18T12:00:01Z", diffFixture(0, 1, 0), nil)
	// complete + its collision-suffixed sibling (written later on a same-second run).
	seedRun(t, snapsDir, "2026-07-18T12:00:00Z", diffFixture(0, 0, 0), runDocFixture(mgr("brew", "ok")))
	seedRun(t, snapsDir, "2026-07-18T12:00:00Z-1", diffFixture(0, 0, 0), runDocFixture(mgr("brew", "ok")))
	// (c) incomplete: no diff.json.
	seedRun(t, snapsDir, "2026-07-17T00:00:00Z", nil, nil)
	// (d) leftover staging dir and (e) stray file — both ignored.
	if err := os.MkdirAll(filepath.Join(snapsDir, ".tmp-snap-abcd"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(snapsDir, "notadir.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Structured order + summaries via --json.
	var out bytes.Buffer
	if err := doUpHistory(&out, snapsDir, true); err != nil {
		t.Fatalf("history --json: %v", err)
	}
	var doc upHistoryJSONDoc
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("decode history json: %v\n%s", err, out.String())
	}
	if doc.SchemaVersion != 1 {
		t.Fatalf("schema_version = %d, want 1", doc.SchemaVersion)
	}
	wantOrder := []string{
		"2026-07-18T12:00:02Z",
		"2026-07-18T12:00:01Z",
		"2026-07-18T12:00:00Z-1",
		"2026-07-18T12:00:00Z",
		"2026-07-17T00:00:00Z",
	}
	if len(doc.Runs) != len(wantOrder) {
		t.Fatalf("listed %d runs, want %d (staging dir + stray file must be ignored): %+v", len(doc.Runs), len(wantOrder), doc.Runs)
	}
	for i, want := range wantOrder {
		if doc.Runs[i].ID != want {
			t.Fatalf("run[%d].id = %q, want %q (newest-first order)", i, doc.Runs[i].ID, want)
		}
	}
	// (a) complete run's manager summary.
	if a := doc.Runs[0]; a.Managers == nil || a.Managers.OK != 2 || a.Managers.Failed != 1 {
		t.Fatalf("complete run managers = %+v, want {ok:2 failed:1}", a.Managers)
	}
	if doc.Runs[0].Changed != 1 || doc.Runs[0].Added != 1 {
		t.Fatalf("complete run counts = changed %d added %d, want 1/1", doc.Runs[0].Changed, doc.Runs[0].Added)
	}
	// (b) pre-U3: no run.json -> null managers, but diff counts still present.
	if b := doc.Runs[1]; b.Managers != nil {
		t.Fatalf("pre-U3 run managers = %+v, want null", b.Managers)
	}
	if doc.Runs[1].Added != 1 {
		t.Fatalf("pre-U3 run added = %d, want 1", doc.Runs[1].Added)
	}
	// (c) incomplete: no diff.json.
	if c := doc.Runs[4]; !c.Incomplete {
		t.Fatalf("run with no diff.json not marked incomplete: %+v", c)
	}

	// Human table: pre-U3 shows "-", incomplete shows "(incomplete)".
	out.Reset()
	if err := doUpHistory(&out, snapsDir, false); err != nil {
		t.Fatalf("history table: %v", err)
	}
	human := out.String()
	for _, want := range []string{
		"2026-07-18T12:00:02Z", "2 ok / 1 failed",
		"(incomplete)",
	} {
		if !strings.Contains(human, want) {
			t.Fatalf("history table missing %q:\n%s", want, human)
		}
	}
	// The pre-U3 row's manager cell is "-".
	if !containsPreU3Dash(human, "2026-07-18T12:00:01Z") {
		t.Fatalf("pre-U3 row does not show '-' for managers:\n%s", human)
	}
	if strings.Contains(human, ".tmp-snap-") || strings.Contains(human, "notadir") {
		t.Fatalf("history table leaked an ignored entry:\n%s", human)
	}
}

// containsPreU3Dash asserts the row for id ends with a "-" manager cell.
func containsPreU3Dash(table, id string) bool {
	for _, line := range strings.Split(table, "\n") {
		if strings.HasPrefix(line, id) {
			return strings.HasSuffix(strings.TrimRight(line, " "), "-")
		}
	}
	return false
}

// TestUpHistory_NoSnapshotsDirRecordsNothingAndCreatesNothing proves the empty
// state: a missing snapshots directory prints "no up runs recorded", returns nil
// (exit 0), and — critically — does NOT create the data root or the dir.
func TestUpHistory_NoSnapshotsDirRecordsNothingAndCreatesNothing(t *testing.T) {
	root := t.TempDir()
	dataRootDir := filepath.Join(root, "data") // never created by hukou here
	snapsDir := filepath.Join(dataRootDir, "snapshots")

	var out bytes.Buffer
	if err := doUpHistory(&out, snapsDir, false); err != nil {
		t.Fatalf("history over a missing dir must succeed, got: %v", err)
	}
	if !strings.Contains(out.String(), "no up runs recorded") {
		t.Fatalf("missing empty-state message:\n%s", out.String())
	}
	if _, err := os.Stat(dataRootDir); !os.IsNotExist(err) {
		t.Fatalf("data root was created by a read-only history call: stat err = %v", err)
	}

	// --json over the empty state is still a valid, pure JSON document.
	out.Reset()
	if err := doUpHistory(&out, snapsDir, true); err != nil {
		t.Fatalf("history --json empty: %v", err)
	}
	var doc upHistoryJSONDoc
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("empty history --json is not valid JSON: %v\n%s", err, out.String())
	}
	if doc.SchemaVersion != 1 || len(doc.Runs) != 0 {
		t.Fatalf("empty history --json = %+v, want schema 1 and no runs", doc)
	}
	if _, err := os.Stat(dataRootDir); !os.IsNotExist(err) {
		t.Fatalf("data root was created by a read-only history --json call: stat err = %v", err)
	}
}

// --- up show ---------------------------------------------------------------

// TestUpShow_DefaultNewestAndExplicitID proves the default selects the newest
// run and an explicit id selects exactly that run.
func TestUpShow_DefaultNewestAndExplicitID(t *testing.T) {
	snapsDir := t.TempDir()
	seedRun(t, snapsDir, "2026-07-18T12:00:00Z", diffFixture(1, 0, 0), runDocFixture(mgr("brew", "ok")))
	seedRun(t, snapsDir, "2026-07-19T12:00:00Z", diffFixture(0, 1, 0), runDocFixture(mgr("npm", "ok")))

	// Default: newest (2026-07-19...).
	var out bytes.Buffer
	if err := doUpShow(&out, snapsDir, "", false); err != nil {
		t.Fatalf("show default: %v", err)
	}
	if !strings.Contains(out.String(), "run: 2026-07-19T12:00:00Z") {
		t.Fatalf("default show did not pick the newest run:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "upgrade results:") || !strings.Contains(out.String(), "npm") {
		t.Fatalf("default show missing manager table:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "snapshot: "+filepath.Join(snapsDir, "2026-07-19T12:00:00Z")) {
		t.Fatalf("default show missing snapshot path:\n%s", out.String())
	}

	// Explicit id: the older run.
	out.Reset()
	if err := doUpShow(&out, snapsDir, "2026-07-18T12:00:00Z", false); err != nil {
		t.Fatalf("show explicit: %v", err)
	}
	if !strings.Contains(out.String(), "run: 2026-07-18T12:00:00Z") || !strings.Contains(out.String(), "brew") {
		t.Fatalf("explicit show picked the wrong run:\n%s", out.String())
	}
}

// TestUpShow_EmptyHistoryAndUnknownIDError proves both non-zero cases.
func TestUpShow_EmptyHistoryAndUnknownIDError(t *testing.T) {
	// Empty history (missing snapshots dir).
	missing := filepath.Join(t.TempDir(), "data", "snapshots")
	var out bytes.Buffer
	if err := doUpShow(&out, missing, "", false); err == nil {
		t.Fatal("show over an empty history must error")
	}

	// Unknown id.
	snapsDir := t.TempDir()
	seedRun(t, snapsDir, "2026-07-18T12:00:00Z", diffFixture(0, 0, 0), runDocFixture(mgr("brew", "ok")))
	out.Reset()
	err := doUpShow(&out, snapsDir, "2099-01-01T00:00:00Z", false)
	if err == nil {
		t.Fatal("show of an unknown id must error")
	}
	if !strings.Contains(err.Error(), "unknown up run") {
		t.Fatalf("unknown-id error = %v, want an 'unknown up run' message", err)
	}
}

// TestUpShow_TraversalIDsRejectedWithoutEscaping proves a traversal id is matched
// against the ReadDir names BEFORE any join, so it can never reach a valid file
// planted outside snapshots/. Each attempt errors with no panic and no escape.
func TestUpShow_TraversalIDsRejectedWithoutEscaping(t *testing.T) {
	root := t.TempDir()
	snapsDir := filepath.Join(root, "snapshots")
	seedRun(t, snapsDir, "2026-07-18T12:00:00Z", diffFixture(0, 0, 0), runDocFixture(mgr("brew", "ok")))

	// A perfectly valid run planted OUTSIDE snapshots/: if show ever joined the
	// raw id, "../evil" would resolve here and render successfully.
	seedRun(t, root, "evil", diffFixture(9, 9, 9), runDocFixture(mgr("evil", "ok")))

	// "" is the "newest" sentinel (no id given), not a traversal attempt, so it is
	// excluded here; every genuine traversal form must be rejected.
	for _, bad := range []string{"../evil", "evil/../evil", "a/b", filepath.Join(root, "evil"), ".."} {
		var out bytes.Buffer
		err := doUpShow(&out, snapsDir, bad, false)
		if err == nil {
			t.Fatalf("traversal id %q was accepted; must be rejected", bad)
		}
		if strings.Contains(out.String(), "evil") {
			t.Fatalf("traversal id %q escaped snapshots/ and rendered the planted run:\n%s", bad, out.String())
		}
	}
}

// TestUpShow_IncompleteRunErrors proves a run whose diff.json is missing/unparseable
// is a non-zero "incomplete run" error even if run.json is present.
func TestUpShow_IncompleteRunErrors(t *testing.T) {
	snapsDir := t.TempDir()
	// run.json present, diff.json absent.
	seedRun(t, snapsDir, "2026-07-18T12:00:00Z", nil, runDocFixture(mgr("brew", "ok")))

	var out bytes.Buffer
	err := doUpShow(&out, snapsDir, "", false)
	if err == nil {
		t.Fatal("show of a run with no diff.json must error")
	}
	if !strings.Contains(err.Error(), "incomplete run") {
		t.Fatalf("error = %v, want an 'incomplete run' message", err)
	}

	// An unparseable diff.json is equally incomplete.
	badDir := filepath.Join(snapsDir, "2026-07-19T12:00:00Z")
	if err := os.MkdirAll(badDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(badDir, "diff.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := doUpShow(&out, snapsDir, "2026-07-19T12:00:00Z", false); err == nil {
		t.Fatal("show of a run with an unparseable diff.json must error")
	}
}

// TestUpShow_PreU3RunRendersDiffAndHintsWithNotice proves a pre-U3 run (no
// run.json) still renders its diff and rollback hints, with the manager results
// noted as unavailable.
func TestUpShow_PreU3RunRendersDiffAndHintsWithNotice(t *testing.T) {
	snapsDir := t.TempDir()
	// A foreign (npm) change so writeRollbackHints emits a downgrade suggestion.
	seedRun(t, snapsDir, "2026-07-18T12:00:00Z", diffFixture(1, 0, 0), nil)

	var out bytes.Buffer
	if err := doUpShow(&out, snapsDir, "", false); err != nil {
		t.Fatalf("show pre-U3 run: %v", err)
	}
	s := out.String()
	for _, want := range []string{
		"run: 2026-07-18T12:00:00Z",
		"manager results unavailable",
		"inventory changes:",
		"tsc", "5.3.0", "5.4.0", // the diff table
		"rollback options",
		"npm i -g tsc@5.3.0", // recomputed rollback hint
		"snapshot: ",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("pre-U3 show missing %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "upgrade results:") {
		t.Fatalf("pre-U3 show should not render a manager table:\n%s", s)
	}
}

// TestUpShow_JSONEmbedsRunAndDiff proves the --json document: schema 1, the id,
// the stored run (present for a complete run, null for a pre-U3 run), and the diff.
func TestUpShow_JSONEmbedsRunAndDiff(t *testing.T) {
	snapsDir := t.TempDir()
	seedRun(t, snapsDir, "2026-07-18T12:00:00Z", diffFixture(1, 1, 0),
		runDocFixture(mgr("brew", "ok"), mgr("npm", "ok")))
	seedRun(t, snapsDir, "2026-07-17T00:00:00Z", diffFixture(0, 1, 0), nil) // pre-U3

	// Complete run: run embedded, diff carried.
	var out bytes.Buffer
	if err := doUpShow(&out, snapsDir, "2026-07-18T12:00:00Z", true); err != nil {
		t.Fatalf("show --json: %v", err)
	}
	var doc upShowJSONDoc
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("decode show json: %v\n%s", err, out.String())
	}
	if doc.SchemaVersion != 1 || doc.ID != "2026-07-18T12:00:00Z" {
		t.Fatalf("show json header = %+v, want schema 1 + id", doc)
	}
	if doc.Run == nil || len(doc.Run.Managers) != 2 {
		t.Fatalf("show json run = %+v, want the two managers", doc.Run)
	}
	if len(doc.Diff.Changed) != 1 || len(doc.Diff.Added) != 1 {
		t.Fatalf("show json diff = %+v, want 1 changed + 1 added", doc.Diff)
	}

	// Pre-U3 run: run is null, diff still carried.
	out.Reset()
	if err := doUpShow(&out, snapsDir, "2026-07-17T00:00:00Z", true); err != nil {
		t.Fatalf("show --json pre-U3: %v", err)
	}
	doc = upShowJSONDoc{}
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("decode pre-U3 show json: %v\n%s", err, out.String())
	}
	if doc.Run != nil {
		t.Fatalf("pre-U3 show json run = %+v, want null", doc.Run)
	}
	if len(doc.Diff.Added) != 1 {
		t.Fatalf("pre-U3 show json diff added = %d, want 1", len(doc.Diff.Added))
	}
}

// TestUpHistoryShow_E2EThroughCobra drives the real cobra commands end to end: a
// real `up --only brew` run (real read-only scan + real constrained executor)
// inside a HUKOU_DATA_DIR sandbox persists a run (now including run.json), then
// `up history --json` and `up show --json` — dispatched through rootCmd.Execute,
// resolving the data root exactly as production does — read it back.
func TestUpHistoryShow_E2EThroughCobra(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture brew is a POSIX shell script")
	}
	_, dataDir := e2eSandbox(t)

	// Produce one real run via the production wiring.
	var runOut, runErr bytes.Buffer
	if err := runUpExecute(&runOut, &runErr, upOptions{only: []string{"brew"}}); err != nil {
		t.Fatalf("seed run failed: %v\nstderr:\n%s", err, runErr.String())
	}
	runDir := singleSnapshotRun(t, dataDir)
	if _, err := os.Stat(filepath.Join(runDir, "run.json")); err != nil {
		t.Fatalf("real run did not persist run.json: %v", err)
	}

	// Restore cobra/flag state after driving the real commands.
	t.Cleanup(func() {
		upHistoryJSON, upShowJSON = false, false
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(os.Stdout)
		rootCmd.SetErr(os.Stderr)
	})

	// up history --json through the real command.
	var hOut, hErr bytes.Buffer
	rootCmd.SetArgs([]string{"up", "history", "--json"})
	rootCmd.SetOut(&hOut)
	rootCmd.SetErr(&hErr)
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("up history via cobra: %v\nstderr:\n%s", err, hErr.String())
	}
	var hist upHistoryJSONDoc
	if err := json.Unmarshal(hOut.Bytes(), &hist); err != nil {
		t.Fatalf("history stdout is not pure JSON: %v\n%s", err, hOut.String())
	}
	if len(hist.Runs) != 1 {
		t.Fatalf("history listed %d runs, want 1: %+v", len(hist.Runs), hist.Runs)
	}
	if hist.Runs[0].Managers == nil || hist.Runs[0].Managers.OK != 1 {
		t.Fatalf("history run managers = %+v, want {ok:1 ...}", hist.Runs[0].Managers)
	}

	// up show --json (default newest) through the real command.
	upHistoryJSON, upShowJSON = false, false
	var sOut, sErr bytes.Buffer
	rootCmd.SetArgs([]string{"up", "show", "--json"})
	rootCmd.SetOut(&sOut)
	rootCmd.SetErr(&sErr)
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("up show via cobra: %v\nstderr:\n%s", err, sErr.String())
	}
	var show upShowJSONDoc
	if err := json.Unmarshal(sOut.Bytes(), &show); err != nil {
		t.Fatalf("show stdout is not pure JSON: %v\n%s", err, sOut.String())
	}
	if show.SchemaVersion != 1 || show.Run == nil {
		t.Fatalf("show doc = %+v, want schema 1 + embedded run", show)
	}
	if show.ID != filepath.Base(runDir) {
		t.Fatalf("show id = %q, want %q (the newest run)", show.ID, filepath.Base(runDir))
	}
}
