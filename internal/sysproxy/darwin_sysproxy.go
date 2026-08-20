//go:build darwin

package sysproxy

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"
)

const systemConfigPath = "/Library/Preferences/SystemConfiguration/preferences.plist"

var platformSystemProxyURL = func() *url.URL {
	payload, err := os.ReadFile(systemConfigPath)
	if err != nil {
		return nil
	}
	root, err := parseXMLPlist(payload)
	if err != nil {
		return nil
	}
	dict, ok := root.(plistDict)
	if !ok {
		return nil
	}
	services, ok := dict["NetworkServices"].(plistDict)
	if !ok {
		return nil
	}
	// Multiple network services may exist; scan them in a deterministic order
	// and return the first enabled proxy found.
	names := make([]string, 0, len(services))
	for name := range services {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		svc, ok := services[name].(plistDict)
		if !ok {
			continue
		}
		proxies, ok := svc["Proxies"].(plistDict)
		if !ok {
			continue
		}
		if u := proxyFromDict(proxies); u != nil {
			return u
		}
	}
	return nil
}

// proxyFromDict extracts the first enabled proxy from a Proxies dict:
// HTTPS, then HTTP, then SOCKS.
func proxyFromDict(p plistDict) *url.URL {
	type candidate struct{ enableKey, hostKey, portKey, scheme string }
	candidates := []candidate{
		{"HTTPSEnable", "HTTPSProxy", "HTTPSPort", "http"},
		{"HTTPEnable", "HTTPProxy", "HTTPPort", "http"},
		{"SOCKSEnable", "SOCKSProxy", "SOCKSPort", "socks5"},
	}
	for _, c := range candidates {
		if enabled(p[c.enableKey]) == false {
			continue
		}
		host, hostOK := p[c.hostKey].(string)
		port, portOK := p[c.portKey].(int64)
		if !hostOK || host == "" || !portOK || port <= 0 || port > 65535 {
			continue
		}
		return &url.URL{Scheme: c.scheme, Host: fmt.Sprintf("%s:%d", host, port)}
	}
	return nil
}

func enabled(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case int64:
		return t != 0
	default:
		return false
	}
}

// ---- minimal XML plist reader (dict/key/value model) ----

type plistDict map[string]any

type plistReader struct {
	decoder *xml.Decoder
}

func parseXMLPlist(data []byte) (any, error) {
	r := &plistReader{decoder: xml.NewDecoder(bytes.NewReader(data))}
	return r.readValue()
}

// readValue consumes tokens until it finds a value element and returns its
// parsed contents.
func (r *plistReader) readValue() (any, error) {
	for {
		tok, err := r.decoder.Token()
		if err != nil {
			return nil, err
		}
		if start, ok := tok.(xml.StartElement); ok {
			return r.readValueFrom(start)
		}
	}
}

// readValueFrom parses the value of the element whose StartElement has
// already been consumed.
func (r *plistReader) readValueFrom(start xml.StartElement) (any, error) {
	switch start.Name.Local {
	case "plist":
		// The plist root wrapper: its single value is the document body.
		return r.readValue()
	case "dict":
		return r.readDict()
	case "array":
		return r.readArray()
	case "string":
		return r.readText()
	case "integer":
		text, err := r.readText()
		if err != nil {
			return nil, err
		}
		var n int64
		if _, err := fmt.Sscanf(strings.TrimSpace(text), "%d", &n); err != nil {
			return nil, err
		}
		return n, nil
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		// Unknown element (data/date/real/...): skip its subtree. Proxy
		// settings never live in those types.
		if err := r.decoder.Skip(); err != nil {
			return nil, err
		}
		return nil, nil
	}
}

func (r *plistReader) readDict() (any, error) {
	out := plistDict{}
	var pendingKey string
	haveKey := false
	for {
		tok, err := r.decoder.Token()
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.EndElement:
			if t.Name.Local == "dict" {
				return out, nil
			}
		case xml.StartElement:
			if t.Name.Local == "key" {
				pendingKey, err = r.readText()
				if err != nil {
					return nil, err
				}
				haveKey = true
				continue
			}
			v, err := r.readValueFrom(t)
			if err != nil {
				return nil, err
			}
			if haveKey {
				out[pendingKey] = v
				haveKey = false
			}
		}
	}
}

func (r *plistReader) readArray() (any, error) {
	out := []any{}
	for {
		tok, err := r.decoder.Token()
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.EndElement:
			if t.Name.Local == "array" {
				return out, nil
			}
		case xml.StartElement:
			v, err := r.readValueFrom(t)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
	}
}

// readText returns the character content of the current element.
func (r *plistReader) readText() (string, error) {
	for {
		tok, err := r.decoder.Token()
		if err != nil {
			return "", err
		}
		switch t := tok.(type) {
		case xml.CharData:
			return string(t), nil
		case xml.EndElement:
			return "", nil
		}
	}
}
