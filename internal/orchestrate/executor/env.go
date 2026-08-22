// Child-process environment composition for manager subprocesses. Two layers:
//
//  1. An allowlist (filteredEnviron) decides which parent variables a manager
//     may see at all. Manager upgrades run third-party code — brew formulas,
//     npm/pnpm lifecycle scripts — so the parent's full environment (with
//     GITHUB_TOKEN, NPM_TOKEN, AWS_* and friends) must NOT leak into it.
//  2. System-proxy inheritance (buildChildEnv), layered on top: when the
//     filtered environment has no explicit proxy, the OS system proxy is
//     injected so a manager stuck behind a slow direct route can use it.
//
// Both layers are standalone, testable functions so policy changes stay local
// to this file and never touch runOne.
package executor

import (
	"os"
	"slices"
	"strings"

	"github.com/rtwsvj/hukou/internal/sysproxy"
)

// proxyEnvKeys is the single source of truth for proxy variables: the
// allowlist passes them through when the parent set them, and system-proxy
// inheritance fills whichever are missing. One list so the two never drift.
var proxyEnvKeys = []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"}

// childEnvAllowlist is the exact-name pass-through for a manager subprocess:
// everything a well-behaved CLI tool legitimately needs, nothing more.
var childEnvAllowlist = []string{
	"PATH", "HOME", "USER", "LOGNAME", "SHELL",
	"TMPDIR", "TEMP", "TMP",
	"LANG", "TERM", "COLORTERM",
	"CI",
	"NO_PROXY", "no_proxy",
}

// childEnvAllowPrefixes passes whole variable families: XDG directories and
// locale categories. HOMEBREW_* deliberately has no prefix entry — the family
// carries secrets (HOMEBREW_GITHUB_API_TOKEN) and download redirects
// (HOMEBREW_BOTTLE_DOMAIN) — so only the exact, non-secret settings brew
// benefits from during an upgrade are allowlisted by name below.
var childEnvAllowPrefixes = []string{"XDG_", "LC_"}

// childEnvBrewAllowlist is the exact-name pass-through for the non-secret
// HOMEBREW_* variables brew configures behavior through. Domain overrides
// (HOMEBREW_API_DOMAIN, HOMEBREW_BOTTLE_DOMAIN) are intentionally absent:
// they redirect formula metadata and bottle downloads.
var childEnvBrewAllowlist = []string{
	"HOMEBREW_NO_AUTO_UPDATE",
	"HOMEBREW_NO_INSTALL_CLEANUP",
	"HOMEBREW_NO_ENV_HINTS",
}

// childEnvDenylist is a hard block evaluated BEFORE any allowlist entry,
// prefix, or HUKOU_UP_ENV_PASSTHRU extra — including the "*" escape hatch.
// These variables are credentials by definition, and a manager subprocess
// running third-party lifecycle scripts must never see them, no matter what
// the parent asked for.
var childEnvDenylist = []string{
	"HOMEBREW_GITHUB_API_TOKEN",
	"HOMEBREW_GITHUB_PACKAGES_TOKEN",
	"GITHUB_TOKEN",
	"GH_TOKEN",
	"GITHUB_ENTERPRISE_TOKEN",
	"GH_ENTERPRISE_TOKEN",
	"NPM_TOKEN",
	"NODE_AUTH_TOKEN",
	"AWS_ACCESS_KEY_ID",
	"AWS_SECRET_ACCESS_KEY",
}

// buildChildEnv composes the environment a manager subprocess runs with. The
// base is the allowlist-filtered parent environment; on top of it, proxy
//   - an explicit HTTPS_PROXY/https_proxy already in the environment wins —
//     the user's own proxy configuration is never overridden. A variable
//     that is PRESENT but empty counts as explicit too: setting it to ""
//     is a common, deliberate way of saying "no proxy for this run";
//   - HUKOU_UP_NO_PROXY_INHERIT=1 opts out of inheritance entirely;
//   - otherwise, when the OS reports a system proxy (sysproxy.SystemProxyURL,
//     currently macOS only), it is injected into whichever of proxyEnvKeys
//     are unset, so a manager stuck behind a slow direct route (the classic
//     brew-on-a-slow-network case) can use it.
//
// Existing NO_PROXY/no_proxy entries ride along untouched (only absent keys
// are ever appended). The second return value is the proxy's host:port for a
// one-line stderr note, or "" when nothing was injected; the URL's userinfo
// (possible credentials) is deliberately not returned. The third reports
// HUKOU_UP_ENV_PASSTHRU=* (full parent inheritance) so the caller can warn.
func buildChildEnv() (env []string, proxyHost string, fullPassthru bool) {
	env, fullPassthru = filteredEnviron()
	if envPresent(env, "HTTPS_PROXY") || envPresent(env, "https_proxy") {
		return env, "", fullPassthru
	}
	if os.Getenv("HUKOU_UP_NO_PROXY_INHERIT") == "1" {
		return env, "", fullPassthru
	}
	u := sysproxy.SystemProxyURL()
	if u == nil {
		return env, "", fullPassthru
	}
	value := u.String()
	for _, key := range proxyEnvKeys {
		if !envHas(env, key) {
			env = append(env, key+"="+value)
		}
	}
	return env, u.Host, fullPassthru
}

// filteredEnviron builds the allowlist-filtered base environment.
// HUKOU_UP_ENV_PASSTHRU extends it: a comma-separated list of extra variable
// names to pass through, or "*" to restore full parent inheritance (an
// explicit escape hatch, reported as true so the caller can say so). The
// denylist wins over both forms: credentials are stripped even under "*",
// which still reports fullPassthru=true.
func filteredEnviron() (env []string, fullPassthru bool) {
	passthru := os.Getenv("HUKOU_UP_ENV_PASSTHRU")
	if passthru == "*" {
		for _, e := range os.Environ() {
			key, _, _ := strings.Cut(e, "=")
			if !slices.Contains(childEnvDenylist, key) {
				env = append(env, e)
			}
		}
		return env, true
	}
	var extra []string
	for _, name := range strings.Split(passthru, ",") {
		if name = strings.TrimSpace(name); name != "" {
			extra = append(extra, name)
		}
	}
	env = make([]string, 0, len(childEnvAllowlist)+len(extra))
	for _, e := range os.Environ() {
		key, _, _ := strings.Cut(e, "=")
		if envAllowed(key, extra) {
			env = append(env, e)
		}
	}
	return env, false
}

// envAllowed reports whether one variable name passes the allowlist. The
// secret denylist is consulted first and refuses unconditionally; after that,
// exact names (including the brew settings), whole prefixes, the proxy keys,
// or a HUKOU_UP_ENV_PASSTHRU extra may admit it.
func envAllowed(key string, extra []string) bool {
	if slices.Contains(childEnvDenylist, key) {
		return false
	}
	if slices.Contains(childEnvAllowlist, key) || slices.Contains(childEnvBrewAllowlist, key) ||
		slices.Contains(proxyEnvKeys, key) || slices.Contains(extra, key) {
		return true
	}
	for _, prefix := range childEnvAllowPrefixes {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}

// envPresent reports whether env carries key at all, empty or not. Unlike
// envHas it treats an empty assignment as set: buildChildEnv uses it for the
// proxy skip decision, where an explicit `HTTPS_PROXY=` means "the user said
// no proxy for this run", while envHas keeps its empty=unset meaning
// everywhere else (injection only fills keys whose value is genuinely unset).
func envPresent(env []string, key string) bool {
	prefix := key + "="
	for _, e := range env {
		if strings.HasPrefix(e, prefix) {
			return true
		}
	}
	return false
}

// envHas reports whether env carries key with a non-empty value (an empty
// assignment counts as unset).
func envHas(env []string, key string) bool {
	prefix := key + "="
	for _, e := range env {
		if strings.HasPrefix(e, prefix) {
			return strings.TrimSpace(e[len(prefix):]) != ""
		}
	}
	return false
}
