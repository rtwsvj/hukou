# 数据与外部 API

## 数据根目录

优先级：

1. `HUKOU_DATA_DIR`
2. `${XDG_DATA_HOME}/hukou`
3. `$HOME/.local/share/hukou`

```text
<dataRoot>/
├── state.lock
├── manifest.json
├── manifest.json.bak
├── transactions/
│   ├── .building-<id>/
│   ├── pending-<id>/
│   └── completed-<id>/
└── store/
    ├── .tmp/
    └── <name>/
        ├── original/<binary>
        └── <tag>/<binary>
```

`state.lock` 串行化同一 data root 上的 adopt、真实 upgrade 和 rollback。写命令获得锁后先恢复 transactions。scan、list、doctor 与纯 dry-run 不持有写锁；只读路径发现 pending 状态时报告或拒绝使用 hukou 归属。Darwin/Linux 实现使用非阻塞 `flock`，拒绝把预置软链当作锁文件。

## Manifest schema

顶层：

```json
{
  "schema_version": 2,
  "retention": { "rollback_depth": 2 },
  "entries": []
}
```

Entry 字段：

| JSON 字段 | 含义 |
|---|---|
| `name` | manifest 唯一名称 |
| `path` | 用户 PATH 中的活跃文件路径；新激活保持为常规文件，兼容旧 symlink 快照恢复 |
| `repo` | GitHub `owner/repo`；local 条目为空 |
| `tag` | 当前激活 tag，或 `local` / `original` |
| `sha256` | 当前激活二进制 SHA-256；本地完整性闸门使用 |
| `upstream` | 可推导的上游模块路径 |
| `adopted_at` | RFC3339 收编时间 |
| `updated_at` | RFC3339 最近成功变更时间 |
| `asset_name` | 最近成功升级所选 release asset 名 |
| `asset_sha256` | 下载归档本体的 SHA-256；用于来源审计 |
| `checksum_asset` | 实际使用的 checksum asset 名；没有时省略 |
| `checksum_verified` | 仅 checksum 验证成功时为 `true`；否则省略 |
| `active_activation_id` | 当前 activation event ID；必须指向 `activations` 最后一项 |
| `activations` | 不可变 activation event 序列，包含 id/parent/operation/tag/SHA/time/reverts |
| `update_policy` | `mode`、`channel` 与可选 `pinned_tag` |
| `retention` | 可选 entry 级 `rollback_depth`；缺失时继承顶层 retention |

`sha256` 与 `asset_sha256` 不是同一对象：归档解压、重命名后，活跃二进制 hash 通常不同于下载资产 hash。

schema 0/1 读取时确定性迁移为 2：每个 entry 只为“当前 tag/SHA”创建一个
synthetic `legacy` root，policy 使用兼容模式 `github-latest/stable`，不读取 store
mtime 猜测旧历史。decoder 先按声明 schema 选择允许字段：schema 0/1 若携带 v2-only
顶层 retention 或 entry policy/history 字段会直接拒绝，不能用 legacy migration
偷渡新语义。新收编条目默认 `semver/stable`。已声明 schema 2 的文件必须显式带齐
policy、retention 与有效 lineage；未知字段、future schema、重复 name/path、非法
digest/time/path/repo/policy、forward/missing parent/reverts、active/current 不一致都会
拒绝。V0.2 二进制会拒绝 schema 2，这是避免旧版本保存时丢字段的兼容门禁。

`manifest.Decode` 是 command transaction encoder、doctor 与 repair backup restore
共用的 strict boundary，三条路径不得分别实现宽松 JSON 解析。Backup 缺显式 v2
policy/history、携带孤立 checksum evidence 或未知字段时，同样不能成为 repair 候选。
当前 strict decoder 仍依赖 Go JSON 对象语义，不拒绝重复 JSON key；duplicate-key
rejection 是后续 defense-in-depth，不得把 unknown-field 检查解释成已经覆盖它。

### Activation lineage

- adopt 创建无 parent 的 root event。
- upgrade 创建以当前 active event 为 parent 的 child。
- rollback 创建新的 active event；其 parent 指向目标 event 的 parent，`reverts_id`
  指向操作前 active event。因此 `A→B→C` 连续默认 rollback 得到 `B→A`，不会回到 C。
- 显式 original restore 创建无 parent 的 rollback event，因为 schema v1 migration
  无法证明 original 属于 synthetic lineage；后续默认 rollback 不猜测去向。
- history/current 一起写进 transaction 的 after-manifest，不分两次提交。
- 每个 event tag 都必须通过安全单路径组件校验；同一 history 中同 tag 只能绑定同一
  SHA-256，历史或新事件试图把相同 tag 重新解释为不同字节时失败关闭。

### Update 与 retention policy

- 顶层默认 `rollback_depth=2`；entry 可覆盖为任意非负整数。
- `pinned_tag` 优先于 mode/channel；pin 已 active 时可零网络 no-op。
- Go update policy 的 SemVer 模式接受严格、可确定排序的 `X.Y.Z`（可带小写 `v`、
  合法 prerelease/build），并默认拒绝降级；release/install shell 则要求 v-prefix
  且刻意拒绝 build metadata。两条边界用途不同，分别有合法/非法矩阵。
  `github-latest` 是 legacy/non-SemVer 兼容模式。
- Prune plan 绑定 tag+SHA，保护 current、immutable original、已安装 pin 和最近 N
  个逻辑 ancestors。应用前重新校验全部 protected/delete refs；新出现但未列入 plan
  的版本保持不动。存在未决 transaction 时命令层不进入 prune。

## 原子性与持久性边界

- manifest 使用同目录完整临时文件、file sync、close、rename 与 parent sync；覆盖前把上一份可解析且 schema 受支持的内容保存为 `manifest.json.bak`。主文件或 backup 为 symlink/非 regular 时失败关闭；backup 仍由 doctor 做字段、重复项和 hash 格式等语义审计。
- 活跃二进制使用目标目录内完整临时常规文件、file sync、close、rename 与 parent sync；不会让 PATH 名称指向正在交换的 symlink inode。
- store 的 name/tag/original/`.tmp` 子目录使用精确拼写解析；大小写别名、symlink 与非目录中间组件均失败关闭，避免写入或 Prune 越出 store 信任根。
- store 的 mkdir、hard link、rename、remove 与 GC 在完成后同步受影响目录。
- store、快照和活跃文件之间的复制只保证字节与 rwx 权限位。owner/group、ACL、xattr、mtime、setuid/setgid/sticky 等特殊权限位和 hardlink topology 不保留；`adopt` 拒绝带特权或特殊权限位的源文件。
- H1 普通错误补偿与 H2 WAL 使用同一收敛目标：PREPARED 回滚 before，durable COMMIT 前滚 after。
- WAL 覆盖 hukou 协作写及其真实进程中断窗口；不宣称修复未知外部漂移、磁盘 bitrot 或文件系统损坏。见 [`08-risk-and-debt.md`](08-risk-and-debt.md)。

## Transaction journal schema

journal schema 与 manifest schema 独立。`intent.json` 记录 operation、name、每个 absolute resource path，以及 before/after 的 topology、SHA-256、rwx mode 与 journal-local payload。regular payload 不依赖 store 在恢复时仍完整存在；legacy symlink before 保存精确 link target。

`COMMIT` 是唯一不可逆方向标记。cleanup 前先把 pending 原子移动到 completed namespace，避免删除中断把已提交事务误识别成未提交。

## Repair plan 与 support report

Repair plan 是用户显式指定路径上的 schema v1 JSON，包含 action、opaque
data-root identity、前置条件、state fingerprint 和生成时间，不包含绝对 data root。
plan 文件使用 `0600` 原子写入。apply 不调用普通写命令的隐式 recovery，而是在
existing data root 上持锁后重算全部绑定；stale/ambiguous/unknown drift 失败关闭。
apply 可能创建/使用 `state.lock`，所以 stale 的保证是 live/store/manifest/backup/
journal 等业务状态零写，不是绝对文件系统零写。

Support report 是独立 schema v1 JSON：build、OS/arch、脱敏 doctor finding、匿名
entry policy/history 计数、transaction/store topology。它不包含 entry name/tag/path/
repo/upstream/asset/event ID、环境变量、用户名、HOME、二进制或 WAL payload。
`--format json` 写 stdout；`--output` 只写一个 `0600` 文件；两者都不联网、不上传。

## GitHub API

- metadata：legacy stable 使用 `GET /repos/{owner}/{repo}/releases/latest`；exact pin
  使用 `/releases/tags/{tag}`；SemVer/prerelease 使用 bounded `/releases?per_page=100&page=N`。
- release list 最多读取 10 页（1000 项），不跟随响应 `Link` 到其他 host；刚好填满
  安全上限而无法证明完整时失败关闭。
- token：`GITHUB_TOKEN` 优先，其次 `GH_TOKEN`。
- API 请求才携带 Authorization；下载与重定向按 host 白名单隔离。
- 429、5xx 与网络错误有限次退避重试；429 尊重有上限的 `Retry-After`。
- API 和下载都有总超时。
- 下载资产在 API size 未知时使用 512 MiB 上限；API 声明正数 size 时当前以该
  声明值作为精确长度与读取上限，尚无独立全局 ceiling。被选中的单个解压 entry
  有 512 MiB 上限，但 tar 目标选择的第一次全流扫描尚无总展开工作量/member 数预算。

当前 GitHub API JSON response 尚无独立 body byte cap；增加 cap 是后续
defense-in-depth。类似地，安装脚本会检查 archive 根和目标 member 唯一性，但总解压
体积与 member 数的膨胀预算仍是后续项。路径安全当前依赖逐组件校验与重检；采用
`openat`/目录 fd 锚定遍历以进一步缩小非协作 TOCTOU 也尚未实现。

## Checksum 语义

1. 优先查找 release 的 checksum 清单或 `<asset>.sha256[sum]`。
2. 通用清单接受 GNU coreutils 与 BSD `SHA256 (file) = hash` 形式；精确绑定的 sidecar 还可只包含单个 64 位摘要。
3. 找到 checksum asset 后，所选资产必须存在有效条目并匹配；缺失、格式无效或不匹配都中止。
4. 没有 checksum asset 时仍计算并保存 `asset_sha256`，但 `checksum_verified` 不得伪造为 true。
5. manifest 只在激活和审计字段都准备完成后保存。
