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

## v0.2.1, the develop merge done by a person (`steps.<project>.merge.by: human`)

A project whose GitHub requires an approving review the lane cannot give
may now declare its develop merges done by a person:
`pf config set steps.<project>.merge.by human` (the workspace yaml's
`steps.<project>.merge.by`, the one mode this release knows; the lane's
step declares `human_mergeable: true` — develop's `merge` does, micro's
does not, and a claim of a lane that cannot honor the declaration is
refused naming it; a step entry declares `by` or `skip`, never both). The
claim fixes the mode into the capsule's
`runtime-state/step-modes.json`, exactly as the skipped-step set. For
such a work item publish's PR body awaits the person's merge (and asks
for GitHub's *Require branches to be up to date before merging*) instead
of forbidding the hand merge, and the merge step asks the owner over the
PR links and the verified heads instead of merging; the owner's revision
request rewinds to build charged to nothing. `merge.py` takes the
person's landing by the patch-id fingerprint — GitHub's three merge
methods all count (a merge commit carrying the verified head as its
branch side, a rebase's replayed commits, a squash's whole diff); a
landing that does not match the verified change, a closed PR or one
still open is the script's exit 3: the step asks the owner again, never a
FAIL back to `sync` and never a lane's `uncounted_rewinds` class's to
match. The landing record's `merge_commits` now name each landed commit
with its merge flag (`{sha, merge}`; a pre-#36 record's bare string reads
as a merge commit), so the integration lane's `revert.py` reverts a
squash or a rebase landing too — the mainline flag `-m 1` is asked of the
commit itself (its second parent), never of the record's word.

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

## v0.2.1, the task concurrency bound is a Host key

The one-off task lane's concurrency limit is no longer the code constant
2: it is the Host-only `[daemon] max_concurrent_tasks` (default 2, so an
untouched cli.toml keeps the old bound), live on reload — the default
Cell's file moves the one bound every Cell's spawns share, the next spawn
is judged by the new value, and a lowered bound refuses only new spawns
(never stopping running tasks). One-off tasks no longer take a
`max_concurrent_jobs` (or `cell_max_concurrent_jobs`) slot: those keys
bound work-item jobs only, so a full job ceiling no longer refuses a task
spawn and running tasks no longer hold back a job start. `pf ops status`'s
`tasks: x/N` and `background_model_calls.bound` report the configured
value. A spawn without budget flags now gets 40 minutes and 1000000 tokens
(the token default is the ceiling, so `--max-tokens` can only lower it);
the ceilings (60m / 1000000) are unchanged. Raising the bound needs no
restart: `pf config set max_concurrent_tasks 8 --host` and a reload.

## v0.2.1, a bundle-bound ticket can be a parent

aihub no longer refuses a bundle-bound ticket as a parent
(`PARENT_NOT_REGISTRY`) nor a bundle bound to a ticket with children
(`HAS_CHILDREN_NO_BUNDLE`); work items still nest one level. A bundle-bound
parent waits for its children to merge: until every child is wrapped its
claim is refused `DEPENDENCY_BLOCKED`, it stays out of the ready queue, and
its `waiting_for` lists each child with `"via": "child"`; a failed or
canceled child keeps it waiting. Cycle detection follows that wait. A child
is detached with `pf file edit <child> --parent ""` (aihub only clears
`parent_id`; a running child is refused `CHILD_RUNNING`; the write records
`wi_detached`), and once a child was claimed the parent's bundle is frozen
(`PARENT_BUNDLE_FROZEN`). `pf queue` shows a child as `<slug> (child,
<status>)` in WAITING-FOR and marks a parent waiting for a failed or
canceled child (the merged `--json` carries `"child_failed": true`). An
unbound parent behaves as before: `pf wrap` once its children are terminal
(`OPEN_CHILDREN` otherwise), never claimable.

Deploy the new aihub first, then upgrade the pf clients. An older pf
against the new aihub ignores `via`: it lists a child as a plain blocker and
its refused-submit guidance names `pf file dep rm`, which finds no edge for
a child; a new pf against an older aihub cannot detach (`parent_id` is not
updatable there). There is no schema change. Before deploying, query the
target aihub once, read-only, for work items that already have a bundle and
children: they start waiting for their unwrapped children as soon as the new
aihub runs.

Rollback is still swapping the aihub binary, with one new limit: once a
bundle-bound parent with children exists, the old aihub loses the wait —
the parent becomes claimable and enters the ready queue before its children
merge — and its rules refuse new writes on that data (another child under
the bound parent, `PARENT_NOT_REGISTRY`; a bundle bound to a ticket with
children, `HAS_CHILDREN_NO_BUNDLE`; a detach, `CORE-400`). Before rolling
back, list the bound parents with unwrapped children and either accept that,
or keep each one waiting with an explicit edge from each bundle-bound child
(`pf file dep add <child> <parent>`), which both aihubs honor.

## v0.2.3, corrections to the v0.2.2 upgrade notes

Five sections above say v0.2.1 in their headings but belong to v0.2.2:
each was appended after the v0.2.1 tag and first shipped in the v0.2.2
release. They are "`pf step ask` answers -h/--help itself", "the gate
falls back to the lane's scope engine", "the develop merge done by a
person (`steps.<project>.merge.by: human`)", "the task concurrency
bound is a Host key" and "a bundle-bound ticket can be a parent"; a
section that documents a v0.2.1 feature predates the tag, so only those
five headings misdate their content. The pin's comment list in
`test/w254_docs_set_acceptance_test.go` still describes the five with
their v0.2.1 labels, and the w77 contract test pins the bundle-parent
heading (`v0.2.1, a bundle-bound ticket can be a parent`) verbatim, so
the older labels stay visible there — this section is the correction
of record: read each of the five as a v0.2.2 section.

A v0.2.2 pf or daemon that writes a ticket's analysis back to an aihub
server still on v0.2.1 gets the whole write refused: the request carries
the new `expected_updated_at` field, and the older server rejects the
unknown PATCH key before its transaction starts —
`CORE-400 IMMUTABLE field "expected_updated_at" is not patchable`,
nothing written. Two writes carry the field: the daemon's automatic
analysis write-back (the same route as `pf task analysis-write`) and
`pf file analysis confirm`; `pf file edit` does not carry it and still
writes against the older server. Deploy the new aihub first, then
upgrade the pf clients — and an aihub rolled back to v0.2.1 leaves the
v0.2.2 automatic analysis write-back refused until it is upgraded
again.

## v0.2.3, the scratch layout categories

`scratch/` gets a fixed category layout (polyforge#121): `raw/` for raw
data (batch JSON, logs, downloaded archives, history exports — kept
3 days), `tasks/` for a background task's other disposable output
(kept 14 days) and `sessions/` for a session's other intermediate
output (kept 14 days), each entry one directory per run, and no loose
file at the top level. The old rule — `pf doctor --prune-scratch`
deleting any top-level entry older than 14 days — is replaced: entries
outside the categories are only ever reported, whatever their age, and
the prune removes only a category's stale batches (judged by the newest
mtime or ctime of the whole batch subtree, strictly past the retention).
After upgrading, run `pf init` again in every workspace: it creates the
three categories and refreshes the notes file's layout section, and
`pf doctor` warns a workspace whose section is still an old one. A
dispatched background task is handed its directories at spawn —
`PF_SCRATCH_DIR` and `PF_SCRATCH_RAW_DIR` in its environment and the
same absolute paths in its prompt — so nothing depends on the old flat
layout.

## v0.2.3, the memory writer columns

polyforge#107: memories gain their writer attribution — `writer_kind`
(the daemon-stamped writer class, `session` or `step`; `NULL` on rows
written before this release and on requests that send none) and
`writer_details` (the writer's JSON detail object) — and every memory
read face also serves the owner's display name (`owner_display_name`,
read live from the users table, so an admin rename shows on the next
read). **Upgrade the aihub server first and run the migration**: the new
server reads both columns on every memory path, so on a database that
has not been migrated every memory request fails with a SQLite error.
A `deploy.sh` run applies the SQL below inside its switch window by
itself (its probe skips a database that already carries the columns); a
manual deploy runs it by hand first, as one transaction:

```sql
ALTER TABLE memories ADD COLUMN writer_kind TEXT CHECK (writer_kind IN ('session','step'));
ALTER TABLE memories ADD COLUMN writer_details TEXT;
UPDATE memories AS m SET writer_kind = 'step',
    writer_details = json_object(
        'work_item', s.wi, 'step', s.step, 'attempt', s.attempt)
  FROM (SELECT id,
          substr(k, 1, instr(k, ':') - 1) AS wi,
          substr(substr(k, instr(k, ':') + 1), 1,
                 instr(substr(k, instr(k, ':') + 1), ':') - 1) AS step,
          CAST(substr(k,
                 instr(k, ':') + instr(substr(k, instr(k, ':') + 1), ':') + 1)
               AS INTEGER) AS attempt
          FROM (SELECT id, substr(idempotency_key, 16) AS k
                  FROM memories
                 WHERE idempotency_key LIKE 'pf-step-memory:%') AS keyrows
         WHERE instr(k, ':') > 0
           AND instr(substr(k, instr(k, ':') + 1), ':') > 0) AS s
 WHERE m.id = s.id;
```

These are `deploy/sql/v0.2.3/upgrade.sql`; the same directory's
`probe.sql` answers 0 when a database already carries the columns. The
one-time backfill in the same file marks exactly the rows a step
dispatch wrote — those carrying the daemon's idempotency key
`pf-step-memory:<wi>:<step>:<attempt>` — as `step`, with the work item,
step and attempt parsed out of the key; every other row keeps `NULL`.
The rollback is a no-op by decision: the writer attribution is durable
data, the columns are never dropped, and the v0.2.2 binary keeps reading
and writing the table unchanged beside them (its statements name their
columns) — so the probe keeps answering 0 after a rollback and a
re-deploy correctly skips the SQL. Work machines are unaffected until
they upgrade: an old `pf`/daemon sending no writer fields stores `NULL`,
and the new fields in the answers are ignored by old clients.

## v0.2.3, harness guidance: the concise-output block, doctor's claude target and the installer's auto-fix

Three changes reach the harness context files and every dispatched prompt
this upgrade:

- every step dispatch and every task input (a normal task, the ticket
  auto-analysis, the message analysis) now carries a short
  output-discipline rule — one English source in the binary
  (`harness.ConciseRule`), the same bytes everywhere, riding the prompt
  right after the prohibited actions;
- `pf doctor` gained the `harness concise guidance` row beside the
  diagram row: it warns while an installed harness's context file lacks
  the concise-output block, and `--fix-harness-prompts` appends it —
  append-only, idempotent, never creating a file. Claude Code's target is
  resolved: `~/.claude/CLAUDE.md` when it exists, else
  `~/.claude/AGENTS.md` — the file Claude Code actually loads, so a
  machine with only the AGENTS.md gets the fix there;
- `tools/install.sh` runs `pf doctor --fix-harness-prompts` once per
  install or upgrade (it is idempotent), so the blocks reach machines
  that already carry harness context files without a manual step.

Rollback for either block: delete the lines between its two markers
(`polyforge:diagram-guidance:svg:` / `polyforge:concise-guidance:`) —
nothing else changed in those files.

## v0.2.3, the dependency audit declares its level and the release reuses the gate's audit

The dependency-audit gate audits production dependencies from the declared
level up: this repository declares moderate (`.polyforge/verify-scope.conf`,
the new `audit_prod_level` key — the only conf key that reaches the gate, and
it only tightens; the value may be moderate or low, never above the default
high). Each npm surface then runs a second, production-only audit
(`--omit=dev`) whose moderate and above are findings; dev dependencies stay
at the high threshold, and a repository that declares nothing is unchanged.
The gate id hashes the key (`tools/gate-id`, the lane's `pfstep/gateid.py`),
so a green tag of one level is never reused for another; main's CI
full-audit job runs the same command, `--npm-prod-level moderate` included.

The release no longer audits one hardcoded tree at the high level: its
default audit is the scope engine's own `audit` command, taken verbatim from
`verify-scope --json --full`, run after the binaries build (govulncheck
loads every Go package, so a resume that built no binary builds the console
SPA's go:embed tree first) and never skipped by a reused green tag. A tree
the engine reports no audit command for refuses. `RELEASE_AUDIT_CMD` still
replaces the whole gate.

Every audited surface now prints when its advisory data was fetched (UTC)
and its source (`surface <name>: advisories fetched <time> from <source>`):
the npm registry the surface's npm is configured against, or
`https://vuln.go.dev` for the Go module.

Nothing to do on the work machines: the engine ships with the release and
reads the conf from the repository.

## v0.2.3, the forensics standing rule

The routing skill gains its ninth standing rule (polyforge#122):
forensics — raw material lands on disk, the conversation carries
conclusions and references. Six duties: a command's full output, a
large response or an export is saved complete into a dedicated
subdirectory of the workspace's temporary directory before it is
quoted (the conversation carries counts, differences and short
excerpts only); history is located first and then read by range;
one logical tool result carries at most 8 KiB of visible text, a
longer body going to disk with an excerpt read back and the
truncation stated; already-read material is referenced instead of
reloaded while a redirected command's exit code is captured and
kept; a report filed where it belongs is later referenced by
conclusions and pointers only; and a task prompt or a sub-agent
brief states the same constraints, naming the raw directory. The
`pf task` drafting list and the develop build's sub-agent brief
rule carry them bound to their own directories (a task's raw
batch, a step's `downloads/`). No action on upgrade: skills are
wired by reference from the release checkout, so sessions pick
the rule up at their next start.

## v0.2.3, the epic lane (polyforge#117)

The new `epic` lane runs one work item per epic: verify-children → sync
→ build (`rewind_only`: the fix build runs only after a rewind covers
it, inheriting the plan) → review (the epic mode: the total acceptance,
the children's landing records and PR bodies, the seams and the final
diff — never a child's internals) → verification (the full bar
explicitly) → publish → merge. The gates after build rewind to it;
merge's moved-base FAIL goes back to sync (the same uncounted class
develop declares, its own cap of 2), and the lane re-runs from there.

A lane declares it with the new `epic` key (which must name
`children_branch` too): its OWN work items are epics, each checking out
the children branch of its own seq — recorded as
`runtime-state/target-branch.json`'s `checkout` key with NO `target`, so
the epic's merge target is the origin's default branch and every step
script keeps its default-branch logic. The first claim also snapshots
the epic's children (ids, slugs, statuses, landing records verbatim)
into `runtime-state/children.json`, immutable while the epic runs; the
driver re-checks it against aihub before every dispatch, and a changed
child parks the workflow blocked — the daemon asks nothing. A re-claim
over changed children writes the next snapshot version beside
`runtime-state/epic-resume.json`, which the driver lands as one
owner-class rewind to the step the lane's `resume_reset` names (the
epic lane names its sync step; charged to no budget), so the affected
steps re-run over a tree that first takes the changed set's commits on
the epic branch in (the epic's sync merges its own checkout branch
before the default branch). The hub's work-item list gained a `parent` filter
(`GET /work-items?parent=<id>`); the daemon's runtime reads an epic's
children through it.

The epic's merge registers ONE landed record — `state: landed`, the
integrate pool's own — with a `children` key carrying the claim's
snapshot verbatim: each child keeps its own `landed_on_target` evidence
on its own work item and never enters the pool; a children snapshot a
step cannot read is not judged (exit 2). Sub-tickets are untouched: a
sub-ticket's claim still reads the PARENT's lane's `children_branch`
and merges into the children branch, never the default branch.

## v0.2.3, the light tier's selection closes its historical misses (harness carries and scoped_command)

Five changes of this release reached the develop lane's light verification
green and broke only at the integration full gate (polyforge#87's five, each
with its own fix ticket). The engine now consumes what the repository had
declared all along, and a new conf key maps what no analyzer's own rule can
express. Both are selection rules of the shipped engine
(`workflows/_shared/scripts/verify-scope`); neither changes any tier's
commands by itself, and both are guarded by a replay corpus
(`test/w111_light_historical_replay_test.go`) that reds when a rule quietly
regresses — or quietly balloons.

- In a repository whose conf declares `go_harness=`, the harness directory's
  `*_test.go` files' `carries:` declarations are consumed the way the npm,
  python and rust analyzers consume their suites': a changed path a
  declaration covers selects that file's tests through the harness command,
  beside the literal-reference names (one `E2E_RUN='^(…)$' sh <go_harness>`
  command); a malformed declaration fails closed, and a harness test without
  one is unchanged. Outside the declared harness directory a Go test file's
  `carries:` comments stay comments.
- `scoped_command=<glob>|<command>` (repeatable) adds a conditional command
  to the scoped tier's report: a changed path an `exclude=` match has not
  removed that matches the glob adds the command, once per declaration, after
  the analyzer's own scoped commands. The light tier included; `--full`,
  `--micro` and no-analyzer reports never carry it; a value with no `|` or
  an empty side is diagnosed and fails the conf closed. This repository
  declares one: `workflows/_shared/scripts/*` selects the step scripts'
  Python unit suite (CI parity, `python3 -B -m unittest discover`), which no
  tier selected before — polyforge#36's PoolTest miss.

An upgrading machine does nothing: the engine ships with the release and
reads the declarations from the repository. A repository's own conf may
declare `scoped_command=` lines the same way; the engine of an older release
diagnoses the key as unknown and ignores it, so a conf may declare them
before this release ships.

## v0.2.3, the epic split filing procedure and the split metrics (polyforge#118)

The routing skill's file verb gains the owner-confirmed split: a
queued, never-claimed ticket whose pre-submit analysis recommends an
upgrade is, on the owner's confirmation, held by its first sub-ticket
(a bound parent waits for its children — out of the ready queue,
`DEPENDENCY_BLOCKED`), moved to the epic lane with `pf file edit
--bundle epic`, and split per analysis unit into sub-tickets filed
`--parent` with the split attrs (`split.origin`, `split.unit`) —
check-first per unit, so a re-run files no duplicate — and the sibling
order as `--after` dependencies written at create; the checklist is
verified against the confirmed decomposition before the children are
released. The docs follow (concepts' Work item term, bundles'
children_branch bullet, the config reference's epic thresholds).

tools/review-firstpass gains `--cohort`: an owner-confirmed cohort
manifest groups exported capsule runs — unsplit control, split
children, the epic aggregation listed apart — prints the #1 background
baseline the manifest carries, and reports, beside the first-pass
table, the rework share of proxy execution time (a rewind section's
named steps' later rounds over all stamped attempt time; counted and
owner/uncounted rewinds and infra-retry rounds listed apart) and the
claim-to-merge duration (the merge step's end as the labeled proxy;
the real merge moment is N/A offline). No action on upgrade beyond
picking the texts up: the skill is wired by reference, sessions load
it at their next start, and the metrics tool reads exported capsule
trees and touches no protocol.

## v0.2.3, the integration batch's in-batch repair

The integrate lane grows two steps between `collect` and `revert`, both
`rewind_only` (skipped without a dispatch until a rewind covers them):
`repair` and its `review`. A red full gate whose every failing unit is
attributed and fixable by touching only tests or documentation no longer
files a fix ticket first — the gate writes the units to a repair brief
and hands the batch back with a charged FAIL, the repair fixes them on
one branch per repo off the pinned heads, the review judges the branch,
and the revert step lands it as a PR onto the default branch under the
merge lock it already holds; the gate re-runs on the fix's merge and a
green tag lands on the default branch, so one batch covers its own fix.
The rounds are bounded by the lane's one `max_rewinds: 3` (a FAIL not
citing the owner is refused once it is spent, and the gate takes the
spent budget to the owner: one more round, close red — the remaining
reds filed as fix tickets exactly as before — or cancel). A red out of
the repair's scope (a product change, pre-existing, unattributed, a
source past two fix rounds, an audit red) still files fix tickets or
goes to the owner; an owner's revert after a red rewinds to `revert` by
name (`rewind --to revert --per-owner`); a project that declares its
merge done by a person (`steps.<project>.merge.by: human`, frozen into
the capsule's step modes at the claim) gets no in-batch repair — its
PRs await the person, so the gate files the fix tickets as before. The
batch's record gains `fixes` (repo, branch, PR, merge, the fixed units
and their attributed items) beside `reverts`. No action on upgrade: the
lane and its skills ship with the release; a workspace's
`steps.<project>.merge.by` is read through the claim's frozen step
modes, which the first claim after the upgrade freezes.

## v0.2.4, the rebuildable build products a doctor switch reclaims at once (polyforge#127)

`pf doctor` gains the `rebuildable artifacts` row and the
`--prune-rebuildables` switch: the declared rebuildable build products —
the directories a repository lists at its root in
`.polyforge/capsule-gc.conf`, one `rebuild_dir=` line per
repository-root-relative directory literal — are reported per mapped
workspace (the logical bytes inside the terminal work items' capsules,
what a prune frees) and removed at once by
`pf doctor --prune-rebuildables`, without waiting for the 14-day
retention, which stays exactly as it is. A declaration is read from the
mapped workspace's `.repo/<name>` clone's committed default branch (no
fetch, the commit recorded in the report) — the owner's Q3=B ruling, so
capsules cloned before a repository declared anything are cleaned too;
a working tree is never read, and a repository that declares nothing
changes nothing. A declared directory is removed only when everything
under it is git-ignored and no tracked file, no untracked-not-ignored
file and no nested repository sits under it, no path component of it is
a symlink and none of it is the repository's own `.git`;
`artifacts/`, `capsule.json`, the git dirs, the local commits and the
git status are never touched (a repository with unpushed commits loses
its ignored build products and nothing else), a capsule of a work item
still in flight, one a daemon job holds and one whose state cannot be
read is kept and named, and nothing is removed while the daemon is down.
This repository's own declaration ships with the switch: `pi-web/.next`
and `pi-web/node_modules`, the two directories that dominate a terminal
capsule's bytes and rebuild by the fast gates. No upgrade action beyond
taking the new `pf`; the default `pf doctor` run still writes nothing.
Run the row report-only once before the first `--prune-rebuildables`,
the same discipline the first `--prune-capsules` asked for.

## v0.2.4, the local development channel (polyforge#136)

`tools/install.sh --dev` installs, on this machine only, the latest
green-tag commit of main — built locally through the new
`make dev-build` entry, versioned `v<next patch of the latest release
tag>-dev.<N>` (`N` the first-parent commit count from the release tag;
re-installing the same green tag derives the same `N`, and main only
fast-forwards; over the v0.2.3 line that is `v0.2.4-dev.2` two commits
past the tag), never published anywhere and refused in prepare when the
release-tag..green diff touches an AIHub-side path of
`tools/aihub-paths.conf` — those sets change the server, and the two
AIHubs do not follow dev builds; they need a formal release. The version
string carries no `+build` metadata, so the installer's downgrade guard
keeps comparing it: after a dev install the formal channel is refused
until the next formal release (an ordinary upgrade then), and the
source commit rides its own field — `pf --json version`'s `commit` and
`pf doctor`'s new `pf build` row name it. No action on upgrade beyond
taking the new `pf`: a machine that wants the channel re-runs its
installer with `--dev`, and `docs/install.md` documents the
prerequisites (go, make, npm, node of the checkout's `.nvmrc` major,
all checked and reported together) and the way back.

## v0.2.4, the per-clone-path cache volume (polyforge#153)

The verification containers' named cache volume is keyed by the clone's
path beside the image's generation:
`pf-verify-cache-<repo>-<image hash>-<clone path hash>`, and the clone is
mounted at its canonical path — two clones of one repo at different
paths never share one volume, so a path-sensitive cache tool
(golangci-lint's result cache) no longer reads another clone's cached
results, while the same path keeps reusing its own volume. Volumes of
the pre-#153 shared shape — `pf-verify-cache-<repo>-<32 hex>`, one field,
no path hash — are neither read nor removed by the new scripts: the
pruning rule recognizes only the two-field shape, so old and new
scripts may run beside each other, each pruning its own shape. To
reclaim the old shared volumes once no old script runs any more: list
them (`docker volume ls --quiet | grep -E '^pf-verify-cache-.+-[0-9a-f]{32}$'
| grep -Ev -- '-[0-9a-f]{32}-[0-9a-f]{32}$'` — a name ending in one
32-hex field, not the new shape's two; a repo name may itself carry a
dash),
confirm no container references one (`docker ps --all --filter
volume=<name>` answers empty — a stopped container still counts), then
remove them by hand with `docker volume rm`; nothing else is removed
automatically. The new shape accumulates one volume per clone path per
image generation — every capsule and every integrate batch has its own
path — and each start still prunes only its own repo's older
generations; the disk cost is the price of the isolation, and no time-
or count-based reclamation ships with it.

## v0.2.4, the release lane ships the green integrate batch (polyforge#154)

The release lane no longer releases a branch head: its object is the
project's most recent green integrate batch, resolved by the lane's own
command (`workflows/_shared/scripts/release.py`, reading the project's
batches through the step's hub) and refused — before any version is
claimed, with a named code — when there is no green batch
(`PF-RELEASE-NO_GREEN_BATCH`), when the owner repo's pinned commit
carries two or more release tags or a release tag appeared elsewhere
after the pre-release list was recorded (`PF-RELEASE-ALREADY_RELEASED`),
when a batch pin left the default branch (`PF-RELEASE-PIN_OFF_DEFAULT`),
when the batch names no commit for the owner repo
(`PF-RELEASE-UNPINNED_OWNER`) or when two repos both carry the release
procedure (`PF-RELEASE-AMBIGUOUS_OWNER`).

What a project must change — its own `.polyforge/release.sh`:

- Accept `[bump] --pins <file>`: the pins file is the batch's whole
  group, `{"batch": "<id>", "repos": {"<repo>": "<sha>", ...}}` (plus
  `"red_batches_after": [...]` when red batches closed after it). The
  script releases ITS repo's pinned commit: require the clone to sit at
  it (HEAD is one of the pins' shas) and require it on the origin
  default branch's history — a pinned commit the branch has moved past
  still releases, exactly the shape the old origin-head release could
  not. A run without `--pins` must refuse, never fall back to a head.
- Declare the convention: the script's `--help` output must contain
  `--pins`. The lane probes it and refuses a script that does not
  declare it with `PF-RELEASE-PROCEDURE_CONTRACT`, before the script
  runs — an old-convention script would read the pins file as a bump
  argument or ignore it and release a branch head, so it is never
  handed one.
- Stop running a test gate: the batch's own full-gate run is the test
  evidence, and the release notes name the batch (polyforge's own script
  dropped its green-tag check and its containerized gate; what it kept
  is the machine preflight, the design-doc cross-reference gate, the
  PR-only and workflow-contract checks and the dependency audit after
  the build).
- The resume contract keeps its shape: a pinned commit that already
  carries exactly one release tag resumes that version, whoever claimed
  it — the lane adopts a complete one and finishes a partial one, and the
  release record carries the batch's id and its whole pins for `verify`.

A project that does not change its script cannot release after the
daemon upgrade that ships this: the resolver still finds the green
batch, but the procedure-contract probe (`--help` lacking `--pins`)
refuses with `PF-RELEASE-PROCEDURE_CONTRACT`, nothing built or published.
Make the four changes above and file the release again.

The order cuts both ways, and the first release across it is always a
hand run. A daemon still on the pre-#154 release lane calls the
procedure without `--pins` (from the origin head, per its old step
text), so a script changed first refuses it (`--pins <file> is
required`) until the daemon's lane text carries the resolver — which
arrives with the polyforge release that ships this change, installed by
`pf daemon ensure`. In that window no release goes through the lane, by
design; cut the release by hand: take the latest green batch's pins
(`{"batch": "<id>", "repos": {"<repo>": "<sha>", ...}}`), sit the owner
repo at its pin and run `.polyforge/release.sh [patch] --pins <file>`
yourself (the same way polyforge's own first release across this change
is cut — the reference page's "Two halves" paragraph says so).

## v0.2.4, the build fast check's flaky re-run and the red-state limit (polyforge#157)

The workflow's own scripts changed, and a lane claimed before this keeps
running its claim-time snapshot: the new rules reach a work item through
the workflow version its lane snapshot carries, never a `pf` release.
What changed under `workflows/_shared/scripts`:

- `fast_checks.py --repo` re-runs a red command's located failing units
  once each, by their derived targeted re-run (the same rules
  verification's bar runs), and a pass is recorded flaky beside the
  first-red log; a red command every failing unit of which re-ran flaky
  is confirmed — by its first-run output's completion proof (the
  declared Go harness's trailer, an aggregate e2e runner's closing
  summary) or by one whole re-run exiting 0 — and only such a command
  turns green. A command its own timeout killed skips the re-run phase
  and stays red.
- `fast_checks.py --check-units` re-runs a red unit declared
  `pre_existing_red` once (a pass records it flaky with both logs); a
  unit the lane just wrote is never re-run there — red it stays. A
  unit's self-reported `timeout` in `artifacts/build/units.json` (a Go
  duration; an invalid or non-positive one makes the file unusable)
  bounds its recorded run, else a shared 10m default — the bar
  commands' own 20m bound no longer applies to a recorded unit run.
- `facts.py build --replay` bounds each replay the same way — the
  unit's self-reported `timeout`, else `--unit-timeout` — and a
  red-state run its own limit kills is an evidence problem: exit 1, the
  problem naming the unit and its limit (a red-state run must exit
  non-zero within its limit, never hang), no longer the old "not
  judged" retried. Only a kill by the whole run's deadline stays not
  judged. `verify.py --unit-runs` reads the same self-report with the
  same default.

What a project must change — nothing: the rules are the workflow's own,
project-neutral, and the slow red run of a project that needs longer
self-reports a higher `timeout` in its `units.json`.

## v0.2.4, the split attrs one shape and the data migration (polyforge#174)

`pf file create --attr <path>=json-value` writes a dotted path nested —
`--attr split.origin=X --attr split.unit=Y` stores `attrs` as
`{"split":{"origin":"X","unit":"Y"}}` — the same dot path `pf queue
--attr` filters on, and the aihub create boundary refuses a top-level
attrs key that carries a dot (`CORE-400`; the CLI nests dotted paths
itself, a `--stdin` body must carry the nested object — the `detail`
spells it). One shape, write and read: the epic split's duplicate
check, which the flat-key defect had never let match, works from this
release on. Deploy the aihub server first, as ever: an old CLI's
dotted `--attr` create fails loudly on the new server until the work
machine takes the new `pf` — an explicit failure instead of the old
success whose ticket no query could ever find (a new `pf` keeps
working against an old server: the nested shape is plain attrs to
it).

The v0.2.4 window also moves the OLD rows: `deploy/sql/v0.2.4`
(applied by `.polyforge/deploy.sh` inside the switch window, run-once
by its probe) migrates the flat `split.origin` / `split.unit` attrs
keys of every project and every status to the nested dot paths — other
attrs, other dotted keys and statuses stay, the ticket ids stay, and
after the deploy the split check's own query (`pf queue --project P
--attr split.origin=… --attr split.unit=…`) lists the old sub-tickets
again:

```sql
-- deploy/sql/v0.2.4/upgrade.sql — the split attrs migration
-- (polyforge#174): the one-time move of the flat `split.origin` /
-- `split.unit` attrs keys to the nested dot paths every reader walks
-- (the list filter's json_extract, the attrs write's `if`), so the
-- tickets the pre-fix `pf file create --attr` filed become listable by
-- the split check's own query again. Scope: the WHOLE database, every
-- project and every status, exactly these two keys — other attrs,
-- other dotted keys and statuses are not touched. Refusal: a row whose
-- nested `split` shape conflicts (a same-key different value, or a
-- `split` parent that is no object) refuses the WHOLE batch — one
-- transaction, nothing applied; resolve the row by hand and re-deploy.
-- Rollback reads the before-image table split_attrs_before_w174, which
-- holds exactly the rows this migration changes.

-- 1. the whole-batch refusal, first: one conflict aborts the
--    transaction (the CHECK fires on the inserted 'refuse' row).
CREATE TABLE w174_refuse(x TEXT CHECK (x <> 'refuse'));
INSERT INTO w174_refuse (x)
  SELECT CASE WHEN
         (json_type(w.attrs, '$.split') IS NOT NULL
          AND json_type(w.attrs, '$.split') <> 'object')
      OR (json_type(w.attrs, '$."split.origin"') IS NOT NULL
          AND json_type(w.attrs, '$.split.origin') IS NOT NULL
          AND json_extract(w.attrs, '$."split.origin"')
              IS NOT json_extract(w.attrs, '$.split.origin'))
      OR (json_type(w.attrs, '$."split.unit"') IS NOT NULL
          AND json_type(w.attrs, '$.split.unit') IS NOT NULL
          AND json_extract(w.attrs, '$."split.unit"')
              IS NOT json_extract(w.attrs, '$.split.unit'))
       THEN 'refuse' ELSE 'ok' END
    FROM work_items w
   WHERE json_type(w.attrs, '$."split.origin"') IS NOT NULL
      OR json_type(w.attrs, '$."split.unit"') IS NOT NULL;
DROP TABLE w174_refuse;

-- 2. the before-image: exactly the rows this migration changes, their
--    pre-migration attrs whole (the rollback's whole input).
CREATE TABLE IF NOT EXISTS split_attrs_before_w174 (
  id    TEXT PRIMARY KEY,
  attrs TEXT NOT NULL
);
INSERT OR REPLACE INTO split_attrs_before_w174 (id, attrs)
  SELECT id, attrs FROM work_items
   WHERE json_type(attrs, '$."split.origin"') IS NOT NULL
      OR json_type(attrs, '$."split.unit"') IS NOT NULL;

-- 3. the move: each flat key becomes the nested dot path it names
--    (json_set creates the missing `split` object, or writes into the
--    existing one — the guard already refused every conflicting
--    shape, so a same-key double shape keeps one nested copy), and the
--    flat key goes.
UPDATE work_items
   SET attrs = json_remove(json_set(attrs, '$.split.origin',
                        json_extract(attrs, '$."split.origin"')),
                           '$."split.origin"')
 WHERE json_type(attrs, '$."split.origin"') IS NOT NULL;
UPDATE work_items
   SET attrs = json_remove(json_set(attrs, '$.split.unit',
                        json_extract(attrs, '$."split.unit"')),
                           '$."split.unit"')
 WHERE json_type(attrs, '$."split.unit"') IS NOT NULL;
```

A row whose nested `split` shape conflicts — a `split` parent that is
no object, or a nested `split.origin` / `split.unit` of another value
than the flat key — refuses the whole batch: the window's one
transaction applies nothing and the deploy aborts; resolve the row by
hand (keep one value, drop the other shape) and re-deploy. The
automatic rollback of the same window restores exactly the changed
rows from the `split_attrs_before_w174` before-image table the upgrade
writes, then drops it — a row the migration never moved is never
flattened. After a successful deploy the before-image table stays as
the durable record of what moved: the post-deploy comparison reads
it, and once that comparison is signed off a person may DROP it — no
code reads it, and it serves nothing else.

Before authorizing the deploy, review the blast radius read-only (the
probe counts only the split pair) and keep the list for the
post-deploy comparison — the accepted delivery's last half re-runs the
split check's query per original id and expects exactly the old ids:

```sql
-- the tickets the migration moves (whole database, every project and status)
SELECT id, project_id, status, attrs FROM work_items
 WHERE json_type(attrs, '$."split.origin"') IS NOT NULL
    OR json_type(attrs, '$."split.unit"') IS NOT NULL;
-- the inventory of OTHER dotted top-level keys (reported only — NOT migrated)
SELECT w.id, je.key, w.project_id FROM work_items w, json_each(w.attrs) je
 WHERE je.key LIKE '%.%'
   AND je.key NOT IN ('split.origin', 'split.unit');
```

## v0.2.4, the build close's scoped selection (polyforge#164)

What changed under `workflows/_shared/scripts`:

- `fast_checks.py --repo <name> --close-scoped` is the build close's own
  mode: it plans the same light-tier selection verification's light tier
  plans (one shared implementation, `pfstep/scoped_plan.py`, which
  verify.py's own light tier now plans through) over the repo's final
  tree and executes it on the host, reusing the green same-tree runs a
  `--reuse` result file records instead of re-running them; a selection
  the engine answers with the full tier, or a selected command that
  cannot run, records `incomplete` with the reason kept and exits
  non-zero, claiming nothing. The develop build step runs it once per
  touched repo after its fast checks — for a work item whose
  verification runs the light tier only (an integration batch's fix
  ticket runs the full bar and closes without one) — and a red or
  `incomplete` result never hands off `done` without the owner's
  ruling, so a repository whose light selection falls back to the full
  tier (a `stack=none` pin, a changed lockfile, no analyzer) stops at
  the owner where it never did before.
- The change analysis (`artifacts/build/change-analysis.json`) gains a
  `selected` section — the close's machine selection record, one entry
  per task repo (head, tree, base, default branch, the engine
  invocations, the conf's identity, the selection's paths and suites,
  every command with its evidence, and the status: green, red or
  incomplete with its reason). `facts.py analysis` and `verify.py
  --analysis` judge the records an analysis carries by the one shared
  validator: a stale head, a non-green selection or a command without
  green evidence is red — a sync's merge commits between the record's
  head and the verified HEAD leave it valid — and the records are never
  subject to the supplement cap; whether a touched repo carries one at
  all stays the build step's own rule and review's judgment.

What a project must change — nothing: the close runs the same
project-neutral selection verification runs, and a repository whose
light selection answers the full tier sees the same stop at the owner
that its verification's own full bar always was.

## v0.2.4, the development channel's graded AIHub-side preflight (polyforge#188)

What changed under `tools/` and the dev channel of `tools/install.sh --dev`:

- `tools/aihub-interface-paths.conf` is the new client-server interface
  list, read from the green commit's tree beside the total list
  `tools/aihub-paths.conf` (one list each, both readers: the tests
  generate their fixture paths from the same files). A
  release-tag..green diff touching an interface path
  (`internal/common/api/**`, `internal/common/protocol/**` — a
  conservative path rule, not a compatibility proof) still refuses the
  dev install in prepare; touching only the total list's other paths —
  the server's insides, the SQL migrations, the deploy tree — passes
  with one WARN naming the formal tag, the green tag and every hit
  path: the two AIHubs stay on their formal release and those changes
  wait for the next formal one.
- The preflight is fail closed on its input: a green tree whose interface
  list or total list is missing, declares no entry, or carries an
  unsupported entry refuses the install with nothing on the machine
  changed, and so does a changed path git cannot print unambiguously;
  renames are judged on both their ends (no rename folding).
- No action on upgrade beyond taking the new pf: the change is
  installer logic and repository declarations. A dev install over a
  green tag that predates the interface list now refuses by design —
  wait for the next green tag; the one-time bypass copy that installed
  v0.2.4-dev.19 over green/20261120-1 is retired with this change.

## v0.2.4, the build close runs review's replay and the plan maps the analysis (polyforge#181)

- The develop build's close no longer stops at the static facts: before the
  hand-off it runs the same lines review judges with — `facts.py
  plan-conformance` and `facts.py build --replay --isolation host` — beside
  the `carries` line it already ran, a red fixed before the hand-off; an
  exit 2 that stays 2 after its retries (one minute apart, at most two) is
  disclosed in the hand-off and left to review to re-run, never judged
  blocked. The shared build skill carries the same close, conditioned: a
  lane whose fix build inherited its plan (the epic lane's) skips the
  plan-conformance line and runs only the replay. The develop build step's
  line budget rises from 190 to 215 lines for the two additions, the pinning
  contract test moved with it.
- The plan `build` writes gains a `## 分析对照` section on a ticket that
  carries a `## 分析` section: each item the analysis lists — each old test
  and document obligation, each risk, each decided point (`已定`) — is
  mapped to the unit that carries it or a stated reason it is not taken; a
  decided point takes precedence over the analysis's recommendation, and an
  analysis written against an older base has its file:line references
  re-checked against the current one. A ticket without `## 分析` writes
  `none` there.

What a project must change — nothing: the close's lines and the plan's
section are the workflow's own texts; no script, conf key or command
surface changed.

## v0.2.4, the build fast check reuses green runs by key and the per-unit bar narrows to the touched roots (polyforge#182)

- `fast_checks.py --repo` reuses a green command instead of executing it
  when its key — the command text, its root's subtree tree hash of the
  working tree (a private index over tracked modifications plus
  untracked, unignored files; the repository root's key counts the whole
  tree, nested roots included), its sorted declared `requires_env`/
  `requires_cmd` prerequisites — is unchanged from the previous run in
  the same result file: the entry is recorded `reused`, not executed,
  its original log path and `exit_code: 0` kept, and a green entry of a
  root or command the run never plans is carried in the file for a later
  run or the close to reuse. A red, timed-out, never-started or
  flaky-confirmed result is never reused, and a previous file that is
  unreadable or not a usable result is no cache — everything runs, never
  a usage error.
- The per-unit bar narrows: `--repo <name> --phase fast --root <dir>`
  (repeatable; `.` the repository's own root, a nested root its
  directory) runs only the named roots' fast commands — the scoped
  selection still runs once per touched repo at the close — and the
  logs move one directory per run (`<log-dir>/<n>/<repo>/<seq>/`), so a
  later run's executions never overwrite a reused entry's original log.
- The close's `--close-scoped` reuse is keyed the same way (one key, two
  modes): the whole-repo head comparison is gone, a commit outside a
  nested root keeps that root's green runs reusable, and the per-unit
  file's own `reused` and `carried` entries are accepted evidence.

What a project must change — nothing: the command's flags and result
shape are the workflow's own; no conf key, pf subcommand or schema
changed.

## v0.2.4, review-firstpass delivery metrics and trial arms (polyforge#209)

- `tools/review-firstpass --events <events-dir>` adds the delivery block over a Cell's exported event files: the develop lane's completed run set (an absent event file is `events missing`, its event fields `unknown`; events without a completed terminal are `not completed (excluded)`), wall clock median and nearest-rank p90, owner-wait-adjusted effective time, per-file delivery time (approved size = the first build section's start to the last build before the first sync; git numstat across the repos), rework share (every rewind's named gate to that gate's next `step_done`; a dispatch ends at its step's `step_done`/`step_failed`/`gate_verdict`, the next same-step `step_started`, a rewind or the terminal, with the owner wait inside it subtracted), first pass, infra failures per model+effort (a `step_failed` owned by the same step's most recent `step_started`) and model-class candidate switches with the 503 subcount. A ticket with no measured build dispatch is `unknown`; a missing transcript or an empty window is that dispatch's unknown, never an estimated number.
- The cohort manifest gains `"arms": {"<arm>": ["<capsule>", ...]}`: each arm prints the same tier / infra / switch / estimate summary, one detail row per run in the manifest's order, and the missing names (counted, excluded). An arm uses the delivery block's run set: develop only, a completed terminal or an absent event file enters, a run with events but no completed terminal is named `not completed (excluded)` and stays out of every number. Without `--events` the arm keeps size and first pass, the event fields are `unknown`, and the infra, switch and estimate lines say `n/a (no events dir)`.
- The estimate line is read only inside `## 分析` -> `### 1.`, one `估算：<repo> 新增 N 个文件、修改 M 个文件、单元 K 个` per repository; a malformed or duplicated line voids that repository only; the summary counts `estimate unknown` apart, with its names (the shared samples are polyforge#213's `internal/contracttest/testdata/analysis_estimate_lines.tsv`).

What a project must change — nothing: the tool is read-only, its new output is gated by `--events` / `arms`, and the pre-existing report and CSV stay byte-identical without them.

## v0.2.4, the hand-off gate: build and sync hand off done only with a passing fast check at HEAD (polyforge#211)

- The develop lane's `build` and `sync` declare the step key
  `fast_check_proof {result, command}`. Before `done`, the daemon reads the
  result file the declaration names — `artifacts/build/fast-checks/<repo>.json`
  for build, `artifacts/sync/fast-checks/<repo>.json` for sync — for every task
  repo the step's round moved: it must be a `pf-step-result/1` with `verdict`
  `pass` and `heads` carrying that repo at its current HEAD. A missing file, an
  unreadable result, a stale head or a non-pass verdict refuses `done` and names
  the repo, the failing check and the line that re-runs it. A repo left at the
  round's start commit and a fast-forward need none; a start the record does not
  carry needs one like a changed repo.
- A red the change did not cause — the default branch already red, an
  `error`/`partial` result, or a lane that planned no check — is released by the
  owner's word: the step asks, then hands off `done` with `--per-owner <the
  reply's at>` and the section's header records the cited message (the same
  release a spent rewind budget gives a gate's `fail`). polyforge#212 (C4) will
  later treat a triaged known red as passing inside the gate.
- Build's close re-runs the check with `--phase fast` after its last commit so
  the result's `heads` are the commits the gate compares; sync's skill runs the
  check after a merge that moved a repo (before `continue` on the working tree
  and again after it, so the merge commit's HEAD is recorded). The daemon still
  only reads files and git; it runs no check and knows no repository's command.

What a project must change — nothing: this is a new optional lane step key and
the develop lane's own declaration; no config key, `pf` subcommand or result
schema changed. A work item already in flight keeps its claim-time lane snapshot,
so it is not gated; a work item claimed after the upgrade runs the new build and
sync texts. A project whose default branch is already red will see its build and
sync ask the owner at hand-off instead of at verification.

## v0.2.4, the fast checks' baseline triage: a red the change's base carries is not this change's (polyforge#212)

- `fast_checks.py` (`--repo`, the per-unit bar and the hand-off gate's own
  command, and `--close-scoped`, the build close's scoped selection) triages a
  unit still red after its one flaky re-run on the change's baseline — the same
  judgment `verify.py`'s pre-existing phase runs, one shared entry
  (`workflows/_shared/scripts/pfstep/preexisting.py`, `judge_on_base`): a
  throwaway worktree of the merge-base, the report's declared `base_setup`, then
  the unit's own targeted re-run there. The classes land in the result file —
  `pre_existing` (the base fails the same way, its signature, the baseline
  commit, `details.baseline` and `details.reds[]` recording the judgment: not
  this change's red, the run exits 0 and the selection's `pre_existing` status
  is the shared validator's green-equivalent evidence in
  `facts.py analysis` and `verify.py --analysis`), `this_change` (the base lacks
  the test, or it passes there) and `undetermined` (anything the base runs
  could not settle — fail closed, the existing red rules hold and the owner's
  release path is unchanged).
- `--close-scoped` takes `--unit-runs <units.json>` (the build skill passes
  `artifacts/build/units.json`): the files its test commits touched are
  protected, never pre-existing — a test this change authored is this change's
  red. A pre-existing failure is excluded from its command's confirming re-run
  by the skip form verification uses (go `-skip`, `E2E_SKIP`, pytest
  `--deselect`); a command that cannot exclude it and has no completion proof
  keeps a residual whole-command failure.
- The hand-off gate (polyforge#211) needs no change: a triaged check exits 0,
  its `pass` verdict passes the gate — the gate's own red-release path
  (`--per-owner`) stays for what the base could not settle. What a project
  must change: nothing — the judgment, its records and the baseline worktree
  are the lane's own scripts; a work item claimed before this section keeps
  its claim-time lane snapshot and runs the previous behavior.

## v0.2.4, the subagent process lifecycle and the cleanup_failed park (polyforge#200)

Two owner-visible changes ship together. The daemon-owned pi subagent tool
gains a per-task `timeoutSeconds` parameter (a positive integer of seconds,
default 1800, measured from the child's actual start; 0, negatives and
fractions are refused before anything spawns): a timed-out worker is stopped
with its descendants — TERM, a bounded grace, KILL, a bounded wait — and the
result is a failure that says so (`timedOut` and the effective value beside
the existing details shape; the step's archive marks it failed). An aborted
or timed-out stop collects only that call's descendants, identified by a
per-call inherited `PF_SUBAGENT_ID` marker beside the work item's and the
daemon's markers.

A step-end process cleanup that cannot prove the step's processes gone now
parks the work item blocked, kind `cleanup_failed` — after its path's own
records are written: a received hand-off stays on the call ledger (the
resume re-runs the sweep and settles it without re-running the step), a
failed dispatch keeps its failure record and retry ladder (the resume
retries the step), an open ask keeps standing (the resume re-enters it);
`pf watch` shows the kind with its required action, Pi Web's item panel
labels it, and `pf answer` names the continuation. The `pi_subagents`
switch stays default-off; a work item in flight keeps its claim-time lane
snapshot, so nothing is gated retroactively.

## v0.2.4, the dev channel's comment-only exception on the interface paths (polyforge#219)

The development channel's client-server interface preflight (the graded
AIHub-side check of polyforge#188) gains exactly one pass: a modified
`.go` file on a `tools/aihub-interface-paths.conf` path whose two
versions differ only in Go comments — added, changed or deleted
comments, and the whitespace they cause — no longer counts as an
interface change, and a passing run prints one info line naming every
such path (the fixed fragment `comment-only`); the file never reaches
the total list's `tools/aihub-paths.conf` WARN. The judgment stays
fail closed on every other shape: any code change however small (an
identifier, a type, a field, a string literal, a struct tag, an
import), a new, deleted or renamed file, a mode or type change, a
non-`.go` path, a version that will not parse, and a green tree that
does not carry a working judge — each refuses exactly as before,
waiting for a formal release. The judge is `tools/gocommentonly`, part
of the repository and built by `tools/install.sh --dev` from the green
commit's own tree with the machine's `go` (directive comments and cgo
preludes are code: a changed `//go:build`, `//go:generate` or `import
"C"` preamble refuses); a green tag too old to carry the judge judges
nothing, so the exception takes effect with the next green tag after
this change — this is what unblocked the fifteenth batch:
`green/20261121-1` over `v0.2.3` touches exactly one interface file,
`internal/common/protocol/watch.go`, whose change is four comment
lines.

## v0.2.4, the plan maps the ticket's decided rulings (polyforge#221)

Build's plan-conformance close fact reads the ticket for the first time:
the work item's verbatim content (`runtime-state/work-item.json`, already
materialized by the daemon) is scanned for every section whose heading
opens `## 已定` — the section the owner's plan-review rulings are written
into, before the analysis (the 2026-11-21 ruling; polyforge#202's trial
showed both models skipping rulings appended after the analysis) — and
every item of every such section, numbered across the sections in order of
appearance (a prose paragraph that is no list counts as one, so a ruling
cannot slip past), must be mapped in the plan's new `## 已定对照` section
to a unit — an id of units.json, or a bold id of the plan's `## Units`
when there is no units file — or to a stated `none — <reason>`. An
unmapped ruling is red and named with its number and text, so the close
cannot hand off, and build's close and review run the same one line — one
engine, one result file. A ticket without such a section asks for no
mapping section, and a work item the daemon has not materialized reads as
no decided ruling (a corrupt one is not judged, exit 2). The rule reads
the ticket's own writing only — no project is named, any repository's
ticket written this way is read the same. Plans written before this
change are judged from the next plan-writing round or the next re-run
close: a lane in flight whose ticket carries `## 已定` sections fails the
close red until the mapping section is added (the result names every
unmapped item, so the fix is mechanical), and a lane whose fix build
inherited its plan keeps skipping the plan-conformance line. The develop
build step's line budget rises from 215 to 222. No upgrade action beyond
the next `pf` install: texts and the one engine entry, nothing to migrate.

## v0.2.4, `pf file analysis redo` re-schedules an old ticket's analysis (polyforge#233)

A queued or blocked execution ticket whose pre-submit analysis was written
before the current report format or against a stale base can be re-analyzed
in place: `pf file analysis redo <id>` archives the active `## 分析` section
verbatim in the ticket's new `## 历次分析` area, schedules the same analysis
task `pf file create` schedules, and leaves the new draft unconfirmed until
the owner runs `pf file analysis confirm <id>`. The ticket is refused while
it is claimed, running or terminal, or while a local execution job, a running
analysis task or a pending analysis request holds it. The daemon and the CLI
must both carry the new verb; the strong guarantee is that machine's daemon —
another machine's auto-pull relies on the ticket's unconfirmed marker. No
database or configuration migration is needed.

## v0.2.4, the step tier catalog (polyforge#143)

`cli.toml` gains the Host-only `[step_tiers]` table: the step tiers'
model-candidate catalog, one row per tier (the conventional `default`
tier carries the mainline models), each row a non-empty list of
`<harness>:<model>[:<effort>]` candidates. It is live on reload (a
changed catalog reports one `applied` outcome, no restart needed),
refused in a named Cell's file as a Host-only key, and it is the
recommendation list `pf doctor`'s new `step tiers catalog` row checks a
workspace's user-written candidates against — a candidate outside it
warns, never fails (the workspace yaml and the `[summarize]` models still
run exactly as before), and an empty catalog skips the row. The catalog
recommends, it never gates: a pi candidate's admission stays the Cell's
`step-agent/models.json` check (`pf override --models` answers
`DAEMON-400` for a model it does not declare, catalog or no catalog).
No upgrade action is required — but a `pf` older than this release
refuses the whole `cli.toml` on the unknown section, so downgrading, or
running a mixed-version CLI/daemon pair against a filled catalog, fails
every command with that parse error: empty the table first
(`pf config unset step_tiers.<tier> --host`) on the way back. After
upgrading, fill the catalog once by hand — `pf config set
step_tiers.default '["pi:…", "claude:…"]' --host`, and one row per tier
the lanes name — `pf doctor`'s row is the check it took effect.

## v0.2.4, the pi sub-agent compatibility gate (polyforge#227)

The `pi_subagents` switch (develop `build` only, default off) opens the
daemon-owned worker only after that dispatch's startup checks pass: the
dedicated step agent directory must carry every built-in resource — the
worker definition, the subagent tool and the guard, SOURCE.md included —
and the installed `pi` must pass a real capability probe. The probe uses
the target's own `--version` and `--help` (the required flags include
`--no-approve`), then launches the real `pi` against those resources and a
loopback fake provider with a literal fake key: the subagent tool must be
registered and callable, its controlled refusal must return to the model,
and the session must settle un-aborted. No operator credential or model
configuration is read and no request leaves the loopback service.

A refusal keeps one stable class and a readable message: `pi_version`
(the target cannot report a version), `pi_flag` (a required flag is
missing), `pi_api` (the installed target looks supported but its
extension API cannot load or complete the minimal call),
`pi_tool_registration` (the extension loads but the subagent tool is not
offered) and `pi_timeout` (the whole check exceeded its budget). The check
has ONE 10-second budget including process recovery — no per-stage
timeouts — and no version floor: the version is a cache key and a message,
not a numeric comparison.

Successful verdicts are cached in memory per executable target (resolved
path plus entry fingerprint), version and built-in resource digest; a
same-key concurrent batch probes once and a failure is never cached. Any
change to the target, the version or any embedded resource probes again.
An `on` dispatch whose resources or probe fail is refused at that
dispatch, before the step process or the prompt exists, classed
`pi_resources` for the resources and otherwise by the five classes above;
an invalid switch value is refused as `pi_subagents_invalid`. Off and
empty probe nothing. No upgrade action beyond taking the new `pf`; the
switch stays default off.

## v0.2.4, the container supervisor and the upgrade drain (polyforge#160)

The workstation container's supervision ships: `pf supervise` is the
image entry point (84-image runs it as catatonit's direct child) — it
starts and watches the daemon, Pi Web and, with `--with-aihub`, the
aihub server as its own children, restarts a dead child with a growing
delay, ends with the daemon's own 0/78 exit, and forwards a stop signal
once with a `--grace` (floor 45s) for the daemon's shutdown before a
SIGKILL. Under it `pf daemon ensure` is a successful no-op (the machine
mapping check still runs; only the starting is skipped) and `pf doctor`'s
supervision row passes on the `POLYFORGE_SUPERVISOR=container:pf-supervise`
marker, with tmux no longer a required prerequisite there. A host is
untouched: the tmux loop keeps its behavior.

The upgrade drain ships with it: `pf ops drain --upgrade [--force]`
(Host-level, `POST /ops/drain`, human-only) sets a Host-wide refusal of
new work — every Cell refuses new submits, automatic claims and task
spawns `DAEMON-409` while running work goes on — then judges idleness;
an idle Host answers (safe to upgrade), a busy one refuses with 409
naming the busy Cells until `--force` accepts the in-flight sessions
(interrupted by the shutdown that follows, resumed after the restart).
No upgrade action beyond taking the new pf: `tools/install.sh` does not
use the gate yet (that integration is its own ticket), but a manual
upgrade now has the gate available — run it, wait for the idle answer,
then `pf ops shutdown` and install. The refusal of new work ends only
with the daemon restart; an upgrade called off is unwound by restarting
the daemon (`pf ops shutdown` then `pf daemon ensure`, or the container
supervisor restarting it).

## v0.2.4, the workstation image and compose (polyforge#161)

The workstation container ships: a whole polyforge machine in one
container, its base image and outer parameter set in the new top-level
`dist/workstation/` — podman (rootful, crun) runs the verification
containers inside, catatonit is PID 1, the entrypoint initializes a
private writable cgroup namespace as root and hands the machine to
`pf supervise`, and two named volumes persist the data dir and podman's
storage. The outer container carries exactly the measured minimal set:
`SYS_ADMIN` and `NET_ADMIN`, the pinned seccomp file (moby's default
template plus `pivot_root`, its SHA recorded beside it), a private
cgroup namespace, AppArmor and system paths unconfined — no
`privileged`, no devices, no docker socket. Boot modes are compose
profiles, one per smoke flag (`--profile no-aihub`, `--profile aihub`
adds `pf supervise --with-aihub`), and the optional HTTPS front is a
third profile (`--profile https`: a pinned caddy terminates TLS with its
own local CA at `WORKSTATION_HTTPS_HOST` — the same name belongs in
`PI_WEB_ALLOWED_HOSTS` — and reverse-proxies the `workstation` network
alias both boot modes answer); `PI_WEB_PASSWORD` is required by the
compose interpolation, the container's environment is its explicit list
plus the `PI_*` families the entrypoint whitelists (`PI_TEST_*` never
crosses). `dist/workstation/smoke.sh` (`boot`, `env`, `podman-verify`)
is the acceptance driver: it prints every measured value and fails on
the first miss; `boot --with-aihub` and `env` need the data volume to
carry the release layout's Pi Web component (84-components). Inside the
workstation, podman needs two measured accommodations the minimal cap
set forces, both shipped in the image and the entrypoint: no container
keyrings (`keyring = false`; the pinned profile blocks `add_key`) and
`BUILDAH_ISOLATION=chroot` for build steps (buildah's build-network
setup would need `CAP_SYS_PTRACE`; the daemon's environment allow-list
now carries that one name to every step). A host is untouched — the
workstation is opt-in, nothing existing changes shape; the only
repository-wide registrations are the new `dist` component in
`.polyforge/verify-scope.conf` and a root `.dockerignore` trimming the
image build's repo-root context (the release and verification image
builds use their own contexts and are unaffected).

## v0.2.4, the component seed and install transaction (polyforge#162)

The workstation image now carries the component seed beside the
runtime. The final layer adds the Node runtime (the same node:24.22.2
pin the webui build uses — Pi Web and the two harness CLIs run on it,
so the workstation boots without installing anything) and one
complete, read-only seed at `/opt/polyforge/seed/releases/<ver>`: the
release checkout (package.json, skills, workflows), `bin/pf`, Pi Web
built with its production dependencies (no dev packages, no build
cache) and the two harness packages
(`@earendil-works/pi-coding-agent`, `@anthropic-ai/claude-code` —
official packages as published, version-pinned, carrying no
credentials; each user logs in themselves). A manifest pins the whole
seed: per-component sha256 digests, the product version, the source
commit, the platform, the channel repository, the minimum base
requirement and the harnesses' own package versions —
`dist/workstation/build-seed.sh` assembles it inside the image build
and `seedtool` (from `internal/polyforge/components/seedtool`) writes
and verifies the manifest. The version and TARGETARCH build arguments
are the injection points a release pipeline fills; the repository
carries defaults for a repository-root dev build.

The same `internal/polyforge/components` package ships the local
install transaction every component switch goes through — the one the
workstation's first boot and #92's in-container upgrade verb share. A
components root holds one complete version directory per
`releases/<ver>` (a version directory IS the release checkout root)
with `current` and `previous` symlinks at the root; `current` is the
single commit point: a staged copy is verified against the manifest's
digests, published by one rename, and the link is swapped. A newer
seed upgrades and keeps `previous`; an older seed never downgrades;
the same version and digest writes nothing; the same version with a
different digest, a bad digest, a wrong platform, a base below the
manifest's minimum, an illegal version and a cross-channel seed are
all refused with `current` untouched. A kill at any commit stage
recovers on the next run — the journal completes a committed switch
or an uncommitted one is discarded, and staging never survives — and
the pre-#162 layout the #161 image and the host installer leave in the
data directory (`<data-dir>/release/<ver>` with a `current` symlink
inside) is refused, through the install's data-directory input, with
a migration note, never imported. The first boot's wiring, the components volume itself and
the supervision that runs the components' pf are the later
84-components units; nothing on a host changes — the host installer's
own layout is untouched.

## v0.2.4, the runtime paths and the upgrade-restart exit (polyforge#175)

The workstation's runtime paths follow one component-root convention.
Inside the image — judged by the one fact the seed manifest at
`/opt/polyforge/seed/releases/<ver>` exists — the supervised daemon, the
aihub server and Pi Web's `PF_BIN` all launch the components volume's
`/components/current/bin/pf` (a literal path: exec resolves the current
symlink afresh at every start, so an upgrade that switches current is
live at the next relaunch), and the `pf supervise` flag defaults
(`--workflows-dir`, `--pi-web-dir`) sit under `/components/current`.
`pf doctor`'s pi package and Claude skill layout rows judge the
components current there, never name `tools/install.sh` in a fix, and
the pre-#162 `<data-dir>/release` layout is refused with a migration
note, never bridged. The base pf moved to
`/usr/local/libexec/polyforge/pf` — the path the image's entry point
execs by absolute value, never a PATH lookup. The container supervisor
grew the restart-for-an-upgrade exit: a daemon exit 75 is relaunched AT
ONCE — no failure count, no backoff, the run never ends on it — while
0/78, the stop window and the failed-start backoff stay as they were;
the constant `UpgradeRestartExit` ships in
`internal/polyforge/supervisor` beside 78, the daemon aliasing it, and
the trigger that exits 75 is #92's upgrade orchestration. Nothing on a
host changes: `tools/install.sh`'s `/usr/local/bin/pf` and
`<data-dir>/release/current` are untouched. No action on upgrade beyond
taking the new image.

## v0.2.4, the first boot: identity, config and the persistent volumes (polyforge#176)

The workstation container now persists everything in six independent
named volumes — `pf-home` (the whole `/root`: cli.toml, the pi agent
dir `PI_CODING_AGENT_DIR=/root/.pi/agent`, every login), the existing
`polyforge-data` (nested inside pf-home at its canonical path), the
components volume the seed installs into, the local AIHub's store at
`/var/lib/polyforge/aihub`, the `/workspace` working tree and the
existing `podman-storage`. The DEFAULT boot is the aihub one: the
service `workstation` carries profile `aihub` and starts the local
AIHub as a watched child, and running without an AIHub is the explicit
opt-out — the service `workstation-noaihub`, profile `no-aihub`
(#161's no-aihub default is flipped; a deployment that ran
`--profile no-aihub` keeps working, and a bare `docker compose up`
still starts nothing until a profile is named). Both profiles point
supervise's component paths at `/components/current`.

The entrypoint runs `pf first-boot` before supervise, and the boot is
idempotent per stage: the seed install (a same-version noop), the AIHub
identity — a daily user, its key and the local `workstation` project,
established in one in-process transaction through the new local
initialization entry `pf aihub init` (no network listener, refused
inside a step or task environment and beside a running server) —
cli.toml's default Cell (the local `http://127.0.0.1:8420` or the
external server, `container_engine = "podman"`, the `/workspace`
mapping), and the harness references toward `/components/current` (the
pi packages entry and the claude skill symlinks; the host installer's
own merge is untouched and the two are documented as independent). A
fresh store initialized this way never gets the server's own bootstrap
admin (usr_root / bootstrap-admin.key); the default Cell's key is a
daily key by construction. The result record
`<data-dir>/workstation/first-boot.json` publishes the phase (done, or
`<stage>/failed` with the reason — never a credential) and is a
published contract for the later management units. A completed boot
skips itself (the first choice — local or the external mode whose
`WORKSTATION_AIHUB_SERVER`/`KEY`/`PROJECT` inputs ride the container
environment — is made exactly once); an interrupted boot resumes
without duplicating the user (the same-named key is revoked and
reissued), with the in-progress record — not the environment — as
the authority for the choice (owner ruling of 2026-11-20 on review
r1#1: environment inputs that disagree with the record refuse the
start, and an external record's key input must be set again); done
without the default Cell, or a Cell with no record, is an
inconsistency the container refuses to start on. The smoke gains
`first-boot`；`first-boot --external` 的计数桩在首启和重启全过程中只允许
`POST /api/v1/machines/heartbeat`，创建用户、key、项目以及任何其他请求均令
验收失败并点名方法与路径。No action on upgrade beyond taking the new image
and compose. The image has not shipped in a release, so no real
deployment exists to migrate; a dev host's leftover volumes are the
only pre-#176 state, and one honest leftover comes with them: such a
volume carries the daemon's recorded Cell identity against
`http://localhost:8420` (the bare config default of that era), and the
first boot's default Cell names `http://127.0.0.1:8420` — another
identity domain, so the daemon refuses to start beside it (exit 78,
its own message naming both). Recovery is the daemon's own rule:
reset the old dev volume (the machine is disposable) or keep the old
Cell under its recorded identity and let the first boot be skipped.
## v0.2.4, 容器内组件更新（polyforge#190）

工作站镜像内可通过 Pi Web 的「容器组件更新」卡片或 `pf components`
升级 pf（含 daemon 与 AIHub）、车道与 skill、Pi Web。harness 仍使用原有
钉住版本，待 #86 提供版本清单；node、python、git、Podman、pf supervise
和底座二进制只能换镜像升级。宿主机的 tools/install.sh 与自更新卡片不变。

升级从当前 manifest 的 channelRepo 下载 GitHub Release（公开渠道匿名访问），
核对 SHA256SUMS 后组装完整 releases/<ver>，current 切换是提交点，previous
保存旧版本。取资产或 staging 失败不会切换 current。upgrade-status.json
保存在组件卷上，记录阶段、成功或失败、目标版本及原因，页面断线后自动重连读取。
编排 CLI 独立于 Pi Web 会话运行。`--seed-dir` 可指定已校验的本地完整种子。

排空期间新作业收到 DAEMON-409；现有作业完成后自动继续。取消会重启 daemon
清除排空；`--force` 允许直接切换。daemon 通过 Host 级 POST /ops/restart
触发退出码 75，supervise 立即重新解析 current 启动，不停止容器；0 和 78
仍结束 supervise。重启标志只在内存存在，不会在新 daemon 上重复触发。

回到上一版交换 current 与 previous 并重启组件；目标 manifest schema 不可读、
平台或渠道不同、最低底座版本不满足时拒绝，保留软链与运行组件并显示原因。
回退在数据层同样受机械判定（owner 2026-11-21 裁定）：manifest 新增
dataLayout 字段，由构建时的 seedtool 与组装器从本构建读取的 cellcfg 布局
版本钉入；回退时与数据目录的 layout-version 比对，previous 声明的布局与
卷上实际不一致即拒绝并提示两边的布局名、什么都不改。早于该字段的版本
不声明、也不被机械判定：回退到它们前先备份并遵循对应版本的迁移说明。
manifest schema 不是数据库兼容性的证明。组件卷首启与 compose 接线由
84-components-C 提供。

## v0.2.4, 工作站发版镜像（polyforge#86）

发版现在在 AIHub 镜像之外同时构建并推送本地运行（工作站）镜像
`ghcr.io/silgrid/polyforge-workstation:<tag>`：buildx 一次构建 linux/amd64 与
linux/arm64 双架构，构建在占版本之前（进 buildx 缓存——占版本之前不发布任
何东西），推送在占版本之后；index digest 作为新资产 `workstation-image.digest`
发布，进 SHA256SUMS 与 release-manifest.json。`release.sh --verify` 核对 index
digest 与两个平台，并对公开渠道用空 `DOCKER_CONFIG` 做一次匿名读取。全新发
版遇到任一镜像标签已存在即在占版本之前拒绝；续发按镜像各自保留已在
registry 的、只补缺的；预检在占版本之前检查 buildx 与 arm64 binfmt。内部
`-gmi` 线必须显式设置 `RELEASE_WORKSTATION_IMAGE`（未设即拒绝），把
`vX.Y.Z-gmi.N` 推到指定仓库。镜像自带宿主侧文件（compose.yaml、seccomp 及
其 SHA、Caddyfile、smoke.sh，位于镜像内 /opt/polyforge/host-files），一行
`docker run --rm --entrypoint tar <镜像>:<版本> -C /opt/polyforge/host-files -cf - . | tar -xf -`
导出——安装与升级只需镜像地址加版本号，不需要 gh；GHCR 首次推送会建私有
包，org owner 需一次性把公开包改成公开。
## v0.2.4, review-firstpass 阻断项明细与疑似误报率（polyforge#242）

`tools/review-firstpass` 在 `--events` 下新增阻断项段落：遍历 capsule 的
`artifacts/review.md` git 版本链（没有可读历史时回退到 handoff 的盖章
review 节，两者不叠加），逐条提取阻断项，按 `r<n>#<k>` 关联后续轮次和
owner 的 asks.jsonl 裁定，给出结局（修复后通过、反驳成立、owner 改判、
撤回原因不明、无法判定）、按类别与按模型的疑似误报率，以及无法判定按
原因计数和无法解析的 FAIL 版本数。stdout 只打印汇总与无法判定的计数；
同时给 `--csv` 时另写 `<csv>.blocking.csv`，既有 CSV 不加列。这是离线
只读工具，不需要迁移动作。

## v0.2.4, 低星级问题卡的自动按推荐继续（polyforge#239）

owner 2026-11-23 授权：步骤问题卡（`pf step ask` 的四列表）里每一行的
`重点关注程度` 都是 ★ 或 ★★ 时，若 T0（该 dispatch 首次进入
`awaiting_owner` 的持久时刻）+ 超时（默认 30 分钟）到期仍无 owner 回复，
daemon 以保留身份 `daemon:auto-recommendation` 追加一条 reply，逐行采纳
「推荐选项」，并写明实际等待时长与授权日期。自动回复不是 owner 授权：
`--per-owner`、waive/rewind/强制 fail、attrs 与免计数都不认它；决策流出现
固定条目「已自动按推荐继续」。任一行 ≥ ★★★、星级缺失或不可解析、不是
唯一一张非 fenced 严格四列表、没有可信 T0、该步骤是 gate（review、
owner-review、人工 merge、gate FAIL 等）、该步骤声明 `protected_stop`、
车道无法验证（daemon 从 `workflows/` 枚举已发布车道），或工单带
`confirm_plan: true`（宽松识别）时，一律继续等人。重启保留原 T0，并从
reply 补齐缺失的 `auto_answered` 事件；关闭开关或改超时后下一次判断即时生效。

两个 Cell 级 live 配置键（`[daemon]`，经 `pf config set` 写入；unset 即默认）：

- `ask_auto_reply_enabled`：unquoted `true` / `false`，默认 `true`。
- `ask_auto_reply_timeout`：quoted Go duration，> 0，默认 `30m`。

升级动作：无。取新的 pf 即生效；已有等待若没有本版本写入的 `awaiting`
记录，不会自动回复（保守边界）。`workflows/_shared/batch_questions.md` 与
`skills/output-format/SKILL.md` 已改写为「星级会被机械读取、如实打星；
取消 / 放弃类问题必须 ≥ ★★★」。
