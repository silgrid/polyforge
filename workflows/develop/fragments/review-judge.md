1. Read, in this order: the acceptance criteria — the work item's acceptance section
   (`## Acceptance` or `## 验收`, quoted in the opening of `artifacts/handoff.md`), or,
   when it had none, the list in `artifacts/plan.md` `## Acceptance` the owner's
   recorded reply answered; `artifacts/plan.md` (units, coverage);
   `artifacts/self-review.md`; build's latest section of `artifacts/handoff.md`; the
   facts your skill writes under `artifacts/facts/`; and the full diff, tests
   included — in each task repo `git diff $(git merge-base HEAD origin/HEAD)..HEAD`
   (fall back to `origin/main` or `origin/master` when `origin/HEAD` is unset). The
   machine gate (`verification`) runs after you: judge the diff itself.
2. **Coverage, criterion by criterion (`ac_gap`).** The plan's `## Coverage` names,
   per criterion id, a unit's test — unit-level or an e2e scenario — (kind `test`),
   an existing test (kind `existing`) or a doc validator (kind `doc`). Read each named
   test's source. A `test` entry must exercise that criterion's behaviour and fail
   without the change. An `existing` entry passes without the change by definition:
   it must already assert exactly the behaviour the criterion names, on the path the
   diff leaves in place. A `doc` entry's validator must show the criterion holds. A
   criterion with no entry, a vacuous or wrong test, an `existing` test that does not
   really cover it, or a criterion the diff does not meet is a blocking `ac_gap`
   finding carrying its `ac_id`. So is a user-visible criterion whose plan names
   neither an e2e scenario nor why a unit test suffices, and an owner reply (the
   owner messages the daemon quotes in build's section headers) that the plan's
   `## Acceptance` / `## Non-goals` or the diff ignores or contradicts. **Owner
   replies (`guarantee`).** A deviation from the ticket, or an acceptance list
   `build` wrote itself (a ticket without an acceptance section, or an incomplete
   one), needs an owner reply on record — quoted, stamped, in build's section
   headers — and build's 关键决定和理由 must state the decision and its reading
   consistently with the owner's raw words. A deviation or self-written list with no
   reply, a reading the words do not support (an "ok but drop AC3" read as a plain
   yes), or — on a ticket carrying `confirm_plan: true` — a first unit `test_commit`
   older than the owner's reply to the plan, is a blocking `guarantee` finding: the
   judged list and every deviation are the owner's.
3. **Tests after their red commit (`test_weakened`).** `artifacts/facts/build.json`
   (`facts.py build --replay`) gives per unit its commits' test-first shape
   (`commits`), its run replayed on the tree of its `test_commit` (`replay`: red is
   the evidence) and every hunk of `git diff <test_commit>..HEAD -- <its test files>`
   (`test_weakened[]`). A unit whose commits are not test first, or whose replay
   exited 0 — a test that passes on the old code, never red or weakened since — is
   blocking `test_weakened`. A unit whose `replay` carries `not_runnable` has no red
   evidence (its run could not run there: a build, setup, compile or import error, a
   missing module) — neither red nor a pass: blocking `test_weakened`, naming the unit and
   its `rule`, never made up for by reading the code. So is a deleted or relaxed assertion, a new skip, a
   changed `carries:` header, a changed helper or fixture the test relies on, or an
   input changed so the assertion holds trivially, with no finding cited for it in
   build's 关键决定和理由.
4. **e2e quality (`test_weakened`).** Read every e2e suite the diff adds or changes.
   Blocking `test_weakened`: a scenario synchronised by a fixed delay instead of
   polling the condition it awaits (a bounded poll deadline is fine); one that asserts
   or measures before the awaited state has settled (a column width read right after
   a resize, while a ResizeObserver callback still recomputes it); a `carries:` header
   that is missing or does not name the paths the suite exercises, on a suite the
   verification-scope engine discovers (an npm, python or rust root's default
   discovery or its `suite_glob=`; never a Go `test/` file) —
   `artifacts/facts/carries.json` (`facts.py carries`) lists each suite the build
   added or changed whose header is missing, malformed or matches no path it touched.
5. **The plan against the diff, both directions.** `artifacts/facts/plan-conformance.json`
   (`facts.py plan-conformance`) lists a changed file no unit declares (`undeclared`)
   and a declared coupled obligation left unchanged (`unchanged_obligations`): facts,
   blocking only inside a class — an obligation the repo's `AGENTS.md` names is
   `project_rule`, an undeclared change that removes or weakens a guarantee is
   `guarantee`; anything else is recorded non-blocking.
