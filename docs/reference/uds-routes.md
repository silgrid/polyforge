# UDS routes — the daemon's local HTTP face

The routes mounted by the daemon's local Unix-socket HTTP face
(`internal/polyforge/daemon/routes.go`). **Face** marks who calls the
route: `user` = the operator's CLI / watch tooling; `step` = a workflow
step's own process, identified by its process tree (every other caller
is refused); `task` = a one-shot task agent's process, identified by its
own markers (`PF_TASK_ID` + `PF_DAEMON_ID`, workbench#53). Every mutating
(non-GET) route refuses a caller that belongs to a step agent's process
tree, or whose UDS peer credential is unavailable, with `DAEMON-403`
(polyforge#122 accident guard). A caller in a TASK agent's process tree is
refused `DAEMON-403` on EVERY mutating route with no allowlist at all —
`/step/result` and `/step/memories` included, and `/task/spawn` itself (no
recursion); its reads stay open, and the two `/task/memories` GETs are
scoped to its own project. The
concepts (daemon, claim, chat identity) are defined on the [concepts
page](../concepts.md); the failure codes of the recovery paths are the
[failure-codes page](failure-codes.md).

| Method | Path | Role | Face |
| --- | --- | --- | --- |
| POST | `/jobs/{wi}/answer` | Answer a pending agent question. | user |
| POST | `/jobs/{wi}/claim` | Start a queued work item: enqueue its claim (`pf submit`; optional body `models`: the manual candidate list of `--models`), or with `{"resume":true}` claim and start it now — the daemon half of `pf resume` on a blocked work item (the CLI lands aihub's resume-blocked door with the human's own key first). | user |
| POST | `/jobs/{wi}/resume-paused` | The live-holder half of `pf resume` on a paused work item (the CLI clears `clean_pause` at aihub first; this makes the online holder re-attach). | user |
| POST | `/jobs/{wi}/pause` | CLI close path: seal the job with reason `paused`. | user |
| POST | `/jobs/{wi}/wrap` | CLI close path: seal the job with reason `wrap` or `fail`. | user |
| GET | `/status` | Machine status: jobs (a pending question read from the capsule's `execution.json`), queue stats, auto switch and cap, plus the additive `model_health` summary (window + current warnings). `queue.max_configured` is the daemon's last applied/startup cap (additive, read-only): `pf ops max` changes only `queue.max_concurrent`, so the pair distinguishes a temporary override from a stale file value. | user |
| GET | `/models` | The read-only model+effort × step profile over the events-retention window (`pf ops models`; `--json` re-emits this document). Read-only, no write face, no config, no mutation: any non-GET on the path is refused (405). Reachable by any local UDS peer, including a step process, exactly like `GET /status`. The payload carries only model/effort names, step ids, counts, rates, durations, the window and the current warnings — no paths, prompts, transcripts, envelopes or credentials. | user; step processes may read |
| POST | `/ops/config` | `pf ops auto on\|off` / `pf ops max N`: body `{"auto":bool,"max":N}` (either or both). | user |
| POST | `/config/reload` | `pf config set/unset` (after its write) and `pf config reload`: re-read the daemon's own `cli.toml` through the shared validator and apply each key by its class (live / next-claim / restart-only). A failure applies nothing, answers a typed error naming the key and leaves the daemon up. Local Unix socket only; a step agent's process tree is refused `DAEMON-403` by the step-origin guard. | user; step agents refused |
| POST | `/config/check` | `pf config set/unset`'s pre-write preflight: a proposed `[workspace]` mapping, checked against the held jobs (a removal/re-route that would strand an in-flight work item is refused, listing it) without writing anything. Local Unix socket only; step agents refused `DAEMON-403`. | user; step agents refused |
| POST | `/shutdown` | Shut the daemon down: stop the agents, save each job's task-repo WIP, exit (≤30s). | user |
| POST | `/chat/init` | `pf chat init`: the chat id of a harness session (created on first use); the body's `workspace` (the workspace root the CLI resolved the session cwd to, `""` = unmapped) tags the chat — the tag scopes a gone-origin decision wake and adopt eligibility. | user |
| GET | `/jobs/{wi}/watch` | `pf watch <wi> --once`: per-work-item snapshot (status, step, pending question, events). A WI running/blocked on another fresh machine is resolved through a signed 5 s progress query and the answer carries `holder_machine`; stale/deregistered/unsupported holders answer CORE-504/410/501. A step process may read its own local snapshot but is refused DAEMON-403 before a remote query leaves this daemon. | user |
| GET | `/jobs/{wi}/artifact?path=<relpath>` | `pf watch <wi> --artifact <relpath>`: read a capsule `artifacts/` file (relative, no `..`/absolute/symlink escape, ≤4 MiB) through the same decision chain; queued/terminal answers a pointer only. A step process is refused DAEMON-403. | user |
| POST | `/messages/{id}/analyze` | `pf msg analyze <id>`: the explicit, read-only, one-at-a-time analyzer (300 s cap, the workspace's `summarize.models`, summarize-cwd); the conclusion returns only to the caller and is never stored. A step process is refused DAEMON-403; no loop starts it. | user |
| GET | `/chats/{id}/subscriptions` | A chat's daemon-held subscriptions. | user |
| POST | `/chats/{id}/subscribe`, `/unsubscribe` | Subscribe / drop a work item for a chat. | user |
| POST | `/chats/{id}/adopt` | `pf watch adopt <wi>`: an eligible same-workspace chat takes over the work item's wake-target role — it becomes the origin chat and inherits the delivery record (nothing replays). Refusals: DAEMON-400 naming the work item's workspace (untagged / cross-workspace adopter), DAEMON-409 naming the live target's chat id, DAEMON-404 without a local job. | user; step processes refused by the step-origin guard (DAEMON-403) — adopt runs only on an explicit human request |
| POST | `/chats/{id}/ack` | Acknowledge consumed events / delivered decisions (advances the cursor). | user |
| POST | `/chats/{id}/subscribe-owned` | Every watcher poll: subscribe the chat to the held work items it submitted or its owner owns (a work item submitted from another chat is shown as progress only; only its origin chat — or, while that chat's watcher is silent, one fallback chat — is woken), and mark the chat live. | user |
| GET | `/step/memories/search` | A step agent's `pf recall`: search the calling step's own project's memories. | step |
| GET | `/step/memories/{id}` | A step agent's `pf memory show`: read one memory of the calling step's own project. | step |
| POST | `/step/memories` | A step agent's `pf remember`: the wrap-summary write, validated (type `experience.wrap`, attributed to the step's own work item, idempotent per step dispatch). | step |
| POST | `/step/result` | `pf result submit` inside a step: the daemon stamps the calling step's `wi_id`/`step_id`/`spawn_n` and lands its result envelope atomically at `runtime-state/pending-results/<spawn_n>.json`; a disagreeing identity or a shape defect is refused and nothing is written (polyforge#214). | step |
| POST | `/task/spawn` | `pf task spawn`: start one one-shot task agent (workbench#53); the id is answered immediately. A step or task process tree is refused `DAEMON-403` (no recursion). | user; step and task processes refused |
| GET | `/task/list` | `pf task list`: every known task (id, project, state, age; no report text or prompt). | user; any local peer may read (like `GET /status`) |
| GET | `/task/show/{id}` | `pf task show`: one task's metadata and full report. | user; any local peer may read (like `GET /status`) |
| POST | `/task/cancel/{id}` | `pf task cancel`: end a running task (it ends `interrupted`). | user; step and task processes refused |
| GET | `/chats/{id}/task-notices` | The resident watcher's fetch of the chat's unacked task terminal summaries (one line each, ending with `pf task show <id>`). | user; any local peer may read |
| POST | `/chats/{id}/task-notices/ack` | Ack the delivered task summaries (fetch-then-ack; a crash between print and ack repeats a line, never loses one). | user; step and task processes refused |
| GET | `/task/memories/search` | A task agent's `pf recall`: search the calling task's own project's memories (the daemon holds the key). | task |
| GET | `/task/memories/{id}` | A task agent's `pf memory show`: read one memory of the calling task's own project. | task |

Authoritative source for this table is the code itself — the
`mux.Handle`/`mux.HandleFunc` mount list in
`internal/polyforge/daemon/routes.go`; the same table, with the
step-origin refusal and socket resolution, is in the design docs'
reference (`docs/design/polyforge-vnext/reference.md` §6.1 — the
[architecture page](../architecture.md) lists the design docs' reading
order).