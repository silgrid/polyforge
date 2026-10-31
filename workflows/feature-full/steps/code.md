---
name: code
description: Implement the plan's steps in the repo worktree, keeping the build green and committing one compilable unit at a time.
output:
  type: object
  required: [commits, build_status]
  properties:
    commits:
      type: array
      minItems: 1
      items:
        type: object
        required: [step_id, sha, message]
        properties:
          step_id: { type: string }
          sha: { type: string }
          message: { type: string }
      description: One entry per plan step actually committed, in the order committed. At least one commit is required (minItems 1) - a completed code step that changed nothing is not a valid completion (report infeasibility honestly instead, never an empty list).
    build_status:
      type: string
      enum: [green, red]
      description: Build/test status of the tree at the end of this step's execution. Must be green unless this attempt is being deliberately handed off incomplete (see incomplete handling below).
    needs_code_change:
      type: boolean
      description: Optional (polyforge#222, polyforge#260). Absent on every normal completion. Set only when this attempt is a light rewind's re-entry (the rewind input's light-rewind note names the kind - text-only or mechanical) and the verdict's fix needs a change OUTSIDE that kind's boundary - for the text-only kind, code, tests or assertions, user-facing string literals, workflows/** .md files, skills/** prompt material; for the mechanical kind, outside the verdict's mechanical_bound (a file it does not name, an added/deleted/renamed file, or its caps) - deliver true instead of applying it, and the driver rewinds fresh with the same verdict - that escalation charges the edge's rewind slot once (the light rewind itself was uncharged), never twice, and when the edge is already spent the workflow ends failed_rewind_exhausted. Never set it on a normal completion, and never to dodge the plan.
    declared_extensions:
      type: array
      description: "Optional (after a gate FAIL or human deliver answer rewound you): each file changed outside the plan's declared set, cumulative since the plan, with its citation - `review` + `s<n>#<k>` (n = the review's spawn ordinal, k = the finding's 1-based position), `verification` + `s<n>` (its FAIL's ordinal), or `deliver` + the question id (the answer declares the file outright). Cited = declared; uncited = undeclared (blocking). The plan is never edited or re-run."
      items:
        type: object
        required: [path, source, ref]
        properties:
          path: { type: string, description: "repo-relative path of the added file" }
          source: { type: string, enum: [review, verification, deliver] }
          ref: { type: string }
    withdrawn_obligations:
      type: array
      description: "Optional mirror: a declared coupled obligation a citation made moot, same {path, source, ref}. One whose declaring writes are all absent from the diff lapses by itself; an uncited withdrawal stays blocking."
      items:
        type: object
        required: [path, source, ref]
        properties:
          path: { type: string }
          source: { type: string, enum: [review, verification, deliver] }
          ref: { type: string }
    summary:
      type: string
      description: Short prose summary of what was implemented, for review and rewind context.
---

# code

Implement `artifacts/plan.md` against the repo worktree(s) declared for this WI. Read the
plan in full before starting; if this is a rewind re-entry, also read
`execution.json`'s `pending_rewind_input` for this step (a `verification` or `review` FAIL
verdict) and address every point it raises before proceeding with the rest of the plan.

## Discipline

- **Work only inside the declared repo worktree(s)** (`repos/<name>/**`); never touch
  paths outside them.
- **Follow the plan's step order.** Each plan step is designed to leave the tree
  buildable; do not skip ahead or reorder for convenience — if the order genuinely does
  not work, that is itself a finding to report in `summary`, not a license to silently
  reorder.
- **Keep the build green between commits — fast checks only, per unit.** After
  implementing each plan step, run, via `bash`, the repo's fast checks: {{fast_checks}}
  {{fast_checks_build}}
  The gate's full bar on this lane also runs the authored e2e suite. The files named in
  `execution.json`'s
  `steps.e2e-author.output.test_files` are not part of this step's per-unit bar — they
  are committed by `e2e-author` itself (proven red there, see its `baseline`) and
  exercised by `verification`; running them while you work is fine.
- **Never edit `e2e-author`'s test files.** The paths in
  `steps.e2e-author.output.test_files` are read-only to `code`: do not modify, move,
  rename, delete, or stage them. If one looks wrong, make no change to it — ask via
  `needs_input`; satisfying the tests is the job, rewriting them is not. The same
  holds when a `review` FAIL names an authored test as wrong or vacuous: ask via
  `needs_input`, quoting the finding, and change the test only after a human answered. The driver
  enforces this: a completion whose tree changed those files since `e2e-author`'s commit
  (`git diff <author-commit>..HEAD -- <test_files>`) is rejected, unless a human
  answered your question in this attempt.
- **Commit per compilable unit**, one commit per plan step (or a tightly related group of
  plan steps if the project's convention groups small steps), with a message that
  identifies which plan step it satisfies. Do not accumulate the entire plan into one
  undifferentiated commit — `review` and any rewind re-entry depend on being able to see
  what changed at each unit's boundary.
- **Do not touch files the plan did not declare** for this step without a good reason
  recorded in `summary`, and change every coupled obligation the plan declares for your
  writes: the driver compares your diff with the plan's declared set in both directions
  and hands the differences to `review`, where an undeclared change and an unchanged
  declared coupled obligation are both blocking. After a review or verification FAIL, or a
  human deliver answer, that requires a file outside the declared set, change it and cite
  it in `declared_extensions` (withdraw a moot obligation in `withdrawn_obligations`);
  never edit or re-run the plan.

## If you cannot finish

If a plan step turns out to be infeasible as written, or the build cannot be made green
within reasonable effort, do not fabricate a passing `build_status: green`. Report
honestly: leave `build_status: red` and explain the blocker in `summary`. A dishonest
green here corrupts every downstream gate (`verification`, `review`) that
trusts this step's word for it.

{{owner_review}}

## Principles

Pick the simplest mechanism; before adding timing, shutdown or retry logic, check whether the recovery path already covers it; no I/O under a lock.

{{principles}}

## Output

Emit the final JSON object per the schema above: every commit actually made (step id, sha,
message - at least one; the schema rejects an empty list), the final build status, and a
prose summary useful to the next reader (reviewer, or a future rewound instance of this
very step).
