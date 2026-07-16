# Phase 1 Spec: hukou scan

Status: implemented and in maintenance. This document defines the scan
contract; current verification results are authoritative per the evidence
mapping in `docs/audit/`.

## Goal

`hukou scan`: walks every executable file on PATH, determines an
installation source (attribution) for each binary, and outputs a table or
JSON. Performs no write operations and makes no network calls (Phase 1 is
purely local).

## Technical Baseline

- Go version follows the exact `go` directive in the root `go.mod`; module
  is `github.com/rtwsvj/hukou`
- CLI framework: spf13/cobra; everything else is standard library only
  (archive libraries / network libraries are prohibited)
- Platform: macOS first; code reserves a Linux slot via build tags /
  runtime.GOOS
- Output defaults to a human-readable table; `--json` outputs the full
  structure

## Commands and Flags

```
hukou scan [--json] [--unknown-only] [--source <name>] [--dir <extra-dir>...]
```

- `--unknown-only`: list only unattributed (ownerless) binaries
- `--source`: list only the specified source
- `--dir`: append extra directories to scan beyond PATH

## Architecture

```
main.go / cmd/root.go / cmd/scan.go     # cobra layer, thin
internal/scan/       # PATH traversal, dedup (first in PATH order wins for
                      # same name, rest marked shadowed), executable detection
internal/provenance/ # Detector interface + chain of responsibility + per-source detectors
internal/output/     # table + JSON
internal/manifest/   # struct definitions for scan results (Phase 2's manifest evolves here)
```

### Core Types (must be followed)

```go
type Binary struct {
    Name, Path, RealPath string // RealPath = after EvalSymlinks
    Kind BinKind               // MachO | ELF | Script | Other
    Shadowed bool
}
type Attribution struct {
    Source     string // "brew" | "cargo" | "go" | ... | "system" | "unknown"
    Package    string // package/formula/module name
    Version    string // filled in when cheaply obtainable
    Upstream   string // filled in when derivable (e.g. go module path)
    Confidence string // "exact" | "inferred"
    Evidence   string // human-readable rationale, e.g. "symlink → ../Cellar/fzf/0.46.0/bin/fzf"
}
type Detector interface {
    Name() string
    Load(env Env) error            // preload manifest data (e.g. parse .crates2.json), once
    Match(b Binary) *Attribution   // returns nil if attribution is not possible
}
```

- `Env` injects HOME, PATH, and various root directories — **detectors must
  never read os.Getenv directly or hardcode $HOME**; everything goes
  through Env, to keep fixture testing easy.
- Chain-of-responsibility order: hukou manifest → path-prefix-based →
  symlink-resolution-based → buildinfo (only for unattributed) → system →
  unknown. Stops at the first match.

## Detector Inventory

Tier 1 (must be implemented, each with a fixture unit test):

| Source | Determination Basis |
|---|---|
| hukou | Path/RealPath registered in the manifest, as the authoritative head-of-chain source; a corrupt manifest only produces a warning and does not abort the scan. Transaction residue: the read path only admits **verified `completed-*`** entries — name exactly matching `completed-<32-lowercase-hex-chars>`, Lstat is a real directory (not a symlink), and the COMMIT marker inside the directory matches the ID (i.e., committed and converged, only the directory removal is pending); in this case the detector attributes normally and only emits a non-fatal note (`stale journal residue; run a mutating command or repair to clean`), reported through the runner's separate notes channel (not mixed into warnings). Everything else fails closed and is downgraded (the detector is removed from the chain → the registered binary falls back to system/unknown and a warning is written): `pending-*` (published but not converged; a protected path may be in flight); `building-*` (may be the visible window of another process's active Begin — a single-point check cannot cover the entire read cycle, is indistinguishable from abandoned residue, and there is a race with an active writer); unknown and any malformed name (wrong ID shape, uppercase hex, symlinked directory, missing or mismatched COMMIT) |
| brew | RealPath falls under `$(brew --prefix)/Cellar/<formula>/<ver>/` (prefix injected via Env; defaults to checking both /opt/homebrew and /usr/local); formula + version extracted from the path |
| macports | RealPath prefix /opt/local |
| cargo | Parses `~/.cargo/.crates2.json` (package name/version/source URL); falls back to the `~/.cargo/bin` path prefix |
| rustup | rustup proxies (fixed list: rustc/cargo/etc.) under `~/.rustup/toolchains` or `~/.cargo/bin` |
| go | GOBIN/GOPATH/bin path + **direct debug/buildinfo.ReadFile read on any unattributed binary** (uses internal/provenance/gobin.go, vendored from gup — do not rewrite) |
| npm / pnpm / yarn / bun | Each tool's global bin directory (npm prefix, ~/Library/pnpm, ~/.yarn/bin, ~/.bun/bin); npm resolves the package name by tracing back through the global node_modules/.bin symlink |
| pipx | `~/.local/pipx/venvs` or XDG; venv name resolved by tracing back through the symlink |
| uv | `~/.local/share/uv/tools`; symlinks in `~/.local/bin` that point into uv tools |
| pip-user | `~/Library/Python/*/bin` |
| mise | `~/.local/share/mise/{shims,installs}`; tool name + version extracted from the installs path |
| asdf | `~/.asdf/{shims,installs}` |
| gem | gem bindir (`~/.gem/ruby/*/bin` and system gem paths) |
| nix | RealPath prefix /nix/store; drv name + version extracted from the store path |
| volta | `~/.volta/bin` |
| deno | `~/.deno/bin` |
| dotnet | `~/.dotnet/tools` |
| composer | `~/.composer/vendor/bin` or XDG config/composer |
| krew | `~/.krew/bin` |
| curl-installer | Known install-directory table: `~/.opencode/bin`→opencode, `~/.kimi-code/bin`→kimi, `~/.claude/local`→claude, `~/.codeium`, `~/.foundry/bin`, `~/.bun/install`, etc. — table-driven and extensible |
| system | /bin /sbin /usr/bin /usr/sbin /usr/libexec, /System/*, Xcode CommandLineTools |
| unknown | Fallback |

Tier 2 (implement as time allows, same standard): opam (~/.opam),
ghcup/stack (~/.ghcup, resolved via ~/.local/bin), luarocks, cpanm
(~/perl5/bin), conda (~/miniconda*/bin, ~/anaconda*/bin), sdkman
(~/.sdkman), flutter/dart pub (~/.pub-cache/bin), gcloud
(google-cloud-sdk/bin), aqua, proto (~/.proto/bin), stew
(~/.local/share/stew default layout).

## macOS Details (required)

- Executable determination: mode&0111; Kind determination reads the first
  4 header bytes: Mach-O `0xfeedface/0xfeedfacf/0xcafebabe/0xbebafeca`
  (covering fat binaries/byte order), ELF `0x7f454c46`, `#!` is Script
- Large files: only the header is read, not the whole file; unreadable
  files are skipped and counted
- Same-name dedup: the first one in PATH order takes effect; the rest are
  marked shadowed but still attributed

## Acceptance Criteria (all must be satisfied to be considered complete)

1. `go build ./... && go vet ./... && go test ./...` all green; test
   commands must be chained with `&&`
2. Every Tier 1 detector has at least one fixture unit test (fake
   directory structures under testdata/; the go detector uses a minimal
   precompiled Go binary in testdata, or an integration test against
   hukou's own binary)
3. `hukou scan` scans the entire PATH on a real machine in <5s; `--json`
   output is parseable by `python3 -m json.tool`
4. No new third-party dependencies (beyond cobra);
   `internal/provenance/gobin.go` keeps its vendored wiring as-is, its
   core logic unchanged
5. The table output ends with a summary line: total count / source count /
   unknown count / shadowed count; after the summary line (and the
   optional by-source breakdown), `Report.Warnings` is rendered line by
   line first (prefixed `warning:`, e.g. detector downgrades), then
   `Report.Notes` is rendered line by line (prefixed `note:`, non-fatal
   hints from successfully loaded detectors), consistent with the
   `explain` table style; the first write error in the render loop is
   propagated upward. Each channel has its own independent JSON field
   (`warnings`/`notes`); the safety gate only consumes warnings.

A historical green result in a completion record does not prove the
current HEAD is green; the full gate in
`docs/07-testing-and-verification.md` must still be run before every
release.

## Prohibited

- Copying any code from topgrade/pacaptr/meta-package-manager (GPL) is
  prohibited
- Network requests are prohibited; writing to user directories is
  prohibited (scan is a purely read-only command)
- Shelling out from detectors to call external commands like brew/npm is
  prohibited (slow and untestable; everything relies on filesystem
  evidence). The sole exception: Env construction is allowed to do a
  one-time read of environment variables and fixed candidate paths

## Known Limitations

1. **TOCTOU**: there is a time window between `Stat` and the subsequent
   `Open`/`DetectKind` inside `Walk`; if a file is replaced, deleted, or
   has its permissions changed during the scan, the result may not match
   the final open (recorded in `FileErrors` or as `Kind=Other`), and no
   retry or locking is performed. The transaction residue check
   (`transaction.CheckReadable`) has the same kind of window — there is no
   atomicity between the triple verification and the caller's subsequent
   read, nor among the three verification steps (name/Lstat/COMMIT)
   themselves — and this has been recorded and accepted without adding a
   read lock (2026-07-15 decision, see `docs/09-decision-log.md`): the
   read path is a same-user diagnostic view rather than a security
   boundary, the hukou detector independently re-verifies sha256 for each
   matched entry, and the attribution conclusion does not depend on this
   check being correct at any given instant; the write path (`Begin`)
   remains fail-closed across all categories and holds the mutation lock.
2. **npm `.bin` wrapper scripts cannot be traced back to a package
   name**: if a shim under the global `node_modules/.bin` cannot be
   resolved to a real package directory, it can only fall back to the
   binary name — the npm package name cannot be reliably recovered.
3. **nvm / custom npm prefix not covered**: only the global layout under
   `npm_config_prefix` / `NPM_CONFIG_PREFIX` and the brew prefix is
   recognized; nvm version directories, manually changed user prefixes,
   etc. are not enumerated.
4. **Empty PATH segments are deliberately not treated as CWD per POSIX**:
   POSIX treats an empty segment in `PATH` as the current directory;
   hukou skips empty segments and writes to `Report.Warnings` instead
   (this diverges from shell semantics and is an intentional choice).
