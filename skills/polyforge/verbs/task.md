# task — one-shot task agents (`pf task`)

Execution-plane verb: the whole `pf task` face lives on the local daemon's
unix-socket face. A task is ONE fresh harness agent started from a free-text
prompt — no session reuse, no resume, no retry, no queue — running under a
hard wall-clock and token budget in a mapped project's workspace. It is the
read-only-errand face: hand it research, analysis, a report or a pre-review,
never a code change.

## Triggers

- EN: spawn a task / outsource a research errand / have a fresh agent look
  into X / start a background investigation / what tasks are running / read
  that task's report / cancel that task
- 中文: 起个任务 / 外包给一个新 agent / 调研一下 X / 派个只读跑腿 / 让新 agent
  先看看 / 现在有哪些任务在跑 / 看那个任务的报告 / 取消那个任务

## Command

```
pf task spawn --project <p> [--max-duration D] [--max-tokens N] "<prompt>"
pf task list [--json]
pf task show <id> [--json]
pf task cancel <id>
```

- `spawn` prints the task id (`task_` + 16 lowercase hex) immediately and
  returns; the task runs in the background. There is no `--wait`: poll
  `pf task show <id>`.
- `--project` is required and is the only selector: a project **name** mapped
  on this daemon, never a filesystem path.
- Budgets: `--max-duration` defaults to `20m` (ceiling `60m`) and
  `--max-tokens` to `300000` (ceiling `1000000`). The token budget counts
  **fresh tokens** per model call: uncached input + cache creation/write +
  output. Context re-sent from the prompt cache (a cache read) is not charged
  again. Both are hard caps — a value above its ceiling, or one that is not a
  duration / not a positive integer (`0` and negatives included), is refused
  locally, exit 1, before any wire call, and no config key raises the
  ceilings.
- `--models`, `--wait` and `--type` are refused, never swallowed: the lane has
  no spawn-level model override, no blocking mode and one task kind.
- Model candidates come from the workspace yaml: `steps.<project>.task.models`
  when declared, else `steps.<project>.default.models`; with neither the spawn
  is refused naming those keys.

## Drafting (起草): prompt, budget, approval

A task receives a prompt, not a conversation: whatever the draft leaves out it
cannot ask about. Before spawning, draft these and show them to the owner —
spawn only after the owner approves:

- **目标** — the question to answer or the errand to run, in one sentence.
- **交付物** — what "done" looks like and the shape of the answer (a report, a
  comparison, a verdict, a pre-review).
- **范围** — which sources, trees or topics are in scope, and explicitly what
  is out of scope.
- **预算** — a wall-clock/token proposal with its reason: start from the
  defaults `20m` / `300000` and raise it only when the errand needs it, never
  above the ceilings.

Name the model-config requirement in the same breath, so a missing
`steps.<project>.task.models` (and missing project default) is fixed before
the spawn rather than after it.

## Boundary: a read-only errand, never a code change

Research, analysis, reports and pre-review only. A code change, branch,
commit or PR goes through a ticket (`pf file create --bundle <lane>` +
`pf submit`, `verbs/execute-intent.md`); a task owns no work item and produces
no deliverable.

- The task process carries `PF_TASK_ID` (with `PF_DAEMON_ID`): the daemon
  refuses it every mutating unix-socket request with `DAEMON-403`, and `pf`
  refuses every write verb locally before any config read or network call. Its
  reads stay open (`pf recall`, `pf memory show`, `pf watch <wi> --once`).
- There is no supported bypass. Do not teach or attempt one — unsetting the
  markers, `setsid`/`env -i`, or a raw call to the hub are not a path. Treat
  `DAEMON-403` as a stop and route the code work into a ticket instead.
- Trust equals a session agent's: there is no OS-level sandbox, so the task
  may read and write what the human's session may on the same filesystem and
  network. Scope its prompt accordingly.

## Lifecycle and retention

- `list` shows the retained tasks (id, project, state, age); its `--json`
  rows also carry `prompt_summary` (the prompt's first non-empty line, at
  most 120 code points; empty for an empty prompt), `reporter` and
  `report_tail`, and never the full prompt or the full report — those stay
  on `pf task show`.
  - `reporter` is the spawner's display name as a human member of the
    task's project, else `machine`. Its trust is self-asserted: the id comes
    from `PF_USER_ID` (else `cli.toml` `user_id`), the same trust as a work
    item's `origin.owner`, not an authenticated identity. It is a spawn-time
    snapshot: a later rename or an aihub outage does not change it.
  - `report_tail` is the report's last non-empty line (at most 200 code
    points, `…` + the last 199 when longer), unmasked, `""` while running or
    for an empty report — never the error. Read the full report with
    `pf task show <id>`.
- `show <id>` prints the budget usage and the report (its
  `--json` also carries the full prompt); `cancel <id>` asks the daemon to
  stop a running task.
- States: `running`, then exactly one of `completed`, `capped` (a budget was
  hit — the partial report is kept and marked truncated), `failed` (every
  candidate errored) or `interrupted` (`cancel` or a daemon restart).
- At most 2 tasks run per daemon; a third spawn is refused immediately —
  nothing is queued.
- Reports stay on this machine: the store keeps the newest 50 entries and the
  last 7 days, oldest removed first. Nothing is uploaded to AIHub or memory.
- A daemon restart marks a running task `interrupted` and never resumes it:
  re-spawn a new task if the errand is still wanted.

## Presentation (回投呈现)

A task spawned from a chat delivers one terminal summary line through the
resident watcher:

```
task <task_…> <state>: <trailing report text> — see `pf task show <task_…>`
```

- It is NOT a `[polyforge]` decision line: it **does not trigger an agent turn**.
  Present it to the owner like a message line and let the owner decide;
  never answer, retry or re-spawn on your own.
- The line is a tail (at most 2000 characters, newlines flattened); the full
  report is `pf task show <id>`.

## Failure branches

| Code / refusal | When | What to tell the owner |
|---|---|---|
| `pf: task spawn: --project <name> is required` (local, exit 1) | spawn without a project | Amend with the mapped project **name** of the workspace the errand belongs to. |
| `pf: task spawn: "…" is a filesystem path` (local, exit 1) | the selector was a path | A path is never a selector — use the mapped project name (`pf doctor` checks the mapping). |
| `DAEMON-412` | the project is not mapped on this daemon | Pick a project this machine maps, or fix the mapping; a task runs in the project's workspace. |
| `DAEMON-400` naming `steps.<project>.task.models` | neither the task list nor the project default declares a candidate | Declare `steps.<project>.task.models` (or fall back to `steps.<project>.default.models`) in the workspace yaml, then retry once. |
| `DAEMON-400` `task spawn refused: candidate … cannot report token usage` | at least one candidate in the resolved list (`steps.<project>.task.models`, else `steps.<project>.default.models`) is on a harness that cannot report token usage — the detail names it; spawn stops at the first such candidate, even when the others report | Remove that named candidate from the list, or replace it with one whose harness reports usage: a task is hard-capped on tokens, so every listed candidate must be measurable — adding a reporting candidate alone does not help. |
| `DAEMON-414` | the project's workspace yaml fails the strict load (a bad key, or a candidate without a `<harness>:` prefix) | Fix the file the detail names, then retry once — nothing was spawned. |
| `pf: task spawn: --max-duration … is above the 60m ceiling` / `--max-tokens … is above the 1000000 ceiling` (local, exit 1) | a budget above its ceiling | Lower the budget: the ceilings are code constants, not config. |
| `pf: task spawn: --max-duration … is not a duration` / `--max-tokens … is not an integer` / `--max-duration … must be positive (got …)` / `--max-tokens … must be positive (got …)` (local, exit 1) | a malformed budget value, or a zero/negative one | Give a Go duration (`20m`, `1h30m`) or a positive integer; nothing was sent. |
| `DAEMON-409` `task spawn refused: 2 tasks already running` | third concurrent spawn | Wait for a running task to reach a terminal state (nothing is queued) or `pf task cancel <id>` one. |
| `pf: task spawn: --models is not supported (…)` (local, exit 1) | asked for a spawn-level model override | Drop the flag: candidates come from `steps.<project>.task.models`, else `steps.<project>.default.models`. |
| `pf: task spawn: --wait is not supported (…)` (local, exit 1) | asked for a blocking wait | Drop the flag: `spawn` returns the id immediately; poll `pf task show <id>`. |
| `pf: task spawn: --type is not supported (…)` (local, exit 1) | asked for a task type | Drop the flag: there is one task kind. |
| `DAEMON-409` `task is not running` / `DAEMON-404` `no such task` | cancel of an already-terminal or unknown task | Read `pf task show <id>` — it already ended (or check the id). |
| `DAEMON-403` | any write verb from inside a task or a step (spawn, cancel, and inside a task every verb but its reads) | The caller is a task/step process: the human session runs the verb; code work routes into a ticket. |
| `dial unix …` | the daemon is not running | Nothing was spawned — run `pf daemon ensure`, then retry once; if ensure fails, relay its message and suggest `pf doctor` (or check the socket path). |

A local `pf: task …` line (exit 1, no typed code) means a local refusal before
any wire call — a missing project or prompt, a filesystem-path selector, an
over-ceiling or malformed budget, or one of the refused flags.

## Notes

- This fragment is for the interactive session, not a step or task agent:
  `spawn` and `cancel` are daemon writes (refused there), and inside a task
  even `list` / `show` are refused — a task reads memories and watch snapshots
  only.
- Full detail of the surface (JSON fields, retention layout, guard wording)
  lives in `docs/reference/commands.md`; this fragment owns the
  session-operational discipline and the routing decision.
