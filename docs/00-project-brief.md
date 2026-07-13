# 项目简报

## 一句话目标

hukou 是一个 macOS/Linux CLI 工具管理器：先盘点机器上已有的可执行文件并判断来源，再把无主工具收编到可校验、可升级、可回滚的本地版本仓库。

## 目标用户

- 同时使用 Homebrew、Cargo、Go、npm、pipx、mise 等多种工具链的开发者。
- 手工下载或 curl 安装过散装二进制、希望补上来源与回滚能力的用户。
- 需要审计本机 CLI 来源，但不希望扫描过程联网或修改系统的人。

## 当前产品范围

- `scan`：PATH 扫描、来源责任链、表格与 JSON。
- `adopt`：登记并备份本地二进制。
- `upgrade`：GitHub Release 查询、资产选择、下载、校验和激活。
- `rollback`：切换到 store 中旧版本或 original。
- `list`：展示 manifest。
- `version`：展示发布版本、提交和构建时间。

## 明确不在当前范围

- 管理或代理 Homebrew/npm/Cargo 等已有管理器的升级。
- Windows。
- topgrade 集成、mise/Brewfile 导出。
- changelog diff、供应链风险评分和 GUI。
- 对断电或 `SIGKILL` 的完整事务恢复；H1 只覆盖可观测错误返回路径。

## 成功标准

1. `scan` 保持本地只读、无网络。
2. 未收编工具不会被 upgrade/rollback 修改。
3. checksum 和当前文件完整性检查失败时不切换安装。
4. 正常错误返回后，活跃文件、store 与 manifest 保持一致或恢复到旧状态。
5. Linux/macOS CI 可重复完成 fmt、vet、test、race、coverage、build。
6. 同一提交和 Go 工具链可重复生成四个平台 archive 与 checksums。
