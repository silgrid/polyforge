# Upgrade guide

How a deployment moves from one Polyforge release to the next: the aihub
server, its database and the work machines. A release that needs more than
installing it adds one section at the end of this file, and a released
section is never rewritten: the file is append-only, its sha256 pinned by
`test/w254_docs_set_acceptance_test.go`.

The server goes first: deploy the new aihub (`pf aihub`), then upgrade every
work machine with the installer (`tools/install.sh`; see
[install](install.md)).

## v0.2.0, the first public release: workflow contract 1

v0.2.0 starts this file. It is the first release of the unified product:
one `pf` binary (the server is `pf aihub`), the workflow lanes, the skills
and Pi Web, published together as one GitHub Release. Its workflow contract
is **contract 1**: every built-in lane declares `contract: 1`, and the
daemon implements contract 1 alone and refuses any other value (`WFC-101`);
[the workflow contract reference](reference/workflow-contract.md) is its
rule. Changes made before this release are in the git history, not here.

Moving from the earlier public v0.1.x tags is a drain and a reinstall, in
this order:

- **Drain the work machines.** On every work machine still on v0.1.x, stop
  claiming (`pf ops auto off`) and let every work item in flight finish, or
  cancel it (`pf cancel <wi>`). A work item claimed under v0.1.x cannot
  continue under v0.2.0: its capsule holds a v0.1.x `workflow.md` lane,
  which v0.2.0 refuses (`WFC-101`). One left over parks blocked with kind
  `contract`, naming this section; cancel it and file it again.
- **The server.** `pf aihub` replaces the separate `aihub` binary of v0.1.x
  (the image `ghcr.io/silgrid/polyforge-aihub:<tag>` runs it). The server never
  migrates its database at start, and a database written by v0.1.x lacks
  one schema change v0.2.0 needs: the messaging read cursor is split into a
  delivery cursor and a read cursor. With the server stopped and `aihub.db`
  backed up, run these statements as one transaction (SQLite 3.35 or later,
  for `DROP COLUMN`), then start v0.2.0 on the database:

  ```sql
  ALTER TABLE conversation_members ADD COLUMN delivered_seq INTEGER NOT NULL DEFAULT 0;
  ALTER TABLE conversation_members ADD COLUMN read_seq INTEGER NOT NULL DEFAULT 0;
  UPDATE conversation_members SET delivered_seq = last_read_seq, read_seq = last_read_seq;
  ALTER TABLE conversation_members DROP COLUMN last_read_seq;
  ```

  These are `deploy/sql/v2.20.7/upgrade.sql`; the same directory's
  `probe.sql` answers 0 when a database already carries the split, and its
  `rollback.sql` reverses it (the backup is the simpler way back). Apply
  them by hand on this move: the deploy's SQL step selects only the
  directories of the release tags between the running version and the
  target, and no public tag carries this one.
- **The work machines.** Re-run the installer on each machine, the same
  command as a first install; add `--with-pi-web` to install Pi Web from
  the same release (Pi Web ships only with the GitHub Release). v0.2.0
  keeps a machine's state per Cell, one per AIHub profile under
  `<data-dir>/cells/<profile>/`, and its daemon refuses the v0.1.x data dir
  until it is migrated once, with the daemon stopped: `pf migrate
  cell-layout` (the plan; it writes nothing), `pf migrate cell-layout
  --apply`, then `pf daemon ensure`.
- **The workspace yaml.** A `.polyforge.yaml` that still carries the root
  `summarize:` or `memory_policy:` section is refused: `pf config migrate`
  moves both into cli.toml and deletes them from the yaml, leaving a
  `<file>.bak-<UTC timestamp>` copy of every file it changes. A per-step
  key must name `default`, `task` or a step of an installed lane; one that
  names a step of a v0.1.x lane is stale and governs nothing. `pf doctor`'s
  `workspace yaml` row names each stale key: move its models to a step that
  exists, or delete it.
- **The lanes.** The built-in lanes are `develop` (the default development
  lane), `micro`, `integrate` and `release`; v0.1.x's `autopilot`,
  `bug-fix`, `chore` and `feature-full` are gone. A queued work item bound
  to one of them is refused at the claim and stays queued (`WFC-123`;
  `pf doctor`'s `workflow lanes` row lists it): re-bind it while it is
  queued and never claimed (`pf file edit <wi> --bundle develop`), or
  cancel it. A lane you wrote yourself becomes a directory under
  `<workspace>/.polyforge/workflows/<lane>/` holding `workflow.yaml` (the
  former `workflow.md` frontmatter, with `contract: 1`), `README.md` (its
  prose) and `steps/<id>.md`; a `workflow.md` is refused with that rewrite.
  Check each lane with `pf workflows validate <lane-dir>`;
  [bundles](bundles.md) teaches the format.
- **Finish.** `pf ops auto on` on each work machine, then `pf doctor`.

## v0.2.1, the project deactivation status

v0.2.1 adds the project's global **deactivation** state: a maintainer or
admin deactivates a project (`pf projects deactivate <id-or-name>`), the
server then refuses every write that does work on it — creating or
modifying work items, comments, submits and claims (the auto-claim
included), resume execution, memory writes — with the typed
`PROJECT_DEACTIVATED` error, while reads keep working under the existing
permissions. Nothing is deleted: only the hard delete
(`pf projects delete`, unchanged) removes data. `pf projects restore`
brings the project back. The CLI, Pi Web and the AIHub console hide
deactivated projects from their lists by default and offer a restore
entry in the same place. Deactivation is refused (CORE-409, the blocking
work items listed) while the project still has queued, running, paused
or blocked work items.

A database written by v0.2.0 or earlier lacks the `projects.status`
column; the new server refuses to serve projects until the migration
below has run. Both v0.2.0 and v0.2.1 are public tags, so an unattended
`deploy.sh` from v0.2.0 applies it inside the switch window
(`deploy/sql/v0.2.1/`); there is nothing to run by hand on that move:

```sql
ALTER TABLE projects ADD COLUMN status TEXT NOT NULL DEFAULT 'active'
  CHECK (status IN ('active','deactivated'));
```

The migration is purely additive: every existing project answers
`active` (the only state a pre-v0.2.1 database can hold), and no row is
rewritten or lost. The same directory's `probe.sql` answers 0 once the
column exists (the run-once guard), and its `rollback.sql` drops the
column again — deactivation state is operational, not durable data; a
retry upgrade re-adds it with every project `active`.

## v0.2.1, the pre-submit analysis check

v0.2.1 also adds the pre-submit analysis check (the `pf submit` one, the
`verbs/submit.md` skill): an execution ticket on a lane that requires
acceptance — `develop` and `micro` — carries a `## 分析` (or `## Analysis`)
section with content before it can be submitted, the daemon's
auto-claim skips a ticket without one the same way it skips a ticket
without acceptance, and `pf queue` marks it 需要交互：未分析.

The upgrade impact: a develop or micro work item filed before v0.2.1 has
no `## 分析` section. After the upgrade `pf submit` refuses such a ticket
(exit 1, nothing claimed; `--allow-no-analysis` bypasses it only on the
owner's explicit word), and the auto-claim does not take it. Before turning the auto-claim back on
(`pf ops auto on`), run `pf queue` and settle every queued execution
ticket: write the analysis report into its `## 分析` section, or the
owner's dated 「免分析（<date>, <who>）」 line, or cancel the ticket. A
ticket already in flight is untouched — the check runs at submit and
claim, never mid-run.

This section also corrects the one above: the server runs no startup
check for the `projects.status` migration. Until the migration has run,
the requests that touch the missing column fail with SQLite's own errors
(no typed refusal); serve traffic only after the migration, which an
unattended `deploy.sh` applies inside its switch window.

## v0.2.1, the scope engine in Python

The release-shipped verification-scope engine,
`workflows/_shared/scripts/verify-scope`, is rewritten in Python: the same
path, the same command line and the same report, with one implementation of
the `carries:` header parse shared with the step scripts' `pfstep` library —
the shell implementation is gone, with no forwarding stub. The upgrade
impact: a host that runs a repository's bar on the host (a tree without
`.polyforge/Dockerfile.verify`) now needs `python3` for every engine run,
not only for `--json` — the documented `pf doctor` prerequisite already
names python3, and a bar in a container is unaffected (the verification
image provides it).

Two rules that lived in the workflow's own text become declarations of each
repository's `.polyforge/verify-scope.conf`: the light-tier prefix an
exceeded `e2e_start_threshold` puts before `e2e_runner=` is the declared
`e2e_start_prefix=` (pi-web declares its current
`E2E_SERVER_MODE=start`), and `facts.py build --replay` classifies by the
repository-declared `not_runnable_rule=<toolchain>|<rule>|<pattern>` lines
(pi-web declares its browser preflight's text). An engine older than
this section reading a conf that declares the keys diagnoses them as
unknown and ignores them, and an older facts.py simply skips keys it does
not read, so a conf may declare them before the release ships; after the
upgrade the older hardcodes are gone, so a repository that wants the
behavior declares the key.

## v0.2.1, the integrate full gate runs every command

The integrate lane's full-gate step gates a batch with `--keep-going`:
the gate runs every command of the full bar, never stopping at the first
failing one, so one red batch records every failing command — none is
left `not_run` — and the triage names each command's own failed units at
once. A red that used to wait a whole round behind the first one (the
commands after it never ran) now shows in the same batch; the cost is the
longer red run, the commands after the first red running too. The
script's default is unchanged — every other caller of `gate.py` (the
release's, an operator's run) still stops at the first failing command
unless it passes `--keep-going` itself — and the develop and micro
lanes' verification is untouched: develop's `verify.py` runs its whole
plan, as before, and micro's evidence (`run.py`) still stops at its
first failing line.

## v0.2.1, units already red before the change

`facts.py build --replay` understands a unit whose reproduction test
already exists and is red before the change (an integration fix ticket:
the test came with the batch, the fix is the change): the unit declares
`"pre_existing_red": true` in `artifacts/build/units.json` and reports no
`test_commit`; the engine replays its recorded run on the tree of the
change's base (the merge-base with the fetched default branch) — red
there is the unit's red evidence, which the result records, and a run
that exited 0 there fails the unit (the test was green before the
change, so it was not already red; review classes that `test_weakened`).
Everything else is unchanged: an ordinary unit keeps its test commit and
the four requirements of the test-first shape, and verification still
runs every recorded run green on the final tree.

## v0.2.1, `pf step ask` answers -h/--help itself

`pf step ask --help` used to post the literal "--help" to the owner as the
question — the step stalled waiting for an answer to a word its agent only
wanted explained. ask is a flag-less sub-action: the words after it are the
question, so nothing parsed them. It now answers `-h` and `--help` locally
(ask's usage on stdout, exit 0, no call to the daemon) and refuses, before
any call, a question whose first word starts with `-` (the lone `-` stdin
marker excepted), pointing at the quoting and stdin escapes. The audit of
every other `pf step` sub-action found no second case: each either registers
flags — the flag package answers their `-h`/`--help` with an error before
any call — or takes strict positionals. ask's own semantics (one open
question per dispatch, the size and UTF-8 bounds) and the daemon's checks
are unchanged.

## v0.2.1, the gate falls back to the lane's scope engine

The integrate lane's full gate used to judge only a repository whose
tree carries its own scope engine (workflows/_shared/scripts/verify-scope)
and tools/gate-id: every other repository was not judged, exit 2 — the
lane was unusable for anyone but polyforge itself. The gate now falls
back: the engine the tree carries plans the gate when there is one, else
the lane's own copy, mounted read-only beside the clone, and the green
tag's gate id comes from tools/gate-id, else the lane's own
implementation over the same bytes. A repository needs to provide only
.polyforge/verify-scope.conf and .polyforge/Dockerfile.verify; the image
must carry python3 (>= 3.9), git, bash and sh beside the repository's own
toolchain — a Go repository also govulncheck, one with an npm lockfile
also npm, the container reaching the vulnerability databases (the audit
gate never skips). A tree without the Dockerfile at HEAD is still not
judged: that one the lane cannot provide.

Which source served a run is recorded in the result and in the green
tag's message (engine_source and gate_id_source, "tree" or "lane"),
never in the plan digest — a fallback's plan names the lane snapshot's
directory <engine>, so the same commit digests the same in every batch
capsule, and a repository with its own engine keeps its digest and
runner version byte for byte. A green tag is reused only for a record of
the same sources; one written before the fields existed is the tree's by
construction. The batch record does not aggregate the sources: each
repository's result carries its own.

## v0.2.2, the deploy configuration follows the work item's Cell

A machine that runs more than one Cell no longer deploys everything
through the one `~/.config/polyforge/deploy.env`: the daemon states to
every step process its work item's Cell facts — `PF_CELL` (the Cell's
name), `PF_CELL_SERVER` (that Cell's server) and `PF_DEPLOY_ENV` (the
deploy configuration file that Cell's deploys read, in the Host
configuration directory) — and `.polyforge/deploy.sh` reads that file,
refusing before any work when it does not exist (both components alike),
and checks the deploy target against the Cell's server before any network
work, `--dry-run` and `--preflight` included. After upgrading, an
operator of a named Cell creates its `deploy-<profile>.env` before that
Cell's next step-driven deploy, or the deploy refuses naming the missing
file — the default Cell keeps `deploy.env`. A run by hand, without the
Cell facts, keeps the old behavior and says the target check was skipped.
