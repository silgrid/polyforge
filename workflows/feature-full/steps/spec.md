---
name: spec
description: Write the spec artifact — OpenSpec-grammar requirements and scenarios, scope, non-goals, and acceptance criteria — grounded in the grill Q&A.
output:
  type: object
  required: [spec_path, requirement_titles]
  properties:
    spec_path:
      type: string
      description: Capsule-relative path to the written spec artifact (artifacts/spec.md).
    requirement_titles:
      type: array
      minItems: 1
      items: { type: string }
      description: Titles of every Requirement in the spec, in document order. At least one is required (minItems 1) - a spec with zero requirements is an unfilled template.
    non_goals:
      type: array
      items: { type: string }
      description: Explicit non-goals called out in the spec.
---

# spec

Write the spec for this work item. Ground it in `artifacts/grill-qa.md`'s `## grill`
section (the confirmed intent from the previous step) — do not re-derive intent from the
raw WI goal alone; the grill pass exists precisely so you are not guessing here.

## Contract

The spec MUST be written in OpenSpec grammar:

- `## Requirements`
- `### Requirement: <title>` — phrase each with an RFC-2119 keyword (MUST / SHALL / SHOULD)
- `#### Scenario: <title>` — at least one per requirement, in GIVEN/WHEN/THEN form

If this WI is a bug fix, phrase the broken behavior as a Requirement being violated (e.g.
"The scheduler MUST NOT double-count draining nodes") with a Scenario that reproduces it,
plus a second Requirement capturing the corrected behavior.

Additionally include, before or after `## Requirements`:

- `## Scope` — a short list of what this change covers.
- `## Non-goals` — an explicit list of adjacent things this change deliberately does not
  do. An empty non-goals section is a signal you have not thought about scope boundaries;
  do not omit the heading even if the list is short.
- `## Acceptance criteria` — a checklist form of the scenarios above, phrased so a
  reviewer or the `review` step can check them off without re-deriving them from prose.

Example:

```markdown
## Scope
- Reject payloads missing the `name` field on the create-user endpoint.

## Non-goals
- Validating any field other than `name`.

## Requirements

### Requirement: The API MUST reject payloads missing the `name` field
#### Scenario: Missing required field
- GIVEN a request payload without a `name` field
- WHEN the endpoint receives the request
- THEN the API MUST respond 400 with a field-level error

## Acceptance criteria
- [ ] POST without `name` returns 400 with a field-level error
```

## What to do

1. Read `artifacts/grill-qa.md` in full.
2. Write `artifacts/spec.md` per the Contract above.
3. If this is a rewind re-entry (i.e. `execution.json`'s `steps.spec.pending_rewind_input`
   is non-null, carrying a `grill-review` FAIL verdict), open by explicitly addressing
   every point the verdict raised before writing the rest of the spec — do not silently
   revise without acknowledging what changed and why.
4. Keep the spec's scope tight to what `grill` actually clarified. Do not expand scope on
   your own initiative; if you believe the WI needs a bigger scope than what was
   clarified, say so explicitly in `## Non-goals` as a called-out exclusion rather than
   silently including or silently dropping it.

## Non-goals

- Do not write an implementation plan or file-level breakdown — that is `plan`'s job.
- Do not write test code or e2e scenarios — that is `e2e-author`'s job.

## Principles

State the destination (features and guarantees) and non-goals; no requirement may force an invented mechanism or pin an intermediate path.

{{principles}}

## Output

Emit the final JSON object per the schema above: the artifact path, the list of
requirement titles in document order (at least one - the schema rejects an empty list),
and the list of non-goals.
