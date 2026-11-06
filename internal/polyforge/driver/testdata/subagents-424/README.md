# subagents-424 — pi subagent stdout replay

`pi-stdout.jsonl` is the stdout a pi step streams for one call of its
`subagent` tool, as `driver/subagentrec_test.go` replays it into the recorder:
`tool_execution_start`, one `tool_execution_update` per child message (each
carrying the child's whole conversation so far in
`partialResult.details.results[0].messages`, the way the subagent extension's
`onUpdate` streams it), one unrelated parent record, then
`tool_execution_end` with the final `result.details`.

Derived, not hand-written: the child messages are a recorded subagent run's
`details.results[0].messages`, taken from the tool result a real pi step
persisted in its capsule transcript (capsule `task-263`, recorded
2026-11-02, a single-mode `merge-conflict-reviewer` call), by a one-off
script (not committed). Kept: the first six messages (`system`, `user`,
two tool rounds) and the run's final assistant message, so the conversation
is complete and ends on the child's answer.

Redaction: every text, thinking and system-prompt body is a
`[redacted <kind> <n>]` marker; tool-call arguments are a neutral
`{"command":"git status --short"}`; provider/model names are
`example-gateway` / `model-a`; response ids, raw stop reasons and nested tool
`details` are `[redacted]`; the task is a neutral sentence. Roles, content
block types, tool names and ids, timestamps, usage and stop reasons are the
recorded ones.

The recorder's output for this replay is the Pi Web fixture
`pi-web/lib/__fixtures__/subagents-424/pi/`, held byte-equal by
`TestPiSubagentRecorderMatchesThePiWebFixture`.

## pi-parallel-stdout.jsonl — a recorded parallel call

Recorded, not derived: the full stdout of a real
`pi --mode json -p --no-session --no-skills --no-context-files
--no-prompt-templates` run (pi 1.4.0, 2026-11-09) in a throwaway agent
directory carrying the same `extensions/subagent` the step agent dir carries
and one user agent `reader` (tools `read, ls`), asked to call `subagent` once
in parallel mode with two tasks (read `a.txt` / `b.txt`, reply with the first
line). It shows the extension's real streaming shape: a task not yet started
is the `exitCode: -1` placeholder with no messages, and a started task
streams `exitCode: 0` while it is still running — so no task is finished
before the call's `tool_execution_end`
(`TestPiSubagentRecorderRecordedParallel`).

Redaction: the child system prompts are `[redacted system prompt]` (their
other fields `[redacted]`), response ids `[redacted]`, the provider and model
names `example-gateway` / `model-a`, and the run directory `/workspace/work`
(the agents dir `/workspace/.pi/agents`). Everything else — record order,
event types, ids, timestamps, usage, the children's tool calls and file
contents — is the recording.
