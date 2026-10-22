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
pf submit <id> [--priority low|normal|high|urgent] [--models <harness>:<model>[:<effort>],...]
```

`--priority` defaults to `normal`. `--models` is a manual model-candidate
list for this work item: it is the candidate chain of every step (advancing
on model-class failures like any list), kept by this daemon only (across its
restarts and `pf resume`); a re-claim on another machine uses that machine's
settings. Non-empty, comma-separated, no empty
or duplicate candidates, else a local bad-input refusal. Every candidate is
`<harness>:<model>[:<effort>]` (e.g. `--models
claude:claude-opus-5-5:high,pi:sub2api-glm/glm-5.3`); a candidate without a
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

## Success

`submitted <slug> (accepted by the local daemon)` — acceptance means the daemon
queued the job; its dispatcher derives the Go driver for it within a
moment, priority-desc against the machine's concurrency cap.

The acceptance line is followed by the placement line — `placement:
claimed by machine <machine_id>, capsule <capsule_path>` — the claim's
target machine and the workspace / capsule landing path the work item
will occupy. When the mapped workspace lacks a git clone under
`<workspace>/.repo/<name>` for a repo the claim will clone, a warning
line names each missing repo (`warning: no local clone under .repo/ for
repo(s) <names> — the claim clones fresh`): the submit is still
accepted — the claim clones fresh, no override flag exists. A placement
the daemon could not resolve prints nothing beyond the acceptance line.

`--json`: the raw answer `{accepted, work_item_id, machine_id,
capsule_path, missing_repos}` — `machine_id` + `capsule_path` are absent
when the placement is unknown, `missing_repos` when every clone is
present.

Relay the placement line and any missing-clone warning to the owner,
word for word. A warning the precheck above did not predict: tell the owner
`pf pause <id>` / `pf cancel <id>` can stop the work item before it
invests work.

## Failure branches

| Code | When | What to tell the user |
|---|---|---|
| `CORE-404` | Unknown or invisible id (CLI precheck) | No such work item — check the `wi_` id. |
| (no code) `submit: <slug> is terminal (<status>)` | The work item is already wrapped/failed/canceled (CLI precheck) | It is finished and can never be claimed again — file a new work item instead. |
| `DAEMON-409` | The work item is already active or queued on this daemon | Wait for the running attempt. |
| `DAEMON-410` (`SHUTTING_DOWN`) | The daemon is shutting down | Wait for the shutdown to finish and the daemon to restart, then submit again. |
| (accepted, then deferred) | The hub could not be reached when the dispatcher went to claim (transient). The submit itself already answered `accepted`; the deferral shows only in the daemon log (and, if the claim is later refused, as a `submit_rejected` event) | Nothing was claimed yet; the entry stays queued in the daemon and is retried with backoff (1s doubling to 30s). Check aihub reachability; `pf watch <id> --once` shows it once claimed. |
| `DAEMON-400` | Malformed request body | Fix the invocation. |
| `DAEMON-412` | The work item's project is not in the `[workspace]` mapping of this machine (refused before any capsule or claim state exists) | Add the project to a `[workspace."<path>"]` section; `pf doctor` checks the mapping. |
| `DAEMON-516` | (surfaces as a `submit_rejected` event) re-claim clone refusal: the capsule clone failed for a work item with published progress, so a local init that would restart at step 1 is refused | Fix aihub reachability / credentials, then `pf resume <id>`. |
| `dial unix …` | The daemon is not running on this machine | Run `pf daemon ensure`, then retry once (or check `--socket` / `PF_DAEMON_SOCKET`). |

The claim itself happens asynchronously in the daemon's dispatcher: a
server-side refusal (e.g. `CORE-409`, another session holds the work
item) surfaces in the daemon log and the job never appears in
`pf ops status` — `pf watch`/`pf ops status` is the follow-up, not the submit answer.

## Recovering a work item held by a dead or stuck daemon

An admin or project maintainer ends a dead or stuck holder's stale session (`pf ops end-session <id> --reason <text>`, see `verbs/ops.md`):
the work item goes back to `queued` and the old holder is fenced. Then
`pf submit <id>` on any daemon is the ordinary claim, which restores progress
from the capsule revision chain and continues from the recorded current step.
A `CORE-409 NOT_CLAIMABLE`/`ALREADY_HELD` answer on submit means the session
has not been ended yet.

## Watching what you submitted

`pf submit` subscribes the submitting chat (`PF_CHAT_ID`) to the work item in
the daemon — there is nothing to arm. Make sure the chat's resident watcher is
running (Claude Code: the `pf watch --chat "$PF_CHAT_ID" --wait-decision`
background task; pi: the extension does it) so a decision wakes you. Until the
daemon registers the work item with a monitor it shows as `queued/preparing`
— not an error. Full procedure: the `pf watch` section of `SKILL.md`.
