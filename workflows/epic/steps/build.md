---
name: build
description: The epic's fix build - a rewind_only step that never runs on the first round (the children's landed work is the change); it runs only after a gate FAIL or an owner's rewind covers it, inheriting the plan and answering every point that sent the lane back, never re-planning.
---

# build

The epic's fix build. It runs ONLY after a rewind covers it: on the first
round the lane records it skipped (the children's landed work is the
change, already reviewed and verified on their own lanes); a gate FAIL
back to you, or an owner's rewind naming you, is the work to do — the
increment the lane sent back, not the epic again. Your skill is the whole
procedure; on this lane its 3a plan is INHERITED, never re-made.

## 开工前读什么、核对什么 — before you start: what you read and check

1. **What sent the lane back.** The 回退 section of `artifacts/handoff.md`
   that named this step, and the FAILing gate's section (its 结论 and
   发现的问题和风险, its evidence files): every point raised gets an
   answer in your 回应上一轮.
2. **The inherited plan.** `artifacts/plan.md`, `artifacts/build/units.json`
   and the plan's `## Coverage` as an earlier round left them — cumulative,
   never rewritten; a rewind that moved the default branch past
   verification (a sync round, no gate findings) leaves the plan standing
   and your build with nothing to change: hand off `done` saying exactly
   that, per point.
3. **A plan that does not exist** (a FAIL arrived before any build ran):
   you are the first build — run your skill's 3a against the FAIL's points
   and the epic's own increment only, and say so in your hand-off.

## Procedure

Your skill's, with these lane bindings: the units are red then green,
recorded in `artifacts/build/units.json` and checked as the skill says;
the change analysis (`artifacts/build/change-analysis.json`) names the
tests the full verification runs beside the engine's own selection —
write it, its supplement cap the lane's `vars.supplement_limit`
({{supplement_limit}}); the fast checks run over the whole increment;
the close's facts checks (`facts.py build` with the replay and
`carries`, and `plan-conformance` beside them when the plan is this
lane's own — an inherited plan skips that line; the skill's own lines)
run over it too, a red fixed before
the hand-off; a plan this lane writes maps the ticket's `## 已定`
rulings in its `## 已定对照` section, as the skill states, an unmapped
ruling red; the
self-review reads the full diff review judges — the WHOLE epic against
the default branch
(`git diff $(git merge-base HEAD origin/<the default branch>)..HEAD`) —
against review's five blocking classes, naming within it the increment
this round owns (the commits since the heads the FAILing gate's header
stamps). `pf step ask` only on the cases
your skill names, right after the
plan, never at the end.

## 完成判据 — done criteria

`done` only when every item below holds:

1. **Every point the round that sent the lane back raised** — the 回退's findings or the owner's words, numbered as there — has its numbered answer in 回应上一轮: a commit, a `units.json` entry, or the evidence for leaving it as it was.
2. **Every unit of the inherited plan** is either unchanged for a recorded reason, or a plain unit, a `pre_existing_red` unit or a doc-type criterion the skill lets through — `artifacts/build/units.json` with the close's replay `artifacts/facts/build.json`; **every criterion** of the plan's `## Coverage` keeps its row.
3. **Every existing test that `artifacts/build/change-analysis.json` lists** passes at HEAD, and its `uncovered[<repo>]` names every changed path no `carries:` declaration covers.
4. **The close's facts results** are green (`artifacts/facts/build.json` with the replay, `artifacts/facts/carries.json`, and `artifacts/facts/plan-conformance.json` for a plan this lane wrote), or one whose exit 2 stayed 2 after the retries is disclosed with its summary and left to review to re-run; `artifacts/self-review.md` lists every document compared with its conclusion.
5. **Every repo the increment touched** has a green fast-check result at HEAD — `artifacts/build/fast-checks/<repo>.json`.

## 交接要写什么 — what your hand-off carries

- **Status.** `done` only on a green increment; a real blocker is
  `blocked` with `--reason`.
- **回应上一轮**: every point of the FAIL or the owner's words, numbered
  as there — the commit or the units.json entry that answers it, or the
  evidence for leaving it as it was.
- **做了什么**: the units, their commits and who built them; the fast
  checks. **验证**: each unit's red and green, the fast checks,
  `artifacts/self-review.md`. **给下游与未决**: what review should look at
  first.
- **Evidence**: your skill's list — `artifacts/plan.md`,
  `artifacts/build/units.json`, `artifacts/build/change-analysis.json`,
  `artifacts/build/units-check.json`,
  `artifacts/build/fast-checks/<repo>.json`, `artifacts/facts/build.json`,
  `artifacts/facts/carries.json`, `artifacts/self-review.md`.
