# Card C — Hash hot-path dedup + eager manifest index

Branch: `card-c-20260715`. Scope: remove redundant *same-boundary* re-hashing
of the same byte stream in the upgrade/adopt pipeline, and accelerate manifest
lookups. Every cross-trust-boundary verification hash (pre-activation live
recheck, publisher-checksum verification, journal payload capture/re-verify,
pre-destructive prune verification) is preserved. No third-party deps added;
`gobin.go`/`detect.go` untouched.

Reworked after independent Codex (xhigh) review; the five review items and
their resolutions are inlined below.

## Change list

| File | Change |
|---|---|
| `internal/store/store.go:235` | New `PutWithDigest(name, tag, src) (string, error)` returns the content SHA-256 the store already computes (copy MultiWriter, `:315`) and cross-checks against a fresh source read (`:319`). `Put` (`:224`) is a thin wrapper — no existing caller churn. Digest returned on the fresh-copy path (`:366`) and on the idempotent existing-version path (`:285`, computed from the destination artifact). |
| `internal/store/store.go:23` | Unexported `sha256FileCalls atomic.Uint64`, incremented in `SHA256File`. Diagnostics/benchmark only; no production decision reads it. |
| `internal/verify/verify.go:161,183` | `VerifyAsset` keeps its **original error priority** (review item 1): missing checksum entry → `ErrNoChecksum` and invalid published digest are both decided *before* the asset file is opened; only then can a read error or mismatch surface. New `VerifyAssetDigest` (`:183`) performs the same map-level contract against a caller-supplied digest. |
| `cmd/upgrade.go:209,216` | Download asset hashed **once**; the digest both records `asset_sha256` and feeds `verify.VerifyAssetDigest`. Was: `VerifyAsset` (full read) + `SHA256File` (full read). |
| `cmd/upgrade.go:251,322` | `PutWithDigest` return reused as `targetSHA`; removed the second full read of the just-stored artifact. The journal still captures `targetSource` independently and `validateTransactionStateSHA(tx,"live",targetSHA,true)` re-checks that capture — see the tamper test below. |
| `internal/manifest/manifest.go:120,903-981` | Eager, race-safe, drift-tolerant name→position index (review item 2; design below). |
| `internal/manifest/bench_test.go` | Batch-Get benchmarks split into linear baseline / cold index / hot index (review item 3). |
| `internal/store/bench_test.go` | Whole-file hash-pass benchmark for the store+activate segment (redundant vs deduped), reporting a measured `SHA256File/op` metric. |
| `internal/store/put_digest_test.go` | Adversarial digest tests: fresh path, idempotent existing path, conflicting-content rejection (review item 4a). |
| `cmd/upgrade_tamper_test.go` | Store artifact tampered after `PutWithDigest`/before journal capture → activation rejected, journal recovered clean, live path untouched (review item 4b). |
| `internal/manifest/index_test.go` | Concurrent-read race test (`-race`), direct-`Entries`-mutation drift tests, first-occurrence tie-break pinning (review items 2/4c). |
| `internal/verify/verify_contract_test.go` | Error-priority contract tests: missing-file × {missing, invalid, valid} checksum-entry combinations with sentinel/class assertions (review item 1). |

## Manifest index design (review item 2)

`internal/manifest/manifest.go`:

- **Eager, not lazy** (`:120` field; `buildIndex :903`; `reindex :916`). The
  index is built by `Load` (`:167` empty branch), `Decode` (`:200`, after
  Normalize+Validate), and `Clone` (`:673`), and rebuilt synchronously by the
  mutating methods `Put` (`:967`) and `Remove` (`:980`). **`Get` never writes
  manifest state**, so concurrent read-only lookups are lock-free and
  race-free — verified by `TestGetIsSafeForConcurrentReaders` under `-race`.
  The previous lazy build-on-first-Get was a data race and is gone.
- **Advisory, never authoritative** (`indexedHit :925`, `locate :935`). Every
  index hit is re-verified against the live `Entries` slice
  (`Entries[idx].Name == name`, bounds-checked); any miss falls back to the
  original `slices.IndexFunc` linear scan. External code that manipulates the
  exported `Entries` slice directly (several cmd call sites do, and tests
  build literals) therefore degrades to exactly the pre-index behavior —
  stale positions can never be dereferenced or returned. Pinned by
  `TestGetSurvivesDirectEntriesMutation` (append / in-place rename / truncate)
  and `TestPutGetRemoveFirstOccurrenceSemantics`.
- **Why not unexported `Entries` + accessors:** `Entries` is a JSON-tagged
  serialized field. Unexporting it forces custom `MarshalJSON`/`UnmarshalJSON`
  on `Manifest`, which touches three load-bearing contracts at once: the
  strict envelope decoder (`DisallowUnknownFields`, schema-gated fields), the
  byte-for-byte identity between journal payload and `Save` output, and
  doctor's deliberate *lenient* `json.Unmarshal` first pass. That is a
  serialization-boundary rewrite, not an index fix; the verified-hit +
  authoritative-fallback design achieves the same safety (no stale result is
  ever observable) with zero serialization risk, so the entry-point-validation
  option was chosen deliberately.

### Complexity (corrected, review item 3)

| Operation | Before | After |
|---|---|---|
| `Get` (hit) | O(n) | **O(1)** (verified index hit) |
| `Get` (miss) | O(n) | O(n) (authoritative fallback scan; misses are rare in hot loops) |
| `Put` | O(n) | O(n) (authoritative locate + eager reindex) |
| `Remove` | O(n) | **O(n) — not O(1)** (slice shift + eager reindex) |
| Batch of n Get-hits (`upgrade --all` loop) | O(n²) | **O(n)** |

The earlier draft of this document claimed Remove was O(1); that was wrong and
is corrected above. The hot-path win is the batch Get-hit pattern.

## Decision table — every whole-file SHA-256 in one `hukou upgrade <tool>` pass

Streams: **A** = downloaded asset, **E/E′** = extracted new binary → store
version → new live, **L** = current live (old) → backup + original.
"edge-hash" = hash computed *while copying* (MultiWriter), not an extra read.

| # | Site | Stream | Purpose | Boundary | Decision |
|---|---|---|---|---|---|
| 1 | `updatecheck/check.go:87` | L | drift precheck vs manifest before network | cross-boundary | **KEEP** |
| 2 | `verify.VerifyAsset` internal hash (old call) | A | publisher-checksum verify | cross-boundary | **MERGED → #3** (verification kept, byte pass merged) |
| 3 | `cmd/upgrade.go:209` | A | record `asset_sha256`; feeds `VerifyAssetDigest :216` | cross-boundary | **KEEP** (single asset pass) |
| 4 | `store.go:315` MultiWriter (new binary Put) | E | copy into store | necessary copy | KEEP (edge-hash) |
| 5 | `store.go:319` source reread | E | TOCTOU: source stable during copy | fail-closed guard | **KEEP** |
| 6 | `cmd/upgrade.go:258` (`latestSHA`) | L | pre-activation live recheck after network window | cross-boundary | **KEEP** |
| 7 | `store.go:315` (backup Put) | L | copy backup into store | necessary copy | KEEP (edge-hash) |
| 8 | `store.go:319` (backup Put) reread | L | TOCTOU guard | fail-closed guard | **KEEP** |
| 9 | old `store.SHA256File(targetSource)` | E′ | activation-source digest | same-boundary (already known from #4/#5) | **REMOVED — reuse `PutWithDigest` (`upgrade.go:251,322`)** |
| 10 | `transaction/journal.go:559` MultiWriter | E′ | copy redo payload into journal | necessary durable capture | KEEP (edge-hash) |
| 11 | `transaction/journal.go:588` reread | E′ | capture guard + independent re-verify of store artifact | cross-check backstop for #9 | **KEEP** |
| 12 | `journal.go:559` (original) | L | original redo payload | necessary durable capture | KEEP (edge-hash; needsOriginal) |
| 13 | `journal.go:588` (original) reread | L | capture guard | fail-closed | KEEP (needsOriginal) |
| 14 | `journal.go:559` (live before) | L | live before-state redo payload | necessary durable capture | KEEP (edge-hash) |
| 15 | `journal.go:588` (live before) reread | L | capture guard | fail-closed | KEEP |
| — | `validateTransactionStateSHA` ×N (`cmd/state_transaction.go:45`) | — | compares already-captured digests | no file I/O | reuse (already) |
| — | prune `inspectVersion` per retained/deleted version | store versions | pre-destructive integrity verify | cross-boundary | KEEP (not same-stream redundancy) |

Whole-file passes per single-tool upgrade (checksum present, first upgrade /
needsOriginal): **15 → 13**. Subsequent upgrades (original exists, #12/#13
absent): **13 → 11**. Both −2, on the two largest streams (release asset, new
binary).

**Backstop proof for #9 (review item 4b):**
`cmd/upgrade_tamper_test.go` tampers the immutable store artifact after
`PutWithDigest` returned its digest and before `statejournal.Begin` captures
it. The journal's independent capture hash (#11) plus
`validateTransactionStateSHA(tx, "live", targetSHA, true)` — the exact check
`upgradeOne` runs before `Apply` — rejects the activation,
`abortStateTransaction` recovers to a clean journal, and the live path is
untouched. The genuine artifact passes the same check (positive control).

## Benchmarks (review item 3: cold/hot split)

Benchmark definitions:

- `BenchmarkManifestBatchGetLinear` — pre-index behavior: 100 lookups × fresh
  linear scan (O(n²) batch).
- `BenchmarkManifestBatchGetColdIndex` — first use: one eager index build
  (what Load/Decode/Clone pay) + 100 verified-hit Gets.
- `BenchmarkManifestBatchGetHotIndex` — steady state: index already built,
  100 verified O(1) hits.
- `BenchmarkStoreVersionActivateRedundant/Deduped` — store+activate segment on
  a ~4.5 MiB artifact; `SHA256File/op` is the measured count of whole-file
  hash passes (excluding the necessary copy edge-hash): redundant = old
  re-hash of `targetSource`, deduped = reuse of `PutWithDigest`'s digest.

Measured numbers are recorded in the "Verification on the fixed commit"
section below, from multi-round runs on the reviewed commit.

## Verification on the fixed commit

Recorded by the follow-up docs commit after running, on the code commit:
`go test -count=1 ./...` && `go test -count=1 -race ./...` && `make verify`
plus multi-round benchmarks (`-count=5`).
