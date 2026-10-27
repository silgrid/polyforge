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
      description: Optional (polyforge#222). Absent on every normal completion. Set only when this attempt is a light rewind's re-entry (the rewind input's light-rewind section instructs it) and the verdict's fix needs a change OUTSIDE the text-only boundary - code, tests or assertions, user-facing string literals, workflows/** .md files, skills/** prompt material - deliver true instead of applying it, and the driver rewinds fresh with the same verdict, charging no additional rewind slot. Never set it on a normal completion, and never to dodge the chore.
    summary:
      type: string
      description: Short prose summary of what was done, for verification and rewind context.
---

# code

Execute `artifacts/plan.md` against the repo worktree(s) declared for this WI. Read the
plan in full before starting; if this is a rewind re-entry, also read
`execution.json`'s `pending_rewind_input` for this step (a `verification` FAIL verdict)
and address every point it raises before proceeding with the rest of the plan.

## Discipline

- **Work only inside the declared repo worktree(s)** (`repos/<name>/**`); never touch
  paths outside them.
- **Follow the plan's step order.** Each plan step is designed to leave the tree
  buildable; if the order genuinely does not work, that is a finding to report in
  `summary`, not a license to silently reorder.
- **Keep the build green between commits — fast checks only, per unit.** After each plan
  step, run, via `bash`, the repo's fast checks: the project's own build, its
  lint/format check, and the unit tests of the packages/modules this unit touched (the
  changed files' packages/modules and their tests, no more) — as the repo itself defines
  them (Makefile, package.json scripts, CI config). For a Go repo that is `go build
  ./... && go vet ./... && gofmt -l .` plus `go test` of the touched packages. A
  formatter listing any file (non-empty `gofmt -l` output, or the repo's equivalent)
  counts as red, exactly like a non-zero exit. That is the whole per-unit bar.
  Full-suite testing belongs to `verification` (the one gate that runs the project's
  full bar); do not run the full suite (e.g. `go test ./...`) here, and do not proceed
  on top of a red fast check.
- **Commit per compilable unit**, with a message identifying which plan step it
  satisfies. For a single-step chore this is one commit; for a sweep it is many.
- **Stay mechanical.** This lane's contract is no judgment calls: no drive-by
  "improvements" the plan did not declare, no reformatting beyond what the chore
  requires, no half-included adjacent work. An undeclared write is a lock-scope
  violation waiting to happen — record any genuinely necessary deviation in `summary`.

## If you cannot finish

If a plan step is infeasible as written, or the build cannot be made green within
reasonable effort, do not fabricate a passing `build_status: green`. Report honestly:
leave `build_status: red` and explain the blocker in `summary`. On this lane a red hand
off is doubly important — `review` and `verification` are what stand between your
tree and merge.

## Light rewind re-entry (text-only fixes only)

If your rewind input carries a light-rewind section, this attempt is a LIGHT re-entry:
apply only text-only fixes — comments and documentation in any file, test files included
(the boundary is defined once in the review step's own material). A fix that needs a
change outside that boundary (code, tests, assertions, user-facing string literals,
`workflows/**` `.md`, `skills/**` material) is NOT yours to apply: deliver the completed
result carrying `needs_code_change: true` instead — the driver rewinds fresh with the same
verdict. Fast checks and per-unit commits are unchanged either way.

## Principles

Pick the simplest mechanism; before adding timing, shutdown or retry logic, check whether the recovery path already covers it; no I/O under a lock.

{{principles}}

## Output

Emit the final JSON object per the schema above: every commit actually made, the final
build status, and a prose summary useful to the next reader.
