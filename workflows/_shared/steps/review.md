---
name: review
description: Human-judgment gate over the completed change - the lane's own conformance judgment (from the workflow.md step entry's vars) plus the fixed owner-rules checklist - after the build step, before the machine gate and delivery. Shared by every delivery bundle with a review.
output:
  type: object
  required: [verdict]
  properties:
    verdict:
      type: string
      enum: [pass, fail]
      description: pass means the change is ready to deliver as-is; fail means it needs another pass of the step this gate rewinds to.
    findings:
      type: array
      items:
        type: object
        required: [severity, detail]
        properties:
          severity: { type: string, enum: [blocking, non-blocking] }
          detail: { type: string }
          repair_target:
            type: string
            enum: [code, authored-suite, spec, declared-obligation, instruction]
            description: "Required on a blocking finding (absent or unknown = an unfilled result, rejected) - where the fix lands. `code`: {{build_step}} fixes it in its own step - every repair it can make in its own round is `code`, including updating a declared obligation the diff left unchanged, withdrawing a moot one in `withdrawn_obligations` and citing an undeclared file. Off-graph: `authored-suite`, `spec`, `declared-obligation` (reserved for a plan-declared obligation that is itself wrong and that {{build_step}} can neither satisfy nor withdraw), `instruction` (an un-IDed verification FAIL or human deliver instruction). Any `code` finding rewinds {{build_step}}; an all-off-graph FAIL asks the owner instead (amend-upstream / waive / cancel)."
          residual_of:
            x-prompt-when: reentry
            type: string
            description: "Optional per-finding reference. From the 2nd review round on, set it on a blocking finding that is a RESIDUAL of the previous verdict round's findings list - the prior round's `s<n>#k` position (e.g. `s4#2`), the same reference the checklist face records. Absent (or empty) = a new discovery. The driver reads the marker for rewind-quota classification only: a FAIL verdict whose blocking findings all carry it, and are strictly fewer than the last verdict round's, is a converging round and charges no gate→target edge slot (it still counts toward max_total). Never mark a new discovery with it - a round that mixes residuals with any unmarked blocking finding is charged like any fresh one."
          text_only:
            type: boolean
            description: Optional per-finding classification (polyforge#222) - true only when this finding's fix is text-only by the boundary the body defines once; the driver then rewinds {{build_step}} light instead of fresh. Absent or false means structural (a fresh rewind).
      description: Individual review findings, whether or not they are severe enough to fail the gate. A pass verdict with a blocking finding is self-contradictory - the schema's oneOf forbids it. The text_only marker rides blocking findings only into the light-rewind eligibility - non-blocking findings never enter that rule.
    fail_reason:
      type: string
      description: Required when verdict is fail - a synthesis of the blocking findings, to be carried back to the step this gate rewinds to as its pending_rewind_input. The oneOf split below makes the requirement mechanical - a fail verdict without a non-empty fail_reason does not validate.
    scope_adequate:
      x-prompt-when: scoped
      type: boolean
      description: "Required whenever the work item opted into the scoped tier (its content carries the marker line `verification: scoped` and the lane is scoped-capable): whether the scope you declared in `scope` actually covers the diff you judged. `false` does not fail this review - it only blocks the automatic merge, routing the delivery to the human question. Omit it entirely when the work item runs the full tier."
    scope:
      x-prompt-when: scoped
      type: object
      description: "The scope you judged the diff against, on a work item that opted into the scoped tier: `mode: scoped` plus the `declared_paths` (opaque strings) that cover the change you reviewed. The next step, `verification`, MUST verify with exactly this scope - the two declarations form one merging credential only when their path sets are equal, and any disagreement routes the delivery to the human. Record it together with `scope_adequate`; omit both on the full tier."
      required: [mode, declared_paths]
      properties:
        mode: { type: string, enum: [scoped] }
        declared_paths:
          type: array
          minItems: 1
          items: { type: string, minLength: 1 }
          description: "The one definition of declared_paths: the path set review judged in this round and verification verifies for that same round (opaque strings, never classified by the driver)."
  oneOf:
    - description: A pass verdict - zero blocking findings; any recorded findings must be non-blocking.
      properties:
        verdict: { type: string, enum: [pass] }
        findings:
          type: array
          items:
            type: object
            properties:
              severity: { type: string, enum: [non-blocking] }
      required: [verdict]
    - description: A fail verdict - at least one blocking finding exists; requires a non-empty fail_reason synthesizing them.
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

{{intro}}

## What to do

{{judge}}

Then, on every lane:

- Run the owner-rules checklist below — a violation of any AGENTS.md rule
  blocks at any position, in any round; any other checklist hit blocks on the
  first round (on a re-entry the round contract narrows that set).
- Record every finding in `findings`, tagged `blocking` or `non-blocking`.
  Non-blocking findings (style nits, minor improvement suggestions,
  follow-ups outside the round's blocking set) do not fail the gate but are
  still useful history — `deliver` summarizes every `non-blocking` item into
  the delivery note and the PR body, and no step ever files them
  automatically: the owner decides what becomes a ticket. A step's missing
  `## Prior experience` section (the shared recall duty asks it only of the steps
  whose entry declares `recall: true`) is never a blocking finding on its own -
  record it `non-blocking` if noted at all; it is a form gap, not a defect in the
  change.
- `verdict: pass` only if there are zero blocking findings — the schema enforces
  this: under a pass verdict every recorded finding must be `non-blocking`.
- `verdict: fail` if there is at least one blocking finding (the schema's fail
  branch requires the findings list to contain one); synthesize them into
  `fail_reason` — concrete and actionable enough that `{{build_step}}`'s next attempt
  (which receives this text as its `pending_rewind_input`) does not have to guess what
  to change. The schema requires `fail_reason` on any fail verdict.
- **Classify each finding's fix against the text-only boundary** (polyforge#222),
  defined here in exactly one place:
  - **text-only**: comments and documentation in any file, test files included;
  - **structural**: code, tests and assertions, user-facing string literals (error
    messages, log lines, CLI help), `workflows/**` `.md` files, and `skills/**` prompt
    material — prompts drive step agents, so their wording is polyforge's runtime
    behavior.

  Mark a blocking finding `text_only: true` only when its fix touches nothing but the
  text-only side; absence (or false) means structural. The driver acts on the marker as
  recorded — no re-classification, no owner confirmation: a fail verdict whose blocking
  findings (at least one) are all marked `text_only` rewinds `{{build_step}}` light (a
  resume of its previous session carrying this verdict), any unmarked blocking finding
  keeps the rewind fresh, and non-blocking findings never enter the rule.

**Do not run tests or builds.** This step reads and judges; it never runs the build,
the test suite (`go test ./...`, `make test`, the full `./test`, `npm test`) or an
e2e suite. The machine gate is `verification`, the step AFTER you: it runs the full
bar on the tree you judged merged with the latest origin default branch, and
`deliver` re-runs that same bar only when its own merge moves HEAD past the verified
head. You have no verification report to read — this
lane's order is `{{build_step}}` → review → verification → deliver, so judge the diff
itself: reading a test file's source is part of the review; running it is not.

This review always runs **fresh**. When a prior verdict-bearing review round is on record
the driver adds a `# Review re-entry` section (the round contract, the two faces, the
light-round mis-marking duty); without it this is the first round — the full judgment,
every checklist hit blocking.

## Principles

Every principle is a check (a violation is blocking); the checklist spells them out.

{{principles}}

## Owner-rules checklist (first round: any hit blocks; later rounds: the round contract)

On top of the lane judgment above, check the diff against the owner's
fixed rules. On the first round ANY hit is a blocking finding; on a re-entry
the round contract decides — a violation of any AGENTS.md rule blocks at any
position, and every other hit outside the contract's blocking set is recorded
`non-blocking` (a follow-up):

- **No faked concurrency semantics.** No timing-based fakes (`time.Sleep` as a
  synchronization stand-in, backoff tuned to "usually long enough") and no
  in-process locks/mutexes standing in for the cross-process or distributed mutual
  exclusion the semantics actually require.
- **No unrequested concepts.** No new states, fields, error codes, config items, or
  concepts {{scope_source}} did not request. List each one you find as its own
  must-fix finding.
- **No manual-gate bypass.** No code path that bypasses a manual checkpoint
  (`needs_input` / gate) - the flow's interactive checkpoints must stay
  load-bearing.
- **Spec-forced inventions.** If any of the change's own requirements
  ({{scope_source}}) forces an invented mechanism - e.g. it demands "the loser
  must be refused" where both serializations are valid, which is what drove a sleep+latch hack - do not pass
  the diff for faithfully implementing it: flag it as a blocking finding and
  propose the spec change in that finding instead.
- **Unruled destination change.** A feature removed or a guarantee weakened without an owner ruling (a shorter path with the same destination is not a finding; a removal the work item names, filed on `chore`, is ruled).
- **I/O under a lock.** Network or disk I/O while holding a lock.
- **Weak tests.** A new test that claims to pin new or changed behaviour yet passes on
  the old code (a characterization test of existing behaviour is exempt), or an
  assertion relaxed without a stated reason.
- **Stale docs.** Docs not updated with the code they describe.
- **Leaked credentials** in an agent's env, a command line, a log or an artifact.
- **Guess as fact.** An unverified claim stated as fact.
- **Prior experience ignored.** The change repeats a pitfall or a failed approach that a recalled memory records, without saying why this time differs.

## Position in the flow

In every delivery bundle that has a review this step sits between the build step
(`{{build_step}}`) and `verification`: the judgment runs first, so a quality
failure never costs a full machine bar, and the machine gate behind you verifies
the tree you already judged merged with the latest origin default branch. A review
pass records the tree it covered (the driver writes the `reviewed_heads`); any code
change after it invalidates the pass, and the delivery needs both coverages — the
review of the final diff and the verification of the final tree.

## Output

Write `artifacts/review.md` (this step's declared output): the verdict, every
finding with its severity (and, from round 2 on, its `residual_of` reference on
a residual), and the `fail_reason` on a fail. Then emit the final JSON object
per the schema above.
