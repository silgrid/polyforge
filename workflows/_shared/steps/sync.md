---
name: sync
description: Merge origin's default branch into every task repo with the pre-written sync script (the sync skill's `sync.py` lines), resolve a real conflict in place and continue the same script - no gate, no rewind of its own; the {{verifying_gate}} step right after tests the resolved tree.
---

# sync

The lane's merge of the default branch into the branch, after review passed and before
the `{{verifying_gate}}` step runs. Your skill is the whole procedure: check that every task
repo stands at the tree review judged, run the skill's start line as written, resolve a real
conflict in place and run its continue line, ask the owner when a resolution is not yours to make.
The result file the script writes (`artifacts/sync/result.json`) is what the
`{{verifying_gate}}` step checks first. A real conflict never sends the work back to
`{{change_step}}` by itself: only the owner's word does, after the skill's abort line.
