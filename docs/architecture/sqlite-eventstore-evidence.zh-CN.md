# SQLite 规范 EventStore 完成证据（中文阅读版）

**状态：** Slice 2 完整证据台账

**合同：** [SQLite 规范 EventStore — 已实现合同](sqlite-eventstore.md)

**分支：** `agent/sqlite-canonical-eventstore`

英文版本 [sqlite-eventstore-evidence.md](sqlite-eventstore-evidence.md) 是规范文本；本文是与之同步的中文阅读版。两者若有分歧，以英文为准。


## 提交

| 提交 | 任务 | 内容 |
| --- | --- | --- |
| `103204e` | 文档 | 架构门、双语规范与双语计划 |
| `9d7f187` | 任务 1 / PR 1 | 引入驱动、打开画像校验、版本门、完整形态迁移、失败封闭损坏路径 |
| `35523bd` | 任务 2 / PR 2 | 追加事务：精确重试、CAS、准许、领域身份、结果码分类 |
| `c310f59` | 任务 3 / PR 3 | 钉住分页、追加解析、命令请求查找、读写原子性 |
| `74c65f1` | 任务 4 / PR 4 | fencing 租约获取/续约、每次追加谓词、未知结果协议、一致性套件全绿 |
| `6ef089d` | 任务 5 / PR 5 | 备份、投影重建、基准、合同与证据发布 |

## 依赖与许可证清单

仓库第一个外部依赖。来自 `go.mod`：

| 模块 | 版本 | 角色 | 许可证 |
| --- | --- | --- | --- |
| `modernc.org/sqlite` | v1.56.0 | 纯 Go SQLite 驱动（直接） | BSD-3-Clause |
| `modernc.org/libc` | v1.74.4 | 驱动运行时（间接） | BSD-3-Clause |
| `modernc.org/mathutil` | v1.7.1 | 驱动工具（间接） | BSD-3-Clause |
| `modernc.org/memory` | v1.11.0 | 驱动工具（间接） | BSD-3-Clause |
| `golang.org/x/sys` | v0.47.0 | 驱动平台层（间接） | BSD-3-Clause |
| `github.com/dustin/go-humanize`、`github.com/google/uuid`、`github.com/mattn/go-isatty`、`github.com/ncruces/go-strftime`、`github.com/remyoudompheng/bigfft` | 按钉定版本 | 驱动间接依赖 | MIT |

观察时捆绑的 SQLite 为 3.5x 线，由打开时 `sqlite_version()` 门验证
≥ 3.42。linux、darwin、windows 的 `CGO_ENABLED=0` 构建全部通过。

## 验证证据

命令与观察结果（Apple M1，go 1.26.4）：

- `go build ./...`、`go vet ./...` —— 干净。
- `go test ./... -count=1` —— 全部 12 个含测试的包通过。
- `go test ./internal/harness/adapters/sqlite/ -count=1 -race` —— 通过，
  包括读写并发与并行追加串行化。
- `CGO_ENABLED=0 GOOS=linux|windows|darwin go build ./...` —— 全部通过。
- `eventstoretest.Run` 对 SQLite 适配器以零套件改动通过
  （`TestConformance`，全部十个用例）。
- 规范第 13 节测试类：结果码分类表（busy、busy 扩展、locked、full、
  IOERR、IOERR 扩展、interrupt、readonly、cantopen、corrupt、notadb、
  constraint、mismatch、internal）；带一次有界新连接查找的未知结果；
  并发连续提交位置（一致性用例）；终止后重开一致性；日志模式失败
  封闭校验（单元表）；真实繁忙争用的有界不可用实测；磁盘满与注入
  IO 通过与单元表相同的分类路径覆盖（CI 中无真实 ENOSPC 设备；记为
  GA 阻塞项）。

## 基准样本

`go test ./internal/harness/adapters/sqlite/ -run XXX -bench . -benchtime 50x`：

```text
BenchmarkAppend1Event-8       50   288240 ns/op   10102 B/op    259 allocs/op
BenchmarkAppend8Events-8      50   602344 ns/op   40037 B/op    828 allocs/op
BenchmarkReadStreamPaged-8    50  4624678 ns/op 2893281 B/op  66922 allocs/op
BenchmarkBackup-8             50  2150897 ns/op   13506 B/op    182 allocs/op
```

`ReadStreamPaged` 每操作读取完整 400 事件流（两页 256 记录），包括
每条记录的规范 JSON 解码。`Backup` 复制带校验的 100 追加数据库。

## 与已接受设计的偏差

1. 备份使用 `VACUUM INTO` 而非 Online Backup API：纯 Go 驱动未导出
   备份设施（`NewBackup` 只存在于内部类型）。SQLite 文档说明
   `VACUUM INTO` 生成同等的一致快照；副本在报告成功前经过校验。

## 延迟的 GA 阻塞项

- 进程级崩溃注入框架（WAL 写入中途的不清洁关停）。
- 对数据库文件的长时间浸泡与损坏模糊测试。
- 真实 `SQLITE_FULL` 设备级证据（分类已经过单元证明）。
- 租约谓词与 busy 测试之外的多进程写者证据。
