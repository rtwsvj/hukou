package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rtwsvj/hukou/internal/activation"
	"github.com/rtwsvj/hukou/internal/manifest"
	"github.com/rtwsvj/hukou/internal/store"
	"github.com/rtwsvj/hukou/internal/supportbundle"
	statejournal "github.com/rtwsvj/hukou/internal/transaction"
)

func TestRepairCommandsRegistered(t *testing.T) {
	command, _, err := rootCmd.Find([]string{"repair", "plan"})
	if err != nil {
		t.Fatal(err)
	}
	if command != repairPlanCmd || command.Flags().Lookup("action") == nil || command.Flags().Lookup("output") == nil {
		t.Fatal("repair plan command or flags are not registered")
	}
	command, _, err = rootCmd.Find([]string{"repair", "apply"})
	if err != nil {
		t.Fatal(err)
	}
	if command != repairApplyCmd || command.Flags().Lookup("plan") == nil {
		t.Fatal("repair apply command or flag is not registered")
	}
}

func TestRepairPlanAndApplyCommandHelpers(t *testing.T) {
	root := t.TempDir()
	live := filepath.Join(t.TempDir(), "repair-command-tool")
	if err := os.WriteFile(live, []byte("before"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := statejournal.Begin(root, "command-test", "repair-command-tool", []statejournal.Spec{
		{Role: "live", Path: live, After: statejournal.RegularBytes([]byte("after"), 0o755)},
	}); err != nil {
		t.Fatal(err)
	}
	planPath := filepath.Join(t.TempDir(), "repair-plan.json")
	var output bytes.Buffer
	if err := doRepairPlan(&output, root, "recover-transaction", planPath, time.Unix(1_700_000_000, 0)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Repair plan written") {
		t.Fatalf("unexpected plan output: %s", output.String())
	}
	output.Reset()
	if err := doRepairApply(&output, root, planPath); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Repair completed: recover-transaction") {
		t.Fatalf("unexpected apply output: %s", output.String())
	}
	if got, _ := os.ReadFile(live); string(got) != "before" {
		t.Fatalf("repair did not restore before state: %q", got)
	}
}

// chainFixture builds a data root with one registered live tool so the CLI
// chain tests exercise doctor and repair against a real manifest.
func chainFixture(t *testing.T) (root, livePath string) {
	t.Helper()
	root = t.TempDir()
	livePath = filepath.Join(t.TempDir(), "chain-tool")
	if err := os.WriteFile(livePath, []byte("chain-v1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	sha, err := store.SHA256File(livePath)
	if err != nil {
		t.Fatal(err)
	}
	m := &manifest.Manifest{
		SchemaVersion: manifest.CurrentSchemaVersion,
		Retention:     manifest.DefaultRetentionPolicy(),
		Entries:       make([]manifest.Entry, 0),
	}
	entry := manifest.Entry{
		Name:         "chain-tool",
		Path:         livePath,
		Repo:         "example/project",
		Tag:          "v1.0.0",
		SHA256:       sha,
		Upstream:     "https://example.invalid",
		AdoptedAt:    "2026-01-01T00:00:00Z",
		UpdatedAt:    "2026-01-01T00:00:00Z",
		UpdatePolicy: manifest.DefaultUpdatePolicy(),
	}
	if err := activation.RecordAdopt(&entry, "act-chain", entry.UpdatedAt); err != nil {
		t.Fatal(err)
	}
	m.Put(entry)
	if err := m.Save(filepath.Join(root, "manifest.json")); err != nil {
		t.Fatal(err)
	}
	return root, livePath
}

func TestRepairCLIQuarantineChain(t *testing.T) {
	root, _ := chainFixture(t)
	txRoot := filepath.Join(root, "transactions")
	if err := os.MkdirAll(txRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(txRoot, "junk-entry"), []byte("junk"), 0o600); err != nil {
		t.Fatal(err)
	}

	// 1. doctor reports the stray entry before any repair.
	var out bytes.Buffer
	_ = doDoctor(&out, root, false, false)
	if !strings.Contains(out.String(), "TRANSACTION_ENTRY_INVALID") {
		t.Fatalf("doctor did not flag the stray entry: %s", out.String())
	}

	// 2. recover-transaction plan + apply quarantines it and says so.
	planPath := filepath.Join(t.TempDir(), "recover.json")
	out.Reset()
	if err := doRepairPlan(&out, root, "recover-transaction", planPath, time.Now()); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := doRepairApply(&out, root, planPath); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `Quarantined unknown transaction entry "junk-entry"`) {
		t.Fatalf("apply output does not surface the quarantine: %s", out.String())
	}

	// 3. doctor now reports the quarantined entry as a warning.
	out.Reset()
	_ = doDoctor(&out, root, false, false)
	if !strings.Contains(out.String(), "TRANSACTION_QUARANTINED_PRESENT") {
		t.Fatalf("doctor did not report the quarantine: %s", out.String())
	}

	// 4. purge-quarantine plan + apply removes it and says so.
	purgePath := filepath.Join(t.TempDir(), "purge.json")
	out.Reset()
	if err := doRepairPlan(&out, root, "purge-quarantine", purgePath, time.Now()); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := doRepairApply(&out, root, purgePath); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Purged quarantined entry") {
		t.Fatalf("apply output does not surface the purge: %s", out.String())
	}
	out.Reset()
	_ = doDoctor(&out, root, false, false)
	if strings.Contains(out.String(), "TRANSACTION_QUARANTINED_PRESENT") {
		t.Fatalf("quarantine still reported after purge: %s", out.String())
	}
}

func TestRepairCLICleanLiveTempsChain(t *testing.T) {
	root, livePath := chainFixture(t)
	liveDir := filepath.Dir(livePath)
	now := time.Now()
	orphan := filepath.Join(liveDir, ".hukou-txn-orphan")
	if err := os.WriteFile(orphan, []byte("orphan"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(orphan, now.Add(-2*time.Hour), now.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}

	// 1. deep doctor reports the orphan and points at clean-live-temps.
	var out bytes.Buffer
	_ = doDoctor(&out, root, false, true)
	if !strings.Contains(out.String(), "LIVE_TRANSACTION_TEMP_PRESENT") || !strings.Contains(out.String(), "clean-live-temps") {
		t.Fatalf("doctor guidance is incomplete: %s", out.String())
	}

	// 2. plan + apply removes exactly the orphan.
	planPath := filepath.Join(t.TempDir(), "clean.json")
	out.Reset()
	if err := doRepairPlan(&out, root, "clean-live-temps", planPath, now); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := doRepairApply(&out, root, planPath); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Removed orphaned live temporary") {
		t.Fatalf("apply output does not surface the removal: %s", out.String())
	}
	if _, err := os.Lstat(orphan); !os.IsNotExist(err) {
		t.Fatalf("orphan survived the chain: %v", err)
	}
	if data, err := os.ReadFile(livePath); err != nil || string(data) != "chain-v1\n" {
		t.Fatalf("live tool was touched: data=%q err=%v", data, err)
	}

	// 3. deep doctor no longer reports the orphan.
	out.Reset()
	_ = doDoctor(&out, root, false, true)
	if strings.Contains(out.String(), "LIVE_TRANSACTION_TEMP_PRESENT") {
		t.Fatalf("orphan still reported after clean: %s", out.String())
	}
}

func TestSupportBundleCommandRegistered(t *testing.T) {
	command, _, err := rootCmd.Find([]string{"support", "bundle"})
	if err != nil {
		t.Fatal(err)
	}
	if command != supportBundleCmd || command.Flags().Lookup("output") == nil || command.Flags().Lookup("format") == nil {
		t.Fatal("support bundle command or flags are not registered")
	}
}

func TestSupportBundleJSONStdoutDoesNotCreateDataRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing-data-root")
	var output bytes.Buffer
	if err := doSupportBundle(&output, root, "", "json", supportbundle.Build{Version: "test"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `"schema_version": 1`) {
		t.Fatalf("unexpected support JSON: %s", output.String())
	}
	if _, err := os.Lstat(root); !os.IsNotExist(err) {
		t.Fatalf("JSON stdout mode created data root: %v", err)
	}
}

func TestSupportBundleOutputModeAndOptionValidation(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing-data-root")
	outputPath := filepath.Join(t.TempDir(), "support.json")
	var output bytes.Buffer
	if err := doSupportBundle(&output, root, outputPath, "", supportbundle.Build{Version: "test"}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("support output mode = %o", info.Mode().Perm())
	}
	if err := doSupportBundle(&output, root, outputPath, "json", supportbundle.Build{}); err == nil {
		t.Fatal("accepted both --output and --format")
	}
	if err := doSupportBundle(&output, root, "", "yaml", supportbundle.Build{}); err == nil {
		t.Fatal("accepted unsupported format")
	}
}
