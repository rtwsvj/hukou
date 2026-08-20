package store

import (
	"os"
	"path/filepath"
	"testing"
)

// entryIsRegular must judge by the filesystem, not by DirEntry.Type() alone:
// on DT_UNKNOWN filesystems Type() is zero for everything, regular or not.
// (A real DT_UNKNOWN mount cannot be simulated portably; the helper's Lstat
// fallback is what these cases exercise.)
func TestEntryIsRegular(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "bin"), []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "bin"), filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, e := range entries {
		got[e.Name()] = entryIsRegular(dir, e)
	}
	if !got["bin"] {
		t.Error("regular file not recognized as regular")
	}
	if got["subdir"] {
		t.Error("directory misjudged as regular")
	}
	if got["link"] {
		t.Error("symlink misjudged as regular (Lstat must not follow)")
	}
}
