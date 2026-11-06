# claude 2.1.319 turn-boundary recordings

Verbatim stdout of the real `claude` CLI, `2.1.319 (Claude Code)` — the version on
this host's PATH when they were made — recorded on 2026-11-08 with model
`claude-haiku-5-5`, the way `subagent/` under `claude-2.1.312` was made: each run in a
throwaway directory with a throwaway `CLAUDE_CONFIG_DIR` whose `settings.json` was
`{}` (no hooks, plugins of the user's, skills or MCP servers), credentials from the
recording environment's own `ANTHROPIC_*` variables. Not hand-authored.
`lib/claude-fixtures-319.test.mjs` pins them; the unit stub
(`lib/__fixtures__/claude-stub/claude.mjs`) replays them, stdin line by stdin line.

argv — exactly pi-web's (`buildClaudeArgv({ sessionId, resume: false, model:
"claude-haiku-5-5" })`):

    claude -p --input-format stream-json --output-format stream-json --verbose \
      --include-partial-messages --model claude-haiku-5-5 \
      --permission-prompts none --session-id <id>

stdin — pi-web's user line plus the `uuid` it now carries
(`{"type":"user","uuid":"<uuid>","message":{"role":"user","content":"<text>"}}`),
written by a small driver at the moments named below; stdin stayed open until the
end of each scenario. The driver's environment dropped the recording host's own
`CLAUDE_CODE_*` / `CLAUDECODE` variables (the host runs inside claude itself, and its
`CLAUDE_CODE_DISABLE_BACKGROUND_TASKS` would have switched background runs off).

## What claude says about turns (all four recordings)

- A user line with a `uuid` gets claude's own lifecycle frames:
  `{"type":"command_lifecycle","command_uuid":<uuid>,"state":"queued"|"started"|"completed"}`.
  `queued` arrives within milliseconds of the line; `started` when claude takes the
  message in — right before the turn's `init` when it opens a turn, or inside the
  running turn when it is absorbed; `completed` after the turn that carried it.
- Every turn opens with `system`/`init` and ends with one `result`; the `result` lists
  the messages it carried in `user_message_uuids` (also on `status` frames).
- `result_index` counts the process's results from 0. `queued_turn_count` is 0 in
  every result here — also in `steer-queued/`, where a message was waiting — so it is
  no boundary signal.
- A background run's `task_notification` absorbed into the running turn leaves no
  stdout trace; only the transcript's `queue-operation` `remove` with
  `reason: absorbed_mid_turn` shows it. Counting `init`s cannot tell it from a
  follow-up turn.
- The same frames come from claude 2.1.312 (a one-line probe on this host, 2026-11-08:
  `queued`, `started`, `init`, `result` with `user_message_uuids`, `completed`).

## The recordings

#1, #2 are the stdin user lines in order.

### `absorbed-notification/` — ① a background run's notification absorbed mid-turn, then a second message

Prompt #1: start `sleep 4; echo …` with Bash `run_in_background`, then a foreground
`sleep 15`, then answer. The background run finished during the foreground sleep;
its notification was absorbed (transcript: `queue-operation remove absorbed_mid_turn`
with the `<task-notification>` text, and a `queued_command` attachment). #2 was
written 4 s after the first `result`.

    lifecycle queued #1, lifecycle started #1, init,
    task_started (background local_bash), task_started (the foreground sleep, local_bash),
    task_notification (background run completed), task_notification (foreground),
    result (result_index 0, queued_turn_count 0, user_message_uuids [#1]), lifecycle completed #1,
    lifecycle queued #2, lifecycle started #2, init,
    result (result_index 1, queued_turn_count 0, user_message_uuids [#2]), lifecycle completed #2

Two `init`s, two `result`s: the absorbed notification opened no turn. (The bug: pi-web
counted the second `init` as the notification's follow-up and waited for a second
`result` that never came.)

### `steer-absorbed/` — ② a message written mid-turn, absorbed

Prompt #1: a foreground `sleep 12` then a one-sentence answer; #2 ("end your reply
with STEERED") was written 3 s after the tool call. claude took #2 in after the tool
result and the reply ends with STEERED. Transcript: #2 is a `queued_command`
attachment (`source_uuid` #2, no user entry of its own) and `queue-operation remove
absorbed_mid_turn`.

    lifecycle queued #1, lifecycle started #1, init, lifecycle queued #2,
    task_started (foreground local_bash), task_notification,
    lifecycle started #2, lifecycle completed #2,
    result (result_index 0, queued_turn_count 0, user_message_uuids [#1, #2]), lifecycle completed #1

### `steer-queued/` — ② a message written mid-turn, queued as the next turn

Prompt #1: a 40-line list without tools; #2 was written 1 s into the streamed reply.
No model request followed, so #2 waited and ran as its own turn (a user entry of its
own in the transcript).

    lifecycle queued #1, lifecycle started #1, init, lifecycle queued #2,
    result (result_index 0, queued_turn_count 0, user_message_uuids [#1]), lifecycle completed #1,
    lifecycle started #2, init,
    result (result_index 1, queued_turn_count 0, user_message_uuids [#2]), lifecycle completed #2

### `idle-interrupt/` — ③ an interrupt while claude is idle, a background agent running

Prompt #1: start a general-purpose agent with `run_in_background` (its task: `sleep
20` in Bash), then answer "launched". 1.5 s after the `result` — claude idle, the
agent's Bash running — the interrupt control request was written
(`{"type":"control_request","request_id":…,"request":{"subtype":"interrupt"}}`).

    lifecycle queued #1, lifecycle started #1, init, task_started (background local_agent),
    result (result_index 0, queued_turn_count 0, user_message_uuids [#1]), lifecycle completed #1,
    (the agent's own frames, parent_tool_use_id set; task_progress)
    task_updated (status killed), task_notification (status stopped), background_tasks_changed [],
    control_response {"still_queued": []}

claude answered the idle interrupt with nothing queued — and stopped the background
agent (its Bash call rejected, `[Request interrupted by user for tool use]`). No
`init` or `result` followed in the next 120 s. So Stop must not reach an idle claude:
an interrupt alone ends the user's background agents. (A background Bash run, in a
probe the same day, survived an idle interrupt.)

## Files

| file | what it holds |
|---|---|
| `<name>/stdin.jsonl` | the exact lines written to stdin, in order |
| `<name>/stream.jsonl` | stream-json stdout, every event (token deltas included) |
| `<name>/transcript.jsonl` | the main transcript claude wrote at `<config dir>/projects/<cwd>/<session id>.jsonl` |

Scrubbing: each run directory `/tmp/claude-rec-395-<scenario>-<random>` was rewritten
to `/tmp/claude-rec-395-<name>` (also in claude's encoded project directory names and
its task output paths), the config dir to `/tmp/claude-rec-395-config`. The
transcripts drop claude's `api-request`, `api-request-shape`, `api-request-blob`
(the full system prompt and request parameters), `atis-latch`, `cost-state` and
`tools-used` entries and every `attachment` but `queued_command`; an entry whose
parent was dropped is re-parented to the nearest kept ancestor (also a `last-prompt`
`leafUuid`), so the chain claude resumes stays whole. Session ids are the fixed
`39500000-0000-4000-8000-0000000000{01,21,22,03}`. The files were searched for the
recording environment's `ANTHROPIC_*` values, `sk-ant`, `/root/` and the projection's
identifier families: none remains. Thinking `signature` fields are the model's opaque
signatures and stay as recorded.

Secret-scan respelling: claude's API message ids (`api_message_id`,
the same id as the assistant frame's `message.id`) and the stdin-line uuids that a
transcript's `turnAttributionKey` names are random, high-entropy values, and a secret
scanner reads such a value as a key when it sits under a key-like field name. Each
distinct value was replaced, in every file and field it occurs in, by a fixed
synthetic one — a message id by `msg_` and 24 digits, such a uuid by
`<session-id prefix>-0000-4000-8000-0010000000NN` — one-to-one across this directory,
`../claude-2.1.322/` and the supervise package's `testdata/claude-real-agent-call.jsonl`. So every
equality the recordings carry between ids (a message's frames and its transcript
entries, a stdin line and the lifecycle frames and transcript entries naming it)
holds exactly as recorded; nothing in Pi Web reads either value otherwise.
