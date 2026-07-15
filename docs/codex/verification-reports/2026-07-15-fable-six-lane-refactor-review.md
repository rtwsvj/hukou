# Codex 改动核验报告:v0.1→v0.3 重构(afed279..88cb336)

## 元信息
- Verification ID: VERIFY-20260715-fable-six-lane
- 日期:2026-07-15
- Scope:afed279..88cb336 全部 23 个提交(H1 加固 / H2 恢复+doctor / v0.2 / v0.3 私有 RC / 审计包)
- 核验方:Fable 5 终审 + 4×Opus 子代理(事务恢复/声称核验/发布工程/命令契约)+ Kimi 盲区扫描 + ocr 双模型
- Related Change Records:docs/codex/change-records/ 下全部 6 条
- Verdict: **pass**

## 1. 核验结论

改动真实、质量高、无假完成。独立复跑 `go test ./... -count=1` = 641 tests / 21 packages 全过,覆盖率 72.9% 与 v0.3 记录逐字吻合;`-race`(cmd/store/transaction/manifest)通过;Phase 2 遗留的全部故障注入断言完好且为硬断言。事务 WAL 模型逐崩溃点推演收敛、恢复幂等自包含,零数据完整性必修 bug。发布工程(install.sh/release.sh/workflows)为 curl|sh 正面范本。scan JSON 契约零回归。记录诚实(CI 计费受阻如实标 infrastructure-blocked,历史覆盖率轨迹如实披露)。

## 2. Claims vs Evidence(摘要)

| ID | 声称 | 证据 | 状态 |
|---|---|---|---|
| H1 全部(checksum 严格解析/激活前二次 SHA/write-once original/case-alias+symlink-escape 拒绝/精确 tag+SHA Prune) | verify.go、upgrade.go:253-258、store.go:451/481/102-208/708 + 对应 e2e 硬断言 | 已完成 |
| H2(WAL+可重入恢复+doctor 零写) | transaction/{journal,recover}.go、cmd/helpers.go:48、cmd/doctor.go | 已完成 |
| v0.3(policy/repair/support/explain/outdated/英文化/SBOM/install.sh) | 各 cmd 已注册接线,SBOM 从打包产物提取(1fa45a0),install.sh:245-278 校验链 | 已完成 |
| 历史数字(325/79%,401/73.8%) | 属 v0.1/v0.2 时点,HEAD 为 641/72.9%,轨迹已如实披露 | 无法复现(已注明),非虚假 |
| GitHub 托管 CI 绿灯 | 全部标 infrastructure-blocked(计费),本地/容器替代验证 | 如实,未过度声称 |

## 3. 遗留改进清单(无阻断;已立卡 A-D)

- 中:①scan 表格不渲染 Warnings(事务残留时收编工具被静默误归)②CheckClean 对 building/completed 过度 fail-closed ③热路径重复 SHA256 全文件哈希(scan 0.6s→1.5s)+ manifest 批量 O(n²)
- 低-中:④transactions/ 未知条目楔死全部写命令,无 CLI 自愈 ⑤installer 未消费 CI 已产出的 Sigstore attestation
- 低:list 退出码契约变化未入 CLI reference;adopt spec 命令块缺 --dry-run/--json;ADR-0002 临时文件命名笔误(.hukou-tmp-* vs .hukou-txn-*);L6 真网络 e2e 门禁后置未关闭;setuid/sticky 位经事务被剥;release.yml tag 祖先校验晚于 make verify;repair 包 %v 包装破坏 sentinel;updatecheck 未透传 context;live 目录孤儿 .hukou-txn-* 无清理者

## 4. 命令/测试核验

| 命令 | 结果 |
|---|---|
| go build ./... && go vet ./... && go test ./... -count=1 | 全绿(641/21) |
| go test -race(cmd/store/transaction/manifest) | 通过 |
| gofmt -l / 生产代码汉字 sweep | 空 / 0 |
| scan --json 可解析 + schema 对比基线 | 零增删 |
| shellcheck 7 脚本 | 零告警 |

## 5. 建议下一步

卡 A(scan 可观测:Warnings 渲染 + CheckClean 收窄)、卡 B(事务未知条目隔离自愈)、卡 C(哈希去重+manifest 索引)、卡 D(attestation 消费+祖先校验前移+文档收账)。Eric 已于 2026-07-15 批准四卡并行,执行分层:Fable 设计 → Opus 子代理执行 → Codex(sol+high)逐卡核验。
