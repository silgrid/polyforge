# 00 — 总体架构：三模块边界与 Core/Capsule 状态分离

> **ARCHIVED 2026-10-16** (repo review 2026-10-16 wave 4): describes the v0/v1 design and is kept for history only — do not build from it. Current reference: [reference.md](../reference.md); decisions: [v2-converge.md](../v2-converge.md). Live mechanisms this file still held were moved there before archiving; section anchors are unchanged.

> **ARCHIVED（repo review 2026-10-16 G-1）— 本文大部分描述已删除的机制：WI Agent（每 WI 一个持久 agent，INV-14）、Interactive/Batch/Drain、watcher dirty 位与 Revision 发布、`internal/{core,workflow,daemon,…}` 模块树、force-takeover / budget-increments / rotate-secret。标有 ARCHIVED 横幅的节 do not build from this；未标横幅的节也只作历史与溯源参考。现行设计权威：[v2-converge.md](../v2-converge.md)、[wi-kind-contract.md](../wi-kind-contract.md)、[08-capsule-base-migration.md](../08-capsule-base-migration.md)；与代码冲突时一律以代码为准。**
> Most of this document describes deleted mechanisms. Sections bannered ARCHIVED must not be built from; the live authority is v2-converge.md plus the code.

> Polyforge vNext 设计基线文档。本文档是其余七份文档（`01`–`07`）的**术语与不变量基线**：
> 全部跨文档术语的权威分配表（§2.2）、全部全局不变量（§2.3）在此定义，其余文档只做交叉引用
> （`文件名#锚点`），不得重复定义同一术语。

| 权威项 | 值 |
|---|---|
| 文档定位 | vNext 总体架构与基线 |
| 上游依赖 | 无（基线文档） |
| 术语权威表 | 本文 §2.2（全局） |

## 1. 范围与非目标 <!-- §1 -->

### 1.1 范围 <!-- §1.1 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（持久 WI Agent、owner generation、Interactive/Batch/Drain、watcher dirty 位、takeover）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

- 定义 Polyforge vNext 的三个部署单元——**路由 Skill（Routing Skill）**、**Daemon**（每机一个的
  本地执行守护进程，进程名 `pf-daemon`，见 §3.1 命名裁定）、**AIHub（核心服务）**——的职责边界与
  判据。
- 定义 **Core 数据**（AIHub 持久化）与 **Capsule 执行态**（每 WI 一个，落在本机文件系统）的分离
  原则：Core 绝不保存 workflow 执行状态与执行进度，Capsule 的 `runtime-state/execution.json` 是
  **唯一执行权威**（§4.2、INV-1）。
- 定义 Daemon 作为 Interactive / Batch / Drain 三种模式下**唯一** WI Job 执行者的判据（§3.4）：
  Interactive 是单 WI 的提交/观察/回答 needs_input，Batch 是调度器推荐一个 wave、经人确认后成批
  提交，Drain 是作为 AIHub 调度器（vNext 自带的就绪队列）的自主消费者、自动连续执行一波又一波
  wave；并把「优雅停机」
  （quiesce / shutdown）明确定义为一个**完全不同**的概念——它是 Daemon 进程生命周期的终止路径，
  不是第四种执行模式，不得把 Drain 定义为停机，也不得把停机定义为一种「没活可干」的空闲状态
  （§3.5、INV-13）。
- 定义 AIHub 的**对外面与接口分流**：AIHub 的对外客户端面有且只有 HTTP API 与 `pf` CLI 两个
  （MCP 删除，§3.1）；AIHub 进程只开一个 HTTP 监听端口（A5 单端口裁定，§3.1）；路由 13 个
  用户动词按目标面二分为「经 `pf` CLI 直连 AIHub」与「打 Daemon 本机面」（分流表 §5.1；
  第 13 个动词 `ship` (removed by polyforge#166: delivery is the workflows' deliver step) 为基线修订 polyforge#37 所加，见 §5.1）；
  AIHub 服务端是权限硬边界的唯一执行点（A17，§8）。
- 【polyforge#106 修订】原 v1 的「每 WI 一个持久化 Harness RPC WI Agent（agent 侧执行
  skill 主循环 + harness-native subagent 派生）」执行面已被删除。现在的执行引擎是 Daemon 内的
  **Go driver**：按 workflow 的 step 图逐 step 派生短驻 harness 进程（envelope 一次一步），
  驱动器独占推进 `execution.json` 状态机，Daemon 无条件绑定 driver，无引擎选择、无回退。
  历史 WI-Agent 面的删除裁定见 PR 描述（re-audit 分类）与本目录 §3.6 的删除注记。
- 定义同步的**触发时机边界**：仅在 step 完成/中断、WI 出最终结果、Daemon 优雅停机、以及可选
  milestone 上做一次全扫描发布；触发请求由该事件的责任方（通常是 WI Agent）**发起**，实际的
  全扫描与发布是 Daemon 的机械执行动作；后台 watcher 的唯一职责是把 Capsule 标记为 dirty，不得
  自行触发扫描或发布（§4.3、INV-11，细节见 `05-sync-revision-recovery.md`）。
- 定义一期部署形态（单 active AIHub + SQLite + 本地文件；v1 面向 trusted internal repo 场景，
  Linux/macOS 均为 host-native 执行，不引入 per-step isolation）与后续演进方向（§11）。
- 明确一期**保留**的既有机制，不因本次简化架构而移除：per-WI worktree、owner generation 仲裁、
  takeover fencing、WI Agent 对子进程的进程组管理、Git diff 能力、路径安全（subagent 写限制、
  凭证隔离；两者在 host-native 同 UID 形态下均为防误写措施而非安全边界，见 §8）、单 active
  AIHub + 单 SQLite + 单机本地文件系统。
- 建立全部八份文档共用的术语权威分配表与跨文档不变量基线。

### 1.2 非目标 <!-- §1.2 -->

- 不实现任何代码、DDL 迁移或可运行原型（本目录全部为设计文档）。
- 不设计多 active AIHub / 主备 / 分片 / 水平扩展（一期单 active Service，见 `02-aihub-core.md#§2.2`）。
- 不设计 PostgreSQL / JuiceFS / S3 接入层（仅作为 §11 演进方向提及，不给出接口设计）。
- 不设计 Windows 平台与 K8s 内运行 Daemon 的一期形态。
- **v1 明确排除**（裁定，非「暂缓」）：任何形式的 per-step isolation。v1 只支持 trusted
  internal repo 场景下的 host-native 执行；相关裁定见 §3.7。
- 不为 Daemon 引入「逐 step 新启动 headless agent 进程」的执行路径——每 WI 一个持久 WI Agent
  是唯一执行形态（INV-14），不作为可选项与旧形态并存。
- 不迁移 workflow-v2 的 invocation / episode / retry / supersede 状态机（vNext 按新系统对待；历史
  教训仅以回归引用形式出现在 `07-deployment-pilot.md#§10.2`，不得出现在 00–06 的设计正文中）。
  执行状态/invocation/episode 一律不迁移；**唯一例外是 Memory 存量**——一次性导出为 Markdown
  权威文件（权威在 `06-memory-search.md#§10`），不受本条非目标约束，总则见 §11。

## 2. 术语与不变量 <!-- §2 -->

### 2.1 术语首次定义索引 <!-- §2.1 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（持久 WI Agent、Revision/CAS/manifest、Interactive/Batch/Drain、watcher dirty 位、takeover）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

以下术语的**唯一权威定义**均在其权威文档（见 §2.2 分配表）；本节仅作导航索引：
- **路由 Skill、Daemon、Interactive/Batch/Drain、优雅停机（quiesce/shutdown）、持久化 Harness
  RPC WI Agent、harness-native subagent、harness 适配器最小契约**：本文 §3。
- **`pf` CLI、路由 verbs 分流（13 个用户动词 → 目标面）、AIHub 对外面（HTTP + `pf` CLI，MCP 删除）、
  单端口（A5）**：本文 §3.1/§5.1。
- **Capsule、`runtime-state/execution.json`、repo inventory、Native runner profile、
  harness 会话目录与会话数据落位（per-adapter）**：见 `03-execution-capsule.md`。
- **workflow bundle / workflow.md / step skill（step agent 定义文件）/ bundle digest / budget /
  on_fail rewind / grill / e2e-author / interactive step / needs_input 判定层语义**：见
  `01-workflow-skills.md`。
- **Revision / HEAD / watcher（dirty 标记） / 全扫描发布的协议细节 / 先数据后状态 /
  takeover fencing / 删除保护**：见 `05-sync-revision-recovery.md`。
- **Memory 文件格式 / 派生 HTML / 搜索索引 / 实时检索**：见 `06-memory-search.md`。

### 2.2 术语权威分配表（全局，唯一权威） <!-- §2.2 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（持久 WI Agent、owner generation、Revision/CAS/manifest、Interactive/Batch/Drain、watcher dirty 位、takeover、read-through）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

每个术语只在列出的文档中给出权威定义；其他文档一律以「`文件名#锚点`」交叉引用，不得重复定义。

| 术语（英文对照） | 权威文档 |
|---|---|
| 路由 Skill（Routing Skill）、Daemon 命名裁定、Interactive/Batch/Drain 模式定义、优雅停机（quiesce/shutdown）与其和 Drain 的区分、持久化 Harness RPC WI Agent、harness-native subagent、harness 适配器最小契约、模块归属判据、Core/Capsule 状态分离、全局不变量 | 本文（00）§3 / §4 / §2.3 |
| Core（数据集）、单 active Service、owner generation（存储与生命周期）、assignee / waiting_on、attrs 拆分、进度 read-through、AIHub 本地目录布局 | `02-aihub-core.md` |
| workflow.md frontmatter schema、workflow bundle、bundle digest、step skill（step agent 定义文件）内容 schema、model_candidates、grill、interactive step / needs_input 判定、e2e-author、budget、on_fail rewind（有界）、执行语义（rewind vs resume）| `01-workflow-skills.md` |
| Capsule（身份/绑定/生成时机）、`runtime-state/execution.json`（执行权威文件格式）、repo inventory、Native runner profile、harness 会话目录布局与会话数据落位（per-adapter）、Subagent 写限制不变量、多机副本裁定 | `03-execution-capsule.md` |
| Daemon 内部职责实现（Job 队列、持久 WI Agent 生命周期管理、harness 适配器启动参数）、命令执行入口（调用面）、Daemon state 存储位置、模式切换实现、全扫描发布的机械执行 | `04-local-runtime.md` |
| Revision（数据模型与文件格式）、HEAD（推进规则）、watcher（dirty 标记）、全扫描发布协议、同步传输机制、先数据后状态、takeover fencing（判定条件与错误码）、删除保护、崩溃恢复（同步域） | `05-sync-revision-recovery.md` |
| Memory Markdown 权威格式、frontmatter 约定、Markdown→HTML 派生规则、搜索索引（可重建）、实时检索、Memory API | `06-memory-search.md` |
| AIHub 对外面（HTTP + `pf` CLI，MCP 删除）、单端口（A5）、路由 verbs 分流（13 个用户动词 → 目标面）、AIHub 新 repo 实现的正式取代声明、权限硬边界（A17） | 本文（00）§3.1/§3.3/§5/§5.1/§8 |
| 一期部署形态、安装/升级/回滚、回归清单（#699…#780）、Pilot 否决门槛、与旧 aihub 系统的并存与切换 | `07-deployment-pilot.md` |

各模块的监控指标分层、测试与故障注入类别、迁移总序在本文 §9/§10/§11 给出**总则**，各文档在
总则内给出本域细节。

### 2.3 全局不变量基线 <!-- §2.3 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（持久 WI Agent、owner generation、Revision/CAS/manifest、Interactive/Batch/Drain、watcher dirty 位、takeover、read-through）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

以下不变量对全部八份文档成立；各文档可增加本域不变量，但不得与下列冲突。

| 编号 | 不变式 | 权威细节 |
|---|---|---|
| INV-1 | AIHub Core **不保存任何 workflow 执行状态**：current step、step 结果、review 轮次、per-step `budget_consumed`、wall-clock 基线、待消费 rewind 输入（gate FAIL 判定全文）、归档 attempts、interactive 待问问题/回答关联只存在于 Capsule 的 `runtime-state/execution.json`（该文件必含字段的权威 schema 在 `03-execution-capsule.md#§4.2`，本文只引用）；进度查询一律 read-through 读取该文件当前内容或其最近一次已发布 Revision | `02-aihub-core.md#§5.2`、`03-execution-capsule.md#§4.2` |
| INV-2 | 单 active Service：任何时刻至多一个 aihub 进程持有 service lock 并可写 Core | `02-aihub-core.md#§2.2` |
| INV-3 | 每个 WI 任何时刻至多一个 **generation 拥有发布权**（能把 Revision 推进 HEAD）；不保证旧 generation 的进程/WI Agent 立即停止执行——隔离约束的是写入 Core 的权利，不是本地进程的存活 | `02-aihub-core.md#§2.3`/§4.3、`03-execution-capsule.md#§2.4` |
| INV-4 | Revision 一经发布不可变；HEAD 只指向已完整落盘并通过摘要校验的 Revision（先数据后状态） | `05-sync-revision-recovery.md#§2.1`/§2.5 |
| INV-5 | 过期 owner generation 不得发布 HEAD（takeover fencing） | `05-sync-revision-recovery.md#§2.9` |
| INV-6 | harness-native subagent 不得写 `runtime-state/`（Capsule 执行权威文件），也不得读写 Daemon 凭证与 Daemon state；只有该 WI 的 WI Agent 进程可写 `runtime-state/`。该约束在 v1 host-native 同 UID 形态下是**防误写**措施（权限位 + 路径不暴露），**不是安全边界**，不构成机制级隔离（B02 裁定，见 §8） | `03-execution-capsule.md#§6.2`、`04-local-runtime.md#§8`、本文 §8 |
| INV-7 | Memory 权威内容为 Markdown 文件；HTML 与搜索索引均为派生物，可由权威内容全量重建 | `06-memory-search.md#§2.1`/§4 |
| INV-8 | WI 记录对 workflow 只保留一个 scalar：`bundle_name`（DDL 权威 `02-aihub-core.md#§4.2`；digest 列已删，polyforge#149 B9）；AIHub Core **不存储 resolved steps 或任何解析结果**。WI 创建时 AIHub 服务端读本机 checkout 内该 bundle 的 workflow.md **frontmatter** 做校验，**不解析正文、不落任何解析结果**；随创建事务写入的只有 `bundle_name` 与 `repo_inventory`，其中 `bundle_name` 为**首次认领冻结**（R2 窗口：queued 且从未认领时可经 `PATCH` 三向变更，`02-aihub-core.md#§2.6` INV-2e/§4.5），首次认领后不存在任何重新固定/替换窗口。**不可变的对象是 WI 记录的 bundle 引用（`bundle_name`），不存在 Core 侧的「版本锁定」承诺**（B07 裁定）：执行侧从本机 checkout 按 `bundle_name` 物化 bundle，不再做 digest 校验（校验链已删，polyforge#149 B9）；本机 checkout 缺该 bundle 属**配置事故**（告警、继续执行，v1 信任内网），不存在任何 Core 侧阻断或版本回取路径 | `01-workflow-skills.md#§4.6`、`02-aihub-core.md#§4.4`/§4.5、`03-execution-capsule.md#§4.5` |
| INV-9 | 业务 gate FAIL（有界 on_fail rewind）与基础设施失败（当前 step resume）语义严格区分 | `01-workflow-skills.md#§6` |
| INV-10 | 一期 AIHub 的外部持久化依赖仅为 SQLite + 本地目录；不依赖 PostgreSQL/JuiceFS/S3 | `02-aihub-core.md#§1`、`07-deployment-pilot.md#§2` |
| INV-11 | 全扫描发布只在四类事件（+ 可选 milestone）上触发；触发**请求**由该事件的责任方发起（step 完成/中断/WI 结果由 WI Agent 发起，优雅停机由 Daemon 自己发起），但实际的全量扫描与发布动作**由 Daemon 机械执行**，不存在任何一方绕过 Daemon 直接改写 Revision/HEAD 的路径；watcher 只能把 Capsule 标记 dirty，不得自行发起扫描或发布 | `04-local-runtime.md#§6.3`/§6.4、`05-sync-revision-recovery.md#§2.4`/§6 |
| INV-12 | v1 范围内 WI Agent 与 harness-native subagent 均为宿主机原生进程（Linux/macOS 同一形态）；v1 面向 trusted internal repo，采用 host-native 执行，不引入 per-step isolation 作为 v1 要求（裁定见 §3.7） | `03-execution-capsule.md#§1.2`/§5 |
| INV-13 | 「优雅停机」（quiesce/shutdown）与「Drain 模式」是两个不同概念，命名与判据均不得混用：优雅停机是 Daemon 进程生命周期的终止路径（§3.5）；Drain 是一种常驻的自主 WI Job 执行模式（§3.4c）；不得把 Drain 表述为「一种停机」，也不得把优雅停机表述为「没活可干时的空闲态」 | 本文 §3.4/§3.5、`04-local-runtime.md#§2.3` |
| INV-14 | 每个正在执行的 WI 有且只有一个**持久**的 WI Agent 进程，从 claim 存活到该 WI 到达终态或 Daemon 优雅停机中断为止；Daemon 不得为同一个 WI 的不同 step 分别启动独立的 headless agent 进程——所有 step（含 harness-native subagent 派生）都在同一个 WI Agent 进程的同一次 harness 会话内完成 | 本文 §3.6、`04-local-runtime.md#§6.1`/§6.6 |

## 3. 模块与代码目录 <!-- §3 -->

### 3.1 三模块与命名裁定 <!-- §3.1 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（持久 WI Agent、owner generation、Revision/CAS/manifest、Interactive/Batch/Drain、backupBarrier、watcher dirty 位、read-through）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

```
┌─────────────┐   自然语言路由    ┌──────────────────────┐     HTTP(gRPC/JSON)      ┌──────────────────┐
│ 用户 Harness │ ───────────────▶ │ Daemon（每机一个）      │ ◀──────────────────────▶ │ AIHub               │
│ （含路由      │  （执行面动词：    │  · Capsule 创建/管理   │  · Core 读写             │  （单 active；         │
│  Skill）     │   submit/observe/ │  · Interactive/Batch/  │  · Revision 发布          │    新 repo 实现）      │
└──────┬──────┘   answer/…）      │    Drain 三种 Job 执行  │  · 进度 read-through      │  · 对外面 = HTTP +   │
       │                          │    模式（唯一执行者）    │                          │    pf CLI（无 MCP）   │
       │ 数据面动词                 │  · 优雅停机（quiesce/   │                          │  · 单端口（A5）       │
       │（file/queue/cancel/        │    shutdown）           │                          │  · Core             │
       │ recall）                   │  · 全扫描发布的机械执行  │                          │  · Memory           │
       ▼                          └──────────┬───────────┘                          │  · Revision         │
┌─────────────┐  HTTP（直连，不经 Daemon）                                     │  · 最终结果           │
│ pf CLI       │ ─────────────────────────────────────────────────────────────▶ │  · 调度器/就绪队列     │
└─────────────┘                                                                   │    （扁平）            │
                                                                                  └────────────────────┘
                                            │ Daemon 内的 Go driver 驱动（每 WI 一份）
                                  ┌─────────▼──────────┐
                                  │ Go Driver （polyforge#106 起）│
                                  │  · Daemon 无条件绑定的唯一引擎 │
                                  │  · 按 step 图逐 step 派生短驻 │
                                  │    harness 进程（envelope）│
                                  │  · 独占推进 Capsule      │
                                  │    execution.json 状态机 │
                                  │  · 每个 transition 发布  │
                                  └─────────┬───────────┘
                                            │ 读写
                                  ┌─────────▼──────────┐
                                  │ WI Execution Capsule │（每 WI 一个，本机目录）
                                  │  · worktree/repo inventory│
                                  │  · runtime-state/      │
                                  │    execution.json（唯一执行权威）│
                                  │  · harness 会话目录（落位 per-adapter）│
                                  │  · artifacts / logs   │
                                  └─────────────────────┘
```

- **路由 Skill**：纯意图路由层。读取用户请求、判定意图（提交执行 / 查看进度 / 创建 WI /
  查询队列 / 检索 Memory…），按 §5.1 的分流表把请求发往目标面：执行面动词转给本机 Daemon，
  数据面动词经 `pf` CLI 直连 AIHub；不解析 workflow、不触碰文件、不保存状态。自然语言到操作
  （含目标面判定）的映射表是路由 Skill 的唯一内容。
- **安装面与执行面裁定**：用户 harness 只安装 `skills/polyforge` 路由 skill 这一个 skill——Pi 侧
  是 settings 中 skills 配置的一行路径，直接指向本机 aihub checkout 内的 `skills/polyforge/`
  （零复制，不搬文件）；Claude Code 侧经 plugin marketplace 安装。用户 harness **永不执行
  workflow**：Interactive 会话（§3.4a）中用户只提交意图、观察进度、回答 needs_input，workflow 的
  解析与执行全部发生在 Daemon 一侧：workflow 的执行引擎是 Daemon 内的 Go driver
  （polyforge#106 起唯一引擎，无选择、无回退；原 agent 侧执行 skill 已随 WI-Agent 引擎删除）。
  （skill 的权威定义见 `01-workflow-skills.md#§2.1`，
  部署边界见 `07-deployment-pilot.md#§2.3`）。
- **Daemon**（进程名 `pf-daemon`，全文档统一用「Daemon」这一名称，不得再用「Runtime」单独指代该
  部署单元）：本机执行管理层，是 Interactive / Batch / Drain 三种 WI Job 执行模式的**唯一执行者**
  （§3.4）。职责：接收 Job 请求、为每个正在执行的 WI 创建/回收 Capsule、为每个正在执行的 WI
  派生并监督其**持久** Harness RPC WI Agent、驱动 watcher 把 Capsule 标记 dirty、在四类触发
  时机（+可选 milestone）收到发布请求时**机械地**执行一次全扫描发布（`05-sync-revision-recovery.md#§2.4`，INV-11）、
  接收并执行优雅停机（quiesce/shutdown，§3.5）。**操作面仅本机、无网络面**：Daemon 只监听本机
  Unix domain socket，不开任何内网/网络监听端口；**Daemon 之间零通信**——不存在任何
  Daemon-to-Daemon 协议、直连或互推通道，一切跨机信息经 AIHub 中转（实现层安全边界见
  `04-local-runtime.md#§8`）。Daemon 自身不重新实现 workflow 驱动逻辑——驱动
  workflow 是 WI Agent 的职责（§3.6），Daemon 只负责 Job 的排入、WI Agent 的生命周期、三种
  模式的判据、以及全扫描发布这一件机械动作的实际执行。
- **`pf` CLI**（`cmd/pf/`，§3.3）：AIHub 的第二个对外面。数据面动词（file/queue/cancel/recall，
  §5.1）、CI/脚本与管理调用经它直达 AIHub 的 HTTP API；不经过 Daemon、不触碰任何 Capsule，
  不携带任何执行现场语义，也不持有任何超出调用者身份的权限（A17，§8）。
  配置契约（polyforge#113）：`~/.config/polyforge/cli.toml` 的
  `[workspace."<path>"]` sections（每节 `projects = [...]`）是**唯一的
  workspace 来源**——CLI 与 Daemon 读同一份映射，placement 类动词的
  `--project` 按它校验，Capsule 按被认领 WI 的 project 经逆映射落到
  `<workspace>/capsules/<project>-<seq>`（polyforge#144）；不存在任何启动期 workspace 选择面（历史旗标/环境变量已删除，仅在
  迁移文档中作为历史出现），也不存在环境默认 project——唯一例外是 `pf file create` 的
  cwd 推断（C16 裁定，polyforge#150：`cli/verbs_aihub.go` `inferProjectFromCwd`）：cwd 恰好
  落在**一个**映射 workspace 且该 workspace 恰好映射**一个** project 时，视同已传
  `--project`；其余一切情形（无包含、多包含、多 project）都要求显式 `--project`，
  绝不环境默认（#113 原则不变）；
  `pf doctor` 是本机的唯一健康面（配置 + 映射 + 连通性）。
- **AIHub**：全局仲裁与持久层。work item 生命周期状态机（排队/认领/暂停/终止）、owner generation
  仲裁、Memory 管理与检索、远端 Capsule Revision 存储与 HEAD 指针、最终结果归档，以及调度器/
  就绪队列（`{items, running, paused, pending_bundles}` 四段响应（D1 裁定，polyforge#150：以代码为准），schema 权威见
  `02-aihub-core.md#§5.1b`：`items` 为可执行段，恒排除 `bundle_name IS NULL` 的登记票，
  也排除一切终态与 `running`/`paused`/`blocked` 状态的 WI；等依赖的票不出现在任何段）。AIHub 对执行态**只读**（read-through，INV-1），
  从不落库。**对外面与单端口（定稿裁定）**：AIHub 的对外客户端面有且只有 HTTP API 与 `pf` CLI
  两个——现行系统的 MCP 服务器在 vNext 中删除，不实现、不迁移（新 repo 取代声明见 §3.3）；
  AIHub 进程只开**一个** HTTP 监听端口（A5 单端口裁定）：Core API、Revision 发布、Memory、
  service 自描述等全部端点共用该端口与同一鉴权面，不按功能分设第二监听面；仅限本机来源的
  管理类路径（如备份屏障，`07-deployment-pilot.md#§6.2`）是同一端口上的路径级来源限制，不是
  第二个端口。

### 3.2 候选功能归属判据 <!-- §3.2 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（持久 WI Agent、owner generation、Revision/CAS/manifest、Interactive/Batch/Drain、watcher dirty 位、read-through）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

任一候选功能按下列判据表唯一归属一个部署单元；判据按序适用，第一条命中即归属，列表已按特异性
降序排列。

| 序 | 判据 | 归属 | 例 |
|---|---|---|---|
| 1 | 是否只是「把自然语言意图映射为对本机 Daemon 的调用」？ | 路由 Skill | 「认领 aihub#12」「查看进度」 |
| 2 | 是否需要全局唯一仲裁（谁拥有 WI、跨机器可见、多人协作）？ | AIHub | owner generation 仲裁、WI 状态机、Memory 共享 |
| 3 | 是否是执行产生的不可再生的全局交付物（最终结果、归档）？ | AIHub | 最终 PR 结果归档、Memory 权威文件 |
| 4 | 是否是「单 WI 执行现场」的数据、进程或工具？ | WI Agent / Capsule | workflow 驱动、subagent 派生、repo 检出/worktree、Git diff、step 结果文件、`execution.json` |
| 5 | 其余（跨 WI 的本机共享设施：Job 排入、模式判据、watcher、优雅停机、全扫描发布的机械执行） | Daemon | Interactive/Batch/Drain 判据、dirty 标记触发、发布请求的实际执行 |

归属示例（判定记录，后续文档引用时不得另行归属）：

| 候选功能 | 归属 | 依据判据 |
|---|---|---|
| workflow 执行状态（current step/review 轮次） | WI Agent 写入 Capsule 的 `execution.json` | 4（+ INV-1） |
| WI 状态机（queued/running/wrapped…） | AIHub Core | 2 |
| workflow.md frontmatter 校验与 bundle digest 固化 | AIHub Core 服务端（WI 创建事务内的服务端内部步骤，无独立端点，`01-workflow-skills.md#§4.6`；解析结果不落库） | 2（+INV-8） |
| 进度查询 | AIHub read-through API → 读取 Capsule 最近发布 Revision | 2（全局可见）+ INV-1 |
| WI Agent / subagent 进程回收 | Daemon | 5 |
| 全扫描发布的实际扫描与写入 | Daemon（收到 WI Agent 或自身的请求后机械执行） | 5（+ INV-11） |
| 删除远端 Revision 数据 | AIHub（受删除保护，`05-sync-revision-recovery.md#§2.10`） | 3 |
| Memory Markdown 权威文件 | AIHub 本地目录 | 3 |
| Git diff（工作树改动查看） | WI Agent（对其 Capsule worktree 操作） | 4 |

### 3.3 代码目录（vNext 目标仓库布局） <!-- §3.3 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（Revision/CAS/manifest、Interactive/Batch/Drain、watcher dirty 位）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

**正式取代声明（AIHub 层定稿裁决）**：AIHub 按全新仓库（new repo）实现。gen-2 spec 假设③——
「vNext AIHub 由现有 aihub 代码库演进而来」——自本声明起正式作废：vNext AIHub 不继承、不演进
现行 aihub 仓的任何实现代码，Core / server / memory / sync 等各模块均从零实现。与旧代码的关系只有
两种且都不是代码继承——回归教训的引用（`07-deployment-pilot.md#§10.2`）与旧入口的处置矩阵
（§11.1）；该取代声明与 §11.1 的迁移/处置表并列，同为本设计对旧系统的唯一处置权威。部署侧的
并存与切换（Pilot 通过前旧 aihub 系统继续承担生产）见 `07-deployment-pilot.md#§2.1`。

vNext AIHub 为单仓（monorepo）形态（D0 裁定）：一个 git tag 即一个版本，仓级唯一 semver——`aihub` 服务二进制与工作机二进制（单文件：`pf` CLI 动词 + `daemon` 子命令，部署单元合并裁定 `07-deployment-pilot.md#§3`）二产物同 tag 同版构建，releases 按 tag 存二产物 + 校验清单；`skills/` 与 `workflows/` 随仓演进、不单独发版（路由 skill 与 bundle 的分发渠道即 git 本身）。滚动部署存在各机时差，但不构成独立版本线。按模块拆目录：

```
aihub/
├── cmd/
│   ├── aihub/            # AIHub Service 入口
│   └── pf/               # CLI（用户与 CI 面）+ `daemon` 子命令——单文件工作机二进制（部署单元合并裁定 `07-deployment-pilot.md#§3`）
├── internal/
│   ├── core/             # Core 领域模型 + SQLite 存取（02）
│   ├── workflow/         # workflow.md frontmatter 校验、bundle digest、驱动语义（01）
│   ├── daemon/           # Daemon：Job 排入、Interactive/Batch/Drain 判据、优雅停机、全扫描发布执行（04）
│   ├── driver/           # Go driver：Daemon 唯一执行引擎（polyforge#106），按 step 图驱动 envelope 派生（`00-architecture.md#§3.6` 修订, 04）
│   ├── capsule/           # Capsule 布局、inventory、execution.json、会话目录、Native runner（03）
│   ├── sync/              # Revision、watcher（dirty 标记）、全扫描发布协议、fencing（05）
│   ├── memory/            # Markdown 权威、HTML 派生、索引（06）
│   └── server/            # HTTP API 面（02/05/06 的 API）
├── docs/design/polyforge-vnext/   # 本设计目录
├── skills/
│   └── polyforge/       # 路由 skill（用户 harness 唯一安装面，§3.1）
└── workflows/           # 参考 workflow bundle 源：<bundle>/workflow.md + agents/*.md（skill 层布局权威见 `01-workflow-skills.md#§3`）
```

各模块实现细节由对应文档定义；本文只锁定归属与依赖方向：`daemon → driver → workflow → core`，
`sync → core`，`capsule → sync`（发布 Revision），单向无环。

### 3.4 Daemon 的三种 WI Job 执行模式（唯一执行者判据） <!-- §3.4 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（持久 WI Agent、Revision/CAS/manifest、Interactive/Batch/Drain、read-through）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

Daemon 是 Interactive / Batch / Drain 三种模式下**唯一**的 WI Job 执行者：任何 WI 的 Job 执行
（创建 Capsule、派生持久 WI Agent、驱动到完成）都必须经过 Daemon，不存在绕过 Daemon 直接执行
Job 的路径。三种模式的区别仅在 Job 的**来源与准入判据**，执行机制（持久 WI Agent + Capsule）
完全相同。Daemon 侧 Job 队列（jobq）对 pending Job 按 WI priority 降序、同级 FIFO 排序，无抢占、
按序补位——本文一句带过，簿记、并发上限与排序/补位的完整规则权威在 `04-local-runtime.md#§6.1d`。

#### 3.4a Interactive <!-- §3.4a -->

Job 来自本机路由 Skill 会话。Daemon 在此模式下的职责严格限定为
**submit（提交单个 WI）/ observe（观察）/ answer（回答 needs_input）**三个动作：

- **submit**：把用户在会话中给出的针对单个 WI 的 claim/resume 请求提交给 Daemon，Daemon
  为该 WI 创建一个 Job（§6.1）。
- **observe**：把该 WI 持久 WI Agent 的执行进度回传给会话，**两层观察**（协议权威
  `04-local-runtime.md#§5.1a`；两层均不额外落库，INV-1）：本机拥有该 WI 的 Capsule 时走第一层——
  Daemon 内存 ring buffer 的增量事件流（`GET /jobs/{wi_id}/observe?after=<cursor>`）；本机没有该
  Capsule（跨机观察，在别机执行或已结束）时兜底走第二层——`02-aihub-core.md#§5.2` 的
  read-through，渲染最新已发布 Revision 的 `execution.json` 并标注「数据截至最近一次发布」；
  进度内容中的待问问题以本节下方「needs_input 事件/观察报文」的形态呈现。
- **answer**：当该 WI 的当前 step 处于 needs_input（人在会话内驱动的 interactive step，
  等待用户输入才能继续，判定层语义见 `01-workflow-skills.md` 的 interactive step / grill）
  时，把用户的自然语言回答转发给该 step。中继报文的具体 JSON 格式见下方。

Daemon 在 Interactive 模式下**不做任何自主调度决策**（不自动挑选下一个 WI、不自动批准
gate）；一次 Interactive 会话只对应一个正在被驱动的 WI。

**needs_input 事件/观察报文格式（权威定义）**：当某个 interactive step 进入 needs_input
等待时产生一个「待问问题」事件，它在 observe 流中呈现为下列 JSON 观察报文（本机事件流层与
跨机 read-through 层同形态，`04-local-runtime.md#§5.1a`）。该报文是
`03-execution-capsule.md#§4.2` 的 `steps.<id>.pending_question`（持久化形态，schema 权威在
`03-execution-capsule.md#§4.2`）面向 observer 的呈现层，不另立持久化存储：

```json
{
  "question_id": "q_7f3a1c",
  "step_id": "code_review",
  "asked_at": "2026-09-26T09:58:00Z",
  "content": "呈现给用户的问题正文（自然语言）",
  "status": "pending",
  "timeout_hint": "4h"
}
```

- `question_id`：必填，待问问题的唯一标识，与下方 answer 中继报文的 `question_id` 同一标识
  （生成规则见下方 answer 报文字段说明：由 WI Agent 在进入等待前生成，格式 `q_` + 6 位随机
  字符）。
- `step_id`：必填，产生该问题的 step 的 id（持久化形态中该信息由 `steps.<id>` 的键承载，
  观察报文中显式给出）。
- `asked_at`：必填，RFC3339，提问时刻。
- `content`：必填，非空字符串，呈现给 owner 的问题正文全文。
- `status`：必填，`pending`（等待回答）或 `answered`（该问题已被一次成功回答消费）：进入
  等待时为 `pending`；回答被消费（step 原地续跑、`pending_question` 清空，`03-execution-capsule.md#§4.2`）后，该
  问题不再作为活动待问问题出现在 observe 流中。
- `timeout_hint`：可选，提示用户该问题预期可等待多久的宽松提示（如 `"4h"`）；仅为呈现
  提示，不构成强制超时，也不产生任何自动作答。

**产生时机（observe 流）**：WI Agent 驱动某 step 判定为 needs_input（判定层语义见
`01-workflow-skills.md` 的 interactive step）时，在把该 step 置入等待的**同一次原子写入**中把
待问问题持久化到 `03-execution-capsule.md#§4.2` 的 `steps.<id>.pending_question`；该写入
同时是「step 中断」类触发边界（§4.3），WI Agent 随即向本机 Daemon 发起一次发布请求，待问
问题经发布链路进入最近发布的 Revision、对 read-through（`02-aihub-core.md#§5.2`）可见——此后 observe
动作回传的进度中即包含上述观察报文（`status="pending"`）。从「写入 `pending_question`」到
「observe 可见」之间存在一次发布的时延，这是设计使然：问题必须先落盘、后可见，不存在
「已问出去但 `execution.json` 里没有记录」的窗口（`03-execution-capsule.md#§4.2`）。

**answer 中继报文格式（权威定义，修复 B14 OQ5；单一权威在本文，`04-local-runtime.md#§5.1`
的 answer API 对本报文的引用见该行）**：报文为 JSON 对象，由 UI/CLI 经
`04-local-runtime.md#§5.1` 的 `answer(wi_id, payload)` 提交（本报文即该 API 的 `payload`），
Daemon 原样转发给正在等待的 WI Agent，不解析、不改写：

```json
{
  "question_id": "q_7f3a1c",
  "answer_id": "a_7k9m2p",
  "answer_text": "用户的自然语言回答正文",
  "answered_at": "2026-09-26T10:00:00Z",
  "answered_by": "u_5dFjeaMZ"
}
```

- `question_id`：必填，必须等于该 step 在 needs_input 等待阶段生成并随进度展示给用户的那个
  `question_id`（由 WI Agent 在进入等待前生成，格式 `q_` + 6 位随机字符，写入
  `03-execution-capsule.md#§4.2` 的 `steps.<id>.pending_question`）。不匹配则拒绝为
  `DAEMON-409`（避免回答错阶段的旧问题或重放的旧回答）。待问问题写入 `execution.json` 的
  同时，作为「step 中断」类触发边界（§4.3）同步触发一次发布请求，使问题经发布链路对 owner
  可见（`01-workflow-skills.md#§4.3`）。
- `answer_id`：必填，本次回答的唯一 id（格式 `a_` + 6 位随机字符，由提交方生成）。Daemon 的
  answer API 以 `answer_id` **幂等**：同一 `answer_id` 的重复提交不产生第二次效果（不重复应用、
  不重复消费），返回与首次提交一致的结果。
- `answer_text`：必填，非空字符串，不做结构化解析（引擎侧把回答原文投递给进行中的 step
  上下文，本报文不强制结构）。
- `answered_at`：必填，RFC3339，由提交方（UI/CLI）填写，不由 Daemon 重新覆盖的。
- `answered_by`：必填，回答者的 `user_id`，与 `04-local-runtime.md#§5.1` 请求的认证身份必须
  一致，不一致拒绝为 `DAEMON-403`。

**去重/过期回答规则**：同一 `question_id` 的重复 `answer` 调用（同样 `question_id`）在该
`question_id` 已被消费（step 已继续执行）后到达，拒绝为 `DAEMON-409`（旧 `pending_question` 已不
存在），不会被错误应用到后续新产生的同名问题（`question_id` 每次重新随机生成，不重复利用）。
同一 `answer_id` 的重复提交按 `answer_id` 幂等处理（无第二次效果，不报错）；仅当 `answer_id`
不同而 `question_id` 已被消费时，才落入本条的 `DAEMON-409` 拒绝。

#### 3.4b Batch <!-- §3.4b -->

AIHub 调度器（vNext AIHub 自带的就绪队列——`{items, running, paused, pending_bundles}` 四段响应（D1 裁定，polyforge#150），schema 权威
`02-aihub-core.md#§5.1b`，端点 `02-aihub-core.md#§5.1` 行 4；wave 推荐消费其 `items` 可执行段）先给出一批候选
WI 作为**一个 wave 的推荐**；这个推荐必须经**人显式确认**（可增删）之后，才由 Daemon 一次性成批（wave）
创建 Capsule 并派生持久 WI Agent。wave 的最终边界（确认后实际提交哪些 WI）由人给定，
Daemon 不自行扩大或缩小该集合，也不在未经确认前把调度器的推荐当作既定 Job 执行。wave 内**同时
派生**的 Job 数受 `04-local-runtime.md#§12` 已裁定的 `max_concurrent_jobs` 默认上限约束（缺省
4，可覆盖），不影响 wave **内容**由人给定的裁定。

#### 3.4c Drain <!-- §3.4c -->

自主的、常驻的连续 wave 执行模式：Daemon 反复向 **AIHub 调度器**（vNext AIHub 自带的就绪
队列——`{items, running, paused, pending_bundles}` 四段响应（D1 裁定，polyforge#150；schema 权威 `02-aihub-core.md#§5.1b`）及其排队与
优先级判据，端点 `02-aihub-core.md#§5.1` 行 4）取下一批可执行 WI，无需每个 WI 单独的
人工确认，取到即提交，一波接一波连续执行。Drain **不实现自己的新调度算法**——它是 AIHub 调度器
的一个自主消费者；调度决策（谁先执行、并发度）的权威定义在 AIHub 侧，Daemon 侧只负责
按该结果创建 Job（同样受 `max_concurrent_jobs` 上限约束）。Drain 是一种**执行模式**，不是停机
路径（INV-13）。

### 3.5 优雅停机（quiesce / shutdown，与 Drain 明确区分的独立概念） <!-- §3.5 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（持久 WI Agent、Revision/CAS/manifest、Interactive/Batch/Drain、takeover）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

「优雅停机」（graceful shutdown，亦称 **quiesce** 或 **shutdown**，三个名字指同一件事，全文档
可互换使用，但均不得与 §3.4c 的 Drain 混用）是 Daemon **进程生命周期的终止路径**，与 Drain 是
两个不同的概念，不共用名字，也不共用判据（INV-13）：
- 优雅停机的触发条件是 Daemon 进程收到终止信号或人工发起的停止请求，**不是**「没有更多 WI 可
  做」。「没有更多 WI 可做」时 Drain 只是空闲等待（poll AIHub 调度器），这不是停机，Daemon 进程
  继续存活。
- 优雅停机不创建新 Job；它等待当前进行中的持久 WI Agent 到达一个可安全中断的边界（当前 step
  完成或显式的中断点），然后**自己**（代替仍在进行的 WI Agent）发起一次 §4.3 定义的全扫描
  发布请求，交由 Daemon 自身机械执行，最后允许 Daemon 进程退出。
- 优雅停机之后，未完成的 WI 保持其 Capsule 现场不变，下次任意机器上的 Daemon 为该 WI 派生新的
  持久 WI Agent 时按 `05-sync-revision-recovery.md` 的 takeover 恢复语义从最近发布的 Revision
  继续。
- 判断一次终止请求是「优雅停机」还是「Drain 模式的一次空闲」的唯一依据是**触发来源**：终止
  信号/人工停止请求 ⇒ 优雅停机；调度器暂时无候选 WI ⇒ Drain 空闲，不是停机的任何一种形式。

### 3.6 持久化 Harness RPC WI Agent（每 WI 一个） <!-- §3.6 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（持久 WI Agent、Revision/CAS/manifest、Interactive/Batch/Drain）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

- **身份**：每个正在执行的 WI 有且只有一个 WI Agent 进程，由 Daemon 在创建 Job 时派生，
  在 Job 结束（wrap/fail/优雅停机中断）时回收（INV-14）。
- **持久，非逐 step 起新进程**：该进程从 claim 开始持续存活，驱动该 WI 全部 step，直到该 WI
  到达终态或被优雅停机中断为止；Daemon **不**为同一个 WI 的每个 step 分别新起一个 headless
  agent 进程——step 之间的切换、subagent 的派生，都发生在同一个 WI Agent 进程、同一次 harness
  会话内部（INV-14）。这与「每 step 起一个短生命周期无状态 agent」的旧形态是两种不同的执行
  模型，v1 只采用前者。
- **原生与可观察**：WI Agent 是宿主机原生进程（v1 trusted internal repo、host-native，不引入
  per-step isolation，INV-12），Daemon 通过标准进程句柄（pid、退出码、stdout/stderr 管道、进程组）
  观察其存活与输出；不存在 Daemon 无法感知 WI Agent 存活状态的情形。
- **可恢复**：WI Agent 崩溃或被杀后，Daemon 检测到进程退出，可基于 Capsule 的
  `runtime-state/execution.json` 当前内容重新派生一个新的 WI Agent 进程续跑，不要求从零
  重放整个 workflow（细节见 `03-execution-capsule.md#§7`、`04-local-runtime.md#§7`）。
- 【polyforge#106 修订】**引擎唯一**：workflow 的自动执行与交互式（Interactive）路径下人工
  逐 step 推进使用**同一个** Daemon 内的 Go driver 实现，不存在「自动执行用一套引擎、交互
  执行用另一套引擎」的分叉；区别只在于该 step 是否为 needs_input（人工输入门），这一判据
  本身属于 `01-workflow-skills.md` 的 workflow 语义。Daemon 无条件绑定 driver（无引擎选择
  旗标、无回退），原 agent 侧执行 skill 已删除，任何 harness 都不安装任何执行 skill
  （§3.1 安装面裁定、`01-workflow-skills.md#§2.1`）。
- **使用 harness-native subagent**：WI Agent 需要派生 step agent（step 级子代理，例如 code-change 的
  实现子任务）时，使用该 WI Agent 所在 harness 自身提供的原生 subagent 机制（同进程/同会话内
  派生的子会话/子上下文），而不是启动独立的 headless agent 进程；subagent
  与 WI Agent 共享同一宿主机原生执行环境，但被 INV-6 限定不得写 `runtime-state/`——该限定在
  host-native 同 UID 形态下是防误写措施（权限位 + 路径不暴露），不是安全边界，不构成机制级隔离
  （B02 裁定，见 §8）。
- **发起发布请求，不自己写 Revision**：WI Agent 在 §4.3 的四类触发时机上向 Daemon 发起一次
  全扫描发布请求；实际的扫描、打包与 HEAD 推进由 Daemon 机械执行（INV-11）；WI Agent 不直接
  持有 Revision 写入路径。

### 3.6a Harness 适配器的最小契约（v1：Pi、Claude） <!-- §3.6a -->

> **ARCHIVED — 本节描述或依赖已删除的机制（持久 WI Agent、owner generation）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

【polyforge#106 修订注记】本节原文描述的「宿主机原生进程 + agent 侧执行主循环」抽象随
WI-Agent 引擎删除：引擎是 Daemon 内的 Go driver，step 派生是短驻 harness 进程。下表的
会话身份/派生裁定在 driver 时代的对应面（短驻 step 进程的 envelope 语义与 journal 栅栏）
由 `04-local-runtime.md` 与 `03-execution-capsule.md` 给出；原文保留为历史裁定记录。

| 契约项 | 要求 |
|---|---|
| 进程持久性 | 每 WI 一个长驻进程，符合 §3.6 持久化要求，不因单个 step 结束而退出 |
| 会话身份规则 | pause 终止当前 generation（报告发布完毕后 WI Agent 退出）；resume 总是开启一个新 generation，因而总是建立一个全新 session id，从不复用 pause 前的旧 session；**唯一**复用旧 session id 的情形是同一台机器上同一个还没结束的 generation 因 WI Agent 进程崩溃而由 Daemon 重新派生续跑（非 pause 导致的进程退出，同机、同 generation）；换 generation（包括 pause 后的 resume、或同机的 owner 变更）或换机器时一律建立新 session，新 WI Agent 从 Capsule 的 `runtime-state/execution.json` 恢复执行现场；完整规则权威见 `03-execution-capsule.md#§4.4`，本节不重复定义 |
| 会话数据落位 | 跨 harness 的契约要求是**封存（seal）时刻该 WI 的 Capsule 内含有该会话的会话记录**（the sealed capsule contains the session record）；运行时落位为各适配器的实现注记（权威布局见 `03-execution-capsule.md#§4.4`）：**Pi**（支持自定义 session 路径）运行时 session 目录即位于该 WI 的 Capsule 内（Capsule 本地会话目录，现状不变）；**Claude**（不支持自定义 session 路径）运行时使用该 harness 的**默认会话位置**（本机、Capsule 外，现状不变），运行中的会话数据读取一律指向该默认位置，并在 WI 到达终态（end-generation 收尾）时由 Daemon 把该会话的 JSONL 从默认位置**只读**复制进 Capsule——封存完整性（seal 时刻的完整现场）是本契约项的实质，不是运行时共位；采集源缺失时以 typed error（`DAEMON-513`，`04-local-runtime.md#§5.2`）失败收尾，**绝不静默**；任何适配器的会话数据都不与其它 WI 的会话数据混放 |
| subagent introspection | **可选能力**：一个 harness 适配器可以不提供对已派生 subagent 的外部可观测接口；Daemon/WI Agent 的核心流程不得依赖该能力存在 |

v1 的两条具体适配器（实现细节在 `04-local-runtime.md`）：
- **Pi 适配器**：以 `--mode rpc` 启动持久进程；session id 只在同一机器上同一个还未结束的
  generation 内因进程崩溃而续跑时保持不变；pause 终止 generation，resume 或换机器时总按
  `03-execution-capsule.md#§4.4` 建立新 session；session 目录落在 Capsule 内（Capsule-local session-dir）。
- **Claude 适配器**：支持稳定的 session id 与该 session 之上的流式（stream）交互；同一机器上
  同一个还未结束的 generation 内因进程崩溃而续跑时复用同一 session id，pause 后的 resume 则建立
  新 session（规则同上，`03-execution-capsule.md#§4.4`）；该 harness 不支持自定义 session
  路径——运行时会话数据位于其默认会话位置（本机、Capsule 外），终态由 Daemon 采集进 Capsule
  （上表「会话数据落位」行，权威布局 `03-execution-capsule.md#§4.4`）；
  对其子代理的 introspection 是否可用取决于该 harness 版本，按上表「可选能力」处理，不作为 v1
  的硬性前提。

两条适配器都必须满足 INV-6（其派生的 subagent 不得写 `runtime-state/`）与 INV-14（不得由此
衍生出「逐 step 起新 headless agent」的行为）。

### 3.7 v1 范围判据（trusted-repo native） <!-- §3.7 -->

v1 面向的是**可信内部仓库（trusted internal repo）**场景：所有 repo 均为团队自有、可信任其构建/
测试脚本不含恶意行为。在此前提下，v1 统一采用**宿主机原生（host-native）**执行，**不引入
per-step isolation**：
- Linux 与 macOS 上 WI Agent 与 harness-native subagent 均以宿主机原生进程运行（INV-12），
  两个平台在 v1 是**同一套原生执行形态**，不为其中一个平台单独引入例外。
- 不需要为 step 提供任何形式的隔离通道：这类机制的唯一存在理由是隔离不可信代码，v1 没有该
  威胁模型。
- v1 的任何范围/数据结构/API/验收章节一律统一表述为“v1 面向可信内部仓库（trusted internal
  repo），采用宿主机原生（host-native）执行，不引入 per-step isolation”，不点名、不枚举任何
  具体隔离机制或技术。**未来 untrusted runner**（例如执行外部贡献者代码）作为演进方向，仅允许
  在 `03-execution-capsule.md#§11.1` 中提及一句；本文档及其余 00–06 正文不得展开或预先讨论其
  具体形态。

## 4. 数据结构：Core 与 Capsule 状态分离 <!-- §4 -->

### 4.1 数据类归属清单（唯一归属侧） <!-- §4.1 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（持久 WI Agent、owner generation、Revision/CAS/manifest）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

| 数据类别 | 归属侧 | 存储介质 | 权威定义 |
|---|---|---|---|
| WI 记录（id/goal/content/labels/priority/status/attrs） | Core | SQLite | `02-aihub-core.md#§4.2` |
| assignee / waiting_on | Core | SQLite 列 | `02-aihub-core.md#§2.4`/§4.2 |
| owner generation（仲裁与历史） | Core | SQLite 表 | `02-aihub-core.md#§2.3`/§4.3 |
| WI 的 workflow 引用（`bundle_name` 一个 scalar；Core 不存 resolved steps 或任何解析结果） | Core | SQLite 列（`work_items.bundle_name`） | `02-aihub-core.md#§4.2`/§4.4 |
| WI 事件流（审计） | Core | SQLite 表 | `02-aihub-core.md#§4.7` |
| Capsule Revision 登记 + HEAD 指针 | Core | SQLite 表 | `05-sync-revision-recovery.md#§4`、`02-aihub-core.md#§4.8` |
| Revision 数据文件（manifest + 分块） | AIHub 本地目录（远端面） | 文件系统 | `05-sync-revision-recovery.md#§4` |
| Memory 权威内容 | AIHub 本地目录 | Markdown 文件 | `06-memory-search.md#§4` |
| Memory 元数据 / 派生 HTML / 搜索索引 | AIHub（目录 + SQLite 派生库） | 文件 + SQLite | `06-memory-search.md#§4`/§5 |
| repo inventory（检出清单与基线 commit） | Capsule 声明，Core 可见（声明随 WI），检出在 Capsule | Capsule 目录 + 声明镜像 | `03-execution-capsule.md#§4` |
| 工作树（worktree，含未提交改动，Git diff 的操作对象） | Capsule | 文件系统 | `03-execution-capsule.md#§3` |
| harness 会话数据（运行时落位按适配器区分，§3.6a：pi＝Capsule 本地会话目录；claude＝harness 默认会话位置（本机、Capsule 外）；终态会话记录由 Daemon 采集进 Capsule） | pi 运行时即在内；claude 运行时在本机默认位置，终态记录进 Capsule | 文件系统 | `03-execution-capsule.md#§4` |
| **`runtime-state/execution.json`（current step / step 结果 / review 轮次 / per-step `budget_consumed` / wall-clock 基线 / 待消费 rewind 输入（gate FAIL 判定全文）/ 归档 attempts / interactive 待问问题与回答关联；权威 schema 在 `03-execution-capsule.md#§4.2`，本文只引用；workflow 执行的唯一权威）** | **Capsule**，只由该 WI 的 WI Agent 写 | Capsule 内单一 JSON 文件 | `01-workflow-skills.md#§6`、`03-execution-capsule.md#§4.2` |
| step 执行日志、产物中间态 | Capsule | Capsule artifacts/logs 目录 | `03-execution-capsule.md#§3` |
| Daemon 自身状态（当前承载的 Job 列表、模式、机器注册） | Daemon | Daemon state 文件（本机） | `04-local-runtime.md#§4.1` |

### 4.2 状态分离不变量（INV-1 展开） <!-- §4.2 -->

- Core 的全部表与列（`02-aihub-core.md#§4` 完整 DDL）**不含**任何 `current_step`、`step_attempt`、
  `review_round` 类列；进度查询端点被定义为**代理读取**远端 Capsule 最近发布的
  `execution.json` 内容（read-through，`02-aihub-core.md#§5.2`）。
- `runtime-state/execution.json` 是 workflow 执行状态的**唯一权威**（其必含内容——per-step
  `budget_consumed`、wall-clock 基线、待消费 rewind 输入（gate FAIL 判定全文）、归档 attempts、
  interactive 待问问题/回答关联——的权威 schema 在 `03-execution-capsule.md#§4.2`，本文只引用）：
  不存在任何第二份「影子」
  执行状态（无论是 Daemon state、Core 表还是内存缓存都不得被当作可独立恢复的权威源）；崩溃后
  恢复以该文件 + 最近完整已发布 Revision 为准（`05-sync-revision-recovery.md#§7.4`）。
- 进度对外的唯一呈现：AIHub 进度 API 从「已发布的最新 HEAD」读取（数据新鲜度由 §4.3 的四类
  触发时机保证），**不在 Core 落任何进度快照列**。

### 4.3 同步触发时机（INV-11 展开） <!-- §4.3 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（持久 WI Agent、Revision/CAS/manifest、watcher dirty 位）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

全扫描发布只允许在下列四类事件（+可选 milestone）上发生；「触发者」是**发起发布请求**的一方，
「机械执行者」永远是 Daemon——不存在任何一方绕开 Daemon 直接写 Revision/HEAD 的路径：

| 触发时机 | 请求发起者 | 机械执行者 | 说明 |
|---|---|---|---|
| step 完成 | WI Agent | Daemon | 每个 step 成功/失败结束时，WI Agent 向本机 Daemon 发起一次发布请求 |
| step 中断 | WI Agent | Daemon | 基础设施失败、gate FAIL rewind、interactive step 进入 needs_input 等待（待问问题持久化到 `execution.json`，`01-workflow-skills.md#§4.3`）等中断边界 |
| WI 出最终结果 | WI Agent | Daemon | wrap / fail 时 |
| Daemon 优雅停机 | Daemon（自己） | Daemon | §3.5，代替所有仍在进行的 WI Agent 各发起并执行一次 |
| milestone（可选） | WI Agent | Daemon | workflow 定义的可选检查点，非强制 |

watcher 在这四类事件之间检测到文件变化时，**只能**把该 Capsule 标记为 dirty（一个布尔位 +
时间戳），供下一次上述事件触发扫描时决定是否需要发布；watcher 自身不得调用发布路径，也不得
定时轮询后自主发布（详细协议见 `05-sync-revision-recovery.md#§2.4`/§6，去抖动见 §2.3，快照一致性见 §2.7，
Daemon 侧的机械执行实现见 `04-local-runtime.md#§6.3`/§6.4）。

## 5. API 与错误码（模块间接口总览） <!-- §5 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（持久 WI Agent、PILOT-* 码、Revision/CAS/manifest、RunCommand、read-through、本地认证代理）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

本节只给**接口总览与权威文档指针**；每个接口的完整签名、参数与错误码在权威文档定义，对端文档
引用时逐字一致。AIHub HTTP API 的端点权威枚举是 `02-aihub-core.md#§5.1` 的 canonical
26 端点清单（计数口径一并以该节为唯一权威；行 26 依赖边为基线修订 polyforge#3 所补，理由与裁定记录在该票），本文与各文档一律引用该表、不另行枚举端点；AIHub 的对外客户端面有且只有
HTTP API 与 `pf` CLI 两个（MCP 删除，§3.1），且全部端点共用单端口（A5，§3.1）。

| 接口 | 调用方 → 被调方 | 权威定义 |
|---|---|---|
| 路由 Skill → Daemon 操作请求（执行面动词 submit/observe/answer/batch-confirm/pause/resume/wrap/ship (removed by polyforge#166: delivery is the workflows' deliver step)/ops，分流表 §5.1） | Harness → Daemon | `04-local-runtime.md#§5.1` |
| 路由 Skill / CI → AIHub 数据面动词（file/queue/cancel/recall，经 `pf` CLI 直连 HTTP，不经 Daemon） | `pf` CLI → AIHub | 本文 §5.1、`02-aihub-core.md#§5.1`（canonical 26 端点清单） |
| Workflow 选择与创建时校验/固化（提交方创建 WI 时只提交 `bundle_name`；frontmatter 校验与 digest 固化是 AIHub 创建事务内的服务端内部步骤，无独立端点，INV-8） | 路由 Skill/CLI → AIHub（`POST /api/v1/work-items`） | `01-workflow-skills.md#§4.6`、`02-aihub-core.md#§4.5` |
| Core 读写 API（允许操作集为 WI 生命周期/读操作） | WI Agent（不持有任何 Core API key 或 Revision 发布 token）→ Daemon 本地认证代理（`04-local-runtime.md#§5.3`）→ AIHub | `04-local-runtime.md#§5.3`、`02-aihub-core.md#§5.1` |
| 全扫描发布请求（内部） | WI Agent → 本机 Daemon | `04-local-runtime.md#§5.1` |
| Revision 发布 / HEAD 推进 / fencing 拒绝 | Daemon（受发布请求触发）→ AIHub | `05-sync-revision-recovery.md#§5.1` |
| 进度 read-through API | 任意客户端 → AIHub | `02-aihub-core.md#§5.2` |
| Capsule 内命令执行入口（RunCommand，宿主机原生） | WI Agent → Capsule Native runner | `03-execution-capsule.md#§5.1` |
| Memory 管理 API（写入/检索/重建） | 客户端 → AIHub（WI Agent 仍经 Daemon 的本地认证代理转发，与上行同一代理面） | `06-memory-search.md#§5.1` |
| WI Agent 存活心跳与 stale 检测 | Daemon → AIHub | `02-aihub-core.md#§4.10`、`04-local-runtime.md#§7.2` |

错误码前缀按权威文档分配，全局唯一：`01`→`WF-*`，`02`→`CORE-*`，`03`→`CAP-*`，`04`→`DAEMON-*`，
`05`→`SYNC-*`，`06`→`MEM-*`，`07`→`DEPLOY-*`/`PILOT-*`。各前缀下的具体错误码见各文档自己的错误
码表；任何文档不得在他人前缀下定义错误码。

### 5.1 路由 verbs 分流表（13 个用户动词 → 目标面，定稿裁决） <!-- §5.1 -->

> **ARCHIVED — 本表列错了动词（含从未出货的 `observe`、`batch-confirm` 与已删的 `ship`，缺 `remember`、`memory`、`watch`），承载列描述已删除的 Revision/flush/takeover 机制；do not build from this.** 现行的 13 个路由动词是 file、queue、cancel、recall、remember、memory（直连 aihub）与 submit、watch、answer、pause、resume、wrap、ops（daemon UDS 面），另有 `chat`、管理面与 doctor：以 `internal/polyforge/cli/cli.go` 的 registry、`skills/polyforge/SKILL.md` 与 README「`pf` commands」/「Daemon UDS routes」为准（repo review 2026-10-16 G-3）。

路由 skill 的 13 个用户动词 fragment（`01-workflow-skills.md#§2.1`/§3）按目标面二分；分流判据：**凡
语义上依赖本机执行现场（Capsule、WI Agent 进程）或必须由 Daemon 机械执行的 Revision 发布
（INV-11，含收尾 flush 证据 `02-aihub-core.md#§6.4`）的动词，打 Daemon 本机面；纯 Core 数据读写
（不触碰执行现场）的动词直连 AIHub，由 `pf` CLI 承载 HTTP 调用**。第 13 个动词 `ship` (removed by polyforge#166: delivery is the workflows' deliver step)
（wrap→git 交付桥，基线修订 polyforge#37 所加，方案 B 裁定：polyforge#2 全程 wrap 落定后，其
git commit 只存在于封存 capsule（detached HEAD）与 Revision 链，原有动词无一能把它推送至 git
origin，人工桥接验证可行后收编为一等动词）按同一判据落在 Daemon 本机面：ship 的语义依赖本机
封存 capsule 的现场字节（执行现场），不是纯 Core 数据读写。

| 路由 verb | 目标面 | 承载与权威 |
|---|---|---|
| `file`（创建/编辑 WI） | AIHub（直连） | `pf` CLI → `02-aihub-core.md#§5.1`（canonical 26 端点清单） |
| `queue`（列表/就绪队列查询） | AIHub（直连） | `pf` CLI → `02-aihub-core.md#§5.1` |
| `cancel`（取消非活跃 WI） | AIHub（直连） | `pf` CLI → `02-aihub-core.md#§5.1`、`02-aihub-core.md#§6.5` |
| `recall`（Memory 检索） | AIHub（直连） | `pf` CLI → `06-memory-search.md#§5.1` |
| `submit`（提交执行：claim/resume/takeover） | Daemon 本机面 | `04-local-runtime.md#§5.1` |
| `observe`（观察进度/状态） | Daemon 本机面 | `04-local-runtime.md#§5.1`/§5.1a（两层观察：本机 Capsule 走 ring 事件流，跨机兜底走 read-through，`02-aihub-core.md#§5.2`） |
| `answer`（回答 needs_input） | Daemon 本机面 | `04-local-runtime.md#§5.1`（中继报文权威见本文 §3.4a） |
| `batch-confirm`（确认 Batch wave） | Daemon 本机面 | `04-local-runtime.md#§5.1`（`mode(get_recommendation)` + `batch_submit`） |
| `pause`（暂停执行中的 WI） | Daemon 本机面 | `04-local-runtime.md#§5.1`（收尾需 Daemon 生成的 `flush_evidence`，`02-aihub-core.md#§6.4`） |
| `resume`（恢复执行） | Daemon 本机面 | `04-local-runtime.md#§5.1` |
| `wrap`（收尾） | Daemon 本机面 | `04-local-runtime.md#§5.1`（同 `pause` 的 flush 理由） |
| `ship` (removed by polyforge#166: delivery is the workflows' deliver step)（wrap→git 交付桥：把已收尾 WI 的封存 capsule 以固定名分支 `polyforge/<project>-<seq>` 推送到 git origin） | Daemon 本机面 | `04-local-runtime.md#§5.1`（语义、幂等、PR 分层与安全面权威在彼处；CLI 入口 `pf ship <wi>` 同面转发；基线修订 polyforge#37） |
| `ops`（模式切换、优雅停机等运维动作） | Daemon 本机面 | `04-local-runtime.md#§5.1`（`mode(set/drain/...)`、`shutdown()`） |

分流不改变任何动词的语义权威，也不绕过任何校验：数据面动词直连不意味着豁免 A17 权限硬边界
（§8）——`pf` CLI 与其他 HTTP 客户端一样过 AIHub 服务端的同一套鉴权；执行面动词经 Daemon 也不
放宽 Core 侧校验（Daemon 只做机械转发与本地认证代理，`04-local-runtime.md#§5.3`）。

### 5.2 基线澄清（B01 裁定；供后续 spec 形式化引用） <!-- §5.2 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（owner generation、Revision/CAS/manifest、flush 证据）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

四条已裁定的基线澄清，散点权威在各对应文档；后续 spec 形式化时必须按本节口径引用，不得重新
解读：

1. **块下载端点服务的是 Revision CAS 块**：`GET /api/v1/work-items/{id}/revisions/{seq}/blocks/{sha256}`
   （`02-aihub-core.md#§5.1` 行 17）下载的是 Revision 的内容寻址存储（CAS）块，供远端现场恢复
   （`05-sync-revision-recovery.md#§5.1`、`05-sync-revision-recovery.md#§7.4`）拉取；**bundle
   文件取回端点不存在**——Core 不存、不分发任何 bundle 文件（`02-aihub-core.md#§4.4`），执行侧从
   本机 checkout 物化（`03-execution-capsule.md#§4.5`）。
2. **登记票不可升级为执行票**：`bundle_name` 为 NULL 的登记票与执行票之间不存在 NULL→非 NULL
   的任何路径（INV-2e，`02-aihub-core.md#§4.4`）；要执行某登记票承载的内容，唯一路径是创建其
   子票（epic-lite，`02-aihub-core.md#§4.2a`）——子票可携带 workflow 引用三列成为执行票，父票保持
   登记票不变。
3. **end-generation 是四 reason 共用端点**：`POST /api/v1/work-items/{id}/end-generation`
   （`02-aihub-core.md#§5.1` 行 9）以 body 的 `reason ∈ {pause, block, wrap, fail}` 区分四条结束活跃
   generation 的迁移，共用同一端点与同一套校验；**数据发布不经过本端点**——本端点只结束
   generation，任何数据仍先经同步端点（块上传 + manifest，`05-sync-revision-recovery.md#§5.1`）
   发布，`flush_evidence`（`02-aihub-core.md#§6.4`）校验的正是「最后一次发布已完成」。
4. **on_fail rewind 仅由业务 gate FAIL 触发**：有界 on_fail rewind（`01-workflow-skills.md#§6.2`）
   的唯一触发条件是业务 gate FAIL；基础设施失败的处置是当前 step 原地 resume
   （`01-workflow-skills.md#§6.3`），与 on_fail 无关——两者互不越界（INV-9），不存在「infra 失败也
   走 rewind」的路径。

## 6. 核心流程（端到端概述） <!-- §6 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（owner generation、Revision/CAS/manifest、Interactive/Batch/Drain、read-through）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

以下用编号步骤描述端到端主流程的责任分工（细节均在各权威文档，本节不重复接口签名，也不作为
可执行伪代码使用）：

0. **（claim 之前，随 WI 创建事务一次完成）** workflow 的选择→frontmatter 校验在
   WI 创建事务内由 **AIHub Core 服务端完成**（`01-workflow-skills.md#§4.6`、`02-aihub-core.md#§4.5`）：
   AIHub 读本机 checkout 内该 bundle 的 workflow.md **frontmatter** 做校验，
   **不解析正文、不落任何解析结果**；随创建事务落库的只有 `bundle_name`
   一个 scalar 与 `repo_inventory`（`02-aihub-core.md#§4.6`；digest 列已删，
   polyforge#149 B9）。`bundle_name` 为**首次认领冻结**（INV-8，R2 窗口）：首次认领之前（queued 且
   从未认领）仍可经 `PATCH` 三向变更（NULL→X / X→Y / X→NULL 解绑），认领后冻结，变更 workflow 引用
   的唯一路径是取消当前 WI 并新建；`repo_inventory` 维持创建事务冻结。
1. 用户在 Harness 中表达意图；路由 Skill 把意图映射为对本机 Daemon 的一次操作请求（`00-architecture.md#§3.1`）。
2. Daemon 按 Interactive/Batch/Drain 判据（`00-architecture.md#§3.4`）决定是否创建 Job；claim 请求与
   Job 记录只携带 WI 记录中的 `bundle_name`；**claim 响应携带 `bundle_name`**
   （`02-aihub-core.md#§8` 执行面规则——机器不做任何 claim 前的数据面读取即可完成执行路径）——Daemon 不查询、不拉取任何 resolved steps
   （Core 中不存在该数据，INV-8）。Daemon 向 AIHub 申请 owner
   generation（`02-aihub-core.md#§5.1`），创建该 WI 的 Capsule（含 worktree/repo inventory 检出，`03-execution-capsule.md#§4.2`），
   物化 bundle 并启动**Daemon 内的 Go driver**（`00-architecture.md#§3.6`/§3.6a，每 WI 一份）。
3. **Go driver**是执行引擎：从本机 checkout 物化 `bundle_name` 指向的 bundle（`steps/*.md`
   与 `workflow.md` 快照，`03-execution-capsule.md#§4.5`；不做 digest 校验——校验链已删，polyforge#149 B9），加载
   workflow 图后按 step 图逐个**派生短驻 harness step 进程**（envelope 一次一步，`01-workflow-skills.md#§6`）：
   每个 step 进程把结果信封写入 capsule，driver 校验后单一 transition 推进
   `runtime-state/execution.json`（`03-execution-capsule.md#§4.2`）；
   needs_input 的 step 在 Interactive 模式下等待用户通过路由 Skill 的 answer 动作作答
   （`00-architecture.md#§3.4a`）。
4. 每次 transition 提交即产生一次 §4.3 定义的发布（driver 为唯一写者，Observe 的 C3 引擎机械
   执行全扫描发布，`05-sync-revision-recovery.md#§5`/§6）。
5. gate FAIL 触发有界 on_fail rewind（`01-workflow-skills.md#§6.2`）；基础设施失败在当前 step 原地 resume
   （`01-workflow-skills.md#§6.3`）；两者互不越界（INV-9）。
6. WI 出最终结果（wrap/fail/blocked）时，driver 的终局接线把工作流终态翻译为 WI 的关闭
   （`04-local-runtime.md#§7`），终态版本随最后一次 C3 发布上链，并把最终结果写入 Core
   （`02-aihub-core.md#§6`）。
7. 任意客户端查询进度：AIHub 从 Core 记录的 HEAD 指针 read-through 读取对应 Revision 的
   `execution.json` 内容并返回（`02-aihub-core.md#§5.2`），不经过任何 Core 落库的进度列。

## 7. 并发与崩溃恢复（总则） <!-- §7 -->

- **单写者域**：Core 只被单 active Service 写（INV-2）；`runtime-state/execution.json` 只被该
  WI 的 Daemon 内 Go driver 单一 transition 入口写（INV-3 + INV-6）；Revision/HEAD 只被 Daemon 机械写入（INV-11）；两者之间
  的并发面只剩「transition 提交 → C3 机械发布」这一个接缝，由先数据后状态（INV-4）+ fencing
  （INV-5）收敛——这是 vNext 全系统唯一需要分布式一致性的位置。
- **崩溃恢复分层**：driver/step 进程崩溃 → Daemon 的恢复引擎基于 `execution.json` 与 spawn
  journal 重新派生 driver 续跑（序号栅栏防重放）（`04-local-runtime.md#§7`、`03-execution-capsule.md#§7`）；同步中断 → 下一次触发时机的全扫描对账收敛
  （`05-sync-revision-recovery.md#§7.2`）；takeover 竞态 → fencing（`05-sync-revision-recovery.md#§2.9`）+ 从远端 HEAD 恢复现场（`05-sync-revision-recovery.md#§7.4`）。各文档
  的并发/崩溃细节不得引入上述三类之外的恢复路径。

## 8. 安全边界（总则） <!-- §8 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（持久 WI Agent、owner generation、Revision/CAS/manifest、takeover、rotate-secret、本地认证代理）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

- 信任面：Harness 用户（经路由 Skill）→ Daemon（本机，全权，唯一持有 AIHub 凭证与 Revision
  发布 token 的角色；**Revision 发布权仅属 Daemon**）→ WI Agent（本机原生进程，受 §3.7 v1 判据
  约束，host-native，不引入 per-step isolation，**不持有任何 Core API key 或 Revision 发布
  token，只通过 Daemon 的本地认证代理调用允许的 WI 生命周期/读操作**）；Daemon → AIHub
  （HTTP，经机器身份 + WI 级 owner generation 凭证）；WI Agent → AIHub 仅能通过 Daemon 的
  本地认证代理（`04-local-runtime.md#§5.3`，允许操作集为 WI 生命周期/读操作，不含 Revision 发布）中转；
  harness-native subagent → 仅可写该 WI 的 worktree 与 artifacts 目录（INV-6 限定其不得写
  `runtime-state/`，也不得读写 Daemon 凭证与 Daemon state）——在 host-native 同 UID 形态下，
  这一限定由权限位与路径不暴露实现，是**防误写**措施，**不是安全边界**，不得声称机制级隔离
  （B02 裁定；v1 的安全边界建立在「仓库可信」前提上，见本节末条）。
- 路径安全：Capsule 内各目录的读写面（worktree、`runtime-state/`、Capsule 本地会话目录、
  artifacts/logs）按 §4.1 的归属唯一分配，subagent 的可写路径集合是该分配表的严格子集；该路径级
  保护在 host-native 同 UID 形态下同为防误写措施（权限位 + 路径不暴露），不是安全边界，不构成
  机制级隔离（B02 裁定）。权威的路径级判定与凭证隔离细节见 `03-execution-capsule.md#§5`/§6、
  `04-local-runtime.md#§8`。
- 最小权限：Core API 按 WI 凭证限定到该 WI 范围（`02-aihub-core.md#§5.1`）；Daemon 派生 WI Agent 时不注入
  任何超出该 WI Agent 所需范围的凭证。
- **权限硬边界（A17，AIHub 层定稿裁决）**：鉴权与授权的唯一执行点在 AIHub 服务端——api_key
  （身份）→ WI 级 generation capability token（有活跃 generation 时）→ 状态机合法性的三重校验，
  敏感操作（`force-takeover`/`budget-increments`/`rotate-secret`）按 `users.role` 授权
  （`02-aihub-core.md#§8`/§5.1）。HTTP API 与 `pf` CLI 是 AIHub 仅有的两个客户端面（§3.1），
  二者在同一端口（A5）、同一鉴权面下完全等价：`pf` CLI 不携带、不放大任何额外权限；MCP 面
  不存在，因此不存在第二条绕过该硬边界的特权通道。Daemon 的本地认证代理
  （`04-local-runtime.md#§5.3`）只做转发，不做任何授权决定，不放宽 AIHub 服务端的任何校验。
- v1 面向 trusted internal repo，采用 host-native 执行，不引入 per-step isolation（裁定见
  §3.7、INV-12），这不构成 v1 的安全边界组成部分；v1 的安全边界建立在「仓库可信」这一前提
  假设上，该假设及其失效后的应对方向在 `03-execution-capsule.md#§11.1` 讨论。

## 9. 监控（总则，指标分层） <!-- §9 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（持久 WI Agent、Revision/CAS/manifest、Interactive/Batch/Drain、watcher dirty 位、read-through）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

| 层 | 指标类别 | 权威细节 |
|---|---|---|
| AIHub | 单 active 心跳、Core 写延迟、read-through 进度查询延迟、Revision 发布成功率 | `02-aihub-core.md#§9` |
| Daemon | 承载 Job 数、持久 WI Agent 存活率、当前模式（Interactive/Batch/Drain）、收到的发布请求数与机械发布延迟、优雅停机耗时 | `04-local-runtime.md#§9` |
| Sync | dirty 标记数、请求→机械发布的延迟、reconciliation 差异数、fencing 拒绝数、HEAD 回退事故（须为 0） | `05-sync-revision-recovery.md#§9` |
| Workflow | step 时长、rewind 次数（须 ≤ 上界）、resume 次数、预算命中率、needs_input 等待时长 | `01-workflow-skills.md#§9` |
| Capsule | Native runner 启动失败率、`runtime-state/` 写拦截次数、harness 适配器启动失败率 | `03-execution-capsule.md#§9` |
| Memory | 写入→可检索延迟、索引重建时长 | `06-memory-search.md#§9` |

## 10. 测试与故障注入（总则） <!-- §10 -->

每份文档的测试小节须覆盖：核心流程行为验证、跨文档接口契约的一致性核对、以及本域可注入故障
清单。**交叉引用机械校验**（修复 B12/B20）：`docs/design/polyforge-vnext/tools/check-xrefs.py`
运行时无参数，扫描本目录全部八份文档中每条 `` `NN-name.md#§X.Y` `` 或 `` `NN` §X.Y `` 形式的
交叉引用，逐条验证目标文件存在且目标文件内存在一个以该节号开头的标题；发现悬空引用则非零退出并
逐条打印。该脚本 MUST 接入 `07-deployment-pilot.md#§10` 的 CI 流水线，作为文档提交前的
必经门栋。全局故障注入类别基线：进程崩溃、断网、磁盘满、并发双会话操作同一 WI、过期凭证、目录不可
写/被删、SQLite 损坏、时间回拨。各文档按职责裁剪并在自己 §10 列出，其中 Daemon 与 WI Agent的
持久化生命周期（崩溃续跑、pid 复用防护）与全扫描发布的机械执行路径是 `04-local-runtime.md#§10` 的核心测试面。

## 11. 迁移与 rollout（总则） <!-- §11 -->

一期为**从无到有**的新系统引入：安装 aihub（SQLite + 本地目录）→ 注册 Daemon 机器 → 引入参考
workflow bundle → 小范围 Pilot（回归清单与否决门槛见 `07-deployment-pilot.md#§9`/§10）。**迁移总则
（B03 裁定，精确化）**：不迁移 workflow-v2 的执行状态 / invocation / episode / retry / supersede
状态机（见 §1.2 非目标）；**Memory 是唯一必须迁移的存量**——现有 aihub memory 一次性导出为
Markdown 权威文件，权威流程在 `06-memory-search.md#§10`（管理 CLI 直写文件、不走 API、一次
性）；除 Memory 外不存在任何第二类必须迁移的存量数据，与 `07-deployment-pilot.md#§2.1` 的并存
与切换叙述一致。安装/升级/回滚的权威细节在 `07-deployment-pilot.md#§6`。

演进方向（非一期承诺，且不得提前进入一期依赖面 INV-10/INV-12）：多 active AIHub 需要先解决 Core
写者仲裁的分布式化；远端对象存储（S3/JuiceFS）可替换 Revision 数据文件与 Memory 文件的落盘层
而不改变协议；untrusted runner 层（具体裁定见 `03-execution-capsule.md#§11.1`）用于执行不可信
代码场景。

### 11.1 旧 skill 与散装 agent 定义的迁移矩阵（唯一处置权威） <!-- §11.1 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（持久 WI Agent、pf upgrade）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

vNext 不继承现行 polyforge 的任何 `/pf-*` skill 入口或散装 agent 定义；唯一处置如下（本表是唯一
权威，部署面不得另立去向）：

| 旧入口 | vNext 去向 |
|---|---|
| pf-spec / pf-plan / pf-retro | 不再是独立 skill：对应能力以 workflow bundle 内 step skill（`workflows/<bundle>/agents/*.md`）表达（参考流程见 `01-workflow-skills.md#§4.2`），由 WI Agent 第 0 步物化进 Capsule 后派生（`03-execution-capsule.md#§4.5`） |
| pf-crystallize | 删除：不再提供自动结晶；skill/bundle 的沉淀与版本化一律走 git + PR（bundle 与 agent 定义本身即仓内文件，`workflows/`） |
| pf-revise | 删除：spec/plan 的批注修订回路由 grill-review 的有界 on_fail rewind 取代（`01-workflow-skills.md#§6.2`）——修订即 rewind 重跑对应 step，不另立批注解析通道 |
| pf-project / pf-user / pf-sync | 由 `pf` 管理 CLI（`cmd/pf/`，§3.3）承载 |
| pf-release | vNext 发版 = release step 钩子（`01-workflow-skills.md#§4.2`）：无独立发版模型与 CLI 命令；平台自身的发版是其 bundle 内 release step 的一个实例（R1/R3 裁定），`pf upgrade` 仅是部署管道 |
| pf-init / pf-doctor / pf-update | 由 `pf` doctor 与 ops 动词（安装/升级/回滚，`07-deployment-pilot.md#§6`）承载 |
| using-polyforge | 删除独立 skill：会话启动与路由背景并入路由 skill（用户 harness 唯一安装面，§3.1） |
| worker764 / wi708-* / composer-* 等散装 agent 定义 | 全部废弃：step agent 一律来自 bundle 的 `agents/*.md`，经 Capsule `.pi/agents` / `.claude/agents` 物化机制派生（`03-execution-capsule.md#§4.5`）；不维护任何 capsule 之外的散装 agent 定义 |

`skills/` 目录只保留一个 skill：`skills/polyforge/`（路由 skill，§3.1）——原执行 skill 已随
WI-Agent 引擎删除（polyforge#106）；参考
workflow bundle 的源目录为 `workflows/`（§3.3、`01-workflow-skills.md#§3`）。

## 12. 未决问题（第 1、3 项已在本次修订中裁定，不再是开放项） <!-- §12 -->

本文档层面未决与已裁定记录（各域技术未决问题在对应文档 §12 列出，此处只列跨文档级；第 1、3 项
已裁定，保留为裁定记录）：

1. **（已裁定，不再列为未决）多项目（multi-project）是功能面**：project 是 Core 数据模型的一等
   维度，多 project 支持是 AIHub 的**功能面**而非架构假设——「一期先按单 project 使用」的假设句
   自本裁定起删除；项目级隔离粒度随功能面演进，不进入全局不变量，也不与任何已决定义冲突。
2. **路由 Skill 的多语言（中/英）意图映射维护策略**：一期中文优先，映射表版本化；开放。
3. **（已裁定，不再列为未决）Batch wave 的默认并发上限**：缺省 `max_concurrent_jobs = 4`，
   可经 `mode(set, max_concurrent_jobs=<n>)` 覆盖（`n ≥ 1`）；权威裁定与实现细节（含
   `batch_submit` 超上限的顺序排队递补、Drain 循环同受该上限约束）在
   `04-local-runtime.md#§12` 第 0 项。该上限只约束 wave 内**同时派生**的 Job 数，不改变
   §3.4b「wave 边界由人给定」的裁定。
4. **Claude 适配器的 subagent introspection 何时补齐为强能力**：一期按 §3.6a 视为可选能力，
   是否及何时把它提升为两条适配器的共同硬性要求待定；不得因等待该能力而阻塞 v1 交付。
