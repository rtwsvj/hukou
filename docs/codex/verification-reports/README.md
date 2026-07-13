# Verification Reports

验证报告对某个确定 commit 核验 change record。

Verdict：`pass | partial | fail | inconclusive`。

最小内容：

- Verification ID、commit、OS、Go 版本
- Related change records
- Claims vs Evidence
- 命令、退出状态和关键输出
- release artifacts/checksums（如适用）
- 跳过项、限制与遗留风险

若验证后代码或配置继续变化，旧报告仍保留，但不再证明新 HEAD。
