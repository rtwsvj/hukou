# ADR-0004：Trust-first CLI 与管理器所有权边界

- Status: Accepted
- Date: 2026-07-14
- Implementation: fixed subject `1fa45a0` passed the recorded local/private RC gate;
  external audit and hosted execution remain pending

## 背景

hukou 已能扫描、收编、升级和回滚，但陌生用户在第一次写操作前缺少“为什么”和“会做什么”的可解释入口。同时，“一口气升级整台机器”具有传播价值，却会把 Homebrew、mise、aqua、npm、Cargo、GUI 和系统更新的不同语义与故障责任混在一起。

## 决策

1. V0.3 增加 `explain`、`adopt --dry-run` 和 `outdated`，形成先解释、先预览、再修改的信任阶梯。
2. 所有 read-only report 使用独立 schema，保持确定性英文 JSON。
3. dry-run 结果不是写操作授权；真正写入必须在 state lock 内重检现场。
4. `upgrade --all` 只处理 hukou manifest entries。
5. 全机升级通过 Topgrade custom command 集成；hukou 不执行其他管理器的升级。
6. V0.3 不推出语义不完整的跨管理器 plan/apply。

## 当前落实

- `explain`、`adopt --dry-run --json`、`outdated` 已接入；outdated 与 upgrade
  使用共享 update checker，写操作仍在锁内重检。
- `upgrade --all` 的 help 与实现只枚举 manifest entries；Topgrade 配置见
  [`../integrations/topgrade.md`](../integrations/topgrade.md)。
- Topgrade 失败只表示某个独立 custom command 失败，hukou 不接管其他 manager
  的重试、repair 或 rollback。

以上是当前分支实现状态，不是 V0.3 已发布或远端 CI 绿色的声明。

## 后果

- 用户可以在不信任 hukou 写入的情况下先获得价值。
- hukou 的强事务和回滚承诺只覆盖自己拥有的条目，不被外部管理器稀释。
- Topgrade 提供“一条命令升级全部”的现成入口，hukou 补上无人管理二进制的空白。
- 未来若实现控制平面，必须另立 ADR，采用 provider capabilities、固定 plan 和 Saga/reconcile，不宣称全局原子事务。
