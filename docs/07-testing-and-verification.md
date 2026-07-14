# 测试与验证

## 证据等级

| 等级 | 内容 | 能证明什么 |
|---|---|---|
| L1 静态 | diff、文件存在、配置解析 | 改动已落盘，不能证明行为 |
| L2 单测 | 包级 `go test` | 局部契约 |
| L3 全仓 | test、race、vet、build、coverage | 当前提交的工程基线 |
| L4 隔离 smoke | 临时 HOME/PATH/data root 的 CLI 流程 | 命令接线与文件系统行为 |
| L5 发布验证 | 四平台 archive、checksums、版本注入 | 发布链路 |
| L6 真实网络 smoke | 受控公共 fixture repo | GitHub API/CDN 集成；当前后置 |

历史 DONE 文档只记录当时声称的结果。新的“通过”结论必须写入 `docs/codex/verification-reports/`，并包含提交 SHA、命令、退出状态和关键输出。

## 必需门禁

```bash
make verify
```

`make verify` 展开为 fmt-check、module verify、vet、test、race、coverage 和 build；各 target 仍可单独运行。

同时执行：

- `git diff --check`
- `go mod verify`
- release 脚本静态检查与 snapshot 打包
- Linux amd64 archive 解包并执行 `hukou version`

## CI

`.github/workflows/ci.yml`：

- Ubuntu：格式、模块完整性、vet。
- Ubuntu + macOS matrix：test、race、build、binary smoke。
- Ubuntu：coverage profile 与 artifact。

CI 使用 `go.mod` 的 Go 版本，不维护第二份版本字符串。

## 关键失败注入

H1 至少覆盖：

- checksum asset 存在但缺所选文件条目。
- checksum 不匹配。
- 精确 checksum sidecar 的单摘要格式与 BSD checksum 格式。
- asset 下载 404、大小不符、超限。
- 下载期间外部修改 live binary，激活前二次 SHA 闸门拒绝覆盖。
- manifest 保存失败发生在 Activate 之后。
- rollback 保存失败发生在 Activate 之后。
- regular-file 激活始终只暴露完整旧内容或新内容；事务恢复仍覆盖 regular file 与旧版 symlink 两种输入拓扑。
- 写锁在同进程和子进程竞争时立即返回 `ErrLocked`，释放后可重新获取；锁路径软链被拒绝。
- adopt 同名冲突不覆盖 original/manifest。
- dry-run 不创建 data root、不 GC、不下载。
- PREPARED 的 live/manifest before/after 四种组合全部收敛到 before。
- durable COMMIT 后相同四种组合全部收敛到 after。
- transaction payload/COMMIT 损坏，或预检/写入前复核发现任一资源 unknown drift 时，零覆盖并保留 pending evidence。
- 子进程在 PREPARED/COMMITTED 窗口被强杀后，下一次 Recover 分别回滚/前滚。
- absent→regular/symlink 使用原子 no-replace，检查后竞争写不会被覆盖。
- doctor 在 data root 缺失、健康和损坏 fixture 上都保持零写；文本/JSON同源且稳定。

## 覆盖率

- profile 是 CI artifact，不进入 Git。
- H1 首先建立当前真实基线；在不知道基线前不虚构百分比门槛。
- 后续不得无说明降低总体覆盖率。
- `cmd`、store、manifest、verify、archive、ghrelease 是优先提高的安全关键包。

## 隔离要求

- 测试统一使用 `t.TempDir` 或 runner 临时目录。
- 不读取真实 manifest/store。
- 不把真实 PATH 交给 adopt/upgrade/rollback e2e。
- PR 测试使用 `httptest`，默认不访问公网。
- 真实网络 smoke 独立为手动/定时 workflow，使用只读 fixture repo 和最小权限 token。

## 验证报告最小字段

- Verification ID、日期、commit、OS、Go 版本。
- 实际运行命令及退出状态。
- Claims vs Evidence。
- 未运行/跳过项及原因。
- 生成的 release artifact 名与 checksums。
- 总结：pass、partial、fail 或 inconclusive。
