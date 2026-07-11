package provenance

import (
	"path/filepath"
	"testing"
)

func TestSplitNameVersion(t *testing.T) {
	cases := []struct {
		in, name, ver string
	}{
		{"glibc-2.39-5", "glibc", "2.39-5"},
		{"unstable-2024-01-01", "unstable", "2024-01-01"},
		{"python3.11", "python3.11", ""},
		{"ripgrep-14.1.0", "ripgrep", "14.1.0"},
		{"foo", "foo", ""},
		{"foo-bar", "foo-bar", ""}, // no digit after hyphen
		{"openssl-3.0.13", "openssl", "3.0.13"},
		{"", "", ""},
	}
	for _, tc := range cases {
		name, ver := splitNameVersion(tc.in)
		if name != tc.name || ver != tc.ver {
			t.Errorf("splitNameVersion(%q) = (%q, %q), want (%q, %q)",
				tc.in, name, ver, tc.name, tc.ver)
		}
	}
}

func TestPnpmPackageVersion(t *testing.T) {
	cases := []struct {
		path, pkg, ver string
	}{
		{
			"/home/u/Library/pnpm/global/5/node_modules/.pnpm/prettier@3.0.0/node_modules/prettier/bin/prettier.cjs",
			"prettier", "3.0.0",
		},
		{
			// peer-dep encoding after '_'
			"/x/.pnpm/react@18.2.0_react-dom@18.2.0/node_modules/react/index.js",
			"react", "18.2.0",
		},
		{
			// scoped: @scope+name@version
			"/x/.pnpm/@babel+core@7.23.0/node_modules/@babel/core/lib/index.js",
			"@babel/core", "7.23.0",
		},
		{
			// scoped + peer deps
			"/x/.pnpm/@scope+pkg@1.2.3_peer@9.0.0/node_modules/@scope/pkg/bin.js",
			"@scope/pkg", "1.2.3",
		},
		{
			"/no/pnpm/here",
			"", "",
		},
	}
	for _, tc := range cases {
		pkg, ver := pnpmPackageVersion(tc.path)
		if pkg != tc.pkg || ver != tc.ver {
			t.Errorf("pnpmPackageVersion(%q) = (%q, %q), want (%q, %q)",
				tc.path, pkg, ver, tc.pkg, tc.ver)
		}
	}
}

func TestPathRelUnder(t *testing.T) {
	cases := []struct {
		path, prefix, rel string
		ok                bool
	}{
		{"/a/b/c", "/a/b", "c", true},
		{"/a/b", "/a/b", "", false}, // exact = not under
		{"/a/b", "/a/c", "", false},
		{"/usr/binary", "/usr/bin", "", false}, // boundary
		{"/usr/bin/ls", "/usr/bin", "ls", true},
		{"", "/a", "", false},
		{"/a", "", "", false},
	}
	for _, tc := range cases {
		rel, ok := pathRelUnder(tc.path, tc.prefix)
		if ok != tc.ok || rel != tc.rel {
			t.Errorf("pathRelUnder(%q, %q) = (%q, %v), want (%q, %v)",
				tc.path, tc.prefix, rel, ok, tc.rel, tc.ok)
		}
	}
}

func TestNodePackageFromRel(t *testing.T) {
	cases := []struct {
		rel, want string
	}{
		{"eslint/bin/eslint.js", "eslint"},
		{"@babel/core/lib/index.js", "@babel/core"},
		{".bin/eslint", ""},
		{"", ""},
		{"foo", "foo"},
	}
	for _, tc := range cases {
		got := nodePackageFromRel(tc.rel)
		if got != tc.want {
			t.Errorf("nodePackageFromRel(%q) = %q, want %q", tc.rel, got, tc.want)
		}
	}
	// also via path helper
	nm := filepath.Join("/prefix", "lib", "node_modules")
	path := filepath.Join(nm, "@scope", "pkg", "bin", "x")
	if got := nodePackageFromPath(path, nm); got != "@scope/pkg" {
		t.Errorf("nodePackageFromPath scoped = %q", got)
	}
}
