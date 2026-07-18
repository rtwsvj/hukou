package orchestrate

import "sort"

// SnapItem is one binary in an inventory snapshot, reduced to the fields the
// diff keys on. It is derived from a scan Report row (Name/Path/Source/Version)
// plus a content hash captured at snapshot time so a content change is caught
// even when the version string is unchanged.
type SnapItem struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Source  string `json:"source"`
	Version string `json:"version"`
	SHA256  string `json:"sha256"`
}

// Change is a binary present in both snapshots whose version and/or content
// changed. BEFORE/AFTER carry the version strings; the SHA fields carry the
// content digests; Reasons records which comparison(s) fired.
type Change struct {
	Name          string   `json:"name"`
	Path          string   `json:"path"`
	Source        string   `json:"source"`
	BeforeVersion string   `json:"before_version"`
	AfterVersion  string   `json:"after_version"`
	BeforeSHA256  string   `json:"before_sha256"`
	AfterSHA256   string   `json:"after_sha256"`
	Reasons       []string `json:"reasons"` // any of: "version", "sha256"
}

// Diff is the classified delta between a pre and a post snapshot, keyed on
// (Name, Path). Each slice is sorted deterministically by (Source, Name, Path).
type Diff struct {
	Added   []SnapItem `json:"added"`
	Removed []SnapItem `json:"removed"`
	Changed []Change   `json:"changed"`
}

// Empty reports whether the diff recorded no additions, removals, or changes.
func (d Diff) Empty() bool {
	return len(d.Added) == 0 && len(d.Removed) == 0 && len(d.Changed) == 0
}

// snapKey is the diff identity for a binary: its name and the path it occupied
// on PATH. Two same-named binaries at different paths are distinct entries.
func snapKey(it SnapItem) [2]string { return [2]string{it.Name, it.Path} }

// ComputeDiff classifies the transition from before to after, keyed on
// (Name, Path):
//   - a key only in after is added;
//   - a key only in before is removed;
//   - a key in both is changed when its version differs, or when both content
//     hashes are present and differ (an empty hash means "unknown" and never
//     fabricates a change on its own).
func ComputeDiff(before, after []SnapItem) Diff {
	beforeByKey := make(map[[2]string]SnapItem, len(before))
	for _, it := range before {
		beforeByKey[snapKey(it)] = it
	}
	afterByKey := make(map[[2]string]SnapItem, len(after))
	for _, it := range after {
		afterByKey[snapKey(it)] = it
	}

	var d Diff
	for _, it := range after {
		b, ok := beforeByKey[snapKey(it)]
		if !ok {
			d.Added = append(d.Added, it)
			continue
		}
		if reasons := changeReasons(b, it); len(reasons) > 0 {
			d.Changed = append(d.Changed, Change{
				Name:          it.Name,
				Path:          it.Path,
				Source:        it.Source,
				BeforeVersion: b.Version,
				AfterVersion:  it.Version,
				BeforeSHA256:  b.SHA256,
				AfterSHA256:   it.SHA256,
				Reasons:       reasons,
			})
		}
	}
	for _, it := range before {
		if _, ok := afterByKey[snapKey(it)]; !ok {
			d.Removed = append(d.Removed, it)
		}
	}

	sortItems(d.Added)
	sortItems(d.Removed)
	sortChanges(d.Changed)
	return d
}

// changeReasons returns the non-empty set of reasons a shared entry counts as
// changed. Version is compared verbatim; content is compared only when both
// digests are known, so a machine where hashing failed never invents a change.
func changeReasons(before, after SnapItem) []string {
	var reasons []string
	if before.Version != after.Version {
		reasons = append(reasons, "version")
	}
	if before.SHA256 != "" && after.SHA256 != "" && before.SHA256 != after.SHA256 {
		reasons = append(reasons, "sha256")
	}
	return reasons
}

func sortItems(items []SnapItem) {
	sort.Slice(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if a.Source != b.Source {
			return a.Source < b.Source
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.Path < b.Path
	})
}

func sortChanges(changes []Change) {
	sort.Slice(changes, func(i, j int) bool {
		a, b := changes[i], changes[j]
		if a.Source != b.Source {
			return a.Source < b.Source
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.Path < b.Path
	})
}

// DowngradeSuggestion returns the manager's own downgrade command for a changed
// foreign entry, or "" when no standard one-liner exists for that source. hukou
// entries are handled by the caller with a real `hukou rollback`; this function
// deliberately covers only foreign package managers and only prints — it never
// runs anything. prevVersion is the recorded pre-upgrade version; an empty one
// yields no suggestion because the target version is unknown.
func DowngradeSuggestion(source, name, prevVersion string) string {
	if prevVersion == "" {
		return ""
	}
	switch source {
	case "npm":
		return "npm i -g " + name + "@" + prevVersion
	case "pnpm":
		return "pnpm add -g " + name + "@" + prevVersion
	case "yarn":
		return "yarn global add " + name + "@" + prevVersion
	case "cargo":
		return "cargo install " + name + " --version " + prevVersion
	case "uv":
		return "uv tool install " + name + "==" + prevVersion
	case "pip-user":
		return "pip install --user " + name + "==" + prevVersion
	case "go":
		return "go install <module>@" + prevVersion
	default:
		return ""
	}
}
