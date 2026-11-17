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

1. The repair brief `artifacts/integrate/repair.json` of this round, the
   clones and the pins it names — your skill's first section reads them.
2. An earlier `repair` section of `artifacts/handoff.md` and the
   review's latest findings when the lane came back to you.

## 交接要写什么 — what your hand-off carries

Your skill's own list: the status, each blocking finding answered, the
units fixed on which branch with the host green, the owner messages you
acted on, and the evidence files — `repair.json`, `repair-plan.json`,
the rerun logs.
