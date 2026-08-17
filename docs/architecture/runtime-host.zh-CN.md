# Runtime Host 与崩溃恢复 — 已实现合同（中文阅读版）

**状态：** 已实现；非 GA

**权威：** [Runtime Host 与崩溃恢复（Slice 4）设计](../superpowers/specs/2026-08-16-runtime-host-recovery-design.md)

**基线：** [SQLite 规范 EventStore](sqlite-eventstore.md)——租约机制、fencing 谓词、`session_heads` 投影

**包：** `internal/harness/runtime`

## 范围

Application 服务与 SQLite 存储之上的唯一 Runtime Host：带确定性恢复
追加的启动调和、带 fencing 反应的有界心跳循环、匹配对释放的优雅
关停、第二宿主诊断，以及后台审计导出器的生命周期归属。宿主只拥有
策略——没有领域规则、没有存储权威、不改动租约机制。

## 启动顺序

`Launch` 按父设计序列执行：打开（格式校验与迁移在内）、获取 Runtime
租约与 fencing 令牌、从 `session_heads` 枚举运行中候选（投影、经
重放确认）、追加恢复终态事实、就绪、启动心跳循环与后台导出器。
调和完成前命令不可用（`ErrNotReady`）；审计导出延迟绝不阻塞就绪。
无法获取租约的第二个进程以指名持有者的 `ErrLeaseHeld` 失败，不做
任何调和。

## 恢复转移

以活动会话、运行中 Turn 与运行中 Assistant Item 结束的重放追加一个
原子批次：先 `assistant.message.interrupted (code = process_crash)`、
后 `turn.interrupted (reason = process_crash)`。会话保持活动；原
`CommandID` 保持血统。恢复 `AppendID` 是会话、Turn、Item（或
`no_item` 哨兵）与 `process_crash` 在固定命名空间内的确定性哈希，
因此丢失确认时以完全相同的追加重试，重复调和解析到原回执。无活动
Item 的遗留运行 Turn 仅以哨兵关闭 turn。活动 Item 引用其他 Turn
拒绝；缺失 TurnStarted 血统拒绝。绝不自动重放模型或工具。

## 心跳与 fencing 反应

续约按有界间隔运行，截止期严格短于租约期限（启动时校验）。被
fence 的续约立即反应：停止接纳、经工作上下文取消本地工作、停止
导出器——不删除任何东西，所有权不确定时不尝试接管。截止期内的
瞬时存储不可用不吊销所有权：每次追加的存储谓词才是权威，而不是
续约往返。静止之后循环可经正常的过期接管路径（下一单调令牌）重新
获取并恢复接纳。

## 关停与导出器归属

`Shutdown` 停止接纳、取消在途工作、在调用方界限内等待循环，并
通过使租约过期来释放——更新精确匹配持有的 runtime ID 与 fencing
令牌，使过期宿主绝不能释放后继者的租约（Pi 规则）。后台导出器只
在就绪后按有界节奏启动，关停时停止。

## 排除项

- 每库多宿主、选主、集群。
- 自动模型或工具重试；`retryOfTurnID` 血统记录属于 Application
  命令层。
- ACP 与 TUI 面。
- GA 阻塞项：没有调和期间的进程级 kill 注入框架；心跳证据是确定性
  时间（`testing/synctest`）加脚本化租约结果，不是墙钟浸泡；除
  存储偏向安全的谓词外没有多机租约异常（时钟跳变）证据。
