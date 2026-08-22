package transaction

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestHasValidCommitClassifiesTornMarkers pins the torn-COMMIT contract at the
// classifier level: only the exact fully written "<id>\n" marker counts as
// committed, anything the crash window can plausibly leave behind (empty file,
// partial write, missing trailing newline) classifies as never-committed, and
// alien content stays a hard error.
func TestHasValidCommitClassifiesTornMarkers(t *testing.T) {
	fixture := newMatrixFixture(t)
	id := fixture.tx.intent.ID
	markerPath := filepath.Join(fixture.tx.dir, commitFileName)

	cases := []struct {
		name          string
		content       string
		wantCommitted bool
		wantErr       string
	}{
		{name: "exact durable decision", content: id + "\n", wantCommitted: true},
		{name: "empty marker", content: "", wantCommitted: false},
		{name: "whitespace only marker", content: "\n", wantCommitted: false},
		{name: "single byte prefix", content: id[:1], wantCommitted: false},
		{name: "two byte prefix", content: id[:2], wantCommitted: false},
		{name: "full id without newline", content: id, wantCommitted: false},
		{name: "alien short content", content: "torn\n", wantErr: "invalid COMMIT marker contents"},
		{name: "alien long content", content: strings.Repeat("x", 200) + "\n", wantErr: "invalid COMMIT marker contents"},
		{name: "prefix then divergence", content: id[:8] + "zz", wantErr: "invalid COMMIT marker contents"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(markerPath, []byte(tc.content), 0o600); err != nil {
				t.Fatal(err)
			}
			committed, err := hasValidCommit(fixture.tx.dir, id)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("expected %q error, got committed=%v err=%v", tc.wantErr, committed, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if committed != tc.wantCommitted {
				t.Fatalf("committed = %v, want %v", committed, tc.wantCommitted)
			}
		})
	}
}

// TestHasValidCommitRejectsMarkerTopologySwap keeps the topology half of the
// contract fail-closed: a COMMIT swapped for a symlink is evidence tampering,
// not a torn write.
func TestHasValidCommitRejectsMarkerTopologySwap(t *testing.T) {
	fixture := newMatrixFixture(t)
	markerPath := filepath.Join(fixture.tx.dir, commitFileName)
	if err := os.WriteFile(markerPath, []byte(fixture.tx.intent.ID+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(markerPath); err != nil {
		t.Fatal(err)
	}
	decoy := filepath.Join(t.TempDir(), "decoy")
	if err := os.WriteFile(decoy, []byte(fixture.tx.intent.ID+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(decoy, markerPath); err != nil {
		t.Fatal(err)
	}
	if _, err := hasValidCommit(fixture.tx.dir, fixture.tx.intent.ID); err == nil || !strings.Contains(err.Error(), "invalid COMMIT marker topology") {
		t.Fatalf("expected topology error, got %v", err)
	}
}

// TestRecoverRollsBackTornCommitMarker proves the crash-window end to end: a
// transaction whose mutations already landed but whose COMMIT write was torn
// must roll back like an uncommitted PREPARED transaction instead of wedging
// every write command behind a hard error.
func TestRecoverRollsBackTornCommitMarker(t *testing.T) {
	cases := []struct {
		name    string
		content func(id string) string
	}{
		{name: "empty COMMIT marker", content: func(string) string { return "" }},
		{name: "partial byte prefix of decision", content: func(id string) string { return id[:2] }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newMatrixFixture(t)
			mustApply(t, fixture.tx, "live")
			mustApply(t, fixture.tx, "manifest")
			if err := os.WriteFile(filepath.Join(fixture.tx.dir, commitFileName), []byte(tc.content(fixture.tx.intent.ID)), 0o600); err != nil {
				t.Fatal(err)
			}

			if _, err := Recover(fixture.root); err != nil {
				t.Fatalf("torn COMMIT marker must roll back, got error: %v", err)
			}
			assertFile(t, fixture.live, "live-before", 0o755)
			assertFile(t, fixture.manifest, "manifest-before", 0o600)
			assertJournalClean(t, fixture.root)
		})
	}
}
