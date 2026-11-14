# override — a running ticket's per-step overlay: the model chain and the skip switch

Execution-plane verb: routed to the local daemon (the override routes). A
work item's **step overlay** is a capsule-scoped record (`runtime-state/
step-overrides.json`, polyforge#25): per step of this one ticket, a model
chain and a skip switch that outrank every other candidate source at
dispatch time. It changes no lane snapshot, travels with the capsule
chain, and is written only through the daemon (a step agent's process
tree is refused `DAEMON-403`).

## Triggers

- EN: change this step's model / give the review step another model / use opus for the verification step / skip this ticket's merge step / un-skip it / clear the step's override / what model will the next step run / 给这一步换个模型 / 跳过这张票的 merge / 取消跳过 / 清掉这步的覆盖 / 下一步会用什么模型
- 中文: 改某步的模型链 / 停住的票换个模型再继续 / 看看每步的模型来源

## Command

```
pf override <id>                                        # the view: every step, its overlay, the effective chain and its source
pf override <id> --step <step-id> --models <h:m[:e],...>   # set the step's overlay chain (comma-separated, in order)
pf override <id> --step <step-id> --clear-models         # clear it (the step composes from its usual sources again)
pf override <id> --step <step-id> --skip                 # switch a lane-declared skippable step off (never dispatched, records skipped)
pf override <id> --step <step-id> --unskip               # clear the skip switch
```

`--by <user>` records the operator (default: config `user_id` /
`PF_USER_ID`); `--json` prints the daemon's raw answer. Candidates are
`<harness>:<model>[:<effort>]` with harness `pi` or `claude` — the same
shape as `pf submit --models`. Precedence: **the overlay > `pf submit --models` > workspace yaml step/tier > bundle > yaml default > bundle defaults**; the overlay is read at every dispatch, so the next dispatch of
that step takes its first candidate and a model-class failure rotates
along it. The overlay holds for the whole ticket (a rewind's re-dispatch
included) until cleared.

## Who may be written

- A step **with a live dispatch** (a running ticket's current
  in_progress step): refused `DAEMON-409`.
- A step that **already ran** (done/skipped): refused for every action.
- A **paused** ticket: every step that has not finished accepts a chain —
  including the frozen current step (the change-before-wake window); the
  chain commit stays local until the resume (aihub refuses pushes while
  the ticket is not running).
- A **blocked** ticket (this machine's capsule): the change lands in the
  daemon's local pending record and enters the fresh capsule of the next
  `pf resume` here — the sealed scene is never written.

## Skip rules

Only a step the lane snapshot declares `skippable` may be switched off;
a request for any other step is refused naming the alternative: **to let
a failed gate pass, waive it** — tell the step's agent (`pf answer`) and
it hands off `waive` citing your words (waivable gates only). The skip needs a step that never started; a
skipped tail's wrap attrs move to the last running step exactly as the
claim-time skip does.

## Success

`step <id>: overlay models=… (posture running, by <user>)` plus
`published: the capsule chain carries it` (or the posture's note for a
parked ticket). Every change is one `pf watch` event (`override_set` /
`override_clear`: the step, the aspect, the new value, the operator).

## Failure branches

| Code | When | What to tell the user |
|---|---|---|
| `DAEMON-400` | a candidate without a harness prefix, an unknown harness, an effort its harness refuses | Quote the daemon's text; the shape is `<pi\|claude>:<model>[:<effort>]`. |
| `DAEMON-400` | a pi model the Cell's step-agent `models.json` does not declare | Add the model there first (the operator agent dir's `models.json`), then retry; claude candidates are not file-checked. |
| `DAEMON-409` | the step holds a live dispatch, already ran, or is not declared skippable | Set it on a step that has not started, or while the ticket is parked; for a gate that must pass, use the waive. |
| `DAEMON-403` | a step or task agent's process tree called | The overlay is the owner's move; call it from a human session. |
| `DAEMON-404` | this daemon holds neither a job nor a capsule of the ticket | Set the overlay on the machine that runs (or last ran) the ticket. |

Pi Web's job detail reaches the same face through this verb's routes; the
panel offers the single-candidate form (harness + model) and the skip
toggle of a skippable step.
