---
name: review
description: Fresh-context judgment of a micro change - re-run the bounds judgment on fix's evidence plan (facts.py micro-bounds), then the diff against the ticket, no behavior change, green evidence - before delivery.
---

# review

Judge whether this micro change is ready to deliver. This lane has no spec, no plan and
no verification step: the ticket body is the specification, and after you `sync` merges
the default branch in, `evidence` re-runs `fix`'s evidence plan on the host, from each
repository's root, `publish` opens the PR and `merge` merges it. Your pass is the
lane's only independent judgment. This step has no skill: this definition is the whole
procedure.

## 开工前读什么、核对什么 — before you start: what you read and check

1. **What you judge against.** The opening of `artifacts/handoff.md` and the ticket
   (`runtime-state/work-item.json`, its goal and content); fix's latest section — its body
   (what changed where, the evidence it ran, its bounds verdict, its reading of any owner
   reply) and, in its header, the owner messages fix received (verbatim) and each task
   repo's start → end commit.
2. **fix's bounds, judged again.** Run this line yourself, as written (no argument added or
   removed, no Bash timeout):
   `"$PF_PYTHON" "$PF_SHARED_DIR/scripts/facts.py" micro-bounds --plan artifacts/fix/evidence.json --json --out artifacts/review/micro-bounds.json`
   Its `verdict` is the exit code's alone — 0 in bounds, 1 out of bounds (its
   `details.reason` names every refusal), 2 not judged (run it once more; still 2: hand off
   `blocked` with its `summary`). Then compare with fix's own result,
   `artifacts/facts/micro-bounds.json`: its `verdict` is `pass` and every repo's `heads`
   entry equals that repo's HEAD. Out of bounds is blocking: one finding per refused path,
   naming it. So is a command your result lists for a repo (`details.repos[].commands`:
   the fast phase, then every declared `micro_check`) that the plan omits — the `evidence`
   step runs only the plan — and a fix result that is not this tree's or that your run
   contradicts.
3. **A later round.** When `artifacts/handoff.md` carries an earlier `review` section, read
   it and `artifacts/review.md` as that round left it (before you rewrite it): each of its
   blocking findings is fixed on the current tree, or it is still blocking — reported again
   by its place (`r<round>#<k>`), not re-derived. When every task repo's HEAD still equals
   the head that round judged (its header's end commit), fix changed nothing: judge the
   reason its 回应上一轮 gives for each finding, and a finding it does not answer stands.

## What to judge

Read the whole diff, in each task repo `git diff $(git merge-base HEAD origin/HEAD)..HEAD`
(falling back to `origin/main` or `origin/master` when `origin/HEAD` is unset), including
any commit fix did not report. Pass only when ALL hold, and fail with one finding per
violation otherwise, naming the path and why:

- the change is in bounds and the plan carries every command its repos owe (item 2 above);
- the diff is confined to the text change the ticket asks for — nothing it did not ask
  for, nothing it asked for missing, except what the owner narrowed: an owner reply quoted
  in fix's section header that approved a narrower change than the ticket's (to stay
  inside the declaration) makes that narrower change the one to judge — what it drops is
  not missing;
- no product runtime behavior, code, configuration, generated file or test changed — a
  runtime branch, a config value or an assertion is never text, even inside a declared
  path;
- fix's body says its micro evidence ran green.

Record every finding in `artifacts/review.md`, numbered, with its severity and path; on this
lane every blocking finding is `fix`'s to repair in its next round, and the file's fail
synthesis is concrete enough that it does not have to guess.

**Do not run the build, the evidence or tests.** The micro-bounds line is the one command
you run; the `evidence` step re-runs the plan on the synced HEAD.

Judge what the machine cannot — whether the text is the ticket's and whether anything in
it changes behavior. The daemon stamps the heads you judged on your section, and `sync`
starts from exactly that tree, so any commit after your verdict is caught there. This
review always runs **fresh**.

{{review_security}}

## 交接要写什么 — what your hand-off carries

- **Status.** `done` with zero blocking findings; `fail` with at least one (the lane goes
  back to `fix`); `blocked` when the micro-bounds line stayed at exit 2.
- **结论**: pass or fail, and the count of blocking and non-blocking findings.
- **回应上一轮** (a later round): each earlier finding — fixed or still blocking.
- **发现的问题和风险**: each blocking finding — its number, the path and what is wrong —
  for fix's next round; the non-blocking ones stay in `artifacts/review.md` for publish.
- **验证**: your micro-bounds verdict against fix's.
- **Evidence**: `artifacts/review.md`, `artifacts/review/micro-bounds.json`.

## Principles

Every principle is a check (a violation is blocking).

{{principles}}
