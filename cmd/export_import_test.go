package cmd

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rtwsvj/hukou/internal/manifest"
)

func TestExportEmptyManifestFails(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("HUKOU_DATA_DIR", dataDir)
	var out bytes.Buffer
	err := doExport(&out, "")
	if err == nil || !strings.Contains(err.Error(), "no adopted tools to export") {
		t.Fatalf("expected empty-manifest refusal, got %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("rejected export wrote output: %s", out.String())
	}
}

func TestExportImportRoundTrip(t *testing.T) {
	srcData := t.TempDir()
	t.Setenv("HUKOU_DATA_DIR", srcData)
	srcBin := t.TempDir()
	toolA := writeExecutable(t, srcBin, "toola", "v1\n")
	localTool := writeExecutable(t, srcBin, "localtool", "l1\n")
	var out bytes.Buffer
	if err := doAdopt(&out, &out, toolA, "owner/repo", false, "v1.0.0", false, "inner-a"); err != nil {
		t.Fatal(err)
	}
	if err := doAdopt(&out, &out, localTool, "", true, "local", false, ""); err != nil {
		t.Fatal(err)
	}
	// Non-default policy so the round trip must re-apply it.
	if err := doPolicySetWithSave(&out, &out, "toola", policySetOptions{Mode: "github-latest", ModeSet: true}, saveManifest); err != nil {
		t.Fatal(err)
	}

	listFile := filepath.Join(t.TempDir(), "tools.json")
	if err := doExport(&out, listFile); err != nil {
		t.Fatal(err)
	}
	var doc exportDoc
	payload, err := os.ReadFile(listFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(payload, &doc); err != nil {
		t.Fatalf("exported doc is not JSON: %v", err)
	}
	if doc.SchemaVersion != exportFileSchemaVersion || len(doc.Tools) != 2 {
		t.Fatalf("unexpected doc: %+v", doc)
	}
	if doc.Tools[0].Name != "localtool" || doc.Tools[1].Name != "toola" {
		t.Fatalf("tools not sorted: %+v", doc.Tools)
	}
	if doc.Tools[1].Repo != "owner/repo" || doc.Tools[1].ArchiveExe != "inner-a" || doc.Tools[1].UpdatePolicy.Mode != "github-latest" || doc.Tools[1].AdoptedSHA256 == "" {
		t.Fatalf("toola metadata wrong: %+v", doc.Tools[1])
	}
	if doc.Tools[0].Type != "local" {
		t.Fatalf("localtool type wrong: %+v", doc.Tools[0])
	}

	// New machine: fresh data dir, fresh binaries with the same names.
	dstData := t.TempDir()
	t.Setenv("HUKOU_DATA_DIR", dstData)
	dstBin := t.TempDir()
	if err := os.WriteFile(filepath.Join(dstBin, "toola"), []byte("v1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dstBin, "localtool"), []byte("l1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dstBin)
	var iout bytes.Buffer
	if err := doImport(&iout, &iout, listFile, false, false, false, nil); err != nil {
		t.Fatalf("import failed: %v\noutput: %s", err, iout.String())
	}
	m, err := manifest.Load(filepath.Join(dstData, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if m.Get("toola") == nil {
		t.Fatalf("toola not imported; manifest entries: %+v", m.Entries)
	}
	e := m.Get("toola")
	if e.Repo != "owner/repo" || e.Tag != "v1.0.0" || e.ArchiveExe != "inner-a" {
		t.Fatalf("toola import metadata wrong: %+v", e)
	}
	if e.UpdatePolicy.Mode != "github-latest" {
		t.Fatalf("policy not re-applied: %+v", e.UpdatePolicy)
	}
	if m.Get("localtool") != nil {
		t.Fatalf("local entry should have been skipped: %+v", m.Get("localtool"))
	}
	if !strings.Contains(iout.String(), "localtool") || !strings.Contains(iout.String(), "skipped") {
		t.Fatalf("skip not reported: %s", iout.String())
	}
}

func TestImportDryRunWritesNothing(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "missing-data-root")
	t.Setenv("HUKOU_DATA_DIR", dataDir)
	listFile := filepath.Join(t.TempDir(), "tools.json")
	doc := exportDoc{
		SchemaVersion: exportFileSchemaVersion,
		ExportedAt:    "2026-08-01T00:00:00Z",
		Tools:         []exportEntry{{Name: "toola", Type: "github", Repo: "owner/repo", Tag: "v1.0.0", UpdatePolicy: exportPolicy{Mode: "semver", Channel: "stable"}}},
	}
	payload, _ := json.Marshal(doc)
	if err := os.WriteFile(listFile, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "toola"), []byte("v1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	var out bytes.Buffer
	if err := doImport(&out, &out, listFile, true, false, false, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Would import toola") {
		t.Fatalf("dry-run plan missing: %s", out.String())
	}
	if _, err := os.Stat(dataDir); !os.IsNotExist(err) {
		t.Fatalf("dry-run created data root: %v", err)
	}
}

func TestImportRejectsInvalidDocs(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("HUKOU_DATA_DIR", dataDir)
	cases := map[string]string{
		"unsupported schema": `{"schema_version":99,"exported_at":"x","tools":[]}`,
		"trailing json":      `{"schema_version":1,"exported_at":"x","tools":[]} {}`,
		"unknown field":      `{"schema_version":1,"exported_at":"x","tools":[],"evil":1}`,
		"duplicate name":     `{"schema_version":1,"exported_at":"x","tools":[{"name":"a","type":"github","repo":"o/r","tag":"v1","update_policy":{"mode":"semver","channel":"stable"}},{"name":"a","type":"github","repo":"o/r","tag":"v1","update_policy":{"mode":"semver","channel":"stable"}}]}`,
		"bad repo":           `{"schema_version":1,"exported_at":"x","tools":[{"name":"a","type":"github","repo":"not-a-repo","tag":"v1","update_policy":{"mode":"semver","channel":"stable"}}]}`,
		"bad type":           `{"schema_version":1,"exported_at":"x","tools":[{"name":"a","type":"ftp","tag":"v1","update_policy":{"mode":"semver","channel":"stable"}}]}`,
		"empty tools":        `{"schema_version":1,"exported_at":"x","tools":[]}`,
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			f := filepath.Join(t.TempDir(), "bad.json")
			if err := os.WriteFile(f, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			if err := doImport(&out, &out, f, true, false, false, nil); err == nil {
				t.Fatalf("expected rejection for %s; output: %s", name, out.String())
			}
		})
	}
}

func TestImportMissingBinaryFailsAndLeavesNothing(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("HUKOU_DATA_DIR", dataDir)
	listFile := filepath.Join(t.TempDir(), "tools.json")
	doc := exportDoc{
		SchemaVersion: exportFileSchemaVersion,
		ExportedAt:    "2026-08-01T00:00:00Z",
		Tools:         []exportEntry{{Name: "ghost", Type: "github", Repo: "owner/repo", Tag: "v1.0.0", UpdatePolicy: exportPolicy{Mode: "semver", Channel: "stable"}}},
	}
	payload, _ := json.Marshal(doc)
	if err := os.WriteFile(listFile, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir()) // empty dir: nothing on PATH
	var out bytes.Buffer
	err := doImport(&out, &out, listFile, false, false, true, nil)
	if err == nil {
		t.Fatalf("expected failure for missing binary; output: %s", out.String())
	}
	var report importReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("json report invalid: %v\n%s", err, out.String())
	}
	if report.Failed != 1 || report.Results[0].Status != "error" {
		t.Fatalf("unexpected report: %+v", report)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "manifest.json")); !os.IsNotExist(err) {
		t.Fatalf("failed import wrote a manifest: %v", err)
	}
}

// TestImportMismatchedSHAWarnsAndRecordsActualTag: the binary on this
// machine's PATH is not the build the export list recorded (common version
// skew — or a malicious list claiming v999.0.0 to freeze upgrades). Import
// must warn and record the actual version, never the list's tag; a matching
// SHA keeps the exported tag and stays silent.
func TestImportMismatchedSHAWarnsAndRecordsActualTag(t *testing.T) {
	t.Setenv("HUKOU_DATA_DIR", t.TempDir())
	binDir := t.TempDir()
	writeExecutable(t, binDir, "toola", "v2-binary\n")
	writeExecutable(t, binDir, "toolb", "v1-binary\n")
	t.Setenv("PATH", binDir)

	sumV2 := sha256.Sum256([]byte("v2-binary\n"))
	shaV2 := hex.EncodeToString(sumV2[:])
	sumV1 := sha256.Sum256([]byte("v1-binary\n"))
	shaV1 := hex.EncodeToString(sumV1[:])

	// toola: the list's sha256 belongs to a DIFFERENT build (v1), tag is a lie.
	var errBuf bytes.Buffer
	res := importOne(exportEntry{Name: "toola", Type: "github", Repo: "owner/repo", Tag: "v999.0.0", SHA256: shaV1}, false, &errBuf)
	if res.Status != "ok" {
		t.Fatalf("importOne mismatch: %+v (stderr: %s)", res, errBuf.String())
	}
	warning := errBuf.String()
	if !strings.Contains(warning, "v999.0.0") || !strings.Contains(warning, shaV1) || !strings.Contains(warning, shaV2) {
		t.Fatalf("warning missing name/tag/both hashes:\n%s", warning)
	}
	m, err := loadManifest()
	if err != nil {
		t.Fatal(err)
	}
	e := m.Get("toola")
	if e == nil {
		t.Fatal("toola not adopted")
	}
	if e.Tag == "v999.0.0" {
		t.Fatal("manifest recorded the export list's fake tag")
	}
	// A shell script carries no Go build info, so the honest fallback is
	// the neutral "imported" tag with the real sha recorded on the entry.
	if e.Tag != "imported" {
		t.Fatalf("tag = %q, want the neutral fallback %q", e.Tag, "imported")
	}
	if e.SHA256 != shaV2 {
		t.Fatalf("entry sha256 = %s, want the actual binary's %s", e.SHA256, shaV2)
	}

	// toolb: sha matches the list, so the exported tag stands and nothing warns.
	errBuf.Reset()
	res = importOne(exportEntry{Name: "toolb", Type: "github", Repo: "owner/repo", Tag: "v1.0.0", SHA256: shaV1}, false, &errBuf)
	if res.Status != "ok" {
		t.Fatalf("importOne match: %+v (stderr: %s)", res, errBuf.String())
	}
	if strings.Contains(errBuf.String(), "differs from the exported list") {
		t.Fatalf("false-positive warning on a matching sha:\n%s", errBuf.String())
	}
	m, err = loadManifest()
	if err != nil {
		t.Fatal(err)
	}
	if e := m.Get("toolb"); e == nil || e.Tag != "v1.0.0" {
		t.Fatalf("matching import did not keep the exported tag: %+v", e)
	}
}

// L17: export --output never writes through a planted symlink and always
// lands the list with exactly 0600 — even over a pre-existing file with
// looser permissions.
func TestExportOutputRejectsSymlinkAndForces0600(t *testing.T) {
	t.Setenv("HUKOU_DATA_DIR", t.TempDir())
	binDir := t.TempDir()
	toolA := writeExecutable(t, binDir, "toola", "v1\n")
	var out bytes.Buffer
	if err := doAdopt(&out, &out, toolA, "owner/repo", false, "v1.0.0", false, ""); err != nil {
		t.Fatal(err)
	}

	// Symlinked output path: refused, and the link target is untouched.
	target := filepath.Join(t.TempDir(), "target.json")
	if err := os.WriteFile(target, []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := doExport(&out, link); err == nil {
		t.Fatal("export wrote through a symlinked output path")
	}
	if got, _ := os.ReadFile(target); string(got) != "keep\n" {
		t.Fatalf("symlink target was overwritten: %q", got)
	}

	// Pre-existing 0o644 file: content replaced AND mode forced to 0600.
	outFile := filepath.Join(t.TempDir(), "tools.json")
	if err := os.WriteFile(outFile, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := doExport(&out, outFile); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(outFile)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("mode = %o, want 600 (forced, not inherited)", got)
	}
}

// L18: import refuses a symlinked toolset list and one over the size cap.
func TestImportRejectsSymlinkAndOversizedList(t *testing.T) {
	real := filepath.Join(t.TempDir(), "real.json")
	if err := os.WriteFile(real, []byte(`{"schema_version":1,"tools":[{"name":"toola","type":"github","repo":"owner/repo","tag":"v1.0.0","sha256":"","update_policy":{"mode":"semver","channel":"stable"}}]}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link.json")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if _, err := readExportDoc(link); err == nil {
		t.Fatal("import accepted a symlinked toolset list")
	}

	big := filepath.Join(t.TempDir(), "big.json")
	if err := os.WriteFile(big, make([]byte, maxExportDocBytes+1), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readExportDoc(big); err == nil {
		t.Fatal("import accepted an oversized toolset list")
	}

	// The plain regular file still reads fine.
	if _, err := readExportDoc(real); err != nil {
		t.Fatalf("regular toolset list rejected: %v", err)
	}
}
