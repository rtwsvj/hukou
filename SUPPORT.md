# Support

Support is provided on a best-effort basis. The channels below keep usage
questions, reproducible defects, feature discovery, and security reports in
the right place. Repository-hosted community channels become publicly usable
when the repository is public; during private development they are limited to
authorized collaborators.

## Where to ask

| Need | Channel |
|---|---|
| Usage question or troubleshooting | [GitHub Discussions Q&A](https://github.com/rtwsvj/hukou/discussions/categories/q-a) |
| Open-ended idea or use case | [GitHub Discussions Ideas](https://github.com/rtwsvj/hukou/discussions/categories/ideas) |
| Reproducible bug | [Bug report](https://github.com/rtwsvj/hukou/issues/new?template=bug.yml) |
| New provenance detector | [Detector request](https://github.com/rtwsvj/hukou/issues/new?template=detector-request.yml) |
| Confirmed, scoped feature work | [Feature request](https://github.com/rtwsvj/hukou/issues/new?template=feature.yml) |
| Documentation problem | [Documentation report](https://github.com/rtwsvj/hukou/issues/new?template=docs.yml) |
| Security vulnerability | Follow [SECURITY.md](SECURITY.md); do not post publicly |
| Conduct incident | Follow [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md) |

## Before posting diagnostics

Please include:

```bash
hukou version
hukou doctor --deep --json
```

Also include the operating system, architecture, installation method, command,
expected result, actual result, and minimal reproduction.

Review all output before posting it. Remove or replace:

- usernames and home-directory paths;
- private tool and repository names;
- tokens, credentials, and signed URLs;
- manifest entries that reveal private infrastructure;
- proprietary binaries or archive contents.

Never upload your complete hukou data directory to a public issue.

## Supported scope

Current release archives target macOS and Linux on amd64 and arm64. Windows is
not supported. The claims-to-evidence map and reproduction commands for the
latest release candidate are recorded under [docs/audit/](docs/audit/).

hukou supports its own documented workflow for standalone binaries. It does
not provide support for upgrading software owned by Homebrew, npm, Cargo,
pipx, mise, or another package manager. Behavior after bypassing an ownership
refusal with `--force` may require additional reproduction in a disposable
environment.

The project is pre-1.0. CLI, JSON, and manifest compatibility changes will be
documented in [CHANGELOG.md](CHANGELOG.md), but users should read release notes
before every minor upgrade.

## Response goals

These are goals, not guaranteed service levels:

- suspected data loss or unsafe replacement: human triage within 2 business
  days;
- ordinary reproducible bug: initial human triage within 7 days;
- pull request: initial review when maintainer capacity permits;
- Discussions: best effort;
- security report: targets in [SECURITY.md](SECURITY.md).

Maintainers may close stale reports after requesting information and waiting
at least 30 days. A report can be reopened when the missing reproduction is
available.
