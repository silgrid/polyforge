# claude 2.1.312 background-subagent recording

Verbatim stdout of the real `claude` CLI, `2.1.312 (Claude Code)`, recorded on
2026-11-02 with model `claude-haiku-5-5`, in a throwaway directory with a
throwaway `CLAUDE_CONFIG_DIR` whose `settings.json` held only the recording
environment's `env` block (credentials); it contained no hooks, plugins, skills
or MCP servers. The user message was written to stdin as stream-json and stdin
then closed. Not hand-authored.

pi-web's argv at the time of recording (the reduced set defines;
#250 was merged into the base, not into `main` at authoring time):

    claude -p --input-format stream-json --output-format stream-json --verbose \
      --include-partial-messages --model claude-haiku-5-5 \
      --permission-prompts none --session-id 44444444-4444-4444-8444-444444444444

Run directory: `/tmp/claude-rec-253/work` with `note.txt` holding
`SUBAGENT_PROBE_TOKEN_253` on its first line. The prompt asked for the `Agent`
tool once, told the subagent to read `note.txt` and report its first line, and
told the main turn to answer "The agent was launched." and end. claude
backgrounded the agent (`task_started` `is_backgrounded: true`), returned the
async launch receipt, then the subagent's own `Read` and text arrived with
`parent_tool_use_id` set, followed by `task_updated`, `task_notification`, a
claude-initiated follow-up turn, and two `result` events.

| file | what it holds |
|---|---|
| `stream.jsonl` | stream-json stdout: `init`, the `Agent` tool_use, `task_started`/`task_progress`/`task_updated`/`task_notification`, the subagent's `Read`/result/text events (`parent_tool_use_id` set), the follow-up `init` and reply, both `result` aggregates |
| `transcript.jsonl` | the main transcript claude wrote at `<config dir>/projects/<cwd>/<session id>.jsonl`: the user turn, the `Agent` call, its async launch receipt (`toolUseResult.agentId`), the main reply, then the hidden `task-notification` anchor (with `turnOrigin: task_notification`) and the follow-up reply |
| `subagents/agent-a9fb2ff3aa8e8ff40.jsonl` | the sidecar claude wrote for that agent: its prompt, `Read` tool call, result and text |
| `subagents/agent-a9fb2ff3aa8e8ff40.meta.json` | the sidecar meta: `toolUseId` naming the parent `Agent` call, `agentType: general-purpose`, `spawnDepth: 1`, `finished: true` |

Observations kept as facts, not worked around:

- claude emitted both `result` events after the subagent had finished: the
  parent turn's first answer was streamed earlier, but its turn-end aggregate
  arrived only at the end of the whole process. A single recorded session
  therefore cannot show the parent turn already folded while the subagent still
  runs; that fold-exception state is a document-only scenario in
  `artifacts/e2e-scenarios.md`, flagged as a spec under-specification.
- The `Agent` tool_use is named `Agent`; the init `tools` list does not include
  it (the recording kept the user's own tool set because the reduced argv names
  no `--tools`).

Scrubbing: `/tmp/cc253-rec` was rewritten to `/tmp/claude-rec-253` (cwd, prompt
path, output-file path). `stream_event` token-delta lines were dropped from
`stream.jsonl`; every other recorded event is verbatim. `transcript.jsonl` and
the sidecar keep only `user`/`assistant` conversation entries; claude's
`attachment`, `api-request*`, `queue-operation`, `atis-latch`, `last-prompt`,
`tools-used` and `cost-state` entries were dropped (pi-web never reads them).
No credential, token or base URL appears in any file (checked by searching for
the recording environment's values, which are not reproduced here). Thinking
`signature` fields are the model's opaque signatures and stay as recorded.
