# Risks and Technical Debt

## H1 Release Closure Conditions

The following defects already have implementations and local tests, but can only be considered closed after final verification by the verification report and the remote release workflow; this should not be read as implying the current code still deliberately retains these old behaviors:

- Missing checksum entries must fail closed.
- A save failure after upgrade/rollback activation must be compensated.
- Write commands must hold a cross-process mutex lock.
- dry-run must maintain zero local writes.
- Release artifacts must verify version injection, the four-asset count, dual-build consistency, and checksums.

## Known Debt and V0.3 Status

| Risk | Current Boundary | Follow-up Direction |
|---|---|---|
| Power loss/SIGKILL | A single global WAL, file/dir sync, and real process-kill tests already cover hukou-cooperative transactions; ordinary CI cannot fully simulate hardware power loss and cache reordering | Periodic crash harness on target filesystems such as APFS/ext4; not expanding to unverified platforms |
| Non-cooperative external in-place writes | SHA is re-checked both after download and after snapshotting; the regular snapshot is an independent copy; but a narrow window remains between the final re-check and activation | File-descriptor binding and OS-level coordination strategy |
| File metadata | Copying only guarantees the bytes and rwx permission bits; owner/group, ACL, xattr, mtime, special permission bits, and hardlink topology are not preserved; `adopt` rejects privileged/special permission bits | If the preserved scope needs to expand, first define a cross-platform contract and failure semantics |
| Default rollback selection | The V0.3 subject now uses the manifest v2 activation parent and no longer reads mtime; the lineage fault matrix and fixed-commit regression have passed; v0.2 still has the old behavior | Continue observing real-user migration before public release |
| Manifest corruption | The V0.3 branch already has fingerprint-bound backup restore, but it only accepts cases where main is missing/invalid, the backup is semantically valid, the transaction is clean, and all live SHAs match | Keep the action narrow; do not expand into automatic merge/guessed recovery |
| Orphaned store versions | doctor distinguishes tools outside the manifest from legitimately retained versions, and only reports without deleting | Explicit, item-by-item quarantine + undo, not a repair-all |
| tar.xz | Explicitly unsupported | Decide whether to introduce an xz dependency |
| Version comparison | The V0.3 subject already has SemVer/GitHub-latest, channel, pin, and downgrade protection; local/isolated Linux gating passes, but no public-fixture network smoke test has been done | Only claim public stability after a public-fixture smoke test |
| Fixed retention of 3 versions | The V0.3 subject switched to global/entry rollback depth plus a lineage/pin/pending protection set; the fixed-commit fault matrix passes | Observe history growth, then design compression/archiving rather than guessed deletion |
| Real network coverage | PRs use httptest only | Independent fixture smoke test |
| Platform | Windows unsupported | Separate design and CI |

## New V0.3 Boundaries

| Risk | Current Boundary | Follow-up Direction |
|---|---|---|
| schema v2 backward compatibility | V0.2 must reject v2 to avoid old writers dropping history/policy; migration only builds a synthetic root from the current state and cannot recover historical facts | Backup and downgrade documentation before release; no silent v2→v1 conversion is provided |
| schema field boundary | The V0.3 decoder now requires v2 policy/retention/history per the declared schema, and rejects v0/v1 entries carrying v2-only fields; but Go's JSON still accepts duplicate object keys | Add a duplicate-key-aware tokenizer/decoder and regression tests later; the current unknown-field check does not claim to cover duplicate keys |
| History growth | Activation events are appended; retention only deletes artifacts, never events | Design auditable compression in a separate ADR; not guessed in V0.3 |
| Explicit original | Legacy migration cannot prove the original's parent; lineage is deliberately terminated after restore | When the user needs to continue upgrading, a new forward event is built from that state; documentation notes the default rollback has no ancestor |
| Repair plan freshness | The plan is bound to the data-root identity/fingerprint; at apply time, any relevant state difference makes it stale. Writing the plan into the observed tree can invalidate itself | Recommend placing the plan outside the data root; the staleness check is not relaxed |
| Lock traces from repair apply | apply confirms existing root durability and creates/uses `state.lock`, but a fingerprint failure must not change live/store/manifest/journal | The verification report clearly distinguishes the lock file from zero writes to business state |
| support privacy | Currently only anonymized ordinals/counts/enums are output; secret-fixture regression must still be run continuously, and users should also manually review before public submission | A default deny-list for future fields is not enough; an explicit allow-list should be maintained |
| Installer trust root | checksum and archive come from the same GitHub Release; when the installer detects an authenticated gh CLI, it runs `gh attestation verify` on the downloaded archive (`--repo` + an anchored `--cert-identity-regex` pins the certificate SAN to the release workflow's `refs/tags/v*` run; `--signer-workflow` is not used because gh implements it as an unescaped, unanchored SAN prefix regex); `HUKOU_REQUIRE_ATTESTATION=1/true/yes` can force it, and invalid values fail loud; when gh is missing/unauthenticated, it still defaults to falling back to transport-only trust | Attestation is only produced by the attest job once the repository is public; before then this verification is unavailable on real releases; evaluate default enforcement and signing policy before the public flagship release |
| Install target contention | The V0.3 subject now treats dangling symlinks as existing; when Perl is available, the final directory entry uses `link(2)` atomic no-replace / forced `rename(2)`; on Linux without Perl it falls back to `ln -T`/`mv -T`; directory, symlink-to-directory, post-precheck contention, and duplicate-member tests pass on the fixed commit | Keep the same-filesystem-as-target-directory precondition; force remains a separate, explicitly opt-in overwrite path |
| Plan replay/freshness | The fingerprint only covers action-relevant observations; unrelated changes do not make the plan stale; `generated_at` carries no expiry semantics — if the live state returns exactly to an old observation, the old plan may become applicable again | External audit of plan replay; if freshness semantics are needed, add an explicit expiry/nonce rather than relying on display time |
| Transaction intent authorization | The intent validates the schema, absolute clean/unique paths, and before/after payloads, but does not independently bind operation/role/path to the manifest or an allowlist; current security relies on the data root being exclusively owned by the same trusted identity and not consuming low-privilege input in a privilege-escalating way | Define an explicit data-root ownership/permission threat model; audit low-privilege writable roots and elevated invocation, binding role/path if necessary |
| Download size | 512 MiB is used when the API size is unknown; when a positive value is declared, the declared value is currently used as the read ceiling, with no independent global ceiling | Apply both a global maximum and a disk budget; add fail-closed tests for abnormally oversized asset sizes |
| Go archive pre-scan | The selected entry itself is capped at 512 MiB, but the tar's initial selection scan walks the full decompression stream without accumulating total work/member count | Add a streaming budget for header count, total expansion work, and candidate count |
| Shell installer package bloat | Currently checks the archive root, target member uniqueness, and target file type; no independent budget yet for total expanded bytes or member count | Add a streaming budget for zip bombs/excessive member counts, failing closed before writing the target when exceeded |
| Installer network budget | `curl` restricts to the HTTPS protocol and retries, but sets no explicit connect/total timeout, maximum file size, or redirect host allowlist | Add a fail-closed network budget and redirect-host policy, and cover slow-connection/oversized-body/cross-host fixtures |
| No publisher checksum | Default is fail-closed: upgrade refuses when the release publishes no checksum asset. Explicit `--allow-unverified` is the only bypass; it records the post-download hash, prints `UNVERIFIED`, and persists `checksum_verified: false`. This provides auditability of the bypass, not proof of publisher identity. `--allow-unverified` cannot skip a present-but-mismatched/missing entry. `hukou up`'s embedded upgrade never passes the bypass | Keep the fail-closed default for public flagship; consider attestation/signing as a stronger trust root for releases that still ship no checksum |
| Release list ceiling | At most 10x100 releases; fails closed once the ceiling is filled | Introduce a bounded completeness proof if an oversized repository is actually encountered |
| GitHub API body | Requests have a total timeout and bounded pagination, but the JSON response has no independent body byte cap yet | Use a bounded reader and test over-limit responses to avoid memory amplification from an anomalous server |
| Manifest resource budget | The regular manifest currently has no independent byte cap; activation history is appended and not compressed | Set an upper bound on file bytes and entry/event counts; design auditable history compression in a separate ADR |
| Path traversal TOCTOU | Currently rejects symlinks/non-directories component by component, re-checks before writing, and validates tag/SHA; the store root itself can serve as a deliberate symlink trust anchor; a non-cooperative writer may still hit the window between the check and the syscall | Evaluate `openat`/directory-fd-anchored traversal and root pinning; first define consistent Darwin/Linux failure semantics |
| explain read-only evidence | The V0.3 subject now adds independent name/path directory snapshots and an `http.DefaultTransport` spy; 5 targeted tests and the fixed-commit whole-repo regression pass | Keep the tests, to prevent future detector drift from breaking the zero-write/zero-network contract |

## H2 Recovery Boundaries

- The WAL only covers the before/after states precisely bound in the journal. Recovery first classifies all participants and re-checks before writing; if live/manifest/original is found to have become a third state at that point, it stops and retains pending evidence. A non-cooperative external write can still hit the narrow TOCTOU window between the final re-check and the rename/remove syscall.
- scan, list, doctor, and pure `--dry-run` do not hold the write lock; they report the pending transaction known at the instant of the check, but provide no consistent-snapshot guarantee against concurrent non-cooperative filesystem writes.
- A successful durability return means the operating system accepted the file/dir sync; it does not mean the disk media, controller, or filesystem can never be corrupted.
- `.hukou-rollback-*` files left over from old versions have no transaction ID; doctor can only report them, not guess a recovery.
- doctor currently has no automatic repair; this is a deliberate safety boundary, not a missing generic delete button.

## Phase 1 Known Limitations

- There is a TOCTOU between scan's Stat and Open.
- Ownership attribution for npm wrappers, nvm, and custom prefixes cannot always be precisely reconstructed.
- Empty PATH segments are deliberately skipped, not interpreted as CWD per POSIX.
- Detection results are an evidence-driven best judgment, not a software-supply-chain proof.

## Release and Engineering Risks

- The repository is still private, but the V0.3 branch has already added the Apache-2.0 root LICENSE, THIRD_PARTY_NOTICES, and the licenses of dependency/adaptation sources; this is public-release preparation and does not mean the repository is already public or that V0.3 has shipped.
- The release archive's bilingual README, root LICENSE, notices, and `LICENSES/` have been accepted in the four-target dual-build snapshot; 4/4 checksums, root/mode/buildinfo, and the installer smoke test pass.
- The fixed subject has completed ordinary/race testing in a non-root Linux/arm64 container, and completed installer/release tests under GNU tar 1.34; the source/module cache is read-only, with `GOPROXY=off`.
- GitHub-hosted runners and Action major versions will evolve; workflow upgrades should be verified separately.
- Draft PR #6's CI run `29352308455` has confirmed that all five jobs failed before 0 steps due to the account billing/spending limit; local results must not be passed off as a remote green build.
- The CodeQL job currently only runs for public repositories; being skipped while private does not count as passing.
- The workflow first extracts the four platform binaries from the four archives, then generates and asserts SPDX content with a fixed Syft 1.46.0. Acceptance testing once found that scanning the archive directory produced an empty-shell SBOM of only 1 package/0 files; after the fix it became 21 packages/4 files. Artifact attestation still only runs for public repositories.
- The manual workflow only produces a snapshot, to prevent accidental release; only a pushed tag triggers a Release.
- Attestation only runs if the repository is public at the moment the event occurs; if a Release is first created while the repository is private and visibility is later changed directly, existing unattested assets are exposed along with the repository. A public Go/No-Go decision must audit the complete private tag/Release → public sequencing, not just check a single job's condition.
- `go.mod` declares Go 1.26.2, which the hosted setup-go will read; existing fixed-commit verification and historical archive hashes used Go 1.26.5, while the hosted job has not run. The two toolchains need to be reviewed separately.
- This round's authorization prohibits creating the `v0.3.0` tag, an official Release, or changing repository visibility.

## Risk Closure Rules

An entry in this file cannot simply be deleted. Closing a risk must be accompanied by:

1. The corresponding code or documentation change.
2. A failure-scenario test.
3. A change record.
4. A verification report pointing to a specific commit.
