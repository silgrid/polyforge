---
name: build
description: The develop lane's one build session - plan against the work item's acceptance criteria (a fresh-context analysis sub-agent reproduces a reported defect and writes the plan), then per unit (a unit-level test or an e2e scenario) a test committed red before the implementation that turns it green, each unit's run recorded in artifacts/build/units.json (implementer sub-agents build a big plan's units), fast checks over the whole change, and a self-review against review's five blocking classes. It asks the owner only on a surprise, right after the plan, never at the end of its work.
---

# build

One session builds the whole change (3a plan, 3b units test-first, 3c close, 3d self-review);
`review` reads it next, fresh. Your skill is what you read before you start, how you ask the
owner, how a round a FAIL opened must change the tree, how each unit's run is recorded in
`artifacts/build/units.json`, and what your hand-off carries. On a later round (a 回退 in
`artifacts/handoff.md` named this step) keep the plan, answer every point raised, and keep
`units.json` and the plan's `## Coverage` cumulative.

{{recall}}

## Sub-agents

{{build_subagents}}

## 3a. Plan

Item 2 below, and reproducing and locating a reported defect, are the analysis sub-agent's
(`## Sub-agents`): its instruction is that item in the capsule's `workflows/_rendered/build.md`,
and folding an owner's answer into the plan is its too. You read the work item (1), ask the owner
only in the cases item 3 names, and judge the plan; without sub-agents, do it all.

1. Read the work item (`runtime-state/work-item.json`). It was settled before it was filed: its
   background carries the decisions, and its acceptance section (`## Acceptance` or `## 验收`) is
   the judged list — each list item one criterion, its id the item's leading `AC<n>` token, else
   `AC<n>` by 1-based position. Do not grill it again: no clarity checklist, no batched round of
   questions; what the ticket, the code and recalled memory settle, you decide and record.
2. Write `artifacts/plan.md`: `## Acceptance` (the judged list, its ids — the ticket's verbatim;
   for a ticket without one, the list you propose); `## Non-goals` (what the ticket or an answer
   leaves out, or `none`); `## Units` — ordered compilable units, each with its id, the criterion
   ids its test carries, its test (a unit-level test, or an e2e scenario in a new or extended
   suite and the `carries:` globs that suite declares), its fast-check commands, its
   test-quality record (the behaviour its test observes, how its expected values are
   derived independently of the implementation, the change it catches — the bar below), `Touched files:
   <path> (write|read), ...` and `Coupled obligations:` (what must change with each write — the
   repo's `AGENTS.md` names its known couplings — or `none` and why); `## Coverage` — per
   criterion `test` (its unit's test), `existing` (the test already pinning it) or `doc` (the file
   and its validator) — the table your skill gives — and for a user-visible criterion its e2e
   scenario or one line why a unit test suffices.
3. **When to ask the owner**: only in the four cases your skill names — (a) a dead end or a
   contradiction, (b) a deviation, (c) `confirm_plan: true`, (d) a missing or incomplete
   acceptance list — right after the plan, never at the end of your work, with `pf step ask`.
   Its question carries {{batch_questions}}

{{test_quality}}

## 3b. Units, test first

Who builds each unit is `## Sub-agents`. An implementer's instruction is this section's per-unit text
in that file (its fast-check placeholders: `workflows/_shared/fast_checks.md`, `fast_checks_build.md`).

Per unit, in plan order — an e2e scenario is a unit like any other: (1) write its test, by the
project's convention; every suite file opens with a `carries:` header (`// carries: <glob>, ...`,
`#` where comments start with `#`); in a suite you add, or whose run a unit records, its globs, relative
to the suite's project root (where its `.polyforge/verify-scope.conf` is), name the unit's written
`Touched files` — the verification-scope engine selects suites by it. An e2e scenario polls the
condition it asserts (the element, the text, the settled value) — never a fixed delay instead of
polling; a bounded poll deadline is fine — and measures only once the state has settled: a value a
later callback recomputes (a width a ResizeObserver sets after a resize) is read when it stops
changing. The test is red on an assertion when run targeted (a test needing a symbol that does not
exist yet gets the signature with a zero-value body in an ordinary commit first, so the red is never
a compile error); (2) commit the test files alone (T_i, `test_commit`) and run the unit's targeted
command on that clean commit: red; (3) implement until it is green, then run the fast checks:
{{fast_checks}}
{{fast_checks_build}}
(4) commit the implementation (C_i, `impl_commit`), run the same command from the same directory
on that clean commit: green; (5) record the unit in `artifacts/build/units.json` as your skill
says — the run exactly as it ran red and green, and its marker. A unit is not recorded until
the written check has passed it: `"$PF_PYTHON" "$PF_SHARED_DIR/scripts/fast_checks.py"
--check-units artifacts/build/units.json --unit <id> --json --out artifacts/build/units-check.json`
right after adding it (verification's own rules: a red names the unit, its marker and the
closest output line — fix the record and re-check).

A defect ticket's first unit is the reproduction test, red on the reported failure before any fix;
cannot reproduce it → 3a's case (a). A doc-type criterion gets no red test and no unit: commit the
change, run its validator and name it in its `## Coverage` row.

## 3c. Close and 3d. Self-review

Run the fast checks over the whole change (the written command once per touched repo) and
every unit test targeted, then the written unit check over the whole `artifacts/build/units.json`
(the same command with no `--unit`) (the full bar is `verification`'s). Read your full diff (per repo `git diff
$(git merge-base HEAD origin/HEAD)..HEAD`) as `review` will, against its five blocking classes:
`bug`, `ac_gap`, `project_rule`, `test_weakened`, `guarantee`; write each hit and its disposition
to `artifacts/self-review.md` and fix what you can now (a new unit for a behaviour fix).

Write the change analysis — `artifacts/build/change-analysis.json`, the structured file
verification supplements the engine's own selection with (`verify.py --analysis`, add-only,
never narrowing it), in this shape. Its keys are the machine's contract — `supplement_limit`,
`changed`, `uncovered` and `tests`; each listed test's `why` and `evidence` are review's to
judge:

```json
{"supplement_limit": <the lane's cap, {{supplement_limit}} test runs — verification's own default applies when the file names none>,
 "changed": ["<each behavior, component and interface the change altered, one line each>"],
 "uncovered": {"<repo>": ["<each changed path that repo's --light report leaves covered by no `carries:` declaration>"]},
 "tests": [{"id": "a1", "repo": "<name under repos/>", "file": "<the test's repo-relative file>",
            "name": "<its test name, when one test is meant>", "cwd": "<directory inside the repo>",
            "argv": ["<the run that shows it passing>"], "marker": "<a line only its passing run prints>",
            "why": "<what of the change it pins>",
            "evidence": "<how you found it: the selector, route, copy key or test name you searched, and where it led>"}]}
```

Find the tests that already pin what you changed — the e2e suites through the selectors, routes
and copy keys the diff touches (a shared file the engine hands to at most 3 suites is the blind
spot this file closes), the unit tests through the packages and names — and list every one you
find: run each where you found it and list only a test that passes at HEAD. Take `uncovered`
from the engine's own `--light` report of each touched repo
(`"$PF_PYTHON" "$PF_SHARED_DIR/scripts/verify-scope" --light --json`, or the report the
fast-checks command logged): its `e2e_selection.uncovered_paths`, else its
`--light: carries: changed path …` diagnostics. `review` judges the analysis for obviously
missing tests.

## If you cannot finish

Never hand off `done` on a build that is not green: a real blocker — the machine, not a choice —
is `blocked` with the reason, the evidence and what a person must do in your hand-off; a choice
that is the owner's is a `pf step ask` (your skill).

{{owner_review}}

## Principles

Pick the simplest mechanism; a unit's test pins the criterion's behaviour, not your code's shape.

{{principles}}
