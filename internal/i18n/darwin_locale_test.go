//go:build darwin

package i18n

import (
	"os"
	"path/filepath"
	"testing"
)

// testBplist builds a minimal binary plist with one dict containing
// AppleLocale and AppleLanguages.
func testBplist(t *testing.T, locale string, langs []string) []byte {
	t.Helper()
	// object layout:
	//  0: dict {2 keys}
	//  1: ascii "AppleLocale"
	//  2: ascii locale
	//  3: ascii "AppleLanguages"
	//  4: array(1) -> [5]
	//  5: ascii langs[0]
	ascii := func(s string) []byte { return append([]byte{0x50 | byte(len(s))}, []byte(s)...) }
	objs := [][]byte{
		{0xd2}, // dict, 2 entries
		ascii("AppleLocale"),
		ascii(locale),
		ascii("AppleLanguages"),
		{0xa1, 0x05}, // array of 1 ref (1-byte refs)
		ascii(langs[0]),
	}
	// dict payload: key refs (1,3) then value refs (2,4) — one byte per ref
	objs[0] = append(objs[0], 0x01, 0x03, 0x02, 0x04)

	var body []byte
	offsets := []int{}
	for _, o := range objs {
		offsets = append(offsets, 8+len(body))
		body = append(body, o...)
	}
	tableStart := 8 + len(body)
	var table []byte
	for _, off := range offsets {
		table = append(table, byte(off))
	}
	out := append([]byte("bplist00"), body...)
	out = append(out, table...)
	// trailer: 6 unused, offsetIntSize=1, refSize=1, numObjects, topObject=0, tableOffset
	out = append(out, 0, 0, 0, 0, 0, 0, 1, 1)
	for i := 0; i < 8; i++ {
		out = append(out, byte(uint64(len(objs))>>(8*(7-i))))
	}
	for i := 0; i < 8; i++ { // topObject = 0
		out = append(out, 0)
	}
	for i := 0; i < 8; i++ {
		out = append(out, byte(uint64(tableStart)>>(8*(7-i))))
	}
	return out
}

func TestDarwinAppleLocaleFromBplist(t *testing.T) {
	data := testBplist(t, "zh_CN", []string{"zh-Hans-CN"})
	root, err := parseBplist(data)
	if err != nil {
		t.Fatalf("parseBplist: %v", err)
	}
	dict := root.(bplistDict)
	if dict["AppleLocale"] != "zh_CN" {
		t.Fatalf("AppleLocale = %v", dict["AppleLocale"])
	}
	if langs := dict["AppleLanguages"].([]any); langs[0] != "zh-Hans-CN" {
		t.Fatalf("AppleLanguages = %v", langs)
	}
}

func TestResolveLocaleFollowsMacOSGUILanguage(t *testing.T) {
	home := t.TempDir()
	plistPath := filepath.Join(home, "Library", "Preferences", ".GlobalPreferences.plist")
	if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(plistPath, testBplist(t, "zh_CN", []string{"zh-Hans-CN"}), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	// Hermetic locale environment: hosted CI runners carry LC_ALL in the
	// environment, and a leftover value would shadow the GUI fallback being
	// tested here (env precedence is a documented behavior, not a bug).
	t.Setenv("LC_ALL", "")
	t.Setenv("LANG", "") // terminal default on macOS: GUI language decides
	t.Setenv("HUKOU_LANG", "")
	if got := ResolveLocale(func(k string) string { return os.Getenv(k) }); got != ZH {
		t.Fatalf("GUI language should decide when env is unset: got %q", got)
	}
	// A shell LANG still takes precedence over the GUI language.
	t.Setenv("LANG", "en_US.UTF-8")
	if got := ResolveLocale(os.Getenv); got != EN {
		t.Fatalf("shell LANG should win over GUI language: got %q", got)
	}
	// Explicit HUKOU_LANG still overrides everything.
	t.Setenv("HUKOU_LANG", "en")
	if got := ResolveLocale(os.Getenv); got != EN {
		t.Fatalf("explicit HUKOU_LANG=en must win: got %q", got)
	}
}

func TestParseBplistRejectsGarbage(t *testing.T) {
	for _, bad := range [][]byte{nil, []byte("bplist00garbage"), []byte("bplist00" + string(make([]byte, 32)))} {
		if _, err := parseBplist(bad); err == nil {
			t.Fatalf("expected rejection for %q", bad)
		}
	}
}
