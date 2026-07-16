# hukou

> Safety-first management for CLI binaries your package manager does not own.

[简体中文](README.zh-CN.md) ·
[Documentation](docs/README.md) ·
[V0.3 audit handoff](AUDIT.md) ·
[Latest release](https://github.com/rtwsvj/hukou/releases/tag/v0.2.0) ·
[Security](SECURITY.md)

hukou (户口, “household registry”) finds standalone executables on your
`PATH`, explains where they came from, adopts the binaries that have no
package manager, verifies upgrades from GitHub Releases, and keeps a local
rollback path.

The current release is **v0.2.0** for macOS and Linux. A **private v0.3
release-candidate branch** contains the trust-first commands, manifest v2,
repair/support tooling, and distribution preparation. Fixed commit `1fa45a0`
passed the local/private RC readiness gate; GitHub-hosted jobs remain blocked
before execution by the account billing limit. v0.3 has not been tagged,
released, merged, or made public.

## Why hukou?

Package managers already do a good job for software they own. The awkward
cases are binaries installed from a GitHub Release, copied from another
machine, built with `go install`, or dropped into a private `bin`
directory. Those files are easy to forget and risky to replace by hand.

hukou provides a deliberately narrow workflow:

```text
scan safely
    → identify unmanaged binaries
    → adopt one explicitly
    → preview an upgrade
    → verify and activate it
    → roll back if needed
```

It does **not** replace Homebrew, MacPorts, npm, Cargo, pipx, mise, or other
package managers. By default, hukou refuses to adopt a binary already owned
by another manager.

## What the current release can do

- **`scan`** — walk `PATH` and optional directories, identify known
  package managers, version managers, system tools, and hukou-managed tools.
  Scanning is local, read-only, and does not use the network.
- **`adopt`** — register an existing executable, preserve its original
  bytes, and create a manifest entry.
- **`upgrade`** — inspect the latest GitHub Release, select a platform
  asset, verify available publisher checksums, store the version, and replace
  the live regular file atomically.
- **`rollback`** — activate a retained version or the preserved original.
- **`list`** — show adopted tools and retained-version counts.
- **`doctor`** — perform a read-only audit of the manifest, live files,
  store, transaction journal, and temporary remnants.

## What is implemented on the unreleased v0.3 branch

These interfaces exist in the private development tree. They are not part of
the latest release and must not yet be treated as a stable public contract.

- **`explain`** — explain the executable selected on `PATH`, its shadowed
  matches, and the evidence for each ownership attribution.
- **`adopt --dry-run`** — validate an adoption and emit a plan without
  creating hukou state; `--json` is available only with the dry run.
- **`outdated`** — check release metadata and asset selection without
  downloading or changing local state.
- **`policy show/set`** — inspect or atomically change SemVer/GitHub-latest,
  stable/prerelease, exact-pin, and rollback-depth policy.
- **Deterministic rollback and retention** — manifest v2 records activation
  lineage; rollback follows logical parents instead of directory times, and
  pruning protects the current version, original, pins, lineage targets, and
  unfinished transactions.
- **`repair plan/apply`** — expose only unfinished-transaction recovery and a
  tightly checked manifest-backup restore, with plans bound to a state
  fingerprint.
- **`support bundle`** — create an offline, redacted JSON diagnostic summary
  without uploading it.

The branch also contains a checksum-verifying installer whose final directory
entry uses atomic no-replace/replace primitives and rejects duplicate target
archive members, licensing/community files, SBOM packaging,
public-only attestations and CodeQL configuration, and a documented Topgrade
custom-command integration. When an authenticated `gh` CLI is available, the
installer additionally verifies the downloaded archive's GitHub
build-provenance attestation, with the signer identity anchored to this
repository's release workflow running for a release tag (an anchored
`--cert-identity-regex`); otherwise it prints a warning and falls back to
transport trust (HTTPS plus the release's checksums.txt). Set
`HUKOU_REQUIRE_ATTESTATION=1` (or `true`/`yes`) to make that verification
mandatory instead of best-effort; leaving it empty or setting `0`/`false`/`no`
keeps the fallback allowed, and any other value is rejected. The
<!-- TODO(P1 handoff): README rewrite owner — internal docs/codex/ records were removed for the public tree; re-point or reword this link during the rewrite. -->
[external audit entry point](AUDIT.md) tracks what is verified and what
remains pending.

## Safety model

hukou prefers a visible refusal over a clever guess:

- On the unreleased v0.3 branch, `scan`, `explain`, `list`, `doctor`,
  `outdated`, `policy show`, a pure `upgrade --dry-run`, and
  `adopt --dry-run` do not mutate hukou state. Metadata checks may use the
  network; scan/explain/doctor/adopt dry-run do not.
- A missing expected checksum entry fails closed.
- Downloaded-asset hashes and activated-binary hashes are tracked separately.
- Adopt, upgrade, and rollback verify the live file before replacing it.
- State-changing commands are serialized by a process lock.
- Live binaries are replaced through a same-directory temporary regular file
  and atomic rename.
- A durable write-ahead transaction records before/after state. Recovery rolls
  back a prepared transaction, rolls forward a durable commit, and stops on
  unknown external drift.
- `doctor` is zero-write and zero-network by default. It reports problems; it
  does not silently delete or repair them.
- On the unreleased v0.3 branch, repair is a separate plan/apply workflow. A
  plan changes no hukou state but writes only the explicitly requested plan
  file; apply revalidates the fingerprint while holding the state lock.
- The unreleased support report omits raw paths, private repository names,
  usernames, environment variables, binaries, and WAL payloads, and never
  uploads itself.

Read the [data and API contract](docs/04-data-and-api.md) and
[known risks](docs/08-risk-and-debt.md) before adopting a valuable binary.

## Supported release targets

v0.2.0 publishes archives for:

| Operating system | Architecture |
|---|---|
| macOS | amd64, arm64 |
| Linux | amd64, arm64 |

Windows is not currently supported. A cross-compiled artifact alone is not
treated as platform support; the project records actual verification evidence
under [docs/audit/](docs/audit/).
<!-- TODO(P1 handoff): README rewrite owner — evidence formerly under docs/codex/verification-reports was removed for the public tree; confirm the final evidence location during the rewrite. -->


## Install v0.2

Use the v0.2 release archives for the current published version, or build the
private RC from source. The v0.3 branch has passed its local/private readiness
gate, including its installer and SBOM content checks, but no v0.3 installer
endpoint or SBOM has been released. Homebrew and Windows packages do not
exist. Public artifact attestations are intentionally gated on repository
visibility.

### Release archive

1. Open the [v0.2.0 release](https://github.com/rtwsvj/hukou/releases/tag/v0.2.0).
2. Download the archive matching your OS and architecture, plus
   `checksums.txt`.
3. Verify the archive:

```bash
# Linux
sha256sum -c checksums.txt --ignore-missing

# macOS: compare the printed digest with checksums.txt
shasum -a 256 hukou_0.2.0_darwin_arm64.tar.gz
```

4. Extract the archive and move `hukou` to a directory already on your
   `PATH`. Review the destination before using elevated privileges.

### Build from source

Use the Go version declared in [go.mod](go.mod):

```bash
git clone https://github.com/rtwsvj/hukou.git
cd hukou
make build
./bin/hukou version
```

The project build and verification commands are:

```bash
make fmt-check
make vet
make test
make race
make coverage
make license-check
make install-test
make release-test
make verify
make release-verify  # also shellcheck and govulncheck
```

## Five-minute start

Start with the command that cannot modify your tools:

```bash
hukou scan --unknown-only
```

Other useful read-only views:

```bash
hukou scan
hukou scan --json
hukou scan --source hukou
hukou scan --dir /path/to/extra/bin
hukou list
hukou doctor
hukou doctor --deep --json
```

When testing a build from the unreleased v0.3 branch, the trust-first entry
points are:

```bash
hukou explain tool
hukou adopt /path/to/tool owner/repo --tag v1.0.0 --dry-run --json
hukou outdated tool
hukou policy show tool
hukou support bundle --format json
```

`outdated` queries GitHub release metadata for eligible non-local entries;
the other preview commands above are local. `repair plan` writes a plan file
by design, so it is not included in this zero-state-change introduction.

Adopt a local-only binary:

```bash
hukou adopt /path/to/my-tool --local
```

Or associate an unmanaged binary with a GitHub repository:

```bash
hukou adopt /path/to/tool owner/repo --tag v1.0.0
```

Always preview the first upgrade:

```bash
hukou upgrade tool --dry-run
hukou upgrade tool
hukou rollback tool
hukou rollback tool --to v1.0.0
```

`upgrade` and `rollback` replace the adopted tool at its registered path.
Use a disposable test binary before trusting any new workflow with an
important executable.

## Data location

By default, hukou follows XDG and stores state below
`$XDG_DATA_HOME/hukou`, or `$HOME/.local/share/hukou` when
`XDG_DATA_HOME` is unset.

```text
hukou/
├── state.lock
├── manifest.json
├── manifest.json.bak
├── transactions/
└── store/
    ├── .tmp/
    └── <name>/
        ├── original/<binary>
        └── <tag>/<binary>
```

Relevant environment variables:

| Variable | Purpose |
|---|---|
| `HUKOU_DATA_DIR` | Override the complete hukou data root |
| `XDG_DATA_HOME` | Select the default data root |
| `GITHUB_TOKEN`, `GH_TOKEN` | Increase GitHub API limits; tokens are not forwarded to untrusted download hosts |

## Project status

v0.2 delivered durable transaction recovery and read-only diagnosis. The
private v0.3 branch implements trust-first inspection, manifest v2 activation
history, policy-aware updates and retention, narrow repair, redacted support,
an installer, and supply-chain/community preparation.

This is a private-readiness status, not a release claim. Fixed commit
`1fa45a0` passed a 321-test/six-package security audit, direct uncached
ordinary and race runs of 641 tests across 21 packages, the complete local
`release-verify` target at 72.9% coverage, and non-root Linux/arm64 ordinary,
race, installer, and release-script tests. Two four-target release builds were
byte-identical; checksums, archive contents, build information, installer
smoke, and a 21-package/4-file SPDX SBOM passed inspection. An internal Codex
claim-to-evidence review recorded no P0/P1/P2 finding, but retained no
standalone raw report and is not an external clearance. The
[external audit handoff](AUDIT.md) lists newly identified review leads and
known evidence gaps. [Draft PR #6](https://github.com/rtwsvj/hukou/pull/6) is
open. GitHub-hosted jobs are blocked before execution by the account's
billing/spending limit, so no remote-green claim is made.

Still outside this RC:

- making the repository public or publishing `v0.3.0`;
- a public fixture repository and scheduled real-network smoke test;
- public installation channels such as Homebrew;
- cross-manager upgrades or rollback (use Topgrade only as an orchestrator);
- Windows, GUI, self-update, and default telemetry.

See the [roadmap](docs/02-roadmap.md) and
[changelog](CHANGELOG.md) for current scope.

## Get help and participate

These community links are prepared for the public beta and may be unavailable
while the repository is in private development.

- Usage questions: [GitHub Discussions](https://github.com/rtwsvj/hukou/discussions)
- Reproducible bugs: [GitHub Issues](https://github.com/rtwsvj/hukou/issues)
- Security vulnerabilities: [SECURITY.md](SECURITY.md)
- Support boundaries: [SUPPORT.md](SUPPORT.md)
- Contributions: [CONTRIBUTING.md](CONTRIBUTING.md)
- Project governance: [GOVERNANCE.md](GOVERNANCE.md)
- Expected conduct: [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md)

Before posting diagnostics publicly, remove usernames, filesystem paths,
repository names, tokens, and details about private tools.

## License and attribution

Original hukou work is licensed under the
[Apache License 2.0](LICENSE), Copyright 2026 Eric (rtwsvj).

Adapted code and dependencies remain under their respective licenses. See
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md),
[LICENSES/](LICENSES/), and the source-level attribution headers.
