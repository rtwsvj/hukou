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

func TestRemoveEmptyParentsStopsAtDataRootAndAncestors(t *testing.T) {
	base := t.TempDir()
	dataRoot := filepath.Join(base, "srv", "hukou-data")

	// Inside the data root: residue deeper than a direct child is pruned, but
	// the direct child itself (store/) must survive.
	chain := filepath.Join(dataRoot, "store", "tool", "original")
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
	if err := removeEmptyParents(file, dataRoot); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dataRoot, "store")); err != nil {
		t.Fatalf("direct child of data root removed: %v", err)
	}

	// Outside the data root: pruning keeps going through genuinely empty
	// parents but must never ascend into an ancestor of the data root, even
	// when that ancestor has become empty. srv/ is the parent of the data
	// root; pre-fix code anchored on the pending journal would have removed
	// it and kept climbing towards the filesystem root.
	live := filepath.Join(base, "srv", "tools", "tool")
	if err := os.MkdirAll(filepath.Dir(live), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(live, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(live); err != nil {
		t.Fatal(err)
	}
	if err := removeEmptyParents(live, dataRoot); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(base, "srv", "tools")); !os.IsNotExist(err) {
		t.Fatalf("empty live parent not pruned: %v", err)
	}
	if _, err := os.Stat(filepath.Join(base, "srv")); err != nil {
		t.Fatalf("ancestor of data root was removed: %v", err)
	}
}

func TestRemoveEmptyParentsNeverPrunesHomeChildrenOrHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dataRoot := t.TempDir() // deliberately outside the fake HOME

	// ~/bin/tool rolled back to absent: ~/bin is a direct child of $HOME and
	// must survive even though it became empty. Pre-fix code deleted it.
	bin := filepath.Join(home, "bin")
	live := filepath.Join(bin, "tool")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(live, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(live); err != nil {
		t.Fatal(err)
	}
	if err := removeEmptyParents(live, dataRoot); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("~/bin was removed by rollback: %v", err)
	}

	// Deeper chains still converge: ~/local/bin may go, ~/local may not.
	localBin := filepath.Join(home, "local", "bin")
	nested := filepath.Join(localBin, "tool")
	if err := os.MkdirAll(localBin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(nested, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(nested); err != nil {
		t.Fatal(err)
	}
	if err := removeEmptyParents(nested, dataRoot); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(localBin); !os.IsNotExist(err) {
		t.Fatalf("empty ~/local/bin not pruned: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "local")); err != nil {
		t.Fatalf("direct child of $HOME removed: %v", err)
	}
}

func quarantineMetaForTest(original string) []byte {
	return []byte("schema=hukou-quarantine-v1\noriginal_name=\"" + original + "\"\nquarantined_at=2026-08-22T00:00:00Z\n")
}

func TestRecoverCleansEmptyQuarantineContainer(t *testing.T) {
	root := t.TempDir()
	txRoot := filepath.Join(root, transactionsDirName)
	if err := os.MkdirAll(txRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	// An interrupted allocation between Mkdir and the META write leaves an
	// empty container; recovery must discard it instead of wedging.
	container := filepath.Join(txRoot, "quarantined-0123456789abcdef")
	if err := os.Mkdir(container, 0o700); err != nil {
		t.Fatal(err)
	}
	summary, err := Recover(root)
	if err != nil {
		t.Fatalf("recovery did not proceed past empty container: %v", err)
	}
	if len(summary.Quarantined) != 0 {
		t.Fatalf("unexpected quarantine records: %+v", summary.Quarantined)
	}
	if _, err := os.Lstat(container); !os.IsNotExist(err) {
		t.Fatalf("empty quarantine container was not discarded: %v", err)
	}
}

func TestRecoverCleansMetaOnlyQuarantineContainerAndRequarantinesSource(t *testing.T) {
	root := t.TempDir()
	txRoot := filepath.Join(root, transactionsDirName)
	if err := os.MkdirAll(txRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	// Crash window between the META write and the payload rename: the source
	// entry is still at the transaction root and the container holds only its
	// META, so nothing was ever quarantined.
	stray := filepath.Join(txRoot, "stale-debris")
	if err := os.WriteFile(stray, []byte("junk"), 0o600); err != nil {
		t.Fatal(err)
	}
	container := filepath.Join(txRoot, "quarantined-fedcba9876543210")
	if err := os.Mkdir(container, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(container, quarantineMetaFileName), quarantineMetaForTest("stale-debris"), 0o600); err != nil {
		t.Fatal(err)
	}
	summary, err := Recover(root)
	if err != nil {
		t.Fatalf("recovery did not proceed past META-only container: %v", err)
	}
	if _, err := os.Lstat(container); !os.IsNotExist(err) {
		t.Fatalf("META-only quarantine container was not discarded: %v", err)
	}
	// The still-present source entry is quarantined again into a fresh,
	// fully formed container.
	if len(summary.Quarantined) != 1 || summary.Quarantined[0].Original != "stale-debris" {
		t.Fatalf("expected stale-debris to be re-quarantined, got %+v", summary.Quarantined)
	}
	fresh := filepath.Join(txRoot, summary.Quarantined[0].Quarantined)
	payload, err := os.ReadFile(filepath.Join(fresh, quarantinePayloadName))
	if err != nil {
		t.Fatalf("fresh container missing payload: %v", err)
	}
	if string(payload) != "junk" {
		t.Fatalf("payload bytes not preserved: %q", payload)
	}
	if _, err := os.Stat(filepath.Join(fresh, quarantineMetaFileName)); err != nil {
		t.Fatalf("fresh container missing META: %v", err)
	}
}

func TestRecoverStillHonorsFullQuarantineContainer(t *testing.T) {
	root := t.TempDir()
	txRoot := filepath.Join(root, transactionsDirName)
	if err := os.MkdirAll(txRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	container := filepath.Join(txRoot, "quarantined-abcdabcdabcdabcd")
	if err := os.Mkdir(container, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(container, quarantineMetaFileName), quarantineMetaForTest("junk-entry"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(container, quarantinePayloadName), []byte("evidence"), 0o600); err != nil {
		t.Fatal(err)
	}
	summary, err := Recover(root)
	if err != nil {
		t.Fatalf("fully formed container blocked recovery: %v", err)
	}
	if len(summary.Quarantined) != 0 {
		t.Fatalf("unexpected quarantine records: %+v", summary.Quarantined)
	}
	if _, err := os.Stat(filepath.Join(container, quarantinePayloadName)); err != nil {
		t.Fatalf("valid quarantine container was disturbed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(container, quarantineMetaFileName)); err != nil {
		t.Fatalf("valid quarantine container was disturbed: %v", err)
	}
}

func TestRecoverFailsClosedOnPayloadOnlyQuarantineContainer(t *testing.T) {
	root := t.TempDir()
	txRoot := filepath.Join(root, transactionsDirName)
	if err := os.MkdirAll(txRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	// Payload without META has no recorded original name and cannot come from
	// any interruption window quarantineEntry allows, so it must stay
	// fail-closed like any other unknown directory.
	container := filepath.Join(txRoot, "quarantined-1234123412341234")
	if err := os.Mkdir(container, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(container, quarantinePayloadName), []byte("mystery"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Recover(root); err == nil {
		t.Fatal("expected fail-closed error for payload-only container")
	}
	if _, err := os.Stat(filepath.Join(container, quarantinePayloadName)); err != nil {
		t.Fatalf("payload-only container was modified: %v", err)
	}
}
