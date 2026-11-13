---
name: build
description: Build the change test first and record each unit's red-then-green run in artifacts/build/units.json - the run review replays red at the test commit and verification runs green again - asking the owner only in the four cases, right after the plan.
commands: []
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
3. **Your own earlier work.** `artifacts/plan.md`, `artifacts/build/units.json` and
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
   an assertion. Implement, commit (`impl_commit`), run the same command from the same
   directory: exit 0. Never amend (`git commit --amend`) or otherwise rewrite a commit you
   have made — no rebase, no reset past it: the daemon backs every commit up to aihub and
   cannot back up a rewritten history over it; a fix is a new commit. To check an earlier
   commit's red again, check it out in a separate directory (`git worktree add <dir> <sha>`,
   then `git worktree remove <dir>`), never by switching your working directory to that
   commit. Then add the unit to `artifacts/build/units.json` — a JSON array, one
   object per unit in commit order, cumulative across rounds:

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
4. **Coverage stays in the plan.** `artifacts/plan.md`'s `## Coverage` is the table review
   and publish read — one row per criterion of the judged list, every id once:
   `| AC | kind | ref |`, kind `test` (ref `<repo>/<test file>: <test name>`), `existing`
   (the test already pinning it) or `doc` (the file and the validator that shows it).
   Keep it current with every round.

## 交接要写什么 — what your hand-off carries

- **Status.** `done` only on a green build: every unit red then green, the fast checks
  green, the self-review written. A real blocker — the machine, not a choice — is
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
- **Evidence**: `artifacts/plan.md`, `artifacts/build/units.json`, `artifacts/self-review.md`
  (and `artifacts/grill-qa.md` when there is one).

## Never

Report a run you did not make, or a unit whose `run` is not the command that ran red and
green; edit a unit's test after its `test_commit` to make it pass (when a finding shows the
test itself is wrong, change it in a commit whose message cites the finding, `r<n>#<k>`, and
say so in your hand-off); end a turn that needs the owner without `pf step ask`; hand off a
round a FAIL opened on an unchanged tree with an unchanged units.json and no reason per
point; run a unit's command in the background and poll it — each run is a foreground call
that ends with its exit code.
