---
name: fix
description: Implement the ticket's text change inside the paths the repository declares text-only, run the repository's micro evidence (fast phase plus declared micro checks) and report the changed paths and the commands run.
output:
  type: object
  required: [commits, build_status, changed_paths, evidence]
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
      description: Every commit THIS attempt made, in the order committed. At least one is required - a completed fix that changed nothing is not a valid completion (report the blocker honestly instead).
    build_status:
      type: string
      enum: [green, red]
      description: green only when every evidence command exited 0 on the committed tree; red when one did not, or when the change cannot be made inside the declared paths.
    changed_paths:
      type: array
      items: { type: string }
      description: Every repo-relative path the change touches, prefixed by its repo (`<repo>/<path>`), both sides of a rename or deletion.
    evidence:
      type: array
      description: The micro evidence commands you ran, per repo, in the order you ran them - exactly the `cmd` text of `tools/verify-scope --micro --json`'s commands (the fast phase, then the micro checks). The driver builds the delivery's credential plan from this list.
      items:
        type: object
        required: [repo, command]
        properties:
          repo: { type: string, description: The repo directory name under repos/. }
          command: { type: string, description: The command exactly as the engine reports it, run from that repo's root. }
    summary:
      type: string
      description: Short prose summary - what text changed where, the evidence commands and their result, and anything review must know.
---

# fix

Implement the ticket. This lane has no spec and no plan: `runtime-state/work-item.json`
— its goal and content — is the whole specification. Implement what it names, nothing
more. If this is a rewind re-entry, read `execution.json`'s `pending_rewind_input` for
this step (a `review` or `deliver` FAIL verdict) and address every point it raises first.

## The declared text boundary

- **Only declared text paths.** Each touched repository declares, in the
  `.polyforge/verify-scope.conf` of its fetched origin default branch, the paths this lane
  may change (`micro_path=` lines; `tools/verify-scope --micro --json` reports them as
  `micro_paths`). Change files under those declarations only, and only their text.
  `AGENTS.md` and `CLAUDE.md` are never in bounds.
- **No code, config, generated file or test.** No product behavior changes on this lane:
  no source code, no configuration, no generated output, no test or assertion.
- **Never widen the declaration.** Never edit `.polyforge/verify-scope.conf`: the driver
  reads the declaration at the diff base, so an edit there cannot admit a path — it is
  itself out of bounds.
- **The driver is the authority.** After you complete, the driver judges the diff
  against each repository's base declaration; anything outside it ends the run without a
  rewind or a merge and asks for a re-file on the `autopilot` lane (the run has already
  ended there, so no `cancel` answer is needed). If the ticket cannot
  be done inside the declared paths, do not expand them: complete with
  `build_status: red` and say in `summary` what lies outside and why.
- **Work only inside the declared repo worktree(s)** (`repos/<name>/**`), and commit the
  change in each repo with a message that names what it changes.

## The evidence

In every repo you changed, after your last commit, run the micro evidence from the repo
clone:

    <data-dir>/release/current/tools/verify-scope --micro --run

(`<data-dir>` is `$POLYFORGE_DATA_DIR` when set, else `~/.local/share/polyforge`). It
reads the base declaration and runs the repository's fast-phase commands, then its
declared `micro_check` commands, in order from the repository root, stopping at the first
non-zero; a repository that declares no `micro_path`, a missing prerequisite or an empty
command list fails it. Read the same list with `--micro --json` and report, in
`evidence`, each command's `cmd` with its repo, in the order run. A red command is your
repair to make inside the declared paths (a missing locale key, a broken link); never
change the check. Report `changed_paths` and the evidence result in `summary`: `review`
reads them, and `deliver` re-runs exactly these commands on the delivered HEAD.

## If you cannot finish

If the change cannot be made inside the declared paths, or the evidence cannot be made
green within reasonable effort, do not fabricate `build_status: green`. Leave it `red`
and explain the blocker in `summary`.

This lane has no plan, so your result's `summary` is its only place for an owner-review
declaration: when the ticket asks for the owner to look at material before it ships,
declare it there with the line below.

{{owner_review}}

## Principles

A text change has the shortest path: edit the declared text, run the declared evidence, report what ran.

{{principles}}

## Output

Emit the final JSON object per the schema above: every commit made (at least one), the
build status, the changed paths, the evidence commands in the order run, and a prose
summary for `review` and a future rewound instance of this step.
