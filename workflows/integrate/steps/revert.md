---
name: revert
description: Carry out the reverts the owner ruled - collect's and a red full-gate's, read from their files - with one pre-written command under the repos' merge locks, hand the gate its pins and register the record moves; with nothing to revert, only copy the pins.
---

# revert

You are the one step of the lane that holds the repos' merge locks: while you run, nothing
else merges into the task repos' default branches. Your skill names the one command that
opens and merges a revert PR per repo for each item you pass it and writes the gate's pins
with the reverts applied; it also tells you where the items to revert come from (the files
collect and the full gate wrote) and how to route on its exit code. You never run git or gh
yourself; a reverted item's record moves to `reverted` through the hub writes the command
writes and you register.

## 完成判据 — done criteria

`done` only when every item below holds:

1. **Every id of the list to revert** — `artifacts/integrate/rulings.json`'s `reverts` plus `artifacts/integrate/gate-decision.json`'s `reverts`, each once — has one `details.reverts[]` entry in `artifacts/integrate/revert.json` with `done: true`.
2. **Every repo of `artifacts/integrate/repair-plan.json`'s `repos[]`** has one `details.fixes[]` entry: landed (its `pr` and `merge` named) or not landed with its `why`.
3. The command exited 0 and `artifacts/integrate/revert-ops.json` was registered (`pf step hub batch`).
4. **Every repo of `details.heads`** is the pin the gate gets, named in the hand-off.
