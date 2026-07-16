# Testing and verification

## Evidence levels

| Level | Content | What it proves |
|---|---|---|
| L1 static | diff, file existence, config parsing | The change landed; it does not prove behavior |
| L2 unit | package-level `go test` | Local contracts |
| L3 full-repo | test, race, vet, build, coverage | The engineering baseline of the current commit |
| L4 isolated smoke | CLI flows over a temporary HOME/PATH/data root | Command wiring and filesystem behavior |
| L5 release verification | four-platform archives, checksums, version injection | The release pipeline |
| L6 real-network smoke | a controlled public fixture repo | GitHub API + asset selection + asset-metadata (URL/size) validation, excluding download/CDN transfer; passed 2026-07-16 |

Historical DONE documents only record what was claimed at the time. A new
"pass" conclusion must be recorded as verification evidence under `docs/audit/`
— commit SHA, command, exit status, and key output — not merely asserted.

## Required gates

```bash
make verify
make release-verify
```

`make verify` expands to fmt-check, module verify, vet, test, race, coverage,
build, license-check, install-test, and release-test. release-test covers strict
SemVer 2.0.0 with a valid/invalid matrix (including empty prerelease segments,
purely numeric leading zeros, and build-metadata rejection). `make
release-verify` additionally runs shellcheck and a pinned-version govulncheck;
each target can still be run on its own.

Also run:

- `git diff --check`
- `go mod verify`
- the release script's static checks and snapshot packaging
- unpack the Linux amd64 archive and run `hukou version`

## L6 real-network smoke (opt-in, passed 2026-07-16)

L6 covers **real GitHub API + asset selection + asset-metadata (URL/size)
validation, excluding download/CDN transfer**. It ran for real and passed on
2026-07-16 (see the run record below). It stays opt-in and is deliberately kept
out of the default gates in `make verify` / `make release-verify` and CI, which
remain zero-network. The entry point is `make verify-network`:

```bash
GH_TOKEN=$(gh auth token) make verify-network
```

That target runs `cmd/network_e2e_test.go` behind two gates that keep the
default zero-network:

- The `//go:build network_e2e` build tag excludes the test file from the default
  compile, so neither `go test ./...` nor `make verify` compiles or runs it.
- Even when compiled with the tag, the test calls `t.Skip` unless
  `HUKOU_NETWORK_E2E=1` is set.
- `make verify-network` is an explicit request to run the gate: it errors out
  immediately when `GITHUB_TOKEN`/`GH_TOKEN` is missing, so a missing token can
  never produce a "silent skip then green". The in-test token skip is only
  defense-in-depth for a direct `go test` invocation.

The smoke targets a fixed fixture repo (default `cli/cli`'s gh CLI — a stable
release cadence with predictable asset names, overridable with
`HUKOU_NETWORK_E2E_OWNER`/`HUKOU_NETWORK_E2E_REPO`, e.g. `junegunn/fzf`) and
validates three stages:

1. a real `ghrelease.Client.Latest` metadata request (GitHub release API
   integration);
2. feeding the returned asset names to a real `assetpick.Pick`, asserting it
   resolves a unique archive for the fixed `linux/amd64` target (asset-selection
   integration);
3. asserting the chosen asset's `BrowserDownloadURL` is a host-qualified https
   URL with `Size > 0` (the metadata fields the download path will consume are
   present).

It **downloads no asset**: real CDN transfer (the networked download, unpack,
and checksum/attestation paths) is outside L6's current declared scope and stays
covered by isolated `httptest` unit tests. The earlier default of `rtwsvj/hukou`
could not run because that private repo had no public release; repointing at a
stable external fixture is what made L6 actually gate-able. Run evidence is bound
to subject commit `a668ef49d2d7be41c990664ada130df31cb7c92f` (platform/toolchain,
command, exit status, and key output) and recorded in
[`docs/audit/2026-07-16-l6-network-smoke.md`](audit/2026-07-16-l6-network-smoke.md).
The entry point can still be extended with a full adopt -> upgrade `--dry-run` ->
rollback flow inside a temporary directory without touching real PATH files;
that extension is additive and does not change L6's current green scope.

## CI

`.github/workflows/ci.yml`:

- Ubuntu: format, module integrity, license/notices, installer, shellcheck, vet.
- Ubuntu: a standalone `govulncheck` job.
- Ubuntu + macOS matrix: test, race, build, binary smoke.
- Ubuntu: coverage profile and artifact.

`.github/workflows/codeql.yml` runs only when repository visibility is public;
in the current private RC the job is skipped, which cannot be called a CodeQL
pass. A GitHub-hosted runner may also be blocked by an account
billing/spending-limit before any step; such a run is recorded only as
`infrastructure-blocked`, which is not a code pass/fail.

CI uses the Go version from `go.mod` and does not maintain a second version
string. The current `go.mod` declares Go 1.26.2, while the fixed commit's local
and container records used Go 1.26.5; the hosted job did not start, so the Go
1.26.2 compatibility gate still needs an independent re-run. Reproducing the
historical archive hashes should use Go 1.26.5 and record the GNU tar version.

## Key fault injection

H1 covers at least:

- The checksum asset exists but is missing the entry for the selected file.
- A checksum mismatch.
- Both the single-digest exact checksum sidecar format and the BSD checksum
  format.
- Asset download 404, size mismatch, and over-limit.
- The live binary is modified externally during download; the pre-activation
  second SHA gate refuses to overwrite.
- A manifest save failure occurring after Activate.
- A rollback save failure occurring after Activate.
- Regular-file activation always exposes only the complete old or new content;
  transaction recovery still covers both regular-file and legacy-symlink input
  topologies.
- The write lock returns `ErrLocked` immediately under same-process and
  child-process contention and can be re-acquired after release; a symlinked lock
  path is rejected.
- An adopt name collision does not overwrite the original/manifest.
- dry-run does not create the data root, does not GC, and does not download.
- All four PREPARED live/manifest before/after combinations converge to before.
- All four combinations after a durable COMMIT converge to after.
- On corrupted transaction payload/COMMIT, or when the pre-check or pre-write
  re-check finds any resource in unknown drift, there is zero overwrite and the
  pending evidence is preserved.
- After a child process is force-killed in the PREPARED/COMMITTED window, the
  next Recover rolls back / rolls forward respectively.
- absent→regular/symlink uses an atomic no-replace, so a check-then-race write is
  not overwritten.
- doctor stays zero-write on missing, healthy, and corrupted data-root fixtures;
  its text/JSON come from one source and are stable.

V0.3 must also cover:

- explain/adopt dry-run: directory snapshot, network-request count of zero, and a
  stable JSON schema/ordering.
- outdated: the zero-network paths for local/pin-current, plus
  drift-before-network and metadata-only/no-download.
- schema 0/1→2 deterministic migration; schema 2 rejection of missing
  policy/history, unknown fields, a future schema, duplicate path/name, and
  forward/missing lineage.
- schema 0/1 carrying v2-only retention/policy/history fields must be rejected and
  must not be smuggled through migration; an activation unsafe tag and a
  same-tag/different-SHA binding must be rejected without modifying the entry.
- `A→B→C→B→A` default rollback, which does not read or trust directory mtime;
  explicit ancestor/original boundaries.
- stable/prerelease, SemVer-normalized equality, implicit downgrade refusal, exact
  pin forward/back, and bounded release pagination.
- retention protection of current/original/pin/N-ancestors/pending refs; a
  malformed protected ref and a pre-apply replacement must delete nothing.
- The repair plan's zero hukou writes, plan mode 0600, and zero business writes on
  stale fingerprint/data-root mismatch/ambiguous journal/live SHA mismatch.
- support: zero writes on stdout, file mode 0600, and no path/repo/user/HOME/env/
  WAL/binary secret from the fixture appearing in the JSON.
- list verifies the original namespace is complete before counting downloaded
  versions; the original is not counted in `VERSIONS`.
- The installer rejects HTTP, an unauthorized file URL, a bad/duplicate/missing
  checksum, a wrong archive root, a duplicate target member, and an existing
  target without force; with Perl present, the final commit covers `link(2)`
  atomic no-replace and force `rename(2)`, and on Linux without Perl it covers the
  `ln -T`/`mv -T` fallback, and it tests directory, symlink-to-directory, and
  post-check race; dry-run performs zero writes.
- installer attestation: a gh mock that strictly validates arguments (the subject
  must be the downloaded archive, with `--repo` and the anchored
  `--cert-identity-regex` value pinned) covers the pass/fail/missing tri-state and
  the `HUKOU_REQUIRE_ATTESTATION` matrix; an illegal value fails loud; a tar spy
  asserts that a verification failure happens before any extraction/installation
  (no tar call, no prefix, no temporary install file).
- The release archive contains LICENSE, THIRD_PARTY_NOTICES, both README
  languages, and LICENSES, and the SBOM and checksums correspond to the fixed
  commit.

## V0.3 fixed-commit evidence (2026-07-15)

| Check | Current result | What it proves / does not prove |
|---|---|---|
| Targeted audit of the security-critical path | 321 passed / 6 packages | schema/activation/ghrelease/manifest/repair/store evidence for the fixed subject commit |
| `go test -count=1 ./...` | 641 passed / 21 packages | subject `1fa45a0` direct uncached ordinary, zero failures |
| `go test -count=1 -race ./...` | 641 passed / 21 packages | subject `1fa45a0` direct uncached race, zero failures |
| `GOPROXY=https://goproxy.cn,direct make release-verify` | exit 0 | all targets pass; coverage 72.9%; govuln reports no known vulnerabilities; the default proxy path separately has an IPv6 timeout |
| explain name/path read-only targeted | 5 passed | an isolated directory snapshot and an `http.DefaultTransport` spy prove this batch is zero-write / zero-network |
| `scripts/install_test.sh` | pass | includes Perl link(2)/rename(2), the Linux-without-Perl `-T` fallback, and directory/symlink-dir/race/duplicate member |
| `scripts/release_test.sh` | pass | the strict shell SemVer matrix with v-prefix and no build metadata; it does not prove the snapshot |
| Linux/arm64 non-root container ordinary/race | pass / all packages | fixed image digest, UID/GID 65534, read-only source/module cache, `GOPROXY=off` |
| Linux GNU tar 1.34 installer/release tests | pass | the release test passes after configuring git safe.directory; a first root/default-proxy failure is not counted as a code failure |
| Four-target dual build and snapshot | pass | the two directories are byte-for-byte identical; 4/4 checksum, single root / single executable, buildinfo, and installer smoke pass |
| Syft 1.46.0 SPDX JSON | 21 packages / 4 files | the real binaries for all four platforms and all four direct dependencies are listed; the empty-shell SBOM gap is closed |
| actionlint 1.7.12 / Ruby YAML parse / Action pin reconciliation | pass | workflow static structure and pinned SHAs; the hosted run still needs a separate explanation |
| Markdown links / production CJK sweep / secret scan / `git diff --check` | 68 Markdown, 89 targets, 0 missing; 0 CJK; 0 leak; diff pass | documentation, UI, and commit-hygiene gates |

An internal team review recorded P0/P1/P2 = 0 for the fixed subject at the time,
but it kept no standalone raw report and cannot serve as an external clean bill.
The GitHub-hosted CI run `29352308455` for draft PR #6 failed on all five jobs
before any step because of a billing/spending limit, which cannot be recorded as
a remote code failure or a remote green; the CodeQL run `29352310557` was skipped
by design in the private repository.

The 2026-07-15 external handoff review additionally raised high-priority
hypotheses about the download/archive resource budget, the trust root that
authorizes a transaction intent, the timing of attestation becoming public after
a private Release, the default policy when a publisher checksum is missing, and
toolchain differences. They still await third-party confirmation/triage; see
[`audit/v0.3-review-checklist.md`](audit/v0.3-review-checklist.md).

The gap-audit gaps have been closed in the working tree: with Perl present, the
installer uses `link(2)` atomic no-replace / force `rename(2)`, and on Linux
without Perl it uses the `ln -T`/`mv -T` fallback; it covers directory,
symlink-to-directory, and post-check race, and rejects a duplicate target member.
The release workflow removed the historical `v0.1.0` manual-snapshot default.
Also added: schema-specific manifest required fields, legacy v2-only smuggling
rejection, activation safe tag and tag/SHA binding, list original completeness,
and a symlink adopt→upgrade→implicit rollback E2E; these contracts were re-run
under the full-repo and targeted gates of the final subject commit.

Two follow-up P2s from the gap audit are also closed in the working tree:
Store.Versions fails closed on a non-directory/malformed version with two test
sets, and explain gained the five zero-write / network-spy targeted tests above.

Further defense-in-depth is not yet done, and the test plan must not be marked
green early: duplicate JSON key, GitHub API body cap, the installer's total
extraction size / member-count inflation budget, and `openat`/directory-fd path
anchoring.

## Coverage

- The profile is a CI artifact and is not committed to Git.
- H1 first establishes the current real baseline; do not invent a percentage
  threshold before the baseline is known.
- Do not later reduce overall coverage without explanation.
- V0.3 overall coverage is 72.9%, down 0.9 points from the 73.8% of v0.2/H2. The
  cause is that the new repair, support, policy, and supply-chain paths enlarged
  the production-code surface; the security-critical contracts already have 321
  targeted tests, 641 full-repo ordinary/race, and fault-matrix coverage. This
  private RC accepts the small decline and records it as P3, prioritizing higher
  branch coverage of support/store/repair/output next.
- `cmd`, store, manifest, verify, archive, and ghrelease are the
  security-critical packages to prioritize.

## Isolation requirements

- Tests uniformly use `t.TempDir` or the runner's temporary directory.
- Do not read a real manifest/store.
- Do not hand a real PATH to the adopt/upgrade/rollback e2e.
- PR tests use `httptest` and do not access the public network by default.
- The real-network smoke is a separate manual/scheduled workflow that uses a
  read-only fixture repo and a minimal-permission token.

## Minimum fields of a verification report

- Verification ID, date, commit, OS, Go version.
- The actual commands run and their exit status.
- Claims vs Evidence.
- Not-run/skipped items and the reason.
- The generated release artifact names and checksums.
- Summary: pass, partial, fail, or inconclusive.
