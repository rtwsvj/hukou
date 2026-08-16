# Vendored and adapted code

This document tracks every place where hukou reuses third-party source code, so
that the origin, license, and local modifications stay auditable. It exists for
engineering traceability (debugging, upstream sync, re-implementation, issue
triage) and does not replace the license selection or the legal notices in
[`../LICENSE`](../LICENSE) and [`../THIRD_PARTY_NOTICES.md`](../THIRD_PARTY_NOTICES.md).

Two categories are tracked separately:

- **Adapted** — source was copied and modified into hukou. These files keep an
  in-file header with the upstream source, copyright, license, and a summary of
  the modifications, and the matching license text is retained under
  [`../LICENSES/`](../LICENSES/).
- **Design reference** — the upstream project informed a design decision, but
  the project records do not identify copied implementation in the corresponding
  hukou component. A design reference is not a claim of authorship over, or by,
  the referenced project.

## Adapted source code

| Component | Upstream repo (evaluated commit) | License | Local location | Modifications |
|---|---|---|---|---|
| Release-asset detector | [zyedidia/eget](https://github.com/zyedidia/eget) @ `0983dea` (`detect.go`) | MIT (Copyright (c) 2021 Zachary Yedidia) | `internal/assetpick/detect.go` | Package rename; fixed the priority-branch error message to report `len(priority)` instead of `len(matches)`; no logic changes. Tiebreak rules (darwin/arm64 fallback, extension preference, blacklist) live in separate files layered on top. |
| Go binary provenance reader | [nao1215/gup](https://github.com/nao1215/gup) @ `952fb83` (`internal/goutil/pkginfo.go`) | Apache-2.0 (Copyright 2022 CHIKAMATSU Naohiro) | `internal/provenance/gobin.go` | Removed printer/worker-pool dependencies; injectable environment instead of `os.Getenv`/`exec`; reduced to a single-binary read API. |

## Design references

| Upstream repo (evaluated commit) | License | Informed | Notes |
|---|---|---|---|
| [zyedidia/eget](https://github.com/zyedidia/eget) @ `0983dea` | MIT | `internal/archive/`, `internal/verify/` | Archive location/verification approach used as a design reference. Upstream directory extraction lacked `../` path-traversal defense; hukou adds that guard. Upstream `--upgrade-only` compares by mtime; hukou compares explicit tags from the manifest instead. |
| [marwanhawari/stew](https://github.com/marwanhawari/stew) @ `8a9a3ea` | MIT | `internal/ghrelease/`, `internal/manifest/` | GitHub download flow, binary hashing, and manifest design used as a reference. Known upstream pitfalls avoided: array-index panic on all-prerelease repos; archived `mholt/archiver` v3.1.1 (zip-slip history) replaced with the standard library / `mholt/archives`. |
| [houseabsolute/ubi](https://github.com/houseabsolute/ubi) @ `edfac51` | MIT OR Apache-2.0 (dual-licensed, per its upstream `Cargo.toml`) | `internal/assetpick/pick.go` | Asset-tiebreak rules referenced; the Rust implementation is not ported. |
| [pkgforge/soar](https://github.com/pkgforge/soar) @ `cc0526e` | MIT | `internal/store/`, `internal/manifest/` | Version-addressing and switching architecture referenced. Multi-version/symlink/rollback is implemented independently (patterned after mise/aqua shims). |

## Go module dependencies

Runtime module dependencies are recorded authoritatively in `../go.mod` and
`../go.sum`; their licenses are summarized in
[`../THIRD_PARTY_NOTICES.md`](../THIRD_PARTY_NOTICES.md). `golang.org/x/mod/semver`
backs the pure-SemVer selection described in ADR-0005; its BSD-3-Clause license
and Go patent grant are retained under [`../LICENSES/`](../LICENSES/).

## Maintenance rules

- An "adapted" file must retain its upstream commit, copyright, and the matching
  [`../LICENSES/`](../LICENSES/) path in its header.
- "Design reference" does not mean line-by-line copying. If later review finds
  material copying, add a source header and promote the entry to *Adapted*.
- Release archives must ship the root `LICENSE`, `THIRD_PARTY_NOTICES.md`, both
  README languages, and `LICENSES/*.txt`.
- When adding or materially adapting third-party code, update the source-file
  header, this file, `THIRD_PARTY_NOTICES.md`, and the release packaging in the
  same pull request.
