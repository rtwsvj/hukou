package manifest

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type recordingDurability struct {
	calls  []string
	failAt string
}

func (d *recordingDurability) AtomicWriteFile(path string, _ []byte, _ os.FileMode) error {
	d.calls = append(d.calls, filepath.Base(path))
	if filepath.Base(path) == d.failAt {
		return errors.New("injected durability failure")
	}
	return nil
}

func TestSaveWritesBackupBeforeReplacement(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.json")
	if err := os.WriteFile(path, []byte("{\"schema_version\":1,\"entries\":[]}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ops := &recordingDurability{}
	if err := (&Manifest{SchemaVersion: 1}).save(path, ops); err != nil {
		t.Fatal(err)
	}
	want := []string{"manifest.json.bak", "manifest.json"}
	if !reflect.DeepEqual(ops.calls, want) {
		t.Fatalf("calls=%v want %v", ops.calls, want)
	}
}

func TestSaveStopsWhenBackupDurabilityFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.json")
	if err := os.WriteFile(path, []byte("{\"schema_version\":1,\"entries\":[]}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ops := &recordingDurability{failAt: "manifest.json.bak"}
	if err := (&Manifest{SchemaVersion: 1}).save(path, ops); err == nil {
		t.Fatal("expected injected failure")
	}
	want := []string{"manifest.json.bak"}
	if !reflect.DeepEqual(ops.calls, want) {
		t.Fatalf("calls=%v want %v", ops.calls, want)
	}
}
