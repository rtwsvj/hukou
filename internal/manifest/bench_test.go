package manifest

import (
	"fmt"
	"slices"
	"testing"
)

// benchManifest builds a manifest with n uniquely named entries. Get only
// reads Name, so the entries are intentionally minimal. The index starts nil,
// exactly like a hand-constructed literal; each benchmark below sets the index
// state it wants to measure.
func benchManifest(n int) *Manifest {
	entries := make([]Entry, n)
	for i := range entries {
		entries[i] = Entry{Name: fmt.Sprintf("tool-%04d", i)}
	}
	return &Manifest{
		SchemaVersion: CurrentSchemaVersion,
		Entries:       entries,
	}
}

func benchNames(m *Manifest) []string {
	names := make([]string, len(m.Entries))
	for i := range m.Entries {
		names[i] = m.Entries[i].Name
	}
	return names
}

// linearGet reproduces the pre-index Get: one linear scan per lookup.
// Iterating every name this way is O(n^2), the batch cost hukou upgrade --all
// paid per invocation. Keeping it here lets one `go test -bench` run report
// the O(n^2) baseline next to the indexed results.
func linearGet(m *Manifest, name string) *Entry {
	idx := slices.IndexFunc(m.Entries, func(e Entry) bool { return e.Name == name })
	if idx < 0 {
		return nil
	}
	return &m.Entries[idx]
}

func batchGet(b *testing.B, m *Manifest, names []string) {
	b.Helper()
	for _, name := range names {
		if m.Get(name) == nil {
			b.Fatalf("missing %s", name)
		}
	}
}

// BenchmarkManifestBatchGetLinear measures the previous O(n^2) batch behavior:
// resolving every entry by a fresh linear scan (identical to Get on a manifest
// whose index has never been built).
func BenchmarkManifestBatchGetLinear(b *testing.B) {
	m := benchManifest(100)
	names := benchNames(m)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, name := range names {
			if linearGet(m, name) == nil {
				b.Fatalf("missing %s", name)
			}
		}
	}
}

// BenchmarkManifestBatchGetColdIndex measures the first-use cost: every
// iteration pays one eager index build (what Load/Decode/Clone do) plus the
// 100-entry Get batch. This is the cost a command pays on its first batch
// after loading the manifest.
func BenchmarkManifestBatchGetColdIndex(b *testing.B) {
	m := benchManifest(100)
	names := benchNames(m)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.index = nil
		m.reindex()
		batchGet(b, m, names)
	}
}

// BenchmarkManifestBatchGetHotIndex measures the steady state: the index was
// built once (at Load/Decode/Clone or by an earlier mutation) and every Get in
// the batch is a verified O(1) hit.
func BenchmarkManifestBatchGetHotIndex(b *testing.B) {
	m := benchManifest(100)
	m.reindex()
	names := benchNames(m)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		batchGet(b, m, names)
	}
}
