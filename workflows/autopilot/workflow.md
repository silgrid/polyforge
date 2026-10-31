---
name: autopilot
description: Light remediation flow - one interactive checkpoint, a single fix pass and a machine gate, sized for small well-understood changes. It can merge unattended - with no question asked and its gates passing, deliver auto-merges.
rewind:
  max_total: 6
  max_per_step: 3
defaults:
  model_candidates: [pi:example-gateway/model-a, pi:example-claude/claude-opus-5, pi:example-openai/gpt-x]
steps:
  - { id: grill, context_policy: fresh, handoff: true, requires_human_answer: if_asked, recall: true }
  - { id: fix, context_policy: inherit, recall: true }
  - id: verification
    context_policy: fresh
    gate: true
    requires_command_evidence: true
    on_fail: { target: fix }
    model_candidates: [pi:example-gateway/model-a, pi:example-claude/claude-opus-5]
    vars:
      output_required: verdict, checks
      build_step: fix
      position: >-
        In this lane there is no separate review step behind you, so you are also the last
        mechanical line of defense before the change ships. This lane always verifies at
        the FULL tier: autopilot has no review step, so it never opts into the scoped tier
        and never carries a `scope` object.
      lane_checks: |-
        **Memo conformance.** Check the diff `fix` reported (read its `commits` from
        `execution.json`) against `artifacts/fix-memo.md`'s `## Scope` and `## Non-goals`:
        everything in Scope must be visibly addressed, and nothing in Non-goals may appear in
        the diff. Record this as a `memo-conformance` check with pass/fail.
      never_pre_existing: the check of the defect this fix addresses (the memo's `## What is broken or missing` case, and any repro check)
  - { id: deliver, context_policy: fresh, gate: true, requires_command_evidence: true, on_fail: { target: fix } }
---

# autopilot — light remediation flow

This is the flow-level narrative for the workflow. It explains how the four
steps compose and what flows between them; it is not itself a structural schema — the
frontmatter `steps` array above is the *only* execution order. Nothing here overrides it.

## Shape of the flow

```
grill --> fix --> verification --> deliver
           ^          |               |
           +----------+---------------+
                (FAIL: rewind to fix)
```

The flow collapses the feature-full clarification trio (`grill` -> `spec` ->
`grill-review`) into ONE front-loaded interactive checkpoint (`grill`), then runs a
single fix pass and one machine-checkable gate, then delivers (`deliver`, below). It is for work whose
scope is already small and well understood: a bounded bug fix, a small config or
dependency change, a tiny feature. If a WI routed here turns out to need a real spec or a
separate review pass, that is a signal it was misrouted — `grill` asks the owner, who
answers the open question with the reserved `cancel` (it ends this WI) and re-files against
`feature-full` or `bug-fix` rather than stretching this lane.

## The human checkpoint (`requires_human_answer: if_asked`)

`grill` is the lane's only human checkpoint. Its questions are a human's to answer: the
daemon refuses an answer that comes from a step process. Zero questions is a legitimate
outcome for a clear, small ask, so `grill` may complete without asking — an unattended
claim runs on — but that completion is recorded (`human_gate_unasked` event) and the
owner is notified.

## Why `grill` also writes the spec

In `feature-full`, the pre-spec clarification step `grill` is followed by a separate `spec`
step. This bundle deliberately has no separate spec authoring step: the `grill` step does not
stop at gathering answers, it commits them to a micro-spec (`artifacts/fix-memo.md`) that `fix`
implements directly.

## Artifact flow between steps

Every step's output is a path recorded in `execution.json`'s `steps.<id>.output`, pointing at a
file under `artifacts/`; no step declares frontmatter `inputs:`, so downstream steps read upstream artifacts by that convention:

| step | primary artifact it writes | who reads it |
|---|---|---|
| `grill` | `artifacts/grill-qa.md` (question/answer log), `artifacts/fix-memo.md` (micro-spec) | `fix` |
| `fix` | changes under `repos/<name>/**`, `artifacts/fix-summary.md` | `verification`, `deliver` |
| `verification` | `artifacts/verification-report.md` (verdict) | `deliver` (verified heads, test bar; its pass stands in for review) |
| `deliver` | the pushed feature branch, PR and merge outcome per repo, `artifacts/delivery.md` | (terminal) |

## Rewind boundaries

Both gates (`verification`, `deliver`) declare `on_fail: {target: fix}` — this section explains the choice. A rewind target must exist in the `steps` array and sit strictly
earlier than the failing step:

- **`verification` FAILs -> rewind to `fix`.** Verification is the machine gate
  (build/lint/tests); a failure means the tree is wrong, and `fix` is the only step that
  can change it.
- **`deliver` FAILs ("back to code") -> rewind to `fix`.** The human sent the delivery
  back, or the merge rule was not met and a re-work is due; `fix` is again the only
  step that can change the tree.

Workflow-level rewind bounds for this bundle are declared once in the frontmatter:
`max_total: 6`, `max_per_step: 3`. The per-step bound is charged per gate→target edge —
`verification` and `deliver` each carry three charged slots for their `fix` edge — and
`max_total` is the global hard cap that counts every automatic rewind (this lane has no review,
so no light or converging exemption applies). An owner-routed rewind — a `deliver` FAIL a
human answered ("back to code", and on a scoped work item 「全量重验」, which is owner-routed
too) — charges neither the edge slot nor `max_total`. A quick fix that cannot land within three
charged rewinds per gate is not a quick fix; past those bounds the workflow ends in
`failed_rewind_exhausted` and the honest response is a re-filed WI on a heavier lane,
not more loops.

## Budget guidance

No workflow-level budget and no step `budget.wall_minutes`: the bounds are the rewind limits
above and the daemon's per-step runtime bound T (docs/reference/commands.md, `pf help daemon`).

## Delivery and releases

`deliver` runs last, after every gate passed; its contract (merge the latest default branch,
push only the feature branch, open or reuse the PR, merge under the merge rule or ask a
human) is the shared step definition `workflows/_shared/steps/deliver.md`. Lane facts:
its "back to code" answer rewinds to `fix`, and this workflow has no `review` step, so
verification's pass stands in for the independent review — an autopilot delivery
auto-merges when its tests are covered on the pushed head, no conflict was hand-resolved
and the pre-flight is clean, and asks otherwise. A declared `owner-review:` artifact makes
deliver ask the human-only `deliver-owner-review` question first, before any merge. There is no release slot: releases and
deploys happen only through the separate `release` bundle (a separate WI), cut from the
merged origin head.
