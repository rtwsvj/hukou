package manifest_test

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/rtwsvj/hukou/internal/manifest"
)

func indexedFixture(t *testing.T, n int) *manifest.Manifest {
	t.Helper()
	m := &manifest.Manifest{
		SchemaVersion: manifest.CurrentSchemaVersion,
		Retention:     manifest.DefaultRetentionPolicy(),
		Entries:       make([]manifest.Entry, 0, n),
	}
	for i := 0; i < n; i++ {
		entry := manifest.PrepareEntry(fixtureEntry())
		entry.Name = fmt.Sprintf("tool-%03d", i)
		entry.Path = fmt.Sprintf("/usr/local/bin/tool-%03d", i)
		// Put maintains the internal index synchronously, so the returned
		// manifest is in the same state as one produced by Load/Decode.
		m.Put(entry)
	}
	return m
}

// TestGetIsSafeForConcurrentReaders drives many goroutines through Get
// (hits and misses) on both an indexed manifest and a literal-constructed
// manifest whose index was never built. Get must never write manifest state,
// so `go test -race` passes without any synchronization by the callers.
func TestGetIsSafeForConcurrentReaders(t *testing.T) {
	indexed := indexedFixture(t, 50)
	literal := &manifest.Manifest{
		SchemaVersion: manifest.CurrentSchemaVersion,
		Entries: []manifest.Entry{
			{Name: "alpha"}, {Name: "beta"}, {Name: "gamma"},
		},
	}

	const goroutines = 8
	const lookups = 2000
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			for i := 0; i < lookups; i++ {
				name := fmt.Sprintf("tool-%03d", (seed+i)%50)
				if indexed.Get(name) == nil {
					t.Errorf("indexed Get(%s) = nil", name)
					return
				}
				if indexed.Get("no-such-tool") != nil {
					t.Error("indexed Get(no-such-tool) != nil")
					return
				}
				if literal.Get("beta") == nil || literal.Get("missing") != nil {
					t.Error("literal manifest lookup mismatch")
					return
				}
			}
		}(g)
	}
	wg.Wait()
}

// TestGetSurvivesDirectEntriesMutation drifts the exported Entries slice
// behind the index's back and proves every lookup still answers from the
// authoritative slice: index hits are verified before use and misses fall
// back to a linear scan, so no stale or wrong entry can ever be returned.
func TestGetSurvivesDirectEntriesMutation(t *testing.T) {
	m := indexedFixture(t, 5)

	// External append: the index has no idea, the fallback scan must find it.
	m.Entries = append(m.Entries, manifest.Entry{Name: "external-append"})
	if got := m.Get("external-append"); got == nil || got.Name != "external-append" {
		t.Fatalf("externally appended entry not found: %+v", got)
	}

	// External in-place rename: the stale index position no longer matches, so
	// the old name must miss and the new name must be found by fallback.
	m.Entries[0].Name = "renamed-tool"
	if got := m.Get("tool-000"); got != nil {
		t.Fatalf("stale index position returned for renamed entry: %+v", got)
	}
	if got := m.Get("renamed-tool"); got == nil || got.Name != "renamed-tool" {
		t.Fatalf("renamed entry not found: %+v", got)
	}

	// External truncation: indexed positions beyond the new length must never
	// be dereferenced.
	m.Entries = m.Entries[:2]
	if got := m.Get("tool-004"); got != nil {
		t.Fatalf("truncated entry still returned: %+v", got)
	}
	if got := m.Get("tool-001"); got == nil || got.Name != "tool-001" {
		t.Fatalf("surviving entry lost after truncation: %+v", got)
	}

	// Mutating methods resynchronize the index from the drifted slice.
	m.Put(manifest.Entry{Name: "after-drift"})
	if got := m.Get("after-drift"); got == nil {
		t.Fatal("entry added after drift not found")
	}
	if !m.Remove("renamed-tool") {
		t.Fatal("failed to remove renamed entry after drift")
	}
	if m.Get("renamed-tool") != nil {
		t.Fatal("removed entry still visible")
	}
}

// TestPutGetRemoveFirstOccurrenceSemantics pins the tie-break behavior for a
// transiently duplicated name (invalid per Validate, but observable before it
// runs): Get, Put, and Remove must all address the first occurrence, exactly
// like the linear scans they replaced.
func TestPutGetRemoveFirstOccurrenceSemantics(t *testing.T) {
	m := &manifest.Manifest{
		SchemaVersion: manifest.CurrentSchemaVersion,
		Entries: []manifest.Entry{
			{Name: "dup", Path: "/first"},
			{Name: "other"},
			{Name: "dup", Path: "/second"},
		},
	}
	if got := m.Get("dup"); got == nil || got.Path != "/first" {
		t.Fatalf("Get(dup) = %+v, want first occurrence", got)
	}
	m.Put(manifest.Entry{Name: "dup", Path: "/replaced"})
	if m.Entries[0].Path != "/replaced" || m.Entries[2].Path != "/second" {
		t.Fatalf("Put must replace the first occurrence: %+v", m.Entries)
	}
	if !m.Remove("dup") {
		t.Fatal("Remove(dup) = false")
	}
	if m.Entries[0].Name != "other" || !strings.HasPrefix(m.Entries[1].Path, "/second") {
		t.Fatalf("Remove must delete the first occurrence: %+v", m.Entries)
	}
}
