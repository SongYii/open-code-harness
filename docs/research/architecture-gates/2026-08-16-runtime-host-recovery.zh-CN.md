# Runtime Host 与恢复架构门（中文阅读版）

**状态：** 完整研究证据

**日期：** 2026-08-16

**范围：** Slice 4（Runtime Host 与崩溃恢复）第一手来源重核查。记录
必需对照集当前公开的宿主、租约、崩溃检测与调和契约，以及宿主门的
采纳/拒绝边界。

本文档是研究证据。它不改变 Slice 2 交付的租约机制，也不授权复制
参考项目的类型。

英文版为规范研究记录，本文件是同步的中文阅读版。

英文版本 [2026-08-16-runtime-host-recovery.md](2026-08-16-runtime-host-recovery.md) 是规范文本；本文是与之同步的中文阅读版。两者若有分歧，以英文为准。


## 问题

1. 参考实现是否确认已接受的宿主设计——每库单宿主、每次追加 fencing、
   先调和后接令的启动、只追加终态事实的恢复？
2. Slice 4 应当采纳哪些观察到的崩溃检测与活性机制，哪些不足？
3. 除 Slice 2 门引用之外，是否有"仅建议性锁不安全"的第一手证据？

## 重核查的第一手来源

| 来源 | 观察状态 |
| --- | --- |
| [Grok Build](https://github.com/xai-org/grok-build) | `5163763`，2026-08-15 |
| [DeepSeek Harness](https://github.com/deepseek-ai/deepseek-harness) | `47f9438`，2026-08-13 |
| [Maka](https://github.com/maka-agent/maka-agent) | `2e3c82e`，2026-08-16 |
| [OpenAI Codex](https://github.com/openai/codex) | `9ded177`，2026-08-16 |
| [Pi agent core](https://github.com/badlogic/pi-mono) | `d3ab2af`，2026-08-16 |
| [Kleppmann《如何做分布式锁》](https://martin.kleppmann.com/2016/02/08/how-to-do-distributed-locking.html) | 2016-02-08，理论权威 |

## 观察到的契约与边界

| 来源 | 观察到的契约 | Slice 4 决策 | 边界 |
| --- | --- | --- | --- |
| Pi | SQLite 中的 `writer_lease(owner_id, fence, expires_at_ms)`："`writer_lease` enforces the single-writer rule... `open()` acquires the claim, storage renews it on appends and while idle, and close stops renewal after the queue drains and deletes only its matching `(owner_id, fence)` pair — so a stale owner cannot release the replacement that succeeded it." | 唯一实现了 fencing 的参考项目对 Slice 2 租约形态的独立确认。采纳"关闭只释放匹配 (owner, fence) 对"的规则用于优雅关停。 | 不复制其追加即续约策略；续约由我们的心跳拥有。 |
| Pi | "a JSONL session opened twice is corrupt and undetected"——其租约存在要防御的失败模式。 | 证实章程的单写者立场。 | — |
| Kleppmann | "the GC pause lasts longer than the lease expiry period, and the client doesn't realise that it has expired"；修复是 "a fencing token is simply a number that increases"，存储 "rejects the request" 携带过期令牌——前提 "the lock service generates strictly monotonically increasing tokens." | 已实现的每次追加令牌谓词的理论权威。租约过期本身绝不吊销在途写入；只有令牌校验的存储能拒绝它。 | — |
| Grok Build | 崩溃标记："Tracks open TUI sessions in `~/.grok/active_sessions.json` for crash recovery. Clean exit removes the entry; crash leaves it behind. On next launch, `collect_crashed` finds orphaned entries (dead PIDs)." | 采纳干净退出即释放的形态（我们的优雅关停使租约过期）。 | PID 活性是时间点检查，"no heartbeat, no lease, no fencing token"——不足以作为我们的权威。 |
| DeepSeek Harness | "A backend that reloads a log crashed mid-turn finds an open `turn/start` with no `turn/end`. It does not truncate… it closes the orphaned turn with a synthetic `turn/end { reason: { kind: 'interrupted' } }"；"`interrupted` is the one TurnEndReason no loop emits." | 证实孤儿 turn 的合成闭合与我们的 `process_crash` 恢复事实完全一致。 | — |
| DeepSeek Harness | "Repair applies only to cold sessions... an open live turn rejects rather than receiving synthetic interruption boundaries"；end-seed 标记 "NOT a liveness signal about other writers." | 采纳仅冷修复：调和只在启动获取租约之后运行，绝不对活动持有者运行。 | 其多写者的搁置（"needs a signal beyond the log"）已被我们的租约解决；不采纳日志形态的活性推断。 |
| Maka | "Resume Is Not Retry—How Maka Continues Safely from Crash Facts"；"Resume never resurrects the old process or disguises 'try again' as recovery."；"Desktop startup repairs state before it invokes a model"；停泊执行是 "Permanent v1 stop; no second attempt." | 证实先调和后接令的顺序与只追加终态、绝不自动重试的恢复。 | 其停泊状态机器对我们多余：我们的恢复是一个终态批次，不是可继续的操作停泊。 |
| Codex | 建议性 `flock` 写者锁加一次性陈旧清理；公开议题 [#36869](https://github.com/openai/codex/issues/36869)："Thread metadata updates and unarchive can bypass the per-thread writer lock." | （继 Slice 2 门之后）第二条直接证据：无 fencing 的建议性锁是不完整的强制。 | — |

## 拒绝的形态

1. **PID 活性作为活性权威**（Grok Build）：无心跳的时间点检查无法
   检测卡住的持有者。
2. **日志形态的活性推断**（DeepSeek Harness 的显式空白）：日志中
   未闭合的标记是生命周期证据，绝不是活动写者信号。
3. **可继续的停泊操作**（Maka 的当前权宜）：我们的恢复关闭执行，
   不停泊以待继续。
4. **追加即续约而非心跳**（Pi）：追加静默期不得悄悄延长租约；宿主
   显式拥有续约。

## 发现

### F1. 已接受的宿主设计在每个轴上都得到确认

每库单宿主、存储在每次追加时校验 fencing 令牌、先调和后接令的
启动、孤儿执行的合成 `process_crash` 闭合、以及绝不自动重放——
全部有直接的第一手或理论支持。

### F2. 除 Pi 外，Slice 2 的租约是参考项目中唯一的带 fence 租约

Pi 的 `writer_lease` 独立地与我们的形态吻合（持有者、fence、期限、
匹配对释放）。其他所有参考要么用建议性锁、PID 检查，要么完全搁置
多写者。

### F3. 采纳清单

获取租约之后仅冷修复；干净退出的租约释放（匹配持有者规则）；
先于模型的启动调和顺序；只追加终态事实的恢复；任何形式的停泊重试。

### F4. 拒绝清单

PID 活性作为权威；日志形态活性推断；可继续的停泊操作；追加即续约。

## 证据边界

- 观察是所列提交上的时间点快照；Maka 的调和器是有文档的未来工作，
  不是实现。
- 未对参考项目做运行时测试。
- 本门不实现任何东西；Slice 4 的规范与计划引用本文档。
