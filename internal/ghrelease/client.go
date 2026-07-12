package ghrelease

import (
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
	defaultBaseURL = "https://api.github.com"
	maxRetries     = 3
)

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
	BaseURL    string
	Token      string
	HTTPClient *http.Client
	Sleep      func(time.Duration)
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
	return &Client{
		BaseURL:    defaultBaseURL,
		Token:      token,
		HTTPClient: http.DefaultClient,
		Sleep:      time.Sleep,
	}
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

func (c *Client) Download(downloadURL, destDir string) (string, error) {
	req, err := http.NewRequest(http.MethodGet, downloadURL, nil)
	if err != nil {
		return "", err
	}
	c.applyHeaders(req)

	resp, err := c.do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", responseStatusError(resp)
	}

	file, err := os.CreateTemp(destDir, ".ghrelease-*")
	if err != nil {
		return "", err
	}
	tmpPath := file.Name()

	if _, err := io.Copy(file, resp.Body); err != nil {
		file.Close()
		os.Remove(tmpPath)
		return "", err
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
	c.applyHeaders(req)

	resp, err := c.do(req)
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

func (c *Client) do(req *http.Request) (*http.Response, error) {
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		resp, err := c.httpClient().Do(req)
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
			drainClose(resp.Body)
			c.sleep(backoff(attempt))
			continue
		}

		return resp, nil
	}
	return nil, lastErr
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
		return http.DefaultClient
	}
	return c.HTTPClient
}

func (c *Client) sleep(d time.Duration) {
	if c.Sleep != nil {
		c.Sleep(d)
		return
	}
	time.Sleep(d)
}

func (c *Client) applyHeaders(req *http.Request) {
	req.Header.Set("Accept", "application/vnd.github+json")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
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
