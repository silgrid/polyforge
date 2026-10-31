---
name: code
description: Execute the maintenance plan in the repo worktree, keeping the build green and committing one compilable unit at a time.
output:
  type: object
  required: [commits, build_status]
  properties:
    commits:
      type: array
      minItems: 1
      items:
        type: object
        required: [sha, message]
        properties:
          sha: { type: string }
          message: { type: string }
      description: One entry per plan step actually committed, in the order committed. At least one commit is required (minItems 1) - a completed step that changed nothing is not a valid completion (report infeasibility honestly instead, never an empty list).
    build_status:
      type: string
      enum: [green, red]
      description: Build/test status of the tree at the end of this step's execution. Must be green unless this attempt is being deliberately handed off incomplete.
    needs_code_change:
      type: boolean
      description: Optional (polyforge#222). Absent on every normal completion. Set only when this attempt is a light rewind's re-entry (the rewind input's light-rewind section instructs it) and the verdict's fix needs a change OUTSIDE the text-only boundary - code, tests or assertions, user-facing string literals, workflows/** .md files, skills/** prompt material - deliver true instead of applying it, and the driver rewinds fresh with the same verdict - that escalation charges the edge's rewind slot once (the light rewind itself was uncharged), never twice, and when the edge is already spent the workflow ends failed_rewind_exhausted. Never set it on a normal completion, and never to dodge the chore.
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
      description: Short prose summary of what was done, for verification and rewind context.
---

# code

Execute `artifacts/plan.md` against the repo worktree(s) declared for this WI. Read the
plan in full before starting; if this is a rewind re-entry, also read
`execution.json`'s `pending_rewind_input` for this step (a `verification` or `review` FAIL
verdict) and address every point it raises before proceeding with the rest of the plan.

## Discipline

- **Work only inside the declared repo worktree(s)** (`repos/<name>/**`); never touch
  paths outside them.
- **Follow the plan's step order.** Each plan step is designed to leave the tree
  buildable; if the order genuinely does not work, that is a finding to report in
  `summary`, not a license to silently reorder.
- **Keep the build green between commits — fast checks only, per unit.** After each plan
  step, run, via `bash`, the repo's fast checks: {{fast_checks}}
  {{fast_checks_build}}
- **Commit per compilable unit**, with a message identifying which plan step it
  satisfies. For a single-step chore this is one commit; for a sweep it is many.
- **Stay mechanical.** This lane's contract is no judgment calls: no drive-by
  "improvements" the plan did not declare, no reformatting beyond what the chore
  requires, no half-included adjacent work. The driver compares your diff with the
  plan's declared set in both directions and hands the differences to `review` and
  `verification`: an undeclared change is a blocking finding, and so is a declared
  coupled obligation you left unchanged — record any genuinely necessary deviation in
  `summary`. After a review or verification FAIL, or a human deliver answer, that
  requires a file outside the declared set, change it and cite it in
  `declared_extensions` (withdraw a moot obligation in `withdrawn_obligations`); never
  edit or re-run the plan.

## If you cannot finish

If a plan step is infeasible as written, or the build cannot be made green within
reasonable effort, do not fabricate a passing `build_status: green`. Report honestly:
leave `build_status: red` and explain the blocker in `summary`. On this lane a red hand
off is doubly important — `review` and `verification` are what stand between your
tree and merge.

{{owner_review}}

## Principles

Pick the simplest mechanism; before adding timing, shutdown or retry logic, check whether the recovery path already covers it; no I/O under a lock.

{{principles}}

## Output

Emit the final JSON object per the schema above: every commit actually made, the final
build status, and a prose summary useful to the next reader.
