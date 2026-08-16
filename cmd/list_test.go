package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rtwsvj/hukou/internal/manifest"
)

func TestListFailsClosedOnInvalidStoreTopology(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("HUKOU_DATA_DIR", dataDir)

	binPath := filepath.Join(t.TempDir(), "tool")
	if err := os.WriteFile(binPath, []byte("tool\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	m := &manifest.Manifest{SchemaVersion: 1, Entries: []manifest.Entry{{
		Name:      "tool",
		Path:      binPath,
		Tag:       "local",
		SHA256:    strings.Repeat("a", 64),
		AdoptedAt: "2026-07-14T00:00:00Z",
		UpdatedAt: "2026-07-14T00:00:00Z",
	}}}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := m.Save(filepath.Join(dataDir, "manifest.json")); err != nil {
		t.Fatal(err)
	}

	storeDir := filepath.Join(dataDir, "store")
	outside := t.TempDir()
	if err := os.MkdirAll(storeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(storeDir, "tool")); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	err := doList(&out)
	if err == nil || !strings.Contains(err.Error(), "store directory must not be a symlink") {
		t.Fatalf("expected invalid store topology error, got %v output=%q", err, out.String())
	}
}

func TestListFailsClosedWhenTransactionIsPending(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("HUKOU_DATA_DIR", dataDir)
	pending := filepath.Join(dataDir, "transactions", "pending-test")
	if err := os.MkdirAll(pending, 0o700); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	err := doList(&out)
	if err == nil || !strings.Contains(err.Error(), "unfinished transaction") {
		t.Fatalf("expected pending transaction error, got %v output=%q", err, out.String())
	}
}

func TestListFailsClosedWhenManifestStoreIsMissingOrOriginalIsEmpty(t *testing.T) {
	for _, test := range []struct {
		name  string
		setup func(t *testing.T, dataDir string)
	}{
		{name: "missing tool store"},
		{
			name: "empty original",
			setup: func(t *testing.T, dataDir string) {
				t.Helper()
				if err := os.MkdirAll(filepath.Join(dataDir, "store", "tool", "original"), 0o755); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			dataDir := t.TempDir()
			t.Setenv("HUKOU_DATA_DIR", dataDir)
			livePath := filepath.Join(t.TempDir(), "tool")
			if err := os.WriteFile(livePath, []byte("tool\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			m := &manifest.Manifest{SchemaVersion: 1, Entries: []manifest.Entry{{
				Name:      "tool",
				Path:      livePath,
				Tag:       "local",
				SHA256:    strings.Repeat("a", 64),
				AdoptedAt: "2026-07-14T00:00:00Z",
				UpdatedAt: "2026-07-14T00:00:00Z",
			}}}
			if err := os.MkdirAll(dataDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := m.Save(filepath.Join(dataDir, "manifest.json")); err != nil {
				t.Fatal(err)
			}
			if test.setup != nil {
				test.setup(t, dataDir)
			}

			var out bytes.Buffer
			err := doList(&out)
			if err == nil || !strings.Contains(err.Error(), "inspect original backup") {
				t.Fatalf("expected original-backup error, got %v output=%q", err, out.String())
			}
		})
	}
}
