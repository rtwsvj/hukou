// This file is the read-back surface of `hukou up`: the `history` and `show`
// subcommands that list and re-render the snapshot runs persisted by up_exec.go.
// Both are strictly read-only by contract — they never create the data root or
// the snapshots directory, never take the state lock, and launch no subprocess
// (the repo-wide execution fence applies unchanged; this file imports no
// execution primitive). They tolerate pre-U3 snapshot directories that predate
// run.json, listing/showing them with the manager results marked unavailable.
package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rtwsvj/hukou/internal/i18n"
	"github.com/rtwsvj/hukou/internal/orchestrate"
	"github.com/rtwsvj/hukou/internal/output"
	"github.com/spf13/cobra"
)

var (
	upHistoryJSON bool
	upShowJSON    bool
)

// upHistoryCmd lists persisted `up` runs, newest first. It reads only; a missing
// snapshots directory (or data root) is the normal "nothing recorded yet" state,
// not an error.
var upHistoryCmd = &cobra.Command{
	Use:   "history",
	Short: "List persisted up runs, newest first (read-only)",
	Long: `List the up runs persisted under <dataRoot>/snapshots, newest first.

Each row shows the run id (its timestamped directory name), the inventory diff
counts (changed/added/removed), and a manager ok/failed summary. Runs recorded
before run.json existed show "-" for the manager summary; a corrupt run.json is
reported as unreadable, never mislabeled as pre-run.json; a run missing or with
an unparseable diff.json is marked incomplete.

This is read-only: it creates no data root, takes no lock, and launches no
subprocess. A missing snapshots directory prints "no up runs recorded" and
exits 0. With --json, stdout carries only the JSON document.`,
	Args:          cobra.NoArgs,
	SilenceErrors: true, // errors are printed once via fail.
	RunE:          runUpHistory,
}

// upShowCmd re-renders one persisted run (default: the newest). It reads only.
var upShowCmd = &cobra.Command{
	Use:   "show [id]",
	Short: "Re-render a stored up run, default the newest (read-only)",
	Long: `Re-render one persisted up run: its manager results (when run.json exists),
the inventory diff, the recomputed rollback hints, and the snapshot path.

With no id the newest run is shown; otherwise id must exactly match one of the
run directory names under <dataRoot>/snapshots (names containing a path
separator or "..", or an unknown id, are rejected). An empty history or a run
whose diff.json is missing or unparseable is an error (non-zero exit). Runs
recorded before run.json existed still render their diff and rollback hints,
with the manager results noted as unavailable; a corrupt run.json is reported
as unreadable rather than mislabeled as pre-run.json.

This is read-only: it creates no data root, takes no lock, and launches no
subprocess. With --json, stdout carries only the JSON document.`,
	Args:          cobra.MaximumNArgs(1),
	SilenceErrors: true, // errors are printed once via fail.
	RunE:          runUpShow,
}

func init() {
	upHistoryCmd.Flags().BoolVar(&upHistoryJSON, "json", false, "emit the run list as JSON")
	upCmd.AddCommand(upHistoryCmd)

	upShowCmd.Flags().BoolVar(&upShowJSON, "json", false, "emit the stored run as JSON")
	upCmd.AddCommand(upShowCmd)
}

// runUpHistory resolves the snapshots directory from the data root (WITHOUT
// creating either) and lists it.
func runUpHistory(cmd *cobra.Command, _ []string) error {
	return fail(doUpHistory(cmd.OutOrStdout(), upSnapshotsDir(), upHistoryJSON))
}

// runUpShow resolves the snapshots directory and re-renders the requested run
// (or the newest when no id is given). The raw id is matched against the
// directory names read from snapshots/ before any filesystem join.
func runUpShow(cmd *cobra.Command, args []string) error {
	id := ""
	if len(args) == 1 {
		id = args[0]
	}
	return fail(doUpShow(cmd.OutOrStdout(), upSnapshotsDir(), id, upShowJSON))
}

// upSnapshotsDir is the snapshots directory path. It only computes a string; it
// never creates the data root or the snapshots directory (the read-back surface
// must not bring a data root into existence).
func upSnapshotsDir() string { return filepath.Join(dataRoot(), "snapshots") }

// upManagerSummary is the per-run ok/failed rollup shown by history and derived
// from a run.json. It is a pointer field in the JSON entry so a pre-U3 run
// (no run.json) serializes as null rather than a misleading {0,0}.
type upManagerSummary struct {
	OK     int `json:"ok"`
	Failed int `json:"failed"`
}

// upHistoryEntryJSON is one run in the `up history --json` document. Managers is
// null for pre-U3 directories without run.json; Incomplete marks a run whose
// diff.json is missing or unparseable (its counts are then meaningless zeros).
type upHistoryEntryJSON struct {
	ID         string            `json:"id"`
	Incomplete bool              `json:"incomplete,omitempty"`
	Changed    int               `json:"changed"`
	Added      int               `json:"added"`
	Removed    int               `json:"removed"`
	Managers   *upManagerSummary `json:"managers"`
	// RunJSONError marks a run whose run.json exists but is unreadable or
	// unparseable — corruption, distinct from the pre-U3 null-managers state.
	RunJSONError bool `json:"run_json_error,omitempty"`
}

// upHistoryJSONDoc is the `up history --json` document (schema_version 1). Runs
// is always a (possibly empty) array, never null.
type upHistoryJSONDoc struct {
	SchemaVersion int                  `json:"schema_version"`
	Runs          []upHistoryEntryJSON `json:"runs"`
}

// upShowJSONDoc is the `up show --json` document (schema_version 1): the run id,
// the stored run.json (null when absent — a pre-U3 run — or unreadable, which
// run_json_error then distinguishes), and the stored diff.
type upShowJSONDoc struct {
	SchemaVersion int              `json:"schema_version"`
	ID            string           `json:"id"`
	Run           *upRunDoc        `json:"run"`
	RunJSONError  bool             `json:"run_json_error,omitempty"`
	Diff          orchestrate.Diff `json:"diff"`
}

// doUpHistory lists the persisted runs under snapsDir, newest first. It reads
// only: snapsDir is never created, and a missing snapsDir is the empty state.
func doUpHistory(stdout io.Writer, snapsDir string, asJSON bool) error {
	ids, err := listSnapshotRunNames(snapsDir)
	if err != nil {
		return err
	}
	entries := make([]upHistoryEntryJSON, 0, len(ids))
	for _, id := range ids {
		entries = append(entries, readHistoryEntry(snapsDir, id))
	}

	if asJSON {
		return output.WriteJSONValue(stdout, upHistoryJSONDoc{SchemaVersion: 1, Runs: entries})
	}
	if len(entries) == 0 {
		_, err := fmt.Fprintln(stdout, i18n.T("no up runs recorded"))
		return err
	}
	return writeHistoryTable(stdout, entries)
}

// readHistoryEntry summarizes one run directory: the diff counts (or an
// incomplete marker when diff.json is unreadable) and the manager ok/failed
// rollup (nil when run.json is absent, i.e. a pre-U3 run).
func readHistoryEntry(snapsDir, id string) upHistoryEntryJSON {
	entry := upHistoryEntryJSON{ID: id}
	runDir := filepath.Join(snapsDir, id)

	if diff, err := readSnapshotDiff(runDir); err != nil {
		entry.Incomplete = true
	} else {
		entry.Changed = len(diff.Changed)
		entry.Added = len(diff.Added)
		entry.Removed = len(diff.Removed)
	}

	switch run, err := readSnapshotRun(runDir); {
	case err != nil:
		entry.RunJSONError = true
	case run != nil:
		entry.Managers = managerSummary(run.Managers)
	}
	return entry
}

// managerSummary rolls up manager outcomes into ok/failed counts; any status
// other than "ok" (failed/timeout/canceled) counts as failed.
func managerSummary(managers []upRunManagerJSON) *upManagerSummary {
	s := &upManagerSummary{}
	for _, m := range managers {
		if m.Status == string(orchestrate.StatusOK) {
			s.OK++
		} else {
			s.Failed++
		}
	}
	return s
}

// writeHistoryTable renders the human run list, newest first.
func writeHistoryTable(w io.Writer, entries []upHistoryEntryJSON) error {
	t := output.NewTable(w, i18n.T("ID"), i18n.T("CHANGED"), i18n.T("ADDED"), i18n.T("REMOVED"), i18n.T("MANAGERS"))
	for _, e := range entries {
		if e.Incomplete {
			t.Row(e.ID, "-", "-", "-", i18n.T("(incomplete)"))
			continue
		}
		managers := "-"
		switch {
		case e.RunJSONError:
			managers = i18n.T("(run.json unreadable)")
		case e.Managers != nil:
			managers = i18n.T("%d ok / %d failed", e.Managers.OK, e.Managers.Failed)
		}
		t.Row(e.ID, fmt.Sprintf("%d", e.Changed), fmt.Sprintf("%d", e.Added), fmt.Sprintf("%d", e.Removed), managers)
	}
	return t.Flush()
}

// doUpShow re-renders one stored run. id "" selects the newest. The id is matched
// against the directory names read from snapsDir BEFORE any filesystem join, so a
// path separator, "..", an absolute path, or the empty string can never escape
// snapsDir — they simply match no entry and are reported as an unknown id.
func doUpShow(stdout io.Writer, snapsDir, id string, asJSON bool) error {
	ids, err := listSnapshotRunNames(snapsDir)
	if err != nil {
		return err
	}
	// Distinct from history's exit-0 empty-state text, so a message consumer can
	// tell the success form from this error without inspecting the exit code.
	if len(ids) == 0 {
		return i18n.Errorf("no up runs recorded to show")
	}

	target := ids[0] // newest (ids are sorted newest first)
	if id != "" {
		target = ""
		for _, name := range ids {
			if name == id {
				target = name
				break
			}
		}
		if target == "" {
			return i18n.Errorf("unknown up run %q", id)
		}
	}

	// target is one of the names ReadDir returned (a clean base name), so this is
	// the first and only join of a run id — the raw argument never reaches here.
	runDir := filepath.Join(snapsDir, target)
	diff, err := readSnapshotDiff(runDir)
	if err != nil {
		return i18n.Wrapf("incomplete run %q: %w", err, target)
	}
	// run is nil for a pre-U3 run (no run.json, runErr nil — tolerated) and for a
	// corrupt one (runErr non-nil — reported as corruption, never as pre-U3).
	run, runErr := readSnapshotRun(runDir)

	if asJSON {
		return output.WriteJSONValue(stdout, upShowJSONDoc{
			SchemaVersion: 1,
			ID:            target,
			Run:           run,
			RunJSONError:  runErr != nil,
			Diff:          diff,
		})
	}
	return writeShowTable(stdout, target, run, runErr, diff, runDir)
}

// writeShowTable re-renders a stored run for humans, reusing the live-run
// renderers so a replayed run reads identically to when it happened: the manager
// results (or an unavailable notice for a pre-U3 run), the diff, the recomputed
// rollback hints, and the snapshot path.
func writeShowTable(w io.Writer, id string, run *upRunDoc, runErr error, diff orchestrate.Diff, runDir string) error {
	if _, err := fmt.Fprintf(w, "%s\n", i18n.T("run: %s", id)); err != nil {
		return err
	}
	switch {
	case run != nil:
		if err := writeManagerResultsTable(w, run.Managers); err != nil {
			return err
		}
	case runErr != nil:
		fmt.Fprintln(w, i18n.T("manager results unavailable for this run (run.json unreadable)"))
	default:
		fmt.Fprintln(w, i18n.T("manager results unavailable for this run (recorded before run.json)"))
	}

	fmt.Fprintln(w, "\n"+i18n.T("inventory changes:"))
	if diff.Empty() {
		fmt.Fprintln(w, i18n.T("  none"))
	} else if err := writeDiffTable(w, diff); err != nil {
		return err
	}

	if err := writeRollbackHints(w, diff); err != nil {
		return err
	}
	_, err := fmt.Fprintf(w, "\n%s\n", i18n.T("snapshot: %s", runDir))
	return err
}

// listSnapshotRunNames returns the run directory names under snapsDir, newest
// first (lexicographic descending — RFC3339 names sort chronologically and a
// "-N" collision suffix sorts after its base, the same assumption pruning relies
// on; exact for the single-digit suffixes reachable in practice, while eleven-plus
// same-second runs would mis-order "-10" before "-2" — a bound shared with
// pruneSnapshots that a real run's double full scan cannot hit). A missing
// snapsDir yields an empty list and no error: it is never created here.
// Non-directories and in-progress .tmp-snap-* staging dirs are ignored,
// exactly as pruneSnapshots does.
func listSnapshotRunNames(snapsDir string) ([]string, error) {
	entries, err := os.ReadDir(snapsDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".tmp-snap-") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	return names, nil
}

// readSnapshotDiff reads and parses a run's diff.json. A missing or unparseable
// diff.json is an error: without it a run cannot be classified or re-rendered.
func readSnapshotDiff(runDir string) (orchestrate.Diff, error) {
	var diff orchestrate.Diff
	data, err := os.ReadFile(filepath.Join(runDir, "diff.json"))
	if err != nil {
		return diff, err
	}
	if err := json.Unmarshal(data, &diff); err != nil {
		return diff, err
	}
	return diff, nil
}

// readSnapshotRun reads and parses a run's run.json. A MISSING run.json is the
// pre-U3 state and returns (nil, nil): the run stays listable and showable,
// just without its manager results. Any other failure — unreadable or
// unparseable — is returned as an error so callers can report corruption as
// corruption instead of mislabeling the run as pre-U3.
func readSnapshotRun(runDir string) (*upRunDoc, error) {
	data, err := os.ReadFile(filepath.Join(runDir, "run.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var run upRunDoc
	if err := json.Unmarshal(data, &run); err != nil {
		return nil, err
	}
	return &run, nil
}
