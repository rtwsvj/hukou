package ghrelease

import (
	"net/url"
	"strings"

	"github.com/rtwsvj/hukou/internal/i18n"
)

// DefaultBaseURL is the GitHub API base the client uses when BaseURL is
// empty: the public github.com REST API.
const DefaultBaseURL = defaultBaseURL

// ValidateBaseURL checks a user-supplied API base URL (GitHub Enterprise
// Server or a mirror) for use as Client BaseURL and returns it normalized.
//
// The value must be an absolute URL with an http(s) scheme and a host.
// Plain HTTP is enforced to localhost or 127.0.0.1 only — the same
// test-server convenience the client's own auth-host and redirect rules
// already extend to custom base hosts; every other host must serve HTTPS.
// A path suffix (for example GHES's /api/v3) is preserved because the
// client appends API paths to the base verbatim. Query strings, fragments,
// and user credentials are refused: none of them survive meaningful
// base-URL semantics, and accepting them would silently corrupt request
// URLs. An unusable value is always an error, never a silent fallback to
// the default.
func ValidateBaseURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil {
		return "", i18n.Wrapf("invalid GitHub base URL %q: not a valid URL", err, raw)
	}
	switch u.Scheme {
	case "http":
		if host := u.Hostname(); host != "localhost" && host != "127.0.0.1" {
			return "", i18n.Errorf("invalid GitHub base URL %q: HTTP is only allowed for localhost or 127.0.0.1; use HTTPS", raw)
		}
	case "https":
	default:
		return "", i18n.Errorf("invalid GitHub base URL %q: scheme must be http (localhost/127.0.0.1 only) or https", raw)
	}
	if u.Host == "" {
		return "", i18n.Errorf("invalid GitHub base URL %q: missing host", raw)
	}
	if u.User != nil {
		return "", i18n.Errorf("invalid GitHub base URL %q: user credentials are not supported", raw)
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return "", i18n.Errorf("invalid GitHub base URL %q: query and fragment are not supported", raw)
	}
	return strings.TrimRight(u.String(), "/"), nil
}

// NewWithBaseURL builds a release client whose API requests go to baseURL
// instead of the default https://api.github.com (GitHub Enterprise Server /
// mirror support). An empty baseURL selects the default https://api.github.com
// client, exactly like New. It fails closed on a non-empty unusable baseURL.
// As with New, the token is attached only when the request host is allowed to
// receive it — api.github.com, or exactly the host of this custom base — and
// never reaches asset-download hosts or redirects.
func NewWithBaseURL(token, baseURL string) (*Client, error) {
	c := New(token)
	if baseURL == "" {
		return c, nil
	}
	base, err := ValidateBaseURL(baseURL)
	if err != nil {
		return nil, err
	}
	c.BaseURL = base
	return c, nil
}
