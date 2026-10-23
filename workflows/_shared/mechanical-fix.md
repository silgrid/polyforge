---
output:
  type: object
  required: [verdict, checks]
  properties:
    verdict:
      type: string
      enum: [pass, fail]
      description: pass means every bound command you re-ran exited 0 on the current tree; fail means at least one did not (or a semantic confirmation could not be made).
    checks:
      type: array
      minItems: 1
      items:
        type: object
        required: [name, status]
        properties:
          name: { type: string }
          status: { type: string, enum: [pass, fail, skipped] }
          detail: { type: string }
      description: Per-check breakdown of the light recheck - each bound command as its own named check, in the order run. At least one entry is required.
    fail_reason:
      type: string
      description: Required when verdict is fail - a precise description of what still fails.
  oneOf:
    - description: A pass verdict - every recorded check must itself be pass; a skipped or failed check is not a passed recheck.
      properties:
        verdict: { type: string, enum: [pass] }
        checks:
          type: array
          items:
            type: object
            properties:
              status: { type: string, enum: [pass] }
      required: [verdict]
    - description: A fail verdict - requires a non-empty fail_reason and at least one check recorded fail.
      properties:
        verdict: { type: string, enum: [fail] }
        fail_reason: { type: string, minLength: 1 }
        checks:
          type: array
          minItems: 1
          contains:
            type: object
            required: [status]
            properties:
              status: { type: string, enum: [fail] }
      required: [verdict, fail_reason]
---

# The mechanical fast lane (polyforge#260)

One file, the whole fast-lane contract: the classification duty an armed
full verification owes, and the light-recheck contract the light dispatch
runs under. The driver renders each into the prompt it belongs to; the
driver never judges staticness itself - deliver's unconditional full-bar
re-run after a light pass is the backstop that keeps the tests-covered
guarantee even if a classification is wrong.

## Classification duty (an armed full verification FAIL)

This lane is armed for the mechanical fast lane (`mechanical_fix: true` on
this step's entry): a verification FAIL whose every failed check is static
may rewind cheaply. When - and only when - every failed check of your FAIL
verdict is a STATIC check, reproducible by running ONE deterministic command
(a lint, format, vet or spelling-class check), and NO test failed (no
full-bar test, no authored acceptance scenario, no repro - any test failure
disqualifies the classification; the rule is all-or-nothing), classify the
FAIL mechanical: write `mechanical_bound` into your FAIL verdict's output,
one entry per FAILED check -

- `check`: the failing check's `name`, exactly as recorded in `checks`,
- `repo`: the task repo it ran in (`repos/<name>`),
- `command`: the exact command that reproduces the check (no leading `cd`),
- `files`: the non-empty list of repo-relative paths the check named.

Every check recorded `fail` must be covered exactly - one entry per failed
check, no more and no fewer; each entry's command and file list non-empty. A
FAIL that is not all-static, or a check not reproducible by one
deterministic command, carries NO classification: the rewind is the normal
one. Never classify to save cost - the bound is the fix's boundary, and a
malformed classification or a broken bound escalates back to this same full
gate in place.

## Light recheck contract

You are the light recheck: the verification step re-run in light mode,
bounded to the failed checks of the mechanical FAIL verdict (the bound
below). Your duties:

1. Re-run EXACTLY each bound command, byte-faithful, inside the repo it
   names (work inside `repos/<name>/`). Record command evidence for every
   command you run, as always.
2. Do NOT re-run the full test bar, do NOT run the authored acceptance
   scenarios, do NOT modify any file - this step observes, it does not fix.
   This light section OVERRIDES the step definition above it wherever the
   two conflict: the definition describes the full verification's bar, and
   none of it runs here beyond the bound commands.
3. Confirm the semantic claims the classification rests on (for example
   that the failed check's findings name only the bound's files) in the
   report section of `artifacts/verification-report.md`: write the
   fast-lane section there - what was skipped (the full bar and the
   authored scenarios), on what grounds (the mechanical classification),
   the bound, and your semantic confirmations.
4. Your output carries ONLY the light fields per the schema above:
   `verdict`, `checks` (one entry per bound command), and - on a fail -
   `fail_reason`. No `test_bar`, no `authored_titles`, no `scenarios`: the
   driver carries the full bar's records itself.

A bound command that still exits non-zero fails the recheck: record the
check `fail` and the verdict `fail` - never fabricate a pass. A semantic
confirmation you cannot make fails the recheck too: say so in
`fail_reason`.