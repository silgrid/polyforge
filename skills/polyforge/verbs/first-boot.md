# first-boot — the workstation container's one-time initialization

Process verb, not an operator action: `pf first-boot` is what the
workstation container's image (epic #84, unit 84-components-C) runs
once per container start, before `pf supervise` — the entrypoint calls
it between the environment boundary and the supervisor, and a human
session never runs it by hand. `pf help first-boot` carries the flag
surface.

What it does, in stage order (each stage published atomically to
`<data-dir>/workstation/first-boot.json`, the phase record — on
failure the phase is `<stage>/failed` with the reason, never a
credential):

- components — the image's seed installs into the components volume
  (`/components`) through the seed transaction; no seed means a
  not-image-managed run (`imageManaged: false`) and no install.
- identity — the local mode (the default) runs the local
  initialization entry in process (`pf aihub init`): the daily user,
  its API key and the local `workstation` project — the user its
  maintainer — in one transaction. The external mode takes the
  operator-given server and key instead and creates nothing anywhere.
- config — cli.toml gains the default Cell: the server (the local
  `http://127.0.0.1:8420`, or the external one), the api key, the user
  id, `container_engine = "podman"`, and the
  `[workspace."/workspace"]` mapping to the project (the external mode
  maps only a given, already-existing project — otherwise nothing).
- harness — the pi packages entry and the claude skill symlinks point
  at `/components/current`, preserving a login, other packages and the
  user's own skill entries. The host installer (`tools/install.sh`)
  keeps its own merge for host installs; the two are independent.
- done — the record names the mode, the default Cell, the server,
  `userReady`/`keyReady` (`keyReady` says issued-and-written, never
  authenticated — the real authentication proof is the smoke's
  `pf recall` inside the container) and `imageManaged`.

The gate: a record saying done plus a cli.toml carrying the default
Cell skips the boot — the first choice (local or external) is made
exactly once, and a restart with different `WORKSTATION_AIHUB_*`
environment changes nothing. Either half alone is an inconsistency the
boot refuses to start on, naming both files; a default Cell with an
in-progress record is this boot's own interruption and resumes. Every
stage is idempotent: the user is found by its fixed email (never
duplicated) and the same-named key is revoked and reissued, so a retry
never strands anything.

Full reference: [commands.md](../../docs/reference/commands.md),
"`pf first-boot`", and [workstation.md](../../docs/workstation.md),
"The first boot".
