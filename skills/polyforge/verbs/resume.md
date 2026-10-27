# resume — continue a paused work item, or a blocked one

Execution-plane verb with two doors; the hub status of the work item picks
the door. This is the human "continue" (the "abandon" of a blocked or paused
work item is `cancel`). The
blocked door is open to the task's reporter, a project maintainer or an
admin.

## Door 1 — a paused work item (hub status `paused`)

The operator-pause move: a `clean_pause=false` PATCH on the hub, then the
CLI tells the local daemon (the `resume-paused` route). When this machine's
daemon is the work item's holder it re-validates the session (hub: the
session still holds the work item, status `paused`, `clean_pause` cleared),
re-attaches it and continues the current step IN PLACE — the same
semantics attaching after a daemon restart has, so a pending unanswered
question is re-posed and the step waits for the human answer before any
dispatch. A work item whose holder is a different machine (the local
daemon answers `DAEMON-404`) or whose holder is gone moves with `pf ops
end-session <id> --reason ...` then `pf submit <id>`; a holder that was
down attaches on its next start (the restart reconciliation — the same
attach, one daemon start later). A typed mid-flight failure (missing
capsule, unmapped project) answers `DAEMON-409` and the work item is
BLOCKED at aihub with the typed cause — never the restart copy.

Success: `resumed <slug>: clean_pause cleared; the holder daemon re-attached
the paused session and continues the current step in place`. `--json`: the
raw answer.

## Door 2 — a blocked work item (hub status `blocked`)

Two steps: the CLI first lands aihub's resume-blocked door with the HUMAN's
own api key (data plane, like `cancel`; the check is reporter / project
maintainer / admin against the person typing, never the daemon's machine
key), then asks the local daemon to claim and start the WI. A refused door
(`AUTH-403`) stops there and the daemon is never contacted.

The §6.4 owner move (polyforge#116): the workflow parked on an honest
`incomplete` step result — `workflow_status=blocked` in the capsule, the
current step frozen `in_progress`, the owner holding the next move. The
daemon re-establishes execution over the parked capsule: the hub's
resume-blocked door returns the WI to claimable, the daemon claims it back,
and the claim/start path's owner move unblocks the workflow — the frozen
current step re-dispatches IN PLACE as a fresh derivation (the spawn
journal and the step's prior output survive; the resumed lane continues
the parked capsule, never a pre-blocked re-derivation).

Success: `resumed <slug>: the workflow was parked blocked (incomplete
result) — the §6.4 owner move re-established execution over the parked
capsule`, then `(the frozen current step re-dispatches in place as a fresh
derivation; watch it with pf watch <id> --once)`. `--json`: the raw answer.

Sealed-head safety: a blocked work item never continues without the
capsule head the sealing daemon recorded at block time. On the sealing
machine the resume continues from that head even when its last push did
not land: the fresh capsule takes the unpushed commits from the
quarantined previous scene (`capsules/<name>.quarantined.<ts>` and its
git-dir), its task repos come back from the same scene (unpushed commits
and uncommitted work), and the next publish pushes them. When a DIFFERENT daemon picks
it up, or the sealing machine no longer holds the head, the start is
refused (`DAEMON-518`, the claim is abandoned back to blocked) unless the
aihub capsule head equals the sealed head — continue it on the sealing
machine, abandon with `pf cancel <id>`, or — when the sealed work is gone
for good — accept losing it:

```
pf resume <id> --accept-lost-work
```

The human states that the work the sealing machine committed but never
pushed is lost. Same door and same permission as a plain resume (reporter,
project maintainer or admin); in the same aihub transaction the seal is
cleared and a `blocked_seal_abandoned` event records who, when, the
abandoned capsule head and the machine. The claim that follows (on any
machine) continues from the chain aihub holds; the CLI prints the
abandoned head and machine on stderr; if aihub answers with the seal still
in place (an aihub too old for the flag) the CLI fails instead of starting
the claim. On a blocked work item that carries no seal the flag just
resumes it (no event). Only for a `blocked` work item — on
any other status the flag is refused before anything changes. Use it only
when the user explicitly accepts the loss; otherwise continue on the
sealing machine.

`pf wrap <id> --fail` is NOT a door into a parked workflow: the daemon
refuses it (`DAEMON-409`; or `DAEMON-404` plus the same hint when it cannot
see the hub status) and points back at these two moves
(continue with `pf resume`, abandon with `pf cancel`), touching nothing.

A blocked workflow must never answer the pause copy, and a plain claim
(`pf submit`) of a parked work item still refuses (`CORE-409
NOT_CLAIMABLE`) — the owner's moves go through this door, not through the
claim gate.

A work item whose capsule directory was missing at daemon restart, whose
project had no `[workspace]` mapping, or whose restart-time session verify
aihub refused (a typed answer, named in the reason note), is set to
this same `blocked` status (the reason note names the cause; a
`capsule_missing` / `project_unmapped` / `session_verify_outage` event shows in `pf watch`). Restore the mapping if that was the cause, then `pf resume <id>`:
it is a fresh re-claim from the aihub capsule chain plus the task-repo carrier
refs (`refs/pf-task/<name>`); the dead scene (if any) is quarantined, completed
steps are not re-run, and work published after the last push is lost.
An aihub that was merely unreachable at restart does NOT block the work
item: the daemon keeps it `recovering` (visible in `pf ops status`) and retries
the verify on its own; it resumes in place once aihub answers — no
`pf resume` needed.

## Triggers

- EN: resume it / continue it / continue the paused one / it parked blocked,
  continue it / bring it back / pick it up again
- 中文: 恢复执行 / 继续 / 把暂停的那个恢复 / 阻塞了继续跑 / 重新跑起来

Continue is `resume`; abandon is `cancel`.

## Command

```
pf resume <id> [--accept-lost-work]
```

Same CLI precheck as `submit`: an unknown id or a terminal work item is
refused before anything else happens.

## Failure branches

| Code | When | What to tell the user |
|---|---|---|
| `CORE-404` | Unknown or invisible id (CLI precheck) | No such work item — check the `wi_` id. |
| (no code) `submit: <slug> is terminal (<status>)` | The work item is wrapped/failed/canceled | A finished work item cannot be resumed — file a new one. |
| `DAEMON-409` | The work item is already active or queued on this daemon, the `clean_pause` flag was not yet cleared at the hub, or the paused resume was blocked by a typed mid-flight failure (missing capsule / unmapped project — the detail names the remediation) | `pf watch <id> --once` shows the state; for a blocked one, fix the named cause, then `pf resume` again. |
| `DAEMON-410` (`SHUTTING_DOWN`) | The daemon is shutting down | Wait for restart, then resume. |
| `AUTH-403` (from the resume door) | The caller is not the work item's reporter, a project maintainer or an admin | Ask the task owner or a maintainer to continue it. |
| `DAEMON-503` (paused door) | The hub was unreachable while the holder daemon re-validated or re-attached the paused session | Nothing was resumed (`clean_pause` is already cleared, so the holder also attaches on its next start); retry `pf resume` once aihub answers. |
| (200, `deferred: true`) | The hub was unreachable when the daemon went to claim after the resume door | Not an error and no error code: the claim is queued in the daemon (persisted, retried with backoff) and `pf resume` answers `resumed ...: the hub is unreachable right now; the claim is queued`. Do not run `pf resume` again — it would only find the work item already queued (`DAEMON-409`). |
| `DAEMON-412` | The project has no `[workspace]` mapping on this machine (a claim/submit cannot place its capsule) | Re-add the project to a `[workspace."<path>"]` section (`pf doctor` checks it), then `pf resume`. |
| `DAEMON-516` | The claim behind the resume could not clone the capsule for a work item with published progress; a local init would restart the workflow at step 1, so it is refused and the claim returns to blocked | Fix aihub reachability / credentials, then `pf resume` again. |
| `DAEMON-518` | The aihub capsule head is not the sealed head, and the claiming daemon is not the sealing one or (on the sealing machine) no quarantined scene holds the sealed head | Continue it on the machine that sealed it, or abandon with `pf cancel <id>`; if the sealed work is gone and the user accepts losing it, `pf resume <id> --accept-lost-work`. |
| `CORE-409 NOT_BLOCKED` | The resume door on a work item that is no longer blocked | The state moved — check `pf queue`/`pf watch <id> --once`; a paused WI unpauses with this same verb. |
| `CORE-409 SESSION_HELD` (from the resume door) | The blocked WI still carries a session (a half-settled close) | Nobody may resume it until the holder settles — check the holder daemon, or an admin/maintainer ends the session with `pf ops end-session`. |
| `CORE-404` (from the resume door, on claim) | Another machine claimed the work item between the door and the claim | It resumed elsewhere — watch it there. |
| `dial unix …` | Daemon not running | The blocked-door resume re-establishes execution, so a daemon must be involved — run `pf daemon ensure`, then retry once (or check `--socket`). The paused-door resume still cleared `clean_pause` at the hub; once the holder daemon starts, its restart reconciliation attaches the paused session (to continue without it, `pf ops end-session` + `pf submit`). |

Do not route "continue / go on" of a **running** work item here — that is
`watch`. A blocked work item has exactly two owner moves: continue it here,
or abandon it with `pf cancel <id>`. There is no rewind-to-a-step move, and
`pf wrap --fail` on a blocked work item only prints that hint.

## Watching after a resume

`pf resume` subscribes the resuming chat to the work item like `pf submit`
does; nothing to re-arm. If the chat's watcher is not running, start it (see
the `pf watch` section of `SKILL.md`).
