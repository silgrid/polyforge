---
name: build
description: The develop lane's one build session - plan against the work item's acceptance criteria (a fresh-context analysis sub-agent reproduces a reported defect and writes the plan), then per unit (a unit-level test or an e2e scenario) a test committed red before the implementation that turns it green, each unit's run recorded in artifacts/build/units.json (implementer sub-agents build a big plan's units), fast checks over the whole change, and a self-review against review's five blocking classes. It asks the owner only on a surprise, right after the plan, never at the end of its work.
---

# build

One session builds the whole change (3a plan, 3b units test-first, 3c close, 3d self-review); `review`
reads it next, fresh. Your skill is what you read before you start, how you ask the owner, how a round a
FAIL opened must change the tree, how each unit's run is recorded in `artifacts/build/units.json`, and
what your hand-off carries. On a later round (a 回退 in `artifacts/handoff.md` named this step) keep the
plan, answer every point raised, and keep `units.json` and the plan's `## Coverage` cumulative.

{{recall}}

## Sub-agents

{{build_subagents}}

## 3a. Plan

Item 2 below, and reproducing and locating a reported defect, are the analysis sub-agent's
(`## Sub-agents`): its instruction is that item in the capsule's `workflows/_rendered/build.md`,
and folding an owner's answer into the plan is its too. You read the work item (1), ask the
owner only in the cases item 3 names, and judge the plan; without sub-agents, do it all.

1. Read the work item (`runtime-state/work-item.json`). It was settled before it was filed: its
   background carries the decisions, and its acceptance section (`## Acceptance` or `## 验收`) is the
   judged list — each list item one criterion, its id the item's leading `AC<n>` token, else `AC<n>` by
   1-based position. Do not grill it again: no clarity checklist, no batched round of questions; what
   the ticket, the code and recalled memory settle, you decide and record.

2. Write `artifacts/plan.md`: `## Acceptance` (the judged list, its ids — the ticket's verbatim; for a
   ticket without one, the list you propose); `## Non-goals` (what the ticket or an answer leaves out,
   or `none`); `## Units` — ordered compilable units, each with its id, the criterion ids its test
   carries, its test (a unit-level test, or an e2e scenario in a new or extended suite and the
   `carries:` globs that suite declares), its fast-check commands, its test-quality record (the
   behaviour its test observes, how its expected values are derived independently of the implementation,
   the change it catches — the bar below), `Touched files: <path> (write|read), ...` and `Coupled
   obligations:` (what must change with each write — the repo's `AGENTS.md` names its known couplings —
   or `none` and why); `## Coverage` — per criterion `test` (its unit's test), `existing` (the test
   already pinning it) or `doc` (the file and its validator) — the table your skill gives — and for a
   user-visible criterion its e2e scenario or one line why a unit test suffices. The plan carries a
   `## 分析对照` section beside `## Units` — its own `##` heading, never inside it (the plan-conformance
   scan reads `## Units` up to the next `##` heading): on a ticket whose content carries a `## 分析`
   section, each item the analysis lists — each old test and document obligation, each risk, each
   decided point (`已定`) — is mapped to the unit that carries it, or the stated reason it is not
   taken; a decided point takes precedence over the analysis's recommendation; an analysis written
   against a base that differs from the current one has its file:line references re-checked against
   the current base before the work starts; a ticket without `## 分析` writes `none` there.
   The plan maps the ticket's decided rulings too: on a work item whose content carries
   sections opening `## 已定`, it carries a `## 已定对照` section in which every item of
   every such section is mapped to the unit that carries it, or carries `none — <reason>`;
   a ticket without such a section omits the mapping. The close and review judge it by the
   same `facts.py plan-conformance` line: an unmapped ruling is red, named with its number
   and text.

3. **When to ask the owner**: only in the four cases your skill names — (a) a dead end or a
   contradiction, (b) a deviation, (c) `confirm_plan: true`, (d) a missing or incomplete acceptance list
   — right after the plan, never at the end of your work, with `pf step ask`. Its question carries
   {{batch_questions}}
{{test_quality}}

## 3b. Units, test first

Who builds each unit is `## Sub-agents`. An implementer's instruction is this section's per-unit text
in that file (its fast-check placeholders: `workflows/_shared/fast_checks.md`, `fast_checks_build.md`).

Per unit, in plan order — an e2e scenario is a unit like any other: (1) write its test, by the project's
convention; every suite file opens with a `carries:` header (`// carries: <glob>, ...`, `#` where
comments start with `#`); in a suite you add, or whose run a unit records, its globs, relative to the
suite's project root (where its `.polyforge/verify-scope.conf` is), name the unit's written `Touched
files` — the verification-scope engine selects suites by it. An e2e scenario polls the condition it
asserts (the element, the text, the settled value) — never a fixed delay instead of polling; a bounded
poll deadline is fine — and measures only once the state has settled: a value a later callback
recomputes (a width a ResizeObserver sets after a resize) is read when it stops changing. The test is
red on an assertion when run targeted (a test needing a symbol that does not exist yet gets the
signature with a zero-value body in an ordinary commit first, so the red is never a compile error); (2)
commit the test files alone (T_i, `test_commit`) and run the unit's targeted command on that clean
commit: red. The targeted command runs only that unit's test, filtered by the test runner's own name
filter (`go test -run`, `pytest -k`, or the suite's own scenario whitelist); a suite that offers no
filter is named in the hand-off, and the whole suite runs once, in the scoped command, and nowhere else.
(3) implement until it is green, then run the fast checks:
{{fast_checks}}
{{fast_checks_build}}
(4) commit the implementation (C_i, `impl_commit`) — the tree is the one the green run of (3) saw, and a
bare commit changes no tree, so the command is not run again on it; (5) record the unit in
`artifacts/build/units.json` as your skill says — the run exactly as it ran red and green, and its
marker. A unit is not recorded until the written check has passed it: `"$PF_PYTHON"
"$PF_SHARED_DIR/scripts/fast_checks.py" --check-units artifacts/build/units.json --unit <id> --json
--out artifacts/build/units-check.json` right after adding it (verification's own rules: a red names
the unit, its marker and the closest output line — fix the record and re-check). A red unit declared
`pre_existing_red` is re-run once by the check, and a pass records it flaky; a unit this lane just
wrote is never re-run there — a test just written must be stable, so red it stays. A unit's run is
bounded by its self-reported `timeout` in units.json, else the default (10m).

Four rules hold over the whole session, the close included, each command alike. A command's result
already in hand for its key — the same command text, the same working-tree hash of the root it
runs in, the same declared prerequisites — is not re-run: a bare commit changes no tree, and a
differently spelled command proves nothing new on it. The test-first red run is the build's own red
evidence — beside it no manual worktree replay is made, the facts engine's replay is the record. A
flaky failure is re-run only in the suite that failed, once, by the written fast-check command
itself — the same rules verification runs — never by hand: the check already contains that one
re-run, and a failure still red after it is red. A red-state run must exit non-zero within its
limit — a unit self-reports `timeout` in units.json, else the default — and a run its limit kills
is an evidence problem, never a pass: the waiting inside it is bounded and an assertion failure
exits at once. A long command writes its output to a log file and records its exit code
(`> log 2>&1; echo exit=$?`), and the log is searched after the run; a long command is never piped
straight into grep, head or tail — the pipe eats the failure detail and the exit code.

A defect ticket's first unit is the reproduction test, red on the reported failure before any fix;
cannot reproduce it → 3a's case (a). A reproduction test that already exists and is red before the
change is not rewritten: the unit declares `pre_existing_red` and reports no `test_commit` — your skill
says how its red evidence is taken (the facts engine replays it on the tree of the change's base). A
doc-type criterion gets no red test and no unit: commit the change, run its validator and name it in its
`## Coverage` row.

## 3c. Close and 3d. Self-review

After the last commit, run the fast checks over the whole change (the written command once per touched repo) and every unit test targeted, then the written unit check over the whole
`artifacts/build/units.json` (the same command with no `--unit`) (the full bar is `verification`'s).
Then, last, run the fast-check proof line once per touched repo — the same fast check with `--phase fast`, the final write of `artifacts/build/fast-checks/<repo>.json`, so each result's
`heads` is the HEAD the hand-off gate compares against (the tree is unchanged since the whole-change run, so every green command is reused):
`"$PF_PYTHON" "$PF_SHARED_DIR/scripts/fast_checks.py" --repo <repo> --phase fast --json --out artifacts/build/fast-checks/<repo>.json`
The daemon reads that file for every repo build changed and refuses `done` until each is a
`pass` at HEAD. A red the change did not cause the run itself triages on the change's baseline
(the same judgment verification runs): one the base carries the same way is `pre_existing` —
not this change's, the check passes; what is left is the owner's (`pf step ask`, and on the
release hand off `done` with `--per-owner <the reply's at>`; the stamp records it).
Then the build close's scoped selection — the same
light-tier selection verification plans, one shared implementation, over the final tree, group by group (each
group's own commands in plan order: a command's group is the group its report bound it to else its report's root,
the groups side by side at most `--parallel N` at once, default 2, one group or `--parallel 1` the serial plan) —
run in the repository's declared verification image when its HEAD carries `.polyforge/Dockerfile.verify` (the same isolation verification runs — one container for the engine asks, the selected commands, the re-runs and the
baseline's base worktree, the image and tool caches it shares — while a repo without the declaration keeps the host
close exactly as before, the per-unit runs always on the host), the result file recording which isolation ran (`details.isolation`, `host`/`container`) — for a work item whose verification runs the light tier (an integration batch's fix ticket
runs the full bar and closes without one): `"$PF_PYTHON"
"$PF_SHARED_DIR/scripts/fast_checks.py" --repo <name> --close-scoped --unit-runs artifacts/build/units.json --reuse
artifacts/build/fast-checks/<repo>.json --json --out artifacts/build/scoped-close/<repo>.json` once per touched
repo, after its fast checks (a green run in the reuse file whose key — the command text, the
root's subtree tree hash, the declared prerequisites — matches is evidence, not a re-run — on the host alone: a boxed
close consumes no `--reuse` green evidence, the image and tool caches stay shared and an unusable file is still exit 2);
the existing tests it selects are the ones verification will select, a red one getting the one targeted
re-run the per-unit bar gives and the same baseline triage (`--unit-runs` protecting this change's own test commits) — all
of it in each group's own container when one was declared (one container per group: a container's reap kills every
process in it after a command): a container the close cannot build, start or keep is the run's error
(exit 2, never a red of the change's, never a host stand-in). A red or `incomplete` result (the engine answered full, or a selected command cannot run) never hands off `done` without the owner's ruling —
with one exception — a `pass`-verdict result whose selection status is `pre_existing` (every
entry of its `reds[]` pre-existing on the `details.baseline` the run names): not this change's,
handed off `done` citing the result file, the shared validator's green-equivalent evidence.
Any other red or `incomplete`: fix the change and re-run it — a real environment blocker is `blocked`, a full-tier fallback is the owner's to settle (`pf step ask`), a residual keeping its record in the result file, cited in the hand-off. Merge each touched repo's `details.selected` of its green or pre-existing result into the change
analysis's `selected` (below), keyed by the repo, verbatim. Then run the close's facts lines —
review's own first, the same engine with the same replay it judges you with:
`"$PF_PYTHON" "$PF_SHARED_DIR/scripts/facts.py" plan-conformance --json --out artifacts/facts/plan-conformance.json`
`"$PF_PYTHON" "$PF_SHARED_DIR/scripts/facts.py" build --replay --isolation host --json --out artifacts/facts/build.json`
The second gives each unit's commit facts (its ids resolving in a task repo, `test_commit` one ordinary commit of test paths alone and an
ancestor of `impl_commit`, `impl_commit` in HEAD) and its run replayed on the tree of its `test_commit` (or of the change's base for a unit declared `pre_existing_red`) — it must be red
there; the first the plan's two-way conformance with the diff and its `## 已定对照` mapping
(an unmapped decided ruling red, named). A red — the plan's undeclared
writes or lapsed obligations, a unit that does not resolve, whose commits name a test-first
problem or whose replay is not red — is fixed before the hand-off, never handed off on. Exit 2
on either line is not judged: read the result's summary, wait one minute and run that line
again, at most twice; still 2 — its failure summary goes into your hand-off and the lane goes
on to `review`, which re-runs the line itself (never `blocked` for it). Then
`"$PF_PYTHON" "$PF_SHARED_DIR/scripts/facts.py" carries --json --out artifacts/facts/carries.json` — the e2e
carries; a red names the repo, suite and reason — fix it and re-run until green, never handing
off on one; exit 2 is no pass either (fix the input it names; a real machine blocker is
`blocked`). These are the build's own runs — `review` recomputes every line fresh, so no file
here is review's evidence. Read your full diff (per repo `git
diff $(git merge-base HEAD origin/<base>)..HEAD`, `<base>` the step header's base branch) as `review`
will, against its five blocking classes: `bug`, `ac_gap`, `project_rule`, `test_weakened`, `guarantee`;
write each hit and its disposition to `artifacts/self-review.md` and fix what you can now (a new unit
for a behaviour fix). Then review's doc-impact check, item by item: each document that describes what
the change altered (the repository's README, its reference docs, upgrade notes, skill documents, the
coupled obligations its `AGENTS.md` names) compared with the diff, and `artifacts/self-review.md` lists
each document compared, its conclusion, and synced or no-change-needed with the reason.

Write the change analysis — `artifacts/build/change-analysis.json`, the structured file verification
supplements the engine's own selection with (`verify.py --analysis`, add-only, never narrowing
it), in this shape. Its keys are the machine's contract — `supplement_limit`, `changed`,
`uncovered`, `tests` and `selected` (the close-scoped run's machine selection record, merged verbatim);
each listed test's `why` and `evidence` are review's to judge:

```json
{"supplement_limit": <the lane's cap, {{supplement_limit}} test runs — verification's own default applies when the file names none>,
 "changed": ["<each behavior, component and interface the change altered, one line each>"],
 "uncovered": {"<repo>": ["<each changed path that repo's --light report leaves covered by no `carries:` declaration>"]},
 "tests": [{"id": "a1", "repo": "<name under repos/>", "file": "<the test's repo-relative file>",
            "name": "<its test name, when one test is meant — a bare name as it appears in the file, or a qualified name whose segments, joined by `.`, `::` or `/`, each appear in the file>", "cwd": "<directory inside the repo>",
            "argv": ["<the run that shows it passing>"], "marker": "<a line only its passing run prints>",
            "why": "<what of the change it pins>",
            "evidence": "<how you found it: the selector, route, copy key or test name you searched, and where it led>"}],
 "selected": {"<repo>": {"head": "<sha>", "base": "<sha>", "default_branch": "<branch>", "engine": "<the engine invocations>",
              "conf": "<the conf's path>", "mode": "light", "paths": ["<the report's affected paths>"], "suites": ["<its e2e suites>"],
              "commands": [{"name": "<name>", "root": "<repos/<repo>[/<nested root>]>", "cmd": "<the command>", "phase": "scoped",
                            "source": "conf|default", "exit_code": 0, "log": "<its log>", "reused": "<result file>#<name>"}],
              "status": "green"}}}
```

Find the tests that already pin what you changed — the e2e suites through the selectors, routes and copy
keys the diff touches (a shared file the engine hands to at most 3 suites is the blind spot this file
closes), the unit tests through the packages and names — and list every one you find: run each where you
found it and list only a test that passes at HEAD. Take `uncovered` from the engine's own `--light`
report of each touched repo (`"$PF_PYTHON" "$PF_SHARED_DIR/scripts/verify-scope" --light --json`, or the
close-scoped run's record): its `e2e_selection.uncovered_paths`, else its `--light: carries:
changed path …` diagnostics. `review` judges the analysis for obviously missing tests.

Then run the change analysis's close check — the facts engine's own fact, the same validator
`verification` calls (`verify.py --analysis`: one implementation, never a second copy): `"$PF_PYTHON"
"$PF_SHARED_DIR/scripts/facts.py" analysis --json --out artifacts/facts/analysis.json` It judges every
listed entry the way verification will — statically (its repo, its run's shape, its file at HEAD, its
name inside it) and, for the entries its supplement cap takes, by running each distinct run once at HEAD
and matching its marker; over the cap the tail is cut and named. A red names the entry and the rule it
violates: fix it and re-run until green — never hand off with this check red.

## If you cannot finish

Never hand off `done` on a build that is not green: a real blocker — the machine, not a choice — is
`blocked` with the reason, the evidence and what a person must do in your hand-off; a choice that is
the owner's is a `pf step ask` (your skill).

{{owner_review}}

## Principles

Pick the simplest mechanism; a unit's test pins the criterion's behaviour, not your code's shape.

{{principles}}
