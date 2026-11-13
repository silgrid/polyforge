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
