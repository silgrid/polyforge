# release - release lane

This is the flow-level narrative for the workflow (workflow contract 1). It explains how
the three steps compose; it is not itself a structural schema - the `steps` array of its
`workflow.yaml` is the *only* execution order. Nothing here overrides it.

This is the ONLY lane that releases or deploys. The delivery lanes (`develop`, `micro`)
end at their daemon `merge` step (after `publish`'s push and PR)
and never release or deploy; shipping is a separate WI filed against this bundle,
so it always acts on a verified merged origin head and is gated, evidenced and auditable on its own.

The bundle is project-agnostic. The release and deploy procedures belong to the project
repositories (`.polyforge/release.sh`, `.polyforge/deploy.sh`); this lane only
dispatches to them, then checks their result independently.

## Shape of the flow

```
release --> verify --> deploy
```

| step | gate | checks first | resources | budget | tier | what it does |
|---|---|---|---|---|---|---|
| `release` | yes | — (the ticket names a bump, or none) | heavy admission | tool 120 min / wall 135 min | `mid` | preconditions, then runs the repo's `.polyforge/release.sh [bump]` itself |
| `verify` | yes | `release-record.json` parses and names V and its tag's commit | — | wall 30 min | — (strong) | fresh-context audit of what `release` claims, by its own commands, then `release.sh --verify <version>` |
| `deploy` | yes | `verified-version.json`'s V equals `release-record.json`'s version | heavy admission | tool 30 min | `mid` | runs `.polyforge/deploy.sh <version> --confirm <version>` itself, for the verified version only; deploys nothing (and says so) when the project has no deploy procedure |

**Downstream checks upstream.** Every step reads `artifacts/handoff.md` first and ends
with one `pf step handoff` (`workflows/_shared/step-header.md`). None of the three has a
skill: each step definition says what the step reads and checks before it starts and what
its hand-off carries. A check that does not hold is the gate's FAIL, which ends the lane
like any other (no `on_fail`). The version each step checks is read from a file, never
from a hand-off body: `deploy` checks `verify`'s `artifacts/verified-version.json` against
the release step's own record (`artifacts/release-record.json`'s `version`), so its
version check rests on two independent records, never on `verify`'s file alone.

**Tiers.** `release` and `deploy`, which run a procedure and report it, name the `mid`
tier (`model_tier: mid`), one of the two placeholder tiers every shipped lane declares
(`cheap`, `mid`): their models come from the workspace yaml — the step's own
`steps.<project>.<step-id>.models`, then `tiers.<project>.mid.models` — else the project
default (a placeholder never outranks it). `verify`, the lane's judgment gate, is a
strong step and names **no** tier: its models are the step's own key or the project
default — the strong model — never a tier list.

Every step is `gate: true` (fresh context, a `fail` hand-off is routed as a gate failure)
and none is waivable (`waivable: false`): a FAIL ends the work item and there is no
waiver to hand off. Each step's agent runs its commands — the checks,
the git and gh calls, the procedure — itself, in the owner repo, and reports what they
printed; the daemon records no command and judges none. The two procedures run inside
ONE tool call, under the step's own tool window (`budget.tool_minutes`: 120 minutes for
`release`, sized for a procedure that runs the full test gate itself; 30 for `deploy`),
under the step's own `PATH` — the daemon's command PATH, the one its own in-process
commands run under — and the output teed into the capsule (`artifacts/release.log`,
`artifacts/deploy.log`). `release` and `deploy` declare `resources: { admission: heavy }`:
the daemon takes a heavy-run admission slot (the queue its verification runs wait in)
before the step's process starts and gives it back when the process is gone, so a
release waits for running verifications and no verification starts beside the procedure.

## Artifact flow between steps

Each step reads the upstream file by its path convention under `artifacts/`:

| step | writes | read by |
|---|---|---|
| `release` | `artifacts/release-record.json` (version, release commit, default branch, pre-release remote tag list, notes location, resumed) | `verify` (by path), `deploy` (its `version`, as a check) |
| `verify` | `artifacts/verified-version.json` (only on pass: the verified version), `artifacts/release-audit.md` (each check and its evidence) | `deploy` (by path; checked against the release record's version) |
| `deploy` | its hand-off (which version is serving, or the rollback report), `artifacts/deploy.log` | (terminal) |

`deploy` never resolves "latest" and never takes a version from a hand-off body: its only
version input is the file `verify` wrote after its checks passed. A project without a
deploy procedure hands off `done` with nothing deployed, saying so.

## Failure policy: no rewinds; a claimed version resumes, never burns

No step declares `on_fail`, so a `fail` hand-off terminates the workflow as a gate failure
(`failed_gate`). This is deliberate. Published tags, releases and images are immutable:
rewinding `verify` or `deploy` back into `release` would try to publish a second
release for the same intent. A failed release lane therefore stops, with the failing
step's hand-off, quoting what its commands printed, as the record. Retrying is a property of the
`release` step, not a rewind: a version is claimed once (its tag on origin's head) and
never burned. Filing the release WI again, once the cause is fixed, RESUMES the version
on origin's head: a complete release is adopted without running the procedure, a
partial one gets only its missing parts, and no second version of that head is ever
cut. A new version needs new code on the default branch. (A step that needs the owner
asks with `pf step ask` and ends its turn; an owner answer that calls the release off is
handed off as `cancel` citing it, which ends this work item without a failure.) In
particular:

- `release` FAIL: nothing, or only part of a release, was published. Its hand-off says
  exactly what exists; nothing is ever deleted or overwritten automatically. A failure
  before the claim (a preflight, a gate) consumed no version; one after it left the
  version claimed and resumable.
- `verify` FAIL: a release exists but did not pass the audit; `deploy` is never reached.
- `deploy` FAIL: either the procedure refused before touching production (production is
  untouched), or it rolled back (production is on the previous version and safe), or
  the rollback itself failed (loud manual instructions are in its hand-off). Any of these
  needs a human to look; the WI ends failed, not "done".

### Redeploying after a failed deploy

A failed `deploy` ends the WI failed, and production is on the OLD version (the
procedure rolled back automatically, or refused before touching it). The release itself
is already published and verified, so fixing the cause (the environment, not the code)
and deploying again is a deploy-only action: a human or an agent runs the project's
procedure directly, `.polyforge/deploy.sh <version> --confirm <version>`, for the
already-verified version. Filing the release WI again is the lane route to the same
place: its `release` step adopts the complete release (no procedure runs, no second
version is cut), `verify` audits it again and `deploy` deploys it.

## Budget guidance

`release` declares `budget: { wall_minutes: 135, tool_minutes: 120 }`: the tool window is
the bound on one tool call — the procedure's run, which includes the repo's full test
gate, the build and the publish — and rides the step process as `PF_TOOL_WINDOW_MS` (a
claude step's Bash default timeout equals it); the wall is an estimate, not a limit, and
has to hold the window (`tool_minutes` ≤ `wall_minutes`). The daemon bounds a step's
total runtime across its dispatches at T = max(3 x its `budget.wall_minutes`, the
daemon's `--step-timeout`, default 90 minutes), so `release` gets 405 minutes. `verify`
declares a 30-minute wall (inert under that rule: the 90-minute default applies) and no
window (the daemon's global one). `deploy` declares a 30-minute window and no wall
(the 90-minute default).

A process killed mid-procedure (a daemon stop, a kill -9) takes the procedure with it;
nothing is resumed by the daemon. The step's next dispatch starts from section 3 of its
text: it reads what was published and resumes the claimed version — the procedure's own
contract — never cutting a second one.

## Procedure contract (what a repo provides)

- `.polyforge/release.sh [bump]` - releases the origin default-branch head and exits
  non-zero on any failure; on success prints `version=<tag>`, `commit=<sha>` and
  `notes=<path or url>` lines. On a head that already carries its release tag it
  RESUMES that version: it checks what is published, keeps it, publishes only what is
  missing and reports that version (a procedure without resume refuses instead, and the
  step reports `PF-RELEASE-INCOMPLETE`). It must support `--verify <version>`, a read-only audit of
  a published release (format, ordering, artifacts, integrity) that exits 0 only when
  everything checks out.
- `.polyforge/deploy.sh <version> --confirm <version>` - deploys that published version
  and ends with production either on that version or rolled back to the previous one;
  a rollback must exit non-zero.
- `.polyforge/deploy.sh --preflight` (optional) - a read-only check of the deploy
  machine and target that reports every problem in one message and exits 0 only
  when the deploy could run. When it exists, the `release` step runs it before
  anything is published (`PF-RELEASE-PREFLIGHT` on failure), so a deploy that
  cannot run never follows a freshly claimed version.
- Both procedures run on the step's own machine, by the step's own agent, against the
  task-repo clone, under the daemon's command PATH (the step's own `PATH`: `[daemon]
  command_path`, the daemon's own PATH when unset). They read
  LOCAL git tags (the steps run `git fetch --tags origin` first), and whatever else a
  repo's procedure needs on that machine (tools, registry and ssh access, writable
  backup space) is the repo's precondition: the repo documents it next to its scripts,
  and a procedure checks its own preconditions before it publishes anything (the
  release procedure before its gates, the deploy procedure in `--preflight`), so a
  missing one costs a lane but never a version.
