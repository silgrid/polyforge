# migrate — the one-time cell-layout migration (owner-only, not routable)

Machine verb, **owner-only and not routable**: the routing agent never runs
`pf migrate` on its own initiative and never from inside a workflow step or
task (it is refused there with `DAEMON-403`). It is local: it reads and
writes the data dir directly and refuses to act while a daemon runs. On an
explicit owner request, show the commands for the owner to run. A mutating
run (`--apply`, `--rollback`, `--restore-backup … --confirm`) holds the data
dir's `daemon.lock` from its first check to its last byte, so a Host start
cannot interleave with it.

## Command

```
pf ops shutdown                      # the migration acts on a stopped Host only
pf migrate cell-layout               # plan: every item, nothing written
pf migrate cell-layout --apply       # copy into cells/default, layout-version last
                                     # (a completed layout: "already on the cell layout", nothing changed)
pf daemon ensure
pf migrate cell-layout --rollback    # only before a Host served the new layout, and only byte-exact
pf migrate cell-layout --restore-backup <dir>             # list the data-loss scope (nothing changed)
pf migrate cell-layout --restore-backup <dir> --confirm   # replace the state by the backup (the replaced items kept aside)
```

## Failure branches

| Answer | Meaning | Next |
| --- | --- | --- |
| `a daemon answers on …` / `a daemon holds …daemon.lock` | the Host runs | `pf ops shutdown` first |
| `daemon: another daemon… or a running pf migrate cell-layout, holds the data-dir lock` (from `pf daemon ensure`) | a mutating migration holds `daemon.lock` for its whole run | let the migration finish, then start the Host — nothing was started |
| `rollback refused: the Host already served the new layout` | a Host served the new layout (new state may exist there) | repair forward (`pf doctor`) |
| `rollback refused: the new layout holds state the pre-migration copy does not` | something was written into the new layout after the migration (each blocker is named: a member that differs, a non-member entry, a named Cell) | nothing was changed; repair forward (`pf doctor`) — a rollback would delete that state |
| `migrate cell-layout refused: …migration-accepted-work exists but layout-version does not` | a served layout lost its `layout-version` | nothing was changed; restore from a backup (`--restore-backup`) or ask the owner to run `pf doctor` — it is never re-planned from the pre-migration copy |
| `the migration plan has failed checks` | an item is unreadable | fix the named item; nothing was changed |
