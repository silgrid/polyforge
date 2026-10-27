---
name: review
description: Human-judgment gate over the completed change - the lane's own conformance judgment (from the workflow.md step entry's vars) plus the fixed owner-rules checklist - after the machine gate, before delivery. Shared by every delivery bundle with a review.
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
          text_only:
            type: boolean
            description: Optional per-finding classification (polyforge#222). True only when this finding's fix touches nothing but comments and documentation, in any file, test files included - the driver then rewinds {{build_step}} light (a resume of its previous session, this verdict as its rewind input) instead of fresh. Absent or false means the fix is structural (see the text-only boundary in the body) and the rewind is fresh. A fix that would change polyforge's own runtime behavior - a user-facing string literal, a workflows/** .md file, skills/** prompt material - is structural whatever the file type; never mark it text_only.
      description: Individual review findings, whether or not they are severe enough to fail the gate. A pass verdict with a blocking finding is self-contradictory - the schema's oneOf forbids it. The text_only marker rides blocking findings only into the light-rewind eligibility - non-blocking findings never enter that rule.
    fail_reason:
      type: string
      description: Required when verdict is fail - a synthesis of the blocking findings, to be carried back to the step this gate rewinds to as its pending_rewind_input. The oneOf split below makes the requirement mechanical - a fail verdict without a non-empty fail_reason does not validate.
    scope_adequate:
      type: boolean
      description: "Required whenever verification ran scoped (its result carried a `scope` object): whether the declared scope actually covered the change. `false` does not fail this review - it only blocks the automatic merge, routing the delivery to the human question. Omit it entirely when verification ran full."
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

- Run the owner-rules checklist below — any hit is a blocking finding.
- **Answer `scope_adequate` when verification ran scoped.** When `steps.verification`
  completed with a `scope` object, record a boolean `scope_adequate`: whether that
  declared scope actually covered the change. `false` does **not** fail this review and
  does not rewind to `{{build_step}}` — it only blocks the automatic merge, so the
  delivery asks the human. The driver rejects a completed review over a scoped
  verification that omits the field; a full verification requires nothing and an absent
  field there is never a finding.
- Record every finding in `findings`, tagged `blocking` or `non-blocking`.
  Non-blocking findings (style nits, minor improvement suggestions) do not fail the
  gate but are still useful history, even when the overall verdict is pass.
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
- **Judge a light round for mis-marking.** Read `execution.json`'s attempts ledger
  (`runtime-state/execution.json`). When `steps.{{build_step}}.attempts`'s latest
  rewind-archive attempt — namely
  the latest attempt whose cause is neither `accepted` nor `infra_failure` —
  carries `cause: gate_fail_rewind_text_only`, this re-run follows a light rewind:
  judge the light round's changes against the text-only boundary above, on top of
  your normal full-change judgment (which is unchanged and stays the
  adjacent-issue catcher). Anchor the light round to that record, not to
  `steps.{{build_step}}.output.commits` (a resumed session may list its earlier
  commits again): the `gate_fail_rewind_text_only` attempt's
  archived `result` holds the pre-light output and its `commits`, and the light
  round is what changed after the last sha that list names, per task repo
  (`git diff <that sha>..HEAD`). Any structural change in the light round is a
  BLOCKING mis-marking finding: record it WITHOUT `text_only` (it is structural),
  so the resulting FAIL rewinds fresh with its own rewind slot. A light round
  that stayed text-only records no mis-marking finding — a pass is possible if
  nothing else is wrong.

**Do not run tests or builds.** This step reads and judges; it never runs the build, the
test suite (`go test ./...`, `make test`, the full `./test`, `npm test`) or an e2e suite.
The full bar ran exactly once, in `verification` (before this step; `deliver`, after you,
re-runs that same bar only after merging a moved default branch changed the tree): its
report (`artifacts/verification-report.md`) is this step's test evidence. Reading a test
file's source is part of the review; running it is not.

## Re-entry

This section changes nothing about the context policy: `review` always runs **fresh** —
the conversation of a prior review round is never inherited. On a re-entry the only
thing inherited is the prior round's **structured findings list**, read from the
archive (`artifacts/steps/review/`), never from the conversation; a fresh context is
the point, so the new reviewer does not inherit the old reviewer's blind spots.

Detect a re-entry by listing `artifacts/steps/review/`: every `s<n>/result.json` whose
`n` is earlier than this dispatch's derivation ordinal is a prior round. When there is
no such archive this is the **first round**, and nothing below applies — the review is
exactly the full judgment it has always been.

On a re-entry, run two additive faces over the current tree:

- **checklist face** — take the latest prior round's findings (the `findings` array of
  its archived `result.json`) and verify each finding's disposition on the current tree
  — fixed, or waived with an owner ruling on record. A prior **blocking** finding that
  is still present is a **residual**: report it as a `blocking` finding and reference
  the original by its position in that round's findings list (e.g. "prior round s<n>
  finding #k") **without re-deriving** it as a new finding — it is the same finding,
  still open, not a fresh one. A prior **non-blocking** finding that is still present
  is recorded `non-blocking` and never fails the gate.
- **fresh face** — the full judgment above still runs over the whole change, now
  including the fixes' new surface (the commits since the prior round and their
  semantic propagation), from the fresh context. Genuinely new defects are new findings
  and are reported as such.

The light-round mis-marking duty above still runs; the two faces are additive, not
alternatives.

## Principles

Every principle is a check (a violation is blocking); the checklist spells them out.

{{principles}}

## Owner-rules checklist (any hit is a blocking finding)

On top of the lane judgment above, check the diff against the owner's
fixed rules. ANY hit is a blocking finding - there is no pass verdict with one of
these present:

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
- **Unruled destination change.** A feature removed or a guarantee weakened without an owner ruling (a shorter path with the same destination is not a finding).
- **I/O under a lock.** Network or disk I/O while holding a lock.
- **Weak tests.** A new test that passes on the old code, or an assertion relaxed
  without a stated reason.
- **Stale docs.** Docs not updated with the code they describe.
- **Leaked credentials** in an agent's env, a command line, a log or an artifact.
- **Guess as fact.** An unverified claim stated as fact.
- **Prior experience ignored.** The change repeats a pitfall or a failed approach that a recalled memory records, without saying why this time differs; or the step skipped its `## Prior experience` section.

## Position in the flow

In every delivery bundle that has a review this step sits between `verification`
and `deliver`: the mechanical gate runs first, so a mechanical failure never costs a
review, and the judgment reads its results.

## Output

Write `artifacts/review.md` (this step's declared output): the verdict, every
finding with its severity, and the `fail_reason` on a fail. Then emit the final JSON
object per the schema above.
