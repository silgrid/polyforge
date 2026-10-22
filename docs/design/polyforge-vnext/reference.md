# Polyforge reference — the current system

> What exists in this tree (v2.14.0 line), derived from the code; every claim names its source file.
> **The code is the authority**; where this file and the code disagree, fix this file. Decisions and
> their rationale live in [v2-converge.md](v2-converge.md) (decision authority); the
> parent/child/bundle contract in [wi-kind-contract.md](wi-kind-contract.md); bundle authoring in
> [01-workflow-skills.md](01-workflow-skills.md); memory in [06-memory-search.md](06-memory-search.md);
> capsule base migration in [08-capsule-base-migration.md](08-capsule-base-migration.md). The v0/v1
> design set is in [archive/](archive/) — history only, do not build from it.
> Anchors use `<!-- §N -->`; `tools/check-xrefs.py` (run by `make check`) resolves every backticked
> name.md#§N reference.

## 1. System overview <!-- §1 -->

Core = **task** (aihub work items) + **memory** + **workflow** (bundles). Two binaries, one skill:

| Component | Code | Role |
|---|---|---|
| aihub | `cmd/aihub`, `internal/aihub/{server,core,memory,git}` | Single HTTP service on SQLite: work items, projects, users, API keys, sessions, memories, and a git smart-HTTP proxy holding one bare repo per WI (the capsule chain). |
| `pf` CLI | `cmd/pf`, `internal/polyforge/cli` | Data-plane verbs talk to aihub over HTTP (`internal/polyforge/aihubclient`); execution-plane verbs talk to the local daemon over a unix socket (`internal/polyforge/cli/daemon.go`). |
| daemon | `pf daemon` → `internal/polyforge/daemon` | One per machine. Claims WIs, owns capsules, hosts the driver and the supervisor in-process, serves the UDS face, heartbeats to aihub. |
| runtime | `internal/polyforge/runtime` | Job records, claim/attach/end, materialization, recovery engine, restart reconciliation, process reaping. |
| driver | `internal/polyforge/driver` | Runs one WI's workflow: dispatches one short-lived harness process per step, collects result envelopes, advances `runtime-state/execution.json`. |
| supervisor | `internal/polyforge/supervise` (rules), `internal/polyforge/observe` (monitor, events, push loop) | Judges a step dead / the driver stalled, records events, pushes the capsule chain. Never advances execution (`v2-converge.md#§2`). |
| capsule | `internal/common/capsule` | Per-WI working directory `<workspace>/capsules/<project>-<seq>/`, a git worktree whose history is the WI's chain on aihub (§8). |
| harness adapters | `internal/common/harness`, `internal/polyforge/runtime/spawn.go`, `runtime/claudespawn.go` | pi (`--mode rpc`) and Claude Code (`-p --bare` in token mode, `-p --safe-mode` in OAuth mode) step processes (§7.6). |
| watcher | `internal/polyforge/watcher` | `pf watch`: human progress + decision notifications (`v2-converge.md#§3`). |
| summarize lane | `internal/polyforge/summarize` | The demand-gated transcript-delta narration lane behind `GET /jobs/{wi}/watch?demand=1` (polyforge#224): per-WI memory-only state, the live-dispatch (A11) / growth / interval+in-flight / cap gates, the one-shot harness dispatch (the prompt on stdin), the on-failure candidate walk. |
| routing skill | `skills/polyforge/` (`SKILL.md` + `verbs/*.md`, `extensions/pf-chat.ts`, `hooks/claude-session-start.sh`) | The only install face in the user's harness: maps intent to a `pf` verb; never parses or runs a workflow (§11). |

Trust model: team-internal; harnesses are logged in by a human; the daemon never manages credentials;
no heavy isolation (#135 frozen). Guards exist against **accidents** (wrong push target, skipped human
gate, unreviewed release, agent driving the daemon) — `v2-converge.md#§0`.

### 1.1 Glossary <!-- §1.1 -->

- **WI** — work item (aihub row). Slug `<project>#<seq>`; `seq` is unique per project.
- **bundle** — a workflow: `workflows/<name>/workflow.md` + `steps/<id>.md` (§7.1). A WI with `bundle_name` is an *execution* ticket; without, a *registration* ticket; with children, an *epic* (derived, not stored — `wi-kind-contract.md#§1`).
- **session** — the claim lock: a daemon-minted secret whose hash aihub stores (§3). Not a chat.
- **chat** — `PF_CHAT_ID`, a notification label for an interactive harness session; never a credential (`v2-converge.md#§4`).
- **capsule** — the per-WI directory + git chain (§8). **task repo** — a project repo cloned under `repos/<name>/`.
- **spawn / spawn_n** — one step process dispatch and its per-WI monotonic counter.
- **step agent** — the harness process running one step. **operator** — the human.

## 2. Work items <!-- §2 -->

### 2.1 Statuses and transitions <!-- §2.1 -->

Statuses (`internal/common/api/types.go`): `queued running paused blocked wrapped failed canceled`
(single-l `canceled`). Terminal: `wrapped failed canceled` (`internal/aihub/core/workitem.go` `isTerminal`).
PATCH never sets status (`patchableFields`). Every transition is one guarded UPDATE in one transaction.

| Transition | Door (route → function) | Guard / effect |
|---|---|---|
| — → queued | `POST /work-items` → `WIManager.Create` | writer on the project; `repo_inventory` frozen at create (names only, must be project repos — `core/inventory.go`) |
| queued → running | `POST …/claim` → `core/session.go` `Claim` | §3.2 |
| running → paused | `POST …/session-end` `status=paused` → `Pause` | must be running (`NOT_RUNNING`); session kept; `clean_pause` set atomically; event `session_paused` |
| paused → running | `POST …/session-attach` → `Attach` | hash mismatch → `CORE-403 STALE_SESSION`; not paused or `clean_pause=true` → `CORE-409 NOT_ATTACHABLE`; event `session_attached`; operator unpause = PATCH `clean_pause:false` |
| running/paused → wrapped/failed/blocked | `POST …/session-end` → `End` | hash-fenced; clears the whole session row; wrapped/failed set `closed_at`; blocked sets `waiting_on='owner'` and, with `head_sha`, `attrs.blocked_seal` (§3.5); event `session_ended` |
| running/paused → queued | `POST …/force-end-session` → `ForceEnd` | admin or project maintainer; reason required; terminal → `CORE-410`, no session → `CORE-409 NOT_HELD`; clears session + seal; event `session_superseded` (old fingerprint). CLI: `pf ops end-session <wi> --reason …` |
| blocked → queued | `POST …/resume-blocked` `{accept_lost_work?}` → `ResumeBlockedWith` | reporter/maintainer/admin; terminal → `CORE-410`; status blocked (`NOT_BLOCKED`) ∧ no session (`SESSION_HELD`); event `wi_resumed`; `accept_lost_work:true` also clears `attrs.blocked_seal` in the same transaction with event `blocked_seal_abandoned` `{head_sha, machine_id}` (§3.5). CLI: `pf resume <wi> [--accept-lost-work]` |
| queued/paused/blocked → canceled | `POST …/cancel` → `Cancel` | reporter/maintainer/admin; running → `CORE-409`; terminal → `CORE-410`; event `work_item_canceled` |
| queued/paused/blocked → wrapped/failed (no bundle) | `POST …/wrap` `{fail?, note?}` → `Wrap` | reporter/maintainer/admin; terminal → `CORE-410 TERMINAL_STATE`; session held → `CORE-409`; bundle bound → `CORE-409` (the daemon wraps execution tickets); a child not terminal → `CORE-409 OPEN_CHILDREN`; sets `closed_at`; event `work_item_closed` `{status, note?}`. CLI: `pf wrap <wi> [--fail] [--note …]` when the daemon is unreachable or has no local job for it |

The blocked WI has exactly two exits: `pf resume` (continue = resume-blocked + a fresh claim) and
`pf cancel` (abandon) — `v2-converge.md#§6`. `end_pending`, `recovering`, `sealing`, `claiming` are
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
not authorize). One transaction (`core/session.go` `Claim`), checked in order: not terminal
(`CORE-410 TERMINAL_STATE`), no session (`CORE-409 ALREADY_HELD`), queued (`NOT_CLAIMABLE`), no
unfinished blocker (`DEPENDENCY_BLOCKED`), bundle present (`NOT_CLAIMABLE`); then
`UPDATE … WHERE status='queued' AND session_hash IS NULL` (0 rows → `CORE-409 RACE_LOST`); a declared inventory repo that left the project → `CORE-409 INVENTORY_REPO_GONE`. Response `{claim, work_item}`
(`api.SessionClaimResponse`; `claim.repos` = the inventory-filtered project repos; `sealed_head` /
`sealed_machine` from the blocked seal). Idempotency is keyed on the session secret (an `idempotency_key` an older
daemon sends is ignored): a repeat by the session that holds the WI answers the same claim, re-read from the row, with no
write; a secret whose `session_claimed` event exists but that no longer holds the WI (force-ended,
superseded, ended) → `CORE-403 STALE_SESSION` before any gate, so it never claims again.

### 3.3 Verify, attach, end <!-- §3.3 -->

- `session-verify` `{session_id}` → `{work_item_id, matches, status, current_fingerprint, clean_pause}`; compare-only, no state change. Used at restart, by the claiming-journal replay, and by the M4 revalidation (§3.5).
- `session-attach` `{work_item_id, session_id, machine_id}` → same shape as claim; same-session resume of a paused WI (restart reconciliation, `pf resume` of a paused WI via daemon `POST /jobs/{wi}/resume-paused`, `v2-converge.md#§7`).
- `session-end` `{session_id, status: wrapped|failed|blocked|paused, note, clean_pause, head_sha}` (`api.SessionEndRequest`). Hash mismatch (or a lost race on the guarded UPDATE) → `CORE-403 STALE_SESSION`.

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
- **end_pending** (`runtime/runtime.go` `pendingClose`, `restart.go` `closeBlocked`; retried by the work item's actor, `actor.go` `retry`): a failed `SessionEnd(blocked)` is retried (pushing the local chain first) until aihub accepts it or verify shows the session is no longer held; a permanently rejected chain push is recorded on the job record (`end_push_gave_up`) and the retry, restarts included, closes without it.
- **Runtime actors** (`runtime/actor.go`): one mailbox and one goroutine per work item (by canonical id). Every operation that changes one work item's job — a Start's claim and settle, a close, a pause, `pf resume`, a recovery, a supersession, the restart's reconciliation and its retries — is a message to that work item's mailbox; messages run one at a time, network steps included, so a second operation of the same work item queues and gets the final answer (no `DAEMON-409`/`DAEMON-503`), while no door of another work item waits. A Start probes on the caller's goroutine and claims on the canonical id's actor, so two spellings of one work item meet in one mailbox. The capsule clone and materialize run on the actor with the job CLAIMING; a close meanwhile cancels the clone before it queues. Every pending hub step (a close the hub has not taken, a verify, a claim replay) retries through one backoff (`EndRetryBase` → `EndRetryMax`). The job-table lock guards the table, aliases and mailboxes and is never held across I/O: `pf ops status` / `pf watch` read only it and the job's own mutex, so they answer while the hub hangs. `persist` numbers its snapshots under it and writes under its own mutex; an older snapshot never lands last. The shutdown is `Runtime.Close`: it cancels the actors' context — no agent starts and no retry runs after it; an agent start still under way tears its handle down as it binds (its claim is deferred, journal entry kept; a relaunch leaves the record recovering); the shutdown waits for no actor. See `v2-converge.md#§6`.
- **Blocked seal**: `End(blocked)` with `head_sha` stores `attrs.blocked_seal={head, machine}`; a claim whose clone head differs continues from the sealed head only on the sealing machine, which fetches it from a quarantined git-dir of the work item (`<git-dir>.quarantined.<ts>`, newest first, a materialized generation preferred) and moves the chain to it — the next publish pushes it — and restores the task repos (carrier head and uncommitted work, through the carrier lane into a local `pf-carrier-sealed-*` store) from the same quarantined capsule; a claim by another machine, or a sealing machine that holds the head nowhere, is refused `DAEMON-518` (`runtime.go` `verifySealedHead`; `v2-converge.md#§E`). The owner's override is `pf resume <wi> --accept-lost-work`: the resume-blocked door clears the seal (event `blocked_seal_abandoned` with the abandoned head and machine; actor and time on the event) and the next claim on any machine continues from the aihub chain — whatever the sealing machine never pushed is lost.
- **Single daemon per data dir**: `<data-dir>/daemon.lock` flock taken before the socket probe (`daemon/daemon.go` `acquireDataDirLock`); a live listener on the socket also refuses a second daemon.

## 4. aihub service <!-- §4 -->

### 4.1 HTTP routes <!-- §4.1 -->

Registered in `internal/aihub/server/server.go` `routes()` — 35 registrations. Auth: **key** = Bearer API
key (`s.auth`), **admin** = key of a `role=admin` user (`s.authAdmin`). Project access
(`canRead`/`canWrite`): any membership (`viewer`/`writer`/`maintainer`) reads, `writer`+ writes, a global
admin does both; a key scoped to one project is refused elsewhere (`AUTH-403`). No unauthenticated route, no
health route. All paths under `/api/v1` except `/git/`.

| Method | Path | Auth / gate | Notes |
|---|---|---|---|
| POST | `/work-items` | key + project writer | create (`bundle_name`, `repo_inventory`, `parent_id`, …) |
| GET | `/work-items` | key; rows filtered by read access | `limit` (default 500, max 1000), `project`, `status`, `assignee`, `priority`, `milestone` |
| GET | `/work-items/{id}` | key + read | `include=events` (anything else `CORE-400`) |
| PATCH | `/work-items/{id}` | key + writer | `goal content labels priority milestone waiting_on clean_pause`, `bundle_name` in the R2 window (`patchableFields`; `attrs` only at create) |
| POST | `/work-items/{id}/cancel` | reporter/maintainer/admin | §2.1 |
| POST | `/work-items/{id}/wrap` | reporter/maintainer/admin | §2.1; work items with no bundle only |
| POST | `/work-items/{id}/claim` | writer | §3.2 |
| POST | `/work-items/{id}/session-verify`, `/session-attach`, `/session-end` | key (scope check) + secret | §3.3 |
| POST | `/work-items/{id}/force-end-session` | admin/maintainer | §2.1 |
| POST | `/work-items/{id}/resume-blocked` | reporter/maintainer/admin | §2.1 |
| POST, GET | `/work-items/{id}/dependencies` | add: writer; list: read | add / list edges; an unreadable blocker is `CORE-404` |
| DELETE | `/work-items/{id}/dependencies/{blocking}/{kind}` | writer | physical delete |
| GET | `/projects/{project}/ready-queue` | key + read | §2.2 |
| POST | `/machines/heartbeat` | key; first registration needs admin or any-project maintainer | §4.3 |
| GET | `/machines` | admin | §4.3 |
| POST | `/memories` | key + writer (else `MEM-403`) | §9 |
| GET | `/memories/search` | key | `q` (required), `match` (`all` default: every term; `any`: ranked any-term; else `MEM-400`), `type` (exact or `X.*`), `tags`, `limit` (default 50; ≤0 or >200 → 50), `project`, `excerpt` (≤8192 runes → `content_excerpt`) |
| GET | `/memories/{id}` | key; denied = `MEM-404` | `format=content` → raw Markdown body |
| POST | `/memories/{id}/redact` | owner or admin | soft delete |
| POST, GET / GET, PATCH | `/users`, `/users/{id}` | admin | |
| POST, GET / DELETE | `/api-keys`, `/api-keys/{id}` | admin | |
| POST, GET / GET | `/projects`, `/projects/{id}` | admin | |
| PATCH | `/projects/{id}` | key + in-handler role gate | repos, membership, description, visible |
| DELETE | `/projects/{id}` | admin (+key scope, same as PATCH) | hard delete: 409 non-terminal WIs (every offending slug) / unrevoked scoped keys (key ids); audit row in `admin_audit`; `?dry_run=1` = the checks and the same body, nothing written; per-WI bare repos removed after the commit |
| POST | `/admin/backup` | admin | `VACUUM INTO <data-dir>/backups/aihub-<ts>.db` — the SQLite database, every memory included (memories live in it); no git repos (`core/admin.go`) |
| ANY | `/git/` | session Basic auth (§3.4) | git smart-HTTP via `git http-backend` |

### 4.2 Process and data dir <!-- §4.2 -->

`cmd/aihub/main.go` flags: `-data-dir` (default `/var/lib/polyforge/aihub`), `-listen` (`:8420`),
`-git-janitor-period` (1h, gc/repack of grown bare repos, `git/janitor.go`), `-machines-stale-after`
(5m), `-version`. A fresh DB seeds admin `usr_root` and writes its key to a 0600 file in the data dir
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

Heartbeat body `{machine_id, runtime_version, display_name, mode, live_wi_ids}` (`core/admin.go`);
server-stamped `last_heartbeat_at`. A new `machine_id` creates (one transaction) a `mac_` machine user,
and the machines row; no API key is issued (the daemon runs on its operator-issued key). Daemon cadence
`-heartbeat-every` 60s (`daemon/heartbeat.go`), log-only failures with backoff. `GET /machines`
returns `{machines:[{id,user_id,runtime_version,mode,live_wi_ids,last_heartbeat_at,heartbeat_age_seconds,stale}], stale_after_seconds, count}`, oldest first.
The daemon id is the machine id: default `m-<short hostname>` (lower-cased, anything but `a-z0-9-` replaced by `-`; `daemon/ensure.go` `hostMachineID`), persisted in `<data-dir>/daemon-id`
(`daemon/identity.go`, `v2-converge.md#§4`).

## 5. Error codes <!-- §5 -->

Registry `internal/common/api/errors.go`; HTTP status = numeric suffix. `core.Fail` adds `extra.reason`.

| Code | Meaning (as raised) |
|---|---|
| `CORE-400` | Validation. Reasons incl. `IMMUTABLE`, `HAS_CHILDREN_NO_BUNDLE`, `PARENT_*`, `INVENTORY_*`, `BLOCKER_*`, `EXECUTION_STATE_REJECTED` |
| `CORE-403` | `STALE_SESSION`: the presented session is not the holder (attach/end/pause/idempotent replay) |
| `CORE-404` | WI/project/user missing, or hidden by the read gate |
| `CORE-409` | State conflict: `ALREADY_HELD NOT_CLAIMABLE DEPENDENCY_BLOCKED RACE_LOST NOT_ATTACHABLE NOT_HELD NOT_RUNNING NOT_BLOCKED SESSION_HELD DEPENDENCY_CYCLE INVENTORY_REPO_GONE`; without a reason: cancel of a running WI, project already exists, state changed concurrently |
| `CORE-410` | `TERMINAL_STATE` |
| `CORE-500` | Untyped server error (default mapping) |
| `CORE-507` | Admin backup `VACUUM INTO` failed |
| `CORE-508` | Another aihub holds `service.lock` |
| `MEM-400` | Bad memory input (empty `q`, type kind outside `typeKinds`, bad frontmatter, client-supplied id) |
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
| POST | `/jobs/{wi}/resume-paused` | live holder re-attaches a paused WI in place (`attached`/`already_live`/`closed`) | `pf resume` |
| POST | `/jobs/{wi}/pause` | operator pause (clean_pause) | `pf pause` |
| POST | `/jobs/{wi}/wrap` | terminal close with verdict | `pf wrap [--fail]` |
| GET | `/status[?harnesses=1]` | machine id, auto, max, workspaces, jobs, queue; harness probe | `pf ops status`, `pf doctor`, `pf daemon ensure` |
| POST | `/ops/config` | auto-pull switch, max concurrent jobs | `pf ops auto on|off`, `pf ops max N` |
| POST | `/shutdown` | async shutdown (§6.2) | `pf ops shutdown` |
| POST | `/chat/init` | harness session → chat id | `pf chat init` |
| GET | `/jobs/{wi}/watch?after=N[&demand=1]` | per-WI snapshot + events; `execution.declared_steps` lists the steps of the capsule's pinned workflow in frontmatter order as `{"id", "on_fail"}` (`on_fail` the gate's rewind target, else `null`), omitted when that workflow.md is missing or unparseable. `demand=1` (a marker, exactly the value `1`; any other value is unmarked) marks the request as human-side demand for the summarize lane (the per-WI memory-only lane wired into this route): honored only from a HUMAN-side peer — the UDS peer credential available and not a step process tree of this daemon — a step-peer or uncredentialed marker is ignored, never an error, the route stays a read. The snapshot carries the lane's narration beside `progress`: `summary` (the last finished line), `summary_error` (the raw error text of the latest failed call, never faked), `summary_age_sec` (present only with a summary) and `summary_capped` (present and true only while the per-dispatch cap is reached) — narration, never authority; absent = silent. An eligible marked request on a WI whose current step dispatch is live and running (the current step in_progress with a live step process, not parked on a question — a needs_input, paused or between-steps WI starts no call, however watched) may start ONE background summarize call (never waited on: the route answers immediately with the pre-call state; no call starts while the previous one is in flight) | `pf watch <wi> --once [--demand]`, pi-web |
| GET | `/chats/{id}/subscriptions` | list; a subscription owner routing added to a chat that did not submit the WI carries `origin_chat` and, while that origin chat holds a subscription to the WI, shares its delivery record (answered with its `cursor`/`notified`/`done`; acks land there — a decision is delivered once for both chats). It is answered `passive` (progress only, no decision line, no ack) while the origin chat's watcher is live; otherwise the lowest-id live chat sharing the record is the fallback and prints only what nobody delivered. A WI with no origin chat, or one the origin chat unsubscribed, wakes every subscribed chat from its own record (polyforge#199) | `pf watch` |
| POST | `/chats/{id}/subscribe`, `/unsubscribe` | manage subscriptions; an unsubscribe also mutes the WI for the chat (subscribe-owned skips it until a subscribe) | `pf watch subscribe|unsubscribe` |
| POST | `/chats/{id}/ack` | cursor advance | resident `pf watch --chat` |
| POST | `/chats/{id}/subscribe-owned` | `{"owner"}`: subscribe the chat to every held WI it submitted or `owner` owns (origin owner, else reporter) that it has no subscription for; answers the added ones (cursor just before the WI's newest decision event); the call also marks the chat live (in memory, for one minute; a chat not seen since the daemon started counts as live for its first minute) — liveness only picks who prints a decision nobody has delivered yet | resident `pf watch --chat`, every poll |

Socket: `--socket` > `PF_DAEMON_SOCKET` > cli.toml `daemon_socket` > `daemon.sock` in `$POLYFORGE_DATA_DIR`, else `~/.local/share/polyforge` (`cli.DataDir`; the same default for `pf` and the daemon, so `-data-dir` does not move it)
(`cli/config.go`, `daemon/main.go`).

### 6.2 Lifecycle <!-- §6.2 -->

- **Start = `pf daemon ensure` only** (`daemon/ensure.go`). Called by `tools/install.sh`, the Claude SessionStart hook (`--timeout 15s`) and the pi `pf-chat` extension. Socket answers → exit 0. Refuses inside a step (`PF_WI_ID` set). `<data-dir>/ensure.lock` (flock) serializes callers; losers wait and answer by the socket. Canonical argv: `--config`, `-machine-id`, `-data-dir`, `-socket`, `-workflows-dir <data-dir>/release/current/workflows`.
- **tmux loop** (loop script in `daemon/ensure.go`; `internal/polyforge/supervisor` only names the session and the systemd unit): session `polyforge-daemon`; loop runs `nohup pf daemon …`, appends to `<data-dir>/daemon.log` (rotated at 10 MiB); exit 0 → loop ends; **exit 78** → loop ends ("needs an operator fix"); anything else → restart after 5s. No tmux → one unsupervised start with a warning. `pf doctor` also accepts a self-managed `polyforge-daemon.service`.
- **Daemon environment = an allow-list** (`supervisor/env.go`, the one definition): ensure gives the daemon ONLY `PATH`, `HOME` (pinned), `USER`, `LOGNAME`, `SHELL`, `LANG`, `LC_*`, `TZ`, `TMPDIR`, the proxy vars (`http_proxy`/`https_proxy`/`no_proxy`/`all_proxy`, both cases), `SSL_CERT_FILE`/`SSL_CERT_DIR`, the XDG base dirs (`XDG_RUNTIME_DIR`, `XDG_CONFIG_HOME`, `XDG_DATA_HOME`, `XDG_STATE_HOME`, `XDG_CACHE_HOME`), the Go toolchain (`GOPATH`, `GOROOT`, `GOPROXY`, `GOPRIVATE`, `GONOSUMDB`, `GONOPROXY`, `GOFLAGS`, `GOTOOLCHAIN`, `GO111MODULE`, `GOCACHE`, `GOMODCACHE`), `POLYFORGE_DATA_DIR`, `GOCOVERDIR`, the marker `POLYFORGE_SUPERVISOR=tmux:polyforge-daemon` and the loop shell's own `PWD` — values from the CALLER of ensure, never the tmux server (whose environment is whatever shell first started it). The loop script runs `exec /usr/bin/env -i <assignments> /bin/sh -c <loop>`; the no-tmux fallback sets the same list as `cmd.Env`. Names that are not `^[A-Za-z_][A-Za-z0-9_]*$` are never carried (they would be shell syntax in the script); empty values are kept. No `ANTHROPIC_*`, no `PF_*` (ensure's own config load still honours them; the daemon reads `cli.toml`), no retired `POLYFORGE_*`, no harness-session markers. Steps see only this environment: pi provider keys belong in `auth.json` / literal `models.json` keys. Adding a variable is one line there; never a credential. `GET /status` reports the daemon's env NAMES (`env_names`, never values); `pf doctor`'s `daemon env` row warns on any `ANTHROPIC_*` or credential-looking name (`…TOKEN`/`…KEY`/`…SECRET`/`PASSWORD`) outside the list, with the clean restart (`pf ops shutdown`, wait, `pf daemon ensure`), and on any `$NAME` the step agent's `models.json` interpolates (`pi_models_env_refs` in `/status`) that is off the list.
- **Exit 78** = `ExitNeedsOperator` (`daemon/main.go`): startup failure only an operator can fix — config, identity mismatch, no `[workspace]` mapping, workspace dirs, on-disk state, failed restart reconciliation. Fix, then `pf daemon ensure`. After a machine/container reboot the daemon is down until the next
  interactive harness session runs ensure (auto-pull idles meanwhile); the skill retries an
  execution-plane verb once after `pf daemon ensure` when the daemon is unreachable.
- **Shutdown** (`daemon/shutdown.go`; `pf ops shutdown`, SIGTERM/SIGINT; a second signal aborts hard): crash-equals-shutdown — every state change is already durable, so the shutdown only stops work and saves the task repos' uncommitted work: latch shutting-down (new starts `DAEMON-410`; in-flight claims defer, nothing is waited for) → cancel the loops → per live job, in parallel: terminate its agent tree (10s), then snapshot its task repos to `refs/pf-wip/<name>` — all under one **30s** deadline → persist state once → close listener. The cause is one stderr line (`daemon: shutdown: cause=…`). Running WIs are **not** session-ended: they stay `running` at aihub with `clean_pause=false` and are attached on the next start. The same WIP snapshot also runs at every step end, after the step's process exits and before its result is written, bounded by **30s**: on a slow or hung hub it can delay a step's result by up to 30s (`v2-converge.md#§14.3`). There is no `pf daemon restart`: restart = `pf ops shutdown` then `pf daemon ensure`.
- **Restart reconciliation** (`runtime/restart.go`): Restart asks aihub nothing — it seeds every state.json record and claiming-journal entry into the job table (before anything can persist) and posts one message per work item to its actor, so the socket serves at once. Each actor reaps its work item's orphans, then reconciles: a claiming-journal entry first (its session verified: match → the same claim replays; otherwise the entry is dropped), then the record against a session verify — the fence every step passes before it relaunches an agent, pushes under the session or sends a SessionEnd: match + a pending terminal close (a retained paused record holding a wrapped/failed/blocked stop_reason) while the hub shows running or paused → the close replays, never a resume (polyforge#197); match+paused+`clean_pause=false` → attach and resume; match+running → recover-resume (a recorded close finishes, a pending pause replays); mismatch → release with no hub transition (kill, quarantine, drop, `session_released`; a terminal or blocked hub state just drops the record); transport error → keep the record as it is and retry with backoff; typed 4xx → blocked (`session_verify_outage`). Missing capsule / unmapped project blocks only that WI (`capsule_missing`, `project_unmapped`). Reaping (`runtime/reap.go`): spawn-journal pids + a `PF_WI_ID`+`PF_DAEMON_ID` environ sweep (layers in `runtime/cleanup.go`: PPID snapshot → process-group kill → (pid,starttime) re-kill → env-marker scan → cwd scan warns only). Decision record: `v2-converge.md#§6`.
- **Recovery engine** (`runtime/recover.go`): kill the tree, commit dirty state, then relaunch the in-process driver as a fresh derivation (spawn_n advanced; the driver continues from `execution.json`; the old resume→fresh cascade is gone, polyforge#106); after 2 consecutive failed recoveries (`MaxRecoveries`) the WI ends blocked. (Within a step, the driver's own retry shape still resumes the transcript first — §7.4.) A daemon restart re-dispatches the same model and is not counted as a failure.

### 6.3 Configuration <!-- §6.3 -->

- **cli.toml** `$XDG_CONFIG_HOME/polyforge/cli.toml` (default `~/.config/polyforge/cli.toml`; `cli/config.go`). Flat keys `server api_key daemon_socket user_id claude_bin`; env overrides `PF_SERVER PF_API_KEY PF_DAEMON_SOCKET PF_USER_ID PF_CHAT_ID`. Unknown sections / flat keys are load errors.
- **`[daemon]`** keys exactly `step_timeout stall_window harness_root operator_agent_dir max_concurrent_jobs` (quoted Go durations, absolute paths; `parseDaemonTable`). The daemon reads the table itself from `--config`; precedence explicit flag > table > default (`daemon/main.go` `applyDaemonTable`). `pf ops max N` sets the cap until the daemon restarts (never persisted; `daemon-state.json` holds only the auto switch).
- **`[workspace."<path>"]`** `projects = [...]` — the only source of capsule placement (no workspace flag/env). A project maps to exactly one workspace; zero sections → exit 78; each workspace dir must exist and `capsules/` be writable (`internal/polyforge/mapping` builds the plan, `daemon/daemon.go` checks the dirs at start). Migration runbook: `08-capsule-base-migration.md#§2`.
- **Daemon flags** (`cli.DefineDaemonFlags`, parsed by `daemon/main.go`; `pf help daemon` renders them): production-relevant `--config -socket -server -api-key -machine-id -data-dir (POLYFORGE_DATA_DIR, default ~/.local/share/polyforge) -workflows-dir -claude-bin (tests/dev; production source cli.toml claude_bin) -harness-root -operator-agent-dir (default ~/.pi/agent) -max-concurrent-jobs (4) -step-timeout (90m) -stall-window (10m)`; test/dev-only `-display-name -pi-bin -git-remote-base -recall-topk -auto -silence-window -tool-window -model-error-grace -revalidate-every -heartbeat-every`. Retired `POLYFORGE_HARNESS_ROOT` / `POLYFORGE_OPERATOR_AGENT_DIR` only warn.
- **`.polyforge.yaml`** at the workspace root (`internal/polyforge/workspace/workspace.go`): closed grammar `steps.<project>.<default|step-id>.models: [candidates]` plus the optional root `summarize: { models: [candidates], language: <text>, interval_sec: <int> }` (polyforge#224, moved to the root by polyforge#242; `language` by polyforge#243; `interval_sec` by polyforge#250) — the demand-gated summarize lane's model candidates, one list for every project of the workspace: `models` is as strict as a step entry's and mandatory in the entry, never a per-step precedence level, and absent = the lane silently off (a legitimate state, no doctor row); `language` is the narration language inserted verbatim into the prompt (a plain string, trimmed, non-empty, single-line; no allowlist, no cap; absent = `中文`); `interval_sec` is the per-WI minimum spacing between summarize call starts in seconds (a YAML integer in 10-3600 inclusive, never clamped, no 0-means-default; absent = 60; the 20-call per-attempt cap stays fixed); `steps.<project>.summarize` is an unknown step-id key like any other. Strict: unknown keys, `harness` (`RetiredHarnessHint`), the old top-level `models:`, unmapped projects, and step keys that are not installed step ids are errors → `DAEMON-414` before claim; `pf doctor` reports the same. Read fresh before every claim.
- **Auto-pull**: `pf ops auto on|off` + `pf ops max N` (`v2-converge.md#§5`); with auto on the daemon pulls each mapped project's ready queue; accepted submits beyond the cap queue in `submit-queue.json` and replay after restart (the file holds each submit's intent — work item, priority, enqueue/submit time, models, origin; the DAEMON-503 backoff is not persisted and starts over).

### 6.4 Data dir and events <!-- §6.4 -->

Default `~/.local/share/polyforge`. File inventory: `v2-converge.md#§E` (E.4), plus `git/askpass/`
(one-shot credential helpers, swept at start; `gitops/gitops.go`). Events: one JSONL line per event in
`events/wi-<id>.jsonl` (one file per work item, `events/daemon.jsonl` for daemon scope; per-WI seq
monotonic across restarts; a file silent for 30 days is removed at start; `observe/events.go`); kinds and emitters in E.1. Execution state authority is always the capsule's
`runtime-state/execution.json`; events are news.

## 7. Execution <!-- §7 -->

### 7.1 Bundles and materialization <!-- §7.1 -->

- Shipped bundles: `workflows/{feature-full,bug-fix,chore,autopilot,release}`; shared step files in `workflows/_shared/steps/` (today `deliver.md`, `verification.md`, `review.md`; `{{name}}` placeholders rendered from the workflow.md step entry's `vars`, `01-workflow-skills.md#§4.4`). `_shared/principles.md` holds the generic engineering principles; a step definition's `{{principles}}` (the one placeholder needing no `vars` entry) renders it, inside a `## Principles` section that opens with the step's own usage paragraph — shipped in the grill/spec/plan/code-like steps and the shared review (`01-workflow-skills.md#§4.4`). Repo-specific rules live in each repo's `AGENTS.md` / `CLAUDE.md`, which those steps read first. `_shared` is not a bundle (no `workflow.md`; claiming it is refused).
- Step file lookup `workflow.StepDefinitionPath` (`internal/common/workflow/workflow.go`): the bundle's own `steps/<id>.md` wins, else `_shared/steps/<id>.md`. Do not share by symlink — materialization skips symlinks.
- Source: the daemon's `-workflows-dir` (production `<data-dir>/release/current/workflows`). At claim the bundle frontmatter is read from that checkout; then `materializeBundle` (`runtime/materialize.go`) copies the bundle to `<capsule>/workflows/<bundle>/` and `_shared/` beside it in the freshly cloned capsule, marking completion in `sync/materialized.json` (recovery relaunches only over a snapshot that marker vouches for). A missing bundle warns and continues. No digest check (deleted, polyforge#149).
- The driver loads the materialized copy: `driver/workflow.go` `LoadWorkflow` → `validateWorkflow`. Authoring guide: `01-workflow-skills.md#§4`.

### 7.2 workflow.md frontmatter <!-- §7.2 -->

Parsed keys (`driver/workflow.go` `extDoc`/`extStep`); **unknown keys are silently ignored** (incl. the
retired step `kind:`, `interactive:`, `tools:`); `name`/`description` are unread.

| Key | Meaning |
|---|---|
| `defaults.model_candidates` | fallback candidate list (§7.5) |
| `rewind.max_total` / `rewind.max_per_step` | rewind bounds, default 4 / 2 |
| `steps[]` | declaration order = execution order |
| `steps[].id` | required, unique; names `steps/<id>.md` and the `.polyforge.yaml` key |
| `steps[].gate` | step yields a verdict; must be `context_policy: fresh` |
| `steps[].on_fail.target` | gates only; an earlier step; absent → `failed_gate` |
| `steps[].requires_human_answer` | `true` (needs a human answer in the current attempt chain) / `if_asked` (questions are human-only; zero questions → event `human_gate_unasked`) / `false` |
| `steps[].requires_command_evidence` | completed result must carry `command_evidence` |
| `steps[].context_policy` | `inherit` (default) / `fresh` |
| `steps[].handoff` | step writes `artifacts/handoff-<id>.md` for the next inherit step |
| `steps[].model_candidates` | step's own candidate list |
| `steps[].budget.wall_minutes` | estimate: T = max(3 × wall_minutes, `--step-timeout`) |

Load-time rules (`validateWorkflow`): non-empty unique ids; `on_fail` only on gates and pointing
earlier; `context_policy` ∈ {inherit, fresh}, gates fresh. `requires_human_answer` ∈ {true, false,
if_asked} is enforced by its YAML decoder; candidates are validated in `driver.New` and, before the
claim, against the daemon checkout (`common/workflow.CheckCandidates`: unprefixed candidates or a
`harness` key → `DAEMON-414`, WI stays queued). Rewind bounds ≤0 fall back to 4 / 2. Step file frontmatter: only `output:` (draft-07 JSON Schema in YAML) is
read — injected into the prompt and checked (`driver/outputschema.go`): enforced on the release
bundle's `release` and `deploy` steps (a violation is an envelope rejection; their schemas carry the
verdict contract), record-only on every other step (event `output_schema_violation`).

### 7.3 Step dispatch and the result envelope <!-- §7.3 -->

- One harness process per dispatch, cwd = capsule. The step delivers **one** JSON envelope with `pf result submit <json|-|@file>` — the only delivery path the prompt names (polyforge#214; hand-writing the drop is not described, and a helper that cannot deliver ends the turn without a result, the exited-without-result recovery lane). The agent supplies `{outcome, output}` (needs_input: `question_id` + `question`; `command_evidence` when the step owes it); the daemon's `POST /step/result` (`daemon/stepresult.go`) resolves the calling step from its process tree like `/step/memories`, refuses a caller-supplied `wi_id`/`step_id`/`spawn_n` that disagrees and the light shape defects (`driver.CheckEnvelopeShape`: JSON, outcome closed set, `output` an object, the needs_input question pair) with `DAEMON-403`/`DAEMON-400` writing nothing, stamps `wi_id, step_id, spawn_n, submitted_at` itself and lands the envelope atomically at `runtime-state/pending-results/<spawn_n>.json` — the caller's cwd and any harness sandbox have no say. Outcomes `completed | gate_failed | needs_input | incomplete` (`driver/result.go`). The driver collects the drop (refusing a non-empty `wi_id`/`step_id` of another dispatch, `ErrIdentityMismatch`, or a stale `spawn_n`) and archives it to `artifacts/steps/<step>/s<spawn_n>/result.json`; every deeper check stays in its accept gate below.
- Validation order (`ValidateStepResult`): shape; identity + spawn_n fence; needs_input question shape; gate verdict binding (completed with `verdict: fail` → `gate_failed`); `requires_human_answer`; `defer` verdict only on `deploy`; output schema (enforced on `release`/`deploy`, record-only elsewhere); `needs_code_change` only as a boolean on a light re-entry (`ResultSpec.LightRewind`; a non-boolean value, or the field with no light rewind preceding it in the step's ledger, is an envelope rejection); command evidence (a list of objects on any step, else rejected; on evidence-owing steps argv non-empty, `duration_ms>0`); committed test files; pre-existing-failure boundary; authored tests unchanged; deliver binding. A rejection is retried in-process with the reason; the 4th consecutive rejection (`maxEnvelopeRejects`=3 retries) blocks the step `result_invalid`. Details: `v2-converge.md#§2.6`.
- `gate_failed` → bounded rewind (`driver/transition.go` `applyGateFailLocked`): no `on_fail` → `failed_gate`; a bound hit → `failed_rewind_exhausted`; else `[target, gate)` archived and reset (the target as `gate_fail_rewind`, the steps strictly between it and the gate as `superseded_by_rewind`, both with `rewind_gate`), the verdict text delivered to the target as `pending_rewind_input`, candidate pointers reset. The LIGHT variant (polyforge#222), in the same single write and the same one slot: a `review`-gate FAIL whose recorded blocking findings (at least one) all carry `text_only: true` archives the target as `gate_fail_rewind_text_only` and routes the verdict with the light re-entry instruction as the input's `Note` — the re-dispatch then resumes the target's previous conversation (same harness, same session identity) instead of re-deriving fresh, an order derived from the ledger that same write already recorded (the target pending, its latest non-`infra_failure` archived attempt the light cause — the same fact the `needs_code_change` validation reads; no second carrier); a resume that is impossible (another harness, or the native session/transcript gone) degrades to today's fresh-with-reference fallback in the same slot, recorded `resume: false` in the spawn journal. A light re-entry's completed result may carry `needs_code_change: true` (the structural self-report; a non-boolean value, or the field without the light context, is an envelope rejection): the same write archives the resumed attempt as `gate_fail_rewind` (`rewind_gate` = the light gate), resets the target pending, routes the same FAIL verdict as the fresh re-entry's input and charges NO new slot (a classification error is not charged twice).
- `needs_input` → question stored in `execution.json`; a human answers with `pf answer` → daemon `/jobs/{wi}/answer` → the driver (`applyAnswerLocked`). Never answered by an agent (UDS step-origin refusal).
- `incomplete` → workflow `blocked`. Workflow statuses: `running completed failed_gate failed_rewind_exhausted blocked`; step statuses `pending in_progress done` (`common/capsule/capsule.go`). A terminal workflow closes the WI (completed → `wrapped`, failed → `failed`, blocked → hub `blocked`) after a final seal push; if that push fails the close degrades to blocked, carrying the local chain head as `head_sha` so the blocked seal guards the unpushed commits (`v2-converge.md#§D`).
- `execution.json` (`capsule.Execution`, the driver is its single writer): `capsule_id`, `work_item_id`, `current_step`, `workflow_status`, `steps` (per id: `status`, `model`, `effort`, `output`, `step_started_at`, `budget_consumed`, `attempts[]` with `cause` `accepted`/`gate_fail_rewind`/`gate_fail_rewind_text_only`/`superseded_by_rewind`/`infra_failure` and `failure_cause`, `pending_question`, `answers`, `answer_base`, `pending_rewind_input`, `supervision` counters and relaxed bounds, `next_dispatch`), `review_rounds`, `rewind_stats`, `budget_consumed`, `updated_at`, `version` (bumped on every write; the push loop keys on it), `blocked_reason` (set when the driver blocks on its own authority: host-class fault, failure bound, `result_invalid`).

### 7.4 Context between steps <!-- §7.4 -->

`inherit` steps get the body of the nearest earlier `handoff: true` step's `artifacts/handoff-<id>.md`
(producer must be done; a missing file only omits the section); `fresh` steps get none
(`driver/spawn.go`). The claim-time memory snapshot `runtime-state/recalled-memories.json` is rendered
into every step prompt as non-binding history (§9) — one `[type] title` line per memory (no ids or
bodies; a blank title falls back to the excerpt's first non-empty line), id-only noise titles
(`^Verification Report — \S+$`, `^Controller-sink artifact`) dropped, at most 10 lines, no section when
none is left (`driver/spawn.go`). Every step prompt also asks for `pf recall --any` with 2–3
phrasings at the start (`workflows/_shared/memory.md`); only the workflow's last step writes a memory,
the work item's one `experience.wrap` summary guided by the project's memory policy from
`runtime-state/memory-policy.json` (`workflows/_shared/memory-wrap.md`). Step agents hold no
aihub credential path; memory access is daemon-mediated: inside a step (`PF_WI_ID` and `PF_DAEMON_ID`
set) `pf recall` / `pf memory show` read through `GET /step/memories/search|{id}` and `pf remember`
posts synchronously to `POST /step/memories` (one of the step-origin guard's two step-allowed writes, §6.1), all
scoped to the one work item whose journaled step process is the caller's ancestor (never the caller's
own env) and its project (`daemon/stepmem.go`). The journaled step process is recognized by its journaled
pid + `started_at` (live, start time matching — `runtime.JournaledProcessLive`), never by its environment:
darwin omits an entitled agent's environment from `kern.procargs2` (polyforge#234). The write accepts `experience.wrap` only, ≤ 2 KB, a
title, no credential value (`internal/common/stepmem`, shared with the CLI), and creates it with the
aihub idempotency key `pf-step-memory:<wi>:<step>:<attempt>`; aihub down answers DAEMON-503 and the
prompt says a failure never fails the step. Every other aihub verb, `pf doctor` and `pf daemon` are
refused there (DAEMON-403). `cli.toml` stays readable under the shared HOME: a guard against accidents, not malice. Retry shape: first retry resumes the transcript,
later retries start fresh with the old transcript as read-only reference (`v2-converge.md#§2.3`).

### 7.5 Model candidates <!-- §7.5 -->

- Format `<harness>:<model>[:<effort>]` (`internal/common/harness/harness.go` `ParseCandidate`): harness ∈ `pi`, `claude`, split at the first `:`; the last segment is an effort only if it is one of `off minimal low medium high xhigh max` (Claude accepts `low`…`max`), otherwise it belongs to the model id. Effort → pi `--thinking`, claude `--effort`.
- Precedence, first **present** level wins whole (no merge; `workspace/compose.go`): (1) `pf submit --models a,b` (this WI, every step; daemon-local `manual-models.json`, dropped at a terminal close, not carried to another machine) > (2) `.polyforge.yaml` `steps.<project>.<step-id>.models` > (3) bundle step `model_candidates` > (4) `.polyforge.yaml` `steps.<project>.default.models` > (5) bundle `defaults.model_candidates`. Composed once per claim; snapshot in the job record (the summarize lane's list rides the same one-yaml-read snapshot: `workspace.SummarizeModels`, the root `summarize` key's workspace-wide pick, carried as `SummarizeModels`, beside `workspace.SummarizeLanguage` carried as `SummarizeLanguage` (`summarize_language`; a record without it reads as `中文`) and `workspace.SummarizeIntervalSec` carried as `SummarizeIntervalSec` (`summarize_interval_sec`; a record without it reads as 60) — an edit governs the next claim only).
- Advancement (`driver/candidates.go`): the pointer counts model-class failures since the last rewind (`model_error`, `model_hung`, `step_timeout_slow_model`, `exit_model_class`), capped at the last entry; host-class failures block; other causes retry the same model; `rate_limited` / `upstream_transient` retry the same model with backoff. Full cause table: `v2-converge.md#§2.3`.

### 7.6 Harness adapters <!-- §7.6 -->

| | pi (`runtime/spawn.go`) | Claude Code (`runtime/claudespawn.go`) |
|---|---|---|
| argv | `pi --mode rpc --no-skills --no-context-files --no-prompt-templates [--session <transcript>] [--model] [--thinking]` | `claude -p --bare`/`--safe-mode --output-format stream-json --verbose --include-partial-messages --session-id <uuid>`/`--resume <uuid> [--model] [--effort] --tools/--allowedTools Bash,Read,Edit,Write,Glob,Grep,TodoWrite,Agent --permission-mode dontAsk --strict-mcp-config --disable-slash-commands …`; exactly one context flag, picked at every spawn (fresh and resume) from the `<harness-root>/claude-config/settings.json` `env` block: a non-empty `ANTHROPIC_AUTH_TOKEN` or `ANTHROPIC_API_KEY` → `--bare` (token mode); otherwise, or no file / no harness root → `--safe-mode` (OAuth mode: `--bare` would skip the keychain); a file that exists but is unreadable, not JSON or without an `env` block refuses the spawn (polyforge#223); prompt on stdin |
| env | `PF_WI_ID`, `PF_DAEMON_ID`, `PF_DAEMON_SOCKET` (the daemon's own socket, so a step's `pf` reaches it), `PI_CODING_AGENT_DIR=<harness-root>/step-agent`, `PI_CODING_AGENT_SESSION_DIR=<harness-root>/step-agent-sessions/<dashified-capsule-dir>` (the capsule's own dir — pi writes sessions flat into it, and the live-transcript locator scans exactly it); all other `PF_*` stripped | the same `PF_*` markers and socket; `CLAUDE_CONFIG_DIR=<harness-root>/claude-config`, `CLAUDE_CODE_DISABLE_BACKGROUND_TASKS=1`, `DISABLE_AUTOUPDATER=1`, …; no credential injected; every inherited `ANTHROPIC_*`, `CLAUDECODE`, `CLAUDE_CODE_*`, `CLAUDE_CONFIG_DIR` stripped (the settings.json `env` block is the only credential/endpoint source), and so is an inherited copy of any other name the adapter sets (e.g. `DISABLE_AUTOUPDATER`), so each adapter name appears once with the adapter's value |
| transcript | live file located in the session store, copied verbatim to `session/session-<token>.jsonl` at commit boundaries (`runtime/pitranscript.go`) | `<claude-config>/projects/<dashed cwd>/<uuid>.jsonl`; resume only if it exists |

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

- Layout: `v2-converge.md#§E` (E.5), constants `internal/common/capsule/capsule.go`; `pending-results/` and `sync/` are defined in `driver/spawn.go` and `runtime/materialize.go`. `.gitignore`: `/repos/`, `/artifacts/release-*/`, `/sync/`. The capsule's git-dir is external (`<data-dir>/git/<wi>.git`, a `gitdir:` pointer file in the capsule): it is outside the capsule tree, but a step's own `git commit` in the capsule root still reaches it and is published as-is.
- Chain rules (FF-only, no ref deletion, blob ≤16 MiB, no gitlinks), push fencing, push retry schedule, re-claim = clone + task-repo restore, `DAEMON-516/518`: `v2-converge.md#§14.1`. Carrier refs `refs/pf-task/<name>`: `v2-converge.md#§14.2`. Uncommitted-work snapshots `refs/pf-wip/<name>`: `v2-converge.md#§14.3`.
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
and the in-SQL `project=` narrowing are the same in both modes (single entry `Store.SearchMode`).
Both modes order by bm25 weighted by per-type freshness: `rank × (1 + 0.5 × exp(-age_days/stability))`,
age from `updated_at`, stability rule.*/doc.design/doc.decision none, other doc.* (review, audit) 180d,
experience.* 90d, fact.*/methodology.* 180d, else 90d
(`memory/freshness.go`). A memory written without a title gets one derived from its body's first line
(row only, at create time). CLI (`cli/verbs_aihub.go`): `pf recall --project P <query> [--any]` (project must
be mapped; `--any` = `match=any`), `pf remember --project P --type T <content|-|@file>`, `pf memory show <id>`. At claim the
daemon writes `runtime-state/recalled-memories.json` (query = goal + labels, `match=any`, project-scoped, top 10,
1200-rune excerpts; empty list + notice on error or no hits; never fails the claim — `runtime/recall.go`,
`daemon/memface.go`). Deploy aihub before daemons: an older aihub ignores `match` and ANDs, which
leaves the snapshot empty. `pf backup` is a SQLite snapshot holding every memory, without git repos (§4.1).

## 10. Install, release, deploy <!-- §10 -->

The script headers are the contract; this section only points.

- **Work machine**: `tools/install.sh` (header) — `curl … | bash -s -- <api-key> [server-host]` (host = argument 2, else `PF_HOST`, no built-in default; required only for a first install — no cli.toml with an `api_key` — and refused before any change without it; an upgrade needs none); stages the SHA-verified `pf`, checks out `<data-dir>/release/<ver>`, then under `ensure.lock` (waits up to 120s, then proceeds without it) shuts the daemon down gracefully when a new binary was staged (refused while jobs run unless `PF_INSTALL_FORCE=1`), swaps `/usr/local/bin/pf` and the `release/current` symlink, keeps an existing cli.toml that has an `api_key` (else writes it), installs the skill, runs `pf daemon ensure` (skipped when the daemon already answers or there is no `[workspace]` mapping / workflows dir), verifies. Upgrade = re-run it. Skill install: Claude `~/.claude/skills/polyforge` → symlink into `release/current` + one SessionStart hook in `~/.claude/settings.json`; pi one local `packages` entry naming `release/current` (`package.json` declares the skill and `extensions/pf-chat.ts`). `pf doctor` checks both (`cli/doctor_layout.go`).
- **Release**: `.polyforge/release.sh` (header) — `[major|minor|patch]` or `--verify <tag>`; clean tree at `origin/main`, PR-only history since the last tag (bar the committed owner-direct author allowlist, polyforge#240), `make test`, `make release`, docker image `ghcr.io/silgrid/aihub:<tag>`, tag push (the version claim), assets + `gh release create`. Run by the `workflows/release` bundle (`release → verify → deploy`, no rewind).
- **Deploy (aihub host only)**: `.polyforge/deploy.sh` (header) — `<version> --confirm <version>`; `AIHUB_HOST`, `AIHUB_DATA`, `COMPOSE_FILE`, `COMPOSE_PROJECT`, `DEPLOY_LOCK_FILE` required (no defaults; from the environment or the operator's untracked `~/.config/polyforge/deploy.env`, environment wins; missing → exit 2 naming them, before any network work, `--dry-run` included), server-side lock, image via `docker save | ssh docker load`, online backups, seconds-long switch window applying `deploy/sql/<tag>/{upgrade,rollback,probe}.sql`, automatic rollback (exit 10). Schema history and operator steps: `docs/upgrade.md`.
- Workspace layout helper: `pf init [<dir>]` (`cli/verbs_init.go`) — `scratch/` and the managed CLAUDE.md section; mapping stays a cli.toml edit.

## 11. CLI verbs and the routing skill <!-- §11 -->

`internal/polyforge/cli/cli.go` `commands`: aihub-direct `file queue cancel recall remember memory`;
daemon `submit answer pause resume wrap ops chat watch`; admin `users api-keys machines projects backup`;
`doctor`, `version`, `init`, `daemon` (`daemon ensure`; `cmd/pf/main.go` routes `pf daemon …` to
`daemon.Main`). Exit codes: 0 ok, 1 client-class (4xx, validation, transport, a sub-action's flag error), 2
server-class (5xx, unknown verb, a global or verb flag parse error). The skill (`skills/polyforge/SKILL.md`) routes the 13 verbs + doctor/version/help/init,
one fragment per verb under `verbs/` (`ship.md` only says delivery is the workflow's `deliver` step);
`pf help` is the quick reference. Progress/notification design: `v2-converge.md#§3`.

## 12. History <!-- §12 -->

Archived (`archive/`, 2026-10-16): `00-architecture`, `02-aihub-core`, `03-execution-capsule`,
`04-local-runtime`, `05-sync-revision-recovery`, `07-deployment-pilot`, `v1-architecture-spec`,
`v1-spec-full`, `v1.5-session-model`, `v1.5-api-contract`, `v1.5-change-coverage-rules` (its rules now
live in [docs/contributing.md](../../contributing.md)). They describe the v0/v1 design (WI Agent, owner
generations, Revision/CAS sync, interactive/batch/drain modes, `pf upgrade`, …). Live mechanisms they
held were moved here or into `v2-converge.md` §14 / Appendix E. Lessons: `v0-lessons-ledger.md`.
