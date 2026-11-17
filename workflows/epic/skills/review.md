---
name: review
description: Judge the epic in a fresh context, in the epic mode - the total acceptance, the children snapshot's landing records and their PR bodies, the seams between the children's work and the final diff against the default branch - never re-auditing a child's internals; on a round after the fix build, replay the fix's units as the shared review does; class every finding into the five blocking classes.
commands: [facts.py]
---
# review

You read and judge the EPIC, not its children's internals: each child was
reviewed and verified on its own lane, and its own record is evidence you
read, never a change you re-audit: this gate never re-audits a child's internals. You judge the epic as one change — the
total acceptance, the seams, the final diff — and, on a round after the
fix build, the fix's own evidence beside. The five blocking classes are the
shared ones (`bug`, `ac_gap`, `project_rule`, `test_weakened`,
`guarantee`); a defect inside one child's own work, its own lane already
judged, is a follow-up the owner decides on, never a blocking finding of
this gate unless it breaks the epic's total acceptance or a seam.

## 开工前读什么、核对什么 — before you start: what you read and check

1. **The total acceptance.** The opening of `artifacts/handoff.md` (the
   acceptance list verbatim) and `runtime-state/work-item.json` — the
   epic's own list.
2. **The children snapshot and their PR bodies.**
   `runtime-state/children.json`: every child's id, slug, status and its
   landing record (`state: landed_on_target`, the epic branch, the merge
   commits); a child not wrapped, or a record without landing commits, is a
   blocking `guarantee` finding (the verify-children step passed it — read
   its section too). Read each child's PR body with
   `gh pr view <url> --json title,body` (the PR links are the landing
   record's): its coverage of its own acceptance and its own review's
   non-blocking findings are your seam evidence. `gh` missing or a PR
   unreadable is a follow-up for the owner, never a blocking finding.
3. **The final diff and the seams.** Per task repo (`repos/<name>/`):
   `git fetch origin` then
   `git diff $(git merge-base HEAD origin/<the default branch>)..HEAD` —
   the whole epic. The seams: files two children both touch, an interface
   one changed another consumes, duplicated or missing pieces between
   them, and the sync's conflict resolutions
   (`artifacts/sync/result.json`'s `resolved[]`, verbatim as the owner's to
   read since nobody reviewed them).
4. **A round after the fix build** — only when the 回退 section of
   `artifacts/handoff.md` names it: `artifacts/plan.md`,
   `artifacts/build/units.json` (each unit red then green),
   `artifacts/self-review.md`, `artifacts/build/change-analysis.json` —
   checked and replayed exactly as the shared review skill does (the four
   facts lines, the same classes). When every task repo's `git -C repos/<name> rev-parse HEAD` equals the head that round judged and `sha256sum artifacts/build/units.json` equals that line, the build since changed nothing (the daemon does not stop on it — you judge it). Then do not run the facts again: read the fix build's 回应上一轮 — a point it answers with evidence the finding was wrong is withdrawn with the reason in `artifacts/review.md`; a point left unanswered stands as a residual and hands the epic back to the fix build. On the first round none of these
   exists and none is demanded: the change is the children's landed work.

## Procedure

1. **Judge the total acceptance** against the final diff: per criterion,
   what of the epic's whole change carries it — a child's PR body counts,
   a seam you find broken uncounts it; a criterion nothing carries is
   `ac_gap`.
2. **Judge the seams** with the five classes; the conflict resolutions and
   the overlapping files are where an epic breaks. Check the children's
   landing records against the diff: the branch the capsule checked out is
   `runtime-state/target-branch.json`'s `checkout` key, and a repo whose
   `origin` lacks it is `guarantee`.
3. **Run the facts lines only on a round after the fix build** (the
   increment's own evidence, the shared skill's four lines as written);
   the first round reads the diff directly.
4. **Write `artifacts/review.md`**: the verdict first, the per-criterion
   judgement, the seam findings numbered (blocking and non-blocking), and
   per child its PR link with what its own review left open.
5. **A defect the fix build can carry**: hand off `fail`, every finding
   numbered with its file and line (the lane goes back to the fix build).
   **A big problem** — a child's change wrong at its root, a criterion no
   fix build should carry — `pf step ask` the owner proposing a separate fix sub-ticket (a new work item under the epic, on the children
   branch; its own lane's contract tests and driver tests red before its
   fix), then end your turn; the owner's answer decides.

## Never

Never end a turn that needs the owner without `pf step ask`; never
run a command in the background and poll it, or give it a timeout or
deadline of your own.

## 交接要写什么 — what your hand-off carries

- **Status.** `done` (a pass) or `fail` (the findings, numbered); never
  `waive` — the gate is not waivable.
- **正文**: the verdict, the blocking findings with files and lines, the
  seam map in one line each, and on a later round your 回应上一轮 answers.
- **Evidence**: `artifacts/review.md` (and the facts result files on a
  round after the fix build).
