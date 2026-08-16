package ghrelease

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/rtwsvj/hukou/internal/i18n"
	"github.com/rtwsvj/hukou/internal/sysproxy"
	"io"
	"net"
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
	releasesPerPage    = 100
	maxReleasePages    = 10
)

// Production hosts allowed for HTTPS requests and redirects.
var allowedHosts = map[string]struct{}{
	"api.github.com":                       {},
	"github.com":                           {},
	"objects.githubusercontent.com":        {},
	"release-assets.githubusercontent.com": {},
}

type Release struct {
	TagName    string  `json:"tag_name"`
	Draft      bool    `json:"draft"`
	Prerelease bool    `json:"prerelease"`
	Body       string  `json:"body"`
	Assets     []Asset `json:"assets"`
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
	// Log, when set, receives human-facing advisory lines (e.g. the
	// slow-download proxy retry). It is safe to leave nil.
	Log func(msg string)
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

// SearchRepoItem is one /search/repositories hit.
type SearchRepoItem struct {
	FullName        string `json:"full_name"`
	Description     string `json:"description"`
	StargazersCount int    `json:"stargazers_count"`
	UpdatedAt       string `json:"updated_at"`
	Archived        bool   `json:"archived"`
}

// SearchRepositories queries GitHub repository search, ranked by stars
// descending. limit bounds per_page (clamped to [1,20]; default 10). The query
// is passed verbatim and URL-encoded here.
func (c *Client) SearchRepositories(query string, limit int) ([]SearchRepoItem, error) {
	if limit <= 0 || limit > 20 {
		limit = 10
	}
	path := "/search/repositories?q=" + url.QueryEscape(query) + "&per_page=" + strconv.Itoa(limit) + "&sort=stars&order=desc"
	var out struct {
		Items []SearchRepoItem `json:"items"`
	}
	if err := c.fetchJSON(path, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

func (c *Client) ByTag(owner, repo, tag string) (Release, error) {
	return c.fetchRelease("/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/releases/tags/" + url.PathEscape(tag))
}

// List returns release metadata newest-first using bounded, client-generated
// pagination. It deliberately does not follow the response Link header: every
// request stays on the validated API base URL and the requested repository.
// A repository with more than maxReleasePages full pages fails closed instead
// of silently selecting from an incomplete candidate set.
func (c *Client) List(owner, repo string) ([]Release, error) {
	basePath := "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/releases"
	releases := make([]Release, 0, releasesPerPage)
	for page := 1; page <= maxReleasePages; page++ {
		query := url.Values{}
		query.Set("per_page", strconv.Itoa(releasesPerPage))
		query.Set("page", strconv.Itoa(page))

		var batch []Release
		if err := c.fetchJSON(basePath+"?"+query.Encode(), &batch); err != nil {
			return nil, err
		}
		if len(batch) > releasesPerPage {
			return nil, i18n.Errorf("github releases page %d returned %d items; maximum is %d", page, len(batch), releasesPerPage)
		}
		releases = append(releases, batch...)
		if len(batch) < releasesPerPage {
			return releases, nil
		}
	}
	return nil, i18n.Errorf("github releases exceed safe pagination limit of %d items", releasesPerPage*maxReleasePages)
}

// Download streams downloadURL into a temporary file under destDir.
// expectedSize, when > 0, is the Asset.Size from the release API: the body is
// limited to expectedSize+1 bytes and the actual count must match exactly.
// When expectedSize is 0, a global cap of 512 MiB applies.
//
// When the direct attempt stalls (measured throughput below the configured
// floor for the configured grace period) or fails with a transport error, the
// download is retried once through the host's system proxy (macOS
// SystemConfiguration or the standard proxy environment variables). Server
// responses (4xx/5xx) and deterministic limit violations are never retried.
func (c *Client) Download(downloadURL, destDir string, expectedSize int64) (string, error) {
	u, err := url.Parse(downloadURL)
	if err != nil {
		return "", err
	}
	if err := c.validateURL(u); err != nil {
		return "", err
	}
	path, err := c.downloadWithTransport(downloadURL, destDir, expectedSize, nil)
	if err == nil {
		return path, nil
	}
	if !retryableDownloadErr(err) {
		return "", err
	}
	proxy := sysproxy.SystemProxyURL()
	if proxy == nil {
		return "", err
	}
	if c.Log != nil {
		c.Log(i18n.T("download stalled; retrying via system proxy %s", proxy.Host))
	}
	retryPath, retryErr := c.downloadWithTransport(downloadURL, destDir, expectedSize, proxy)
	if retryErr != nil {
		return "", errors.Join(err, i18n.Errorf("retry via system proxy %s also failed: %v", proxy.Host, retryErr))
	}
	return retryPath, nil
}

// downloadWithTransport performs one download attempt; proxyURL == nil uses
// the client's default transport, otherwise a fresh transport that routes all
// requests through the proxy.
func (c *Client) downloadWithTransport(downloadURL, destDir string, expectedSize int64, proxyURL *url.URL) (string, error) {
	client := c.downloadHTTPClient()
	if proxyURL != nil {
		base, ok := http.DefaultTransport.(*http.Transport)
		if !ok {
			return "", i18n.Errorf("cannot build proxy transport")
		}
		tr := base.Clone()
		tr.Proxy = http.ProxyURL(proxyURL)
		client = &http.Client{
			Timeout:       downloadTimeout,
			CheckRedirect: c.checkRedirect,
			Transport:     tr,
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		t := time.NewTimer(downloadTimeout)
		defer t.Stop()
		select {
		case <-t.C:
			cancel()
		case <-ctx.Done():
		}
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return "", err
	}
	c.applyHeaders(req)

	resp, err := c.do(req, client)
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

	body := newSpeedLimitedReader(ctx, cancel, resp.Body)
	n, err := io.Copy(file, io.LimitReader(body, limit+1))
	if err != nil {
		file.Close()
		os.Remove(tmpPath)
		if body.Slow() {
			return "", errSlowDownload
		}
		return "", err
	}
	if n > limit {
		file.Close()
		os.Remove(tmpPath)
		return "", i18n.Errorf("download exceeded size limit of %d bytes", limit)
	}
	if expectedSize > 0 && n != expectedSize {
		file.Close()
		os.Remove(tmpPath)
		return "", i18n.Errorf("download size mismatch: got %d bytes, expected %d", n, expectedSize)
	}
	if err := file.Close(); err != nil {
		os.Remove(tmpPath)
		return "", err
	}
	return tmpPath, nil
}

// retryableDownloadErr reports whether the failure is worth one proxy retry:
// our own slow-transfer sentinel, transport-level errors (dial/reset/EOF),
// and timeouts. HTTP status errors and size-limit violations are not.
func retryableDownloadErr(err error) bool {
	if errors.Is(err, errSlowDownload) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}
	if strings.Contains(err.Error(), "connection reset") || strings.Contains(err.Error(), "connection refused") || strings.Contains(err.Error(), "EOF") {
		return true
	}
	return false
}

func (c *Client) fetchRelease(path string) (Release, error) {
	var release Release
	if err := c.fetchJSON(path, &release); err != nil {
		return Release{}, err
	}
	return release, nil
}

func (c *Client) fetchJSON(path string, target any) error {
	req, err := http.NewRequest(http.MethodGet, c.apiURL(path), nil)
	if err != nil {
		return err
	}
	if err := c.validateURL(req.URL); err != nil {
		return err
	}
	c.applyHeaders(req)

	resp, err := c.do(req, c.httpClient())
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return responseStatusError(resp)
	}

	if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
		return err
	}
	return nil
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
			return nil, i18n.Wrapf("github request %s failed after %d attempts: %w", err, req.URL.String(), attempt+1)
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
		return i18n.Errorf("invalid URL: missing host")
	}
	if c.hostAllowed(u) {
		return nil
	}
	return i18n.Errorf("refusing request to disallowed host %q (scheme %q)", u.Host, u.Scheme)
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
		return i18n.Errorf("stopped after %d redirects", maxRedirects)
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
