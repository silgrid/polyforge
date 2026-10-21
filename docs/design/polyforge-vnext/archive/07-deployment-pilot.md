# 07 — Deployment / Pilot：一期部署形态、回归清单与否决门槛

> **ARCHIVED 2026-10-16** (repo review 2026-10-16 wave 4): describes the v0/v1 design and is kept for history only — do not build from it. Current reference: [reference.md](../reference.md); decisions: [v2-converge.md](../v2-converge.md). Live mechanisms this file still held were moved there before archiving; section anchors are unchanged.

> **ARCHIVED（repo review 2026-10-16 G-1）— 本文大部分描述已删除的机制：`pf upgrade`、`DEPLOY-*` 码、`aihub migrate` / `schema_version`、`rotate-secret` 回滚、`machines register`、Interactive/Batch/Drain（§3、§6.5 已按 #174 更新）。标有 ARCHIVED 横幅的节 do not build from this；未标横幅的节也只作历史与溯源参考。现行设计权威：[v2-converge.md](../v2-converge.md)、[wi-kind-contract.md](../wi-kind-contract.md)、[08-capsule-base-migration.md](../08-capsule-base-migration.md)；与代码冲突时一律以代码为准。**
> Most of this document describes deleted mechanisms. Sections bannered ARCHIVED must not be built from; the live authority is v2-converge.md plus the code.

| 权威项 | 值 |
|---|---|
| 文档定位 | 一期部署形态（SQLite + 本地目录）、安装/升级/回滚顺序、13 条回归清单（#699…#780）、Pilot 范围/指标/否决门槛、与旧 aihub 系统的并存与切换 |
| 上游基线 | 全部 `00`–`06` 文档；`00-architecture.md#§1.2`/§2.3（INV-10/INV-12）/§3.4/§3.5/§3.6/§3.7/§4.3/§11 |
| 术语权威 | 本文负责：一期部署形态、部署 CLI 面与 `DEPLOY-*`/`PILOT-*` 错误码、rollout 顺序、回归清单、Pilot 否决门槛（veto gate） |

## 1. 范围与非目标 <!-- §1 -->

### 1.1 范围 <!-- §1.1 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（PILOT-* 码、Interactive/Batch/Drain）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

- 一期部署形态：单 active AIHub + SQLite + 本地目录，**无 PostgreSQL / JuiceFS / S3**（INV-10），
  Daemon 统一作为 Interactive / Batch / Drain 三种 WI Job 执行模式的唯一执行者（`00-architecture.md#§3.4`），
  Linux/macOS 均为宿主机原生执行、v1 可信内部仓库边界下不引入 per-step 隔离（INV-12，`00-architecture.md#§3.7`）；部署单元清单与目录布局
  （§3/§4）。
- 部署面：AIHub 服务主机的 `.polyforge/deploy.sh`、工作机的 `tools/install.sh` 与 `pf daemon ensure`
  的语义与 `DEPLOY-*`/`PILOT-*` 错误码（§5）。
- 安装 / 升级 / 回滚的编号步骤序列，以及升级窗口内的并发与崩溃恢复（§6/§7）。
- **回归清单**：对照 `aihub#699 / #709 / #714 / #744 / #747 / #749 / #754 / #770 / #773 / #775 /
  #777 / #778 / #780` 共 13 条，逐条给出缺陷摘要、vNext 交叉引用与机械反例断言（§10.2）。
  考证方法（B19）为逐条检索**本仓 git 历史**（撰写本文档所用的同一 worktree，只读）：
  `git log --all --grep='#<n>'`，无命中的再按缺陷关键词补充检索并以 `git show --stat <hash>`
  复核修复落点；§10.2 每条记录检索到的真实 commit（hash + 主题 + 修复落点）或经检索验证的
  「本仓无对应 commit」结论，不以「编号可追溯」替代内容考证，也不编造 commit hash、PR 标题或
  改动细节。
- Pilot 的范围、参与面（含 Interactive/Batch/Drain 三种模式与 Daemon 优雅停机路径的实际验证）、
  观测指标（引用 `00-architecture.md#§9` 的分层总则）与否决门槛：每条门槛给出可机械判定的判定公式（§9）。
- vNext AIHub 作为**新仓部署物**与旧 aihub 系统的并存、Pilot 通过后的切换边界（§2.1/§11）：
  vNext AIHub 是新 repo 实现的部署物（`00-architecture.md#§3.3` 正式取代声明），不由现有 aihub
  代码库演进而来。

### 1.2 非目标 <!-- §1.2 -->

- 不设计 HA / 多 active AIHub / 云上部署（`00-architecture.md#§1.2`）。
- 不设计 workflow-v2 数据迁移（vNext 是从无到有的新系统，`00-architecture.md#§1.2`/§11）。
- 不设计 Windows 平台或 K8s 内运行 Daemon 的一期形态（`00-architecture.md#§1.2`）。
- **不假设任何 per-step 隔离机制是一期部署内容**：v1 的威胁模型边界——**可信内部仓库**，执行形态是
  **宿主机原生**：权威定义在 `00-architecture.md#§3.7`/INV-12，`03-execution-capsule.md#§1.2`/§5/§6.2 落实其在 Capsule 侧的具体表现；本文
  不列举该边界排除的具体隔离机制名称，只承接结论——部署单元清单（§3）与安装/升级流程（§6）均
  不包含任何此类机制的安装、配置或对齐验证步骤，依赖的只是宿主机自身已安装的工具链（§2.1）。面向不可
  信第三方代码的隔离化 untrusted runner 是独立演进方向，不属于 v1 范围，见 `03-execution-capsule.md#§11.1`，本文不展
  开、不重复。
- 不重复定义 `01`–`06` 已权威定义的术语（本文只交叉引用）。
- **不实现外部 tracker sync（Jira/GitHub Issues 等双向同步）**：v1 期间该能力由并存的旧
  aihub 系统承担（§2.1）；现行 `/pf-sync` 入口的处置（不迁移为 skill）仍以
  `00-architecture.md#§11.1` 迁移矩阵为准，本条裁定的是**能力范围**而非入口去向。
- **发版（release）= bundle 内 release step 钩子（定稿裁定；〔已被 2026-10-13 取代：release 槽位已从交付 bundle 删除，发版只经 `workflows/release/`，见 `01-workflow-skills.md`「发版独立成 bundle」〕）**：平台零发版专用代码；发版逻辑长在项目
  bundle 的 `agents/release.md`（模板随参考 bundle 交付，`01-workflow-skills.md#§4.2`），多项目天然隔离。
  平台自身发版（`aihub` 服务二进制与工作机二进制二产物）是同一钩子的实例，不另立发版模型。
  **`pf upgrade` 是部署管道而非发版系统**：只负责下载已发布制品、校验 sha256、quiesce、替换二进制、拉起
  （§6.3），不承担任何发版决策；制品 = v1 内网 `releases/` 目录按 tag 存二产物 + 校验清单（mono-repo 单一 semver，
  `00-architecture.md#§3.3`）。一期部署面（§3/§6）不含发版 step，发版由各项目 bundle 自带。
- **发布凭据两分（polyforge#123 合同声明，publish/distribution 面）**：release 生命周期涉及两个**彼此独立**的凭据——
  (1) **git tag-push credential**（把发版 tag 推到项目 git remote）；(2) **shelf/publish write credential**（向发布架
  `releases/` 目录写入制品）。二者不得混用、不得假设同源；各自的存储与授予机制（哪台机器、哪个身份、哪个最小
  权限面）是刻意留白的后续设计——P2 子票裁定，本层只定"两凭据分离"这一合同。本项目自身的两个 procedure
  （repo 内 `.polyforge/release.sh` 发版、`.polyforge/deploy.sh` 部署）即这两类凭据的消费方，模板与 dispatcher
  对其内容不做任何假设（`01-workflow-skills.md#§4.2` 的 dispatcher 裁定）。
- **deploy face 永远人门控（polyforge#123；〔已被 2026-10-13 取代：deploy 现由 release bundle 的 `deploy` step 触发，防线为 `--confirm <version>`、服务器侧锁、失败自动回滚，见 `01-workflow-skills.md`〕）**：`.polyforge/deploy.sh|.md` 是项目 repo 内的部署 procedure
  （aihub 升级 + 每台工作机 `pf upgrade` 序列，含回滚），**只由人触发**——任何自动化路径（workflow step、
  daemon lane、release procedure）不得引用或触发它；本部署面不为其开设任何 CLI verb，§6 的升级序列由人在
  各机器上手动执行。deploy 不是发版（release 产出制品，deploy 只把已发布版本装上机器），二者凭据亦分离
  （见上一条）。

## 2. 术语与不变量（含一期部署形态） <!-- §2 -->

### 2.1 一期部署形态（权威） <!-- §2.1 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（持久 WI Agent、CAP-* 码、Revision/CAS/manifest、Interactive/Batch/Drain）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

单主机运行 `aihub` 服务进程（单 active Service，INV-2，`02-aihub-core.md#§2.2`）；Core 是单个 SQLite 文件 +
WAL，**不保存任何 workflow 执行状态**（INV-1）。远端数据——Capsule Revision 的 CAS 存储、Memory
权威 Markdown、最终结果归档——全部落在本机目录 `<data_dir>/`（布局权威在 `02-aihub-core.md#§4.11`；本文 §4 给出
部署视角的摘要树）。每台工作机运行一个 Daemon 进程（工作机二进制的 `daemon` 子命令，部署单元合并裁定 `00-architecture.md#§3.3`）：Daemon 是
Interactive / Batch / Drain 三种 WI Job 执行模式下唯一的执行者（`00-architecture.md#§3.4`），为每个正在执行的 WI
创建/回收 Capsule 并派生并监督其**唯一执行权威**——`runtime-state/execution.json`（`03-execution-capsule.md#§4.2`）——
的**唯一写者**：该 WI 的 Harness RPC WI Agent（`00-architecture.md#§3.6`）。机器与 AIHub 之间仅经 HTTP 通信
（Core API `02-aihub-core.md#§5.1`、Revision 发布协议 `05-sync-revision-recovery.md#§5.1`）。机器与机器之间不直接
通信：Daemon 的操作面仅本机 Unix domain socket，**无任何内网监听端口**，Daemon 间零通信——不存在
Daemon-to-Daemon 协议或互推通道，一切跨机信息经 AIHub HTTP 中转（`00-architecture.md#§3.1`、
`04-local-runtime.md#§8`）；部署面因此不含任何 Daemon 内网端口的开票/防火墙配置步骤。

**运行时依赖清单（按主机角色区分，二者互不共享，也不因升级一方而暗示升级另一方）**：

- **AIHub 存储依赖（AIHub 服务主机）**：Go 二进制、SQLite（内嵌驱动）、本机文件系统。**不含**：
  PostgreSQL、JuiceFS、S3、消息队列、MCP 服务器/运行时——AIHub 的对外面只有 HTTP API 与
  `pf` CLI（MCP 删除，`00-architecture.md#§3.1`；现行系统的 MCP 服务器不随 vNext 部署，也不作为
  vNext 的运行依赖）——AIHub 服务主机本身是宿主机原生进程，v1 可信内部
  仓库边界下不引入 per-step 隔离（`00-architecture.md#§3.7`）。这份清单本身
  就是 INV-10 在部署侧的落地判据：任何部署步骤为 AIHub 服务主机引入清单之外的存储/中间件依赖，
  都是对该不变量的违反。
- **工作机宿主执行依赖（每台运行 Daemon 的工作机）**：工作机二进制（`daemon` 子命令长驻）；宿主机原生进程执行
  环境本身（v1 可信内部仓库边界内的宿主机原生执行，不引入 per-step 隔离，INV-12）；该 WI 选定的 harness 适配器所依赖的 harness 运行时——
  **Pi CLI**（`--mode rpc` 持久进程）或 **Claude Code CLI**（稳定 session + stream），二者是
  v1 仅有的两条 harness 适配器（`00-architecture.md#§3.6a`、`04-local-runtime.md#§6.1a`），须预先安装并完成其自身的登录/凭证
  配置，这一步在部署 CLI 面之外，由参与 Pilot 的用户或运维按各 harness 自己的安装说明完成；对使用 Claude Code CLI
  的工作机，该 harness 的**默认会话位置**（其会话 JSONL 的运行时落位，本机、Capsule 外）随之是一项本机运行要素——
  Daemon 只读访问它做运行中的会话数据读取与终态采集（`00-architecture.md#§3.6a`、`04-local-runtime.md#§6.1a`/`#§6.2`），
  具体路径由该 harness 自身约定，部署面不物化、不迁移、不清理；
  该仓库任务实际需要的宿主工具链（如 git、目标语言的构建/测试工具链），由 repo 自身决定，vNext
  不声明、不物化、不做工具链闭包（`03-execution-capsule.md#§1.2`/§3.2）——工具链缺失时的失败路径权威在 `03-execution-capsule.md#§5.1`
  `CAP-401`（R11，§10.2）；对运行 Drain/Batch 的工作机，其机器用户还须是对应 project 的 writer
  成员——membership 不随机器注册自动授予（`machines register` 只建身份），缺失时 ready-queue 查询
  折叠为 `CORE-404`、Daemon 静默空转（安装步骤见 §6.1 步 3）；以及与 AIHub 的 HTTP 网络可达性。**对任务仓 origin 的 git push 可达与推送权限**（基线修订 polyforge#37 所加）：`ship` 交付桥 (removed by polyforge#166: delivery is the workflows' deliver step)（`04-local-runtime.md#§5.1`）在本机把已收尾 WI 的 `polyforge/<project>-<seq>` 固定名分支推送到任务仓 origin，复用工作机既有 git 身份（SSH/credential helper），不新增任何凭证存储；`ship` 成为第 13 个用户动词（`00-architecture.md#§5.1`）后，仅有 clone 拉取权限的工作机不再充分——push 权限与可达性自此属于本清单的一部分。这份清单是 INV-12 在工作机侧的落地
  判据，与上一条 AIHub 存储依赖清单是两份独立的清单，检查/升级一方不隐含对另一方的任何断言。

**新仓部署物与旧系统的并存（权威）**：vNext AIHub 是新 repo 实现的部署物
（`00-architecture.md#§3.3` 正式取代声明），与现行 aihub 系统（gen-1，含其 MCP 服务器与外部
tracker sync 等 v1 不含的能力）是两个互不共享代码与数据的独立系统。Pilot 通过（§9.3 全部门槛
未触发且 §11 达标判据满足）之前，二者**并存运行**：旧 aihub 系统继续作为生产权威，vNext AIHub
只服务 Pilot 范围（§9.1）；二者不共 `<data_dir>`、不共容器/服务名（§3 的 docker compose 服务 `aihub`）、
不共监听端口，之间**不迁移执行状态**（`00-architecture.md#§1.2`：workflow-v2 执行状态不迁移），
也不存在任何执行状态迁移或同步通道；**Memory 存量是唯一迁移项**（`06-memory-search.md#§10`：
现有 aihub memory 一次性导出为 Markdown 权威文件——非执行状态，也不构成两系统间的同步通道）。切换在 Pilot 达标后一次性发生（§11）：新工作一律开在 vNext AIHub，旧系统进入冻结/退役
流程；切换前旧系统不做任何「部分下线」。

### 2.2 本文新增不变量 <!-- §2.2 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（持久 WI Agent、RunCommand）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

| 编号 | 不变式 |
|---|---|
| INV-D1 | 升级期间**没有**双写窗：旧 aihub 进程必须先停稳、释放 `02-aihub-core.md#§2.2` 的 `service.lock`，新进程才允许启动。 |
| INV-D2 | 删除只经 Core API（`05-sync-revision-recovery.md#§2.10` 的状态门 + 确认门 + 24h tombstone 三重防护，签名与错误码见 `05-sync-revision-recovery.md#§5.1`/§5.2）；**不存在**文件系统级清扫入口——任何「直接删除 `<data_dir>` 或工作机 Capsule 目录」的操作在 vNext 部署面上无对应端点。 |
| INV-D3 | Pilot/监控口径中任何「某条命令确实被执行」的判据，其来源只能是 Capsule Native runner 在 `RunCommand`（`03-execution-capsule.md#§5.1`）调用返回给 WI Agent 的 `Result{exit_code, stdout, stderr, killed, duration_ms}`，并进一步体现为 WI Agent 据此写入 `runtime-state/execution.json`（`03-execution-capsule.md#§4.2`）的 `steps.<id>.output`/`attempts` 内容；**不得**采信 Capsule `logs/` 目录的内容作为该证据——`logs/` 对 harness-native subagent 是可追加写权限（`03-execution-capsule.md#§5.2`），subagent 是被观察对象，不能同时充当自己是否被正确执行的证人（§9.2a 展开）。 |
| INV-D4 | Pilot 否决门槛判定「某 step 是否产出了它承诺的 artifact」时，判据是该 step 对应仓库相对**该 step 自己的起始基线**（`03-execution-capsule.md#§4.2` `steps.<id>.step_start_refs`，**不是** §4.1/§4.3 的 WI 级认领基线（`capsule.json` 的 `repos`））的实际工作树 diff、`artifacts/` 目录下的新增产物，或 `runtime-state/execution.json` 中该 step 的 `output` 字段（`03-execution-capsule.md#§4.2`）是否非空——与该 step 调用了多少次 `RunCommand` **无关**：一个 step 完全可能只通过文件写工具（Edit/Write 类）合法完成全部工作而不触发任何 `RunCommand` 调用（§9.2a 展开）。使用 step 自己的起始基线而非 WI 级基线，是为了区分“本 step 自己产出了什么”与“前面 step 留下的改动”：后者对 WI 级基线而言也是非空 diff，会让未实际产出的后续 step 逃过本检查。当前架构未定义任何「声明改动文件清单 vs 实际 diff」的机械全等校验字段，本判据不得假设存在这样一份声明清单。 |
| INV-D5 | 任何以「样本占比超过阈值」形式表达的否决门槛，必须同时给出：统计窗口（Pilot 累计周期或滚动周期）与分母的精确定义（哪一类事件、取自哪个时间范围）；只给比例不给窗口/分母的表述在本文视为未完成定义，不得进入否决门槛表（§9.3 逐条落实）。 |

### 2.3 部署单元与 WI Agent 的关系 <!-- §2.3 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（持久 WI Agent、Interactive/Batch/Drain）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

WI Agent（`00-architecture.md#§3.6`）**不是**独立安装的部署单元：它是 Daemon 在创建 Job 时按需派生的每 WI
一个的宿主机原生子进程，运行的是与 Daemon 同一份分发物中的驱动代码，不单独打包、不单独安装、不
单独作为独立的进程管理单元（tmux 监督环、systemd 单元或 launchd job）存在。部署单元清单（§3）只列出需要独立安装/升级/回滚的物件；WI Agent 的
生命周期完全由其所属 Daemon 管理（`04-local-runtime.md#§6.1`/§7.1），升级 Daemon 二进制即升级了 WI Agent 会
使用的驱动代码版本。

无论 Job 来自哪种模式（Interactive submit/observe/answer、人工确认后的 Batch wave、还是 Drain
向 AIHub 调度器（就绪队列）自主取用的 wave，`00-architecture.md#§3.4a`/b/c），Daemon 派生的都是**同一份** WI Agent
实现，WI Agent 内部运行的都是同一份 `pf-execute` 主循环（`00-architecture.md#§3.6`）——三种模式只影响 Job 的
来源与准入判据，不产生三套不同的执行部署形态。

**用户机器部署物 = 工作机二进制 + 路由 skill 引用（安装面裁定的部署侧落地）**：一台用户工作机的全部
vNext 部署物是工作机二进制（单文件：CLI 动词 + `daemon` 子命令；其发行物内含 `skills/pf-execute/`，见下段）加路由 skill 的一个
引用（数据面动词经 CLI 直连 AIHub，分流表
`00-architecture.md#§5.1`）——两个 harness 都**引用** `tools/install.sh` 维护的同一份发行检出
`~/.local/share/polyforge/release/current`（零复制，不二次克隆，polyforge#174）：**Pi 侧**是 settings
`packages` 中一条指向该目录的本地路径包（仓根 `package.json` 声明路由 skill 与 `pf-chat` 扩展——只写
一行 `skills` 路径会丢掉扩展）；**Claude Code 侧** `~/.claude/skills/polyforge` 是指向
`release/current/skills/polyforge` 的软链，外加一次注册的 SessionStart 钩子
（`00-architecture.md#§3.1` 安装面裁定、`01-workflow-skills.md#§2.1`）。用户 harness **永不执行 workflow**：
Interactive 模式下用户只提交意图、观察进度、回答 needs_input，workflow 的解析与执行全部发生在
Daemon 派生的 WI Agent 一侧（`00-architecture.md#§3.4a`）。

**任何 harness 不安装 executor（S9 部署侧落地）**：`pf-execute` 不是部署单元，也不出现在任何 harness
的安装面上——它由 Daemon 从自身 checkout/发行物（`skills/pf-execute/SKILL.md`）注入给其派生的每个
WI Agent（`04-local-runtime.md#§6.1` 第 8/9 步、§6.1a），注入时机在派生时；版本标识与 Daemon 发行版
绑定，升级 Daemon 即升级 executor。Pi/Claude 在 vNext 中仅作为宿主适配器（`00-architecture.md#§3.6a`），
不因参与者的 harness 自身更新而改变 executor 版本。

**旧 skill 处置不另立部署面（S12）**：现行 `/pf-*` skill 与散装 agent 定义的唯一处置权威是
`00-architecture.md#§11.1` 的迁移矩阵（spec/plan/retro→bundle agents；crystallize/revise 删除；
project/user/release/sync→`pf` CLI；init/doctor/update→doctor+ops；using-polyforge 并入路由背景；散装
agents 废弃）——部署面不部署、不迁移其中任何被删除或废弃的旧入口，下文部署单元清单（§3）与安装
序列（§6.1）均不含它们。

## 3. 模块与代码目录（部署单元） <!-- §3 -->

| 单元 | 产物 | 部署位置 | 独立升级/回滚 |
|---|---|---|---|
| `aihub` | AIHub 服务二进制（**新仓部署物**，`00-architecture.md#§3.3` 取代声明；与旧 aihub 系统并存到 Pilot 通过，切换见 §2.1/§11） | 单主机，docker compose 服务 `aihub`（`deploy/docker-compose.yml`，`restart: unless-stopped`：崩溃与主机重启均由 docker 拉起；镜像只由 `.polyforge/deploy.sh` 拉取/加载，compose 从不构建） | 是（§6.3/§6.4，经 `.polyforge/deploy.sh`：在线备份后秒级切换窗口，失败自动回滚） |
| 工作机二进制 | 单文件：`pf` CLI 动词 + `daemon` 子命令（部署单元合并裁定，`00-architecture.md#§3.3`；数据面动词直连 AIHub 与管理操作，分流表 `00-architecture.md#§5.1`） | 每台工作机：`/usr/local/bin/pf`，由 `tools/install.sh` 安装。`daemon` 子命令**只经 `pf daemon ensure` 拉起**（幂等 + 文件锁）：它在 tmux 会话 `polyforge-daemon` 中运行监督环（`pf daemon <规范 argv>`，非零退出 5s 后重启，干净退出即结束环）——**崩溃**约 5s 内被环重新拉起，`pf ops shutdown` 则连环一起停下；tmux 不跨容器/机器重启存活，**重启**后由下一个开始或恢复的 harness 会话（pi `pf-chat` 扩展、Claude Code SessionStart 钩子）执行 `pf daemon ensure` 拉起（§6.5）。有 systemd 的机器同样走 tmux 环（与容器一致，最小可行）；操作者若改用自管的 systemd 单元 `polyforge-daemon.service`，`pf doctor` 亦认作已监督。满足 `04-local-runtime.md#§7` 的「崩溃后可被重新拉起」 | 是，逐台滚动（§6.3⑤：持 ensure 锁→优雅停机（环随干净退出结束）→替换单文件与 `release/current`→释放锁→`pf daemon ensure`） |
| 路由 Skill | Skill 包（含 pi `pf-chat` 扩展与 Claude Code SessionStart 钩子两个适配层） | 各用户 Harness，引用工作机的 `release/current` 检出（§2.3） | 随仓 tag 版本（mono-repo 单一 semver，`00-architecture.md#§3.3`）：`tools/install.sh` 切换 `release/current` 即更新，下一个 harness 会话生效；pi 为本地路径包、cc 为软链，均零复制；不构成独立版本线 |
| bundle | `workflows/<bundle-name>/`：`workflow.md`（frontmatter + 正文）+ `steps/<id>.md`（`01-workflow-skills.md#§2.1`/§3） | 随 aihub 仓本机 checkout 交付：AIHub 服务主机（创建 WI 时服务端读取，`02-aihub-core.md#§4.5`）与每台工作机（WI Agent 第 0 步物化来源，`03-execution-capsule.md#§4.5`） | 否——无注册接口、无 registry/Core catalog（`01-workflow-skills.md#§2.1`）；随 checkout 的 git 版本演进，不构成独立部署/升级/回滚单元 |

执行 workflow 的 Go driver 运行在 Daemon 进程内，没有独立的部署位置或版本号（原 WI Agent 与
Interactive / Batch / Drain 模式均已删除）。

## 4. 数据结构：部署单元文件清单与目录布局 <!-- §4 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（Revision/CAS/manifest）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

```
主机侧（AIHub）：<data_dir>/           # 缺省 /var/lib/polyforge/aihub/
├── aihub.db (+ -wal / -shm)          # SQLite Core，完整 DDL 见 `02-aihub-core.md#§4`
├── service.lock                      # 单 active 判据，`02-aihub-core.md#§2.2`
├── memory/…                          # Memory 权威子树，`06-memory-search.md#§4.1`
├── capsules/<work_item_id>/rev/<seq>/…  # Revision CAS + manifest，`05-sync-revision-recovery.md#§4`
├── backups/<timestamp>/              # 一致性备份点，§6.2 产出
└── tmp/                              # 原子写中转（tmp+fsync+rename 模式）

工作机侧（Daemon + Capsule）——workspace 由共享配置 `~/.config/polyforge/cli.toml` 的
`[workspace."<path>"]` 映射唯一决定（polyforge#113；见 `08-capsule-base-migration.md#§1`）。不存在启动期
workspace 旗标或环境变量（旧 `-workspace` / `POLYFORGE_WORKSPACE` 已删除，传入即解析失败）；零个
`[workspace]` 节时 Daemon 拒绝启动：

<pf-workspace>/capsules/<project>-<seq>/…  # （polyforge#144；此前认领的为 <wi_id>）Capsule 布局权威，`03-execution-capsule.md#§3.1`；执行权威文件为
                                       # <capsule>/runtime-state/execution.json（`03-execution-capsule.md#§4.2`）
<data-dir>/…                          # Daemon 自身状态（`--data-dir`，缺省 ~/.local/share/polyforge）：daemon-id、
                                       # runtime-state.json、daemon-state.json、events 日志、daemon.log 等；不含任何
                                       # workflow 执行状态字段，与 Capsule 的 execution.json 严格区分。没有 credentials/
                                       # 目录——daemon 不管理凭证（v2-converge §0；harness 由人登录）。
<harness-root>/…                      # 缺省 <data-dir>：step-agent/、step-agent-sessions/、claude-config/（v2-converge §2.7）
```

`<data_dir>` 是 AIHub 唯一持久化根：删除或迁移 `<data_dir>` 等价于删除或迁移全部 Core 数据、
全部 Revision 数据与全部 Memory 权威内容；`backups/<timestamp>/` 下每个子目录是一次 §6.2 备份的
完整产物（SQLite 快照 + `memory/`/`capsules/` 目录快照的原子关联对），是 §6.4 回滚唯一合法的
恢复来源。

## 5. API 与错误码（部署 CLI 面） <!-- §5 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（SYNC-* 码、DEPLOY-* 码、PILOT-* 码、backupBarrier、pf upgrade）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

| 操作 | 语义 | 可能错误码 |
|---|---|---|
| `.polyforge/deploy.sh <ver> --confirm <ver>` | AIHub 服务主机：把已发布版本推上 docker 服务——在线备份（rsync + SQLite backup API 快照 + 追平 + 异机副本，服务不停）→ 秒级切换窗口（停旧容器→该版本 upgrade SQL→起新容器）→ 校验；切换后任一步失败自动回滚（§6.3/§6.4） | 退出码 10（已回滚）/ 11（回滚失败） |
| `pf backup` | M4 管理面：服务端 DB-only 快照（`backupBarrier` 写屏障下 `VACUUM INTO`，§6.2）；不含 `memory/` 与 git 仓——全量备份走 `deploy.sh` | `DEPLOY-507` |
| `tools/install.sh <api-key>` | 工作机装机/升级：二进制、配置、`release/<tag>` 检出与 harness 引用、`pf daemon ensure`（§6.1/§6.3⑤） | — |
| `pf daemon ensure` | 工作机 daemon 唯一拉起入口：socket 可达即退出 0；否则在 tmux 监督环中拉起并等待 socket（§6.5） | 退出码 1（未能拉起，附原因与日志尾） |
| `pf upgrade`（部署管道，非发版：下载已发布制品→校验 sha256→§6.3 升级序列） | §6.3 升级序列 | `DEPLOY-409 LOCK_HELD`（旧进程未停）/ `DEPLOY-507` |
| 回滚（`deploy.sh` 自动回滚；数据恢复按其打印的命令人工执行） | §6.4 回滚序列 | `DEPLOY-404 BACKUP_NOT_FOUND`（备份点不存在）/ `DEPLOY-409 DAEMON_STILL_LIVE`（仍有 Daemon 存活未确认） |
| `DELETE /api/v1/work-items/{id}/capsule-data?confirm=` | Capsule 远端数据删除入口；签名与错误码权威在 `05-sync-revision-recovery.md#§5.1`/§5.2（状态门/确认门定义在 `05-sync-revision-recovery.md#§2.10`），本表仅标注其经部署 CLI 也可触发（不重定义、不借用 `DEPLOY-*` 前缀） | `SYNC-409`（WI 非终态，`05-sync-revision-recovery.md#§5.2`）/ `SYNC-400`（`confirm` 参数不符，`05-sync-revision-recovery.md#§5.2`） |

**错误码表（前缀 `DEPLOY`）**：

| 码 | 语义 |
|---|---|
| `DEPLOY-404 BACKUP_NOT_FOUND` | `rollback` 指定的备份点不存在 |
| `DEPLOY-409 LOCK_HELD` | 升级时旧 aihub 进程的 `service.lock` 尚未释放（INV-D1），拒绝启动新进程 |
| `DEPLOY-409 DAEMON_STILL_LIVE` | 回滚时仍有 Daemon 未确认停止/断开（§6.4 步骤 1），拒绝回滚 |
| `DEPLOY-507 STORAGE_OR_MIGRATE_FAILED` | 建库/迁移/备份 I/O 失败 |

`capsule-data` 删除的确认/状态门错误码不在 `DEPLOY-*` 前缀下——它们是 `05` 的 `SYNC-400`/
`SYNC-409`（见上表交叉引用），避免同一语义在两个前缀下各定义一次。

`PILOT-*` 不是 HTTP 错误码，是 §9.3 否决门槛的编号标识（`PILOT-V1`…`PILOT-V8`），用于 Pilot
报告与评估脚本互相引用同一判据，不构成任何 API 的返回值。

## 6. 核心流程：安装 / 升级 / 回滚（编号步骤） <!-- §6 -->

### 6.1 安装 <!-- §6.1 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（持久 WI Agent、Interactive/Batch/Drain）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

1. 在主机创建 `<data_dir>`（权限 `0750`，属主为服务运行用户）。
2. 部署 `aihub`：以 docker compose 服务 `aihub` 运行已发布镜像（`deploy/docker-compose.yml`，
   `restart: unless-stopped`，镜像由 `.polyforge/deploy.sh` 拉取/加载）：启动序列内部依次完成
   `service.lock` 获取 → schema 安装（`CREATE TABLE IF NOT EXISTS`；无迁移）→ 后台 memory 对账（启动期 `RebuildIndex`）。
3. 在每台工作机运行 `tools/install.sh <api-key>`：安装单一工作机二进制（`pf`，含 `daemon` 子命令），
   并经 `pf daemon ensure` 在监督下拉起 `daemon`（§3、§6.5）：tmux 会话 `polyforge-daemon` 中的监督环在
   崩溃后约 5s 内重新拉起；机器/容器重启后由下一个 harness 会话开始时的 `pf daemon ensure` 拉起——
   满足 `daemon` 子命令崩溃后能被重新拉起这一长驻语义（`04-local-runtime.md#§7`）。有 systemd 的机器同样
   使用 tmux 环；操作者自管的 systemd 单元 `polyforge-daemon.service` 被 `pf doctor` 认作已监督，v1 不进一步规定其
   选择。随后执行注册流程，把机器身份写入 Core 的 `machines` 表（`02-aihub-core.md#§5.1`、`02-aihub-core.md#§4.10`），并把
   该机器的初始 Job 执行模式（Interactive/Batch/Drain，`00-architecture.md#§3.4`）写入
   `$POLYFORGE_HOME/daemon/state.json` 的 `mode` 字段（`04-local-runtime.md#§4.1`/§6.7）。对运行
   Drain/Batch 的机器，还须把其机器用户加入对应 project 作为 writer 成员（membership 经 `pf` 管理
   CLI 的 projects/members 端点授予，`02-aihub-core.md#§5.1` 管理面 M3）——`machines register` 只创建
   机器身份，不授予任何 project membership；缺此步时机器用户非项目成员，Drain/Batch 的 ready-queue
   查询按不存在处理折叠为 `CORE-404`（membership 硬边界 `02-aihub-core.md#§4.10`、404 折叠规则
   `02-aihub-core.md#§5.1b` 尾注），Daemon 因此静默空转、不取 wave。
4. 确认参考 bundle 就位：bundle **没有**注册步骤、注册接口或「已注册 bundle 列表」
   （`01-workflow-skills.md#§2.1`/§4.6）——在 AIHub 服务主机与各工作机确认 aihub checkout 内
   `workflows/<bundle-name>/`（`workflow.md` + `agents/*.md`）存在即可；创建 WI 时由 AIHub 服务端从
   本机 checkout 读取并固化三 scalar（`02-aihub-core.md#§4.5`），执行侧由 WI Agent 第 0 步从工作机本机
   checkout 物化（不做 digest 校验——校验链已删，polyforge#149 B9；`03-execution-capsule.md#§4.5`）。
5. 把路由 skill 安排到参与 Pilot 的用户 harness——用户机器的部署物到此为止只有工作机二进制与路由
   skill 引用（§2.3），由第 3 步的 `tools/install.sh` 一并完成：Pi 侧在 settings `packages` 写一条指向
   `release/current` 的本地路径包（替换旧的 `git:...@<ver>` 条目）；Claude Code 侧把
   `~/.claude/skills/polyforge` 设为指向 `release/current/skills/polyforge` 的软链并注册 SessionStart 钩子
   （`00-architecture.md#§3.1`）。`pf doctor` 对偏离该布局或残留 v1 polyforge 物件的 harness 报 warn 并给出
   先备份再清理的命令。任何 harness 不安装 `pf-execute` 或任何 bundle/step skill（§2.3）。路由
   skill 是 Interactive 模式的唯一入口（`00-architecture.md#§3.4a`）。
6. 安装完成的判据：`pf doctor` 对 AIHub 业务端点（如 `GET /api/v1/work-items`）的连通性自检通过，且响应头报告的 `schema_version` 为当前 v1 版本（无独立 whoami 端点，`02-aihub-core.md#§5.1`）；
   至少一台工作机的 Daemon 心跳已出现在 `machines` 表中，且其 `mode` 已成功写入 Daemon state。装机自检另含一次
   **候选 liveness 探针**（polyforge#27）：对参与 Pilot 的 bundle（第 4 步确认就位的 `workflows/<bundle-name>/`
   全集）所声明的全部 model_candidates（defaults 与 step 级覆盖的并集，`01-workflow-skills.md#§2.1`/
   `01-workflow-skills.md#§4.1`），经对应 harness 的模型调用路径各发一次微型探针请求，全部可达方判安装完成、方入
   Pilot；探针属安装面判据，不是运行时依赖——Pilot 期间候选不可达仍按 `01-workflow-skills.md#§6.3` 的基础设施
   失败原地 resume 语义处理，装机探针不为其兜底，也不因探针曾通过而改变该语义。

### 6.2 一致性备份（`pf backup` 与 `.polyforge/deploy.sh` 在线备份，升级/回滚均以此为前置步骤） <!-- §6.2 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（DEPLOY-* 码、Revision/CAS/manifest、backupBarrier）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

现行实现：升级前的全量备份是 `.polyforge/deploy.sh` 第 3–5 步（服务不停：rsync `memory/` 与 `git/`
→ SQLite backup API 快照 `aihub.db` → rsync 追平 → 快照完整性与计数校验 → 异机副本）；`pf backup`
（M4，admin）只写服务端 DB-only 快照。下文的写屏障是 aihub 进程内 `backupBarrier` 的语义。

一致性备份要求 SQLite 快照与 `capsules/`/`memory/` 目录快照对应**同一个「无在飞写入」时间点**，
否则两者之间可能夹进一次已提交的 Revision 发布而不同点。实现方式是 aihub 进程内一个专用的写
屏障（与 `02-aihub-core.md#§2.2` 的 `service.lock` 是两个不同机制：`service.lock` 只防第二个进程启动，同一
进程对自己重复取锁不阻塞自身的写请求处理，因此不能复用来做备份屏障）：

1. aihub 进程内维护一个进程级读写锁 `backupBarrier`：每个 Core 写事务（`02` 的写 API、`05-sync-revision-recovery.md#§5.1`
   的 block/manifest 发布，二者同属一个 aihub 进程）在事务开始前持有该锁的读态，事务结束后释放。
2. 备份调用（`pf backup` 的管理端点）仅接受本机（`localhost`）来源的内部管理端点（AIHub 单端口 A5
   上的管理路径，`00-architecture.md#§3.1`；以来源限制而非第二监听端口实现本机专属），请求该锁的写态：
   写态请求会阻塞直到
   全部已发起的读态持有者完成并释放，同时暂停新的读态请求进入（即「排空在飞写入」）。
3. 写态到手后依次执行：SQLite backup API 热备；对 `<data_dir>/memory` 与 `<data_dir>/capsules`
   做目录快照（优先使用硬链接以缩短持锁时间，实际数据搬迁在锁外异步完成）。
4. 两份产物写入同一个新目录 `<data_dir>/backups/<timestamp>/`；`timestamp` 即该备份点的 ID。
5. 释放写态锁，恢复正常写事务处理。
6. 持锁窗口的设计目标是亚秒级（使用硬链接快照，不等待数据搬迁完成）；该窗口是本流程中 AIHub
   唯一短暂停止接受新写事务的时刻，升级/回滚流程分别在 §6.3 步骤 1、§6.4 步骤 2 调用本流程一次。
   `Memory` 域的**三**类权威文件写路径（create/PATCH/redact，`06-memory-search.md#§4.6`）同样先获取该锁的读态
   ——且获取时机在**开始写权威文件之前**、保持到**相关 SQLite 元数据提交完成**（含 HTML 派生）
   为止，与 Core 写事务共用同一把锁，不是第二套备份屏障机制。
7. **同一屏障一次只取一把读锁（权威，修复 B15，消除死锁）**：任一涉及 `backupBarrier` 的操作
   （Core 写事务，或 `06-memory-search.md#§4.6` 的三类 Memory 写路径）对同一把锁**恰好获取一次读态**，在其端到端
   窗口（权威文件写入 → SQLite 提交 → 派生完成）内持续持有到释放；操作内部嵌套调用的 Core
   事务层**复用外层已持有的读态**，**MUST NOT** 自己再次请求同一把锁——`backupBarrier` 是非可重入
   读写锁，且写态请求排队时新的读态请求会阻塞：若内层在备份已排队写态之后再次 `RLock`，该请求被
   写态等待者挡住；外层要等内层返回才能释放它已持有的读锁；备份又在等那把读锁释放——三方互等
   成死锁，备份与全部写入永久停摆。事务层通过参数接收「已持有 `backupBarrier` 读态」的上下文
   （或完全不接触该锁，把锁职责完全上交给各 API 入口层）；`06-memory-search.md#§4.6` 是 Memory 侧对同一条规则的
   落地，两文定义一致，不存在第二套获取时序。

**与权威需求「维护窗口备份」的关系（修复 B21）**：上述亚秒级持锁窗口是**实现级**的写阻塞
时长（写事务被暂停多久），不等于权威需求的「维护窗口」——后者是**运维级**的准入条件：本文
裁定备份 **MUST** 只在声明的维护窗口内执行（缺省每日 02:00–05:00 本地时间，可配置）；
窗口外调用备份需显式 `--force-outside-window` 旗并在输出中告警（不拒绝，但留下
审计痕迹）。升级（§6.3）/回滚（§6.4）的**准入条件**之一是“当前处于维护窗口内，或操作者已显式
确认接受窗口外风险”，不在窗口内且未确认则拒绝为 `DEPLOY-412 OUTSIDE_MAINTENANCE_WINDOW`。这一准入
条件獨立于上述亚秒级持锁实现细节，两者不相互替代。

### 6.3 升级 <!-- §6.3 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（持久 WI Agent、owner generation、DEPLOY-* 码、Revision/CAS/manifest、Interactive/Batch/Drain）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

1. 执行一次 §6.2 一致性备份（必做，不可跳过）。
2. 经 `.polyforge/deploy.sh` 的切换窗口停止旧 `aihub` 容器（优雅排空）；确认 `service.lock` 已释放（INV-D1）——若检测到锁未释放
   （例如停止超时），中止升级并返回 `DEPLOY-409 LOCK_HELD`。
3. 替换 `aihub` 二进制；以迁移模式启动一次，执行 schema 迁移（`02-aihub-core.md#§11`，`schema_version` 单调
   递增，迁移事务原子）。
4. 以新镜像启动 `aihub` 容器（`compose up -d --no-deps aihub`）；执行起服自检（业务端点响应头返回新版本号且自注册成功，无独立 whoami 端点）。
5. 逐台滚动升级工作机的单一工作机二进制；Core API 协议向后兼容一个版本窗，允许旧版本 Daemon
   与新版本 AIHub 短暂共存直到该台完成升级。升级某台工作机前，先让其完成一次优雅停机
   （`00-architecture.md#§3.5`、`04-local-runtime.md#§6.5`）：该动作代替所有仍在进行的 WI Agent 各触发一次全扫描发布，保证
   替换二进制前没有正在飞行的执行现场。工作机侧由 `tools/install.sh` 按固定顺序执行：**持有 `<data-dir>/ensure.lock`**（期间 harness 会话的
   ensure 只等待、不拉起任何东西）→ **优雅停 daemon**（`pf ops shutdown`；干净退出同时结束监督环；等待
   daemon 与环都真正消失，残留的空环直接关掉，避免旧二进制被环重新拉起或与新 daemon 抢 socket）→ 替换单一工作机二进制与 `release/current` → **`pf daemon ensure`**
   拉起新环与新 daemon（拉起前释放锁）。新 Daemon 启动后**不**按
   `04-local-runtime.md#§7.1` 的崩溃恢复规则处理：升级前的优雅停机已按 `04-local-runtime.md#§6.2` A 路径结束该 WI 当时的
   generation（并从 `jobs[]` 中移除对应记录），因此重启后的 Daemon 看不到一个仍在“未结束”的
   旧 Job 可供“识别并续跑”；待升级完成后，该 WI 需经 Interactive/Batch/Drain 三种模式之一的
   正常入口重新发起一次 claim/resume（`04-local-runtime.md#§6.1`），开启**新 generation** 并按新 generation
   分配**全新**的 harness session id，从最近发布的 Revision 继续执行——**不得跨机，也不得同机**
   复用升级/优雅停机前的原 session；`04-local-runtime.md#§7.1` 的 pid 复用防护与 session 复用只适用于 Daemon
   自身意外崩溃重启、该 WI 仍在同一 generation 下未结束的情形，不适用于本步骤描述的主动优雅
   停机升级场景。
6. 升级完成判据：全部工作机 Daemon 版本号一致且心跳正常；升级窗口内没有出现 §7 所列任何一种
   异常收敛失败。

### 6.4 回滚 <!-- §6.4 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（持久 WI Agent、owner generation、HMAC 令牌、DEPLOY-* 码、Revision/CAS/manifest、takeover、rotate-secret）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

1. 确认回滚安全前置条件：查询机器身份表，全部机器的最近心跳时间距当前超过 5 分钟，或操作者已
   手动确认接受跨过该检查；若存在近期心跳且未手动确认，中止回滚并返回
   `DEPLOY-409 DAEMON_STILL_LIVE`。该检查存在的原因：回滚会把 owner generation 记录回退到备份
   点时刻的状态；若仍有 Daemon 持有回滚之后才签发的、更新 generation 的 capability token
   （`02-aihub-core.md#§5.1`），该 token 的 HMAC 校验依赖 `service_secret`，若 `service_secret` 本身未变，旧
   token 在回滚后的旧 generation 上会被重新判定为合法——即「旧凭证复活」。本步骤把这类风险收敛
   到显式确认。
2. 停止 `aihub` 容器（旧进程停稳，配合步骤 1 对无 Daemon 存活的确认）；用指定 `<point>` 的
   备份数据恢复 `<data_dir>`（同一个 §6.2 产出的 SQLite 快照 + 目录快照对，保证两者一致）。本步
   与下一步的顺序是裁定后的固定序列：**先恢复备份、再起 AIHub**——不存在任何「在停服期间调
   HTTP 作废凭证」的步骤。
3. 用回滚前的旧版本镜像启动 `aihub` 容器（`deploy.sh` 自动回滚即此步）；其**启动自检自动触发** `rotate-secret` 且
   **`mode="rollback"`**（`02-aihub-core.md#§6.6` 已有启动触发语义——行 M5 的触发方本就包含
   「启动自检自动触发」，本序列不另设停服期 HTTP 调用；**MUST NOT** 使用缺省的 `mode="grace"`；
   `grace_seconds` 强制视为 0，非零值被 `CORE-400` 拒绝）：新 epoch 之前的全部未 `retired_at` 旧 epoch
   在同一事务内标记 `retired_at=now()` 并删除对应 `service.secret.<epoch>` 文件（**epoch 作废**，
   与 `02-aihub-core.md#§6.6` 逐字一致）——回滚前签发的全部 capability token 因此在该次作废完成后
   **立即**失效，无宽限期，从根源上防止步骤 1 描述的旧凭证复活风险，不依赖任何机器"自觉"
   停止使用旧 token。
4. 工作机侧现场重建（`04-local-runtime.md#§7.3` 回滚专用路径，**MUST NOT** 调用 `02-aihub-core.md#§6.6` 的
   普通 `reauthorize`——后者仅适用于 `mode="grace"` 宽限期内的重新签发）：仍在运行的 Daemon 因其派生的
   WI Agent 持有的 token 已被第 3 步立即作废，其下一次写请求会收到 `02-aihub-core.md#§5.3` 定义的
   凭证失效响应——**rollback 作废专用的 `reason=secret_rolled_back`**（区别于 `mode="grace"` 轮换宽限期
   过后的 `reason=secret_rotated_expired`）；Daemon 识别到这是回滚作废而非常规轮换后，**停止其管理的
   WI Agent 与 subagent 进程组、本地 quarantine 当前现场**（不保留、不尝试继续使用），并**不自动
   对该 WI 发起任何重新 claim**。**备份恢复出的 active generation 行由 force-takeover 作为唯一仲裁路径**
   （与 `02-aihub-core.md#§6.6` 对齐）：这些 generation 的原持有方 token 已被立即作废、无法再调用
   `end-generation` 收尾，行在恢复后的 Core 侧保持活跃；唯一能结束它的是人工（admin）经
   `02-aihub-core.md#§5.1` 行 8 的 force-takeover——takeover 调用本身即结束旧代并开启新 generation、
   在响应中返回新代凭证；恢复机器消费该凭证直接创建新现场（`04-local-runtime.md#§7.3` 恢复序），
   不再发起第二次 claim（不复用回滚前现场）——回滚后不存在"用旧 token 继续工作"、"普通宽限重新授权"或"Daemon 自动重新
   claim"的路径。备份点中无活跃 generation 的 WI（`queued`/`paused`/`blocked` 等）的 claim 不受本次
   轮换影响（`02-aihub-core.md#§6.6`：claim 属于无活跃 generation 的类别 2 操作，凭合法 api_key 即可，
   轮换不得拒绝它）。
5. **可测试恢复验收（修复 B21：不仅校验身份，验证数据完整性）**，下列四项**均须通过**才算回滚完成，
   任一不通过均中止回滚并告警（`DEPLOY-413 RESTORE_VERIFY_FAILED`，`detail` 标明哪一项失败）：
   a. **SQLite 完整性**：对恢复后的 `<data_dir>/aihub.db` 执行 `PRAGMA integrity_check`，预期返回
      `ok`；
   b. **Revision 链/CAS 摘要**：对至少一个活跃 WI，沿其 `capsule_revisions` 链从 HEAD 回溯到 seq=0，
      逐级验证每一行的 `parent` 指向上一行的 `seq`（`05-sync-revision-recovery.md#§2.6` 引导规则），且每一行的 `manifest_sha`
      与对应 `<data_dir>/capsules/` 下实际 staging/manifest 字节重算一致；
   c. **Memory 文件与元数据一致性**：对恢复后的每一条 `memories` 行，现读对应 `memory.md` 字节计算
      sha256，与 `content_sha` 列比对（`06` INV-7c）；任一不一致即视为本项失败（不自动对账，
      回滚验收阶段只报告不修复）；
   d. 业务端点响应头报告的 `schema_version`/自注册信息与备份点一致；旧版本 token 的任何写请求
      均被拒绝且响应体不透出可用于伪造新 token 的信息；工作机侧全部旧现场均已 quarantine。
   此验收流程 MUST 在非生产测试环境对每个备份点定期演习一次（与 §6.2 的日常备份频率独立，不要求
   每日演习），确保备份产物真能被恢复且恢复后数据自洽，而不是只验证备份文件能被拷贝。

### 6.5 容器部署（无 systemd）：daemon 的拉起分工 <!-- §6.5 -->

工作机常是容器（PID 1 为 sshd，无 systemd / cron / supervisord），容器重启后 tmux 不保留。daemon 的拉起
因此不碰容器启动流程（ENTRYPOINT / 宿主机），改为「使用时自愈」，唯一入口是 `pf daemon ensure`
（polyforge#174）：

1. **ensure 语义**：daemon socket 可达 ⇒ 什么都不做、退出 0；否则在 tmux 会话 `polyforge-daemon` 中
   启动监督环 `while :; do nohup pf daemon <规范 argv>; rc=$?; [ "$rc" -eq 0 ] && break; [ "$rc" -eq 78 ] && break; sleep 5; done`（规范 argv：`--config`、`-machine-id`
   取 `<data-dir>/daemon-id` 否则 `m-<短主机名>`、`-data-dir`、`-socket` 取配置 `daemon_socket`、
   `-workflows-dir <data-dir>/release/current/workflows`；harness 根目录取缺省 `<data-dir>`（#171）；auto 与
   并发上限沿用 daemon 自身持久设置；工作目录 `$HOME`，日志追加到 `<data-dir>/daemon.log`，超 10 MiB 轮转），
   等待 socket 可达后返回。`<data-dir>/ensure.lock` 文件锁防并发：pi-web 同时恢复多个会话时只拉起一个环、
   一个 daemon；拿不到锁的调用等待持锁者完成后按 socket 结果返回。无 `[workspace]` 映射或缺 workflows
   时拒绝拉起并说明原因。tmux 不可用时退回单次无监督启动并明确告警。
2. **崩溃**：非零退出（崩溃、panic、被杀）由监督环约 5s 内拉起新进程；干净退出（`pf ops shutdown`、
   SIGTERM——优雅停机序列）结束监督环，即 `pf ops shutdown` 仍是「真正停下」，再起用 `pf daemon ensure`。
   **退出码 78**（repo review 2026-10-16 B-14）：daemon 因只有操作员能修的原因拒绝启动（配置、daemon-id、
   workspace、磁盘状态、重启对账失败）——监督环同样结束，不再每 5s 重启进同一个失败；原因在
   `<data-dir>/daemon.log`，修好后 `pf daemon ensure`（下一个 harness 会话开始时也会执行）。
3. **容器 / 机器重启**：下一个开始或恢复的 harness 会话执行 `pf daemon ensure`——pi 的 `pf-chat` 扩展在
   `session_start` 的 `pf chat init` 失败时后台调用一次（pi-web 恢复会话会重放 `session_start`），随后沿用
   每 5s 的重试环；Claude Code 的 SessionStart 钩子（startup / resume / clear / compact 触发）先 ensure
   （15s 上限、20s 强杀）再 `pf chat init`，失败静默、不阻塞会话。
4. **边界**：只有人用的交互会话会调用 ensure。pi step 用 daemon 专属的 `PI_CODING_AGENT_DIR`（不加载用户
   包与 `pf-chat`），claude step 以 `--bare` 运行（不跑钩子），二者环境均带 `PF_WI_ID`，适配层见之即退出。
5. **代价（接受）**：重启后到第一个 harness 会话开始前 daemon 不在，自动领取不工作。
6. **升级停机顺序**：`tools/install.sh` 持 ensure 锁 → 优雅停 daemon（环随干净退出结束；残留空环用
   `tmux kill-session -t =polyforge-daemon` 关掉，环内 daemon 以 `nohup` 运行、不受挂断影响）→ 替换二进制与
   `release/current` → 释放锁 → `pf daemon ensure`（§6.3 第 5 步）。
7. **自检**：`pf doctor` 的 `daemon socket` 在 daemon 不在线时 fail 并给出 `pf daemon ensure`；`daemon
   supervision` 在 daemon 在线但无 tmux 环（亦无 systemd 单元 `polyforge-daemon.service`）时 warn，给出修复
   命令。路由 skill 的兜底：执行面动词报 daemon 不可达时先 `pf daemon ensure` 再重试一次。

AIHub 服务主机不在此列：它是 docker compose 服务（`restart: unless-stopped`），崩溃与主机重启均由
docker 拉起，升级只在 `.polyforge/deploy.sh` 的秒级切换窗口中断服务。

## 7. 并发与崩溃恢复（升级窗口） <!-- §7 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（持久 WI Agent、Revision/CAS/manifest、watcher dirty 位）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

- **升级窗内 Core 不可用**：WI Agent 一侧的 Revision 发布请求失败后按 `05-sync-revision-recovery.md#§6` 的六步流程原地
  重试（幂等，`05-sync-revision-recovery.md#§2.8`）；watcher 标记的 dirty 状态在此期间只是持续累积，不丢失（`00-architecture.md#§4.3`：
  watcher 只标记不发布）。升级完成、AIHub 恢复可用后，`00-architecture.md#§4.3` 定义的四类触发时机之一（通常是
  各 WI 当前 step 的下一次完成/中断）驱动下一次全扫描发布，一次性收敛窗口内累积的全部改动——
  这**仍是** `05-sync-revision-recovery.md#§2.4` 定义的唯一发布路径本身在做的事情，不引入任何按周期或按静默窗口自主触发
  的第二条路径（`05` SYNC-INV-1）。**升级不丢 Capsule 现场**：这是升级窗恢复的唯一断言
  （对应 §10.3 的机械复核，与回归 R 类断言使用同一套「模型审计 + 故障注入」方法）。
- **升级迁移崩溃**：`aihub migrate`（§6.3 步骤 3）在事务中途被杀——schema 迁移事务原子
  （`02-aihub-core.md#§11`），中断即回滚到迁移前状态；此时判据是数据库仍处于迁移前 schema，不存在"半迁移"
  schema 落地的情况。若迁移前状态本身已不可用，唯一恢复路径是按 `deploy.sh` 打印的回滚命令恢复到最近一次
  §6.2 备份点。
- **误起第二个 aihub 进程**：`02-aihub-core.md#§2.2` 的 `service.lock`（第二层，先于本文的写屏障存在）使后起
  进程立即以启动期锁拒绝退出，不进入任何写路径。
- **升级窗内新 claim/发布请求**：一律走正常的失败→退避重试路径（同第一条），不存在"升级期间
  的特殊 API 契约"。

## 8. 安全边界 <!-- §8 -->

- `<data_dir>` 权限 `0750`，属主为 AIHub 服务运行用户；`backups/` 目录继承同一权限（备份不降低
  数据敏感度）。
- 工作机 Capsule 目录属主为执行该 Daemon 的运行用户；不同工作机之间不共享该目录（`03-execution-capsule.md#§2.4`
  多机副本裁定：任一时刻一个 WI 至多一个活跃 Capsule 实例）。
- 一期机器间通信为内网直连 HTTP，不做额外传输层加密（信任面权威在 `00-architecture.md#§8`）；但该
  HTTP 只存在于「工作机 → AIHub」这一个方向——Daemon 对外**无任何内网监听端口**（操作面仅本机
  Unix domain socket），Daemon 间零通信，跨机信息一律经 AIHub 中转（§2.1、`04-local-runtime.md#§8`）；管理操作面
  （升级、回滚、删除）限管理员角色发起 + 仅限本机 CLI 调用（§6.2 的备份屏障管理端点只接受
  `localhost` 来源，不对外网暴露；与 A5 单端口裁定一致，`00-architecture.md#§3.1`）。
- 删除保护（`05-sync-revision-recovery.md#§2.10`：状态门 + 确认门 + 24h tombstone）是本文安全边界的组成部分（INV-D2 的
  操作面体现）：部署 CLI 不提供、也不允许绕过这三重门的文件系统级删除捷径。
- **威胁模型边界**：v1 的可信内部仓库边界（宿主机原生执行）权威定义在 `00-architecture.md#§3.7`/INV-12，部署面
  承接该结论，不重复枚举排除了哪些具体隔离机制；Linux 与 macOS 在这一边界内是**同一执行
  形态**，不需要分平台的隔离配置或对齐验证步骤。面向不可信第三方代码的隔离化 untrusted
  runner 是独立演进方向，不属于 v1 范围，见 `03-execution-capsule.md#§11.1`，本文不重复列举其细节。

## 9. 监控与 Pilot：范围、指标与否决门槛 <!-- §9 -->

### 9.1 Pilot 范围与参与面 <!-- §9.1 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（Interactive/Batch/Drain、read-through）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

- 范围：1 个 project、3 个内部仓库、4 名工程师（经路由 Skill 会话使用 **Interactive** 模式：
  submit/observe/answer，`00-architecture.md#§3.4a`）+ 2 台常驻工作机（分别运行 **Batch**——人显式确认 wave 后
  成批派发，`00-architecture.md#§3.4b`——与 **Drain**——反复向 AIHub 调度器（就绪队列）取用下一批可执行 WI 且无需逐条
  人工确认，`00-architecture.md#§3.4c`）+ 1 台 macOS 工作机（与 Linux 工作机运行同一份 Native runner 形态，
  `03-execution-capsule.md#§5.2`；不设独立的分平台验证阶段，Linux/macOS 在 v1 下没有第二种执行 profile 需要对齐）。
  三种模式与至少一次 Daemon 优雅停机（`00-architecture.md#§3.5`/`04-local-runtime.md#§6.5`）在观测窗口内均须被实际触发至少一次，
  否则对应模式/路径的回归断言（§10.2）视为未覆盖，不计入 Pilot 达标判定。
- 观测窗口：30 天，或累计 ≥ 40 张 wrapped WI，以先满足者为准（两者均为 Pilot 设计参数，见 §12
  第 1 条关于校准的说明）。
- 观测来源：全部经 Core API 与本节 §9.2 定义的机械口径读取，不引入任何人工抽查或主观判断步骤。
- **Pilot 启动前置验证（四项全部通过才进入观测窗口）**：
  ① **pi `agentScope="project"` 断言**：从 capsule 内派生 subagent，断言其解析到的 agent 定义来自
  capsule 内物化的 `.pi/agents/`，而非工作区级同名定义（防冲突三层：`agentScope="project"` +
  项目级覆盖 + 必建空目录，`03-execution-capsule.md#§4.5`/§10）；
  ② **claude `.claude/agents/` 非 git 根发现实测**：capsule 根本身不是 git 仓库根（git 根在各
  `repos/<name>/` 内），按 `03-execution-capsule.md#§10` 的待验证项实测 Claude 在该布局下的 agent 发现
  行为；若实测结论为「不做 git 根无法发现」，须在 claude 适配器层提供显式装载路径后才进入观测窗口
  （该项不得顺延到联调阶段之后）；
  ③ **13 verbs 冒烟**：对路由 skill `verbs/` 下的 13 个用户动词 fragment（`ship` (removed by polyforge#166: delivery is the workflows' deliver step) 为基线修订 polyforge#37 所加，需一个已 wrap 的 WI 与可用 push 凭证作前置）
  （`01-workflow-skills.md#§2.1`/§3）各执行一次端到端冒烟（用户意图 → 目标面：执行面动词打
  本机 Daemon、数据面动词经 `pf` CLI 直连 AIHub，分流表 `00-architecture.md#§5.1` → Core
  响应），断言意图映射覆盖面无缺漏；
  ④ **observe 两层最小验收**：对一台本机拥有该 WI Capsule 的执行中 WI，经
  `GET /jobs/{wi_id}/observe?after=<cursor>` 增量拉取，断言事件在各 Job 内单调编号、
  `next_cursor` 严格推进（第一层，`04-local-runtime.md#§5.1a`）；再对一台本机没有该 Capsule 的
  WI（在另一台工作机执行或已结束）发起 observe，断言兜底走 `02-aihub-core.md#§5.2` 的
  read-through，渲染最新已发布 HEAD 且标注「数据截至最近一次发布」（第二层）；两层均为短
  请求/响应，不出现 SSE、服务端推送或长连接。

### 9.2 观测指标 <!-- §9.2 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（SYNC-* 码、Interactive/Batch/Drain、watcher dirty 位、read-through）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

指标分层遵循 `00-architecture.md#§9` 的总则（AIHub / Daemon / Sync / Workflow / Capsule / Memory 六层各自的
权威指标定义分别在 `02`/`04`/`05`/`01`/`03`/`06-memory-search.md#§9`）；本节只定义 Pilot 期间额外聚合、用于
否决判定的复合指标。

| 指标 | 口径（含统计窗口与分母，INV-D5） |
|---|---|
| M1 现场一致性 | HEAD 回退次数（`05-sync-revision-recovery.md#§9` 定义的事故计数），统计窗口 = Pilot 累计运行以来；出现即事故，无分母（计数型指标，非比例）。 |
| M2 误删 | 分子 = 对非终态 WI 的删除尝试中最终导致实际文件被移除的次数；分母 = 同一时间窗内对非终态 WI 的全部删除尝试次数；窗口 = Pilot 累计运行以来。正常应恒为 0（全部应被 `05` `SYNC-409` 拦截）。 |
| M3 卡死步 | `in_progress` 超过 24 小时且对应机器无心跳事件的 step 数；窗口 = Pilot 累计运行以来；计数型指标。 |
| M4 校验真实性 | 分子 = 其 `runtime-state/execution.json` 记录含**结构化命令证据**——`output.commands` 数组至少一条 `{argv, exit_code, duration_ms}` 且 `exit_code` 可读（字段权威在 `03-execution-capsule.md#§4.2`；证据链约束见 §9.2a INV-D3/INV-D4）——的 verification step 次数；**任意非空 `output`（自由文本、`output.summary` 一类）不构成证据**；分母 = 同一窗口内全部已执行的 verification step 次数；窗口 = Pilot 累计运行以来。旧版 schema 数据（无 `commands` 字段）不计分子。 |
| M5 谎报逃逸 | §9.2a 给出的机械信号命中次数；窗口 = Pilot 累计运行以来；计数型指标，不是比例。 |
| M6 检索实时性 | 口径：滚动 24 小时窗口内，按 `06-memory-search.md#§4.5` 的样本计时定义（`INSERT`/`UPDATE memories` 事务提交 → **FTS5 trigram 文本索引可检索**：`q=` 检索可命中该条目），计算 Memory 写入→可检索延迟样本的 **p99**；合格阈值直接指向 `06-memory-search.md#§4.5` 的固定可计算值 **p99 ≤ 5s（5000 ms）**，本文不自定第二套数值。样本集 = 窗口内全部写入事件产生的延迟样本；**向量语义检索层不参与本指标、不设验收口径**（`06-memory-search.md#§1.2` 最终裁定：v1 仅文本检索（FTS5 trigram）；语义检索为非 v1 演进项，v1 不实现、不部署、不验收，记入 §12 第 6 项）；样本数为 0 时该窗口不参与否决判定（INV-D5：窗口与样本集如上定义）。 |
| M7 同步收敛 | 全扫描发布（`05-sync-revision-recovery.md#§2.4` 唯一路径）后，对同一 WI 连续 3 次触发事件（`00-architecture.md#§4.3` 四类事件，不区分间隔时长）扫描结果仍与已发布 HEAD 存在未收敛差异的次数；窗口 = Pilot 累计运行以来；计数型指标。**不使用任何按时间的「周期」表述**——`05-sync-revision-recovery.md#§2.4` 已裁定不存在独立的周期性对账路径，本指标以「触发次数」而非「时间周期」为分母口径，避免与该裁定冲突。 |
| M8 发布时延与结果 | 见下方独立说明（B13 口径修订：仅对已真实发生的边界同步请求计分，不使用无活动时间窗口判定）。 |
| M9 rewind 收敛 | 数据源 = `03-execution-capsule.md#§4.2` `runtime-state/execution.json` 的 `rewind_stats`（`{total, by_step}`），经 `02-aihub-core.md#§5.2` read-through 逐字读取；窗口 = 单 WI 生命周期内累计值，不做跨 WI 聚合比例。 |

**就绪队列口径（扁平化核对，定稿裁定；D1 修订 polyforge#150：以代码为准）**：vNext 的就绪队列是扁平过滤列表（`GET /api/v1/projects/{project}/ready-queue`，
`02-aihub-core.md#§5.1` 行 4），不存在旧系统的多段结构；其响应 schema 权威为四段
`{items, running, paused, pending_bundles}`（`02-aihub-core.md#§5.1b`）：`items` 为可立即认领面，恒排除
`bundle_name IS NULL` 的登记票与一切终态/`running`/`paused`/`blocked` 的 WI；等依赖的票不出现在任何段（无 `blocked` 数组）；
`running`/`paused` WI 各自出现在同名可见性段（含心跳过期的 stale running，不设独立段）。本节任何涉及队列条目的计数一律以
**响应各段的条目数**（同一 project 查询下 `items`/`running`/`paused`/`pending_bundles` 各自的返回条目数）为口径，
不设段间配额。经核对：
M1–M9 无一依赖旧多段结构——M3 的卡死判定来自 step 状态与机器心跳两个扁平字段
（`02-aihub-core.md#§4.10`），不来自队列分段；Batch/Drain 的取批（`00-architecture.md#§3.4b`/§3.4c）
消费的也是该四段响应的 `items` 可执行段。

**M8（发布时延与结果，机械口径，B13 口径修订）**：早前口径曾用一个可配置的「30 分钟无 dirty/
无发布」窗口（`pilot.stale_head_window_minutes`）推断某个 WI 可能卡死——把「一段时间内没有观测
到活动」本身当成了失败信号，即使该 WI 在这段时间内合法地毫无文件系统变化（例如一个耗时较长但
确实还没有产出的 step），也会被计入「落后」；这不是 INV-D5 要求的「读 API/指标即判」机械口径，
而是一个凭经验设定的沉默窗口阈值，容易把「合法的长时间无变化」与「真实卡死」混为一谈。本节改为
**只对已经真实发生的边界同步请求**计分，不再对「某段时间内没有发生」这件事本身下判断：

- **边界同步请求**：`00-architecture.md#§4.3` 定义的四类触发时机之一实际发生后，Daemon 按 `05-sync-revision-recovery.md#§2.4` 唯一路径
  发起的一次全扫描发布尝试（`05-sync-revision-recovery.md#§6` 的六步流程）。
- 对每一次这样的请求，记录其从触发到结束（成功推进 HEAD，或以某个 `SYNC-*` 错误终止）的耗时与
  结果。
- 分子 = 其中以非幂等失败终止，或耗时超过运维告警阈值（复用 `05-sync-revision-recovery.md#§9` 已定义的“发布延迟（从触发事件到 HEAD
  推进完成）”监控口径，具体阈值由 Pilot 运维配置，本文不新造第二套指标定义）的请求次数；分母 =
  同一 Pilot 观测窗口（§9.1）内全部实际发生的边界同步请求次数；分母为 0（该窗口内该 WI 未触发
  任何 `00-architecture.md#§4.3` 事件，例如全程未产生任何改动）时该次计算不参与否决判定。
- **不再定义、也不再依赖任何「无活动时间窗口」配置作为判据来源**：一个 WI 长时间没有触发任何
  `00-architecture.md#§4.3` 事件，本身不产生任何 M8 样本，不计入分子也不计入分母——这与 M7（同步收敛）以「触发
  次数」而非「时间周期」为分母口径的既有裁定（`05` SYNC-INV-1）保持一致，不重新引入按静默窗口
  判定的第二条路径。
- 真正的「step 卡死且无心跳」由 M3 单独覆盖（`in_progress` 超过 24 小时且对应机器无心跳），两
  个信号不重复计数：M8 只看已发生的同步请求本身的时延与结果，不推断「为什么这段时间没有发布」。
- 心跳本身失活（超过存活阈值）的情形不计入 M8，归入 `02-aihub-core.md#§7`/`04-local-runtime.md#§7.2` 已定义的 `stale_running`
  检测，两个信号不重复计数。

### 9.3 否决门槛（每条均给出机械判定公式） <!-- §9.3 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（WF-* 码、PILOT-* 码）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

| 门槛 | 判定（机械） | 触发动作 |
|---|---|---|
| `PILOT-V1` | M1 计数 > 0（Pilot 累计运行以来任何一次 HEAD 回退） | 否决：回滚或重设计 Sync 域 |
| `PILOT-V2` | M2 分子（实际移除数）> 0 | 否决：回滚或重设计删除面 |
| `PILOT-V3` | M3 计数 > 1（在 ≥ 40 张 WI 的样本内） | 否决：执行恢复语义重设计 |
| `PILOT-V4` | M4 比例 < 100%（分母口径见 §9.2；出现任何一次「未在执行权威文件留下证据却判定通过」） | 否决：验证承载面重设计 |
| `PILOT-V5` | M5 计数（§9.2a 机械定义）> 0 | 否决：强校验契约重设计 |
| `PILOT-V6` | M6 的滚动 24 小时 p99（样本口径见 §9.2；仅 **FTS5 trigram 写入→可检索延迟**样本）**> 5000 ms**，即超出 `06-memory-search.md#§4.5` 固定可计算阈值「写入→可检索 p99 ≤ 5s」 | 否决：索引路径重设计 |
| `PILOT-V7` | M7 计数 > 3 | 否决：对账机制重设计 |
| `PILOT-V8` | M8 比例（§9.2 定义的、仅基于已发生边界同步请求的耗时超阈值/失败占比）> 1% | 否决：全扫描发布链路时延或失败率重设计 |

全部门槛均为「读 API/指标即判」，无主观判断项，且每条均已随 §9.2 补齐窗口与分母定义
（INV-D5）。`M9` 不设否决门槛（观测项）：rewind 数量上界本身已由 `01-workflow-skills.md#§6.2` 的
`max_per_step`/`max_total` 收敛，超界直接进入终态（`WF-101`），不需要额外的 Pilot 期比例门槛。

### 9.2a M4/M5 的机械可判定性与证据来源（INV-D3/INV-D4 落实） <!-- §9.2a -->

> **ARCHIVED — 本节描述或依赖已删除的机制（持久 WI Agent、Revision/CAS/manifest、RunCommand、read-through）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

**M5（谎报逃逸）的判定不依赖任何形式的人工抽查**，也**不以「调用了多少次 `RunCommand`」作为
可疑信号**（该口径的问题：产出代码或文档的 step（如 `code`、`fix`、`plan`、`e2e-author`）完全
可能只经由文件写工具——Edit/Write 类操作——合法完成全部工作而不调用任何 `RunCommand`；把「零
命令调用」本身当作逃逸信号会把这类合法产出误判为逃逸，产生假阳性）。判定改为直接检查**该 step
是否产出了它承诺的 artifact**（INV-D4）：

- **逃逸判定（机械，仅对产出型 step 计算——当前 bundle 中为 `repro`、`fix`、`code`、`deliver`、`release`、`deploy`、`plan`、`e2e-author`
  （polyforge#169 删除 step `kind` 后按 step id 列举，即原 `kind ∈ {code, author}`）；其余 step 不计算本判据）**：一个该类 step 的执行
  结果被判定为「未产出承诺的 artifact」，当且仅当以下三者**同时**成立：
  1. 该 step 所写仓库相对**该 step 自己的起始基线**（`03-execution-capsule.md#§4.2` `steps.<id>.step_start_refs`，不是 WI 级
   认领基线（`capsule.json` 的 `repos`））的实际工作树 diff 为空；
  2. `artifacts/` 目录下没有该 step 新增的文件；
  3. `runtime-state/execution.json` 中该 step 的 `output` 字段（`03-execution-capsule.md#§4.2`）为空或不含任何非空
     产出内容（含 `output.commands` 为空或缺失）。
  三者同时成立意味着该 step 声称自己已完成，但工作树、产物目录、执行权威文件三个独立通道都看不
  到任何实际产出——这与「用了多少条 shell 命令」无关：一个通过文件写工具产出真实 diff 的 step，
  条件 1 就不成立，不计入逃逸；问答、判定类 step 的正常产出本就可能是零文件改动（例如
  纯问答的 grill、只打分不改代码的 review），因此本判据只对上列产出型 step 计算，不对其余
  step 计算（避免把合法的「无产出」误判为逃逸）。
- 当前架构（`00`–`06`）未定义任何「声明改动文件清单」字段（没有 `declared_files` 或等价
  结构）；本判据因此只比对「实际 diff 是否为空」，不做「声明与实际是否全等」的比对——这一点
  与历史（workflow-v2 时代）曾经设想过的强校验机制不同，vNext 按当前已定义的数据模型对待，不
  假设存在尚未落地的声明字段（`00-architecture.md#§1.2`）。
- **局限**（记入 §12）：本判据能拦截「完全没有产出却声称完成」的最小谎报，不能识别「产出了内容
  但与声明的意图不符」这类语义级谎报；后者需要更强的命令-产物关联性或语义审查，一期不实现。

**M4（校验真实性）与 R5/R11 一类回归断言所依赖的「命令确实被执行」证据（B10 引入、B18 裁定
保持：结构化可定位证据，不接受任意非空输出）**，其来源必须满足 INV-D3：唯一可信的信号路径是 Capsule
Native runner 在 `RunCommand`（`03-execution-capsule.md#§5.1`）调用返回时生成的 `Result{exit_code, stdout, stderr, killed,
duration_ms}`，经 WI Agent（唯一调用方与唯一 `runtime-state/execution.json` 写者，INV-6）落入
该 step 的 `output`/`attempts` 记录。**校验不接受任意非空 `output`**：`output` 必须是结构化对象，
至少含一个 `commands: [{argv, exit_code, duration_ms}]` 数组（每次 `RunCommand` 调用逐条追加，由
`03-execution-capsule.md#§5.1` 的 `RunCommand` 调用方（WI Agent）写入，不是 Native runner 自己写）；M4 的分子只计
入“`commands` 数组至少有一条且其 `exit_code` 可读”的 step，一段任意字符串的 `output.summary` 不作
数。该 `commands` 字段已由 `03-execution-capsule.md#§4.2` 定义（B10），不是待新增的假设；旧版 schema 数据（无该字段）
不计分子，`command_count` 等统计列不需要单独存在，`len(commands)` 即可计算。因此 M4/M5 的判据
**不**依赖容易被滥用的“非空就算数”口径，而是依赖结构化、可定位到具体命令调用
的记录。Capsule `logs/` 目录的内容明确**不**作为该证据的来源（INV-D3）：`logs/` 对
harness-native subagent 是可追加写权限（`03-execution-capsule.md#§5.2` 权限面表），若允许 subagent 自己写下的文本充
当「我确实执行过命令」的证据，该证据可以在故障或误导情形下被 subagent 自己产出；只有由 Main
Agent 独占写入的 `runtime-state/execution.json`（INV-6）才具备 INV-D3 所要求的不可由被观察方单
方面伪造的性质。

**M9（rewind 收敛）** 的数据源是确定的：`03-execution-capsule.md#§4.2` `runtime-state/execution.json` 的
`rewind_stats = {total, by_step}` 随每次 rewind（`01-workflow-skills.md#§6.2`）更新并随 Revision 一起发布；
`02-aihub-core.md#§5.2` 的 read-through 逐字返回该字段，Pilot 报告直接读取，不涉及任何估算或抽样。

## 10. 测试、故障注入与回归清单 <!-- §10 -->

### 10.1 部署域测试与故障注入 <!-- §10.1 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（持久 WI Agent、owner generation、SYNC-* 码、Revision/CAS/manifest、Interactive/Batch/Drain）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

- 升级序端到端：单机升级 → 断言 INV-D1（升级窗内任意时刻查询 Core 写路径，不存在两个进程同时
  持有 `service.lock` 的窗口）、Capsule 现场零丢失（`05-sync-revision-recovery.md#§7.1` 收敛断言）。
- **升级/任意机器恢复的 generation-session 回归测试**：对一个处于 running 状态的 WI 触发 §6.3
  步骤 5 的升级流程（先优雅停机、替换二进制、重新拉起 Daemon），或把该 WI 的续跑请求改发到另一
  台工作机 → 断言恢复后该 WI 的 owner generation 严格大于优雅停机前的值（新一次 claim/resume，
  `02-aihub-core.md#§2.3`/§2.5）、其 `harness_session_id`（`04-local-runtime.md#§4.1`）与优雅停机前不同，且续跑走的是 `04-local-runtime.md#§6.1`
  的正常 claim/resume 序列而非 `04-local-runtime.md#§7.1` 的 pid 复用崩溃恢复路径；仅当在**同一台机器**上模拟
  Daemon/WI Agent 意外崩溃（未经优雅停机）时才断言 `harness_session_id` 与崩溃前一致（`04-local-runtime.md#§7.1`）。
- **Interactive/Batch/Drain 与优雅停机的部署面验证**：分别以三种模式各驱动至少一个 WI 到完成，
  并对处于 Batch/Drain 模式的工作机发送一次优雅停机信号（`00-architecture.md#§3.5`/`04-local-runtime.md#§6.5`）→ 断言优雅停机
  期间不接受新 Job（`DAEMON-410`）、每个活跃 Capsule 恰好触发一次全扫描发布、进程正常退出后
  未完成的 WI 现场保持完整；**必须另外断言处于 Drain 模式的工作机在收到优雅停机信号后，其内部
  取 wave 循环立即停止取下一批**（不再对 AIHub 调度器发起新的 ready queue 请求，也就不再产生新
  Job），与拒绝外部提交（`DAEMON-410`）是同一个 `shutting_down` 标志驱动的两条独立阻断路径
  （`04-local-runtime.md#§6.1c`/§6.5）；断言 watcher 在四类触发时机之外从不单独触发发布（`00` INV-11/
  `05` SYNC-INV-1）——构造一个只产生文件系统变化但不触发任何 `00-architecture.md#§4.3` 事件的窗口，断言该窗口
  内 HEAD 不推进，直到下一次真实触发事件到来才推进。
- **可注入故障清单**：
  1. `aihub migrate` 中途 `kill -9`：预期数据库回滚到迁移前 schema，可按 `deploy.sh` 打印的回滚命令
     恢复到最近备份点。
  2. 升级窗口内工作机 Daemon 断连 1 小时：预期 WI Agent 侧退避重试（`05-sync-revision-recovery.md#§6`），AIHub 恢复后
     由下一次 `00-architecture.md#§4.3` 触发事件驱动一次全扫描收敛，无数据丢失。
  3. 两个 `aihub` 进程在同一主机并发启动：预期后起进程立即退出（启动期锁拒绝）。
  4. 对处于 running/paused/blocked 状态的 WI 发起删除：预期 `05` `SYNC-409`，文件系统内容不变。
  5. tombstone 窗口（24h）内撤销删除：预期该 WI 的 Revision 数据与 Memory 引用完整恢复。

### 10.1a Core 功能面最小验收（多 project / membership / epic-lite / milestone / assignee / trigram） <!-- §10.1a -->

与 `02-aihub-core.md` 定稿对齐的最小验收面（本节只给验收条目与机械断言，不展开设计，设计权威均
在 `02-aihub-core.md`，此处不重复；与 §9.1 的四项启动前置验证并列，以下五项逐项通过才进入观测
窗口，缺一视为对应功能面未覆盖）：

1. **多 project 功能面**：经 `pf` 管理 CLI 在验收环境再建一个 project（`02-aihub-core.md#§5.1` M3 面），
   在两个 project 各建 ≥1 张 WI → 断言列表/过滤（行 2）、就绪队列（行 4）与 Memory 检索
   （`06-memory-search.md#§5.1`）均按 `project` 维度隔离返回，不出现跨 project 串数据。
2. **membership 硬边界**：无第二个 project membership 且非 `admin` 的 user，对该 project 的
   WI（详情/progress/事件）与 Memory 发起读 → 一律 `CORE-404`；写 → 一律 `CORE-403`；就绪队列
   查询整个按不存在处理（`CORE-404`）（`02-aihub-core.md#§4.10`）；`admin` 全局豁免面
   （不写 `project_members` 行）可读。
3. **epic-lite parent**：创建登记票父票（`bundle_name IS NULL`）与带 `parent_id` 的子票 →
   断言父票不可 claim（`CORE-409`，`reason=NOT_CLAIMABLE`，`02-aihub-core.md#§4.4`）；再对子票挂父票
   （深度 2）被拒（`CORE-400`，`reason=PARENT_DEPTH`）；以父票（登记票）为 blocker 的依赖边被拒
   （`reason=BLOCKER_NOT_EXECUTABLE`，`02-aihub-core.md#§4.2a`）；`include=children` 返回直接子票
   清单与派生 `rollup`（`02-aihub-core.md#§5.1c`），断言 rollup 各计数与子票实际状态一致且不落库。
4. **milestone / assignee**：对 WI `PATCH` `milestone` 后按 `milestone=` 过滤、按 `assignee=` 过滤
   （`02-aihub-core.md#§5.1` 行 2）→ 断言过滤命中（索引路径 `02-aihub-core.md#§4.2`）；claim 成功后
   断言该 WI 的 `assignee` 等于活跃 generation 行的 owner（`02-aihub-core.md#§2.4`）。
5. **trigram 索引**：断言 `memory_fts` 建表为 `tokenize='trigram'`（`02-aihub-core.md#§4.9`，与
   `06-memory-search.md#§4.4` 一致）；执行一次全量重建（`02-aihub-core.md#§5.1` 行 23，流程
   `06-memory-search.md#§5.3`）后 tokenizer 仍为 trigram，且子串级检索命中预期条目——
   不出现 `unicode61` 等其他 tokenizer 口径。

### 10.2 回归清单（13 条，逐条含缺陷摘要 / vNext 交叉引用 / 机械反例断言） <!-- §10.2 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（持久 WI Agent、owner generation、SYNC-* 码、CAP-* 码、WF-* 码、Revision/CAS/manifest、RunCommand、takeover、read-through）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

> 考证方法（B19 修复：逐条经本仓 git 历史考证，不用「仅按编号可追溯」作声明）：13 个编号均为本 aihub
> 部署自身的真实 polyforge 工作项（work item，非 GitHub issue）。撰写本文档时在当前 worktree（即
> 本仓，只读）逐条执行 `git log --all --grep='#<n>'`；对无编号命中的条目再按缺陷关键词补充检索
> （如 `--grep='wake'`、`--grep='toolchain'`、`--grep='producer'`），并对候选 commit 用
> `git show --stat <hash>` 复核修复落点。下方每条 R 条目的「git 考证」行逐字记录检索结果：
> 找到 commit 的给出 hash、commit 主题与修复落点（文件/目录）；未找到的如实记录为「本仓无引用
> 该编号的 commit」并注明关键词检索得到的最近机械旁证——**不得把相邻编号的 commit 冒充为本条
> 修复**。缺陷摘要以工作项记录的 goal 为准（下方逐字摘录），与 commit 主题互相印证。workflow-v2
> 时代的具体实现细节仅作为「这是什么问题」的语境描述，vNext 按全新执行模型对待，不复刻其内部
> 机制（`00-architecture.md#§1.2`）。

**R1 `aihub#699`** — 缺陷：某类基础设施故障（如上游限流）发生后，恢复路径依赖人工判断是否
应该重派或唤醒，缺乏明确的自动化恢复语义。
真实来源：`pf_get_work_item(aihub#699)` → `wi_B19ymc3t`，goal 逐字为「pf-execute 的派发指引没说「step agent 被上游 429 打死之后该重派还是该唤醒」，实测一次唤醒停死 15+ 分钟后编排方只能在主会话重做；叠加 explorer/operator 两个角色被固定在 haiku 档，恰好是最易撞限流的那一档」。
git 考证（本仓 git 历史）：`git log --all --grep='#699'` 无命中，`--grep='wake'`/`--grep='429'` 亦无
引用该编号的 commit——本仓无 #699 修复；最近机械旁证：28abbb6（aihub#740，2026-09-21）正文记录
了「provider_error first attempt killed by an astra 429 … the runner reconciled correctly」，是
同一 429 类故障的收敛事实，但不冒充本条修复。

vNext：基础设施失败与业务判定严格二分（INV-9），基础设施失败在当前 step 原地 resume 是 Main
Agent/Daemon 的内建语义，不需要人工决策（`01-workflow-skills.md#§6.3`、`04-local-runtime.md#§7.1`）。
机械反例断言：对运行中的 step 注入连续基础设施类故障（如进程被杀）后，workflow 停留在当前 step 且
Daemon 自动重新派生 WI Agent 续跑，不产生任何需要人工介入的中间态。

**R2 `aihub#709`** — 缺陷：claim 操作报告成功，但本地找不到与之对应的可见工作区/状态文件，
执行现场与已声明的成功状态脱节。
真实来源：`pf_get_work_item(aihub#709)` → `wi_gwON5cul`，goal 逐字为「Investigate claim success without visible local state or worktree in Codex workspace」。
git 考证（本仓 git 历史）：`--grep='#709'` 无命中；`--grep='claim success'`/`--grep='Codex'` 仅命中
版本戳与技能审计类 commit（6959a37/#763、dc64d53/#775 的 stamp 行），无 #709 修复——本仓无对应
commit。

vNext：claim 前有本机预检步骤（`04-local-runtime.md#§6.1` 第 1 步），且 Capsule 是有形化的、可独立校验的目录
结构（`03-execution-capsule.md#§3.1`/§6.1），`capsule.json` 是身份可验证文件。
机械反例断言：任一 WI claim 成功后，`<pf-workspace>/capsules/<project>-<seq>/capsule.json` 必然存在，且其记录
的 generation 与 Core 侧记录一致；预检失败时不产生任何 claim 事件（服务端零记录）。

**R3 `aihub#714`** — 缺陷：本地状态目录不可用的情况未在发起接管（takeover）操作前被检测到，
接管在状态不确定的前提下继续执行。
真实来源：`pf_get_work_item(aihub#714)` → `wi_Tvk0wAz6`，goal 逐字为「bug: MCP loses workspace state and performs takeover before detecting an unusable local state directory」。
git 考证（本仓 git 历史）：`--grep='#714'` 命中 a4f75e9（feat(mcp): driver-session attempt binding,
state-dir preflight, stale-running alerts (aihub#744)，2026-09-21）——其正文第 2 条逐字注明
「State-dir preflight (absorbs aihub#714)」；修复落点 `internal/config/state.go` +
`internal/mcp/attempt_binding.go`（claim/force-takeover 前置预检：不可用状态目录以
`STATE_DIR_UNUSABLE` 拒绝且不发起任何服务端写请求）。

vNext：预检步骤先于**任何**服务端写请求（含 force-takeover）执行（`04-local-runtime.md#§6.1` 第 1 步）；目录
不可用时直接拒绝，不发起接管请求。
机械反例断言：使本机 Capsule 状态目录只读或不可访问后发起 takeover，返回预检失败错误（`DAEMON-507`），
且 Core 侧 owner generation 保持不变（不产生任何接管副作用）。

**R4 `aihub#744`** — 缺陷：一个会话被另一方接管后，原会话没有可见信号得知自己已经出局
（supersession 对失败方不可见），继续基于失效状态操作。
真实来源：`pf_get_work_item(aihub#744)` → `wi_1OF9ldJT`，goal 逐字为「Driver-session liveness and multi-session claim hygiene: surface stale running attempts and make supersession visible to the loser」。
git 考证（本仓 git 历史）：`--grep='#744'` 命中 a4f75e9（正文第 1 条「supersession visible to
the loser」：`internal/mcp/attempt_binding.go` 的 typed `LOCAL_ATTEMPT_SUPERSEDED` 拒绝，接线
`internal/config/state.go`/`internal/mcp/tools_*.go`）、39176d0（stale-running alert machinery：
`internal/cli/doctor.go` 与 `internal/domain/`）、17122f9（wi_stalled 自刷新排除：
`internal/domain/work_items.go`）。

vNext：机器心跳与 stale_running 检测经监控面外显（`02-aihub-core.md#§4.10`/§9）；就绪队列不为其
设任何段——该 WI 处于 `running` 状态，按 `02-aihub-core.md#§5.1b` 只出现在 `running` 可见性段、不设独立
stale 段；被接管一方的下一次写/
发布请求会明确收到失效响应（`02-aihub-core.md#§5.1` `CORE-403 STALE_OWNER`），响应体携带需要重新认领的提示，
不含可被误用的裸 generation 整数。
机械反例断言：心跳停止超过既定阈值后，该 WI 经监控面以 stale 标记可查询，且在就绪队列
（`02-aihub-core.md#§5.1b`）中仅出现在 `running` 可见性段、无独立 stale 段（条目数口径 = §9.2 的各段条目数）；接管发生后，旧
generation 的下一次写请求被拒绝，且响应体明确提示需要重新认领而不透出可用于伪造新凭证的信息。

**R5 `aihub#747`** — 缺陷：某个执行环境下 verification 类 step 没有可用的命令执行能力（例如
只读工具面、没有 shell），导致全部验证被静默跳过而不产生真实校验结果。
真实来源：`pf_get_work_item(aihub#747)` → `wi_JI0kjrlW`，goal 逐字为「Verification gate cannot execute its contract commands on the pi harness: read_only tool allowlist has no shell, so every engine-dispatched verification honestly skips all checks」。
git 考证（本仓 git 历史）：`--grep='#747'` 命中 554be69（feat(modelruntime): verify_command —
constrained command execution for read-only verification dispatches，落点
`internal/modelruntime/workerext/submit_result.go`、`internal/modelruntime/command.go`、
`internal/controller/execute.go`）与 9cb3ecc（round-1 hardening：unshare-net、clearenv、
GOPROXY=off，同落点；merge 3a19acc）。

vNext：verification step 的执行是 Capsule 内真实的 `RunCommand` 调用（`03-execution-capsule.md#§5.1`），Native
runner 直接使用宿主机已安装的工具链，没有第二种「只读、无命令执行能力」的执行 profile 可供
派发（`03-execution-capsule.md#§5.2`）；工具缺失时 `RunCommand` 直接失败为 `CAP-401 RUNNER_UNAVAILABLE`，而不是
静默跳过后仍判定通过。
机械反例断言：每个已执行的 verification step 的执行记录（`runtime-state/execution.json` 的
`output.commands`，`03-execution-capsule.md#§4.2`）中含至少一条 `{argv, exit_code, duration_ms}` 且 `exit_code` 可读的
结构化命令证据（与 §9.2 M4 分子同口径，证据来源见 §9.2a
INV-D3）；若宿主机缺少命令声明的可执行文件，派发阶段即返回 `CAP-401`，不存在静默跳过后仍判定
通过的路径。

**R6 `aihub#749`** — 缺陷：某类因基础设施原因中止的执行结果被归入一个既不算基础设施失败、也
不匹配任何既定恢复路径的分类，导致该 step 永久卡在无法被任何恢复机制处理的状态。
真实来源：`pf_get_work_item(aihub#749)` → `wi_PfUXzHwx`，goal 逐字为「workflow-v2 修复面缝隙：非 review 步 worker 死于基础设施被记 invalid_result，而 retry 只认 provider_error、episode 只认 review FAIL ⇒ 步骤永久卡死无 sanctioned 恢复路径（aihub#741 只修了 review 那半）」。
git 考证（本仓 git 历史）：`--grep='#749'` 命中 ccbda3e（fix(workflow): worker-death
classification — provider_error for every no-submit_result death，落点
`internal/controller/execute.go`、`internal/domain/workflow_run.go`）；同一分类面的后续补全为
6a936fe（aihub#755）。

vNext：失败结果只有基础设施失败与业务 gate FAIL 两类（INV-9），外加一等的 incomplete 结果
（`01-workflow-skills.md#§6.4`）；不存在第三种「无恢复路径可用」的分类。
机械反例断言：在 step 执行的任意时点终止其进程，该 step 在 `runtime-state/execution.json` 中的落点必属于
`{done, in_progress（可 resume）}` 之一；状态模型中不存在无法被任何既定路径处理的终态。

**R7 `aihub#7`** — 缺陷：一次对本机某类临时目录的批量清理操作，误删了一个仍在运行中的 WI
的工作区与状态文件（数小时未提交的实现工作因此丢失）。
真实来源：`pf_get_work_item(aihub#7)` → `wi_P7avSVU7`，goal 逐字为「worktree/state 清理面误删运行中工作项：2026-09-21 21:21 另一会话全量清扫 pf.* 目录，删掉 running 状态 demo-ops#7 的 worktree（含 ~4h 未提交实现）与 state 文件 —— 所有删除路径必须先过 server 端 wi 状态校验，running/paused/blocked 一律拒绝」。
git 考证（本仓 git 历史）：`--grep='#754'` 无命中——2026-09-21 的清扫事件无引用该编号的 commit；
同类「清理面误删活跃现场」缺陷在本仓有更早一次可考修复：2b8e924（fix(doctor): stop --fix
deleting live worktrees，aihub#307，2026-09-01，落点 `internal/cli/doctor.go`），证明该缺陷
类别真实存在；本条的修复形态是 vNext 删除保护（`05-sync-revision-recovery.md#§2.10`），不是一次已合并的代码修复。

vNext：删除的唯一入口是 Core API，且经状态门（非终态状态一律拒绝）+ 确认门 + 24h tombstone
（`05-sync-revision-recovery.md#§2.10`/§5.1/§5.2，INV-D2）；不存在任何文件系统级的批量清扫端点。
机械反例断言：对处于 running/paused/blocked 状态的 WI 发起删除，一律返回 `05` `SYNC-409` 且文件系统
内容不变；在 tombstone 保留期内撤销删除，可恢复被删除的全部现场。

**R8 `aihub#770`** — 缺陷：涉及多个仓库的 WI，其补丁组装只锚定单一工作树，执行方需要靠猜测
定位其余仓库的工作树，导致「以为改了但其实落在了错误的仓库/工作树」的结果被多次接受。
真实来源：`pf_get_work_item(aihub#770)` → `wi_SBaWkawH`，goal 逐字为「多仓 work item 的流水线裸奔：补丁组装只锚定单树、worker 找兄弟工作树靠猜、changed_files 声称与实际工作树 diff 无强校验——“声称未做”可四轮通过，需多仓 inventory/patch/校验契约」。
git 考证（本仓 git 历史）：`--grep='#770'` 无命中；关键词旁证：6a936fe（aihub#755）正文
「The inventory is ENUMERATED FROM GIT, never inherited from the producer's changed_files
claims」——同一「声明与实际工作树 diff 无强校验」缺陷面的部分修复，但编号为 #755，不冒充
#770 的修复；本仓无 #770 commit。

vNext：repo inventory 作为声明+冻结基线（`03-execution-capsule.md#§4.1`/§4.3）：每个仓库检出到 `<capsule>/repos/
<name>/`，以声明的 base commit 为唯一基线；补丁组装逐仓锚定到该声明清单本身，不存在默认单一
工作树可供猜测。当前架构未定义「声明改动文件清单 vs 实际 diff 全等」的机械强校验字段（§9.2a
已明确指出这一点），本条回归因此**不**断言存在这样一份全等校验，只断言多仓歧义已被消除。
机械反例断言：构造一个声明多个仓库的 WI，分别在每个仓库产生改动，断言每个仓库的改动准确落在其声明的
`repos/<name>/` 工作树内，不出现「改动落在错误仓库」或「需要猜测目标仓库」的路径；`03-execution-capsule.md#§4.3` 的
`inventory.json` 快照记录的 `base`/`head_at_checkout` 与实际检出一致。

**R9 `aihub#773`** — 缺陷：存在两个都自称权威的执行进度读取面，二者的读数在某些情况下不一致，
可能误导恢复决策或错误触发某个判断门。
真实来源：`pf_get_work_item(aihub#773)` → `wi_6EkbjYXw`，goal 逐字为「pf_get_step 与 pf_get_workflow 步骤记录不一致：#771 六步全 completed（steps_version=1、episode 闭合、事件齐全），pf_get_step 却稳定返回 version=0 / completed_steps=[] —— 自称 authoritative 的读数面与 workflow-v2 记录面分叉，resuming 会被误导、no-steps 闸可能误拦」。
git 考证（本仓 git 历史）：`--grep='#773'` 无命中，`git log --all -S'completed_steps'` 仅命中
步骤读取面的常规演进（aa28b22/#758、580a446/#735），无 #773 修复；goal 引用的 #771 在本仓有
commit（165aaca，cc read-only carrier for gate-step dispatch）可考，佐证事件序列真实；本条无
commit。

vNext：执行态的唯一权威是 Capsule `runtime-state/execution.json`（`03-execution-capsule.md#§4.2`）；对外的进度
读取由 `04-local-runtime.md#§5.1a` 的**两层观察协议**承载：本机拥有该 WI Capsule 时走 Daemon 内存 ring
buffer 事件流端点（`GET /jobs/{wi_id}/observe?after=<cursor>`，活视图），本机无该 Capsule 时 `pf observe`
走 `02-aihub-core.md#§5.2` 的 Core read-through 兜底；Daemon 自身状态（`04-local-runtime.md#§4.1`）明确不含任何
workflow 执行状态字段（`04` INV-D1），不构成第二个执行进度记录面。
机械反例断言：①**本机视图允许领先于最近发布的 Revision**——对一台本机正在执行该 WI 的机器，第一层
事件流的 `state_snapshot` 与增量事件可反映尚未随任何 Revision 发布的执行进度，且事件不持久化、
其渲染来源仍是 Capsule `runtime-state/execution.json` 本身（`04-local-runtime.md#§5.1a`），不因此产生第二
权威；②**跨机视图恒等于最近 HEAD**——对一台本机没有该 Capsule 的 WI 发起 observe，兜底走
`02-aihub-core.md#§5.2` read-through，返回内容与其最近发布 Revision 内的 `runtime-state/execution.json`
（`03-execution-capsule.md#§4.2`）逐字节一致，且 UI 标注「数据截至最近一次发布」；③**无第二进度
数据源**——对全部已定义 API 端点（`02-aihub-core.md#§5.1` canonical 清单 + `04-local-runtime.md#§5.1`
Daemon 操作面）的枚举中，不存在第二个可独立计算执行进度的端点（Daemon `status()` 只返回
`04-local-runtime.md#§4.1` `jobs[]` 的本机簿记字段，不含任何 workflow 执行进度字段，`04` INV-D1）；④
两层均为短请求/响应，不出现 SSE、服务端推送或长连接（`04-local-runtime.md#§5.1a`）。

**R10 `aihub#775`** — 缺陷：某类非 review 步骤产生的诚实 incomplete 结果，不匹配任何既定的
恢复通道，导致该结果永久无法被处理、WI 无法闭合。
真实来源：`pf_get_work_item(aihub#775)` → `wi_BN1eUTBT`，goal 逐字为「fix: honest incomplete result on a non-review step fits no repair kind (retry/episode/supersede all refuse) - extend supersede to accept incomplete with non-FAIL verdict」。
git 考证（本仓 git 历史）：`--grep='#775'` 命中 dc64d53 与 e07208b（aihub#775: supersede accepts
honest incomplete results (non-FAIL verdict)，2026-09-24；落点 `internal/controller/run.go`、
`internal/domain/workflow_run.go`、`internal/domain/workflow_supersede_db_test.go`）。

vNext：incomplete 是一等的 step 结果类型（`01-workflow-skills.md#§6.4`），触发 WI 进入 `blocked` 状态且
`waiting_on=owner`，由人决定续跑或终止该 WI，不需要为它单独设计恢复通道。
机械反例断言：任一 step 返回 incomplete 结果后，WI 在有界时间内进入 `blocked`/`waiting_on=owner` 状态，
且 `runtime-state/execution.json` 记录的当前 step 保持为该 incomplete 结果所属的 step（不前进）；
人工决定续跑后，重新派发的是该**当前** step 而非下一个 step，已完成的 step 结果不重跑。

**R11 `aihub#777`** — 缺陷：某次执行环境的工具链（例如构建所需的语言工具链）缺失，导致所有
依赖该工具链的构建/验证命令系统性失败，且没有在派发前被发现，直到执行预算耗尽仍未闭合。
真实来源：`pf_get_work_item(aihub#777)` → `wi_H5jmAAAD`，goal 逐字为「verification step 沙箱 worker 缺 go：verify_command 执行器构造的 worker 环境丢掉 Go toolchain，导致所有 pin 了 go 构建命令的 verification step 系统性 provider_error、retry 预算被烧尽、episode 无法闭合（#774/#769 同夜双实证）」。
git 考证（本仓 git 历史）：`--grep='#777'` 无命中；缺陷落点可机械定位：verify_command 沙箱的
worker 环境构造段（`internal/modelruntime/workerext/submit_result.go`，由 #747 的 554be69/9cb3ecc
引入：`--clearenv` + 显式 PATH/HOME/GOCACHE/GOPROXY 构造）正是「worker 环境丢 Go toolchain」
的所在；goal 自述的 #774/#769 同夜事件中 #774 有 commit（a244141/d1a4f09）可考；本仓无 #777 修复
commit。

vNext：v1 明确不做任何 Capsule 级工具链闭包物化——工具链是否可用，是宿主机全局环境的一部分，
落在「可信内部仓库、宿主机原生执行」这一威胁模型边界内（`00-architecture.md#§3.7`），vNext 不为此声明或维护
任何隔离专用目录或机制；`RunCommand`（`03-execution-capsule.md#§5.1`）在宿主机缺少命令声明的可执行文件时直接
返回 `CAP-401 RUNNER_UNAVAILABLE`，在**首次尝试执行该命令时**即可发现，不存在「静默跳过后判定
通过、直到预算耗尽才发现」的路径。
机械反例断言：在缺少目标语言工具链的宿主机上派发依赖该工具链的 verification step，`RunCommand` 立即
返回 `CAP-401`，该 step 不被判定为通过；同一命令在已正确安装工具链的 Linux 与 macOS 宿主机上
均成功执行，行为一致（`03` INV-12：两平台是同一 Native runner 形态，不需要分别验证）。

**R12 `aihub#778`** — 缺陷：某个基于评审意见的修复授权，其具体理由内容没有实际到达负责实现
修复的一方，导致修复按照过时的计划重新实现，评审提出的具体问题被丢失。
真实来源：`pf_get_work_item(aihub#778)` → `wi_Ms3vU8OW`，goal 逐字为「Repair-episode producer ignores the repair authorization reason: review-fix deltas never reach the code-change worker, producers re-implement the stale plan instead (pi#47 evidence)」。
git 考证（本仓 git 历史）：`--grep='#778'` 无命中；关键词旁证：c7db8d5/afedb6c（aihub#764，
2026-09-23）对 supersede 与 engine repair-driving 做了端到端字段验证（episode 各 role 经真实
派发面驱动），但编号为 #764，不冒充 #778 的修复；本仓无 #778 commit。

vNext：gate FAIL 触发的修复路径就是有界 rewind 本身，判定全文作为 rewind 输入的结构化组成部分
随 `on_fail.include_verdict` 直接送达被重派 step 的输入（`01-workflow-skills.md#§6.2`）；不存在可被绕过或遗漏的
旁路修复通道。
机械反例断言：gate FAIL 触发 rewind 后，被重新派发的 step 收到的输入参数中包含该次 FAIL 判定的完整
内容（结构断言，检查输入字段而非行为观察）；对代码路径的审计中不存在能让这份判定内容不随
输入到达的第二条修复路径。

**R13 `aihub#780`** — 缺陷：review 类 gate 的重试轮次没有上限约束，可能无限循环，且缺乏按
深度分级、限定复核轮次、超限后转人工授权的机制。
真实来源：`pf_get_work_item(aihub#780)` → `wi_G5yPkKPy`，goal 逐字为「review 闸两化改造：①深度分级 light/standard/deep/challenge（按 wi_type 默认档+升档覆盖，challenge 双 reviewer 异构交叉）②二轮制——第二轮限定范围 recheck（prior_blockers+增量 delta，不翻案已放行区域），per-gate 轮次上限 2，第三轮起升级 owner 人工授权，编码 owner 2026-06-10 裁决（裁决记录 id 从略）」。
git 考证（本仓 git 历史）：`--grep='#780'` 与 `--grep='轮次'`/`--grep='challenge'` 均无两化改造的
实现 commit——该工作项是改造提案，本仓无实现；vNext 的有界 rewind（`01-workflow-skills.md#§6.2` 的
`max_per_step`/`max_total` 与 `WF-101`）是其替代性收敛。

vNext：有界 rewind 直接给出上限（每 step 默认上限 `max_per_step`、整体默认上限 `max_total`，
均在 `01-workflow-skills.md#§6.2` 定义具体缺省值）；达到上限即进入可观测的终态（`WF-101`），落盘在 Capsule 的
`runtime-state/execution.json.workflow_status=failed_rewind_exhausted`（`03-execution-capsule.md#§4.2`）。Core 侧不
记录 `WF-101` 这一 workflow 错误码本身——它只接收该终态映射出的业务信号：对应 WI 转为
`blocked`/`waiting_on=owner`（`02-aihub-core.md#§2.5`/§6.5），具体是哪一种 workflow 终态导致，Core 不持久化
（`02` INV-2c）；后续如需人工升级，是 WI 层面的新决策，而不是流程内新增的第三条路径。
机械反例断言：对同一 step 的重试需求超过 `max_per_step` 上限时不再派发新的重试，而是在
`runtime-state/execution.json` 中产出 `failed_rewind_exhausted`（`WF-101`）终态，且对应 WI 在
Core 侧转为 `blocked`/`waiting_on=owner`；Core 事件流中不出现 `WF-101` 专属事件类型；重试次数
统计（M9）可从 read-through（`02-aihub-core.md#§5.2`）机械读取而非估算。

### 10.3 回归断言的机械复核 <!-- §10.3 -->

**回归断言宿主位置的裁定（修复 B14 OQ7）**：13 条机械反例断言均作为本仓库 CI 的一个独立测试包
`internal/pilot/regression/`的用例实现（每条 R1–R13 对应一个测试函数 `TestRegression_R<n>_aihubNNN`），
并在每次 CI 运行中与单元/集成测试同批执行，**不**另建独立回归目录或分开调度——因为这些断言
本质上是对 vNext 核心不变量（INV-9、INV-D3/D4 等）的回归保护，与日常开发回归同等重要，
不应因存放于单独目录而被日常 CI 跳过。Pilot 运行期间的指标采集（§9.2）与本包测试无直接依赖
关系，Pilot 是运行时观测，本包是构建时断言，两者互补。

13 条断言全部映射到可执行测试类别（模型审计 / 接口审计 / 故障注入）三者之一；Pilot 期间由
§9.2 定义的指标持续监测对应阈值，任一断言被违反即视为该编号对应设计假设的失效，需回到相应
权威文档重新设计，而不是在本文补丁式追加例外说明。

**交叉文档销点机械校验**（修复 B12）：CI 在本注册表相关变更的每次提交上运行
`python3 docs/design/polyforge-vnext/tools/check-xrefs.py`（无外部依赖，只读本目录下八份
`.md`），非零退出则 CI 失败并逐条打印悬空引用；该脚本是 `00-architecture.md#§10` 声明的标准
本实现，不存在另一份独立实现。

## 11. 迁移与 rollout <!-- §11 -->

> **ARCHIVED — 本节描述或依赖已删除的机制（Interactive/Batch/Drain）；do not build from this.** 现行设计以 [v2-converge.md](../v2-converge.md) 与代码为准。

- rollout 顺序（权威，`00-architecture.md#§11` 总则的展开，按顺序描述，不绑定具体日历日期）：`aihub` 装机
  （`02`）→ Memory 面就绪（`06`）→ 参考 bundle 就位（checkout 内 `workflows/<bundle-name>/`，无注册面，`01-workflow-skills.md#§2.1`）→ 工作机 Daemon + Capsule 就绪（`04`/
  `03`，Linux 与 macOS 同步落地，无分平台阶段差，`03-execution-capsule.md#§11`）→ 同步域联调（`05`）→ Daemon 三种
  Job 执行模式（Interactive/Batch/Drain）与优雅停机联调（`00-architecture.md#§3.4`/§3.5、`04-local-runtime.md#§6`）→ Pilot 启动
  （§9.1）。每一步完成的判据是该步所属权威文档 §6/§9 定义的自检通过，不是某个预定的日期。
- 一期从无到有，**不迁移执行状态**（workflow-v2 执行状态不迁移，`00-architecture.md#§1.2`）；
  **Memory 存量是唯一迁移项**（`06-memory-search.md#§10`：一次性导出为 Markdown 权威文件，重启 aihub 让启动对账全量重建索引）；
  升级/回滚序列见 §6.3/§6.4。
- Pilot 达标（30 天或 40 张 WI 观测期内未触发 §9.3 任一门槛，且 Interactive/Batch/Drain 三种
  模式与至少一次优雅停机均已被实际触发，§9.1）→ **切换**：生产工作整体迁往 vNext AIHub
  （新仓部署物），旧 aihub 系统进入冻结/退役流程（并存与切换的权威边界见 §2.1），随后团队全量
  推广；触发任一否决门槛 → 按该门槛对应
  的触发动作回滚或重设计对应域，不进入全量推广（并存期内旧系统不受影响，继续承担生产）。

## 12. 未决问题 <!-- §12 -->

1. **Pilot 规模阈值的弹性**：30 天 / 40 张 WI 是否足以暴露 M3 级别的低频缺陷，是 Pilot 的设计
   参数，可能需要按观测前两周的数据做一次校准；门槛本身的判定公式（§9.3）不因校准改变。
2. **TLS 化时点**：一期内网 HTTP 直连的信任边界评估，与 `05-sync-revision-recovery.md#§12` 的同一未决项共享同一开放状态，
   部署侧不预设时间表。
3. **M5 机械信号的覆盖面**（§9.2a）：INV-D4 定义的三条件判据只能拦截「完全未产出却声称完成」
   的最小谎报，不能识别「产出了内容但与声明意图不符」的语义级谎报；后者需要更强的命令-产物
   关联性分析或语义审查，一期不实现，保持开放。
4. **M4 判据的命令级语义是否需要收紧**（§9.2a）：当前判据已依赖逐命令结构化记录（
   `output.commands` 的 `{argv, exit_code, duration_ms}`，`03-execution-capsule.md#§4.2`），不再依赖「`output`/
   `attempts` 是否非空」的粗粒度口径；仍开放的是**命令级结果的语义**是否纳入判定（例如
   `exit_code != 0` 的命令是否计为有效证据、命令调用与产物 diff 的关联性分析）——若 Pilot 观测
   发现这一粒度不足以支撑 M4/M5 的判定精度，是否在 `03` 扩展命令审计字段（仍不使用可被
   subagent 写入的 `logs/` 作为证据源，INV-D3），是留给 `03` 的后续设计问题，本文不预先假设其存在。
5. **发版钩子的渠道/晋升细则**：发版已裁定为 release step 钩子（§1.1；`01-workflow-skills.md#§4.2`），
   平台侧无待设计项；各项目 release step 内部的渠道与晋升门槛（alpha→stable 等）属项目自填内容，
   平台不预设。
6. **向量语义检索层（非 v1 演进项，B02 裁定）**：`06-memory-search.md#§1.2` 的最终裁定——v1 仅文本
   检索（FTS5 trigram）；语义检索为非 v1 演进项（v1 不实现、不部署、不验收）；v1 的 M6/`PILOT-V6`（§9.2/§9.3）只验收
   FTS5 trigram 写入→可检索延迟（p99 ≤ 5s），**不设向量层延迟验收口径**。向量层的部署、模型
   选型（`06-memory-search.md#§11` 第 1 项）与是否为其补设延迟门槛，属 v1 之后的演进决策，不在
   本 Pilot 否决门槛内。
