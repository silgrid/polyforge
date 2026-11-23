1. Read, in this order: the acceptance criteria — the work item's acceptance section
   (`## Acceptance` or `## 验收`, quoted in the opening of `artifacts/handoff.md`), or,
   when it had none, the list in `artifacts/plan.md` `## Acceptance` the owner's
   recorded reply answered; `artifacts/plan.md` (units, coverage);
   `artifacts/build/change-analysis.json` (the change analysis);
   `artifacts/self-review.md`; build's latest section of `artifacts/handoff.md`; the
   facts your skill writes under `artifacts/facts/`; and the full diff, tests
   included — in each task repo `git diff $(git merge-base HEAD origin/<base>)..HEAD`,
   `<base>` the step header's base branch (a fixed target branch, else the branch
   `origin/HEAD` names, falling back to `origin/main` or `origin/master` when it is
   unset). The machine gate (`verification`) runs after you: judge the diff itself.
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
   in the hand-off gate's subset above: non-blocking when the gate's proof stood,
   blocking `guarantee` when the gate was bypassed. A unit declared
   `pre_existing_red` (its reproduction
   test already exists and is red before the change) has no `test_commit`: its
   replay runs on the tree of the change's base, and red there is its red
   evidence; a replay that exited 0 there (the test was green before the
   change) is in the same subset — the unit has no red evidence. A
   unit whose `replay` carries `not_runnable` has no red
   evidence (its run could not run there: a build, setup, compile or import error, a
   missing module) — neither red nor a pass: in the same subset, naming the unit and
   its `rule`, never made up for by reading the code. So does a unit whose `replay` was
   killed at its own limit (`limit_kill`): its red-state run did not exit non-zero within
   its limit, so no red evidence — the fix is build's (a bounded wait, an assertion that
   fails at once, or the unit self-reports a higher `timeout` in units.json), and the
   item is in the same subset. So is a deleted or relaxed assertion, a new skip, a
   changed `carries:` header, a changed helper or fixture the test relies on, or an
   input changed so the assertion holds trivially, with no finding cited for it in
   build's 关键决定和理由.
   The shared test-quality bar (`workflows/_shared/test_quality.md`, included in this
   step's definition) is the same standard: blocking `test_weakened` too — a unit whose
   plan entry carries no test-quality record (the behaviour its test observes, how its
   expected values are derived independently of the implementation, the change it
   catches); a test that only asserts that some source
   text exists (unless the acceptance is itself that text or document); a unit whose
   replayed red fails for another reason than its record states — the red must be the
   recorded one, not a missing symbol's or a typo's; and a test, of any kind, that breaks
   the bar's settle duty (an asynchronously settling state asserted on one read right
   after the action, or a race fixed by a relaxed or "at least" assertion).
**The hand-off gate's subset.** The deterministic items the build's `done` gate already
judges (`facts_proof`, polyforge#241) are not, on their own, a blocking finding here: a
unit's test-first commits or its replay not red at the test commit, a skip or a changed
`carries:` header with no cited finding, an e2e suite without a matching `carries:`, a
listed analysis test that does not exist, an existing test changed outside the plan's
write set, an undeclared write, an unchanged coupled obligation, an unmapped decided
ruling. Recompute them — the gate only reads build's file, and a fresh recomputation is the
only way to catch a bypass — record each hit as non-blocking and name it "the hand-off gate
should have refused this"; it fails the review only beside a hit of another class. A hit
the gate's `facts_proof` result did not carry at the judged HEAD is not that: the gate was
bypassed or its result forged, which is blocking `guarantee`, naming the item. The semantic
items around this subset — a relaxed assertion, a weakened helper or input, a fixed-delay
e2e, a missing test-quality record, a document left stale — stay as classed above and block
on their own.

4. **e2e quality (`test_weakened`).** Read every e2e suite the diff adds or changes.
   Blocking `test_weakened` on their own: a scenario synchronised by a fixed delay instead of
   polling the condition it awaits (a bounded poll deadline is fine); one that asserts
   or measures before the awaited state has settled (a column width read right after
   a resize, while a ResizeObserver callback still recomputes it). A `carries:` header
   that is missing or does not name the paths the suite exercises, on a suite the
   verification-scope engine discovers (an npm, python or rust root's default
   discovery or its `suite_glob=`; never a Go `test/` file —
   `artifacts/facts/carries.json`, `facts.py carries`, lists each suite the build
   added or changed whose header is missing, malformed or matches no path it touched),
   is in the hand-off gate's subset above.
5. **The plan against the diff, both directions.** `artifacts/facts/plan-conformance.json`
   (`facts.py plan-conformance`) lists a changed file no unit declares (`undeclared`)
   and a declared coupled obligation left unchanged (`unchanged_obligations`): facts,
   blocking only inside a class — an obligation the repo's `AGENTS.md` names is
   `project_rule`, an undeclared change that removes or weakens a guarantee is
   `guarantee`; anything else is recorded non-blocking.

6. **The change analysis (`test_weakened`)** — read `artifacts/build/change-analysis.json`
   (build's close wrote it): the analysis is review's to judge for obviously missing tests —
   a changed behavior, component or interface it names (or the diff shows) that an existing
   test already pins, one the analysis does not list, is a blocking `test_weakened` finding:
   find them the way build should have (the selectors, routes, copy keys and test names the
   diff touches; the engine's `--light` report and its `carries:` declarations). So is an
   `uncovered` list that does not match that report, or a listed test that does not exist
   (its file or name) — each finding names the missing or wrong entry.
   `artifacts/facts/analysis.json` (`facts.py analysis`) names each entry that fails the
   close check (the same shared validator this gate's machine twin calls): a listed test
   that does not exist, cannot run as written, or whose run does not show its marker is in
   the hand-off gate's subset above — non-blocking when the gate's proof stood,
   blocking `guarantee` when the gate was bypassed — naming the entry. The analysis's `selected` section —
   the machine selection the build close's scoped run records, the same selection this
   gate's verification twin plans — is part of the same judgment, read with the
   close-scoped results that wrote it (`artifacts/build/scoped-close/<repo>.json`): a
   touched repo with no record and no result explaining its absence (an `incomplete`
   close-scoped, a red the owner ruled a residual, or a red every entry of whose `reds[]`
   the run records `pre_existing` — each cited in the hand-off; a work
   item whose verification runs the full bar (an integration batch's fix ticket) closes
   without a close-scoped run and owes no record), a record
   whose head is not the judged head, or a selected command without its green
   evidence (a green run, a reuse source, or — on a `pre_existing` record — the skip-form
   confirmation or completion proof that kept it) is in the hand-off gate's subset above,
   naming the repo (the gate's `analysis` check judges the same record); a repo with no
   record at all stays blocking `test_weakened`, the gate not judging its absence. The machine selection never removes the manual
   duty above: it lists what the engine selected, not every test that matters.
