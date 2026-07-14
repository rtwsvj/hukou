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
	if err := Apply(root, plan); err != nil {
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
	err = Apply(root, plan)
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
	err = Apply(root, plan)
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
	if err := Apply(root, plan); err != nil {
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
	if err := Apply(root, plan); err != nil {
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
	err = Apply(root, plan)
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

func manifestBackupFixture(t *testing.T) (root, mainPath, backupPath, livePath string) {
	t.Helper()
	root = t.TempDir()
	livePath = filepath.Join(t.TempDir(), "fixture-tool")
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
	return root, mainPath, backupPath, livePath
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
