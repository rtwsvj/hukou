# PHASE2-STORE-DONE

## 任务

实现 `internal/store` 包（版本仓库）及完整单测，满足 `docs/specs/phase2-adopt-upgrade.md` 中“数据布局”与“替换模型”两节的要求。

## 新增文件

- `internal/store/store.go`
- `internal/store/store_test.go`

## 实现要点

- `Store{Root string}` 管理以下布局：
  - `<Root>/<name>/<tag>/<bin>` — 版本二进制
  - `<Root>/<name>/original/<bin>` — 收编时的原件备份
  - `<Root>/.tmp/` — 暂存区
- 提供方法：
  - `Put(name, tag, srcPath)`：经 `.tmp` 原子拷入版本目录。
  - `Versions(name)`：返回标签列表（按字典序）。
  - `Activate(name, tag, linkPath)`：在 `linkPath` 处原子创建/替换软链指向 store 内对应版本。
  - `AdoptOriginal(name, binPath)`：将实体文件移入 `original/` 并在原位放软链。
  - `Prune(name, keep)`：按目录 `mtime` 保留最近 `keep` 个版本，`original` 永不删。
  - `GC()`：清空 `.tmp/`。
- 提供工具函数 `SHA256File(path)` 进行流式 SHA-256 计算。
- 仅使用标准库；未使用 `os.Getenv` 或 `time.Now`（`Prune` 仅使用 `os.Stat` 返回的 `ModTime`）。

## 测试覆盖

单测覆盖：

- `Put` 与持久化验证
- `Versions` 排序
- `Activate` 切换版本与回滚旧 tag
- `AdoptOriginal` 后原位为软链且 `original/` 保留实体
- `Prune` 保护 `original/` 并按 `mtime` 保留指定数量版本
- 软链替换原子性（并发观察 100 次切换无断链/无异常内容）
- `GC` 清空 `.tmp/`
- `SHA256File` 结果正确

## 验收结果

```text
$ go build ./... && go vet ./... && go test ./internal/store/ -v
=== RUN   TestPut
--- PASS: TestPut (0.00s)
=== RUN   TestVersions
--- PASS: TestVersions (0.00s)
=== RUN   TestActivateSwitchAndRollback
--- PASS: TestActivateSwitchAndRollback (0.00s)
=== RUN   TestAdoptOriginal
--- PASS: TestAdoptOriginal (0.00s)
=== RUN   TestPruneKeepsOriginal
--- PASS: TestPruneKeepsOriginal (0.00s)
=== RUN   TestSymlinkAtomicReplace
--- PASS: TestSymlinkAtomicReplace (0.02s)
=== RUN   TestGC
--- PASS: TestGC (0.00s)
=== RUN   TestSHA256File
--- PASS: TestSHA256File (0.00s)
PASS
ok  	github.com/rtwsvj/hukou/internal/store	0.344s
```

全仓库测试亦通过：

```text
$ go test ./...
?   	github.com/rtwsvj/hukou	[no test files]
?   	github.com/rtwsvj/hukou/cmd	[no test files]
?   	github.com/rtwsvj/hukou/internal/assetpick	[no test files]
?   	github.com/rtwsvj/hukou/internal/ghrelease	[no test files]
ok  	github.com/rtwsvj/hukou/internal/manifest	(cached)
ok  	github.com/rtwsvj/hukou/internal/output	0.416s
ok  	github.com/rtwsvj/hukou/internal/provenance	0.305s
ok  	github.com/rtwsvj/hukou/internal/scan	0.430s
ok  	github.com/rtwsvj/hukou/internal/store	0.267s
```

## 完成状态

已完成。仅修改 `internal/store/` 与 `docs/records/PHASE2-STORE-DONE.md`。
