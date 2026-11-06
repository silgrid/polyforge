---
name: review
description: Judge the build in a fresh context on the facts three pre-written facts.py lines compute - the plan against the diff, each unit's commits test first with its red run replayed at its test commit, the e2e carries - and the owner's words against build's reading of them; on a later round, check the previous round's findings and whether the build changed anything.
commands: [facts.py]
---
# review

You read and judge; three pre-written commands compute the facts you judge with. A result
file's `verdict` is its exit code's (0: nothing found, 1: something to class, 2: not
judged), never yours, and never the gate's verdict by itself: what a fact means is the step
definition's judgment.

## 开工前读什么、核对什么 — before you start: what you read and check

1. **What you judge against.** The opening of `artifacts/handoff.md` (the acceptance list
   verbatim) and `runtime-state/work-item.json`; build's latest section — its body, and in
   its header the owner messages build received (verbatim, stamped) and each task repo's
   start → end commit; `artifacts/plan.md` (`## Acceptance`, `## Units`, `## Coverage`);
   `artifacts/build/units.json`; `artifacts/self-review.md`; `artifacts/grill-qa.md` when
   it is there.
2. **build's hand-off holds.** `artifacts/plan.md` and `artifacts/build/units.json` exist and
   the units file parses; build's 关键决定和理由 records every decision it asked the owner
   for, with the owner's words. A hand-off that does not hold is a blocking `guarantee`
   finding naming what is missing.
3. **A later round: did the build change anything?** When `artifacts/handoff.md` carries an
   earlier `review` section, its header stamps the heads that round judged (each repo's end
   commit), and `artifacts/review.md` — as that round left it; read it before you rewrite
   it — carries its `units.json sha256:` line and its numbered findings. When every task
   repo's `git -C repos/<name> rev-parse HEAD` equals the head that round judged and
   `sha256sum artifacts/build/units.json` equals that line, the build since changed
   nothing (the daemon does not stop on it — you judge it). Then do not run the facts again:
   read build's 回应上一轮. A point it answers with evidence that the finding was wrong —
   judge that evidence: a finding you now agree was wrong is withdrawn, with the reason in
   `artifacts/review.md`. A point it leaves unanswered, or answers with no evidence, stands
   as a residual: hand off `fail` naming each one and "the build changed nothing". The
   rewind budget bounds such loops; when the command refuses the `fail` because the budget
   is spent, `pf step ask` the owner.

## Procedure

1. **Run these lines as written**, from the capsule root — no argument added or removed, no
   Bash timeout:
   `"$PF_PYTHON" "$PF_SHARED_DIR/scripts/facts.py" plan-conformance --json --out artifacts/facts/plan-conformance.json`
   `"$PF_PYTHON" "$PF_SHARED_DIR/scripts/facts.py" build --replay --isolation host --json --out artifacts/facts/build.json`
   `"$PF_PYTHON" "$PF_SHARED_DIR/scripts/facts.py" carries --json --out artifacts/facts/carries.json`
   The replay runs on the host (`--isolation host`), under the step's command PATH. Exit 2 is not judged: read the
   result's `summary` (for `build`, each unit's `replay_error`) — docker, a worktree, the
   deadline: wait one minute and run that line again, at most twice; still 2 — hand off
   `blocked`, the summary and the log's tail in the body.
2. **Class the facts** (the judgment's items name each class):
   - `build.json`: a unit that does not resolve, whose `commits` names a test-first problem,
     or whose `replay` is not red (it exited 0 on the tree of its `test_commit`: the test
     passes on the code before its implementation) is blocking `test_weakened`, naming the
     unit; read every `test_weakened[]` hunk against build's 关键决定和理由. A unit whose
     `replay` carries `not_runnable` has no red evidence: its run could not run on the tree
     of its `test_commit` (a build, setup, compile or import error, a missing module or
     package — `[setup failed]`, `ERR_MODULE_NOT_FOUND` — the matched line and its `rule`
     are in it), so it is neither red nor a pass. It is blocking `test_weakened`, naming the
     unit and its `rule`; reading the code by hand never stands in for the missing red. The
     fix is build's: a test that runs on the replay tree (a project declares the preparation
     such a tree needs as `base_setup=` in `.polyforge/verify-scope.conf`, and the replay
     runs it). It is the result's exit 1, a fact to class, never the exit 2 retried above.
   - `carries.json`: each `problems` entry is blocking `test_weakened`.
   - `plan-conformance.json`: `undeclared` and `unchanged_obligations` entries are facts,
     blocking only inside a class.
   - The owner's words: the messages quoted in build's section headers (and
     `runtime-state/asks.jsonl`, each with its `at`). On a ticket carrying
     `confirm_plan: true`, the owner's reply to the plan precedes the first unit's
     `test_commit` (`git -C repos/<repo> log -1 --format=%cI <test_commit>`); every decision
     build's 关键决定和理由 records reads the owner's own words faithfully; every way the
     plan diverges from the ticket was asked. Any miss is blocking `guarantee`.
3. **Write `artifacts/review.md`**: the verdict; the line `units.json sha256: <digest>`
   (`sha256sum artifacts/build/units.json`); every finding numbered (`#1`, `#2`, …) with its
   severity, its class on a blocking one (and the criterion id of an `ac_gap`) and its
   detail; the documents the doc-impact check compared (the step's **Stale docs.** item);
   on a fail, the synthesis build's next round works from. Every round rewrites it.

## A later round: the two faces and the round contract

You always run fresh: nothing of an earlier round's conversation is yours. What you
inherit is that round's findings (its `artifacts/review.md`, read before you rewrite it,
and its section). Run two faces over the current tree:

- **Checklist face** — each finding of the latest earlier round: fixed, or still present.
  A **blocking** finding still present is a **residual**: report it blocking, named by its
  place (`r<round>#<k>`) **without re-deriving it** — the same finding, still open — with
  its original class. A non-blocking one still present stays non-blocking.
- **Fresh face** — the definition's full judgment, from your fresh context. The fix surface
  is each task repo's first-parent, non-merge commits since the head the latest earlier
  round judged: `git -C repos/<name> log --first-parent --no-merges -p <head>..HEAD`. Never
  read it off `git diff <head>..HEAD`: a sync may have merged the default branch since, and
  none of what it brought in is the fix.

From the second round on, a blocking finding is one of: a residual; a new defect on the fix
surface; a `bug` anywhere in the change; a `project_rule` violation anywhere. Every other
hit is non-blocking. An owner message quoted in a header that settles a finding (rules it
moot, or accepts it) is not re-raised: cite it.

## 交接要写什么 — what your hand-off carries

- **Status.** `done` when there is no blocking finding (the gate passes); `fail` when there
  is at least one (the lane goes back to the build step); `blocked` when a facts line stayed
  at exit 2 after its retries.
- **结论**: pass or fail, and the count of blocking and non-blocking findings.
- **回应上一轮** (a later round): each finding of the earlier round, `r<n>#<k>` — fixed, a
  residual, or withdrawn with its reason; whether the build changed anything (item 3 of
  开工前读什么、核对什么).
- **发现的问题和风险**: on a fail, each blocking finding — its number, class (and criterion
  id), what is wrong and where — concrete enough that the build's next round does not have
  to guess; the non-blocking ones stay in `artifacts/review.md`, which publish lists for the
  owner.
- **验证**: each facts file and its verdict; the `units.json sha256:` you judged.
- **Evidence**: `artifacts/review.md`, `artifacts/facts/plan-conformance.json`,
  `artifacts/facts/build.json`, `artifacts/facts/carries.json`.

## Never

Write or edit a file under `artifacts/facts/` or `runtime-state/asks.jsonl`; run any other
build, lint or test command, or replay a unit by hand; hand off `done` over a blocking fact
you did not class away with its reason in `review.md`; end a turn that needs the owner
without `pf step ask`; run a command in the background and poll it, or give it a timeout or
deadline of your own.
