---
name: revert
description: Carry out the owner's reverts with the revert script under the merge locks, write the gate's pins and register the record moves it writes; with nothing to revert, copy the pins.
commands: [revert.py]
---
# revert

You are the one step of the lane that holds the repos' merge locks: while you run, nothing
else merges into the task repos' default branches. The one script opens and merges a
revert PR per repo for each item you pass it, writes the gate's pins with the reverts
applied, and writes the record moves (each reverted item to `reverted`) to a file you
register. You never run git or gh yourself, and you never write to aihub but through that
registration.

## 开工前读什么、核对什么 — before you start: what you read and check

1. **collect's hand-off.** `artifacts/integrate/batch.json` parses as `integrate.py
   collect`'s result file — `schema` is `pf-step-result/1`, `command` is `integrate.py`,
   `heads` maps each repo to a commit and `details.items` is a list — and
   `artifacts/integrate/rulings.json` parses with a `reverts` list. If not, `pf step ask` the
   owner naming what does not hold and the likely cause (collect did not finish, a file was
   edited), end your turn, and act on the reply: `rewind --to collect`, `rewind --to revert`
   (this step again from the start), or `cancel`.
2. **The items to revert, by work item id**, read from files, never from a body: every id
   in `artifacts/integrate/rulings.json`'s `reverts`, plus — when the lane came back here
   from the full gate — every id in `artifacts/integrate/gate-decision.json`'s `reverts`
   (the owner's revert after a red gate; read full-gate's latest section for the words).
   The union, each id once, is the list; an id must be an `id` of `batch.json`'s
   `details.items` or a `wi` of its `details.holds` (a stuck hold the owner ruled reverted
   is a held item, seldom a batch item); `pf step ask` the owner about any other.

## Procedure

1. **Reuse or run.** When `artifacts/integrate/revert.json` exists, its `round` equals
   `$PF_ROUND`, every item of your list is in its `details.reverts` with `done: true` and
   `artifacts/integrate/revert-ops.json` is beside it, reuse them and go to 3. Otherwise 2
   (the script is resumable: a revert planned in an earlier run is finished before
   anything else).
2. **Run the line of your list** as written, from the capsule root, without a Bash
   timeout. `<id>` is taken only from your list (the ids of 开工前 2, in that order, each
   once) — no other word is yours to add:

   | Your list | The line |
   |---|---|
   | one or more ids | `"$PF_PYTHON" "$PF_SHARED_DIR/scripts/revert.py" --batch artifacts/integrate/batch.json --item <id> --item <id> … --ops-out artifacts/integrate/revert-ops.json --json --out artifacts/integrate/revert.json` |
   | empty | `"$PF_PYTHON" "$PF_SHARED_DIR/scripts/revert.py" --batch artifacts/integrate/batch.json --ops-out artifacts/integrate/revert-ops.json --json --out artifacts/integrate/revert.json` |

   With an empty list it copies the pins; `gated` stays collect's, so an empty batch stays
   empty. The full gate checks the result's `argv` against this table.
3. **Route on the exit code.**
   - **0** — `artifacts/integrate/revert-ops.json` is a JSON array the script wrote: one
     `attrs` write per reverted item — its whole record (read through this step's hub)
     moved to `reverted`, with the batch, the revert PRs and merges, fenced on the record
     as read.
     Register it, exactly this line, always:
     `pf step hub batch @artifacts/integrate/revert-ops.json` (an empty array — nothing
     reverted — is accepted as a recorded no-op). Then hand off `done`.
   - **1** — a revert did not land: the forge refused its PR or the merge conflicts in
     code; the entry's `why` says which. `pf step ask` the owner naming the item, the repo,
     the PR (`revert_pr`) and `why`, and what you suggest; end your turn. On the reply: the
     owner fixed it by hand → `rewind --to revert` (this step again: the plan keeps the
     revert and the next run finishes it); the owner wants the batch back at collect →
     `rewind --to collect`; the owner gives up the batch → `cancel`. Each `--per-owner
     <the reply's at>`.
   - **2** — not judged (a clone missing, git or gh unreachable, an item not in the batch,
     the deadline): wait one minute and run 2 again, at most twice more; still 2 → hand off
     `blocked` with the script's `summary`. An item the script refuses as not in the
     batch is not an environment problem: `pf step ask` the owner with the refusal.

## 交接要写什么 — what your hand-off carries

- **Status.** `done` on exit 0 after the batch line was accepted; `blocked` on exit 2 after
  the retries; `rewind` or `cancel` only on the owner's word (`--per-owner`).
- **回应上一轮** (after a full-gate FAIL): the owner's revert ruling the gate's section
  quotes, and the reverts this round carried out for it.
- **做了什么**: the reverts that landed (item, repo, PR, merge), or that there was nothing
  to revert; the pins the gate gets (`revert.json`'s `heads`).
- **关键决定和理由**: where each item to revert came from (collect's ruling, or the owner's
  revert after the gate), and each owner message you acted on (its `at`, the words, your
  reading).
- **Evidence**: `artifacts/integrate/revert.json`, `artifacts/integrate/revert-ops.json`.

## Never

Run git or gh yourself; open, merge or close a PR by hand; write or edit `revert.json`,
`revert-plan.json`, `revert-ops.json` or `batch.json`; pass an item the owner did not rule;
use a hub verb this skill does not name; end your turn needing the owner without a
`pf step ask`; run a command in the background and poll it, or give it a timeout or
deadline of your own.
