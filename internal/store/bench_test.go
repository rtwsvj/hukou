package store

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// benchStoreVersionHashPasses models the store-and-activate segment of a single
// tool upgrade: the store copies the extracted binary into an immutable version
// directory, then the caller needs that artifact's SHA-256 to publish the
// activation. The redundant variant hashes the immutable store file a second
// time (the pre-refactor cmd/upgrade.go behavior); the deduped variant reuses
// the digest PutWithDigest already computed and cross-checked. A multi-megabyte
// artifact makes each avoided whole-file pass a clear cost. The reported
// SHA256File/op metric is the measured count of whole-file passes the segment
// performs (excluding the necessary edge-hash the copy itself computes).
func benchStoreVersionHashPasses(b *testing.B, dedup bool) {
	data := bytes.Repeat([]byte("hukou-card-c-benchmark-payload\n"), 150000) // ~4.5 MiB

	b.ResetTimer()
	start := sha256FileCalls.Load()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		s := &Store{Root: b.TempDir()}
		src := filepath.Join(b.TempDir(), "tool")
		if err := os.WriteFile(src, data, 0o755); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()

		digest, err := s.PutWithDigest("tool", "v1", src)
		if err != nil {
			b.Fatal(err)
		}
		targetSource, err := s.ActivationSource("tool", "v1")
		if err != nil {
			b.Fatal(err)
		}
		var targetSHA string
		if dedup {
			targetSHA = digest
		} else {
			targetSHA, err = SHA256File(targetSource)
			if err != nil {
				b.Fatal(err)
			}
		}
		if targetSHA != digest {
			b.Fatalf("activation digest mismatch: got %s want %s", targetSHA, digest)
		}
	}
	b.StopTimer()

	passes := sha256FileCalls.Load() - start
	b.ReportMetric(float64(passes)/float64(b.N), "SHA256File/op")
}

// BenchmarkStoreVersionActivateRedundant reports the pre-refactor cost: the new
// store artifact is hashed once by the store and again by the caller.
func BenchmarkStoreVersionActivateRedundant(b *testing.B) {
	benchStoreVersionHashPasses(b, false)
}

// BenchmarkStoreVersionActivateDeduped reports the post-refactor cost: the
// caller reuses PutWithDigest's returned digest, removing one whole-file pass.
func BenchmarkStoreVersionActivateDeduped(b *testing.B) {
	benchStoreVersionHashPasses(b, true)
}
