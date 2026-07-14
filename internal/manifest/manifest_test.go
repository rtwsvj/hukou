package manifest_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
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
		SHA256:           strings.Repeat("d", 64),
		Upstream:         "github.com/owner/repo",
		AdoptedAt:        "2025-01-01T00:00:00Z",
		UpdatedAt:        "2025-01-01T00:00:00Z",
		AssetName:        "mybin-darwin-arm64.tar.gz",
		AssetSHA256:      strings.Repeat("a", 64),
		ChecksumAsset:    "checksums.txt",
		ChecksumVerified: true,
	}
}

func fixtureEntryWithUpgrade() manifest.Entry {
	entry := manifest.PrepareEntry(fixtureEntry())
	active := manifest.ActivationEvent{
		ID:          "upgrade-v2",
		ParentID:    entry.ActiveActivationID,
		Operation:   "upgrade",
		Tag:         "v2.0.0",
		SHA256:      strings.Repeat("e", 64),
		ActivatedAt: "2026-07-14T00:00:00Z",
	}
	entry.Tag = active.Tag
	entry.SHA256 = active.SHA256
	entry.UpdatedAt = active.ActivatedAt
	entry.ActiveActivationID = active.ID
	entry.Activations = append(entry.Activations, active)
	return entry
}

func requireValidateAndDecodeRejected(t *testing.T, candidate *manifest.Manifest) {
	t.Helper()
	if err := candidate.Validate(); err == nil {
		t.Fatal("Validate accepted an unsafe manifest")
	}
	raw, err := json.Marshal(candidate)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manifest.Decode(raw); err == nil {
		t.Fatal("Decode accepted an unsafe manifest")
	}
}

func requireValidateAndDecodeAccepted(t *testing.T, candidate *manifest.Manifest) {
	t.Helper()
	if err := candidate.Validate(); err != nil {
		t.Fatalf("Validate rejected a safe manifest: %v", err)
	}
	raw, err := json.Marshal(candidate)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manifest.Decode(raw); err != nil {
		t.Fatalf("Decode rejected a safe manifest: %v", err)
	}
}

// T01 – missing file returns empty manifest, no error.
func TestLoadMissingFile(t *testing.T) {
	dir := t.TempDir()
	m, err := manifest.Load(filepath.Join(dir, "nonexistent.json"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.SchemaVersion != manifest.CurrentSchemaVersion {
		t.Errorf("SchemaVersion = %d; want %d", m.SchemaVersion, manifest.CurrentSchemaVersion)
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
	if loaded.SchemaVersion != manifest.CurrentSchemaVersion {
		t.Errorf("SchemaVersion = %d; want %d", loaded.SchemaVersion, manifest.CurrentSchemaVersion)
	}
	if len(loaded.Entries) != 1 {
		t.Fatalf("len(Entries) = %d; want 1", len(loaded.Entries))
	}
	e := loaded.Entries[0]
	want := manifest.PrepareEntry(fixtureEntry())
	want.UpdatePolicy = manifest.UpdatePolicy{Mode: manifest.UpdateModeLegacy, Channel: manifest.UpdateChannelStable}
	if !reflect.DeepEqual(e, want) {
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
	raw := `{"schema_version":1,"entries":[{"name":"legacy","path":"/usr/local/bin/legacy","repo":"owner/repo","tag":"v1.0.0","sha256":"` + strings.Repeat("d", 64) + `","upstream":"github.com/owner/repo","adopted_at":"2025-01-01T00:00:00Z","updated_at":"2025-01-01T00:00:00Z"}]}`
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
			SHA256:    strings.Repeat("a", 64),
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
			SHA256:    strings.Repeat("b", 64),
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

// T08 – schema_version above the current version is rejected.
func TestUnknownSchemaVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "m.json")
	raw := `{"schema_version":3,"entries":[]}` // nolint:lll
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := manifest.Load(path)
	if err == nil {
		t.Fatal("expected error for future schema_version, got nil")
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

// T10 – schema_version 0 in file is deterministically migrated to v2 on Load.
func TestSchemaZeroNormalised(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "m.json")
	raw := `{"schema_version":0,"entries":[{"name":"x","path":"/p","repo":"o/r","tag":"v1","sha256":"` + strings.Repeat("c", 64) + `","adopted_at":"2024-01-01T00:00:00Z","updated_at":"2024-01-01T00:00:00Z"}]}`
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := manifest.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if m.SchemaVersion != manifest.CurrentSchemaVersion {
		t.Errorf("SchemaVersion = %d; want %d after migration", m.SchemaVersion, manifest.CurrentSchemaVersion)
	}
}

func TestDecodeRejectsNullSchemaVersionButPreservesImplicitZero(t *testing.T) {
	if _, err := manifest.Decode([]byte(`{"schema_version":null,"entries":[]}`)); err == nil || !strings.Contains(err.Error(), "must be an integer, not null") {
		t.Fatalf("null schema_version error = %v", err)
	}
	m, err := manifest.Decode([]byte(`{"entries":[]}`))
	if err != nil {
		t.Fatalf("implicit schema zero was rejected: %v", err)
	}
	if m.SchemaVersion != manifest.CurrentSchemaVersion || m.Retention != manifest.DefaultRetentionPolicy() || m.Entries == nil {
		t.Fatalf("implicit schema zero migration = %+v", m)
	}
}

func TestV1MigrationIsDeterministicAndRoundTrips(t *testing.T) {
	dir := t.TempDir()
	raw := `{"schema_version":1,"entries":[{"name":"tool","path":"/usr/local/bin/tool","repo":"owner/repo","tag":"v1.2.3","sha256":"` + strings.Repeat("a", 64) + `","upstream":"github.com/owner/repo","adopted_at":"2026-01-01T00:00:00Z","updated_at":"2026-02-01T00:00:00Z"}]}`
	firstPath := filepath.Join(dir, "first.json")
	secondPath := filepath.Join(dir, "second.json")
	if err := os.WriteFile(firstPath, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secondPath, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	first, err := manifest.Load(firstPath)
	if err != nil {
		t.Fatal(err)
	}
	second, err := manifest.Load(secondPath)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("migration is not deterministic\nfirst=%+v\nsecond=%+v", first, second)
	}
	if first.SchemaVersion != manifest.CurrentSchemaVersion || first.Retention.RollbackDepth != manifest.DefaultRollbackDepth {
		t.Fatalf("manifest defaults=%+v", first)
	}
	entry := first.Entries[0]
	if entry.UpdatePolicy.Mode != manifest.UpdateModeLegacy || entry.UpdatePolicy.Channel != manifest.UpdateChannelStable {
		t.Fatalf("legacy policy=%+v", entry.UpdatePolicy)
	}
	if len(entry.Activations) != 1 || entry.ActiveActivationID != entry.Activations[0].ID {
		t.Fatalf("legacy activation=%+v", entry)
	}
	event := entry.Activations[0]
	if !strings.HasPrefix(event.ID, "legacy-") || event.Operation != "legacy" ||
		event.Tag != entry.Tag || event.SHA256 != entry.SHA256 || event.ActivatedAt != entry.UpdatedAt {
		t.Fatalf("legacy event=%+v", event)
	}

	roundTrip := filepath.Join(dir, "roundtrip-v2.json")
	if err := first.Save(roundTrip); err != nil {
		t.Fatal(err)
	}
	loaded, err := manifest.Load(roundTrip)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, loaded) {
		t.Fatalf("v2 round trip changed migration\nfirst=%+v\nloaded=%+v", first, loaded)
	}
}

func TestDecodeRequiresExplicitSchemaV2TopLevelState(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "missing retention and entries", raw: `{"schema_version":2}`, want: `top-level field "retention"`},
		{name: "missing retention", raw: `{"schema_version":2,"entries":[]}`, want: `top-level field "retention"`},
		{name: "missing entries", raw: `{"schema_version":2,"retention":{"rollback_depth":0}}`, want: `top-level field "entries"`},
		{name: "null retention", raw: `{"schema_version":2,"retention":null,"entries":[]}`, want: `field "retention" must be an object`},
		{name: "retention missing depth", raw: `{"schema_version":2,"retention":{},"entries":[]}`, want: `missing required field "rollback_depth"`},
		{name: "null entries", raw: `{"schema_version":2,"retention":{"rollback_depth":0},"entries":null}`, want: `field "entries" must be an array`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := manifest.Decode([]byte(test.raw)); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Decode error = %v; want it to contain %q", err, test.want)
			}
		})
	}

	m, err := manifest.Decode([]byte(`{"schema_version":2,"retention":{"rollback_depth":0},"entries":[]}`))
	if err != nil {
		t.Fatalf("explicit legal zero retention was rejected: %v", err)
	}
	if m.Retention.RollbackDepth != 0 || m.Entries == nil || len(m.Entries) != 0 {
		t.Fatalf("explicit zero/empty state changed during decode: %+v", m)
	}
}

func TestDecodeRejectsV2OnlyStateInLegacySchemas(t *testing.T) {
	mutations := map[string]func(map[string]any, map[string]any){
		"top-level retention": func(document, _ map[string]any) {
			document["retention"] = map[string]any{"rollback_depth": 2}
		},
		"active activation id": func(_, entry map[string]any) {
			entry["active_activation_id"] = "legacy-smuggled"
		},
		"activations": func(_, entry map[string]any) {
			entry["activations"] = []any{}
		},
		"update policy": func(_, entry map[string]any) {
			entry["update_policy"] = map[string]any{"mode": "semver", "channel": "stable"}
		},
		"entry retention": func(_, entry map[string]any) {
			entry["retention"] = map[string]any{"rollback_depth": 0}
		},
	}
	for _, schemaVersion := range []int{0, 1} {
		for name, mutate := range mutations {
			t.Run(fmt.Sprintf("schema_%d/%s", schemaVersion, name), func(t *testing.T) {
				entry := map[string]any{
					"name":       "legacy",
					"path":       "/usr/local/bin/legacy",
					"repo":       "owner/repo",
					"tag":        "v1.0.0",
					"sha256":     strings.Repeat("a", 64),
					"upstream":   "github.com/owner/repo",
					"adopted_at": "2026-01-01T00:00:00Z",
					"updated_at": "2026-01-01T00:00:00Z",
				}
				document := map[string]any{
					"schema_version": schemaVersion,
					"entries":        []any{entry},
				}
				baseline, err := json.Marshal(document)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := manifest.Decode(baseline); err != nil {
					t.Fatalf("valid legacy baseline was rejected: %v", err)
				}
				mutate(document, entry)
				raw, err := json.Marshal(document)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := manifest.Decode(raw); err == nil {
					t.Fatal("legacy schema accepted v2-only state")
				}
			})
		}
	}
}

func TestDecodeRejectsMissingSchemaV2EntryPolicyAndHistoryFields(t *testing.T) {
	entry := manifest.PrepareEntry(fixtureEntry())
	base := map[string]any{
		"schema_version": manifest.CurrentSchemaVersion,
		"retention":      map[string]any{"rollback_depth": 0},
		"entries":        []any{},
	}
	entryRaw, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	var entryDocument map[string]any
	if err := json.Unmarshal(entryRaw, &entryDocument); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"active_activation_id", "activations", "update_policy"} {
		t.Run(field, func(t *testing.T) {
			copyRaw, err := json.Marshal(entryDocument)
			if err != nil {
				t.Fatal(err)
			}
			var candidate map[string]any
			if err := json.Unmarshal(copyRaw, &candidate); err != nil {
				t.Fatal(err)
			}
			delete(candidate, field)
			base["entries"] = []any{candidate}
			raw, err := json.Marshal(base)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := manifest.Decode(raw); err == nil || !strings.Contains(err.Error(), field) {
				t.Fatalf("missing %s error = %v", field, err)
			}
		})
	}
}

func TestPrepareEntryAddsDeterministicCompatibilityTransition(t *testing.T) {
	entry := manifest.PrepareEntry(fixtureEntry())
	oldActive := entry.ActiveActivationID
	entry.Tag = "v2.0.0"
	entry.SHA256 = strings.Repeat("e", 64)
	entry.UpdatedAt = "2026-07-14T00:00:00Z"
	first := manifest.PrepareEntry(entry)
	second := manifest.PrepareEntry(entry)
	if !reflect.DeepEqual(first, second) {
		t.Fatal("compatibility transition is not deterministic")
	}
	if len(first.Activations) != 2 || first.ActiveActivationID == oldActive {
		t.Fatalf("transition=%+v", first)
	}
	transition := first.Activations[1]
	if transition.ParentID != oldActive || transition.Tag != first.Tag || transition.SHA256 != first.SHA256 {
		t.Fatalf("transition=%+v", transition)
	}
	if again := manifest.PrepareEntry(first); len(again.Activations) != 2 {
		t.Fatalf("normalization is not idempotent: %+v", again.Activations)
	}
}

func TestCloneDoesNotAliasActivationOrRetentionState(t *testing.T) {
	entry := manifest.PrepareEntry(fixtureEntry())
	entry.Retention = &manifest.RetentionPolicy{RollbackDepth: 7}
	original := &manifest.Manifest{
		SchemaVersion: manifest.CurrentSchemaVersion,
		Retention:     manifest.DefaultRetentionPolicy(),
		Entries:       []manifest.Entry{entry},
	}
	clone := original.Clone()
	clone.Entries[0].Activations[0].Tag = "changed"
	clone.Entries[0].Retention.RollbackDepth = 1
	if original.Entries[0].Activations[0].Tag == "changed" {
		t.Fatal("Clone aliased activation slice")
	}
	if original.Entries[0].Retention.RollbackDepth != 7 {
		t.Fatal("Clone aliased retention pointer")
	}
}

func TestValidateAndDecodeRejectUnsafeHistoricalActivationTags(t *testing.T) {
	unsafeTags := []string{"", ".", "..", "v1..0", "../escape", "release/v1", `release\v1`, "Original"}
	for _, position := range []string{"inactive", "active"} {
		for _, tag := range unsafeTags {
			t.Run(position+"/"+fmt.Sprintf("%q", tag), func(t *testing.T) {
				entry := fixtureEntryWithUpgrade()
				if position == "inactive" {
					entry.Activations[0].Tag = tag
				} else {
					last := len(entry.Activations) - 1
					entry.Activations[last].Tag = tag
					entry.Tag = tag
				}
				candidate := &manifest.Manifest{
					SchemaVersion: manifest.CurrentSchemaVersion,
					Retention:     manifest.DefaultRetentionPolicy(),
					Entries:       []manifest.Entry{entry},
				}
				requireValidateAndDecodeRejected(t, candidate)
			})
		}
	}
}

func TestValidateAndDecodeAcceptStoreSafeActivationTags(t *testing.T) {
	for _, tag := range []string{"local", "v1.2.3-rc.1+build.7", "legacy-build_42", "original"} {
		t.Run(tag, func(t *testing.T) {
			entry := manifest.PrepareEntry(fixtureEntry())
			entry.Tag = tag
			entry.Activations[0].Tag = tag
			if tag == "original" {
				entry.Activations[0].Operation = "rollback"
			}
			candidate := &manifest.Manifest{
				SchemaVersion: manifest.CurrentSchemaVersion,
				Retention:     manifest.DefaultRetentionPolicy(),
				Entries:       []manifest.Entry{entry},
			}
			requireValidateAndDecodeAccepted(t, candidate)
		})
	}
}

func TestValidateAndDecodeRejectActivationTagDigestRebinding(t *testing.T) {
	for _, tag := range []string{"v1.0.0", "original"} {
		t.Run(tag, func(t *testing.T) {
			entry := fixtureEntryWithUpgrade()
			entry.Activations[0].Tag = tag
			entry.Activations[1].Tag = tag
			entry.Tag = tag
			if tag == "original" {
				entry.Activations[0].Operation = "rollback"
				entry.Activations[1].Operation = "rollback"
			}
			candidate := &manifest.Manifest{
				SchemaVersion: manifest.CurrentSchemaVersion,
				Retention:     manifest.DefaultRetentionPolicy(),
				Entries:       []manifest.Entry{entry},
			}
			requireValidateAndDecodeRejected(t, candidate)
		})
	}
}

func TestValidateAndDecodeAllowRepeatedRollbackWithSameTagAndDigest(t *testing.T) {
	for _, tag := range []string{"v1.0.0", "original"} {
		t.Run(tag, func(t *testing.T) {
			entry := manifest.PrepareEntry(fixtureEntry())
			sha256 := entry.SHA256
			root := entry.Activations[0]
			root.Tag = tag
			if tag == "original" {
				root.Operation = "rollback"
			}
			first := manifest.ActivationEvent{
				ID:          "rollback-1",
				Operation:   "rollback",
				Tag:         tag,
				SHA256:      strings.ToUpper(sha256),
				ActivatedAt: "2026-07-14T01:00:00Z",
				RevertsID:   root.ID,
			}
			second := manifest.ActivationEvent{
				ID:          "rollback-2",
				Operation:   "rollback",
				Tag:         tag,
				SHA256:      sha256,
				ActivatedAt: "2026-07-14T02:00:00Z",
				RevertsID:   first.ID,
			}
			entry.Tag = tag
			entry.SHA256 = sha256
			entry.UpdatedAt = second.ActivatedAt
			entry.ActiveActivationID = second.ID
			entry.Activations = []manifest.ActivationEvent{root, first, second}
			candidate := &manifest.Manifest{
				SchemaVersion: manifest.CurrentSchemaVersion,
				Retention:     manifest.DefaultRetentionPolicy(),
				Entries:       []manifest.Entry{entry},
			}
			requireValidateAndDecodeAccepted(t, candidate)
		})
	}
}

func TestLoadRejectsSchemaV2WithoutValidExplicitLineage(t *testing.T) {
	base := manifest.PrepareEntry(fixtureEntry())
	tests := map[string]func(*manifest.Entry){
		"missing lineage": func(entry *manifest.Entry) {
			entry.ActiveActivationID = ""
			entry.Activations = nil
		},
		"missing parent": func(entry *manifest.Entry) {
			entry.Activations[0].ParentID = "missing"
		},
		"active mismatch": func(entry *manifest.Entry) {
			entry.Tag = "v9.0.0"
		},
	}
	for name, corrupt := range tests {
		t.Run(name, func(t *testing.T) {
			entry := manifest.PrepareEntry(base)
			corrupt(&entry)
			candidate := manifest.Manifest{
				SchemaVersion: manifest.CurrentSchemaVersion,
				Retention:     manifest.DefaultRetentionPolicy(),
				Entries:       []manifest.Entry{entry},
			}
			raw, err := json.Marshal(candidate)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "manifest.json")
			if err := os.WriteFile(path, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := manifest.Load(path); err == nil {
				t.Fatal("Load accepted an invalid schema v2 lineage")
			}
		})
	}
}

func TestEffectiveRetentionUsesEntryOverrideIncludingZero(t *testing.T) {
	m := &manifest.Manifest{Retention: manifest.RetentionPolicy{RollbackDepth: 3}}
	entry := manifest.Entry{}
	if got := m.EffectiveRetention(&entry).RollbackDepth; got != 3 {
		t.Fatalf("inherited depth=%d", got)
	}
	entry.Retention = &manifest.RetentionPolicy{RollbackDepth: 0}
	if got := m.EffectiveRetention(&entry).RollbackDepth; got != 0 {
		t.Fatalf("explicit zero depth=%d", got)
	}
}
