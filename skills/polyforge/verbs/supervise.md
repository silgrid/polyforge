# supervise — the container process supervisor (the image's entry point)

Process verb, not an operator action: `pf supervise` is what the
workstation container's image (epic #84) runs as its entry point —
PID 1 (catatonit) runs it as its direct child — and a human session
never runs it by hand. `cmd/pf/main.go` intercepts it before CLI
dispatch; `pf help supervise` carries the flag surface.

What it does: it starts and watches the daemon, Pi Web and — with
`--with-aihub` — the aihub server as its own children, restarts a child
that dies on it (a crash, a kill -9) with a growing delay, ends the
whole run when the daemon exits 0 or 78 (the container stops with that
code; the 78 reason is in `<data-dir>/daemon.log`), and on a stop signal
forwards it ONCE to every child (a second daemon signal would
hard-abort its shutdown) and gives the daemon the `--grace` (floor 45s)
to finish its shutdown before a SIGKILL. Full reference:
[commands.md](../../docs/reference/commands.md), "`pf supervise`".

What an operator in the container does instead:

- start/check the daemon — nothing: `pf daemon ensure` is a successful
  no-op under this supervision (the marker
  `POLYFORGE_SUPERVISOR=container:pf-supervise` in the daemon's
  environment), and `pf doctor`'s supervision row passes on it; a
  missing `tmux` is not even a warn there;
- stop the machine — stop the container (`docker stop` / `podman
  stop`): the signal reaches `pf supervise`, which forwards it and the
  daemon walks its shutdown inside the grace (set the outer stop
  timeout to the grace plus a margin);
- read the machine — the children's logs: `<data-dir>/daemon.log`,
  `pi-web.log`, `aihub.log`.

On a host none of this applies: the daemon's supervision there stays the
tmux loop `pf daemon ensure` creates, and no documented flow runs
`pf supervise` outside a container.
