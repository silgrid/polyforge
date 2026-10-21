# 06 — Memory / Search：数据库存储、FTS 索引、一次性内容迁移

> 现行文档（2026-10-16 repo review wave 4 重写为只描述代码现状）。原文描述的 HTML 派生链、`backupBarrier`
> 与一致性快照、`PATCH /memories`、分享链接、wrap 摘要条目、`MEM-501` 等**从未建成或已删除**，原文见 git 历史
> （本文件 2026-10-16 之前的版本）。代码权威：`internal/aihub/memory`（`store.go`、`authority.go`、`migrate.go`）、
> `internal/aihub/server/server.go` 的 memory 路由、`internal/polyforge/cli/verbs_aihub.go`、`internal/polyforge/runtime/recall.go`。
> 一页式摘要见 `reference.md#§9`。

## 1. 范围 <!-- §1 -->

Memory 是 Core 域数据（项目知识沉淀）：一条 memory = `memories` 表的一行（`content` 列存文档）+ 一行 FTS 索引，
全部在 Core 数据库里，数据库是唯一的事实来源。写入只有创建与 redact（软删除）两种；**没有更新路径**（改内容 = 新建一条，
必要时 redact 旧的）。检索只有 FTS5 trigram 文本检索，没有向量/语义层。

## 2. 不变量 <!-- §2 -->

- 创建：一个事务插入行（含 `content`）+ FTS 行（FTS 先删后插）（`Store.Create`）。redact：一个事务置
  `status='redacted'` + 删除 FTS 行；文档留在行里，此后永不服务（`Store.Redact`）。
- `content_sha` = `content` 的 SHA-256，`content_len` = 其字节数（`pf memory show` 显示的 `content_len`）。
- 运行时不写也不读 memory 文件。行的 `authority_path`（`memory/<project_id>/<mem_id>`）只供 §4 的迁移定位旧文件。
- `content` 为 NULL 的行（只可能来自迁移时找不到文件，§4）对一切读写路径等同不存在（`MEM-404`）。

## 3. 文档格式 <!-- §3 -->

`content` 存完整文档：YAML frontmatter + 正文原样。frontmatter 与 `memories` 表列一一镜像，按固定顺序序列化
（`authority.go` `frontmatter`）：`id`、`type`、`owner_user_id`、`title?`、`tags?`、`visibility`、`work_item_id?`、
`project_id`、`idempotency_key?`、`related?`、`created_at`、`updated_at`。`id` 由服务端分配（`mem_` 前缀）。
`?format=content` 与检索摘录只返回正文（`memory.Body` 去掉 frontmatter）。行的 `title` 在创建时确定
（`memory/title.go` `rowTitle`）：请求有非空白 `title` 时原样使用，否则从正文派生——第一个非空行，
去掉行首 markdown 标记（`#`、`>`、`-`/`*`/`+`、`[ ]`、`1.`/`1)`），跳过代码围栏行与分隔线，按字符边界截到
80 字符（截断时末尾为 `…`）。派生标题只写入行，不写进 frontmatter。`visibility` ∈
`private|project|team|public|admin`。正文上限 1 MiB（`MEM-413`）。

## 4. 一次性内容迁移（启动期） <!-- §4 -->

此前的版本把文档存为 `<data-dir>/memory/<project_id>/<mem_id>/memory.md`（redact 后在
`<project_id>/_redacted/<mem_id>/`），行里没有 `content`。aihub 启动时、开始服务之前运行
`Store.MigrateContent`（`cmd/aihub/main.go`；先 listen，迁移完成后才 serve，连接在 backlog 里等待）：表缺
`content` 列时 `ALTER TABLE ADD COLUMN`；对每个 `content IS NULL` 的行，按 `authority_path` 读文件（先按行状态
找 active / `_redacted` 位置，再找另一个），文件字节原样写入 `content`，按 16 MiB 一批小事务提交。文件 sha 与
`content_sha` 一致 → 复制；不一致 → 以文件为准（旧版本的规则），重算 `content_sha`/`content_len`，active 行重写
FTS；active 行的文件只在 `_redacted/` 下（redact 在挪文件后、提交前中断）→ 补完 redact；找不到文件 → 留 NULL、
不服务，下次启动重试。随后同一趟为标题为空的 active 行按其文档派生标题（与创建同一规则：frontmatter 的非空
`title`，否则正文首个可用行；派生不出的保持为空）。每次启动记一行计数日志，非简单复制的行逐个列出 id。已填的行不再被选中：迁移过的库上
不读 memory 文件、不写。文件一律保留不删。耗时：生产备份副本上 3,810 条约 1.5 s。

## 5. API 与错误码 <!-- §5 -->

### 5.1 API <!-- §5.1 -->

| 方法 | 路径 | 权限 | 说明 |
|---|---|---|---|
| POST | `/api/v1/memories` | 项目 writer/maintainer 或 admin，否则 `MEM-403`；key 限定在其他项目 → `AUTH-403`；项目不存在 → `CORE-404` | body `{project, type, title, visibility, content, tags, related, work_item_id, idempotency_key}`；`work_item_id` 接受 id 或 `<project>#<seq>`，须同项目；`type` 须为 `kind` 或 `kind.name`，kind（第一个 `.` 之前）属于 `authority.go` `typeKinds`（experience/fact/doc/rule/methodology），否则 `MEM-400` 并点名被拒类型与合法 kind（polyforge#210，类型命名空间的唯一权威）；`title` 缺省或空白时由服务端从正文派生（§3）；同一 `idempotency_key`（同 owner、同项目）重放返回原条目，键属于他人或他项目 → `MEM-409 IDEMPOTENCY_KEY_REUSED` |
| GET | `/api/v1/memories/search` | 任意 key；结果按可见性过滤 | `q`（必填）、`match`（`all` 缺省 / `any`，其他值 → `MEM-400`）、`type`（精确值，或 `X.*` 前缀匹配 `X.`）、`tags`（逗号）、`limit`（缺省 50；≤0 或 >200 时取 50）、`project`（先过读权限再作过滤）、`excerpt`（>0 时每条带 `content_excerpt`，上限 8192 字符）；只返回 `status='active'` |
| GET | `/api/v1/memories/{id}` | 读权限；无权与不存在同为 `MEM-404` | 元数据；`?format=content` 返回原始 Markdown 正文 |
| POST | `/api/v1/memories/{id}/redact` | 先过读门（`memRead`）：无读权限 → `MEM-404`；能读但非 owner 且非 admin → `MEM-403` | 软删除；非 active → `MEM-409` |

检索有两种模式，唯一入口 `Store.SearchMode`（`server.go` 按 `match=` 选择；membership / 可见性 / `type` / `tags` 过滤与 `limit` 在两种模式下完全相同，`project=` 在两种模式下都在候选 SQL 内收窄）：

- **`match=all`（缺省，`Store.Search`）**：每个词都必须命中——≥3 字符的词作为 FTS5 trigram 短语 AND 起来，更短的词走子串匹配（中文短词常见）。适合 2–4 个关键词的短查询。
- **`match=any`（`memory/search_any.go` `anyTerms`）**：查询转小写，按词字符（字母、数字、组合附标 `unicode.IsMark`、`_`）的连续段切分，段内在 CJK 与非 CJK 文字切换处再切开。
  - 非 CJK 段是一个词：保留 3–40 字符且不在停用词表（英文虚词 + markdown/URL 噪声）中的词。
  - CJK 段（汉字、平假名、片假名、谚文）是没有词界的分句：切成重叠的 3 字符片段（「重试阶梯」→ 重试阶、试阶梯）；不足 3 字符的段不产生任何项；40 字符上限不适用于 CJK。
  - 去重后取至多 32 项：在词池（最长优先）与 CJK 片段池（各分句轮流取）之间交替，任一池用完由另一池补满，互不挤占。
  - 各项加引号以 OR 连接，按 bm25 × 新鲜度排序（见下）；只检查该顺序的前 max(100, 10×limit) 个候选，不再翻页（AND 模式翻页到底）。过滤后没有可用项 → 无命中（不报错）。用于长的描述性查询（领取时 recall）。

两种模式的排序相同（`memory/freshness.go`）：`w = exp(-age_days / stability_days)`，`age_days` 按
`memories.updated_at`（没有更新端点，等于 `created_at`）到查询时刻计算；
`score = rank × (1 + 0.5 × w)`（rank 为 FTS5 bm25，越负越好），升序，平分时按 `updated_at` 降序（按解析后的时刻比较——整秒与带小数秒的 RFC3339 并存；解析不了的视为最旧）、再按 id。
stability：`rule.*`、`doc.design`、`doc.decision` 不衰减（w = 1）；`doc.review`、`doc.audit` 及其他 `doc.*`
（时点快照）180 天；`experience.*` 90 天；`fact.*` 与 `methodology.*` 180 天；其他类型 90 天。`updated_at`
解析不了按完全陈旧（w = 0），未来时间按 age 0（w ≤ 1）。新鲜度最多把相关度放大 1.5 倍：相近的命中里新的在前，相关度高出 1.5 倍以上的旧条目始终排在
较弱的新条目之前。只有短词（无 rank）的子串扫描按 w 降序。实现上（`ftsPage`）先只从 FTS 索引读出全部命中的
rowid 与 rank，再沿 rank 顺序分批读取条目元数据，直到下界 (1+0.5)×rank 已劣于当前第 offset+limit 名，正文只读
本页；结果与对全部命中精确排序相同。

### 5.2 错误码 <!-- §5.2 -->

`MEM-400` 输入不合法（缺 `q`/`type`/`content`、`type` 不在命名空间内、可见性越界、frontmatter 不合法）；`MEM-403` 无写权限 / 能读但非 owner
的 redact；`MEM-404` 不存在或无权读（含 redact 时无读权限）；`MEM-409` 幂等键复用、redact 非 active 条目；`MEM-413` 正文超 1 MiB；
`MEM-503` FTS 查询/扫描失败；数据库事务失败 → `CORE-500`（未分类的服务端错误）（常量见 `internal/common/api/errors.go` `ErrMem*`）。另：key 限定在其他项目 → `AUTH-403`；
创建时项目不存在 → `CORE-404`。

## 6. 可见性 <!-- §6 -->

先按 project membership 过滤、再按可见性（`server.go` `memAllowed`）：`admin` 只给 admin；`private` 只给 owner
与 admin；`project`/`team`/`public` 给该项目任意有读权限的成员（三者当前行为相同）与 admin。

## 7. 备份 <!-- §7 -->

`pf backup` → `POST /api/v1/admin/backup`（admin）对 SQLite 做 `VACUUM INTO <data-dir>/backups/aihub-<ts>.db`
（`core/admin.go`）：memory 全部在库里，快照即包含全部 memory（含已 redact 的文档）；**不含** git 仓库（`git/`），
落在同一块盘上。生产完整备份由 `.polyforge/deploy.sh` 的在线备份完成（`docs/upgrade.md`）。

## 8. CLI 与领取时快照 <!-- §8 -->

- `pf recall --project P <query> [--any] [--type T|X.*|*] [--tags] [--limit 1..200，缺省 20]`：`--project` 必填且须在本机
  `[workspace]` 映射中；以 `excerpt=120` 调 search，缺省 `match=all`，`--any` 改用 `match=any`。`pf remember --project P --type T [--title] [--visibility project]
  [--tags] [--work-item] <content|-|@file>`。`pf memory show <id> [--max-bytes 262144] [--json]`。
  （`cli/verbs_aihub.go`；fragment：`skills/polyforge/verbs/{recall,remember,memory}.md`。）
- 领取时 daemon 以 WI 的 goal + labels 为查询（不带 content），用 `match=any`、限定该 WI 的项目，取前 10 条（`-recall-topk`，上限 50）写入
  capsule `runtime-state/recalled-memories.json`（`{work_item_id, query, top_k, items[{id,type,title,excerpt}], notice,
  created_at}`，摘录 1200 字符）；出错或无命中时写空列表 + notice，从不让领取失败（`runtime/recall.go`、
  `daemon/memface.go`）。driver 把它作为「非约束性历史」注入每个 step 的 prompt（`v2-converge.md#§1`）——每条只渲染一行
  `[type] title`（不含 id 与正文；title 为空时取摘录的第一个非空行），丢弃 id-only 噪声标题
  （`^Verification Report — \S+$`、`^Controller-sink artifact`），至多 10 行，无条目则不加该节（`driver/spawn.go`）。
  部署顺序：先升级 aihub 再升级 daemon——旧 aihub 忽略 `match=`、按 AND 检索，长查询于是没有命中，快照为空。
- step agent 不持有 aihub 凭据路径，记忆访问全部由 daemon 代理（owner 裁决 option C，2026-10-17 简化）。step 进程带
  `PF_WI_ID` / `PF_DAEMON_ID` 标记（与 step-origin 守卫、reaper 用的是同一组），另带 `PF_DAEMON_SOCKET`（本 daemon 的 socket）；其余 `PF_*` / `POLYFORGE_*` 被剥掉。
  `PF_WI_ID` 与 `PF_DAEMON_ID` 同时存在时 `pf` 进入 step 模式（`cli/stepctx.go`）：`pf recall` / `pf memory show` 走
  `GET /step/memories/search`、`GET /step/memories/{id}`，`pf remember` 同步走 `POST /step/memories`
  （`daemon/stepmem.go`；step-origin 守卫唯一放行的 step 写请求）。work item 不取自请求、也不取自调用方的环境变量：
  沿 UDS 对端 pid 的祖先链（含进程组长）找本 daemon 派生的 step 进程（某个活跃 job 的 spawn journal 任一条目的 pid，
  且该进程仍带着这个 job 的标记；最新的匹配条目给出 step 与 attempt）；必须恰好一个 job 匹配，且等于调用方最近的
  `PF_WI_ID`，否则拒绝。项目取自该 job（请求里别的项目 → DAEMON-403，项目外的 id 一律 MEM-404）。写入只收
  `experience.wrap`、要求 title、正文 ≤2KB、无疑似凭据值（`internal/common/stepmem`，CLI 先做同样检查），
  `work_item_id` / `project` 取自 job，以 aihub 已有的 idempotency key `pf-step-memory:<wi>:<step>:<attempt>`
  创建——丢了应答后的重试返回同一条记忆；aihub 不可达答 DAEMON-503，CLI 报清楚的错误，prompt 说明失败不让 step
  失败。其余直连 aihub 的动词、`pf doctor`、`pf daemon` 在 step 内一律拒绝，该次调用的 aihub client 拒绝一切请求，
  `cli.toml` 的 `api_key` 不被使用；`cli.toml` 仍在共享 HOME 下可读——这是防误用，不防恶意。
- 每个 step 的 prompt 带共享的召回节（`workflows/_shared/memory.md`）：开工前用 2–3 组不同关键词（含中英文）
  `pf recall --any`，读相关条目正文。只有 workflow 的最后一步写记忆（`workflows/_shared/memory-wrap.md`，变量
  `{{record}}` `{{skip}}` 为生效的记忆策略）：每个 WI 恰好一条 `experience.wrap`（title `<wi slug>: <做了什么>`，
  正文：做了什么、关键决定、遗留及合乎 record 的经验；不在 record 里但有价值的照写并加「建议新增条目：…」、打
  `policy-proposal` 标签）。
- 记忆策略：`.polyforge.yaml` 的可选段 `memory_policy:`（`default` / `<project>`: `{record: [...], skip: [...]}`；
  `default` 未声明的列表取内置默认，项目的列表追加到 `default` 之后、去重）。领取时 daemon 把该项目的生效策略写入
  capsule `runtime-state/memory-policy.json`（`runtime/memorypolicy.go`），driver 渲染进最后一步的 wrap 职责节；缺段＝内置默认
  （`workspace.DefaultMemoryPolicySection`），从不拒绝领取；段格式错误同整个文件无效一样拒绝领取（DAEMON-414）。
  `pf init` 在缺段时把默认段以文本追加到文件末尾（不重写原有字节）；`pf doctor` 只读：无文件 pass（内置默认），
  文件缺段 warn 并提示 `pf init`，有段 pass。
