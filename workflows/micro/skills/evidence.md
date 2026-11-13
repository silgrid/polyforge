---
name: evidence
description: Re-run fix's evidence plan on the host with one pre-written script and route on the result file it writes - a red the change caused FAILs to fix, a red it cannot have caused is the owner's call (ask, then accept the red or have it fixed).
commands: [run.py]
---
# evidence

One pre-written script runs every line of fix's evidence plan
(`artifacts/fix/evidence.json`, `[{repo, line}]`) on the host (`--isolation host`: the
plan's lines are text checks — links, locale keys, spelling — that need no container), from
its repository's root, in the plan's order, stopping at the first line that fails, and
writes the result file; the `verdict` in that file is the exit code's alone (0 pass, 1
fail, 2 error), never yours. Your job: check the sync's hand-off, run the line as written,
read the result and route on it.

## 开工前读什么、核对什么 — before you start: what you read and check

1. **The sync's hand-off.** Read sync's latest section of `artifacts/handoff.md` and
   `artifacts/sync/result.json`: its `argv` is the arguments of the sync skill's start
   line or of its continue line (`--json --out artifacts/sync/result.json`, or
   `--continue --json --out artifacts/sync/result.json`: the words after
   `"$PF_PYTHON" "$PF_SHARED_DIR/scripts/sync.py"`), every repo's `conflicts` is `[]`, and
   `git -C repos/<name> rev-parse HEAD` equals that repo's `after`. Anything else is this
   gate's FAIL: hand off `fail`, the body naming "sync hand-off mismatch: <repo, or argv>:
   <what differs>".
2. **What the change touched.** fix's latest section (what changed where) and its plan,
   `artifacts/fix/evidence.json`: so a red can be placed.
3. **An earlier round of this gate.** When `artifacts/handoff.md` carries an earlier
   `evidence` section, read it and the 回退 that followed: what failed then, and what fix
   did about it.

## Procedure

1. **Reuse or run.** An `artifacts/run/result.json` whose `round` equals `$PF_ROUND`, with
   no owner message asking you to run again, is this round's result: go to 3 (a resumed
   conversation never runs the plan twice). No result, or one of an earlier round: 2 —
   never judge this round on an earlier round's result. **Same-tree rule:** an earlier
   round's result whose `heads` equal every repo's current HEAD and whose `verdict` is
   `fail` is not run again — nothing changed, so it would fail the same way: `pf step ask`
   the owner with that round's red line and output tail (run again / accept the red /
   cancel), then end your turn.
2. **Run this line as written** — no argument added or removed, no Bash timeout:
   `"$PF_PYTHON" "$PF_SHARED_DIR/scripts/run.py" --isolation host --plan-from artifacts/fix/evidence.json --json --out artifacts/run/result.json`
3. **Route on the exit code** (the result's `details`: `runs[{repo, line, exit_code,
   timed_out, log, output_tail}]`, `not_run[]`).
   - **0** — every line ran and exited 0: hand off `done`.
   - **1** — read the failed run's `output_tail` (and its `log`). A red the change caused —
     a broken link, a missing or malformed locale key, a spelling or format check on a file
     the change touched, a plan that names no line — is the change's: hand off `fail` (the
     lane goes back to `fix`). A red the change cannot have caused (a check on files it
     never touched, a line killed at the deadline): `pf step ask` the owner with the red
     line, its output tail and your reading, then end your turn; on the reply — run again:
     2; the owner wants it fixed: `fail --per-owner <the reply's at>`; the owner accepts
     the red: `waive --per-owner <the reply's at>`.
   - **2** — not judged (a line that could not start, a repository missing, the plan
     unreadable). Wait one minute and run 2 again, at most twice more; still 2: hand off
     `blocked`, the result's `summary` in the body and what the repository or the machine
     needs as `--reason`.

## 交接要写什么 — what your hand-off carries

- **Status.** `done` on exit 0; `fail` on a red the change caused, or the owner's fix
  request on one it cannot have caused (`--per-owner`); `waive` when the owner accepted the
  red (`--per-owner`: publish checks the words the daemon quotes in your header);
  `blocked` on exit 2 after the retries.
- **回应上一轮** (a later round): the earlier round's red line — green now, or still red.
- **结论**: the lines run per repo, the red one (or none).
- **发现的问题和风险**: on a fail, the repo, the red line and its decisive output lines — what
  fix's next round must repair.
- **关键决定和理由**: why a red is the change's (or why it cannot be), and each owner message
  you acted on (its `at`, the words, your reading).
- **Evidence**: `artifacts/run/result.json`.

## Never

Write or edit `artifacts/run/result.json`, `artifacts/fix/evidence.json` or
`runtime-state/asks.jsonl`; run a plan line, a build or a test by hand; hand off `done` on
a red; end a turn that needs the owner without `pf step ask`; pass a Bash timeout; run a
command in the background and poll it, or give it a timeout or deadline of your own.
