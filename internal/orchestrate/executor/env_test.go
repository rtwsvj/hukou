package executor

import (
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/rtwsvj/hukou/internal/sysproxy"
)

// stubSystemProxy replaces the package-level sysproxy seam for one test.
func stubSystemProxy(t *testing.T, rawurl string) {
	t.Helper()
	orig := sysproxy.SystemProxyURL
	if rawurl == "" {
		sysproxy.SystemProxyURL = func() *url.URL { return nil }
	} else {
		u, err := url.Parse(rawurl)
		if err != nil {
			t.Fatalf("parse proxy url: %v", err)
		}
		sysproxy.SystemProxyURL = func() *url.URL { return u }
	}
	t.Cleanup(func() { sysproxy.SystemProxyURL = orig })
}

// clearProxyEnv unsets every environment variable buildChildEnv consults,
// so a test never depends on the host machine's real proxy configuration.
// Variables are UNSET, not set empty: a present-but-empty HTTPS_PROXY counts
// as explicit user configuration and would block injection.
func clearProxyEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY",
		"http_proxy", "https_proxy", "all_proxy", "no_proxy",
		"HUKOU_UP_NO_PROXY_INHERIT", "HUKOU_UP_ENV_PASSTHRU",
	} {
		t.Setenv(key, "")
		os.Unsetenv(key)
	}
}

// envValue returns key's value in an env slice, and whether it is present
// with a non-empty value (empty assignments count as unset, mirroring envHas;
// later duplicates win, mirroring how exec'd programs resolve them).
func envValue(env []string, key string) (string, bool) {
	prefix := key + "="
	found := ""
	ok := false
	for _, e := range env {
		if strings.HasPrefix(e, prefix) && e[len(prefix):] != "" {
			found = e[len(prefix):]
			ok = true
		}
	}
	return found, ok
}

func TestBuildChildEnv_ExplicitProxyIsRespected(t *testing.T) {
	clearProxyEnv(t)
	t.Setenv("HTTPS_PROXY", "http://explicit:3128")
	stubSystemProxy(t, "http://127.0.0.1:7890")

	env, host, _ := buildChildEnv()
	if host != "" {
		t.Fatalf("proxyHost = %q, want none when HTTPS_PROXY is explicit", host)
	}
	if v, _ := envValue(env, "HTTPS_PROXY"); v != "http://explicit:3128" {
		t.Fatalf("HTTPS_PROXY = %q, want the explicit value untouched", v)
	}
	if _, ok := envValue(env, "ALL_PROXY"); ok {
		t.Fatal("ALL_PROXY injected despite an explicit HTTPS_PROXY")
	}
	if _, ok := envValue(env, "http_proxy"); ok {
		t.Fatal("http_proxy injected despite an explicit HTTPS_PROXY")
	}
}

func TestBuildChildEnv_EscapeDoorDisablesInheritance(t *testing.T) {
	clearProxyEnv(t)
	t.Setenv("HUKOU_UP_NO_PROXY_INHERIT", "1")
	stubSystemProxy(t, "http://127.0.0.1:7890")

	env, host, _ := buildChildEnv()
	if host != "" {
		t.Fatalf("proxyHost = %q, want none under the escape door", host)
	}
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY"} {
		if _, ok := envValue(env, key); ok {
			t.Fatalf("%s injected despite HUKOU_UP_NO_PROXY_INHERIT=1", key)
		}
	}
}

func TestBuildChildEnv_InjectsSystemProxyWhenUnset(t *testing.T) {
	clearProxyEnv(t)
	t.Setenv("NO_PROXY", "localhost,127.0.0.1")
	stubSystemProxy(t, "http://127.0.0.1:7890")

	env, host, _ := buildChildEnv()
	if host != "127.0.0.1:7890" {
		t.Fatalf("proxyHost = %q, want 127.0.0.1:7890", host)
	}
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"} {
		if v, ok := envValue(env, key); !ok || v != "http://127.0.0.1:7890" {
			t.Fatalf("%s = %q (present=%v), want the system proxy URL", key, v, ok)
		}
	}
	// A pre-existing NO_PROXY is preserved, not rewritten.
	if v, ok := envValue(env, "NO_PROXY"); !ok || v != "localhost,127.0.0.1" {
		t.Fatalf("NO_PROXY = %q (present=%v), want preserved", v, ok)
	}
}

func TestBuildChildEnv_NoSystemProxyNoInjection(t *testing.T) {
	clearProxyEnv(t)
	stubSystemProxy(t, "")

	env, host, _ := buildChildEnv()
	if host != "" {
		t.Fatalf("proxyHost = %q, want none without a system proxy", host)
	}
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY"} {
		if _, ok := envValue(env, key); ok {
			t.Fatalf("%s injected without a system proxy", key)
		}
	}
}

// TestRunManager_ProxyNotePrintsHostOnly: when inheritance kicks in, the
// stderr note names only host:port — never the URL's userinfo credentials —
// while the child still receives the full proxy URL.
func TestRunManager_ProxyNotePrintsHostOnly(t *testing.T) {
	skipOnWindows(t)
	clearProxyEnv(t)
	stubSystemProxy(t, "http://user:secret@127.0.0.1:7890")

	dir := t.TempDir()
	show := writeScript(t, dir, "show.sh", "#!/bin/sh\necho \"child-sees:$HTTPS_PROXY\"\n")

	var out, errb lockedBuf
	e := New(&out, &errb)
	res := e.RunManager(t.Context(), "brew", [][]string{{show}})
	if !res.OK() {
		t.Fatalf("status = %s (err=%v)", res.Status, res.Err)
	}
	if !strings.Contains(errb.String(), "[brew] using system proxy 127.0.0.1:7890") {
		t.Fatalf("stderr missing the host-only proxy note:\n%s", errb.String())
	}
	for _, leak := range []string{"user", "secret"} {
		if strings.Contains(errb.String(), leak) {
			t.Fatalf("stderr leaked proxy credential %q:\n%s", leak, errb.String())
		}
	}
	if !strings.Contains(out.String(), "child-sees:http://user:secret@127.0.0.1:7890") {
		t.Fatalf("child did not receive the full proxy URL:\n%s", out.String())
	}
}

// ---- M1: environment allowlist ----

func TestFilteredEnviron_AllowlistDropsSecretsKeepsToolchain(t *testing.T) {
	clearProxyEnv(t)
	t.Setenv("GITHUB_TOKEN", "ghp_secret")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "aws_secret")
	t.Setenv("RANDOM_API_KEY", "nope")
	t.Setenv("HOMEBREW_NO_AUTO_UPDATE", "1")
	t.Setenv("XDG_CONFIG_HOME", "/tmp/xdg")
	t.Setenv("LC_ALL", "en_US.UTF-8")
	t.Setenv("CI", "true")

	env, full := filteredEnviron()
	if full {
		t.Fatal("fullPassthru = true without the escape hatch")
	}
	for _, key := range []string{"GITHUB_TOKEN", "AWS_SECRET_ACCESS_KEY", "RANDOM_API_KEY"} {
		if _, ok := envValue(env, key); ok {
			t.Fatalf("secret %s leaked into the child environment", key)
		}
	}
	if _, ok := envValue(env, "PATH"); !ok {
		t.Fatal("PATH missing from child env")
	}
	if _, ok := envValue(env, "HOME"); !ok {
		t.Fatal("HOME missing from child env")
	}
	for key, want := range map[string]string{
		"HOMEBREW_NO_AUTO_UPDATE": "1",
		"XDG_CONFIG_HOME":         "/tmp/xdg",
		"LC_ALL":                  "en_US.UTF-8",
		"CI":                      "true",
	} {
		if v, ok := envValue(env, key); !ok || v != want {
			t.Fatalf("allowlisted %s = %q (present=%v), want %q", key, v, ok, want)
		}
	}
}

func TestFilteredEnviron_PassthruListAndStar(t *testing.T) {
	clearProxyEnv(t)
	t.Setenv("GITHUB_TOKEN", "ghp_secret")
	t.Setenv("MY_CUSTOM_FLAG", "1")

	// Comma-separated names pass through individually; everything else drops.
	t.Setenv("HUKOU_UP_ENV_PASSTHRU", "MY_CUSTOM_FLAG")
	env, full := filteredEnviron()
	if full {
		t.Fatal("fullPassthru = true for a name list")
	}
	if v, _ := envValue(env, "MY_CUSTOM_FLAG"); v != "1" {
		t.Fatalf("passthru variable missing: %q", v)
	}
	if _, ok := envValue(env, "GITHUB_TOKEN"); ok {
		t.Fatal("GITHUB_TOKEN leaked despite a name-only passthru list")
	}

	// "*" restores full inheritance and flags the escape for the caller —
	// except the denylist, which wins even over "*".
	t.Setenv("HUKOU_UP_ENV_PASSTHRU", "*")
	env, full = filteredEnviron()
	if !full {
		t.Fatal("fullPassthru = false for the * escape hatch")
	}
	if _, ok := envValue(env, "GITHUB_TOKEN"); ok {
		t.Fatal("GITHUB_TOKEN survived full passthru; denylist must win over *")
	}
	if v, _ := envValue(env, "MY_CUSTOM_FLAG"); v != "1" {
		t.Fatalf("full passthru dropped a non-denylisted variable: %q", v)
	}
}

// TestBuildChildEnv_WhitelistStacksWithProxyInjection: the allowlist filters
// first, then proxy inheritance fills the (allowlisted) proxy keys — a
// passthru-listed custom variable and the injected proxy coexist, while
// secrets stay out.
func TestBuildChildEnv_WhitelistStacksWithProxyInjection(t *testing.T) {
	clearProxyEnv(t)
	t.Setenv("GITHUB_TOKEN", "ghp_secret")
	t.Setenv("MY_CUSTOM_FLAG", "1")
	t.Setenv("HUKOU_UP_ENV_PASSTHRU", "MY_CUSTOM_FLAG")
	stubSystemProxy(t, "http://127.0.0.1:7890")

	env, host, full := buildChildEnv()
	if full {
		t.Fatal("fullPassthru = true for a name list")
	}
	if host != "127.0.0.1:7890" {
		t.Fatalf("proxyHost = %q, want injection", host)
	}
	if v, _ := envValue(env, "HTTPS_PROXY"); v != "http://127.0.0.1:7890" {
		t.Fatalf("injected HTTPS_PROXY = %q", v)
	}
	if v, _ := envValue(env, "MY_CUSTOM_FLAG"); v != "1" {
		t.Fatalf("passthru variable lost: %q", v)
	}
	if _, ok := envValue(env, "GITHUB_TOKEN"); ok {
		t.Fatal("GITHUB_TOKEN leaked through buildChildEnv")
	}
}

// TestFilteredEnviron_EmptyEnvironment: an empty parent environment yields an
// empty (but valid) child base — no nil-map panics, no invented variables.
func TestFilteredEnviron_EmptyEnvironment(t *testing.T) {
	saved := os.Environ()
	os.Clearenv()
	t.Cleanup(func() {
		os.Clearenv()
		for _, e := range saved {
			k, v, _ := strings.Cut(e, "=")
			os.Setenv(k, v)
		}
	})

	env, full := filteredEnviron()
	if full {
		t.Fatal("fullPassthru = true in an empty environment")
	}
	if len(env) != 0 {
		t.Fatalf("env = %v, want empty", env)
	}
}

// TestRunManager_FullPassthruNotice: the * escape hatch announces itself on
// stderr so a deliberately insecure configuration is visible in the run log.
func TestRunManager_FullPassthruNotice(t *testing.T) {
	skipOnWindows(t)
	clearProxyEnv(t)
	t.Setenv("HUKOU_UP_ENV_PASSTHRU", "*")
	stubSystemProxy(t, "")

	dir := t.TempDir()
	ok := writeScript(t, dir, "ok.sh", "#!/bin/sh\nexit 0\n")

	var out, errb lockedBuf
	e := New(&out, &errb)
	if res := e.RunManager(t.Context(), "brew", [][]string{{ok}}); !res.OK() {
		t.Fatalf("status = %s (err=%v)", res.Status, res.Err)
	}
	if !strings.Contains(errb.String(), "[brew] HUKOU_UP_ENV_PASSTHRU=*") {
		t.Fatalf("stderr missing the full-passthru notice:\n%s", errb.String())
	}
}

// TestFilteredEnviron_DenylistStripsBrewTokens: the brew token family used to
// ride in through the HOMEBREW_* prefix; now only exact non-secret settings
// pass, and the tokens drop even though they carry the prefix.
func TestFilteredEnviron_DenylistStripsBrewTokens(t *testing.T) {
	clearProxyEnv(t)
	t.Setenv("HOMEBREW_GITHUB_API_TOKEN", "ghp_brew_secret")
	t.Setenv("HOMEBREW_GITHUB_PACKAGES_TOKEN", "ghp_pkg_secret")
	t.Setenv("GH_TOKEN", "gh_cli_secret")
	t.Setenv("NPM_TOKEN", "npm_secret")
	t.Setenv("NODE_AUTH_TOKEN", "npmrc_secret")
	t.Setenv("AWS_ACCESS_KEY_ID", "AKIA_SECRET")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "aws_secret")
	t.Setenv("HOMEBREW_NO_AUTO_UPDATE", "1")
	t.Setenv("HOMEBREW_NO_INSTALL_CLEANUP", "1")
	t.Setenv("HOMEBREW_NO_ENV_HINTS", "1")

	env, full := filteredEnviron()
	if full {
		t.Fatal("fullPassthru = true without the escape hatch")
	}
	for _, key := range []string{
		"HOMEBREW_GITHUB_API_TOKEN", "HOMEBREW_GITHUB_PACKAGES_TOKEN",
		"GH_TOKEN", "NPM_TOKEN", "NODE_AUTH_TOKEN",
		"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY",
	} {
		if _, ok := envValue(env, key); ok {
			t.Fatalf("denied secret %s leaked into the child environment", key)
		}
	}
	for key, want := range map[string]string{
		"HOMEBREW_NO_AUTO_UPDATE":     "1",
		"HOMEBREW_NO_INSTALL_CLEANUP": "1",
		"HOMEBREW_NO_ENV_HINTS":       "1",
	} {
		if v, ok := envValue(env, key); !ok || v != want {
			t.Fatalf("non-secret brew setting %s = %q (present=%v), want %q", key, v, ok, want)
		}
	}
}

// TestChildEnvDropsBrewTokenForEveryManager: the denylist is manager-agnostic
// — the brew subprocess AND every other manager's subprocess run without it.
func TestChildEnvDropsBrewTokenForEveryManager(t *testing.T) {
	skipOnWindows(t)
	clearProxyEnv(t)
	t.Setenv("HUKOU_UP_ENV_PASSTHRU", "*")
	stubSystemProxy(t, "")
	dir := t.TempDir()
	ok := writeScript(t, dir, "guard.sh", "#!/bin/sh\n"+
		"if [ -n \"$HOMEBREW_GITHUB_API_TOKEN\" ]; then echo \"LEAKED BREW TOKEN\"; exit 1; fi\n"+
		"exit 0\n")
	for _, name := range []string{"brew", "npm"} {
		var out, errb lockedBuf
		e := New(&out, &errb)
		if res := e.RunManager(t.Context(), name, [][]string{{ok}}); !res.OK() {
			t.Fatalf("[%s] status = %s (err=%v)", name, res.Status, res.Err)
		}
		if strings.Contains(out.String(), "LEAKED") {
			t.Fatalf("%s subprocess saw HOMEBREW_GITHUB_API_TOKEN despite passthru=*", name)
		}
	}
}

// TestBuildChildEnv_EmptyExplicitProxyBlocksInjection: an environment that
// carries HTTPS_PROXY/https_proxy PRESENT but EMPTY is deliberate user
// configuration ("no proxy for this run") and must suppress system-proxy
// injection exactly like a filled-in value would.
func TestBuildChildEnv_EmptyExplicitProxyBlocksInjection(t *testing.T) {
	for _, key := range []string{"HTTPS_PROXY", "https_proxy"} {
		t.Run(key, func(t *testing.T) {
			clearProxyEnv(t)
			t.Setenv(key, "")
			stubSystemProxy(t, "http://127.0.0.1:7890")

			env, host, _ := buildChildEnv()
			if host != "" {
				t.Fatalf("proxyHost = %q, want injection blocked by empty %s=", host, key)
			}
			for _, injected := range proxyEnvKeys {
				if _, ok := envValue(env, injected); ok {
					t.Fatalf("%s injected despite explicit empty %s=", injected, key)
				}
			}
		})
	}
}

// TestLineWriterSanitizesEscapeSequences: a manager line containing ESC (and
// other C0/C1 controls) is emitted with them replaced, so subprocess output
// cannot clear the screen or paint a fake final report.
func TestLineWriterSanitizesEscapeSequences(t *testing.T) {
	var buf lockedBuf
	lw := &lineWriter{mu: &sync.Mutex{}, dst: &buf, prefix: "[brew] "}
	lw.Write([]byte("\x1b[2J\x1b[1;1HALL UPGRADES COMPLETE\x07\n"))
	lw.flush()
	got := buf.String()
	if strings.ContainsAny(got, "\x1b\x07") {
		t.Fatalf("escape/control bytes survived sanitization: %q", got)
	}
	if !strings.Contains(got, "?[2J?[1;1HALL UPGRADES COMPLETE?") {
		t.Fatalf("sanitized output lost visible text or mis-marked controls: %q", got)
	}
}

// TestLineOutputFromManagerIsSanitized: end-to-end through RunManager — a
// subprocess painting ANSI clear-screen/fake-report sequences onto stdout has
// them stripped before they reach hukou's own stream.
func TestLineOutputFromManagerIsSanitized(t *testing.T) {
	skipOnWindows(t)
	clearProxyEnv(t)
	stubSystemProxy(t, "")
	dir := t.TempDir()
	painter := writeScript(t, dir, "painter.sh",
		"#!/bin/sh\nprintf '\\033[2JUPGRADE SUCCESSFUL, SEND NO FURTHER REPORT\\n'\nexit 0\n")
	var out, errb lockedBuf
	e := New(&out, &errb)
	if res := e.RunManager(t.Context(), "npm", [][]string{{painter}}); !res.OK() {
		t.Fatalf("status = %s (err=%v)", res.Status, res.Err)
	}
	if strings.ContainsRune(out.String(), '\x1b') {
		t.Fatalf("ESC byte from manager output reached the log:\n%q", out.String())
	}
	if !strings.Contains(out.String(), "[npm] ?[2JUPGRADE SUCCESSFUL") {
		t.Fatalf("sanitized manager line missing from output:\n%q", out.String())
	}
}
