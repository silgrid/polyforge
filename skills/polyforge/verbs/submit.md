# submit — submit a work item for execution on this machine

Execution-plane verb: the `pf` CLI talks to the local daemon over its unix
socket (claim / resume route family). Explicit submit of
exactly one work item; always legal, also while the daemon's auto-pull
(`pf ops auto on`) is on. To see what is ready, use `pf queue --ready`.

## Triggers

- EN: run it / submit it / start this work item / execute / take this one
- 中文: 跑起来 / 执行这个 WI / 提交执行 / 认领开跑

"Take it over from that (dead/stuck) machine" is NOT a submit flag: end the
stuck session first with `pf ops end-session` (see `verbs/ops.md`), then
submit as usual.

## Command

```
pf submit <id> [--priority low|normal|high|urgent] [--models <harness>:<model>[:<effort>],...] [--allow-no-acceptance] [--allow-no-analysis]
```

`--priority` defaults to `normal`. `--models` is a manual model-candidate
list for this work item: it is the candidate chain of every step (advancing
on model-class failures like any list), kept by this daemon only (across its
restarts and `pf resume`); a re-claim on another machine uses that machine's
settings. Non-empty, comma-separated, no empty
or duplicate candidates, else a local bad-input refusal. Every candidate is
`<harness>:<model>[:<effort>]` (e.g. `--models
claude:claude-opus-5-5:high,pi:example-gateway/model-a`); a candidate without a
prefix, with an unknown prefix, an empty model or an effort the harness does
not take is refused up front, naming it. Syntax and precedence (`--models`
is the highest level): the "Model candidates" subsection of
docs/extending.md (routing to docs/reference/config.md's `steps:`
section, the one authority). Before the daemon is
involved, the CLI prechecks the work item against Core with the caller's own
api key, so obviously doomed submissions fail fast with a visible verdict.

## Before submitting: the placement precheck

`pf submit` always targets the local daemon, and the claim lands where
this machine's mapping says — resolve the placement and the clone context
from the existing faces before submitting (no preview flag exists):

1. The target workspace: the `[workspace."<path>"]` sections of
   `~/.config/polyforge/cli.toml` (XDG) — the section whose `projects`
   carries the ticket's project. The machine: `pf ops status`'s
   `machine_id` (the local daemon — the claim's target machine).
2. The repos the claim will clone: `pf file show <id>`'s
   `repo_inventory` when the work item declared one (exactly that
   list), else `pf projects get <project>`'s `repos`.
3. The session cwd is the target workspace and every one of those
   repos is cloned under `<workspace>/.repo/<name>` → submit directly,
   no extra hop.

A target workspace that differs from the session cwd (a cwd inside no
mapped workspace counts as differing), or a repo without a local clone:
ask the owner first — "this ticket will land in X", the machine id and
the workspace — and submit only after the owner agrees. The owner
declines → no `pf submit` (nothing is submitted on the owner's behalf).

When the ticket's goal or content restates a repo's current state, sync that clone
before submitting: SKILL.md session rule 8.

A ticket bound to a lane that is no longer installed (the four lanes `develop`
superseded were removed) is refused at submit and at claim (the
installed bundle is missing; the work item stays queued):
re-bind it while it is queued and never claimed (`pf file edit <id> --bundle develop`),
or cancel it. New development work is filed on `develop` (`verbs/file.md`).

## Before submitting: the acceptance check

An execution ticket on a lane that requires acceptance carries its acceptance
before it is submitted; filing may record a bare idea, submitting may not. The
check applies only when the ticket's lane requires acceptance — its
`workflow.yaml` key `acceptance`, default `required`; a lane declaring
`acceptance: none` (integrate, release) is submitted as is, and a lane that
cannot be read counts as requiring it. Read the ticket (`pf file show <id>`)
and look for at least one criterion (a top-level list item) under
`## Acceptance` or `## 验收`. Present → submit. None → do not run `pf submit`:
stop and tell the user 「这张票需要先交互、补齐验收才能提交」, then grill in the
session: what it does, why, its scope, what it does not do, and
when it is done. Survey the code with a read-only subagent where the answers depend on
it. Write the decisions into the ticket's background and each testable
criterion, marked `— carried by build`, into `## Acceptance` (`pf file edit
<id> --content @file`) — each criterion written in the `Acceptance carrier`
slot's shape (`verbs/file.md`): its proof — the command that shows it holds
and the directory that command runs in — before the carrier, which stays
last. Then submit.

Research, experiments and feasibility checks happen in the session before
submitting; only their conclusion goes into the ticket — the claimed build
plans and implements, it does not investigate.

Re-check the lane while grilling: `micro` only for a pure text change inside
the paths every touched repository declares with `micro_path`; a change to
code or tests goes to `develop` (re-bind a queued ticket with `pf file edit
<id> --bundle develop`).

`pf submit` refuses an execution ticket with no criterion when its lane
requires acceptance (exit 1, nothing is claimed): the lane's steps need a
criterion to carry, so the ticket needs interaction first. `pf submit <id>
--allow-no-acceptance` bypasses the check — only when the owner explicitly says
to submit without acceptance, never on your own.

## Before submitting: the analysis check

Beside the acceptance check, an execution ticket on a lane that requires
acceptance carries a **pre-submit analysis**: a `## 分析` (or `## Analysis`)
section with content in the ticket body. `pf submit` refuses the ticket
without one (exit 1, nothing is claimed) and names the way out;
`pf submit <id> --allow-no-analysis` bypasses the check — only when the
owner explicitly says to submit without analysis, never on your own. The
check applies on exactly the lanes the acceptance check does (a lane
declaring `acceptance: none` is submitted as is), and it never judges what
the section says: content is enough. A ticket that already made release
progress (its event history carries a session lifecycle event or a resume)
is past the check: the `pf ops end-session` takeover resubmits exactly
such a ticket with a plain `pf submit` (polyforge#47).

- **Who runs it (polyforge#72's mechanism)**: `pf file create` of a ticket
  whose lane requires acceptance and whose body has no `## 分析` section
  asks this machine's daemon to schedule one analysis task at once — the
  create's output names the task id, or says 已排队 when the task lane is
  full (the daemon queues what it cannot dispatch, survives its own
  restart, and re-queues an interrupted analysis at most twice). The task
  writes its draft itself, as it analyzes, through the ONE write a task may
  make: `pf task analysis-write --content <text|@file|->`, which replaces
  only the analyzed ticket's `## 分析` section; everything else a task
  writes is still refused. The section the mechanism writes carries the
  unconfirmed marker until the owner confirms: until
  `pf file analysis confirm <id>` runs, the daemon's auto-pull skips the
  ticket and `pf queue` marks it 需要交互：分析待确认. A stopped analysis leaves a
  comment on the ticket; what it wrote stays. If no analysis was scheduled
  (the create's notice said 未安排分析), the fallback is the session's own
  errand — a read-only `pf task spawn` over the ticket and the code it
  touches, the session writing its report with
  `pf file edit <id> --content @file` — and on the owner's explicit skip
  the dated line below.
- **The report's fixed format — 7 items, defined here and here only** (the
  filing, task and execute-intent fragments reference this list):
  1. 改动范围（文件 / 模块）；
  2. 每条验收的证明命令和运行目录；
  3. 断言旧行为的已有测试与文档（需要同步修改的义务）；
  4. 与其他未完成票的依赖或冲突；
  5. 相关记忆（`pf recall` 命中）；
  6. 风险；
  7. 待决问题（每条给选项和推荐，不拍板）。
- **Writing it in**: the auto-scheduled analysis task writes the report
  into the ticket's `## 分析` section itself (`pf task analysis-write`);
  the owner then confirms it with `pf file analysis confirm`
  (`pf file analysis confirm <id>`). A session errand writes with
  `pf file edit` (`pf file edit <id> --content @file`); on the owner's
  explicit skip, one dated line 「免分析（<date>，<who>）」 goes in that
  section. Then submit.
- **The boundary against grilling**: the analysis only establishes facts —
  what the code does, what memory answers, which existing tests and docs
  assert the old behavior — and lists the open questions with a
  recommendation each, never deciding them. Deciding is the grill's: the
  open questions and the acceptance are settled with the owner (the
  acceptance check above). What the code or `pf recall` can answer belongs
  to the analysis; what needs the owner's preference or a trade-off belongs
  to the grill.
- **The pre-submit confirmation rule**: a small ticket is submitted directly;
  every other ticket is explained to the owner first, in plain words — what
  it will do, what counts as done, what the trade-offs are — never by pasting
  the acceptance section verbatim; submit only on the owner's explicit
  confirmation. A ticket is small when all four hold at once (the definition
  is project-independent):
  1. the requirement is **very clear** — the "Acceptance at creation"
     definition of `verbs/file.md`;
  2. the Down-lane check's `expected diff size` is small (`verbs/file.md`);
  3. its `lane risk surface` is low;
  4. the work is not on `docs/concepts.md`'s "never unattended" list.

## Success

`pf submit` answers the claim's verdict. When this machine has a free
execution slot, the daemon claims the work item at aihub before it answers;
otherwise the entry waits on the daemon's queue and the answer says so.
Either way the first line is `submitted <slug> (accepted by the local
daemon)` — the daemon holds the work item.

- **Claimed** (a slot was free): the placement line follows —
  `placement: claimed by machine <machine_id>, capsule <capsule_path>` —
  the machine that holds the claim and the workspace / capsule landing path
  the work item occupies. It is printed only once aihub has answered the
  claim (the capsule clone may still be under way). A placement the daemon
  could not resolve prints nothing beyond the acceptance line.
- **Queued** (every slot is busy, aihub could not be reached, or the daemon
  is shutting down): instead of the placement line, ``queued: <reason> —
  nothing is claimed yet; follow it, and any later refusal, with
  `pf watch <id>` ``. Nothing is claimed: the dispatcher claims it when a
  slot frees (priority-desc against the machine's concurrency cap). A
  refusal at that later claim is recorded as the work item's
  `submit_rejected` event — `pf watch <id>` shows it, and so does the
  decision feed.

When the mapped workspace lacks a git clone under
`<workspace>/.repo/<name>` for a repo the claim will clone, a warning
line names each missing repo (`warning: no local clone under .repo/ for
repo(s) <names> — the claim clones fresh`): the submit is still
accepted — the claim clones fresh, no override flag exists.

`--json`: the raw answer, with the fields `accepted`, `work_item_id`,
`state` (`claimed` or `queued`), `queued_reason` (on `queued` only),
`machine_id`, `capsule_path` and `missing_repos`. `machine_id` +
`capsule_path` are present only when `state` is `claimed` and the
placement is known; `missing_repos` is absent when every clone is present.

Relay the placement or `queued:` line and any missing-clone warning to the
owner, word for word. A warning the precheck above did not predict: tell the owner
`pf pause <id>` / `pf cancel <id>` can stop the work item before it
invests work.

## Failure branches

A claim aihub refuses is also recorded as the work item's `submit_rejected`
event — `pf watch <id>` and the decision feed show it — whether the refusal
came back on the spot or at a later claim.

| Code | When | What to tell the user |
|---|---|---|
| `CORE-404` | Unknown or invisible id (CLI precheck) | No such work item — check the `wi_` id. |
| (no code) `submit: <slug> is terminal (<status>)` | The work item is already wrapped/failed/canceled (CLI precheck) | It is finished and can never be claimed again — file a new work item instead. |
| (no code) `submit: <slug> has no acceptance section …` | An execution ticket with no criterion under `## Acceptance` / `## 验收`, on a lane that requires acceptance (`acceptance`, default `required`; CLI check, exit 1; nothing submitted) | 这张票需要先交互、补齐验收才能提交 — run the acceptance check above, then submit. |
| (no code) `submit: <slug> has no analysis section …` | An execution ticket with no content under `## 分析` / `## Analysis`, on a lane that requires acceptance (the same lanes as the acceptance check; CLI check, exit 1; nothing submitted) | 这张票需要先提交前分析 — run the analysis check above (a `pf task spawn` errand, the report into `## 分析`), or the owner's dated 免分析, then submit; `--allow-no-analysis` only on the owner's explicit say-so. |
| `DAEMON-409` | The work item is already active or queued on this daemon, or a `pf wrap` / `pf pause` landed while this submit's claim was materializing | Wait for the running attempt; after a wrap or pause, `pf watch <id> --once` shows where it stands. |
| `DAEMON-410` (`SHUTTING_DOWN`) | The daemon is shutting down | Wait for the shutdown to finish and the daemon to restart, then submit again. |
| (exit 0) `queued: aihub could not be reached; …` | The hub could not be reached when the daemon went to claim (transient) | Nothing was claimed yet; the entry stays queued in the daemon and is retried with backoff (1s doubling to 30s). Check aihub reachability; `pf watch <id> --once` shows it once claimed, or the `submit_rejected` event if the claim is later refused. |
| `CORE-409` `DEPENDENCY_BLOCKED` | aihub refused the claim: a blocker of the work item (an `--after` edge) is not merged yet (exit 1). The message reads `<slug> was not submitted — aihub refused the claim; nothing stays queued: …; waiting for <blocker> (<status>) — wait until the blocker is merged (wrapped), then pf submit <slug> again, or remove the edge: pf file dep rm <blocker> <slug>` | Nothing was submitted or kept queued. Tell the user which blocker it waits for and its status; submit again once the blocker is wrapped, or — only if the owner says the dependency is wrong — `pf file dep rm <blocker> <id>`, then submit. |
| `CORE-4xx` (other) | aihub refused the claim for another reason, e.g. `NOT_CLAIMABLE` / `ALREADY_HELD` (exit 1; `<slug> was not submitted — aihub refused the claim; nothing stays queued: <aihub's detail>`) | Nothing was submitted or kept queued; relay aihub's reason. Another session holding it: see the recovery section below. |
| `CORE-403` `PROJECT_DEACTIVATED` | aihub refused the claim: the work item's project is deactivated (read-only on the server) | Nothing was submitted or kept queued; the refusal names the project and the restore command (`pf projects restore <project>`, a maintainer or an admin). Restore first, then submit again. |
| `DAEMON-400` | Malformed request body | Fix the invocation. |
| `DAEMON-412` | The work item's project is not in the `[workspace]` mapping of this machine (refused before any capsule or claim state exists) | Add the project to a `[workspace."<path>"]` section; `pf doctor` checks the mapping. |
| `DAEMON-516` | (surfaces as a `submit_rejected` event) re-claim clone refusal: the capsule clone failed for a work item with published progress, so a local init that would restart at step 1 is refused | Fix aihub reachability / credentials, then `pf resume <id>`. |
| `dial unix …` | The daemon is not running on this machine | Run `pf daemon ensure`, then retry once (or check `--socket` / `PF_DAEMON_SOCKET`). |

## Recovering a work item held by a dead or stuck daemon

An admin or project maintainer ends a dead or stuck holder's stale session (`pf ops end-session <id> --reason <text>`, see `verbs/ops.md`):
the work item goes back to `queued` and the old holder is fenced. Then
`pf submit <id>` on any daemon is the ordinary claim, which restores progress
from the capsule revision chain and continues from the recorded current step.
A `CORE-409 NOT_CLAIMABLE`/`ALREADY_HELD` answer on submit means the session
has not been ended yet.

## Watching what you submitted

There is nothing to arm or subscribe: the work item's decisions land in the
daemon's decision feed, which the human handles in Pi Web's Polyforge panel
(`pf watch decisions` on the CLI). Progress is one `pf watch <id> --once`.
Until the daemon registers the work item with a monitor it shows as
`queued/preparing` — not an error. Full rules: the human-decisions section
of `SKILL.md`.
