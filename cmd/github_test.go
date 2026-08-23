package cmd

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rtwsvj/hukou/internal/ghrelease"
)

// TestResolveGitHubBaseURL pins the documented precedence (flag > env >
// default) and the fail-closed rule: any present-but-unusable value is an
// error, never a silent fallback to api.github.com.
func TestResolveGitHubBaseURL(t *testing.T) {
	cases := []struct {
		name      string
		flagValue string
		env       map[string]string
		want      string
		wantErr   bool
	}{
		{name: "nothing set yields default sentinel", want: ""},
		{
			name: "env used when flag empty",
			env:  map[string]string{"HUKOU_GITHUB_BASE_URL": "https://ghe.example.com/api/v3"},
			want: "https://ghe.example.com/api/v3",
		},
		{
			name:      "flag wins over env",
			flagValue: "https://flag.example.com",
			env:       map[string]string{"HUKOU_GITHUB_BASE_URL": "https://env.example.com"},
			want:      "https://flag.example.com",
		},
		{
			name:    "invalid env fails closed",
			env:     map[string]string{"HUKOU_GITHUB_BASE_URL": "http://mirror.example.com"},
			wantErr: true,
		},
		{
			name:      "invalid flag fails closed even with valid env",
			flagValue: "ftp://x.example.com",
			env:       map[string]string{"HUKOU_GITHUB_BASE_URL": "https://ghe.example.com"},
			wantErr:   true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			getenv := func(key string) string { return tc.env[key] }
			got, err := resolveGitHubBaseURL(tc.flagValue, getenv)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("resolveGitHubBaseURL(%q) = %q, want error", tc.flagValue, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveGitHubBaseURL(%q): %v", tc.flagValue, err)
			}
			if got != tc.want {
				t.Fatalf("resolveGitHubBaseURL(%q) = %q, want %q", tc.flagValue, got, tc.want)
			}
		})
	}
}

// TestNewGitHubClientEnvBaseEndToEnd drives the shared cmd-layer constructor
// with HUKOU_GITHUB_BASE_URL pointed at a local test server and proves API
// requests actually land there (G2.1 acceptance: env respected by a
// command-level unit test).
func TestNewGitHubClientEnvBaseEndToEnd(t *testing.T) {
	var gotPath, gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"tag_name":"v1.2.3","assets":[]}`)
	}))
	defer server.Close()

	t.Setenv("HUKOU_GITHUB_BASE_URL", server.URL)
	client, err := newGitHubClient("token-for-tests")
	if err != nil {
		t.Fatalf("newGitHubClient with env base: %v", err)
	}
	release, err := client.Latest("owner", "repo")
	if err != nil {
		t.Fatalf("Latest via env-configured base: %v", err)
	}
	if release.TagName != "v1.2.3" {
		t.Fatalf("tag = %q, want v1.2.3", release.TagName)
	}
	if gotPath != "/repos/owner/repo/releases/latest" {
		t.Fatalf("request reached %q, want the release endpoint on the test server", gotPath)
	}
	// Token handling contract: GITHUB_TOKEN-style tokens go ONLY to the
	// configured base host — here it matches, so the header must be present.
	if gotAuth != "Bearer token-for-tests" {
		t.Fatalf("Authorization = %q, want bearer token sent to the configured base host", gotAuth)
	}
}

// TestNewGitHubClientInvalidEnvFailsClosed proves a mistyped mirror URL
// aborts before any network use instead of silently hitting github.com.
func TestNewGitHubClientInvalidEnvFailsClosed(t *testing.T) {
	t.Setenv("HUKOU_GITHUB_BASE_URL", "not a url at all")
	client, err := newGitHubClient("")
	if err == nil {
		t.Fatal("expected an error for an unusable HUKOU_GITHUB_BASE_URL")
	}
	if !strings.Contains(err.Error(), "GitHub base URL") {
		t.Fatalf("error must name the offending setting, got: %v", err)
	}
	if client != nil {
		t.Fatalf("a client was returned alongside the error: %+v", client)
	}
}

// TestNewGitHubClientDefaultWithoutConfig pins the no-config path: default
// client construction stays infallible.
func TestNewGitHubClientDefaultWithoutConfig(t *testing.T) {
	t.Setenv("HUKOU_GITHUB_BASE_URL", "")
	client, err := newGitHubClient("")
	if err != nil {
		t.Fatalf("default construction must succeed, got: %v", err)
	}
	if client.BaseURL != ghrelease.DefaultBaseURL {
		t.Fatalf("BaseURL = %q, want the built-in default %q", client.BaseURL, ghrelease.DefaultBaseURL)
	}
	if client.Token != "" {
		t.Fatalf("Token = %q, want empty", client.Token)
	}
}

// TestGitHubBaseURLFlagIsPersistentOnRoot checks the CLI surface: the flag
// exists on the root command's persistent set (so every networked
// subcommand inherits it) and its help documents the env variable.
func TestGitHubBaseURLFlagIsPersistentOnRoot(t *testing.T) {
	f := rootCmd.PersistentFlags().Lookup(githubBaseURLFlag)
	if f == nil {
		t.Fatalf("--%s is not registered on the root persistent flags", githubBaseURLFlag)
	}
	if f.DefValue != "" {
		t.Fatalf("default = %q, want empty (env provides the default)", f.DefValue)
	}
	if !strings.Contains(f.Usage, "HUKOU_GITHUB_BASE_URL") || !strings.Contains(f.Usage, "https://api.github.com") {
		t.Fatalf("usage must document the env variable and the default, got: %s", f.Usage)
	}
}
