# 01 — Workflow bundle 编写：frontmatter schema 与执行语义

> 现行文档（2026-10-16 repo review wave 4 修剪：WI Agent 物化、创建时 digest 固化、`WF-*` 错误码、requests/tokens 预算、
> 发版槽位 / dispatcher / deploy-face 历史裁定等已删除内容移出；原文见 git 历史与 [archive/](archive/) 中的同期文档）。
> 一页式摘要见 `reference.md#§7`；决策记录见 [v2-converge.md](v2-converge.md)；与代码冲突时一律以代码为准
> （`internal/polyforge/driver/workflow.go`、`driver/transition.go`、`driver/result.go`、`internal/common/workflow`）。

## 1. 范围与非目标 <!-- §1 -->

### 1.1 范围 <!-- §1.1 -->

- bundle 的目录布局与两类 skill（路由 skill / step 定义文件）的边界（§2.1、§4.4）。
- `workflow.md` frontmatter：driver 解析的全部键与加载期校验（§4.1）；step 定义文件 frontmatter（§4.4）。
- 参考流程与 grill（§4.2、§4.3）；step 间上下文（§4.4a）。
- 执行语义（§6）：顺序推进、业务 gate FAIL 的有界 rewind、基础设施失败的原地重试与候选切换、incomplete、驱动主循环、预算。

### 1.2 非目标 <!-- §1.2 -->

- step 进程的派生、监督判死与死因处理（`v2-converge.md#§2`、`reference.md#§7.6`）。
- capsule 布局与 git 链（`v2-converge.md#§14`、附录 E）。
- 没有 per-step 隔离：step 以宿主机原生方式运行（2026-10-14 信任模型，`v2-converge.md#§0`）。

## 2. 术语与不变量 <!-- §2 -->

### 2.1 定义 <!-- §2.1 -->

- **路由 skill**：`skills/polyforge/`（`SKILL.md` + `verbs/` 下每个动词一个 fragment，另有 doctor/help/init、承载执行意图路由的 `execute-intent.md` 与只说明"交付已是 deliver 步骤"的 `ship.md`；以目录为准）。用户 harness 的唯一安装面：只把意图转成 `pf` 动词，不解析 workflow、不碰执行现场、**永不执行 workflow**。**执行意图也由它承载**（owner 裁定 2026-10-20）：owner 指挥的代码工作（改仓/升级/修 bug/部署）是执行意图，路由层的正确动作是把它变成执行票——`file create --bundle` + `submit`——**把意图变成票是路由，不是执行**；「不碰执行现场」约束的是路由层自己的手（capsule、worktree、执行态文件、步骤），不是它可路由的意图类别。会话 agent 绝不亲手执行代码工作：git branch/commit/push、gh pr create、`.repo/` 写操作只在 claimed capsule 里发生（v1 IR1 写操作门禁的 v2 归宿——v1 skill 集删除时铁律随之失载，本条即其迁移；承载面：SKILL.md description 常驻广告 + 会话规则 7 + `verbs/execute-intent.md`）。
- **workflow 的执行者**是 daemon 进程内的 Go driver（`internal/polyforge/driver`），按 step 图逐步派生短驻 harness 进程；没有执行 skill、没有长驻 agent。
- **workflow bundle**：`workflows/<bundle-name>/` = `workflow.md`（frontmatter + 正文）+ `steps/<id>.md`；多个 bundle 逐字共用的 step 文件放在 `workflows/_shared/steps/`（§4.4）。没有 registry/注册接口/digest：WI 只记 `bundle_name`，工作机 daemon 从 `-workflows-dir`（生产为 `<data-dir>/release/current/workflows`）物化（`reference.md#§7.1`）。
- **workflow.md**：frontmatter 是执行图的唯一结构 schema（§4.1）；正文是流程级说明，不参与解析。
- **执行图**：frontmatter 的 `steps` 数组，数组顺序即执行序。物化进 capsule 的副本在该 WI 执行期间不变；Core 不存任何解析结果。
- **step 定义文件**：`steps/<id>.md`；frontmatter 中 driver 只读 `output`（§4.4），正文拼进该 step 的派发 prompt。
- **budget**：只有 step 级 `budget.wall_minutes`，是估计值：T = max(3 × wall_minutes, `--step-timeout`)（§6.6）。
- **on_fail rewind**：业务 gate FAIL 时向更早 step 的有界回退（§6.2）。
- **model_candidates**：step 的有序候选列表 `<harness>:<model>[:<effort>]`（§4.1；优先级与切换见 `reference.md#§7.5`）。
- **grill / grill-review**：参考流程中两个显式 step（§4.2、§4.3）。
- **e2e-author**：code 之前只为新行为编写验收场景、提交，并在同一步内对实现前的树证明逐场景断言级红；code 之后由 `verification` 复跑（polyforge#165）。
- **gate FAIL / 基础设施失败**：前者是闸对产出的否定判定（`gate_failed` 结果）；后者是一切未产出判定的失败（进程死亡、无结果退出、超时、限流等，死因见 `v2-converge.md#§2.3`）。输出不合 `output` schema **不是**基础设施失败（release/deploy 两步为信封被拒，其余只记录，§4.4）。
- **人工 gate**：`requires_human_answer: true`（本次尝试链里没有人工回答前既不能 completed 也不能 gate_failed）或 `if_asked`（问题只能由人回答，零问题可完成但记 `human_gate_unasked` 事件）。旧键 `interactive:` 是未知键，被忽略——写了它**得不到**人工 gate。
- **`next_step_by_order`**：驱动主循环选下一个待执行 step 的过程（最早的非 done step）；不判断 workflow 成功或失败（§6.5）。

### 2.2 不变量 <!-- §2.2 -->

- INV-1a：`bundle_name` 首次认领冻结（R2 窗口，`wi-kind-contract.md#§2`）；capsule 内物化副本在执行期间只读不变；重新认领时重新物化，不做内容校验，本机 checkout 漂移属配置事故。
- INV-1b：rewind 只由业务 gate FAIL 触发；基础设施失败永远原地重试，不回退。
- INV-1c：每个 workflow 实例的累计 rewind 次数有上界（§6.2）。
- INV-1d：rewind 使 `[target, gate)` 内全部 step 重置为 `pending`，旧产物归档为历史尝试，不存在第二种失效机制。
- INV-1e：`next_step_by_order` 返回空只有两种原因——全部 done，或 workflow 已是 `failed_gate` / `failed_rewind_exhausted`；主循环必须先区分再决定收尾（§6.5）。
- INV-1g：模型候选只在 workflow.md frontmatter（及 `.polyforge.yaml` / `pf submit --models` 覆盖，`reference.md#§7.5`）；候选指针只由模型类死因推进，gate FAIL 绝不切换候选（rewind 重置指针）。
- INV-1h：step 定义文件不写 `model`；completed 结果的 `output` 应符合声明的 `output` schema：release bundle 的 `release`/`deploy` 强制（不合格拒收），其余只记录不拒收（§4.4）。

## 4. 数据结构：workflow.md frontmatter schema 与 step 定义文件 <!-- §4 -->

### 4.1 workflow.md frontmatter 完整 schema（唯一结构 schema） <!-- §4.1 -->

`workflow.md` 以 YAML frontmatter 开头（`---` 定界）。顶层字段（driver 解析的只有 `defaults`、`rewind`、`steps`；`name`/`description` 给人看，不被解析）：

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `name` | string | 约定 | bundle 名，约定等于目录名（不解析） |
| `description` | string | 约定 | 一句话说明（不解析） |
| `rewind.max_total` | int | 可选 | 缺省 4；整个 workflow 累计 rewind 上界 |
| `rewind.max_per_step` | int | 可选 | 缺省 2；同一 step 被回退重入的上界 |
| `defaults.model_candidates` | []string | 可选 | 步骤未声明时的有序候选；每项 `<harness>:<model>[:<effort>]`（harness ∈ pi/claude，按第一个 `:` 切分，前缀必填，polyforge#175） |
| `steps` | []Step | 必填 | 非空，`id` 唯一；**数组顺序即唯一执行序** |

不存在 workflow 级 `budget`。

Step 元素字段——driver 解析的十个键（`internal/polyforge/driver/workflow.go` 的 `extStep`），其余键一律被静默忽略（含 `kind`、`interactive`、`tools`、`inputs`、`silence_minutes`、`tool_minutes`）：

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `id` | string | 必填 | 非空、唯一；绑定 `steps/<id>.md`，也是 `.polyforge.yaml` `steps.<project>.<step-id>.models` 的键（同名步骤跨 bundle 共用一个键） |
| `gate` | bool | 缺省 false | 本 step 产出业务判定 |
| `requires_human_answer` | bool \| `if_asked` | 缺省 false | 人工 gate（§2.1）：`true` 时没有人工回答的 completed / gate_failed 信封被拒（repo review 2026-10-16 V-8） |
| `requires_command_evidence` | bool | 缺省 false | completed 结果必须带 `command_evidence`（§4.4a；没有按 step id 的隐式规则——交付 bundle 的 `verification` 显式声明，2026-10-16 裁定 G10） |
| `on_fail` | `{target}` | 仅 `gate: true` | `target` 为更早的 step id——声明即 rewind 边；未声明即 `failed_gate` 终止（§6.2）。FAIL 判定全文无条件送达 target |
| `context_policy` | string | 缺省 `inherit` | `inherit`\|`fresh`（§4.4a）；`gate: true` 的 step 必须是 `fresh` |
| `handoff` | bool | 缺省 false | 本 step 写交接文件 `artifacts/handoff-<step-id>.md`（§4.4a）；缺省即 false，不写 `handoff: false` |
| `budget.wall_minutes` | int | 可选 | 运行时长**估计**：T = max(3 × wall_minutes, `--step-timeout`)（§6.6） |
| `model_candidates` | []string | 可选 | 覆盖 `defaults`（整体替换，非合并）；可跨 harness，换到另一 harness 的候选时新开会话并带旧记录作只读参考 |
| `vars` | map[string]string | 可选 | 填充本 step 定义文件里的 `{{name}}` 占位符（§4.4「step 变量」）；共享 step 文本的车道差异只在这里声明 |

frontmatter 不声明 step 间输入 wiring、`cwd` 或排序键：上游产物落 capsule `artifacts/` 与 `execution.json` 的 `steps.<id>.output`，下游 step 的指令按产物路径约定读取；step 进程的 cwd 恒为 capsule 根。

**加载期校验（以代码为准）**：AIHub 创建 WI 时不解析 frontmatter。认领前 daemon 先对本机 checkout 的 bundle 做候选检查（`common/workflow.CheckCandidates`：无前缀候选、`harness` 键、损坏的 YAML → `DAEMON-414`，WI 留在 queued；auto-pull 跳过该 bundle）。结构校验在工作机 driver 加载已物化 bundle 时执行（`LoadWorkflow` → `validateWorkflow`）；任一规则失败 → 该 workflow 加载失败、WI 不派发；错误点名 step 与问题，不带规则编号或错误码。读取宽松：未知键忽略。

| 规则 | 内容 |
|---|---|
| V2 | `steps` 非空；每个 step 有非空且唯一的 `id` |
| V3 | 数组顺序即唯一执行序（结构保证，无独立检查） |
| V5 | `on_fail` 只出现在 `gate: true` 的 step；`target` 存在且数组位置严格早于本 step |
| V11 | `context_policy` ∈ `inherit`\|`fresh`；`gate: true` 必须 `fresh` |

另：`requires_human_answer` 只接受 `true`/`false`/`if_asked`；候选在 driver 建立时按 `harness.ParseCandidate` 校验（`driver/driver.go`）。删除的规则编号（V1、V4、V6–V10）不复用；结果信封的「V4 命令取证门禁」与此无关、继续执行。

### 4.2 参考流程（review 可处于任意位置） <!-- §4.2 -->

参考 bundle `feature-full`（`workflows/feature-full/`）的 `steps` 顺序：

1. `grill`（非 gate；feature-full 未声明 `requires_human_answer`，autopilot 的 grill 声明 `if_asked`）
2. `spec`（消费 `grill` 的问答记录）
3. `grill-review`（`requires_human_answer: true`、`gate: true`；`on_fail: {target: spec}`）
4. `plan`
5. `e2e-author`（`requires_command_evidence: true`；只为新行为写验收场景、提交，并对实现前的树只跑作者文件、证明逐场景断言级红；输出 `baseline[]`）
6. `code`（经 inherit 交接续上 `e2e-author`）
7. `verification`（`gate: true`、`requires_command_evidence: true`；完整测试 bar + 作者场景逐场景结果；`on_fail: {target: code}`）
8. `review`（`gate: true`；读完整 diff（含测试提交）与 verification 报告；`on_fail: {target: code}`）
9. `deliver`（`gate: true`、`requires_command_evidence: true`；合并、推送特性分支、PR；`on_fail: {target: code}`；文件在 `_shared/steps/deliver.md`）

交付 bundle feature-full / bug-fix / chore 统一以 `… → verification → review → deliver` 结尾（polyforge#165）；autopilot 为 `grill → fix → verification → deliver`（无 review）。流程级 `rewind`：`max_total: 4`、`max_per_step: 2`。

**发版独立成 bundle（2026-10-13 裁定）**：交付 bundle 不含 `release` 槽位；发版与部署只经项目无关的 `workflows/release/` bundle，作为单独的 WI 提交。三步：`release`（`gate: true`：capsule 内 fetch 并重置到 origin 默认分支头、记录发版前远端 tag、探测 `.polyforge/release.sh` 是否支持 `--verify`、跑 `.polyforge/release.sh [bump]`；owner = 携带 `.polyforge/release.sh` 的 repo：零个 → FAIL，恰一个 → 执行，≥2 → FAIL `PF-RELEASE-AMBIGUOUS_OWNER`）→ `verify`（`gate: true`，fresh 上下文：发版 commit 与 origin 头、新 tag 存在且指向发版 commit、发版前每个 tag 不变、发版说明非空，最后 `release.sh --verify <version>`）→ `deploy`（`gate: true`：只部署 verify 写入 `artifacts/verified-version.json` 的版本；owner = 携带 `.polyforge/deploy.sh` 的 repo，零个 → `defer`，≥2 → FAIL；执行 `.polyforge/deploy.sh <v> --confirm <v>`，回滚 = step FAIL）。三步均 `requires_command_evidence: true`、均不声明 `on_fail`（失败即 `failed_gate`，已发布的 tag/release/image 不可变，重发由人另提 WI）。verdict 词汇由 driver 机械绑定（`driver/result.go`）：`release` 为 `[pass, fail]`（pass 须带非空 `version` 与至少一条 `artifacts_published`），`deploy` 为 `[pass, defer, fail]`（pass 须带非空 `version`）。防线在 `.polyforge/deploy.sh` 内：`--confirm <version>`、服务器侧锁、失败自动回滚。

要点：

- step 间数据流不入结构 schema：上游产物按路径约定读取（如 grill 的 `artifacts/grill-qa.md` 被 `spec` 与 `grill-review` 消费）。
- `grill`、`grill-review`、`review` 的位置都由数组顺序表达，不校验。
- `grill-review` FAIL 且目标为 `spec` 时，FAIL 判定全文随目标 step 的输入送回，复用 §6.2，不引入新状态。

### 4.3 grill 的两段式行为 <!-- §4.3 -->

1. **`grill`**（spec 之前）：对模糊需求逐项发问，非 gate；问答记录写入 capsule `artifacts/grill-qa.md`。
2. **`grill-review`**（spec 之后）：对 spec 做一次总结性确认，`requires_human_answer: true` 且 `gate: true`；PASS 推进 `plan`；FAIL 触发 `on_fail: {target: spec}`，问答追加进同一份 `artifacts/grill-qa.md`。

驱动引擎不区分判定来源，`grill-review` 的 FAIL 与其余 gate 走同一路径。**交互渠道**：step 以 `needs_input` 结果提问，driver 把问题写进 `execution.json` 的 `steps.<id>.pending_question`；人经 `pf answer` → daemon `POST /jobs/{wi}/answer` → driver 回答，driver 原地续跑同一 step（`reference.md#§7.3`、`v2-converge.md#§3`）。agent 不得代答：daemon 拒绝来自 step 进程树的 answer 请求（DAEMON-403）。

### 4.4 step 定义文件 <!-- §4.4 -->

```
workflows/<bundle-name>/
├── workflow.md            # 必需：frontmatter（结构）+ 正文（流程级说明）
└── steps/
    └── <step-id>.md       # 每 step 一个；文件名 = step id
workflows/_shared/
├── principles.md          # 通用工程原则：由 {{principles}} 渲染进 grill/spec/plan/code 类 step 与 review
└── steps/
    └── <step-id>.md       # 多个 bundle 共用的 step 定义（deliver.md、verification.md、review.md）
```

每个 `steps/<step-id>.md` = frontmatter（元数据）+ Markdown 正文（执行指令）；driver 从物化后的 bundle 直接读取（`driver/spawn.go`、`driver/outputschema.go`）。

**共享 step 定义（2026-10-16 裁定 G1）**：查找顺序由 `workflow.StepDefinitionPath` 决定——bundle 自己的 `steps/<id>.md` 存在即用它（可覆盖共享版本）；否则回退到 `workflows/_shared/steps/<id>.md`。`_shared` 不是 bundle（没有 `workflow.md`），物化时整目录拷到 capsule 的 `workflows/_shared/`。不要用 symlink 共享：物化的 `copyTree` 跳过 symlink。多个 bundle 逐字相同的 step 文件必须放进 `_shared`（`driver/shared_steps_test.go` 守护）。

**step 变量（2026-10-16 裁定 G1 后续）**：车道之间只差几处事实（rewind 目标、产物名、车道专属检查）的 step 也只写一份共享文本，差异写成 `{{name}}` 占位符，由 workflow.md 里该 step 条目的 `vars:` 填值（`common/workflow.RenderStepVars`；`name` 为 `[a-z][a-z0-9_]*`）。规则只有三条：渲染作用于整个定义文件（frontmatter 的 `output` schema 与正文都渲染，`readStepDefinition` 与 `loadStepOutputSchema` 读的是同一份渲染结果）；值原样插入（YAML 块标量的结尾换行被去掉），不再二次展开；两个方向都 fail-closed——文件里用了却没声明的占位符、声明了却没被用到的变量都是错误（派发时 step 定义不可读，schema 记 `output_schema_unloadable`；`TestShippedStepDefinitionsResolve` 对所有出厂 bundle 守护）。能用中性措辞表达的差异不开变量。字面的 `{{name}}` 没有转义写法；变量值原样插入（在 schema 行里即未加引号的 YAML），bundle 视为可信、由测试守护（`TestShippedStepOutputSchemasCompile`、`TestShippedStepDefinitionsResolve`）。出厂的 `verification`（`output_required`、`build_step`、`position`、`lane_checks`、`never_pre_existing`）与 `review`（`build_step`、`scope_source`、`intro`、`judge`）按此共享；`TestSharedStepPromptsKeepLaneFacts` 逐 bundle 派发并核对每条车道事实仍在 prompt 里、且不混入别的车道的事实。workflow.md 正文的交付与发版说明也只留一段指向 `_shared/steps/deliver.md` 的「Delivery and releases」。

**共享原则（polyforge#191）**：`workflows/_shared/principles.md` 是对任何项目都成立的工程原则（简单、可恢复、最小权限、证据、行为由所有者决定），外加一条要求：step 开工前先读 `repos/` 下每个仓库根目录的 `AGENTS.md`（否则 `CLAUDE.md`），项目规则同等约束、冲突时以项目规则为准并在产物里注明。全文只有这一份：step 定义文件里写 `{{principles}}` 即由 `workflow.ReadStepDefinition` 渲染成该文件内容——这是唯一不需要在 `vars:` 里声明的占位符，step 条目声明同名变量是错误；没用到它的定义不读该文件（物化前的旧 capsule 照常运行）。每个用到它的 step 在 `## Principles` 节里先写一段本 step 怎么用这些原则，再放 `{{principles}}`。出厂覆盖：feature-full 的 `grill`、`spec`、`plan`、`code`；autopilot 的 `grill`（兼写 memo，按 grill+spec 用）、`fix`（plan/code 类）；bug-fix 的 `root-cause`（给修复范围指引，按 plan 类+证据用）、`fix`；chore 的 `plan`、`code`；以及共享的 `review`（每条原则都是阻断检查，检查清单逐条展开）。每节不超过 1500 字节（`driver/principles_test.go` 守护覆盖范围、恰好一次与字节上限）。只对某一个仓库成立的规则不进 `workflows/`，写在该仓库自己的 `AGENTS.md`——polyforge 自己的（向后兼容代码、daemon/aihub 新入口的可达性声明等）在本仓库根目录的 `AGENTS.md`，`CLAUDE.md` 只有一行 `@AGENTS.md`（Claude Code 按 import 读入；pi 每个目录只加载第一个存在的 `AGENTS.md`/`CLAUDE.md`，读到的就是 `AGENTS.md`）。

frontmatter 字段——**driver 只读 `output`**：

| 字段 | 类别 | 说明 |
|---|---|---|
| `name` / `description` | 约定 | 不解析 |
| `output` | 可选，唯一被读取的键 | draft-07 JSON Schema（YAML 书写）：注入派发 prompt（放在正文之前），并由 driver 校验 completed 结果的 `output`（值为 `null` 的可选属性按缺省处理） |
| `model` | 禁止 | 模型选择只在 workflow.md frontmatter（INV-1g），`model` 不被读取 |
| `tools` | 已删除（G10） | 从未被读取；旧快照里残留的被忽略 |

**输出契约（INV-1h）**：release bundle 的 `release`、`deploy` 两步**强制**（`driver/outputschema.go` 的 `outputSchemaEnforced`）：不合格即信封被拒（同一进程内重试车道），不算基础设施失败、不推进候选。其余 step 为**只记录模式**：不合格只记持久事件 `output_schema_violation`，不拒信封、不算基础设施失败、不推进候选。声明了却无法加载的 schema 记 `output_schema_unloadable` 并写 stderr；在 `release`/`deploy` 上，schema 缺失或无法加载时结果一律被拒（判定契约就在 schema 里）。

### 4.4a step 间上下文传递：`context_policy` 与 `handoff`（polyforge#147） <!-- §4.4a -->

两种上下文关系都由 frontmatter 的 step 键声明，**语义全在路径约定上，没有摘要/清单校验**：

- **`context_policy: inherit`（缺省）**：派发时 driver 取**最近一个声明了 `handoff: true` 的更早 step**（它必须已 done，否则派发报错），按 `artifacts/handoff-<step-id>.md` 读出交接正文，原样拼进本 step 的派发 prompt（「Handoff from step …」小节）；没有这样的上游、或文件不在，就只是少了那一段，不是失败（`driver/spawn.go` `resolveHandoff`）。
- **`context_policy: fresh`**：不注入交接正文；上游产物按路径约定自取。`gate: true` 的 step 一律 `fresh`（解析器强制）。
- **`handoff: true`**（只在某个下游 inherit step 以本 step 为最近交接源时才声明；2026-10-16 裁定 G10 删除了 7 处无读者的声明）：本 step 收尾前必须写 `artifacts/handoff-<step-id>.md`。

**结果信封是最小契约**——`outcome` + `output`（`needs_input` 另带 `question_id` + `question`）加派发身份围栏，没有 `handoff_ref`、sha256、artifacts 清单（`v2-converge.md#§2.6`）。driver 在最小契约之外的机械门禁：V4 命令取证（声明 `requires_command_evidence: true` 的 step 的 completed 结果带 `command_evidence: [{argv, exit_code, duration_ms}]`，至少一条、argv 非空、`duration_ms > 0`，被接受的取证由 driver 写入 `output.commands`）；已提交测试文件门禁（`output.test_files` 的每条路径必须在其所在 task repo 的 HEAD 已提交，polyforge#156）；已写测试不可改（polyforge#164 H4）；`defer` 判定只允许出现在 `deploy`（release/deploy 的其余判定规则在其强制的 output schema 里）。违者信封被拒，带拒绝原因在同一进程内重试；第 4 次连续被拒（`maxEnvelopeRejects` = 3 次重试）→ step blocked `result_invalid`。另有：needs_input 问题形状、pre_existing 失败的边界、deliver 绑定、人工 gate 拒绝、release/deploy 的 `fail_reason` 必填与 `defer` 只在 deploy 合法（`driver/result.go`、`driver/deliver.go`）。

### 4.5 bundle 引用 <!-- §4.5 -->

bundle 引用只有 `bundle_name` 一个 scalar（polyforge#149 B9 删除了整条 digest 链：无聚合摘要、无 stand-in、不接受显式 pin，`internal/` 中没有 `Digest`/`FileEntry`）。执行侧按 `bundle_name` 从本机 checkout 物化，不做内容校验；checkout 缺失或漂移属配置事故（物化告警继续）。

## 6. 核心流程 <!-- §6 -->

### 6.1 成功路径：顺序推进 <!-- §6.1 -->

1. 按 `steps` 数组顺序逐个执行。
2. 每个 step 的结果由 driver 原子写入 capsule 的 `runtime-state/execution.json`（结构见 `internal/common/capsule/capsule.go` 的 `Execution`/`Step`）；supervisor 的推送环在每次提交边界把 capsule 推上 aihub 链（`reference.md#§8`）。
3. 运行时不跳转、不重排。

### 6.2 业务 gate FAIL → 有界 on_fail rewind <!-- §6.2 -->

一个 `gate: true` 的 step 产出 FAIL（`gate_failed` 结果，或 completed 带 `verdict: fail`——driver 改绑为 gate_failed）时（`driver/transition.go` `applyGateFailLocked`）：

1. 未声明 `on_fail`：workflow 终止为 `failed_gate`，判定全文写入 `execution.json`。
2. 声明了 `on_fail: {target: X}`：先查两类上界——累计 rewind 已达 `rewind.max_total`（缺省 4），或 target 重入次数已达 `rewind.max_per_step`（缺省 2）——任一命中即终止为 `failed_rewind_exhausted`，不做下面的回退。
3. 均未命中：`rewind_stats` 的累计计数与 target 的重入计数各加一。
4. `[target, gate)` 内每个 step：当前结果归档为一条带 `rewind_gate`（触发的 gate id）的 attempt——target 自身为 `cause: gate_fail_rewind`，target 与 gate 之间的 step（它们没有失败，只是产出被后面的 gate 作废）为 `cause: superseded_by_rewind`；状态重置为 `pending`、输出清空（防下游消费陈旧产物）。
5. 当前位置设回 target；FAIL 判定全文作为 `steps.<target>.pending_rewind_input` 持久化，target 重跑时消费。
6. 触发 FAIL 的 gate 自身不归档、保持 `in_progress`，以 fresh 形态重入，累计 T 归零（`v2-converge.md#§2.4`）。
7. 从 target 继续 §6.1。

**轻量回退（polyforge#222，上述序列的一个子形态）**：`review` gate 的 FAIL 且其记录的 blocking findings（至少一条）全部带 `text_only: true` 时，同一次原子写入走轻量路径——上界先查、槽位照耗（第 2、3 条不变），target 归档为 `cause: gate_fail_rewind_text_only`，判定全文随轻量重入说明（`pending_rewind_input` 的 `Note`）送达 target；target 的再派发恢复其上一次会话（同 harness、同会话身份）而非全量重推导——恢复指令不另写一份，由同一次写入已落盘的台账推导（target 为 pending，且其最后一条非 `infra_failure` 归档 attempt 即轻量原因；与 `needs_code_change` 自报校验读的是同一事实）；恢复不可能（异 harness、原生会话/transcript 已不存在）时降级为今天的 fresh+只读参考回退，同一槽位内、spawn journal 记录 `resume: false`。轻量重入的 completed 结果可带 `needs_code_change: true`（结构性修复的自报；无轻量上下文或非布尔值即拒收）：同一次写入把 target 按 `gate_fail_rewind` 归档、重置 pending、把同一份 FAIL 判定作为 fresh 重入的输入送达，不再计槽位（分类错误不重复计费）。gate 自身仍以 fresh 重入（第 6 条不变）。

**候选指针在 rewind 中重置**（INV-1g）：被重置的 step 重跑从候选列表第 1 项开始；gate FAIL 的任何路径都不切换候选。上界可在 frontmatter 配置（≤0 的值回落为缺省 4 / 2，无法用 0 关闭 rewind），达到即终止，没有"人工授权后继续"的第三条路径——后续只能是 WI 层面的决定（`pf cancel` 后另开 WI）。

### 6.3 基础设施失败 → 当前 step 原地重试（按死因有界切换候选） <!-- §6.3 -->

1. 判定：一切未产出业务判定的失败（进程死亡、退出未交结果、超时、harness 静默、限流、上游瞬时错误、宿主机错误），由 supervisor 判死或 driver 在等结果时发现，死因分类与处理表见 `v2-converge.md#§2.3`。
2. 处理（一次原子写入，`TransStepFailed`）：step 保持 `in_progress`，向 `steps.<id>.attempts[]` 追加一条 `cause: infra_failure` 的记录（带 `failure_cause`）、更新失败计数与放宽后的阈值、写入下一次派发形态。
3. 原地重新派发同一 step，不跳过、不回退；已 done 的 step 绝不重跑。
4. **候选切换**（`driver/candidates.go`）：生效列表按 `reference.md#§7.5` 的优先级取得；指针 = 最后一条 rewind 归档（`gate_fail_rewind`、`gate_fail_rewind_text_only` 或 `superseded_by_rewind`）之后、模型类死因（`model_error`、`model_hung`、`step_timeout_slow_model`、`exit_model_class`）的 `infra_failure` 条目数，取到末位为止——只依赖 `execution.json`，崩溃恢复后复算一致。宿主机类死因使 step blocked、不推进；其余死因（含 `rate_limited`、界内的 `upstream_transient`）原模型重试。候选用尽后保持末位。
5. 计入的失败达上限（`DefaultMaxStepFailures` = 3）→ step blocked；该计数**按候选**：换到新候选时清零；`rate_limited` 与界内的 `upstream_transient` 不计（后者连续 2 次后第 3 次归为 `exit_model_class` 换候选）；daemon 重启造成的中断不计失败、不推进指针（`driver/supervision.go`）。

### 6.3b 归档 attempts 的两分法（cause 语义） <!-- §6.3b -->

`steps.<id>.attempts[]` 的非接受记录分两类车道，以 `cause` 区分、互斥（另有一类非失败记录：每个被接受的 completed 结果追加一条 `cause: accepted`，`driver/transition.go`，供恢复引擎的进度账本与候选指针复算使用）：

1. **rewind 归档**：§6.2 第 4 步追加——业务车道的归档，随后该 step 重置为 `pending`。rewind 的 target 记为 **`gate_fail_rewind`**，轻量回退（§6.2）的 target 记为 **`gate_fail_rewind_text_only`**；轻量重入以 `needs_code_change: true` 自报结构性修复时，completed 分支的同一写入同样把该次重入按 **`gate_fail_rewind`** 归档（复用同一判定与槽位，见 §6.2 轻量回退）；target 与 gate 之间被连带作废的 step 记为 **`superseded_by_rewind`**（它本身没有失败）。三种 cause 都带 `rewind_gate`，都重置候选指针。
2. **`infra_failure`**：§6.3 第 2 步追加——可审计的中断记录，不改变 step 状态（保持 `in_progress`），同时是候选指针复算的输入。

只有回退归档才产生 `gate_fail_rewind` / `gate_fail_rewind_text_only` / `superseded_by_rewind`——gate FAIL 判定的回退（§6.2），或轻量重入的结构性自报（同样按 `gate_fail_rewind` 归档，复用同一判定与槽位）；一切未产出判定的中断都是 `infra_failure`。两类记录按时间序共存。

### 6.4 incomplete 结果 <!-- §6.4 -->

1. step 可以诚实地产出 `incomplete`（不是质量否决，而是未能完成）。
2. driver 把 workflow 置为 `blocked`、当前位置不变；runtime 先封存推送 capsule，再以 `SessionEnd(blocked)` 把 WI 置为 aihub `blocked`（`waiting_on=owner`；`runtime/recover.go`）。
3. 出路交给人：`pf resume <wi>`（继续：重新认领并在原 step 续跑）或 `pf cancel <wi>`（放弃）（`v2-converge.md#§6`）。

### 6.5 驱动主循环 <!-- §6.5 -->

主循环是 daemon 内的 Go driver（`driver/driver.go` `Run`），反复调用 `next_step_by_order` 选出下一个 step 并派发，直到没有下一个 step：

1. 返回一个 step → 派发（一个短驻 harness 进程），等待其结果信封。
2. `completed` → 标记 `done`，回到 1。
3. `gate_failed` → §6.2；若进入 `failed_gate`/`failed_rewind_exhausted` 立即退出，否则回到 1。
4. 基础设施失败 → §6.3，原地重派同一 step。
5. `needs_input` → 等人回答（等待期间没有 step 进程、不计时），回答后原地续跑。
6. `incomplete` → §6.4，立即退出。
7. 第一次返回空时显式检查状态：已是失败终态 → 退出，不得误判为完成；否则全部 done → workflow `completed`。

**收尾映射**（`runtime/recover.go`）：`completed` → seal 推送后 `SessionEnd(wrapped)`；`failed_gate`/`failed_rewind_exhausted` → seal 推送后 `SessionEnd(failed)`；`blocked` → 封存后 `SessionEnd(blocked)`。终态 seal 推送失败时关闭降级为 blocked（`v2-converge.md#§D`）。

### 6.6 预算（budget） <!-- §6.6 -->

- 结构：step 级 `budget: {wall_minutes: int}`，唯一的键；不存在 requests/tokens 维度或 workflow 级预算（polyforge#147）。
- 语义：`wall_minutes` 是**估计值**——该 step 跨全部派发的累计运行时长界 T = max(3 × wall_minutes, 全局 `--step-timeout`，缺省 90m)（`driver/supervision.go` 的 `WallEstimateFactor`；`v2-converge.md#§2.4`）。在缺省 `--step-timeout` 90m 下，≤30 分钟的估计不起作用，交付 bundle 都不声明；release bundle 的 `release` step 声明 45（T = 135m）。
- 超出 T：supervisor 判 `step_timeout_long_task` / `step_timeout_slow_model`，按 `v2-converge.md#§2.3` 处理（放宽 T 并持久化；慢模型换候选；长任务第二次超时后 blocked）。
