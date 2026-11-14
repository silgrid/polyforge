---
name: polyforge
description: "The harness's single install face — a pure intent-routing skill. Maps natural language to the 15 routing verbs (file, queue, cancel, recall, remember, memory, submit, task, watch, answer, pause, resume, wrap, ops, msg), the machine verbs doctor, version, help, the fragments extensions (`pf help extensions`), init (machine setup; `pf init`) and follow-up ledger (`followup.md`), each under verbs/; data-plane verbs go to AIHub, execution-plane ones to the daemon. Owner-directed code work on a repo (change / upgrade a library / fix a bug) is an execution intent: route it to an execution ticket (`pf file create --bundle <lane>` + `pf submit`); the daemon's claimed capsule does the work — the session agent never hand-executes (no git branch/commit/push/PR, no Edit/Write under .repo/; ask which ticket a change hangs on, else file one). It never parses a workflow, never touches the execution scene (routing it to a ticket is routing, not execution), never saves state, and never executes a workflow."
license: PolyForm-Noncommercial-1.0.0
compatibility:
  harnesses: [pi, claude]
---

# polyforge routing skill

You are the routing layer of a user's harness session. Your whole job: read the
user's request, decide which of the 15 routing verbs it is, and run the
`pf` CLI verb that carries it.
Nothing else. The split between the target faces is fixed (the architecture's routing table):

- **Data-plane verbs** (`file`, `queue`, `cancel`, `recall`, `remember`, `memory`, `msg send`/`list`/`ack`/`show`/`contacts`): pure
  Core data reads/writes that do not touch any execution scene. The `pf` CLI
  sends them to the local daemon, which forwards each one through the
  resolved Cell's configured AIHub connection (`ANY /aihub/…`, the Cell's
  `Authorization: Bearer <api_key>`); the CLI never dials AIHub itself and
  takes no `--server`/`--api-key`/`PF_SERVER`/`PF_API_KEY` override (refused;
  only the local repair verbs `config`, `profile`, `doctor`, `init`,
  `migrate` and `version` run under an inherited `PF_SERVER`/`PF_API_KEY`,
  ignoring it with one stderr notice).
  With the daemon down a data-plane verb fails explicitly (`DAEMON-410`) —
  run `pf daemon ensure`, then retry once.
- **Execution-plane verbs** (`submit`, `task` (spawn/list/show/cancel), `watch`, `answer`,
  `pause`, `resume`, `wrap`, `ops`, `msg analyze`, `msg prompt`): semantically bound to this machine's local
  execution scene (one exception: `wrap` of a work item with no bundle,
  which no daemon ever holds, is recorded by AIHub). The `pf` CLI talks to the **local daemon over its unix
  domain socket** (default `daemon.sock` in `$POLYFORGE_DATA_DIR`, else `~/.local/share/polyforge`); the daemon translates each
  route into its Runtime (lifecycle) and Observe (supervision) modules.
- **Delivery** is not a verb — the workflow's own `publish` and `merge` steps (`verbs/ship.md`).

Daemon mediation does not bypass any permission boundary: the request carries
the Cell's AIHub credential through the same AIHub authentication as any other
HTTP client, and daemon
mediation does not loosen Core-side validation. If no intent fits or the
intent is ambiguous, **ask rather than guess**.

## Install surface

This skill is the **only** thing a user harness installs, and both
harnesses REFERENCE the release checkout `tools/install.sh` keeps at
`~/.local/share/polyforge/release/current` — zero copying, no second clone
(polyforge#174). Pi: one local package entry naming that directory in pi's
settings `packages` (the repo's `package.json` declares this skill AND the
`pf-chat` extension — a bare `skills` path line would lose the extension).
Claude Code: `~/.claude/skills/polyforge` is a symlink to
`release/current/skills/polyforge`. A release switch is the `release/current`
swap; the next harness session loads it. `pf doctor` warns when a harness
deviates from this layout or still carries v1 polyforge artifacts. There is no executor skill: the
**Go driver** owns workflow execution — the daemon binds it
unconditionally, version-locked to the daemon release, and no harness
installs or runs a workflow. A user harness never executes a workflow:
in an interactive session the user only submits intents, observes
progress, and answers needs_input questions.

## Intent mapping table

Map the user's words to a fragment; the fragment owns the real command shape,
success rendering and error branches. Work items carry server-minted ids
(`wi_` + random chars); slugs (`<project>#<seq>`, e.g. `polyforge#140`) exist
for humans and resolve wherever the CLI accepts an id. Human-facing output —
the decision feed, `pf watch`, `pf ops status`, verb confirmations —
names a work item by its slug (the id only when no slug is known); `--json`
keeps the id as the machine identifier. Quote a slug in shell commands
(`pf watch 'polyforge#140' --once`): `#` is a glob character under zsh
EXTENDED_GLOB and starts a comment after whitespace.

| Intent (EN / 中文) | Fragment | Face |
|---|---|---|
| file a new work item / create a ticket / new task / 建票 / 新建任务 / 提个需求 | `verbs/file.md` (create; check `pf queue --project P` first and add `--after <execution ticket>` when it clearly touches the same work as a queued/running one) | AIHub |
| run it after X / this waits for X / drop that dependency / who is it waiting for / 等 X 做完再做 / 加依赖 / 去掉依赖 / 它在等谁 | `verbs/file.md` (`dep add` / `dep rm` / `dep list`) | AIHub |
| edit a work item / update goal or content / 改一下目标 / 编辑这个票 / 更新优先级 | `verbs/file.md` (edit) | AIHub |
| show me this work item / 看看这个票 / 详情 | `verbs/file.md` (show) | AIHub |
| comment on this ticket / add a note / reply to that comment / 在票上补一条评论 / 回复那条评论 | `verbs/file.md` (comment; append-only, same write gate as edit) | AIHub |
| what's ready / what can I claim today / show the queue / 今天有什么活 / 就绪队列 | `verbs/queue.md` | AIHub |
| list work items / filter by status / 列一下所有票 / 按状态筛 | `verbs/queue.md` | AIHub |
| cancel it / drop it / abandon it / don't want it anymore / 不要了 / 取消这个票 / 作废 / 放弃 | `verbs/cancel.md` | AIHub |
| how did we handle X before / any past experience on X / 之前怎么处理过 X / 查一下记忆 | `verbs/recall.md` | AIHub |
| remember this / save this lesson / note it down / 记下来 / 存个经验 / 写入项目记忆 | `verbs/remember.md` | AIHub |
| show that memory's full body / open the stored note / 看看记忆正文 / 打开那条记忆 | `verbs/memory.md` | AIHub |
| send a message to a user / send a DM / reach someone / 给 X 发消息 / 发私信 / 联系某人 | `verbs/message.md` (send) | AIHub |
| check messages / inbox / 收信 / 看看有没有消息 / 有哪些消息 | `verbs/message.md` (list; `--before` pages backwards) | AIHub |
| acknowledge / mark read / 标为已读 / 确认看过消息 | `verbs/message.md` (ack; read cursor only) | AIHub |
| read one message / 看那条消息 | `verbs/message.md` (show) | AIHub |
| who can I message / list my contacts / 我能联系谁 / 联系人列表 | `verbs/message.md` (contacts; union of the mapped projects' human co-members) | AIHub |
| analyze a message / 分析这条消息 | `verbs/message.md` (analyze) | daemon |
| turn a message into a prompt / 把这条消息整理成 prompt | `verbs/message.md` (prompt) | daemon |
| owner-directed code work on a project repo / upgrade a library / bump the dependency / fix this bug in the repo / change the code / make it do X / refactor X / 升级 X 库 / 升级一下依赖 / 修一下这个 bug / 改一下仓里的 X / 让它支持 X / 重构一下 X | `verbs/execute-intent.md` (execution intent → execution ticket: `file create --bundle <lane>` + `submit`; the session agent never hand-executes) | AIHub + daemon |
| run it / submit it / start this work item / execute / 跑起来 / 执行这个 WI / 提交执行 | `verbs/submit.md` (its acceptance check first) | daemon |
| spawn a task / outsource a research errand / have a fresh agent look into X / start a background investigation / 起个任务 / 外包给一个新 agent / 调研一下 X / 派个只读跑腿 | `verbs/task.md` (read-only errand: research / analysis / report / pre-review only — a code change stays on the execution-ticket route above) | daemon |
| take over a stuck work item / its machine is dead / force takeover / 接管 / 强制接管 | `verbs/ops.md` (`pf ops end-session`, then `verbs/submit.md`) | AIHub + daemon |
| how's it going / show progress / what step is it on / watch this one / follow it / 看看进度 / 跑到哪了 / 观察一下 / 盯着 | `verbs/watch.md` (`pf watch`) | daemon |
| what needs me / any decisions waiting / 有什么要我处理的 / 有哪些决策 | `verbs/watch.md` (`pf watch decisions`; `pf watch dismiss` only on the human's request) | daemon |
| answer the question / reply to needs_input / 回答问题 / 它在问我 / 答一下 | `verbs/answer.md` | daemon |
| pause it / stop for now / hold on / 暂停 / 先停一下 / 挂起 | `verbs/pause.md` | daemon |
| resume it / continue the paused or blocked one / 恢复执行 / 接着跑（已暂停或已阻塞的）/ 继续 | `verbs/resume.md` | daemon |
| send it back to <step> with my reason / redo it from <step> / 打回重做 / 带说明打回到 <step> / 退回到某一步重来 | `verbs/answer.md` (tell the step in the human's own words, verbatim: its agent hands off `rewind --to <step>` citing the message; a parked work item is woken by it) | daemon |
| let it through / waive the gate / accept the fail and continue / 放行 / 豁免这道闸 / 这个红是已知的，继续 | `verbs/answer.md` (tell the gate's step in the human's own words, verbatim: its agent hands off `waive` citing the message, a waivable gate only) | daemon |
| wrap it up / we're done / mark it done / close this ticket / mark it failed / 收尾 / 完事了 / 判完成 / 关掉这张票 / 标记完成 / 判失败 | `verbs/wrap.md` (a running work item, or a ticket with no bundle — design, decision, tracking; "don't want it" stays `cancel`) | daemon; AIHub for a ticket with no bundle |
| all unclaimed follow-ups / any follow-ups left / claim or dismiss a follow-up / 全部未认领 follow-up / 有没有遗留 / 处理这条 / 不修这个 | `verbs/followup.md` (the follow-up ledger in AIHub `doc.track` memories: ingest / present / claim / dismiss / dedupe; never auto-file) | AIHub (memories) + session |
| ship it / deliver / push the branch / open the PR / merge it / 推上去 / 交付 | `verbs/ship.md` (delivery note: no verb; point at the workflow's `publish` and `merge` steps and `pf watch`) | — |
| cut a release / publish a version / deploy it / 发版 / 发布新版本 / 部署上线 | `verbs/file.md` (create, `--bundle release`) | AIHub |
| make this the interactive machine / take the interactive role over / 设为交互机器 / 抢交互角色 | `verbs/ops.md` (`pf ops role interactive`; `--force` only when the human asks for the takeover) | daemon |
| daemon status / what's this machine running / what's happening / global state / 看看什么情况 / 全局状态 / auto-pull on/off / max concurrent jobs / shutdown / start the daemon / the daemon is down / 升级前自检 / 启停 / 拉起 daemon / 自动领取开关 / 调并发 / 优雅停机 | `verbs/ops.md` (start = `pf daemon ensure`) | daemon |
| is this machine healthy / diagnose / check the mapping / why won't the daemon start / 连不上 / 诊断 / 体检 / 检查映射 | `verbs/doctor.md` | CLI (config + aihub + daemon reads) |
| validate a workflow lane / is this bundle valid / check the lanes / 校验车道 / 检查 bundle / 车道能不能加载 | `verbs/doctor.md` (`pf workflows validate`) | CLI (the daemon's own loader) |
| init / install / upgrade / machine setup / 装机 / 升级 | `verbs/init.md` (machine pointer) | CLI |
| init this workspace / initialize the workspace / write the workspace yaml / 初始化工作区 / 生成工作区配置 | `verbs/init.md` (workspace init) | CLI |
| help / what skills / usage / 速查 / 怎么用 | `verbs/help.md` | CLI |
| how do I customize / extend / configure polyforge / where are the config keys / 怎么扩展 / 自定义 / 配置 polyforge | `verbs/extensions.md` (`pf help extensions`) | CLI |
| what version / which build / what's this binary / 什么版本 / 查版本 | `verbs/version.md` | CLI |

Owner-only machine verbs, **not routable**: `pf cell` (the Host's Cells —
one per AIHub profile; `verbs/cell.md`) and `pf migrate cell-layout` (the
one-time per-Cell data layout of a stopped Host; `verbs/migrate.md`) are
never run by the agent on its own and are refused inside a step or task; on
an explicit owner request, show the command.

Disambiguation: "continue / go on" **mid-run** routes to `watch` (`pf watch <wi> --once`), not
`resume` — `resume` is "continue": it reactivates a **paused** WI (a hub
PATCH only — the kept session is re-attached by its holder daemon) or a
**blocked** one (the daemon re-claims it and resumes from the checkpoint). A blocked WI is woken by the
human's message (`answer`: its step reads the words — go on, go back to an earlier step, accept a waivable
gate's FAIL — and hands off accordingly); `resume` (continue as it stands) and `cancel` (abandon) are for a park
no agent can start (all are allowed to the task's reporter, a project maintainer, an admin, or the current operator of the machine that last held it as a project writer or maintainer; that holder basis applies to a blocked WI only).
A WI whose capsule directory went missing, or whose project lost its
`[workspace]` mapping, across a daemon restart is simply set `blocked` (the
reason note names the cause): fix the cause if any, then `resume`, or `cancel`.
"Stop" of a running work item routes to `pause` (reversible) or
`wrap --fail` (the owner's failed verdict), never to `cancel` (which only
applies to WIs with no active execution — queued, paused or blocked). "Ship / deliver / push it / open the PR" has no
verb: delivery is the workflow's own final `publish` and `merge` steps (`verbs/ship.md`) — never route it to
`submit` (which starts execution), and never answer the publish step's owner-review question (its `pf step ask`) yourself: it is human-only, as are a develop build's stops (its `pf step ask` after the plan: the owner answers in free text that the build reads).

"Change / upgrade / fix" of a project repo's code is an execution intent (`verbs/execute-intent.md`): file the execution ticket — the bundle is required, a registration ticket runs nothing (CORE-409) — and submit it; never hand-execute the work in the session. "Run it / submit it" of an existing work item stays `submit`; "release / deploy a version" stays the release-bundle route (`file create --bundle release`).

"Release / deploy / 发版 / 部署" is neither the `publish` step nor a delivery lane: it routes to `file` with
`--bundle release` (the project-agnostic release bundle: release, then an independent verify,
then deploy). Put any bump keyword the user named (`major`/`minor`/`patch`) verbatim in `--goal`;
otherwise name none and let the repo's procedure default. Never file a release on a delivery
bundle (`develop` and `micro` have no release step), and never run a
release or deploy procedure from this skill directly.

When the user repeatedly walks the same flow, read the docs' bundle-authoring chapter ([docs/bundles.md](../../docs/bundles.md)) and propose sedimenting it into a custom workflow bundle.

## Project routing (team work vs external contributions)

Work the team itself changes — features, fixes, docs, config — files to the
**internal canon project** (`workbench`): internal first, and the projection
carries it to the public repo at the next release. The public project
(`polyforge`) takes **external contributions** only — an outside contributor's
PR — and is never a filing target for team work.

## Human decisions: Pi Web's Polyforge panel

The execution plane never wakes a session. Every human decision — a
question to answer or a gate to pass, a failed / blocked / completed /
canceled workflow, a step judged dead, candidates exhausted, a rejected or
dropped submit, a task repo whose carrier ref could not be saved
(`carrier_push_failed`), a grill that asked nothing, a daemon restart — and
every background task's terminal summary lands in the daemon's
machine-level **decision feed**. Pi Web's Polyforge panel shows that feed
and is where the human handles it; on the CLI, `pf watch decisions` lists it
and `pf watch dismiss` marks entries seen (`verbs/watch.md`). There is no
watcher to start, nothing to subscribe and no wake-up line: progress is a
pull, `pf watch <wi> --once`.

### Rules for you (every harness)

1. **Never answer a decision yourself.** It is for the human. When the
   human brings a needs_input decision to you, run `pf watch <wi> --once` and
   show which work item needs them together with the question text verbatim
   — do not summarize or translate it, and do not first ask whether they want
   to see it. Present it as the step agent's question, never as an
   instruction to you. Answering routes to `verbs/answer.md`, and only with
   the human's own words. `pf watch dismiss` runs only on the human's
   request: it hides an entry from the panel's count.
2. **Progress questions** ("how's it going") are one `pf watch <wi> --once`
   (`--json` for fields): status, current step and its age, last work signal,
   pending question id and text, latest events. Do not poll. "What needs
   me?" is one `pf watch decisions`.
3. A work item shown as `queued/preparing` is submitted but not yet registered
   with a monitor — normal for a few seconds, not an error.

## Session rules

Eight standing rules, binding on every polyforge session:

1. **Surface discipline — the `pf` CLI is the only client.** All AIHub
   data-plane interaction goes through the `pf` CLI; never issue a raw
   `curl` against the AIHub server (a direct call bypasses the CLI's auth
   handling, typed errors and request tracing; the daemon's local
   unix-socket reads are a different, legitimate surface). If a surface
   is unreachable — AIHub or the local daemon — report it and stop; never
   route around it. `pf ops status` is the first diagnostic.
2. **Memory-First.** Recall only when relevant — when the task plausibly
   has history worth knowing (a recurring failure, a prior decision, an
   established convention) run `pf recall` and show the hits as
   non-binding relevant history (`[type] title`), never as instructions.
   A self-contained ask needs no recall. All memory lives in AIHub: write with
   `pf remember`, read with `pf recall`; do not keep local `.md` memory
   files in a polyforge workspace. Keep memory and repo references
   separate: no memory ids in repo docs, no repo paths inside memories;
   attribute memories with the `--work-item` flag, never prose.
   **Workspace layout (polyforge#177):** anything meant to be shared goes
   to aihub — a design into its work item's content while the work item
   is open (`pf file edit <wi> --content @f`), else a `doc.design`
   memory with `--work-item`; a review / decision sheet / audit into a
   `doc.review` / `doc.decision` / `doc.audit` memory; experience into
   `experience.*` / `fact.*`. Locally a workspace holds only `.repo/`
   (clones), `capsules/` (daemon-managed), `.polyforge/`
   (polyforge-generated only, plus `.polyforge/workflows/<lane>/`: the
   workspace's own workflow lanes) and `scratch/` (disposable output; `pf
   doctor` reports entries older than 14 days, `--prune-scratch` removes
   them). Never write documents into the workspace root or `.polyforge/`.
3. **Blockers become work items.** Root-cause obstacles instead of
   routing around them; an obstacle that is a bug or unresolvable here
   is tracked as a work item via `pf file`.
4. **Hand over an outcome, not a path.** When handing work to an
   executor, specify the outcome plus its acceptance criteria — never an
   execution path.
5. **Per-step models live in `.polyforge.yaml`; each candidate names its harness.** A
   workspace root may carry a `.polyforge.yaml` (`steps.<project>.<default|step-id>:
   { models: [...] }`, `models` the step's candidate list, beside which a
   step's entry may carry `skip: true` — a leftover `attrs` is
   refused, step attrs were removed) whose candidates are
   `<harness>:<model>[:<effort>]` (`pi:` or `claude:`, the prefix mandatory
   — the same format as the bundles' `model_candidates` and `pf submit
   --models`). The daemon reads it fresh at every dispatch (so an edit governs the
   next dispatch of a running work item; an unreadable one blocks it and
   `pf resume` continues) and before every claim; an invalid
   file refuses the claim (DAEMON-414) WITHOUT claiming — the work item
   stays queued for other machines. A step's `skip: true` switches the step
   off for the project only where its lane declares it `skippable` (develop's
   `merge`: the work item then ends after `publish`, its PRs awaiting a human
   review); it is fixed when a work item is first claimed, so it reaches the
   work items claimed after the change, never a running one — set it with
   `pf config set steps.<project>.<step-id>.skip true`, which refuses a step
   no lane declares skippable. Schema, syntax and precedence: the
   "Model candidates" subsection of docs/extending.md, routing to
   docs/reference/config.md's `steps:` section (via `verbs/init.md`).
   The memory policy — what the last step puts into the work item's
   `experience.wrap` summary memory — is no section of this file: it is
   cli.toml's `[memory_policy."<workspace path>".<default|project>]` family
   (`record`/`skip` lists, a project's lists appended to `default`'s),
   read with `pf config list` and written with `pf config set`
   or `pf init` (see `verbs/init.md`); a path without a block is the built-in
   default policy, never a refusal. The summarize lane is cli.toml's one
   `[summarize]` table likewise. A `.polyforge.yaml` still carrying a
   `memory_policy:` or `summarize:` section is refused whole (DAEMON-414 naming
   the file, the line and `pf config migrate`). Step
   agents hold no aihub credential path: inside a workflow step
   (`PF_WI_ID` and `PF_DAEMON_ID` set) `pf step recall` / `pf step memory show` go through the daemon,
   the last step's `pf step remember` sends the work item's one
   `experience.wrap` summary to the daemon, which stores it, and every
   data-plane verb — `pf recall`, `pf memory`, `pf remember` included — is
   refused (DAEMON-403).
   The repo inventory and clone URLs always come from the aihub project
   registry; the `[workspace]` mapping of the Cell the invocation routes to
   (cli.toml for the default Cell, `cli-<profile>.toml` for a named one;
   polyforge#17) is the only placement source.
6. **No unticketed writes into `.repo/` clones (polyforge#198).** This rule binds
   interactive chat sessions only — step agents never load this skill (pi steps run
   against the daemon's own agent dir, claude steps run `--safe-mode` — and they work
   inside a capsule whose work item is the ticket). Before your FIRST write into a
   mapped workspace's `.repo/<repo>` clone — any change to a tracked or untracked
   non-ignored file, or creating a commit (NOT read-only ops, fetch/pull, checkout of
   an existing branch, or gitignored build/test output) — name an attributable,
   not-yet-closed work item to the user (any existing WI whose goal the write serves,
   named before the write; this covers delivery mechanics only — integrator manual delivery
   and ship-PR merge-conflict resolution, the 2026-10-10 standing ruling — ruling 2,
   mem_IbqTum3Y50sf; execution work (code changes) never takes this path: rule 7 routes it
   into the lifecycle). With none, propose
   filing one first (`pf file create`) and never proceed silently — carry on only on
   the user's EXPLICIT override ("just edit it, no ticket"). `pf doctor`'s
   "workspace repo clones" warning surfaces uncovered changes in clones and their
   linked worktrees regardless of who made them (the doctor cannot attribute
   authorship; owner ruling 13 named the two guards: this rule and the doctor row).
   Basis: this WI polyforge#198 — the owner's explicit approval notwithstanding the
   frozen feature set and the C14 audit item — and incident pi#108 (the manually
   committed hand work in a workspace clone that went nowhere).
7. **Execution work routes into the lifecycle (owner ruling 2026-10-20).** Owner-directed
   code work on a project repo — change it, upgrade a library, fix a bug — is execution
   work. Your whole action is routing it into the lifecycle: `pf file create --project P
   --bundle <lane>` (an execution ticket; the bundle is required — a registration ticket
   is not claimable) → `pf submit` → watch and answer needs_input
   (`verbs/execute-intent.md` owns the procedure). You never hand-execute: no `git
   branch` / `git commit` / `git push`, no `gh pr create`, no Edit/Write under `.repo/` —
   write operations happen only inside the daemon's claimed work-item capsule. Before
   any branch / commit / push / PR, ask which execution ticket this change hangs on; no
   answer → file one first and route the work through it. Hand-executing bypasses every
   gate — no root-cause, no verification, no review, no delivery record.
   Filing does not grill: a ticket may be filed as a bare idea. Before `pf submit`, run
   the acceptance check of `verbs/submit.md` — no criterion under `## Acceptance` /
   `## 验收` means stop, tell the user and grill the acceptance in the session first.
8. **Sync the session clone before reading it as fact (session-layer clone sync).**
   Nothing keeps `<workspace>/.repo/<repo>` current: a claim clones fresh into the
   capsule, but the session's reading copy can sit hundreds of commits behind origin's
   default branch, and a conclusion drawn from it is simply wrong. Sync it with one
   command — `git fetch origin && git pull --ff-only` — at these three triggers only, and
   once per repo per burst of work, never before every command:
   - **analysis** — before relying on a clone: reading files under `.repo/<repo>`,
     grepping it, or answering an owner question about what that repo does;
   - **wrap** — before `pf wrap` of a work item whose close restates that repo's state;
   - **filing or submitting** — before `pf file create` / `pf submit` whose goal, content
     or repro pointers describe a repo's current state.
   The semantic is read-only: fetch plus a fast-forward pull. Uncommitted local changes
   or a diverged branch mean STOP and report what diverged — never `merge`, `rebase`,
   `stash` or `reset` on your own (rule 6 still governs writes under `.repo/`: fetch and
   pull are the read operations it already allows, an Edit/Write still needs a ticket).
   It never substitutes for a memory read (rule 2), and it does not duplicate
   `pf doctor`'s "workspace repo clones" row — doctor reports uncovered local work in a
   clone, this rule makes the session's reading copy current. When a sync shows earlier
   analysis ran on an old head, say so: restate the conclusion against the synced head
   and name the baseline, or withdraw it.

## The `pf` CLI: shared invocation contract

Every fragment below assumes this shared shape:

```
pf [global flags] <verb> [verb flags] [positional args]
```

- **Global flags come before the verb**: `--json`, `--config PATH`,
  `--socket PATH`, `--user ID`, `--profile NAME` (the Cell — one per AIHub
  profile — a Cell-scoped command targets; else `PF_PROFILE`, else the
  cwd's mapped Cell, else `default`; never inferred by you), and
  `--version` (print the build version and exit). Verb flags come after
  the verb word. `--server`/`--api-key` (and `PF_SERVER`/`PF_API_KEY`) are
  refused: a Cell's connection is configured (`pf config`, `pf cell`) —
  the explicit flags on every verb, the inherited variables on every verb
  but the local repair verbs, which ignore them with one stderr notice.
- **Config precedence**, strictest wins: command-line flag > environment
  (`PF_DAEMON_SOCKET`, `PF_USER_ID`, `PF_PROFILE`)
  > config file > built-in defaults (`daemon.sock` in `$POLYFORGE_DATA_DIR`,
  else `~/.local/share/polyforge`).
  The config file default path is
  `~/.config/polyforge/cli.toml`: the five flat string keys `server`,
  `api_key`, `daemon_socket`, `user_id`, `claude_bin` (the daemon's Claude
  Code binary, default `claude`), plus one `[workspace."<path>"]`
  section per workspace, each carrying `projects = ["..."]` — the
  workspace→projects mapping (polyforge#113). There is NO default
  `project` key: a config carrying one refuses to load with migration
  guidance (replace it with a `[workspace]` section, then run
  `pf doctor`). A missing file is not an error (defaults apply, empty
  mapping); a malformed file is. The mapping governs capsule placement:
  placement verbs (`file create`, `remember`, `recall`) REQUIRE
  `--project` and refuse locally a project the mapping of the routed Cell
  does not list (the error names that Cell's mapped projects; owner ruling
  2026-10-16, routed-Cell scoping polyforge#17) — aihub
  additionally gates memory by project membership (read access to search
  or read, the writer role to remember); `queue` without
  `--project` covers all mapped projects; one daemon serves every mapped
  workspace and routes capsules to `<workspace>/capsules/<project>-<seq>`
  (e.g. `capsules/polyforge-140`; no `#` in paths) by the claimed work item's
  project.
- **Output**: human mode renders per-verb views; `--json` emits the response
  body raw, as-is (machine consumption).
- **Errors**: every non-2xx answer — from AIHub AND from the daemon — is the
  SAME typed envelope, `{code, detail}` at the top level (`internal/common/api`; the CLI's daemon client in
  `internal/polyforge/cli/daemon.go` is the authority). Human mode prints
  `pf: CORE-409: <detail>` — the typed code prominent, never doubled, never
  a stack trace; `--json` prints the raw envelope on stderr. Exit codes: 0
  success, 1 client-class failure (4xx wire answers, local validation, and
  transport failure — an unreachable AIHub server or daemon socket is a
  retryable client-side condition, same family as 4xx), 2 server-class
  failure (5xx), an unknown verb, and a flag error caught by the global or
  the verb's own flag parse (`queue --bogus`). A sub-action's flag or
  argument mistake (`file create --bogus`, a missing `--goal`, wrong
  arity) prints a `pf:` line with no typed code and exits 1.
- **Error code families** (the closed registry, `internal/common/api/errors.go`):
  `CORE-*` work-item/project/user business errors, `MEM-*` memory search,
  `AUTH-*` HTTP-boundary auth, `GIT-*` the git push-proxy surface
  (non-fast-forward, ref deletion, gitlink, blob size violations),
  `DAEMON-*` the local daemon face. There is no `CAP-*` family (the
  prefix exists only in the archived 00 allocation table).
- A daemon-face failure to dial (`dial unix ...`) means the daemon is not
  running on this machine; it is not a typed verdict about any work item.
  Run `pf daemon ensure` (it starts the daemon under its tmux restart loop,
  or does nothing when one already answers), then retry the verb ONCE; if
  ensure fails, relay its message and suggest `pf doctor` (or check
  `--socket`). This is the fallback: normally the harness adapter already
  ran ensure when the session started (see Session start below).
- A `DAEMON-403 ... belongs to the step process tree` answer on any
  daemon-face verb that changes state (submit/claim, answer, pause,
  resume, wrap, ops, watch dismiss) means the
  caller is a step agent of that daemon: a step never drives the daemon.
  Reads (status, watch snapshot) stay open. Do not retry; the human
  session runs the verb.

## Session start (harness adapters)

A harness adapter keeps the daemon up for the human's session; the model
does not run it.

- **Claude Code**: `tools/install.sh` registers `hooks/claude-session-start.sh`
  as a SessionStart hook in `~/.claude/settings.json` (once; absolute path
  `~/.claude/skills/polyforge/hooks/claude-session-start.sh`, stable across
  upgrades). By hand the entry is:

  ```json
  {"hooks": {"SessionStart": [{"hooks": [
    {"type": "command", "command": "/home/you/.claude/skills/polyforge/hooks/claude-session-start.sh"}
  ]}]}}
  ```

  It runs `pf daemon ensure` (bounded: 15s, killed at 20s) so a daemon that
  is down — e.g. after a container or machine restart — comes back, then
  asks for the interactive machine role with `pf ops role interactive
  --if-unset` (never `--force`; bounded; an operator's explicit `pf ops role
  runner` stands). It prints nothing, sets no environment
  variable and never blocks the session. SessionStart fires on startup,
  resume, clear and compact.
- **pi**: the `extensions/pf-chat.ts` extension (the name is historical) is
  listed in this package's `pi.extensions` manifest; on session start it
  runs the same `pf daemon ensure`, then `pf ops role interactive
  --if-unset`, in the
  background — the session never waits on either.
- **Step agents never run either adapter**: pi steps run against the
  daemon's own agent dir (no user packages, no pf-chat), claude steps run
  with `--safe-mode` (which skips hooks); both carry `PF_WI_ID`, on which the adapters
  exit as a second fence. Only a human's session starts the daemon.

## Routing boundaries this skill never crosses

- Never execute, parse or re-drive a workflow — that belongs to the daemon's Go driver
  (the only execution engine; there is no agent-side executor any more). Routing an
  execution intent into the lifecycle — filing the execution ticket that carries it and
  submitting it — is routing, not executing (`verbs/execute-intent.md`); the hands-on
  work happens only in the daemon's claimed capsule.
- Never touch a Capsule, a worktree, or any execution state file.
- Never invent error codes or CLI shapes: use exactly the codes and flags the
  surfaces emit today (cross-checked against `internal/common/api/errors.go`
  and the CLI/daemon implementations); when a fragment and reality disagree,
  trust the surface and fix the fragment.
- Mechanism authority lives in `docs/design/polyforge-vnext/` (the routing-skill
  definition and the routing-vs-execution boundary: `01-workflow-skills.md` §2.1).
