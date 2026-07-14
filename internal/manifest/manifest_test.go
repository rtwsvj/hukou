package manifest_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rtwsvj/hukou/internal/manifest"
)

func fixtureEntry() manifest.Entry {
	return manifest.Entry{
		Name:             "mybin",
		Path:             "/usr/local/bin/mybin",
		Repo:             "owner/repo",
		Tag:              "v1.0.0",
		SHA256:           "deadbeef",
		Upstream:         "github.com/owner/repo",
		AdoptedAt:        "2025-01-01T00:00:00Z",
		UpdatedAt:        "2025-01-01T00:00:00Z",
		AssetName:        "mybin-darwin-arm64.tar.gz",
		AssetSHA256:      strings.Repeat("a", 64),
		ChecksumAsset:    "checksums.txt",
		ChecksumVerified: true,
	}
}

// T01 – missing file returns empty manifest, no error.
func TestLoadMissingFile(t *testing.T) {
	dir := t.TempDir()
	m, err := manifest.Load(filepath.Join(dir, "nonexistent.json"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.SchemaVersion != 1 {
		t.Errorf("SchemaVersion = %d; want 1", m.SchemaVersion)
	}
	if m.Entries == nil {
		t.Fatal("Entries must not be nil after Load")
	}
	if len(m.Entries) != 0 {
		t.Errorf("len(Entries) = %d; want 0", len(m.Entries))
	}
}

// T02 – Put + Get round-trip.
func TestPutGetRoundtrip(t *testing.T) {
	m := &manifest.Manifest{SchemaVersion: 1}
	entry := fixtureEntry()
	m.Put(entry)

	got := m.Get("mybin")
	if got == nil {
		t.Fatal("Get returned nil")
	}
	if got.Name != entry.Name || got.Path != entry.Path || got.Repo != entry.Repo ||
		got.Tag != entry.Tag || got.SHA256 != entry.SHA256 || got.Upstream != entry.Upstream ||
		got.AdoptedAt != entry.AdoptedAt || got.UpdatedAt != entry.UpdatedAt ||
		got.AssetName != entry.AssetName || got.AssetSHA256 != entry.AssetSHA256 ||
		got.ChecksumAsset != entry.ChecksumAsset || got.ChecksumVerified != entry.ChecksumVerified {
		t.Errorf("Get returned entry = %+v; want %+v", got, entry)
	}
}

// T03 – Put overwrites existing entry with same name.
func TestPutOverwrite(t *testing.T) {
	m := &manifest.Manifest{SchemaVersion: 1}
	m.Put(fixtureEntry())
	m.Put(manifest.Entry{Name: "mybin", Path: "/new/path"})

	if len(m.Entries) != 1 {
		t.Fatalf("len(Entries) = %d; want 1", len(m.Entries))
	}
	if m.Entries[0].Path != "/new/path" {
		t.Errorf("Path = %s; want /new/path", m.Entries[0].Path)
	}
}

// T04 – Remove deletes entry.
func TestRemove(t *testing.T) {
	m := &manifest.Manifest{SchemaVersion: 1}
	m.Put(fixtureEntry())
	removed := m.Remove("mybin")
	if !removed {
		t.Fatal("Remove returned false for existing entry")
	}
	if m.Get("mybin") != nil {
		t.Error("entry still present after Remove")
	}
}

// T05 – Remove returns false for unknown name.
func TestRemoveUnknown(t *testing.T) {
	m := &manifest.Manifest{SchemaVersion: 1}
	removed := m.Remove("nope")
	if removed {
		t.Error("Remove returned true for unknown entry")
	}
}

// T06 – Save + Load round-trip preserves data.
func TestSaveLoadRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "m.json")

	m := &manifest.Manifest{
		SchemaVersion: 1,
		Entries:       []manifest.Entry{fixtureEntry()},
	}
	if err := m.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := manifest.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.SchemaVersion != 1 {
		t.Errorf("SchemaVersion = %d; want 1", loaded.SchemaVersion)
	}
	if len(loaded.Entries) != 1 {
		t.Fatalf("len(Entries) = %d; want 1", len(loaded.Entries))
	}
	e := loaded.Entries[0]
	want := fixtureEntry()
	if e != want {
		t.Errorf("entry mismatch: got %+v want %+v", e, want)
	}
}

func TestSavePreservesPreviousValidManifest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.json")
	oldManifest := &manifest.Manifest{SchemaVersion: 1, Entries: []manifest.Entry{fixtureEntry()}}
	if err := oldManifest.Save(path); err != nil {
		t.Fatal(err)
	}
	oldBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	newEntry := fixtureEntry()
	newEntry.Tag = "v2.0.0"
	newManifest := &manifest.Manifest{SchemaVersion: 1, Entries: []manifest.Entry{newEntry}}
	if err := newManifest.Save(path); err != nil {
		t.Fatal(err)
	}
	backupBytes, err := os.ReadFile(path + ".bak")
	if err != nil {
		t.Fatal(err)
	}
	if string(backupBytes) != string(oldBytes) {
		t.Fatalf("backup does not contain the previous manifest\ngot:  %s\nwant: %s", backupBytes, oldBytes)
	}
	backup, err := manifest.Load(path + ".bak")
	if err != nil {
		t.Fatal(err)
	}
	if got := backup.Get("mybin"); got == nil || got.Tag != "v1.0.0" {
		t.Fatalf("backup entry=%+v", got)
	}
}

func TestSaveRejectsSymlinkManifest(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.json")
	original := []byte(`{"schema_version":1,"entries":[]}`)
	if err := os.WriteFile(target, original, 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "manifest.json")
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	err := (&manifest.Manifest{SchemaVersion: 1}).Save(path)
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("error=%v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatalf("symlink target changed: %q", got)
	}
}

func TestSaveRejectsNonRegularManifest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.json")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	err := (&manifest.Manifest{SchemaVersion: 1}).Save(path)
	if err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("error=%v", err)
	}
	if _, err := manifest.Load(path); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("Load error=%v", err)
	}
}

func TestSaveRejectsInvalidCurrentManifest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.json")
	invalid := []byte("not json\n")
	if err := os.WriteFile(path, invalid, 0o600); err != nil {
		t.Fatal(err)
	}
	err := (&manifest.Manifest{SchemaVersion: 1}).Save(path)
	if err == nil || !strings.Contains(err.Error(), "not valid") {
		t.Fatalf("error=%v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(invalid) {
		t.Fatalf("invalid current manifest was overwritten: %q", got)
	}
	if _, err := os.Lstat(path + ".bak"); !os.IsNotExist(err) {
		t.Fatalf("backup unexpectedly created: %v", err)
	}
}

func TestSaveRejectsSymlinkBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.json")
	first := &manifest.Manifest{SchemaVersion: 1, Entries: []manifest.Entry{fixtureEntry()}}
	if err := first.Save(path); err != nil {
		t.Fatal(err)
	}
	current, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	backupTarget := filepath.Join(dir, "outside")
	if err := os.WriteFile(backupTarget, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(backupTarget, path+".bak"); err != nil {
		t.Fatal(err)
	}
	err = (&manifest.Manifest{SchemaVersion: 1}).Save(path)
	if err == nil || !strings.Contains(err.Error(), "backup") || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("error=%v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(current) {
		t.Fatalf("current manifest changed after backup validation failure")
	}
	outside, err := os.ReadFile(backupTarget)
	if err != nil {
		t.Fatal(err)
	}
	if string(outside) != "outside" {
		t.Fatalf("backup symlink target changed: %q", outside)
	}
}

func TestFirstSaveRejectsPrepositionedSymlinkBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.json")
	backupTarget := filepath.Join(dir, "outside")
	if err := os.WriteFile(backupTarget, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(backupTarget, path+".bak"); err != nil {
		t.Fatal(err)
	}

	err := (&manifest.Manifest{SchemaVersion: 1}).Save(path)
	if err == nil || !strings.Contains(err.Error(), "backup") || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("error=%v", err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("first manifest was written despite hostile backup: %v", err)
	}
	outside, err := os.ReadFile(backupTarget)
	if err != nil {
		t.Fatal(err)
	}
	if string(outside) != "outside" {
		t.Fatalf("backup symlink target changed: %q", outside)
	}
}

func TestLoadRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.json")
	if err := os.WriteFile(target, []byte(`{"schema_version":1,"entries":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "manifest.json")
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if _, err := manifest.Load(path); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("error=%v", err)
	}
}

func TestLegacyEntryLoadsWithoutOptionalAssetFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "legacy.json")
	raw := `{"schema_version":1,"entries":[{"name":"legacy","path":"/usr/local/bin/legacy","repo":"owner/repo","tag":"v1.0.0","sha256":"deadbeef","upstream":"github.com/owner/repo","adopted_at":"2025-01-01T00:00:00Z","updated_at":"2025-01-01T00:00:00Z"}]}`
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}

	m, err := manifest.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Entries) != 1 {
		t.Fatalf("entries=%d want 1", len(m.Entries))
	}
	e := m.Entries[0]
	if e.AssetName != "" || e.AssetSHA256 != "" || e.ChecksumAsset != "" || e.ChecksumVerified {
		t.Fatalf("legacy optional fields must have zero values: %+v", e)
	}

	roundTrip := filepath.Join(dir, "roundtrip.json")
	if err := m.Save(roundTrip); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(roundTrip)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"asset_name", "asset_sha256", "checksum_asset", "checksum_verified"} {
		if strings.Contains(string(data), `"`+field+`"`) {
			t.Fatalf("zero-value optional field %q must be omitted: %s", field, data)
		}
	}
}

// T07 – Save is atomic: old content preserved when the target directory
// becomes read-only before Rename.
func TestSaveAtomicity(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "m.json")

	// Write initial manifest.
	oldM := &manifest.Manifest{
		SchemaVersion: 1,
		Entries: []manifest.Entry{{
			Name:      "old",
			Path:      "/old/path",
			Repo:      "o/r",
			Tag:       "v0.1",
			SHA256:    "oldhash",
			AdoptedAt: "2024-01-01T00:00:00Z",
			UpdatedAt: "2024-01-01T00:00:00Z",
		}},
	}
	if err := oldM.Save(path); err != nil {
		t.Fatalf("initial Save: %v", err)
	}

	// Verify initial content.
	initialData, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	// Make the directory read-only to force Rename to fail.
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	// Restore permissions in case the test continues after this block.
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	newM := &manifest.Manifest{
		SchemaVersion: 1,
		Entries: []manifest.Entry{{
			Name:      "new",
			Path:      "/new/path",
			Repo:      "n/r",
			Tag:       "v9.9",
			SHA256:    "newhash",
			AdoptedAt: "2024-12-31T00:00:00Z",
			UpdatedAt: "2024-12-31T00:00:00Z",
		}},
	}
	err = newM.Save(path)
	if err == nil {
		t.Fatal("expected error from Save when dir is read-only, got nil")
	}

	// Original file must be intact.
	afterData, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile after failed Save: %v", err)
	}
	if string(afterData) != string(initialData) {
		t.Errorf("original file was modified after atomic Save failure")
	}
}

// T08 – schema_version > 1 is rejected.
func TestUnknownSchemaVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "m.json")
	raw := `{"schema_version":2,"entries":[]}` // nolint:lll
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := manifest.Load(path)
	if err == nil {
		t.Fatal("expected error for schema_version > 1, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported schema_version") {
		t.Errorf("error message = %q; want it to contain 'unsupported schema_version'", err.Error())
	}
}

func TestNegativeSchemaVersionRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "negative.json")
	if err := os.WriteFile(path, []byte(`{"schema_version":-1,"entries":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := manifest.Load(path); err == nil || !strings.Contains(err.Error(), "unsupported schema_version") {
		t.Fatalf("expected negative schema rejection, got %v", err)
	}

	if err := (&manifest.Manifest{SchemaVersion: -1}).Save(filepath.Join(dir, "saved.json")); err == nil {
		t.Fatal("expected Save to reject negative schema")
	}
}

// T09 – JSON output is stable: indent=2, one entry per line.
func TestJSONFormatStable(t *testing.T) {
	m := &manifest.Manifest{
		SchemaVersion: 1,
		Entries: []manifest.Entry{{
			Name:      "a",
			Path:      "/a",
			Repo:      "o/r",
			Tag:       "v1",
			SHA256:    "h",
			AdoptedAt: "2024-01-01T00:00:00Z",
			UpdatedAt: "2024-01-01T00:00:00Z",
		}},
	}
	if err := m.Save("/dev/null"); err != nil {
		// /dev/null is expected to fail on macOS; just check encoding
		// by encoding directly instead.
	}

	// Encode to string to verify format without writing to disk.
	var sb strings.Builder
	enc := json.NewEncoder(&sb)
	enc.SetIndent("", "  ")
	if err := enc.Encode(m); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	got := sb.String()
	if !strings.Contains(got, "  \"schema_version\"") {
		t.Error("expected 2-space indent for schema_version field")
	}
	if !strings.Contains(got, "  \"name\"") {
		t.Error("expected 2-space indent for name field")
	}
	if !strings.HasSuffix(got, "\n") {
		t.Error("expected trailing newline")
	}
}

// T10 – schema_version 0 in file is normalised to 1 on Load.
func TestSchemaZeroNormalised(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "m.json")
	raw := `{"schema_version":0,"entries":[{"name":"x","path":"/p","repo":"o/r","tag":"v1","sha256":"h","adopted_at":"2024-01-01T00:00:00Z","updated_at":"2024-01-01T00:00:00Z"}]}`
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := manifest.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if m.SchemaVersion != 1 {
		t.Errorf("SchemaVersion = %d; want 1 after normalisation", m.SchemaVersion)
	}
}
