# 03 — Execution Capsule：身份、repo inventory 与 Native Runner Profile

> **ARCHIVED 2026-10-16** (repo review 2026-10-16 wave 4): describes the v0/v1 design and is kept for history only — do not build from it. Current reference: [reference.md](../reference.md); decisions: [v2-converge.md](../v2-converge.md). Live mechanisms this file still held were moved there before archiving; section anchors are unchanged.

> **ARCHIVED（repo review 2026-10-16 G-1）— 本文大部分描述已删除的机制：WI Agent、RunCommand 与 `CAP-*` 码、Revision 落盘、`capsules/_quarantine/`（代码为 `*.quarantined.<ts>`）、`.pi/agents/` 等物化目标。标有 ARCHIVED 横幅的节 do not build from this；未标横幅的节也只作历史与溯源参考。现行设计权威：[v2-converge.md](../v2-converge.md)、[wi-kind-contract.md](../wi-kind-contract.md)、[08-capsule-base-migration.md](../08-capsule-base-migration.md)；与代码冲突时一律以代码为准。**
> Most of this document describes deleted mechanisms. Sections bannered ARCHIVED must not be built from; the live authority is v2-converge.md plus the code.

| 权威项 | 值 |
|---|---|
| 文档定位 | Capsule 身份与目录、repo inventory、`runtime-state/execution.json`、v1 Native runner profile、Subagent 写限制不变量、subagent 定义文件与 workflow 快照物化（§4.5）、多机副本裁定、session transcript 归档（§4.4） |
| 上游基线 | `00-architecture.md#§2.3`（INV-1/INV-3/INV-6/INV-12）、§3.6（Harness RPC WI Agent）、§3.7（v1 判据）、§4（Core/Capsule 状态分离） 〔#111分类：删→驱动〕 |
| 术语权威 | 本文负责：Capsule、capsule 实例、repo inventory、`runtime-state/execution.json`、Native runner profile、Subagent 写限制（状态面落地）、subagent 定义文件物化与防冲突（§4.5）、多机副本裁定、session transcript 归档固定位置与按 end-generation 一次性发布（§4.4） |

> **执行模型勘误（polyforge#111 删除 pass，2026-10-09）**：Phase-3 引擎切换（`d9bb866`/`f2163fe`）后，默认执行模型是
> Daemon 进程内的 **Go 执行驱动**（下称「驱动」，`internal/polyforge/driver`，`-use-driver` 旗标现已删除，driver 是唯一引擎）：
> 驱动是 `runtime-state/execution.json` 的单写者，answers 由驱动自身消费（`POST /jobs/{wi}/answer` →
> bridge Handle.Answer → 驱动的归档并清空 `pending_question` 的单次原子写——**无任何 agent ack**），
> 崩溃恢复 = Daemon 从 `execution.json` 重新推导驱动，不存在「新派生的 WI Agent 进程」。WI-Agent 派生进程
> 旧引擎及其 `-use-driver=false` 开关均已删除。本文写于 WI-Agent 模型，正文不整篇重写；
> 每处「WI Agent」引用按下列标记分类（行尾标记，可机械检索 `#111分类`）：
>
> - **〔#111分类：已重写→驱动〕** —— 本 pass 已把该行改写到驱动架构（仅限 §4.2/§7 的实现级教学段）。
> - **〔#111分类：删→驱动〕** —— 该行所述的 WI Agent 进程机制在默认引擎下已删除，职责由驱动承接；
>   按「执行者 = 驱动」读，裁定本身仍有效。
> - **〔#111分类：替换后仍成立〕** —— 该行陈述在「执行者 = 驱动」替换下逐字仍成立。
> - **〔#111分类：历史记录〕** —— 引用裁定/历史文档的原文或历史命名，按原样保留，不代表当前执行模型。
>
> **未清理声明**：本 pass 只覆盖 02/03/05 三文；`00-architecture.md`（86 行）、`01-workflow-skills.md`（27 行）、
> `04-local-runtime.md`（100 行）、`07-deployment-pilot.md`（22 行）仍携带未分类的 WI-Agent 引用，
> 不属于 polyforge#111 的验收范围，需要各自的后续 pass。

## 1. 范围与非目标 <!-- §1 -->

### 1.1 范围 <!-- §1.1 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（owner generation、Revision/CAS/manifest、RunCommand）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

- Capsule 权威定义：标识符格式、与 WI 的绑定关系、生成时机、生命周期（创建/复用/销毁，§2）。
- Capsule 目录布局（§3）。
- session transcript 归档的固定位置约定（A14 裁定）：`logs/session/<generation>.jsonl`、**每次 end-generation（pause/block/wrap/fail）无例外按代一次性归档发布**、不逐边界重传；claude 适配器（无自定义 session 路径）的运行时 transcript 位于 harness 默认会话位置，终态由 Daemon 只读采集进 Capsule，采集缺失为 `DAEMON-513` typed error（§4.4；同步侧确认见 `05-sync-revision-recovery.md#§4.1`/§6.1）。
- Capsule 内 harness 适配器 subagent 定义目录（`.pi/agents/`、`.claude/agents/`）与 workflow 执行图快照（`workflow.md` 或 `workflow/`）：第 0 步物化、subagent 定义文件模型、防冲突三层，及其不进入同步根的边界（§3.1/§4.5；同步侧排除由 `05-sync-revision-recovery.md#§4.1` 落实）。
- repo inventory 的声明格式、检出流程、基线 commit 记录（§4.1/§4.3）。
- `runtime-state/execution.json` 的完整文件格式：`current_step`、step 尝试历史（归档 attempts 列表）、step 级与 WI 级预算消耗、step 起始基线（墙钟基线与 diff 基线：每个 inventory 项的起始 HEAD/脏集）、待消费 rewind 输入（gate FAIL 判定全文）、interactive 待问问题、review 轮次、`workflow_status` 取值集合，与 `01-workflow-skills.md` 的 workflow 语义逐字一致（§4.2）。
- **v1 唯一执行 profile：Native runner**（宿主机原生进程，Linux/macOS 同一形态）：v1 trusted internal repo，host-native，不引入 per-step isolation；所需文件系统读写权限、通过普通文件系统权限落实 INV-6（harness-native subagent 不得写 `runtime-state/`）（§5/§6.2）。
- 命令执行入口 `RunCommand` 的调用面与 `CAP-*` 错误码（§5）。
- Revision 恢复落盘（`apply_ops`/`apply_full_replace`）时防 symlink 路径逃逸的安全规则（历史缺口 B07 的修复，§6.3）。
- 多机 Capsule 副本并存裁定（§2.4）。

### 1.2 非目标（v1 判据的直接落实，逐字对齐 `00-architecture.md#§3.7`/INV-12） <!-- §1.2 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（持久 WI Agent、Revision/CAS/manifest、RunCommand）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

- **v1 执行边界**：v1 面向 trusted internal repo，采用 host-native 执行，不引入 per-step isolation；未来扩展只在 §11.1 记录一句方向，不进入本文任何范围、数据结构、API 或验收条款。
- WI Agent 与 subagent 直接使用宿主机原生网络、工具链和运行环境；workflow schema 与 `RunCommand` 不承载隔离策略字段。 〔#111分类：替换后仍成立〕
- 不设计 Capsule 内 workflow 执行语义（`01-workflow-skills.md#§6` 权威）。
- 不设计 Revision 的产生节奏与发布协议（`05-sync-revision-recovery.md#§4`/§5 权威；本文只定义被同步观察的目录集合与恢复落盘的安全规则）。
- 不设计 Windows 及其他非 host-native 的一期 Runtime 形态。
- 不设计 Daemon 自身状态存储（`04-local-runtime.md#§4.1` 权威；本文只定义 subagent 对它的不可见性）。

## 2. 术语与不变量 <!-- §2 -->

### 2.1 Capsule（权威定义） <!-- §2.1 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（持久 WI Agent、owner generation、flush 证据、takeover）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

**Capsule（执行舱）**：一个 WI 在某台机器上的完整执行现场——工作树、执行状态文件与产物的目录集合（§3 布局）。Capsule 是执行态的唯一权威载体（INV-1 的另一面）。

- **标识符格式**：`capsule_id = "cap_" + <wi_id>`。与 WI **一对一终身绑定**：同一 WI 永远对应同一个 `capsule_id`，不随接管（takeover）或跨机器迁移改变——「同一个 WI 同一个 Capsule」是数据模型事实，不是实现细节。
- **capsule 实例（instance）**：Capsule 在某台机器某一代 owner 手下的**运行化身** = `(capsule_id, owner_generation, machine_id)`。同一 `capsule_id` 的多个实例不同时活跃（§2.4）。
- **生成时机**：generation g 被认领后、该机器首次派生 WI Agent 前，由 Daemon 创建（`04-local-runtime.md#§6.1`）；若目录已存在（接管/本机重启恢复场景），按 §6.1 的复用/隔离判定处理。 〔#111分类：删→驱动〕
- **复用时机**：同一 generation 对同一目录的重复创建请求（幂等重试）直接复用已完整初始化的目录，不重新检出、不重新物化。
- **销毁（封存）时机**，按触发路径分两类，二者的收尾动作与顺序均不同：
  - **正常收尾（pause / wrap / fail / block——四者均按 `02-aihub-core.md#§6.4` 携带 flush 证据；cancel 不在此列，见下）**：仅在本 generation 仍是当前唯一活跃 generation 时发生。收尾顺序是**先发布、后结束 generation**：WI Agent 发起最后一次全扫描发布请求，Daemon 执行该次发布并等其落定（成功写入，或确认本次确无待发布内容）之后，该 generation 才在 Core 侧被记为已结束（对应 `02-aihub-core.md#§6.4` 的 flush 证据协议，本文不重复其判据）；发布未落定前不得结束 generation。Daemon 随后停止观察该 capsule，目录保留为只读现场。 〔#111分类：删→驱动〕
  - **stale 检测或被 force-takeover**：旧 generation 已不再被信任为可发布方，**不发起、也不重试任何发布**——不存在「先尝试发布再看结果」这一步。Daemon 一旦判定本 generation 已 stale（心跳过期）或已被接管（收到 `05-sync-revision-recovery.md#§2.9` fencing 拒绝，或另行获知新 generation 已 claim），立即终止该 capsule 的进程组，并将本机目录封存进 quarantine（协作行为，见 §2.4）；新 generation 一侧则独立地在 `capsule_create`（§6.1）时把旧目录移入 quarantine，不依赖旧侧是否已完成自己的封存动作。
  - **cancel 的三类路径（D13 裁定；cancel 是 Core 侧把 WI 迁入 `canceled` 终态的动作，权威契约在 `02-aihub-core.md#§6.5`，Capsule 侧按下述分类各自处理，三类之外不存在第四种混合路径）**：
    1. **WI 处于 `queued`/`paused`/`blocked`（无活跃 generation）**：人工直接经 Core 的 cancel 端点取消（`02-aihub-core.md#§6.5`），不需要任何活跃 generation token，也**不存在 flush**——本就没有活跃代，没有「先发布、后结束 generation」的对象。Capsule 侧若存在本机封存目录（`paused` 场景），保持只读现场，其后按删除保护流程清理。
    2. **WI 处于 `running`（有活跃 generation）**：Core 的 cancel 端点不作用于 running WI（`02-aihub-core.md#§6.5`）；owner 必须先走上面的正常收尾路径——先 flush 落定、generation 结束、WI 转 `paused`——再由人工按第 1 类取消，即「**先 flush 后 cancel**」，不存在跳过 flush 直接取消 running WI 的路径。
    3. **generation 已 stale 或已被 takeover**：走上面的第二类路径——kill 进程组 + 本机 quarantine，**不发布**（零 flush）；WI 的最终终止由新 generation 或人工在 Core 侧另行决定，旧代的本机目录不参与任何 flush。
  两类路径封存后目录均保留（只读现场），等待恢复，或按 `05-sync-revision-recovery.md#§2.10` 删除保护流程清理。（基线修订 polyforge#37）封存目录对 ship 交付桥路径 (removed by polyforge#166: delivery is the workflows' deliver step)（`04-local-runtime.md#§5.1`）保持**只读可达**：ship 从封存 capsule 的 `repos/` 工作树读取封存时 HEAD 的字节并推送到 git origin，不写 capsule 内任何路径——「只读现场」的写禁令对 ship 同样成立，ship 不构成封存的任何写例外通道。「销毁」在 v1 指封存+移出活跃集合，不做磁盘删除；磁盘删除只经删除保护流程。本机 quarantine 只隔离**本机**目录，不影响旧 generation 若运行在另一台机器上时该远端机器本地目录的状态（该状态完全取决于那台机器上 Daemon 自己的收尾行为，见 §2.4）。

### 2.2 与 WI 的绑定关系 <!-- §2.2 -->

> **PARTIALLY ARCHIVED（2026-10-16）— `capsule.json` 作为 capsule 身份文件是现行机制，但形状以 `internal/common/capsule/capsule.go` 的 `Identity` 为准：`work_item_id`、`capsule_id`、`project`、`bundle_name`、`repos`（认领时各任务仓库 SHA）、`created_at`。**已删除、do not build from this 的部分**：`generation`/`machine_id`/`status` 字段、`CAP-409`、WI Agent 与本地认证代理的凭证面。

`capsule.json` 是 Capsule 自我描述的唯一权威文件：

```json
{ "capsule_id": "cap_wi_793", "work_item_id": "wi_793", "generation": 3,
  "machine_id": "m-01", "status": "initializing|ready",
  "created_at": "2026-09-20T08:00:00Z" }
```

- `generation` 与 `02-aihub-core.md#§2.3` 的 owner generation 行一致；请求携带的 generation 与本地 `capsule.json` 不匹配时由 Daemon 拒绝（`CAP-409`，§5.2）。
- **凭证面（D3 裁定，逐字对齐 `00-architecture.md#§5` 与 `04-local-runtime.md#§5.3`/`#§8`）**：**WI Agent 与 subagent 均不持有任何 Core API key（AIHub API key）或 Revision 发布 token/凭证**——WI Agent 对 Core 的全部访问一律经 Daemon 本地代理（`04-local-runtime.md#§5.3`），由 Daemon 用自己持有的凭证代为签名；一切 Revision 发布由 Daemon 机械执行（`04-local-runtime.md#§6.4`），WI Agent 只能发 `publish_request` 请求。Capsule 目录内不落任何凭证字节。口径澄清（B02 威胁模型，详见 §5.2/§6.2）：v1 无网络隔离，Capsule 内命令**网络可达** AIHub 端点，原表述「不可达 AIHub API」的正确含义是**凭证不可达**——无凭证即无法通过 Core 认证，这是凭证不持有的事实结果，不是网络阻断。同步是 Daemon/WI Agent 的职责（`05-sync-revision-recovery.md#§5`）；这使「Capsule 泄漏」的影响面收敛到本机工作区（无任何凭证可随目录泄露）。 〔#111分类：删→驱动〕

### 2.3 本文新增不变量 <!-- §2.3 -->

- **INV-6a**（INV-6 的状态面落实）：`runtime-state/`、`sync/`、Daemon state、AIHub 凭证对 harness-native subagent **不可写**；`runtime-state/execution.json` 对 subagent 只读可见（用于自查进度），其余三类完全不可见（§6.2 面表）。
- **INV-6c**（新增，本文档裁定）：Capsule 内一切路径解析（无论是 subagent 命令的 cwd/argv，还是 Revision 恢复落盘）必须逐级检查父目录是否为 symlink 并拒绝跟随；跨 Capsule 根的 symlink 目标一律拒绝创建（§6.3，修复历史缺口 B07）。

### 2.4 多机 Capsule 副本裁定（本文裁定；与 `05-sync-revision-recovery.md#§2.9` 一致） <!-- §2.4 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（持久 WI Agent、owner generation、Revision/CAS/manifest、takeover）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

**裁定：不允许并存。** 任一时刻一个 WI 至多一个活跃 capsule 实例，由 owner generation 唯一活跃（`02-aihub-core.md#§4.3`）+ fencing（`05-sync-revision-recovery.md#§2.9` takeover fencing）在存储与发布两层强制。**这两层强制的是“过期结果无法生效”，不是“过期进程立即停止”**——后者只是本地協作行为（见下段）。本机 quarantine 机制仅处理**本机**目录：接管后，旧 generation 在**新 generation 所在那台机器上**的代目录（若与旧代同机）一律被新 generation 代的 `capsule_create`（§6.1）**隔离（quarantine）**到 `<pf-workspace>/capsules/_quarantine/<wi_id>/<old_generation>-<ts>/`，不在原地复用；新代总是从干净目录开始并按 `05-sync-revision-recovery.md#§7.4` 从远端 HEAD 完整重建现场（不信任任何本地残留）。若旧 generation 运行在**另一台机器**上，本机 quarantine 对那台远端机器本地目录**不构成任何隔离**：那台机器的旧目录能否及时封存完全取决于旧 Daemon 自己的协作行为（见下段），不在本机 quarantine 机制的控制范围内。隔离目录仅可人工经删除保护流程清理（与远端数据同一删除理念，`05-sync-revision-recovery.md#§2.10`）。详细隔离序列见 §6.1。

发布权（谁能把 Revision 推进 HEAD）与本地执行（旧进程是否立即停止）是两件事，INV-3 只保证前者：`05-sync-revision-recovery.md#§2.9` fencing 保证过期 generation 的任何发布尝试均被拒绝；旧 WI Agent/subagent 可能在接管后仍继续运行一段时间（**尤其跨机时：新 generation 无法直接作用于另一台机器上的进程**），但其结果无法进入 Core/Revision。旧 Daemon 一旦判定自身已 stale 或已被接管（判定依据同 §2.1：心跳过期、收到 fencing 拒绝，或另行获知新 generation 已 claim），**不发起、也不重试任何发布**，直接终止该 capsule 的进程组并封存本地现场（协作行为，非安全不变量本身；安全不变量由发布权单独承载，`05-sync-revision-recovery.md#§2.9`）。 〔#111分类：删→驱动〕

## 3. 模块与代码目录 <!-- §3 -->

### 3.1 Capsule 目录布局（权威） <!-- §3.1 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（持久 WI Agent、owner generation、Revision/CAS/manifest）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

```
<pf-workspace>/capsules/<project>-<seq>/    # capsule_id = cap_<wi_id>；目录名用可读的 <project>-<seq>（polyforge#144，此前为 wi_id）。
                                            # <pf-workspace> 由共享配置 cli.toml 的 [workspace."<path>"] 映射
                                            # 唯一决定（polyforge#113；每节列出落点在该 workspace 的 projects，
                                            # 按被认领 WI 的 project 逆映射路由）。不存在启动期 workspace
                                            # 旗标或环境变量（旧 -workspace / POLYFORGE_WORKSPACE 已删除，
                                            # 传入即解析失败）；零个 [workspace] 节时 Daemon 拒绝启动。启动时
                                            # 校验每个 workspace 目录存在（Daemon 绝不代建它）且
                                            # <workspace>/capsules/ 可创建、可写；Daemon 在 workspace 内只写
                                            # capsules/ 子树。迁移见 08-capsule-base-migration.md §1。
├── capsule.json                # 身份清单（§2.2）
├── repos/<repo_name>/          # inventory 检出的完整工作树（源文件本身，subagent 可读写，§4）
│   └── inventory.json          # 检出快照（§4.3），与源文件同级，subagent 只读
├── runtime-state/
│   └── execution.json          # 唯一执行权威（§4.2）；WI Agent 独占写，subagent 只读（§6.2） 〔#111分类：删→驱动〕
├── artifacts/                  # step 产物（subagent 可读写）
├── logs/                       # step 执行日志（subagent 追加写）
│   └── session/                #   session transcript 归档固定位置（§4.4 A14 裁定）：每次 end-generation（pause/block/wrap/fail）由 Daemon 复制本代会话 JSONL 为 logs/session/<generation>.jsonl，随该代最后一次 Revision 一次性发布
├── .pi/agents/                 # pi 适配器 subagent 定义（§4.5）；capsule 创建时必须建立、可为空——pi 从 cwd 向上找最近 .pi/agents，不建会把定义解析面向上泄漏到 capsule 之外（§4.5）
├── .claude/agents/             # claude 适配器 subagent 定义（按需建立，§4.5）
├── workflow.md                 # 本 WI 的执行图快照（§4.5；或以 workflow/ 目录形态存放，同一 capsule 内两种形态取其一）
├── harness/<adapter>/          # harness 适配器会话目录（adapter ∈ {pi, claude}）；WI Agent/Daemon 专用，subagent 不可见（§4.4） 〔#111分类：删→驱动〕
│   ├── session.json            #   稳定 session identity：{session_id, adapter, created_at}
│   └── session-data/           #   该 harness 自身写入的本机会话工作目录（不透明，格式由 harness 定义）；claude 运行时在 harness 默认会话位置（Capsule 外），终态由 Daemon 采集进本目录（§4.4）
└── sync/                       # Revision 暂存（WI Agent/Daemon 专用；subagent 不可见；发布协议权威在 `05-sync-revision-recovery.md#§5.1`） 〔#111分类：删→驱动〕
```

Capsule 目录内**不物化任何隔离专用目录**——v1 trusted internal repo，host-native，不引入 per-step isolation，Native runner 直接使用宿主机 PATH 与已安装工具链（§1.2）。上表新增的三个物化目标（`.pi/agents/`、`.claude/agents/`、`workflow.md` 或 `workflow/`）不是隔离机制，而是 harness 适配器解析 subagent 定义与执行图的落位（§4.5）：由 WI Agent 第 0 步从本机 aihub checkout 物化，不进入同步根（排除裁定见 `05-sync-revision-recovery.md#§4.1`）。 〔#111分类：删→驱动〕

### 3.2 代码目录 <!-- §3.2 -->

```
internal/capsule/   # 布局、capsule.json、身份校验、生命周期（§2/§3/§6.1）
internal/runner/    # Native runner：RunCommand 实现、runtime-state 写保护（§5/§6.2/§6.3）
```

## 4. 数据结构 <!-- §4 -->

### 4.1 repo inventory：声明格式 <!-- §4.1 -->

WI 创建时可声明 repo inventory：`[{name}]`（项目仓库名），1 ≤ 条数 ≤ 8。**权威存储在 `02-aihub-core.md#§4.6` `work_items.repo_inventory` 列**（本文不重复定义存储）：创建事务内冻结，此后不可变（不受 bundle 变更影响）。只接受名字：url 在认领时从项目记录解析，客户端给的 `url`、`subdir`、`base` 一律在建票时被拒。认领答复的 `repos` 只含清单内仓库（无清单时为项目全部仓库），Daemon 逐个 clone 默认分支（§4.3）；重新认领时若 WI 的 carrier ref 存在，则恢复到上次 session 发布的 head。Daemon 丢失本地声明（takeover/重新检出）时通过认领答复重建清单，不依赖本地缓存或推断。

### 4.2 `runtime-state/execution.json`：完整文件格式（权威，INV-1 的唯一执行权威文件） <!-- §4.2 -->

> **PARTIALLY ARCHIVED（2026-10-16）— `runtime-state/execution.json` 本身与其 steps 结构是现行机制**（权威是 `internal/common/capsule/capsule.go` 的 `Execution`/`Step`，单写者是 daemon 内的 Go driver）：`current_step`、`workflow_status`、`steps.<id>.{status, output, attempts, answers, pending_question, pending_rewind_input, supervision}`、`rewind_stats`、`version` 等。**已删除、do not build from this 的部分**：`generation` 字段、WI Agent 作为写者、`WF-*` 码、watcher dirty 位与发布触发、RunCommand。字段集以 capsule.go 为准（例如 `steps.<id>.supervision` 本节没有）。

```json
{
  "capsule_id": "cap_wi_793",
  "work_item_id": "wi_793",
  "generation": 3,
  "current_step": "code",
  "workflow_status": "running",
  "steps": {
    "grill":  { "status": "done", "output": { "qa_log": "artifacts/grill-qa.md" },
                "step_started_at": "2026-09-20T08:10:00Z",
                "step_start_refs": { "repos/app": { "head": "a1b2c3d", "dirty": [] } },
                "budget_consumed": { "requests": 5, "tokens": 9000, "wall_minutes": 3 },
                "attempts": [], "answers": [], "pending_question": null, "pending_rewind_input": null },
    "spec":   { "status": "done", "output": { "spec": "artifacts/spec.md" },
                "step_started_at": "2026-09-20T08:30:00Z",
                "step_start_refs": { "repos/app": { "head": "a1b2c3d", "dirty": [] } },
                "budget_consumed": { "requests": 7, "tokens": 21000, "wall_minutes": 6 },
                "attempts": [], "answers": [], "pending_question": null, "pending_rewind_input": null },
    "code":   { "status": "in_progress", "output": null,
                "step_started_at": "2026-09-20T09:02:00Z",
                "step_start_refs": { "repos/app": { "head": "a1b2c3d", "dirty": [] },
                                     "repos/lib": { "head": "9f8e7d6", "dirty": ["README.md"] } },
                "budget_consumed": { "requests": 30, "tokens": 150000, "wall_minutes": 28 },
                "attempts": [
                  { "cause": "gate_fail_rewind", "attempt_seq": 1,
                    "result": { "status": "gate_fail_target", "output": null },
                    "started_at": "2026-09-20T08:45:00Z",
                    "archived_at": "2026-09-20T09:00:00Z",
                    "rewind_gate": "verification",
                    "consumed_budget": { "requests": 4, "tokens": 12000, "wall_minutes": 12 } },
                  { "cause": "infra_failure", "attempt_seq": 2,
                    "started_at": "2026-09-20T09:05:00Z",
                    "interrupted_at": "2026-09-20T09:07:00Z",
                    "detail": "subagent process killed (OOM)",
                    "consumed_budget": { "requests": 3, "tokens": 12000, "wall_minutes": 2 } }
                ],
                "answers": [
                  { "question_id": "q_a1b2c3", "answer_text": "按方案 A 处理，细节见评审意见。",
                    "answered_at": "2026-09-20T09:09:00Z", "answered_by": "user_alice" }
                ],
                "pending_question": null,
                "pending_rewind_input": null }
  },
  "review_rounds": { "verification": 1 },
  "rewind_stats": { "total": 1, "by_step": { "code": 1 } },
  "budget_consumed": { "requests": 42, "tokens": 180000, "wall_minutes": 37 },
  "updated_at": "2026-09-20T09:10:00Z",
  "version": 118
}
```

字段权威说明：

- `current_step`：当前活跃 step 的 id；drive 主循环按 `01-workflow-skills.md#§6.5` `next_step_by_order` 推进后更新；`workflow_status` 为终态（`completed`/`failed_gate`/`failed_rewind_exhausted`）时冻结，不再前进。
- `steps.<id>.status`：`pending|in_progress|done`（`01-workflow-skills.md#§6.1`/§6.3 语义逐字一致）。
- **`steps.<id>.output`（新增结构化字段 `commands`，修复 B10）**：除具体 step 自定义的产出字段（如
  `spec`/`qa_log`）外，若该 step 期间发生过一次或多次 `RunCommand`（`03-execution-capsule.md#§5.1`）调用，WI Agent 〔#111分类：删→驱动〕
  必须在每次调用返回后向 `output.commands` 数组追加一条 `{argv, exit_code, duration_ms, started_at}`
  （直接来自 `RunCommand` 返回的 `Result`，不经 subagent 中转或汇总）；`07-deployment-pilot.md` 的
  M4/M5 判定仅认这一字段，不接受 `output` 中任何其他自由文本字段作为命令确实执行的证据。
- `steps.<id>.attempts`：**step 尝试历史（归档列表）**，数组，时间序，每个元素统一携带 `{cause, attempt_seq, started_at, consumed_budget}`，`cause ∈ {gate_fail_rewind, infra_failure}`（`cause` 两分法与 `01-workflow-skills.md#§6.3` 第 2b 条的表述逐字一致）：(a) **rewind 归档**（`cause: "gate_fail_rewind"`）额外携带 `{result, archived_at, rewind_gate?}`，每次该 step 因 rewind 被重置、或产出被归档的结果时追加（与 `01-workflow-skills.md#§6.2` 描述的「归档为一条历史尝试记录」动作逐字对应；`rewind_gate` 记录触发本次回退的 gate step id，供审计与恢复定位）；(b) **基础设施失败中断**（`cause: "infra_failure"`）额外携带 `{interrupted_at, detail}`，即 `01-workflow-skills.md#§6.3` 逐字定义的 `{attempt_seq, started_at, interrupted_at, cause, detail, consumed_budget}`，每次基础设施失败中断时追加（**不**重置 `steps.<id>.status` 为 `pending`，仅记录中断本身，供审计与恢复定位使用）。`01` 不定义独立的 `capsule_state` 结构，本文件的 `steps.<id>.attempts` 是两类归档/追加动作唯一落盘位置；旧结果归档后不参与任何活路径计算（`01` INV-1d）。
- **`steps.<id>.pending_question`（D8/D9 裁定，修复 B14；本条已按驱动架构重写，polyforge#111）**：`{id, step, question, context, asked_at}` 或 `null`（磁盘形状以 `internal/common/capsule/capsule.go` 的 `PendingQuestion` 为准；引擎切换前的登记形状 `{question_id, asked_at, content, pending_answer}` 属历史记录）。**执行驱动**在 step 产出 needs_input 结果时，在把该 step 置入等待的**同一次原子写入**内写入本字段——`question_id`/`question` 取自该 step 自己的 needs_input 结果（envelope 字段或 output JSON 内嵌的 `question_id`/`question`，驱动校验两者必须存在但不代生成）；`asked_at` 由驱动盖戳——这是 **step interrupted 边界的同步触发（D9）**：问题必须先随本次写入落盘，才被呈现给 owner（D6 的 ops status QUESTION 列即从这份持久文件派生），不存在「已问出去但 `execution.json` 里没有记录」的窗口。回答经 Daemon 的 answer API 进入：`POST /jobs/{wi}/answer` → bridge Handle.Answer（驱动路径，`daemon/routes.go`），**按 `question_id` 绑定**到当前 step 的开启问题，不匹配即拒绝（`DAEMON-409` 语义；比对与拒绝的权威在 `00-architecture.md#§3.4a`，本字段是比对依据的唯一持久化位置）。驱动的消费是**单次原子写**：把问答对追加归档到 `answers`（见下一条）并在**同一次写入**内将整个 `pending_question` 清空（设为 `null`，问题已消费）——**无任何 agent ack**；「已投递/待 ack」的两段式等待只属于仍可显式选择的旧引擎（`-use-driver=false` 的 stdio observe 路径），不在默认引擎的语义内。驱动也没有旧模型的 `pending_answer` 中间态：归档与清空既是一次写入，就不存在「收到回答但尚未续跑」的窗口（旧模型的 pending_answer 机制随 WI-Agent 引擎一并退役——历史记录）。驱动等待期间崩溃：`pending_question` 是持久的，重新推导出的驱动在同一问题上再次停车（`plan()` 读到非空 `pending_question` 即不派发），按原 `question_id`/`question` 原样重新呈现同一问题（不重新生成 `question_id`，见 §7），回答跨重启照常可送达（Answer() 不依赖任何进程内存）。〔#111分类：已重写→驱动〕
- **`steps.<id>.answers`（D9 裁定，已消费回答的归档列表，修复 B9；本条已按驱动架构重写，polyforge#111）**：数组，时间序，每个元素 `{question_id, answer_text, answered_at, answered_by}`（`answer_text`/`answered_at`/`answered_by` 与 answer 中继报文同名字段一致；`question_id` 即被消费问题的标识；旧模型曾登记的 `received_at` 属历史记录，磁盘形状以 `internal/common/capsule/capsule.go` 的 `ArchivedAnswer` 为准）。**执行驱动**在消费回答时，向本列表追加该问答对的归档条目，并在**同一次原子写入**内把 `pending_question` 清空（设为 `null`）——归档与清空是同一原子写入的两个组成部分，落盘状态只有「未消费（`pending_question` 非 `null`）」与「已消费且已归档（`pending_question == null` 且 `answers` 末条携带同一 `question_id`）」两种，不存在「已消费但未归档」或「已归档但 `pending_question` 仍非空」的中间态，因此崩溃后可确定性恢复：重新推导出的驱动读到 `pending_question` 非 `null` 时按 §7 的等待恢复路径处理（在同一问题上再次停车、重呈现同一问题），读到 `pending_question == null` 即可机械断定消费与归档已一并落盘，不依赖任何进程内存或旁路传递。已归档条目不参与任何活路径计算（与 `attempts` 同一模式，`01` INV-1d），仅作问答历史与审计依据；本列表是该 step 全部已消费问答的唯一持久化历史位置，问答记录不落入 `attempts`——其 `cause` 两分法逐字受 `01-workflow-skills.md#§6.3` 约束，不为此新增 `cause` 取值。〔#111分类：已重写→驱动〕
- **`steps.<id>.pending_rewind_input`（D8 裁定，待消费 rewind 输入）**：`{gate_step_id, verdict_text, rewound_at}` 或 `null`。当 `on_fail: {action: rewind, include_verdict: true}` 触发回退到本 step 时（`01-workflow-skills.md#§6.2` 第 6 步），WI Agent 在执行 rewind 的**同一次原子写入**内（与 rewind 计数累加、区间归档、状态重置、执行位置回移一起）把本次 gate FAIL **判定全文**（`verdict_text`，触发回退的意见原文）连同 `{gate_step_id, rewound_at}` 写入目标 step 的本字段。该字段是 `01-workflow-skills.md#§6.2`「把 FAIL 判定全文作为附加输入传给 target step」的**持久化形态**：目标 step 被派发时（`pending → in_progress` 的同一次写入）被消费——判定全文组装进该 step 的派发输入，字段清空（设为 `null`）。存在意义是**确定性恢复（D8）**：若 WI Agent 在 rewind 落盘之后、目标 step 派发之前崩溃，新 WI Agent 读到目标 step `status == "pending"` 且本字段非空，即可机械重建派发输入（判定全文必定到达被回退的 step，不依赖任何进程内存或旁路传递），不存在「rewind 已生效但判定全文丢失」的中间态。`include_verdict: false`（显式声明）时本字段保持 `null`，回退输入为空。 〔#111分类：删→驱动〕
- **`steps.<id>.step_started_at`（D8 裁定，单次尝试计量基线）**：RFC3339 时刻，在该 step **每一次进入执行**的写入中更新——首次进入、rewind 重入、**以及基础设施失败 resume（每次中断恢复都开启一次新的计量窗口）**均刷新本字段；被中断尝试的墙钟在其 `attempts` 条目归档（`started_at`/`interrupted_at`）。**当前尝试的 wall 消耗 =「现在 − step_started_at」**——因 resume 也刷新基线，该值永远只度量本计量窗口，不继承任何历史尝试的时间；历史尝试的墙钟只留在其 `attempts` 条目并计入 workflow 级累计（`01-workflow-skills.md#§4.5` 裁定 step 级上界限制单次执行尝试），也是审计时间线的一部分；与 `step_start_refs` 的「首次进入即冻结」不同，本字段随重入刷新——墙钟是流动资源，diff 基线才是必须钉死的比较点。
- **`steps.<id>.step_start_refs`（修复 B10/B18：实现 spec 「diff 必须按 step 起始基线计算」）**：
  `{<repo_relpath>: {"head": <git_commit_sha>, "dirty": [<repo 内相对路径>, …]}}`——每个 inventory 项在该 step
  开始时的**起始 HEAD 与脏集**。在该 step 从 `pending` 转为 `in_progress` 的那一刻（而非
  WI 创建时），WI Agent 对每个 `repos/<name>/` 工作树记录当前 `git rev-parse HEAD`（含未提交改动 〔#111分类：删→驱动〕
  时仍以最后一次提交为参考点，未提交改动本身不影响该值，diff 仍能约到）与当时的未提交改动路径集合
  （`git status --porcelain` 口径的路径列表，即「脏集」）；脏集的作用是把「step 开始时就已存在的未提交
  改动」从该 step 的 diff 归属中排除——某路径既在起始脏集内、又出现在该 step 的 diff 中时，不能仅凭 diff
  断定是本 step 写的。重新派发（rewind 回到该
  step、基础设施失败 resume）**不**更新该字段（保持第一次进入该 step 时的基线，保证多次 attempt
  的 diff 基线一致）。`07-deployment-pilot.md` 的 INV-D4/M4/M5 判定均必须比对该字段（HEAD+脏集）而非 `03-execution-capsule.md#§4.1`/
  §4.3 的 WI 级认领基线（`capsule.json` 的 `repos`），后者是整个 WI 的起始基线，不能区分“本
  step 自己改了什么”与“前面 step 留下的改动”。
- `review_rounds`：**review 轮次**，`{<gate_step_id>: <int>}`，值等于该 gate step 自身 `attempts` 数组长度加当前一次未归档结果（即该 gate 已被评估的总次数）；与 `rewind_stats` 的关系是**派生但同一原子写入单元**内一起落盘，不是第二权威——`rewind_stats` 统计的是「触发了多少次回退」，`review_rounds` 统计的是「某个 gate 被评估了多少次」，两者口径不同，都从同一份 `steps` 归档结构机械推得。
- `rewind_stats`：`{total, by_step}`，`total` 是全 workflow 累计触发的 rewind 次数，`by_step` 是按 `on_fail.target` 分组的计数；与 `01-workflow-skills.md#§6.2` `consumed_rewinds_total`/`rewinds_reached[target]` 逐字对应。
- **`steps.<id>.budget_consumed`（D8 裁定，step 级预算消耗）**：`{requests, tokens, wall_minutes}`，该 step 全部执行尝试（含 rewind 重入后的重跑）的累计消耗；rewind 本身的记账（`01-workflow-skills.md#§6.6`：每次 rewind 额外计一次 requests 单位并预估目标 step 的 token 消耗）归属到目标 step 的本字段。三个维度与 `01-workflow-skills.md#§6.6` 预算维度逐字一致。**判定输入分层（与 `01-workflow-skills.md#§4.5` 逐字一致）**：step 级预算上界判**单次执行尝试**——判定输入**仅是当前计量窗口内自产的消耗**（`requests`/`tokens` 按本计量窗口内的调用计数、`wall` 按「现在 − step_started_at`；不继承 `attempts` 归档条目的任何消耗——那些已属历史尝试），不是本累计字段；本字段承担 step 级累计审计并作为 workflow 级聚合的来源；workflow 级上界（顶层 `budget_consumed`）才判全生命周期累计。
- `budget_consumed`（顶层）：`{requests, tokens, wall_minutes}`，整个 WI 生命周期内全部 step 执行尝试的累计消耗（含 rewind 重跑），与 `01-workflow-skills.md#§6.6` 预算维度逐字一致。**权威在 step 级**：顶层值是各 `steps.<id>.budget_consumed` 的机械聚合，与 `review_rounds` 同一模式——派生但同一原子写入单元内一起落盘，不是第二权威；两层同时生效的预算判定（`01-workflow-skills.md#§6.6`）分别读 step 级与顶层。
- `workflow_status`：**取值集合为 `running | completed | failed_gate | failed_rewind_exhausted | blocked`**，不得出现集合外的名字：
  - `running`：缺省值，drive 主循环正在推进（`01-workflow-skills.md#§6.1`/§6.5）。
  - `completed`：`01-workflow-skills.md#§6.5` drive 主循环跑完 `workflow.md` frontmatter 的全部 steps 后写入，随后触发 `wrap`（`01-workflow-skills.md#§6.5` 末尾）。
  - `failed_gate`：`01-workflow-skills.md#§6.2` `on_fail` 缺失或 `action=terminate` 时的终态（`WF-102`）。
  - `failed_rewind_exhausted`：`01-workflow-skills.md#§6.2` 达到 `rewind.max_total`/`max_per_step` 上界时的终态（`WF-101`）。
  - `blocked`：Capsule 侧**非终态**的等待人工状态，`current_step` 保持不变（不前进不回退），对应且仅对应 `01` 定义的三种情形：① `01-workflow-skills.md#§6.4` incomplete 结果、② `01-workflow-skills.md#§6.6` 超预算 `OVER_BUDGET`、③ interactive step 在无人驱动模式下被调度（`01-workflow-skills.md#§5.2` `WF-105`）。三种情形均已使 WI 在 Core 侧转为 `blocked`（`waiting_on=owner`，`02-aihub-core.md#§2.5`）；`execution.json` 的 `workflow_status=blocked` 是这一 Core 侧状态在 Capsule 执行权威文件里的如实反映，不是新造的第五种“capsule 专属”语义——`blocked` 一词直接取自 `01`/`02` 已使用的同一词，满足与 `01` 逐字一致的要求。owner 处理后（续跑当前 step、增加预算、或转 `terminate`/`rewind` 判定）由 WI Agent 重新写回 `running` 或对应终态。 〔#111分类：删→驱动〕
- `version`：单调递增，每次写入 +1，供对账（`05-sync-revision-recovery.md#§6` 步骤 3「扫描」与 parent 的内容差异对比）判断旧版本。

**与 `02-aihub-core.md#§5.2` read-through 的关系（权威）**：本文件当前 **不单独声明一个名为 `progress` 的子字段**。Core 的 read-through（`02-aihub-core.md#§5.2`）将本 JSON 对象的**完整内容本身**作为对外响应体的 `progress` 字段值原样返回，不裁剪、不重组；即本文上述全部顶层字段（`current_step`/`workflow_status`/`steps`/`review_rounds`/`rewind_stats`/`budget_consumed`/`version` 等）合起来就是对外可见的「进度」。若未来需要引入专门的 `progress` 子字段（例如为了与历史 `attempts` 详情区分开对外摘要），必须同时修订本节与 `02-aihub-core.md#§5.2`，保证两文一致，不存在一方已声明专用字段另一方仍按整个文件透传的不一致状态。

**原子提交规则**：任何写入（rewind 同时改多个 step 状态+归档+待消费 rewind 输入+判定，step 完成同时改 `steps`+两级 `budget_consumed`+`review_rounds`，step 置入等待同时写 `pending_question`，消费回答的续跑同时向 `answers` 追加归档并清空 `pending_question`）均先写入临时文件 `runtime-state/.execution.json.tmp`，`fsync`，再 `rename` 覆盖 `runtime-state/execution.json`（POSIX `rename` 单盘原子）。读方（watcher、subagent 自查、read-through 重建）均读到某一个完整版本，不存在半写状态，不需要额外锁。

这一 tmp+fsync+rename 序列是 Capsule execution-state 唯一的受控更新接口：唯一合法调用方是该 WI 的 WI Agent（INV-6/INV-3），任何 step subagent 均不得直接发起（§6.2）；Daemon 不代 WI Agent 执行这次写入，也不提供另一条写入通道——它只作为观察者，通过 watcher 对本文件的 dirty 标记（`04-local-runtime.md#§6.3`）与 `status()` 的 read-through（`04-local-runtime.md#§5.1`）参与机械同步，写入权与观察权是两件分开的事。 〔#111分类：删→驱动〕

### 4.3 repo inventory：检出流程与基线 commit 记录 <!-- §4.3 -->

认领答复的 `repos`（`[{name, url}]`，url 由 AIHub 在认领时从项目 `projects.repos` 解析，`02-aihub-core.md#§4.6`）在 agent 启动前由 Daemon 逐个检出（`internal/polyforge/runtime/repos.go`）：

1. 对每个 `{name, url}`：目标目录 `dir = <capsule>/repos/<name>`。
2. 若 `dir` 已存在：直接复用（存在性即判据——记录的基线随 prior capsule.json 原样保留；不自动重置、不强制重置，漂移属人工处理范畴）。
3. 若 `dir` 不存在：`git clone -- <url> dir`（默认分支；不支持冻结 base commit）；该 WI 的 carrier ref 存在时（重新认领），把新 clone 移到上次 session 发布的 head。
4. `capsule.json` 的 `repos`（name → 认领时 HEAD sha）记录每个仓库的基线。检出/克隆失败是 typed 的 `DAEMON-507`，绝不静默退化。

### 4.4 harness 会话目录：session 身份规则与本机会话数据的界限（`00-architecture.md#§3.6a` 会话数据落位的权威布局，本节是该规则的唯一权威定义，`04`/`05` 引用本节不重复定义） <!-- §4.4 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（持久 WI Agent、owner generation、Revision/CAS/manifest、flush 证据、takeover）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

`00-architecture.md#§3.6a` 把「稳定的会话身份」与「会话数据落位」（跨 harness 契约要求＝封存（seal）时刻该 WI 的 Capsule 内含有该会话的会话记录；运行时落位按适配器区分，见下文 `session-data/` 条与 A14 归档段）列为跨 harness 的最小契约，权威布局与身份规则由本文给出：`<capsule>/harness/<adapter>/`（`adapter` 取当前该 WI 使用的 harness 适配器名，v1 为 `pi` 或 `claude`，`04-local-runtime.md#§6.1a` 权威）。

**session 身份规则（唯一权威）**：
- **仅一种情况复用 session**：同一台机器上、同一个 generation 内，Daemon/WI Agent 崩溃后续跑（即 Daemon 在本机重新派生该 generation 的 WI Agent，非接管、非新 generation）——此时复用同一个 `session_id`，不重新分配。 〔#111分类：删→驱动〕
- **pause 结束该 generation，resume 是新 generation**：pause 属于 §2.1 定义的正常收尾路径，一旦 pause 落地，该 generation 即告结束；resume 时 owner 需重新 claim/取得新的活跃 generation，与旧 generation 并非同一个——resume **必须**使用全新的 `session_id` 与全新的 `session-data/`，不得复用 pause 前的旧 session。
- **任何新 generation 或跨机器一律新 session**：无论是跨 generation（包括 pause → resume、takeover），还是同 generation 下换机器，一律不延续旧 session；新 WI Agent 从 Capsule 的 `runtime-state/execution.json` 恢复执行现场后，为该 WI 建立全新的 `session_id` 与全新的 `session-data/`。 〔#111分类：删→驱动〕
- **session-data 永不同步**：`session-data/` 仅仅是本机运行时目录，任何情况下都不参与同步，也不从 Revision 重建；新 session 总是从空开始。
- 换言之，`session_id` 的「稳定」仅在（同 generation ∧ 同机器）的崩溃续跑这一个条件内成立；任何 generation 边界（包括 pause → resume）或机器边界都是全新 session 的分界线，不存在第二种复用情况。

该目录下明确区分两类数据，处理方式不同：

- **`session.json`（稳定 session identity，小体量元数据，可同步）**：`{session_id, adapter, created_at}`，由该 capsule 实例的 WI Agent 首次派生 harness 进程时写入一次，此后只读。仅在同 generation、同机器的崩溃续跑（`04-local-runtime.md#§7.1`、本文 §7）时，续跑的 WI Agent 才从这份本机文件读回 `session_id` 并原样传给 harness 适配器的启动参数（`04-local-runtime.md#§6.1a`），不重新分配——这是上面「session 身份规则」的唯一本地实现依据，Daemon 不问 harness 进程本身要这个值。pause → resume 不属于这一情况：resume 是新 generation，必须写入全新的 `session.json`（新 `session_id`），不读回旧文件。`session.json` 本身体量小，**允许作为同步根的一部分随正常发布触发时机一并发布**（与 `repos/`/`artifacts/` 同类，非强制），但发布出去的这份副本**仅作为历史元数据留痕**（供人工/监控事后查看某一次发布时对应的 session 身份），**从不用于恢复身份**：任何恢复路径一律只读本机的 `session.json`，不读、也不信任从 Revision 重建出的副本——session-data 永远不同步，发布若发生仅为历史元数据。 〔#111分类：删→驱动〕
- **`session-data/`（harness 自身的本机会话工作目录，不透明、可能较大，仅本机）**：Pi/Claude 各自的会话转录、工具调用缓存等内部格式，只有对应 harness 进程自己读写，WI Agent/Daemon 不解析其内容——**运行时落位按适配器区分**（`00-architecture.md#§3.6a`）：pi 支持自定义 session 路径，其 harness 进程直接写本目录；claude 不支持自定义 session 路径，运行时写在该 harness 的**默认会话位置**（本机、Capsule 外），本目录在运行期为空，直到终态采集（下文 A14 段）把本代会话 JSONL 收进来——该采集复制是 Daemon 对本目录的唯一写入（机械文件复制、不解析内容，发生在该代 harness 进程结束后），是「只有对应 harness 进程自己读写」的唯一例外。这部分是**仅本机运行元数据**：不属于 `05-sync-revision-recovery.md` 定义的同步根文件集合（`05-sync-revision-recovery.md#§4.1`），watcher 不观察它，任何一次全扫描发布都不会把它打包进 Revision——与 `sync/` 一样，对 subagent 不可见，且额外地对「是否参与同步」也给出确定答案（不参与）。 〔#111分类：删→驱动〕

**session transcript 归档（A14 裁定 + claude 终态采集修订，本节为 `logs/session/` 固定位置与终态采集的唯一权威）**：会话进行中，transcript（JSONL）的**运行时**落位按适配器区分：**pi** 写在本机 `harness/<adapter>/session-data/` 内，随 `session-data/` 一起**不同步**（上文）；**claude** 不支持自定义 session 路径，写在该 harness 的**默认会话位置**（本机、Capsule 外）——运行中的任何会话数据读取一律指向该默认位置，不指向 Capsule（`04-local-runtime.md#§6.1a`），且因位于 Capsule 外而天然不在任何同步面。无论哪个适配器，会话进行中的 transcript 都不进入任何 Revision。**每次 end-generation（pause/block/wrap/fail）时**，Daemon 把该 generation 的会话 JSONL 复制为 `<capsule>/logs/session/<generation>.jsonl`：`logs/session/` 是 transcript 归档的唯一固定位置，文件名与归档它的 generation 一一对应，一个 generation 至多一份归档。**claude 适配器的终态采集（本节权威）**：归档复制之前，Daemon 先以**只读**方式从该 harness 的默认会话位置把本代会话 JSONL 复制进该 Capsule 的 `harness/<adapter>/session-data/`（该会话在 Capsule 内的本机会话目录）——这是「封存时刻 Capsule 含会话记录」这一 `00-architecture.md#§3.6a` 契约的实质落点，采集完成后归档步骤对两条适配器统一从 `harness/<adapter>/session-data/` 取源复制出 `logs/session/<generation>.jsonl`。**采集缺失是 typed error，不是静默跳过**：本代已派生过 harness 会话（本机 `session.json` 存在）而默认会话位置未找到该会话的 JSONL 时，Daemon 以 `DAEMON-513 SESSION_JSONL_MISSING`（`04-local-runtime.md#§5.2`）失败本次收尾，不产出归档文件、也不代为收尾；本代从未派生 harness 会话或 transcript 为空文件时不产生归档路径、不触发该错误（与 `05-sync-revision-recovery.md#§6.1` 的空链分支一致）。采集与归档复制均发生在该 generation 最后一次发布（final flush，`05-sync-revision-recovery.md#§6.1`）的冻结扫描**之前**，归档随该次 end-generation 的最后一次 Revision **一次性发布，不逐边界重传**——裁定理由：CAS 按整文件内容寻址，transcript 在会话期间持续增长，若随每条 Revision 边界同步，每次都要为整个文件重新上传一个块，成本随会话长度线性放大；按代归档使每代 transcript 至多占用一个 CAS 块（pause→跨机 resume→wrap 时前代已在 pause 时归档，换代路径完整覆盖）。归档文件只是**审计材料**：任何恢复路径都不依赖它（跨机重建见 `05-sync-revision-recovery.md#§7.4`），它也不参与 harness 身份的任何判定（上文 session 身份规则只读本机 `session.json`）；写入者是 Daemon（end-generation 收尾时该 capsule 已无 subagent 运行），不占用 subagent 的 `logs/**` 追加写权限面（§5.2）。

**接管/跨机的处理**：`harness/` 整体随 Capsule 目录在接管时被 §2.4/§6.1 的 quarantine 机制隔离，新 generation 的 WI Agent 在新建的干净目录里为该 WI 重新创建全新的 `session.json`（新 `session_id`）与全新的 `session-data/`——不尝试跨 generation 或跨机延续旧 harness 会话。接管（force-takeover）本质上也是产生新 generation，与 pause → resume 适用同一条规则；这与 §2.4 裁定「新代总是从干净目录开始，不信任任何本地残留」对 `harness/` 同样成立，也与上面「session 身份规则」逐字一致。`00-architecture.md#§3.6a`、`04-local-runtime.md`（harness 适配器启动参数，§6.1a）与 `05-sync-revision-recovery.md`（同步根定义，§4.1）均引用本节作为该规则的唯一权威，不重复定义。 〔#111分类：删→驱动〕

### 4.5 subagent 定义文件与 workflow 快照：第 0 步物化与防冲突三层（本节权威=物化协议与防冲突；workflow.md frontmatter schema 与 step skill 定义的权威在 `01-workflow-skills.md#§4.1`/`#§4.4`，本文不重复定义） <!-- §4.5 -->

> **PARTIALLY ARCHIVED（2026-10-16）— 物化的原子集合 + 完成标记（信任门：没有完成标记的物化不被信任、整体重做）是现行机制**（`runtime/materialize.go`）：daemon 把 bundle 整树物化到 capsule 的 `workflows/<bundle>/`，driver 直接读 `workflows/<bundle>/steps/<id>.md`（`steps/` 另拷到 capsule `agents/` 的第二份副本已于 2026-10-16 G4 删除；旧 capsule 里残留的 `agents/` 惰性无读者）。**已删除、do not build from this 的部分**：WI Agent 第 0 步、`.pi/agents/`、`.claude/agents/`、根 `workflow.md` 等物化目标。

本节裁定 Capsule 内三个物化目标（`.pi/agents/`、`.claude/agents/`、`workflow.md` 或 `workflow/`）的内容来源、校验口径、文件模型与防冲突机制；目录本身的建立时机见 §6.1 第 5 步，同步侧排除见 `05-sync-revision-recovery.md#§4.1`。

**物化协议（WI Agent 第 0 步）**： 〔#111分类：删→驱动〕

1. WI Agent 被派生后、驱动任何 step 之前的**第 0 步**，从本机 aihub checkout 的 `workflows/<bundle>/` 物化三项目标：`agents/*.md`（subagent 定义文件）→ `<capsule>/.pi/agents/`（pi 适配器）；同一来源按需复制到 `<capsule>/.claude/agents/`（claude 适配器，仅当该 WI 使用 claude 适配器时建立）；`workflow.md` → Capsule 根下 `workflow.md`（本 WI 执行图快照；或以 `workflow/` 目录形态存放，两种形态同一 capsule 内取其一）。 〔#111分类：删→驱动〕
2. **不做内容校验（polyforge#149 B9：digest 校验链已删）**：物化只按 `bundle_name` 复制；本机 checkout 与 Core 同源（v1 信任内网），漂移属配置事故而非攻击面——缺 bundle/缺文件时告警事件供人工对账，执行继续。
3. 物化产物是**只读快照**：WI Agent 第 0 步写入后不再修改；subagent 对其只读（§5.2），物化产物不是执行权威——执行进度的唯一权威恒为 `runtime-state/execution.json`（§4.2）。 〔#111分类：删→驱动〕

**subagent 定义文件模型（Capsule 侧权威）**：每个定义文件是一个 `*.md`，元数据写在其 frontmatter，字段为 `name`/`description`/`tools`/`output`——`name` 是 harness 派生时引用的 subagent 名，`description` 供 WI Agent 选择与组装派生 prompt，`tools` 是工具面声明，`output` 是输出 JSON schema。**不写 `model` 字段**——模型选择是流程作者在 workflow 层的决定，不是 subagent 定义作者的决定。subagent 的最终输出必须是符合 `output` schema 的 JSON，**不符合即按基础设施失败处理**（`01-workflow-skills.md#§6.3`），不产出业务判定、不触发 gate。 〔#111分类：删→驱动〕

**防冲突三层（本文裁定）**：

1. **spawn 一律 `agentScope="project"`**：WI Agent 派生 subagent 时恒以项目级作用域发起，系统级 agent 定义不进入解析面，杜绝 capsule 之外的同名定义混入或遮蔽。 〔#111分类：删→驱动〕
2. **项目级同名覆盖系统级**：pi 源码已验证——项目级 `.pi/agents/` 内与系统级同名的定义以项目级为准，解析结果可预期。
3. **capsule 必建空 `.pi/agents/`**：pi 从 subagent 的 cwd 向上查找最近的 `.pi/agents`；若 capsule 内不存在该目录，查找会**向上泄漏**到工作区级（或更上层）的 `.pi/agents/`，把与本 WI 无关的定义带入解析面。因此 §6.1 的创建流程必须建立该目录，**即使本次物化没有任何定义文件也要建空目录**，把解析面钉死在 capsule 内。

**同步与恢复边界**：三个物化目标不属于 `05-sync-revision-recovery.md#§4.1` 定义的同步根——watcher 不观察、Revision 不打包、恢复不写回；新 generation 一律由 WI Agent 按本节同一规则从本机 aihub checkout 重新物化（排除理由与不变量见 `05-sync-revision-recovery.md#§4.1` 及其 SYNC-INV-10）。 〔#111分类：删→驱动〕

## 5. API 与错误码：命令执行入口与 v1 Native runner profile <!-- §5 -->

### 5.1 RunCommand 调用面（WI Agent → Capsule Native runner，权威签名） <!-- §5.1 --> 〔#111分类：删→驱动〕

```
RunCommand(spec) → Result
spec = { capsule_id: string
       , cwd: "repos/<name>" | "artifacts"     # 只允许 capsule 内相对目录
       , argv: []string                          # 按宿主机 PATH 正常解析（§1.2：v1 trusted internal repo，host-native，不引入 per-step isolation）
       , env: map[string]string                   # 基线 env（含宿主 PATH），不含 runtime-state 路径或 AIHub 凭证
       , timeout_ms: int }                        # 必填；超时按 §7 回收进程组
Result = { exit_code: int, stdout, stderr: string, killed: bool, duration_ms: int }
```

WI Agent 是唯一调用方；harness-native subagent 的命令都经其所属 step 的执行路径落到此入口（`04-local-runtime.md#§5.2`）。`spec` 不携带任何网络隔离类字段（v1 无网络隔离，权威见 §1.2）。 〔#111分类：删→驱动〕

### 5.1a 发布时输出 schema 机械校验（Daemon 侧检查步；polyforge#27 修订） <!-- §5.1a -->

> **ARCHIVED — 本节描述或依赖已删除的机制（持久 WI Agent、Revision/CAS/manifest、flush 证据、publish_request）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

每次全扫描发布（HEAD 推进）在 staging 固化（`05-sync-revision-recovery.md#§6` 第 4 步）之后、manifest 提交与 HEAD 推进（第 5–6 步）之前，挂载一次输出契约的机械校验步。它是 M 检查面（发布面机械证据通道）的新成员：与 `07-deployment-pilot.md#§9.2` 的 M4/M5 同属「机械证据优先于自报」的谱系，把执法对象从命令证据扩展到输出 schema 合规。六步发布序列的权威仍在 `05-sync-revision-recovery.md#§6`，本节只定义该检查步的 Capsule 侧契约，不构成第二条发布路径：

1. **校验输入（INV-6 边界，本节权威）**：只读 staging 内已固化的 `runtime-state/execution.json` 快照副本——即将随本次发布写入 Revision 的字节——中每个 `status == "done"` 的 step 的 `output`（§4.2）。**绝不读取 Capsule 活目录内的 `runtime-state/`**：runtime-state 是 WI Agent 的单写者私有域（INV-6/§2.3），Daemon 的这次读取只发生在发布面、只针对已固化字节，与 `05-sync-revision-recovery.md#§6` 第 4 步「只读取 staging，不重新读取活动的同步根目录」同一纪律。校验输入恒等于被发布字节，校验结论与 Revision 记录因此不可能分叉。 〔#111分类：删→驱动〕
2. **校验目标**：各 step 的 pinned `output` JSON Schema，从 Capsule 内第 0 步物化的只读 workflow 快照（§4.5）读取 `agents/<step-id>.md` frontmatter 的 `output`；与 WI 首次认领冻结时点的 bundle 引用（`01-workflow-skills.md#§2.2` INV-1a/INV-1f）同源，不存在「校验用 schema 与执行用 schema 不同源」的窗口。
3. **校验器**：draft-07 的机械完整实现；schema-authoring 规则与本仓 bundle 测试（`internal/workflow/schema`）强制的同一契约——参考 bundle 的 output schema 只用标准 draft-07 关键字（required/enum/minItems/minLength/oneOf/contains 等），真校验器接入即同一契约生效，不要求改写任何既有 schema（`01-workflow-skills.md#§4.4`）。
4. **失配分类（INV-1h 既有分类，不新增终态）**：任一 done step 的 `output` 不合其 schema = 该 step 的基础设施失败（`01-workflow-skills.md#§4.4`/`01-workflow-skills.md#§6.3`）：不产出业务判定、不触发 gate。校验结果随 `04-local-runtime.md#§5.1` `publish_request` 的响应返回给 WI Agent——发布边界即 step 完成边界，命中者通常即刚完成的当前 step；若命中更早的 done step（其字节在完成后被改写，本身即异常现场），处理相同：该 step 原地重派。发布本身不受失配拦截：HEAD 照常推进，失配字节作为可审计历史留在 Revision 链上；WI Agent 随后按 `01-workflow-skills.md#§6.3` 第 2b 条追加 `infra_failure` attempt 并原地 resume（同一次原子写入内 `status` 由 `done` 改回 `in_progress`，§4.2），候选指针按其第 4 步有界规则推进。校验步是检测信号，不是发布否决项：`05` 六步序列与 Core 侧校验链均不变，候选/预算耗尽后的 `blocked` 收尾路径（§4.2）也因发布不被拦截而始终可 flush。 〔#111分类：删→驱动〕

### 5.2 v1 Native runner profile：文件系统读写权限（唯一 v1 执行 profile） <!-- §5.2 -->

> **PARTIALLY ARCHIVED（2026-10-16）— 没有任何 runner 或文件系统权限强制**：step 与 daemon 同 uid、host-native 运行，按 2026-10-14 信任模型不做隔离（v2-converge §0）。代码仍沿用的只有布局约定——`sync/` 与 `runtime-state/` 是 daemon/driver 私有区，不进入 agent 的工作面（`runtime/materialize.go`）。本表其余权限条目与 RunCommand、WI Agent do not build from this。

Native runner：v1 trusted internal repo，host-native，不引入 per-step isolation；它是在宿主机上以普通子进程方式启动的命令执行器，Linux 与 macOS 同一形态（`00` INV-12）。

**B02 威胁模型口径（本表与 §6.2 的统一前提）**：host-native 同 UID 执行形态下，本表全部机制——权限位、工作目录约定、「不注入路径/凭证」——都是**防误写**机制，**不是安全边界**：与 subagent 同一 OS 账户的进程物理上可以枚举并读写该账户可访问的一切路径（含表中标记「不可见」的目录），网络亦可达一切端点（§2.2）；表中「不可见/不可达」的含义是「执行路径的约定与构造上不出现」，不是「同 UID 进程试图触碰时必然失败」。v1 依赖 `00-architecture.md#§3.7` 的可信内部仓库前提接受这一点，对蓄意绕过的应对方向见 §11.1，不在 v1 验收标准内。

其文件系统权限面：

| 路径 | subagent 权限 | 说明 |
|---|---|---|
| `<capsule>/repos/<name>/**` | **读 + 写** | **必须覆盖源文件本身**，不能只允许读 `repos/inventory.json` 等元数据——这是历史评审发现的具体缺口：早期草稿只 bind 了 inventory.json，subagent 无法读到自己要修改的源码。v1 裁定：整个 `repos/<name>/` 工作树（含 `.git/`）对 subagent 可读可写。 |
| `<capsule>/artifacts/**` | 读 + 写 | step 产物；grill 问答记录等亦落此目录（`01-workflow-skills.md#§4.3`）。 |
| `<capsule>/logs/**` | 追加写 | 执行日志；`logs/session/` 归档文件由 Daemon 在终态收尾写入（§4.4），不占用 subagent 写入面。 |
| `<capsule>/.pi/agents/**`、`.claude/agents/**`、`workflow.md`（或 `workflow/`） | **只读** | WI Agent 第 0 步物化产物（§4.5）：harness 适配器解析 subagent 定义与执行图快照所需；物化完成后不再修改，不进入 subagent 的 `cwd` 约定。 〔#111分类：删→驱动〕 |
| `<capsule>/runtime-state/execution.json` | **只读** | §6.2 落实 INV-6；subagent 可读用于自查当前进度，不可写。 |
| `<capsule>/harness/` | 不可见 | harness 适配器会话目录（§4.4），WI Agent/Daemon 专用，subagent 无需知道其存在，不纳入其工作目录约定（§6.2）。 〔#111分类：删→驱动〕 |
| `<capsule>/sync/` | 不可见 | WI Agent/Daemon 专用暂存，subagent 无需知道其存在，不纳入其工作目录约定（§6.2）。 〔#111分类：删→驱动〕 |
| `<capsule>/capsule.json` | 不可见 | 身份文件，subagent 无需读取。 |
| Daemon state / AIHub 凭证 | 不可达 | 不进 Capsule，不注入 subagent 的 env/cwd/argv（`04-local-runtime.md#§8`）。 |

`RunCommand` 的 `cwd` 只允许 `repos/<name>` 或 `artifacts`（§5.1），这本身就是 subagent 工作目录约定的一部分：subagent 发起的任何相对路径操作天然落在这两个目录之内。

### 5.3 错误码表（前缀 `CAP`） <!-- §5.3 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（owner generation、CAP-* 码、Revision/CAS/manifest）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

| 码 | 语义 |
|---|---|
| `CAP-401` | RUNNER_UNAVAILABLE：宿主机缺少命令声明的可执行文件或必要 shell |
| `CAP-403` | STATE_WRITE_DENIED：subagent 对 `runtime-state/` 的写被拒绝（§6.2，宿主机返回 `EACCES`） |
| `CAP-404` | inventory 声明的 repo 不存在或未检出 |
| `CAP-406` | SYMLINK_ESCAPE_DENIED：Revision 恢复落盘时检测到 symlink 路径逃逸尝试（§6.3，修复 B07） |
| `CAP-409` | CAPSULE_FENCED：调用方携带的 generation 与本地 `capsule.json` 已记录的不一致（正常路径下应先被 `02-aihub-core.md#§5.1` `CORE-403 STALE_GENERATION` 拦下，本错误码是本地二道防线） |
| `CAP-500` | RUNNER_CRASHED：命令进程组异常死亡，转 `01-workflow-skills.md#§6.3` 基础设施失败 |

## 6. 核心流程（编号步骤） <!-- §6 -->

### 6.1 Capsule 创建 / 复用 / 隔离重建 <!-- §6.1 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（持久 WI Agent、owner generation、CAP-* 码、takeover）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

Daemon 在 `claim`/`takeover` 成功后（`04-local-runtime.md#§6.1`）按下列顺序驱动：

1. 计算 `dir = <pf-workspace>/capsules/<project>-<seq>`，`gen` 取自本次 claim/takeover 返回的当前活跃 generation。
2. 若 `dir` 存在且可读到 `capsule.json`：
   a. 若 `capsule.json.generation == gen` 且 `status == "ready"`：**直接复用**该目录（幂等重试路径），跳到第 6 步。
   b. 若 `capsule.json.generation == gen` 且 `status == "initializing"`：视为上次创建中途崩溃留下的不可信半成品，走第 3 步的隔离重建。
   c. 若 `capsule.json.generation > gen`：返回 `CAP-409 CAPSULE_FENCED`（防御性分支，正常路径下不应发生）。
   d. 若 `capsule.json.generation < gen`：走第 3 步的隔离重建（接管场景的常态路径）。
3. **隔离**：将 `dir` 整体移动到 `<pf-workspace>/capsules/_quarantine/<wi_id>/<old_generation>-<old_status>-<ts>/`，不原地复用、不原地续跑（§2.4 裁定）。
4. 创建全新目录结构（§3.1 布局），原子写入 `capsule.json`，`status: "initializing"`。
5. 依次执行：repo inventory 检出（§4.3）→ 从远端 HEAD 恢复现场（`05-sync-revision-recovery.md#§7.4`；无 HEAD 的全新 WI 跳过此步）→ 创建空的 `harness/<adapter>/` 目录（§4.4：不从远端恢复，因为 `harness/` 不在 `05` 同步根文件集合内；`session.json`/`session-data/` 由随后派生的 WI Agent 首次启动 harness 时写入，本步只建目录，不预写内容）→ 创建空的 `.pi/agents/` 目录（§4.5：**必须建立、可为空**——pi 从 subagent 的 cwd 向上查找最近的 `.pi/agents`，不建会把定义解析面向上泄漏到 capsule 之外；claude 适配器在用时按需一并创建空 `.claude/agents/`）。上述目录与 `workflow.md` 快照的**内容**由随后派生的 WI Agent 在第 0 步物化（§4.5），本步只建目录、不预写内容——与 `harness/` 同一「不从远端恢复」模式：它们同样不在 `05-sync-revision-recovery.md#§4.1` 同步根内。 〔#111分类：删→驱动〕
6. 原子更新 `capsule.json.status = "ready"`；只有此时才被视为“可安全复用”——第 4–5 步之间任何一步崩溃，下次调用都会在第 2b 步被识别为 `initializing` 并重新走隔离重建，不会把半初始化目录当作完整现场复用。
7. 返回 `dir` 供 Daemon 派生该 WI 的 WI Agent（`00-architecture.md#§3.6`）。 〔#111分类：删→驱动〕

### 6.2 Subagent 写限制：INV-6 的文件系统权限落实（v1 唯一机制，不引入 per-step isolation） <!-- §6.2 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（持久 WI Agent、CAP-* 码、RunCommand）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

v1 trusted internal repo，host-native，不引入 per-step isolation；INV-6（harness-native subagent 不得写 `runtime-state/`）由**普通文件系统权限**落实，机制选定为**方式 B：只读权限位**，辅以方式 A 作为纵深防御：

- **权威机制（方式 B，只读权限位）**：WI Agent 每次原子写完 `runtime-state/execution.json` 后，将该文件与其所在目录设为不可写模式（目录 `0555`，文件 `0444`），属主为运行 WI Agent 的本机账户。harness-native subagent（无论是同进程内的子上下文，还是 WI Agent 派生的独立子进程）对该路径发起的任何写系统调用，都会被宿主机文件权限机制以 `EACCES` 拒绝，映射为 `CAP-403 STATE_WRITE_DENIED`。WI Agent 自身在下一次需要写入时先将权限位改回可写、写入、再改回只读——这一权限切换只由 WI Agent 的写入路径执行，不暴露给 subagent 的执行路径。 〔#111分类：删→驱动〕
- **纵深防御（方式 A，工作目录约定之外）**：WI Agent 构造 subagent 的 `cwd`/`argv`/`env`（§5.1 `RunCommand.spec`）时，永远不传入 `runtime-state/` 的路径；subagent 唯一被授予的 `cwd` 取值是 `repos/<name>` 或 `artifacts`（§5.2），因此在正常调用路径下 subagent 根本不会构造出指向 `runtime-state/` 的相对路径。 〔#111分类：删→驱动〕
- **边界声明（B02 威胁模型口径，对齐 `00-architecture.md#§3.7`）**：v1 面向可信内部仓库，subagent 执行的是团队自有、可信任的构建/测试脚本（`00-architecture.md#§3.7`）；host-native 同 UID 执行形态下，本节全部机制（只读权限位、工作目录约定、不注入路径/凭证）都是**防误写**机制，**不是安全边界**——它们防御的是**误写**（脚本 bug、路径拼接错误），不防御**蓄意绕过**：与 subagent 同一 OS 账户的进程可以自行 `chmod` 改回权限位、枚举并读写一切「不可见」路径，这是同 UID 形态的确定事实，不是理论窗口。对蓄意绕过的防御不在 v1 范围内，其应对方向是 §11.1 的未来 untrusted runner 演进方向。

`runtime-state/` 之外的三类不可见状态面（`sync/`、Daemon state、AIHub 凭证）分别通过「不在 subagent 工作目录约定内」+「从不注入 subagent 的 env/cwd/argv」两条规则落实，与 `runtime-state/` 采用只读权限位的理由不同：这三类subagent 从不需要读取，因此直接不告知其路径即可，无需额外的权限位机制——同样按上面的 B02 口径，这是构造性约定而非强制隔离，同 UID 进程试图触碰时并不必然失败。

面表汇总：

| 状态面 | 位置 | subagent 权限 | 阻止方式 | 失败错误码 |
|---|---|---|---|---|
| Capsule 执行态 | `<capsule>/runtime-state/` | 只读 | 文件/目录权限位 `0444`/`0555`（方式 B）+ 从不作为 `cwd`/`argv` 传入（方式 A） | `CAP-403` |
| 同步暂存 | `<capsule>/sync/` | 不可见 | 不告知路径、不纳入工作目录约定 | （不存在可达路径） |
| Daemon state | Daemon 数据目录（`04-local-runtime.md#§4.1`） | 不可见 | 不告知路径 | （不存在可达路径） |
| AIHub 凭证 | Daemon 进程内/数据目录 | 不可达 | 不进 Capsule；env 不注入 | （不存在可达路径） |
| Capsule 身份 | `capsule.json` | 不可见 | 不告知路径 | （不存在可达路径） |
| 工作树 / 产物 / 日志 | `repos/ artifacts/ logs/` | 读写 | — | — |

### 6.3 Revision 恢复落盘的 symlink 安全规则（修复历史缺口 B07：symlink 路径逃逸） <!-- §6.3 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（CAP-* 码、Revision/CAS/manifest）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

**问题**：`05` 定义的 Revision 恢复（`apply_ops` 增量应用 / `apply_full_replace` 全量替换）把远端记录的文件树落盘到 Capsule 的 `repos/<name>/` 与 `artifacts/`。若恢复逻辑只检查目标路径**字符串**是否包含 `..`，则无法防御这样的攻击：先在工作树内创建一个 symlink（例如 `repos/ieops/evil -> ../../runtime-state`），字符串本身不含 `..`，但该 symlink 的**父目录**在恢复时会被“跟随”到 Capsule 根之外，导致后续对 `repos/ieops/evil/execution.json` 的写入实际落在 `runtime-state/execution.json` 上，绕过 §6.2 的只读权限位保护。

**权威规则**：

1. **逐级检查父目录，不看最终路径字符串**：对恢复落盘的每一个目标路径 `repos/<name>/a/b/c`，从 Capsule 根开始逐级 `lstat`：`repos/`、`repos/<name>/`、`repos/<name>/a/`、`repos/<name>/a/b/`——每一级都必须是**普通目录**（`lstat` 结果非 symlink）。只要有一级是 symlink，立即拒绝整次恢复操作，返回 `CAP-406 SYMLINK_ESCAPE_DENIED`，不尝试解析该 symlink 指向何处后继续。检查最终路径字符串是否包含 `..` 不能替代这一步，因为符号链接可以不依赖 `..` 而直接把相对路径重定向到任意位置。
2. **跨 Capsule 根的 symlink 目标一律拒绝创建**：若 Revision 数据本身声明了一个 symlink 类型的文件条目，恢复逻辑必须先解析其目标（相对于其所在目录），若解析结果（无论是否规范化后的绝对路径）落在 `<capsule>/` 根之外，则拒绝创建该 symlink，返回 `CAP-406`；即使目标恰好仍在 Capsule 根内但落在 `runtime-state/` 或 `sync/` 之内，同样拒绝（这两个目录不允许被任何机制指向）。
3. **整体原子性**：`apply_ops`/`apply_full_replace` 一旦检测到任一条目触发规则 1 或规则 2，整次恢复中止，不部分落盘——已写入的条目回滚（`05-sync-revision-recovery.md#§6` 的先数据后状态原则同样适用于本地恢复，不产出半恢复的 Capsule 现场）。

**测试场景（对应 §10 的可注入故障之一）**：

1. 正常初始化一个 Capsule，`repos/ieops/` 检出完成。
2. 在 `repos/ieops/` 内创建符号链接 `evil -> ../../runtime-state`（字符串不含 `..` 之外的可疑片段，仅一级 `..`，属于合法相对路径写法）。
3. 触发一次 Revision 恢复，其中声明的目标路径为 `repos/ieops/evil/execution.json`（意图覆盖真正的 `runtime-state/execution.json`）。
4. **预期**：规则 1 在逐级检查到 `repos/ieops/evil` 这一级时发现它是 symlink，立即以 `CAP-406 SYMLINK_ESCAPE_DENIED` 拒绝整次恢复；`runtime-state/execution.json` 内容不受影响；`repos/ieops/evil` 本身也不被跟随写入。

### 6.4 命令执行流程（编号步骤，非伪代码） <!-- §6.4 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（持久 WI Agent、CAP-* 码、RunCommand）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

1. WI Agent 根据当前 step 的声明（`01-workflow-skills.md#§4.1`）与该 step 需要驱动的 harness-native subagent，构造 `RunCommand.spec`：`cwd` 取 `repos/<name>` 或 `artifacts`，`env` 为基线环境（不含 `runtime-state` 路径或 AIHub 凭证），`timeout_ms` 按 step/workflow 预算折算。 〔#111分类：删→驱动〕
2. Native runner 以普通子进程方式启动 `argv`，工作目录设为 `<capsule>/<cwd>`。
3. 子进程对 `repos/`、`artifacts/`、`logs/` 的读写按 §5.2 权限面正常放行；对 `runtime-state/` 的任何写尝试按 §6.2 权限位机制以 `EACCES`/`CAP-403` 失败。
4. 超时或进程组异常退出：Native runner 按 §7 回收进程组，结果标记为基础设施失败（`01-workflow-skills.md#§6.3`），不产出业务判定。
5. 正常退出：`Result{exit_code, stdout, stderr, killed=false, duration_ms}` 返回给 WI Agent；WI Agent 据此更新 `runtime-state/execution.json`（§4.2，原子写）。 〔#111分类：删→驱动〕

## 7. 并发与崩溃恢复 <!-- §7 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（持久 WI Agent、owner generation、Revision/CAS/manifest）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

- **单写者域**：`runtime-state/execution.json` 只被该 WI 的**执行驱动**写（INV-3 + INV-6；驱动是 Daemon 进程内的 goroutine，`pf daemon -use-driver` 默认路径；旧引擎下则是该 WI 的 WI-Agent 进程——历史记录）；同 capsule 内同一时刻至多一个 step 的 subagent 在跑（`01-workflow-skills.md#§6.1` 顺序语义）；watcher 读取 `runtime-state/` 与工作树不阻塞 subagent（读侧无锁，Revision 摘要以暂存快照为准，`05-sync-revision-recovery.md#§6`）。〔#111分类：已重写→驱动〕
- **双实例竞态**：§2.4 裁定由 generation fencing 排除；本地残留的旧目录在 `capsule_create`（§6.1）时被隔离，不参与竞态。
- **物化产物与崩溃续跑**：`.pi/agents/`、`.claude/agents/`、`workflow.md`（或 `workflow/`）是本机可重建的只读快照（§4.5），不参与同步、不随崩溃失效。驱动路径下物化由 Daemon 的运行时 F7 lane 执行（`runtime/materialize.go`：原子整组物化 + `sync/materialized.json` 完成标记；旧引擎由 WI-Agent 第 0 步承担——历史记录）。**同 generation 崩溃续跑复用既有快照**（快照只读，INV-1a）：重新推导出的驱动**不重读本机 checkout**——仅当快照**缺失**（上次物化从未成功落盘，即完成标记不在）才重新物化；快照存在而与 pin digest 不一致时**告警、继续用既有快照**（本代已被接受的执行基准，不因 checkout 后续漂移而更换）。**跨 generation/跨机重建**才重新物化并按 pin digest 校验（漂移告警继续，INV-8）。不依赖任何进程内存，也不从 Revision 重建。〔#111分类：已重写→驱动〕
- **驱动崩溃后基于 `execution.json` 续跑（权威恢复路径）**：驱动不持有任何进程内存——「崩溃」= Daemon 进程死亡，或驱动 `Run` 返回基础设施失败；两种情形都由 Daemon 的恢复通道从 `execution.json` **重新推导驱动**（re-derivation），不存在「重新派生一个 WI-Agent 进程」这一步（旧引擎的「检测 WI-Agent 进程退出」检测步在驱动模型下无对应物——历史记录）：
  1. Daemon 读取该 capsule 的 `runtime-state/execution.json`，取 `current_step` 与其 `steps.<current_step>.status`。
  2. 若 `status == "in_progress"`：恢复通道按 `01-workflow-skills.md#§6.3` 基础设施失败语义**原地重派当前 step**，不重放已 `done` 的 step，不要求从零重跑整个 workflow（崩溃安全的关键序：step_start 写先于 step 进程存在，故「写完启动记录、未出结果」的崩溃留下的就是 in_progress 现场，重派路径按 resume 语义续跑）。若该 step 的 `pending_question` 非空（崩溃发生在 needs_input 等待中），按 §4.2 恢复：pending question 是持久的，重新推导出的驱动在同一问题上再次停车，按原 `question_id`/`question` 原样重新呈现同一问题（不重新生成 `question_id`），回答经 answer API 跨重启照常可送达——**无任何 agent ack**（旧引擎的「新 WI-Agent 进程消费 `pending_answer` 后续跑」机制随 WI-Agent 引擎退役——历史记录）。
  3. 若 `status == "pending"`（典型场景：rewind 已落盘、目标 step 派发前崩溃，即 §4.2 `pending_rewind_input` 对应的确定性恢复场景）：恢复通道检查其 `pending_rewind_input`——非空则把判定全文组装进派发输入，并在 `pending → in_progress` 的同一次写入中清空该字段——随后正常派发该 step；判定全文的到达不依赖任何进程内存或旁路传递（`01-workflow-skills.md#§6.2` 第 6 步）。
  4. 若 `status == "done"`（恰好在写完结果、发布前崩溃）：直接推进到 `01-workflow-skills.md#§6.5` `next_step_by_order` 给出的下一个 step；若因此发现本地已完成但尚未发布的内容，交由下一次触发时机（`00-architecture.md#§4.3`）补发。
  5. 若 `workflow_status` 已是终态（`completed`/`failed_gate`/`failed_rewind_exhausted`）或 `blocked`：不驱动任何 step，只做一次对账发布（terminal 态）或等待人工处理（`blocked` 态）。
  〔#111分类：已重写→驱动〕
- **机器整机重启**：Daemon 启动时扫描 `capsules/`，对每个 generation 匹配的 capsule 按 `05-sync-revision-recovery.md#§7.4` 恢复：`runtime-state/` 中最后一次已发布 Revision 之后的改动若未发布，标记为「本地脏 + 未发布」，按上面第 3 步续跑当前 step。
- **Native runner 进程崩溃**（区别于驱动自身失败）：子进程死亡 → 按超时/kill 路径回收进程组，结果记为基础设施失败（`01-workflow-skills.md#§6.3`），驱动自身继续存活并按 resume 语义重派。〔#111分类：已重写→驱动〕

## 8. 安全边界 <!-- §8 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（持久 WI Agent、CAP-* 码、Revision/CAS/manifest、publish_request）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

- **信任面**（对齐 `00-architecture.md#§3.7`）：v1 面向可信内部仓库，harness-native subagent 执行的一切命令被视为**可信代码**——不存在需要防御「蓄意逃逸」的威胁模型；文件系统权限面（§5.2/§6.2）防御的是误写与实现 bug，不是对抗性绕过——host-native 同 UID 下不构成安全边界（B02 口径，§5.2/§6.2）。
- **v1 明确不构成安全边界组成部分的机制**（完整清单与逐字裁定见 §1.2，不在此重复；不得在验收标准中出现）。
- **`runtime-state/` 写保护**（INV-6）：唯一被机制化保护的路径，方式为只读权限位（§6.2），失败码 `CAP-403`。
- **symlink 逃逸防御**（INV-6c）：Revision 恢复落盘的逐级父目录检查与跨根 symlink 拒绝（§6.3），失败码 `CAP-406`。
- **凭证隔离（D3）**：Core API key（AIHub 凭证）与 Revision 发布 token 只存在于 Daemon 进程内/数据目录，从不进入 Capsule 目录或 subagent 的 env/cwd/argv（`04-local-runtime.md#§8`）；**WI Agent 同样不持有任何 Core API key 或 Revision 发布 token**（§2.2，`04-local-runtime.md#§5.3`/§8）——其全部 Core 访问经 Daemon 本地代理中转，全部发布经 `publish_request` 由 Daemon 机械执行。 〔#111分类：删→驱动〕

## 9. 监控 <!-- §9 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（CAP-* 码、Revision/CAS/manifest）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

- `CAP-403` 写拦截次数（>0 即值得审计——subagent 试图写 `runtime-state/`，即便 v1 不视为攻击，也说明脚本行为超出预期）。
- `CAP-406` symlink 逃逸拦截次数（>0 必须审计：Revision 数据或本地工作树出现了指向 Capsule 根之外的 symlink）。
- Native runner 命令超时率、`CAP-500` 崩溃率；Capsule 磁盘占用（告警阈值）；隔离目录（`_quarantine/`）累计大小（长期未清理即提醒走删除保护流程）。

## 10. 测试与故障注入 <!-- §10 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（持久 WI Agent、owner generation、CAP-* 码、Revision/CAS/manifest、flush 证据、RunCommand）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

- 身份/绑定：篡改 `capsule.json.generation` → `capsule_create` 返回 `CAP-409`。
- inventory：克隆/检出失败 → `DAEMON-507`（typed 的任务 repo 克隆失败，关闭为 blocked）。
- **INV-6 写保护测试（B02 误写口径）**：subagent 进程内尝试 `echo x > runtime-state/execution.json`（预期 `CAP-403`/`EACCES`）——这是**防误写**断言，保留；对「不可见」面（`capsule.json`/`sync/`/Daemon state）**不做**「读取必然失败」的验收断言——host-native 同 UID 下这些路径物理可读，「不可见」是工作目录约定层面的构造性保证（§5.2/§6.2 的 B02 口径），不是可断言的运行时边界。机械可断言的只有构造面本身：WI Agent 构造的 `RunCommand.spec` 从不携带 `repos/<name>`/`artifacts` 之外的 `cwd`，且 `env` 中不含 `runtime-state` 路径与任何凭证（§5.1）。 〔#111分类：删→驱动〕
- **symlink 逃逸测试（B07，§6.3 场景的机械化版本）**：①创建 Capsule 并完成 inventory 检出；②在 `repos/<name>/` 内创建 `evil -> ../../runtime-state`；③触发一次声明目标为 `repos/<name>/evil/execution.json` 的 Revision 恢复；④断言返回 `CAP-406`，`runtime-state/execution.json` 内容与恢复前逐字节相同，且恢复操作未部分落盘任何其它文件。
- 超时命令：预期进程组被杀、`killed=true`，结果记基础设施失败。
- 机器重启后残留未发布改动：预期按 §7 恢复流程标记为「本地脏 + 未发布」并续跑。
- **D8 确定性恢复测试**：① rewind 落盘后、目标 step 派发前 kill WI Agent → 新 WI Agent 从 `pending_rewind_input` 机械重建派发输入（gate FAIL 判定全文到达 target），并在派发写入中清空该字段；② needs_input 边界（D9）：step 置入等待的写入落盘后 `pending_question` 非空且含 `content`，回答经 Daemon answer API 到达后 `pending_answer` 非空，续跑写入后 `pending_question` 为 `null` 且 `answers` 末条以同一 `question_id` 归档了 `{answer_text, answered_at, answered_by}`（修复 B9），此后同一 `question_id` 的重复回答被拒（`DAEMON-409`，`00-architecture.md#§3.4a`）；③ per-step `budget_consumed` 与顶层聚合一致，且一次 rewind 后的重跑消耗归属目标 step。 〔#111分类：删→驱动〕
- **D13 cancel 三类路径测试**：`queued`/`paused`/`blocked` 直接取消成功且零发布动作；`running` WI 未经正常收尾直接调用 cancel 被拒（`02-aihub-core.md#§6.5`），先正常收尾（flush 落定）再取消成功；stale 代被接管后旧 Daemon 侧零发布、本机目录进 quarantine。
- **待验证项（Claude 适配器，Pilot 前必须实测）**：`.claude/agents/` 在**非 git 根**的 capsule 目录下的 agent 发现行为——capsule 根本身不是 git 仓库根（git 根在各 `repos/<name>/` 内），Claude 定位项目级 agent 目录是否依赖 git 根、能否按 cwd 向上查找，尚未经源码或实测确认；若实测结论是「不做 git 根无法发现」，须在 claude 适配器层提供显式装载路径（把定义内容注入会话）作为替代。该项验证不得顺延到联调阶段之后。
- **物化与防冲突测试（§4.5）**：① capsule 创建后断言 `.pi/agents/` 目录存在（可为空）；② 在工作区级放置一个同名 `.pi/agents/` 定义，从 capsule 内派生 subagent → 断言解析命中的是 capsule 内定义（agentScope="project" + 项目级覆盖 + 空目录防泄漏三层共同生效）；③ 物化源缺 bundle/缺文件 → 断言产生告警且执行继续（不硬拒；不做内容校验，polyforge#149 B9）；④ subagent 输出不符合定义文件 `output` schema → 断言按基础设施失败处理（`01-workflow-skills.md#§6.3`），不产出业务判定。

## 11. 迁移与 rollout <!-- §11 -->

- 无存量迁移（新系统）。v1 引入顺序：Capsule 目录布局与 `capsule.json` → repo inventory 检出 → `runtime-state/execution.json` 读写与原子提交 → Native runner（Linux 与 macOS 同步落地，无「先 Linux 后 macOS」的阶段差，因为两平台是同一形态）→ symlink 安全规则的恢复落盘联调（与 `05`）→ 契约测试矩阵（总序 `07-deployment-pilot.md#§11`）。
- 升级 `runtime-state/` 权限位方案或恢复落盘规则属于安全面变更，必须双平台同步评审 + §10 测试全绿。

### 11.1 未来 untrusted runner（非 v1 范围，仅一句话提及） <!-- §11.1 -->

v1 只有 Native runner 一种执行 profile；若将来需要执行不可信第三方代码，untrusted runner 是另行立项的非一期演进方向（`00-architecture.md#§1.2`/§3.7/INV-12）。

## 12. 未决问题 <!-- §12 -->

1. **不受信外部仓库的 untrusted runner 详细设计**（§11.1 方向）：一期不引入，何时立项开放。
2. **`runtime-state/` 只读权限位方案在极端并发下的窗口**（WI Agent 短暂切回可写态写入时，是否存在竞态窗口让同进程内的子上下文抢先写入）：按 §5.2/§6.2 的 B02 口径，权限位本就是**防误写**机制而非安全边界，该窗口同属防误写层面的残余风险，不构成新的安全暴露；v1 依赖「可信代码」假设接受该窗口的存在，是否需要加锁开放。 〔#111分类：删→驱动〕
3. **接管后旧 subagent 的外部副作用边界**（§2.4）：fencing 保证写入 Core 的安全，但不保证 step 内容调用的外部 API（如外部 PR/邮件）在接管后立即停止或幂等；一期依赖旧 Daemon 自省快速停止（协作行为，非安全保证）降低窗口，是否需要 step 声明幂等性开放。
