---
name: merge
description: Merge the published PRs at the verified heads with one pre-written script and register the landing record it writes for the work item's wrap (pf step hub wrap-attrs integration); a PR that moved, closed or was refused is this gate's FAIL back to sync.
commands: [merge.py]
---
# merge

One pre-written script reads what publish opened (`artifacts/publish.json`) and the
verified heads — the result file of the lane's verifying gate: develop's `verification`
(`artifacts/verify/result.json`), micro's `evidence` (`artifacts/run/result.json`) —
judges every PR before anything merges and merges each open one with
`gh pr merge --merge --match-head-commit <verified head>`; a PR already merged is taken
only by the landing rules (its branch side the verified head, or a merge commit on the
default branch containing it). On a pass it also writes the landing record the work item
is wrapped with. You never merge by hand, and you never write the landing record yourself.

| Lane | The line, run exactly as written (no argument added or removed, no Bash timeout) |
|---|---|
| develop | `"$PF_PYTHON" "$PF_SHARED_DIR/scripts/merge.py" --publish artifacts/publish.json --verified artifacts/verify/result.json --landing-out artifacts/merge/landing.json --json --out artifacts/merge/result.json` |
| micro | `"$PF_PYTHON" "$PF_SHARED_DIR/scripts/merge.py" --publish artifacts/publish.json --verified artifacts/run/result.json --landing-out artifacts/merge/landing.json --json --out artifacts/merge/result.json` |

## 开工前读什么、核对什么 — before you start: what you read and check

1. **publish's hand-off against the verified heads.** Read publish's latest section and
   `artifacts/publish.json`. For every entry of `repos[]` without a `skipped_reason`: its
   `head` equals that repo's entry in the `heads` of the verified result file (waived or
   not, those are the heads the verifying gate ran over). A mismatch is this gate's FAIL:
   hand off `fail`, the body naming "publish hand-off mismatch: <repo>: published <sha> but
   verified <sha>" (the lane goes back to `sync`).
2. **The verified result file is the gate's line's.** Its `argv` is exactly the arguments
   of the line the verifying gate's skill writes out (develop's `verify`, micro's
   `evidence`: the words after the script's or command's own name, in order). A result
   written by any other line proves nothing: hand off `fail`, the body naming "verified
   result mismatch: argv <the result's argv>, the gate's line <its arguments>".
3. **A later round.** When a 回退 sent the lane back through `sync` after an earlier `merge`
   section, read that section: what the forge refused then.

## Procedure

1. **Reuse or run.** An `artifacts/merge/result.json` whose `round` equals `$PF_ROUND` and
   whose `verdict` is `pass`, with `artifacts/merge/landing.json` beside it, is this round's:
   go to 3. Otherwise 2 (a run that refused merges nothing; a re-run takes what landed by
   the landing rules).
2. **Run your lane's line** (the table above).
3. **Route on the exit code.**
   - **0** — every published PR is landed, and the script wrote the landing record
     (`artifacts/merge/landing.json`: `{state: landed, repos: [{repo, pr, head,
     light_verified_head, merge_commit, accepted?}], merge_commits}`). Register it for the
     wrap, exactly this line: `pf step hub wrap-attrs integration @artifacts/merge/landing.json`
     — the work item is wrapped with it as its `integration` attr, awaiting the integration
     lane's full gate; without it the work item never reaches that lane, so the step
     declares `wrap_attrs: [integration]` and `pf step handoff --status done` is refused
     until the line was accepted. Then, as the lane's
     last step, write the wrap summary (the step header's Memory section), then hand off
     `done`.
   - **1** — a PR moved (its head is not the verified one), closed, retargeted, merged
     elsewhere off the verified head, or the forge refused the merge: hand off `fail` (the
     lane goes back to `sync`, which merges the default branch again).
   - **2** — not judged (a file unreadable, a head missing, gh or git unreachable, the
     deadline). Wait one minute and run 2 again, at most twice more; still 2: hand off
     `blocked`, the result's `summary` in the body and what the forge or the machine needs
     as `--reason`.

## 交接要写什么 — what your hand-off carries

- **Status.** `done` on exit 0 after the `wrap-attrs` line was accepted; `fail` on a
  hand-off or verified result mismatch or exit 1; `blocked` on exit 2 after the retries.
- **回应上一轮** (a later round): what the forge refused in the earlier round, and whether
  it lands now.
- **结论**: each repo's PR and merge commit, or why the merge was refused.
- **发现的问题和风险**: on a fail, the result's `summary` and each refused repo's `why` —
  what `sync` must merge again.
- **关键决定和理由**: any merge made elsewhere that the landing rule took (`accepted: true`):
  nobody verified that merge itself.
- **Evidence**: `artifacts/merge/result.json`, and on a pass `artifacts/merge/landing.json`.

## Never

Run `gh pr merge`, `gh api`, `git merge`, `git push` or any history change yourself; write
or edit `artifacts/merge/result.json` or `artifacts/merge/landing.json`; register any
`wrap-attrs` but the line above; run any merge line but your lane's; resolve a conflict
with the default branch here (that is
`sync`'s); update the PR branch; pass a Bash timeout; run a command in the background and
poll it, or give it a timeout or deadline of your own; end a turn that needs the owner
without `pf step ask`.
