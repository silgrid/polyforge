---
name: sync
description: Merge origin's default branch into every task repo with one pre-written command; resolve a real conflict in place and continue the same command; ask the owner when a resolution is not yours to make.
commands: [sync.py]
---
# sync

One pre-written script merges origin's default branch into each task repo in place —
fast-forward when the branch has no commit of its own, else a merge commit — resolves the
conflicts the default branch's `.polyforge/integrate.conf` makes mechanical, and writes the
result file. A real conflict is left in progress for you to resolve; the same script,
continued, commits it. Nothing of yours lands outside the conflicted files. "The step that
makes the change" below is develop's `build`, micro's `fix`.

The three lines, each run exactly as written (no argument added or removed, no Bash
timeout) — the one the situation names:

| Situation | The line |
|---|---|
| every start and every re-run | `"$PF_PYTHON" "$PF_SHARED_DIR/scripts/sync.py" --json --out artifacts/sync/result.json` |
| a real conflict resolved in place, `git add`ed | `"$PF_PYTHON" "$PF_SHARED_DIR/scripts/sync.py" --continue --json --out artifacts/sync/result.json` |
| handing the work back (`rewind`, `cancel`) on the owner's word | `"$PF_PYTHON" "$PF_SHARED_DIR/scripts/sync.py" --abort --json --out artifacts/sync/result.json` |

The step after you checks the result file's `argv` against the first two lines: a result
written by any other line is a hand-off mismatch there.

## 开工前读什么、核对什么 — before you start: what you read and check

1. **The tree review judged.** Read the latest `review` section of `artifacts/handoff.md`:
   its header stamps each task repo's end commit — the heads review judged. Per task repo,
   `git -C repos/<name> rev-parse HEAD` equals that head.
2. **Or your own earlier merge.** When a 回退 section sent the lane back here from `merge`,
   read merge's section (why it refused) and your own earlier `artifacts/sync/result.json`:
   HEAD equal to that repo's `after` there passes too.
3. A HEAD that is none of these is a commit made outside the lane: `pf step ask` the owner —
   which repo, the shas, what probably moved it and what you suggest — then end your turn,
   and act on the reply: continue; or run this step again from the start (`rewind --to`
   this step); or send the work back to the step that makes the change (`rewind`); or
   `cancel` — a `rewind` to that step or a `cancel` only after `--abort` (below).

## Procedure

1. **Run the start line** (the table above) every time — it is idempotent.
2. **Route on the exit code** (the result's `details.repos[]`: `before`, `after`,
   `default_head`, `mechanical[]`, `conflicts[]`, `resolved[]`, `incoming[]`).
   - **0** — every repo is synced: hand off `done`.
   - **1** — a real conflict is in progress in the repos whose `conflicts[]` is not empty.
     Resolve it there, in place, then run the continue line (below). After it exits 0, hand
     off `done` (the result now lists the files under `resolved[]`); exits 1 again (a file
     still unresolved): resolve the rest and run the continue line once more.
   - **2** — not judged (no task repo, a fetch that failed, a branch or origin moved under
     the run, a linked worktree, work committed on top of the sync's record). Wait one
     minute and run the start line again, once; still 2: hand off `blocked` with the
     script's stderr tail in the body and what the machine needs as `--reason`.

## Resolving a real conflict

Keep **both** sides' intent: the default branch's changes stay (they are on the default
branch already), and this work item's change must still do what its goal and acceptance say
(`runtime-state/work-item.json`). Read the incoming commits that touch a conflicted file
(`incoming[]`, `git -C repos/<name> log --stat HEAD..MERGE_HEAD -- <file>`) before you
choose; adapt this change's lines to the default branch's (a renamed function, a moved
file, a new parameter), never the other way round. For each conflicted file: edit it until
no `<<<<<<<`, `|||||||` or `>>>>>>>` line is left, then `git -C repos/<name> add <file>`; a
file both sides deleted or renamed: `git rm` or `git add` the outcome you chose. You may
run a unit's test for your own confidence; nothing trusts it — the gate right after you
(develop's verification, micro's evidence) tests the resolved tree, and the PR body lists
every file you resolved. Then run the continue line (the table above) — it refuses a
residual marker, an unstaged or unmerged path, a mechanically resolved file changed and a
resolved text file that drops a line both parents (`HEAD` and `MERGE_HEAD`) carry (fix the
tree, run it again), commits each merge and rewrites the same result file. Refused for
dropped lines: put back the lines its note names; when dropping them is the resolution, that
is **Not yours to resolve** (below) — ask the owner.

**Not yours to resolve** — the default branch removed or rewrote what this change builds on,
so resolving the hunks would leave the acceptance unmet or the change pointless; or you are
not confident the resolution keeps the ticket's intent: `pf step ask` the owner with the
files, the hunks, the incoming commit and your reading, then end your turn. On the reply:
resolve as the owner says and continue, or hand the work item back.

**Handing it back — `rewind` or `cancel`.** Before either, run the abort line (the table
above). It aborts the merge in progress
(each repo back at its own HEAD, nothing reset), drops the sync's record and marks the
result `aborted`, so the step that makes the change resumes on a clean tree and the next
sync starts over from its new commits instead of treating them as a crash to undo. Then hand
off `--status rewind --to <build on develop, fix on micro> --per-owner <the reply's at>` (or
`--status cancel --per-owner <at>`).

## 交接要写什么 — what your hand-off carries

- **Status.** `done` on exit 0 (after a resolution: after the continue line exits 0);
  `blocked` on exit 2 after the retry; `rewind` or `cancel` only on the owner's word, after
  the abort line.
- **回应上一轮** (after merge's FAIL): what the forge refused, and what this merge of the
  default branch changed about it.
- **做了什么**: per repo — `before` → `after`, the default branch's commit merged in
  (`default_head`), the files resolved mechanically, the files you resolved.
- **关键决定和理由**: for each file you resolved, which side's change you adapted and why;
  each owner message you acted on (its `at`, the words, your reading).
- **给下游与未决**: the files you resolved, verbatim — no review judges them; the gate after
  you tests them and publish lists them for the owner.
- **Evidence**: `artifacts/sync/result.json`.

## Never

Write or edit `artifacts/sync/result.json` or `runtime-state/asks.jsonl`; commit, push,
reset, rebase, stash, cherry-pick, check out or `git merge` (`--abort`, `--continue` or a
new one) by hand — the script owns the merge; run any line but the table's; edit a file
the result lists under
`mechanical[]` (its resolution is the conf's rule), or any repo whose `conflicts[]` is
empty; hand off `rewind` to the step that makes the change, or `cancel`, without running
the abort line first; pass a Bash timeout; end a turn that needs the owner without
`pf step ask`; run a command in the background and poll it, or give it a timeout or
deadline of your own.
