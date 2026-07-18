# hukou

**The household registry for your stray binaries — find, adopt, upgrade, and roll back the CLI tools no package manager owns.**

[![CI](https://github.com/rtwsvj/hukou/actions/workflows/ci.yml/badge.svg)](https://github.com/rtwsvj/hukou/actions/workflows/ci.yml)
[![CodeQL](https://github.com/rtwsvj/hukou/actions/workflows/codeql.yml/badge.svg)](https://github.com/rtwsvj/hukou/actions/workflows/codeql.yml)
[![Release](https://img.shields.io/github/v/release/rtwsvj/hukou?sort=semver)](https://github.com/rtwsvj/hukou/releases)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)

[简体中文](README.zh-CN.md) · [Documentation](docs/README.md) · [Security](SECURITY.md) · [ADRs](docs/adr/)

---

Every developer machine accumulates binaries that no package manager tracks: a
release you grabbed from GitHub, a `go install` build, a binary copied off
another laptop, something a `curl … | sh` installer dropped into `~/.local/bin`.
`brew`, `mise`, `npm`, and friends do a fine job with what they installed —
everything else is out of scope for them. Those files are the ones you forget
about, never upgrade, and are afraid to overwrite by hand.

hukou (户口, *"household registry"*) is the tool for that gap. It walks your
`PATH`, attributes each executable to a known origin — or tells you honestly
that it is unknown — and gives the strays a registered upgrade-and-rollback
path they never had.

```
topgrade  orchestrates your managers
mise      installs new tools
hukou     adopts the strays nothing else owns
```

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/rtwsvj/hukou/main/scripts/install.sh | sh
```

The installer downloads the latest release archive, verifies it against a
same-origin `checksums.txt`, and — when an authenticated [`gh`](https://cli.github.com/)
CLI is present — verifies the archive's GitHub build-provenance (Sigstore)
attestation against an anchored signer identity before unpacking. Set
`HUKOU_REQUIRE_ATTESTATION=1` to make that check mandatory instead of a
warn-and-fall-back-to-transport-trust default. Prefer to see it first? Add
`--dry-run`, or download the archive and `checksums.txt` from the
[releases page](https://github.com/rtwsvj/hukou/releases) yourself.

## Quick start (30 seconds)

hukou is deliberately trust-first: the commands that *read* come before the
commands that *write*, and the heavyweight writes can be previewed first —
`adopt --dry-run`, `upgrade --dry-run`, and `repair plan`. (`rollback` and
`policy set` have no dry-run; they apply immediately.)

The session below is a real, unedited run in a demo sandbox with one stray
`rg` on `PATH`.

**1. See who owns what on your `PATH`** (local, read-only, no network):

```console
$ hukou scan --unknown-only
NAME  PATH                    KIND    SOURCE   PACKAGE  VERSION  SHADOWED  EVIDENCE
rg    /tmp/quickstart/bin/rg  Script  unknown  rg                          no prior detector matched; realpath=/private/tmp/quicksta...

summary: total=1 sources=1 unknown=1 shadowed=0 skipped=1
by source: unknown=1
```

**2. Adopt a stray.** Point it at a GitHub repo (or use `--local` for a binary
with no upstream). hukou preserves the original bytes as an immutable backup:

```console
$ hukou adopt rg BurntSushi/ripgrep --tag 15.1.0
Adopted rg (15.1.0) at /tmp/quickstart/bin/rg
repo: BurntSushi/ripgrep
```

**3. Upgrade it.** Preview first — `--dry-run` never touches the file — then
apply. hukou selects the platform asset, verifies the publisher checksum when
the release publishes one (see [Security model](#security-model)), and replaces
the live file with an atomic rename:

```console
$ hukou upgrade rg --dry-run
Would upgrade rg: 15.1.0 -> 15.2.0 using asset ripgrep-15.2.0-aarch64-apple-darwin.tar.gz (higher eligible semantic version is available)

$ hukou upgrade rg
Upgraded rg: 15.1.0 -> 15.2.0
```

**4. Roll back** if the new version misbehaves. hukou retains prior versions
and the adoption-time original:

```console
$ hukou rollback rg
Rolled back rg to 15.1.0
```

That is the whole loop: `scan → adopt → upgrade → rollback`. `hukou doctor`
audits the resulting state at any time without writing a byte.

![hukou demo](assets/demo.gif)

## Why not topgrade / mise / eget?

They are good tools. hukou does not replace them — it fills the slot they leave
open. Those tools are built around software **from the moment they install it
forward**; hukou is built for **retroactive adoption at scale** — scan the whole
`PATH`, attribute provenance with recorded evidence, then give the adopted
strays lineage-tracked upgrades and rollback.

| Tool | What it manages | The gap hukou fills |
|---|---|---|
| **Homebrew / mise / aqua** | Tools *they* installed, going forward | Pre-existing binaries. `mise link` can hand-register one external tool at a time, but there is no `PATH`-wide scan, no provenance attribution, and no lineage-tracked rollback |
| **topgrade** | Orchestrates other upgraders in one command | Has nothing to hand the strays; hukou plugs in as one custom command |
| **eget / stew** | Fetching GitHub releases into place, with checksum verification (and, for stew, a lockfile) | No detection or adoption of binaries *already* on disk, and no activation-lineage rollback of what got replaced |
| **hukou** | **Existing** unmanaged binaries: attribute → adopt → upgrade → roll back | — |

hukou composes with topgrade rather than competing with it. Register it as a
custom command and your "upgrade everything" run includes your strays:

```toml
# topgrade.toml
[commands]
"hukou adopted CLI tools" = "hukou upgrade --all"
```

`upgrade --all` means *entries hukou adopted* — never another manager's
territory. See [docs/integrations/topgrade.md](docs/integrations/topgrade.md)
for the ownership boundary.

## Features

- **Provenance by evidence, not guesswork.** A chain of 24 source detectors
  (Homebrew, MacPorts, mise, asdf, rustup, cargo, npm, pnpm, yarn, bun, pipx,
  uv, pip, gem, nix, volta, deno, dotnet, composer, krew, `curl … | sh`
  installers, macOS app bundles, local builds, Go build info), closed out by
  hukou/system/unknown terminal attributions, attributes each binary and
  records *why*. Go binaries are traced straight from their embedded build
  info — which also means the go detector claims a `go install`ed binary as
  owned, so adopting one takes an explicit `--force`, after which `adopt`
  infers `owner/repo` from the build info with no extra flags.
  See [`hukou scan`](docs/05-cli-reference.md).
- **Crash-safe, transactional replacement.** Live binaries are swapped through a
  same-directory temp file plus atomic rename, and a durable write-ahead log
  records before/after state. A process killed mid-upgrade recovers
  deterministically — roll a prepared transaction back, roll a committed one
  forward, and fail closed on unknown external drift.
  See [ADR-0002](docs/adr/ADR-0002-regular-file-activation.md) and
  [ADR-0003](docs/adr/ADR-0003-crash-recovery-and-doctor.md).
- **Multi-version store with real rollback.** Every adopted tool keeps its
  immutable adoption-time original plus retained versions. `rollback` follows a
  recorded activation lineage — logical parents, not directory timestamps.
  See [ADR-0005](docs/adr/ADR-0005-manifest-v2-history-policy-and-repair.md).
- **Supply-chain-aware installs.** When a release publishes a checksum asset,
  a missing, invalid, or mismatched entry for the selected asset fails closed;
  when the release has no checksum asset at all, hukou warns loudly and records
  the downloaded asset's SHA-256 rather than pretending it verified anything.
  Downloaded-asset and activated-binary hashes are tracked separately, and the
  installer verifies GitHub build-provenance attestation against an anchored
  signer identity. See [SECURITY.md](SECURITY.md).
- **Read-only diagnosis you can trust.** `doctor` takes no lock, writes nothing,
  and makes no network call by default. It reports problems; it never silently
  deletes or "repairs" your data. Repair is a separate, explicit `plan → apply`
  workflow bound to a state fingerprint.
- **Policy-aware updates.** Per-tool SemVer vs. GitHub-latest, stable vs.
  prerelease, exact pins, and rollback-retention depth — inspected with
  `policy show`, changed atomically with `policy set`.
- **Privacy-preserving support bundles.** `support bundle` produces an offline,
  redacted JSON diagnostic — no paths, repo names, usernames, env vars, or WAL
  payloads — and never uploads itself.

## Command surface

| Command | Writes? | Network? | Purpose |
|---|---|---|---|
| `scan` | no | no | Inventory `PATH` and attribute every executable |
| `explain <name\|path>` | no | no | Show which binary wins on `PATH` and who owns each match |
| `adopt <name\|path> [owner/repo]` | yes | no | Register a binary; `--dry-run` previews, `--local` skips the repo |
| `list` | no | no | List adopted tools and retained-version counts |
| `outdated [name…]` | no | yes | Check for newer releases without downloading |
| `upgrade [name…]` | yes | yes | Verify and replace; `--dry-run` previews, `--all` for every entry |
| `rollback <name>` | yes | no | Activate a retained version; `--to <tag>` or `--to original` |
| `up` | no (dry-run only in this release) | no | Plan a full-machine upgrade across known managers; `--dry-run` prints the exact commands and an inventory summary, real execution lands in a later release (exit 2 placeholder) |
| `policy show/set` | show: no; set: manifest | no | Inspect or atomically change update/rollback policy |
| `doctor` | no | no | Audit manifest, store, journal, and live files; `--deep`, `--json` |
| `repair plan/apply` | plan: plan file only; apply: manifest, live binary, transaction state (may quarantine journal residue) | no | Fingerprint-bound recovery of unfinished transactions or a manifest backup |
| `support bundle` | writes bundle only | no | Redacted, offline diagnostic |

Full flags and side effects: [docs/05-cli-reference.md](docs/05-cli-reference.md).
Most read-only commands take `--json` for scripting.

## Platform support

| OS | Arch | Status |
|---|---|---|
| macOS | arm64 | Daily-driven primary target; exercised on a real machine |
| macOS | amd64 | Cross-compiled release build; no runtime testing yet |
| Linux | amd64 | Cross-compiled release build; release-archive smoke checks |
| Linux | arm64 | Cross-compiled release build; no runtime testing yet |
| Windows | — | Not supported |

hukou does not treat "it cross-compiles" as "it is supported." macOS arm64 is
where it runs daily; the other targets are cross-compiled release builds, with
release-archive smoke checks on Linux amd64. Independent verification on real
hardware is welcome — please open an issue with what you saw.

## Security model

hukou rewrites executable files and stores rollback material, so integrity,
path handling, archive extraction, credential handling, and crash recovery are
all part of its security boundary. The design bias is simple: **prefer a visible
refusal over a clever guess.**

- When a checksum asset exists, a missing, invalid, or mismatched entry for the
  selected asset fails closed — hukou will not "probably it's fine" its way past
  a verification gap. A release with no checksum asset at all proceeds with a
  loud warning, and the downloaded asset's SHA-256 is recorded either way.
- Downloaded-asset hashes and activated-binary hashes are tracked separately, so
  a swapped artifact cannot masquerade as a verified one.
- `adopt`, `upgrade`, and `rollback` re-check the live file under a process lock
  before replacing it; a dry-run plan is never itself a write authorization.
- Recovery rolls back a prepared transaction, rolls forward a durable commit,
  and stops — preserving evidence — on unknown external drift. It never deletes
  user data to "fix" a state it does not understand.
- Adopt rejects source files carrying setuid/setgid/sticky or other special
  permission bits rather than silently dropping them.

Read [SECURITY.md](SECURITY.md) for the threat model and private vulnerability
reporting, and [ADR-0004](docs/adr/ADR-0004-trust-first-and-manager-boundaries.md)
for the trust-first command ladder and manager-ownership boundary. Before
adopting a valuable binary, skim the [data & API contract](docs/04-data-and-api.md)
and [known risks](docs/08-risk-and-debt.md).

## Data location

hukou follows XDG and stores everything under `$XDG_DATA_HOME/hukou` (or
`$HOME/.local/share/hukou`). It never edits your shell configuration.

```text
hukou/
├── state.lock
├── manifest.json
├── manifest.json.bak
├── transactions/            # write-ahead log
└── store/
    └── <name>/
        ├── original/<binary>   # immutable adoption-time backup
        └── <tag>/<binary>      # retained versions
```

| Variable | Purpose |
|---|---|
| `HUKOU_DATA_DIR` | Override the entire hukou data root |
| `XDG_DATA_HOME` | Select the default data root |
| `GITHUB_TOKEN`, `GH_TOKEN` | Raise GitHub API limits; never forwarded to download hosts |
| `HUKOU_REQUIRE_ATTESTATION` | Require installer attestation verification (`1`/`true`/`yes`) |

## Build from source

Requires the Go toolchain version declared in [go.mod](go.mod).

```sh
git clone https://github.com/rtwsvj/hukou.git
cd hukou
make build
./bin/hukou version
```

The full local gate is `make verify` (fmt, vet, unit, race, coverage, build,
license, installer, and release-script checks); `make release-verify` adds
`shellcheck` and `govulncheck`. See [docs/06-dev-setup.md](docs/06-dev-setup.md).

## FAQ

**Why the name "hukou"?**
户口 (hùkǒu) is the Chinese household-registration system — the official
record of who lives where. It is the exact metaphor for what this tool does:
your `PATH` is full of residents, most of them registered to some package
manager, and a handful living there undocumented. hukou gives those strays a
registration entry — a known origin, a version history, and a way home if an
upgrade goes wrong.

**Does it fight with Homebrew / mise / npm?**
No. By default hukou *refuses* to adopt a binary another manager already owns
(override with `--force` only if you know why). It manages the gap, not their
territory.

**Is it safe to point at an important binary?**
The safety machinery is built for exactly that, but trust is earned: start with
the read-only commands (`scan`, `explain`, `list`, `doctor`), preview your first
`adopt` and `upgrade` with `--dry-run`, and rehearse the loop on a disposable
binary before you adopt something you care about.

**Can it upgrade everything on my machine in one command?**
Not on its own — and on purpose. Cross-manager orchestration belongs to
[topgrade](https://github.com/topgrade-rs/topgrade); hukou plugs in as one
custom command so its strong transaction and rollback guarantees stay scoped to
what it actually owns.

**Where does it put its state? Does it touch my shell config?**
Under `$XDG_DATA_HOME/hukou` (see [Data location](#data-location)). It never
modifies `.bashrc`, `.zshrc`, or any shell startup file.

## Contributing

Issues and PRs welcome — especially real-hardware verification on Linux and
detector coverage for package managers not yet in the chain. See
[CONTRIBUTING.md](CONTRIBUTING.md), [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md),
and [GOVERNANCE.md](GOVERNANCE.md). For usage questions use
[GitHub Discussions](https://github.com/rtwsvj/hukou/discussions); for
suspected vulnerabilities follow [SECURITY.md](SECURITY.md) rather than the
public tracker.

## License

Original hukou work is licensed under the [Apache License 2.0](LICENSE),
Copyright 2026 rtwsvj. Adapted code and dependencies keep their own
licenses — see [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md),
[docs/VENDORED.md](docs/VENDORED.md), and [LICENSES/](LICENSES/).
