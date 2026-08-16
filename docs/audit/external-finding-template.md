# External audit finding template

Copy one block per finding. Do not include secrets, private repository names,
real user binaries, raw HOME paths, or transaction payloads.

```text
Finding ID:
Title:
Severity (P0/P1/P2/P3):
Confidence (confirmed / likely / hypothesis):
Subject commit:
Audit-package commit:
OS / architecture:
Go and tool versions:

Affected invariant:
Affected files and symbols:
Preconditions / threat actor:
Minimal reproducer:
Exact command and exit status:
Expected behavior:
Actual behavior:
User-state impact:
Why existing tests did not catch it:
Suggested remediation or decision:
Evidence attachments / hashes:
```

## Final report summary

```text
Audit scope and exclusions:
Pinned commits:
Environments exercised:
Commands completed:
Commands blocked or skipped:
P0 findings:
P1 findings:
P2 findings:
P3 findings:
Disputed or inconclusive items:
Hosted-state refresh:
Public-release recommendation (Go / conditional Go / No-Go):
Required conditions before merge/tag/visibility change:
```

Use `infrastructure-blocked`, `skipped by policy`, and `not independently
reproduced` precisely. Do not collapse them into pass or fail.
