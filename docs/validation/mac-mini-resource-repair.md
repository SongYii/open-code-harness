# Mac mini CPU／内存资源修复

日期：2026-10-14 UTC。中文为本轮权威验收说明。
状态：实现及资源包三轮 race 已通过；完整十轮 soak 和新提交 CI 尚待完成。
历史基线和失败结果保留于 [前一阶段报告](mac-mini-acceptance.md)。

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
待完整回归完成后在此补入精确功能提交、soak／Context矩阵和远端CI结果。

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
最终提交的完整soak及CI仍待本页后续结果补齐，不能将中断的旧版本soak计为通过。
