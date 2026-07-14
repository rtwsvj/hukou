package versionpolicy_test

import (
	"errors"
	"testing"

	"github.com/rtwsvj/hukou/internal/manifest"
	"github.com/rtwsvj/hukou/internal/versionpolicy"
)

func semverPolicy(channel manifest.UpdateChannel) manifest.UpdatePolicy {
	return manifest.UpdatePolicy{Mode: manifest.UpdateModeSemver, Channel: channel}
}

func TestNormalizeDefaultsAndRejectsUnknownValues(t *testing.T) {
	got, err := versionpolicy.Normalize(manifest.UpdatePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Mode != manifest.UpdateModeSemver || got.Channel != manifest.UpdateChannelStable {
		t.Fatalf("defaults=%+v", got)
	}
	for _, policy := range []manifest.UpdatePolicy{
		{Mode: "calendar", Channel: manifest.UpdateChannelStable},
		{Mode: manifest.UpdateModeSemver, Channel: "nightly"},
		{Mode: manifest.UpdateModeSemver, Channel: manifest.UpdateChannelStable, PinnedTag: " v1.0.0"},
	} {
		if _, err := versionpolicy.Normalize(policy); !errors.Is(err, versionpolicy.ErrInvalidPolicy) {
			t.Fatalf("policy=%+v error=%v", policy, err)
		}
	}
}

func TestIsSemanticTagRequiresStrictSortableVersion(t *testing.T) {
	for _, tag := range []string{"v1.2.3", "1.2.3", "v2.0.0-beta.1", "1.2.3+build.7"} {
		if !versionpolicy.IsSemanticTag(tag) {
			t.Fatalf("valid semantic tag rejected: %q", tag)
		}
	}
	for _, tag := range []string{"", "release-2024", "adopted", "local", "original", " v1.2.3", "v1", "1", "v1.2", "1.2"} {
		if versionpolicy.IsSemanticTag(tag) {
			t.Fatalf("non-semantic tag accepted: %q", tag)
		}
	}
}

func TestSemverSelectionIgnoresShorthandCandidates(t *testing.T) {
	decision, err := versionpolicy.Select(
		semverPolicy(manifest.UpdateChannelStable),
		"v1.0.0",
		[]versionpolicy.Release{{Tag: "v9"}, {Tag: "8.1"}, {Tag: "v2.1.0"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != versionpolicy.ActionActivate || decision.CandidateTag != "v2.1.0" {
		t.Fatalf("decision=%+v", decision)
	}

	for _, channel := range []manifest.UpdateChannel{manifest.UpdateChannelStable, manifest.UpdateChannelPrerelease} {
		_, err := versionpolicy.Select(
			semverPolicy(channel),
			"v1.0.0",
			[]versionpolicy.Release{{Tag: "v2"}, {Tag: "2.1"}},
		)
		if !errors.Is(err, versionpolicy.ErrNoCandidate) {
			t.Fatalf("channel=%q error=%v", channel, err)
		}
	}
}

func TestSemverSelectionRejectsShorthandCurrentBaseline(t *testing.T) {
	for _, current := range []string{"v1", "1", "v1.2", "1.2"} {
		_, err := versionpolicy.Select(
			semverPolicy(manifest.UpdateChannelStable),
			current,
			[]versionpolicy.Release{{Tag: "v2.0.0"}},
		)
		if !errors.Is(err, versionpolicy.ErrCurrentNotSemver) {
			t.Fatalf("current=%q error=%v", current, err)
		}
	}
}

func TestUnorderedBaselinesAndLegacyModeRemainSupported(t *testing.T) {
	for _, current := range []string{"local", "adopted", "original"} {
		decision, err := versionpolicy.Select(
			semverPolicy(manifest.UpdateChannelStable),
			current,
			[]versionpolicy.Release{{Tag: "v1.0.0"}},
		)
		if err != nil || decision.Action != versionpolicy.ActionActivate {
			t.Fatalf("current=%q decision=%+v error=%v", current, decision, err)
		}
	}

	legacy := manifest.UpdatePolicy{Mode: manifest.UpdateModeLegacy, Channel: manifest.UpdateChannelStable}
	decision, err := versionpolicy.Select(legacy, "v1.2", []versionpolicy.Release{{Tag: "release-42"}})
	if err != nil || decision.Action != versionpolicy.ActionActivate || decision.CandidateTag != "release-42" {
		t.Fatalf("legacy decision=%+v error=%v", decision, err)
	}
}

func TestStableSelectsHighestSemverAndExcludesPrereleases(t *testing.T) {
	releases := []versionpolicy.Release{
		{Tag: "v1.5.0"},
		{Tag: "v3.0.0", Draft: true},
		{Tag: "v2.0.0-beta.2", Prerelease: true},
		{Tag: "2.0.0-beta.3"}, // Tag syntax alone also identifies prerelease.
		{Tag: "v1.9.0"},
		{Tag: "not-semver"},
	}
	decision, err := versionpolicy.Select(semverPolicy(manifest.UpdateChannelStable), "v1.0.0", releases)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != versionpolicy.ActionActivate || decision.CandidateTag != "v1.9.0" {
		t.Fatalf("decision=%+v", decision)
	}
}

func TestPrereleaseChannelUsesSemanticPrecedence(t *testing.T) {
	releases := []versionpolicy.Release{
		{Tag: "v2.0.0-beta.2", Prerelease: true},
		{Tag: "v1.9.9"},
		{Tag: "v2.0.0-beta.11", Prerelease: true},
	}
	decision, err := versionpolicy.Select(semverPolicy(manifest.UpdateChannelPrerelease), "v1.0.0", releases)
	if err != nil {
		t.Fatal(err)
	}
	if decision.CandidateTag != "v2.0.0-beta.11" {
		t.Fatalf("decision=%+v", decision)
	}
}

func TestNormalizedEquivalentVersionIsNoop(t *testing.T) {
	releases := []versionpolicy.Release{{Tag: "1.2.3+new-build"}}
	decision, err := versionpolicy.Select(semverPolicy(manifest.UpdateChannelStable), "v1.2.3+old-build", releases)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != versionpolicy.ActionNone {
		t.Fatalf("decision=%+v", decision)
	}
}

func TestSemanticDowngradeFailsClosed(t *testing.T) {
	_, err := versionpolicy.Select(
		semverPolicy(manifest.UpdateChannelStable),
		"v2.0.0",
		[]versionpolicy.Release{{Tag: "v1.99.0"}},
	)
	if !errors.Is(err, versionpolicy.ErrDowngrade) {
		t.Fatalf("error=%v", err)
	}
}

func TestLocalEntryCanEstablishSemverBaseline(t *testing.T) {
	decision, err := versionpolicy.Select(
		semverPolicy(manifest.UpdateChannelStable),
		"local",
		[]versionpolicy.Release{{Tag: "v1.0.0"}},
	)
	if err != nil || decision.Action != versionpolicy.ActionActivate {
		t.Fatalf("decision=%+v error=%v", decision, err)
	}
	_, err = versionpolicy.Select(
		semverPolicy(manifest.UpdateChannelStable),
		"nightly",
		[]versionpolicy.Release{{Tag: "v1.0.0"}},
	)
	if !errors.Is(err, versionpolicy.ErrCurrentNotSemver) {
		t.Fatalf("error=%v", err)
	}
}

func TestExactPinOverridesChannelAndOrdering(t *testing.T) {
	policy := semverPolicy(manifest.UpdateChannelStable)
	policy.PinnedTag = "v1.0.0-beta.1"
	decision, err := versionpolicy.Select(policy, "v2.0.0", []versionpolicy.Release{
		{Tag: "v2.1.0"},
		{Tag: "v1.0.0-beta.1", Prerelease: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != versionpolicy.ActionActivate || !decision.Pinned || decision.CandidateTag != policy.PinnedTag {
		t.Fatalf("decision=%+v", decision)
	}
}

func TestAlreadyPinnedNeedsNoReleaseList(t *testing.T) {
	policy := semverPolicy(manifest.UpdateChannelStable)
	policy.PinnedTag = "v1.0.0"
	if !versionpolicy.PinnedCurrent(policy, "v1.0.0") {
		t.Fatal("PinnedCurrent=false")
	}
	decision, err := versionpolicy.Select(policy, "v1.0.0", nil)
	if err != nil || decision.Action != versionpolicy.ActionNone || !decision.Pinned {
		t.Fatalf("decision=%+v error=%v", decision, err)
	}
}

func TestMissingOrDraftPinFailsClosed(t *testing.T) {
	policy := semverPolicy(manifest.UpdateChannelStable)
	policy.PinnedTag = "v1.0.0"
	for name, releases := range map[string][]versionpolicy.Release{
		"missing": {{Tag: "v2.0.0"}},
		"draft":   {{Tag: "v1.0.0", Draft: true}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := versionpolicy.Select(policy, "v0.9.0", releases)
			if !errors.Is(err, versionpolicy.ErrPinnedReleaseNotFound) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestLegacyModePreservesNewestFirstTagBehavior(t *testing.T) {
	policy := manifest.UpdatePolicy{Mode: manifest.UpdateModeLegacy, Channel: manifest.UpdateChannelStable}
	decision, err := versionpolicy.Select(policy, "release-41", []versionpolicy.Release{
		{Tag: "release-43", Prerelease: true},
		{Tag: "release-42"},
		{Tag: "release-99"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.CandidateTag != "release-42" || decision.Action != versionpolicy.ActionActivate {
		t.Fatalf("decision=%+v", decision)
	}
}

func TestNoEligibleSemanticCandidate(t *testing.T) {
	_, err := versionpolicy.Select(
		semverPolicy(manifest.UpdateChannelStable),
		"v1.0.0",
		[]versionpolicy.Release{{Tag: "nightly"}, {Tag: "v2.0.0-beta.1", Prerelease: true}},
	)
	if !errors.Is(err, versionpolicy.ErrNoCandidate) {
		t.Fatalf("error=%v", err)
	}
}
