# Code-review fixes (Round 2)

Acceptance: `GOPATH=/tmp/gopath GOCACHE=/tmp/go-cache GOMODCACHE=/tmp/gomod go build ./... && go vet ./... && go test ./...` green;  
`go build -o bin/hukou . && ./bin/hukou scan` OK (sample host: total=1901 sources=8 unknown=20 shadowed=10 skipped=9).  
No changes to `internal/provenance/gobin.go`; no new third-party deps.

| # | Fix | Location |
|---|-----|----------|
| A | Detector `Load()` error → `Report.Warnings` (name+reason), skip that detector, continue chain | `internal/provenance/runner.go:60-71` (`Load` returns `[]string`, drops failed detectors); wire-up `cmd/scan.go:48-52`; test `system_test.go` `TestRunner_LoadSkipsFailedDetector` |
| B | cargo `.crates2.json` missing/permission/malformed → never error; path-prefix inferred fallback; `Load` always succeeds | `internal/provenance/cargo.go:34-61`; tests `cargo_test.go` (`TestCargoLoad_malformedJSON`, `TestCargoLoad_missingCrates2`) |
| C | Table output sanitizes Name/Path/Package/Evidence (control chars / ANSI → `?`); JSON untouched | `internal/output/output.go:96-106` (call sites), `136-152` (`sanitizeField`); tests `output_test.go` (`TestWriteTable_sanitizesControlChars`, `TestSanitizeField`) |
| D | `splitNameVersion`: first `-<digit>` boundary (nix style) | `internal/provenance/helpers.go:119-131`; tests `helpers_test.go` `TestSplitNameVersion` (glibc-2.39-5 / unstable-2024-01-01 / python3.11 / ripgrep-14.1.0) |
| E | `pnpmPackageVersion`: first `@` separator (scoped-aware); strip `_` peer-dep suffix | `helpers.go:73-117` (`pnpmPackageVersion`, `parsePnpmEntry`, `stripPnpmPeerDeps`); tests `helpers_test.go` `TestPnpmPackageVersion` |
| F | system: exclude `/System/Volumes/*` (Data volume) | `internal/provenance/system.go:49-52`; case in `system_test.go` |
| G | `scan` uses `DefaultEnv().Path` (Env contract), not raw `os.Getenv("PATH")` | `cmd/scan.go:36-37` |
| H | Chain order: mise/asdf before language PMs (cargo/npm/gem/…) | `internal/provenance/runner.go:28-55` (mise/asdf L32-33 before cargo L34); assert in `system_test.go` `TestRunner_chain` |
| I | `walk`: EvalSymlinks fail + dir-identify fail → `FileErrors` (no silent drop) | `internal/scan/walk.go:81-89` (identify), `140-147` (EvalSymlinks) |
| J | Narrow prefixes: macports=`{bin,sbin,libexec}`; nix requires `bin` component; composer only `vendor/bin` | `macports.go:10,25-44`; `nix.go:35-37,55-62`; `composer.go:26-62`; negative cases in `negative_test.go` |
| K1 | Tier-1 negative suite: unrelated binary → nil (excl. unknown) | `internal/provenance/negative_test.go` |
| K2 | helpers table-driven unit tests | `internal/provenance/helpers_test.go` |
| K3 | cargo error-path tests | `internal/provenance/cargo_test.go` |
| K4 | `gobin_test.go` only (no `gobin.go` edits): `ReadGoBinary(os.Executable())` ModulePath; `GoBinDir` priority | `internal/provenance/gobin_test.go` |
| L | Spec “已知限制” | `docs/specs/phase1-scan.md:112-117` (TOCTOU / npm .bin / nvm prefix / PATH 空段) |

## Supporting notes

- `Runner.Load` signature is now `func (r *Runner) Load(env Env) []string` (no hard error).
- Failed detectors are removed from the chain so `Match` never sees half-loaded state.
- Composer may still infer package from RealPath under `vendor/<org>/<pkg>/` when Path is in `vendor/bin`.
