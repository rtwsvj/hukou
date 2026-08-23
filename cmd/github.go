package cmd

import (
	"os"

	"github.com/rtwsvj/hukou/internal/ghrelease"
)

func firstEnv(names ...string) string {
	for _, n := range names {
		if v := os.Getenv(n); v != "" {
			return v
		}
	}
	return ""
}

// githubBaseURLFlag is the registered name of the root persistent flag that
// overrides the GitHub API base URL.
const githubBaseURLFlag = "github-base-url"

// githubBaseURLValue holds the --github-base-url persistent flag value
// (empty when the flag was not given).
var githubBaseURLValue string

// init registers the GitHub API base override on the root command as a
// persistent flag, so every networked subcommand inherits it. The env
// variable HUKOU_GITHUB_BASE_URL provides the default; the flag wins when
// both are set (documented precedence: flag > env > default). Registration
// lives here so the resolution helper and its tests sit beside it.
func init() {
	rootCmd.PersistentFlags().StringVar(&githubBaseURLValue, githubBaseURLFlag, "",
		flagGitHubBaseURLUsage())
}

// flagGitHubBaseURLUsage renders the help text of the --github-base-url
// persistent flag. The literal is the catalog key localized by the central
// i18n pass; the runtime value is the English default.
func flagGitHubBaseURLUsage() string {
	return "GitHub API base URL override (default https://api.github.com); overrides HUKOU_GITHUB_BASE_URL; HTTPS enforced except localhost/127.0.0.1"
}

// githubBaseURL resolves the effective GitHub API base URL for a command:
// the --github-base-url persistent flag wins, then HUKOU_GITHUB_BASE_URL,
// then the ghrelease package default (https://api.github.com). Any value
// that is present but unusable is an error — a mistyped mirror URL must
// fail closed, never silently fall back to github.com.
func githubBaseURL() (string, error) {
	return resolveGitHubBaseURL(githubBaseURLValue, os.Getenv)
}

// resolveGitHubBaseURL is the testable core of githubBaseURL: same
// precedence, error instead of cobra RunE plumbing.
func resolveGitHubBaseURL(flagValue string, getenv func(string) string) (string, error) {
	raw := flagValue
	if raw == "" {
		if env := getenv("HUKOU_GITHUB_BASE_URL"); env != "" {
			raw = env
		}
	}
	if raw == "" {
		return "", nil // empty means: use the client's built-in default
	}
	return ghrelease.ValidateBaseURL(raw)
}

// newGitHubClient is the single shared constructor for release clients in
// the cmd layer: token from GITHUB_TOKEN/GH_TOKEN and base URL from
// --github-base-url / HUKOU_GITHUB_BASE_URL with the documented precedence.
func newGitHubClient(token string) (*ghrelease.Client, error) {
	base, err := githubBaseURL()
	if err != nil {
		return nil, err
	}
	if base == "" {
		return ghrelease.New(token), nil
	}
	return ghrelease.NewWithBaseURL(token, base)
}
