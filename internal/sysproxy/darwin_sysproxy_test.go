//go:build darwin

package sysproxy

import (
	"bytes"
	"encoding/xml"
	"net/url"
	"testing"
)

// plistFixture builds an XML plist resembling
// /Library/Preferences/SystemConfiguration/preferences.plist with two
// services, one proxy enabled and one disabled.
func plistFixture(t *testing.T, enabled bool) []byte {
	t.Helper()
	var buf bytes.Buffer
	buf.WriteString(xml.Header)
	buf.WriteString(`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">`)
	buf.WriteString(`<plist version="1.0"><dict>`)
	buf.WriteString(`<key>NetworkServices</key><dict>`)
	buf.WriteString(`<key>AAAA</key><dict><key>Proxies</key><dict>`)
	if enabled {
		buf.WriteString(`<key>HTTPSEnable</key><integer>1</integer>`)
		buf.WriteString(`<key>HTTPSProxy</key><string>127.0.0.1</string>`)
		buf.WriteString(`<key>HTTPSPort</key><integer>7890</integer>`)
	} else {
		buf.WriteString(`<key>HTTPSEnable</key><integer>0</integer>`)
		buf.WriteString(`<key>HTTPSProxy</key><string>127.0.0.1</string>`)
		buf.WriteString(`<key>HTTPSPort</key><integer>7890</integer>`)
	}
	buf.WriteString(`</dict></dict>`)
	buf.WriteString(`<key>BBBB</key><dict><key>Proxies</key><dict>`)
	buf.WriteString(`<key>HTTPEnable</key><integer>1</integer>`)
	buf.WriteString(`<key>HTTPProxy</key><string>10.0.0.9</string>`)
	buf.WriteString(`<key>HTTPPort</key><integer>3128</integer>`)
	buf.WriteString(`</dict></dict>`)
	buf.WriteString(`</dict></dict></plist>`)
	return buf.Bytes()
}

func TestParseSystemPreferencesProxyEnabled(t *testing.T) {
	root, err := parseXMLPlist(plistFixture(t, true))
	if err != nil {
		t.Fatal(err)
	}
	services := root.(plistDict)["NetworkServices"].(plistDict)
	if _, ok := services["AAAA"]; !ok {
		t.Fatalf("services missing: %v", services)
	}
	// HTTPS on AAAA (7890) sorts before HTTP on BBBB (3128); HTTPS preferred.
	proxies := services["AAAA"].(plistDict)["Proxies"].(plistDict)
	u := proxyFromDict(proxies)
	if u == nil || u.Host != "127.0.0.1:7890" || u.Scheme != "http" {
		t.Fatalf("proxyFromDict = %v", u)
	}
}

func TestParseSystemPreferencesProxyDisabledAndHTTPFallback(t *testing.T) {
	root, err := parseXMLPlist(plistFixture(t, false))
	if err != nil {
		t.Fatal(err)
	}
	services := root.(plistDict)["NetworkServices"].(plistDict)
	// Disabled HTTPS on AAAA → nil; HTTP enabled on BBBB → fallback.
	if u := proxyFromDict(services["AAAA"].(plistDict)["Proxies"].(plistDict)); u != nil {
		t.Fatalf("disabled proxy returned: %v", u)
	}
	u := proxyFromDict(services["BBBB"].(plistDict)["Proxies"].(plistDict))
	if u == nil || u.Host != "10.0.0.9:3128" {
		t.Fatalf("HTTP fallback = %v", u)
	}
}

func TestEnvProxyURL(t *testing.T) {
	get := func(m map[string]string) func(string) string {
		return func(k string) string { return m[k] }
	}
	if u := EnvProxyURL(get(map[string]string{"HTTPS_PROXY": "http://127.0.0.1:7890"})); u == nil || u.Host != "127.0.0.1:7890" {
		t.Fatalf("HTTPS_PROXY not honored: %v", u)
	}
	if u := EnvProxyURL(get(map[string]string{"HTTP_PROXY": "garbage://", "ALL_PROXY": "http://10.0.0.1:8080"})); u == nil || u.Host != "10.0.0.1:8080" {
		t.Fatalf("ALL_PROXY fallback failed: %v", u)
	}
	if u := EnvProxyURL(get(map[string]string{})); u != nil {
		t.Fatalf("empty env must yield nil: %v", u)
	}
	_ = url.URL{}
}

// TestSystemProxyURLFallsBackToEnvironment: when the platform layer reports no
// proxy (always the case off macOS), SystemProxyURL falls back to the standard
// environment variables so Linux/CI env proxies work identically.
func TestSystemProxyURLFallsBackToEnvironment(t *testing.T) {
	orig := platformSystemProxyURL
	platformSystemProxyURL = func() *url.URL { return nil }
	t.Cleanup(func() { platformSystemProxyURL = orig })

	t.Setenv("HTTPS_PROXY", "http://env-proxy:3128")
	u := SystemProxyURL()
	if u == nil || u.String() != "http://env-proxy:3128" {
		t.Fatalf("SystemProxyURL = %v, want the env proxy", u)
	}
}

// TestSystemProxyURLPlatformWinsOverEnvironment: a configured platform proxy
// takes precedence over the environment fallback.
func TestSystemProxyURLPlatformWinsOverEnvironment(t *testing.T) {
	orig := platformSystemProxyURL
	platformSystemProxyURL = func() *url.URL {
		u, _ := url.Parse("http://plist-proxy:7890")
		return u
	}
	t.Cleanup(func() { platformSystemProxyURL = orig })

	t.Setenv("HTTPS_PROXY", "http://env-proxy:3128")
	u := SystemProxyURL()
	if u == nil || u.String() != "http://plist-proxy:7890" {
		t.Fatalf("SystemProxyURL = %v, want the platform proxy", u)
	}
}
