---
name: repair
description: Fix the batch's red units in the capsule's clones - only tests or documentation - on one local branch per repo off the pinned heads, run each unit's own rerun command for the host green, write the repair plan the review judges and the revert step lands, and put a fix that would leave that scope to the owner; never declare the gate green.
commands: []
---

# repair

The full gate judged the batch red and handed it back to you: every red
unit carries an attribution and a fix that touches only tests or
documentation. Your skill is the whole procedure — what you read before
you start, how you fix and verify, what you ask the owner and what your
hand-off carries. You run only after a rewind covered you: on your
first round of a batch that never came back red there is nothing of
yours to do — the lane records you skipped. On a round that reached you
with no repair brief, change nothing, commit nothing and hand off
`done`: the steps after you run exactly as they would without you.

## 开工前读什么、核对什么 — before you start: what you read and check

1. **The repair brief.** `artifacts/integrate/repair.json` parses as the
   gate wrote it — `{"round": <number>, "heads": {"<repo>": "<sha>"},
   "units": [{"repo", "unit", "item", "file", "log", "excerpt",
   "rerun"}]}`. The brief's `round` is the gate's, never yours — no
   comparison with your own round decides anything (rounds are each
   step's own: a review FAIL or an owner's rewind moves them apart; the
   brief's round names the gate red you owe work to, and the gate
   rewrites the file when it re-runs). No brief at all: nothing of the
   batch's repair is yours — change nothing, commit nothing, hand off
   `done`.
2. **The refusal of this brief.** `artifacts/integrate/repair-refused.json`
   carries `"brief_round"`: when it names the brief before you, the owner
   already ruled those units out of the batch — hand off `done` and touch
   nothing. A refusal of an older brief is history: a newer brief is new
   work.
3. **Your own earlier round.** `artifacts/integrate/repair-plan.json`
   with `"brief_round"` equal to the brief's `round` is the work the
   review sent back: read the section and its findings in
   `artifacts/review.md` — each blocking finding is fixed on the branch
   (a new commit — history is never rewritten), or it stands and your
   hand-off says why. A plan answering an older brief is history: the
   gate re-red and wrote a new brief, and the older branch is already
   landed or dead.
4. **The clones and the pins.** Each unit's `repo` has its clone under
   `repos/`, and the brief's `heads` name the pinned head the gate
   judged — the branch you grow must stand on that head, never on a
   default branch that may have moved past it.

## Procedure

1. **Branch off the pin — or continue your own.** When the plan on disk
   answers this very brief (its `brief_round` equals the brief's
   `round`), an existing plan answering this very brief is kept: check
   its branch out as it stands (never `checkout -B`, never rewritten
   history) and build on it with new commits answering the review's
   findings. Otherwise start fresh: per repo the brief names, from the
   capsule root,
   `git -C repos/<repo> fetch origin`
   then `git -C repos/<repo> checkout -B integrate/repair-$PF_WI_ID <the pinned head>`
   (the older brief's landing is already on the default branch; the
   branch starts over on the new pins).
2. **Fix only tests or documentation.** Fix each unit exactly as its
   `log` and `excerpt` show it, touching only the repository's test and
   documentation files. The moment a fix turns out to need more — a
   runtime branch, a configuration value, a product file, a behaviour
   the tests should observe but do not — stop, do not commit it, and put
   it to the owner in ONE `pf step ask`: the units, what the fix would
   have to touch, and the ways out — **fix anyway** (the owner's words
   widen the scope: fix exactly what they name, nothing beside),
   **revert <item>** (the batch reverts the blamed item with its landed
   fixes and gates again: write the item into
   `artifacts/integrate/gate-decision.json`'s `reverts` — the file's one
   line yours to write, only on this ruling — remove the repair plan
   file so nothing of this brief is left to land, then hand off `done`:
   a rewind to the later step is the daemon's to refuse, and the lane
   needs none — the review passes with nothing to judge, the revert step
   reads the decision and carries the revert out),
   **hand back** (the batch does not fix these in-batch: write
   `artifacts/integrate/repair-refused.json` —
   `{"brief_round": <the brief's round>, "units", "why", "at"}` with the
   owner's words — remove the repair plan file, and hand off `done`; the
   next gate run files the fix tickets as it did before). End your turn
   there; act on the reply as the ways out read.
3. **Verify each unit on the host.** With the branch checked out, run
   the unit's own `rerun` command from the brief in its clone
   (`repos/<repo>`). The gate's red is its own record — you never
   re-derive it, and no worktree replay is made beside it; the green you
   run here is your evidence the fix took, and its output goes to a log
   file you name. A command the host cannot run (its toolchain lives in
   the verification container) is said so in your hand-off — you fixed
   by the brief's `log` and `excerpt`, and the gate's re-run judges.
   Never claim a green you did not see.
4. **Commit and write the plan.** Commit the fix files alone
   (`git -C repos/<repo> add -- <each path>`, never `-A`), then write
   `artifacts/integrate/repair-plan.json`, whose first key is
   `"brief_round": <the brief's round>` — the brief it answers — beside
   `"repos": [{"name", "branch", "base", "head", "units", "items"}]`:
   the branch, the pinned head it grew from, its head after your
   commits, the units it fixes and the items the gate attributed them
   to.
5. **Hand off `done`.** The review judges your branch next; the landing
   is the revert step's. Nothing you write is a green — the batch is
   green only when the gate's re-run says so.

## 交接要写什么 — what your hand-off carries

- **Status.** `done` with every brief unit fixed on the branch and the
  plan written — or with nothing of yours to do (no brief of this
  round, or the owner's refusal); `blocked` on a real blocker (a clone
  missing, the pin unreachable); `rewind` or `cancel` only on the
  owner's word (`--per-owner`).
- **回应上一轮** (a later round): each of the review's blocking
  findings — the commit that fixed it, or why it stands.
- **结论**: which units are fixed on which branch, and the host green —
  or why the host could not run a unit's command.
- **做了什么**: each unit, its repo and its fix in one line; the branch;
  the plan file.
- **关键决定和理由**: how each unit was fixed; every owner message you
  acted on (its `at`, the words verbatim, your reading) — an out-of-scope
  ruling above all.
- **验证**: each fixed unit's host rerun — its command, its exit code
  and its log file.
- **Evidence**: `artifacts/integrate/repair.json`,
  `artifacts/integrate/repair-plan.json`, the rerun logs.

## Never

Push a branch, open, merge or close a PR, or run gh yourself (the
revert step lands your branch under the merge lock); commit a product
file — runtime code, configuration and generated files are never tests
or documentation; declare the gate green (only its re-run does); edit
another step's result file (`batch.json`, `revert.json`, `gate.json`,
  `triage.json`) — the gate decision's `reverts` on the owner's revert
  ruling is the one line yours to write; run a command in the background
  and poll it, or give it a timeout or deadline of your own; end your
  turn needing the owner without a `pf step ask`.
