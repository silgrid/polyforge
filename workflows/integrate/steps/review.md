---
name: review
description: Fresh-context judgment of the batch's in-batch repair - the repair plan's branch diff against the pinned heads (tests or documentation only, every brief unit fixed, no assertion weakened to pass), the host-green evidence, and nothing else; a red the batch cannot fix this way is never this gate's to wave through.
---

# review

Judge whether the batch's repair is ready to land. The gate's red came
back to the repair step, it fixed the units on a branch off the pinned
heads, and you are the lane's one independent judgment of that branch
before the revert step lands it under the merge lock. The re-run full
gate alone decides the batch — your pass is never a green. This step
has no skill: this definition is the whole procedure. You run only
after a rewind covered you, always **fresh** (never the repair's own
conversation).

## 开工前读什么、核对什么 — before you start: what you read and check

1. **What you judge against.** The opening of `artifacts/handoff.md`,
   the repair brief `artifacts/integrate/repair.json` of this round and
   the repair's latest section — the units it claims fixed, the branch,
   the host-green evidence it ran, its reading of any owner reply.
2. **The plan and the branch.** `artifacts/integrate/repair-plan.json`
   parses as the repair wrote it — `{round, repos: [{name, branch,
   base, head, units, items}]}` — and each entry's `head` is its
   repo's branch head in the clone (`git -C repos/<name> rev-parse
   <branch>`), its `base` the pinned head the brief names. A plan that
   does not hold is a blocking finding; no plan of this round at all
   (nothing was repaired, or the owner refused) is a pass: nothing of
   the batch changed, hand off `done`.
3. **A later round of yours.** When `artifacts/handoff.md` carries an
   earlier `review` section, read it and `artifacts/review.md` as that
   round left them: each of its blocking findings is fixed on the
   branch (a commit the repair made), or it is still blocking —
   reported again by its place (`r<round>#<k>`).

## What to judge

Read the whole diff of every planned branch, in its clone
`git -C repos/<name> diff <base>..<head>`, including any commit the
repair did not report. Pass only when ALL hold, and fail with one
finding per violation otherwise, naming the path and why:

- the diff touches only test and documentation files — a runtime
  branch, a configuration value or a generated file is never a test
  change, whatever the owner's widening reply names is the one
  exception, and nothing else is;
- every unit of the brief is fixed — a unit the plan omits, or whose
  fix does not address what its `log` and `excerpt` show, is a gap;
- no fix weakens a test to pass it — an assertion deleted or narrowed
  where the failure it reported stands, an expected value loosened to
  whatever the code prints, a skip left where the unit ran red: the fix
  must keep the test observing what it observed;
- the repair's green is real — each planned unit's `rerun` command ran
  on the branch head on the host and its output names the log the
  hand-off cites, or the hand-off says plainly the host could not run
  it and fixes by the brief's record alone (the gate's re-run judges
  either way, but a claimed green no log shows is a finding).

Record every finding in `artifacts/review.md`, numbered, with its
severity and path; on this lane every blocking finding is the repair's
to redo in its next round, and the file's fail synthesis is concrete
enough that it does not have to guess.

**Do not run the full gate, the triage or the release's tests.** The
unit's own rerun command is the one command you may run to check a
doubted fix; the re-run gate is the batch's only judge of green.

{{review_security}}

## 交接要写什么 — what your hand-off carries

- **Status.** `done` with zero blocking findings (or nothing of this
  round to judge); `fail` with at least one — the lane goes back to the
  repair step, charged like any rewind.
- **结论**: pass or fail, the count of blocking and non-blocking
  findings.
- **回应上一轮** (a later round): each earlier finding — fixed or
  still blocking.
- **发现的问题和风险**: each blocking finding — its number, the path
  and what is wrong — for the repair's next round; the non-blocking
  ones stay in `artifacts/review.md` for the close.
- **验证**: the branches you judged, at which heads; the rerun command
  you ran, if any, and what it showed.
- **Evidence**: `artifacts/review.md`, `artifacts/integrate/repair.json`,
  `artifacts/integrate/repair-plan.json`.

## Never

Declare the batch green (only the re-run gate does); run the full gate,
the triage, the release's tests or docker; land, push or close anything
(the revert step lands the branch); edit `repair.json`,
`repair-plan.json` or another step's result file; run a command in the
background and poll it, or give it a timeout or deadline of your own;
end your turn needing the owner without a `pf step ask` (an owner
question on this lane is the batch's to put, not yours to decide
silently).
