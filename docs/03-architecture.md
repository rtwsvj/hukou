# 架构

## 模块地图

| 模块 | 职责 | 关键边界 |
|---|---|---|
| `main.go`, `cmd/` | Cobra 命令编排 | 不把业务安全规则只留在帮助文案 |
| `internal/scan` | PATH 遍历、类型识别、shadowed | 只读文件系统，不联网 |
| `internal/provenance` | 来源责任链 | 首个匹配生效，hukou 自有登记优先 |
| `internal/output` | 表格与 JSON | 表格清理控制字符，JSON 保留完整错误 |
| `internal/manifest` | schema 与原子保存 | 向后兼容，写入由命令层锁保护 |
| `internal/store` | original/版本目录、激活、Prune、GC | name/tag/目标限制，软链同目录原子替换 |
| `internal/ghrelease` | GitHub API 与下载 | host 白名单、token 隔离、超时、大小限制 |
| `internal/assetpick` | 平台资产选择 | 无交互、结果确定性 |
| `internal/archive` | tar.gz/zip/gz/裸文件解包 | 防路径穿越与解压炸弹；不支持容器不得退化成可激活裸文件，裸资产还需可执行格式识别 |
| `internal/verify` | checksum 解析和校验 | checksum 存在但缺条目时由调用方 fail closed |
| `internal/buildinfo` | 发布版本元数据 | 由 release ldflags 注入 |

## scan 流程

```text
PATH + --dir
  -> scan.Walk
  -> provenance.DefaultRunner.Load
  -> 每个 Binary 走责任链
  -> output.Report
  -> table 或 JSON
```

责任链首位读取 hukou manifest；随后依次判断系统包管理器、版本管理器、语言包管理器、curl/local 路径、Go build info、system，最后 unknown。

## adopt 流程

```text
定位文件 -> 获取写锁 -> 校验 regular/executable -> 推导或读取 repo/tag
-> 来源安全闸（探测器加载失败也拒绝）-> 冲突检查
-> SHA-256 -> original 备份 -> manifest 原子保存
```

## upgrade 流程

```text
选目标 -> dry-run 或获取写锁 -> 当前 SHA 闸门
-> GitHub release -> assetpick -> 有界下载
-> checksum fail-closed -> 有界解压 -> store.Put
-> 激活前再次核对当前 SHA -> 捕获旧路径/manifest 状态
-> Activate -> 保存 manifest
-> 失败则补偿，成功后 Prune
```

网络只允许出现在 `internal/ghrelease`。真实升级前后的路径拓扑和 manifest 是一个逻辑事务；H1 覆盖可观测错误，H2 再覆盖进程崩溃。

## rollback 流程

```text
获取写锁 -> 当前 SHA 闸门 -> 选择 tag/original
-> 捕获旧状态 -> Activate -> 重算 active SHA -> 保存 manifest
-> 失败补偿旧状态
```

## 并发模型

- scan 可并发执行，因为不写数据。
- adopt/upgrade/rollback 对同一 data root 使用进程级锁。
- manifest 内部数据结构不承诺跨进程并发；命令层负责串行化。
- release 构建使用固定 commit、固定 Go 版本和固定归档时间戳。
