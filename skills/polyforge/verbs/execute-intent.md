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

1. Pick the lane with the owner. The filing proposal fills two mandatory
   slots before `pf file create` — `Lane + reason` and `Acceptance carrier`.
   Answer both; do not file on prose alone. A lane runs the verification
   its steps declare; a work item chooses no tier.
   - **Lane + reason** — the chosen `--bundle` and one line why; the owner may
     veto or re-route. Apply the filing skill's `Down-lane check` first — judge
     `expected diff size` and `lane risk surface`; with `develop` the default, a
     small, low-risk pure text ticket whose every touched repository declares
     `micro_path=` covering its paths may go to `micro`, but it
     never down-lanes onto a lane whose step table carries no step for the
     acceptance criteria. Shipped lanes: `develop` — **the default** for
     development work (build → review → verification → publish: one build session
     plans against the ticket's `## Acceptance`, writes each unit's test red before
     its implementation and reviews itself; review blocks only on its five classes;
     a ticket without `## Acceptance` stops at build for the owner's list) — and
     `micro` (a pure text change — docs or locale message text, no code,
     config, generated file, test or behavior change — inside the paths every
     touched repository declares with `micro_path=` in its own
     `.polyforge/verify-scope.conf`: fix → review → sync → evidence → publish
     → merge, no verification step, the ticket body as its spec; never for a repository without that
     declaration nor for work on the "never unattended" list, and a criterion
     only a full bar, an e2e scenario or a verification step carries makes the
     ticket ineligible for it).
     `release` is the separate release lane — see the lane table in
     `verbs/file.md`. Every delivery lane can merge unattended, so
     work on `docs/concepts.md`'s "never unattended" list is filed `develop` with
     the content line `confirm_plan: true` (build stops for the owner after its
     plan; the driver refuses the build's completion until that answer is
     recorded). A project may carry its own
     bundles — ask when unsure.
   - **Acceptance carrier** — every acceptance criterion is written
     `<criterion> — carried by <step id>`, the step id taken from the chosen
     lane's step table (the `steps:` list of its `workflow.yaml`; the lane
     table in `verbs/file.md` names the lanes). A criterion that names no
     step either drops to the evidence that graph actually runs, recorded as
     an explicit waiver proposed to the owner, or the ticket moves to a lane
     whose step table carries it.
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
     Any read of `<workspace>/.repo/<repo>` that feeds this proposal is preceded by
     the session clone sync: SKILL.md session rule 8.
   - **methods and tools** — the deterministic methods and tools verification
     will use.
   - **external oracle** (外部神谕) — what fact *other than the executor or the
     requester* shows the result is right; it must map to a `falsifiable`
     `verification` check and be carried by a step of the chosen lane's step table
     (the `Acceptance carrier` slot), never a self-certifying "it looks
     consistent" statement. Points ③ and ④ land in the `content` acceptance.
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
   Then watch and answer needs_input (`verbs/answer.md`); the publish
   step's owner-review question (its `pf step ask`) is human-only
   (`verbs/ship.md`), and so are a develop build's stops (its `pf step ask`
   after the plan: the owner answers in free text that the build reads).

## The discipline: routing, not executing

Before any `git branch` / `git commit` / `git push` / `gh pr create`, and
before any Edit/Write under `.repo/`, ask: **which execution ticket does
this change hang on?** No answer → file one first (with a bundle) and route
the work through it. Write operations happen only inside the daemon's
claimed work-item worktree (the capsule) — a session that hand-executes
bypasses every gate: no root-cause, no verification, no review, no delivery
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
