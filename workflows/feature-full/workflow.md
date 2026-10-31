---
name: feature-full
rewind:
  max_total: 9
  max_per_step: 3
steps:
  - { id: grill, context_policy: fresh, handoff: true, recall: true }
  - { id: spec, context_policy: inherit, recall: true }
  - { id: grill-review, context_policy: fresh, handoff: true, requires_human_answer: true, gate: true, on_fail: { target: spec } }
  - { id: plan, context_policy: inherit, recall: true }
  - { id: e2e-author, context_policy: fresh, handoff: true, requires_command_evidence: true, vars: { e2e_author_mode: required }, recall: true }
  - { id: code, context_policy: inherit, recall: true }
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
           `artifacts/plan.md` (how it was supposed to be built), and the full diff (what
           was really built, tests included — see below). The machine gate (`verification`)
           runs after you: you have no verification report to read — judge the diff, the
           authored suite's intent and the spec's acceptance criteria themselves.
        2. Check the implementation against the spec's requirements and acceptance criteria one
           by one — do not rubber-stamp because the tests pass; the mechanical gate
           behind you checks different things than "is this the right change." An authored
           scenario `verification` later records as `skipped` blocks the automatic merge by
           itself (a human decides at delivery) — it is never this gate's verdict to make.
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
        4. **Plan conformance, both directions.** Read the plan-conformance facts section
           of this prompt — the driver computed the set differences between the plan's
           declared set and the diff; do not reconstruct them. In EVERY round, whatever the round contract says about new discoveries:
           a changed file the plan did not declare and no review finding cites is
           blocking; a declared coupled obligation the diff leaves unchanged is blocking;
           a declared plain write left unchanged is non-blocking. A file `code` added
           after a FAIL counts as declared only when its `declared_extensions` entry
           cites what requires it (a review finding `s<n>#<k>`, a verification FAIL
           `s<n>`, a human deliver answer) — judge whether it really does. Judge too whether the plan's coupled obligations are the
           right ones: the repo's `AGENTS.md` names the project's known couplings, and one
           it names that the plan omits is blocking.
        5. **e2e-author mode.** The mode is `skipped-with-rationale` ONLY when
           `artifacts/grill-qa.md` records the owner's explicit approval of it — at grill,
           or as the owner's `skip` answer to an all-green baseline (the named
           discipline-① exception, behaviour already on main); with no such record the
           mode is `required` (fail-closed), whatever `plan` states. On a skip the new-behaviour
           scenario checks in step 3 (new-behaviour coverage and
           deleted-as-already-satisfied) are not applicable — judge the recorded
           rationale instead. When the mode is `required`, they apply as written.
  - id: verification
    context_policy: fresh
    gate: true
    requires_command_evidence: true
    mechanical_fix: true
    on_fail: { target: code }
    model_candidates: [pi:example-gateway/model-a, pi:example-claude/claude-opus-5]
    vars:
      output_required: verdict, checks, authored_titles, scenarios
      build_step: code
      position: >-
        Specific to this lane, you also run the acceptance scenarios `e2e-author` authored
        and proved red before `code` ran. Design quality was already judged: `review` ran
        before you (this lane's order is code → review → verification → deliver) and its
        pass covers the tree you verify. This lane is scoped-capable (the scoped
        tier): a work item whose content carries the marker line `verification: scoped`
        runs this step scoped — declare the `scope` object (`mode`/`declared_paths`/
        `rationale`/`fast_gates_ref`) with exactly the `declared_paths` review recorded
        (`steps.review.output.scope`; the driver blocks the automatic merge on any
        disagreement) and keep the authored acceptance scenarios mandatory
        (unless the owner-approved e2e-author mode is `skipped-with-rationale`); without
        the marker it runs full and carries no `scope`.
      lane_checks: |-
        **Authored acceptance scenarios.** Copy `steps.e2e-author.output.scenario_titles`
        verbatim into `authored_titles` (`result.json`'s `authored_titles` is that list) and
        record one `scenarios` entry per title, same order — on this lane both fields are
        required. A title `e2e-author` bound to a command, unit and marker is resolved by
        `pf verify-run`: take its `bound_scenarios` entry's status and detail as they are —
        the binding's command ran and is in `test_bar`, and only a re-run of
        `pf verify-run` changes its outcome. A title without a binding
        (`unbound_titles`) is yours to judge. If the full bar already ran `e2e-author`'s
        test files (e.g. `go test ./test/` covers `test/...`), read that scenario's result
        from that run (`result.json`'s `units`, the command logs). If it did not (e.g. the
        project's e2e runner is not part of its unit test command), run those files with a
        targeted command and add that command to `test_bar`, so the acceptance tests are
        re-run on delivery when deliver's own merge moves HEAD past the verified head (and
        unconditionally after a light pass). A document-only scenario is driven through the
        project's actual interfaces and judged against its THEN clause. Never rewrite, relax or skip a
        scenario because the
        implementation does not satisfy it — record `fail` with a detailed `detail`. A skip
        is legal only with a non-empty `detail` and does not fail this gate; it blocks the
        automatic merge — `deliver` routes the delivery to a human naming the skipped
        title (the owner-approved `skipped-with-rationale` mode records an empty list and
        triggers nothing). Record the suite as its own named check (`authored-scenarios`): `fail` if any
        scenario failed. For a failing authored scenario, `fail_reason` names its title and
        expected vs observed.

        **e2e-author mode.** The mode is `skipped-with-rationale` ONLY when
        `artifacts/grill-qa.md` records both the approved mode and the owner's explicit
        approval of it (an all-green baseline's `skip` answer, recorded verbatim, is one); with no such record — or with the owner's veto — the mode is
        `required` (fail-closed), and the mandatory authored-suite text above applies
        unchanged. On a skip, `steps.e2e-author.output.scenario_titles` and `.baseline`
        are empty: echo them as empty `authored_titles`/`scenarios`, record the
        `authored-scenarios` check as `skipped` with the recorded rationale in its
        `detail`, and run no authored suite.
      never_pre_existing: the authored acceptance tests (`steps.e2e-author.output.test_files`) and any repro check
  - { id: deliver, context_policy: fresh, gate: true, requires_command_evidence: true, on_fail: { target: code } }
defaults:
  model_candidates: [pi:example-gateway/model-a, pi:example-claude/claude-opus-5, pi:example-openai/gpt-x]
---

# feature-full — reference flow

This is the flow-level narrative for the workflow. It explains how the
nine steps compose and what flows between them; it is not itself a structural
schema — the frontmatter `steps` array above is the *only* execution order. Nothing here overrides it.

## Shape of the flow

```
grill --> spec --> grill-review --> plan --> e2e-author --> code --> review --> verification --> deliver
```

The four gate FAIL edges, each declared in the frontmatter as that gate's `on_fail`
rewind target: `grill-review` --> `spec`, `review` --> `code`, `verification` -->
`code` and `deliver` --> `code`. One deliver result may override that target: a scoped
delivery whose human answer is 「全量重验」 carries `rewind_to: verification`, and the driver
rewinds there at the full tier.

The flow has two front-loaded interactive checkpoints (`grill`, `grill-review`) that
replace a heavy spec-review loop with a leaner two-pass confirmation, an acceptance
step that runs BEFORE the build (`e2e-author` authors scenarios for the spec's new
behaviour, commits them and proves each one red against the pre-implementation tree
in the same step), a build step that implements against that executable acceptance
(`code` inherits `e2e-author`'s handoff: test locations, the targeted run command, the
red bar to turn green), the judgment gate
(`review` reads the full diff, tests included, and judges design fit, scope and
quality), the machine gate (`verification` runs the full bar and reports each authored
scenario, rewinding to `code` on red) and `deliver`. Every delivery bundle runs
`review` before `verification`: a quality failure never costs a full machine bar, and
the machine gate verifies the tree the judgment already passed merged with the latest
origin default branch.

## e2e-author mode

The `e2e-author` step entry's `e2e_author_mode` var is the lane **default**, not a
per-work-item value: it renders `required` on every run. The per-work-item mode is
the owner-approved one recorded in `artifacts/grill-qa.md`; without an approved
record the default applies (fail-closed). `grill` proposes `skipped-with-rationale`
for an internal-mechanism / non-behaviour-face work item and the owner approves it;
the audit anchor is `artifacts/grill-qa.md` alone — `plan` only restates the mode, and
a plan proposal without an owner record resolves to `required`. A work item on the
behaviour face MUST stay `required`, with one named exception: when every authored
scenario passes at baseline (the behaviour is already on main), the driver asks the
owner — `rewind-to-spec: <words>` (an owner-routed rewind to `spec`), `skip:
<rationale>` (`e2e-author` records the rationale verbatim in `artifacts/grill-qa.md`
and completes skipped) or `cancel`. On a skip, `e2e-author` commits nothing, reporting empty
`scenario_titles`/`baseline`; `verification` and `review` honour the skip only on
the recorded owner's explicit approval.

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
| `code` | changes under `repos/<name>/**`, `artifacts/code-summary.md` | `review`, `verification` |
| `review` | `artifacts/review.md` (verdict, findings) | `verification` (its scope record, on the scoped tier), `deliver` (the merge rule's independent review and its follow-up summary) |
| `verification` | `artifacts/verification-report.md` (verdict, full bar + per-scenario results of the authored suite) | `deliver` (verified heads, test bar, authored-scenario outcomes) |
| `deliver` | `artifacts/delivery.md` | (terminal) |

`grill` and `grill-review` share one running document, `artifacts/grill-qa.md`, sectioned
by step id: `grill` appends its question/answer pairs first, `spec` reads the whole file
for context, and `grill-review` appends its own confirmation pass underneath.

## Rewind boundaries

Every gate in this flow declares its rewind target in the frontmatter `on_fail` — four
gates, two distinct targets (`spec`, `code`). The one override — a scoped `deliver`
answered 「全量重验」, rewinding to verification at full — is named under "Shape of the
flow" above and explained in `docs/concepts.md` §Rewind. This section only explains
*why* each target was picked:

- **`grill-review` FAILs -> rewind to `spec`.** The user's confirmation pass found the
  spec itself wrong (not just the confirmation script). Rewinding to `spec` re-runs
  spec authoring with the FAIL verdict attached as input; `grill` is *not* re-run —
  the raw requirements gathered there are still valid, only the spec's interpretation
  of them needs correction.
- **`verification` FAILs -> rewind to `code`.** The full bar or an authored scenario
  is red: an implementation failure, not review material — the bar was proven red and
  visible before `code` ran, and the judgment already passed on the earlier tree.
  Rewinding to `code` re-runs the build step with the
  verdict attached as `pending_rewind_input`; the authored scenarios are still valid,
  so `e2e-author` is not re-run.
- **The mechanical fast lane (`mechanical_fix: true`, polyforge#260).** This lane's
  verification step entry arms it: a FAIL whose every failed check is static (lint/
  format/vet/spelling — each reproducible by one deterministic command, no test failed)
  may carry a mechanical classification (`mechanical_bound`: per failed check its repo,
  its exact command and the files it named) and rewind cheaply — no edge slot charged
  (the rewind counts toward `max_total` only), `code`
  resuming its previous session to fix only inside the bound (modifications of
  bound-named files, ≤ 40 changed lines and ≤ 5 files summed across repos), then one
  light recheck that re-runs exactly the bound commands (no full bar, no authored
  scenarios) and confirms the semantic claims in its report. Any light-path failure —
  a malformed classification, a repair outside the bound, a recheck that does not
  pass — escalates in place, in the same step, to this same full verification
  (no new rewind slot); a light pass writes no `verified_heads`, and `deliver` covers
  the tests only by re-running the carried `test_bar` in the merge dispatch,
  unconditionally. The re-entered round re-runs a fresh `review` over the repaired
  diff before this verification re-runs — the mechanical lane never skips the
  judgment.
- **`review` FAILs -> rewind to `code`.** Review found the change wanting; `code`
  is re-run with the review verdict attached, and the re-run re-enters a fresh `review`,
  then `verification`, behind it. `plan` is never re-run and is not a rewind target:
  when a finding requires a file outside the plan's declared set, the re-run `code`
  changes it and cites the finding in its `declared_extensions` output (`path`,
  `source: review`, `ref` = the finding's `s<n>#<k>`; a verification FAIL or a human
  deliver answer is cited the same way, and a moot obligation is withdrawn in
  `withdrawn_obligations`), and the driver counts that cited file as
  declared in the next round's plan-conformance facts. A blocking finding against an
  authored test sends `code` through `needs_input`: it may not change those tests
  without a human answer (the driver's authored-tests gate).
  An all-text-only review FAIL (every blocking finding marked `text_only: true`) takes
  the light path: `code`'s previous session resumes with the verdict, applies only
  text-only fixes, and the gate tail (a fresh `review`, then `verification`) re-runs
  unchanged.
  A review FAIL whose blocking findings are ALL off-graph (each finding's
  `repair_target` names the authored suite, the spec, a declared obligation that is itself
  wrong or an un-IDed instruction — never `code`, which covers every repair the
  build step can make in its own round: updating a declared obligation the diff
  left unchanged, withdrawing a moot one, citing an undeclared file) is not rewound: the driver asks the owner (the
  review owner exit), who answers `amend-upstream: <words>` (recorded verbatim in
  `artifacts/grill-qa.md` `## Owner rulings`; a suite gap re-runs `e2e-author` append-only,
  anything else is executed by `code`; spec and plan are never re-run), `waive <refs> —
  <words>` (clears exactly the named findings; review re-runs) or `cancel`. That
  rewind is owner-routed. A mixed FAIL rewinds `code` as above, the off-graph
  findings riding along.
- **`deliver` "back to code" -> rewind to `code`.** A human chose to send the change
  back instead of merging it.

`code`'s per-step rewind allowance (`max_per_step`) is charged per gate→target edge:
the three gates that target it (`review`, `verification`, `deliver`) carry three
charged slots each, and the light (text-only), mechanical and converging rewinds
charge none — until a light re-entry self-reports `needs_code_change: true`: that turns
its light rewind into a fresh round, which charges the edge once.

Workflow-level rewind bounds are declared in the frontmatter: `max_total: 9`,
`max_per_step: 3` — the global cap counts every automatic rewind, light ones included. Owner-routed rewinds charge neither the edge slot nor `max_total`: a `deliver` FAIL a human answered ("back to code", and on a scoped work item 「全量重验」, which is owner-routed too), the review owner exit's rewind and the e2e-author all-green owner answers. A gate
that keeps failing past its edge's bound ends the workflow in
`failed_rewind_exhausted`, not an infinite loop.

## Budget guidance

No workflow-level budget and no step `budget.wall_minutes`: the bounds are the rewind limits
above and the daemon's per-step runtime bound T (docs/reference/commands.md, `pf help daemon`).

## Delivery and releases

`deliver` runs last, after every gate passed; its contract (merge the latest default branch,
push only the feature branch, open or reuse the PR, merge under the merge rule or ask a
human) is the shared step definition `workflows/_shared/steps/deliver.md`. Lane facts:
its "back to code" answer rewinds to `code`, and the independent review it reads is this
workflow's `review` gate (`steps.review.output` — its verdict, its recorded scope on
the scoped tier, and the `reviewed_heads` the driver wrote at its pass, which must
cover the delivered diff). There is no release slot: releases and
deploys happen only through the separate `release` bundle (a separate WI), cut from the
merged origin head.
