package store_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rtwsvj/hukou/internal/store"
)

func putVersionRef(t *testing.T, s *store.Store, root, tag string) store.VersionRef {
	t.Helper()
	source := writeFile(t, t.TempDir(), "tool", "content-"+tag)
	if err := s.Put("tool", tag, source); err != nil {
		t.Fatalf("Put %s: %v", tag, err)
	}
	digest, err := store.SHA256File(filepath.Join(root, "tool", tag, "tool"))
	if err != nil {
		t.Fatalf("SHA256 %s: %v", tag, err)
	}
	return store.VersionRef{Tag: tag, SHA256: digest}
}

func putOriginal(t *testing.T, s *store.Store) {
	t.Helper()
	original := writeFile(t, t.TempDir(), "tool", "original")
	if err := s.AdoptOriginal("tool", original); err != nil {
		t.Fatal(err)
	}
}

func TestHistoryAwarePruneProtectsCurrentAncestorsPinAndOriginal(t *testing.T) {
	root := t.TempDir()
	s := &store.Store{Root: root}
	refs := make(map[string]store.VersionRef)
	for _, tag := range []string{"v0.0.0", "v1.0.0", "v2.0.0", "v3.0.0", "v4.0.0"} {
		refs[tag] = putVersionRef(t, s, root, tag)
	}
	putOriginal(t, s)

	// Deliberately make the deletion candidate newest by mtime and the current
	// version oldest. History, not timestamp, must determine retention.
	newest := time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)
	oldest := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(filepath.Join(root, "tool", "v0.0.0"), newest, newest); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(root, "tool", "v4.0.0"), oldest, oldest); err != nil {
		t.Fatal(err)
	}

	plan, err := s.PlanPrune(store.PruneRequest{
		Name:            "tool",
		Current:         refs["v4.0.0"],
		PinnedTag:       "v1.0.0",
		Ancestors:       []store.VersionRef{refs["v3.0.0"], refs["v2.0.0"], refs["v1.0.0"]},
		RetainAncestors: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Delete) != 1 || plan.Delete[0].Tag != "v0.0.0" {
		t.Fatalf("delete plan=%+v", plan.Delete)
	}
	if err := s.ApplyPrunePlan(plan); err != nil {
		t.Fatal(err)
	}
	versions, err := s.Versions("tool")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(versions, ","), "v1.0.0,v2.0.0,v3.0.0,v4.0.0"; got != want {
		t.Fatalf("versions=%s want=%s", got, want)
	}
	if _, err := os.Stat(filepath.Join(root, "tool", "original", "tool")); err != nil {
		t.Fatalf("original was pruned: %v", err)
	}
}

func TestPlanPruneFailsBeforeDeletionOnProtectedDigestMismatch(t *testing.T) {
	root := t.TempDir()
	s := &store.Store{Root: root}
	v1 := putVersionRef(t, s, root, "v1.0.0")
	_ = putVersionRef(t, s, root, "v2.0.0")
	putOriginal(t, s)
	v1.SHA256 = strings.Repeat("0", 64)
	if _, err := s.PlanPrune(store.PruneRequest{Name: "tool", Current: v1}); err == nil {
		t.Fatal("PlanPrune accepted a mismatched current digest")
	}
	versions, err := s.Versions("tool")
	if err != nil || len(versions) != 2 {
		t.Fatalf("versions=%v error=%v", versions, err)
	}
}

func TestApplyPrunePlanRejectsStaleCandidateBeforeRemovingAnything(t *testing.T) {
	root := t.TempDir()
	s := &store.Store{Root: root}
	v1 := putVersionRef(t, s, root, "v1.0.0")
	v2 := putVersionRef(t, s, root, "v2.0.0")
	v3 := putVersionRef(t, s, root, "v3.0.0")
	putOriginal(t, s)
	plan, err := s.PlanPrune(store.PruneRequest{
		Name:            "tool",
		Current:         v3,
		Ancestors:       []store.VersionRef{v2},
		RetainAncestors: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Delete) != 1 || plan.Delete[0].Tag != v1.Tag {
		t.Fatalf("delete plan=%+v", plan.Delete)
	}
	if err := os.WriteFile(filepath.Join(root, "tool", v1.Tag, "tool"), []byte("tampered"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyPrunePlan(plan); err == nil {
		t.Fatal("ApplyPrunePlan accepted a stale candidate")
	}
	versions, err := s.Versions("tool")
	if err != nil || len(versions) != 3 {
		t.Fatalf("versions=%v error=%v", versions, err)
	}
}

func TestPlanPruneRejectsConflictingProtectedTagBindings(t *testing.T) {
	root := t.TempDir()
	s := &store.Store{Root: root}
	current := putVersionRef(t, s, root, "v1.0.0")
	putOriginal(t, s)
	conflict := current
	conflict.SHA256 = strings.Repeat("f", 64)
	_, err := s.PlanPrune(store.PruneRequest{
		Name:            "tool",
		Current:         current,
		Ancestors:       []store.VersionRef{conflict},
		RetainAncestors: 1,
	})
	if err == nil {
		t.Fatal("PlanPrune accepted conflicting protected bindings")
	}
}

func TestPlanPruneFailsClosedOnMalformedVersionDirectory(t *testing.T) {
	root := t.TempDir()
	s := &store.Store{Root: root}
	current := putVersionRef(t, s, root, "v2.0.0")
	_ = putVersionRef(t, s, root, "v1.0.0")
	putOriginal(t, s)
	if err := os.WriteFile(filepath.Join(root, "tool", "v1.0.0", "extra"), []byte("extra"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PlanPrune(store.PruneRequest{Name: "tool", Current: current}); err == nil {
		t.Fatal("PlanPrune accepted malformed version topology")
	}
	if _, err := s.Versions("tool"); err == nil {
		t.Fatal("Versions accepted the same malformed topology")
	}
	for _, tag := range []string{"v1.0.0", "v2.0.0"} {
		if _, err := os.Stat(filepath.Join(root, "tool", tag)); err != nil {
			t.Fatalf("failed prune removed %s: %v", tag, err)
		}
	}
}

func TestPruneHistoryAllowsOriginalAsCurrent(t *testing.T) {
	root := t.TempDir()
	s := &store.Store{Root: root}
	_ = putVersionRef(t, s, root, "v1.0.0")
	putOriginal(t, s)
	originalSHA, err := store.SHA256File(filepath.Join(root, "tool", "original", "tool"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.PruneHistory(store.PruneRequest{
		Name:    "tool",
		Current: store.VersionRef{Tag: "original", SHA256: originalSHA},
	}); err != nil {
		t.Fatal(err)
	}
	versions, err := s.Versions("tool")
	if err != nil || len(versions) != 0 {
		t.Fatalf("versions=%v error=%v", versions, err)
	}
	if _, err := os.Stat(filepath.Join(root, "tool", "original", "tool")); err != nil {
		t.Fatalf("original missing: %v", err)
	}
}

func TestPlanPruneRejectsMalformedOriginalBeforeDeletingVersions(t *testing.T) {
	root := t.TempDir()
	s := &store.Store{Root: root}
	current := putVersionRef(t, s, root, "v2.0.0")
	old := putVersionRef(t, s, root, "v1.0.0")
	putOriginal(t, s)
	if err := os.WriteFile(filepath.Join(root, "tool", "original", "extra"), []byte("unexpected"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PlanPrune(store.PruneRequest{Name: "tool", Current: current}); err == nil {
		t.Fatal("PlanPrune accepted a malformed original backup")
	}
	if _, err := os.Stat(filepath.Join(root, "tool", old.Tag, "tool")); err != nil {
		t.Fatalf("old version changed after rejected plan: %v", err)
	}
}
