---
name: review
description: Fresh-context judgment of a micro change - the diff against the ticket and against each repository's declared text paths, no behavior change, green evidence - before delivery.
output:
  type: object
  required: [verdict]
  properties:
    verdict:
      type: string
      enum: [pass, fail]
      description: pass means the change is a clean, ticket-shaped text change inside the declaration with green evidence; fail means fix must repair it.
    findings:
      type: array
      items:
        type: object
        required: [severity, detail]
        properties:
          severity: { type: string, enum: [blocking, non-blocking] }
          detail: { type: string, description: What is wrong and why it is not a ticket-shaped text change. }
          path: { type: string, description: The repo-prefixed path the finding names (`<repo>/<path>`), when it names one. }
          repair_target:
            type: string
            enum: [code]
            description: "Required on a blocking finding - on this lane every repair is {{build_step}}'s own, in its next round."
      description: Every finding, whether or not it fails the gate. A pass verdict with a blocking finding is self-contradictory - the schema's oneOf forbids it.
    fail_reason:
      type: string
      description: Required when verdict is fail - a synthesis of the blocking findings, carried back to fix as its pending_rewind_input.
  oneOf:
    - description: A pass verdict - zero blocking findings.
      properties:
        verdict: { type: string, enum: [pass] }
        findings:
          type: array
          items:
            type: object
            properties:
              severity: { type: string, enum: [non-blocking] }
      required: [verdict]
    - description: A fail verdict - at least one blocking finding and a non-empty fail_reason.
      properties:
        verdict: { type: string, enum: [fail] }
        fail_reason: { type: string, minLength: 1 }
        findings:
          type: array
          contains:
            properties:
              severity: { type: string, enum: [blocking] }
      required: [verdict, fail_reason]
---

# review

Judge whether this micro change is ready to deliver. This lane has no spec, no plan and
no verification step: the ticket body is the specification, and after you only
`deliver` runs — it re-runs the recorded fast evidence on the delivered HEAD and merges.
Your pass is the lane's only independent judgment.

## What to do

1. Read, in this order: the ticket (`runtime-state/work-item.json`, its goal and content);
   `{{build_step}}`'s output in `execution.json` (`steps.{{build_step}}.output`: its
   `changed_paths`, `evidence`, `summary`, and the driver-written `fast_lane` — per repo
   the base, the changed and declared paths, and the credential commands); and the whole
   diff, in each task repo
   `git diff $(git merge-base HEAD origin/HEAD)..HEAD` (falling back to `origin/main` or
   `origin/master` when `origin/HEAD` is unset), including any commit `{{build_step}}`
   did not report.
2. In each changed repo, read `<data-dir>/release/current/tools/verify-scope --micro --json`
   (`<data-dir>` is `$POLYFORGE_DATA_DIR` when set, else `~/.local/share/polyforge`) and
   compare its `commands` (the fast phase, then the declared micro checks of the base
   declaration) with the evidence `{{build_step}}` reported and the `fast_lane` commands:
   a command of the engine's list missing from them is blocking.
3. Pass only when ALL hold, and fail with one finding per violation otherwise, naming the
   path and why:
   - the diff is confined to the text change the ticket asks for — nothing it did not ask
     for, nothing it asked for missing;
   - every changed path lies inside its repository's declared `micro_paths`, and no
     `AGENTS.md` or `CLAUDE.md` changed;
   - no product runtime behavior, code, configuration, generated file or test changed —
     a runtime branch, a config value or an assertion is never text, even inside a
     declared path;
   - the evidence is green: `{{build_step}}` reported `build_status: green` and ran every
     command of the engine's list.
4. Record every finding with its severity; on this lane every blocking finding is
   `{{build_step}}`'s to repair (`repair_target: code`), and `fail_reason` synthesizes
   them concretely enough that its next attempt does not have to guess.

**Do not run the build or tests.** Reading the evidence list is part of the review; the
delivery re-runs the commands on the delivered HEAD.

You are not the out-of-bounds authority: the driver already judged every changed path
against each repository's base declaration before dispatching you, and a refused diff
never reaches you. Judge what the machine cannot — whether the text is the ticket's and
whether anything in it changes behavior. A pass records the tree it covered (the driver
writes `reviewed_heads`); any commit after it invalidates the pass.

This review always runs **fresh**. When a prior verdict-bearing review round is on record
the driver adds a `# Review re-entry` section.

## Principles

Every principle is a check (a violation is blocking).

{{principles}}

## Output

Write `artifacts/review.md`: the verdict, every finding with its severity and path, and
the `fail_reason` on a fail. Then emit the final JSON object per the schema above.
