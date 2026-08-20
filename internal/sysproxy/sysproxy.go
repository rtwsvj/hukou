// Package sysproxy resolves the host's system proxy settings without spawning
// subprocesses: environment variables (HTTP_PROXY/HTTPS_PROXY/ALL_PROXY) and,
// on macOS, the SystemConfiguration preferences plist at
// /Library/Preferences/SystemConfiguration/preferences.plist. It exists for
// hukou's download-retry path; the repository's execution fence (no os/exec
// outside internal/orchestrate/executor) stays intact because only stdlib XML
// parsing is used.
package sysproxy

import (
	"net/url"
	"os"
	"strings"
)

// EnvProxyURL returns the first proxy URL found in the standard environment
// variables, in order HTTPS_PROXY, HTTP_PROXY, ALL_PROXY. Values are passed
// through url.Parse; invalid entries are skipped.
func EnvProxyURL(getenv func(string) string) *url.URL {
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	for _, key := range []string{"HTTPS_PROXY", "HTTP_PROXY", "ALL_PROXY"} {
		raw := strings.TrimSpace(getenv(key))
		if raw == "" {
			continue
		}
		if u, err := url.Parse(raw); err == nil && u.Host != "" {
			return u
		}
	}
	return nil
}

// SystemProxyURL returns the proxy to offer code paths that need one beyond
// net/http's automatic ProxyFromEnvironment: on macOS the SystemConfiguration
// plist proxy (HTTPS preferred, then HTTP, then SOCKS, only when the
// corresponding Enable flag is set); when the platform reports none — always
// the case off macOS — it falls back to the standard proxy environment
// variables (EnvProxyURL), so an env-configured proxy behaves the same on
// every platform. This mirrors, not contradicts, http.DefaultTransport: the
// env fallback only fires when the platform layer found nothing.
var SystemProxyURL = func() *url.URL {
	if u := platformSystemProxyURL(); u != nil {
		return u
	}
	return EnvProxyURL(os.Getenv)
}
