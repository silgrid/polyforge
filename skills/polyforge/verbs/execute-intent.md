# execute-intent — owner-directed code work routes into the lifecycle

An execution intent: the owner directs code work on a project repo — change
it, upgrade a library, fix a bug, add a capability. The session agent never
performs that work. Its whole action is routing it into the polyforge
lifecycle: an execution ticket (`pf file create --bundle <lane>`) → `pf
submit` → the daemon claims it, clones the repos into its capsule and the Go
driver runs the lane's steps; you observe (`pf watch`) and answer needs_input
with the owner's own words.

## Triggers

- EN: upgrade X / bump the dependency / fix the bug in X / change X in the
  repo / make it do Y / refactor X / add Z
- 中文: 升级 X 库 / 升级一下依赖 / 修一下这个 bug / 改一下仓里的 X / 让它支持 X / 重构一下 X

NOT this fragment: machine setup or upgrading polyforge itself →
`verbs/init.md`; cutting a release / deploying a version → the
release-bundle route (`verbs/file.md`, `--bundle release`); "run it / submit
it" of an existing work item → `verbs/submit.md`.

## The routing action

1. Pick the lane with the owner when unclear. Shipped lanes: `bug-fix`
   (fix a failure: repro → root-cause → fix → verification → review →
   deliver), `feature-full` (new behavior, spec and acceptance scenarios),
   `chore` (deletion / cleanup), `autopilot` (small well-understood changes).
   A project may carry its own bundles — ask when unsure.
2. `pf file create --project P --bundle <lane> --goal "<one line>"` — the
   bundle is REQUIRED: a registration ticket (no bundle) is not claimable
   (CORE-409). Background goes in `--content`, the clone scope in `--repo`,
   dependencies in `--after` (full shape: `verbs/file.md`).
3. `pf submit <id>` (`verbs/submit.md`) — then watch and answer needs_input
   (`verbs/answer.md`); the deliver step's merge question is human-only
   (`verbs/ship.md`).

## The discipline: routing, not executing

Before any `git branch` / `git commit` / `git push` / `gh pr create`, and
before any Edit/Write under `.repo/`, ask: **which execution ticket does
this change hang on?** No answer → file one first (with a bundle) and route
the work through it. Write operations happen only inside the daemon's
claimed work-item worktree (the capsule) — a session that hand-executes
bypasses every gate: no root-cause, no verification, no review, no deliver
record (2026-10-20: a session hand-upgraded a library end-to-end — branch,
tests, commit, push, PR — with no ticket until after the PR; the daemon had
correctly refused the bundle-less ticket, and nothing loaded told the
session not to).

The exceptions are delivery mechanics, not execution: merging a
ship-produced PR and resolving its conflicts in the workspace clone stays on
session rule 6's named-WI path (ruling 2, 2026-10-10); execution work never
takes that path.

## Failure branches

Routing failures are the verbs' own: create (`verbs/file.md`) and submit
(`verbs/submit.md`). A refusal — a bundle-less ticket refused, a project
not in the mapping, the daemon down — is never a reason to hand-execute
instead: fix the routing (bind the bundle, add the mapping, `pf daemon
ensure`) or ask the owner.
