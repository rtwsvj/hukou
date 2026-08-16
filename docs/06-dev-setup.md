# Development Environment

## Prerequisites

- Go: the minimum toolchain version is authoritative in the root `go.mod`; locally you may use a compatible newer patch release, and CI resolves the declared version via `go-version-file`.
- Git.
- GNU tar: needed only when running the reproducible release script; on macOS you can use `gtar`.
- GitHub CLI: used only by the release workflow when publishing assets.

Local collaboration convention: all shell commands are run with the `rtk` prefix. GitHub Actions runners do not install RTK; workflows run standard commands directly.

## Build and verification

```bash
make build
make test
make race
make coverage
make vet
make fmt-check
make license-check
make install-test
make release-test
make shellcheck
make vulncheck
make verify
make release-verify
```

| Target | Meaning |
|---|---|
| `build` | builds to `bin/hukou` with `-trimpath` |
| `build-release` | single-platform fast build to `bin/hukou`, injecting version/commit/date via `scripts/build_flags.sh` (the same ldflags source as `scripts/release.sh`); for local self-install after going public |
| `test` | repo-wide unit tests and isolated mock e2e tests |
| `race` | repo-wide race detector |
| `coverage` | generates a gitignored `coverage.out` and prints a per-function summary |
| `vet` | `go vet ./...` |
| `fmt-check` | checks gofmt only, without modifying files |
| `license-check` | checks the root license, notices, source/dependency licenses, and release packaging wiring |
| `install-test` | validates install, dry-run, force, strict SemVer 2.0.0 (build metadata not accepted), checksum, and URL scheme boundaries against a temporary fixture |
| `release-test` | validates the release script's strict SemVer 2.0.0 boundaries against a valid/invalid matrix, using a local fake builder and without creating real release artifacts |
| `shellcheck` | `shellcheck scripts/*.sh`; requires shellcheck installed locally |
| `vulncheck` | runs a pinned `govulncheck@v1.5.0 ./...`; requires access to the Go module/vulnerability data sources |
| `verify` | the standard gate covering fmt, module verify, vet, test, race, coverage, build, license, installer, and the release-version matrix |
| `release-verify` | `verify` plus shellcheck and govulncheck, the release-specific gate |
| `release` | runs `release-verify` first, then invokes `scripts/release.sh` |
| `demo` | after building, runs a read-only/isolated demo in a temporary directory; it should never touch real hukou state |

### Single source for version injection, and the semantic difference

`scripts/build_flags.sh` is the only assembly point for the buildinfo `-X`
ldflags; both `scripts/release.sh` and `make build-release` call it, so the two
build paths never drift. Their **version semantics** are intentionally
different:

- `make release` (via `scripts/release.sh`) requires strict SemVer 2.0.0
  (validated before build_flags is called) and refuses a dirty working tree by
  default.
- `make build-release` targets local development/self-install: `VERSION=` may be
  any string; when omitted it falls back to `git describe --tags --always
  --dirty`, allowing a `-dirty` suffix.

```bash
make build-release                    # git describe version
make build-release VERSION=v0.3.0    # explicit version
./bin/hukou version                   # verify injection
```

## Safe local CLI experimentation

Never run development smoke tests against real user data. Always set up a temporary root:

```bash
tmp="$(mktemp -d)"
HOME="$tmp/home" \
XDG_DATA_HOME="$tmp/data" \
HUKOU_DATA_DIR="$tmp/hukou" \
PATH="$tmp/bin" \
./bin/hukou scan
```

When testing upgrade/rollback, the test binary must also live on this temporary PATH. Delete the entire temporary directory when finished.

## V0.3 installer development contract

`scripts/install.sh` supports Darwin/Linux × amd64/arm64, writing by default to
`$HOME/.local/bin/hukou`, without using sudo or modifying shell rc files. It first downloads
`checksums.txt` and the target archive; when an authenticated gh CLI is detected, it runs
`gh attestation verify --repo rtwsvj/hukou --cert-identity-regex
'^https://github\.com/rtwsvj/hukou/\.github/workflows/release\.yml@refs/tags/v[0-9][^ ]*$'`
against the **downloaded archive** (the actual subject of the attestation — checksums.txt is not)
(the anchored regex pins down the certificate SAN; `--signer-workflow` is not used, since it is
an unescaped, unanchored prefix regex), terminating on failure (this happens before any tar
inspection/extraction); when gh is missing or unauthenticated, it prints a warning and falls back
to transport trust only. It then requires a single, exact SHA-256 entry, checks the archive root,
extracts only the regular executable, and replaces the target via a same-directory temp file.

- Production/mirror URLs must be HTTPS; HTTP is always rejected.
- `file://` is only for isolated testing, and requires explicitly setting `HUKOU_ALLOW_FILE_URL=1`.
- An existing target is rejected by default; only `--force` will replace it.
- In non-force mode, a dangling symlink is also treated as "target already exists"; after
  downloading, an atomic no-replace commit is done via a hard link inside the target directory —
  if a competing process creates any node after the precheck, the operation fails without overwriting.
- `--force` is the user's explicit authorization to replace; it activates via a same-directory
  temp file followed by `mv -f`.
- `--dry-run` makes no network access and creates no prefix; it only prints the
  version/platform/checksum URL/attestation intent/destination.
- `HUKOU_REQUIRE_ATTESTATION` is case-insensitive: `1/true/yes` forces attestation (fails closed
  even if gh is missing/unauthenticated); empty or `0/false/no` allows falling back to transport
  trust; **any other value fails immediately** — a typo is never allowed to silently downgrade
  the guarantee.
- `scripts/install_test.sh` covers the pass/fail/missing tri-state, the require matrix, and the
  "failure must happen before extraction/install" assertion, using a gh mock that strictly
  validates its actual arguments (subcommand, subject being an existing archive, `--repo`, and
  the anchored `--cert-identity-regex` value) together with a tar spy.
- The installer has landed on the private V0.3 branch, but there is no v0.3 release endpoint yet;
  the existence of the script must not be written up as if a public install channel were already live.

## Generated artifacts

- `bin/`: local builds
- `dist/`: release archives
- `coverage.out`, `*.coverprofile`: coverage data

All of these files are gitignored and should not be committed. The historically tracked copy of `coverage.out` is a Phase 1 legacy artifact and should be removed from the Git index.

## Documentation sync rules for changes

- CLI changes: root README, CLI reference, test documentation.
- manifest/store changes: data/API, requirements, risk documentation.
- Security semantics changes: spec, ADR, failure-injection tests.
- Release changes: Makefile, release script, workflow, release documentation.
