package updatecheck

import (
	"errors"
	"fmt"
	"runtime"
	"strings"

	"github.com/rtwsvj/hukou/internal/assetpick"
	"github.com/rtwsvj/hukou/internal/ghrelease"
	"github.com/rtwsvj/hukou/internal/manifest"
	"github.com/rtwsvj/hukou/internal/store"
	"github.com/rtwsvj/hukou/internal/versionpolicy"
)

type Status string

const (
	StatusCurrent     Status = "current"
	StatusOutdated    Status = "outdated"
	StatusLocal       Status = "local"
	StatusDrifted     Status = "drifted"
	StatusUnavailable Status = "unavailable"
	StatusUnsupported Status = "unsupported"
)

// ReleaseSource is the minimal GitHub metadata dependency used by update
// planning. It intentionally excludes asset download methods.
type ReleaseSource interface {
	Latest(owner, repo string) (ghrelease.Release, error)
}

// ReleaseLister is implemented by metadata clients that can enumerate a
// repository's release candidates. Production semantic-version and
// prerelease selection requires this capability. Checker retains a Latest
// fallback for narrow legacy test doubles and existing internal callers.
type ReleaseLister interface {
	List(owner, repo string) ([]ghrelease.Release, error)
}

// ReleaseByTagSource is the exact metadata lookup required by a pinned policy.
// A pin is never resolved by scanning a release list or by accepting latest.
type ReleaseByTagSource interface {
	ByTag(owner, repo, tag string) (ghrelease.Release, error)
}

type Result struct {
	Name       string `json:"name"`
	Repo       string `json:"repo,omitempty"`
	CurrentTag string `json:"current_tag"`
	LatestTag  string `json:"latest_tag,omitempty"`
	Asset      string `json:"asset,omitempty"`
	Status     Status `json:"status"`
	Note       string `json:"note,omitempty"`
	Error      string `json:"error,omitempty"`
}

// Checked retains release metadata for the real upgrade path while exposing a
// stable Result for dry-run and outdated reports.
type Checked struct {
	Result
	Release ghrelease.Release `json:"-"`
}

type Checker struct {
	Releases ReleaseSource
	GOOS     string
	GOARCH   string
}

func New(releases ReleaseSource) Checker {
	return Checker{Releases: releases, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
}

func (c Checker) Check(entry manifest.Entry, assetFilter string) (Checked, error) {
	checked := Checked{Result: Result{
		Name:       entry.Name,
		Repo:       entry.Repo,
		CurrentTag: entry.Tag,
	}}
	if entry.Repo == "" || entry.Tag == "local" {
		checked.Status = StatusLocal
		checked.Note = "local entries have no release source"
		return checked, nil
	}

	currentSHA, err := store.SHA256File(entry.Path)
	if err != nil {
		checked.Status = StatusUnavailable
		checked.Error = fmt.Sprintf("read current file: %v", err)
		return checked, fmt.Errorf("read current file: %w", err)
	}
	if currentSHA != entry.SHA256 {
		checked.Status = StatusDrifted
		checked.Error = "current file sha256 does not match manifest"
		return checked, fmt.Errorf("current file sha256 does not match manifest (possible external modification)")
	}

	owner, repo, ok := splitRepo(entry.Repo)
	if !ok {
		checked.Status = StatusUnsupported
		checked.Error = fmt.Sprintf("invalid repo %q", entry.Repo)
		return checked, fmt.Errorf("invalid repo %q", entry.Repo)
	}
	if c.Releases == nil {
		checked.Status = StatusUnavailable
		checked.Error = "release source is not configured"
		return checked, fmt.Errorf("release source is not configured")
	}
	release, decision, err := c.selectRelease(entry, owner, repo)
	if err != nil {
		checked.Status = releaseSelectionErrorStatus(err)
		checked.Error = err.Error()
		return checked, err
	}
	checked.Release = release
	checked.LatestTag = decision.CandidateTag
	checked.Note = decision.Reason
	if decision.Action == versionpolicy.ActionNone {
		checked.Status = StatusCurrent
		return checked, nil
	}
	if err := store.ValidateTag(decision.CandidateTag); err != nil {
		checked.Status = StatusUnsupported
		checked.Error = fmt.Sprintf("selected release tag %q cannot be stored: %v", decision.CandidateTag, err)
		return checked, fmt.Errorf("selected release tag %q cannot be stored: %w", decision.CandidateTag, err)
	}

	assetNames := make([]string, len(release.Assets))
	for i, asset := range release.Assets {
		assetNames[i] = asset.Name
	}
	goos, goarch := c.GOOS, c.GOARCH
	if goos == "" {
		goos = runtime.GOOS
	}
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	chosen, note, err := assetpick.Pick(assetNames, goos, goarch, assetFilter)
	if err != nil {
		checked.Status = StatusUnsupported
		checked.Error = err.Error()
		return checked, err
	}
	var selected *ghrelease.Asset
	for i := range release.Assets {
		if release.Assets[i].Name == chosen {
			selected = &release.Assets[i]
			break
		}
	}
	if selected == nil || selected.BrowserDownloadURL == "" || selected.Size < 0 {
		checked.Status = StatusUnsupported
		checked.Error = fmt.Sprintf("selected asset %q has incomplete or invalid download metadata", chosen)
		return checked, errors.New(checked.Error)
	}
	checked.Status = StatusOutdated
	checked.Asset = chosen
	if note != "" {
		if checked.Note != "" {
			checked.Note += "; "
		}
		checked.Note += note
	}
	return checked, nil
}

func (c Checker) selectRelease(entry manifest.Entry, owner, repo string) (ghrelease.Release, versionpolicy.Decision, error) {
	policy, err := versionpolicy.Normalize(entry.UpdatePolicy)
	if err != nil {
		return ghrelease.Release{}, versionpolicy.Decision{}, err
	}
	if versionpolicy.PinnedCurrent(policy, entry.Tag) {
		decision, err := versionpolicy.Select(policy, entry.Tag, nil)
		return ghrelease.Release{TagName: entry.Tag}, decision, err
	}
	if policy.PinnedTag != "" {
		byTag, ok := c.Releases.(ReleaseByTagSource)
		if !ok {
			return ghrelease.Release{}, versionpolicy.Decision{}, fmt.Errorf("release source does not support exact tag lookup required by pin %q", policy.PinnedTag)
		}
		release, err := byTag.ByTag(owner, repo, policy.PinnedTag)
		if err != nil {
			return ghrelease.Release{}, versionpolicy.Decision{}, err
		}
		decision, err := versionpolicy.Select(policy, entry.Tag, []versionpolicy.Release{policyRelease(release)})
		return release, decision, err
	}

	// github-latest/stable is deliberately the v0.2 compatibility path. All
	// semantic or prerelease policies need a candidate list so policy selection
	// remains independent of GitHub's latest-release heuristics.
	if policy.Mode == manifest.UpdateModeLegacy && policy.Channel == manifest.UpdateChannelStable {
		release, err := c.Releases.Latest(owner, repo)
		if err != nil {
			return ghrelease.Release{}, versionpolicy.Decision{}, err
		}
		decision, err := versionpolicy.Select(policy, entry.Tag, []versionpolicy.Release{policyRelease(release)})
		return release, decision, err
	}

	releases, err := c.listReleases(owner, repo)
	if err != nil {
		return ghrelease.Release{}, versionpolicy.Decision{}, err
	}
	candidates := make([]versionpolicy.Release, len(releases))
	for i, release := range releases {
		candidates[i] = policyRelease(release)
	}
	decision, err := versionpolicy.Select(policy, entry.Tag, candidates)
	if err != nil {
		return ghrelease.Release{}, versionpolicy.Decision{}, err
	}
	for _, release := range releases {
		if release.TagName == decision.CandidateTag {
			return release, decision, nil
		}
	}
	return ghrelease.Release{}, versionpolicy.Decision{}, fmt.Errorf("selected release %q is missing from metadata response", decision.CandidateTag)
}

func (c Checker) listReleases(owner, repo string) ([]ghrelease.Release, error) {
	if lister, ok := c.Releases.(ReleaseLister); ok {
		return lister.List(owner, repo)
	}
	// Compatibility for the original narrow ReleaseSource contract. Production
	// ghrelease.Client implements ReleaseLister and never takes this path.
	release, err := c.Releases.Latest(owner, repo)
	if err != nil {
		return nil, err
	}
	return []ghrelease.Release{release}, nil
}

func policyRelease(release ghrelease.Release) versionpolicy.Release {
	return versionpolicy.Release{
		Tag:        release.TagName,
		Draft:      release.Draft,
		Prerelease: release.Prerelease,
	}
}

func releaseSelectionErrorStatus(err error) Status {
	for _, policyErr := range []error{
		versionpolicy.ErrInvalidPolicy,
		versionpolicy.ErrNoCandidate,
		versionpolicy.ErrPinnedReleaseNotFound,
		versionpolicy.ErrCurrentNotSemver,
		versionpolicy.ErrDowngrade,
	} {
		if errors.Is(err, policyErr) {
			return StatusUnsupported
		}
	}
	return StatusUnavailable
}

func splitRepo(value string) (owner, repo string, ok bool) {
	parts := strings.Split(value, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}
