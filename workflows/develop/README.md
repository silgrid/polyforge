# develop — the default development lane

The flow-level contract of this lane (workflow contract 1); the `steps` array of its
`workflow.yaml` is the only execution order. It replaced the four lanes listed in
`workflows/_archive/README.md`.

```
build --> review --> sync --> verification --> publish --> merge ==> landed
  ^         |                       |              |         |
  +---------+-----------------------+--------------+         |  (a FAIL: back to build)
                      ^                                      |
                      +--------------------------------------+  (a PR that moved, the default branch moved since verification, or was refused at merge)
```

A project may skip `merge` (below, **Skipping merge**): the lane then ends after `publish`
with the PRs open, awaiting a human review — `published`, never `landed`. A project may also
declare the merge done by a person (below, **Merging by a person**): `steps.<project>.merge.by:
human` keeps every step running and hands the PRs to the project's reviewer — the lane takes
the person's landing and wraps the work item `landed`, into the integration lane's pool.

The lane is built for speed and ends with the work item's PRs merged into the default
branch — each work item merges itself, and resolves its conflicts with the default branch
then, while they are its own. `build` and `review` run on the lane's strong candidates;
`sync`, `verification`, `publish` and `merge` are **mechanical steps** on the `cheap` tier
(the workspace yaml's `tiers.<project>.cheap.models`, else the project default: the lane's
`defaults.tiers.cheap` is a placeholder), each following a shared skill
(`workflows/_shared/skills/<name>.md`) that runs one pre-written command verbatim and
routes on the exit code its result file carries. The integration lane then runs the full
gate over the default branch's merged work items, triages a red one to fix tickets, and
tags a release-ready commit; the release lane ships the newest such commit.

**How the steps connect.** Every step reads `artifacts/handoff.md` first and ends with one
`pf step handoff` (`workflows/_shared/step-header.md`): the daemon stamps each section's
header — status, round, the commits each repo moved through, the owner's words — and the
step writes the body. Each skill says what its step reads and checks before it starts
(**downstream checks upstream**: a hand-off that does not hold is a gate's `fail`, or a
non-gate's question to the owner) and what its hand-off carries. Data a later command
needs travels in result files, never in a body: `artifacts/plan.md`,
`artifacts/build/units.json`, `artifacts/review.md`, `artifacts/sync/result.json`,
`artifacts/verify/result.json`, `artifacts/publish.json`, `artifacts/merge/result.json`
and `artifacts/merge/landing.json`. The daemon orchestrates; it judges no content.

## Step process environment

The daemon gives every pi and Claude step process its capsule facts and a capsule private temporary directory. `TMPDIR` is `<capsule>/artifacts/downloads/tmp/`; the daemon creates it before spawning the harness, and the value is different for concurrent capsules. A harness configuration or tool that deliberately overwrites `TMPDIR` is outside this guarantee. A background process started by a step shell ends with that tool call, so a later call must not wait for it; run long commands in the foreground and put temporary files under `$TMPDIR` or `artifacts/downloads/`, never a fixed `/tmp/<name>`.

The complete variable table is in [the workflow contract's Scripts environment section](../../docs/reference/workflow-contract.md#scripts). Its Environment section describes inheritance and `PATH`; the lane's step processes are resident across turns, but a shell tool invocation does not provide cross-invocation process lifetime.

## What each step owes

- **build** (fresh session; skill `build`) — one session, four segments
  (`steps/build.md`): a plan against the work item's acceptance criteria
  (`artifacts/plan.md`, its `## Coverage` the criterion → test table, its `## 分析对照` section
  mapping the ticket's `## 分析` items to units or stated reasons, each `## Units` entry
  recording the behaviour its test observes, how its expected values are derived
  independently of the implementation and the change it
  catches (`workflows/_shared/test_quality.md`)); per unit, a test — a
  unit-level test, or an e2e scenario whose suite declares `carries:` — committed red
  before the implementation that turns it green, both runs the session's own, recorded in
  `artifacts/build/units.json` (`[{id, pre_existing_red?, timeout?, test_commit?, impl_commit,
  run{repo, cwd, argv}, marker}]`; a unit whose reproduction test already exists red
  before the change declares `pre_existing_red: true` and carries no `test_commit`). Each
  unit's targeted command runs only that unit's test (the test runner's own name filter; a suite
  that offers no filter is named in the hand-off and runs once, in the scoped command); a result
  already in hand for its key — the same command text, the same working-tree hash of the root it
  runs in, the same declared prerequisites — is not re-proven: a bare commit changes no tree, and beside the
  test-first red no manual worktree replay is made (the facts engine's replay is the record); a
  flaky failure is re-run only in the suite that failed, once, by the written fast-check command
  itself — the same rules verification runs — never by hand: the check already contains that
  one re-run, and a failure still red after it is red; a red-state run must exit non-zero within
  its limit — a unit self-reports `timeout` in units.json, else the default — and a run its
  limit kills is an evidence problem, never a pass: the waiting inside it is bounded and an
  assertion failure exits at once; and a long command writes its output to
  a log file with its exit code and the log is searched after the run, never piped straight into
  grep, head or tail. After the last commit it re-runs the lane's fast-check
  proof line with `--phase fast` per touched repo
  (`fast_checks.py --repo <repo> --phase fast --json --out
  artifacts/build/fast-checks/<repo>.json`): the tree is unchanged, so every
  green command is reused and only the result's `heads` move to the commits
  the hand-off gate compares against HEAD; a repo the change touched without
  that pass at HEAD has its `done` refused. Per-unit
  fast checks narrowed to the roots the unit's written `Touched files` reach (`--phase fast
  --root`, the green runs whose key is unchanged reused) plus every new test; the close's scoped selection — `fast_checks.py --close-scoped`,
  the same light-tier selection verification plans, for a work item whose verification runs the
  light tier (an integration batch's fix ticket closes without one), its red or `incomplete`
  result fixed or owner-ruled before the hand-off — a red every entry of whose `reds[]` the
  run's baseline triage records `pre_existing` excepted: not this change's, the close hands
  off citing the result file — and a green or pre-existing result's `details.selected` merged
  into the change analysis — before the close's
  facts checks — `facts.py build --replay --isolation host` (each
  unit's run replayed red at its test commit), `plan-conformance` and
  `carries`, the same engine and the same lines review judges with, a
  red naming the unit, the plan's undeclared or lapsed obligation, or
  the repo, suite and reason and fixed before the hand-off (an exit 2
  that stays 2 after its retries is disclosed in the hand-off, for
  review to re-run); the change analysis's close check (`facts.py analysis` — the
  same shared validator `verify.py --analysis` calls; a red names the entry and the rule it
  violates and must be fixed before the hand-off); a self-review against review's five
  blocking classes and its doc-impact check, item by item — each document compared, its
  conclusion, and synced or no-change-needed with the reason — recorded
  (`artifacts/self-review.md`). A red the change did not cause is the check's own to triage
  first: the run judges every still-red unit on the change's baseline (the same judgment
  verification runs) and records the pre-existing ones — signature and baseline commit in the
  result file — as no red of this change's. What is left — the default branch
  already red, an `error`/`partial` fast check, or a lane that planned no check — is the
  owner's: `pf step ask`, and on the release hand off `done` with `--per-owner <the reply's
  at>`; the stamp records it. It asks the owner (`pf step ask`) only
  when the code reveals something the ticket did not decide, when the plan diverges from
  the ticket, when the ticket carries `confirm_plan: true`, or when it has no acceptance
  list or an incomplete one — right after the plan, never at the end. Its hand-off states
  each owner decision with the owner's words and its reading. A round a FAIL opened
  changes the tree (or units.json), or states per point why not; `review` judges that.
  The session orchestrates fresh-context sub-agents (`workflows/_shared/build_subagents.md`; a pi build uses the daemon-owned worker when `pi_subagents` is on and records `main` when it is off).
- **review** (fresh, gate; skill `review`, heavy-run admission, tool window 20 minutes) —
  the shared review (`workflows/_shared/steps/review.md`), on the facts of four
  pre-written lines of the workflow's script `facts.py`: `plan-conformance`, `build
  --replay --isolation host` (each unit's run replayed on the tree of its test commit
  (or of the change's base for a unit declared `pre_existing_red`, whose red evidence
  that replay is), on the host: it must be red), `carries` and `analysis` (the change
  analysis's own close check — the same shared validator `verify.py --analysis` calls;
  every entry it fails is a blocking `test_weakened` finding naming the entry). A
  blocking finding names one of five classes: `bug`, `ac_gap`, `project_rule`,
  `test_weakened`, `guarantee`; everything else is non-blocking. A unit that misses its
  test-quality record (`workflows/_shared/test_quality.md`), or whose replayed red
  contradicts the recorded reason, is `test_weakened`, and so is a test that asserts an
  asynchronously settling state on one read right after the action instead of a bounded
  poll (the bar's settle duty). On a later round it checks
  the previous round's findings and, from the commits the daemon stamped, whether the build
  changed anything at all — a build that changed nothing without a reason per finding fails
  again.
- **sync** (no gate; skill `sync`, `sync.py`, `fast_checks.py`, tool window 20 minutes) — checks that each
  task repo stands at the heads review judged (stamped on review's section), or at its own
  earlier `after` when merge's FAIL sent the lane back here; then merges origin's default
  branch in place (never a rebase): the conflicts the default branch's
  `.polyforge/integrate.conf` makes mechanical resolved by the script, a real conflict
  resolved by the step's own agent and committed by the skill's continue line; then it runs
  the lane's fast-check line over every repo the merge moved (`fast_checks.py --repo
  <repo> --phase fast --json --out artifacts/sync/fast-checks/<repo>.json`). `sync`
  declares `fast_check_proof`, so its `done` is refused until each moved repo has a `pass`
  at HEAD (a fast-forward needs none); a red breaks the tree at sync, where the agent can
  still fix it in the conflicted files, rather than at verification. A red that is not this
  merge's the check itself triages first: one the baseline (after the merge, the default
  branch's tip) reproduces the same way is recorded `pre_existing` and the check passes;
  what is left — an `error`/`partial` result, no planned check, or a red the baseline could
  not settle — is the owner's: `pf step ask`, and on the release hand off `done` with `--per-owner
  <the reply's at>`, the stamp recording it. When the
  resolution is not the agent's to make, it asks the owner, and hands the work back to
  `build` (or cancels) only on the owner's word, after the skill's abort line.
- **verification** (gate, `waivable: true`; skill `verify`, `verify.py`, heavy-run
  admission, tool window 90 minutes) — checks the sync's result, then runs `verify.py
  --tier auto --unit-runs artifacts/build/units.json --analysis artifacts/build/change-analysis.json`
  (in each repo's verification container when it declares one, else on the host; each unit's
  green run again, beside the light plan and the change analysis's listed tests — the union
  is add-only, the engine's selection never narrowed: the fast gates, the affected unit
  tests, the e2e suites the change touched, the tests build's analysis listed under its
  supplement cap). The tier is the work item's own: an integration batch's fix ticket (its
  attrs carry the `integration_fix` marker) runs the full bar — everything the light tier
  runs and the rest, the whole tool window its budget; every other ticket runs the light
  tier. Exit 0 passes; a red it can place on the change FAILs to `build`; a red it
  cannot place is the owner's call — run again, have it fixed, or accept it (`waive`,
  citing the owner's message). The step's `audit` parameter (the dispatch's
  `PF_PARAM_AUDIT`, default `on`) turns the lane's dependency audit off
  when the project's owner sets it to `off`
  (`steps.<project>.verification.params.audit` in the workspace config, or
  a ticket's `pf override` — a repository's own conf key can never do it):
  the engine is then asked `--audit off`, no full report plans the `audit`
  command, and the result files record the closure. Any other value is a
  named error before any check runs (exit 2, the fixed mark
  `pf-audit-param-invalid` in the summary) — the owner fixes the config; a
  retry cannot. Exit 2 is retried, then `blocked`. The heavy-run admission
  is the daemon's: a denied dispatch waits in the daemon's queue, no process, no budget.
- **publish** (gate; skill `publish`) — checks that the verification's result names every
  repo's HEAD and passed (or that the owner's waiver of that same round is quoted on the
  verification's section) and that review passed; pushes `polyforge/<capsule directory
  name>`, opens or updates the PR (its body: the sync record, the verification's record or
  the waiver verbatim, the review verdict, the acceptance list, the coverage table and a
  `## Follow-up` with every non-blocking finding), writes `artifacts/publish.json` and
  `artifacts/delivery.md`, and holds for the owner's 「过目通过」 when an `owner-review:`
  line is declared (「不通过」 is a FAIL citing the owner, charged to nothing). When the
  project skips `merge`, publish is the lane's last step (the step order of its dispatch
  header notes it so; the opening of `artifacts/handoff.md` is the claim-time copy): the PR
  body says "This lane does not merge this PR —
  it awaits a human review.", the step runs `published.py`, registers its record —
  `pf step hub wrap-attrs integration @artifacts/publish/integration.json` — and writes the
  wrap summary (below, **Skipping merge**).
- **merge** (gate, `skippable: true`, `human_mergeable: true`; skill `merge`, `merge.py`,
  the per-repo merge lock,
  tool window 10 minutes; wrap attrs `integration`) — checks publish's heads against the verified heads, then
  merges each PR with `gh pr merge --merge --match-head-commit <verified head>` under the
  daemon's merge lock (one per repository, every repo of the item locked in one order). A
  PR that moved, the default branch gained commits after verification, closed, was retargeted, merged elsewhere off the verified head or refused
  by the forge FAILs back to `sync`; sync merges the new default branch and verification runs again. On a pass the command writes the landing record
  (`artifacts/merge/landing.json`: `{state: landed, repos, merge_commits}`) and the step
  registers it — `pf step hub wrap-attrs integration @artifacts/merge/landing.json` — so
  the work item is wrapped with the attrs `integration` (the daemon adds the owner's
  waivers as `waivers`), awaiting the integration lane's full gate. The step declares
  `wrap_attrs: [integration]`: `pf step handoff --status done` is refused until that
  record is registered. As the lane's last
  step it writes the work item's wrap summary.

## Human stops

Every stop is the step's own agent running `pf step ask` and ending its turn; the owner's
reply is its next turn. The owner may also write to a step at any time, and a message to
a stopped work item wakes the step that stopped. Where the owner's words decide more than
the step's own work, the step ends with `cancel`, `waive`, `rewind` or a `fail` citing that
message (`--per-owner`); the daemon checks the message exists and never reads it.

1. **A surprise in build** — a dead end or a contradiction, a deviation, `confirm_plan:
   true`, a missing or incomplete acceptance list: once, right after the plan. No daemon
   check enforces either stop: `review` judges a self-written list with no owner reply, or a
   first test commit older than the reply to a `confirm_plan` ticket, a blocking
   `guarantee`.
2. **A machine that cannot run a step's command** — `facts.py`, `sync.py`, `fast_checks.py`, `verify.py` or
   `merge.py` exiting 2 after the skill's retries: the step hands off `blocked`; fix the
   machine, then write to the work item (or `pf resume`).
3. **A sync that is not the agent's to resolve**, or a HEAD that is neither the reviewed
   tree nor the sync's own earlier head.
4. **A red bar nobody can place** — verification asks; the same question replaces a
   re-run when an earlier round failed on the same tree.
5. **The rewind budget** — 3 rewinds in total (`max_rewinds: 3`, one budget across every
   gate, merge's FAIL back to sync included). Once it is spent, `pf step handoff` refuses
   a `fail` that cites no owner message: the gate asks the owner — one more round (a
   `fail` citing the reply, charged to nothing), accepting the red (verification only, the
   lane's one waivable gate), or cancel. Beside it the lane declares one uncounted class
   (`uncounted_rewinds`): a FAIL whose reason carries its substring — merge's
   default-branch-moved FAIL, another lane's landing that moved the default branch past
   what verification ran over — spends none of `max_rewinds` and counts on its own toward
   its cap (`max: 2`); `pf watch` shows that count apart, and past the cap the same FAIL
   asks the owner exactly like a spent `max_rewinds`.
6. **Publish's owner-review** — an `owner-review:` line holds the publication for the
   owner's 「过目通过」.

Without any of these the lane merges unattended — unless the project skips `merge`, when it
publishes unattended and the PRs wait for a human review. Work on `docs/concepts.md`'s "never
unattended" list carries `confirm_plan: true`.

## Skipping merge

`merge` is the lane's one `skippable` step: a project switches it off with
`pf config set steps.<project>.merge.skip true` (the workspace yaml's
`steps.<project>.merge.skip`; any other develop step is refused naming `skippable`). The set
of skipped steps is fixed when the work item is claimed (`runtime-state/skipped-steps.json`):
switching it later changes the next work item, never one already claimed, and a re-claim or
`pf resume` keeps it. For such a work item the lane runs build → review → sync →
verification → publish and ends:

- `merge` is never dispatched — its execution record says `skipped` and the event log
  carries `step_skipped`; nothing merges the PRs, they stay open and await a human review
  (the PR body says so);
- `publish` is the lane's last step: the step order of its dispatch header notes it as the
  last step and `merge` as skipped (the opening of
  `artifacts/handoff.md` carries the claim-time copy; a mid-run `pf override --skip` of
  `merge` updates the dispatch header's order, never the opening), and merge's
  `wrap_attrs: [integration]` moves to it — its `done` is refused until it registered the
  record `published.py` wrote (`artifacts/publish/integration.json`:
  `{state: published, repos: [{repo, pr, head, light_verified_head}]}`, each PR's link and
  its verified head);
- the work item is wrapped with `integration.state = published` and never enters an
  integration batch: the integration lane's collect takes `landed` work items only.

The micro lane shares the publish skill, but its merge declares no `skippable`: a project's
`steps.<project>.merge.skip` never skips it.

## Merging by a person

`merge` is the lane's one `human_mergeable` step: a project declares its develop merges
done by a person with `pf config set steps.<project>.merge.by human` (the workspace
yaml's `steps.<project>.merge.by`, the one mode this release knows; a `by` on any other
step is refused naming `human_mergeable`, and a step entry declares `by` or `skip`, never
both). The mode is fixed when the work item is claimed
(`runtime-state/step-modes.json`), exactly as the skipped-step set is: changing it later
governs the next work item, never one already claimed, and a re-claim or `pf resume`
keeps it. A claim of a lane that carries the step without declaring it `human_mergeable`
— micro's — is refused naming it, so the mode never reaches a lane that does not ask for
it; a `by` on a step the lane does not carry governs nothing (a skip's rule), so the
project's integrate and release batches — lanes with no `merge` step — claim as usual.

For such a work item every step still runs, and `merge` waits for the person:

- `publish` pushes the branch and opens the PR with the human-merge closing lines
  (「此 PR 等待人工审查并由人合并，合并后请回复车道」 and the hint to enable GitHub's
  *Require branches to be up to date before merging*, 合并前分支必须最新) — never the
  lane's own "do not merge it by hand";
- `merge` begins with `pf step ask`, listing every repo's PR link and the head the lane
  verified, and ends its turn: the question stands in the decision stream (`pf watch`),
  the step's process is released and its clocks stop while the owner answers — the ask's
  release every step has, so the waiting holds no step process and no budget;
- the owner's revision request is carried to `build` as an owner rewind
  (`pf step handoff --status rewind --to build --per-owner <at>` — charged to nothing);
- the person merges on GitHub with any of the three methods — **Create a merge commit,
  Rebase and merge, Squash and merge** — and the owner's answer resumes the step:
  `merge.py` never asks the forge to merge and never judges the default branch's
  freshness (the person's landing is the integration lane's full gate to judge); it
  takes the landing by the human landing rules — the patch-id fingerprint: a merge
  commit must carry the verified head as its branch side, a rebase must replay every
  commit of the branch, a squash must carry the branch's whole diff. What the person
  merged lands on the merge rules and enters the integration lane's pool: the work
  item is wrapped `integration: {state: landed, merge_commits: …}` naming, per repo,
  the landed commit set — a rebase's replayed commits, a squash's one plain commit —
  each with its merge flag, so the integration lane's `revert.py` reverts it
  (the plain commits with no `-m 1`);
- a PR closed without merging, merged with content that does not match the verified
  change (a commit added to the branch, a content changed on the way), or still open
  is `merge.py`'s exit 3: the step asks the owner again with that reason — never a
  FAIL back to `sync`, never `landed`, and the summary is never a lane's
  `uncounted_rewinds` class's to match.

The micro lane shares the merge skill, but its merge declares no `human_mergeable`: a
project's `steps.<project>.merge.by` never reaches it (the claim is refused).

## Rewinds

Every FAIL of review, verification or publish rewinds to `build`, which resumes its
previous session and reads the gate's section first; merge's FAIL rewinds to `sync`, which
merges the default branch again. `sync` is no gate: a real conflict never rewinds. No sweep
runs after a review PASS: its non-blocking findings, text-only ones included, reach the
owner in the PR body. A FAIL
without an owner message spends one rewind; one citing the owner (`--per-owner`) spends
none. **Same-tree rules** are the skills', never the daemon's: `review` judges whether a
re-done build changed anything from the commits stamped on the sections, and
`verification` never re-runs an unchanged tree and units.json that already failed — it
asks instead.

## Budget

`build` declares `budget.wall_minutes: 60`, `review` `wall_minutes: 30` with a tool window
of 20 minutes (the replays), `verification` `wall_minutes: 150` with a tool window of 90
minutes (a window that fits the full bar an integration batch's fix ticket runs; a light run
ends in its own time — the window is a bound, never a spend), `publish` `wall_minutes: 15`: the daemon's step bound T is the larger of its
global default and three times the estimate. `sync` and `merge` declare a tool window only
(20 and 10 minutes): each pre-written command derives its own deadline from the window
(two minutes under it) and ends — its result written — before the harness's shell call and
the daemon's supervision cut it. No workflow-level budget.
