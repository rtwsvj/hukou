package manifest_test

import (
	"fmt"
	"sync"
	"testing"

	"github.com/rtwsvj/hukou/internal/manifest"
)

func lookupFixture(t *testing.T, n int) *manifest.Manifest {
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
		m.Put(entry)
	}
	return m
}

// TestGetIsSafeForConcurrentReaders drives many goroutines through Get (hits
// and misses) with no synchronization. Get is a pure linear scan that performs
// no writes, so `go test -race` must pass.
func TestGetIsSafeForConcurrentReaders(t *testing.T) {
	m := lookupFixture(t, 50)
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
				if m.Get(name) == nil {
					t.Errorf("Get(%s) = nil", name)
					return
				}
				if m.Get("no-such-tool") != nil {
					t.Error("Get(no-such-tool) != nil")
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

// TestPutGetRemoveFirstOccurrenceSemantics pins the tie-break behavior for a
// transiently duplicated name (invalid per Validate, but observable before it
// runs): Get, Put, and Remove must all address the first occurrence.
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
	if m.Entries[0].Name != "other" || m.Entries[1].Path != "/second" {
		t.Fatalf("Remove must delete the first occurrence: %+v", m.Entries)
	}
}
