# Exec 沙箱与资源配额设计（中文摘要）

- **日期：** 2026-08-30
- **状态：** 2026-08-30 已批准。人工复审明确确认了第五节里 Windows 那条权衡：这个切片不做 Windows，现在可以接受。
- **稳定性：** 涉及现有的 `experimental`/pre-GA 面（`tools.CommandRunner`、`adapters/localexec`）

英文版本 [2026-08-30-exec-sandboxing-resource-quotas-design.md](2026-08-30-exec-sandboxing-resource-quotas-design.md) 是规范文本；本文是摘要性的中文阅读版，不是逐字翻译。两者若有分歧，以英文为准。

**权威依据：** [客户端界面复用与安全加固顺序决策](../../research/architecture-gates/2026-08-30-client-surface-and-security-sequencing.zh-CN.md)；[exec 沙箱与资源配额架构调研门](../../research/architecture-gates/2026-08-30-exec-sandboxing-and-resource-quotas.zh-CN.md)

## 决策摘要

`internal/harness/adapters/localexec` 现在实现 `tools.CommandRunner`：argv-only 执行、整组 kill、环境变量收窄，文档和测试都诚实地标注为 `Enforcement == "partial"`。本设计把这句诚实的"部分强制"变成真正的 OS 级隔离加内存配额——在确实存在可靠机制的平台上——同时保持 `SECURITY.md` 里"Enforced"一节现有的每一条保证不变，并且完全保住本项目 `CGO_ENABLED=0` 的纯 Go 约束。

调研门的发现逼出了一个核心决策：**隔离机制必须作为外部进程运行，不能是 CGO 链接或进程内代码**，因为两个真正的系统调用级候选方案（Landlock、seccomp）在 Go 里各自有一个绕不过去的正确性问题——`go-landlock` 自己的 `AllThreadsLandlockRestrictSelf` 在没有 Landlock ABI V8 的旧内核上需要靠 `libcap/psx` 用 CGO 才能广播到所有 OS 线程（已经实读 `landlock-lsm/go-landlock` `4b35c42` 的 `landlock/syscall/allthreads_linux.go` 验证过）；而在 fork 和 exec 之间安装 seccomp-bpf 过滤器，正是 Go 运行时文档里明确说不安全的模式（goroutine 和 GC 不是 fork-safe 的）。Bubblewrap 同时绕开了这两个问题：它是一个独立的 ELF 二进制，通过 `os/exec` 调用；它接受通过 fd 传入的预构建 BPF 程序（`--seccomp FD`），所以*过滤器字节本身*可以用纯 Go（`golang.org/x/net/bpf`）拼出来，而我们这个进程从头到尾不需要 fork。这也是六个参考项目里三个（Codex、DeepSeek Harness、Maka）在 Linux 上各自独立收敛到的机制。

第二个决策：**fail-closed，在组合根启动时检查一次，不是每次调用都查。** 如果当前平台的机制不可用，`composition.Open` 直接拒绝启动——和它现在因为 provider API key 缺失或数据库连不上而拒绝启动是同一套逻辑——而不是每次调用都悄悄不设防地跑 `exec`。为需要在迁移期间保留现状（不沙箱）行为的运维方，留了一个明确的、默认关闭的配置开关；打开它是响亮的（启动时打日志，不是静默降级）。

## 范围（v1）

**做：** Linux 上用 bubblewrap 做进程/文件系统/网络隔离，配合 cgroup v2 内存上限和一次抢在内核 OOM killer 之前的、体面的、可归因的击杀；macOS 上用 `sandbox-exec` + 运行时生成的 Seatbelt profile 做文件系统/网络隔离，外加 `RLIMIT_AS` 做尽力而为的地址空间上限；组合根启动时做一次 fail-closed 可用性检查，外加一个默认关闭、打开会响亮告警的例外开关；把现在写死的 `localexec.Enforcement = "partial"` 换成一个按效果分别报告的结构化值；给"因资源超限被杀"新增一个和现有 `TimedOut` 平级、互斥的分类结果。

**不做：** Windows 上的 OS 级沙箱（两个尝试过的参考项目都要靠手搓的、明确"非完全强制"的 ACL/受限令牌机制才做到——本设计不做这个，而是把由此带来的后果和权衡在设计里明写出来，不擅自替人做决定）；Landlock（CGO 原因同上，且 bwrap 自身的挂载命名空间已经覆盖了 Landlock 在 Linux 上要加的文件系统隔离）；容器/虚拟机（调研门里明确拒绝——这个对照集里没有项目为逐命令的交互式路径用它，DeepSeek Harness 自己的文档也划清了这条线）；CPU 和磁盘 IO 配额（这个对照集里没有项目做，确实是未设计，不是有答案但延后）；自己打包/内置 `bwrap` 二进制（v1 要求运维方自己装好，装不上就是 fail-closed 的那一种情况）；网络白名单/代理路由例外/逐工具自定义 profile（v1 直接拒绝所有网络访问）；把"这个会话跑在无沙箱状态下"记成一条持久领域事实（这是组合根层面的配置项，启动时打日志，不碰领域事件日志——把它做成一等公审计事实是一个真实但独立的想法，留给专门设计它的人，不在这次适配器层设计里夹带塞进领域层改动）。

## 关键机制

- **Linux 可用性探测**：组合根启动时在 `PATH` 上解析一次 `bwrap`，跑一次受限的探测调用，结果缓存整个 assembly 生命周期（和所有参考项目"探测一次、缓存整个生命周期"的做法一致）。二进制缺失、探测非零退出、或 WSL1（通过 `/proc/version` 区分，和 Codex 自己的 `Wsl1UnsupportedForBubblewrap` 是同一个判断）都算不可用，走 fail-closed。
- **Linux 逐次调用隔离**：给现有 argv 包一层 bwrap，带 `--unshare-user/-pid/-ipc/-uts/-cgroup/-net`、`--die-with-parent`、`--new-session`、`--cap-drop ALL`、只读根 `--ro-bind / /`、`--tmpfs /tmp`、显式可写工作区绑定——和调研门里三个做 Linux 沙箱的项目要求的命名空间集合基本一致。`--unshare-net` 本身就在 v1 里拒绝了全部网络访问，不需要额外的 seccomp 层就能满足"无网络"这个目标；用 `golang.org/x/net/bpf` 构造、通过 `--seccomp FD` 传入的过滤器作为未来更细粒度系统调用拒绝的可选第二层保留着，但这次不做。
- **Linux 内存配额（cgroup v2）**：组合根生命周期内建一个子 cgroup，设 `memory.high`（软）和 `memory.max = memory.high + headroom`（硬），采纳 Grok Build 的模型——这是整个对照集里唯一真正的资源*配额强制*（而不是权限隔离）。每次 bwrap 子进程一启动（还没 exec 目标命令）就把它的 PID 写进 `cgroup.procs`；一个每个 assembly 一个的后台监控器通过 `inotify` 盯 `memory.events` 的 `high` 计数器，超过可配置阈值（默认 90%，同 Grok Build）就发 `SIGKILL`，抢在内核自己更粗暴的 memcg OOM killer 之前。cgroup v2 不可用时只降级、打警告，不让 `composition.Open` 失败——这是叠加的尽力而为项，不是 fail-closed 必须项。
- **macOS 机制**：探测 `sandbox-exec`；逐次调用在 Go 里现拼一份 `.sbpl`：默认拒绝写、显式放行规范化后的工作区根、读保持宽松（和 Codex 一致，收紧读历来是更脆弱的方向）、v1 拒绝所有网络。macOS 没有 cgroup 等价物，用 `setrlimit(RLIMIT_AS, ...)` 做尽力而为的地址空间上限，明确标注这比 Linux 的 cgroup 上限弱得多（失败模式是分配器拿到 `ENOMEM`，不是干净的外部击杀），在结构化的强制报告里如实区分开，不和 Linux 的保证混为一谈。
- **Windows：默认 fail-closed，且明确点出这是一次退步**。今天 `exec` 在 Windows 上是能跑的（虽然不设防）；这次改动后默认会直接拒绝启动，这是一次真实的能力回退，不是"功能没做完"，本设计不打算自己悄悄拍板，而是明写出来。缓解手段是一个跨平台、默认关闭的配置开关（暂定名 `AllowUnsandboxedExec`），打开它是响亮的（启动时打出具体缺了哪项保证的日志），覆盖 Windows 以及任何主机制不可用的平台（缺 `bwrap`、缺 `sandbox-exec`、WSL1）。**复审决定（2026-08-30）：** 这条权衡——这个切片不做 Windows、默认 fail-closed——已经明确被接受；Windows 不是近期优先级，后续切片可以再议。一份后续切片可以用 Windows Job Object（`SetInformationJobObject` + `JOBOBJECT_EXTENDED_LIMIT_INFORMATION`，通过 `golang.org/x/sys/windows` 可达、不需要 CGO）给 Windows 做独立于文件系统权限沙箱难题之外的真实内存/CPU/进程数配额——这里只是记下这个已验证可行的选项，不在这次设计里拍板。
- **结构化强制报告**：`localexec.Enforcement` 不再是包级常量，改成在 `Runner` 构造时算一次的值，按效果分别报告（`Filesystem`/`Network`/`Memory` 各自 full/partial/none），而不是压成一个词——这是这次设计从 DeepSeek Harness 那里采纳的、和具体 OS 机制无关的那条原则。既有的 `TestEnforcementPartial` 测试和 `tool-runtime.md` 里那一行会被这次设计取代；实施计划要用按效果拆开的断言去替换它，而不是直接删掉这块覆盖。

## 验证与验收（要点）

argv/profile 构造在没有真实 `bwrap`/`sandbox-exec` 的情况下也能单元测试；一套依赖真实二进制存在的集成测试（缺失时 `t.Skip`，不让 CI 因为宿主缺依赖而失败）验证工作区外写入真被 OS 拒绝、网络连接真的失败、Linux 上看不到 PID 命名空间外的宿主进程；一个内存配额测试在真实 cgroup 路径下跑一个故意吃内存的测试程序，断言 `ResourceLimited` 在有界时间内变 true 且进程组被彻底回收；一个 fail-closed 测试强制让可用性探测失败，断言 `composition.Open` 拒绝启动，以及打开例外开关后既会打出具名警告、也确实能带着 `Enforcement` 报告该效果为 `"none"` 启动成功；`go test -race ./...`、Windows/macOS 交叉编译，外加一次专门的 `CGO_ENABLED=0 go build ./...`——因为这次设计的核心主张就是完全不需要 CGO，这个主张必须被检验，不能只是假定。

## 主要风险

`bwrap` 必须提前装好、v1 不内置——明确排除在范围外，缺失时 fail-closed 而不是静默降级，且例外开关给还没准备好环境的运维方留了路；Windows 默认丢掉现在能跑（虽不设防）的 `exec`——在设计里明写成一次需要签字确认的回退，不擅自解决；WSL1 不支持 bubblewrap——单独识别、不和"bwrap 缺失"混在一起；`sandbox-exec` 是一个已废弃且没有替代品的 Apple API——整个行业共同的遗留风险，不是本设计独有；内存配额监控器给每个 assembly 多加一个后台 goroutine 和一个 inotify fd——生命周期收在 assembly 范围内，和现有心跳/导出循环一样的纪律；一个拆成三个效果的结构化 `Enforcement` 值比一个字符串更难一眼看懂——这正是"不虚报"这条设计初衷本身要付出的代价，单词版本的替代方案恰恰是本设计想要拒绝的东西。
