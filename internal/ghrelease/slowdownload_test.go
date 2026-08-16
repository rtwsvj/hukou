package ghrelease

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rtwsvj/hukou/internal/sysproxy"
)

// TestDownloadRetriesThroughSystemProxyWhenSlow: the direct server dribbles
// bytes below the configured floor; the proxy server serves the asset fast.
// The client must abort the direct attempt, retry through the proxy, and
// deliver the correct bytes — with the advisory logged.
func TestDownloadRetriesThroughSystemProxyWhenSlow(t *testing.T) {
	asset := strings.Repeat("A", 256*1024)

	var directHits, proxyHits atomic.Int64
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		directHits.Add(1)
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(asset)))
		// Dribble ~100 B/s so the floor trips immediately after the grace.
		for i := 0; i < len(asset); i += 64 {
			end := i + 64
			if end > len(asset) {
				end = len(asset)
			}
			if _, err := w.Write([]byte(asset[i:end])); err != nil {
				return
			}
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			time.Sleep(500 * time.Millisecond)
		}
	}))
	defer slow.Close()

	fast := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyHits.Add(1)
		if r.URL.Scheme == "" || r.URL.Host == "" {
			t.Errorf("proxy request not absolute-form: %s", r.URL.String())
		}
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(asset)))
		_, _ = w.Write([]byte(asset))
	}))
	defer fast.Close()

	// Route the client's "system proxy" at the fast server.
	orig := sysproxy.SystemProxyURL
	sysproxy.SystemProxyURL = func() *url.URL {
		u, _ := url.Parse(fast.URL)
		return u
	}
	defer func() { sysproxy.SystemProxyURL = orig }()

	// Tune thresholds down so the test runs quickly.
	t.Setenv("HUKOU_DOWNLOAD_MIN_SPEED", "10000")
	t.Setenv("HUKOU_DOWNLOAD_GRACE", "500ms")

	// Point the direct download at the slow server (validateURL requires an
	// allowed host — use a client with BaseURL and bypass via test hook).
	c := New("")
	c.BaseURL = slow.URL
	c.Sleep = func(time.Duration) {}
	var logged []string
	c.Log = func(msg string) { logged = append(logged, msg) }

	// Download via the slow server's /asset path.
	dest := t.TempDir()
	path, err := c.Download(slow.URL+"/asset", dest, int64(len(asset)))
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != asset {
		t.Fatalf("wrong bytes: %d vs %d", len(got), len(asset))
	}
	if directHits.Load() < 1 || proxyHits.Load() < 1 {
		t.Fatalf("expected both attempts: direct=%d proxy=%d", directHits.Load(), proxyHits.Load())
	}
	if len(logged) == 0 || !strings.Contains(logged[0], "system proxy") {
		t.Fatalf("retry advisory not logged: %v", logged)
	}
	_ = filepath.Join(dest, "x")
}

// TestDownloadNoProxyRetryWithoutSystemProxy: when no system proxy exists,
// the slow download just fails (no second attempt).
func TestDownloadNoProxyRetryWithoutSystemProxy(t *testing.T) {
	orig := sysproxy.SystemProxyURL
	sysproxy.SystemProxyURL = func() *url.URL { return nil }
	defer func() { sysproxy.SystemProxyURL = orig }()

	t.Setenv("HUKOU_DOWNLOAD_MIN_SPEED", "10000")
	t.Setenv("HUKOU_DOWNLOAD_GRACE", "200ms")

	var hits atomic.Int64
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Length", "1048576")
		_, _ = io.WriteString(w, "x")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		time.Sleep(2 * time.Second)
	}))
	defer slow.Close()

	c := New("")
	c.BaseURL = slow.URL
	c.Sleep = func(time.Duration) {}
	if _, err := c.Download(slow.URL+"/asset", t.TempDir(), 1048576); err == nil {
		t.Fatal("expected failure without proxy")
	}
	if hits.Load() != 1 {
		t.Fatalf("expected exactly one direct attempt, got %d", hits.Load())
	}
}
