# v2 收敛设计：减法、监督重构、watch 与身份模型

> 状态：**r3，owner 裁决已全部纳入，待审阅**（2026-10-12）。
> 来源：2026-10-12 owner 与 review 会话的完整讨论；r2 吸收了一次独立深度审查（25 个漏洞 + 历史回归对照，附录 A/B），owner 已同意其 18 条修补默认值。
> 本文取代的范围：`archive/04-local-runtime.md#§6.2` 监督车道、`archive/v1.5-session-model.md` 中 watch/adoption 相关描述、`skills/polyforge/scripts/watch.sh`。
> 落地进度：本文第 2–8 节的设计均已落地（含监督重写与 `pf watch` 进展字段）；见第 8 节状态列、第 10 节映射与文末「已知缺口」。
> 落地方式：**快速落地**——直接切换到新设计，不走"新旧并行观察 → 再删旧"的分阶段迁移（owner 2026-10-12）。

---

## 0. 定位与规则（已定） <!-- §0 -->

- polyforge 是**团队级多 agent 系统**，将开源；保持克制，不大量加功能。
- 核心三根支柱：**任务**（aihub WI）、**记忆**（项目演进与知识沉淀）、**workflow**（团队统一的任务处理规范）——目标是团队的 agent 在同一套体系下像一个 agent 一样运转。
- 新功能冻结，先做减法；大改现有逻辑先问 owner；**心智模型越简单越好**（以"概念数减少"衡量方案）。
- **按步骤选模型**是核心能力，保留；**按步骤选 harness**（pi / Claude Code / Codex）要做，排在减法之后。
- **信任模型（owner 裁定 2026-10-14，取代 2026-10-12 的"agent 不可信"）**：polyforge 只在团队内部使用；harness（pi / Claude Code）由人事先登录，daemon 只调用 CLI，**从不管理登录凭证**；不做重型隔离（独立 uid / bwrap 等）——**#135 冻结**，设计稿保留备查。仍然要防的是**事故**而非恶意：agent 推到 GitHub main（2026-10-13 #149 事故）、发布未经评审的提交、step agent 代答自己的人工 gate 问题（#122）。本文中以"agent 不可信"为理由的机制，现在一律按**事故护栏**理解：已建成并付出成本的护栏保留（#122/V-1 的 UDS step 进程拒绝、#156 的 ls-tree 检查与无 hook 推送、#158 的临时仓库 carrier 推送、V4 取证门禁、固定模板唤醒），但不再扩展为对抗性防御。step agent 按设计写 capsule（其结果信封经 `pf result submit` 由 daemon 写入 `runtime-state/pending-results/`，polyforge#214），这不是缺陷。

## 1. 业务领域划分 <!-- §1 -->

| 类型 | 领域 | 主要内容 |
|---|---|---|
| 主线 | 安装与升级 | install.sh（安装与升级均重跑它）、发版、aihub 部署 |
| 主线 | 团队与权限 | 用户、key、项目、成员角色、agent 信任边界 |
| 主线 | 任务管理与生命周期 | WI 状态机、依赖、就绪队列、领取（session 锁）、blocked 出口 |
| 主线 | 记忆 | 存储与检索、recall/remember、领取时快照（`runtime-state/recalled-memories.json`：以 goal + labels 为查询、`match=any` 排序检索本项目记忆取前 10 条；driver 以"非约束性历史"注入每个 step 提示，有界；无快照则不加该节） |
| 主线 | workflow 定义 | bundle、step、gate/rewind、按步骤选模型（将来：harness） |
| 主线 | 执行 | daemon、capsule、driver、并发、工作区映射 |
| 主线 | 交付 | ship → git 分支 / PR (removed by polyforge#166: delivery is the workflows' deliver step. The driver checks the deliver step's recorded commands and the delivered history after the fact and escalates violations to a human; prevention rests on the step contract and the machine's local pre-push hook.) |
| 横切 | 人工协同 | 提问/回答、暂停/继续、进展可见（`pf watch`） |
| 横切 | 可靠性 | 监督（判死）、恢复、session 隔离、接管 |
| 横切 | 运维 | doctor、backup、ops status、事件日志 |

## 2. 执行与监督 <!-- §2 -->

### 2.1 分工（已定） <!-- §2.1 -->

| 组件 | 职责 | 不做什么 |
|---|---|---|
| **driver** | 执行 WI：按步骤派发 step 进程、收结果文件、推进 `execution.json`、按死因处理、维护失败计数 | 不判断"活着但不推进" |
| **supervisor**（原 observe 组件，改名以免与命令混淆） | 观察执行动态、**判断 step 是否已死**、判断 **driver 是否卡在步骤之间**、M4 僵尸会话重验、记录事件、推送进度到 aihub、提供查询接口 | 不推进执行 |
| **恢复引擎**（runtime） | driver 自身异常退出后的重拉；保留有界失败语义（连续多次恢复失败 → blocked；唯一的失败账本是“连续恢复失败”计数器，步骤结果被接受即清零） | 不判 step 死活 |
| **`pf watch`**（CLI） | 面向人的进展与决策通知 | 不做任何判断 |

**例外（已定）**：step 进程**退出却没交结果**，由 driver 直接处理（它在等结果时就能看到），再告知 supervisor。

### 2.2 driver ↔ supervisor 契约 <!-- §2.2 -->

二者同在 `pf daemon` 进程内。

**通道规则**
- 实时信号走进程内通道：**每个 WI 一条无界队列，发送方永不阻塞**（supervisor 的 select 循环与 runtime 的串行处理都不得被投递卡住）；**事实与判定不得丢弃**。
- driver 消费判定时**不得在跨 WI 的锁下做网络 / git 等长操作**（避免一个 WI 的推送卡住所有 WI）；runtime 只按 WI 加锁跨网络，全局的任务表锁持有期间不做任何 I/O。
- harness 的海量输出**只保留最新状态**（合并，不排队）。
- 持久化：`execution.json` 是**唯一权威**（进度、待答问题、失败计数、放宽后的阈值、终态）；事件日志是**补充记录**——driver 事件也写入（替代只打 stderr 的 `stdLogger`），每条事件带**跨重启单调递增 id**（不再每次重启从 1 重编），每个 WI 一个文件；磁盘满时事件追加失败只降级为日志告警，权威仍在 `execution.json`。
- supervisor 不再轮询 `execution.json` 作为主路径；推送进度到 aihub 改由事件触发，**失败按指数退避重试**（F18/F22 教训：曾 185 次推送风暴；见 §14.1）。
- 转录同步（F3）以最近一次 `step_started.transcript_path` 为源，commit 前同步。（现状：job 级收尾前的活转录同步**保留**——C11 裁定，polyforge#150；实现为 `runtime/pitranscript.go` 的 `syncLiveTranscript`（commit 24cde0d 恢复）：job 在 pause / bounded-fail / seal 等收尾路径上结束时，当前派发的 step 未走到提交边界，其活转录若不在收尾提交前同步就会丢失——收尾前同步保证终态前转录落盘。）

**driver → supervisor（事实）**

| 事件 | 字段 |
|---|---|
| `step_started` | step, spawn_n, model, harness, pid, transcript_path, started_at, 本步阈值（含已放宽值，见 2.4） |
| `step_waiting_human` / `step_answered` | step, spawn_n, question_id |
| `step_ended` | step, spawn_n, outcome（正常交结果 / 退出未交结果）, exit_code, 最后一条错误信号 |

**supervisor → driver（判定）**

| 事件 | 字段 |
|---|---|
| `step_dead` | step, spawn_n, cause（见 2.3）, evidence |
| `driver_stalled` | 当前所处步骤间隙、持续时长 |

**围栏与幂等（r2 补）**
- driver 只接受 `spawn_n == 当前派发` 的判定，过期判定丢弃。
- **每次派发至多一次判定、至多一次失败记录**：driver 收到判定后该派发进入 terminating 状态，之后这次派发的进程退出、后续判定一律忽略；supervisor 对某次派发发出判定后静默，直到 `step_ended`。
- supervisor 的状态**按每次派发一份**：`step_started` 新建，`step_ended` 丢弃（根治 #124 turn_duration 跨步不清零误杀）；**步骤级累计量**（总时长 T 的已用时间）由 driver 持久化在 `execution.json`，随 `step_started` 下发。

### 2.3 判死规则（supervisor）与死因处理（driver） <!-- §2.3 -->

supervisor 对每次派发维护：派发时刻、最后一次"真实输出"时间、当前是否在执行工具（哪个、何时开始）、是否在等模型回复（何时开始）、本次派发的"工具时间 / 等模型时间"。

**"真实输出"**：只有工作类事件算活着；UI 刷新类不算（F15 教训）。

**判死规则（多条同时命中时按编号优先）**

| # | 规则 | 条件 | 死因 cause |
|---|---|---|---|
| ⓪ | 启动后无信号 | 派发后 X 内没有任何工作类信号（harness 卡在启动、stdout 被孙进程持有等） | `harness_silent` |
| ① | 本轮结束后停住 | harness 发出"本轮结束"，宽限 1 分钟仍无结果文件：原因出错 → `model_error`（换候选；但该轮错误文本先过与退出路径相同的 D7 分类，见下表与 polyforge#162）；正常结束 → `ended_without_result`（同模型重试） | `model_error` / `ended_without_result` |
| ② | 等模型太久 | 在等模型回复，超过 X 无任何输出 | `model_hung` |
| ③ | 工具太久 | 某工具超过 Y 未结束 | `tool_hung` |
| ④ | 总时长超 | 本步**累计**运行超过 T | `step_timeout_long_task`（主要耗在工具上）或 `step_timeout_slow_model`（主要耗在等模型 / 单次回复异常慢） |
| — | 步骤之间卡住 | driver 不在任何 step、也不在等人，超过 N 分钟（如推送 git 子进程卡死仍持锁） | `driver_stalled` |

等人回答期间没有 step 进程，**不计时**。

**"退出未交结果"的细分（driver 自己发现，r2 补）**

| 细分 | 判据 | 处理 |
|---|---|---|
| 模型类 | 错误文本命中额度·鉴权特征（401/403/鉴权失败/api key、quota / insufficient / billing / credit；中文：余额不足 / 欠费 / 额度·配额用尽 / 认证失败 / 模型·接口无权限 等）或模型名不可解析，或退出前最后信号为"本轮出错"且文本不属限流类/上游瞬时类。规则 ① 的判决（进程**存活**的出错轮次，pi rpc 出错后不退出）同样把该轮错误文本送入 `classifyWaitFailure`（polyforge#162），不命中限流 / 上游瞬时时仍记 `model_error` 换模型 | 同 `model_error`：换模型（F4 教训：模型 pin 失败必须推进候选）。额度·鉴权文本最先判：同时带 429 或连接中断特征（"429 … exceeded your current quota"、"insufficient balance … connection reset"）仍立即换模型 |
| 限流类（polyforge#147 D7） | 错误文本命中瞬时限流特征（429 / rate limit / too many requests / overloaded；中文：并发数过高 / 请求过于频繁 / 请求过多 / 限流 / 繁忙 等），且不命中额度·鉴权特征 | `rate_limited`：原模型退避重试，不推进候选指针、不计失败计数 |
| 上游瞬时类（wi_seWTgHeKtaa8） | harness 报告的上游返回损坏或上游流中断：工具调用参数 / JSON 不完整·截断·畸形、空回复（0 token）、上游连接 reset / refused / closed / EOF、上游流在完成前结束（"Upstream response stream ended before completion"，polyforge#202；须带 upstream / provider / stream / connection 主语，裸的 "the model ended the turn" 不算）；网关回复同时点名上游错误**且**明确请求重试（`400: {"message":"The model request failed due to an upstream error. Please retry.",…}`，polyforge#227；按两者合取判定，不看 400 状态码或 invalid_request_error，只有其一仍属模型类）；不命中额度·鉴权与限流特征 | `upstream_transient`：原模型按 D7 退避重试，**最多 2 次**，第 3 次即归档为模型类（`exit_model_class`）并换模型；不计失败计数。裸的 "invalid …"（工具名、请求参数）、上下文超长、driver 自身的本地 I/O 错误（如 "read transcript: unexpected EOF"）都**不**属此类 |

**文本分类顺序**（`driver/supervision.go` `classifyWaitFailure`）：机器类 → 正常结束一轮后干净退出（`ended_without_result`）→ 额度·鉴权（换模型）→ 限流（同模型）→ 上游瞬时（同模型，界 2）→ 模型名不可解析（换模型）→ 本轮出错（模型类）→ 其余。非进程退出（driver 自身失败）不做上游瞬时匹配。中文 provider 文本同样覆盖（polyforge#155）：额度·鉴权类含 余额不足 / 欠费 / 额度·配额 用尽·不足·超出 / 账户…停用 / 鉴权失败 / 认证失败 / 模型·接口·API·服务 无权限 等（裸"权限不足"不算，本地 权限不够 / 权限被拒绝 / 拒绝访问 归机器类），限流类含 并发数过高·超限 / 并发超额 / 请求过于频繁 / 请求频率 / 请求过多 / 限流 / 速率限制 / 繁忙 / 负载过高；均为多字短语（CJK 下 `(?i)`、`\b` 无效，不用单字），两者同时出现仍额度·鉴权优先（换模型）。
| 机器类 | driver 自身 I/O 错误（ENOSPC、EACCES 等）或翻译器上报的宿主错误 | 机器问题 |
| 其余 | — | 同模型重试 |

**driver 按死因处理（已定）**

| 死因 | 处理 |
|---|---|
| 退出未交结果（其余） | 同模型重试 |
| `harness_silent` | 同模型重试 |
| `ended_without_result` | 同模型重试；重试的 prompt 明确告知"上一轮已结束但没有写结果信封"。进程在正常结束一轮后以退出码 0 自行退出且无结果，也归此类（被杀 / 崩溃仍是退出其余） |
| `model_error`、`model_hung`、退出（模型类） | 换下一个候选模型。规则 ① 的 `model_error`（存活进程的出错轮次）先把轮次错误文本过与退出路径相同的 D7 分类（polyforge#162）：只有限流类 / 上游瞬时类文本把失败记录的死因改为 `rate_limited` / `upstream_transient`（原模型重试、退避计入 T、上游瞬时界 2）；机器类特征及其余文本一律保持 `model_error`（换模型）。`step_dead` / supervisor 判决的 cause 仍是 `model_error`，失败记录（`step_failed`、attempt）才带 `rate_limited` / `upstream_transient`——有意如此，由 `test/v2_live_error_turn_d7_test.go` 固定 |
| `step_timeout_slow_model` | **放宽 T（翻倍，写入 `execution.json`）后换下一个候选模型**（polyforge#147 D6）：新候选在**放宽后的完整额度**下运行，累计耗时连续结转但严格低于翻倍后的界——绝不在启动瞬间即被判超时 |
| `rate_limited`（429 / rate limit / too many requests / overloaded / 中文限流文本；额度/账单/权限类不在此列，归模型类换模型） | **不换模型**：原模型退避重试（polyforge#147 D7），退避 1s 起逐次翻倍、单次上限 300s；**退避等待计入该步累计 T**；**不另设重试次数上限**，唯一的界是该步累计 T（T 耗尽即走既有超时处理）；**不放宽 T**（只有超时类死因放宽）；不计入失败计数 |
| `upstream_transient`（上游返回损坏 / 上游流中断，见上表） | **不换模型**：原模型按 D7 同一退避重试（1s 起翻倍、单次上限 300s，**退避计入该步累计 T**），不推进候选指针、不计失败计数；**连续 2 次后**第 3 次归档为 `exit_model_class` → 换下一个候选（界只由 attempts 归档里尾部连续的 `upstream_transient` 计数，崩溃恢复后的 driver 读到同样的剩余次数） |
| `tool_hung` | 同模型重试，并**放宽 Y**（同 T 的放宽规则） |
| `step_timeout_long_task` | 同模型重试（**不换模型**）并放宽 T（翻倍并持久化）；再超时 → 同样再翻倍 T 并持久化，然后 blocked（人工"继续"在新放宽的界下运行，不会一启动就超时） |
| 机器问题 | blocked，通知人，不换模型 |
| daemon 重启 | 同模型重新派发，**不计失败** |
| `driver_stalled` | 终止当前 git/推送子进程，driver 重拉（计入 driver 异常退出界） |
| driver 自身异常退出 | 恢复引擎重拉；**连续多次恢复失败（唯一账本：连续恢复失败计数器）→ blocked**——防止"崩溃 → 重启不计失败 → 再崩溃"无界循环（#97 教训） |
| 同一步失败计数达上限（默认 3） | blocked，通知人 |

**候选用尽**：指针停在末尾，后续"换模型"按同模型重试计数；决策事件注明"候选已用尽"。

**重试形态**：第一次重试接着原对话（resume 原 transcript）；第二次起重开对话（fresh），把上次 transcript 作为只读参考输入——避免上下文超限类错误在任何模型上都重复失败（F13 一族）。

**失败计数（r2 补）**
- 存放：`execution.json` 该 step 的字段（attempts 记录带 cause 子类）。
- 计入：上表除"daemon 重启"、"限流类 `rate_limited`"（polyforge#147 D7：退避重试只受 T 约束）与界内的"上游瞬时类 `upstream_transient`"外的所有死因。**信封被拒不计入**——polyforge#147 C4 起它不再走"进程内重试耗尽后重启 driver"的升级车道：进程内重试 3 次仍不合格 → 该步直接 blocked，原因码 `result_invalid`，不重启 driver、不路由 `on_fail`（rewind 只留给评审 FAIL 裁决）。
- 清零：gate rewind 回到该步、换到新候选模型、人工"继续"。
- daemon 重启触发的中断**不归档为失败、不推进候选指针**；一般地，被中断的 attempt 永不推进候选指针。（现状：`FaultClass` 分类层已删除，改为**死因规则**——只有模型类死因 `model_error`、`model_hung`、`step_timeout_slow_model`、`exit_model_class` 推进候选指针；指针在 gate_fail rewind 时重置；见 `driver/candidates.go`、`driver/supervision.go`。事件体与 attempts 记录带 `failure_cause`，不再有 `fault_class`。）
- （现状：候选列表五级优先，高到低：`pf submit --models a,b,c` 的手工列表（仅由本 daemon 保存、按 WI、作用于该 WI 的每一步；随 submit 队列落盘，认领时写入 daemon 数据目录的 `manual-models.json`（不进 capsule），本机 daemon 重启、恢复、`pf resume` 重新认领均沿用，WI 终态（wrapped/failed/canceled）时删除；在另一台机器上重新认领则没有该列表，使用那台机器自己的设置）> 工作区 yaml 文件的 `steps.<project>.<step-id>.models`（polyforge#169：按步骤 id 配置，顶层键 `steps:` 取代 `models:`，旧 `models:` 不兼容、加载即拒绝并给迁移提示，同名步骤跨 bundle 共用一个键；非 `default` 的键必须是已安装 workflows 中的步骤 id，否则 DAEMON-414 拒绝认领、`pf doctor` 报告）> bundle 步骤自带 `model_candidates` > `steps.<project>.default.models` > bundle `defaults.model_candidates`（polyforge#164：步骤自带列表高于项目 default，项目 default 只兜底未自选模型的步骤）；推进/重置语义不变。见 `workspace/compose.go`。）
- 失败处理按死因表（上节）执行；`PolicyFor` / provider·host·operator·schema 分类已随 FaultClass 层删除（`driver/supervision.go`）。

### 2.4 阈值（已定） <!-- §2.4 -->

- X（⓪②）、Y（③）、T（④）、N（driver_stalled）**全局默认**；step 级只能覆盖 T（见下条）。
- **T 的 step 级覆盖复用现有 `budget.wall_minutes`**，但它是**估计值而非硬上限**（polyforge#164：≤30 分钟的估计在下式下不起作用，交付 bundle 已全部删除，只剩 release bundle 的 `release: 45` → T=135）：声明了 `wall_minutes` 的 step，总时长界 **T = max(3 × wall_minutes, 全局 `--step-timeout` 默认)**；未声明则就是全局默认。不新增键；X、Y 没有 step 级覆盖（polyforge#147 C5 删除了 `silence_minutes` / `tool_minutes`，旧 bundle 中残留的这两个键被静默忽略）。覆盖值随 bundle 在领取时钉住，对之后领取的 WI 生效。
- X 默认按 harness 翻译器声明的**输出粒度**取：逐字粒度（思考时也有输出）可短（例 10 分钟）；整条粒度或模型不返回思考内容时放长（例 30 分钟）。"模型不返回思考内容"在模型配置中声明。
- Y 默认 30 分钟（#83 教训）；T 默认 60–90 分钟（#117 教训）；N 默认 10 分钟。
- **放宽**：每次放宽翻倍（放宽后的界 = 放宽前界 × 2），写入 `execution.json`（`steps.<id>.supervision.relaxed_wall_ms` / `relaxed_tool_ms`），重启后沿用。**只有超时类死因放宽 T**（`step_timeout_slow_model` 与 `step_timeout_long_task` 每次处置都放宽一次，第二次 `step_timeout_long_task` 也先翻倍再 blocked）；换模型、人工继续、限流退避一律不放宽（polyforge#147 D6）。
- T 按**整个步骤累计**（跨该步的多次派发、跨换模型、跨人工继续连续累计，polyforge#147 D6 起不再有任何"重开窗口"的 rebase——`runtime_base_ms` 已删）；计入 agent 实际运行时间与**限流退避的等待时间**（D7：同模型重试只受 T 约束，owner 裁定），等人回答（needs_input）的挂钟时间不计入。**被打断的派发（D6，owner 裁定）**：以结果或失败结束的派发在同一次写入里记账；**操作员暂停**取消的派发不计入任何挂钟时间（无论之后以重新领取还是 live-holder 的 `operator resume` 继续，暂停期间也不计；重新领取时若当前派发未结算，driver 先写一次 `claim_reentry` 把 `step_started_at` 重新打点，之后即便系统重拉也只从领取时刻起算）；**系统**在挂起 / 停滞 / 死亡后重拉的派发**计入**其运行时间——由继任 driver 的 interrupted 归档写入记账，从该派发开始（或该派发上一次归档）到归档写入为止（其间恢复车道的处理时间一并计入），因此挂起→重拉循环仍会达到 T。daemon 重启（`daemon_restart`）与操作员 attach 属生命周期事件，同暂停一样不计（停机时间不是运行时间）；判定用 runtime 的 `LifecycleCause`（与连续恢复失败预算的豁免表同一张），在 daemon 的 `interruptedFromCause`（`daemon/daemon.go`）处决定，被取消的 driver 自身不写任何东西。
- **rewind 重跑清零**：gate FAIL rewind 回到某步时，该步的累计 T 归零（旧窗口的记账随归档 attempt 留存）；发起 rewind 的 gate 步自身（不归档、保持 in_progress）的累计 T 同样归零，重入在新窗口下评审。

### 2.5 harness 翻译器 <!-- §2.5 -->

每个 harness 一个翻译器，把原生事件翻成标准信号：活着、在等模型、模型在输出（思考/正文）、工具开始（名称+参数）、工具结束、本轮结束（成功/出错）、**进程退出（退出码、最后一条错误）**。supervisor 只认标准信号。**这也是"按步骤选 harness"的接入点**。

| harness | 对接方式 | 思考时有输出 | 核实来源 |
|---|---|---|---|
| pi | `--mode rpc` | ✅ `thinking_delta` | 本机源码 `agent-loop.js` |
| Claude Code | `-p --output-format stream-json --verbose --include-partial-messages`（由 driver 一律加上） | ✅ | 2.1.296 二进制事件名 |
| Codex | **`codex app-server`**（JSON-RPC），**不用 `exec --json`** | ✅ `item/reasoning/textDelta` 等 | 0.162.1 源码；`exec --json` 会过滤掉回复/思考的开始事件且无增量 |

第一版只实现 pi 翻译器；Claude Code / Codex 随"按步骤选 harness"落地，接入时按当时最新版本复核协议。

### 2.6 结果信封、`context_policy` 与 `handoff`（polyforge#147 定稿） <!-- §2.6 -->

**结果信封 = 最小契约**：step agent 用 `pf result submit` 交付**一个** JSON 对象，这是提示词里唯一的交付通道（polyforge#214；不再描述手写落盘，helper 交付失败时 agent 报告后结束本轮，按"进程退出未交结果"走既有恢复车道，绝不改为手写）。agent 只给 `outcome` + `output`，`needs_input` 时另带 `question_id` + `question`（欠取证的 step 另带 `command_evidence`）；daemon 的 `POST /step/result` 像 `/step/memories` 一样按调用方进程树认定 step（只有本 daemon 的 step 进程可达），调用方自带的 `wi_id` / `step_id` / `spawn_n` 与之不符即拒，轻量形状缺陷（非 JSON、outcome 不在闭集、`output` 不是对象、needs_input 缺合法问题对）也拒，拒绝时不写任何文件；通过后由 daemon 盖上派发身份围栏（`wi_id` / `step_id` / `spawn_n`）与 `submitted_at`，原子写到约定位置 `runtime-state/pending-results/<spawn_n>.json`（位置与命名不变），调用方 cwd 与 harness 沙箱都影响不到落点。driver 收取时 `wi_id` / `step_id` 非空且不是本次派发的即按身份不符拒收。`protocol_version`、`result_id`、`artifacts` 哈希清单、`handoff_ref` 摘要引用**全部删除**，driver 不再校验它们，缺这些旧字段 MUST NOT 判不合格；result_id 宽容等补全层一并删除，不留兼容分支。**V4 命令取证门禁保留**（owner 裁定；按 2026-10-14 信任模型它是**事故护栏**——防 agent 漏跑或误报验证命令，取证检查留在 driver）：欠取证的 step（声明了 `requires_command_evidence: true`；按 step id `verification` 的隐式规则已由 2026-10-16 裁定 G10 删除）的 completed 结果另带 `command_evidence: [{argv, exit_code, duration_ms}]`——至少一条、argv 非空、`duration_ms > 0`，否则信封被拒（进程内重试车道）；任意 step 的 `command_evidence` 若不是列表、或含非对象 / 无法解析的条目，信封被拒（同一车道，拒绝原因点名该条目），不做部分保留；被接受的取证由 driver 写入 `output.commands`（B10，step 自写的同名键被覆盖）。其余 step 的信封不带该字段。**已提交测试文件门禁**（polyforge#156，任意 step）：completed 结果的 `output.test_files` 非空时，每条路径必须是 repo 相对路径、至少存在于一个 task repo，且在其存在的每个 task repo 中都已提交在 HEAD，否则信封被拒（同一进程内重试车道，拒绝原因点名路径）——resume 只按 `refs/pf-task/<name>`（HEAD）恢复 task repo，未提交文件会丢。**声明输出 schema 校验**（polyforge#164 H3）：step 定义文件 frontmatter 的 `output:`（draft-07 JSON Schema，YAML 书写）既注入提示词（放在 step 正文之前，"per the schema above" 成立），也由 driver 机械校验 completed 结果的 `output`（值为 `null` 的可选属性按缺省处理）。release bundle 的 `release`、`deploy` 两步**强制校验**（polyforge#193 所有者裁定；`outputSchemaEnforced`）：不合格即信封被拒（同一进程内重试车道），其 schema 就是发布判定契约（verdict 精确枚举；pass 带 version，release 另带 commit 与至少一条 artifacts_published；fail 带非空 fail_reason；deploy 的 defer 不带 version / artifacts_published）；`defer` 判定在 deploy 之外的任何 step 上都被拒。其余 step 为**只记录模式**：不合格时不拒信封，只记持久事件 `output_schema_violation`（step、schema 路径、截断的校验错误）——用近期真实 result.json 回放约 64% 不合格（agent 此前从未见过 schema）。声明了却加载失败的 schema 记 `output_schema_unloadable` 事件并写 stderr；`release`/`deploy` 上 schema 缺失或加载失败则结果被拒。**轻量重入的 `needs_code_change` 自报**（polyforge#222）：completed 结果的 `needs_code_change` 必须是布尔值，且只有该 step 台账里先有一条轻量回退（最后一条非 `infra_failure` 归档 attempt 为 `gate_fail_rewind_text_only`）的轻量重入才可携带——非布尔值、或无轻量上下文而携带，信封被拒（同一进程内重试车道）；该字段只是结构性修复的自报通道，正常完成不带。**既有失败的边界**（#164 S6）：repro 检查、作者 step 的 `test_files`、bug-fix fix 的 `regression_test` 永远不能标为 `pre_existing`（它们在 base 上失败是设计使然，未修好的 bug 会被当成既有失败放过），pre_existing 检查的 detail 必须带 base sha，且至少有一条 `<repo 相对路径>: <测试名>` 形式的 `pre_existing_failures` 条目（路径按完整路径或目录前缀的路径分段精确比对，不做子串匹配）；bug-fix 的 repro 已复现时，verification 的 pass 必须带一条名为 repro、状态 pass 的检查；违者信封被拒（同一车道）。**已写测试不可改**（polyforge#164 H4）：声明 `test_files` 的 step（e2e-author / repro）被接受时，driver 在其 `output.test_files_commit` 记下各 task repo 的 HEAD（只由 driver 写，step 自写的同名键被丢弃）；之后每个 completed 的 step 都机械比对这些文件在该提交与 HEAD 之间是否一致（等价于 `git diff <author-commit>..HEAD -- <test_files>`，以两次 `git ls-tree -r` 实现，沿用 #156 的加固 git 调用），有差异即信封被拒——除非该 step 当前尝试链里有人工回答（step 认为测试本身错了，必须用 needs_input 提出），此时改动成为新基线（driver 把新的 HEAD 写回前面作者 step 的 `test_files_commit`，故该 step 之后被 rewind 也不丢），并记持久事件 `authored_tests_rebaselined`（列出被改的文件）。**`requires_human_answer` 取值**：`true` = 完成前必须有人工回答（feature-full grill-review）；`if_asked`（polyforge#164 H11，autopilot grill）= 提出的问题只能由人回答，零问题可直接完成，但 driver 记一条持久事件 `human_gate_unasked`，watcher 据此通知 owner。

**`context_policy`（step 键，`inherit`\|`fresh`，缺省 `inherit`）**：声明一个 step 与上游的上下文关系。`inherit` = 本 step 续上游的活：派发时 driver 找**最近一个已 done 且声明 `handoff: true` 的更早 step**，按路径约定 `artifacts/handoff-<step-id>.md` 读出交接正文，原样拼进派发 prompt 的「Handoff from step …」小节；`fresh` = 从零开始，不注入交接正文，要读的上游产物按产物路径约定自取。`gate: true` 的 step 强制 `fresh`——闸只凭自己的输入判，不继承有倾向性的上下文。

**`handoff`（step 键，bool，缺省 false）**：`true` 的 step 必须在收尾前把交接内容写成 `artifacts/handoff-<step-id>.md`。**路径约定就是全部契约**：没有摘要、没有字节数、没有信封字段，driver 不校验交接文件的哈希、不比对清单；交接文件不在时，下游 inherit step 的派发只是少了那一段正文，不构成信封级失败。权威细节见 `01-workflow-skills.md#§4.4a`。

### 2.7 harness 适配层的隔离保证（owner 裁决 A4，2026-10-13） <!-- §2.7 -->

**禁止 step agent 使用 harness 后台任务，是 harness 适配层的责任，不是写进提示词的纪律。** 后台任务会让一个步骤在它的派生进程之外留下活动，监督面（判死、step 结束、派生计数）全部失真；靠"规则里写了不许用"约束的只是诚实的 agent。

pi 适配层的具体机制：**专用 step agent 目录**。daemon 维护 `<harness-root>/step-agent/`（`--harness-root`，生产由 daemon 读 cli.toml `[daemon] harness_root`，缺省 `<data-dir>`；polyforge#171；env 回退已于 2026-10-16 G2 删除），派生每个 step 进程时注入 `PI_CODING_AGENT_DIR=<harness-root>/step-agent`（会话另置于 `PI_CODING_AGENT_SESSION_DIR=<harness-root>/step-agent-sessions/<dashified-capsule-dir>`——每个 capsule 一个目录，pi 把会话平铺写入该目录，实时 transcript 定位器扫描的正是它；polyforge#211）。该目录是 operator agent 目录（`--operator-agent-dir`，生产由 daemon 读 cli.toml `[daemon] operator_agent_dir`，缺省 `~/.pi/agent`；配置了却没有可读的 `auth.json` 即拒绝启动）的**最小快照**——只有 `auth.json`（模型服务凭证）、`models.json`（模型服务配置，保留源文件权限）、`agents/`（子 agent 定义）、`extensions/subagent/`（子 agent **工具**本身），**没有 `settings.json`**，而用户级 package（例如提供后台任务工具的 `npm:pi-background-tasks`）正是在 `settings.json` 里声明的：没有声明就没有加载，后台任务工具对 step agent **机械不可见**，与提示词无关。

- **子 agent 工具如何送达**：它不是 pi 内建工具，而是 operator agent 目录下的扩展 `extensions/subagent/`；pi 只从 `<agent 目录>/extensions/` 自动发现全局扩展（`resource-loader` / `package-manager` 的全局 extensions 根）。所以快照**恰好**拷贝 `extensions/subagent/` 这一个子目录，其余扩展一律不带（例如 `extensions/polyforge` 是交互会话用的），`settings.json` 与 packages 永远不带。
- 拷贝而非链接：快照不跟随 operator 后续改动（包括全局重装），也没有任何符号链接把专用目录变成整个 operator 目录的窗口（树内符号链接跳过不拷）。**每次派生前**只刷新白名单成员（源文件大小+mtime 变化即重拷，源里没了就删）；清除白名单以外的条目**只在 daemon 启动时**做——那时本 daemon 没有存活的 step 进程，不会把文件从运行中的 step 进程脚下抽走。凭证不可读 = 大声拒绝派生，绝不降级运行。
- **凭证限制**：拷贝的 `auth.json` 支持 API-key 条目。OAuth 条目在 pi 刷新令牌之前可用；pi 会把刷新后的令牌写进**副本**，而下一次同步发现副本与 operator 文件不一致，就用 operator 的（旧）条目覆盖它——step 侧的刷新结果会丢失。没有为此设机制：step agent 应使用 API-key 凭证。
- 位置在数据目录下，**绝不在 capsule 内**：capsule 会提交并推送到 aihub，且对 agent 可写。
- `--no-extensions` 仍然**不传**：子 agent 工具就是上面那个扩展，关掉它等于关掉整条 step-agent 派发路径；隔离由目录（只带这一个扩展）而非开关完成。
- **任何未来的 harness 在接入前，必须提供等价的保证**（一个 harness 若无法从启动配置上关掉后台任务族，就不具备接入资格）——这是适配层的准入条件，写在这里而不是写在执行者规则里。
- 凭证边界（2026-10-14 裁定）：daemon 不管理凭证——`auth.json` / `models.json` 是人登录 harness 后留在 operator agent 目录里的文件，daemon 只按白名单**拷贝**进 step-agent 目录，不签发、不刷新、不轮换；claude step 的凭证同理只在 `<harness-root>/claude-config/settings.json` 的 env 块里，由人维护。（原"与 #135 独立 agent 用户方向一致"的注随 #135 冻结作废。）

## 3. 人工协同：`pf watch`（已定） <!-- §3 -->

### 3.1 形态 <!-- §3.1 -->

- Go 实现的 `pf watch`，**取代 `watch.sh` 与 `pf watch-probe`**；`pf observe` 并入为 `pf watch <wi> --once`。
- `pf watch --chat <id>`（缺省取 `PF_CHAT_ID`）**不因订阅的 WI 结束而退出**（保持常驻；D10 裁定，polyforge#150：以代码为准——只跳过已结束的 WI；仍会在父 harness 进程消失、stdout 关闭、或 `--wait-decision` 打印首批决策后退出，`watcher/engine.go`）；一个会话一个进程。
- 只产出两种信号：
  - **进展**：当前 step、已运行时长、正在执行的命令及时长、最近修改的文件、工具调用计数（由翻译器信号机械生成，**不调用模型**）。
  - **决策事件**：有问题要回答/gate、失败、blocked、完成、被判死并已处理、候选已用尽；以及状态/来源类事件（C13 裁定，polyforge#150，与 `watcher/decision.go` 的事件类别表对齐）：`canceled`（终态）、`paused`（非终态，可继续）、`submit_rejected`、`submit_dropped`、`daemon_restarted`；以及 `carrier_push_failed`（polyforge#158：capsule 链推送成功而某任务仓库的 carrier ref 推送失败或被拒，resume 可能丢该仓库的提交；提醒行带经字符集过滤的仓库名，不带错误文本；每个 job 每个仓库只提醒一次，直到该仓库推送再次成功）；以及 `human_gate_unasked`（polyforge#164 H11：`requires_human_answer: if_asked` 的人工关口——autopilot grill——零问题完成，工作流照常继续，owner 收到一次提醒）。
- **订阅关系与读取游标存于 daemon**（按 chat id），不存在 agent 可写的位置。
- **重连 / daemon 重启后**：先以 `execution.json` 核对每个订阅 WI 的当前状态（权威），再按单调事件 id 补读事件——"WI 在 watch 不在时结束"也不会漏。
- **提交后、监控尚未注册**：显示"排队 / 准备中"，不当作错误（#98/#101 注册竞态）。
- daemon 不可达：明确提示"daemon 无法连接，进展暂停"，而不是静默循环。
- pidfile + 锁；**父进程（会话）消失即退出**，不留孤儿。

### 3.2 两条通道（已定） <!-- §3.2 -->

| 信息 | 通道 | 进 agent 上下文？ |
|---|---|---|
| 进展 | 直接显示：Claude Code 用 `statusLine`（可多行，最快每秒刷新）；pi 用扩展 `setWidget`/`setStatus` | 否 |
| 决策事件 | 唤醒 agent，**只给一句由 daemon 按固定模板生成的短话** | 是（少而短） |
| 用户主动问进展 | agent 执行一次 `pf watch <wi> --once` | 仅此时 |

**事故护栏（r2 补；#135 已冻结，这些约束就是现行机制）**
- 唤醒语句**只用 daemon 生成的固定模板**（WI 编号、事件类型、问题 id），**不夹带任何 agent 可写的自由文本**——防止 step agent 借此对交互 agent 做提示注入。问题正文由人通过 `pf watch <wi> --once` 主动查看。
- 唤醒语句固定附带一句："这是需要人做的决定，agent 不得代答"（#122 教训）。
- 进展状态文件由常驻 watcher（`pf watch --chat`，`watcher/engine.go`）写在固定路径 `$XDG_RUNTIME_DIR/polyforge/chat-<chat>.status`；statusLine 用 `pf watch --statusline` 直接读文件、不经 daemon，见附录 E.2。（同 uid、不隔离的信任模型（#135）下，任何本地路径对 step agent 都同样可写，不再区分主/回退路径。）

**不做**：tmux 分屏（owner 否决）；把进展原样经过 agent（污染上下文、耗 token）。

### 3.3 唤醒适配 <!-- §3.3 -->

| 交互 harness | 唤醒方式 |
|---|---|
| pi | 扩展托管常驻 `pf watch`，决策事件用 `sendMessage(..., {triggerTurn: true})` 直接唤醒 |
| Claude Code | 只能"进程退出即唤醒"：agent 以后台任务运行 `pf watch --chat "$PF_CHAT_ID" --wait-decision`（它同时写进度文件），出现决策事件即打印固定模板行并以 0 退出唤醒，agent 处理后重新挂上（`skills/polyforge/SKILL.md` Claude Code adapter；现状 2026-10-16）。Monitor 工具有 30 分钟上限且每行耗 token，不用 |

### 3.4 待验证 <!-- §3.4 -->

- Claude Code 上下文压缩后 SessionStart（`compact`）是否触发（用于补注入提示）。
- `claude --resume` 后 harness session_id 是否不变（决定 chat 映射依据）；pi 的 resume 行为；`/clear`、fork 后的语义。
- pi 事件中 `tool_execution_start.args` 足以提取命令与文件路径。

## 4. 身份模型（已定） <!-- §4 -->

| 概念 | 规则 |
|---|---|
| **daemon**（取代"机器"） | **沿用现有 machine-id 字符串作为 daemon id**（默认 `m-<短主机名>`，不生成新值——aihub 注册新 id 需要 admin/maintainer，daemon key 没有该权限，`server.go:1050-1075`）；改为首次启动时写入 daemon 数据目录，之后以数据目录为准（不放 `cli.toml`：会被 install.sh 重写、会被复制到别的机器）；在途 WI 的 `session_machine` 因值不变而不受影响。另有仅供显示的名字（默认主机名）。aihub machines 表/心跳/凭证概念改称 daemon，表结构可后迁 |
| **chat**（原称 session，改名 `PF_CHAT_ID`，避免与 v1.5"session_id = 推送凭证"重名） | 由 **pf 生成**，会话启动时由 Claude Code SessionStart 钩子 / pi 扩展调用 pf 生成，经环境变量 **`PF_CHAT_ID`** 注入每条命令（CC：`CLAUDE_ENV_FILE`，按会话隔离，已核实 `~/.claude/session-env/<id>/`；pi：`tool_call` 钩子改写命令）。模型不经手；上下文只放一句提示，丢失不影响功能。**resume 同一对话 = 同一 chat**（harness 会话 → chat id 映射存于 daemon 数据目录） |
| 隔离 | 每会话独立进程；daemon 派生 step 时清除所有 `PF_*`，只加回 `PF_WI_ID` / `PF_DAEMON_ID` 标记与本 daemon 的 `PF_DAEMON_SOCKET`（`runtime/spawn.go`），step agent 拿不到 chat id |
| 性质 | `PF_CHAT_ID` 是"通知谁"的**标签，不是凭证**，不得用于权限判断；不得写入任何共享或 agent 可写的文件 |
| **负责人** | 该 WI 的 **reporter**（创建它的认证用户；交互提交、自动领取与在途 WI 一律如此——aihub 没有比 reporter 更可信的身份来源，D5 裁定）。门范围：负责人可 continue（resume-blocked）与 abandon（cancel）；ForceEnd 等恢复门仍限 admin/maintainer |
| **任务来源 origin** | 每个 WI 的 daemon 侧记录：`chat`（含 chat id、负责人）或 `auto`（仅负责人） |
| 通知路由 | 有订阅 chat → 通知 chat；否则 → 通知负责人（其下次任一会话启动时提示，可选择订阅；将来经 #84 推外部渠道）。负责人路由（subscribe-owned）给负责人的其他 chat 加的订阅记下 origin chat，并与 origin chat 的订阅共用同一份送达记录（游标、已送达决策键、done）：决策只唤醒 origin chat（它的 watcher 在线时），其他 chat 只显示进度；origin chat 的 watcher 一分钟没轮询时，由共用记录、id 最小的在线 chat 兜底，且只补送记录里谁都没送达过的决策——origin chat 已拿到的决策绝不再交给兜底 chat。显式 `pf watch subscribe` 让该 chat 成为独立的唤醒对象（polyforge#199） |

"会话绑定 + adoption（接管 watcher）"整套规则被"负责人（必有）+ 订阅（可选）"取代。

## 5. 执行模式与提交（已定） <!-- §5 -->

- **drain 暂不做**，但架构保留支持：领取与执行分离（已是）；人与任务的交互不绑死在执行机上（依赖 #84 跨 daemon 通信）。
- 形态（**已落地**，分支 `v2/m2-modes`）：`pf ops auto on|off` + `pf ops max N`，自动领取与手动提交并存。`auto off` = 只有显式 `pf submit`；`auto on` = daemon 另外自己拉 aihub 就绪队列（即原 drain 循环，origin 仍为 `auto`）。已删除 interactive/batch/drain 三模式、`abort-drain`、`batch-confirm`、`batch_submit`、`get_recommendation`（只是原样转发就绪队列，改用 `pf queue --ready`）、DAEMON-409「drain 是唯一建 job 入口」拒绝。`status` 头为 `auto: on|off  max: N`；heartbeat 体仍带 mode 字符串（`auto` / `manual`）以兼容 aihub。daemon 旗标 `-auto`（`-mode` 别名与 `daemon-state.json` 旧 `mode` 字段的迁移均已删除，见第 8 节「兼容垫片」行：v2 不留向后兼容代码）。drain 作为产品能力仍属「暂不做」范围（跨 daemon 通信 #84 未到），但架构上已可用。
- **名额已满时的提交**：daemon 排队队列**持久化到 daemon 数据目录**（方案 B），重启不丢。重放规则（r2 补）：
  - 条目带 origin 与提交时间；
  - 只重放普通提交；
  - 重放前先查 aihub 状态（已被领取 / 已结束则丢弃）；
  - 与 B1 claiming journal 重放去重，避免同一 WI 双重领取；
  - 被拒一律发决策事件给订阅者 / 负责人。
- `pf submit` 只在本机执行；跨机器使用方式 = 建票进就绪队列由执行方领取（auto 打开后）。

## 6. 恢复与重启 <!-- §6 -->

**原则**：session 是唯一的锁；`execution.json` 是唯一的进度账本；推到 aihub git 才算保存；**绝不悄悄从远端重建本地现场**（#103）。

**已定**
- 某 WI 的 capsule 在重启时缺失 / 项目未映射（cli.toml 无该项目的 workspace 映射）：**只该 WI 被置为普通的 aihub `blocked`**——经与其他 blocked 结局相同的 bounded-fail 收尾发 SessionEnd(blocked)，原因备注写明成因（"capsule missing at <path> …" / "project <p> is not in the daemon's [workspace] mapping …"）；**本地不停放任何东西**（无本地 job 记录、无槽位），仍写 `capsule_missing` / `project_unmapped` 事件（`pf watch` 可见）；daemon 照常启动、其余 WI 照常恢复。人（如需）先修复成因（补映射），再 `pf resume <wi>`：这是普通的 blocked 门（resume-blocked + daemon claim）= 从 aihub capsule 链 + task-repo 载体 ref（`refs/pf-task/<name>`）**重新领取**；死现场（如有）被隔离，已完成的步骤不重跑，最后一次推送之后发布的工作丢失。blocked 的两个出口就是 **`pf resume <wi>`（继续）/ `pf cancel <wi>`（放弃）**；`pf wrap --fail <wi>` 不是 blocked 的门，对 parked 工作流不再有任何 hub 门（§6.4 收紧）：daemon 以 DAEMON-409 拒绝并点名这两条路；daemon 无法判定时（本机无 job，DAEMON-404）CLI 只打印这两条路的提示，不调用 hub、不终结任何东西（审计减法 pass B，C9）。session-end 只接受 wrapped|failed|blocked|paused。
- **重启时 aihub 不可达（repo review 2026-10-16 B-2 裁定，取代未成文的 "C6 audit-subtraction" 姿态）**：重启时 SessionVerify 的**传输错误**（连不上、超时、5xx）不是裁决，**不做任何 hub 转换**：保留本地记录（它持有 session 秘钥的唯一副本和尚未推送的 capsule 提交），先回收孤儿 step 进程（单写者），记录保持原状态（`pf ops status` 可见；其间对它的 `pf wrap`/`pf pause` 在该 WI 的 actor 里排队，verify 应答后得到最终答复），由该 WI 的 actor 退避重试 verify；首个应答走普通对账路径——WI 无需 `pf resume` 即在原 capsule 里续跑，不丢提交。aihub 的 typed 拒绝（4xx）是应答：走原有路径（blocked 并写明原因）。sealing/paused 记录保持其状态，verify 应答后照常对账。关停停止重试，记录持久化，下次重启重新 verify。
- **end_pending 先推链再收尾（同一裁定）**：bounded-fail 在裁决前 commit 脏 capsule；若当时 push 失败（多半正是让 SessionEnd 失败的同一次中断），end_pending 车道的每次重试**先用 session 凭证推送本地链，再发 SessionEnd(blocked)**，push 失败即整体重试；仅当 verify 表明 session 已不再持有该 WI（凭证已死，推不上去）时不带推送收尾。
- **终态关闭的 SessionEnd 失败（同一评审 B-4）**：job 停在 SEALING 时——或保留的 paused 记录已带上终态 stop_reason（暂停后的 wrap/fail/block，即"待处理的终态关闭"）时——该 WI 的 actor 退避后以记录的结论重跑与重启收尾器相同的 `stopOp(alreadySealing)`；待处理的终态关闭期间，再来的 `pf wrap`/`--fail`/`pause` 以 DAEMON-409 拒绝（关闭已在进行），重启对账或 `pf resume` 遇到它时只重放该关闭、绝不恢复执行（polyforge#197）；typed `STALE_SESSION` 停止重试并本地收尾（job 本地 Done，事件 `seal_finished_locally` 注明 hub 状态以管理员/先前已落地的关闭为准）。
- **领取的克隆不持任何锁（repo review 2026-10-16 B-8）**：`Start` 在该 WI 的 actor 里做准入检查、写 claiming journal、领取，并把 job 以 **CLAIMING** 占位放进表（占槽，不写 state.json——journal 即其持久形态）；capsule 克隆与 materialize（含 task-repo 克隆）在该 actor 里进行，不挡其他 WI。dispatcher 每个领取一个 goroutine，队列把未落定的领取计入并发上限。CLAIMING 期间 `pf wrap`/`--fail` 取消克隆、隔离半成品 capsule，并以该结论 SessionEnd，**不做 seal 推送**（未跑过 agent，capsule 链未变）；hub 拒绝该关闭时退回 blocked 收尾（后台重试至落地）。`pf pause` 无可保留的现场，同样取消、隔离后以 **blocked** 收尾并写事件 `claim_paused_ended_blocked`——继续走 `pf resume`（resume-blocked 门：owner/maintainer/admin）重新领取。关停取消 actor 的上下文（`Runtime.Close`），所有在途领取**延后**：不 SessionEnd、不置 blocked，journal 与队列条目保留，下次启动按 B1 重放同一领取；关停不等任何领取或 agent 启动：在途领取遇取消自行延后，仍在装入的 agent 启动由装入方自行拆除。
- **每张票一个 actor（polyforge#182，取代 #178 的每 WI 状态锁、重启 lane 及其闩）**：runtime 对每个 WI（按 canonical id）有一个 mailbox 与一个 goroutine（`runtime/actor.go`）。改变该 WI job 的一切操作——Start 的领取与落定、关闭、暂停、`pf resume`、恢复、被接管处理、重启对账及其重试——都是发给它的消息，逐条执行，网络步骤在 actor 里一路做完（状态边照旧先迁移并持久化）。同一 WI 的并发操作**排队**并得到最终答复（不再答 `DAEMON-409`/`DAEMON-503`）；daemon 的 wrap/pause 遇到表中尚无 job、但该 WI 的 actor 正有操作时同样排队。其他 WI 的任何入口都不等它。任务表锁只护任务表、别名与 mailbox 表，持有期间不做任何网络或磁盘 I/O，`pf ops status`/`pf watch` 只取它与 job 自己的互斥量，hub 挂住时照常应答；`persist` 在它下面取快照并编代号、在自己的互斥量下写盘，旧快照永远不会最后落盘。挂起的 hub 步骤（hub 未接受的关闭、verify、claim 重放）共用一条退避（EndRetryBase → EndRetryMax）。**重启**不问 hub：先把 state.json 的全部记录与 claiming journal 条目种进表（种完之前无人写 state.json），再给每个 WI 发一条消息：回收孤儿 step 进程，然后对账——journal 条目先 verify 其 session 再重放或丢弃，记录先 verify 再按应答恢复、收尾、重放或释放。**会话栅栏**：每个 actor 在重新派生 agent、以该 session 推送或发 SessionEnd 之前都先 verify，不匹配时一件都不做（daemon 停机期间被 hub 结束或接管的 WI 只做本地释放）。**关停** = `Runtime.Close` 取消 actor 的上下文：之后不再启动 agent、不再调度重试；正在装入的 agent 装上即拆除（领取延后、journal 保留；恢复的记录留在 recovering）；关停不等任何 actor。被接管的 daemon 由 M4 每 30 秒复核发现；推送因凭据被拒时 monitor 立即复核，恢复的推送被拒且 verify 确认不匹配时立即停掉该 WI。
- **不加租约**（保留 v1.5 裁决：领取永不驱逐活着的持有者，接管是单独的特权操作）。改为 WI 级"失联"提示：**WI 失联 = 其持有 daemon 的心跳超时（统一一个阈值，替代 aihub 展示层的 5 分钟与本文早先的 15 分钟两套），或该 WI 从该 daemon 心跳上报的在跑列表中消失**；在 `pf queue`/`pf watch` 提示"可接管"并发事件。
- 同一步失败计数上限（2.3）；driver 异常退出界（2.3）。

**曾经的缺陷（均已修复，保留作回归清单）**
1. 重新领取时克隆失败会退化为本地 init，`execution.json` 丢失，工作流从第 1 步重来（`runtime/runtime.go:1143-1153`）→ 改为本地"待 blocked"，保留现场，**持续重试 SessionEnd**（克隆失败多半因 aihub 不可达，此时 SessionEnd 也会失败，不能假设立刻 blocked 成功）；修复须保留 #108 的 task-ref 拉取（`FetchTaskRef`）。
2. 同机重新领取接上旧 git-dir（`gitops.go:649-656`，ENOTEMPTY 被 `os.IsExist` 视为已存在）。
3. daemon 崩溃后 step 进程及其子进程不回收（`recover.go:534-545`）→ 重启时按 spawn journal 中的 pid 与 `PF_WI_ID` + `PF_DAEMON_ID` 双环境标记（同机另一 daemon 的同 WI 进程不受影响）回收（四层清理对 pid=0 也执行）。
4. 部分 bounded-fail 分支不杀 driver、不 commitDirty（`recover.go:62-75`）。
5. bounded-fail 时 SessionEnd 失败仍删除本地记录 → 孤儿（`recover.go:483-499`）。
6. 正常关停白等 ~60s 的 flush(shutdown)（#136）。
7. 重启后 `pf observe` 只读内存事件环，历史从头开始（由 2.2 的持久事件 + 单调 id 解决）。
8. 重启后无任何"daemon 重启过"的信号给会话（→ 决策事件）。

**事实澄清**：正常关停后 WI 在 aihub 上保持 `running`（session 保留），下次 daemon 启动时恢复。

## 7. 生命周期简化（已定方向） <!-- §7 -->

- "resume"现有 5 种含义 → 对人只保留 **暂停 / 继续**；系统内部自动处理统一称 **恢复（recover）**，不再叫 resume。
- blocked 两扇门（unblock / resume-blocked）→ **继续 / 放弃**两个动作（terminate 门已删，polyforge#149）。
- **暂停后的"继续"（wi_seWTgHeKtaa8）**：`pf resume <wi>`（WI 为 paused）先在 aihub 清 `clean_pause`，再调本机 daemon 的 `POST /jobs/{wi}/resume-paused`：本机 daemon 若持有该 paused 记录，**立即原地继续**——与重启时相同的流程（session verify → SessionAttach → 恢复引擎续跑当前步；未答问题重新提出并等待人回答），不必等 daemon 重启。daemon 回答 `attached`（原地继续）/ `already_live`（已在跑，幂等）/ `closed`（已 attach，但恢复续跑当场把 job 收尾——终态裁决或重拉耗尽——不再原地继续）；拒绝：DAEMON-404（本机不持有，CLI 提示到持有机器上 resume）、DAEMON-409（`clean_pause` 未清 / 该记录有待处理的终态关闭——resume 不得与它要取消的关闭赛跑 / session 已不持有 / capsule 缺失则置 blocked）、DAEMON-503（aihub 不可达，未继续）。hub 侧已清 flag 而 daemon 拒绝或不可达时，flag 保持清除，持有 daemon 下次启动的重启对账照常 attach（有待处理的终态关闭时改为重放该关闭）。
- **"继续"的范围（r2 补）**：默认由**原持有 daemon 原地继续**；跨 daemon 继续前，必须确认 aihub 上的 capsule 版本等于原 daemon 封存时的版本，否则拒绝（#116 教训：防止拿到 blocked 之前的旧状态）。
- 继续 / 放弃的权限（已定）：**任务负责人、项目 maintainer、admin** 均可；顺带修 resume-blocked 只查 key scope 不查成员。
- WI 依赖关系保留（#120 已解冻）。

## 8. 减法清单 <!-- §8 -->

| 档 | 内容 | 状态 |
|---|---|---|
| 一（直接删） | `v0/`（已打 tag `v0-archive`，workflow digest 测试向量已迁入活测试）；根目录 `fakepi` 二进制；旧答案缓存 `observe/answers.go`+`answerfile.go`；`tools/gate-metrics`、`tools/capsule-audit.go` | **集成分支已完成**（`3a036b9`、`90d95bf`、`34fdf86`）；deadcode 函数清扫**已完成**（`EffectiveModel` 属 CORE 模型候选特性，保留） |
| 二（#106 残骸） | flush 车道全套、answer 缓存/ledger、recall/remember 代理路由、step 进程 stdio 控制通道、`PF_AGENT_ROLE`（#81）；步骤文本旧协议（#137） | **集成分支已完成**（`34fdf86`、`5f4fc3c`、`4c4564f`）；`agents/` 布局兼容（#136）**未单独核对** |
| 三（改逻辑，随本设计） | supervisor 猜测型车道（hang/idle/probe/wall/turn_duration/工具豁免/问题豁免/ack veto）→ 2.3 规则（**M4 重验保留**）；watch.sh → `pf watch`；三模式 → 第 5 节 | `pf watch`（Go）、watch.sh / watch-probe 删除：**已完成**（`f642e06`、`c01bd94`）；supervisor 车道重写：**已完成**（`internal/polyforge/supervise`、`driver/supervision.go`）；三模式简化：**已完成**（`v2/m2-modes`：`pf ops auto on|off` + `pf ops max N`，守护进程旗标 `-auto`，无兼容别名） |
| 四 | takeover/adopt-session 简化（先验证"结束会话后重新领取能从断点继续"）；心跳保留并修权限漏洞 | takeover 简化：**已验证并完成**——验收测试 `test/v2_m3_end_session_recovery_test.go`（A 完成两步后被 SIGKILL → 管理员强制结束会话 → B 普通领取，从中断步继续、A 的任务仓提交经 `refs/pf-task` 还原、旧密钥写入被拒、A 重启后释放不复活）通过后，删除 `pf takeover`、`pf submit --takeover`、`pf verify-session`、daemon `takeover`/`adopt-session` 路由、`runtime/adopted.go` 托管、aihub `force-takeover` 端点；恢复路径 = 管理员/维护者 `pf ops end-session <wi> --reason ...`（aihub `POST /work-items/{id}/force-end-session`，WI→queued，审计事件 `session_superseded`）+ 任意 daemon 普通 `pf submit`；**M4 重验保留**。心跳权限漏洞：未动 |
| 已完成 | release-intent 门禁（#139，commit a159c8c）；driver 机械保证（needs_input 校验、仅 feature-full grill-review 的 `requires_human_answer`、V4 command_evidence 门禁，`f00b398`）；重新领取两个 bug（git-dir 隔离、克隆失败拒绝 InitLocal / DAEMON-516）；身份模型（daemon-id 文件、`pf chat init`、`PF_CHAT_ID`、origin）；持久化提交队列 + 待修复停放 + `pf resume` 续跑（DAEMON-517；此后已并入 blocked，见第6节）+ `daemon_restarted`/`submit_*` 事件；bounded-fail 杀树 + 推送、end_pending 重试、隔离原子性、继续/放弃走人的 key（`pf resume`/`pf cancel`/`pf wrap --fail`）、`unblock` 动词移除、blocked 封存跨 daemon 校验（DAEMON-518）；待修复 WI 的放弃路径（此后已并入 blocked，见第6节）；孤儿 step 进程回收；`atomicWrite` 修复 | 已完成（`v2/final`）；含孤儿回收的双标记边界（`PF_WI_ID` + `PF_DAEMON_ID`，同机多 daemon 互不误杀）|
| 审计减法 A（polyforge#147） | 结果信封瘦身为最小契约（`protocol_version`/`result_id`/`artifacts` 清单/`handoff_ref` 摘要及 result_id 宽容层全删，§2.6；V4 `command_evidence` 门禁按 owner 裁定保留，仅欠取证的 step 携带）；拒绝重启层删除（3 次进程内重试仍不合格 → blocked `result_invalid`）；未用 frontmatter 键删除（`inputs`/`outputs`+`schema`/`silence_minutes`/`tool_minutes`/`include_verdict`/`on_fail: terminate`）；三个 bundle 的 release step 删除（与 #134 一致；release/deploy 的判定契约按 #134 保留，供 `workflows/release` bundle 使用，作用于最小信封的 `output`——现由这两步强制执行的 output schema 承载，代码里只剩 `defer` 仅限 `deploy` 一条）；driver 死代码（`Driver.Rewind`/`Terminate`、`Budget.USD`/`Tokens`、stdLogger 兜底、调试输出）删除；T 累计语义归位（`runtime_base_ms` 删）+ 超时类死因放宽翻倍（D6）；限流类死因独立成类、原模型退避重试（D7） | **已完成**（本票；交付清单见 WI capsule 的 `artifacts/code-summary.md`） |
| workflow 结构精简（polyforge#165） | feature-full 11→9 步：`e2e-baseline` 并入 `e2e-author`（作者提交测试后在实现前的树上只跑作者文件、证明逐场景断言级红，同一步内修接线 / 删除已有行为的场景（`already_satisfied`）/ 重写空断言；只有"全部通过"或"环境跑不起来"才 needs_input；新增 `requires_command_evidence`），`e2e-run` 并入 feature-full `verification`（输出 `authored_titles` + `scenarios`；全量 bar 没覆盖作者测试时补一条定向命令进 `test_bar`）；所有交付 bundle 统一为 `verification → review → deliver`（chore 调换顺序）；e2e-author 只写新行为场景、软上限新增 400 行测试，超出填 `size_justification` 由 review 判定（driver 不检查）；review 读完整 diff `git diff $(git merge-base HEAD origin/HEAD)..HEAD`（含 e2e-author / repro 的测试提交），测试错误或空断言 = blocking（code 走 H4 的 needs_input 重新定基线），冗余或超规模 = non-blocking；删除无生产调用方的操作员 rewind（`TransRewind` / `applyRewindLocked`），blocked 只有 `pf resume` / `pf cancel`；加载期校验补 kind 集合与 on_fail（只在 gate 上、目标存在且更早），01 文档 V 表改为只列代码执行的规则（#118） | **已完成**（本票） |
| 删除步骤 kind（polyforge#169） | 步骤 `kind` 概念整体删除：bundle frontmatter 的 `kind` 字段、`KindVocabulary`/`ValidKind`、driver 的 Kind 字段与 #165 的 kind 校验；`.polyforge.yaml` 改为 `steps.<project>.<default|step-id>.models`（每个条目目前只允许 `models` 键，#159 再加 `harness`；顶层 `models:` 不兼容、拒绝），拼写保护改为"键必须是已安装 workflows 的步骤 id"（DAEMON-414 拒绝认领 + `pf doctor` 报告）；旧 bundle 快照里遗留的 `kind:` 被宽松解析忽略，同 daemon 恢复不受影响；迁移对照表见 `docs/upgrade.md` | **已完成**（本票） |
| 迁移代码 | v0→v1 迁移（`migrate-memories`/`migrate-work-items`、`core/import.go`、`tools/v0export`、`seed-v1.sh` 等）：owner 裁定提前删除，不再设观察期 | **已完成**（`v2/m1-migrate`） |
| 兼容垫片 | owner 裁定 v2 不留向后兼容代码：`-mode` 别名、`daemon-state.json` 的 `mode` 迁移、`pf observe` 别名、`pf unblock` 提示桩、aihub `unblock` 端点与 `WIManager.Unblock`、`[watch.*]`/`watch_*` 配置容忍、`project_scope` 别名、对遗留 `takeover` 队列项的专名处理 | **已完成**（`v2/final`）；配置加载现对未知 section（含 `[watch.*]`）报错，未知顶层平铺键同样报错（含已移除的 `url` 别名；`533e032`）；2026-10-16 G5 再删 pre-#175 状态读兼容（`step_harness`/裸 id 候选）、`manual_models_dropped` 丢弃路径、pre-#144 按 id 命名 capsule 的隔离、无 `capsule_dir` 记录的目录回退 |

## 9. 快速落地顺序 <!-- §9 -->

不做新旧并行观察，按依赖关系直接替换：

1. **止血**：去掉 release-intent 门禁（已完成待部署）；修步骤文本旧协议（#137）；driver 人工确认保证（#138）；**重新领取路径的两个 bug（第 6 节 1、2）**——已暂停的 #130、#134 恢复依赖它们，修复前不要 resume。
   - git-dir 修法：隔离 capsule 时把外置 git-dir 一并移入隔离区；目标已存在时报错而非复用（含跨文件系统 EXDEV 合并情形）；加"同机隔离后重新领取 → HEAD 等于 aihub 最新提交"的测试。
   - **V4 验证证据门禁移入 driver**（已定）：driver 接受 verification 步骤的 completed 结果时，要求结构化命令证据（argv、exit_code、duration_ms），缺失则走信封拒绝的进程内重试；旧 flush 路径上的同名检查随 #136 删除。
   - 步骤 1 完成后：**验证"结束会话 + 重新领取能从断点继续"**，通过则按第 8 节四档简化 takeover / adopt-session / verify-session（**已完成**，见第 8 节四档）。
2. **事件底座**：driver 事件写入事件日志；单调 id、每 WI 一个文件；`execution.json` 权威。
3. **监督替换（同一批上线）**：driver ↔ supervisor 契约 + 判死规则 + 死因处理 + 失败计数 + 阈值 + 恢复引擎的 driver 异常退出界 + **孤儿 step 进程回收（第 6 节 bug 3，必须同批——否则"重启后同模型重派"会与孤儿进程双写同一 capsule，#99 教训）**。
4. **`pf watch`**：Go 实现、双通道、固定模板唤醒、订阅/游标存 daemon、Claude Code statusLine 与唤醒适配、pi 扩展；删除 watch.sh / watch-probe。
5. **身份模型**：daemon id 存放迁移、`PF_CHAT_ID`、负责人、origin、通知路由。
6. **恢复与重启其余 bug**（第 6 节 4、5、6、8）+ 坏任务置 blocked 后 `pf resume` 重新领取（原"待修复"原地续跑，此后已并入 blocked，见第6节） + 持久化排队队列（含重放规则）+ "继续"范围校验。
7. **减法一、二档**（可与 2–6 并行）。
8. 之后：按步骤选 harness、drain + 跨 daemon 通信（#84）。（权限隔离 #135 已于 2026-10-14 冻结，不在计划内。）

## 10. 现有 WI 映射 <!-- §10 -->

| WI | 去向 |
|---|---|
| #119 | 已取消（本文即其产出） |
| #102 | 已取消（残留） |
| #87、#88 | 被已失联的 `machine-x` 持有自 10-06；接管路径（`pf ops end-session <wi> --reason ...` + 任意 daemon 普通 `pf submit`）已落地，**现在可清理** |
| #122 | 由 #137/#138 与 3.2 的唤醒约束修复后复现验证关闭 |
| #139 | 由门禁删除分支解决（已落在集成分支） |
| #136、#137、#138 | 归入第 9 节 1、7；集成分支已落：#137（步骤文本，`5f4fc3c`）、#138（driver 人工确认保证，`f00b398`）、#136 的 flush/stdio/`PF_AGENT_ROLE` 部分（`34fdf86`）；`agents/` 布局兼容未单独核对 |
| #135 | **冻结**（owner 2026-10-14：团队内部使用，不做重型隔离）；3.2 的事故护栏与「已知缺口」(b) 的 UDS 拒绝即现行机制 |
| #84 | 重要性上升：drain 与跨机器回答的前提；仍冻结，范围待改写 |
| #120 | 已解冻 |
| #130（执行 #126）、#134（执行 #133） | owner 已于 2026-10-12 暂停（paused，进度已推到 aihub）；重新领取 bug 已在集成分支修复（git-dir 隔离、克隆失败 DAEMON-516），**部署后**再 resume；#134 的 **epic/task 概念保留**（已定） |
| #71、#73、#74、#118、#132、#133 | 不受影响 |
| #78、#121、#125、#126、#128 | 继续冻结 |

## 11. owner 裁决（2026-10-12，原待定项全部已定） <!-- §11 -->

1. **V4 验证证据门禁**：移入 driver（第 9 节步骤 1）。
2. **`PF_AGENT_ROLE`**：删除（第 8 节二档）。
3. **历史设计文档**：00–08、v1-*、v1.5-* 开头加注"部分内容已被本文取代，冲突时以本文为准"，不逐篇改写。（2026-10-16 repo review wave 4 取代：00、02–05、07 与 v1/v1.5 规格整体移入 `archive/`；现行参考为 `reference.md`，01/06 修剪为现状。）
4. **takeover 简化**：**已完成**（先验证"结束会话 + 重新领取能从断点继续"，通过后删除 takeover；见第 8 节四档）。
5. **#87、#88**：保留至"失联提示 + 接管"落地后再清理；接管（`pf ops end-session`）已落地，现可清理。
6. **epic/task 概念（#133/#134）**：保留。
7. **继续 / 放弃权限**：任务负责人、项目 maintainer、admin。

## 12. polyforge#140 裁决（2026-10-13，拆分 a：wi-kind 契约——父子与 bundle 规则） <!-- §12 -->

拆分 a（aihub 端 + 设计文档）落地 grill-review 的调整 1–6（w140 裁决记录；拆分 b 承接客户端面：CLI/skills/daemon submit 预检）。接口细节（字段、reason 清单、响应形状、部署与回滚）权威在 `wi-kind-contract.md`；本节登记裁决本身：

1. **epic 由数据派生，不落列**：不引入 `work_items.kind` 列、无迁移、无 PARENT_NOT_EPIC/EPIC_NO_BUNDLE 等新错误码；有子票（`parent_id` 指向它）即 epic，可选 `epic` label 仅展示，服务端不据此强制任何行为。
2. **父子规则对称化**：两条对称规则取代旧“父票必须 epic”的单向表述——①有子票的票不可绑定 bundle（create 与 PATCH 共用同一校验路径，`CORE-400`，`reason=HAS_CHILDREN_NO_BUNDLE`）；②已绑 bundle 的票不可作父票（沿用 `PARENT_NOT_REGISTRY` 语义，不留旧名别名）。bundle 只落在叶子上，epic/父票永远是登记票。
3. **R2 窗口**：`bundle_name` 的固化时点从“建票即冻结”改为**首次认领冻结**（R2 窗口：status=queued ∧ 从未认领；waiting_on 非空不单独拒绝）；窗口内 `PATCH bundle_name` 三向变更（NULL→X / X→Y / X→NULL 解绑），与 create 共用同一落库路径（引用即名字本身——digest 链已删，polyforge#149 B9：不再派生 stand-in，也不接受显式 digest）；`repo_inventory` 不触碰。
4. **待选 bundle 分段**：ready queue 新增“待选 bundle”分段——bundle 为空 ∧ 无子票 ∧ queued 的票，与可执行分段同样排除 waiting_on 非空与依赖未完成的票；epic 不出现在任何分段；响应形状只做加法（不改名不删减旧字段）。（此条已删：无读者，polyforge#194；现行响应见 `wi-kind-contract.md#§3`）
5. **父票完成状态由子票派生、不写终态**：全部子票终态且 ≥1 wrapped → 派生“完成”，否则“进行中”（附子票计数：总数/已终态/wrapped）；父票库内 status 保持 queued，任何终态不写；新增子票自动回到“进行中”（纯读派生，无重开写路径）；暴露只做加法。（此条已删：无读者，polyforge#194；现行响应见 `wi-kind-contract.md#§3`）
6. **执行中发现的问题不挂子票**（本票及后续执行期行为约束）：范围内→顺手修（改动重新声明进 touched files）；越界不阻塞→新票 + `related`；阻塞→新票 + `blocks` 依赖；方向错误→`wrap --fail`/`cancel` 后新开 task。四分支均不向正在执行的票挂子票。

**并发（两组，均有测试覆盖）**：只靠单写者事务串行化加各自的 CAS，不另设仲裁（不用内存闩、不用定时等待）。PATCH bundle vs Claim：绑定或改名的 PATCH 不会让 Claim 失败（解绑若排在前，Claim 按无 bundle 票被拒）；PATCH 排在 Claim 之前则 Claim 冻结新 bundle，排在之后则被拒且不写库（`CORE-400 reason=IMMUTABLE`，或 `CORE-409 reason=RACE_LOST`）——两种次序的终态都正确。绑定 bundle vs 新增子票：恰好一方成功，终态不同时出现“有子票 ∧ bundle 非空”。

## 13. 审计裁定（2026-10-13，polyforge#150：文档对齐——以代码为准） <!-- §13 -->

来源：`audit-2026-10-13-invented-logic.md` §三/§七 + owner 同日补登（claim 幂等记录保留期），划归本票的全部 11 条裁定集中登记于此。每条一行：编号、一句话裁定、现文档落点（本次修订均已落盘）。

| # | 裁定（一句话） | 文档落点 |
|---|---|---|
| D1 | 就绪队列（`GET /api/v1/projects/{project}/ready-queue`）响应为 `{items, running, paused}` 三段（`internal/aihub/core/queue.go`；`pending_bundles` 段已于 polyforge#194 删除）；无 `blocked` 数组，等依赖的票不出现 | `archive/02-aihub-core.md#§5.1b`（重写）及 §5.1 行 4 / §4.2a 行 26 / §7 / §10 关联句；`archive/00-architecture.md#§3.1` 组件表、§3.4b/§3.4c 与 `archive/07-deployment-pilot.md#§9.2` 同步最小修订 |
| D2 | `seq` 项目内唯一（`UNIQUE(project_id, seq)`），非全局 | `archive/02-aihub-core.md#§4.2` DDL |
| D4 | claim 需该 project 的 writer/maintainer membership 或全局 admin（`hSessionClaim`→`requireWrite`，403 otherwise）；key scope 不构成认领授权 | `archive/02-aihub-core.md#§4.10`、`#§8`；`archive/04-local-runtime.md#§6.1` 关联句 |
| D8 | 先推 capsule 链，再推任务仓库 carrier ref（`refs/pf-task/<name>`）；任务仓库推送失败不回滚 capsule 链（polyforge#158 起另发 `carrier_push_failed` 决策提醒，见本文 §14.2） | `archive/v1-spec-full.md`（单行最小修订）+ 本表登记（“或”：两处都做） |
| D10 | watcher 不因订阅的 WI 结束而退出（保持常驻），只跳过已结束的 WI；父 harness 消失、stdout 关闭或 `--wait-decision` 打印后仍退出 | 本文 §3.1 |
| D14 | `paused` 的 WI 保留会话，可直接 wrap/fail/block，无需先 resume（`End()` 同一 session-hash fence，通过后清空会话） | `archive/02-aihub-core.md#§2.5` 表行与相邻文字、§5.1 行 9 |
| C5 | `context_policy` / `handoff` 保留并写入文档 | **已由 #147 落地**，见 `01-workflow-skills.md#§4`/`§4.4a` 与本文 §2.6（本票不重复修改） |
| C11 | job 级收尾前活转录同步保留（`runtime/pitranscript.go`） | 本文 §2.2 |
| C13 | 决策类别补 `canceled`/`paused`/`submit_dropped`（另含 `submit_rejected`/`daemon_restarted`，与 `watcher/decision.go` 代码表对齐） | 本文 §3.1 |
| C16 | `pf file create` 在 cwd 恰落一个映射 workspace 且该 workspace 恰映射一个 project 时推断 `--project`；其余一切情形要求显式 | `archive/00-architecture.md#§3.1` |
| 补登 | claim 幂等记录永不过期、无任何清理任务（缓存应答只含公开 fingerprint，永久保留无安全问题）——已被取代：claim 缓存表已删，重放按 session secret 识别（`reference.md#§3.2`） | `archive/02-aihub-core.md#§4.3`、`#§6.6` |

其余审计条目（B*、C1–C4、C6–C10、C12、C14–C15、D3/D5–D7/D9/D11–D13/D15 等）属其他票（#144/#145/#146/#147 等），不在本票范围。

---

## 14. capsule 链与任务仓库保全（现行机制；2026-10-16 自 05 文档 7.5/7.6 节迁入） <!-- §14 -->

`archive/05-sync-revision-recovery.md` 的其余部分描述已删除的 Revision/CAS/SYNC 模型（已归档）；本节是 capsule 链规则、任务仓库
carrier ref 与 WIP ref 这三项**现行**机制的唯一设计记录。§14.2、§14.3 逐字迁自原 `archive/05-sync-revision-recovery.md#§7.5`、`archive/05-sync-revision-recovery.md#§7.6`（仅按 2026-10-14 信任模型改写了
理由句，交叉引用改为本节编号）；原处随 05 归档为历史副本，以本节为准。

### 14.1 capsule 链规则 <!-- §14.1 -->

- **一条 FF-only 链**：每个 WI 在 aihub 有一个裸仓库，capsule 的提交推到 `refs/heads/main`。aihub pre-receive hook（`internal/aihub/git/hook.go`）
  强制：只能快进、不得删除 ref（`refs/pf-wip/*` 除外）、提交树中不得有 gitlink（`GIT-412`；daemon 在本地 commit 前、以及推送前对待推送范围内的每个提交（含 step 自己的提交）预检同一规则并拒绝，`repos/`、`artifacts/release-*` 下的 gitlink 指引是恢复 capsule `.gitignore`，绝不删任务仓库的 `.git`）、blob ≤ 16 MiB（`capsule.MaxBlobBytes`）。
  提交不带序号 trailer：step 在 capsule 根自己 `git commit` 的提交就是链上一个普通快进提交，随下一次发布原样推送。
- **会话围栏**：推送经 aihub git 代理，Basic 口令必须是该 WI **当前活会话**的 secret（一次性 `GIT_ASKPASS` 传递，不进 argv）；被取代或已
  结束的会话被拒（`GIT-401`）。
- **异步推送的重试调度**（`observe/observe.go`、`observe/monitor.go`）：每次失败一条 `push_failed`，同一 Version 按指数退避重试
  （10s 起，每次翻倍，上限 5 分钟）；退避触顶时发一次 `push_backpressure` 降级事件。新的 Version（提交边界）立即推送但不重置退避，
  一次成功推送结束本轮。每次推送上界 60s（`protocol.PushTimeout`）。
- **再认领 = 重新 clone**：blocked→`pf resume` 等再认领从 aihub 链 clone capsule，再按 §14.2 的 carrier ref 与 §14.3 的 WIP ref 恢复任务仓库；
  已有发布进度的 WI clone 失败时拒绝本地初始化（`DAEMON-516`，否则会从第 1 步重跑）；aihub 链头不等于封存时记录的 head 时，封存机从该 WI
  的隔离 git-dir（`<git-dir>.quarantined.<ts>`，新者优先）取回该 head 并把链移过去，由下一次发布推送，任务仓库（carrier head 与未提交改动）也从同一代隔离 capsule 恢复；另一台 daemon，或封存机上找不到该 head，
  则拒绝（`DAEMON-518`）。

### 14.2 任务仓库 carrier ref（polyforge#108 / #158） <!-- §14.2 -->

本节描述当前代码（v2 Go 执行驱动）。每次 capsule 发布，daemon 先推 capsule 链（`refs/heads/main`），
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
- **凭证安全：带凭证的推送从不在任务仓库里执行**（任务仓库由 agent 操作——按 2026-10-14 信任模型这是**防事故**：agent 可能在仓库里留下 credential helper、insteadOf、hook 等配置；git 没有忽略仓库本地配置的开关，
  配置也可能在检查与推送之间变化，所以不做配置黑名单）。每个任务仓库：
  1. 只读校验（不带凭证）：仓库必须完全自有其存储——`repos/<name>/.git` 是真实目录（Lstat：非 symlink、非 gitfile）；
     不存在 `.git/commondir`；`.git/HEAD`、`.git/packed-refs`、`.git/refs` 及其下任何条目都不是 symlink；`.git/objects` 是真实目录，
     其每个条目（`pack`、`info`、`??` 扇出目录）与 `objects/pack/` 的每个条目都不是 symlink；不存在 `objects/info/alternates`
     与 `objects/info/http-alternates`。在仓库内执行 `git rev-parse --absolute-git-dir` 必须恰好等于该 `.git`；再读一次 HEAD：
     `git rev-parse --verify -q HEAD^{commit}`（unborn = 无可携带，静默跳过）。仓库内的这两次读取另带 `-c safe.directory=<repo>`
     （仓库属主与 daemon 不同时仍可读）。任一校验失败即拒绝并发 `carrier_push_failed`（原因写在事件里），不准备凭证。
  2. 在任务仓库之外（WI 裸仓库目录旁，`pf-carrier-*` 临时目录）建一个 daemon 自有的临时裸仓库，`objects/info/alternates`
     指向 `<repo>/.git/objects`，`update-ref refs/pf-carrier/tmp <sha>`；推送完即删除。
  3. 从临时仓库推送：
     `git --git-dir=<tmp> -c core.hooksPath=/dev/null -c core.fsmonitor=false -c credential.helper= -c http.proxy= -c protocol.allow=never -c protocol.<RemoteURL 的 scheme>.allow=always push --no-recurse-submodules <RemoteURL> <sha>:refs/pf-task/<name>`。
  carrier 通道上每一次 git 调用（校验与推送）用同一个环境：daemon 环境去掉全部 `GIT_*`，加 `GIT_CONFIG_NOSYSTEM=1`、
  `GIT_CONFIG_GLOBAL=/dev/null`、`GIT_CEILING_DIRECTORIES=<解析 symlink 后的 repos 目录>`（校验与推送用同一个值）；推送另加一次性 askpass（`GIT_ASKPASS`、`GIT_TERMINAL_PROMPT=0`）。
  于是任务仓库里的 `credential.helper`、`url.*.insteadOf`/`pushInsteadOf`、`remote.<url>.vcs`（remote helper）、
  `push.recurseSubmodules`/`submodule.recurse` 与子模块自己的 origin、hooks 等都不参与推送。git 从 alternates 只读取对象数据，
  不执行其中任何东西。carrier 通道的每次 git 调用都有超时（校验与临时仓库准备 1 分钟，推送 10 分钟，超时杀掉整个 git 进程组），
  挂住的 hub 不会永久占住该 WI 的锁；daemon 启动时清理残留的 `pf-carrier-*` 临时仓库（与 askpass 清理相同）。
  **残余**：上述无竞态的借用方式（commondir、symlink 的 refs/objects、alternates）一律拒绝；但对象读取与打包
  仍以 daemon 的 uid 读 agent 可写的目录，校验到推送之间的窗口里被改写的仓库布局仍可能把别处的内容带进本 WI 的 carrier ref。
  这只能是蓄意行为；按 2026-10-14 信任模型（团队内部使用，#135 冻结）不再追加防御——这一层是事故护栏，不是隔离边界。

### 14.3 任务仓库未提交改动：WIP ref（polyforge#167） <!-- §14.3 -->

carrier ref 只记录 HEAD，重新认领会丢掉任务仓库里未提交的改动。补充一条 WIP ref `refs/pf-wip/<name>`（`gitops.PushTaskWIP`）：

- **何时写**：只在步骤进程已退出的关闭点——bounded-fail（blocked，SessionEnd 之前）、`stopOp` 收尾（failed / blocked 等非 wrapped）、
  operator pause（`pauseOp`）、daemon 关停（杀掉 agent 树之后）、以及每个 step 结束时（driver 在该次派发的进程退出后、写入其结果或失败之前
  调用 `StepEnded`，即 `runtime.SaveTaskWIP`，上限 30 秒（因此可能把该 step 结果的写入推迟至多 30 秒）；失败不影响该 step，不持任何 runtime 锁），所以 kill -9 至多丢掉当前 step
  尚未结束的改动。observe 的常规发布不写。
  先按 §14.2 镜像每个仓库的 HEAD 到 `refs/pf-task/<name>`，再对每个仓库：有未提交改动（已跟踪修改、删除与未跟踪文件）→ 生成一个
  WIP 提交（唯一父提交 = HEAD）并强推到 `refs/pf-wip/<name>`；干净或 wrapped 关闭 → 直接推 `:refs/pf-wip/<name>` 删除
  （hub 上没有该 ref 时静默）。本次 WIP 生成/推送失败时**保留** hub 上较旧的 WIP ref：它只在父提交等于恢复后的 HEAD（即它被拍下时
  的状态）时才会被应用，应用它总比什么都没有好；失败只记日志并发 `carrier_push_failed`（`ref` = WIP ref，`error` 注明 hub 上的
  WIP 早于本次关闭；同样仅在链推送成功时、每 job 每 ref 一次），从不让关闭失败。
  unborn HEAD 跳过。
- **生成方式**：在 §14.2 的 `pf-carrier-*` 临时裸仓库里完成（alternates 借任务仓库对象），任务仓库只作 `--work-tree`，
  index 用临时文件（`GIT_INDEX_FILE=<tmp>/pf-wip-index`）：`read-tree <HEAD>`、预扫描（见下）、`add -A --ignore-errors`、剔除、
  `write-tree`（树与 HEAD 相同 =
  干净）、`commit-tree <tree> -p <HEAD>`（作者/提交者 `polyforge-daemon`），再用 §14.2 同一条推送命令推 `+<wip>:refs/pf-wip/<name>`。
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
- **关停与 step 结束**：快照在一个 context 下运行（`gitops.Opts.Ctx`；关停：所有 job 并行、共用一个 30 秒期限；step 结束：driver 的
  context 加 30 秒期限）：carrier 通道每次 git 调用的超时都截到其期限，context 结束（到期或取消）即杀掉整个 git 进程组并释放
  per-WI 锁；之后剩下的仓库不再尝试，`carrier_push_failed` 写明 `deadline exceeded` 或 `cancelled`（而不是误导的仓库布局拒绝原因）。
- **hook**：`refs/pf-wip/*` 是草稿 ref，允许删除与 non-fast-forward；只检查新对象（`rev-list --objects <new> --not --all`）的
  blob 上限，用 carrier 的 100 MiB；不做 gitlink 检查。其余 ref 的规则不变（`refs/pf-task/*` 仍禁止删除与非快进）。
- **恢复**（`runtime.restoreTaskWIP`，在 `restoreTaskRepoState` 之后，只对新 clone）：取 `refs/pf-wip/<name>`；若其唯一父提交等于
  恢复后的 HEAD，把新 clone 的 `.git/index` 复制成临时 `GIT_INDEX_FILE`，由 git 执行 `read-tree -m -u <head> <wip>` 把改动写入
  工作树（删除、mode、目录变 symlink 都由 git 处理，不跟随 symlink 走出工作树；没有手写的删除）——不提交、不移动 HEAD、
  仓库 index 不变，改动以未暂存/未跟踪形式出现（原先暂存与否的区别不保留）。父提交不一致（carrier 已前进）或读取/应用失败 →
  不应用，发 `task_wip_not_applied` 事件（`repo`、`ref`、`head`、`wip`、`wip_parent`、`reason`）。不阻塞认领。
  应用后 WIP ref 保留，由下一个关闭点覆盖或删除；在那之前崩溃再认领会再次应用同一份（HEAD 未变时结果相同）。

---

## 附录 A：r2 深度审查漏洞清单与处置 <!-- §A -->

| # | 严重度 | 漏洞 | 处置（本文位置） |
|---|---|---|---|
| 1 | 高 | 退出未交结果一律同模型重试（F4 回归），"机器问题"无来源 | 2.3 细分表；2.5 翻译器上报进程退出 |
| 2 | 高 | 一次派发多次判定、kill 后再计一次失败；多规则优先级未定义 | 2.2 围栏与幂等；2.3 规则按编号优先 |
| 3 | 高 | T 作用域矛盾，放宽值无处持久化 | 2.2、2.4：步骤累计、写入 execution.json |
| 4 | 高 | 失败计数未定义 | 2.3 失败计数 |
| 5 | 高 | driver 异常退出无人管、重启不计失败致无界循环、M4 无归属 | 2.1 恢复引擎；2.3 driver 异常退出界；8 三档保留 M4 |
| 6 | 高 | 事件日志 best-effort、seq 不跨重启 | 2.2 execution.json 权威 + 单调 id；3.1 重连先核对状态 |
| 7 | 高 | daemon id 新生成会被 aihub 拒绝注册 | 4：沿用 machine-id 字符串 |
| 8 | 高 | 孤儿回收晚于"重启同模型重派"（#99 回归） | 9：同批上线 |
| 9 | 高 | 坏任务 blocked 后继续会重新克隆（#103 回归）；克隆失败时 SessionEnd 也会失败 | 6：置 blocked + `pf resume` 重新领取（原待修复原地续跑，此后已并入 blocked，见第6节）；bug 1 持续重试 |
| 10 | 高 | watch 状态/唤醒/映射/队列文件 agent 可写 | 3.1、3.2、4、5：存 daemon 数据目录、固定模板 |
| 11 | 中 | 工具超时重试不放宽（#83 回归）；阈值受 bundle 钉住 | 2.3 tool_hung 放宽；2.4 覆盖对之后领取的 WI 生效 |
| 12 | 中 | 零事件只能靠 T 兜底（#114 退化） | 2.3 规则 ⓪ |
| 13 | 中 | 重试形态未定义 | 2.3 重试形态 |
| 14 | 中 | 持久化队列重放与 aihub 分歧、takeover 重放抢占 | 5 重放规则 |
| 15 | 中 | 步骤之间卡住无人管 | 2.3 driver_stalled |
| 16 | 中 | watch 生命周期、注册竞态 | 3.1 |
| 17 | 中 | 负责人未定义 | 4 负责人 |
| 18 | 中 | 候选用尽语义 | 2.3 候选用尽 |
| 19 | 中 | 阻塞投递与非阻塞循环冲突 | 2.2 无界队列、不在跨 WI 的锁下做长操作 |
| 20 | 中 | T 与 `budget.wall_minutes` 关系、命名撞车 | 2.4 复用 wall_minutes；改名 step_timeout_* |
| 21 | 中 | 跨机继续拿到 blocked 前旧状态（#116 回归） | 7 继续范围 |
| 22 | 中 | 继续/放弃角色未定 | 11 第 7 条 |
| 23 | 低 | PF_SESSION_ID 与 session_id 凭证重名 | 4 改名 PF_CHAT_ID |
| 24 | 低 | 转录同步来源未写明 | 2.2 |
| 25 | 低 | 失联阈值两套、WI 级失联定义 | 6 |

## 附录 B：历史问题回归对照 <!-- §B -->

| 历史问题 | 新方案覆盖机制 |
|---|---|
| #44 agent 死无人知 | driver 退出检测 + ②③④ |
| #48 活动量误判、incident 风暴 | "真实输出"定义 + 失败计数上限 |
| #83 长工具被误杀 | ③ 默认 30 分钟 + 重试放宽 Y |
| #97 快速死亡死循环 | 恢复引擎 driver 异常退出界（连续恢复失败计数器） |
| #99 等人期间假卡死 | 等人期间无进程、不计时 |
| #99 双写者 | 孤儿回收与监督替换同批 |
| #103 绝不悄悄重建 | 坏任务置 blocked，人显式 `pf resume` 才重新领取（原"待修复"原地续跑，此后已并入 blocked，见第6节） |
| #108 task-repo 持久 | 保留 FetchTaskRef |
| #114 探针洗白 / 零事件 | 无探针 + 规则 ⓪ + ④ |
| #116 blocked 前状态 | 继续范围校验 |
| #117/#124 trickle-wedge | ④ 步骤累计 T；状态按派发、不跨步 |
| #122 agent 自答 | #138 + 唤醒固定附带"不得代答" + 唤醒不夹带自由文本 |
| F3 转录 | step_started.transcript_path + commit 前同步 |
| F4 模型 pin 失败 | 退出细分：模型类 → 换候选 |
| F5 致命错误后永久 idle | ① 1 分钟宽限 |
| F13 上下文超限 | 第二次起重开对话 |
| F15 UI 噪声掩盖卡死 | "真实输出"定义 |
| F16/B2 僵尸 flush | spawn_n 围栏 |
| F18/F22 推送风暴 | 事件触发推送保留退避与熔断 |
| B1 领取日志 | 不变；与持久化队列重放去重 |
| M4 僵尸会话发现 | 保留在 supervisor |
| NEW-1/2/3 | 指纹围栏、判定不丢、待关闭回放不变 |
| #92 孙进程持管道 | 组杀不变 |
| #107 信封拒绝 | 进程内重试不变，不计入失败计数 |
| watch 四次修补 | Go 实现、不调模型、进展不进上下文 |

---

## 附录 C：已知缺口（集成分支，2026-10-12） <!-- §C -->

- **(a) bundle digest**：已不存在——digest 链随 polyforge#149 B9 删除，`internal/` 中没有 `Digest` / `FileEntry`；`internal/common/workflow` 只读 frontmatter 的模型候选字段。`01-workflow-skills.md#§4.5` 的 digest 描述已归档。
- **(b) 人工确认保证是事故护栏，不是对抗性防御**：driver 的 `requires_human_answer` 与 needs_input 校验是进程内的机械保证；step agent 可写 capsule，蓄意绕过不在 2026-10-14 信任模型的防御范围内（#135 冻结）。daemon 的 UDS 面（polyforge#122 起于 answer 路由，#164 并入；repo review 2026-10-16 V-1 扩到全部变更路由）按 UDS 对端凭据的 pid（Linux SO_PEERCRED，darwin LOCAL_PEERCRED + LOCAL_PEERPID）拒绝来自本 daemon step 进程树的一切非读请求（DAEMON-403）：对端、其祖先或其进程组 leader 带有 `PF_WI_ID` + `PF_DAEMON_ID=<本 daemon>` 标记即拒；取不到对端凭据也拒（fail closed）。拒绝面是一个包住整张路由表的中间件：`/jobs/{wi}/answer|claim|resume-paused|pause|wrap`、`/ops/config`、`/shutdown`、`/chat/init`、`/chats/{id}/subscribe|unsubscribe|subscribe-owned|ack`；开放的只有纯读（`GET /status`、`/jobs/{wi}/watch`、`/chats/{id}/subscriptions`、`/step/memories/...`）与 step 的记忆写入 `POST /step/memories`（只作用于调用方自己的 WI，`daemon/stepmem.go`）。answer 路由保留其 #122 拒绝文案。残余：`setsid` 脱离进程组且清空环境的进程不在此检查内——这只能是蓄意行为，按信任模型不再追加防御（#135 冻结）。（曾有的 abandon-repair 路由随 repair_pending 一并删除，此实例已不存在。）
- **(c) `pf watch` 进展字段**：已随监督重写合入——daemon watch 路由从 `Observe.Progress(wi)` 填充命令、命令时长、最近文件、工具调用数（见附录 D）。

## 附录 D：监督替换落地对照（第 9 节步骤 2–3，分支 `v2/a-supervision`） <!-- §D -->

**代码位置**
- 契约、翻译器、判死规则：`internal/polyforge/supervise`（`contract.go` 事实/判定/无界队列/每 WI 的 `Link`；`translator.go` + `pi.go` 翻译器接口与 pi 实现；`tracker.go` 每次派发一份的合并状态；`rules.go` 规则 ⓪–④ 的纯函数 `Judge`；`defaults.go` 全局阈值）。
- driver 一侧：`internal/polyforge/driver/supervision.go`（判定围栏、死因表、失败计数、放宽、事实发送）；`TransStepFailed` 在 `transition.go`，一次原子写入记录 attempt（带死因子类）、计数、放宽值、下一次派发形态，必要时写 blocked。
- supervisor 一侧：`internal/polyforge/observe/monitor.go`（消费事实、每派发一份状态、判定、`driver_stalled`、事件触发推送、M4 保留）；事件底座 `observe/events.go`（每 WI 一个 JSONL 文件，逐行带 id、重启后续号、`?after` 从文件读）。
- 失败计数等持久字段：`capsule.Step.Supervision`、`Attempt.FailureCause`、`Execution.BlockedReason`。

**规则 ① 与 T 的后续修订（集成分支）**：规则 ① 扩展到正常结束的轮次（`ended_without_result`，见 2.3 表）；T 取 `max(3 × wall_minutes, 全局默认)`（见 2.4）。

**阈值配置名**：旗标 `--silence-window`（X）、`--tool-window`（Y）、`--step-timeout`（T）、`--stall-window`（N）、`--model-error-grace`（规则 ① 宽限）；step 级覆盖在 workflow.md 只剩 `budget.wall_minutes`（T）——polyforge#147 C5 删除了从未被 bundle 使用的 `silence_minutes` / `tool_minutes` 键，X、Y 只有全局默认。旧旗标 `--hang-window/--idle-window/--probe-timeout/--wall-window` 已删，传入即解析失败。生产中 daemon 只经 `pf daemon ensure` 启动：T、N 来自 cli.toml `[daemon]` 表的 `step_timeout` / `stall_window`，由 daemon 从 `--config` 自行读取，ensure 的 argv 保持固定（2026-10-16 G2；优先级：显式命令行旗标 > `[daemon]` 表 > 内建默认；同表还有 `harness_root`、`operator_agent_dir`、`max_concurrent_jobs`——`pf ops max N` 即时生效、不持久化，daemon 重启后回到配置值）；X、Y、规则 ① 宽限及其余旗标只供测试/开发。

**与正文的实现差异（均为补充，不改契约语义）**
- 事实集合多一条 `exec_committed{version}`：事件触发推送需要"driver 提交了新 Version"的提示，否则只能回到轮询 `execution.json`。
- 事实/判定走 `supervise.Link`（进程内）；driver 与 supervisor 之外的"harness 海量输出"由翻译器直接折叠进每派发一份的 `Tracker`（只保留最新状态，不排队）。
- `driver_stalled`：supervisor 同时向 driver 发判定，并经 `OBNeedsRecovery(reason=driver_stalled)` 交给恢复引擎——卡死的 driver 无法自己消费判定；恢复引擎杀树并重拉，计入连续恢复失败计数器。当前 driver 自身不做 git/推送（推送在 supervisor 的推送环里，受 60s 单次上界约束），所以该规则管的是"driver 在步骤之间的任何卡死"。
- **终态关闭的封存推送**（repo review 2026-10-16 B-7）：workflow 以 completed / failed 结束时，关闭路径照常执行一次 seal 推送（`CommitPush`，干净树只是重推 HEAD），不再假设 driver 已推送（driver 不做 git，终态 `execution.json` 与 transcript 尾只经推送到达 hub）；最终推送重试后仍失败则关闭降级为 **blocked**（本地提交保留字节，hub 从未收到终态），而不是在缺终态的链上 `SessionEnd(wrapped|failed)`；这个 blocked 关闭与其他 blocked 关闭一样带本地链 head（`head_sha`），hub 据此写 `attrs.blocked_seal`，别的机器从缺这些提交的 hub head 继续会被 `DAEMON-518` 拒绝（E.3）。
- 前一次 transcript 作为"只读参考"仅在 capsule 里读得到时才附上（capsule 内的转录靠提交边界同步，进程刚死时可能还没有）；声明的 resume 路径不可读时，resume 退化为 fresh。

## 附录 E：运行中实际存在的契约清单（自代码生成，2026-10-16，decision 2026-10-16 G17） <!-- §E -->

本附录只登记现状，不新增也不改变任何机制：以下各项在代码里是活的、有读者或有外部可见面，但此前设计文档里查不到。来源即表中的常量与文件；与代码不一致时以代码为准，并回改本表。

### E.1 事件 kind（`<data-dir>/events/wi-<id>.jsonl`，daemon 级为 `events/daemon.jsonl`；`protocol.EventEntry` 一行一条）

事件是"新闻"不是状态：执行态权威始终是 capsule 的 `runtime-state/execution.json`（INV-1），追加失败只降级不影响正确性（`observe/events.go`）。`pf watch` 读取的就是这些行。

| kind | 发射者 | 含义 |
|---|---|---|
| `job_started` / `job_stopped` | observe（`monitor.go`、`observe.go`） | 监督开始 / 结束一个 job |
| `needs_recovery` | observe `monitor.go` | 同一 attempt 内的信号要求恢复（reason/detail/spawn_n） |
| `wi_agent_rederived` | observe `monitor.go` | 恢复后的后继 step 进程已起（spawn_n） |
| `pending_invalid` | observe `monitor.go` | `execution.json` 的 `pending_question` 缺 `id`，状态面不把它当待答问题（每次看到一条） |
| `transcript_missing` | observe `monitor.go` | 提交边界找不到本次派生的 harness 会话文件（F3，仅告警，提交照常） |
| `published` / `push_failed` / `push_backpressure` / `push_poisoned_local` | observe `monitor.go`；`push_failed` 另由 runtime 封存路径经 EventTap 发出（`runtime.go`、`restart.go`） | capsule 链推送成功 / 失败 / 背压 / 本地 gitlink 预检拒绝（后者不计入 M8） |
| `session_superseded` | observe `session.go`；runtime `runtime.go` | 周期 session 校验发现另一 daemon 持有该 WI（载荷只有公开指纹） |
| `session_verify_failed` | observe `session.go` | 周期 session 校验连不上 aihub（只记录，不采取行动） |
| `queue_fetch_denied` | daemon `daemon.go`（`wi=""`） | auto-pull 的 ready 队列拉取被 401/403/404 拒绝；每个"健康→拒绝"周期一次 |
| `submit_rejected` / `submit_dropped` | daemon `restartsignal.go`、`modes.go`、`daemon.go` | 已应答 accepted 的 submit 在派发时失败 / 重启后不再重放（body 写原因） |
| `daemon_restarted` | daemon `restartsignal.go` | 写入每个重启对账恢复或停放的 WI；body 带该 job 的状态 |
| `capsule_missing` / `project_unmapped` / `session_verify_outage` | runtime `restart.go` | 重启对账把 WI 置 blocked 的原因（同时是 blocked 原因码） |
| `session_released` | runtime `restart.go` | 重启时发现 WI 已被别的 session 持有：杀进程树、隔离 capsule、丢弃记录 |
| `interrupted_seal` / `pending_close_replay` / `pending_close_replay_failed` / `seal_finished_locally` | runtime `restart.go` | 中断的封存以 blocked 收尾 / 重放持久化的收尾意图 / 该重放未被 hub 接受（记录保留，退避重试） / hub 已不再持有 session，本地完成封存 |
| `missing_midflight_capsule` | runtime `recover.go` | 运行中 capsule 消失，拒绝静默重建（#103） |
| `claim_paused_ended_blocked` | runtime `runtime.go` | 领取尚在克隆时被 `pf pause`：取消克隆并以 blocked 收尾（§6） |
| `retired_env_ignored` | daemon `daemon.go`（`wi=""`） | 已退役的 `POLYFORGE_*` 变量在 daemon 环境里且与解析值不同（只告警） |
| `carrier_push_failed` / `task_ref_diverged` / `task_wip_skipped` / `task_wip_not_applied` | gitops `gitops.go`、`wip.go` | 任务仓库 carrier / WIP ref 推送失败 / carrier 分叉 / WIP 快照剔除的路径 / 恢复时未应用 WIP（§14.2、§14.3） |
| `exec_init` / `step_started` / `step_done` / `gate_verdict` / `needs_input` / `incomplete` / `rewind` / `terminal` / `answered` / `interrupted` / `resumed` / `step_failed` / `blocked` / `candidate_switched` / `candidates_exhausted` | driver `transition.go`（`emitLocked` 按已提交的状态转移发出） | 状态转移的进度新闻；body 带 spawn_n 与 step |
| `step_dead` / `verdict_stale` / `schema_fault` | driver `driver.go`、`supervision.go` | driver 收到的监督裁决（cause/证据）/ 过期裁决被丢弃 / 结果信封持续被拒（契约坏了而非模型问题）；`driver_stalled` 只记在 `needs_recovery.reason` |
| `envelope_rejected` / `gate_verdict_bound` / `human_gate_unasked` | driver `result.go` 常量 | 信封校验拒绝（原文带错误）/ gate 步以 completed 交了 fail 判定，被改绑为 gate_failed / `requires_human_answer: if_asked` 步零提问完成 |
| `output_schema_violation` / `output_schema_unloadable` | driver `outputschema.go` | 输出不符合声明的 schema / schema 读不出来 |
| `authored_tests_rebaselined` | driver `testfiles.go` | 人工批准的测试文件改动成为新基线 |

### E.2 chat / watch 文件

| 路径 | 写者 | 内容 |
|---|---|---|
| `<data-dir>/chats.json` | daemon `identity.go` | (harness, harness-session) → chat id，让恢复的会话沿用同一 chat；chat id 只是通知标签，不是凭据 |
| `<data-dir>/chat-subs.json` | daemon `chatsubs.go` | 每个 chat 订阅的 WI、事件游标与已送达的决策键；负责人路由加的订阅另存 `origin_chat`（与 origin chat 的订阅共用送达记录）；原子写 |
| `$XDG_RUNTIME_DIR/polyforge/chat-<chat>.{lock,pid,status}`（无 `XDG_RUNTIME_DIR` 时为临时目录下按 uid 区分的 0700 目录） | 常驻 watcher `watcher/files.go` | 单实例锁、pid 文件、该 chat 的进度文件；临时目录不是本人所有或可被他人写入时拒绝使用 |

### E.3 `attrs.blocked_seal` 与 `sealed_head` / `sealed_machine`

- `work_items.attrs.blocked_seal = {"head": <sha>, "machine": <id>}`（`core/session.go` `blockedSealKey`）：带 head 的 `End(blocked)` 写入，其他任何 End/Pause 清除；不改 schema，搭在业务 attrs JSON 上。
- 领取应答的 `sealed_head` / `sealed_machine`（`api/types.go` `SessionClaim`）由它读出：继续一个 blocked WI 时 clone 的 head 必须等于它：封存机从隔离 git-dir 取回该 head 后继续，另一台机器（或封存机上已找不到该 head）则拒绝（第 7 节"跨 daemon 继续"）。`attrs` 不在 PATCH 可更新键中（`core/workitem.go` `patchableFields`，只在 create 时写入），用户无法改写或删掉它（2026-10-16 wave 4 按代码更正：原文称 PATCH 可删）。
- 与 `archive/02-aihub-core.md#§2.6` INV-2c（attrs 不得承载执行态字段）的关系：02 已归档，不再是权威；现行代码是权威。代码里 create 只拒顶层保留键 `blocked_seal`（`core/workitem.go` `checkAttrs`；保留键，不是执行态），带它即 `CORE-400 EXECUTION_STATE_REJECTED`；执行态不进 attrs 由 daemon 只写 capsule 保证。现行行为：`blocked_seal` 只由 aihub 在 session 事务里读写（`setBlockedSealTx` 直接 UPDATE，不经 create 校验 / `blockedSealTx`），daemon 只能经 `End(blocked)` 间接写入，用户既不能在 create 时带入也不能 PATCH；它不是执行态（执行态的唯一权威仍是 capsule 的 `runtime-state/execution.json`），只记录 blocked 封存时的交接点——哪台机器、哪个 head。读不到它（键不存在或 attrs 解析失败）时领取应答的 `sealed_head` 为空，跨机继续不做 head 校验（fail open）。不做迁移（G17 选 A，I-5 不做 D）。

### E.4 daemon data dir（默认 `~/.local/share/polyforge`）

| 文件 | 写者 | 内容 |
|---|---|---|
| `daemon-id` | daemon `identity.go` | 首次启动记录的 daemon id；之后与 flag/env 不一致即拒绝启动 |
| `daemon.lock` / `ensure.lock` | daemon `daemon.go` / `ensure.go` | 单 daemon flock / `pf daemon ensure` 与安装器互斥 |
| `daemon.log` | `pf daemon ensure` 的重启循环 | daemon 输出，超过 10 MiB 轮转为 `.1` |
| `daemon.sock` | daemon `main.go` | 未配置 socket 时 CLI 与 daemon 共用的缺省 UDS（`cli.DataDir`，`reference.md#§6.1`） |
| `daemon-state.json` | daemon `modes.go` | auto 开关（`pf ops max N` 不持久化） |
| `submit-queue.json` | daemon `modes.go` | 已接受、待派发的 submit 队列（重启后重放） |
| `runtime-state.json` | runtime `runtime.go` | runtime 的 job 记录（旧文档称作 `state.json`） |
| `claiming.json` | runtime `claiming.go` | 领取前的持久化意图日志（B1，`reference.md#§3.5`），崩溃后重放 |
| `manual-models.json` | runtime `manualmodels.go` | 每个 WI 的 `pf submit --models` 列表（只在本机有效） |
| `events/wi-<id>.jsonl`、`events/daemon.jsonl` | observe `events.go` | 每 WI 一个事件 JSONL（seq = 文件末行 seq + 1，跨重启单调）；静默超过 30 天的文件（含 daemon.jsonl）在 daemon 启动时删除 |
| `chats.json`、`chat-subs.json` | 见 E.2 | |
| `git/<wi>.git` | runtime | capsule 的外置 git-dir（agent 碰不到 hooks/config） |
| `release/<tag>`、`release/current` | `tools/install.sh` | release checkout；daemon 以 `release/current/workflows` 为 `-workflows-dir` |
| `step-agent/`、`step-agent-sessions/`、`claude-config/` | runtime（harness root，默认即 data dir） | 专用 pi agent 目录 / pi 会话 / Claude Code 配置目录（其 `settings.json` env 块是凭据权威层） |

### E.5 capsule 布局（`common/capsule/capsule.go` 及各常量）

| 路径 | 内容 |
|---|---|
| `capsule.json`、`.gitignore` | capsule 身份（含各仓库领取时 SHA）/ 只忽略 `repos/`、`artifacts/release-*/` 与 `sync/` |
| `runtime-state/work-item.json` | daemon 写入的 WI 快照 |
| `runtime-state/execution.json` | 执行态唯一权威（结构：`capsule.Execution`） |
| `runtime-state/spawn-journal.jsonl` | 每次 step 派生一行（harness、会话、transcript 路径） |
| `runtime-state/pending-results/<spawn_n>.json` | 结果信封的唯一落点：step agent 经 `pf result submit` 交付，由 daemon（`POST /step/result`）盖上派发身份后原子写入 |
| `runtime-state/recalled-memories.json` | 领取时的记忆快照（any-term 排序检索，见 `06-memory-search.md#§8`）；driver 以"非约束性历史"注入 step 提示（G3），每条一行 `[type] title`，至多 10 行 |
| `runtime-state/memory-policy.json` | 领取时该项目生效的记忆策略（`.polyforge.yaml` 的 `memory_policy`，缺段为内置默认）；driver 渲染进最后一步的 wrap 摘要职责节 |
| `artifacts/steps/<step>/s<spawn_n>/result.json` | 已接受信封的归档 |
| `artifacts/handoff-<step>.md` | 声明 `handoff` 的步骤按路径约定写出，后续 inherit 步读取 |
| `session/session-<token>.jsonl` | harness 会话文件，在提交边界同步进来 |
| `sync/materialized.json` | daemon 私有（不进链）：bundle 物化完成标记（恢复前的信任门） |
| `workflows/<bundle>/`、`agents/` | 物化的 workflow bundle / step 定义副本（`agents/` 是 decision 2026-10-16 G4 的删除对象） |
| `repos/<name>/`、`logs/`、`artifacts/` | 任务仓库 clone（各有自己的 git 历史）/ 日志 / step 产出 |
