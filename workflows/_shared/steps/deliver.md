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
          skipped_reason: { type: string, description: Set (and nothing else needed) when the repo has no commits beyond its claim baseline - nothing to deliver. }
          default_branch: { type: string }
          branch: { type: string, description: "The pushed feature branch, exactly polyforge/<capsule directory name>." }
          pr_url: { type: string }
          merged: { type: boolean }
          merge_commit: { type: string, description: The PR's merge commit sha (required when merged is true). }
          not_merged_reason: { type: string, description: Why the PR stays open (required when merged is false). }
          llm_resolved_conflict: { type: boolean, description: true when merging the default branch conflicted and you resolved it. }
          tests_passed: { type: boolean, description: The full test bar passed on the pushed head (re-run after any merge that changed the tree). }
    delivery_note:
      type: string
      description: The delivery note - what was delivered where (branch, PR, merge commit or reason), the review and test conclusions, and anything a human must follow up. Also written to artifacts/delivery.md.
    fail_reason:
      type: string
      description: Required when verdict is fail - the human's "back to code" instruction verbatim plus the PR links, carried to the code step as its rewind input.
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
- Never run `pf` and never read polyforge credentials or configuration; the delivery
  note goes into your result and `artifacts/delivery.md`.

## 1. Inputs

- `execution.json` (under `runtime-state/`): `steps.review.output` - the independent
  review's verdict and findings - and `steps.verification.output`: its `verified_heads`
  (written by the driver: each repo's HEAD when verification passed) and its `test_bar`
  (`{repo, command}` - the test-bar commands it ran in each repo). A workflow without a `review` step has no independent review.
- `capsule.json`: `repos` maps each declared repo name to its claim-time baseline sha.
- `artifacts/verification-report.md`: the full test bar (the exact build/lint/test
  commands verification ran).

## 2. Per declared repo (one pass for each `repos/<name>/`)

A capsule with no task repos has nothing to deliver: complete with `repos: []` and say so
in the delivery note. Work inside `repos/<name>/`. Skip the repo (record `skipped_reason`) when it has no
commits beyond its baseline (`git rev-list --count <baseline>..HEAD` is 0). Otherwise:

1. **Fetch.** `git fetch origin`; find the default branch
   (`git remote show origin | sed -n 's/.*HEAD branch: //p'`, falling back to
   `git symbolic-ref --short refs/remotes/origin/HEAD`) - call it `<default>`.
   **Already delivered?** (a re-dispatch, a resume or a daemon restart after the merge):
   when `git merge-base --is-ancestor HEAD origin/<default>` holds and
   `gh pr list --head <branch> --state merged --json url,mergeCommit` names a PR, this
   repo is merged - record that PR and its merge commit (`merged: true`) and skip to the
   result. The merge rule still applies to it: when HEAD is not the verified commit,
   re-run this repo's `test_bar` commands (as in step 3, but WITHOUT step 3's merge) in
   this dispatch first; if that fails or the history shows a hand-resolved merge, report
   it through the human question ("already merged; acknowledge") instead of completing.
   **Origin not on GitHub, or `gh` unavailable/unauthenticated:** there is no PR to open.
   Do the pre-flight, the merge and the push (steps 2-4) only, then ask the human question
   with the pushed branch instead of a PR link; record `merged: false` with the reason
   (or what the human did by hand). This is not `incomplete`.
2. **Mechanical pre-flight (the delivery checklist).** Any hit is a reason for the human
   question (section 3) - not yours to fix:
   - `git status --porcelain --untracked-files=all` must be empty: an untracked or
     uncommitted test file (or any other file) would not be delivered.
   - No release residue: no tag points at a commit this WI made
     (`for c in $(git rev-list <baseline>..HEAD); do git tag --points-at "$c"; done` prints
     nothing - a release this WI must not have cut), and the capsule holds no
     `artifacts/release-*` tree.
   - Where the repo's review checklist names owner-kept mechanisms (for polyforge: the
     V4 command-evidence gate, the human gates, the enforced release/deploy output schemas), grep
     that they still exist in the tree you deliver.
   Judgment items (invented mechanisms, duplicated acceptance tests) belong to the
   review; list any you notice in the delivery note as review points for the human.
3. **Merge the latest default branch.** Record `pre_merge=$(git rev-parse HEAD)`, then
   `git merge --no-edit origin/<default>`.
   - **Tests are covered only when HEAD equals the commit verification passed on**
     (`verified_heads.<name>`). "Already up to date" is NOT enough: an earlier dispatch of
     this step (before a crash or a daemon restart) may already have merged. Whenever
     HEAD differs - a clean merge now or earlier - re-run every `test_bar` command of this
     repo exactly as written, inside `repos/<name>/` (as `cd repos/<name> && <command>`, or
     with that cwd), AFTER your last `git merge`/`git pull` of this repo and BEFORE its
     `gh pr merge`, in this same dispatch, each with exit 0. The driver checks exactly
     that, in the order your commands are recorded; tests run in an earlier dispatch do not
     count, and a verification without a `test_bar` for this repo means a human decides.
   - **A light-passed workflow** (no `verified_heads.<name>` for this repo - the last
     verification pass was a light recheck, `steps.verification.output.mechanical_pass`):
     there is no verified head to equal, so the tests are covered ONLY by this dispatch's
     recorded re-run of the CARRIED `test_bar` (`steps.verification.output.test_bar`) -
     unconditionally, whatever HEAD is. Re-run every carried `test_bar` command of this
     repo exactly as written, inside `repos/<name>/`, in this same dispatch, after your
     last `git merge`/`git pull` of this repo and before its `gh pr merge`, each with exit
     0, and record it. Without a recorded re-run - or without a carried `test_bar` entry
     for this repo - a human decides.
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
   true only when it equals the verified commit or you re-ran the test bar on it.
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
   - none (or only CLOSED unmerged ones - mention them in the note): create one,
     `gh pr create --base <default> --head <branch> --title "<WI slug>: <one-line summary>" --body-file artifacts/deliver-pr-<name>.md`,
     the body naming the WI slug, a change summary, the review verdict and the test
     results (verification's, plus your re-run if any).
6. **Merge rule.** The PR may be merged without asking iff ALL hold:
   - the independent review passed with no blocking finding
     (`steps.review.output.verdict` is pass, no finding with severity `blocking`); in a
     workflow without a `review` step (autopilot) verification's pass stands in for it;
   - the tests are covered on the pushed head (HEAD is the verified commit, or you re-ran
     the test bar after the last merge in this dispatch) and `tests_passed` is true;
   - no merge since the verified commit was hand-resolved (`llm_resolved_conflict` is
     not true);
   - the pre-flight found nothing.
   Then: `gh pr merge <pr_url> --merge`, and read the merge commit with
   `gh pr view <pr_url> --json mergeCommit -q .mergeCommit.oid`. If `gh pr merge` itself
   fails (not mergeable, checks pending), that is a reason for the human question, not a
   retry loop.
   After the PR merge this repo is done: no further `git merge`/`git pull`/`git push`
   in it (do not sync the local branch with `origin/<default>`). The driver judges the
   local HEAD you pushed and tested; fast-forwarding it onto the PR merge commit makes
   that the "last merge" and voids the re-run that licensed the PR merge.

## 3. The human-only question

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
  branch and PR.

Your next dispatch sees the answer under "Answered questions". Act on it exactly; anything
else is a new question.

## 4. Result

Write `artifacts/delivery.md` (the delivery note) and emit `verdict: pass` with one
`repos` entry per declared repo (`branch`, `pr_url`, `merged` and `merge_commit` or
`not_merged_reason`, `llm_resolved_conflict`, `tests_passed`; or `skipped_reason`) and the
same note in `delivery_note`. A repo left unmerged without a PR (non-GitHub origin) has
`pr_url` empty. An infrastructure failure (no push identity, origin unreachable) is
outcome `incomplete` with the exact error - the
work item blocks and a human fixes the machine and resumes; never work around it.

## Output

Emit the final JSON object per the schema above.
