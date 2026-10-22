# UDS routes — the daemon's local HTTP face

The routes mounted by the daemon's local Unix-socket HTTP face
(`internal/polyforge/daemon/routes.go`). **Face** marks who calls the
route: `user` = the operator's CLI / watch tooling; `step` = a workflow
step's own process, identified by its process tree (every other caller
is refused). Every other mutating (non-GET) route refuses a caller that
belongs to a step agent's process tree, or whose UDS peer credential is
unavailable, with `DAEMON-403` (polyforge#122 accident guard). The
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
| GET | `/status` | Machine status: jobs (a pending question read from the capsule's `execution.json`), queue stats, auto switch and cap. | user |
| POST | `/ops/config` | `pf ops auto on\|off` / `pf ops max N`: body `{"auto":bool,"max":N}` (either or both). | user |
| POST | `/shutdown` | Shut the daemon down: stop the agents, save each job's task-repo WIP, exit (≤30s). | user |
| POST | `/chat/init` | `pf chat init`: the chat id of a harness session (created on first use). | user |
| GET | `/jobs/{wi}/watch` | `pf watch <wi> --once`: per-work-item snapshot (status, step, pending question, events). | user |
| GET | `/chats/{id}/subscriptions` | A chat's daemon-held subscriptions. | user |
| POST | `/chats/{id}/subscribe`, `/unsubscribe` | Subscribe / drop a work item for a chat. | user |
| POST | `/chats/{id}/ack` | Acknowledge consumed events / delivered decisions (advances the cursor). | user |
| POST | `/chats/{id}/subscribe-owned` | Every watcher poll: subscribe the chat to the held work items it submitted or its owner owns (a work item submitted from another chat is shown as progress only; only its origin chat — or, while that chat's watcher is silent, one fallback chat — is woken), and mark the chat live. | user |
| GET | `/step/memories/search` | A step agent's `pf recall`: search the calling step's own project's memories. | step |
| GET | `/step/memories/{id}` | A step agent's `pf memory show`: read one memory of the calling step's own project. | step |
| POST | `/step/memories` | A step agent's `pf remember`: the wrap-summary write, validated (type `experience.wrap`, attributed to the step's own work item, idempotent per step dispatch). | step |
| POST | `/step/result` | `pf result submit` inside a step: the daemon stamps the calling step's `wi_id`/`step_id`/`spawn_n` and lands its result envelope atomically at `runtime-state/pending-results/<spawn_n>.json`; a disagreeing identity or a shape defect is refused and nothing is written (polyforge#214). | step |

Authoritative source for this table is the code itself — the
`mux.Handle`/`mux.HandleFunc` mount list in
`internal/polyforge/daemon/routes.go`; the same table, with the
step-origin refusal and socket resolution, is in the design docs'
reference (`docs/design/polyforge-vnext/reference.md` §6.1 — the
[architecture page](../architecture.md) lists the design docs' reading
order).