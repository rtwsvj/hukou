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
	client := &Client{HTTPClient: server.Client(), Sleep: func(time.Duration) {}}
	tmpPath, err := client.Download(server.URL+"/asset.tar.gz", dir)
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

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func testClient(server *httptest.Server) *Client {
	return &Client{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
		Sleep:      func(time.Duration) {},
	}
}
