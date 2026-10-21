# 05 — Sync / Revision / Recovery：单一发布路径、不可变 Revision、先数据后状态与 takeover fencing

> **ARCHIVED 2026-10-16** (repo review 2026-10-16 wave 4): describes the v0/v1 design and is kept for history only — do not build from it. Current reference: [reference.md](../reference.md); decisions: [v2-converge.md](../v2-converge.md). Live mechanisms this file still held were moved there before archiving; section anchors are unchanged.

> **ARCHIVED（repo review 2026-10-16 G-1）— 本文大部分描述已删除的机制：Revision/CAS/manifest、`SYNC-*` 码、watcher dirty 位、删除保护、generation 收尾的 flush 证据（只有 §7.5/§7.6 是现行机制，已迁至 v2-converge §14）。标有 ARCHIVED 横幅的节 do not build from this；未标横幅的节也只作历史与溯源参考。现行设计权威：[v2-converge.md](../v2-converge.md)、[wi-kind-contract.md](../wi-kind-contract.md)、[08-capsule-base-migration.md](../08-capsule-base-migration.md)；与代码冲突时一律以代码为准。**
> Most of this document describes deleted mechanisms. Sections bannered ARCHIVED must not be built from; the live authority is v2-converge.md plus the code.

| 权威项 | 值 |
|---|---|
| 文档定位 | 全扫描发布的单一路径（watcher dirty 标记 + 四类触发事件）、不可变 Revision 数据模型与文件格式、HEAD 推进、先数据后状态、同步传输机制（裁定）、takeover fencing、删除保护、崩溃恢复 |
| 上游基线 | `00-architecture.md#§2.3`（INV-4/INV-5/INV-11）、§4.2/§4.3、§7；`02-aihub-core.md#§4.3`（owner generation）/§4.6（repo inventory）/§4.8（capsule 登记表与 HEAD 指针）/§4.11（本地数据目录布局，含 CAS 存储路径）、§5.1（发布挂载点）、§5.2（read-through）、§6.4（generation 收尾的 flush 证据协议，本文定义其 Daemon 侧证据生成，校验侧归 `02`）；`03-execution-capsule.md#§3.1`（Capsule 目录布局）、§4.2（`runtime-state/execution.json` 原子提交契约）、§4.3（repo inventory 检出基线）、§4.4（harness 会话目录同步范围的最终契约）、§6（落盘与写限制权威）；`04-local-runtime.md#§2.4`（INV-D1/INV-D2/INV-D3）、§6.3/§6.4（触发请求发起与 Daemon 机械执行的分工）、§6.5（优雅停机实现序列） |
| 术语权威 | 本文负责：Revision、HEAD、watcher（dirty 标记）、全扫描发布触发时机、先数据后状态、takeover fencing（判定条件与错误码）、删除保护、同步传输机制、崩溃恢复（同步域） |

> **执行模型勘误（polyforge#111 删除 pass，2026-10-09）**：Phase-3 引擎切换（`d9bb866`/`f2163fe`）后，默认执行模型是
> Daemon 进程内的 **Go 执行驱动**（`internal/polyforge/driver`，`-use-driver` 旗标现已删除，driver 是唯一引擎）；WI-Agent 派生进程
> 旧引擎及其 `-use-driver=false` 开关均已删除。本文的同步协议实质（单一发布路径、先数据后状态、
> fencing）不受执行模型影响；受影响的只是「被协调的写入方/被检测的进程」的身份：驱动路径下，同步根的写入方是
> 驱动派生的 step agent 子进程，驱动的崩溃恢复 = 从 `execution.json` 重新推导驱动，不存在「新派生的 WI Agent 进程」。
> 本文写于 WI-Agent 模型，正文不整篇重写；每处「WI Agent」引用按下列行尾标记分类（可机械检索 `#111分类`）：
>
> - **〔#111分类：删→驱动〕** —— 该行所述的 WI Agent 进程机制在默认引擎下已删除，职责由驱动承接；按「执行者 = 驱动」读，裁定本身仍有效。
> - **〔#111分类：替换后仍成立〕** —— 该行陈述在「执行者 = 驱动」替换下逐字仍成立。
> - **〔#111分类：历史记录〕** —— 引用裁定/历史文档/已废弃草稿的原文，按原样保留，不代表当前执行模型。
>
> **未清理声明**：本 pass 只覆盖 02/03/05 三文；`00-architecture.md`（86 行）、`01-workflow-skills.md`（27 行）、
> `04-local-runtime.md`（100 行）、`07-deployment-pilot.md`（22 行）仍携带未分类的 WI-Agent 引用，不属于 polyforge#111 的验收范围。

## 1. 范围与非目标 <!-- §1 -->

### 1.1 范围 <!-- §1.1 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（持久 WI Agent、owner generation、Revision/CAS/manifest、flush 证据、watcher dirty 位、takeover）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

- **单一发布路径的裁定**：全扫描发布只有一条路径——由 `00-architecture.md#§4.3` 列出的四类事件（step 完成、step
  中断、WI 出最终结果、Daemon 优雅停机，以及可选 milestone）触发；watcher 不触发任何发布，唯一
  职责是把 Capsule 标记 dirty（§2.3）。「reconciliation（全量对账）」不是第二条独立路径，而是这
  同一条路径在每次触发时刻做的事情的另一个名字（§2.4）。
- 不可变 Revision 的数据模型与文件格式（§4，权威）。
- HEAD 推进规则与「先数据后状态」发布顺序不变量（§2.5、§2.6）。
- **冻结协议与发布互斥（freeze window）**：全扫描发布前，Daemon 必须先协调该 WI 的 WI Agent（wi agent） 〔#111分类：删→驱动〕
  让本 Capsule 的写入静止，扫描才能对同一冻结窗口内的字节做一次性打包；冻结的获取、发布互斥的
  持有与释放均由 Daemon 主导，不由 WI Agent 自行管理（§2.7，修复 B01）。 〔#111分类：删→驱动〕
- **幂等重放与 HEAD 校验的处理顺序**：先查幂等命中，未命中才校验 HEAD（§2.8，修复 B02）。
- **发布确认的持久化与消歧**：pending publish 记录的持久化、确认丢失后的 GET HEAD 三分支
  消歧、以及 manifest 幂等身份排除时间性字段（§2.8/§4.2，修复 B10）。
- takeover fencing：判定条件与错误码（§2.9）。
- 删除保护（§2.10）。
- generation 收尾 flush 证据的 Daemon 侧生成，含无 HEAD 空链情形（§6.1；校验权威在
  `02-aihub-core.md#§6.4`，修复 D6）。
- 崩溃恢复的三类场景：本机崩溃、同步中断、takeover 竞态（§7）。

### 1.2 非目标 <!-- §1.2 -->

- 不设计 Capsule 目录与可见面（`03` 权威；本文只定义被同步的目录集合，§4.1）。
- 不设计 Core 登记表的 DDL（`02-aihub-core.md#§4.8` 权威；本文只定义其写入协议）。
- 不设计云对象存储后端（S3/JuiceFS 适配，见 `00-architecture.md#§11` 演进）。
- 不解决跨 Capsule 的多 WI 协作同步（一期 WI 间协作经 Core API）。
- 不定义任何周期性/定时触发的发布路径——一期不存在「每 N 分钟对账一次」这种独立调度；dirty
  标记只是本地待发布状态，消费时机完全由 §1.1 的四类事件决定（对齐 `00` INV-11）。
- 不重复定义 Capsule 落盘/写路径的路径逃逸防护细节（symlink 目标校验、写路径白名单等）——那是
  `03-execution-capsule.md#§6` 的权威内容；本文只在恢复流程中重申必须遵守该规则（§7.4、
  §8）。

## 2. 术语与不变量 <!-- §2 -->

### 2.1 Revision（权威定义） <!-- §2.1 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（Revision/CAS/manifest）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

**Revision**：一个 Capsule 同步面在某次触发事件时刻的**不可变增量快照**。性质：一经发布不可改写
（`00` INV-4）；每个 Revision 按序号 `seq` 单调递增，指向唯一 parent（前一 seq）；内容 = 相对
parent 的文件操作集合（§4.2）。**第一个 Revision（`seq=0`）的 `parent` 字段为 `null`**（哨兵值，
不是指向 Capsule 基线的真实 seq——Capsule 基线，即 repo inventory 检出基线 `03-execution-capsule.md#§4.3`，不是一个
Revision，没有 seq 号，不能被引用为 parent）。parent/HEAD 的完整引导规则见 §2.7。

### 2.2 HEAD（权威定义） <!-- §2.2 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（owner generation、Revision/CAS/manifest）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

**HEAD**：Core 中 `capsule_heads`（`02-aihub-core.md#§4.8`）记录的指针，指向某 WI 最新**完整发布**的 Revision
`seq`。推进规则（§6 步骤 6）：仅当 ①候选 Revision 的全部 CAS 数据块已在 AIHub 数据目录落盘且
摘要校验通过，②其 `parent == 当前 HEAD`（或命中 §2.9 的幂等路径而豁免此项），③发布者 generation
为当前活跃 generation——同时成立时，才在**单个写事务**内推进 HEAD。HEAD 永远指向完整 Revision，
绝无「先移指针后补数据」。

### 2.3 watcher（唯一职责：标记 dirty） <!-- §2.3 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（持久 WI Agent、Revision/CAS/manifest、watcher dirty 位）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

**watcher**：Daemon 内对 Capsule 同步面（§4.1 定义的 `runtime-state/execution.json`、`artifacts/`、
`logs/`、`repos/<name>/`，以及可选纳入同步的 `harness/<adapter>/session.json`（`03-execution-capsule.md#§4.4`），
含 untracked 新文件；`harness/<adapter>/session-data/` 不属于同步面，watcher 不观察它；WI Agent 第 0 步物化的 〔#111分类：删→驱动〕
`.pi/agents/`、`.claude/agents/` 与 `workflow.md`（或 `workflow/`，`03-execution-capsule.md#§4.5`）同样不属于
同步面——见 §4.1 排除清单（SYNC-INV-10），watcher 不观察它们）的文件事件监听器。**watcher 的唯一职责是把该 Capsule 标记
为 dirty**——一个布尔位加一次写入时间戳，写入 Capsule 本地的同步暂存区（`<capsule>/sync/`），
不做任何其他事情：

- watcher **不**扫描全部同步根、**不**计算 manifest、**不**构建 Revision、**不**调用发布 API。
- watcher **不**定时轮询后自主发布；没有"去抖窗口到了就发一次"这种行为。
- dirty 标记的唯一用途：在下一次 §2.4 四类事件之一触发全扫描时，供该次扫描判断"自上次发布以来
  是否确有文件事件发生过"（可用于跳过一次明显无变化的扫描，是性能优化，不是正确性依据——即使
  dirty 位因某种原因丢失，触发事件到来时仍会做一次完整扫描，见 §6）。

这是 `00` INV-11 的直接落实：watcher 不构成任何发布触发时机。

### 2.4 全扫描发布：唯一路径（INV-11 展开） <!-- §2.4 -->

> **ARCHIVED — 本节描述已删除的机制（watcher dirty 位、WI Agent 发起的全扫描发布）；do not build from this.** 另：「无重试调度」不成立——异步推送失败后 10s 重试、5 次/30 分钟发 backpressure 事件、同错 5 次停止本轮（v2-converge §14.1）。

**只有一条发布路径**：由 `00-architecture.md#§4.3` 的四类事件——step 完成、step 中断、WI 出最终结果、Daemon
优雅停机、可选 milestone——触发的一次全扫描 + 打包 + 发布（§6 的六个编号步骤）。

历史草稿中曾把「watcher 增量感知」与「周期性 full reconciliation（全量对账）」描述为两条并行的
同步子系统：前者「快」、后者「对」，各自独立触发。**该描述已废弃，不得再出现在任何后续修订
中**：reconciliation 不是一个独立的、按周期或按静默窗口自主触发的第二路径，它就是"四类事件触发
时刻所做的那次全扫描"本身——两者是同一件事的两个历史名字，本文档统一使用「全扫描发布」这一
名称，「reconciliation」仅作为该动作内部"比对本地文件集合与远端已发布内容"这一子步骤的说法保留
（见 §6 步骤 2、§7.2）。

全扫描发布**触发请求的发起者**与内容对齐 `00-architecture.md#§4.3`：

| 触发时机 | 请求发起者 | 说明 |
|---|---|---|
| step 完成 | WI Agent | 每个 step 成功/失败结束时，向 Daemon 发起一次发布请求 〔#111分类：删→驱动〕 |
| step 中断 | WI Agent | 基础设施失败、gate FAIL rewind 等非正常终止路径，向 Daemon 发起一次发布请求 〔#111分类：删→驱动〕 |
| WI 出最终结果 | WI Agent | wrap / fail 时，向 Daemon 发起一次发布请求 〔#111分类：删→驱动〕 |
| Daemon 优雅停机 | Daemon | `00-architecture.md#§3.5`，代替所有仍在进行的 WI Agent 各自向自己发起一次请求 〔#111分类：删→驱动〕 |
| milestone（可选） | WI Agent | workflow 定义的可选检查点，非强制，向 Daemon 发起一次发布请求 〔#111分类：删→驱动〕 |

没有第六种触发时机；watcher 检测到的文件事件不在此表中，也不能加入此表。

**发起者与执行者是两件事（`00` INV-11、`04` INV-D3）**：上表的"请求发起者"只回答"谁认为现在
该发一次"；无论发起者是谁，**实际的冻结协调、全扫描、manifest 打包与向 AIHub 提交推进 HEAD，
恒由 Daemon 一方机械执行**（`04-local-runtime.md#§6.4`）。本文档此前的历史草稿中出现过"WI Agent 直接向 〔#111分类：历史记录〕
AIHub 发布""只有 WI Agent 能写 Revision""冻结不涉及 Daemon"等表述，均已废弃，不得再出现在 〔#111分类：历史记录〕
任何后续修订中：WI Agent 在同步链路中的全部职责仅止于"发起请求"（以及配合 Daemon 的冻结 〔#111分类：删→驱动〕
确认，§2.7），它不持有直接调用 §5.1 发布接口的凭证或路径；持有该凭证、发起块 PUT 与
manifest POST 的一方始终是 Daemon。

### 2.5 先数据后状态（不变量，`00` INV-4 展开） <!-- §2.5 -->

发布顺序恒为：**数据块全部落盘（fsync）→ manifest 落盘 → Core 写事务推进 HEAD**。任何一步失败，
HEAD 不前移；失败后的收敛发生在下一次 §2.4 触发时刻的全扫描内（不存在独立的重试调度，§7.2）。

### 2.6 HEAD 引导规则（parent/HEAD 哨兵，解决 seq=0 自指矛盾） <!-- §2.6 -->

> **ARCHIVED — 本节描述已删除的 Revision HEAD 引导与 `SYNC-*` 码；do not build from this.** 另：现行 capsule 链首个提交 seq = 1（不是 0），见 v2-converge §14.1。

`capsule_heads`（`02-aihub-core.md#§4.8`）在任何 Revision 发布之前**无行**。发布校验分两支（`SYNC-409` 的完整
判定，与 §6 步骤 5/6 一致）：

- **首发（bootstrap）**：若 `capsule_heads` 中该 WI **无行**，则只有 `seq=0 且 manifest.parent=null`
  的发布能被接受；成功后事务内**插入** `capsule_heads` 行（而非 UPDATE）。任何非 0 的 seq 或非
  null 的 parent 在无行时均拒（`SYNC-409`）。
- **后续发布**：若 `capsule_heads` 中已有行，则必须 `manifest.parent == capsule_heads.seq`
  （标准链式推进，除非命中 §2.9 幂等路径），事务内 **UPDATE** 该行。
- 两支互斥：服务端以"`capsule_heads` 行是否存在"作为唯一判别依据，不依赖客户端声明的 seq 值。
- `03-execution-capsule.md#§6.1` `create_capsule` 重建新 Capsule（隔离重建或全新 WI）时，若远端已有 HEAD，由 §7.4
  的恢复流程拉取已发布链，**不重新走 bootstrap 分支**（该分支仅属于真正无 HEAD 的全新 WI）。

### 2.7 冻结协议与发布互斥（freeze window，修复 B01：快照一致性） <!-- §2.7 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（持久 WI Agent、Revision/CAS/manifest）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

**问题**：WI Agent 在一次 step 执行期间可能连续修改多个文件（自身写入或经其派生的 step agent（subagent）写入 `repos/`/`artifacts/`/`logs/`），而不逐次记录版本；若扫描发生在这 〔#111分类：删→驱动〕
些写入进行中的任意时刻，打包出的 manifest 可能混合了跨越两个不同逻辑时刻的字节（例如新产物文件
已落盘但对应的 `runtime-state/execution.json` 尚未原子写入新版本，或反之），使得该 Revision 描述的不是任何
一个真实存在过的一致状态。

**权威定义（两个不同粒度的控制，不得混为一谈）**：

- **write freeze（写冻结）**：使该 Capsule 的写入方（WI Agent 自身与其当前派生的 〔#111分类：删→驱动〕
  step agent）在本次扫描 + staging 固化期间不再产生新写入的短暂状态。已经在执行
  中的单个原子写入操作（如 `03-execution-capsule.md#§4.2` 要求的"先写产物、最后原子写 `runtime-state/execution.json`"
  契约里的那次原子写）允许自然完成，不被中途打断——中途打断会破坏该原子提交契约本身，比不冻结
  更糟。write freeze 的持有时长只覆盖"确认停止写入 → 扫描 → 摘要计算 → 构建 manifest → 把本次
  将要上传的全部字节固化进 Daemon 管理的不可变 staging blocks"（见下）这一段本地操作；一旦这些
  字节已经固化到 staging，Daemon 立即释放 write freeze，WI Agent 恢复写入——此后即使工作树 〔#111分类：删→驱动〕
  内容继续变化，也不影响本次发布，因为本次发布已经不再读取工作树，只读取 staging。
- **per-WI publish mutex（发布互斥）**：粒度是 per-WI（不跨 WI，不同 WI 的发布流程互不阻塞）的
  一把串行锁，覆盖"获取 write freeze → 扫描 → staging 固化 → 释放 write freeze → 块上传 →
  manifest 提交 → HEAD 推进的成功或失败处理全部完成"这一整条流程；只有当本次流程的结果（成功
  推进 HEAD、或确定失败留待下次重试）已经处理完毕，Daemon 才释放 publish mutex 并开始接受该 WI
  的下一次发布请求。publish mutex 因此比 write freeze 持有得更久：write freeze 的释放不等待
  网络往返，publish mutex 的释放必须等待。Daemon 每收到该 WI 的一次发布请求时先尝试获取 publish
  mutex（若该 WI 已有一次发布流程在途，新请求排队等待其释放，不并发发起第二次扫描），避免同一
  WI 的两次扫描相互踩踏或对同一 `seq` 产生竞争的 manifest。两者均由 **Daemon** 持有和管理，
  不是 WI Agent——这是 `00` INV-11/`04` INV-D3 的直接推论：实际扫描、staging 固化、打包与 〔#111分类：删→驱动〕
  HEAD 推进只能由 Daemon 执行，WI Agent 不持有直接调用 §5.1 发布接口的凭证或路径。 〔#111分类：删→驱动〕

**获取/释放协议**：

- **获取（publish mutex 先于 write freeze）**：Daemon 收到一次发布请求后：
  1. 获取该 WI 的 publish mutex（若已被同 WI 的另一次发布流程持有，排队等待其释放）。
  2. 向该 WI 的持久 WI Agent 发出"到达安全边界并确认停止写入"的请求（本机进程间协调，传输 〔#111分类：删→驱动〕
     机制属 `04` 权威，本文只定义协议语义，不重复定义传输层）：
     - **step 完成/step 中断/WI 出最终结果**（强制触发）：按 `01-workflow-skills.md#§6.1` 的执行契约，WI Agent 〔#111分类：删→驱动〕
       在宣告这些状态转换之前就应已经停止自身与其 step agent 的写入，因此该请求在正常路径下应
       立即得到确认；确认内容是"当前没有进行中的写入操作，也不会开始新的"。
     - **milestone（可选）**：WI Agent 收到请求后主动暂停新写入的发起（不打断已在执行中的 〔#111分类：删→驱动〕
       单个原子写入操作），确认后回复 Daemon。
     - **Daemon 优雅停机 / 本机崩溃恢复（进程终止判定，修复 B01 追加）**：只有同时满足下列
       **两个条件**，Daemon 才可以不等待任何确认消息、不发起任何 IPC 调用，直接以此作为
       write freeze 已获取：
       ① **该 Job 的 WI Agent 进程本身、以及它曾经派生的全部命令执行子进程与 〔#111分类：删→驱动〕
       step agent 进程（含整个 Native runner 进程组，`03-execution-capsule.md#§5.1`/§7）已经全部
       终止**——Daemon 必须逐一确认进程组内每个已知 pid 都已退出（而不是只看 WI Agent 的 〔#111分类：删→驱动〕
       顶层 pid），因为一个仍在运行的命令子进程或 step agent 同样可能正在写
       `repos/`/`artifacts/`/`logs/`；只看 WI Agent 自身退出而不管其子进程组是否已回收， 〔#111分类：删→驱动〕
       不足以断言"不会再有新写入"。
       ② **代替该 Job 的新 WI Agent（若已经在被派生）尚未开始产生任何写入**——即本次 〔#111分类：删→驱动〕
       判定必须发生在"确认①成立"与"新 WI Agent 首次写入 `runtime-state/execution.json` 〔#111分类：删→驱动〕
       或同步根内任何其它文件"之间的空窗期内。若新 WI Agent 已经先于本次发布请求写 〔#111分类：删→驱动〕
       过任何同步根内容（例如 §7.1 崩溃续跑路径中新 WI Agent 已经原地重派当前 step 并 〔#111分类：删→驱动〕
       产出了新的 `execution.json` 版本），则"进程终止"判定不再成立——此时冻结窗口必须按
       上面的正常路径向这个新 WI Agent 发起确认请求，不得再假定"旧进程已死即安全"， 〔#111分类：删→驱动〕
       因为真正持有写入能力的现在是这个仍存活的新进程。
       同时满足①②时，没有任何进程持有写入能力，这是比任何确认信号都更强的保证；
       不满足任一条，Daemon 必须走正常确认路径。同一判定同样适用于 §7.1 描述的本机
       崩溃恢复路径。
  3. 确认到达（或按上一步①②同时成立的判定）后，即视为 write freeze 已获取，Daemon
     进入扫描阶段。
- **窗口内动作（write freeze 持有期间）**：
  1. Daemon 扫描（读取同步根全部文件、计算 sha256/mode）+ 构建 manifest 对象（§4.2，含
     SYNC-INV-3/SYNC-INV-6 的字段与固定顺序要求）。
  2. **staging 固化（修复：避免"上传阶段回读活目录"的一致性漏洞；B09：禁止硬链接）**：对
     manifest 中每一个
     `put`/`symlink` 操作引用到的字节，Daemon 在释放 write freeze **之前**，把该文件当前内容
     **独立复制**到 Daemon 本机管理的一处**不可变 staging 目录**，按其 sha256 命名存放
     （`<daemon_local>/staging/<wi_id>/<seq>/<sha256>`，不属于 Capsule 可见面，step agent
     不可见，`03-execution-capsule.md#§5.2`/§6.2 面表同一原则）。同盘时优先使用 reflink/CoW（`FICLONE`/
     `clonefile`/`cp --reflink`）以避免双倍磁盘占用，文件系统不支持或跨盘时退化为全量复制；
     **禁止硬链接**（修复 B09）——硬链接与活动工作树文件共享同一个 inode，write freeze
     释放后 WI Agent 对原文件的**原地写（in-place write）或 truncate 会直接改写 staging 〔#111分类：删→驱动〕
     中"已固定"的字节**，冻结窗口等于根本没有固定任何东西。reflink/CoW 与全量复制都保证
     staging 块自固化那一刻起与源文件**不共享 inode、不共享可变字节**（写入方任何后续
     改动——原地写、truncate、unlink 后重建——都只作用于写入方自己的 inode/私有 CoW
     页），staging 中的固定字节不受影响。这一步必须在 write freeze 释放前完成：只有在
     "确认没有新写入会发生"的窗口内复制，才能保证 staging 里固化的字节确实是这次冻结窗口
     所见的字节，而不是窗口释放后被新写入覆盖的内容。manifest 对象本身此时已经完整（内容仅
     是路径/sha256/mode 等元数据，不含数据字节），可以安全地脱离本次窗口继续使用。
- **释放 write freeze**：staging 固化**完成后**（即本次冻结窗口内的字节已经既被固定为一份完整
  的 manifest 对象、又被独立复制进了不可变 staging——与活动工作树文件不共享 inode，§2.7
  窗口内动作第 2 步），Daemon 立即释放 write freeze（通知
  WI Agent 恢复写入；WI Agent 已终止则无需通知），不等待块上传、manifest 提交或 HEAD 〔#111分类：删→驱动〕
  推进完成。write freeze 的时长因此只与"枚举 + 摘要计算 + manifest 构建 + 本机文件复制
  （同盘 reflink/CoW 优先，退化全量复制）"这几步本地操作的耗时成正比，与网络往返无关。
- **释放后的动作（不持有 write freeze，但仍持有 publish mutex）**：write freeze 释放之后、
  第一个块 PUT 发出**之前**，Daemon 先把本次发布持久化为一条 pending publish 记录
  （manifest 对象 + `manifest_sha` + `(seq, parent)` + staging 目录引用；内容与消歧协议见
  §2.8，修复 B10）——此后无论 Daemon 在上传/提交的哪一步崩溃，重启后都能凭该记录恢复
  "这次发布究竟有没有被服务端接受"的消歧所需全部信息。随后 Daemon 把 staging 中尚未在
  AIHub CAS 中存在的数据块通过 §5.1 的块 PUT 接口上传（携带
  `X-PF-Generation-Token`，接受 §2.9 fencing 校验，已存在块跳过）——**上传阶段只读取
  staging，不再重新读取 Capsule 的活动同步根目录**，因此工作树在此期间发生的任何新写入都
  不会污染本次发布的字节来源。全部块 fsync 落盘后，Daemon 提交 `POST manifest`（§5.1），
  处理其成功/失败结果（§6 步骤 5/6）；服务端确认落定（任何明确响应，或 §2.8 的 GET HEAD 消歧
  判定为"已被接受"）之后，pending publish 记录才被删除。
- **释放 publish mutex**：直到上一步的块上传与 `POST manifest` 的成功/失败结果处理**全部
  结束**（HEAD 已推进，或已确定本次发布未发生、留待下一次触发收敛），Daemon 才释放该 WI 的
  publish mutex，随后才开始处理该 WI 排队中的下一次发布请求（若有）。staging 目录在本次
  流程的成功/失败结果落定、**且不再被任何 pending publish 记录（§2.8）引用**后清理
  （成功：数据已进入 AIHub CAS，本机 staging 副本不再需要；
  失败：留给下一次触发时机的扫描重新生成，同一 sha256 内容幂等；被未消歧的 pending 记录
  引用的 staging 目录必须原样保留，供确认丢失后的重发/消歧使用）。
- **超时处理**：确认阶段设一个有界等待，仅在 WI Agent 进程仍存活但迟迟不确认时才会触发 〔#111分类：删→驱动〕
  （进程已终止且满足上面①②两个条件的情形按判定直接视为 write freeze 成立，不进入本超时
  逻辑）。分两种情况：
  - 强制触发（step 完成/中断/WI 最终结果）：等待超时视为 WI Agent 未遵守 `01-workflow-skills.md#§6.1` 执行 〔#111分类：删→驱动〕
    契约的基础设施异常，按 `00` INV-9 的基础设施失败语义处理（当前 step 原地 resume 并由
    WI Agent 重新发起发布请求，不触发 gate FAIL），Daemon 不允许在未确认冻结的情况下强行 〔#111分类：删→驱动〕
    扫描；超时同样释放 publish mutex（本次流程未发生，不留下悬挂的互斥）。
  - milestone（可选）：等待超时时 Daemon 直接放弃本次 milestone 扫描（不升级为基础设施失败、
    不重试），释放 write freeze 请求与 publish mutex，把发布推迟到下一次真正的四类触发
    事件；milestone 的存在理由只是"多一次可选检查点"，跳过一次不影响正确性。
- **与 step 执行推进的关系（不互相饿死）**：publish mutex 的粒度是 per-WI，不同 WI 的发布请
  求互不阻塞；同一 WI 内，write freeze 只在触发时机短暂出现且时长受限于本机扫描+staging
  固化耗时（通常远小于任何一个 step 的执行时长），WI Agent 与其 step agent 在 write freeze 之 〔#111分类：删→驱动〕
  外（含随后的块上传阶段）可以持续写入而不受任何阻塞；publish mutex 虽然覆盖到上传+提交结
  束，但它阻塞的只是"该 WI 的下一次发布流程"，不阻塞 WI Agent 继续驱动 step 或产生新的本 〔#111分类：删→驱动〕
  地写入（下一次发布请求会排队，不会丢失，§2.4）。milestone 触发在超时时主动放弃扫描并释放
  两把锁，进一步保证不会阻塞 step 从中断状态恢复，也不会阻塞该 WI 后续到来的强制触发请求。

### 2.8 幂等重放与 HEAD 校验的处理顺序（修复 B02） <!-- §2.8 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（持久 WI Agent、owner generation、SYNC-* 码、Revision/CAS/manifest）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

**问题**：若发布请求先校验 `parent == 当前 HEAD` 再判断是否为重复请求，会在下列两个合法重放
场景中产生错误的 `SYNC-409`：请求已在此前成功提交（Core 已 INSERT 且 HEAD 已推进到该 seq 或
更新的 seq），但响应在网络回程中丢失，客户端重发同一请求时看到的"当前 HEAD"已经不是这次请求
的 `parent` 值，若先校验 parent 就会被误判为冲突而非重放。在当前模型下，发布请求的发起方
恒为 Daemon（§2.7），重放也发生在 Daemon↔AIHub 这一段，与 WI Agent↔Daemon 的本机请求 〔#111分类：删→驱动〕
无关。

**权威处理顺序**（发布请求 `(work_item_id, seq, parent, generation, manifest_sha)` 到达时，
在同一次 API 调用内严格按此顺序执行，见 §6 步骤 4/5/6）：

1. **幂等检查（先于一切 HEAD 校验）**：按 `(work_item_id, seq, manifest_sha)` 查询
   `capsule_revisions`（`02-aihub-core.md#§4.8`）是否已存在匹配行。
   - **命中**：该 Revision 已经被成功接受过，直接返回成功（`{head_seq: 当前 capsule_heads.seq}`），
     **不再校验 `parent == 当前 HEAD`**——因为命中即证明这次发布内容此前已被完整处理过，HEAD
     此后可能已经继续推进，用"当前 HEAD"去比对一个历史请求的 parent 没有意义，比对本身就是
     用错了时间点。
   - **未命中**：进入第 2 步。
2. **HEAD 校验（仅未命中幂等记录时执行）**：校验 `manifest.parent == 当前 capsule_heads.seq`
   （无行时走 §2.6 bootstrap 分支）。不等 → 拒绝 `SYNC-409 SEQ_CONFLICT`，流程终止，不做任何
   写入。
3. **写入（校验通过后，同一事务内）**：`INSERT capsule_revisions` + `INSERT`（bootstrap）或
   `UPDATE`（后续）`capsule_heads`，提交后返回 `{head_seq: seq}`。

**两个必须命中步骤 1 的场景**（均不得落入步骤 2 的 HEAD 比对）：

- **已提交但响应丢失后重发**：Daemon 完成一次发布，Core 已提交事务并推进 HEAD，但响应包在
  返回途中丢失（网络中断/进程被杀）。Daemon 未收到确认，视为发布失败，在下一次发布请求（或
  同一请求的重试）时用相同的 `(seq, manifest_sha)` 重新提交。此次请求必须在步骤 1 命中同一行，
  直接返回成功。
- **进程重启后补发**：WI Agent 崩溃重启（`04-local-runtime.md#§7`、§7.1）时会向 Daemon 重新发起发布请求，或 〔#111分类：删→驱动〕
  Daemon 自身重启后重新发现待发布内容；此时 Daemon 重新走一次 §2.7 的冻结→扫描→打包，若该次
  扫描恰好重新生成了与此前某次已发布 Revision 完全相同的内容（同一 parent、同一文件集合，
  因而同一 `manifest_sha`——其成立前提正是下述"幂等身份排除时间性字段"的裁定，`built_at`
  的差异不会破坏摘要相等），此次提交同样必须在步骤 1 命中该历史行，直接返回成功，不因"Daemon
  认为自己还没发布过"而误走步骤 2 产生冲突。

**Daemon 侧持久化 pending publish 记录（修复 B10）**：上面两条规则保护的是"重发已成功的请求"
的幂等性，但它们都默认一个前提——Daemon 在崩溃重启后还拿得回当时那份 manifest 对象与 staging
字节。若 Daemon（或整机）在 `POST manifest` 发出后、服务端确认收到前崩溃，重启后的 Daemon
既不知道该次发布是否已被接受，也没有凭据重放它。因此：Daemon 在每次发布的块上传开始前（
§2.7 释放后的动作），必须把一条 **pending publish 记录**原子写入（tmp+fsync+rename，与
`03-execution-capsule.md#§4.2` 同一原子写模式）Daemon 本地持久存储，内容为：manifest 对象全文 + `manifest_sha` +
`(seq, parent)` + staging 目录引用（`<daemon_local>/staging/<wi_id>/<seq>/`）。该记录只在
**服务端确认落定**后才删除：确认的来源是 `POST manifest` 的任何明确响应（成功响应或
`SYNC-4xx` 错误码——错误码意味着本次发布的结果已经确定，无需再消歧），或下述 GET HEAD 消歧
判定为"已被接受"；仅当响应本身丢失/超时，记录才保留供消歧。被未删除的 pending 记录引用的
staging 目录不得清理（§2.7）。

**确认丢失时的 GET HEAD 消歧（修复 B10）**：Daemon 重启后（或对一次在途 `POST manifest` 的
响应超时/连接中断后）发现存在未删除的 pending publish 记录时，不得盲目重发，先调用
`GET /api/v1/work-items/{id}/head`（§5.1；404 视为无 HEAD 行）消歧，严格三分支：

1. **HEAD `seq` == 记录的 `seq`，且 HEAD 行的 `manifest_sha` == 记录的 `manifest_sha`** →
   该次发布实际已被服务端接受、HEAD 已停在它上面，**视为成功**：删除记录（及不再被引用的
   staging，按 §2.7 清理策略），本次发布流程落定为已成功，不重发任何请求。
2. **HEAD `seq` == 记录的 `parent`**（无 HEAD 行时，记录的 `parent` 为 `null` 的 bootstrap
   发布亦归入本分支）→ 服务端从未接受这份 manifest（HEAD 仍停在父位或链尚未引导），**可安全
   重发**：从记录引用的 staging 重传块（内容寻址、PUT 幂等，已在 CAS 的块跳过）并重提同一
   `(seq, parent, manifest_sha)`；即使存在"第一次提交实际已被接受、HEAD 随即又推进"的极窄
   竞态，重提也会在步骤 1 幂等检查处命中历史行直接返回成功，不会产生第二条 Revision。
3. **其它**（HEAD 已推进到记录 `seq` 之后；或 HEAD `seq` 相同但 `manifest_sha` 不同；或无
   HEAD 行而记录 `parent`≠`null`）→ 本 generation 之外的状态变化已经发生（被接管后的新代
   已发布、或该记录已过时），**走全量对账分支**：丢弃记录中过时的 `(seq, parent)` 声明
   （staging 保留至按 §2.7 清理策略回收），以远端 HEAD 为准，在下一次 §2.4 触发事件时重新
   执行一次全扫描发布（§6 步骤 1–6，即 reconciliation——本动作不是第二路径，§2.4）；期间
   由 §2.8 幂等检查与 §2.9 fencing 兜底。不得盲目重发旧 manifest：其 `parent` 已不等于当前
   HEAD，重发必然 `SYNC-409`，且可能干扰接管后的新代。

消歧期间不释放（也不重入）该 WI 的 publish mutex 语义：若消歧发生在同一进程内在途请求的响应
超时后，mutex 仍由本次流程持有直至三分支之一落定；若发生在 Daemon 重启后，mutex 随旧进程已
消亡（§2.7），重启后的消歧在任何块重传之前完成即可。

**manifest 幂等身份排除时间性字段（修复 B10）**：`manifest_sha` 是发布幂等身份的唯一载体
（步骤 1 的命中键、pending 记录的消歧键、`02-aihub-core.md#§4.8` 的存储值），它必须对"内容相同"的两个
manifest 恒等——而 `built_at` 是 manifest 中唯一的时间性字段，若它参与摘要，重新扫描生成
的相同内容会因时间戳漂移而永远得不到相同摘要，上面的场景二与三分支 1 的判定都会被破坏。
裁定：`manifest_sha` 的计算输入是 manifest 的**幂等视图**——`canonical_json` 序列化前先剔除
`built_at`（未来新增任何时间性/环境性字段必须同样列入剔除清单，见 §4.2）；磁盘上的
`manifest.json` 本身仍保留 `built_at`（仅供审计）。裁决中提到的等价替代（发布时携带独立请求
指纹）不采用：它多出一处需 Daemon/AIHub 两侧一致维护的派生值，不如直接收敛哈希输入。

本节与本文追加不变量 §2.12 的 SYNC-INV-2 是同一规则的两种表述。

### 2.9 takeover fencing（判定条件与错误码，`00` INV-5 展开） <!-- §2.9 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（持久 WI Agent、owner generation、HMAC 令牌、SYNC-* 码、Revision/CAS/manifest、takeover）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

**判定**：发布请求（发起方恒为 Daemon，§2.7）携带 `(work_item_id, generation, seq, parent,
manifest_sha)` 与 `X-PF-Generation-Token`（`02-aihub-core.md#§5.1` capability token，claim/takeover 时签发给
该 WI 的 Daemon，绑定 `(wi_id, generation, machine_id)`，不可伪造）；Core 在写事务内校验
token（HMAC 重算比对，非裸整数相等）。不等 → 拒绝 `SYNC-403 STALE_OWNER`，响应携带
`{retry_hint: "reclaim_required", reason: <Core 透传的拒绝原因>}`——`reason` 逐字透传 `02-aihub-core.md#§5.1` 的 CORE-403 细分：`stale_generation`（被接管→reclaim/force-takeover）|`secret_rotated_expired`（宽限轮换过期→claim 幂等重放重签）|`secret_rolled_back`（回滚作废→停止进程组、quarantine、admin force-takeover，`04-local-runtime.md#§7.3`）——C 组端点不吞并拒绝原因，处置按 reason 分流（澄清 5 的同步侧边界；不回传可伪造的裸 `current_generation` 整数，旧 Daemon
见此即知必须重新走 claim/查询获取新 token 才能继续为该 WI 发布，`02-aihub-core.md#§5.1` 同一修复）。
Core 写 API 的 generation token 校验（`02-aihub-core.md#§5.1` `CORE-403`）是同一 fencing 的应用层延伸。
此机制同时是 `03-execution-capsule.md#§2.4` 多机副本裁定（任何时刻至多一个活跃发布权持有者）的强制层。fencing 校验
在**块上传阶段**就已生效（不等到 manifest 提交才校验），理由见 §5.1。旧 owner 被接管后，即使
其 WI Agent 仍在运行并向旧 Daemon 发起新的发布请求，旧 Daemon 尝试向 AIHub 发布时仍会在本节 〔#111分类：删→驱动〕
被拒，旧 owner 一侧不存在绕过 Daemon 直接写 Revision 的路径。

### 2.10 删除保护（权威定义） <!-- §2.10 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（SYNC-* 码、Revision/CAS/manifest）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

远端 Capsule 数据（Revision 块、manifest、归档）的删除受三重保护：

1. **状态门**：仅终态 WI 可删（running/paused/blocked 一律 `SYNC-409`，本文 §5.2 错误码表；
   删除接口的签名与错误码权威在本文 §5.1/§5.2，`07` 仅交叉引用、不重定义）；
2. **确认门**：删除请求必须携带与目标 WI 匹配的显式 confirm token（重复输入 wi_id）；
3. **延迟窗**：删除先标记 tombstone（软删除，24h 内可撤销），硬删除发生在窗口后；且**永不**删除
   仍被任何 HEAD 或保留窗（90 天）内 checkpoint 可达的 Revision 块——「不可达 Revision 可从既有
   增量链重建」。

一切删除只经 Core API（`02-aihub-core.md#§8` 明确不提供文件系统清扫端点）；不存在旁路删除面。

### 2.11 与 `03` 落盘规则的关系（修复 B07：symlink 逃逸） <!-- §2.11 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（Revision/CAS/manifest）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

Capsule 落盘/写入的路径规则——同步根内的路径白名单、`../` 逃逸拒绝、symlink 目标解析后仍必须
落在同步根内等——由 `03-execution-capsule.md#§6`（核心流程与 Subagent 写限制）权威定义，本文
**不重复定义**这些细节。本文的义务只有一条：**恢复路径（§7.4 的现场重建）必须逐字遵守该规则**，
即从 CAS 块与 manifest 重建工作树时，任何 `symlink` 类操作（§4.2）在落盘前都必须经过与首次
写入时相同的路径校验，不允许因为"这是恢复重放、内容已被信任"而跳过该校验；manifest 中的
`symlink` 目标一旦解析后越出同步根，恢复流程必须拒绝该 Revision 的重建并报告为数据不一致，而
不是静默落盘。

### 2.12 本文追加不变量 <!-- §2.12 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（持久 WI Agent、owner generation、Revision/CAS/manifest、flush 证据、read-through）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

| 编号 | 不变式 |
|---|---|
| SYNC-INV-1 | 全扫描发布只有一条路径（§2.4）：由 `00-architecture.md#§4.3` 四类事件触发；watcher 与任何周期性任务都不构成独立触发时机。 |
| SYNC-INV-2 | 幂等检查先于 HEAD 校验（§2.8）：`(work_item_id, seq, manifest_sha)` 命中历史记录时直接返回成功，不比对 parent；未命中才校验 parent。 |
| SYNC-INV-3 | 每个 Revision 的 manifest 必须携带当前完整的 `runtime-state/execution.json`（`03-execution-capsule.md#§4.2`）的文件操作（该文件本身不单独声明一个名为 `progress` 的子字段，`03-execution-capsule.md#§4.2` 裁定其全部顶层字段合起来就是对外可见的“进度”），保证 read-through（`02-aihub-core.md#§5.2`）恒可从 HEAD Revision 直接读到完整的执行状态。 |
| SYNC-INV-4 | 全扫描发布前必须先完成一次冻结窗口的获取与释放（§2.7）；扫描到的字节必须来自同一个冻结窗口，不得跨窗口混合。 |
| SYNC-INV-5 | 恢复路径重建工作树时必须应用与首次写入相同的路径逃逸防护（§2.11，权威在 `03-execution-capsule.md#§6`）。 |
| SYNC-INV-6 | 构建 manifest 时，数据/产物文件（`repos/`、`artifacts/`、`logs/`、可选纳入的 `harness/<adapter>/session.json`）必须先于 `runtime-state/execution.json` 被固定（读取并计入 `ops`）；`execution.json` 对应的 `put` 操作在扫描/固定顺序中最后确定（§4.2、§6 步骤 3/4）。 |
| SYNC-INV-7 | staging 固化必须产生与活动工作树字节独立的副本（§2.7，修复 B09）：禁止硬链接，reflink/CoW 或全量复制均可；write freeze 释放后，工作树内任何原地写/truncate 不得改变 staging 已固定的字节与 sha256。 |
| SYNC-INV-8 | 块上传开始前必须已原子持久化 pending publish 记录，且仅在服务端确认（任何明确响应，或 GET HEAD 消歧判定为已被接受）后删除（§2.8，修复 B10）；确认丢失时必须先按 GET HEAD 三分支消歧（视为成功/安全重发/全量对账），不得盲目重发；`manifest_sha` 的幂等身份剔除 `built_at` 等时间性字段（§4.2）。 |
| SYNC-INV-9 | generation 收尾（`02-aihub-core.md#§6.4` 的 pause/block/wrap/fail）前，Daemon 必须在写冻结后实际执行一次 final flush 扫描并据此生成 flush 证据（§6.1，修复 D6）；无 HEAD 的首次 claim 后收尾仅允许 `published_seq=0`+`frozen_checkpoint.bytes_digest == EMPTY_SYNC_BYTES_DIGEST`（空同步面聚合摘要常量，且本 generation 从未产生同步面写入）这一种空链形态。 |
| SYNC-INV-10 | WI Agent 第 0 步物化的三个目标（`.pi/agents/`、`.claude/agents/`、`workflow.md` 或 `workflow/`）不属于同步根（§4.1）：watcher 不观察，manifest 的 `ops` 永不包含其路径，§7.4 恢复重建不写回；新 generation 一律由 WI Agent 按物化规则从本机 aihub checkout 重新物化（`03-execution-capsule.md#§4.5`）。 〔#111分类：删→驱动〕 |

## 3. 模块与代码目录 <!-- §3 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（Revision/CAS/manifest、flush 证据、watcher dirty 位）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

```
internal/sync/
├── revision/    # manifest 构建、CAS 块寻址、checkpoint（§4）
├── freeze/      # write freeze 与 per-WI publish mutex 的获取/释放协议、staging blocks 的固化与清理，由 Daemon 持有（§2.7）
├── watcher/     # 文件事件监听，仅写 dirty 标记（§2.3）
├── publish/     # 全扫描发布协议：扫描、打包、幂等检查、HEAD 校验、fencing，由 Daemon 执行（§5/§6）；pending publish 记录持久化与 GET HEAD 消歧（§2.8）；final flush 证据生成（§6.1）
└── recover/     # 远端现场恢复（§7.4）
```

`watcher/` 与 `publish/` 之间只有一个方向的依赖：`publish/` 在扫描前可读取 `watcher/` 写下的
dirty 标记作为"是否值得扫一次"的提示，`watcher/` 绝不反向调用 `publish/` 或 `freeze/`。

## 4. 数据结构：Revision 数据模型与文件格式（权威） <!-- §4 -->

### 4.1 同步面与 CAS <!-- §4.1 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（持久 WI Agent、owner generation、Revision/CAS/manifest、flush 证据）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

- **同步根**（权威目录集合，边界按 `03-execution-capsule.md#§4.4` 最终契约裁定）：`runtime-state/execution.json`、
  `artifacts/`、`logs/`、`repos/<name>/`，以及**可选**的 `harness/<adapter>/session.json`
  （`03-execution-capsule.md#§4.4`：稳定 session identity 的小体量元数据；是否纳入某次发布不影响正确性，纳入时
  只是作为**历史元数据**随该次 Revision 一并打包，供事后审计查看某次发布时对应的本机
  session 身份，**绝不构成任何一次 Capsule 重建/接管时恢复 harness 身份的依据**——见 §7.4
  关于 `harness/` 永远从零重建、不从 Revision 拉回的裁定）。**`harness/<adapter>/session-data/`
  不属于同步根**：它是仅本机运行的会话工作目录（Pi/Claude 各自的转录、工具调用缓存等不
  透明格式），`03-execution-capsule.md#§4.4` 明确裁定其不参与任何一次全扫描发布，watcher 不观察它，manifest 的
  `ops` 永远不包含该目录下的任何路径。**WI Agent 第 0 步物化的三个目标（`03-execution-capsule.md#§4.5`）同样明确排除在同步根之外（SYNC-INV-10）**： 〔#111分类：删→驱动〕
  `.pi/agents/`、`.claude/agents/`、`workflow.md`（或 `workflow/`）——harness 适配器 step agent 定义与本 WI 执行图快照，
  由 WI Agent 第 0 步从本机 aihub checkout 的 `workflows/<bundle>/` 复制而来——watcher 不观察，manifest 的 〔#111分类：删→驱动〕
  `ops` 永不包含其路径，§7.4 恢复重建不写回。排除理由：① **claim 时可重新物化**：三者是本机 checkout 派生的
  只读副本，新 generation 的 WI Agent 在第 0 步按同一规则重建，不依赖任何远端数据；② **随 quarantine 销毁**： 〔#111分类：删→驱动〕
  隔离/封存时目录整体废弃，不存在需要跨代保留或跨机迁移的价值；③ **`runtime-state/execution.json` 才是
  执行权威**：workflow 快照只是执行图的可读副本，执行进度的唯一权威恒在 `execution.json`
  （`03-execution-capsule.md#§4.2`），同步快照不增加任何正确性。`runtime-state/` 目录下只有 `execution.json` 一个
  文件需要同步（目录本身的只读权限位机制不改变其内容需被 Revision 记录这一事实，`03-execution-capsule.md#§6.2`）。
  同步范围（repo/artifacts/logs 三类目录）= 工作树中实际存在的文件集合：`git status
  --porcelain` 列出的全部路径（含 untracked 新文件）与 `git ls-files` 已提交文件的并集；
  唯一排除项是 `.git/` 目录本身（还原时以 `03-execution-capsule.md#§4.3` 声明的 base commit 重新 `git clone`/
  `checkout` 重建，`04-local-runtime.md#§7`）。gitignore 规则下的文件（如构建产物）需执行该 step 的 step agent
  （按其 step agent 定义文件指令，`03-execution-capsule.md#§4.5`）显式写入
  `artifacts/` 而非 `repos/` 才会被同步（`repos/` 内被 gitignore 的文件不入 Revision，因为
  `git status --porcelain` 默认不列 ignored 文件）。`session.json`（若纳入）不适用 gitignore
  过滤，因为它不是 `repos/<name>/` 下的 git 工作树。
- **session transcript 归档的同步确认（A14 裁定）**：`logs/` 已在同步根（上文），其子目录 `logs/session/` 随之天然在同步根内。session transcript 归档文件 `logs/session/<generation>.jsonl`（固定位置与命名规则的权威在 `03-execution-capsule.md#§4.4`：文件名与归档它的 generation 一一对应，**每次 end-generation（pause/block/wrap/fail）无例外**由 Daemon 从本机 `harness/<adapter>/session-data/` 复制写入——claude 适配器下，该源文件本身由 Daemon 在同一时点先从该 harness 的**默认会话位置**（本机、Capsule 外）只读采集进 `session-data/`，采集缺失为 `DAEMON-513` typed error（权威见 `03-execution-capsule.md#§4.4`、`04-local-runtime.md#§5.2`）——复制先于 final flush 的冻结扫描）**随该 generation 的 final flush（§6.1）即最后一次 Revision 一次性上传**；会话进行中该文件不存在，不存在逐边界重传的问题——裁定理由：CAS 按整文件内容寻址，transcript 在会话期间持续增长，若逐 Revision 边界同步，每次发布都要为整个文件重新上传一个块，成本随会话长度线性放大；按代一次性归档使每代 transcript 至多占用一个 CAS 块。归档文件只是审计材料，跨机重建不依赖它（§7.4）。
- 内容寻址存储（CAS，唯一权威存储布局，`02-aihub-core.md#§4.11` 本地数据目录布局与此逐字一致）：数据块按其
  内容 sha256 命名存放于 `<data_dir>/capsules/<wi>/rev/_cas/<sha256[:2]>/<sha256>`（AIHub 侧数据目录，按 WI id
  键控；与工作机的 capsule 目录名 `<project>-<seq>`（polyforge#144）无关）；
  manifest 落盘于 `<data_dir>/capsules/<wi>/rev/<seq>/manifest.json`（不存数据，只存操作
  清单 + 引用）；同块跨 Revision 去重。

### 4.2 manifest 文件格式（`rev/<seq>/manifest.json`） <!-- §4.2 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（owner generation、SYNC-* 码、Revision/CAS/manifest）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

磁盘上落盘的 `rev/<seq>/manifest.json` 即为下列 `manifest` 对象本身（合法 JSON，无注释）：

```json
{ "work_item_id": "wi_…", "seq": 7, "parent": 6, "generation": 3,
  "kind": "incremental",
  "ops": [
    { "op": "put",     "path": "repos/demo-ops/internal/x.go", "sha256": "…", "bytes": 1840, "mode": 420 },
    { "op": "delete",  "path": "artifacts/scratch.tmp" },
    { "op": "symlink", "path": "repos/demo-ops/vendor/self", "target": "." },
    { "op": "chmod",   "path": "scripts/run.sh", "mode": 493 },
    { "op": "put",     "path": "runtime-state/execution.json", "sha256": "…", "bytes": 412, "mode": 420 }
  ],
  "live_files": 118, "data_bytes": 241000,
  "execution_state_included": true,
  "built_at": "2026-09-26T08:00:00Z" }
```

字段说明：`kind` 为 `incremental` 或 `checkpoint`；`ops` 对 `incremental` 是相对 parent 的操作
集，对 `checkpoint` 是全量清单；`execution_state_included` 是 SYNC-INV-3 断言字段（必为
`true`，否则发布拒绝 `SYNC-400`；断言的是"完整的 `runtime-state/execution.json`已被纳入
`ops`"，不涉及 `03-execution-capsule.md#§4.2` 已明确裁定不存在的名为 `progress` 的子字段）；`built_at` 是本次
manifest 的生成时刻（RFC3339），仅供审计，**不参与 `manifest_sha`**（见下，修复 B10）。示例中把
`runtime-state/execution.json` 对应的 `put` 排在 `ops` 末尾，是对下面「扫描与固定顺序」
约束的直接体现，而不是任意排版选择。

**扫描与固定顺序（SYNC-INV-6：数据先于状态转换）**：Daemon 在窗口内（§2.7）计算 `ops` 时，必须
先固定同步根内除 `runtime-state/execution.json` 之外的全部数据与产物文件（`repos/`、
`artifacts/`、`logs/`、可选纳入的 `harness/<adapter>/session.json`）的字节与摘要，最后才读取并固定
`runtime-state/execution.json` 本身的内容计入 `ops`——即该文件对应的 `put` 操作在整个扫描/
固定过程中最后确定。理由：`execution.json` 的内容是「此刻 workflow 处于哪个状态」的转移记录
（`03-execution-capsule.md#§4.2`），只有在其余数据/产物字节已经确定之后再读它，才能保证该 Revision 里记录的状态
转移与它所描述的数据确实来自同一个逻辑时刻；把它放在固定顺序的最后，使其成为本次冻结窗口的
「收尾读」，与冻结窗口本身「确认无新写入后才开始扫描」的不变量（§2.7）共同构成两层保险。

- `put` 操作必带 `mode`（POSIX 权限位整数，如 `420`=0644、`493`=0755），`checkpoint` 的全量
  `put` 清单同样必带；`mode` 缺失 → 发布拒绝 `SYNC-400 MODE_MISSING`。
- `symlink` 用独立操作（`path` + `target`，无 `sha256`/`bytes`/`mode`）；恢复重建时按 §2.11
  的规则校验 `target` 解析后不越出同步根。
- `delete` 对常规文件与 symlink 同样适用：移除该路径。
- `kind: checkpoint`（每 64 个 Revision 或累计块 >512 MiB 生成）：`ops` 变为**全量清单**（所有
  存活文件的 put 清单），使重建只需 checkpoint + 其后增量。
- 文件格式为 UTF-8 JSON + zstd 压缩的块文件；manifest 本身不压缩（供人工排查）。

**自引用摘要的解决（`manifest_sha` 不写入 manifest.json 本身）**：`manifest_sha` 作为独立字段
随发布请求传输（`POST manifest` 的 body 是 `{manifest: <上述 JSON 对象>, manifest_sha:
"sha256:…"}`，见 §5.1），存储于 `02-aihub-core.md#§4.8` `capsule_revisions.manifest_sha` 列。计算规则
（修复 B10）：`manifest_sha` 等于
`"sha256:" + hex(sha256(canonical_json(manifest_idempotent_view)))`，其中
**manifest_idempotent_view** 是在序列化前剔除 `built_at`（及未来任何时间性/环境性字段，
新增此类字段时必须同步加入剔除清单）之后的 manifest 对象视图——摘要承载的是发布的**幂等
身份**，不得因生成时刻不同而对相同内容产生不同值（§2.8 场景二与 GET HEAD 消歧分支 1 都
依赖这一点）；**canonical_json**（规范化序列化）要求 UTF-8、对象键按代码点升序排列、无多余
空白（固定 2 空格缩进）、无尾随换行符。AIHub 收到 `POST manifest` 时按同一剔除规则重新计算
该值并与请求体中的 `manifest_sha` 比对，不符 → `SYNC-400 MANIFEST_HASH_MISMATCH`；此层
只有 Daemon 写 / AIHub 读一条代码路径，不需要跨语言实现一致性保证。

### 4.3 数据模型约束 <!-- §4.3 -->

- `(work_item_id, seq)` 唯一；`parent == HEAD.seq`（未命中幂等记录时校验，`SYNC-409`）。
- 块不可变：CAS 命中已存在文件时跳过写入（幂等）。
- `delete` 不物理删除块：块的生命周期由删除保护（§2.10）管理。

## 5. API 与错误码（本层） <!-- §5 -->

### 5.1 发布协议（Daemon → AIHub，权威签名） <!-- §5.1 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（持久 WI Agent、owner generation、SYNC-* 码、Revision/CAS/manifest）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

**调用方与端点权威**：本节端点的方法/路径/调用方**一律以 `02-aihub-core.md#§5.1` canonical 行 14–20 为权威**（块上传/manifest/HEAD 读取为 Daemon；删除保护与撤销为人（admin）经 `pf` 部署 CLI；恢复读取为恢复方机器），本节不重复定义清单，只展开同步协议细节（分块、manifest、幂等、fencing）。发布动作的机械执行者恒为 Daemon（`00` INV-11/`04` INV-D3），与端点的授权调用方按 canonical 各行定义不冲突。WI Agent 向 〔#111分类：删→驱动〕
本机 Daemon 发起发布请求的信道不属于本层 API，权威定义在 `04-local-runtime.md#§5.1`/§6.3/§6.4，
本文不重复定义。

| 端点 | 方法 | 语义 |
|---|---|---|
| `/api/v1/work-items/{id}/revisions/{seq}/blocks/{sha256}` | PUT | 上传单个 CAS 块。header 必带 `X-PF-Generation-Token`；body 为块内容；已存在且摘要同 → 200 空转（幂等）。→ 200 \| 403 SYNC-403 \| 507 CORE-507 |
| `/api/v1/work-items/{id}/revisions/{seq}/manifest` | POST | 提交 manifest。header 必带 `X-PF-Generation-Token`；body `{manifest, manifest_sha}`（`manifest_sha` 在 body 外层传输，不内嵌，§4.2）。校验顺序：§2.9 fencing → §2.8 幂等检查 → 未命中时的 §2.6 HEAD 引导校验 → `manifest_sha` 重算比对 → SYNC-INV-3 → 块完整性。→ 200 `{head_seq: seq}` \| 400 SYNC-400 \| 403 SYNC-403 \| 409 SYNC-409 \| 507 CORE-507 |
| `/api/v1/work-items/{id}/head` | GET | 读取当前 HEAD → `{seq, generation, manifest_sha, updated_at}` \| 404（无行，bootstrap 前） |
| `/api/v1/work-items/{id}/revisions/{seq}/blocks/{sha256}` | GET | 恢复拉取单个块 |
| `/api/v1/work-items/{id}/revisions?since=<seq>` | GET | manifest 清单（恢复用） |
| `/api/v1/work-items/{id}/capsule-data?confirm=<wi_id>` | DELETE | §2.10 删除保护入口：写 `capsule_tombstones`（持久化）。本接口签名与错误码权威在本文（`07` 仅交叉引用部署 CLI 触发面，不重定义）。→ 200 \| 409 SYNC-409（非终态）\| 400 SYNC-400（confirm 不符） |
| `/api/v1/work-items/{id}/capsule-data/revert-tombstone` | POST | 24h 窗口内撤销，仅 admin。→ 200 \| 404 SYNC-410（无 pending tombstone） |

**块上传阶段的 fencing 必要性**：若仅 manifest 阶段校验 generation，旧 owner 可在被接管后仍先
上传任意多个孤儿块（不受 `SYNC-403` 拦截，只是最终不被 manifest 引用），造成无界 CAS 占用。
块 PUT 带 generation 校验后，旧 owner 在第一个块就被拒，不会产生孤儿块。

（挂载点声明见 `02-aihub-core.md#§5.1` 的 revisions/manifest 行；两处引用签名逐字一致。）

### 5.2 错误码表（前缀 SYNC） <!-- §5.2 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（持久 WI Agent、owner generation、SYNC-* 码、Revision/CAS/manifest）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

| 码 | 语义 |
|---|---|
| SYNC-400 | manifest 非法（结构 / MANIFEST_HASH_MISMATCH / MODE_MISSING / SYNC-INV-3 缺失 / 块缺失 / 路径逃逸）；或 `DELETE .../capsule-data` 的 confirm 与 wi_id 不符 |
| SYNC-403 | STALE_OWNER：发布者 token 校验失败（body `{retry_hint: "reclaim_required", reason: stale_generation|secret_rotated_expired|secret_rolled_back}`，reason 逐字透传 CORE-403 细分并按其分流处置，见 §2.9；不含裸 generation 整数） |
| SYNC-404 | Revision / 块不存在 |
| SYNC-409 | SEQ_CONFLICT：未命中幂等记录且 parent ≠ HEAD，或同 seq 不同 manifest_sha；亦覆盖 §2.6 bootstrap 规则违反（无 HEAD 行时 seq≠0 或 parent≠null）；或 `DELETE .../capsule-data` 对非终态 WI 的状态门拒绝 |
| SYNC-410 | 目标 Capsule 数据已 tombstone / 硬删除；或 revert-tombstone 时无 pending tombstone 可撤销 |

冻结窗口的确认超时（§2.7）不产生 AIHub 侧的 SYNC-* 响应码——它是 Daemon 与 WI Agent 之间的本机 〔#111分类：删→驱动〕
协调结果，在 Daemon 发起任何向 AIHub 的网络调用之前就已经决出（继续等待 / 放弃本次 milestone /
升级为基础设施失败 / 按进程终止判定直接成立），不经过本层 API。

## 6. 核心流程：全扫描发布的六个步骤 <!-- §6 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（持久 WI Agent、SYNC-* 码、Revision/CAS/manifest、watcher dirty 位）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

以下按执行顺序描述唯一发布路径的六个阶段（编号步骤，非可执行代码；细节接口签名见 §5，数据格式
见 §4）：

1. **触发**：`00-architecture.md#§4.3` 四类事件之一发生（step 完成/中断、WI 出最终结果、Daemon 优雅停机、可选
   milestone），请求发起方按 §2.4 表向 Daemon 发出一次发布请求。watcher 此前写下的 dirty 标记
   （若有）只作为"值得扫一次"的提示被 Daemon 读取，不改变本流程的必经性——即使 dirty 位缺
   失，触发事件本身也足以发起本流程。
2. **冻结（write freeze，§2.7）**：Daemon 获取该 WI 的 publish mutex 后，协调 WI Agent 〔#111分类：删→驱动〕
   令其自身与当前派生的 step agent 停止产生新的写入，等待"当前无进行中写入、
   也不会开始新写入"的确认；若该 Job 的 WI Agent 进程及其全部命令子进程与 step agent 进程组 〔#111分类：删→驱动〕
   均已终止，且代替它的新 WI Agent（若已派生）尚未开始任何写入，则直接以此作为 write freeze 〔#111分类：删→驱动〕
   已获取，不等待确认；任一条不满足则改走正常确认路径。确认（或上述判定）到达即视为
   write freeze 已获取。超时按 §2.7 的分类处理。
3. **扫描**：Daemon 在 write freeze 窗口内一次性枚举同步根（§4.1）全部文件，按 SYNC-INV-6 的
   固定顺序（数据/产物先于 `runtime-state/execution.json`）计算每个文件的 sha256/mode，与
   本地已知的 parent（上一次成功发布的 manifest）比较得到操作集合（`put`/`delete`/`symlink`/
   `chmod`）。
4. **打包、staging 固化与释放 write freeze**：Daemon 构建 manifest 对象（§4.2），断言其包含当前
   `runtime-state/execution.json` 的操作（SYNC-INV-3）；计算幂等视图的 `canonical_json` 与
   `manifest_sha`（不含 `built_at`，§4.2）；把 manifest 引用到的全部数据字节**独立复制**
   （禁止硬链接，同盘 reflink/CoW 优先，退化全量复制）进 Daemon 管理的不可变 staging
   （§2.7，SYNC-INV-7）。到此为止本次 write freeze 窗口内的字节已既固定为一份完整的
   manifest、又固化进了 staging，Daemon 立即释放 write freeze（WI Agent 恢复写入）， 〔#111分类：删→驱动〕
   但**继续持有**该 WI 的 publish mutex。释放之后，Daemon 先原子持久化 pending publish
   记录（§2.8，SYNC-INV-8），再把 staging 中尚未在 CAS 中存在的数据块通过 §5.1 的块 PUT
   接口上传（携带 `X-PF-Generation-Token`，接受 §2.9 fencing 校验，已存在块跳过；**只读
   取 staging，不重新读取活动的同步根目录**）；全部块 fsync 落盘后再进入下一步（先数据后
   状态，§2.5）。
5. **幂等检查**：Daemon 向 `POST manifest` 提交请求；AIHub 侧首先按 `(work_item_id, seq,
   manifest_sha)` 查询是否已有匹配的历史 `capsule_revisions` 行（§2.8）。命中 → 直接返回成功
   （跳到步骤 6 的"已完成"结果，不再执行下面的 HEAD 校验）。
6. **HEAD 校验与发布**：未命中幂等记录时，校验 `manifest.parent == 当前 capsule_heads.seq`
   （或 §2.6 的 bootstrap 分支）；不等 → `SYNC-409`，本次发布未发生，HEAD 不动，留待下一次
   触发事件重新走步骤 1–6。校验通过 → 在同一写事务内 `INSERT capsule_revisions`（manifest_sha
   入库）+ `INSERT`（bootstrap）或 `UPDATE`（后续）`capsule_heads`，提交后返回
   `{head_seq: seq}`。任一步骤失败，HEAD 保持不动，本次发布视为未发生，可安全重试（幂等）；无论成功还是失败，
   本步骤的结果确定后（收到服务端的明确响应，无论成功码还是 SYNC-4xx 错误码）Daemon 才删除
   pending publish 记录（§2.8；仅当响应本身丢失/超时，记录才保留待 GET HEAD 消歧）、才释放
   该 WI 的 publish mutex（§2.7），随后才处理该 WI 排队
   中的下一次发布请求（若有）。

### 6.1 generation 收尾的 final flush 与 flush 证据生成（D6；Core 校验权威在 `02-aihub-core.md#§6.4`） <!-- §6.1 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（持久 WI Agent、owner generation、Revision/CAS/manifest、flush 证据、watcher dirty 位）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

`02-aihub-core.md#§6.4` 规定 pause/block/wrap/fail 四个结束 generation 的端点必须携带 `flush_evidence`，且
不允许调用方自报了事——证据的全部事实性字段都必须来自 **Daemon 侧**在写冻结后的一次真实
扫描（D6 裁定：final flush 证据由 Daemon 生成，WI Agent 只负责发起收尾本身）。本节定义这份 〔#111分类：删→驱动〕
证据的生成协议；字段名与服务端校验规则以 `02-aihub-core.md#§6.4` 为权威，本文不重复校验侧，只定义生产侧。
证据的核心元组是「**链当前停在哪个版本**（`published_seq`，即收尾时刻的 checkpoint 版本）与
**最终 manifest 幂等视图摘要**」，加上**绑定冻结后最终本地检查点**的 `frozen_checkpoint`
（`{scanned_at, bytes_digest, worktree_state, manifest_sha, matches_head}`：`bytes_digest` 是冻结扫描覆盖的全部同步面字节的聚合摘要——对排序后 `(path, sha256, mode)` 列表按 RFC 8785 canonical JSON 序列化后取 sha256；`worktree_state` 是各 inventory repo 的 `{name, base, head, dirty_count}` 列表同法序列化的 sha256。字段语义与校验权威在 `02-aihub-core.md#§6.4`，本节只定义生产侧）。

**生成序列**（Daemon 执行，复用本节六个步骤的全部协议，不另设第二条发布路径）：

1. WI Agent 到达收尾决策（按 `01-workflow-skills.md#§6.1` 契约，收尾前自身与 step agent 已停止写入），向 〔#111分类：删→驱动〕
   Daemon 发起一次发布请求（reason 对应收尾类型）；Daemon 按步骤 2–3 获取 write freeze 并
   完成一次 **final flush 冻结扫描**——无论本 generation 是否产生过改动，这次扫描都必须
   实际执行（`02-aihub-core.md#§6.4` 明确不允许仅凭旧 HEAD 推断）。**每次 end-generation（pause/block/wrap/fail）**，Daemon 在发起
   本次 final flush 的冻结扫描**之前**（claude 适配器下须先完成 `03-execution-capsule.md#§4.4` 的终态采集：从该
   harness 默认会话位置只读复制本代 JSONL 进 `harness/<adapter>/session-data/`；本代已派生 harness 会话而默认位置
   缺失该 JSONL 时以 `DAEMON-513`（`04-local-runtime.md#§5.2`）失败本次收尾，不静默跳过），若本机
   `harness/<adapter>/session-data/` 内存在**非空**的会话
   transcript 文件，则把它复制为同步根内的 `logs/session/<generation>.jsonl`（固定位置与命名规则
   权威在 `03-execution-capsule.md#§4.4`，A14 裁定；归档前提=本代曾实际产出会话内容：从未派生
   harness 会话、或 transcript 为空文件的 generation 不产生归档路径——「首次 claim 后未及派生即
   收尾」因此与第 3 步的空链分支不冲突：无会话即无归档文件，同步面保持为空，
   `EMPTY_SYNC_BYTES_DIGEST` 判定成立），使该归档文件随本次冻结扫描进入最后一次 Revision，**一次性
   发布**（不存在会话进行中的逐边界重传：CAS 按整文件内容寻址，增长的 transcript 若逐 Revision
   同步，每次都要为整个文件重新上传一个块，成本随会话长度线性放大）。
2. **`matches_head` 一律相对「收尾调用提交时刻的当前 HEAD」判定**（本步为该语义的权威）：
   - **无改动**（本次冻结扫描的 `bytes_digest`/`worktree_state` 与上一次已发布状态的冻结字节摘要一致——
     Daemon 持有上次发布时记录的同口径摘要，跨机恢复时由 HEAD 物化后重算）→ 无需再发布：证据为
     `frozen_checkpoint = {scanned_at, bytes_digest, worktree_state, manifest_sha: <HEAD 行摘要>, matches_head: true}`、
     `published_seq` = HEAD `seq`、`manifest_sha` = HEAD 行摘要。「内容未变」的判定输入是 `bytes_digest`
     （稳定字节摘要）；`frozen_checkpoint.manifest_sha` **恒取随附外层 `manifest_sha`（= 当前 HEAD 行摘要）**——
     它是「对齐声明」（满足 `02-aihub-core.md#§6.4` 规则 6 的相等校验），不是新算的候选摘要：
     新扫描因含 seq/parent/generation 必然与 HEAD 行摘要不同，二者不承担比较职责（比较由 bytes_digest 承担）。
     跨代无改动收尾（HEAD 属于更早 generation）同样按此规则，校验恒可执行。
   - **有改动** → 必须先把步骤 4–6 走完（staging 固化、pending 记录、块上传、manifest 提交、
     HEAD 推进——发布产出的正是本次冻结扫描的固化字节），**发布完成后** HEAD 即为该新 Revision，
     提交时证据为：`published_seq` = 新 `seq`、`manifest_sha` = 新 Revision 的幂等视图摘要、
     `frozen_checkpoint = {scanned_at: <本次扫描完成时刻>, bytes_digest, worktree_state,
     manifest_sha: <扫描摘要（= 新 Revision 摘要，同一扫描的产物）>, matches_head: true}`。
     `matches_head=false` 不是合法提交形态——它仅出现在「扫描发现改动但未完成发布就提交」的失败路径，
     `02-aihub-core.md#§6.4` 规则 6 将其拒为 `UNPUBLISHED_CHANGES`。`scanned_at` 早于发布提交，
     `02-aihub-core.md#§6.4` 的新鲜窗（缺省 60s）以它起算，收尾调用必须紧随其后发出。
3. **无 HEAD 的空链情形（D6 裁定）**：首次 claim 后立即 pause/block（本 generation 从未发布过、
   `capsule_heads` 无行、§2.6 bootstrap 从未发生）时，`02-aihub-core.md#§6.4` 的 `published_seq ==
   head.seq` 校验没有可比对象。裁定为：**仅当本 generation 自 claim 起从未产生过任何同步面
   写入**（Daemon 依据 watcher dirty 位、pending publish 记录与本地发布历史判定）时，允许且
   仅允许证据形态 **`published_seq = 0` + `frozen_checkpoint.bytes_digest ==
   EMPTY_SYNC_BYTES_DIGEST`**（空同步面聚合摘要常量 `sha256("[]") =
   4f53cda18c2baa0c0354bb5f9a3ecbe5ed12ab4d8e11ba873c2f11161202b945`；`manifest_sha` 为空、
   `matches_head` 忽略）——此处的 `0` 是
   "链上不存在任何 Revision"的哨兵，与 §2.1 首个 Revision 的 `seq=0` 哨兵含义不同，不得混淆；
   `frozen_checkpoint` 照常携带（`bytes_digest` 为空集常量，比对基准是"无 HEAD 行且本
   generation 从未发布"）。Core（`02-aihub-core.md#§6.4`）对无 HEAD 行的 WI 接受且仅接受这一种形态。若本 generation
   已产生过任何同步面写入（哪怕尚未触发过任何发布），该捷径不适用：Daemon 必须先完成一次
   bootstrap 发布（§2.6 分支，产生真正的 `seq=0` Revision）再按第 2 步的正常比对生成证据
   ——不允许以空链形态把未发布的本地改动静默丢弃。
4. 证据生成后由 WI Agent 随收尾调用提交给 Core；`03-execution-capsule.md#§2.3` 的"先发布、后结束 generation" 〔#111分类：删→驱动〕
   顺序由本序列天然满足（第 2 步不一致分支内的发布完成先于收尾调用）。

**与 §2.8 pending 记录的关系**：final flush 若走了第 2 步的发布分支，同样按 §2.8 持久化
pending publish 记录并在确认落定后删除；收尾调用（携带证据）必须在 pending 记录已消歧/删除
之后发出——存在未消歧的 pending 记录时，`flush_evidence` 里的 `published_seq`/`manifest_sha`
不可信（与 `04-local-runtime.md#§6.5` 第 3 步同步边界的要求同源）。

## 7. 并发与崩溃恢复（三场景） <!-- §7 -->

### 7.1 本机崩溃（WI Agent / 整机） <!-- §7.1 --> 〔#111分类：删→驱动〕

> **ARCHIVED — 本节描述或依赖已删除的机制（持久 WI Agent、Revision/CAS/manifest）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

WI Agent 或其所在机器崩溃重启后，Daemon 按 `04-local-runtime.md#§7` 检测到进程退出并基于 〔#111分类：删→驱动〕
`runtime-state/execution.json` 当前内容重新派生新的 WI Agent 续跑（不要求从零重放整个 〔#111分类：删→驱动〕
workflow）。新 WI Agent 恢复驱动后，会在到达下一次 §2.4 的四类触发事件之一时向 Daemon 发出 〔#111分类：删→驱动〕
发布请求，Daemon 自然发起一次全扫描发布（§6 步骤 1–6）：崩溃前已发布的 Revision（若有）保持
不变；崩溃前尚未来得及发布的本地改动仍留在工作树中，会被 Daemon 下一次扫描纳入新的 manifest
一并发布，不会丢失。若在这个重新发起的冻结中 Daemon 发现旧 WI Agent 进程、及其全部命令子进程 〔#111分类：删→驱动〕
与 step agent 进程组均已终止，且新 WI Agent 尚未开始任何写入（即在新 WI Agent 首次写入之前 〔#111分类：删→驱动〕
就已确认死亡），按 §2.7 的判定直接开始扫描，无需等待确认；一旦新 WI Agent 已经写过任何同步 〔#111分类：删→驱动〕
根内容，该判定不再适用，须改走对新 WI Agent 的正常确认路径。若 Daemon 重新生成的 manifest 〔#111分类：删→驱动〕
与崩溃前某次已经成功提交但响应未确认的发布内容完全一致，步骤 5 的幂等检查会命中并直接返回
成功（§2.8 场景二）。

### 7.2 同步中断（数据块仅部分落盘时崩溃） <!-- §7.2 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（Revision/CAS/manifest）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

若在 §6 步骤 4（打包/staging 固化/块上传）进行中崩溃，分两种子情形：**staging 固化尚未完成时
崩溃**（write freeze 已获取但字节尚未全部复制进 staging）：本次发布流程视为未发生，Daemon
重启后（或由下一次触发事件）重新走§6 步骤 1–6，此前部分复制的 staging 残留（若有）不被任何
manifest 引用，按 staging 的清理策略回收，不影响正确性，也不留下悬挂的 publish mutex
（Daemon 进程本身终止时该锁随进程一并失效）。**staging 已固化、块上传/提交阶段崩溃**：块上传开始前已持久化的 pending publish 记录
（§2.8，修复 B10）在重启后原样存在，AIHub 侧已落盘的块保持不变（块内容寻址、天然幂等）。
Daemon 重启后先按 §2.8 的 GET HEAD 三分支消歧：HEAD==记录 `seq` 且 `manifest_sha` 相同 →
视为成功，删除记录；HEAD==记录 `parent`（或 bootstrap 发布的无 HEAD 行）→ 从记录引用的
本地 staging 安全重发（相同 sha256 命中即跳过，缺失则补传）；其它 → 走全量对账分支，
丢弃过时的 `(seq, parent)` 声明，等下一次触发事件重新全扫描发布。若崩溃发生在步骤 5/6
之间（manifest 已提交但客户端未确认响应），消歧落在"视为成功"分支；即使误判重发，也会
在步骤 5 的幂等检查中命中历史记录直接返回成功（§2.8 场景一），二者互为双保险。不变式：HEAD 从未指向不完整的 Revision——发布校验失败即整体未
发生，绝不存在"HEAD 已推进但对应块缺失"的中间态。

### 7.3 takeover 竞态（旧 owner 与新 owner 同时活动） <!-- §7.3 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（持久 WI Agent、owner generation、SYNC-* 码、Revision/CAS/manifest、takeover）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

时序：旧 WI Agent 仍在运行 → 人工（admin）经恢复机器发起 force-takeover（该调用本身结束旧代并 〔#111分类：删→驱动〕
开启 generation+1、在响应中返回新代凭证）→ 恢复机器的 Daemon **消费该响应凭证**创建新现场并派生
新 WI Agent（无第二次 claim——再 claim 会被 `02-aihub-core.md#§6.1` 以 ALREADY_CLAIMED 拒绝； 〔#111分类：删→驱动〕
`04-local-runtime.md#§7.3` 恢复序权威）。旧 Daemon 在接管后代旧 WI Agent 发起的任何后续发布（即使旧 WI Agent 〔#111分类：删→驱动〕
仍在运行并持续向旧 Daemon 发出发布请求），无论处于步骤 4（块上传）还是步骤 6（manifest
提交），都会在 §2.9 fencing 校验处被拒（`SYNC-403 STALE_OWNER`），旧 Daemon 据此立即停止为
该 WI 发布并通知旧 WI Agent 停止驱动、封存本地现场（不再尝试发布，`03-execution-capsule.md#§2.4`）。新 Main 〔#111分类：删→驱动〕
Agent 通过 §7.4（若适用）从远端 HEAD 恢复现场后，以新 generation 正常发布。窗口内旧 Daemon
可能已经代旧 WI Agent 上传过的块因未被任何 manifest 引用而成为无害残留（不被 HEAD 引用）， 〔#111分类：删→驱动〕
由删除保护（§2.10）与 checkpoint GC 正常回收，不影响正确性。

### 7.4 远端现场恢复（takeover / Capsule 重建时调用） <!-- §7.4 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（持久 WI Agent、owner generation、Revision/CAS/manifest、read-through）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

当 `03-execution-capsule.md#§6.1` 的 `create_capsule` 因 generation 变化（隔离重建或全新 Capsule）需要从远端恢复
现场时：先读取当前 HEAD（若 404，说明尚无任何发布，`03` 创建的空目录原样保留，等待第一次走
bootstrap 分支）；再按 `02-aihub-core.md#§4.6`/§5.1 的 Core 权威 repo inventory 逐仓检出基线（含 `.git`）；
从最近一个不晚于 HEAD 的 checkpoint（若存在）加上其后全部增量 Revision 的 `ops`，重建出 HEAD
对应的预期文件集合（无 checkpoint 时从 `seq=0` 完整重放）；用该预期集合**完整替换**同步根
（先删后写，避免 checkpoint 之前已删除的文件被错误复活），过程中每个 `symlink`/`put` 操作都
必须经过 §2.11 引用的 `03-execution-capsule.md#§6` 路径校验；替换后逐文件校验 sha256 与预期一致。**`harness/<adapter>/`
整体不参与本次替换**：即使某个历史 Revision 里包含过 `harness/<adapter>/session.json` 的
`put` 操作（§4.1 的可选同步项），本次重建也不据此写回该文件——`harness/<adapter>/` 由 `03-execution-capsule.md#§6.1` 第 5 步为新 generation 创建全新的空目录，随后由新派生的 WI Agent 首次启动 harness 时 〔#111分类：删→驱动〕
重新写入全新的 `session.json`/`session-data/`；Revision 中的历史 `session.json` 只是审计用
的历史元数据，不构成任何身份恢复依据（`03-execution-capsule.md#§4.4`「新代总是从干净目录开始」的裁定同样约
束本恢复路径）。WI Agent 第 0 步物化的 `.pi/agents/`、`.claude/agents/` 与 `workflow.md`（或 `workflow/`）与 `harness/` 同一「永远从零重建」模式：不在同步根（§4.1，SYNC-INV-10），不参与本次替换，由新 WI Agent 在第 0 步从本机 aihub checkout 重新物化（`03-execution-capsule.md#§4.5`）。已归档的 〔#111分类：删→驱动〕
  session transcript（`logs/session/<generation>.jsonl`，`03-execution-capsule.md#§4.4` A14 裁定）若存在于历史
  Revision 中，作为 `logs/` 同步根内的一等文件照常写回，但**跨机重建不依赖已归档 transcript**：它
  只是审计材料，不参与任何执行现场或 harness 身份的重建——重建后的新 generation 产生自己的
  transcript 并在自身终态归档为不同的文件名（与 generation 一一对应），历史归档与新的执行现场
  天然隔离。标记 Capsule 为已恢复并按 `01-workflow-skills.md#§6.3` 让 in_progress 的 step 原地续跑。恢复
前后 HEAD 未变，因此 read-through（`02-aihub-core.md#§5.2`）观测到的进度在恢复前后一致，观测面无感。

**与本机崩溃的区分**：远端现场恢复只在 generation 发生变化（隔离重建或全新 Capsule）时调用，
总是完整替换，不保留本地内容。§7.1 的本机崩溃（WI Agent 重启但 generation 未变）不走这条 〔#111分类：删→驱动〕
路径，而是等待下一次触发事件走 §6 的正常发布流程；本地未发布的改动被保留，不被完整替换覆盖。

### 7.5 任务仓库 carrier ref（polyforge#108 / #158） <!-- §7.5 -->

> **已迁出 — 本节是历史副本；现行权威是 [v2-converge.md](../v2-converge.md) §14.2（逐字迁入，2026-10-16），两者不一致时以那里为准。**

本节描述当前代码（v2 Go 执行驱动），与上文 Revision 模型并列。每次 capsule 发布，daemon 先推 capsule 链（`refs/heads/main`），
再把每个任务仓库 `repos/<name>` 的本地 HEAD 推到该 WI 裸仓库的 carrier ref `refs/pf-task/<name>`（`gitops.pushTaskRefsLocked`）；
blocked→resume 等重新认领从上游 clone 后按 carrier ref 恢复任务仓库（`runtime.restoreTaskRepoState`）。carrier 推送失败不回滚、
不阻塞 capsule 链发布（D8）。

- **blob 上限分两档**：aihub pre-receive hook 对 capsule 链沿用 16 MiB（`capsule.MaxBlobBytes`，env `GIT_PF_MAX_BLOB`），
  对 `refs/pf-task/*` 用 100 MiB（`capsule.MaxTaskBlobBytes`，env `GIT_PF_MAX_TASK_BLOB`）。原因：任务仓库在 aihub 上最初为空，
  首次 carrier 推送带上整个上游历史，上游历史里的大资源（例：skypilot 的 21 MB `streamlit_app.mp4`）不能让该仓库永远无法携带。
  超过 100 MiB 的上游 blob 仍会被拒（"carrier 只存 WI 自己的提交"的根治方案未实施）。
- **失败即决策**：carrier 推送因任何原因失败或被拒（超限、non-fast-forward 分叉、传输、下述仓库配置拒绝），除 stderr 日志外，
  **仅当本次 capsule 链推送成功时** daemon 写 `carrier_push_failed` 事件（body：`repo`、`ref`、截断到约 2 KB 的 git 错误或拒绝原因）；
  链推送也失败（aihub 不可达、会话被 fence）时不提醒，仍失败的 carrier 在下一次链推送成功的发布时提醒。watcher 把它归为人工决策，
  提醒行为固定模板：
  `[polyforge] <slug> carrier_push_failed <repo> — this task repo cannot be saved; a resume may lose its commits; this is a human decision, the agent must not answer it`。
  仓库名经与 WI id 相同的字符集过滤；错误文本只在 `pf watch <wi> --once` 的事件里。每个 job 每个仓库只提醒一次，直到该仓库的 carrier
  推送再次成功（状态挂在 runtime job 上的内存里：job 结束即消失，重新认领 = 新 job 会再提醒；daemon 重启后同样重新提醒一次）。
  non-fast-forward 失败另外照旧发 `task_ref_diverged`。
- **凭证安全：带凭证的推送从不在任务仓库里执行**（任务仓库由不可信 agent 控制；git 没有忽略仓库本地配置的开关，
  agent 也可能在检查与推送之间改写配置，所以不做配置黑名单）。每个任务仓库：
  1. 只读校验（不带凭证）：仓库必须完全自有其存储——`repos/<name>/.git` 是真实目录（Lstat：非 symlink、非 gitfile）；
     不存在 `.git/commondir`；`.git/HEAD`、`.git/packed-refs`、`.git/refs` 及其下任何条目都不是 symlink；`.git/objects` 是真实目录，
     其每个条目（`pack`、`info`、`??` 扇出目录）与 `objects/pack/` 的每个条目都不是 symlink；不存在 `objects/info/alternates`
     与 `objects/info/http-alternates`。在仓库内执行 `git rev-parse --absolute-git-dir` 必须恰好等于该 `.git`；再读一次 HEAD：
     `git rev-parse --verify -q HEAD^{commit}`（unborn = 无可携带，静默跳过）。仓库内的这两次读取另带 `-c safe.directory=<repo>`
     （#135 后仓库属于另一个 uid 时仍可读）。任一校验失败即拒绝并发 `carrier_push_failed`（原因写在事件里），不准备凭证。
  2. 在任务仓库之外（WI 裸仓库目录旁，`pf-carrier-*` 临时目录）建一个 daemon 自有的临时裸仓库，`objects/info/alternates`
     指向 `<repo>/.git/objects`，`update-ref refs/pf-carrier/tmp <sha>`；推送完即删除。
  3. 从临时仓库推送：
     `git --git-dir=<tmp> -c core.hooksPath=/dev/null -c core.fsmonitor=false -c credential.helper= -c http.proxy= -c protocol.allow=never -c protocol.<RemoteURL 的 scheme>.allow=always push --no-recurse-submodules [--push-option=session-fingerprint=<fp>] <RemoteURL> <sha>:refs/pf-task/<name>`。
  carrier 通道上每一次 git 调用（校验与推送）用同一个环境：daemon 环境去掉全部 `GIT_*`，加 `GIT_CONFIG_NOSYSTEM=1`、
  `GIT_CONFIG_GLOBAL=/dev/null`、`GIT_CEILING_DIRECTORIES=<解析 symlink 后的 repos 目录>`（校验与推送用同一个值）；推送另加一次性 askpass（`GIT_ASKPASS`、`GIT_TERMINAL_PROMPT=0`）。
  于是任务仓库里的 `credential.helper`、`url.*.insteadOf`/`pushInsteadOf`、`remote.<url>.vcs`（remote helper）、
  `push.recurseSubmodules`/`submodule.recurse` 与子模块自己的 origin、hooks 等都不参与推送。git 从 alternates 只读取对象数据，
  不执行其中任何东西。carrier 通道的每次 git 调用都有超时（校验与临时仓库准备 1 分钟，推送 10 分钟，超时杀掉整个 git 进程组），
  挂住的 hub 不会永久占住该 WI 的锁；daemon 启动时清理残留的 `pf-carrier-*` 临时仓库（与 askpass 清理相同）。
  **残余（#135 之前）**：上述无竞态的借用方式（commondir、symlink 的 refs/objects、alternates）一律拒绝；但对象读取与打包
  仍以 daemon 的 uid 读 agent 可写的目录，agent 可以利用校验到推送之间的窗口——例如校验之后再写入嵌套的
  `objects/info/alternates`，或把 `.git` 换成指向别处的 symlink——把**另一个 WI 的内容**拉进本 WI 的 carrier ref
  （不只是推送失败或推送垃圾）。这类基于竞态的残余要等读取/打包改在 agent 自己的 uid 下执行（#135）才能消除；
  在那之前 agent 与 daemon 同 uid，本可直接读写同机其他目录，这一层是纵深防御，而不是隔离边界。

### 7.6 任务仓库未提交改动：WIP ref（polyforge#167） <!-- §7.6 -->

> **已迁出 — 本节是历史副本；现行权威是 [v2-converge.md](../v2-converge.md) §14.3（逐字迁入，2026-10-16），两者不一致时以那里为准。**

carrier ref 只记录 HEAD，重新认领会丢掉任务仓库里未提交的改动。补充一条 WIP ref `refs/pf-wip/<name>`（`gitops.PushTaskWIP`）：

- **何时写**：只在步骤进程已退出的关闭点——bounded-fail（blocked，SessionEnd 之前）、`stopOp` 收尾（failed / blocked 等非 wrapped）、
  operator pause（`pauseOp`）、daemon 正常关停（杀掉 agent 树之后）。observe 的常规发布不写。
  先按 §7.5 镜像每个仓库的 HEAD 到 `refs/pf-task/<name>`，再对每个仓库：有未提交改动（已跟踪修改、删除与未跟踪文件）→ 生成一个
  WIP 提交（唯一父提交 = HEAD）并强推到 `refs/pf-wip/<name>`；干净或 wrapped 关闭 → 直接推 `:refs/pf-wip/<name>` 删除
  （hub 上没有该 ref 时静默）。本次 WIP 生成/推送失败时**保留** hub 上较旧的 WIP ref：它只在父提交等于恢复后的 HEAD（即它被拍下时
  的状态）时才会被应用，应用它总比什么都没有好；失败只记日志并发 `carrier_push_failed`（`ref` = WIP ref，`error` 注明 hub 上的
  WIP 早于本次关闭；同样仅在链推送成功时、每 job 每 ref 一次），从不让关闭失败。
  unborn HEAD 跳过。
- **生成方式**：在 §7.5 的 `pf-carrier-*` 临时裸仓库里完成（alternates 借任务仓库对象），任务仓库只作 `--work-tree`，
  index 用临时文件（`GIT_INDEX_FILE=<tmp>/pf-wip-index`）：`read-tree <HEAD>`、预扫描（见下）、`add -A --ignore-errors`、剔除、
  `write-tree`（树与 HEAD 相同 =
  干净）、`commit-tree <tree> -p <HEAD>`（作者/提交者 `polyforge-daemon`），再用 §7.5 同一条推送命令推 `+<wip>:refs/pf-wip/<name>`。
  同样的 env 与 `-c` 标志、超时（`add -A` 用推送的 10 分钟）。不写任务仓库的 HEAD、分支、index、对象库，也不把它的配置当配置读——
  git 读的是临时仓库的配置，所以仓库里定义的 clean filter、fsmonitor、hook 都不会运行。不用 `git stash create`：它在 agent
  仓库里执行（读其配置、写其对象库），且不收未跟踪文件。
- **快照是工作树原始字节**：不应用任何 clean/smudge filter 与属性驱动的转换（eol、`working-tree-encoding` 等）。因此属性声明
  `filter=lfs` 或 `filter=git-crypt` 的已改动文件不进快照（其原始内容是 smudge 后的对象/明文）。
- **忽略与属性来源**：工作树里的 `.gitignore` / `.gitattributes`（数据）照常生效；carrier env 丢掉的来源复制进临时仓库的
  `info/exclude` / `info/attributes`：生效的 `core.excludesFile` / `core.attributesFile`（任务仓库自己配置里的值——只按文件读该键、
  不跟 include；否则运维者的全局配置（含 `GIT_CONFIG_GLOBAL`）；否则 `$XDG_CONFIG_HOME/git/ignore|attributes`），再加仓库的
  `.git/info/exclude` / `.git/info/attributes`。只读普通文件（非阻塞打开），每个上限 1 MiB。
- **剔除并记录**（WIP 提交说明列出路径，并发 `task_wip_skipped` 事件：`repo`、`ref`、`skipped`），单个坏路径不再让整个快照丢失：
  - 预扫描（`add` 之前，不读文件内容）：`ls-files -o --exclude-standard`（临时 exclude 生效，不进入被忽略目录）列出未跟踪路径——
    嵌套 git 仓库显示为一个 `dir/` 条目（HEAD 记为子模块的不在其中；没有提交的嵌套仓库原本会让 `add -A` 整体失败）、超过 carrier
    blob 上限（`capsule.MaxTaskBlobBytes`）的未跟踪文件、未跟踪总量超过同一上限时的最大文件，都写进 `info/exclude`；工作区大小超过
    blob 上限的已跟踪文件设 `skip-worktree`，保持 HEAD 条目（大小与 HEAD 的 blob 不同时记录）。这些文件从不被哈希。
  - `add -A --ignore-errors`：读不了的文件（root 拥有的构建产物、mode 000）跳过，退出码 1 视为部分成功，跳过的路径来自
    `unable to index file` 行。
  - `add` 之后兜底剔除：属性为 lfs/git-crypt filter 的改动文件；单个超过 blob 上限的；改动总量超过上限时先剔除最大的。
  被剔除的已跟踪路径在快照里保持 HEAD 的内容（不会变成删除），未跟踪路径直接不收。
- **关停**：每个 job 的快照分到剩余关停预算的一份（上限 2 分钟），作为 `gitops.Opts.Deadline`：carrier 通道每次 git 调用的超时都
  截到该时刻，到点杀掉整个 git 进程组，不会有推送活过 daemon 进程；到点后剩下的仓库不再尝试，`carrier_push_failed` 写明
  `deadline exceeded`（而不是误导的仓库布局拒绝原因）。
- **hook**：`refs/pf-wip/*` 是草稿 ref，允许删除与 non-fast-forward；只检查新对象（`rev-list --objects <new> --not --all`）的
  blob 上限，用 carrier 的 100 MiB；不做 seq / gitlink 检查。其余 ref 的规则不变（`refs/pf-task/*` 仍禁止删除与非快进）。
- **恢复**（`runtime.restoreTaskWIP`，在 `restoreTaskRepoState` 之后，只对新 clone）：取 `refs/pf-wip/<name>`；若其唯一父提交等于
  恢复后的 HEAD，把新 clone 的 `.git/index` 复制成临时 `GIT_INDEX_FILE`，由 git 执行 `read-tree -m -u <head> <wip>` 把改动写入
  工作树（删除、mode、目录变 symlink 都由 git 处理，不跟随 symlink 走出工作树；没有手写的删除）——不提交、不移动 HEAD、
  仓库 index 不变，改动以未暂存/未跟踪形式出现（原先暂存与否的区别不保留）。父提交不一致（carrier 已前进）或读取/应用失败 →
  不应用，发 `task_wip_not_applied` 事件（`repo`、`ref`、`head`、`wip`、`wip_parent`、`reason`）。不阻塞认领。
  应用后 WIP ref 保留，由下一个关闭点覆盖或删除；在那之前崩溃再认领会再次应用同一份（HEAD 未变时结果相同）。

## 8. 安全边界 <!-- §8 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（owner generation、SYNC-* 码、Revision/CAS/manifest）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

- 发布面信任模型：只有持有当前 generation 的 Daemon 可写 Revision 数据（§2.9 fencing）；Main
  Agent 不持有直接写入凭证，只能向本机 Daemon 发起发布请求（`00` INV-11/`04` INV-D3）；块内容不
  做内容审查（Capsule 内本就是任意工作产物）。
- 落盘/恢复的路径逃逸防护（同步根内路径白名单、symlink 目标校验、`../` 拒绝）权威在
  `03-execution-capsule.md#§6`，本文所有写入路径——首次发布的打包、以及 §7.4 的恢复重建——都必须
  遵守同一规则，违反即 `SYNC-400`（§2.11）。
- 恢复拉取面（GET 块）要求 WI 读权限 + 只读；删除面要求 §2.10 三重门 + admin 角色。
- 传输安全：一期 HTTP 内网直连（部署边界即信任边界，`07-deployment-pilot.md#§2`）；TLS 与凭证轮换在 `07-deployment-pilot.md#§8` 的部署
  安全内评估。

## 9. 监控 <!-- §9 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（SYNC-* 码、Revision/CAS/manifest）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

- 发布延迟（从触发事件到 HEAD 推进完成）；write freeze 时长分布与超时次数（区分"强制触发
  超时升级为基础设施失败"与"milestone 超时放弃"两类，并细分为"确认/判定阶段"与"staging
  固化阶段"两段耗时）；publish mutex 持有时长（含块上传与 manifest 提交阶段）与出现排队次
  数，二者分开统计用于区分本地操作瓶颈与网络瓶颈；fencing 拒绝数（`SYNC-403`，每次都应对应
  一次接管审计事件）；HEAD 回退事故（必须为 0，出现即需 page）；幂等命中次数（用于观察
  重放/重试的实际发生频率）；pending publish 记录数量与最长存活时长（§2.8；记录长期不删
  即提示存在未消歧的确认丢失，需排查 GET HEAD 消歧是否失效）；tombstone 计数与撤销次数；CAS 存储增长与 checkpoint 压缩率；
  Daemon 本地 staging 目录占用与清理延迟（长期未清理即提醒排查失败/崩溃残留）。

## 10. 测试与故障注入 <!-- §10 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（持久 WI Agent、owner generation、SYNC-* 码、Revision/CAS/manifest、flush 证据、watcher dirty 位、takeover）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

- 单一路径断言：制造 watcher 检测到文件变化但没有任何 §2.4 触发事件发生的情形 → 断言不产生
  任何发布、HEAD 不动，仅 dirty 标记被写入。
- write freeze 协议：在 step agent 持续写入时人为触发一次全扫描发布 → 断言 Daemon 不开始扫描，
  直到 WI Agent 确认停止写入；断言扫描窗口内没有任何新文件写入落入本次 manifest。另构造 〔#111分类：删→驱动〕
  WI Agent 进程、其全部命令子进程与 step agent 进程组均已终止的情形（模拟优雅停机/崩溃） 〔#111分类：删→驱动〕
  → 断言 Daemon 不等待任何确认信号即开始扫描。
- 进程组未完全终止：构造 WI Agent 顶层进程已退出但它派生的某个命令子进程/step agent 进程 〔#111分类：删→驱动〕
  组仍存活的情形 → 断言 Daemon 不视为 write freeze 已获取，继续等待该子进程组终止，不
  提前开始扫描。
- 代替 WI Agent 已开始写入：构造新 WI Agent 已经先于本次发布请求写过 〔#111分类：删→驱动〕
  `runtime-state/execution.json` 的情形 → 断言 Daemon 不再使用进程终止判定，转为对该新
  WI Agent 发起正常确认请求。 〔#111分类：删→驱动〕
- staging 隔离与独立副本（修复：防止上传阶段回读活目录；B09：禁止硬链接）：在 write freeze
  释放后、块上传开始前，人为修改 Capsule 工作树内容（包括对本次已固化进 manifest 的某个文件
  执行**原地写**——以 O_RDWR 不截断方式覆写文件中段字节——与 **truncate**（截短与截长两种
  都要覆盖；修复 B09 的两类原地破坏方式））→ 断言本次上传/提交的字节仍与冻结窗口内固化的 manifest 摘要一致，不受释放后
  修改影响（即上传阶段确实只读 staging，未重新读取活动目录）；同时断言 staging 中对应块的
  字节与 sha256 未变，且 staging 块与工作树源文件不共享 inode（`st_ino` 不同）——任何实现
  路径都不得对活动工作树文件使用硬链接（§2.7，SYNC-INV-7）。
- pending publish 记录与 GET HEAD 消歧（B10）：构造块上传开始前 Daemon 被杀 → 断言重启后
  pending 记录存在且其引用的 staging 目录未被清理；随后分别构造三分支——服务端 HEAD 已停在
  记录的 `seq` 且 `manifest_sha` 相同（发布实际已成功）→ 断言视为成功、删除记录、不重发；
  HEAD 停在记录的 `parent` → 断言从 staging 安全重发且 HEAD 最终推进；HEAD 已越过 `seq`
  （或同 `seq` 不同 `manifest_sha`）→ 断言走全量对账分支：丢弃过时声明、不盲目重发旧
  manifest，由下一次全扫描发布收敛（§2.8）。
- manifest 幂等身份（B10）：两次独立冻结扫描生成内容相同、仅 `built_at` 不同的 manifest →
  断言二者 `manifest_sha` 相同；用其中任一重放历史成功发布 → 断言命中 §2.8 步骤 1 幂等
  检查而直接返回成功。
- final flush 证据（D6，§6.1）：构造 generation 收尾前的冻结扫描与 HEAD 一致 → 断言生成
  `frozen_checkpoint`（含 `bytes_digest`/`worktree_state`）且通过 `02-aihub-core.md#§6.4` 校验；构造扫描发现未发布改动
  → 断言必须先完成一次真实发布才能收尾（直接提交证据被 `CORE-425` `reason=UNPUBLISHED_CHANGES` 拒绝）；构造首次 claim
  后无 HEAD 且从未产生同步面写入即 pause → 断言证据为 `published_seq=0`+
  `frozen_checkpoint.bytes_digest == EMPTY_SYNC_BYTES_DIGEST` 且 Core 接受；构造无 HEAD 但本 generation 已有同步面写入 → 断言
  Daemon 先走一次 bootstrap 发布（seq=0）再生成证据，不允许以空链形态
  静默丢弃未发布改动。
- write freeze 与 publish mutex 的释放时机差：断言 write freeze 在 staging 固化完成后立即
  释放（WI Agent 可恢复写入），而 publish mutex 直到本次块上传+manifest 提交的成功/失败 〔#111分类：删→驱动〕
  结果处理完毕才释放，其间该 WI 的下一次发布请求保持排队。
- manifest 固定顺序（SYNC-INV-6）：在同一次扫描中对 `runtime-state/execution.json` 与另一个
  产物文件同时发起变化（实验环境下人为控制时序）→ 断言生成的 `ops` 中 `execution.json` 对应的
  `put` 排在最后确定（其所记录的内容反映其余文件已固定后的状态）。
- 幂等重放：对已成功发布的 `(seq, manifest_sha)` 重放同一 `POST manifest` 请求 → 断言直接
  返回成功且不校验 parent、不产生第二条 `capsule_revisions` 行；用一个此时 `parent` 已不等于
  当前 HEAD 的历史请求重放，断言依然成功（§2.8 场景一/二）。
- 先数据后状态：在块上传中途 kill AIHub → 断言 HEAD 未动、下一次触发时重试后收敛；在 manifest
  提交后崩溃（HEAD 已推进）→ 断言 Revision 完整可重建。
- fencing：双进程模拟新旧 generation 并发发布 → 断言旧方 `SYNC-403`、新方成功、无交叉写入。
- symlink 逃逸：制造一个 `target` 解析后越出同步根的 `symlink` 操作 → 断言首次发布与恢复重建
  均拒绝该操作（与 `03-execution-capsule.md#§6` 的校验路径一致）。
- Harness 同步范围：发布一个本次变化仅在 `harness/<adapter>/session-data/` 内的快照（不
  碰 `runtime-state/`/`repos/`/`artifacts/`/`logs/`/`session.json`）→ 断言本次 manifest
  的 `ops` 为空或不发生发布（dirty 仅因不参与同步的目录变化而产生时不触发实质性变更）；
  另构造一次包含 `harness/<adapter>/session.json` `put` 的历史 Revision，随后走 §7.4 远端
  现场恢复 → 断言新生成的 `session.json` 与新 `session_id` 与历史值不同，恢复流程并未
  将历史 `session.json` 写回工作树。
- 物化目录排除（SYNC-INV-10，§4.1）：制造一次仅 `.pi/agents/`（或 `.claude/agents/`、`workflow.md`）内
  发生变化、不碰任何同步根路径的快照 → 断言 manifest 的 `ops` 为空或不发生实质性发布；随后走
  §7.4 远端现场恢复 → 断言这些路径不被写回工作树，由新 WI Agent 在第 0 步按物化规则重建。 〔#111分类：删→驱动〕
- **可注入故障**：网络分区（预期积压后下一次触发事件补齐）；磁盘满（预期 `CORE-507`、HEAD
  不动）；块位翻转（预期抽样校验发现并重发）；进程崩溃于步骤 2/4/5/6 各阶段（预期分别对应
  §7.1/§7.2 的收敛路径；步骤 2 崩溃尤指 Daemon 本身崩溃与 WI Agent 崩溃两种子情形都需覆 〔#111分类：删→驱动〕
  盖）；takeover 竞态（预期 §7.3 序列）；删除撤销（预期 tombstone 窗口内可撤销）。

## 11. 迁移与 rollout <!-- §11 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（Revision/CAS/manifest）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

- 新系统无存量迁移。协议携带自描述版本信息（checkpoint manifest 的 `kind` 字段与 CAS 布局
  版本）；未来存储后端替换（对象存储）只动 CAS 与块 PUT/GET 的实现，manifest 链与本文定义的
  单一发布路径协议不变。

## 12. 未决问题 <!-- §12 -->

1. **冻结窗口超时阈值的产线调优**：一期先给出可配置的有界等待，具体默认值按 Pilot 观测
   （`07-deployment-pilot.md#§9`）调优；开放。
2. **checkpoint 保留与块 GC 的成本模型**（保留窗是否按 WI 体量分级）：一期固定窗口，开放。
3. **HTTP 传输的 TLS 化时点**：部署边界评估后定（`07-deployment-pilot.md#§12` 联动），协议面不因之变化。
