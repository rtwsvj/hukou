# Roadmap

## Delivered

### Phase 1: scan

- PATH traversal, type detection, shadowed handling.
- Tier 1 source-detection chain of responsibility.
- Table/JSON output and error/warning reporting.
- macOS native detector patches.

### Phase 2: adopt / upgrade / rollback / list

- manifest and version store.
- GitHub Releases client, asset selection, archive extraction, and checksum
  parsing.
- Command-layer mock e2e tests.
- First round of download, path, and redirect security hardening.

"Delivered" means the implementation exists; whether current HEAD passes is
determined by the latest verification report.

## H1: Security Hardening and First SemVer Release

Status: **`v0.1.0` released; GitHub-hosted runner billing/scheduling exception
recorded**.

- [x] checksum missing-entry fails closed
- [x] downloaded asset hash kept separate from active binary hash
- [x] observable failure compensation for upgrade/rollback
- [x] manifest/store process lock
- [x] purely local dry-run
- [x] adopt same-name conflict protection
- [x] hukou source integrity prompts
- [x] current documentation source of truth and internal review record chain
- [x] Linux/macOS CI workflow (the most recent run was rejected by GitHub
      before 0 steps due to account payment/spending limits; must not be
      interpreted as a code pass or fail)
- [x] four-platform reproducible packaging and checksums
- [x] full verification and release report
- [x] [`v0.1.0` GitHub Release](https://github.com/rtwsvj/hukou/releases/tag/v0.1.0)

There is evidence for the code, local full/race/adversarial stress testing,
isolated dual Linux builds, four-platform artifacts, remote re-download, and
the Release. The GitHub-hosted runner gate did not execute due to account
billing limits, which is an explicit infrastructure exception; it should be
rerun once billing is restored, and must not be retroactively recorded as a
historical pass.

## H2: Operations and Crash Recovery

Status: **Recovery and read-only diagnostics foundation shipped with
[`v0.2.0`](https://github.com/rtwsvj/hukou/releases/tag/v0.2.0); repair/history
policy has moved into the unreleased V0.3 branch, and public-network smoke
testing is not yet complete**.

- [x] adopt/upgrade/rollback single global WAL: PREPARED rollback, COMMITTED
      roll-forward, fail-closed on unknown drift
- [x] file and parent-directory durability for manifest, store, live, and
      transaction payloads
- [x] a previous, parseable, schema-supported `manifest.json.bak`
- [x] zero-write, zero-network-by-default `doctor` text/JSON audit with
      orphan/unclassifiable distinction
- [x] macOS + native Linux full/race testing, crash/durability stress
      testing, four-platform reproducible assets, and remote-download
      re-verification
- [x] two explicitly enumerated, state-fingerprint-bound narrow repair
      actions (implemented in the V0.3 subject and passed internal
      local/private RC acceptance; not yet released)
- [x] activation history and configurable version-retention policy (manifest
      v2 implemented in the V0.3 subject and passed internal local/private RC
      acceptance; not yet released)
- [ ] scheduled smoke testing against a real public fixture repo

## V0.3: Trust-First Private Release Candidate

Status: **subject commit `1fa45a0` has passed local/private RC readiness
acceptance and is in [draft PR #6](https://github.com/rtwsvj/hukou/pull/6);
GitHub-hosted CI was infrastructure-blocked by billing before 0 steps. Not
merged, not tagged, not released; the repository remains private**.

- [x] `explain`, `adopt --dry-run --json`, shared inventory
- [x] `outdated` and upgrade dry-run/real upgrade share a policy-aware
      checker
- [x] `policy show/set`: SemVer/GitHub-latest, channel, exact pin, rollback
      depth
- [x] manifest schema v2, v0/v1 deterministic migration, strict validation
- [x] activation lineage, parent-based rollback, history-aware prune plan
- [x] fingerprint-bound `repair plan/apply`, two action types
- [x] offline redacted `support bundle`
- [x] English-default CLI, bilingual README, Apache-2.0, notices, community
      health files
- [x] checksum installer, license/install/shell gates, SBOM, and public-only
      attest/CodeQL configuration
- [x] Topgrade custom command integration docs; `upgrade --all` continues to
      manage only hukou entries
- [x] uncached whole-repo ordinary/race/coverage/vet/build final green, and
      commit pinned
- [x] macOS/Linux, four-target build, dual-build reproducibility, and release
      snapshot/SBOM content acceptance
- [x] internal claims-vs-evidence review (P0/P1/P2 = 0 recorded at the time;
      no standalone raw report retained)
- [ ] third-party external security/reliability audit; a handoff package has
      been established, with key hypotheses still to be confirmed or ruled
      out
- [x] pushed the private branch and created a draft private PR; the hosted
      gate's billing block has been recorded as an external gate

Evidence for the final pinned commit:

- Targeted audit of security-critical paths: 321 tests / 6 packages.
- direct uncached whole-repo ordinary/race: 641 tests / 21 packages each,
  zero failures.
- `GOPROXY=https://goproxy.cn,direct make release-verify`: exit 0, coverage
  72.9%, govuln `No vulnerabilities found`; the default `proxy.golang.org`
  IPv6 timeout remains separately noted.
- installer link(2)/rename(2), duplicate member, symlink/race, and strict
  shell SemVer; manifest schema-specific/legacy-smuggling, activation
  safe-tag/tag-SHA, list original integrity, and other targeted matrices:
  pass.
- actionlint/Ruby YAML, 68 Markdown/89 relative targets, production
  CJK-character sweep, official Action tag pin reconciliation, secret scan,
  and `git diff --check`: pass.
- non-root Linux/arm64 (UID 65534, source/module cache read-only,
  `GOPROXY=off`): whole-repo ordinary/race and GNU tar 1.34
  installer/release tests: pass.
- four-target dual builds are byte-for-byte identical, 4/4 checksums,
  archive content/mode, buildinfo, and installer smoke: pass. The Syft
  1.46.0 SBOM is SPDX 2.3, 21 packages/4 files.
- Draft PR #6's CI run `29352308455` had all five jobs report `steps=[]`;
  the billing annotation clearly indicates an account infrastructure block.
  The CodeQL private skip is not recorded as a pass.

These results record only V0.3's internal local/private RC readiness. The
handoff phase has surfaced high-priority audit hypotheses covering
download/archive resource budgets, the WAL intent trust anchor,
private-to-public attestation timing, and toolchain differences; until an
external audit confirms or rules them out, the original internal P0/P1/P2
record must not be interpreted as an external clean bill. Merging, tagging,
releasing, public visibility, and the public companion repository still
require an independent Go/No-Go decision.

## After V0.3

- Version snapshots, changelog diffs, risk warnings.
- mise/Brewfile export.
- Automatic repo matching for non-Go binaries.
- tar.xz support decision.
- Windows design and testing.
- Public fixture repo, scheduled real-network smoke testing, Homebrew Tap,
  and public beta Go/No-Go.
- If a cross-manager control plane is needed in the future, file a separate
  ADR; V0.3 leaves outer-layer orchestration to Topgrade alone.
- Defense-in-depth backlog: duplicate JSON key rejection, GitHub API body
  cap, installer total-extraction-size/member-count budget,
  `openat`/directory-fd path anchoring.
