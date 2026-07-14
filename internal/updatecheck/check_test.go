package updatecheck

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/rtwsvj/hukou/internal/ghrelease"
	"github.com/rtwsvj/hukou/internal/manifest"
	"github.com/rtwsvj/hukou/internal/store"
	"github.com/rtwsvj/hukou/internal/versionpolicy"
)

type releaseSourceFunc func(owner, repo string) (ghrelease.Release, error)

func (f releaseSourceFunc) Latest(owner, repo string) (ghrelease.Release, error) {
	return f(owner, repo)
}

type recordingReleaseSource struct {
	latestCalls int
	listCalls   int
	byTagCalls  int
	latest      ghrelease.Release
	releases    []ghrelease.Release
	byTag       ghrelease.Release
	err         error
}

func (s *recordingReleaseSource) Latest(owner, repo string) (ghrelease.Release, error) {
	s.latestCalls++
	return s.latest, s.err
}

func (s *recordingReleaseSource) List(owner, repo string) ([]ghrelease.Release, error) {
	s.listCalls++
	return append([]ghrelease.Release(nil), s.releases...), s.err
}

func (s *recordingReleaseSource) ByTag(owner, repo, tag string) (ghrelease.Release, error) {
	s.byTagCalls++
	return s.byTag, s.err
}

func (s *recordingReleaseSource) totalCalls() int {
	return s.latestCalls + s.listCalls + s.byTagCalls
}

func updateEntry(t *testing.T, tag string) manifest.Entry {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tool")
	if err := os.WriteFile(path, []byte("binary\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	sha, err := store.SHA256File(path)
	if err != nil {
		t.Fatal(err)
	}
	return manifest.Entry{Name: "tool", Path: path, Repo: "owner/repo", Tag: tag, SHA256: sha}
}

func completeAsset(name string) ghrelease.Asset {
	return ghrelease.Asset{
		Name:               name,
		BrowserDownloadURL: "https://example.test/assets/" + name,
		Size:               1,
	}
}

func TestCheckerOutdatedSelectsSamePlatformAsset(t *testing.T) {
	entry := updateEntry(t, "v1.0.0")
	asset := fmt.Sprintf("tool-%s-%s.tar.gz", runtime.GOOS, runtime.GOARCH)
	source := releaseSourceFunc(func(owner, repo string) (ghrelease.Release, error) {
		if owner != "owner" || repo != "repo" {
			t.Fatalf("repo = %s/%s", owner, repo)
		}
		return ghrelease.Release{TagName: "v2.0.0", Assets: []ghrelease.Asset{completeAsset(asset)}}, nil
	})
	checked, err := New(source).Check(entry, "")
	if err != nil {
		t.Fatal(err)
	}
	if checked.Status != StatusOutdated || checked.LatestTag != "v2.0.0" || checked.Asset != asset {
		t.Fatalf("unexpected check: %+v", checked)
	}
}

func TestCheckerCurrentAndLocal(t *testing.T) {
	entry := updateEntry(t, "v1.0.0")
	calls := 0
	source := releaseSourceFunc(func(owner, repo string) (ghrelease.Release, error) {
		calls++
		return ghrelease.Release{TagName: "v1.0.0"}, nil
	})
	checked, err := New(source).Check(entry, "")
	if err != nil || checked.Status != StatusCurrent {
		t.Fatalf("current check: %+v err=%v", checked, err)
	}
	entry.Repo = ""
	entry.Tag = "local"
	checked, err = New(source).Check(entry, "")
	if err != nil || checked.Status != StatusLocal || calls != 1 {
		t.Fatalf("local check: %+v calls=%d err=%v", checked, calls, err)
	}
}

func TestCheckerDriftFailsBeforeNetwork(t *testing.T) {
	entry := updateEntry(t, "v1.0.0")
	if err := os.WriteFile(entry.Path, []byte("external\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	calls := 0
	source := releaseSourceFunc(func(owner, repo string) (ghrelease.Release, error) {
		calls++
		return ghrelease.Release{}, nil
	})
	checked, err := New(source).Check(entry, "")
	if err == nil || checked.Status != StatusDrifted || calls != 0 {
		t.Fatalf("drift check: %+v calls=%d err=%v", checked, calls, err)
	}
}

func TestCheckerSemverStableUsesReleaseListAndFiltersCandidates(t *testing.T) {
	entry := updateEntry(t, "v1.0.0")
	entry.UpdatePolicy = manifest.UpdatePolicy{Mode: manifest.UpdateModeSemver, Channel: manifest.UpdateChannelStable}
	asset := fmt.Sprintf("tool-%s-%s.tar.gz", runtime.GOOS, runtime.GOARCH)
	source := &recordingReleaseSource{releases: []ghrelease.Release{
		{TagName: "v9.0.0", Draft: true, Assets: []ghrelease.Asset{completeAsset(asset)}},
		{TagName: "v3.0.0-beta.1", Prerelease: true, Assets: []ghrelease.Asset{completeAsset(asset)}},
		{TagName: "v2.1.0", Assets: []ghrelease.Asset{completeAsset(asset)}},
		{TagName: "v2.0.0", Assets: []ghrelease.Asset{completeAsset(asset)}},
	}}

	checked, err := New(source).Check(entry, "")
	if err != nil {
		t.Fatal(err)
	}
	if checked.Status != StatusOutdated || checked.LatestTag != "v2.1.0" || checked.Asset != asset {
		t.Fatalf("checked=%+v", checked)
	}
	if source.listCalls != 1 || source.latestCalls != 0 || source.byTagCalls != 0 {
		t.Fatalf("endpoint calls: latest=%d list=%d byTag=%d", source.latestCalls, source.listCalls, source.byTagCalls)
	}
}

func TestCheckerSemverIgnoresShorthandReleaseCandidates(t *testing.T) {
	entry := updateEntry(t, "v1.0.0")
	entry.UpdatePolicy = manifest.UpdatePolicy{Mode: manifest.UpdateModeSemver, Channel: manifest.UpdateChannelStable}
	asset := fmt.Sprintf("tool-%s-%s.tar.gz", runtime.GOOS, runtime.GOARCH)
	source := &recordingReleaseSource{releases: []ghrelease.Release{
		{TagName: "v9", Assets: []ghrelease.Asset{completeAsset(asset)}},
		{TagName: "8.1", Assets: []ghrelease.Asset{completeAsset(asset)}},
		{TagName: "v2.1.0", Assets: []ghrelease.Asset{completeAsset(asset)}},
	}}

	checked, err := New(source).Check(entry, "")
	if err != nil || checked.Status != StatusOutdated || checked.LatestTag != "v2.1.0" {
		t.Fatalf("checked=%+v error=%v", checked, err)
	}
	if source.listCalls != 1 || source.latestCalls != 0 || source.byTagCalls != 0 {
		t.Fatalf("endpoint calls: latest=%d list=%d byTag=%d", source.latestCalls, source.listCalls, source.byTagCalls)
	}

	source = &recordingReleaseSource{releases: []ghrelease.Release{{TagName: "v2"}, {TagName: "2.1"}}}
	checked, err = New(source).Check(entry, "")
	if !errors.Is(err, versionpolicy.ErrNoCandidate) || checked.Status != StatusUnsupported || checked.Asset != "" {
		t.Fatalf("checked=%+v error=%v", checked, err)
	}
	if source.listCalls != 1 || source.latestCalls != 0 || source.byTagCalls != 0 {
		t.Fatalf("endpoint calls: latest=%d list=%d byTag=%d", source.latestCalls, source.listCalls, source.byTagCalls)
	}
}

func TestCheckerSemverShorthandCurrentFailsClosedAfterMetadataOnly(t *testing.T) {
	asset := fmt.Sprintf("tool-%s-%s.tar.gz", runtime.GOOS, runtime.GOARCH)
	for _, current := range []string{"v1", "v1.2"} {
		t.Run(current, func(t *testing.T) {
			entry := updateEntry(t, current)
			entry.UpdatePolicy = manifest.UpdatePolicy{Mode: manifest.UpdateModeSemver, Channel: manifest.UpdateChannelStable}
			source := &recordingReleaseSource{releases: []ghrelease.Release{{
				TagName: "v2.0.0", Assets: []ghrelease.Asset{completeAsset(asset)},
			}}}

			checked, err := New(source).Check(entry, "")
			if !errors.Is(err, versionpolicy.ErrCurrentNotSemver) || checked.Status != StatusUnsupported || checked.Asset != "" {
				t.Fatalf("checked=%+v error=%v", checked, err)
			}
			if source.listCalls != 1 || source.latestCalls != 0 || source.byTagCalls != 0 {
				t.Fatalf("endpoint calls: latest=%d list=%d byTag=%d", source.latestCalls, source.listCalls, source.byTagCalls)
			}
		})
	}
}

func TestCheckerPrereleaseChannelUsesSemanticMaximum(t *testing.T) {
	entry := updateEntry(t, "v1.0.0")
	entry.UpdatePolicy = manifest.UpdatePolicy{Mode: manifest.UpdateModeSemver, Channel: manifest.UpdateChannelPrerelease}
	asset := fmt.Sprintf("tool-%s-%s.tar.gz", runtime.GOOS, runtime.GOARCH)
	source := &recordingReleaseSource{releases: []ghrelease.Release{
		{TagName: "v2.0.0-beta.2", Prerelease: true, Assets: []ghrelease.Asset{completeAsset(asset)}},
		{TagName: "v2.0.0-beta.11", Prerelease: true, Assets: []ghrelease.Asset{completeAsset(asset)}},
	}}
	checked, err := New(source).Check(entry, "")
	if err != nil || checked.LatestTag != "v2.0.0-beta.11" {
		t.Fatalf("checked=%+v error=%v", checked, err)
	}
}

func TestCheckerLegacyStableUsesOnlyLatestEndpoint(t *testing.T) {
	entry := updateEntry(t, "release-41")
	entry.UpdatePolicy = manifest.UpdatePolicy{Mode: manifest.UpdateModeLegacy, Channel: manifest.UpdateChannelStable}
	asset := fmt.Sprintf("tool-%s-%s.tar.gz", runtime.GOOS, runtime.GOARCH)
	source := &recordingReleaseSource{latest: ghrelease.Release{
		TagName: "release-42", Assets: []ghrelease.Asset{completeAsset(asset)},
	}}
	checked, err := New(source).Check(entry, "")
	if err != nil || checked.Status != StatusOutdated || checked.LatestTag != "release-42" {
		t.Fatalf("checked=%+v error=%v", checked, err)
	}
	if source.latestCalls != 1 || source.listCalls != 0 || source.byTagCalls != 0 {
		t.Fatalf("endpoint calls: latest=%d list=%d byTag=%d", source.latestCalls, source.listCalls, source.byTagCalls)
	}
}

func TestCheckerRejectsReleaseTagsThatCannotBeStored(t *testing.T) {
	for _, candidate := range []string{"release/v1", "original"} {
		t.Run(candidate, func(t *testing.T) {
			entry := updateEntry(t, "release-40")
			entry.UpdatePolicy = manifest.UpdatePolicy{Mode: manifest.UpdateModeLegacy, Channel: manifest.UpdateChannelStable}
			asset := completeAsset(fmt.Sprintf("tool-%s-%s.tar.gz", runtime.GOOS, runtime.GOARCH))
			source := &recordingReleaseSource{latest: ghrelease.Release{TagName: candidate, Assets: []ghrelease.Asset{asset}}}
			checked, err := New(source).Check(entry, "")
			if err == nil || checked.Status != StatusUnsupported {
				t.Fatalf("checked=%+v error=%v", checked, err)
			}
			if checked.Asset != "" {
				t.Fatalf("invalid release reached asset planning: %+v", checked)
			}
		})
	}
}

func TestCheckerRejectsIncompleteSelectedAssetMetadata(t *testing.T) {
	entry := updateEntry(t, "release-40")
	entry.UpdatePolicy = manifest.UpdatePolicy{Mode: manifest.UpdateModeLegacy, Channel: manifest.UpdateChannelStable}
	name := fmt.Sprintf("tool-%s-%s.tar.gz", runtime.GOOS, runtime.GOARCH)
	for testName, asset := range map[string]ghrelease.Asset{
		"missing URL":   {Name: name, Size: 1},
		"negative size": {Name: name, BrowserDownloadURL: "https://example.test/tool", Size: -1},
	} {
		t.Run(testName, func(t *testing.T) {
			source := &recordingReleaseSource{latest: ghrelease.Release{TagName: "release-41", Assets: []ghrelease.Asset{asset}}}
			checked, err := New(source).Check(entry, "")
			if err == nil || checked.Status != StatusUnsupported || checked.Asset != "" {
				t.Fatalf("checked=%+v error=%v", checked, err)
			}
		})
	}
}

func TestCheckerPinUsesOnlyExactByTagAndAllowsDeliberateDowngrade(t *testing.T) {
	entry := updateEntry(t, "v2.0.0")
	entry.UpdatePolicy = manifest.UpdatePolicy{
		Mode:      manifest.UpdateModeSemver,
		Channel:   manifest.UpdateChannelStable,
		PinnedTag: "v1.0.0-beta.1",
	}
	asset := fmt.Sprintf("tool-%s-%s.tar.gz", runtime.GOOS, runtime.GOARCH)
	source := &recordingReleaseSource{byTag: ghrelease.Release{
		TagName: "v1.0.0-beta.1", Prerelease: true, Assets: []ghrelease.Asset{completeAsset(asset)},
	}}
	checked, err := New(source).Check(entry, "")
	if err != nil {
		t.Fatal(err)
	}
	if checked.Status != StatusOutdated || checked.LatestTag != "v1.0.0-beta.1" {
		t.Fatalf("checked=%+v", checked)
	}
	if source.byTagCalls != 1 || source.latestCalls != 0 || source.listCalls != 0 {
		t.Fatalf("endpoint calls: latest=%d list=%d byTag=%d", source.latestCalls, source.listCalls, source.byTagCalls)
	}
}

func TestCheckerCurrentPinNeedsNoNetwork(t *testing.T) {
	entry := updateEntry(t, "v1.2.3")
	entry.UpdatePolicy = manifest.UpdatePolicy{
		Mode: manifest.UpdateModeSemver, Channel: manifest.UpdateChannelStable, PinnedTag: entry.Tag,
	}
	source := &recordingReleaseSource{}
	checked, err := New(source).Check(entry, "")
	if err != nil || checked.Status != StatusCurrent || checked.LatestTag != entry.Tag {
		t.Fatalf("checked=%+v error=%v", checked, err)
	}
	if source.totalCalls() != 0 {
		t.Fatalf("network calls=%d", source.totalCalls())
	}
}

func TestCheckerNormalizedEquivalentIsCurrentAndDowngradeFails(t *testing.T) {
	entry := updateEntry(t, "v1.2.3+old")
	entry.UpdatePolicy = manifest.DefaultUpdatePolicy()
	source := &recordingReleaseSource{releases: []ghrelease.Release{{TagName: "1.2.3+new"}}}
	checked, err := New(source).Check(entry, "")
	if err != nil || checked.Status != StatusCurrent || checked.LatestTag != "1.2.3+new" {
		t.Fatalf("checked=%+v error=%v", checked, err)
	}

	entry.Tag = "v2.0.0"
	source.releases = []ghrelease.Release{{TagName: "v1.9.9"}}
	checked, err = New(source).Check(entry, "")
	if !errors.Is(err, versionpolicy.ErrDowngrade) || checked.Status != StatusUnsupported {
		t.Fatalf("checked=%+v error=%v", checked, err)
	}
}

func TestCheckerLocalAndDriftAvoidEveryMetadataEndpoint(t *testing.T) {
	source := &recordingReleaseSource{}
	local := updateEntry(t, "local")
	local.Repo = ""
	if checked, err := New(source).Check(local, ""); err != nil || checked.Status != StatusLocal {
		t.Fatalf("local checked=%+v error=%v", checked, err)
	}
	drift := updateEntry(t, "v1.0.0")
	if err := os.WriteFile(drift.Path, []byte("changed\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if checked, err := New(source).Check(drift, ""); err == nil || checked.Status != StatusDrifted {
		t.Fatalf("drift checked=%+v error=%v", checked, err)
	}
	if source.totalCalls() != 0 {
		t.Fatalf("network calls=%d", source.totalCalls())
	}
}
