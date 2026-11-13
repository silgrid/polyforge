---
name: merge
description: Merge the published PRs at the heads the {{verifying_gate}} step ran over with the pre-written merge script (the merge skill's `merge.py` line) and register the landing record the work item is wrapped with; a PR that moved, closed or was refused FAILs back to sync.
---

# merge

The lane's last step: the published PRs merged into the default branch at the heads the
`{{verifying_gate}}` step ran over, under the daemon's per-repo merge lock, by one
pre-written command. Your skill is the whole procedure: check publish's hand-off against
the verified heads, reuse this round's result or run the line as written, route on the
exit code, and register the landing record the command wrote
(`pf step hub wrap-attrs integration @artifacts/merge/landing.json`). Its pass ends the
workflow: the work item is wrapped with the attrs `integration: {state: landed, repos,
merge_commits}` — the daemon adds the owner's waivers as `waivers`, never you — awaiting
the integration lane's full gate. As the lane's last step you write the work item's wrap
summary before you hand off `done`.
