# Project governance

hukou uses an open-maintainer model with documented roles and public technical
decisions. The current lead maintainer is [@rtwsvj](https://github.com/rtwsvj).

## Principles

- User safety and recoverability take precedence over feature velocity.
- Decisions and their evidence should be inspectable after the conversation
  ends.
- Authority grows through sustained, constructive work.
- No contributor is expected to be permanently available.
- Security reports and conduct incidents remain private until disclosure is
  safe and appropriate.

## Roles

### Contributor

Anyone who reports, documents, tests, designs, translates, reviews, or submits
code. Contributors do not need commit access.

### Triager

A trusted contributor who can label, reproduce, deduplicate, and route public
issues and Discussions. Triagers cannot publish releases or access private
security material by default.

### Reviewer

A trusted contributor with demonstrated knowledge in one or more areas.
Reviewers provide technical approval but do not automatically have merge or
release authority.

### Maintainer

A contributor with sustained project-wide judgment who can merge changes,
manage repository settings, and participate in releases. Access follows least
privilege; private vulnerability access is granted separately.

### Lead maintainer

The lead maintainer is responsible for project direction, final conflict
resolution, maintainer appointments, and release authorization. This role is
not permission to bypass documented safety or licensing requirements.

## Becoming a project member

Maintainers may nominate a contributor for triager, reviewer, or maintainer
status based on:

- repeated high-quality contributions;
- respectful and reliable collaboration;
- understanding of the project's safety boundaries;
- sound review and disclosure judgment;
- willingness and actual capacity to accept the role.

The nomination and role scope are recorded publicly unless doing so would
expose private security or conduct information. Maintainer status should aim
for support from existing maintainers; while the project has only one
maintainer, the lead documents the appointment decision.

Inactive access may be removed after six months without project activity,
after a private check-in when contact is possible. Returning contributors can
regain access through the normal role process.

## Decision process

Routine fixes and documentation changes use pull request review.

Material changes to the CLI contract, manifest schema, transaction model,
security boundary, release process, licensing, or governance require:

1. a public issue or Discussion describing the problem and alternatives;
2. an ADR or equivalent durable decision record;
3. focused verification and migration/rollback analysis;
4. maintainer approval.

The project prefers rough consensus backed by evidence. If discussion reaches
an impasse, the lead maintainer records the decision and rationale. Decisions
may be revisited when new evidence appears.

Security fixes may be developed privately. The public record is completed when
coordinated disclosure is safe.

## Pull requests and merges

A merge requires:

- applicable automated checks passing;
- reviewer questions resolved;
- tests proportional to risk;
- documentation and changelog updates for user-visible changes;
- license and attribution review for third-party material.

Until a second active maintainer is available, the lead may merge a
self-authored pull request after CI and a documented self-review. Changes to
transactions, downloads, archive extraction, credentials, release workflows,
or repository security should receive independent review whenever practical.
The project aims to reach a bus factor of at least two before 1.0.

## Releases

Only maintainers explicitly granted release authority may publish a release.
Every release must follow [docs/09-release.md](docs/09-release.md), use a new
SemVer tag, pass the current release gates, and publish user-facing notes.

Published tags and assets are never silently replaced. A defect in a release
is corrected with a new version and, when necessary, a security advisory.

## Conduct and moderation

All project spaces follow [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md). Maintainers
handling a report must disclose conflicts of interest and recuse themselves
when appropriate. Private reports are shared only with the people needed to
investigate and act.

## Governance changes and succession

Governance changes use a pull request with at least 14 days for public comment,
unless an urgent safety or legal issue requires a shorter documented process.

If the lead maintainer expects an extended absence, they should name an acting
maintainer and transfer the minimum required access. If the lead is unreachable
for 90 days, active maintainers may nominate a successor and document the
evidence and decision publicly. Repository-owner controls outside GitHub's
technical ability remain subject to the hosting account's legal authority.
