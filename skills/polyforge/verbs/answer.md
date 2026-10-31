# answer — answer a pending needs_input question

Execution-plane verb: routed to the local daemon (the answer route). The
daemon hands the payload to the work item's in-process Go driver; it never
rewrites the answer. One answer is reserved: `cancel` (the whole answer, ignoring
case and surrounding whitespace — every question states it) abandons the work
item. The daemon first probes the hub's cancel door (`dry_run`) with its own
credential, so the hub's `pf cancel` gate decides (reporter, project maintainer or
admin — never the self-asserted `--by`); a refusal is returned and the question
stays pending. An authorized cancel parks the workflow, ends the session blocked
and cancels the work item through the hub's blocked→canceled door, with your
verbatim answer on the cancel event; no memory wrap is written.

## Triggers

- EN: answer the question / reply to needs_input / here's my answer / tell
  it X / respond to what it's asking
- 中文: 回答问题 / 它在问我 / 答一下 / 回复它 / 告诉它……

## Command

```
pf answer <id> --text "..." [--question q_xxx] [--by user]
```

`--question` is the `question_id` the `pf watch --once` output showed; it may be
omitted when exactly one question is pending (the default). With ZERO or
SEVERAL pending questions a no-id answer is refused typed (`DAEMON-400`)
and nothing is delivered — with several, the error names the open
questions: pick one and pass its id via `--question` (a guess would
deliver the answer to an arbitrary question). `--text` is the
natural-language answer — required. Without `--question` the daemon answers
the step's currently pending question; there is no idempotent replay (see
Success). `--by` is the answerer's user id recorded as `answered_by`, defaulting to the config's
`user_id` / `PF_USER_ID` (the daemon does not cross-check it against the
caller); the CLI refuses locally if no identity is resolvable.

## Success

`answer delivered for question <q_…>` (`--json`: the daemon's raw answer,
`{"status":"accepted","question_id":"q_…"}`): the driver accepted the answer
for its pending question and the parked step resumes — `pf watch` to follow
it land. An answer is applied once: a second answer to a question that was
already answered is refused with `DAEMON-409` — it is never re-applied and
does not return the first submission's outcome. Run `pf watch <id> --once` to
confirm the first answer landed.

## Failure branches

| Code | When | What to tell the user |
|---|---|---|
| `DAEMON-409` (driver refused) | The driver could not take the answer: the `question_id` does not match the waiting step's persisted question, was already consumed, or another delivery of the same answer is in flight | The question is stale or mistyped — run `pf watch <id> --once` again, take the currently printed `question_id`, and answer that one; for an in-flight duplicate, simply retry shortly. Nothing was lost. |
| `DAEMON-409` (no driver handle) | The work item has no live driver handle (the workflow is between derivations) | Retry shortly, after the daemon re-derives the workflow. |
| `DAEMON-400` | No `--question` was given and the work item has NO pending question | Nothing is waiting for an answer — check `pf watch <id> --once`; do not retry blindly. |
| `DAEMON-400` (several open questions) | No `--question` was given and the work item has TWO OR MORE pending questions (the error names them) | The daemon refuses to guess which one you meant — run `pf watch <id> --once`, pick the question you intend, and answer with `--question <id>`. |
| `DAEMON-403` (step process — the only 403) | The caller runs inside a step agent's process tree (polyforge#122: the daemon checks the UDS peer, its ancestors and its process group for the step markers) | Questions are a human's to answer — a step agent never answers; answer from your own session. |
| `DAEMON-404` | No local job for this work item | The work item is not running on this machine — answer from the machine that runs it. |
| (no code) `answer: --text is required` / `… identity required` | Local usage refusal | Fill in the required flags; no request left the machine. |
| `dial unix …` | Daemon not running | Run `pf daemon ensure`, then retry once (or check `--socket`). |

Answers bind per `question_id`: if the step re-asks, it mints a **new**
question id — never reuse an old answer command; read the new one with
`pf watch --once` and answer that question.
