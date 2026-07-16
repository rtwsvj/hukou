//go:build network_e2e

package cmd

import (
	"os"
	"testing"

	"github.com/rtwsvj/hukou/internal/ghrelease"
)

// TestNetworkE2E_LatestRelease is the L6 real-network gate described in
// docs/07-testing-and-verification.md. It is excluded from the default build by
// the network_e2e build tag and additionally skips unless HUKOU_NETWORK_E2E=1,
// so neither `go test ./...` nor `make verify` can ever reach GitHub. Run it
// with `make verify-network` and a GITHUB_TOKEN (or GH_TOKEN) that can read the
// fixture repository.
//
// This is intentionally a minimal, honest skeleton: it exercises the real
// GitHub release metadata path (ghrelease.Client.Latest) against a controlled
// fixture repository. A fuller adopt -> upgrade --dry-run -> rollback flow can
// be layered on top once a stable public fixture asset exists for the host
// platform; keeping the entry point here lets that grow without touching the
// default gates.
func TestNetworkE2E_LatestRelease(t *testing.T) {
	if os.Getenv("HUKOU_NETWORK_E2E") != "1" {
		t.Skip("network e2e disabled; set HUKOU_NETWORK_E2E=1 to enable")
	}

	token := firstEnv("GITHUB_TOKEN", "GH_TOKEN")
	if token == "" {
		t.Skip("network e2e requires GITHUB_TOKEN or GH_TOKEN")
	}

	owner := "rtwsvj"
	if v := os.Getenv("HUKOU_NETWORK_E2E_OWNER"); v != "" {
		owner = v
	}
	repo := "hukou"
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
	t.Logf("latest %s/%s release: tag=%s assets=%d", owner, repo, rel.TagName, len(rel.Assets))
}
