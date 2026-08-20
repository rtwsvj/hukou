//go:build !darwin

package sysproxy

import "net/url"

var platformSystemProxyURL = func() *url.URL { return nil }
