---
name: review
description: Judge the epic in a fresh context, in the epic mode - the total acceptance, the children's landing records and PR bodies, the seams between the children's work and the final diff against the default branch - never re-auditing a child's internals; a big problem is the owner's, asked as a separate fix sub-ticket; a defect the lane can fix in place FAILs back to the fix build.
---

# review

The epic's review gate, in the epic mode: the lane orchestrates — it never
re-audits a child's internals (each child was reviewed and verified on its
own lane). You judge the epic as ONE change: the total acceptance (the
work item's own acceptance list — `## Acceptance` or `## 验收`), the
seams between the children's work and the final diff against the default
branch. Your skill is the whole procedure and the five blocking classes
are its judgement.

## 开工前读什么、核对什么 — before you start: what you read and check

1. **The total acceptance.** `runtime-state/work-item.json`'s acceptance
   section — the epic's own judged list, not a child's.
2. **The children snapshot.** `runtime-state/children.json`: every child,
   its landing record (`state: landed_on_target`, its merge commits into
   the epic branch) and its PR links — read each child's PR body with
   `gh pr view <url> --json title,body` (the body carries its own review's
   non-blocking findings and its acceptance coverage).
3. **The final diff and the seams.** Per task repo (`repos/<name>/`):
   `git fetch origin` then
   `git diff $(git merge-base HEAD origin/<the default branch>)..HEAD`
   — the whole epic against the default branch. The seams are where the
   children's changes meet: files two children both touch, an interface one
   child changed another child's work consumes, duplicated or missing
   pieces between them, the sync's conflict resolutions (its result file's
   `resolved[]`, verbatim as the owner's to read since nobody reviewed
   them).
4. **A rewind fix's evidence** — only when the 回退 section of
   `artifacts/handoff.md` names the fix build: `artifacts/build/units.json`
   (each unit red then green), `artifacts/plan.md`, `artifacts/self-review.md`
   and `artifacts/build/change-analysis.json` as your skill's replay
   demands. On the first round none of these exists and none is demanded.

## Procedure

1. Judge the total acceptance against the final diff: per criterion, what
   of the epic's whole change carries it (a child's PR body's coverage
   counts as evidence; the criterion no child and no seam change carries is
   an `ac_gap`).
2. Judge the seams: the conflict resolutions, the overlapping files, the
   interfaces between the children's work — the five blocking classes of
   your skill (`bug`, `ac_gap`, `project_rule`, `test_weakened`,
   `guarantee`) apply to the EPIC's seams and total acceptance; a defect
   INSIDE one child's own work its own lane already judged is the child's,
   named in your report as a follow-up, never a blocking finding of this
   gate unless it breaks the epic's total acceptance or a seam.
3. A defect the lane can fix in place (a seam, a missed criterion the fix
   build can carry): hand off `fail`, every finding numbered with its file
   and line — the lane goes back to the fix build, which inherits the plan.
   A BIG problem — a child's change is wrong at its root, a criterion
   needs a change no fix build should carry — is the owner's: `pf step ask`
   proposing a separate fix sub-ticket (a new work item on the epic's
   children branch, its own lane's contract tests and driver tests red
   before its fix), then end your turn; the owner's answer decides.
4. Write `artifacts/review.md`: the verdict, the criterion-by-criterion
   judgement, the seam findings (blocking and non-blocking), and per child
   its PR link with what its own review left open.

{{review_security}}

## 交接要写什么 — what your hand-off carries

- **Status.** `done` (a pass) or `fail` (the findings, each numbered) —
  never `waive`: this gate is not waivable.
- **正文**: the verdict first, then the blocking findings with their files
  and lines, the seam map in one line each, and — on a round after the fix
  build — your 回应上一轮 answers per finding.
- **Evidence**: `artifacts/review.md`.
