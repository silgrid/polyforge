---
name: chore
description: Deletion-type maintenance lane - plan, code, verify, review, deliver; deletions and cleanup ship through this lane. No release slot.
rewind:
  max_total: 2
  max_per_step: 2
defaults:
  model_candidates: [pi:sub2api-glm/glm-5.3, pi:sub2api-claude/pi-claude-opus-5, pi:sub2api-openai/gpt-6-astra]
steps:
  - { id: plan, context_policy: fresh, handoff: true }
  - { id: code, context_policy: inherit }
  - id: verification
    context_policy: fresh
    gate: true
    requires_command_evidence: true
    on_fail: { target: code }
    model_candidates: [pi:sub2api-glm/glm-5.3-flash, pi:sub2api-glm/glm-5.3, pi:sub2api-claude/pi-claude-opus-5]
    vars:
      output_required: verdict, checks
      build_step: code
      position: >-
        `review` judges the change after you (reading your report), and there is no
        release slot behind you: the full set runs here or not at all.
      lane_checks: |-
        **Plan conformance.** Check the diff `code` reported against `artifacts/plan.md`'s
        touched-files declaration: undeclared writes are a finding worth recording even when
        the checks pass — they signal a lock-scope gap. Record this as a `plan-conformance`
        check.
      never_pre_existing: any test the plan names as pinning this change, and any repro check
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
           includes any commit `code` did not report), `artifacts/code-summary.md` (the code
           step's own account of what it did and why) and `artifacts/verification-report.md`
           (the machine gate's results).
        2. Judge plan conformance: the diff covers the plan's declared steps, touches no
           files the plan did not declare (an undeclared write is a lock-scope gap, not a
           paperwork gap), and a declared deletion is actually complete — no half-removed
           feature left compiling against ghosts of itself.
        3. Judge the deletion-type acceptance bar: the existing test suite stays meaningful
           after the deletion, and the few targeted tests for behavior the deletion actually
           changes exist where the project expects tests. A full acceptance suite or an e2e
           lane is NOT this lane's bar — demanding one here is scope creep, not rigor. A test
           that asserts nothing real (vacuous) is a blocking finding; a redundant one is
           non-blocking.
  - { id: deliver, context_policy: fresh, gate: true, requires_command_evidence: true, on_fail: { target: code } }
---

# chore — deletion-type maintenance flow

This is the flow-level narrative for the workflow. It explains how the five
steps compose; it is not itself a structural schema — the frontmatter `steps` array above
is the *only* execution order. Nothing here overrides it.

## Shape of the flow

```
plan --> code --> verification --> review --> deliver
          ^            |              |           |
          +------------+--------------+-----------+
                    (FAIL: rewind to code)
```

The lane covers deletion-type and maintenance work: dead-code and feature removal,
cleanup sweeps, dependency bumps, tooling and lint config updates, mechanical
refactors, doc sweeps, test hygiene. The bundle chosen at filing IS the
classification — filing a WI on `chore` declares it a deletion-type change (no new
label or field is added later), and `feature-full` is NOT trimmed by change type, so
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
  removal judgment, it was misrouted: cancel and re-file on `feature-full`.
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
| `code` | changes under `repos/<name>/**`, `artifacts/code-summary.md` | `verification`, `review` |
| `verification` | `artifacts/verification-report.md` (verdict) | `review`, `deliver` |
| `review` | `artifacts/review.md` (verdict) | `deliver`, (rewind context for `code` via `pending_rewind_input`) |
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
exist and sit strictly earlier than the failing step. This section explains the choices:

- **`verification` FAILs -> rewind to `code`.** Build/lint/tests failed on the changed
  tree, and `code` is the only step that can change it. It runs before `review`, so a
  mechanical failure never costs a review.
- **`review` FAILs -> rewind to `code`.** The owner-rules checklist or the
  plan-conformance judgment found the change wanting (undeclared writes, a
  half-finished deletion, a rule hit); `code` is the only step that can change the
  tree, so every gate on this lane shares it as its rewind target (`deliver`'s "back to
  code" included). The re-run passes through `verification` again before `review`.
  An all-text-only review FAIL (every blocking finding marked `text_only: true`) takes
  the light path: `code`'s previous session resumes with the verdict, applies only
  text-only fixes, and the gate tail (`verification`, then a fresh `review`) re-runs
  unchanged.

Workflow-level rewind bounds are the tightest of the reference bundles:
`max_total: 2`, `max_per_step: 2`. A chore that needs more than two bounded attempts to
keep the tree green is either not mechanical or is fighting a real problem — surface it
honestly instead of burning the budget.

## Budget guidance

No workflow-level budget and no step `budget.wall_minutes`: the bounds are the rewind limits
above and the daemon's per-step runtime bound T (README, `pf daemon` flags).

## Delivery and releases

`deliver` runs last, after every gate passed; its contract (merge the latest default branch,
push only the feature branch, open or reuse the PR, merge under the merge rule or ask a
human) is the shared step definition `workflows/_shared/steps/deliver.md`. Lane facts:
its "back to code" answer rewinds to `code`, and the independent review it reads is this
workflow's `review` gate (`steps.review.output`). There is no release slot: releases and
deploys happen only through the separate `release` bundle (a separate WI), cut from the
merged origin head.
