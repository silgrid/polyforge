---
name: verify
description: Run the light verification of the task repos as they stand after the sync and route on the result file the command writes - never pass a red bar you cannot place.
commands: [verify.py]
---
# verify

One pre-written script runs the light tier over the synced tree and writes the result
file; the `verdict` in that file is the exit code's alone (0 pass, 1 fail, 2 error), never
yours. Your job: check the sync's hand-off, run the line as written, read the result and
route on it — the red you can place is this gate's FAIL, the red you cannot is the owner's
call, and a pass is the command's, not yours.

## 开工前读什么、核对什么 — before you start: what you read and check

1. **The sync's hand-off.** Read sync's latest section of `artifacts/handoff.md` and
   `artifacts/sync/result.json`: its `argv` is the arguments of the sync skill's start
   line or of its continue line (`--json --out artifacts/sync/result.json`, or
   `--continue --json --out artifacts/sync/result.json`), every repo's `conflicts` is
   `[]`, and `git -C repos/<name> rev-parse HEAD` equals that repo's `after`. Anything else
   is this gate's FAIL: hand off `fail`, the body naming "sync hand-off mismatch: <repo, or
   argv>: <what differs>".
2. **Where a red could come from.** `artifacts/plan.md` (`## Units`, when the lane has a
   plan) and build's latest section: what the change touched, so a red can be placed.
3. **An earlier round of this gate.** When `artifacts/handoff.md` carries an earlier
   `verification` section, read it and the 回退 that followed: what failed then, and what
   the build did about it.

## Procedure

1. **Reuse or run.** An `artifacts/verify/result.json` whose `round` equals `$PF_ROUND`, with
   no owner message asking you to run again, is this round's result: go to 3 (a resumed
   conversation never runs the bar twice). No result, or one of an earlier round: 2 — never
   judge this round on an earlier round's result. **Same-tree rule:** an earlier round's
   result whose `heads` equal every repo's current HEAD and whose `verdict` is `fail` is not
   run again when `artifacts/build/units.json` is unchanged too (its sha256 equals the
   `units.json sha256:` line of `artifacts/verify/report.md`) — nothing changed, so it would
   fail the same way: `pf step ask` the owner with that round's red checks (run again /
   accept the red / cancel), then end your turn.
2. **Run this line as written** — no argument added or removed, no Bash timeout:
   `"$PF_PYTHON" "$PF_SHARED_DIR/scripts/verify.py" --tier light --unit-runs artifacts/build/units.json --json --out artifacts/verify/result.json`
   (a repo carrying `.polyforge/Dockerfile.verify` runs its bar in its verification
   container, any other on the host; `--unit-runs` runs each unit's green run as the build
   recorded it, beside the light plan: one check `<repo>:unit-run:<id>` per unit; `--help`
   is the script's full reference), then write the line `units.json
   sha256: <digest>` (`sha256sum artifacts/build/units.json`) as the first line of
   `artifacts/verify/report.md`, whatever the exit.
3. **Route on the exit code.**
   - **0** — add to `artifacts/verify/report.md` the verdict and each check of
     `details.checks[]` (id, repo, status) in a few lines; hand off `done`.
   - **1** — read the checks whose `status` is `fail` and the tail of each one's `log` (a
     `units[]` entry marked `flaky` or `pre_existing` already passed on record: the command
     re-ran and baselined it). A unit's `failure` is `browser_crash` — Chromium's renderer
     crashed (`Target crashed` / `Page crashed`), most often the container's memory OOM
     or `assertion`: never place a `browser_crash` on the change from its
     log; still red, it is a red you cannot place. A unit with `not_rerun` is neither a
     proven red nor flaky (its re-run never started): `pf step ask` the owner with those
     units by check (usually: run again). A red you can place on the change — a unit of the plan (a
     failing `<repo>:unit-run:<id>` check is that unit's own run), a file the change
     touched (`git -C repos/<name> diff --stat origin/<default>...HEAD`), a suite whose
     check names what the change touched — is the change's: hand off `fail` (the lane goes
     back to `build`). A red you cannot place: `pf step ask` the owner with the red checks,
     the log tails and your reading, then end your turn; on the reply — run again: 2; the
     owner wants it fixed: `fail --per-owner <the reply's at>`; the owner accepts the red:
     `waive --per-owner <the reply's at>`.
   - **2** — not judged (the environment, the scope engine, a container). Wait one minute
     and run 2 again, at most twice more; still 2: hand off `blocked`, the result's
     `summary` and the last log's tail in the body, what the machine needs as `--reason`.

## 交接要写什么 — what your hand-off carries

- **Status.** `done` on exit 0; `fail` on a red you placed, or the owner's fix request on
  one you could not (`--per-owner`); `waive` when the owner accepted the red
  (`--per-owner`: publish checks the words the daemon quotes in your header); `blocked` on
  exit 2 after the retries.
- **回应上一轮** (a later round): each red check of the earlier round — green now, or still
  red.
- **结论**: the verdict, the number of checks, the red ones (or none).
- **发现的问题和风险**: on a fail, each red check — its id, the unit or file it names and the
  decisive log lines — so the build's next round can act on it; the `flaky` and
  `pre_existing` units (a flaky `browser_crash` is a browser crash, never a flaky test of
  the change).
- **关键决定和理由**: why a red is the change's (or why you could not place it), and each
  owner message you acted on (its `at`, the words, your reading).
- **Evidence**: `artifacts/verify/result.json`, `artifacts/verify/report.md`.

## Never

Write or edit `artifacts/verify/result.json` or `runtime-state/asks.jsonl`; run any other
build, lint or test command, or re-run one check by hand; hand off `done` on a red you
could not place; end a turn that needs the owner without `pf step ask`; pass a Bash
timeout; run a command in the background and poll it, or give it a timeout or deadline of
your own.
