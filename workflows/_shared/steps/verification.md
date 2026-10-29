---
name: verification
description: Machine-checkable gate - the build, lint, and tests (plus the lane's own checks named in the step entry's vars) pass on the merged tree - what the build step produced merged with the latest origin default branch - the machine gate after review, before delivery. Shared by every delivery bundle; the lane-specific text comes from the workflow.md step entry's vars.
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
      description: The full test bar - the build/lint/test commands that make this verdict, one entry per command per repo, exactly as run. The deliver step re-runs exactly these commands only when its own merge moves HEAD past the merged head this run proved (and unconditionally after a light pass), and the driver only accepts that re-run as covering the tests when the commands match; without a test_bar a moved default branch always needs a human.
    mechanical_bound:
      x-prompt-when: armed
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
      x-prompt-when: authored
      type: array
      uniqueItems: true
      items: { type: string }
      description: "Only on a lane with an authored acceptance suite (an e2e-author step; the instructions below say so, and the required list above then names this field) - omit it otherwise. Verbatim echo of e2e-author's scenario_titles, read from execution.json's steps.e2e-author.output.scenario_titles. Must equal it element for element and in order - the cross-check that one result exists per authored scenario, no more and no fewer. uniqueItems mirrors the producer's own schema, so a verbatim echo of a valid authored list can never violate it. Compare the two lists element for element before returning completed: a mismatch means this step did not exercise the authored suite. Empty (with an empty scenarios) only when the lane's e2e-author mode is `skipped-with-rationale`."
    scenarios:
      x-prompt-when: authored
      type: array
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
      description: Only on a lane with an authored acceptance suite (as authored_titles) - omit it otherwise. Per-scenario outcome of the authored acceptance suite, one entry per authored title, same order as authored_titles. Empty only when the lane's e2e-author mode is `skipped-with-rationale`; otherwise at least one entry. A failing scenario is also a failing check (authored-scenarios), so it fails the verdict.
    scope:
      x-prompt-when: scoped
      type: object
      description: "Only on a work item that opted into the scoped tier (its content carries the marker line `verification: scoped`, and the lane is scoped-capable). Omit it entirely on the full tier - an absent scope is the single canonical shape of a full result. The driver rejects an incomplete declaration and any scoped claim on a work item that did not opt in; it never classifies the declared paths."
      required: [mode, declared_paths, rationale, fast_gates_ref]
      properties:
        mode: { type: string, enum: [scoped] }
        declared_paths:
          type: array
          minItems: 1
          items: { type: string, minLength: 1 }
          description: This run's agent-declared coverage - opaque strings, never classified by the driver.
        rationale: { type: string, minLength: 1 }
        fast_gates_ref: { type: string, minLength: 1, description: "The release-shipped engine invocation this run used (`<data-dir>/release/current/tools/verify-scope`, default `~/.local/share/polyforge/release/current/tools/verify-scope`) and whether a repo `.polyforge/verify-scope.conf` applied - `pf verify-run`'s `result.json` `scope.fast_gates_ref`, verbatim." }
        prior:
          type: object
          required: [tier, declared_paths]
          properties:
            tier: { type: string, enum: [scoped] }
            declared_paths:
              type: array
              minItems: 1
              items: { type: string }
          description: Leave it out - every run scopes the whole diff (merge-base..HEAD), so a re-entered round declares the scope review recorded for that round in declared_paths and carries no prior.
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
          x-prompt-when: authored
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

You are a machine-checkable gate, not a human-style code review: report whether the
project's build, lint, and test commands pass against the tree `{{build_step}}` produced
merged with the latest origin default branch — the full standard set, not a smoke subset.
No subjective judgment about design quality belongs here.
{{position}}
The tier this dispatch runs at rides the driver's `# Verification tier` section below; on a
scoped dispatch it also carries the scoped rules (the contract and every lane check are never shed).

The mechanical half — the merge, the whole bar, flaky re-runs, pre-existing proof, the
test-file checks, the test bar — is ONE command, `pf verify-run`. Your half is judgment.

## Run `pf verify-run` exactly once

From the capsule root, in the foreground:
`pf verify-run --tier <full|scoped> --engine <data-dir>/release/current/tools/verify-scope --deadline <D>`

- `--tier` is what the `# Verification tier` section names (FULL → `full`, SCOPED →
  `scoped`); never guess it. `<data-dir>` is `$POLYFORGE_DATA_DIR` when set, else
  `~/.local/share/polyforge` — the release-shipped scope engine, which a repo may steer
  with a `.polyforge/verify-scope.conf`.
- One blocking call: give it an explicit timeout as long as your shell tool allows, pass
  `--deadline` about 2 minutes under that timeout, and wait for it. Nothing runs past the
  deadline (a command that can no longer start is recorded `fail`) and the result is
  written before it. Never background it, never poll for its completion, never split its
  work into calls of your own. A re-dispatch calls it once again (a new `runtime-state/verify/<n>/`).
- Read the `result.json` it names: the whole mechanical record (command logs stay local
  under `runtime-state/verify/<n>/`, never in `artifacts/`).
- Exit 0 means a result was produced — a red bar is data, read it. Exit 1 with
  `incomplete:` means no merged-tree proof could be made: return outcome `incomplete` with
  that reason verbatim. Any other exit 1 means it could not run: correct the invocation
  once, else return `incomplete` with its error — never run the bar by hand instead.
- A light recheck dispatch (its prompt carries the mechanical fast lane's light-recheck
  section) does NOT call it: that section overrides this definition.

## Merge the latest default branch before the bar (the bar proves the merged tree)

The tree your bar proves is the tree that will be delivered, so `pf verify-run` first merges
the latest origin default branch into the branch under test, in each `repos/<name>/`:

1. `git fetch origin`, then it resolves the default branch
   (`git symbolic-ref --short refs/remotes/origin/HEAD | sed 's|^origin/||'`, falling back
   to `main`, then `master`) - call it `<default>`.
2. `git merge --no-edit origin/<default>`. Both are command evidence like any other
   (`merge_outcomes.<repo>` records the outcome). "Already up to date" is a legal no-op: it
   creates no commit, and the bar still runs on that HEAD.
3. Every build/lint/test and lane-check command runs AFTER that merge, on the merged
   HEAD, yours included. The driver stamps the repo's HEAD at your completed pass as
   `verified_heads.<repo>`, so the proof must be of this merged HEAD.

The merge commit MUST be machine-made - exactly `git merge --no-edit origin/<default>`,
never a rebase, squash, cherry-pick or hand edit (deliver's merge rule re-derives merges
with `git merge-tree --write-tree`). Never merge or commit in a task repo yourself.

- **A failed fetch:** when `git fetch origin` (or resolving `<default>`) fails, `pf verify-run`
  exits with `incomplete:` and the error: stop with outcome `incomplete` and the exact error,
  as deliver does — a human fixes the machine and resumes. Never run the bar on the
  unfetched tree, and never report a pass as the merged-tree proof.
- **A conflict:** it runs `git merge --abort`, confirms the pre-merge tree is restored and
  runs the full bar on that unmerged tree (`conflict-aborted`); take the verdict from it.
  Never resolve the conflict - it is deliver's problem.
- **A crash or restart mid-merge:** `verified_heads` is written only when your result
  completes, so an interrupted dispatch leaves no proof;
  a resumed dispatch simply repeats the fetch and merge ("Already up to date" when
  already merged; a merge left in progress is aborted first).

The pre-existing-failure base is therefore the merged-in default tip.

## Your part

1. Start `checks` with `result.json`'s `checks`, verbatim and in order, and carry its
   `pre_existing_failures`, `test_bar` and `command_evidence` verbatim. Never change a
   status it recorded. What they mean: a repo with no commit beyond its default branch is
   `<repo>:untouched` (skipped, no bar); each planned command is `<repo>:<name>` — the WHOLE
   plan ran, a failure never stopped the rest; a command whose declared precondition is
   unmet is `skipped` naming it; a test that passed on a re-run at HEAD is `pass` with
   `flaky n/m`; `<repo>:no-untracked-test-files` (the `no-untracked-test-files` check: no
   untracked test file before or after the bar, and every earlier step's `test_files`
   tracked) and `test-files-present` catch test residue the flow must not ship.
2. **Unplanned repos** (`unplanned_repos`, their `<repo>:plan` skipped): identify that
   repo's standard build/lint/test commands (Makefile, package.json scripts, CI config —
   never guess), run each via `bash` inside `repos/<name>/`, and record each as a check, a
   `test_bar` entry and command evidence.
3. Run the lane checks below, each its own named check. Do not modify any files.
4. Scoped tier: declare `scope` only when `result.json`'s `tier.effective` is `scoped` —
   `declared_paths` verbatim from `steps.review.output.scope`, `fast_gates_ref` from its
   `scope.fast_gates_ref`, a factual `rationale` (the opted-in tier, the engine's
   `scope.paths`, the commands run), and no `prior` (every run covers the whole diff). When
   it is `full`, carry no `scope`.
5. `verdict: pass` when no check is `fail` (`skipped` and `pre_existing` do not fail it).
   Otherwise `verdict: fail` with a precise `fail_reason` — start from `result.json`'s
   `fail_reason` (each failing command, its new failures, its log), add the lane checks'
   failures and which part of `{{build_step}}`'s work it implicates (on a lane with a plan
   or a memo, which plan step or memo scope item). It becomes
   `{{build_step}}`'s `pending_rewind_input`; "tests failed" without specifics wastes the rewind.

## Lane checks

{{lane_checks}}

## Pre-existing failures (reported, never a rewind)

`pf verify-run` re-runs ONLY a failing test on the base (`merge_outcomes.<repo>.base`) in a
throwaway worktree OUTSIDE the capsule, after proving the base has that test, and calls it
pre-existing only when its failure in the bar, its isolated re-run at HEAD and every base run
sign the same (every assertion line of the failure with its body); a test the base lacks, an environmental failure or a different
failure is this change's `fail`, and a failure no test locates is never pre-existing. Such
a check is `status: pre_existing` with the base commit sha in `detail` and its
`<repo-relative test file>: <test name>` entry in `pre_existing_failures`. Pre-existing
failures do not fail this gate: no rewind, no question to the owner — they are reported.
A check with both kinds is `fail`, and `fail_reason` names only the new failures. A failure
in a command you ran yourself is pre-existing only by the same proof done by you
(`tmp=$(mktemp -d)`, `git worktree add --detach "$tmp" <base>`, run only those tests there,
`git worktree remove --force "$tmp"`) — never check the base out in the task repo itself.

**Never pre_existing:** {{never_pre_existing}}. These pin THIS change and fail on the base by design
— a repro that still fails is an unfixed bug, not a pre-existing failure. Report them
`pass` or `fail`, never `pre_existing`; the driver rejects a result that classifies a
repro check or an authored/regression test as pre_existing.

## Test bar for delivery

`test_bar` is `result.json`'s plus every bar command you ran yourself (`{repo, command}`,
exactly as run inside `repos/<repo>/`, no leading `cd`; no inspection commands). A command
carrying a pre-existing failure is listed with exactly those tests excluded (e.g.
`go test ./... -skip '^(TestA|TestB)$'`): a test-bar command that exits non-zero always
routes a delivery onto a moved default branch to a human. `test_bar_notes` name a command
with no such switch; say so in the report.

## Honesty

Do not report `pass` on a check you skipped because it seemed slow or you assumed it
would pass — mark it `skipped` with a reason. A false `pass` here defeats the entire
purpose of the machine gate: every step after you trusts your report.

## Output

Write `artifacts/verification-report.md` (read by `deliver`): `report-mechanical.md` in
whole (merges, plans, every check, pre-existing failures with the base sha, flaky re-runs,
the `test_bar`), then the lane checks' results (per-scenario results on a lane with an
authored acceptance suite), any bar you ran yourself, and the verdict. An authored scenario
you skip is legal for this gate (an honest `skipped` with a non-empty `detail`) but it
blocks the automatic merge — `deliver` routes the delivery to a human naming the skipped
title. Then emit the final JSON object per the schema above, `command_evidence` being
`result.json`'s plus your own commands'.
