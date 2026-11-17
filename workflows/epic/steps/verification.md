---
name: verification
description: The epic's explicit FULL verification - the whole bar over the whole epic diff against the default branch (the engine's full tier, never the light one), beside the change analysis's listed tests when a rewind fix build wrote one; a red you cannot place is the owner's call.
---

# verification

The epic's machine gate, and it is FULL by declaration: the light tier
never runs here (the epic's diff is the whole change against the default
branch — the children's landed work together with the fix increments —
and only the full bar covers it). Your skill is the whole procedure with
these bindings: check the sync's hand-off, reuse this round's result or
run the line as written with `--tier full`, route on the exit code, and
ask the owner on a red you cannot place. You are the lane's one waivable
gate: a red the owner accepts is your `waive`, citing the owner's
message, and publish reads the words the daemon quotes in your section's
header.

The line's `--analysis` argument: add
`--analysis artifacts/build/change-analysis.json` only when that file
exists (a rewind fix build wrote it); the first round has no build and
the engine's own selection alone is the bar.

## 开工前读什么、核对什么 — before you start: what you read and check

1. **The sync's hand-off.** Its latest section is stamped `done` and
   `artifacts/sync/result.json` is its round's (the tree you run over).
2. **The review's pass.** Review's latest section is stamped `done` and
   `artifacts/review.md` gives the verdict pass.

## Procedure

1. Run your skill's line as written with `--tier full` (and `--analysis`
   only as above): no argument added or removed, no Bash timeout. Route
   on the exit code exactly as your skill says — a red you can place
   FAILs back to the fix build, a red you cannot is the owner's call
   (`pf step ask`), never a silent pass.
2. Hand off `done` naming the result file's path; your status never
   contradicts its `verdict`.

## 交接要写什么 — what your hand-off carries

- **Status.** `done` on the command's pass; `fail` on a red you place
  (the findings numbered); `waive` only on the owner's word accepting a
  red, citing the message's `at`; `blocked` when the run is not judged
  and the skill's retries are spent, the summary as `--reason`.
- **正文**: the tier that ran (full, always), the red findings you placed
  or the waiver's words, and anything publish must know.
- **Evidence**: `artifacts/verify/result.json`.
