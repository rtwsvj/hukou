package durablefs

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestAtomicWriteFileReplacesCompleteFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := AtomicWriteFile(path, []byte("new contents"), 0o640); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new contents" {
		t.Fatalf("content=%q", data)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o640 {
		t.Fatalf("mode=%#o want %#o", info.Mode().Perm(), os.FileMode(0o640))
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "state.json" {
		t.Fatalf("entries=%v", entries)
	}
}

func TestRenameRejectsDifferentParents(t *testing.T) {
	left := t.TempDir()
	right := t.TempDir()
	src := filepath.Join(left, "src")
	if err := os.WriteFile(src, []byte("body"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := Rename(src, filepath.Join(right, "dst"))
	if err == nil || !strings.Contains(err.Error(), "same parent") {
		t.Fatalf("error=%v", err)
	}
	if _, err := os.Stat(src); err != nil {
		t.Fatalf("source changed after rejected rename: %v", err)
	}
}

func TestMkdirAllAndDurableMutations(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "a", "b")
	if err := MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(nested, "src")
	if err := AtomicWriteFile(src, []byte("body"), 0o600); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(nested, "linked")
	if err := Link(src, linked); err != nil {
		t.Fatal(err)
	}
	if err := Remove(linked); err != nil {
		t.Fatal(err)
	}
	if err := RemoveAll(filepath.Join(root, "a")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(nested); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("nested path remains: %v", err)
	}
}

func TestMkdirAllReaffirmsExistingAncestorEntriesRootToLeaf(t *testing.T) {
	path := filepath.Join(t.TempDir(), "already", "present")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}

	var synced []string
	fs := FileSystem{syncDirFn: func(path string) error {
		synced = append(synced, filepath.Clean(path))
		return nil
	}}
	if err := fs.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	for current := filepath.Clean(abs); ; {
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		want = append(want, parent)
		current = parent
	}
	for left, right := 0, len(want)-1; left < right; left, right = left+1, right-1 {
		want[left], want[right] = want[right], want[left]
	}
	if !reflect.DeepEqual(synced, want) {
		t.Fatalf("synced=%v want root-to-leaf %v", synced, want)
	}
}

func TestRemoveAllMissingReaffirmsParent(t *testing.T) {
	parent := t.TempDir()
	missing := filepath.Join(parent, "already-removed")
	var synced []string
	fs := FileSystem{syncDirFn: func(path string) error {
		synced = append(synced, filepath.Clean(path))
		return nil
	}}
	if err := fs.RemoveAll(missing); err != nil {
		t.Fatal(err)
	}
	if want := []string{filepath.Clean(parent)}; !reflect.DeepEqual(synced, want) {
		t.Fatalf("synced=%v want %v", synced, want)
	}
}

func TestRemoveAllMissingPropagatesParentSyncFailure(t *testing.T) {
	sentinel := errors.New("injected directory sync failure")
	fs := FileSystem{syncDirFn: func(string) error { return sentinel }}
	err := fs.RemoveAll(filepath.Join(t.TempDir(), "already-removed"))
	if !errors.Is(err, sentinel) {
		t.Fatalf("error=%v want wrapped sentinel", err)
	}
}

func TestSyncFileRejectsNil(t *testing.T) {
	if err := SyncFile(nil); err == nil {
		t.Fatal("expected nil-file error")
	}
}
