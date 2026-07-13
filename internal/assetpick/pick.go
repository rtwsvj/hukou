// This file is original hukou code layered on top of the vendored eget
// detector in detect.go (which is not modified). It adds a blacklist
// prefilter, an optional --asset substring filter, and a deterministic
// tiebreak waterfall on top of eget's SystemDetector.
package assetpick

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/rtwsvj/hukou/internal/archive"
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
// remains after filtering.
func Pick(assets []string, goos, goarch, assetFilter string) (string, string, error) {
	original := assets

	// (1) capability + blacklist prefilter. Known archive formats that hukou
	// cannot extract must be removed before the system detector can select a
	// direct match (which would otherwise bypass the tiebreak waterfall).
	var filtered []string
	for _, a := range assets {
		if !archive.DetectFormat(a).Supported() {
			continue
		}
		if blacklisted(a, goos) {
			continue
		}
		filtered = append(filtered, a)
	}
	if len(filtered) == 0 {
		return "", "", noCandidatesErr(original, goos, goarch)
	}

	// (2) optional --asset substring filter (reuses eget SingleAssetDetector).
	if assetFilter != "" {
		anti := false
		needle := assetFilter
		if strings.HasPrefix(needle, "^") {
			anti = true
			needle = needle[1:]
		}
		d := &SingleAssetDetector{Asset: needle, Anti: anti}
		choice, cands, _ := d.Detect(filtered)
		switch {
		case choice != "":
			return choice, "asset-filter", nil
		case len(cands) > 0:
			filtered = cands
		default:
			return "", "", noCandidatesErr(original, goos, goarch)
		}
	}

	// (3) primary OS/Arch match via the vendored SystemDetector.
	sd, err := NewSystemDetector(goos, goarch)
	if err != nil {
		return "", "", err
	}
	choice, cands, _ := sd.Detect(filtered)
	if choice != "" {
		return choice, "", nil
	}
	if len(cands) == 0 {
		return "", "", noCandidatesErr(original, goos, goarch)
	}

	// (4) deterministic tiebreak waterfall on the remaining candidates.
	var notes []string
	cands, notes = tiebreak(cands, goos, goarch, notes)

	if len(cands) == 1 {
		return cands[0], strings.Join(notes, ","), nil
	}

	// (5) still ambiguous: fail closed and require an explicit --asset filter.
	sort.Strings(cands)
	return "", strings.Join(notes, ","), fmt.Errorf("multiple matching assets; use --asset to choose one: %s", strings.Join(cands, ", "))
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
	return fmt.Errorf("no suitable asset for %s/%s; available assets: %s",
		goos, goarch, strings.Join(assets, ", "))
}
