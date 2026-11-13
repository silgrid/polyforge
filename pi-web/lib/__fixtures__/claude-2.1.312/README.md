# claude 2.1.312 recordings

Verbatim stdout of the real `claude` CLI, `2.1.312 (Claude Code)`, recorded on
2026-11-01 with model alias `haiku`, each run in a throwaway directory with a
throwaway `CLAUDE_CONFIG_DIR` (its `settings.json` named below), one user
message written to stdin as stream-json and stdin then closed. Not
hand-authored. `lib/claude-adapter.test.mjs` and `lib/claude-session.test.mjs`
read them.

Common argv: `-p --input-format stream-json --output-format stream-json
--verbose --include-partial-messages --model haiku --session-id <uuid>`, plus
the preset flags of `lib/claude-argv.ts`.

| file | preset flags | config dir settings.json | what it shows |
|---|---|---|---|
| `stream.jsonl` | read-only (`--bare --strict-mcp-config --disable-slash-commands --permission-mode dontAsk --permission-prompts none --tools Read,Glob,Grep --allowedTools Read,Glob,Grep`) | `{}` | init, token deltas (thinking, text, `input_json_delta`), one `Read` tool_use and its tool_result, the `result` aggregate |
| `transcript.jsonl` | same run as `stream.jsonl` | — | the on-disk transcript claude wrote at `<config dir>/projects/<encoded cwd>/<session id>.jsonl` |
| `read-only-deny.jsonl` | read-only, as above | `{}` | asked to create `out.txt`: no write tool exists, the file was absent afterwards (`ls -A` of the run directory printed nothing), and the reply says it cannot write; `permission_denials` is `[]` because nothing was attempted |
| `writable-bash-deny.jsonl` | writable (`--permission-mode acceptEdits --permission-prompts none --tools Read,Glob,Grep,Edit,Write,Bash --allowedTools Read,Glob,Grep,Edit,Write`) | `{"permissions":{"allow":["Bash(echo:*)"]}}` | `echo allowed-ok` (allow-listed) ran; a `python3 -c …` write was denied automatically: an `is_error` tool_result naming the denial, a `system`/`permission_denied` event, and the entry in the result's `permission_denials`; `made.txt` was absent afterwards |

Observed while recording, kept as facts rather than worked around:

- Under `--bare`, claude 2.1.312 offers only `Read` of `Read,Glob,Grep`
  (init `tools: ["Read"]`); without `--bare` the same `--tools` list offers
  all six writable tools. The read-only argv is unchanged (it is the spec's).
- Under the read-only preset a write cannot even be attempted, so a real
  read-only run produces no `permission_denials`; the denial path is
  recorded from the writable preset's Bash allow list instead.
- `acceptEdits` also auto-accepts plain file commands such as `touch` in the
  cwd (`reason_type: subcommandResults`); the allow list confines the rest.
- The `assistant` event of a content block arrives before that block's
  `content_block_stop`; one API message yields one `assistant` event per
  content block, all with the same message id.
- The streamed per-message `usage.output_tokens` (2 on the final message of
  `stream.jsonl`) under-counts the `result` aggregate (283).

Scrubbing: the run directory `/tmp/claude-rec-<random>` was rewritten to
`/tmp/claude-rec` (also inside claude's encoded project directory names and one
split `input_json_delta` chunk); the transcript's `api-request`,
`api-request-shape` and `api-request-blob` entries were dropped (they echo the
full system prompt and request parameters, which pi-web never reads). No
credential, token or base URL appears in any file (checked by searching for the
recording environment's values, which are not reproduced here). Thinking
`signature` fields are the model's opaque signatures and stay as recorded,
except one base64 run in `read-only-deny.jsonl`'s signature (both copies), where
one character was changed for the public baseline: pi-web never decodes or
verifies a signature, and the recorded bytes held a run the public mirror's
identifier check refuses.

## `image/`

A second recording, made on 2026-11-02 with the same `2.1.312 (Claude Code)` and
the argv pi-web launches today: exactly `buildClaudeArgv({ sessionId, resume:
false, model: "claude-haiku-5-5" })` (`-p --input-format stream-json
--output-format stream-json --verbose --include-partial-messages --model
claude-haiku-5-5 --permission-prompts none --session-id <uuid>`), env
`buildClaudeEnv(process.env)` (the inherited `CLAUDE_CONFIG_DIR`; no dedicated
config dir), cwd a throwaway directory, one user line on stdin and stdin then
closed. Not hand-authored. `lib/claude-adapter.test.mjs` reads it, and the e2e
claude stub replays `stream.jsonl` in its `image` mode.

| file | what it shows |
|---|---|
| `image/stdin.jsonl` | the exact user line written to stdin: one `text` block and one `image` block (`source: {type: "base64", media_type: "image/png", data}`, a 16×16 PNG) |
| `image/stream.jsonl` | init, the streamed thinking + text reply naming the image's colours (claude's own evidence it received the image), the `result` aggregate |
| `image/transcript.jsonl` | the on-disk transcript: the `promptSource: "sdk"` user entry whose content keeps the image block with inline base64 + `media_type`, claude's hidden `isMeta: true` `[Image: source: /tmp/claude-<uid>/…/images/1.png]` companion, and the reply |

Scrubbing: the run directory `/tmp/claude-img-rec-<random>` was rewritten to
`/tmp/claude-img-rec` (also inside the encoded project directory name), the
session id to the fixed `27000000-0000-4000-8000-000000000270`, and the
transcript's `api-request`, `api-request-shape` and `api-request-blob` entries
were dropped. The three files were searched for the recording environment's
`ANTHROPIC_*`, `CLAUDE_*` and `HOME` values and for `sk-ant`: no credential,
token, username, hostname or base URL remains (the only matches were the generic
`sdk-cli` entrypoint and `medium` effort labels claude writes itself).

## `model-switch/`, `effort/`, `compact/`

Three more recordings, made on 2026-11-02 with the same `2.1.312 (Claude Code)`,
each in a throwaway cwd, with pi-web's argv (`buildClaudeArgv()`) and env
(`buildClaudeEnv(process.env)`, the inherited `CLAUDE_CONFIG_DIR`; no dedicated
config dir). The recording shell itself ran inside an agent harness, so the
harness's own session variables (`CLAUDE_EFFORT`, `CLAUDE_CODE_SESSION_ID`,
`CLAUDE_CODE_CHILD_SESSION`, `CLAUDE_PID`, `CLAUDE_CODE_SESSION_ATTENDED`,
`CLAUDE_CODE_ENTRYPOINT`, `CLAUDE_CODE_EXECPATH`, `CLAUDE_CODE_SIMPLE`,
`CLAUDE_CODE_STARTUP_FAILURE_RESULTS`) were left out of the child's env as well as
`CLAUDECODE`; a pi-web server does not carry them. Each stdin line was written
after the previous one was answered (its `result`, or the `control_response`
with its `request_id`), then stdin was closed. Not hand-authored.
`lib/claude-fixtures.test.mjs`, `lib/claude-adapter.test.mjs` and
`lib/claude-session.test.mjs` read them; both claude stubs replay them.

| recording | argv beyond the stream flags | stdin | what it proves |
|---|---|---|---|
| `model-switch/` | `--model claude-opus-5-5 --permission-prompts none --session-id <uuid>` | a turn; `control_request` `{subtype: "set_model", model: "claude-bogus-0-0"}`; `{subtype: "set_model", model: "claude-sonnet-5-5"}`; a turn | the bogus model is refused, `control_response` `{subtype: "error", error: "API error: 502 Upstream request failed · model not changed", error_code: "check_failed", model: "claude-opus-5-5"}` (claude's own text, the old model kept); the full name is accepted, `{subtype: "success", response: {model: "claude-sonnet-5-5"}}`; the next turn's `init.model` and `assistant` `message.model` are `claude-sonnet-5-5` with the same session id and no new process |
| `effort/` | `--model claude-haiku-5-5 --permission-prompts none --effort low --session-id <uuid>` (plus `--debug-file`, a probe only, never product argv) | a turn; `control_request` `{subtype: "apply_flag_settings", settings: {effortLevel: "max"}}`; a turn | `apply_flag_settings` answers `{subtype: "success"}` without a model; `effort/runtime-probe.jsonl` holds the two API requests' metadata only: `params.output_config.effort` is `low` for the first turn and `max` for the turn after the request, and the transcript's `assistant` entries carry `effort`/`perTurnEffort` `low` then `max` |
| `compact/` | `--model claude-haiku-5-5 --permission-prompts none --session-id <uuid>` | two short turns; the user line `{type: "user", message: {role: "user", content: "/compact"}}` | claude's own compaction: `system` `status: "compacting"`, `status: null, compact_result: "success"`, a fresh `init`, `system` `compact_boundary` with `compact_metadata.pre_tokens` (16739) and `post_tokens`, two `isReplay` user events (the summary, `<local-command-stdout>Compacted </local-command-stdout>`), and a zero-usage `result` that ends the turn |

The `--effort` row: a non-default level is passed as `--effort <level>` after
`--model`; the 「默认」 launch (`model-switch/`, `compact/`) passes no `--effort`.

Effort channel (the probe's conclusion): **proven in place.** `apply_flag_settings
{effortLevel}` on a live child changes the next API request's
`output_config.effort`, so a live effort change sends that request and keeps the
child; no resume is needed. The 「默认」 entry has no recorded in-place value, so
picking it on a live child ends the idle child and the next spawn passes no
`--effort`. No model rejected a level in these recordings, so no level is hidden.

Model-switch side effects claude writes to its transcript: an `isMeta`
`<local-command-caveat>` entry, a `<command-name>/model</command-name>…` entry and a
`<local-command-stdout>Set model to …</local-command-stdout>` entry (and the latter
as an `isReplay` stream `user` event). They are claude's own bookkeeping of the
switch, not the user's words.

Compact marker shape (the transcript, `compact/transcript.jsonl`): one entry
`{type: "system", subtype: "compact_boundary", content: "Conversation compacted",
compactMetadata: {trigger: "manual", preTokens, postTokens, …}}`, followed by the
summary as a `user` entry with `isCompactSummary: true` and
`isVisibleInTranscriptOnly: true`, then the `<local-command-caveat>` (`isMeta`),
`<command-name>/compact</command-name>…` and `<local-command-stdout>Compacted
</local-command-stdout>` entries. Every pre-compaction entry stays in the file.
pi-web maps the boundary to its compaction card with `preTokens` and shows neither
the summary nor the bookkeeping entries.

Scrubbing: the run directories `/tmp/claude-{switch,effort,compact}-rec-<random>`
were rewritten to `/tmp/claude-switch-rec`, `/tmp/claude-effort-rec` and
`/tmp/claude-compact-rec` (also inside encoded project directory names), the
session ids to `27200000-0000-4000-8000-00000000000{1,2,3}`, the inherited config
dir to `/tmp/claude-config` and the home directory to `/home/user`. Of the
transcripts only the `user`, `assistant` and `system` entries and the `model`
attachment were kept: `api-request*`, `prompt_snapshot` and the other attachments,
`queue-operation`, `last-prompt`, `atis-latch` and `cost-state` entries were dropped
(they echo the system prompt, the request parameters and the user's own
config); `effort/runtime-probe.jsonl` keeps only each API request's `model`,
`max_tokens`, `thinking` and `output_config`. The files were then searched for the
recording environment's `ANTHROPIC_*`, `CLAUDE_*`, `HOME`, `USER` and hostname
values and for `sk-ant`, `Bearer` and the home path: no credential, token,
username, hostname or base URL remains (the generic `sdk-cli` entrypoint and
claude's effort words were not counted as findings).

## `language/`

One more recording, made on 2026-11-03 with the same `2.1.312 (Claude Code)`: the
argv of a 中文 Agent language session, exactly `buildClaudeArgv({ sessionId,
resume: false, model: "claude-haiku-5-5", agentLanguage: "zh-CN" })`:

```
-p --input-format stream-json --output-format stream-json --verbose --include-partial-messages
--model claude-haiku-5-5
--append-system-prompt "Use Simplified Chinese (简体中文) for replies, narration and reports. Keep code, code comments, commit messages, files you write, technical terms and identifiers as they are, or as the project's conventions require. Thinking in Chinese is best effort only."
--permission-prompts none --session-id <uuid>
```

env `buildClaudeEnv(process.env)` without the recording harness's own session
variables (as for `model-switch/` above, plus `CLAUDE_CODE_DISABLE_BACKGROUND_TASKS`
and `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC`), with a throwaway
`CLAUDE_CONFIG_DIR`; cwd a throwaway directory; one user line on stdin, written in
English on purpose so the reply's language comes from the instruction, not the
prompt; stdin closed after the `result`. Not hand-authored.
`lib/claude-fixtures.test.mjs` reads it.

| file | what it shows |
|---|---|
| `language/stdin.jsonl` | the exact user line: `In two short sentences, what does \`git rebase\` do?` (English) |
| `language/stream.jsonl` | init, the streamed thinking and text reply, the `result` aggregate: the reply is Simplified Chinese (`\`git rebase\` 会把当前分支的提交…`) with `git rebase` kept as written |
| `language/transcript.jsonl` | the on-disk transcript's `user` entry and the two `assistant` entries (thinking, then the same Simplified Chinese text) |

Before scrubbing, claude's own `api-request-shape` entry held the instruction
byte-for-byte inside the request's `system` blocks, the evidence that
`--append-system-prompt` reached the system prompt rather than the conversation;
that entry was then dropped with the others below. The thinking is
claude's redacted block (an empty `thinking` with its `signature`); no claim is made
about the language of thinking.

Scrubbing: the run directory `/tmp/claude-lang-rec-<random>` was rewritten to
`/tmp/claude-lang-rec` (also inside the encoded project directory name), the
throwaway config dir to `/tmp/claude-config`, the session id to
`29300000-0000-4000-8000-000000000293` and the home directory to `/home/user`. Of the
transcript only the `user` and `assistant` entries were kept: `api-request*`,
`queue-operation`, `attachment`, `last-prompt`, `atis-latch` and `cost-state`
entries were dropped. The three files were searched for the recording environment's
`ANTHROPIC_*`, `CLAUDE_*`, `HOME`, `USER` and hostname values and for `sk-ant`,
`Bearer` and the home path: no credential, token, username, hostname or base URL
remains (the generic `sdk-cli` entrypoint and `medium` effort labels claude writes
itself were not counted as findings).
