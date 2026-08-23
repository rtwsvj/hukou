// This file is original hukou code layered on top of the vendored eget
// detector in detect.go (which is not modified). It adds a blacklist
// prefilter, an optional --asset substring filter, and a deterministic
// tiebreak waterfall on top of eget's SystemDetector.
package assetpick

import (
	"path"
	"sort"
	"strings"

	"github.com/rtwsvj/hukou/internal/archive"
	"github.com/rtwsvj/hukou/internal/i18n"
)

// archiveCompoundSuffixes are the known multi-part archive extensions. They
// are stripped before extension analysis so that a version number such as the
// ".5" in "foo-1.3.5.tar.gz" is not mistaken for a real extension.
var archiveCompoundSuffixes = []string{
	".tar.gz", ".tar.xz", ".tgz", ".txz", ".zip", ".gz",
}

// blacklistExts are extensions that never contain the binary we want.
var blacklistExts = map[string]bool{
	".sha256":    true,
	".sha256sum": true,
	".sig":       true,
	".asc":       true,
	".pem":       true,
	".sbom":      true,
	".txt":       true,
	".md":        true,
	".deb":       true,
	".rpm":       true,
	".apk":       true,
	".msi":       true,
}

// darwinBlacklistExts are additionally excluded when targeting macOS.
var darwinBlacklistExts = map[string]bool{
	".exe":      true,
	".appimage": true,
}

// Pick selects the best release asset for the goos/goarch pair. assetFilter,
// when non-empty, narrows the candidates by substring (a leading '^' inverts
// the match). It returns the chosen asset name, a note describing which
// tiebreak path (if any) produced the choice, and an error only when nothing
// installable remains after filtering.
//
// Before any ranking runs, candidates are partitioned by installability:
// extractable archives and true bare executables are preferred, while
// application containers (.appimage/.dmg/.pkg), archive formats hukou cannot
// extract, and platform-less archives that look like upstream source trees
// are demoted. The demoted partition is considered only when nothing
// preferred exists, and selecting from it emits a warning note naming the
// limitation. This partitioning keeps the vendored detector's OS-priority
// shortcut (e.g. the Linux .AppImage preference) from beating the real
// archive the same release also ships (PF-4), and keeps the detector's
// all-assets last resort from silently installing a source tarball.
//
// detect.go is intentionally left untouched (vendored eget): the fix lives
// entirely in this waterfall layer.
func Pick(assets []string, goos, goarch, assetFilter string) (string, string, error) {
	sd, err := NewSystemDetector(goos, goarch)
	if err != nil {
		return "", "", err
	}

	preferred, demoted := partition(assets, goos)

	pool := preferred
	var notes []string
	if len(preferred) == 0 {
		if len(demoted) == 0 {
			return "", "", noCandidatesErr(assets, goos, goarch)
		}
		pool = demotedNames(demoted)
		notes = fallbackNotes(demoted)
	}

	// Optional --asset substring filter (reuses eget SingleAssetDetector),
	// applied inside the selected partition: a filter can narrow the choice
	// but never promote a demoted candidate back over a preferred one.
	if assetFilter != "" {
		anti := false
		needle := assetFilter
		if strings.HasPrefix(needle, "^") {
			anti = true
			needle = needle[1:]
		}
		d := &SingleAssetDetector{Asset: needle, Anti: anti}
		choice, cands, _ := d.Detect(pool)
		switch {
		case choice != "":
			notes = append(notes, "asset-filter")
			return choice, strings.Join(notes, ","), nil
		case len(cands) > 0:
			pool = cands
		default:
			return "", "", noCandidatesErr(assets, goos, goarch)
		}
	}

	// Primary OS/Arch match via the vendored SystemDetector.
	choice, cands, _ := sd.Detect(pool)
	if choice != "" {
		return choice, strings.Join(notes, ","), nil
	}
	if len(cands) == 0 {
		return "", "", noCandidatesErr(assets, goos, goarch)
	}

	// Deterministic tiebreak waterfall on the remaining candidates. The
	// fallback notes (if any) are carried through: they must survive the
	// tiebreak's note accumulation, not be overwritten by it.
	cands, notes = tiebreak(cands, goos, goarch, notes)

	if len(cands) == 1 {
		return cands[0], strings.Join(notes, ","), nil
	}

	// Still ambiguous: fail closed and require an explicit --asset filter.
	sort.Strings(cands)
	return "", strings.Join(notes, ","), i18n.Errorf("multiple matching assets; use --asset to choose one: %s", strings.Join(cands, ", "))
}

// appContainerExts are single-file application containers: self-mounting or
// runtime-dependent bundles that DetectFormat classifies as bare files but
// that hukou cannot install as a working binary (an AppImage needs libfuse2
// and a mount; .dmg/.pkg are OS image/installer formats). They are demoted
// below every extractable archive and every true bare executable.
var appContainerExts = map[string]bool{
	".appimage": true,
	".dmg":      true,
	".pkg":      true,
}

// refusal pairs a rejected asset with the reason it can never be installed
// correctly, so a total refusal can name every dropped candidate.
type refusal struct {
	name   string
	reason string
}

// partition splits assets into the installable-preferred pool and the demoted
// pool, applying the OS-specific extension blacklist to both. Demotion
// reasons, most specific first:
//
//   - unsupported format: DetectFormat says hukou cannot extract it;
//   - likely source archive: an archive whose stem names neither any known
//     operating system nor any known architecture (release engineering
//     attaches platform tokens to compiled artifacts);
//   - application container: .appimage/.dmg/.pkg (see appContainerExts).
func partition(assets []string, goos string) (preferred []string, demoted []refusal) {
	for _, a := range assets {
		if blacklisted(a, goos) {
			continue
		}
		switch {
		case !archive.DetectFormat(a).Supported():
			demoted = append(demoted, refusal{a, "unsupported format " + formatLimit(a)})
		case looksLikeSourceArchive(a):
			demoted = append(demoted, refusal{a, "likely source archive"})
		case appContainerExts[effectiveExt(a)]:
			demoted = append(demoted, refusal{a, "application container"})
		default:
			preferred = append(preferred, a)
		}
	}
	return preferred, demoted
}

// demotedNames extracts the asset names from a partition result.
func demotedNames(demoted []refusal) []string {
	names := make([]string, 0, len(demoted))
	for _, r := range demoted {
		names = append(names, r.name)
	}
	return names
}

// fallbackNotes builds the warning lines emitted when selection had to
// settle for demoted candidates. Each distinct limiting format contributes
// one "<format>-fallback" note through the standard note channel; the
// source-archive demotion contributes "source-fallback". Notes are sorted
// so the warning does not depend on API asset order.
func fallbackNotes(demoted []refusal) []string {
	seen := map[string]bool{}
	var notes []string
	for _, r := range demoted {
		var limit string
		switch {
		case strings.HasPrefix(r.reason, "unsupported format "):
			limit = strings.TrimPrefix(r.reason, "unsupported format ")
		case r.reason == "application container":
			limit = strings.TrimPrefix(effectiveExt(r.name), ".")
		default:
			limit = "source"
		}
		if seen[limit] {
			continue
		}
		seen[limit] = true
		notes = append(notes, limit+"-fallback")
	}
	sort.Strings(notes)
	return notes
}

// noInstallableErr explains a total refusal: every non-blacklisted candidate
// was demoted, so downloading any of them cannot yield a working install.
func noInstallableErr(goos, goarch string, demoted []refusal) error {
	parts := make([]string, 0, len(demoted))
	for _, r := range demoted {
		parts = append(parts, r.name+" ("+r.reason+")")
	}
	sort.Strings(parts)
	return i18n.Errorf("no installable asset for %s/%s; all candidates were refused: %s", goos, goarch, strings.Join(parts, ", "))
}

// formatLimit names the concrete archive format of an asset DetectFormat
// refuses to install, for use in refusal errors and warning notes.
func formatLimit(name string) string {
	if ext := effectiveExt(name); ext != "" {
		return strings.TrimPrefix(ext, ".")
	}
	return "unrecognized"
}

// looksLikeSourceArchive reports whether name is a compressed or multi-file
// archive whose stem names neither any known operating system nor any known
// architecture. Compiled release artifacts carry platform tokens; a
// platform-less archive is almost certainly the upstream source tree, which
// hukou cannot install. Bare files are exempt: they are installed directly,
// so there is nothing to mis-extract.
func looksLikeSourceArchive(name string) bool {
	if archive.DetectFormat(name) == archive.FormatBare {
		return false
	}
	return !namesAnyPlatform(name)
}

// namesAnyPlatform reports whether s matches any known OS or architecture
// matcher from the vendored detector tables.
func namesAnyPlatform(s string) bool {
	for _, osm := range goosmap {
		if ok, _ := osm.Match(s); ok {
			return true
		}
	}
	for _, arch := range goarchmap {
		if arch.Match(s) {
			return true
		}
	}
	return false
}

// tiebreak applies the arch-preference, 32-bit drop, and archive-format
// preferences in order, returning the narrowed list and the notes describing
// which steps actually reduced the candidate set.
func tiebreak(cands []string, goos, goarch string, notes []string) ([]string, []string) {
	// (a) darwin/arm64: prefer arm64/aarch64/universal, else fall back to
	// amd64/x86_64 (runs under Rosetta 2).
	if goos == "darwin" && goarch == "arm64" {
		var arm []string
		for _, c := range cands {
			if ArchArm64.Match(c) || strings.Contains(strings.ToLower(c), "universal") {
				arm = append(arm, c)
			}
		}
		if len(arm) > 0 {
			cands = arm
		} else {
			var amd []string
			for _, c := range cands {
				if ArchAMD64.Match(c) {
					amd = append(amd, c)
				}
			}
			if len(amd) > 0 {
				notes = append(notes, "rosetta-fallback")
				cands = amd
			}
		}
	}

	// (b) 64-bit platforms: drop obvious 32-bit assets.
	if is64Bit(goarch) {
		var kept []string
		for _, c := range cands {
			if ArchI386.Match(c) || ArchArm.Match(c) {
				continue
			}
			kept = append(kept, c)
		}
		if len(kept) > 0 {
			if len(kept) < len(cands) {
				notes = append(notes, "drop-32bit")
			}
			cands = kept
		}
	}

	// (c) supported archive-format preference: .tar.gz > .zip > bare.
	tiers := [][]string{
		{".tar.gz", ".tgz"},
		{".zip"},
		{".gz"},
	}
	for _, tier := range tiers {
		var sub []string
		for _, c := range cands {
			if hasAnySuffix(c, tier...) {
				sub = append(sub, c)
			}
		}
		if len(sub) > 0 {
			if len(sub) < len(cands) {
				notes = append(notes, "archive-preference")
			}
			cands = sub
			break
		}
	}

	// (d) linux: prefer musl-linked assets (statically self-contained) over
	// candidates that carry no libc identifier, mirroring ubi's documented
	// rule. On darwin this step is a no-op.
	if goos == "linux" {
		var musl []string
		for _, c := range cands {
			if strings.Contains(strings.ToLower(c), "musl") {
				musl = append(musl, c)
			}
		}
		if len(musl) > 0 && len(musl) < len(cands) {
			notes = append(notes, "musl-preference")
			cands = musl
		}
	}

	// Determinism: the remaining set is fixed by the rules above, but its
	// order still reflects the (API-supplied) input order. Sort it so the
	// final choice and the fail-closed ambiguity listing never depend on the
	// order GitHub happened to return assets in.
	sort.Strings(cands)

	return cands, notes
}

// blacklisted reports whether the asset name carries an extension that never
// holds the wanted binary for the given OS.
func blacklisted(name, goos string) bool {
	ext := effectiveExt(name)
	if ext == "" {
		return false
	}
	if blacklistExts[ext] {
		return true
	}
	if goos == "darwin" && darwinBlacklistExts[ext] {
		return true
	}
	return false
}

// effectiveExt returns the meaningful extension of a file name. Known archive
// compound suffixes are stripped first, and an all-digit trailer (a version
// number such as ".5") is treated as no extension.
func effectiveExt(name string) string {
	lower := strings.ToLower(path.Base(name))
	for _, ae := range archiveCompoundSuffixes {
		if strings.HasSuffix(lower, ae) {
			lower = strings.TrimSuffix(lower, ae)
			break
		}
	}
	ext := path.Ext(lower)
	if ext == "" {
		return ""
	}
	allDigits := true
	for _, r := range ext[1:] {
		if r < '0' || r > '9' {
			allDigits = false
			break
		}
	}
	if allDigits {
		return ""
	}
	return ext
}

func hasAnySuffix(s string, suffixes ...string) bool {
	l := strings.ToLower(s)
	for _, suf := range suffixes {
		if strings.HasSuffix(l, suf) {
			return true
		}
	}
	return false
}

func is64Bit(goarch string) bool {
	switch goarch {
	case "amd64", "arm64", "riscv64":
		return true
	}
	return false
}

func noCandidatesErr(assets []string, goos, goarch string) error {
	return i18n.Errorf("no suitable asset for %s/%s; available assets: %s", goos, goarch, strings.Join(assets, ", "))
}
