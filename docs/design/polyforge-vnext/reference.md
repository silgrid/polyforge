# Polyforge reference — the current system

> What exists in this tree, derived from the code; every claim names its source file.
> **The code is the authority**; where this file and the code disagree, fix this file. Decisions and
> their rationale live in [v2-converge.md](v2-converge.md) (decision authority); the
> parent/child/bundle contract in [wi-kind-contract.md](wi-kind-contract.md); bundle authoring in
> [01-workflow-skills.md](01-workflow-skills.md); memory in [06-memory-search.md](06-memory-search.md);
> capsule base migration in [08-capsule-base-migration.md](08-capsule-base-migration.md). The v0/v1
> design set is in [archive/](archive/) — history only, do not build from it.
> Anchors use `<!-- §N -->`; `tools/check-xrefs.py` (run by `make check`) resolves every backticked
> name.md#§N reference.

## 1. System overview <!-- §1 -->

Core = **task** (aihub work items) + **memory** + **workflow** (bundles). One binary, `pf`
(`cmd/pf/main.go` routes `pf aihub …` to the server and `pf daemon …` to the daemon), and two
skills — the routing skill and the standalone output-format skill:

| Component | Code | Role |
|---|---|---|
| aihub | `pf aihub` (intercepted in `cmd/pf`) → `internal/aihub`, `internal/aihub/{server,core,memory,git}` | Single HTTP service on SQLite: work items, projects, users, API keys, sessions, memories, and a git smart-HTTP proxy holding one bare repo per WI (the capsule chain). |
| `pf` CLI | `cmd/pf`, `internal/polyforge/cli` | Data-plane verbs talk to aihub over HTTP (`internal/polyforge/aihubclient`); execution-plane verbs talk to the local daemon over a unix socket (`internal/polyforge/cli/daemon.go`). |
| daemon | `pf daemon` → `internal/polyforge/daemon` | One per machine. Claims WIs, owns capsules, hosts the driver and the supervisor in-process, serves the UDS face, heartbeats to aihub. |
| runtime | `internal/polyforge/runtime` | Job records, claim/attach/end, materialization, recovery engine, restart reconciliation, process reaping. |
| driver | `internal/polyforge/driver` | Runs one WI's workflow: dispatches one short-lived harness process per step, collects result envelopes, advances `runtime-state/execution.json`. |
| supervisor | `internal/polyforge/supervise` (rules), `internal/polyforge/observe` (monitor, events, push loop) | Judges a step dead / the driver stalled, records events, pushes the capsule chain. Never advances execution (`v2-converge.md#§2`). |
| capsule | `internal/common/capsule` | Per-WI working directory `<workspace>/capsules/<project>-<seq>/`, a git worktree whose history is the WI's chain on aihub (§8). |
| harness adapters | `internal/common/harness`, `internal/polyforge/runtime/spawn.go`, `runtime/claudespawn.go` | pi (`--mode rpc`) and Claude Code (`-p --safe-mode --setting-sources user`, every auth mode) step processes (§7.6). |
| watcher | `internal/polyforge/watcher` | `pf watch`: human progress + decision notifications (`v2-converge.md#§3`). |
| summarize lane | `internal/polyforge/summarize` | The demand-gated transcript-delta narration lane behind `GET /jobs/{wi}/watch?demand=1` (polyforge#224): per-WI memory-only state, the live-dispatch (A11) / growth / interval+in-flight / cap gates, the one-shot harness dispatch (the prompt on stdin), the on-failure candidate walk; a claude dispatch has no tools (`--tools ""`) and opens no cross-session inbox. |
| routing skill | `skills/polyforge/` (`SKILL.md` + `verbs/*.md`, `extensions/pf-chat.ts`, `hooks/claude-session-start.sh`) | The only routing skill in the user's harness: maps intent to a `pf` verb; never parses or runs a workflow (§11). Install also ships the independent output-format skill (next row). |
| output-format skill | `skills/output-format/` (`SKILL.md`) | Owner-facing output discipline for the session, envelope and panel surfaces; loaded independently of the routing skill; carries no verbs and does no routing. |

Trust model: team-internal; harnesses are logged in by a human; the daemon never manages credentials;
no heavy isolation (#135 frozen). Guards exist against **accidents** (wrong push target, skipped human
gate, unreviewed release, agent driving the daemon) — `v2-converge.md#§0`.

### 1.1 Glossary <!-- §1.1 -->

- **WI** — work item (aihub row). Slug `<project>#<seq>`; `seq` is unique per project.
- **bundle** — a workflow: `workflows/<name>/workflow.yaml` (its prose `README.md`, which the daemon never reads) + `steps/<id>.md` (§7.1). A WI with `bundle_name` is an *execution* ticket; without, a *registration* ticket; with children, an *epic* (derived, not stored — `wi-kind-contract.md#§1`).
- **session** — the claim lock: a daemon-minted secret whose hash aihub stores (§3). Not a chat.
- **chat** — `PF_CHAT_ID`, a notification label for an interactive harness session; never a credential (`v2-converge.md#§4`). Retired: see §6.1.
- **capsule** — the per-WI directory + git chain (§8). **task repo** — a project repo cloned under `repos/<name>/`.
- **spawn / spawn_n** — one step process dispatch and its per-WI monotonic counter.
- **step agent** — the harness process running one step. **operator** — the human.

## 2. Work items <!-- §2 -->

### 2.1 Statuses and transitions <!-- §2.1 -->

Statuses (`internal/common/api/types.go`): `queued running paused blocked wrapped failed canceled`
(single-l `canceled`). Terminal: `wrapped failed canceled` (`internal/aihub/core/workitem.go` `isTerminal`).
PATCH never sets status (`patchableFields`). Every transition is one guarded UPDATE in one transaction.
PATCH records the authenticated caller as the `wi_content_updated` event's actor (
the payload stays empty and historical events are not backfilled).

| Transition | Door (route → function) | Guard / effect |
|---|---|---|
| — → queued | `POST /work-items` → `WIManager.Create` | writer on the project; `repo_inventory` frozen at create (names only, must be project repos — `core/inventory.go`); `exclusive: true` (needs `bundle_name`, else `CORE-400`) refuses the create `CORE-409 OPEN_ON_BUNDLE`, naming the open work item and its status, while another work item of the same project on the same bundle is not wrapped/failed/canceled — checked inside the create transaction, so racing creators never both commit (the integration lane's one open batch per project) |
| queued → running | `POST …/claim` → `core/session.go` `Claim` | §3.2 |
| running → paused | `POST …/session-end` `status=paused` → `Pause` | must be running (`NOT_RUNNING`); session kept; `clean_pause` set atomically; event `session_paused` |
| paused → running | `POST …/session-attach` → `Attach` | hash mismatch → `CORE-403 STALE_SESSION`; not paused or `clean_pause=true` → `CORE-409 NOT_ATTACHABLE`; event `session_attached`; operator unpause = PATCH `clean_pause:false` |
| running/paused → wrapped/failed/blocked | `POST …/session-end` → `End` | hash-fenced; clears the whole session row; wrapped/failed set `closed_at`; blocked sets `waiting_on='owner'` and, with `head_sha`, `attrs.blocked_seal` (§3.5); wrapped with `attrs` merges them key by key into the work item's attrs (the develop lane's `integration` hand-off; any other verdict with `attrs`, or a `blocked_seal` key, is `CORE-400`); event `session_ended` (naming the merged keys under `attrs`) |
| running/paused → queued | `POST …/force-end-session` → `ForceEnd` | admin or project maintainer; reason required; terminal → `CORE-410`, no session → `CORE-409 NOT_HELD`; clears session + seal; event `session_superseded` (old fingerprint). CLI: `pf ops end-session <wi> --reason …` |
| blocked → queued | `POST …/resume-blocked` `{accept_lost_work?}` → `ResumeBlockedWith` | reporter/maintainer/admin, or the holder machine's current operator as a project writer or maintainer (below); terminal → `CORE-410`; status blocked (`NOT_BLOCKED`) ∧ no session (`SESSION_HELD`); event `wi_resumed` (`{from, to}`, plus `basis: holder_operator` and the holder `machine_id` when holding the machine is the caller's sole basis); `accept_lost_work:true` also clears `attrs.blocked_seal` in the same transaction with event `blocked_seal_abandoned` `{head_sha, machine_id}` (§3.5). CLI: `pf resume <wi> [--accept-lost-work]` |
| queued/paused/blocked → canceled | `POST …/cancel` → `Cancel` | reporter/maintainer/admin; on a **blocked** work item only, also the holder machine's current operator as a project writer or maintainer (below); running → `CORE-409`; terminal → `CORE-410`; event `work_item_canceled` (`{reason, question_id}` when given, plus `basis: holder_operator` and the holder `machine_id` when holding the machine is the caller's sole basis) |
| queued/paused/blocked → wrapped/failed (no bundle) | `POST …/wrap` `{fail?, note?}` → `Wrap` | reporter/maintainer/admin; terminal → `CORE-410 TERMINAL_STATE`; session held → `CORE-409`; bundle bound → `CORE-409` (the daemon wraps execution tickets); a child not terminal → `CORE-409 OPEN_CHILDREN`; sets `closed_at`; event `work_item_closed` `{status, note?}`. CLI: `pf wrap <wi> [--fail] [--note …]` when the daemon is unreachable or has no local job for it |

The blocked WI has exactly two exits: `pf resume` (continue = resume-blocked + a fresh claim) and
`pf cancel` (abandon) — `v2-converge.md#§6`. Both exits also admit the **holder operator**:
the current `machines.operator_id` of the machine named by the work item's latest
`session_claimed`/`session_attached` event (`WIManager.HolderMachine`), when that user is a project
writer or maintainer now. No liveness test applies — a blocked work item has no session, an offline
machine's operator keeps the right, and a handover of the machine row moves it; a later claim or
attach moves the holder; a latest event with no machine, or a machine row with no operator, means no
holder. The holder basis never widens cancel on a queued, running or paused work item (the reserved
`cancel` answer's probe of a running work item keeps the reporter/maintainer/admin gate), paused
resume (a writer-level PATCH `clean_pause:false`) or `wrap`. A refused member on a blocked work item
with a holder gets `AUTH-403` naming the holding machine's id and that its operator may resume or
cancel — never the operator's user id; with no holder the existing wording stands. "Resume is a human
verb" is a code convention, not a hub check: only the `pf resume` CLI calls the resume-blocked door
and the daemon never does; the hub cannot and does not tell a human operator from that operator's
own daemon. `end_pending`, `recovering`, `sealing`, `claiming` are
**daemon-local job states** (`internal/polyforge/runtime/state.go`), not hub statuses.

### 2.2 Dependencies, parents, ready queue <!-- §2.2 -->

- Edge kinds `blocks` (default), `supersedes`, `related` (`core/workitem.go` `AddDependency`); same project only; no self-edge; a `blocks` blocker must be an execution ticket (`CORE-400 BLOCKER_NOT_EXECUTABLE`); non-`related` edges refuse cycles (`CORE-409 DEPENDENCY_CYCLE`). Only a **wrapped** blocker releases a `blocks` edge (`core/owner.go`, #161).
- Parent/child and bundle binding rules: `wi-kind-contract.md#§2` (one nesting level, `PARENT_NOT_REGISTRY`, `HAS_CHILDREN_NO_BUNDLE`, bundle frozen at first claim).
- Ready queue `GET /projects/{p}/ready-queue` (`core/queue.go`): `{project, items, running, paused}`; `items` = queued ∧ `waiting_on` empty ∧ no unfinished `blocks` edge ∧ has a bundle ∧ inventory repos still exist; ordered priority (`urgent high normal low`) then `created_at`. No `blocked` segment.

### 2.3 attrs: no execution state <!-- §2.3 -->

`attrs` is a free JSON object written at create; aihub never stores workflow execution state in it.
Execution state lives only in the capsule's `execution.json` (§7.3). The one reserved key is a
top-level `blocked_seal`: aihub itself writes it when a daemon's session-end reports `blocked` with a
head (§3.5), through a direct update that bypasses the create-time check, so create refuses it with
`CORE-400 EXECUTION_STATE_REJECTED` (`core/workitem.go` `checkAttrs`); a create cannot plant one.

## 3. Sessions and fencing <!-- §3 -->

### 3.1 Secret, hash, fingerprint <!-- §3.1 -->

- **secret** `session_id`: 32 random bytes hex, minted by the daemon (`runtime/runtime.go`), journaled before the claim call (§3.5). Lives only in the daemon's data dir — the claiming journal (`claiming.json`) and the job record (`runtime-state.json`) — and in a one-shot `GIT_ASKPASS` file; never in the capsule, argv or step env.
- aihub stores only `session_hash = hex(sha256(secret))`; public **fingerprint** = first 16 hex chars (`api.SessionHashHex`, `api.SessionFingerprint` in `internal/common/api/types.go`). WI reads expose `session_fingerprint`, `session_machine`, `session_started_at`, `clean_pause`.

### 3.2 Claim <!-- §3.2 -->

`POST /api/v1/work-items/{id}/claim` body `{work_item_id, session_id, machine_id}`
(`api.SessionClaimRequest`); caller needs writer/maintainer membership or admin (key scope alone does
not authorize). A caller without access — a non-member, or a key scoped to another project — gets the
same missing-work-item `CORE-404` an unknown reference gets, before any body decoding;
a member below writer keeps `AUTH-403`. The submitted `machine_id` resolves inside the claim transaction:
a machines row owned by the caller records its id; a row owned by another operator is
refused `AUTH-403` with nothing written (admins follow the same rule); an absent or operator-less row
records `NULL` and the claim succeeds — a later progress/artifact query for that run gets the existing
`CORE-410` missing-holder answer. One transaction (`core/session.go` `Claim`), checked in order: not terminal
(`CORE-410 TERMINAL_STATE`), no session (`CORE-409 ALREADY_HELD`), queued (`NOT_CLAIMABLE`), no
unfinished blocker (`DEPENDENCY_BLOCKED`), bundle present (`NOT_CLAIMABLE`); then
`UPDATE … WHERE status='queued' AND session_hash IS NULL` (0 rows → `CORE-409 RACE_LOST`); a declared inventory repo that left the project → `CORE-409 INVENTORY_REPO_GONE`. Response `{claim, work_item}`
(`api.SessionClaimResponse`; `claim.repos` = the inventory-filtered project repos; `sealed_head` /
`sealed_machine` from the blocked seal). Idempotency is keyed on the session secret (an `idempotency_key` an older
daemon sends is ignored): a repeat by the session that holds the WI answers the same claim, re-read from the row, with no
write; a secret whose `session_claimed` event exists but that no longer holds the WI (force-ended,
superseded, ended) → `CORE-403 STALE_SESSION` before any gate, so it never claims again.

### 3.3 Verify, attach, end <!-- §3.3 -->

Access gates: `session-verify` requires project read (any membership or admin);
`session-attach` and `session-end` require writer or above, like claim. A caller without access gets the
same missing-work-item `CORE-404` an unknown reference gets — a leaked live session secret does not lift
the membership gate — and the refusal precedes any body decoding or state change. A member below writer
keeps the route's `AUTH-403`.

- `session-verify` `{session_id}` → `{work_item_id, matches, status, current_fingerprint, clean_pause}`; compare-only, no state change. Used at restart, by the claiming-journal replay, and by the M4 revalidation (§3.5).
- `session-attach` `{work_item_id, session_id, machine_id}` → same shape as claim; same-session resume of a paused WI (restart reconciliation, `pf resume` of a paused WI via daemon `POST /jobs/{wi}/resume-paused`, `v2-converge.md#§7`). The submitted `machine_id` resolves under the same ownership rule as claim: caller-owned records, another operator's row is `AUTH-403` with nothing written, absent or operator-less records `NULL`; an absent `machine_id` keeps the recorded machine.
- `session-end` `{session_id, status: wrapped|failed|blocked|paused, note, clean_pause, head_sha, attrs}` (`api.SessionEndRequest`; `attrs` on wrapped only). Hash mismatch (or a lost race on the guarded UPDATE) → `CORE-403 STALE_SESSION`.

**De-membered holder (accepted mid-run behavior)**: when the operator of a running work item loses project
membership, its daemon's verify and end answer the missing-work-item `CORE-404`; the hub leaves the work
item running — no implicit termination, reassignment or repair — with its session and events unchanged.
The accepted recovery is a maintainer/admin `force-end-session`, which returns the WI to queued under the
existing behavior (the daemon-side reaction is §3.5's ordinary 404 handling).

### 3.4 Git push auth <!-- §3.4 -->

`/git/<wi>.git` (`internal/aihub/git/proxy.go`, `core/owner.go` `CheckSessionCredential`): Basic user
must be the WI id, password the live session secret. No session → `GIT-401 NO_SESSION`; mismatch →
`GIT-401 BAD_CREDENTIAL` (message names the current holder fingerprint); status ≠ running →
`GIT-403 PAUSED` (checked on every smart-HTTP request, so fetch/clone are refused too). Fencing covers the
aihub proxy only — pushes a step makes to an external origin are not fenced.

### 3.5 Daemon-side session state <!-- §3.5 -->

- **Claiming journal** `<data-dir>/claiming.json` (`runtime/claiming.go`): `{work_item_id, session_id, models}`, keyed by the canonical work-item id, written durably **before** the claim call (write failure refuses the claim); cleared when the start settles (RUNNING record persisted), when the hub refuses the claim, or when a post-claim failure is compensated with `SessionEnd(blocked)` (a compensation that does not land keeps the session in an `end_pending` record). A lost claim answer keeps the entry. Replay (a later Start, or Restart through a CLAIMING placeholder): verify transport error → keep; no match → drop; a record already carrying the session → drop; else re-claim with the same session secret. The clone and materialize of an adopted replay run after Restart returns, on the work item's actor (a shutdown defers it, entry kept). Like every claim, a replay quarantines whatever scene sits at the capsule path (`<name>.quarantined.<ts>`, bytes kept) and clones afresh — no scene is reused. Corrupt file → quarantined `claiming.json.corrupt.<ts>`.
- **Job record** `<data-dir>/runtime-state.json` (`runtime/state.go`): secret, fingerprint, job state, capsule/git dirs, remote, `spawn_n`, recovery-failure count, per-step candidate snapshot.
- **M4 revalidation**: the monitor re-verifies every running job's session (default every 30s, `--revalidate-every`, `observe/session.go`; a boundary push the git face refuses for its credential re-checks at once, and a recovery push refused that way stops the work item once a verify confirms the mismatch); `matches=false` → kill tree, quarantine the capsule (`<name>.quarantined.<ts>`), drop the record, event `session_superseded`; no hub transition. A retained **paused** record has no monitor; the runtime revalidates it at the same cadence (`runtime/restart.go` `revalidatePaused`, one check per record on its actor; polyforge#217): `matches=false` settles it as the restart would (a canceled/wrapped/failed/blocked/queued work item → record dropped; another holder → released), so a `pf cancel` (direct to aihub, from any machine) or a hub-side session end of a paused work item frees the holder's record within ~30s, without a restart; a match changes nothing (the operator pause stands; a cleared `clean_pause` still waits for `pf resume` on the holder or its next start); a verify error waits for the next tick.
- **end_pending** (`runtime/runtime.go` `pendingClose`, `restart.go` `closeBlocked`; retried by the work item's actor, `actor.go` `retry`): a failed `SessionEnd(blocked)` is retried (pushing the local chain first) with the actor's growing backoff until aihub accepts it, verify shows the session is no longer held, or the retry chain reaches its bound (`EndRetryWindow`, 24h from the chain's first retry; then a `retry_exhausted` event leaves the record `end_pending` for a human, a close door answers that the chain is exhausted, and a daemon start reconciles it afresh). A typed 4xx refusal is parked on the spot (`close_refused`) and never retried. A permanently rejected chain push is recorded on the job record (`end_push_gave_up`) and the retry, restarts included, closes without it.
- **Runtime actors** (`runtime/actor.go`): one mailbox and one goroutine per work item (by canonical id). Every operation that changes one work item's job — a Start's claim and settle, a close, a pause, `pf resume`, a recovery, a supersession, the restart's reconciliation and its retries — is a message to that work item's mailbox; messages run one at a time, network steps included, so a second operation of the same work item queues and gets the final answer (no `DAEMON-409`/`DAEMON-503`), while no door of another work item waits. A Start probes on the caller's goroutine and claims on the canonical id's actor, so two spellings of one work item meet in one mailbox. The capsule clone and materialize run on the actor with the job CLAIMING; a close meanwhile cancels the clone before it queues. Every pending hub step (a close the hub has not taken, a verify, a claim replay) retries through one backoff (`EndRetryBase` → `EndRetryMax`, then every `EndRetryMax`), one chain bounded by time — `EndRetryWindow` (24h) from its first retry, so an aihub outage of that length is ridden out — after which it stops and parks the record with a `retry_exhausted` event and the close doors answer that the chain is exhausted (restart the daemon) instead of promising a retry; a typed 4xx refusal is not an outage and is never retried (`close_refused`). The job-table lock guards the table, aliases and mailboxes and is never held across I/O: `pf ops status` / `pf watch` read only it and the job's own mutex, so they answer while the hub hangs. `persist` numbers its snapshots under it and writes under its own mutex; an older snapshot never lands last. The shutdown is `Runtime.Close`: it cancels the actors' context — no agent starts and no retry runs after it; an agent start still under way tears its handle down as it binds (its claim is deferred, journal entry kept; a relaunch leaves the record recovering); the shutdown waits for no actor. See `v2-converge.md#§6`.
- **Blocked seal**: `End(blocked)` with `head_sha` stores `attrs.blocked_seal={head, machine}`; a claim whose clone head differs continues from the sealed head only on the sealing machine, which fetches it from a quarantined git-dir of the work item (`<git-dir>.quarantined.<ts>`, newest first, a materialized generation preferred) and moves the chain to it — the next publish pushes it — and restores the task repos (carrier head and uncommitted work, through the carrier lane into a local `pf-carrier-sealed-*` store) from the same quarantined capsule; a claim by another machine, or a sealing machine that holds the head nowhere, is refused `DAEMON-518` (`runtime.go` `verifySealedHead`; `v2-converge.md#§E`). The owner's override is `pf resume <wi> --accept-lost-work`: the resume-blocked door clears the seal (event `blocked_seal_abandoned` with the abandoned head and machine; actor and time on the event) and a later claim on any machine continues from the aihub chain — whatever the sealing machine never pushed is lost.
- **Single daemon per data dir**: `<data-dir>/daemon.lock` flock taken before the socket probe (`daemon/daemon.go` `acquireDataDirLock`); a live listener on the socket also refuses a second daemon.

## 4. aihub service <!-- §4 -->

### 4.1 HTTP routes <!-- §4.1 -->

Registered in `internal/aihub/server/server.go` `routes()` — 48 registrations (47 `m.HandleFunc` plus the `m.Handle("/git/", …)` mount). Auth: **key** = Bearer API
key (`s.auth`), **admin** = key of a `role=admin` user (`s.authAdmin`). Project access
(`canRead`/`canWrite`): any membership (`viewer`/`writer`/`maintainer`) reads, `writer`+ writes, a global
admin does both; a key scoped to one project is refused elsewhere (`AUTH-403` on project routes).
Work-item-addressed routes (the shared `wiGrant`): a caller without access — a non-member
with no retained reporter right, or a key scoped to another project — gets the same missing-work-item
`CORE-404` an unknown reference gets, before any body decoding or state change; a member whose role is
too low keeps the route's `AUTH-403`. A reporter who left the project keeps cancel/wrap/resume-blocked;
a blocked work item's holder operator (§2.1) is admitted on cancel/resume-blocked only as a current
project writer or maintainer.
A holder who loses membership mid-run sees a missing work item on verify/end while the WI stays
running until a maintainer/admin `force-end-session` — the accepted recovery. No unauthenticated route, no
health route. All paths under `/api/v1` except `/git/` and the read-only web console's own
`/web/` prefix (Basic auth, not Bearer — below).

| Method | Path | Auth / gate | Notes |
|---|---|---|---|
| POST | `/work-items` | key + project writer | create (`bundle_name`, `repo_inventory`, `parent_id`, `exclusive`, …); `exclusive: true` → `CORE-409 OPEN_ON_BUNDLE` while the project has an unfinished work item on the same bundle (§2.1 table) |
| GET | `/work-items` | key; rows filtered by the caller's readable project set, pushed into the SQL **before** the limit (`total` = returned count, admin and non-admin alike) | `limit` (default 500, max 1000), `project`, `status`, `assignee`, `priority`, `milestone`, `bundle`, and `attr=<path>:<value>` (repeatable, all must hold: the attr at the dot path — `^[a-z0-9_]+(\.[a-z0-9_]+)*$`, else `CORE-400` — is that string, read with `json_extract`; workflow contract 1); `?project=<non-member>` stays empty |
| GET | `/work-items/{id}` | key + read | `include=events` (anything else `CORE-400`); the answer always carries the append-only `comments` list |
| PATCH | `/work-items/{id}` | key + writer | `goal content labels priority milestone waiting_on clean_pause`, `bundle_name` in the R2 window (`patchableFields`; `attrs` only at create) |
| PATCH | `/work-items/{id}/attrs` | signed machine + writer | workflow contract 1 (`core/attrs.go`): body `{key, value, if?, owner_message?}` writes the one top-level attrs key `key` (`^[a-z][a-z0-9_]{0,63}$`; Core's own `blocked_seal` is `CORE-400`) whole with the JSON `value` (`null` deletes it), on a work item of any status, terminal included. `if` maps attrs paths to JSON values, a compare-and-swap fence checked in the write's transaction (an absent path reads as `null`): any mismatch is `CORE-409` reason `ATTRS_IF_MISMATCH` and writes nothing. Every write appends `attrs_written` `{key, machine_id, owner_message?}` (`owner_message` `{words, by, at}`, copied verbatim). aihub keeps no lane's rules on the record. Reach: the daemon's machine key only — a step reaches it solely as a pending `pf step hub attrs` write the daemon makes |
| POST | `/work-items/{id}/cancel` | reporter/maintainer/admin; a blocked work item also its holder operator (writer/maintainer) | §2.1 |
| POST | `/work-items/{id}/wrap` | reporter/maintainer/admin | §2.1; work items with no bundle only |
| POST | `/work-items/{id}/claim` | writer (concealed from callers without access, §3.2) | §3.2 |
| POST | `/work-items/{id}/session-verify` | read + secret | §3.3 |
| POST | `/work-items/{id}/session-attach`, `/session-end` | writer + secret | §3.3 |
| POST | `/work-items/{id}/force-end-session` | admin/maintainer | §2.1 |
| POST | `/work-items/{id}/resume-blocked` | reporter/maintainer/admin, or the holder operator (writer/maintainer) | §2.1 |
| POST, GET | `/work-items/{id}/dependencies` | add: writer; list: read | add / list edges; an unreadable blocker is `CORE-404` |
| DELETE | `/work-items/{id}/dependencies/{blocking}/{kind}` | writer | physical delete |
| POST | `/work-items/{id}/comments` | key + writer | append one comment (`{body, reply_to}`); append-only — no edit and no delete route; `reply_to` must name a comment on the SAME work item, else `CORE-404` and nothing is written |
| GET | `/projects/{project}/ready-queue` | key + read | §2.2 |
| POST | `/machines/heartbeat` | key + machine signature; the operator's Bearer; first registration and a legacy-row claim also need any project membership or a global admin | §4.3; body adds `public_key`, `launcher`, `role`, `force`, `capability`; answer adds `operator_id`, `machine_face_version`, negotiated `role`, `multi_machine`, `superseded`, `superseded_by`, `interactive_refused` (#57) |
| GET | `/machines` | admin | §4.3; rows also project `public_key`, `launcher`, `role`, `operator_id` (never `user_id`) |
| POST | `/machines/{id}/rekey` | admin | rebind `public_key` (no key history) |
| GET | `/machines/inbox?faces=queries,messages&wait=25s` | signed machine | holder long-poll: pending queries (always) and the operator's messages pending delivery (above `delivered_seq`) to whichever signed machine asks; the daemon itself requests `faces=messages` only when interactive, not superseded and with a fresh peer (`messageFaceWanted`) |
| POST | `/queries/{qid}/answer` | signed machine | only the addressed holder may answer |
| POST | `/work-items/{id}/progress-query`, `/artifact-query` | signed machine + read | the read gate conceals callers without access (the missing-work-item `CORE-404`); then resolve the fresh holder; the relay budget admits at most 448 relayed queries per holder and 320 per member per holder in any 605 s (the replay TTL plus the answer deadline), else `CORE-429` before the holder is reached; relay the answer (5 s deadline); in-memory only |
| POST | `/messages` | key | send (`to` human user, text ≤4096, optional `about_wi`/`reply_to`); secret scan before any write |
| POST | `/messages/ack` | key | max-advance the caller's own member read cursor (member-only; an admin cannot ack for someone else) |
| POST | `/messages/delivered` | key | max-advance the caller's own member delivery cursor only; unread/read state unchanged (daemon delivery path) |
| GET | `/messages/{id}` | key + member/admin | one message |
| GET | `/conversations` | key | conversations the caller may see (admin: all; a member: its own); each row carries `member_names` (display names for exactly that conversation's `members`); unread counts only other participants above `read_seq` |
| GET | `/conversations/{id}/messages` | key + member/admin | permanent history; `after`/`before` seq cursor, default 50/max 200 |
| POST | `/memories` | key + writer (else `MEM-403`) | §9 |
| GET | `/memories/search` | key; candidates narrowed inside the SQL to the caller's readable projects (key scope ∩ membership, one membership query per request — never per candidate) | **Keyword mode** (`q` non-empty): `q`, `match` (`all` default: every term; `any`: ranked any-term; else `MEM-400`), `type` (exact or `X.*`), `tags`, `limit` (default 50; ≤0 or >200 → 50), `project`, `excerpt` (≤8192 runes → `content_excerpt`). **Browse mode** (empty/absent `q`): `sort` (`updated` default / `created`, else `MEM-400`; descending only), `after` (the opaque cursor of the previous page's `next_cursor`; malformed or issued under another sort → `MEM-400`; past the end → an empty page), `limit` (strict: default 20, 1..200, else `MEM-400` — never clamped), `type`, `tags`, `project`; answer `{items, total, next_cursor?}` (`total` = the whole walk for this caller, visibility applied) with `next_cursor` omitted on the last page; `q` together with `after`/`sort` → `MEM-400` |
| GET | `/memories/{id}` | key; denied = `MEM-404` | `format=content` → raw Markdown body |
| POST | `/memories/{id}/redact` | owner or admin | soft delete |
| POST, GET / GET, PATCH | `/users`, `/users/{id}` | admin | the list is one page of the newest 200 (no cursor); `total` = the true user count |
| POST, GET / DELETE | `/api-keys`, `/api-keys/{id}` | admin | the list is one page of the newest 500 (no cursor); `total` = the true key count |
| POST, GET / GET | `/projects`, `/projects/{id}` | admin | the list is one page of the oldest 200 (no cursor); `total` = the true project count |
| PATCH | `/projects/{id}` | key + in-handler role gate | repos, membership, description |
| GET | `/projects/{id}/members` | key + project read (member or global admin, api-key scope applied) | a non-reader gets the concealed `CORE-404`, a key scoped to another project `AUTH-403`; answer `{contacts:[{id,display_name}]}` = the project's human members other than the caller (no type, role or count); the explicit `?include_caller=true` keeps the caller's own row too (any other value, or none, excludes it) and exists for the daemon's spawn-time task `reporter` resolution with its own key; callers: `pf msg contacts` (default, caller excluded) and the daemon's `/task/spawn` route — a step agent cannot reach it |
| DELETE | `/projects/{id}` | admin (+key scope, same as PATCH) | hard delete: 409 non-terminal WIs (every offending slug) / unrevoked scoped keys (key ids); audit row in `admin_audit`; `?dry_run=1` = the checks and the same body, nothing written; per-WI bare repos removed after the commit |
| POST | `/admin/backup` | admin | `VACUUM INTO <data-dir>/backups/aihub-<ts>.db` — the SQLite database, every memory included (memories live in it); no git repos (`core/admin.go`) |
| GET/HEAD | `/web/`, `/web/{project}/…`, `/web/assets/…`, `/web/api/…` | **Basic only** (any username; the password is an api key, verified through the same key query the Bearer face uses; every key kind then meets the same read gates — no console-specific policy) | the read-only web console — the aihub server's only HTML face, an embedded React SPA: every authenticated document path answers the ONE shell (the client router resolves deep links; unknown client routes render one not-found view), assets answer only under `/web/assets/` (a missing asset is 404 bytes, never the shell), and the private JSON face `/web/api/…` carries the data: `context` (the caller's readable projects), `work-items` (20-page keyset list: status filter, case-insensitive goal+content substring search, opaque `next_cursor`), `work-items/{id}` (goal, server-sanitized `html`, metadata, the append-only comment thread, every message whose `about_wi` matches the ticket by slug or id, both dependency directions with unreadable targets reduced to their id, the redacted event timeline), `memories` (browse with the existing keyset cursor; keyword = one top-50 page with a refine hint when full), `memories/{id}` (header card + already-sanitized html), `search` (project-scoped, two capped groups) and admin-only `machines` (health snapshot, never a public key or capability). No credential and a wrong credential answer the same 401 + `WWW-Authenticate: Basic` static body; every other method → 405 `Allow: GET, HEAD`; `HEAD` matches `GET`'s status and headers with no body; unknown and unreadable objects share one static 404; every response — 200/400/401/404/405 alike — carries `Content-Security-Policy: default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'`, `X-Content-Type-Options: nosniff`, `Cache-Control: no-store`, `Referrer-Policy: no-referrer`, `X-Robots-Tag: noindex` (plus a robots meta). Hashed same-origin script/style assets only — no inline script or style, no external resource; the api key never appears in a response or the log. Reach: a human at a browser only — a step or task agent holds no aihub key and the daemon proxies no `/web/` route |
| ANY | `/git/` | session Basic auth (§3.4) | git smart-HTTP via `git http-backend`; every request body is capped at the 1 GiB constant `capsule.MaxGitRequestBodyBytes` — a declared over-cap `Content-Length` is refused `GIT-400` before any CGI runs; a chunked/undeclared body is cut at the cap and the proxy awaits the copy's verdict before answering anything (http-backend writes its CGI response headers before it reads stdin, so the headers' arrival decides nothing) — an over-cap body gets 4xx, refs unchanged, no quarantine residue |

### 4.2 Process and data dir <!-- §4.2 -->

`internal/aihub/main.go` (`pf aihub`) flags: `-data-dir` (default `/var/lib/polyforge/aihub`), `-listen` (`:8420`),
`-git-janitor-period` (1h, gc/repack of grown bare repos, `git/janitor.go`), `-machines-stale-after`
(5m), `-version`. The HTTP server comes from `server.NewHTTPServer` (`server/httpserver.go`) with three
**constant** pressure bounds — `ReadHeaderTimeout` 10s, `IdleTimeout` 25s, `MaxHeaderBytes` 64 KiB — and
no flag/config/env can change them (registerFlags registers exactly the five flags above); there is
**no global `ReadTimeout`/`WriteTimeout`**, so long git transfers and the 25s inbox long-poll are never
cut mid-body. A fresh DB seeds admin `usr_root` and writes its key to a 0600 file in the data dir
(`bootstrapAdmin`). A second process on the same data dir exits `CORE-508` (`service.lock`). SIGTERM
drains for 10s. SQLite (`core/store.go` `Open`): WAL, `foreign_keys(1)`, `busy_timeout(5000)`,
`synchronous(FULL)`, one open connection (single writer). No startup schema migration: SQL moves only through `.polyforge/deploy.sh` with
`deploy/sql/<tag>/` (`docs/upgrade.md`) — except `memories.content`, which the one-time memory content
migration adds (§9). Data dir: `aihub.db`, `git/<wi>.git` (bare chain repos with the pre-receive hook,
`git/repo.go`), `backups/`, and the memory files `memory/<project>/[_redacted/]<mem>/memory.md`
of a database from before `memories.content` (read only by the migration, never written).
Startup runs the memory content migration after listening and before serving (§9). In production aihub runs as a docker
compose service (`deploy/docker-compose.yml`, `restart: unless-stopped`).

### 4.3 Machines <!-- §4.3 -->

Heartbeat body `{machine_id, runtime_version, mode, live_wi_ids, public_key, launcher, role, force, capability}`
(`core/admin.go`); server-stamped `last_heartbeat_at`. A machine is
identified by its `operator_id` and nothing else: registration creates the `machines` row
and **no** `users` row (there is no machine user, no `user_id` column and no API key — the
daemon runs on its operator-issued key). Daemon cadence
`-heartbeat-every` 60s (`daemon/heartbeat.go`; the first heartbeat lands immediately at boot so registration needs no wait), log-only failures with backoff. `GET /machines`
returns `{machines:[{id,operator_id,runtime_version,mode,live_wi_ids,last_heartbeat_at,heartbeat_age_seconds,stale}], stale_after_seconds, count}`, oldest first.
The daemon id is the machine id: default `m-<short hostname>` (lower-cased, anything but `a-z0-9-` replaced by `-`; `daemon/ensure.go` `hostMachineID`), persisted in `<data-dir>/daemon-id`
(`daemon/identity.go`, `v2-converge.md#§4`).

 adds the machine identity on that row: `public_key` (Ed25519,
base64 on the wire; `core/admin.go`, `<data-dir>/machine-key` is the hex
seed), `launcher` (`claude|pi-web|non-interactive`), `role`
(`interactive|runner`, one interactive per `operator_id`; `force` supersedes
the others and stamps `superseded_at`), `operator_id` (the authenticated
Bearer user, never a body field) and `capability` (JSON card). `GET
/machines` projects all five. There is no machine API key and no key-history
table; `POST /machines/{id}/rekey` (admin) overwrites the binding.

 makes the heartbeat a proof, not only a Bearer (`server.go`
`hHeartbeat`, `machineface.go` `heartbeatProof`, `core/admin.go`
`HeartbeatAuthorized`):

- A first registration — and the claim of a legacy row whose `operator_id`
  is NULL — needs a `public_key` in the body and a signature by it, and
  passes the registration gate: any `project_members` row (viewer, writer or
  maintainer, no role filter) or a global admin. A key outside every project
  is `AUTH-403` and writes nothing.
- A refresh needs the row operator's Bearer plus a signature by the row's
  bound key, using the machine face's existing scheme
  (`internal/common/machinesign`: `METHOD\nPATH\nsha256hex(body)\nTS\nMACHINE_ID`
  in the three `X-PF-*` headers, ±300 s). Another Bearer is `AUTH-403`; an
  absent, invalid, stale or replayed signature is `AUTH-401`, and no field
  changes. A signature is recorded in the anti-replay cache when an accepted
  beat writes, so a refused beat never burns it. The cache is in memory,
  one bucket per machine id per signed-route lane (512 live entries per
  bucket / 600 s TTL, lanes): the heartbeat,
  the inbox long-poll, the relay answer and the outgoing progress/artifact
  query each fill their own bucket, so traffic another member induces (the
  answers to its relayed queries, the inbox wakes they cause) never shares
  the heartbeat's quota and cannot push a holder stale. A signature covers
  its method and path and each route has one lane, so it is only ever
  checked in one bucket and a replay is caught as before. An unexpired
  entry is never evicted — another machine's flood cannot flush a captured
  signature — and a machine that fills one of its buckets is `CORE-429` on
  that lane's routes only, while every other machine and lane stays
  accepted. Every check sweeps every bucket — expired entries are dropped
  and empty buckets go with them, whichever machine checks next — so the
  bound (≈150 KiB per saturating bucket, 512 × buckets with live entries
  in total) counts buckets holding live entries, not ids seen since
  startup; an over-quota machine recovers as its own entries
  expire (≤600 s, and a refused beat records nothing). An aihub restart
  forgets the cache, reopening at most one ±300 s window per restart.
- A different presented key is `AUTH-403` and never rebinds the row;
  `POST /machines/{id}/rekey` stays the only rebind, and a heartbeat never
  changes `operator_id`.
- A `machine_id` already registered to another operator is `AUTH-403` saying the
  machine is registered to another operator and suggesting a different
  `--machine-id` — naming no one (the refusal carries neither
  the operator's id nor the display name; the admin machine inventory,
  `GET /machines`, is the place that maps a machine to its operator); the
  existing row is untouched. Membership is never re-checked on refresh — only
  a revoked operator API key ends a machine's authority.
- One heartbeat is one transaction (read, decide, write, commit on the
  store's single connection): two concurrent beats
  serialize, and the loser answers exactly what it would have answered had
  the two run one after the other — the same operator's loser refreshes
  (`200`, `registered=false`), another operator's loser gets the unnamed
  `AUTH-403` collision, a residual concurrent registration `INSERT` maps to
  `CORE-409` as a backstop (never `CORE-500`), and no path rebinds
  `operator_id` or `public_key`. The registration gate is evaluated before
  the transaction and enforced by value inside it, only on the two
  registration acts.
- An UNSIGNED heartbeat is the bounded transition for an already-registered
  pre-signing daemon: accepted only from the row's operator, only on a row
  that already has a bound key, only while BOTH the request's and the row's
  stored `runtime_version` parse as `vX.Y.Z` and predate `v2.20.8`
  (the gate: `dev`, empty or unparseable on either side fails
  closed and writes nothing), and it touches only `last_heartbeat_at`,
  `live_wi_ids`, `mode` and `runtime_version` (identity, role, launcher,
  capability and key fields are ignored). A signed `v2.20.8+` beat ratchets
  the row closed — an unsigned beat can never raise the stored version back
  over the gate. The owner-accepted limit: the gate does not stop a leaked
  operator API key from keeping a retired pre-`v2.20.8` machine online —
  an unsigned beat never ratchets the row closed, so a row no signed beat
  ever refreshed stays open to the key's holder; revoking the operator
  key retires the machine, and only deleting the branch closes the
  residual. **Deletion condition:** once the
  owner confirms `GET /machines` shows no live unsigned machine — a live row
  (`stale=false`) whose `runtime_version` predates this release, because this
  release's daemon signs — the next release deletes this branch.
- Reachability: a daemon holding its operator's API key plus the machine key
  reaches the route (during the transition, the operator's key alone
  refreshes an already-registered row's liveness, and only while that
  row's stored `runtime_version` still predates `v2.20.8` — the version
  gate, which a signed `v2.20.8+` beat ratchets closed). A step agent
  holds no aihub key and reaches neither.

## 5. Error codes <!-- §5 -->

Registry `internal/common/api/errors.go`; HTTP status = numeric suffix. `core.Fail` adds `extra.reason`.

| Code | Meaning (as raised) |
|---|---|
| `CORE-400` | Validation. Reasons incl. `IMMUTABLE`, `HAS_CHILDREN_NO_BUNDLE`, `PARENT_*`, `INVENTORY_*`, `BLOCKER_*`, `EXECUTION_STATE_REJECTED` |
| `CORE-403` | `STALE_SESSION`: the presented session is not the holder (attach/end/pause/idempotent replay) |
| `CORE-404` | WI/project/user missing, or hidden by the read gate |
| `CORE-409` | State conflict: `ALREADY_HELD NOT_CLAIMABLE DEPENDENCY_BLOCKED RACE_LOST NOT_ATTACHABLE NOT_HELD NOT_RUNNING NOT_BLOCKED SESSION_HELD DEPENDENCY_CYCLE INVENTORY_REPO_GONE OPEN_ON_BUNDLE`; without a reason: cancel of a running WI, project already exists, state changed concurrently |
| `CORE-410` | `TERMINAL_STATE`; also the cross-machine holder is deregistered / the WI has no holder machine |
| `CORE-413` | message text over 4096 bytes / artifact over 4 MiB |
| `CORE-422` | secret-shaped message content (nothing stored) |
| `CORE-429` | message anti-flood cap (60 per sender per rolling 60 s); machine replay quota: a machine filled one lane's anti-replay bucket (512 live signatures per machine id per lane, 600 s TTL); relay budget: a holder received 448, or one member sent it 320, relayed queries in the last 605 s |
| `CORE-501` | holder lacks the requested capability (`progress.query`, `artifact.read`) |
| `CORE-503` | holder did not answer a relayed query within 5 s |
| `CORE-504` | holder heartbeat stale; detail names machine + age |
| `CORE-500` | Untyped server error (default mapping) |
| `CORE-507` | Admin backup `VACUUM INTO` failed |
| `CORE-508` | Another aihub holds `service.lock` |
| `MEM-400` | Bad memory input (type kind outside `typeKinds`, bad frontmatter, client-supplied id, browse misuse: unknown `sort`, `limit`/cursor out of contract, `q` mixed with browse paging) |
| `MEM-403` | No write role; redact by a non-owner |
| `MEM-404` | Memory missing or read-denied |
| `MEM-409` | Idempotency key reused; redact of a non-active memory |
| `MEM-413` | Content over 1 MiB |
| `MEM-503` | FTS query/scan failure |
| `AUTH-401` | Missing/invalid API key |
| `AUTH-403` | Not admin; key scoped to another project; not a writer |
| `GIT-400` | Wrong smart-HTTP method/target |
| `GIT-401` | `NO_SESSION`, `BAD_CREDENTIAL`; client side "no push remote" |
| `GIT-403` | `PAUSED`: any git request while the WI is not running |
| `GIT-404` | Bad path / traversal / repo missing |
| `GIT-412` | Local staged gitlink refused by `pf` before commit (the hook rejects gitlinks with text only) |
| `GIT-500` | CGI failure; push failure in `pf` |
| `DAEMON-400` | Bad UDS request |
| `DAEMON-403` | Mutating UDS request from a step process tree (UDS peer credential — SO_PEERCRED on Linux, LOCAL_PEERCRED + LOCAL_PEERPID on darwin; fail closed) |
| `DAEMON-404` | No local job for the WI |
| `DAEMON-409` | Job-state conflict (e.g. wrap/fail of a parked WI, pause/wrap while recovering, resume-paused refused) |
| `DAEMON-410` | Daemon is shutting down; start deferred |
| `DAEMON-412` | WI's project not in the `[workspace]` mapping (pre-claim) |
| `DAEMON-414` | `.polyforge.yaml` invalid / unknown step key (pre-claim) |
| `DAEMON-500` | Daemon internal |
| `DAEMON-503` | aihub unreachable (pre-claim probe, journaled-claim verify, resume-paused); nothing claimed |
| `DAEMON-507` | Task-repo clone failure at claim (`runtime/repos.go`; the constant is named `SpawnFail`) |
| `DAEMON-516` | Re-claim capsule clone failed for a WI with published progress; local init refused |
| `DAEMON-518` | Blocked WI continued with the aihub head ≠ the sealed head, on another daemon or on the sealing one with no quarantined scene holding it (override: `pf resume <wi> --accept-lost-work`) |

`CORE-507` (`ErrCoreStorage`): the admin backup's `VACUUM INTO` failed (`core/admin.go`).

## 6. Daemon <!-- §6 -->

### 6.1 UDS routes <!-- §6.1 -->

> Retired: the chat subscriptions, `PF_CHAT_ID`, the resident `pf watch` watcher and its wake-up lines are gone — human decisions are read from the daemon's decision feed (`GET /decisions`, `pf watch decisions`) and handled in Pi Web's Polyforge panel; the current routes are [uds-routes](../../reference/uds-routes.md).

Host/Cell split (single-host multi-AIHub): one daemon process is a DaemonHost
(`daemon/cells.go`) serving one or more ProfileCells, each a full `*Daemon`
assembly run Hosted (its own runtime, observe lane, queue, tasks, hub client,
machine identity) behind the Host's one listener. The Host answers `GET /profiles`,
the profile-less `GET /status` aggregate and `POST /ops/config`, and `POST /shutdown`
(Host-level only); every other route below is served by the Cell the
`X-PF-Profile` header names (none = the default Cell; unknown or unavailable =
`DAEMON-404`, never another Cell).

`internal/polyforge/daemon/routes.go` `routes()` — 19 routes, all wrapped by `refuseStepOrigin`: every
non-read request whose peer (or its ancestors / process-group leader) carries this daemon's
`PF_WI_ID`+`PF_DAEMON_ID` markers — or whose peer credential cannot be read — is refused `DAEMON-403`
(credential capture per OS: `daemon/peercred_linux.go` reads SO_PEERCRED on Linux;
`daemon/peercred_darwin.go` reads LOCAL_PEERCRED + LOCAL_PEERPID on darwin — ok only when both
succeed and the pid is positive, else refused. Ancestry and markers are read through the runtime
proc seam `runtime/procinfo_linux.go` / `runtime/procinfo_darwin.go`; the check itself is
`runtime/stepproc.go` `StepProcessOrigin`). The guard
lets exactly two step writes through, and both are reachable ONLY from a step process of this daemon
(resolved from the peer's process tree, every other caller refused `DAEMON-403`): `POST /step/memories`
(`pf remember` inside a step, §7.4) and `POST /step/result` (`pf result submit`, §7.3); with
`GET /step/memories/search|{id}` they are the step face, not listed below.

| Method | Path | Does | Caller |
|---|---|---|---|
| POST | `/jobs/{wi}/claim` | enqueue + claim + start (priority, models, origin); `{"resume":true}`: claim + start now — the continue of a blocked WI after the CLI landed the hub door (DAEMON-503 → queued, answered `deferred`) | `pf submit`, `pf resume` |
| POST | `/jobs/{wi}/answer` | deliver an answer to the work item's driver (no `question_id` → the single pending one) | `pf answer` |
| POST | `/jobs/{wi}/resume-paused` | live holder re-attaches a paused WI in place (`attached`/`already_live`/`closed`/`parked`) | `pf resume` |
| POST | `/jobs/{wi}/pause` | operator pause (clean_pause) | `pf pause` |
| POST | `/jobs/{wi}/wrap` | terminal close with verdict | `pf wrap [--fail]` |
| GET | `/status[?harnesses=1]` | machine id, auto, max, workspaces, jobs, queue; harness probe | `pf ops status`, `pf doctor`, `pf daemon ensure` |
| POST | `/ops/config` | auto-pull switch, max concurrent jobs | `pf ops auto on|off`, `pf ops max N` |
| POST | `/shutdown` | async shutdown (§6.2) | `pf ops shutdown` |
| POST | `/chat/init` | **retired** — was: harness session → chat id; the interactive supersession move is now `POST /machine/role` (`pf ops role interactive --force`) | ~~`pf chat init`~~ |
| GET | `/jobs/{wi}/artifact?path=<relpath>` | read a capsule `artifacts/` file (relative, no `..`/absolute/symlink escape, ≤4 MiB; queued/terminal = pointer); step peer refused DAEMON-403 | `pf watch <wi> --artifact <relpath>` |
| POST | `/messages/{id}/analyze` | the explicit read-only analysis as ONE task-lane task (body `{caller_id}`; `steps.<project>.task` models, read-only tool profile, fixed 5 min / 100000 tokens, caller as reporter, no analyzer lock — the lane's two slots bound it); blocks until the task ends and answers `{message_id, task_id, project, type, summary, suggested_actions, model, duration, reply_draft?}` only to the caller; slot-full DAEMON-409, no chain DAEMON-400, unmapped DAEMON-412, failed/capped/interrupted DAEMON-500; step and task peers refused DAEMON-403; no loop starts it | `pf msg analyze <id>` |
| POST | `/messages/{id}/prompt` | the explicit read-only message-to-prompt task, built like analyze (same lane, project choice, read-only profile, fixed 5 min / 100000 tokens, caller as reporter, no lock); its input is the message, ≤10 most recent earlier messages of its conversation, the replied-to message and the anchored work item; blocks until the task ends and answers exactly `{prompt, task_id, model, duration}`; slot-full DAEMON-409, no chain DAEMON-400, unmapped DAEMON-412, failed/capped/interrupted or blank report DAEMON-500; step and task peers refused DAEMON-403; no loop starts it | `pf msg prompt <id>` |
| GET | `/jobs/{wi}/watch?after=N[&demand=1]` | per-WI snapshot + events; a WI running/blocked on another fresh machine is resolved by a signed 5 s progress query and the relayed snapshot carries `holder_machine` (stale/deregistered/unsupported answer CORE-504/410/501); the daemon serves a relayed snapshot from a memory-only per-WI cache for 10 s (≤64 WIs, oldest evicted), keyed on the WI's aihub `status` and `updated_at`, so remote progress refreshes at that TTL and any aihub-visible change relays afresh; a failed relay is never cached, and a WI that is local, queued, paused or terminal never reads the cache; `execution.declared_steps` lists the steps of the capsule's pinned workflow in its workflow.yaml's order as `{"id", "on_fail"}` (`on_fail` the gate's rewind target, else `null`), omitted when that workflow.yaml is missing or unparseable. `demand=1` (a marker, exactly the value `1`; any other value is unmarked) marks the request as human-side demand for the summarize lane (the per-WI memory-only lane wired into this route): honored only from a HUMAN-side peer — the UDS peer credential available and not a step process tree of this daemon — a step-peer or uncredentialed marker is ignored, never an error, the route stays a read. The snapshot carries the lane's narration beside `progress`: `summary` (the last finished line), `summary_error` (the raw error text of the latest failed call, never faked), `summary_age_sec` (present only with a summary) and `summary_capped` (present and true only while the per-dispatch cap is reached) — narration, never authority; absent = silent. An eligible marked request on a WI whose current step dispatch is live and running (the current step in_progress with a live step process, not parked on a question — a needs_input, paused or between-steps WI starts no call, however watched) may start ONE background summarize call (never waited on: the route answers immediately with the pre-call state; no call starts while the previous one is in flight) | `pf watch <wi> --once [--demand]`, pi-web |
| GET | `/chats/{id}/subscriptions` | **retired: the decision feed (`GET /decisions`) replaced the chat subscriptions** — was: list; a subscription owner routing added to a chat that did not submit the WI carries `origin_chat` and, while that origin chat holds a subscription to the WI, shares its delivery record (answered with its `cursor`/`notified`/`done`; acks land there — a decision is delivered once for both chats). It is answered `passive` (progress only, no decision line, no ack) while the origin chat's watcher is live; otherwise the lowest-id live chat sharing the record is the fallback and prints only what nobody delivered. A WI with no origin chat, or one the origin chat unsubscribed, wakes every subscribed chat from its own record (polyforge#199) | `pf watch` |
| POST | `/chats/{id}/subscribe`, `/unsubscribe` | **retired: the decision feed (`GET /decisions`) replaced the chat subscriptions** — was: manage subscriptions; an unsubscribe also mutes the WI for the chat (persisted in `chat-subs.json`; subscribe-owned skips it until a subscribe, an adopt, or a submit/resume claim from that chat) | `pf watch subscribe|unsubscribe` |
| POST | `/chats/{id}/ack` | **retired: the decision feed (`GET /decisions`) replaced the chat subscriptions** — was: cursor advance | resident `pf watch --chat` |
| POST | `/chats/{id}/subscribe-owned` | **retired: the decision feed (`GET /decisions`) replaced the chat subscriptions** — was: `{"owner"}`: subscribe the chat to every held WI it submitted or `owner` owns (origin owner, else reporter) that it has no subscription for and has not muted; answers the added ones (cursor just before the WI's newest decision event); the call also marks the chat live (in memory, for one minute; a chat not seen since the daemon started counts as live for its first minute) — liveness only picks who prints a decision nobody has delivered yet | resident `pf watch --chat`, every poll |

Socket: `--socket` > `PF_DAEMON_SOCKET` > cli.toml `daemon_socket` > `daemon.sock` in `$POLYFORGE_DATA_DIR`, else `~/.local/share/polyforge` (`cli.DataDir`; the same default for `pf` and the daemon, so `-data-dir` does not move it)
(`cli/config.go`, `daemon/main.go`).

### 6.2 Lifecycle <!-- §6.2 -->

- **Start = `pf daemon ensure` only** (`daemon/ensure.go`). Called by `tools/install.sh`, the Claude SessionStart hook (`--timeout 15s`) and the pi `pf-chat` extension. Socket answers → exit 0. Refuses inside a step (`PF_WI_ID` set). `<data-dir>/ensure.lock` (flock) serializes callers; losers wait and answer by the socket. Canonical argv: `--config`, `-machine-id`, `-data-dir`, `-socket`, `-workflows-dir <data-dir>/release/current/workflows`.
- **tmux loop** (loop script in `daemon/ensure.go`; `internal/polyforge/supervisor` only names the session and the systemd unit): session `polyforge-daemon`; loop runs `nohup pf daemon …`, appends to `<data-dir>/daemon.log` (rotated at 10 MiB); exit 0 → loop ends; **exit 78** → loop ends ("needs an operator fix"); anything else → restart after 5s. No tmux → one unsupervised start with a warning. `pf doctor` also accepts a self-managed `polyforge-daemon.service`.
- **Daemon environment = an allow-list** (`supervisor/env.go`, the one definition): ensure gives the daemon ONLY `PATH`, `HOME` (pinned), `USER`, `LOGNAME`, `SHELL`, `LANG`, `LC_*`, `TZ`, `TMPDIR`, the proxy vars (`http_proxy`/`https_proxy`/`no_proxy`/`all_proxy`, both cases), `SSL_CERT_FILE`/`SSL_CERT_DIR`, `npm_config_registry`, the XDG base dirs (`XDG_RUNTIME_DIR`, `XDG_CONFIG_HOME`, `XDG_DATA_HOME`, `XDG_STATE_HOME`, `XDG_CACHE_HOME`), the Go toolchain (`GOPATH`, `GOROOT`, `GOPROXY`, `GOPRIVATE`, `GONOSUMDB`, `GONOPROXY`, `GOFLAGS`, `GOTOOLCHAIN`, `GO111MODULE`, `GOCACHE`, `GOMODCACHE`), the task-repo Git low-speed thresholds (`GIT_HTTP_LOW_SPEED_LIMIT`, `GIT_HTTP_LOW_SPEED_TIME`), the docker client's engine selection (`DOCKER_HOST`, `DOCKER_CONTEXT`, `DOCKER_CONFIG`, `DOCKER_CERT_PATH`, `DOCKER_TLS_VERIFY`), `POLYFORGE_DATA_DIR`, `GOCOVERDIR`, the marker `POLYFORGE_SUPERVISOR=tmux:polyforge-daemon` and the loop shell's own `PWD` — values from the CALLER of ensure, never the tmux server (whose environment is whatever shell first started it). The loop script runs `exec /usr/bin/env -i <assignments> /bin/sh -c <loop>`; the no-tmux fallback sets the same list as `cmd.Env`. Names that are not `^[A-Za-z_][A-Za-z0-9_]*$` are never carried (they would be shell syntax in the script); empty values are kept. No `ANTHROPIC_*`, no `PF_*` (ensure's own config load still honours them; the daemon reads `cli.toml`), no retired `POLYFORGE_*`, no harness-session markers. Steps see only this environment: pi provider keys belong in `auth.json` / literal `models.json` keys. Adding a variable is one line there; never a credential. `GET /status` reports the daemon's env NAMES (`env_names`, never values); `pf doctor`'s `daemon env` row warns on any `ANTHROPIC_*` or credential-looking name (`…TOKEN`/`…KEY`/`…SECRET`/`PASSWORD`) outside the list, with the clean restart (`pf ops shutdown`, wait, `pf daemon ensure`), and on any `$NAME` the step agent's `models.json` interpolates (`pi_models_env_refs` in `/status`) that is off the list.
- **Exit 78** = `ExitNeedsOperator` (`daemon/main.go`): startup failure only an operator can fix — config, identity mismatch, no `[workspace]` mapping, workspace dirs, on-disk state, failed restart reconciliation. Fix, then `pf daemon ensure`. After a machine/container reboot the daemon is down until the next
  interactive harness session runs ensure (auto-pull idles meanwhile); the skill retries an
  execution-plane verb once after `pf daemon ensure` when the daemon is unreachable.
- **Shutdown** (`daemon/shutdown.go`; `pf ops shutdown`, SIGTERM/SIGINT; a second signal aborts hard): crash-equals-shutdown — every state change is already durable, so the shutdown only stops work and saves the task repos' uncommitted work: latch shutting-down (new starts `DAEMON-410`; in-flight claims defer, nothing is waited for) → cancel the loops → per live job, in parallel: terminate its agent tree (10s, then up to 10s more waiting for the old driver Run to report done), then snapshot its task repos to `refs/pf-wip/<name>` — all under one **30s** deadline; the snapshot is skipped while that Run is not proven gone (it may still write the repos), and the next daemon start reaps it → persist state once → close listener. The cause is one stderr line (`daemon: shutdown: cause=…`). Running WIs are **not** session-ended: they stay `running` at aihub with `clean_pause=false` and are attached on the next start. At every step end, after the step's process exits and before its result is written, the step-end hook first runs the L4 `PF_WI_ID`+`PF_DAEMON_ID` env-marker sweep (`runtime/cleanup.go`) so a child that left the step's process group (`setsid`/double fork) dies with its step instead of at the WI's end, then takes the same WIP snapshot, bounded by **30s**: on a slow or hung hub it can delay a step's result by up to 30s (`v2-converge.md#§14.3`). The driver dispatches one step at a time per WI, so no successor runs during the sweep, and a dispatch's wall ends when its process exits, so this step-end cleanup is not booked into the step's cumulative T. There is no `pf daemon restart`: restart = `pf ops shutdown` then `pf daemon ensure`.
- **Restart reconciliation** (`runtime/restart.go`): Restart asks aihub nothing — it seeds every state.json record and claiming-journal entry into the job table (before anything can persist) and posts one message per work item to its actor, so the socket serves at once. Each actor reaps its work item's orphans, then reconciles: a claiming-journal entry first (its session verified: match → the same claim replays; otherwise the entry is dropped), then the record against a session verify — the fence every step passes before it relaunches an agent, pushes under the session or sends a SessionEnd: match + a pending terminal close (a retained paused record holding a wrapped/failed/blocked stop_reason) while the hub shows running or paused → the close replays, never a resume (polyforge#197); match+paused+`clean_pause=false` → attach and resume; match+running → recover-resume (a recorded close finishes, a pending pause replays); mismatch → release with no hub transition (kill, quarantine, drop, `session_released`; a terminal or blocked hub state just drops the record; a driver Run that outlives its teardown parks the release — no quarantine, no drop next to it — as `close_parked` kind `release`, finished once that Run exits); transport error → keep the record as it is and retry with backoff (the chain is bounded by `EndRetryWindow`, 24h: `retry_exhausted`); typed 4xx → on a record already closing (`end_pending`, `sealing`) the refusal re-runs nothing: the record is parked in that state (`session_verify_outage`) and never retried; any other record takes the ordinary blocked close once (`session_verify_outage`, resting `end_pending` with `close_refused` if that blocked close is itself refused); and a typed 4xx from a paused record's replayed close (its terminal close or pending pause, verify matching) is parked `close_refused` and never retried. Missing capsule / unmapped project blocks only that WI (`capsule_missing`, `project_unmapped`). Reaping (`runtime/reap.go`): spawn-journal pids + a `PF_WI_ID`+`PF_DAEMON_ID` environ sweep (layers in `runtime/cleanup.go`: PPID snapshot → process-group kill → (pid,starttime) re-kill → env-marker scan → cwd scan warns only). Decision record: `v2-converge.md#§6`.
- **Recovery engine** (`runtime/recover.go`): kill the tree, commit dirty state, then relaunch the in-process driver as a fresh derivation (spawn_n advanced; the driver continues from `execution.json`; the old resume→fresh cascade is gone, polyforge#106); after 2 consecutive failed recoveries (`MaxRecoveries`) the WI ends blocked. A kill whose old driver Run outlives its bounded teardown parks the WI: it stays RECOVERING with the live handle in place (never a second writer on `execution.json`), an owner-visible `recovery_parked` event records it, and recovery re-enters by itself as soon as that Run exits (the wait is a goroutine, not the actor); the persisted RECOVERING record also resumes at the next daemon start, where no old Run exists. A close meets the same live Run the same way (`parkClose`): a wrap/fail seal, an operator pause or a bounded-fail blocked verdict whose teardown leaves the Run alive commits, pushes, saves WIP and seals nothing next to it — the record keeps the state the close already persisted (SEALING, PAUSED, or the recovering record a crash there would leave), a `close_parked` event records it, the close doors and `pf resume` answer DAEMON-409 naming the park, and the close finishes by itself once that Run exits (a daemon restart finishes it too). (Within a step, the driver's own retry shape still resumes the transcript first — §7.4.) A daemon restart re-dispatches the same model and is not counted as a failure.

### 6.3 Configuration <!-- §6.3 -->

- **cli.toml** `$XDG_CONFIG_HOME/polyforge/cli.toml` (default `~/.config/polyforge/cli.toml`; `cli/config.go`). Flat keys `server api_key daemon_socket user_id claude_bin`; env overrides `PF_SERVER PF_API_KEY PF_DAEMON_SOCKET PF_USER_ID` (`PF_CHAT_ID` is no longer read). Unknown sections / flat keys are load errors.
- **`[daemon]`** keys exactly `step_timeout stall_window harness_root operator_agent_dir max_concurrent_jobs` (quoted Go durations, absolute paths; `parseDaemonTable`). The daemon reads the table itself from `--config`; precedence explicit flag > table > default (`daemon/main.go` `applyDaemonTable`). `pf ops max N` sets the cap until the daemon restarts (never persisted; `daemon-state.json` holds only the auto switch).
- **`[workspace."<path>"]`** `projects = [...]` — the only source of capsule placement (no workspace flag/env). A project maps to exactly one workspace; zero sections → exit 78; each workspace dir must exist and `capsules/` be writable (`internal/polyforge/mapping` builds the plan, `daemon/daemon.go` checks the dirs at start). Migration runbook: `08-capsule-base-migration.md#§2`.
- **Daemon flags** (`cli.DefineDaemonFlags`, parsed by `daemon/main.go`; `pf help daemon` renders them): production-relevant `--config -socket -server -api-key -machine-id -data-dir (POLYFORGE_DATA_DIR, default ~/.local/share/polyforge) -workflows-dir -claude-bin (tests/dev; production source cli.toml claude_bin) -harness-root -operator-agent-dir (default ~/.pi/agent) -max-concurrent-jobs (4) -step-timeout (90m) -stall-window (10m)`; test/dev-only `-display-name -pi-bin -git-remote-base -recall-topk -auto -silence-window -tool-window -model-error-grace -revalidate-every -heartbeat-every`. Retired `POLYFORGE_HARNESS_ROOT` / `POLYFORGE_OPERATOR_AGENT_DIR` only warn.
- **`[summarize]`** (`cli/config_tables.go`): one machine-level table `models = [candidates]` (mandatory, as strict as a step entry's list), `language = "<text>"` (optional; trimmed, non-empty, single-line free text inserted verbatim into the prompt; absent = `中文`), `interval_sec = <int>` (optional; 10-3600 inclusive, never clamped; absent = 60; the 20-call per-attempt cap stays fixed) — the demand-gated summarize lane's model candidates for every mapped workspace (polyforge#224/#242/#243/#250); absent table = the lane silently off. Any other key, a missing/empty `models` or an unprefixed candidate refuses the cli.toml load (file and line named). The daemon serves it from its loaded snapshot (`liveconfig`, class live; `pf config set` / `pf config reload` swap it).
- **`[memory_policy."<path>".<default|project>]`**: per mapped workspace path, `record = [...]` / `skip = [...]` quoted-string arrays; the path must be a mapped `[workspace]` key and the entry `default` or a project of that path. Served from the daemon snapshot (class next-claim; written into the claim's capsule). Arrays may span lines (a comma inside quotes is data). `pf config list` is the one read face of both families; `pf config set|unset` writes them (`summarize.*` flat, `memory_policy.<entry>.record|skip` with `--workspace`/`--cwd`); `pf config migrate` moves the retired yaml sections once (plan-then-apply, refuse-don't-pick, per-file backups).
- **`.polyforge.yaml`** at the workspace root (`internal/polyforge/workspace/workspace.go`): closed grammar `steps.<project>.<default|step-id>.models: [candidates]` (and `.attrs`); `steps.<project>.summarize` is an unknown step-id key like any other. A retired root `summarize:` or `memory_policy:` section (moved to cli.toml above) refuses the whole file naming the line and `pf config migrate` (no dual read). Strict: unknown keys, `harness` (`RetiredHarnessHint`), the old top-level `models:`, unmapped projects, and step keys that are not installed step ids are errors → `DAEMON-414` before claim; `pf doctor` reports the same. Read fresh before every claim.
- **Auto-pull**: `pf ops auto on|off` + `pf ops max N` (`v2-converge.md#§5`); with auto on the daemon pulls each mapped project's ready queue; accepted submits beyond the cap queue in `submit-queue.json` and replay after restart (the file holds each submit's intent — work item, priority, enqueue/submit time, models, origin; the DAEMON-503 backoff is not persisted and starts over).

### 6.4 Data dir and events <!-- §6.4 -->

Default `~/.local/share/polyforge`. File inventory: `v2-converge.md#§E` (E.4), plus `git/askpass/`
(one-shot credential helpers, swept at start; `gitops/gitops.go`). Events: one JSONL line per event in
`events/wi-<id>.jsonl` (one file per work item, `events/daemon.jsonl` for daemon scope; per-WI seq
monotonic across restarts; a file silent for 30 days is removed at start; `observe/events.go`); kinds and emitters in E.1. Execution state authority is always the capsule's
`runtime-state/execution.json`; events are news.

## 7. Execution <!-- §7 -->

### 7.1 Bundles and materialization <!-- §7.1 -->

- Shipped bundles: `workflows/{develop,integrate,micro,release}` (the four lanes `develop` superseded — feature-full, bug-fix, chore, autopilot — removed, `workflows/_archive/README.md`; `micro`, `fix` → `review` → `deliver`, no verification step — the verification-less delivery shape, §7.3); shared step files in `workflows/_shared/steps/` (today `deliver.md`, `verification.md`, `review.md`; `{{name}}` placeholders rendered from the workflow.yaml step entry's `vars` (or `vars_files`), `01-workflow-skills.md#§4.4`). `_shared/principles.md` holds the generic engineering principles; a step definition's `{{principles}}` renders it, inside a `## Principles` section that opens with the step's own usage paragraph — shipped in the grill/spec/plan/code-like steps, the shared review, and the long-running steps that carry the waiting rule — bug-fix `repro`, feature-full `e2e-author`, the shared `deliver` and release `verify` (`01-workflow-skills.md#§4.4`). Other `workflows/_shared/<name>.md` files are shared fragments: a block several step files repeat (the per-unit fast-check bar, the grill batch-question table, the handoff duty, the plan's deliverable-carrier check `deliverable_carrier`, the plan and build steps' owner-review declaration `owner_review` — the `owner-review:` line deliver's `deliver-owner-review` gate reads before merging) lives once there and a step file includes it with `{{<name>}}` — no `vars` entry, rendered verbatim by `workflow.ReadStepDefinition`, fail-closed on a placeholder with neither a var nor a fragment. Repo-specific rules live in each repo's `AGENTS.md` / `CLAUDE.md`, which those steps read first. `_shared` is not a bundle (no `workflow.yaml`; claiming it is refused).
- Step file lookup `workflow.StepDefinitionPath` (`internal/common/workflow/workflow.go`): the bundle's own `steps/<id>.md` wins, else `_shared/steps/<id>.md`. Do not share by symlink — materialization skips symlinks.
- Source: the daemon's `-workflows-dir` (production `<data-dir>/release/current/workflows`). At claim the lane's `workflow.yaml` is read from that checkout; then `materializeBundle` (`runtime/materialize.go`) copies the bundle to `<capsule>/workflows/<bundle>/` and `_shared/` beside it (less `_shared/scripts/tests/`, the scripts' own unit tests) in the freshly cloned capsule, writes each step's definition rendered as the driver reads it (`workflow.RenderDefinition`) to `<capsule>/workflows/_rendered/<step>.md`, the copy a build sub-agent reads, marking completion in `sync/materialized.json` (recovery relaunches only over a snapshot that marker vouches for). A missing bundle warns and continues. No digest check (deleted, polyforge#149).
- The driver loads the materialized copy: `driver/workflow.go` `LoadWorkflow` → `validateWorkflow`. Authoring guide: `01-workflow-skills.md#§4`.

### 7.2 The lane file: workflow.yaml <!-- §7.2 -->

A lane is `workflows/<lane>/workflow.yaml` (workflow contract 1): one YAML
mapping the one lane loader (`internal/common/workflow`, `workflow.Load`)
judges — its keys, their values, the rules across them and the error codes
are [the workflow contract reference](../../reference/workflow-contract.md),
and `workflows/_schema/workflow-1.schema.json` is its JSON Schema, generated
from the contract registry. The lane's prose is its `README.md`, which the
daemon never reads; a lane that is still a `workflow.md` (the lane file of
the earlier releases) is refused with its rewrite (`WFC-101` alone, `WFC-110` beside a
`workflow.yaml`). The step definition files `steps/<id>.md` and the skills
are the reference's too.

### 7.3 Step dispatch and the result envelope <!-- §7.3 -->

- One harness process per dispatch, cwd = capsule. The step delivers **one** JSON envelope with `pf result submit <json|-|@file>` — the only delivery path the prompt names (polyforge#214; hand-writing the drop is not described, and a helper that cannot deliver ends the turn without a result, the exited-without-result recovery lane). The agent supplies `{outcome, output}` (needs_input: `question_id` + `question`; `command_evidence` when the step owes it); the daemon's `POST /step/result` (`daemon/stepresult.go`) resolves the calling step from its process tree like `/step/memories`, refuses a caller-supplied `wi_id`/`step_id`/`spawn_n` that disagrees and the light shape defects (`driver.CheckEnvelopeShape`: JSON, outcome closed set, `output` an object, the needs_input question pair) with `DAEMON-403`/`DAEMON-400` writing nothing, stamps `wi_id, step_id, spawn_n, submitted_at` itself and lands the envelope atomically at `runtime-state/pending-results/<spawn_n>.json` — the caller's cwd and any harness sandbox have no say. Outcomes `completed | gate_failed | needs_input | incomplete` (`driver/result.go`). The driver collects the drop (refusing a non-empty `wi_id`/`step_id` of another dispatch, `ErrIdentityMismatch`, or a stale `spawn_n`) and archives it to `artifacts/steps/<step>/s<spawn_n>/result.json`; every deeper check stays in its accept gate below.
- Validation order (`ValidateStepResult`): shape; identity + spawn_n fence; needs_input question shape; gate verdict binding (completed with `verdict: fail` → `gate_failed`); `requires_human_answer`; `defer` verdict only on `deploy`; output schema (enforced on `release`/`deploy`, record-only elsewhere); `review repair-target classes (every `blocking` review finding — completed or `gate_failed` — carries one `repair_target` of `code`/`authored-suite`/`spec`/`declared-obligation`/`instruction`; a missing or unknown class is an unfilled result and an envelope rejection; every class but `code` is off-graph; `code` covers every repair the build step can make in its own round — updating a declared obligation the diff left unchanged, withdrawing a moot one, citing an undeclared file — and `declared-obligation` is reserved for an obligation that is itself wrong and that the build step can neither satisfy nor withdraw); `needs_code_change` only as a boolean on a light re-entry (`ResultSpec.LightRewind`; a non-boolean value, or the field with no light rewind preceding it in the step's ledger, is an envelope rejection); the fix's conditional commit minimum (a completed `fix` carrying `root_cause_addressed` with an empty or absent `commits` list is an envelope rejection unless the report is the literal `false`); the plan's coupled obligations (a completed `plan` declaring a write without a non-empty `coupled_obligations` list of `{path, note?}` or exactly one `{none: true, reason}` is an envelope rejection); command evidence (a list of objects on any step, else rejected; on evidence-owing steps argv non-empty, `duration_ms>0`); committed test files; pre-existing-failure boundary; authored tests unchanged; deliver binding. A rejection is retried in-process with the reason; the 3rd consecutive rejection (`maxEnvelopeRejects`=3) blocks the step `result_invalid`; the count and the ordinal it last counted are persisted on the step, so a restart neither resets nor double-counts the bound. The count is kept on the step rather than re-derived from the archived `s<n>/result.json` envelopes: those record every delivered envelope but not whether the driver already judged and counted it, so a re-derivation would have to replay validation over old rounds and could count one twice; the persisted ordinal is exact. Details: `v2-converge.md#§2.6`.
- A gate's `fail` hand-off → bounded rewind (`driver/transition.go` `applyGateFailLocked`): no `on_fail` → `failed_gate`; else `[target, gate)` is archived and reset (the target as `gate_fail_rewind`, the steps strictly between it and the gate as `superseded_by_rewind`, both with `rewind_gate`) and the position moves back to the target, which — a non-gate step — re-enters as a resume of its previous round's conversation and keeps its candidate pointer (the superseded steps and a gate target reset theirs and start fresh). The gate is neither archived nor reset: it stays `in_progress`, records the audit-only `gate_fail_verdict` attempt, opens its next round and re-enters in a new conversation (`closeGateRoundLocked`). The rewind lands on `rewind_stats` as `charged` (counted toward the lane's `max_rewinds`), or as `owner` (counted toward nothing) when the gate handed it off citing the owner (`--per-owner`). A spent budget parks nothing: once `max_rewinds` automatic rewinds landed, `pf step handoff` refuses a gate's `fail` that does not cite the owner (`driver.RewindBudgetSpent`, `daemon/stephandoff.go`), so the gate asks the owner first (`pf step ask`) and hands off as the reply says — `fail --per-owner` (one more round), `waive --per-owner` on a `waivable: true` gate (`driver/waive.go`: the gate accepted on the waiver, no rewind, the next step; the lane's last agent step re-runs in place instead) or `cancel --per-owner`. The FAIL's findings are the gate's section of `artifacts/handoff.md`, which every step reads first (`workflows/_shared/step-header.md`); the owner's words stay in `runtime-state/asks.jsonl` and each hand-off's record in the call ledger `runtime-state/step-calls.jsonl` — `execution.json` copies no verdict, owner ruling, waiver or termination reason.
- The MECHANICAL light variant (polyforge#260), same single write and the same accounting as the text-only one (counted toward `max_total`, no edge slot, its record flipped to charged by a `needs_code_change` self-report): a `verification`-gate FAIL on a lane whose step entry declares `mechanical_fix: true`, whose verdict output carries `mechanical_bound` (one entry per failed check — `check`, `repo`, `command`, `files`; the classification is the verification agent's own, never the driver's), archives the target as `gate_fail_rewind_mechanical` with the mechanical light re-entry note (fix only inside the bound — modifications of bound-named files, ≤ 40 lines / ≤ 5 files hard-coded — or self-report `needs_code_change: true`, the self-report's same write also abandoning the marker), and records `steps.verification.pending_light_recheck` (`{bound, diff_base}`: the bound copied verbatim + the FAIL-settlement repo HEADs). Every write that opens a fresh re-derivation of the build step abandons the marker in that same write (`abandonLightRecheckLocked`): a fresh (non-light) gate FAIL whose range does not reset verification (a structural review FAIL, review running first), a light re-entry's `needs_code_change` self-report — whichever light cause rewound it (after a mechanical FAIL a text-only review FAIL keeps the marker, so the self-report that follows is the write that abandons it) — and the fix's first `root_cause_addressed: false` (its range `[root-cause, fix)` never reaches verification). Each archives the escalation's audit-only `light_recheck_escalated` attempt on verification (the reason, the marker's facts and `abandoned: true` — the flag that keeps the unchanged-tree guard judging the full run after it), clears the marker and emits one `light_recheck_escalated` event after the write's own rewind event; a text-only review FAIL keeps it. The marker makes the gate's next dispatch the LIGHT RECHECK (`driver/lightrecheck.go`): pre-dispatch, the driver validates the bound's coverage (every `fail` check covered exactly; every bound repo carrying a diff base) and measures the repair diff (M-only, bound-named, within the caps); a malformed classification or broken bound — or a recheck that does not pass (its own FAIL verdict, or command evidence not showing every bound command run with exit 0 in its repo) — escalates IN PLACE (audit-only `light_recheck_escalated` attempt with the reason in `detail` and the audit facts — bound, diff base, measured diff, recheck, `escalated: true`, reason — in its `result`, marker cleared, next dispatch fresh, no rewind slot, one `light_recheck_escalated` event) to the existing full verification, whose prompt carries the escalation note (the shared material's `Escalated full re-run (the mechanical fast lane)` section, `workflows/_shared/mechanical-fix.md` — it owes the fast-lane section of `artifacts/verification-report.md` the light recheck never settled to write). A light pass settles `mechanical_pass` (bound, measured diff, recheck commands/results, `recheck_heads`, `escalated: false`) + the FAIL verdict's `test_bar` carried verbatim, NO `verified_heads`; deliver then covers the tests ONLY by the merge dispatch's recorded re-run of the carried `test_bar` (unconditional), its hand-made-history walk anchored on `recheck_heads`.
- Retired (with the bug-fix lane; the driver branch is gone): the fix→root-cause rewind (`applyRootCauseFalseLocked`): a completed `fix` with `root_cause_addressed: false` on a lane declaring `root-cause` before it (bug-fix) is routed before acceptance — the first time (no `fix → root-cause` ledger record) `root-cause` is archived `root_cause_unaddressed` (`rewind_gate: fix`) and reset, one charged `fix → root-cause` record is appended (count increments; neither bound refuses it), the fix's `summary` becomes root-cause's `pending_rewind_input`, and `fix` stays `in_progress` (re-dispatched fresh after the new diagnosis); a later false appends nothing and parks on the driver-authored `root_cause_false` question (`needs_input` event; the answer re-dispatches `fix` in place) — never `failed_rewind_exhausted`. Root-cause's `on_fail: repro` is untouched; autopilot is untouched. Its siblings (bug-fix dead ends): a completed `repro` with `reproduces: false` parks `repro_not_reproduced`, and a second root-cause FAIL on the root-cause→repro edge parks `root_cause_inconclusive` instead of a second charged rewind — answers `re-run-repro`/`continue`/`cancel`, a re-run from root-cause being an owner-routed rewind.
- The plan-conformance facts (`driver/planconformance.go`): a `review` dispatch on a lane with a done `plan` step, and a `verification` dispatch whose entry declares `plan_conformance: true` (chore), carry the driver-computed `# Plan conformance (driver-computed facts)` prompt section — the plan's declared set (writes, their coupled obligations, the build step's finding-cited `declared_extensions`, cumulative over its rounds archived since the plan's acceptance plus the live output (a carried citation renders marked), each `{path, source, ref}` counted only after a rewind record from its source's gate — `review` (`s<n>#<k>`), `verification` (`s<n>`), `deliver` (a question id, the human answer declaring the file outright) — to the build step and only with a well-formed ref; the mirror `withdrawn_obligations` withdraws a cited moot obligation, and an obligation with no declaring write in the diff lapses — both not blocking, an uncited withdrawal blocks as before; and the test files an earlier authoring step declared in its own `output.test_files` and committed — e2e-author/repro; never changed-but-undeclared facts) against each task repo's committed diff from the merge base with the origin default branch, read outside the lock. Severities: changed but undeclared and uncited = blocking; declared coupled obligation unchanged = blocking; declared plain write unchanged = non-blocking. An unresolvable base or git failure is named in the section, never rendered as an empty set. Nothing is persisted.
- The review coverage record: a PASS review's settlement writes `steps.review.output.reviewed_heads` (each task repo's HEAD at the pass, driver-written in the same atomic write — the `verified_heads` pattern; a FAIL verdict records nothing, and a step-supplied value is deleted). The deliver merge rule requires, on a workflow with a review, every delivered repo a reviewed head with only git-reproducible merges between it and the delivered HEAD — a plain commit after the pass, or a missing record, blocks the automatic merge and routes the delivery to a human; a re-verification that does not move HEAD keeps the pass covered. On a workflow without a review step (`spec.HasReview` false: autopilot) no review head is owed — verification's `verified_heads` stands in, and the deliver contract's review-coverage bullet states that carve-out. An already-merged repo whose HEAD is not the verified commit is offered `acknowledge` (the merge must contain the verified head and the owed bar re-run pass; the round's answer completes `merged: true`, never asked twice); a MERGED PR followed by "back to code" delivers the new commits as exactly one follow-up PR on the same branch, while an OPEN PR is always reused.
- The unchanged-tree guard: every gate verdict records the task-repo tree it judged in `steps.<gate>.judged_repo_hash` (driver-written in the same settlement write; `reposTreeHash` covers `repos/**` only, so the driver's own `artifacts/steps/` archives never move it). When that verdict was delivered is derived from the attempts ledger — the gate's newest `accepted` attempt, or the `gate_fail_rewind*` archive naming it in `rewind_gate` (never a light re-entry's `needs_code_change` self-report archive: the gate delivered nothing then, it only re-routed the earlier verdict) — so no second timestamp is persisted, and the comparison is inclusive of a verdict archived in the same write as the rewind. The guard applies only when the rewind's target is the lane's BUILD step (`buildStep`: the verification entry's `build_step` var, else its `on_fail` target, never a hard-coded id); an artifact-producing target (spec, repro, the deliver override that re-runs verification at full through `rewind_to: verification`) is exempt. A retry inside such a rewound round whose current repos tree still equals the rewinding gate's judged tree made no code progress, so the continue-in-place wording is suppressed (the round's reference — since the previous round's transcript and `summary` — stays beside the FAIL verdict, `driver/spawn.go`). When the rewound build step's accepted result leaves that identical tree — crashed attempt or not (D3) — a gate re-entry over it is not dispatched: `driver/blockUnchangedGateTree` parks the workflow `blocked` with a `blocked_reason` stating the build round produced no change and naming the gate, the rewinding gate and its verdict round (`review_rounds[<gate>]`), a bounded excerpt of the build step's `summary`, and the crashed attempt when an `infra_failure` was archived after the rewind; it starts no full verification/review and charges no rewind slot; the owner's `pf resume` clears the gate steps' judged trees so the explicit override may run the tail once. A metadata-only plan-conformance repair is the exception: on a lane with a done plan step, the guard recomputes the facts the gate last judged — the same diff, folding the build round the FAIL's rewind archived (the earliest build rewind archive at/after the verdict time) as the live output and the older archives as carried — and re-runs the gate on the same tree only when those judged facts carried a blocking entry (a changed-but-undeclared file or an unchanged coupled obligation) and the gate's current recomputed facts carry none; the input the gate would judge changed even though the tree did not, and the metadata-only round charges no rewind slot. A live `declared_extensions` citation is not the test (the list is cumulative across rounds, so a later no-change round re-emits it); facts that still block, or facts the gate already judged clean, keep the park. The light recheck and its in-place escalation are exempt (the designed same-tree fast lanes, polyforge#260); a light rewind's resumed build round is not, nor is the full verification after a light recheck that a fresh re-derivation abandoned (S2: its `light_recheck_escalated` audit record carries `abandoned: true`, and a fresh build round came between).
- `needs_input` → question stored in `execution.json`; a human answers with `pf answer` → daemon `/jobs/{wi}/answer` → the driver (`applyAnswerLocked`). Never answered by an agent (UDS step-origin refusal).
- The fast-lane guard (`driver/fast_lane.go`, `guardFastLane`): on a lane with a `deliver` step and no `verification` step (the shape, never a bundle name — the shipped `micro`), before the step right after the build step is dispatched and once the build step is done, the driver judges every task repo with commits beyond its merge-base with the fetched origin default branch: the governing declaration is the `.polyforge/verify-scope.conf` at that base (`git show`, never the working tree), its `micro_path=` lines the opt-in (`*` within one segment, `**` across `/`, no wildcard = exact; `AGENTS.md`/`CLAUDE.md` by basename never in bounds), every changed path — both sides of a rename or copy — must match one, each repo against its own declaration. The facts (git outside the lock) land in ONE `TransFastLane` write onto the build step's output as the driver-written `fast_lane` key (`{in_bounds, reason, repos[{name, base, changed_paths, declared_paths, commands[{repo, command}]}]}` — `commands` = the build step's reported `evidence` for the repo followed by every declared `micro_check=` it omitted; an identical recorded verdict is not re-written). A refusal — a path out of bounds, a touched repo declaring no `micro_path`, an unreadable base/conf/diff, an empty plan — is terminal in that write: `workflow_status: failed_gate`, top-level `termination_reason` naming the paths and the `autopilot` re-file, event `fast_lane_out_of_bounds`; no rewind is charged, no step reset, nothing dispatched, no question asked. The merge rule of that shape (`deliver_facts.go` `fastLaneMergeBlocker`, keyed on `ResultSpec.NoVerification`): the review passed with no blocking finding and its `reviewed_heads` cover the delivered HEAD, the delivered head ties to the local HEAD on the latest default branch, no LLM-resolved conflict, no reported test failure, and this deliver dispatch's own command evidence re-ran every `fast_lane` command of the repo in that repo with exit 0 inside the re-run window (after its last `git merge`/`git pull` there, before its `gh pr merge`) — never a daemon-recorded bar run, `verified_heads`, a test bar, a scope or a scenario record; anything else routes to the human question. Every lane with a `verification` step keeps its merge rule unchanged.
- A step's `blocked` hand-off → workflow `blocked`, its `blocked_reason` the hand-off's reason (`driver/handoff.go` `StepBlockedReason`). Workflow statuses: `running completed failed_gate blocked`; step statuses `pending in_progress done` (`common/capsule/capsule.go`). A terminal workflow closes the WI (completed → `wrapped`, failed_gate → `failed` with no hub note, blocked → hub `blocked` with `runtime/recover.go` `blockedWorkflowNote`) after a final seal push; if that push fails the close degrades to blocked, carrying the local chain head as `head_sha` so the blocked seal guards the unpushed commits (`v2-converge.md#§D`).
- `execution.json` (`capsule.Execution`; the daemon's private progress record — the driver is its single writer and no step or script reads it): `capsule_id`, `work_item_id`, `current_step`, `workflow_status`, `steps` (per id: `status`, `candidate` (the `<harness>:<model>[:effort]` string of the current/next dispatch — the step's one model fact, `harness.ParseCandidate` derives the model id and effort; written at step_start and at the failure that decides a switch, cleared by a rewind of a gate or of a step between target and gate — a resuming rewind target keeps it; polyforge#33/#34), `step_started_at`, `round` (the step's round ordinal, `PF_ROUND` on every dispatch of the round), `budget_consumed`, `attempts[]` with `cause` `accepted` (a waived gate's carrying the waiver's detail)/`gate_fail_rewind`/`gate_fail_verdict` (audit-only, on the gate itself: the dispatch whose FAIL rewound the workflow — `result` = its verdict, `consumed_budget` = its window; no `rewind_gate`)/`superseded_by_rewind`/`intent_retry` (a rewind --to itself citing the owner: the same step again, same round)/`infra_failure` and `failure_cause` plus `tree_hash` (the capsule tree hash recorded on live and `daemon_restart` `infra_failure` attempts, which a new-conversation re-dispatch compares against the current tree to choose the continue-in-place fast path) plus `candidate` (the dispatch an infra_failure attempt records), `supervision` counters and relaxed bounds (and `resumed_at`, the owner's last continue: a new streak era for the ledger-derived streaks, `next_dispatch`), `dispatch` (the derivation ordinal whose result the step awaits), `start_heads` (the task repos' HEADs when the round opened: the hand-off stamp's start commits)), `rewind_stats` (`{count, rewinds[]}`, each record `{gate, to_step, class}` — `charged` counts toward the lane's `max_rewinds`, `owner` (a gate's fail or a rewind --to citing the owner, the latter with `by`, `at` and `reason`) toward nothing; `count == len(rewinds)`), `budget_consumed`, `updated_at`, `version` (bumped on every write; the push loop keys on it), `blocked_reason` (a step's own `blocked` hand-off, or the driver's own authority: a host-class fault, a failure bound, a dispatch-time candidate composition failure — an unreadable yaml/bundle, DAEMON-414 —, a lane snapshot of another workflow contract (`driver/contractblock.go`), a cancel request; the runtime's blocked close carries it in the hub note, bounded to 500 runes — `runtime/recover.go` `blockedWorkflowNote`), `cancel_requested` (top level: the durable intent of a `cancel --per-owner` hand-off — the owner message's stamp, the owner's words with the agent's reading, the owner's identity, the stamp — written with `workflow_status: blocked` in the same write; the runtime's blocked close, once the hub took it, calls the hub's existing blocked→canceled door with it, so the closed status vocabulary and the hub's state machine — no running→canceled — are unchanged; a cancel that does not land after the settled close leaves the work item blocked for `pf cancel`, and the owner's resume clears the intent). A step's output, the waivers and a failed workflow's reason are not in it: a hand-off is its section of `artifacts/handoff.md` and its record in `runtime-state/step-calls.jsonl`, and the wrap writes the waivers to the work item's `attrs.waivers` from that ledger (`runtime/runtime.go` `wrapAttrs`).

### 7.4 Context between steps <!-- §7.4 -->

`inherit` steps get the body of the nearest earlier `handoff: true` step's `artifacts/handoff-<id>.md`
(producer must be done; a missing file only omits the section); `fresh` steps get none
(`driver/spawn.go`). The claim-time memory snapshot `runtime-state/recalled-memories.json` is rendered
into every step prompt as non-binding history (§9) — one `[type] title` line per memory (no ids or
bodies; a blank title falls back to the excerpt's first non-empty line), id-only noise titles
(`^Verification Report — \S+$`, `^Controller-sink artifact`) dropped, at most 10 lines, no section when
none is left (`driver/spawn.go`). A step whose entry declares `recall: true` (the decision/authoring
steps; never a review/verification/deliver gate or a release step) is also asked for
`pf recall --any` with 2–3 phrasings at the start (`workflows/_shared/memory.md`); only the workflow's
last step writes a memory, whatever it is,
the work item's one `experience.wrap` summary guided by the project's memory policy from
`runtime-state/memory-policy.json` (`workflows/_shared/memory-wrap.md`). Step agents hold no
aihub credential path; memory access is daemon-mediated: inside a step (`PF_WI_ID` and `PF_DAEMON_ID`
set) `pf recall` / `pf memory show` read through `GET /step/memories/search|{id}` and `pf remember`
posts synchronously to `POST /step/memories` (one of the step-origin guard's two step-allowed writes, §6.1), all
scoped to the one work item whose journaled step process is the caller's ancestor (never the caller's
own env) and its project (`daemon/stepmem.go`). The journaled step process is recognized by its journaled
pid + `started_at` (the process's kernel start time, stamped at spawn — `runtime.processStartStamp`, converted as the proofs convert it, so a slow stamp cannot break the match; live, start time matching — `runtime.JournaledProcessLive`), never by its environment:
darwin omits an entitled agent's environment from `kern.procargs2` (polyforge#234). The write accepts `experience.wrap` only, ≤ 2 KB, a
title, no credential value (`internal/common/stepmem`, shared with the CLI), and creates it with the
aihub idempotency key `pf-step-memory:<wi>:<step>:<attempt>`; aihub down answers DAEMON-503 and the
prompt says a failure never fails the step. Every other aihub verb, `pf doctor` and `pf daemon` are
refused there (DAEMON-403). `cli.toml` stays readable under the shared HOME: a guard against accidents, not malice. Retry shape: first retry resumes the transcript,
later retries start fresh with the old transcript as read-only reference (`v2-converge.md#§2.3`); a
later retry whose capsule tree hash matches the hash recorded on the step's latest failed attempt **and which runs on the same claim** continues
in place instead of starting from scratch (a mismatch, a missing hash **or a re-claim** keeps the from-scratch reference). In a rewound round
of the lane's build step the fast path is additionally suppressed when the retry's task-repo (`repos/**`) tree still equals the tree the rewinding
gate judged (`judged_repo_hash`) — no code progress — so the from-scratch reference stays beside the FAIL verdict (criterion 5;
an unknown judged hash or a repos-hash error keeps the wording above).

### 7.5 Model candidates <!-- §7.5 -->

- Format `<harness>:<model>[:<effort>]` (`internal/common/harness/harness.go` `ParseCandidate`): harness ∈ `pi`, `claude`, split at the first `:`; the last segment is an effort only if it is one of `off minimal low medium high xhigh max` (Claude accepts `low`…`max`), otherwise it belongs to the model id. Effort → pi `--thinking`, claude `--effort`.
- Precedence, first **present** level wins whole (no merge; `workspace/compose.go`): (1) `pf submit --models a,b` (this WI, every step; daemon-local `manual-models.json`, dropped at a terminal close, not carried to another machine) > (2) `.polyforge.yaml` `steps.<project>.<step-id>.models` > (3) bundle step `model_candidates` > (4) `.polyforge.yaml` `steps.<project>.default.models` > (5) bundle `defaults.model_candidates`. Composed at every dispatch (polyforge#33: the driver's compose closure re-reads the yaml and bundle, so an edit governs the next dispatch; nothing is snapshotted in the job record; an unreadable yaml/bundle blocks the WI with DAEMON-414 and `pf resume` continues; the step's model is located by identity in the live list, a removed one → the new head; the manual list stays frozen). The summarize lane's settings are cli.toml's `[summarize]` table, served from the daemon's loaded snapshot at every lane request (`liveconfig.State.Summarize`; `language` default `中文`, `interval_sec` default 60). Two `ended_without_result` attempts on one candidate (`driver.EwrRotationThreshold`; neutral causes skipped, accepted result / rewind / another counted failure reset) rotate like a model error; `candidate_switched` carries cause `config_changed` or the rotating death cause.
- Advancement (`driver/candidates.go`): the pointer is an identity anchor (the persisted `Step.Candidate`, the model of the current/next dispatch, located in the live list; empty or removed → the head) plus the pending advancement the newest non-neutral attempt owes, only when that attempt ran the anchor (a model-class failure — `model_hung`, `step_timeout_slow_model`, `exit_model_class`, or a `model_error` that is the second consecutive model-class death on that candidate — or the second `ended_without_result` on one candidate), capped at the last entry; host-class failures block; other causes retry the same model; `rate_limited` / `upstream_transient` retry the same model with backoff, and consecutive `model_error` / `upstream_transient` / `ended_without_result` deaths on one candidate wait 1m, 3m, then 9m before a re-dispatch of that candidate (a switch dispatches at once; each candidate's ladder starts at 1m). Full cause table: `v2-converge.md#§2.3`.

### 7.6 Harness adapters <!-- §7.6 -->

| | pi (`runtime/spawn.go`) | Claude Code (`runtime/claudespawn.go`) |
|---|---|---|
| argv | `pi --mode rpc --no-skills --no-context-files --no-prompt-templates [--session <transcript>] [--model] [--thinking]` | `claude -p --safe-mode --setting-sources user --settings {"enabledPlugins":{"cc-plugin-agents-md@builtin":false}} --output-format stream-json --verbose --include-partial-messages --input-format stream-json --session-id <uuid>`/`--resume <uuid> [--model] [--effort] --tools/--allowedTools Bash,Read,Edit,Write,Glob,Grep,TodoWrite,Agent --permission-mode dontAsk --strict-mcp-config --disable-slash-commands …`; one profile in every auth mode: its `system/init.tools` equal the `--tools` list, which `--bare` (polyforge#223's token-mode flag) cut to Bash, Edit and Read; only `<harness-root>/claude-config/settings.json` is read as settings, re-read at every spawn (fresh and resume) — a file that exists but is unreadable, not JSON or without an `env` block refuses the spawn; the prompt is the first stream-json user message on stdin, which stays open for the dispatch's later messages (one process per dispatch across many turns; turn boundaries are the stream's `result` records; the daemon's teardown closes stdin and terminates) |
| env | `PF_WI_ID`, `PF_DAEMON_ID`, `PF_DAEMON_SOCKET` (the daemon's own socket, so a step's `pf` reaches it), `PI_CODING_AGENT_DIR=<harness-root>/step-agent`, `PI_CODING_AGENT_SESSION_DIR=<harness-root>/step-agent-sessions/<dashified-capsule-dir>` (the capsule's own dir — pi writes sessions flat into it, and the live-transcript locator scans exactly it); all other `PF_*` stripped | the same `PF_*` markers and socket; `CLAUDE_CONFIG_DIR=<harness-root>/claude-config`, `CLAUDE_CODE_DISABLE_BACKGROUND_TASKS=1`, `CLAUDE_CODE_ENABLE_TASKS=false` (TodoWrite exists only with Claude Code's Tasks family off), `CLAUDE_CODE_HARBOR_KITE=0` (no cross-session messaging inbox: no socket, no `CLAUDE_CODE_MESSAGING_*` in the Bash env; a claude task and a summarize dispatch carry it too), `DISABLE_AUTOUPDATER=1`, …; no credential injected; every inherited `ANTHROPIC_*`, `CLAUDECODE`, `CLAUDE_CODE_*`, `CLAUDE_CONFIG_DIR` stripped (the settings.json `env` block is the only credential/endpoint source), and so is an inherited copy of any other name the adapter sets (e.g. `DISABLE_AUTOUPDATER`), so each adapter name appears once with the adapter's value |
| transcript | live file located in the session store, copied verbatim to `session/session-<token>.jsonl` at commit boundaries (`runtime/pitranscript.go`) | `<claude-config>/projects/<dashed cwd>/<uuid>.jsonl`; resume only if it exists |
| subagents | the subagent extension's children run `--no-session`; the driver records each child's conversation from the step's stdout (`tool_execution_update`/`_end` of `subagent`) into `session/<transcript stem>/subagents/<tool call id>-<i>.jsonl` + `.meta.json`, live; a child is finished only at the call's `tool_execution_end` (a chain step also once the next starts) and its `isError` is its own (`driver/subagentrec.go`; a child conversation past the 4 MiB stdout record bound stops being recorded — Pi Web then takes the ended call's ok/error) | `<claude-config>/projects/<dashed cwd>/<uuid>/subagents/agent-<id>.jsonl` + `.meta.json`, copied at the same syncs as the main session beside the session's first capsule transcript (`runtime/subagents.go`) |

Provider errors (pi, `supervise/pi.go`): a `message_end`/`turn_end` whose message has `stopReason=="error"` is an error signal; an `agent_end` whose last assistant message ended in error and `willRetry=false` ends the turn as failed (rule ① / D7 classification in `v2-converge.md#§2.3`); `willRetry=true` means pi retries itself and the turn is not judged.

`<harness-root>` = `[daemon] harness_root`, default the data dir. The pi step-agent dir is a minimal
copy of the operator agent dir (`auth.json`, `models.json`, `agents/`, `extensions/subagent/`; never
`settings.json`), refreshed before every spawn, pruned at start; a configured operator dir without
`auth.json` refuses start (`runtime/stepagentdir.go`; rationale `v2-converge.md#§2.7`).

### 7.7 Supervision bounds <!-- §7.7 -->

Rules and causes: `v2-converge.md#§2.3`, bounds `v2-converge.md#§2.4` (code `internal/polyforge/supervise`,
`driver/supervision.go`). Defaults: X silence 10m token-level / 30m message-level; Y tool 30m; T step
90m cumulative (`[daemon] step_timeout`); N driver stall 10m (`[daemon] stall_window`); model-error
grace 1m; per-step failure bound 3 (`DefaultMaxStepFailures`). Timeouts relax (double) and persist in
`execution.json`. The 10s → 5 min backoff is the **push** loop's (§8), not a step bound.

## 8. Capsule and git chain <!-- §8 -->

- Layout: `v2-converge.md#§E` (E.5), constants `internal/common/capsule/capsule.go`; `pending-results/` and `sync/` are defined in `driver/spawn.go` and `runtime/materialize.go`. `.gitignore`: `/repos/`, `/artifacts/release-*/`, `/artifacts/downloads/` (raw downloads), `/sync/` — written at every claim, rewritten at every recovery. A file over `capsule.MaxBlobBytes` never enters the chain: the commit guard leaves it out and lists it in `artifacts/oversize.json` (`gitops/oversize.go`). The capsule's git-dir is external (`<data-dir>/git/<wi>.git`, a `gitdir:` pointer file in the capsule): it is outside the capsule tree, but a step's own `git commit` in the capsule root still reaches it and is published as-is.
- Lifecycle and retention: a claim materializes the capsule at `capsules/<name>` and it stays there through every state, terminal ones included — no close path deletes it. Only a re-claim, a supersede or a claim-close that finds a scene at the path renames it aside (`capsules/<name>.quarantined.<ts>`) together with its external git dir (`<wi>.git.quarantined.<ts>`), all-or-nothing, so the bytes survive. The only polyforge path that deletes or slims a capsule scene is `pf doctor --prune-capsules` (the `capsule retention` row), which acts on a terminal work item's entries past the 14-day retention: a `wrapped` work item's capsule, live or set aside, is slimmed to `artifacts/` + `capsule.json` (everything else in it, its `.git` pointer included, is removed), a `failed` or `canceled` one's set-aside scene and external git dirs are removed, and a live capsule of a work item that is not `wrapped` is never touched. It refuses a capsule whose `repos/*` task clones hold uncommitted changes or commits neither the clone's own nor the mapped workspace's `.repo/<name>` clone's remote-tracking refs carry (read-only, no fetch — a missing object or an unreadable view keeps it), keeping the work item's git dirs too. Manual removal outside polyforge must first archive each task repo's unpushed branches (`git bundle create --branches --not --remotes`) and its `refs/pf-wip/*` snapshots: a bare `rm` loses exactly what no remote carries.
- Chain rules (FF-only, no ref deletion, blob ≤16 MiB, no gitlinks), push fencing, push retry schedule, re-claim = clone + task-repo restore, `DAEMON-516/518`: `v2-converge.md#§14.1`. Carrier refs `refs/pf-task/<name>`: `v2-converge.md#§14.2`. Uncommitted-work snapshots `refs/pf-wip/<name>`: `v2-converge.md#§14.3`. The pre-receive hook (`git/hook.go`) is **fail-closed**: `set -u -o pipefail` and an explicit error check on every pipeline/command — a failing check (blob pipeline `rev-list`/`cat-file`, gitlink `rev-list`/`ls-tree`) rejects the push instead of reading an empty result as clean; the proxy caps every smart-HTTP request body at the 1 GiB constant `capsule.MaxGitRequestBodyBytes` (≥10× the carrier blob limit; declared over-cap → `GIT-400` before CGI; chunked over-cap → cut at the cap, the proxy awaits the copy's verdict before answering — http-backend's response headers precede its stdin read, so the verdict cannot be polled off them — the CGI rejects the truncated pack and cleans up itself, proxy answers 4xx, refs unchanged, no residue).
- Local invariants (`internal/polyforge/gitops/gitops.go`): one per-WI git mutex shared by runtime and observe; the chain push is bounded by its caller's context (on its end the push's git process group is killed and the lock released); `index.lock` older than 10m removed at acquisition; local gitlink precheck (`GIT-412`) before the commit and over every commit about to be pushed (an agent's own commit included); a commit a step made on the chain branch is pushed as an ordinary fast-forward commit; an amended already-pushed commit is non-fast-forward and needs a human.
- Push loop (`observe/observe.go`, `observe/monitor.go`): event-triggered on each `execution.json` commit; a failed Version is retried with exponential backoff (10s doubling to 5 min; one `push_failed` per attempt, one `push_backpressure` when the delay reaches its cap); a new Version is pushed at once (only a success resets the delay); 60s per push.

## 9. Memory and recall <!-- §9 -->

Detail: `06-memory-search.md#§1`. Summary (`internal/aihub/memory`): a memory is one `memories` row whose
`content` column holds the document (frontmatter + body; `content_sha`/`content_len` describe it) plus an
FTS5 trigram row (`memory_fts`). Create = one transaction (row + FTS); redact = one transaction (status +
FTS removal; the document stays, never served); no memory file is written or read while serving.
On start, `MigrateContent` fills `content` from the
former memory files for rows that lack it (file bytes verbatim; a file differing from `content_sha` wins;
a missing file leaves the row unserved and is retried next start; the files are not deleted). Search needs `q`;
`match=all` (default) ANDs every token (≥3 runes as FTS phrases, shorter ones as substrings);
`match=any` ORs up to 32 filtered terms (words, plus 3-rune shingles of CJK runs) ranked by bm25 and
examines at most max(100, 10×limit) candidates (rules in `06-memory-search.md#§5.1`); filters, limit
and the in-SQL project narrowing are the same in both modes (single entry `Store.SearchMode`). The
caller's readable project set (key scope ∩ membership, `Server.readableProjects` — one membership
query per request, nil = no narrowing for an unscoped admin, an empty set = nothing readable) is
pushed into the candidate SQL (`projects []string`, IN-narrowed), so unreadable matches cost no
per-candidate membership lookup; the private/admin **visibility tiers** stay in the application
layer (`memVisible`/`memAllowed`).
Both modes order by bm25 weighted by per-type freshness: `rank × (1 + 0.5 × exp(-age_days/stability))`,
except that a `doc.module-map`'s revisions (same `<repo>/<module>` title key) rank at their newest revision's place, newest first
(`06-memory-search.md#§5.1`);
age from `updated_at`, stability rule.*/doc.design/doc.decision none, other doc.* (review, audit) 180d,
experience.* 90d, fact.*/methodology.* 180d, else 90d
(`memory/freshness.go`). A memory written without a title gets one derived from its body's first line
(row only, at create time). CLI (`cli/verbs_aihub.go`): `pf recall --project P <query> [--any]` (project must
be mapped; `--any` = `match=any`), `pf remember --project P --type T <content|-|@file>`, `pf memory show <id>`. At claim the
daemon writes `runtime-state/recalled-memories.json` (query = goal + labels, `match=any`, project-scoped, top 10,
1200-rune excerpts; empty list + notice on error or no hits; never fails the claim — `runtime/recall.go`,
`daemon/memface.go`). Deploy aihub before daemons: an older aihub ignores `match` and ANDs, which
leaves the snapshot empty. `pf backup` is a SQLite snapshot holding every memory, without git repos (§4.1).

**Browse mode and the viewer.** The same search route also browses: an empty or absent `q` answers a page of the caller's readable active memories instead of `MEM-400` — descending `updated_at` (default) or `created_at` via `sort` (a keyset walk: the answer carries an opaque `next_cursor` whenever more entries remain, `after` continues strictly after the last returned entry so a write between pages duplicates and skips nothing, a malformed or sort-mismatched cursor is `MEM-400`, past the end is an empty page; `limit` is strict 1..200, default 20). The same filters and read gates as keyword search apply (`type` exact/`X.*`/`*`, `tags` all, `status='active'`, the readable-project narrowing inside the SQL — no per-candidate membership query — plus the visibility tiers), and a non-empty `q` carrying `after`/`sort` is `MEM-400`, so the keyword path is unchanged and cursor-free. Over the same store sits the read-only web console's memories module — the `/web/api/memories` face behind Basic auth (the api key as the password, the username ignored; browse/search list with type and tags filters, sanitized-Markdown detail; §4.1's route row). A step or task agent reaches neither: it holds no aihub key and the daemon's memory proxies forward no browse parameter and no `/web/` route.

## 10. Install, release, deploy <!-- §10 -->

The script headers are the contract; this section only points.

- **Work machine**: `tools/install.sh` (header) — `read -rs PF_KEY` then `curl … | bash -s -- "$PF_KEY" [server-host]` (the key never lands in shell history; host = argument 2, else `PF_HOST`, no built-in default; required only for a first install — no cli.toml with an `api_key` — and refused before any change without it; an upgrade needs none); stages the SHA-verified `pf`, checks out `<data-dir>/release/<ver>`, then under `ensure.lock` (waits up to 120s, then proceeds without it) shuts the daemon down gracefully when a new binary was staged (refused while jobs run unless `PF_INSTALL_FORCE=1`), swaps `/usr/local/bin/pf` and the `release/current` symlink, keeps an existing cli.toml that has an `api_key` (else writes it), installs the skills, runs `pf daemon ensure` (skipped when the daemon already answers or there is no `[workspace]` mapping / workflows dir), verifies. Upgrade = re-run it. Skill install: Claude one `~/.claude/skills/<name>` symlink per top-level `release/current/skills/*/SKILL.md` (today `polyforge` and `output-format`) + one SessionStart hook in `~/.claude/settings.json`; pi one local `packages` entry naming `release/current` (`package.json` declares both skills and `extensions/pf-chat.ts`). `pf doctor` checks both (`cli/doctor_layout.go`).
- **Release**: `.polyforge/release.sh` (header) — `[patch]` (the default; `minor`/`major` are refused) or `--verify <tag>`; clean tree at `origin/main`, PR-only history since the last tag (bar the committed owner-direct author allowlist, polyforge#240), `make test`, `make release`, docker image `ghcr.io/silgrid/polyforge-aihub:<tag>`, tag push (the version claim), assets + `gh release create`. Run by the `workflows/release` bundle (`release → verify → deploy`, no rewind).
- **Deploy (aihub host only)**: `.polyforge/deploy.sh` (header) — `<version> --confirm <version>`; `AIHUB_HOST`, `AIHUB_DATA`, `COMPOSE_FILE`, `COMPOSE_PROJECT`, `DEPLOY_LOCK_FILE` required (no defaults; from the environment or the operator's untracked `~/.config/polyforge/deploy.env`, environment wins; missing → exit 2 naming them, before any network work, `--dry-run` included), server-side lock, image via `docker save | ssh docker load`, online backups, seconds-long switch window applying `deploy/sql/<tag>/{upgrade,rollback,probe}.sql`, automatic rollback (exit 10). Schema history and operator steps: `docs/upgrade.md`.
- Workspace layout helper: `pf init [<dir>]` (`cli/verbs_init.go`) — `scratch/` and the managed CLAUDE.md section; mapping stays a cli.toml edit.

## 11. CLI verbs and the routing skill <!-- §11 -->

`internal/polyforge/cli/cli.go` `commands`: aihub-direct `file queue cancel recall remember memory msg`
(`msg analyze` and `msg prompt` alone go to the daemon); daemon `submit answer pause resume rewind wrap ops chat
watch task`, plus the in-step `result` (`pf result submit`, the step's envelope; `verify-run` and `revise-binding`
were removed with the retired lanes); admin `users api-keys machines projects backup`; local configuration `config`
(`cli.toml`'s one writer, then a daemon reload) and `workflows` (the installed bundles, read-only JSON);
`doctor`, `version`, `init`, `daemon` (`daemon ensure`; `cmd/pf/main.go` routes `pf daemon …` to
`daemon.Main`), `aihub` (`cmd/pf/main.go` routes `pf aihub …` to `aihub.Main`). Exit codes: 0 ok, 1 client-class (4xx, validation, transport, a sub-action's flag error), 2
server-class (5xx, unknown verb, a global or verb flag parse error). The skill (`skills/polyforge/SKILL.md`) routes the 16 routing verbs (the data-plane and execution-plane verbs above minus `chat` and `result`) + doctor/version/help/init,
one fragment per verb under `verbs/` (`ship.md` only says delivery is the workflow's `deliver` step;
`followup.md` is the session-only follow-up ledger lifecycle);
`pf help` is the quick reference. Progress/notification design: `v2-converge.md#§3`.

## 12. History <!-- §12 -->

Archived (`archive/`, 2026-10-16): `00-architecture`, `02-aihub-core`, `03-execution-capsule`,
`04-local-runtime`, `05-sync-revision-recovery`, `07-deployment-pilot`, `v1-architecture-spec`,
`v1-spec-full`, `v1.5-session-model`, `v1.5-api-contract`, `v1.5-change-coverage-rules` (its rules now
live in [docs/contributing.md](../../contributing.md)). They describe the v0/v1 design (WI Agent, owner
generations, Revision/CAS sync, interactive/batch/drain modes, `pf upgrade`, …). Live mechanisms they
held were moved here or into `v2-converge.md` §14 / Appendix E. Lessons: `v0-lessons-ledger.md`.
