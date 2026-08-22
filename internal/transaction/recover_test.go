package transaction

import (
	"os"
	"path/filepath"
	"strings"
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

// TestRemoveEmptyParentsNeverAscendsAboveOrigTop pins the origTop stop rule:
// origTop is the first component of the ORIGINAL cleaned path under its
// volume root — "/opt" in "/opt/tool". After processing origTop the walk
// stops, so a rollback of /opt/tool prunes opt/tool and everything below it
// but never ascends into /opt's parent, however empty it has become. The
// walk is anchored to the data root too, so the test anchors its protected
// directory by making the data root the PARENT of the case tree: everything
// directly under dataRoot is permanent by the existing rule, which lets us
// observe exactly where the origTop boundary cuts.
func TestRemoveEmptyParentsNeverAscendsAboveOrigTop(t *testing.T) {
	// dataRoot doubles as the fake volume-root parent: its direct children
	// are protected by the parent == dataRoot guard, mirroring how /opt is
	// protected by (dir == origTop && parent == sep).
	dataRoot := t.TempDir()
	dataRoot = filepath.Clean(dataRoot)

	// Shape of /opt/tool: direct child "a" of dataRoot holding "b" and the
	// resource. origTop = dataRoot/a; pruning must remove a/b but stop at a.
	ab := filepath.Join(dataRoot, "a", "b")
	if err := os.MkdirAll(ab, 0o755); err != nil {
		t.Fatal(err)
	}
	live := filepath.Join(ab, "tool")
	if err := os.WriteFile(live, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(live); err != nil {
		t.Fatal(err)
	}
	if err := removeEmptyParents(live, dataRoot); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dataRoot, "a")); err != nil {
		t.Fatalf("origTop directory was removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataRoot, "a", "b")); !os.IsNotExist(err) {
		t.Fatalf("empty dirs below origTop not pruned: %v", err)
	}

	// A second top-level child proves the boundary is per-path, not global:
	// with live = dataRoot/c/tool, origTop = dataRoot/c, whose empty residue
	// child goes while c itself stays.
	c := filepath.Join(dataRoot, "c")
	live = filepath.Join(c, "tool")
	if err := os.MkdirAll(c, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(live, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(live); err != nil {
		t.Fatal(err)
	}
	if err := removeEmptyParents(filepath.Join(c, "tool"), dataRoot); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(c); err != nil {
		t.Fatalf("second origTop directory was removed: %v", err)
	}
}

func quarantineMetaForTest(original string) []byte {
	return []byte("schema=hukou-quarantine-v1\noriginal_name=\"" + original + "\"\nquarantined_at=2026-08-22T00:00:00Z\n")
}

func TestRecoverRelocatesEmptyQuarantineContainerIntact(t *testing.T) {
	root := t.TempDir()
	txRoot := filepath.Join(root, transactionsDirName)
	if err := os.MkdirAll(txRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	// An interrupted allocation between Mkdir and the META write leaves an
	// empty container; recovery must relocate it wholesale, never delete it.
	container := filepath.Join(txRoot, "quarantined-0123456789abcdef")
	if err := os.Mkdir(container, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Recover(root); err != nil {
		t.Fatalf("recovery did not proceed past empty container: %v", err)
	}
	if _, err := os.Lstat(container); !os.IsNotExist(err) {
		t.Fatalf("residue container still at transaction root: %v", err)
	}
	outer := findResidueContainer(t, txRoot)
	if outer == "" {
		t.Fatal("empty residue container was not preserved in a fresh container")
	}
	assertResidueMeta(t, outer)
	if info, err := os.Stat(filepath.Join(outer, quarantinePayloadName)); err != nil || !info.IsDir() {
		t.Fatalf("moved residue payload missing or not a directory: %v", err)
	}
}

func TestRecoverRelocatesMetaOnlyContainerAndRequarantinesSource(t *testing.T) {
	root := t.TempDir()
	txRoot := filepath.Join(root, transactionsDirName)
	if err := os.MkdirAll(txRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	// Crash window between the META write and the payload rename: the source
	// entry is still at the transaction root and the container holds only its
	// META. Both survive: the source is re-quarantined fresh, the incomplete
	// container is moved intact (it may be the last record of that name).
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
		t.Fatalf("META-only residue container still at transaction root: %v", err)
	}
	outer := findResidueContainer(t, txRoot)
	if outer == "" {
		t.Fatal("META-only residue container was not preserved in a fresh container")
	}
	movedMeta, err := os.ReadFile(filepath.Join(outer, quarantinePayloadName, quarantineMetaFileName))
	if err != nil {
		t.Fatalf("relocated container lost its META: %v", err)
	}
	if string(movedMeta) != string(quarantineMetaForTest("stale-debris")) {
		t.Fatalf("relocated META not byte-identical: %q", movedMeta)
	}
	assertResidueMeta(t, outer)
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

// findResidueContainer returns the quarantined-* container whose payload is
// a directory — i.e. the wholesale-moved remains of an interrupted
// allocation. Regular-file payloads belong to ordinary quarantine entries.
func findResidueContainer(t *testing.T, txRoot string) string {
	t.Helper()
	entries, err := os.ReadDir(txRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), quarantinedPrefix) {
			continue
		}
		payload := filepath.Join(txRoot, e.Name(), quarantinePayloadName)
		if info, err := os.Lstat(payload); err == nil && info.IsDir() {
			return filepath.Join(txRoot, e.Name())
		}
	}
	return ""
}

func assertResidueMeta(t *testing.T, container string) {
	t.Helper()
	meta, err := os.ReadFile(filepath.Join(container, quarantineMetaFileName))
	if err != nil {
		t.Fatalf("outer container missing META: %v", err)
	}
	if !strings.Contains(string(meta), "interrupted quarantine allocation") {
		t.Fatalf("META missing residue note: %q", meta)
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
