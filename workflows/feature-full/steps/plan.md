---
name: plan
description: Write the implementation plan — ordered, compilable units of work with declared touched files — grounded in the confirmed spec.
output:
  type: object
  required: [plan_path, step_ids, touched_files]
  properties:
    plan_path:
      type: string
      description: Capsule-relative path to the written plan artifact (artifacts/plan.md).
    step_ids:
      type: array
      minItems: 1
      items: { type: string }
      description: Ordered ids of every implementation step declared in the plan. At least one is required (minItems 1) - a plan with zero steps is an unfilled template.
    touched_files:
      type: array
      minItems: 1
      items:
        type: object
        required: [path, intent]
        properties:
          path: { type: string, description: "repo-relative path" }
          intent: { type: string, enum: [read, write] }
          coupled_obligations:
            type: array
            minItems: 1
            description: "Required on every `write` entry (the driver rejects a write without it). The documents, tables, enumeration lists and projection rules that must change together with this file, each as `{path, note}`; or, when nothing is coupled, exactly one `{none: true, reason}` entry saying why."
            items:
              type: object
              properties:
                path: { type: string, description: "repo-relative path of the coupled document, table or rule" }
                note: { type: string, description: "what in it must change (the row, entry or rule)" }
                none: { type: boolean, enum: [true] }
                reason: { type: string, description: "why no document, table or projection rule is coupled to this write" }
        if:
          properties: { intent: { const: write } }
        then:
          required: [coupled_obligations]
      description: Deduplicated list of every file the plan declares as read or written, across all steps (write wins over read for the same path). At least one entry is required (minItems 1) - a plan that touches nothing leaves `code` nothing to commit.
---

# plan

Write the implementation plan for this work item, grounded in `artifacts/spec.md` (read
it in full — do not plan against your own recollection of the goal).

## Contract

The plan MUST be written in OpenSpec grammar mirroring the spec's own requirement
structure where it maps directly (do not re-derive requirements from scratch; reference
the spec's requirement titles), plus an ordered `## Steps` section. Every step MUST:

- be phrased as a **compilable/testable unit** — something that leaves the tree in a
  working, buildable state when done, not a mid-refactor fragment;
- declare each step's **per-unit green bar** as the repo's FAST-CHECK bar - the same
  bar `code` runs per unit: {{fast_checks}} Name the concrete commands. The full
  suite is NOT a per-unit bar, not even for the final unit - it belongs to
  `verification`, which also exercises `e2e-author`'s test files (those are not part
  of any per-unit bar);
- carry a `Touched files: <repo-relative path> (write|read), ...` line, or
  `Touched files: (no file edits)` for steps that only reason/verify without changing
  the tree, and a `Coupled obligations:` line naming, for each write, what must change
  with it (or `none` and why).

Example:

```markdown
## Steps

1. **implement_pvc_guard** (m) - add the bound-PVC check to the scheduler predicate
   - Touched files: `pkg/scheduler/predicates.go` (write)
   - Coupled obligations: `docs/scheduling.md` (the predicate table gains the guard's row)
2. **write_tests** (s) - unit tests for the guard
   - Touched files: `pkg/scheduler/predicates_test.go` (write)
   - Coupled obligations: none — a test file no document or list enumerates
3. **wire_config** (xs) - expose the new guard behind a feature flag
   - Touched files: `pkg/scheduler/config.go` (write), `pkg/scheduler/predicates.go` (read)
   - Coupled obligations: `docs/configuration.md` (the flag table gains the new flag)
```

## What to do

1. Read `artifacts/spec.md`.
2. Order steps so that each one, once done, leaves `code` (the next step) able to build
   and run tests on the tree so far — do not plan a sequence that only compiles once
   every step has landed. This is what lets `code` commit per unit rather than in one
   undifferentiated blob.
3. Declare every file each step will touch, honestly distinguishing read-only inspection
   from actual writes — the list exists for two checks: the coupled obligations (what
   must change with each written file), and the scope check, where the driver compares
   the delivered diff with the declared set in both directions and hands the
   differences to `review` as facts; an under-declared write shows up there as a
   blocking undeclared change, not a paperwork gap.
4. For every `write`, name its coupled obligations: the documentation, tables,
   enumeration lists and projection/exclusion rules that describe or enumerate the file
   and must change with it — or an explicit `none` with the reason. Read the root
   `AGENTS.md` (else `CLAUDE.md`) of every repo first: a project lists its own known
   couplings there. A new file has no incoming references, so a grep for its name cannot
   find its obligations — ask what documents, lists or rules would have to name it.

## e2e-author mode

Read the mode the owner approved in `artifacts/grill-qa.md` and state it in
`artifacts/plan.md`. The default is `required`; the per-work-item value is the
owner-approved one, and with no approved record the default `required` applies. A
skip is honoured only when `grill-qa.md` records the owner's explicit approval of
`skipped-with-rationale` (fail-closed to `required` otherwise);
`skipped-with-rationale` means the lane bypasses `e2e-author`'s authored scenarios
(the `e2e-author` step writes only its scenario document recording the mode and
rationale, and commits nothing). You MAY propose `skipped-with-rationale` with a
rationale for an internal-mechanism / non-behaviour-face work item and record it as
a proposal, but you MUST NOT silently downgrade the owner-approved mode, and a
behaviour-face work item (change-coverage discipline ①) stays `required`.

## Non-goals

- Do not write any code here — that is `code`'s job. The plan is instructions, not a diff.
- Do not write test scenarios — `e2e-author` covers end-to-end scenarios separately;
  ordinary unit tests belong inside the relevant `code` step of this plan if the project
  expects them alongside the implementation.

## Principles

Pick the simplest mechanism; before adding timing, shutdown or retry logic, check whether the recovery path already covers it; no I/O under a lock.

{{principles}}

## Output

Emit the final JSON object per the schema above: the artifact path, the ordered step ids
(at least one), and the deduplicated touched-files list (at least one entry; each path
once, `write` intent winning over `read` for the same path; every `write` carrying its
`coupled_obligations`).
