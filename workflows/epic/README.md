# epic — the epic lane

The flow-level contract of this lane (workflow contract 1); the `steps` array of its
`workflow.yaml` is the only execution order. Its own work items are EPICS
(the lane's `epic: true` over `children_branch: "epic/{seq}"`): a bundle of
sub-tickets, each landed on the children branch by its own lane, merged into
the default branch as ONE change.

```
verify-children --> sync --> build --> review --> verification --> publish --> merge ==> landed
                     ^        ^         |             |              |          |
                     |        +---------+-------------+--------------+          |  (a FAIL: back to the fix build)
                     |                                                            |
                     +------------------------------------------------------------+  (the default branch moved since verification)
```

- **verify-children** (skill `verify-children`) — the first preparation step: the
  claim's children snapshot (`runtime-state/children.json`) against the task
  repos — every child wrapped and its landing record's merge commits actual
  ancestors of the epic branch. An anomaly is a `blocked` hand-off to the
  owner, never a rewind; the preparation steps carry no `on_fail`.
- **sync** (skill `sync`) — the closing-phase sync: the DEFAULT branch merged
  into the epic branch (the capsule's `runtime-state/target-branch.json`
  carries a `checkout` — the epic branch of its own seq — and no `target`,
  so every script keeps its default-branch logic: the epic merges into the
  default branch). A conflict the agent cannot resolve is the owner's; the
  default branch moving past verification is merge's FAIL below.
- **build** (`rewind_only`, skill `build`) — the fix build. It never runs on
  the first round (the children's landed work is the change); a gate FAIL or
  an owner's rewind that covers it runs it — inheriting the plan, answering
  every point that sent the lane back, never re-planning.
- **review** (gate → build, skill `review`, the lane's own) — the EPIC mode:
  the total acceptance, the children's landing records and PR bodies
  (`gh pr view`), the seams between the children's work and the final diff
  against the default branch. It never re-audits a child's internals — a
  defect inside one child's own work is a follow-up, unless it breaks the
  epic's total acceptance or a seam. A big problem is `pf step ask` to the
  owner proposing a separate fix sub-ticket (its own lane's contract tests
  and driver tests red before its fix).
- **verification** (gate → build, waivable, skill `verify`) — the explicit
  FULL bar over the whole epic diff; the light tier never runs. A red the
  owner accepts is the gate's `waive`.
- **publish** (gate → build, skill `publish`) — the branch derived from the
  epic branch, its PR against the default branch, the delivery note carrying
  the total acceptance, the children's PR links and the seam resolutions.
- **merge** (gate → sync, skill `merge`) — the PRs merged at the verified
  heads and the LANDING RECORD the epic is wrapped with: `state: landed`
  (the epic enters the integration lane's pool as ONE record), its
  `merge_commits` the actual default-branch landing, its `children` the
  snapshot's landing records — the children keep their own
  `landed_on_target` evidence and never enter the pool themselves. A PR that
  moved, or the default branch that gained commits after verification,
  FAILs back to sync: the sync merges the new default branch in and the
  lane re-runs from there — the review and the full verification included
  (the moved-base class is uncounted; past its own cap of 2 the merge gate
  asks the owner).

## The epic's own mechanics

- **The claim.** The first claim of an epic fixes its checkout branch (the
  children branch substituted with the epic's OWN seq) into
  `runtime-state/target-branch.json` as the record's `checkout` key — no
  `target`: the epic's merge target is the default branch, which every
  reader takes as the change's base exactly as for a work item with no
  fixed target. The same claim snapshots the epic's children (ids, slugs,
  statuses and landing records) into `runtime-state/children.json`,
  immutable while the epic runs.
- **The children.** A sub-ticket of the epic works on the same children
  branch (`children_branch` read from the PARENT's lane — its own claim is
  untouched by the epic key); it syncs and merges into the children branch
  and wraps `landed_on_target`, never into the pool.
- **The running re-check.** Before every dispatch the driver re-checks the
  snapshot against aihub: a child that failed or was canceled, or a child
  added while the epic runs, parks the workflow blocked — no question, the
  owner's next message wakes it. A resume after that re-claims: the changed
  children go into a NEW version of the snapshot and the steps from review
  onward re-run (an owner-class rewind, charged to no budget).
- **The preparation steps.** `verify-children` and `sync` carry no
  `on_fail`: a child anomaly blocks (the daemon's or the step's own
  `blocked` hand-off), a sync conflict the agent cannot resolve is the
  owner's; nothing rewinds into them but the merge gate's moved-base FAIL,
  which returns to sync to re-sync and re-run.

The lane is project-neutral: no text names a project, a repository or a
forge beyond the generic (`gh`, the origin). Its steps are the shared
mechanical skills (`workflows/_shared/skills/`) with the epic's own
definitions (`steps/*.md`) binding them — `review` and `verify-children`
are the lane's own skills. The daemon orchestrates; it judges no content.
