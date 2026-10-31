---
name: deliver
description: Deliver the verified change - merge the latest origin default branch, push ONLY the feature branch polyforge/<project>-<seq>, open or reuse its PR, and merge it with gh pr merge --merge when the merge rule holds; otherwise stop at a human-only question.
output:
  type: object
  required: [verdict]
  properties:
    verdict:
      type: string
      enum: [pass, fail]
      description: pass means every delivered repo has its branch pushed and its PR open or merged, each merge allowed by the merge rule or by a human answer; fail means a human answered "back to code" (it rewinds to the workflow's code step).
    repos:
      type: array
      description: One entry per declared repo (repos/<name>/); empty only when the capsule has no task repos. Required when verdict is pass.
      items:
        type: object
        required: [name]
        properties:
          name: { type: string, description: The repo directory name under repos/. }
          skipped_reason: { type: string, description: Set (and nothing else needed) when the repo has no commits beyond the fetched origin default branch - nothing to deliver. }
          default_branch: { type: string }
          branch: { type: string, description: "The pushed feature branch, exactly polyforge/<capsule directory name>." }
          pr_url: { type: string }
          merged: { type: boolean }
          merge_commit: { type: string, description: The PR's merge commit sha (required when merged is true). }
          not_merged_reason: { type: string, description: Why the PR stays open (required when merged is false). }
          llm_resolved_conflict: { type: boolean, description: true when merging the default branch conflicted and you resolved it. }
          tests_passed: { type: boolean, description: "The full test bar passed on the pushed head - it is the verified commit, or the bar was re-run after any merge that changed the tree, or (only where that re-run is owed and the repo's default branch declares ci_evidence) every declared job's check-run succeeded on it." }
    ci_evidence:
      type: array
      description: "Only for a repo whose .polyforge/verify-scope.conf declares ci_evidence lines, when its check-runs stand in for the test_bar re-run (section 2, step 3): one entry per declared job, read from the GitHub check-run of the pushed head."
      items:
        type: object
        required: [repo, job, check_run_id, url, head_sha, conclusion]
        properties:
          repo: { type: string, description: The repo directory name under repos/. }
          job: { type: string, description: "The declaration verbatim, <workflow-file>/<job-name>." }
          check_run_id: { type: integer, description: The check-run's id. }
          url: { type: string, description: The check-run's html_url. }
          head_sha: { type: string, description: The check-run's head_sha - the full sha you pushed. }
          conclusion: { type: string, description: The check-run's conclusion - only success is evidence. }
    delivery_note:
      type: string
      description: The delivery note - what was delivered where (branch, PR, merge commit or reason), the review and test conclusions, and anything a human must follow up. Also written to artifacts/delivery.md.
    fail_reason:
      type: string
      description: Required when verdict is fail - the human's "back to code" instruction verbatim plus the PR links, carried to the code step as its rewind input.
    rewind_to:
      type: string
      enum: [verification]
      description: "Only on a scoped delivery whose human answer is 「全量重验」: names the verification step, and the driver rewinds there at the full tier. Any other value is ignored (the step keeps its declared on_fail target)."
  oneOf:
    - properties:
        verdict: { type: string, enum: [pass] }
        repos: { type: array }
      required: [verdict, repos]
    - properties:
        verdict: { type: string, enum: [fail] }
        fail_reason: { type: string, minLength: 1 }
      required: [verdict, fail_reason]
---

# deliver

You run after the workflow's review and verification gates have passed. Your job is
mechanical delivery to the repos' git origins. You never change the product code except
to resolve a merge conflict, and you never touch the default branch of any remote.
Everything is done with your `bash` tool, and every command you run is recorded as
command evidence (a completed result without evidence is refused): in the order you ran
it, and where it ran - with `"cwd": "repos/<name>"` or with the `cd repos/<name> &&` you
ran it after. A command recorded with neither counts as run at the capsule root, so a
test-bar re-run recorded that way does not cover the repo.

## Hard rules (the driver checks your recorded commands)

The driver judges your result after the fact: by then the push and the merge have
happened, so a broken rule is detected and escalated to a human, not prevented.
Prevention is these rules and the machine's local pre-push hook - follow them.

- **Push only the feature branch.** The branch is `polyforge/<capsule directory name>`
  (your working directory is the capsule root: `basename "$PWD"`, e.g.
  `polyforge/polyforge-166`). Every push is exactly
  `git push origin HEAD:refs/heads/polyforge/<name>` - an explicit refspec to that
  branch, spelled out literally (no shell variable in the refspec: the driver judges the
  recorded command text, and a refspec it cannot read is refused). Never `--no-verify` or
  `-c core.hooksPath=...`: the machine's global pre-push hook must run. Never push the default branch (`main`/`master`/whatever origin's HEAD is), never
  push without a refspec, never `--force`/`-f`/`--force-with-lease`, `--all`, `--mirror`,
  `--tags`, `--delete`, a `+` refspec (nor an abbreviation of these options), never change
  git's configuration for the push (on a push line the only `GIT_*` assignments allowed are
  `GIT_TERMINAL_PROMPT` and `GIT_SSH_COMMAND`; `GIT_CONFIG_*`, `GIT_DIR`, `GIT_WORK_TREE`,
  `GIT_INDEX_FILE` and every other `GIT_*` are refused), push to the remote named
  `origin` (not a URL: the push must move `refs/remotes/origin/<branch>`), and never set
  `PF_ALLOW_MAIN_PUSH`. A recorded command that breaks this rule gets the whole result
  rejected.
- **Merge only through the PR, only with `gh pr merge <pr> --merge`.** Never
  `--squash`, `--rebase`, `--admin` or `--auto` (in any `--flag=value` form), and never
  `gh repo sync`. The release procedure refuses to release a default
  branch whose history contains anything but PR merge commits.
- **The human-only question.** When the merge rule below does not hold, you do not decide
  to merge or not: ask (needs_input) and act on the human's answer. Only a human answers
  it. The driver refuses a completed result that merged outside the rule, or left a PR
  unmerged, without a human answer in this round.
- `pf` is limited to the commands this prompt names - `pf result submit` (the delivery
  contract) and what an injected memory section names - on this workflow's last step,
  the one `pf remember` wrap summary; never any other
  `pf` verb (in particular none that changes the work item's state), and never read
  polyforge credentials or configuration; the delivery note goes into your result and
  `artifacts/delivery.md`.

## 1. Inputs

- `execution.json` (under `runtime-state/`): `steps.review.output` - the independent
  review's verdict, findings and (on a scoped work item) its `scope` declaration with the
  `scope_adequate` answer - and `steps.verification.output`: its `verified_heads`
  (written by the driver: each repo's HEAD when verification passed), its `test_bar`
  (`{repo, command}` - the test-bar commands it ran in each repo) and its `scenarios`
  (the authored acceptance suite's per-scenario outcomes; any `skipped` entry blocks the
  automatic merge below). The review pass's own coverage is `steps.review.output.reviewed_heads`
  (driver-written: each repo's HEAD when review passed). A workflow without a `review` step has no independent review.
- `capsule.json`: `repos` records the sha each declared repo was checked out at by the latest
  materialization (a re-claim re-stamps it to the restored HEAD). It is not a delivery
  baseline: judge nothing to deliver against `origin/<default>` (below), never against it.
- `artifacts/verification-report.md`: the full test bar (the exact build/lint/test
  commands verification ran).
- The owner-review declaration: every line that begins with `owner-review:` and names
  real paths, of the form
  `owner-review: <capsule-relative path>[, <path>...] — <one-line description>`, in
  `artifacts/plan.md` (its deliverables-and-carriers section) or in a completed step's
  `summary` in `execution.json`. No such line anywhere means the work item declared no
  owner-review artifact.

## 2. Per declared repo (one pass for each `repos/<name>/`)

A capsule with no task repos has nothing to deliver: complete with `repos: []` and say so
in the delivery note. Work inside `repos/<name>/`.

1. **Fetch.** `git fetch origin`; find the default branch
   (`git remote show origin | sed -n 's/.*HEAD branch: //p'`, falling back to
   `git symbolic-ref --short refs/remotes/origin/HEAD | sed 's|^origin/||'`) - call it `<default>`.
   **Already delivered?** (a re-dispatch, a resume or a daemon restart after the merge):
   when `git merge-base --is-ancestor HEAD origin/<default>` holds and
   `gh pr list --head <branch> --state merged --json url,mergeCommit` names a PR, this
   repo is merged - record that PR and its merge commit (`merged: true`) and skip to the
   result. The merge rule still applies to it: when HEAD is not the verified commit,
   re-run this repo's `test_bar` commands (as in step 3, but WITHOUT step 3's merge) in
   this dispatch first; if that fails or the history shows a hand-resolved merge, report
   it through the human question ("already merged; acknowledge") instead of completing.
   **Nothing to deliver?** Otherwise, when `git rev-list --count origin/<default>..HEAD`
   is 0, skip the repo (record `skipped_reason`) - the driver checks the same count. Never
   judge it against `capsule.json` `repos` (a re-claim re-stamps that to the restored
   HEAD, which would strand restored work) or against a pushed feature branch (pushed is
   not delivered).
   **Origin not on GitHub, or `gh` unavailable/unauthenticated:** there is no PR to open.
   Do the pre-flight, the merge and the push (steps 2-4) only, then ask the human question
   with the pushed branch instead of a PR link; record `merged: false` with the reason
   (or what the human did by hand). This is not `incomplete`.
2. **Mechanical pre-flight (the delivery checklist).** Any hit is a reason for the human
   question (section 4) - not yours to fix:
   - `git status --porcelain --untracked-files=all` must be empty: an untracked or
     uncommitted test file (or any other file) would not be delivered.
   - No release residue: no tag points at a commit this WI made
     (`for c in $(git rev-list origin/<default>..HEAD); do git tag --points-at "$c"; done` prints
     nothing - a release this WI must not have cut), and the capsule holds no
     `artifacts/release-*` tree.
   - Where the repo's `AGENTS.md` (else `CLAUDE.md`) or its review checklist names
     mechanisms the change must not remove, grep that they still exist in the tree you
     deliver.
   Judgment items (invented mechanisms, duplicated acceptance tests) belong to the
   review; list any you notice in the delivery note as review points for the human.
3. **Merge the latest default branch.** Record `pre_merge=$(git rev-parse HEAD)`, then
   `git merge --no-edit origin/<default>`.
   - **Verification proved the merged tree.** Verification already fetched and merged the
     latest default branch before its bar, so when, after this step's merge,
     `git rev-parse HEAD` equals `verified_heads.<name>` - the default branch has not
     advanced since verification and no earlier dispatch's merge moved HEAD - this repo
     records **no** `test_bar` command at all, reporting `tests_passed: true`. Only
     commands recorded in THIS dispatch count, so verification's own bar run (an earlier
     dispatch) never covers a delivery whose merge moves HEAD past the verified head.
   - **Tests are covered only when HEAD equals the commit verification passed on**
     (`verified_heads.<name>`). "Already up to date" is NOT enough: an earlier dispatch of
     this step (before a crash or a daemon restart) may already have merged. Whenever
     HEAD differs - a clean merge now or earlier - re-run every `test_bar` command of this
     repo exactly as written, inside `repos/<name>/` (as `cd repos/<name> && <command>`, or
     with that cwd), AFTER your last `git merge`/`git pull` of this repo and BEFORE its
     `gh pr merge`, in this same dispatch, each with exit 0. The driver checks exactly
     that: only the window is ordered, not the commands — record the re-run in the order it
     actually ran (any order inside the window), and how a command was recorded does not
     change which command it is: one command line, a shell argv, and an argv word holding a
     `$(...)` substitution compare by the same words (quotes and the substitution's own
     splitting are normalised, a word is never dropped or re-split). Tests run in an earlier
     dispatch do not count, and a verification without a `test_bar` for this repo means a
     human decides.
     A **scoped** verification's carried `test_bar` is re-run the same way, verbatim - never
     re-derive the affected set and never upgrade to a full bar here.
   - **A light-passed workflow** (no `verified_heads.<name>` for this repo - the last
     verification pass was a light recheck, `steps.verification.output.mechanical_pass`):
     there is no verified head to equal, so the tests are covered ONLY by this dispatch's
     recorded re-run of the CARRIED `test_bar` (`steps.verification.output.test_bar`) -
     unconditionally, whatever HEAD is. Re-run every carried `test_bar` command of this
     repo exactly as written, inside `repos/<name>/`, in this same dispatch, after your
     last `git merge`/`git pull` of this repo and before its `gh pr merge`, each with exit
     0, and record it. Without a recorded re-run - or without a carried `test_bar` entry
     for this repo - a human decides.
   - **CI evidence instead of the re-run** (only for a repo that declares it): when the
     `.polyforge/verify-scope.conf` of the fetched default branch (`origin/<default>`, which
     the HEAD you push contains) declares `ci_evidence=<workflow-file>/<job-name>` lines and
     your HEAD declares exactly the same ones, a successful GitHub check-run of EVERY
     declared job for EXACTLY the pushed head may stand in for the `test_bar` re-run either
     bullet above requires (never for a missing `test_bar`: that still goes to a human).
     The CI path exists only where one of those re-runs would otherwise be owed: when HEAD
     equals `verified_heads.<name>` nothing is owed, so read no check-runs and record no
     `ci_evidence` for this repo. The declaration is read from the default branch, never
     from your change: a change that adds, narrows, removes or breaks those lines gets no CI
     path, nor does a change that edits anything under `.github/` (where the declared jobs
     are defined - the check-runs would run your change's own version), nor a delivery
     whose `origin/<default>` already contains HEAD.
     After the push (step 4) and the PR (step 5), before `gh pr merge`, read
     `gh api repos/<owner>/<repo>/commits/<sha>/check-runs` in this dispatch - `<owner>/<repo>`
     as the PR URL names them, `<sha>` the full `git rev-parse HEAD` you pushed, both spelled
     out literally, the command recorded with exit 0 - and record one `ci_evidence` entry
     per declared job from the check-run whose `name` is its job name: `repo` (`<name>`),
     `job` (the declaration verbatim), `check_run_id`, `url` (its `html_url`), `head_sha`
     and `conclusion`. Only a check-run with `conclusion` `success` and `head_sha` equal to
     the pushed head is evidence; a queued or in-progress one is not. To wait for one, wait
     inside that same command: a loop that re-reads the check-runs at most every 3 s and
     stops at a deadline. A job still pending at the deadline, failed or missing means the
     re-run above applies unchanged, and a repo that declares no `ci_evidence` keeps the
     re-run rule exactly.
   - **A conflict:** resolve it yourself in this worktree, keeping both sides' intent,
     commit the merge, then you MUST (a) re-run the test bar as above and (b) have the
     resolution reviewed in a FRESH context - start a sub-agent (a new agent with no history
     of this conversation: the `subagent` tool on pi, the `Agent` tool on Claude Code) and have it read only `git diff <pre_merge> HEAD` for the conflicted files and the two sides
     (`git show origin/<default>:<file>`, `git show <pre_merge>:<file>`) and reports
     whether the resolution drops or breaks either side. Record `llm_resolved_conflict: true`
     and the reviewer's conclusion in the delivery note. A failing re-run or a reviewer
     objection is a reason in the human question. The driver recognises a hand-resolved
     merge from the history itself (it re-does every merge since the verified commit), so
     the question is asked whatever you report.
   Never add other commits here. Record `tests_passed` for the head you are about to push:
   true only when it equals the verified commit or you re-ran the test bar on it. On the
   CI-evidence path (only where a re-run is owed - never when HEAD equals the verified
   commit) leave `tests_passed` undecided here: the check-runs it rests on exist only after
   the push, so you decide it in step 5.
4. **Push the feature branch (idempotent).** `git ls-remote origin refs/heads/<branch>`:
   (HEAD must contain `origin/<default>` as fetched - the driver checks it.)
   - absent, or an ancestor of HEAD (`git merge-base --is-ancestor <remote> HEAD`):
     `git push origin HEAD:refs/heads/<branch>` (a re-run of an up-to-date branch is a
     harmless no-op);
   - anything else (someone else moved the branch): never force - stop with outcome
     `incomplete` and say so; a human reconciles and resumes.
5. **Open or reuse the PR (idempotent).**
   `gh pr list --head <branch> --state all --json number,url,state,mergeCommit`:
   - an OPEN PR: reuse it (never a second PR);
   - a MERGED PR whose branch carries new commits since its merge commit (a "back to
     code" answer after the merge): open exactly one follow-up PR for those commits on
     the same branch - the only case a second PR is allowed;
   - none (or only CLOSED unmerged ones - mention them in the note): create one,
     `gh pr create --base <default> --head <branch> --title "<WI slug>: <one-line summary>" --body-file artifacts/deliver-pr-<name>.md`,
     the body naming the WI slug, a change summary, the review verdict and the test
     results (verification's, plus your re-run if any).
   **CI-evidence path only** (the CI bullet in step 3, where a re-run is owed): now - after
   the push, with the PR open, before step 6 - read the check-runs, record the `ci_evidence` entries, and only
   then decide this repo's `tests_passed`: true when every declared job's check-run is
   `success` for the pushed head; otherwise re-run the test bar now (still before
   `gh pr merge`) and decide it on that re-run.
6. **Merge rule.** The PR may be merged without asking iff ALL hold:
   - the independent review passed with no blocking finding
     (`steps.review.output.verdict` is pass, no finding with severity `blocking`); in a
     workflow without a `review` step (autopilot) verification's pass stands in for it;
   - the review covers the final diff: `steps.review.output.reviewed_heads.<name>`
     exists, and every commit between it and the delivered HEAD is a merge git itself
     reproduces (the driver checks the first-parent history). A plain commit after the
     review pass - by the build step's re-entry, by verification or by you - invalidates
     the pass: a re-verification that does not move HEAD keeps it covered. In a workflow
     without a `review` step (autopilot) there is no review head: verification's
     `verified_heads.<name>` stands in for this coverage, exactly as the driver judges it;
   - when verification ran **scoped** (`steps.verification.output.scope`), review answered
     `scope_adequate: true` and recorded the same scope
     (`steps.review.output.scope.declared_paths` equals verification's effective declared
     paths, an incremental prior's union included); a missing answer, a missing review
     scope or any disagreement means the rule does not hold and you ask the human — it is never a reason to
     rewind to code on its own;
   - no authored scenario the verification report records is `skipped`
     (`steps.verification.output.scenarios`): a skip is legal for the gate but never for
     the automatic merge - it routes the delivery to the human question naming the
     skipped title (an owner-approved `skipped-with-rationale` e2e-author mode records an
     empty list and triggers nothing);
   - the tests are covered on the pushed head (HEAD is the verified commit, you re-ran
     the test bar after the last merge in this dispatch, or the repo's declared CI evidence
     holds for the pushed head) and `tests_passed` is true;
   - no merge since the verified commit was hand-resolved (`llm_resolved_conflict` is
     not true);
   - the owner-review gate (section 3) is settled: the work item declared no
     `owner-review:` artifact, or this round carries the owner's `deliver-owner-review`
     answer 「过目通过」;
   - the pre-flight found nothing.
   Then: `gh pr merge <pr_url> --merge`, and read the merge commit with
   `gh pr view <pr_url> --json mergeCommit -q .mergeCommit.oid`. If `gh pr merge` itself
   fails (not mergeable, checks pending), that is a reason for the human question, not a
   retry loop.
   After the PR merge this repo is done: no further `git merge`/`git pull`/`git push`
   in it (do not sync the local branch with `origin/<default>`). The driver judges the
   local HEAD you pushed and tested; fast-forwarding it onto the PR merge commit makes
   that the "last merge" and voids the re-run that licensed the PR merge.

## 3. The owner-review gate (declared artifacts)

Settle this gate once for the whole delivery, after every repo's PR is open and pushed
(section 2, steps 1-5) and **before any `gh pr merge`** of this dispatch.

- **No declaration -> no question.** When no `owner-review:` line exists in
  `artifacts/plan.md` or in any completed step's `summary` in `execution.json`, skip this
  section entirely: the gate is settled and the delivery has zero new interruptions.
- **A declaration -> show the material, once per delivery round.** Return outcome
  `needs_input` with `question_id` `deliver-owner-review` (add a suffix such as `-2` when
  you ask again after an answer) and a `question` that carries, per repo, the PR link;
  each declared `owner-review:` line verbatim; and, per declared path, whether it exists
  (`test -e <path>` from the capsule root) with its description. State that nothing
  merges until the owner answers.
- **The answers - exactly two:**
  - **过目通过** - continue: the merge rule's owner-review condition now holds, and
    nothing more;
  - **不通过: <what to change>** - you return outcome `gate_failed` with `verdict: fail`
    and a `fail_reason` carrying the owner's instruction verbatim and the PR links; the
    workflow rewinds to its build step, and the next deliver run reuses the same branch
    and PR.
  Anything else is a new question.
- **过目通过 is not a merge decision.** Every other merge-rule condition (section 2,
  step 6) must still hold on its own; when one fails, ask `deliver-merge` per section 4,
  even though a human has already answered in this round. The driver's human-answer check
  counts any answer in the round, so it no longer catches a merge outside the rule after an
  owner-review answer: this rule is the only fence.
- **Silence is a wait, never an approval.** The parked `needs_input` is the wait: never
  answer it yourself, never merge around it, never treat an unanswered question as
  passed.
- **The material is read-only.** Never edit, regenerate or move a declared artifact, and
  never commit one into `repos/<name>/` to show it.
- **Already merged on a re-dispatch** (section 2, step 1): report it merged, never
  "owner-reviewed". When a declaration exists and this dispatch carries no
  `deliver-owner-review` answer, say so and route the delivery through section 4's
  "already merged; acknowledge" question.
- **The answer is this round's.** A `deliver-owner-review` answer under "Answered
  questions" covers the tree it was shown; a rewind to the build step starts a new round
  that carries no such answer, so the gate asks again.

## 4. The human-only question

If the merge rule does not hold for any repo (or a merge failed), push and open/reuse the
PRs first (so the human can look), then return outcome `needs_input` with
`question_id` `deliver-merge` (add a suffix such as `-2` when you ask again after an
answer) and a `question` that lists, per repo, the PR link and every reason the rule did
not hold, and offers exactly three answers:

- **merge** - you run `gh pr merge <pr_url> --merge` for those PRs and complete;
- **don't merge** - you complete with `merged: false` and the human's reason as
  `not_merged_reason`; the PR stays open;
- **back to code: <what to change>** - you return outcome `gate_failed` with
  `verdict: fail` and a `fail_reason` carrying the human's instruction verbatim and the PR
  links; the workflow rewinds to its code step, and the next deliver run reuses the same
  branch and PR (after a MERGED PR: one follow-up PR, step 5).

For a repo that is **already merged** (section 2, step 1: "already merged;
acknowledge") the question offers, instead of merge / don't merge:

- **acknowledge** - the human accepts the merge that happened: check that the merge
  contains the verified head (`git merge-base --is-ancestor <verified_heads.<name>>
  <merge_commit>`) and that the owed `test_bar` re-run passed, then complete with
  `merged: true` for that repo. This round's `acknowledge` answer settles it: never ask
  again;
- **back to code: <what to change>** - as above.

Only when this delivery is scoped (`steps.verification.output.scope` is present) the
question additionally offers a fourth answer:

- **全量重验** (full re-verification) - the human wants the scoped tier abandoned: you return
  outcome `gate_failed` with `verdict: fail`, a `fail_reason` naming the human's answer, and
  `rewind_to: verification` in the output. The driver honors `rewind_to` on the deliver step
  and rewinds to verification at the FULL tier; the next verification runs the full bar and
  the following deliver is judged with the resulting full credential (no scoped chain).
  Offer this answer only on a scoped delivery: on a full-tier ticket the merge rule has no
  scoped condition, and the driver ignores `rewind_to`.

Both rewinds are owner-routed: a deliver FAIL a human answered in this attempt charges
neither the edge slot nor `max_total` (only a FAIL no human answered is charged).

Your next dispatch sees the answer under "Answered questions". Act on it exactly; anything
else is a new question.

## 5. Result

Write `artifacts/delivery.md` (the delivery note) and emit `verdict: pass` with one
`repos` entry per declared repo (`branch`, `pr_url`, `merged` and `merge_commit` or
`not_merged_reason`, `llm_resolved_conflict`, `tests_passed`; or `skipped_reason`), the
`ci_evidence` entries of any repo whose tests its CI evidence covered, and the same note in
`delivery_note`. The delivery note records the owner-review gate: when it asked, the
question, the artifacts shown and the owner's answer verbatim; otherwise "no
owner-review declared". It (and the PR body, which already goes
through `--body-file`) also summarizes every `non-blocking` finding of
`steps.review.output.findings` — the follow-ups the review recorded but did not block
on — so the owner can decide which become tickets; no step ever files them
automatically. A repo left unmerged without a PR (non-GitHub origin) has
`pr_url` empty. An infrastructure failure (no push identity, origin unreachable) is
outcome `incomplete` with the exact error - the
work item blocks and a human fixes the machine and resumes; never work around it.

## Principles

Merge, push, PR and check-run waits are each one foreground command with an explicit timeout; a wait on CI or a merge is a condition loop inside that same call.

{{principles}}

## Output

Emit the final JSON object per the schema above.
