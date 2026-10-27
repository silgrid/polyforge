# 02 — AIHub Core：SQLite Core schema、单 active Service、owner generation 与进度 read-through

> **ARCHIVED 2026-10-16** (repo review 2026-10-16 wave 4): describes the v0/v1 design and is kept for history only — do not build from it. Current reference: [reference.md](../reference.md); decisions: [v2-converge.md](../v2-converge.md). Live mechanisms this file still held were moved there before archiving; section anchors are unchanged.

> **ARCHIVED（repo review 2026-10-16 G-1）— 本文大部分描述已删除的机制：owner generation + HMAC token、flush 证据（CORE-425）、Revision/CAS 登记、预算增量、凭证轮换、26 端点清单（11 行无路由、6 条真路由未列）。标有 ARCHIVED 横幅的节 do not build from this；未标横幅的节也只作历史与溯源参考。现行设计权威：[v2-converge.md](../v2-converge.md)、[wi-kind-contract.md](../wi-kind-contract.md)、[08-capsule-base-migration.md](../08-capsule-base-migration.md)；与代码冲突时一律以代码为准。**
> Most of this document describes deleted mechanisms. Sections bannered ARCHIVED must not be built from; the live authority is v2-converge.md plus the code.

| 权威项 | 值 |
|---|---|
| 文档定位 | Core 数据集、完整 SQLite DDL、assignee/waiting_on、attrs 拆分、workflow 引用（bundle_name；digest 列已删，polyforge#149 B9）、parent_id（epic-lite）与依赖边、project membership 权限硬边界、单 active Service、owner generation、进度 read-through、AIHub 本地目录布局、Core API 与错误码 |
| 上游基线 | `00-architecture.md`（全部术语与不变量的唯一权威基线；本文只交叉引用，不重复定义） |
| 术语权威 | 本文负责：Core、单 active Service、owner generation、assignee/waiting_on、attrs 拆分、workflow 引用（bundle_name）、parent_id（epic-lite）与依赖边、project membership 权限硬边界、进度 read-through、AIHub 本地目录布局 |

> **执行模型勘误（polyforge#111 删除 pass，2026-10-09）**：Phase-3 引擎切换（`d9bb866`/`f2163fe`）后，默认执行模型是
> Daemon 进程内的 **Go 执行驱动**（`internal/polyforge/driver`，`-use-driver` 旗标现已删除，driver 是唯一引擎）；WI-Agent 派生进程
> 旧引擎及其 `-use-driver=false` 开关均已删除。本文写于 WI-Agent 模型，正文不整篇重写；
> 每处「WI Agent」引用按下列行尾标记分类（可机械检索 `#111分类`）：
>
> - **〔#111分类：删→驱动〕** —— 该行所述的 WI Agent 进程机制在默认引擎下已删除，职责由驱动承接；按「执行者 = 驱动」读，裁定本身仍有效。
> - **〔#111分类：替换后仍成立〕** —— 该行陈述在「执行者 = 驱动」替换下逐字仍成立。
> - **〔#111分类：历史记录〕** —— 引用裁定/历史文档的原文或历史命名，按原样保留，不代表当前执行模型。
>
> **未清理声明**：本 pass 只覆盖 02/03/05 三文；`00-architecture.md`（86 行）、`01-workflow-skills.md`（27 行）、
> `04-local-runtime.md`（100 行）、`07-deployment-pilot.md`（22 行）仍携带未分类的 WI-Agent 引用，不属于 polyforge#111 的验收范围。

## 1. 范围与非目标 <!-- §1 -->

### 1.1 范围 <!-- §1.1 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（owner generation、Revision/CAS/manifest、flush 证据、read-through）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

- 一期唯一持久层 SQLite 的完整 Core schema DDL（§4），覆盖 work item 基本字段（含 parent_id（epic-lite）、milestone、assignee、workflow 引用三列与预算基线，§4.2、§4.2a、§4.4）、WI 间依赖边（§4.2a）、project membership 与权限硬边界（§4.10）、assignee/waiting_on 语义与状态迁移、attrs 拆分原则；DDL 中不含任何执行态列（`current_step`/`step_attempt`/`review_round` 等一律不出现）。
- 单 active Service 的含义与保证（§2.2/§7）；owner generation 的存储与生命周期（§2.3/§6.2）。
- 进度 API 的 read-through 语义（§5.2）：代理读取远端 Capsule 已发布 Revision 中的 `runtime-state/execution.json` 内容，Core 自身不落任何执行态（`00-architecture.md#§2.3` INV-1）。
- Core 暴露的 API 与错误码全表（§5）；AIHub 本地数据目录布局（§4.11）。
- 一期外部依赖：仅 SQLite + 本地目录（`00-architecture.md#§2.3` INV-10），不依赖 PostgreSQL/JuiceFS/S3。
- 三类此前遗留缺口的完整契约：generation 收尾的 flush 证据协议（§6.4）、非活跃 WI 的终止路径（§6.5）、凭证轮换后的重新授权（§6.6）、超预算后的增加预算 API（§6.7）。

### 1.2 非目标 <!-- §1.2 -->

- 不设计 Revision 数据模型与发布协议（权威见 `05-sync-revision-recovery.md#§4`、`#§5`；本文只定义其 Core 侧的登记表，§4.7）。
- 不设计 Memory 文件格式与检索（权威见 `06-memory-search.md`；本文只定义 Core 侧元数据表，§4.9）。
- 不设计多 active / HA / 主备切换。
- 不设计 workflow.md frontmatter 校验与执行语义（权威见 `01-workflow-skills.md`；Core 只落 WI 记录中的 workflow 引用三列与预算基线（§4.4）以及预算增量（§4.5a），不存储 resolved steps 或任何解析结果）。
- 不设计 step skill / subagent 定义内容的任何管理面：存储、校验、分发均属 harness 层事务，AIHub 完全退出该职责（§4.4 裁定）。
- 不给出量化的历史测试指标、故障案例日期；测试小节只列类别，权威回归清单在 `07-deployment-pilot.md`。

## 2. 术语与不变量 <!-- §2 -->

### 2.1 Core（权威定义） <!-- §2.1 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（Revision/CAS/manifest、read-through）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

**Core = AIHub 持有的持久化数据集合**：具体为 §4 的全部 SQLite 表，加上 §4.11 目录中由 AIHub 管理的文件（Memory 权威 Markdown、Revision 数据文件、最终结果归档）。Core 的边界由 `00-architecture.md#§2.3` INV-1 锁定：**不含任何执行态**（current step、step 结果、review 轮次、预算消耗只存在于 Capsule 的 `runtime-state/execution.json`）。

需要澄清的表面矛盾：Core 确实持有 Revision 数据文件（`05-sync-revision-recovery.md#§4` 权威），而 Revision 里可能包含 `execution.json` 的历史快照——这不违反 INV-1，因为 Core 对这些文件是不解析的不透明字节档案：没有任何 SQLite 列或 API 字段以结构化方式暴露 `current_step`/`step_attempt_id` 等执行态语义（§2.6 INV-2c 已将此列为写路径的硬性禁止）。唯一对外读取路径是 §5.2 的 read-through：把整份 Revision 当作不透明 byte blob 重建后，原样透传其中 `runtime-state/execution.json`（`03-execution-capsule.md#§4.2` 当前权威 schema）的完整内容作为对外可见的「进度」，不额外解析、不裁剪字段、不在 Core 自己的数据模型中重新表示它；`execution.json` 当前 schema 未定义独立命名的 `progress` 字段，若 `03` 未来新增该字段，本节的透传对象相应收窄为该字段本身，接口形状不变。「持有 Revision 文件」（存储层面）与「含有执行态」（数据模型/API 层面）是两件事，INV-1 约束的是后者。

### 2.2 单 active Service（权威定义） <!-- §2.2 -->

**单 active Service**：一个 AIHub 部署中，任何时刻至多一个 aihub 进程可写 Core（`00-architecture.md#§2.3` INV-2）。保证手段三层：

1. **部署层**：一期一台主机一个 systemd 单元，systemd 保证单实例。
2. **文件锁层**：进程启动时对 `<data_dir>/service.lock` 取排他非阻塞锁；取锁失败即退出（`CORE-508 LOCK_HELD`）。
3. **Core 层**：service 自注册行（§4.10）记录 `service_id` 与心跳时间戳；所有写 API 校验调用方携带的 `service_id` 等于当前注册行，不等即拒绝（`CORE-509 STALE_SERVICE`）——防止「旧进程未死透、新进程已接管」的双写窗口。

### 2.3 owner generation（权威定义：概念与生命周期；存储见 §4.3） <!-- §2.3 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（owner generation、Revision/CAS/manifest、takeover）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

**owner generation**：WI 归属权的单调递增整数，每发生一次认领（claim）或接管（force-takeover）加一。第 g 代 owner 是 `[g, g+1)` 时间窗内该 WI 的唯一执行权持有者。保证（`00-architecture.md#§2.3` INV-3 的 Core 侧）：

- 同一时刻只有一个 generation 处于活跃态（`owner_generations` 中 `ended_at IS NULL` 的行至多一条，由部分唯一索引在存储层强制）。
- generation 递增由单 active Service 在事务内完成。
- 过期 generation 的副作用被 fencing 拒绝：Revision/HEAD 发布（权威见 `05-sync-revision-recovery.md#§2.9`）、Core 写 API 携带的 generation 凭证校验（§5.1）。
- **生命周期**：`claim` 使 generation+1、行状态转为 `active`；`pause`/`complete`（wrap/fail）/`block` 均使当前活跃行状态转为 `ended`（`ended_at` 落值，`end_reason` 对应取值）。`cancel` **不**出现在 end_reason 取值中：它只作用于本就没有活跃 generation 的 `queued`/`paused`/`blocked` WI（§6.5），不存在「cancel 结束一个活跃行」的路径（running WI 必须先 pause/block 结束 generation，再按 §6.5 取消）。`unblock` 与 `resume` 都不是让旧行重新活跃，而是各自触发一次新的 claim（generation+1）——因为旧 generation 已经结束，重新获得执行权必须走新代，这与「接管后重新获得执行权」的语义保持一致，不引入第二种重新活跃路径。历史行永久不可变，作为审计记录保留。

### 2.4 assignee / waiting_on（权威定义） <!-- §2.4 -->

- **assignee**：当前对 WI 拥有执行权的身份（human 或 machine 的 user id）；活跃 generation 行的 owner 即当前 assignee。历史 assignee 可从 `owner_generations` 追溯。
- **waiting_on**：WI 当前等待的对象，正交于状态机的状态标注，人工/客户端写入（`PATCH`），取值域：`owner`（等人决策/输入）、`dependency:<slug>`（人工注记的等待对象）、空值（无需等待）；`gate` 与 `runtime:*` 已删除（polyforge#149）。`waiting_on` 非空是「被阻塞」的可见信号；就绪队列据此过滤（§5.1）。`blocks` 依赖边**不**写 `waiting_on`：依赖等待由派生只读字段 `waiting_for` 呈现（§4.2a）。

### 2.5 状态机（WI 生命周期，Core 权威） <!-- §2.5 -->

> **PARTIALLY ARCHIVED（2026-10-16）— 状态集合（`queued`/`running`/`paused`/`blocked`/`wrapped`/`failed`/`canceled`）与 claim / pause / resume / block / wrap / fail / cancel 迁移是现行机制。**已删除、do not build from this 的部分**：「对活跃 generation 的影响」一列（现为 session 模型：WI 行上的 session hash/fingerprint 与 `clean_pause`，paused 保留 session）、`force-takeover` 行（已删，替代为 `force-end-session` + 重新 submit）、`unblock`（blocked 的继续走 `resume-blocked` 门）、事件 payload 中的 `generation`。

状态集合：`queued`、`running`、`paused`、`blocked`，以及三个终态 `wrapped`、`failed`、`canceled`。合法迁移：

| 迁移 | 前置状态 | 后置状态 | 对活跃 generation 的影响 |
|---|---|---|---|
| `claim` | `queued` | `running` | 开启新 generation |
| `pause` | `running` | `paused` | 结束当前活跃 generation |
| `resume` | `paused` | `running` | 新一次 claim（开启新 generation） |
| `block` | `running`/`paused` | `blocked` | 结束当前活跃 generation（`paused` 起点仍持有会话：`End()` 按同一 session-hash fence 校验后直接落 `blocked` 并清空会话，无需先 `resume`） |
| `unblock` | `blocked` | `running` | 新一次 claim（开启新 generation） |
| `wrap`/`fail` | `running`/`paused` | `wrapped`/`failed` | 结束当前活跃 generation（`paused` 起点仍持有会话：`End()` 按同一 session-hash fence 校验后直接落终态并清空会话，无需先 `resume`） |
| `cancel` | `queued`/`paused`/`blocked` | `canceled` | 本就没有活跃 generation（§6.5） |
| `force-takeover` | `running`（另一 machine 持有） | `running` | 结束旧活跃 generation，开启新 generation |

`blocked` 的进入原因写入 `waiting_on` 与生命周期事件（§5.1a 枚举内的通用 `blocked` 事件，`{generation, reason?}`）；`paused` 与 `blocked` 的区别：`paused` 是人主动挂起（无外部等待对象），`blocked` 是有明确等待对象（`waiting_on` 非空）。**`paused` 直接收尾（D14 裁定，polyforge#150，以代码为准）**：`paused` 的 WI **保留其会话**（pause 不清空 session 行），因此可**直接**迁移到 `wrapped`/`failed`/`blocked`：`End()` 对 `paused` 起点与 `running` 起点走同一 session-hash fence（hash 不匹配即 `CORE-403 STALE_SESSION`），校验通过后落状态并清空会话，无需先 `resume`——上表 `wrap`/`fail`/`block` 三行的前置状态因此含 `paused`，行 9 `session-end` 对 `paused` 起点同样合法；`cancel` 亦早已允许从 `paused` 直接终止（§6.5）。workflow 侧的 `failed_gate`/`failed_rewind_exhausted` 终态（`01-workflow-skills.md#§6.5`，权威落盘在 Capsule 的 `execution.json.workflow_status`，`03-execution-capsule.md#§4.2`）在 Core 侧统一映射为业务态 `blocked`（`waiting_on=owner`），不映射为 Core 的终态——workflow 失败与 WI 失败是两层分离的概念（`00-architecture.md#§3.2` 归属判据）。**Core 只落这一层映射后的业务信号本身**：`status=blocked`、`waiting_on=owner`，不持久化「具体是哪一种 workflow 终态导致」这一区分——那是 workflow/Capsule 侧的执行态语义，只存在于 `execution.json`，Core 不复制、不新增列或事件类型来表示它（INV-2c）。终态行只读；对终态 WI 的任何写 API 一律返回 `CORE-410 TERMINAL_STATE`。

### 2.6 本文新增不变量 <!-- §2.6 -->

- INV-2a：Core 写路径全部经单 active Service 的写事务，不存在旁路直连 SQLite 的写客户端。
- INV-2b：`owner_generations` 的活跃行唯一性由部分唯一索引在存储层强制，不依赖应用纪律。
- **INV-2c（执行状态写入面封堵）**：`wi_events.payload`（自由 JSON）与 `work_items.attrs`（自由 JSON）禁止承载执行态字段。Core 在写路径上做闭集校验：
  - `POST .../events` 的 `event_type` 必须属于 §5.1a 附录枚举（部署/生命周期/审计类事件），枚举之外一律 `CORE-400 UNKNOWN_EVENT_TYPE`；枚举内每个 `event_type` 都绑定一个固定 payload schema（§5.1a），出现执行态键名（词表同附录）一律 `CORE-400 EXECUTION_STATE_REJECTED`。
  - `POST .../work-items`（创建，body 可选携带 `attrs`）与 `PATCH .../work-items/{id}`（更新）两条写路径，对 `attrs` 顶层键名做同一词表黑名单校验；命中即 `CORE-400 EXECUTION_STATE_REJECTED`。`attrs` 只用于 §4.2 拆分原则之外的业务级自由属性，不是执行态的后门；创建与更新共用同一份校验函数与同一份词表（§5.1a），不因“新建时还没有历史状态”而豁免创建路径的校验。
  - 两处校验共用 §5.1a 的同一份执行态键名词表，新增执行态概念必须先扩词表再实现。
- INV-2d：`claim_idempotency` 命中判定必须同时校验 `idempotency_key`、调用方身份（`caller_identity`）与 `work_item_id` 三者一致，缺一不可（§6.6 详述），防止跨身份或跨 WI 误命中缓存响应。
- INV-2e：WI 的 workflow 引用（`work_items.bundle_name`，§4.4）为**首次认领冻结**：R2 窗口（status=queued ∧ 从未认领）内可经 `PATCH` 三向变更（NULL↔非 NULL 含解绑，与创建共用同一落库路径 §4.5），首次认领后即不可变（不存在认领后的 UPDATE 路径）。digest 列与预算基线列已删（polyforge#149）；预算的运行期口径由执行侧账本（`runtime-state/execution.json` 的 `budget_consumed`）承担。

## 3. 模块与代码目录 <!-- §3 -->

对应 `00-architecture.md#§3.3` 目标仓库布局中的 `internal/core/`：

- `internal/core/schema/`：DDL 定义与迁移版本（§11）。
- `internal/core/workitem/`：WI 状态机、assignee/waiting_on（§2.4/§2.5）。
- `internal/core/owner/`：owner generation 仲裁、生成/校验凭证（§2.3、§6.6）。
- `internal/core/service/`：单 active 自注册与文件锁（§2.2）。
- `internal/core/store/`：表存取、事件追加。
- `internal/server/`：HTTP API 面（§5），只调 `internal/core`，不直写 SQL。
- `cmd/aihub/`：入口。

## 4. 数据结构：SQLite Core schema 完整 DDL <!-- §4 -->

- 数据库文件 `<data_dir>/aihub.db`，`journal_mode=WAL`、`foreign_keys=ON`、`busy_timeout=5000`。
- 全部时间戳为 RFC3339 字符串（UTC）。
- 审计声明：下列 DDL 中不存在任何 `current_step`/step attempt/review round/预算消耗类列或表；进度查询端点为代理读取（§5.2）。

### 4.1 概览 <!-- §4.1 -->

| 表 | 职责 |
|---|---|
| `service_secret_epochs` | service_secret 轮换历史（§6.6） |
| `projects` / `project_members` | project 定义（精简）与成员关系——权限硬边界的机械判据（§4.10） |
| `users` / `api_keys` / `machines` | 身份、密钥（可带可选 project scope）与机器注册（§4.10） |
| `work_items` | WI 记录，含 parent_id（epic-lite）、milestone、workflow 引用三列与预算基线（§4.2、§4.4）；`repo_inventory` JSON 列（§4.6） |
| `wi_dependencies` | WI 间依赖边（blocks/supersedes/related；防环与 epic 父子约束，§4.2a） |
| `owner_generations` | 归属权代数（§4.3） |
| `claim_idempotency` | claim 幂等硬化（§4.3、§6.6） |
| `workflow_budget_increments` | 预算增量的独立可追加记录（§4.5a、§6.7） |
| `wi_events` | WI 事件流（§4.7） |
| `capsule_revisions` / `capsule_heads` / `capsule_tombstones` | Revision 登记、HEAD 指针与删除保护（§4.8） |
| `memories` / `memory_fts` | Memory 元数据与派生检索索引（§4.9） |

### 4.2 `work_items`（基本字段、assignee/waiting_on、attrs 拆分） <!-- §4.2 -->

```sql
CREATE TABLE work_items (
  id           TEXT PRIMARY KEY,            -- "wi_" + 12 位随机
  seq          INTEGER NOT NULL,            -- 展示序号（如 "aihub#793" 中的 793）；唯一范围 = 项目内（D2 裁定，
                                             -- polyforge#150：以代码为准 UNIQUE(project_id, seq)，非全局唯一）
  project_id   TEXT NOT NULL REFERENCES projects(id),
  goal         TEXT NOT NULL,               -- ≤500 字符
  content      TEXT,                        -- markdown 背景，可空
  parent_id    TEXT REFERENCES work_items(id), -- epic-lite 父票引用；写路径约束见 §4.2a，随创建写入后不可变
  status       TEXT NOT NULL DEFAULT 'queued'
               CHECK (status IN ('queued','running','paused','blocked',
                                 'wrapped','failed','canceled')),
  priority     TEXT NOT NULL DEFAULT 'normal'
               CHECK (priority IN ('low','normal','high','urgent')),
  milestone    TEXT,                        -- 里程碑标签，可空；列表/过滤经 idx_wi_milestone
  labels       TEXT NOT NULL DEFAULT '[]',  -- JSON array[string]
  attrs        TEXT NOT NULL DEFAULT '{}',  -- JSON object（业务级自由属性，受 INV-2c 黑名单约束）
  reporter     TEXT NOT NULL REFERENCES users(id),
  assignee     TEXT REFERENCES users(id),   -- 当前活跃 generation 行的 owner（§2.4）
  waiting_on   TEXT,                        -- §2.4 取值域；非空 = 阻塞可见信号
  bundle_name  TEXT,                        -- workflow bundle 名；NULL = 纯登记票（不可 claim、不可 submit，§4.4）。digest 列与
                                             -- 预算基线列已删（polyforge#149 B9/dead-code：引用即名字本身）
  repo_inventory TEXT,                      -- §4.6：JSON [{name}]（项目仓库名），建票冻结；NULL = 无清单
  created_at   TEXT NOT NULL,
  updated_at   TEXT NOT NULL,
  closed_at    TEXT                         -- 终态时间戳
  , UNIQUE (project_id, seq)                -- D2 裁定：seq 项目内唯一（与 §2.4 展示名 <project>#<seq> 的解析一致）
);
CREATE INDEX idx_wi_status      ON work_items(status, priority DESC, created_at);
CREATE INDEX idx_wi_assignee    ON work_items(assignee) WHERE status IN ('running','paused','blocked');
CREATE INDEX idx_wi_waiting_on  ON work_items(waiting_on) WHERE waiting_on IS NOT NULL;
CREATE INDEX idx_wi_parent      ON work_items(parent_id) WHERE parent_id IS NOT NULL;   -- 子票清单查询（§4.2a）
CREATE INDEX idx_wi_milestone   ON work_items(project_id, milestone) WHERE milestone IS NOT NULL;  -- 里程碑过滤/列表
```

**attrs 拆分原则（权威）**：满足任一条件者必须独立成列——①参与状态机或 UNIQUE 约束（`status`、`seq`）；②有高频查询/排序需求且语义稳定（`assignee`、`waiting_on`、`priority`、`milestone`、`project_id`、`closed_at`）；③被外键引用（`project_id`、`reporter`、`parent_id`）。其余一律留在 `attrs` JSON 内，不得为省一次 JSON 提取而升列。workflow 引用（§4.4）按条件②升列：语义稳定（`bundle_name` 首次认领冻结后不可变）且冻结语义须在写路径（R2 窗口门控，§4.5）强制，不适合沉入 `attrs`。一期拆分冻结如上；后续升列必须经 §11 迁移并保持 `attrs` 内旧键读兼容一个版本。

**`wi_type` 删除（裁定）**：`work_items` 不存在任何 WI 类型分类列。WI 的分类需求由两处既有机制承载，不再引入第三处：执行形态由 workflow 引用三列区分（执行票 vs 登记票，§4.4），其余业务分类（如 feature/fix 等）作为 `attrs` 内自由属性携带（受 INV-2c 词表约束），不设列、不承诺过滤索引。

### 4.2a `wi_dependencies` 与 `parent_id`（epic-lite）约束 <!-- §4.2a -->

```sql
CREATE TABLE wi_dependencies (
  blocking_wi_id TEXT NOT NULL REFERENCES work_items(id),  -- 语义上的「被依赖方」
  blocked_wi_id  TEXT NOT NULL REFERENCES work_items(id),  -- 语义上的「依赖方」
  kind           TEXT NOT NULL CHECK (kind IN ('blocks','supersedes','related')),
  created_at     TEXT NOT NULL,
  PRIMARY KEY (blocking_wi_id, blocked_wi_id, kind)
);
```

**kind 语义**：`blocks`——blocking 未 `wrapped` 时 blocked 不入就绪队列、认领被拒（`CORE-409 DEPENDENCY_BLOCKED`）；边本身就是依赖等待的唯一持久化，不物化 `waiting_on`、不迁移状态；`supersedes`——新 WI 取代旧 WI；`related`——纯关联标注，无调度语义。两端不得为同一 WI（`CORE-400`）。

**写路径约束（SQLite DDL 无法表达跨表/条件自引用约束，全部在创建/删改边事务内强制）**：

- **同 project 硬边界**：边两端必须属于同一 project；跨 project 依赖边一律拒绝（`CORE-400`）。无调用方可见性的 project 中的 WI 本身不可见（§4.10），不会成为边端。
- **防环**：`blocks` 边写入前，在既有 `blocks` 边集合上做有向可达性检测，会构成有向环（含自环）→ `CORE-409`（`reason=DEPENDENCY_CYCLE`）；`supersedes` 边同样检测（supersedes 链不得成环）；`related` 无方向语义，不参与环检测。
- **blocker 必须是执行票（polyforge#161）**：`blocks` 边的 blocking 方必须已绑 bundle（`bundle_name` 非空）；登记票与 epic 父票（父票恒为登记票，§4.4）永远不会 `wrapped`，以其为 blocker 一律拒绝（`CORE-400`，`reason=BLOCKER_NOT_EXECUTABLE`，「先绑 bundle」）。这也覆盖「子票等其 epic 父票」的情形；不另设父子专用规则。对称地，仍是任一 `blocks` 边 blocker 的 WI 不得解绑 bundle（`PATCH bundle_name=null` → `CORE-400`，`reason=BLOCKER_STILL_BLOCKS`，列出被阻塞票，先删边）；改绑另一 bundle 仍允许。
- **幂等**：重复添加已存在的边是 no-op，答复当前依赖视图。
- **不做存在性探测**：建边/删边/建票 `after` 中指向调用方不可读 project 的 WI，与不存在的 WI 答同一 `CORE-404`（`work item <ref>`）。
- 删除边即物理删除行；依赖关系变更不新增 `wi_events` 事件类型（`§5.1a` 枚举不变）。
- **只有 wrapped 放行（polyforge#161）**：blocker 为 `failed`/`canceled` 时 blocked 继续等待，不自动放行；只想表达先后顺序时由人删边（`pf file dep rm`）——删边就是覆盖，不设 `--force`。手动 `pf submit` 被阻塞的票同样被认领拒绝，票留在 `queued`。
- **等待可见（只读派生）**：WI 视图（详情与列表）带 `waiting_for: [{slug, status}]`——仅 `queued` WI 的未 wrapped `blocks` blocker，不落库、不是状态，就绪队列分段不变。列边端点的每条边带对方的 `slug` 与 `status`；建边/删边的两端（path `{id}`、body `blocking_wi_id`、path `{blocking_id}`）均接受 id 或 slug。
- **建票即建边**：`POST /api/v1/work-items` 的 body 可带 `after: [id|slug, ...]`（≤20）；每项按上述规则（解析 slug、同 project、blocker 必须是执行票）在建票事务内校验并写入 `blocks` 边，任一项不合法 → 整个建票被拒（类型化错误），什么都不写。新票没有出边，建票时不可能成环。
- **CLI 面**：`pf file create --after <id|slug>`（可重复，随建票 body 发送，原子）、`pf file dep add <blocking> <blocked>` / `rm` / `list <id>`、`pf file show` 渲染依赖块。CLI 只开放 `blocks`；`supersedes`/`related` 服务端暂留、无 CLI 入口。

**`parent_id`（epic-lite）约束（写路径强制）**：

- **深度限 1 层**：子票的父票自身 `parent_id` 必为 NULL（父票不得再有 parent）；违反 → `CORE-400`（`reason=PARENT_DEPTH`）。不存在第 2 层及更深的层级。
- **父票必为登记票（对称规则）**：父票的 `bundle_name` 必为 NULL——已绑 bundle 的票不得作为 parent（`CORE-400`，`reason=PARENT_NOT_REGISTRY`）；对称地，已有子票（任意子票状态，含已终态）的票不得绑定非 NULL `bundle_name`（create 与 `PATCH` 共用同一校验路径，`CORE-400`，`reason=HAS_CHILDREN_NO_BUNDLE`，§4.5）：bundle 只落在叶子上，epic/父票永远是登记票（w140 裁决，`v2-converge.md#§12`）。
- **同 project**：子票 `project_id` 必须等于父票 `project_id`（`CORE-400`，`reason=PARENT_PROJECT_MISMATCH`）。
- **不可变**：`parent_id` 随创建事务写入后不存在任何 UPDATE 路径（`PATCH` 可更新字段集不含它；workflow 三列已改按 R2 窗口处置，见 §4.5）；子票自身可以是登记票或执行票。
- **父不可依赖子**：见上表约束；子票清单查询经 `idx_wi_parent` 索引支撑（§4.2）。

### 4.3 `owner_generations` 与 `claim_idempotency` <!-- §4.3 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（owner generation、takeover）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

```sql
CREATE TABLE owner_generations (
  work_item_id TEXT NOT NULL REFERENCES work_items(id),
  generation   INTEGER NOT NULL,            -- 单调递增，自 1 起
  owner        TEXT NOT NULL REFERENCES users(id),   -- 活跃行 owner == work_items.assignee
  machine_id   TEXT REFERENCES machines(id),
  started_at   TEXT NOT NULL,
  ended_at     TEXT,                        -- NULL = active
  end_reason   TEXT                         -- pause|complete|takeover|block（cancel 不在此枚举：见 §2.3/§6.5）
);
CREATE UNIQUE INDEX uq_owner_gen_active
  ON owner_generations(work_item_id) WHERE ended_at IS NULL;   -- INV-2b：活跃行唯一
CREATE UNIQUE INDEX uq_owner_gen_seq
  ON owner_generations(work_item_id, generation);

CREATE TABLE claim_idempotency (
  idempotency_key TEXT PRIMARY KEY,
  work_item_id    TEXT NOT NULL REFERENCES work_items(id),
  caller_identity TEXT NOT NULL,             -- 发起 claim 的 api_key 所属 user_id（或 machine_id）；INV-2d 命中前必须核对
  response_json   TEXT NOT NULL,             -- 缓存的 claim 应答：公开的 SessionClaim（含 session_fingerprint），不含任何 secret
  created_at      TEXT NOT NULL              -- 永不过期、无任何清理任务（owner 2026-10-13 补登裁定，polyforge#150：
                                             -- 以代码为准——全库不存在 DELETE 本表的路径；缓存应答里只有公开
                                             -- fingerprint、无任何密钥，永久保留无安全问题，不为此新增定时清理任务）
);
```

当前 generation = 该 WI 最新一行的 `generation`；活跃 = `ended_at IS NULL`。

### 4.4 `work_items` 的 workflow 引用列与预算基线（WI 记录瘦身为引用） <!-- §4.4 -->

> **PARTIALLY ARCHIVED（2026-10-16）— `bundle_name` 是 WI 对 workflow 的唯一引用、NULL = 纯登记票（不可认领，`CORE-409 NOT_CLAIMABLE`）、首次认领冻结——这些是现行机制。**已删除、do not build from this 的部分**：digest、预算基线与 `WF-*` 码的残余描述。

**WI 记录对 workflow 只保留引用**（skill 层定稿裁决；polyforge#149 B9 收窄）：`bundle_name` 一列（DDL 见 §4.2）**是 WI 引用的唯一数据模型**——不存在任何第二张表或第二组列承载 WI 对 workflow 的固定信息，resolved steps、frontmatter 解析结果、任何 digest 一律不落库。digest 列与预算基线列已删。Core 不存储 resolved steps 大 JSON，也不保留任何 bundle 目录/注册/文件内容职责——原 `workflow_bundles`/`workflow_bundle_versions`/`workflow_bundle_files`/`workflow_pins` 四表整体删除，对应端点（bundle 注册表、bundle 文件取回、workflow-pin）一并删除（§5.1）。

**NULL = 纯登记票**：`bundle_name` 可空，NULL/非 NULL 由写路径强制（§4.5 的共用落库路径；存量异常行按字面数据展示、不拒读）。`bundle_name IS NULL` 的 WI 是**纯登记票**（registration-only）：仅承载 goal/content/labels/attrs/parent_id 等登记信息——**不可 claim**（`CORE-409`（`reason=NOT_CLAIMABLE`），§6.1；无 owner_generations 行、无 Capsule 现场），**不可 submit**（块上传与 manifest 提交端点一律 `CORE-409`，无 Revision 可发布）；状态机上只能停在 `queued` 或被 `cancel`（§6.5）。非 NULL 时即为**执行票**，受 §4.5 创建时校验与固化约束。NULL 与非 NULL 之间在 R2 窗口（queued ∧ 从未认领）内可经 `PATCH bundle_name` 双向变更（登记票可在窗口内补挂 workflow，执行票可解绑降回登记票；§4.5），首次认领后不存在任何 UPDATE 路径（INV-2e）；要执行某登记票承载的内容，可在 R2 窗口内直接补挂 bundle（§4.5），或将内容拆为子票（epic-lite，§4.2a；子票自身可为执行票），父票保持登记票不变。epic 父票（§4.2a）必为登记票（对称规则，§4.2a）。

**AIHub 完全退出 step skill / subagent 文本管理**：不存储、不解析、不校验内容、不分发任何 step skill 或 subagent 定义正文——这些是 harness 层事务；创建事务对 `agents/*.md` 只做存在性检查（`01-workflow-skills.md#§4.1` V7），不做任何摘要（digest 链已删，polyforge#149 B9）。执行侧（WI Agent/Daemon 所在机器）以 `bundle_name` 为键在本地 bundle 目录取得内容，不做 digest 校验（校验链同删；本地 bundle 缺失/漂移即 `WF-203` 的配置事故场景完全在 harness 侧处理，Core 无任何文件取回端点）。 〔#111分类：删→驱动〕

**首次认领冻结（对象是 WI 记录的 bundle 引用；B07 裁定，w140 修订）**：`bundle_name` 随创建事务（§4.5）写入，在 R2 窗口（queued ∧ 从未认领）内可经 `PATCH /api/v1/work-items/{id}` 的 `bundle_name` 键三向变更（NULL→X / X→Y / X→NULL 解绑；携带 digest 键的 PATCH 整体拒绝 `CORE-400`——该字段已删，polyforge#149 B9）；首次认领后冻结（INV-2e），窗口外携带即拒：已认领/非 queued → `CORE-400`（`reason=IMMUTABLE`），终态 → `CORE-410`（`reason=TERMINAL_STATE`）。冻结的对象是 **WI 记录的 bundle 引用（`bundle_name`）在首次认领之后的状态**（`00-architecture.md#§2.3` INV-8 的 Core 侧）——Core 只承诺该引用认领后不再变更，**不存在 Core 侧的「版本锁定」承诺**：执行侧从本机 checkout 按 `bundle_name` 物化，不做 digest 校验（校验链已删），本机 checkout 漂移属配置事故（告警、继续执行，v1 信任内网；`01-workflow-skills.md#§5.2` WF-203），Core 无任何阻断或版本回取路径。变更已认领 WI 的 workflow 引用的唯一路径是取消当前 WI 并新建（不存在「认领后整体替换」窗口，§4.5）。

**漂移告警**：Core 不存 bundle 内容、不再计算 digest（polyforge#149 B9）；同一 `bundle_name` 的 checkout 漂移只能靠执行侧物化时的告警（缺 bundle/缺文件报警继续，`WF-203`），Core 不改写任何已落库值（处置归人）。

**预算基线（列已删）**：`work_items` 不再携带任何预算基线列（polyforge#149 dead-code：该列从未被写路径填充）。预算的运行期口径由执行侧账本承担——`runtime-state/execution.json` 的 `budget_consumed`（driver 单写者）记录累计消耗，step 级与 workflow 级上界判定都在执行侧（`01-workflow-skills.md#§6.6`）；Core 不持有任何预算数据。

### 4.5 创建 WI 时的 workflow 校验与 digest 固化（内部步骤；原 workflow-pin 端点删除） <!-- §4.5 -->

原 `workflow_pins` 表与 `POST /api/v1/work-items/{id}/workflow-pin` 端点删除：resolved steps 大 JSON 存储删除后该表无剩余职责。校验与固化改为 `POST /api/v1/work-items` 创建事务内的**服务端内部步骤**——无独立端点、无独立中间落库状态，「先于任何 claim 完成」的时序约束（`01-workflow-skills.md#§4.6`）由此天然满足（创建必先于 claim）。

**适用范围**：仅 `bundle_name` 非 NULL 的执行票。`bundle_name` 为 NULL 或缺省时创建纯登记票（§4.4），跳过本节全部步骤（无 workflow 读取/校验、无 digest 固化，三 workflow 列落 NULL；epic 父票亦然，§4.2a）。

1. **读取对象**：创建 body 携带非空 `bundle_name`；AIHub 服务端读本机 aihub checkout 内该 bundle 的 workflow.md **frontmatter**。checkout 路径与 bundle 定位规则是部署配置项（随部署给定），不落库、不经 API。对 `agents/*.md` 只做存在性检查与按字节摘要（digest 计算输入，`01-workflow-skills.md#§4.1` V7），不解析、不校验其内容、不落库（§4.4）。
2. **严格校验（仅作用于 frontmatter，结果不落库）**：按 `01-workflow-skills.md#§4.1` 的 V 规则全集校验 workflow.md 的 frontmatter（唯一结构 schema）——含各字段类型与取值、`on_fail` rewind 目标存在且更早（V5）、budget 数值类型与 >0（V6）等。**Markdown 正文不解析、不校验、不落库**——正文是 WI Agent 第 0 步加载的流程级说明（`03-execution-capsule.md#§4.5`），不在 AIHub 校验边界内。任一规则失败 → 创建整体失败 `CORE-400`（`detail` 携带命中的 `rule_id`）。 〔#111分类：删→驱动〕
3. **引用落库**：校验通过后把 `bundle_name` 写入 `work_items`（§4.2/§4.4），与 repo inventory 冻结（§4.6）在同一创建事务内提交。frontmatter 解析结果等一切校验中间产物**不落库**——落库的只有 `bundle_name`（引用即名字本身；digest 列已删，polyforge#149 B9）。
4. **无（认领后的）重新固定路径**：`bundle_name` 首次认领冻结（INV-2e）；R2 窗口内 `PATCH bundle_name` 是唯一的重新固定路径（见下段），认领后不存在「整体替换引用」的窗口，确需变更 workflow 只能取消当前 WI（§6.5）并新建 WI 重新走创建流程。

**R2 窗口与 PATCH 落库（w140 裁决，首次认领冻结）**：`bundle_name` 的固化时点为**首次认领冻结**——R2 窗口（`status='queued'` 且从未认领：无活跃 session 行且无 `session_claimed` 事件）内，`PATCH /api/v1/work-items/{id}` 携带 `bundle_name` 被接受：NULL→X 补挂、X→Y 换名、X→NULL 解绑（票回到 ready queue 待选 bundle 分段）；写路径与创建共用同一落库规则（引用即名字；携带 digest 键的 PATCH 整体拒绝 `CORE-400`；`repo_inventory` 不触碰），窗口判定不看 `waiting_on`。窗口外拒绝且不写库：已认领/非 queued → `CORE-400 reason=IMMUTABLE`，终态 → `CORE-410 reason=TERMINAL_STATE`。对称规则（§4.2a）：有子票的票拒绝非 NULL 绑定（`CORE-400 reason=HAS_CHILDREN_NO_BUNDLE`）。并发：PATCH bundle vs Claim 不另设仲裁，单写者事务串行化加各自 CAS 保证终态一致——绑定或改名的 PATCH 不会让 Claim 失败，PATCH 排在其前则 Claim 冻结新 bundle，排在其后则被拒（`CORE-400 reason=IMMUTABLE`，或复查无具体原因时 `CORE-409 reason=RACE_LOST`），均不写库；绑定 bundle vs 新增子票以创建事务内父票 bundle 复查互斥，终态不同时出现「有子票 ∧ bundle 非空」。接口契约（字段、reason 清单、响应形状、部署与回滚）权威：`wi-kind-contract.md`。

### 4.5a `workflow_budget_increments`（预算增量，独立可追加记录） <!-- §4.5a -->

> **ARCHIVED — 本节描述或依赖已删除的机制（Revision/CAS/manifest）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

```sql
CREATE TABLE workflow_budget_increments (
  work_item_id TEXT NOT NULL REFERENCES work_items(id),
  dimension    TEXT NOT NULL CHECK (dimension IN ('requests','tokens','wall_minutes')),
  seq          INTEGER NOT NULL,            -- 在同一 work_item_id 内跨 dimension 全局单调递增
                                             -- （dimension 只是属性列，不参与序列作用域）
  amount       INTEGER NOT NULL CHECK (amount > 0),   -- 增量值，累加到该维度原有上界之上
  granted_by   TEXT NOT NULL REFERENCES users(id),
  granted_at   TEXT NOT NULL,
  note         TEXT,                        -- 人工旁注，可空
  PRIMARY KEY (work_item_id, seq)          -- 主键不含 dimension：seq 的唯一作用域是整个 WI，
                                               -- 与 §6.7 的按 WI 读取最大 seq 并 max+1 插入的
                                               -- CAS 逻辑逐字一致
);
```

**序列作用域裁定（与 §6.7 CAS 逐字一致）**：`seq` 的唯一作用域是单个 `work_item_id`——同一 WI 的 `requests`/`tokens`/`wall_minutes` 三个维度**共享同一条全局序列**（首行 seq=1，此后每次人工增量跨 dimension 取 max+1），`dimension` 只是行的属性列。任一 increment 恰好被计入一次：生效额度按 dimension 分组求和（见下），序列全局化不影响额度计算的确定性，只让并发 CAS 有单一比较对象。

预算基线列已删（polyforge#149 dead-code）：Core 不再持有任何预算数据；WI 的预算消耗与上界判定全部由执行侧账本（`runtime-state/execution.json` 的 `budget_consumed`，driver 单写者）承担（`01-workflow-skills.md#§6.6`）。 〔#111分类：删→驱动〕

### 4.6 `work_items.repo_inventory`（repo inventory 的 Core 权威存储） <!-- §4.6 -->

**权威归属**：WI 的 repo inventory（这张票要动的仓库）以 `work_items.repo_inventory` 一列为唯一权威落点（polyforge#157：原 `wi_repo_inventory` 表并入此列，`deploy/sql/v2.5.0`）；`03-execution-capsule.md#§4` 只描述其语义与消费方式，不重复存储。

```sql
-- work_items 上的一列（§4.2 DDL）：
repo_inventory TEXT   -- JSON 数组 [{"name": "<项目仓库名>"}]，只存名字；NULL = 无清单
```

- **建票冻结**：在 WI 创建事务内校验并写入，此后不可变（无 UPDATE 路径；`PATCH` 携带 `repo_inventory` → `CORE-400 reason=IMMUTABLE`；改清单 = 取消并新建 WI）。
- **只存名字（安全边界）**：认领方 Daemon 用本机 git 凭证 clone 清单里的仓库，所以 clone 源只能来自项目记录（`projects.repos`，由项目 maintainer/admin 维护），绝不来自建票人。每项只接受 `{name}`；url 在**认领时**从项目记录解析。
- **校验（Go 侧，创建事务内，失败则整体不落库）**：1..8 项；每项必须有字符串 `name`；`name` 必须是该项目的仓库（否则 `CORE-400 REPO_NOT_IN_PROJECT`）且在同一 WI 内唯一（重复 → `CORE-400 INVENTORY_DUPLICATE_NAME`）；携带 `url` → `CORE-400 INVENTORY_URL_NOT_ALLOWED`（即使与项目记录相同）；携带 `subdir` → `CORE-400 INVENTORY_SUBDIR_UNSUPPORTED`；其他键 → `CORE-400`（未知键）。
- **不支持冻结 base commit**：工作机不会检出指定 base，故任一项携带 `base`，或请求体的 `repo_inventory_base` 数组非空 → `CORE-400 INVENTORY_BASE_UNSUPPORTED`（拒绝而非存了不生效；`null`/`[]` 视为无）。工作机 clone 各仓库的默认分支；同一 WI 重新认领时由 carrier ref 恢复上次现场（`03-execution-capsule.md#§4`）。
- **认领消费**：`POST /api/v1/work-items/{id}/claim` 的答复 `repos` 在 WI 有清单时**只**返回清单内仓库，url 取认领时项目记录中的值（`{name, url, description?}`）；无清单时返回项目全部仓库（向后兼容）；重新认领（blocked→resume）同理。清单中的名字已不再是项目仓库 → 认领被拒 `CORE-409 INVENTORY_REPO_GONE`（认领事务回滚，WI 保持 `queued`；在项目上恢复该仓库，或取消并重新建票）。这样的 WI 同时**离开就绪队列**（`GET .../ready-queue` 的 `items` 不含它，与有未完成 `blocks` 边的 WI 同理，不新增状态或 `waiting_on` 值，就绪投影也不单列原因），因此 Daemon 的自动拉取不会反复认领它；仓库恢复后自动回到 `items`。Daemon 按 `repos` 逐个 clone，无额外逻辑。

**API**（权威签名；`01`/`03`/`05` 只引用，不重复定义）：

- `POST /api/v1/work-items` body 可带 `repo_inventory: [{name}]`（规则见上）；缺省 = 无清单。`pf file create --repo <name>`（可重复）发送这些项。
- `GET /api/v1/work-items/{id}` 响应体在 WI 有清单时含 `repo_inventory`（冻结的名字数组）；无清单时省略该键。

### 4.7 `wi_events` <!-- §4.7 -->

```sql
CREATE TABLE wi_events (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  work_item_id TEXT NOT NULL REFERENCES work_items(id),
  created_at   TEXT NOT NULL,
  actor        TEXT REFERENCES users(id),   -- NULL = 系统事件
  event_type   TEXT NOT NULL,               -- 枚举见 §5.1a 附录
  payload      TEXT NOT NULL DEFAULT '{}'   -- JSON object，受 INV-2c 词表校验
);
CREATE INDEX idx_events_wi ON wi_events(work_item_id, id);
```

### 4.8 `capsule_revisions` / `capsule_heads` / `capsule_tombstones`（Revision 登记与 HEAD 指针） <!-- §4.8 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（owner generation、Revision/CAS/manifest）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

数据模型与发布协议权威在 `05-sync-revision-recovery.md#§4`、`#§5`；本文只定义 Core 侧登记表（HEAD 是指针状态，不是执行态——两表列均为 Revision 元数据，无 step/attempt 语义）。

```sql
CREATE TABLE capsule_revisions (
  work_item_id TEXT NOT NULL REFERENCES work_items(id),
  seq          INTEGER NOT NULL,            -- Revision 单调序号
  parent       INTEGER,                     -- NULL 仅当 seq=0（bootstrap 哨兵）；否则 = seq-1
  generation   INTEGER NOT NULL,            -- 发布者 owner generation（fencing 输入）
  manifest_sha TEXT NOT NULL,               -- manifest 对象的规范化 JSON sha256（不存入 manifest.json 本身）
  data_bytes   INTEGER NOT NULL,
  published_at TEXT NOT NULL,
  PRIMARY KEY (work_item_id, seq),
  CHECK ((seq = 0 AND parent IS NULL) OR (seq > 0 AND parent = seq - 1))
);
CREATE TABLE capsule_heads (
  work_item_id TEXT PRIMARY KEY REFERENCES work_items(id),
  generation   INTEGER NOT NULL,            -- 当前 HEAD 内容对应的发布 generation（不要求等于调用方当前活跃 generation，见 §6.4）
  seq          INTEGER NOT NULL,            -- HEAD 指向的 Revision 序号
  updated_at   TEXT NOT NULL,
  FOREIGN KEY (work_item_id, seq) REFERENCES capsule_revisions(work_item_id, seq)
);
-- 注：本表在首发前无行；首发 seq=0 成功后才插入唯一行，之后每次发布 UPDATE 同一行。
CREATE TABLE capsule_tombstones (            -- 删除保护持久化状态（流程/错误码权威在 `05-sync-revision-recovery.md#§2.10`/§5.1）
  work_item_id TEXT PRIMARY KEY REFERENCES work_items(id),
  tombstoned_at TEXT NOT NULL,
  hard_delete_at TEXT NOT NULL,             -- tombstoned_at + 24h
  status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','reverted','hard_deleted')),
  confirm_wi_id TEXT NOT NULL               -- 审计：当时传入的 confirm 值
);
```

### 4.9 `memories` / `memory_fts`（元数据与派生索引；权威格式在 `06-memory-search.md`） <!-- §4.9 -->

```sql
CREATE TABLE memories (
  id           TEXT PRIMARY KEY,            -- "mem_" + 12 位，服务端分配
  project_id   TEXT NOT NULL REFERENCES projects(id),
  owner_user_id TEXT NOT NULL REFERENCES users(id),      -- create_memory 调用方身份，private 可见性判据
  work_item_id TEXT REFERENCES work_items(id),
  type         TEXT NOT NULL,
  title        TEXT,
  visibility   TEXT NOT NULL,
  authority_path TEXT NOT NULL,             -- memory.md 的数据目录内相对路径
  content_sha  TEXT NOT NULL,               -- 权威 Markdown 的 sha256
  content_len  INTEGER NOT NULL,
  source_markdown_sha TEXT,                 -- 派生时所基于的权威 Markdown 内容的 sha256（与 `content_sha` 同根源但独立写入时机：本列只在每次成功派生
                                             -- HTML 时回写，用于判定权威文件是否在上次派生后又变更。过期检测第一个条件，权威定义见 `06-memory-search.md#§4.1`）
  html_sha     TEXT,                        -- 派生 HTML 自身内容的 sha256（与 `source_markdown_sha` 是两个独立字段，任一不匹配均触发
                                             -- 重新派生：本列检测 HTML 文件自身被篡改，above 检测权威源文件变更。过期检测第二个条件，权威定义见 `06-memory-search.md#§4.1`）
  tags         TEXT NOT NULL DEFAULT '[]',
  related      TEXT NOT NULL DEFAULT '[]',
  idempotency_key TEXT UNIQUE,               -- 客户端显式幂等键（wrap-summary-<wi>-<gen> 等）；NULL=普通创建；
                                             -- UNIQUE 约束承担去重：重试命中约束返回原条目（`06-memory-search.md#§2.1` wrap 摘要契约）
  status       TEXT NOT NULL DEFAULT 'active',
  created_at   TEXT NOT NULL,
  updated_at   TEXT NOT NULL
);
CREATE VIRTUAL TABLE memory_fts USING fts5(
  mem_id UNINDEXED, body,                   -- body 为从权威 Markdown 抽取的文本（派生）
  tokenize='trigram'                        -- 固定 trigram，与 `06-memory-search.md#§4.4` 一致；禁止 unicode61 等其他
                                             -- tokenizer 并存（滑窗切分保证任意子串检索对中文等无空格语言同样成立）
);
```

任何写路径（create 或 update）均在同一事务内先 `DELETE FROM memory_fts WHERE mem_id=?` 再 `INSERT`，保证任一时刻每个 `mem_id` 至多一行可检索文本，与最新权威内容一致（完整流程权威见 `06-memory-search.md#§5.2`）。`memory_fts` 是派生物，可从权威 Markdown 全量重建，Core 不承担内容权威；重建（`06-memory-search.md#§5.3`）与上述增量写路径同以 trigram 为唯一口径，tokenizer 不随重建变化。

### 4.10 身份、机器与 service 自注册 <!-- §4.10 -->

> **PARTIALLY ARCHIVED（2026-10-16）— projects / users / membership / api key 等身份与权限表是现行机制（DDL 以 `internal/aihub/core/store.go` 为准）。**已删除、do not build from this 的部分**：涉及 owner generation、Revision 登记与 service 密钥 epoch 的内容。

```sql
CREATE TABLE projects (
  id        TEXT PRIMARY KEY,
  name      TEXT NOT NULL UNIQUE,
  visible   INTEGER NOT NULL DEFAULT 1,    -- 布尔：project 是否对无 membership 者出现在目录列举面（仅目录可见，
                                             -- 不授予任何对象数据访问，判据见下方权限统一规则；一期缺省 1）
  scenario  TEXT                            -- 可选：scenario 仓引用（沿用现有语义），可空；不参与任何锁/调度语义
);
CREATE TABLE project_members (
  project_id TEXT NOT NULL REFERENCES projects(id),
  user_id    TEXT NOT NULL REFERENCES users(id),
  role       TEXT NOT NULL CHECK (role IN ('viewer','writer','maintainer')),
  created_at TEXT NOT NULL,
  PRIMARY KEY (project_id, user_id)
);
CREATE TABLE users (
  id           TEXT PRIMARY KEY,           -- "usr_" / "mac_"（machine）
  display_name TEXT NOT NULL,
  kind         TEXT NOT NULL CHECK (kind IN ('human','machine')),
  role         TEXT NOT NULL DEFAULT 'writer' CHECK (role IN ('admin','writer'))
);
CREATE TABLE api_keys (
  id         TEXT PRIMARY KEY,
  user_id    TEXT NOT NULL REFERENCES users(id),
  project_id TEXT REFERENCES projects(id), -- 可选 project scope（沿用现有语义）：NULL = 该 user 可见面内
                                             -- 全量 project；非 NULL = 仅该 project（middleware 强制，§8）
  key_hash   TEXT NOT NULL,                  -- 仅存哈希
  revoked_at  TEXT
);
CREATE TABLE machines (
  id          TEXT PRIMARY KEY,             -- machine_id（upsert 键）
  user_id     TEXT NOT NULL REFERENCES users(id),   -- 绑定的 machine user（users.kind='machine'）
  runtime_version TEXT,
  last_heartbeat_at TEXT NOT NULL           -- 注册即首次心跳：建行时刻即首次心跳时刻，无独立 registered_at 列
);
-- （键值元数据表已删除，polyforge#149 dead-code：无任何代码路径读写它。）
CREATE TABLE service_secret_epochs (
  epoch        INTEGER PRIMARY KEY,          -- 单调递增，自 1 起
  mode         TEXT NOT NULL CHECK (mode IN ('grace','rollback')),
                                             -- 作废模式，随 epoch 行持久化（§6.6 判别依据，可跨重启恢复）
  created_at   TEXT NOT NULL,
  retired_at   TEXT,                         -- NULL = 仍在宽限期内可用于校验；非 NULL 后一律拒绝
  rotated_by   TEXT REFERENCES users(id)      -- NULL = 系统自动轮换（如启动时发现文件丢失）
);
```

实际的 secret 字节值不落 SQLite（避免数据库文件泄露即等价泄露凭证），只落 `<data_dir>/service.secret.<epoch>`（权限 0600）；`service_secret_epochs` 表只记录版本号与生命周期时间戳，供 §6.6 的宽限期判定使用。

**机器注册即首次心跳（upsert，`POST /api/v1/machines/heartbeat`）**：心跳端点以 `machine_id` 为 upsert 键。Core 无该 `machine_id` 行时，本次调用即完成注册：同一事务内创建一个 machine user（`users` 行，`kind='machine'`，display_name 取心跳 body），并签发其 api_key——**machine key 的 scope 语义与人类 key 相同**：注册时签发全局 key（`project_id=NULL`；要能 claim 某 project 的 WI，该 machine user 仍须满足下述 claim 授权判据——全局 key 本身不构成认领授权，D4 裁定）；需要限定时，由 admin 经管理面 M2 换发/追加带 project scope 的 key（scope 是收窄不是扩张），或经 M3 将该 machine user 加入某 project 的 `project_members`——CI 机器限定跑某些 project 的授权由此取得，不需要新的注册参数或端点。**两面授权（B1 澄清；D4 修订 polyforge#150：以代码为准）**：机器的 **claim 执行面**授权 = 对**该 project 的 writer/maintainer membership 或全局 admin**（`server.go` `hSessionClaim` → `requireWrite`/`canWrite`，403 otherwise）——key scope 本身**不构成**认领授权（scope 只是 §8 middleware 层的收窄过滤）；全局 machine key 若无任何 membership（且非 admin）也不能 claim。**对象数据面**（WI 详情/进度、memory、queue 查询）仍按本节 membership OR admin 规则——全局 machine key 不授予任何 project 的数据访问资格、写入 `machines` 行绑定该 `user_id`，并为该 machine user 签发一把 api_key（明文仅在响应中返回一次，此后机器以它认证一切调用；首次注册调用由部署操作员以任一合法 human/admin api_key 发起）。已有行时仅更新 `last_heartbeat_at`/`runtime_version`，幂等（重复心跳不重建 user、不重复签发 key）。机器的一切 Core 调用（claim、Revision 发布等）以该 machine user 的 api_key 认证，`owner_generations.machine_id` 指向本表行。

**project 权限统一规则（权威；B05 裁定，A17 硬边界的判据全集）**：访问分两层，判据各只有一条：

- **对象数据访问 = project membership OR 全局 admin**。一个 user 对某 project 内对象数据——WI（列表、详情、事件流、progress、Revision 数据与块端点）、Memory、就绪队列条目——的访问资格，当且仅当满足下列二者之一：在 `project_members` 中有 membership 行，或 `users.role='admin'`。`admin` 是**唯一**的角色级例外（全局豁免，不写入 `project_members`、不占行、不是第六种角色）；与之**并列**的另一例外是 **owner 显式分享**（Memory 分享链接，A18 裁定，`06-memory-search.md#§5.1`——owner 对单条对象的显式授予，非角色、非默认开启）。两大例外之外不存在第三条访问路径：无 membership、非 admin、且无显式分享者，对该 project 的一切对象**全不可见**（读一律 `CORE-404`，不泄露存在性；写一律 `CORE-403`）。
- **project 目录可列举 = membership OR `projects.visible=1`**。`visible` 只决定该 project 的名称/标识是否出现在无 membership 调用方的**目录列举面**（project 列表响应），**仅目录可见、不含任何数据**：无 membership 者即使列举到该 project，对其对象数据的访问仍完全按上一条判据执行（404/403），`visible` 不构成任何数据访问授权。
- **Memory 面的执行顺序**：`06-memory-search.md#§5.1` 的 membership-first 过滤（先按 project membership 过滤、再在候选集内应用可见性分级）是本规则在 Memory 检索/列表面的执行顺序——机械判据以本表为唯一权威，06 不另立判据。
- **验收引用**：`07-deployment-pilot.md#§10.1a` 的 membership 硬边界与 admin 全局豁免验收条目以本规则为判据。
- `api_keys.project_id` 非 NULL 时可见面进一步收窄到该 scope（§8 middleware 强制），但**不扩大**授权：scoped key 的 user 仍须本就满足上述对象访问判据（membership 或 admin），scope 不能凭空授予 membership。

### 4.11 AIHub 本地数据目录布局（权威；`07-deployment-pilot.md#§4` 为部署视角摘要，完整树以本文为准） <!-- §4.11 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（Revision/CAS/manifest）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

```
<data_dir>/                              # 缺省 /var/lib/polyforge/aihub（可配置）
├── aihub.db                             # Core（§4 全部表）
├── aihub.db-wal / aihub.db-shm          # WAL 伴生文件
├── service.lock                         # §2.2 文件锁
├── service.secret.<epoch>               # 当前及宽限期内的历史 service_secret（§6.6）
├── memory/<project_id>/<mem_id>/
│   ├── memory.md                        # 权威内容
│   └── memory.html                      # 派生
├── memory/<project_id>/_redacted/<mem_id>/…   # redact 归位
├── capsules/<work_item_id>/
│   ├── rev/<seq>/manifest.json          # Revision manifest（不存数据，只存操作清单+引用）
│   ├── rev/_cas/<sha256[:2]>/<sha256>   # 内容寻址存储（CAS，唯一权威存储布局，同块跨 seq 去重）
│   └── archive/                         # 最终结果归档（wrap 后整理）
├── backups/                             # §11 备份
└── tmp/                                 # 原子写中转目录（先写 tmp 再 rename）
```

## 5. API 与错误码 <!-- §5 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（flush 证据、read-through、本地认证代理）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

本节是 AIHub 对外 HTTP API 的唯一权威面（端点 canonical 清单见 §5.1）。

**单端口与无长连接（A5 裁定，保留）**：AIHub 只监听一个 HTTP 端口，§5.1 全部端点（业务面 26 端点与管理面 M1–M5）共用该端口与同一鉴权面，不按功能分设第二监听面；AIHub 与任何调用方之间不存在长连接（无 WebSocket/SSE/流式保活），一切交互均为短 HTTP 请求/响应。**跨机无 Daemon 直连**：Daemon 是本机执行面（`00-architecture.md#§3.1`，只经本机 Unix domain socket 服务同机 Harness，`04-local-runtime.md#§5.1`），跨机器的客户端（`pf` CLI、CI、人）一律直连 AIHub 本节端点，不存在「跨机直连他人 Daemon」的任何路径。

**路由 verbs 分流表（13 个用户动词 → 目标面；分流判据权威在 `00-architecture.md#§5.1`，本表为其在 Core API 面的投影）**：

| 路由 verb | 目标面 | 承载 |
|---|---|---|
| `file`（创建/编辑 WI）、`queue`（列表/就绪队列查询）、`cancel`（取消非活跃 WI）、`recall`（Memory 检索）——共 4 个 | AIHub（直连） | `pf` CLI → §5.1 端点行（`file`：行 1–3；`queue`：行 2–4；`cancel`：行 5；`recall`：行 23） |
| `submit`、`observe`、`answer`、`batch-confirm`、`pause`、`resume`、`wrap`、`ship` (removed by polyforge#166: delivery is the workflows' deliver step)（基线修订 polyforge#37 所加：wrap→git 交付桥，纯本机 git 面，**不落任何 Core 端点**，见 `04-local-runtime.md#§5.1`）、`ops`——其余 9 个 | Daemon 本机面 | `04-local-runtime.md#§5.1`（`observe` 的 read-through 经 Daemon 本地认证代理落到行 6；`pause`/`wrap` 的收尾由 Daemon 生成 `flush_evidence` 后调行 9；`resume`/`submit` 的 claim 落到行 7） |

分流不改变语义权威，也不放宽 Core 侧校验：直连 verbs 与其他 HTTP 客户端过同一套鉴权（§8）；经 Daemon 的 verbs 由 Daemon 机械转发与本地认证代理（`04-local-runtime.md#§5.3`），Core 端点仍是同一张 §5.1 清单。

### 5.1 API 全集（26 端点）——canonical inventory <!-- §5.1 -->

> **PARTIALLY ARCHIVED（2026-10-16）— 本清单已过期**：11 行无路由（progress、force-takeover、unblock、budget、repo-inventory、SYNC 各行、PATCH /memories、share、rotate-secret），6 条真路由未列（session-verify/attach、force-end-session、resume-blocked、GET /machines、`/git/` 代理）。仍描述现行端点的行（例如行 3 的可 PATCH 字段）可作参考；路由权威是 `internal/aihub/server/server.go` 的注册表。do not build from the removed rows.

**计数口径（权威，A6 修复；B06 裁定后计数为 25；基线修订 polyforge#3 补 G 组行 26 后为 26——理由：§4.2a 定义了 wi_dependencies 完整写路径，原清单漏列其路由）**：本表是 AIHub HTTP API 的唯一 canonical 端点清单（`00-architecture.md#§5` 所引「26 个端点分组」即本表），任何文档不得在别处另行枚举 AIHub 端点。计数单位 = **端点行**：A–D 组每行是一个 method+path 端点（唯一例外：行 3 把 `/work-items/{id}` 同路径的 GET（详情）与 PATCH（更新）合并计 1 行——两方法共享同一路径、同一鉴权与 §4.10 权限面）；E 组每行是一个**操作组**（组内可含多条 method+path，计 1 行）；F 组每行是一个资源的 CRUD 面（同资源路径族计 1 行）。业务面 26 行逐字可复算：

**26 = WI 业务 6（行 1–6）+ 执行权 7（行 7–13）+ 同步 7（行 14–20）+ 机器 1（行 21）+ Memory 4 组（行 22–25；行 25 `memories/rebuild-index` 已由 polyforge#149 删除，表中不再列出，索引重建只在 aihub 启动时 reconcile）+ 依赖边 1 组（行 26，基线修订 polyforge#3）**

F 组管理面（users/api-keys/projects CRUD + admin/backup + service/rotate-secret）单列 M1–M5 共 5 行：同端口、同鉴权，属 `pf` 管理 CLI 的 CRUD 面，不在 26 端点计数口径内（该口径只覆盖业务/执行/同步/机器/Memory/依赖边面）。清单外的旧端点行全部删除：workflow-pin（§4.5 已改为创建事务内部步骤，无独立端点）、bundle 注册表与文件取回端点（§4.4 裁定删除）、`pause`/`block`/`wrap`/`fail` 四个分立端点（合并为行 9 的 `session-end` 四合一）、`resume`（并入行 7：§2.3 裁定 resume 即新一次 claim）、事件流分立读写端点（读取并入行 3 的 `include=events`，追加由 Core 状态机事务内部完成，§5.1c）、`reauthorize` 独立端点（§6.6 改为 claim 幂等重放流程，无独立端点）与 service 自管理操作（`service/heartbeat` 已由行 21 承载、`whoami` 由 `pf` 读本地配置不设端点、`rotate-secret` 归管理面 M5 行——均不占业务 26 端点行）。

| # | 方法 | 路径 | 调用方 | 语义 | 错误码 |
|---|---|---|---|---|---|
| **A. WI 业务（6 行：行 1–6）** | | | | | |
| 1 | POST | `/api/v1/work-items` | `pf` CLI（路由 skill/CI/人） | 创建 WI（`queued`）；body 可带 `bundle_name`（非空 = 执行票，走 §4.5 校验/固化；NULL/缺省 = 纯登记票，跳过，§4.4）、可选 `parent_id`（epic-lite，写路径约束 §4.2a）、`repo_inventory`（§4.6）与 `attrs`（业务级自由属性，受 INV-2c 黑名单校验，§5.1a）；服务端在同一事务内完成 workflow.md frontmatter 校验与三 scalar 固化（§4.5） | CORE-400（含 frontmatter 校验失败，`detail` 携带命中 `rule_id`）/ CORE-409 |
| 2 | GET | `/api/v1/work-items?project=&status=&assignee=&waiting_on=&parent=&milestone=` | `pf` CLI | 列表/过滤；`parent=` 经 `idx_wi_parent`、`milestone=` 经 `idx_wi_milestone`（§4.2）；结果先经 §4.10 权限硬边界裁剪 | CORE-400 |
| 3 | GET/PATCH | `/api/v1/work-items/{id}?include=events,deps,children` | `pf` CLI（只读详情亦经 Daemon 本地认证代理，`04-local-runtime.md#§5.3`） | 详情（基本记录 + `repo_inventory`（有清单时）；`include` 参数权威定义见 §5.1c；w140 新增只读派生 `derived` 块（有子票时，`wi-kind-contract.md`））/ 更新 goal、content、labels、priority、milestone、waiting_on（`attrs` 受 INV-2c 黑名单校验；`bundle_name` 仅 R2 窗口内可 PATCH（§4.5），认领后不可变 INV-2e；digest 键不在可更新集合（携带即 `CORE-400`）；`parent_id` 不可变 §4.2a）。同路径 GET+PATCH 计 1 行（计数口径见上） | CORE-404 / CORE-409 / CORE-410 / CORE-400 |
| 4 | GET | `/api/v1/projects/{project}/ready-queue` | `pf` CLI | 就绪队列：`{items, running, paused, pending_bundles}` 四段响应（D1 裁定，polyforge#150：以代码为准），schema 权威见 §5.1b；结果先经 §4.10 裁剪 | CORE-400 / CORE-404 |
| 5 | POST | `/api/v1/work-items/{id}/cancel` | `pf` CLI（reporter / 该 project maintainer / admin，§6.5） | 终止 `queued`/`paused`/`blocked` 的 WI（不要求活跃 generation） | CORE-409（状态不属于三者之一）/ CORE-410 |
| 6 | GET | `/api/v1/work-items/{id}/progress` | 任意客户端（人/CI；`observe` 经 Daemon 本地认证代理） | read-through 进度（§5.2） | CORE-404 / CORE-504 |
| **B. 执行权（7 行：行 7–13）** | | | | | |
| 7 | POST | `/api/v1/work-items/{id}/claim` | Daemon（机器 api_key） | 认领：generation+1、开活跃行（§6.1）；`resume`（对 `paused` WI 的重新激活）不是独立端点，走本端点（§2.3/§6.2：重新激活 = 新一次 claim）；body 带 `idempotency_key`，重发同 key + 同调用方身份 + 同 WI 返回同一次结果（§6.6，INV-2d）；登记票不可 claim（`NOT_CLAIMABLE`，§4.4） | CORE-409（ALREADY_CLAIMED / NOT_CLAIMABLE / IDEMPOTENCY_KEY_REUSED / INVENTORY_REPO_GONE，§4.6）/ CORE-503 |
| 8 | POST | `/api/v1/work-items/{id}/force-takeover` | 人（`role='admin'`）经 `pf` CLI | 强制接管：generation+1（§6.2）；不需 `flush_evidence`、不需持有旧 token | CORE-403 / CORE-404 |
| 9 | POST | `/api/v1/work-items/{id}/session-end` | Daemon | 会话收尾端点（已取代并删除 v1 的 `end-generation`）：body `{session_id, status, note?, clean_pause?, head_sha?}`，`status` ∈ {`wrapped`, `failed`, `blocked`, `paused`}；`paused` 走 M3 pause 迁移（会话**保留**，`clean_pause` 随同一 fenced UPDATE 落盘），三个终态裁决经 session-hash fence 校验后落状态并清空会话（D14 裁定：`paused` 起点可直接 `blocked`/`wrapped`/`failed`，无需先 `resume`）；`head_sha` 仅随 `blocked` 携带 | CORE-400 / CORE-403 / CORE-404 / CORE-409 |
| 10 | POST | `/api/v1/work-items/{id}/unblock` | Daemon（重新认领） | 解阻：`blocked` → `running`，新一次 claim（generation+1，§6.2） | CORE-409（非 `blocked`）/ CORE-410 |
| 11 | POST | `/api/v1/work-items/{id}/budget-increments` | 人（reporter / 该 project maintainer / admin）经 `pf` CLI | 增加预算（§6.7），CAS 写入 `workflow_budget_increments`（`based_on_seq`）；按角色授权，不要求 generation token（§6.7/§8） | CORE-400 / CORE-403 / CORE-409（CAS 冲突） |
| 12 | GET | `/api/v1/work-items/{id}/budget` | Daemon 本地认证代理 / `pf` CLI | 预算读面：Core 不持有预算数据（基线列已删，polyforge#149），预算消耗与上界判定由执行侧账本（`runtime-state/execution.json` 的 `budget_consumed`）承担，本端点仅转发该口径 | CORE-404 |
| 13 | GET | `/api/v1/work-items/{id}/repo-inventory` | Daemon（远端现场恢复，`05-sync-revision-recovery.md#§7.4`） | 只读返回 repo inventory 清单（§4.6，含冻结 `base`），供不拉取整份详情即可重建检出清单 | CORE-404 |
| **C. 同步（7 行：行 14–20；签名权威在 `05-sync-revision-recovery.md#§5.1`，与本表逐字一致）——行 14–17 与行 19（发布/恢复面）唯一合法调用方是 Daemon（INV-11）；行 18 与行 20（删除保护/撤销，`05-sync-revision-recovery.md#§2.10`）由人（admin）经部署 CLI 触发** | | | | | |
| 14 | PUT | `/api/v1/work-items/{id}/revisions/{seq}/blocks/{sha256}` | Daemon | 上传单个 CAS 块（幂等内容写入；header 必带 `X-PF-Generation-Token`，块级 fencing） | SYNC-403 / CORE-507 |
| 15 | POST | `/api/v1/work-items/{id}/revisions/{seq}/manifest` | Daemon | 提交 manifest（触发 HEAD 推进；校验链：fencing → 幂等 → HEAD 引导 → `manifest_sha` 重算 → 块完整性） | SYNC-400 / SYNC-403 / SYNC-409 / CORE-507 |
| 16 | GET | `/api/v1/work-items/{id}/head` | Daemon | 读取当前 HEAD：`{seq, generation, manifest_sha, updated_at}`；无行（bootstrap 前）→ 404 | 404（无行） |
| 17 | GET | `/api/v1/work-items/{id}/revisions/{seq}/blocks/{sha256}` | Daemon | 块下载：恢复拉取单个 CAS 块（`05-sync-revision-recovery.md#§7.4`） | SYNC-404 |
| 18 | DELETE | `/api/v1/work-items/{id}/capsule-data?confirm=<wi_id>` | 人（admin）经部署 CLI 触发 | 删除保护入口（`05-sync-revision-recovery.md#§2.10`）：写 `capsule_tombstones`（持久化） | SYNC-409（非终态）/ SYNC-400（confirm 不符） |
| 19 | GET | `/api/v1/work-items/{id}/revisions?since=<seq>&include=ops` | Daemon | 恢复用 manifest 列表/读取：返回 `since` 起（含）的 Revision manifest 元数据清单，供远端现场恢复（`05-sync-revision-recovery.md#§7.4`）定位与读取需拉取的 Revision；块本体经行 17 下载。可选 `include=ops`（基线修订 polyforge#14，修正本行原「仅元数据清单」与 `05-sync-revision-recovery.md#§7.4`「逐 Revision 重放 manifest ops」之间的基线矛盾；参数风格同行 3 的 `include`，不新增端点、不改变端点计数）：缺省（无参数）时响应仅元数据，行为完全不变；携带时响应各行额外内联该 Revision 的 manifest 审计内容——`kind` 与 `ops`，读自 Core 的 `rev/<seq>/manifest.json` 审计副本，元数据字段本身不变——使 `05-sync-revision-recovery.md#§7.4` 的远端现场恢复可全程走 HTTP。`include` 合法值仅 `ops`，未知值一律 `CORE-400`（同 §5.1c 行 3 的未知值裁决风格） | SYNC-404 / CORE-400 |
| 20 | POST | `/api/v1/work-items/{id}/capsule-data/revert-tombstone` | 人（admin）经部署 CLI 触发 | 24h 撤销窗内撤销删除保护（`05-sync-revision-recovery.md#§2.10` 的入口）：将 `capsule_tombstones` 的 `pending` 行置为 `reverted`，硬删除不再执行；无 `pending` tombstone 可撤销 → `SYNC-410` | SYNC-404 / SYNC-410 |
| **D. 机器（1 行：行 21）** | | | | | |
| 21 | POST | `/api/v1/machines/heartbeat` | Daemon（machine api_key；首次注册由部署操作员以任一合法 human/admin api_key 发起） | 心跳 upsert（§4.10）：无该 `machine_id` 行则以本次调用完成注册（注册即首次心跳：同一事务建 machine user、绑定 `machines.user_id`、签发机器 api_key——明文仅响应一次），已有行仅刷新 `last_heartbeat_at`/`runtime_version`；幂等 | CORE-400 / CORE-401 |
| **E. Memory（3 组 = 3 行：行 22–24；签名、写序与错误码权威在 `06-memory-search.md#§5.1`）** | | | | | |
| 22 | POST/GET/PATCH | `/api/v1/memories`、`/api/v1/memories/{id}` | 客户端（WI Agent 经 Daemon 本地认证代理；人经 `pf` CLI） | 【CRUD 组】创建（POST，服务端分配 `mem_id`）/ 详情（GET，`format=html` 返回派生）/ 更新（PATCH，生成新版本 + FTS 先删后插）；三步写序与 `backupBarrier` 权威在 `06-memory-search.md#§5.2` | MEM-400 / MEM-404 / MEM-409 〔#111分类：删→驱动〕 |
| 23 | GET | `/api/v1/memories/search?q=&type=&tags=&limit=` | 同上 | 【检索组】实时文本检索（FTS trigram，§4.9）；两层过滤顺序（project membership 先行）权威在 `06-memory-search.md#§4.5` | MEM-400 / MEM-404 / MEM-503 |
| 24 | POST/GET | `/api/v1/memories/{id}/redact`、`/api/v1/memories/{id}/share`、`/api/v1/share/<token>` | 客户端（`share` 仅该条 Memory 的 owner）；`/api/v1/share/<token>` 为**匿名只读消费**——无需 api_key | 【条目动作组】redact 软删除 / share 生成 owner 显式分享链接（A18 裁定：显式例外，不构成第六种 visibility）/ 匿名消费：`GET /share/<token>` 仅返回 token 对应的**单条** Memory 只读视图（无列表、无检索、无任何写操作、token 由 share 生成时随机签发且不可枚举；middleware 对该路径跳过 api_key/membership 校验，除该单条外不暴露任何对象存在性信息）；同组计 1 组 | MEM-403 / MEM-404 / MEM-409 |
| **F. 管理面（M1–M5，共 5 行；同端口同鉴权，`pf` 管理 CLI 的 CRUD/服务自管理面，不在 25 端点计数口径内）** | | | | | |
| M1 | POST/GET/PATCH | `/api/v1/users`、`/api/v1/users/{id}` | 人（admin）经 `pf` 管理 CLI | users CRUD（human/machine 身份、display_name、全局 `role`，§4.10） | CORE-400 / CORE-403 / CORE-404 |
| M2 | POST/GET/DELETE | `/api/v1/api-keys`、`/api/v1/api-keys/{id}` | 人（admin）经 `pf` 管理 CLI | api-keys CRUD（签发明文仅响应一次、只存哈希；可选 project scope（§4.10）；撤销置 `revoked_at`） | CORE-400 / CORE-403 / CORE-404 |
| M3 | POST/GET/PATCH | `/api/v1/projects`、`/api/v1/projects/{id}` | 人（admin / 该 project maintainer）经 `pf` 管理 CLI | projects CRUD 与 `project_members` 成员关系管理（role ∈ viewer/writer/maintainer；§4.10 权限硬边界的唯一机械判据） | CORE-400 / CORE-403 / CORE-404 / CORE-409 |
| M4 | POST | `/api/v1/admin/backup` | 人（admin）经 `pf` 管理 CLI | 按需触发 `<data_dir>/backups` 快照（§11 每日热备之外的显式触发入口） | CORE-403 / CORE-507 |
| M5 | POST | `/api/v1/service/rotate-secret` | 人（admin）经 `pf` 管理 CLI / 启动自检自动触发 | 凭证轮换两模式（§6.6：`grace` 宽限 / `rollback` 立即失效，body `{mode, grace_seconds?}`）；`whoami`/心跳残余语义不设端点（whoami 由 `pf` 读本地配置，心跳已由行 21 承载） | CORE-403 / CORE-400 |
| **G. 依赖边（1 组 = 1 行：行 26；基线修订 polyforge#3 所补——§4.2a 定义了 wi_dependencies 的完整写路径，原清单漏列其路由；路由形状照现役 aihub precedent，语义一律按本文：同 project 硬边界、只标 waiting_on 不迁移状态。排列在 F 组之后仅为不重编号（M1–M5 标号被跨文档引用），行 26 属业务面计数）** | | | | | |
| 26 | POST/GET/DELETE | `/api/v1/work-items/{id}/dependencies`、`/api/v1/work-items/{id}/dependencies/{blocking_id}/{kind}` | `pf` CLI | 【依赖边 CRUD 组】建边（POST，body `{blocking_wi_id, kind?}`，kind 缺省 `blocks`，path `{id}` = 被阻塞方）/ 列边（GET，返回 `blocking` 与 `blocked_by` 两组清单，与行 3 `include=deps` 同构）/ 删边（DELETE，物理删除行，§4.2a）。全部写路径约束在 §4.2a 事务内强制：同 project 硬边界、防环（`CORE-409 DEPENDENCY_CYCLE`）、blocker 必须是执行票（`CORE-400 BLOCKER_NOT_EXECUTABLE`）、重复边幂等；`blocking_wi_id` 与 path 两端均接受 id 或 slug；授权：对该 project 的 writer+ 或 admin（§4.10）。`blocks` 边**不写 `waiting_on`、不迁移状态**——只有 `wrapped` 的 blocker 放行；依赖等待的 `queued` WI 不出现在就绪队列任何段（§5.1b：无可执行性即不呈现，不存在 `blocked` 数组；依赖等待的可见性经 WI 视图的派生字段 `waiting_for`、行 3 `include=deps` 与本组列边端点获取，每条边带对方 `slug`/`status`） | CORE-400（含 BLOCKER_NOT_EXECUTABLE）/ CORE-403 / CORE-404 / CORE-409（DEPENDENCY_CYCLE） |

**generation 凭证（能力令牌，不可伪造）**：`claim`（§6.2）成功时签发一个 capability token：`token = HMAC-SHA256(service_secret[epoch], wi_id + "|" + generation + "|" + machine_id)`，其中 `service_secret[epoch]` 为当前生效或宽限期内的历史 secret（§6.6）。第 3 类端点（见下）以 `X-PF-Generation-Token: <token>` 传递；`g`（generation 整数）仍可同传供日志可读性，但校验以 token 为准。Core 用当前及宽限期内的历史 `service_secret` 逐一重算并常量时间比对：

- 与当前 epoch 的 secret 算出的值匹配 → 通过。
- 与某个仍在宽限期内的历史 epoch 的 secret 算出的值匹配（而非当前 epoch）→ 通过，但响应体标注 `secret_epoch` 为该历史值，提示调用方应尽快按 §6.6 的重新授权路径换取当前 epoch 下的新 token。
- 与任何宽限期内的 secret 都不匹配（generation 已被 fencing，或 secret 已过宽限期）→ `CORE-403`，body 携带 `reason`：`stale_generation`（被接管/已结束，唯一处置是本地封存退出，§6.4）或 `secret_rotated_expired`（曾经合法但宽限期已过，处置是 §6.6 的重新授权路径）。

token 与 `(wi_id, generation, machine_id)` 三元绑定；换机器重放同一 token 无效。

**适用范围（三类端点校验层次；行号指本节 canonical 表）**：

1. **无 WI 上下文**（行 1 `POST /work-items` 创建、行 21 `POST /machines/heartbeat` 注册/心跳 upsert）：仅需 api_key（身份），无 generation 概念，不要求任何 token。
2. **WI 已存在但当前无活跃 generation**（行 7 `claim`（含对 `paused` WI 的 resume 场景）、对 `queued`/`paused`/`blocked` WI 的行 3 `PATCH`、行 10 `unblock`、行 5 `cancel`（§6.5））：仅需 api_key（身份）+ WI 状态合法性，不需要 token——`blocked`/`paused` 的 WI 本就没有可用的活跃 token，若要求 token 则这些 WI 永远无法被解阻或取消。
3. **WI 有活跃 generation 且操作属于该 generation 的执行现场**（行 14–15 Revision 发布、行 9 `session-end`、行 12 `budget` 读取）：需 token，因为这些操作都是「当前 owner 结束或延续自己的 generation」，与类别 2 的「重新激活一个已结束的 generation」在时序上根本不同。两类角色授权例外不要求 token：行 8 `force-takeover`（调用方不是当前 owner，只需 `users.role='admin'`）与行 11 `budget-increments`（人工在 WI 处于 `blocked` 时从外部做出的决定，按 reporter/maintainer/admin 角色授权，§6.7）——二者均基于角色而非执行现场凭证（§8）。

### 5.1b ready-queue 响应 schema（四段：items/running/paused/pending_bundles；A8 裁定 + D1 修订 polyforge#150） <!-- §5.1b -->

行 4 `GET /api/v1/projects/{project}/ready-queue`（`{project}` 为项目名或 id）的响应体是 **`project` 字段 + `{items, running, paused, pending_bundles}` 四段**（D1 裁定，2026-10-13，polyforge#150：以代码为准，`internal/aihub/core/queue.go` `ReadyQueue`），不存在旧七段队列语义（`needs_human_session`/`stale_running` 等分段一律不存在），也不存在 `blocked` 数组——等依赖的票**不出现**在任何段（无可执行性即从响应消失）：

```json
{
  "project": "<project-name>",
  "items": [
    {"id": "wi_xxxxxxxxxxxx", "slug": "<project>#<seq>", "goal": "…",
     "priority": "normal", "bundle_name": "…", "created_at": "…",
     "…": "其余 §4.2 基本列（labels/assignee/milestone 等，随详情同构裁剪）"}
  ],
  "running": [ {"…": "同构条目"} ],
  "paused":  [ {"…": "同构条目"} ],
  "pending_bundles": [ {"…": "同构条目"} ]
}
```

- **`items`（可执行段）**：`status='queued'` 且 `waiting_on IS NULL` 且无未 `wrapped` 的 `blocks` 依赖边（§4.2a；failed/canceled blocker 不放行）的执行票。**恒排除 `bundle_name IS NULL` 的登记票**（§4.4：不可 claim），也排除一切终态与 `running`/`paused`/`blocked` 状态的 WI；因未 `wrapped` 的 `blocks` 边而等待的 `queued` WI 同样**不出现**（无 `blocked` 数组承载等待面，依赖等待的可见性经 WI 视图派生字段 `waiting_for`（列表与详情）、行 3 `include=deps` 与行 26 列边端点获取）。
- **`running` / `paused`（可见性段）**：当前处于 `running` 与 `paused` 状态的 WI 的只读可见面（供人/工具观察），不参与认领判定。
- **排序**：各段内均按 `priority`（`urgent`→`high`→`normal`→`low`）→ `created_at`（先创建在前）。
- **v1 无分页**：全量返回，不设 cursor/limit；规模出现压力后再议（§12）。
- `running` WI（含心跳过期的 stale running）只出现在 `running` 可见性段、不设任何独立 stale 段；stale 检测在 §9 监控面暴露，接管由人决策（行 8），Core 不自动接管（§7）。
- **w140 增量（只做加法，`wi-kind-contract.md`）**：在线实现的 ready-queue 响应（`project`/`items`/`running`/`paused`）新增「待选 bundle」分段（`pending_bundles`：bundle 为空 ∧ 无子票 ∧ queued 且等待/依赖排除同上）与条目级只读派生 `derived` 块（父票完成状态，不写终态）；旧字段不改名不删减。
- `slug` 为展示名 `<project-name>#<seq>`（如 `aihub#793`，`seq` 见 §4.2，项目内唯一）；调用方对该 project 无访问资格（membership OR 全局 admin 均不满足，§4.10）时整个查询按不存在处理（`CORE-404`）；全局 admin 不受 membership 限制。

### 5.1c `GET /work-items/{id}` 的 `include` 参数（events/deps/children 拼详情；A9 裁定） <!-- §5.1c -->

行 3 的 GET 支持 `?include=`（逗号分隔多值；未知值 → `CORE-400`）：

| 值 | 拼装字段 | 语义 |
|---|---|---|
| `events` | `events` | 该 WI 的 `wi_events` 流（§4.7，按 `id` 升序；`event_type` 与 payload schema 见 §5.1a 附录）。事件**追加无对外端点**：由 Core 状态机事务内部完成（如 §6.1 第 6 步的 `claimed` 事件），对外读取只经本 include |
| `deps` | `deps` | 依赖边两组清单（§4.2a）：`blocking`（本 WI 阻塞了谁）与 `blocked_by`（谁阻塞本 WI），各条目 `{wi_id, kind}`；边端对象随 §4.10 权限硬边界先行裁剪 |
| `children` | `children` | 直接子票清单（`parent_id` = 本 WI，经 `idx_wi_parent`，§4.2/§4.2a）+ 派生 `rollup`：`{total, wrapped, running, blocked, queued}`——`total` 为直接子票总数，其余为对应状态计数（未列状态不单独计数，可由 `total` 差额得出）；**rollup 是查询时派生聚合，不落库**（无任何 Core 列/表承载，每次现算） |

include 拼装与基本记录读取在同一读事务内完成，保证一致性；默认（无 `include`）只返回基本记录 + `repo_inventory`（有清单时，§4.6）。三条 include 均不携带执行态（`events` 的 payload 受 INV-2c 词表约束，§5.1a）。

### 5.2 进度 read-through API（权威定义） <!-- §5.2 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（持久 WI Agent、owner generation、Revision/CAS/manifest、read-through）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

`GET /api/v1/work-items/{id}/progress` 返回 `{work_item_id, head_seq, head_generation, progress, head_updated_at, freshness_note}`；WI 不存在 → `CORE-404`；该 WI 从未发布任何 Revision 且其机器心跳仍存活（Capsule 尚在初次检出）→ `CORE-504`。

语义（`00-architecture.md#§2.3` INV-1 的接口面）：

1. 读 `capsule_heads` 得 `(seq, generation)`；若该 WI 从未 claim 过，返回 `head_seq: null` 及当前 Core 状态即可，不做后续步骤。
2. **进度不是 Core 侧的独立数据**，而是 HEAD 指向的那份 Revision 里 `runtime-state/execution.json`（权威 schema 见 `03-execution-capsule.md#§4.2`）的完整 JSON 内容本身——该文件当前 schema 未定义独立的 `progress` 字段，`current_step`/`workflow_status`/`steps`/`review_rounds`/`rewind_stats`/`budget_consumed`/`version` 等全部字段合起来就是对外可见的「进度」；若 `03` 未来新增专门的 `progress` 字段，本步骤改为只透传该字段，响应体的 `progress` 键形状不变。
3. **每次调用都直接 read-through，不维护任何独立落盘的物化缓存**：调用 `05-sync-revision-recovery.md#§7.4` 的远端现场重建过程，按 HEAD 的 `seq` 重建该 Revision 对应的完整同步根文件集（checkpoint+增量或 bootstrap 重放），取其中 `runtime-state/execution.json` 路径对应的内容（通过 sha256 从 CAS 块解压读出并 JSON 解析），将其整体作为响应体的 `progress` 字段值原样返回，不改写、不落任何 Core 列、不做二次聚合存储。AIHub 内部实现可以对 CAS 块做常规的读缓存/页缓存优化，但这是实现细节而非契约：契约上不承诺、也不要求存在任何按 WI/HEAD 维护的进度物化文件，也不定义任何缓存失效协议。
4. 新鲜度 = HEAD 更新时间，由 `05-sync-revision-recovery.md#§6` 的发布触发时机保证（HEAD 只在该流程的步骤 6 推进）；`freshness_note` 标注「数据截至最近一次发布」。AIHub 不主动向 Daemon/WI Agent 拉取实时状态，read-through 的读取对象永远是已发布的 Revision 现场，不是任何进程的内存状态。 〔#111分类：删→驱动〕

### 5.1a 事件与执行态键名附录（§2.6 INV-2c 引用的权威表） <!-- §5.1a -->

> **PARTIALLY ARCHIVED（2026-10-16）— 执行态字段黑名单（`work_items.attrs` / 事件 payload 不得承载执行态，`core/workitem.go`）是现行机制。**已删除、do not build from this 的部分**：事件表中的 `{generation}` payload、`capsule_takeover`、`budget_incremented`、`secret_rotated` 等事件类型。

`wi_events.event_type` 允许枚举（部署/生命周期/审计类，无 step/attempt/review 语义）：

| event_type | payload schema（允许字段） |
|---|---|
| `claimed` / `paused` / `resumed` / `blocked` / `unblocked` / `wrapped` / `failed` / `canceled` | `{generation, reason?}` |
| `capsule_registered` / `capsule_takeover` | `{machine_id, generation}` |
| `budget_incremented` | `{seq, dimension, amount}` |
| `secret_rotated` | `{epoch}` |
| `note`（人工备注，不载执行态） | `{text}`（≤4KB） |

**workflow 终态原因不进入 Core 事件流**：`failed_gate`/`failed_rewind_exhausted`（以及对应的 `WF-101`/`WF-102` 等 workflow 层错误码）不在上表枚举内，也不允许借用 `note` 或任何其它事件的 payload 携带——Core 只持久化这些终态映射出的业务信号（`blocked`/`waiting_on=owner`，见 §2.5）；具体是哪一种 workflow 终态导致，只保留在 Capsule 的 `execution.json`（`03-execution-capsule.md#§4.2`），需要时通过 §5.2 read-through 读取，Core 不重复记录。

**执行态键名黑名单（递归扫描，非仅顶层，修复 B22）**：`current_step`、`step_id`、`step_attempt_id`、`attempt`、`review_round`、`review_rounds`、`review_verdict`、`progress`、`workflow_status`、`steps`、`rewind_stats`、`budget_consumed`、`command_count`、`step_start_refs`、`pending_question`。校验器**递归遍历** `attrs`/payload 整个 JSON 树的每一层对象键（不只检查顶层），任何深度的对象键命中该词表均拒绝，不因嵌套在任意包装键之下（例如 `attrs.custom.current_step`、`attrs.foo.bar.budget_consumed`）而侥幸通过；数组元素内的对象同样递归扫描。拒绝错误码为 `CORE-400`（`reason=EXECUTION_STATE_REJECTED`，`detail` 携带命中的完整 JSON 路径，如 `attrs.custom.current_step`）。该词表逐一覆盖 `03-execution-capsule.md#§4.2` 当前权威 `runtime-state/execution.json` schema 的全部执行态字段（`current_step`/`workflow_status`/`steps`/`review_rounds`/`rewind_stats`/`budget_consumed`/`step_start_refs`/`pending_question`），而不是只列举示例性字段名——目的是让 `attrs`/事件 payload 这类自由 JSON 结构（无论嵌套多深）都拼不出任何一段完整或局部的 workflow execution state；`03` schema 未来新增字段时，必须先在此表登记再实现（本文与 `01-workflow-skills.md`/`03-execution-capsule.md`/`04-local-runtime.md` 共享同一词表权威在此）。

### 5.3 错误码表（前缀 CORE） <!-- §5.3 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（owner generation、Revision/CAS/manifest、flush 证据、takeover、read-through）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

| 码 | 语义 |
|---|---|
| CORE-400 | 请求非法（字段/类型/UNKNOWN_EVENT_TYPE/EXECUTION_STATE_REJECTED/INVENTORY_DUPLICATE_NAME/INVENTORY_URL_NOT_ALLOWED/INVENTORY_SUBDIR_UNSUPPORTED/INVENTORY_BASE_UNSUPPORTED/REPO_NOT_IN_PROJECT/parent_id 与依赖边约束（§4.2a：PARENT_DEPTH/PARENT_NOT_REGISTRY/PARENT_PROJECT_MISMATCH/BLOCKER_NOT_EXECUTABLE；跨 project 依赖边）/创建时 workflow.md frontmatter 校验失败——`detail` 携带命中 `rule_id`，§4.5） |
| CORE-401 | 凭证缺失或无效 |
| CORE-403 | token 或授权校验失败；body 携带 `reason`：`stale_generation`（fencing，§6.4）\|`secret_rotated_expired`（§6.6 宽限轮换后的过期）\|`secret_rolled_back`（§6.6 mode=rollback 作废：token 的 epoch 已被回滚标记 retired，**不可重新授权**，唯一出路是 admin force-takeover）\|`forbidden`（角色/权限不足，如非 admin 调 force-takeover/budget-increments、无 membership 访问他 project 对象，§4.10） |
| CORE-404 | 对象不存在（含权限硬边界下的「按不存在处理」，§4.10） |
| CORE-409 | 状态冲突（已认领/登记票不可 claim（NOT_CLAIMABLE）/清单仓库已不在项目中（INVENTORY_REPO_GONE，§4.6）/依赖环（DEPENDENCY_CYCLE）/索引重复/budget-increments 的 CAS 冲突） |
| CORE-410 | 终态不可写 |
| CORE-413 | payload 超限 |
| CORE-425 | FLUSH_REQUIRED：结束 generation 前 `flush_evidence` 缺失或校验未通过（§6.4），`reason` 细分见 §6.4 |
| CORE-503 | Core 忙（事务超时） |
| CORE-504 | 进度不可得（read-through 无可读对象，§5.2） |
| CORE-507 | 本地存储不可用（写失败、磁盘满） |
| CORE-508 | service.lock 已被持有（启动期） |
| — | （启动期锁拒绝：`cmd/aihub` 的文件锁直接退出，不经注册表错误码——polyforge#149 删除了未用的注册表常量） |
| CORE-509 | STALE_SERVICE：service_id 非当前注册行 |

## 6. 核心流程（编号步骤） <!-- §6 -->

### 6.1 claim（generation 仲裁，含幂等硬化） <!-- §6.1 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（owner generation、HMAC 令牌、takeover）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

1. 查 `claim_idempotency` 是否存在 `idempotency_key` 匹配的行。若存在：校验该行的 `caller_identity` 与 `work_item_id` 是否与本次请求完全一致（INV-2d）——三项均匹配才直接返回缓存的 `response_json`，不重新分配 generation；若 `idempotency_key` 相同但 `caller_identity` 或 `work_item_id` 不同，视为不同请求，拒绝为 `CORE-409 IDEMPOTENCY_KEY_REUSED`（防止跨身份或跨 WI 误命中缓存响应）。
2. 若无匹配行：加载 WI；不存在 → `CORE-404`；处于终态 → `CORE-410`；登记票（`bundle_name IS NULL`，§4.4）→ `CORE-409`（`reason=NOT_CLAIMABLE`）；已存在活跃 generation 行 → `CORE-409 ALREADY_CLAIMED`。
3. 若均未命中上述拒绝条件：新 generation `g = 当前最大 generation + 1`，插入一行 `owner_generations`（`ended_at` 为 NULL）；WI 的 `assignee` 设为调用方、`status` 转 `running`、`waiting_on` 清空。
4. 取当前生效的 `service_secret[current_epoch]`，计算 `token = HMAC-SHA256(secret, wi_id|g|machine_id)`，组装响应体 `{generation: g, generation_token: token, secret_epoch: current_epoch, capsule_bootstrap: {…}}`。
5. 将 `(idempotency_key, work_item_id, caller_identity, response_json, now)` 写入 `claim_idempotency`，与第 3/4 步在同一事务内原子提交。
6. 追加 `claimed` 事件（`{generation: g}`），返回响应体。

若请求发出后、响应返回前调用方进程崩溃：调用方重启后带同一 `idempotency_key` 重发，命中第 1 步缓存直接返回之前结果，不重新分配 generation，也不报 `CORE-409`。若本地的 `idempotency_key` 也丢失（磁盘同时损坏）：唯一人工介入路径是查询 WI 当前 `assignee`/generation，若确认属于本机则手动 `force-takeover`（自接管，仅升 generation，不影响语义）后重建本地现场。

### 6.2 generation 生命周期（§2.3 的流程面） <!-- §6.2 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（owner generation、flush 证据、takeover）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

1. `pause`/`block`/`wrap`/`fail`：先校验 §6.4 的 `flush_evidence`，通过后将当前活跃行 `ended_at` 设为 `now`、`end_reason` 设为对应值；`assignee` 保留为最后一任 owner（作为历史记录）。
2. `force-takeover`：不需要 `flush_evidence`（旧 owner 可能已失联，无法提供证据）；将旧活跃行 `end_reason` 设为 `takeover`，开新一行 `generation+1`；旧 owner 之后任何携旧 token 的写请求均因校验失败而返回 `CORE-403`（`reason=stale_generation`）。
3. `resume`/`unblock`：不存在“恢复旧 generation”的路径，完全按 §6.1 的步骤重新走一次 claim（仅把前置状态校验改为 `paused`/`blocked`）。

### 6.3 read-through（§5.2 的流程面） <!-- §6.3 -->

1. 读 `capsule_heads` 得 `(seq, generation)`；不存在 → 返回 `{head_seq: null}`。
2. 按 `05-sync-revision-recovery.md#§7.4` 的重建过程将该 `seq` 对应 Revision 重建为完整文件集，解析其中 `runtime-state/execution.json`（`03-execution-capsule.md#§4.2`）。
3. 将该文件的完整内容（或其未来新增的 `progress` 字段，若 `03` 演进出该字段）直接作为响应返回；每次调用都现读现算，不写入、不查询任何独立的物化缓存文件。

### 6.4 generation 收尾的 flush 证据协议（B03 修复） <!-- §6.4 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（持久 WI Agent、owner generation、Revision/CAS/manifest、flush 证据、watcher dirty 位）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

**适用范围**：`pause`、`block`、`wrap`、`fail` 四个使当前活跃 generation 结束的端点，调用时必须携带 `flush_evidence` 字段，证明本 generation 已完成最后一次全扫描发布（或确认无需发布）。`cancel` 不适用本协议（见 §6.5：只能作用于本来就没有活跃 generation 的 WI）。

**请求字段**（`flush_evidence` 对象，随 4.个端点的 body 一同传递）：

| 字段 | 类型 | 语义 |
|---|---|---|
| `published_generation` | int | 本次调用方自己的活跃 generation（必须与 token 解析出的 generation 一致） |
| `published_seq` | int | 本 generation 最后一次全扫描发布得到的 Revision `seq`；若本 generation 从未产生新发布（例如紧接着 claim 就立即 pause），则填写上一个已存在的 HEAD `seq` |
| `manifest_sha` | string | `capsule_revisions` 中 `(work_item_id, published_seq)` 行的 `manifest_sha`，由调用方从本地已发布记录中取得 |
| `frozen_checkpoint` | object | **本次不可逆动作（generation 结束）前的最后一次 write freeze + 全量扫描的凭证，绑定冻结后的最终本地检查点**，字段为 `{scanned_at, bytes_digest, worktree_state, manifest_sha, matches_head}`：`scanned_at` 是本次冻结扫描完成的时刻（RFC3339）；`bytes_digest` 是冻结扫描覆盖的全部同步面字节的聚合摘要（对排序后的 `(path, sha256, mode)` 列表按 RFC 8785 canonical JSON 序列化后取 sha256，与 `05-sync-revision-recovery.md#§2.7` staging 固化的字节一一对应，空同步面时等于 `EMPTY_SYNC_BYTES_DIGEST` 常量）；`worktree_state` 是冻结时刻 worktree 状态摘要（各 inventory repo 的 `{name, base, head, dirty_count}` 列表同法序列化的 sha256）；`manifest_sha` **恒取随附外层 `manifest_sha`（= 当前 HEAD 行摘要）——对齐声明**（生成协议权威 `05-sync-revision-recovery.md#§6.1`：无改动时取 HEAD 行摘要，有改动则先发布、再取新 Revision 即新 HEAD 的行摘要——二者最终都等于提交时刻 HEAD 的行摘要，规则 6 的相等校验因此恒可执行，跨代无改动收尾同样成立）；不论是否产生新 Revision，冻结扫描均必须实际执行一次（`05-sync-revision-recovery.md#§2.7` 的 write freeze + 扫描，不允许仅凭旧 HEAD 推断）——**内容比较由 `bytes_digest` 承担**，manifest 因含 seq/parent/generation 不做内容比较；`matches_head` 相对**本端点调用提交时刻的当前 HEAD** 判定（生成协议权威在 `05-sync-revision-recovery.md#§6.1`）：有改动时先完成发布再提交，扫描产物即已是 HEAD，恒为 `true`；`false` 仅为「扫描发现改动但未完成发布即提交」的失败形态（规则 6 拒为 `UNPUBLISHED_CHANGES`），不存在以其收尾成功的路径。「内容未变」的判定输入是 `bytes_digest`（稳定字节摘要）；`manifest_sha` 含 seq/parent/generation，仅作审计与 HEAD 行一致性校验，不承担内容比较。本表**不设** `no_new_changes` 之类的自报布尔——「无新改动」只能由 `matches_head=true` 加上述摘要校验承载 |

**服务端校验规则**（同一事务内）：

1. 从请求的 `X-PF-Generation-Token` 解出 `(wi_id, generation, machine_id)`，校验 token 本身有效（否则根据原因返回 `CORE-403`，不进入 flush 校验）。
2. 若 `flush_evidence` 整体缺失，或缺失 `frozen_checkpoint` → `CORE-425 FLUSH_REQUIRED`（`reason=MISSING`）。
3. 若 `flush_evidence.published_generation ≠` token 解出的 generation → `CORE-425 FLUSH_REQUIRED`（`reason=GENERATION_MISMATCH`）。
4. 读 `capsule_heads` 得当前 `head.seq`；若 `flush_evidence.published_seq ≠ head.seq` → `CORE-425 FLUSH_REQUIRED`（`reason=NOT_HEAD`，意味着调用方声称的最新发布与 Core 记录的当前 HEAD 不一致）。
5. 读 `capsule_revisions` 中 `(wi_id, head.seq)` 行的 `manifest_sha`；若与 `flush_evidence.manifest_sha` 不一致 → `CORE-425 FLUSH_REQUIRED`（`reason=MANIFEST_MISMATCH`）。
6. **校验 `frozen_checkpoint`（绑定冻结后的最终本地检查点）**：`frozen_checkpoint` 必须含全部五个事实性子字段——`scanned_at` 早于本次请求到达时刻超过一个可配置窗口（缺省 60s）→ `CORE-425`（`reason=FROZEN_CHECKPOINT_STALE`，防止复用远早之前的一次扫描作为“冻结后最终”证据）；`bytes_digest` 缺失或非 64 位 hex → `CORE-425`（`reason=FROZEN_CHECKPOINT_MISSING`）；`worktree_state` 缺失同因拒绝。若 `matches_head` 为真但 `frozen_checkpoint.manifest_sha ≠ flush_evidence.manifest_sha`（自相矛盾）→ `CORE-425`（`reason=FROZEN_CHECKPOINT_MISMATCH`）；若 `matches_head` 为假 → `CORE-425`（`reason=UNPUBLISHED_CHANGES`，最后一次冻结扫描实际发现了与 HEAD 不一致的本地变化，必须先完成一次真正发布才能结束 generation）。证据因此绑定「冻结后的最终本地检查点」本身（字节聚合摘要 + worktree 状态摘要），不存在仅凭某个旧 HEAD 值直通的形态。
8. **无 HEAD 的空链分支（与 `05-sync-revision-recovery.md#§6.1` SYNC-INV-9 逐字一致）**：若该 WI 从未发布过任一 Revision（`capsule_heads` 无行，bootstrap 亦未发生），第 4/5 步无校验对象；此时接受且仅接受 `published_seq=0`（哨兵值，与 `05-sync-revision-recovery.md#§2.1` 的 seq=0 首修订明确区分）、`manifest_sha` 为空、且 `frozen_checkpoint.bytes_digest == EMPTY_SYNC_BYTES_DIGEST`（空同步面聚合摘要常量：空列表的 canonical JSON 序列化摘要 `sha256("[]") = 4f53cda18c2baa0c0354bb5f9a3ecbe5ed12ab4d8e11ba873c2f11161202b945`，与 `05-sync-revision-recovery.md#§6.1` 逐字一致；该情形下忽略 `matches_head` 子字段）的 `flush_evidence`；`published_seq>0`、缺 `frozen_checkpoint`、或 `bytes_digest` 不等于该常量 → `CORE-425`（`reason=NOT_HEAD`/`MISSING`）。一旦本 generation 产生过任何同步面写入，必须先完成发布形成 HEAD，再回到常规校验，不得以空链分支静默丢弃改动。
本协议不要求 `capsule_heads.generation` 必须等于调用方自己的 generation：如果本 generation 自始至终未产生任何改动（例如刚 claim 就立即 pause），HEAD 仍可合法属于更早的 generation；该情形的合法性由 `frozen_checkpoint` 的真实冻结扫描承载（`matches_head=true` 且其 `manifest_sha` 与 HEAD 一致、`bytes_digest`/`worktree_state` 齐备），不存在自报布尔的直通路径，校验规则不变（仍需 `published_seq`/`manifest_sha` 与当前 HEAD 一致）。
**被 fencing 的旧代无需成功发布即可本地封存退出**（B03 第二半）：一旦 WI Agent 的 generation 已被接管（任何 Core 调用返回 `CORE-403` 且 `reason=stale_generation`），它对 `pause`/`block`/`wrap`/`fail` 的调用必然失败（token 已无效），因此**不要求、也不可能要求**它先完成一次成功发布才能停止：合法的退出路径是将本地未发布的中间状态写一份本地封存记录（写入 Capsule `logs/`，不同步）供事后审计，然后直接退出进程，不尝试任何 Core 调用。正确性不依赖这一步：接管它的新 generation 会从它最后一次成功发布的 HEAD 开始重建现场（`05-sync-revision-recovery.md#§7.4`），旧代未发布的本地改动本来就不在新代的恢复范围内。 〔#111分类：删→驱动〕

### 6.5 非活跃 WI 的终止（cancel）契约（B05 修复） <!-- §6.5 -->

> **PARTIALLY ARCHIVED（2026-10-16）— cancel 契约是现行机制**：只适用于 `queued`/`paused`/`blocked`，授权为 reporter、该 project 的 maintainer 或 admin（`server.go`），`running` 的 cancel 答 `CORE-409`。**已删除、do not build from this 的部分**：以 owner generation 表述的前置条件（现为 session 模型）与 `force-takeover` 路径。

`cancel` 只适用于 `queued`/`paused`/`blocked` 三种非活跃状态（`running` 需先 `pause`/`block` 或被 `force-takeover` 后再 cancel，不在本契约范围内）。三种状态的授权与前置条件完全一致，不需要持有任何活跃 generation 令牌：

1. **`queued`**：该 WI 从未发生过任何 claim，不存在 `owner_generations` 行。授权：调用方为 `reporter`、该 project 的 maintainer 或 `role='admin'`。成功后 `status → canceled`，`closed_at` 赋值，不涉及任何 generation 行。
2. **`paused`**：全部历史 generation 均已结束（§2.5）。授权同上。成功后 `status → canceled`，不要求先 `resume`。
3. **`blocked`**：同样无活跃 generation（无论进入 `blocked` 的原因是 `waiting_on=owner`、还是§6.4 提到的 workflow `failed_gate`/`failed_rewind_exhausted` 映射而来）。授权同上。成功后 `status → canceled`，**不要求先 `unblock` 或重新执行任何 step**。

**workflow 失败终态的映射与可取消性**：`01-workflow-skills.md#§6.5` 定义的 `failed_gate`/`failed_rewind_exhausted` 是 Capsule 侧（`execution.json.workflow_status`，`03-execution-capsule.md#§4.2`）终态，WI Agent 检测到这两种终态后，按 §6.4 的 flush 证据协议调用 `block` 端点（`waiting_on=owner`）。Core 侧只接收并落盘这一次状态迁移本身（`status=blocked`、`waiting_on=owner`，以及 §5.1a 通用 `blocked` 事件 `{generation, reason?}`）；具体是 `failed_gate` 还是 `failed_rewind_exhausted` 这一区分不随事件或任何字段进入 Core（Core 不新增专属事件类型来承载它，INV-2c），该区分永远只能从 Capsule 的 `execution.json` 或 read-through（§5.2）读到。进入 `blocked` 之后，该 WI 就是本节定义的普通 `blocked` WI，可直接按上述第 3 条取消，**不要求先重新执行任何 step 才能取消**。 〔#111分类：删→驱动〕

### 6.6 凭证轮换后的重新授权（B09 修复） <!-- §6.6 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（持久 WI Agent、owner generation、HMAC 令牌、takeover、rotate-secret）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

**两种轮换模式（管理面操作 M5，不占业务 26 端点口径；由 `mode` 区分，互不混用）**：经 `POST /api/v1/service/rotate-secret`（行 M5，`role='admin'` 或启动自检触发）接受 body `{mode: "grace"|"rollback", grace_seconds?}`：

- `mode="grace"`（缺省，即下文的常规轮换）：旧 epoch 进入宽限期后才失效，适用于定期安全轮换、无异常事件触发的场景。
- `mode="rollback"`（**rollback 专用，与 `07-deployment-pilot.md#§6.4` 的回滚序列绑定，不可用于常规轮换**）：旧 epoch **立即**失效（`grace_seconds` 强制视为 0，不接受非零值，否则 `CORE-400`）：本次插入的 `service_secret_epochs` 行在同一个事务内将该新 epoch 之前的**所有**未 `retired_at` 的旧 epoch 一并标记 `retired_at=now()`并删除对应 `service.secret.<epoch>` 文件，不等待任何定时任务；这保证回滚前签发的全部 capability token（不论属于哪个旧 epoch）在本次调用返回后立即无法通过 HMAC 校验，不存在「旧凭证复活」窗口。`rollback` 模式 **MUST NOT** 复用 `mode="grace"` 的宽限期路径。

**常规（`mode="grace"`）轮换过程**：

1. 管理员经 `pf` 管理 CLI 调用 `POST /api/v1/service/rotate-secret`（行 M5，需 `role='admin'`），或系统在启动时发现 `service.secret.<epoch>` 丢失而自动触发。
2. Core 生成新 `epoch = 当前最大 epoch + 1`，随机生成 32 字节新 secret 写入 `<data_dir>/service.secret.<epoch>`（0600），向 `service_secret_epochs` 插入新行，当前生效 epoch 指针随行持久化（键值元数据表已删，polyforge#149）。
3. 旧 epoch 不立即失效：进入一个宽限期（缺省时长可配置，建议足够覆盖一次正常心跳周期），期间 `service_secret_epochs.retired_at` 为 NULL，仍可用于校验用这个 epoch 签发的旧 token（§5.1 已定义）。
4. 宽限期局后，定时任务将 `retired_at` 写上并删除对应 `service.secret.<epoch>` 文件；之后用该 epoch 签发的 token 一律 `CORE-403`（`reason=secret_rotated_expired`）。

**`mode="rollback"` 与现场重建**：回滚不仅需要凭证失效，还需要本机现场隔离与重建，两者分属 Core 与 Daemon 两侧，不共享任何路径：Core 侧只负责上述立即失效；Daemon 侧的现场重建权威在 `04-local-runtime.md#§7`：回滚后的 Daemon 检测到自己持有的 token 的 `secret_epoch` 已被 `mode="rollback"` 标记 `retired_at` 且**无宽限期**后，**MUST NOT** 调用本节的 `reauthorize`（后者仅适用于 `mode="grace"` 轮换的宽限期内重新签发，不适用于回滚），而 MUST 按 `04-local-runtime.md#§7.3` 的回滚专用路径：停止其管理的 WI Agent/subagent 进程组、本地 quarantine 现场。**不存在自动 re-claim 路径**：回滚恢复出的 active generation 行由 admin 通过 `force-takeover`（§6.2）仲裁接管（Gen-3 澄清 5；`04-local-runtime.md#§7.3`/`07-deployment-pilot.md#§6.4` 为该仲裁规则的权威），接管后开启新 generation、新 harness session。 〔#111分类：删→驱动〕

**新 key 的 claim 不受影响**：claim 属于 §5.1 类别 2（无活跃 generation），只需 api_key身份有效且 WI 状态合法，与 service_secret 无关；轮换不得以任何形式拒绝一个带有合法 api_key 的新 claim 请求。

**仍持有 active generation 的 WI Agent 如何重新授权（reauthorize 流程 = claim 幂等重放，无独立端点）**： 〔#111分类：删→驱动〕

1. WI Agent 使用旧 token 发起写请求时收到 `CORE-403`，`reason=secret_rotated_expired`（与 `stale_generation` 区分开，后者意味着真正被接管，不应重新授权，只能按 §6.4 本地封存退出）。 〔#111分类：删→驱动〕
2. **重放由 Daemon 自身执行（B09 裁定）**：WI Agent 不持有任何 Core 凭证（§8），且 reauthorize/secret 类操作在 WI Agent 代理白名单外（`04-local-runtime.md#§5.3` 返回 `DAEMON-403`，该排除保持有效）——正确路径是：WI Agent 收到 `secret_rotated_expired` 后将该失败经内部通道上报 Daemon，**Daemon 以机器凭证直接重放**（携带当初的同一 `idempotency_key`、同一调用方身份、对同一 WI 重放行 7 claim；发起者是 Daemon 进程本身而非代理转发，不占用代理白名单），重签 token 随内部通道交回 WI Agent。不引入任何新端点（§5.1 canonical 清单不因此变动）。 〔#111分类：删→驱动〕
3. Core 命中 `claim_idempotency`（命中分支先于 WI 状态校验的顺序保持不变，§6.1）后校验：`owner_generations` 中 `(work_item_id, generation)` 行仍为活跃（`ended_at IS NULL`），且与幂等记录绑定的身份匹配。generation 已结束 → `CORE-404`；行不活跃或身份不符 → `CORE-403`（`reason=forbidden`）。幂等行永不过期、无任何清理任务（§4.3 补登裁定），故活跃 WI 的重放在宽限期内必然可命中缓存行。
4. 均通过后，按下方「幂等响应重签」规则，用当前 `current_secret_epoch` 的 secret 重新签发同一 `(wi_id, generation, machine_id)` 的 token 并返回。**不增加 generation**，不重新创建 Capsule 现场；同 key 重复重放天然幂等，不需额外表。

**重放旧 key 的幂等请求不得返回含旧失效 token 的响应**：`claim_idempotency.response_json` 里缓存的 token 绑定了它被签发时的 `secret_epoch`。当命中幂等缓存时，Core 在返回前多一步检查：若缓存响应体中的 `secret_epoch` 已非 `current_secret_epoch` 且已超出宽限期（即那个 epoch 已 `retired_at` 非空），则先按 `service_secret_epochs.mode` 判别作废模式（`grace`\|`rollback` 随 epoch 行持久化，判别可跨重启恢复）：若 **该 epoch 的 mode=rollback**，**不重签、不返回缓存内容**，而是返回 `CORE-403 reason=secret_rolled_back`——回滚作废没有重新授权路径，唯一出路是 admin `force-takeover`（§6.2；Gen-3 澄清 5），确保不能绕过 force-takeover 复活回滚恢复出的 active generation。**回滚边界判定（权威，可跨重启恢复）**：设 `R` = 历史上最近一次 mode=rollback 的 epoch 行号。**任一凭证（token 或幂等缓存）的绑定 epoch ≤ R 时，无论该 epoch 自身的 mode 是 grace 还是 rollback、也无论 R 之后又发生过多少次 grace 轮换，一律按 `secret_rolled_back` 拒绝**——回滚废除的是该边界之前的一切凭证信任，永久生效、不被后续 grace 轮换恢复；token 校验与 claim 幂等重放共用这一条判定（同一 middleware 函数）。仅当绑定 epoch > R（回滚之后新签发的凭证）且其 epoch 因 **mode=grace** 过期时，才走 grace 重签分支；epoch > R 且未过期则正常通过。反例闭合：rollback 前的 mode=grace 缓存 token 在后续任意 grace 轮换后仍 ≤ R → 拒绝；回滚后新代凭证 > R → 正常。，更新并回写 `claim_idempotency.response_json`（仍是同一次 claim 的结果，只是 token 的有效性被透明刷新，generation 不变），再返回。这样重放请求永远不会拿到一个当前已不可验证的 token，也不会在回滚场景错误复活旧凭证。

### 6.7 超预算后的增加预算 API（B11 修复） <!-- §6.7 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（持久 WI Agent、owner generation、Revision/CAS/manifest）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

**端点**：`POST /api/v1/work-items/{id}/budget-increments`，body `{dimension, amount, note?, based_on_seq}`。

**授权规则**：调用方必须是以下之一：该 WI 的 `reporter`、该 project 的 maintainer、或 `role='admin'`。不要求调用方持有该 WI 的 generation token——这是人工在 WI 处于 `blocked`（`waiting_on=owner`）时从外部做出的决定，不是当前执行现场的操作。不符合上述角色 → `CORE-403`（`reason=forbidden`）。

**CAS 规则**（避免并发覆盖）：

1. 调用方先 `GET .../budget`（行 12）读得当前（跨全部 dimension）的最大增量 `seq`，以此作为请求中的 `based_on_seq`（若该 WI 尚无任何增量行，`based_on_seq=0`）；不存在独立的 `GET budget-increments` 读取端点。
2. Core 在事务内重新查询该 `work_item_id` 的当前最大 `seq`（不分 dimension，序列全局）；若与请求中的 `based_on_seq` 不一致，说明有其他并发增量已写入，拒绝为 `CORE-409`（body 携带当前实际最大 `seq`，调用方重读后重试）。
3. 一致则插入新行 `(work_item_id, seq=based_on_seq+1, dimension, amount, granted_by, now, note)`，追加 `budget_incremented` 事件。不同 dimension 的并发增量基于同一 `based_on_seq` 竞争时恰一成功，失败方重读重试——序列全局化使并发比较对象唯一，与 §4.5a 主键逐字一致。

**预算口径**：Core 不持有预算数据（基线列已删，polyforge#149）；预算消耗与上界判定全部由执行侧账本（`runtime-state/execution.json` 的 `budget_consumed`，driver 单写者）承担（与 `01-workflow-skills.md#§6.6` 一致）。 〔#111分类：删→驱动〕

人工增加预算后，WI 需按 §2.5 的 `unblock` 重新激活（新一次 claim），WI Agent 原地重派之前被停止的 step；若人工判断不值得继续，则直接按 §6.5 取消该 WI。 〔#111分类：删→驱动〕

## 7. 并发与崩溃恢复 <!-- §7 -->

- **单写者**：全部写经一个 SQLite 连接的事务（INV-2a）；WAL 模式允许读并发。`busy_timeout` 兵底读端偶发锁等待。
- **崩溃点与恢复**：进程崩溃 → WAL 回放，事务原子性保证无半写；重启后 `service.lock` 释放并重新自注册。SQLite 文件损坏 → 停止服务（`CORE-507` 告警），按 §11 从最近备份恢复，不提供带伤运行。
- **凭证轮换期间的崩溃**：若 AIHub 进程在轮换中途崩溃（新 epoch 已写入 `service_secret_epochs` 但生效指针尚未更新，或反之）：重启后以持久化的生效 epoch 指针为准重新确定当前生效 epoch（键值元数据表已删，polyforge#149），未被指向但已存在于 `service_secret_epochs` 的行视为未完成的轮换，可直接作废（不删 secret 文件，下次轮换自然覆盖）。
- **心跳过期**：`machines.last_heartbeat_at` 超过阈值（缺省可配置）的 Daemon 标记 stale；就绪队列响应（§5.1b）不为其设置任何独立段——stale 与正常 `running` WI 同样只出现在 `running` 可见性段，stale 经 §9 监控面暴露，供人决策 force-takeover（Core 不自动接管，避免与 `05-sync-revision-recovery.md#§7.3` 的 takeover 竞态）。

## 8. 安全边界 <!-- §8 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（owner generation、takeover、rotate-secret）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

- 写 API 三重校验：api_key（身份）→ WI 级 generation 凭证（有活跃 generation 时）→ 状态机合法性。过期 generation 一律 `CORE-403`，防止被接管方残留写入。
- **api_key project scope 由 HTTP middleware 在进入任何业务 handler 之前强制执行**（§4.10）：`api_keys.project_id` 非 NULL 的 key 对 scope 外 project 的一切对象（含列表/过滤/检索请求中的 project 维参数）一律拒绝，不依赖各 handler 自觉校验；scope 只收窄不扩大授权（scoped key 的 user 仍须本就在 `project_members` 中）。无 membership 者的不可见性（§4.10 权限硬边界）同样在 middleware 层先于 handler 生效。**两面分层（澄清 7 的 middleware 落地；D4 修订 polyforge#150）**：membership 不可见性只作用于**数据面端点**（A 组 WI 业务、E 组 Memory、管理面）——机器调用方在这些端点上按 membership OR admin 判定；**执行面端点**（B 组 claim/force-takeover/session-end/budget、C 组同步、D 组心跳）不做 membership **可见性过滤**，但 claim 本身仍要求该 project 的 writer/maintainer membership 或全局 admin（D4：`hSessionClaim` → `requireWrite`，403 otherwise），其余执行面端点按角色（force-takeover 限 admin）与 generation token 判定；claim 响应携带 bundle 引用与预算基线，机器无需任何数据面读取即可完成执行路径（`04-local-runtime.md#§6.1`）。
- api_keys 只存哈希；service_secret 仅存在进程内存 + 本地文件（0600），不入库。本地目录 `memory/`、`capsules/`、`backups/` 权限 0750、属主 aihub 进程用户。
- Core API 不提供任何「删除目录/工作树」端点：远端数据的删除只有 `05-sync-revision-recovery.md#§2.10` 的带保护删除流程；运维清扫面的入口被结构性移除。
- `budget-increments`、`force-takeover`、`rotate-secret` 等敏感操作均基于 `users.role` 而非 generation token 授权，避免把人工决策面错误地绑定到执行现场凭证上。

## 9. 监控 <!-- §9 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（持久 WI Agent、flush 证据、read-through）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

- service 心跳年龄（告警阈值可配）；Core 写事务 p99 延迟；`capsule_heads.updated_at` 滞后分布（= 各 WI 同步新鲜度）；read-through 进度查询延迟 p99。
- `CORE-403`/`CORE-425`/`CORE-509` 计数分别监控：`CORE-403` 尖峰常对应接管事件或凭证轮换窗口；`CORE-425` 尖峰提示 WI Agent 侧 flush 协议实现可能有缺陷；`CORE-509` 任何非零计数都需人工审查。 〔#111分类：删→驱动〕
- stale Daemon/机器数量（心跳过期）；就绪队列长度；`workflow_budget_increments` 的频率（频繁增量提示 workflow 预算声明偏保守，反馈给 `01-workflow-skills.md` 的参考流程作者）。

## 10. 测试与故障注入 <!-- §10 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（owner generation、Revision/CAS/manifest、flush 证据）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

测试类别（不给具体历史数据或日期，量化指标与回归清单权威在 `07-deployment-pilot.md`）：

- DDL 审计测试：断言全表列集中不存在 `step`/`attempt`/`review`/`progress` 类执行态列（INV-1 机械断言）。
- 状态机矩阵测试：§2.5 全部合法迁移 + 全部非法迁移（断言对应 `CORE-409`/`CORE-410`）。
- generation 并发测试：双会话同时 claim 同一 WI → 恰一成功一 `CORE-409`；活跃行唯一索引冲突回滚断言。
- flush 证据协议测试：分别构造 `flush_evidence` 缺失、generation 不匹配、`published_seq` 与当前 HEAD 不一致、`manifest_sha` 不匹配四种情况，断言均拒绝为 `CORE-425` 且 `reason` 正确；另断言已被 fencing 的旧代不需要任何成功调用即可本地退出。
- 非活跃 WI 取消测试：分别对 `queued`/`paused`/`blocked`（含由 `failed_gate`/`failed_rewind_exhausted` 映射而来的 `blocked`）构造 cancel 请求，断言均无需持有活跃 generation 令牌即可成功。
- 凭证轮换测试：轮换后旧 token 在宽限期内仍可验证但提示 `secret_epoch`；宽限期外旧 token 拒绝为 `secret_rotated_expired`；`reauthorize` 成功后 generation 不变但 token 已更新；重放旧 `idempotency_key` 在轮换后不返回已失效 token。
- 预算口径测试：断言 Core 的 `work_items` 行在预算增量前后字节不变（Core 不持有预算数据，polyforge#149）。
- **可注入故障类别**：SQLite 文件损坏（预期 `CORE-507` 停服 + 备份恢复路径）；磁盘满（预期写失败原子回滚，无半写）；kill -9 写事务中（预期 WAL 回放后无半写）；service.lock 双进程（预期 `CORE-508` 第二进程退出）；心跳停止超过阈值（预期机器标记 stale、其 running WI 仅出现在就绪队列 `running` 可见性段、无独立 stale 段（§5.1b）且无自动接管）。

## 11. 迁移与 rollout <!-- §11 -->

- DDL 带 `schema_version`；升级 = 停服 → 迁移工具（向后兼容的建表/加列）→ 起新（§2.2 三层锁保证不双写）。回滚 = 恢复 `<data_dir>/backups/` 最近快照（每日一次 SQLite 热备 + WAL 归档）。
- 一期从无到有：首次启动建库 v1，无任何 schema/存量数据迁移（新系统；唯一例外是 Memory 存量导入——那是一次性管理 CLI 直写动作，权威在 `06-memory-search.md#§10`，不属建库迁移）。
- 升列（§4.2 attrs 拆分演进）必须升 `schema_version` 并保持旧读兼容一个版本。
- Daemon/WI Agent 的升级与 AIHub 的升级可以错开，但不得让旧版 Daemon 拿着不同 hash 算法的 token 请求新版 Core（token 结构改变必须随 `schema_version` 升级，不单独演进）。 〔#111分类：删→驱动〕

## 12. 未决问题 <!-- §12 -->

1. WI 事件流的保留策略（全量保留 vs 归档裁剪）：一期全量保留，容量压力出现后再议；不与已决冲突。
2. read-through 是否增加「WI Agent 在线时直接查询」旁路以获得零延迟进度：一期不做，仅以 HEAD 新鲜度为准；开放，引入时不得违反 INV-1。 〔#111分类：删→驱动〕
3. 多 project 的配额与可见性矩阵：一期单 project 主用，开放。
4. service_secret 宽限期时长的默认值与是否允许按 WI 级别差异化配置：开放，不影响 §6.6 机制本身。
