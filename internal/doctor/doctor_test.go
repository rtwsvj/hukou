package doctor_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rtwsvj/hukou/internal/doctor"
	"github.com/rtwsvj/hukou/internal/manifest"
)

func TestMissingDataRootIsHealthyAndCreatesNothing(t *testing.T) {
	root := filepath.Join(t.TempDir(), "does-not-exist")
	report := doctor.Scan(doctor.Options{DataRoot: root})
	if !report.Healthy() || !report.Complete {
		t.Fatalf("report = %+v", report)
	}
	if _, err := os.Lstat(root); !os.IsNotExist(err) {
		t.Fatalf("doctor created data root: %v", err)
	}
}

func TestValidAdoptBaselineIsHealthy(t *testing.T) {
	root, entry := validAdoptState(t)
	report := doctor.Scan(doctor.Options{DataRoot: root})
	if !report.Healthy() {
		t.Fatalf("report = %+v", report)
	}
	if !hasCode(report, "ADOPT_BASELINE_NOT_MATERIALIZED") {
		t.Fatalf("baseline finding missing: %+v", report.Findings)
	}
	if hasCode(report, "STORE_ACTIVE_VERSION_MISSING") {
		t.Fatalf("valid baseline treated as missing version: %+v", report.Findings)
	}
	if entry.Name != "tool" { // keep fixture return value exercised
		t.Fatal("unexpected fixture")
	}
}

func TestValidMaterializedActiveVersionIsHealthy(t *testing.T) {
	root := t.TempDir()
	live := writeExecutable(t, filepath.Join(t.TempDir(), "tool"), "v2")
	entry := fixtureEntry("tool", live, hashString("v2"), "v2")
	writeExecutable(t, filepath.Join(root, "store", "tool", "original", "tool"), "original")
	writeExecutable(t, filepath.Join(root, "store", "tool", "v2", "tool"), "v2")
	if err := (&manifest.Manifest{SchemaVersion: 1, Entries: []manifest.Entry{entry}}).Save(filepath.Join(root, "manifest.json")); err != nil {
		t.Fatal(err)
	}

	report := doctor.Scan(doctor.Options{DataRoot: root})
	if !report.Healthy() || len(report.Findings) != 0 {
		t.Fatalf("report = %+v", report)
	}
}

func TestCorruptManifestMakesStoreUnclassifiable(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "manifest.json"), []byte(`{"schema_version":1,"entries":[`), 0o600); err != nil {
		t.Fatal(err)
	}
	writeExecutable(t, filepath.Join(root, "store", "orphan", "original", "orphan"), "original")

	report := doctor.Scan(doctor.Options{DataRoot: root})
	if !hasCode(report, "MANIFEST_JSON_INVALID") || !hasCode(report, "STORE_TOOL_UNCLASSIFIABLE") {
		t.Fatalf("unexpected findings: %+v", report.Findings)
	}
	if hasCode(report, "STORE_TOOL_ORPHAN") {
		t.Fatalf("corrupt manifest must not produce orphan classification: %+v", report.Findings)
	}
}

func TestValidBackupIsReportedForCorruptManifest(t *testing.T) {
	root := t.TempDir()
	manifestPath := filepath.Join(root, "manifest.json")
	if err := os.WriteFile(manifestPath, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	live := writeExecutable(t, filepath.Join(t.TempDir(), "tool"), "body")
	backup := &manifest.Manifest{SchemaVersion: 1, Entries: []manifest.Entry{
		fixtureEntry("tool", live, hashString("body"), "local"),
	}}
	if err := backup.Save(manifestPath + ".bak"); err != nil {
		t.Fatal(err)
	}

	report := doctor.Scan(doctor.Options{DataRoot: root})
	if !hasCode(report, "MANIFEST_JSON_INVALID") || !hasCode(report, "MANIFEST_BACKUP_AVAILABLE") {
		t.Fatalf("unexpected findings: %+v", report.Findings)
	}
}

func TestManifestDuplicatesAndLiveDrift(t *testing.T) {
	root := t.TempDir()
	live := writeExecutable(t, filepath.Join(t.TempDir(), "tool"), "changed")
	entry := fixtureEntry("tool", live, hashString("expected"), "v1")
	m := &manifest.Manifest{SchemaVersion: 1, Entries: []manifest.Entry{entry, entry}}
	if err := m.Save(filepath.Join(root, "manifest.json")); err != nil {
		t.Fatal(err)
	}
	writeExecutable(t, filepath.Join(root, "store", "tool", "original", "tool"), "expected")

	report := doctor.Scan(doctor.Options{DataRoot: root})
	for _, code := range []string{"MANIFEST_DUPLICATE_NAME", "MANIFEST_DUPLICATE_PATH", "LIVE_SHA256_MISMATCH"} {
		if !hasCode(report, code) {
			t.Fatalf("missing %s: %+v", code, report.Findings)
		}
	}
}

func TestSemanticallyInvalidManifestDoesNotClassifyOrphans(t *testing.T) {
	root := t.TempDir()
	live := writeExecutable(t, filepath.Join(t.TempDir(), "tool"), "body")
	entry := fixtureEntry("tool", live, "not-a-sha", "v1")
	m := &manifest.Manifest{SchemaVersion: 1, Entries: []manifest.Entry{entry}}
	if err := m.Save(filepath.Join(root, "manifest.json")); err != nil {
		t.Fatal(err)
	}
	writeExecutable(t, filepath.Join(root, "store", "tool", "original", "tool"), "body")
	writeExecutable(t, filepath.Join(root, "store", "unbound", "original", "unbound"), "body")

	report := doctor.Scan(doctor.Options{DataRoot: root})
	if !hasCode(report, "MANIFEST_ENTRY_SHA256_INVALID") || !hasCode(report, "STORE_TOOL_UNCLASSIFIABLE") {
		t.Fatalf("unexpected findings: %+v", report.Findings)
	}
	if hasCode(report, "STORE_TOOL_ORPHAN") {
		t.Fatalf("invalid manifest must not classify orphan: %+v", report.Findings)
	}
}

func TestValidManifestClassifiesOrphanTool(t *testing.T) {
	root, _ := validAdoptState(t)
	writeExecutable(t, filepath.Join(root, "store", "unbound", "original", "unbound"), "body")

	report := doctor.Scan(doctor.Options{DataRoot: root})
	if !hasCode(report, "STORE_TOOL_ORPHAN") || hasCode(report, "STORE_TOOL_UNCLASSIFIABLE") {
		t.Fatalf("unexpected findings: %+v", report.Findings)
	}
}

func TestDeepReportsRetainedHashAndLiveTemps(t *testing.T) {
	root, entry := validAdoptState(t)
	writeExecutable(t, filepath.Join(root, "store", entry.Name, "v2", entry.Name), "retained")
	liveTemp := filepath.Join(filepath.Dir(entry.Path), ".hukou-tmp-leftover")
	if err := os.WriteFile(liveTemp, []byte("tmp"), 0o600); err != nil {
		t.Fatal(err)
	}
	rollbackTemp := filepath.Join(filepath.Dir(entry.Path), ".hukou-rollback-leftover")
	if err := os.WriteFile(rollbackTemp, []byte("snapshot"), 0o600); err != nil {
		t.Fatal(err)
	}
	txnTemp := filepath.Join(filepath.Dir(entry.Path), ".hukou-txn-leftover")
	if err := os.WriteFile(txnTemp, []byte("txn"), 0o600); err != nil {
		t.Fatal(err)
	}

	report := doctor.Scan(doctor.Options{DataRoot: root, Deep: true})
	for _, code := range []string{"STORE_RETAINED_SHA256", "LIVE_TEMP_PRESENT", "LIVE_ROLLBACK_SNAPSHOT_PRESENT", "LIVE_TRANSACTION_TEMP_PRESENT"} {
		if !hasCode(report, code) {
			t.Fatalf("missing %s: %+v", code, report.Findings)
		}
	}
}

func TestStagingManifestTempAndPendingTransaction(t *testing.T) {
	root, _ := validAdoptState(t)
	if err := os.WriteFile(filepath.Join(root, "manifest-crash.tmp"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".manifest.json-crash.tmp"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "store", ".tmp", "extract-leftover"), 0o755); err != nil {
		t.Fatal(err)
	}
	pending := filepath.Join(root, "transactions", "pending-abc")
	if err := os.MkdirAll(pending, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pending, "intent.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}

	report := doctor.Scan(doctor.Options{DataRoot: root})
	for _, code := range []string{"MANIFEST_TEMP_PRESENT", "STORE_TMP_NOT_EMPTY", "TRANSACTION_PENDING"} {
		if !hasCode(report, code) {
			t.Fatalf("missing %s: %+v", code, report.Findings)
		}
	}
}

func TestReportRenderingIsDeterministic(t *testing.T) {
	root, _ := validAdoptState(t)
	first := doctor.Scan(doctor.Options{DataRoot: root, Deep: true})
	second := doctor.Scan(doctor.Options{DataRoot: root, Deep: true})
	var firstJSON, secondJSON bytes.Buffer
	if err := doctor.WriteJSON(&firstJSON, first); err != nil {
		t.Fatal(err)
	}
	if err := doctor.WriteJSON(&secondJSON, second); err != nil {
		t.Fatal(err)
	}
	if firstJSON.String() != secondJSON.String() {
		t.Fatalf("JSON is unstable:\n%s\n---\n%s", firstJSON.String(), secondJSON.String())
	}
	var text bytes.Buffer
	if err := doctor.WriteText(&text, first); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text.String(), "hukou doctor: HEALTHY") || !strings.Contains(text.String(), "summary:") {
		t.Fatalf("unexpected text output: %s", text.String())
	}
}

func TestCaseAliasRenderingIsDeterministicWithMultipleManifestMatches(t *testing.T) {
	root := t.TempDir()
	liveUpper := writeExecutable(t, filepath.Join(t.TempDir(), "Foo"), "upper")
	liveMixed := writeExecutable(t, filepath.Join(t.TempDir(), "fOO"), "mixed")
	m := &manifest.Manifest{SchemaVersion: 1, Entries: []manifest.Entry{
		fixtureEntry("Foo", liveUpper, hashString("upper"), "v1"),
		fixtureEntry("fOO", liveMixed, hashString("mixed"), "v1"),
	}}
	if err := m.Save(filepath.Join(root, "manifest.json")); err != nil {
		t.Fatal(err)
	}
	writeExecutable(t, filepath.Join(root, "store", "foo", "original", "foo"), "original")

	var baseline string
	var aliasMessages []string
	for i := 0; i < 50; i++ {
		report := doctor.Scan(doctor.Options{DataRoot: root})
		var rendered bytes.Buffer
		if err := doctor.WriteJSON(&rendered, report); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			baseline = rendered.String()
			for _, finding := range report.Findings {
				if finding.Code == "STORE_TOOL_CASE_ALIAS" {
					aliasMessages = append(aliasMessages, finding.Message)
				}
			}
		} else if rendered.String() != baseline {
			t.Fatalf("case-alias JSON changed on iteration %d:\n%s\n---\n%s", i, baseline, rendered.String())
		}
	}
	if len(aliasMessages) != 2 ||
		aliasMessages[0] != `store spelling conflicts with manifest name "Foo"` ||
		aliasMessages[1] != `store spelling conflicts with manifest name "fOO"` {
		t.Fatalf("unexpected deterministic alias findings: %q", aliasMessages)
	}
}

func TestTextRenderingEscapesControlCharacters(t *testing.T) {
	report := doctor.Report{
		SchemaVersion: doctor.ReportSchemaVersion,
		Mode:          "standard",
		Status:        doctor.StatusDegraded,
		Complete:      true,
		DataRoot:      "/tmp/root",
		Summary:       doctor.Summary{Warnings: 1},
		Findings: []doctor.Finding{{
			Code:     "UNEXPECTED",
			Severity: doctor.SeverityWarning,
			Scope:    "store",
			Subject:  "evil\nsubject",
			Path:     "/tmp/evil\tpath",
			Message:  "bad\rmessage",
		}},
	}
	var text bytes.Buffer
	if err := doctor.WriteText(&text, report); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text.String(), "evil\nsubject") || strings.Contains(text.String(), "evil\tpath") || strings.Contains(text.String(), "bad\rmessage") {
		t.Fatalf("control character was emitted literally: %q", text.String())
	}
}

func TestTextRenderingEscapesDataRootControlCharacters(t *testing.T) {
	report := doctor.Report{
		SchemaVersion: doctor.ReportSchemaVersion,
		Mode:          "standard",
		Status:        doctor.StatusHealthy,
		Complete:      true,
		DataRoot:      "/tmp/root\n[ERROR] FORGED\tvalue",
		Findings:      []doctor.Finding{},
	}
	var text bytes.Buffer
	if err := doctor.WriteText(&text, report); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text.String(), "\n[ERROR] FORGED") || strings.Contains(text.String(), "FORGED\tvalue") {
		t.Fatalf("data root control character was emitted literally: %q", text.String())
	}
	if !strings.Contains(text.String(), `data root: "/tmp/root\n[ERROR] FORGED\tvalue"`) {
		t.Fatalf("data root was not escaped predictably: %q", text.String())
	}
}

func validAdoptState(t *testing.T) (string, manifest.Entry) {
	t.Helper()
	root := t.TempDir()
	live := writeExecutable(t, filepath.Join(t.TempDir(), "tool"), "original")
	entry := fixtureEntry("tool", live, hashString("original"), "v1")
	writeExecutable(t, filepath.Join(root, "store", "tool", "original", "tool"), "original")
	m := &manifest.Manifest{SchemaVersion: 1, Entries: []manifest.Entry{entry}}
	if err := m.Save(filepath.Join(root, "manifest.json")); err != nil {
		t.Fatal(err)
	}
	return root, entry
}

func fixtureEntry(name, path, sha, tag string) manifest.Entry {
	return manifest.Entry{
		Name:      name,
		Path:      path,
		Repo:      "owner/repo",
		Tag:       tag,
		SHA256:    sha,
		AdoptedAt: "2026-07-13T00:00:00Z",
		UpdatedAt: "2026-07-13T00:00:00Z",
	}
}

func writeExecutable(t *testing.T, path, content string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func hashString(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func hasCode(report doctor.Report, code string) bool {
	for _, finding := range report.Findings {
		if finding.Code == code {
			return true
		}
	}
	return false
}
