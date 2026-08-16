// Package i18n is the minimal, dependency-free translation layer for
// human-facing CLI text. English is both the default locale and the message
// key: every user-visible string stays English in the code, and the catalog
// maps it to a translation selected via SetLocale. A missing catalog entry
// renders the English original, so an untranslated string can never break
// output. Machine-readable surfaces (JSON field names, enum tokens, exit
// codes) are intentionally not routed through this package.
//
// Strings belonging to the `notify` command are deliberately excluded from
// the catalog: that feature is scheduled for removal before release (see
// docs/planning/next-steps.md), and translating it would be throwaway work.
package i18n

import (
	"fmt"
	"strings"
	"sync"
)

// Locale identifies a UI language.
type Locale string

const (
	EN Locale = "en"
	ZH Locale = "zh"
)

var (
	mu      sync.RWMutex
	current = EN
)

// SetLocale selects the active locale. Unknown values fall back to EN.
// Only the CLI entry point resolves the environment; library code and tests
// default to EN unless they call this explicitly.
func SetLocale(l Locale) {
	mu.Lock()
	defer mu.Unlock()
	switch l {
	case ZH:
		current = ZH
	default:
		current = EN
	}
}

// CurrentLocale returns the active locale.
func CurrentLocale() Locale {
	mu.RLock()
	defer mu.RUnlock()
	return current
}

// ResolveLocale picks the locale from environment-like lookups, in order:
// explicit HUKOU_LANG, then the system locale (LC_ALL, LANG), then EN.
// Recognized values: zh / zh-CN / zh_CN / zh-Hans / zh_TW → ZH; en, C, POSIX
// → EN. An explicitly set but unrecognized HUKOU_LANG falls back to EN
// (explicit intent beats silently honoring the system locale).
func ResolveLocale(getenv func(string) string) Locale {
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	if v := getenv("HUKOU_LANG"); v != "" {
		if l, ok := parseLocale(v); ok {
			return l
		}
		return EN
	}
	for _, key := range []string{"LC_ALL", "LANG"} {
		if v := getenv(key); v != "" {
			if l, ok := parseLocale(v); ok {
				return l
			}
		}
	}
	// Follow the OS GUI language (macOS: AppleLocale/AppleLanguages from
	// ~/Library/Preferences/.GlobalPreferences.plist) when the shell
	// environment says nothing. Unknown or unreadable → EN.
	if v := systemGUILocale(); v != "" {
		if l, ok := parseLocale(v); ok {
			return l
		}
	}
	return EN
}

// systemGUILocale returns the OS-level GUI language preference. It is a
// variable so platforms and tests can override it; the default reads the
// macOS global preferences on darwin and nothing elsewhere.
var systemGUILocale = defaultSystemGUILocale

func parseLocale(v string) (Locale, bool) {
	s := strings.ToLower(strings.TrimSpace(v))
	switch {
	case s == "en" || s == "c" || s == "posix" || strings.HasPrefix(s, "en_"):
		return EN, true
	case s == "zh" || strings.HasPrefix(s, "zh-") || strings.HasPrefix(s, "zh_"):
		return ZH, true
	default:
		return EN, false
	}
}

// T renders format in the active locale. When args are empty the template is
// returned verbatim (no fmt pass), so help text containing literal '%' is
// never mangled. With args, the translated template must carry compatible
// verbs — CatalogTemplatesMatch enforces this in tests. Error templates with
// %w must use Errorf/Wrapf instead (T renders via Sprintf, which cannot
// handle %w).
func T(format string, args ...any) string {
	tmpl := Template(format)
	if len(args) == 0 {
		return tmpl
	}
	return fmt.Sprintf(tmpl, args...)
}

// Template returns the localized template for format without formatting it.
func Template(format string) string {
	if CurrentLocale() == ZH {
		if zh, ok := catalog[format]; ok {
			return zh
		}
	}
	return format
}

// Errorf builds a localized error from a template. It exists so error
// templates never reach fmt.Errorf as a non-constant format string (which go
// vet's printf check rejects) while still rendering through the catalog.
func Errorf(format string, args ...any) error {
	return &localizedError{format: format, args: args}
}

// Wrapf builds a localized error that wraps err as the %w target of format.
// The wrapped error stays reachable through errors.Is/errors.As.
func Wrapf(format string, err error, args ...any) error {
	return &localizedError{format: format, args: args, wrapped: err}
}

// localizedError renders its template in the active locale at Error() time,
// splicing the wrapped error's message at the %w position (the position among
// verbs is fixed, so the wrapped argument order never depends on the locale).
type localizedError struct {
	format  string
	args    []any
	wrapped error
}

func (e *localizedError) Error() string {
	return renderTemplate(Template(e.format), e.args, e.wrapped)
}

func (e *localizedError) Unwrap() error {
	return e.wrapped
}

// renderTemplate is a %w-aware Sprintf: every verb except %w consumes the
// next arg, and %w emits the wrapped error's message in place. Literal "%%"
// renders as "%". This mirrors fmt.Errorf semantics for the verb subset the
// codebase uses.
func renderTemplate(tmpl string, args []any, wrapped error) string {
	var b strings.Builder
	argIdx := 0
	for i := 0; i < len(tmpl); {
		c := tmpl[i]
		if c != '%' {
			b.WriteByte(c)
			i++
			continue
		}
		if i+1 < len(tmpl) && tmpl[i+1] == '%' {
			b.WriteByte('%')
			i += 2
			continue
		}
		if i+1 < len(tmpl) && tmpl[i+1] == 'w' {
			if wrapped != nil {
				b.WriteString(wrapped.Error())
			} else {
				b.WriteString("%!w(<nil>)")
			}
			i += 2
			continue
		}
		// Parse the full verb: flags/width/precision then the verb letter.
		j := i + 1
		for j < len(tmpl) && strings.ContainsRune("#+- 0.123456789", rune(tmpl[j])) {
			j++
		}
		if j >= len(tmpl) || !isVerbLetter(tmpl[j]) {
			// Malformed trailing '%': emit it literally (fmt would error).
			b.WriteByte('%')
			i++
			continue
		}
		verb := tmpl[i : j+1]
		if argIdx < len(args) {
			b.WriteString(fmt.Sprintf(verb, args[argIdx]))
			argIdx++
		} else {
			b.WriteString("%!" + verb[1:] + "(MISSING)")
		}
		i = j + 1
	}
	return b.String()
}

func isVerbLetter(c byte) bool {
	return strings.ContainsRune("vTtbcdoOqxXUeEfFgGsp", rune(c))
}

// IsTranslated reports whether the catalog carries a zh entry for format.
func IsTranslated(format string) bool {
	_, ok := catalog[format]
	return ok
}

// reverseCatalog maps zh values back to their English keys, making tree
// localization reversible: applying EN after ZH restores the original text.
// Duplicate zh values would make that ambiguous, so init rejects them loudly.
var reverseCatalog = func() map[string]string {
	m := make(map[string]string, len(catalog))
	for en, zh := range catalog {
		if prev, dup := m[zh]; dup {
			panic(fmt.Sprintf("i18n catalog: duplicate zh value %q for %q and %q", zh, prev, en))
		}
		m[zh] = en
	}
	return m
}()

// Localize renders one string for the active locale, and — when the active
// locale is EN — restores the English original of a string that was previously
// localized to ZH. It exists so the command tree can be re-localized in place
// (a process-local operation) without losing the canonical English text.
func Localize(s string) string {
	switch CurrentLocale() {
	case ZH:
		if zh, ok := catalog[s]; ok {
			return zh
		}
	case EN:
		if en, ok := reverseCatalog[s]; ok {
			return en
		}
	}
	return s
}

// CatalogSize reports the number of catalog entries (tests and audits only).
func CatalogSize() int {
	return len(catalog)
}
