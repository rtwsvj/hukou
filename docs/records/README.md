# Historical phase records

This directory preserves the completion claims made by the executors of each
Phase 1 and Phase 2 batch at the time. They are historical evidence, not the
current source of truth, and they do not assert that the current HEAD passes
the same tests. The record bodies are kept in their original Chinese.

Reading order:

1. `SKELETON-DONE.md`
2. `DETECTORS-DONE.md`
3. `FIXES-DONE.md`, `FIXES-ROUND2.md`
4. `DETPATCH-DONE.md`
5. `PHASE2-GHRELEASE/MANIFEST/STORE/ASSETPICK/ARCHIVE/CMD-DONE.md`
6. `PHASE2-FIXES-DONE.md`

Later fixes may have made function signatures, line numbers, and limitations in
the earlier files stale. For example, the `Prune(name, keep)` signature noted in
an old store record was extended by a later fix. For current status, start from
`../README.md`, the specs, and the evidence under `../audit/`.

These `*-DONE.md` files are frozen; new work is not recorded by adding more of
them here.
