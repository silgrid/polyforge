---
name: repair
description: The batch's in-batch repair round - the gate's red handed back over units every one of which is attributed and fixable by touching only tests or documentation - fix them on one local branch per repo off the pinned heads, verify each unit's own rerun command green on the host, write the repair plan the review judges and the revert step lands, and never declare the gate green.
---

# repair

The full gate judged the batch red and handed it back to you. Your skill
is the whole procedure — what you read before you start, how you fix and
verify, what you ask the owner and what your hand-off carries. You run
only after a rewind covered you: on a round that reached you with no
repair brief of its own, change nothing, commit nothing and hand off
`done` — the steps after you run exactly as they would without you.

## 开工前读什么、核对什么 — before you start: what you read and check

1. The repair brief `artifacts/integrate/repair.json` — the gate red the
   batch owes work to; its `round` is the gate's, never yours — the
   clones and the pins it names: your skill's first section reads them.
2. An earlier `repair` section of `artifacts/handoff.md` and the
   review's latest findings when the lane came back to you.

## 完成判据 — done criteria

`done` only when every item below holds on its own branch; the full gate's green is never this step's to declare — only its re-run says so.

1. **Every unit of an accepted brief** — each `units[]` entry of `artifacts/integrate/repair.json` — has one unit in `artifacts/integrate/repair-plan.json`, whose `brief_round` equals the brief's `round`.
2. **Every unit's rerun** either exited 0 on the host with its log path in the plan, or the host could not run it and the plan records why — the gate's re-run judges it.
3. **Every blocking finding of the latest review** has its numbered answer in 回应上一轮 — the commit that fixed it, or why it stands.
4. **A round with no brief of its own, a brief the owner refused or ruled reverted, or a hand-back** changes nothing, leaves no plan file, and says so in the body — that is `done` for that round.

## 交接要写什么 — what your hand-off carries

Your skill's own list: the status, each blocking finding answered, the
units fixed on which branch with the host green, the owner messages you
acted on, and the evidence files — `repair.json`, `repair-plan.json`,
the rerun logs.
