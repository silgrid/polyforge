---
name: sync
description: Merge origin's default branch into every task repo with the pre-written sync script (the sync skill's `sync.py` lines), resolve a real conflict in place and continue the same script, then run the lane's fast-check line over every repo the merge moved - no gate, no rewind of its own; the hand-off gate needs that pass at HEAD, and the {{verifying_gate}} step right after tests the resolved tree.
---

# sync

The lane's merge of the default branch into the branch, after review passed and before
the `{{verifying_gate}}` step runs. Your skill is the whole procedure: check that every task
repo stands at the tree review judged, run the skill's start line as written, resolve a real
conflict in place and run its continue line, run the fast-check line over every repo the merge
moved, and ask the owner when a resolution is not yours to make. The result file the script
writes (`artifacts/sync/result.json`) is what the `{{verifying_gate}}` step checks first, and
the fast-check result (`artifacts/sync/fast-checks/<repo>.json`) is what the daemon's hand-off
gate checks before `done`. A real conflict never sends the work back to
`{{change_step}}` by itself: only the owner's word does, after the skill's abort line.

## 完成判据 — done criteria

`done` only when every item below holds, each over the set it names:

1. **Every repo** of `artifacts/sync/result.json`'s `details.repos[]` stands at its `after` commit with an empty `conflicts[]`, and the start line (or, after a conflict, the continue line) exited 0.
2. **Every file the script resolved mechanically** — `details.repos[].mechanical[]` — and **every conflict file you resolved** (`resolved[]`) are named verbatim in the hand-off body.
3. **Every repo the merge moved** has a passing fast-check result at HEAD — the daemon's hand-off gate reads `artifacts/sync/fast-checks/<repo>.json`; a fast-forwarded repo needs none, a red the check triages `pre_existing` is none of this merge's, and a released red is handed off citing the owner's message.
4. The `{{verifying_gate}}` step is told **every file you resolved** (`resolved[]`), so it tests the tree this step leaves.
