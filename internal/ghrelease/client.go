package ghrelease

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultBaseURL     = "https://api.github.com"
	maxRetries         = 3
	apiTimeout         = 30 * time.Second
	downloadTimeout    = 10 * time.Minute
	maxRedirects       = 5
	maxRetryAfter      = 60 * time.Second
	defaultMaxDownload = 512 << 20 // 512 MiB
)

// Production hosts allowed for HTTPS requests and redirects.
var allowedHosts = map[string]struct{}{
	"api.github.com":                       {},
	"github.com":                           {},
	"objects.githubusercontent.com":        {},
	"release-assets.githubusercontent.com": {},
}

type Release struct {
	TagName string  `json:"tag_name"`
	Assets  []Asset `json:"assets"`
}

type Asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

type Client struct {
	BaseURL        string
	Token          string
	HTTPClient     *http.Client // release API requests
	DownloadClient *http.Client // release asset downloads
	Sleep          func(time.Duration)
}

type StatusError struct {
	StatusCode int
	Status     string
	URL        string
	Body       string
}

func (e *StatusError) Error() string {
	if e.Body != "" {
		return fmt.Sprintf("github request %s failed: %s: %s", e.URL, e.Status, e.Body)
	}
	return fmt.Sprintf("github request %s failed: %s", e.URL, e.Status)
}

type RateLimitError struct {
	Reset string
}

func (e *RateLimitError) Error() string {
	if resetTime, err := strconv.ParseInt(e.Reset, 10, 64); err == nil && resetTime > 0 {
		return fmt.Sprintf("github rate limit exceeded; X-RateLimit-Reset: %s (%s)", e.Reset, time.Unix(resetTime, 0).UTC().Format(time.RFC3339))
	}
	if e.Reset != "" {
		return fmt.Sprintf("github rate limit exceeded; X-RateLimit-Reset: %s", e.Reset)
	}
	return "github rate limit exceeded; X-RateLimit-Reset header missing"
}

func New(token string) *Client {
	c := &Client{
		BaseURL: defaultBaseURL,
		Token:   token,
		Sleep:   time.Sleep,
	}
	c.HTTPClient = &http.Client{
		Timeout:       apiTimeout,
		CheckRedirect: c.checkRedirect,
	}
	c.DownloadClient = &http.Client{
		Timeout:       downloadTimeout,
		CheckRedirect: c.checkRedirect,
	}
	return c
}

func NewClient(token string) *Client {
	return New(token)
}

func (c *Client) Latest(owner, repo string) (Release, error) {
	return c.fetchRelease("/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/releases/latest")
}

func (c *Client) ByTag(owner, repo, tag string) (Release, error) {
	return c.fetchRelease("/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/releases/tags/" + url.PathEscape(tag))
}

// Download streams downloadURL into a temporary file under destDir.
// expectedSize, when > 0, is the Asset.Size from the release API: the body is
// limited to expectedSize+1 bytes and the actual count must match exactly.
// When expectedSize is 0, a global cap of 512 MiB applies.
func (c *Client) Download(downloadURL, destDir string, expectedSize int64) (string, error) {
	u, err := url.Parse(downloadURL)
	if err != nil {
		return "", err
	}
	if err := c.validateURL(u); err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(context.Background(), downloadTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return "", err
	}
	c.applyHeaders(req)

	resp, err := c.do(req, c.downloadHTTPClient())
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", responseStatusError(resp)
	}

	limit := expectedSize
	if limit <= 0 {
		limit = defaultMaxDownload
	}

	file, err := os.CreateTemp(destDir, ".ghrelease-*")
	if err != nil {
		return "", err
	}
	tmpPath := file.Name()

	n, err := io.Copy(file, io.LimitReader(resp.Body, limit+1))
	if err != nil {
		file.Close()
		os.Remove(tmpPath)
		return "", err
	}
	if n > limit {
		file.Close()
		os.Remove(tmpPath)
		return "", fmt.Errorf("download exceeded size limit of %d bytes", limit)
	}
	if expectedSize > 0 && n != expectedSize {
		file.Close()
		os.Remove(tmpPath)
		return "", fmt.Errorf("download size mismatch: got %d bytes, expected %d", n, expectedSize)
	}
	if err := file.Close(); err != nil {
		os.Remove(tmpPath)
		return "", err
	}

	return tmpPath, nil
}

func (c *Client) fetchRelease(path string) (Release, error) {
	var release Release
	req, err := http.NewRequest(http.MethodGet, c.apiURL(path), nil)
	if err != nil {
		return release, err
	}
	if err := c.validateURL(req.URL); err != nil {
		return release, err
	}
	c.applyHeaders(req)

	resp, err := c.do(req, c.httpClient())
	if err != nil {
		return release, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return release, responseStatusError(resp)
	}

	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return release, err
	}
	return release, nil
}

func (c *Client) do(req *http.Request, client *http.Client) (*http.Response, error) {
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		// Re-apply headers each attempt; body is nil for our GET requests.
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			if attempt < maxRetries {
				c.sleep(backoff(attempt))
				continue
			}
			return nil, fmt.Errorf("github request %s failed after %d attempts: %w", req.URL.String(), attempt+1, err)
		}

		if resp.StatusCode == http.StatusForbidden && resp.Header.Get("X-RateLimit-Remaining") == "0" {
			drainClose(resp.Body)
			return nil, &RateLimitError{Reset: resp.Header.Get("X-RateLimit-Reset")}
		}

		if retryStatus(resp.StatusCode) && attempt < maxRetries {
			delay := retryDelay(resp, attempt)
			drainClose(resp.Body)
			c.sleep(delay)
			continue
		}

		return resp, nil
	}
	return nil, lastErr
}

// retryDelay prefers Retry-After (capped at 60s) for 429; otherwise exponential backoff.
func retryDelay(resp *http.Response, attempt int) time.Duration {
	if resp.StatusCode == http.StatusTooManyRequests {
		if ra := resp.Header.Get("Retry-After"); ra != "" {
			if secs, err := strconv.Atoi(ra); err == nil && secs >= 0 {
				d := time.Duration(secs) * time.Second
				if d > maxRetryAfter {
					d = maxRetryAfter
				}
				return d
			}
			if t, err := http.ParseTime(ra); err == nil {
				d := time.Until(t)
				if d < 0 {
					d = 0
				}
				if d > maxRetryAfter {
					d = maxRetryAfter
				}
				return d
			}
		}
	}
	return backoff(attempt)
}

func (c *Client) apiURL(path string) string {
	return strings.TrimRight(c.baseURL(), "/") + path
}

func (c *Client) baseURL() string {
	if c.BaseURL == "" {
		return defaultBaseURL
	}
	return c.BaseURL
}

func (c *Client) httpClient() *http.Client {
	if c.HTTPClient == nil {
		c.HTTPClient = &http.Client{
			Timeout:       apiTimeout,
			CheckRedirect: c.checkRedirect,
		}
	}
	// Ensure CheckRedirect is set when callers supply a custom client without one
	// (e.g. httptest). Preserve an already-configured CheckRedirect.
	if c.HTTPClient.CheckRedirect == nil {
		c.HTTPClient.CheckRedirect = c.checkRedirect
	}
	return c.HTTPClient
}

// downloadHTTPClient returns the client dedicated to long-running asset
// downloads. Manually constructed Clients from older callers may provide only
// HTTPClient; retain that fallback while New always configures separate API and
// download timeouts.
func (c *Client) downloadHTTPClient() *http.Client {
	if c.DownloadClient == nil {
		if c.HTTPClient != nil {
			if c.HTTPClient.CheckRedirect == nil {
				c.HTTPClient.CheckRedirect = c.checkRedirect
			}
			return c.HTTPClient
		}
		c.DownloadClient = &http.Client{
			Timeout:       downloadTimeout,
			CheckRedirect: c.checkRedirect,
		}
	}
	if c.DownloadClient.CheckRedirect == nil {
		c.DownloadClient.CheckRedirect = c.checkRedirect
	}
	return c.DownloadClient
}

func (c *Client) sleep(d time.Duration) {
	if c.Sleep != nil {
		c.Sleep(d)
		return
	}
	time.Sleep(d)
}

// applyHeaders sets Accept always. Authorization is only attached when the
// request host is allowed to receive the token (api.github.com, or a custom
// BaseURL host used in tests).
func (c *Client) applyHeaders(req *http.Request) {
	req.Header.Set("Accept", "application/vnd.github+json")
	if c.Token != "" && c.authHostAllowed(req.URL.Host) {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
}

func (c *Client) authHostAllowed(host string) bool {
	if host == "api.github.com" {
		return true
	}
	// Custom BaseURL (tests / mirrors): allow auth only to that host.
	if c.baseURL() != defaultBaseURL {
		if base, err := url.Parse(c.baseURL()); err == nil && base.Host == host {
			return true
		}
	}
	return false
}

// validateURL enforces HTTPS + host whitelist for production, and allows the
// custom BaseURL host (http or https) for tests/mirrors.
func (c *Client) validateURL(u *url.URL) error {
	if u == nil || u.Host == "" {
		return fmt.Errorf("invalid URL: missing host")
	}
	if c.hostAllowed(u) {
		return nil
	}
	return fmt.Errorf("refusing request to disallowed host %q (scheme %q)", u.Host, u.Scheme)
}

func (c *Client) hostAllowed(u *url.URL) bool {
	host := u.Hostname() // strip port for comparison where relevant
	// Full host (with port) for custom BaseURL matching (httptest uses host:port).
	fullHost := u.Host

	// Custom BaseURL host is permitted (tests).
	if c.baseURL() != defaultBaseURL {
		if base, err := url.Parse(c.baseURL()); err == nil && base.Host != "" {
			if fullHost == base.Host || host == base.Hostname() {
				return u.Scheme == "http" || u.Scheme == "https"
			}
		}
	}

	if u.Scheme != "https" {
		return false
	}
	_, ok := allowedHosts[host]
	return ok
}

func (c *Client) checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return fmt.Errorf("stopped after %d redirects", maxRedirects)
	}
	if err := c.validateURL(req.URL); err != nil {
		return err
	}
	// Never forward Authorization to non-auth hosts (e.g. download CDNs).
	if !c.authHostAllowed(req.URL.Host) {
		req.Header.Del("Authorization")
	}
	return nil
}

func retryStatus(code int) bool {
	return code == http.StatusTooManyRequests || code >= 500
}

func backoff(attempt int) time.Duration {
	return time.Second << attempt
}

func responseStatusError(resp *http.Response) error {
	return &StatusError{
		StatusCode: resp.StatusCode,
		Status:     resp.Status,
		URL:        resp.Request.URL.String(),
		Body:       readErrorBody(resp.Body),
	}
}

func readErrorBody(body io.Reader) string {
	data, err := io.ReadAll(io.LimitReader(body, 4096))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func drainClose(body io.ReadCloser) {
	io.Copy(io.Discard, io.LimitReader(body, 4096))
	body.Close()
}
