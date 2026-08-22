package repair

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	statejournal "github.com/rtwsvj/hukou/internal/transaction"
)

// beginPendingWithTornMarker starts a real pending transaction, applies its
// mutation, and then damages the COMMIT marker the way a crash inside
// Commit's create-then-write window would: contents is what a torn write can
// plausibly leave. It returns nothing; callers assert on recovery outcomes.
func beginPendingWithTornMarker(t *testing.T, content func(id string) string) (root, live string) {
	t.Helper()
	root = t.TempDir()
	live = filepath.Join(t.TempDir(), "tool")
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
	status, err := statejournal.Inspect(root)
	if err != nil || len(status.Pending) != 1 {
		t.Fatalf("expected one pending journal, got %+v err=%v", status, err)
	}
	pendingDir := filepath.Join(root, "transactions", status.Pending[0])
	id := strings.TrimPrefix(status.Pending[0], "pending-")
	if err := os.WriteFile(filepath.Join(pendingDir, "COMMIT"), []byte(content(id)), 0o600); err != nil {
		t.Fatal(err)
	}
	return root, live
}

// TestRecoverTransactionPlanConvergesOnTornCommitMarker proves the repair path
// converges on torn COMMIT markers exactly like journal recovery does instead
// of refusing with ErrNotRepairable: planning succeeds and applying rolls the
// transaction back to its before state.
func TestRecoverTransactionPlanConvergesOnTornCommitMarker(t *testing.T) {
	cases := []struct {
		name    string
		content func(id string) string
	}{
		{name: "empty marker", content: func(string) string { return "" }},
		{name: "partial byte prefix of decision", content: func(id string) string { return id[:2] }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, live := beginPendingWithTornMarker(t, tc.content)

			plan, err := BuildPlan(root, ActionRecoverTransaction, time.Now())
			if err != nil {
				t.Fatalf("torn COMMIT marker must stay repairable, got error: %v", err)
			}
			if _, err := Apply(root, plan); err != nil {
				t.Fatal(err)
			}
			if got, err := os.ReadFile(live); err != nil || string(got) != "before" {
				t.Fatalf("torn-marker recovery did not roll back: %q err=%v", got, err)
			}
			status, err := statejournal.Inspect(root)
			if err != nil {
				t.Fatal(err)
			}
			if status.NeedsRecovery() {
				t.Fatalf("transaction state remains after repair: %+v", status)
			}
		})
	}
}

// TestRecoverTransactionPlanStillRejectsAlienCommitMarker keeps the alien /
// corrupt half of the contract fail-closed on the repair side: content that no
// crash window can produce must surface as ErrNotRepairable.
func TestRecoverTransactionPlanStillRejectsAlienCommitMarker(t *testing.T) {
	root, _ := beginPendingWithTornMarker(t, func(id string) string { return id[:8] + "zz" })
	if _, err := BuildPlan(root, ActionRecoverTransaction, time.Now()); !errors.Is(err, ErrNotRepairable) {
		t.Fatalf("alien COMMIT contents must be ErrNotRepairable, got %v", err)
	}
}
