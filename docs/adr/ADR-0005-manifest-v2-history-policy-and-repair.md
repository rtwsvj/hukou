# ADR-0005: Manifest v2, Activation History, Policy, and Narrow-Scope repair

- Status: Accepted
- Date: 2026-07-14
- Implementation: fixed subject `1fa45a0` passed the recorded local/private RC gate;
  external audit and hosted execution remain pending

## Background

schema v1 only records the current state. Default rollback relies on store directory mtime, and consecutive operations bounce back and forth between the most recent directories; a fixed retention count does not know which artifacts are genuine historical targets; upgrade only compares tag strings; and although doctor can report anomalies, there is no verifiable, replayable repair protocol with clearly defined boundaries.

## Decision

1. The manifest is upgraded to schema v2, recording activation lineage and update/retention policy within each entry.
2. schema 0/1 migration only generates a synthetic root for the current state, without guessing old history.
3. History and the current entry go into the same after-manifest, and are committed together by the existing before/after WAL.
4. Default rollback follows parent lineage; retention builds a protection set from lineage, pin, and transaction references, without using mtime.
5. The update policy explicitly distinguishes SemVer from legacy GitHub latest; it supports stable/prerelease and exact pin, and does not downgrade by default.
6. doctor remains read-only. repair uses `plan → apply`, bound to a live-state fingerprint; V0.3 only exposes transaction recovery and manifest backup restore.
7. The support bundle is anonymized by default, has no network access, and is not auto-uploaded.

## Current Implementation

- schema 0/1 is migrated in memory into a synthetic legacy root; schema 2 load/save
  performs strict validation of unknown fields, policy, retention, digest/time/path,
  and lineage. V0.2 rejects schema 2.
- New entries default to `semver/stable`; legacy migration uses
  `github-latest/stable`. SemVer comparison uses the locked and
  license-recorded `golang.org/x/mod/semver`. When explicitly switching to
  semver, entries whose local or current tag is not strict SemVer are rejected,
  to avoid discovering only after the policy is saved that there is no
  sortable baseline.
- rollback follows parent lineage; an explicit original restore creates a
  parent-less event, avoiding guessing at legacy lineage. Prune's two phases are
  bound to tag+SHA, and it skips when the transaction is not clean.
- repair only implements `recover-transaction` and `restore-manifest-backup`;
  the plan is written to a 0600 file explicitly specified by the user, and apply
  holds the lock while recomputing identity/fingerprint/preconditions.
- The support report uses anonymized entry ordinals, enums, and counts, and
  does not copy path/repo/name/tag, environment variables, usernames,
  binaries, or WAL payloads.

These implementations already have internal acceptance records from the fixed-commit whole-repo, container, and release snapshot testing; external audit and the GitHub-hosted gate remain unclosed. ADR Accepted only means the design decision has been adopted; it does not mean external audit has passed or that the project has been publicly released.

## Consequences

- V0.2 rejects schema v2; this is a deliberate compatibility gate to prevent old versions from silently dropping fields.
- migration, history, policy, retention, and repair all become safety-critical paths, and must be included in the crash/fault matrix.
- repair-all, directory quarantine, history compression, and self-service upload are left for future versions.
