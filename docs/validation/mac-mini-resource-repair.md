# Mac mini CPU／内存资源修复

日期：2026-10-14 UTC。中文为本轮权威验收说明。
状态：最终功能提交的资源专项、完整十轮 race soak、九集合 Context fixture 矩阵及七个适用 CI job 均通过。严格内核资源请求仍明确拒绝，不把采样监控写成硬隔离。
历史基线和失败结果保留于 [前一阶段报告](mac-mini-acceptance.md)。

## 最终验收绑定

功能提交：[f70a2134594d469e4051d68b17be89521ff3e1ec](https://github.com/SongYii/open-code-harness/commit/f70a2134594d469e4051d68b17be89521ff3e1ec)，分支fix/mac-mini-ci。
下面全部运行在该SHA；后续验收说明提交只改文档，不改变此功能基线。

| 检查 | 实际终态 |
| --- | --- |
| Mac mini完整十轮soak | `go test -json -race ./... -count=10 -timeout=40m`，1330.67秒，退出0，36包／41730测试实例通过，0失败，watchdog未触发 |
| 原先失败的恢复回归 | `TestRlimitBracketSetsAndRestoresRLIMIT_CPU` 连续10轮通过；localexec整个包十轮通过，406.355秒 |
| Context显式fixture矩阵 | 同SHA，9集合（4 in-process、5 ACP），54.17秒，退出0，11测试实例，0失败／0skip；没有真实模型调用 |
| 精确功能SHA CI | [run 38501908168](https://github.com/SongYii/open-code-harness/actions/runs/38501908168) completed/success；7个适用job通过，含Linux全仓race与3轮repeat、漏洞、真实bwrap隔离、两种cross-build，以及原生Darwin资源包3轮 |
| 原生Darwin CI范围 | macOS15.7.9 ARM64／Go1.26.9；282测试实例通过，包含leader已Wait后故障唤醒阻塞Read与退出RSS不计费；仅既有Seatbelt共6次skip，没有资源测试skip |
| 静态／构建 | docsguard、全仓vet/native build、CGO_ENABLED=0 Darwin amd64／Windows amd64 build通过 |
| 环境与工作区 | 父任务CPU／AS限额仍无限；原始main、GUI修复工作区均保持原SHA，无新GUI／手机／真实模型操作，无系统权限或VM安装、无合并部署 |

soak额外有35分钟整体及6分钟单测试watchdog；Context矩阵在soak结束后单独运行，未叠加。
原始JSON/text与探针在本地evidence/，未上传原始请求、密钥、截图或设备配置。

### 尚未验证或不支持的范围

- 本Mac及原生CI的两个真实Seatbelt测试仍因既有sandbox-exec探针SIGABRT跳过；资源通过不代表本机filesystem/network confinement通过。默认composition启动仍有既有sandbox admission要求。
- Linux CI真实bwrap文件、网络与宿主进程隐藏通过；3个真实cgroup CPU／memory quota测试因宿主delegation不可用skip，不宣称这些实际quota已验收。
- CI的context-matrix／soak／nightly-report是schedule-only，本次push未跑。Context与完整soak已有上述本机显式结果，不能把CI skip计为通过。
- soak还有真实browser自动发现未找到Chrome、显式MCP未opt-in、外部citations未opt-in以及helper／sandbox条件guard的skip。没有添加Chrome路径、绕过浏览器sandbox或扩大外部应用范围。
- Darwin amd64和Windows仅cross-build，未原生运行；原生证据限定于ARM64。
- 保护仍为partial采样监控：超采样窗口、未观察到的快速退出worker、脱组后代、共享RSS及非原子身份核对边界见下文。任何full CPU／memory请求继续fail closed。

## 已实施的修复与合同

父进程不再调用 setrlimit，Run 和 StartBracket 不产生 CPU／地址空间限额污染。
默认 Darwin 命令使用外部监控：25ms 采样原有进程组，累计已观察到的各进程 CPU
时间，合计当前驻留内存；达到默认 30 CPU 秒或 768MiB 时发送 SIGKILL。
该保护报告为 CPU=partial、Memory=partial，绝不报告内核硬边界。
DefaultCPUSoftSeconds 保留为默认监控预算；历史 hard 值仅保留配置兼容与校验，
不会设置父／子 CPU hardlimit，也不能被解释为内核强制保证。

NewWithOptions 可指定采样 CPUTimeLimit、MemoryLimitBytes 和最低 RequiredEnforcement。
Darwin full CPU 或 memory 请求在构造阶段返回 ErrEnforcementUnavailable，不启动命令。
非 Darwin 的 CPU-time／RSS 覆盖请求也明确拒绝，避免用 Linux cpu.max 带宽限额替代
总 CPU 时间预算。Linux 原有 cgroup bandwidth／memory 合同和默认可用性行为保留。
Darwin sampler 初始化、注册或运行中失败均明确报错；运行中失败终止已确认归属的组，
不能确认归属或终止时报告错误／teardown unproven，不继续宣称保护生效。

Run、ConfinedCommand.Start、StdioProcess.Start 使用同一监控实现。
既有 prepared API 保持兼容：raw Cmd().Start 用户必须立即 Register，且在并发 Wait 之前；
推荐改用 Start 原子完成启动与注册。StartBracket 仍可调用，但成为无父进程副作用的 no-op。
注册已 Wait 的命令不会把复用的 PID 重新纳入监控。取消、超时、输出上限和资源原因仍分别处理，
ResourceLimited 与 TimedOut 不混用；普通 SIGKILL 不作为资源超限证据。
原 argv／LookPath／ErrDot、环境白名单 PATH/HOME/TMPDIR 和 Setpgid 保留，无 shell bootstrap。
组终止后的等待有有限截止，无法确认清理会报错。

## 实际计量与执行边界

使用 Go 的 Darwin syscall 入口读取 SYS_PROC_INFO：PIDRUSAGE (9)/RUSAGE_INFO_V0
与 LISTPIDS (1)/PROC_PGRP_ONLY (2)，不需要 cgo、额外 helper 部署、VM 或系统权限。
RUSAGE_INFO_V0 为96字节；CPU值是 Mach ticks，必须按宿主 mach_timebase_info 换算。
本 Mac mini 实测为125/3。原生 libproc+mach_timebase_info 与独立 getrusage 交叉核对：
换算后0.257291292 CPU秒，getrusage为0.257295000秒，相差约4微秒。
Go 换算采用128位乘除并饱和，避免溢出回绕关闭预算。
构造探针只读自身统计与组枚举，不要求读取共享shell组中其他用户的CI supervisor；实际受监管命令另建Setpgid组，对该组每个成员的读取拒绝仍明确失败。
接口拒绝或 ABI 不兼容时明确失败；amd64 timebase 仍需原生 CI 验证，交叉构建不替代它。

实现参考 Apple [libproc wrapper](https://github.com/apple-oss-distributions/xnu/blob/main/libsyscall/wrappers/libproc/libproc.c)、
[resource ABI](https://github.com/apple-oss-distributions/xnu/blob/main/bsd/sys/resource.h) 和
[Mach trap table](https://github.com/apple-oss-distributions/xnu/blob/main/osfmk/kern/syscall_sw.c)。

## 必须保留的限制说明

- 25ms 是采样周期，不是硬截止；调度延迟和采样之间的增长会造成 overshoot，不能承诺严格最大 RSS／CPU。
- CPU账本保留已采样退出进程的高水位，按 PID+内核启动时间区分复用；未被任何采样观察到的快速退出子进程，其 CPU 可能漏计。
- 离开原进程组的后代不在这项保护内，监控也不是完整进程树内核隔离。
- RSS相加可能重复计算共享页，不等于Linux cgroup memory.current或虚拟地址空间限额。
- 发送组信号前重新核对启动身份／已观察成员的连续性，拒绝已复用 leader 或无法确认的组；核对与 signal 并非内核原子操作，仍不能提供严格 PID／进程组能力保证。
- 不用 RLIMIT_AS 设置成功或 CPU hard 数值推断内核保证。前一阶段已证实本机 AS 设置 EINVAL，忽略 SIGXCPU 可越过 hard 值。
- Seatbelt 的既有宿主 SIGABRT 阻碍未通过权限修改绕过；这项资源修复不把 filesystem/network 标为已通过。

## 已完成的实测

- 第一轮资源专项：32个测试实例通过，0失败、0skip，30.797秒。
- 完整 localexec 包三轮 race：228个测试实例通过，0失败，92.201秒。
  两个既有 Seatbelt integration 每轮因探针 SIGABRT 跳过，共6次；没有资源测试跳过。
- 原父进程恢复回归保留名称，增强为CPU／AS在 before/during/after 均完全不变；旧“during降为30”断言要求的正是必须消除的污染行为。
- 真实 busy loop 忽略 SIGXCPU，子进程自身设1/2 kernel值，外部2.5 CPU秒预算仍能终止它。
- 真实驻留内存增长、两个worker的累计CPU与内存、stdio和prepared启动、故障注入、
  实际三变量环境、三Runner九命令并发继承限额、旧PID身份拒绝均有回归。
- 加强聚合检查三轮通过：单worker峰值CPU约257–263ms，低于500ms组预算；单进程RSS约81–96MiB，低于180MiB组预算，但组总量触发终止。
- 资源专项最终三轮：111个实例通过，0失败、0skip，90.171秒。
- 原始 main 和 GUI 未修改，本轮不执行真实模型请求、GUI／Android／iPhone动作，不合并或部署。

原始JSON/text、原生探针和失败日志只保存在任务本地 evidence/，不上传密钥、截图或设备配置。
最终绑定与完整结果见下一节；历史中断和失败过程保留在后续记录，不作为当前通过依据。

首个原生 Darwin CI 暴露构造探针过宽：共享runner组含其他UID的PID967，读取返回EPERM。
该失败日志保留；随后将构造探针限定于自身统计+组枚举，未降低运行中受监管组的访问要求。
旧SHA 1ea007a 的soak在337.3秒主动中断：33010实例、33包已通过、0失败；它不是完整验收。
新SHA必须重新完成十轮soak和原生CI，不以这一中断结果冒充全绿。

## 独立复核后的生命周期修复

受控夹具先复现两项错误：监控与身份核对失败后stdio仍能收发after-fault；
快速退出后的nil-monitor回退在模拟编号复用时向另一自有组发送TERM。
另一个受控用例证实仅leader退出不能把初始空样本判为整组退出。
失败日志均保留，随后新增了以下实现与强制回归：

- 启动监控失败也采用同一个5秒有界收尾和single Wait；group／leader信号错误保留，未证明终止返回ErrProcessTeardownUnproven。
- 取消、超时、输出／资源超限不仅等待leader，还核对同组已观察成员退出；负对照故意只杀leader，四个路径必须明确报unproven，不能宣称成功。
- stdio实时观察monitor事件，Read／Write直接传播已锁存错误，关闭管道唤醒阻塞I/O，并立即启动有界收尾。
- prepared owner可用ResourceFailure／ResourceError监听实时故障。若组归属无法确认，只对原有Go Process句柄尝试终止，不用新FindProcess或复用编号作盲目信号。
- 初始空／已退出样本仍保留monitor身份对象，确认整组没有活动成员才记retired；retired组以后不按旧编号发信号。Darwin不再回退到无身份的组操作。

完整资源包三轮在该轮修复后270实例通过，113.547秒；仅既有Seatbelt共6次skip。
进一步的空组／实时故障／启动／终止／prepared专项三轮51实例通过，0失败、0skip，22.951秒。
超时负对照冷启动窗口从350ms改为2秒，未移除后代存活、unproven或错误传播断言。
该阶段记录时完整soak／CI尚待验收；现由上面的f70a213最终结果完成。中断的旧版本soak不计为通过。

## 十轮发现的终止竞态与补充复核

功能版e3f50cd的本机十轮中，多进程内存用例前9轮通过，第10轮在收尾返回EPERM；
整个soak因此未通过，并在719.27秒主动停止以修复，失败／中断日志保留。
独立原生探针30/30确认：KILL已使组内成员退出后，再对残留组发KILL会返回EPERM，
同时rusage仍可能保留旧RSS快照。现在先核对活动成员，已退出组不再重复发送信号，
当前内存只累计alive成员；活跃组的权限错误仍显式返回。

另一自有夹具先复现：leader先Wait退出、后代仍持有协议管道时，watcher提前结束，
随后monitor与signal失败无法唤醒阻塞Read。现在监听到monitor的终止／故障或显式Close，
不把leader的Wait作为组监管终点。三种失败证据均保留，没有删除断言或将其skip。
watcher、组终止、RSS与多worker内存聚合专项30轮共150实例通过，0失败、0skip，85.121秒。
macOS15.7.9 ARM64／Go1.26.9原生CI曾对e3f50cd运行完整资源包三轮，273实例通过、
仅既有Seatbelt六次skip；这一CI成功不替代失败的本机十轮。
之后的f70a213已重新完成完整soak、Context fixture矩阵与精确SHA CI，终态见本页最终验收表。
