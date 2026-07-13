package assetpick

import (
	"strings"
	"testing"
)

// Real-world release asset name lists, all evaluated from the darwin/arm64
// perspective.
var (
	fzfAssets = []string{
		"fzf-0.60.3-darwin_amd64.tar.gz",
		"fzf-0.60.3-darwin_arm64.tar.gz",
		"fzf-0.60.3-freebsd_amd64.tar.gz",
		"fzf-0.60.3-linux_amd64.tar.gz",
		"fzf-0.60.3-linux_arm64.tar.gz",
		"fzf-0.60.3-linux_armv5.tar.gz",
		"fzf-0.60.3-linux_armv6.tar.gz",
		"fzf-0.60.3-linux_armv7.tar.gz",
		"fzf-0.60.3-linux_loong64.tar.gz",
		"fzf-0.60.3-linux_ppc64le.tar.gz",
		"fzf-0.60.3-linux_s390x.tar.gz",
		"fzf-0.60.3-openbsd_amd64.tar.gz",
		"fzf-0.60.3-windows_amd64.zip",
		"fzf-0.60.3-windows_arm64.zip",
	}

	ghAssets = []string{
		"gh_2.63.2_checksums.txt",
		"gh_2.63.2_linux_386.deb",
		"gh_2.63.2_linux_386.rpm",
		"gh_2.63.2_linux_386.tar.gz",
		"gh_2.63.2_linux_amd64.deb",
		"gh_2.63.2_linux_amd64.tar.gz",
		"gh_2.63.2_linux_arm64.tar.gz",
		"gh_2.63.2_macOS_amd64.zip",
		"gh_2.63.2_macOS_arm64.zip",
		"gh_2.63.2_windows_amd64.msi",
		"gh_2.63.2_windows_amd64.zip",
		"gh_2.63.2_windows_arm64.zip",
	}

	lazygitAssets = []string{
		"lazygit_0.44.1_Darwin_arm64.tar.gz",
		"lazygit_0.44.1_Darwin_x86_64.tar.gz",
		"lazygit_0.44.1_Linux_32-bit.tar.gz",
		"lazygit_0.44.1_Linux_arm64.tar.gz",
		"lazygit_0.44.1_Linux_armv6.tar.gz",
		"lazygit_0.44.1_Linux_x86_64.tar.gz",
		"lazygit_0.44.1_Windows_arm64.zip",
		"lazygit_0.44.1_Windows_x86_64.zip",
		"lazygit_0.44.1_freebsd_x86_64.tar.gz",
		"checksums.txt",
	}

	ripgrepAssets = []string{
		"ripgrep-14.1.1-aarch64-apple-darwin.tar.gz",
		"ripgrep-14.1.1-x86_64-apple-darwin.tar.gz",
		"ripgrep-14.1.1-aarch64-unknown-linux-gnu.tar.gz",
		"ripgrep-14.1.1-arm-unknown-linux-gnueabihf.tar.gz",
		"ripgrep-14.1.1-i686-unknown-linux-gnu.tar.gz",
		"ripgrep-14.1.1-powerpc64-unknown-linux-gnu.tar.gz",
		"ripgrep-14.1.1-s390x-unknown-linux-gnu.tar.gz",
		"ripgrep-14.1.1-x86_64-pc-windows-gnu.zip",
		"ripgrep-14.1.1-x86_64-pc-windows-msvc.zip",
		"ripgrep-14.1.1-x86_64-unknown-linux-musl.tar.gz",
		"ripgrep_14.1.1-1_amd64.deb",
	}

	uvAssets = []string{
		"uv-aarch64-apple-darwin.tar.gz",
		"uv-x86_64-apple-darwin.tar.gz",
		"uv-aarch64-pc-windows-msvc.zip",
		"uv-aarch64-unknown-linux-gnu.tar.gz",
		"uv-aarch64-unknown-linux-musl.tar.gz",
		"uv-arm-unknown-linux-musleabihf.tar.gz",
		"uv-i686-pc-windows-msvc.zip",
		"uv-i686-unknown-linux-gnu.tar.gz",
		"uv-powerpc64-unknown-linux-gnu.tar.gz",
		"uv-x86_64-pc-windows-msvc.zip",
		"uv-x86_64-unknown-linux-gnu.tar.gz",
	}
)

func TestPickDarwinArm64(t *testing.T) {
	cases := []struct {
		name   string
		assets []string
		want   string
	}{
		{"fzf", fzfAssets, "fzf-0.60.3-darwin_arm64.tar.gz"},
		{"gh", ghAssets, "gh_2.63.2_macOS_arm64.zip"},
		{"lazygit", lazygitAssets, "lazygit_0.44.1_Darwin_arm64.tar.gz"},
		{"ripgrep", ripgrepAssets, "ripgrep-14.1.1-aarch64-apple-darwin.tar.gz"},
		{"uv", uvAssets, "uv-aarch64-apple-darwin.tar.gz"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, note, err := Pick(tc.assets, "darwin", "arm64", "")
			if err != nil {
				t.Fatalf("Pick(%s) unexpected error: %v", tc.name, err)
			}
			if got != tc.want {
				t.Fatalf("Pick(%s) = %q (note %q); want %q", tc.name, got, note, tc.want)
			}
		})
	}
}

func TestPickLinuxAmd64(t *testing.T) {
	got, _, err := Pick(fzfAssets, "linux", "amd64", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "fzf-0.60.3-linux_amd64.tar.gz" {
		t.Fatalf("got %q; want linux_amd64", got)
	}
}

func TestPickWindowsAmd64PrefersZipOverMsi(t *testing.T) {
	got, _, err := Pick(ghAssets, "windows", "amd64", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "gh_2.63.2_windows_amd64.zip" {
		t.Fatalf("got %q; want windows_amd64.zip (.msi must be blacklisted)", got)
	}
}

func TestPickRosettaFallback(t *testing.T) {
	// Only amd64 darwin assets exist; darwin/arm64 must fall back via Rosetta.
	assets := []string{
		"tool_1.2.0_darwin_amd64.tar.gz",
		"tool_1.2.0_darwin_amd64.zip",
		"tool_1.2.0_linux_amd64.tar.gz",
		"tool_1.2.0_windows_amd64.zip",
	}
	got, note, err := Pick(assets, "darwin", "arm64", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "tool_1.2.0_darwin_amd64.tar.gz" {
		t.Fatalf("got %q; want darwin_amd64.tar.gz", got)
	}
	if !strings.Contains(note, "rosetta-fallback") {
		t.Fatalf("note = %q; want it to contain rosetta-fallback", note)
	}
	if !strings.Contains(note, "archive-preference") {
		t.Fatalf("note = %q; want it to contain archive-preference", note)
	}
}

func TestPickIgnoresChecksumAndSignatureNoise(t *testing.T) {
	assets := []string{
		"mytool_1.2.0_darwin_arm64.tar.gz",
		"mytool_1.2.0_darwin_amd64.tar.gz",
		"mytool_1.2.0_linux_amd64.tar.gz",
		"mytool_1.2.0_checksums.txt",
		"mytool_1.2.0_darwin_arm64.tar.gz.sig",
		"mytool_1.2.0_darwin_arm64.tar.gz.sha256",
		"mytool_1.2.0.asc",
		"mytool.sbom",
		"SHA256SUMS.pem",
		"README.md",
	}
	got, _, err := Pick(assets, "darwin", "arm64", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "mytool_1.2.0_darwin_arm64.tar.gz" {
		t.Fatalf("got %q; want darwin_arm64.tar.gz", got)
	}
}

func TestPickAssetFilter(t *testing.T) {
	// Force the zip variant on a platform where tar.gz would normally win.
	got, note, err := Pick(lazygitAssets, "darwin", "arm64", "Windows_arm64")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "lazygit_0.44.1_Windows_arm64.zip" {
		t.Fatalf("got %q; want Windows_arm64.zip", got)
	}
	if note != "asset-filter" {
		t.Fatalf("note = %q; want asset-filter", note)
	}
}

func TestPickAssetFilterAnti(t *testing.T) {
	// '^' inverts: exclude every darwin asset, then pick for linux/amd64.
	got, _, err := Pick(fzfAssets, "linux", "amd64", "^arm")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "fzf-0.60.3-linux_amd64.tar.gz" {
		t.Fatalf("got %q; want linux_amd64.tar.gz", got)
	}
}

func TestPickAssetFilterNoMatch(t *testing.T) {
	_, _, err := Pick(fzfAssets, "darwin", "arm64", "nonexistent-substring")
	if err == nil {
		t.Fatal("expected error when asset filter matches nothing")
	}
}

func TestPickNoCandidatesListsAll(t *testing.T) {
	assets := []string{"notes.txt", "hashes.sha256", "signature.sig"}
	_, _, err := Pick(assets, "darwin", "arm64", "")
	if err == nil {
		t.Fatal("expected error when everything is blacklisted")
	}
	for _, a := range assets {
		if !strings.Contains(err.Error(), a) {
			t.Fatalf("error %q should list asset %q", err.Error(), a)
		}
	}
}

func TestPickSkipsTarXZWhenZipAvailable(t *testing.T) {
	assets := []string{
		"tool_1.2.3_darwin_arm64.tar.xz",
		"tool_1.2.3_darwin_arm64.zip",
	}
	got, _, err := Pick(assets, "darwin", "arm64", "")
	if err != nil {
		t.Fatal(err)
	}
	if got != "tool_1.2.3_darwin_arm64.zip" {
		t.Fatalf("got %q; want supported ZIP", got)
	}
}

func TestPickSkipsTXZWhenZipAvailable(t *testing.T) {
	assets := []string{
		"tool_1.2.3_darwin_arm64.txz",
		"tool_1.2.3_darwin_arm64.zip",
	}
	got, _, err := Pick(assets, "darwin", "arm64", "")
	if err != nil {
		t.Fatal(err)
	}
	if got != "tool_1.2.3_darwin_arm64.zip" {
		t.Fatalf("got %q; want supported ZIP", got)
	}
}

func TestPickOnlyXZReturnsUnsupported(t *testing.T) {
	assets := []string{
		"tool_1.2.3_darwin_arm64.tar.xz",
		"tool_1.2.3_darwin_arm64.txz",
	}
	_, _, err := Pick(assets, "darwin", "arm64", "")
	if err == nil {
		t.Fatal("expected unsupported-archive error")
	}
	for _, asset := range assets {
		if !strings.Contains(err.Error(), asset) {
			t.Fatalf("error %q should list unsupported asset %q", err, asset)
		}
	}
}

func TestPickAssetFilterCannotForceUnsupportedXZ(t *testing.T) {
	assets := []string{
		"tool_1.2.3_darwin_arm64.tar.xz",
		"tool_1.2.3_darwin_arm64.zip",
	}
	_, _, err := Pick(assets, "darwin", "arm64", "tar.xz")
	if err == nil {
		t.Fatal("asset filter must not force an unsupported archive")
	}
}

func TestPickSkipsKnownUnsupportedContainer(t *testing.T) {
	assets := []string{
		"tool_1.2.3_darwin_arm64.tar.zst",
		"tool_1.2.3_darwin_arm64.dmg",
		"tool_1.2.3_darwin_arm64.tar",
		"tool_1.2.3_darwin_arm64.zip",
	}
	got, _, err := Pick(assets, "darwin", "arm64", "")
	if err != nil {
		t.Fatal(err)
	}
	if got != "tool_1.2.3_darwin_arm64.zip" {
		t.Fatalf("got %q; want supported ZIP", got)
	}
}

func TestPickUnsupportedOS(t *testing.T) {
	_, _, err := Pick(fzfAssets, "plan9-bogus", "arm64", "")
	if err == nil {
		t.Fatal("expected error for unsupported OS")
	}
}

func TestPickAmbiguousRequiresAssetFilter(t *testing.T) {
	assets := []string{
		"tool-darwin-arm64-alpha",
		"tool-darwin-arm64-beta",
	}
	_, _, err := Pick(assets, "darwin", "arm64", "")
	if err == nil || !strings.Contains(err.Error(), "multiple matching assets") || !strings.Contains(err.Error(), "--asset") {
		t.Fatalf("expected actionable ambiguity error, got %v", err)
	}
}

func TestPickPrefersGzipOverBare(t *testing.T) {
	assets := []string{
		"tool-darwin-arm64",
		"tool-darwin-arm64.gz",
	}
	got, _, err := Pick(assets, "darwin", "arm64", "")
	if err != nil {
		t.Fatal(err)
	}
	if got != "tool-darwin-arm64.gz" {
		t.Fatalf("got %q", got)
	}
}

func TestEffectiveExt(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{"foo-1.3.5.tar.gz", ""},     // version pseudo-ext + archive stripped
		{"tool-1.2.3.zip", ""},       // archive stripped -> version -> none
		{"checksums.txt", ".txt"},    // real ext
		{"tool.sig", ".sig"},         // real ext
		{"hashes.sha256", ".sha256"}, // digits inside a word -> real ext
		{"hashes.sha256sum", ".sha256sum"},
		{"fzf", ""},                   // bare binary
		{"tool.tar.gz.sig", ".sig"},   // trailing sig wins
		{"App.AppImage", ".appimage"}, // case-insensitive
	}
	for _, tc := range cases {
		if got := effectiveExt(tc.name); got != tc.want {
			t.Errorf("effectiveExt(%q) = %q; want %q", tc.name, got, tc.want)
		}
	}
}

func TestBlacklistDarwinExe(t *testing.T) {
	if !blacklisted("tool_windows.exe", "darwin") {
		t.Error(".exe should be blacklisted on darwin")
	}
	if blacklisted("tool_windows.exe", "windows") {
		t.Error(".exe should NOT be blacklisted on windows")
	}
	if !blacklisted("tool.AppImage", "darwin") {
		t.Error(".appimage should be blacklisted on darwin")
	}
	if blacklisted("tool.AppImage", "linux") {
		t.Error(".appimage should NOT be blacklisted on linux")
	}
}
