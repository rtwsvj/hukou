package orchestrate

import (
	"reflect"
	"testing"
)

func TestComputeDiff_Classification(t *testing.T) {
	tests := []struct {
		name    string
		before  []SnapItem
		after   []SnapItem
		added   []string // names
		removed []string // names
		changed []struct {
			name    string
			reasons []string
		}
	}{
		{
			name:   "added binary",
			before: []SnapItem{{Name: "brew", Path: "/b/brew", Source: "brew", Version: "4.0"}},
			after: []SnapItem{
				{Name: "brew", Path: "/b/brew", Source: "brew", Version: "4.0"},
				{Name: "gh", Path: "/b/gh", Source: "brew", Version: "2.1"},
			},
			added: []string{"gh"},
		},
		{
			name: "removed binary",
			before: []SnapItem{
				{Name: "brew", Path: "/b/brew", Source: "brew", Version: "4.0"},
				{Name: "old", Path: "/b/old", Source: "npm", Version: "1.0"},
			},
			after:   []SnapItem{{Name: "brew", Path: "/b/brew", Source: "brew", Version: "4.0"}},
			removed: []string{"old"},
		},
		{
			name:   "version changed",
			before: []SnapItem{{Name: "tsc", Path: "/n/tsc", Source: "npm", Version: "5.3.0"}},
			after:  []SnapItem{{Name: "tsc", Path: "/n/tsc", Source: "npm", Version: "5.4.0"}},
			changed: []struct {
				name    string
				reasons []string
			}{{"tsc", []string{"version"}}},
		},
		{
			name:   "sha changed but version equal",
			before: []SnapItem{{Name: "tool", Path: "/x/tool", Source: "cargo", Version: "1.0", SHA256: "aaa"}},
			after:  []SnapItem{{Name: "tool", Path: "/x/tool", Source: "cargo", Version: "1.0", SHA256: "bbb"}},
			changed: []struct {
				name    string
				reasons []string
			}{{"tool", []string{"sha256"}}},
		},
		{
			name:   "version and sha both changed",
			before: []SnapItem{{Name: "tool", Path: "/x/tool", Source: "cargo", Version: "1.0", SHA256: "aaa"}},
			after:  []SnapItem{{Name: "tool", Path: "/x/tool", Source: "cargo", Version: "2.0", SHA256: "bbb"}},
			changed: []struct {
				name    string
				reasons []string
			}{{"tool", []string{"version", "sha256"}}},
		},
		{
			name:   "empty hash never fabricates a change",
			before: []SnapItem{{Name: "tool", Path: "/x/tool", Source: "cargo", Version: "1.0", SHA256: ""}},
			after:  []SnapItem{{Name: "tool", Path: "/x/tool", Source: "cargo", Version: "1.0", SHA256: "bbb"}},
			// version equal, one hash empty -> no change.
		},
		{
			name: "same name different path are distinct entries",
			before: []SnapItem{
				{Name: "python", Path: "/usr/bin/python", Source: "system", Version: "3.11"},
			},
			after: []SnapItem{
				{Name: "python", Path: "/usr/bin/python", Source: "system", Version: "3.11"},
				{Name: "python", Path: "/opt/bin/python", Source: "brew", Version: "3.12"},
			},
			added: []string{"python"}, // the /opt one is new
		},
		{
			name:   "no changes",
			before: []SnapItem{{Name: "brew", Path: "/b/brew", Source: "brew", Version: "4.0"}},
			after:  []SnapItem{{Name: "brew", Path: "/b/brew", Source: "brew", Version: "4.0"}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := ComputeDiff(tc.before, tc.after)

			gotAdded := names(d.Added)
			if !reflect.DeepEqual(gotAdded, tc.added) {
				t.Errorf("added = %v, want %v", gotAdded, tc.added)
			}
			gotRemoved := names(d.Removed)
			if !reflect.DeepEqual(gotRemoved, tc.removed) {
				t.Errorf("removed = %v, want %v", gotRemoved, tc.removed)
			}
			if len(d.Changed) != len(tc.changed) {
				t.Fatalf("changed count = %d (%+v), want %d", len(d.Changed), d.Changed, len(tc.changed))
			}
			for i, want := range tc.changed {
				if d.Changed[i].Name != want.name {
					t.Errorf("changed[%d].Name = %s, want %s", i, d.Changed[i].Name, want.name)
				}
				if !reflect.DeepEqual(d.Changed[i].Reasons, want.reasons) {
					t.Errorf("changed[%d].Reasons = %v, want %v", i, d.Changed[i].Reasons, want.reasons)
				}
			}
			if tc.name == "no changes" && !d.Empty() {
				t.Errorf("expected Empty() diff, got %+v", d)
			}
		})
	}
}

// TestComputeDiff_GroupingIsSortedBySource proves the diff is deterministically
// ordered by (source, name, path) so table and JSON output are stable.
func TestComputeDiff_GroupingIsSortedBySource(t *testing.T) {
	after := []SnapItem{
		{Name: "z", Path: "/z", Source: "npm"},
		{Name: "a", Path: "/a", Source: "brew"},
		{Name: "m", Path: "/m", Source: "brew"},
		{Name: "b", Path: "/b", Source: "cargo"},
	}
	d := ComputeDiff(nil, after)
	got := make([]string, 0, len(d.Added))
	for _, it := range d.Added {
		got = append(got, it.Source+"/"+it.Name)
	}
	want := []string{"brew/a", "brew/m", "cargo/b", "npm/z"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("added order = %v, want %v", got, want)
	}
}

func TestDowngradeSuggestion(t *testing.T) {
	tests := []struct {
		source, name, prev string
		want               string
	}{
		{"npm", "typescript", "5.3.0", "npm i -g typescript@5.3.0"},
		{"pnpm", "eslint", "9.0.0", "pnpm add -g eslint@9.0.0"},
		{"cargo", "ripgrep", "14.0.0", "cargo install ripgrep --version 14.0.0"},
		{"uv", "ruff", "0.5.0", "uv tool install ruff==0.5.0"},
		{"npm", "typescript", "", ""},   // no prior version -> no suggestion
		{"brew", "wget", "1.21", ""},    // no standard one-liner
		{"unknown", "thing", "1.0", ""}, // unknown source
	}
	for _, tc := range tests {
		if got := DowngradeSuggestion(tc.source, tc.name, tc.prev); got != tc.want {
			t.Errorf("DowngradeSuggestion(%q,%q,%q) = %q, want %q", tc.source, tc.name, tc.prev, got, tc.want)
		}
	}
}

func names(items []SnapItem) []string {
	if len(items) == 0 {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.Name)
	}
	return out
}
