package repair

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/rtwsvj/hukou/internal/activation"
	"github.com/rtwsvj/hukou/internal/manifest"
	"github.com/rtwsvj/hukou/internal/store"
	statejournal "github.com/rtwsvj/hukou/internal/transaction"
)

func TestRestoreManifestBackupPlanAndApply(t *testing.T) {
	root, mainPath, backupPath, livePath := manifestBackupFixture(t)
	if err := os.WriteFile(mainPath, []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := snapshotTree(t, root, "state.lock")
	plan, err := BuildPlan(root, ActionRestoreManifestBackup, time.Unix(1_700_000_000, 0))
	if err != nil {
		t.Fatal(err)
	}
	after := snapshotTree(t, root, "state.lock")
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("planning changed state:\nbefore=%v\nafter=%v", before, after)
	}
	if strings.Contains(plan.DataRootIdentity, root) || plan.Action != ActionRestoreManifestBackup {
		t.Fatalf("unexpected plan: %+v", plan)
	}
	want, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(root, plan); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(mainPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("restored manifest differs from backup\ngot=%s\nwant=%s", got, want)
	}
	if sha, _ := store.SHA256File(livePath); sha == "" {
		t.Fatal("live fixture disappeared")
	}
}

func TestRestoreManifestBackupRejectsStalePlanWithoutBusinessWrite(t *testing.T) {
	root, mainPath, backupPath, livePath := manifestBackupFixture(t)
	if err := os.WriteFile(mainPath, []byte("broken-one"), 0o600); err != nil {
		t.Fatal(err)
	}
	plan, err := BuildPlan(root, ActionRestoreManifestBackup, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mainPath, []byte("broken-two"), 0o600); err != nil {
		t.Fatal(err)
	}
	wantBackup, _ := os.ReadFile(backupPath)
	wantLive, _ := os.ReadFile(livePath)
	_, err = Apply(root, plan)
	if !errors.Is(err, ErrStateChanged) {
		t.Fatalf("error = %v, want ErrStateChanged", err)
	}
	if got, _ := os.ReadFile(mainPath); string(got) != "broken-two" {
		t.Fatalf("stale apply changed main manifest: %q", got)
	}
	if got, _ := os.ReadFile(backupPath); string(got) != string(wantBackup) {
		t.Fatal("stale apply changed backup")
	}
	if got, _ := os.ReadFile(livePath); string(got) != string(wantLive) {
		t.Fatal("stale apply changed live file")
	}
}

func TestRestoreManifestBackupDoesNotAutoRecoverBeforeFingerprintCheck(t *testing.T) {
	root, mainPath, _, _ := manifestBackupFixture(t)
	if err := os.WriteFile(mainPath, []byte("broken-before-plan"), 0o600); err != nil {
		t.Fatal(err)
	}
	plan, err := BuildPlan(root, ActionRestoreManifestBackup, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "pending-target")
	if err := os.WriteFile(target, []byte("before"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := statejournal.Begin(root, "late-transaction", "target", []statejournal.Spec{
		{Role: "target", Path: target, After: statejournal.RegularBytes([]byte("after"), 0o600)},
	}); err != nil {
		t.Fatal(err)
	}
	_, err = Apply(root, plan)
	if !errors.Is(err, ErrStateChanged) {
		t.Fatalf("error = %v, want ErrStateChanged", err)
	}
	if got, _ := os.ReadFile(mainPath); string(got) != "broken-before-plan" {
		t.Fatalf("stale restore wrote manifest: %q", got)
	}
	if got, _ := os.ReadFile(target); string(got) != "before" {
		t.Fatalf("stale restore auto-recovered transaction target: %q", got)
	}
	status, inspectErr := statejournal.Inspect(root)
	if inspectErr != nil || len(status.Pending) != 1 {
		t.Fatalf("stale restore changed pending transaction: status=%+v err=%v", status, inspectErr)
	}
}

func TestRestoreManifestBackupRequiresInvalidMainAndMatchingLive(t *testing.T) {
	root, mainPath, _, livePath := manifestBackupFixture(t)
	if _, err := BuildPlan(root, ActionRestoreManifestBackup, time.Now()); !errors.Is(err, ErrNotRepairable) {
		t.Fatalf("valid main error = %v", err)
	}
	if err := os.WriteFile(mainPath, []byte("broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(livePath, []byte("drift"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildPlan(root, ActionRestoreManifestBackup, time.Now()); !errors.Is(err, ErrNotRepairable) {
		t.Fatalf("live drift error = %v", err)
	}
}

func TestRestoreManifestBackupRejectsAnythingNormalManifestLoadRejects(t *testing.T) {
	tests := map[string]func(map[string]any){
		"missing all v2 top-level state": func(document map[string]any) {
			delete(document, "retention")
			delete(document, "entries")
		},
		"missing v2 retention": func(document map[string]any) {
			delete(document, "retention")
		},
		"missing v2 entries": func(document map[string]any) {
			delete(document, "entries")
		},
		"missing v2 update policy": func(document map[string]any) {
			entries := document["entries"].([]any)
			delete(entries[0].(map[string]any), "update_policy")
		},
		"schema v1 carrying v2 state": func(document map[string]any) {
			document["schema_version"] = float64(1)
		},
		"orphan checksum evidence": func(document map[string]any) {
			entries := document["entries"].([]any)
			entry := entries[0].(map[string]any)
			entry["checksum_asset"] = "checksums.txt"
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			root, mainPath, backupPath, livePath := manifestBackupFixture(t)
			raw, err := os.ReadFile(backupPath)
			if err != nil {
				t.Fatal(err)
			}
			var document map[string]any
			if err := json.Unmarshal(raw, &document); err != nil {
				t.Fatal(err)
			}
			mutate(document)
			invalid, err := json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := manifest.Decode(invalid); err == nil {
				t.Fatal("normal manifest decoder accepted invalid fixture")
			}
			if err := os.WriteFile(backupPath, invalid, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(mainPath, []byte("{broken"), 0o600); err != nil {
				t.Fatal(err)
			}
			before := snapshotTree(t, root)
			wantLive, err := os.ReadFile(livePath)
			if err != nil {
				t.Fatal(err)
			}

			if _, err := BuildPlan(root, ActionRestoreManifestBackup, time.Now()); !errors.Is(err, ErrNotRepairable) {
				t.Fatalf("repair accepted an unloadable backup: %v", err)
			}
			if after := snapshotTree(t, root); !reflect.DeepEqual(before, after) {
				t.Fatalf("planning changed business state\nbefore=%+v\nafter=%+v", before, after)
			}
			if got, err := os.ReadFile(livePath); err != nil || string(got) != string(wantLive) {
				t.Fatalf("planning changed live binary: %q err=%v", got, err)
			}
		})
	}
}

func TestRecoverTransactionPlanAndApply(t *testing.T) {
	root := t.TempDir()
	live := filepath.Join(t.TempDir(), "tool")
	if err := os.WriteFile(live, []byte("before"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := statejournal.Begin(root, "test", "tool", []statejournal.Spec{
		{Role: "live", Path: live, After: statejournal.RegularBytes([]byte("after"), 0o755)},
	}); err != nil {
		t.Fatal(err)
	}
	before := snapshotTree(t, root, "state.lock")
	plan, err := BuildPlan(root, ActionRecoverTransaction, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if after := snapshotTree(t, root, "state.lock"); !reflect.DeepEqual(before, after) {
		t.Fatalf("recovery planning changed state")
	}
	if _, err := Apply(root, plan); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(live); string(got) != "before" {
		t.Fatalf("uncommitted recovery did not restore before state: %q", got)
	}
	status, err := statejournal.Inspect(root)
	if err != nil {
		t.Fatal(err)
	}
	if status.NeedsRecovery() {
		t.Fatalf("transaction state remains after repair: %+v", status)
	}
}

func TestRecoverTransactionSupportsCapturedSymlinkState(t *testing.T) {
	root := t.TempDir()
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	live := filepath.Join(dir, "tool")
	if err := os.WriteFile(target, []byte("before"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, live); err != nil {
		t.Fatal(err)
	}
	if _, err := statejournal.Begin(root, "test", "tool", []statejournal.Spec{
		{Role: "live", Path: live, After: statejournal.RegularBytes([]byte("after"), 0o755)},
	}); err != nil {
		t.Fatal(err)
	}
	plan, err := BuildPlan(root, ActionRecoverTransaction, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(root, plan); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(live)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("recovery did not preserve captured symlink state")
	}
	if got, _ := os.ReadFile(live); string(got) != "before" {
		t.Fatalf("symlink target content changed: %q", got)
	}
}

func TestRecoverTransactionStalePlanDoesNotRecover(t *testing.T) {
	root := t.TempDir()
	live := filepath.Join(t.TempDir(), "tool")
	if err := os.WriteFile(live, []byte("before"), 0o755); err != nil {
		t.Fatal(err)
	}
	tx, err := statejournal.Begin(root, "test", "tool", []statejournal.Spec{
		{Role: "live", Path: live, After: statejournal.RegularBytes([]byte("after"), 0o755)},
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildPlan(root, ActionRecoverTransaction, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Apply("live"); err != nil {
		t.Fatal(err)
	}
	_, err = Apply(root, plan)
	if !errors.Is(err, ErrStateChanged) {
		t.Fatalf("error = %v, want ErrStateChanged", err)
	}
	if got, _ := os.ReadFile(live); string(got) != "after" {
		t.Fatalf("stale plan recovered state: %q", got)
	}
	status, inspectErr := statejournal.Inspect(root)
	if inspectErr != nil || len(status.Pending) != 1 {
		t.Fatalf("pending transaction was changed: status=%+v err=%v", status, inspectErr)
	}
}

func TestPlanRoundTripUsesOwnerOnlyFile(t *testing.T) {
	root := t.TempDir()
	live := filepath.Join(t.TempDir(), "tool")
	if err := os.WriteFile(live, []byte("before"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := statejournal.Begin(root, "test", "tool", []statejournal.Spec{
		{Role: "live", Path: live, After: statejournal.RegularBytes([]byte("after"), 0o755)},
	}); err != nil {
		t.Fatal(err)
	}
	plan, err := BuildPlan(root, ActionRecoverTransaction, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "repair.json")
	if err := WritePlan(path, plan); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("plan mode = %o", info.Mode().Perm())
	}
	loaded, err := LoadPlan(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(loaded, plan) {
		t.Fatalf("round trip mismatch:\ngot=%+v\nwant=%+v", loaded, plan)
	}
}

func TestRecoverTransactionRepairQuarantinesUnknownEntry(t *testing.T) {
	root := t.TempDir()
	live := filepath.Join(t.TempDir(), "tool")
	if err := os.WriteFile(live, []byte("before"), 0o755); err != nil {
		t.Fatal(err)
	}
	tx, err := statejournal.Begin(root, "test", "tool", []statejournal.Spec{
		{Role: "live", Path: live, After: statejournal.RegularBytes([]byte("after"), 0o755)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Apply("live"); err != nil {
		t.Fatal(err)
	}
	// A stray unknown file that previously wedged the recover-transaction action.
	txRoot := filepath.Join(root, "transactions")
	if err := os.WriteFile(filepath.Join(txRoot, "stray"), []byte("junk"), 0o600); err != nil {
		t.Fatal(err)
	}

	plan, err := BuildPlan(root, ActionRecoverTransaction, time.Now())
	if err != nil {
		t.Fatalf("recover-transaction plan wedged on unknown entry: %v", err)
	}
	result, err := Apply(root, plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Quarantined) != 1 || result.Quarantined[0].Original != "stray" {
		t.Fatalf("apply result does not surface the quarantine: %+v", result)
	}
	if got, _ := os.ReadFile(live); string(got) != "before" {
		t.Fatalf("recovery did not roll back: %q", got)
	}
	status, err := statejournal.Inspect(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Quarantined) != 1 {
		t.Fatalf("unknown entry not quarantined: %+v", status)
	}
	if status.NeedsRecovery() {
		t.Fatalf("state still needs recovery: %+v", status)
	}
}

func TestRecoverTransactionPlanFailsClosedOnUnknownDirectory(t *testing.T) {
	root := t.TempDir()
	live := filepath.Join(t.TempDir(), "tool")
	if err := os.WriteFile(live, []byte("before"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := statejournal.Begin(root, "test", "tool", []statejournal.Spec{
		{Role: "live", Path: live, After: statejournal.RegularBytes([]byte("after"), 0o755)},
	}); err != nil {
		t.Fatal(err)
	}
	// An unknown directory may be a journal from a newer hukou; the repair
	// action must refuse to plan around it instead of demoting it.
	unknownDir := filepath.Join(root, "transactions", "future-journal")
	if err := os.Mkdir(unknownDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(unknownDir, "evidence"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildPlan(root, ActionRecoverTransaction, time.Now()); !errors.Is(err, ErrNotRepairable) {
		t.Fatalf("error = %v, want ErrNotRepairable", err)
	}
	if data, err := os.ReadFile(filepath.Join(unknownDir, "evidence")); err != nil || string(data) != "keep" {
		t.Fatalf("planning touched the unknown directory: data=%q err=%v", data, err)
	}
}

func TestPurgeQuarantinePlanAndApply(t *testing.T) {
	root := t.TempDir()
	txRoot := filepath.Join(root, "transactions")
	if err := os.MkdirAll(txRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	// A quarantined directory carrying evidence, plus a real completed journal
	// that purge must leave untouched.
	quarantined := filepath.Join(txRoot, "quarantined-mystery-"+strings.Repeat("a", 32))
	if err := os.Mkdir(quarantined, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(quarantined, "evidence"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	completed := filepath.Join(txRoot, "completed-"+strings.Repeat("b", 32))
	if err := os.Mkdir(completed, 0o700); err != nil {
		t.Fatal(err)
	}

	before := snapshotTree(t, root, "state.lock")
	plan, err := BuildPlan(root, ActionPurgeQuarantine, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if after := snapshotTree(t, root, "state.lock"); !reflect.DeepEqual(before, after) {
		t.Fatal("purge-quarantine planning changed state")
	}
	result, err := Apply(root, plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.PurgedQuarantine) != 1 || result.PurgedQuarantine[0] != filepath.Base(quarantined) {
		t.Fatalf("apply result does not surface the purge: %+v", result)
	}
	if _, err := os.Lstat(quarantined); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("quarantined entry not purged: %v", err)
	}
	if _, err := os.Lstat(completed); err != nil {
		t.Fatalf("purge removed a real journal: %v", err)
	}
}

func TestPurgeQuarantineRejectsStalePlan(t *testing.T) {
	root := t.TempDir()
	txRoot := filepath.Join(root, "transactions")
	if err := os.MkdirAll(txRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	quarantined := filepath.Join(txRoot, "quarantined-a-"+strings.Repeat("a", 32))
	if err := os.Mkdir(quarantined, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(quarantined, "x"), []byte("1"), 0o600); err != nil {
		t.Fatal(err)
	}
	plan, err := BuildPlan(root, ActionPurgeQuarantine, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	// A new quarantined entry appears after planning; apply must fail closed.
	other := filepath.Join(txRoot, "quarantined-b-"+strings.Repeat("b", 32))
	if err := os.Mkdir(other, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(root, plan); !errors.Is(err, ErrStateChanged) {
		t.Fatalf("error = %v, want ErrStateChanged", err)
	}
	if _, err := os.Lstat(quarantined); err != nil {
		t.Fatalf("stale purge deleted evidence: %v", err)
	}
}

func TestCleanLiveTempsPlanAndApply(t *testing.T) {
	root, _, _, livePath := manifestBackupFixture(t)
	liveDir := filepath.Dir(livePath)
	// base is roughly "wall now"; the plan's reference clock is set two hours
	// ahead so freshly created entries fall before the one-hour cutoff without
	// backdating a symlink (os.Chtimes follows symlinks and cannot rewrite a
	// symlink's own mtime).
	base := time.Now()
	now := base.Add(2 * time.Hour)

	oldRegular := filepath.Join(liveDir, ".hukou-txn-oldregular")
	if err := os.WriteFile(oldRegular, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(oldRegular, base, base); err != nil {
		t.Fatal(err)
	}
	// A stale symlink temporary keeps its natural creation mtime (~base), which
	// is well before the cutoff (base+1h), so it is selected for removal.
	oldLink := filepath.Join(liveDir, ".hukou-txn-link-oldlink")
	if err := os.Symlink("somewhere", oldLink); err != nil {
		t.Fatal(err)
	}
	// A fresh temporary from an in-flight operation must be preserved: its mtime
	// is set past the cutoff.
	freshTemp := filepath.Join(liveDir, ".hukou-txn-fresh")
	if err := os.WriteFile(freshTemp, []byte("busy"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(freshTemp, base.Add(90*time.Minute), base.Add(90*time.Minute)); err != nil {
		t.Fatal(err)
	}

	before := snapshotTree(t, root, "state.lock")
	plan, err := BuildPlan(root, ActionCleanLiveTemps, now)
	if err != nil {
		t.Fatal(err)
	}
	if after := snapshotTree(t, root, "state.lock"); !reflect.DeepEqual(before, after) {
		t.Fatal("clean-live-temps planning changed the data root")
	}
	if len(plan.Targets) != 2 {
		t.Fatalf("plan targets = %+v, want the two aged orphans", plan.Targets)
	}
	for _, target := range plan.Targets {
		if target.Kind == "regular" && (!target.HasFileID || len(target.SHA256Prefix) != 16) {
			t.Fatalf("regular target lacks identity binding: %+v", target)
		}
	}
	result, err := Apply(root, plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.RemovedLiveTemps) != 2 || len(result.SkippedLiveTemps) != 0 {
		t.Fatalf("unexpected apply result: %+v", result)
	}
	for _, gone := range []string{oldRegular, oldLink} {
		if _, err := os.Lstat(gone); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("orphan temp %s not removed: %v", gone, err)
		}
	}
	if _, err := os.Lstat(freshTemp); err != nil {
		t.Fatalf("fresh temp wrongly removed: %v", err)
	}
	if _, err := os.Lstat(livePath); err != nil {
		t.Fatalf("clean-live-temps disturbed the live binary: %v", err)
	}
}

func TestCleanLiveTempsSkipsMutatedTargetWithoutDeleting(t *testing.T) {
	root, _, _, livePath := manifestBackupFixture(t)
	liveDir := filepath.Dir(livePath)
	now := time.Now()
	mutated := filepath.Join(liveDir, ".hukou-txn-mutated")
	stable := filepath.Join(liveDir, ".hukou-txn-stable")
	for _, path := range []string{mutated, stable} {
		if err := os.WriteFile(path, []byte("orphan-payload"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, now.Add(-2*time.Hour), now.Add(-2*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	plan, err := BuildPlan(root, ActionCleanLiveTemps, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Targets) != 2 {
		t.Fatalf("plan targets = %+v", plan.Targets)
	}
	// TOCTOU between plan and apply: the candidate is replaced with different
	// bytes. Apply must re-verify the recorded identity and skip it rather than
	// delete a resource the plan never described.
	if err := os.WriteFile(mutated, []byte("replaced-after-planning"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := Apply(root, plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.SkippedLiveTemps) != 1 || result.SkippedLiveTemps[0] != mutated {
		t.Fatalf("mutated target not skipped: %+v", result)
	}
	if len(result.RemovedLiveTemps) != 1 || result.RemovedLiveTemps[0] != stable {
		t.Fatalf("stable target not removed: %+v", result)
	}
	if data, err := os.ReadFile(mutated); err != nil || string(data) != "replaced-after-planning" {
		t.Fatalf("mutated candidate was deleted or changed: data=%q err=%v", data, err)
	}
	if _, err := os.Lstat(stable); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stable orphan not removed: %v", err)
	}
}

func TestCleanLiveTempsLeavesUnplannedOrphanAlone(t *testing.T) {
	root, _, _, livePath := manifestBackupFixture(t)
	liveDir := filepath.Dir(livePath)
	now := time.Now()
	planned := filepath.Join(liveDir, ".hukou-txn-planned")
	if err := os.WriteFile(planned, []byte("planned"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(planned, now.Add(-2*time.Hour), now.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	plan, err := BuildPlan(root, ActionCleanLiveTemps, now)
	if err != nil {
		t.Fatal(err)
	}
	// A new aged orphan appears after planning. The deletion set was fixed at
	// plan time, so the new orphan must survive and the planned one is removed.
	unplanned := filepath.Join(liveDir, ".hukou-txn-unplanned")
	if err := os.WriteFile(unplanned, []byte("unplanned"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(unplanned, now.Add(-2*time.Hour), now.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	result, err := Apply(root, plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.RemovedLiveTemps) != 1 || result.RemovedLiveTemps[0] != planned {
		t.Fatalf("unexpected apply result: %+v", result)
	}
	if _, err := os.Lstat(planned); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("planned orphan not removed: %v", err)
	}
	if _, err := os.Lstat(unplanned); err != nil {
		t.Fatalf("unplanned orphan was touched: %v", err)
	}
}

func TestCleanLiveTempsNeverRemovesRegisteredLivePath(t *testing.T) {
	// The registered live file itself carries the temporary prefix: a worst-case
	// manifest that must still be immune to clean-live-temps.
	liveDir := t.TempDir()
	livePath := filepath.Join(liveDir, ".hukou-txn-livetool")
	root, _, _ := manifestFixtureWithLive(t, livePath)
	now := time.Now()
	if err := os.Chtimes(livePath, now.Add(-3*time.Hour), now.Add(-3*time.Hour)); err != nil {
		t.Fatal(err)
	}
	// With only the registered live file present there is nothing to clean.
	if _, err := BuildPlan(root, ActionCleanLiveTemps, now); !errors.Is(err, ErrNotRepairable) {
		t.Fatalf("live path was offered for deletion: %v", err)
	}
	// A genuine aged orphan next to it is cleaned; the live file survives.
	orphan := filepath.Join(liveDir, ".hukou-txn-orphan")
	if err := os.WriteFile(orphan, []byte("orphan"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(orphan, now.Add(-2*time.Hour), now.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	plan, err := BuildPlan(root, ActionCleanLiveTemps, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Targets) != 1 || plan.Targets[0].Path != orphan {
		t.Fatalf("plan selected the wrong candidates: %+v", plan.Targets)
	}
	result, err := Apply(root, plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.RemovedLiveTemps) != 1 || result.RemovedLiveTemps[0] != orphan {
		t.Fatalf("unexpected apply result: %+v", result)
	}
	if data, err := os.ReadFile(livePath); err != nil || string(data) != "fixture-v1\n" {
		t.Fatalf("registered live file was touched: data=%q err=%v", data, err)
	}
}

func TestCleanLiveTempsRequiresNoActiveJournals(t *testing.T) {
	root, _, _, livePath := manifestBackupFixture(t)
	liveDir := filepath.Dir(livePath)
	now := time.Now()
	orphan := filepath.Join(liveDir, ".hukou-txn-orphan")
	if err := os.WriteFile(orphan, []byte("orphan"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(orphan, now.Add(-2*time.Hour), now.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	plan, err := BuildPlan(root, ActionCleanLiveTemps, now)
	if err != nil {
		t.Fatal(err)
	}
	// A pending journal appears: even an old temporary may belong to a slow
	// in-flight copy, so both planning and apply refuse to run.
	target := filepath.Join(t.TempDir(), "pending-target")
	if err := os.WriteFile(target, []byte("before"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := statejournal.Begin(root, "gate-test", "target", []statejournal.Spec{
		{Role: "target", Path: target, After: statejournal.RegularBytes([]byte("after"), 0o600)},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildPlan(root, ActionCleanLiveTemps, now); !errors.Is(err, ErrNotRepairable) {
		t.Fatalf("plan error = %v, want ErrNotRepairable", err)
	}
	if _, err := Apply(root, plan); !errors.Is(err, ErrStateChanged) {
		t.Fatalf("apply error = %v, want ErrStateChanged", err)
	}
	if _, err := os.Lstat(orphan); err != nil {
		t.Fatalf("gated apply still removed the orphan: %v", err)
	}
}

func TestCleanLiveTempsPlanRoundTripAndTamperRejection(t *testing.T) {
	root, _, _, livePath := manifestBackupFixture(t)
	liveDir := filepath.Dir(livePath)
	now := time.Now()
	orphan := filepath.Join(liveDir, ".hukou-txn-orphan")
	if err := os.WriteFile(orphan, []byte("orphan"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(orphan, now.Add(-2*time.Hour), now.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	plan, err := BuildPlan(root, ActionCleanLiveTemps, now)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "clean-live-temps.json")
	if err := WritePlan(path, plan); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadPlan(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(loaded, plan) {
		t.Fatalf("round trip mismatch:\ngot=%+v\nwant=%+v", loaded, plan)
	}
	// A tampered target list no longer matches the plan fingerprint.
	tampered := loaded
	tampered.Targets = append([]LiveTempTarget(nil), loaded.Targets...)
	tampered.Targets[0].SHA256Prefix = "0123456789abcdef"
	if _, err := Apply(root, tampered); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("tampered plan error = %v, want ErrInvalidPlan", err)
	}
	if _, err := os.Lstat(orphan); err != nil {
		t.Fatalf("tampered plan still removed the orphan: %v", err)
	}
}

func TestNewActionsRejectCrossDataRoot(t *testing.T) {
	rootA := t.TempDir()
	txRootA := filepath.Join(rootA, "transactions")
	if err := os.MkdirAll(txRootA, 0o700); err != nil {
		t.Fatal(err)
	}
	quarantined := filepath.Join(txRootA, "quarantined-"+strings.Repeat("a", 16))
	if err := os.Mkdir(quarantined, 0o700); err != nil {
		t.Fatal(err)
	}
	purgePlan, err := BuildPlan(rootA, ActionPurgeQuarantine, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	// The same quarantine layout under a different data root must not satisfy a
	// plan generated elsewhere.
	rootB := t.TempDir()
	txRootB := filepath.Join(rootB, "transactions")
	if err := os.MkdirAll(filepath.Join(txRootB, filepath.Base(quarantined)), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(rootB, purgePlan); !errors.Is(err, ErrStateChanged) {
		t.Fatalf("cross-root purge error = %v, want ErrStateChanged", err)
	}
	if _, err := os.Lstat(filepath.Join(txRootB, filepath.Base(quarantined))); err != nil {
		t.Fatalf("cross-root purge deleted data: %v", err)
	}

	rootC, _, _, livePathC := manifestBackupFixture(t)
	now := time.Now()
	orphan := filepath.Join(filepath.Dir(livePathC), ".hukou-txn-orphan")
	if err := os.WriteFile(orphan, []byte("orphan"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(orphan, now.Add(-2*time.Hour), now.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	cleanPlan, err := BuildPlan(rootC, ActionCleanLiveTemps, now)
	if err != nil {
		t.Fatal(err)
	}
	rootD, _, _, _ := manifestBackupFixture(t)
	if _, err := Apply(rootD, cleanPlan); !errors.Is(err, ErrStateChanged) {
		t.Fatalf("cross-root clean error = %v, want ErrStateChanged", err)
	}
	if _, err := os.Lstat(orphan); err != nil {
		t.Fatalf("cross-root clean removed the orphan: %v", err)
	}
}

func TestNewRepairActionsRequirePresentState(t *testing.T) {
	root := t.TempDir()
	if _, err := BuildPlan(root, ActionPurgeQuarantine, time.Now()); !errors.Is(err, ErrNotRepairable) {
		t.Fatalf("purge with no quarantine: %v", err)
	}
	if _, err := BuildPlan(root, ActionCleanLiveTemps, time.Now()); !errors.Is(err, ErrNotRepairable) {
		t.Fatalf("clean-live-temps with no orphans: %v", err)
	}
}

func manifestBackupFixture(t *testing.T) (root, mainPath, backupPath, livePath string) {
	t.Helper()
	livePath = filepath.Join(t.TempDir(), "fixture-tool")
	root, mainPath, backupPath = manifestFixtureWithLive(t, livePath)
	return root, mainPath, backupPath, livePath
}

func manifestFixtureWithLive(t *testing.T, livePath string) (root, mainPath, backupPath string) {
	t.Helper()
	root = t.TempDir()
	if err := os.WriteFile(livePath, []byte("fixture-v1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	sha, err := store.SHA256File(livePath)
	if err != nil {
		t.Fatal(err)
	}
	m := &manifest.Manifest{
		SchemaVersion: manifest.CurrentSchemaVersion,
		Retention:     manifest.DefaultRetentionPolicy(),
		Entries:       make([]manifest.Entry, 0),
	}
	entry := manifest.Entry{
		Name:         filepath.Base(livePath),
		Path:         livePath,
		Repo:         "example/project",
		Tag:          "v1.0.0",
		SHA256:       sha,
		Upstream:     "https://example.invalid",
		AdoptedAt:    "2026-01-01T00:00:00Z",
		UpdatedAt:    "2026-01-01T00:00:00Z",
		UpdatePolicy: manifest.DefaultUpdatePolicy(),
	}
	if err := activation.RecordAdopt(&entry, "act-fixture", entry.UpdatedAt); err != nil {
		t.Fatal(err)
	}
	m.Put(entry)
	mainPath = filepath.Join(root, "manifest.json")
	if err := m.Save(mainPath); err != nil {
		t.Fatal(err)
	}
	if err := m.Save(mainPath); err != nil {
		t.Fatal(err)
	}
	backupPath = mainPath + ".bak"
	return root, mainPath, backupPath
}

type snapshotEntry struct {
	Name string
	Mode os.FileMode
	SHA  string
	Link string
}

func snapshotTree(t *testing.T, root string, ignoredBase ...string) []snapshotEntry {
	t.Helper()
	ignored := make(map[string]struct{}, len(ignoredBase))
	for _, name := range ignoredBase {
		ignored[name] = struct{}{}
	}
	var result []snapshotEntry
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if _, skip := ignored[filepath.Base(path)]; skip {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		entry := snapshotEntry{Name: rel, Mode: info.Mode()}
		if info.Mode()&os.ModeSymlink != 0 {
			entry.Link, err = os.Readlink(path)
		} else if info.Mode().IsRegular() {
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			sum := sha256.Sum256(data)
			entry.SHA = hex.EncodeToString(sum[:])
		}
		result = append(result, entry)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}
