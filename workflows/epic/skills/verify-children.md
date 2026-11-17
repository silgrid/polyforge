---
name: verify-children
description: Check the epic's children snapshot against the task repos - every child wrapped and its landing record's merge commits actual ancestors of the epic branch on every origin - and report the table; an anomaly is a blocked hand-off to the owner, never a rewind.
commands: []
---
# verify-children

You check what the claim snapshotted: the children are all wrapped and
actually merged into the epic branch. One bounded pass, no test runs, no
rewind of your own; an anomaly you cannot resolve hands the epic to the
owner (`blocked`), never a FAIL back.

## 开工前读什么、核对什么 — before you start: what you read and check

1. **The children snapshot.** `runtime-state/children.json` parses, its
   `version` is a positive number, and every child carries `id`, `slug`,
   `status` and a `landing` record; a file that is there but does not parse
   is the machine's (the claim wrote it): hand off `blocked` naming the
   parse error.
2. **The epic branch.** The capsule's `runtime-state/target-branch.json`
   names the checkout branch (its `checkout` key — the branch of the
   epic's own seq); each task repo (`repos/<name>/`) has it checked out.

## Procedure

1. `git -C repos/<name> fetch origin` per repo, then per child of the
   snapshot, per repo its landing record names:
   `git -C repos/<name> merge-base --is-ancestor <merge commit> origin/<the checkout branch>`
   — every merge commit is an ancestor. A child's `status` other than
   `wrapped`, a missing branch on an origin, or a merge commit that is no
   ancestor is the anomaly.
2. Write `artifacts/verify-children.md`: one row per child (slug, status,
   the landing record's PR links, per repo whether the merge commits are
   ancestors) and the epic branch's tip per repo.
3. **Anomaly**: hand off `blocked` — the body names the child and the
   check that did not hold, `--reason` the one line (the owner decides:
   wait for a child, file a fix child, or cancel; the daemon never asks a
   question of its own).
4. **Everything holds**: hand off `done` — the body names the table's path
   and the epic branch's tips.

## Never

End a turn that needs the owner without `pf step ask` (a question only
the owner can answer); never run a command in the background and poll
it, or give it a timeout or deadline of your own — each check is a
foreground call that ends with its exit code; never hand off `done` on
an unverified child.

## 交接要写什么 — what your hand-off carries

- **Status.** `done` only when every child is wrapped and merged into the
  epic branch; `blocked` with `--reason` on any anomaly.
- **正文**: the row table's path, the epic branch's tips per repo, and
  anything the sync step must know.
- **Evidence**: `artifacts/verify-children.md`.
