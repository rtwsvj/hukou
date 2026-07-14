# Security policy

hukou replaces executable files and stores rollback material, so integrity,
path handling, archive extraction, credential handling, and crash recovery are
part of its security boundary.

## Supported versions

Before 1.0, the latest released minor line receives normal security fixes.
Older lines may receive a critical fix at the maintainers' discretion, but
users should plan to upgrade.

| Version | Supported |
|---|---|
| 0.3.x | In development; not yet released |
| 0.2.x | Yes |
| 0.1.x and earlier | No |

## Report a vulnerability privately

Do not open a public issue for a suspected vulnerability.

Use GitHub's private vulnerability reporting form:

<https://github.com/rtwsvj/hukou/security/advisories/new>

If the form is unavailable, do not disclose exploit details in Discussions or
Issues. Contact the repository owner through a private channel and provide
only enough public information to request a secure reporting path.

Include, when safe:

- affected hukou version and commit;
- operating system and architecture;
- installation method;
- affected command and minimal reproduction;
- expected and observed security impact;
- whether the behavior requires a malicious archive, release, path, local
  process, or network response;
- a proposed fix, if available.

Remove tokens, usernames, private repository names, real tool paths, manifest
contents, and proprietary binaries. A maintainer may request additional
details through the private advisory.

## Response targets

These are maintainer targets, not a service-level agreement:

- acknowledge a new private report within 72 hours;
- provide an initial severity and scope assessment within 7 days;
- coordinate a fix and disclosure timeline based on exploitability and user
  impact;
- credit reporters who want public credit.

The project will not ask a reporter to keep a vulnerability secret
indefinitely. If timelines change, maintainers will explain why in the private
advisory.

## Security-relevant examples

Please report issues involving:

- archive traversal, unsafe extraction, or unexpected file writes;
- checksum bypass, asset confusion, or release-origin confusion;
- GitHub token or credential disclosure;
- symlink, hard-link, path, permission, or ownership attacks;
- replacing a live binary without the expected integrity checks;
- transaction recovery that corrupts state or overwrites unknown drift;
- privilege escalation or unsafe elevated installation;
- release artifact, workflow, or dependency supply-chain compromise.

Ordinary crashes, confusing output, and non-security bugs belong in the public
issue tracker unless they expose sensitive data or cause unsafe file changes.

## Coordinated disclosure and safe research

Good-faith research that avoids privacy violations, data destruction,
service disruption, credential theft, and testing against systems you do not
own is welcome. Use temporary directories and disposable binaries.

After a fix is available, the project may publish a GitHub Security Advisory
describing impact, affected versions, remediation, reporter credit, and any
known limitations.
