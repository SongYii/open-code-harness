# exec 沙箱与资源配额架构调研门

**状态：** 调研证据完成

**日期：** 2026-08-30

**范围：** 按 [客户端界面复用与安全加固顺序决策](2026-08-30-client-surface-and-security-sequencing.md) 里定下的第一项工作，在设计沙箱/资源配额子系统之前，重新核验本项目既有对照集在**当前状态**下，究竟是怎么约束"模型提议的 shell/子进程执行"的。本文不做任何设计或实现。`SECURITY.md` 里"Not enforced"清单——没有 OS 级沙箱，除了墙钟超时和输出截断之外没有 CPU/内存/磁盘/fd 配额，`PATH` 继承自宿主，没有多租户隔离——就是后续设计要回答的问题。

英文版本 [2026-08-30-exec-sandboxing-and-resource-quotas.md](2026-08-30-exec-sandboxing-and-resource-quotas.md) 是规范文本；本文是与之同步的中文阅读版。两者若有分歧，以英文为准。

## 本项目现在已经做了什么

`internal/harness/adapters/localexec`（见 `SECURITY.md`"Enforced"一节）：命令只接受 argv 形式，绝不拼 shell 字符串；子进程独立进程组，超时或取消时整组 kill；环境变量收窄到 `PATH`（继承宿主）、`HOME`（工作区根）、`TMPDIR`（退出后删除的工作区子目录）。文件类工具（`read_file`/`write_file`/`list_dir`）由 `adapters/workspacefs` 通过 `filepath.EvalSymlinks` 加规范根路径校验单独 jail。这些都不是 OS 级沙箱：一条被批准的 `exec` 命令拿的是 harness 进程的全部权限，没有 CPU/内存/磁盘/fd 配额，网络出口从不被拦截。

## 对照集与钉住的 commit

按文档规则第 8 条，每个都用 `scripts/fetch-reference.sh <owner/repo> <sha>` 抓到 gitignore 的 `.reference/` 目录后直接读源码——不是凭记忆或营销页判断。

| 项目 | 仓库 | Commit | 观察日期 |
| --- | --- | --- | --- |
| OpenAI Codex | `openai/codex` | `dde85b4` | 2026-08-30 |
| Kimi Code | `MoonshotAI/kimi-code` | `9619277` | 2026-08-30 |
| Grok Build | `xai-org/grok-build` | `bc7f02e` | 2026-08-30 |
| Pi agent core | `badlogic/pi-mono` | `853a80d` | 2026-08-30 |
| Maka | `maka-agent/maka-agent` | `d093ba5` | 2026-08-30 |
| DeepSeek Harness | `deepseek-ai/deepseek-harness` | `cd5ef81` | 2026-08-30 |

这就是 2026-08-15 那份 DeepSeek Harness 调研门、以及文档规则第 7 条要求的同一个六项目对照集；没有新增或替换任何项目。

## 逐项目发现

### OpenAI Codex——这个对照集里最完整的跨平台沙箱

`codex-rs/sandboxing`、`codex-rs/linux-sandbox`、`codex-rs/windows-sandbox-rs` 三套独立的平台后端，由 `SandboxManager`（`sandboxing/src/manager.rs`）选择。

- **Linux，主机制是 bubblewrap。** `linux-sandbox/src/bwrap.rs` 调用内置的 `bwrap` 二进制，带 `--unshare-user`、`--unshare-pid`、`--unshare-net`、`--unshare-ipc`、`--cap-drop`、`--die-with-parent`、`--new-session`，用 `--ro-bind` 挂只读的宿主根，用 `--tmpfs`/`--bind` 显式挂可写目录。Landlock（`linux-sandbox/src/landlock.rs`）被明确降级：它自己的文档注释写着"文件系统限制由 bubblewrap 强制……Landlock 辅助函数作为遗留/备用工具保留在这里"。Landlock 现在真正在用的部分比文件系统限制窄得多：同一个文件里手写了一个 seccomp-bpf 过滤器，作为叠加在命名空间自身 `--unshare-net` 之上的纵深防御，额外拦网络系统调用（`connect`、`bind`、`listen`、`accept`、`sendto`，以及 `AF_UNIX` 之外的裸 `socket`），还有一个独立的"proxy-routed"模式改为只放行 `AF_INET`/`AF_INET6` 去连一个本地桥接进程。
- **macOS：Seatbelt。** `sandboxing/src/seatbelt.rs` 在运行时把基础策略、网络策略与逐次调用参数组合成一份 Apple Sandbox Profile Language（`.sbpl`）文档——默认拒绝写、用 `(allow file-write* (subpath "..."))` 白名单条目放行——再调 `sandbox-exec`。
- **Windows：受限令牌 + 拒读遍历 + WFP。** `windows-sandbox-rs` 构造一个受限令牌子进程（`spawn_prep.rs`、`proc_thread_attr.rs`），通过遍历文件系统计算出拒读 ACL 集合（`deny_read_walker.rs`/`deny_read_resolver.rs`），另外单独配置 Windows Filtering Platform 做网络规则（`wfp_setup.rs`），`dpapi.rs` 处理凭据相关的秘密。
- 这三个 crate 里**没有找到任何 CPU/内存/磁盘的资源配额强制**。`codex-rs` 里唯一一处 `rlimit` 相关命中是 `utils/pty/src/pty.rs` 读 `RLIMIT_NOFILE` 来定轮询缓冲区大小，不是配额。内置的 `bubblewrap.c` 里有 `--unshare-cgroup`，那只是隔离 *cgroup 命名空间*，跟在里面真正设置资源上限不是一回事。

### DeepSeek Harness——fail-closed 选择与诚实的部分强制报告

`packages/sandbox/{sandbox,sandbox-local,sandbox-windows-acl,sandbox-policy}` 文档写得异常完整（见 `sandbox-local/README.md`）。比起某个具体机制，更值得学的是两条平台无关的**原则**：

1. **fail-closed 的 runner 选择。**"不支持的平台或不可用的 runner 失败封闭：`confine()` 抛出 `SANDBOX_UNAVAILABLE`……调用方要把这个错误亮出来，而不是让命令不受限地跑。"没有"静默退化为不沙箱"这条路。
2. **强制级别是上报出来的事实，不是假定的承诺。** 每个后端都上报 `full` 或 `partial`：Windows ACL 那一档和较旧的 Landlock ABI 被明确标成 `partial`，这样"需要绝对边界的调用方可以拒绝或亮出这个信号"，而不是系统自己夸大它实际限制到的范围。

机制层面它和 Codex 收敛到同样的平台选择：Linux 先 bwrap 后 Landlock，走一条探测式的选择链（`sandbox-local/src/index.ts` 的选择逻辑，其 README 有描述）；macOS 用 Seatbelt，同样是默认拒绝写加上一份规范化过的写白名单（路径先解析再匹配，因为"Seatbelt 匹配的是已解析路径"）；Windows 每个工作区保留一个确定性的写 SID，但每个会话都用*全新随机*的 SID 和临时目录——"一个全新的 provider 总是选一个新的临时路径和 SID，这样崩溃残留就不能阻塞或授权一个恢复的会话"。这个包组里没有找到任何 CPU/内存配额机制。

### Grok Build——这个对照集里唯一真正强制资源配额的实现

`crates/codegen/xai-grok-tools/src/computer/local/cgroup.rs` 对生成的命令实现了一套真正的 cgroup v2 内存上限，而不只是权限隔离：

- 启动时在宿主进程自己的 cgroup 下创建一个子 cgroup，一次性配置好 `memory.high`（软限制）和 `memory.max = memory.high + headroom`（硬性 OOM kill 边界）。
- 每次生成命令之前，只把该子进程的 PID（按 cgroup 语义连带其进程组后代）写进 `cgroup.procs`——"grok-tools 进程本身从不在这个 cgroup 里——只有生成的子命令在"，命令退出后这个 cgroup 会清空，所以它是一个可复用的、按次调用的边界，而不是整个进程级别的。
- 一个后台监控器通过 `inotify` 监听 `memory.events` 里内核的 `high` 计数器；如果它触发时 RSS 仍然高于 `memory.high` 的 90%，监控器就把它当作持续压力，主动对整个进程组发 `SIGKILL`，上报一个合成的退出码 137（128 + `SIGKILL`）、信号标记 `"oom"`——这是在内核自己更粗暴的 memcg OOM killer 出手之前，一次体面的、可归因的杀掉。
- 这只覆盖内存：这个文件以及这个 crate 别处都没有出现 `cpu.max`、`cpu.weight`、`pids.max` 或 `io.max` 控制器。`xai-grok-shell/src/util/limits.rs` 另外*读取*了 `RLIMIT_NOFILE`、`RLIMIT_NPROC` 以及环境里的 cgroup pids/内存上限，但只是用于崩溃诊断，不设置任何东西。
- `xai-grok-sandbox/src/child_net.rs` 还额外装了一个 seccomp-bpf 过滤器（通过 `pre_exec`），阻止被沙箱的子进程用带新命名空间标志（`CLONE_NEWUSER`、`CLONE_NEWNET`、`CLONE_NEWPID` 等）的 `clone`/`clone3`——堵上了一条具体的沙箱逃逸路径：被限制的子进程通过重新 unshare 自己的嵌套命名空间来逃逸。

### Maka——对 Linux 基线的第三次独立印证

`packages/runtime/src/sandbox/linux-capability.ts` 通过一次能力探测，要求 `bwrap` 同时支持 `--seccomp` 以及 `--unshare-user/-pid/-ipc/-uts/-cgroup`、`--ro-bind`、`--proc`、`--dev`、`--die-with-parent`——和 Codex、DeepSeek Harness 各自独立得出的是同一套命名空间集合。

### Kimi Code——没有找到专门的 exec 沙箱子系统

`agent-core-v2/src/agent/toolExecutor` 和 `_base/execEnv` 做的是调度和环境/shell 路径探测，不是限制。`kap-server/src/security/bindClassify.ts`——`security/` 目录下唯一的文件——分类的是**绑定地址**（loopback/LAN/public），服务于 kap-server 自己的 debug/RPC 监听端口，跟限制模型提议的命令无关。这和 2026-08-15 那份调研门对 Kimi Code 的定位（包/传输结构，而非沙箱）一致；这次没有发现改变这个结论的东西。

### Pi agent core——沙箱只是一个示例，不是核心

`badlogic/pi-mono` 里唯一匹配"sandbox"的是 `packages/coding-agent/examples/extensions/sandbox/index.ts`，一个示例扩展文件，不属于 agent 核心。跟既有调研门"Pi 是一个小型可注入循环与取消机制"的定位一致，不是沙箱参考；这次也没有改变这个结论。

## 交叉综合

- **Linux：bubblewrap（命名空间隔离）是收敛出来的主机制**，三个真正做了沙箱的项目（Codex、DeepSeek Harness、Maka）各自独立得出，要求的命名空间集合基本一致（`user`、`pid`、`net`、`ipc`，加上只读根 + 显式可写挂载）。Landlock 只在其中两个里作为更窄的、明确标注的兜底，或者作为系统调用层的第二层（seccomp）出现，从未作为唯一的文件系统机制。
- **macOS：Seatbelt（`sandbox-exec` + 运行时组装的 `.sbpl` 配置文件）**是两个做了 macOS 沙箱的项目（Codex、DeepSeek Harness）共同的机制，两者都是同样的"默认拒绝写 + 白名单"形态。
- **Windows 没有 bwrap 的等价物**；两个做了 Windows 沙箱的项目（Codex、DeepSeek Harness）都是手搓一套 ACL/受限令牌机制，而且都明确把它标成"没有达到完全强制"，而不是夸大它。
- **这个对照集里没有任何项目为交互式的、逐命令的工具执行路径使用容器或虚拟机。** DeepSeek Harness 自己的 README 明确划了这条线："当进程必须运行在一个隔离环境时选另一种机制——容器或远程执行器替换的是整套能力，而这个 provider 是和宿主共享内核与文件系统的。"对这个具体用例，收敛出来的模式是基于命名空间/profile 的 OS 沙箱，不是容器化。
- **真正的资源配额强制（而不是权限隔离）很罕见。** 只有 Grok Build 真正用内核强制的上限卡住了一种资源（cgroup v2 内存），而且只有内存——这个对照集里没有任何项目对生成的命令强制 CPU 或磁盘配额；除此之外最接近的做法也只是读取环境里的 rlimit 用于诊断。
- **fail-closed 的沙箱选择反复出现**（DeepSeek Harness 明确的 `SANDBOX_UNAVAILABLE`；Codex 的 `SandboxTransformError` 拒绝畸形配置），和本项目自己既有的 fail-closed 立场（SQLite 损坏分类、ACP 校验）是一致的——采纳成本低，不是引入新的价值取向。
- **诚实上报部分强制是 DeepSeek Harness 特有的模式，这个对照集里没有别的项目有对应做法**：按后端上报 `full`/`partial`，而不是一个二元的"是否已沙箱"标志。不管后续设计选哪种 OS 机制，这条都值得采纳，因为本项目自己 CGO-free、跨平台（Linux/macOS/Windows）的定位，几乎必然从第一天起就会在不同平台上有不均衡的强制强度。

## 本文没有回答、留给设计阶段的问题

- `bwrap`、Seatbelt 的 `sandbox-exec`，以及要用到的 Landlock/seccomp Go 绑定，是否能在不打破本项目 `CGO_ENABLED=0` 约束（这条约束是为 SQLite 驱动定的，从未针对沙箱原语验证过）的前提下用上。`bwrap` 和 `sandbox-exec` 是通过 `os/exec` 调用的外部二进制，不是链接库，这一点是有利信号，但对本项目实际的构建/部署方式（内置二进制 vs. 要求宿主已装好，参照 Codex 自己"内置 bwrap + `find_system_bwrap_in_path` 兜底"的做法）还没有验证过。
- 第一个切片是否要覆盖 Windows 后端，考虑到两个参考项目为此都投入了相当可观、且明确标注为部分强制的工程量。
- 本项目是想要 Grok Build 那种主动的内存配额模型（带软/硬阈值和被监控的体面击杀的逐命令 cgroup），还是一个更简单的上限；这个对照集里没有项目强制 CPU 或磁盘，所以那两项确实是"待设计"，不是"照抄某个项目的答案"。
- 网络出口策略：这些机制里没有一个是独立于文件系统沙箱之外的可选附加件——Codex 自己的网络 seccomp 过滤器是叠加在 bwrap 自身 `--unshare-net` *之上*的，这意味着一个 Go 实现的设计大概率也需要同样的两层关系（命名空间级别的默认拒绝 + 一个显式的系统调用或代理路由例外），而不是一个独立的网络过滤器。

## 证据边界

- 上面每一条引用都对应本次会话里实际读取过的一个钉住 commit；没有一条来自记忆或某个项目的营销页。
- 本文不授权照抄这些项目里的任何类型名、schema、`.sbpl` 模板或 seccomp 规则表——只授权借鉴机制本身和它们代表的平台选择。
- 这里的"当前状态"指 2026-08-30。未来若有 gate 要重新评估这六个项目中的任何一个，按文档规则第 7 条必须重新抓取、重新阅读，而不是沿用本文的描述。
- 本文不做设计选择。下一步是给 `internal/harness/adapters/localexec` 的沙箱与资源配额扩展写一份规范设计——参考上面的发现，但不由它们决定。
