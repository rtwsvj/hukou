# External audit entry point

Thank you for reviewing hukou. This file is the shortest route to the exact
V0.3 private release-candidate scope, its evidence, and its known gaps.

## Audit these objects

| Object | Immutable identifier | Meaning |
|---|---|---|
| Base | `bd4faa32d9b5b604b1b224f97fe891ed670f3742` | Current `main` before V0.3 |
| Code subject | `1fa45a0d8473446e3208490f037aef924abea181` | V0.3 implementation and SBOM workflow fix |
| Original evidence-docs commit | `2cd0467098700b899b8b87ee627eb2b75412f397` | Evidence recorded after the fixed code run, before this audit package |
| Audit-package content baseline | `b60e890a6a5b9bbb9ad8f18bd96c8fbdf7b6139e` | First immutable commit containing this complete handoff package |
| Review-time documentation head | Capture the current PR head after fetching | Record later documentation/verification-only commits separately |
| Review branch | `codex/hukou-v0.3-private-rc` | Moving branch; do not use it as an immutable subject |
| Draft pull request | [#6](https://github.com/rtwsvj/hukou/pull/6) | Private, open, draft, and unmerged at handoff time |

The code verdict is bound to the **code subject**, not to a branch name. The
documentation commit does not change Go, shell, or workflow code. Audit the
exact range:

```bash
git diff bd4faa32d9b5b604b1b224f97fe891ed670f3742...1fa45a0d8473446e3208490f037aef924abea181
```

## Start here

1. Read the [handoff and reproduction guide](docs/audit/v0.3-private-rc-handoff.md).
2. Review the [claims-to-evidence map](docs/audit/v0.3-claims-evidence.md).
3. Work through the [security and reliability checklist](docs/audit/v0.3-review-checklist.md).
4. Check the [artifact and platform matrix](docs/audit/v0.3-artifact-and-platform-evidence.md).

## Current boundary

- The repository is private and the latest official release is `v0.2.0`.
- There is no V0.3 tag or GitHub Release, and PR #6 has not been merged.
- The recorded verdict is **local/private RC readiness pass**, not public
  release readiness.
- GitHub-hosted CI did not execute any job step because of an account
  billing/spending-limit block. CodeQL is intentionally skipped while the
  repository is private. Neither result is a green remote gate.
- The recorded release archives and SBOM were local-only evidence. Their
  hashes are useful for comparison but do not replace an independent rebuild.
- The original `pinhaoma-review` was an internal Codex review without a
  standalone raw report. It is context, not an external clearance or proof
  that no P1/P2 issue exists.

## Reporting findings

Please include the subject SHA, OS/architecture, Go version, exact command,
exit status, minimal reproducer, expected behavior, actual behavior, and
whether real user state was involved. Remove tokens, usernames, private
repository names, absolute home paths, binaries, and transaction payloads.

For a vulnerability, follow [SECURITY.md](SECURITY.md). For ordinary defects,
attach the same evidence to the private review channel or PR #6.
