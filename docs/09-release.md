# Release Process

## Current Stable Release

[`v0.2.0`](https://github.com/rtwsvj/hukou/releases/tag/v0.2.0) is the current
stable release, containing four Darwin/Linux × amd64/arm64 archives and
`checksums.txt`; [`v0.1.0`](https://github.com/rtwsvj/hukou/releases/tag/v0.1.0)
remains unchanged. This time the GitHub-hosted CI/tag workflow failed before
runner scheduling due to an account payment/spending limit; the release was
completed manually using the same annotated tag, only after two fresh
Go/Linux containers running the official scripts produced byte-for-byte
identical results, three-platform buildinfo smoke tests passed, and remote
re-download matched the local artifacts byte-for-byte. This exception does
not change the runner-gate requirement for the normal future process.

V0.3 is currently a private RC branch with internal local/private readiness
records, in preparation for an external audit. There is no `v0.3.0`/prerelease
tag, no V0.3 GitHub Release, and no change to repository visibility. The V0.3
content later on this page describes the release contract verified for
subject `1fa45a0`; that conclusion does not equal a public release. This
round only created a draft private PR; even if local gates pass, merging
still requires an independent Go/No-Go decision — it cannot be
auto-authorized by "tests passed."

## Versioning Policy

- Stable releases use a stable SemVer tag, e.g. `v0.3.0`; RCs may use
  `v0.3.0-rc.1`.
- The release/install scripts strictly accept SemVer 2.0.0: neither the core
  nor purely numeric prerelease identifiers may have leading zeros, and
  dot-separated prerelease identifiers must not be empty; this project does
  not accept build metadata. A tag containing `-` creates a GitHub
  prerelease.
- The above is the v-prefix contract for the shell release entry point; the
  Go update policy separately accepts sortable `X.Y.Z`/`vX.Y.Z` with valid
  build metadata. The two serve different purposes, and their test matrices
  are maintained separately.
- The historical `phase1` / `phase2` tags are retained, but are not
  installable releases.
- Published tags are never moved; fixes use a new patch version.

## Build Contract

`scripts/release.sh` reads the commit SHA and commit timestamp from a pinned
commit, and injects them into the following variables:

- `github.com/rtwsvj/hukou/internal/buildinfo.Version`
- `github.com/rtwsvj/hukou/internal/buildinfo.Commit`
- `github.com/rtwsvj/hukou/internal/buildinfo.Date`

The build uses `CGO_ENABLED=0`, `-trimpath`, `-buildvcs=false`. The script
pins umask and file mode; GNU tar pins member order, owner/group, and mtime;
gzip uses `-n` to strip timestamps. The release workflow builds independently
twice on the same controlled runner and compares file-by-file before using
the artifacts.

By default the script rejects a dirty worktree, to avoid wrapping
uncommitted code with a given commit's version information. Local
experiments may explicitly set `ALLOW_DIRTY=1`, but such artifacts must not
be released.

Artifacts:

```text
dist/
├── hukou_<version>_darwin_amd64.tar.gz
├── hukou_<version>_darwin_arm64.tar.gz
├── hukou_<version>_linux_amd64.tar.gz
├── hukou_<version>_linux_arm64.tar.gz
└── checksums.txt
```

Each V0.3 archive is configured to include `hukou`, `README.md`,
`README.zh-CN.md`, the root `LICENSE`, `THIRD_PARTY_NOTICES.md`, and
`LICENSES/*.txt`. Although the repository remains private, public-readiness
preparation has already added the Apache-2.0 root license; this does not
mean the repository is public.

## Local Snapshot

```bash
VERSION=v0.3.0-rc.1 ALLOW_DIRTY=1 bash scripts/release.sh  # only for uncommitted dev-tree experiments
```

Requires GNU tar; on macOS, where BSD tar is the default, install and use
`gtar`. Generated artifacts are placed in the gitignored `dist/`.

## GitHub Actions

`.github/workflows/release.yml` supports:

- Manual run: executes the full gate on Linux/macOS and uploads a snapshot
  artifact, without creating a Release.
- Pushing a `v*` tag: executes the same gate on Linux/macOS, then packages,
  verifies the tag, and creates a GitHub Release.

Third-party GitHub Actions are pinned to immutable commit SHAs verified
against the official API, with the corresponding version noted at the end of
the line, to prevent a movable major-version tag from changing what the
release executes.

The regular verify step includes fmt/module/vet/test/race/coverage/build/
license/installer and the release-version matrix; the package job further
runs shellcheck and a pinned `govulncheck@v1.5.0`. Tag releases additionally
enforce: the tag is an annotated tag, the target commit is already on
`origin/main`, the four archives correspond one-to-one to the four filenames
in `checksums.txt` and pass verification, and the Linux amd64 binary's
reported version/commit/build date exactly matches the current commit. The
package job holds only `contents: read`; only after Linux/macOS verification
and all packaging gates succeed does the independent publish job gain
`contents: write` and create the Release.

The package job extracts the Linux amd64 archive and runs `hukou version` to
confirm the archive layout and ldflags injection; it then extracts one
platform binary from each of the four archives into an isolated scan root,
generates an SPDX JSON SBOM with a pinned Syft 1.46.0, and asserts that
hukou and its three direct dependencies each appear 4 times, with files
equal to 4. The four archives, `checksums.txt`, and the SBOM are uploaded as
workflow artifacts.

As of 2026-07-15, subject `1fa45a0` has completed direct uncached
ordinary/race testing at 641 tests / 21 packages each, `make release-verify`
exits 0 under a command-level mirror override (coverage 72.9%, govuln
reports no known vulnerabilities), and it passed whole-repo ordinary/race
plus installer/release tests in a non-root Linux/arm64 + GNU tar 1.34
environment. Two independent builds of all four targets are byte-for-byte
identical, with 4/4 checksums, archive root/mode, buildinfo, and installer
smoke all passing. The final Syft 1.46.0 SBOM is SPDX 2.3, 21 packages/4
files; acceptance testing found and fixed the prior scheme's hollow SBOM of
1 package/0 files. The default `proxy.golang.org` IPv6 timeout remains
honestly noted as-is.

Artifact attestation only runs when repository visibility is public. A
public tag release must wait for build provenance and SBOM attestation to
succeed; a private tag skips attestation and can still proceed to publish,
so a private snapshot/Release cannot claim to have GitHub attestation.
CodeQL is likewise only enabled for a public repository. Draft PR #6's CI
run `29352308455` has confirmed `steps=[]` for all five jobs, blocked before
execution by the billing/spending limit; this must be recorded as an
external infrastructure gate, not described as a green remote CI.

Creating a Release while private and later making the repository public
would expose existing un-attested assets as visibility changes, since the
workflow does not retroactively fill in the attestation chain. The public
Go/No-Go must therefore either forbid or explicitly handle private
tags/Releases, and verify the attestation of the final public assets before
the visibility change. It's also necessary to separately verify the Go
1.26.2 declared in `go.mod` against the Go 1.26.5 used for the historical
archive hash, and not substitute the latter for the expected hosted
toolchain.

## Release Checklist

1. The worktree is clean, and the target commit is already on `main`; during
   the private RC phase only branches/draft PRs are allowed, no stable tag is
   cut. A draft PR must not be merged without an independent Go/No-Go.
2. The current change has a pinned-commit verification report; the external
   audit has completed and closed/accepted all P0/P1/P2 findings and
   handoff-checklist hypotheses. The internal review has no standalone raw
   report and cannot satisfy this item on its own.
3. CI's Linux/macOS test, race, build, coverage, and quality/vulnerability
   gates are all green; if blocked by billing, the Go/No-Go must explicitly
   accept the external gate rather than recording it as green.
4. The dual-build snapshot is byte-for-byte identical, all four archives
   extract correctly, and `version` is correct.
5. `checksums.txt` verifies all archives; the archives contain the
   license/notices/bilingual README/dependency licenses.
6. The SPDX JSON SBOM parses correctly and corresponds to the target
   commit/artifacts; attestation must succeed when public.
7. Update the changelog/release notes, and re-confirm that the README still
   lists v0.2.0 as the current stable release until the actual release
   completes.
8. Only after obtaining an independent public Go/No-Go may the annotated
   SemVer tag be created and pushed; the workflow then verifies the tag
   points into `main`'s history.
9. After the release workflow succeeds, verify the remote assets, prerelease
   flag, and visibility; a published tag must never be moved.
10. Before the visibility change, confirm there is no private V0.3 Release
    that would be unintentionally exposed; the final public assets must go
    through the intended public attestation path.

## Rollback

- Failure before tagging: delete the local `dist/`, fix the issue, rerun,
  and do not create a tag.
- If the manual process created a draft/unpublished Release: delete the
  draft and rerun; the current automated workflow publishes directly on
  success and does not create a draft.
- Published Release: never overwrite assets or move the tag; note the issue
  and publish a patch version.
