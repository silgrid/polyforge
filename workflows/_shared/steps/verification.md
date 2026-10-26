---
name: verification
description: Machine-checkable gate - the build, lint, and tests (plus the lane's own checks named in the step entry's vars) pass on the tree the build step produced - the machine gate before review or delivery. Shared by every delivery bundle; the lane-specific text comes from the workflow.md step entry's vars.
output:
  type: object
  required: [{{output_required}}]
  properties:
    verdict:
      type: string
      enum: [pass, fail]
      description: pass means every check run (build/lint/tests and every lane check the instructions below name) succeeded on the current tree; fail means at least one did not.
    checks:
      type: array
      minItems: 1
      items:
        type: object
        required: [name, status]
        properties:
          name: { type: string }
          status: { type: string, enum: [pass, fail, skipped, pre_existing] }
          detail: { type: string }
      description: Per-check breakdown (build, lint, tests, and every lane check the instructions below name, each as its own named check), in the order run. At least one check must be recorded (minItems 1) - a verdict over zero checks is an unfilled template.
    fail_reason:
      type: string
      description: Required when verdict is fail - a precise description of what failed, to be carried back to the step this gate rewinds to as its pending_rewind_input. The oneOf split below makes the requirement mechanical - a fail verdict without a non-empty fail_reason does not validate.
    pre_existing_failures:
      type: array
      items: { type: string }
      description: "Tests that fail on this tree AND fail the same way on the base commit (re-run there) - pre-existing failures, reported but never this change's fault, each as `<repo-relative test file>: <test name>`. Their checks carry status pre_existing with the base sha in detail; they never fail the gate. Never the repro, an authored test_files entry or the regression test (the driver rejects that)."
    test_bar:
      type: array
      items:
        type: object
        required: [repo, command]
        properties:
          repo: { type: string, description: "The task repo (repos/<repo>) the command runs in." }
          command: { type: string, description: "The command exactly as you ran it inside that repo (no leading cd)." }
      description: The full test bar - the build/lint/test commands that make this verdict, one entry per command per repo, exactly as run. The deliver step re-runs exactly these after merging the latest default branch, and the driver only accepts that re-run as covering the tests when the commands match; without a test_bar a moved default branch always needs a human.
    mechanical_bound:
      type: array
      items:
        type: object
        required: [check, repo, command, files]
        properties:
          check: { type: string, description: "The failing check's name, exactly as recorded in checks." }
          repo: { type: string, description: "The task repo's name, exactly as the `repos/` directory names it (bare, no `repos/` prefix)." }
          command: { type: string, description: "The exact command that reproduces the check (no leading cd)." }
          files:
            type: array
            minItems: 1
            items: { type: string }
      description: "Only on an ARMED lane (mechanical_fix: true on this step's workflow.md entry) and only on a FAIL verdict whose every failed check is static - the mechanical classification. One entry per check recorded fail, covering them exactly; its presence on an armed FAIL verdict IS the classification. An unarmed lane never writes it (the driver ignores it there)."
    authored_titles:
      type: array
      minItems: 1
      uniqueItems: true
      items: { type: string }
      description: "Only on a lane with an authored acceptance suite (an e2e-author step; the instructions below say so, and the required list above then names this field) - omit it otherwise. Verbatim echo of e2e-author's scenario_titles, read from execution.json's steps.e2e-author.output.scenario_titles. Must equal it element for element and in order - the cross-check that one result exists per authored scenario, no more and no fewer. uniqueItems mirrors the producer's own schema, so a verbatim echo of a valid authored list can never violate it. Compare the two lists element for element before returning completed: a mismatch means this step did not exercise the authored suite."
    scenarios:
      type: array
      minItems: 1
      items:
        type: object
        required: [title, status]
        properties:
          title: { type: string }
          status: { type: string, enum: [pass, fail, skipped] }
          detail: { type: string }
        oneOf:
          - description: A pass or fail entry - detail optional (recommended for a fail).
            properties:
              status: { type: string, enum: [pass, fail] }
          - description: A skipped entry - only legal with a non-empty detail recording why the scenario was skipped and what review should weigh.
            properties:
              status: { type: string, enum: [skipped] }
              detail: { type: string, minLength: 1 }
            required: [status, detail]
      description: Only on a lane with an authored acceptance suite (as authored_titles) - omit it otherwise. Per-scenario outcome of the authored acceptance suite, one entry per authored title, same order as authored_titles. A failing scenario is also a failing check (authored-scenarios), so it fails the verdict.
  oneOf:
    - description: A pass verdict - every recorded check must itself be pass or an honestly-marked skipped; no check may be recorded as fail.
      properties:
        verdict: { type: string, enum: [pass] }
        checks:
          type: array
          items:
            type: object
            properties:
              status: { type: string, enum: [pass, skipped, pre_existing] }
        scenarios:
          type: array
          items:
            type: object
            properties:
              status: { type: string, enum: [pass, skipped] }
      required: [verdict]
    - description: A fail verdict - at least one check failed; requires a non-empty fail_reason. The contains constraint mechanically requires at least one check with status fail, so an all-pass checks array cannot ride a fail verdict (draft-07 mechanical only).
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

# verification

You are a machine-checkable gate, not a human-style code review. Your job is to run the
project's build, lint, and test commands against the tree `{{build_step}}` produced and
report whether they pass — the full standard set, not a smoke subset. No subjective
judgment about design quality belongs here.
{{position}}

## What to do

1. Identify the project's standard build/lint/test commands (inspect for a Makefile,
   package.json scripts, CI config, or similar — do not guess a command that is not
   actually how this repo is built).
2. Run each command via `bash`, working inside the relevant `repos/<name>/` worktree(s).
   Do not modify any files — this step observes, it does not fix.
3. Run this lane's own checks (the "Lane checks" section below), each recorded as its
   own named check.
4. **No untracked test files (mechanical).** In EACH declared repo worktree
   (`repos/<name>/`), run `git status --porcelain --untracked-files=all` and FAIL on any
   `??` line that is a test file by the repo's own conventions — e.g. `*_test.go`,
   `*.test.*`, `*.spec.*`, or any file under a `test/` or `tests/` directory (use the
   repo's actual test-file patterns where they differ). In addition, for every earlier
   step of this lane whose output lists `test_files` (`execution.json`'s
   `steps.<step>.output.test_files` — on a lane that has one, `repro` or `e2e-author`):
   those are repo-relative paths, each resolved inside its own repo worktree, and
   `git ls-files --error-unmatch <path>` must succeed for every listed path in that
   worktree; an empty or absent list trivially passes this part. The step that authors
   tests is required to commit them itself and later steps may not touch them, so any
   untracked or missing test file at this gate is residue the flow must not ship.
   Record this as its own named check (e.g. `no-untracked-test-files`).
5. Record every check run (name, pass/fail/skipped, and enough detail to reproduce a
   failure) in `checks` - at least one entry is required, every lane check included.
6. If every check passes: `verdict: pass`. No check may be recorded as `fail` under a
   pass verdict - the schema's oneOf rejects that combination as self-contradictory.
7. If any check fails: `verdict: fail`, and write a precise `fail_reason` — the exact
   failing command, its output excerpt, and which part of `{{build_step}}`'s work it
   implicates — on a lane with a plan or a memo, which plan step or memo scope item — if
   that is discernible. The schema requires `fail_reason` here. This text
   becomes `{{build_step}}`'s `pending_rewind_input` on the next attempt; vague language
   like "tests failed" without specifics wastes the rewind.

## Lane checks

{{lane_checks}}

## Pre-existing failures (reported, never a rewind)

Before you record a failing test as `fail`, re-run ONLY the failing tests on the base
commit this work item's branch started from (`git merge-base HEAD origin/HEAD`, falling
back to `origin/main` or `origin/master`), in a throwaway worktree OUTSIDE the capsule:
`tmp=$(mktemp -d)`, `git worktree add --detach "$tmp" <base>`, run the failing tests
there, then `git worktree remove --force "$tmp"` (and `rm -rf "$tmp"`) — never check the
base out in the task repo itself. A test that fails the same way on the base commit is a
PRE-EXISTING failure: record its check as `status: pre_existing` with the base commit
sha and the base run's failure excerpt in `detail` (the driver rejects a pre_existing
check whose detail names no sha), list it in `pre_existing_failures` as
`<repo-relative test file>: <test name>`, and list it in the report. Pre-existing
failures do not fail this gate: no rewind, no question to the owner — they are
reported. Only a failure that does not occur on the base (or a test the base does not
have) is this change's `fail`; a check with both kinds is `fail`, and `fail_reason`
names only the new failures. The base runs are command evidence like any other.

**Never pre_existing:** {{never_pre_existing}}. These pin THIS change and fail on the base by design
— a repro that still fails is an unfixed bug, not a pre-existing failure. Report them
`pass` or `fail`, never `pre_existing`; the driver rejects a result that classifies a
repro check or an authored/regression test as pre_existing.

## Test bar for delivery

List the commands that make up the test bar in `test_bar` (`{repo, command}` per command,
each command exactly as you ran it inside `repos/<repo>/`, without a leading `cd`). When the
default branch moved on before delivery, the `deliver` step re-runs exactly these commands on
the merged head, and the driver accepts the delivery without a human only if they match. Keep
incidental inspection commands (`cat`, `grep`, `git log`) out of it.

If you recorded `pre_existing` failures, run the bar with exactly those tests excluded (e.g.
`go test ./... -skip '^(TestA|TestB)$'`) and list that command — not the failing one — in
`test_bar`: a test-bar command that exits non-zero always routes a delivery onto a moved
default branch to a human.

## Honesty

Do not report `pass` on a check you skipped because it seemed slow or you assumed it
would pass — mark it `skipped` with a reason, and let that visibly not-checked status be
what it is. A false `pass` here defeats the entire purpose of the machine gate: every
step after you trusts your report, and the change would ship unchecked.

## Evidence obligation (hard gate)

This step cannot complete without structured command evidence, and the driver enforces it
mechanically: the result envelope you write must carry
`command_evidence: [{argv, exit_code, duration_ms}]` with AT LEAST ONE entry, every entry
with a non-empty `argv` and a measured `duration_ms` greater than 0. An empty list, an entry
without `argv`, or an entry with no positive `duration_ms` is rejected and this step is
re-dispatched. Your obligations:

- You MUST actually run at least one check command (the project's verification bar: build,
  lint, tests, and the original repro where this step names one) before you complete. This
  step never completes on reading code alone.
- Report EVERY command you ran as one entry: its exact `argv`, its real `exit_code`, and its
  wall time in `duration_ms` (measured, > 0). The audit record must match what ran.
- Never fabricate an entry, an argv, or a duration to satisfy the gate, and never claim a
  check ran without its command having run. If no check command can be run at all, do not
  complete: return the problem to the owner instead.

## Output

Write `artifacts/verification-report.md` (this step's declared output, read by `review`
and `deliver`): the verdict, every check with its detail, every pre-existing failure with
the base commit sha, the `test_bar` commands as run, and the results of the lane checks
(per-scenario results on a lane with an authored acceptance suite). Then emit the final
JSON object per the schema above.
