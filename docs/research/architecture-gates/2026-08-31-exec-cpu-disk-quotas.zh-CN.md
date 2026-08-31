# Exec CPU 与磁盘配额架构调研门

**状态：** 调研证据完成

**日期：** 2026-08-31

**范围：** [`SECURITY.md`](../../../SECURITY.md) 的"未强制执行"清单写着："No CPU or disk-IO quota, on any platform. No file-descriptor limit."[2026-08-30 exec 沙箱与资源配额调研门](2026-08-30-exec-sandboxing-and-resource-quotas.md)已经研究过本项目对照集里 OS 级沙箱和资源配额机制的整体情况，结论是"这个对照集里没有一个项目对生成的命令做 CPU 或磁盘配额"，后续落地的设计因此把 CPU/磁盘留成"确实没设计过",只做了内存。本文不重新推导那份调研门已经定论的东西（文件系统沙箱的机制收敛、fail-closed 选择、诚实的部分强制报告）；本文按文档规则第 7 条重新核实同样六个项目自那之后有没有加过 CPU 或磁盘强制机制，并研究一份设计如果要延伸本项目自己现有的 cgroup v2 内存配额代码，会用到的两个具体 Linux controller（`cpu.max`、`io.max`）的第一手技术细节。本文不做任何设计或实现。

英文版本 [2026-08-31-exec-cpu-disk-quotas.md](2026-08-31-exec-cpu-disk-quotas.md) 是规范文本；本文是与之同步的中文阅读版。两者若有分歧，以英文为准。

## 本项目自己已有的东西

`internal/harness/adapters/localexec/cgroup_linux.go` 实现了一个可复用的、按次调用的 cgroup v2 子目录：`memory.high`/`memory.max` 在构造时写一次（采用的是 Grok Build 自己的模型，被之前那份设计采纳），每次 `Run` 调用只把那次命令自己的 PID 写进 `cgroup.procs`，一个后台 goroutine 通过 `inotify` 监视 `memory.events` 的 `high` 计数器，如果内核自己的计数器递增时用量仍然超过 `memory.high` 的 90%，就主动 `SIGKILL` 整个进程组——报告 `CommandResult.ResourceLimited`。这套"监控加主动杀掉"的架构之所以存在，是因为 `memory.max` 是一条硬性的 OOM 边界，本项目选择抢在内核自己的硬杀之前优雅处理；本文下面的第一手研究会证明，`cpu.max` 和 `io.max` 都不需要——也没有任何钩子可供——类似的监控，因为两者都是内核自己透明强制执行的限流型 controller。

macOS 上等价的内存边界是 `RLIMIT_AS`（`sandbox_darwin.go`），明确标注为尽力而为：它限的是虚拟地址空间，不是常驻内存，触发时表现为子进程自己的分配器拿到 `ENOMEM`，从来不是一次干净的外部杀掉——`SECURITY.md` 自己的"未强制执行"清单里已经写明 `CommandResult.ResourceLimited` 在那边从不会被设置。

## 对照集与钉住的 commit

按文档规则第 8 条抓取、直读源码；按第 7 条，六个项目今天全部对照上一份调研门的钉点重新核实：`grok-build`、`deepseek-harness`、`pi-mono` 没有变过；`codex`、`kimi-code`、`maka-agent` 各自往前走了，已重新抓取并 checkout。

| 项目 | 仓库 | Commit（上一份调研门 → 今天） | 观察日期 | 重新核实结果 |
| --- | --- | --- | --- | --- |
| OpenAI Codex | `openai/codex` | `dde85b4` → `a9519cb` | 2026-08-31 | `linux-sandbox`、`execpolicy`、`core/src/seatbelt.rs` 在两个 commit 之间没有 diff；没有新东西可读 |
| Kimi Code | `MoonshotAI/kimi-code` | `9619277` → `8f2c60b` | 2026-08-31 | 所有改动文件都在 `packages/transcript/` 下；跟沙箱或资源配额无关 |
| Grok Build | `xai-org/grok-build` | `bc7f02e`（未变） | 2026-08-31 | 直接重读了 `cgroup.rs`；这个 crate 里哪都没有 `cpu.max`、`cpu.weight`、`io.max`、`pids.max` 或 `blkio` 的引用 |
| Maka | `maka-agent/maka-agent` | `d093ba5`/`5d519d6` → `ef94235` | 2026-08-31 | `packages/runtime/src/sandbox/diagnostics.ts`（474 行）和其他几个沙箱文件在这个区间里被删掉或缩小了；剩下的文件里依然没有 `cpu`/`io`/`blkio`/rlimit-cpu 的引用。本文没有去查 `diagnostics.ts` 为什么被删——记一笔观察到的变化，没有深究，因为它本身看起来不是资源配额机制 |
| DeepSeek Harness | `deepseek-ai/deepseek-harness` | `cd5ef81`（未变） | 2026-08-31 | 见下文的新发现——这是同一个仓库里*另一个*沙箱，不是上一份调研门研究过的那个 |
| Pi（`pi-mono`） | `badlogic/pi-mono` | `853a80d`（未变） | 2026-08-31 | 未变；上一份调研门"沙箱只是个例子，不是核心"的表述依然成立 |

在今天的六个 commit 上用 `cpu.max`、`io.max`、`cpu.weight`、`blkio`、`RLIMIT_CPU` 做了一次有针对性的重新检索，只命中一处，见下文。**这个对照集里今天没有一个项目对自己的 shell/exec 工具做 CPU 或磁盘 IO 配额**——上一份调研门的结论站得住，是重新核实过的，不是想当然沿用。

## 新发现：DeepSeek Harness 的 *Python 代码运行时*会自己设 `RLIMIT_CPU`——是另一个沙箱，机制形状也不一样

`packages/code-runtime/code-runtime-python/src/protocol.ts` 里有一个 `BootMessage.cpuSeconds` 字段的文档："RLIMIT_CPU seconds; the Python bootstrap sets this on itself before executing model code."（RLIMIT_CPU 秒数；Python 启动脚本在执行模型代码之前，在自己身上设置这个值）。这不是上一份调研门研究过的那个 shell 工具——这是一个单独给运行模型生成的 Python 代码用的沙箱，DeepSeek Harness 自己控制着在被沙箱化的进程里启动的那段引导脚本。

这跟本项目自己的 `exec` 工具能直接采用的任何机制,在形状上有实质性的不同：这个限制是*目标进程自己*设的，在它启动之后、执行不可信代码之前——不是一个父进程在子进程已经在跑操作者/模型提供的任意代码之后,从外面伸手进去设的。Go 的 `os/exec` 没有等价的钩子（不像 Python 自己的 `preexec_fn`，或者 C 的 `posix_spawn_file_actions` 回调）可以在 fork 和 exec 之间给一个*任意*命令注入"给自己设 RLIMIT_CPU"这个动作——`exec` 存在的意义就是跑调用方提供的 argv，不是本项目自己写的引导脚本。直接查过 Go 标准库自己的 `os/exec` 和 `syscall.SysProcAttr` 文档确认了这一点：Linux 上的 `SysProcAttr` 暴露的是 `Setsid`、`Setpgid`、`Chroot`、`Credential`、`Ptrace` 这类进程创建时的旗标，没有 rlimit 字段。最接近的、面向外部的 Linux 专属原语 `prlimit(2)`（在权限足够的情况下*可以*设置另一个已经在跑的进程的限制），在这个对照集的六个项目里都没有被用来干这件事，而且在 macOS 上完全没有等价物。

## 第一手来源：cgroup v2 `cpu.max` 和 `io.max` 的语义

直接读自内核自己的 admin-guide 文档（`docs.kernel.org/admin-guide/cgroup-v2.html`），跟本项目之前那份沙箱设计给 `memory.high`/`memory.max` 用的是同一个权威来源：

- **`cpu.max`** 接受 `"$MAX $PERIOD"`（单位微秒，比如 `"50000 100000"` 把一个 cgroup 限制在一个 CPU 核的 50%），或者字面量 `"max"` 表示不限；只写一个数字只更新 `$MAX`。内核靠**限流**强制执行，不是杀掉——一个 cgroup 在一个周期内用超了自己分到的 CPU 时间，就是不再被调度直到下一个周期，没有任何类似 OOM killer 的介入,也不需要任何守护进程或监控。`cpu.stat` 报告 `nr_periods`、`nr_throttled`、`throttled_usec` 供观测。
- **`io.max`** 接受一个嵌套键值格式,用 major:minor 号标识块设备,配 `rbps`/`wbps`（字节/秒）和 `riops`/`wiops`（IO 操作/秒）这几个键——比如 `"8:16 rbps=2097152 wiops=120"`。强制执行靠内核自己的 `blk-throttle`，同样**不杀、不监控**："如果达到限制,IO 会被延迟……允许短暂的突发。" `io.stat` 报告每个设备的 `rbytes`/`wbytes`/`rios`/`wios`。
- 这两个 controller 都要求跟本项目自己 `newCgroupQuota` 给 `memory` 已经做过的同一套委派形状：父 cgroup 必须在 `cgroup.subtree_control` 里列出这个 controller，子 cgroup 才能用，受同一个"内部不能有进程"约束限制，现有的内存代码已经绕过了这个约束。
- **这两个 controller 都没有任何类似 `memory.events` 的 `high` 计数器或者违规通知机制。** `cgroup_linux.go` 专门为内存搭的那套监控 goroutine/inotify/优雅杀掉的架构，对 CPU 或 IO 完全没有对应物可以延伸——一个 CPU 或 IO 配额,机制上就是在构造 cgroup 的时候多写一次值，之后没有任何东西需要监视。

## 交叉综合

- **在 Linux 上，CPU 和 IO 这两个 cgroup v2 controller 架构上比内存更容易加**，恰恰因为它们不需要监控：给 `newCgroupQuota` 多加两个 `os.WriteFile` 调用（`cpu.max`、`io.max`），写进本项目自己已经在为内存创建、委派、拆除的同一个可复用子 cgroup 里——是对已经上线、有证据支撑的基础设施做一次小的增量改动，不是一个新子系统。
- **一个被限流的命令不会像被内存限制的命令那样失败——它只是跑得更慢**，本项目自己现有的 `CommandResult` 形状（`TimedOut` 和 `ResourceLimited` 互斥，`tools/ports.go` 自己的文档注释写着："一次运行只因为一个原因被杀"）对这个没有对应的分类。一个被 CPU 或 IO 限流的命令,最可能的可观察结果就是撞上*现有*的墙钟超时——如果不去事后读 `cpu.stat`/`io.stat`，根本没法跟一个本来就慢的命令区分开。这个区别值不值得暴露给模型/运维方,是一个真实的设计问题（见下文），本文不做回答。
- **`io.max` 限的是*速率*，不是*总共用掉的磁盘空间*。** 一个被限制在中等 `wbps` 的命令，只要给足够的墙钟时间（由现有的超时单独限制），照样能写出一个任意大的文件——`io.max` 本身并没有解决 `SECURITY.md` 里"磁盘"这个措辞乍一看容易让人以为已经解决的那个"工作区被写满"问题。本项目自己在同一个领域已经有一个*不同*的现成边界——`MaxToolResultBytes`/输出截断——但那限的是*捕获并返回给模型*的内容，不是一个命令真正往工作区文件系统底下写了多少。
- **`io.max` 需要先解析出工作区根目录（以及任何临时目录）实际躺在哪个块设备上**（major:minor 号）——这是内存那种单一、通用的上限从来没遇到过的、真正跟环境相关的新解析问题，本文也没有在这六个参考项目里找到谁解决过它，因为没有一个项目实现了 `io.max`。
- **在这个六项目对照集里,没有找到任何 macOS 上等价于 cgroup v2 的 CPU 或 IO 限流机制，也没找到别的外部强制原语。** 跟内存不一样（`RLIMIT_AS` 是一个真实、尽管尽力而为的 POSIX rlimit，任何进程都能给自己和自己派生的子进程设置），从这个对照集来看，"在 macOS 上,什么东西能从外部给一个已经拉起来的任意子进程强制加一个 CPU 或 IO 上限"这个问题的诚实答案似乎是：本文和这六个参考项目都没演示出任何东西。这比内存那种"部分/尽力而为"的缺口更彻底，不只是一个没研究到的角落。

## 本文没有回答、留给设计阶段的问题

- **一个被限流但没被杀掉的结果,要不要给 `CommandResult` 加一个新字段或者新的报告分类**，跟今天的 `TimedOut`/`ResourceLimited` 这对区分开，还是接受 CPU/IO 限流只能间接体现出来（一个本来能按时跑完的命令现在超时了）、没有任何显式信号——以及超时之后去读 `cpu.stat` 的 `nr_throttled`/`throttled_usec` 来归因,值不值得这个额外的复杂度。
- **"磁盘配额"到底该指 IO 吞吐量（`io.max`）、一次命令写进工作区的总字节数上限，还是两者都要**——`io.max` 本身并不解决磁盘空间耗尽的问题，本文也没有在参考项目里找到"总写入字节统计"或者"给一次生成的命令做磁盘空间配额"的先例（跟本项目自己已经实现的、独立的输出捕获截断不是一回事）。
- **怎么解析出工作区根目录（以及临时目录）的块设备 major:minor 号**，包括它们躺在不同设备上、网络文件系统上，或者跨重启/容器 major:minor 会变的情况。
- **CPU/IO 配额第一版要不要覆盖 macOS**，考虑到本文没有找到任何外部强制机制能跟内存那套（不完美但真实）的 `RLIMIT_AS` 故事相提并论——一个明说的、有名字的缺口（照搬本项目自己上一版沙箱切片对 Windows 的先例）可能才是诚实的答案，而不是发明一个没有任何参考项目演示过的、未经验证的机制。
- **按 CPU 时间给一个固定绝对上限，还是按墙钟比例给一个默认值,哪个更有用**，考虑到 `cpu.max` 的 `$MAX/$PERIOD` 形状本质上是一个带宽比例（比如"一个核的 50%"），不是 `RLIMIT_CPU` 那种总秒数上限——这是两种实质不同的策略，本文不推荐哪一个。
- **文件描述符上限**（`SECURITY.md` 同一句话里跟 CPU/磁盘 IO 并列提到的）本文完全没有研究——`RLIMIT_NOFILE` 是一个真实、简单、可移植的 POSIX rlimit（Codex 自己的 `pty.rs` 已经在*读*它，据上一份调研门），但它要不要跟 CPU/磁盘配额放进同一轮设计周期，还是单独、更小的一轮，本文留白。

## 证据边界

- 上面每一条都对应本次会话里实际读过的一个钉住 commit（见上表），或者直接抓取的内核自己的 admin-guide 文档；没有一条来自记忆或营销页。
- 本文不授权照抄任何参考项目的文件路径、常量名或配置形状——只授权借鉴机制本身和它们代表的架构选择，跟本项目此前每一份调研门对各自对照集的表态一样。
- Maka 的 `diagnostics.ts` 删除（这个区间里好几个沙箱文件净减少 592 行）只是观察到了，没有深究——从上一份调研门自己对 Maka 的阅读范围（只覆盖了 `linux-capability.ts` 的 bwrap 探测）来看，它删除前似乎不是资源配额代码，但本文没有去读被删文件的原始内容来确认这一点，而是推断的。
- `prlimit(2)`——在 Linux 上从外部设置另一个进程 rlimit 的可能性——是本文知道的一个真实 Linux 系统调用，没有独立核实过 Go 的 `syscall` 包对它的支持情况，也没有在本项目代码里测试过——如果设计阶段要把它当作 Linux 上 cgroup 的替代方案来考虑，这里标记为一个需要单独核实的事实。
- 这里的"当前状态"指 2026-08-31。未来若有调研门要重新评估这六个项目，或者内核自己的 cgroup v2 文档，必须按文档规则第 7 条重新抓取、重新阅读，而不是沿用本文的描述。
- 本文不做设计选择。下一步是给 `internal/harness/adapters/localexec` 现有资源配额机制的 CPU 和/或磁盘扩展写一份规范设计——参考上面的发现，但不由它们决定。
