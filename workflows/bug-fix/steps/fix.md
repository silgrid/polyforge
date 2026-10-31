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
      items:
        type: object
        required: [sha, message]
        properties:
          sha: { type: string }
          message: { type: string }
      description: Every commit THIS attempt made, in the order committed. At least one is required, with one exception the driver checks - root_cause_addressed false with nothing changed may report an empty list. A true fix with no commit is rejected; never re-list an earlier attempt's commits to fill the list (they are not this attempt's change).
    build_status:
      type: string
      enum: [green, red]
      description: Build/test status of the tree at the end of this step's execution. Must be green unless this attempt is being deliberately handed off incomplete.
    root_cause_addressed:
      type: boolean
      description: True only if the change removes the diagnosed condition itself. False means the diagnosis did not hold (this attempt mitigates the symptom or the cause is a different one) - say which, and what you found, in summary. A completed false is not reviewed - the driver rewinds to root-cause once with your summary as its input, and a second false asks the owner.
    needs_code_change:
      type: boolean
      description: Optional (polyforge#222). Absent on every normal completion. Set only when this attempt is a light rewind's re-entry (the rewind input's light-rewind section instructs it) and the verdict's fix needs a change OUTSIDE the text-only boundary - code, tests or assertions, user-facing string literals, workflows/** .md files, skills/** prompt material - deliver true instead of applying it, and the driver rewinds fresh with the same verdict - that escalation charges the edge's rewind slot once (the light rewind itself was uncharged), never twice, and when the edge is already spent the workflow ends failed_rewind_exhausted. Never set it on a normal completion, and never to dodge the fix.
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
If `root-cause` re-ran because an earlier attempt of this step reported the diagnosis
wrong, `artifacts/root-cause.md` now holds the new diagnosis: fix against it.

## Discipline

- **Fix the cause, not the symptom.** `root_cause_addressed` is the step's honesty field:
  the change must remove the diagnosed condition itself. If mid-implementation you
  discover the diagnosis was wrong — or a review verdict says so — do NOT silently fix
  what you now believe is the real cause, and do not re-litigate the cause mid-flow:
  complete with `root_cause_addressed: false` and put what you found in `summary` — with
  an empty `commits` list when you changed nothing (the one legal empty list; never
  re-list an earlier attempt's commits). The
  driver rewinds to `root-cause` once, with your summary as its rewind input, and this
  step re-runs against the new diagnosis; a second false asks the owner (`needs_input`)
  whether to continue with corrective guidance or answer the reserved `cancel` (ending
  this work item) and re-file. A `repro` re-run
  after that rewind reproduces against the pre-fix base the driver recorded
  (`pre_fix_heads`), so a mitigation you committed never hides the original failure.
- **Work only inside the declared repo worktree(s)** (`repos/<name>/**`).
- **Keep the build green between commits — fast checks only, per unit.** After each
  compilable unit, run, via `bash`, the repo's fast checks: {{fast_checks}}
  {{fast_checks_build}}
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

{{owner_review}}

## Principles

Pick the simplest mechanism; before adding timing, shutdown or retry logic, check whether the recovery path already covers it; no I/O under a lock.

{{principles}}

## Output

Emit the final JSON object per the schema above: every commit made (at least one - the
schema rejects an empty list), the final build
status, whether the root cause itself is addressed, and a prose summary for the next
reader.
