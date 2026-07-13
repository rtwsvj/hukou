package store_test

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

func TestPutRejectsReservedOriginalTag(t *testing.T) {
	root := t.TempDir()
	s := &store.Store{Root: root}
	src := writeFile(t, t.TempDir(), "mybin", "original-body")

	for _, tag := range []string{"original", "Original", "ORIGINAL"} {
		if err := s.Put("tool", tag, src); err == nil {
			t.Fatalf("Put tag=%s: expected reserved-tag error", tag)
		}
		if _, err := os.Stat(filepath.Join(root, "tool", tag, "mybin")); !os.IsNotExist(err) {
			t.Fatalf("Put tag=%s created a store entry: err=%v", tag, err)
		}
	}
}

func TestPutVersionIsImmutableAndIdempotent(t *testing.T) {
	root := t.TempDir()
	s := &store.Store{Root: root}

	first := writeFile(t, t.TempDir(), "mybin", "version-one")
	if err := os.Chmod(first, 0o751); err != nil {
		t.Fatalf("chmod first source: %v", err)
	}
	if err := s.Put("tool", "v1.0", first); err != nil {
		t.Fatalf("Put first: %v", err)
	}

	stored := filepath.Join(root, "tool", "v1.0", "mybin")
	sentinelTime := time.Date(2001, 2, 3, 4, 5, 6, 0, time.UTC)
	if err := os.Chtimes(stored, sentinelTime, sentinelTime); err != nil {
		t.Fatalf("set stored mtime: %v", err)
	}
	before, err := os.Stat(stored)
	if err != nil {
		t.Fatalf("stat stored before idempotent Put: %v", err)
	}
	beforeBody, err := os.ReadFile(stored)
	if err != nil {
		t.Fatalf("read stored before idempotent Put: %v", err)
	}

	// The same bytes are an idempotent success even when the incoming source
	// has a different mode. The immutable stored inode and metadata must not be
	// rewritten to match the new source.
	same := writeFile(t, t.TempDir(), "mybin", "version-one")
	if err := os.Chmod(same, 0o700); err != nil {
		t.Fatalf("chmod same-content source: %v", err)
	}
	if err := s.Put("tool", "v1.0", same); err != nil {
		t.Fatalf("idempotent Put: %v", err)
	}
	assertStoredVersionUnchanged(t, stored, before, beforeBody)

	// Different bytes for the same name/tag must fail closed and leave the
	// existing immutable version untouched.
	different := writeFile(t, t.TempDir(), "mybin", "version-two")
	if err := os.Chmod(different, 0o777); err != nil {
		t.Fatalf("chmod different-content source: %v", err)
	}
	if err := s.Put("tool", "v1.0", different); err == nil {
		t.Fatal("Put different content into existing version: expected error")
	}
	assertStoredVersionUnchanged(t, stored, before, beforeBody)
}

func assertStoredVersionUnchanged(t *testing.T, path string, before os.FileInfo, wantBody []byte) {
	t.Helper()
	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat immutable version after Put: %v", err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("immutable version was replaced with a different file")
	}
	if after.Mode() != before.Mode() {
		t.Fatalf("immutable version mode changed: got %v, want %v", after.Mode(), before.Mode())
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Fatalf("immutable version mtime changed: got %v, want %v", after.ModTime(), before.ModTime())
	}
	gotBody, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read immutable version after Put: %v", err)
	}
	if string(gotBody) != string(wantBody) {
		t.Fatalf("immutable version content changed: got %q, want %q", gotBody, wantBody)
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
		if info, err := os.Lstat(linkPath); err != nil || !info.Mode().IsRegular() {
			t.Fatalf("live path is not a regular file")
		} else if info.Mode()&0o111 == 0 {
			t.Fatalf("live path is not executable: mode=%v", info.Mode())
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

	// The live original remains a regular file and unchanged.
	info, err := os.Lstat(binPath)
	if err != nil {
		t.Fatalf("lstat binPath: %v", err)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("binPath is not a regular file after adoption")
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

	// The unchanged live path still has the same content.
	got, err = os.ReadFile(binPath)
	if err != nil {
		t.Fatalf("read live original: %v", err)
	}
	if string(got) != "original-body" {
		t.Fatalf("live content = %q, want %q", got, "original-body")
	}
	if err := os.WriteFile(binPath, []byte("changed-live"), 0o755); err != nil {
		t.Fatalf("change live original: %v", err)
	}
	got, err = os.ReadFile(backup)
	if err != nil || string(got) != "original-body" {
		t.Fatalf("backup changed with live file: content=%q err=%v", got, err)
	}
}

func TestAdoptOriginalIsWriteOnce(t *testing.T) {
	root := t.TempDir()
	s := &store.Store{Root: root}
	first := writeFile(t, t.TempDir(), "mybin", "first")
	second := writeFile(t, t.TempDir(), "mybin", "second")
	if err := s.AdoptOriginal("mybin", first); err != nil {
		t.Fatalf("AdoptOriginal first: %v", err)
	}
	if err := s.AdoptOriginal("mybin", second); err == nil {
		t.Fatal("AdoptOriginal second: expected write-once error")
	}
	backup := filepath.Join(root, "mybin", "original", "mybin")
	got, err := os.ReadFile(backup)
	if err != nil || string(got) != "first" {
		t.Fatalf("original backup was replaced: content=%q err=%v", got, err)
	}
}

func TestAdoptOriginalRejectsSpecialModeBits(t *testing.T) {
	root := t.TempDir()
	s := &store.Store{Root: root}
	src := writeFile(t, t.TempDir(), "mybin", "privileged")
	if err := os.Chmod(src, 0o755|os.ModeSetuid); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(src)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSetuid == 0 {
		t.Skip("filesystem did not retain setuid bit")
	}
	if err := s.AdoptOriginal("mybin", src); err == nil {
		t.Fatal("AdoptOriginal special mode: expected rejection")
	}
	if _, err := os.Stat(filepath.Join(root, "mybin", "original", "mybin")); !os.IsNotExist(err) {
		t.Fatalf("special-mode source was backed up: err=%v", err)
	}
}

func TestActivateDoesNotAliasStore(t *testing.T) {
	root := t.TempDir()
	s := &store.Store{Root: root}
	src := writeFile(t, t.TempDir(), "mybin", "stored")
	if err := s.Put("tool", "v1.0", src); err != nil {
		t.Fatalf("Put: %v", err)
	}
	live := filepath.Join(t.TempDir(), "mybin")
	if err := s.Activate("tool", "v1.0", live); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if err := os.WriteFile(live, []byte("changed-live"), 0o755); err != nil {
		t.Fatalf("change live: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(root, "tool", "v1.0", "mybin"))
	if err != nil || string(got) != "stored" {
		t.Fatalf("store version changed with live file: content=%q err=%v", got, err)
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
	if err := s.Prune("mybin", 2, "", ""); err != nil {
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

func TestActivateAtomicReplace(t *testing.T) {
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
	observed := make(chan struct{})
	var observedOnce sync.Once

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}

			got, err := os.ReadFile(linkPath)
			if err != nil {
				errs <- "read live path: " + err.Error()
				return
			}
			if string(got) != "v1" && string(got) != "v2" {
				errs <- fmt.Sprintf("unexpected live content: %q", got)
				return
			}
			observedOnce.Do(func() { close(observed) })
		}
	}()

	select {
	case <-observed:
	case <-time.After(2 * time.Second):
		close(stop)
		wg.Wait()
		t.Fatal("concurrent observer did not read the initial live file")
	}

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

func TestPruneProtectsActiveVersion(t *testing.T) {
	root := t.TempDir()
	s := &store.Store{Root: root}

	tags := []string{"v1.0", "v2.0", "v3.0", "v4.0"}
	for i, tag := range tags {
		src := writeFile(t, t.TempDir(), "mybin", tag)
		if err := s.Put("tool", tag, src); err != nil {
			t.Fatalf("Put %s: %v", tag, err)
		}
		mtime := time.Date(2020, 1, 1+i, 0, 0, 0, 0, time.UTC)
		if err := os.Chtimes(filepath.Join(root, "tool", tag), mtime, mtime); err != nil {
			t.Fatalf("Chtimes: %v", err)
		}
	}

	linkPath := filepath.Join(t.TempDir(), "mybin")
	// Activate the oldest version so it would be pruned without protection.
	if err := s.Activate("tool", "v1.0", linkPath); err != nil {
		t.Fatalf("Activate: %v", err)
	}

	// keep=1 would normally leave only v4.0; active v1.0 must survive.
	protectedSHA, err := store.SHA256File(linkPath)
	if err != nil {
		t.Fatalf("hash protected live version: %v", err)
	}
	if err := s.Prune("tool", 1, "v1.0", protectedSHA); err != nil {
		t.Fatalf("Prune: %v", err)
	}

	versions, err := s.Versions("tool")
	if err != nil {
		t.Fatalf("Versions: %v", err)
	}
	found := map[string]bool{}
	for _, v := range versions {
		found[v] = true
	}
	if !found["v1.0"] {
		t.Fatalf("active version v1.0 was pruned: %v", versions)
	}
	if !found["v4.0"] {
		t.Fatalf("newest version v4.0 missing: %v", versions)
	}
	if found["v2.0"] || found["v3.0"] {
		t.Fatalf("expected only active+newest, got %v", versions)
	}

	got, err := os.ReadFile(linkPath)
	if err != nil {
		t.Fatalf("read active: %v", err)
	}
	if string(got) != "v1.0" {
		t.Fatalf("active content = %q", got)
	}
}

func TestPruneProtectedSHAMismatchDeletesNothing(t *testing.T) {
	root := t.TempDir()
	s := &store.Store{Root: root}

	tags := []string{"v1.0", "v2.0", "v3.0"}
	for i, tag := range tags {
		src := writeFile(t, t.TempDir(), "mybin", tag)
		if err := s.Put("tool", tag, src); err != nil {
			t.Fatalf("Put %s: %v", tag, err)
		}
		mtime := time.Date(2020, 1, 1+i, 0, 0, 0, 0, time.UTC)
		if err := os.Chtimes(filepath.Join(root, "tool", tag), mtime, mtime); err != nil {
			t.Fatalf("Chtimes %s: %v", tag, err)
		}
	}

	// keep=0 would delete every unprotected version. A mismatched binding for
	// the protected tag must abort before the first deletion happens.
	wrongSHA := strings.Repeat("0", sha256.Size*2)
	if err := s.Prune("tool", 0, "v1.0", wrongSHA); err == nil {
		t.Fatal("Prune with mismatched protected SHA: expected error")
	}

	versions, err := s.Versions("tool")
	if err != nil {
		t.Fatalf("Versions after rejected Prune: %v", err)
	}
	if strings.Join(versions, ",") != strings.Join(tags, ",") {
		t.Fatalf("Prune deleted versions before protected SHA validation: got %v, want %v", versions, tags)
	}
	for _, tag := range tags {
		got, err := os.ReadFile(filepath.Join(root, "tool", tag, "mybin"))
		if err != nil {
			t.Fatalf("read %s after rejected Prune: %v", tag, err)
		}
		if string(got) != tag {
			t.Fatalf("version %s changed after rejected Prune: got %q", tag, got)
		}
	}
}

func TestReservedTmpNameRejectedByAllStoreEntryPoints(t *testing.T) {
	root := t.TempDir()
	s := &store.Store{Root: root}
	src := writeFile(t, t.TempDir(), "mybin", "body")
	live := filepath.Join(t.TempDir(), "mybin")

	for _, reservedName := range []string{".tmp", ".TMP", ".Tmp"} {
		tests := []struct {
			name string
			call func() error
		}{
			{name: "Put", call: func() error { return s.Put(reservedName, "v1.0", src) }},
			{name: "Activate", call: func() error { return s.Activate(reservedName, "v1.0", live) }},
			{name: "AdoptOriginal", call: func() error { return s.AdoptOriginal(reservedName, src) }},
			{name: "Versions", call: func() error { _, err := s.Versions(reservedName); return err }},
			{name: "Prune", call: func() error { return s.Prune(reservedName, 1, "", "") }},
		}
		for _, tt := range tests {
			t.Run(reservedName+"/"+tt.name, func(t *testing.T) {
				if err := tt.call(); err == nil {
					t.Fatalf("%s name=%s: expected reserved-name error", tt.name, reservedName)
				}
			})
		}
	}
}

func TestStoreRejectsSymlinkedInternalDirectories(t *testing.T) {
	src := writeFile(t, t.TempDir(), "mybin", "body")

	t.Run("name", func(t *testing.T) {
		root := t.TempDir()
		outside := t.TempDir()
		if err := os.Symlink(outside, filepath.Join(root, "tool")); err != nil {
			t.Fatal(err)
		}
		s := &store.Store{Root: root}
		if err := s.Put("tool", "v1", src); err == nil {
			t.Fatal("Put followed symlinked name directory")
		}
		if err := s.AdoptOriginal("tool", src); err == nil {
			t.Fatal("AdoptOriginal followed symlinked name directory")
		}
		if _, err := s.Versions("tool"); err == nil {
			t.Fatal("Versions followed symlinked name directory")
		}
		outsideVersion := filepath.Join(outside, "v1")
		if err := os.MkdirAll(outsideVersion, 0o755); err != nil {
			t.Fatal(err)
		}
		outsideBinary := filepath.Join(outsideVersion, "mybin")
		if err := os.WriteFile(outsideBinary, []byte("outside"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := s.Prune("tool", 0, "", ""); err == nil {
			t.Fatal("Prune followed symlinked name directory")
		}
		if got, err := os.ReadFile(outsideBinary); err != nil || string(got) != "outside" {
			t.Fatalf("Prune changed data outside store: content=%q err=%v", got, err)
		}
	})

	t.Run("tag", func(t *testing.T) {
		root := t.TempDir()
		outside := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, "tool"), 0o755); err != nil {
			t.Fatal(err)
		}
		outsideBinary := filepath.Join(outside, "mybin")
		if err := os.WriteFile(outsideBinary, []byte("outside"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(root, "tool", "v1")); err != nil {
			t.Fatal(err)
		}
		s := &store.Store{Root: root}
		if err := s.Put("tool", "v1", src); err == nil {
			t.Fatal("Put followed symlinked tag directory")
		}
		if err := s.Activate("tool", "v1", filepath.Join(t.TempDir(), "live")); err == nil {
			t.Fatal("Activate followed symlinked tag directory")
		}
		if got, err := os.ReadFile(outsideBinary); err != nil || string(got) != "outside" {
			t.Fatalf("tag operation changed data outside store: content=%q err=%v", got, err)
		}
	})

	t.Run("original", func(t *testing.T) {
		root := t.TempDir()
		outside := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, "tool"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(root, "tool", "original")); err != nil {
			t.Fatal(err)
		}
		if err := (&store.Store{Root: root}).AdoptOriginal("tool", src); err == nil {
			t.Fatal("AdoptOriginal followed symlinked original directory")
		}
		if entries, err := os.ReadDir(outside); err != nil || len(entries) != 0 {
			t.Fatalf("original operation wrote outside store: entries=%v err=%v", entries, err)
		}
	})

	t.Run("tmp", func(t *testing.T) {
		root := t.TempDir()
		outside := t.TempDir()
		if err := os.Symlink(outside, filepath.Join(root, ".tmp")); err != nil {
			t.Fatal(err)
		}
		s := &store.Store{Root: root}
		if err := s.Put("tool", "v1", src); err == nil {
			t.Fatal("Put followed symlinked staging directory")
		}
		if err := s.GC(); err == nil {
			t.Fatal("GC accepted symlinked staging directory")
		}
		if entries, err := os.ReadDir(outside); err != nil || len(entries) != 0 {
			t.Fatalf("staging operation changed outside directory: entries=%v err=%v", entries, err)
		}
	})
}

func TestStoreRejectsCaseAliases(t *testing.T) {
	root := t.TempDir()
	s := &store.Store{Root: root}
	src := writeFile(t, t.TempDir(), "mybin", "body")
	if err := s.Put("tool", "v1", src); err != nil {
		t.Fatal(err)
	}
	if err := s.Put("tool", "V1", src); err == nil {
		t.Fatal("Put accepted case alias for existing tag")
	}
	if err := s.Put("Tool", "v2", src); err == nil {
		t.Fatal("Put accepted case alias for existing tool name")
	}
}

func TestValidateNameTagRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	s := &store.Store{Root: root}
	src := writeFile(t, t.TempDir(), "bin", "x")

	for _, bad := range []string{"../escape", "a/b", "a\\b", "..", ""} {
		if err := s.Put(bad, "v1", src); err == nil {
			t.Fatalf("Put name %q: expected error", bad)
		}
		if err := s.Put("tool", bad, src); err == nil {
			t.Fatalf("Put tag %q: expected error", bad)
		}
		if err := s.Activate(bad, "v1", filepath.Join(t.TempDir(), "l")); err == nil {
			t.Fatalf("Activate name %q: expected error", bad)
		}
		if err := s.AdoptOriginal(bad, src); err == nil {
			t.Fatalf("AdoptOriginal name %q: expected error", bad)
		}
	}
}

func TestActivateTmpFileSameDir(t *testing.T) {
	root := t.TempDir()
	s := &store.Store{Root: root}
	src := writeFile(t, t.TempDir(), "mybin", "v1")
	if err := s.Put("tool", "v1.0", src); err != nil {
		t.Fatalf("Put: %v", err)
	}
	linkDir := t.TempDir()
	linkPath := filepath.Join(linkDir, "mybin")
	if err := s.Activate("tool", "v1.0", linkPath); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	// No leftover .hukou-tmp-* in linkDir.
	entries, err := os.ReadDir(linkDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".hukou-tmp-") {
			t.Fatalf("leftover activation temp file: %s", e.Name())
		}
	}
	if len(entries) != 1 || entries[0].Name() != "mybin" {
		t.Fatalf("linkDir entries = %v", entries)
	}
}
