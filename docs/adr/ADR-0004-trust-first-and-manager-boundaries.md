# ADR-0004: Trust-First CLI and Manager Ownership Boundaries

- Status: Accepted
- Date: 2026-07-14
- Implementation: fixed subject `1fa45a0` passed the recorded local/private RC gate;
  external audit and hosted execution remain pending

## Background

hukou can already scan, adopt, upgrade, and roll back, but a new user lacks an explainable entry point for "why" and "what will happen" before the first write operation. At the same time, "upgrade the whole machine in one go" has viral appeal, but it would mix together the different semantics and failure responsibilities of Homebrew, mise, aqua, npm, Cargo, GUI, and system updates.

## Decision

1. V0.3 adds `explain`, `adopt --dry-run`, and `outdated`, forming a trust ladder of explain first, preview first, modify last.
2. All read-only reports use an independent schema, keeping deterministic English JSON.
3. A dry-run result is not authorization for a write operation; an actual write must re-check the live state inside the state lock.
4. `upgrade --all` only processes hukou manifest entries.
5. Whole-machine upgrades are integrated via a Topgrade custom command; hukou does not perform upgrades for other managers.
6. V0.3 does not ship a cross-manager plan/apply with incomplete semantics.

## Current Implementation

- `explain`, `adopt --dry-run --json`, and `outdated` are wired up; outdated and
  upgrade use a shared update checker, and write operations still re-check inside
  the lock.
- The help text and implementation of `upgrade --all` only enumerate manifest
  entries; see [`../integrations/topgrade.md`](../integrations/topgrade.md) for
  the Topgrade configuration.
- A Topgrade failure only indicates that one independent custom command failed;
  hukou does not take over retry, repair, or rollback for other managers.

The above is the current state of the branch's implementation, not a claim that V0.3 has shipped or that remote CI is green.

## Consequences

- Users can obtain value first without trusting hukou's writes.
- hukou's strong transaction and rollback guarantees only cover entries it owns, and are not diluted by external managers.
- Topgrade provides a ready-made "upgrade everything with one command" entry point, and hukou fills the gap for unmanaged binaries.
- If a control plane is implemented in the future, it must be defined in a separate ADR, using provider capabilities, fixed plans, and Saga/reconcile, without claiming a global atomic transaction.
