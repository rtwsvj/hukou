//go:build network_e2e

package cmd

import (
	"net/url"
	"os"
	"runtime"
	"testing"

	"github.com/rtwsvj/hukou/internal/assetpick"
	"github.com/rtwsvj/hukou/internal/ghrelease"
)

// TestNetworkE2E_LatestRelease is the L6 real-network gate described in
// docs/07-testing-and-verification.md. It is excluded from the default build by
// the network_e2e build tag and additionally skips unless HUKOU_NETWORK_E2E=1,
// so neither `go test ./...` nor `make verify` can ever reach GitHub. Run it
// with `make verify-network` and a GITHUB_TOKEN (or GH_TOKEN) that can read the
// fixture repository.
//
// It exercises the read-only release resolution path against a stable public
// fixture repository:
//
//   - ghrelease.Client.Latest performs a real GitHub release-API request and
//     returns the tag plus the asset inventory of the latest stable release.
//   - assetpick.Pick runs the real asset-selection waterfall over those asset
//     names for a fixed platform, proving the metadata that GitHub actually
//     ships still resolves to a single downloadable archive.
//   - The selected asset's metadata must be actionable: its
//     BrowserDownloadURL must be an https URL and its reported Size must be
//     positive, since those are exactly the fields the download path would
//     consume next.
//
// It deliberately never downloads an asset: the gate covers the GitHub API,
// asset selection, and asset metadata (URL scheme and size) — not the CDN
// transfer itself, which would pull multi-megabyte binaries. The default
// fixture is cli/cli (the gh CLI), which publishes a stable, predictably named
// release on every version; override it with HUKOU_NETWORK_E2E_OWNER /
// HUKOU_NETWORK_E2E_REPO to point at another repository (for example
// junegunn/fzf) without touching this file.
func TestNetworkE2E_LatestRelease(t *testing.T) {
	if os.Getenv("HUKOU_NETWORK_E2E") != "1" {
		t.Skip("network e2e disabled; set HUKOU_NETWORK_E2E=1 to enable")
	}

	token := firstEnv("GITHUB_TOKEN", "GH_TOKEN")
	if token == "" {
		t.Skip("network e2e requires GITHUB_TOKEN or GH_TOKEN")
	}

	owner := "cli"
	if v := os.Getenv("HUKOU_NETWORK_E2E_OWNER"); v != "" {
		owner = v
	}
	repo := "cli"
	if v := os.Getenv("HUKOU_NETWORK_E2E_REPO"); v != "" {
		repo = v
	}

	client := ghrelease.New(token)
	rel, err := client.Latest(owner, repo)
	if err != nil {
		t.Fatalf("fetch latest release for %s/%s: %v", owner, repo, err)
	}
	if rel.TagName == "" {
		t.Fatalf("latest release for %s/%s returned an empty tag", owner, repo)
	}
	if len(rel.Assets) == 0 {
		t.Fatalf("latest release %s for %s/%s carried no assets", rel.TagName, owner, repo)
	}

	names := make([]string, 0, len(rel.Assets))
	for _, a := range rel.Assets {
		names = append(names, a.Name)
	}
	t.Logf("latest %s/%s release: tag=%s assets=%d", owner, repo, rel.TagName, len(names))

	// linux/amd64 is the one platform every real release fixture ships, so it is
	// a deterministic assertion regardless of the host running the gate.
	const wantGOOS, wantGOARCH = "linux", "amd64"
	choice, note, err := assetpick.Pick(names, wantGOOS, wantGOARCH, "")
	if err != nil {
		t.Fatalf("assetpick.Pick(%s/%s) over %s/%s@%s assets failed: %v\navailable: %v",
			wantGOOS, wantGOARCH, owner, repo, rel.TagName, err, names)
	}
	if choice == "" {
		t.Fatalf("assetpick.Pick(%s/%s) returned an empty choice for %s/%s@%s",
			wantGOOS, wantGOARCH, owner, repo, rel.TagName)
	}
	picked, ok := findAsset(rel.Assets, choice)
	if !ok {
		t.Fatalf("assetpick.Pick returned %q which is not among the release assets %v", choice, names)
	}
	t.Logf("assetpick.Pick(%s/%s) -> %s (tiebreak note %q)", wantGOOS, wantGOARCH, choice, note)

	// The picked asset's metadata must be actionable without downloading it:
	// the download path would consume exactly these two fields next.
	u, err := url.Parse(picked.BrowserDownloadURL)
	if err != nil {
		t.Fatalf("picked asset %q has unparseable BrowserDownloadURL %q: %v", picked.Name, picked.BrowserDownloadURL, err)
	}
	if u.Scheme != "https" || u.Host == "" {
		t.Fatalf("picked asset %q must have an https download URL with a host, got %q", picked.Name, picked.BrowserDownloadURL)
	}
	if picked.Size <= 0 {
		t.Fatalf("picked asset %q must report a positive size, got %d", picked.Name, picked.Size)
	}
	t.Logf("picked asset metadata: url=%s size=%d", picked.BrowserDownloadURL, picked.Size)

	// The host platform is best-effort: some fixtures may not ship the exact
	// runner combination, so a miss is logged rather than failed. It documents
	// what this machine would actually resolve to.
	if hostChoice, hostNote, hostErr := assetpick.Pick(names, runtime.GOOS, runtime.GOARCH, ""); hostErr != nil {
		t.Logf("assetpick.Pick(host %s/%s) did not resolve a single asset: %v", runtime.GOOS, runtime.GOARCH, hostErr)
	} else {
		t.Logf("assetpick.Pick(host %s/%s) -> %s (tiebreak note %q)", runtime.GOOS, runtime.GOARCH, hostChoice, hostNote)
	}
}

func findAsset(assets []ghrelease.Asset, name string) (ghrelease.Asset, bool) {
	for _, a := range assets {
		if a.Name == name {
			return a, true
		}
	}
	return ghrelease.Asset{}, false
}
