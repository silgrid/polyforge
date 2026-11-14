# answer — speak to the current step's agent

Execution-plane verb: routed to the local daemon (the answer route). The
daemon hands the words to the work item's in-process Go driver; it never
rewrites them. `pf answer <id> --text "..."` goes to the current step's
agent at any time — a reply to its `pf step ask` question
(the one `pf watch --once` shows under the ask's stamp as its id) while it
waits, an interjection while it runs; the words are recorded in the capsule
first, then relayed verbatim into that agent's own conversation (the live
process, or the session resumed after the warm window), and the daemon reads
none of them — no question id, no option, no reserved word. To send the
work item back to an earlier step, to let a waivable gate's FAIL through, or
to abandon it, say so in your words: the step's agent reads them and hands
off `rewind --to <step>`, `waive` or `cancel` itself, citing your message
(there is no owner command for the first two). `pf cancel` (`cancel.md`)
abandons a work item directly.

A **parked** work item (blocked or paused) is answered too: the message wakes the step the park belongs to — its own
session after it handed off `blocked`, the step that timed out or kept
exiting without a hand-off, the current step after a pause, a replay of
failed hub writes. The command opens the work item's door with your own
key first (the one `pf resume` opens), then the daemon records the words
and claims the work item. Only a park no agent can start (no usable model,
a snapshot of another contract) is refused — then `pf resume` after the
fix, or `pf cancel`.

## Triggers

- EN: answer the question / reply to the step / here's my answer / tell
  it X / respond to what it's asking
- 中文: 回答问题 / 它在问我 / 答一下 / 回复它 / 告诉它……

## Command

```
pf answer <id> --text "..." [--by user]
```

`--text` is your words — required, verbatim (an owner's `/foo` reaches the
agent as words). `--by` is the answerer's user id recorded with the
message, defaulting to the config's `user_id` / `PF_USER_ID` (the daemon
does not cross-check it against the caller); the CLI refuses locally if no
identity is resolvable.

## Success

`reply to ask <at> recorded for step <s> (dispatch <n>, at <at>): delivered
to the step's agent` — or `… queued by the step process (a turn is
running); delivered at the turn's next boundary`, `… no step process is
alive; the daemon resumes the session to deliver it`, `… the step process
did not take it (<why>); the step's next dispatch carries it`; `message
recorded for step …` for an interjection (`--json`: `{status, at, kind,
ask_at?, step, spawn_n, delivered, disposition?, note?}`). The agent reads
the words in its next turn and goes on, or asks again with `pf step ask`; the
daemon never judges whether they answered the question. `pf watch <id>
--once` lists `owner_message` and `delivered` among the events. A woken
park prints `… recorded for step <s> (dispatch <n>, at <at>): the parked
work item is woken — <how it continues>`.

## Failure branches

| Code | When | What to tell the user |
|---|---|---|
| `DAEMON-409` (no driver handle) | The work item has no live driver handle (the workflow is between derivations) | Retry shortly, after the daemon re-derives the workflow. |
| `DAEMON-409` (no step agent) | No step agent can be talked to: the workflow is terminal, or no step is in progress under a dispatch | Nothing is running to talk to — check `pf watch <id> --once`. |
| `DAEMON-409` `WAKE_CANNOT_START` | The work item is parked and its agent cannot start (no usable model, a failed candidate composition, a lane snapshot of another contract); nothing was recorded | Relay the reason; fix it (e.g. the step's models in `.polyforge.yaml`), then `pf resume <id>` (`resume.md`), or `pf cancel <id>`. |
| (door refused) `answer: <id> is parked …; opening its door was refused` | aihub refused the resume door with your key (you are not the reporter, a maintainer, an admin or the machine's operator) | Nothing was recorded; ask someone who may resume it. |
| `DAEMON-409` (disabled Cell) | The Cell's execution plane is closed (the error names why) | Nothing was recorded; fix the Cell's cause first. |
| `DAEMON-400` (empty or oversize message) | The `--text` words are blank or over 64 KiB; nothing was recorded | Say it in words, at most 64 KiB (split a longer message). |
| `DAEMON-400` (a question id or an option) | The request named a `question_id` or an `option` — the fields of an earlier answer shape (an older client) | Answer with `--text` alone; the step's agent reads the words. |
| `DAEMON-403` (step process — the only 403) | The caller runs inside a step agent's process tree (polyforge#122: the daemon checks the UDS peer, its ancestors and its process group for the step markers) | Questions are a human's to answer — a step agent never answers; answer from your own session. |
| `DAEMON-404` | No local job for this work item | The work item is not running on this machine — answer from the machine that runs it. |
| (no code) `answer: --text is required` / `… identity required` | Local usage refusal | Fill in the required flags; no request left the machine. |
| `dial unix …` | Daemon not running | Run `pf daemon ensure`, then retry once (or check `--socket`). |

Every question comes from a step's own agent (`pf step ask`), and your
words continue that agent's conversation; the daemon itself asks nothing. A
parked work item asks nothing either, but your message wakes it (above);
`pf resume` is for the park no agent can start.
