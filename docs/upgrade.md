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
  2. when an applied version declares `expected_deltas`, deploy.sh takes a
     read-only count of **just those declared tables** (no full-db copy, no
     integrity check);
  3. deploy.sh applies the upgrade SQL (see below), one transaction per
     script;
  4. if step 2 ran, it counts the declared tables again;
  5. the new container starts and HTTP is polled.
  The actual downtime is printed and warned past `MAX_DOWNTIME_SECS`. It
  is still seconds: the SQL is a handful of DDL statements, and the two
  in-window samples are read-only counts of the declared tables only.
- **Post-swap verification**: row counts must be >= the frozen baseline,
  except tables dropped by a `DROP TABLE` in an upgrade.sql applied this
  run, which are excluded from the post-swap row-count check. Tables a
  migration declares in `expected_deltas` are measured with the service
  stopped, right before and right after the upgrade SQL, and must move by
  exactly the declared amount (see below); their baseline is then rebased
  to the post-SQL count, so the post-swap check still guards them against
  any further loss. An undeclared decrease, a declared table whose actual
  delta differs, or a declared table that loses rows after the SQL rolls
  the deploy back.
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

    deploy/sql/<TAG>/upgrade.sql      the schema delta, applied as ONE transaction
    deploy/sql/<TAG>/rollback.sql     restores the pre-release shape for auto-rollback
    deploy/sql/<TAG>/probe.sql        the structural applied-check (run-once)
    deploy/sql/<TAG>/expected_deltas  optional: the row-count deltas the migration makes

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

**Expected row deltas**: a migration that legitimately removes rows from a
table it keeps may ship `expected_deltas` next to the three SQL files: one
`table: signed-delta` per line, blank lines and `#` comments ignored, e.g.
`users: -2`. deploy.sh fetches it from the target tag like the SQL files
and sums the declarations of every version applied in the window. The
declared tables' counts are sampled read-only with the service stopped,
before and after the SQL (only those tables); each actual delta must equal
the declaration exactly. A delta is a checklist, not an amnesty: an
undeclared table keeps the strict `>= frozen baseline` rule, and a
declared table that moves differently rolls back. After the exact check,
the declared table's baseline becomes its post-SQL count, so the post-swap
`>=` check still covers it. The file must be inside the release's own tag:
a later addition on the branch is not visible to `git show <TAG>:...`
(the working-tree fallback would still honor it on a deployer checkout
that carries it, with a NOTE, but put it in the tag you deploy).

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
  demo:
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

## Candidate lists are composed at every dispatch

- A `.polyforge.yaml` or bundle edit now governs the **next dispatch** of
  a running work item (it was frozen at claim). A step whose current model
  left the list restarts from the new list head; an in-flight dispatch is
  never killed. An unreadable yaml/bundle at dispatch blocks the work item
  (DAEMON-414); fix it and `pf resume`.
- The claim-time snapshot keys in the daemon's `runtime-state.json` are
  gone; nothing replaces them.
- The second `ended_without_result` on one candidate now switches the
  model like `model_error` (the first still retries the same model).
- Steps in flight from before this change restart once from the list head
  (their archived attempts carry no candidate stamp).

## v2.20.6 (cross-machine identity, query relay and messaging)

Deploy aihub first, then upgrade the work machines (`tools/install.sh`).
The new machine face is signed; an old daemon never sends a signature and
simply keeps its local execution (a new daemon talking to an old aihub
disables its own cross-machine face). The schema added here is the machine
identity on the existing `machines` row (public key, launcher, role,
operator, supersession stamp, capability card) plus the messaging tables:
`conversations`, `conversation_members` (per-user read cursor) and
`messages` (append-only, monotonic `seq`). Progress/artifact relay state is
never persisted. On first boot the daemon creates `<data-dir>/machine-key`
(0600, Ed25519 seed) and registers its public half through the existing
heartbeat; `pf machines rekey` is the admin-only rebind. The file
`deploy/sql/v2.20.6/upgrade.sql` is the source of truth; its content:

```sql
ALTER TABLE machines ADD COLUMN public_key TEXT;
ALTER TABLE machines ADD COLUMN launcher TEXT;
ALTER TABLE machines ADD COLUMN role TEXT NOT NULL DEFAULT 'runner' CHECK (role IN ('interactive','runner'));
ALTER TABLE machines ADD COLUMN operator_id TEXT REFERENCES users(id);
ALTER TABLE machines ADD COLUMN superseded_at TEXT;
ALTER TABLE machines ADD COLUMN capability TEXT;
CREATE TABLE IF NOT EXISTS conversations (
  id         TEXT PRIMARY KEY,
  kind       TEXT NOT NULL DEFAULT 'direct' CHECK (kind IN ('direct')),
  direct_key TEXT NOT NULL UNIQUE,
  created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS conversation_members (
  conversation_id TEXT NOT NULL REFERENCES conversations(id),
  user_id         TEXT NOT NULL REFERENCES users(id),
  last_read_seq   INTEGER NOT NULL DEFAULT 0,
  joined_at       TEXT NOT NULL,
  PRIMARY KEY (conversation_id, user_id)
);
CREATE TABLE IF NOT EXISTS messages (
  seq             INTEGER PRIMARY KEY AUTOINCREMENT,
  id              TEXT NOT NULL UNIQUE,
  conversation_id TEXT NOT NULL REFERENCES conversations(id),
  sender_id       TEXT NOT NULL REFERENCES users(id),
  text            TEXT NOT NULL,
  about_wi        TEXT,
  reply_to        TEXT REFERENCES messages(id),
  created_at      TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_messages_conv_seq ON messages(conversation_id, seq);
```

`deploy/sql/v2.20.6/rollback.sql` drops the child tables first and then the
additive machine columns; a rollback after a rekey needs the rekey repeated.
`deploy/sql/v2.20.6/probe.sql` answers 0 once `machines.public_key` exists.

A machine whose `<data-dir>` was wiped keeps its default `m-<hostname>` id
but generates a new key, so its heartbeat is refused `AUTH-403` until an
admin rekeys it: `pf machines rekey <machine>` (with the machine's local
public key) or `--public-key <base64>`. Local execution is unaffected. The
same refusal is the recovery move for a key that was replaced by hand.

Legacy `machines` rows carry `operator_id` NULL until their daemon's first
post-upgrade heartbeat. The first authenticated heartbeat for such a row
claims it (operator and public key), so upgrade the work machines promptly
after aihub: a machine that stays offline, or keeps running an old daemon,
leaves its row unclaimed — and therefore unowned for the one-interactive
rule — until it heartbeats once with the new daemon.

## v2.20.7 (separate delivery and read cursors)

aihub first, then the work machines. `conversation_members` stops carrying
one `last_read_seq` and carries two independent cursors instead:
`delivered_seq`, advanced by the operator's daemon as it delivers, and
`read_seq`, advanced only by a human acknowledgment (`pf msg ack`, or the
pi-web Messages panel). Unread counts only the other participant's messages
above `read_seq`, so machine delivery no longer clears unread. Existing
members are backfilled from the old cursor (read stays read, delivery starts
at the same position) and the old column is dropped — there is no dual path.
The file `deploy/sql/v2.20.7/upgrade.sql` is the source of truth; its
content:

```sql
ALTER TABLE conversation_members ADD COLUMN delivered_seq INTEGER NOT NULL DEFAULT 0;
ALTER TABLE conversation_members ADD COLUMN read_seq INTEGER NOT NULL DEFAULT 0;
UPDATE conversation_members SET delivered_seq = last_read_seq, read_seq = last_read_seq;
ALTER TABLE conversation_members DROP COLUMN last_read_seq;
```

`deploy/sql/v2.20.7/rollback.sql` restores `last_read_seq` from
`delivered_seq` and drops both new columns, so an old daemon resumes
delivery where the new one stopped and never replays delivered messages; the
read position is dropped with the new shape. `deploy/sql/v2.20.7/probe.sql`
answers 0 once both `delivered_seq` and `read_seq` exist, and answers
non-zero again after a rollback, so the window SQL runs at most once per
direction. `pf msg ack <conversation> [--seq N]` advances only the caller's
read cursor, refuses a non-positive or past-the-newest sequence with
CORE-400, and an unknown conversation with CORE-404; it keeps the data-plane
verb's permission surface (a step agent is refused locally and an admin
cannot acknowledge another member's row).

## v2.20.8 (the machine-heartbeat permission model)

Deploy aihub first, then upgrade the work machines (`tools/install.sh`).
`POST /api/v1/machines/heartbeat` now needs the row operator's Bearer PLUS
a signature by the machine's bound key (the machine face's existing X-PF-*
scheme) and a first registration needs a public key plus a signature by
it. A machine's identity is its `operator_id`: registration no longer mints
a `mac_` machine user, `machines.user_id` is gone, and the heartbeat answer
and `GET /machines` report `operator_id` instead. The one bounded
transition is an already-registered v2.20.6 daemon's unsigned heartbeat; it
is accepted only from the row's operator and touches only liveness
(`last_heartbeat_at`, `live_wi_ids`, `mode`, `runtime_version`). Its
deletion condition: once the owner confirms `GET /machines` shows no live
unsigned machine (a live row whose `runtime_version` predates this
release), the next release deletes that code. The file
`deploy/sql/v2.20.8/upgrade.sql` is the source of truth; its content:

```sql
ALTER TABLE machines DROP COLUMN user_id;

DELETE FROM users
 WHERE kind = 'machine'
   AND email LIKE '%@machines.local'
   AND NOT EXISTS (SELECT 1 FROM api_keys k WHERE k.user_id = users.id)
   AND NOT EXISTS (SELECT 1 FROM project_members pm WHERE pm.user_id = users.id)
   AND NOT EXISTS (SELECT 1 FROM messages msg WHERE msg.sender_id = users.id)
   AND NOT EXISTS (SELECT 1 FROM conversation_members cm WHERE cm.user_id = users.id)
   AND NOT EXISTS (SELECT 1 FROM work_items wi WHERE wi.reporter = users.id OR wi.assignee = users.id)
   AND NOT EXISTS (SELECT 1 FROM wi_events e WHERE e.actor = users.id)
   AND NOT EXISTS (SELECT 1 FROM memories mem WHERE mem.owner_user_id = users.id)
   AND NOT EXISTS (SELECT 1 FROM machines m WHERE m.operator_id = users.id);
```

The column drops first so it cannot act as the reference that saves the
machine users it used to name; the `LIKE '%@machines.local'` filter keeps
an administrator-created `--user-type machine` user (a real email), and the
`NOT EXISTS` set keeps any heartbeat-minted user something still references.
`deploy/sql/v2.20.8/rollback.sql` brings `machines.user_id` back NOT NULL
and fills it from `operator_id`, the v2.20.6 binary's required shape. The
deleted heartbeat-minted users are deliberately not resurrected: the
column is a plain id string the old binary never joins on, and it mints a
fresh user on its next registration. `deploy/sql/v2.20.8/probe.sql`
answers 0 once `machines.user_id` is gone.

## v2.20.10 (the signed-heartbeat version gate)

Upgrade the daemon BEFORE deploying this aihub. The heartbeat's bounded
unsigned transition is an already-registered pre-signing daemon's unsigned
heartbeat; it is accepted only from the row's operator, only while BOTH the
request's and the row's stored `runtime_version` parse as `vX.Y.Z` and
predate `v2.20.8`, and it touches only liveness
(`last_heartbeat_at`, `live_wi_ids`, `mode`, `runtime_version`). The gate
fails closed: a daemon whose `runtime_version` is `dev`, empty or
unparseable (for example a local build without the version ldflags) is
refused and goes stale. A signed `v2.20.8+` beat ratchets the row closed:
an unsigned beat can never raise the stored version back over the gate.
Accepted limit (the owner's ruling): the gate does not stop a leaked
operator API key from keeping a retired pre-`v2.20.8` machine online — an
unsigned beat touches only liveness and can never ratchet the row closed,
so a row no signed beat ever refreshed stays open to whoever holds the
operator's key; revoking that key retires the machine, and only deleting
the transition branch closes the residual for good. Its deletion condition:
once the owner confirms `GET /machines` shows no live unsigned machine (a
live row whose `runtime_version` predates `v2.20.8`, the release whose
daemon signs), the next release deletes that code.

## The recall duty rides the authoring steps only

A step's prompt now carries the project-memory recall duty
(`workflows/_shared/memory.md`: `pf recall` from three angles, read the
relevant bodies, a `## Prior experience` section) only when its
`workflow.md` step entry declares `recall: true`. The shipped lanes declare
it on their decision and authoring steps (grill, spec, plan, repro,
root-cause, e2e-author, code, fix); the review, verification and deliver
gates, feature-full's grill-review and the release lane no longer recall,
so they no longer spend their first seconds on searches. The workflow's
last step still carries the one wrap-summary duty
(`workflows/_shared/memory-wrap.md`), with or without `recall`.

**A custom bundle** gets no recall duty on any step until its entries
declare `recall: true`; add it to the steps that decide or author.

**Gap for work items claimed before this release:** their capsule's own
copy of the old `workflow.md` declares no `recall`, so the steps still
ahead of them run without the recall duty (the claim-time recalled-memory
titles still render). Recall is advisory, so nothing fails; a work item
claimed after the upgrade gets the new bundle.

## Parked closes, the parked resume outcome and the close-retry window

Nothing to migrate: the daemon start that installs this release reconciles
every record afresh. What operators see differently:

- **`DAEMON-409 … is parked`.** When a work item's previous driver Run does
  not stop within its bounded teardown, the daemon no longer commits,
  pushes, saves WIP, seals or quarantines the capsule next to it. A wrap,
  fail, pause or bounded-fail block answers `the close is parked …`; the
  daemon's release of a session that is no longer its own (superseded, or
  ended at aihub) answers `the release of this work item is parked …`. The
  record keeps its state, `pf watch <id> --once` shows a `close_parked`
  event (`kind`: close, pause, blocked or release), and the close finishes
  by itself once that Run exits; a daemon restart finishes it too. Do not
  repeat the verb meanwhile: it gets the same answer.
- **`pf resume` → `parked`.** A resume whose recovery (or the close it led
  to) waits on such a Run now answers the `parked` outcome (`--json`:
  `"outcome": "parked"`) — nothing continues next to the old Run, and the
  work item continues by itself once it exits. It used to answer `closed`.
- **The close-retry window is 24 hours.** A close, verify or claim replay
  aihub does not take is retried with the growing backoff and then once a
  minute for 24 hours from its first retry (it used to stop after eight
  attempts, about four minutes), so an aihub outage shorter than that is
  ridden out with no restart. Past the window the record stays where it is
  with a `retry_exhausted` event, and the doors say so: a `pf wrap` or
  `pf pause` over it (and a `pf resume` of a paused record whose close is
  pending) answers `its retry chain is exhausted (retry_exhausted) and
  nothing retries it any more — restart the daemon` (`pf ops shutdown`,
  then `pf daemon ensure`), instead of "retried with backoff" or "retry
  later".
- **Blocked notes carry the driver's reason.** When the driver parks a
  workflow blocked on its own authority, the hub note of the blocked close
  ends with `; blocked reason: <blocked_reason>` (at most 500 characters).
- **Remote watch snapshots are cached for 10 seconds.** A `pf watch` or
  chat progress line of a work item held by another machine is served from
  the requesting daemon's 10-second cache (any aihub-visible change of the
  work item misses it), so remote progress and a remote human-decision
  wake lag by at most 10 seconds; a requesting daemon relays at most one
  query per work item per 10 seconds to the holder (a failed relay is not
  cached and is asked again on the next poll).

## Claude steps' Bash timeouts follow the tool window (2026-10-29)

Nothing to migrate. A claude step no longer runs its Bash commands under
Claude Code's 2-minute per-call default: the daemon starts every claude
step with `BASH_DEFAULT_TIMEOUT_MS=600000` (10 minutes) and
`BASH_MAX_TIMEOUT_MS` set to the supervision tool window minus one minute
(`1740000` for the default 30 minutes), so a `make test` or `go test` of a
few minutes finishes in the foreground instead of being killed, moved to
the background and polled. To choose other values, set the key in the env
block of `<harness-root>/claude-config/settings.json`: a key set there wins
over the daemon's value (details: the `claude_bin` section of
docs/reference/config.md). pi steps are unchanged.

## A model error resumes first; outage-shaped retries are paced

Nothing to migrate: both rules read the attempts already archived in
`execution.json`, so a work item in flight picks them up at its next
failure. What operators see differently:

- **The first `model_error` on a candidate no longer switches models.**
  A turn that ends on an error naming no throttle, transient, allowance or
  auth problem — in practice a gateway 5xx — is retried on the SAME
  candidate, resuming its conversation; only the second consecutive
  model-class death on that candidate moves to the next one. It still
  counts toward the step's three-failure bound. An allowance or auth error
  on a live turn (401/403, quota, insufficient balance, billing) or a
  model name that cannot resolve is now recorded as `exit_model_class`, as
  the same text on an exited process always was, and still switches at
  once.
- **Retries after an outage-shaped death are paced.** Consecutive
  `model_error`, `upstream_transient` and `ended_without_result` deaths of
  a step on one candidate wait 1m, 3m, then 9m (the cap) before the step's
  next dispatch re-runs that candidate (an upstream transient used to wait
  1s, 2s); a death that switches to the next candidate dispatches it at
  once and that candidate's ladder starts again at 1m, and a throttle
  keeps its own 1s-doubling backoff. The waits count into the step's cumulative time
  bound. A daemon restart in the middle of a wait serves only what is left
  of it; `pf resume` of a blocked step does not wait, and starts the
  step's streaks afresh (the next 502 is again a first, resumed, 1m one).
- **`pf ops status` shows the wait.** A RETRY column appears while some
  job waits one: `retry in 3m: upstream_transient` (`--json`:
  `retry_in_sec`, `retry_cause`). The supervisor does not count a declared
  wait toward the driver-stall window.

## The verification engine is conf-first and the step runs it through `pf verify-run` (, 2026-10-29)

Nothing to migrate for Go and npm repositories: their engine line report is
byte-identical. What changes:

- **`tools/verify-scope` honours a repository's `.polyforge/verify-scope.conf`
  on every stack.** A repository with no `go.mod` / `package.json` (or a
  `stack=none` pin) verifies on the full tier with exactly the commands its
  conf declares; without a conf it declares no commands and the step derives
  the bar itself, as before. New keys (`check`, `check_requires_env`,
  `check_requires_cmd`, `check_timeout`, `test_glob`, `base_setup`, `runner`,
  `ci_evidence`) and the `--json` / `--full` flags are in
  docs/reference/config.md. `--run` executes conf commands with `bash -c`
  (it was `sh -c`): a conf line that relied on a strictly POSIX `sh` keeps
  working, a line that needs bash now does too.
- **The `verification` step calls `pf verify-run` once** for its mechanical
  half (the default-branch merge, the engine plan, the whole plan, flaky
  re-runs, the pre-existing proof, the test bar, the test-file checks) and
  keeps only judgment. Installed releases pick this up with their next
  `.polyforge/release.sh`; a running daemon keeps the previous step text
  until then. `pf doctor`'s `scoped-gates readiness` row now reports a
  conf-only repository as `pass` (full tier) and a stackless repository with
  no conf as `warn`.

## A step's tool window now bounds `pf verify-run`'s deadline

Nothing to migrate: an omitted `--deadline` still gets a whole-run deadline
below the daemon's supervision tool window, and a shorter explicit one is
honoured. What changes:

- **Every step environment carries the tool window.** `PF_TOOL_WINDOW_MS`
  is the daemon's `--tool-window` (the 30-minute default) in milliseconds,
  set for pi and claude steps alike; `pf verify-run` derives its default
  whole-run deadline from it (and from `BASH_MAX_TIMEOUT_MS` when that is
  smaller), leaving a margin below the bound.
- **An explicit `--deadline` above that bound is clamped**, not honoured:
  the run stops in time to write `result.json` before the daemon judges the
  single blocking call hung. `result.json`'s `deadline_clamped_from` names
  the value the caller asked for; `deadline` is the value the run used.
- **The verification prompt no longer derives the deadline from the shell
  timeout.** A pi step has no shell-call maximum to read; the runner owns
  the bound.

## A hard-killed `pf verify-run` no longer leaves its private TMPDIRs behind

Nothing to migrate. pi kills its detached bash children with SIGKILL when
the daemon tears the step down, so `pf verify-run` cannot run its own
deferred cleanup. Every private `TMPDIR` is now marked with an owner lock
held for the life of its run; the lock is released by the kernel however
the process dies, and the next `pf verify-run` removes the marked
directories whose owner is gone. A partial `result.json` after SIGKILL
would need incremental writes during the run and is not claimed here; a
clamped deadline (the previous entry) is what keeps the kill from being
reached in the normal case.

## v2.20.12 (`pf recall` browse mode and the task-list prompt summary)

Nothing to migrate. aihub ships first (`tools/install.sh` after it). What
changes:

- **aihub's browse `total` is the whole walk.** `GET /api/v1/memories/search`
  with an empty `q` now answers `total` = the number of entries the whole walk
  covers for the caller (the same type/tags/project narrowing, visibility tiers
  applied — an entry the caller cannot read is never counted), instead of the
  page length. It costs one more statement per page over the narrowed
  candidates. Keyword search and the web console are unchanged.

- **`pf recall` with no query browses.** On the human direct-aihub path,
  `pf recall --project P` lists the project's active memories newest first
  (aihub's browse mode) instead of refusing: `--limit` is the page size
  (default 20, `1..200`), `--sort updated|created` the descending order
  (aihub's default `updated` when omitted), `--after <cursor>` continues the
  walk. `--json` is aihub's `{items, total, next_cursor?}`; human mode adds
  `N of TOTAL` and `next: <cursor>`. A query together with `--after`/`--sort`
  is aihub's `MEM-400`; `--any` with no query is refused locally. Step and
  task agents still search only: inside a step or task an empty query,
  `--after` or `--sort` is refused locally, and the daemon's memory proxy is
  unchanged.
- **`GET /task/list` rows carry `prompt_summary`.** Each row of
  `pf task list --json` gains the prompt's first non-empty line, at most 120
  code points (empty for an empty prompt); the list still carries no full
  prompt and no report, and `pf task show` is unchanged. The human table is
  unchanged.
- **pi-web's Memory and Tasks tabs need pf 2.20.12.** The dock's Memory tab
  browses by default and the new Tasks tab reads `pf task list` /
  `pf task show`; below 2.20.12 both show their upgrade-required state.

## v2.20.12 (the project member face and `pf msg contacts`)

Nothing to migrate. aihub ships first (`tools/install.sh` after it). What
changes:

- **aihub answers a project's human co-members.** The new read-only
  `GET /api/v1/projects/{id}/members` answers
  `{"contacts":[{"id","display_name"}]}`: the project's human members other
  than the caller, nothing else (no machine row, no type, role or count). It
  is gated by the existing project read — a member or a global admin, the
  api-key project scope applied; anyone else gets the concealed `CORE-404`,
  a key scoped to another project `AUTH-403`. A step agent cannot reach it
  (it holds no aihub key and the daemon forwards no such route).
- **`pf msg contacts` lists the people you can message.** It reads the
  `cli.toml` `[workspace.*]` mapping at call time and returns the union,
  deduplicated by user id, of every mapped project's member face — one read
  per project, at most 4 at once, 15 s each, no retry. A project you cannot
  read contributes nothing (exit 0); no mapping answers `{"contacts":[]}`.
  It is refused inside a workflow step like every other `pf msg` verb.
- **pi-web's Messages tab needs pf 2.20.12.** Its contact list is built from
  `pf msg contacts`; below 2.20.12 it shows the upgrade-required state.

## Owner exits, the reserved cancel answer and owner-routed rewinds

No data migration; aihub ships first (`tools/install.sh` after it) — the
daemon's cancel answer uses the cancel door's new optional body. What
changes:

- **`rewind_stats.rewinds[]` records carry `class`, not `charged`.** The
  class is `charged` (the edge's `max_per_step` and `max_total`),
  `uncharged` (light, mechanical, converging: `max_total` only) or `owner`
  (neither). A work item claimed before this release keeps its old records,
  which no longer count toward any per-edge quota: finish or re-file such an
  in-flight item. The rewind numbers are unchanged.
- **Owner-routed rewinds charge neither bound.** A deliver FAIL a human
  answered ("back to code", the full re-verification answer), the review
  owner exit's amend-upstream, the bug-fix dead ends' re-run-repro and
  e2e-author's all-green rewind-to-spec never count toward `max_total` or
  an edge slot; automatic rewinds still do.
- **Every blocking review finding carries `repair_target`.** One of `code`,
  `authored-suite`, `spec`, `declared-obligation`, `instruction`; a missing
  or unknown class is an envelope rejection. A review FAIL whose blocking
  findings are all off-graph asks the owner (`review_owner_exit`):
  `amend-upstream: <words>` (recorded verbatim in `artifacts/grill-qa.md`
  `## Owner rulings`, executed by the authoring step append-only or by the
  build step; spec and plan never re-run), `waive <refs> — <words>` (exactly
  the named findings) or `cancel`.
- **`cancel` is a reserved answer at every question.** Every question
  states it. The daemon first has the hub's cancel gate authorize it (a
  dry-run of `POST /api/v1/work-items/{id}/cancel`, which now accepts an
  optional `{reason, question_id, dry_run}` body); the workflow then parks
  blocked with `cancel_requested`, the session ends blocked and the work
  item is canceled through the existing door with the verbatim answer on
  its event. No memory wrap is written.
- **`declared_extensions` entries are `{path, source, ref}`.** `source` is
  `review` (`ref` `s<n>#<k>`), `verification` (`s<n>`) or `deliver` (a
  question id); the old `finding_id` field is gone. The mirror
  `withdrawn_obligations` withdraws a cited moot coupled obligation, and an
  obligation none of whose declaring writes is in the diff lapses.
- **Bug-fix dead ends ask the owner.** `reproduces: false` parks
  `repro_not_reproduced`, a second inconclusive root cause parks
  `root_cause_inconclusive` (`re-run-repro` / `continue` / `cancel`), and a
  repro re-run after a fix → root-cause rewind is pointed at the recorded
  pre-fix heads (`pre_fix_heads`).
- **An all-green e2e baseline asks the owner** (`e2e_all_green`:
  `rewind-to-spec`, `skip`, `cancel`); the e2e-author mode's one anchor is
  `artifacts/grill-qa.md`.
- **A completed `fix` may list no commit only with
  `root_cause_addressed: false`**; a true fix with no commit is rejected.
- **Deliver offers `acknowledge` for an already-merged PR** and allows one
  follow-up PR after a MERGED PR's "back to code"; a re-dispatched release
  adopts a published tag only when it is on `origin_head`, this capsule's
  `artifacts/pre-release-tags.txt` exists and the tag is absent from it.

## A rewind record without a class fails closed

No data migration. What changes:

- **A `rewind_stats.rewinds[]` record whose `class` is not `charged`,
  `uncharged` or `owner` now fails the next rewind closed.** A record
  written before the class ledger (`charged: true|false`, no `class`) used to
  be read as class `""` and silently occupied no edge slot; the next gate
  FAIL of such a work item now ends `failed_rewind_exhausted` with the
  `rewind_ledger_unattributable` event, exactly like the count-only ledger
  shape. Finish or re-file any work item still in flight from before the
  class ledger; the `charged` field is never read.

## Step attrs in `.polyforge.yaml`

No data migration. What changes:

- **A step entry may carry `attrs`.** `steps.<project>.<step-id>.attrs.<name>`
  overrides an attr the bundle step declares (`attrs:` in its `workflow.md`
  entry). The loader refuses an undeclared name, a value of the wrong type
  and any `attrs` under `default` or `task`; an attrs-only entry declares no
  `models` level. An older daemon refuses a yaml that carries `attrs`
  (`not allowed`) and the work items of that workspace stay queued with that
  refusal: upgrade pf, then restart the daemon (`pf daemon ensure`) before
  writing an attr.

## The aihub server is `pf aihub`; one pf binary ships

No data migration, no wire-contract change. What changes:

- **The server runs as `pf aihub`.** `cmd/aihub` and the separate `aihub`
  binary are gone; the `pf` binary intercepts `pf aihub` before the CLI
  dispatch (like `pf daemon`) and runs the same server with the same flags
  and defaults (`-data-dir`, `-listen`, `-git-janitor-period`,
  `-machines-stale-after`, `-version`), the same single-active data-dir
  lock, bootstrap admin key file, memory content migration and graceful
  drain. `pf aihub -version` prints exactly what `aihub -version` printed
  (the build tag). A hand-run server (the macOS and developer quickstarts)
  starts `pf aihub ...` instead of `aihub ...`.
- **A release publishes four payload assets plus `SHA256SUMS`:**
  `pf-linux-amd64`, `pf-darwin-arm64`, `pf-darwin-amd64` and
  `aihub-image.digest`, with `SHA256SUMS` covering the four. No
  `aihub-linux-*` or `aihub-darwin-*` asset is published any more, and
  `release.sh --verify` refuses a release that carries one.
- **The image is built from the linux `pf` binary** and its entrypoint is
  `/pf aihub -data-dir /data -listen :8080`; the image name, the `/data`
  volume, the port and the compose service are unchanged.
- **`deploy.sh` reads the new server with `/pf aihub -version`.** For the
  old side of a deploy — the running container before the swap, the saved
  old binary, the rollback image and the rollback container — it tries that
  first and falls back to `/aihub -version` for a pre-consolidation image,
  saving the old binary from whichever path answered; a legacy rollback
  build uses the old release's own `deploy/Dockerfile`. This fallback is
  transition code for a safe rollback and is deleted once no
  pre-consolidation release remains a deploy or rollback target.
- **Deploy timing does not change.** The same tag is the same build for the
  server and the work machines, but promoting the server stays a manual
  decision: a tag that changes only the daemon does not require the server
  to follow, and an upgrade order is needed only for a tag that changes the
  daemon↔aihub wire contract. `tools/install.sh` is unchanged (it always
  installed only `pf`).

## verify-scope selects e2e suites by `carries:` declarations; python and rust analyzers

No data migration, no wire-contract change; the engine travels in the
release tree as before. What changes for a repository the engine reports on:

- **npm suites are selected by declaration, not by text.** A suite declares
  the paths it covers in its opening comment block (`// carries: <glob>,
  <glob>`; `# carries:` in python). The old selection of an `e2e/*.mjs` suite
  that merely mentions a changed path is gone. Once a repository holds at
  least one suite, an undeclared or malformed suite, an unresolvable import,
  or a changed path no declaration covers (documentation, `exclude=` matches,
  suites and their imported helpers aside) makes every report `mode: full`,
  naming the reason. A repository with no suite file reports exactly as
  before. Before relying on scoped tiers, add a header to every suite;
  `pf doctor`'s `scoped-gates readiness` row now names a repository's
  undeclared suites.
- **Three conf keys.** `e2e_runner=` (the selected suites are appended to it
  as one command), `suite_glob=` (replaces suite discovery) and
  `e2e_map=<glob>=<suite>` (replaces one suite's header). An empty or
  malformed value forces the full tier. An older engine diagnoses the keys as
  unknown and ignores them, so a conf may declare them before this release is
  installed. The JSON report echoes them as `e2e_runner`, `suite_glob` and
  `e2e_map`.
- **Python and rust repositories get an analyzer.** `pyproject.toml` or
  `requirements.txt` selects the python analyzer (`python -m compileall -q`,
  the unit tests importing a changed module plus carries suites, `pytest`)
  and `Cargo.toml` the rust analyzer (`cargo build --all-targets`, the unit
  tests of the modules containing or using a changed module plus carries
  suites, `cargo test`); either falls back to full when a change's affected
  tests cannot be resolved. A repository that relied on "no analyzer" for
  such a manifest and declared its bar in the conf keeps its conf commands
  (they replace the analyzer's defaults per phase) but now gets a scoped
  tier; pin `stack=none` to keep the conf-only full tier. Two or more of
  `go.mod`, `package.json`, a python manifest and `Cargo.toml` without a
  `stack=` pin stay `ambiguous` and full.
- **Authored suites carry the header.** The feature lane's `e2e-author` step
  now derives each authored suite's `carries:` header from the plan's
  declared changed paths.

## The legacy rollback image needs the running version's tag

No data migration, no wire-contract change. What changes for an operator:

- **`deploy.sh` refuses at step 0 when a legacy rollback image cannot be
  built from the running release's own files.** When the running version
  has no GHCR image (a release before the image existed), the rollback image
  is built on the server from the saved old binary and the old release's
  `deploy/Dockerfile`, read from the tag `refs/tags/<running version>` and
  nowhere else. A deployer clone that has not fetched that tag now stops in
  preflight — `tag <old> (the running version) not in the local repo - git
  fetch --tags` — before anything on the server changes; the old fallback to
  the working tree's Dockerfile (which copies a different binary) is gone.
  Run `git fetch --tags` in the deployer clone and deploy again. A running
  version with a GHCR image is unaffected.
- **The dry-run plan names both probe layouts.** Its step 2 (rollback
  image), step 6 (the pulled image) and step 7 (the new container) lines
  now say `/pf aihub -version, else /aihub -version`, which is what the
  probes already did.

## v2.20.18 (message analysis runs as a task and answers a structured result)

- **`pf msg analyze` runs one task.** The analysis no longer uses the
  summarize dispatcher or `summarize.models`: the daemon spawns one task in
  the task lane — the message's work-item project, else the first mapped
  project — with models from `steps.<project>.task.models` (else
  `steps.<project>.default.models`). A project with neither now refuses the
  analysis with `DAEMON-400`; configure a task chain before relying on it.
  The task shows in `pf task list` with the caller as reporter, and its
  `prompt_summary` is only `Analyze message <id>`; the whole prompt (message
  body included) is readable through `pf task show` under the task store's
  unchanged retention.
- **Read-only tool profile, fixed budget.** The analysis task is offered only
  read/search tools (claude `Read,Glob,Grep`, pi `read,grep,find,ls`) and a
  fixed 5-minute / 100000 fresh-token budget; neither is configurable, and a
  normal `pf task spawn` keeps the standard surface. The profile narrows the
  tool surface; it is not a sandbox.
- **Blocking, structured answer.** The call blocks until the task ends and
  answers `message_id`, `task_id`, `project`, `type` (`requirement`, `bug`,
  `ruling`, `discussion`, `clarification`, `outreach`, `other`), `summary`,
  `suggested_actions` (`file_ticket`, `reply`, `note_only`), `model`,
  `duration` and, beside a reply suggestion, `reply_draft`. The `conclusion`
  field is gone, with no compatibility path; pi-web's analysis floor is
  therefore `2.20.18` (its Messages floor stays `2.20.12`).
- **No analyzer lock.** Two analyses may run at once; with both task slots
  busy the call is the retryable `DAEMON-409` and nothing is queued. A task
  that ends `failed`, `capped` or `interrupted` answers `DAEMON-500` naming
  the state and the task id.

## Full-tier e2e selection by smoke suites; the deliver step's recorded bar runs

No data migration. What changes:

- **`smoke_suite=` opts a repository into the full-tier e2e selection.** A
  repository whose `.polyforge/verify-scope.conf` declares at least one
  `smoke_suite=<suite>` line gets, on the full tier of a non-empty diff, its
  fast gates plus the carries-selected e2e suites union its smoke suites
  (the smoke suites alone for a documentation-only diff); an uncovered
  non-documentation path is listed, not a reason for the full e2e command,
  and a forced-full path, a change to the conf itself or a structural
  diagnostic runs the full e2e command. `--json` reports `smoke_suite` and
  `e2e_selection` for such a repository only (the key set of every other
  repository is unchanged). A repository without the key, the scoped tier
  and the release lane behave exactly as before. The key takes effect once
  the release that ships this engine is installed: an older engine
  diagnoses it as unknown and runs the full e2e command.
- **The carried bar stays the full command.** For an opted-in repository
  `pf verify-run` carries the full e2e command in `test_bar` in place of the
  selection it ran, and `deliver` re-runs it on every full-tier delivery —
  the HEAD-equality shortcut no longer applies to such a repository. A
  scoped delivery keeps its carried scoped bar.
- **`pf bar-run` and the bar-run record.** The deliver step re-runs carried
  `test_bar` commands as `cd repos/<name> && pf bar-run -- '<command>'`. The
  daemon (`POST /step/bar-run`, reachable only by the calling work item's
  live deliver step) records each run with the repo HEAD it reads itself in
  `<data-dir>/bar-runs/<work-item-id>.jsonl`. A re-dispatch of the same
  deliver run reuses a recorded run with exit 0 at the pushed head and runs
  only the missing commands; the record is cleared at the rewind/re-entry
  boundary. A deliver re-dispatch no longer merges the default branch when
  it has not advanced. `pf doctor`'s scoped-gates readiness row reads and
  validates `smoke_suite=`.

## Capsule retention slims wrapped capsules; `--prune-capsules`

- **The doctor row is `capsule retention`, the flag `--prune-capsules`.**
  The `git dir retention` row is renamed and its scan now covers every
  mapped workspace's live capsules as well as the set-aside scenes and the
  external git dirs. `--prune-git-dirs` is gone with no alias: `pf doctor
  --prune-git-dirs` is a usage error (exit 2); scripts and runbooks that
  call it switch to `pf doctor --prune-capsules`.
- **A wrapped work item's capsule is slimmed, not kept whole.** Past the
  14-day retention, with no daemon job naming it and no uncommitted or
  unpushed task-repo work in it, a `wrapped` work item's capsule — live or
  set aside — is reported reclaimable (live) or retired (set aside), and
  `--prune-capsules` reduces it to `artifacts/` + `capsule.json` (its
  `repos/`, `runtime-state/`, `logs/`, `session/`, `workflows/`, `sync/`
  and `.git` pointer go). `pf watch` then shows the hub record's status
  without step detail, and the artifacts stay readable. A failed or
  canceled work item's set-aside scene and the external git dirs are still
  removed as before; a live capsule of a work item that is not `wrapped` is
  never touched. The default run still only reports.
- **A new `disk watermark` doctor row.** `pf doctor` now reports, for every
  mapped workspace, the filesystem holding it (used, total, percentage) and
  the bytes under its `capsules/`, and warns when a filesystem is used above
  70%. It never fails and never moves the exit code; the walk of a large
  `capsules/` tree makes the full report take longer on a crowded disk.

## e2e-author records an executability preflight per scenario

- **Every kept baseline row carries a four-key `preflight`.** A completed
  `e2e-author` result's `baseline` rows each record `data_source` (the source
  path plus its grep hit count), `async_settle` (the bounded wait primitive
  plus its bound), `timing_window` (the window duration versus the assertion
  point) and `selector_unique` (a runtime count of zero pre-existing hits),
  each `{verdict: ok | n/a, evidence}`. A check the stack does not have is
  `n/a` with the reason as its evidence; no browser, DOM or runner is
  assumed.
- **One run serves both.** The preflight is read from the existing targeted
  baseline run; no second run is added.
- **Structural failures do not complete.** A baseline red no correct
  implementation could pass (a failed self-check, or a build/wiring error)
  is fixed in place and amended into the author commit, or returned as
  `needs_input` naming the scenario, the self-check and its evidence when
  the spec itself demands it.
- **The driver rejects a bad preflight.** A completed `e2e-author` result
  whose row lacks the preflight, a fixed key or a non-empty evidence, carries
  an unknown key, or has a verdict other than `ok`/`n/a` (`structural` is not
  a verdict) is an envelope rejection and the step is re-dispatched.
- **Per mode.** All-green rows carry the preflight too; an append-only
  re-entry preflights only its newly appended rows; a
  `skipped-with-rationale` run has no baseline and owes none.
- **No workspace change.** No `.polyforge.yaml` key is added. The contract
  takes effect with the release that ships the workflows tree; an
  `e2e-author` result accepted before it is not re-judged. One in-flight
  edge: on a work item whose baseline rows were recorded before the
  release (so they carry no preflight), an append-only `e2e-author`
  re-entry after the release is rejected for those rows.

## v2.20.20 (`pf msg prompt` turns a message into an execution prompt)

- **New verb, new daemon route.** `pf msg prompt <id>` (daemon UDS route
  `POST /messages/{id}/prompt`, body `{caller_id}`) runs one read-only task
  that restates the message as a self-contained execution prompt for
  another agent, in the message's language. The task prompt carries the
  clicked message, at most the 10 most recent earlier messages of its
  conversation (oldest first), the replied-to message when there is one
  and the anchored work item (slug, status, goal) when it is readable —
  nothing later and nothing from another conversation. A conversation read
  the daemon is refused is the caller's typed refusal; no prompt is built
  from part of a conversation.
- **Same lane, budget and reach as `pf msg analyze`.** One task in the task
  lane — the anchored work item's project, else the first mapped project —
  with the read/search-only tool profile, the fixed 5-minute / 100000
  fresh-token budget and the caller as reporter; no config key or flag
  changes it. Its `prompt_summary` is only `Turn message <id> into an
  execution prompt`. Local UDS only, refused to step and task processes
  (`DAEMON-403`); the CLI verb is refused inside a workflow step like every
  `pf msg` verb.
- **Four-field answer, typed failures.** The call blocks until the task
  ends and answers exactly `prompt`, `task_id`, `model` and `duration`.
  A task that ends `failed`, `capped` or `interrupted`, or completes with a
  blank report, is `DAEMON-500` naming the state and the task id, with no
  prompt text; both task slots busy is the retryable `DAEMON-409`; no task
  chain is `DAEMON-400`; no mapped project is `DAEMON-412`. The task record
  is the only stored copy.
- **pi-web's message 「@」 action needs this version.** Its prompt route
  answers `400 {error, upgrade}` below `2.20.20` before any spawn; nothing
  else in pi-web changes floor.

## v2.20.21 (summarize and memory_policy move into cli.toml; `pf config migrate`)

A data migration of each machine's configuration: the summarize lane and
the wrap-summary memory policy stop being workspace `.polyforge.yaml`
sections and become cli.toml tables.

- **The new home.** cli.toml carries one machine-level `[summarize]` table
  (`models`, `language`, `interval_sec`) and the path-keyed
  `[memory_policy."<workspace path>".<default|project>]` family (`record`,
  `skip`). The daemon serves both from its loaded cli.toml (summarize live,
  memory_policy at the next claim); `pf config list` is their one read face,
  `pf config set|unset` writes them (`summarize.*` flat,
  `memory_policy.<entry>.record|skip` with `--workspace`/`--cwd`), `pf init`
  writes a mapped workspace's default policy block into cli.toml, and
  `pf config workspace` no longer carries either. A `.polyforge.yaml` still
  carrying `summarize:` or `memory_policy:` is refused whole — claims and
  dispatches fail with DAEMON-414 naming the file, the line and
  `pf config migrate` (the work item stays queued), and `pf doctor` fails
  its `workspace yaml` row the same way. There is no dual read.
- **Deploy order, per machine.** (1) Install the new pf. (2) Align each
  workspace's summarize values in its `.polyforge.yaml` — `models`,
  `language` and `interval_sec` must agree across the workspaces of the
  machine (one machine-level table; set the `language` you want). (3) Run
  `pf config migrate`. (4) Restart the daemon. Ordering hazard: never run
  `pf config migrate`, `pf config set summarize.*|memory_policy.*` or
  `pf init` — any write of the new tables — while the old pf is still
  installed: the old parser refuses the unknown tables and every `pf`
  command on the machine (the daemon included) would fail to load cli.toml.
- **Refusal and list.** `pf config migrate` writes nothing and exits 1 with
  an itemized list when the summarize values disagree (between workspaces,
  or with an existing `[summarize]`), when a path's existing cli.toml
  memory_policy block differs from its yaml section, or when a section is
  invalid; it never picks a side. Fix the listed sources and re-run. An
  interrupted run converges on the next one; an already migrated machine
  prints `nothing to migrate`, changes no byte and exits 0.
- **Backups.** Before changing a file the command writes a sibling
  `<file>.bak-<UTC timestamp>` holding the pre-change bytes — cli.toml and
  each changed `.polyforge.yaml`; an unchanged file gets none and an
  existing backup is never overwritten. `pf doctor`'s workspace layout row
  lists a `.polyforge.yaml.bak-*` as an entry to move to `scratch/` once
  the migration is confirmed.
- **Rollback.** There is no reverse-migration command: stop the daemon,
  restore the backups over cli.toml and each `.polyforge.yaml`, then
  install the older pf.
- **One-batch deploy with the pi-web follow-up.** pi-web's configuration
  panel still reads these keys from `pf config workspace` and writes
  `summarize.* --cwd`; this release refuses both. Do not install it on a
  production machine before the pi-web follow-up that moves the panel onto
  `pf config list` / `pf config set` ships: the two are deployed as one
  batch, and no compatibility path exists as an alternative.
- **Post-deploy checks.** `pf --json config list` shows the three
  `summarize.*` items with the intended values (and the `memory_policy`
  blocks per workspace path); no mapped workspace's `.polyforge.yaml`
  carries `summarize:` or `memory_policy:`; `pf doctor` reports no failing
  row; and the next claimed work item's `runtime-state/memory-policy.json`
  equals the one a claim of the same project wrote before the migration.

## The micro lane: pure text changes deliver on fast evidence

- **New shipped bundle `micro`.** `fix` (inherit, recall) → `review`
  (fresh, gate) → `deliver` (gate, the shared definition), both gates
  rewinding to `fix`, `rewind: {max_total: 2, max_per_step: 1}`; no
  grill, spec, plan, e2e-author or verification step — the ticket body is
  the spec. File a ticket on it with `--bundle micro`; no queued or
  in-flight work item changes lane.
- **Opt-in per repository, fail-closed.** A repository opts in only by
  declaring `micro_path=<glob>` lines (and, optionally,
  `micro_check=<command>` extra evidence commands) in its own
  `.polyforge/verify-scope.conf`: `*` matches within one path segment,
  `**` crosses `/`, a pattern without a wildcard is that exact path.
  `AGENTS.md` and `CLAUDE.md` are never in bounds. A repository that
  declares nothing is refused by the lane and sees no other change; its
  JSON report keeps its key set. No repository is opted in by this
  release.
- **The base declaration governs.** The driver and
  `tools/verify-scope --micro` read the conf at the diff base (the fetched
  origin default branch), never the change's own, so a micro change cannot
  widen its whitelist.
- **Machine termination.** Before `review` the driver judges every
  touched repository's diff (both sides of a rename or deletion) against
  that declaration. A path outside it, an undeclared repository or an
  unreadable diff/conf ends the run `failed_gate` — no rewind, no merge,
  no human question — with `termination_reason` (new, top level in
  `execution.json`), the `fast_lane_out_of_bounds` event and the failed
  close's hub note naming the paths and the `autopilot` re-file. In
  bounds, the driver records `fast_lane` (the verdict and the credential
  plan) on `fix`'s output.
- **The narrowed delivery credential.** A lane with a `deliver` step and
  no `verification` step merges unattended on the review pass covering the
  delivered HEAD plus the deliver dispatch's own re-run of every
  `fast_lane` command (the repository's fast phase, then its micro
  checks), each exit 0 after its last merge and before `gh pr merge` —
  never a `pf bar-run` reuse. This is the owner's ruling, limited to
  declared text paths. Every lane with a verification step (`autopilot`,
  `bug-fix`, `chore`, `feature-full`) keeps its merge credential
  unchanged; `release` is untouched.
- **Engine.** `tools/verify-scope` learns the two keys (echoed as
  `micro_paths` / `micro_checks` when declared) and `--micro`
  (`--json` / `--run`): the fast phase plus the micro checks of the base
  conf, phase `micro`; `micro-<n>` becomes a reserved check name. The
  default report is otherwise unchanged.
- **Discipline-③ observation window.** This release changes
  `workflows/feature-full/steps/grill.md` (the Lane proposal names
  `micro`), the shared `deliver` step and adds `workflows/micro/steps/`:
  per docs/contributing.md discipline ③ it opens the observation window —
  a clean rig, at least 10 real tickets of mixed bundle types (at least
  one of them a `micro` run, which exercises the changed surface),
  owner-answered needs_input and the discipline's readings compared
  against the previous window — which blocks the next cutover-grade
  release, not this merge. The owner files the window ticket at merge.

## `doc.module-map`: a revisioned memory type

- **Create enforces the convention.** `POST /api/v1/memories` with type
  `doc.module-map` now needs the title `<repo>/<module> rev<N>` (N a
  decimal ≥ 1), tags containing that repo and module, and a body line
  `baseline @<commit> <YYYY-MM-DD>`; otherwise it answers one `MEM-400`
  naming every missing piece and stores nothing. `supersedes` stays
  optional and is never read. Every other type validates as before.
  Existing rows are not touched or re-validated.
- **Recall ranks the current revision first.** In both keyword modes
  (`pf recall`, `--any`) and the short-term scan, the revisions of one
  module key (from the row title) rank at the place of the newest one
  (latest `updated_at`), newest first, whatever their bm25. Older
  revisions stay returned and readable; other types, other keys and
  unparseable titles keep the previous order. Browse's default
  `sort=updated` already listed the newest first and is unchanged.
- **Console.** The `/web/` memories module gives `doc.module-map` a
  ninth kind hue (`--k-module-map`, both themes).
- **No action needed.** No configuration, migration or deploy-order
  change; a `doc.module-map` written by an older client that misses the
  convention is refused and has to be re-sent conforming.

## Same-repo full bars run one at a time

- **Behaviour change.** Full-tier bars of the same repo no longer run at
  once across work items: a full-tier `pf verify-run` takes the daemon's
  per-repo bar lock of every repo it runs a bar on before its first bar
  (in repo-name order) and holds them until its last command, and a
  deliver `pf bar-run` of a full-tier work item takes it around its one
  re-run.
  Waiters queue first-come first-served; the lock is keyed by the repo
  name (`repos/<name>`), so two capsules of one workspace repo contend.
  Scoped bars, fast gates and everything else run as before.
- **New live key.** `[daemon] max_concurrent_bars_per_repo` (unquoted
  integer >= 1, default 1; `0`, negative and non-integer values are
  refused) sets how many full bars of one repo run at once. It is
  independent of `max_concurrent_jobs` and live: `pf config set` / a
  reload applies it to the next admission without a daemon restart.
- **A queued call ends and is called again.** The queue place belongs to
  the step, not to one call. A `pf verify-run` or `pf bar-run` whose lock
  is not the step's when it arrives waits at most until its own deadline,
  then runs nothing and exits 75 with `queued: …` (still queued) or
  `granted: …` (granted while it waited; kept for the step for 2 minutes);
  the step calls the same command again, which claims the place. A bar
  therefore always starts in a call that did not queue: queued time counts
  toward neither `--deadline`/`--budget` nor the harness's shell-call
  maximum, and the daemon's supervision leaves it out of `tool_hung` and
  `step_timeout`. The verification and deliver step prompts say so.
  `result.json` records `bar_locks` per repo.
- **Visible waits.** `pf watch <wi> --once` shows `bar wait: queued #<n>
  for the <repo> bar lock, held by <work item>`, the statusline progress
  line appends `bar-wait=<repo>#<n> holder=<work item>`, and `--json`
  carries `bar_wait {repo, position, holder, holder_slug}`.
- **Release and recovery.** A running bar's holder is its calling
  process: its slot frees on release or as soon as that process is gone
  (SIGKILL included); a place parked between calls leaves once its step
  process is gone.
  The lock lives in daemon memory only — a daemon restart drops every
  grant and queue. Inside a step a lock the daemon cannot give stops the
  run (`pf verify-run`: `incomplete: <repo> bar lock unavailable: …`;
  `pf bar-run`: nothing runs); a manual `pf verify-run` outside a step
  runs unlocked and records so.
- **New route.** `POST /step/bar-lock` serves the calling work item's live
  verification or deliver step process only; every other caller is
  refused `DAEMON-403`.
- **No action needed.** The default serializes same-repo full bars; raise
  `max_concurrent_bars_per_repo` to allow more. No migration or
  deploy-order change.

## Dependency-audit gate in the full verification bar

- **What changes.** The release-shipped `tools/verify-scope` appends one
  full-phase command named `audit` to every full-tier report of a
  repository with an analyzer and a Go module or an npm lockfile: the
  `tools/audit-deps` helper beside it runs `govulncheck` (symbol mode) and
  `npm audit --audit-level=high` over the whole lockfile tree, dev
  dependencies included, and fails the bar on any finding a valid,
  unexpired row of the repository's `.polyforge/audit-exemptions.conf` does
  not exempt — a newly published advisory on an unchanged dependency
  included. A missing tool or an unreachable advisory source fails it too.
  No conf key removes or weakens it; `audit` becomes a reserved check name.
  The `--json` shape is unchanged (`schema` stays `1`).
- **When it activates.** With the release that ships this engine: the
  verification lane runs the engine from the release checkout, so the gate
  is inert until that release is installed.
- **Action before that release.** Every host that runs the full bar installs
  the tools first — `npm` and
  `go install golang.org/x/vuln/cmd/govulncheck@v1.9.0`
  (docs/install.md, "Dependency-audit tools"); `pf doctor`'s new warn-only
  `audit tools` row names what is missing. A repository whose tree carries
  high or critical findings records owner-ruled exemption rows (at most 90
  days) or fixes them; otherwise its full bar is red from that release on.
- **Release unchanged.** `.polyforge/release.sh` keeps its production-only
  `npm audit --omit=dev --audit-level=high` gate; only its comments now name
  the two scopes.

## A blocked work item's holder operator may resume or cancel it

- **What changes.** The aihub doors `POST /api/v1/work-items/{id}/resume-blocked`
  (with or without `accept_lost_work`) and `POST /api/v1/work-items/{id}/cancel`
  on a **blocked** work item admit one more caller beside the reporter, a
  project maintainer and a global admin: the current `operator_id` of the
  machine named by the work item's latest `session_claimed` or
  `session_attached` event, provided that user is a project writer or
  maintainer now. No liveness test applies: an offline machine's operator
  keeps the right, a machine row handed to another operator moves it, and a
  later claim or attach moves the holder. Cancel on a queued, running or
  paused work item, paused resume and `wrap` are unchanged; a non-member
  still gets the missing-work-item `CORE-404`.
- **Audit.** When holding the machine is the caller's sole basis, the
  `wi_resumed` and `work_item_canceled` events also carry
  `basis: "holder_operator"` and the holder `machine_id`; any other caller's
  event payload is unchanged. The actor stays the authenticated caller.
- **Refusal.** A refused member on a blocked work item with a recorded holder
  gets an `AUTH-403` whose detail names the holding machine's id and says its
  operator may resume or cancel; it never names the operator's user id. With
  no holder recorded the existing wording stands.
- **Convention, not enforcement.** "Resume is a human verb" stays a code
  convention: only `pf resume` calls the resume-blocked door; the hub does
  not distinguish a human operator from that operator's daemon.
- **Action.** None: no route, status, event type, config or migration is
  added. The widened gate is live once the aihub server running this release
  is deployed.

## One Host, many Cells: the per-Cell data layout

- **What changes.** The daemon is one Host serving one Cell per AIHub
  profile, and every Cell's state lives under `<data-dir>/cells/<profile>/`
  — the `default` Cell included (`daemon-id`, `machine-key`,
  `runtime-state.json`, `claiming.json`, `submit-queue.json`,
  `manual-models.json`, `events/`, `git/`, `bar-runs/`, `model-health.json`,
  `task-reports/`, `step-agent*/`, `claude-config/`, `summarize-cwd/`). The
  Host root keeps the Host state (`chats.json`, `chat-subs.json`,
  `daemon.lock`, the socket, `release/`) and `layout-version`.
- **Breaking.** The new runtime refuses a data dir that carries the pre-Cell
  layout and no `layout-version`, naming `pf migrate cell-layout`; it never
  migrates at start. A fresh, empty data dir is initialized.
- **Action.** Stop the daemon (`pf ops shutdown`), then run
  `pf migrate cell-layout` (plan: prints every item, writes nothing),
  `pf migrate cell-layout --apply` (copies the items into `cells/default`
  — the root copies stay untouched as the pre-migration copy — annotates
  the existing subscriptions with `profile: "default"` in place, keeps chat
  ids, machine id and key, and writes `layout-version` last; a re-run
  converges), then `pf daemon ensure`. `pf migrate cell-layout --rollback`
  returns to the pre-migration copy while no Host has served the new
  layout yet (`cells/default/migration-accepted-work` absent);
  afterwards repair forward. The migration refuses while a daemon answers.

## Cell safety fixes after the multi-Cell layout

- **Migration.** Once `layout-version` is `cells-v1`, `pf migrate
  cell-layout --apply` is a no-op that says the data dir is already on the
  cell layout: it reads and writes no Cell state, so nothing written after
  the migration is ever replaced or deleted. The accepted-work marker
  without `layout-version` is refused (repair forward, never re-planned).
  `--rollback` is refused, naming each blocker and changing nothing, unless
  the new layout is byte-identical to the pre-migration copy (a named Cell,
  a non-member entry of `cells/default`, a changed member or changed
  subscriptions all block it). A mutating run (`--apply`, `--rollback`,
  `--restore-backup … --confirm`) holds the data dir's `daemon.lock` for
  its whole run; a Host start meanwhile is refused on that lock.
- **Connection rotation.** While a Cell's same-identity `api_key` rotation
  is pending, `pf task spawn` is refused `DAEMON-409` like a submit or an
  automatic claim (nothing queued), and a running one-off task keeps the
  Cell from switching; no admission can race the switch.
- **Execution-disabled Cells.** A Cell whose execution is disabled refuses
  `pf task spawn`, `pf resume` and every `pf answer` but the reserved
  `cancel`, naming its reason; reads, cancel and running work are
  unaffected, and the reopen rules are unchanged.
- **Cell ceiling.** A reloaded `cell_max_concurrent_jobs` governs the Cell's
  admission at once, clamped to the Host ceiling; unset falls back to it;
  lowering it kills nothing.
- **Profile resolution.** A business command's cwd routing reads the Host's
  effective workspace index only (a removed Cell file no longer demotes it)
  and fails `DAEMON-410` naming that index when it cannot be read — nothing
  is sent.
- **Inherited connection variables.** `pf config`, `pf profile`,
  `pf doctor`, `pf init`, `pf migrate` and `pf version` run under an
  inherited `PF_SERVER`/`PF_API_KEY`, ignoring it with one stderr notice
  that names the variable and never its value; `--server`/`--api-key` stay
  refused on every verb and the variables on every other verb.
- **Action.** None beyond the release itself: no config key, route, data
  format or migration is added.

## The unified v0.2.x line and the merged Pi Web package

- **One version line.** Pi Web now lives in this repository (`pi-web/`, its
  own MIT npm package `@silgrid/pi-web`) and every artifact of a release
  shares the one tag: the pf binaries, the AIHub image digest, the Pi Web
  tarball `silgrid-pi-web-<tag>.tgz`, `SHA256SUMS` and the new
  `release-manifest.json`. The first release of the unified product line is
  `v0.2.0`.
- **The one accepted cutover.** The internal `v2.20.x` line is retired.
  `.polyforge/release.sh` cuts exactly `v0.2.0` from a `v2.20.x` floor and
  then patches inside `v0.2.x`; `tools/install.sh` accepts any `v0.2.x`
  release over an installed `v2.20.x` pf (so a `v2.20.x` machine still
  crosses after `v0.2.1` ships). Every other downgrade (`v0.2.2` →
  `v0.2.0`, `v2.20.22` → `v0.1.9`) is refused before anything on the
  machine changes, naming the pf it still runs. `.polyforge/deploy.sh`
  promotes a `v0.2.x` AIHub over a running `v2.20.x` one as an upgrade (no
  `--force`; its SQL plan runs only the `v0.2.x` dirs). The AIHub's
  unsigned-heartbeat gate counts a `v0.2.x` daemon as at or after the
  signing release, so an unsigned beat claiming `v0.2.x`, or on a row a
  `v0.2.x` beat stored, is refused (`AUTH-401`). Pi Web's pf capability
  floors count a `v0.2.x` pf as newer than every `2.20.x` floor.
- **Pi Web install.** `tools/install.sh --with-pi-web` installs the Pi Web
  tarball of the same release (checksum-verified); without the flag the
  installer installs `pf` only, and `npm install -g @silgrid/pi-web`
  stays an equivalent standalone path. `pi-web update` follows the same
  release channel.
- **Action.** Promote the AIHub with `.polyforge/deploy.sh v0.2.0 --confirm
  v0.2.0` and upgrade work machines with `tools/install.sh`, like any
  release; no config key, route, data format or migration is
  added.

## Incomplete results and owner continuation

- **What changed.** A step result with `outcome: incomplete` now carries
  `output.blocker` with `reason`, `evidence` and `required_action`, all
  non-empty strings: what makes further authorized work impossible, the
  observed fact or artifact that shows it, and the external action that
  would unblock it. A submission without that evidence is refused at
  `pf result submit` and at the driver's accept gate, using the existing
  envelope-repair path. Ordinary remaining work, task size and a desire
  to hand off are not blockers; the step continues while useful
  authorized work remains.
- **Owner continuation.** A step re-dispatched by `pf resume` carries
  its previous recorded output and cumulative runtime in its prompt,
  preserving existing progress rather than starting a fresh task. The
  continuation text is also present when the harness resumes with a
  shortened prompt; the accumulated runtime is not reset.
- **Round and output bounds.** A gate verdict closes its continuation;
  the next automatic gate evaluation starts fresh. Historical output is
  quoted as data and capped at an 8 KiB excerpt, with a pointer to the
  recorded artifacts when truncated.
- **Upgrade action.** No config, path or stored-state migration. Existing
  blocked work items can resume; their next incomplete result must meet
  the new `output.blocker` contract. The unified v0.2.0 release includes
  the fix previously installed locally as v2.20.23.

## Pi Web deploy and test tmux isolation

- **Deployment.** The caller's inherited TMUX selector no longer chooses
  the Pi Web service server. Deployment clears TMUX/TMUX_PANE and honors
  its explicit scratch directory or the normal default socket.
- **Tests.** Scratch cleanup selects its own socket explicitly. Unit and
  browser runners isolate HOME/tmux routing and remove operator pf
  connection overrides while retaining task identity guards.
- **Action.** No configuration or stored-state migration is added. Full
  service-mutating validation runs in a dedicated container, outside the
  live Pi Web process and socket namespace.

## First public unified release and publication checks

- **Version.** The first unified public release also cuts over from the
  existing public v0.1.x floor to exactly v0.2.0. The internal v2.20.x
  cutover remains unchanged; later releases use the highest v0.2.x tag.
- **Checks.** Public baseline scanning evaluates synthetic exceptions on
  credential-shaped matches only. External-scanner resolutions bind to
  reviewed file hashes and finding counts, and npm tarball members are
  unpacked before scanning. Public Pi Web provenance retains upstream
  attribution while stripping its private import snapshot.
- **Action.** No installed-state migration is added. The private GitHub
  Release and the public GitHub Release remain distinct channels; npm
  publication additionally requires a valid package-publisher identity.

## Release lane: a claimed version resumes, preflight first

- **Procedures (in effect from the merge).** `release.sh` resumes a
  version whose tag is already on origin's head instead of refusing it:
  a complete release runs nothing, a partial one publishes only what is
  missing and re-reads the GitHub release and the registry right before
  each publish action (any change refuses, nothing more pushed). A
  preflight (tools, node major, the ghcr.io credential) runs before the
  gates, and every gate failure says no version was claimed.
  `RELEASE_GH_REPO` defaults to `silgrid/polyforge` and must name
  the repository origin's url and pushurl name; a remote that is not a
  github.com URL needs `RELEASE_REMOTE_IS`. `deploy.sh` gains a read-only
  `--preflight` and the same origin check (`DEPLOY_REMOTE_IS`).
- **Step text (in effect with the next daemon install).** The release
  step resumes or adopts a tagged head (`resumed: true`) instead of
  refusing it, runs `deploy.sh --preflight` first
  (`PF-RELEASE-PREFLIGHT`), and ends `PF-RELEASE-INCOMPLETE` when a
  procedure cannot resume; `PF-RELEASE-ALREADY_RELEASED` remains for two
  release tags on the head or a tag a kept pre-release list lacks on
  another commit. The verify step's check (b) accepts a resumed tag.
  Until a work machine's daemon carries this bundle, a re-filed release
  work item still refuses a tagged head: resume by running `release.sh`
  by hand from the same commit.
- **Action.** No configuration or stored-state migration. A checkout
  whose origin is not a github.com URL (an ssh host alias, another host)
  sets `RELEASE_REMOTE_IS` / `DEPLOY_REMOTE_IS`; an ambient `GH_REPO`
  naming another repository now refuses a deploy instead of redirecting
  it.

## Daemon command PATH, its doctor row and the recorded tier fallback

- **Configuration (in effect with the next daemon install).** A new live
  `[daemon] command_path` key sets the `PATH` of the commands the daemon
  runs in-process: a daemon-run verification bar's commands and its scope
  engine `tools/verify-scope` (the engine's `go list` included). Unset, they
  keep inheriting the daemon process's own `PATH`, as before. The value is a
  quoted list of absolute directories (`pf config set command_path
  /usr/local/go/bin:/usr/local/bin:/usr/bin:/bin`). Commands a step agent
  runs (`pf verify-run`, `pf bar-run`) are unchanged: they inherit the
  agent's environment.
- **Doctor.** `pf doctor` gains the warn-only `daemon command path` row: the
  daemon's effective command `PATH` (GET /status `command_path`) and whether
  `git` — and `go`, when a mapped clone is a Go repo for the engine —
  resolve under it.
- **Records.** When a requested scoped verification proves only the full
  tier, `result.json`'s `tier` carries `reason` (per repo the engine's
  fallback diagnostic) and `report-mechanical.md` and
  `artifacts/verification-report.md` state it. The engine's `go list`
  failure diagnostic now names the command, its exit and its first output
  line (`go list failed: … exited 127: …: go: not found; falling back to the
  full tier`).
- **Action.** On a machine whose toolchain enters `PATH` only through a
  shell profile (`.zshrc`, `.bashrc`), run `pf doctor`; on a `daemon command
  path` warning set `command_path` to a `PATH` holding the toolchain
  instead of linking the binaries into a system directory. No stored-state
  migration.

## Heavy-run admission: a shared repo follows the admission order

- **Behavior (in effect with the next daemon install).** Queued daemon-run
  verifications that each lock one repo (the usual case) now get a repo
  they share in the admission queue's order — the work item's priority,
  then when the run first queued, then arrival — instead of the order in
  which their bar-lock reservations happened to be taken. A higher-priority
  run that queues after lower-priority runs reserved a repo goes ahead of
  every one of them not yet granted it, also while the slot or load gate
  still holds it; a run already granted the repo keeps it (nothing is
  preempted) and is shown ahead. The position `pf ops status`, `pf watch`
  and `GET /status` / `GET /jobs/{wi}/watch` (`verify_queue.position`)
  show is that order.
- **Runs that lock several repos: best-effort.** Such a run takes its
  places one repo at a time in name order, so until it holds the repos
  before a later one it has no place in that repo's queue: a lower-ranked
  run already waiting there may get the repo first, and until it does the
  shown positions do not match the grant order (the several-repo run's is
  optimistic, the other run's pessimistic).
- **Step bars.** A step's bar-lock place (`pf verify-run`, `pf bar-run`)
  and the daemon's in-process lockers keep first-come first-served and
  never move: the admission reorders only its own queued reservations
  among the places they occupy, so a step bar waits behind exactly the
  places that were ahead of it when it queued.
- **Action.** None: no configuration, wire or stored-state change. The
  admission's state is memory only, and a restart re-queues each run in
  place from its recorded first-queued time.

## Develop build: Bindings commands checked at settle; an unlocatable bound command rewinds build

- **Driver (in effect with the next daemon install).** The develop build
  settle also reads the plan's `## Bindings` the way the verification run
  reads it, and refuses — an envelope rejection: the in-process
  re-dispatch with the reasons, three in a row block — a build with a
  binding whose command (backticks and surrounding whitespace dropped)
  neither contains nor is contained in, in the same repo, the command line
  of a unit whose red and green runs (`pf bar-run`) both ran it. A binding
  may carry the line and more (`<line> && echo PASS`) or a part of it (the
  line without an `export PATH=...;` prefix); an empty binding never
  counts. The rejection names each offending entry, lists the lines a
  binding may name and states the rule. An abbreviation such as `same`,
  which verification used to run as `bash -c same` (exit 127) only after
  the queue and the full bar, no longer reaches verification. A plan with
  no readable binding is still accepted, as before: an unbound unit test
  runs only when the bar covers it.
- **Plan reader (verification and the settle alike).** A `## Bindings`
  table cell is cut only at a `|` outside a code span, and `\|` reads as
  `|`, so a command such as `go test -run '^Test(A|B)$'` is run, and
  judged, whole instead of cut at its first `|`. A list item's label loses
  its Markdown dressing: `**u1:**` and a mapping quoted whole in a code
  span read as `u1`, not `u1:`.
- **Verification routing (in effect with the next daemon install).** An
  authored check — a command the run executed because a binding bound it —
  that exited 1–125 with no failure unit located in its output rewinds
  `build` instead of going to the verify-judge, naming the build units
  whose recorded red and green line the command contains or is contained
  in (not the binding's label); build's rewind input carries the command,
  its exit and the last 40 lines of its log. Still the judge's, as before:
  an exit of 126, 127 or 128 and up (not executable, not found, a signal —
  the machine could not run the command, and a rewind would only spend the
  lane's shared `max_rewinds`), an unlocatable failure of a plan command,
  a command killed at its timeout or at the run's deadline, a bound
  command no unit's line relates to, and the unconfirmed remainder of a
  command whose located failures passed a re-run.
- **Step text (in effect with the next daemon install).** `build` step 4
  writes each binding's command out in full in every entry, never
  abbreviated (`same`, `as above`, `ditto`), containing or contained in
  the line it will run red and green.
- **Action.** No configuration or stored-state migration. A build that
  settled before the install is not judged again; if its verification
  runs an abbreviated binding, that exits 127 and still reaches the
  verify-judge.

## Daemon-run verification in a per-run container

- **Behavior (in effect with the next daemon install).** A daemon-run
  verification bar (an `executor: daemon` verification step) of a
  repository whose merged tree carries `.polyforge/Dockerfile.verify` runs
  its scope engine and every bar command inside a container started for the
  run and removed when it ends, built from that file alone and tagged
  `pf-verify-<repo>:<hash of the file>` (built when missing; its log under
  `runtime-state/verify/<n>/<repo>/`). The container mounts only the repo's
  clone, the run's base-worktree directory and the release's `tools/`
  directory, each at its own host path, plus the named cache volume
  `pf-verify-cache-<repo>` at `/cache`; its `PATH` is the image's, not
  `[daemon] command_path`. Its environment is the image's plus, when the
  daemon's environment sets them, the locale, the proxies, `SSL_CERT_FILE` /
  `SSL_CERT_DIR` (as paths inside the container), `GOPROXY`, `GOPRIVATE`,
  `GONOSUMDB`, `GOFLAGS` and `npm_config_registry`. Repositories without the
  file, and every `pf verify-run` / `pf bar-run` a step agent runs, stay on
  the host. Such a run takes no per-repo bar lock
  (`max_concurrent_bars_per_repo` no longer serializes it);
  `max_concurrent_bars` and `bar_load_threshold` still bound it. Docker
  unavailable, a failed image build, or a `requires_cmd` probe docker could
  not run ends the run incomplete (the infrastructure route, then
  `verify-environment`), never a red bar or a pass. Every docker call but
  the build and the commands themselves gets at most 2 minutes; a container
  a run cannot remove is logged once to the daemon's log (and noted as
  `left` in a complete run's `result.json` container record) and removed
  by the next daemon start, which removes only its Cell's containers of an earlier start (each
  container carries the `pf.verify.boot` label, a nonce per daemon start).
- **Configuration.** Three new live `[daemon]` keys set the container's
  limits: `verify_container_cpus` (default `8`), `verify_container_memory`
  (default `"8g"`) and `verify_container_pids` (default `4096`). Budget
  `max_concurrent_bars` × these limits against the machine. The daemon's
  environment allowlist (`pf daemon ensure`) gains the docker client's
  engine selection — `DOCKER_HOST`, `DOCKER_CONTEXT`, `DOCKER_CONFIG`,
  `DOCKER_CERT_PATH`, `DOCKER_TLS_VERIFY` — and `npm_config_registry`.
- **Doctor.** `pf doctor` gains the warn-only `verify container engine` row
  (the docker client resolves and its engine answers) and `verify container
  paths` row (a throwaway `busybox:1.37` container reads a sentinel written
  under each workspace's `capsules/`, and sees the release's
  `tools/verify-scope`, at the same paths); both pass with nothing to check
  while no mapped clone carries the file. The `daemon command path` row no
  longer asks for `go` on the daemon's `PATH` for a Go repository whose
  clone carries the file (its image provides it).
- **Action.** On a machine whose repositories carry the file, install
  docker for the daemon's user and run `pf doctor`. When the docker engine
  is selected by `DOCKER_HOST` / `DOCKER_CONTEXT` (or a proxy, CA override
  or module mirror is needed), set it in the shell that runs
  `pf daemon ensure`, so the daemon carries the same values `pf doctor`
  sees. When the daemon itself runs in a container, mount the workspaces'
  capsule roots and the polyforge data dir at identical host paths and give
  it the docker socket. A repository adopting the file pins its Go
  toolchain to an exact patch release and bumps it when a new one ships:
  the dependency-audit gate reports the standard-library advisories of the
  toolchain that runs it; its image also carries what the scope engine runs
  (`sh`, `bash`, `git`, `python3`, `awk`) and the toolchains the conf's
  commands call. No stored-state migration.

## `pf bar-run` records a run whatever its output

- **Behavior.** A `pf bar-run` line printing more than about 4 KB (or a
  long command line or `PATH`) used to lose its record: the daemon answered
  the whole record — output tail, line and `PATH` included — in the
  `X-Pf-Bar-Run` response trailer, which Go's HTTP client refuses past its
  read buffer (`the output stream broke: http: suspiciously long trailer
  after chunked body`). The trailer now carries only the record's
  fixed-length facts, `{seq, exit, duration_ms, head, dirty}`; the whole
  entry, its output tail still bounded at 4096 bytes, stays in
  `<data-dir>/bar-runs/<work-item-id>.jsonl`, where the driver reads it. A
  red or green line no longer needs a `| tail` to be recorded.
- **Action.** Upgrade the daemon and `pf` together: an older `pf` reads
  the new trailer unchanged (the fields it prints are all there), and a
  newer `pf` reads an older daemon's trailer only when it was short enough
  to arrive. No stored-state migration.

## Develop build: the grill judgement, e2e scenarios as units, review's e2e quality

- **Step text (in effect with the next daemon install).** `build` now
  grills the ticket before planning: the plan's `## Clarity` carries one
  row per checklist item — `acceptance` (present and testable), `scope`,
  `behaviour` (user-visible texts, layout, interaction), `compatibility`
  (and migration), `dependencies` (other tickets) — each `clear` or `ask`
  with a one-line basis. Every `ask` goes to the owner in one batched stop
  before planning, `build-grill` (reason `grill`), each question with its
  options and a recommended answer in the shared question table; the
  answers are copied verbatim into `artifacts/grill-qa.md` and folded into
  the plan's `## Acceptance` or its new `## Non-goals`. The grill replaces
  the old `build-clarify` stop (the `clarify` reason is gone from build's
  schema). A ticket without `## Acceptance` keeps its acceptance stop: its
  `acceptance` row is `ask`, asked at `confirm-plan` (still on by default
  for such a ticket) or at `build-acceptance` when `confirm_plan: false`,
  never at the grill. `build` also writes the e2e scenarios: a
  user-visible criterion names one (a new or extended suite) or why a unit
  test suffices, and an e2e scenario is a unit like any other — red at its
  test commit, green at its implementation commit through `pf bar-run`,
  bound in `## Bindings`. Every suite file declares `carries:`; one the
  build adds or binds names the unit's touched files. A scenario polls the
  condition it awaits — never a fixed delay instead of polling; a bounded
  poll deadline is fine.
- **Driver (in effect with the next daemon install).** The build settle
  also refuses — an envelope rejection: the in-process re-dispatch with
  the reasons, three in a row block — a plan without `## Clarity`, one
  that misses or repeats an item, a row whose first word after the item
  (and its separator) is neither `clear` nor `ask` (`scope: not clear` is
  neither) or that has no basis, and an `acceptance` row called `clear` on
  a ticket with no `## Acceptance` list; and a recorded `build-grill`
  answer missing from `artifacts/grill-qa.md` (whitespace aside). Each
  `build-grill` answer now records, with the answer, the plan's `ask` rows
  at that moment (a new optional `grill` field on the archived answer in
  `execution.json`); an `ask` row the grill owes that no recorded answer
  covers is refused as an owed human stop — before the acceptance stops,
  and a needs_input asking `confirm-plan` or `build-acceptance` while such
  a row is open is refused too, so a grill answer folded into
  `## Acceptance` never voids that approval. In every task repo the build
  touched (an unknown origin default branch is refused only where a unit's
  commits live or HEAD holds commits no remote-tracking ref has), an e2e
  suite the build added or changed whose `carries:` is missing or
  malformed is refused, and one it added or its plan's `## Bindings` names
  is refused when its `carries:` matches none of the paths it touched
  under the suite's project root — test paths, documentation, `exclude=`
  matches and paths that force the full tier anyway (the analyzer's
  built-in config files such as npm's `package.json`, `package-lock.json`,
  `tsconfig*` and eslint configs, `forced_full=` matches, the conf) aside;
  a suite merely edited (a flake fix) owes only the declaration. A suite
  is what the verification-scope engine discovers from the project root's
  conf at the change's base: only an npm, python or rust root (`stack=`,
  else its one manifest) has suites — `suite_glob=`, else the analyzer's
  default discovery (`e2e/*.mjs`; python `test_*.py` / `*_test.py` under
  `e2e/`; rust `tests/*.rs`) — minus `exclude=` matches, the `e2e_runner=`
  target and a helper the engine's import graph reaches from a suite (a
  real relative import, never a name appearing in a string); a Go
  repository's `test/` files are never suites.
- **Review (in effect with the next daemon install).** A grill answer the
  acceptance criteria or the diff ignore, and a user-visible criterion
  whose plan names neither an e2e scenario nor why a unit test suffices,
  are blocking `ac_gap`; a `## Clarity` row marked `clear` that the ticket
  and the code do not support (the build settled a real question itself)
  is blocking `guarantee`; an e2e scenario waiting by a fixed delay
  instead of polling, asserting before the awaited state settles, or — a
  suite the engine discovers — with a missing or wrong `carries:`, is
  blocking `test_weakened`. The five classes are unchanged. The class
  definitions live in the shared review step, so the `test_weakened`
  wording (a fixed delay instead of polling; `carries:` judged on the
  suites the engine discovers) and the new `guarantee` clause reach every
  lane's review, not only develop's.
- **Routing skill.** `build-grill` joins `confirm-plan` and
  `build-acceptance` as a human-only stop: a session never answers it for
  the owner.
- **Action.** No configuration migration; the archived answer's `grill`
  field is optional and written only on new `build-grill` answers. A
  develop build in flight when the daemon is upgraded is judged by the new
  settle at its next completion: one whose plan predates `## Clarity` is
  re-dispatched with the reason and adds the section.

## The develop lane verifies light and publishes for integration

- **Behavior.** develop's daemon-run verification declares
  `verify_tier: light`: the scope engine's new `--light` report — the conf's
  `forced_full=` globs and `module_root=` spanning rule force nothing, an
  uncovered carries path is diagnosed instead of forcing full, and a nested
  engine root (a subdirectory with its own tracked
  `.polyforge/verify-scope.conf`, such as `pi-web/`) is asked too, its
  commands run behind `cd <root> &&` — with no bar lock. A failing unit is
  re-run once and, still red, run once on the baseline: the newest
  `green/<YYYYMMDD>-<n>` tag reachable from `origin/<default>`, else the
  merge-base. A red is routed by the change's area: a test a build unit
  owns, a bound command, a protected test, a touched file, a suite whose
  `carries:` meet a touched path or a Go test in a touched package rewinds
  `build`; flaky and pre-existing reds pass on record and spend no rewind;
  everything else goes to the verify-judge, whose input now lists the
  touched paths and each red's `carries:`. The conf gains
  `rerun=<name>|<template>` (a command's unit re-run template, `{unit}`
  replaced by the failing unit's name; `--json` reports it as the command's
  `rerun`); this repository binds
  `cd pi-web && E2E_SERVER_MODE=start npm run test:e2e:all -- {unit}` to its
  pi-web e2e bar and adds `base_setup=cd pi-web && npm ci`. An aggregate e2e
  runner's closing summary now proves its command complete (no whole
  re-run). `units[].carries`, `merge_outcomes.<repo>.baseline` /
  `baseline_ref` and `pf verify-run --tier light` are new. develop's last
  step is `publish` (`workflows/develop/steps/publish.md`) instead of the
  shared `deliver`: it pushes HEAD to the feature branch at the
  light-verified head (origin's branch is checked with `git ls-remote`) and
  opens or updates the PR — a published repo without a PR is refused —,
  never merges (`gh pr merge`, a `gh api` pulls merge endpoint or merge
  mutation, `curl`/`wget` to a pulls merge URL are refused), never re-runs a
  test (`pf bar-run` is refused) and never takes a bar lock; the work item is
  wrapped with the attrs `integration: {state: pending, pr, head,
  light_verified_head, repos}`, plus `conflict: true` when verification's
  merge of the default branch conflicted and `waived: [{gate, ruling}]` for
  every owner waiver, which `pf watch` shows (`integration: awaiting
  integration …`, the snapshot's additive `integration` block). An owner
  waiver of verification's FAIL (`rewind_exhausted`) leaves no verified
  head: publish pushes the current HEAD and the hand-off carries no
  `light_verified_head`. A hand-off the daemon cannot read at the close
  closes the work item blocked instead of wrapping without it. The daemon-run
  verification's output gains `merge_conflicts`. A `rerun=` template with
  `{unit}` inside quotes is diagnosed and dropped; a template without
  `-race` never re-runs a red from a run that reported a data race; the
  aggregate summary proves completion only when its total equals the
  runner's announced `e2e/all: N suite(s)`. The other lanes keep `deliver`
  and their tiers.
- **Action.** Deploy the aihub server first (`pf aihub`, before
  `tools/install.sh`): the wrapped `session-end` now carries `attrs`, which
  an older server silently ignores, so a published work item would wrap
  without its hand-off. Then install the release (the engine, the
  workflows and the daemon move together). A develop work item already past
  verification when the daemon upgrades keeps its recorded bundle snapshot
  and delivers as before; new claims publish. Until the integration lane
  ships, published PRs are merged by hand. No stored-state migration.

## The release's test gate runs in the verification container; green tags let it reuse a result

- **Behavior.** `.polyforge/release.sh` no longer runs `make test` on the
  release host. Its test gate is the full verification: the tree's own
  scope-engine plan (`tools/verify-scope --json --full --base HEAD` — the
  conf's fast gates, its full commands and the dependency audit) with every
  command inside the container built from `.polyforge/Dockerfile.verify`,
  run by the new `cmd/pf-gate` through the daemon's container executor
  (the same image tag, mounts, environment allowlist and per-command
  timeouts; the container limits are the pf CLI config's `[daemon]
  verify_container_*` when set, else the defaults). A host-only difference
  — on 2026-11-08 a docker client on the release host's `PATH`, which the
  container does not have — can no longer fail a release whose
  containerized verification passed. A gate that cannot be judged (no
  docker engine, no image, no plan, its deadline, an interrupt) stops the
  release as "not judged", never as a failed gate. Before running it, the
  release asks `tools/green-tag check` for the released commit: a green tag
  on `origin` — an annotated `green/<YYYYMMDD>-<n>` tag whose one-line JSON
  message is `{commit, gate_id, runner, runner_version, plan_digest,
  verified_at, summary}` — recording that commit, this tree's
  `tools/gate-id` (sha256 over the conf's gate lines and
  `.polyforge/Dockerfile.verify`), runner `pf-gate` and the plan digest
  `pf-gate --plan-only` computes for `HEAD` (the commands, prerequisites,
  timeouts, image and container limits as executed) skips the gate;
  anything else runs it. Only `pf-gate --mark-green --summary <text>`
  writes a green tag, after a pass (it pushes only `refs/tags/green/…`);
  `tools/green-tag` only reads (`check`, `latest`). The release notes end
  with a `## Test gate` section naming the reused tag or the run and the
  gate id; the gate's logs stay in `artifacts/release-<tag>/gate/`.
  `release-manifest.json`, the assets and `release.sh --verify` are
  unchanged, so every published release still verifies. The Makefile's
  default `VERSION` now matches only `vX.Y.Z` tags, so a green tag on
  `HEAD` never stamps a build. A gate's container ends itself five minutes
  after the gate's deadline even when `pf-gate` was killed outright, and
  the next gate removes one whose deadline passed.
- **Action.** A machine that cuts releases needs a docker engine that sees
  the clone at its own path (the daemon's verification already needs it;
  `pf doctor`'s `verify container paths` row checks it) and the image's
  first build, which a daemon-run verification of the same clone name
  usually did already. Run the gate by hand with
  `GOWORK=off go run ./cmd/pf-gate` from the clone's root; mark a commit
  releasable with `GOWORK=off go run ./cmd/pf-gate --mark-green --summary
  <text>` from a clean clone of it (the integrate workflow will do this
  once it ships). Anyone who may push tags to `origin` can hand-write a
  green tag, so tag-push rights are release rights. No stored-state
  migration.

## The integration lane merges published work items in batches and marks green

- **Behavior.** A new bundle, `integrate`, runs one work item per batch:
  `collect` (the daemon snapshots the project's wrapped work items whose
  attrs `integration.state` is `pending`, in dependency, priority and
  publication order; only the fix tickets while a red batch's blamed items
  wait for them; a blamed item none of whose fix tickets can release it any
  more is the owner's first: `integrate-hold` — revert, clear, or a new fix)
  → `analyze` (an agent predicts conflicts with `git merge-tree` and
  answers the merge order; skipped under two items) → `merge` (the daemon
  updates each PR branch with the default branch, resolving the conflicts
  `.polyforge/integrate.conf` makes mechanical — `docs/upgrade.md` appended
  in merge order, its pins recomputed inside the daemon (no pin runs a
  command; a projection list's rows are interpreted within fixed bounds,
  and a row outside that grammar, or a form sed would read otherwise,
  bounces the item), and the w254 pin's list of sections unioned line by
  line, nothing spliced inside a line — bounces every real code conflict
  back to develop with a filed ticket (no agent resolves one, and a
  branch's `.gitattributes` `merge=union` resolves none: the lane's clone
  overrides every in-tree merge attribute), accepts a PR branch beyond its
  published head only at an update it recorded before pushing it — on the
  item's record too, so a later batch accepts an update a failed or
  canceled one pushed — and an already merged PR only when its merge
  commit merged the published head or such an update (else the owner is
  asked, `integrate-merged`: `accept` or `skip`), holds an item whose
  blocker did not merge or bounced, lands a multi-repo item whole or not at all (a crash that left
  part of it merged: `landed` with `partial`, and the owner asked,
  `integrate-partial`), and merges with `gh pr merge --merge
  --match-head-commit`) →
  `full-gate` (the release's test gate, `verifyrun.RunGate`, once on the new
  head; a red one triaged: one re-run per failing unit by its `rerun=`
  template, the newest green tag (else the batch's starting main) as the
  pre-existing baseline under the verification's own rule, attribution by
  path, `carries:`, Go package and the scope engine's selection — a single
  candidate only when the unit is red at its merge and not at its first
  parent — else bisection over the default branch's first-parent history
  (a commit outside the batch is the owner's), one develop fix ticket per
  root cause
  filed under a stable key, the owner asked to revert a source whose fixes
  failed two rounds — the revert gated again in the same batch) → `close`
  (the green head tagged `green/<YYYYMMDD>-<n>`, every landed item whose
  every repo's merge is green moved to `merged`). Started by hand: `pf ops
  integrate --project <p> [--max N]`. A work item's `integration.state` now
  moves `pending` → `landed` → `merged` (or `bounced`, `reverted`) through
  aihub's new signed machine route `POST
  /api/v1/work-items/{id}/integration` (fenced on the state it moves from,
  audited as `integration_updated`; the daemon's only), and the work-item
  list accepts `bundle=` and `integration=` filters; that route moves
  `landed` → `merged` only on a green tag, and re-writes a `pending`
  record `pending` only to record the lane's `updates`. A hold is cleared only through
  aihub's new maintainer/admin route `POST
  /api/v1/work-items/{id}/integration/clear-hold` (audited as
  `integration_hold_cleared`; refused while an integration batch of the
  project runs, save its own ruling): `pf ops integrate --clear-hold <wi>
  --reason <text>`, and a batch's `integrate-hold` `clear <reason>` on the
  daemon's own key, which must be a project maintainer's or an admin's.
  Both warn that a cleared hold fixes nothing on a still-red default
  branch. An answer to a lane question that is none of its rulings is
  asked again; it never defaults. A batch's record rides its wrap as attrs
  `integration_batch`. `GET /status?integration=1` (what `pf ops integrate
  status` asks; the plain `pf ops status` does not) adds the pool per
  mapped project and every running batch's step and progress.
- **Action.** Ship aihub first (the new routes and list filters), then the
  daemon. A repository the lane integrates may add `.polyforge/integrate.conf`
  (docs/reference/config.md: `append_only=`, `pin=<pinned> <holder>
  [projected <list>]`, `list=<holder> <comment prefix> <begin> <end>`);
  without it every merge conflict is a real one. A declared list holds one
  whole line per entry between its markers — here each `docs/upgrade.md`
  append adds one line at the end of the w254 pin's list — and two lines
  appended at once are the only list conflict the lane resolves.
  The machine running a batch needs what a release needs — `gh` with merge
  rights, push rights on the PR branches and the green tags, and a docker
  engine for the gate — but no git identity: the lane's merges, reverts
  and green tags carry its own (`polyforge integrate
  <integrate@polyforge.invalid>`). Its aihub key must be a project
  maintainer's or an admin's for a batch to carry out an `integrate-hold`
  `clear`. Work items
  published before this upgrade carry `integration: {state: pending}` and
  join the first batch. No stored-state migration.

## Develop merges itself; integrate gates and tags; daemon steps run named actions

- **Behavior: daemon steps name their action.** A workflow step entry
  declaring `executor: daemon` names the daemon action it runs, `action:
  <name>`, and passes the action's parameters under `with: { <key>:
  <value> }`; the driver looks a daemon step's behaviour up by its action,
  no longer by its step id, and a step finds another's output by the action
  that step runs. The actions are `verify` (the verification bar; `with: {
  tier: light }` runs the light tier, absent the recorded tier; only on the
  step `verification`), `sync-default-branch` and `merge-pr` (`with: {
  pr_from: <an earlier step> }`), which rewind (their step is a gate with an
  `on_fail` target), and the integration lane's `collect-landed`,
  `full-gate` and `mark-green`, which rewind nothing (neither `gate` nor
  `on_fail`). The load refuses a daemon step without a known action, a
  `with:` key the action does not take or a value it refuses (`tier` takes
  only `light`, `pr_from` only an earlier step's id), `action` or `with` on
  an agent step, a `gate` or `on_fail` that does not match the action,
  `verify` on any other step, and the retired `verify_tier:` key (its
  replacement is `action: verify` with `with: { tier: light }`). A daemon
  without a binding an action needs blocks the step, naming it; git and
  `gh` of sync, merge and the integration lane run through one forge
  binding, the daemon's own command environment.
- **Behavior: develop merges itself.** develop is build → review → sync →
  verification → publish → merge. `sync` (`sync-default-branch`) merges
  `origin/<default>` into each task repo's branch in place, as the daemon:
  a merge a crash left in progress is aborted first, a repo the item did
  not touch is fast-forwarded, every in-tree merge attribute is overridden
  for that merge only, and the conflicts the default branch's
  `.polyforge/integrate.conf` makes mechanical are resolved by the daemon.
  `sync` owns every task repo's branch for the whole step: before it merges
  anything it records each repo's branch, HEAD and origin's head of the
  branch in `artifacts/sync.json` (`syncing: true` until the step settles).
  A later pass of the same step (a retried git failure, a failed resolver
  dispatch, a daemon restart, an owner's resume) resets a HEAD that is
  neither where the sync started nor a commit the daemon recorded; the
  discarded commit stays reachable in that repo's clone under
  `refs/polyforge/discarded/<step>/<n>` and is named in a `sync` event and
  in the record's `discarded`. Another branch checked out, or origin's
  branch moved, blocks the step naming it: restore it, then resume. A real
  conflict goes to one fresh-context agent, the conflict resolver
  (`workflows/_shared/steps/resolve-conflicts.md`; its models from
  `steps.<project>.sync`, else the step's own `model_candidates`), which
  only edits and stages in the conflicted repos and answers `resolved` or
  `rewind_build`. After each dispatch the daemon checks every task repo
  itself, not only the conflicted ones: HEAD, the branch checked out and
  origin's branch unchanged; a conflicted repo's merge still in progress; a
  repo with nothing to resolve with no merge in progress and its tracked
  changes as they were; and for `resolved` no unmerged path, no unstaged
  change, no conflict marker and every mechanically resolved file
  byte-identical to its own resolution. It re-dispatches the resolver with
  what it refused, then commits the merge or aborts it and FAILs to
  `build`. A git failure is retried twice, then the owner is asked
  (`sync-environment`); a task repo that is a linked git worktree is
  refused. `artifacts/sync.json` records the sync and publish's PR body
  carries it. Conflicted paths with spaces or non-ASCII bytes are read
  verbatim (git's `-z` listings), so the conflict-marker check reads the
  files that conflicted. The light verification follows:
  a unit still red after its re-run runs once on the default branch's commit
  the tested tree carries — the `origin/<default>` head the verification
  itself merged (newer than sync's when the default branch moved since), or
  the merge-base when that merge conflicted and was aborted — never a green
  tag (`merge_outcomes.<repo>.baseline_ref` is `origin/<default>` or
  `merge-base`). `publish` opens or updates the PR, never merges it, and is
  the last agent step (it writes the wrap summary). `merge` (`merge-pr`,
  `with: { pr_from: publish }`) merges the PRs as the daemon with the
  machine's own `gh` account: a PR already merged (a resume) counts only
  when its merge commit merged the published head or an update the step
  made; a closed PR, one based on or heading another branch, one merged at
  a head nobody published, or a branch pushed outside the lane asks the
  owner (`merge-owner`: `retry`, `accept`, `rewind: …` or `cancel`). Under
  the daemon's in-memory merge lock — one per repository, keyed by
  `host/owner/repo` lower-cased, so https and ssh clones of one repository
  share it; every repo of the item locked in one order and checked before
  anything merges — a default branch the head contains, or one that merges
  cleanly, merges as it is (a moved default branch is not verified again);
  a conflict `integrate.conf` makes mechanical is resolved in place and the
  PR branch pushed by the daemon; a real conflict releases the locks and
  FAILs back to `sync` (it counts toward `max_rewinds`). `merge` records an
  update of the PR branch before making it (`updating` in
  `artifacts/merge.json`): a kill -9 between that commit and its record
  makes the update again instead of blocking. Each PR merges with `gh pr
  merge --merge --match-head-commit <head>`, recorded before and after in
  `artifacts/merge.json`. A refused merge is re-checked: merged after all
  at a head the step made, it is taken; merged at another head, its PR
  branch moved or the PR no longer open, the owner is asked (`merge-owner`);
  a default branch that moved meanwhile is merged as the check does
  (cleanly, or by `integrate.conf` with the update pushed under the lock)
  and the PR merged again; a real conflict, or a second refusal, FAILs to
  `sync`. A repo merged before a pass FAILed to `sync` stays on the landing
  record. `sync` and `merge` are never offered `waive`. The work item then
  wraps with the attrs `integration: {state: landed, repos: [{repo, pr,
  head, light_verified_head, merge_commit, accepted}], merge_commits:
  {<repo>: <sha>}, waived}`, written only once every repo merged; a repo
  carries `accepted: true` for a merge the owner accepted, its `head` then
  that merge's branch side. The `pending` state, the record's top-level
  `pr`, `head` and `light_verified_head`, `conflict`, and verification's
  `merge_conflicts` output are gone. The daemon's merge commits and green
  tags carry `polyforge daemon <daemon@polyforge.invalid>` instead of
  `polyforge integrate <integrate@polyforge.invalid>`. A task repo whose
  merge is in progress takes no WIP snapshot at any close point
  (`task_wip_skipped` names it; the hub's older WIP ref stands and a
  re-claim resumes at HEAD).
- **Behavior: integrate gates and tags.** The integration lane is
  `collect` (`collect-landed`) → `full-gate` (`full-gate`) → `close`
  (`mark-green`) and merges nothing: the `analyze` and `merge` steps, the
  merge order, `bounced`, the lane's PR-branch `updates`, partial landings
  and the `integrate-merged` and `integrate-partial` questions are gone.
  `collect` first settles the holds — a landed source a red batch blamed
  keeps the green tag off but never stops anything from merging; when none
  of its fix tickets is open any more (still in develop, or landed after the
  gate that last blamed it: the record's new `red_heads`) the owner is asked
  (`integrate-hold`), and a `revert` ruling is carried out right there —
  then pins each repo's `origin/<default>` head. The batch is the landed
  items whose merges are in the pinned heads; it ends `empty` while another
  batch of the project runs, or when no pinned head is new (each is at its
  newest green tag or at the head the last red gate failed on).
  `full-gate` gates the pinned heads, never the live tip; its triage
  baseline is the newest green tag merged into the gated head, else the
  parent of the batch's first merge in that repo; a revert merges under the
  same per-repo merge lock. When a red gate has failures attributed to
  landed items and others that are pre-existing or attributed to no item,
  `full-gate` first settles the attributed ones — the revert question,
  their fix tickets and each round on its source's record — and only then
  asks the owner (`integrate-red`), naming only the rest; a `close` keeps
  the attributed rounds, and a `rerun` files no ticket twice. A gate whose
  other failures are only pre-existing now asks too, after the tickets,
  where it used to end red without asking. Answering `close` to
  `integrate-environment` ends the batch red at once; the gate is not run
  again on the repos it could not judge (before, it was, and the same
  question came back while the machine stayed broken). `close` tags a green
  head and moves every landed item a green tag covers to `merged`; it keeps
  the items it moves in the capsule's `artifacts/integrate/close.json`
  before the first move, so `green_items` names every item a resumed close
  moved. aihub's integration moves are `landed` → `landed`, `merged` or
  `reverted` only, and a move may not set `repos`, `merge_commits` or
  `waived` (the develop lane's); the batch record `integration_batch` loses
  `merged`, `bounced`, `held` and `partial`, gains `items` and `empty`, and
  its `main` lists the heads a green batch tagged, or only the heads a red
  batch's gate failed on: a repo that passed in a red batch, or whose gate
  could not be judged, is no red head, and the next batch gates it again (a
  red batch still tags nothing).
  `pf ops integrate` loses `--max` and refuses only while another batch of
  the project runs or nothing is landed; its `--json` carries `landed` and
  `held` instead of `pending`, `max` and `stuck_holds`. `GET
  /status?integration=1` drops `projects[].pending` and an item's
  `conflict`, renames `published_at` to `landed_at`, and names the steps
  `collect`, `full-gate` and `close`. The watch snapshot's `integration`
  block loses its top-level `pr`, `head` and `light_verified_head` (each
  `repos[]` entry carries them), and `pf watch` prints a landed item as
  `integration: landed, awaiting a green tag (<repo> <PR>)`.
- **Action.** Drain before deploying: no integrate batch running; the
  integration pool drained (a wrapped work item still `pending` or
  `bounced` is invisible to the new lane); every develop or integrate work
  item claimed on an old bundle finished, or canceled and refiled after the
  upgrade — its bundle snapshot names no action, so it fails to load
  (blocked after the recovery retries; a resume does not help), and `pf
  watch` drops the declared steps of a capsule claimed before the upgrade.
  Then ship the aihub server first (`pf aihub`, before `tools/install.sh`:
  the new move table and `red_heads`), then the release (the workflows and
  the daemon move together). A bundle of your own moves every `executor:
  daemon` entry to its action and `verify_tier: light` to `with: { tier:
  light }`. In `.polyforge.yaml` delete `steps.<project>.analyze` (refused,
  DAEMON-414); the conflict resolver's models go under
  `steps.<project>.sync`, the fix-ticket writer's stay under
  `steps.<project>.full-gate`, and `steps.<project>.merge` names develop's
  daemon step. Every machine running develop work items now merges: its
  `gh` account needs merge rights on the default branch and push rights on
  the PR branches; no git identity is needed. A capsule's task repos must
  be standalone clones (a linked worktree is refused at `sync`).
  `.polyforge/integrate.conf` now drives `sync` and `merge` (and a revert).
  A source a red batch blamed before the upgrade has no `red_heads`, so its
  landed fix tickets count as open until its next red round. A red batch
  finished before the upgrade lists every head it gated in its `main`, so
  each counts as a last red head: such a repo is gated again once its
  default branch moves. No stored-state migration.

## The daemon asks no environment question; the verify-judge asks itself

- **Behavior: an environment failure blocks instead of asking.** A
  daemon-run step that cannot run its bar or gate to a verdict on this
  machine — develop's `verification` (the bar could not run, after two
  re-runs), develop's `sync` (git failed, after two re-runs) and the
  integration lane's `full-gate` (the gate could not be judged, after two
  re-runs) — no longer asks `verify-environment`, `sync-environment` or
  `integrate-environment`. The step settles `incomplete` with a blocker of
  kind `environment` (why, where the daemon's record of the run is, the
  last log's tail) and the workflow parks `blocked`; `pf resume` re-runs
  the step from its records once the machine is fixed (the full gate with
  a fresh retry count). The answers those questions took are gone:
  `rerun`/`retry` is the resume, `rewind: …` has no replacement (an
  environment failure is never the change's), and the integration lane's
  `close` of an unjudged batch is a cancel of the batch.
- **Behavior: the verify-judge asks the owner itself.** The judge's routes
  are `rerun_verify` and `rewind_build`; its `owner` route and the
  daemon-worded `verify-owner` question are gone. When the evidence settles
  neither route the judge ends `needs_input` with its own question; the
  daemon parks it on `verification` verbatim and the owner's answer
  resumes that judge's session on the same run (no bar runs for the
  answer), which then answers a route or asks again. Once the round's two
  re-runs are spent the judge is told so and a `rerun_verify` is refused
  (a rejected envelope) instead of asking the owner on its behalf. A red
  bar is still never passed.
- **Wire (additive).** A pending question records its asker,
  `asked_by: {spawn_n, definition?}`, in `execution.json`, the answered
  question's archive entry and the `needs_input` event; the watch
  snapshot's `execution.pending_question.asked_by` adds the asking
  dispatch's `harness`, `harness_session` and `transcript` from the spawn
  journal (all absent on a question the daemon worded itself). The watch snapshot carries `execution.blocked: {kind,
  step, reason, required_action, evidence?, log_tail?}` while the workflow
  is blocked (`kind`: `environment`, `incomplete` or `driver`).
- **Action.** Answer any open `verify-environment`, `verify-owner`,
  `sync-environment` or `integrate-environment` question before deploying
  (after the upgrade the step no longer reads those answers). A bundle of
  your own that copies `workflows/_shared/steps/verify-judge.md` takes the
  new text and schema. No stored-state migration.

## The daemon asks nothing: agents raise authorization requests, the owner's selection runs the daemon action

- **Behavior: no daemon-worded question remains.** Every needs_input comes
  from an agent session (`asked_by`), and the daemon parses no answer's
  first word:
  - **Spent rewind budget** (`rewind_exhausted` is gone). A gate dispatch
    states the budget (`# Rewind budget`). A FAIL — or a light re-entry's
    `needs_code_change` self-report — the spent budget refuses is refused
    back to its agent (a rejected envelope), which asks the owner itself:
    `needs_input` with the verdict as its output and `options` naming the
    daemon actions `waive`, `one-more-round` and `manual`. The verify-judge
    asks the same way for a red bar (a mechanical rewind past the budget
    goes to the judge). The owner's selection is executed in the answer's
    write, with the same effects as the old answers. Develop's `sync` and
    `merge` have no agent: their spent FAIL parks `blocked`
    (`rewind_budget`), and `pf resume` grants exactly one more round.
  - **Integration rulings** (`integrate-red`, `integrate-revert`,
    `integrate-hold` are gone). The step dispatches the new sub-definition
    `workflows/integrate/steps/ruling.md` (under full-gate or collect; the
    full-gate step's model candidates). It files another fix itself where
    that can still repair the source, or asks with options naming the
    daemon actions (`rerun` | `close`; `revert`; `revert` | `clear`). A
    selected action runs once — its result recorded before the agent is
    resumed with it — and `clear` keeps the hub's clear-hold check.
  - **Merge** (`merge-owner` is gone). A PR merged outside the step is the
    landing only when its merge commit is on the default branch and contains
    the light-verified head (the published head only when the owner waived
    verification, which then verified none), recorded `accepted`.
    Anything else the step cannot take is forge trouble: `blocked`
    (`forge`), released by `pf resume` once the forge is fixed. The old
    `rewind: <instructions>` answer has no replacement: no owner command
    sends a work item back with instructions (cancel and re-file instead).
- **Wire (additive).** A needs_input envelope may carry `options: [{id,
  label, action?}]`; the pending question, its archived answer
  (`options`, `option`) and the watch snapshot
  (`execution.pending_question.options`) keep them. `POST
  /jobs/{wi}/answer` takes `option` (`pf answer <wi> --option <id>`, with
  any `--text` in the same answer). An option that runs a daemon action is
  selected only by `option`: answer text that is its id is refused
  `DAEMON-409` (the question stays pending), text that is exactly a
  non-action option's id selects it, and any other text answers the asking
  agent with no option and no action; a named option must be one of the
  pending question's. Each option that names an action carries the daemon's own fixed
  text for it (`options[].action_text`), and `pf watch --once` lists every
  option as `<id> → daemon action <action>: <that text> (agent's label: …)`;
  an action option whose label names another action is refused when the
  agent asks, and a selected `clear` needs the owner's words (its recorded
  reason). `execution.blocked.kind` adds `forge` and `rewind_budget`. The
  step result envelope's `owner_option` never shipped. aihub's
  "batch running" refusal of `pf ops integrate --clear-hold` now names the
  batch's ruling question (text only).
- **Upgrade.** A capsule parked on `rewind_exhausted`, `merge-owner`,
  `integrate-red`, `integrate-revert` or `integrate-hold` under the old
  daemon is not stuck and nothing is released: at its first run on the new
  driver the question is withdrawn in one write (event
  `legacy_question_withdrawn`, the old text kept on it) and its step runs
  again under the new rules — the gate's agent is dispatched once more and
  asks, the merge re-reads its PRs, the integration step hands the
  situation to the ruling agent. No answer is invented. One thing an old
  question's answer authorized now happens without one: a PR merged outside
  the merge step is taken as the landing when the landing rule holds (what
  `merge-owner`'s `accept` did). Every other old ruling — a waiver, another
  round, a hand-over, a revert, a cleared hold, a closed batch — happens only
  on a new selection.
- **Action.** Answer open questions with those ids before deploying to
  save the extra dispatch. A bundle of your own that copies
  `workflows/_shared/steps/verify-judge.md` or the integrate bundle takes
  the new texts (and `steps/ruling.md`). No stored-state migration.

## The daemon is the notary of a step's commands; long runs are requested

- **Behavior: command evidence is the daemon's record only.** Every agent
  step that declares `requires_command_evidence` — develop's `build` and
  `publish`, the release lane's `release`, `verify` and `deploy`, `micro`'s
  and the deprecated lanes' `deliver`, feature-full's `e2e-author` — runs
  each command it reports through `pf bar-run` (now served to all of these
  steps, not only build and deliver) and names the records in
  `command_evidence` as `{"bar_run": <n>, "exit_code": <exit>}`. The driver
  accepts an entry only when it names a record of that step in the current
  round with the record's exit (and argv, when stated), and replaces it by
  the record's facts; a self-reported entry is refused and the step
  re-dispatched with the reason. The deliver and publish bindings, the
  stored `commands` and review read only those facts, in the record's
  order; a deliver re-run counts only at the pushed head. A deprecated
  lane's agent-run `verification` keeps `pf verify-run`'s evidence until
  those lanes are removed.
- **Behavior: a long run is requested, never waited for.** A notarized
  step may end `needs_input` with `run_request: {runs: [{cwd:
  "repos/<name>", line}], why}` (1 to 8 runs) instead of a question. The
  daemon runs the lines under the heavy-run admission (a full-tier deliver
  re-run also under the per-repo bar lock), records them as bar runs of the
  step, and answers the request itself (`answered_by: daemon:bar-run`),
  which resumes the asking session with each record's number, exit and
  output tail. No person answers a run request (`pf answer` is refused),
  its answer never counts as a person's, and a line the daemon cannot start
  blocks the step with an `environment` blocker; `pf resume` continues the
  request. No line runs twice: each line's start and record are kept on the
  pending request and its output streams to `artifacts/run-requests/`, so
  after a pause, a crash or a restart a recorded line is answered from its
  record and a line that started but never ended is answered as
  interrupted (its output so far named; no later line runs) for the agent
  to judge. The release and deploy procedures are run this way.
- **Wire (additive).** `command_evidence[].bar_run`; the envelope's
  `run_request`; a pending question's `run_request` in `execution.json`
  (its id is the reserved `run-request`; each run carries the daemon's
  `started_at`, `log` and `seq`); the `run_request`, `run_request_started`
  and `run_request_line` events (a run request emits no `needs_input`); the
  watch snapshot's `execution.run_request: {step, runs: [{cwd, line}],
  asked_at}` (never a `pending_question`, and the status face does not read
  `needs_input`).
- **Action.** None for stored state. A step running under the old daemon
  when it is replaced has its envelope judged by the new rule: a
  self-reported envelope collected after the upgrade is refused and the
  step re-dispatched under the new prompt, and a `pf bar-run` in flight
  ends with the daemon (nothing recorded; the step runs it again). A bundle
  of your own whose agent steps declare `requires_command_evidence` takes
  the new evidence rule.

## Chat subscriptions retired; the Pi Web Polyforge panel takes every decision

- **Behavior: no chat routing.** The daemon no longer maps a harness session
  to a chat id, records the chat a submit came from, or routes a decision to
  a chat: the chat registry (`chats.json`), the subscription store
  (`chat-subs.json`), the wake target, passive and fallback chats, owner
  routing, the workspace-scoped broadcast, adopt, mute and the task notices
  sent to a chat are gone, and so is the resident watcher. Removed commands:
  `pf chat` (`pf chat init`), `pf watch --chat`, `--wait-decision`,
  `--statusline`, `--harness-session`, `--harness`, `--poll`,
  `--no-parent-check`, and `pf watch subscribe`, `unsubscribe`, `adopt`,
  `list`. Removed daemon routes: `POST /chat/init`, `GET
  /chats/{id}/subscriptions`, `POST /chats/{id}/subscribe`, `unsubscribe`,
  `adopt`, `ack`, `subscribe-owned`, `GET /chats/{id}/task-notices`, `POST
  /chats/{id}/task-notices/ack`. `PF_CHAT_ID` is no longer read; a submit's
  `origin` carries only its owner (`origin.chat_id` is gone from `GET
  /status` and the watch snapshot; `pf ops status` prints the origin kind
  alone), and `pf task spawn` sends no `origin_chat`.
- **Behavior: the decision feed.** The daemon keeps one machine-level list
  of the human decisions it observed, `<data-dir>/decisions.json` (the
  newest 300 entries, none older than 14 days): every kind the chat watcher
  woke on — `needs_input`, `blocked`, `failed` (from `failed_gate`,
  `failed_rewind_exhausted`, `failed`), `completed` (from `completed`,
  `wrapped`), `canceled`, `paused`, `terminal`, `step_dead`,
  `candidates_exhausted`, `carrier_push_failed`, `submit_rejected`,
  `submit_dropped`, `human_gate_unasked`, `daemon_restarted` — plus
  `superseded` (this machine released the work item: another session holds
  it, or a local close the hub never confirmed), and every finished
  background task with its summary (not the analyze/prompt tasks a caller
  waits on). A close is recorded as it actually ended: a wrap whose final
  push failed ends `blocked`, and a record dropped at a restart records
  aihub's status. An entry needing a human is never pruned. An entry needing a human
  stays open until the state it announced ends (an answer, the work item
  running again, a later state) or a human dismisses it. A blocked or
  paused work item with no local job has its aihub status re-read every
  minute, so a `pf cancel` or a resume made elsewhere reaches the feed.
  Read it with `pf watch decisions` (`--json` = `GET /decisions`), mark
  entries seen with `pf watch dismiss <id>...|--all` (`POST
  /decisions/dismiss`, refused to a step or task process). In the Pi Web
  Polyforge panel each work item's entries — with its pending question and
  discussion and, when the workflow is blocked, its `blocked` cause — sit in
  the item's own panel, opened from the button at the bottom-right of its
  job detail (Work › 作业), where a blocked (whatever the kind) or paused
  item offers 继续 (it runs `pf
  resume <wi>` through Pi Web's new `POST /api/polyforge/resume`; the
  copyable command stays); a background task's summary sits on its row in
  Work › 任务; the status-bar reminder counts the jobs waiting for an
  answer plus the open entries and a click opens the next one; Pi Web no longer starts a watcher in a session,
  shows decision notices in a chat, or names a chat id on a session tab.
- **Behavior: side functions moved.** The machine-role request that rode
  `pf chat init` is `pf ops role [interactive|runner] [--force|--if-unset]`
  (`POST /machine/role`; `--force` takes the interactive role over; an
  explicit choice is kept in the Cell's data dir, `machine-role.json`,
  across restarts, and `--if-unset` leaves it alone). `pf answer`
  and `pf watch <wi> --once` without `--profile` resolve their Cell by the
  cwd and `PF_PROFILE`, like every Cell-scoped command. The Claude Code
  SessionStart hook keeps starting the daemon when it is down and asks for
  the interactive role with `--if-unset` (an explicit `pf ops role runner`
  stands); it no longer sets `PF_CHAT_ID` or prints a chat id. `pf watch
  list|subscribe|unsubscribe|adopt|ack` are refused by name.
  The pi extension `pf-chat.ts` keeps only the same two moves. `pf migrate
  cell-layout` no longer annotates `chat-subs.json`.
- **Behavior: doctor.** New row `retired chat state` (warn) lists
  `chats.json`, `chat-subs.json`, `chat-status/`,
  `migration/chat-subs.json.pre-migration` and the old watcher's runtime
  files (`chat-<id>.lock|.pid|.status`, `session-*.chat` under
  `$XDG_RUNTIME_DIR/polyforge`, else `$TMPDIR/polyforge-<uid>`); `pf doctor
  --prune-chat-state` deletes them. The `claude progress display` row is
  now `claude statusline` (warns while the statusLine still runs `pf watch
  --statusline`) and `pi progress display` is now `pi package`. `pf cell
  cleanup` reports the feed entries naming the Cell instead of
  subscriptions (`--json`: `decisions` replaces `subscriptions` and
  `retained_records`).
- **Action.** Upgrade the release and Pi Web together (the panel reads `pf
  watch decisions`). Remove `pf watch --statusline …` from your Claude Code
  statusLine, then run `pf doctor --prune-chat-state` once on each machine.
  Watchers an old session started keep running until that session ends;
  restart pi and Claude Code sessions to drop them. Scripts calling `pf
  chat init` or `pf watch --chat` move to `pf ops role interactive` and
  `pf watch decisions`. No aihub change; no stored-state migration (the
  decision feed starts empty).

## Four retired lanes removed; an uninstalled bundle is refused before the claim

- **Behavior: the lanes are gone.** `feature-full`, `chore`, `bug-fix` and
  `autopilot` (superseded by `develop`,; retired by the owner
  on 2026-11-09) are no longer installed: `workflows/` ships `develop`,
  `integrate`, `micro` and `release`, and `workflows/_archive/README.md`
  points at their last shipped text. A terminal work item bound to one keeps
  displaying in `pf file show`, `pf queue` and `pf watch --once` and in Pi
  Web: they read the hub record and the capsule's own copy of the bundle,
  never the installed one.
- **Behavior: an uninstalled bundle is refused before the claim.** A work
  item bound to any bundle the daemon's release does not install — a removed
  lane or a name no release shipped — is refused at submit and at claim with
  DAEMON-414 naming the bundle, and stays queued; the auto-pull wave skips
  it. Before, it was claimed, its capsule materialized without the bundle,
  and it ended blocked when the driver could not load it.
- **Behavior: micro's re-file lane.** A micro change outside the declared
  paths ends naming a re-file on `develop` (was `autopilot`).
- **Behavior: driver.** The retired steps' special handling is gone: the
  bug-fix dead ends (`repro_not_reproduced`, `root_cause_inconclusive`), the
  fix's `root_cause_addressed: false` rewind and its `root_cause_false`
  question, the pre-fix base (`pre_fix_heads`), the e2e-author all-green
  question (`e2e_all_green`: `rewind-to-spec` / `skip`) and e2e-author's
  acceptance-time binding and preflight checks; the review owner exit's
  `amend-upstream` routes to the review's rewind target (no authoring-step
  routing). Only `develop` is scoped-capable.
- **Action, before installing: delete the removed steps' keys from every
  workspace `.polyforge.yaml`.** The release refuses a per-step key that is
  no installed step id, and `pf submit` and every claim load the workspace
  yaml first: a yaml that still carries `steps.<project>.<step-id>` for
  `code`, `grill`, `grill-review`, `root-cause`, `spec`, `plan`,
  `e2e-author` or `repro` refuses every submit and claim in that workspace,
  on every lane (`unknown key(s) steps.<project>.<step-id>`; `pf doctor`
  shows the same refusal). Deleting them is safe on the old release: the
  keys are optional and those steps run only on the removed lanes. This
  lists every such key, one `<yaml>: steps.<project>.<step-id>` line each,
  over the workspaces your `cli.toml` maps, and prints nothing when all are
  clean (pass another profile's `cli.toml` as an argument after `-`; it needs
  python3 3.11 or later with PyYAML):

```sh
python3 -I - <<'EOF'
import os, sys, tomllib, yaml
removed = {"code", "grill", "grill-review", "root-cause", "spec", "plan", "e2e-author", "repro"}
base = os.environ.get("XDG_CONFIG_HOME") or os.path.expanduser("~/.config")
for cfg in sys.argv[1:] or [os.path.join(base, "polyforge", "cli.toml")]:
    with open(cfg, "rb") as f:
        workspaces = tomllib.load(f).get("workspace", {})
    for ws in workspaces:
        p = os.path.join(ws, ".polyforge.yaml")
        if not os.path.isfile(p):
            continue
        with open(p) as f:
            steps = (yaml.safe_load(f) or {}).get("steps") or {}
        for project, keys in sorted(steps.items()):
            for key in sorted(set(keys or {}) & removed):
                print(f"{p}: steps.{project}.{key}")
EOF
```

- **Action, after installing.** Re-bind a queued work item on a removed lane
  (`pf file edit <id> --bundle develop`) or cancel it; a claimed or paused
  one cannot be re-bound — cancel it and file anew. No stored-state
  migration: an old capsule's `pre_fix_heads` is ignored.

## Capsule chain: files over the blob limit stay out; a downloads directory

- **Behavior (in effect with the next daemon install).** Before every
  capsule chain commit the daemon leaves out each file over the hub's
  16 MiB chain blob limit (`capsule.MaxBlobBytes`): an untracked one
  through a daemon-owned block of the capsule git-dir's `info/exclude`
  (never the tracked `.gitignore`), a tracked one by dropping it from the
  index (the commit records its removal). Each is listed with path, size
  and sha256 in the tracked manifest `artifacts/oversize.json`, and a
  `chain_oversize_left_out` event names each new file version (`pf watch`
  lists it). A file back at or under the limit returns to the chain at the
  next commit. The chain push no longer sticks on such a file.
- **Downloads directory.** The capsule `.gitignore` gains
  `/artifacts/downloads/`: raw downloads — CI artifacts, traces,
  screenshots, video bundles — go there and are never committed, whatever
  their size; a file the chain already tracks there leaves it at the next
  commit (it stays on disk). Every claim writes the new `.gitignore` and
  every recovery (an operator resume, a daemon restart, a re-derivation)
  rewrites it before its first commit, so an existing capsule gets the rule
  on its next claim or resume with no manual step. Every step prompt names
  the directory beside the capsule root.
- **Diagnosis.** A chain push the hub still refuses for size — a blob
  committed before this change, or by a step's own `git commit` at the
  capsule root — names each refused path, the commit that records it and
  the unpushed range `<pushed>..<head>`: in the `push_failed` event's
  `error` and its `range` and `oversize` fields, and in the note of the
  blocked close it leads to.
- **Action.** None for a capsule whose chain pushes. A capsule already
  stuck on such a blob stays stuck until a human repairs it: read the path
  and range from its `push_failed` event, pause the work item, keep a copy
  of the chain, drop the path from those unpushed commits, then resume. No
  configuration or stored-state change.

## A repo published again after its merge is merged again; the landing record names every merge

- **Behavior: develop's merge lands a reworked repo's new PR.** A merge
  pass that merged some repos and then FAILed back to `sync` carried those
  merges forward and skipped the repos whatever publish named next: a repo
  build changed once more after the FAIL, published with a new PR, was
  never merged, and the work item still wrapped `landed` without that
  change on the default branch, reporting nothing. Every merge now stays on
  the merge record and the landing record, and a repo publish names at a
  new head or a new PR is merged again under the same lock, checks and
  crash records. A repo publish names at the PR and head a merge on record
  already took (as published, as an update the step made, or the merge
  commit itself once the sync fast-forwarded the branch to it) is not
  merged again. A merged PR named at any other head (one its merge did not
  take) FAILs the merge step back to `sync` before anything merges (no lock
  taken): that change lands only through a new PR, which the lane's next
  publish opens (the shipped publish opens one for a merged PR whose branch
  carries new commits), and the merge is never recorded twice. The FAIL is
  charged to the rewind budget like any merge FAIL, and a spent budget
  parks it blocked `rewind_budget`.
- **Wire (changed shape: the landing record).** `integration.repos` has one
  entry per merge — a repo merged twice has two, oldest first — and
  `integration.merge_commits` is `{<repo>: [<sha>, …]}`, each repo's merge
  commits oldest first (it was `{<repo>: <sha>}`). The record is still
  written only once every repo publish names is merged. aihub stores the
  attrs as given (it checks no shape) and the integration lane's update
  route still refuses to rewrite `repos` and `merge_commits`. Every reader
  of the integration lane takes every merge: a landed item is the batch's
  only when each of its merges is in its repo's pinned head; it moves to
  `merged` only when each merge is under a green tag; the triage baseline
  is the parent of the batch's oldest merge; attribution has one change
  per merge, and a single candidate is checked at each of its merges,
  oldest first; a fix ticket releases a hold when any of its merges is
  newer than the gate that blamed the source; a revert reverts each merge
  of the source and of its landed fix tickets, newest first, one revert PR
  per repo (an earlier attempt's branch or PR picked up, a merge already
  reverted skipped).
- **Action.** The integration lane reads no landing record of the old
  shape: a landed item whose `merge_commits` maps a repo to a single sha
  is never batched or moved to `merged`, and a wrapped item's attrs cannot
  be rewritten. Before deploying, list the landed items that carry one —
  `$AIHUB` is the aihub's base URL, `$KEY` an admin's API key (`api_key`
  in `~/.config/polyforge/cli.toml`; another user's key lists only the
  projects it reads), and the list answers at most 1000 items (add
  `&project=<p>` per project beyond that):

  ```sh
  curl -s -H "Authorization: Bearer $KEY" "$AIHUB/api/v1/work-items?integration=landed&limit=1000" \
    | jq -r '.items[] | select(any(.attrs.integration.merge_commits[]?; type == "string")) | .slug'
  ```

  It prints the slug of each such item. A landing record a capsule still
  holds — written by the merge step, not yet carried to aihub by the
  wrapped close — is not in that list; on each daemon machine, for each
  workspace it serves (`<workspace>`), list those too:

  ```sh
  for f in <workspace>/capsules/*/runtime-state/execution.json; do
    jq -e 'any(.integration.merge_commits[]?; type == "string")' "$f" >/dev/null && echo "$f"
  done
  ```

  It prints the execution record of each such capsule; let those work items
  wrap on the running release first (they then show in the aihub list).
  Deploy once both print nothing (the release running now still reads
  them: a green tag over an item moves it to `merged`; one a red batch
  holds stays `landed`, unread after the upgrade). Deploy the daemon and the
  integration lane together (one `pf` binary). No stored-state migration.

## The develop build hands the analysis and a big plan's units to sub-agents; a sub-agent call is supervised on its silence

- **Behavior: the build session orchestrates.** develop's `build` now
  dispatches one fresh-context analysis sub-agent that grills the work
  item, reproduces and locates a reported defect and writes
  `artifacts/plan.md`; the owner's questions stay the session's own batched
  stops (`build-grill`, `confirm-plan`, `build-acceptance`,
  `build-dead-end`). When the plan's units declare more distinct `(write)`
  paths than the threshold in `workflows/_shared/build_subagents.md`, every
  unit is built test first by implementer sub-agents, in plan order (tasks
  overlap only on disjoint write sets in different repo clones); below it
  the session builds the units itself, as before.
- **Behavior: only an isolated sub-agent is dispatched.** A sub-agent must
  run with its step's isolation. Claude's `Agent` tool under `--safe-mode`
  (OAuth mode) does: its sub-agents load no CLAUDE.md. A pi worker does
  not — the `subagent` extension starts `pi --mode json -p --no-session`
  without the step's `--no-context-files`/`--no-skills`/
  `--no-prompt-templates`, and pi has no environment or settings switch for
  context files — so a pi build dispatch carries the driver fact `# Sub-agents
  on this dispatch` and does that work itself. So does a Claude step started
  with `--bare` (token mode): Claude Code 2.1.319 under `--bare` offers only
  Bash, Edit and Read whatever `--tools` names. Such units record `main`.
- **Behavior: every unit records who built it.** Each
  `steps.build.output.units[]` entry carries `implemented_by` (`main` or
  `subagent`); the build settle refuses a unit without it, or with any
  other value, and re-dispatches with the reason.
- **Behavior: a long sub-agent is not a hung tool.** Supervision's rule 3
  (`tool_hung`, the tool window Y) judges a sub-agent call — Claude's
  `Agent` tool, pi's `subagent` tool — on its silence: Y since its
  sub-agent's last record, however long it has run in total. A harness
  heartbeat for a running call is no such record, and any other tool is
  judged on its whole run as before (pi's streamed tool output included);
  the step bound T still counts every minute.
- **Measurement.** `python3 -I tools/review-firstpass <capsules-dir>`
  prints the first review's pass rate by the plan's write set (<=7, 8-22,
  >22 files) and by the units' `implemented_by`, on the 2026-11-09
  baseline's terms; `--csv` writes one row per run.
- **Action.** None for stored state. A develop build in flight when the
  daemon is replaced finishes under the new rules: its next completion
  must name `implemented_by` on every unit (a refusal tells it so). A
  bundle of your own that copied develop's `build.md` takes the new
  `## Sub-agents` section only if you copy
  `workflows/_shared/build_subagents.md` too.

## The owner sends a work item back with a reason: `pf rewind`

- **Behavior: a new owner move.** `pf rewind <id> --to <step> --reason
  <text>` sends a work item back to an agent step before its current one;
  the reason reaches that step's agent verbatim as its rewind input for the
  whole round (the step resumes its previous conversation where it can),
  and every step from the target through the current one runs again in
  order. A running work item (a step running, a question waiting) is
  stopped the way `pf pause` stops it and continues from the target at
  once, its pending question withdrawn (a `question_withdrawn` event keeps
  the text; the decision-feed entry resolves). A paused one is recorded on
  this machine and stays paused; `pf resume` here applies it. For a blocked
  one the CLI checks `--to` against the local daemon's snapshot first
  (nothing opens on a mistyped step), then aihub's new send-back door
  records the send-back in the work item's attrs (`owner_rewind`) in the
  same transaction that returns it to claimable, and whichever claim takes
  it next applies it once; once the capsule chain carries the applied
  send-back the claiming daemon settles the record (a crash before that is
  settled by the recovery that holds the item again), so a later plain `pf
  resume` drops nothing (a record still standing — never applied — is
  dropped by it). A claim refused at start returns the item to blocked with
  the record standing. A finished one is refused `DAEMON-409` with
  `extra.reason` `REWIND_TERMINAL`; one whose daemon step with a side effect
  (develop's merge or sync) is performing its action, or — running — whose
  reserved `cancel` answer is being carried out, `REWIND_BUSY` (on a blocked
  one the send-back supersedes a cancel that never reached the hub). The
  reason is at most 8000 bytes, the same limit in pf, the daemon, aihub and
  Pi Web. It replaces the retired `rewind:` answer to the old merge
  question: re-filing is no longer the only way back.
- **Behavior: budget and record.** A send-back is charged to no rewind
  budget (`max_per_step` and `max_total` bound only the automatic gate
  loops) and is counted on its own: `pf watch <id> --once` prints
  `rewinds: agent N/M (max_total), owner-initiated K (charged to no
  budget)` (every uncharged owner rewind). Each one is an `owner_rewind`
  event and a rewind-ledger record in `execution.json`
  (`rewind_stats.rewinds[]`, class `owner`, with the new `by`, `at`,
  `reason` and, for a send-back aihub recorded, `request`; `gate` names the
  step it was sent back from). The target's `pending_rewind_input` carries
  the new `owner` and `from`.
- **Behavior: a stopped driver writes nothing.** Once a work item's driver
  Run has returned, a late write through its handle (an answer or a binding
  revision racing a send-back's teardown) is refused `DAEMON-409` instead of
  landing.
- **Behavior: new faces.** aihub: `POST /api/v1/work-items/{id}/send-back
  {id, to, reason}` (the resume-blocked door's permission; a human
  session's route — no step agent reaches it: a step process holds no aihub
  key and the daemon's /aihub proxy refuses step and task process trees)
  and `POST /api/v1/work-items/{id}/owner-rewind/applied {session_id, id}`
  (the claiming daemon's settle, fenced on its session, idempotent; no step
  agent reaches it either), the `owner_rewind` attrs key (a create carrying
  it is refused), events `owner_rewind_recorded` / `owner_rewind_applied` /
  `owner_rewind_dropped`. Daemon: `POST /jobs/{wi}/rewind {to, reason,
  by?}` (a step or task process tree is refused `DAEMON-403`); every claim
  applies the `owner_rewind` its claim answer carries; the watch snapshot
  adds `execution.rewind_targets`, `execution.rewind_busy` and
  `execution.rewinds {agent, max_total, owner}`.
- **Behavior: Pi Web.** A work item's panel (the job detail's bottom-right
  button) offers 打回重做 while the work item has an earlier agent step to
  go back to: pick the step, write the reason, and it runs `pf rewind`
  through Pi Web's new `POST /api/polyforge/rewind`, showing the outcome in
  the panel beside the two counts — or, when pf does not answer within 60
  s, "submitted, outcome pending" and a re-read of the work item; while the
  snapshot says busy it shows why and offers no send-back.
- **Action.** Deploy aihub first (the send-back door), then upgrade the
  release and Pi Web together (the panel reads the new snapshot fields). A
  blocked send-back against an aihub without the door is refused with
  nothing changed. Every machine that may claim a work item must run the
  new pf before `pf rewind` is used on a blocked one: an older daemon
  ignores the recorded send-back and continues the parked step as a plain
  resume. No stored-state migration (the new record fields and the
  attrs key are additive and absent on every earlier rewind).

## A Claude step gets the tools it declares, and reads only the harness's settings

- **Behavior: one Claude step profile in every auth mode.** A step on
  Claude Code now runs `claude -p --safe-mode --setting-sources user
  --settings {"enabledPlugins":{"cc-plugin-agents-md@builtin":false}} …`
  (the rest of its argv unchanged) with `CLAUDE_CODE_ENABLE_TASKS=false`
  in its environment, for a token in
  `<harness-root>/claude-config/settings.json` and for an OAuth login
  alike. Token mode no longer runs `--bare`: Claude Code 2.1.319 under
  `--bare` offers Bash, Edit and Read whatever `--tools` names, so a
  token-mode step had no Write, Glob, Grep, TodoWrite or Agent. The
  session's `system/init.tools` now list exactly the eight tools `--tools`
  names (Agent under its legacy init name `Task`). TodoWrite exists only
  while Claude Code's Tasks family is off, hence the environment switch; a
  `CLAUDE_CODE_ENABLE_TASKS` of your own in that settings.json `env` block
  wins over it and removes TodoWrite again.
- **Behavior: the step's context stays clean.** `--safe-mode` keeps
  CLAUDE.md, skills, installed plugins, hooks, MCP servers, custom
  commands and agents and auto-memory out, for the step and for every
  sub-agent its `Agent` tool starts. `--settings` turns off the built-in
  `cc-plugin-agents-md`, which loads AGENTS.md as project instructions
  wherever no CLAUDE.md is found up the path — a capsule's case, under a
  workspace whose root carries an AGENTS.md. `--setting-sources user`
  makes that settings.json the only settings file read: a capsule's
  `.claude/settings.json` or `.claude/settings.local.json` no longer
  redirects the endpoint the credential is sent to, nor adds environment,
  permission rules or hooks (under `--bare` it did).
- **Behavior: token-mode Claude builds dispatch sub-agents.** With the
  `Agent` tool present, the develop build's sub-agent rules
  apply to a token-mode Claude step as to an OAuth one, so its units may
  record `implemented_by: subagent`. A pi dispatch still does that work
  itself.
- **Behavior: Claude Code's own session context.** Like an OAuth-mode step
  before, a token-mode step now gets Claude Code's built-in git status
  snapshot and its commit attribution reminder (a `Co-Authored-By` trailer
  line); `--bare` left both out.
- **Regression.** `go test ./internal/polyforge/runtime -run
  TestClaudeStepInitToolsEqualDeclared` replays the `system/init` recorded
  for the exact argv and environment the daemon builds. After a Claude Code
  upgrade, re-record it with the opt-in live probe (it spends model
  tokens): `POLYFORGE_TEST_REAL_CLAUDE=1
  POLYFORGE_TEST_CLAUDE_HARNESS_ROOT=<harness-root>
  POLYFORGE_TEST_CLAUDE_RECORD=1 go test ./internal/polyforge/runtime -run
  TestClaudeStepRealProbe` — red when the upgrade narrows the tools or lets
  planted context into the step or its sub-agent.
- **Action.** None for stored state; deploy the daemon (one `pf` binary). A
  Claude step process running at the switch finishes on its old flags;
  every later spawn, a resume included, runs the new profile. Remove a
  `CLAUDE_CODE_ENABLE_TASKS` from the claude-config settings.json `env`
  block if you set one.

## The retired lanes' leftovers removed: `pf verify-run`, `pf revise-binding`, the authored-suite surface

- **Behavior: verbs removed.** `pf verify-run` (an agent-run verification
  step's mechanical call) and `pf revise-binding` (the owner's correction of
  a done e2e-author step's binding commands) are gone, with the daemon's
  `POST /jobs/{wi}/revise-binding` route. No shipped lane has either step
  since develop's verification is run by the daemon itself
  (the same mechanical bar, in-process), and no lane authors an e2e suite
  in a step of its own.
- **Behavior: notary.** Every agent step that declares
  `requires_command_evidence` is notarized: the agent-run `verification`
  step's exception (self-reported evidence) is gone with the last lane that
  had one.
- **Behavior: bar lock.** `POST /step/bar-lock` serves a `deliver` step
  only (a daemon-run verification takes its locks in-process, as before).
- **Behavior: the authored-suite surface.** The verification bar's
  `result.json` no longer carries `authored`, `authored_titles`,
  `bound_scenarios` or `unbound_titles`, nor the `authored-tests-unchanged`
  check; the bar no longer reads an e2e-author step's titles, bindings or
  test stamp (the develop build plan's `## Bindings` are read as before).
  The verification schema drops `authored_titles` / `scenarios` and the
  `authored` `x-prompt-when` condition; the deliver merge rule no longer
  reads authored scenario outcomes; `execution.json` no longer carries a
  step's `binding_revisions` ledger.
- **Behavior: deliver's 「全量重验」 override is gone.** The deliver step's
  `rewind_to: verification` output field (a scoped delivery's fourth
  answer) is removed from the definition and the driver: no shipped lane
  reaches it (micro is not scoped-capable and has no verification step); a
  deliver FAIL rewinds to its declared `on_fail` target.
- **Behavior: `pf file create`.** The e2e-specific acceptance-carrier
  warning keyed on an `e2e_author_mode` var is gone (it fired on every
  develop ticket mentioning e2e); the bundle step-table advisory stays.
- **Behavior: the bar's deadline is the caller's.** The tool-window
  ceiling `pf verify-run` clamped its deadline to is gone with the verb:
  the daemon-run bar runs under `[daemon] verify_deadline` as before, and
  its `result.json` no longer carries `deadline_bound` or
  `deadline_clamped_from` (nor the verification output's
  `verify_run.deadline_bound`). The bar's lock is held or the run is
  incomplete — the queued/granted call-again answer and the
  not-applicable answer only the verb's route gave are gone from the bar
  (`pf bar-run`'s own call-again protocol, exit 75, is unchanged).
- **Behavior: in-flight develop work items.** A work item keeps the bundle
  snapshot it was claimed with. A develop item claimed before this
  upgrade carries the old `_shared/steps/verification.md`, whose output
  schema marks fields `x-prompt-when: authored` — a condition this
  release no longer accepts — so its verification records an
  `output_schema_unloadable` event and its output is not schema-validated.
  That validation is record-only on a verification step: the step is never
  blocked by it, and a work item claimed after the upgrade loads the new
  definition.
- **Action.** None: nothing shipped calls the removed verbs or route. A
  script that still calls `pf verify-run` or `pf revise-binding` gets the
  unknown-command exit 2. No stored-state migration: an old capsule's
  `binding_revisions` ledger and a verification record's authored fields
  are left unread; an in-flight develop item's verification output goes
  unvalidated as above.

## Workflow contract 1: one strict lane loader

- **Behavior: one strict loader.** Every reader of a lane — the driver and
  its spawn adapter, the claim check, `pf file create`, the candidate and
  attr composition, the step id/attrs enumeration — reads it through one
  loader that judges it against the workflow contract
  ([reference](reference/workflow-contract.md)): an unknown or removed key
  at any level, a duplicate key, a missing definition, an output schema
  that does not compile and a placeholder that does not resolve are each a
  typed `WFC-*` defect with its file:line and YAML path, all reported at
  once. A lane now declares `contract: 1`, `name` (its directory name) and
  `description`.
- **Behavior: removed keys.** `rewind: {max_total, max_per_step}` is gone:
  a lane with an `on_fail` declares one budget, `max_rewinds`, and no
  per-edge bound applies (micro: `max_rewinds: 2` — two rewinds in total
  across review and deliver, where it had one slot per gate). The step keys
  `context_policy` (every step starts from a clean context: micro's `fix`
  prompt now carries the FRESH context line), `handoff`,
  `requires_human_answer` (the human gate and its `human_gate_unasked`
  event), `mechanical_fix`, `plan_conformance`, `verify_tier` and `kind`
  are refused, as is `defaults.harness`.
- **Behavior: `build_step`.** The lane property `build_step` names the
  step the unchanged-tree guard compares (develop: `build`, micro: `fix`);
  a lane with an `on_fail` must declare it, and every `vars.build_step`
  must equal it. It replaces the driver's derivation of that step and
  nothing else: the develop lane is still recognized by its shape.
- **Behavior: placeholders resolve at load.** A var no placeholder uses, a
  placeholder no var or fragment resolves and a var that shadows a
  fragment fail the lane's load (`WFC-195`), not its first dispatch.
- **Behavior: claims.** A work item bound to a lane the daemon's lane set
  does not carry is refused before the claim (`WFC-123`), stays queued, is
  skipped by auto-pull, and is listed by `pf doctor` (`workflow lanes`) and
  under `pf queue` (`--json`: `lanes_missing`). The claim materializes the
  lane bytes its pre-claim check judged: a lane is never claimed into a
  capsule without its snapshot, and one changed or removed after the check
  does not reach it.
- **Behavior: the snapshot.** The claim's snapshot marker
  (`sync/materialized.json`) records `contract`, `source` and
  `content_hash`. A capsule whose lane snapshot targets another contract
  version starts nothing: its workflow parks blocked with kind `contract`,
  the reason naming both versions and this section. `pf resume` parks it
  again — the re-claim keeps that snapshot rather than materializing the
  daemon's lane over it — and Pi Web offers no 继续 on it, only the cancel.
- **Behavior: daemon start.** An installed lane the loader refuses is a
  release defect: the daemon does not start, naming every defect.
- **Behavior: withdrawn question ids.** The ids the daemon worded itself
  before (`rewind_exhausted`, `merge-owner`,
  `integrate-red`, `integrate-revert`, `integrate-hold`) and their
  withdrawal on a capsule's first run are gone: no capsule of a drained
  deployment is parked on one.
- **Behavior: `pf workflows validate`.** `pf workflows validate
  [--workspace <dir>] [<lane-dir>…] [--json]` judges lanes with the
  daemon's own loader (default: the lane set the daemon runs); any defect
  exits 1. `pf doctor` has the matching `workflow lanes` row.
- **Behavior: the watch snapshot.** `execution.rewinds` carries the lane's
  budget as `max_rewinds` (it was `max_total`); `pf watch --once` reads
  `rewinds: agent N/M (max_rewinds)`. The `human_gate_unasked` decision
  class is gone with the human gate.
- **Action: the drain (every contract version).** A contract version is
  deployed by draining; deploying several versions at once is one drain.
  1. Stop claiming on every machine: `pf ops auto off`, and file no
     `pf submit` until step 3 is done.
  2. Let the work items in flight finish: `pf queue --status running`,
     `--status paused` and `--status blocked` are empty, or each remaining
     one is cancelled (`pf cancel <wi>`) to be filed again after the
     deploy.
  3. Deploy (`.polyforge/release.sh`, then `.polyforge/deploy.sh`): the
     release's lanes load under the new version, or the daemon does not
     start. Check a hand-maintained lane first with
     `pf workflows validate <lane-dir>`.
  4. A work item still holding an old snapshot parks blocked with kind
     `contract` (`pf watch <wi> --once`): cancel it and file it again.
  5. Re-bind every queued work item `pf doctor` (`workflow lanes`) or
     `pf queue` lists under `WFC-123` to an installed lane
     (`pf file edit <wi> --bundle <lane>`, accepted while it was never
     claimed; else cancel it and file it again).
  6. `pf ops auto on`.

## Workflow contract 2: the run action and merge-pr's verified_by

- **Behavior: the `run` action.** A daemon step may run one declared
  command line: `{ executor: daemon, action: run, gate: true, with: {
  command: "<line>", repo: <task repo>, timeout: 20m } }`. The daemon runs
  `bash -c <line>` from the repo's root in the repo's verification
  container (the image of its `.polyforge/Dockerfile.verify` at HEAD) and
  judges the step by the exit code: every exit 0 passes; a non-zero exit
  or the timeout (`timed_out: true`) FAILs to the step's `on_fail`, or ends
  the workflow `failed_gate` without one; the action is never waivable.
  `command` and `timeout` (at most 120 minutes, the whole step) are
  required; without `repo` the command runs in every task repo with a
  commit beyond its default branch, and with none of those the step FAILs
  (the command ran nowhere). The container mounts the repo's clone and
  nothing else of the host: the command never sees the aihub key, gh
  credentials, the daemon's data directory or the host's tmux. Each
  command is one record of the step in the work item's bar-run record and
  one `run_command` event carrying its output tail, credential-masked
  (URL userinfo, `*API_KEY=`/`*TOKEN=`/`*PASSWORD=` values and the known
  key shapes); the step's output is `verdict`, `fail_reason` and `runs[]`
  (repo, command, exit code, duration, `timed_out`, log, output tail,
  bar-run record). A container that cannot be had (no Dockerfile, no
  docker engine, the image or the container not built or started) is
  tried twice more a minute apart, then the step blocks with kind
  `environment` — also when the step's timeout runs out meanwhile, never
  a command timeout. A command runs at least once: a crash or restart
  runs the step again on the next claim, and a container the daemon could
  not remove ends by itself five minutes after the step's deadline. The
  shipped lanes declare
  no `run` step (the release lane is unchanged); the reference
  ([workflow contract](reference/workflow-contract.md#run)) shows a deploy
  script written as one.
- **Behavior: `binding_missing`.** A daemon step whose action needs a
  binding the daemon was not given — a `run` or `verify` step on a daemon
  with no verification runner, a merge with no forge — parks blocked with
  kind `binding_missing` (it was kind `driver`); `pf watch --once` and Pi
  Web name it. Give the daemon the binding, then `pf resume`: the step
  runs again.
- **Behavior: claims.** A work item whose lane has a `run` step naming a
  `repo` that is not a task repo of the work item's project is refused
  before the claim (`WFC-185`, DAEMON-414) and stays queued; the auto-pull
  wave skips such work items up front and logs the refusal once. The
  daemon reads the project's task repos from aihub only for such a lane.
- **Behavior: merge-pr names its verification step.** `merge-pr` declares
  `verified_by: <step>` (required) — the step whose verified heads the
  landing rule reads, under contract 2 an earlier `verify` step — and its
  `pr_from` (required) must be the lane's last agent step; either missing
  or naming another step is `WFC-151`. The shipped develop lane declares
  `with: { pr_from: publish, verified_by: verification }`. The landing
  rule and a waiver's effect are unchanged: a PR merged outside the step
  is taken only when its merge commit contains the head verification
  verified, or the published head once the owner waived verification.
- **Behavior: the contract.** Every lane declares `contract: 2`; a lane
  declaring `contract: 1` is refused (`WFC-101`) naming this section, and
  a capsule claimed under contract 1 parks blocked with kind `contract`.
  The registry's actions table now states each action's gate rule
  (`required` / `optional_on_fail` / `forbidden`), whether it is waivable
  and the daemon bindings it needs.
- **Action: the drain.** Deploy contract 2 by the drain of the workflow
  contract 1 section above (stop claiming, let the work items in flight
  finish, deploy, cancel and re-file what parks blocked `contract`, re-bind
  `WFC-123` items, `pf ops auto on`); deploying contracts 1 and 2 at once
  is one drain. Before deploying, rewrite every hand-maintained lane to
  `contract: 2`, add `verified_by` to its `merge-pr` step, and check it
  with `pf workflows validate <lane-dir>`.

## A claude task gets the tools it declares, a summarize dispatch gets none, and no Claude process opens a messaging inbox

- **Behavior: a claude task runs a step's profile.** A task on a claude
  candidate (`pf task spawn`, and the read-only task spawns behind
  `pf msg analyze` / `pf msg prompt`) now runs `claude -p --safe-mode
  --setting-sources user --settings
  {"enabledPlugins":{"cc-plugin-agents-md@builtin":false}} --output-format
  stream-json --verbose --no-session-persistence --model <m> [--effort <e>]
  --tools <list> --allowedTools <list> --permission-mode dontAsk
  --permission-prompts none --strict-mcp-config --disable-slash-commands`
  in every auth mode. Token mode no longer runs `--bare`, under which
  Claude Code 2.1.319 gave the standard list (Read, Glob, Grep, Bash,
  WebSearch, WebFetch) only Bash and Read, and the read-only list (Read,
  Glob, Grep) only Read; the session's `system/init.tools` now equal the
  list. Like a step, a claude task reads only
  `<harness-root>/claude-config/settings.json` (a workspace's
  `.claude/settings.json` or `.claude/settings.local.json` no longer
  applies), and its session lists none of Claude Code's bundled skills or
  slash commands and neither the built-in AGENTS.md plugin nor the
  telemetry plugin — under `--bare` it listed all of them. Its environment
  carries `CLAUDE_CODE_DISABLE_BACKGROUND_TASKS=1`, `DISABLE_AUTOUPDATER=1`,
  `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1` and
  `CLAUDE_CODE_HARBOR_KITE=0`. A claude-config settings.json that exists
  but is unreadable, not JSON or without an `env` block refuses the task's
  run, as it refuses a step.
- **Behavior: a summarize dispatch has no tools.** The claude dispatch of
  the summarize lane now runs `claude -p <--bare|--safe-mode>
  --setting-sources user --settings {…} --tools "" --model <m>` (the prompt
  still on stdin), with the same four environment switches. Its
  `system/init.tools` are empty; before, token mode offered Bash, Edit and
  Read, and the request sent to the model listed all three, while the
  prompt carries an agent-written transcript tail. The context flag still
  follows the auth mode (owner ruling 2026-11-10): `--bare` for a token in
  the settings.json `env` block, `--safe-mode` for an OAuth login.
- **Behavior: no cross-session messaging inbox.** Every Claude Code
  process the daemon starts — a step, a claude task, a summarize dispatch —
  carries `CLAUDE_CODE_HARBOR_KITE=0`. Without it Claude Code 2.1.319 under
  `--safe-mode` opens a messaging socket (`$TMPDIR/cc-socks/<pid>.sock`)
  and exports `CLAUDE_CODE_MESSAGING_SOCKET` and
  `CLAUDE_CODE_MESSAGING_TOKEN` to every Bash child; a user message a
  step's Bash wrote there reached the step's model and never appeared in
  the step's stream, with the token and without it (the token is optional
  on Linux). Moving the socket into a private directory
  (`--messaging-socket-path`) changes neither: the path still reaches the
  Bash env, and any process of the daemon's user can open it. Now no
  socket is opened and neither variable exists in a step's Bash. The
  switch is an undocumented Claude Code variable; a
  `CLAUDE_CODE_HARBOR_KITE` in the claude-config settings.json `env` block
  overrides it and opens the inbox again.
- **Regression.** `go test ./internal/polyforge/task -run
  TestClaudeTaskInitToolsEqualDeclared` and `go test
  ./internal/polyforge/summarize -run TestClaudeSummarizeInitHasNoTools`
  replay the `system/init` recorded for the exact argv and environment the
  daemon builds; the step's replay (`TestClaudeStepInitToolsEqualDeclared`)
  now also fails on an inbox. After a Claude Code upgrade, re-record with
  the opt-in live probes (they spend model tokens and write session records
  into the harness root's claude-config, so point them at a copy):
  `POLYFORGE_TEST_REAL_CLAUDE=1
  POLYFORGE_TEST_CLAUDE_HARNESS_ROOT=<harness-root>
  POLYFORGE_TEST_CLAUDE_RECORD=1 go test ./internal/polyforge/task -run
  TestClaudeTaskRealProbe`, likewise `./internal/polyforge/summarize -run
  TestClaudeSummarizeRealProbe` and `./internal/polyforge/runtime -run
  TestClaudeStepRealProbe` (the step's probe also has its Bash list the
  messaging variables).
- **Not yet run: an OAuth login.** The recordings come from a harness root
  with a token in settings.json. The summarize dispatch's OAuth-mode init
  (`--safe-mode`: no tools, no inbox) was recorded with no login at all —
  the init precedes authentication, and the run then ends `Not logged in`.
  On a machine with an OAuth login, run the three probes once against a
  harness root holding that login.
- **Action.** None for stored state; deploy the daemon (one `pf` binary).
  A step or task process running at the switch finishes on its old flags;
  every later spawn runs the new profile. Remove a
  `CLAUDE_CODE_HARBOR_KITE` from the claude-config settings.json `env`
  block if you set one.

## Workflow contract 3: declared inputs, outputs and checks; micro delivers like develop

- **Behavior: what a step reads and owes is declared.** An agent step
  declares `inputs` (the ticket fields, an earlier step's output path,
  record or answers, an artifact, a driver fact section, a re-entry
  fragment, the memory snapshot or recall duty), `outputs` (`schema:
  record|enforce`, the contract `artifacts`), `checks`, a gate's
  `verdicts`, an agent gate's `sweep`, `on_fail.charge` and `evidence:
  {mode: notarized, bar_lock}`. The prompt renders exactly the declared
  inputs in a new `# Inputs` section after the step definition (an absent
  one named absent), and a completed envelope missing an output path or
  artifact a later step requires of it is refused. `recall`,
  `requires_command_evidence`, a daemon step's `vars`, any
  `vars.build_step` (`{{build_step}}` is reserved and filled from the
  lane), a sub-definition's `output` and `x-prompt-when` are refused
  (`WFC-1xx`, named in the [reference](reference/workflow-contract.md)).
- **Behavior: records, not driver keys.** The driver's facts live under
  `steps.<id>.record` in `execution.json`: `heads` (every accepted step),
  `last_review_round` (every agent gate), `test_files` (test-first-units)
  and `fast_lane` (micro-bounds); `reviewed_heads`, `verified_heads`,
  `test_files` and `fast_lane` no longer appear in a step's output.
- **Behavior: checks by declaration.** The closing checks run only where
  declared: `owner-stops`, `test-first-units`, `acceptance-coverage`,
  `e2e-carries` (develop's build), `finding-classes` (develop's review:
  five classes; micro's review: `class: code`, the old `repair_target`
  renamed), `publish-binding` (both lanes' publish) and `micro-bounds`
  (micro's fix). Every problem of every check on one envelope is one
  refusal of at most 12 lines; a check's duty is stated in `# Result
  delivery`; a question id a check owns (`build-grill`, `confirm-plan`,
  `build-acceptance`, `resolved-conflict-review`) is asked only by a step
  that declares the check.
- **Behavior: the envelope is stricter.** A gate's verdict is exactly one
  of its verdicts (lower case; no `FAIL: …` prefix match), `gate_failed`
  carries `fail`, a `fail` carries a non-empty `fail_reason`, and a gate's
  envelope without a verdict is refused; `question_id`, `question`,
  `options` and `blocker` are read at the envelope's top level only.
  `verify` defaults to the full tier; `with: { tier: light }` keeps
  develop's light tier. The per-work-item scoped tier, the light,
  mechanical and converging rewinds, the review owner exit and
  `rewind_to` are gone.
- **Behavior: micro delivers like develop.** micro is fix → review → sync →
  evidence → publish → merge, `max_rewinds: 3`. `fix`'s micro-bounds check
  judges the diff when fix completes (out of bounds: the run ends
  `failed_gate`, re-file on `develop`); the daemon syncs the default
  branch, runs the recorded fast evidence in the repository's
  verification container (`run` with `plan_from: fix`), `publish` opens
  the PR — asking the owner `resolved-conflict-review` first when the
  sync's resolver resolved a conflict — and the daemon's `merge` merges
  it and writes the landing record, so a micro work item now joins the
  integration lane's batch. The agent no longer merges; a machine running
  micro needs the verification runner (else the evidence step blocks
  `binding_missing`), and an evidence command must run inside that
  container. No step re-runs a test at publication any more, so a
  repository's `ci_evidence=` lines are read by no step (their one reader
  was `deliver`). `_shared/steps/deliver.md` and
  `_shared/steps/verification.md` are deleted; `publish.md` moved to
  `_shared/steps/`.
- **Action: the drain.** Deploy contract 3 by the drain of the workflow
  contract 1 section above: stop claiming (`pf ops auto off`), let every
  work item in flight finish, deploy, cancel and re-file what parks
  blocked `contract`, re-bind `WFC-123` items, `pf ops auto on`. A micro
  work item that was claimed under contract 2 cannot resume under 3: let it
  finish before the deploy or re-file it. Rewrite every hand-maintained
  lane to `contract: 3` in the new keys and check it with
  `pf workflows validate <lane-dir>`.

## Workflow contract 4: the integration lane is a declaration; a sub-definition takes its step's models

- **Behavior: the integration lane is a declaration.** The lane running
  `collect-landed`, `full-gate` and `mark-green` declares `integration: {
  fix_lane: <lane> }` — the shipped `integrate` declares `{ fix_lane:
  develop }` — and no other lane may (`WFC-172`; also a `fix_lane` naming
  the lane itself or no lane installed beside it). `pf ops integrate` files
  its batch on the installed lane carrying the declaration (it reads the
  daemon's lane set — its workflows dir, else the installed release's —
  and refuses before filing anything when the set declares no integration
  lane, or two); the daemon lists that lane's batches, shows their
  progress in `pf ops integrate status` and files every fix ticket on its
  `fix_lane`. Nothing finds either lane by its name any more: renaming
  `integrate` and `develop` together with their references changes no
  behaviour.
- **Behavior: a sub-definition takes its step's models.** The agents a
  daemon step dispatches have no candidate key of their own: the
  verify-judge runs on the list composed for the step that dispatched it
  (`verification` on the develop lane) — `pf submit --models`, else
  `steps.<project>.verification.models`, else the step's
  `model_candidates` (develop's `verification` declares none), else
  `steps.<project>.default.models`, else the lane's defaults; the ruling
  agent runs on the list of `collect` or `full-gate`, whichever asked
  (collect now declares full-gate's list, so the models chosen without a
  workspace key are unchanged). The `verify-judge` key of
  `.polyforge.yaml` is retired, and a `full-gate` key no longer governs a
  ruling collect dispatches.
- **Behavior: a stale workspace key warns.** A `steps.<project>.<key>`
  entry that is no step of an installed lane (nor `default` or `task`) —
  `verify-judge`, a removed step's, a typo — no longer refuses the yaml (it
  did, with DAEMON-414, at every claim): it governs nothing, the
  `workspace yaml` row of `pf doctor` warns naming each one with this
  section, and `pf config set` refuses to write one.
- **Behavior: removed outputs.** The verification step's output no longer
  carries `test_bar` or `test_bar_notes` (no step read them; the run's
  `result.json` and `report-mechanical.md` still list the bar), and
  `tools/verify-scope` no longer reads `ci_evidence=`: its JSON report has
  no `ci_evidence` key, and a conf line carrying one is diagnosed `conf
  rejects the removed key ci_evidence` — delete it.
- **Action, before installing: rename the workspace keys.** In every
  workspace `.polyforge.yaml`, rename `steps.<project>.verify-judge` to
  `steps.<project>.verification` (the verification step runs no agent of
  its own, so the list governs the judge alone); if a `full-gate` key was
  meant for the ruling collect asks too, copy it to
  `steps.<project>.collect`. Delete `ci_evidence=` lines from each
  repository's `.polyforge/verify-scope.conf`. The old release loads the
  renamed keys (`verification` and `collect` are step ids there too), so
  the edit can precede the deploy; a judge the old release dispatches in
  between runs on the project default.
- **Action: the drain.** Deploy contract 4 by the drain of the workflow
  contract 1 section above: stop claiming (`pf ops auto off`), let every
  work item in flight finish — an integration batch included — deploy,
  cancel and re-file what parks blocked `contract`, re-bind `WFC-123`
  items, `pf ops auto on`, then run `pf doctor` (the `workspace yaml` row
  names any stale key left). Rewrite every hand-maintained lane to
  `contract: 4` and check it with `pf workflows validate <lane-dir>`.

## Workflow contract 1, publicly frozen: user lanes in a workspace

- **Behavior: the contract is renumbered to 1 and frozen.** The workflow
  contract this release ships is **contract 1**, its publicly frozen form:
  every built-in lane declares `contract: 1`, the daemon implements
  contract 1 alone and refuses every other value (`WFC-101`), and the
  [reference](reference/workflow-contract.md) is its rule. The sections
  above that name contracts 1, 2, 3 and 4 — "Workflow contract 1: one
  strict lane loader", "Workflow contract 2: …", "Workflow contract 3: …"
  and "Workflow contract 4: …" — describe internal stages of one change:
  no release carried any of those numbers, and none of their shapes is
  this contract 1. The released contract 1 is the final shape: their
  behavior together with this section's. A lane written against an
  internal stage number is refused naming this section; rewrite it against
  the reference.
- **Behavior: user lanes.** A workspace may keep lanes of its own under
  `<workspace>/.polyforge/workflows/<lane>/`, in the built-in format. A
  work item's lane is resolved in its lane set: its project → the
  workspace the `[workspace."<path>"]` mapping maps it to → the daemon's
  built-in lanes plus that workspace's user lanes → the lane by name; one
  daemon serving several workspaces resolves each work item in its own
  set. A user lane's step definition is its own `steps/<id>.md`, else the
  built-in `_shared/steps/<id>.md`; fragments come from the built-in
  `_shared/` alone (a workspace has no shared tree); an attr fragment may
  sit in its own `attrs/<name>.md`. Its claim snapshots it beside the
  built-in `_shared/`; the marker records `source: workspace` and the
  content hash.
- **Behavior: new refusals.** A user lane named like a built-in lane is
  refused (`WFC-122`) and never shadows it. A user lane declaring
  `integration`, or running an action that needs the integration binding
  (`collect-landed`, `full-gate`, `mark-green`), is refused (`WFC-172`):
  the built-in lane declaring `integration` is the integration lane. A
  work item whose workspace's lane set lacks its lane is refused at the
  claim and stays queued (`WFC-123`), on every machine whose workspace
  lacks it.
- **Behavior: a refused user lane.** It only makes itself unavailable: at
  the daemon's start and at every change of the workspace mapping each
  refused user lane is recorded once as a daemon-scope `user_lane_refused`
  event naming its defects, and the daemon starts and runs on (a refused
  built-in lane still stops the start). `pf doctor`'s `workflow lanes` row
  warns naming the defects.
- **Behavior: the snapshot is the lane.** The per-dispatch model-candidate
  and attr composition reads the capsule's lane snapshot (the workspace's
  `.polyforge.yaml` is still read fresh at every dispatch): an edit of a
  lane — a release's or a workspace's — reaches the next claim, never a
  work item in flight, through a restart's recovery, a rewind and `pf
  rewind` alike. A dispatch judges the yaml's attr overrides against the
  snapshot, not the installed lanes: one naming an attr the snapshot does
  not declare (or of the wrong type) is ignored with a warning — the
  daemon log and an `attr_override_ignored` event — where it used to block
  the work item (DAEMON-414); a claim still refuses it. `pf resume` of a
  blocked work item is a re-claim and reads the lane as it is now, a user
  lane like a built-in one: it is how a fixed lane reaches that work item.
- **Behavior: validation.** `pf workflows validate` with no lane directory
  judges the workspace's whole lane set (`--workspace`, else the mapped
  workspace holding the working directory; none: the built-in lanes
  alone); a lane directory under a workspace's `.polyforge/workflows/` is
  judged as that workspace's user lane. A `.polyforge.yaml` per-step key
  may name a user lane's step id (it is not stale). `pf workflows --cwd`
  lists the workspace's user lanes (`source`). `pf doctor` no longer
  reports `.polyforge/workflows/` as a stray entry.
- **Action: for the renumber itself, nothing extra.** No release before
  this one carried a workflow contract number, so a machine on a released
  build has no snapshot of an internal number to drain. It still upgrades
  as every machine does: it follows every section after its release, in
  order — the sections above named for contracts 1 to 4 included, each
  with its actions (rename the `verify-judge` key, delete the
  `ci_evidence=` lines, rewrite the hand-maintained lanes, against the
  reference's `contract: 1`) — and deploys them all by one drain, that of
  "Workflow contract 1: one strict lane loader" above.
- **Action: a machine that ran an internal build (contract 1 to 4).**
  Drain it onto this release as one drain: stop claiming (`pf ops auto
  off`), let every work item in flight finish, deploy, then `pf ops auto
  on`. A straggler whose snapshot targets contract 2, 3 or 4 parks blocked
  with kind `contract`, naming this section: cancel it (`pf cancel <wi>`)
  and file it again. A straggler from an internal contract 1 build carries
  this release's number but an older shape: it does not park `contract`,
  its start fails on the loader's defects instead — cancel it and file it
  again too. Re-bind what `pf doctor` or `pf queue` lists under `WFC-123`.
  Rewrite every hand-maintained lane to `contract: 1` against the
  reference and check it with `pf workflows validate <lane-dir>`; a lane
  the workspace itself should carry now moves to
  `<workspace>/.polyforge/workflows/<lane>/` and is checked with `pf
  workflows validate` run in the workspace.

## Hub git calls read a daemon-owned empty config; pf doctor checks /dev/null

- **Behavior: a daemon-owned empty global config.** The daemon's git calls
  that talk to aihub with the session credential — the capsule chain push,
  the capsule clone and the carrier ls-remote/fetch/push, the calls the
  "Hub git calls ignore the operator's git config" section above describes
  — now read their global config from `<data-dir>/git/empty.config`
  instead of the null device: `GIT_CONFIG_GLOBAL` names that file. It is
  created on first use, empty and mode 0444; before each use the daemon
  checks it, and anything but an empty regular file (missing, holding text,
  a symlink, another type) is rebuilt atomically. `GIT_CONFIG_NOSYSTEM=1`
  and the stripping of every inherited `GIT_*` variable are unchanged, so
  the operator's `~/.gitconfig` still does not apply to these calls. That
  section's value for `GIT_CONFIG_GLOBAL` is superseded by this one.
- **Why.** When `/dev/null` stops being the null character device and
  becomes a regular file, every `2>/dev/null` redirect on the machine
  writes text into it, and git reading it as its global config fails with
  `bad config line 1`: every capsule clone failed (DAEMON-516) and every
  claim on the machine blocked. The daemon's own git calls no longer
  depend on it.
- **Behavior: a new `pf doctor` row, `null device`.** It checks that
  `/dev/null` is the platform's null character device (`c 1,3` on Linux,
  `c 3,2` on macOS); it reads only and is out of `--harness`. Otherwise it
  reports `fail` (the exit code moves) naming what the path is instead,
  with the atomic recovery command to run as root: `mknod -m 666
  /dev/null.new c 1 3 && mv -f /dev/null.new /dev/null` on Linux (`c 3 2`
  on macOS). Shells, tools and hooks on the machine still use `/dev/null`;
  this row is the diagnosis for them.
- **Action: none.** No schema change, no config key, no aihub change; the
  upgrade is the `pf` binary alone. The file appears under the data
  directory's `git/` on the first hub git call after the upgrade.

## The daemon files the integration batch itself after a quiet period

- **Behavior: the integrate trigger is on by default.** Every daemon now
  runs the integration lane's automatic trigger beside `pf ops integrate`:
  every 5 s it decides each mapped project once, and when something landed
  (a wrapped work item with `integration.state: landed`) after the
  project's newest batch was filed, no batch of the project is unfinished,
  and the quiet period after the last of those landings passed, it files
  the same batch `pf ops integrate --project <p>` files — same goal,
  priority high, the integration lane — plus the label `integration-auto`,
  and submits it to its own queue (priority high). A landing inside the
  window restarts the quiet period, so the window's landings ride one
  batch. With nothing new it files nothing, so a red, empty, failed or
  canceled batch is never re-opened without a new landing. On upgrade, a
  project with landed items and no open batch gets its first automatic
  batch once the quiet period after its last landing has passed.
- **Behavior: the trigger obeys `pf ops auto off`.** It files and submits
  only while the daemon's auto pull is on — auto off still means only an
  explicit `pf submit` (or `pf ops integrate`) starts work, so step 1 of
  the drain procedure above (`pf ops auto off`) stops it too — and, like
  the auto-pull wave, never while the daemon shuts down, the Cell's
  execution is disabled or degraded, or a connection change is pending.
  A held trigger records the decision `held`, naming the reason, and files
  nothing until the gate opens (`pf ops auto on`).
- **Behavior: one unfinished batch per project, at aihub.** Both triggers
  file the batch `exclusive`: aihub refuses the create `CORE-409` (reason
  `OPEN_ON_BUNDLE`, naming the open work item) while another work item of
  the project on the same lane is not wrapped, failed or canceled — so two
  daemons meeting the condition at once file one batch, and a manual and
  an automatic batch obey the same rule (`pf ops integrate` reports the
  refusal as its "one batch at a time" error).
- **Behavior: settings.** The workspace `.polyforge.yaml` takes an
  optional top-level `integrate:` section, one entry per mapped project:
  `integrate: {<project>: {auto: <bool>, quiet: <duration>}}` — `auto`
  defaults to `true`, `quiet` (a Go duration, `>= 0`) to `30m`; an absent
  section or entry is the defaults. It is strict like the rest of the file
  and read fresh by every trigger pass (no restart).
- **Behavior: observable.** Each change of a project's decision appends
  one daemon-scope `integrate_trigger` event (`{project, decision, detail,
  batch?, landed?, until?}`); `GET /status?integration=1` carries per
  project `trigger: {auto, quiet, decision, detail, at, last?}`; `pf
  doctor` gains the row `integrate trigger` (per project on/off, the quiet
  period, the last automatic batch and its result, the current decision);
  `pf watch <batch> --once` prints `batch:      auto — filed by the
  daemon's integrate trigger` or `batch:      manual — filed by pf ops
  integrate`.
- **Action: to keep integrating by hand only**, in this order: `pf ops
  auto off` (the upgraded trigger holds with it), upgrade the daemon, add
  `integrate: {<project>: {auto: false}}` to the project's workspace
  `.polyforge.yaml` (`pf doctor`'s `integrate trigger` row then shows
  `<project>: off`), then `pf ops auto on`. Never add the `integrate:`
  section before the upgrade: a daemon older than this section refuses
  the unknown top-level key, so the drain skips every project of that
  workspace and its dispatches fail until the upgrade. To batch less
  often, raise `quiet`.
- **Action: rollback.** Before rolling a daemon back past this section,
  remove the `integrate:` section from every workspace `.polyforge.yaml`
  it reads, for the same reason.
- **Action: order.** Upgrade the aihub server (`pf aihub`) before running
  `tools/install.sh` on the machines, as always: the one-open-batch rule
  is the server's `exclusive` create. An older aihub ignores the field and
  files the batch anyway — the daemon's own check that no batch is open
  still holds, only the guard against two machines filing at the same
  instant is lost until aihub is upgraded.

## pf submit refuses an execution ticket without acceptance; `## 验收` counts

- **Behavior: `pf submit` checks the ticket's acceptance.** Submitting a
  work item that has a bundle (an execution ticket) whose content lists no
  criterion — no top-level list item under an `## Acceptance` or `## 验收`
  heading — is refused before the daemon is contacted: `pf: submit: <slug>
  has no acceptance section …`, exit 1, nothing submitted. The message says
  the ticket needs interaction first (settle its acceptance and write it in
  with `pf file edit <id> --content @file`) and names the escape,
  `pf submit <id> --allow-no-acceptance`, the only bypass (no env var or
  config key). With the switch, or with acceptance present, submit is
  unchanged. A registration ticket (no bundle), `pf resume`, auto-pull,
  `pf ops integrate` and `pf file create` are not checked.
- **Behavior: `## 验收` is an acceptance heading.** The driver and the CLI
  share one definition: `## Acceptance` or `## 验收`, case-insensitive, as a
  prefix (`## Acceptance criteria`, `## 验收（…）`). A develop ticket whose
  criteria sit under `## 验收` is no longer treated as having none, so its
  build no longer owes the acceptance stop and `confirm_plan` no longer
  defaults on for it. The plan's own acceptance section reads both headings
  too; the hash of an existing `## Acceptance` section is unchanged.
- **Why.** Tickets written with `## 验收` were judged to have no acceptance,
  and their build implemented everything first and only asked the owner to
  confirm the plan at completion. A ticket
  carries its acceptance before it runs; filing still may record a bare
  idea, and the routing skill grills the acceptance in the session before
  submitting.
- **Action: none beyond the `pf` binary.** No schema change, no config key,
  no aihub change. A script that submits execution tickets without
  acceptance adds `--allow-no-acceptance`.

## develop asks only on surprises; `confirm_plan` fires right after the plan; the approval grammar is gone (workflow contract v2, PR 1)

- **Behavior: `build` no longer grills the ticket.** The develop lane's build
  step declares `owner-stops: {stops: [acceptance]}` — no `grill` stop. Its
  prompt has no `## Clarity` checklist and no batched `build-grill` round:
  the requirement is grilled in the session that files the ticket
  (`pf submit` refuses an execution ticket without an acceptance section,
). `build` asks the owner only on a surprise, right after the
  plan and never at completion: (a) the code reveals something the ticket
  did not decide — a dead end, a contradiction (`build-dead-end`); (b) the
  plan diverges from the ticket (`build-deviation`); (c) the ticket says
  `confirm_plan: true` (`confirm-plan`, right after `plan.md` is written and
  before the first test); (d) the ticket has no acceptance section, or an
  incomplete list (asked once with the plan, the proposed list in the
  question — `confirm-plan`, or `build-acceptance` when the ticket turns
  `confirm_plan` off). Its `needs_input` reasons are `dead_end`,
  `deviation`, `confirm_plan` and `acceptance_missing`; `grill` is gone.
- **Behavior: the owner's answer is free text; the driver only checks that
  one exists.** The approval grammar is deleted: an answer to `confirm-plan`
  or `build-acceptance` no longer has to be a bare approval word — "按你的建
  议来", "ok but drop AC3" or a list all count as answered, and the build
  reads them. The answer's write records the words only: the archived
  answer's `acceptance` snapshot (with its `section_hash`) and `grill`
  snapshot are no longer written or read (a capsule holding them still
  loads; the fields are ignored). The judged acceptance list of a ticket
  without an acceptance section is the plan's `## Acceptance` as it stands
  when the build completes — the list the owner replied to; the "changed
  after the owner approved it" refusal went with the hash. `build` writes
  each decision it asked for — the owner's words verbatim and how it read
  them — into its `summary`.
- **Behavior: review's verdict rule 2.** A deviation from the ticket, or an
  acceptance list `build` wrote itself, needs an owner reply on record
  (`steps.build.answers`), and `build`'s reading in its summary must be
  consistent with the owner's raw words; otherwise a blocking `guarantee`
  finding. The `## Clarity` `clear`-row rule went with the grill.
- **Unchanged.** The workflow contract stays 1: the check registry keeps
  `owner-stops` with `stops ⊆ {grill, acceptance}` and its three question
  ids. A user lane that declares `grill` keeps its `## Clarity` judgement;
  an `ask` row then owes a recorded `build-grill` answer (any words, no
  snapshot). `## 验收` still counts as the acceptance heading.
- **Why.** The product model (owner ruling 2026-11-10): the main session
  grills the requirement and files a ticket that carries its decisions and
  acceptance; the daemon runs it through the lane, and the build asks only
  what the code reveals. Tickets with explicit acceptance built straight
  through in 14–44 minutes; a build that grilled spent 41 minutes before
  its first question. A daemon-side answer grammar contradicts "the
  owner's words are free text, read by the asking agent", so it goes
  rather than grows.
- **Action: none beyond the `pf` binary and its bundled workflows.** No
  schema change, no config key, no aihub change. A user lane that copied
  develop's `stops: [grill, acceptance]` keeps working; drop `grill` to
  stop grilling. An in-flight develop ticket runs on the lane snapshot it
  was claimed with (its grill stop still checked, answers by existence);
  a `confirm-plan` answer already recorded counts.

## Step commands `pf-run` and `pf-verify` (workflow contract v2, change 2)

- **Behavior: two pre-written step commands ship with pf.** `pf-run` runs
  command lines in a task repo's verification container — `pf-run --repo
  <name> -- '<line>'` for one line, `pf-run --plan-from <file>` for a plan
  (a JSON array `[{"repo": "<name>", "line": "<line>"}]`, every entry in
  order, stopping at the first failure) — and `pf-verify --tier light` runs
  the light verification of the capsule's task repos at their HEAD: no
  fetch, no merge (the sync step's tree is what is verified; a merge in
  progress is not judged), the base and the baseline being `merge-base HEAD
  origin/<default>`. Both take `--json --out <path>` and write one result
  file atomically, the same JSON on stdout: schema `pf-step-result/1` with
  `command`, `version`, `argv`, `round`, `started_at`, `ended_at`,
  `exit_code`, `verdict`, `heads` (each repo's HEAD), `summary`, `details`
  (`runs[]` for pf-run, `checks[]` for pf-verify) and `logs`. The verdict
  follows the exit code and nothing else: 0 `pass`, 1 `fail`, 2 `error`
  (not judged — the infrastructure, an unusable invocation), 3 `partial`.
  Their containers carry the daemon's owner and boot labels, a
  `pf.verify.expires` deadline and a bounded keep-alive, so the daemon's
  next start, or the next command's start, reclaims one a killed step left.
  The daemon's own actions are unchanged and no lane runs the commands yet.
- **Behavior: installed as symlinks; a doctor row.** `tools/install.sh`
  links `pf-run` and `pf-verify` beside `pf` (`pf-run -> pf`, relative,
  ensured on every run); the binary dispatches on its argv[0]. `pf doctor`
  gains the row `step commands`: each name resolves, on the daemon's
  command PATH when the daemon answers (else this shell's), to this very
  `pf`; `warn` at most, never `fail`.
- **Behavior: the step environment they read.** `PF_ROUND` is copied into
  the result's `round`; `PF_COMMAND_PATH` is the PATH their subcommands —
  git, the docker client, the scope engine, a bar that runs on the host —
  run under (the daemon's effective `[daemon] command_path`; unset, the
  process's own PATH); the deadline is derived two minutes under the
  tighter of `PF_TOOL_WINDOW_MS` and `BASH_MAX_TIMEOUT_MS` (`--deadline`
  overrides); `PF_VERIFY_DAEMON` / `PF_VERIFY_BOOT` label the containers,
  `PF_WI_ID` the work item. The daemon injects them into every step
  process (docs/reference/config.md, the step environment); outside a
  step each is unset and a command runs with no round, the process's own
  PATH, the owner label `pf-step` and a fresh boot. They connect to no
  daemon and no aihub.
- **Action: re-run `tools/install.sh` on each machine** — it creates the two
  links and changes nothing else. A machine that installs `pf` by hand
  links them itself (`ln -s pf /usr/local/bin/pf-run`, likewise
  `pf-verify`). Rollback: remove the two links; nothing reads them yet.

## Step commands `pf-sync`, `pf-merge` and `pf-revert` (workflow contract v2, change 3)

- **Behavior: three more pre-written step commands ship with pf.**
  `pf-sync` merges origin's default branch into every task repo of the
  capsule in place (`gitmerge.Sync`: a fast-forward for a branch with no
  commit of its own, the default branch's `.polyforge/integrate.conf`
  conflicts resolved mechanically and committed); a real conflict is left
  in progress with its files listed (exit 1), the agent resolves it in
  place and runs `pf-sync --continue`, which refuses a residual marker, an
  unmerged or unstaged path and a changed mechanical resolution, commits
  the merges and rewrites the same result file (`conflicts` emptied,
  `resolved` filled, `after` the merge commit). Its record `syncing.json`
  beside the result makes a crash re-entrant (a leftover merge aborted, a
  HEAD that is neither `before` nor a recorded merge reset to `before`
  with the commit kept under `refs/polyforge/discarded/pf-sync/<n>`, the
  merge made again). `pf-merge --publish <publish.json> --verified
  <result.json>` merges the published PRs with `gh pr merge --merge
  --match-head-commit <verified head>`, every repo judged first, and takes
  a PR merged already by the landing rules (its branch side the verified
  head: `merged`; merged outside the lane with a merge commit on the
  default branch containing the verified head: `merged_elsewhere`); it
  updates no PR branch — a forge refusal is the sync step's again.
  `pf-revert --batch <batch.json> [--item <wi>]...` reverts an item and its
  landed fix tickets per repo through one revert PR each
  (`gitmerge.Revert`, newest merge first), resumable from `revert-plan.json`
  beside the result, and writes the pins after the reverts (`heads`) and
  the repos to gate (`gated`); without `--item` it copies the pins. All
  three write the `pf-step-result/1` file (`--json --out`), the verdict
  from the exit code alone: 0 `pass`, 1 `fail`, 2 `error`. The landing
  rules, the revert branch name and the newest-first ordering moved from
  the driver into `internal/polyforge/gitmerge` (`TakeAsLanding`,
  `LandingRule`, `PR.Mismatch`, `RevertBranch`, `NewestFirst`); the
  daemon's own sync, merge and revert actions call them and behave as
  before. No lane runs the new commands yet.
- **Behavior: installed as symlinks; the doctor row covers them.**
  `tools/install.sh` links `pf-sync`, `pf-merge` and `pf-revert` beside
  `pf` like `pf-run` and `pf-verify`; the `pf doctor` row `step commands`
  checks all five.
- **Action: re-run `tools/install.sh` on each machine** — it creates the
  three links and changes nothing else. A machine that installs `pf` by
  hand links them itself (`ln -s pf /usr/local/bin/pf-sync`, likewise
  `pf-merge` and `pf-revert`). Rollback: remove the three links; nothing
  reads them yet.

## The verification container's default cpu limit is capped at the host's cores

- **Behavior: the built-in `--cpus` default follows the host.** When
  `[daemon] verify_container_cpus` is unset, a verification container —
  the daemon's own run, a test gate, the step commands `pf-run`,
  `pf-verify` and `pf-facts --replay` — gets 8 capped at the host's core
  count instead of a flat 8: on a 2-core host the default is 2, on an
  8-core or larger host it stays 8. `pf config` shows the default this
  host gets. A configured `verify_container_cpus` (or a `pf-gate --cpus`
  flag) is passed as written and is not clamped: a value above the host's
  cores still fails the run as before — docker refuses it with `range of
  CPUs is from 0.01 to N` (exit 125). The memory and pids defaults (`8g`,
  4096) do not move.
- **Why.** Docker refuses a `--cpus` above the host's cores, so on a host
  with fewer than 8 cores no verification container could start by
  default: every daemon-run bar, test gate and step command there failed
  before one command ran. The repository's own full bar on a 2-core CI
  runner failed this way at the step-command container test (`docker
  run: exit status 125`) on every run since the step commands landed;
  the container tests of the verification package passed only because
  they set their own limit of 1.
- **Behavior: a green tag's plan digest carries the limit as executed.**
  A test gate's plan records the container's effective cpu limit, so on
  a host under 8 cores the digest of a commit's plan now differs from
  the one an 8-core host computes; a green tag written there is reused
  only where the effective limit is the same, as a configured limit
  already behaves.
- **Action: none.** No schema change, no config key, no aihub change; the
  upgrade is the `pf` binary (with the `pf-gate` binary a release's test
  gate builds from the same tree). A host under 8 cores that set
  `verify_container_cpus` only to get past the refusal may drop the key.
  Rollback: the previous binary, with the flat default again.

## Auto-pull skips an execution ticket without acceptance; pf queue and Pi Web show 需要交互：缺验收; fix tickets carry the red check's criterion

- **Behavior: the daemon's auto-pull applies `pf submit`'s acceptance
  check.** The auto-pull wave no longer claims a queued execution ticket
  (one with a bundle) whose content lists no criterion — no top-level list
  item under an `## Acceptance` or `## 验收` heading. It is the same
  predicate `pf submit` applies (one shared definition), so
  the #460 section's "auto-pull … not checked" no longer holds. Only
  presence is checked, never what a criterion says, and no lane is exempt.
  Registration tickets (no bundle) are unaffected. The skip writes no log
  line, event or state.
- **Behavior: the queue shows why such a ticket waits.** In `pf queue`'s
  table the STATUS cell of a queued execution ticket without acceptance
  reads `queued (需要交互：缺验收)`; the merged `pf --json queue` document
  carries `"acceptance_missing": true` on that item only (the `--project P
  --json` body stays the server's raw body). Pi Web's 工单列表 shows the
  label 「需要交互：缺验收」 beside the row's status; an older `pf` sends no
  key and Pi Web shows no label.
- **Behavior: an integration fix ticket carries the red check's
  criterion.** When an integration batch files a fix ticket, the daemon
  appends to the writer's `## Acceptance` one criterion per red unit the
  ticket names — that unit passes on the fixed head, marked `— carried by
  build` — so every fix ticket lists a criterion and the auto-pull still
  claims it. The writer's tickets must each name at least one red unit.
- **Why.** `pf submit` was the only entry that checked acceptance; the
  auto-pull claimed queued tickets without it, and the integrate lane's
  fix tickets (run by the owner or the auto-pull, never submitted) relied
  on that path. A ticket carries its acceptance before it runs, whichever
  way it starts.
- **Action: none beyond the binaries.** No config key, no schema or aihub
  change. A queued execution ticket without acceptance stays queued until
  its acceptance is written in with `pf file edit <id> --content @file`
  (the auto-pull then claims it), or it is submitted by hand (`pf submit`,
  which asks for the same acceptance or `--allow-no-acceptance`).
## `pf ask` and the awaiting-owner state (workflow contract v2, change 6c)

- **Behavior: a step asks the owner with `pf ask`.** A step agent that
  needs the owner — a decision, a missing fact, a confirmation — runs
  `pf ask "<question>"` and ends its turn. The command is local (no
  daemon, no aihub, no network, no config; a new step-reachable entry
  point) and appends one fsynced line `{at, step, spawn, kind: "ask",
  text}` to the step's own capsule record `runtime-state/asks.jsonl`,
  keyed by its own `at`. Every agent step's delivery contract now carries
  that instruction; the prohibited-actions list is unchanged. The daemon
  injects `PF_STEP` and `PF_SPAWN` (the dispatch's step id and spawn
  ordinal) into every step process beside `PF_ROUND`; `pf ask` reads
  them. The daemon watches the record (at most 2 s late) and re-reads it
  at a turn end.
- **Behavior: an open ask parks the step awaiting the owner.** The
  `awaiting_owner` event carries the question verbatim (`step`,
  `spawn_n`, `ask_at`, `question_id` = `ask_at`, `question`; `resumed:
  true` when a restarted daemon finds the ask still open); the decision
  feed records it under the class `needs_input` (the attention channel
  Pi Web's orange button and card read), the watch snapshot and `pf
  watch <wi> --once` show it as the step's pending question under the id
  `<at>`, and the status face's QUESTION column shows that id. While the
  step waits its job holds no running slot (another queued job may
  start; the driver takes the slot back before its next dispatch), and
  nothing of the dispatch is judged: silence, the tool window and T are
  not counted while the owner is asked, and the wall booked into T ends
  where the wait began. The step process stays resident for the warm
  window — `[daemon] ask_warm_window`, flag `--ask-warm-window`, default
  `15m`, restart-only — then the daemon ends it (stdin closed, then the
  signal) with the step still awaiting on the same dispatch; a process
  that exits by itself while waiting is treated the same (no death, no
  retry). A daemon restart parks the step again on the open ask without
  an interruption archive and without a new dispatch. A turn that ends
  with neither a result nor an open ask is still the ended_without_result
  rule (a retry), so an accidental stop is never read as a question.
- **Behavior: contract 1 unchanged.** A v1 `needs_input` envelope keeps
  parking the step as before and is also recorded as one ask line (with
  its `question_id`); its `pf answer` is recorded as the reply line
  `{at, kind: "reply", ask_at, text, by}` before the answer is applied.
  The owner's reply to a `pf ask` reaches the step's session through the
  owner-message relay of the next change; until it is installed, `pf
  answer` on a step waiting on a `pf ask` refuses naming the open ask.
- **Action: re-run `tools/install.sh` on each machine** (the `pf`
  binary; no new link, no schema change, no aihub change). Optional:
  `ask_warm_window = "<duration>"` in `cli.toml`'s `[daemon]` table
  (restart-only). Lanes need no change; the shipped lanes still ask with
  `needs_input` until they are migrated. Rollback: a step waiting on a
  `pf ask` under the previous binary is an in_progress step with no
  pending question — the previous daemon re-dispatches it (the record
  stays in the capsule, read by nothing).
## Owner messages relayed into the step (workflow contract v2, change 6d)

- **Behavior: `pf answer <wi> --text "<words>"` works at any time, no
  question id.** While a step's agent runs or waits on its `pf ask`, the
  words go to that agent. The daemon appends one fsynced line to the
  step's capsule record `runtime-state/asks.jsonl` first — `{at, step,
  spawn, kind: "reply", ask_at, text, by}` while the current dispatch's
  ask is open (the reply closes it: the step is no longer waiting),
  `kind: "message"` otherwise (an interjection) — emits the
  `owner_message` event (`at`, `step`, `spawn_n`, `kind`, `ask_at`, `by`,
  `text`), then delivers. The daemon reads none of the words: no option,
  no reserved word (the reserved `cancel` belongs to a v1 question).
- **Behavior: delivery.** The step process alive: the message goes into
  it through its harness's own channel (claude: a stream-json user
  message; pi: a `prompt` with `steer`), framed `Owner reply to your
  question (ask <ask_at>) from <by>:` / `Owner message from <by>:`, the
  words verbatim, then the marker `[pf-msg:<at>]` on its own line (the
  first line never starts with `/`). Idle, it opens the agent's next turn;
  mid-turn, the harness queues it and hands it to the model at the turn's
  next boundary. A step awaiting the owner is running again from the
  acceptance: the warm window stops, the running slot is taken back,
  supervision judges again and the whole wait is an observation gap
  (neither silence nor T counts it). The process gone (the warm window,
  a crash, a restart): the daemon resumes the same session (claude
  `--resume`, pi `--session`) as a new dispatch — a new `spawn_n`, which
  the result envelope must carry — whose first message is the undelivered
  owner messages (alone when the conversation ended awaiting the owner,
  as a `# Owner messages` section of the full prompt otherwise).
- **Behavior: delivered means observed.** A message is marked delivered
  only once its marker is seen in the harness's stream (claude: the
  `started` lifecycle of the message; pi: the user message carrying the
  marker; a resumed session: its first turn starting), never on a
  "queued" acknowledgement: then the line `{at, step, spawn, kind:
  "delivered", msg_at}` and the `delivered` event (`msg_at`, `step`,
  `spawn_n`, `kind`, `disposition`, `confirmed_by`). A daemon restart
  replays exactly the reply and message lines no delivered line names,
  into the step's next dispatch.
- **Behavior: `pf answer`'s other shapes.** The previous change's refusal
  of `pf answer` on a step waiting on a `pf ask` is gone; the open ask's
  own stamp as `--question` names the relay too; `--option` with no v1
  question pending is refused `DAEMON-400`; a disabled Cell refuses the
  relay `DAEMON-409`; no step in progress, a terminal or blocked workflow
  answer `DAEMON-409`. A v1 `needs_input` question is answered exactly as
  before (`--question`, `--option`, the reserved `cancel`). The command
  prints what was recorded and how far the delivery got, and waits at most
  3 s for the proof; `--json` answers the daemon's ack.
- **Behavior: the faces.** `pf watch <wi> --once` lists `owner_message`
  and `delivered` among the events; the decision feed's `awaiting_owner`
  entry resolves when the step's result is accepted (`step_done`,
  `gate_verdict`, `incomplete`) or a new ask supersedes it — never on the
  owner's message: the daemon does not judge whether the words answered.
- **Action: re-run `tools/install.sh` on each machine** (the `pf`
  binary; no new link, no config key, no schema change, no aihub change).
  Lanes need no change. Rollback: the previous binary refuses `pf answer`
  on an open ask again, and the record's reply, message and delivered
  lines stay in the capsule, read by nothing.

## Workflow contract 2 (additive): the agent step's v2 keys, intents and effects, owner messages, pf waive, the rewind budget asks the owner (workflow contract v2, change 8)

- **Behavior: contract 2 is additive.** Every contract-1 key keeps its
  meaning and the agent step gains the v2 keys: `skill` (a markdown file
  of the lane snapshot, `<lane>/skills/<name>.md` else
  `_shared/skills/<name>.md`, rendered as the prompt's `# Skill` section;
  `WFC-200`), `verifies` (what the step verifies of its upstream before it
  acts, rendered as `# Verify first`; `WFC-204`), `resources`
  (`{admission: none|heavy, locks: [bar|merge]}`, taken by the daemon for
  each dispatch), `budget.tool_minutes` (the step's own tool window, ≤ 180,
  ≤ `wall_minutes`), `model_tier` with the lane's `defaults.tiers` (the
  tier→model table; `WFC-198` for a missing tier), `effects` (the aihub
  writes the envelope may declare, the integration lane's only), `handoff`
  (`landing` | `integration_batch`, the output field copied verbatim onto
  the record), `waivable` (bool, default false), the `pool: landed` input
  (the integration pool the daemon writes into
  `artifacts/integrate/pool.json` at the dispatch) and an artifact's
  `format: json` (parsed when the step completes). `handoff` is no longer
  a removed key. The envelope gains `intent` (`{kind, reply_at, to?,
  reason}`: `cancel` / `retry` / `rewind` with `outcome: intent`; `waive` /
  `one_more_round` with `gate_failed`) and `effects`; a new record `waived`
  (`{round, reply_at, words, reason}`) and a new blocked kind
  `effect_failed`. The error codes `WFC-200`, `WFC-204` and `WFC-205` are
  new; `WFC-172`, `WFC-180`, `WFC-182` and `WFC-198` widen as the
  reference page states. Every shipped lane declares `contract: 2` and
  the placeholder tiers `cheap` and `mid` in its `defaults.tiers`, and
  changes nothing else, except that the step-level placeholder candidate
  lists (`pi:example-…`) are gone from develop, integrate and release
  (; integrate keeps the list as its lane defaults) — a
  bundle step list that is placeholders only is undeclared and never
  outranks the project default, and `pf doctor`'s workspace yaml row
  warns when a resolved step's first candidate is a placeholder (a daemon
  step whose action starts no agent is skipped). The envelope's effects
  ride a completed result or a gate's FAIL verdict (recorded pending in
  that write) and are refused on any other outcome; a landing hand-off
  that does not decode as the landing record is refused at the envelope.
  A step declaring `resources` is not kept warm while it awaits the
  owner: its idle process ends at the `pf ask`, its admission and locks
  are released, and the resume that carries the owner's message takes
  them again first.
- **Behavior: a spent rewind budget asks the owner through the gate's
  agent, on every lane (O7).** The gate dispatch's `# Rewind budget`
  section tells the agent to `pf ask` and deliver the owner's decision as
  an intent; the daemon no longer refuses a FAIL back to the agent and
  offers no `waive` / `one-more-round` / `manual` option actions. A bare
  FAIL past the budget — an agent gate's, a daemon action's, the verify
  action's mechanical one — parks the work item blocked, kind
  `rewind_budget`, for `pf resume` (exactly one more round, charged to
  nothing), `pf waive` or `pf cancel`; `manual` is gone (cancel ends a
  work item). `on_fail.charge: unless_owner_answered` now reads the
  daemon's owner-message record (`runtime-state/asks.jsonl`: the reply
  and message lines whose own `step` is the gate — the relay's — or a
  mirrored v1 answer to the gate's ask, in the gate's current round; an
  intent retry stays in the round) instead of the archived v1 answers.
- **Behavior: `waivable` defaults to false.** Only an agent gate declaring
  `waivable: true` takes `intent: waive` and `pf waive`; a daemon action's
  waivability stays the registry's (`verify`). No shipped lane declares
  it yet (the lane migrations do).
- **Behavior: `pf waive <wi> --step <gate> --reason <text>`.** A new
  execution-plane verb and daemon route (`POST /jobs/{wi}/waive`, refused
  `DAEMON-403` from a step or task process tree): on a work item parked
  `rewind_budget` on a waivable gate it records the waiver in the capsule
  and takes `pf resume`'s blocked door; the gate is accepted on the
  waiver (its `waived` record, the delivery waiver the landing record
  carries), no round granted. Refused typed otherwise (`WAIVE_NOT_PARKED`,
  `WAIVE_NOT_WAIVABLE`, `WAIVE_STEP`, `WAIVE_REASON`). The recorded waiver
  is bound to its park (the gate and its round) and removed after the
  write that applies it; when aihub's door refuses the resume the verb
  withdraws it (`{step, withdraw: true}` on the same route), so a later
  plain `pf resume` grants one more round.
- **Behavior: `<id>.answers` names the owner messages.** The input's
  location is `runtime-state/asks.jsonl` (the step's asks and the owner's
  replies and messages, verbatim and stamped); it is present when the
  record carries one. Every delivery contract states the intents, the
  effects a step declares and the answer-readings duty (state your reading
  of an owner message in `intent.reason` or your output). A `pf bar-run`
  line and a run request now run under the step's declared tool window
  when its lane declares one. `pf-facts build --replay` treats a replay
  exiting 126 or 127 as not judged (never red).
- **Behavior: the workspace yaml's `tiers:` section.** A new optional
  top-level section, `tiers.<project>.<tier>.models` (`models` the only
  key of a tier entry): the project's tier→model table, the list of every
  bundle step naming that `model_tier`. It outranks the lane's
  `defaults.tiers`; a step's own `steps.<project>.<step-id>.models`
  outranks it, and the project default ranks below both. A file using it
  is refused by the previous binary (an unknown top-level key).
- **Why.** Workflow v2 moves the daemon's judgements into agent steps
  that run pre-written commands (design §2, §5.3, §6.1): the keys land in
  one bump so the lane migrations (changes 9–13) change lane files only;
  the owner's words stay free text read by the agent, the daemon applying
  what the agent declares.
- **Action: drain, then deploy.** Stop claiming (`pf ops auto off`), let
  the work items in flight finish, deploy, re-run `tools/install.sh` on
  each machine (the `pf` binary; no schema change, no aihub change); a
  work item whose capsule snapshot targets contract 1 parks blocked, kind
  `contract` — cancel it and file it anew. A user lane changes
  `contract: 1` to `contract: 2` (every v1 shape still loads; `WFC-101`
  until it does). The polyforge skill gains the `waive` verb
  (`skills/polyforge/verbs/waive.md`; re-run `/plugin marketplace update`
  where the skill is installed). Rollback: the previous binary with
  `contract: 1` lanes; a capsule claimed under contract 2 parks blocked,
  kind `contract`, under it.

## The release lane runs release.sh and deploy.sh itself under heavy admission and a 120-minute tool window (workflow contract v2, change 9)

- **Behavior: the release lane's agents run the procedures.** `release`
  runs `.polyforge/release.sh [<bump>]`, `verify` runs `release.sh
  --verify <version>` and `deploy` runs `.polyforge/deploy.sh <version>
  --confirm <version>` themselves — in the owner repo, inside one tool
  call, under the daemon's command PATH (`PATH="$PF_COMMAND_PATH"`: the
  `[daemon] command_path` the daemon's own commands run under, its own
  PATH when unset), the output teed into `artifacts/release.log` /
  `artifacts/deploy.log`. The lane declares no `evidence` any more:
  nothing goes through `pf bar-run` or a run request (`pf bar-run` is
  refused `DAEMON-403` from its steps, as from every step that is not
  notarized), its envelopes carry no `command_evidence`, and no bar-run
  record of a release work item is written. The two procedures stay the
  only entry points and their contract is unchanged.
- **Behavior: the release step holds the heavy-run admission.** `release`
  and `deploy` declare `resources: { admission: heavy }`: the dispatch
  takes a slot of the heavy-run admission — the queue the daemon's
  verification runs wait in (`[daemon] max_concurrent_bars`, default 2)
  — before its process starts and gives it back when the process is gone.
  A release therefore waits for running verifications (the work item's
  `verify_queued` event, "heavy-run slots full"; the status face shows the
  queue) and no verification starts beside it while the procedure runs.
  The slot is held for the whole dispatch, model time included; the lane
  takes no bar or merge lock.
- **Behavior: the lane's own tool windows.** `release` declares
  `budget: { wall_minutes: 135, tool_minutes: 120 }`: its dispatches run
  under a 120-minute tool window (`PF_TOOL_WINDOW_MS`; a claude step's
  Bash default and max timeout equal it), sized for a procedure that runs
  the repository's full test gate itself, and T = 3 × 135 = 405 minutes
  across its dispatches (was 135). `deploy` declares `tool_minutes: 30`,
  `verify` `wall_minutes: 30`. The daemon's global `--tool-window` no
  longer bounds the release or deploy procedure.
- **Behavior: no gate of the lane is waivable.** The three gates declare
  `waivable: false` (the contract-2 default, now stated): a FAIL ends the
  work item, `intent: waive` is refused and `pf waive` answers
  `WAIVE_NOT_WAIVABLE`. A step that needs the owner asks with `pf ask`
  and ends its turn; an owner answer that calls the release off is
  delivered as `intent: cancel`.
- **Behavior: downstream verifies upstream; model tiers.** `verify`
  declares `verifies` on `artifacts/release-record.json` (it parses and
  names the version and its commit) and `deploy` on
  `artifacts/verified-version.json` (its version equals the one `release`
  reported — `release.output.version`, now a declared input of
  `deploy`). The duties render as the prompt's `# Verify first` section;
  one that does not hold is the gate's FAIL, which ends the lane (the
  daemon checks nothing). `release` and `deploy` name `model_tier: mid`,
  so their models come from `.polyforge.yaml`: the step's own
  `steps.<project>.<step-id>.models`, then `tiers.<project>.mid.models`,
  else the project default (the lane's placeholder tiers never outrank
  it). `verify` names no tier: it stays on its step key or the project
  default (the strong model), whatever `tiers:` a workspace adds.
- **Why.** Workflow v2 moves every judgement and every long run into the
  step's own agent (design §4.4, ruling O11): the daemon orchestrates —
  admission, windows, records — and runs no line for a step. The lane
  keeps the admission and the command PATH the run request gave it, and
  gains a window its procedure fits in.
- **Action: re-run `tools/install.sh` on each machine** (the `pf` binary
  with the lane snapshot; no new link, no schema change, no aihub change).
  Nothing to configure: the lane's windows and admission are its own, and
  `[daemon] command_path` and `max_concurrent_bars` keep their meaning. A
  release work item claimed under the previous snapshot finishes on it
  (its steps still request their runs). Rollback: the previous binary
  with its lane; a capsule claimed under this snapshot runs the procedures
  itself on it too — the keys it declares are contract-2 keys the previous
  binary already reads.

## Pi Web's job card is a chat-style session with one input box; the discussion assistant is gone; a no-agent park shows a menu (workflow contract v2, change 14)

- **Behavior: the job card is the step's conversation.** The job detail's
  execution progress now reads as the main session does: the agent's
  narration and tool cards as before, its `pf ask` question as a
  highlighted block where it was asked (the event log's `awaiting_owner`
  line), the owner's messages as user bubbles with their sender and time
  (`owner_message`), and `delivered`, `intent` and `blocked` (an
  `effect_failed` park among them) as status rows. Under the scroll region
  sits the card's bottom: the running sub-agent list (the main session's
  component over the step's running subagents; a chip reveals that
  subagent's entry), the question the step waits on, highlighted and
  verbatim (the watch snapshot's `pending_question`: a contract-1 question
  by its id, or the current dispatch's open ask, whose id is its stamp),
  and ONE input box. Its words go to the step's own agent through
  `POST /api/polyforge/answer` → `pf --json answer <wi> --text=<words>` —
  no question id: the daemon tells a reply to an open ask from an
  interjection into a running turn (change 6d), and answers a lone
  contract-1 question itself. The box is enabled while the step runs AND
  while it waits on the owner (a reply and a live interjection are one
  path); Enter sends per the composer's send-key setting. An accepted
  message is echoed into the flow at once with how far its delivery got
  (delivered / queued / carried by the next dispatch); the event log's own
  line — matched by the message record's own stamp — then replaces the
  echo. A refusal keeps the words in the box with pf's text beside it. A
  restarted daemon's re-announcement of a still-open ask shows no second
  question.
- **Behavior: the no-agent menu.** When no step agent can be talked to the
  box gives way to the reason and a minimal menu of actions, never answers:
  a paused job offers 继续 (`pf resume`) and 取消 (`pf cancel`, confirmed
  inline, new `POST /api/polyforge/cancel`); the failure-counter park of a
  step that ran out of model candidates in its current run, and a block the
  runtime made outside any workflow park (the capsule clone failed at the
  claim; a run that completed but whose final push failed), offer 继续 and
  取消; every other
  park (the recovery budget, the rewind budget, incomplete, an effect
  failure, …) offers 继续, 取消 and 打回到… (`pf rewind --to`); the
  contract-mismatch park offers 取消 only; a queued job, an ended work item
  and an unreachable daemon show the reason alone, as do the runtime's
  transient states (claiming, recovering, sealing). A landed send-back stays
  in the flow as a status row. With an agent to talk to
  the input box is the only channel — no 继续, 取消 or 打回 control anywhere:
  the owner says "cancel" or "send it back" in it and the agent delivers
  the intent (change 8). The work item's panel (the dock's bottom-right
  button) becomes its record: its blocked cause with the copyable `pf
  resume` / `pf cancel` commands and its decision entries; its 继续 buttons
  and its 打回重做 form moved into this menu.
- **Behavior: the discussion assistant is deleted.** The needs-input chat's
  read-only AI session (`PolyforgeQuestionPanel`, the
  forked/assembled discussion, 「填入答案草稿」, `GET`/`POST
  /api/polyforge/discussion`, `POST /api/polyforge/discussion/answer`, the
  `polyforge-discussions/` working directory under the pi agent dir, the
  startup sweep, the capsule's `session/discussion/` record) and the
 option buttons (`PolyforgeQuestionOptions`) are gone. A
  contract-1 authorization request's options are listed as text under the
  question; the owner selects one only explicitly — the whole message is
  the option id, or `/<id> <words>` — and it goes out as `--option`; any
  other text goes to the asking agent as words (transitional until
  contract 3 deletes `options`). The watch route no longer projects
  `asked_by`.
- **Behavior: the faces.** The watch route projects `pendingQuestion`
  `{id, step?, question, askedAt?}` and admits `awaiting_owner` (step,
  question, question_id), `owner_message` (step, by), `delivered` (step,
  disposition) and `intent` (step, its kind as `intent`, to) to the event
  tail; the owner's words and an intent's reason ride the progress stream
  only. The Polyforge button and the job card stay amber while a step waits
  on the owner (the status face reads `needs_input` for an open ask as for a
  contract-1 question).
- **Action: re-run `tools/install.sh` on each machine** (Pi Web; no new
  config key, no schema change, no daemon or aihub change). A Pi Web
  started before the upgrade keeps serving the old card until restarted.
  Rollback: the previous Pi Web shows the needs-input chat again; a
  discussion it finds no working files for starts afresh; nothing this
  change wrote is read by it.

## The integrate lane's four agent steps: collect, revert, full-gate and close under skills; effects and the integration_batch hand-off (workflow contract v2, change 13)

- **Behavior: the integrate lane is four agent steps.** `collect` →
  `revert` → `full-gate` → `close`, each an agent step under a skill
  (`workflows/integrate/skills/<step>.md`) that runs one pre-written
  command and routes on its exit code: `pf-integrate collect` over the
  pool the daemon writes at the dispatch (`pool: landed`), `pf-revert`
  (the one step holding the task repos' merge locks), `pf-gate --capsule
  --pins` with `--mark-green` and, on a red gate, `pf-gate --triage`
  (heavy admission, the lane's one gate: `on_fail` to `revert`, charged
  to nothing after the owner's message, `max_rewinds: 3`), and
  `pf-integrate close`. The daemon's `collect-landed`, `full-gate` and
  `mark-green` actions stay registered but no shipped lane runs them; the
  lane's `ruling` and `fix-tickets` sub-definitions are gone from the
  shipped tree (no integrate question carries `options` any more: the
  owner answers a step's `pf ask` in words, `pf answer <wi> --text`).
- **Behavior: the aihub writes are declared effects.** A hold the owner
  clears or files a new fix for (collect: `hold_clears`, `fix_tickets` and
  the `attributions` registering the new ticket on the held item), a reverted
  item's record (revert: `integration_moves` to `reverted`), the fix
  tickets and the blamed sources' rounds (full-gate: `fix_tickets`,
  `attributions`) and the items a green tag covers (close:
  `integration_moves` to `merged`) are `effects` in the step's envelope,
  applied by the daemon through the capsule's effects ledger
  (`runtime-state/effects.json`); a fix ticket's acceptance is non-empty
  and names each red unit that passes on the fixed head. The batch's
  record is `close`'s `integration_batch` hand-off, copied verbatim onto
  the execution record and into the work item's attrs: the same shape as
  before (`result`, `main`, `tags`, `items`, `green_items`, `fix_tickets`,
  `hold_rulings`, `refused`, `empty`, `closed_at`) plus `reverts`
  (`[{wi, name, prs}]`, the reverts that landed).
- **Behavior: `pf-integrate close` and `pf-revert` grow.** `close` takes
  `--revert` (pf-revert's result: the record's `reverts`), `--triage`
  (pf-gate --triage's result, which now names the gate run it triaged,
  `gate_run`: the regate of this gate run's triage is the gate's result;
  a triage of a run an owner's rerun replaced is not),
  `--execution` (runtime-state/execution.json: the record's
  `hold_rulings` from collect's output) and `--ledger`
  (runtime-state/effects.json: the record's `fix_tickets`, the filed
  references); a named gate, triage or revert file that does not exist is
  absent (an empty batch ran none), never an error. `pf-integrate
  collect`'s `holds` carry the held item's `merge_commits` and its
  `landed_fixes`, and `pf-revert --item` reverts a held item from them
  (a stuck hold the owner ruled reverted is seldom a batch item).
- **Behavior: a batch's progress is its step state.** `GET
  /status?integration=1` (`pf ops integrate status`, the panel) reads a
  batch's `step`, `detail`, `done`/`total` and `at` from the step state
  of its execution record — the current step, its status (`awaiting the
  owner` on an open `pf ask`) and round, the lane's steps done over its
  steps — never from `artifacts/integrate/progress.json`, which the lane
  no longer writes.
- **Why.** Workflow v2 (design §4.3): the daemon orchestrates and checks
  shapes, the step's agent runs the command and decides; the owner is
  asked in words by the step that needs the decision; the hub writes are
  declared, applied once under idempotent keys, and visible on the ledger.
- **Action: none beyond the binary.** Re-run `tools/install.sh` on each
  machine (the `pf` binary; the step commands are already linked beside
  it; no schema change, no aihub change). The lane's steps name tiers
  (`collect` mid, `revert` and `close` cheap): a workspace yaml binds
  them with `tiers.<project>.<tier>.models`, or per step with
  `steps.<project>.<step>.models` (`revert` is a new step key);
  `full-gate` names no tier and runs on `steps.<project>.full-gate.models`,
  else the project default (the strong model). A batch in flight under the old lane
  finishes on its snapshot. Rollback: the previous binary; a batch claimed
  under this lane parks blocked, kind `contract`, under it — cancel it and
  file it anew.

## develop: sync, verification and merge are agent steps on pre-written commands; shared skills (workflow contract v2, change 11)

- **Behavior: every develop step is an agent step.** `sync`, `verification`,
  `publish` and `merge` are mechanical steps on the `cheap` tier
  (`model_tier: cheap`: the workspace yaml's `tiers.<project>.cheap.models`,
  else the project default — the lane's `defaults.tiers.cheap` is a
  placeholder and never outranks it; a step's own
  `steps.<project>.<step>.models` outranks both), each following a
  shared skill —
  `workflows/_shared/skills/{sync,verify,publish,merge}.md`, rendered into
  the step's prompt as `# Skill` — that runs one pre-written command
  verbatim and routes on the exit code its result file carries
  (`artifacts/sync/result.json`, `artifacts/verify/result.json`,
  `artifacts/merge/result.json`), and verifying the step before it first
  (`verifies`, the prompt's `# Verify first`): sync checks review's judged
  heads, verification the sync's result (no conflict, HEAD at `after`),
  publish the verification's result (pass at HEAD, or the owner's waiver)
  and review's verdict, merge publish's repos against the verified heads.
  A hand-off that does not hold is a gate's FAIL along its `on_fail`. The
  daemon runs no action for develop any more: no in-process sync,
  verification or merge, no verify-judge, no conflict resolver, no
  publish-binding check, no notarized evidence on publish.
- **Behavior: sync is no gate and resolves a real conflict in place.**
  `pf-sync` leaves a real conflict in progress (exit 1); the sync agent
  resolves the files, stages them and runs `pf-sync --continue`, which
  commits the merge and rewrites the result with `resolved[]`; the
  verification tests the resolved tree and publish's PR body lists the
  files ("conflict resolved in sync: <files>"). The owner's conflict review
  before the merge (`resolved-conflict-review`) is gone: the sync agent
  asks the owner (`pf ask`) when it is not confident, and a premise the
  default branch changed is an `intent: rewind` to build. A sync FAIL back
  to build no longer exists; merge's FAIL (a PR that moved, closed or was
  refused by the forge — `pf-merge` exit 1) still rewinds to sync, charged.
- **Behavior: `pf-sync --abort`; the sync record is tied to its round.**
  The new flag abandons a sync: every merge its record left in progress is
  aborted (nothing reset; a merge already committed stays), the record is
  removed and the result is written marked `aborted`. The sync agent runs
  it before any rewind or cancel intent, so build never resumes on a
  half-merged tree. The record (`syncing.json`) now carries the step round
  (`PF_ROUND`): a plain `pf-sync` of a later round supersedes an unsettled
  record (its merge aborted, the sync started over from HEAD) instead of
  resetting HEAD to the old `before`, and within a round a HEAD that moved
  past `before` with commits that are not the sync's own merge is refused
  (exit 2) rather than discarded — `pf-sync --abort` drops the record and
  the commits stay. A hand-made merge commit is still reset and made again.
- **Behavior: verification is the lane's one waivable gate.** `pf-verify
  --tier light` runs under the heavy-run admission (no bar lock); the
  result's verdict is the exit code's. A red the agent can place on the
  change FAILs to build; a red it cannot place is asked (`pf ask`) and
  waived only by the owner's words (`intent: waive`, the `waived` record,
  no rewind charged), or by `pf waive <wi> --step verification` on a spent
  budget; an unchanged tree that already failed is asked about, not run
  again. A waived verification lands with the heads its red run tested
  (`light_verified_head` stays set; the landing's `waived` names the
  gate).
- **Behavior: merge is an agent step that hands the landing off.** The
  merge agent runs `pf-merge` under the daemon's per-repo merge lock
  (`resources: { locks: [merge] }`) and delivers `landing` (`handoff:
  landing`), which the daemon copies onto the record with its own waivers;
  the wrap carries it as `integration` exactly as before. It is now the
  lane's last agent step: the wrap summary (`experience.wrap`) is its.
  `blocked: forge` no longer arises on develop (a PR the merge cannot
  take is the gate's FAIL; gh unreachable is `incomplete`).
- **Why.** Workflow contract v2 (design §4.1): the daemon orchestrates and
  checks shapes, agents judge; downstream verifies upstream in place of
  the daemon's cross-checks.
- **Action: re-run `tools/install.sh` on each machine** (the `pf-sync`,
  `pf-verify` and `pf-merge` links beside pf; `pf doctor`'s step-commands
  row checks them), and set the `cheap` tier's model in the workspace yaml
  (`tiers.<project>.cheap.models`; docs/reference/config.md, "Tiers") —
  without it develop's four mechanical steps run on the project default.
  No schema
  change, no aihub change, no drain (the contract version stays 2): a
  work item in flight keeps its snapshot. Rollback: the previous release's
  lane files with its binary; a work item claimed on the new lane finishes
  on its snapshot either way.

## develop: build records units.json, review replays the red runs via pf-facts, pf-verify --unit-runs; same-tree rules; no sweep (workflow contract v2, change 12)

- **Behavior: build records its own runs; the daemon checks none of them.**
  develop's `build` follows the shared `build` skill
  (`workflows/_shared/skills/build.md`): it runs each unit red at its test
  commit and green at its implementation commit itself — no `pf bar-run`,
  no daemon record, no notarized evidence — and records every unit in
  `artifacts/build/units.json`, a JSON array `[{id, test_commit,
  impl_commit, run{repo, cwd, argv}, marker}]` (required, `format: json`:
  the daemon checks only that it parses). Its output is `coverage` and
  `summary` (the owner's decisions verbatim and its reading); `plan_path`,
  `units`, `tier`, `build_status` and `needs_input` are gone, and so are
  the plan's `## Bindings` and `## Tier`. The lane declares no check on
  build any more (`owner-stops`, `test-first-units`,
  `acceptance-coverage`, `e2e-carries`): nothing refuses a build
  completion for its content.
- **Behavior: build asks through `pf ask`, only in four cases.** A dead
  end or contradiction, a deviation from the ticket, `confirm_plan: true`
  (right after the plan, before the first test) and a missing or
  incomplete acceptance list (asked once with the proposed list) — never
  at completion; the question ids `confirm-plan`, `build-acceptance`,
  `build-deviation` and `build-dead-end` are gone. Answer with `pf answer
  <wi> --text "<words>"`. A round a FAIL opened changes the tree or asks.
- **Behavior: review computes its facts with `pf-facts`.** develop's
  `review` follows the shared `review` skill under the heavy-run admission
  (`resources: { admission: heavy }`, tool window 20 minutes, wall 30): it
  runs `pf-facts plan-conformance`, `pf-facts build --replay` (each unit's
  run replayed on the tree of its test commit — in the repo's verification
  container when its HEAD carries `.polyforge/Dockerfile.verify`, else on
  the host under the step's command `PATH`, as `pf-verify` runs the bar; a
  run that passes there is `test_weakened`) and `pf-facts carries` as
  written, writing `artifacts/facts/{plan-conformance,build,
  carries}.json` (required on a pass, `format: json`), and verifies build's
  hand-off first: the plan, `units.json`, the summary's decisions against
  the owner's words (`build.answers`), a `confirm_plan` reply before the
  first test commit. The driver's `# Build facts` and `# Plan
  conformance` sections, the `finding-classes` check (the schema's enum,
  now `schema: enforce`) and the text-only sweep after a PASS are gone
  from the lane (their code stays until contract 3 removes the keys).
- **Behavior: verification runs the units.** `pf-verify` gains
  `--unit-runs <units.json>`, and develop's verification runs `pf-verify
  --tier light --unit-runs artifacts/build/units.json`: each unit's run
  is run again as written beside the light plan, one check
  `<repo>:unit-run:<id>` each (exit 0 and a line matching its marker; a
  failing first run is re-run once; never pre-existing). The marker is
  required: a unit with none, or with one the empty string matches
  (`.*`, `^`), fails its check without running.
- **Behavior: same-tree rules in the skills.** build changes the tree in
  a round a FAIL opened — or rewrites the unit in units.json when the
  FAIL names a unit's record — or asks; review does not judge again a
  tree whose heads equal its `last_review_round` with the units.json that
  round judged (its sha256 in `artifacts/review.md`); verification does
  not re-run a tree and units.json an earlier round failed (the sha256
  heads `artifacts/verify/report.md`) — each asks the owner instead. The
  daemon's dispatch-time unchanged-tree park still fires first until it
  is deleted, and it compares the trees alone: a round that changed only
  units.json parks there until the owner resumes.
- **Why.** Workflow contract v2 (design §4.1): the daemon checks shapes,
  agents judge; the units the build records are the runs review replays
  red and verification runs green.
- **Action: re-run `tools/install.sh` on each machine** (the `pf` binary;
  the `pf-facts` and `pf-verify` links are already there), and
  `/plugin marketplace update` where the polyforge skill is installed (its
  file, execute-intent and SKILL texts name the build's `pf ask` stops).
  No schema change, no aihub change, no drain (the contract version stays
  2): a work item in flight keeps its snapshot. Rollback: the previous
  release's lane files with its binary; a work item claimed on the new
  lane finishes on its snapshot either way.

## micro: fix, review, sync, evidence, publish and merge are agent steps on pre-written commands; evidence waivable; out of bounds asks the owner (workflow contract v2, change 10)

- **Behavior: every micro step is an agent step.** `fix` and `review` run
  on the `mid` tier, `sync`, `evidence`, `publish` and `merge` on the
  `cheap` tier (the workspace yaml's `tiers.<project>.<tier>.models`, else
  the project default — the lane's `defaults.tiers` are placeholders); each
  step follows a skill rendered into its prompt as `# Skill` — micro's own
  `workflows/micro/skills/{micro-bounds,evidence}.md`, the shared
  `sync`, `publish` and `merge` the develop lane uses — that runs one
  pre-written command verbatim and routes on its result file, and verifies
  the step before it first (`# Verify first`). The daemon runs no action
  for micro any more: no micro-bounds check, no in-process sync, run or
  merge, no conflict resolver, no publish-binding check, no notarized
  evidence on publish; `blocked: forge` no longer arises on micro.
- **Behavior: fix writes the evidence plan and judges its own bounds.** It
  writes `artifacts/fix/evidence.json` (`[{repo, line}]`: the commands of
  `verify-scope --micro --json`, the fast phase then every declared
  `micro_check`) and runs `pf-facts micro-bounds --plan
  artifacts/fix/evidence.json --json --out artifacts/facts/micro-bounds.json`
  before it completes; review runs the same command itself and a change
  out of bounds, or a declared command the plan omits, is its blocking
  finding (FAIL to fix); review reads fix's owner replies (`fix.answers`)
  and judges a narrowing the owner approved as the change to judge. The evidence step runs exactly that plan:
  `pf-run --plan-from artifacts/fix/evidence.json --json --out
  artifacts/run/result.json`, every line in its repository's verification
  container (a repository without `.polyforge/Dockerfile.verify` cannot run
  it, as before), under the heavy-run admission and no bar lock. The
  output field `evidence` and the `fast_lane` record are gone from micro.
- **Behavior: out of bounds asks the owner instead of failing the run.**
  Before, a fix out of bounds ended the work item `failed_gate` with
  `fast_lane_out_of_bounds`, naming the re-file lane. Now fix asks the
  owner (`pf ask`: the refused paths and the suggestion to re-file on
  `develop`) and the work item waits awaiting the owner; when the owner
  agrees in free text, fix delivers `intent: cancel` naming that reply and
  the work item ends **canceled** (the owner files the work anew on
  develop — nothing files it for them); the owner may instead ask for a
  narrower change, which fix makes and judges again.
- **Behavior: evidence is micro's one waivable gate.** A red line the
  change caused FAILs to fix; a red it cannot have caused is asked
  (`pf ask`) and waived only by the owner's words (`intent: waive`, the
  `waived` record, no rewind charged) or by `pf waive <wi> --step evidence
  --reason <text>` on a spent budget. Publish reads the waiver beside the
  red run result only when the waiver is this round's (its `round` is the
  run result's) and the run's heads are HEAD; the landing's `waived` names
  the gate `evidence` and its `light_verified_head` is the head the red run
  tested. The
  resolved-conflict question (`resolved-conflict-review`) is gone from
  micro: sync's agent resolves a real conflict in place and asks the owner
  only when unsure — handing the work back to fix (`intent: rewind`) or
  cancelling only after `pf-sync --abort`; publish's PR body lists the
  resolved files.
- **Behavior: merge is micro's last agent step.** It runs `pf-merge
  --verified artifacts/run/result.json` under the per-repo merge lock,
  writes the wrap summary (`experience.wrap`) and then hands `landing` off; a PR
  that moved, closed or was refused FAILs back to sync, charged.
- **Behavior: review's finding class is its schema's.** Micro's review
  declares no `finding-classes` check any more: its output schema
  enumerates the one class `code` and, like every schema, records a
  violation (`output_schema_violation`) instead of refusing the envelope.
- **Behavior: `_shared/steps/publish.md` is the workflow-v2 publish.** The
  shared definition micro's publish uses (develop keeps its own) now
  defers to the `publish` skill; a user lane whose `publish` step relied on
  the old shared text (notarized `pf bar-run` pushes, the publish-binding
  check) carries its own `steps/publish.md` from the previous release.
- **Why.** Workflow contract v2 (design §4.2): the daemon orchestrates and
  checks shapes, agents judge; downstream verifies upstream in place of
  the daemon's cross-checks; Q6: out of bounds is the owner's call.
- **Action: re-run `tools/install.sh` on each machine** (the `pf-facts`,
  `pf-sync`, `pf-run` and `pf-merge` links beside pf; `pf doctor`'s
  step-commands row checks them), and set the `mid` and `cheap` tiers'
  models in the workspace yaml (`tiers.<project>.mid.models`,
  `tiers.<project>.cheap.models`; docs/reference/config.md, "Tiers") —
  without them micro's steps run on the project default. No schema change,
  no aihub change, no drain (the contract version stays 2): a work item in
  flight keeps its snapshot. Rollback: the previous release's lane files
  with its binary; a work item claimed on the new lane finishes on its
  snapshot either way.

## Workflow contract 3: the v1 keys removed, every step an agent step (workflow contract v2, change 15)

- **Behavior: contract 3 removes the v1 keys.** A lane declares
  `contract: 3`. The lane property `build_step` (with its reserved
  `{{build_step}}` placeholder), the step keys `executor`, `action`,
  `with`, `checks`, `sweep` and `evidence`, and the `facts` input source
  are removed: each is `WFC-111`, its message naming the rewrite. Every
  step is an agent step. The rewrites:

  | removed key | rewrite |
  |---|---|
  | `build_step` (lane) | delete it; a gate's FAIL rewinds to its `on_fail` target, and a definition names the step it means (a var of its own or the step's name) instead of `{{build_step}}` |
  | `executor` | delete it; a daemon step becomes an agent step whose skill runs the action's pre-written command: `verify` → `pf-verify`, `sync-default-branch` → `pf-sync`, `merge-pr` → `pf-merge`, `run` → `pf-run`, `collect-landed` and `mark-green` → `pf-integrate`, `full-gate` → `pf-gate` |
  | `action` | delete it; the step's `skill:` runs the action's command: `verify` → skill `verify`, `sync-default-branch` → skill `sync`, `merge-pr` → skill `merge`, `run` → a skill running `pf-run`, the integration actions → the integrate lane's `collect`, `full-gate` and `close` skills |
  | `with` | delete it; the action's parameters are the arguments of the command line the skill runs |
  | `checks` | delete it; the daemon runs no closing check: the next step verifies the hand-off (`verifies`), and a review judges the facts `pf-facts` computes |
  | `sweep` | delete it; a non-blocking finding reaches the owner through the PR body |
  | `evidence` | delete it; the daemon notarizes no command (a step running heavy commands declares `resources: {admission: heavy}` and `budget.tool_minutes`) |
  | `facts` (input) | delete it; the driver computes no fact section: run `pf-facts <kind> --json --out <path>` through the step's skill and read the result file |

  The same table, with the keys removed earlier, is
  docs/reference/workflow-contract.md, "Removed keys". The record catalog keeps
  `last_review_round` and `waived` (a `step: <id>.record.heads`,
  `.commands`, `.test_files` or `.fast_lane` input is `WFC-180`); the
  check catalog, the fact sections and the sub-definitions leave the
  contract. The codes `WFC-131`–`WFC-133`, `WFC-150`–`WFC-152`,
  `WFC-160`–`WFC-162`, `WFC-171`, `WFC-185` and `WFC-196` are no longer in
  force; `WFC-130`, `WFC-141`, `WFC-172`, `WFC-180`, `WFC-191` and
  `WFC-195` narrow as the reference page states. The contract's envelope
  no longer carries `needs_input` (`question_id`, `question`, `options`),
  `run_request` or `command_evidence`: a step asks the owner with `pf ask`
  and runs its own commands (the daemon's v1 handling of those fields is
  deleted with the v1 mechanisms in changes 16–19).
- **Behavior: the shipped lanes.** Every lane declares `contract: 3`;
  develop, micro and integrate no longer declare `build_step` (integrate's
  `revert` stays `full-gate`'s `on_fail` target, its FAIL edge unchanged).
  The shared review definition names its rework step through the var
  `rework_step` (develop's review declares `rework_step: build`). Every
  skill states that the owner is asked only through `pf ask` and that a
  command is never backgrounded and polled nor given a timeout or deadline
  of the agent's own; a test pins each skill's key rules, the same-tree
  rules included, in its step's assembled prompt. The shared principles'
  waiting rule says the same (a command runs in the foreground with no
  timeout of the agent's own: the harness bounds each call by the step's
  tool window), and the unused `deliverable_carrier` fragment, which still
  taught `needs_input`, is gone.
- **Behavior: user lanes.** A user lane still declaring `contract: 2` is
  refused (`WFC-101`, and a `WFC-111` naming the rewrite for each v1 key
  it declares): it is unavailable (one `user_lane_refused` event) until it
  declares `contract: 3`, drops `build_step` and rewrites its
  `{{build_step}}` placeholders, `executor: daemon` steps, `checks`,
  `sweep`, `evidence` and `facts` inputs as the rewrites say.
- **Why.** Workflow v2 (design §2.1, §6.1): contract 2 carried the v1 keys
  beside the v2 ones so the lanes could migrate one by one (changes
  9–14); contract 3 removes them, so no lane reaches the daemon's actions,
  checks, sweep, notary or fact sections any more, and their code is
  deleted in changes 16–19.
- **Action: drain, then deploy (drain #2).** Stop claiming (`pf ops auto
  off`), let the work items in flight finish, deploy, re-run
  `tools/install.sh` on each machine (the `pf` binary; no schema change,
  no aihub change). A work item whose capsule snapshot targets contract 2
  parks blocked, kind `contract` — cancel it and file it anew; a queued
  work item is unaffected (the lane names are unchanged: its claim takes
  the new snapshot). Rewrite each workspace's user lanes before the
  deploy (`pf workflows validate --workspace <dir>` names every defect).
  Rollback: the previous binary with its contract-2 lanes; a capsule
  claimed under contract 3 parks blocked, kind `contract`, under it.

## The watch snapshot drops verify_queue, run_request, options[].action_text and asked_by (workflow contract v2, change 20)

- **Behavior: four projections leave the watch snapshot.** `GET
  /jobs/{wi}/watch` (`pf watch <wi> --once [--json]`, Pi Web's job card)
  no longer carries `verify_queue` (a dispatch waiting for the heavy-run
  admission), `execution.run_request` (a pending run request),
  `execution.pending_question.options[].action_text` (the daemon's fixed
  text for an option's action) or — a fourth, found dead since change 14
  (#911) — `execution.pending_question.asked_by` (the asking dispatch's
  ordinal, sub-definition, harness, session id and transcript path, which
  only the retired discussion assistant read; the capsule's
  `execution.json` still records the asker). `pf watch <wi> --once`
  prints no `verify:` line and lists an action option as `<id> → daemon
  action <action> (agent's label: …)`; a pending run request is still
  never shown as a question. Pi Web's watch route no longer passes
  `actionText`: the composer lists a contract-1 option as its id and its
  label.
- **Unchanged.** The heavy-run admission and its queue: the
  `verify_queued` / `verify_admitted` events (the job card's progress
  stream, `pf watch`'s event list) and the `GET /status` job row's
  `verify_queue` (`pf ops status`'s `VERIFY` column, Pi Web's queue split
  and the card's `verification (queued #n)`). An option keeps `id`,
  `label` and `action`.
- **Why.** Workflow v2 (design §5.4): the job card reads the events and
  the watch snapshot, and the queued state is an event there. Under
  contract 3 no step can send a run request (only a notarized step could,
  and `evidence` is gone) and no step reaches the integration ruling, the
  one place the daemon runs an option's action; so the run request and the
  action text had nothing left to show, and `verify_queue` had no reader
  but `pf watch --once`. `asked_by` was read only by Pi Web's discussion
  assistant, deleted in change 14, and Pi Web has dropped it since.
- **Action: re-run `tools/install.sh` on each machine** (the `pf` binary
  and Pi Web), and `/plugin marketplace update` where the polyforge skill
  is installed (its answer verb shows the new option line). No schema
  change, no aihub change, no drain (the contract version stays 3).
  Rollback: the previous binary and Pi Web show the four fields again;
  nothing this change wrote is read by them.

## The v1 question's answer, its options and the reserved `cancel` are deleted; so are the checks, the fact sections, the sweep and the unchanged-tree park (workflow contract v2, change 19)

- **Behavior: a step's `needs_input` is refused.** The v1 question left
  the contract with workflow contract 3; the driver still accepted it
  until now. A step's `needs_input` envelope is refused at `pf result
  submit` and at the accept gate (`DAEMON-400`, an envelope rejection),
  and the step is re-dispatched told to `pf ask "<question>"` and end its
  turn; the delivery contract no longer names the outcome.
- **Behavior: `pf answer` is the owner-message relay alone.** `pf answer
  <wi> --text "<words>"` relays the words to the current step's agent at
  any time. `--question` and `--option` are gone, and so is the reserved
  `cancel` answer: `pf cancel` cancels, or the owner says so and the
  step's agent delivers `intent: cancel`. `POST /jobs/{wi}/answer` takes
  `{answer_text, answered_by?}` only and refuses a `question_id` or an
  `option` (`DAEMON-400`). The option selection (an option's id as the
  answer, the action options and their labels' check) goes with it.
- **Behavior: no v1 question on any face.** The watch snapshot's pending
  question, the status face's QUESTION column and `needs_input` job state
  are the current dispatch's open `pf ask` only; the snapshot carries no
  option set and `pf watch <wi> --once` prints no `options:` lines; the
  decision feed and the model-health view no longer read a `needs_input`
  event (none is written); the monitor's `pending_invalid` diagnostic is
  gone. Pi Web's job card sends every message as words (`--text`): the
  transitional option path (a whole message equal to an option id, or
  `/<id> <words>`) and the option list under the question are gone.
- **Behavior: the driver's checks, fact sections, sweep and
  unchanged-tree park are deleted.** No contract-3 lane declares a check,
  a `facts` input or a sweep, and the shipped lanes' skills carry the
  same-tree rules; their code goes: the check runners, the fact-section
  renderers, the text-only sweep, and the dispatch-time park of a gate
  whose task-repo tree is unchanged since its last verdict (the `gate tree
  unchanged:` blocked reason, and the resume that cleared it). The capsule
  tree hash that picks a retry's continue-in-place wording stays.
- **Behavior: aihub's cancel door has no `dry_run`.** The daemon probed it
  before carrying out a reserved `cancel` answer; the door now cancels
  (or refuses) as `pf cancel` always did.
- **Why.** Workflow v2 (design §6.4 change 19): the owner's words are free
  text the asking agent reads, so the daemon keeps no grammar — no
  question id to route by, no option to select, no reserved word — and
  no check, fact section or tree comparison judges a step's work.
- **Action: deploy aihub, then re-run `tools/install.sh` on each machine**
  (the `pf` binary and Pi Web), and `/plugin marketplace update` where the
  polyforge skill is installed (its answer verb is the relay's). No
  schema change and no drain (the contract version stays 3), but answer
  every open v1 question before the deploy (`pf ops status`'s QUESTION
  column names it): afterwards it has no answer path — cancel such a work
  item and file it anew. Rollback: the previous binary answers a v1
  question again; nothing this change wrote is read by it.

## The daemon's verify, run, sync and merge actions, the heads record, `verify_deadline` and `binding_missing` are deleted (workflow contract v2, change 16)

- **Behavior: the daemon runs no action.** No contract-3 lane declares
  `executor: daemon`, an `action` or a `with` (each is `WFC-111`), so the
  daemon's own `verify`, `run`, `sync-default-branch` and `merge-pr`
  actions were unreachable; their code goes with the dispatch that ran
  them, the verify-judge and conflict-resolver sub-definitions they
  dispatched (`workflows/_shared/steps/verify-judge.md` and
  `resolve-conflicts.md`), the claim's run-repo check (`WFC-185`) and the
  `verify: queued` record a daemon-run verification kept on its step.
  Every step is an agent step: the shipped lanes verify, sync and merge
  with `pf-verify`, `pf-sync` and `pf-merge` on agent steps (changes 9–13),
  and a heavy step's admission (`resources: { admission: heavy }`) still
  queues it with no process, no budget and no job slot, writing the
  `verify_queued` / `verify_admitted` events as before.
- **Behavior: no heads record.** The driver no longer writes the task
  repos' HEADs onto an accepted step's record (`record.heads`); its last
  readers were the merge action's landing rule and the publish binding. A
  gate's `last_review_round.heads` (the tree a verdict judged) stays.
- **Behavior: `pf-verify` verifies the tree as it stands.** The bar
  library's merge phase (`git fetch` and `git merge origin/<default>` before
  the bar, the daemon action's) is gone, and so are the parts of its
  result only the action read: `test_bar`, `test_bar_notes`,
  `command_evidence`, the `test_files` checks and the plan bindings read
  from `runtime-state/execution.json`. `pf-verify`'s light baseline is
  `merge-base HEAD origin/<default>`, as it was.
- **Behavior: `[daemon] verify_deadline` is gone.** It bounded the bar the
  daemon ran in-process; `pf-verify` takes its own `--deadline` (default:
  two minutes under the step's tool window). A `cli.toml` that still sets
  the key is refused as an unknown key: delete the line before the upgrade
  (`pf config unset verify_deadline` on the old binary). `command_path` and
  the `verify_container_*` keys stay — the step commands read them.
- **Behavior: no `binding_missing` or `forge` block.** The watch
  snapshot's `execution.blocked.kind` no longer names `binding_missing` (a
  daemon step missing its daemon binding) or `forge` (the merge action's
  forge trouble): no daemon step exists to raise them; Pi Web no longer
  labels them, and its job card no longer renders the daemon run's
  `verify_routed` end line, its check lines or a verify-judge block.
- **Behavior: `pf-verify`'s bar is light-only.** The bar library's full
  and scoped tiers (the daemon action's) are gone: `verifyrun` runs the
  light tier alone and refuses any other; the full tier is `pf-gate`'s.
- **Note for change 19: no fast-path suppression on an unchanged tree.**
  Change 19's section missed one effect of the same-tree guard's deletion:
  a rewound build round's retry no longer suppresses the continue-in-place
  wording when the task repos' tree still equals the tree the rewinding
  gate judged (`judged_repo_hash`, criterion 5) — the capsule
  tree hash alone decides it.
- **Why.** Workflow v2 (design §6.4 change 16): the daemon orchestrates
  agent steps; the mechanical work is the steps' pre-written commands, so
  the action machinery had no caller left.
- **Action: re-run `tools/install.sh` on each machine** (the `pf` binary),
  after removing `verify_deadline` from every Cell's `cli.toml`. No schema
  change, no aihub change and no drain (the contract version stays 3): a
  contract-3 work item never ran an action. Rollback: the previous binary
  accepts `verify_deadline` again; nothing this change wrote is read by it.

## The notary, `pf bar-run`, the run request and the push rules are deleted (workflow contract v2, change 18)

- **Behavior: no `pf bar-run`.** The verb, the daemon's `POST
  /step/bar-run` and `POST /step/bar-lock` routes and the per-work-item
  bar-run record (`cells/<profile>/bar-runs/<work-item-id>.jsonl` under the
  data dir) are gone: no contract-3 lane declares `evidence`, so no step was
  notarized. A step runs its commands itself — the pre-written `pf-*`
  commands its skill names — and a script still calling `pf bar-run` gets
  an unknown-command error. An existing `bar-runs/` directory is no longer
  read or written; delete it at will.
- **Behavior: the envelope carries no question, run request or command
  evidence.** A `needs_input` envelope is refused naming `pf ask` — it can
  no longer ride as a run request — and `question_id`, `question`,
  `options`, `run_request` and `command_evidence` are no envelope fields:
  an envelope still carrying them lands without them (`pf result submit`
  and the driver read none). A step's execution record no longer holds
  `pending_question`, `answers`, `answer_base` or the `commands` record;
  those keys in an existing `execution.json` are ignored.
- **Behavior: no `environment` block.** The watch snapshot's
  `execution.blocked.kind` no longer names `environment` (a run request
  the daemon could not run) and `execution.blocked` carries no `log_tail`;
  Pi Web's panel labels follow. A blocker an agent names `environment` is
  an `incomplete` like any other.
- **Behavior: no daemon-held question.** The owner's send-back emits no
  `question_withdrawn` event (an open `pf ask` belongs to the dispatch the
  send-back stops), the decision feed reads no `answered` event, and the
  candidate pointer and the retry streaks read no answer era — the owner's
  continue (`pf resume`) is the one era boundary.
- **Behavior: the publish rules are gone.** The push rule, the publish
  result rule and the resolved-conflict-review rule lost their check with
  change 19; their code goes with this change. The publish skill's agent
  verifies its upstream itself (P1).
- **Behavior: the per-repo bar lock stays for `resources: { locks: [bar]
  }`.** A step declaring it still has the heavy-run admission reserve its
  task repos' bar-lock turns (`max_concurrent_bars_per_repo`); only the
  step's own lock route and the daemon's in-process locker are gone. No
  configuration key is removed: `max_concurrent_bars_per_repo`,
  `max_concurrent_bars` and `bar_load_threshold` keep their meaning.
- **Why.** Workflow v2 (design §6.4 change 18; O5): the daemon notarizes
  no command — a downstream step verifies the result file its upstream's
  command wrote — so the notary, the run request and the rules that read
  its records had no caller left.
- **Action: remove `pf bar-run` from your scripts** (a call now fails as an
  unknown command; run the line itself, or the step's `pf-*` command).
- **Action: re-run `tools/install.sh` on each machine** (the `pf` binary
  and Pi Web), and `/plugin marketplace update` where the polyforge skill
  is installed (its answer, resume, rewind, watch and waive verbs changed). No
  schema change, no aihub change and no drain (the contract version stays
  3): a contract-3 work item never sent a run request. Rollback: the
  previous binary runs again; nothing this change wrote is read by it.

## Workflow contract 4: one step entry (pf step) and one hand-off document (handoff.md)

- **Behavior: a step talks to the daemon through `pf step` alone.** A lane
  declares `contract: 4`. A step ends every dispatch with exactly one `pf
  step handoff --status <done|fail|blocked|cancel|waive|rewind> --body
  @<file> [--evidence <path>]… [--to <step>] [--per-owner <at>] [--reason
  "<line>"]`, checked at the call (a wrong call exits non-zero at once,
  naming why; no envelope is refused later). `cancel`, `waive` and
  `rewind --to <step>` carry out an owner's word and must cite the
  owner's message (`--per-owner`, its `at` in `runtime-state/asks.jsonl`;
  the daemon checks that it exists, never what it says). It asks the owner
  with `pf step ask`, reads and writes memories with `pf step recall`,
  `pf step memory show` and `pf step remember`, and reaches aihub only
  through `pf step hub <verb>` (`get`, `list`, `file`, `move`,
  `clear-hold`, `wrap-attrs`, `batch`), each verb granted by the step's
  new `hub:` key. A hub write is recorded pending for the step's round and
  made by the daemon only when the round hands off `done`; any other status
  drops it (the ledger records it `dropped`). An empty `batch` is accepted
  and records nothing; `clear-hold` must cite the owner's message
  (`--per-owner`), whose writer, stamp and words the daemon records with
  the clear.
  Every call is recorded first in the new call ledger
  `runtime-state/step-calls.jsonl`. The reference is
  docs/reference/workflow-contract.md, "The step interface".
- **Behavior: one hand-off document.** `artifacts/handoff.md` is
  append-only: the daemon writes its opening at the claim (goal,
  acceptance, step order), stamps every section's header (step, round,
  dispatch, times, model, status, the owner's messages verbatim, each
  repo's start and end commit, the evidence paths) and appends rewind and
  wrap events; `pf step handoff` adds the body — seven template sections
  (`_shared/handoff.md`), at most 3,000 characters. Every step reads it
  first (`_shared/step-header.md`). It replaces the step outputs, the
  `# Inputs` and `# Verify first` sections, the rewind input and the
  prior-round sections.
- **Behavior: contract 4 removes keys.** Each is refused naming its
  rewrite — `WFC-111`, but `WFC-191` for a definition's `output` and
  `WFC-112` for `fix_lane` and `bar`; `WFC-141`, `WFC-172`, `WFC-180`–`WFC-182`, `WFC-192`,
  `WFC-193`, `WFC-197`, `WFC-204` and `WFC-205` are no longer in force:

  | removed | rewrite |
  |---|---|
  | `inputs` | delete it; the step reads `handoff.md` and the files its definition or skill names |
  | `verifies` | delete it; the skill's read-and-verify-first section says what to check |
  | `outputs` | delete it; the skill names the files it writes; evidence paths ride `--evidence` |
  | `verdicts` | delete it; a gate passes with `done` and fails with `fail`; `defer` becomes `done`, the reason in the body |
  | `effects` | delete it; the skill runs `pf step hub file`/`move`/`clear-hold` or one `batch`, listed in `hub:` |
  | `handoff` | delete it; the skill runs `pf step hub wrap-attrs <name> @<file>` and the step declares the new `wrap_attrs: [<name>]` — its `done` is refused until the record is registered (develop's and micro's merge: `integration`; integrate's close: `integration_batch`) |
  | `attrs` (step) | delete it; write the behaviour into the definition or a `vars` entry |
  | `on_fail.charge` | delete it; an automatic FAIL is charged, one handed off with `--per-owner` is not |
  | `integration.fix_lane` | delete it; the integrate skill passes `--lane <lane>` to `pf step hub file` |
  | `output` (step definition frontmatter) | delete the schema; the definition keeps `name` and `description` |
  | `resources.locks: [bar]` | delete `bar`; `merge` is the one lock |

  `integration` stays as the marker `integration: true` the integrate
  trigger and `pf ops integrate` find the integration lane by. A user lane
  may not declare it (`WFC-112`); no other integration-lane rule remains.
- **Behavior: the daemon reacts to the status.** `done` makes the pending
  writes and dispatches the next step; a gate's `fail` appends a rewind
  event and rewinds to `on_fail.target` (charged unless `--per-owner`);
  `blocked` parks; `cancel`, `waive` and `rewind` end the work item,
  accept the gate, or rewind uncharged. With `max_rewinds` spent a `fail`
  without `--per-owner` is refused at the call, so no work item parks on a
  spent budget any more (the blocked kind `rewind_budget` is gone, and so
  is `result_invalid`); a failed pending write parks it `write_failed`
  (the old `effect_failed`), replayed by `pf resume`. The daemon no longer
  parks a re-run whose tree did not change: the review skill judges that
  from the stamped commits.
- **Behavior: an owner message wakes a parked work item.** `pf answer` (and
  Pi Web's job card, whose input box now stays open) on a work item parked
  by its step's `blocked`, a timeout or exits without a hand-off, a failed
  write or a pause wakes the step concerned with the message instead of
  `DAEMON-409`. Only a work item no agent can start (a broken model
  configuration, no candidate left) stays parked; its card shows why and
  the `pf resume` / `pf cancel` lines, and `pf answer` refuses the
  message (`DAEMON-409`, reason `WAKE_CANNOT_START`) without recording it.
  The wake opens the aihub door `pf resume` opens with the caller's own
  key (a blocked work item's resume-blocked route, a paused one's
  `clean_pause`), then the daemon records the message and claims the work
  item; the daemon's own key opens no door, and aihub is unchanged.
- **Behavior: supervision.** An exit without a hand-off resumes the same
  session (the third in a row parks); a model error moves to the next
  candidate; a transient upstream failure retries the same model after a
  backoff, uncharged; a timeout resumes once with the limit doubled; any
  other exit resumes once.
- **Behavior: the prompt is six parts** — identity and dispatch facts, the
  prohibited actions, `_shared/step-header.md` with the body template
  `_shared/handoff.md`, the definition, the skill
  and, on a resume, the owner's messages or the missing-hand-off reminder.
  `# Inputs`, `# Verify first`, the output schema, the rewind input, the
  prior round, the rewind budget, the recalled memories' text, the memory
  duty and the delivery contract are gone; the memory policy is still
  written at the claim (`runtime-state/memory-policy.json`) and the step
  reads it.
- **Behavior: removed commands and routes.** `pf result submit` →
  `pf step handoff`. `pf waive` → tell the gate's agent; it hands off
  `--status waive --per-owner <at>`. `pf rewind` → tell the current step's
  agent; it hands off `--status rewind --to <step> --per-owner <at>`. A
  step's `pf ask`, `pf recall`, `pf memory show` and `pf remember` →
  `pf step ask`, `pf step recall`, `pf step memory show`, `pf step
  remember`. The owner keeps `pf resume` and `pf cancel`. The daemon's
  `POST /step/result`, `POST /jobs/{wi}/waive` and `POST /jobs/{wi}/rewind`
  are gone, and so are Pi Web's continue, cancel and send-back controls on
  the job card with their `/api/polyforge/{cancel,resume,rewind}` routes.
- **Behavior: the capsule.** New: `artifacts/handoff.md`,
  `runtime-state/step-calls.jsonl`. Gone: `pending-results/`,
  `artifacts/steps/<id>/s<n>/result.json`, `artifacts/integrate/pool.json`
  (the integrate commands compute the pool through `pf step hub list` and
  `get`), `runtime-state/effects.json` and `artifacts/grill-qa.md`'s
  `## Owner rulings`. `runtime-state/execution.json` is the daemon's
  private record: no step or script reads it, and it no longer holds
  `output`, `pending_rewind_input`, the step records (but a gate FAIL's
  commits and the waivers), `envelope_rejects`, `owner_rulings`,
  `effects`, `integration` or `integration_batch`. At the wrap the daemon
  merges the `wrap-attrs` into the work item's attrs and writes
  `attrs.waivers` (every waiver: the owner's words verbatim, the agent's
  reading, the time), which the integration lane now reads.
- **Behavior: removed configuration.** `.polyforge.yaml`'s
  `steps.<project>.<step-id>.attrs` is refused naming its removal, `pf
  config set` writes no attr, the daemon no longer reports the
  `steps_attrs` capability and Pi Web's settings page has no attrs editor.
  `[daemon] max_concurrent_bars_per_repo` goes with the bar lock: a
  leftover is refused as an unknown `[daemon]` key, as a leftover attrs
  override is refused naming its removal — remove both before the deploy.
  `[daemon] verify_container_*` is unchanged.
- **Behavior: the shipped lanes.** Every lane declares `contract: 4`; the
  step definitions carry no output schema; every skill says what its step
  reads and verifies first and what its hand-off carries; release's
  deploy hands off `done` where it delivered `defer`; a step's old `retry`
  is `rewind --to` itself; develop's and micro's merge declare
  `wrap_attrs: [integration]` and integrate's close `wrap_attrs:
  [integration_batch]`; `_shared/step-header.md`, `_shared/handoff.md` and
  the `{{recall}}` fragment (`_shared/recall.md`) are new,
  `_shared/review_reentry.md`, `_shared/memory.md`,
  `_shared/memory-wrap.md` and `_shared/attr_question_batch_size.md` are
  gone.
- **Why.** The daemon only orchestrates (owner, 2026-11-10): it judged
  envelopes, owned the integration lane's pool, effects and hand-offs and
  rendered some twenty prompt sections; contract 4 gives a step one entry
  and one document, keeps what programs read structured, and leaves the
  rest to the step's own skill.
- **Action: rewrite each user lane before the deploy** —
  1. `contract: 4`;
  2. delete `inputs`, `verifies`, `outputs`, `verdicts`, `attrs` and
     `on_fail.charge`, and move what they said into the step definition
     or the skill (what to read and check first, what to hand off);
  3. delete `output:` from every `steps/<id>.md`;
  4. replace `effects` and `handoff` with `pf step hub` calls in the skill
     and list those verbs in the step's `hub:`; a record the wrap must
     carry is a `wrap-attrs` the step declares in `wrap_attrs: [<name>]`;
  5. delete `bar` from `resources.locks`;
  6. replace `pf result submit`, `pf ask`, `pf recall` and `pf remember`
     in the definitions and skills with `pf step handoff`, `pf step ask`,
     `pf step recall` and `pf step remember`;
  7. `pf workflows validate --workspace <dir>` names every defect left.
- **Action: drain, then deploy (drain #3).** Stop claiming (`pf ops auto
  off`) and let the work items in flight finish. Remove
  `steps.*.attrs` from every `.polyforge.yaml` and
  `max_concurrent_bars_per_repo` from `cli.toml`, and `pf result
  submit`, `pf waive` and `pf rewind` from your scripts. Deploy, re-run
  `tools/install.sh` on each machine (the `pf` binary and Pi Web) and run
  `/plugin marketplace update` where the polyforge skill is installed.
  A work item whose capsule snapshot targets contract 3 parks blocked,
  kind `contract`: cancel it and file it anew; a queued work item is
  unaffected (its claim takes the new snapshot). `pf ops auto on`.
  Rollback: the previous binary with its contract-3 lanes; a capsule
  claimed under contract 4 parks blocked, kind `contract`, under it.

## Workflow contract 4: the bar lock's and the step attrs' remaining faces and `--retry-backoff-base` are deleted

The code behind the configuration the workflow contract 4 section above
removed is deleted; no lane changes.

- **Behavior: the heavy-run admission has two gates.** A dispatch of a
  step declaring `resources: { admission: heavy }` waits only for a free
  slot (`max_concurrent_bars`) and the load (`bar_load_threshold`); its
  queue position is the admission order — priority, then first queued,
  then arrival. The per-repo bar lock and its reservations are gone, and
  with them the watch snapshot's `bar_wait` (`pf watch --json`), the `bar
  wait:` line of `pf watch --once`, the `waiting its turn for the <repo>
  bar lock` queue reason and Pi Web's read-only Host row for
  `max_concurrent_bars_per_repo`.
- **Behavior: no step attrs anywhere.** `pf workflows` lists no `attrs`
  per step and `pf config workspace` none per step entry; `pf config
  set|unset steps.<project>.<step-id>.attrs.<name>` is refused as an
  unknown workspace key. A `steps.*` write — and `pf workflows`'
  `write_capable` — needs the running daemon to answer and name its
  workflows dir (`CORE-501` otherwise); `GET /status` carries no
  `capabilities` list. The `attr_override_ignored` event is gone.
- **Behavior: `pf daemon --retry-backoff-base` is removed.** The
  test/dev-only flag paced nothing under contract 4's supervision rules
  (a transient death backs off 1 s, doubling to at most 3 min); a launcher
  still passing it fails the daemon's flag parse.
- **Action: none beyond the contract 4 section's drain.** Remove the two
  configuration keys as that section says, and drop `--retry-backoff-base`
  from any test or dev launcher. Rollback: the previous binary.

## Workflow contract 4: execution.json keeps no gate FAIL's commits and no waivers; `failed_rewind_exhausted` is gone (a correction)

The workflow contract 4 section above says `runtime-state/execution.json`
no longer holds the step records "but a gate FAIL's commits and the
waivers". It keeps neither. No lane and no configuration changes.

- **Behavior: where those facts are.** A gate's FAIL — like every
  hand-off — is its section of `artifacts/handoff.md`, whose header the
  daemon stamps with each task repo's start and end commit of the step's
  round, and its record in the call ledger
  (`runtime-state/step-calls.jsonl`). The waivers are the call ledger's
  `waive` hand-offs with the owner messages they cite
  (`runtime-state/asks.jsonl`); the wrap writes them to the work item's
  `attrs.waivers`, which the integration lane reads.
- **Behavior: the workflow status `failed_rewind_exhausted` is gone.** No
  daemon since v0.2.7 writes it (a spent rewind budget is refused at the
  gate's hand-off; nothing ends on it). The decision table (`pf watch`)
  and Pi Web's job card no longer know it: a capsule a v0.2.6 or older
  daemon ended with it reads as an unknown status — `pf watch --once`
  prints the raw word when no hub status is at hand, and the card says no
  final status instead of finished. Its work item's hub status is still
  `failed`.
- **Action: none.**

## Step scripts: the daemon needs Python 3.9 or newer; four more step variables; the result file's schema

The groundwork for the workflow's commands moving into Python scripts the
lanes carry (docs/reference/workflow-contract.md, "Scripts"). No lane and
no step changes, and no shipped step runs a script yet.

- **Behavior: the daemon checks its Python at the start.** It resolves the
  interpreter the step scripts run under once — the new `[daemon] python`
  key (Host-only, restart-only, a quoted absolute path), else `python3` on
  the daemon's own `PATH` — and asks it for its version. A missing one, one
  that does not answer or one older than 3.9 refuses the start (exit 78),
  naming the source, the path and the version. `tools/install.sh` already
  required `python3`, but not its version: a host whose `python3` is 3.8 or
  older no longer starts the daemon.
- **Behavior: four more step variables.** Every step process also carries
  `PF_CAPSULE` (the capsule root), `PF_LANE_DIR`
  (`<capsule>/workflows/<lane>`), `PF_SHARED_DIR`
  (`<capsule>/workflows/_shared`) and `PF_PYTHON` (that interpreter); a
  step's own copy of any of them never reaches it.
- **Behavior: `pf doctor` has a `python3` row**, judging the same rule from
  the shell it runs in: `fail` on the finding that refuses the daemon.
- **Behavior: the result file's JSON Schema is published** as
  `workflows/_shared/scripts/schema/pf-step-result-1.json`, with the
  scripts' shared library `workflows/_shared/scripts/pfstep/`; both ride
  every capsule's lane snapshot. The Go step commands' result files are
  unchanged and satisfy the schema.
- **Action: after installing, run `pf doctor`.** When its `python3` row
  fails, install Python 3.9 or newer; when that interpreter is not the
  daemon's `python3` on `PATH`, `pf config set python <absolute path>`.
  Then `pf daemon ensure`. Rollback: the previous binary refuses a
  `cli.toml` carrying `python` (an unknown `[daemon]` key) — `pf config
  unset python` first.

## The scope engine lists Go packages past an unbuilt embed directory; one `tier.reasons` entry per fallback; the doctor counts light runs that ran full

- **Behavior: a light verification no longer falls back to full for an
  unbuilt embed directory.** The engine `tools/verify-scope` lists the Go
  package graph with `GOWORK=off go list -e -test …` and tolerates a
  `//go:embed` pattern that matches no file (`pattern <p>: no matching files
  found`), so a fresh clone whose embedded front-end build output was never
  built still gets a scoped `--light` plan. Every other package error — an
  empty embed directory (`contains no embeddable files`) included — still
  fails closed to the full tier, now diagnosed as `go list failed:
  \`GOWORK=off go list -e -test -f <record format> ./...\` reports a package
  error: <first error>; falling back to the full tier`; a non-zero exit
  still reads `… exited N: <first line>`.
- **Behavior: a changed stack config file is diagnosed under `--light`.** A
  diff touching the analyzer's own config file (e.g. `go.mod`,
  `package.json`) reports `--light: changed path <p> is the <analyzer>
  analyzer's own config file, which it cannot scope; falling back to the
  full tier` instead of a full plan with no reason.
- **Records: `tier.reason` is replaced by `tier.reasons`.** A verify run's
  `result.json` carries `tier.reasons`, a list with one `repos/<name>: <why>`
  entry per repo and fallback reason, in place of the joined `tier.reason`
  string; `report-mechanical.md` renders each entry as its own line under
  "Why the run is full". Only the engine's diagnostics that say `falling
  back to the full tier` are reasons: the informational `--light`
  diagnostics no longer appear there.
- **Doctor.** `pf doctor`'s `scoped-gates readiness` row reads the latest
  verify run of each live capsule of every mapped workspace and, when light
  verifications ran full, appends `the latest light verification of N
  capsule(s) ran full — reasons: <category> ×k, …` (go package graph, stack
  config file changed, carries, conf, no analyzer, analyzer could not
  resolve, git, unplanned, unexplained, other). The note never changes the
  row's status.
- **Action: none** beyond `install.sh`. A script reading `result.json`'s
  `tier.reason` reads `tier.reasons` instead.

## `pf submit` answers the claim's verdict

`pf submit` used to answer `accepted` (and print the placement line) as
soon as the entry was on the daemon's queue; the claim ran a moment later,
and an aihub refusal (e.g. an unmerged blocker, `CORE-409`) showed only as
a later `submit_rejected` event. The submit now waits for its own claim.

- **Behavior: a free slot claims before the answer.** When this machine
  has a free execution slot, the daemon claims the work item at aihub
  before it answers (not after the capsule clone). The human face is
  unchanged — `submitted <slug> (accepted by the local daemon)`, then
  `placement: claimed by machine <machine_id>, capsule <capsule_path>` —
  but the placement line now means the claim really happened.
- **Behavior: a refused claim fails the submit.** An aihub refusal of the
  claim is now `pf submit`'s own error, exit 1, with aihub's typed code
  and extra (`CORE-409`, `extra.reason: DEPENDENCY_BLOCKED` for an
  unmerged blocker): `pf: CORE-409: <slug> was not submitted — aihub
  refused the claim; nothing stays queued: <aihub's detail>`. For an
  unmerged blocker the CLI reads the work item with the caller's own key
  and appends `; waiting for <blocker> (<status>) — wait until the blocker
  is merged (wrapped), then pf submit <slug> again, or remove the edge: pf
  file dep rm <blocker> <slug>`. No `accepted` and no `placement:` line is
  printed and nothing stays on the daemon's queue. The refusal is still
  recorded as the work item's `submit_rejected` event before the submit
  answers, so `pf watch` and the decision feed show it as before.
- **Behavior: a submit that waits says so.** When every slot is busy,
  aihub cannot be reached (the claim is retried with backoff) or the
  daemon is shutting down (the claim replays at its next start), the
  submit still succeeds (exit 0) but prints no placement line; instead:
  ``queued: <reason> — nothing is claimed yet; follow it, and any later
  refusal, with `pf watch <slug>` ``. A refusal at that later claim is
  still the work item's `submit_rejected` event, as before.
- **Behavior: the `--json` answer gains `state` and `queued_reason`.**
  `state` is `claimed` or `queued`; `queued_reason` rides a `queued`
  answer only. `machine_id` and `capsule_path` ride a `claimed` answer
  only (and still only when the placement is known); `missing_repos` is
  unchanged. `accepted` is always true on a successful answer; a refusal
  is the error envelope, never this answer. The shape changes in place —
  no dual shape, no compatibility shim: a parser that read `machine_id`
  as "will be claimed" reads `state` instead, and a script that treated
  exit 0 as "queued, maybe refused later" now sees the refusal as exit 1.
- **Action: none beyond the `pf` binary.** No schema change, no aihub
  change; the upgrade is the `pf` binary alone (the daemon answers the
  new verdict once it runs the new binary).
## Develop's verification runs the workflow script verify.py; the step command pf-verify is gone

The light verification moves into the lane: the workflow script
`workflows/_shared/scripts/verify.py`, which the verify skill runs from the
capsule's lane snapshot as `"$PF_PYTHON" "$PF_SHARED_DIR/scripts/verify.py"
--tier light --unit-runs artifacts/build/units.json --json --out
artifacts/verify/result.json` (docs/reference/commands.md, "The
verification bar"). What it checks, its exit codes and its result file's
`details` are the step command's.

- **Behavior: `pf-verify` is gone.** pf no longer dispatches the name,
  `tools/install.sh` no longer links it and `pf doctor`'s `step commands`
  row no longer looks for it; a `pf-verify -> pf` link an earlier install
  left is pf itself (remove it by hand). The verify skill declares
  `commands: [verify.py]`.
- **Behavior: the result file names the script.** Its `command` is
  `verify.py` and its `version` the script library's (`pfstep/1`);
  `pf-merge --verified` reads only its `heads`.
- **Behavior: where a repo's bar runs is unchanged:** in its verification
  container when its HEAD carries `.polyforge/Dockerfile.verify`, else on
  the host under the step's command `PATH`.
- **Behavior: that container's limits are the script's own:** CPUs
  min(8, the cores), memory 8g, pids 4096. `[daemon] verify_container_*`
  no longer reach the light verification (they still bound `pf-run` and
  `pf-gate`).
- **Behavior: a unit's `marker` is a Python regular expression**
  (`re.ASCII`, searched in each output line), Go's RE2 before
  (docs/reference/workflow-contract.md, "Scripts"); an empty, blank or
  empty-matching marker still fails its unit unrun.
- **Behavior: a nested engine root's full-tier fallback makes the run
  full.** A nested root (`pi-web/`) whose own light report fell back to the
  full tier ran its full plan under `tier.effective: light`; the run is now
  `full`, a `tier.reasons` entry naming the root and why.
- **Behavior: the scope engine** is `$PF_SHARED_DIR/scripts/verify-scope`
  when the shared tree carries one, else the installed release's
  `tools/verify-scope` as before.
- **Action: drain develop's verification.** A work item claimed before the
  upgrade carries the old verify skill in its snapshot: its `pf-verify`
  line no longer runs and the step hands off `blocked`; `pf resume`
  re-claims it with the lane as it is now. Rollback: the previous release;
  a work item claimed under this one keeps `verify.py` in its snapshot.

## sync, merge, revert and run are the workflow's step scripts

- **Behavior: `pf-sync`, `pf-merge`, `pf-revert` and `pf-run` left pf.** They
  are the workflow's own scripts, `workflows/_shared/scripts/sync.py`,
  `merge.py`, `revert.py` and `run.py`, which the develop, micro and
  integrate skills run from the lane snapshot as `"$PF_PYTHON"
  "$PF_SHARED_DIR/scripts/<name>.py" …`, each line written out in full: the
  same flags (`--flag` forms only), exit codes, result file
  (`pf-step-result/1`), details and side files (`syncing.json`,
  `revert-plan.json`, `--landing-out`, `--ops-out`). A result file's
  `command` is the script's file name and its `version` `pfstep/1`;
  `pf-integrate close` reads the revert step's result with `command`
  `revert.py`. Their git is pfstep's hardened call: no hook runs, and a
  merge or revert commits as `polyforge daemon <daemon@polyforge.invalid>`.
- **Behavior: `run.py` takes `--isolation container|host`, required.** micro's
  evidence step now runs its plan on the host (`--isolation host`); a
  container run of a repository without `.polyforge/Dockerfile.verify` is
  exit 2, never the host. A container run's limits are the script's own
  defaults (cpus min(8, cores), memory 8g, pids 4096), never `cli.toml`'s
  `[daemon] verify_container_*` (those stay `pf-verify`'s and `pf-gate`'s).
- **Behavior: the step after one checks the result file's `argv`.** develop's
  verification and micro's evidence take sync's result only when its
  `argv` is the sync skill's start or continue line, merge takes the
  verified result only when its `argv` is its gate's line, and full-gate
  takes revert's only when its `argv` is the revert skill's line for the
  round's ids: a result written by any other line is a hand-off mismatch
  (that gate's FAIL).
- **Install and doctor.** `tools/install.sh` links only `pf-facts`,
  `pf-gate` and `pf-integrate`; `pf doctor`'s `step commands` row checks
  those three. A `pf-run`, `pf-sync`, `pf-merge` or `pf-revert`
  link an earlier install left is now `pf` itself (its usage, exit 2):
  remove it (`rm "$(dirname "$(command -v pf)")"/pf-{run,sync,merge,revert}`).
- **Action: drain before installing.** A capsule claimed before the upgrade
  carries the old skill lines (`pf-sync …`), which the new `pf` no longer
  answers: stop claiming (`pf ops auto off`), let the develop, micro and
  integrate work items in flight finish, then install, as for a contract
  version. Rollback: drain the same way, then install the previous release
  (its `install.sh` links the four commands again).

## Step scripts: facts.py and integrate.py replace pf-facts and pf-integrate; the scope engine moves into the workflow

The develop lane's review, the micro lane's bounds and the integrate lane's
collect and close now run the workflow's own scripts instead of two `pf`
step commands, and the verification-scope engine moves into the workflow
beside them (docs/reference/commands.md, "Workflow scripts").

- **Behavior: the skills run the scripts.** `"$PF_PYTHON"
  "$PF_SHARED_DIR/scripts/facts.py" <kind> …` and `… integrate.py collect`
  / `close …` take the place of `pf-facts` and `pf-integrate`: the same
  flags (`--flag` form), exit codes, result files and details; the result
  file names the script (`command` `facts.py` / `integrate.py`, `version`
  `pfstep/1`), as verify.py's does. `integrate.py` reads the
  integration pool through `pf --json step hub list` / `get` with the `pf`
  on the step's `PATH`, and `integrate.py close` reads the revert step's
  result as `pf-integrate close` did since the section above (`command`
  `revert.py`). `pf-facts` and `pf-integrate` are gone: `pf` no longer
  dispatches the names, `tools/install.sh` links only `pf-gate`, and
  `pf doctor`'s `step commands` row checks that one.
- **Behavior: review replays on the host.** The review skill's line is
  `facts.py build --replay --isolation host`: each unit's run replays on
  the host under the step's command `PATH` even when the repository
  declares a verification container (before: in that container). A replay
  run in a container (`--isolation container`, or no flag on a repository
  that declares one) takes the scripts' container limits — CPUs min(8,
  cores), memory 8g, 4096 pids — never `cli.toml`'s `[daemon]
  verify_container_*`.
- **Behavior: the engine's place.** `tools/verify-scope` and its
  dependency-audit helper `tools/audit-deps` are now
  `workflows/_shared/scripts/verify-scope` and `audit-deps`, unchanged. A
  step finds the engine in its lane snapshot,
  `$PF_SHARED_DIR/scripts/verify-scope` (read-only there: run it with
  `sh`) — `verify.py`'s engine is always that one now, its fallback to the
  installed release's `tools/verify-scope` gone; a hand run uses the
  release checkout's
  `<data-dir>/release/current/workflows/_shared/scripts/verify-scope`; the
  test gate plans with the gated tree's own
  `workflows/_shared/scripts/verify-scope`.
- **Behavior: `pf doctor` has no `scoped-gates readiness` row.** Its check
  is the workflow's `scope_ready.py --repo <clone>` (exit 0 ready, 1 not
  ready, 2 no clone); the doctor verb's page shows the line. The note the
  row carried about the latest light verifications that ran full (the
 section above) is a row of its own, `light verification fallbacks`,
  with the same words; it never moves the status.
- **Action: drain, then install.** A work item claimed before the upgrade
  runs its lane snapshot's old lines (`pf-facts`, `pf-integrate`), which
  this `pf` no longer has: let the develop, micro and integrate work items
  in flight finish (or cancel them) before installing. After
  `tools/install.sh`, remove the two leftover links beside `pf` —
  `rm "$(dirname "$(command -v pf)")"/pf-facts "$(dirname "$(command -v
  pf)")"/pf-integrate` — and change any hand-run command line or script of
  your own that names `tools/verify-scope` or `tools/audit-deps` to the new
  path. Rollback: the previous release's `tools/install.sh` (it relinks
  `pf-facts` and `pf-integrate`).

## The step agents' language: `[daemon] agent_language`, default English

A new Cell key picks the language of the daemon's step agents: `en`
(English, the default when the key is absent) or `zh-CN` (Simplified
Chinese). No lane changes.

- **Behavior: after the upgrade the step agents work in English.** The
  key is absent on an upgraded machine, so it reads `en`: a newly
  dispatched step is told to work in English (its replies, its questions
  to the owner, the prose it writes and its hand-off body), and every
  label the daemon stamps on `artifacts/handoff.md` — the opening, each
  section's header, the rewind and close events — is English where it
  was Chinese. The hand-off body's seven headings and the re-entry
  heading come in both languages (`workflows/_shared/handoff.md`);
  `pf step handoff` checks a body against the set of the language its
  dispatch was told and refuses the other set.
- **Behavior: the value is fixed per dispatch.** `pf config set
  agent_language <en|zh-CN>` takes effect live, for the next new
  conversation; a running step, and a step resuming its conversation,
  keeps the language it was told. A step dispatched by the previous
  binary carries no recorded language and is checked and stamped as
  `en`: if it writes Chinese headings, its hand-off is refused — it
  reads the refusal and hands off again under the English headings. A
  hand-off document whose language changed midway carries sections of
  both.
- **Behavior: Pi Web's Agent language setting writes the key.** Saving
  the Agent language in Pi Web's settings also writes `[daemon]
  agent_language` of the default Cell through `pf config set` (when the
  installed `pf` knows the key); a refused write changes neither. The
  sync is one way: `pf config set` does not change Pi Web's setting. A
  Pi Web already set to 中文 is not carried over by the upgrade: the key
  stays absent (`en`) until its Agent language is saved again, or until
  `pf config set agent_language zh-CN` runs.
- **Behavior: a work item claimed before the upgrade keeps its old
  template.** Its capsule holds the snapshot of
  `workflows/_shared/handoff.md` taken at the claim, with the Chinese
  headings only, while its new dispatches default to `en` and are checked
  against the English headings. Set `zh-CN` right after install to keep
  such work items consistent; otherwise its step reads the refusal, which
  names the English headings, and hands off again under them.
- **Action: to keep Chinese, set the key right after install.**
  Run `tools/install.sh` on each machine (the `pf` binary and Pi Web),
  then at once `pf config set agent_language zh-CN` (per Cell: `pf
  --profile <name> config set …`; the previous binary refuses the unknown
  key, so it cannot be set ahead) — it applies live, and a step
  dispatched between the two runs in English. Nothing to do to take
  English. Rollback: the previous binary refuses the key in `cli.toml` —
  `pf config unset agent_language` first.

## The full test gate is the workflow script gate.py; the step command pf-gate is gone

The test gate moves into the workflows: the script
`workflows/_shared/scripts/gate.py`, which the integrate lane's full-gate
skill runs from the capsule's lane snapshot as `"$PF_PYTHON"
"$PF_SHARED_DIR/scripts/gate.py" --isolation container --capsule . --pins …`
and `.polyforge/release.sh` and `tools/green-tag` run from the released
tree with `python3` (docs/reference/commands.md, "The full test gate";
docs/reference/release.md, "The test gate"). Its four modes, its exit codes
and its result files' `details` are the step command's; its plan comes from
the tree's own `workflows/_shared/scripts/verify-scope`.

- **Behavior: `pf-gate` and `cmd/pf-gate` are gone, and with them the last
  step command.** pf dispatches no `pf-<name>` any more, `tools/install.sh`
  links none and `pf doctor`'s `step commands` row has nothing to check
  (the dispatch, the link loop and the row go with); a
  `pf-gate -> pf` link an earlier install left is pf itself (remove it by
  hand). The release no longer builds the gate with `go`: its test gate
  needs `python3` ≥ 3.9 on the release host, which its preflight checks
  (an older one is refused before any gate runs). The full-gate skill
  declares `commands: [gate.py]`.
- **Behavior: `--isolation container` is required and the only isolation a
  gate has.** A gated tree without `.polyforge/Dockerfile.verify` at HEAD,
  or a machine whose docker engine does not answer, is not judged (exit 2,
  the reason said) — never run on the host.
- **Behavior: the gate's container limits are the script's flags** —
  `--cpus` / `--memory` / `--pids`, default min(8, the cores) / `8g` /
  4096. The machine's `[daemon] verify_container_*` keys no longer reach
  the gate (design r2 D7) — nor anything else: every step command that
  read them is a workflow script with its own limits. A machine that set
  them to bound the release's or the integrate lane's gate passes the
  flags on the gate's command line instead; the keys go with
.
- **Behavior: the gate's result files and green tags name the script.** A
  result file's `command` is `gate.py` (its `version` the script
  library's, `pfstep/1`) and a green tag's `runner` is `gate.py`, its
  `runner_version` `2`: the plan digest is the sha256 of the plan's
  compact JSON as the script encodes it. A green tag the Go runner wrote
  (`pf-gate`, `1`) is never reused, so the first release after the upgrade
  runs the gate once more (or reuses a tag the integrate lane's next green
  batch writes). `integrate.py close` reads only `gate.py` result files:
  no `pf-gate` result survives the upgrade's drain.
- **Action: drain the integrate lane's full-gate.** A batch claimed before
  the upgrade carries the old full-gate skill in its snapshot: its `pf-gate`
  line no longer runs and the step hands off `blocked`; `pf resume`
  re-claims it with the lane as it is now, and its gate runs again as
  `gate.py`. Rollback: the previous release; a batch claimed under this one
  keeps `gate.py` in its snapshot, and a green tag `gate.py` wrote is not
  reused by the previous release either.

## Integration batches only by hand: the daemon's integrate trigger is gone

The daemon's automatic integrate trigger (the section "The daemon files
the integration batch itself after a quiet period" above) is deleted. An
integration batch is started only by a person, with `pf ops integrate
--project <p>`, which files and submits it as before; the integrate lane
and its steps are unchanged.

- **Behavior: the daemon files no batch of its own.** No daemon decides,
  files or submits an integration batch, and none submits a batch its
  filer left queued. Landed work items wait in the pool (`pf ops
  integrate status`) until someone runs `pf ops integrate`.
- **Behavior: a leftover `integrate:` section is refused.** The workspace
  `.polyforge.yaml` takes no `integrate:` section any more
  (`integrate.<project>.auto`, `integrate.<project>.quiet`). A file still
  carrying one — whatever its entries — is refused whole, the reason
  naming the removal and saying to delete the section: every claim and
  dispatch of the workspace's projects is refused `DAEMON-414` (auto-pull
  skips them; a step about to start blocks), and `pf doctor`'s `workspace
  yaml` row fails.
- **Behavior: the trigger's faces are gone.** `GET /status?integration=1`
  carries no per-project `trigger` block; `pf doctor` has no `integrate
  trigger` row; no `integrate_trigger` event is written (the old ones stay
  in `events/daemon.jsonl`); the watch snapshot has no `batch` block and
  `pf watch --once` prints no `batch:` line. A batch the trigger filed
  earlier (label `integration-auto`) reads as any other batch.
- **Behavior: the refusal names the open batch.** `pf ops integrate`
  refuses while another batch of the project is unfinished, naming it and
  its status. For a batch still queued that no machine ever claimed (its
  filer stopped between filing and submitting it; the trigger used to
  submit such a batch) the refusal adds when it was filed and the way
  out: `pf submit <batch>` runs it, `pf cancel <batch>` drops it.
- **Action: delete the `integrate:` section right after installing.**
  Run `tools/install.sh` on each machine, then at once delete the whole
  `integrate:` section from every workspace `.polyforge.yaml` the daemon
  reads (`pf doctor`'s `workspace yaml` row names any left). Not before:
  the previous binary reads an absent section as the trigger on, so it
  would file batches until the upgrade. A work item a step start blocked
  in between (`DAEMON-414`) continues with `pf resume` once the section is
  gone. From then on, run `pf ops integrate --project <p>` when landed
  items wait.
- **Action: rollback.** Install the previous binary, then at once add
  `integrate: {<project>: {auto: false}}` per project to keep its trigger
  off (this binary refuses the section, so not before).

## The Go step-command machinery is gone; `[daemon] verify_container_*`, the daemon's container sweep and three step variables go with it

Every command a workflow step runs is the workflow's own script
(`workflows/_shared/scripts`), so what pf kept for its own step commands is
deleted: the step command package with its argv[0] dispatch, the
verification runner and its container executor, the merge library, the
container keys of `cli.toml`, the daemon's container sweep and the doctor
rows that served them (docs/reference/commands.md, "The commands a step
runs").

- **Behavior: pf answers to no step-command name.** The binary runs the
  same whatever name it is started under. `tools/install.sh` removes the
  `pf-run`, `pf-verify`, `pf-gate`, `pf-sync`, `pf-merge`, `pf-revert`,
  `pf-facts` and `pf-integrate` links an earlier install laid beside `pf`
  (a symlink of that name pointing at `pf`; any other file of that name is
  the user's own and left alone): nothing to remove by hand. `pf doctor`
  has no `step commands` row.
- **Behavior: `[daemon] verify_container_cpus`, `verify_container_memory`
  and `verify_container_pids` are gone.** Nothing read them since the
  scripts took their container limits as flags (design r2 D7). A
  `cli.toml` that still sets one is refused as an unknown key — `pf config`
  refuses the file and the daemon does not start: delete these lines before
  installing (`pf config unset verify_container_cpus`, and the same for
  `verify_container_memory` and `verify_container_pids`, on the old
  binary). `pf config list` and Pi Web's config panel no longer show them.
- **Behavior: the daemon knows no container.** `pf doctor`'s `verify
  container engine` and `verify container paths` rows are gone (a script
  that needs docker and finds none exits 2 and says so), and the daemon's
  start no longer sweeps leftover verification containers. A container
  carries the script's own owner label — `pf.verify.daemon=pf-step`,
  `gate.py`'s `pf-gate` — and a fresh `pf.verify.boot` per run (the work
  item, run, repo and `pf.verify.expires` labels as before); a container a
  killed script left ends by itself five minutes after its deadline, and
  the next run of the same owner removes every expired one. A container an
  earlier release labelled with a Cell's data dir ends at its own
  keep-alive as well.
- **Behavior: a step process's `PATH` carries the daemon's command PATH;
  no `PF_COMMAND_PATH`, `PF_VERIFY_DAEMON` or `PF_VERIFY_BOOT`.** With
  `[daemon] command_path` set, a step starts with `PATH` = the directory
  of the daemon's own `pf` executable (symlinks resolved), then the
  directory of its harness binary as the daemon resolves it on its own
  `PATH`, then the key's entries, each entry once in that order — so a
  value that lacks them (config.md's example
  `/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin` on a machine whose
  `pf` is `~/.local/share/polyforge/bin/pf`, or whose `pi` and its `node`
  come from nvm) loses the step neither `pf step …` nor the harness's
  interpreter. Unset, nothing changes: the step inherits the daemon's own
  `PATH`, as before. The scripts read no variable of their own for it,
  and the release lane's lines run `.polyforge/release.sh` and
  `.polyforge/deploy.sh` without the `PATH="$PF_COMMAND_PATH"` prefix.
  With the key set, the step agent's own commands and the harness run
  under that `PATH` too. `GET /status` adds `step_path` (that `PATH`
  before the harness's directory), and `pf doctor`'s `daemon command
  path` row shows it and checks that `pf` resolves under it beside the
  engine's tools.
- **Behavior: `gate.py --triage` checks its `--batch`.** The file must be
  `integrate.py collect`'s result (`command` `integrate.py`); another
  command's result file is a usage error (exit 2).
- **Behavior: the script library.** `pfstep.command` merged into
  `pfstep.invoke` (one invocation helper: the result's heads, summary,
  details and logs are the invocation's own fields), the step's hub reads
  are `pfstep.hub` (shared by `gate.py` and `integrate.py`), and
  `pfstep.container.sweep_stale` (the daemon's start-up rule) and
  `pfstep.stepenv.command_path` / `command_env` are gone. A lane's own
  script that imported them imports `pfstep.invoke` and reads `PATH`.
- **Action: remove the `verify_container_*` lines, drain the release lane,
  install.** A release work item claimed before the upgrade carries the
  old release, verify and deploy steps, whose lines prefix
  `PATH="$PF_COMMAND_PATH"` — empty after the upgrade: let it finish (or
  cancel it) before installing. Nothing to change in `command_path`: the
  daemon puts its own `pf` directory and the harness binary's in front of
  it, because a step needs both whatever the key holds. Develop, micro and integrate work items in
  flight keep working: their snapshot's scripts read `PF_COMMAND_PATH`,
  else `PATH`, which is now the same value. No schema change, no aihub
  change, the contract version stays. Rollback: the previous release; its
  daemon accepts the `verify_container_*` keys again (none is needed) and
  sweeps its own containers at start; a release work item claimed under
  this one runs its procedures under the step's own `PATH`, which the
  previous release leaves the daemon's own `PATH` even when
  `command_path` is set.

## The integration lane's read-only faces are gone

The daemon no longer serves views that exist only for the integration
lane. An integration batch is a work item like any other, followed the
same way; the integrate lane and its steps, `pf ops integrate --project
<p>` and `pf ops integrate --clear-hold` are unchanged, and the workflow
contract is not touched.

- **Behavior: no `pf ops integrate status`.** The subcommand is gone:
  `pf ops integrate status` is refused as an unexpected argument and asks
  the daemon nothing. An older `pf` still carrying it, run against the new
  daemon, prints an empty pool (`integration: no batch running` and no
  pool line; `--json`: empty `projects` and `batches`), because it reads
  the absent `integration` block as an empty one — not a sign that nothing
  landed. Follow a batch with `pf watch <batch> --once` (or its row in
  `pf ops status`); the landed items are aihub's records (wrapped,
  `integration.state` landed).
- **Behavior: `GET /status` has no `integration` block.** `?integration=1`
  is a query parameter nothing reads: the answer is the plain status, on a
  Cell and on the Host aggregate alike, and the daemon reads no pool from
  aihub for it.
- **Behavior: the watch snapshot has no `integration` block.**
  `pf watch <wi> --once` prints no `integration:` line and its `--json`
  carries no `integration` object. The landing record itself is unchanged:
  the work item's aihub attrs `integration`, written at its wrap.
- **Action: none beyond `tools/install.sh`.** Pi Web read none of these
  faces. A script that read `pf ops integrate status` or
  `GET /status?integration=1` reads the work items at aihub instead.
  Rollback: the previous binary.

## A step's last stdout records reach its exit's class: stdout gets stderr's catch-up rule

Nothing to migrate. After a step's harness process exits, the daemon
reads both of its pipes by one rule before it announces the exit and
classifies it.

- **Behavior: stdout is read until it caught up, not for 500 ms.** The
  stdout reader used to get at most 500 ms after the process exited to
  reach EOF. On a loaded host the harness's last record — the error
  turn's end, written as its last act — could still be unread then, and a
  provider error (a model not found) was archived as `exit_other`: the
  step retried the same model instead of switching to the next
  candidate. The reader now has its own bound of 5 s, and the exit is
  announced as soon as a read started after the exit finds the pipe
  empty, every complete record by then through the supervisor and the
  driver. A descendant that merely keeps the pipe open (a provider
  connection, a spawned tool) costs nothing; the exit waits up to the
  bound only while the reader cannot keep up or a descendant floods
  stdout after the exit.
- **Behavior: stderr follows the same rule (shipped earlier, recorded
  here).** The stderr copy that feeds the exit's stderr tail has its own
  bound of 5 s by the same empty-read rule.
- **Behavior: a missed bound is recorded, never classified.** A pipe
  whose reader had not caught up when its bound passed leaves a daemon
  log line (`… its stdout reader had not caught up 5s later …`, `… its
  stderr copy had not caught up 5s later …`) and a note in the
  dispatch's archived failure detail: `[stdout records incomplete: its
  reader had not caught up with the exit]`, `[stderr tail incomplete:
  its copy had not caught up with the exit]`. The failure classes and
  their rules are unchanged: the exit is classified from what had
  arrived, and the note matches no class.
- **Action: none.** Rollback: the previous binary (its stdout bound is
  500 ms again).

## Workflow contract 5 (part 1): a lane is workflow.yaml and README.md

The workflow contract moves to version 5. Its first part is the lane's
file format (docs/reference/workflow-contract.md, "The lane"); contract
5's other changes are the sections that follow this one, deployed by the
same release and the same drain.

- **Behavior: a lane is its `workflow.yaml`.** The one lane loader reads
  `workflows/<lane>/workflow.yaml` — one YAML mapping, contract 4's
  `workflow.md` frontmatter with `contract: 5`, the same keys — and nothing
  else of the lane's own; the lane's prose is its `README.md`, which the
  daemon never reads. A lane that is still a `workflow.md` is refused with
  its rewrite: alone it is `WFC-101` (naming the contract its frontmatter
  declares), beside a `workflow.yaml` it is `WFC-110`. A `workflow.yaml`
  holding more than one YAML document (a frontmatter's closing `---` left
  in) is `WFC-110`.
- **Behavior: `vars_files`.** A step may read a var from a file of its
  lane: `vars_files: { judge: fragments/review-judge.md }`. The path is
  relative to the lane directory and stays inside it (no `..`, no absolute
  path, no symbolic link); the file's text is the var's value, exactly as
  if it were written in `vars`. A var declared in both is `WFC-112`.
  develop's review reads its `intro` and `judge` this way.
- **Behavior: the lane schema.** `workflows/_schema/workflow-5.schema.json`
  is the JSON Schema of a `workflow.yaml`, generated from the contract
  registry; each shipped lane's first line,
  `# yaml-language-server: $schema=../_schema/workflow-5.schema.json`,
  points an editor at it. The loader stays the judge (`pf workflows
  validate`).
- **Behavior: the snapshot.** A claim copies the whole lane directory into
  the capsule as before — `workflow.yaml`, `README.md`, `fragments/` and
  `steps/`. Pi Web's job cards read a capsule's declared step order from
  its `workflow.yaml`; a capsule claimed before the upgrade shows its steps
  in the order they were observed.
- **Action: drain.** In-flight work items cannot cross the upgrade: stop
  claiming (`pf ops auto off`), let every running work item finish on the
  old daemon, then install. A work item left in flight keeps its
  `workflow.md` snapshot and parks blocked, kind `contract`, at its next
  start or resume: cancel it and file it again. The built-in lanes
  (develop, micro, integrate, release) ship converted with the release.
- **Action: rewrite each user lane** (`<workspace>/.polyforge/workflows/<lane>/`):
  the frontmatter between the `---` fences becomes `workflow.yaml`, its
  `contract: 4` now `contract: 5`; the body becomes `README.md`;
  `workflow.md` is deleted. Every key keeps its name and value, save a
  step's `hub:` list, which contract 5 makes a mapping (part 2: `WFC-112`
  until rewritten). In the lane directory:

      awk 'NR==1&&$0=="---"{f=1;next} f&&$0=="---"{exit} f' workflow.md | sed 's/^contract: 4$/contract: 5/' > workflow.yaml
      awk 'b{print} NR>1&&!b&&$0=="---"{b=1}' workflow.md > README.md
      rm workflow.md && pf workflows validate .

  A long var may move to a file of the lane: its block's text (dedented)
  into `fragments/<name>.md`, the var out of `vars` and into
  `vars_files: { <var>: fragments/<name>.md }`. `pf doctor`'s `workflow
  lanes` row names every user lane still a `workflow.md`.
- **Rollback:** the previous release reads only `workflow.md`: restore each
  user lane's `workflow.md` (and delete its `workflow.yaml`) before
  installing it, and drain again.

## Workflow contract 5 (part 2): generic hub writes, aihub keeps no lane's rules

The second part of contract 5 (the same release and the same drain as
part 1). A step's aihub writes become one generic verb, and aihub drops
the integration lane's routes and rules: what a lane's record may hold,
and how it moves, is now the lane's scripts' to judge
(docs/reference/workflow-contract.md, "Hub verbs").

- **Behavior: `pf step hub attrs` replaces `move` and `clear-hold`.**
  `pf step hub attrs <wi | key:K> --set <key> <json | @file> [--if
  <path>=<value>]... [--per-owner <at>]` replaces one top-level attrs key
  of a work item whole (`null` deletes it). Each `--if` fences the write:
  it is made only while the attr at the dot path is that string (in a
  `batch` op, `if` maps a path to any JSON value; an absent path reads as
  `null`), else aihub refuses it `CORE-409` `ATTRS_IF_MISMATCH` and
  nothing is written. A batch op is `{"verb": "attrs", "target", "name":
  <key>, "value", "if"?, "per_owner"?}`. `--per-owner` names the owner
  message to the step in its round, checked as a hand-off's is; the
  daemon copies its words, writer and stamp verbatim to aihub with the
  write. `pf step hub move` and `clear-hold` are unknown verbs.
- **Behavior: `pf step hub list --attr <path>=<value>`** (repeatable, all
  must hold) replaces `--integration <state>`: `--attr
  integration.state=landed`.
- **Behavior: a step's `hub:` is a mapping.** `hub: { verbs: [...],
  attrs: [...] }`: `verbs` names the verbs the step may call (`get`,
  `list`, `file`, `wrap-attrs`, `batch`), `attrs` the top-level attrs keys
  it may write — listing one grants `attrs`, and the daemon refuses a
  write of any other key `DAEMON-403`. The list form is `WFC-112`. The
  integrate lane's four steps write `integration`.
- **Behavior (aihub): the integration routes are gone.** `POST
  /api/v1/work-items/{id}/integration` and `.../integration/clear-hold`
  no longer exist, nor does the list's `?integration=<state>` filter (an
  unknown query parameter, ignored). In their place: the signed machine
  route `PATCH /api/v1/work-items/{id}/attrs` (`{key, value, if?,
  owner_message?}`, a project writer's machine; any status, a terminal
  work item included; each write audited as `attrs_written` with the key,
  the signing machine and the owner message) and the list filter
  `?attr=<path>:<value>` (repeatable). A step reaches the route only as a
  pending `pf step hub attrs` write the daemon makes on its own key.
- **Behavior: the integration lane's rules live in its scripts.** One
  module, `workflows/_shared/scripts/pfstep/integration.py`, builds every
  write of the `integration` record from the record the step's hub reads
  now, as the whole record fenced on the whole record as read (a write
  built from a read another write has since changed is refused `CORE-409`,
  so no write drops another's keys): a record moves only `landed` to `landed`, `merged` (on a green tag) or
  `reverted`; the develop lane's `repos`, `merge_commits` and `waived` are
  never rewritten; a record already carrying a move needs no write. Its
  writers: `integrate.py close` (the moves to `merged`), `revert.py
  --ops-out` (to `reverted`; the revert step's hub now names `get`) and the
  new `integrate.py records --in <file> --ops-out <file> [--pins <file>]`,
  which turns a step's record entries — collect's `{item, fix: [ref]}`
  and `{item, clear: <at>}`, full-gate's `{item, blame: [ref]}` — with the
  `file` writes beside them into the hub writes the step registers (exit 1
  when a rule refuses one; nothing written). A hold (a landed record with
  `red_rounds > 0` or a fix ticket) is cleared only on the owner's
  message: `hold_cleared` `{reason: its words, by, at, answered_by,
  answered_at, cleared_in}`, refused while another batch of the lane is
  unfinished (collect reads them with `pf step hub list --bundle`). The
  blaming batch's record no longer gains `holds_cleared` (the clearing
  batch's own record carries the ruling, `hold_rulings`). The records keep
  their shape: no migration. Clearing a hold no longer needs a
  maintainer: the daemon's check of the owner's message is the authority,
  and aihub records it.
- **Action: deploy aihub with this release.** A daemon of the previous
  release cannot write an integration record against this aihub (its
  routes are gone, and its pool read through `?integration=` is no longer
  narrowed), and this daemon's writes fail against the previous aihub
  (no `PATCH .../attrs`: the step parks blocked, kind `write_failed`).
  Drain every machine (part 1), deploy aihub (`.polyforge/deploy.sh`),
  then install this release on every machine before any integrate batch
  runs.
- **Action: rewrite each user lane's `hub:`.** `hub: [get, list, batch]`
  becomes `hub: { verbs: [get, list, batch] }`; a step that called `move`
  or `clear-hold` drops them and lists the key it writes, `attrs:
  [<key>]`, and its skill registers `attrs` writes (the whole record,
  with its `--if` fence) instead. The built-in integrate lane's skills
  (collect, full-gate) write record entries and run `integrate.py records`
  in place of their hand-written `move` and `clear-hold` writes.
- **Rollback:** the previous aihub and the previous release together,
  after a drain; the records written meanwhile keep the previous shape,
  and the `attrs_written` events stay as history.

## Workflow contract 5 (part 3): an integration batch is filed like any work item; `pf ops integrate` is gone

The integrate lane is a lane like any other: nothing in the daemon, the
CLI or the driver names it any more. Deployed by the contract 5 release,
under its drain (part 1).

- **Behavior: `pf ops integrate` is deleted** — the batch starter, its
  `status` face and `--clear-hold`. `pf ops integrate …` is now an unknown
  `pf ops` action. A person files a batch as any work item:

      pf file create --bundle integrate --project <p> --goal "integrate: gate the landed work items"
      pf submit <the work item it printed>

  The batch carries no `integration` label any more, and nothing checks
  the project's pool before filing: a batch with nothing landed, or over
  default branches that have not moved since their newest green tag or
  last red gate, runs and ends `empty`. A batch is followed like any work
  item (`pf watch <batch>`).
- **Behavior: the lane key `exclusive`.** A lane declaring `exclusive:
  true` holds every project to one unfinished work item on it; the shipped
  `integrate` declares it. The daemon's data-plane proxy forwards a
  work-item create (`pf file create --bundle <lane>`) on such a lane —
  resolved in the lane set of the project's workspace — to aihub
  exclusive, and aihub refuses it `CORE-409` `OPEN_ON_BUNDLE` while another
  work item of the project on the lane is not wrapped, failed or canceled.
  `pf file create` answers that refusal naming the open work item and its
  status; one still queued that no machine ever claimed (filed, never
  submitted) is named with its filing time and `pf submit` / `pf cancel`.
  Every other create is forwarded byte for byte. A user lane may declare
  it too. Exclusivity is checked when a work item is created, not when one
  is rebound to the lane (`pf file edit --bundle <lane>`).
- **Behavior: a hold is cleared only by a running batch.** Nothing outside
  a batch clears one: the batch's `collect` step puts a hold none of its
  fix tickets can release to the owner (`pf step ask`), and the agent
  clears it on the owner's word, citing the reply (`--per-owner`). The
  maintainer's `pf ops integrate --clear-hold <wi> --reason <text>` is
  gone with the verb.
- **Behavior: the `integration: true` marker is removed** from the
  contract, with the lane property it named: nothing finds a lane by it
  (its readers — `pf ops integrate`, the daemon's integrate status face
  and trigger — are gone), so a `workflow.yaml` carrying it is `WFC-111`,
  an unknown key. The shipped `integrate` declares `exclusive: true`
  instead.
- **Behavior: the `integrate:` section's refusal** (a `.polyforge.yaml`
  still carrying it) now names `pf file create --bundle integrate` and `pf
  submit` as the way a batch is filed.
- **Action:** drain as part 1 says, then install on every machine — a
  machine still on the previous daemon forwards an integrate create
  unchanged, so aihub does not hold it to one batch. Replace any script or
  runbook line running `pf ops integrate --project <p>` with the two lines
  above, and any `pf ops integrate --clear-hold` with answering the running
  batch's `collect` question. A user lane carrying `integration: true` (the
  loader refused it on a user lane already) has nothing to rewrite.
- **Rollback:** the previous release, as part 1 says (its daemon and CLI
  bring `pf ops integrate` back; the lane files read `workflow.md` again).

## aihub: the send-back and owner-rewind/applied routes are gone

aihub deletes the two routes that lost their last client with workflow
contract 4 C3c (v0.2.9), which removed `pf rewind`. No other aihub
interface changes.

- **Behavior: both routes answer 404.** `POST
  /api/v1/work-items/{id}/send-back` and `POST
  /api/v1/work-items/{id}/owner-rewind/applied` are unknown to the server
  and answer 404; nothing changes. No client has called them since
  workflow contract 4 C3c (v0.2.9).
- **Behavior: aihub no longer writes `attrs.owner_rewind`** or the
  `owner_rewind_recorded`, `owner_rewind_applied` and
  `owner_rewind_dropped` events. A plain resume (`POST …/resume-blocked`)
  no longer drops a legacy `attrs.owner_rewind`: it stays in the row and
  nothing reads it.
- **Behavior: `owner_rewind` is an ordinary attrs key.** A create or an
  attrs write (`PATCH …/attrs`) naming it is no longer refused; only
  Core's own `blocked_seal` is.
- **Action: deploy aihub; nothing else.** No SQL migration: no schema or
  stored-state change. A machine still on a release before v0.2.9 has its
  `pf rewind` of a blocked work item refused 404 and nothing changes;
  upgrade it.
- **Rollback:** the previous aihub.

## Summarize dispatches leave no session; removing the ones they left

The progress summary's one-shot calls no longer write a harness session,
and Pi Web's hidden-session badge counts only inside the day window.

- **Behavior: a summarize call writes no session.** Its pi call runs `pi
  -p --no-session --model <m>`, its claude call adds
  `--no-session-persistence`. Before, every call (one per work item every
  interval) left one session, which Pi Web listed as a hidden session.
- **Behavior: Pi Web's "show N hidden sessions" counts like the list.**
  Only sessions inside the "show the last N days" window count (default
  15); a window of 0 days counts every session.
- **Action (optional, by hand; neither the daemon nor `install.sh` deletes
  anything):** remove the sessions earlier summarize calls left. These
  commands touch only the directories named after the summarize working
  directory (`summarize-cwd`); every other session stays. List first, then
  delete:

  ```sh
  ls -d ~/.pi/agent/sessions/--*-summarize-cwd--/
  ls -d ~/.local/share/polyforge/claude-config/projects/*-summarize-cwd \
        ~/.local/share/polyforge/cells/*/claude-config/projects/*-summarize-cwd
  rm -rf ~/.pi/agent/sessions/--*-summarize-cwd--/
  rm -rf ~/.local/share/polyforge/claude-config/projects/*-summarize-cwd \
         ~/.local/share/polyforge/cells/*/claude-config/projects/*-summarize-cwd
  ```

  Run them as the user the daemon runs as: the pi sessions sit under that
  user's `~/.pi/agent/sessions/` (a summarize call does not inherit
  `PI_CODING_AGENT_DIR`), one directory per harness root, named after
  `<harness-root>/summarize-cwd` with each `/` turned into `-`. The claude
  ones sit under `<harness-root>/claude-config/projects/`. With a custom
  harness root (`--harness-root` / `[daemon] harness_root`), put that root
  in place of `~/.local/share/polyforge` in the claude lines; the pi glob
  already matches it.
- **Rollback:** the previous binary and Pi Web (summarize sessions are
  written again, the badge counts outside the window again).

## A lane declares whether its work items need acceptance: `acceptance: required | none`

A lane's `workflow.yaml` takes a new optional key, `acceptance`, and every
check that asks a work item for an acceptance criterion now reads it from
the item's lane. Workflow contract 5 is extended in place: the version
stays 5, no lane has to change.

- **Behavior: the lane key `acceptance`.** `required` (the default; not
  declaring it is the same, which is today's behavior) or `none`; any
  other value is `WFC-112`, on a built-in lane and a user lane alike. On a
  lane declaring `none`, `pf submit` no longer asks for a `## Acceptance`
  (or `## 验收`) section, the daemon's auto claim no longer skips an item
  without one, `pf queue` (and the Pi Web list, which reads its
  `acceptance_missing`) no longer marks it as needing interaction, and
  `pf file create` no longer prints which step carries the criteria. On a
  lane requiring it, all four behave as before, and `pf submit
  --allow-no-acceptance` still bypasses the check. A lane the reader
  cannot load (no daemon, no workflows dir, a lane not installed or
  refused) counts as `required`.
- **Behavior: the shipped lanes.** `integrate` and `release` declare
  `acceptance: none` — a batch or a release ticket has no criterion to
  list, so `pf file create --bundle integrate` followed by `pf submit
  <batch>` works as the lane's README says, without
  `--allow-no-acceptance`. `develop` and `micro` do not declare it and
  still require a criterion.
- **Behavior: `pf submit`'s refusal names no lane.** It says the item has
  no acceptance section and its lane requires one (the `acceptance` key
  of `workflow.yaml`, default `required`), and keeps naming `pf file
  edit`, the two headings and `--allow-no-acceptance`.
- **Behavior: the contract stays 5.** The registry's golden file and the
  lane schema `workflows/_schema/workflow-5.schema.json` are regenerated
  in place; the reference document records the rule: a new optional key
  whose default is the present behavior does not bump the contract
  version.
- **Action: deploy; no drain.** Install on every machine; no user lane has
  to change. To exempt a user lane, add `acceptance: none` to its
  `workflow.yaml` and run `pf workflows validate`. A machine still on a
  daemon from before this release refuses a lane carrying the key as
  `WFC-111` (an unknown key), not `WFC-101`, with no pointer to this
  section: upgrade every machine before a user lane declares it.
- **Rollback:** first remove `acceptance` from every user lane, and let
  the integrate and release work items in flight finish (or cancel them):
  their capsule's lane snapshot carries the key, and the previous daemon's
  loader refuses it `WFC-111` instead of parking them blocked with kind
  `contract`. Then the previous release; integrate and release items again
  need `pf submit --allow-no-acceptance`.

## Pi Web carries its Agent language into an unset `agent_language`

Pi Web now fills the default Cell's `[daemon] agent_language` from its own
saved Agent language when pf's file does not set the key. On a machine
with Pi Web this replaces the manual step that the section's
**Action** asks for right after install (`pf config set agent_language
zh-CN`); that section is left as it was, and its command stays as the
fallback. Nothing else in pf changes: the daemon still reads only its own
configuration.

- **Behavior: Pi Web backfills an unset key.** When Pi Web's server starts,
  and each time its settings page reads the Agent language (`GET
  /api/agent-language`), it reads the default Cell's `agent_language` from
  `pf --json config list`. If the key is unset (`source: default`) and Pi
  Web's saved language differs from pf's (its default English), Pi Web
  writes its saved value through `pf config set`. If Pi Web has English
  saved, or nothing saved, nothing is written. Only the default Cell is
  handled, as in.
- **Behavior: a key pf's file sets is never overwritten automatically.**
  If pf's file sets the key (`source: file`), Pi Web leaves it alone. The
  settings page shows the language pf's step agents use, next to Pi Web's
  own setting; when the two differ, it shows a hint and a sync button. The
  sync is the user's explicit choice: it saves Pi Web's language again, as
  a normal save does, and that save writes the key.
- **Behavior: an older pf, no pf, or a failing pf is skipped.** If the
  installed `pf` does not know the key, is not installed, or a `pf` call
  fails, nothing is written and no error is reported (Pi Web starts as
  before). The settings page then shows no pf line.
- **Behavior: reading the setting can now write.** The settings page's
  `GET /api/agent-language` can make one write, which is idempotent and
  happens only while the key is unset. Any caller that can reach Pi Web's
  `/api/*` can trigger it; with `PI_WEB_PASSWORD` set, that caller must be
  signed in.
- **Action: install and start Pi Web; the manual command is the
  fallback.** Run `tools/install.sh` (the `pf` binary and Pi Web). Once Pi
  Web's server starts, the key is filled; no command is needed. Run `pf
  config set agent_language zh-CN` yourself on a machine without Pi Web,
  for a named Cell (`pf --profile <name> config set …`), or to set the key
  before Pi Web starts. Steps dispatched before the key is set still run in
  English, as describes.
- **Rollback:** the previous Pi Web, which no longer backfills; a key it
  has written stays. Before rolling `pf` back, run `pf config unset
  agent_language`, as says.

## The workflow contract version moves only on an incompatible change, at most once per release

The workflow contract version (`contract:` in a lane's `workflow.yaml`, 5
today) no longer moves with every change to the contract. It moves only on
an incompatible change, and at most once per release. This release
changes the rule, the checks and one error message; it changes no lane
key, value or error code, and the version stays 5.

- **Behavior: compatible and incompatible changes.** A change is
  compatible when every lane valid under the previous release stays valid
  and behaves the same, and the work items that release claimed keep
  running: for example a new optional key whose default is the present
  behavior, or a new value for an existing key. Any other change is
  incompatible: a key removed or renamed, a key's meaning or default
  changed, a new required key, a check that refuses a lane it used to
  accept, a value removed, or a change to a capsule or hand-off format the
  work items in flight rely on.
- **Behavior: a compatible change never bumps the version.** It needs no
  drain: the registry's golden file and the lane schema
  (`workflows/_schema/workflow-<N>.schema.json`) are regenerated in place,
  and no lane has to change its `contract:` line.
- **Behavior: an incompatible change is registered when it merges.** The
  change adds one entry to `internal/common/workflow/contract-pending.md`:
  what changed, and what the migration takes for user lanes, for the work
  items in flight and for the deployment. It leaves the version as it is.
  A test fails, and says to register the change, when the registry shows
  a removed key, a new required key or a removed value while the list has
  no entry.
- **Behavior: the release enforces the bump.** Before anything is built,
  tagged or published, `.polyforge/release.sh` compares the version with
  the previous release's and counts the list's entries. While the list has
  an entry and the version is still the previous release's, it refuses
  and names the bump to make. The bump is one change: the version goes up
  by one, a new section of this file carries every entry's migration and
  the drain, and the list is emptied. A bump that leaves the list
  non-empty, or that moves the version by more than one, is refused too.
  With the list empty, a release neither bumps the version nor drains.
- **Behavior: an unknown key asks to upgrade pf.** Since compatible
  changes now add keys under the same version, a pf that meets a key it
  does not know still refuses the lane with `WFC-111`. The message now
  names the key and asks to upgrade pf: `this lane uses key "<k>", which
  this pf (workflow contract 5) does not know: upgrade pf — a newer
  release may define it (the keys this pf takes here: …)`. A key removed
  in an earlier contract keeps its rewrite hint. Releases up to and
  including v0.2.10 still print the old message.
- **Current state.** Since v0.2.10, the only contract change on main is
  the `acceptance` lane key from, which is compatible.
  The skippable-step key planned in is not merged yet; under
  the same rule it is a compatible change too. The pending list is empty,
  so the next release keeps contract 5 and needs no drain.
- **Action: none beyond installing.** Install the release on every
  machine; no drain is needed and no lane has to change. Upgrade every
  machine before a user lane uses a key added by a compatible change: an
  older pf refuses such a lane with `WFC-111`.
- **Rollback:** the previous release.

## Summarize sessions removed by their first message; Pi Web's Agent language read without writing; install.sh keeps a same-version Pi Web

Three small fixes. The cleanup command below replaces the `rm -rf`
commands of the section, which is left as it was.

- **Action (optional, by hand): remove summarize sessions with
  `tools/clean-summarize-sessions`, not with the `rm -rf`.**
  Those commands delete whole `summarize-cwd` directories: they also delete
  one-off sessions in them that are not summaries, miss the summarize
  sessions written before `summarize-cwd` existed (mixed into `--root--`
  and each workspace's session directory), and their pattern can match a
  directory of your own whose name ends in `summarize-cwd`. Do not run
  them. The script judges each session by its first user message: a
  session is removed only when that message starts with `Summarize in ONE
  short line what this coding-agent`. Run it as the user the daemon runs
  as. It lists first; nothing is deleted until you add `--delete`:

  ```sh
  python3 -I ~/.local/share/polyforge/release/current/tools/clean-summarize-sessions
  python3 -I ~/.local/share/polyforge/release/current/tools/clean-summarize-sessions --delete
  ```

  It scans every directory under `~/.pi/agent/sessions/` and every Cell's
  `<data-dir>/cells/*/claude-config/projects/` (`<data-dir>` is
  `$POLYFORGE_DATA_DIR`, else `~/.local/share/polyforge`; `--pi-sessions`
  and `--data-dir` override them). With a custom harness root (`[daemon]
  harness_root`), add `--harness-root <dir>` for it. It removes only the
  matching session files, never a directory.
- **Behavior: reading the Agent language no longer writes pf.** Pi Web's
  `GET /api/agent-language` now only reads: it runs `pf --profile default
  --json config list` once and returns Pi Web's saved language and pf's.
  It never runs `pf config set`. The backfill of the section
  now happens only when Pi Web's server starts (same rules: only while
  pf's file does not set the key, only when the values differ, with the
  compare-and-set); that section's "reading the setting can now write" no
  longer holds. The settings page's sync button saves through `PUT`, as
  before.
- **Behavior: the settings page names a pf that is still unset.** When
  pf's file does not set `agent_language` and Pi Web's saved language
  differs (for example the start's backfill was refused because pf's
  configuration changed at the same moment), the hint now says pf has no
  language set and offers the sync; the "never overwritten automatically"
  hint shows only when pf's file sets the key.
- **Behavior: `tools/install.sh --with-pi-web` keeps a same-version Pi
  Web.** When the installed `@silgrid/pi-web` already has the version of
  the release's tarball, the installer skips `npm install -g` (and prints
  that it did), so a running Pi Web is not made stale; the install check
  still runs. Another version installs as before. `--reinstall-pi-web`
  (only together with `--with-pi-web`) reinstalls anyway, for example to
  repair a broken install; a Pi Web running over that reinstall gets the
  usual `ACTION:` line, or the restart with `--restart-pi-web`.
- **Action: install.sh (with `--with-pi-web` on a machine with Pi Web);
  nothing else.**
- **Rollback:** the previous release and Pi Web (the settings page's GET
  backfills again, install.sh reinstalls Pi Web on every run). Deleted
  session files are not restored.

## aihub: the console's work-item list orders same-millisecond creations newest first; its page cursor moves to version 2

aihub's console work-item list (the web console's 20-row pages, newest
first) breaks ties inside one millisecond by the project's creation order.
Its opaque page cursor changes shape with it. No other interface changes.

- **Behavior: newest first holds inside one millisecond.** The list
  orders by `julianday(created_at) DESC, seq DESC`. `julianday()` rounds a
  creation time to the nearest millisecond, so items created less than a
  millisecond apart used to tie and fall back to their random id. They now
  fall back to the per-project number (`<project>#<seq>`), the order in
  which they were created.
- **Behavior: the page cursor is version 2.** The cursor (`next_cursor`,
  passed back as `after`) carries the last row's creation time and seq
  instead of its id. A version-1 cursor is refused as bad input (400); no
  compatibility path reads it.
- **Action: deploy aihub; nothing else.** No SQL migration: the cursor is
  never stored. A console page opened before the deploy on page 2 or later
  answers 400 once when it fetches its next page; going back to the first
  page and paging again continues. Nothing is lost.
- **Rollback:** the previous aihub. A version-2 cursor issued by the new
  aihub is refused the same way by the previous one, once.

## A project may skip develop's `merge`: the work item ends `published`, its PRs awaiting a human review

A lane step may now be declared skippable, and a project may switch such a
step off. The built-in develop lane declares its `merge` skippable: a
project that switches it off gets work items that end after `publish`,
with their PRs open for a human to review and merge. Nothing changes for a
project that does not switch it off — merge runs and the work item ends
`landed` as before. Workflow contract 5 is extended in place (the rule
 recorded): the version stays 5.

- **Behavior: the step key `skippable`.** A bool, default `false`; any
  other value is `WFC-112`. The registry's golden file and the lane schema
  `workflows/_schema/workflow-5.schema.json` are regenerated in place.
  Among the shipped lanes only develop's `merge` declares it; micro's
  `merge`, integrate and release declare none.
- **Behavior: the workspace switch `steps.<project>.merge.skip`.** A step
  entry of `.polyforge.yaml` now has two keys, `models` and `skip` (a
  bool), at least one declared; an entry with only `skip` is no level of
  the model chain, and `default` and `task` take no `skip` (a load error).
  `pf config set|unset steps.<project>.<step-id>.skip true|false` writes
  it (class `next-claim`); a write for a step no lane of the workspace
  declares skippable is refused, naming `skippable: true`. A `skip: true`
  written by hand on another step loads and is ignored. The key names a
  step id and takes effect only in the lanes declaring that step
  skippable: micro's `merge` still runs.
- **Behavior: fixed when the work item is first claimed.** The claim writes
  the work item's skipped steps to `runtime-state/skipped-steps.json` in
  its capsule; a re-claim and `pf resume` keep the file and never
  recompute it. Switching changes the work items claimed afterwards, never
  a running one.
- **Behavior: a skipped step.** The driver never dispatches it: its
  execution status is `skipped`, the event log records `step_skipped`, the
  opening of `artifacts/handoff.md` notes it as skipped, and the work item
  completes when every step is done or skipped. Pi Web's job panel draws a
  skipped step gray with a dashed frame.
- **Behavior: publish as the lane's last step.** With `merge` skipped,
  `publish` is the last step and merge's `wrap_attrs: [integration]` pass
  to it: it is granted `pf step hub wrap-attrs` and its `done` is refused
  until it registered `integration`. Its skill writes "This lane does not
  merge this PR — it awaits a human review." into the PR body, runs the
  new shared script `published.py` (it checks each published head against
  the verified head and writes `{state: published, repos: [{repo, pr,
  head, light_verified_head}]}` to `artifacts/publish/integration.json`)
  and registers that record. The work item is wrapped with
  `integration.state = published` and the PR links; the integrate lane's
  collect takes `landed` work items only, so it never enters a batch.
- **Behavior: the read faces.** `pf workflows` reports each step's
  `skippable`, and with `--cwd` the projects that skip it (`skip`); Pi
  Web's workflow card shows a switch on a skippable step's row and writes
  it through `pf config set|unset`.
- **Action: install on every machine, then switch per project.** Install
  the release (`tools/install.sh`) on every machine that maps the
  workspace; aihub is unchanged and nothing is drained. Nothing is
  switched off by the upgrade. Once every machine runs the release, a
  person switches it on for each project that wants it — `pf config set
  steps.<project>.merge.skip true --cwd <workspace>`, or the switch on Pi
  Web's workflow card. A machine still on a daemon from before this
  release refuses a `.polyforge.yaml` carrying `skip` (a key not allowed):
  it claims nothing from that workspace (`DAEMON-414`) until upgraded.
- **Rollback:** first unset the switch on every project (`pf config unset
  steps.<project>.merge.skip --cwd <workspace>`), and let the develop work
  items in flight finish (or cancel them): every develop work item this
  release claimed carries `skippable: true` in its lane snapshot, which
  the previous daemon's loader refuses `WFC-111` instead of parking it
  blocked with kind `contract`. Then the previous release. A work item
  wrapped `published` keeps its attrs and its PRs stay open for a human to
  merge; nothing reads the record.

## The verification container's memory is 16g by default; `verify.py` marks browser crashes and the units it could not re-run

The light verification's browser e2e suites crashed on their first run
(Chromium's `Target crashed` / `Page crashed`) and passed on the re-run,
recorded flaky. The cause was the verification container's 8g memory
limit: each suite runs its own `next dev` (1.2–2.4 GiB), and at 4 browsers
they filled the container's cgroup until the kernel's OOM kill took
Chromium's renderer. `/dev/shm` played no part (Chromium does not use it
here; 2g of shm crashed the same).

- **Behavior: the container's memory default is 16g.** `verify.py` and
  `gate.py` run their container with `--memory 16g` (swap the same), up
  from 8g; `gate.py --memory` still sets another limit. Measured at 4
  browsers: 3 suites peak near 9.4 GiB and crashed in 3 of 10 rounds under
  8g, none of 10 under 16g; 6 suites peak near 12.5 GiB, 4 of 5 rounds
  under 8g, none of 5 under 16g. The limit is a ceiling, not a
  reservation.
- **Behavior: `units[].failure`.** Every unit that failed its first run —
  still red, flaky or pre-existing, a `<repo>:unit-run:<id>` unit included
  — carries `failure` in `result.json` and under the result file's
  `details.checks[].units[]`: `browser_crash` when that run's output for
  it shows `Target crashed` or `Page crashed` (a suite's `ABORT` line
  inside its block included), else `assertion`. A passing unit carries
  none.
- **Behavior: `units[].not_rerun`.** A failed unit whose re-run never
  started — `verify.py`'s 20m budget spent, or its deadline passed —
  carries `not_rerun: true` there and stays a failure; the budget is
  unchanged.
- **Behavior: the report.** `report-mechanical.md` marks a browser crash
  in its flaky and pre-existing lists and in a failed check's detail, and
  lists the `not_rerun` units by check under `### Not re-run`.
- **Behavior: the verify skill.** It never places a `browser_crash` on the
  change from its log: one still red is a red it cannot place and asks the
  owner about (`pf step ask`); a flaky one is named a browser crash in the
  hand-off, never a flaky test of the change. It asks the owner about the
  `not_rerun` units with their list (usually: run again).
- **Action: install.** `tools/install.sh`; nothing is drained. A work item
  claimed before the upgrade runs its lane snapshot's scripts and skill (8g,
  neither field) until it ends. A machine whose memory cannot hold a 16g
  container while its other work runs gets the same OOM kill as before,
  later.
- **Rollback:** the previous release. Nothing reads the two fields outside
  the verify skill.

## The verification's private TMPDIR is short and named in the report

A Go test that listens on a unix socket under `t.TempDir()` failed the
verification with `bind: invalid argument`: the command's private TMPDIR
(`pf-verify-tmp-` plus a suffix, on the host under the step's own
`$TMPDIR`) left too few of the 107 bytes `sun_path` allows for
`<test name><suffix>/001/<name>.sock`. The report showed the path as
`<tmp>`, so its length could not be checked.

- **Nothing to migrate.**
- **Behavior: the private TMPDIR.** `verify.py`, `gate.py` and `run.py`
  give each command a private TMPDIR named `/tmp/pfv-…` on the host and in
  the verification container alike. On the host it is always under
  `/tmp`, no longer under the step's `$TMPDIR`. A socket at
  `t.TempDir()/<name>.sock` of a test whose name takes Go's full 64 bytes
  keeps 11 bytes for `/<name>.sock`.
- **Behavior: the private TMPDIR in the report.** The verification
  report (`report-mechanical.md`) names each command's private TMPDIR,
  with its length in bytes, under its repo's plan; `result.json`'s
  `repos[].commands[]` and the result file's `details.checks[]` carry it
  as `tmp_dir` (the real path, inside the container for a container run;
  the command's first run). The failure output still shows `<tmp>`, and
  the failure signatures are unchanged.
- **Action: install.** `tools/install.sh`; nothing is drained. A work item
  claimed before the upgrade runs its lane snapshot's scripts until it
  ends. A run killed too hard to clean up under the previous release
  leaves a `pf-verify-tmp-*` directory the new release no longer sweeps:
  once every work item claimed before the upgrade has ended, remove any
  left under the old `$TMPDIR` (and `/tmp`) by hand.
- **Rollback:** the previous release. Nothing reads `tmp_dir`; the report
  loses its TMPDIR lines.

## `pf doctor` probes each mapped repository's declared host prerequisites; this repository declares mypy

A repository's fast gate runs on the host as well as in the verification
container (build's fast checks, review's host replay, the micro lane), so a
tool the gate needs and the host lacks turns every work item's fast checks
red. Until now nothing said so before the first one did. A repository now
declares what its verification rules need on the host, and the doctor
checks it after an upgrade.

- **Behavior: `host_requires=` in `.polyforge/verify-scope.conf`.** One line
  per prerequisite, `host_requires=<name>|<probe>|<hint>`: a name, a probe
  command (exit 0 means met) and an install hint. The scope engine parses
  it and lists the accepted lines in its `--json` report as
  `host_requires: [{name, probe, hint}]` (the key is there only when one is
  declared); a malformed line is refused with a diagnostic. It is a
  declaration only: it binds no command and changes no tier, and a fast
  gate whose tool is missing still fails red.
- **Behavior: the doctor's `host prerequisites` row.** For each mapped
  workspace's `.repo/<name>` clone with a `.polyforge/verify-scope.conf`,
  the doctor asks the engine of the daemon's lane set for its
  `host_requires` and runs each probe with `bash -c` from the clone's root
  under the step PATH, bounded by 30 s. Met: `pass` with the probe's first
  output line (its version). Unmet or timed out: `fail` naming the
  repository, the prerequisite and its install hint. The doctor names no
  tool of its own and installs nothing.
- **Behavior: this repository declares mypy.** Its fast gate type-checks
  the workflow step scripts with `python3 -m mypy`; the
  declaration's range, `mypy>=1.20,<2`, is pf CI's and holds the
  verification image's pin.
- **Action: install, then run `pf doctor`.** `tools/install.sh`, then
  `pf doctor`: install by hand whatever the `host prerequisites` row fails
  on, with the hint it prints, and run the doctor again until the row
  passes. A work item claimed before the upgrade
  keeps its lane snapshot's engine, which reports `host_requires=` as an
  unknown key (a diagnostic, not a red) until it ends.
- **Rollback:** the previous release; its engine reports the line as an
  unknown key the same way, and its doctor has no such row.

## Step harnesses, task agents and step commands start without a controlling terminal

The daemon runs on tmux's pty and started each step harness and task agent
with only its own process group, so they held the daemon's controlling
terminal, and nothing below them guaranteed a command could not reach it.
An interactive shell on that terminal (`zsh -f -i`, `sh -i`, `bash -i` in
a background group) opens `/dev/tty` and reads its commands from the
terminal instead of its pipe: a test piping a script into one hung until
the verification command's timeout, while the same test passed in a
fraction of a second outside the daemon.

- **Behavior: the daemon starts a step harness and a task agent in a new
  session.** Every step harness (pi, claude) and every task agent starts
  with `setsid`, never `setctty`: it has no controlling terminal. It still
  leads its own process group (a session's leader leads its group), so the
  timeout, the step's end and the cleanup layers kill it and what it
  started as a whole, as before.
- **Behavior: `pfstep`'s commands start in a new session.**
  `pfstep.proc.run` (every command `verify.py`, `gate.py` and the other
  step scripts run) uses `start_new_session`: an interactive shell reads
  its pipe, and a command that reads `/dev/tty` (a git or ssh password or
  host-key prompt, gpg's pinentry, an interactive installer) fails at once
  (`No such device or address`) instead of waiting on the daemon's
  terminal until its timeout. The timeout and the cleanup still kill the
  command's group whole (124 on a timeout).
- **Action: install.** `tools/install.sh`; nothing is drained. The harness
  and task-agent change takes effect at the first dispatch after the
  daemon restarts; a work item claimed before the upgrade runs its lane
  snapshot's scripts (the previous `proc.py`) until it ends.
- **Rollback:** the previous release. No data or format changes.

## Review blocks on documents a change leaves out of sync; a release reads its docs first

The lanes now check the documents a change affects, by judgment in the
step texts only: no new step, script or mechanical test.

- **Nothing to migrate.**
- **Behavior: review's doc-impact check.** The shared review's **Stale
  docs.** item: when a change alters user-visible behaviour, a
  configuration key, a command or a step definition, the README, the
  reference documentation, the upgrade notes (appended to only, where a
  repository keeps them append-only) and the skill documents that describe
  it must change in the same diff. One left out of sync is a blocking
  finding — `project_rule` for a coupling the repository's `AGENTS.md`
  names, `guarantee` for any other — and `artifacts/review.md` lists every
  document the review compared. Before, only a coupling `AGENTS.md` names
  or user-visible wording that contradicted the behaviour blocked. micro's
  own review is unchanged.
- **Behavior: the release step's docs check.** On a first run, after the
  already-released check and before the procedure, the release step reads
  the README, the install instructions and the upgrade notes against what
  was merged since the previous release tag (`git describe --tags
  --abbrev=0 --match 'v*'`; the whole tree when there is none), looking
  for disagreement and for a removed command or configuration key still
  named. It edits no document: on a mismatch it asks the owner once —
  release as is, or cancel — and on a go-ahead starts its preconditions
  again. A resumed version skips the check.
- **Action: install.** `tools/install.sh`; nothing is drained. A work item
  claimed before the upgrade keeps its lane snapshot's texts until it
  ends.
- **Rollback:** the previous release.

## The verification container's cache volume follows its image; older ones are removed

The cache volume mounted at `/cache` in a verification container (Go's
module and build caches, npm's cache) was named after the repository
alone, `pf-verify-cache-<repo>` (the section above): it was
never invalidated and never removed, whatever toolchain pins the image
moved, and grew without bound.

- **Behavior: one volume per image generation.** The volume is
  `pf-verify-cache-<repo>-<hash>`, `<hash>` the image tag's own (the first
  16 bytes of `.polyforge/Dockerfile.verify`'s sha256, hex): the same
  Dockerfile reuses its volume (warm); any byte changed in it — a Go,
  Node or Playwright pin — gives a new, empty one (cold). Within a
  generation the contents check themselves (Go modules against `go.sum`,
  build actions keyed by the toolchain, npm's cache by integrity): a
  lockfile change only misses and downloads. `node_modules` is not
  cached: each `npm ci` installs it into the clone as before. A volume
  suspected corrupt is removed by hand (`docker volume rm` of the current
  one, once no verification runs); the next run starts cold.
- **Behavior: pf removes docker volumes.** Each container start
  (`verify.py`, `gate.py`, a step script's container) removes the
  repository's older generations no container references: only a volume
  named exactly `pf-verify-cache-<repo>-<32 hex>`, not the current one,
  that docker lists as unused (`docker volume ls --filter dangling=true`),
  removed with `docker volume rm` (never `--force`, so docker refuses one
  in use). Another repository's volume — one whose name starts with this
  one's too — or a volume of any other shape is never touched. A
  verification still running on an older image keeps its volume: its
  container references it, so it is neither listed nor removable; a later
  start removes it. A failing listing or removal is one line in the run's
  output and changes nothing else.
- **Action: install.** `tools/install.sh`; nothing is drained. Each
  repository's first verification after the upgrade starts cold (a new
  volume), and the change to `.polyforge/Dockerfile.verify` rebuilds this
  repository's image once. The volume of the name before generations is
  not removed by pf: once no verification of the previous release runs,
  remove it by hand on every machine, once per repository —
  `docker volume rm pf-verify-cache-<repo>` (`docker volume ls --filter
  name=pf-verify-cache-` lists them).
- **Rollback:** the previous release. It uses `pf-verify-cache-<repo>`
  again (cold if it was removed); the generation volumes it leaves are
  removed by hand with `docker volume rm`.

## `pf queue --bundle` and `--attr`: aihub's list filters passed straight through

`pf queue` gains two list filters that aihub's work-item list already
answers. The CLI passes them through as they are; it adds no default and
treats no lane or attr path specially.

- **Behavior: `--bundle L`.** Sent as `?bundle=L`: only the work items of
  that lane (bundle).
- **Behavior: `--attr <path>=<value>`, repeatable.** Each is split at its
  first `=` and sent as `?attr=<path>:<value>`; the rest of the value is
  sent verbatim (`--attr x.y=a:b=c` sends `attr=x.y:a:b=c`). Every one
  must hold: the attr at the dot path is that string. aihub checks the
  path and its refusal is reported as is (`CORE-400`). An `--attr` with no
  `=` or an empty path is refused before any request.
- **Behavior: list mode only.** Both combine with `--project`, `--status`
  and `--limit`; without `--project` every mapped project's query carries
  them. With `--ready` they are refused, like `--limit`.
- **Who uses it.** Any reader of the list, for example `pf --json queue
  --project P --attr integration.state=<state>`; Pi Web's Work view of
  landed items reads each project through it. Reach is unchanged: it is
  the same data-plane `pf queue`, and a step agent is still refused
  (`DAEMON-403`).
- **Action: install.** `tools/install.sh`; nothing is drained. An older
  `pf` refuses the flags (`flag provided but not defined`), and a Pi Web
  view that reads through them shows that refusal for each project until
  `pf` is upgraded.
- **Rollback:** the previous release; the flags are gone again.

## build keeps its committed history; a final end resolves carrier_push_failed

The daemon backs each work item's commits up to aihub and refuses to back
up a history that no longer contains the previous backup. A build that
amended a commit, or switched its working directory to an earlier commit
to check its red, left the work item without a backup, and the
`carrier_push_failed` entry it raised stayed open on the panel after the
work item ended.

- **Nothing to migrate.**
- **Behavior: build skill.** The build skill never amends or otherwise
  rewrites a commit it has made (no rebase, no reset past it): a fix is a
  new commit. To check an earlier commit's red again it checks the commit
  out in a separate directory (`git worktree add <dir> <sha>`, then
  `git worktree remove <dir>`), never by switching its working directory
  to that commit. The backup rule and the push are unchanged.
- **Behavior: decision feed.** A final end of a work item — completed
  (wrapped), failed or canceled, through this daemon's release or a hub
  read — resolves its `carrier_push_failed` entries: a work item that
  ended is never resumed, so the warning that a resume may lose commits
  no longer holds. A blocked or paused end keeps them open; a resume
  resolves them as before. An entry left open by the previous release is
  not resolved retroactively: dismiss it with `pf watch dismiss <id>`.
- **Action: install.** `tools/install.sh`; nothing is drained. A work item
  claimed before the upgrade keeps its lane snapshot's build skill until
  it ends.
- **Rollback:** the previous release.

## sync refuses a resolution that drops lines both parents keep

A sync agent resolving a real conflict in place could drop a large part of
a file both sides kept (a section, a table's tail) and `sync.py
--continue` committed it: nothing after the sync checks the resolved
content against its parents.

- **Nothing to migrate.**
- **Behavior: `sync.py --continue`.** Before it commits a merge, each
  conflicted file's staged result is compared with its two parents
  (`HEAD` and `MERGE_HEAD`): a line both parents carry must still be in
  the result (lines compared with trailing whitespace stripped, blank lines
  ignored, order and repetition not counted; a file removed from the index
  reads as empty). One that is missing keeps the file under `conflicts`
  and adds a note naming the file, how many distinct lines it drops and up
  to three of them; the exit is 1, as for a residual conflict marker. A
  binary file (a NUL in its first 8000 bytes) and a file only one parent
  has are not checked. The result's shape is unchanged.
- **Behavior: sync skill.** Refused for dropped lines, the agent puts the
  lines back, or — when dropping them is the resolution — asks the owner
  as for any conflict that is not its to resolve.
- **Action: install.** `tools/install.sh`; nothing is drained. A work item
  claimed before the upgrade keeps its lane snapshot's `sync.py` until it
  ends.
- **Rollback:** the previous release.
