---
name: collect
description: Pin the default branches and take the integration batch with integrate.py collect (it reads the project's pool through the step's hub); put every stuck hold to the owner, write the owner's rulings to a file and register the hub writes integrate.py records builds for them.
commands: [integrate.py]
---
# collect

You open the integration batch. The one command pins each task repo's default-branch head
and takes the batch; it reads the project's integration pool itself (the landed items, the
open batches, the heads the newest red batch failed on, the state of every registered fix
ticket — a ticket outside this project reads as not found, so not open) through this
step's hub reads. You never pin, fetch or judge anything by hand, and
nothing you decide reaches aihub except through the hub writes below, made when you hand
off `done`.

A person filed this batch (`pf file create --bundle integrate`, then `pf submit`); the lane
admits one unfinished batch per project. A hold is cleared only here: on the owner's ruling
in your `pf step ask`, by the write that cites the owner's reply — nothing outside a running
batch clears one.

## 开工前读什么、核对什么 — before you start: what you read and check

1. **The batch's work item.** The opening of `artifacts/handoff.md` — the batch is its work
   item; you are the lane's first step, so no upstream hand-off precedes you.
2. **A later round.** When `artifacts/handoff.md` carries an earlier `collect` section, read
   it and whatever sent the lane back here (a 回退 naming this step: the owner's words in
   its header, the step that asked for it and why).

## Procedure

1. **Reuse or run.** When `artifacts/integrate/batch.json` exists and its `round` equals
   `$PF_ROUND`, the batch is already taken this round (you were resumed): read it and go to
   3. Otherwise 2. Never judge this round on a result written in an earlier round.
2. **Run this line as written**, from the capsule root, without a Bash timeout:
   `"$PF_PYTHON" "$PF_SHARED_DIR/scripts/integrate.py" collect --json --out artifacts/integrate/batch.json`
3. **Route on the exit code.**
   - **0** — read `artifacts/integrate/batch.json`. Its `details` carry `refused` (another
     batch of the project is not finished: nothing else is yours), `empty` (nothing new to
     gate; the steps after you still run and end the batch empty), `heads`, `gated`,
     `items`, `skipped` and `holds`. Every entry of `details.holds` with `stuck: true`
     needs the owner: put them all in ONE `pf step ask` — per hold the item (`wi`,
     `name`), the `reason` the command gives, its `red_rounds`, its `fix_tickets` and
     `prs`, and the three ways out — **revert** (this batch reverts the item with its
     landed fixes and gates the revert), **clear** (the item is released to merged, the
     owner's words recorded on it as the reason; a red still on the
     default branch keeps failing later batches), **fix** (another fix ticket, filed on the
     `develop` lane with an acceptance naming what must pass) — then end your turn. Read the
     reply item by item (one reply may rule several holds; ask again while a hold is left
     undecided). No stuck hold, or a refused batch: nothing to ask.
   - **2** — not judged (the pool unreadable through the hub, a fetch failed, no clones):
     wait one minute and run 2 again, at most twice more; still 2 → hand off `blocked`,
     the command's `summary` and the log tail in the body.
   - any other code — treat as 2.
4. **Write the rulings file, always** — `artifacts/integrate/rulings.json`, for the revert
   and close steps (a refused batch, or one without a stuck hold, writes empty lists):

   ```json
   {"round": <$PF_ROUND, a number>,
    "reverts": ["<wi of each hold ruled revert>"],
    "hold_rulings": [{"item": "<wi>", "ruling": "revert|clear|fix", "words": "<the owner's words, verbatim>", "at": "<that message's at>"}]}
   ```
5. **Register the hub writes the rulings call for** — only when a hold was ruled `clear` or
   `fix`. Write `artifacts/integrate/collect-records.json`, a JSON array, in this order:
   - per `fix`: one fix ticket, then its registration on the held item —
     `{"verb": "file", "key": "hold-fix/<item>/<n>", "lane": "develop", "goal": "<one line>",
     "content": "<background>\n\n## Sources\n- <item>\n- <each of its PRs>\n\n## Acceptance\n- <criterion>\n…",
     "priority": "high", "labels": ["integration-fix"]}` — `n` the number of the hold's
     `fix_tickets` (the same key for the same ruling in any batch, so a ticket filed by a
     batch that died before registering it is found, not filed twice); the background
     names what the blaming gate found red, the acceptance that it passes on the fixed
     head — then `{"item": "<item>", "fix": ["key:hold-fix/<item>/<n>"]}` (added to the
     hold's fix tickets; the `key:` reference becomes the filed ticket's id when the writes
     are made: the hold reads it, and stays open while the new ticket can still release it);
   - per `clear`: `{"item": "<item>", "clear": "<that message's at>"}` — the stamp of the
     owner message (a reply or message to this step in this round, `runtime-state/asks.jsonl`)
     whose ruling it carries out: the record is written with that message's words verbatim
     as the reason, its writer and stamp, and the daemon checks the message and records it
     with the write.
   Then run this line as written, from the capsule root:
   `"$PF_PYTHON" "$PF_SHARED_DIR/scripts/integrate.py" records --in artifacts/integrate/collect-records.json --ops-out artifacts/integrate/collect-ops.json --json --out artifacts/integrate/collect-records-result.json`
   and route on its exit code: **0** — register it, exactly this line:
   `pf step hub batch @artifacts/integrate/collect-ops.json`; **1** — a rule refused an
   entry (its `summary` names each: a hold already released, another batch of the lane
   running, no such owner message): nothing was written — tell the owner in a `pf step
   ask` and act on the reply; **2** — not judged (an input or the hub unreadable): run it
   again, at most twice more, then hand off `blocked` with its `summary`.
   A `revert` ruling registers nothing here: the revert step carries it out.

## 交接要写什么 — what your hand-off carries

- **Status.** `done` once the batch is taken and every stuck hold is ruled (or refused /
  empty / no hold); `blocked` on exit 2 after the retries; `cancel` or `rewind` only on the
  owner's word (`--per-owner`).
- **结论**: the batch taken (items, gated repos), or why it is empty or refused.
- **关键决定和理由**: every hold ruling — the item, the owner's message (`at`, words
  verbatim) and your reading of it; the fix tickets and hold clears you registered.
- **给下游与未决**: the items ruled reverted (the revert step reads them from
  `artifacts/integrate/rulings.json`, not from this body).
- **Evidence**: `artifacts/integrate/batch.json`, `artifacts/integrate/rulings.json`, and
  `artifacts/integrate/collect-records.json` and `artifacts/integrate/collect-ops.json` when
  you wrote them.

## Never

Write or edit `artifacts/integrate/batch.json`, `collect-ops.json` or `runtime-state/asks.jsonl`; run git, gh
or `pf ops` yourself; decide a stuck hold without the owner's words; register a write the
owner did not rule; use a hub verb this skill does not name; end your turn needing the
owner without a `pf step ask`; run a command in the background and poll it, or give it a
timeout or deadline of your own.
