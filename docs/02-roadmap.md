# 路线图

## 已交付

### Phase 1：scan

- PATH 遍历、类型识别、shadowed 处理。
- Tier 1 来源探测责任链。
- 表格/JSON 输出和错误/警告报告。
- macOS 本机探测器补丁。

### Phase 2：adopt / upgrade / rollback / list

- manifest 与版本 store。
- GitHub Releases 客户端、资产选择、归档解压与 checksum 解析。
- 命令层 mock e2e。
- 第一轮下载、路径与重定向安全加固。

“已交付”表示实现存在；当前 HEAD 是否通过以最新 verification report 为准。

## H1：安全硬化与首个 SemVer 发布

状态：**执行中**。

- [ ] checksum 缺条目 fail closed
- [ ] 下载资产 hash 与 active binary hash 分离
- [ ] upgrade/rollback 可观测失败补偿
- [ ] manifest/store 进程锁
- [ ] 纯本地 dry-run
- [ ] adopt 同名冲突保护
- [ ] hukou 来源完整性提示
- [ ] 当前文档事实源与 Codex 记录链
- [ ] Linux/macOS CI
- [ ] 四平台可重复打包与 checksums
- [ ] 全量验证报告
- [ ] `v0.1.0` GitHub Release

只有代码、CI、隔离 smoke 和发布资产全部有证据后，才能把 H1 标成完成。

## H2：运维与崩溃恢复

- WAL/事务日志或等价恢复机制。
- `doctor`/repair、manifest 备份与孤儿 store 检测。
- 可配置版本保留策略。
- 真实公共 fixture repo 的定时 smoke。

## 后续产品能力

- 版本快照、changelog diff、风险提示。
- topgrade custom command。
- mise/Brewfile 导出。
- 非 Go 二进制自动 repo 匹配。
- tar.xz 支持决策。
- Windows 设计与测试。
