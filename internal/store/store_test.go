package store_test

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/rtwsvj/hukou/internal/store"
)

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(p, []byte(content), 0o755); err != nil {
		t.Fatalf("write file: %v", err)
	}
	return p
}

func TestPut(t *testing.T) {
	root := t.TempDir()
	s := &store.Store{Root: root}

	src := writeFile(t, t.TempDir(), "mybin", "version-one")
	if err := s.Put("tool", "v1.0", src); err != nil {
		t.Fatalf("Put: %v", err)
	}

	want := filepath.Join(root, "tool", "v1.0", "mybin")
	got, err := os.ReadFile(want)
	if err != nil {
		t.Fatalf("read stored file: %v", err)
	}
	if string(got) != "version-one" {
		t.Fatalf("stored content = %q, want %q", got, "version-one")
	}

	// second version under the same name keeps the first intact
	src2 := writeFile(t, t.TempDir(), "mybin", "version-two")
	if err := s.Put("tool", "v2.0", src2); err != nil {
		t.Fatalf("Put second: %v", err)
	}
	if got, err := os.ReadFile(want); err != nil || string(got) != "version-one" {
		t.Fatalf("first version damaged after second Put")
	}
}

func TestVersions(t *testing.T) {
	root := t.TempDir()
	s := &store.Store{Root: root}

	for _, tag := range []string{"v2.0", "v1.0", "v1.1"} {
		src := writeFile(t, t.TempDir(), "mybin", tag)
		if err := s.Put("tool", tag, src); err != nil {
			t.Fatalf("Put %s: %v", tag, err)
		}
	}

	versions, err := s.Versions("tool")
	if err != nil {
		t.Fatalf("Versions: %v", err)
	}
	want := []string{"v1.0", "v1.1", "v2.0"}
	if len(versions) != len(want) {
		t.Fatalf("Versions = %v, want %v", versions, want)
	}
	for i := range want {
		if versions[i] != want[i] {
			t.Fatalf("Versions = %v, want %v", versions, want)
		}
	}
}

func TestActivateSwitchAndRollback(t *testing.T) {
	root := t.TempDir()
	s := &store.Store{Root: root}

	v1 := writeFile(t, t.TempDir(), "mybin", "v1")
	v2 := writeFile(t, t.TempDir(), "mybin", "v2")
	if err := s.Put("tool", "v1.0", v1); err != nil {
		t.Fatalf("Put v1: %v", err)
	}
	if err := s.Put("tool", "v2.0", v2); err != nil {
		t.Fatalf("Put v2: %v", err)
	}

	linkPath := filepath.Join(t.TempDir(), "mybin")

	mustActivate := func(tag, wantContent string) {
		t.Helper()
		if err := s.Activate("tool", tag, linkPath); err != nil {
			t.Fatalf("Activate %s: %v", tag, err)
		}
		if info, err := os.Lstat(linkPath); err != nil || info.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("linkPath is not a symlink")
		}
		got, err := os.ReadFile(linkPath)
		if err != nil {
			t.Fatalf("read active link: %v", err)
		}
		if string(got) != wantContent {
			t.Fatalf("active content = %q, want %q", got, wantContent)
		}
	}

	mustActivate("v1.0", "v1")
	mustActivate("v2.0", "v2")
	mustActivate("v1.0", "v1") // rollback
}

func TestAdoptOriginal(t *testing.T) {
	root := t.TempDir()
	s := &store.Store{Root: root}

	binDir := t.TempDir()
	binPath := writeFile(t, binDir, "mybin", "original-body")

	if err := s.AdoptOriginal("mybin", binPath); err != nil {
		t.Fatalf("AdoptOriginal: %v", err)
	}

	// original path is now a symlink
	info, err := os.Lstat(binPath)
	if err != nil {
		t.Fatalf("lstat binPath: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("binPath is not a symlink after adoption")
	}

	// symlink points into the store's original directory
	target, err := os.Readlink(binPath)
	if err != nil {
		t.Fatalf("Readlink: %v", err)
	}
	wantTarget := filepath.Join(root, "mybin", "original", "mybin")
	if target != wantTarget {
		t.Fatalf("symlink target = %q, want %q", target, wantTarget)
	}

	// the backup file holds the original contents
	backup := filepath.Join(root, "mybin", "original", "mybin")
	got, err := os.ReadFile(backup)
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if string(got) != "original-body" {
		t.Fatalf("backup content = %q, want %q", got, "original-body")
	}
	if info, err := os.Lstat(backup); err != nil || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("backup is not a regular file")
	}

	// symlink resolves to the same content
	got, err = os.ReadFile(binPath)
	if err != nil {
		t.Fatalf("read through symlink: %v", err)
	}
	if string(got) != "original-body" {
		t.Fatalf("content through symlink = %q, want %q", got, "original-body")
	}
}

func TestPruneKeepsOriginal(t *testing.T) {
	root := t.TempDir()
	s := &store.Store{Root: root}

	// adopt an original binary so that original/ is populated
	binDir := t.TempDir()
	binPath := writeFile(t, binDir, "mybin", "original-body")
	if err := s.AdoptOriginal("mybin", binPath); err != nil {
		t.Fatalf("AdoptOriginal: %v", err)
	}

	// install several versions with controlled mtimes
	tags := []string{"v1.0", "v2.0", "v3.0"}
	for i, tag := range tags {
		src := writeFile(t, t.TempDir(), "mybin", tag)
		if err := s.Put("mybin", tag, src); err != nil {
			t.Fatalf("Put %s: %v", tag, err)
		}
		// spread mtimes apart deterministically
		mtime := time.Date(2020, 1, 1+i, 0, 0, 0, 0, time.UTC)
		if err := os.Chtimes(filepath.Join(root, "mybin", tag), mtime, mtime); err != nil {
			t.Fatalf("Chtimes: %v", err)
		}
	}

	// keep the two most recent versions (v3.0 and v2.0)
	if err := s.Prune("mybin", 2); err != nil {
		t.Fatalf("Prune: %v", err)
	}

	versions, err := s.Versions("mybin")
	if err != nil {
		t.Fatalf("Versions: %v", err)
	}
	want := []string{"v2.0", "v3.0"}
	if len(versions) != len(want) {
		t.Fatalf("Versions = %v, want %v", versions, want)
	}
	for i := range want {
		if versions[i] != want[i] {
			t.Fatalf("Versions = %v, want %v", versions, want)
		}
	}

	// original backup must survive pruning
	backup := filepath.Join(root, "mybin", "original", "mybin")
	if _, err := os.Stat(backup); err != nil {
		t.Fatalf("original backup removed by Prune: %v", err)
	}
}

func TestSymlinkAtomicReplace(t *testing.T) {
	root := t.TempDir()
	s := &store.Store{Root: root}

	v1 := writeFile(t, t.TempDir(), "mybin", "v1")
	v2 := writeFile(t, t.TempDir(), "mybin", "v2")
	if err := s.Put("tool", "v1.0", v1); err != nil {
		t.Fatalf("Put v1: %v", err)
	}
	if err := s.Put("tool", "v2.0", v2); err != nil {
		t.Fatalf("Put v2: %v", err)
	}

	linkPath := filepath.Join(t.TempDir(), "mybin")
	if err := s.Activate("tool", "v1.0", linkPath); err != nil {
		t.Fatalf("Activate initial: %v", err)
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})
	errs := make(chan string, 1000)

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}

			info, err := os.Lstat(linkPath)
			if err != nil {
				errs <- "lstat: " + err.Error()
				return
			}
			if info.Mode()&os.ModeSymlink == 0 {
				errs <- "linkPath is not a symlink"
				return
			}

			got, err := os.ReadFile(linkPath)
			if err != nil {
				errs <- "read: " + err.Error()
				return
			}
			content := string(got)
			if content != "v1" && content != "v2" {
				errs <- "unexpected content: " + content
				return
			}
		}
	}()

	for i := 0; i < 100; i++ {
		tag := "v1.0"
		if i%2 == 1 {
			tag = "v2.0"
		}
		if err := s.Activate("tool", tag, linkPath); err != nil {
			t.Fatalf("Activate iteration %d: %v", i, err)
		}
	}

	close(stop)
	wg.Wait()
	close(errs)

	for e := range errs {
		t.Fatalf("concurrent observer error: %s", e)
	}
}

func TestGC(t *testing.T) {
	root := t.TempDir()
	s := &store.Store{Root: root}

	tmpDir := filepath.Join(root, ".tmp")
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		t.Fatalf("mkdir .tmp: %v", err)
	}
	for i := 0; i < 3; i++ {
		name := fmt.Sprintf("junk-%d", i)
		if err := os.WriteFile(filepath.Join(tmpDir, name), []byte("x"), 0o644); err != nil {
			t.Fatalf("write junk: %v", err)
		}
	}

	if err := s.GC(); err != nil {
		t.Fatalf("GC: %v", err)
	}

	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		t.Fatalf("ReadDir .tmp: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("GC left %d entries in .tmp", len(entries))
	}
}

func TestSHA256File(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "data", "hello world")

	got, err := store.SHA256File(path)
	if err != nil {
		t.Fatalf("SHA256File: %v", err)
	}

	h := sha256.Sum256([]byte("hello world"))
	want := hex.EncodeToString(h[:])
	if got != want {
		t.Fatalf("SHA256File = %q, want %q", got, want)
	}
}
