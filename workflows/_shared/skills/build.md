---
name: build
description: Build the change test first and record each unit's red-then-green run in artifacts/build/units.json - the run review replays red at the test commit and verification runs green again - asking the owner only in the four cases, right after the plan.
commands: [fast_checks.py, facts.py]
---
# build

No daemon records your runs: the record is `artifacts/build/units.json`, and the steps after
you check it — `review` replays each unit's run on the tree of its test commit
(`facts.py build --replay`: it must be red there) and `verification` runs it again on the
final tree (`verify.py --unit-runs`: it must be green). Your job: read the ticket and what
came back to you, ask only when a case below holds, build each unit red then green, and
record exactly what ran.

## 开工前读什么、核对什么 — before you start: what you read and check

1. **The ticket's acceptance.** Read the opening of `artifacts/handoff.md` and
   `runtime-state/work-item.json`. Its acceptance section (`## Acceptance` or `## 验收`) is
   the judged list. No such section, or an incomplete one (a criterion nothing could
   check, a bug ticket with no criterion on the reported failure, an integration fix
   ticket naming no red check) is case (d) below.
2. **A round a rewind opened.** When `artifacts/handoff.md` carries an earlier `build`
   section, this is a later round: read the 回退 section that sent the lane back here and
   the section of the step that FAILed — its 结论 and 发现的问题和风险, and its evidence
   files (`artifacts/review.md`; `artifacts/verify/result.json` and
   `artifacts/verify/report.md`; publish's quoted owner words) — and every owner message
   quoted in those headers. Keep the plan, `units.json` and the plan's `## Coverage`
   cumulative; every point raised gets an answer in your 回应上一轮.
3. **Your own earlier work.** `artifacts/plan.md`, `artifacts/build/units.json`,
   `artifacts/build/change-analysis.json` and
   `artifacts/self-review.md` as your earlier round left them; `artifacts/grill-qa.md` when
   it is there.

## Procedure

1. **A round a FAIL opened changes the tree — or units.json — or says why not.** Commit
   what the FAIL demands before you hand off; when it names a defect of a unit's record (a
   `<repo>:unit-run:<id>` check that missed its marker or ran the wrong argv or cwd),
   rewriting that unit in `artifacts/build/units.json` is the change. When you find nothing
   to change — a red you cannot reproduce, a finding you believe wrong — you may hand off
   on the same tree only with, per point, the evidence for that reading in 回应上一轮:
   `review` reads the commits the daemon stamps on your section and judges the reason. When
   the point is the owner's to settle, `pf step ask` instead.
2. **Ask only in these cases** — run `pf step ask "<question>"`, then end your turn; the
   answer is your next turn. Never at the end of your work; without one of these you do
   not ask:
   (a) the code reveals something the ticket did not decide — a dead end, a ticket that
   contradicts the code or itself (a defect you cannot reproduce: the evidence tried and
   the options — a clue, a separate diagnosis, or cancel);
   (b) the plan diverges from the ticket — what diverges and why;
   (c) the ticket says `confirm_plan: true` — right after `artifacts/plan.md` is written and
   before the first test, with the acceptance list and the units;
   (d) no acceptance section, or an incomplete one — once, right after the plan, the list
   you propose in the question.
   Each question names its case and your recommended answer. The owner replies in free
   text ("按你的建议来", "ok but drop AC3", a list): that reply is the decision, whatever its
   words. Act on it and fold what it changes into the plan's `## Acceptance` /
   `## Non-goals`. A plan the owner replied to is the judged list — never judge against a
   list only you wrote. A reply that calls the work off: hand off `cancel` citing it.
3. **Each unit red, then green, then recorded.** Commit the unit's test files alone
   (`test_commit`) and run its targeted command on that clean commit yourself: it fails on
   an assertion. The targeted command runs only that unit's test, filtered by the test
   runner's own name filter (`go test -run`, `pytest -k`, or the suite's own scenario
   whitelist); a suite that offers no filter is named in the hand-off, and the whole suite
   runs once, in the scoped command, and nowhere else. Implement until it is green, then
   commit the implementation (`impl_commit`): the tree is the one the green run saw (a bare
   commit changes no tree), so the command is not run again on it. Never amend (`git
   commit --amend`) or otherwise rewrite a commit you have made — no rebase, no reset past
   it: the daemon backs every commit up to aihub and cannot back up a rewritten history
   over it; a fix is a new commit. The red run just made is the build's own red evidence —
   beside it no manual worktree replay is made (the facts engine's replay is the record),
   and a command's result already in hand for its key — the same command text, the same
   working-tree hash of the root it runs in, the same declared prerequisites — is not
   re-run: a differently spelled command proves nothing new on it. A flaky failure is re-run only in
   the suite that failed, once, by the written fast-check command itself — the same rules
   verification runs — never by hand: the check already contains that one re-run, and a
   failure still red after it is red. A red-state run must exit non-zero within its limit
   — a unit self-reports `timeout` in units.json, else the default — and a run its limit
   kills is an evidence problem, never a pass: the waiting inside it is bounded and an
   assertion failure exits at once. A long command writes its output to a log file and records
   its exit code (`> log 2>&1; echo exit=$?`), and the log is searched after the run; a long
   command is never piped straight into grep, head or tail — the pipe eats the failure
   detail and the exit code. Then add the unit to `artifacts/build/units.json` — a JSON
   array, one object per unit in commit order, cumulative across rounds:

   ```json
   [{"id": "u1", "test_commit": "<full sha>", "impl_commit": "<full sha>",
     "run": {"repo": "<name under repos/>", "cwd": "<directory inside the repo, or .>",
             "argv": ["go", "test", "./internal/x/", "-run", "^TestAnswer$", "-count=1"]},
     "marker": "^--- PASS: TestAnswer "}]
   ```

   `run` is exactly what ran red and green — the repo, the directory you ran it from and
   the argv word by word (a pipeline or `&&` chain is `["bash", "-c", "<line>"]`). `marker`
   is required: a Python regular expression (`re.ASCII`, searched in each output line) one
   output line matches only when that test passed — never a line
   the red run also prints, never one a filter that matched no test prints (`ok  ` alone is
   not a marker: `go test -run NoSuchTest` prints it), never one the empty string matches
   (`.*`, `^`, `a*`: verification fails such a unit without running it).
   A unit may self-report `timeout` (a Go duration) — the limit its recorded run and the
   facts engine's red-state replay are bounded by (the default is 10m): a red run that
   needs longer raises it, and one that hangs is an evidence problem, never a pass.
   Both commit ids are the full output of `git -C repos/<repo> rev-parse <ref>`, taken right
   after each commit is made and saved where the unit is recorded — never hand-copied from a
   transcript or a log (`test_commit` and `impl_commit` each resolve their own commit the
   moment it is made; a `pre_existing_red` unit reports only `impl_commit`, taken the same way).
   A unit whose reproduction test already exists and is red before the change — an
   integration fix ticket: the test came with the batch, only the fix is this change —
   is not rewritten: it declares `"pre_existing_red": true` and reports no
   `test_commit`; its `impl_commit`, `run` and `marker` are the ordinary ones. Run its
   targeted command yourself at the head: green — its red evidence is the facts engine's
   replay on the tree of the change's base, and no manual worktree replay is made. The
   facts engine replays its run on the tree of that base and takes its
   red evidence from there; a run that is green there means the test was not already
   red — fix the record or the fix.
   A unit is not recorded until the written check has passed it: right after adding it to
   `artifacts/build/units.json`, run
   `"$PF_PYTHON" "$PF_SHARED_DIR/scripts/fast_checks.py" --check-units artifacts/build/units.json --unit <id> --json --out artifacts/build/units-check.json`
   — it re-runs the unit's recorded run as written and checks its marker against that run's
   output by `verify.py`'s own rules (`pfstep/unitruns`: the one implementation, never a second
   copy). A red names the unit, its marker and the closest output line: fix the record (the
   marker, or the recorded run) and re-check; at the close (3c) run the same command over the
   whole file with no `--unit`. A red unit declared `pre_existing_red` is re-run once by the
   check, and a pass records it flaky; a unit this lane just wrote is never re-run there — a
   test just written must be stable, so red it stays.
4. **Coverage stays in the plan.** `artifacts/plan.md`'s `## Coverage` is the table review
   and publish read — one row per criterion of the judged list, every id once:
   `| AC | kind | ref |`, kind `test` (ref `<repo>/<test file>: <test name>`), `existing`
   (the test already pinning it) or `doc` (the file and the validator that shows it).
   Keep it current with every round.
   A round that wrote the plan adds its `## 分析对照` section beside `## Units` — its own `##`
   heading, never inside it (the plan-conformance scan reads `## Units` up to the next `##`
   heading): on a ticket whose content carries a `## 分析` section, each item the analysis
   lists — each old test and document obligation, each risk, each decided point (`已定`) —
   mapped to the unit that carries it, or the stated reason it is not taken; a decided point
   takes precedence over the analysis's recommendation; an analysis written against a base that
   differs from the current one has its file:line references re-checked against the current
   base before the work starts; a ticket without `## 分析` writes `none` there. A round that
   keeps an inherited plan adds nothing to it.
5. **The close's checks.** The step's close (3c) first runs the build close's scoped selection once
   per touched repo — for a work item whose verification runs the light tier (an integration
   batch's fix ticket runs the full bar and closes without one) — `"$PF_PYTHON"
   "$PF_SHARED_DIR/scripts/fast_checks.py" --repo <name> --close-scoped --reuse
   artifacts/build/fast-checks/<repo>.json --json --out artifacts/build/scoped-close/<repo>.json`,
   after that repo's fast checks (a green run in the reuse file whose key — the command text,
   the root's subtree tree hash, the declared prerequisites — matches is evidence, not a
   re-run): the same light-tier selection verification plans, one shared implementation, over the
   final tree. A red or `incomplete` result never hands off `done` without the owner's ruling —
   fix the change and re-run it, a full-tier fallback being the owner's to settle, a residual the
   owner accepted keeping its record in the result file, cited in the hand-off — and a green
   result's `details.selected` merges into the change analysis's `selected`, keyed by the repo,
   verbatim. Then the step's close runs the facts lines review judges with, the same
   engine and the same replay —
   `"$PF_PYTHON" "$PF_SHARED_DIR/scripts/facts.py" plan-conformance --json --out artifacts/facts/plan-conformance.json`
   (a plan the lane inherited skips this line and runs only the
   replay) and
   `"$PF_PYTHON" "$PF_SHARED_DIR/scripts/facts.py" build --replay --isolation host --json --out artifacts/facts/build.json`
   and `"$PF_PYTHON" "$PF_SHARED_DIR/scripts/facts.py" carries --json --out artifacts/facts/carries.json`
   — the second each unit's commit facts (its ids resolving in a task repo, test-first shape)
   with its run replayed red on the tree of its test commit, the first the plan's two-way
   conformance with the diff, the third the e2e carries. A red is fixed before the hand-off,
   never handed off on. Exit 2 on the first two lines is not judged: read the result's
   summary, wait one minute and run that line again, at most twice; still 2 — its failure
   summary goes into the hand-off and the lane goes on to review, which re-runs the line
   itself (never `blocked` for it); exit 2 on `carries` is no pass either (fix the input it
   names; a real machine blocker is `blocked`).
   They are the build's own runs — review recomputes them fresh, so no file here is review's
   evidence. Then the change analysis's own facts
   line, `facts.py analysis` — the same shared validator `verification` calls; its red names
   the entry and the rule it violates, and the close check must be green before the hand-off.

## 交接要写什么 — what your hand-off carries

- **Status.** `done` only on a green build: every unit red then green, the fast checks
  green, the close's scoped selection green (its flaky units, if any, named as the fast
  checks name theirs), the close's facts checks (plan-conformance, build with the replay, carries)
  green, an exit 2 that stayed 2 after its retries disclosed in the hand-off with its summary,
  the close analysis check green,
  the self-review written; a fast check that turned green through a flaky re-run has its
  flaky units (with their first-red logs) listed in Status. A real blocker — the machine, not a choice — is
  `blocked` with `--reason`. A choice that is the owner's is a `pf step ask`, not a
  hand-off. `cancel` only on the owner's word (`--per-owner`).
- **回应上一轮** (a later round): each point of the FAIL or the owner's words, numbered as
  there — the commit or the units.json entry that answers it, or the evidence for leaving
  it as it was.
- **做了什么**: the plan's units, each with its `test_commit`, `impl_commit` and who built
  it (`main` or `subagent`, and why a big plan's unit is `main`); the fast checks run.
- **关键决定和理由**: every decision you asked the owner for — the message's `at`, the
  owner's words verbatim and how you read them (`review` checks your reading against the
  words the daemon quotes in your header); every change to a unit's test after its
  `test_commit`, with the finding that required it (`r<n>#<k>`); the memories you used.
- **验证**: each unit's red and green run (exit codes), the fast checks, `artifacts/self-review.md`.
- **给下游与未决**: an `owner-review:` line when one is declared; what `review` should look
  at first.
- **Evidence**: `artifacts/plan.md`, `artifacts/build/units.json`,
  `artifacts/build/change-analysis.json`, `artifacts/build/units-check.json`,
  `artifacts/build/fast-checks/<repo>.json`, `artifacts/build/scoped-close/<repo>.json`,
  `artifacts/facts/plan-conformance.json`, `artifacts/facts/build.json`,
  `artifacts/facts/carries.json`, `artifacts/facts/analysis.json`,
  `artifacts/self-review.md` (and `artifacts/grill-qa.md` when there is one).

## Never

Report a run you did not make, or a unit whose `run` is not the command that ran red and
green; edit a unit's test after its `test_commit` to make it pass (when a finding shows the
test itself is wrong, change it in a commit whose message cites the finding, `r<n>#<k>`, and
say so in your hand-off); end a turn that needs the owner without `pf step ask`; hand off a
round a FAIL opened on an unchanged tree with an unchanged units.json and no reason per
point; run a unit's command in the background and poll it — each run is a foreground call
that ends with its exit code.
