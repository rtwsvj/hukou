# Contributing to hukou

Thank you for helping make unmanaged CLI binaries safer to inspect, adopt,
upgrade, and recover.

Contributions in English or Simplified Chinese are welcome. User-facing
English text should remain understandable to readers who do not speak Chinese,
and Chinese documentation should link to its English source of truth.

## Before opening work

- Read the [Code of Conduct](CODE_OF_CONDUCT.md).
- Use [Discussions](https://github.com/rtwsvj/hukou/discussions) for usage
  questions and open-ended ideas.
- Search [Issues](https://github.com/rtwsvj/hukou/issues) for existing work.
- Report vulnerabilities privately as described in [SECURITY.md](SECURITY.md).
- Propose a discussion or issue before a large behavior, data-model, security,
  or architecture change.

Small documentation fixes, test improvements, and clearly scoped bug fixes do
not need a separate issue.

## Development setup

Use the Go version declared in [go.mod](go.mod).

```bash
git clone https://github.com/rtwsvj/hukou.git
cd hukou
make build
./bin/hukou version
```

Before submitting a pull request:

```bash
make fmt-check
make vet
make test
make race
make coverage
make verify
git diff --check
```

If a test needs a manifest, store, HOME, PATH, or live executable, use a
temporary directory. Tests must not adopt, upgrade, or delete real user tools.

## Safety invariants

Changes must preserve these contracts unless an approved architecture decision
explicitly replaces them:

- Scanning is local, read-only, and zero-network.
- Read-only commands do not silently recover or mutate state.
- Expected checksum metadata fails closed when an asset entry is absent,
  invalid, or mismatched; a release with no checksum asset at all also
  refuses by default (explicit `upgrade --allow-unverified` only, with a
  durable UNVERIFIED audit marker).
- Downloaded-asset and activated-binary hashes remain distinct.
- Adopt, upgrade, and rollback verify live state before replacement.
- Live replacement remains atomic and durable.
- State-changing commands remain serialized.
- Write-ahead recovery stops on unknown external drift.
- GitHub credentials are not forwarded to untrusted download hosts.
- Archive extraction cannot escape its destination.

Security-sensitive changes to transactions, archive extraction, checksum
verification, paths, permissions, downloads, release workflows, or credential
handling require focused adversarial tests and an update to the relevant
architecture or risk documentation.

## Adding or changing a provenance detector

1. Add filesystem-only evidence where possible; avoid subprocesses in detector
   hot paths.
2. Add positive fixtures and unrelated negative fixtures.
3. Define the detector's confidence and human-readable evidence.
4. Check detector ordering so a broad heuristic cannot override stronger
   ownership evidence.
5. Update the CLI/reference documentation if a new source value is emitted.
6. Keep private paths, usernames, tokens, and host-specific data out of
   fixtures.

## Pull requests

Keep a pull request focused enough to review as one decision. The description
should explain:

- the user problem and intended behavior;
- what changed and what deliberately did not change;
- tests and manual checks performed;
- safety, compatibility, data migration, and rollback impact;
- documentation and changelog changes;
- third-party source or license impact.

Update [CHANGELOG.md](CHANGELOG.md) for user-visible changes. Add or update an
ADR under `docs/adr/` for a durable architecture or security decision.

The project uses Developer Certificate of Origin sign-off. Sign each commit:

```bash
git commit -s
```

The `Signed-off-by` line certifies that you have the right to submit the
contribution under the project's license. See
<https://developercertificate.org/>.

## Third-party code

Do not paste or adapt third-party code without recording its exact source
repository, evaluated commit, copyright, license, local relationship, and
modifications.

When third-party code is added or materially adapted, update all applicable
items in the same pull request:

- the source-file attribution header;
- [docs/VENDORED.md](docs/VENDORED.md);
- [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md);
- the applicable text under [LICENSES/](LICENSES/);
- release packaging and license checks.

Design inspiration is not permission to copy code. GPL implementation must not
be copied into the Apache-2.0 codebase.

## Review and acceptance

A maintainer may request changes for behavior, tests, documentation, scope,
security, portability, compatibility, or licensing. Passing CI is required but
does not replace review.

Contributor roles and decision rules are documented in
[GOVERNANCE.md](GOVERNANCE.md).
