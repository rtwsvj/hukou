package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rtwsvj/hukou/internal/doctor"
)

func TestDoctorJSONMissingRootIsReadOnly(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing")
	var out bytes.Buffer
	if err := doDoctor(&out, root, true, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(root); !os.IsNotExist(err) {
		t.Fatalf("doctor created data root: %v", err)
	}
	var report doctor.Report
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out.String())
	}
	if report.Status != doctor.StatusHealthy || report.Mode != "standard" {
		t.Fatalf("unexpected report: %+v", report)
	}
}

func TestDoctorTextReturnsFindingError(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "manifest.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err := doDoctor(&out, root, false, false)
	if !errors.Is(err, errDoctorFindings) {
		t.Fatalf("error = %v", err)
	}
	if !strings.Contains(out.String(), "MANIFEST_JSON_INVALID") || !strings.Contains(out.String(), "hukou doctor: BROKEN") {
		t.Fatalf("unexpected output: %s", out.String())
	}
}

func TestDoctorCommandRegistered(t *testing.T) {
	command, _, err := rootCmd.Find([]string{"doctor"})
	if err != nil {
		t.Fatal(err)
	}
	if command != doctorCmd {
		t.Fatalf("doctor command not registered: %v", command)
	}
	if doctorCmd.Flags().Lookup("json") == nil || doctorCmd.Flags().Lookup("deep") == nil {
		t.Fatal("doctor flags are not registered")
	}
}
