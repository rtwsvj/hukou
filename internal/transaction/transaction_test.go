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

			if err := Recover(fixture.root); err != nil {
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

			if err := Recover(fixture.root); err != nil {
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

	err := Recover(fixture.root)
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

	if err := Recover(root); err != nil {
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

	err = Recover(root)
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
	if err := Recover(root); err != nil {
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
	err := Recover(fixture.root)
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
			if err := Recover(root); err != nil {
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
