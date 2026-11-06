# cell — the Host's Cells (owner-only, not routable)

Machine verb, **owner-only and not routable**: the routing agent never runs
`pf cell` on its own initiative and never from inside a workflow step or
task (every `pf cell` verb is refused there with `DAEMON-403`, and the
daemon's `/profiles` lifecycle routes refuse an agent caller too). On an
explicit owner request, show the command for the owner to run.

One daemon (the Host) serves one Cell per AIHub profile: `default` (from
`cli.toml`) plus one `cli-<profile>.toml` per named Cell. A Cell's file is
its truth; the Host applies a change without a restart.

## Command

```
pf cell list                                      # the Host's Cells, their state (serving/degraded/invalid/removed) and reasons
printf '%s\n' "$KEY" | pf cell add <profile> --server <url> [--model-source shared-default|<dir>] [--workspace <path> --projects a,b]
pf cell update <profile>                          # re-apply its file after `pf --profile <profile> config set …` or a hand edit
pf cell repair <profile>                          # the same: retry an invalid Cell after fixing its file
pf cell remove <profile>                          # refused while it holds work; default cannot be removed; its state dir is kept
pf cell cleanup <profile>                         # impact only: what a permanent cleanup would delete (exit 1, nothing changed)
pf cell cleanup <profile> --confirm <profile>     # delete a removed Cell's state dir and its Host decision-feed entries for good
```

- The api key is read from stdin only (`--api-key` is refused: a credential
  never rides argv).
- A profile name matches `[a-z0-9][a-z0-9-]{0,31}`; `default` is reserved;
  a name is never renamed — another name is another Cell.
- An existing profile never changes its AIHub or user in place: a different
  server (or a credential of another user) needs a new profile name. A
  removed Cell restored under the same name must point at the same AIHub,
  else the restore is refused with the new-profile route.
- A Cell with no `[model] source` loads and serves the data plane, with its
  execution plane disabled (the reason shows on `pf cell list`).

## Failure branches

| Answer | Meaning | Next |
| --- | --- | --- |
| `DAEMON-403` | called from a step or task | never run it from an agent |
| `DAEMON-409 … holds work` | the Cell still holds jobs, queued submits or tasks | finish, wrap or cancel them first |
| `DAEMON-409 … create a new profile` | the file points the profile at another AIHub | use a new profile name |
| `DAEMON-409 … was not changed` | the file is invalid or conflicts with a serving Cell | fix the file (the registry kept serving), then `pf cell repair <profile>` |
| `DAEMON-409 … still configured` | cleanup of a Cell that is not removed | `pf cell remove <profile>` first |
| `saved; no daemon answers` | the daemon is down | the Host applies the file at its next start (`pf daemon ensure`) |
