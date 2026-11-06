# develop — the default development lane

The flow-level contract of this lane (workflow contract 5); the `steps` array of its
`workflow.yaml` is the only execution order. It replaced the four lanes listed in
`workflows/_archive/README.md`.

```
build --> review --> sync --> verification --> publish --> merge ==> landed
  ^         |                       |              |         |
  +---------+-----------------------+--------------+         |  (a FAIL: back to build)
                      ^                                      |
                      +--------------------------------------+  (a PR that moved or was refused at merge)
```

A project may skip `merge` (below, **Skipping merge**): the lane then ends after `publish`
with the PRs open, awaiting a human review — `published`, never `landed`.

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

## What each step owes

- **build** (fresh session; skill `build`) — one session, four segments
  (`steps/build.md`): a plan against the work item's acceptance criteria
  (`artifacts/plan.md`, its `## Coverage` the criterion → test table); per unit, a test — a
  unit-level test, or an e2e scenario whose suite declares `carries:` — committed red
  before the implementation that turns it green, both runs the session's own, recorded in
  `artifacts/build/units.json` (`[{id, test_commit, impl_commit, run{repo, cwd, argv},
  marker}]`); fast checks plus every new test; a self-review against review's five
  blocking classes (`artifacts/self-review.md`). It asks the owner (`pf step ask`) only
  when the code reveals something the ticket did not decide, when the plan diverges from
  the ticket, when the ticket carries `confirm_plan: true`, or when it has no acceptance
  list or an incomplete one — right after the plan, never at the end. Its hand-off states
  each owner decision with the owner's words and its reading. A round a FAIL opened
  changes the tree (or units.json), or states per point why not; `review` judges that.
  The session orchestrates fresh-context sub-agents (`workflows/_shared/build_subagents.md`;
  a pi dispatch is told, as a dispatch fact, that it dispatches none).
- **review** (fresh, gate; skill `review`, heavy-run admission, tool window 20 minutes) —
  the shared review (`workflows/_shared/steps/review.md`), on the facts of three
  pre-written lines of the workflow's script `facts.py`: `plan-conformance`, `build
  --replay --isolation host` (each unit's run replayed on the tree of its test commit, on
  the host: it must be red) and `carries`. A
  blocking finding names one of five classes: `bug`, `ac_gap`, `project_rule`,
  `test_weakened`, `guarantee`; everything else is non-blocking. On a later round it checks
  the previous round's findings and, from the commits the daemon stamped, whether the build
  changed anything at all — a build that changed nothing without a reason per finding fails
  again.
- **sync** (no gate; skill `sync`, `sync.py`, tool window 20 minutes) — checks that each
  task repo stands at the heads review judged (stamped on review's section), or at its own
  earlier `after` when merge's FAIL sent the lane back here; then merges origin's default
  branch in place (never a rebase): the conflicts the default branch's
  `.polyforge/integrate.conf` makes mechanical resolved by the script, a real conflict
  resolved by the step's own agent and committed by the skill's continue line. When the
  resolution is not the agent's to make, it asks the owner, and hands the work back to
  `build` (or cancels) only on the owner's word, after the skill's abort line.
- **verification** (gate, `waivable: true`; skill `verify`, `verify.py`, heavy-run
  admission, tool window 35 minutes) — checks the sync's result, then runs `verify.py
  --tier light --unit-runs artifacts/build/units.json` (in each repo's verification
  container when it declares one, else on the host; each unit's green run again,
  beside the light plan: the fast gates, the affected unit tests, the e2e suites the change
  touched). Exit 0 passes; a red it can place on the change FAILs to `build`; a red it
  cannot place is the owner's call — run again, have it fixed, or accept it (`waive`,
  citing the owner's message). Exit 2 is retried, then `blocked`. The heavy-run admission
  is the daemon's: a denied dispatch waits in the daemon's queue, no process, no budget.
- **publish** (gate; skill `publish`) — checks that the verification's result names every
  repo's HEAD and passed (or that the owner's waiver of that same round is quoted on the
  verification's section) and that review passed; pushes `polyforge/<capsule directory
  name>`, opens or updates the PR (its body: the sync record, the verification's record or
  the waiver verbatim, the review verdict, the acceptance list, the coverage table and a
  `## Follow-up` with every non-blocking finding), writes `artifacts/publish.json` and
  `artifacts/delivery.md`, and holds for the owner's 「过目通过」 when an `owner-review:`
  line is declared (「不通过」 is a FAIL citing the owner, charged to nothing). When the
  project skips `merge`, publish is the lane's last step (the opening of
  `artifacts/handoff.md` notes it so): the PR body says "This lane does not merge this PR —
  it awaits a human review.", the step runs `published.py`, registers its record —
  `pf step hub wrap-attrs integration @artifacts/publish/integration.json` — and writes the
  wrap summary (below, **Skipping merge**).
- **merge** (gate, `skippable: true`; skill `merge`, `merge.py`, the per-repo merge lock,
  tool window 10 minutes; wrap attrs `integration`) — checks publish's heads against the verified heads, then
  merges each PR with `gh pr merge --merge --match-head-commit <verified head>` under the
  daemon's merge lock (one per repository, every repo of the item locked in one order). A
  PR that moved, closed, was retargeted, merged elsewhere off the verified head or refused
  by the forge FAILs back to `sync`. On a pass the command writes the landing record
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
2. **A machine that cannot run a step's command** — `facts.py`, `sync.py`, `verify.py` or
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
   lane's one waivable gate), or cancel.
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
- `publish` is the lane's last step: the step order in the opening of
  `artifacts/handoff.md` notes it as the last step and `merge` as skipped, and merge's
  `wrap_attrs: [integration]` moves to it — its `done` is refused until it registered the
  record `published.py` wrote (`artifacts/publish/integration.json`:
  `{state: published, repos: [{repo, pr, head, light_verified_head}]}`, each PR's link and
  its verified head);
- the work item is wrapped with `integration.state = published` and never enters an
  integration batch: the integration lane's collect takes `landed` work items only.

The micro lane shares the publish skill, but its merge declares no `skippable`: a project's
`steps.<project>.merge.skip` never skips it.

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
of 20 minutes (the replays), `verification` `wall_minutes: 45` with a tool window of 35
minutes, `publish` `wall_minutes: 15`: the daemon's step bound T is the larger of its
global default and three times the estimate. `sync` and `merge` declare a tool window only
(20 and 10 minutes): each pre-written command derives its own deadline from the window
(two minutes under it) and ends — its result written — before the harness's shell call and
the daemon's supervision cut it. No workflow-level budget.
