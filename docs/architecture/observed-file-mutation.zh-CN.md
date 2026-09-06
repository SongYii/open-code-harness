# 基于观测状态的安全文件变更

**状态：** 已实现契约（中文阅读版）。本文与英文正本
[observed-file-mutation.md](observed-file-mutation.md) 完整同步；**两者若有出入，以英文正本为准。**

被接受的设计是[基于观测状态的安全文件变更设计](../superpowers/specs/2026-09-04-observed-file-mutation-design.md)，
实现计划在[这里](../superpowers/plans/2026-09-05-observed-file-mutation.md)，
证据台账是 [observed-file-mutation-evidence.md](observed-file-mutation-evidence.md)。

## 问题是什么，说人话

一个 agent 去写它从没读过的文件，或者读完之后隔了十分钟、隔了三次工具调用才写，那么这中间发生的任何改动都会被它毁掉。中间那个写入者可能是编辑器里的人、构建步骤、另一个 agent，也可能是这个 agent 自己早先犯的错。旧的 `write_file` 分辨不出「替换我刚读过的那个文件」和「替换现在那儿的任何东西」，因为它就是用 `O_TRUNC` 打开目标然后写。

改了两件事。每一个破坏性文件操作现在都带着一个**守卫**——一个关于「我预期会看到什么状态」的显式承诺；并且发布是**原子的**，所以写到一半失败是一件没发生过的事，而不是一个被截断的文件。

## 端口

`internal/harness/tools/ports.go`：

```go
type FileSystem interface {
	Resolve(ctx context.Context, workspace, requested string) (abs string, err error)
	Read(ctx context.Context, abs string, limit int) (read FileRead, err error)
	Write(ctx context.Context, abs string, data []byte, guard MutationGuard) (MutationResult, error)
	Edit(ctx context.Context, abs string, oldString, newString []byte, replaceAll bool, guard MutationGuard) (MutationResult, error)
	List(ctx context.Context, abs string, depth, limit int) (names []string, truncated bool, err error)
}
```

这里刻意不存在无守卫的写。调用方不可能忘记做出承诺，因为承诺就是一个参数。

`internal/harness/tools/files.go` 只放词汇，别的什么都没有——没有 I/O、没有文件系统、没有时钟：

```go
type FileVersion string          // 不透明；只比较相等，从不解析
type GuardKind string            // "create_if_absent" | "replace_if_version"
type MutationGuard struct { Kind GuardKind; Version FileVersion }
type FileRead struct { Data []byte; Truncated bool; Version FileVersion }
type MutationResult struct { Version FileVersion; Operation MutationOperation }
const MaxEditFileBytes = 1 << 20
```

`MutationGuard.Validate` 只接受四种组合：不带版本的 create 守卫，和带版本的 replace 守卫。带版本的 create 守卫是在对一个它声称不存在的文件做断言；不带版本的 replace 守卫则是一个披着守卫外衣的无条件覆盖。两者都在碰到任何真实文件之前就被拒绝。

`FileRead.Truncated` 和版本一起传递是刻意的。用一次被裁剪的读取导出的守卫，会让 agent 凭着只看过首页就去替换整个文件。

## 版本

`internal/harness/adapters/workspacefs` 把目标的身份字段按固定宽度大端做规范编码，再取 SHA-256 作为版本：

| 平台 | 字段 |
| --- | --- |
| Linux（`version_linux.go`） | device、inode、size、mtime 秒+纳秒、ctime 秒+纳秒、mode |
| macOS（`version_darwin.go`） | 同上，走 `Mtimespec`/`Ctimespec` |
| 其他平台（`version_other.go`） | size、mode、mtime 纳秒 |

包含 device 和 inode，是因为这里的发布是靠 rename 完成的，所以「文件身份在读者脚下发生变化」在这套机制里是常态而不是奇事。包含 ctime，是因为它会在只改元数据（比如 `chmod`）时移动，而那种改动不碰 size 和 mtime。

`version_other.go` 的存在只是为了让这个包能交叉编译——尤其是 Windows——并且**不做任何运行时承诺**。没有 device 和 inode，它无法察觉一个文件被另一个大小、权限、时间戳都相同的文件替换掉。想要真正保证的平台需要自己加一个文件，而不是让一个更弱的版本悄悄顶替。

这个 token 是哈希出来的而不是拼出来的，就是为了让它保持不透明：没有哪个消费方能开始依赖它碰巧能解析出来的 inode 号。

## 读就是观测

`Read` 打开一个在牢笼内的常规文件，在读取 `limit+1` 字节的前后各从打开的描述符上取一次版本，两者不一致就报 `fs_stale_version`。字节和一个它们并非在其上被读出的版本配对，会让所有基于它的守卫都变成假承诺。

它最多返回 `limit` 字节，还有更多时置 `Truncated`，丢掉裁剪留下的不完整 rune，然后拒绝非法 UTF-8 内容并报 `fs_not_text`。丢 rune 在前，这样在字符中间切一刀就不会被算成这个文件的问题。目录和特殊文件都会在打开前以 `fs_not_regular_file` 拒绝，因此读取 FIFO 不会阻塞。

## 变更

`Write` 和 `Edit` 走同样的顺序，而这个顺序本身就是契约：

1. context 取消；
2. `guard.Validate()`；
3. 工作区牢笼（`Resolve`/`jail`，本次工作没有改动）；
4. 拿到按目标划分的锁，然后**在锁内重新过一遍牢笼**，因为这中间这个路径可能已经变成了一个指向工作区外的符号链接；
5. 用当前状态检查守卫；
6. 仅 `Edit`：读取并匹配；
7. 暂存、sync、发布。

按目标划分的锁不能替代守卫。守卫防的是本进程管不到的写入者；锁只是阻止一个进程在第 5 步和第 7 步之间和自己赛跑——只有守卫的话那里会留一个窗口。锁表项带引用计数、归零即删，所以一个长期存活的 `FileSystem` 不会为每个碰过的文件攒下一把互斥锁。

### 发布

目标文件绝不会被以截断方式打开。替换内容先写进一个权限为 `0700` 的私有同级 `.och-stage` 目录，sync，套上原文件的权限位（create 的话是 `0600`），关闭。然后：

- create 用 `os.Link` 发布，目标已存在就会失败；
- replace 用 `os.Rename` 发布。

发布之后，保留在暂存描述符上的 verifier 会确认目标仍然指向该暂存身份，并确认其版本稳定、字节精确等于预期 payload。不匹配时返回零值 `MutationResult` 和 `fs_stale_version`；绝不返回可能描述外部写入者字节的版本。payload 比较通过一个固定的 32 KiB scratch buffer 流式进行，因此验证不会给 `Write` 施加 edit 的大小上限。

父目录尽力 sync，暂存目录随后删除。link 或 rename 之前的任何失败，都让原文件保持原样。

暂存目录必须是同级目录而不是进程临时目录，因为 link 和 rename 只在同一个文件系统内有效。

### 编辑

`Edit` 是有界的 UTF-8 字面量替换，没有任何形式的模式语言。它最多读 `MaxEditFileBytes+1`，超出就以 `fs_too_large` 拒绝。在分配任一转换后输出之前，它会检查 CRLF 归一化中间结果和 CRLF 恢复后的最终结果都不超过 1 MiB；任一超出均为 `fs_too_large`。这个上限是内存上限，而且把源文件编辑一半比拒绝掉更糟。`Write` 没有 edit 大小上限。

匹配时把 CRLF 归一成 LF，因为调用方是在拿它被展示过的文本做匹配。发布时恢复文件自己占多数的行尾，因为一次从未声称要动行尾的编辑，不该悄悄把 CRLF 文件的每一行都重写一遍。

`replace_all` 是唯一的匹配选项。不加它时，多于一处匹配会以 `fs_ambiguous_edit` 拒绝，而不是随便挑一处改掉。

**守卫在查找字面量之前就被检查。** 否则，一个状态已过期、而字面量恰好又不存在的调用方，会被告知「你的文本不在那儿」并被打发去找文本，而真正的答案是文件在它脚下变了、需要重读。

## 观测

`internal/harness/application/file_observations.go` 保存每个会话读过什么的表。三种状态，任何两种都不可合并：

| 状态 | `write_file` 守卫 | `edit_file` 守卫 |
| --- | --- | --- |
| 未见过（没有表项） | `create_if_absent`——失败关闭 | 拒绝，`fs_not_observed` |
| 观测为不存在 | `create_if_absent` | 拒绝，`fs_not_found` |
| 观测为存在 | 按观测版本 `replace_if_version` | 同左 |

未见过或观测为不存在后，写入都使用 `create_if_absent`，所以已存在的文件会被拒绝而不是被覆盖；Application 把原始 `fs.ErrExist` create conflict 映射成 `fs_not_observed`。编辑没有这种兜底：未见过时报 `fs_not_observed`，已经观测为不存在时报 `fs_not_found`。

这张表**只存在于进程内，从不持久化**。版本是关于「这台机器上此刻这个文件」的事实；把它写进 Domain 事件会让它看起来像持久历史，而一个在另一台主机上恢复的会话就会带着描述它从没见过的文件的守卫。这个后果是被明说而不是被藏起来的：重启，或任何持有同一个持久 Session 的第二个进程，都从「什么也没见过」开始。

清空发生在**成功的 resume、close 和 delete** 上。它刻意不发生在普通的 load 上，否则每个 Turn 都会以「无法改动任何它读过的东西」开场。resume 要清空，是因为那个间隔可能任意长，会话在那之前看到的东西已经不再是关于磁盘现状的证据。

失败的变更绝不推进观测。那个显而易见的善意 bug 是：既然被拒了，那就刷新一下观测让重试成功——这会把守卫变成减速带，让第二次尝试毁掉 agent 从没看过的工作。

## 模型看到什么

模型从不看到、提供或被问及版本。Application 在 Policy 和 Approver 跑完之后、紧挨着适配器调用之前导出每一个守卫。新鲜度不等于授权。

`edit_file` 加入原有的四个内置工具，`RiskWrite` 且会变更，因此直接继承 `write_file` 所在的那张 Policy 表：

```json
{"type":"object","additionalProperties":false,
 "required":["path","old_string","new_string"],
 "properties":{
   "path":{"type":"string","minLength":1,"maxLength":4096},
   "old_string":{"type":"string","minLength":1,"maxLength":32768},
   "new_string":{"type":"string","maxLength":32768},
   "replace_all":{"type":"boolean"}}}
```

`old_string` 和 `new_string` 沿用 `write_file` 自己那 32,768 字节的参数上限，而不是另造一个。两条 JSON schema 表达不了的规则住在 Application 里：编辑需要先有一次读，以及 `old_string` 不得等于 `new_string`——一次改变不了任何东西的变更，照样要花掉一次审批和一次发布。

支持 `replace_all` 需要 schema 编译器接受布尔叶子。这个叶子拒绝一切从别的类型借来的关键字，因为布尔本身没有任何约束，而一个写了却不生效的边界比没有边界更糟。`compileSchema` 现在还要求根必须是 object：下游的每一条保证都是用 object 的 properties 表述的。

一次成功的编辑回答 `edited file` 或 `replaced all occurrences`。它不会把结果文件抄回去，那正好会花掉编辑工具本来要省下的上下文预算。

### 失败词汇表

八个码，因为每一个都对应不同的下一步。它们作为 Turn 内普通的失败 Tool Result 抵达模型；没有一个会渲染路径、版本或文件内容。

| 码 | 消息 |
| --- | --- |
| `fs_not_observed` | read the file before changing it |
| `fs_not_found` | file does not exist; create it or re-read after it appears |
| `fs_stale_version` | file changed since it was read; re-read it and retry |
| `fs_edit_not_found` | literal was not found |
| `fs_ambiguous_edit` | literal appears more than once; include more context or use replace_all |
| `fs_not_regular_file` | target is not a regular file |
| `fs_not_text` | file is not valid UTF-8 text |
| `fs_too_large` | file exceeds the edit size limit |

有一处翻译发生在 Application，因为适配器不可能知道得更多。create-if-absent 守卫在目标已存在时返回原始 `fs.ErrExist`；Application 把这个 create conflict 映射成 `fs_not_observed` 和 read-before-change 恢复消息，不暴露 adapter 细节。

## 边界值

| 边界 | 值 | 位置 |
| --- | --- | --- |
| edit 源文件、归一化中间结果和 CRLF 恢复后的最终输出 | 各 1 MiB | `tools/files.go` / `workspacefs` |
| `Write` payload | 无 edit 大小上限 | `workspacefs` |
| 已发布写入的 verifier scratch | 固定 32 KiB | `workspacefs` |
| `old_string` / `new_string` | 各 32,768 字节 | `edit_file` schema |
| `path` | 4,096 字节 | 每个文件工具的 schema |
| 读取上限 | `MaxToolResultBytes` | Application 的读路径 |

## 这套机制不覆盖什么

这些是以测试而不是以免责声明的形式存在的——见证据台账。

- **`exec` 不受中介。** 一条命令可以重写工作区里的任何东西，这套机制既不知情也不阻止。它承诺的是伤害不会被叠加：针对 `exec` 改过的文件的下一次结构化写入会被判为过期而拒绝，而不是覆盖上去。
- **守卫验证与 rename 之间的外部写入者。** 不合作的外部写入者可在 `checkGuard` 之后、我们的 `os.Rename` 之前改动目标；我们的 rename 可能覆盖该竞争版本。verifier 随后看到的是我们的暂存身份和预期字节，而不是被覆盖的版本，因此无法发现这场竞争。要关掉它，需要本项目没有的内核级 compare-and-swap 原语。
- **最终验证后的外部变更。** verifier 会在最终目标检查之前发现暂存身份、payload 或版本的变化。文件若在最终稳定验证 / return 边界之后才被外部写入者改动，仍在保证范围之外；只有下一次带守卫的操作能把它发现为 stale。
- **Windows 运行时。** 这个包能交叉编译，`version_other.go` 也给了它一个版本函数，但那里不声称也没有测试任何运行时行为。
- **跨进程观测。** 两个 `och` 进程操作同一个工作区，各有各的表，谁也看不见对方读了什么。守卫依然会拒绝第二个进程的盲写，而那才是真正重要的性质。
- **目录、设备、套接字。** 以 `fs_not_regular_file` 拒绝，而不是去处理。
