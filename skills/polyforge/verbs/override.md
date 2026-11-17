# override — a work item's per-step overlay: the model chain, the skip switch and the step params

Execution-plane verb: routed to the local daemon (the override routes). A
work item's **step overlay** is a capsule-scoped record (`runtime-state/
step-overrides.json`, polyforge#25): per step of this one ticket, a model
chain and a skip switch that outrank every other candidate source at
dispatch time, and param values that outrank the project's and the
lane's (polyforge#78). It changes no lane snapshot, travels with the capsule
chain, and is written only through the daemon (a step agent's process
tree is refused `DAEMON-403`). A **queued** work item takes its param
changes the same way (polyforge#79): they ride the reserved `step_params`
attrs key until its claim, so the owner can configure a ticket's params
before it is submitted or claimed — the composed final values and sources
read back with `pf params <id>`.

## Triggers

- EN: set this ticket's value of a step param / clear the step's param override / change this step's model / give the review step another model / use opus for the verification step / skip this ticket's merge step / un-skip it / clear the step's override / what model will the next step run / 给这一步换个模型 / 跳过这张票的 merge / 取消跳过 / 清掉这步的覆盖 / 下一步会用什么模型
- 中文: 改某步的模型链 / 停住的票换个模型再继续 / 看看每步的模型来源 / 这张票改某步的参数 / 清掉这步的参数覆盖 / 看看每步参数的最终值和来源 / 给排队的票先配好参数

## Command

```
pf override <id>                                        # the view: every step, its overlay, the effective chain and its source
pf override <id> --step <step-id> --models <h:m[:e],...>   # set the step's overlay chain (comma-separated, in order)
pf override <id> --step <step-id> --clear-models         # clear it (the step composes from its usual sources again)
pf override <id> --step <step-id> --skip                 # switch a lane-declared skippable step off (never dispatched, records skipped)
pf override <id> --step <step-id> --unskip               # clear the overlay's own skip switch (a project-level skip is refused — see Skip rules)
pf override <id> --step <step-id> --param <name>=<value>  # set this ticket's value of a param the lane declares on the step (see Params)
pf override <id> --step <step-id> --clear-param <name>    # clear it (the project's value or the lane default governs again)
```

Exactly one action per call: two of the action flags above are refused before
anything is sent.

`--by <user>` records the operator (default: config `user_id` /
`PF_USER_ID`); `--json` prints the daemon's raw answer. Candidates are
`<harness>:<model>[:<effort>]` with harness `pi` or `claude` — the same
shape as `pf submit --models`. Precedence: **the overlay > `pf submit --models` > workspace yaml step/tier > bundle > yaml default > bundle defaults**; the overlay is read at every dispatch, so the next dispatch of
that step takes its first candidate and a model-class failure rotates
along it. The overlay holds for the whole ticket (a rewind's re-dispatch
included) until cleared.

## Who may be written

- A **queued** work item (polyforge#79): the params only — `--param` and
  `--clear-param` write the reserved `step_params` attrs key through the
  daemon's machine-signed route, whole-key read-merge-write, judged
  against this machine's lane set before anything is written (a step or
  param the lane does not declare, or a value off the one-line bound, is
  `DAEMON-400`; a lane that does not load on this daemon is `DAEMON-409`).
  The chain and the skip switch are refused here (`DAEMON-409`): they are
  set once the ticket runs. A claim that lands while the write does is
  detected by the re-read and named in the answer — the key gone says the
  claim merged the params into its capsule, the key still there says the
  change never reached it: set the param on the running ticket again. At
  the claim the fresh capsule's own lane snapshot re-judges every entry:
  declared, legal ones merge into the capsule's overlay record (the
  per-ticket layer), anything else is dropped with a line — never a
  refused claim — and the key is cleared once that publish succeeded.
- A step **with a live dispatch** (a running ticket's current
  in_progress step): refused `DAEMON-409`.
- A step that **already ran** (done/skipped): refused for every action.
- A **paused** ticket: every step that has not finished accepts a chain —
  including the frozen current step (the change-before-wake window); the
  change stays in the kept scene, not published, until the resume here
  publishes the chain.
- A **blocked** ticket (this machine's capsule): the change lands in the
  daemon's local pending record and enters the fresh capsule of the next
  `pf resume` here, published at once — the sealed scene is never written.
- A parked change holds on **this machine only** (`published: false` in
  the answer and the event); a resume elsewhere does not see it.
- A **terminal** ticket (wrapped, canceled, failed): refused `DAEMON-409`.

## Skip rules

Only a step the lane snapshot declares `skippable` may be switched off;
a request for any other step is refused naming the alternative: **to let
a failed gate pass, waive it** — tell the step's agent (`pf answer`) and
it hands off `waive` citing your words (waivable gates only). The skip needs a step that never started; a
skipped tail's wrap attrs move to the last running step exactly as the
claim-time skip does.

`--unskip` clears the overlay's own switch only. A step skipped at the
**project level** — the workspace yaml's skip switch, fixed in the
capsule at the ticket's first claim — is refused naming that skip: the
overlay cannot restore a dispatch the project switched off (the driver
honors the union of the two sets), and the project's own switch governs
the claims made after it changes, never a ticket already claimed.

A skip that would move the lane's last agent step onto a step that has
**already started** is refused naming it: the running step was
dispatched under the step order it read, and the obligations a mid-run
skip hands the new last step (the moved wrap attrs, the
published-record registration) were settled at that dispatch. Skip
the tail before that step starts, or let it run.

## Params

A lane step may declare `params` (a name to its lane default, the
workflow contract's Step keys). The value a dispatch takes is composed
at every dispatch, highest first: **the overlay > `steps.<project>.<step-id>.params.<name>` > the lane default** — the
overlay's `--param` value, else the project's value in the workspace
`.polyforge.yaml` (only where the lane declares that param on the step),
else the lane's own default. The final value fills the step definition's
`{{<name>}}` and reaches the step's scripts as `PF_PARAM_<NAME>` (the
name upper-cased); a change reaches the step's next dispatch, and a
dispatch that continues the step's earlier conversation tells the agent
which values changed.

- Only a param the capsule's lane snapshot declares on the step is
  accepted (`DAEMON-400` otherwise); the value is one line of at most
  256 bytes, empty allowed (`DAEMON-400` otherwise).
- The writability above holds: a live dispatch or a step that already
  ran refuses it (`DAEMON-409`); a parked ticket's change holds on this
  machine only, like a chain.
- The view lists the param overrides only, never the final value: each
  `param <name>="<value>" (by <user>)`. The composed final value and the
  layer each param comes from — the per-ticket layer, the project's
  `steps.<project>.<step-id>.params.<name>`, or the lane default — read
  back with `pf params <id>` (a queued work item included: its per-ticket
  layer is the `step_params` attrs key, labelled `step override` like the
  overlay's). The project's own value is written with `pf config set
  steps.<project>.<step-id>.params.<name> <value>` (docs/reference/config.md).

## Success

`step <id>: overlay models=… (posture running, by <user>)` (a param:
`overlay param <name>="<value>" …`, every param override of the step;
a clear: `overlay param <name> cleared`) plus
`published: the capsule chain carries it` (or the posture's note for a
parked ticket). Every change is one `pf watch` event (`override_set` /
`override_clear`: the step, the aspect — `param` with the param's name
for a param —, the new value, the operator).

## Failure branches

| Code | When | What to tell the user |
|---|---|---|
| `DAEMON-400` | a candidate without a harness prefix, an unknown harness, an effort its harness refuses | Quote the daemon's text; the shape is `<pi\|claude>:<model>[:<effort>]`. |
| `DAEMON-400` | a param the lane snapshot does not declare on the step, or a value that is not one line of at most 256 bytes | Name the params the step declares (`pf workflows --cwd <dir>`); a value goes on one line. |
| `DAEMON-400` | a pi model the Cell's step-agent `models.json` does not declare | Add the model there first (the operator agent dir's `models.json`), then retry; claude candidates are not file-checked. |
| `DAEMON-409` | the step holds a live dispatch, already ran, is not declared skippable, or is skipped at the project level (`--unskip` refused naming it) | Set it on a step that has not started, or while the ticket is parked; for a gate that must pass, use the waive; a project-level skip is the project's switch, not this ticket's. |
| `DAEMON-403` | a step or task agent's process tree called | The overlay is the owner's move; call it from a human session. |
| `DAEMON-404` | this daemon holds neither a job nor a capsule of the ticket, and the ticket is not queued (an id aihub does not know, one running on another machine, a terminal one with no scene here) | Set the overlay on the machine that runs (or last ran) the ticket; a queued work item answers here — its params ride the `step_params` attrs key. |
| `DAEMON-409` | a queued work item's lane does not load on this daemon (its params are written against its lane), or its chain/skip write reached the queued posture | Write the params on a machine whose lane set has the bundle; the chain and the skip switch wait until the ticket runs. |

Pi Web's job detail reaches the same face through this verb's routes; the
panel offers the single-candidate form (harness + model), the skip
toggle of a skippable step and — per declared param — a set/clear row
(queued work items get theirs in the 工单详情; a step that already ran
shows its params read-only). `pf params <id>` is the composed read both
surfaces display.
