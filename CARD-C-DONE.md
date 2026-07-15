# Card C — Digest threading + call-site batch fix

Branch: `card-c-20260715`. Scope: remove redundant *same-boundary* re-hashing
of the same byte stream in the upgrade pipeline, and fix the `upgrade --all`
batch loop's redundant manifest scans at the call site. Every cross-trust-
boundary verification hash (pre-activation live recheck, publisher-checksum
verification, journal payload capture/re-verify, pre-destructive prune
verification) is preserved. No third-party deps added; `gobin.go`/`detect.go`
untouched.

History: an earlier revision added a name→position lookup structure inside
`internal/manifest`. Two independent review rounds (Codex xhigh) each found a
correctness gap in its staleness reasoning — the second a deterministic
counterexample (renaming `Entries[0]` through the exported slice or a
Get-returned pointer defeats the per-slot verification while remaining a valid
"hit"). Product ruling: the structure's complexity outran its correctness
argument twice and the real workload does not need it, so it was **withdrawn
entirely**. `Get`/`Put`/`Remove` are pure linear scans again (authoritative by
construction), and the quadratic batch pattern is fixed where it lived: in the
`upgrade --all` loop.

## Change list

| File | Change |
|---|---|
| `internal/store/store.go:235` | New `PutWithDigest(name, tag, src) (string, error)` returns the content SHA-256 the store already computes (copy MultiWriter, `:315`) and cross-checks against a fresh source read (`:319`). `Put` (`:224`) is a thin wrapper — no existing caller churn. Digest returned on the fresh-copy path (`:366`) and on the idempotent existing-version path (`:285`, computed from the destination artifact). |
| `internal/store/store.go:23` | Unexported `sha256FileCalls atomic.Uint64`, incremented in `SHA256File`. Diagnostics/benchmark only; no production decision reads it. |
| `internal/verify/verify.go:161,183` | `VerifyAsset` keeps its **original error priority**: missing checksum entry → `ErrNoChecksum` and invalid published digest are both decided *before* the asset file is opened; only then can a read error or mismatch surface. New `VerifyAssetDigest` (`:183`) performs the same map-level contract against a caller-supplied digest. |
| `cmd/upgrade.go:232,239` | Download asset hashed **once**; the digest both records `asset_sha256` and feeds `verify.VerifyAssetDigest`. Was: `VerifyAsset` (full read) + `SHA256File` (full read). |
| `cmd/upgrade.go:274,348` | `PutWithDigest` return reused as `targetSHA`; removed the second full read of the just-stored artifact. The journal still captures `targetSource` independently and the pre-Apply digest check re-verifies that capture — pinned by the real-flow tamper test below. |
| `cmd/upgrade.go:102-131` | **Call-site batch fix**: the upgrade loop iterates a snapshot of the targets and holds each `Entry` copy directly instead of re-resolving it with an in-loop `m.Get` linear scan. Each snapshot entry is authoritative for its own upgrade because `upgradeOne` mutates only the entry it was given; write-back remains `m.Put` (one linear scan per upgraded tool, imperceptible at n ≤ hundreds). Targets are deduplicated by name (`:113`) so a repeated argument cannot operate on a stale snapshot. No constant-time lookup structure is involved and none is claimed. |
| `cmd/upgrade.go:29-35,278` | `upgradeTestHookAfterStoreNewVersion`: nil in production; fires between the store commit of the new version and all subsequent validation/transaction capture. Exists solely to give adversarial tests a deterministic injection point inside that window while driving the real flow. |
| `internal/manifest/manifest.go:877,890,904` | `Get`/`Put`/`Remove` are the original pure linear scans (plus a doc note that Get performs no writes). The only diff against the pre-card-C baseline is that two-line comment. |
| `internal/manifest/bench_test.go` | Benchmark of the batch-upgrade loop's total manifest-operation time, old shape vs new shape (below). |
| `internal/store/bench_test.go` | Whole-file hash-pass benchmark for the store+activate segment (redundant vs deduped), reporting a measured `SHA256File/op` metric. |
| `internal/store/put_digest_test.go` | Digest contract tests: fresh path, idempotent existing path, conflicting-content rejection. |
| `cmd/upgrade_tamper_test.go` | Real-flow tamper rejection test (below). |
| `internal/manifest/lookup_test.go` | Concurrent read-only `Get` race test (pure linear scan performs no writes; passes `-race` with zero synchronization) and first-occurrence tie-break pinning for Get/Put/Remove. |
| `internal/verify/verify_contract_test.go` | Error-priority contract tests: missing-file × {missing, invalid, valid} checksum-entry combinations with sentinel/class assertions. |

## Decision table — every whole-file SHA-256 in one `hukou upgrade <tool>` pass

Streams: **A** = downloaded asset, **E/E′** = extracted new binary → store
version → new live, **L** = current live (old) → backup + original.
"edge-hash" = hash computed *while copying* (MultiWriter), not an extra read.

| # | Site | Stream | Purpose | Boundary | Decision |
|---|---|---|---|---|---|
| 1 | `updatecheck/check.go:87` | L | drift precheck vs manifest before network | cross-boundary | **KEEP** |
| 2 | `verify.VerifyAsset` internal hash (old call) | A | publisher-checksum verify | cross-boundary | **MERGED → #3** (verification kept, byte pass merged) |
| 3 | `cmd/upgrade.go:232` | A | record `asset_sha256`; feeds `VerifyAssetDigest :239` | cross-boundary | **KEEP** (single asset pass) |
| 4 | `store.go:315` MultiWriter (new binary Put) | E | copy into store | necessary copy | KEEP (edge-hash) |
| 5 | `store.go:319` source reread | E | TOCTOU: source stable during copy | fail-closed guard | **KEEP** |
| 6 | `cmd/upgrade.go:284` (`latestSHA`) | L | pre-activation live recheck after network window | cross-boundary | **KEEP** |
| 7 | `store.go:315` (backup Put) | L | copy backup into store | necessary copy | KEEP (edge-hash) |
| 8 | `store.go:319` (backup Put) reread | L | TOCTOU guard | fail-closed guard | **KEEP** |
| 9 | old `store.SHA256File(targetSource)` | E′ | activation-source digest | same-boundary (already known from #4/#5) | **REMOVED — reuse `PutWithDigest` (`upgrade.go:274,348`)** |
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

## Backstop proof for #9 — real-flow tamper test

`cmd/upgrade_tamper_test.go` (`TestUpgradeRejectsStoreArtifactTamperedAfterStorePut`)
drives the production path end to end: adopt a binary, serve a v2 release from
an httptest fake GitHub server (existing e2e pattern), run `doUpgrade`, and
tamper with the immutable store artifact inside the exact window whose second
hash was removed.

- **Injection point**: `upgradeTestHookAfterStoreNewVersion`
  (`cmd/upgrade.go:278`), invoked immediately after `PutWithDigest` commits
  the new version — i.e. at the top of the Put→capture window. Nothing in the
  flow re-reads the artifact between that point and `statejournal.Begin`'s
  capture (`ActivationSource` only lists/validates the directory), so
  tampering at the hook is equivalent to tampering anywhere in the window.
- **Limitation**: the hook is a test seam compiled into the package (nil and
  inert in production). It marks the earliest point of the window rather than
  racing the filesystem; since no intermediate read of the artifact exists,
  this loses no coverage.
- **Assertions**: the whole upgrade fails with a SHA-256 mismatch rejection,
  the live binary still holds the v1 bytes, the manifest is byte-for-byte
  unchanged (and still records v1.0.0), and the aborted transaction leaves a
  clean journal (`CheckClean` passes).

## Benchmarks

darwin/arm64 (Apple M-series, `-10`), 3 rounds each.

### Batch-upgrade loop manifest operations — 100 targets, total time per batch

    BenchmarkUpgradeBatchManifestOpsInLoopGet-10   40166 / 40253 / 40369 ns/op   0 allocs/op   (old loop shape: in-loop Get + Put)
    BenchmarkUpgradeBatchManifestOpsSnapshot-10    21928 / 21873 / 21353 ns/op   1 alloc/op    (new loop shape: snapshot + Put)

The call-site fix removes the per-target `m.Get` scan and roughly halves the
batch's manifest-operation time (~40.3 µs → ~21.7 µs per 100-target batch);
the single allocation is the up-front snapshot copy. The remaining `m.Put` is
one linear scan per upgraded tool. These are measured totals, not asymptotic
claims.

### Store + activate segment — whole-file SHA-256 passes (~4.5 MiB artifact)

    BenchmarkStoreVersionActivateRedundant-10   37.7–38.6 ms/op   2.000 SHA256File/op   (x3 rounds)
    BenchmarkStoreVersionActivateDeduped-10     36.2–36.8 ms/op   1.000 SHA256File/op   (x3 rounds)

The deterministic claim is the measured `SHA256File/op` counter: exactly
**2 → 1** whole-file passes over the new store artifact in every round
(excluding the necessary copy edge-hash). Wall time is I/O-dominated and
noisier; the hash-pass count is the regression-proof metric.

## Local verification

`go build ./...`, `go vet ./...`, `go test ./...` (19/19 packages ok), and
`go test -race` on the touched packages (`internal/manifest`, `cmd`) all pass
on this commit. Full gates (`go test -count=1`, `-race`, `make verify`) are
re-run independently by the coordinator on the fixed commit.
