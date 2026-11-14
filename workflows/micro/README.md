# micro — pure-text lane

The flow-level narrative of the lane (workflow contract 1); the `steps` array of its
`workflow.yaml` is the *only* execution order. Nothing here overrides it.

## Shape of the flow

```
fix --> review --> sync --> evidence --> publish --> merge ==> landed
 ^        |                    |            |          |
 +--------+--------------------+------------+          +--> sync
       (FAIL: rewind to fix)                    (a PR that moved or was refused at merge)
```

The lane is for a change that is text and nothing else — documentation, locale message
text, other prose a repository has declared text-only. There is no `grill`, no `plan` and
no `verification` step: **the ticket body is the spec** (`runtime-state/work-item.json`),
`fix` implements it, `review` judges it in a fresh context, `sync` merges the default
branch in, `evidence` re-runs the repository's fast evidence, `publish` opens the PR and
`merge` merges it. A ticket that needs a clarification, a plan, a full bar or an e2e
scenario is not a micro ticket: it belongs on a lane whose step table carries it.

`fix` and `review` run on the `mid` tier; `sync`, `evidence`, `publish` and `merge` are
**mechanical steps** on the `cheap` tier (both tiers are the lane's `defaults.tiers`,
overridden per project in the workspace yaml). `fix` follows the lane's `micro-bounds`
skill and `evidence` the lane's `evidence` skill (`workflows/micro/skills/`); `sync`,
`publish` and `merge` the ones the develop lane uses too (`workflows/_shared/skills/`);
`review` has no skill — its definition is its procedure.

**How the steps connect.** Every step reads `artifacts/handoff.md` first and ends with one
`pf step handoff` (`workflows/_shared/step-header.md`): the daemon stamps each section's
header — status, round, the commits each repo moved through, the owner's words — and the
step writes the body. Each step checks what the step before it handed off before it acts:
a hand-off that does not hold is a gate's `fail`, or a non-gate's question to the owner.
Data a later command needs travels in files, never in a body.

## The repository's own declaration (opt-in, fail-closed)

A repository opts in by declaring one or more `micro_path=<glob>` lines in its own
`.polyforge/verify-scope.conf` (`*` matches within one path segment, `**` crosses `/`, a
pattern without a wildcard is that exact path); it may add `micro_check=<command>` lines,
extra evidence commands run after its fast phase. The platform names no default path, no
repository and no stack. The declaration that governs is the one at the diff base (the
merge-base of HEAD with the fetched origin default branch), never one the change
introduces — a micro change cannot widen its own whitelist. `AGENTS.md` and `CLAUDE.md`
are out of bounds in every directory, whatever a pattern matches.

## The machine boundary (micro-bounds, judged twice)

`fix` writes its evidence plan, `artifacts/fix/evidence.json` — a JSON array `[{repo,
line}]`: per changed repository the commands of `verify-scope --micro --json` (the fast
phase, then every declared `micro_check`), in order — and, before it hands off, runs
`"$PF_PYTHON" "$PF_SHARED_DIR/scripts/facts.py" micro-bounds --plan
artifacts/fix/evidence.json --json --out artifacts/facts/micro-bounds.json` (the
workflow's script). The command judges the diff of every touched repository
against that repository's base declaration and lists per repository the commands the
delivery re-runs. Exit 0 is in bounds: `fix` hands off `done`. **Exit 1 is out of bounds**
— a changed path outside the declaration, a touched repository that declares no
`micro_path`, nothing to re-run — and the change is not this lane's: `fix` asks the owner
(`pf step ask`: the refused paths, the reason, and the suggestion to re-file the work on
the `develop` lane), ends its turn and acts on the reply. When the owner agrees, `fix`
hands off `cancel` citing that reply (the owner files the work anew on `develop` — the lane
files nothing). When the owner instead asks for a narrower change, `fix` reverts what lies
outside, judges again and hands off in bounds. `fix` never hands off `done` out of bounds
and never widens a declaration.

`review` does not trust that judgment: it runs the same command itself (`--out
artifacts/review/micro-bounds.json`) on fix's plan, compares its result with fix's, and a
change out of bounds — or a declared command the plan omits — is a blocking finding. It is
the lane's only independent judgment of the text itself: the diff is the ticket's text
change, nothing more, and changes no behavior.

## Files between steps

| step | primary files it writes | who reads them |
|---|---|---|
| `fix` | changes under `repos/<name>/**`; `artifacts/fix/evidence.json` (the plan), `artifacts/facts/micro-bounds.json` | `review`, `evidence` |
| `review` | `artifacts/review.md` (verdict and numbered findings), `artifacts/review/micro-bounds.json`; the daemon stamps the heads it judged on its section | `sync`, `publish` |
| `sync` | the default branch merged into the branch, `artifacts/sync/result.json` | `evidence`, `publish` |
| `evidence` | every plan line run on the host from its repo's root, `artifacts/run/result.json` | `publish`, `merge` |
| `publish` | the pushed feature branch and its PR per repo, `artifacts/publish.json`, `artifacts/delivery.md` | `merge` |
| `merge` | the PRs merged, `artifacts/merge/result.json`, `artifacts/merge/landing.json` (registered as the wrap's `integration` attr) | the integration lane |

## What each mechanical step owes

- **sync** (no gate; skill `sync`, `sync.py`, tool window 20 minutes) — checks that each
  task repo stands at the heads review judged, or at its own earlier `after` when merge's
  FAIL sent the lane back here; merges the default branch in place, the mechanical
  conflicts resolved by the script, a real conflict by the step's own agent (committed by
  the skill's continue line). `evidence` re-runs the plan over the resolved tree, and publish's
  PR body lists the resolved files verbatim. A real conflict never rewinds by itself: the
  work goes back to `fix` (or the work item ends) only on the owner's word, after
  the skill's abort line.
- **evidence** (gate, `waivable: true`; skill `evidence`, `run.py --isolation host --plan-from`, heavy-run
  admission, tool window 30 minutes) — checks the sync's result (its `argv` the sync
  skill's line), then runs every plan line on the host from its repository's root,
  stopping at the first that fails. Exit 0
  passes; a red the change caused FAILs to `fix`; a red it cannot have caused is the
  owner's call — run again, have it fixed, or accept it (`waive`, citing the owner's
  message). Exit 2 is retried, then `blocked`.
- **publish** (gate; skill `publish`) — checks the evidence's result (or the owner's waiver
  of that same round, quoted on the evidence's section) and review's pass, pushes the
  feature branch, opens or updates the PR, writes `artifacts/publish.json` and
  `artifacts/delivery.md`, and holds for the owner's 「过目通过」 when fix's hand-off
  declares an `owner-review:` line.
- **merge** (gate; skill `merge`, `merge.py`, the per-repo merge lock, tool window 10
  minutes; wrap attrs `integration`) — checks publish's heads against the evidence's heads, merges
  each PR at its verified head under the daemon's merge lock, and on a pass registers the
  landing record the script wrote (`pf step hub wrap-attrs integration
  @artifacts/merge/landing.json`); a PR that moved, closed, was retargeted or was refused
  FAILs back to `sync`. As the lane's last step it writes the work item's wrap summary.

## Human stops

1. **Out of bounds** — `fix`'s micro-bounds exit 1: the owner is asked to re-file on
   `develop`; the owner's agreement is `fix`'s `cancel`.
2. **A sync that is not the agent's to resolve**, or a HEAD that is neither the reviewed
   tree nor the sync's own earlier head.
3. **A red the change cannot have caused** — `evidence` asks and acts on the reply.
4. **A machine that cannot run a step's command** — `facts.py`, `sync.py`, `run.py` or
   `merge.py` exiting 2 after the skill's retries: the step hands off `blocked`; fix the
   machine, then write to the work item (or `pf resume`).
5. **The rewind budget** — below.
6. **Publish's owner-review** — an `owner-review:` line in fix's hand-off holds the
   publication for the owner's 「过目通过」.

## Rewind boundaries

Every gate but `merge` declares `on_fail: {target: fix}`: `fix` is the only step that
changes the tree; `merge`'s FAIL goes back to `sync`, which merges the default branch again.
The lane has 3 rewinds in total (`max_rewinds: 3`, one budget across every gate). A FAIL
citing the owner (`--per-owner`) spends none. Once the budget is spent, `pf step handoff`
refuses a `fail` that cites no owner message: the gate asks the owner — one more round,
accepting the red (`evidence` only: the lane's one `waivable` gate), or cancel. A waived
evidence lands with the heads its red run tested: publish says so in the PR body, and the
daemon carries the owner's words in the wrap's `waivers`.

## Budget guidance

`fix` declares `budget.wall_minutes: 20`, `review` 15 and `publish` 15: the daemon's step
bound T is the larger of its global default and three times the estimate. `sync`,
`evidence` and `merge` declare a tool window only (20, 30 and 10 minutes): each
pre-written command derives its own deadline from the window (two minutes under it) and
ends — its result written — before the harness's shell call and the daemon's supervision
cut it. No workflow-level budget.

## Releases

There is no release slot: releases and deploys happen only through the separate
`release` bundle.
