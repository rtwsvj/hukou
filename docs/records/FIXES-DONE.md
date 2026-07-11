# Code-review fixes (12 items)

Acceptance: `go build ./... && go vet ./... && go test ./...` green; `go build -o bin/hukou . && ./bin/hukou scan` OK.  
No changes to `internal/provenance/gobin.go`; no new third-party deps.

| # | Fix | Location |
|---|-----|----------|
| 1 | Exec-only unreadable regular files still recorded (`Kind=Other`, `Evidence`), occupy `seen` | `internal/scan/walk.go:126-152`; `Binary.Evidence` in `internal/scan/binary.go:20-21`; test `internal/scan/walk_test.go:243-305` (`TestWalk_execOnlyUnreadable`) |
| 2 | Non-regular (FIFO/socket/device): never `Open`; skip + file error detail | `internal/scan/walk.go:117-124`; test `internal/scan/walk_test.go:307-343` (`TestWalk_skipFIFO`, `syscall.Mkfifo`) |
| 3 | PATH dir dedup: `EvalSymlinks` + `os.SameFile` | `internal/scan/walk.go:76-84`, `158-194` (`identifyDir`, `shouldSkipDir`); tests `walk_test.go:345-418` (`TestWalk_dirDedupSymlink`, `TestWalk_dirDedupCaseFold` skips on case-sensitive volumes) |
| 4 | Relative PATH → `filepath.Abs`; empty segment skipped + Report warning (deliberate non-POSIX) | `internal/scan/walk.go:26-50` (`SplitPATHWithWarnings`), `69-74`; `cmd/scan.go:39-48`; `output.Report.Warnings` `internal/output/output.go:28`; tests `walk_test.go:420-482` |
| 5 | `FAT_MAGIC_64` `0xcafebabf` + swap `0xbfbafeca` | `internal/scan/kind.go:14`, `19`; `isMachOFatMagic` `117-125`; test `walk_test.go:86-113` (`TestDetectKind_fatMagic64`) |
| 6 | `cafebabe` vs Java: read nfat_arch (bytes 5–8); `>128` → Other | `internal/scan/kind.go:26`, `67-70`, `75-104` (`classifyFatOrJava`); test `walk_test.go:115-143` (`TestDetectKind_javaClassDisambiguation`) |
| 7 | Remove dead post-`isMachOMagic` equality branch | `internal/scan/kind.go:61-70` (thin via `isMachOThinMagic`, fat via `isMachOFatMagic` only; old raw-equality block deleted) |
| 8 | UTF-8-safe table truncate; JSON per-file errors; table count only | `internal/output/output.go:27` (`FileErrors`), `133-166` (`truncate`/`truncateRunes`); table omits paths `78`; wire-up `cmd/scan.go:80`; tests `output_test.go:50-55`, `77-80`, `108-116`, `128-165` |
| 9 | `runner.go` comments: full Phase-1 chain (not skeleton/gobin slot) | `internal/provenance/runner.go:5-10`, `20-47`; test assert chain `system_test.go:77-93` |
| 10 | brew self → `Package=homebrew`; Caskroom → `Package=<cask>` exact | `internal/provenance/brew.go:22-57` (self + Caskroom before Cellar); tests `detectors_test.go:59-70`, `system_test.go:96-123` |
| 11 | system prefix `/Library/Apple` | `internal/provenance/system.go:31`; test `system_test.go:25` |
| 12 | Unit tests for each fix | see rows above; all under `internal/scan/walk_test.go`, `internal/output/output_test.go`, `internal/provenance/{detectors,system}_test.go` |

## Supporting types

- `scan.FileError` — `internal/scan/binary.go:24-28`
- `scan.Result.FileErrors` / `Warnings` — `internal/scan/walk.go:14-15`
- `output.Report.FileErrors` / `Warnings` — `internal/output/output.go:27-28`
