package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
