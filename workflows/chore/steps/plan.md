---
name: plan
description: Write the maintenance plan - ordered, compilable units of work with declared touched files - proportionate to the chore's actual size.
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
      description: Ordered ids of every implementation step declared in the plan. One entry is the correct answer for a genuinely single-step chore. At least one is required (minItems 1) - a plan with zero steps is an unfilled template.
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

Write the maintenance plan for this work item, grounded in `runtime-state/work-item.json`
(the daemon-materialized task definition — read it in full; do not plan against your own
recollection of the ask).

## Proportionality is the contract

This is a normal step that always runs — the schema has no optional steps, and this
lane does not invent any. What scales with the chore's size is the plan's *depth*, not
whether it exists:

- A dependency bump, a config flag flip, a one-file mechanical edit: a plan declaring
  exactly one trivial step is the *correct* plan, not a degenerate one. Do not pad it.
- A sweep (rename across many files, lint-rule rollout, dependency chain upgrade):
  enumerate the units so `code` can commit per compilable unit and keep the tree green
  between commits. A sweep planned as one undifferentiated blob is a plan failure.

## What to do

1. Read `runtime-state/work-item.json` in full.
2. Determine the unit structure: if the change cannot land in one compilable unit, order
   the steps so each leaves the tree buildable — same discipline as feature-full's
   `plan`, minus the spec input (chores carry no product spec).
3. Declare each step's per-unit green bar as the repo's FAST-CHECK bar — the same bar
   `code` runs per unit: {{fast_checks}} Name the
   concrete commands. The full suite is not a per-unit bar — it belongs to
   `verification`.
4. Every step MUST carry a `Touched files: <repo-relative path> (write|read), ...` line,
   or `Touched files: (no file edits)` for steps that only verify, and a
   `Coupled obligations:` line for its writes. Declare honestly — the list exists for
   two checks: the coupled obligations (what must change with each written file), and
   the scope check, where the driver compares the delivered diff with the declared set
   in both directions and hands the differences to review and `verification`
   as facts; an under-declared write shows up there as a blocking undeclared change.
   For every `write`, name its coupled obligations: the documentation, tables,
   enumeration lists and projection/exclusion rules that describe or enumerate the file
   and must change with it — or an explicit `none` with the reason. Read the root
   `AGENTS.md` (else `CLAUDE.md`) of every repo first: a project lists its own known
   couplings there. A new file has no incoming references, so a grep for its name cannot
   find its obligations — ask what documents, lists or rules would have to name it.
5. Write `artifacts/plan.md` with an ordered `## Steps` section, then emit the ordered
   step ids and the deduplicated touched-files list with each write's
   `coupled_obligations`.
6. BEHAVIOR-FACE ESCALATION (change-coverage rules, discipline ① — hard rule): a
   chore graph authors no acceptance scenarios. If reading the WI reveals the work
   alters OBSERVABLE BEHAVIOR — new endpoint/verb, protocol/contract/semantics
   change, ownership or fence rule change — you cannot proceed silently. The
   chore graph has no interactive checkpoint, so do NOT guess: emit no plan and stop with
   `needs_input` (`question_id` `plan-behavior-face`), naming the observable behavior the
   work would change and asking the owner to answer `cancel` (the reserved answer ends
   this work item) and re-file it on an e2e-carrying graph, or to record a waiver, so the owner question is answered before `code` runs. A behavior-facing
   change executed on the chore graph without an owner waiver on record is a
   contract violation.

{{deliverable_carrier}}

{{owner_review}}

## Non-goals

- Do not write code — that is `code`'s job.
- Do not add scope the WI did not ask for. The classic chore failure is the dependency
  bump that becomes a refactor; if adjacent work is genuinely needed, say so in the plan
  as a called-out exclusion and let a human decide, rather than silently including it.

## Principles

Pick the simplest mechanism; before adding timing, shutdown or retry logic, check whether the recovery path already covers it; no I/O under a lock.

{{principles}}

## Output

Emit the final JSON object per the schema above: the artifact path, the ordered step
ids, and the deduplicated touched-files list (each path once, `write` intent winning
over `read` for the same path; every `write` carrying its `coupled_obligations`).

{{handoff_duty}}
