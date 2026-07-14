# 项目简报

## 一句话目标

hukou 是一个 macOS/Linux CLI 工具管理器：先盘点机器上已有的可执行文件并判断来源，再把无主工具收编到可校验、可升级、可回滚的本地版本仓库。

## 目标用户

- 同时使用 Homebrew、Cargo、Go、npm、pipx、mise 等多种工具链的开发者。
- 手工下载或 curl 安装过散装二进制、希望补上来源与回滚能力的用户。
- 需要审计本机 CLI 来源，但不希望扫描过程联网或修改系统的人。

## 当前正式发布范围（v0.2.0）

- `scan`：PATH 扫描、来源责任链、表格与 JSON。
- `adopt`：登记并备份本地二进制。
- `upgrade`：GitHub Release 查询、资产选择、下载、校验和激活。
- `rollback`：切换到 store 中旧版本或 original。
- `list`：展示 manifest。
- `doctor`：以文本或稳定 JSON 只读审计 manifest、live、store、backup、transaction 与临时残留。
- `version`：展示发布版本、提交和构建时间。

## V0.3 私有 RC 分支已实现范围

- `explain`、`adopt --dry-run`、`outdated`：先解释、先预览、再修改。
- `policy show/set`：SemVer/GitHub-latest、stable/prerelease、精确 pin 与 rollback depth。
- manifest v2 activation lineage：确定性 rollback 与不依赖 mtime 的保留计划。
- `repair plan/apply`：只开放 transaction recovery 和 manifest backup restore。
- `support bundle`：默认离线、脱敏、不上传的 JSON 诊断。
- checksum 安装器、双语/社区/许可证入口、SBOM 与公开仓库条件下的 attestation/CodeQL 配置。

“已实现”只表示代码和局部测试存在；在完整 verification report 与独立 review
完成前，不等于 RC 已验收。当前正式版本仍是 v0.2.0，仓库仍保持 private。

## 明确不在当前范围

- 管理或代理 Homebrew/npm/Cargo 等已有管理器的升级。
- Windows。
- hukou 自己执行其他管理器的升级；Topgrade 只作为外层编排器，配置见 `integrations/topgrade.md`。
- mise/Brewfile 导出。
- changelog diff、供应链风险评分和 GUI。
- repair-all、孤儿自动删除、硬件掉电/控制器缓存重排证明，以及非协作 writer 在最终复核与系统调用之间的原子 CAS 保证。

## 成功标准

1. `scan` 保持本地只读、无网络。
2. 未收编工具不会被 upgrade/rollback 修改。
3. checksum 和当前文件完整性检查失败时不切换安装。
4. 正常错误或 hukou 协作事务的进程级中断后，活跃文件、store 与 manifest 可由 WAL 收敛到已记录的 before/after；未知漂移失败关闭。
5. `doctor` 默认零写、零网络，不把无法安全判断的状态猜成可删除对象。
6. Linux/macOS CI 可重复完成 fmt、vet、test、race、coverage、build。
7. 同一提交和 Go 工具链可重复生成四个平台 archive 与 checksums。
8. V0.3 repair 只执行 fingerprint 绑定且满足完整前置条件的两类动作；support 输出不泄露原始路径、私有 repo、环境变量、WAL payload 或二进制。
