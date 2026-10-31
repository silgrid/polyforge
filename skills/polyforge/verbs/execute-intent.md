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

1. Pick the lane with the owner. The filing proposal fills three mandatory
   slots before `pf file create` — `Lane + reason`, `Tier + reason` and, when
   the tier is `scoped`, `Entry confirmation`. Answer all three; do not file on
   prose alone.
   - **Lane + reason** — the chosen `--bundle` and one line why; the owner may
     veto or re-route. Apply the filing skill's `Down-lane check` first — judge
     `expected diff size` and `lane risk surface`; a small, low-risk ticket
     prefers `chore`/`autopilot` over `feature-full`. Shipped lanes:
     `bug-fix` (fix a failure: repro →
     root-cause → fix → review → verification → deliver), `feature-full` (new
     user-visible behavior: grill → spec → grill-review → plan → e2e-author →
     code → review → verification → deliver), `chore` (internal maintenance
     with no new user-visible behavior — tools, docs, config, cleanup,
     deletion: plan → code → review → verification → deliver), `autopilot`
     (small well-understood changes: grill → fix → verification → deliver).
     `release` is the separate release lane — see the lane table in
     `verbs/file.md`. Every lane except `feature-full` can merge unattended, so
     work on `docs/concepts.md`'s "never unattended" list is filed
     `feature-full`. A project may carry its own bundles — ask when unsure.
     Verification tier capability: `bug-fix`, `feature-full` and `chore` are
     `scoped-capable`; `autopilot` always verifies `full` (it has no review, so
     no `scope_adequate`) and is not scoped-capable; `release` has no
     verification step.
   - **Tier + reason** — `scoped` or `full` and one line why, by the same
     heuristic as the filing skill: propose `scoped` for a text/docs change
     confined to one repository and a small surface; propose `full` when the
     change crosses modules or repos, touches the driver/daemon core, or is
     gate-type (a `gate: true` step definition — `verification`, `review`,
     `deliver` — or the driver/daemon logic behind it); when you
     cannot determine the tier, propose `full` (fail-closed). The owner may veto a
     downgrade and insist on `full`. The tier is an opt-in marker
     (`verification: scoped` in the ticket `content`), and a scoped-capable
     lane still needs the release tree's `tools/verify-scope` engine plus a
     scoped-ready repo (a supported stack manifest, or a
     `.polyforge/verify-scope.conf` pin).
   - **Entry confirmation** — required whenever the tier slot proposes
     `scoped`: confirm readiness before filing with the `pf doctor`
     `scoped-gates readiness` row (engine default / repo conf = `pass`,
     unsupported or unpinned multi-manifest = `warn`, no local clone =
     `unknown`), or `pf projects get` plus a look at the clone's
     manifests/conf; if it is not ready the marker is inert and the ticket
     **falls back to full** — the contract is
     docs/contributing.md『Verification tiers』.
2. **Deepening check** — for an execution ticket, deepen the ask before
   filing it: a prompt becomes a question a top practitioner would answer.
   Deeper, not bigger — sharpen the existing ask, add no scope. Put the four
   results in the filing proposal and land them in the ticket `content`:
   - **expert gap** — what a top practitioner would do here that the owner
     did not ask for; it must be backed by the point-② executable references,
     otherwise it is expert theater. Lands in the `content` background.
   - **raw material** — the rawest starting material (log paths, commit ids,
     command output, source files), never a paraphrase or a second-hand
     summary. Lands in the `content` repro pointers.
   - **methods and tools** — the deterministic methods and tools verification
     will use.
   - **external oracle** (外部神谕) — what fact *other than the executor or the
     requester* shows the result is right; it must map to a `falsifiable`
     `verification` check, never a self-certifying "it looks consistent"
     statement. Points ③ and ④ land in the `content` acceptance.
   An execution ticket's deepening is present when its `content` carries the
   two sections **gap and oracle** — the gap rides the background, the oracle
   the acceptance. A pure registry / design / tracking
   ticket (no bundle) is a **light ticket** and is exempt.
3. `pf file create --project P --bundle <lane> --goal "<one line>"` — the
   bundle is REQUIRED: a registration ticket (no bundle) is not claimable
   (CORE-409). Background goes in `--content`, the clone scope in `--repo`,
   dependencies in `--after` (full shape: `verbs/file.md`); the deepening
   results from step 2 land in `--content`.
4. `pf submit <id>` (`verbs/submit.md`) — run its placement precheck first:
   resolve the target workspace from the `[workspace."<path>"]` sections of
   cli.toml and the repo set from the ticket's `repo_inventory` (else the
   project's repos); ask the owner to confirm before submitting when the
   target workspace differs from the session cwd or a repo clone is
   missing under `<workspace>/.repo/<name>` ("this ticket will land in
   X": machine + workspace), and submit only after the owner agrees.
   Then watch and answer needs_input (`verbs/answer.md`); the deliver
   step's two questions - the merge question (`deliver-merge`) and the
   owner-review question (`deliver-owner-review`) - are human-only
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
