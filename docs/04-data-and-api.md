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
└── store/
    ├── .tmp/
    └── <name>/
        ├── original/<binary>
        └── <tag>/<binary>
```

`state.lock` 串行化同一 data root 上的 adopt、真实 upgrade 和 rollback。scan 与纯 dry-run 不持有写锁。Darwin/Linux 实现使用非阻塞 `flock`，拒绝把预置软链当作锁文件。

## Manifest schema

顶层：

```json
{
  "schema_version": 1,
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

`sha256` 与 `asset_sha256` 不是同一对象：归档解压、重命名后，活跃二进制 hash 通常不同于下载资产 hash。

新增审计字段保持可选，旧 schema v1 文件仍可读取。任何不兼容变更必须提升 `schema_version` 并提供迁移与回滚说明。

## 原子性与持久性边界

- manifest 使用同目录临时文件加 rename。
- 活跃二进制使用目标目录内完整临时常规文件、`fsync`、close 加 rename；不会让 PATH 名称指向正在交换的 symlink inode。
- store 的 name/tag/original/`.tmp` 子目录使用精确拼写解析；大小写别名、symlink 与非目录中间组件均失败关闭，避免写入或 Prune 越出 store 信任根。
- store、快照和活跃文件之间的复制只保证字节与 rwx 权限位。owner/group、ACL、xattr、mtime、setuid/setgid/sticky 等特殊权限位和 hardlink topology 不保留；`adopt` 拒绝带特权或特殊权限位的源文件。
- H1 对普通错误返回执行补偿恢复。
- 当前不承诺断电、`SIGKILL`、磁盘/文件系统损坏后的自动恢复。目录 `fsync` 和 WAL 仍属 H2；进程被强制终止时，live path 所在目录可能留下未生效的 `.hukou-tmp-*` 临时文件。见 [`08-risk-and-debt.md`](08-risk-and-debt.md)。

## GitHub API

- metadata：`GET /repos/{owner}/{repo}/releases/latest`，库层也支持按 tag 查询。
- token：`GITHUB_TOKEN` 优先，其次 `GH_TOKEN`。
- API 请求才携带 Authorization；下载与重定向按 host 白名单隔离。
- 429、5xx 与网络错误有限次退避重试；429 尊重有上限的 `Retry-After`。
- API 和下载都有总超时。
- 下载和解压默认有 512 MiB 上限。

## Checksum 语义

1. 优先查找 release 的 checksum 清单或 `<asset>.sha256[sum]`。
2. 通用清单接受 GNU coreutils 与 BSD `SHA256 (file) = hash` 形式；精确绑定的 sidecar 还可只包含单个 64 位摘要。
3. 找到 checksum asset 后，所选资产必须存在有效条目并匹配；缺失、格式无效或不匹配都中止。
4. 没有 checksum asset 时仍计算并保存 `asset_sha256`，但 `checksum_verified` 不得伪造为 true。
5. manifest 只在激活和审计字段都准备完成后保存。
