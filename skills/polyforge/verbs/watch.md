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
pf watch adopt <id>                                        # take over the wake-target role
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

When a wake-up or snapshot line presents a work item as completed, read the
work item's delivered PR body (its follow-up section) or its capsule's
`artifacts/delivery.md` before presenting the completion. If either carries a
follow-up list, present every unclaimed item to the human and mark it
unclaimed — present only, and leave deciding, claiming, filing, routing or
dismissing any follow-up to the human. The fixed line carries no count and no
list, so this lookup is the session's own step, not a fact the daemon already
checked. If neither source is reachable, say so and ask the human where to
read it; never assume the list is empty.

## Taking the wake-target role over: `pf watch adopt <id>`

When a work item's submitting (origin) chat has unsubscribed, or the work item
has none, its decision wake is scoped to the work item's own workspace: the
live, subscribed, non-muted chats tagged with that workspace are woken, and
each such decision line carries the hint 「可用 `pf watch adopt <id>` 接管」.
That hint is ONLY a hint: **adopt runs on an explicit user request, never
automatic** — never adopt on your own, however inviting the line reads; say
the option exists and let the user decide. When the user asks, `pf watch
adopt <id>` (the chat resolves the same way as every other watch verb) makes
this chat the work item's origin chat and inherits the delivery record —
nothing already delivered replays. Refusals are the daemon's: a chat tagged
with another workspace (or untagged) is refused naming the work item's
workspace (the tag comes from `pf chat init` — re-init from that workspace),
a chat is refused while the current wake target is still subscribed, and a
step process can never reach the route (DAEMON-403 by the guard).

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
