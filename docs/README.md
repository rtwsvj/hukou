# hukou documentation entry point

## Purpose

This directory is the single entry point for the project's current facts and
its long-lived decisions. A reader should not infer current behavior from any
one historical `*-DONE.md`; those records only prove what was claimed complete
at the time.

## Current status

| Area | Status | Current evidence entry |
|---|---|---|
| Phase 1: PATH scan and attribution | Implemented | `specs/phase1-scan.md`, `records/` |
| Phase 2: adopt, upgrade, rollback, manifest | Implemented | `specs/phase2-adopt-upgrade.md`, `records/` |
| H1 safety hardening | Delivered; GitHub-hosted-runner billing exception recorded | `audit/`, `08-risk-and-debt.md` |
| First SemVer release | [`v0.1.0`](https://github.com/rtwsvj/hukou/releases/tag/v0.1.0) published | GitHub Releases |
| H2 recovery and doctor foundation | Released and verified with [`v0.2.0`](https://github.com/rtwsvj/hukou/releases/tag/v0.2.0); further boundaries in the roadmap | `02-roadmap.md`, GitHub Releases |
| V0.3 private RC | Local/private RC readiness recorded for subject `1fa45a0`; external audit pending; the hosted gate is infrastructure-blocked by billing; unmerged, unreleased, not public | `../AUDIT.md`, `audit/`, `specs/v0.3-private-rc.md` |
| Public fixtures, public install channel, cross-manager execution, Windows | Not implemented / out of this RC | `02-roadmap.md`, `integrations/topgrade.md` |

Any "pass" conclusion must come with a commit SHA and the corresponding
evidence under `audit/`. An execution report is not a verification report.

The V0.3 fixed-commit evidence: the security-critical path audits 321 tests /
6 packages; a direct uncached full-repo run is 641 tests / 21 packages for both
ordinary and race modes; a full local `release-verify` under a command-scoped
GOPROXY mirror exits 0, with 72.9% coverage and no known vulnerabilities from
govulncheck; a non-root Linux/arm64 run, the four-target dual build, the
installer/release tests, and the 21-package / 4-file SPDX SBOM all pass. The
final boundaries, artifact hashes, remote billing exception, and the internal
review are recorded in the audit package; that review kept no standalone raw
report, so a third party should re-verify from [`audit/`](audit/).

## Source-of-truth priority

1. User behavior: root `README.md`, `05-cli-reference.md` (the final truth of a
   command is the current code).
2. Requirements and safety invariants: `01-requirements.md`, approved specs, ADRs.
3. Architecture and data: `03-architecture.md`, `04-data-and-api.md`.
4. Verification and release: `07-testing-and-verification.md`, `09-release.md`.
5. Current progress: `02-roadmap.md`, the audit evidence map.
6. Historical material: `records/*-DONE.md`.

On a contradiction, do not silently pick one document: record the ruling in
`09-decision-log.md` and update the affected specs and user docs.

## Document map

- [`00-project-brief.md`](00-project-brief.md): goals, users, and boundaries
- [`01-requirements.md`](01-requirements.md): functional requirements and safety invariants
- [`02-roadmap.md`](02-roadmap.md): phase progress and open items
- [`03-architecture.md`](03-architecture.md): modules and key flows
- [`04-data-and-api.md`](04-data-and-api.md): manifest, store, environment variables, and external API
- [`05-cli-reference.md`](05-cli-reference.md): commands, flags, and side effects
- [`06-dev-setup.md`](06-dev-setup.md): development, build, and local isolation
- [`07-testing-and-verification.md`](07-testing-and-verification.md): verification layers and evidence rules
- [`08-risk-and-debt.md`](08-risk-and-debt.md): risks, limits, and technical debt
- [`09-decision-log.md`](09-decision-log.md): index of important decisions
- [`09-release.md`](09-release.md): versioning and release process
- [`10-glossary.md`](10-glossary.md): terminology
- [`VENDORED.md`](VENDORED.md): vendored/adapted code, upstream sources, licenses, and reuse boundaries
- [`integrations/topgrade.md`](integrations/topgrade.md): cross-manager orchestration boundary and custom-command configuration
- [`audit/`](audit/): V0.3 external-audit handoff, reproduction commands, evidence map, and known hypotheses
- [`adr/`](adr/): technical decisions that are not easily reversed
- [`records/`](records/): historical phase completion records
- [`specs/`](specs/): phase specifications

## When to maintain

- Command, flag, or environment-variable changes: update the root README, CLI
  reference, and dev setup.
- Data-model or safety-semantics changes: update requirements, data/API, the
  risk document, and the ADR.
- Phase-status changes: update the roadmap; mark something "verified" only after
  the verification evidence is complete.
- Any significant change: record the ruling in `09-decision-log.md`, and add or
  supersede an ADR when the decision is long-lived.
