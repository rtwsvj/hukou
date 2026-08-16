package transaction

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRemoveEmptyParentsResidueAndBoundary(t *testing.T) {
	root := t.TempDir()
	// store/tool/original/tool — the adopt staging chain; "store" must survive
	// (direct child of the journal root), "tool" and "original" may go.
	chain := filepath.Join(root, "store", "tool", "original")
	if err := os.MkdirAll(chain, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(chain, "tool")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := removeEmptyParents(file, root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "store", "tool")); !os.IsNotExist(err) {
		t.Fatalf("residue tool dir not removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "store")); err != nil {
		t.Fatalf("store root child must survive: %v", err)
	}
	// journal root and its parent must never be touched
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("journal root touched: %v", err)
	}
}

func TestRemoveEmptyParentsStopsAtNonEmpty(t *testing.T) {
	root := t.TempDir()
	chain := filepath.Join(root, "store", "tool", "original")
	if err := os.MkdirAll(chain, 0o755); err != nil {
		t.Fatal(err)
	}
	// sibling keeps store/tool non-empty after original/ is removed
	if err := os.WriteFile(filepath.Join(root, "store", "tool", "keep"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(chain, "tool")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := removeEmptyParents(file, root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "store", "tool", "keep")); err != nil {
		t.Fatalf("non-empty sibling dir removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "store", "tool")); err != nil {
		t.Fatalf("non-empty tool dir removed: %v", err)
	}
}
