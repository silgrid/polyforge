# watch — watch a work item's progress and the machine's decision feed (`pf watch`)

Execution-plane verb: routed to the local daemon. One command for a work
item's snapshot, one for the machine's decision feed, one to mark feed
entries seen. Nothing here is resident: there is no watcher to start, no
subscription and no wake-up line — human decisions are shown and handled in
Pi Web's Polyforge panel, which reads the same feed.

A snapshot targets one Cell (one per AIHub profile): `--profile`, else
`PF_PROFILE`, else the cwd's mapped Cell, else `default` — never chosen by
you. The decision feed covers every Cell of the Host; each entry names its
`profile`.

## Triggers

- EN: how's it going / show progress / what step is it on / any events /
  is it asking anything / what's the latest / keep an eye on it / what needs
  me / any decisions waiting
- 中文: 看看进度 / 跑到哪了 / 观察一下 / 到哪一步了 / 它在问我什么吗 / 最新状态 / 盯着 /
  有什么要我处理的 / 有哪些决策

## Command

```
pf watch <id> --once [--json] [--after N] [--events N] [--demand] [--artifact <relpath>]
                                     # one snapshot of a work item
pf watch decisions [--json]          # the machine's decision feed, newest first
pf watch dismiss <id>... | --all     # mark open feed entries seen (the human's request only)
```

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
The panel's poll sets it while it is open; nothing else does, and the
flag without `--once` is a usage error.

`--artifact <relpath>` (with `--once`) prints one capsule file,
`artifacts/<relpath>`, instead of the snapshot — from the local holder or
through the signed relay of a remote one; for a work item that is not
running it prints only the pointer.

## The decision feed: `pf watch decisions`

Every human decision of every Cell on this machine — and every background
task's terminal summary — is one feed entry (`GET /decisions`): its class
(`needs_input` — a step's `pf step ask` — with the ask's id, `blocked`, `failed`, `completed`,
`canceled`, `paused`, `terminal`, `step_dead`, `candidates_exhausted`,
`carrier_push_failed` with the repo, `submit_rejected`, `submit_dropped`,
`daemon_restarted`; a task entry is `completed`,
`failed`, `capped` or `interrupted` and carries the summary text), the work
item's slug or the task id, the Cell and a fixed phrase. An entry that needs
the human counts as **open** (`attention`, not yet resolved, not dismissed);
the top-level `attention` number is the open count Pi Web shows. The daemon
resolves entries itself: the step's accepted result (or a new ask) resolves
the open `needs_input`, a work item that runs again resolves its earlier
entries, a later status resolves the earlier `needs_input` / `blocked` /
`paused` ones, and a final one (completed, failed, canceled) the work
item's `carrier_push_failed` ones. The owner's reply naming the entry's ask marks it
`replied` (with `replied_at`): no longer open (`attention` false), still
unresolved until the step hands over; an interjection changes nothing. The feed keeps the
newest 300 entries and none older than 14 days.

Text mode prints one line per retained entry, newest first; `--json` (or the
global `--json`) prints `GET /decisions` verbatim. An entry carries no
question or error text: for a `needs_input` entry run `pf watch <wi> --once`
and show the question verbatim; never answer it yourself (`verbs/answer.md`
takes only the human's own words). A `carrier_push_failed` entry means the
work item's commits in that task repo could not be saved to aihub, so a
`pf resume` after a block may lose them; the git error (or the refusal reason)
is in the `--once` events. A task entry is no
decision to act on: present it to the owner and let the owner decide
(`pf task show <id>` has the full report).

## Marking entries seen: `pf watch dismiss`

`pf watch dismiss <id>...` (or `--all`) marks open feed entries dismissed —
the human has seen them; it changes nothing about the work item. Run it only
on the human's request: it removes the entry from the panel's open count. A
step or task process tree is refused (`DAEMON-403`).

The feed and `pf watch <id> --once` name the work item by its
slug (`polyforge#140`); the id stands in only when no slug is known. Quote a
slug when you pass it back (`pf watch 'polyforge#140' --once`) — `#` is a zsh
glob character.

When a feed entry or a snapshot presents a work item as completed, read its
delivered follow-ups before presenting the completion: run the Ingest and
Present steps in `verbs/followup.md` (the PR body's follow-up section or the
capsule's `artifacts/delivery.md`; if neither is reachable, say so and ask —
never assume the list is empty). The entry carries no count and no list,
so this lookup is the session's own step, not a fact the daemon already
checked.

## Failure branches

| Code | When | What to tell the user |
|---|---|---|
| `DAEMON-400` | Bad cursor | Fix the invocation. |
| `DAEMON-403` (`dismiss`) | Run from a step or task process tree | A step never marks the human's decisions seen; the human's session runs it. |
| `dial unix …` | Daemon not running | Nothing is executing here — run `pf daemon ensure`, then retry once (or check `--socket`). |

Progress is never a second authority: the step fields are a live direct read of
the capsule's execution record; the events are news, not state.
