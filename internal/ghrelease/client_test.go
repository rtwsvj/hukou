package ghrelease

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestLatest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/owner/repo/releases/latest" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer token" {
			t.Fatalf("Authorization=%q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"tag_name":"v1.2.3","assets":[{"name":"tool.tar.gz","browser_download_url":"https://example.test/tool.tar.gz","size":123}]}`)
	}))
	defer server.Close()

	client := testClient(server)
	client.Token = "token"

	release, err := client.Latest("owner", "repo")
	if err != nil {
		t.Fatal(err)
	}
	if release.TagName != "v1.2.3" {
		t.Fatalf("TagName=%q", release.TagName)
	}
	if len(release.Assets) != 1 {
		t.Fatalf("assets=%d", len(release.Assets))
	}
	asset := release.Assets[0]
	if asset.Name != "tool.tar.gz" || asset.BrowserDownloadURL != "https://example.test/tool.tar.gz" || asset.Size != 123 {
		t.Fatalf("asset=%+v", asset)
	}
}

func TestByTag(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/owner/repo/releases/tags/v2.0.0" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"tag_name":"v2.0.0","assets":[]}`)
	}))
	defer server.Close()

	release, err := testClient(server).ByTag("owner", "repo", "v2.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if release.TagName != "v2.0.0" {
		t.Fatalf("TagName=%q", release.TagName)
	}
}

func TestLatestNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "missing", http.StatusNotFound)
	}))
	defer server.Close()

	_, err := testClient(server).Latest("owner", "repo")
	if err == nil {
		t.Fatal("expected error")
	}
	var statusErr *StatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("err=%T %v", err, err)
	}
	if statusErr.StatusCode != http.StatusNotFound {
		t.Fatalf("StatusCode=%d", statusErr.StatusCode)
	}
	if !strings.Contains(err.Error(), "404") {
		t.Fatalf("err=%v", err)
	}
}

func TestRetry429TwiceThenSuccess(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls <= 2 {
			http.Error(w, "slow down", http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"tag_name":"v3.0.0","assets":[]}`)
	}))
	defer server.Close()

	client := testClient(server)
	var sleeps []time.Duration
	client.Sleep = func(d time.Duration) { sleeps = append(sleeps, d) }

	release, err := client.Latest("owner", "repo")
	if err != nil {
		t.Fatal(err)
	}
	if release.TagName != "v3.0.0" {
		t.Fatalf("TagName=%q", release.TagName)
	}
	if calls != 3 {
		t.Fatalf("calls=%d", calls)
	}
	wantSleeps := []time.Duration{time.Second, 2 * time.Second}
	if !reflect.DeepEqual(sleeps, wantSleeps) {
		t.Fatalf("sleeps=%v want %v", sleeps, wantSleeps)
	}
}

func TestRetry429RespectsRetryAfter(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "7")
			http.Error(w, "slow down", http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"tag_name":"v3.1.0","assets":[]}`)
	}))
	defer server.Close()

	client := testClient(server)
	var sleeps []time.Duration
	client.Sleep = func(d time.Duration) { sleeps = append(sleeps, d) }

	release, err := client.Latest("owner", "repo")
	if err != nil {
		t.Fatal(err)
	}
	if release.TagName != "v3.1.0" {
		t.Fatalf("TagName=%q", release.TagName)
	}
	if len(sleeps) != 1 || sleeps[0] != 7*time.Second {
		t.Fatalf("sleeps=%v want [7s]", sleeps)
	}
}

func TestRetry429RetryAfterCappedAt60(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "999")
			http.Error(w, "slow down", http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"tag_name":"v3.2.0","assets":[]}`)
	}))
	defer server.Close()

	client := testClient(server)
	var sleeps []time.Duration
	client.Sleep = func(d time.Duration) { sleeps = append(sleeps, d) }

	if _, err := client.Latest("owner", "repo"); err != nil {
		t.Fatal(err)
	}
	if len(sleeps) != 1 || sleeps[0] != 60*time.Second {
		t.Fatalf("sleeps=%v want [60s]", sleeps)
	}
}

func TestNetworkErrorRetry(t *testing.T) {
	calls := 0
	client := &Client{
		BaseURL: "https://api.example.test",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			calls++
			if calls <= 2 {
				return nil, errors.New("network down")
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"tag_name":"v4.0.0","assets":[]}`)),
				Request:    req,
			}, nil
		})},
	}
	var sleeps []time.Duration
	client.Sleep = func(d time.Duration) { sleeps = append(sleeps, d) }

	release, err := client.Latest("owner", "repo")
	if err != nil {
		t.Fatal(err)
	}
	if release.TagName != "v4.0.0" {
		t.Fatalf("TagName=%q", release.TagName)
	}
	if calls != 3 {
		t.Fatalf("calls=%d", calls)
	}
	wantSleeps := []time.Duration{time.Second, 2 * time.Second}
	if !reflect.DeepEqual(sleeps, wantSleeps) {
		t.Fatalf("sleeps=%v want %v", sleeps, wantSleeps)
	}
}

func Test5xxRetryExhausted(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.Error(w, "unavailable", http.StatusBadGateway)
	}))
	defer server.Close()

	client := testClient(server)
	var sleeps []time.Duration
	client.Sleep = func(d time.Duration) { sleeps = append(sleeps, d) }

	_, err := client.Latest("owner", "repo")
	if err == nil {
		t.Fatal("expected error")
	}
	var statusErr *StatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("err=%T %v", err, err)
	}
	if statusErr.StatusCode != http.StatusBadGateway {
		t.Fatalf("StatusCode=%d", statusErr.StatusCode)
	}
	if calls != 4 {
		t.Fatalf("calls=%d", calls)
	}
	wantSleeps := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}
	if !reflect.DeepEqual(sleeps, wantSleeps) {
		t.Fatalf("sleeps=%v want %v", sleeps, wantSleeps)
	}
}

func TestRateLimitErrorMessage(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Reset", "1700000000")
		http.Error(w, "rate limited", http.StatusForbidden)
	}))
	defer server.Close()

	client := testClient(server)
	var sleeps []time.Duration
	client.Sleep = func(d time.Duration) { sleeps = append(sleeps, d) }

	_, err := client.Latest("owner", "repo")
	if err == nil {
		t.Fatal("expected error")
	}
	var rateErr *RateLimitError
	if !errors.As(err, &rateErr) {
		t.Fatalf("err=%T %v", err, err)
	}
	text := err.Error()
	if !strings.Contains(text, "X-RateLimit-Reset") || !strings.Contains(text, "1700000000") || !strings.Contains(text, "2023-11-14T22:13:20Z") {
		t.Fatalf("err=%v", err)
	}
	if calls != 1 {
		t.Fatalf("calls=%d", calls)
	}
	if len(sleeps) != 0 {
		t.Fatalf("sleeps=%v", sleeps)
	}
}

func TestDownloadWritesTempFileNotFinalName(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/asset.tar.gz" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		io.WriteString(w, "payload")
	}))
	defer server.Close()

	dir := t.TempDir()
	client := testClient(server)
	tmpPath, err := client.Download(server.URL+"/asset.tar.gz", dir, 7)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(tmpPath) != dir {
		t.Fatalf("tmpPath=%s dir=%s", tmpPath, dir)
	}
	if filepath.Base(tmpPath) == "asset.tar.gz" {
		t.Fatalf("tmpPath used final name: %s", tmpPath)
	}
	data, err := os.ReadFile(tmpPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "payload" {
		t.Fatalf("data=%q", data)
	}
	if _, err := os.Stat(filepath.Join(dir, "asset.tar.gz")); !os.IsNotExist(err) {
		t.Fatalf("final name exists or unexpected stat err: %v", err)
	}
}

func TestDownloadSizeMismatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "short")
	}))
	defer server.Close()

	client := testClient(server)
	_, err := client.Download(server.URL+"/a", t.TempDir(), 100)
	if err == nil || !strings.Contains(err.Error(), "size mismatch") {
		t.Fatalf("err=%v", err)
	}
}

func TestDownloadExceedsLimit(t *testing.T) {
	// expectedSize=0 uses 512MiB global cap; use a tiny expected size overflow via LimitReader.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, strings.Repeat("x", 20))
	}))
	defer server.Close()

	client := testClient(server)
	// expectedSize 5 → limit 5, body 20 → exceeded
	_, err := client.Download(server.URL+"/a", t.TempDir(), 5)
	if err == nil || !strings.Contains(err.Error(), "size") {
		t.Fatalf("err=%v", err)
	}
}

func TestDownloadRejectsDisallowedHost(t *testing.T) {
	client := New("")
	_, err := client.Download("https://evil.example.com/asset", t.TempDir(), 0)
	if err == nil || !strings.Contains(err.Error(), "disallowed") {
		t.Fatalf("err=%v", err)
	}
}

func TestRedirectToDisallowedHostRejected(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "https://evil.example.com/loot", http.StatusFound)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client := testClient(server)
	_, err := client.Download(server.URL+"/start", t.TempDir(), 0)
	if err == nil {
		t.Fatal("expected redirect rejection")
	}
	if !strings.Contains(err.Error(), "disallowed") && !strings.Contains(err.Error(), "redirect") {
		t.Fatalf("err=%v", err)
	}
}

func TestAuthorizationNotLeakedOnDownloadRedirect(t *testing.T) {
	// Auth host redirects to a CDN host; Authorization must be stripped.
	cdnAuth := ""
	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cdnAuth = r.Header.Get("Authorization")
		io.WriteString(w, "cdn-payload")
	}))
	defer cdn.Close()

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, cdn.URL+"/file", http.StatusFound)
	}))
	defer api.Close()

	c := &Client{
		BaseURL: api.URL,
		Token:   "secret-token",
		Sleep:   func(time.Duration) {},
	}
	c.HTTPClient = &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// Allow the CDN host for this auth-stripping test only.
			if !c.authHostAllowed(req.URL.Host) {
				req.Header.Del("Authorization")
			}
			return nil
		},
	}

	req, err := http.NewRequest(http.MethodGet, api.URL+"/r", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer secret-token")
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if cdnAuth != "" {
		t.Fatalf("Authorization leaked to CDN: %q", cdnAuth)
	}
}

func TestAuthorizationNotSentOnDownloadRequest(t *testing.T) {
	// Download URL host is BaseURL host (allowed) but should only get auth if
	// authHostAllowed — for custom BaseURL same host DOES get auth. To test
	// that production download hosts don't get auth, use New() defaults and
	// a RoundTripper that records headers without actually dialing.
	var gotAuth string
	client := New("super-secret")
	client.HTTPClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			gotAuth = req.Header.Get("Authorization")
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader("data")),
				Request:    req,
			}, nil
		}),
		CheckRedirect: client.checkRedirect,
	}

	// objects.githubusercontent.com is on the whitelist and must NOT receive Authorization.
	path, err := client.Download("https://objects.githubusercontent.com/github-production-release-asset/1", t.TempDir(), 4)
	if err != nil {
		t.Fatal(err)
	}
	os.Remove(path)
	if gotAuth != "" {
		t.Fatalf("Authorization sent to download host: %q", gotAuth)
	}
}

func TestNewClientHasTimeout(t *testing.T) {
	c := New("")
	if c.HTTPClient == nil || c.HTTPClient.Timeout != 30*time.Second {
		t.Fatalf("Timeout=%v", c.HTTPClient.Timeout)
	}
	if c.HTTPClient.CheckRedirect == nil {
		t.Fatal("CheckRedirect not set")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func testClient(server *httptest.Server) *Client {
	c := &Client{
		BaseURL: server.URL,
		Sleep:   func(time.Duration) {},
	}
	c.HTTPClient = &http.Client{
		// No short Timeout for tests; inherit CheckRedirect from client.
		CheckRedirect: c.checkRedirect,
	}
	return c
}
