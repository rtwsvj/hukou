# ADR-0003：持久化事务决策与只读 doctor

- Status: Accepted
- Date: 2026-07-14
- Supersedes: 无；扩展 ADR-0001/0002 的 H2 边界

## 背景

ADR-0002 保证 live path 的单次替换不会暴露半写文件，H1 补偿也覆盖普通错误返回；但 upgrade/rollback 仍需先后修改 live 与 manifest。进程若在两者之间被 `SIGKILL`，可能留下 `live=new / manifest=old`。仅给单个文件增加 `fsync` 或备份无法判断重启后应前滚还是回滚。

adopt 也有同类跨资源窗口：original 先创建、manifest 后保存。中断后会留下没有登记记录的 original，并阻止安全重试。

同时，项目需要一个不会自行改变现场的诊断入口。非当前 tag 往往是合法 rollback retention；损坏 manifest 下也无法可靠判断 store 所有权，因此 doctor 不能靠猜测自动删除数据。

## 决策

### 1. 单全局持久化事务

同一 data root 已由 `state.lock` 串行化写操作，因此采用单全局事务 WAL，而不是多事务并发日志。

事务先持久化 before/after 状态及恢复 payload，再发布 `PREPARED`。只有在 live 与 manifest 都 durable 后才持久化 `COMMITTED` 决策。

恢复规则固定为：

- `PREPARED`：幂等回滚所有资源到 before。
- `COMMITTED`：幂等前滚所有资源到 after。
- 已完成清理状态：只继续清理，不再回滚。
- 预检或写入前复核时，任一资源既不匹配 before 也不匹配 after：视为外部漂移或损坏，零覆盖失败并保留事务证据。

恢复在写命令取得 `state.lock` 后、GC、manifest 业务读取和网络请求前执行。`upgrade --dry-run` 必须继续保持零写；发现 pending transaction 时只报告并中止，不自动恢复。

### 2. 持久化顺序

需要 durability 的文件遵循：完整写入临时文件、文件 `fsync`、close、同目录 rename/link、父目录 `fsync`。事务决策文件和事务目录也遵循同一规则。

普通返回错误仍立即补偿；补偿若不能证明完成，就保留 WAL，让下次恢复继续收敛。

### 3. doctor 默认只读

`hukou doctor` 默认零写、零网络，不创建 data root、不获取会改写 PID 的 mutation lock、不运行 GC。

doctor 可以报告 manifest、live SHA/类型/权限、store 拓扑、临时残留、manifest 外 tool 目录与 pending transaction。manifest 无效时，store tool 只能标为不可分类，不能标成可删除 orphan。

首个版本不提供 `repair-all`，也不自动修改 live、manifest、original、retained versions 或未知临时文件。未来 repair 必须是显式枚举动作、重新审计并绑定 state fingerprint。

## 结果

### 正面

- crash 后的事务方向由 durable 决策决定，不依赖猜测时间戳或当前进程内存。
- recovery 可重入；恢复自身再次中断时仍按相同规则收敛。
- doctor 为运维提供统一、可机器读取的证据，同时保持现场不变。

### 代价

- 每次写操作增加 payload、日志和目录同步成本。
- WAL 只覆盖 hukou 协作写与明确绑定的 before/after；外部同时改写仍会失败关闭并要求人工判断。
- 普通 CI 可以验证状态机与真实 `SIGKILL` 窗口，但不能完全模拟硬件掉电与文件系统缓存重排；保证范围必须以支持目录同步的文件系统为边界。
- 状态分类和写入前复核不能为不合作的外部 writer 提供原子 CAS；最后一次复核与 rename/remove 之间仍有窄 TOCTOU 窗口。

## 非目标

- 本 ADR 不定义通用 store orphan 删除策略。
- 不把目录 mtime rollback 启发式升级为历史栈。
- 不宣称 Windows crash semantics 已验证。
- 不处理磁盘 bitrot、恶意 data root 所有者或文件系统本身损坏。
