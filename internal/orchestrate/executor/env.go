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

// childEnvAllowPrefixes passes whole variable families: XDG directories,
// locale categories, and HOMEBREW_* — brew configures much of its behavior
// through the latter (HOMEBREW_NO_AUTO_UPDATE etc.), so stripping it would
// silently change how upgrades run.
var childEnvAllowPrefixes = []string{"XDG_", "LC_", "HOMEBREW_"}

// buildChildEnv composes the environment a manager subprocess runs with. The
// base is the allowlist-filtered parent environment; on top of it, proxy
// inheritance:
//
//   - an explicit HTTPS_PROXY/https_proxy already in the environment wins —
//     the user's own proxy configuration is never overridden;
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
	if envHas(env, "HTTPS_PROXY") || envHas(env, "https_proxy") {
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
// explicit escape hatch, reported as true so the caller can say so).
func filteredEnviron() (env []string, fullPassthru bool) {
	passthru := os.Getenv("HUKOU_UP_ENV_PASSTHRU")
	if passthru == "*" {
		return os.Environ(), true
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

// envAllowed reports whether one variable name passes the allowlist: exact
// names, whole prefixes, the proxy keys, or a HUKOU_UP_ENV_PASSTHRU extra.
func envAllowed(key string, extra []string) bool {
	if slices.Contains(childEnvAllowlist, key) || slices.Contains(proxyEnvKeys, key) || slices.Contains(extra, key) {
		return true
	}
	for _, prefix := range childEnvAllowPrefixes {
		if strings.HasPrefix(key, prefix) {
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
