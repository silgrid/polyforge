You orchestrate; fresh-context sub-agents do the heavy reading and writing, so your context
stays on the plan, the stops and the result. A sub-agent's report is evidence, not proof: check
each claim against the file or commit it names before you build on it or report it.

- **The analysis sub-agent** (every build that has no settled plan yet, and again after the owner's
  answer): one sub-agent reproduces and locates a reported defect and writes `artifacts/plan.md` —
  not you. It returns the plan's path; for a defect, the reproduction (command and output) or
  everything it tried; and every surprise as one question with its options and a recommended
  answer — a dead end, a ticket that contradicts the code or itself, a plan that diverges from the
  ticket — having planned nothing past the point that depends on it. It works from the ticket
  as settled before it was filed: the clarity checklist and the batched question round belong
  to the session that filed it.
  A work item without an acceptance section: the sub-agent proposes the list in the plan's
  `## Acceptance` and returns it, and plans the units against it. You ask the owner — a sub-agent
  cannot stop the step — only in the cases 3a names, right after the plan; folding the answer into
  the plan is the sub-agent's, when you dispatch it again with the owner's words. You read the plan
  against its instructions before any unit; a plan that misses them goes back to it with what is
  wrong.
- **Implementer sub-agents — the big plan.** Count the distinct paths the plan's `## Units` declare
  `(write)`, each path once across all units. More than 7: every unit is built by a fresh-context
  implementer sub-agent, test first, and you only orchestrate; 7 or fewer: you build every unit
  yourself. A task carries consecutive units of the plan, and the units are built in plan order: a
  task starts only once every earlier unit has finished — except that tasks whose write sets are
  disjoint, each working in a different repo clone and none needing another task's code, may run at
  the same time (a red or green run counts only on a clean tree, and one clone holds one
  implementer's uncommitted work). A task's write set is its units' `(write)` paths: it commits
  them by name (`git add <path>`); the run is that unit's test alone (the test runner's
  own name filter; a suite without one is named in the task's answer and runs once, in the
  scoped command), the red is taken once, at the test commit — the facts engine's replay is the
  record — a flaky failure is re-run only in the suite that failed, once, by the written
  fast-check command itself — the same rules verification runs: the check already contains that
  one re-run, and a failure still red after it is red; a red-state run must exit non-zero within
  its limit — a unit self-reports `timeout` in units.json, else the default — and a run its limit
  kills is an evidence problem: the waiting inside it is bounded and an assertion failure exits
  at once; and a long command writes its output to a log file with its exit code
  (`> log 2>&1; echo exit=$?`) and the log is searched after the run; piping it straight into
  grep, head or tail loses the failure detail and the exit code. Each task returns, per unit,
  `test_commit`, `impl_commit`, the run that went red then green (`repo`, `cwd`, `argv`) with both
  exit codes, its marker and the files it changed (a unit declared `pre_existing_red` returns no
  `test_commit` — its reproduction test predates the change and its red evidence is the engine's
  base replay): check both commits are in the repo's history and
  the diff stays inside the write set before the next unit builds on them; a unit failing the
  check goes back once, then you build it. You write `artifacts/build/units.json` from what the tasks return.
- **Pi 派发。** 当 `pi_subagents` 为 `on` 时，使用 pi 的 `subagent` 工具、`agent: "worker"`，一次只发一个任务，完成后再发下一个（`parallel` 与 `chain` 由扩展拒收）。daemon 内置扩展会串行处理并用完整隔离参数启动 worker。每个任务可选 `timeoutSeconds`（正整数秒；0、负数、非整数拒绝且不启动；缺省默认 1800），从该 worker 实际启动时计时（排队不计）；超时后 worker 及其后代按 TERM→KILL 结束，超时是失败：正文与 details 标明超时（`timedOut`、实际超时值），清理不净时报告剩余 PID，都不是部分成果。中止与超时的清理以每调用唯一的 `PF_SUBAGENT_ID` 继承标记锁定本调用的后代（连同 `PF_WI_ID`/`PF_DAEMON_ID` 三重匹配），不触碰兄弟调用与本步骤进程。当它为 `off`（默认）时保持主 agent 路径，并记录 `implemented_by: main`。
- **`implemented_by`.** A unit is `subagent` when an implementer sub-agent made both its commits,
  else `main`; your hand-off's 做了什么 says which each unit is, and why a big plan's unit is
  `main`.
- **Dispatch.** The `Agent` tool (Claude Code), `subagent_type: "general-purpose"`, in the
  foreground (`run_in_background` stays off); several `Agent` calls in one reply run in parallel. The
  call ends when the sub-agent does; supervision judges it on its sub-agent's activity, so a long one
  still working is no hung tool. When this dispatch's facts say it dispatches no sub-agent, your
  tools offer no `Agent` tool, or a dispatch fails to start, do that work yourself, record `main`
  and say why in your hand-off.
- **A task is the sub-agent's whole brief.** It sees nothing of this session: name the capsule root
  (absolute) and that every capsule-relative path resolves against it, the work item
  (`runtime-state/work-item.json`), its instruction — the sections of this step's definition it
  follows, read from its rendered copy `workflows/_rendered/<step>.md` — what it may write and what it returns.
  Its brief also carries the output constraints (SKILL.md session rule 9's six duties — a
  sub-agent cannot be relied on to load the rules itself): the sub-agent saves its raw
  material complete into the step's `downloads/` directory (the brief names the absolute
  path), returns only a summary and an evidence index — paths, line ranges or JSON
  pointers, with hashes — that you can read on from, keeps each of its tool returns within
  rule 9's bound, and reads saved material from the file instead of loading it again.
  Its whole surface is the brief: `pf step` (owner questions, hand-off, hub calls),
  `runtime-state/` and `artifacts/handoff.md` stay yours, its commands run in the foreground,
  and it dispatches no sub-agent of its own; an open question comes back in its answer.
