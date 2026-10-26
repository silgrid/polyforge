---
name: fix
description: Remove the confirmed root cause - implement the change that eliminates the diagnosed condition, keep the build green, and encode the corrected behavior where the project expects tests.
output:
  type: object
  required: [commits, build_status, root_cause_addressed, regression_test]
  properties:
    regression_test:
      type: string
      description: "Repo-relative path(s) of the regression test that pins the repro from the `repro` step (comma-separated if several). REQUIRED: the fix's last line of defense is a test that fails on the pre-fix tree and passes after — a bug fixed without its repro pinned is not fixed, it is deferred. If the project genuinely has no test surface, an explicit waiver string `waived: <reason>` is accepted, but the waiver is visible to `review` and must justify why no test can exist. `verification` runs it; `review` challenges weak waivers."
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
    root_cause_addressed:
      type: boolean
      description: True only if the change removes the diagnosed condition itself. False means this attempt mitigates the symptom or addresses a different cause - say which in summary.
    needs_code_change:
      type: boolean
      description: Optional (polyforge#222). Absent on every normal completion. Set only when this attempt is a light rewind's re-entry (the rewind input's light-rewind section instructs it) and the verdict's fix needs a change OUTSIDE the text-only boundary - code, tests or assertions, user-facing string literals, workflows/** .md files, skills/** prompt material - deliver true instead of applying it, and the driver rewinds fresh with the same verdict, charging no additional rewind slot. Never set it on a normal completion, and never to dodge the fix.
    summary:
      type: string
      description: "Short prose summary - what changed, why it removes the root cause, and any scope notes for review and rewind context."
---

# fix

Implement the change that removes the root cause `root-cause` confirmed. Your inputs are
`artifacts/root-cause.md` (the proven cause and its scope guidance) and
`artifacts/repro.md` (the observable failure). Read both in full before starting; if this
is a rewind re-entry, also read `execution.json`'s `pending_rewind_input` for this step
(a `verification` or `review` FAIL verdict) and address every point it raises first.

## Discipline

- **Fix the cause, not the symptom.** `root_cause_addressed` is the step's honesty field:
  the change must remove the diagnosed condition itself. If mid-implementation you
  discover the diagnosis was wrong, do NOT silently fix what you now believe is the real
  cause — report it: set `root_cause_addressed: false`, explain in `summary`, and let
  the review verdict route the WI honestly (there is no direct rewind to `root-cause`
  in this lane; see `workflow.md`'s rewind-boundaries section).
- **Work only inside the declared repo worktree(s)** (`repos/<name>/**`).
- **Keep the build green between commits — fast checks only, per unit.** After each
  compilable unit, run, via `bash`, the repo's fast checks: the project's own build, its
  lint/format check, and the unit tests of the packages/modules this unit touched (the
  changed files' packages/modules and their tests, no more) — as the repo itself defines
  them (Makefile, package.json scripts, CI config). For a Go repo that is `go build
  ./... && go vet ./... && gofmt -l .` plus `go test` of the touched packages. A
  formatter listing any file (non-empty `gofmt -l` output, or the repo's equivalent)
  counts as red, exactly like a non-zero exit. That is the whole per-unit bar.
  Full-suite testing belongs to `verification` (the one gate that runs the project's
  full bar); do not run the full suite (e.g. `go test ./...`) here, and do not proceed
  on top of a red fast check.
- **Commit per compilable unit** with a message that references what it changes and why.
- **The repro test must now pass** — if `repro` encoded the failure as a failing test,
  that test passing on your tree is your own fastest feedback; run it before declaring
  done. `fix` never edits `repro`'s test files (the paths in
  `steps.repro.output.test_files`) — no exceptions. If a repro test is wrong, leave it
  untouched and ask via `needs_input`. The driver enforces this: a completion whose
  tree changed those files since `repro`'s commit is rejected, unless a human answered
  your question in this attempt.
- **No drive-by fixes.** Adjacent ugliness the diagnosis did not implicate stays
  untouched; note it in `summary` if it deserves its own WI.

## If you cannot finish

If the fix is infeasible as diagnosed, or the build cannot be made green within
reasonable effort, do not fabricate `build_status: green`. Leave it `red`, explain in
`summary`. A dishonest green corrupts `verification` and `review` downstream.

## Light rewind re-entry (text-only fixes only)

If your rewind input carries a light-rewind section, this attempt is a LIGHT re-entry:
apply only text-only fixes — comments and documentation in any file, test files included
(the boundary is defined once in the review step's own material). A fix that needs a
change outside that boundary (code, tests, assertions, user-facing string literals,
`workflows/**` `.md`, `skills/**` material) is NOT yours to apply: deliver the completed
result carrying `needs_code_change: true` instead — the driver rewinds fresh with the same
verdict. Fast checks, the repro bar and per-unit commits are unchanged either way.

## Principles

Pick the simplest mechanism; before adding timing, shutdown or retry logic, check whether the recovery path already covers it; no I/O under a lock.

{{principles}}

## Output

Emit the final JSON object per the schema above: every commit made (at least one - the
schema rejects an empty list), the final build
status, whether the root cause itself is addressed, and a prose summary for the next
reader.
