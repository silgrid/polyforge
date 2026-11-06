---
name: verification
description: Machine-checkable gate - the build, lint, and tests (plus the lane's own checks named in the step entry's vars) pass on the merged tree - what the build step produced merged with the latest origin default branch - the machine gate after review, before delivery. Shared by every delivery bundle; the lane-specific text comes from the workflow.yaml step entry's vars.
output:
  type: object
  required: [verdict, checks]
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
      description: Per-check breakdown (build, lint, tests, and every lane check the instructions below name, each as its own named check), in the order run - `pf verify-run`'s `result.json` `checks` first, verbatim, then your own. At least one check must be recorded (minItems 1) - a verdict over zero checks is an unfilled template.
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
      description: The full test bar - the build/lint/test commands that make this verdict, one entry per command per repo, exactly as run. The deliver step re-runs exactly these commands only when its own merge moves HEAD past the merged head this run proved, and the driver only accepts that re-run as covering the tests when the commands match; without a test_bar a moved default branch always needs a human. Exception - an opted-in repo (smoke_suite=) carries its FULL e2e command here in place of the selection it ran, and deliver re-runs it even at the verified head.
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

Verify the change: run the bar and report each check.
