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
reissued); done without the default Cell, or a Cell with no record, is
an inconsistency the container refuses to start on. The smoke gains
`first-boot` (and `first-boot --external`, whose counting stub asserts
zero remote writes). No action on upgrade beyond taking the new image
and compose; existing two-volume deployments keep their volumes (the
new volumes start empty and the first boot fills them). One honest
leftover: a dev volume from a pre-#176 boot carries the daemon's
recorded Cell identity against `http://localhost:8420` (the bare
config default of that era), and the first boot's default Cell names
`http://127.0.0.1:8420` — another identity domain, so the daemon
refuses to start beside it (exit 78, its own message naming both).
Recovery is the daemon's own rule: reset the old volume (the machine
is disposable) or keep the old Cell under its recorded identity and
let the first boot be skipped.
