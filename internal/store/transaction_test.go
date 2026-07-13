package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLiveSnapshotRestoreRegular(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tool")
	if err := os.WriteFile(path, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	snap, err := SnapshotLive(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("new-target", path); err != nil {
		t.Fatal(err)
	}
	if err := snap.Restore(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("want regular file, got %v", info.Mode())
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "old" {
		t.Fatalf("want old bytes, got %q", got)
	}
	assertNoRollbackFiles(t, dir)
}

func TestLiveSnapshotRestoreSymlink(t *testing.T) {
	dir := t.TempDir()
	oldTarget := filepath.Join(dir, "old")
	newTarget := filepath.Join(dir, "new")
	for path, body := range map[string]string{oldTarget: "old", newTarget: "new"} {
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(dir, "tool")
	if err := os.Symlink(oldTarget, link); err != nil {
		t.Fatal(err)
	}
	snap, err := SnapshotLive(link)
	if err != nil {
		t.Fatal(err)
	}
	if err := atomicSymlink(newTarget, link, dir); err != nil {
		t.Fatal(err)
	}
	if err := snap.Restore(); err != nil {
		t.Fatal(err)
	}
	got, err := os.Readlink(link)
	if err != nil {
		t.Fatal(err)
	}
	if got != oldTarget {
		t.Fatalf("want %s, got %s", oldTarget, got)
	}
}

func TestLiveSnapshotCommitRemovesBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tool")
	if err := os.WriteFile(path, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	snap, err := SnapshotLive(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := snap.Commit(); err != nil {
		t.Fatal(err)
	}
	assertNoRollbackFiles(t, dir)
}

func TestLiveSnapshotRejectsNonRegular(t *testing.T) {
	if _, err := SnapshotLive(t.TempDir()); err == nil {
		t.Fatal("expected non-regular snapshot error")
	}
}

func assertNoRollbackFiles(t *testing.T, dir string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, ".hukou-rollback-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("rollback files remain: %v", matches)
	}
}
