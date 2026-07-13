# 历史阶段记录

本目录保存 Phase 1、Phase 2 各批次执行者当时的完成声明。它们是历史证据，不是当前事实源，也不表示当前 HEAD 已通过相同测试。

阅读顺序：

1. `SKELETON-DONE.md`
2. `DETECTORS-DONE.md`
3. `FIXES-DONE.md`、`FIXES-ROUND2.md`
4. `DETPATCH-DONE.md`
5. `PHASE2-GHRELEASE/MANIFEST/STORE/ASSETPICK/ARCHIVE/CMD-DONE.md`
6. `PHASE2-FIXES-DONE.md`

后续修复可能使早期文件中的函数签名、行号和限制过时。例如旧 store 记录中的 `Prune(name, keep)` 已被后续修复扩展。当前状态请从 `../README.md`、规格与 Codex verification reports 进入。

新工作不再直接新增 `*-DONE.md`；使用 `docs/codex/execution-reports`、`change-records`、`verification-reports` 三段式记录。
