package cmd

import (
	"context"
	"fmt"
	"io"

	"github.com/rtwsvj/hukou/internal/i18n"
)

// menuQuickUpdateAction is the interactive wrapper of `hukou upgrade --all`:
// it reuses runUpgrade's exact setup (token via firstEnv, client via
// newGitHubClient so --github-base-url / HUKOU_GITHUB_BASE_URL precedence and
// its fail-closed validation apply, advisory retry log on stderr) and then
// hands off to doUpgradeCtx, the same engine the CLI uses.
//
// The state mutation lock is NOT taken here on purpose: doUpgradeCtx acquires
// it internally for non-dry runs, and the lock file is not re-entrant — a
// second acquisition from the same process would deadlock against itself.
//
// Errors are informational inside the interactive menu: they are printed to
// stderr and swallowed (nil return) so the loop continues, matching the
// approved error-fallback decision for menu actions. The empty-manifest case
// needs no special handling either — doUpgradeCtx prints the same
// "No adopted tools" line the `hukou upgrade` CLI prints.
func menuQuickUpdateAction(stdout, stderr io.Writer) error {
	token := firstEnv("GITHUB_TOKEN", "GH_TOKEN")
	client, err := newGitHubClient(token)
	if err != nil {
		fmt.Fprintf(stderr, "%s\n", i18n.T("Warning: %v", err))
		return nil
	}
	// Surface advisory download retries (system-proxy fallback) on stderr,
	// exactly as runUpgrade does.
	client.Log = func(msg string) {
		fmt.Fprintf(stderr, "%s\n", msg)
	}
	if err := doUpgradeCtx(context.Background(), stdout, stderr, nil, true, false, "", client, false); err != nil {
		fmt.Fprintf(stderr, "%s\n", i18n.T("Warning: %v", err))
	}
	return nil
}
