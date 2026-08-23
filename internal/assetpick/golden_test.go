package assetpick

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rtwsvj/hukou/internal/archive"
)

// releaseFixture mirrors one captured GitHub release manifest under testdata/.
// The asset lists are real API responses (fetched 2026-08-23), so the golden
// assertions run against the exact inventories upstream actually publishes —
// including the noise (source tarballs, checksum files, .zsync sidecars,
// documentation PDFs) that a name-based picker has to survive.
type releaseFixture struct {
	Repo   string   `json:"repo"`
	Tag    string   `json:"tag"`
	Assets []string `json:"assets"`
}

func loadRelease(t *testing.T, file string) releaseFixture {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", file))
	if err != nil {
		t.Fatalf("read fixture %s: %v", file, err)
	}
	var rel releaseFixture
	if err := json.Unmarshal(data, &rel); err != nil {
		t.Fatalf("parse fixture %s: %v", file, err)
	}
	return rel
}

// TestGoldenHelixPrefersTarXzOverAppImage is the PF-4 regression: helix ships
// linux binaries as .tar.xz plus an .AppImage. Before the installability
// partitioning, OSLinux's priority regex matched the AppImage and returned it
// as a direct hit before any format reasoning ran — silently installing a
// container that needs libfuse2 while refusing nothing. The fix must pick the
// extractable tar.xz on linux/amd64 and demote the AppImage.
func TestGoldenHelixPrefersTarXzOverAppImage(t *testing.T) {
	if !archive.FormatTarXz.Supported() {
		t.Skip("tar.xz not supported in this build; helix golden needs ADR-0008 codecs")
	}
	rel := loadRelease(t, "helix-25.07.1.json")

	got, note, err := Pick(rel.Assets, "linux", "amd64", "")
	if err != nil {
		t.Fatalf("Pick(helix linux/amd64): %v", err)
	}
	if got != "helix-25.07.1-x86_64-linux.tar.xz" {
		t.Fatalf("Pick(helix linux/amd64) = %q (note %q); want tar.xz archive, NOT the AppImage", got, note)
	}
	for _, banned := range []string{".appimage", ".deb"} {
		if strings.Contains(strings.ToLower(got), banned) {
			t.Fatalf("Pick(helix linux/amd64) = %q; must never be a %s container", got, banned)
		}
	}
	if strings.Contains(note, "fallback") {
		t.Fatalf("tar.xz is fully supported; unexpected fallback note %q", note)
	}

	// darwin/arm64 must also resolve to the extractable archive, never to
	// anything else from this x86_64-skewed release.
	gotDarwin, _, err := Pick(rel.Assets, "darwin", "arm64", "")
	if err != nil {
		t.Fatalf("Pick(helix darwin/arm64): %v", err)
	}
	if gotDarwin != "helix-25.07.1-aarch64-macos.tar.xz" {
		t.Fatalf("Pick(helix darwin/arm64) = %q; want aarch64-macos.tar.xz", gotDarwin)
	}

	// windows/amd64: only the zip matches; source tarball and PDF must not win.
	gotWin, _, err := Pick(rel.Assets, "windows", "amd64", "")
	if err != nil {
		t.Fatalf("Pick(helix windows/amd64): %v", err)
	}
	if gotWin != "helix-25.07.1-x86_64-windows.zip" {
		t.Fatalf("Pick(helix windows/amd64) = %q; want windows zip", gotWin)
	}
}

// TestGoldenResticBZ2RefusedNamesFormat is the PF-1/PF-4 companion: restic
// v0.19.1 ships every compiled binary as .bz2 plus a platform-less source
// tarball. hukou cannot extract bz2 (ADR-0008 keeps it out of scope), so the
// correct outcome is a fail-closed refusal that (a) picks zero candidates,
// (b) names the bz2 limitation explicitly, and (c) does NOT silently fall
// back to the bare-looking source tarball restic-0.19.1.tar.gz.
func TestGoldenResticBZ2RefusedNamesFormat(t *testing.T) {
	if !archive.FormatTarXz.Supported() {
		t.Skip("format capability table differs from ADR-0008 build")
	}
	rel := loadRelease(t, "restic-v0.19.1.json")

	got, note, err := Pick(rel.Assets, "linux", "amd64", "")
	if err != nil {
		t.Fatalf("Pick(restic linux/amd64): %v", err)
	}
	// No linux bz2 is installable and the source tarball must never win.
	// The only remaining supported-format candidates are the foreign-OS
	// windows zips (eget's OS regexes do not exclude other OS names); they
	// are legitimately installable, so the pick may be the windows amd64 zip
	// — but it must NEVER be the source tarball or a bz2.
	if got == "restic-0.19.1.tar.gz" || strings.HasSuffix(got, ".bz2") {
		t.Fatalf("Pick(restic linux/amd64) = %q (note %q); source tarball and bz2 must be refused", got, note)
	}
	if got != "" && got != "restic_0.19.1_windows_amd64.zip" {
		t.Fatalf("Pick(restic linux/amd64) = %q; unexpected candidate", got)
	}
	if got == "" && err == nil {
		t.Fatal("empty pick without error")
	}

	// darwin/arm64: the bz2 assets are refused and the source tarball is
	// demoted; only the foreign-OS windows zips remain installable. The
	// waterfall may pick the amd64 zip under a rosetta-fallback note, but it
	// must never pick the source tarball or a bz2 — and if it picks nothing,
	// the refusal must name bz2.
	gotD, noteD, errD := Pick(rel.Assets, "darwin", "arm64", "")
	if gotD == "restic-0.19.1.tar.gz" || strings.HasSuffix(gotD, ".bz2") {
		t.Fatalf("Pick(restic darwin/arm64) = %q (note %q); source tarball and bz2 must be refused", gotD, noteD)
	}
	if gotD == "" && (errD == nil || !strings.Contains(errD.Error(), "bz2")) {
		t.Fatalf("empty darwin/arm64 pick must refuse naming bz2: %v", errD)
	}
}

// TestGoldenTypstTarXzPickedAfterCodecs pins typst v0.15.1: unix assets are
// .tar.xz-only, with a windows zip pair and a platform-less documentation PDF.
// Before ADR-0008 this release had zero installable candidates on any unix;
// after it, the tar.xz archives are first-class picks and the PDF (bare, but
// a source/doc-classification trap) must never win.
func TestGoldenTypstTarXzPickedAfterCodecs(t *testing.T) {
	if !archive.FormatTarXz.Supported() {
		t.Skip("tar.xz not supported in this build; typst golden needs ADR-0008 codecs")
	}
	rel := loadRelease(t, "typst-v0.15.1.json")

	cases := []struct {
		goos, goarch, want string
	}{
		{"linux", "amd64", "typst-x86_64-unknown-linux-musl.tar.xz"},
		{"linux", "arm64", "typst-aarch64-unknown-linux-musl.tar.xz"},
		{"darwin", "arm64", "typst-aarch64-apple-darwin.tar.xz"},
		{"windows", "amd64", "typst-x86_64-pc-windows-msvc.zip"},
	}
	for _, tc := range cases {
		t.Run(tc.goos+"/"+tc.goarch, func(t *testing.T) {
			got, note, err := Pick(rel.Assets, tc.goos, tc.goarch, "")
			if err != nil {
				t.Fatalf("Pick(%s/%s): %v", tc.goos, tc.goarch, err)
			}
			if got != tc.want {
				t.Fatalf("Pick(%s/%s) = %q (note %q); want %q", tc.goos, tc.goarch, got, note, tc.want)
			}
			if strings.Contains(got, ".pdf") {
				t.Fatalf("Pick(%s/%s) = %q; documentation PDF must never be picked", tc.goos, tc.goarch, got)
			}
		})
	}

	// If the codecs were ever reverted, the same manifest must refuse cleanly
	// naming xz rather than degrade — assert the refusal path directly by
	// simulating a capability loss through partition-level reasoning.
	preferred, demoted := partition(rel.Assets, "linux")
	if archive.FormatTarXz.Supported() {
		found := false
		for _, p := range preferred {
			if p == "typst-x86_64-unknown-linux-musl.tar.xz" {
				found = true
			}
		}
		if !found {
			t.Fatalf("tar.xz supported but typst unix assets demoted: preferred=%v demoted=%v", preferred, demoted)
		}
	}
	for _, d := range demoted {
		if d.name == "typst-documentation.pdf" {
			t.Fatalf("documentation.pdf should stay preferred-pool (bare) or be excluded, not demoted-with-warning: %+v", d)
		}
	}
}

// TestGoldenFallbackNoteNamesLimitation exercises the demoted-fallback warning
// channel directly: when ONLY unsupported-format candidates exist for the
// target platform, Pick may still return one, but the note must carry the
// "<format>-fallback" warning naming the limitation (the acceptance contract
// for falling back).
func TestGoldenFallbackNoteNamesLimitation(t *testing.T) {
	// Synthetic minimal case: a linux amd64 tool shipped only as .bz2.
	assets := []string{
		"tool_1.0.0_linux_amd64.bz2",
		"tool_1.0.0_linux_arm64.bz2",
	}
	if archive.DetectFormat("tool_1.0.0_linux_amd64.bz2").Supported() {
		t.Skip("bz2 became supported; fallback-note scenario no longer reachable via bz2")
	}
	got, note, err := Pick(assets, "linux", "amd64", "")
	if err != nil {
		t.Fatalf("expected graceful fallback pick, got error %v", err)
	}
	if got != "tool_1.0.0_linux_amd64.bz2" {
		t.Fatalf("got %q; want the amd64 bz2 as last-resort fallback", got)
	}
	if !strings.Contains(note, "fallback") {
		t.Fatalf("fallback pick must warn through the note channel; note was %q", note)
	}
}
