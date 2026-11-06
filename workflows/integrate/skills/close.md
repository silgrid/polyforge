---
name: close
description: Decide the batch's result from the gate with integrate.py close, register the moves to merged it writes for every landed item a green tag covers, and register the batch's record for the work item's wrap (pf step hub wrap-attrs integration_batch).
commands: [integrate.py]
---
# close

You end the batch. The one command decides the result from the gate's files, names every
landed item a green tag now covers, assembles the batch's record and writes two files you
register: the moves to `merged`, and the record the work item is wrapped with. You never
write to aihub but through that registration, never tag, and never edit a result file.

## 开工前读什么、核对什么 — before you start: what you read and check

1. **The upstream files.** `artifacts/integrate/batch.json` and
   `artifacts/integrate/revert.json` exist and parse as their commands' result files;
   `artifacts/integrate/rulings.json` parses; `artifacts/integrate/gate-decision.json`
   parses and its `result` is `green`, `red` or `empty`; when it is `green` or `red`,
   `artifacts/integrate/gate.json` exists and its `verdict` agrees — `pass` for green,
   `fail` for red — unless `artifacts/integrate/triage.json` is this gate run's (its
   `details.gate_run` is the sha256 of `gate.json`) with a `details.regate` whose `failed`
   is empty (every failure was flaky and the gate passed again: green).
2. **The steps before you.** full-gate's latest section (the result, the tickets it
   registered, the owner's words it acted on) and collect's (the hold rulings).
3. A check of 1 that does not hold: `pf step ask` the owner naming the source and what did
   not hold, end your turn, and act on the reply — `rewind --to full-gate`, `rewind --to
   close` (this step again from the start), or `cancel`, each `--per-owner <the reply's at>`.

## Procedure

1. **Reuse or run.** When `artifacts/integrate/close.json` exists, its `round` equals
   `$PF_ROUND`, and `artifacts/integrate/close-ops.json` and
   `artifacts/integrate/integration-batch.json` are beside it, reuse them and go to 3.
   Otherwise 2.
2. **Run this one line as written**, from the capsule root, without a Bash timeout (a named
   gate, triage, revert or call-ledger file that does not exist — a step that never ran on
   an empty batch — is taken as absent; a missing rulings or decision file is exit 2, since
   collect and full-gate always write theirs; the command reads the project's pool itself through this
   step's hub reads, and the fix tickets this batch filed from the step call ledger):
   `"$PF_PYTHON" "$PF_SHARED_DIR/scripts/integrate.py" close --batch artifacts/integrate/batch.json --revert artifacts/integrate/revert.json --gate artifacts/integrate/gate.json --triage artifacts/integrate/triage.json --rulings artifacts/integrate/rulings.json --decision artifacts/integrate/gate-decision.json --calls runtime-state/step-calls.jsonl --ops-out artifacts/integrate/close-ops.json --record-out artifacts/integrate/integration-batch.json --json --out artifacts/integrate/close.json`
3. **Route on the exit code.**
   - **0** — register what the command wrote, exactly these lines, in this order:
     `pf step hub batch @artifacts/integrate/close-ops.json` — always (one `attrs` write
     per landed item a green tag covers: its whole record moved to `merged`, fenced on
     the record as read; an empty array — a red, empty or refused batch, or nothing newly covered — is
     accepted as a recorded no-op);
     `pf step hub wrap-attrs integration_batch @artifacts/integrate/integration-batch.json`
     — always (the batch's record — `{result, main, tags, items, waived, green_items,
     fix_tickets, hold_rulings, reverts, refused, empty, closed_at}` — which the wrap carries
     into this work item's attrs; the step declares `wrap_attrs: [integration_batch]`, so
     `pf step handoff --status done` is refused until this line was accepted).
     Then, as the lane's last step, write the wrap summary (the step header's Memory
     section), then hand off `done`.
   - **2** — not judged (an input unreadable, a fetch failed, the pool unreadable through
     the hub, every judged repo passed but one carries no tag, the gate decision
     contradicting the gate files): wait one minute and run 2 again, at most twice more;
     still 2 → hand off `blocked` with the command's `summary`. A passed batch without its
     tag, or a decision the gate files contradict, is the full-gate step's to redo: say so
     in the body.

## 交接要写什么 — what your hand-off carries

- **Status.** `done` on exit 0 after the registration lines were accepted; `blocked` on
  exit 2 after the retries; `rewind` or `cancel` only on the owner's word (`--per-owner`).
- **结论**: the batch's result and how many landed items move to merged.
- **做了什么**: the command's `summary`; the green tags; the red heads on a red batch.
- **给下游与未决**: on a red batch, the fix tickets that must land before the next batch can
  go green.
- **Evidence**: `artifacts/integrate/close.json`, `artifacts/integrate/close-ops.json`,
  `artifacts/integrate/integration-batch.json`.

## Never

Write a green tag, run git or gh yourself; edit `close.json`, `close-ops.json`,
`integration-batch.json` or any upstream result; register a write the command did not
write, or any `wrap-attrs` but the line above; use a hub verb this skill does not name; end
your turn needing the owner without a `pf step ask`; run a command in the background and
poll it, or give it a timeout or deadline of your own.
