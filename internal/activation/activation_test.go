package activation_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/rtwsvj/hukou/internal/activation"
	"github.com/rtwsvj/hukou/internal/manifest"
)

const (
	t0 = "2026-07-14T00:00:00Z"
	t1 = "2026-07-14T01:00:00Z"
	t2 = "2026-07-14T02:00:00Z"
	t3 = "2026-07-14T03:00:00Z"
	t4 = "2026-07-14T04:00:00Z"
)

func digest(character string) string { return strings.Repeat(character, 64) }

func cloneEntry(entry manifest.Entry) manifest.Entry {
	clone := entry
	clone.Activations = append([]manifest.ActivationEvent(nil), entry.Activations...)
	return clone
}

func adoptedEntry(t *testing.T) manifest.Entry {
	t.Helper()
	entry := manifest.Entry{
		Name:         "tool",
		Tag:          "v1.0.0",
		SHA256:       digest("1"),
		UpdatePolicy: manifest.DefaultUpdatePolicy(),
	}
	if err := activation.RecordAdopt(&entry, "a", t0); err != nil {
		t.Fatalf("RecordAdopt: %v", err)
	}
	return entry
}

func TestRecordAdoptBuildsRoot(t *testing.T) {
	entry := adoptedEntry(t)
	if entry.ActiveActivationID != "a" || len(entry.Activations) != 1 {
		t.Fatalf("entry lineage=%+v", entry)
	}
	root := entry.Activations[0]
	if root.ParentID != "" || root.Operation != activation.OperationAdopt {
		t.Fatalf("root=%+v", root)
	}
	if entry.AdoptedAt != t0 || entry.UpdatedAt != t0 {
		t.Fatalf("timestamps adopted=%q updated=%q", entry.AdoptedAt, entry.UpdatedAt)
	}
	if err := activation.Validate(entry); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestRepeatedRollbackWalksLogicalParents(t *testing.T) {
	entry := adoptedEntry(t)
	if err := activation.RecordUpgrade(&entry, "b", "v2.0.0", digest("2"), t1); err != nil {
		t.Fatal(err)
	}
	if err := activation.RecordUpgrade(&entry, "c", "v3.0.0", digest("3"), t2); err != nil {
		t.Fatal(err)
	}

	previous, err := activation.Previous(entry)
	if err != nil || previous.ID != "b" {
		t.Fatalf("Previous after C = %+v, %v", previous, err)
	}
	if err := activation.RecordRollback(&entry, "d", previous.ID, t3); err != nil {
		t.Fatal(err)
	}
	if entry.Tag != "v2.0.0" || entry.ActiveActivationID != "d" {
		t.Fatalf("first rollback entry=%+v", entry)
	}
	if got := entry.Activations[len(entry.Activations)-1]; got.ParentID != "a" || got.RevertsID != "c" {
		t.Fatalf("first rollback event=%+v", got)
	}

	previous, err = activation.Previous(entry)
	if err != nil || previous.ID != "a" {
		t.Fatalf("Previous after rollback to B = %+v, %v", previous, err)
	}
	if err := activation.RecordRollback(&entry, "e", previous.ID, t4); err != nil {
		t.Fatal(err)
	}
	if entry.Tag != "v1.0.0" || entry.ActiveActivationID != "e" {
		t.Fatalf("second rollback entry=%+v", entry)
	}
	if _, err := activation.Previous(entry); !errors.Is(err, activation.ErrNoPreviousActivation) {
		t.Fatalf("Previous at root error=%v", err)
	}
}

func TestRecordRestoreOriginalTerminatesUnknownLegacyLineage(t *testing.T) {
	entry := adoptedEntry(t)
	if err := activation.RecordUpgrade(&entry, "b", "v2.0.0", digest("2"), t1); err != nil {
		t.Fatal(err)
	}
	if err := activation.RecordRestoreOriginal(&entry, "original", digest("1"), t2); err != nil {
		t.Fatal(err)
	}
	if entry.Tag != "original" || entry.SHA256 != digest("1") {
		t.Fatalf("entry=%+v", entry)
	}
	if err := activation.Validate(entry); err != nil {
		t.Fatal(err)
	}
	if _, err := activation.Previous(entry); !errors.Is(err, activation.ErrNoPreviousActivation) {
		t.Fatalf("Previous error=%v", err)
	}
}

func TestFindAncestorByTagUsesNearestLogicalAncestor(t *testing.T) {
	entry := adoptedEntry(t)
	if err := activation.RecordUpgrade(&entry, "b", "v2.0.0", digest("2"), t1); err != nil {
		t.Fatal(err)
	}
	if err := activation.RecordUpgrade(&entry, "c", "v1.0.0", digest("1"), t2); err != nil {
		t.Fatal(err)
	}
	if err := activation.RecordUpgrade(&entry, "d", "v3.0.0", digest("4"), t3); err != nil {
		t.Fatal(err)
	}
	target, err := activation.FindAncestorByTag(entry, "v1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if target.ID != "c" {
		t.Fatalf("target=%+v, want event c", target)
	}
}

func TestAncestorsRespectsLimit(t *testing.T) {
	entry := adoptedEntry(t)
	if err := activation.RecordUpgrade(&entry, "b", "v2", digest("2"), t1); err != nil {
		t.Fatal(err)
	}
	if err := activation.RecordUpgrade(&entry, "c", "v3", digest("3"), t2); err != nil {
		t.Fatal(err)
	}
	ancestors, err := activation.Ancestors(entry, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(ancestors) != 1 || ancestors[0].ID != "b" {
		t.Fatalf("ancestors=%+v", ancestors)
	}
	ancestors, err = activation.Ancestors(entry, 0)
	if err != nil || len(ancestors) != 0 {
		t.Fatalf("zero ancestors=%+v err=%v", ancestors, err)
	}
}

func TestValidateFailsClosedOnBrokenLineage(t *testing.T) {
	tests := map[string]func(*manifest.Entry){
		"active mismatch": func(entry *manifest.Entry) { entry.Tag = "v9" },
		"missing parent": func(entry *manifest.Entry) {
			entry.Activations[0].ParentID = "missing"
		},
		"duplicate id": func(entry *manifest.Entry) {
			entry.Activations = append(entry.Activations, entry.Activations[0])
			entry.ActiveActivationID = entry.Activations[1].ID
		},
		"bad digest": func(entry *manifest.Entry) {
			entry.SHA256 = "bad"
			entry.Activations[0].SHA256 = "bad"
		},
	}
	for name, corrupt := range tests {
		t.Run(name, func(t *testing.T) {
			entry := adoptedEntry(t)
			corrupt(&entry)
			if err := activation.Validate(entry); err == nil {
				t.Fatal("Validate accepted corrupt lineage")
			}
		})
	}
}

func TestRecordRollbackRejectsNonAncestor(t *testing.T) {
	entry := adoptedEntry(t)
	if err := activation.RecordUpgrade(&entry, "b", "v2", digest("2"), t1); err != nil {
		t.Fatal(err)
	}
	if err := activation.RecordRollback(&entry, "c", "not-an-ancestor", t2); err == nil {
		t.Fatal("RecordRollback accepted a non-ancestor")
	}
	if entry.Tag != "v2" || entry.ActiveActivationID != "b" || len(entry.Activations) != 2 {
		t.Fatalf("entry changed after rejected rollback: %+v", entry)
	}
}

func TestValidateRejectsUnsafeHistoricalActivationTags(t *testing.T) {
	unsafeTags := []string{"", ".", "..", "v1..0", "../escape", "release/v1", `release\v1`, "Original"}
	for _, position := range []string{"inactive", "active"} {
		for _, tag := range unsafeTags {
			t.Run(position+"/"+tag, func(t *testing.T) {
				entry := adoptedEntry(t)
				if err := activation.RecordUpgrade(&entry, "b", "v2.0.0", digest("2"), t1); err != nil {
					t.Fatal(err)
				}
				if position == "inactive" {
					entry.Activations[0].Tag = tag
				} else {
					entry.Activations[1].Tag = tag
					entry.Tag = tag
				}
				if err := activation.Validate(entry); err == nil {
					t.Fatal("Validate accepted an unsafe historical activation tag")
				}
			})
		}
	}
}

func TestRecordAPIsRejectUnsafeTagsWithoutMutation(t *testing.T) {
	unsafeTags := []string{"", ".", "..", "v1..0", "../escape", "release/v1", `release\v1`, "Original"}
	for _, tag := range unsafeTags {
		t.Run("adopt/"+tag, func(t *testing.T) {
			entry := manifest.Entry{Tag: tag, SHA256: digest("1"), UpdatePolicy: manifest.DefaultUpdatePolicy()}
			before := cloneEntry(entry)
			if err := activation.RecordAdopt(&entry, "a", t0); err == nil {
				t.Fatal("RecordAdopt accepted an unsafe tag")
			}
			if !reflect.DeepEqual(entry, before) {
				t.Fatalf("RecordAdopt mutated entry after rejection: before=%+v after=%+v", before, entry)
			}
		})
		t.Run("upgrade/"+tag, func(t *testing.T) {
			entry := adoptedEntry(t)
			before := cloneEntry(entry)
			if err := activation.RecordUpgrade(&entry, "b", tag, digest("2"), t1); err == nil {
				t.Fatal("RecordUpgrade accepted an unsafe tag")
			}
			if !reflect.DeepEqual(entry, before) {
				t.Fatalf("RecordUpgrade mutated entry after rejection: before=%+v after=%+v", before, entry)
			}
		})
	}
}

func TestRecordAdoptAcceptsStoreSafeTagForms(t *testing.T) {
	for _, tag := range []string{"local", "v1.2.3-rc.1+build.7", "legacy-build_42"} {
		t.Run(tag, func(t *testing.T) {
			entry := manifest.Entry{Tag: tag, SHA256: digest("1"), UpdatePolicy: manifest.DefaultUpdatePolicy()}
			if err := activation.RecordAdopt(&entry, "a", t0); err != nil {
				t.Fatalf("RecordAdopt rejected safe tag %q: %v", tag, err)
			}
			if err := activation.Validate(entry); err != nil {
				t.Fatalf("Validate rejected safe tag %q: %v", tag, err)
			}
		})
	}
}

func TestRecordUpgradeRejectsTagDigestRebindingWithoutMutation(t *testing.T) {
	entry := adoptedEntry(t)
	before := cloneEntry(entry)
	if err := activation.RecordUpgrade(&entry, "b", "v1.0.0", digest("2"), t1); err == nil {
		t.Fatal("RecordUpgrade accepted a different digest for an existing tag")
	}
	if !reflect.DeepEqual(entry, before) {
		t.Fatalf("entry changed after rejected tag rebinding: before=%+v after=%+v", before, entry)
	}

	if err := activation.RecordUpgrade(&entry, "b", "v1.0.0", strings.ToUpper(digest("1")), t1); err != nil {
		t.Fatalf("RecordUpgrade rejected the same digest with different hex case: %v", err)
	}
	if err := activation.Validate(entry); err != nil {
		t.Fatalf("Validate rejected same tag/SHA binding: %v", err)
	}
}

func TestRecordRestoreOriginalRejectsTagDigestRebindingWithoutMutation(t *testing.T) {
	entry := adoptedEntry(t)
	if err := activation.RecordRestoreOriginal(&entry, "original-1", digest("2"), t1); err != nil {
		t.Fatal(err)
	}
	if err := activation.RecordUpgrade(&entry, "b", "v2.0.0", digest("3"), t2); err != nil {
		t.Fatal(err)
	}
	before := cloneEntry(entry)
	if err := activation.RecordRestoreOriginal(&entry, "original-2", digest("4"), t3); err == nil {
		t.Fatal("RecordRestoreOriginal accepted a different digest for original")
	}
	if !reflect.DeepEqual(entry, before) {
		t.Fatalf("entry changed after rejected original rebinding: before=%+v after=%+v", before, entry)
	}
}

func TestValidateRejectsTagDigestRebinding(t *testing.T) {
	for _, tag := range []string{"v1.0.0", "original"} {
		t.Run(tag, func(t *testing.T) {
			entry := adoptedEntry(t)
			if err := activation.RecordUpgrade(&entry, "b", "v2.0.0", digest("2"), t1); err != nil {
				t.Fatal(err)
			}
			entry.Activations[0].Tag = tag
			entry.Activations[1].Tag = tag
			entry.Tag = tag
			if err := activation.Validate(entry); err == nil {
				t.Fatal("Validate accepted one tag bound to different digests")
			}
		})
	}
}

func TestNewIDProducesValidDistinctIDs(t *testing.T) {
	first, err := activation.NewID()
	if err != nil {
		t.Fatal(err)
	}
	second, err := activation.NewID()
	if err != nil {
		t.Fatal(err)
	}
	if first == second || !strings.HasPrefix(first, "act-") || !strings.HasPrefix(second, "act-") {
		t.Fatalf("ids=%q %q", first, second)
	}
}
