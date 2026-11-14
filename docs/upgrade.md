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
