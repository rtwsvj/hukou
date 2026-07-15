// Package versionpolicy makes deterministic, side-effect-free release
// selection decisions. Network clients translate their release metadata into
// Release values; this package never performs I/O.
package versionpolicy

import (
	"errors"
	"fmt"
	"strings"

	"github.com/rtwsvj/hukou/internal/manifest"
	"golang.org/x/mod/semver"
)

var (
	ErrInvalidPolicy         = errors.New("invalid update policy")
	ErrNoCandidate           = errors.New("no eligible release candidate")
	ErrPinnedReleaseNotFound = errors.New("pinned release not found")
	ErrCurrentNotSemver      = errors.New("current version is not semantic")
	ErrDowngrade             = errors.New("release candidate would downgrade the current version")
)

type Action string

const (
	ActionNone     Action = "none"
	ActionActivate Action = "activate"
)

// Release is the minimum trusted metadata needed to select a version. Releases
// should be supplied newest-first for legacy GitHub-latest mode; semantic mode
// is independent of input order.
type Release struct {
	Tag        string
	Draft      bool
	Prerelease bool
}

// Decision explains whether a candidate should be activated. Pinned is true
// when an exact user pin intentionally overrode normal ordering, including a
// possible downgrade.
type Decision struct {
	Action       Action
	CurrentTag   string
	CandidateTag string
	Pinned       bool
	Reason       string
}

// Normalize fills safe defaults and validates a policy without changing the
// caller's value.
func Normalize(policy manifest.UpdatePolicy) (manifest.UpdatePolicy, error) {
	if policy.Mode == "" {
		policy.Mode = manifest.UpdateModeSemver
	}
	if policy.Channel == "" {
		policy.Channel = manifest.UpdateChannelStable
	}
	switch policy.Mode {
	case manifest.UpdateModeSemver, manifest.UpdateModeLegacy:
	default:
		return manifest.UpdatePolicy{}, fmt.Errorf("%w: unsupported mode %q", ErrInvalidPolicy, policy.Mode)
	}
	switch policy.Channel {
	case manifest.UpdateChannelStable, manifest.UpdateChannelPrerelease:
	default:
		return manifest.UpdatePolicy{}, fmt.Errorf("%w: unsupported channel %q", ErrInvalidPolicy, policy.Channel)
	}
	if policy.PinnedTag != "" && strings.TrimSpace(policy.PinnedTag) != policy.PinnedTag {
		return manifest.UpdatePolicy{}, fmt.Errorf("%w: pinned tag has surrounding whitespace", ErrInvalidPolicy)
	}
	return policy, nil
}

// PinnedCurrent reports whether upgrade can return a no-op without any
// network request because the exact requested pin is already active.
func PinnedCurrent(policy manifest.UpdatePolicy, currentTag string) bool {
	return policy.PinnedTag != "" && policy.PinnedTag == currentTag
}

// IsSemanticTag reports whether tag is a strict, deterministically sortable
// Semantic Version. Hukou accepts the conventional optional lowercase v
// prefix, but does not accept local/adopted/original baseline sentinels here.
func IsSemanticTag(tag string) bool {
	if tag == "" || strings.TrimSpace(tag) != tag {
		return false
	}
	version := normalizeVersion(tag)
	if !semver.IsValid(version) {
		return false
	}
	core := strings.TrimPrefix(version, "v")
	if suffix := strings.IndexAny(core, "-+"); suffix >= 0 {
		core = core[:suffix]
	}
	return strings.Count(core, ".") == 2
}

// Select chooses a release according to policy and prevents implicit semantic
// downgrades. Exact pins are deliberate desired-state requests and therefore
// may move either forward or backward.
func Select(policy manifest.UpdatePolicy, currentTag string, releases []Release) (Decision, error) {
	policy, err := Normalize(policy)
	if err != nil {
		return Decision{}, err
	}
	if policy.PinnedTag != "" {
		return selectPin(policy.PinnedTag, currentTag, releases)
	}
	if policy.Mode == manifest.UpdateModeLegacy {
		return selectLegacy(policy.Channel, currentTag, releases)
	}
	return selectSemver(policy.Channel, currentTag, releases)
}

func selectPin(pin, currentTag string, releases []Release) (Decision, error) {
	if pin == currentTag {
		return Decision{
			Action:       ActionNone,
			CurrentTag:   currentTag,
			CandidateTag: pin,
			Pinned:       true,
			Reason:       "exact pin is already active",
		}, nil
	}
	for _, release := range releases {
		if release.Tag != pin {
			continue
		}
		if release.Draft {
			return Decision{}, fmt.Errorf("%w: %q is a draft", ErrPinnedReleaseNotFound, pin)
		}
		return Decision{
			Action:       ActionActivate,
			CurrentTag:   currentTag,
			CandidateTag: release.Tag,
			Pinned:       true,
			Reason:       "activate exact pinned release",
		}, nil
	}
	return Decision{}, fmt.Errorf("%w: %q", ErrPinnedReleaseNotFound, pin)
}

func selectLegacy(channel manifest.UpdateChannel, currentTag string, releases []Release) (Decision, error) {
	for _, release := range releases {
		if release.Draft || release.Tag == "" {
			continue
		}
		if channel == manifest.UpdateChannelStable && release.Prerelease {
			continue
		}
		action := ActionActivate
		reason := "GitHub latest tag differs from current tag"
		if release.Tag == currentTag {
			action = ActionNone
			reason = "GitHub latest tag is already active"
		}
		return Decision{
			Action:       action,
			CurrentTag:   currentTag,
			CandidateTag: release.Tag,
			Reason:       reason,
		}, nil
	}
	return Decision{}, ErrNoCandidate
}

func selectSemver(channel manifest.UpdateChannel, currentTag string, releases []Release) (Decision, error) {
	var selected Release
	selectedVersion := ""
	for _, release := range releases {
		if release.Draft || release.Tag == "" {
			continue
		}
		if !IsSemanticTag(release.Tag) {
			continue
		}
		version := normalizeVersion(release.Tag)
		isPrerelease := release.Prerelease || semver.Prerelease(version) != ""
		if channel == manifest.UpdateChannelStable && isPrerelease {
			continue
		}
		comparison := semver.Compare(version, selectedVersion)
		if selectedVersion == "" || comparison > 0 || comparison == 0 && release.Tag < selected.Tag {
			selected = release
			selectedVersion = version
		}
	}
	if selectedVersion == "" {
		return Decision{}, ErrNoCandidate
	}

	if unorderedBaseline(currentTag) {
		return Decision{
			Action:       ActionActivate,
			CurrentTag:   currentTag,
			CandidateTag: selected.Tag,
			Reason:       "establish semantic version baseline",
		}, nil
	}
	currentVersion := normalizeVersion(currentTag)
	if !IsSemanticTag(currentTag) {
		return Decision{}, fmt.Errorf("%w: %q", ErrCurrentNotSemver, currentTag)
	}
	switch semver.Compare(selectedVersion, currentVersion) {
	case -1:
		return Decision{}, fmt.Errorf("%w: candidate %s is lower than current %s", ErrDowngrade, selected.Tag, currentTag)
	case 0:
		return Decision{
			Action:       ActionNone,
			CurrentTag:   currentTag,
			CandidateTag: selected.Tag,
			Reason:       "highest eligible semantic version is already active",
		}, nil
	default:
		return Decision{
			Action:       ActionActivate,
			CurrentTag:   currentTag,
			CandidateTag: selected.Tag,
			Reason:       "higher eligible semantic version is available",
		}, nil
	}
}

func normalizeVersion(tag string) string {
	if strings.HasPrefix(tag, "v") {
		return tag
	}
	return "v" + tag
}

func unorderedBaseline(tag string) bool {
	switch strings.ToLower(tag) {
	case "local", "adopted", "original":
		return true
	default:
		return false
	}
}
