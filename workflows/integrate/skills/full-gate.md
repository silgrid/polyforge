---
name: full-gate
description: Gate the batch's pinned heads with the gate script (a pass writes the green tag), triage a red gate with its --triage, register one fix ticket per root cause and the attributions integrate.py records builds, put a revert, a rerun or a red close to the owner, and write the gate's decision for the steps after.
commands: [gate.py, integrate.py]
---
# full-gate

You are the lane's one gate. One script gates every pinned head in the batch capsule's
clones, every command inside each repo's verification container (and, on a pass, writes
the green tag itself); the same script with `--triage` triages a red gate — re-runs, the
baseline, attribution, bisection — reading the project's pool itself through this step's
hub reads. Its result files say `command` `gate.py`. You decide the fix tickets (one per
root cause) and register them; you put to the owner what only the owner may decide. You
never run the tests yourself, never edit a result file, and never write to aihub but
through the registration below.

## 开工前读什么、核对什么 — before you start: what you read and check

1. **revert's hand-off.** `artifacts/integrate/revert.json` parses as the revert script's
   result (`command` is `revert.py`), its `argv` is the revert skill's line for the round's
   list — `--batch artifacts/integrate/batch.json`, one `--item <id>` per id of
   `artifacts/integrate/rulings.json`'s `reverts` and, when the lane came back from this
   gate, of `artifacts/integrate/gate-decision.json`'s `reverts` (each once, in that order),
   then `--ops-out artifacts/integrate/revert-ops.json --json --out
   artifacts/integrate/revert.json` — and every entry of `details.reverts` is `done: true` with a
   `revert_merge` in each of its `repos` (the clones need not stand at the pins: the gate
   fetches and checks out each pinned head itself). A check that does not hold is this
   gate's FAIL: write the decision file (4 below: `result` `empty`, no `reverts`) and hand
   off `fail`, the body naming the source and what did not hold.
2. **The batch.** `artifacts/integrate/batch.json` (`details.items`: each landed item, its
   `red_rounds`, its PRs under `repos[].pr`).
3. **An earlier round of this gate.** When `artifacts/handoff.md` carries an earlier
   `full-gate` section, read it: what was red, what the owner ruled, which items the revert
   step then reverted.

## Procedure

1. **Nothing gated:** when `details.gated` of `revert.json` is empty, run no gate: write the
   decision file with `result` `empty` and hand off `done`.
2. **Reuse or run:** when `artifacts/integrate/gate.json` exists, its `round` equals
   `$PF_ROUND` and its `heads` equal `revert.json`'s `heads`, the gate already ran on these
   pins this round — read it and continue at 3 with its exit code (`exit_code`); the same
   for `artifacts/integrate/triage.json` at the triage routes. A result from an earlier
   round, or on other heads, is not this round's. Otherwise run this line as written,
   from the capsule root, without a Bash timeout (it takes the full tool window;
   `--mark-green` writes the green tag itself when every repo passes):
   `"$PF_PYTHON" "$PF_SHARED_DIR/scripts/gate.py" --isolation container --capsule . --pins artifacts/integrate/revert.json --mark-green --summary "integration batch $PF_WI_ID" --json --out artifacts/integrate/gate.json`
   (`--help` is the script's full reference)
3. **Route on the gate's exit code:**
   - **0** — `result` `green` (the tags are in `details.tags`).
   - **1** — a repo's gate failed: run the triage, this one line as written:
     `"$PF_PYTHON" "$PF_SHARED_DIR/scripts/gate.py" --isolation container --triage --capsule . --pins artifacts/integrate/revert.json --gate artifacts/integrate/gate.json --batch artifacts/integrate/batch.json --mark-green --summary "integration batch $PF_WI_ID" --json --out artifacts/integrate/triage.json`
     then route on the triage's exit code:
     - **0** — every failure was flaky and the gate passed again (`details.regate`, its
       tags written): `result` `green`.
     - **3** — the gate passed again but a tag was not written: as the gate's 3.
     - **2** — the triage could not run: as the gate's 2 (the triage line again, twice at
       most; then `blocked`).
     - **1** with `details.all_flaky` true — every failing unit re-ran green, yet the gate
       ran once more (`details.regate`) and failed: nothing is attributed and nothing is
       settled. `pf step ask` the owner with the regate's failed repos and log tails and
       the ways out — **rerun**, **close** red, **revert <item>** — and act on the reply as
       in b. below. Never close red on it without the owner's words.
     - **1** — the red stands. Read `details.units`: each has `class` — `flaky` (settled),
       `pre_existing` (red at the baseline too: the owner's, never an item's), `attributed`
       (`item` names the landed item, `root` the first red commit or the merge, `why` how
       it was attributed, `log`, `excerpt`), `unattributed` (no item; bisection found
       nothing or the first red commit is outside the batch: `outside`). Then:
       a. **The fix tickets.** Group the `attributed` units by `item` and root cause (one
          reason, one ticket; read each unit's `log` and `excerpt`; a source can need
          several tickets, never one ticket for two sources). Write
          `artifacts/integrate/full-gate-records.json`, a JSON array: per group one
          `{"verb": "file", "key": "<this work item's id>/fix/<item>/<n>", "lane": "develop",
          "goal": "<one line naming the repo, the units and the cause>", "content":
          "<background: the root cause, the decisive log lines, the source item's PRs>\n\n## Sources\n- <item>\n- <each of its PRs>\n\n## Units\n- <unit>\n…\n\n## Acceptance\n- `<unit>` passes on the fixed head (the full gate, <repo>, <command>)\n…",
          "priority": "high", "labels": ["integration-fix"]}` (`n` counting the item's
          tickets from 0; the acceptance is never empty and names every unit the ticket
          covers — that is what makes the ticket claimable; no attributed unit, no file);
          then per blamed item one `{"item": "<item>", "blame": ["key:<each of the item's
          keys above>"]}` — `integrate.py records` turns it into the item's record write:
          landed, this batch, one red round more, `revert.json`'s heads as its red heads,
          those tickets.
       b. **What the owner decides.** When a blamed item's `red_rounds` in `batch.json` is
          already 2 or more (two fix rounds failed), or any unit is `pre_existing` or
          `unattributed`: ONE `pf step ask` with, per such item or unit, the facts (the
          units, their class, `why`, `root`, `outside`, the log tail) and the ways out —
          **revert <item>** (this batch reverts it with its landed fixes and gates again),
          **rerun** (the same pins gated again, at most twice in this step), **close** (the
          batch ends red now; the fix tickets ride, nothing is gated again). End your turn.
          On the reply: revert → write the decision file (`result` `red`, `reverts` the
          items) and hand off `--status fail --per-owner <the reply's at>` — no hub writes
          (a FAIL drops them; the gate after the revert triages again); rerun → run the gate
          line of 2 again (count it in `reruns`; after the second rerun ask once more);
          close → `result` `red`, with the tickets of (a).
       c. Otherwise (at least one red unit, every red unit attributed, no source past two
          rounds): `result` `red`, with the tickets of (a).
   - **2** — not judged (the docker engine, the image, a gated repo without
     `.polyforge/Dockerfile.verify`, a fetch or checkout, the deadline): wait one
     minute and run the gate line again, at most twice more; still 2 → hand off `blocked`,
     the command's `summary` and the last gate log's tail (`logs`) in the body. No question
     to the owner: an environment problem is fixed on the machine, and the owner's message
     or `pf resume` gates the same pins again.
   - **3** — every repo passed but a green tag was not written (`details.repos.<repo>
     .tag_error`): hand off `blocked`, naming the repo and the error; the next run of this
     step gates again (the gate finds the tag or writes it).
4. **Write the decision file before every hand-off** —
   `artifacts/integrate/gate-decision.json`, the gate's outcome for the revert and close
   steps (they read it, never your body):

   ```json
   {"round": <$PF_ROUND, a number>, "result": "green|red|empty", "reverts": ["<wi>"], "reruns": <n>}
   ```

   `reverts` is non-empty only on the owner's revert.
5. **Register the tickets and hand off** — when `result` is `red` and you wrote
   `artifacts/integrate/full-gate-records.json`, run this line as written, from the
   capsule root:
   `"$PF_PYTHON" "$PF_SHARED_DIR/scripts/integrate.py" records --in artifacts/integrate/full-gate-records.json --pins artifacts/integrate/revert.json --ops-out artifacts/integrate/full-gate-ops.json --json --out artifacts/integrate/full-gate-records-result.json`
   — exit **0**: register it, exactly this line:
   `pf step hub batch @artifacts/integrate/full-gate-ops.json`; **1**: a rule refused an
   entry (its `summary` names it — an item no longer landed): nothing was written, `pf
   step ask` the owner; **2**: not judged, run it again at most twice more, then hand off
   `blocked`. Then (green, red or empty) hand off `done`.

## 交接要写什么 — what your hand-off carries

- **Status.** `done` with `result` green, red or empty; `fail` only for a revert hand-off
  that did not hold (charged), or the owner's revert (`--per-owner`, not charged — the lane
  goes back to the revert step); `blocked` on exit 2 or 3. This gate is not waivable. When
  the command refuses a `fail` because the rewind budget is spent, `pf step ask` the owner
  (one more round, close red, or cancel).
- **回应上一轮** (a later round): the earlier round's red and the owner's ruling, and what
  the revert (or the rerun) changed about it.
- **结论**: the result, at which heads.
- **做了什么**: what was gated at which heads; each red unit with its class and root cause;
  the fix tickets you registered (their keys) and the items they blame.
- **关键决定和理由**: how you grouped the units into tickets; every owner message you acted on
  (its `at`, the words verbatim, your reading).
- **Evidence**: `artifacts/integrate/gate.json`, `artifacts/integrate/triage.json` (when it
  ran), `artifacts/integrate/gate-decision.json`, `artifacts/integrate/full-gate-records.json`
  and `artifacts/integrate/full-gate-ops.json` (when you wrote them).

## Never

Run the tests, the engine or docker yourself; edit `gate.json`, `triage.json`,
`revert.json` or `batch.json`; register a fix ticket without an acceptance naming its red
units, or one ticket for two sources; revert, rerun or close red without the owner's
words; write the green tag by hand; use a hub verb this skill does not name; end your turn
needing the owner without a `pf step ask`; run a command in the background and poll it, or
give it a timeout or deadline of your own.
