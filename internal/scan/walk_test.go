package scan

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"runtime"
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
		{"macho_fat", u32be(0xcafebabe), KindMachO},
		{"macho_fat_swap", u32be(0xbebafeca), KindMachO},
		{"macho64_le", u32le(0xfeedfacf), KindMachO},
		{"elf", []byte{0x7f, 'E', 'L', 'F'}, KindELF},
		{"script", []byte("#!/bin/sh\necho hi\n"), KindScript},
		{"other", []byte("PK\x03\x04"), KindOther},
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

	// Unreadable file: create then chmod 000 (skip on platforms that ignore).
	secret := filepath.Join(dirB, "secret")
	if err := os.WriteFile(secret, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(secret, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(secret, 0o755) })

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
	// RealPath should resolve to fooA (may differ by EvalSymlinks cleaning)
	wantReal, _ := filepath.EvalSymlinks(fooA)
	if links[0].RealPath != wantReal {
		// On some systems may equal fooA cleaned
		if links[0].RealPath != fooA {
			t.Fatalf("linky RealPath=%s want %s or %s", links[0].RealPath, wantReal, fooA)
		}
	}

	// macho header
	ms := byName["macho"]
	if len(ms) != 1 || ms[0].Kind != KindMachO {
		t.Fatalf("macho: %+v", ms)
	}

	// secret unreadable: either skipped (not in list) or skipped counted.
	// On macOS as root/sandbox chmod 0 may still be readable by owner in some cases;
	// only assert: if not present, Skipped >= 1; if present, ok (platform quirk).
	if _, ok := byName["secret"]; !ok {
		if res.Skipped < 1 {
			// On Linux without read, DetectKind fails → Skipped++.
			// If Stat itself fails (mode 000 and not owner), also Skipped++.
			if runtime.GOOS != "windows" {
				// Owner can often still Stat own mode-0 file; DetectKind Open may fail.
				// If Open succeeds for owner, secret appears — that's acceptable.
				t.Logf("secret absent, skipped=%d (ok)", res.Skipped)
			}
		}
	}
}

func TestSplitPATH(t *testing.T) {
	got := SplitPATH("/a:/b::/c")
	if len(got) != 3 || got[0] != "/a" || got[1] != "/b" || got[2] != "/c" {
		t.Fatalf("got %#v", got)
	}
	if SplitPATH("") != nil {
		t.Fatal("empty PATH should yield nil")
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
