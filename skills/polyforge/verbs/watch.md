# watch — watch a work item's progress (`pf watch`)

Execution-plane verb: routed to the local daemon. One command for a snapshot,
one resident watcher per chat for progress and human-decision wake-ups.

## Triggers

- EN: how's it going / show progress / what step is it on / any events /
  is it asking anything / what's the latest / keep an eye on it
- 中文: 看看进度 / 跑到哪了 / 观察一下 / 到哪一步了 / 它在问我什么吗 / 最新状态 / 盯着

## Command

```
pf watch <id> --once [--json] [--after N] [--events N] [--demand]    # one snapshot
pf watch subscribe <id> | unsubscribe <id> | list          # this chat's subscriptions
pf watch --chat <chat> [--wait-decision] [--poll D] [--no-parent-check]
                                                           # resident watcher (default chat: $PF_CHAT_ID)
pf watch --chat <chat> --statusline                        # print the progress file
pf watch --statusline --harness-session <id|->             # same, chat resolved from the harness session
```

Chat id resolution (the explicit flags outrank the environment):
`--chat <id>` or `--harness-session <id|->` names the chat explicitly and wins
over `PF_CHAT_ID`; passing BOTH flags is a usage error (`--chat and
--harness-session are both explicit chat-id sources`); `PF_CHAT_ID` is used only
when neither flag is given.

`--once` is the pull, headed `work item:  polyforge#140 (wi_…)` (the slug,
the id beside it): status (`running`, `needs input`, `completed`, `failed`,
`blocked`, or `queued/preparing` for a work item the daemon
has not registered yet), the current step and how long it has run, the age of
the last work signal when known, the pending question's id and text, and the
latest events. `--json` is the daemon's raw watch snapshot. The state comes
from the capsule's `execution.json` (the authority), so it also answers for a
work item that finished while nobody was watching.

`--demand` marks the snapshot request as human-side demand for the
summarize lane (polyforge#224): the daemon may then keep the work item's
transcript summarized in the watch JSON (`summary` — one plain-text line,
narration never authority — `summary_error`, `summary_age_sec`,
`summary_capped`), model calls happening only while a human is looking.
The panel's poll sets it while it is open; the resident watcher never
does, and the flag without `--once` is a usage error.

The resident watcher, the Claude Code status line / wake loop and the pi
extension are described in the `pf watch` section of `SKILL.md`. `--poll D`
sets the resident watcher's poll interval (default 2s); above one minute the
daemon counts the chat as silent between polls, so a decision of a work item
this chat submitted that it has not received yet may wake another chat of the
owner instead (once — never both). A decision
wake-up line is a fixed template and carries no question or error text:
present it to the human, never answer it yourself, and read the question text
here (`--once`) only when asked. A `carrier_push_failed <repo>` line means the
work item's commits in that task repo could not be saved to aihub, so a
`pf resume` after a block may lose them; the git error (or the refusal reason)
is in the `--once` events, never in the line. A `human_gate_unasked` line means a
autopilot grill finished without asking the human anything; the work item kept
running — tell the human, who may check the grill's memo. Progress lines, wake-up lines and
`pf watch list` name the work item by its slug (`polyforge#140`); the id stands
in only when no slug is known. Quote a slug when you pass it back
(`pf watch 'polyforge#140' --once`) — `#` is a zsh glob character.

## Failure branches

| Code | When | What to tell the user |
|---|---|---|
| (no code) `watch: no chat id` | None of `--chat`, `--harness-session` or `PF_CHAT_ID` is set | The session has no chat id — see the Chat identity section of `SKILL.md`. |
| (no code) `watch: --chat and --harness-session are both explicit chat-id sources` | Both flags were passed | Pass exactly one of them. |
| (no code) `watch: chat id "…" must be 1-64 characters of [A-Za-z0-9_-]` | A malformed chat id (refused locally, nothing sent) | Fix the chat id. |
| `DAEMON-400` | Bad cursor | Fix the invocation. |
| `dial unix …` | Daemon not running | Nothing is executing here — run `pf daemon ensure`, then retry once (or check `--socket`). The resident watcher prints `daemon unreachable; progress paused` and keeps retrying. |

Progress is never a second authority: the step fields are a live direct read of
the capsule's execution record; the events are news, not state.
