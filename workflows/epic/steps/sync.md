---
name: sync
description: The epic's closing-phase sync - the default branch merged into the epic branch (the branch of the capsule's checkout) with the pre-written sync script, once per closing and again whenever it moved past verification; a conflict you cannot resolve is the owner's, never a rewind.
---

# sync

The epic's own sync: the epic branch's own remote commits merged in FIRST
(the children's — a fix child lands on `origin/<the checkout branch>`
while the epic runs or is parked, and the resumed lane judges a tree that
contains them), then the default branch merged into the branch the
capsule checked out (the epic branch), after the children are verified
and before the lane judges the epic. The epic merges into the DEFAULT
branch, so the base the scripts take is the default branch — exactly
what they do for a capsule without a fixed target (the epic's record
carries a `checkout`, no `target`). This is the closing-phase sync the lane runs once, and the step
the merge gate's moved-base FAIL returns to: the default branch moved past
what verification ran over, the sync merges it in again, and the lane
re-runs from here — the review and the full verification included. Your
skill is the whole procedure: check verify-children's hand-off, run the
skill's start line as written, resolve a real conflict in place and run its
continue line, ask the owner when a resolution is not yours to make.

## 开工前读什么、核对什么 — before you start: what you read and check

1. **verify-children's hand-off.** Its latest section is stamped `done` and
   `artifacts/verify-children.md` names every child merged; a `blocked`
   section instead is not yours to override — the epic stays with the
   owner.
2. **The tree you merge into.** Per task repo (`repos/<name>/`): the
   checkout branch (the capsule's `runtime-state/target-branch.json`
   `checkout` key) is checked out and the tree is clean (a residue from an
   interrupted run is the skill's crash re-entry to handle, not a reset of
   yours).

## Procedure

1. Run the skill's start line as written. Exit 1 (a real conflict left in
   progress): resolve the files in place — the epic's own conflicts are
   yours (its fix build's tree, its children's seams), a child's internal
   file is its own record's to explain — then the skill's continue line.
   A defect found in the merged tree is the `{{change_step}}` step's
   (the fix the rewind runs), never this step's FAIL. Exit 2 (not
   judged): hand off `blocked` with the result's `summary` as
   `--reason`.
2. Exit 0: hand off `done`, the body naming the default branch's commit
   merged in per repo (`default_head`) and every file resolved
   mechanically (`resolved[]`), verbatim as the owner's to read — the
   `{{verifying_gate}}` step runs over the tree you leave.

## 交接要写什么 — what your hand-off carries

- **Status.** `done` only on the script's exit 0; `blocked` with the
  `--reason` a resolution is not yours to make; a FAIL back is never yours
  (a preparation step rewinds nowhere).
- **正文**: per repo the default branch's merged commit and the
  mechanically resolved files; anything review must look at first.
- **Evidence**: `artifacts/sync/result.json`.
