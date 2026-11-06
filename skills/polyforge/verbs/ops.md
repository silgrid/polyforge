# ops — daemon operations: status, auto-pull, max jobs, machine role, graceful shutdown; end-session recovery

Execution-plane verb: routed to the local daemon (status / ops-config /
shutdown routes). The daemon is one Host serving one or more Cells (one
per AIHub profile): `status`, `auto` and `max` address the Host aggregate
— the Host ceiling, every Cell's jobs and a block per Cell — unless an
explicit `pf --profile <name> ops …` targets one Cell (`PF_PROFILE` and
the cwd never change their scope); `shutdown` always stops the whole Host
and refuses `--profile`. Each Cell has its own machine id and heartbeat:
the aggregate's `cells[]` rows carry `machine_id` and `heartbeat`. `max`
is the Host ceiling over the SUM of every Cell's running jobs and one-off
tasks (a Cell's optional `cell_max_concurrent_jobs` lowers its own share,
tasks included; a task spawn at a taken ceiling is refused, nothing queued; Cells with
queued work start round-robin); the aggregate also reports
`background_model_calls: {bound: 2, running}` — every Cell's one-off tasks
share that one bound (summarize calls take none of it). Machine-local operations only: what this daemon is
managing, whether it also pulls work by itself, and how it stops. The
daemon has ONE switch and ONE number: `auto` (off = only explicit
`pf submit`; on = the daemon additionally pulls the aihub ready queue by
itself) and `max` (concurrent jobs). Manual `pf submit` stays legal while
auto is on. Graceful shutdown is a process-lifecycle action and is **not**
the auto switch — the shutdown sequence terminates every live agent tree,
persists its bookkeeping and exits; auto keeps claiming queued work while
the daemon lives.

## Triggers

- EN: daemon status / what's this machine running / how many jobs / turn
  auto-pull on / turn auto-pull off / let the machine pick up ready work
  itself / raise the concurrency / shut the daemon down gracefully /
  pre-upgrade check / start the daemon / the daemon is down / make this the
  interactive machine / take the interactive role over
- 中文: 升级前自检 / 启停 / 拉起 daemon / daemon 挂了 / 守护进程状态 / 本机在跑什么 / 开启自动领取 /
  关闭自动领取 / 优雅停机 / 调大并发 / 设为交互机器 / 抢交互角色
- EN (recovery): take over a stuck work item / its daemon is dead / the
  machine holding it is gone / force-end the session
- 中文 (恢复): 接管卡住的 WI / 持有它的机器挂了 / 强制结束会话

## Command

```
pf ops status                                        # local jobs + auto/max + queue counters + the model-health summary (never reads aihub)
pf ops models                                        # the model+effort x step profile over the events-retention window (read-only; --json re-emits GET /models)
pf ops auto [on|off]                                 # bare: show; on: pull the ready queue itself; off: explicit pf submit only
pf ops max [N]                                       # bare: show; N >= 1: the cap until the daemon restarts or a pf config set/reload changes [daemon] max_concurrent_jobs (last action wins)
pf ops role [interactive|runner] [--force|--if-unset] # bare: show this machine's role; interactive|runner: ask the heartbeat for it; --force: take interactive over from the machine holding it; --if-unset: only while no operator chose a role
pf ops shutdown                                      # graceful shutdown
pf ops end-session <wi> --reason <text>              # force-end a stuck holder's session (aihub-direct)
pf daemon ensure                                     # START: the daemon under its tmux restart loop (no-op when it answers)
```

Starting the daemon is `pf daemon ensure` (polyforge#174) — never a bare
`pf daemon` in the background. It is idempotent and file-locked: when a
daemon answers it does nothing; otherwise it starts the restart loop in
tmux session `polyforge-daemon` (a failed exit — crash, panic, kill — is
restarted within ~5s; a clean exit ends the loop; exit 78 — the daemon
refused to start for an operator-fixable reason: config, daemon id,
workspace, on-disk state, failed restart reconciliation — also ends the
loop, so read `<data-dir>/daemon.log`, fix the cause, then `pf daemon
ensure` again) and waits for the socket. The harness adapters already run it when a session
starts or resumes, so the agent runs it only as the fallback: an
execution-plane verb answered `dial unix …` → `pf daemon ensure`, then
retry that verb ONCE. A non-zero exit carries the reason (no `[workspace]`
mapping, missing release workflows, the daemon log tail) — relay it and
run `pf doctor`. `pf ops shutdown` stops the daemon for good: its clean
exit ends the restart loop as well; `pf daemon ensure` starts it again.

The daemon's other production knobs are not ops verbs: they live in the
`[daemon]` table of `cli.toml` (`step_timeout`, `stall_window`,
`harness_root`, `operator_agent_dir`, `max_concurrent_jobs`,
`max_concurrent_bars` — how many heavy runs run at once, default 2), which
the daemon reads itself. Change one with `pf
config set <key> <value>`; a hand edit needs `pf config reload`. The running daemon applies the live
keys (`step_timeout`, `stall_window`, `max_concurrent_jobs`,
`cell_max_concurrent_jobs`, `max_concurrent_bars`, `agent_language`) without a restart, takes the `[workspace]`
mapping at the next claim, and reports the restart-only keys (`harness_root`, `operator_agent_dir`, and the flat
`server`/`api_key`/`daemon_socket`) as pending until the next daemon
start (`pf ops shutdown`, then `pf daemon ensure`). Job cap: `pf ops max
N` changes the cap at once; a reload that **changes**
`max_concurrent_jobs` also overrides a temporary `pf ops max` (last
action wins), while an unrelated reload leaves it in force; every start
takes the table's `max_concurrent_jobs` (else 4). Bare `pf ops max` shows
the cap in force. A Cell's `cell_max_concurrent_jobs` is applied on its
reload at once, clamped to the Host ceiling (unset = the Host ceiling); a
Host-wide `pf ops max` never lifts a Cell above it.

`pf ops auto off` is more than the switch: the queue's not-yet-started
`origin=auto` entries are REMOVED at the same time (from memory and from
the persisted `submit-queue.json` a restart replays — auto off = only
explicit `pf submit`, v2-converge §5), each with a `submit_dropped` event
in `pf watch`; explicit `pf submit` entries ride untouched, and so
do entries already being claimed. A restart with auto off replays the
same rule: the previous process's auto entries are dropped, never
re-admitted.

On a lane that requires acceptance (its `workflow.yaml` key `acceptance`,
default `required`; a lane that cannot be read counts as requiring it), auto
pull claims only ready execution tickets whose content lists an acceptance
criterion (under `## Acceptance` or `## 验收` — the check `pf submit`
applies); a lane declaring `acceptance: none` is pulled without one. Others
stay queued, and `pf queue` marks them
需要交互：缺验收: settle the acceptance with the owner and write it in
(`pf file edit <id> --content @file`), or submit by hand
(`verbs/submit.md`).

To see what is ready before turning auto on (or to submit by hand), use
`pf queue --ready`.

`pf ops role` is the machine-role request. Each Cell's machine registers
with its aihub as `interactive` (a human works here) or `runner`; exactly
one interactive machine exists per user, and a second request is refused
(`interactive_refused`) while another machine holds the role. Bare `pf ops
role` shows the current role; `pf ops role interactive|runner` asks the
heartbeat for that role; `--force` takes the interactive role over from the
machine that holds it (supersession, without interrupting its execution).
It addresses one Cell, resolved like every Cell-scoped command:
`--profile`, else `PF_PROFILE`, else the cwd's workspace, else the default
Cell. The harness adapters already run `pf ops role interactive --if-unset`
(never `--force`) when a session starts: `--if-unset` applies only while no
operator ever chose a role on this machine (the choice is kept in the
data dir across restarts), so an explicit `pf ops role
runner` is not undone by the next start, compact or clear. Run `--force`
only on the human's explicit request — it moves the role away from their
other machine. A step or task process tree
is refused (`DAEMON-403`).

`pf ops models` is advisory telemetry, never a control: it aggregates the daemon's own `events/` stream into one row per model+effort × step (dispatches, successes, model deaths, rate, mean wall time, non-model deaths and the most recent death cause), states the rolling events-retention window above the table, and prints the current threshold warnings. It is read-only through a `GET /models` route at the same local-peer reachability as `GET /status`; `--json` re-emits that route's document unchanged. It never reorders, pins, skips or blocks a dispatch, and it has no config knob (the thresholds are compiled constants). The same summary appears in `pf ops status` (current warnings, or an explicit no-warnings line, with the pointer back to `pf ops models`).

`end-session` is the one `ops` action that does not touch the local daemon:
it calls aihub with the CALLER's own key (admin or project maintainer,
checked server-side), ends the running/paused session without the holder's
secret, and returns the work item to `queued`; the old holder is fenced (its
secret no longer matches). Then any daemon claims it with a plain
`pf submit <wi>` — progress is restored from the aihub capsule revision chain
and the driver continues from the recorded current step. Failures: `AUTH-403`
(not admin/maintainer), `CORE-409` (`NOT_HELD`: not running/paused, nothing to
end), `CORE-410` (terminal).

## Success

- `models`: `window: <duration> (from <start> to <end>)` followed by the model+effort × step table (MODEL — the key, an empty effort renders as the model alone —, STEP,
  DISPATCHES, SUCCESSES, MODEL-DEATHS, RATE, MEAN-WALL, NON-MODEL, LAST-CAUSE) and any current warnings. `--json`
  prints the GET /models document unchanged.
- `status`: `daemon: <id>  auto: on|off  max: N` (plus `(shutting down)` when set),
  the queue counters (`pending / running`), one line naming the
  mapped workspace SET (`workspaces: <path>, <path>` — sorted; every
  `[workspace]` section the daemon routes capsules into, polyforge#113;
  per-WI capsules live at `<workspace>/capsules/<project>-<seq>`), and a
  jobs table (WORK
  ITEM — the slug, e.g. `polyforge#140`, or the id for a job claimed before
  slugs were recorded —, STATUS, SESSION, SPAWN, AGENT PID, QUESTION, STEP-AGE, LAST-WORK;
  plus ORIGIN when some job has one — the ORIGIN cell is `chat` for an
  interactive submit, `auto` for an auto-claim, and `-` when the job has
  no origin; plus RETRY while some job waits a retry backoff before its
  step's next dispatch — `retry in 3m: upstream_transient`, the death cause
  that owes it; the driver paces consecutive model_error /
  upstream_transient / ended_without_result deaths on one candidate 1m, 3m,
  then 9m before re-running it, a switch goes at once). This is
  bookkeeping about what this machine manages — not workflow execution
  progress (`pf watch` is the progress verb) — with two deliberately
  progress-shaped exceptions: STEP-AGE is mm:ss since the current dispatch
  began, and LAST-WORK is mm:ss since its harness last did real work (`-`
  when there is none). UI noise never refreshes LAST-WORK, so a job that is
  chattering but wedged reads as stale, not fresh. The daemon's own
  supervisor owns the stuck verdict (its silence/tool/step bounds; a
  workflow step can only stretch the step bound via `budget.wall_minutes`)
  and acts on it; a long LAST-WORK here is an observation to read in that
  light, not a rule to apply.
  A work item whose capsule directory is missing at restart
  (`capsule_missing`) or whose project is no longer mapped
  (`project_unmapped`) is not listed here: the daemon sets it `blocked` at
  the hub (reason note names the cause; the event shows in `pf watch`).
  The owner's moves: fix the cause if any and `pf resume <id>`, or give up
  with `pf cancel <id>` (`pf wrap` / `--fail` are refused on a blocked WI).
- `auto` / `max` bare: `auto: on|off` / `max: N`; a change answers with
  the effective `{"auto":..,"max":..}`.
- `shutdown`: `graceful shutdown requested; the daemon quiesces and exits`
  — the daemon terminates the live agent trees, saves each job's
  uncommitted task-repo work to aihub (`refs/pf-wip/<name>`) and exits
  within 30s. Interrupted work items stay running server-side; the next
  daemon start attaches and continues them.

## Failure branches

| Code | When | What to tell the user |
|---|---|---|
| `DAEMON-410` (`SHUTTING_DOWN`) | Any external submit while the daemon is shutting down | Wait for the shutdown to complete and the daemon to restart. |
| `DAEMON-400` | Malformed ops-config request (`max < 1`, empty body) — the CLI's own argument validation usually catches this first with a usage error | Fix the invocation; read the detail. |
| `dial unix …` | The daemon is not running on this machine | Run `pf daemon ensure` (starts it under its tmux restart loop; a no-op when one answers), then retry the verb once; if ensure fails, relay its message and run `pf doctor`. Check `--socket` / `PF_DAEMON_SOCKET` if you expect it elsewhere. |

Auto vs shutdown: with auto on the machine also claims and runs
ready-queue work by itself (an internal loop; idle-polls when the queue is
empty, never exits); shutdown stops accepting anything new, closes what
remains and exits. They are independent concepts and never substitute for
each other.
