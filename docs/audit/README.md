# External audit package

## Purpose

This directory lets a third-party reviewer audit V0.3 without relying on the
author's Codex conversation or local machine. It deliberately separates
claims, reproducible evidence, recorded-but-not-portable evidence, and known
gaps.

## Canonical objects

| Name | Value |
|---|---|
| Base | `bd4faa32d9b5b604b1b224f97fe891ed670f3742` |
| Code subject | `1fa45a0d8473446e3208490f037aef924abea181` |
| Original evidence-docs commit | `2cd0467098700b899b8b87ee627eb2b75412f397` |
| Audit-package content baseline | `b60e890a6a5b9bbb9ad8f18bd96c8fbdf7b6139e` |
| Review-time documentation head | Capture `origin/codex/hukou-v0.3-private-rc` after fetching and record its full SHA |
| Branch | `codex/hukou-v0.3-private-rc` |
| Draft PR | <https://github.com/rtwsvj/hukou/pull/6> |
| Official release | `v0.2.0` |
| V0.3 tag/Release | None |

The branch is only a transport. Pin every review result to a full commit SHA.

## Package map

- [`v0.3-private-rc-handoff.md`](v0.3-private-rc-handoff.md): access,
  checkout, environment, tiered commands, evidence handling, and result format.
- [`v0.3-claims-evidence.md`](v0.3-claims-evidence.md): C1-C10 source,
  test, command, and caveat map.
- [`v0.3-review-checklist.md`](v0.3-review-checklist.md): prioritized
  security, data-integrity, release, privacy, and portability review questions.
- [`v0.3-artifact-and-platform-evidence.md`](v0.3-artifact-and-platform-evidence.md):
  recorded hashes, availability, native-run/build distinctions, and toolchain caveats.
- [`external-finding-template.md`](external-finding-template.md): copyable
  finding and final-report structure.
- [`../specs/v0.3-private-rc.md`](../specs/v0.3-private-rc.md): intended V0.3 contract.
- [`../03-architecture.md`](../03-architecture.md) and
  [`../04-data-and-api.md`](../04-data-and-api.md): implementation and state boundaries.
- [`../08-risk-and-debt.md`](../08-risk-and-debt.md): maintained risk register.

## Evidence classes

| Class | Example | Reviewer treatment |
|---|---|---|
| Repository fact | code, tests, workflow, Git object | Inspect directly at the pinned SHA |
| Reproducible command | `go test`, `make release-verify` | Rerun and retain raw output |
| Hosted fact | PR state, Actions run, repository visibility | Refresh from GitHub; handoff values may age |
| Recorded local evidence | prior Linux container run, local archive hashes, local SBOM | Treat as a claim until independently reproduced |
| Out-of-scope assertion | legal compliance, public user adoption | Do not infer from engineering tests |

## Fact-source order

1. Pinned source and tests.
2. Independent reproduction output.
3. Git/GitHub immutable objects and refreshed hosted state.
4. Maintained specifications and ADRs.
5. Original Codex change and verification records.
6. Historical `docs/records/*-DONE.md` files.

If two sources disagree, report the disagreement instead of silently choosing
the more favorable one.

The original `pinhaoma-review` was performed inside the Codex execution team
and has no standalone raw finding log. It is not an independent external audit.
