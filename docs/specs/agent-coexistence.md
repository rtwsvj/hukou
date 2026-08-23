# Spec: Agent Coexistence Protocol (draft r1)

Status: **draft for review**. This spec defines how hukou covers the adoption,
upgrade orchestration, and rollback lineage of CLI agents that ship their own
self-updaters — without becoming a second, competing writer to those binaries.

## Problem

Modern CLI agents (opencode, kimi, grok, hermes, omp, pi, openclaw, …) mostly
install outside package managers and many ship native self-updaters. Today
hukou has exactly two answers and both are all-or-nothing:

1. **Adopt + upgrade**: hukou becomes the only writer. A native updater that
   later replaces the live file trips drift detection
   (`internal/updatecheck/check.go:88-98`, `StatusDrifted`) and every
   subsequent upgrade fails closed until manual repair. Two writers, one file.
2. **Don't adopt**: the tool is invisible to `hukou up`'s internal step, gets
   no snapshot diff, no lineage, no rollback.

Neither matches reality: the operator wants the agent's own update channel to
keep working **and** wants hukou's observability, inventory diff, and rollback
lineage around it.

## Design principles

- **P1 — One writer per file.** For any adopted path, exactly one of
  {hukou upgrade, native updater} performs replacements in a given run. Which
  one is a per-entry policy decision, never an accident.
- **P2 — hukou observes and verifies; it does not race.** Reconciliation of an
  externally updated file is a hash-and-attribute operation. Reconciliation
  never executes the drifted file and never writes to it.
- **P3 — Fail closed stays the default.** Every new mechanism that accepts
  external change requires positive evidence (publisher-checksum match) and an
  explicit per-tool opt-in. Unattributed drift is still refused.
- **P4 — Same trust machinery.** Native updaters invoked through hukou run
  under the same constraints as built-in managers: argv-only commands, env
  allowlist with the secret denylist, timeouts, process-group termination,
  before/after snapshot diff.

## Mechanism 1 — Delegated managers (user-defined `up` participants)

`hukou up` gains user-registered participants loaded from
`${XDG_CONFIG_HOME:-~/.config}/hukou/managers.json`. JSON, not TOML: hukou
reads zero config files today and go.mod is deliberately stdlib-plus-cobra;
reusing `encoding/json` with the existing strict-decode pattern
(`DisallowUnknownFields`, trailing-value rejection) keeps that property and
yields unknown-field rejection for free:

```json
{
  "managers": [
    {
      "name": "omp",
      "detect": "omp",
      "commands": [
        ["omp-upgrade", "--yes"]
      ],
      "timeout": "10m"
    },
    {
      "name": "opencode-native",
      "detect": "opencode",
      "commands": [
        ["opencode", "upgrade"]
      ],
      "timeout": "10m"
    }
  ]
}
```

Contract:

- Loaded before plan construction, validated like the static registry:
  unique names, non-empty argv slices, detect binary PATH-resolvable,
  timeout parseable within the hard cap (30m), no collision with built-in
  manager names, and `Internal: true` rejected outright (the internal step
  is singular and unforgeable). The `Manager` struct grows a `Timeout`
  field; per-command timeout resolution replaces the single global
  `up --timeout` default for delegated rows. Invalid file → `up` refuses
  with the offending entry named (fail closed, same class as `--only`
  validation).
- The config file must be a regular file (no symlink), owned by the euid, and
  not group/other-writable — the same trust gate as the data root.
- Delegated managers participate in the existing plan/fence/guard machinery
  (`internal/orchestrate/plan`, execution fence tests), get run.json entries,
  and are covered by the pre/post snapshot diff.
- Ordering: delegated managers run **before** hukou's internal
  `upgrade --all` step, so reconciliation (Mechanism 2) sees the
  post-native-update reality inside the same run and one snapshot pair covers
  both.

This alone satisfies "cover the agents without breaking their updaters" at the
orchestration level: the native updater stays the writer; hukou adds
scheduling, env hygiene, timeouts, kill safety, and the inventory diff.

## Mechanism 2 — Drift reconciliation (`policy set drift=adopt`)

New per-entry policy knob next to mode/channel/pin:

```text
drift: refuse (default) | adopt
```

- `refuse` preserves today's behavior exactly (StatusDrifted fail-closed).
- `adopt` changes the upgrade pre-flight: when the live file's SHA-256 no
  longer matches the manifest, hukou attributes the new bytes before doing
  anything else:
  1. Hash the live file.
  2. Query the entry's repo releases. Ordering note: today the release list
     is fetched *after* the drift gate (`internal/updatecheck/check.go:88-98`),
     so reconciliation reorders the flow — releases are fetched before the
     drift decision when `drift=adopt` — or re-enters `selectRelease` after
     attribution.
  3. If the live hash equals the publisher checksum of exactly one release
     asset for this platform → record an activation lineage event whose tag
     is the *real matched release tag* (lineage rules forbid sentinel tags:
     baseline sentinels local/adopted/original exist only in versionpolicy,
     and "original" is store-reserved; activation tags must pass
     ValidateActivationTag), with `checksum_verified: true` and an actor
     marker identifying the external updater, then re-baseline the manifest
     entry and continue the normal upgrade flow (which may be a no-op or a
     real upgrade). Lineage immutability applies unchanged: a tag binds to
     one SHA-256 within a lineage, so a native update to a tag hukou already
     recorded at different bytes fails closed and stays `drift=refuse`
     behavior until repaired — by design, that is a republish, not an
     update.
  4. If the bytes match no published asset → refuse, exactly as `refuse`
     does. Unattributed drift is potential tampering, not an update.
- Reconciliation is pure hashing + API reads: it never executes the drifted
  file and never writes to it (P2).
- Every reconciliation prints a visible line and lands in `doctor receipt` so
  the audit trail shows who moved the file.
- Store hygiene: the reconciled bytes are ingested via the normal immutable
  store path (`Put`), so rollback targets keep growing through native updates
  too. `rollback --to original` and lineage walks keep working.

Security analysis: the worst case is that a native updater installed attacker
bytes that happen to match a *publisher-signed* checksum — which is exactly
the trust level of a normal hukou upgrade. The mechanism adds no new trust
anchor.

## Mechanism 3 — Updater-aware adoption

`adopt` grows a detection pass (provenance-adjacent, static only):

- companion binaries on PATH: `<name>-upgrade`, `<name>-up`, `<name>_upgrade`;
- `--self-update` / `upgrade` subcommand in `--help` output is NOT probed at
  adopt time (no execution of the candidate binary); detection uses file
  presence and package metadata only;
- well-known config markers in the tool's config directory
  (`auto_update`, `autoUpgrade`, `selfUpdate` keys), read-only.

When a live self-updater is detected, adopt says so and recommends the
coexistence preset:

```console
$ hukou adopt opencode sst/opencode
note: opencode ships its own updater (opencode upgrade).
      recommended: hukou policy set opencode drift=adopt
      (hukou will verify and record its updates instead of fighting them)
Adopted opencode (v0.4.0) at ~/.opencode/bin/opencode
```

`doctor` grows a check: entries with `drift=refuse` whose tool has a detected
live updater companion → warning "two writers for <name>: disable one".

## Mechanism 4 — Observe-only adoption (inventory without management)

For tools with no usable release channel (private builds, e.g. omp today):

```sh
hukou adopt omp --observe          # baseline + lineage, upgrade never touches it
```

- `policy mode=observe`: excluded from `upgrade --all` and from the internal
  `up` step; still inventoried in scan/list, still snapshotted, still
  drift-reported (informational, never blocking), still rollback-capable via
  the adoption-time original.
- Combined with Mechanism 1 this covers "omp upgrades itself via omp-upgrade;
  hukou schedules the updater, snapshots the change, and keeps a rollback
  anchor" without hukou ever needing a public repo for it.

## Decision matrix

| Tool situation | Preset |
|---|---|
| npm/brew-owned (pi, openclaw, grok#2) | no adopt needed; manager already covers them. Optional adopt with `drift=adopt` for rollback lineage. |
| Public repo + checksums + native updater (opencode, kimi, …) | adopt + `drift=adopt` + (optionally) register native updater as delegated manager; OR disable native updater and let hukou upgrade directly. Pick one writer. |
| Public repo + checksums, no native updater | classic adopt; hukou is the writer. |
| Public repo, no checksums | classic adopt + explicit `--allow-unverified` decision per tool (loud, durable UNVERIFIED markers), or don't adopt. |
| No public channel (omp today) | `--observe` adopt + delegated manager for its native updater. |

## Non-goals

- No auto-adoption. `scan`/`up` never adopt anything without an explicit
  `adopt` command; the "automatic" part is that adopted + delegated tools then
  flow through every `hukou up` without further interaction.
- No execution of candidate binaries during detection or suggestion.
- No change to the manager-ownership boundary of ADR-0004: delegated managers
  are still managers; hukou's internal step still touches only adopted
  entries.

## Implementation plan (phased)

1. **P-A (this branch's follow-up): policy plumbing** — `drift` and `observe`
   policy fields added to the `UpdatePolicy` struct (mandatory: manifest
   decoding uses DisallowUnknownFields, so unknown fields fail hard — the
   field must exist for v2 files carrying it), additive + omitempty +
   normalize-defaulted per ADR-0005 compatibility rules; validated in all
   three gates (validateDecodedV2, validateEntry, versionpolicy.Normalize);
   legacyEntry must NOT gain it (0/1 migration keeps producing compat
   policy). Hook: updatecheck Check drift branch + upgradeOne pre-flight;
   doctor receipt fields; policy show/set flags; report schemas stay
   independently versioned (additive). Tests.
2. **P-B: delegated managers** — managers.json loader (strict decode +
   trust gate), Manager.Timeout field + executor plumbing, registry merge
   before plan construction in up/up_plan so delegated rows flow through the
   existing plan/fence/guard machinery (fence tests need dynamic-manager
   fixtures), env denylist inheritance, run.json/diff coverage. Because
   ADR-0004 routes any new control plane to its own ADR, P-B ships with
   **ADR-0007: user-defined delegated managers** (capabilities, validation
   contract, no global-atomicity claim). Tests.
3. **P-C: updater-aware adopt + doctor two-writer check** — static detection,
   adopt note, doctor warning, tests.
4. **P-D: docs** — CLI reference, SECURITY.md threat-model note (reconciliation
   trust analysis), topgrade integration note.

Each phase is independently shippable; P-A alone converts "adopted agent
bricked by drift" into "adopted agent reconciles".

## Open questions

- Should `drift=adopt` reconciliation also apply when `rollback` encounters a
  drifted live file (today it refuses)? Proposal: yes, same attribution rule.
- managers.json: support per-manager env passthru? Proposal: no for r1 —
  the allowlist + denylist is the contract; exceptions invite the EO-1 class
  of leak back.

## Appendix A — Machine survey (2026-08-22, this workstation)

Facts gathered read-only (binary strings, live configs, GitHub/npm registry
API). Auto-updaters currently **ON** are marked ⚠.

| Tool | Upstream | Native updater | Disable switch | Checksum source | Preset |
|---|---|---|---|---|---|
| opencode (~/.opencode) | anomalyco/opencode | ⚠ autoupdate config key; patch releases auto-install | `OPENCODE_DISABLE_AUTOUPDATE=1` (presence alone skips the whole check) or `"autoupdate": false` | GitHub API per-asset `digest` (no checksums.txt asset) | adopt + `drift=adopt`, updater kept |
| grok 1.0.5 (~/.local/bin) | xAI (closed; x.ai CDN) | ⚠ `~/.grok/config.toml` `auto_update = true` today | `GROK_DISABLE_AUTOUPDATER=1`, `[cli] auto_update = false`, or `--no-auto-update` | **none published** | adopt requires explicit `--allow-unverified` decision, or observe-only |
| grok 0.1.4 (/opt/homebrew) | stale npm generation | notify-only wrapper, no updater | — (remove: `npm rm -g @xai-official/grok`; shares ~/.grok state with 1.0.5) | npm dist.integrity | remove (PATH ambiguity, not adopt) |
| hermes (~/.hermes) | NousResearch/hermes-agent | git-pull manual only; 6h notify banner; no auto-install | nothing to disable (`updates:` schema has backup knobs only) | none (source tree; pin by git SHA not file hash) | observe-only + delegated manager if desired |
| omp (~/.local/bin) | can1357/oh-my-pi | ⚠ `startup.checkUpdate` default true; `marketplace.autoUpdate` notify/auto; local shim omp-upgrade execs `omp update` | `startup.checkUpdate: false`, `marketplace.autoUpdate: off` in ~/.omp/agent/config.yml; neutralize the shim | SHA256SUMS.txt + API digests | adopt + `drift=adopt`, updater kept |
| pi (/opt/homebrew, npm) | earendil-works/pi | manual `pi update` shells to owning package manager; notify-only banner | nothing auto to disable | SHA256SUMS + API digests | npm manager covers it; optional adopt for lineage |
| openclaw (/opt/homebrew, npm) | openclaw/openclaw | daemon auto-updater exists but **default OFF** (`update.auto.enabled`) | keep `update.auto.enabled` absent/false | none (npm is the artifact channel; dist.integrity sha512) | npm manager covers it |

### Design implication: API-digest verification

GitHub's release API returns a server-computed `digest` (sha256) for every
asset — confirmed present for opencode, oh-my-pi, kimi-code and pi. It is
served from the same origin under the same TLS as the release metadata
hukou already trusts, and it is exactly as resistant to repo-write compromise
as a publisher checksums.txt (both are attacker-forgeable with repo access).
hukou should therefore accept *either* a checksum asset **or** the API
`digest` field when deciding "publisher checksum present" — this unlocks
fail-closed adoption for opencode without any publisher-side change.
Implementation note: `ghrelease.Asset` gains the digest field;
`resolvePublisherChecksum` treats a matching API digest as
`checksum_verified: true` with `checksum_asset: "(api-digest)"` recorded in
the audit trail. Same-origin honesty rule stays intact: the digest must come
from the same API response as the asset URL, never fetched separately.
