# override — a work item's per-step overlay: the model chain, the skip switch and the step params

Execution-plane verb: routed to the local daemon (the override routes). A
work item's **step overlay** is a capsule-scoped record (`runtime-state/
step-overrides.json`, polyforge#25): per step of this one ticket, a model
chain, a capability-tier name and a skip switch that outrank every other
candidate source at dispatch time, and param values that outrank the
project's and the lane's (polyforge#78). It changes no lane snapshot, travels with the capsule
chain, and is written only through the daemon (a step agent's process
tree is refused `DAEMON-403`). A **queued** work item takes its param
changes the same way (polyforge#79): they ride the reserved `step_params`
attrs key until its claim, so the owner can configure a ticket's params
before it is submitted or claimed. Its capability tier (polyforge#81)
also can be set while queued: it rides this daemon's local pending
override record (the same record a parked ticket's change uses), so the
next claim of the ticket ON THIS MACHINE inherits it — a claim elsewhere
does not, and the answer says so. The composed final values and sources
read back with `pf params <id>`.

## Triggers

- EN: set this ticket's value of a step param / clear the step's param override / change this step's model / give the review step another model / use opus for the verification step / skip this ticket's merge step / un-skip it / clear the step's override / what model will the next step run / 给这一步换个模型 / 跳过这张票的 merge / 取消跳过 / 清掉这步的覆盖 / 下一步会用什么模型
- 中文: 改某步的模型链 / 停住的票换个模型再继续 / 看看每步的模型来源 / 这张票改某步的参数 / 清掉这步的参数覆盖 / 看看每步参数的最终值和来源 / 给排队的票先配好参数

## Command

```
pf override <id>                                        # the view: every step, its overlay, the effective chain and its source
pf override <id> --step <step-id> --models <h:m[:e],...>   # set the step's overlay chain (comma-separated, in order)
pf override <id> --step <step-id> --clear-models         # clear it (the step composes from its usual sources again)
pf override <id> --step <step-id> --tier <name>          # set a capability tier from the Host [step_tiers] catalog (expanded at dispatch)
pf override <id> --step <step-id> --clear-tier           # clear it (the lower layers govern again)
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
shape as `pf submit --models`. Precedence: **the overlay > `pf submit --models` > workspace yaml step/tier > bundle > yaml default > bundle defaults**; inside the overlay an explicit `--models` chain outranks `--tier`. The overlay is read at every dispatch, so the next dispatch of
that step takes its first candidate and a model-class failure rotates
along it. The overlay holds for the whole ticket (a rewind's re-dispatch
included) until cleared — it is **not consumed once** by its first
dispatch (Q3 B).

## The next dispatch on a step that ran or is running

A models/tier overlay is for the step's **next dispatch**, so it is also
accepted on a step that already ran (done) or is running right now
(polyforge#270, the owner's Q1 B/Q2 B):

- **Reachability is the judge**: the daemon computes, from the lane graph,
  the execution record and the effective skips, whether the step can still
  be dispatched again. A later step that still runs can re-dispatch it; a
  done step counts only while a reachable gate's rewind still covers it —
  a rewind re-runs `[on_fail target, gate)`, so the target and the done
  steps between it and the gate (a done build, or a done review while
  verification can rewind to build); a rewind_only step waits for a rewind
  whose span holds it; the rewind budget is
  not consulted (the owner's own authorization can open another round).
- **A step with no next dispatch refuses** (`DAEMON-409` naming the
  reason): the done prefix past its last rewind, an effectively skipped
  step, a completed lane.
- **The change does not touch the dispatch in flight**: only the capsule's
  overlay record is written; the running process keeps its candidate and
  the finished round keeps its execution record and attempts. A dispatch
  whose candidate composition already ran keeps that snapshot — the change
  governs the next composition (Q4 A).
- **The view and the answer say so**: the step row carries
  `can_override_models`, `model_override_reason` when it cannot be written
  and `model_effect_scope: "next_dispatch"` only while it can (an overlay
  left on a step with no next dispatch never claims one); the write answer says the
  change applies on the next dispatch (the finished or live one
  unchanged). The skip switch and params keep their old rules.

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
  in_progress step): the model aspects — `--models`, `--clear-models`,
  `--tier`, `--clear-tier` — are accepted for its **next dispatch**
  (polyforge#270); the dispatch in flight is not interrupted and keeps its
  candidate and round. The skip switch and a param keep the old refusal
  (`DAEMON-409`): they would change the dispatch in flight.
- A step that **already ran** (done/skipped): the model aspects are
  accepted while the lane can still dispatch the step again — a reachable
  gate's rewind span `[on_fail target, gate)` covers it (a done build with a
  running review or verification gate, for instance) — and take effect on that
  step's next dispatch; the round that ran keeps its candidate, round and
  attempts. A step the lane will not dispatch again (no rewind left, an
  effective skip, a completed lane) refuses them `DAEMON-409` naming the
  reason; skip and params are refused for every done step.
- A **paused** ticket: every step that has not finished accepts a chain —
  including the frozen current step (the change-before-wake window) — and,
  as on a running ticket, so does a done step the lane can still dispatch
  again; the
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

## Capability tier

`--tier <name>` sets the overlay's second model aspect (polyforge#81):
the **name**, never the expanded chain, is stored, and the step's next
dispatch expands it through the Host `[step_tiers]` catalog
(`docs/reference/config.md`) to that tier's whole ordered candidate
list — above `pf submit --models` and every yaml/bundle level, below an
explicit `--models` chain on the same step. A Host reload takes effect at
the next dispatch with no restart.

- The name must be in **this** Host's catalog (`DAEMON-400` naming the
  catalog's tiers when it is not; an empty catalog says so) and follow the
  lane tier grammar; `--tier default` is no exemption — it must be in the
  catalog, and it means **unset** (the yaml and lane layers govern; the
  catalog's `default` candidates are never expanded). A clear needs no
  catalog row.
- An explicit `--models` chain on the same step masks the tier; the view
  shows the tier as set and the chain as the effective source.
  `--clear-models` lets the tier govern; `--clear-tier` clears only the
  tier.
- A tier the catalog does not declare (or declares empty) does not block
  the ticket: the dispatch falls to the next layer and records one
  composition-scoped `step_tier_fallback` event (the affected steps, the
  tier, `missing`/`empty`, the layer that now governs). The view's source
  label names it too.
- Queued: the tier rides this daemon's local pending record (see above);
  a repeated set is the unchanged success. The claim re-judges it against
  the fresh capsule's lane snapshot (Q14 A): a tier whose step the lane no
  longer declares never enters the overlay — one visible
  `step_tier_dropped` event names the work item, the lane, the step and
  the tier, and the claim goes on — so set it again on a step of the lane
  the ticket now runs.
- Pi Web shows the daemon's source string; it has no tier editor yet
  (a follow-up ticket).

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

`step <id>: overlay models=… (posture running, by <user>)` (a tier:
`overlay tier=<name> (by <user>)`; a param:
`overlay param <name>="<value>" …`, every param override of the step;
a clear: `overlay param <name> cleared`) plus
`published: the capsule chain carries it` (or the posture's note for a
parked ticket) and, on a step that already ran or is running, a
`next dispatch:` line saying the change applies on that step's next
dispatch only (the finished round or live dispatch unchanged). Every change
is one `pf watch` event (`override_set` / `override_clear`: the step, the
aspect — `param` with the param's name for a param —, the new value, the
operator).

## Failure branches

| Code | When | What to tell the user |
|---|---|---|
| `DAEMON-400` | a candidate without a harness prefix, an unknown harness, an effort its harness refuses | Quote the daemon's text; the shape is `<pi\|claude>:<model>[:<effort>]`. |
| `DAEMON-400` | a `--tier` name outside the lane tier grammar, or a name the Host `[step_tiers]` catalog does not declare (or the catalog is empty) | Clear the tier (`--clear-tier`) or set one the catalog names (`pf config list --host` shows its tiers); `default` is validated too. |
| `DAEMON-400` | a param the lane snapshot does not declare on the step, or a value that is not one line of at most 256 bytes | Name the params the step declares (`pf workflows --cwd <dir>`); a value goes on one line. |
| `DAEMON-400` | a pi model the Cell's step-agent `models.json` does not declare | Add the model there first (the operator agent dir's `models.json`), then retry; claude candidates are not file-checked. The `[step_tiers]` catalog (cli.toml's step tier list) validates a `--tier` NAME only — a catalog entry never admits a pi model the Cell's `models.json` lacks (doctor's `step tiers catalog` row warns when a user-written candidate sits outside the catalog; the admission stays this file's hard check). |
| `DAEMON-409` | the step will not be dispatched again (no rewind left, effectively skipped, a completed lane), a skip/param on a live or done step, the step is not declared skippable, or is skipped at the project level (`--unskip` refused naming it) | A models/tier overlay on a done or running step is accepted while the lane can still dispatch it; otherwise set it on a step that will run, or while the ticket is parked; for a gate that must pass, use the waive; a project-level skip is the project's switch, not this ticket's. |
| `DAEMON-403` | a step or task agent's process tree called | The overlay is the owner's move; call it from a human session. |
| `DAEMON-404` | this daemon holds neither a job nor a capsule of the ticket, and the ticket is not queued (an id aihub does not know, one running on another machine, a terminal one with no scene here) | Set the overlay on the machine that runs (or last ran) the ticket; a queued work item answers here — its params ride the `step_params` attrs key. |
| `DAEMON-409` | a queued work item's lane does not load on this daemon (its params/tier are written against its lane), or its chain/skip write reached the queued posture | Write the params/tier on a machine whose lane set has the bundle; the chain and the skip switch wait until the ticket runs. |

Pi Web's job detail reaches the same face through this verb's routes; the
panel offers the single-candidate form (harness + model) on an unstarted
step and on a done/live step whose view row says `can_override_models`
(with the next-dispatch note; polyforge#270), the skip toggle of a
skippable unstarted step and — per declared param — a set/clear row
(a tier shows its source string and has no editor yet; polyforge#81)
(queued work items get theirs in the 工单详情; a started step keeps its
params read-only). `pf params <id>` is the composed read both surfaces
display.
