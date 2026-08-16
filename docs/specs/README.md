# Specifications

This directory holds the phase-level contracts for hukou. The summaries below
give each spec's contract scope and current status. When a spec and the
current code disagree, the code is authoritative — record the resolution in
[`../09-decision-log.md`](../09-decision-log.md).

## [`phase1-scan.md`](phase1-scan.md) — `hukou scan`

- **Contract scope:** Walk every executable on `PATH`, attribute each binary to
  its install source (ownership), and print a table or JSON. Read-only and
  offline: Phase 1 performs no writes and no network access. Standard library
  only plus spf13/cobra; no archive or network libraries.
- **Status:** Implemented and in maintenance. Current verification evidence is
  tracked through the evidence map under [`../audit/`](../audit/).

## [`phase2-adopt-upgrade.md`](phase2-adopt-upgrade.md) — adopt / upgrade / rollback / doctor

- **Contract scope:** Adopt ownerless binaries found by `scan` into hukou
  management; provide GitHub-release upgrade, rollback, cooperative-transaction
  crash recovery, and read-only state diagnosis (`doctor`).
- **Status:** The core, plus the H2 crash-recovery and read-only doctor
  foundation, shipped and was verified with `v0.2.0`. This file preserves the
  v0.2 historical contract. The V0.3 private branch adds a narrow repair flow,
  activation history, and configurable retention; behavior changes there are
  described in [`v0.3-private-rc.md`](v0.3-private-rc.md).

## [`v0.3-private-rc.md`](v0.3-private-rc.md) — V0.3 private release candidate

- **Contract scope:** The "trust first" theme — explain first, preview first,
  then modify. Defines the target contract of the current private branch:
  narrow plan/apply repair, activation history, and public-readiness
  preparation done entirely inside the private repository.
- **Status:** Implemented; local/private RC readiness pass; the hosted CI gate
  is infrastructure-blocked (account billing). This is **not** a release or
  merge approval: the current official version is still `v0.2.0`, there is no
  v0.3 tag or GitHub Release, and repository visibility is unchanged.
