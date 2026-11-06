---
name: micro-bounds
description: Judge the micro change's bounds with one pre-written command before handing off - in bounds, hand off done; out of bounds, ask the owner to re-file the work on develop and hand off cancel on the owner's word.
commands: [facts.py]
---
# micro-bounds

One pre-written command judges every touched repository's diff (the merge-base with the
fetched default branch to HEAD, both sides of a rename or deletion) against the
`micro_path=` lines of that repository's `.polyforge/verify-scope.conf` **at the diff
base**, and lists per repository the commands the delivery re-runs: your plan's lines
plus every declared `micro_check` the plan omitted. Its `verdict` is the exit code's
alone; you never write its result file.

## 开工前读什么、核对什么 — before you start: what you read and check

1. **The ticket.** The opening of `artifacts/handoff.md` and `runtime-state/work-item.json`
   (its goal and content: the whole specification).
2. **A later round.** When `artifacts/handoff.md` carries an earlier `fix` section, read the
   回退 that sent the lane back here and the section of the gate that FAILed — review's
   findings (`artifacts/review.md`), or evidence's red line (`artifacts/run/result.json`) —
   and every owner message quoted in those headers. Each point gets an answer in your
   回应上一轮.
3. **Before you run the line**: every change is committed, and `artifacts/fix/evidence.json`
   is the plan the step definition describes (`[{"repo", "line"}]`, every command of
   `verify-scope --micro --json` per changed repo, in order).

## Procedure

1. **Run this line as written** — no argument added or removed, no Bash timeout:
   `"$PF_PYTHON" "$PF_SHARED_DIR/scripts/facts.py" micro-bounds --plan artifacts/fix/evidence.json --json --out artifacts/facts/micro-bounds.json`
2. **Route on the exit code** (the result's `details`: `in_bounds`, `reason`,
   `repos[{name, base, changed_paths, declared_paths, commands}]`).
   - **0** — in bounds. Every `commands` entry of every repo must be a line of your plan
     for that repo: one that is not (a declared `micro_check` you left out) goes into
     `artifacts/fix/evidence.json` in the engine's order, then run 1 again. Then hand off
     `done`.
   - **1** — out of bounds: `reason` names every refusal (a changed path outside the
     declaration, an agent-contract file, a touched repository that declares no
     `micro_path`, nothing to re-run). The change is not this lane's. Never widen a
     declaration, never edit `.polyforge/verify-scope.conf`, never hand off `done`.
     `pf step ask` the owner — the refused repos and paths verbatim from `reason`, why the
     ticket needs them, and the suggestion: re-file the work on the `develop` lane, this
     work item ending by your `cancel` (or name a narrower change that stays inside the
     declaration) — then end your turn. On the reply:
     - the owner agrees to re-file (any words that say so): hand off
       `--status cancel --per-owner <the reply's at>` (your reading: the owner re-files on
       develop);
     - the owner names a narrower change: revert what lies outside (a new commit), run the
       evidence again, rewrite the plan and run 1 again;
     - anything else (a question, a doubt): answer it and `pf step ask` again.
   - **2** — not judged (the plan unreadable, a repository whose default branch, base or
     declaration cannot be read). Fix the plan when it is the plan's fault and run 1
     again; otherwise wait one minute and run it once more; still 2: hand off `blocked`,
     the result's `summary` in the body and what the repository or the machine needs as
     `--reason`.

## 交接要写什么 — what your hand-off carries

- **Status.** `done` only when the last run of the line exited 0; `cancel` on the owner's
  agreement to re-file (`--per-owner`); `blocked` on exit 2, or on micro evidence you could
  not make green.
- **回应上一轮** (a later round): each point of review's findings or evidence's red line,
  and the commit that answers it.
- **做了什么**: every commit you made (sha and message); the changed paths
  (`<repo>/<path>`, the result's `changed_paths`); the micro evidence you ran
  (`verify-scope --micro --run`) and its exit.
- **关键决定和理由**: each owner message you acted on (its `at`, the words, your reading —
  a narrower change the owner approved is the one review judges).
- **验证**: the micro evidence green, and the micro-bounds verdict.
- **给下游与未决**: an `owner-review:` line when the ticket asks for one.
- **Evidence**: `artifacts/fix/evidence.json`, `artifacts/facts/micro-bounds.json`.

## Never

Write or edit `artifacts/facts/micro-bounds.json` or `runtime-state/asks.jsonl`; hand off
`done` while the last run's exit code is not 0; edit a `.polyforge/verify-scope.conf`; hand
off `cancel` without an owner message that agrees to it; end a turn that needs the owner
without `pf step ask`; pass a Bash timeout; run a command in the background and poll it, or
give it a timeout or deadline of your own.
