# Upgrade guide

This document describes how an aihub database moves from an old schema to
a new one. The upgrade is executed by `.polyforge/deploy.sh`; there is no
startup auto-migration (`Open` installs the schema with
`CREATE TABLE IF NOT EXISTS` only), and the new server does **not** work
on an un-upgraded database: the old `projects.identifier_hash` column is
`TEXT NOT NULL` without a default and the new code no longer writes it,
so creating a project fails until the upgrade SQL has run.

## The deploy.sh flow (seconds-level downtime)

- **Online backup before the window**: rsync of `memory/` and `git/`, a
  SQLite backup-API snapshot of `aihub.db` (consistent even under WAL
  writes), and an off-host copy. The service keeps running throughout.
  Once that backup has verified (step 5), deploy.sh writes a `.verified`
  marker into it on both sides, keeps the newest 3 verified
  `aihub-backup-<version>-<timestamp>` dirs (this run's included) on the
  server and in `BACKUP_ROOT`, prunes older verified ones and prints what
  it pruned; `--dry-run` prints what it would prune. A backup dir without
  the marker — a failed run's, and **every backup made before this
  change** — is never counted and never pruned: each run lists them as
  "unverified, kept; remove by hand". Delete the pre-marker ones yourself
  once a marked backup exists. Nothing else is ever touched. The backup
  never copies `bootstrap-admin.key` (a one-time handoff file; the key is
  stored in the DB as a hash) or a stray `.verified`.
- **The switch window is the only service interruption**, in this order:
  1. the old aihub container exits gracefully (drain ≤ `DRAIN_SECS`, default
     15s — the compose file's `stop_grace_period` is also 15s, longer than
     aihub's own 10s HTTP drain; the new default applies from the next
     deploy);
  2. deploy.sh applies the upgrade SQL (see below), one transaction per
     script;
  3. the new container starts and HTTP is polled.
  The actual downtime is printed and warned past `MAX_DOWNTIME_SECS`. It
  is still seconds: the SQL is a handful of DDL statements.
- **Post-swap verification**: row counts must be >= the frozen baseline,
  except tables dropped by a `DROP TABLE` in an upgrade.sql applied this
  run, which are excluded from the post-swap row-count check.
- **Automatic rollback**: if anything from the window on fails (the SQL
  transaction fails, the new container does not come up, verification
  fails, unexpected exit), deploy.sh first runs the `rollback.sql` of
  every version applied in this run — in descending version order, one
  transaction each — and only then recreates the old image and verifies
  the old version answers. The old binary never sees the post-upgrade
  schema; no backup restore is needed. A script that failed was fully
  rolled back by its own transaction, so nothing of it is applied.

## Where the SQL lives

Every release tag that changes the schema ships a directory:

    deploy/sql/<TAG>/upgrade.sql    the schema delta, applied as ONE transaction
    deploy/sql/<TAG>/rollback.sql   restores the pre-release shape for auto-rollback
    deploy/sql/<TAG>/probe.sql      the structural applied-check (run-once)

deploy.sh reads the files **from the target git tag at deploy time**
(`git -C .polyforge/.. show <TAG>:deploy/sql/<TAG>/...`, with a
working-tree fallback), the same way the legacy rollback Dockerfile is
fetched. release.sh is not involved — the SQL is not a release asset. A
release tag without a `deploy/sql/<TAG>/` directory carries no schema
change; its switch window is just the container recreate.

The candidate versions come from the deployer's **local** git tags, so the
target tag must exist locally — deploy.sh refuses in preflight otherwise
(`git fetch --tags`). The running version's tag is not required.

**File format**: each file is a sequence of `;`-terminated statements.
Comments are fine before or between statements, but **nothing may follow
the last statement's `;`** — a trailing comment after it is read as an
incomplete final statement and the runner refuses the file.

**Downgrades**: a `--force` downgrade whose range (target, running] contains
a `deploy/sql` version is refused in preflight — deploy.sh runs rollback SQL
only inside its own auto-rollback. Roll such a schema back by hand (each
crossed version's `rollback.sql`, newest first, service stopped).

**Run-once**: `probe.sql` is a structural check answered read-only against
the live database before the window (e.g. "does the column that will be
dropped still exist?"). `0` means the upgrade already ran and the version
is skipped — re-deploying the same version (`--force` included) never
applies the SQL twice. No bookkeeping table is written to the production
database.

**Multi-version gaps**: when the running version is several schema-changing
releases behind, deploy.sh applies every not-yet-applied version's SQL in
ascending version order inside the one window (probe-reported applied
versions are skipped).

## Requirements

The server's `python3` (stdlib `sqlite3`) must link a SQLite library
**≥ 3.35** — the first release with `ALTER TABLE ... DROP COLUMN`. deploy.sh
checks this in preflight, before the window, and refuses the deploy with
nothing changed when the linked library is older. SQL execution itself uses
the deployer's ssh + `python3` stdlib-sqlite3 path; the `sqlite3` CLI is
neither used nor required.

**Minimum database baseline.** The `deploy/sql/` chain starts at v2.4.0
and assumes the columns that the old in-process Go migrations added:
`machines.mode` / `machines.live_wi_ids` (heartbeat face, first in v0.4.2)
and `memories.fs_mtime_ns` / `memories.fs_size` (memory freshness stamp,
polyforge#129, first in v1.12.0). Those migrations were deleted in v2.2.0
(3d7a857) and `Open` runs none, so a database must either have been
created by v2.2.0 or later, or have been opened at least once by a server
between v1.12.0 and v2.1.x. A database last served by anything older is
not upgradable by deploy.sh: its first heartbeat or memory write fails
with `no such column`. Bring it through a v2.1.x server first (or add the
four columns by hand) before deploying a current version.

## First instance: v2.4.0 (polyforge#149, audit subtraction C)

The whole schema delta of #149: the delivery ledger, the dead tables and
columns are gone. The file `deploy/sql/v2.4.0/upgrade.sql` is the source
of truth; its content:

```sql
DROP TABLE IF EXISTS wi_ship_ledger;
DROP TABLE IF EXISTS core_meta;
ALTER TABLE projects DROP COLUMN scenario;
ALTER TABLE projects DROP COLUMN identifier_hash;
ALTER TABLE work_items DROP COLUMN bundle_digest;
ALTER TABLE work_items DROP COLUMN workflow_budget_baseline;
ALTER TABLE work_items DROP COLUMN session_owner;
```

The matching `deploy/sql/v2.4.0/rollback.sql` re-adds the five dropped
columns with defaults (`identifier_hash TEXT NOT NULL DEFAULT ''`; the
others nullable as before) and rebuilds `wi_ship_ledger` and `core_meta`
as empty tables from the pre-#149 (85cf926) DDL: old-version code runs
correctly on the result, existing rows carry the defaults.
`deploy/sql/v2.4.0/probe.sql` counts the dead shapes (0 = already applied).

## v2.5.0 (polyforge#157, repo inventory column)

The per-work-item repo inventory (frozen at create; see
`docs/design/polyforge-vnext/reference.md` §2.1) moves from the
`wi_repo_inventory` table into one JSON column, `work_items.repo_inventory`
(`[{"name": "<project repo>"}]`, names only — URLs are resolved from the
project at claim time; NULL = no inventory). Production held 0 rows in
the table, so nothing is carried over. The v2.5.0 server reads and writes
the column, so it does not work until this SQL has run. The file
`deploy/sql/v2.5.0/upgrade.sql` is the source of truth; its content:

```sql
DROP TABLE IF EXISTS wi_repo_inventory;
ALTER TABLE work_items ADD COLUMN repo_inventory TEXT;
```

The post-swap row-count check leaves `wi_repo_inventory` out (it is
dropped by this upgrade.sql). `deploy/sql/v2.5.0/rollback.sql` drops the
column (an inventory filed during the failed deploy is lost with it) and
rebuilds `wi_repo_inventory` with its unique index as an empty table from
the v2.4.x DDL. `deploy/sql/v2.5.0/probe.sql` answers 0 when the table is
gone and the column exists (a database created by v2.5.0 is already in
that shape).

## v2.14.0

**Deploy aihub first, then upgrade the work machines** (`tools/install.sh`
re-run; it upgrades `pf` and restarts the daemon together). A new hub
accepts what older daemons still send (the `Polyforge-Seq` trailer, the
session-fingerprint push option, a claim's `idempotency_key`) and ignores
it; a new daemon against an older hub fails: the old hook demands the
trailer the new daemon no longer writes, and a lost claim answer ends
blocked instead of being replayed. Upgrade `pf` and the daemon together:
the daemon's `POST /jobs/{wi}/resume-blocked` route is gone (`pf resume`
on a blocked work item now uses `POST /jobs/{wi}/claim` with
`{"resume":true}`), so an older `pf resume` fails against a new daemon.

### Upgrade SQL (polyforge#194)

aihub stops writing memory files: create and redact touch only the
database (see "Memories move into the database" below). The memories
freshness columns the file-authority store used and the claim replay
cache that claims no longer read are dropped. The file
`deploy/sql/v2.14.0/upgrade.sql` is the source of truth; its content:

```sql
DROP TABLE IF EXISTS claim_idempotency;
ALTER TABLE memories DROP COLUMN fs_mtime_ns;
ALTER TABLE memories DROP COLUMN fs_size;
```

The post-swap row-count check leaves `claim_idempotency` out (it is
dropped by this upgrade.sql). `deploy/sql/v2.14.0/rollback.sql` re-adds
the two columns (NULL; v2.13.0 never reads them) and rebuilds
`claim_idempotency` as an empty table from the v2.13.0 DDL.
`deploy/sql/v2.14.0/probe.sql` answers 0 when the columns and the table
are gone (a database created by v2.14.0 is already in that shape).

- **Rollback target: v2.13.0 only.** v2.13.0 serves memories from the
  database, so rows this release wrote (no file) serve there unchanged. A
  release older than v2.13.0 reads memory files and must not run on this
  database; deploy.sh already refuses a downgrade across a `deploy/sql`
  version.
- After an automatic rollback the claim cache starts empty: a claim
  replayed by an idempotency key cached before the deploy is not answered
  from the cache.
- The files under `memory/` are left in place and still copied by the
  deploy backup's rsync; aihub reads them only to fill a row without
  content (a database last served before v2.13.0).

### Other visible changes

- **aihub API** (polyforge#194, #192): the work-item detail no longer
  carries the `derived` epic block and the ready queue no longer has the
  `pending_bundles` segment (`{project, items, running, paused}`);
  `GET /work-items/{id}?include=deps` is `CORE-400` (use
  `GET /work-items/{id}/dependencies`, i.e. `pf file dep list`; only
  `include=events` remains). A create body with `repo_inventory_base` is
  `CORE-400 INVENTORY_BASE_UNSUPPORTED` (`pf file create --stdin` refuses
  it locally). Claim replay is keyed by the session secret: the holder's
  repeat answers the same claim, a secret that no longer holds the work
  item gets `CORE-403 STALE_SESSION`, and `IDEMPOTENCY_KEY_REUSED` is never
  answered on a claim. `MEM-500` is gone. A first heartbeat registers the
  machine without minting an API key.
- **Job cap** (polyforge#183): `pf ops max N` lasts until the daemon
  restarts and is never persisted; `pf ops max --reset` is refused, naming
  the change. Details, the socket default and the machine id: "Work
  machines: job cap, socket default and daemon id" below.
- **Events and watching** (polyforge#181): one events file per work item;
  the old `events.log*` / `events.seq` are not read; a resident watcher's
  start subscribes its chat to its owner's work items. Details: "Work
  machines: per-work-item events and owner subscriptions" below.
- **One actor per work item** (polyforge#182): a daemon restart serves at
  once and verifies each work item's session in the background (no
  `(awaiting aihub)` in `pf ops status`); concurrent `pf wrap` / `pf pause`
  / `pf resume` of one work item queue and get the final answer instead of
  `DAEMON-409` / `DAEMON-503`.
- **Capsule chain** (polyforge#188): commits carry no `Polyforge-Seq`
  trailer; a step's own `git commit` in the capsule root is published as
  an ordinary commit; a gitlink in any commit about to be pushed is refused
  locally (`GIT-412`, naming commit and path).
- **Release and deploy steps** (polyforge#193): their output schemas are
  enforced (a violation, a missing verdict or an unloadable schema rejects
  the result); a `command_evidence` that is not a list of objects rejects
  the result on any step.
- **Step time budget** (polyforge#192, `v2-converge.md` §2.4): time spent
  paused by an operator never counts toward a step's budget T; a dispatch
  the daemon relaunches after a hang, stall or death does.
- **Claim-time recall**: the memory snapshot's query is the work item's
  goal plus its labels (the content is no longer sent).
- Step processes get `PF_DAEMON_SOCKET` (the daemon's own socket), so a
  step's `pf recall` / `pf remember` reach a daemon on a non-default data
  dir.

## v2.13.0 (work machines)

- **Crash-only shutdown** (polyforge#180): `pf ops shutdown` / SIGTERM
  stops every agent tree (10s), pushes each job's uncommitted task-repo
  work to `refs/pf-wip/<name>` and exits within 30s; running work items
  stay running at aihub and continue at the next start. `last-shutdown.json`
  is no longer written (delete a leftover) and `GET /status` carries no
  `previous_shutdown`; `tools/install.sh` waits 30s for the daemon to stop.
- **WIP at every step end**: the same task-repo WIP push runs after each
  step's process exits, before its result is recorded (bounded by 30s, so
  a hung hub can delay a step's result by up to 30s; a failure never fails
  the step). A `kill -9` loses at most the current step's uncommitted work.
- **Blocked-seal override** (polyforge#187): `pf resume <wi>
  --accept-lost-work` clears a blocked seal (`DAEMON-518`) with a
  `blocked_seal_abandoned` event; deploy aihub before upgrading the CLI
  (the CLI fails when an older hub leaves the seal in place).

## Work item dependencies (polyforge#161)

**Deploy aihub before upgrading the CLI.** `pf file create --after` sends
its blockers in the create body (`after: [...]`); an older aihub ignores
the unknown key and files the work item WITHOUT the dependency, silently.
`pf file dep add|rm|list` and `pf file show` use endpoints every aihub
already has.

- Semantics change: only a **wrapped** blocker releases its dependent. A
  failed or canceled blocker now keeps the dependent queued (before, any
  terminal status released it); remove the edge with `pf file dep rm` to
  proceed. Production held 0 dependency rows, so nothing existing changes.
- A `blocks` blocker must be an execution ticket (bundle bound);
  re-adding an existing edge is a no-op instead of a CORE-500.
- No window SQL: the new index `idx_dep_blocked` is in the DDL that
  `core.Open` runs on every start (`CREATE INDEX IF NOT EXISTS`), so it is
  built on the first boot of the new server. An older server ignores it,
  so a rollback needs nothing either.

## Optional: stale `waiting_on` cleanup (manual, NOT in the window)

The server's `waiting_on` value domain dropped the `gate` and
`runtime:*` branches (polyforge#149) while keeping `owner` and
`dependency:<slug>` live. Rows written before that change may still hold
a now-dead value. The cleanup is OPTIONAL — a stale value is inert (the
row is excluded from the executable queue segments, exactly as before) —
and its WHERE clause is deliberately bounded to the dead values: it must
never touch `owner` or `dependency:*` rows, which are live data. It is
deliberately NOT part of deploy.sh's window SQL:

```sql
UPDATE work_items SET waiting_on = NULL
WHERE waiting_on = 'gate' OR waiting_on LIKE 'runtime:%';
```

## No auto migration

`core.Open` never rewrites an existing database: the DDL is
`CREATE TABLE/INDEX IF NOT EXISTS` — a missing table or index is created,
nothing existing is altered (no column is added or changed; the one
exception is `memories.content`, added by the memory content migration at
aihub start — see "Memories move into the database" below). An old-schema
database is not repaired at boot, and the new server cannot create
projects on it (the dead `projects.identifier_hash NOT NULL` column has
no default). The upgrade is always the deploy.sh window SQL described
above — applied transactionally, probe-guarded and rolled back
automatically by the same tool when the deploy fails.

## Externally visible changes of polyforge#149 (audit subtraction C)

The deletions themselves are in the commits (`git show aff1909 abd463c
e34202a 146b053 1c5e7a3 0cde108 8919250 b267598`); what callers notice:

- the `POST /work-items/{id}/ship-ledger` endpoint answers 404 (delivery
  truth is the git branch);
- blocked work items recover only via `pf resume` or `pf cancel`; `pf
  wrap --fail` on a parked WI fails (the terminate-blocked door is gone);
- no session answer or view carries an owner field (`session_machine`
  stays);
- create/PATCH carry the bundle name only; a PATCH body with a digest key
  is refused CORE-400;
- `X-API-Key` no longer authenticates (Bearer only); the memory index
  rebuild runs at boot only;
- PATCH `waiting_on='gate'` / `'runtime:*'` is refused CORE-400 (`owner`
  and `dependency:<slug>` stay);
- the goal limit counts characters (500), not bytes.

## Work machines: readable capsule directories and ship branches (polyforge#144)

From this release a work machine names what a human sees by the work
item's slug (`<project>#<seq>`, e.g. `polyforge#140`), and the two on-disk
or on-remote names derived from it use the `#`-free form
`<project>-<seq>`:

- **capsule directory**: a NEW claim lands at
  `<workspace>/capsules/<project>-<seq>` (was `capsules/<wi_id>`);
- **ship branch**: `pf ship` pushes `polyforge/<project>-<seq>` (was
  `polyforge/<wi_id>`). (Pre-#166: `pf ship` is removed — see the
  polyforge#166 section below; the `deliver` step pushes the same branch.)

The id stays the machine identifier everywhere else (API, `state.json`
keys, `capsule.json`, the external git-dir, the capsule remote).

**Nothing is renamed or migrated.** A work item that is running, paused or
end_pending when the daemon is upgraded keeps the capsule directory its
`state.json` record names and continues there (restart recovery, attach,
`pf resume`). A blocked work item resumed after the upgrade is re-claimed
into the new `capsules/<project>-<seq>` directory (cloned from its capsule
chain, as every re-claim is); its old `capsules/<wi_id>` worktree and the
id-keyed external git-dir `<data-dir>/git/<wi_id>.git` are moved aside
together, as one unit, to `capsules/<wi_id>.quarantined.<ts>` and
`git/<wi_id>.git.quarantined.<ts>` (never deleted). (That one-time
quarantine of the id-named sibling is gone since the v1→v2 compat
cleanup — see "Compat shims removed" at the end.) Apart from that
re-claim, the daemon never looks for an id-named directory again:
`pf ship` and `pf watch` of a closed work item find only
`<workspace>/capsules/<project>-<seq>`, and there is no "accept both
names" fallback for directories or branches.

**Before upgrading a work machine:**

1. Let every work item claimed on it finish — or at least close it — and
   **ship every closed work item you still intend to ship** before the
   upgrade. After it, `pf ship` of a work item whose capsule is still
   `capsules/<wi_id>` answers `DAEMON-404`, and a work item that was
   already shipped would otherwise open a second PR from the new
   `polyforge/<project>-<seq>` branch beside the old `polyforge/<wi_id>`
   one.
2. Upgrade the binary and restart the daemon as usual.

**Leftovers after the upgrade.** Existing `polyforge/wi_*` branches and
their PRs are left untouched. An id-named capsule directory of a closed
work item is no longer found by any daemon face; to ship it anyway, rename
it by hand while the daemon holds no job for it
(`mv capsules/<wi_id> capsules/<project>-<seq>`), or bridge its commits
manually; otherwise delete it once it is no longer needed.

## `pf ship` removed: delivery is the workflows' `deliver` step (polyforge#166)

The daemon no longer pushes deliveries: the `pf ship` verb, the daemon's
`POST /jobs/{wi}/ship` route and the `DAEMON-512` push-preflight code are
gone (`pf ship` now answers "unknown command"). The delivery lanes
(`feature-full`, `bug-fix`, `autopilot`, `chore`) instead end with a
`deliver` workflow step that merges the latest origin default branch,
pushes only the feature branch `polyforge/<project>-<seq>`, opens or reuses
its PR, and auto-merges it (`gh pr merge --merge`) when review and tests
passed with no LLM-resolved conflict (autopilot, which has no review step,
on verification's pass) — otherwise it asks a human-only
question answered via `pf answer`. The section above describes the
pre-#166 `pf ship`; a closed work item that was never shipped before the
upgrade has to be bridged manually (push its sealed capsule commits by
hand).

`deliver` runs at the end of EVERY work item on those four lanes, so before
upgrading check each work machine and each task repo:

- the machine's git push identity and `gh` login work for the step agents
  (they inherit the daemon's environment: `HOME`, SSH agent, `gh`'s login);
- a task repo whose origin is not on GitHub, or a machine without a usable
  `gh`, gets no PR: the step pushes the feature branch only and then asks
  the human question (merge by hand, or don't merge) — it does not block;
- the `polyforge/<project>-<seq>` branches it pushes are the only refs it
  writes; nothing merges without a PR (`gh pr merge --merge`) or a human's
  answer.

## Workflow structure simplification (polyforge#165)

`feature-full` drops from 11 to 9 steps: `e2e-baseline` merges into
`e2e-author` and `e2e-run` merges into `verification`; every delivery
bundle now runs `verification → review → deliver` (`chore` swapped its
`review` and `verification`). No migration is shipped; what an in-flight
work item meets depends on how its driver next starts:

- **Same-daemon recovery** (daemon restart, crash recovery, a paused work
  item re-attached by its holder) relaunches from the capsule's own
  materialized bundle snapshot, never from the checkout: the work item
  finishes on the bundle it was claimed on.
- **Re-claim** (`pf resume` of a blocked work item, or a claim by another
  machine) clones the capsule fresh and materializes the daemon's
  CURRENT `--workflows-dir` checkout. The `execution.json` of every
  claimed `feature-full` work item carries an entry for all 11 old steps
  (the driver seeds them at the first start, whatever the position), so the
  driver refuses the start and the work item lands `blocked` with the
  reason `execution.json records step(s) "e2e-baseline", "e2e-run" that
  workflow bundle "feature-full" no longer has …`. Resuming it again
  repeats the refusal; cancel it and re-file. A `chore` work item keeps
  its step ids and continues in the new order.

Therefore update the workflows checkout only when no `feature-full` work
item is claimed and unfinished (queued ones are fine: they have no
`execution.json` yet), or accept that a blocked one must be re-filed.
`pf queue` lists them.

## Step `kind` removed; `.polyforge.yaml` is `steps.<project>.<step-id>.models` (polyforge#169)

The step `kind` (`qa`/`spec`/`author`/`code`/`gate`/`review`) is gone: the
shipped bundles no longer declare it and the driver no longer validates
it. Its only use was grouping steps for the workspace yaml's model lists.
The workspace yaml is now keyed by **step id**, under a new top-level
`steps:` section:

```yaml
steps:
  <project>:
    default:   { models: [...] }
    <step-id>: { models: [...] }
```

A step-id key governs the step with that id in every bundle that has one
(`fix` covers bug-fix's and autopilot's `fix`). A step entry's only key is
`models` (polyforge#159 briefly added `harness`; polyforge#175 removed it
again — a residual `harness` key is refused with the retired-harness hint,
see "Per-step harness" below); any other key is refused, naming it. YAML anchors and aliases work as usual
(`models: &x [...]`, `{ models: *x }`).

**No backward compatibility — edit all three workspace files together
with the deploy.** A file that still has the top-level `models:` section
is refused at every claim (DAEMON-414: `unknown top-level key "models"
(only `steps` and `memory_policy`)`; earlier versions added a migration hint, dropped by
decision 2026-10-16 G10 once every workspace was rewritten), the work
item stays queued, and `pf doctor` reports the same under `workspace
yaml`. Deploying this version therefore requires rewriting ws-a,
ws-b and ws-c's `.polyforge.yaml` (below) at the same time; until a
file is rewritten, no work item of that workspace's projects is claimed
on that machine.

Typo protection: every non-`default` key must be a step id of the
workflows the daemon runs (its `--workflows-dir`). Any other key refuses
the claim with DAEMON-414 before anything is claimed — the work item
stays queued — and `pf doctor` names every unknown key
(`steps.<project>.<key>`). A workflows dir with no readable bundle
refuses with "no workflows found in <dir>". So a retired kind that is
not also a step id (`qa`, `author`, `gate`) refuses; `code`, `spec` and
`review` load but mean only the step with that id. Likewise every
project key under `steps:` must be a project cli.toml maps to that
workspace, and a `models` list's ids are trimmed and must be non-empty,
unique strings (the `pf submit --models` rules) written as plain
strings — numbers, YAML tags and aliased items (`[&m a, *m]`) are
refused. YAML merge keys (`<<`) are refused at every level; share lists
with anchors instead (`models: &x [...]` / `{ models: *x }`, as in the
blocks below). The file must hold exactly one YAML document.

Step-id keys are checked against the workflows dir at every claim, not
once: before deploying a workflows release that removes or renames a
step id named in a workspace yaml, delete or rename that key first
(`pf doctor` flags it) — otherwise every claim in that workspace is
refused (DAEMON-414), and a work item whose claim was already open when
the daemon restarted (claiming-journal replay) ends `blocked` instead.
The same holds for the `models:` → `steps:` rewrite above.

Old bundle snapshots: a capsule claimed before the upgrade carries a
materialized bundle whose frontmatter still says `kind: …`. The parser
ignores the field, so same-daemon recovery of such a capsule loads it
unchanged. The model lists such a work item uses were composed at claim
time and snapshotted, so it keeps them (recovery never re-reads the
yaml).

### Old kind → step ids (current bundles)

| old kind | step ids it covered |
|---|---|
| `qa` | `grill` (feature-full), `grill-review` (feature-full), `root-cause` (bug-fix) |
| `spec` | `spec` (feature-full), `grill` (autopilot) |
| `author` | `plan` (feature-full, chore), `e2e-author` (feature-full) |
| `code` | `code` (feature-full, chore), `repro` (bug-fix), `fix` (bug-fix, autopilot), `deliver` (feature-full, bug-fix, chore, autopilot), `release`, `deploy` (release) |
| `gate` | `verification` (feature-full, bug-fix, chore, autopilot), `verify` (release) |
| `review` | `review` (feature-full, bug-fix, chore) |

`grill` is one key for both lanes (it was `qa` in feature-full and `spec`
in autopilot).

### The three workspaces

The #169 rewrite of ws-a, ws-b and ws-c is superseded: use the
prefixed lists in "Rewriting the three workspaces" under polyforge#175
below (they carry both the `steps:` shape and the `pi:` / `claude:`
prefixes). The precedence line for a file header is in the README's
"Extending › Model candidates" subsection.

## Per-step harness: pi or Claude Code (polyforge#159)

> **Superseded in part by polyforge#175** — see "Model candidates name
> their harness: `<harness>:<model>`" below. #159's `harness` key (yaml
> and bundle), its separate harness chain, the id-shape compatibility
> rule and the "model/harness mismatch" refusal are **removed**: a step
> runs on Claude Code when its candidate carries the `claude:` prefix.
> What a claude step runs, its credentials and `claude_bin` below still
> hold.

### What a claude step runs

```
claude -p --bare | --safe-mode
  --output-format stream-json --verbose --include-partial-messages
  --session-id <uuid> | --resume <uuid>  [--model <claude id>]
  --tools Bash,Read,Edit,Write,Glob,Grep,TodoWrite,Agent --allowedTools <same>
  --permission-mode dontAsk --permission-prompts none
  --strict-mcp-config --disable-slash-commands
```

with the capsule as cwd and the step prompt (the same text pi gets) on
stdin. The context flag follows the auth mode of
`<harness-root>/claude-config/settings.json`, read at every spawn (fresh
and `--resume`, so an edit applies to the next step without a daemon
restart):

- **Token mode** — its `env` block carries a non-empty
  `ANTHROPIC_AUTH_TOKEN` or `ANTHROPIC_API_KEY`: `--bare` only (no hooks,
  plugins, LSP, auto-memory, CLAUDE.md discovery or keychain).
- **OAuth mode** — no non-empty token key, no settings.json, or no
  harness root: `--safe-mode` only (every customization off: CLAUDE.md,
  skills, plugins, hooks, MCP servers, custom commands and agents, output
  styles, workflows). `--bare` would skip the keychain the login lives in.
- A settings.json that exists but is unreadable, is not JSON or has no
  `env` block refuses the spawn with an error naming the file (`"env": {}`
  makes it tokenless).

Either way `--strict-mcp-config` and `--disable-slash-commands` keep the
human's MCP servers and skills out. Background tasks are off
(`CLAUDE_CODE_DISABLE_BACKGROUND_TASKS=1` and no background tool in
`--tools`). The native session stays at
`~/.claude/projects/…/<uuid>.jsonl` and is copied into the capsule's
`session/` at every commit boundary; an answered question resumes the
same session (`--resume`).

**Credentials are the human's.** The daemon never writes or manages a
credential and injects none itself — the step's credential authority
layer is its own `<harness-root>/claude-config/settings.json`
(polyforge#171; see the later section), and every inherited
`ANTHROPIC_*` is stripped from the step's environment. The adapter
hard-sets `CLAUDE_CONFIG_DIR` to the claude-config dir, so in token mode
the settings env block is what a bare step applies, and in OAuth mode the
step uses the Claude Code login of that config dir (log it in on the work
machine beforehand). The daemon reads the env block only to pick the
mode, never reporting a value, so it CAN see that layer from the machine
it runs on.

New cli.toml keys (flat, optional):

```toml
claude_bin = "/root/.local/bin/claude"     # default: claude on the daemon's PATH (or --claude-bin)
```

`pf doctor` adds a `harness claude` check whenever any step resolves to
claude: the binary must resolve on the daemon's PATH (else fail); the
version should be at least the verified minimum (2.1.296 — newer
passes, older or unparsable warns); when no credential is
visible — neither the daemon env nor the claude-config
`settings.json` env block (the daemon reports set/unset, never a
value) — it warns too. Credential values are never shown. The check
lists every claude step with the level its harness and models came
from.

## CC harness auth + directory unification (polyforge#171, #159 closeout)

The v2.5.2 per-step claude harness carried the credential through a
cli.toml key AND scattered the step harness directories over the data
dir and the daemon user's `~/.claude`. The closeout unifies them: the
`settings.json` of the harness root's `claude-config` dir is the single
credential authority layer, and all three step-harness directories
derive from one root.

### The daemons move

- `--harness-sessions-root` is REMOVED. Its old semantics ("the pi
  session dir itself") made an upgrade silently re-home the live
  transcript sync, so the flag was renamed instead: the new daemon
  refuses the old value with the unknown-flag error.

- `--harness-root` (env `POLYFORGE_HARNESS_ROOT` — removed since, see
  "Daemon knobs move into cli.toml" at the end; default
  `<data-dir>`, higher than DataDir) is now the PARENT root of the
  three harness dirs:

      <harness-root>/step-agent            pi step agent dir (the dedicated minimal set)
      <harness-root>/step-agent-sessions   pi step sessions (the live-transcript sync's source)
      <harness-root>/claude-config         claude config dir (settings.json, projects/, debug/)

  The default keeps the byte-identical step-session and step-agent dirs
  under `<data-dir>`; only the claude dir is new and only the explicit
  override moves all three together.

### The credential key moves

- cli.toml's `anthropic_auth_token` is HARD-DELETED (no deprecation
  path). A residual key fails the boot with the unknown-key parse
  error — that is the intended migration story, not a bug:

      pf daemon: config ~/.config/polyforge/cli.toml: unknown key "anthropic_auth_token" in …

- Migrate (manual, ONCE): the claude step reads its credentials from
  the settings `env` block only — copy ONE credential pair from wherever
  it lives today (the daemon user's `~/.claude/settings.json` or a
  cli.toml you never checked in) into
  `<harness-root>/claude-config/settings.json`:

      {
        "env": {
          "ANTHROPIC_AUTH_TOKEN": "…",
          "ANTHROPIC_BASE_URL": "…"        // optional — paired with the token
        }
      }

  Copy only the two keys; never write credentials into project-level or
  capsule settings (a capsule is sealed and distributed), and NEVER set
  `CLAUDE_CONFIG_DIR` inside the settings `env` block: the adapter
  hard-sets the step's config dir and a value there would override it.

- The adapter injects ZERO credentials henceforth: no token rides the
  environment or argv from the daemon. The daemon-started
  `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1` and the claude-config
  `CLAUDE_CONFIG_DIR` are added, and the operator's inherited config-dir
  value is stripped (it is hard-set). Every inherited `ANTHROPIC_*` is
  stripped as well (env hygiene, below).

### The pairing table (generic wording)

| what the settings env block carries | what a claude step does |
| --- | --- |
| gateway-credential + gateway address | gateway profile |
| subscription long-lived token (`claude setup-token`, `sk-ant-oat01-`, ~365 days) with NO gateway address | subscription direct profile |
| subscription long-lived token with ANY gateway address | REJECTED combination |
| no token at all | the step cannot authenticate (it will not run) |

Notes: `claude login`'s interactive credentials do not serve headless
steps — mint a long-lived token with `claude setup-token` and put it in
the settings env block; it is valid about a year, so re-mint before it
expires. An `ANTHROPIC_BASE_URL` in the DAEMON environment is NOT inherited
(env hygiene: claude steps strip every inherited `ANTHROPIC_*`); the rejection clause above is the doctor's
pairing clause — token shape and gateway address TOGETHER, values
never printed.

### The doctor sees the authority layer

`pf doctor`'s `harness claude` now reports (daemon-reported
harness-root layout, polyforge#171):

- the three dirs' existence (present/missing) under the daemon's
  harness root;
- the claude-config `settings.json` env block's key facts
  (ANTHROPIC_AUTH_TOKEN / ANTHROPIC_BASE_URL / CLAUDE_CONFIG_DIR) as
  set/unset — values are read in-memory, NEVER printed;
- the pairing clause above whenever the shape says rejected, and a
  remove-it clause whenever the settings env block carries
  `CLAUDE_CONFIG_DIR` (it would override the adapter's value).

## Model candidates name their harness: `<harness>:<model>` (polyforge#175)

polyforge#159 made the harness a per-step setting (a `harness` key in the
workspace yaml and the bundles) and guessed each candidate's harness from
the shape of its id (`claude-…` → claude, anything else → pi). That
could not express one step trying Claude Code first and pi second, and
the same model can run on both harnesses (Opus through pi + example-gateway, or
through Claude Code's own API), so the guess was unreliable. The harness
is now part of every candidate.

### The format

Every model candidate is `<harness>:<model>`:

- `harness` is `pi` or `claude` (closed set);
- the prefix is case-sensitive: `PI:` / `Claude:` are unknown prefixes;
- the string is split on the **first** `:` — the model keeps any `/` and
  any further `:` (`pi:ollama/llama3:8b` is pi running `ollama/llama3:8b`);
- the model part is exactly what that harness's `--model` gets
  (`claude:claude-opus-5-5` runs `claude … --model claude-opus-5-5`,
  `pi:example-gateway/model-a` runs `pi … --model example-gateway/model-a`).

The prefix is **mandatory** and is the whole truth: there is no inference
from the id any more (`pi:claude-opus-5-5` is a pi candidate), no
`harness` key and no "this harness serves none of the list" check. One
format everywhere:

| where | key |
|---|---|
| `.polyforge.yaml` | `steps.<project>.<default\|step-id>.models` |
| bundle frontmatter | step `model_candidates`, `defaults.model_candidates` |
| CLI | `pf submit <id> --models claude:claude-opus-5-5,pi:example-gateway/model-a` |

The precedence of the model chain is unchanged: `pf submit --models` >
`steps.<project>.<step-id>.models` > bundle step `model_candidates` >
`steps.<project>.default.models` > bundle `defaults.model_candidates`.
A step with no list (or a declared-empty one) runs pi's own default model.

A chain may **mix harnesses**: the fallback walks the candidates in order
(D7 unchanged: switch on auth / billing / unknown model, same-model retry
on rate limit / 5xx), and each dispatch runs on its candidate's harness.
A switch always starts a fresh session; when the next candidate is on
another harness the previous dispatch's transcript (the other harness's
JSONL) rides the new prompt as read-only reference — the #159 mechanism.
Same-step continuations (an answered question, the first retry after a
crash) still resume the same session when the harness is unchanged.

### Refusals (no backward compatibility)

| what | where it refuses | message (example) |
|---|---|---|
| a candidate without a prefix, with an unknown prefix, or with an empty model | `.polyforge.yaml`: every claim of that workspace's projects, DAEMON-414 before anything is claimed (the work item stays queued); `pf doctor` `workspace yaml` fails the same | `steps.polyforge.code.models[1]: "example-gateway/model-a" needs a harness prefix (pi:… or claude:…)` |
| the `harness` key in a yaml step entry | same as above | ``steps.polyforge.fix.harness: the `harness` key was removed (polyforge#175) — move it into each model candidate's prefix, e.g. [claude:claude-opus-5-5, pi:example-gateway/model-a] (see docs/upgrade.md)`` |
| an unprefixed `model_candidates` entry, or a `harness` key under a bundle step or `defaults` | the bundle fails to load: a claim of a work item on it is refused before it is made (DAEMON-414, the work item stays queued; `pf submit` gets the refusal directly and auto-pull skips such work items, logging the refusal once; should the bundle only be known after the claim, the claim ends `blocked` with the same text); `pf doctor` `workspace yaml` names file and step. The bundle's step ids still count as legal yaml keys | `workflow bundle bug-fix refused: …/bug-fix/workflow.md: step root-cause: model_candidates[0]: "example-claude/claude-opus-5" needs a harness prefix (pi:… or claude:…)` |
| `pf submit --models` with an unprefixed candidate | the CLI, before any request (the daemon route refuses the same with DAEMON-400) | `submit: --models: "example-gateway/model-a" needs a harness prefix (pi:… or claude:…)` |

So, **as with #169, edit all three workspace files together with the
deploy**: until a file is rewritten, no work item of that workspace's
projects is claimed on that machine. The five shipped bundles are
already rewritten (every candidate `pi:` — they ran on pi before).

In-flight work items are unaffected: their candidate lists were
snapshotted at claim time in the daemon's `state.json`. (The read-compat
for records and `--models` lists written before this version is gone
since the v1→v2 compat cleanup — see "Compat shims removed" at the end.)

### Before / after

```yaml
# before (#159)
steps:
  polyforge:
    default: { models: [example-gateway/model-a, example-claude/claude-opus-5] }
    review:  { models: [claude-opus-5-5, claude-sonnet-5], harness: claude }

# after (#175)
steps:
  polyforge:
    default: { models: [pi:example-gateway/model-a, pi:example-claude/claude-opus-5] }
    review:  { models: [claude:claude-opus-5-5, claude:claude-sonnet-5] }
    # new: one step, two harnesses — Claude Code's API first, pi + glm as fallback
    fix:     { models: [claude:claude-opus-5-5, pi:example-gateway/model-a] }
```

```
# before                                        # after
pf submit wi_x --models a/b,claude-opus-5-5     pf submit wi_x --models pi:a/b,claude:claude-opus-5-5
```

A bundle step: `model_candidates: [example-gateway/model-a]` becomes
`model_candidates: [pi:example-gateway/model-a]`; a `harness: claude` on the
step is deleted and its candidates get `claude:`.

`pf doctor`: `harness claude` runs only when some resolved candidate is
`claude:…`, and lists each such step's whole chain (each candidate's
harness is its prefix) with the level it came from; `workspace yaml`
also loads the daemon's installed bundles. The #171 claude-config
authority-layer checks are unchanged.

### Rewriting the three workspaces (examples — edit the files yourself)

The rule: a bare id ran on its step's harness — `harness: claude` when
the entry (or the bundle step) said so, otherwise pi — and #159 silently
**skipped** ids the step's harness could not run (`claude-…` ids on a pi
step, everything else on a claude step). Prefix each id accordingly. An
id that was being skipped can now either be dropped (exactly today's
behaviour) or kept with its real harness (it will then actually run, in
its place in the chain — a behaviour change that adds Claude spend; the
examples below keep today's behaviour and show that alternative as a
comment). Keep each file's header comment but drop any `harness`
wording.

ws-a (`<osp-workspace>/.polyforge.yaml`):

```yaml
steps:
  polyforge:
    default:
      models: [pi:example-gateway/model-a, pi:example-claude/claude-opus-5, pi:example-openai/gpt-x]
    smoke:
      models: [claude:claude-opus-5-5]   # the smoke bundle's step ran on claude
    fix:
      models: [claude:claude-opus-5-5]   # was: harness: claude + [claude-opus-5-5]
    code:
      models: [pi:example-gateway/model-a-flash, pi:example-gateway/model-a, pi:example-claude/claude-opus-5]
  pi:
    default:
      models: [pi:example-gateway/model-a, pi:example-claude/claude-opus-5, pi:example-openai/gpt-x]
    smoke:
      models: [claude:claude-opus-5-5]
    code:
      models: [pi:example-gateway/model-a-flash, pi:example-gateway/model-a, pi:example-claude/claude-opus-5]
  tsshd:
    default:
      models: [pi:example-gateway/model-a, pi:example-claude/claude-opus-5, pi:example-openai/gpt-x]
    smoke:
      models: [claude:claude-opus-5-5]
    code:
      models: [pi:example-gateway/model-a-flash, pi:example-gateway/model-a, pi:example-claude/claude-opus-5]
```

(`smoke` is the step id of a smoke bundle that is not among the five
shipped ones: if that bundle ran the step on pi rather than claude, the
bare `claude-opus-5-5` was being skipped there — write the list that
bundle step should really walk. The bundle itself must also be
rewritten: no `harness` key, prefixed `model_candidates`.)

ws-b (`<gmi-workspace>/.polyforge.yaml`):

```yaml
steps:
  acme-ops:
    default:
      # today's behaviour: #159 skipped claude-opus-5-5 on these (pi) steps.
      # Alternative — keep it as a real Claude Code fallback (adds Claude spend):
      #   models: [pi:example-gateway/model-a, claude:claude-opus-5-5, pi:example-openai/gpt-x]
      models: [pi:example-gateway/model-a, pi:example-openai/gpt-x]
    smoke:
      models: [claude:claude-opus-5-5]
    code:
      # today's behaviour: claude-sonnet-5-5 was skipped likewise.
      # Alternative (adds Claude spend):
      #   models: [pi:example-gateway/model-a, claude:claude-sonnet-5-5]
      models: [pi:example-gateway/model-a]
```

ws-c (`<yf-workspace>/.polyforge.yaml`):

```yaml
steps:
  yingfang:
    default:
      models: [pi:example-gateway/model-a, pi:example-claude/claude-opus-5, pi:example-openai/gpt-x]
    smoke:
      models: [claude:claude-opus-5-5]
    code:
      models: [pi:example-gateway/model-a-flash, pi:example-gateway/model-a, pi:example-claude/claude-opus-5]
```

Run `pf doctor` after the edit: `workspace yaml` must pass, and
`harness claude` must pass wherever a `claude:` candidate remains.

## Thinking effort per candidate: `<harness>:<model>[:<effort>]` (polyforge#176)

Until now polyforge passed no thinking level to a step's harness: each
ran at its own default. A candidate may now end in a thinking effort:

```
<harness>:<model>[:<effort>]       e.g. claude:claude-opus-5-5:high, pi:example-gateway/model-a:low
```

- `effort` is one word of a closed set — `off`, `minimal`, `low`,
  `medium`, `high`, `xhigh`, `max` (lowercase; the union of what the two
  harnesses accept, checked against `pi --help` and `claude --help`).
- **Only the last segment, and only when it is one of those words, is
  the effort.** Every other `:` stays in the model id:
  `pi:ollama/llama3:8b` is the model `ollama/llama3:8b` with no effort,
  `pi:ollama/llama3:8b:high` is that model at `high`. A misspelt effort
  (`:hgih`, `:High`) is therefore part of the model id and fails at the
  harness as an unknown model, not at load. Two trailing effort words
  (`…:high:low`) are refused.
- Mapping (the model id goes to `--model` unchanged either way):

  | harness | accepts | argv |
  |---|---|---|
  | `pi` | `off minimal low medium high xhigh max` | `--thinking <effort>` |
  | `claude` | `low medium high xhigh max` | `--effort <effort>` |

  pi gets the `--thinking` flag rather than its `--model <id>:<level>`
  shorthand, so `--model` stays exactly the model id and both harnesses
  get the same shape (one dedicated flag).
- **No effort = exactly the previous behaviour**: no flag is passed and
  the harness uses its own default.
- The same model at two efforts is two distinct candidates, so a chain
  can escalate: `[pi:example-gateway/model-a:medium, pi:example-gateway/model-a:xhigh]`.
  Switching walks whole candidates, effort included.

The format holds in every place a candidate is written (`.polyforge.yaml`
`steps.<project>.<default|step-id>.models`, bundle `model_candidates`,
`pf submit --models`), with the #175 refusal surfaces: an effort the
candidate's harness does not accept is refused naming the path —

| where | message (example) |
|---|---|
| `.polyforge.yaml` (DAEMON-414 before the claim; `pf doctor` `workspace yaml` fails) | `steps.polyforge.fix.models[0]: "claude:claude-opus-5-5:off": effort "off" is not accepted by claude (it accepts low\|medium\|high\|xhigh\|max)` |
| bundle `model_candidates` (the bundle fails to load) | `step fix: model_candidates[1]: "claude:claude-opus-5-5:minimal": effort "minimal" is not accepted by claude (…)` |
| `pf submit --models` (the CLI, before any request) | `submit: --models: "claude:claude-opus-5-5:minimal": effort "minimal" is not accepted by claude (…)` |

Where the effort shows: the claim-time candidate snapshot keeps the
canonical string (`claude:claude-opus-5-5:high`); `pf doctor`'s
`harness claude` resolution table lists the chains with their efforts;
`pf ops status` gains a CANDIDATE column (shown when a job has a
dispatch in flight on a pinned candidate) naming
`<harness>:<model>[:<effort>]`; the step record in `execution.json`
carries `effort` next to `model`, and the `step_started` event an
`effort` field.

**Upgrade check.** Nothing needs editing: a list without efforts behaves
as before. Look only for a candidate that already ends in one of the
seven words — it now means an effort. `pi:<id>:high` hands pi
`--model <id> --thinking high`, the same as pi's own shorthand did
(unless a model with that full id, `<id>:high`, exists — that id can no
longer be named, see below); a
`claude:<id>:<word>` that used to reach claude as a (non-existent) model
id now runs `<id>` with `--effort <word>`, and with `off` or `minimal`
it is refused (above). An in-flight work item whose snapshot holds such
a refused candidate fails its driver start with an error naming the
step and the candidate (it is not re-read as a pre-#175 bare id).

**Known limit.** A model id that literally ends in an effort word (e.g.
a model named `foo:max`) cannot be named: the last segment is always
taken as the effort (`pi:foo:max` is `foo` at `max`), and
`pi:foo:max:high` is refused as two efforts.

## Work machines: harnesses reference the release; the daemon is supervised (polyforge#174)

Re-running `tools/install.sh` on a machine installed before this change
migrates it in place; a second run changes nothing.

### What the re-run changes

- **pi**: the `git:git@github.com:silgrid/polyforge.git@<ver>` entry in
  pi's settings `packages` (a second, full clone of the repo under
  `~/.pi/agent/git/`) is replaced, in place, by one local entry naming
  `~/.local/share/polyforge/release/current` — the checkout install.sh
  already keeps. pi loads a local package where it lies (no copy); the
  release's `package.json` declares the skill AND the `pf-chat`
  extension. Other packages are untouched; the previous settings file is
  kept as `settings.json.bak-polyforge-<timestamp>`. `pi` itself is no
  longer invoked. Restart the pi sessions (pi-web) to load it. The old
  clone is no longer referenced; remove it once pi runs fine:
  `rm -rf ~/.pi/agent/git/github.com/silgrid/polyforge`.
- **Claude Code**: the copied `~/.claude/skills/polyforge` directory is
  removed and replaced by a symlink to
  `release/current/skills/polyforge`. The SessionStart hook keeps its
  registered path (`~/.claude/skills/polyforge/hooks/claude-session-start.sh`,
  now resolved through the link). Claude Code 2.1.296 accepts symlinked
  skill dirs; confirm once in an interactive session that `polyforge` is
  listed among the skills.
- **daemon**: the upgrade holds `~/.local/share/polyforge/ensure.lock`
  (a harness session starting meanwhile starts nothing), shuts the daemon
  down gracefully (the old `nohup` one included; a loop's daemon takes its
  loop with it on that clean exit) and waits until daemon and loop are
  gone, swaps the binary, then runs `pf daemon ensure`: from now on the
  daemon runs inside the restart loop (a failed exit is restarted within
  ~5s; log
  `~/.local/share/polyforge/daemon.log`, appended, rotated at 10 MiB). When
  the binary is already current, a live daemon is left alone and an
  unsupervised one is reported with the fix:
  `pf ops shutdown && while pf ops status >/dev/null 2>&1 || ! flock -n ~/.local/share/polyforge/daemon.lock true; do sleep 2; done; pf daemon ensure`.
- **order and failure safety** (repo review 2026-10-16 V-3): everything
  that can fail for an outside reason — download, SHA check, the release
  clone into its own `release/<VER>` directory — runs before anything is
  stopped or replaced; such a failure exits non-zero and says that pf,
  `release/current` and the running daemon are unchanged. The new binary
  is staged next to the installed one (`<bin>.new.<pid>`, already
  executable) and swapped by one rename: `pf` is never missing, partial
  or non-executable.
  An abort after the daemon was asked to stop reports the state it
  leaves (daemon stopped, the binary in place, where `release/current`
  points) and what to run: re-run the installer, or `pf daemon ensure`.
- **running jobs refuse the upgrade**: a binary upgrade needs the
  graceful shutdown, which terminates every live agent tree. While
  `pf ops status` lists a job that is running, recovering or sealing
  (`paused` and `end_pending` have no agent tree and do not count; `claiming` does), or
  while the daemon's job list cannot be read, install.sh lists the
  jobs, changes nothing (the fetched `release/<VER>` checkout is kept for
  the next run) and exits non-zero. Wait for them, pause them
  (`pf pause <wi>`), or override — the interrupted work items recover at
  the next daemon start (with `PF_KEY` read as in the install example):
  `curl ... | PF_INSTALL_FORCE=1 bash -s -- "$PF_KEY"`.
- **after a container / machine restart** tmux is gone too: the next
  Claude Code session start (SessionStart: startup/resume/clear/compact) or
  pi session start (pi-web replays it when it resumes its sessions) runs
  `pf daemon ensure` and brings the daemon back. Until then auto-pull does
  not run — the accepted cost of not touching the container's startup.
- **`pf ops shutdown` still stops** the daemon for good: the loop restarts
  only a failed exit (crash, panic, kill); a clean exit (`pf ops shutdown`,
  SIGTERM) ends the loop too. Start it again with `pf daemon ensure`.

### v1 leftovers

`pf doctor` (and the installer's harness rows) now warn about v1
polyforge artifacts still installed — the v1 aihub bridge as a pi package
(`silgrid/polyforge`) or at `~/.pi/agent/extensions/polyforge/`, v1
Claude skills (`using-polyforge`, `pf-*`) and v1 Claude plugins — and
print one command line that first backs them up into
`~/polyforge-v1-backup-<timestamp>/`, then removes them. Nothing is
removed automatically. Restart the harness sessions afterwards. The
check is migration hygiene and is removed at v3.0.

## Workspace runtime-artifact layout (polyforge#177)

Shared content — designs, reviews, decision sheets, audits, experience —
belongs in aihub; a workspace keeps locally only `.repo/`, `capsules/`,
`.polyforge/` (polyforge-generated only) and
`scratch/` (disposable). The README's "Workspace layout" paragraph has
the full convention. Per existing workspace, after upgrading `pf`:

1. `pf init <workspace>` — creates `scratch/` and adds the layout
   section to the workspace notes file, `AGENTS.md` when it exists, else
   `CLAUDE.md` (inside the existing `polyforge:managed` block; the human
   part is untouched; safe to re-run). See "The workspace notes file is
   AGENTS.md, else CLAUDE.md" below.
2. `pf doctor` — the `workspace layout` row lists every root entry and
   every `.polyforge/` entry outside the convention, with its
   destination. Nothing is moved for you.
3. Move each document listed into aihub. A design whose work item is
   still open goes into its content; anything else becomes a memory
   (memories cannot be edited later — only re-stored or redacted):

   ```
   pf file edit polyforge#160 --content @.polyforge/160-design.md            # open work item
   pf remember --project polyforge --type doc.design   --work-item polyforge#159 --title "#159 design" @.polyforge/159-design.md
   pf remember --project polyforge --type doc.review   --work-item polyforge#160 --title "#160 review" @.polyforge/160-review.md
   pf remember --project polyforge --type doc.decision --title "owner decisions 2026-10" @.polyforge/decisions.md
   pf remember --project polyforge --type doc.audit    --title "invented-logic audit" @.polyforge/audit.md
   ```

   A terminal work item refuses the content edit (`CORE-410`) — use the
   `doc.design` memory with `--work-item` instead. Check with `pf recall
   --project <p> --type doc.design "<title words>"` (or `doc.review`,
   …), then delete the local file.
4. Move logs, briefs, downloads and `.polyforge.yaml.bak-*` backups into
   `scratch/`. `.polyforge/repo-map/` and `.polyforge/watchers/` are
   legacy v1 artifacts — nothing in v2 generates or reads them; doctor
   tolerates them, and you may delete them (CLAUDE.md lines pointing at
   `repo-map/*.md` then dangle — drop them too). `pf doctor` reports scratch entries older than 14 days;
   `pf doctor --prune-scratch` deletes them and lists what it deleted.

## The workspace notes file is AGENTS.md, else CLAUDE.md

`pf init` and `pf doctor`'s `workspace layout` row used to know only
`CLAUDE.md`. Both now resolve the notes file with one rule: `AGENTS.md`
when it exists, else `CLAUDE.md`; a workspace with neither gets
`AGENTS.md` from `pf init`. A workspace that has only `CLAUDE.md` behaves
as before. Nothing is migrated:

- A workspace with only `AGENTS.md` no longer gets a second `CLAUDE.md`
  holding just the managed block. If an earlier `pf init` created one,
  `pf doctor` now warns about its duplicate managed block and `pf init`
  refuses (non-zero exit, nothing written) until you remove that block
  from `CLAUDE.md` by hand (or delete the file if it holds nothing
  else). A `CLAUDE.md` with no managed block, such as an `@AGENTS.md`
  import, is fine.
- `AGENTS.md` and `CLAUDE.md` linked to the same file count as one file.
- `pf init --json` reports the notes outcome as `notes_file` (was
  `claude_md`; same values) plus `notes_path`, the file chosen. Update
  any script that reads `claude_md`.

No schema change, no aihub change; the upgrade is the `pf` binary alone.

## Hub git calls ignore the operator's git config (repo review 2026-10-16 V-5)

The daemon's git calls
that talk to aihub with the session credential — the capsule chain push, the
capsule clone and the carrier ls-remote/fetch/push — run with no system or
global git config (`GIT_CONFIG_NOSYSTEM=1`, `GIT_CONFIG_GLOBAL=/dev/null`) and
with every inherited `GIT_*` variable stripped. So `http.proxy`,
`http.sslCAInfo`, `url.*.insteadOf` and `credential.helper` from
`~/.gitconfig` do NOT apply to them: give the daemon a proxy through
`https_proxy` / `HTTPS_PROXY` in its environment, and since `GIT_SSL_CAINFO`
is stripped too, an aihub behind an internal CA needs that CA in the system
trust store. The capsule commit itself still reads the global config, so
`user.name` / `user.email` must be set for the daemon user; task-repo git
(inside `repos/<name>`) also still uses the global config.

Before this change an operator's `~/.gitconfig` could reroute the
credentialed push (`url.*.insteadOf`) or hand the session secret to a
storing `credential.helper`. Check before upgrading a work machine whose
aihub access depends on a git-config proxy or CA setting: move it to
`https_proxy` / the system trust store first, or the chain push and
re-claim clone will fail.

## Bundle `quick-fix` renamed to `autopilot` (repo review 2026-10-16 V-7)

The light remediation bundle is now `workflows/autopilot/` (frontmatter
`name: autopilot`). Behaviour is unchanged — the new name says what the
old one hid: with no grill question asked and its gates passing, its
`deliver` step merges without any human. Step ids are unchanged (`grill`,
`fix`, `verification`, `deliver`), so `.polyforge.yaml` entries keyed
`steps.<project>.fix` (and the other step ids) resolve exactly as before;
nothing in the workspace yaml names a bundle.

The daemon resolves a work item's bundle by name in its workflows
checkout, and the old name no longer exists there. Before deploying:

- **Queued, never-claimed work items bound to `quick-fix`**: re-bind them,
  `pf file edit <wi> --bundle autopilot`. aihub accepts a `bundle_name`
  PATCH exactly while the work item is queued and has never been claimed
  (the R2 window); list them with `pf queue`.
- **Work items that were already claimed** (running, paused, blocked, or
  queued again after a claim) cannot be re-bound — `bundle_name` froze at
  the first claim. Let them finish before the upgrade, or cancel them and
  file again with `--bundle autopilot`.


## Bundle simplification: shared step files and dead keys removed (decision 2026-10-16 G1/G10)

**Shared step definitions (G1).** `deliver.md` now exists once, at
`workflows/_shared/steps/deliver.md`. The driver reads a bundle's own
`steps/<id>.md` when it has one and falls back to
`workflows/_shared/steps/<id>.md` when it does not; the daemon copies
`workflows/_shared/` into every new capsule beside the bundle. `_shared`
is not a bundle: a work item bound to it is refused before the claim.
**The `pf` binary and the workflows checkout must move together.** An
older binary running against the new checkout cannot find the shared
`deliver.md`, so every delivery lane fails at its `deliver` step.
`tools/install.sh` swaps both together; do not update only the checkout
under `release/current`.

**Shared verification and review (G1 follow-up).** `verification.md` and
`review.md` now also live once under `workflows/_shared/steps/`; their
lane-specific text comes from the new `vars:` map on each bundle's
`workflow.md` step entry, rendered into `{{name}}` placeholders by the
driver. This raises the same rule: an older `pf` binary does not render
`vars`, so against the new checkout it would dispatch verification and
review with raw `{{name}}` placeholders, and verification's output schema
(whose `required` list is a placeholder) would not load.
Upgrade the binary and the checkout together (`tools/install.sh`).
Capsules claimed before the upgrade keep their own snapshot and are
unaffected.

**Dead bundle keys removed (G10).** Step files no longer carry `tools:`.
Seven steps whose handoff file nobody read no longer declare
`handoff: true`. The release bundle's inert `rewind: 1/1` is gone. The
evidence rule changed: the step id `verification` no longer implies
`requires_command_evidence`. The four delivery bundles now declare it
explicitly, so new claims behave as before.

**Gap for work items claimed before this release:** when the same daemon
recovers such a work item, it reuses that capsule's own copy of the old
`workflow.md`, where `verification` does not declare the flag. Its
verification step therefore no longer requires command evidence. Deploy
when no claimed work item still has `verification` ahead of it (check
with `pf queue` and `pf watch`), or accept that those few runs skip the
evidence gate. A work item claimed fresh after the upgrade gets the new
bundle and is not affected.

## Shared engineering principles in step prompts (polyforge#191)

- `workflows/_shared/principles.md` is rendered into the grill, spec,
  plan and code-like steps of every bundle and into the shared `review`
  (each adds at most 1.5 KB to its prompt). Those steps first read each
  task repo's `AGENTS.md` (else `CLAUDE.md`); project rules win on a
  conflict. Put a project's own rules there, not into `workflows/`.
- The review checklist no longer carries "no backward-compatibility
  code" or the daemon/aihub entry-point reachability item: both are
  polyforge's own rules, now in this repository's `AGENTS.md`. It gains
  generic checks (unruled behavior change, I/O under a lock, weak tests,
  stale docs, leaked credentials, guesses stated as facts).
- Nothing to configure. A capsule materialized earlier keeps its own
  snapshot and runs as before.

## Compat shims removed (decision 2026-10-16 G5)

The remaining v1→v2 / pre-#175 read-compat code is deleted ("v2 keeps
no backward-compatibility code"). **Before upgrading a work machine,
let every work item it holds finish, or at least close it**: `pf ops
status` must list no job. A machine that still holds a record from
before polyforge#175 or #144 fails it loudly instead of converting it:

- a `state.json` record whose `step_candidates` are bare ids (pre-#175,
  with or without the #159 `step_harness`): the ids are no longer
  prefixed on read; the driver refuses to start the work item, naming
  the candidate. Resubmit the work item.
- a `pf submit --models` list recorded in `manual-models.json` before
  #175 (bare ids): a re-claim no longer drops it (the
  `manual_models_dropped` event is gone); the driver refuses it by name
  and the claim ends blocked.
  Resubmit with a prefixed `--models` list.
- a `state.json` record without `capsule_dir`: restart no longer derives
  a directory name from the slug; the work item is a missing capsule
  (blocked, as any missing capsule).
- a re-claim no longer looks for, or quarantines, an id-named
  `capsules/<wi_id>` sibling from before #144; remove such leftovers by
  hand.
- capsules materialized by older daemons keep their (now unused)
  `agents/` directory and the marker's `adapters` key; both are ignored
  (decision 2026-10-16 G4).

## Daemon knobs move into cli.toml (decision 2026-10-16 G2)

The daemon is started only by `pf daemon ensure`, whose argv is fixed,
so the daemon's tuning flags and `POLYFORGE_*` variables never reached a
production daemon. Five knobs now live in a `[daemon]` table of
`cli.toml`; the daemon reads it from its `--config` file (ensure's argv
is unchanged). Precedence: an explicit daemon command-line flag
(tests/dev) > the table > the built-in default.

```toml
[daemon]                              # after the flat keys
step_timeout = "45m"                  # T, Go duration > 0
stall_window = "10m"                  # N, non-zero; negative disables
harness_root = "/srv/polyforge/h"     # absolute path
operator_agent_dir = "/home/u/.pi/agent"  # absolute path
max_concurrent_jobs = 6               # integer >= 1
```

- Every `pf` command, ensure and the daemon validate the table on load: a
  bad value, an unknown key or a duplicate refuses with `config <file>:
  line N: [daemon] <key>: <reason>` (ensure starts nothing, the daemon
  exits 78), and `pf doctor`'s `config` row shows it.
- A change applies at the next daemon start: `pf ops shutdown`, wait
  until `pf ops status` fails, `pf daemon ensure`.
- **Job cap.** `max_concurrent_jobs` is the configured cap; `pf ops max N`
  overrides it until the daemon restarts (polyforge#183, below).
- **Downgrade:** binaries older than this version refuse a `[daemon]`
  table (`unknown section`) — every `pf` command and the daemon fail to
  load the config. Remove the `[daemon]` table from `cli.toml` **before**
  downgrading below this version, including a `deploy.sh --force`
  rollback or an `install.sh` run pinned to an older release.
- **Removed environment fallbacks**: `POLYFORGE_MACHINE_ID`,
  `POLYFORGE_DISPLAY_NAME`, `POLYFORGE_HARNESS_ROOT`,
  `POLYFORGE_OPERATOR_AGENT_DIR`, `POLYFORGE_RECALL_TOPK`. They only
  ever worked when the tmux server happened to carry them. A machine
  that relied on `POLYFORGE_HARNESS_ROOT` or
  `POLYFORGE_OPERATOR_AGENT_DIR` must move the value into the table
  **before** restarting the daemon, or the daemon falls back to
  `<data-dir>` / `~/.pi/agent`. So that this never happens silently, the
  daemon warns at boot when `POLYFORGE_HARNESS_ROOT` or
  `POLYFORGE_OPERATOR_AGENT_DIR` is set to a value differing from what it
  resolved (stderr in `daemon.log`, a machine-level
  `retired_env_ignored` event, and `env_warnings` in `GET /status`), and
  `pf doctor`'s `retired env` row warns about the daemon's report and
  about either variable set in your own shell. `POLYFORGE_DATA_DIR` stays
  (ensure and the daemon both read it).
- The other daemon flags (`--silence-window`, `--tool-window`,
  `--model-error-grace`, `--revalidate-every`, `--heartbeat-every`,
  `--pi-bin`, `--git-remote-base`, `--recall-topk`, `--display-name`,
  `--auto`) stay, documented as test/dev-only: nothing in production
  sets them.

## Daemon environment hygiene (supersedes repo review V-11)

`pf daemon ensure` used to start the daemon in the `polyforge-daemon`
tmux session with the tmux SERVER's environment (captured when that server
first started, often from a human's shell), so the daemon — and every step
it spawned — could inherit `ANTHROPIC_AUTH_TOKEN` / `ANTHROPIC_BASE_URL` /
`ANTHROPIC_*MODEL` of an unrelated gateway or a stale API key.

- The daemon's environment is now an explicit allow-list (see the README
  daemon section; defined in `internal/polyforge/supervisor/env.go`)
  taken from the shell that runs `pf daemon ensure`. A proxy, CA bundle or
  locale the daemon needs must be set there; anything else (`PF_*`,
  `ANTHROPIC_*`, retired `POLYFORGE_*`) no longer reaches the daemon.
- Claude steps strip every inherited `ANTHROPIC_*`: put every
  `ANTHROPIC_*` a claude step needs (token, base URL, model defaults) in
  `<harness-root>/claude-config/settings.json`'s `env` block. A setup that
  relied on a daemon-env token no longer authenticates — `pf doctor`'s
  `harness claude` row warns.
- pi steps see only the daemon's environment too, so after the clean
  restart they see only the allow-list. pi resolves a provider key from
  `auth.json` (copied into the step agent dir from `--operator-agent-dir`
  / `[daemon] operator_agent_dir`), a literal `apiKey` in `models.json`,
  or the environment (a provider `*_API_KEY`, or a `$NAME` / `${NAME}`
  reference in `models.json`). A key that only lived in the daemon's
  environment no longer reaches pi steps: move it into `auth.json` (pi
  `/login`) or a literal in `models.json`. `pf doctor`'s `daemon env` row
  warns, names only, when the step agent's `models.json` references a
  variable outside the allow-list.
- A daemon started before this upgrade keeps its old environment until it
  is restarted: `pf doctor`'s new `daemon env` row warns on any
  `ANTHROPIC_*` or credential-looking name in it. Restart it clean:
  `pf ops shutdown`, wait until it is gone, `pf daemon ensure`
  (tools/install.sh restarts it when it stages a new binary).

## Memory write-back: `memory_policy` and the wrap summary

- Every step prompt now asks the step agent to run `pf recall --any`
  itself at the start (`workflows/_shared/memory.md`); only the
  workflow's last step writes a memory: one `experience.wrap` summary per
  work item, guided by the memory policy
  (`workflows/_shared/memory-wrap.md`). Step agents hold no aihub
  credential path: inside a step (`PF_WI_ID` and `PF_DAEMON_ID` set)
  `pf recall` / `pf memory show` read through the daemon socket,
  `pf remember` sends the summary synchronously to the daemon, which
  stores it with its machine credential (idempotent per step dispatch),
  and every other aihub verb, `pf doctor` and `pf daemon` are refused
  (DAEMON-403). An operator shell that inherited both variables is in step
  mode too (the refusal names them): unset them. Nothing to configure;
  the machine credential needs memory read and writer access on the
  project. A failing call never fails the step. `cli.toml` stays
  readable under the shared HOME — this guards against accidents, not
  against an agent that reads it on purpose.
- `.polyforge.yaml` gains the optional `memory_policy:` section (README,
  "Extending › Memory policy"). Nothing is required: a missing section is the
  built-in default. Run `pf init <ws>` once per workspace to append the
  default section (as text — existing bytes and comments are kept), then
  tune it; `pf doctor`'s new `workspace memory policy` row warns about a
  `.polyforge.yaml` without the section (no file at all passes: the
  built-in default applies).
- **Rollback hazard:** a pf/daemon binary older than this version parses
  `.polyforge.yaml` strictly and does not know `memory_policy`, so after
  `pf init` appended the section an older daemon refuses EVERY claim of
  that workspace's projects (DAEMON-414: `unknown top-level key
  "memory_policy"`) until the section is removed. Remedy before (or right
  after) rolling back, per workspace — the appended section is the file's
  tail, starting at its `# memory_policy:` comment line:
  `sed -i '/^# memory_policy:/,$d' <workspace>/.polyforge.yaml`
  (a hand-written section elsewhere in the file: delete it by hand).
- The claim-time recall section of the step prompt is now `[type] title`
  lines only (at most 10, id-only noise titles dropped); ids and excerpts
  stay in `runtime-state/recalled-memories.json`.

## Memories move into the database (polyforge#186)

- Each memory's document (frontmatter + body) now lives in the new
  `memories.content` column; create and redact are one transaction each
  and no memory file is read while serving. `pf recall`,
  `pf remember`, `pf memory show` and the search / read / redact faces
  answer exactly as before (`content_sha` / `content_len` still describe
  the same document bytes).
- **One-time migration at aihub start**, after the listener binds and
  before it serves (clients wait in the listen backlog): the column is
  added when missing, and every row without content gets the bytes of its
  former file `memory/<project>/[_redacted/]<id>/memory.md`. A file whose
  digest differs from `content_sha` wins (the old rule) and the row's
  digest, length and index follow it; an active row whose file sits only
  under `_redacted/` (an interrupted redact) is redacted; a row without a
  file keeps no content, is not served (MEM-404), and is retried on every
  start. The same pass derives the title of every active row whose title
  is blank from its document (the rule create uses: an explicit
  frontmatter title, else the body's first usable line). One log line
  counts copied / adopted / redact-completed / missing rows and derived
  titles,
  followed by the ids of every row that needed more than a copy. The
  migration is idempotent: once done, a start reads no memory file and
  writes nothing. Measured on a copy of the production backup: 3,810
  documents in ~1.5 s. deploy.sh's `UP_WAIT_SECS` default (the switch
  window's HTTP poll, counted from the stop, drain included) is 120 s so
  the first start's migration fits.
- **Deploy gate (step 4)**: on a database with `memories.content`, every
  active memories row in the snapshot must carry its content or have its
  `memory.md` in the backup (a row of a database last served before
  v2.13.0 has no content; aihub fills it from the file at start).
  Redacted rows never block. An active row with neither has no copy
  anywhere: restore its `memory.md` under the data dir's `memory/` from an
  older backup and restart aihub. A snapshot that
  predates the column (a database last served before v2.13.0) is checked
  the old way (rows == `memory.md` files in the backup).
- `pf backup` (`VACUUM INTO`) now holds every memory; it still does not
  include the git repos.

## Work machines: job cap, socket default and daemon id (polyforge#183)

- `pf ops max N` changes the cap of the running daemon only; every daemon
  start takes the configured cap (`--max-concurrent-jobs`, else cli.toml
  `[daemon] max_concurrent_jobs`, else 4). `pf ops max --reset` is gone
  (nothing to reset), and so are the boot line about a persisted cap and
  `max_source` in the `GET /status` and `POST /ops/config` answers.
- A `max_concurrent_jobs` an older daemon left in
  `<data-dir>/daemon-state.json` is ignored from the first start of this
  version on. A machine that relied on it: put the number into the
  `[daemon]` table before upgrading.
- With no socket named anywhere (no `--socket`, `PF_DAEMON_SOCKET` or
  `daemon_socket`), `pf` and the daemon use `daemon.sock` in
  `$POLYFORGE_DATA_DIR`, else `~/.local/share/polyforge`, instead of
  `/run/polyforge.sock`. `tools/install.sh` wrote that same
  path into every `cli.toml` it created, so installed machines see no
  change; a fresh `cli.toml` no longer carries the key.
- A daemon started with no `-machine-id` and no recorded
  `<data-dir>/daemon-id` derives `m-<short hostname>` (the one
  `pf daemon ensure` and `tools/install.sh` use) instead of
  `m-<full hostname>`. A recorded id always wins, so running machines keep
  theirs.

 
## v2.17.0: the project hard delete

The admin-only project delete lands: `DELETE /api/v1/projects/{id}`
(id or name) and `pf projects delete <id-or-name> [--yes]` remove a
project and everything it owns — work items, events, dependency edges,
memories (with their FTS rows), memberships, revoked project-scoped api
keys and the per-WI bare git repos under `git/` — in one SQLite
transaction, recording one audit row per performed deletion. Every work
item must be terminal (wrapped|failed|canceled) and no unrevoked api key
may be scoped to the project (CORE-409 otherwise, naming every offender);
the route is admin-only (AUTH-403 otherwise) and an unknown project
answers CORE-404. The delete is irreversible and takes no automatic
backup: run `POST /api/v1/admin/backup` (`pf backup`) first, and copy the
server's `git/` directory if the bare repos must be kept — the backup
does not include it (see the README's management-face section).

**Deploy aihub before upgrading `pf`.** `pf projects delete` calls the
new `DELETE /api/v1/projects/{id}` route, which an older hub answers with
a bare 404/405. An older `pf` against the new hub changes nothing (the
verb does not exist there, and the daemon does not proxy the route).

### Upgrade SQL

The new `admin_audit` table records every performed deletion (actor,
time, project id and name, the deleted work-item ids as a JSON array —
the startup replay's repo-removal intent). It carries no FK to
`projects`: the cascade of a later delete must never touch it. The file
`deploy/sql/v2.17.0/upgrade.sql` is the source of truth; its content:

```sql
CREATE TABLE IF NOT EXISTS admin_audit (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  actor        TEXT NOT NULL,
  created_at   TEXT NOT NULL,
  project_id   TEXT NOT NULL,
  project_name TEXT NOT NULL,
  wi_ids       TEXT NOT NULL DEFAULT '[]'
);
```

`deploy/sql/v2.17.0/rollback.sql` drops the table (no release before
v2.17.0 reads it; nothing references it). `deploy/sql/v2.17.0/probe.sql`
answers 0 when the table exists (a database created by v2.17.0 is
already in that shape — `core.Open` installs the same DDL) and nonzero
when it is absent, so a re-deploy never applies the SQL twice.

## Work machines: per-work-item events and owner subscriptions (polyforge#181)

The daemon now keeps one events file per work item under
`<data-dir>/events/` (`wi-<id>.jsonl`, daemon scope `daemon.jsonl`; a
file silent for 30 days is removed at start) and no longer reads the old
`events.log`, `events.log.1`…`.3` and `events.seq`; delete them by hand
once the new daemon runs. Numbering restarts per work item, and a
watcher whose cursor is past the new file replays it from the start. A
decision carried only by an event (`step_dead`, `candidates_exhausted`,
`submit_rejected`, `submit_dropped`, `daemon_restarted`,
`carrier_push_failed`, `human_gate_unasked`) that no watcher had acked
before the upgrade is not re-delivered; state-based decisions
(needs_input, blocked, paused, completed, failed, canceled) are, from
execution.json and the hub. The watcher's progress file moves to
`$XDG_RUNTIME_DIR/polyforge/chat-<chat>.status`, and a resident watcher's
start subscribes its chat to the work items it or its owner holds (the
attention and heartbeat routes are gone): restart running `pf watch
--chat` processes with the new `pf`.

## v2.16.0: `pf wrap` closes a work item with no bundle

Deploy aihub before upgrading `pf`: `pf wrap` of a work item with no
bundle calls the new `POST /api/v1/work-items/{id}/wrap`, which an older
hub answers with a bare 404 (the CLI then says to upgrade aihub first).
No schema change.
## The watch summarize lane returns, demand-gated (polyforge#224)

The transcript-delta watch summarize lane (deleted with the simplify era,
`1254d93f`) is back — re-added under the owner's explicit feature-freeze
waiver (this ticket is the waiver), with the demand gate closing the old
cost hole: the old lane dispatched unconditionally on transcript growth,
so model spend happened with nobody looking. Now a summarize call happens
only while a human is looking — a demand-marked snapshot request
(`pf watch <wi> --once --demand`, the marker the panel sets while it is
open) arms the lane; the resident chat watcher's polls never carry the
marker, and a step agent's `demand=1` authorizes nothing (the daemon honors
the marker only when the UDS peer credential says the caller is not a
step process tree of it). Calls are gated on a live running step
dispatch (the current step in_progress with a live step process, not
parked on a question — a needs_input, paused or between-steps work item
starts no call, however watched) and on transcript growth (the current
live dispatch's transcript — a resumed pi dispatch's is the capsule
transcript itself), spaced at least 60 s apart per work item with none
starting while the previous one is in flight, and capped at 20 per
dispatch; at the cap the last summary stays
with `summary_capped: true`. The call runs in the background — the
snapshot route never waits on it. The lane's whole state is memory-only:
a daemon restart resets it; the old `<workspace>/.polyforge/watchers/
<wi>.state` file, its directory and any recovery path do NOT return.

The model source is exactly one place: the reserved
`steps.<project>.summarize.models` key in `.polyforge.yaml` (since
polyforge#242 the root `summarize.models` key — see the next entry; validated like
`default`, never a per-step precedence level; resolved once per claim into
the job record — an edit governs the next claim only). On a failed call
the candidate pointer advances (the last entry retries), like a step. The
old per-harness `cli.toml` `[watch.summarize_model.<harness>]` key does
NOT return — no compat, no dual path. A project with no `summarize` key
has the lane silently off: zero calls, no error anywhere, no new
`pf doctor` row — "off" is a legitimate state now.

**Wire**: `GET /jobs/{wi}/watch` gains four optional snapshot fields —
`summary` (the last finished line, ≤200 characters, one plain-text line,
non-authoritative narration), `summary_error` (the raw error text of the
latest failed call, never faked), `summary_age_sec` (present only with a
summary) and `summary_capped`. **CLI**: `pf watch <wi> --once --demand`
(the flag exists only on the `--once` path; without it it is a usage
error). The dispatch follows the harness rules: the daemon's configured
`--pi-bin`/`--claude-bin`, `pi -p --model <m>` / `claude -p <ctx> --model
<m>` with the prompt on stdin (never argv), the claude context flag per
the 2026-10-19 auth-mode ruling, a 5-minute bound, and no credential
value in any argv, log or artifact.

No schema change, no aihub change; the upgrade is the `pf` binary alone.

## The summarize lane's models move to a root `summarize` key (polyforge#242)

The summarize lane's model candidates move from the per-project reserved
key `steps.<project>.summarize.models` to one top-level key of
`.polyforge.yaml`, `summarize: { models: [...] }`: one list for every
project of the workspace (the narration model is global; there is no
per-project override). The entry is as strict as a step entry — `models`
is its only key, the usual `<harness>:<model>[:<effort>]` candidates —
and a file may declare only `summarize:`. Absent (or no file) the lane
is silently off; a declared-empty list is off too. Shape: README
Extending › Summarize models.

The old per-project key is gone, not kept alongside: `summarize` is no
longer a reserved key under `steps.<project>`, so
`steps.<project>.summarize` refuses like any other key that is no step
id (DAEMON-414 before the claim; `pf doctor` reports it). No migration:
no workspace configured the old shape. Add the root key only after this
`pf` is running — an older daemon refuses the unknown top-level
`summarize` key and every claim of that workspace with it.

No schema change, no aihub change; the upgrade is the `pf` binary alone.

## Summarize narration defaults to 中文; `summarize.language` (polyforge#243)

The summarize lane's one-line narration used to answer "in the
transcript's own language" — mostly English. It now answers in the
claim-time narration language: the root `summarize` entry's new optional
`language` key, or `中文` when the key is absent. **This flips the
default**: a workspace that declares `summarize.models` and no
`language` narrates in 中文 after the upgrade; declare
`language: English` (any free text, inserted verbatim into the prompt)
to keep English. The prompt also asks to keep code identifiers, file
paths and commands as written. Shape and validation: README Extending ›
Summarize models.

The language is snapshotted at claim beside the models list (job record
field `summarize_language`); a job claimed before the upgrade has no
such field and narrates in 中文. Add the key only after this `pf` is
running — an older daemon refuses the unknown `summarize.language` key
and every claim of that workspace with it.

No schema change, no aihub change; the upgrade is the `pf` binary alone.

## `summarize.interval_sec` paces the summarize lane (polyforge#250)

The summarize lane spaced call starts for one work item at a fixed 60 s.
The root `summarize` entry now takes an optional `interval_sec`: the
minimum number of seconds between two call starts for one work item. It
must be a YAML integer from 10 to 3600, inclusive; any other value
refuses (DAEMON-414 before the claim; `pf doctor` reports the same) and
is never clamped. Absent, it is 60, so a workspace that does not declare
the key behaves exactly as before. The 20-call per-attempt cap is
unchanged. Shape and validation: README Extending › Summarize models.

The interval is snapshotted at claim beside the models list and the
language (job record field `summarize_interval_sec`); a job claimed
before the upgrade has no such field and keeps the 60 s spacing. Add the
key only after this `pf` is running — an older daemon refuses the
unknown `summarize.interval_sec` key and every claim of that workspace
with it.

No schema change, no aihub change; the upgrade is the `pf` binary alone.

## `pf submit --json` answers the placement + missing-clone warnings

The submit answer's `--json` shape changed in place. `pf submit <id> --json`
now answers the placement and the missing-clone warnings beside the
acceptance:

    {"accepted":true,"work_item_id":"wi_…","machine_id":"m-…",
     "capsule_path":"<workspace>/capsules/<project>-<seq>",
     "missing_repos":["<repo>"]}

- `machine_id` — the local daemon's machine id, the claim's target machine;
- `capsule_path` — the workspace / capsule landing path the work item will
  occupy (the claim's own resolution);
- `missing_repos` — every repo the claim will clone whose clone is missing
  from the mapped workspace, sorted. The claim still clones fresh, so the
  submit stays accepted — the field is a warning, never a refusal.

`machine_id` and `capsule_path` are absent when the placement is unknown
(the submit face's hub probe did not resolve the work item; the submit
stays accepted and durable-queued). `missing_repos` is absent when every
clone is present or the check could not read the repo set. The old
`{accepted, work_item_id}`-only shape is gone — no dual shape, no
compatibility shim: external parsers of the submit answer must read the
new fields. The human face prints the same facts as plain lines.

No schema change, no aihub change; the upgrade is the `pf` binary alone.

## Wake-target handover: chat workspace tags, scoped decision wake, `pf watch adopt`

Three changes ride together; all of them are the `pf` binary alone (no
schema change, no aihub change).

**The chat workspace tag.** Every `pf chat init` now tags the chat with
the workspace its session cwd resolves to: the root itself or any
subdirectory of a `[workspace."<path>"]` mapping counts, nested mappings
resolve by the longest prefix, and every init refreshes the tag from the
current cwd (a resumed session that moved directories re-tags). An
unmapped cwd mints an untagged chat — it is neither adopt-eligible nor
in the scoped wake below. The tag is recorded in the daemon's
`chat-subs.json` (per chat id); the tag the daemon accepts must be in
the daemon's own `[workspace]` mapping — a drift between the CLI's and
the daemon's `cli.toml` is a DAEMON-400 refusal, never a silently useless
tag. There is **no backfill**: a chat minted before this change carries
no tag until its session's next `pf chat init` records one. `chats.json`
— the (harness, session)→id identity map pi-web reads back — keeps its
exact shape, and `pf chat init` still prints the bare chat id.

**The scoped gone-origin decision wake.** When a work item's origin chat
has unsubscribed (or the work item has none — chat-less submit, or a
missing origin record), its decisions no longer wake every subscribed
chat daemon-wide: they go immediately — no claim window — to exactly the
chats that are tagged with the work item's project-mapping workspace,
live, holders of a subscription, and not muted on that work item by that
chat itself. Every other owner-routed subscriber of that work item sees
progress only (Passive); explicit subscribers stay wake targets of their
own records, untouched. With no eligible chat in the workspace at
decision time, the previous owner-wide wake applies unchanged, with no
new output. Both scope facts — the work item's origin chat and its
workspace — survive the job's terminal close in the running daemon: a
`completed` / `failed` / `blocked` decision stays scoped after the job is
dropped. What an eligible chat delivers is also recorded, in the same
`chat-subs.json` write, for every Passive owner-routed subscriber of that
work item: their records mirror that chat's. A later owner-wide fallback
(after a daemon restart, or once every eligible chat has gone quiet)
therefore never prints it again in any other chat, while a decision that
cleared and recurs (blocked again, a reused question id) counts as
undelivered again. Only a decision no eligible chat delivered before such
a fallback still reaches the owner-wide audience.

**`pf watch adopt <wi>`.** An eligible same-workspace chat takes the
wake-target role over: it becomes the work item's origin chat in every
sense (the same origin field a resume rewrites), and its delivery record
is the union of the origin's retained record and its own delivered keys —
nothing replays. Adopt suppresses further scoped broadcasts for that work
item (the adopter IS the origin), re-points every owner-routed subscriber
— cross-workspace ones included — at the adopter, and leaves explicit
subscribers untouched. Refusals answer the existing DAEMON-4xx family: a
cross-workspace or untagged adopter gets DAEMON-400 naming the work item's
workspace (never a silent subscribe), an adopt while the wake target is
still subscribed gets DAEMON-409 naming its chat id, and a step process
tree can never reach the route (DAEMON-403 by the existing step-origin
guard — adoption runs only on an explicit human request, never
automatic). Subscriptions answers gain the stored `owner_routed` mark
(set by the owner routing; an explicit subscribe and the adopter's own
subscription clear it) and the computed `adoptable` flag (the `passive`
precedent: computed per answer, never stored) that the watcher turns
into the one human-readable hint line on a scoped decision print.
