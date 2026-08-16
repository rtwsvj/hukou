# Glossary

| Term | Meaning |
|---|---|
| household register / manifest | The JSON list of entries hukou manages |
| adopt | Registers an existing binary and saves an original backup |
| active binary | The regular file currently executed from the user's PATH; the older symlink form is retained only as a compatible input |
| asset | The archive or bare file downloaded from a GitHub Release |
| asset hash | The SHA-256 of the downloaded asset itself |
| active hash | The SHA-256 of the binary after extraction and activation |
| original | The original binary version preserved at adoption time |
| store | The version directory under `<dataRoot>/store` |
| shadowed | A same-named file on PATH with lower priority that the shell will not execute first |
| fail closed | Stop when evidence is missing or verification is abnormal, rather than degrading to continue installing |
| compensation | Restoring the old path and manifest state when a step after activation fails |
| H1 | The 2026-07-13 security-hardening and first SemVer release milestone |
| WAL / transaction journal | The before/after recovery record and COMMIT decision persisted before changing user state |
| PREPARED | The journal is durable, but the transaction has not yet made an irrevocable commit decision; recovery direction is before |
| COMMITTED | A durable COMMIT already exists; recovery direction is after |
| doctor | The hukou local-state audit command that performs zero writes and zero network access by default |
| UNCLASSIFIABLE | Manifest evidence is invalid, so whether store content is orphaned cannot be safely determined |
| activation event | The immutable record of a single adopt/upgrade/rollback/repair activation in manifest v2 |
| lineage / parent | The provable logical rollback chain for the current version; not ordered by time or store mtime |
| update policy | An entry's selection rules for SemVer/GitHub-latest, stable/prerelease, and exact pin |
| rollback depth | The number of most recent logical ancestors retention protects; current/original/pin are protected separately |
| state fingerprint | The binding digest a repair plan uses over data-root identity, preconditions, and related state content |
| support bundle | An offline-generated, redacted JSON diagnostic summary; excludes raw paths/repos/env/WAL payloads and is never uploaded |
| private RC | Code that has been implemented and is under acceptance review on a private branch; not equivalent to a tag, Release, public repository, or a stable public interface |
