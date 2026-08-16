## Summary

<!-- What changed? Keep this to one reviewable decision. -->

## User problem and outcome

<!-- Who benefits, what problem is solved, and what observable behavior changes? -->

## Scope

- [ ] Bug fix
- [ ] New or changed behavior
- [ ] Provenance detector
- [ ] Documentation or translation
- [ ] Build, packaging, or dependency maintenance
- [ ] Security-sensitive change

### Intentionally not changed

<!-- State important non-goals and boundaries. -->

## Verification

<!-- List exact commands and results. Use temporary HOME/PATH/HUKOU_DATA_DIR for mutation tests. -->

- [ ] `make fmt-check`
- [ ] `make vet`
- [ ] `make test`
- [ ] `make race`
- [ ] `make coverage`
- [ ] `make verify`
- [ ] `git diff --check`

Additional focused or manual verification:

## Safety and compatibility

- [ ] Read-only commands remain zero-write unless this PR explicitly and visibly changes that contract.
- [ ] Live-file, path, archive, checksum, credential, transaction, and permission risks were considered.
- [ ] Failure, interruption, rollback, and unknown external drift were tested where applicable.
- [ ] CLI, JSON, manifest, and supported-platform compatibility were considered.
- [ ] Tests use disposable binaries and temporary directories, not real user tools.

Explain any checked item that does not apply or any accepted risk:

## Documentation and release notes

- [ ] User-visible changes are recorded in `CHANGELOG.md`.
- [ ] CLI/reference, architecture, risk, roadmap, or ADR documentation is updated where applicable.
- [ ] No planned command or feature is described as already released.

## Third-party and legal review

- [ ] No third-party code or assets were added.
- [ ] Or: exact source, commit, copyright, license, modification header, `LICENSES/`, `THIRD_PARTY_NOTICES.md`, and source records were updated.
- [ ] All commits include a DCO `Signed-off-by` line.

### Vendored / adapted files

Adapted upstream files listed in `THIRD_PARTY_NOTICES.md` (for example
`internal/assetpick/detect.go` and `internal/provenance/gobin.go`) are kept as
close to their source as possible and must not be edited casually.

- [ ] This PR does not modify any vendored/adapted third-party file.
- [ ] Or: it does, and the reason, the upstream diff, and the updated
      modification header / `THIRD_PARTY_NOTICES.md` records are described above.

## Related work

<!-- Link issues, Discussions, ADRs, security advisories, or prior pull requests. -->
