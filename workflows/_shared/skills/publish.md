---
name: publish
description: Push the verified branch, open or update its PR and write the delivery note - after checking the verified result file (develop's verification, micro's evidence; or the owner's waiver of that gate, quoted in handoff.md) and the review's pass; never merge, never re-run a test. As the lane's last step (its merge skipped) it records the PRs as published with one pre-written script and registers that record for the wrap.
commands: [published.py]
---
# publish

You publish a change the lane verified: push its feature branch, open or update the PR,
say what it is. When the lane's merge step runs after you, it merges the PR with a
pre-written command; when the opening of `artifacts/handoff.md` notes you as the last step
(the steps after you `skipped`), nobody in the lane merges it — the PR awaits a human
review (step 5). Either way you never merge anything, merge nothing into the branch and run
no test. "The verifying
gate" below is develop's `verification` (result file `artifacts/verify/result.json`),
micro's `evidence` (`artifacts/run/result.json`); "the step that makes the change" is
develop's `build`, micro's `fix`.

## 开工前读什么、核对什么 — before you start: what you read and check

1. **The verified result file.** Per task repo, its `heads` entry equals
   `git -C repos/<name> rev-parse HEAD` — always, waived or not. Its `verdict` is `pass`; or
   it is `fail` and the verifying gate's latest section in `artifacts/handoff.md` is stamped
   `waive` **in the same round** as the result file's `round` (an earlier round's waiver
   never covers a later red), and the owner message that header quotes for `--per-owner`
   really accepts that red — "ship it", "放行", "merge anyway" do; a question, a fix request
   or an "ok but …" does not.
2. **The review passed.** Review's latest section is stamped `done`, and
   `artifacts/review.md` gives the verdict pass. Review is never waived.
3. **What the PR body needs.** `artifacts/sync/result.json`; `artifacts/review.md` (its
   non-blocking findings); on develop `artifacts/plan.md` (`## Acceptance`, `## Coverage`);
   the latest section of the step that makes the change (its 给下游与未决, for an
   `owner-review:` line).

A check of 1 or 2 that does not hold is this gate's FAIL: hand off `fail`, the body naming
the source and what did not hold (the lane goes back to the step that makes the change).

## Procedure

1. **Per task repo** (`repos/<name>/`; a capsule with none publishes nothing and writes
   `{"repos": []}`): `git fetch origin`; `<default>` is
   `git symbolic-ref --short refs/remotes/origin/HEAD` less `origin/` (else `main`).
   `git rev-list --count origin/<default>..HEAD` of 0: record `skipped_reason`, nothing to
   publish. Else: `git status --porcelain --untracked-files=all` is empty, no tag points at
   a commit of this work item (a hit is a `## Follow-up` point, not a stop). The branch is
   `polyforge/<capsule directory name>` (`basename "$PWD"`). **Push, idempotent:**
   `git ls-remote origin refs/heads/<branch>` absent, or an ancestor of HEAD:
   `git push origin HEAD:refs/heads/<branch>` — an explicit refspec, `HEAD` as its source,
   the remote `origin`; anything else moved the branch outside the lane: hand off `blocked`
   naming both shas, never force. **The PR, idempotent:** write the body to
   `artifacts/publish-pr-<name>.md`, then `gh pr list --head <branch> --state all --json
   number,url,state`: an OPEN one — `gh pr edit <url> --body-file artifacts/publish-pr-<name>.md`;
   a MERGED one whose branch carries new commits since — exactly one follow-up PR; none —
   `gh pr create --base <default> --head <branch> --title "<slug>: <one line>" --body-file
   artifacts/publish-pr-<name>.md`. No GitHub origin, or no `gh`: hand off `blocked`.
2. **The PR body**, in this order: the slug and a change summary; when the verifying gate
   was waived, first the line that the change's verification (micro: its evidence) was red
   and waived (the owner's words verbatim, as the header quotes them) and that the
   integration lane's full gate is its next bar; the sync record from
   `artifacts/sync/result.json` — per repo the default branch's commit merged in
   (`default_head`), the files resolved mechanically and, verbatim as the owner's to read
   since no review judged them, "conflict resolved in sync: <files>" from `resolved[]`; the
   review's verdict; the verified result (its `verdict` and, on develop, each check of
   `details.checks[]` with every `flaky` or `pre_existing` unit; on micro, each run of
   `details.runs[]`: repo, line, exit code); on develop, the judged acceptance list (the
   ticket's, else the plan's `## Acceptance`) and the plan's `## Coverage` table — micro has
   neither, its acceptance is the ticket's goal; a **`## Follow-up`** section listing every
   non-blocking finding of `artifacts/review.md` and every pre-flight hit (or "none") — the
   owner decides which become tickets, no step files them; and the line "The lane's merge
   step merges this PR — do not merge it by hand." — or, when the opening notes you as the
   last step (step 5), the line "This lane does not merge this PR — it awaits a human
   review."
3. **Owner review.** An `owner-review: <paths> — <description>` line in `artifacts/plan.md`
   or in the latest hand-off of the step that makes the change holds the publication: once
   the PRs are open, `pf step ask` the owner once — per repo the PR link, each line verbatim
   and whether each path exists — then end your turn. 「过目通过」: continue. 「不通过: <what to
   change>」: hand off `fail --per-owner <the reply's at>`, the instruction verbatim and the PR
   links in the body (the owner answered, so it spends no rewind). Anything else: ask again.
   The material is read-only — never edit, regenerate or commit it. No such line anywhere:
   ask nothing; the note says "no owner-review declared".
4. **Write** `artifacts/publish.json` — `{"repos": [{name, branch, pr_url, head,
   skipped_reason}]}`, `head` the sha you pushed (what the merge step matches) — and
   `artifacts/delivery.md` (the PR body's content plus the owner-review record).
5. **The last step.** Only when the opening's step order notes you as the last step (the
   steps after you `skipped`; develop's merge is skipped per project, micro's never is):
   after 4, run exactly this line (no argument added or removed, no Bash timeout):
   `"$PF_PYTHON" "$PF_SHARED_DIR/scripts/published.py" --publish artifacts/publish.json --verified artifacts/verify/result.json --record-out artifacts/publish/integration.json --json --out artifacts/publish/record.json`
   — it checks every published head against the verified one, touches no forge, and on
   exit 0 writes the published record `artifacts/publish/integration.json`:
   `{state: published, repos: [{repo, pr, head, light_verified_head}]}`.
   - **0** — register it for the wrap, exactly this line:
     `pf step hub wrap-attrs integration @artifacts/publish/integration.json` — the work
     item is wrapped with it as its `integration` attr (`pf step handoff --status done` is
     refused until the line was accepted; the integration lane never collects a published
     item). Then, as the lane's last step, write the wrap summary (the step header's Memory
     section), then hand off `done`.
   - **1** — a published head is not the verified one: hand off `fail`, the result's
     `summary` in the body.
   - **2** — not judged (a file unreadable, a head missing): hand off `blocked`, the
     result's `summary` in the body and as `--reason`.

## 交接要写什么 — what your hand-off carries

- **Status.** `done` once every repo with commits is pushed and its PR open (and the owner
  passed an owner-review; as the last step, once the `wrap-attrs` line was accepted); `fail` when a check before you started did not hold, or on the
  owner's 「不通过」 (`--per-owner`); `blocked` on a push identity, an origin or a `gh` that
  is not there, or a branch moved outside the lane.
- **回应上一轮** (a later round): what failed the earlier publication (a check, or the
  owner's 「不通过」), and what is different now.
- **做了什么**: per repo the branch, the PR link and the head pushed (or why skipped).
- **关键决定和理由**: the waiver you relied on (the header that quotes it), each owner
  message you acted on (its `at`, the words, your reading).
- **给下游与未决**: the `## Follow-up` points for the owner.
- **Evidence**: `artifacts/publish.json`, `artifacts/delivery.md`; as the last step
  `artifacts/publish/record.json` and `artifacts/publish/integration.json`.

## Never

`gh pr merge`, a merge through `gh api` or `curl`, `git merge`, `git pull`, `git rebase`,
`git commit`, `git cherry-pick`, `git reset` or `git am` — you push exactly the head the
verified result names; `--force`, `-f`, `--force-with-lease`, `--all`, `--mirror`,
`--tags`, `--delete`, a `+` refspec, `--no-verify`, `-c core.hooksPath=…`, a push to the
default branch or without a refspec; writing or editing `artifacts/publish/record.json` or
`artifacts/publish/integration.json`; any build, lint or test command; a publication while
a check before you started does not hold; writing `runtime-state/asks.jsonl`; ending a turn
that needs the owner without `pf step ask`; a command run in the background and polled, or
given a timeout or deadline of your own.
