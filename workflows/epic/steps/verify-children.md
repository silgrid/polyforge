---
name: verify-children
description: The epic's first preparation step - read the claim's children snapshot, check every child wrapped and actually merged into the epic branch (its landing record's merge commits are the branch's ancestors), report the table; an anomaly is a blocked hand-off to the owner, never a rewind.
---

# verify-children

The epic's first preparation step: the children the claim snapshotted are
all wrapped and actually merged into the epic branch. You read
`runtime-state/children.json` (the claim's immutable snapshot: every child's
id, slug, status and its landing record) and check it against the task
repos — no gate, no rewind of its own; an anomaly you cannot resolve is
this step's `blocked` hand-off (the owner's next message wakes the epic),
never a question the daemon asks and never a FAIL.

## 开工前读什么、核对什么 — before you start: what you read and check

1. **The children snapshot.** `runtime-state/children.json` parses and its
   `version` is the capsule's (a file that is there but does not parse is
   `blocked` naming why — the claim wrote it, so a torn file is the
   machine's to fix). Every child's `status` is `wrapped`; a child that is
   not is the anomaly below.
2. **The epic branch.** Each task repo (`repos/<name>/`) carries the
   capsule's checkout branch (`runtime-state/target-branch.json`'s
   `checkout` key, the branch of your own seq): `git fetch origin` and the
   branch exists on origin. A repo without the branch is the anomaly.

## Procedure

1. Per child of the snapshot, per repo its landing record names: the
   record's `merge_commits` are ancestors of `origin/<the checkout branch>`
   (`git merge-base --is-ancestor`), and their diff content is the child's
   own (`--stat` suffices; the child's record is its evidence, you do not
   re-audit it).
2. Write `artifacts/verify-children.md`: one row per child — slug, status,
   PR link(s) of its landing record, per repo whether the merge commits are
   ancestors — and the epic branch's tip per repo.
3. An anomaly — a child not wrapped or not merged, a branch a repo's
   origin does not carry, a snapshot that does not parse — is a `blocked`
   hand-off: the body names the child, the check and what did not hold,
   `--reason` the one line. The children changed (a new or failed child)?
   The daemon's own re-check (which parks the epic blocked without a
   question) has not fired yet — hand off `blocked` the same way.
4. Everything holds: hand off `done`, the body carrying the row table's
   path and the epic branch's tips.

## 完成判据 — done criteria

`done` only when every item below holds:

1. **Every child** of `runtime-state/children.json` has exactly one row in `artifacts/verify-children.md`, and its status there is `wrapped`.
2. **Every child's every `merge_commits` entry** — per repo, from the child's landing record — is an ancestor of `origin/<the checkout branch>` (`git merge-base --is-ancestor`), and the row records that check.
3. **Every task repo** of the capsule appears in the table with the epic branch's tip.

## 交接要写什么 — what your hand-off carries

- **Status.** `done` only when every child is wrapped and merged into the
  epic branch; `blocked` with `--reason` on an anomaly (the owner decides:
  wait for a child, file a fix child, or cancel).
- **正文**: the row table (its path), the epic branch's tips, and anything
  the sync step must know (a child whose landing names a repo the capsule
  does not carry).
- **Evidence**: `artifacts/verify-children.md`.
