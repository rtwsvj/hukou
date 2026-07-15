# ADR-0005：Manifest v2、激活历史、策略与窄版 repair

- Status: Accepted
- Date: 2026-07-14
- Implementation: fixed subject `1fa45a0` passed the recorded local/private RC gate;
  external audit and hosted execution remain pending

## 背景

schema v1 只记录当前状态。默认 rollback 依赖 store 目录 mtime，连续操作会在最近目录之间来回；固定保留数量不知道哪些 artifact 是真实历史目标；upgrade 只比较 tag 字符串；doctor 虽能报告异常，但没有可验证、可重放边界明确的修复协议。

## 决策

1. Manifest 提升到 schema v2，在 entry 内记录 activation lineage 和 update/retention policy。
2. schema 0/1 迁移只为当前状态生成 synthetic root，不猜测旧历史。
3. history 与当前 entry 进入同一 after-manifest，由现有 before/after WAL 一起提交。
4. 默认 rollback 走 parent lineage；retention 根据 lineage、pin 和 transaction references 构造保护集，不使用 mtime。
5. 更新策略显式区分 SemVer 与 legacy GitHub latest；支持 stable/prerelease 和 exact pin，默认不降级。
6. doctor 保持只读。repair 使用 `plan → apply`，绑定现场 fingerprint；V0.3 只开放 transaction recovery 与 manifest backup restore。
7. support bundle 默认脱敏、无网络、不自动上传。

## 当前落实

- schema 0/1 在内存中迁移为 synthetic legacy root；schema 2 load/save 对未知字段、
  policy、retention、digest/time/path 和 lineage 做严格验证。V0.2 拒绝 schema 2。
- 新 entry 默认 `semver/stable`；legacy migration 使用
  `github-latest/stable`。SemVer 比较使用锁定并记录许可证的
  `golang.org/x/mod/semver`。显式切换 semver 时会拒绝 local 或当前 tag 非严格
  SemVer 的 entry，避免 policy 保存后才发现没有可排序基线。
- rollback 走 parent lineage；显式 original restore 创建无 parent event，避免对
  legacy lineage 猜测。Prune 两阶段绑定 tag+SHA，并在 transaction 不 clean 时跳过。
- repair 只实现 `recover-transaction` 与 `restore-manifest-backup`；plan 写入用户显式
  指定的 0600 文件，apply 持锁重算 identity/fingerprint/preconditions。
- support report 使用匿名 entry 序号、枚举和计数，不复制 path/repo/name/tag、环境
  变量、用户名、二进制或 WAL payload。

这些实现已有固定提交的全仓、容器与 release snapshot 内部验收记录；外部审计和
GitHub-hosted gate 尚未关闭。ADR Accepted 只表示设计决定已采纳，不等于外部审计
通过或公开发布。

## 后果

- V0.2 会拒绝 schema v2；这是防止旧版本静默丢字段的刻意兼容门禁。
- migration、history、policy、retention 和 repair 都成为安全关键路径，必须进入 crash/fault matrix。
- repair-all、目录 quarantine、历史压缩和自助上传留给后续版本。
