package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/rtwsvj/hukou/internal/activation"
	"github.com/rtwsvj/hukou/internal/manifest"
	"github.com/rtwsvj/hukou/internal/store"
)

func TestReceiptCommandRegistered(t *testing.T) {
	command, _, err := rootCmd.Find([]string{"doctor", "receipt"})
	if err != nil {
		t.Fatal(err)
	}
	if command != receiptCmd {
		t.Fatalf("receipt command not registered under doctor: %v", command)
	}
	_, _, err = rootCmd.Find([]string{"receipt"})
	if err == nil {
		t.Fatal("top-level receipt command must be removed; it now lives under doctor")
	}
	if receiptCmd.Flags().Lookup("json") == nil {
		t.Fatal("missing --json flag")
	}
	if receiptCmd.Flags().Lookup("no-fail-on-drift") == nil {
		t.Fatal("missing --no-fail-on-drift flag")
	}
}

func TestDoReceiptConsistentJSONSchema(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("HUKOU_DATA_DIR", dataDir)

	live := writeExecutable(t, t.TempDir(), "tool", "body-v1\n")
	sha, err := store.SHA256File(live)
	if err != nil {
		t.Fatal(err)
	}
	saveReceiptFixture(t, dataDir, receiptFixture{
		name:             "tool",
		path:             live,
		repo:             "owner/repo",
		tag:              "v1.0.0",
		sha:              sha,
		checksumVerified: true,
		assetName:        "tool.tar.gz",
		assetSHA:         strings.Repeat("a", 64),
		checksumAsset:    "checksums.txt",
		storeVersions:    []string{"v0.9.0"},
		withOriginal:     true,
	})

	var stdout bytes.Buffer
	if err := doReceipt(&stdout, nil, true, false); err != nil {
		t.Fatalf("doReceipt: %v\n%s", err, stdout.String())
	}

	// Schema lock: decode into generic maps ONLY (not production structs) and
	// assert exact key sets from literal golden lists. Renaming a json tag on
	// the production type or adding an unknown key must fail this test.
	assertReceiptJSONSchemaLocked(t, stdout.Bytes(), sha)

	// Semantic value checks still use production types (orthogonal to key lock).
	var report ReceiptReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stdout.String())
	}
	if report.SchemaVersion != receiptReportSchemaVersion {
		t.Fatalf("schema_version = %d, want %d", report.SchemaVersion, receiptReportSchemaVersion)
	}
	if len(report.Receipts) != 1 {
		t.Fatalf("receipts = %d, want 1", len(report.Receipts))
	}
	r := report.Receipts[0]
	if r.Name != "tool" {
		t.Fatalf("name = %q", r.Name)
	}
	if r.Source.Type != "github" || r.Source.Repo != "owner/repo" || r.Source.URL != "https://github.com/owner/repo" {
		t.Fatalf("source = %+v", r.Source)
	}
	if r.AdoptedVersion != "v1.0.0" {
		t.Fatalf("adopted_version = %q", r.AdoptedVersion)
	}
	if !r.CurrentObserved.Present || !r.CurrentObserved.MatchesManifest || r.CurrentObserved.SHA256 != sha {
		t.Fatalf("current_observed = %+v", r.CurrentObserved)
	}
	if r.CurrentObserved.ManifestSHA256 != sha {
		t.Fatalf("manifest_sha256 = %q, want %q", r.CurrentObserved.ManifestSHA256, sha)
	}
	if r.ChecksumStatus != checksumStatusVerified {
		t.Fatalf("checksum_status = %q, want %q", r.ChecksumStatus, checksumStatusVerified)
	}
	if r.LastVerifiedAt == "" {
		t.Fatal("last_verified_at must be set when verified")
	}
	if r.Drift {
		t.Fatal("consistent receipt must have drift=false")
	}
	if len(r.Errors) != 0 {
		t.Fatalf("consistent receipt must have empty errors, got %v", r.Errors)
	}
	if len(r.RollbackTargets) < 1 {
		t.Fatalf("expected rollback targets, got %+v", r.RollbackTargets)
	}
}

// Literal golden key sets for the DependencyReceipt JSON surface. These are
// deliberately independent of production struct field names / json tags: a
// rename or unexpected additive key fails assertExactJSONKeys.
var (
	goldenReceiptEnvelopeKeys = []string{"schema_version", "receipts"}
	// Consistent verified+present github receipt without note/errors omitempty.
	goldenReceiptObjectKeys = []string{
		"name",
		"source",
		"adopted_version",
		"current_observed",
		"checksum_status",
		"last_verified_at",
		"drift",
		"rollback_targets",
	}
	goldenReceiptSourceKeysGithub = []string{"type", "repo", "url"}
	// Present live file: sha256 is populated (not omitted).
	goldenCurrentObservedKeysPresent = []string{
		"path",
		"sha256",
		"manifest_sha256",
		"matches_manifest",
		"present",
	}
	goldenRollbackTargetKeys = []string{"tag", "sha256", "kind"}
)

// assertReceiptJSONSchemaLocked walks the --json output via generic maps and
// asserts exact key sets + snake_case naming. It must not use production
// report types for key discovery.
func assertReceiptJSONSchemaLocked(t *testing.T, raw []byte, wantLiveSHA string) {
	t.Helper()

	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("envelope decode: %v\n%s", err, raw)
	}
	assertExactJSONKeys(t, "envelope", envelope, goldenReceiptEnvelopeKeys)
	assertSnakeCaseKeys(t, "envelope", envelope)

	var schemaVersion float64
	if err := json.Unmarshal(envelope["schema_version"], &schemaVersion); err != nil {
		t.Fatalf("schema_version: %v", err)
	}
	if int(schemaVersion) != 1 {
		t.Fatalf("schema_version = %v, want 1 (literal golden)", schemaVersion)
	}

	var receipts []map[string]json.RawMessage
	if err := json.Unmarshal(envelope["receipts"], &receipts); err != nil {
		t.Fatalf("receipts decode: %v", err)
	}
	if len(receipts) != 1 {
		t.Fatalf("receipts len = %d, want 1", len(receipts))
	}
	r := receipts[0]
	assertExactJSONKeys(t, "receipt[0]", r, goldenReceiptObjectKeys)
	assertSnakeCaseKeys(t, "receipt[0]", r)

	// Value locks that do not depend on production structs.
	assertJSONString(t, r, "name", "tool")
	assertJSONString(t, r, "adopted_version", "v1.0.0")
	assertJSONString(t, r, "checksum_status", "verified")
	assertJSONString(t, r, "last_verified_at", "2026-08-05T01:00:00Z")
	var drift bool
	if err := json.Unmarshal(r["drift"], &drift); err != nil || drift {
		t.Fatalf("drift = %v err=%v, want false", drift, err)
	}

	var source map[string]json.RawMessage
	if err := json.Unmarshal(r["source"], &source); err != nil {
		t.Fatalf("source: %v", err)
	}
	assertExactJSONKeys(t, "source", source, goldenReceiptSourceKeysGithub)
	assertSnakeCaseKeys(t, "source", source)
	assertJSONString(t, source, "type", "github")
	assertJSONString(t, source, "repo", "owner/repo")
	assertJSONString(t, source, "url", "https://github.com/owner/repo")

	var observed map[string]json.RawMessage
	if err := json.Unmarshal(r["current_observed"], &observed); err != nil {
		t.Fatalf("current_observed: %v", err)
	}
	assertExactJSONKeys(t, "current_observed", observed, goldenCurrentObservedKeysPresent)
	assertSnakeCaseKeys(t, "current_observed", observed)
	assertJSONString(t, observed, "sha256", wantLiveSHA)
	assertJSONString(t, observed, "manifest_sha256", wantLiveSHA)
	var present, matches bool
	if err := json.Unmarshal(observed["present"], &present); err != nil || !present {
		t.Fatalf("present = %v err=%v", present, err)
	}
	if err := json.Unmarshal(observed["matches_manifest"], &matches); err != nil || !matches {
		t.Fatalf("matches_manifest = %v err=%v", matches, err)
	}

	var targets []map[string]json.RawMessage
	if err := json.Unmarshal(r["rollback_targets"], &targets); err != nil {
		t.Fatalf("rollback_targets: %v", err)
	}
	if len(targets) < 1 {
		t.Fatal("expected at least one rollback target")
	}
	for i, target := range targets {
		assertExactJSONKeys(t, fmt.Sprintf("rollback_targets[%d]", i), target, goldenRollbackTargetKeys)
		assertSnakeCaseKeys(t, fmt.Sprintf("rollback_targets[%d]", i), target)
	}
}

func assertExactJSONKeys(t *testing.T, path string, obj map[string]json.RawMessage, want []string) {
	t.Helper()
	if len(obj) != len(want) {
		got := make([]string, 0, len(obj))
		for k := range obj {
			got = append(got, k)
		}
		sort.Strings(got)
		t.Fatalf("%s key set size = %d %v, want %d %v", path, len(obj), got, len(want), want)
	}
	for _, k := range want {
		if _, ok := obj[k]; !ok {
			got := make([]string, 0, len(obj))
			for gk := range obj {
				got = append(got, gk)
			}
			sort.Strings(got)
			t.Fatalf("%s missing key %q; got %v", path, k, got)
		}
	}
}

func assertSnakeCaseKeys(t *testing.T, path string, obj map[string]json.RawMessage) {
	t.Helper()
	for k := range obj {
		if k == "" || k[0] < 'a' || k[0] > 'z' {
			t.Fatalf("%s key %q is not snake_case (must start with lowercase)", path, k)
		}
		for _, c := range k {
			if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_' {
				continue
			}
			t.Fatalf("%s key %q is not snake_case", path, k)
		}
	}
}

func assertJSONString(t *testing.T, obj map[string]json.RawMessage, key, want string) {
	t.Helper()
	raw, ok := obj[key]
	if !ok {
		t.Fatalf("missing key %q", key)
	}
	var got string
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("%s: %v", key, err)
	}
	if got != want {
		t.Fatalf("%s = %q, want %q", key, got, want)
	}
}

func TestDoReceiptDriftExitsNonZero(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("HUKOU_DATA_DIR", dataDir)

	live := writeExecutable(t, t.TempDir(), "drifted", "original-body\n")
	sha, err := store.SHA256File(live)
	if err != nil {
		t.Fatal(err)
	}
	saveReceiptFixture(t, dataDir, receiptFixture{
		name:         "drifted",
		path:         live,
		tag:          "local",
		sha:          sha,
		withOriginal: true,
	})

	// Mutate live file after adopt so observation no longer matches manifest.
	if err := os.WriteFile(live, []byte("tampered-body\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	newSHA, err := store.SHA256File(live)
	if err != nil {
		t.Fatal(err)
	}
	if newSHA == sha {
		t.Fatal("fixture failed to create distinct live hash")
	}

	var stdout bytes.Buffer
	err = doReceipt(&stdout, []string{"drifted"}, true, false)
	if !errors.Is(err, errReceiptDrift) {
		t.Fatalf("error = %v, want errReceiptDrift; out=%s", err, stdout.String())
	}

	var report ReceiptReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stdout.String())
	}
	if len(report.Receipts) != 1 {
		t.Fatalf("receipts = %d", len(report.Receipts))
	}
	r := report.Receipts[0]
	if !r.Drift {
		t.Fatal("expected drift=true")
	}
	if r.CurrentObserved.MatchesManifest {
		t.Fatal("matches_manifest must be false on drift")
	}
	if r.CurrentObserved.SHA256 != newSHA {
		t.Fatalf("live sha = %q, want %q", r.CurrentObserved.SHA256, newSHA)
	}
	if r.CurrentObserved.ManifestSHA256 != sha {
		t.Fatalf("manifest sha = %q, want %q", r.CurrentObserved.ManifestSHA256, sha)
	}

	// --no-fail-on-drift keeps the report but returns nil.
	stdout.Reset()
	if err := doReceipt(&stdout, []string{"drifted"}, true, true); err != nil {
		t.Fatalf("no-fail-on-drift should succeed: %v\n%s", err, stdout.String())
	}
}

func TestDoReceiptMissingManifestRequestedName(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("HUKOU_DATA_DIR", dataDir)
	// No manifest.json written: Load returns empty in-memory manifest.

	var stdout bytes.Buffer
	err := doReceipt(&stdout, []string{"missing-tool"}, true, false)
	if !errors.Is(err, errReceiptNotFound) {
		t.Fatalf("error = %v, want errReceiptNotFound; out=%s", err, stdout.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("should not emit report when name missing from empty manifest: %s", stdout.String())
	}
}

func TestDoReceiptEmptyManifestTable(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "absent-root")
	t.Setenv("HUKOU_DATA_DIR", dataDir)

	var stdout bytes.Buffer
	if err := doReceipt(&stdout, nil, false, false); err != nil {
		t.Fatalf("empty receipt: %v", err)
	}
	if !strings.Contains(stdout.String(), "No tools have been adopted") {
		t.Fatalf("unexpected output: %s", stdout.String())
	}
	if _, err := os.Lstat(dataDir); !os.IsNotExist(err) {
		t.Fatalf("receipt must not create data root: %v", err)
	}
}

func TestDoReceiptChecksumStatusThreeStates(t *testing.T) {
	cases := []struct {
		name     string
		entry    manifest.Entry
		want     string
		wantLast bool
	}{
		{
			name: "verified",
			entry: manifest.Entry{
				ChecksumVerified: true,
				ChecksumAsset:    "checksums.txt",
				AssetName:        "a.tar.gz",
				AssetSHA256:      strings.Repeat("b", 64),
				UpdatedAt:        "2026-08-05T00:00:00Z",
			},
			want:     checksumStatusVerified,
			wantLast: true,
		},
		{
			name: "unverified_bypass",
			entry: manifest.Entry{
				ChecksumVerified: false,
				AssetName:        "a.tar.gz",
				AssetSHA256:      strings.Repeat("c", 64),
			},
			want: checksumStatusUnverifiedBypass,
		},
		{
			name:  "unknown_local",
			entry: manifest.Entry{ChecksumVerified: false},
			want:  checksumStatusUnknown,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := receiptChecksumStatus(tc.entry)
			if got != tc.want {
				t.Fatalf("checksum_status = %q, want %q", got, tc.want)
			}
			last := receiptLastVerifiedAt(tc.entry)
			if tc.wantLast && last == "" {
				t.Fatal("expected last_verified_at")
			}
			if !tc.wantLast && last != "" {
				t.Fatalf("unexpected last_verified_at %q", last)
			}
		})
	}
}

func TestDoReceiptNameNotFoundAmongAdopted(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("HUKOU_DATA_DIR", dataDir)

	live := writeExecutable(t, t.TempDir(), "present", "p\n")
	sha, err := store.SHA256File(live)
	if err != nil {
		t.Fatal(err)
	}
	saveReceiptFixture(t, dataDir, receiptFixture{
		name:         "present",
		path:         live,
		tag:          "local",
		sha:          sha,
		withOriginal: true,
	})

	var stdout bytes.Buffer
	err = doReceipt(&stdout, []string{"absent"}, false, false)
	if !errors.Is(err, errReceiptNotFound) {
		t.Fatalf("error = %v, want errReceiptNotFound", err)
	}
}

func TestDoReceiptPendingTransactionFailsClosed(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("HUKOU_DATA_DIR", dataDir)
	pending := filepath.Join(dataDir, "transactions", "pending-test")
	if err := os.MkdirAll(pending, 0o700); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	err := doReceipt(&stdout, nil, false, false)
	if err == nil || !strings.Contains(err.Error(), "unfinished transaction") {
		t.Fatalf("expected pending transaction error, got %v", err)
	}
}

func TestDoReceiptStoreCorruptionRecordsErrors(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("HUKOU_DATA_DIR", dataDir)

	live := writeExecutable(t, t.TempDir(), "broken-store", "body\n")
	sha, err := store.SHA256File(live)
	if err != nil {
		t.Fatal(err)
	}
	saveReceiptFixture(t, dataDir, receiptFixture{
		name:          "broken-store",
		path:          live,
		tag:           "v1.0.0",
		sha:           sha,
		withOriginal:  true,
		storeVersions: []string{"v0.9.0"},
	})

	// Corrupt the multi-version store: a non-directory entry makes Versions fail,
	// distinguishing "no rollback targets" from "store unreadable".
	toolStore := filepath.Join(dataDir, "store", "broken-store")
	if err := os.WriteFile(filepath.Join(toolStore, "not-a-version"), []byte("garbage"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	err = doReceipt(&stdout, []string{"broken-store"}, true, false)
	if !errors.Is(err, errReceiptErrors) {
		t.Fatalf("error = %v, want errReceiptErrors; out=%s", err, stdout.String())
	}

	// Generic map decode so we lock the errors key without production types.
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stdout.String())
	}
	var receipts []map[string]json.RawMessage
	if err := json.Unmarshal(envelope["receipts"], &receipts); err != nil {
		t.Fatalf("receipts: %v", err)
	}
	if len(receipts) != 1 {
		t.Fatalf("receipts = %d", len(receipts))
	}
	r := receipts[0]
	rawErrs, ok := r["errors"]
	if !ok {
		t.Fatalf("JSON missing errors field:\n%s", stdout.String())
	}
	var errs []string
	if err := json.Unmarshal(rawErrs, &errs); err != nil {
		t.Fatalf("errors decode: %v", err)
	}
	if len(errs) == 0 {
		t.Fatal("expected non-empty errors")
	}
	joined := strings.Join(errs, "\n")
	if !strings.Contains(joined, "list store versions") {
		t.Fatalf("errors should mention list store versions, got %v", errs)
	}

	// Human table must explicitly mark ERRORS.
	stdout.Reset()
	err = doReceipt(&stdout, []string{"broken-store"}, false, false)
	if !errors.Is(err, errReceiptErrors) {
		t.Fatalf("table mode error = %v, want errReceiptErrors", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "ERRORS") {
		t.Fatalf("table missing ERRORS column:\n%s", out)
	}
	if !strings.Contains(out, "yes:") {
		t.Fatalf("table must mark errors with yes: prefix:\n%s", out)
	}

	// --no-fail-on-drift does not suppress store errors.
	stdout.Reset()
	err = doReceipt(&stdout, []string{"broken-store"}, true, true)
	if !errors.Is(err, errReceiptErrors) {
		t.Fatalf("no-fail-on-drift must still fail on store errors: %v", err)
	}
}

func TestDoReceiptOriginalCorruptionRecordsErrors(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("HUKOU_DATA_DIR", dataDir)

	live := writeExecutable(t, t.TempDir(), "bad-original", "body\n")
	sha, err := store.SHA256File(live)
	if err != nil {
		t.Fatal(err)
	}
	// No original backup and no retained versions: Original must fail for an
	// adopted tool (list treats missing original as hard error too).
	saveReceiptFixture(t, dataDir, receiptFixture{
		name:         "bad-original",
		path:         live,
		tag:          "local",
		sha:          sha,
		withOriginal: false,
	})

	var stdout bytes.Buffer
	err = doReceipt(&stdout, []string{"bad-original"}, true, false)
	if !errors.Is(err, errReceiptErrors) {
		t.Fatalf("error = %v, want errReceiptErrors; out=%s", err, stdout.String())
	}
	if !strings.Contains(stdout.String(), `"errors"`) {
		t.Fatalf("JSON must include errors:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "inspect original backup") {
		t.Fatalf("errors should mention original backup:\n%s", stdout.String())
	}
}

type receiptFixture struct {
	name             string
	path             string
	repo             string
	tag              string
	sha              string
	checksumVerified bool
	assetName        string
	assetSHA         string
	checksumAsset    string
	storeVersions    []string
	withOriginal     bool
}

func saveReceiptFixture(t *testing.T, dataDir string, fx receiptFixture) {
	t.Helper()
	m, err := manifest.Load(filepath.Join(dataDir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	entry := manifest.Entry{
		Name:             fx.name,
		Path:             fx.path,
		Repo:             fx.repo,
		Tag:              fx.tag,
		SHA256:           fx.sha,
		AdoptedAt:        "2026-08-05T00:00:00Z",
		UpdatedAt:        "2026-08-05T01:00:00Z",
		ChecksumVerified: fx.checksumVerified,
		AssetName:        fx.assetName,
		AssetSHA256:      fx.assetSHA,
		ChecksumAsset:    fx.checksumAsset,
	}
	if err := activation.RecordAdopt(&entry, "fixture-"+fx.name, entry.AdoptedAt); err != nil {
		t.Fatal(err)
	}
	// RecordAdopt overwrites UpdatedAt with activatedAt; restore the desired
	// last-verified stamp for verified fixtures after lineage is valid.
	if fx.checksumVerified {
		entry.UpdatedAt = "2026-08-05T01:00:00Z"
		entry.ChecksumVerified = true
		entry.AssetName = fx.assetName
		entry.AssetSHA256 = fx.assetSHA
		entry.ChecksumAsset = fx.checksumAsset
	}
	m.Put(entry)
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := m.Save(filepath.Join(dataDir, "manifest.json")); err != nil {
		t.Fatal(err)
	}

	s := &store.Store{Root: filepath.Join(dataDir, "store")}
	if fx.withOriginal {
		if err := s.AdoptOriginal(fx.name, fx.path); err != nil {
			t.Fatal(err)
		}
	}
	for _, tag := range fx.storeVersions {
		// Distinct body so store Put accepts a real file for retained history.
		hist := writeExecutable(t, t.TempDir(), fx.name, "history-"+tag+"\n")
		if err := s.Put(fx.name, tag, hist); err != nil {
			t.Fatal(err)
		}
	}
}
