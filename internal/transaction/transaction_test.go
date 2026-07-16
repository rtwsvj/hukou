package transaction

import (
	"errors"
	"fmt"
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

func mkResidue(t *testing.T, root, name string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, transactionsDirName, name), 0o700); err != nil {
		t.Fatal(err)
	}
}

// Card A: the write path stays strictly fail-closed. Begin must refuse to open
// a new transaction while any residue class is present so recovery runs first
// under the state lock. This is the regression guard for the unchanged strict
// check.
func TestBeginRefusesEveryResidueClass(t *testing.T) {
	for name, entry := range map[string]string{
		"building":  ".building-abc",
		"pending":   "pending-abc",
		"completed": "completed-abc",
		"unknown":   "leftover-junk",
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			mkResidue(t, root, entry)
			_, err := Begin(root, "upgrade", "tool", []Spec{{
				Role: "live", Path: filepath.Join(t.TempDir(), "tool"), After: RegularBytes([]byte("x"), 0o755),
			}})
			var pendingErr *PendingError
			if !errors.As(err, &pendingErr) {
				t.Fatalf("residue %q must block Begin with PendingError, got %v", entry, err)
			}
		})
	}
}

// makeVerifiedCompletedResidue runs one full real transaction lifecycle —
// Begin, Apply, Commit — and then forces Finalize's directory removal to fail
// by revoking write permission on the journal directory (the parent of the
// entries RemoveAll must unlink). The result is exactly what a crash or I/O
// failure between COMMIT and cleanup leaves behind: a verified completed-<id>
// journal whose COMMIT marker matches the id, with live state converged.
// It returns the mutated live path and the completed journal directory.
func makeVerifiedCompletedResidue(t *testing.T, root string) (livePath, completedDir string) {
	t.Helper()
	live := filepath.Join(t.TempDir(), "tool")
	mustWrite(t, live, "live-before", 0o755)
	tx, err := Begin(root, "upgrade", "tool", []Spec{{
		Role: "live", Path: live, After: RegularBytes([]byte("live-after"), 0o755),
	}})
	if err != nil {
		t.Fatal(err)
	}
	mustApply(t, tx, "live")
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	status, err := Inspect(root)
	if err != nil || len(status.Pending) != 1 {
		t.Fatalf("expected exactly one pending journal: status=%+v err=%v", status, err)
	}
	journalDir := filepath.Join(root, transactionsDirName, status.Pending[0])
	if err := os.Chmod(journalDir, 0o500); err != nil {
		t.Fatal(err)
	}
	err = tx.Finalize()
	if err == nil || !strings.Contains(err.Error(), "remove completed journal") {
		t.Fatalf("expected Finalize cleanup failure, got %v", err)
	}
	status, err = Inspect(root)
	if err != nil || len(status.Completed) != 1 {
		t.Fatalf("expected completed residue: status=%+v err=%v", status, err)
	}
	completedDir = filepath.Join(root, transactionsDirName, status.Completed[0])
	t.Cleanup(func() { _ = os.Chmod(completedDir, 0o700) })
	return live, completedDir
}

// Card A rework: CheckReadable on clean or missing data roots is a strictly
// read-only no-op with no notes.
func TestCheckReadableCleanRoots(t *testing.T) {
	t.Run("clean", func(t *testing.T) {
		notes, err := CheckReadable(t.TempDir())
		if err != nil {
			t.Fatalf("clean root must not error: %v", err)
		}
		if len(notes) != 0 {
			t.Fatalf("clean root must have no notes, got %v", notes)
		}
	})

	t.Run("missing-data-root", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "missing")
		notes, err := CheckReadable(root)
		if err != nil || len(notes) != 0 {
			t.Fatalf("missing data root: notes=%v err=%v", notes, err)
		}
		if _, statErr := os.Lstat(root); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("read-only check must not create the data root: %v", statErr)
		}
	})
}

// Card A rework: the ONLY tolerated residue class. A genuine committed and
// converged journal (real lifecycle, cleanup forced to fail) yields a
// non-fatal note; the write path (Begin) still refuses it.
func TestCheckReadableAllowsVerifiedCompletedResidue(t *testing.T) {
	root := t.TempDir()
	live, _ := makeVerifiedCompletedResidue(t, root)

	notes, err := CheckReadable(root)
	if err != nil {
		t.Fatalf("verified completed residue must not block a read: %v", err)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "stale journal residue; run a mutating command or repair to clean") {
		t.Fatalf("expected one stale-residue note, got %v", notes)
	}
	if !strings.Contains(notes[0], "completed=1") {
		t.Fatalf("note should report the completed count, got %v", notes)
	}
	// Committed means converged: readers observe the after state.
	assertFile(t, live, "live-after", 0o755)

	// The write path stays strictly fail-closed on the same residue.
	_, err = Begin(root, "upgrade", "other", []Spec{{
		Role: "live", Path: filepath.Join(t.TempDir(), "other"), After: RegularBytes([]byte("x"), 0o755),
	}})
	var pendingErr *PendingError
	if !errors.As(err, &pendingErr) {
		t.Fatalf("Begin must refuse completed residue with PendingError, got %v", err)
	}
}

// Card A rework: everything that is not a VERIFIED completed-* journal fails
// closed. Malformed and adversarial entries are hand-crafted on purpose: name
// forgery is exactly the input class this check must reject.
func TestCheckReadableFailsClosedOnUnverifiedResidue(t *testing.T) {
	goodID := strings.Repeat("a", 32)
	otherID := strings.Repeat("b", 32)

	assertFailClosed := func(t *testing.T, root string) {
		t.Helper()
		notes, err := CheckReadable(root)
		var pendingErr *PendingError
		if !errors.As(err, &pendingErr) {
			t.Fatalf("expected fail-closed PendingError, got notes=%v err=%v", notes, err)
		}
		if notes != nil {
			t.Fatalf("blocked read must not return notes, got %v", notes)
		}
	}

	t.Run("unknown-junk-name", func(t *testing.T) {
		root := t.TempDir()
		mkResidue(t, root, "leftover-junk")
		assertFailClosed(t, root)
	})

	t.Run("building-forged-name", func(t *testing.T) {
		root := t.TempDir()
		mkResidue(t, root, ".building-"+goodID)
		assertFailClosed(t, root)
	})

	t.Run("pending-forged-name", func(t *testing.T) {
		root := t.TempDir()
		mkResidue(t, root, "pending-"+goodID)
		assertFailClosed(t, root)
	})

	t.Run("completed-non-hex-id", func(t *testing.T) {
		root := t.TempDir()
		mkResidue(t, root, "completed-"+strings.Repeat("z", 32))
		assertFailClosed(t, root)
	})

	t.Run("completed-short-id", func(t *testing.T) {
		root := t.TempDir()
		mkResidue(t, root, "completed-"+strings.Repeat("a", 31))
		assertFailClosed(t, root)
	})

	t.Run("completed-uppercase-id", func(t *testing.T) {
		root := t.TempDir()
		mkResidue(t, root, "completed-"+strings.Repeat("A", 32))
		assertFailClosed(t, root)
	})

	t.Run("completed-missing-commit", func(t *testing.T) {
		root := t.TempDir()
		mkResidue(t, root, "completed-"+goodID)
		assertFailClosed(t, root)
	})

	t.Run("completed-wrong-id-commit", func(t *testing.T) {
		root := t.TempDir()
		mkResidue(t, root, "completed-"+goodID)
		commit := filepath.Join(root, transactionsDirName, "completed-"+goodID, commitFileName)
		if err := os.WriteFile(commit, []byte(otherID+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		assertFailClosed(t, root)
	})

	t.Run("completed-symlinked-directory", func(t *testing.T) {
		root := t.TempDir()
		// A convincing target: a real directory holding a COMMIT that matches
		// the forged name. The symlink topology alone must reject it.
		target := filepath.Join(t.TempDir(), "target")
		if err := os.MkdirAll(target, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(target, commitFileName), []byte(goodID+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(root, transactionsDirName), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(root, transactionsDirName, "completed-"+goodID)); err != nil {
			t.Fatal(err)
		}
		assertFailClosed(t, root)
	})

	t.Run("completed-symlinked-commit", func(t *testing.T) {
		root := t.TempDir()
		mkResidue(t, root, "completed-"+goodID)
		payload := filepath.Join(t.TempDir(), "payload")
		if err := os.WriteFile(payload, []byte(goodID+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		commit := filepath.Join(root, transactionsDirName, "completed-"+goodID, commitFileName)
		if err := os.Symlink(payload, commit); err != nil {
			t.Fatal(err)
		}
		assertFailClosed(t, root)
	})

	t.Run("verified-completed-plus-junk", func(t *testing.T) {
		root := t.TempDir()
		makeVerifiedCompletedResidue(t, root)
		mkResidue(t, root, "leftover-junk")
		assertFailClosed(t, root)
	})
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
	// Two unknown non-directory entries: a stray file and a stray symlink. Both
	// carry data that must be preserved, never deleted.
	strayFile := filepath.Join(txRoot, "stray-note.txt")
	if err := os.WriteFile(strayFile, []byte("evidence"), 0o600); err != nil {
		t.Fatal(err)
	}
	strayLink := filepath.Join(txRoot, "stray-link")
	if err := os.Symlink("dangling-target", strayLink); err != nil {
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
	byOriginal := map[string]QuarantineRecord{}
	for _, record := range summary.Quarantined {
		byOriginal[record.Original] = record
		if !strings.HasPrefix(record.Quarantined, quarantinedPrefix) {
			t.Fatalf("quarantined name lacks prefix: %q", record.Quarantined)
		}
		if len(record.Quarantined) != len(quarantinedPrefix)+16 {
			t.Fatalf("quarantined name is not length-controlled: %q", record.Quarantined)
		}
		if _, err := os.Lstat(filepath.Join(txRoot, record.Original)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("original entry %q still present: %v", record.Original, err)
		}
		meta, err := os.ReadFile(filepath.Join(txRoot, record.Quarantined, quarantineMetaFileName))
		if err != nil || !strings.Contains(string(meta), fmt.Sprintf("original_name=%q", record.Original)) {
			t.Fatalf("META does not record the original name: meta=%q err=%v", meta, err)
		}
	}
	fileRecord, ok := byOriginal["stray-note.txt"]
	if !ok {
		t.Fatalf("stray file not quarantined: %+v", byOriginal)
	}
	if data, err := os.ReadFile(filepath.Join(txRoot, fileRecord.Quarantined, quarantinePayloadName)); err != nil || string(data) != "evidence" {
		t.Fatalf("quarantined payload corrupted: data=%q err=%v", data, err)
	}
	linkRecord, ok := byOriginal["stray-link"]
	if !ok {
		t.Fatalf("stray symlink not quarantined: %+v", byOriginal)
	}
	if target, err := os.Readlink(filepath.Join(txRoot, linkRecord.Quarantined, quarantinePayloadName)); err != nil || target != "dangling-target" {
		t.Fatalf("quarantined symlink corrupted: target=%q err=%v", target, err)
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
}

func TestRecoverFailsClosedOnUnknownDirectory(t *testing.T) {
	fixture := newMatrixFixture(t)
	mustApply(t, fixture.tx, "live")

	txRoot := filepath.Join(fixture.root, transactionsDirName)
	// A stray file is junk and gets isolated; an unknown directory may be a
	// journal layout from a newer hukou and must wedge recovery instead of
	// being demoted to quarantine.
	strayFile := filepath.Join(txRoot, "stray-note.txt")
	if err := os.WriteFile(strayFile, []byte("evidence"), 0o600); err != nil {
		t.Fatal(err)
	}
	futureDir := filepath.Join(txRoot, "future-journal")
	if err := os.Mkdir(futureDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(futureDir, "evidence"), []byte("keep-me"), 0o600); err != nil {
		t.Fatal(err)
	}

	summary, err := Recover(fixture.root)
	if err == nil || !strings.Contains(err.Error(), "unknown directories") || !strings.Contains(err.Error(), "hukou doctor") {
		t.Fatalf("expected fail-closed unknown-directory error, got %v", err)
	}
	if len(summary.Quarantined) != 1 || summary.Quarantined[0].Original != "stray-note.txt" {
		t.Fatalf("non-directory junk not isolated first: %+v", summary.Quarantined)
	}
	// The unknown directory and its contents are untouched.
	if data, readErr := os.ReadFile(filepath.Join(futureDir, "evidence")); readErr != nil || string(data) != "keep-me" {
		t.Fatalf("unknown directory was modified: data=%q err=%v", data, readErr)
	}
	// Fail-closed means no journal was recovered: live keeps the half-applied
	// after state and the pending journal remains as evidence.
	assertFile(t, fixture.live, "live-after", 0o755)
	status, inspectErr := Inspect(fixture.root)
	if inspectErr != nil || len(status.Pending) != 1 {
		t.Fatalf("pending evidence not retained: status=%+v err=%v", status, inspectErr)
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
	if data, err := os.ReadFile(filepath.Join(txRoot, quarantinedName, quarantinePayloadName)); err != nil || string(data) != "data" {
		t.Fatalf("quarantined payload changed: data=%q err=%v", data, err)
	}
}

func TestQuarantineNameBoundaries(t *testing.T) {
	longName := strings.Repeat("n", 200) + ".junk"
	backslashName := `stray\entry`
	for _, name := range []string{longName, backslashName} {
		t.Run(fmt.Sprintf("len=%d", len(name)), func(t *testing.T) {
			root := t.TempDir()
			txRoot := filepath.Join(root, transactionsDirName)
			if err := os.MkdirAll(txRoot, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(txRoot, name), []byte("payload"), 0o600); err != nil {
				t.Fatal(err)
			}
			summary, err := Recover(root)
			if err != nil {
				t.Fatalf("boundary name wedged quarantine: %v", err)
			}
			if len(summary.Quarantined) != 1 || summary.Quarantined[0].Original != name {
				t.Fatalf("summary = %+v", summary.Quarantined)
			}
			container := summary.Quarantined[0].Quarantined
			// The container name is length-controlled regardless of the original.
			if len(container) != len(quarantinedPrefix)+16 {
				t.Fatalf("container name not length-controlled: %q", container)
			}
			meta, err := os.ReadFile(filepath.Join(txRoot, container, quarantineMetaFileName))
			if err != nil || !strings.Contains(string(meta), fmt.Sprintf("original_name=%q", name)) {
				t.Fatalf("META lost the original name: meta=%q err=%v", meta, err)
			}
			if data, err := os.ReadFile(filepath.Join(txRoot, container, quarantinePayloadName)); err != nil || string(data) != "payload" {
				t.Fatalf("payload lost: data=%q err=%v", data, err)
			}
		})
	}
}

func TestQuarantineCollisionRetriesWithoutOverwriting(t *testing.T) {
	root := t.TempDir()
	txRoot := filepath.Join(root, transactionsDirName)
	if err := os.MkdirAll(txRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(txRoot, "stray"), []byte("new-data"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Pre-create a valid container whose name the first random draw will collide
	// with. It holds existing evidence that must never be overwritten.
	collide := strings.Repeat("c", 16)
	fresh := strings.Repeat("f", 16)
	existing := filepath.Join(txRoot, quarantinedPrefix+collide)
	if err := os.Mkdir(existing, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(existing, quarantineMetaFileName), []byte("old-meta"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(existing, quarantinePayloadName), []byte("old-evidence"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldSuffix := quarantineNameSuffix
	calls := 0
	quarantineNameSuffix = func() (string, error) {
		calls++
		if calls == 1 {
			return collide, nil
		}
		return fresh, nil
	}
	t.Cleanup(func() { quarantineNameSuffix = oldSuffix })

	summary, err := Recover(root)
	if err != nil {
		t.Fatal(err)
	}
	if calls < 2 {
		t.Fatalf("collision was not retried: calls=%d", calls)
	}
	if len(summary.Quarantined) != 1 || summary.Quarantined[0].Quarantined != quarantinedPrefix+fresh {
		t.Fatalf("summary = %+v", summary.Quarantined)
	}
	// The colliding container is untouched and the new container has the data.
	if data, err := os.ReadFile(filepath.Join(existing, quarantinePayloadName)); err != nil || string(data) != "old-evidence" {
		t.Fatalf("existing container was overwritten: data=%q err=%v", data, err)
	}
	if data, err := os.ReadFile(filepath.Join(txRoot, quarantinedPrefix+fresh, quarantinePayloadName)); err != nil || string(data) != "new-data" {
		t.Fatalf("new container payload wrong: data=%q err=%v", data, err)
	}
}

func TestSubprocessSIGKILLRecoveryWithStrayEntry(t *testing.T) {
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
			// The crash left a real journal from a real Begin (+ Commit for the
			// committed stage). Drop stray junk next to it, as an interrupted or
			// misbehaving process would.
			txRoot := filepath.Join(root, transactionsDirName)
			if err := os.WriteFile(filepath.Join(txRoot, "crash-debris"), []byte("junk"), 0o600); err != nil {
				t.Fatal(err)
			}

			summary, err := Recover(root)
			if err != nil {
				t.Fatal(err)
			}
			if len(summary.Quarantined) != 1 || summary.Quarantined[0].Original != "crash-debris" {
				t.Fatalf("stray junk not quarantined: %+v", summary.Quarantined)
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

func TestInspectRejectsInvalidQuarantineContainers(t *testing.T) {
	root := t.TempDir()
	txRoot := filepath.Join(root, transactionsDirName)
	if err := os.MkdirAll(txRoot, 0o700); err != nil {
		t.Fatal(err)
	}

	valid := filepath.Join(txRoot, "quarantined-"+strings.Repeat("a", 16))
	if err := os.Mkdir(valid, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(valid, quarantineMetaFileName), []byte("meta"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(valid, quarantinePayloadName), []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}

	invalid := []string{
		"quarantined-" + strings.Repeat("b", 15),            // too short
		"quarantined-" + strings.Repeat("c", 17),            // too long
		"quarantined-" + strings.Repeat("d", 32),            // wrong length
		"quarantined-" + strings.Repeat("E", 16),            // uppercase hex
		"quarantined-" + strings.Repeat("f", 16) + "-extra", // extra suffix
		"quarantined-" + strings.Repeat("g", 16),            // non-hex
	}
	for _, name := range invalid {
		if err := os.Mkdir(filepath.Join(txRoot, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}

	// A directory symlink with a valid-looking name is not a real directory.
	linkName := "quarantined-" + strings.Repeat("b", 16)
	if err := os.Symlink(valid, filepath.Join(txRoot, linkName)); err != nil {
		t.Fatal(err)
	}

	// A valid name with an untrusted layout (extra entry) is also Unknown.
	untrusted := filepath.Join(txRoot, "quarantined-"+strings.Repeat("c", 16))
	if err := os.Mkdir(untrusted, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(untrusted, quarantineMetaFileName), []byte("meta"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(untrusted, quarantinePayloadName), []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(untrusted, "extra"), []byte("extra"), 0o600); err != nil {
		t.Fatal(err)
	}

	// A payload that is a directory is not a valid quarantine container.
	payloadDir := filepath.Join(txRoot, "quarantined-"+strings.Repeat("d", 16))
	if err := os.Mkdir(payloadDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(payloadDir, quarantineMetaFileName), []byte("meta"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(payloadDir, quarantinePayloadName), 0o700); err != nil {
		t.Fatal(err)
	}

	// A META that is a symlink is not a valid quarantine container.
	metaSymlink := filepath.Join(txRoot, "quarantined-"+strings.Repeat("h", 16))
	if err := os.Mkdir(metaSymlink, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("somewhere", filepath.Join(metaSymlink, quarantineMetaFileName)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(metaSymlink, quarantinePayloadName), []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}

	status, err := Inspect(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Quarantined) != 1 || status.Quarantined[0] != filepath.Base(valid) {
		t.Fatalf("want only valid container quarantined, got %+v", status)
	}
	wantUnknown := len(invalid) + 4 // invalid names + symlink + untrusted layout + payload-dir + META-symlink
	if len(status.Unknown) != wantUnknown {
		t.Fatalf("want %d unknown entries, got %+v", wantUnknown, status.Unknown)
	}
}

func TestRecoverQuarantinesInvalidQuarantineFileButFailsClosedOnInvalidDirectory(t *testing.T) {
	root := t.TempDir()
	txRoot := filepath.Join(root, transactionsDirName)
	if err := os.MkdirAll(txRoot, 0o700); err != nil {
		t.Fatal(err)
	}

	// An invalid quarantined-like file is treated as unknown non-directory junk
	// and wrapped into a fresh valid container.
	badFile := filepath.Join(txRoot, "quarantined-notahex")
	if err := os.WriteFile(badFile, []byte("junk"), 0o600); err != nil {
		t.Fatal(err)
	}

	// An invalid quarantined-like directory is fail-closed: it blocks recovery.
	badDir := filepath.Join(txRoot, "quarantined-"+strings.Repeat("d", 16)+"-extra")
	if err := os.Mkdir(badDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(badDir, "evidence"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}

	summary, err := Recover(root)
	if err == nil || !strings.Contains(err.Error(), "unknown directories") {
		t.Fatalf("expected fail-closed on invalid quarantine directory, got %v", err)
	}
	if len(summary.Quarantined) != 1 || summary.Quarantined[0].Original != "quarantined-notahex" {
		t.Fatalf("expected the invalid quarantine file to be quarantined, got %+v", summary.Quarantined)
	}
	if _, err := os.Lstat(badDir); err != nil {
		t.Fatalf("invalid quarantine directory was modified: %v", err)
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
