# ADR-0008: Archive Codec Policy (Dependency Allowlist for Release Codecs)

- Status: Accepted
- Date: 2026-08-23
- Amends: the "zero third-party dependencies" stance recorded in docs/specs/phase2-adopt-upgrade.md and referenced by earlier ADRs; supersedes the "tar.xz is not yet supported" decision in that document.
- Related: portability plan PF-1 (archive-format ceiling), docs/adr/ADR-0002 (regular-file activation).

## Background

hukou's dependency stance was stdlib-only: the Go standard library ships gzip and zip readers, so release assets were limited to tar.gz/tgz, zip, single-file gz, and bare binaries. That stance froze the codec whitelist at whatever `archive/*` and `compress/*` happen to include. Live verification showed real cost:

- typst ships its unix binaries exclusively as `.tar.xz` — zero installable candidates on any OS.
- restic v0.19.1 ships all binaries as `.bz2` (still out of scope; see below).
- helix ships linux/macos as `.tar.xz`, so selection silently fell through to a `.AppImage` bare install needing libfuse2 at runtime.

xz and zstd are now common enough in release engineering that refusing them makes hukou unusable for a growing class of upstreams. The Go standard library has no xz or zstd decompressor, so supporting these formats requires third-party code.

## Decision

1. The stdlib-only stance is replaced by an explicit **codec allowlist**: compression codecs may be added only when they satisfy the rules in item 4, and each addition requires its own ADR amendment or entry here.
2. Two dependencies are admitted immediately, both **reader-only**:
   - `github.com/ulikunitz/xz` — xz stream reader for `.tar.xz`/`.txz` release assets.
   - `github.com/klauspost/compress/zstd` — zstd stream reader for single-file `.zst` assets.
3. Reader-only means: hukou never writes xz/zstd streams. Only the decompression side of each library is exercised (`xz.NewReader`, `zstd.NewReader`). Compressors pull in far more API surface, more attack surface, and more maintenance risk, and no hukou workflow produces archives — it only unpacks upstream release assets.
4. Rules for any future codec addition — all must hold:
   - **Pure Go, no cgo.** cgo breaks cross-compilation from CI and forces C toolchains on contributors; every current build target must remain a plain `GOOS/GOARCH` matrix.
   - **Actively maintained**, with a public security history (timely CVE response) and no abandoned-maintenance signals.
   - **Smallest API surface** that does the job: prefer packages exposing a reader over kitchen-sink libraries; do not admit a module to use one helper.
   - **Reader side only** unless a documented hukou workflow needs writing.
   - The format must be one upstream projects actually ship (demand signal), not speculative coverage.

## Current Implementation

- `internal/archive/format.go` recognizes `.tar.xz`/`.txz` (`FormatTarXz`) and `.zst`/`.tzst` (`FormatZst`) as supported formats.
- `internal/archive/archive.go` extracts both under all existing guards (traversal-safe joins, member/candidate/declared-size discovery budgets, 512 MiB per-entry and total caps, mode normalization to 0755). tar.xz uses two passes over the same verified descriptor: xz readers cannot be re-read after EOF like gzip multistream, so pass 2 opens a fresh decoder after `Seek(0)`.
- Bare `.bz2`, the `.tar.bz2` variants, bare `.xz`, and tar-over-zstd containers (`.tar.zst`) remain explicitly unsupported this round: bz2 was judged optional (restic-class demand only) and container-over-zstd adds selection logic without a live upstream demanding it. They fail closed with a clear error naming the format, never degrading to a bare-binary install. `.tzst` is treated as a single zstd stream — the same relationship `.gz` has to `.tgz`.

