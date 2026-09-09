# 版本化 System Prompt 与 Append-Only Workspace 指令设计（中文阅读版）

**状态：** 已于 2026-09-05 接受；2026-09-07 完成细化并授权实施。此次细化把模型可见角色固定为当前 domain 可重建的 durable `user` message，临时 source 故障改为保留最后确认状态，并固定 v1 的容量与超预算取舍；这些决定取代早期的 `developer/context`、source fail-closed 和全有或全无预算表述。

**日期：** 2026-09-04

**稳定性：** v1.0 前 prompt format、instruction event 和配置均为 `internal`。

**规范源：** [英文设计](2026-09-04-system-prompt-workspace-instructions-design.md)。若中英文语义不一致，以英文为准。

**调研依据：** [Agent 指令与安全文件修改架构门](../../research/architecture-gates/2026-09-04-agent-instructions-and-file-mutation.zh-CN.md)

## 问题与决定

项目已有 provider-neutral request recording 与 Context Engine，却没有明确的 coding-agent prompt identity 和 workspace instruction lifecycle。每 turn 重读并改写最早的 system message 能发现变化，却会从第一个变化 token 起损失 prefix cache；只在 session 开始读一次则会忽略仓库后来合理更新。

采用固定、版本化、model-neutral 的 `och_coding_agent_v1`，再把 `AGENTS.md` baseline 与 set/replace/remove delta 作为带 harness framing 的 durable `user` 消息追加在历史末尾。每次 Provider dispatch 前先持久记录 exact delta，再记录 exact request。Compaction 把已覆盖的 deltas rebase 成一个 durable effective snapshot。

第一版只认 admitted workspace 内名字完全等于 `AGENTS.md` 的文件，不读 home/global 文件，不兼容 `CLAUDE.md`/`GEMINI.md`，不支持 includes/remote/executable directive，不按 provider 分叉 prompt，也不把仓库 prose 当授权。

## Request layout 与缓存

顺序固定为：

```text
system: och_coding_agent_v1 固定 bytes/digest
user/context: root AGENTS.md baseline（若存在）
已有 canonical conversation/tool history
user/context: 零或多个 append-only instruction delta
当前 user/tool continuation
```

当前 provider-neutral domain 没有 developer role，因此 workspace message 固定使用 `domain.PromptRoleUser`；harness 自有 framing 会标明来源、scope，并明确它不能覆盖 system、operator、Policy 或用户当前直接要求。System prompt 包含工具纪律、workspace scope、stale-file 恢复、Policy/Approver 优先级、简洁进度和“未经验证不得声称成功”，不含 model name、credential、时间戳、Session ID 或 workspace-specific 内容。任何 byte 变化必须同时改 semantic version/digest 和 golden test。

每个 preparation boundary 的扫描如果没有变化，请求 bytes 完全不变，因此不会因为“检查了一遍”而掉缓存。发现变化时只追加 suffix，严禁改写旧 system/baseline message；旧前缀仍可复用。Provider 是否实际命中由其实现决定，live evidence 只能报告，不能臆测。

## 发现、层级与变化

Session 首次准备时读取 root `AGENTS.md`，明确 absence 也进入 registry。Structured filesystem tool 解析目标后，Application 从 workspace root 到目标 parent 发现目录链；每目录最多一个 `AGENTS.md`，由浅到深排序，越深对自己的 subtree 优先级越高。Symlink 仍受 workspace jail 限制。

Registry 为每个已发现路径保存 scope、present/absent、opaque version、content digest 与上次接受的 bytes。每次 Provider preparation 重查 root 和 active route 已发现路径；新目录在触碰其 subtree 的工具动作之后、下一次 Provider call 之前检查。

差异合并成一个确定性 delta：`set(path, scope, digest, content)`、`replace(path, scope, priorDigest, digest, content)` 或 `remove(path, scope, priorDigest)`。同边界多变化按 normalized depth/path 排序。Rendered message 明确旧内容已被替代或删除，但绝不修改旧 event/message。新的 source failure episode 可以追加 operations 为空、effective-set digest 不变的 diagnostic-only event；同一进程内同 path/class 的连续失败只报告一次，完整成功 probe 会解除抑制，进程重启则开始新 episode。没有 operation 和新 diagnostic 的健康扫描不追加内容。

`FileVersion` 只是跳过正文读取的快速线索，真正的内容身份是 digest：version 变化而 digest 不变时不追加消息。只有 confirmed absence 才移除旧指令。Unreadable、非 regular、非法 UTF-8、超过单 source 上限、canceled 或 unstable read 都视为 temporarily unavailable：保留最后确认状态，记录一次 bounded path/class diagnostic，并在下一 boundary 重试；没有旧状态时暂不加载该 source。一个 source 失败不阻止同批其他已确认变化。

## Durability、compaction 与 restart

Provider dispatch 前先 append `workspace.instructions.recorded`，包含 format、prompt ID/digest、relative path/scope、有序 operations、old/new digest、有序 bounded source diagnostics、exact rendered message bytes 和 resulting effective-set digest；diagnostic 可以在 operations 为空时单独存在。只有它明确提交后才构造并持久化 `model.request.recorded`；后者仍是 dispatched envelope 的最终重建权威。Unknown outcome 沿用现有 resolve-before-effect 规则。

Instruction event 不交给 summarizer 改写。Context checkpoint 覆盖 instruction messages 时，保存排序后的 effective files/scopes/bytes/digests、prompt identity、instruction epoch、event coverage/aggregate digest 和唯一 deterministic rendered snapshot。之后 materialization 依次使用固定 system prompt、checkpoint summary/reset marker、instruction snapshot、保留的 conversation tail、later deltas 和 current input，不再发送已被替代的旧 delta；canonical events 永不改写。

重启从 durable events/checkpoint 恢复上次 model-visible 状态，下一次 Provider call 前再核对 live workspace，变化作为新 delta。此前从未触碰的 subtree 仍等到被工具发现。

## 安全、预算与验收

Repository instruction 是 bounded、delimited、明确标记的不可信内容。它不能改变 tool risk、approval mode、workspace admission、sandbox、credential 或 event authority。包装结束标记必须转义。单个 source 最多读取 1 MiB；每个请求渲染的 effective instruction context 最多 64 KiB，并继续计入 Context Engine 普通 input budget；每个 session 最多追踪 256 个 discovered instruction path，root 预留一个名额，超出的新 path 在读取前跳过并产生 diagnostic，且不会驱逐已接受 source。超总量时优先保留最具体 source，先整份舍弃更宽泛 source，最后才截断最具体 source；模型可见消息和 audit 必须确定性列出 omitted/truncated path，不能静默省略。

验收覆盖：system prompt golden bytes/digest；root presence/absence；浅到深优先级与按触碰发现 nested file；set/replace/remove 顺序与 diagnostic episode 去重；健康 no-change 的零 event/零 request 差异；delta 前的 prefix byte-stable；version 变而 digest 不变；confirmed absence 与 temporary failure 的区别；1 MiB/64 KiB/256 path、specific-first 预算、framing escape、symlink 与 durable failure；指令无法授权 denied tool；两个 durable record 的严格顺序；restart recheck 与 corrupt snapshot 拒绝；summary/reset compaction rebase 等价与长期 bounded；并发 race；Application/ACP 双路径。

可选 live lane 使用用户提供的 DeepSeek OpenAI-compatible key，在明确 consent 和离线检查之后才读取凭证，记录 request token、endpoint 若提供的 cached-token 证据，以及 exact prompt/delta digest。它不是普通 PR 门。

完成还需 implemented contract、中英文同步、evidence ledger 与 prompt 变更流程。书面复核通过后才写实施计划。
