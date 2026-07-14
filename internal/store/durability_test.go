package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rtwsvj/hukou/internal/durablefs"
)

type recordingDurableFS struct {
	base          durablefs.FileSystem
	calls         []string
	failAt        string
	failPath      string
	failAfterLink bool
	failedLink    bool
}

func (fs *recordingDurableFS) record(name, path string) error {
	fs.calls = append(fs.calls, name+":"+path)
	if name == fs.failAt && (fs.failPath == "" || path == fs.failPath) {
		return errors.New("injected durability failure")
	}
	return nil
}

func (fs *recordingDurableFS) SyncFile(file *os.File) error {
	if err := fs.record("sync-file", file.Name()); err != nil {
		return err
	}
	return fs.base.SyncFile(file)
}

func (fs *recordingDurableFS) SyncDir(path string) error {
	if err := fs.record("sync-dir", path); err != nil {
		return err
	}
	return fs.base.SyncDir(path)
}

func (fs *recordingDurableFS) Mkdir(path string, mode os.FileMode) error {
	if err := fs.record("mkdir", path); err != nil {
		return err
	}
	return fs.base.Mkdir(path, mode)
}

func (fs *recordingDurableFS) MkdirAll(path string, mode os.FileMode) error {
	if err := fs.record("mkdir-all", path); err != nil {
		return err
	}
	return fs.base.MkdirAll(path, mode)
}

func (fs *recordingDurableFS) Rename(oldPath, newPath string) error {
	if err := fs.record("rename", oldPath+"->"+newPath); err != nil {
		return err
	}
	return fs.base.Rename(oldPath, newPath)
}

func (fs *recordingDurableFS) Link(oldPath, newPath string) error {
	if err := fs.record("link", oldPath+"->"+newPath); err != nil {
		return err
	}
	if fs.failAfterLink && !fs.failedLink {
		if err := os.Link(oldPath, newPath); err != nil {
			return err
		}
		fs.failedLink = true
		return errors.New("injected failure after visible link")
	}
	return fs.base.Link(oldPath, newPath)
}

func (fs *recordingDurableFS) Remove(path string) error {
	if err := fs.record("remove", path); err != nil {
		return err
	}
	return fs.base.Remove(path)
}

func (fs *recordingDurableFS) RemoveAll(path string) error {
	if err := fs.record("remove-all", path); err != nil {
		return err
	}
	return fs.base.RemoveAll(path)
}

func TestPutPersistsDataBeforeLinkAndCleanup(t *testing.T) {
	root := t.TempDir()
	srcDir := t.TempDir()
	src := filepath.Join(srcDir, "tool")
	if err := os.WriteFile(src, []byte("body"), 0o755); err != nil {
		t.Fatal(err)
	}
	recorder := &recordingDurableFS{}
	s := &Store{Root: root, fs: recorder}
	if err := s.Put("tool", "v1.0.0", src); err != nil {
		t.Fatal(err)
	}

	syncIndex := callIndex(recorder.calls, "sync-file:")
	linkIndex := callIndex(recorder.calls, "link:")
	removeIndex := callIndex(recorder.calls, "remove:")
	if syncIndex < 0 || linkIndex < 0 || removeIndex < 0 {
		t.Fatalf("missing durable operations: %v", recorder.calls)
	}
	if !(syncIndex < linkIndex && linkIndex < removeIndex) {
		t.Fatalf("operation order=%v", recorder.calls)
	}
}

func TestPutSyncFailureDoesNotCommitVersion(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(t.TempDir(), "tool")
	if err := os.WriteFile(src, []byte("body"), 0o755); err != nil {
		t.Fatal(err)
	}
	recorder := &recordingDurableFS{failAt: "sync-file"}
	s := &Store{Root: root, fs: recorder}
	err := s.Put("tool", "v1.0.0", src)
	if err == nil || !strings.Contains(err.Error(), "injected durability failure") {
		t.Fatalf("error=%v", err)
	}
	if callIndex(recorder.calls, "link:") >= 0 {
		t.Fatalf("version linked after failed file sync: %v", recorder.calls)
	}
	if _, err := os.Lstat(filepath.Join(root, "tool", "v1.0.0")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("version directory unexpectedly committed: %v", err)
	}
}

func TestPutResumesEmptyVersionDirectory(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(t.TempDir(), "tool")
	if err := os.WriteFile(src, []byte("body"), 0o755); err != nil {
		t.Fatal(err)
	}
	versionDir := filepath.Join(root, "tool", "v1.0.0")
	if err := os.MkdirAll(versionDir, 0o755); err != nil {
		t.Fatal(err)
	}

	recorder := &recordingDurableFS{}
	s := &Store{Root: root, fs: recorder}
	if err := s.Put("tool", "v1.0.0", src); err != nil {
		t.Fatal(err)
	}
	if callIndex(recorder.calls, "link:") < 0 {
		t.Fatalf("empty version directory was not resumed: %v", recorder.calls)
	}
	got, err := os.ReadFile(filepath.Join(versionDir, "tool"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "body" {
		t.Fatalf("stored body=%q", got)
	}
}

func TestPutRetryReaffirmsLinkVisibleBeforeDirectorySync(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(t.TempDir(), "tool")
	if err := os.WriteFile(src, []byte("body"), 0o755); err != nil {
		t.Fatal(err)
	}
	recorder := &recordingDurableFS{failAfterLink: true}
	s := &Store{Root: root, fs: recorder}
	err := s.Put("tool", "v1.0.0", src)
	if err == nil || !strings.Contains(err.Error(), "injected failure after visible link") {
		t.Fatalf("error=%v", err)
	}
	dst := filepath.Join(root, "tool", "v1.0.0", "tool")
	if _, err := os.Lstat(dst); err != nil {
		t.Fatalf("link mutation was not visible: %v", err)
	}

	recorder.calls = nil
	recorder.failAfterLink = false
	if err := s.Put("tool", "v1.0.0", src); err != nil {
		t.Fatal(err)
	}
	syncFile := callIndex(recorder.calls, "sync-file:")
	syncDir := callIndex(recorder.calls, "sync-dir:"+filepath.Join(root, "tool", "v1.0.0"))
	if syncFile < 0 || syncDir < 0 || syncFile >= syncDir {
		t.Fatalf("retry did not reaffirm file then directory durability: %v", recorder.calls)
	}
	if callIndex(recorder.calls, "link:") >= 0 {
		t.Fatalf("retry attempted a second link instead of reaffirming the existing one: %v", recorder.calls)
	}
}

func TestPutExistingVersionDirectorySyncFailureCanRetry(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(t.TempDir(), "tool")
	if err := os.WriteFile(src, []byte("body"), 0o755); err != nil {
		t.Fatal(err)
	}
	s := &Store{Root: root}
	if err := s.Put("tool", "v1.0.0", src); err != nil {
		t.Fatal(err)
	}

	versionDir := filepath.Join(root, "tool", "v1.0.0")
	recorder := &recordingDurableFS{failAt: "sync-dir", failPath: versionDir}
	s.fs = recorder
	err := s.Put("tool", "v1.0.0", src)
	if err == nil || !strings.Contains(err.Error(), "injected durability failure") {
		t.Fatalf("error=%v", err)
	}
	recorder.calls = nil
	recorder.failAt = ""
	recorder.failPath = ""
	if err := s.Put("tool", "v1.0.0", src); err != nil {
		t.Fatal(err)
	}
	if callIndex(recorder.calls, "sync-dir:"+versionDir) < 0 {
		t.Fatalf("retry did not repeat directory sync: %v", recorder.calls)
	}
}

func TestVersionsKeepsExistingStoreLookupReadOnly(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(t.TempDir(), "tool")
	if err := os.WriteFile(src, []byte("body"), 0o755); err != nil {
		t.Fatal(err)
	}
	s := &Store{Root: root}
	if err := s.Put("tool", "v1.0.0", src); err != nil {
		t.Fatal(err)
	}

	recorder := &recordingDurableFS{}
	s.fs = recorder
	versions, err := s.Versions("tool")
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 1 || versions[0] != "v1.0.0" {
		t.Fatalf("versions=%v", versions)
	}
	if len(recorder.calls) != 0 {
		t.Fatalf("read-only Versions performed durability operations: %v", recorder.calls)
	}
}

func TestActivateSyncsTemporaryFileBeforeRename(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(t.TempDir(), "tool")
	if err := os.WriteFile(src, []byte("body"), 0o755); err != nil {
		t.Fatal(err)
	}
	s := &Store{Root: root}
	if err := s.Put("tool", "v1.0.0", src); err != nil {
		t.Fatal(err)
	}
	recorder := &recordingDurableFS{}
	s.fs = recorder
	live := filepath.Join(t.TempDir(), "tool")
	if err := s.Activate("tool", "v1.0.0", live); err != nil {
		t.Fatal(err)
	}
	syncIndex := callIndex(recorder.calls, "sync-file:")
	renameIndex := callIndex(recorder.calls, "rename:")
	if syncIndex < 0 || renameIndex < 0 || syncIndex >= renameIndex {
		t.Fatalf("operation order=%v", recorder.calls)
	}
}

func TestPruneAndGCPersistRemovals(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(t.TempDir(), "tool")
	if err := os.WriteFile(src, []byte("body"), 0o755); err != nil {
		t.Fatal(err)
	}
	s := &Store{Root: root}
	if err := s.Put("tool", "v1.0.0", src); err != nil {
		t.Fatal(err)
	}
	recorder := &recordingDurableFS{}
	s.fs = recorder
	if err := s.Prune("tool", 0, "", ""); err != nil {
		t.Fatal(err)
	}
	if callIndex(recorder.calls, "remove-all:") < 0 {
		t.Fatalf("Prune did not use durable removal: %v", recorder.calls)
	}
	recorder.calls = nil
	if err := s.GC(); err != nil {
		t.Fatal(err)
	}
	removeIndex := callIndex(recorder.calls, "remove-all:")
	mkdirIndex := callIndex(recorder.calls, "mkdir:")
	if removeIndex < 0 || mkdirIndex < 0 || removeIndex >= mkdirIndex {
		t.Fatalf("GC operation order=%v", recorder.calls)
	}
}

func callIndex(calls []string, prefix string) int {
	for i, call := range calls {
		if strings.HasPrefix(call, prefix) {
			return i
		}
	}
	return -1
}
