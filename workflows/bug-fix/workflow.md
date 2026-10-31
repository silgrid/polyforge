---
name: bug-fix
description: Root-cause lane - reproduce the failure first, prove the cause before fixing, then fix and run the gate tail - repro → root-cause → fix → review → verification → deliver.
rewind:
  max_total: 9
  max_per_step: 3
defaults:
  model_candidates: [pi:example-gateway/model-a, pi:example-claude/claude-opus-5, pi:example-openai/gpt-x]
steps:
  - { id: repro, context_policy: fresh, recall: true }
  # root-cause is a gate: it must run FRESH (the judge never inherits the
  # judged conversation — driver rule, separation of powers) and takes the
  # repro record (artifacts/repro.md) by path convention: it reads the
  # artifact, not the chat.
  - { id: root-cause, context_policy: fresh, handoff: true, gate: true, model_candidates: [pi:example-claude/claude-opus-5, pi:example-openai/gpt-x, pi:example-gateway/model-a], on_fail: { target: repro }, recall: true }
  - { id: fix, context_policy: inherit, recall: true }
  - id: review
    context_policy: fresh
    gate: true
    on_fail: { target: fix }
    vars:
      build_step: fix
      scope_source: the WI and the root-cause scope guidance
      intro: |-
        Judge whether this bug fix is ready to release. Unlike `verification`, this step is a
        genuine quality judgment, not a mechanical check — and in this lane it has a specific
        question to answer above all: **did the fix remove the actual cause?** The lane's whole
        reason to exist is preventing symptom-patches. You run after `fix` and before
        `verification`: judge the diff and the evidence chain (repro -> cause -> fix) yourself —
        no machine results exist yet, and `verification` re-runs the repro behind you.
      judge: |-
        1. Read, in this order: `artifacts/root-cause.md` (the proven cause),
           `artifacts/repro.md` (the observed failure), and the full diff (in each task repo
           `git diff $(git merge-base HEAD origin/HEAD)..HEAD`, falling back to `origin/main` or
           `origin/master` when `origin/HEAD` is unset — this carries `repro`'s test commit,
           `fix`'s commits and any commit `fix` did not report). The machine gate
           (`verification`) runs after you: you have no verification report to read — judge
           the diff itself, the evidence chain and the corrected behaviour in it.
        2. Judge the causal fit: does the diff remove the *diagnosed condition*, or does it
           merely suppress the observed symptom at one call site? Would the failure still occur
           through a path the diff did not touch? The lane requires the cause itself to be
           gone. (A fix that reports `root_cause_addressed: false` never reaches you: the
           driver routes it back to `root-cause` first.)
        3. Judge the blast radius: the diff's effect on adjacent behavior the diagnosis did not
           implicate; scope creep beyond what the cause demanded; missing test coverage of the
           corrected behavior if the project expects tests alongside fixes.
           Judge the tests in the diff too: a repro or regression test that does not actually
           reproduce the diagnosed failure (vacuous, or passing whatever the code does) is a
           blocking finding — `fix` may not change it on its own (the driver's authored-tests
           gate) and must ask a human through `needs_input`, so name the test and why. A
           redundant or oversized test is a `non-blocking` finding.
        4. If your blocking finding is that the *diagnosis* was wrong, say exactly that in
           `fail_reason`. The FAIL rewinds to `fix`, which reports `root_cause_addressed: false`
           honestly instead of re-litigating the cause; the driver then rewinds to
           `root-cause` once with that report as its input, and a second false asks the
           owner (see `workflow.md`'s rewind-boundaries section).
  - id: verification
    context_policy: fresh
    gate: true
    requires_command_evidence: true
    on_fail: { target: fix }
    model_candidates: [pi:example-gateway/model-a, pi:example-claude/claude-opus-5]
    vars:
      output_required: verdict, checks
      build_step: fix
      position: >-
        Specific to this lane, you also re-run the original reproduction: the lane's core
        acceptance signal is *the artifact that originally failed now passes*. Design
        quality was already judged: `review` ran before you (this lane's order is
        fix → review → verification → deliver) and its pass covers the tree you verify.
        This lane is scoped-capable (the scoped tier): a
        work item whose content carries the marker line `verification: scoped` runs this
        step scoped — declare the `scope` object with exactly the `declared_paths` review
        recorded (`steps.review.output.scope`) and keep the repro mandatory; without the
        marker it runs full and carries no `scope`.
      lane_checks: |-
        **Re-run the repro.** Read `artifacts/repro.md` and execute its `## Steps` verbatim
        against the fixed tree; if `repro` authored a failing test, run that test. Record the
        outcome as its own named check, `repro`. The observed
        behavior must match `## Expected` — a repro that now fails *differently* is a fail,
        not a pass. When `repro` reproduced the bug (`reproduces: true`), a `pass` verdict
        must record the repro re-run as a check named `repro` with status `pass` — the
        driver rejects a pass whose repro check is skipped or missing.
      never_pre_existing: the repro re-run (its check and the original repro case), every test in `steps.repro.output.test_files`, and the `steps.fix.output.regression_test` test(s)
  - { id: deliver, context_policy: fresh, gate: true, requires_command_evidence: true, on_fail: { target: fix } }
---

# bug-fix — root-cause lane

This is the flow-level narrative for the workflow. It explains how the six
steps compose and what flows between them; it is not itself a structural schema — the
frontmatter `steps` array above is the *only* execution order. Nothing here overrides it.

## Shape of the flow

```
repro --> root-cause --> fix --> review --> verification --> deliver
   ^         |   ^         |        |            |                |
   |   (FAIL: rewind to repro)  (FAIL: rewind to fix)  (FAIL: rewind to fix)  (FAIL: rewind to fix)
             +-- fix false, once --+
```

The lane exists for bugs whose cause is NOT already known. Its contract with the rest of
the system is the evidence chain: a failure is captured as a reproducing artifact BEFORE
anyone theorizes about it, the theory is proven against that artifact BEFORE anyone
writes a fix, and the fix is validated by the same artifact that originally failed. A bug
whose root cause is already obvious from the WI text belongs on `autopilot` instead; a
bug that turns out to need design decisions belongs on `feature-full`.

## Why `root-cause` is a gate (`gate: true`)

Any step may carry `gate: true`: the flag means "this step produces a business verdict"
(review, verification and grill-review are examples, not an exclusive set). Root-cause analysis
produces exactly such a verdict: `pass` (the cause is identified and supported by evidence that
explains the observed repro) or `fail` (it is not, and proceeding to `fix` would mean guessing). The step's output schema uses
`enum: [pass, fail]` so no verdict-vocabulary mapping is ever needed — a reading agent
sees the same two words the gate machinery acts on. Making it a gate is also
structurally load-bearing rather than cosmetic:

- A `fail` verdict (inconclusive diagnosis) FAILs the gate and rewinds to `repro` — the
  correct bounded
  response is better evidence, not a blind fix attempt.
- This lane has no spec step (bugs spec their corrected behavior inside the fix memo/
  commits, not via a dedicated authoring step), so its diagnosis step is declared in its
  gate form, like feature-full's `grill-review`.

## Artifact flow between steps

Every step's output is a path recorded in `execution.json`'s `steps.<id>.output`, pointing at a
file under `artifacts/`. Downstream steps read the artifacts they need by following that path
convention — the driver injects nothing and there is no frontmatter declaration for it
(`root-cause`, for instance, simply reads `artifacts/repro.md`).

| step | primary artifact it writes | who reads it |
|---|---|---|
| `repro` | `artifacts/repro.md` (commands, environment, observed vs expected), plus a failing test under `repos/<name>/**` when one is authored | `root-cause`, `verification` |
| `root-cause` | `artifacts/root-cause.md` (verdict, cause, evidence) | `fix`, `review` |
| `fix` | changes under `repos/<name>/**`, `artifacts/fix-summary.md`, and the repro pinned as a regression test (required output `regression_test`; waived only with a review-visible reason) | `review`, `verification`, `deliver` |
| `review` | `artifacts/review.md` (verdict, findings) | `verification` (its scope record, on the scoped tier), `deliver` (the merge rule's independent review and its follow-up summary) |
| `verification` | `artifacts/verification-report.md` (verdict) | `deliver` (verified heads, test bar) |
| `deliver` | the pushed feature branch, PR and merge outcome per repo, `artifacts/delivery.md` | (terminal) |

The repro artifact is the spine of the lane: `root-cause` cites it as evidence, and
`verification` re-runs it — the originally failing case passing on the fixed tree is the
lane's core acceptance signal.

## Rewind boundaries

All rewind declarations live in the frontmatter `on_fail` of their gates; a target must
exist and sit strictly earlier than the failing step. One exception: a scoped `deliver`
answered 「全量重验」 overrides its `on_fail` with `rewind_to: verification` (full tier).
This section explains the choices:

- **`root-cause` FAILs (fail verdict = inconclusive diagnosis) -> rewind to `repro`.**
  The analysis could not
  determine the cause from the evidence at hand. `repro` re-runs with the `fail_reason`
  (what was missing: intermittent behavior, missing logs, wrong environment) attached as
  its `pending_rewind_input`, and sharpens the reproduction accordingly. Only the first
  inconclusive diagnosis rewinds: a second one on this edge does not spend a charged
  rewind — the driver asks the owner (`needs_input`) instead: `re-run-repro: <evidence>` (supplement the
  reproduction and re-run `repro`, an owner-routed rewind), `continue: <guidance>` or
  `cancel`. Likewise a `repro` that honestly reports `reproduces: false`
  is not handed to `root-cause`: the driver asks the owner the same three answers
  (`continue` accepts the report and moves on with the guidance: `root-cause`'s next
  dispatch carries it verbatim as its `pending_rewind_input`).
- **Pre-fix base.** The driver records the task-repo heads the first accepted `repro`
  ran on (`execution.json` `pre_fix_heads`). After a `fix → root-cause` rewind, a
  `repro` re-run reproduces against that pre-fix base (its rewind input names the
  heads), never against the tree that already carries the fix's mitigation.
- **`review` FAILs -> rewind to `fix`.** Review found the fix wanting (wrong cause
  addressed, incomplete fix, or a fix that breaks something adjacent). `fix` re-runs
  with the blocking findings attached. If the verdict says the diagnosis itself was
  wrong, `fix` reports `root_cause_addressed: false` honestly rather than re-litigating
  the cause mid-flow.
- **`fix` completes with `root_cause_addressed: false` -> the driver rewinds to
  `root-cause`, once.** Before `review` or `verification` runs, the driver archives and
  re-runs `root-cause` with the fix's `summary` as its rewind input (a false report that
  changed nothing completes with an empty `commits` list — the one legal empty list; a
  true fix with no commit is rejected), charged on the
  `fix → root-cause` edge; `fix` then re-runs against the new diagnosis. A re-run
  `root-cause` that cannot prove a cause still FAILs to `repro` as above. A second false
  does not rewind again: the driver asks the owner (`needs_input`) whether to continue
  with corrective guidance or answer the reserved `cancel` (which ends the work item)
  and re-file — so an honestly reported
  misdiagnosis never ends in `failed_rewind_exhausted`.
  An all-text-only review FAIL (every blocking finding marked `text_only: true`) takes
  the light path: `fix`'s previous session resumes with the verdict, applies only
  text-only fixes, and the gate tail (a fresh `review`, then `verification`) re-runs
  unchanged.
  A review FAIL whose blocking findings are ALL off-graph (each finding's
  `repair_target` names the authored suite, the spec, a declared obligation that is itself
  wrong or an un-IDed instruction — never `code`, which covers every repair the
  build step can make in its own round: updating a declared obligation the diff
  left unchanged, withdrawing a moot one, citing an undeclared file) is not rewound: the driver asks the owner (the
  review owner exit), who answers `amend-upstream: <words>` (recorded verbatim in
  `artifacts/grill-qa.md` `## Owner rulings`; a suite gap re-runs `repro` append-only,
  anything else is executed by `fix`; spec and plan are never re-run), `waive <refs> —
  <words>` (clears exactly the named findings; review re-runs) or `cancel`. That
  rewind is owner-routed. A mixed FAIL rewinds `fix` as above, the off-graph
  findings riding along.
- **`verification` FAILs -> rewind to `fix`.** Build/lint/tests — including the original
  repro case — did not pass; only `fix` can change the tree. The judgment already
  passed on the earlier tree; the re-entered round re-runs it after the fix.
- **`deliver` FAILs ("back to code") -> rewind to `fix`.** The human sent the delivery
  back; `fix` re-runs and the re-run delivery reuses the same branch and PR.

Workflow-level rewind bounds: `max_total: 9`, `max_per_step: 3`. The per-step bound
is charged per gate→target edge (the three gates that rewind to `fix` carry three
charged slots each; light and converging rewinds charge none, until a light re-entry
self-reports `needs_code_change: true` — that turns its light rewind into a fresh round,
which charges the edge once), and `max_total` counts every automatic rewind, light ones and `fix → root-cause` included. Owner-routed rewinds charge neither the edge slot nor `max_total`: a `deliver` FAIL a human answered ("back to code", and on a scoped work item 「全量重验」, which is owner-routed too), the review owner exit's rewind and the bug-fix dead-end owner answers. A diagnosis-fix loop that cannot converge in three
charged rewinds per gate is telling you the WI is mis-scoped — surface that honestly
instead of burning the budget on repeat attempts.

## Budget guidance

No workflow-level budget and no step `budget.wall_minutes`: the bounds are the rewind limits
above and the daemon's per-step runtime bound T (docs/reference/commands.md, `pf help daemon`).

## Delivery and releases

`deliver` runs last, after every gate passed; its contract (merge the latest default branch,
push only the feature branch, open or reuse the PR, merge under the merge rule or ask a
human) is the shared step definition `workflows/_shared/steps/deliver.md`. Lane facts:
its "back to code" answer rewinds to `fix`, and the independent review it reads is this
workflow's `review` gate (`steps.review.output` — its verdict, its recorded scope on the
scoped tier, and the `reviewed_heads` the driver wrote at its pass, which must cover the
delivered diff). There is no release slot: releases and
deploys happen only through the separate `release` bundle (a separate WI), cut from the
merged origin head.
