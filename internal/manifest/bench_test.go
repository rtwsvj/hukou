package manifest_test

import (
	"fmt"
	"testing"

	"github.com/rtwsvj/hukou/internal/manifest"
)

// benchManifest builds a manifest with n uniquely named entries. The
// benchmarks below only exercise Get/Put, so the entries are minimal.
func benchManifest(n int) *manifest.Manifest {
	entries := make([]manifest.Entry, n)
	for i := range entries {
		entries[i] = manifest.Entry{Name: fmt.Sprintf("tool-%04d", i)}
	}
	return &manifest.Manifest{
		SchemaVersion: manifest.CurrentSchemaVersion,
		Entries:       entries,
	}
}

// BenchmarkUpgradeBatchManifestOpsInLoopGet measures the manifest-operation
// cost of the previous `upgrade --all` loop shape over 100 targets: every
// iteration re-resolved its entry with m.Get (a linear scan) before upgrading,
// then wrote the result back with m.Put (another linear scan).
func BenchmarkUpgradeBatchManifestOpsInLoopGet(b *testing.B) {
	m := benchManifest(100)
	targets := append([]manifest.Entry(nil), m.Entries...)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, e := range targets {
			entry := e
			if live := m.Get(e.Name); live != nil {
				entry = *live
			}
			m.Put(entry)
		}
	}
}

// BenchmarkUpgradeBatchManifestOpsSnapshot measures the current loop shape:
// the batch iterates a snapshot of Entries taken once up front, holds each
// entry copy directly (no in-loop Get), and still writes back with m.Put.
// The remaining Put is a single linear scan per upgraded tool; no constant-
// time lookup structure is involved.
func BenchmarkUpgradeBatchManifestOpsSnapshot(b *testing.B) {
	m := benchManifest(100)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		targets := append([]manifest.Entry(nil), m.Entries...)
		for _, e := range targets {
			entry := e
			m.Put(entry)
		}
	}
}
