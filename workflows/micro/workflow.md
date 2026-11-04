---
name: micro
description: Pure-text lane - fix → review → deliver, no grill, plan or verification step; the ticket body is the spec, the change must stay inside the paths each touched repository declares text-only (micro_path= in its .polyforge/verify-scope.conf), and the delivery's re-run of the repository's fast evidence is the merge credential. It can merge unattended.
rewind:
  max_total: 2
  max_per_step: 1
defaults:
  model_candidates: [pi:example-gateway/model-a, pi:example-claude/claude-opus-5, pi:example-openai/gpt-x]
steps:
  - { id: fix, context_policy: inherit, recall: true }
  - id: review
    context_policy: fresh
    gate: true
    on_fail: { target: fix }
    vars:
      build_step: fix
  - { id: deliver, context_policy: fresh, gate: true, requires_command_evidence: true, on_fail: { target: fix } }
---

# micro — pure-text lane

This is the flow-level narrative for the workflow. It explains how the three steps
compose and what flows between them; it is not itself a structural schema — the
frontmatter `steps` array above is the *only* execution order. Nothing here overrides it.

## Shape of the flow

```
fix --> review --> deliver
 ^        |          |
 +--------+----------+
     (FAIL: rewind to fix)
```

The lane is for a change that is text and nothing else — documentation, locale message
text, other prose a repository has declared text-only. There is no `grill`, no `spec`,
no `plan`, no `e2e-author` and no `verification` step: **the ticket body is the spec**
(`runtime-state/work-item.json`), `fix` implements it, `review` judges it in a fresh
context, and `deliver` merges it. A ticket that needs a clarification, a plan, a full
bar, an e2e scenario or any guarantee only a verification step carries is not a micro
ticket: it belongs on a lane whose step table carries it.

## The repository's own declaration (opt-in, fail-closed)

A repository opts in by declaring one or more `micro_path=<glob>` lines in its own
`.polyforge/verify-scope.conf` (`*` matches within one path segment, `**` crosses `/`, a
pattern without a wildcard is that exact path); it may add `micro_check=<command>` lines,
extra evidence commands run after its fast phase. The platform names no default path, no
repository and no stack. The declaration that governs is the one at the diff base (the
fetched origin default branch), never one the change introduces — a micro change cannot
widen its own whitelist. `AGENTS.md` and `CLAUDE.md` are out of bounds in every directory,
whatever a pattern matches.

## The machine boundary (driver-enforced, terminal)

After `fix` completes and before `review` is dispatched, the driver judges the diff of
every touched repository (merge-base with the fetched default branch to HEAD, both sides
of a rename or deletion) against that repository's own base declaration. A changed path
outside it, a touched repository that declares no `micro_path`, or a diff, base or conf
the driver cannot read ends the run `failed_gate` — no rewind to `fix`, no merge, no human
merge question, no rewind slot charged. The reason (`termination_reason` in
`execution.json`, the `fast_lane_out_of_bounds` event and the failed close's hub note)
names the paths and says to re-file the work on the `autopilot` lane (the run has already
ended: no question is open, so no `cancel` answer is needed). In bounds, the
driver records the verdict and the delivery's credential plan on `fix`'s output as
`fast_lane`: per repository its base, changed and declared paths, and the commands the
delivery must re-run — `fix`'s reported fast evidence followed by every declared
`micro_check` it omitted. `review` is never the out-of-bounds authority.

## Artifact flow between steps

| step | primary artifact it writes | who reads it |
|---|---|---|
| `fix` | changes under `repos/<name>/**`, its output (`changed_paths`, `evidence`, `summary`); the driver adds `fast_lane` | `review`, `deliver` |
| `review` | `artifacts/review.md` (verdict); the driver records `reviewed_heads` on a pass | `deliver` |
| `deliver` | the pushed feature branch, PR and merge outcome per repo, `artifacts/delivery.md` | (terminal) |

## Delivery and the merge credential

`deliver` is the shared step definition `workflows/_shared/steps/deliver.md`. This lane
has no verification step, so its branch for a verification-less lane applies: the
delivery re-runs every `fast_lane` command of each repository itself, one by one in
`repos/<name>/`, after its last merge of the default branch and before that repository's
`gh pr merge`, each with exit 0 — never through `pf bar-run`, and evidence from `fix` or
an earlier dispatch never counts. The PR merges without a human when the review passed
with no blocking finding and its recorded head covers the delivered HEAD (only
git-reproducible merges in between), that re-run is green, and the existing delivery facts
hold. Nothing else stands in: no verification record, `verified_heads`, test bar or
scenario record exists or is fabricated on this lane. A red, missing or skipped command
blocks the merge and returns the delivery to `fix`. A declared `owner-review:` artifact
still makes deliver ask `deliver-owner-review` first; an unmet merge condition still asks
`deliver-merge`. Those are the only human exits: no step declares
`requires_human_answer`, so a run that asks nothing merges unattended.

## Rewind boundaries

Both gates (`review`, `deliver`) declare `on_fail: {target: fix}`: `fix` is the only step
that changes the tree. Workflow-level rewind bounds are `max_total: 2`, `max_per_step: 1`
— one charged slot per gate→`fix` edge, so a second charged FAIL on the same edge ends the
workflow `failed_rewind_exhausted`. An owner-routed rewind (a `deliver` FAIL a human
answered) charges neither bound. A text change that cannot land in one repair per gate is
not a micro change; re-file it on a heavier lane.

## Budget guidance

No workflow-level budget and no step `budget.wall_minutes`: the bounds are the rewind
limits above and the daemon's per-step runtime bound T.

## Releases

There is no release slot: releases and deploys happen only through the separate
`release` bundle.
