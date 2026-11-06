# integrate — the integration lane

The second of three lanes: develop builds, reviews, light-verifies, publishes and merges
each work item into the default branch at the end of its own run, and wraps it with the
attrs `integration: {state: landed, repos, merge_commits}` (and the owner's waivers as
`waivers`); this lane gates the default branch once over everything that landed and marks
the releasable commit; the release lane ships the newest marked commit. The `steps`
array of its `workflow.yaml` is the only execution order.

```
collect --> revert --> full-gate --> close
  mid       cheap     (no tier)      cheap
 (pool)   (merge lock)  (heavy)     (record)
                ^           |
                +-- FAIL ---+  (the owner's revert: gated again in the same batch)
```

A batch is a work item on this bundle, filed by a person like a work item on any lane —
`pf file create --bundle integrate --project <p> --goal "<what to gate>"`, then `pf submit
<batch>`; the daemon files none of its own. The lane declares `acceptance: none`: a batch
lists no `## Acceptance` (there is no criterion to write — the gate is the check), and `pf
submit <batch>` submits it as is, with no `--allow-no-acceptance`. One batch at a time per
project: the lane declares `exclusive: true`, so the daemon forwards its create to aihub
exclusive, and aihub refuses it atomically while another work item of the project on this
bundle is not wrapped, failed or canceled — across machines too. The refusal names the open
batch and its status; one filed but never claimed is pointed at `pf submit` or `pf cancel`.

A hold — a landed item a red batch blamed — is cleared only by a running batch: its collect
step puts a hold nothing can release to the owner (`pf step ask`), and the agent clears it
on the owner's word, citing the reply (`--per-owner`). Nothing outside a batch clears one.

It is a lane like any other: nothing in the daemon names it, computes a pool, applies an
effect or shows a face of its own for it. Fix tickets are filed on the lane each skill
names (`--lane develop`). The tiers are placeholders the workspace yaml binds to real
models (`tiers.<project>.<tier>.models`, or a step's `steps.<project>.<step>.models`;
[config](../../docs/reference/config.md)); `full-gate` names no tier — it runs on its step
key, else the project default (the strong model).

**How the steps connect.** Every step reads `artifacts/handoff.md` first and ends with one
`pf step handoff` (`workflows/_shared/step-header.md`). The pre-written commands read the
project's integration pool themselves, through the step's hub reads (`pf step hub get` /
`list`: the landed items, the open batches, the heads the newest red batch failed on, the
state of every registered fix ticket). Every aihub write a step leads to — a hold cleared,
a fix ticket filed, a landed item moved — is a hub write the step registers (`pf step hub
batch @<ops file>`, each step only the verbs its `hub` allows and only the attrs key
`integration`; an empty ops file is a recorded no-op); the daemon makes them, in order,
only when the step hands off `done`, and drops them on any other status. A record write is
the item's whole `integration` record, built by the lane's scripts under its rules
(`pfstep/integration.py`: a move only from `landed` — to `landed`, `merged` on a green tag,
or `reverted` — fenced on the whole record as read, the develop lane's keys never rewritten, a hold cleared
only on the owner's word); aihub keeps none of them. The
batch's record is registered by `close` for the wrap (`pf step hub wrap-attrs
integration_batch`). What a later step's command needs travels in files, never in a body:
`batch.json`, `rulings.json`, `revert.json`, `gate-decision.json`, `gate.json`,
`triage.json`.

## What each step does

- **collect** (`integrate.py collect`, mid tier; hub `get list batch file`, attrs `integration`)
  — pins each task repo's default-branch head in `artifacts/integrate/batch.json` and takes
  the batch: the landed items each of whose merges is in its repo's pinned head, one of
  them in a gated repo and under no green tag yet. Another unfinished batch of the project
  refuses the batch (`refused`: nothing is pinned, the steps after it do nothing). The
  command also judges every hold: a landed item a red batch blamed holds the green tag
  until a gate passes over its fixes; when none of its registered fix tickets is open any
  more the hold is **stuck**, and the agent puts every stuck hold to the owner in one
  `pf step ask` — revert (into `artifacts/integrate/rulings.json`'s `reverts`, for the
  revert step), clear (the record's write to `merged` citing the owner's message, the
  owner's words as the reason) or fix (a fix ticket `file` with its acceptance, registered
  on the held item's record) — both built by `integrate.py records`, which refuses a clear
  while another batch of the lane runs.
  It always writes `artifacts/integrate/rulings.json` (the reverts and the hold rulings,
  the owner's words verbatim), which revert and close read.
- **revert** (`revert.py`, cheap tier, the merge lock; hub `get batch`, attrs `integration`) — carries out the
  reverts: the ids in `rulings.json`'s `reverts`, plus those in
  `artifacts/integrate/gate-decision.json`'s `reverts` after a full-gate FAIL (the owner's
  revert after a red gate), each passed as `--item`; without one it only copies the pins.
  Each item and its landed fix tickets are reverted per repo, newest first, on a revert PR
  opened and merged under the repo's merge lock — the one develop's `merge` step holds;
  the gate's pins follow the reverts (`artifacts/integrate/revert.json`). The command
  writes each reverted item's move to `reverted` (`artifacts/integrate/revert-ops.json`),
  which the step registers. A revert the forge refuses is put to the owner.
- **full-gate** (the script `gate.py --isolation container --capsule --pins`, no tier;
  heavy admission; a gate; hub `get list batch file`, attrs `integration`) — runs the release's test gate
  once on every gated repo's pinned head, inside its verification container, with
  `--mark-green`, so a pass writes the green tag itself. Nothing gated → result
  `empty`. Green → `green`. Red → `gate.py --triage` (re-runs, the baseline, attribution,
  bisection); the agent decides one fix ticket per root cause (filed on `develop`, high
  priority, under its stable key, so a resumed batch never files twice) and an attribution
  per blamed source, registered as hub writes, then `red`. A source red after two fix
  rounds, or a pre-existing or unattributed red, goes to the owner: revert (a `fail`
  citing the owner — back to revert, charged to nothing), rerun, or close red. It writes
  `artifacts/integrate/gate-decision.json` (`result`, `reverts`) before every hand-off.
- **close** (`integrate.py close`, cheap tier; hub `get list batch`, attrs `integration`; wrap attrs
  `integration_batch`) —
  reads the batch, the reverts, the rulings, the gate and its decision, decides the result
  (green: every judged repo passed and carries its tag; red: a judged repo failed, its
  heads the record's red heads; empty: refused, nothing gated or no gate), names every
  landed item a green tag — this batch's or an earlier one's — covers, and writes their
  moves to `merged` (`artifacts/integrate/close-ops.json`) and the batch's record
  (`artifacts/integrate/integration-batch.json`: `{result, main, tags, items, waived,
  green_items, fix_tickets, hold_rulings, reverts, refused, empty, closed_at}`, the fix
  tickets read from the step call ledger). The step registers both. A red batch tags
  nothing; the heads its gate failed on are the red heads the next batch's pool carries.
  As the lane's last step it writes the work item's wrap summary.

## Human stops

The daemon asks nothing: each step's own agent asks, with `pf step ask`, and acts on the
owner's free-text reply — a stuck hold (collect), a revert the forge refused (revert), a
source red after two fix rounds or a pre-existing / unattributed red (full-gate), an
upstream file that does not hold (any step). The owner answers in the work item's panel or
with `pf answer <wi> "<words>"`; the agent states its reading of the words in its hand-off,
where the next step can check it against the words the daemon quotes. A spent rewind budget
(`max_rewinds: 3`) is full-gate's to ask about too: one more round, close red, or cancel.

## Records

`artifacts/integrate/` in the batch's capsule: `batch.json` (collect's result: the pinned
heads, which are gated and why not, the batch's items, the holds), `rulings.json`
(collect's: the reverts and the hold rulings), `collect-records.json` and `collect-ops.json`
(collect's record entries and the hub writes `integrate.py records` built from them),
`revert.json`, `revert-plan.json` and `revert-ops.json` (revert.py's result, its resumable
plan and its moves), `gate.json` and `triage.json` (gate.py's results; the logs under
`artifacts/integrate/logs/`), `gate-decision.json`, `full-gate-records.json` and
`full-gate-ops.json` (full-gate's decision, record entries and hub writes), `close.json`, `close-ops.json` and `integration-batch.json`
(integrate.py close's result, its moves and the record). Every hub write a step registered,
and what the daemon made of it, is `runtime-state/step-calls.jsonl`. A resumed batch re-runs
its current step from them: each command's result carries the round it was written in, a
filed ticket is never filed twice, a half-done revert is finished first.
