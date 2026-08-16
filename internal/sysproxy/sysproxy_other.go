//go:build !darwin

package sysproxy

import "net/url"

func defaultSystemProxyURL() *url.URL { return nil }
