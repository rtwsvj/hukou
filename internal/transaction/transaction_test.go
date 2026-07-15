package transaction

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rtwsvj/hukou/internal/durablefs"
)

type matrixFixture struct {
	root     string
	live     string
	manifest string
	tx       *Transaction
}

func newMatrixFixture(t *testing.T) matrixFixture {
	t.Helper()
	root := t.TempDir()
	live := filepath.Join(t.TempDir(), "tool")
	manifest := filepath.Join(root, "manifest.json")
	mustWrite(t, live, "live-before", 0o755)
	mustWrite(t, manifest, "manifest-before", 0o600)
	tx, err := Begin(root, "upgrade", "tool", []Spec{
		{Role: "live", Path: live, After: RegularBytes([]byte("live-after"), 0o755)},
		{Role: "manifest", Path: manifest, After: RegularBytes([]byte("manifest-after"), 0o600)},
	})
	if err != nil {
		t.Fatal(err)
	}
	return matrixFixture{root: root, live: live, manifest: manifest, tx: tx}
}

func TestRecoverPreparedCrashStateMatrix(t *testing.T) {
	for _, tc := range []struct {
		name          string
		liveAfter     bool
		manifestAfter bool
	}{
		{name: "before-before"},
		{name: "after-before", liveAfter: true},
		{name: "before-after", manifestAfter: true},
		{name: "after-after", liveAfter: true, manifestAfter: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newMatrixFixture(t)
			if tc.liveAfter {
				mustApply(t, fixture.tx, "live")
			}
			if tc.manifestAfter {
				mustApply(t, fixture.tx, "manifest")
			}

			if _, err := Recover(fixture.root); err != nil {
				t.Fatal(err)
			}
			assertFile(t, fixture.live, "live-before", 0o755)
			assertFile(t, fixture.manifest, "manifest-before", 0o600)
			assertJournalClean(t, fixture.root)
		})
	}
}

func TestRecoverCommittedCrashStateMatrix(t *testing.T) {
	for _, tc := range []struct {
		name          string
		liveAfter     bool
		manifestAfter bool
	}{
		{name: "before-before"},
		{name: "after-before", liveAfter: true},
		{name: "before-after", manifestAfter: true},
		{name: "after-after", liveAfter: true, manifestAfter: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newMatrixFixture(t)
			mustApply(t, fixture.tx, "live")
			mustApply(t, fixture.tx, "manifest")
			if err := fixture.tx.Commit(); err != nil {
				t.Fatal(err)
			}
			if !tc.liveAfter {
				mustConvergeForTest(t, fixture.tx, "live", false)
			}
			if !tc.manifestAfter {
				mustConvergeForTest(t, fixture.tx, "manifest", false)
			}

			if _, err := Recover(fixture.root); err != nil {
				t.Fatal(err)
			}
			assertFile(t, fixture.live, "live-after", 0o755)
			assertFile(t, fixture.manifest, "manifest-after", 0o600)
			assertJournalClean(t, fixture.root)
		})
	}
}

func TestRecoverUnknownDriftFailsClosedBeforeAnyWrite(t *testing.T) {
	fixture := newMatrixFixture(t)
	mustApply(t, fixture.tx, "manifest")
	mustWrite(t, fixture.live, "external-change", 0o755)

	_, err := Recover(fixture.root)
	if err == nil || !strings.Contains(err.Error(), "unknown drift") {
		t.Fatalf("expected unknown drift error, got %v", err)
	}
	assertFile(t, fixture.live, "external-change", 0o755)
	// Preclassification means manifest is not rolled back before the unknown
	// live resource has been detected.
	assertFile(t, fixture.manifest, "manifest-after", 0o600)
	status, inspectErr := Inspect(fixture.root)
	if inspectErr != nil || len(status.Pending) != 1 {
		t.Fatalf("pending evidence not retained: status=%+v err=%v", status, inspectErr)
	}
}

func TestAbsentToRegularUsesNoReplaceAtCommitPoint(t *testing.T) {
	root := t.TempDir()
	destination := filepath.Join(t.TempDir(), "original")
	tx, err := Begin(root, "adopt", "tool", []Spec{
		{Role: "original", Path: destination, After: RegularBytes([]byte("hukou-copy"), 0o755)},
	})
	if err != nil {
		t.Fatal(err)
	}
	oldHook := testBeforeApplyHook
	testBeforeApplyHook = func(path string) {
		if path != destination {
			t.Fatalf("hook path=%s want %s", path, destination)
		}
		mustWrite(t, destination, "competing-writer", 0o755)
	}
	t.Cleanup(func() { testBeforeApplyHook = oldHook })

	err = tx.Apply("original")
	if err == nil || !errors.Is(err, os.ErrExist) {
		t.Fatalf("expected atomic no-replace EEXIST, got %v", err)
	}
	assertFile(t, destination, "competing-writer", 0o755)
	status, inspectErr := Inspect(root)
	if inspectErr != nil || len(status.Pending) != 1 {
		t.Fatalf("journal must remain for drift diagnosis: status=%+v err=%v", status, inspectErr)
	}
}

func TestExistingMutationRechecksAfterApplyHook(t *testing.T) {
	for _, tc := range []struct {
		name  string
		after Desired
	}{
		{name: "replace-regular", after: RegularBytes([]byte("hukou-after"), 0o755)},
		{name: "delete", after: Absent()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			destination := filepath.Join(t.TempDir(), "tool")
			mustWrite(t, destination, "hukou-before", 0o755)
			tx, err := Begin(root, "upgrade", "tool", []Spec{{
				Role: "live", Path: destination, After: tc.after,
			}})
			if err != nil {
				t.Fatal(err)
			}

			oldHook := testBeforeApplyHook
			testBeforeApplyHook = func(path string) {
				if path != destination {
					t.Fatalf("hook path=%s want %s", path, destination)
				}
				mustWrite(t, destination, "competing-writer", 0o755)
			}
			t.Cleanup(func() { testBeforeApplyHook = oldHook })

			err = tx.Apply("live")
			if err == nil || !strings.Contains(err.Error(), "unknown drift") {
				t.Fatalf("expected post-hook drift refusal, got %v", err)
			}
			assertFile(t, destination, "competing-writer", 0o755)
			status, inspectErr := Inspect(root)
			if inspectErr != nil || len(status.Pending) != 1 {
				t.Fatalf("journal must remain for drift diagnosis: status=%+v err=%v", status, inspectErr)
			}
		})
	}
}

func TestMatchedMutableStatesReassertDurability(t *testing.T) {
	root := t.TempDir()
	participantDir := t.TempDir()
	regular := filepath.Join(participantDir, "regular")
	absent := filepath.Join(participantDir, "absent")
	guard := filepath.Join(participantDir, "guard")
	mustWrite(t, regular, "before", 0o755)
	mustWrite(t, guard, "guard", 0o755)
	_, err := Begin(root, "upgrade", "tool", []Spec{
		{Role: "regular", Path: regular, After: RegularBytes([]byte("after"), 0o755)},
		{Role: "absent", Path: absent, After: RegularBytes([]byte("created"), 0o755)},
		{Role: "guard", Path: guard, After: Unchanged()},
	})
	if err != nil {
		t.Fatal(err)
	}

	fileSyncs := map[string]int{}
	parentSyncs := map[string]int{}
	oldFileSync := syncMatchedFile
	oldParentSync := syncMatchedParent
	syncMatchedFile = func(file *os.File) error {
		fileSyncs[file.Name()]++
		return durablefs.SyncFile(file)
	}
	syncMatchedParent = func(path string) error {
		parentSyncs[path]++
		return durablefs.SyncParent(path)
	}
	t.Cleanup(func() {
		syncMatchedFile = oldFileSync
		syncMatchedParent = oldParentSync
	})

	if _, err := Recover(root); err != nil {
		t.Fatal(err)
	}
	if fileSyncs[regular] != 1 || parentSyncs[regular] != 1 {
		t.Fatalf("regular fast path syncs: file=%d parent=%d", fileSyncs[regular], parentSyncs[regular])
	}
	if fileSyncs[absent] != 0 || parentSyncs[absent] != 1 {
		t.Fatalf("absent fast path syncs: file=%d parent=%d", fileSyncs[absent], parentSyncs[absent])
	}
	if fileSyncs[guard] != 0 || parentSyncs[guard] != 0 {
		t.Fatalf("read-only guard must not be synced: file=%d parent=%d", fileSyncs[guard], parentSyncs[guard])
	}
}

func TestMatchedAbsentSyncFailureRetainsPendingEvidence(t *testing.T) {
	root := t.TempDir()
	destination := filepath.Join(t.TempDir(), "original")
	_, err := Begin(root, "adopt", "tool", []Spec{{
		Role: "original", Path: destination, After: RegularBytes([]byte("copy"), 0o755),
	}})
	if err != nil {
		t.Fatal(err)
	}

	injected := errors.New("injected parent sync failure")
	oldParentSync := syncMatchedParent
	syncMatchedParent = func(path string) error {
		if path == destination {
			return injected
		}
		return durablefs.SyncParent(path)
	}
	t.Cleanup(func() { syncMatchedParent = oldParentSync })

	_, err = Recover(root)
	if !errors.Is(err, injected) {
		t.Fatalf("expected durability error, got %v", err)
	}
	status, inspectErr := Inspect(root)
	if inspectErr != nil || len(status.Pending) != 1 {
		t.Fatalf("pending evidence not retained: status=%+v err=%v", status, inspectErr)
	}
	if _, statErr := os.Lstat(destination); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("absent resource changed after sync failure: %v", statErr)
	}
}

func TestPendingErrorGuidanceUsesSafeRecoveryAndStandardDoctor(t *testing.T) {
	message := (&PendingError{Status: Status{Pending: []string{"pending-test"}}}).Error()
	if !strings.Contains(message, "attempt safe recovery") || !strings.Contains(message, "run hukou doctor to inspect it") {
		t.Fatalf("pending guidance is incomplete: %s", message)
	}
	if strings.Contains(message, "--deep") {
		t.Fatalf("standard doctor should be sufficient guidance: %s", message)
	}
}

func TestPreparedRecoveryRestoresLegacySymlinkTopology(t *testing.T) {
	root := t.TempDir()
	dir := t.TempDir()
	oldTarget := filepath.Join(dir, "old-target")
	live := filepath.Join(dir, "tool")
	mustWrite(t, oldTarget, "old", 0o755)
	if err := os.Symlink("old-target", live); err != nil {
		t.Fatal(err)
	}
	tx, err := Begin(root, "rollback", "tool", []Spec{
		{Role: "live", Path: live, After: RegularBytes([]byte("new"), 0o755)},
	})
	if err != nil {
		t.Fatal(err)
	}
	mustApply(t, tx, "live")
	if _, err := Recover(root); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(live)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("legacy symlink topology not restored: info=%v err=%v", info, err)
	}
	target, err := os.Readlink(live)
	if err != nil || target != "old-target" {
		t.Fatalf("symlink target=%q err=%v", target, err)
	}
}

func TestInvalidCommitMarkerFailsClosed(t *testing.T) {
	fixture := newMatrixFixture(t)
	mustApply(t, fixture.tx, "live")
	if err := os.WriteFile(filepath.Join(fixture.tx.dir, commitFileName), []byte("torn"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Recover(fixture.root)
	if err == nil || !strings.Contains(err.Error(), "invalid COMMIT") {
		t.Fatalf("expected invalid COMMIT error, got %v", err)
	}
	assertFile(t, fixture.live, "live-after", 0o755)
	assertFile(t, fixture.manifest, "manifest-before", 0o600)
}

func TestCheckCleanIsReadOnlyWhenDataRootIsMissing(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing-data-root")
	if err := CheckClean(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read-only check created data root or returned unexpected error: %v", err)
	}
}

func TestBeginRefusesSecondPendingTransaction(t *testing.T) {
	fixture := newMatrixFixture(t)
	_, err := Begin(fixture.root, "rollback", "other", []Spec{{
		Role: "live", Path: filepath.Join(t.TempDir(), "other"), After: RegularBytes([]byte("x"), 0o755),
	}})
	var pendingErr *PendingError
	if !errors.As(err, &pendingErr) {
		t.Fatalf("expected PendingError, got %v", err)
	}
}

func TestSubprocessSIGKILLRecovery(t *testing.T) {
	for _, stage := range []string{"prepared", "committed"} {
		t.Run(stage, func(t *testing.T) {
			root := t.TempDir()
			live := filepath.Join(t.TempDir(), "tool")
			manifest := filepath.Join(root, "manifest.json")
			mustWrite(t, live, "live-before", 0o755)
			mustWrite(t, manifest, "manifest-before", 0o600)
			cmd := exec.Command(os.Args[0], "-test.run=^TestTransactionCrashHelper$")
			cmd.Env = append(os.Environ(),
				"HUKOU_TXN_CRASH_HELPER=1",
				"HUKOU_TXN_CRASH_STAGE="+stage,
				"HUKOU_TXN_CRASH_ROOT="+root,
				"HUKOU_TXN_CRASH_LIVE="+live,
				"HUKOU_TXN_CRASH_MANIFEST="+manifest,
			)
			err := cmd.Run()
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) {
				t.Fatalf("helper was not killed: %v", err)
			}
			if _, err := Recover(root); err != nil {
				t.Fatal(err)
			}
			if stage == "prepared" {
				assertFile(t, live, "live-before", 0o755)
				assertFile(t, manifest, "manifest-before", 0o600)
			} else {
				assertFile(t, live, "live-after", 0o755)
				assertFile(t, manifest, "manifest-after", 0o600)
			}
			assertJournalClean(t, root)
		})
	}
}

// TestTransactionCrashHelper is launched as a subprocess above. It proves the
// journal survives abrupt process death; the committed branch also models a
// persisted before/after visibility combination. It does not claim to emulate
// storage-controller cache reordering from a real power cut.
func TestTransactionCrashHelper(t *testing.T) {
	if os.Getenv("HUKOU_TXN_CRASH_HELPER") != "1" {
		return
	}
	root := os.Getenv("HUKOU_TXN_CRASH_ROOT")
	live := os.Getenv("HUKOU_TXN_CRASH_LIVE")
	manifest := os.Getenv("HUKOU_TXN_CRASH_MANIFEST")
	stage := os.Getenv("HUKOU_TXN_CRASH_STAGE")
	tx, err := Begin(root, "upgrade", "tool", []Spec{
		{Role: "live", Path: live, After: RegularBytes([]byte("live-after"), 0o755)},
		{Role: "manifest", Path: manifest, After: RegularBytes([]byte("manifest-after"), 0o600)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Apply("live"); err != nil {
		t.Fatal(err)
	}
	if stage == "committed" {
		if err := tx.Apply("manifest"); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		// Model the live directory entry presenting its pre-transaction state
		// even though the durable COMMIT marker survived.
		if err := os.WriteFile(live, []byte("live-before"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	process, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if err := process.Kill(); err != nil {
		t.Fatal(err)
	}
	select {}
}

func TestRecoverQuarantinesUnknownEntriesAndRecoversKnown(t *testing.T) {
	fixture := newMatrixFixture(t)
	// Advance the live participant to its after state; the prepared (uncommitted)
	// transaction must still roll every participant back to before.
	mustApply(t, fixture.tx, "live")

	txRoot := filepath.Join(fixture.root, transactionsDirName)
	buildingDir := filepath.Join(txRoot, buildingPrefix+strings.Repeat("a", 32))
	completedDir := filepath.Join(txRoot, completedPrefix+strings.Repeat("b", 32))
	if err := os.Mkdir(buildingDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(completedDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Two unknown entries with different topologies: a stray file and a stray
	// directory carrying data that must be preserved, never deleted.
	strayFile := filepath.Join(txRoot, "stray-note.txt")
	if err := os.WriteFile(strayFile, []byte("evidence"), 0o600); err != nil {
		t.Fatal(err)
	}
	strayDir := filepath.Join(txRoot, "weird-entry")
	if err := os.Mkdir(strayDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(strayDir, "inner"), []byte("keep-me"), 0o600); err != nil {
		t.Fatal(err)
	}

	summary, err := Recover(fixture.root)
	if err != nil {
		t.Fatal(err)
	}

	// The pending transaction rolled back and the stray journals were cleaned.
	assertFile(t, fixture.live, "live-before", 0o755)
	assertFile(t, fixture.manifest, "manifest-before", 0o600)
	if _, err := os.Lstat(buildingDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("building journal not cleaned: %v", err)
	}
	if _, err := os.Lstat(completedDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("completed journal not cleaned: %v", err)
	}

	// Both unknown entries were quarantined, not deleted, and their data survives.
	if len(summary.Quarantined) != 2 {
		t.Fatalf("summary.Quarantined = %+v, want 2 records", summary.Quarantined)
	}
	originals := map[string]bool{}
	for _, record := range summary.Quarantined {
		originals[record.Original] = true
		if !strings.HasPrefix(record.Quarantined, quarantinedPrefix) {
			t.Fatalf("quarantined name lacks prefix: %q", record.Quarantined)
		}
		if _, err := os.Lstat(filepath.Join(txRoot, record.Original)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("original entry %q still present: %v", record.Original, err)
		}
	}
	if !originals["stray-note.txt"] || !originals["weird-entry"] {
		t.Fatalf("unexpected quarantined originals: %+v", originals)
	}

	status, err := Inspect(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Quarantined) != 2 {
		t.Fatalf("Inspect quarantined = %v, want 2", status.Quarantined)
	}
	if status.NeedsRecovery() {
		t.Fatalf("quarantined-only state must not need recovery: %+v", status)
	}
	// Preserved stray-directory payload is still readable inside quarantine.
	found := false
	for _, name := range status.Quarantined {
		if data, err := os.ReadFile(filepath.Join(txRoot, name, "inner")); err == nil {
			if string(data) != "keep-me" {
				t.Fatalf("quarantined data corrupted: %q", data)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("quarantined directory payload was not preserved")
	}
}

func TestQuarantineUnblocksBeginNewTransaction(t *testing.T) {
	root := t.TempDir()
	txRoot := filepath.Join(root, transactionsDirName)
	if err := os.MkdirAll(txRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(txRoot, "leftover-garbage"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Before quarantine, the unknown entry blocks new transactions.
	if _, err := Begin(root, "upgrade", "tool", []Spec{{
		Role: "live", Path: filepath.Join(t.TempDir(), "tool"), After: RegularBytes([]byte("x"), 0o755),
	}}); err == nil {
		t.Fatal("expected the unknown entry to block Begin before quarantine")
	}

	summary, err := Recover(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(summary.Quarantined) != 1 {
		t.Fatalf("summary.Quarantined = %+v", summary.Quarantined)
	}

	status, err := Inspect(root)
	if err != nil {
		t.Fatal(err)
	}
	if status.NeedsRecovery() {
		t.Fatalf("quarantined state must not need recovery: %+v", status)
	}

	// A fresh transaction now begins and completes normally alongside the
	// quarantined evidence.
	dest := filepath.Join(t.TempDir(), "tool")
	if err := os.WriteFile(dest, []byte("before"), 0o755); err != nil {
		t.Fatal(err)
	}
	tx, err := Begin(root, "upgrade", "tool", []Spec{{
		Role: "live", Path: dest, After: RegularBytes([]byte("after"), 0o755),
	}})
	if err != nil {
		t.Fatalf("Begin after quarantine: %v", err)
	}
	mustApply(t, tx, "live")
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := tx.Finalize(); err != nil {
		t.Fatal(err)
	}
	assertFile(t, dest, "after", 0o755)
}

func TestRecoverQuarantineIsIdempotent(t *testing.T) {
	root := t.TempDir()
	txRoot := filepath.Join(root, transactionsDirName)
	if err := os.MkdirAll(txRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(txRoot, "mystery"), []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}

	first, err := Recover(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Quarantined) != 1 {
		t.Fatalf("first pass quarantined = %+v", first.Quarantined)
	}
	quarantinedName := first.Quarantined[0].Quarantined

	// A recovery interrupted after quarantine and retried must be a no-op: the
	// already-quarantined entry keeps its name and is never wrapped again.
	second, err := Recover(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Quarantined) != 0 {
		t.Fatalf("second pass re-quarantined: %+v", second.Quarantined)
	}
	status, err := Inspect(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Quarantined) != 1 || status.Quarantined[0] != quarantinedName {
		t.Fatalf("quarantine set drifted across retries: %+v", status.Quarantined)
	}
	if data, err := os.ReadFile(filepath.Join(txRoot, quarantinedName)); err != nil || string(data) != "data" {
		t.Fatalf("quarantined payload changed: data=%q err=%v", data, err)
	}
}

func mustApply(t *testing.T, tx *Transaction, role string) {
	t.Helper()
	if err := tx.Apply(role); err != nil {
		t.Fatal(err)
	}
}

func mustConvergeForTest(t *testing.T, tx *Transaction, role string, after bool) {
	t.Helper()
	mutation, ok := tx.mutation(role)
	if !ok {
		t.Fatalf("missing role %s", role)
	}
	target := mutation.Before
	if after {
		target = mutation.After
	}
	if err := convergeMutation(tx.dir, *mutation, target); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func assertFile(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != body {
		t.Fatalf("%s body=%q want %q", path, data, body)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != mode.Perm() {
		t.Fatalf("%s mode=%o want %o", path, info.Mode().Perm(), mode.Perm())
	}
}

func assertJournalClean(t *testing.T, root string) {
	t.Helper()
	status, err := Inspect(root)
	if err != nil {
		t.Fatal(err)
	}
	if status.NeedsRecovery() {
		t.Fatalf("journal not clean: %+v", status)
	}
}
