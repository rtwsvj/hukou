package store

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Version flood: 100 retained versions for one tool, then a prune plan bound to
// a shallow retention must protect current/pin/original and delete exactly the
// rest — never consulting mtimes, never touching protected artifacts.
func TestVersionFloodAndPrune(t *testing.T) {
	root := t.TempDir()
	s := &Store{Root: root}
	bin := filepath.Join(t.TempDir(), "tool")
	if err := os.WriteFile(bin, []byte("v0"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := s.AdoptOriginal("tool", bin); err != nil {
		t.Fatal(err)
	}
	// 100 versions, each with distinct bytes so hashes differ.
	refs := make([]VersionRef, 0, 100)
	for i := 1; i <= 100; i++ {
		src := filepath.Join(t.TempDir(), fmt.Sprintf("v%d", i))
		if err := os.WriteFile(src, []byte(strings.Repeat("x", i)), 0o755); err != nil {
			t.Fatal(err)
		}
		sha, err := s.PutWithDigest("tool", fmt.Sprintf("v%d", i), src)
		if err != nil {
			t.Fatalf("Put v%d: %v", i, err)
		}
		refs = append(refs, VersionRef{Tag: fmt.Sprintf("v%d", i), SHA256: sha})
	}
	// Activate the newest as current, pin v50.
	if err := s.Activate("tool", "v100", bin); err != nil {
		t.Fatalf("Activate v100: %v", err)
	}
	// Ancestors are supplied most-recent-first, matching the real caller
	// (activation lineage): v99, v98, ... v1.
	ancestors := make([]VersionRef, 0, len(refs)-1)
	for i := len(refs) - 2; i >= 0; i-- {
		ancestors = append(ancestors, refs[i])
	}
	request := PruneRequest{
		Name:            "tool",
		Current:         refs[len(refs)-1],
		PinnedTag:       "v50",
		Ancestors:       ancestors,
		RetainAncestors: 5,
	}
	plan, err := s.PlanPrune(request)
	if err != nil {
		t.Fatalf("PlanPrune: %v", err)
	}
	if len(plan.Delete) != 93 { // 100 versions - current v100 - pin v50 - 5 retained ancestors (v99..v95)
		t.Fatalf("delete count = %d, want 93", len(plan.Delete))
	}
	protectedTags := map[string]bool{"v100": true, "v50": true, "v99": true, "v98": true, "v97": true, "v96": true, "v95": true, "original": true}
	for _, p := range plan.Protected {
		if !protectedTags[p.Tag] {
			t.Fatalf("unexpectedly protected %q", p.Tag)
		}
		delete(protectedTags, p.Tag)
	}
	for tag := range protectedTags {
		t.Fatalf("expected protection missing for %q", tag)
	}
	// Determinism: the same request on the SAME pre-apply state must produce
	// the identical plan.
	planAgain, err := s.PlanPrune(request)
	if err != nil {
		t.Fatal(err)
	}
	if len(planAgain.Delete) != len(plan.Delete) {
		t.Fatalf("prune plan not deterministic: %d vs %d", len(planAgain.Delete), len(plan.Delete))
	}
	if err := s.ApplyPrunePlan(plan); err != nil {
		t.Fatalf("ApplyPrunePlan: %v", err)
	}
	// Protected artifacts must still exist; deleted ones must be gone.
	for _, tag := range []string{"v100", "v50", "v99", "v98", "v97", "v96", "v95"} {
		if _, err := s.ActivationSource("tool", tag); err != nil {
			t.Fatalf("protected %s missing after prune: %v", tag, err)
		}
	}
	for _, tag := range []string{"v1", "v2", "v90"} {
		if _, err := s.ActivationSource("tool", tag); err == nil {
			t.Fatalf("deleted %s still present after prune", tag)
		}
	}
	if _, err := s.Original("tool"); err != nil {
		t.Fatalf("original lost after prune: %v", err)
	}
	// After apply, a re-plan must delete nothing: the store has converged.
	post, err := s.PlanPrune(request)
	if err != nil {
		t.Fatal(err)
	}
	if len(post.Delete) != 0 {
		t.Fatalf("post-apply plan wants to delete %d entries; state did not converge", len(post.Delete))
	}
}

// Leak smoke: repeated adopt+store transactions must not leak file descriptors
// or leave transaction residue.
func TestRepeatedAdoptNoFdLeak(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("fd counting via lsof is macOS-specific here")
	}
	countFD := func() int {
		out, err := exec.Command("lsof", "-p", fmt.Sprintf("%d", os.Getpid())).Output()
		if err != nil {
			t.Skipf("lsof unavailable: %v", err)
		}
		return strings.Count(string(out), "\n")
	}
	root := t.TempDir()
	s := &Store{Root: root}
	before := countFD()
	for i := 0; i < 300; i++ {
		name := fmt.Sprintf("tool%d", i)
		src := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(src, []byte("x"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := s.AdoptOriginal(name, src); err != nil {
			t.Fatalf("AdoptOriginal %d: %v", i, err)
		}
		if err := s.Put(name, "v1", src); err != nil {
			t.Fatalf("Put %d: %v", i, err)
		}
	}
	after := countFD()
	if after > before+8 {
		t.Fatalf("fd leak: before=%d after=%d", before, after)
	}
}
