---
name: fix
description: Implement the micro-spec memo in the repo worktree in one tight pass, keeping the build green and committing compilable units.
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
      description: Every commit actually made, in the order committed. At least one commit is required (minItems 1) - a completed fix step that changed nothing is not a valid completion (report infeasibility honestly instead, never an empty list).
    build_status:
      type: string
      enum: [green, red]
      description: Build/test status of the tree at the end of this step's execution. Must be green unless this attempt is being deliberately handed off incomplete.
    summary:
      type: string
      description: Short prose summary of what was implemented, for verification and rewind context.
---

# fix

Implement `artifacts/fix-memo.md` against the repo worktree(s) declared for this WI.
Read the memo in full before starting; it is the only written instruction this lane
produces, so if it is genuinely insufficient to implement from, do not guess your way
past the gap - report it in `summary` and hand the attempt off honestly. If this is a
rewind re-entry, also read `execution.json`'s `pending_rewind_input` for this step (a
`verification` FAIL verdict) and address every point it raises first.

## Discipline

- **Work only inside the declared repo worktree(s)** (`repos/<name>/**`); never touch
  paths outside them.
- **Keep the build green between commits — fast checks only, per unit.** After each
  compilable unit, run, via `bash`, the repo's fast checks: {{fast_checks}} {{fast_checks_build}}
  This lane has no time budget that can absorb full-suite runs per unit — its bounds are
  the rewind limits and the daemon's per-step runtime bound.
- **Commit per compilable unit** with a message that names what it fixes. A quick fix is
  usually one or two commits - do not accumulate everything into one undifferentiated
  commit, and do not pad the change with drive-by edits the memo did not ask for: scope
  creep is the classic failure mode of this lane, and `verification` reads
  the diff against the memo.
- **Stay inside the memo's `## Non-goals`.** If you believe an exclusion is wrong, say
  so in `summary` - do not silently include it.

## If you cannot finish

If the fix turns out infeasible as written, or the build cannot be made green within
reasonable effort, do not fabricate a passing `build_status: green`. Leave it `red`,
explain the blocker in `summary`, and let the honest state surface: a dishonest green
here corrupts `verification` downstream.

## Principles

Pick the simplest mechanism; before adding timing, shutdown or retry logic, check whether the recovery path already covers it; no I/O under a lock.

{{principles}}

## Output

Emit the final JSON object per the schema above: every commit actually made, the final
build status, and a prose summary useful to the next reader (verification or a
future rewound instance of this very step).
