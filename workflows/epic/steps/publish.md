---
name: publish
description: Publish the verified epic - push the branch derived from the epic branch, open its PR against the DEFAULT branch with the delivery note (the total acceptance, the children's PR links, the seam resolutions), ask the owner-review question when one is declared, never merge and never run a test.
---

# publish

You publish the epic the lane verified: push the feature branch, open or
update its PR — against the default branch (the epic's merge target; the
capsule's `runtime-state/target-branch.json` carries a `checkout`, no
`target`, so `<base>` in your skill's procedure is the origin's default
branch) — and write the delivery note. Your skill is the whole procedure;
the PR body below is the epic's own.

The PR body, in this order: the epic's slug and a change summary over the
whole diff; the sync record (`artifacts/sync/result.json`) — per repo the
default branch's commit merged in and the conflicts resolved mechanically,
verbatim as the owner's to read; the review's verdict and its seam map;
the verified result (its `verdict` and every check of
`details.checks[]`); the total acceptance (the ticket's own list) with a
one-line judgement per criterion (the review's); the children — per child
of `runtime-state/children.json` its slug and PR link(s); a
**`## Follow-up`** section listing every non-blocking finding of
`artifacts/review.md` (or "none"); and the closing line in the mode the
merge runs in (the skill's three forms). A plan's `## Coverage` table
appears only when a rewind fix build wrote `artifacts/plan.md` — the
first round has neither and the body says so in the summary line.

## 开工前读什么、核对什么 — before you start: what you read and check

1. Your skill's checks 1 and 2 (the verified result file's heads and the
   review's pass) — a check that does not hold is this gate's FAIL.
2. `runtime-state/children.json` parses (the PR body names every child);
   `artifacts/sync/result.json`; `artifacts/review.md`.

## Procedure

Your skill's, with `<base>` the default branch and the PR body above.

## 交接要写什么 — what your hand-off carries

- **Status.** `done` on the skill's own conditions; `fail` on its check
  findings; `blocked` when the forge is unreachable, the reason as
  `--reason`.
- **正文**: per repo the PR link and the pushed head; the body file's
  path; the owner-review record when one was declared.
- **Evidence**: `artifacts/publish.json`, `artifacts/delivery.md`.
