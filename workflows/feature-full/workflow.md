---
name: feature-full
steps:
  - { id: grill, context_policy: fresh, handoff: true }
  - { id: spec, context_policy: inherit }
  - { id: grill-review, context_policy: fresh, handoff: true, requires_human_answer: true, gate: true, on_fail: { target: spec } }
  - { id: plan, context_policy: inherit }
  - { id: e2e-author, context_policy: fresh, handoff: true, requires_command_evidence: true }
  - { id: code, context_policy: inherit }
  - id: verification
    context_policy: fresh
    gate: true
    requires_command_evidence: true
    on_fail: { target: code }
    model_candidates: [pi:sub2api-glm/glm-5.3-flash, pi:sub2api-glm/glm-5.3, pi:sub2api-claude/pi-claude-opus-5]
    vars:
      output_required: verdict, checks, authored_titles, scenarios
      build_step: code
      position: >-
        Specific to this lane, you also run the acceptance scenarios `e2e-author` authored
        and proved red before `code` ran. Design quality is `review`'s job — the step right
        after this one; it reads your report.
      lane_checks: |-
        **Authored acceptance scenarios.** Copy `steps.e2e-author.output.scenario_titles`
        verbatim into `authored_titles` and record one `scenarios` entry per title, same
        order — on this lane both fields are required. If the full bar already ran
        `e2e-author`'s test files (e.g. `go test ./test/` covers `test/...`), read each
        scenario's result from that run. If it did not (e.g. the project's e2e runner is not
        part of its unit test command), run those files with a targeted command and add that
        command to `test_bar`, so the acceptance tests are re-run on delivery too. A
        document-only scenario is driven through the project's actual interfaces and judged
        against its THEN clause. Never rewrite, relax or skip a scenario because the
        implementation does not satisfy it — record `fail` with a detailed `detail`. A skip
        is legal only with a non-empty `detail` and does not fail this gate; `review` judges
        it. Record the suite as its own named check (`authored-scenarios`): `fail` if any
        scenario failed. For a failing authored scenario, `fail_reason` names its title and
        expected vs observed.
      never_pre_existing: the authored acceptance tests (`steps.e2e-author.output.test_files`) and any repro check
  - id: review
    context_policy: fresh
    gate: true
    on_fail: { target: code }
    vars:
      build_step: code
      scope_source: the spec
      intro: |-
        Judge whether this change is ready to deliver. Unlike `verification`, this
        step is a genuine business/quality judgment call, not a mechanical check — it exists to
        catch what automated checks cannot: design fit, scope creep or scope gaps against the
        spec, code quality, and whether the authored scenarios actually demonstrate the spec's intent
        rather than merely passing on a technicality.
      judge: |-
        1. Read, in this order: `artifacts/spec.md` (what was supposed to happen),
           `artifacts/plan.md` (how it was supposed to be built), the full diff (what was
           really built, tests included — see below), and `artifacts/verification-report.md`
           (the machine gate's results, with the per-scenario results of the authored
           acceptance suite). Judge whether `verification`'s scenario skips, if any, are
           acceptable — skips do not fail that gate; you do.
        2. Check the implementation against the spec's requirements and acceptance criteria one
           by one — do not rubber-stamp because verification passed; the mechanical gate
           checks different things than "is this the right change."
        3. Judge the full diff, including the tests. Read the whole change, not only `code`'s
           commits: in each task repo, `git diff $(git merge-base HEAD origin/HEAD)..HEAD`
           (fall back to `origin/main` or `origin/master` when `origin/HEAD` is unset — the same
           base `verification` uses for pre-existing failures). That diff carries
           `e2e-author`'s test commit and any commit `code` did not report. Judge the tests as
           part of the change:
           - **Wrong or vacuous tests are a blocking finding.** An assertion that does not
             actually test the spec's behaviour, or a test that would pass whatever the
             implementation does. `code` may not change authored tests on its own (the driver's
             authored-tests gate); after your FAIL it asks a human through `needs_input`, and the
             approved change becomes the new baseline — say precisely which test and why.
           - **Scenarios deleted as already satisfied.** Every title in
             `steps.e2e-author.output.already_satisfied` must really be existing behaviour; a
             new-behaviour scenario deleted because it was written wrong is a blocking finding.
           - **New-behaviour coverage.** Every new-behaviour acceptance criterion of the spec has
             a scenario; a missing one is a blocking finding.
           - **Redundant or oversized tests are non-blocking.** A scenario that re-tests
             existing behaviour or duplicates an existing test, or a suite above the 400-line
             soft cap (`added_test_lines`) whose `size_justification` does not hold up: record it
             as a `non-blocking` finding — removing a test would itself need a human round trip
             under the authored-tests gate, which is not worth it for size alone.
  - { id: deliver, context_policy: fresh, gate: true, requires_command_evidence: true, on_fail: { target: code } }
defaults:
  model_candidates: [pi:sub2api-glm/glm-5.3, pi:sub2api-claude/pi-claude-opus-5, pi:sub2api-openai/gpt-6-astra]
---

# feature-full — reference flow

This is the flow-level narrative for the workflow. It explains how the
nine steps compose and what flows between them; it is not itself a structural
schema — the frontmatter `steps` array above is the *only* execution order. Nothing here overrides it.

## Shape of the flow

```
grill --> spec --> grill-review --> plan --> e2e-author --> code --> verification --> review --> deliver
```

The four gate FAIL edges, each declared in the frontmatter as that gate's `on_fail`
rewind target: `grill-review` --> `spec`, `verification` --> `code`, `review` -->
`code` and `deliver` --> `code`.

The flow has two front-loaded interactive checkpoints (`grill`, `grill-review`) that
replace a heavy spec-review loop with a leaner two-pass confirmation, an acceptance
step that runs BEFORE the build (`e2e-author` authors scenarios for the spec's new
behaviour, commits them and proves each one red against the pre-implementation tree
in the same step), a build step that implements against that executable acceptance
(`code` continues `e2e-author`'s session via its inherit handoff), the machine gate
(`verification` runs the full bar and reports each authored scenario, rewinding to
`code` on red), the judgment gate (`review` reads the full diff, tests included, and
the verification report) and `deliver`. Every delivery bundle runs `verification`
before `review`: a mechanical failure never costs a review.

## Artifact flow between steps

Every step's output is a path recorded in `execution.json`'s `steps.<id>.output`, pointing at a
file under `artifacts/`; no step declares frontmatter `inputs:`, so downstream steps read upstream artifacts by that convention:

| step | primary artifact it writes | who reads it |
|---|---|---|
| `grill` | `artifacts/grill-qa.md` (grill section) | `spec` |
| `spec` | `artifacts/spec.md` | `grill-review`, `plan`, `e2e-author`, `review` |
| `grill-review` | `artifacts/grill-qa.md` (grill-review section, appended) | `spec` (only on rewind, via `pending_rewind_input`) |
| `plan` | `artifacts/plan.md` | `code` |
| `e2e-author` | `artifacts/e2e-scenarios.md` with its `## Baseline run` section (the proven-red bar: one fail entry per scenario with evidence) + the committed test files under `repos/<name>/**` if the project runs them from the tree | `code` (primary, via its inherit handoff), `verification`, `review` |
| `code` | changes under `repos/<name>/**`, `artifacts/code-summary.md` | `verification`, `review` |
| `verification` | `artifacts/verification-report.md` (verdict, full bar + per-scenario results of the authored suite) | `review`, `deliver` |
| `review` | `artifacts/review.md` (verdict) | `deliver` |
| `deliver` | `artifacts/delivery.md` | (terminal) |

`grill` and `grill-review` share one running document, `artifacts/grill-qa.md`, sectioned
by step id: `grill` appends its question/answer pairs first, `spec` reads the whole file
for context, and `grill-review` appends its own confirmation pass underneath.

## Rewind boundaries

Every gate in this flow declares its rewind target in the frontmatter `on_fail` — four
gates, two distinct targets (`spec`, `code`). This section only explains
*why* each target was picked:

- **`grill-review` FAILs -> rewind to `spec`.** The user's confirmation pass found the
  spec itself wrong (not just the confirmation script). Rewinding to `spec` re-runs
  spec authoring with the FAIL verdict attached as input; `grill` is *not* re-run —
  the raw requirements gathered there are still valid, only the spec's interpretation
  of them needs correction.
- **`verification` FAILs -> rewind to `code`.** The full bar or an authored scenario
  is red: an implementation failure, not review material — the bar was proven red and
  visible before `code` ran. Rewinding to `code` re-runs the build step with the
  verdict attached as `pending_rewind_input`; the authored scenarios are still valid,
  so `e2e-author` is not re-run.
- **`review` FAILs -> rewind to `code`.** Review found the change wanting after the
  machine gate passed; `code` is re-run with the review verdict attached, and the
  re-run passes through `verification` again. `plan` is not re-run unless the
  reviewer's verdict says the plan itself was wrong, in which case the plan step's own
  re-authoring happens as part of the `code` step consuming the verdict text (the flow
  does not need a third rewind target for this). A blocking finding against an
  authored test sends `code` through `needs_input`: it may not change those tests
  without a human answer (the driver's authored-tests gate).
  An all-text-only review FAIL (every blocking finding marked `text_only: true`) takes
  the light path: `code`'s previous session resumes with the verdict, applies only
  text-only fixes, and the gate tail (`verification`, then a fresh `review`) re-runs
  unchanged.
- **`deliver` "back to code" -> rewind to `code`.** A human chose to send the change
  back instead of merging it.

`code`'s per-step rewind allowance (`max_per_step`) is shared by the three gates that
target it (`verification`, `review`, `deliver`).

Workflow-level rewind bounds: this bundle's frontmatter deliberately omits the
`rewind` block, so the driver defaults apply — `max_total: 4`, `max_per_step: 2`. A gate that keeps failing past those bounds ends
the workflow in `failed_rewind_exhausted`, not an infinite loop.

## Budget guidance

No workflow-level budget and no step `budget.wall_minutes`: the bounds are the rewind limits
above and the daemon's per-step runtime bound T (README, `pf daemon` flags).

## Delivery and releases

`deliver` runs last, after every gate passed; its contract (merge the latest default branch,
push only the feature branch, open or reuse the PR, merge under the merge rule or ask a
human) is the shared step definition `workflows/_shared/steps/deliver.md`. Lane facts:
its "back to code" answer rewinds to `code`, and the independent review it reads is this
workflow's `review` gate (`steps.review.output`). There is no release slot: releases and
deploys happen only through the separate `release` bundle (a separate WI), cut from the
merged origin head.
