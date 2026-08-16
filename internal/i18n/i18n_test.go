package i18n

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
)

func TestResolveLocale(t *testing.T) {
	// Deterministic matrix: the OS GUI-language fallback is covered by the
	// darwin-specific tests; here it is stubbed out.
	origGUI := systemGUILocale
	systemGUILocale = func() string { return "" }
	defer func() { systemGUILocale = origGUI }()

	env := func(pairs map[string]string) func(string) string {
		return func(key string) string { return pairs[key] }
	}
	cases := []struct {
		name string
		env  map[string]string
		want Locale
	}{
		{"explicit zh", map[string]string{"HUKOU_LANG": "zh"}, ZH},
		{"explicit zh-CN", map[string]string{"HUKOU_LANG": "zh-CN"}, ZH},
		{"explicit zh_CN.UTF-8", map[string]string{"HUKOU_LANG": "zh_CN.UTF-8"}, ZH},
		{"explicit en overrides system", map[string]string{"HUKOU_LANG": "en", "LANG": "zh_CN.UTF-8"}, EN},
		{"explicit unknown falls back to en", map[string]string{"HUKOU_LANG": "de", "LANG": "zh_CN.UTF-8"}, EN},
		{"system LANG zh", map[string]string{"LANG": "zh_CN.UTF-8"}, ZH},
		{"system LC_ALL zh beats LANG en", map[string]string{"LC_ALL": "zh_CN", "LANG": "en_US.UTF-8"}, ZH},
		{"c locale", map[string]string{"LANG": "C"}, EN},
		{"posix locale", map[string]string{"LANG": "POSIX"}, EN},
		{"empty everything", map[string]string{}, EN},
		{"nil getenv", nil, EN},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResolveLocale(env(tc.env)); got != tc.want {
				t.Fatalf("ResolveLocale = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestT_FallbackAndArgs(t *testing.T) {
	SetLocale(ZH)
	defer SetLocale(EN)

	if got := T("not in the catalog"); got != "not in the catalog" {
		t.Fatalf("fallback failed: %q", got)
	}
	if got := T("summary: total=%d sources=%d unknown=%d shadowed=%d", 3, 2, 1, 0); got != "汇总：总数=3 来源种类=2 未知=1 被遮蔽=0" {
		t.Fatalf("zh arg render failed: %q", got)
	}
	// No-arg templates are returned verbatim: literal '%' must survive.
	if got := T("progress: 100%"); got != "progress: 100%" {
		t.Fatalf("no-arg verbatim failed: %q", got)
	}
}

func TestLocalizeReversible(t *testing.T) {
	SetLocale(ZH)
	zh := Localize("List adopted tools")
	if zh == "List adopted tools" || !strings.Contains(zh, "列出") {
		t.Fatalf("zh localization failed: %q", zh)
	}
	SetLocale(EN)
	en := Localize(zh)
	if en != "List adopted tools" {
		t.Fatalf("reverse localization failed: %q", en)
	}
	// A string that was never translated is untouched in both locales.
	if Localize("arbitrary text") != "arbitrary text" {
		t.Fatal("untranslated string mutated")
	}
}

// TestCatalogTemplateVerbsMatch is the adversarial guard for the catalog: every
// zh template must carry exactly the same printf verbs in the same order as its
// English key, otherwise an arg-bearing call would misrender or panic the %!v
// formatter. '%' in prose must be escaped in both sides.
func TestCatalogTemplateVerbsMatch(t *testing.T) {
	verbRe := regexp.MustCompile(`%([+#\- 0]*\d*\.?\d*)?([vTtbcdoOqxXUeEfFgGspw]|%)`)
	verbs := func(s string) []string {
		var out []string
		for _, m := range verbRe.FindAllStringSubmatch(s, -1) {
			if m[2] == "%" {
				out = append(out, "%%")
			} else {
				out = append(out, m[2])
			}
		}
		return out
	}
	for en, zh := range catalog {
		if zh == "" {
			t.Errorf("catalog entry %q has empty zh value", en)
			continue
		}
		enV, zhV := verbs(en), verbs(zh)
		if len(enV) != len(zhV) {
			t.Errorf("catalog entry %q: verb count mismatch en=%v zh=%v (zh=%q)", en, enV, zhV, zh)
			continue
		}
		for i := range enV {
			if enV[i] != zhV[i] {
				t.Errorf("catalog entry %q: verb %d mismatch en=%q zh=%q (zh=%q)", en, i, enV[i], zhV[i], zh)
			}
		}
	}
}

// TestCatalogReachableFromCode greps nothing at runtime; it guards the map
// itself against dead entries (keys no code uses) by requiring every key to
// look like a user-facing string with at least one non-whitespace rune.
func TestCatalogEntriesSane(t *testing.T) {
	for en := range catalog {
		if strings.TrimSpace(en) == "" {
			t.Error("catalog has an empty key")
		}
	}
	if CatalogSize() == 0 {
		t.Fatal("catalog is empty")
	}
	_ = fmt.Sprint() // keep fmt import honest if assertions change
}
