package supportbundle

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/rtwsvj/hukou/internal/activation"
	"github.com/rtwsvj/hukou/internal/manifest"
	"github.com/rtwsvj/hukou/internal/store"
	statejournal "github.com/rtwsvj/hukou/internal/transaction"
)

func TestCollectAndJSONAreStrictlyReadOnlyAndRedacted(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "Users", "super-secret-user", "private-home", "hukou")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	liveDir := filepath.Join(base, "Users", "super-secret-user", "private-bin")
	if err := os.MkdirAll(liveDir, 0o755); err != nil {
		t.Fatal(err)
	}
	live := filepath.Join(liveDir, "privacy-tool")
	if err := os.WriteFile(live, []byte("live-private-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	sha, err := store.SHA256File(live)
	if err != nil {
		t.Fatal(err)
	}
	m := &manifest.Manifest{
		SchemaVersion: manifest.CurrentSchemaVersion,
		Retention:     manifest.DefaultRetentionPolicy(),
		Entries:       make([]manifest.Entry, 0),
	}
	entry := manifest.Entry{
		Name:         "privacy-tool",
		Path:         live,
		Repo:         "private-owner/private-repo",
		Tag:          "v1.0.0",
		SHA256:       sha,
		Upstream:     "https://github.com/private-owner/private-repo",
		AdoptedAt:    "2026-01-01T00:00:00Z",
		UpdatedAt:    "2026-01-01T00:00:00Z",
		UpdatePolicy: manifest.DefaultUpdatePolicy(),
	}
	if err := activation.RecordAdopt(&entry, "act-privacy", entry.UpdatedAt); err != nil {
		t.Fatal(err)
	}
	m.Put(entry)
	// Sensitive but valid manifest strings must not bypass redaction.
	m.Entries[0].Name = "super-secret-user"
	m.Entries[0].Tag = "private-repo"
	m.Entries[0].Activations[0].Tag = "private-repo"
	if err := m.Save(filepath.Join(root, "manifest.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := statejournal.Begin(root, "support-test", "privacy-tool", []statejournal.Spec{
		{Role: "live", Path: live, After: statejournal.RegularBytes([]byte("WAL_SECRET_PAYLOAD"), 0o755)},
	}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SUPPORT_PRIVATE_ENV", "ENV_SECRET_VALUE")

	before := snapshot(t, root)
	report := Collect(root, Build{Version: "v0.3.0-rc.1", Commit: "private-owner/private-repo", Date: "2026-07-14"})
	var out bytes.Buffer
	if err := WriteJSON(&out, report); err != nil {
		t.Fatal(err)
	}
	after := snapshot(t, root)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("support collection changed state")
	}
	if report.Transactions.Pending != 1 || report.Manifest.EntryCount != 1 {
		t.Fatalf("unexpected report: %+v", report)
	}
	for _, secret := range []string{
		root,
		live,
		"super-secret-user",
		"private-home",
		"private-owner",
		"private-repo",
		"WAL_SECRET_PAYLOAD",
		"ENV_SECRET_VALUE",
	} {
		if strings.Contains(out.String(), secret) {
			t.Fatalf("support JSON leaked %q:\n%s", secret, out.String())
		}
	}
	var decoded Report
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
}

func TestCollectMissingRootDoesNotCreateIt(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing")
	report := Collect(root, Build{Version: "devel"})
	if report.Manifest.Status != "missing" || report.Store.Status != "missing" {
		t.Fatalf("unexpected missing-root report: %+v", report)
	}
	if _, err := os.Lstat(root); !errorsIsNotExist(err) {
		t.Fatalf("collect created data root: %v", err)
	}
}

func TestWriteFileUses0600AndRefusesSymlink(t *testing.T) {
	report := Report{SchemaVersion: ReportSchemaVersion}
	dir := t.TempDir()
	path := filepath.Join(dir, "support.json")
	if err := WriteFile(path, report); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("support file mode = %o", info.Mode().Perm())
	}
	link := filepath.Join(dir, "support-link.json")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(link, report); err == nil {
		t.Fatal("WriteFile replaced a symlink")
	}
}

func errorsIsNotExist(err error) bool { return err != nil && os.IsNotExist(err) }

type snapshotEntry struct {
	Name string
	Mode os.FileMode
	SHA  string
	Link string
}

func snapshot(t *testing.T, root string) []snapshotEntry {
	t.Helper()
	var result []snapshotEntry
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
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
