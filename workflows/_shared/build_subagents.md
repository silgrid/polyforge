You orchestrate; fresh-context sub-agents do the heavy reading and writing, so your own context
stays on the plan, the stops and the result. A sub-agent's report is evidence, not proof: check
what it claims before you build on it or report it.

- **The analysis sub-agent** (every build that has no settled plan yet, and again after the owner's
  answer): one sub-agent reproduces and locates a reported defect and writes `artifacts/plan.md` —
  not you. It returns the plan's path; for a defect, the reproduction (command and output) or
  everything it tried; and every surprise as one question with its options and a recommended
  answer — a dead end, a ticket that contradicts the code or itself, a plan that diverges from the
  ticket — having planned nothing past the point that depends on it. It does not grill the ticket:
  no clarity checklist, no batched round of questions — the ticket was settled before it was filed.
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
  implementer's uncommitted work). A task's write set is its units' `(write)` paths: it writes
  nothing else and commits only them (`git add <path>`, never `-A`). Each task returns, per unit,
  `test_commit`, `impl_commit`, the run that went red then green (`repo`, `cwd`, `argv`) with both
  exit codes, its marker and the files it changed: check both commits are in the repo's history and
  the diff stays inside the write set before the next unit builds on them; a unit that fails the
  check goes back once, then you build it. You write `artifacts/build/units.json` from what the
  tasks return (a task never writes it).
- **`implemented_by`.** A unit is `subagent` when an implementer sub-agent made both its commits,
  else `main`; your hand-off's 做了什么 says which each unit is, and why a big plan's unit is
  `main`.
- **Dispatch.** The `Agent` tool (Claude Code), `subagent_type: "general-purpose"`, in the
  foreground (never `run_in_background`); several `Agent` calls in one reply run in parallel. The
  call ends when the sub-agent does; supervision judges it on its sub-agent's activity, so a long one
  still working is no hung tool. When this dispatch's facts say it dispatches no sub-agent, your
  tools offer no `Agent` tool, or a dispatch fails to start, do that work yourself, record `main`
  and say why in your hand-off.
- **A task is the sub-agent's whole brief.** It sees nothing of this session: name the capsule root
  (absolute) and that every capsule-relative path resolves against it, the work item
  (`runtime-state/work-item.json`), its instruction — the sections of this step's definition it
  follows, read from its rendered copy `workflows/_rendered/<step>.md` — what it may write and what it returns.
  It never runs `pf step` (no question to the owner, no hand-off, no hub call), never writes
  `runtime-state/` or `artifacts/handoff.md`, starts nothing in the background and dispatches no
  sub-agent of its own; an open question comes back in its answer.
