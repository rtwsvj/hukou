# Data and External APIs

## Data Root Directory

Priority order:

1. `HUKOU_DATA_DIR`
2. `${XDG_DATA_HOME}/hukou`
3. `$HOME/.local/share/hukou`

The UI locale is resolved in order: explicit `HUKOU_LANG` (`zh` / `zh-CN` /
`zh_CN` … for Chinese; `en` / `C` / `POSIX` for English; an unrecognized
explicit value falls back to English), then the system locale (`LC_ALL`,
`LANG`), then — on macOS only — the system GUI locale (`AppleLocale`, then
`AppleLanguages`) read from
`~/Library/Preferences/.GlobalPreferences.plist`, then English. The locale
affects human-facing text only (help, tables, reports, messages); JSON field
names, enum tokens, exit codes, and machine contracts stay English.
`HUKOU_LANG=en` pins English explicitly; on macOS `HUKOU_LANG` and the
`LC_*`/`LANG` variables keep priority over the GUI locale, and the GUI
locale is ignored when it cannot be parsed safely.

```text
<dataRoot>/
├── state.lock
├── manifest.json
├── manifest.json.bak
├── transactions/
│   ├── .building-<id>/
│   ├── pending-<id>/
│   └── completed-<id>/
└── store/
    ├── .tmp/
    └── <name>/
        ├── original/<binary>
        └── <tag>/<binary>
```

`state.lock` serializes adopt, real upgrade, and rollback on the same data
root. Write commands recover transactions first after acquiring the lock.
scan, list, doctor, and pure dry-run do not hold the write lock; when a
read-only path finds a pending state, it reports it or refuses to use hukou
attribution. The Darwin/Linux implementation uses non-blocking `flock` and
refuses to treat a pre-existing symlink as the lock file.

## Manifest schema

Top level:

```json
{
  "schema_version": 2,
  "retention": { "rollback_depth": 2 },
  "entries": []
}
```

Entry fields:

| JSON field | Meaning |
|---|---|
| `name` | Unique name within the manifest |
| `path` | Active file path on the user's PATH; new activations remain a regular file, with compatibility for restoring old symlink snapshots |
| `repo` | GitHub `owner/repo`; empty for local entries |
| `tag` | Currently active tag, or `local` / `original` |
| `sha256` | SHA-256 of the currently active binary; used for the local integrity gate |
| `upstream` | Derivable upstream module path |
| `adopted_at` | RFC3339 adoption time |
| `updated_at` | RFC3339 time of the most recent successful change |
| `asset_name` | Release asset name selected by the most recent successful upgrade |
| `asset_sha256` | SHA-256 of the downloaded archive itself; used for provenance auditing |
| `checksum_asset` | Name of the checksum asset actually used; omitted when absent |
| `checksum_verified` | Always serialized. `true` only when publisher checksum verification succeeded; `false` for local adopt, rollback, and explicit `--allow-unverified` bypass (must not be omitempty-swallowed, so the UNVERIFIED audit marker stays durable on disk and in the journal payload) |
| `archive_exe` | Executable name inside the release archive when it differs from the tool name; empty means the tool name itself. Upgrades select this member exactly, which keeps renamed adoptions upgradable without guessing among multiple candidates |
| `adopted_sha256` | Adoption fingerprint anchor: the SHA-256 of the binary at the moment it was adopted, equal by construction to the immutable original backup and never rewritten by upgrades/rollbacks. `doctor` compares the store's original backup against it and reports an error on mismatch (store tampering). Absent on entries adopted before the field existed |
| `active_activation_id` | Current activation event ID; must point to the last item in `activations` |
| `activations` | Immutable sequence of activation events, containing id/parent/operation/tag/SHA/time/reverts |
| `update_policy` | `mode`, `channel`, and optional `pinned_tag` |
| `retention` | Optional entry-level `rollback_depth`; inherits the top-level retention when absent |

`sha256` and `asset_sha256` are not the same object: after archive
extraction and renaming, the active binary's hash usually differs from the
downloaded asset's hash.

schema 0/1 is deterministically migrated to 2 on read: each entry creates
exactly one synthetic `legacy` root for its "current tag/SHA," policy uses
the compatibility mode `github-latest/stable`, and store mtime is never read
to guess old history. The decoder chooses allowed fields based on the
declared schema first: schema 0/1 files carrying v2-only top-level retention
or entry policy/history fields are rejected outright — legacy migration
cannot be used to smuggle in new semantics. Newly adopted entries default to
`semver/stable`. A file that declares schema 2 must explicitly carry
complete policy, retention, and valid lineage; unknown fields, future
schema, duplicate name/path, invalid digest/time/path/repo/policy,
forward/missing parent/reverts, and active/current mismatches are all
rejected. V0.2 binaries reject schema 2 — this is a compatibility gate that
prevents older versions from dropping fields when saving.

`manifest.Decode` is the strict boundary shared by the command transaction
encoder, doctor, and repair backup restore; none of the three paths may
implement its own loose JSON parsing. A backup that lacks explicit v2
policy/history, or that carries orphaned checksum evidence or unknown
fields, likewise cannot become a repair candidate. The current strict
decoder still relies on Go's JSON object semantics and does not reject
duplicate JSON keys; duplicate-key rejection is a future defense-in-depth
item, and the unknown-field check must not be read as already covering it.

### Activation lineage

- adopt creates a root event with no parent.
- upgrade creates a child whose parent is the current active event.
- rollback creates a new active event; its parent points to the target
  event's parent, and `reverts_id` points to the active event before the
  operation. So consecutive default rollbacks on `A→B→C` produce `B→A`, never
  back to C.
- An explicit original restore creates a rollback event with no parent,
  because schema v1 migration cannot prove that original belongs to the
  synthetic lineage; subsequent default rollbacks do not guess a
  destination.
- history/current are written together into the transaction's
  after-manifest, never committed in two separate steps.
- Every event tag must pass the safe single-path-component check; within
  the same history the same tag can only ever bind to the same SHA-256, and
  it fails closed if a historical or new event tries to reinterpret the same
  tag as different bytes.

### Update and retention policy

- The top-level default is `rollback_depth=2`; an entry may override it with any non-negative integer.
- `pinned_tag` takes priority over mode/channel; when the pin is already active, it can be a zero-network no-op.
- The Go update policy's SemVer mode accepts strict, deterministically
  orderable `X.Y.Z` (optionally with a lowercase `v`, valid
  prerelease/build), and rejects downgrades by default; the release/install
  shell, in contrast, requires a v-prefix and deliberately rejects build
  metadata. The two boundaries serve different purposes and each has its own
  valid/invalid matrix. `github-latest` is the legacy/non-SemVer
  compatibility mode.
- A Prune plan is bound to tag+SHA and protects current, the immutable
  original, any installed pin, and the most recent N logical ancestors. All
  protected/delete refs are re-verified before applying; versions that
  appear newly but aren't listed in the plan are left untouched. The command
  layer does not enter prune while a transaction is pending.

## Atomicity and Durability Boundaries

- The manifest uses a full same-directory temporary file, file sync,
  close, and rename, plus a parent sync; before overwriting, the previous
  parseable, schema-supported content is saved as `manifest.json.bak`. It
  fails closed if the main file or backup is a symlink or not a regular
  file; the backup still undergoes doctor's semantic audit for fields,
  duplicates, and hash format.
- The active binary uses a full temporary regular file inside the target
  directory, file sync, close, and rename, plus a parent sync; the PATH name
  is never left pointing at a symlink inode mid-swap.
- store's name/tag/original/`.tmp` subdirectories are resolved with
  exact-spelling matching; case aliases, symlinks, and non-directory
  intermediate components all fail closed, preventing writes or Prune from
  escaping the store trust root.
- store's mkdir, hard link, rename, remove, and GC all sync the affected directory after completing.
- Copies between store, snapshots, and the active file only guarantee
  bytes and rwx permission bits. owner/group, ACLs, xattrs, mtime,
  setuid/setgid/sticky and other special permission bits, and hardlink
  topology are not preserved; `adopt` rejects source files that carry
  privileged or special permission bits.
- H1 ordinary-error compensation and H2 WAL converge on the same target:
  PREPARED rolls back to before, a durable COMMIT rolls forward to after.
- The WAL covers hukou's cooperative writes and the real process
  interruption window; it makes no claim to repair unknown external drift,
  disk bitrot, or filesystem corruption. See
  [`08-risk-and-debt.md`](08-risk-and-debt.md).

## Transaction journal schema

The journal schema is independent of the manifest schema. `intent.json`
records the operation, name, every absolute resource path, and the
before/after topology, SHA-256, rwx mode, and journal-local payload. A
regular payload does not depend on the store still being intact at recovery
time; a legacy symlink before-state preserves the exact link target.

`COMMIT` is the only irreversible direction marker. Before cleanup, the
pending entry is atomically moved to the completed namespace, so that an
interrupted deletion cannot cause a committed transaction to be
misidentified as uncommitted.

## Repair plan and support report

A repair plan is a schema v1 JSON file at a path the user explicitly
specifies, containing the action, an opaque data-root identity,
preconditions, a state fingerprint, and the generation time; it does not
contain the absolute data root. The plan file is written atomically with
mode `0600`. apply does not invoke the implicit recovery used by ordinary
write commands; instead, it recomputes all bindings after taking the lock on
the existing data root, and fails closed on stale/ambiguous/unknown drift.
apply may create/use `state.lock`, so the "stale" guarantee is zero writes
to business state such as live/store/manifest/backup/journal — not zero
writes to the filesystem overall.

A support report is a separate schema v1 JSON: build, OS/arch, redacted
doctor findings, anonymized entry policy/history counts, and
transaction/store topology. It does not contain entry name/tag/path/repo/
upstream/asset/event ID, environment variables, username, HOME, binaries, or
WAL payload. `--format json` writes to stdout; `--output` writes exactly one
`0600` file; neither one connects to the network or uploads anything.

## GitHub API

- metadata: legacy stable uses `GET /repos/{owner}/{repo}/releases/latest`;
  exact pin uses `/releases/tags/{tag}`; SemVer/prerelease uses a bounded
  `/releases?per_page=100&page=N`.
- The release list reads at most 10 pages (1000 items) and does not
  follow the response `Link` header to other hosts; it fails closed when the
  safety cap is exactly filled and completeness cannot be proven.
- Token: `GITHUB_TOKEN` takes priority, then `GH_TOKEN`.
- Only API requests carry Authorization; downloads and redirects are isolated by a host allowlist.
- 429, 5xx, and network errors are retried with backoff a limited number of times; 429 respects a capped `Retry-After`.
- Both API calls and downloads have an overall timeout.
- Downloads carry a throughput watchdog: below `HUKOU_DOWNLOAD_MIN_SPEED`
  (default 32 KiB/s) past `HUKOU_DOWNLOAD_GRACE` (default 5 s) the download
  is considered stalled. A stalled or transport-failed download is retried
  once through the system proxy on macOS (the standard
  `HTTPS_PROXY`/`HTTP_PROXY`/`ALL_PROXY` variables are honored everywhere; on
  macOS the enabled NetworkServices proxy is used when none of them is set —
  HTTPS preferred, then HTTP, then SOCKS). HTTP 4xx/5xx responses,
  size-limit refusals, and checksum failures are never retried through the
  proxy.
- A downloaded asset uses a 512 MiB cap when the API size is unknown; when
  the API declares a positive size, that declared value is currently used as
  the exact length and read limit, with no independent global ceiling yet.
  The single selected extraction entry has a 512 MiB cap, but the tar
  target-selection first full-stream scan has no total expansion
  work/member-count budget yet.

The GitHub API JSON response currently has no independent body byte cap;
adding one is a future defense-in-depth item. Similarly, the install script
checks the archive root and target member uniqueness, but a total expansion
size/member-count budget is still a future item. Path safety currently
relies on per-component validation and re-checking; adopting
`openat`/directory-fd-anchored traversal to further narrow the
non-cooperative TOCTOU window has also not been implemented yet.

## Checksum Semantics

1. First look for the release's checksum manifest or `<asset>.sha256[sum]`.
2. A generic manifest accepts both the GNU coreutils and BSD `SHA256
   (file) = hash` forms; an exactly-bound sidecar may also contain just a
   single 64-hex-digit digest.
3. Once a checksum asset is found, the selected asset must have a valid,
   matching entry; a missing entry, invalid format, or mismatch all abort.
   `--allow-unverified` does **not** bypass a present-but-unusable checksum.
4. When the release has **no** checksum asset at all, upgrade **refuses by
   default** (fail-closed) with an error that points at `--allow-unverified`.
   With the explicit bypass: `asset_sha256` is still computed and saved,
   stdout/stderr print a loud `UNVERIFIED` marker, and the manifest/journal
   persist `checksum_verified: false` (never falsely set to true).
5. The embedded upgrade path inside `hukou up` does not expose the bypass
   flag and always fails closed when a publisher checksum is absent.
6. The manifest is only saved once both activation and audit fields are ready.
