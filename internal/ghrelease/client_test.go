package ghrelease

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rtwsvj/hukou/internal/sysproxy"
)

func TestDownloadRefusesNegativeExpectedSize(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		io.WriteString(w, "x")
	}))
	defer server.Close()

	client := testClient(server)
	client.BaseURL = server.URL
	if _, err := client.Download(server.URL+"/tool.bin", t.TempDir(), -1); err == nil {
		t.Fatal("expected negative declared size to be refused")
	}
	if requests.Load() != 0 {
		t.Fatalf("refusal must happen before any request, saw %d", requests.Load())
	}
}

func TestDownloadZeroByteAssetSucceeds(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := testClient(server)
	client.BaseURL = server.URL
	path, err := client.Download(server.URL+"/empty.bin", t.TempDir(), 0)
	if err != nil {
		t.Fatalf("declared-unknown zero-byte asset must succeed: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != 0 {
		t.Fatalf("size=%d, want 0", info.Size())
	}
}

func TestFetchJSONRejectsTrailingGarbage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"tag_name":"v1.0.0"} trailing junk`)
	}))
	defer server.Close()

	c := testClient(server)
	release, err := c.Latest("owner", "repo")
	if err == nil {
		t.Fatal("expected trailing garbage after the JSON value to fail closed")
	}
	if release.TagName != "" {
		t.Fatalf("decoded despite trailing garbage: %+v", release)
	}
}

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
		io.WriteString(w, `{"tag_name":"v2.0.0","draft":false,"prerelease":true,"assets":[]}`)
	}))
	defer server.Close()

	release, err := testClient(server).ByTag("owner", "repo", "v2.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if release.TagName != "v2.0.0" {
		t.Fatalf("TagName=%q", release.TagName)
	}
	if release.Draft || !release.Prerelease {
		t.Fatalf("release metadata=%+v", release)
	}
}

func TestListUsesBoundedGeneratedPagination(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/repos/owner/repo/releases" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		if got := r.URL.Query().Get("per_page"); got != "100" {
			t.Fatalf("per_page=%q", got)
		}
		if got := r.URL.Query().Get("page"); got != strconv.Itoa(calls) {
			t.Fatalf("page=%q calls=%d", got, calls)
		}
		// List must ignore an untrusted Link target and construct page 2 itself.
		w.Header().Set("Link", `<https://evil.example.test/releases?page=2>; rel="next"`)
		w.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			batch := make([]Release, releasesPerPage)
			for i := range batch {
				batch[i] = Release{TagName: "v1.0.0"}
			}
			if err := json.NewEncoder(w).Encode(batch); err != nil {
				t.Fatal(err)
			}
			return
		}
		if err := json.NewEncoder(w).Encode([]Release{{TagName: "v2.0.0-beta.1", Draft: true, Prerelease: true}}); err != nil {
			t.Fatal(err)
		}
	}))
	defer server.Close()

	releases, err := testClient(server).List("owner", "repo")
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || len(releases) != releasesPerPage+1 {
		t.Fatalf("calls=%d releases=%d", calls, len(releases))
	}
	last := releases[len(releases)-1]
	if !last.Draft || !last.Prerelease || last.TagName != "v2.0.0-beta.1" {
		t.Fatalf("last release=%+v", last)
	}
}

func TestListFailsClosedAtPaginationLimit(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		batch := make([]Release, releasesPerPage)
		if err := json.NewEncoder(w).Encode(batch); err != nil {
			t.Fatal(err)
		}
	}))
	defer server.Close()

	_, err := testClient(server).List("owner", "repo")
	if err == nil || !strings.Contains(err.Error(), "safe pagination limit") {
		t.Fatalf("error=%v", err)
	}
	if calls != maxReleasePages {
		t.Fatalf("calls=%d want=%d", calls, maxReleasePages)
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
	client.DownloadClient = &http.Client{
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
		t.Fatalf("API Timeout=%v", c.HTTPClient.Timeout)
	}
	if c.HTTPClient.CheckRedirect == nil {
		t.Fatal("API CheckRedirect not set")
	}
	if c.DownloadClient == nil || c.DownloadClient.Timeout != 10*time.Minute {
		t.Fatalf("download Timeout=%v", c.DownloadClient.Timeout)
	}
	if c.DownloadClient.CheckRedirect == nil {
		t.Fatal("download CheckRedirect not set")
	}
}

func TestLatestUsesAPIClient(t *testing.T) {
	apiCalls := 0
	downloadCalls := 0
	c := &Client{
		BaseURL: "https://api.example.test",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			apiCalls++
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"tag_name":"v1.0.0","assets":[]}`)),
				Request:    req,
			}, nil
		})},
		DownloadClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			downloadCalls++
			return nil, errors.New("download client must not handle API requests")
		})},
		Sleep: func(time.Duration) {},
	}

	if _, err := c.Latest("owner", "repo"); err != nil {
		t.Fatal(err)
	}
	if apiCalls != 1 || downloadCalls != 0 {
		t.Fatalf("apiCalls=%d downloadCalls=%d", apiCalls, downloadCalls)
	}
}

func TestDownloadUsesDownloadClient(t *testing.T) {
	apiCalls := 0
	downloadCalls := 0
	c := &Client{
		BaseURL: "https://download.example.test",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			apiCalls++
			return nil, errors.New("API client must not handle downloads")
		})},
		DownloadClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			downloadCalls++
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader("data")),
				Request:    req,
			}, nil
		})},
		Sleep: func(time.Duration) {},
	}

	path, err := c.Download("https://download.example.test/asset", t.TempDir(), 4)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)
	if apiCalls != 0 || downloadCalls != 1 {
		t.Fatalf("apiCalls=%d downloadCalls=%d", apiCalls, downloadCalls)
	}
}

func TestDownloadNotBoundByAPITimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(20 * time.Millisecond)
		io.WriteString(w, "data")
	}))
	defer server.Close()

	downloadClient := server.Client()
	downloadClient.Timeout = time.Second
	c := &Client{
		BaseURL:        server.URL,
		HTTPClient:     &http.Client{Timeout: time.Nanosecond},
		DownloadClient: downloadClient,
		Sleep:          func(time.Duration) {},
	}

	path, err := c.Download(server.URL+"/asset", t.TempDir(), 4)
	if err != nil {
		t.Fatalf("download inherited API timeout: %v", err)
	}
	defer os.Remove(path)
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
	c.DownloadClient = c.HTTPClient
	return c
}

func TestSearchRepositories(t *testing.T) {
	var gotQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search/repositories" {
			http.NotFound(w, r)
			return
		}
		gotQuery = r.URL.Query().Get("q")
		if r.URL.Query().Get("per_page") != "10" || r.URL.Query().Get("sort") != "stars" {
			t.Errorf("unexpected params: %s", r.URL.RawQuery)
		}
		io.WriteString(w, `{"total_count":2,"items":[{"full_name":"owner/mytool","description":"desc","stargazers_count":12,"updated_at":"2026-08-01T00:00:00Z","archived":false},{"full_name":"other/x","stargazers_count":1}]}`)
	}))
	defer server.Close()
	c := New("")
	c.BaseURL = server.URL
	c.Sleep = func(time.Duration) {}

	items, err := c.SearchRepositories("my tool in:name", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].FullName != "owner/mytool" || items[0].StargazersCount != 12 || !items[0].Archived == false {
		t.Fatalf("unexpected items: %+v", items)
	}
	if gotQuery != "my tool in:name" {
		t.Fatalf("query not encoded/decoded correctly: %q", gotQuery)
	}
}

func TestSearchRepositoriesRateLimited(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Reset", "9999999999")
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()
	c := New("")
	c.BaseURL = server.URL
	c.Sleep = func(time.Duration) {}
	if _, err := c.SearchRepositories("x", 10); err == nil {
		t.Fatal("expected rate limit error")
	} else {
		var rl *RateLimitError
		if !errors.As(err, &rl) {
			t.Fatalf("expected RateLimitError, got %T: %v", err, err)
		}
	}
}

// TestStatusErrorBodySanitized: a server-controlled error body is stripped of
// control characters at ingestion, so ANSI/OSC payloads can never ride a
// StatusError onto the terminal.
func TestStatusErrorBodySanitized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom \x1b[2J\x1b]52;c;aGVsbG8=\x07 end"))
	}))
	defer server.Close()

	_, err := testClient(server).Latest("owner", "repo")
	var statusErr *StatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("err=%T %v", err, err)
	}
	if strings.Contains(statusErr.Body, "\x1b") || strings.Contains(statusErr.Body, "\x07") {
		t.Fatalf("escape sequence survived ingestion: %q", statusErr.Body)
	}
	if !strings.Contains(statusErr.Body, "boom ?[2J?]52;c;aGVsbG8=? end") {
		t.Fatalf("body not sanitized as expected: %q", statusErr.Body)
	}
}

// TestRetry403WithRetryAfter: GitHub's secondary rate limit (403 with a
// Retry-After header, X-RateLimit-Remaining NOT zero) is retried like 429.
func TestRetry403WithRetryAfter(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tag_name":"v1.0.0"}`))
	}))
	defer server.Close()

	rel, err := testClient(server).Latest("owner", "repo")
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if rel.TagName != "v1.0.0" {
		t.Fatalf("tag=%q", rel.TagName)
	}
	if calls != 2 {
		t.Fatalf("calls=%d, want 2 (403+Retry-After retried once)", calls)
	}
}

// TestStatusErrorStripsQueryFromURL: a signed download URL's credentials must
// never appear in the error text — only scheme://host/path survives.
func TestStatusErrorStripsQueryFromURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "denied", http.StatusForbidden)
	}))
	defer server.Close()

	c := testClient(server)
	_, err := c.Download(server.URL+"/asset.tar.gz?X-Amz-Signature=deadbeef&X-Amz-Credential=abc", t.TempDir(), 0)
	if err == nil {
		t.Fatal("expected error")
	}
	var statusErr *StatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("err=%T %v", err, err)
	}
	for _, leak := range []string{"X-Amz-Signature", "deadbeef", "X-Amz-Credential"} {
		if strings.Contains(err.Error(), leak) || strings.Contains(statusErr.URL, leak) {
			t.Fatalf("signed URL query leaked into error: %v (url=%q)", err, statusErr.URL)
		}
	}
	if !strings.HasSuffix(statusErr.URL, "/asset.tar.gz") {
		t.Fatalf("sanitized URL lost its path: %q", statusErr.URL)
	}
}

// TestTransportErrorStripsQueryFromURLError: a transport failure wraps a
// *url.Error whose Error() would render the signed query; the surfaced error
// must carry only the query-stripped URL.
func TestTransportErrorStripsQueryFromURLError(t *testing.T) {
	// No proxy retry in this test: stub the system proxy away (the host may
	// have a real one) so the transport failure surfaces immediately.
	orig := sysproxy.SystemProxyURL
	sysproxy.SystemProxyURL = func() *url.URL { return nil }
	defer func() { sysproxy.SystemProxyURL = orig }()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	serverURL := server.URL
	server.Close() // every request now fails at the transport layer

	c := testClient(server)
	c.Sleep = func(time.Duration) {} // no backoff wait in tests
	_, err := c.Download(serverURL+"/asset.tar.gz?X-Amz-Signature=deadbeef&X-Amz-Credential=abc", t.TempDir(), 0)
	if err == nil {
		t.Fatal("expected a transport error")
	}
	for _, leak := range []string{"X-Amz-Signature", "deadbeef", "X-Amz-Credential"} {
		if strings.Contains(err.Error(), leak) {
			t.Fatalf("signed query leaked into transport error: %v", err)
		}
	}
	if !strings.Contains(err.Error(), "/asset.tar.gz") {
		t.Fatalf("error lost the URL path entirely: %v", err)
	}
}

// TestStatusErrorBodyDoesNotTriggerProxyRetry: a *StatusError's body is
// server-controlled text; if it happens to contain transport-flavored
// substrings ("unexpected EOF", "connection reset") the proxy retry decision
// must still say no. Before the *StatusError guard in retryableDownloadErr,
// these bodies flipped the decision via substring matching.
func TestStatusErrorBodyDoesNotTriggerProxyRetry(t *testing.T) {
	for _, body := range []string{"unexpected EOF", "read: connection reset by peer", "dial: connection refused"} {
		err := fmt.Errorf("download attempt failed: %w", &StatusError{
			StatusCode: http.StatusBadGateway,
			Status:     "502 Bad Gateway",
			URL:        "https://example.test/asset.tar.gz",
			Body:       body,
		})
		if retryableDownloadErr(err) {
			t.Fatalf("body %q: status error treated as retryable", body)
		}
	}
}

// TestDownload502WithEOFBodyNotProxied: a 502 whose error body contains "EOF"
// must surface as a StatusError without a second round of attempts through
// the system proxy — the request count stays at exactly maxRetries+1.
func TestDownload502WithEOFBodyNotProxied(t *testing.T) {
	var requests int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requests, 1)
		w.WriteHeader(http.StatusBadGateway)
		io.WriteString(w, "unexpected EOF")
	}))
	defer server.Close()

	c := testClient(server)
	c.Sleep = func(time.Duration) {}
	_, err := c.Download(server.URL+"/asset.tar.gz", t.TempDir(), 10)
	if err == nil {
		t.Fatal("expected an error")
	}
	var statusErr *StatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("expected *StatusError, got %T: %v", err, err)
	}
	if got := atomic.LoadInt32(&requests); got != maxRetries+1 {
		t.Fatalf("requests=%d, want exactly %d (a proxy retry would add more)", got, maxRetries+1)
	}
	if retryableDownloadErr(err) {
		t.Fatal("502 with EOF-bearing body classified as retryable")
	}
}

// TestRateLimitResetHeaderSanitized: the X-RateLimit-Reset value is
// server-controlled and rendered verbatim by RateLimitError.Error() when it
// does not parse as a timestamp; control characters must be stripped at
// ingestion so ANSI/OSC payloads cannot ride the error onto the terminal.
func TestRateLimitResetHeaderSanitized(t *testing.T) {
	hostile := "\x1b]0;pwned\x071700000000"
	c := New("")
	c.Sleep = func(time.Duration) {}
	c.HTTPClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		resp := &http.Response{
			StatusCode: http.StatusForbidden,
			Status:     "403 Forbidden",
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader("rate limit exceeded")),
			Request:    req,
		}
		resp.Header.Set("X-RateLimit-Remaining", "0")
		resp.Header.Set("X-RateLimit-Reset", hostile)
		return resp, nil
	})}

	_, err := c.Latest("owner", "repo")
	if err == nil {
		t.Fatal("expected a rate-limit error")
	}
	msg := err.Error()
	for _, bad := range []string{"\x1b", "\x07", "pwned]0;"} {
		if strings.Contains(msg, bad) {
			t.Fatalf("hostile Reset header leaked into error message: %q", msg)
		}
	}
	if !strings.Contains(msg, "1700000000") {
		t.Fatalf("sanitized Reset lost its numeric payload: %q", msg)
	}
}

// TestDownloadRefusesOversizedExpectedSizeBeforeRequest: a declared size
// above the global cap is refused before any request leaves the process, so
// a hostile manifest cannot even start a multi-gigabyte fetch.
func TestDownloadRefusesOversizedExpectedSizeBeforeRequest(t *testing.T) {
	var requests int32
	c := New("")
	c.Sleep = func(time.Duration) {}
	c.DownloadClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		atomic.AddInt32(&requests, 1)
		return nil, errors.New("request must never be made")
	})}

	twoGiB := int64(2) << 30
	_, err := c.Download("https://github.com/owner/repo/releases/download/v1/asset.tar.gz", t.TempDir(), twoGiB)
	if err == nil {
		t.Fatal("expected refusal")
	}
	if !strings.Contains(err.Error(), strconv.FormatInt(twoGiB, 10)) || !strings.Contains(err.Error(), strconv.FormatInt(defaultMaxDownload, 10)) {
		t.Fatalf("refusal must name both sizes, got: %v", err)
	}
	if got := atomic.LoadInt32(&requests); got != 0 {
		t.Fatalf("requests=%d, want 0 (refusal must happen before any request)", got)
	}
}

// TestFetchJSONFailsClosedOverCap: a JSON response larger than
// defaultMaxJSONBytes is truncated by the bounded reader and json.Decode
// fails closed instead of buffering unbounded server-controlled bytes. The
// body is deliberately valid JSON when read in full, so only the cap can
// produce the failure.
func TestFetchJSONFailsClosedOverCap(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"tag_name":"`)
		io.WriteString(w, strings.Repeat("a", defaultMaxJSONBytes+(4<<20)))
		io.WriteString(w, `"}`)
	}))
	defer server.Close()

	c := testClient(server)
	release, err := c.Latest("owner", "repo")
	if err == nil {
		t.Fatal("expected truncation to fail the decode")
	}
	if release.TagName != "" {
		t.Fatalf("partial decode leaked through: %+v", release)
	}
}
