package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	statejournal "github.com/rtwsvj/hukou/internal/transaction"
)

// TestActivationRejectsStoreArtifactTamperedBeforeCapture reproduces the
// store-and-activate segment of upgradeOne with an adversary who rewrites the
// immutable store artifact after PutWithDigest returned its digest but before
// the transaction journal captures the activation source. This is the window
// card C stopped re-hashing (the old `store.SHA256File(targetSource)` call);
// the test proves the downstream backstop upgradeOne relies on instead:
// statejournal.Begin independently hashes the artifact while capturing it, and
// validateTransactionStateSHA(tx, "live", targetSHA, true) — the exact check
// upgradeOne performs before Apply — fails closed on the mismatch, so the
// tampered bytes never reach the live path.
func TestActivationRejectsStoreArtifactTamperedBeforeCapture(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("HUKOU_DATA_DIR", dataDir)

	s := newStore()
	src := filepath.Join(t.TempDir(), "tool")
	if err := os.WriteFile(src, []byte("genuine-v2-bytes\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	digest, err := s.PutWithDigest("tool", "v2.0.0", src)
	if err != nil {
		t.Fatalf("PutWithDigest: %v", err)
	}
	targetSource, err := s.ActivationSource("tool", "v2.0.0")
	if err != nil {
		t.Fatalf("ActivationSource: %v", err)
	}

	livePath := filepath.Join(t.TempDir(), "tool")
	if err := os.WriteFile(livePath, []byte("old-live-bytes\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	liveSpec := []statejournal.Spec{
		{Role: "live", Path: livePath, After: statejournal.RegularFile(targetSource)},
	}

	t.Run("genuine artifact passes the pre-activation check", func(t *testing.T) {
		tx, err := statejournal.Begin(dataRoot(), "upgrade", "tool", liveSpec)
		if err != nil {
			t.Fatalf("Begin: %v", err)
		}
		if err := validateTransactionStateSHA(tx, "live", digest, true); err != nil {
			t.Fatalf("genuine artifact rejected: %v", err)
		}
		if err := tx.Abort(); err != nil {
			t.Fatalf("Abort: %v", err)
		}
	})

	t.Run("tampered artifact is rejected before activation", func(t *testing.T) {
		if err := os.WriteFile(targetSource, []byte("tampered-payload\n"), 0o755); err != nil {
			t.Fatalf("tamper store artifact: %v", err)
		}
		tx, err := statejournal.Begin(dataRoot(), "upgrade", "tool", liveSpec)
		if err != nil {
			t.Fatalf("Begin: %v", err)
		}
		err = validateTransactionStateSHA(tx, "live", digest, true)
		if err == nil {
			_ = tx.Abort()
			t.Fatal("tampered store artifact passed pre-activation digest validation")
		}
		if !strings.Contains(err.Error(), "SHA-256 mismatch") {
			_ = tx.Abort()
			t.Fatalf("unexpected rejection error: %v", err)
		}
		// upgradeOne aborts on exactly this failure; the abort must recover to a
		// clean journal and the live path must be untouched.
		if abortErr := abortStateTransaction(tx, err); abortErr == nil || !strings.Contains(abortErr.Error(), "SHA-256 mismatch") {
			t.Fatalf("abortStateTransaction = %v", abortErr)
		}
		if cleanErr := statejournal.CheckClean(dataRoot()); cleanErr != nil {
			t.Fatalf("journal not clean after abort: %v", cleanErr)
		}
		got, readErr := os.ReadFile(livePath)
		if readErr != nil || string(got) != "old-live-bytes\n" {
			t.Fatalf("live path disturbed: content=%q err=%v", got, readErr)
		}
	})
}
