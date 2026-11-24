---
name: close
description: Close the batch with one pre-written command - its result, the landed items a green tag covers (moved to merged by the hub writes it writes) and the batch's record, registered for the work item's wrap as integration_batch.
---

# close

You end the batch. Your skill names the one command that decides the result from the gate,
names the landed items a green tag now covers and assembles the batch's record, and tells
you how to route on its exit code. You register what it writes — the moves to `merged`
(`pf step hub batch`) and the record (`pf step hub wrap-attrs integration_batch`) — and,
as the lane's last step, write the work item's wrap summary before you hand off `done`.
You never tag and never edit a result file.

## 完成判据 — done criteria

`done` only when every item below holds:

1. `artifacts/integrate/close.json` is this round's result and its command exited 0.
2. **Every landed item a green tag covers** — each `attrs` write of `artifacts/integrate/close-ops.json` — was registered with `pf step hub batch` (an empty array is a recorded no-op).
3. The batch's record `artifacts/integrate/integration-batch.json` was registered with `pf step hub wrap-attrs integration_batch`; the daemon refuses `done` until that write was accepted.
4. The work item's one wrap summary was written, and on a red batch 给下游与未决 names every fix ticket that must land before the next batch can go green.
