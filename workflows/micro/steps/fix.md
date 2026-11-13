---
name: fix
description: Implement the ticket's text change inside the paths the repository declares text-only, run the repository's micro evidence, write the evidence plan and judge its bounds with the pre-written facts.py micro-bounds (the micro-bounds skill) - out of bounds, ask the owner to re-file on develop and hand off `cancel` on the owner's word.
---

# fix

Implement the ticket. This lane has no spec and no plan: `runtime-state/work-item.json`
— its goal and content — is the whole specification. Implement what it names, nothing
more. On a later round (a 回退 in `artifacts/handoff.md` named this step) read the section
of the gate that FAILed and the files it names first, and answer every point it raises.

{{recall}}

## The declared text boundary

- **Only declared text paths.** Each touched repository declares, in the
  `.polyforge/verify-scope.conf` of its fetched origin default branch, the paths this lane
  may change (`micro_path=` lines; the workflow's engine, `verify-scope --micro --json`, reports them as
  `micro_paths`). Change files under those declarations only, and only their text.
  `AGENTS.md` and `CLAUDE.md` are never in bounds.
- **No code, config, generated file or test.** No product behavior changes on this lane:
  no source code, no configuration, no generated output, no test or assertion.
- **Never widen the declaration.** Never edit `.polyforge/verify-scope.conf`: the
  judgment reads the declaration at the diff base, so an edit there cannot admit a path —
  it is itself out of bounds.
- **Work only inside the declared repo worktree(s)** (`repos/<name>/**`), and commit the
  change in each repo with a message that names what it changes.

## The evidence and its plan

In every repo you changed, after your last commit, run the micro evidence from the repo
clone:

    sh "$PF_SHARED_DIR/scripts/verify-scope" --micro --run

(the engine in this lane's snapshot). It reads the base declaration and runs the repository's fast-phase commands, then its
declared `micro_check` commands, in order from the repository root, stopping at the first
non-zero. A red command is your repair to make inside the declared paths (a missing locale
key, a broken link); never change the check. Then read the same list with `--micro --json`
and write the evidence plan, `artifacts/fix/evidence.json`: a JSON array with one
`{"repo": "<name>", "line": "<cmd>"}` per command, per changed repo, in the order the
engine lists them — exactly each command's `cmd` text, nothing added or dropped. The lane's
`evidence` step re-runs exactly this plan on the host, from each repository's root (the
run script with `--isolation host`), and `review` judges it.

## Bounds, before you complete

Your skill is the procedure: run the pre-written `facts.py micro-bounds` on your plan as
written and route on its exit code. In bounds, hand off `done`. Out of bounds, the change
is not this lane's: ask the owner to re-file it on `develop` (`pf step ask`), end your
turn, and act on the reply — `cancel`, citing it, when the owner agrees. You never hand
off `done` out of bounds.

## If you cannot finish

If the evidence cannot be made green within reasonable effort, never claim it green:
hand off `blocked`, the red command and its output tail in your body.

This lane has no plan, so your hand-off body's 给下游与未决 is its only place for an
owner-review declaration: when the ticket asks for the owner to look at material before
it ships, declare it there with the line below.

{{owner_review}}

## Principles

A text change has the shortest path: edit the declared text, run the declared evidence, write the plan, judge the bounds.

{{principles}}
