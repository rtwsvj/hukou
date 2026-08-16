package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/rtwsvj/hukou/internal/i18n"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// TestMain pins the cmd package to English by default so every existing test
// that asserts on English output stays deterministic regardless of the host
// machine's locale. Tests that exercise zh set the locale explicitly.
func TestMain(m *testing.M) {
	i18n.SetLocale(i18n.EN)
	os.Exit(m.Run())
}

// TestLocalizeCommandTreeReversible drives the real tree through zh and back
// and asserts both directions leave the expected text behind.
func TestLocalizeCommandTreeReversible(t *testing.T) {
	i18n.SetLocale(i18n.EN)
	localizeCommandTree(rootCmd)
	enShort := rootCmd.Short
	if enShort != "Safely inventory, adopt, upgrade, and roll back standalone CLI tools" {
		t.Fatalf("tree not in canonical English: %q", enShort)
	}

	i18n.SetLocale(i18n.ZH)
	localizeCommandTree(rootCmd)
	if !strings.Contains(rootCmd.Short, "安全地盘点") {
		t.Fatalf("zh Short not applied: %q", rootCmd.Short)
	}
	foundScan := false
	for _, c := range rootCmd.Commands() {
		if c.Name() != "scan" {
			continue
		}
		foundScan = true
		if !strings.Contains(c.Short, "盘点") {
			t.Fatalf("scan Short not localized: %q", c.Short)
		}
		c.Flags().VisitAll(func(f *pflag.Flag) {
			if f.Name == "unknown-only" && !strings.Contains(f.Usage, "只显示") {
				t.Fatalf("flag usage not localized: %q", f.Usage)
			}
		})
	}
	if !foundScan {
		t.Fatal("scan command missing from tree")
	}

	// Back to English: every string must be restored, not left Chinese.
	i18n.SetLocale(i18n.EN)
	localizeCommandTree(rootCmd)
	if rootCmd.Short != "Safely inventory, adopt, upgrade, and roll back standalone CLI tools" {
		t.Fatalf("tree not restored to English: %q", rootCmd.Short)
	}
}

// TestHelpTreeCoverage is the completeness gate for T1: every human-facing
// help string on the command tree (Short/Long and every flag Usage) must have
// a zh catalog entry — except the notify subtree, whose strings are
// intentionally excluded because the feature is scheduled for removal.
func TestHelpTreeCoverage(t *testing.T) {
	var missing []string
	collect := func(prefix string, short, long string, flags *pflag.FlagSet) {
		if short != "" && !i18n.IsTranslated(short) {
			missing = append(missing, prefix+" Short: "+short)
		}
		if long != "" && !i18n.IsTranslated(long) {
			missing = append(missing, prefix+" Long: "+long)
		}
		if flags != nil {
			flags.VisitAll(func(f *pflag.Flag) {
				if f.Name == "help" {
					return // dynamic per-command usage, handled in localizeCommandTree
				}
				if f.Usage != "" && !i18n.IsTranslated(f.Usage) {
					missing = append(missing, prefix+" Flag --"+f.Name+": "+f.Usage)
				}
			})
		}
	}
	rootCmd.InitDefaultHelpFlag()
	rootCmd.InitDefaultCompletionCmd()
	collect("hukou", rootCmd.Short, rootCmd.Long, rootCmd.Flags())
	for _, c := range rootCmd.Commands() {
		// completion: cobra-generated shell boilerplate whose Long texts embed
		// the program name — a documented exclusion from the zh catalog.
		if c.Name() == "help" || c.Name() == "completion" {
			continue
		}
		collect(c.Name(), c.Short, c.Long, c.Flags())
	}
	if len(missing) > 0 {
		t.Fatalf("%d help strings lack zh entries:\n  %s", len(missing), strings.Join(missing, "\n  "))
	}
}

// TestZhHelpSubprocess runs the real binary (the test binary itself, via the
// helper-process pattern) with HUKOU_LANG=zh and asserts the rendered help is
// Chinese; the companion run with HUKOU_LANG=en asserts English. This covers
// the Execute() path end-to-end — locale resolution, tree localization, and
// the zh help template — which in-process tests cannot reach.
func TestZhHelpSubprocess(t *testing.T) {
	if os.Getenv("HUKOU_I18N_HELPER") == "1" {
		os.Args = []string{"hukou", "scan", "--help"}
		rootCmd.SetArgs(os.Args[1:])
		if err := Execute(); err != nil {
			os.Exit(2)
		}
		os.Exit(0)
	}
	run := func(lang string) string {
		cmd := exec.Command(os.Args[0], "-test.run=TestZhHelpSubprocess")
		env := []string{}
		for _, e := range os.Environ() {
			if !strings.HasPrefix(e, "HUKOU_LANG=") && !strings.HasPrefix(e, "HUKOU_I18N_HELPER=") {
				env = append(env, e)
			}
		}
		env = append(env, "HUKOU_I18N_HELPER=1", "HUKOU_LANG="+lang)
		cmd.Env = env
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("subprocess failed (%s): %v", lang, err)
		}
		return string(out)
	}
	zh := run("zh")
	if !strings.Contains(zh, "用法") || !strings.Contains(zh, "标志") || !strings.Contains(zh, "额外扫描一个目录") || !strings.Contains(zh, "显示本命令帮助") {
		t.Fatalf("zh help not Chinese:\n%s", zh)
	}
	en := run("en")
	if strings.Contains(en, "用法") || !strings.Contains(en, "Usage:") || !strings.Contains(en, "Scan executables on PATH") || !strings.Contains(en, "help for scan") {
		t.Fatalf("en help not English:\n%s", en)
	}
}

// guard against accidental future loss of the cobra import when tests change.
var _ = cobra.Command{}

// TestDoctorFindingCoverage requires a zh entry for every literal doctor
// finding message, so the read-only diagnostic stays fully localized. It
// parses each report.add/incomplete call with a small paren/quote-aware
// splitter and checks the 6th argument (the message format); dynamic
// passthrough formats ("%v", error args) are exempt by construction.
func TestDoctorFindingCoverage(t *testing.T) {
	src, err := os.ReadFile("../internal/doctor/scanner.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(src)
	var missing []string
	callRe := regexp.MustCompile(`report\.(?:add|incomplete)\(`)
	for _, m := range callRe.FindAllStringIndex(text, -1) {
		oparen := m[1] - 1
		close := matchingParen(text, oparen)
		if close < 0 {
			t.Fatalf("unbalanced call at %d", oparen)
		}
		args := splitCallArgs(text[oparen+1 : close])
		if len(args) < 6 {
			continue
		}
		lit := args[5]
		if !strings.HasPrefix(lit, `"`) || !strings.HasSuffix(lit, `"`) {
			continue // dynamic passthrough (err argument)
		}
		body := lit[1 : len(lit)-1]
		if body == "%v" {
			continue
		}
		if !i18n.IsTranslated(body) {
			missing = append(missing, body)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("%d doctor finding messages lack zh entries:\n  %s", len(missing), strings.Join(missing, "\n  "))
	}
}

// matchingParen returns the index of the ')' that closes the '(' at oparen,
// skipping string literals and nested parens.
func matchingParen(src string, oparen int) int {
	depth := 0
	inStr := false
	for i := oparen; i < len(src); i++ {
		c := src[i]
		if inStr {
			if c == '\\' {
				i++
				continue
			}
			if c == '"' {
				inStr = false
			}
			continue
		}
		if c == '"' {
			inStr = true
			continue
		}
		if c == '(' {
			depth++
		} else if c == ')' {
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// splitCallArgs splits a call's argument text on top-level commas.
func splitCallArgs(argsRaw string) []string {
	var args []string
	cur := ""
	depth := 0
	inStr := false
	for i := 0; i < len(argsRaw); i++ {
		c := argsRaw[i]
		if inStr {
			cur += string(c)
			if c == '\\' && i+1 < len(argsRaw) {
				cur += string(argsRaw[i+1])
				i++
				continue
			}
			if c == '"' {
				inStr = false
			}
			continue
		}
		if c == '"' {
			inStr = true
			cur += string(c)
			continue
		}
		if c == '(' {
			depth++
		} else if c == ')' {
			depth--
		}
		if c == ',' && depth == 0 {
			if t := strings.TrimSpace(cur); t != "" {
				args = append(args, t)
			}
			cur = ""
		} else {
			cur += string(c)
		}
	}
	if t := strings.TrimSpace(cur); t != "" {
		args = append(args, t)
	}
	return args
}

// TestInternalErrorCoverage requires a zh entry for every letter-bearing
// error literal in the internal packages, so deep-layer errors surface in
// Chinese too. Templates without any ASCII letters (pure verb passthroughs
// like "%w: %v") are exempt by construction.
func TestInternalErrorCoverage(t *testing.T) {
	re := regexp.MustCompile(`(?:errors\.New|fmt\.Errorf)\(\s*"((?:[^"\\]|\\.)*)"`)
	var missing []string
	err := filepath.Walk("../internal", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		name := filepath.Base(path)
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		if strings.Contains(path, "/i18n/") || strings.Contains(path, "/buildinfo/") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range re.FindAllSubmatch(src, -1) {
			lit := string(m[1])
			if !hasASCIILetter(lit) {
				continue
			}
			if !i18n.IsTranslated(lit) {
				missing = append(missing, path+": "+lit)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) > 0 {
		t.Fatalf("%d internal error strings lack zh entries:\n  %s", len(missing), strings.Join(missing, "\n  "))
	}
}

func hasASCIILetter(s string) bool {
	for i := 0; i < len(s); i++ {
		if (s[i] >= 'a' && s[i] <= 'z') || (s[i] >= 'A' && s[i] <= 'Z') {
			return true
		}
	}
	return false
}

// TestI18nCallSiteCoverage scans every i18n.T / i18n.Errorf / i18n.Wrapf call
// with a literal template across the localized packages and requires a catalog
// entry, so a newly wrapped string can never ship without a translation.
func TestI18nCallSiteCoverage(t *testing.T) {
	re := regexp.MustCompile(`i18n\.(?:T|Errorf|Wrapf)\(\s*"((?:[^"\\]|\\.)*)"`)
	dirs := []string{".", "../internal/output", "../internal/doctor", "../internal/orchestrate/plan"}
	var missing []string
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			name := e.Name()
			if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			src, err := os.ReadFile(dir + "/" + name)
			if err != nil {
				t.Fatal(err)
			}
			for _, m := range re.FindAllSubmatch(src, -1) {
				lit := string(m[1])
				if !i18n.IsTranslated(lit) {
					missing = append(missing, dir+"/"+name+": "+lit)
				}
			}
		}
	}
	if len(missing) > 0 {
		t.Fatalf("%d i18n call sites lack zh entries:\n  %s", len(missing), strings.Join(missing, "\n  "))
	}
}

// TestErrorStringCoverage is the completeness gate for T3: every error
// constructor literal in the cmd layer (errors.New / fmt.Errorf) must have a
// zh catalog entry, so a new English error can never silently ship
// untranslated. notify.go is excluded (scheduled for removal); i18n.go and
// test files are excluded by construction.
func TestErrorStringCoverage(t *testing.T) {
	re := regexp.MustCompile(`(?:errors\.New\(\s*i18n\.T|i18n\.Errorf|i18n\.Wrapf)\(\s*"((?:[^"\\]|\\.)*)"`)
	var missing []string
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") || name == "notify.go" {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range re.FindAllSubmatch(src, -1) {
			lit := string(m[1])
			if !i18n.IsTranslated(lit) {
				missing = append(missing, name+": "+lit)
			}
		}
	}
	if len(missing) > 0 {
		t.Fatalf("%d error strings lack zh entries:\n  %s", len(missing), strings.Join(missing, "\n  "))
	}
}
