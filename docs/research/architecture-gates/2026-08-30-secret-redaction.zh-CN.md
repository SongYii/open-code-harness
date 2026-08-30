# Secret 脱敏架构调研门

**状态：** 调研证据完成

**日期：** 2026-08-30

**范围：** [`SECURITY.md`](../../../SECURITY.md) 的"未强制执行"清单里写得很直白："没有 secret 脱敏。Provider 凭证活在 adapter 配置里；事件负载和工具结果在被持久化或发出之前不会被扫描 secret。"本文先摸清本项目自己代码里已经有的东西（见"本项目自己已有的东西"），再重新核实六个参考 agent 项目在当前钉住状态下真正的 secret 处理机制，为后续设计打地基。本文不做任何设计或实现。

英文版本 [2026-08-30-secret-redaction.md](2026-08-30-secret-redaction.md) 是规范文本；本文是与之同步的中文阅读版。两者若有分歧，以英文为准。

## 对照集与钉住的 commit

按文档规则第 8 条抓取、直读源码；按第 7 条，六个项目今天全部重新核实：`kimi-code`、`grok-build`、`pi`、`deepseek-harness` 自本项目自己的 MCP 客户端适配器调研门今天早些时候核实过之后没有变过；`codex` 和 `maka-agent` 各自往前走了一个 commit，已重新抓取并 checkout 到新 commit——对本文引用的具体文件做了 diff，确认内容没有漂移。

| 项目 | 仓库 | Commit | 观察日期 | 为什么读它 |
| --- | --- | --- | --- | --- |
| Codex | `openai/codex` | `94cbbdd` | 2026-08-30 | Rust；有专门做的 `RedactedString` 类型，以及带默认 secret 名字排除规则的 shell 环境策略 |
| Kimi Code | `MoonshotAI/kimi-code` | `cbe0a77` | 2026-08-30 | TypeScript；这套对照集里唯一一个有真正通用自由文本 secret 模式脱敏器的项目 |
| Grok Build | `xai-org/grok-build` | `bc7f02e` | 2026-08-30 | Rust；对凭证持有类型"构造时脱敏"这个模式的第二个独立实例 |
| Maka | `maka-agent/maka-agent` | `5d519d6` | 2026-08-30 | TypeScript；一套受管 secret 注入系统——查过之后判定是一个*不同*的问题（见"Maka"一节） |
| DeepSeek Harness | `deepseek-ai/deepseek-harness` | `0a53fb5` | 2026-08-30 | TypeScript；terminal 工具里有个文件字面意义上就叫 `sanitize.ts`——直接查过，排除掉了（见"DeepSeek Harness"一节） |
| Pi | `earendil-works/pi` | `853a80d` | 2026-08-30 | TypeScript；按第 7 条直接查证，而不是想当然沿用之前某份调研门里跟这次问题无关的表述 |

## 本项目自己已有的东西

- **一个真实、已测试、但覆盖面很窄的先例已经存在。** `internal/harness/adapters/openaicompat/classify.go` 的 `redactSecrets` 跑四个硬编码正则——`reAuthorization`（`Authorization: ...`）、`reBearer`（`Bearer ...`）、`reSecretKey`（`sk-[A-Za-z0-9_-]+`）、`reQueryKey`（`?key=...`/`&key=...`，保留参数名）——被 `safeMessage`/`startupFailure` 调用，构造出每一个 `engine.ProviderFailure.SafeMessage`。这东西存在的具体原因是 provider 自己的 HTTP 错误响应体可能会把请求头或者形似 API key 的子串原样回显；由 `TestProviderFailureErrorNeverRendersSecrets` 验证。它在这个代码库里的其他任何地方都没被调用过。
- **Provider API key 本身是靠结构性手段而不是扫描来保护的——但只是靠约定，不是靠类型系统强制。** `APIKeySource.APIKey()`（`openaicompat/model.go`）在发请求时才被调用，返回值直接注入出站 `Authorization` 头；`Model` 结构体从不在那次调用之后继续持有它。注释"Implementations must not log it"是靠代码评审来执行的纪律规则，不是类型系统的保证——没有任何东西能阻止未来某一行代码把一个 `APIKeySource` 的值像普通字符串一样格式化进日志行。
- **工具调用结果原封不动地流过每一跳，中间没有任何扫描。** `domain.CompleteToolCall.Content`/`ToolCallFailed.Message`（`read_file`、`list_dir`、`exec` 产出的原始字节）被原样持久化成领域事件、原样复制进 JSONL 审计轨迹、原样投影到 ACP `session/update` 的 `tool_call_update.content`——`session/load` 重放路径本来就是这样（这个 session 开始之前就是），而且从本 session 自己那次 `internal/harness/adapters/acp/project.go` 改动开始（见[对话面与会话转录证据台账](../../architecture/conversation-and-transcript-evidence.md) 2026-08-30 那条 Update），**实时**路径现在也是这样了。那次改动关掉了一个已经记录在案的实时/重放工具卡片保真度缺口；诚实地说，它也是一个新增的投影调用点——将来这份调研门带出的设计一旦落地，这个点也得记得套上脱敏逻辑，这里点出来是给那份设计用的，不是在汇报一个回归。

## 逐项目发现

### openai/codex——凭证持有类型构造时脱敏，子进程拉起前的黑名单过滤

三个各自独立、范围更窄的机制，没有一个会扫描工具产出的自由文本：

- **`RedactedString`**（`codex-rs/utils/redacted-string/src/lib.rs`）：一个 newtype 包装类型，它的 `Debug` 实现永远打印 `<redacted>`，不管被包的值是什么。只有显式调用 `.into_inner()` 才会泄露，一次意外的派生 `Debug`/日志打印不会——防住这个常见错误的是编译器，不是评审者。
- **`ShellEnvironmentPolicy`** 的默认排除规则（`codex-rs/protocol/src/shell_environment.rs:125-130`）：shell 命令拉起之前，任何名字匹配 `*KEY*`、`*SECRET*` 或 `*TOKEN*`（大小写不敏感）的继承环境变量会被剔除，叠加在已配置的显式继承/排除策略之上——对一个默认继承面很宽的环境做黑名单过滤。
- **`SanitizedGitUrl`**（`codex-rs/protocol/src/sanitized_git_url.rs`）：一个智能构造器类型，在构造时就把 git 远程 URL 里形如 `user:token@` 的内嵌凭证剥掉，而且对一个嵌套语法可能夹带任意 secret 进可执行命令行的 remote-helper URL 直接拒绝（不是剥掉，是拒绝）。

三个都是针对一个具体、已知的数据形状（Codex 自己构造的一个类型、一个环境变量的名字、一个 URL）——没有一个是扫描工具真正产出的自由文本。

### xai-org/grok-build——同一个构造时脱敏模式，独立出现的第二例

`AuthManager` 手写的 `Debug` 实现（`crates/codegen/xai-grok-shell/src/auth/manager.rs:188-196`）只打印 `AuthManager` 且不带任何字段，文档注释明确写着这么做是为了"让 `AuthManager`……绝不会把凭证泄露进日志或 panic 信息里"。这是 Codex 的 `RedactedString` 所代表的那个类型级模式的第二个、独立出现的实例——两个互不相关的 Rust 代码库都收敛到"让这个错误在自己拥有的类型上结构性地不可能发生"，而不是去扫描代码本身并没有构造成凭证的内容。

### MoonshotAI/kimi-code——唯一找到的自由文本 secret 扫描器，但只用在自己的日志上

`redactCtx`（`packages/agent-core-v2/src/_base/log/formatter.ts`）是一个真正通用的双层脱敏器：

1. **结构化、按键名匹配：** `REDACTED_KEYS` 是一个固定的、归一化过的键名集合（`authorization`、`apikey`、`token`、`refreshtoken`、`accesstoken`、`idtoken`、`password`、`secret`、`clientsecret`、`apisecret`、`cookie`、`setcookie`、`bearer`）。递归遍历一个任意的结构化日志 context 对象，任何键名命中这个集合，对应的值就被替换成 `[REDACTED]`，带循环检测（`WeakSet`）和深度上限（`REDACT_MAX_DEPTH = 10`），确保一个畸形或有环的 context 不会把日志器挂死或崩溃。
2. **自由文本、按正则匹配：** `RAW_SECRET_PATTERNS` 是一小组正则，匹配形如 `key: value`/`key=value` 的子串——`authorization: bearer ...`、`api_key=...`/`access_token=...`/`refresh_token=...`/`id_token=...`/`token=...`/`password=...`/`secret=...`、`cookie=...`——只替换值部分，保留匹配到的键/前缀。

直接核实过（`packages/agent-core/src/logging/logger.ts`、`index.ts`）：`redactCtx` 只从 Kimi Code 自己内部的诊断日志管线里被调用。它从没被用在工具调用结果、助手消息，或者任何写进持久化会话记录、或者会暴露给模型的东西上——而这正是本项目自己 `SECURITY.md` 那条缺口点名的那几个面。

### maka-agent/maka-agent——一个不同的、相邻的问题：安全注入，不是脱敏

`ActivationSecretSink`/`ManagedSecretStore`（`packages/storage/src/activation-secret-injector.ts`）解决的是"把一个凭证安全地送**进**一个隔离的工具执行环境"：secret 活在一个受管的存储里，被间接引用（`ManagedSecretReference`），只在一次沙箱化 activation 前才被物化进一个全新的、隔离的环境覆盖层——明确写着绝不用宿主全局的 `process.env`，这样并发的 activation 之间不会看到彼此的值。这跟本项目自己的缺口方向正好相反（本项目的缺口是 secret 通过工具结果和事件负载往**外**流，不是凭证怎么安全地往里送）。这里点出来是提醒设计阶段别把两个问题搞混：本项目今天完全没有"给工具用的受管 secret"这个概念——唯一在范围内的凭证是 Provider API key，已经靠结构性手段处理过了（见"本项目自己已有的东西"）。

### deepseek-ai/deepseek-harness——一个假线索，直接查过并排除

`packages/terminal/terminal-bash/src/sanitize.ts` 的 `TerminalSanitizer` 干的是从 PTY 输出里剥离 ANSI/OSC/CSI 终端控制序列，为了按行渲染（`normalizeTerminalText`、提示符标记检测）。这里的"sanitize"指的是终端转义序列剥离，跟 secret 脱敏完全是两回事。整份读完了才下的结论，不是看文件名就假定它相关——这也是本文自己坚持的标准：一个线索要直接查过才能引用。

### earendil-works/pi——没找到任何 secret 脱敏机制，直接查过

这个代码库里唯一命中"redact"的地方是 Anthropic 风格"thinking"内容块上的一个 `redacted` 字段（`packages/server/src/protocol.ts:42,266`）——这是一个 provider 特定的含义（隐藏/脱敏的扩展思考内容），跟 secret 脱敏无关。`packages/agent`、`packages/coding-agent`、`packages/server` 里哪都没有类似 `REDACTED_KEYS` 的结构，也没有任何 secret 模式正则。这是一个直接查证过的真实负面发现，不是从之前某份跟这个问题无关的调研门表述里想当然推出来的"没有"。

## 交叉综合

- **两个独立项目（Codex、Grok Build）收敛到对凭证*持有*类型做构造时脱敏**：在类型层面压掉 `Debug`/序列化，让那个常见错误——一次意外的日志行、一个派生 trait、一条 panic 消息——被编译器挡住，而不是靠评审者记住一条规则。本项目自己的 Provider API key 今天得到的是等价的保护，但只是靠约定（一句代码注释），不是靠一个让这个错误结构性地不可能发生的 Go 类型。
- **本项目自己 `SECURITY.md` 点名的那个真实缺口——扫描工具或模型*产出*的内容（不是代码自己持有的凭证类型）里形似 secret 的子串，在持久化或发出之前——这六个项目的对照集里只有一个先例**（Kimi Code 的 `redactCtx`/`RAW_SECRET_PATTERNS`），而且这个先例也只用在内部诊断日志上，从没用在工具输出、会话内容，或者任何模型可见、会被持久化的东西上。**这个对照集里没有一个项目解决了这份调研门本来要研究的那个真实问题。** 接下来的设计不是在采纳一个已经收敛的行业模式——它要关闭的是一个整个对照集都还开着的缺口，不只是本项目一家。这里直说，不暗示这是一个"别人早就解决、本项目还没跟上"的问题。
- **本项目自己已有的 `redactSecrets` 才是最贴近、最该被扩展的先例，不是这几个外部项目里的哪一个。** 它的机制（一小组硬编码正则，在一个人类/模型可见的字符串被构造出来的那个点上应用）跟 Kimi Code 双层设计里的自由文本正则那一半在结构上是一回事；另外那一半（按键名的结构化脱敏）能不能搬过来还不确定，因为本项目的工具结果是纯字符串，不是结构化的日志 context（这是下面的开放问题，不是本文的结论）。

## 本文没有回答、留给设计阶段的问题

- **扫描哪些面**：只扫工具调用结果（`domain.CompleteToolCall.Content`/`ToolCallFailed.Message`），还是也扫模型生成的助手文本，还是也扫模型传给工具调用的原始参数（用户完全可能直接把一个 secret 粘进 prompt，原样变成工具参数）？
- **检测方法**：扩展本项目自己已有的硬编码正则集（便宜、确定性、不引入新依赖，但只能抓住它已经认识的形状——`sk-...`、bearer token、`?key=...`），还是上一个更宽的基于熵的启发式（能抓住形状未知的高熵字符串——一个没有可识别前缀的原始 hex/base64 凭证——代价是对一个 coding agent 日常读写的合法高熵内容——git commit SHA、base64 编码的二进制内容、UUID——有真实的误报率）？
- **在管线的哪个位置做**：在领域事件构造并持久化之前就脱敏（上游、一个地方，但对一个确实需要看原文做调查的运维方来说是不可逆的），还是只在 ACP/导出投影边界做（持久化存储和审计保持完整，但每一个投影调用点都得自己记得套上——本 session 自己的那次实时路径改动就是一个新鲜、具体的"以后还会冒出新的发出面"的例子，因为它在已有的重放路径调用点之外又加了一个实时路径调用点）？
- **Kimi Code 那套按键名的结构化层是否用得上**：本项目的工具结果今天是纯字符串，不是结构化日志 context，所以 `REDACTED_KEYS` 式的按键匹配可能在这里根本找不到对应的目标；自由文本正则那一半才是直接适用的先例。
- **Provider API key 这条路径要不要一个等价于 `RedactedString` 的 Go 类型**（编译期构造时脱敏），这是一个比主问题小得多、边界清楚得多的独立决定——跟随 Codex 和 Grok Build 收敛到的模式，而不是继续停留在本项目现在这种纯靠代码评审纪律的约定上。
- **跟本项目自己已有的截断/裁剪逻辑**（`toolTextContent` 的 16 KiB 裁剪、`MaxToolResultBytes`）**的交互**：脱敏应该在截断之前跑还是之后跑，考虑到一个 secret 完全可能横跨截断边界，导致正则在边界两侧都只匹配到一半？
- **持久化存储加密**（`SECURITY.md` 紧挨着的那条"Durable storage is not encrypted"）是一个相关但独立的缺口，本文没有研究——在写入之前脱敏一个 secret，和给写入的存储本身加密，是两道独立的防线，不能假设做了一个就不需要做另一个。

## 证据边界

- 上面每一条都对应本次会话里实际读过的一个钉住 commit（见上表）；没有一条来自记忆或营销页。
- 本文不授权照抄这五个外部项目里的任何正则、类型名或键名列表——只授权借鉴机制本身和它们代表的架构选择，跟本项目历史上每一份调研门对各自对照集的表态一样。
- `RAW_SECRET_PATTERNS`/`REDACTED_KEYS` 列表是读过之后总结在这里的，不是逐字照搬的完整清单；设计阶段如果需要完整列表，应该直接重读 `formatter.ts`，不要把本文的总结当成全集。
- 本文用有针对性的、大小写不敏感的关键词（"redact"、"scrub"、"secret"、"mask"、"sanitize" 及其变体）在每个参考项目里搜索，然后直接读了每一处命中周围的上下文。一个用了本文没预料到的词汇的机制完全可能存在而没被找到——这是关键词驱动搜索的一个真实、明说的局限，不是对六个大代码库做过穷尽式代码审查的声明。
- 这里的"当前状态"指 2026-08-30。未来若有调研门要重新评估这几个项目，必须按文档规则第 7 条重新抓取、重新阅读，而不是沿用本文的描述。
- 本文不做设计选择。下一步是给 secret 脱敏写一份规范设计——参考上面的发现，但不由它们决定。
