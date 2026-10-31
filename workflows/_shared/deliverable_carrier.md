## Every deliverable has a carrier step

A *deliverable* is any artifact, evidence, human action or check this plan requires a
later step to produce or perform — a report, a matrix, a screenshot set, a proof, a doc
update, an owner presentation. A plan promises only what a step contract carries:

1. Read this capsule's own step contracts: the ordered `steps` array of
   `workflows/<bundle>/workflow.md` and, per step id, its definition
   (`workflows/<bundle>/steps/<id>.md`, else `workflows/_shared/steps/<id>.md`).
2. In `artifacts/plan.md`, write a `## Deliverables and carriers` section with one line
   per deliverable: `<deliverable> — carried by <step id> (<artifact path or output
   field>)`. "Review will check it" or "the owner will see it" counts only when that
   step's contract says so.
3. A deliverable no step contract carries is never written as a promise. Either drop it
   and record it under `## Excluded (no carrier step)` — the deliverable, the step ids
   you checked, and that no step contract carries it — or, when the task definition or
   spec requires it, stop with `needs_input`, `question_id` `plan-carrier`, naming the
   deliverable, the step ids you checked and the missing carrier, and ask the owner to
   drop the requirement, or to answer `cancel` at that question (the reserved answer
   ends this work item) and re-file it on a workflow whose steps carry it.
4. An owner presentation — material the owner must look at before it ships — has a
   carrier: `deliver`'s owner-review gate. Map it to `deliver` (its
   `deliver-owner-review` question) and to the step that produces the artifacts, and
   declare it with the `owner-review:` line (this definition's owner-review section); it
   is never excluded for want of a carrier.
