package scan

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
)

func TestIsExecutable(t *testing.T) {
	if !IsExecutable(0o755) {
		t.Fatal("0o755 should be executable")
	}
	if !IsExecutable(0o111) {
		t.Fatal("0o111 should be executable")
	}
	if IsExecutable(0o644) {
		t.Fatal("0o644 should not be executable")
	}
	if IsExecutable(0o600) {
		t.Fatal("0o600 should not be executable")
	}
}

func TestDetectKind(t *testing.T) {
	dir := t.TempDir()

	cases := []struct {
		name string
		hdr  []byte
		want BinKind
	}{
		{"macho64", u32be(0xfeedfacf), KindMachO},
		{"macho32", u32be(0xfeedface), KindMachO},
		// fat: magic + nfat_arch=1 (must be ≤128)
		{"macho_fat", append(u32be(0xcafebabe), u32be(1)...), KindMachO},
		{"macho_fat_swap", append(u32be(0xbebafeca), u32le(1)...), KindMachO},
		{"macho_fat64", append(u32be(0xcafebabf), u32be(2)...), KindMachO},
		{"macho_fat64_swap", append(u32be(0xbfbafeca), u32le(1)...), KindMachO},
		{"macho64_le", u32le(0xfeedfacf), KindMachO},
		{"elf", []byte{0x7f, 'E', 'L', 'F'}, KindELF},
		{"script", []byte("#!/bin/sh\necho hi\n"), KindScript},
		{"other", []byte("PK\x03\x04"), KindOther},
		// nfat_arch > 128 → Other (Java-class disambiguation)
		{"java_like", append(u32be(0xcafebabe), u32be(200)...), KindOther},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, tc.name)
			if err := os.WriteFile(path, tc.hdr, 0o755); err != nil {
				t.Fatal(err)
			}
			got, err := DetectKind(path)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("DetectKind(%s)=%s want %s", tc.name, got, tc.want)
			}
		})
	}
}

func TestDetectKind_largeFileOnlyHeader(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big")
	// 1 MiB payload with script shebang at start — must not hang or load all.
	buf := make([]byte, 1<<20)
	copy(buf, []byte("#!/usr/bin/env bash\n"))
	if err := os.WriteFile(path, buf, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := DetectKind(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != KindScript {
		t.Fatalf("got %s want Script", got)
	}
}

func TestDetectKind_fatMagic64(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fat64")
	hdr := append(u32be(0xcafebabf), u32be(1)...)
	if err := os.WriteFile(path, hdr, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := DetectKind(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != KindMachO {
		t.Fatalf("got %s want MachO", got)
	}
	// swap form
	path2 := filepath.Join(dir, "fat64swap")
	hdr2 := append(u32be(0xbfbafeca), u32le(1)...)
	if err := os.WriteFile(path2, hdr2, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err = DetectKind(path2)
	if err != nil {
		t.Fatal(err)
	}
	if got != KindMachO {
		t.Fatalf("swap got %s want MachO", got)
	}
}

func TestDetectKind_javaClassDisambiguation(t *testing.T) {
	dir := t.TempDir()
	// nfat_arch = 129 → Other
	path := filepath.Join(dir, "java")
	hdr := append(u32be(0xcafebabe), u32be(129)...)
	if err := os.WriteFile(path, hdr, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := DetectKind(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != KindOther {
		t.Fatalf("got %s want Other", got)
	}
	// nfat_arch = 128 → still MachO (boundary)
	path2 := filepath.Join(dir, "fat128")
	hdr2 := append(u32be(0xcafebabe), u32be(128)...)
	if err := os.WriteFile(path2, hdr2, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err = DetectKind(path2)
	if err != nil {
		t.Fatal(err)
	}
	if got != KindMachO {
		t.Fatalf("boundary 128 got %s want MachO", got)
	}
}

func TestWalk_fixture(t *testing.T) {
	// Layout:
	//   a/foo       executable script (active)
	//   a/bar       non-executable (ignored)
	//   b/foo       executable (shadowed)
	//   b/linky -> a/foo  symlink to executable
	//   b/macho     fake Mach-O header
	root := t.TempDir()
	dirA := filepath.Join(root, "a")
	dirB := filepath.Join(root, "b")
	if err := os.MkdirAll(dirA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dirB, 0o755); err != nil {
		t.Fatal(err)
	}

	fooA := filepath.Join(dirA, "foo")
	if err := os.WriteFile(fooA, []byte("#!/bin/sh\necho foo\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	barA := filepath.Join(dirA, "bar")
	if err := os.WriteFile(barA, []byte("not exec"), 0o644); err != nil {
		t.Fatal(err)
	}
	fooB := filepath.Join(dirB, "foo")
	if err := os.WriteFile(fooB, []byte("#!/bin/sh\necho shadow\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	linky := filepath.Join(dirB, "linky")
	if err := os.Symlink(fooA, linky); err != nil {
		t.Fatal(err)
	}
	macho := filepath.Join(dirB, "macho")
	if err := os.WriteFile(macho, u32be(0xfeedfacf), 0o755); err != nil {
		t.Fatal(err)
	}

	res, err := Walk([]string{dirA, dirB})
	if err != nil {
		t.Fatal(err)
	}

	byName := map[string][]Binary{}
	for _, b := range res.Binaries {
		byName[b.Name] = append(byName[b.Name], b)
	}

	// bar is non-executable → absent
	if _, ok := byName["bar"]; ok {
		t.Fatalf("non-executable bar should be absent: %+v", byName["bar"])
	}

	// foo: two entries, first not shadowed, second shadowed
	foos := byName["foo"]
	if len(foos) != 2 {
		t.Fatalf("foo count=%d want 2: %+v", len(foos), foos)
	}
	if foos[0].Shadowed {
		t.Fatal("first foo must not be shadowed")
	}
	if foos[0].Path != fooA {
		t.Fatalf("first foo path=%s want %s", foos[0].Path, fooA)
	}
	if foos[0].Kind != KindScript {
		t.Fatalf("first foo kind=%s want Script", foos[0].Kind)
	}
	if !foos[1].Shadowed {
		t.Fatal("second foo must be shadowed")
	}
	if foos[1].Path != fooB {
		t.Fatalf("second foo path=%s want %s", foos[1].Path, fooB)
	}

	// symlink
	links := byName["linky"]
	if len(links) != 1 {
		t.Fatalf("linky count=%d want 1", len(links))
	}
	if links[0].Kind != KindScript {
		t.Fatalf("linky kind=%s want Script", links[0].Kind)
	}
	if links[0].RealPath == "" {
		t.Fatal("linky RealPath should be resolved")
	}
	wantReal, _ := filepath.EvalSymlinks(fooA)
	if links[0].RealPath != wantReal {
		if links[0].RealPath != fooA {
			t.Fatalf("linky RealPath=%s want %s or %s", links[0].RealPath, wantReal, fooA)
		}
	}

	// macho header
	ms := byName["macho"]
	if len(ms) != 1 || ms[0].Kind != KindMachO {
		t.Fatalf("macho: %+v", ms)
	}
}

// Fix #1: exec-only (no read) regular file is still recorded, occupies seen slot.
func TestWalk_execOnlyUnreadable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod semantics differ on windows")
	}
	root := t.TempDir()
	dirA := filepath.Join(root, "a")
	dirB := filepath.Join(root, "b")
	for _, d := range []string{dirA, dirB} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// First on PATH: execute-only unreadable
	secret := filepath.Join(dirA, "tool")
	if err := os.WriteFile(secret, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(secret, 0o111); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(secret, 0o755) })

	// Probe whether Open fails for owner with 0o111 (macOS may still allow read).
	if f, err := os.Open(secret); err == nil {
		f.Close()
		t.Skip("platform allows owner read on mode 0111; cannot test unreadable path")
	}

	// Second on PATH: same name, readable — must be Shadowed=true
	later := filepath.Join(dirB, "tool")
	if err := os.WriteFile(later, []byte("#!/bin/sh\necho later\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	res, err := Walk([]string{dirA, dirB})
	if err != nil {
		t.Fatal(err)
	}
	var tools []Binary
	for _, b := range res.Binaries {
		if b.Name == "tool" {
			tools = append(tools, b)
		}
	}
	if len(tools) != 2 {
		t.Fatalf("want 2 tool entries (unreadable still recorded), got %d: %+v", len(tools), tools)
	}
	if tools[0].Shadowed {
		t.Fatal("first (unreadable) tool must not be shadowed")
	}
	if tools[0].Kind != KindOther {
		t.Fatalf("unreadable kind=%s want Other", tools[0].Kind)
	}
	if tools[0].Evidence == "" || !strings.Contains(tools[0].Evidence, "unreadable") {
		t.Fatalf("want unreadable evidence, got %q", tools[0].Evidence)
	}
	if !tools[1].Shadowed {
		t.Fatal("second tool must be shadowed (first occupied seen slot)")
	}
}

// Fix #2: FIFO must not be opened; skipped with file error detail.
func TestWalk_skipFIFO(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no fifo on windows")
	}
	dir := t.TempDir()
	fifo := filepath.Join(dir, "pipey")
	if err := syscall.Mkfifo(fifo, 0o755); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}
	// Ensure +x is visible via Stat (mode from mkfifo).
	if err := os.Chmod(fifo, 0o755); err != nil {
		t.Fatal(err)
	}

	res, err := Walk([]string{dir})
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range res.Binaries {
		if b.Name == "pipey" {
			t.Fatalf("FIFO must not be listed as binary: %+v", b)
		}
	}
	if res.Skipped < 1 {
		t.Fatalf("expected Skipped>=1 for FIFO, got %d", res.Skipped)
	}
	found := false
	for _, fe := range res.FileErrors {
		if fe.Path == fifo && strings.Contains(fe.Reason, "non-regular") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected FileError for FIFO, got %+v", res.FileErrors)
	}
}

// Fix #3: PATH directory-level dedup via EvalSymlinks / SameFile.
func TestWalk_dirDedupSymlink(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(real, "once")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}

	res, err := Walk([]string{real, link})
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, b := range res.Binaries {
		if b.Name == "once" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("symlink-dup dir should scan once, got %d entries", count)
	}
	// Errors should mention duplicate skip
	joined := strings.Join(res.Errors, "\n")
	if !strings.Contains(joined, "duplicate") {
		t.Fatalf("expected duplicate skip diagnostic, got %v", res.Errors)
	}
}

// Fix #3 (case-insensitive volume): same dir via different case should dedup when SameFile.
func TestWalk_dirDedupCaseFold(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "BinDir")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tool"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Probe case-insensitivity: open alternate case path.
	alt := filepath.Join(root, "bindir")
	if _, err := os.Stat(alt); err != nil {
		t.Skip("volume is case-sensitive; skipping case-fold dir dedup test")
	}
	// Confirm SameFile would see them as equal
	info1, err1 := os.Stat(dir)
	info2, err2 := os.Stat(alt)
	if err1 != nil || err2 != nil || !os.SameFile(info1, info2) {
		t.Skip("alternate case path not same file; skip")
	}

	res, err := Walk([]string{dir, alt})
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, b := range res.Binaries {
		if b.Name == "tool" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("case-fold dup dir should scan once, got %d", count)
	}
}

// Fix #4: relative PATH segments Abs-normalized; empty segment → warning.
func TestSplitPATHWithWarnings(t *testing.T) {
	dirs, warns := SplitPATHWithWarnings("/a::/b")
	if len(dirs) != 2 || dirs[0] != "/a" || dirs[1] != "/b" {
		t.Fatalf("dirs=%#v", dirs)
	}
	if len(warns) != 1 {
		t.Fatalf("want 1 empty-segment warning, got %v", warns)
	}
	if !strings.Contains(warns[0], "empty PATH segment") {
		t.Fatalf("warning text: %s", warns[0])
	}
	if !strings.Contains(warns[0], "POSIX") {
		t.Fatalf("warning should mention POSIX deviation: %s", warns[0])
	}

	// Relative segment → Abs
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dirs, _ = SplitPATHWithWarnings("relseg")
	if len(dirs) != 1 {
		t.Fatalf("dirs=%#v", dirs)
	}
	if !filepath.IsAbs(dirs[0]) {
		t.Fatalf("relative segment should be Abs, got %s", dirs[0])
	}
	if dirs[0] != filepath.Join(cwd, "relseg") {
		// Abs may clean; compare cleaned
		want, _ := filepath.Abs("relseg")
		if dirs[0] != want {
			t.Fatalf("got %s want %s", dirs[0], want)
		}
	}

	// SplitPATH still drops empties without exposing warnings
	got := SplitPATH("/a::/b")
	if len(got) != 2 {
		t.Fatalf("SplitPATH got %#v", got)
	}
	if SplitPATH("") != nil {
		t.Fatal("empty PATH should yield nil")
	}
}

func TestWalk_relativeDirAbs(t *testing.T) {
	// Walk itself Abs-normalizes relative dir arguments.
	root := t.TempDir()
	// Create a subdir relative to cwd is hard; use chdir into temp.
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	if err := os.MkdirAll("bins", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("bins", "x"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	res, err := Walk([]string{"bins"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Binaries) != 1 {
		t.Fatalf("want 1 binary, got %+v", res.Binaries)
	}
	if !filepath.IsAbs(res.Binaries[0].Path) {
		t.Fatalf("path should be absolute after Walk: %s", res.Binaries[0].Path)
	}
}

func u32be(v uint32) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, v)
	return b
}

func u32le(v uint32) []byte {
	b := make([]byte, 4)
	binary.LittleEndian.PutUint32(b, v)
	return b
}
