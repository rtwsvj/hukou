# PHASE2 解压与校验验收记录

## 完成内容

实现了 `internal/archive` 与 `internal/verify` 两个包及完整单测，覆盖规格中“解压与校验”一节要求。

## 新增文件

- `internal/archive/archive.go`
- `internal/archive/archive_test.go`
- `internal/verify/verify.go`
- `internal/verify/verify_test.go`

## 实现要点

### internal/archive

- `Extract(archivePath, destDir, wantName string) (binPath string, err error)`
- 按后缀分派：
  - `.tar.gz` / `.tgz`：标准库 `archive/tar` + `compress/gzip` 解压
  - `.tar.xz`：返回明确错误 `xz 暂不支持`
  - `.zip`：标准库 `archive/zip` 解压
  - `.gz` 单文件：解压后重命名为 `wantName`
  - 无后缀裸文件：直接拷贝并命名为 `wantName`
- 路径穿越防护：所有条目路径经 `filepath.Clean` 后必须仍在 `destDir` 内；绝对路径与 `../` 前缀均被拒绝
- 归档内二进制定位：
  1. 优先 `basename == wantName` 或 `wantName + ".exe"`
  2. 否则唯一的“可执行样”条目（`mode & 0111 != 0` 或无扩展名）
  3. 多个候选返回错误并列出条目名

### internal/verify

- `SHA256File(path string) (string, error)`
- `ParseChecksums(r io.Reader) map[string]string`
  - 支持 `hash  filename` 与 `hash *filename` 两种格式
  - 忽略空行与 `#` 注释行
- `VerifyAsset(assetPath, assetName string, checksums map[string]string) error`
  - 缺条目返回哨兵 `ErrNoChecksum`
  - 不匹配返回明确错误

## 验收结果

```text
$ go build ./... && go vet ./... && go test ./internal/archive/ ./internal/verify/
ok      github.com/rtwsvj/hukou/internal/archive
ok      github.com/rtwsvj/hukou/internal/verify
```

全部通过。
