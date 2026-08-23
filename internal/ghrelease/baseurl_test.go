package ghrelease

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestValidateBaseURL covers the fail-closed matrix for the user-supplied
// API base: absolute http(s) URLs only, plain HTTP restricted to the
// localhost/127.0.0.1 test-server parity the client already extends to
// custom base hosts, no credentials/query/fragment, and a preserved path
// suffix (GHES serves its REST API under /api/v3).
func TestValidateBaseURL(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		want    string
		wantErr bool
	}{
		{name: "default https host", raw: "https://api.github.com", want: "https://api.github.com"},
		{name: "ghes with api path", raw: "https://ghe.example.com/api/v3/", want: "https://ghe.example.com/api/v3"},
		{name: "host with port", raw: "https://ghe.internal:8443", want: "https://ghe.internal:8443"},
		{name: "trailing slash trimmed", raw: "https://ghe.example.com/", want: "https://ghe.example.com"},
		{name: "localhost http allowed", raw: "http://localhost:8080", want: "http://localhost:8080"},
		{name: "loopback http allowed", raw: "http://127.0.0.1:9000", want: "http://127.0.0.1:9000"},
		{name: "plain http external refused", raw: "http://mirror.example.com", wantErr: true},
		{name: "ftp scheme refused", raw: "ftp://files.example.com", wantErr: true},
		{name: "bare host refused", raw: "ghe.example.com", wantErr: true},
		{name: "missing host refused", raw: "https:///api/v3", wantErr: true},
		{name: "embedded credentials refused", raw: "https://user:pass@ghe.example.com", wantErr: true},
		{name: "query refused", raw: "https://ghe.example.com?x=1", wantErr: true},
		{name: "fragment refused", raw: "https://ghe.example.com#frag", wantErr: true},
		{name: "empty string refused", raw: "", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ValidateBaseURL(tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ValidateBaseURL(%q) = %q, want error", tc.raw, got)
				}
				if got != "" {
					t.Fatalf("ValidateBaseURL(%q) returned a value alongside an error: %q", tc.raw, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ValidateBaseURL(%q): %v", tc.raw, err)
			}
			if got != tc.want {
				t.Fatalf("ValidateBaseURL(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

// TestNewWithBaseURLThreadsBaseIntoClient proves the constructor actually
// wires the validated base into the client's request path (not just a
// struct field), using the same localhost-HTTP allowance as the client's
// own tests.
func TestNewWithBaseURLThreadsBaseIntoClient(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"tag_name":"v9.9.9","assets":[]}`)
	}))
	defer server.Close()

	client, err := NewWithBaseURL("", server.URL)
	if err != nil {
		t.Fatalf("NewWithBaseURL(%q): %v", server.URL, err)
	}
	release, err := client.Latest("owner", "repo")
	if err != nil {
		t.Fatalf("Latest via custom base: %v", err)
	}
	if release.TagName != "v9.9.9" {
		t.Fatalf("tag = %q, want v9.9.9", release.TagName)
	}
	if gotPath != "/repos/owner/repo/releases/latest" {
		t.Fatalf("request path = %q", gotPath)
	}
}

// TestNewWithBaseURLEmptyMeansDefault pins that an empty base is not an
// error and yields the built-in default client.
func TestNewWithBaseURLEmptyMeansDefault(t *testing.T) {
	client, err := NewWithBaseURL("tok", "")
	if err != nil {
		t.Fatalf("empty base must mean default, got: %v", err)
	}
	if client.baseURL() != DefaultBaseURL {
		t.Fatalf("base = %q, want default %q", client.baseURL(), DefaultBaseURL)
	}
}

// TestNewWithBaseURLFailsClosed checks the constructor refuses an unusable
// base instead of silently falling back to github.com.
func TestNewWithBaseURLFailsClosed(t *testing.T) {
	client, err := NewWithBaseURL("tok", "http://not-local.example.com")
	if err == nil {
		t.Fatal("expected non-local HTTP base to be refused")
	}
	if !strings.Contains(err.Error(), "HTTPS") {
		t.Fatalf("error should name the HTTPS requirement, got: %v", err)
	}
	if client != nil {
		t.Fatalf("a client was returned alongside the error: %+v", client)
	}
}

// TestNewWithBaseURLKeepsTimeoutsAndSleep verifies NewWithBaseURL produces
// the same operational defaults as New (timeouts, redirect policy seam).
func TestNewWithBaseURLKeepsTimeoutsAndSleep(t *testing.T) {
	client, err := NewWithBaseURL("", "https://ghe.example.com/api/v3")
	if err != nil {
		t.Fatal(err)
	}
	if client.HTTPClient.Timeout != apiTimeout || client.DownloadClient.Timeout != downloadTimeout {
		t.Fatalf("timeouts not configured: api=%v download=%v", client.HTTPClient.Timeout, client.DownloadClient.Timeout)
	}
	if client.Sleep == nil {
		t.Fatal("Sleep seam not configured")
	}
	client.Sleep(time.Millisecond) // must not panic
}
