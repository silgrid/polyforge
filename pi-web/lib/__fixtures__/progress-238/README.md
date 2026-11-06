# progress-238 — recorded transcript fixtures

Recorded, not hand-written: trimmed and redacted copies of real step transcripts, used by
`lib/polyforge-cards.test.mjs` to prove the execution-progress parser (`progressEntries`)
and the whole-job read (`readProgressSnapshot`) on both harness formats.

| file | source | harness | spawn |
|---|---|---|---|
| `pi.jsonl` | capsule `task-219` (wi `wi_ly8ZLo9wz703`), `session/session-aa78d3368a8565b2#3.jsonl` | pi | spawn 4, step `spec` |
| `claude.jsonl` | the same capsule, `session/session-aa78d3368a8565b2#4.jsonl`, plus its one assistant-text record from `#5.jsonl` | claude 2.1.312 | spawn 5, step `grill-review` (the text record: spawn 6, the `--resume` continuation of the same claude session — spawn 5 itself wrote no assistant text) |

Recorded on this host on 2026-11-01, by a one-off script run in the `code` step of
 (not committed).

**Trimming.** Records kept in file order: the first record of every distinct
`(type, message.role, content block types)` shape — so every excluded kind is present
once (pi `session`, `model_change`, `thinking_level_change`, system/user prompts, tool
results; claude `queue-operation`, `attachment`, `api-request*`, `api-request-blob`,
`tools-used`, `last-prompt`, `cost-state`, `atis-latch`, user prompts and `tool_result`) —
plus up to two or three of the shapes that carry progress (pi assistant
`text`/`thinking`/`toolCall`, claude assistant `tool_use`).

**Redaction.** Assistant text kept (cut at 160 characters), tool-call arguments kept
as scalar strings (cut at 160 characters), and pi thinking text kept (cut at 160
characters — makes a non-empty thinking block a progress narration line);
claude thinking is stored as the empty string the real records carry (Claude redacts it).
Tool results, user/system prompts and every other content are replaced by a
`[redacted …]` marker, thinking signatures dropped; every top-level field outside the
record's identity, type, timestamp and `message` replaced by `"[redacted]"`; `cwd`
replaced by `/redacted/capsule`; the developer host root of the remaining paths
rewritten to the neutral `/workspace` root for the public baseline; credential-shaped values (`token=`, `password=`,
`api_key=`, `sk-`/`ghp_`/`npm_` prefixes) replaced before the cut. Timestamps, ids,
block order and record shapes are the recorded ones.
