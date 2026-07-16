# Security policy

hukou replaces executable files and stores rollback material, so integrity,
path handling, archive extraction, credential handling, and crash recovery are
part of its security boundary.

hukou is an individual, best-effort open-source project maintained in spare
time. There is no company, on-call rotation, or paid support behind it. The
commitments below describe what the maintainer aims for, not a guarantee.

## Threat model

This summary states what hukou is designed to protect and what it explicitly is
not. Architecture decision records carry the full rationale.

### Protected (in scope)

- **Installer trust chain.** `scripts/install.sh` refuses non-HTTPS asset URLs,
  verifies the SHA-256 checksum of every downloaded archive against the
  release's published `checksums.txt`, and can additionally require a GitHub
  build-provenance attestation (`gh attestation verify`, gated by
  `HUKOU_REQUIRE_ATTESTATION`) that binds the archive to this repository and its
  release workflow before anything is extracted. See
  [ADR-0001](docs/adr/ADR-0001-h1-safety-and-release-contract.md).
- **Write-path integrity.** Activating a binary replaces the live file through a
  same-directory temporary regular file that is fully written, `fsync`ed, and
  atomically renamed, so a concurrent reader always observes the complete old or
  complete new binary — never a half-written one
  ([ADR-0002](docs/adr/ADR-0002-regular-file-activation.md)). The paired live
  binary and manifest updates are guarded by a single persisted transaction WAL:
  a crash mid-update is deterministically rolled back or rolled forward on the
  next run, and unknown external drift fails closed instead of being overwritten
  ([ADR-0003](docs/adr/ADR-0003-crash-recovery-and-doctor.md),
  [ADR-0006](docs/adr/ADR-0006-transaction-residue-self-heal.md)). Write
  commands serialize through a cross-process state lock.
- **Release-origin integrity.** Missing or invalid checksum entries fail closed,
  asset selection is deterministic, release requests stay on an allowlisted set
  of GitHub hosts over HTTPS, and the API token is never forwarded to download
  CDNs on redirect.

### Not a security boundary (out of scope)

- **The local same-user boundary.** hukou is a single-user CLI. `doctor`,
  `explain`, and the other read-only reports describe your own installation for
  diagnosis; they are not a sandbox and do not defend one local process against
  another running as the same user. An attacker who can already write your
  `$HOME`, `$PATH`, `HUKOU_DATA_DIR`, or the live binary's directory has already
  crossed the boundary hukou protects
  ([ADR-0004](docs/adr/ADR-0004-trust-first-and-manager-boundaries.md)).
  Installing files you do not own into elevated/system locations is likewise out
  of scope.
- **Compromised upstream sources.** hukou verifies that a downloaded archive
  matches the checksums and optional attestation the upstream repository
  published; it cannot detect a project whose own release or signing identity is
  compromised at the source.
- **Other package managers.** `upgrade --all` only touches hukou-managed
  manifest entries. Whole-machine upgrades delegated to Topgrade run under those
  tools' own trust and failure semantics, not hukou's.

## Supported versions

Before 1.0, the latest released minor line receives normal security fixes.
Older lines may receive a critical fix at the maintainers' discretion, but
users should plan to upgrade.

| Version | Supported |
|---|---|
| 0.3.x | In development; not yet released |
| 0.2.x | Yes |
| 0.1.x and earlier | No |

## Report a vulnerability privately

Do not open a public issue for a suspected vulnerability.

Use GitHub's private vulnerability reporting form:

<https://github.com/rtwsvj/hukou/security/advisories/new>

If the form is unavailable, do not disclose exploit details in Discussions or
Issues. Contact the repository owner through a private channel and provide
only enough public information to request a secure reporting path.

Include, when safe:

- affected hukou version and commit;
- operating system and architecture;
- installation method;
- affected command and minimal reproduction;
- expected and observed security impact;
- whether the behavior requires a malicious archive, release, path, local
  process, or network response;
- a proposed fix, if available.

Remove tokens, usernames, private repository names, real tool paths, manifest
contents, and proprietary binaries. A maintainer may request additional
details through the private advisory.

## Response targets

These are best-effort targets from a single maintainer, not a service-level
agreement:

- acknowledge a new private report within 72 hours;
- provide an initial severity and scope assessment within 7 days;
- coordinate a fix and disclosure timeline based on exploitability and user
  impact;
- credit reporters who want public credit.

The project will not ask a reporter to keep a vulnerability secret
indefinitely. If timelines change, maintainers will explain why in the private
advisory.

## Security-relevant examples

Please report issues involving:

- archive traversal, unsafe extraction, or unexpected file writes;
- checksum bypass, asset confusion, or release-origin confusion;
- GitHub token or credential disclosure;
- symlink, hard-link, path, permission, or ownership attacks;
- replacing a live binary without the expected integrity checks;
- transaction recovery that corrupts state or overwrites unknown drift;
- privilege escalation or unsafe elevated installation;
- release artifact, workflow, or dependency supply-chain compromise.

Ordinary crashes, confusing output, and non-security bugs belong in the public
issue tracker unless they expose sensitive data or cause unsafe file changes.

## Coordinated disclosure and safe research

Good-faith research that avoids privacy violations, data destruction,
service disruption, credential theft, and testing against systems you do not
own is welcome. Use temporary directories and disposable binaries.

After a fix is available, the project may publish a GitHub Security Advisory
describing impact, affected versions, remediation, reporter credit, and any
known limitations.
