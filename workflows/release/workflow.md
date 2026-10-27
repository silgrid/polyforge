---
name: release
description: Release lane - cut a release from the verified origin head via the repo's own procedure, independently verify what was published, then deploy exactly that verified version if the project has a deploy procedure.
defaults:
  model_candidates: [pi:example-gateway/model-a, pi:example-claude/claude-opus-5, pi:example-openai/gpt-x]
steps:
  - { id: release, context_policy: fresh, gate: true, requires_command_evidence: true, budget: { wall_minutes: 45 }, model_candidates: [pi:example-gateway/model-a, pi:example-claude/claude-opus-5] }
  - { id: verify, context_policy: fresh, gate: true, requires_command_evidence: true, model_candidates: [pi:example-claude/claude-opus-5, pi:example-openai/gpt-x, pi:example-gateway/model-a] }
  - { id: deploy, context_policy: fresh, gate: true, requires_command_evidence: true, model_candidates: [pi:example-gateway/model-a, pi:example-claude/claude-opus-5] }
---

# release - release lane

This is the flow-level narrative for the workflow. It explains how the three steps
compose; it is not itself a structural schema - the frontmatter `steps` array above is
the *only* execution order. Nothing here overrides it.

This is the ONLY lane that releases or deploys. The delivery lanes (`feature-full`,
`bug-fix`, `autopilot`, `chore`) end at their `deliver` step (push, PR, rule-bound
merge) and never release or deploy; shipping is a separate WI filed against this bundle,
so it always acts on a verified merged origin head and is gated, evidenced and auditable on its own.

The bundle is project-agnostic. The release and deploy procedures belong to the project
repositories (`.polyforge/release.sh`, `.polyforge/deploy.sh`); this lane only
dispatches to them, then checks their result independently.

## Shape of the flow

```
release --> verify --> deploy
```

| step | gate | what it does |
|---|---|---|
| `release` | yes | preconditions, then runs the repo's `.polyforge/release.sh [bump]` |
| `verify` | yes | fresh-context, command-evidenced audit of what `release` claims, then `release.sh --verify <version>` |
| `deploy` | yes | runs `.polyforge/deploy.sh <version> --confirm <version>` for the verified version only; defers when the project has no deploy procedure |

Every step is `gate: true` (fresh context, a `fail` verdict is routed as a gate failure)
and requires command evidence (`requires_command_evidence: true`): a completed result
without recorded commands is refused.

## Artifact flow between steps

No step declares frontmatter `inputs:`; each step reads the upstream artifact by its
path convention under `artifacts/`:

| step | writes | read by |
|---|---|---|
| `release` | `artifacts/release-record.json` (version, release commit, default branch, pre-release remote tag list, notes location) | `verify` (by path) |
| `verify` | `artifacts/verified-version.json` (only on pass: the verified version) | `deploy` (by path) |
| `deploy` | the step output (which version is serving, or the rollback report) | (terminal) |

`deploy` never resolves "latest" and never reads `release`'s claims: its only version
input is the file `verify` wrote after its checks passed.

## Failure policy: no rewinds into a re-release

No step declares `on_fail`, so a FAIL verdict terminates the workflow as a gate failure
(`failed_gate`). This is deliberate. Published tags, releases and images are immutable:
rewinding `verify` or `deploy` back into `release` would try to publish a second
release for the same intent, either colliding with the claimed version or silently
burning a new one. A failed release lane therefore stops, with the failing step's
`fail_reason` and command evidence as the record, and a human decides: re-file a new
release WI (a new version) for NEW code, or repair the procedure first. In particular:

- `release` FAIL: nothing, or only part of a release, was published. The reason says
  exactly what exists; nothing is ever deleted or overwritten automatically.
- `verify` FAIL: a release exists but did not pass the audit; `deploy` is never reached.
- `deploy` FAIL: either the procedure refused before touching production (production is
  untouched), or it rolled back (production is on the previous version and safe), or
  the rollback itself failed (loud manual instructions are in the reason). Any of these
  needs a human to look; the WI ends failed, not "done".

### Redeploying after a failed deploy

A failed `deploy` ends the WI failed, and production is on the OLD version (the
procedure rolled back automatically, or refused before touching it). The release itself
is already published and verified, so fixing the cause (the environment, not the code)
and deploying again is a deploy-only action: a human or an agent runs the project's
procedure directly, `.polyforge/deploy.sh <version> --confirm <version>`, for the
already-verified version. Never file a new release WI for that: a second release of the
same commit would cut a duplicate version, and a project's release procedure may refuse
it outright. A new release WI is only for new code.

## Budget guidance

Only `release` declares a wall: 45 minutes (it runs the repo's full test gate, builds and
publishes). The declared wall is an estimate, not a limit: the daemon bounds a step's total
runtime across its dispatches at T = max(3 x its `budget.wall_minutes`, the daemon's
`--step-timeout`, default 90 minutes), so `release` gets 135 minutes. `verify` and `deploy`
declare none (an estimate of 30 or less is inert under that rule) and get the 90-minute default.

## Procedure contract (what a repo provides)

- `.polyforge/release.sh [bump]` - releases the origin default-branch head and exits
  non-zero on any failure; on success prints `version=<tag>`, `commit=<sha>` and
  `notes=<path or url>` lines. It must support `--verify <version>`, a read-only audit of
  a published release (format, ordering, artifacts, integrity) that exits 0 only when
  everything checks out.
- `.polyforge/deploy.sh <version> --confirm <version>` - deploys that published version
  and ends with production either on that version or rolled back to the previous one;
  a rollback must exit non-zero.
- Both procedures run on the step's own machine, against the task-repo clone. They read
  LOCAL git tags (the steps run `git fetch --tags origin` first), and whatever else a
  repo's procedure needs on that machine (tools, registry and ssh access, writable
  backup space) is the repo's precondition: the repo documents it next to its scripts,
  and a missing one fails the step gracefully, but only after a lane was spent - check
  them before filing a release WI.
