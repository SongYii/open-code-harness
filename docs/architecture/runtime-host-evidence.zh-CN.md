# Runtime Host 完成证据（中文阅读版）

**状态：** Slice 4 完整证据台账

**合同：** [Runtime Host 与崩溃恢复 — 已实现合同](runtime-host.md)

**分支：** `agent/runtime-host-recovery`

英文版本 [runtime-host-evidence.md](runtime-host-evidence.md) 是规范文本；本文是与之同步的中文阅读版。两者若有分歧，以英文为准。


## 提交

| 提交 | 任务 | 内容 |
| --- | --- | --- |
| `056d0ff` | 文档 | 双语规范与计划（连同 Slice 3 文档与两份门） |
| （runtime 实现） | 任务 1–4 | 调和、宿主骨架与启动顺序、带 fencing 反应的心跳、匹配对释放的关停、导出器接线 |
| （本台账） | 任务 5 | 合同与证据 |

## 验证证据

命令与观察结果（Apple M1，go 1.26.4）：

- `go test ./... -count=1` —— 全部 13 个含测试的包通过。
- `go test ./internal/harness/runtime/ -count=1 -race` —— 通过（脚本化
  租约结果有互斥保护）。
- linux、darwin、windows 的 `CGO_ENABLED=0` 构建。
- 调和矩阵：被打断的 assistant item 以终态对与原血统关闭；第二次
  调和幂等（确定性 `AppendID`）；遗留 no-item turn 仅关闭 turn；
  干净流无操作；`ActiveSessions` 只枚举投影为活动的会话；Item 与
  Turn 不匹配、血统缺失均拒绝。
- 确定性：`recoveryAppendID` 跨推导稳定；会话、item 与 `no_item`
  哨兵各自改变 ID。
- 心跳（经 `testing/synctest` 的确定性时间，Go 1.26 `synctest.Test`）：
  被 fence 的续约停止接纳并取消工作上下文而循环继续轮询；截止期
  内的瞬时不可用继续；静止后的接管重新恢复接纳并尝试重新获取；
  配置边界校验（截止期严格介于间隔与租约期限之间）。
- 生命周期：launch 调和崩溃数据库并带持久终态事实地就绪；第二个
  进程收到指名持有者的稳定 `ErrLeaseHeld`；关停释放租约使后继取
  下一令牌；后台导出器只在就绪后发布 manifest。
- 架构：runtime 包受依赖规则治理（只允许 application、domain 与
  sqlite 适配器；禁止 engine、tools、policy、testkit 及其他适配器）。

## 与已接受设计的偏差

无。心跳截止期语义区分被 fence 的续约（立即反应）与瞬时不可用
（截止期内容忍），设计将其留给实现，此处记录为冻结行为。

## 延迟的 GA 阻塞项

- 其他完整 Launch 阶段的进程级崩溃注入。2026-09-15 的
  [后续证据](provider-replay-evidence.md)已在生产恢复器 COMMIT 前后增加
  8 次真实进程强杀和 8 个放行对照，继任者使用真实 Launch。再增加的 4 次强杀／
  4 个对照覆盖混合多会话中第 2、第 4 次恢复提交前后，验证保留已提交恢复、重新
  发现待处理会话；尚未覆盖所有启动阶段，也不承诺全数据库原子恢复。
- 心跳节奏对真实租约过期的墙钟浸泡。
- 除存储偏向安全的谓词外的时钟异常（跳变）证据。
- ACP 客户端驱动的重启流程（Slice 5 范围）。
