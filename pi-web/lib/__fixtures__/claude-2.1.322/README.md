# claude 2.1.322 subagent recordings, an edit recording and permission-mode, initialize and hooks recordings

Verbatim stdout of the real `claude` CLI, `2.1.322 (Claude Code)` — the version on
this host's PATH when they were made — recorded on 2026-11-10 with model
`claude-haiku-5-5`, the way the 2.1.319 recordings were made: run directory
`/tmp/claude-rec-441/work`, a throwaway `CLAUDE_CONFIG_DIR` (`/tmp/claude-rec-441/config`)
whose `settings.json` was `{}` (no hooks, plugins of the user's, skills or MCP servers),
credentials from the recording environment's own `ANTHROPIC_*` variables. Not
hand-authored. `lib/claude-fixtures-322.test.mjs` pins them.

argv — exactly pi-web's (`buildClaudeArgv({ sessionId, resume: false, model:
"claude-haiku-5-5" })`, `--forward-subagent-text` included):

    claude -p --input-format stream-json --output-format stream-json --verbose \
      --include-partial-messages --forward-subagent-text --model claude-haiku-5-5 \
      --permission-prompts none --session-id <id>

stdin — pi-web's user line with its `uuid`
(`{"type":"user","uuid":"<uuid>","message":{"role":"user","content":"<text>"}}`) and,
in `relay/`, one control request, written by a small driver at the moments named
below; stdin stayed open until the end of each scenario. The driver's environment
dropped the recording host's own `CLAUDECODE`, every `CLAUDE_CODE_*` variable,
`CLAUDE_EFFORT` and `CLAUDE_PID` (the host runs inside claude itself; its
`CLAUDE_CODE_DISABLE_BACKGROUND_TASKS` would have switched background runs off and
made `background_tasks` answer "Background tasks are disabled in this session."). The
run directory held `f1.txt` … `f15.txt`, each naming the next file
(`f1 content: word-1-7. Next file: f2.txt`).

## `subagent-forward/` — the subagent's text with `--forward-subagent-text`

One user line: use the `Agent` tool once in the foreground (description `read f1`) to
read `f1.txt` and reply with its content. The subagent's prompt, thinking, `Read` call,
tool result **and its text reply** all stream with `parent_tool_use_id` = the `Agent`
call id (and `agent_id`), before the run's `task_updated` / `task_notification`, the
`Agent` tool result and the main reply. Without the flag claude 2.1.322 forwards only a
subagent's `tool_use` / `tool_result` frames, never its text or thinking (measured in
planning; in the 2.1.322 bundle the forwarding filter passes text only when
`forwardSubagentText` is set).

    lifecycle queued/started, init, Agent tool_use, task_started (is_backgrounded false),
    subagent: user prompt, thinking, task_progress, Read tool_use, tool_result, text,
    task_updated, task_notification (completed), Agent tool_result, main text, result

## `relay/` — a message relayed through the main session's `SendMessage`

#1 … #3 are the stdin user lines in order; #2 and #3 use pi-web's relay wording
(`请用 SendMessage 把下面这段话原样转给 subagent <agentId>（<描述>），不要做别的：` + a
newline + the text).

1. #1: use the `Agent` tool once in the foreground (description `chain`); its prompt
   reads `f1.txt` … `f15.txt`, one per tool call, following any coordinator message
   between reads; then reply DONE.
2. At the subagent's first `tool_use` frame (its `Read` of `f1.txt`) the driver wrote
   `{"type":"control_request","request_id":"bg-1","request":{"subtype":"background_tasks","tool_use_id":<the Agent call>}}`.
   claude answered `background_tasks_changed`, `task_updated {is_backgrounded: true}`,
   then `control_response {"backgrounded": true}`; the `Agent` call returned the async
   receipt ("Async agent launched successfully. …"), and the main turn ended (its
   `result`) while the subagent kept reading.
3. Right after `backgrounded: true` the driver wrote #2 (text `COORD-PING: when you read
   your next file, also say the word PONG in your final reply.`). It was `queued` while
   the first turn ran and `started` after its `result`. The main agent loaded
   `SendMessage` (`ToolSearch select:SendMessage`), called it with `to: <agentId>` and
   the text verbatim, and got `{"success":true,"message":"Message queued for delivery to
   <agentId> at its next tool round. If it finishes first, it is resumed with the
   message and notifies again, …"}` while the subagent was at `f6.txt`. The sidecar holds
   the text as a `queued_command` attachment (`origin: coordinator`, rendered as "The
   coordinator sent a message while you were working: …") after the `f7.txt` round; the
   subagent read on to `f15.txt` and its final reply starts with PONG. The queued message
   itself is no stdout frame. Then the run's `task_notification` (completed, the
   `Agent` call id) and claude's own follow-up turn.
4. After that turn's `result` (and 20 s with no further turn) the driver wrote #3 (text
   `Now read f1.txt once more and reply with its content only.`) to the finished agent.
   `SendMessage` answered `{"success":true,"message":"Resuming agent <short id>",
   "resumedAgentId":<agentId>, …}` at once. The resume's `task_started` and
   `task_notification` carry the **`SendMessage`** call id and the same `task_id`; its
   frames (a `Read`, its result, the text reply) carry the **original `Agent`** call id.
   The main turn ended before the resumed run did; its notification opened claude's
   follow-up turn. The sidecar is the same file, appended: a user entry "The coordinator
   sent a message while you were working: Now read f1.txt …" (`origin: coordinator`),
   then the new round and reply; the meta keeps the original `toolUseId`.

Against the planning runs: there the resume was `awaited` (`task_started`
`awaited: true`, the `SendMessage` result "Resumed agent. Its final report follows …"
inline). Here it was not: no `awaited` field, the result came back at once and the
main turn ended first. A first take of this scenario with five files (not kept) also
showed the other branch of claude's rule: the message was queued after the subagent's
last tool round, so claude resumed the agent with it after it finished — a
`task_started` carrying the **`Agent`** call id again, a new `run_id`, and a second
notification.

    lifecycle queued #1, started #1, init, Agent tool_use, task_started (is_backgrounded false),
    subagent Read f1 …, [control_request bg-1], background_tasks_changed, task_updated,
    control_response {backgrounded: true}, Agent tool_result (async receipt), lifecycle queued #2,
    subagent Read f2, f3, main text, result, lifecycle completed #1,
    lifecycle started #2, init, ToolSearch, subagent Read f4 … f6, SendMessage (queued), subagent Read f7,
    main text, result, lifecycle completed #2, subagent Read f8 … f15, subagent text (PONG …),
    task_updated (completed), task_notification (Agent call id), init, main text, result,
    lifecycle queued #3, started #3, init, SendMessage (resuming), task_started (SendMessage call id),
    subagent Read f1 (Agent call id), main text, result, lifecycle completed #3,
    subagent text, task_updated (completed), task_notification (SendMessage call id), init, main text, result

## `edit/` — claude's `structuredPatch` in an `Edit`, a `replace_all` `Edit` and a `Write` update

Recorded on 2026-11-12 with the same host `claude` 2.1.322, model `claude-haiku-5-5` and
pi-web's exact argv as above (`buildClaudeArgv({ sessionId, resume: false, model:
"claude-haiku-5-5" })`), by a small driver that wrote one user line to stdin and
closed stdin after the turn's `result`. The driver's environment dropped the host's
`CLAUDECODE`, every `CLAUDE_CODE_*` variable, `CLAUDE_EFFORT` and `CLAUDE_PID`, and
set a throwaway `CLAUDE_CONFIG_DIR` whose `settings.json` was
`{"permissions":{"allow":["Edit","Write"]}}` (so `--permission-prompts none` let the
edits through); credentials came from the recording environment's own `ANTHROPIC_*`
variables. The run directory held `notes.txt` (15 lines: `apple at the top`,
`line 2` … `line 7`, `the middle line`, `line 9` … `line 14`, `apple at the bottom`)
and `other.txt` (`old other text`, `second line`).

One user line: with the `Edit` tool change `the middle line` to `the MIDDLE line`;
with one `Edit` call using `replace_all` replace every `apple` with `pear`; with the
`Write` tool replace `other.txt` with `new other text` / `second line`; reply DONE.
claude first `Read` both files (an `Edit` or `Write` of an existing file needs a prior
read), then made the three calls in order. Each of the three results carries
`structuredPatch` — `[{oldStart, oldLines, newStart, newLines, lines[]}]` — in the
stream's `tool_use_result` and the transcript's `toolUseResult` alike: one hunk
(`-5,7 +5,7`) for the one-line `Edit`, two hunks (`-1,4 +1,4` and `-12,4 +12,4`) for
the `replace_all` `Edit`, and one hunk (`-1,2 +1,2`) for the `Write` update
(`type: "update"`). The `Read` results carry none.

    lifecycle queued/started, init, thinking, Read notes.txt, tool_result, Read other.txt, tool_result,
    Edit, tool_result, Edit (replace_all), tool_result, Write, tool_result, text DONE, result,
    lifecycle completed

Scrubbing: the run directory `/tmp/claude-edit-rec` was rewritten to
`/tmp/claude-rec-444-edit` (also in claude's encoded project directory name), the
config dir to `/tmp/claude-rec-444-config`, and the session id to the fixed
`44400000-0000-4000-8000-000000000444`; the home directory appeared nowhere. The
transcript keeps only its `user`, `assistant` and `system` entries (it had no
`system` entry); each kept entry's parent is the nearest kept ancestor (here every
parent was kept). The three files were searched for the recording environment's
`ANTHROPIC_*` and `CLAUDE_*` values, its `HOME`, `USER` and host name, the base URL's
host, `sk-ant`, `Bearer`, `/root` and the unscrubbed run directory: none remains, except
the transcript's `entrypoint: "sdk-cli"` and `effort: "medium"`, which are claude's own
field values that happen to equal two of the dropped host variables. No thinking
signature holds a `[wW][bB]<digits>` run, so none was reshaped.

## `permission-mode/` — `set_permission_mode` on a live child

Recorded on 2026-11-13 with the same host `claude` 2.1.322 (`claude --version`: `2.1.322
(Claude Code)`), at zero credit: stdin carried only control requests, never a user line,
so claude ran no turn (no `init`, no assistant frame, no `result`). Run directory
`/tmp/claude-rec-445/work`, a throwaway `CLAUDE_CONFIG_DIR` (`/tmp/claude-rec-445/config`)
whose `settings.json` was `{}`; the driver's environment dropped the host's
`CLAUDECODE`, every `CLAUDE_CODE_*` variable, `CLAUDE_EFFORT` and `CLAUDE_PID`. argv
(not pi-web's: no session, so no transcript, and the launch mode set so that the first
request changes nothing):

    claude -p --input-format stream-json --output-format stream-json --verbose \
      --no-session-persistence --permission-prompts none --permission-mode acceptEdits

stdin — four control requests, `{"type":"control_request","request_id":"pm-<n>",
"request":{"subtype":"set_permission_mode","mode":<mode>}}`, modes `acceptEdits`,
`plan`, `bogus`, `default` in that order; the driver wrote each one after the previous
one's `control_response` (and, for a mode that changed, its `status` frame) had arrived,
then closed stdin and claude exited 0.

    pm-1 acceptEdits (already the mode): control_response success {mode: "acceptEdits"}, no status frame
    pm-2 plan: control_response success {mode: "plan"}, then system status {status: null, permissionMode: "plan"}
    pm-3 bogus: control_response error, error_code "invalid_mode",
         "Cannot set permission mode: must be one of acceptEdits, auto, bypassPermissions, default, dontAsk, plan"
    pm-4 default: control_response success {mode: "default"}, then system status {status: null, permissionMode: "default"}

Scrubbing: none needed — the stream holds only the answers above, claude's random
session id and two frame uuids; it was searched for `ANTHROPIC_`, `sk-ant`, `/root/`,
`/home/` and `[wW][bB]<digits>`, and none occurs.

## `initialize/` — claude's answer to the host's `initialize`

Recorded on 2026-11-13 with the same host `claude` 2.1.322 (`claude --version`: `2.1.322
(Claude Code)`), at zero credit, exactly as Pi Web's probe asks (`lib/claude-probe.ts`):
stdin carried one control request and never a user line, so claude ran no turn (no `init`,
no assistant frame, no `result`). Run directory `/tmp/claude-rec-445/work`, the throwaway
`CLAUDE_CONFIG_DIR` of `permission-mode/` (`/tmp/claude-rec-445/config`, `settings.json`
`{}`); the driver's environment dropped the host's `CLAUDECODE`, every `CLAUDE_CODE_*`
variable, `CLAUDE_EFFORT` and `CLAUDE_PID`. argv (the probe's, no session):

    claude -p --input-format stream-json --output-format stream-json --verbose \
      --no-session-persistence --permission-prompts none

stdin — `{"type":"control_request","request_id":"init-1","request":{"subtype":"initialize"}}`;
the driver closed stdin once the answer had arrived and claude exited 0. The stream is that
one `control_response` (`success`, `init-1`). Its `response` holds, among claude's own
fields (`commands`, `agents`, output styles, `account`, `pid`, remote-control and fast-mode
state, `session_state`): `claude_code_version` `2.1.322`, `current_permission_mode`
`default`, `capabilities` `ui_surface_v1`, `mcp_call_result_not_for_model_v1`,
`mcp_call_check_permissions_v1`, and `models` — claude's aliases, each with the full name
it resolves to:

    default → claude-opus-5-5 (displayName "Default (recommended)")
    opus    → claude-opus-5-5
    fable   → claude-fable-5-2
    sonnet  → claude-sonnet-5-5
    haiku   → claude-haiku-5-5

Pi Web's model list is these aliases (`value`) plus Settings › Claude's custom names; an
alias's context window is the one pi's catalog has for its `resolvedModel`.

Scrubbing: `account.tokenSource` named the recording environment's credential variable;
it was rewritten to `"scrubbed"`. Nothing else was changed: the paths are the throwaway
`/tmp/claude-rec-445/…` ones, and the stream was searched for `ANTHROPIC_`, `sk-ant`,
`/root/`, `/home/`, e-mail addresses, the projection's identifier families and
`[wW][bB]<digits>` — none occurs.

## `hooks/` — hook activity with `--include-hook-events`

Recorded on 2026-11-13 with the same host `claude` 2.1.322 (`claude --version`: `2.1.322
(Claude Code)`), one haiku turn (owner-approved credit). argv — pi-web's
(`buildClaudeArgv({ sessionId, resume: false, model: "claude-haiku-5-5",
includeHookEvents: true })`):

    claude -p --input-format stream-json --output-format stream-json --verbose \
      --include-partial-messages --forward-subagent-text --model claude-haiku-5-5 \
      --include-hook-events --permission-prompts none --session-id <id>

Run directory `/tmp/claude-rec-445/hooks-work` holding `hello.txt` (`hello content:
word-445-7`); a throwaway `CLAUDE_CONFIG_DIR` (`/tmp/claude-rec-445/hooks-config`) whose
`settings.json` held only these command hooks:

    SessionStart              echo session-start-hook-ran
    PreToolUse  (matcher Read) echo pre-read-hook-ran
    PreToolUse  (matcher Read) echo failing-hook-says-no >&2; exit 1
    PostToolUse (matcher Read) echo post-read-hook-ran

The driver's environment dropped the host's `CLAUDECODE`, every `CLAUDE_CODE_*` variable,
`CLAUDE_EFFORT` and `CLAUDE_PID`; credentials came from the recording environment's own
`ANTHROPIC_*` variables. stdin — one pi-web user line (with its `uuid`): use the `Read`
tool once to read `hello.txt`, then reply with its content only; the driver closed stdin
after the turn's `result` and claude exited 0.

Each hook is a `hook_started` then a `hook_response` with the same `hook_id`
(`hook_name` `<event>:<matcher or source>`, `hook_event`, `output` / `stdout` / `stderr`,
`exit_code`, `outcome`); no `hook_progress` came for these short commands. The
`SessionStart` hook ran as claude started, before the user line's lifecycle frames and the
turn's `init`. The `exit 1` hook is `outcome: "error"`, `exit_code: 1`, its stderr in
`stderr` and `output`; a non-blocking failure, so the `Read` still ran.

    hook_started / hook_response SessionStart:startup (success, exit 0),
    lifecycle queued/started, init, status, Read tool_use,
    hook_started PreToolUse:Read ×2, hook_response PreToolUse:Read (success, exit 0),
    hook_response PreToolUse:Read (error, exit 1),
    hook_started / hook_response PostToolUse:Read (success, exit 0),
    tool_result, status, text "hello content: word-445-7", result, lifecycle completed

Scrubbing: claude's random session id was rewritten to the fixed
`44500000-0000-4000-8000-000000000445`. Nothing else was changed: the paths are the
throwaway `/tmp/claude-rec-445/…` ones (`init.apiKeySource` is claude's own `"none"`), and
both files were searched for the recording environment's `ANTHROPIC_*` values and the
base URL's host, `sk-ant`, `Bearer`, `/root/`, `/home/`, the user and host names, e-mail
addresses, the projection's identifier families and `[wW][bB]<digits>` — none occurs (the
turn had no thinking, so no signature).

## Files

| file | what it holds |
|---|---|
| `<name>/stdin.jsonl` | the exact lines written to stdin, in order |
| `<name>/stream.jsonl` | stream-json stdout, every event (token deltas included) |
| `relay/transcript.jsonl`, `edit/transcript.jsonl` | the main transcript claude wrote at `<config dir>/projects/<cwd>/<session id>.jsonl` |
| `relay/subagents/agent-aabe137615e4325e1.jsonl` | the agent's sidecar, both runs (the resume appended) |
| `relay/subagents/agent-aabe137615e4325e1.meta.json` | the sidecar meta: `toolUseId` = the `Agent` call, `requestShape: foreground`, `finished: true` |

Scrubbing: the run directory and config dir were neutral when recorded, so no path
was rewritten. The transcript and sidecar drop claude's `api-request`,
`api-request-shape`, `api-request-blob` (the full system prompt and request
parameters), `atis-latch`, `cost-state` and `tools-used` entries and every
`attachment` but `queued_command`; an entry whose parent was dropped is re-parented to
the nearest kept ancestor (also a `last-prompt` `leafUuid`), so the chain claude
resumes stays whole. Session ids are the fixed
`32200000-0000-4000-8000-00000000000{1,2}`. The files were searched for the recording
environment's `ANTHROPIC_*` values, `sk-ant`, `/root/`, `/home/`, the host name, e-mail
addresses and the projection's identifier families. Thinking `signature` fields are
the model's opaque signatures, and several carried a `[wW][bB]<digits>` run that the
projection's identifier invariant rejects (desensitize.md's REWRITE does not touch it).
In each such run only the first digit after the two letters was changed to `x`; in a
multi-digit run the later digits stay, which is enough to break the pattern. This was
done identically in every copy (`signature_delta`, the
assistant frame, the transcript and the sidecar). So those signatures are reshaped, not
as recorded, and they would likely no longer verify if replayed to the API. Nothing
reads them: Pi Web ignores signatures. `grep -rnE '[wW][bB][0-9]+'` over this directory
prints nothing.

Secret-scan respelling: claude's API message ids (`api_message_id`,
the same id as the assistant frame's `message.id`) and the stdin-line uuids that a
transcript's `turnAttributionKey` names are random, high-entropy values, and a secret
scanner reads such a value as a key when it sits under a key-like field name. Each
distinct value was replaced, in every file and field it occurs in, by a fixed
synthetic one — a message id by `msg_` and 24 digits, such a uuid by
`<session-id prefix>-0000-4000-8000-0010000000NN` — one-to-one across this directory,
`../claude-2.1.319/` and the supervise package's `testdata/claude-real-agent-call.jsonl`. So every
equality the recordings carry between ids (a message's frames and its transcript
entries, a stdin line and the lifecycle frames and transcript entries naming it)
holds exactly as recorded; nothing in Pi Web reads either value otherwise.
