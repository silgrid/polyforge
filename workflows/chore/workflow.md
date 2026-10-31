---
name: chore
description: Deletion-type maintenance lane - plan → code → review → verification → deliver; deletions and cleanup ship through this lane. No release slot.
rewind:
  max_total: 9
  max_per_step: 3
defaults:
  model_candidates: [pi:example-gateway/model-a, pi:example-claude/claude-opus-5, pi:example-openai/gpt-x]
steps:
  - { id: plan, context_policy: fresh, handoff: true, recall: true }
  - { id: code, context_policy: inherit, recall: true }
  - id: review
    context_policy: fresh
    gate: true
    on_fail: { target: code }
    vars:
      build_step: code
      scope_source: the WI and the plan
      intro: |-
        Judge whether this maintenance/deletion change is ready to ship. Unlike
        `verification`, this step is a judgment call, not a mechanical check — and on this
        lane it carries the same fixed owner-rules checklist the other delivery lanes'
        reviews carry, so a deletion-type change cannot dodge the rules by riding the light
        lane.
      judge: |-
        1. Read, in this order: `artifacts/plan.md` (the declared units and touched files),
           the full diff (in each task repo `git diff $(git merge-base HEAD origin/HEAD)..HEAD`,
           falling back to `origin/main` or `origin/master` when `origin/HEAD` is unset — this
           includes any commit `code` did not report), and `artifacts/code-summary.md` (the
           code step's own account of what it did and why). The machine gate
           (`verification`) runs after you: you have no verification report to read —
           judge the diff and the plan conformance themselves.
        2. Judge plan conformance in both directions from the plan-conformance facts
           section of this prompt — the driver computed the set differences, do not
           reconstruct them. In EVERY round, whatever the round contract says about new
           discoveries: a changed file the plan did not declare and no review finding
           cites is blocking; a declared coupled obligation the diff
           leaves unchanged is blocking; a declared plain write left unchanged is
           non-blocking. A file `code` added after a FAIL counts as declared only when its
           `declared_extensions` entry cites what requires it (a review finding
           `s<n>#<k>`, a verification FAIL `s<n>`, a human deliver answer) — judge
           whether it really does. Judge too whether the
           plan's coupled obligations are the right ones (the repo's `AGENTS.md` names
           the project's known couplings; one it names that is missing is blocking), that
           the diff covers the plan's declared steps, and that a declared deletion is
           actually complete — no half-removed feature left compiling against ghosts of
           itself.
        3. Judge the deletion-type acceptance bar: the existing test suite stays meaningful
           after the deletion, and the few targeted tests for behavior the deletion actually
           changes exist where the project expects tests. A full acceptance suite or an e2e
           lane is NOT this lane's bar — demanding one here is scope creep, not rigor. A test
           that asserts nothing real (vacuous) is a blocking finding; a redundant one is
           non-blocking.
  - id: verification
    context_policy: fresh
    gate: true
    requires_command_evidence: true
    plan_conformance: true
    on_fail: { target: code }
    model_candidates: [pi:example-gateway/model-a, pi:example-claude/claude-opus-5]
    vars:
      output_required: verdict, checks
      build_step: code
      position: >-
        `review` already judged the change before you (this lane's order is code →
        review → verification → deliver), and there is no
        release slot behind you: the full set runs here or not at all. This lane is
        scoped-capable (the scoped tier): a work item whose content carries the marker
        line `verification: scoped` runs this step scoped — declare the `scope` object
        with exactly the `declared_paths` review recorded (`steps.review.output.scope`)
        and keep every lane check; without the marker it runs full and carries no `scope`.
      lane_checks: |-
        **Plan conformance.** Read the plan-conformance facts section of this prompt: the
        driver compared `artifacts/plan.md`'s declared set (writes, their coupled
        obligations, finding-cited extensions) with the diff in both directions. A changed-but-undeclared file with no cited finding, or a declared
        coupled obligation left unchanged, fails this check even when every other check
        passes; a declared plain write left unchanged is recorded, not failed. When the
        section says the diff could not be read, check the diff yourself. Record this as
        a `plan-conformance` check.
      never_pre_existing: any test the plan names as pinning this change, and any repro check
  - { id: deliver, context_policy: fresh, gate: true, requires_command_evidence: true, on_fail: { target: code } }
---

# chore — deletion-type maintenance flow

This is the flow-level narrative for the workflow. It explains how the five
steps compose; it is not itself a structural schema — the frontmatter `steps` array above
is the *only* execution order. Nothing here overrides it.

## Shape of the flow

```
plan --> code --> review --> verification --> deliver
          ^         |            |              |
          +---------+------------+--------------+
                    (FAIL: rewind to code)
```

The lane covers deletion-type and maintenance work: dead-code and feature removal,
cleanup sweeps, dependency bumps, tooling and lint config updates, mechanical
refactors, doc sweeps, test hygiene. The bundle chosen at filing IS the
classification — filing a WI on `chore` declares it a deletion-type change (no new
label or field is added later), and filing a removal the work item names on `chore` is
the owner's ruling for that removal: review does not block it as an unruled
destination change, while a removal or guarantee weakening the work item does not name
still blocks, and `feature-full` is NOT trimmed by change type, so
a pure-deletion ticket does not belong there (feature-full's `grill` probes that
mismatch and asks the owner). Deletion-type acceptance on this lane is deliberately
light: the existing test suite green plus a few targeted tests for any behavior the
deletion actually changes — no `e2e-author`, no full acceptance suite. The lane is
deliverable: its changes ship like any other delivery lane's, merging as ordinary
commits on a reviewed, verified tree.

Its two defining properties:

- **Review is the owner-rules gate.** `review` carries the same fixed owner-rules
  checklist and shared principles as `bug-fix` and `feature-full` (no faked
  concurrency semantics, no unrequested concepts, no manual-gate bypass, no unruled
  behavior change, among others) on top of plan conformance — a deletion-type change cannot dodge the rules by riding the light
  lane. If a "chore" turns out to need genuine design or spec work rather than
  removal judgment, it was misrouted: the step that finds it asks (`needs_input`), and
  the owner answers with the reserved `cancel` (it ends this work item) and re-files on
  `feature-full`.
- **No release slot.** Deliverable does not mean self-releasing: the lane's changes
  merge as ordinary commits and ride the project's next release train; the release
  hook belongs to whichever WI carries that release (releases happen only via the
  dedicated `release` bundle, which this bundle never includes).

## Artifact flow between steps

Every step's output is a path recorded in `execution.json`'s `steps.<id>.output`, pointing at a
file under `artifacts/`; no step declares frontmatter `inputs:`, so downstream steps read upstream artifacts by that convention:

| step | primary artifact it writes | who reads it |
|---|---|---|
| `plan` | `artifacts/plan.md` (may legitimately be a single trivial step for a small chore) | `code` |
| `code` | changes under `repos/<name>/**`, `artifacts/code-summary.md` | `review`, `verification` |
| `review` | `artifacts/review.md` (verdict) | `verification` (its scope record, on the scoped tier), `deliver`, (rewind context for `code` via `pending_rewind_input`) |
| `verification` | `artifacts/verification-report.md` (verdict) | `deliver` (verified heads, test bar) |
| `deliver` | `artifacts/delivery.md` | (terminal) |

## `plan` is a normal step, not an optional one

The schema has no optional-step semantics, and this bundle does not invent any: `plan`
is a full step that always runs. Its contract is *proportionality* instead — for a
one-line dependency bump, a plan listing exactly one trivial step is the correct,
complete plan; for a sweep across many files, the plan enumerates the units so `code`
can keep the tree green between commits. Skipping the thinking because the chore is
small is not an option; writing the small thought down is cheap.

## Rewind boundaries

All rewind declarations live in the frontmatter `on_fail` of the gates; a target must
exist and sit strictly earlier than the failing step. One exception: a scoped `deliver`
answered 「全量重验」 overrides its `on_fail` with `rewind_to: verification` (full tier).
This section explains the choices:

- **`review` FAILs -> rewind to `code`.** The owner-rules checklist or the
  plan-conformance judgment found the change wanting (undeclared writes, a
  half-finished deletion, a rule hit); `code` is the only step that can change the
  tree, so every gate on this lane shares it as its rewind target (`deliver`'s "back to
  code" included). The re-run passes through a fresh `review`, then `verification`.
  An all-text-only review FAIL (every blocking finding marked `text_only: true`) takes
  the light path: `code`'s previous session resumes with the verdict, applies only
  text-only fixes, and the gate tail (a fresh `review`, then `verification`) re-runs
  unchanged.
  A review FAIL whose blocking findings are ALL off-graph (each finding's
  `repair_target` names the authored suite, the spec, a declared obligation that is itself
  wrong or an un-IDed instruction — never `code`, which covers every repair the
  build step can make in its own round: updating a declared obligation the diff
  left unchanged, withdrawing a moot one, citing an undeclared file) is not rewound: the driver asks the owner (the
  review owner exit), who answers `amend-upstream: <words>` (recorded verbatim in
  `artifacts/grill-qa.md` `## Owner rulings`; chore has no authoring step, so
  every ruling is executed by `code`; spec and plan are never re-run), `waive <refs> —
  <words>` (clears exactly the named findings; review re-runs) or `cancel`. That
  rewind is owner-routed. A mixed FAIL rewinds `code` as above, the off-graph
  findings riding along.
- **`verification` FAILs -> rewind to `code`.** Build/lint/tests failed on the changed
  tree, and `code` is the only step that can change it. The re-entered round re-runs the
  whole gate tail after the fix — a fresh `review` judges the repaired diff, then
  `verification` re-runs the machine bar on the tree that review passed merged with
  the latest origin default branch.

Workflow-level rewind bounds are the uniform `9 / 3` the delivery lanes share:
`max_total: 9`, `max_per_step: 3` — the per-step bound is charged per gate→target edge
(the gates that rewind to `code` carry three charged slots each; light and converging
rewinds charge none, until a light re-entry self-reports `needs_code_change: true` —
that turns its light rewind into a fresh round, which charges the edge once), and the
global cap counts every automatic rewind, light ones included. Owner-routed rewinds charge neither the edge slot nor `max_total`: a `deliver` FAIL a human answered ("back to code", and on a scoped work item 「全量重验」, which is owner-routed too) and the review owner exit's rewind. A
chore that needs more than three charged attempts per gate to
keep the tree green is either not mechanical or is fighting a real problem — surface it
honestly instead of burning the budget.

## Budget guidance

No workflow-level budget and no step `budget.wall_minutes`: the bounds are the rewind limits
above and the daemon's per-step runtime bound T (docs/reference/commands.md, `pf help daemon`).

## Delivery and releases

`deliver` runs last, after every gate passed; its contract (merge the latest default branch,
push only the feature branch, open or reuse the PR, merge under the merge rule or ask a
human) is the shared step definition `workflows/_shared/steps/deliver.md`. Lane facts:
its "back to code" answer rewinds to `code`, and the independent review it reads is this
workflow's `review` gate (`steps.review.output` — its verdict, its recorded scope on
the scoped tier, and the `reviewed_heads` the driver wrote at its pass, which must
cover the delivered diff). There is no release slot: releases and
deploys happen only through the separate `release` bundle (a separate WI), cut from the
merged origin head.
