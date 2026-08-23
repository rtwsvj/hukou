# Spec: Portability Audit & Generalization Plan (draft r1)

Status: **draft for review**. Companion to [`agent-coexistence.md`](agent-coexistence.md).
Method: two parallel read-only audits (machine-specific hardcodings; cross-platform /
cross-environment gaps) against `fix/adversarial-review-r1` @ `d4b796f`, cross-checked
against live GitHub API data where a claim depended on external facts (e.g. restic's
asset formats were verified, not assumed). Every finding cites `file:line`.

## Headline conclusions

1. **No HIGH machine-specific finding exists in shippable code.** Personal paths live
   only in deliberately gitignored artifacts plus one spec example line. The codebase
   is cleanly partitioned by build tags (`_unix` / `_other`) with conservative
   fallbacks, so both declared targets (darwin arm64 primary, linux amd64/arm64
   secondary) inherit the hardened paths: flock+O_NOFOLLOW locking, FIFO-safe
   O_NONBLOCK opens, process-group termination, exact-size downloads.
2. **The real generality ceiling is not "Eric's Mac" but three deliberate design
   stances**: the stdlib-only dependency rule (which froze archive codecs at
   tar.gz/zip/gz/bare), GitHub-releases-only hosting (host allowlist + wire format +
   Sigstore-via-gh), and the v1 manager registry scoped to one workstation's toolset
   (documented in phase3-up.md as "breadth is explicitly NOT a goal").
3. The coexistence spec's design-level assumptions are few and separable from its
   correctly-quarantined Appendix A (this-machine survey): XDG-only config path,
   10m/30m delegated-timeout defaults that contradict today's documented brew=45m,
   and drift=adopt's reliance on the forge-specific API digest field.

## Findings inventory

### A. Machine-specific hardcodings (audit angle 1)

| ID | Sev | Finding | Where | Effort |
|---|---|---|---|---|
| MS-1 | MED | pip-user detector is macOS-only by construction: `PipUserBase=~/Library/Python` set unconditionally, no GOOS branch (unlike BrewPrefixes/PnpmHome which branch). Linux `pip install --user` scripts land in `~/.local/bin` and can never match → attribution degrades to `unknown`. | internal/provenance/env.go:97, pip_user.go:20-22 | trivial |
| MS-2 | MED | `up` registry mirrors THIS machine's manager set (brew/npm/pnpm/rustup/uv/gh-ext), by documented policy (phase3-up.md:52-54). Linux system managers (apt/dnf/pacman/apk) invisible; pipx/cargo/go-install users get zero `up` coverage even though provenance *detects* those sources — attribution/orchestration asymmetry. System-manager rows need a privilege model the executor lacks; genuinely major. | internal/orchestrate/registry.go:33-44 | major |
| MS-3 | MED | OS-level proxy discovery is darwin-only; other platforms fall back to proxy env vars only. Headless/env-var setups are fully covered; GNOME/KDE GUI-configured proxies are silently unproxied (downloads connect direct, failing on filtered networks). | internal/sysproxy/sysproxy_other.go:7, sysproxy.go:43-48 | moderate |
| MS-4 | LOW | Personal artifacts (`RELEASE-CHECKLIST.md`, `REPORT-*.md` with `/Users/eric` paths) exist in the worktree but are deliberately gitignored ("never public"). Residual risk: forced-add or worktree tarball. | .gitignore:31-34 | trivial |
| MS-5 | LOW | Spec example leaks `/Users/eric/.opencode/bin/opencode`; rewrite to `~/.opencode/bin/opencode` before merge. | docs/specs/agent-coexistence.md:162 | trivial |
| MS-6 | LOW | macOS-only detector inputs registered on every GOOS (MacPorts `/opt/local`, Xcode CLT, `/Applications`). Dead config on Linux — no false positives (dirs don't exist), just misleading Env reading and wasted chain steps. | internal/provenance/env.go:87-89,163-166 | trivial |
| MS-7 | LOW | local-project detector keyed to `~/Projects` convention, no override knob. Other layouts degrade to `unknown` (labeling only). | internal/provenance/env.go:167 | trivial |
| MS-8 | LOW | curl-installer roots table encodes the agent tools present on this workstation (each entry IS a real product default — tool conventions, not personal paths). Coverage grows per hand-added row. | internal/provenance/env.go:152-161 | moderate |
| MS-9 | LOW | GUI-language locale detection darwin-only; elsewhere env vars only (sane degradation). | internal/i18n/system_locale_other.go | moderate |
| MS-10 | LOW | Attestation identity pins `.github/workflows/release.yml@refs/tags/<tag>` — repo-layout coupling, not machine coupling. Renaming the workflow breaks attestation for ALL installer users (fails closed, safe but total). `rtwsvj` is load-bearing in REPO default/formula/npm metadata; cosmetic elsewhere. | scripts/install.sh:46,142-147 | trivial |
| MS-11 | LOW | Installer dependency floor excludes minimal/busybox hosts (needs perl or GNU `ln -T`/`mv -T` for atomic activation) — fails closed with named dependency; correct posture, undocumented floor. | scripts/install.sh:168-231 | trivial |
| MS-12 | LOW | Coexistence-spec delegated timeouts (10m default / 30m cap) contradict shipped docs+tests asserting brew=45m acceptance — must be reconciled in P-B, not just retuned. | docs/specs/agent-coexistence.md:58,66,76 vs cmd/up_timeout_test.go:143-148 | trivial |
| MS-13 | LOW | drift=adopt leans on the GitHub-API per-asset `digest` field — forge-specific, consistent with current GitHub-only scope, belongs on the roadmap. | docs/specs/agent-coexistence.md:255-259 | moderate |
| MS-14 | LOW | Test-suite sensitivity: this host's real system proxy (127.0.0.1:7890) once leaked into tests (now hermetically stubbed); fd-leak regression test skips on non-darwin (lsof-based) so Linux never exercises it; network e2e properly triple-gated. | cmd/upgrade_preextract_tamper_test.go:156-162, store/stress_scale_test.go:112-114 | trivial |

### B. Cross-platform / cross-environment gaps (audit angle 2)

Premise corrections established by code reading: flock+O_NOFOLLOW locking and
FIFO-safe opens DO apply on Linux (`lock_unix.go`/`safeopen_unix.go` build tags are
`unix`, covering linux); missing Setpgid group-kill is Windows-only. Declared Linux
support does not silently lose the hardened paths.

| ID | Sev | Finding | Where |
|---|---|---|---|
| PF-1 | HIGH | Archive-format ceiling: only tar.gz/tgz, zip, gz, bare extractable. Live-verified impact: restic v0.19.1 ships all binaries as `.bz2` (23 assets); typst ships unix assets as `.tar.xz` → zero candidates on ANY OS; helix linux/macos are `.tar.xz` so linux/amd64 silently falls to installing the `.AppImage` bare (needs libfuse2 at runtime). Root cause: stdlib-only dependency stance froze the codec whitelist. | internal/archive/format.go:14-46 | 
| PF-2 | MED | GitHub welded beneath otherwise good seams: hardcoded host allowlist, GitHub-shaped Release/Asset JSON (Gitea uses attachments), suggest/search GitHub-only, Sigstore attestation shell-side via gh only. `Client.BaseURL` mirror seam EXISTS but no flag/env wires it to the CLI. | internal/ghrelease/client.go, updatecheck/check.go |
| PF-3 | MED | No offline/mirror mode in the binary: upgrade/suggest/drift-reconciliation are network-bound and fail closed offline (by design); only install.sh carries `HUKOU_RELEASE_BASE_URL=file://`. Air-gapped drift=adopt = permanent refuse. | updatecheck/check.go |
| PF-4 | MED | assetpick mis-picks for helix-class releases: OSLinux priority regex matches `.AppImage` before format filtering rejects the true archive — silent mis-install needing libfuse2. | internal/assetpick/pick.go+detect.go |
| PF-5 | MED | Linux desktop (GUI-configured) proxies invisible (=MS-3 from the environment side). | internal/sysproxy/* |
| PF-6 | LOW | BusyBox-only hosts fail activation (perl-or-GNU-T requirement) — fail-closed, needs documenting. | scripts/install.sh:168-231 |
| PF-10 | LATENT | Three structural POSIX assumptions define future-Windows cost: scan exec-bit test finds zero binaries (walk.go:96); durablefs directory-fsync fails on first durable write; roottrust Unix-perm gate rejects all data roots under Go-synthesized perms. Everything else already has `_other` fallbacks. | walk.go:96, durablefs.go, roottrust.go |
| PF-16..22 | LOW | Corporate/air-gap posture verified sound: no TLS pinning anywhere (system CA respected), env proxies honored incl. installer curl, nothing writes shell rc files, i18n en→zh with English fallback harmless, darwin plist locale reader darwin-tagged. | various |

## Generalization roadmap

Phases are ordered by unlock-value ÷ effort. Each names its demand signal — phases
without their signal stay parked.

### G0 — Hygiene (no demand signal needed; trivial)
1. Rewrite the spec example path to `~/.opencode/bin/opencode` (MS-5).
2. Reconcile delegated-timeout defaults with the shipped 45m contract in P-B (MS-12).
3. Document the two floors in README platform matrix: BusyBox activation floor
   (PF-6) and the archive-format ceiling (PF-1) with a pointer to G1.
4. Record the XDG-everywhere managers.json decision (incl. "Windows maps later")
   in ADR-0007 when P-B lands (per audit note — consistent with the existing
   data-root stance, not an oversight).
5. GOOS-gate macOS-only detector inputs; add Linux pip-user base `~/.local`
   alongside the darwin base (MS-1, MS-6).

### G1 — Codec decision (biggest unlock; ADR required)
Unblocks restic/typst/helix-class tools on ALL platforms. Options:
- **a. Pure-Go codecs** (`github.com/klauspost/compress` for zst, an xz reader,
  `dsnet/compress` bzip2): keeps single-binary story; adds dependencies — requires
  amending the stdlib-only ADR with an explicit allowlist.
- **b. External decompressor shelling-out** (`xz -d`, `zstd`, `bzip2` if present):
  zero deps, but reintroduces the subprocess surface the executor fence was built
  to contain, plus availability variance.
- **c. Refuse-with-better-guidance only**: name the codec and link install
  instructions (current behavior, better error).
Recommendation: **(a)** for xz+zstd, keep bz2 optional; write ADR-0008 (dependency
policy amendment) first. Pair with the assetpick AppImage-priority fix (PF-4) so
helix-class releases stop mis-installing.

### G2 — Forge seam wiring (mirror/corporate demand signal)
1. Expose `Client.BaseURL` via env (`HUKOU_GITHUB_BASE_URL`) + flag — the seam
   already exists; document token handling for GHES.
2. Land the API-digest verification (already specified in agent-coexistence.md)
   behind a small `ChecksumSource` abstraction so a future GitLab backend can
   supply its own evidence type (MS-13).
3. Document explicitly: GitLab/Gitea/Forgejo remain unsupported; the seam is
   `ReleaseSource/Lister/ByTagSource` in updatecheck.

### G3 — Detector hygiene + cheap manager rows (Linux-user demand signal)
1. `~/.local` pip-user base + GOOS gates (from G0, listed here for roadmap weight).
2. `HUKOU_PROJECTS_DIR` override for the local-project detector (MS-7).
3. Cheap `up` rows with honest no-atomic-inventory documentation: pipx, cargo-install
   (`cargo install-update` only if the helper exists — detect it), go install
   (detect-only, no bulk upgrade). Each row documents its diff-semantics limits.
4. Linux fd-leak regression via `/proc/self/fd` replacing the lsof gate (MS-14).

### G4 — Linux desktop conveniences (GNOME/KDE demand signal)
gsettings/KDE proxy reader behind a build tag, feeding the existing
SystemProxyURL seam (MS-3/PF-5). Moderate effort; skip until requested.

### G5 — Parked, gated on explicit demand
1. **System package managers** (apt/dnf/pacman/apk): need a privilege model
   (executor has none), honest no-atomic-inventory semantics, and per-family
   ADRs per ADR-0004's control-plane rule. managers.json (P-B) is the intended
   pressure valve meanwhile.
2. **Windows port**: cost is exactly the three POSIX assumptions (exec-bit scan,
   dir fsync, Unix-perm trust gate) plus procgroup/symlink divergence. Do not
   start without a demand signal; the `_other` fallback pattern keeps the cost
   bounded and enumerable.
3. **Air-gapped drift=adopt**: document that reconciliation fails closed offline;
   an offline evidence bundle (operator-supplied SHA256SUMS) is a possible future
   extension, not a promise.

## Non-goals

- Becoming a general archive utility — codecs are admitted only to serve release
  assets hukou itself verifies.
- Supporting non-GitHub forges beyond keeping the seam honest.
- Any auto-adoption or background daemon behavior (unchanged from coexistence spec).

## Verification hooks

- G1: poison-style fixture archives in xz/zst/bz2; restic/typst/helix release
  manifests as golden tests for assetpick.
- G2: httptest GHES-shaped server exercising BaseURL end-to-end.
- G3: GOOS-matrix unit tests for DefaultEnv; fake `$HOME` layouts per detector.
