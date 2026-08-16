package cmd

import (
	"bytes"
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
