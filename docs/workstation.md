# The workstation image

The workstation is a whole polyforge machine inside one container: the
daemon runs there, `pf supervise` keeps it (and, in the default boot,
the aihub server) alive, and podman — rootful, with crun — runs the
verification containers. The image is the base plus the component
seed: podman, crun, catatonit as PID 1, the pf binary, the container
configs and the Node runtime, plus one complete seed of the versioned
components — the release checkout, `bin/pf`, Pi Web built with its
production dependencies and the two pinned harness packages —
read-only at `/opt/polyforge/seed/releases/<ver>` and pinned by the
manifest the install verifies (#162, 84-components-A). The versions
that RUN live in the components volume — `releases/<ver>` there, with
`current` and `previous` at the volume root — which the FIRST BOOT
populates from the seed (#176, 84-components-C): a newer seed upgrades
the volume, an older seed never downgrades it.

Everything the workstation needs lives in `dist/workstation/`:

| File | What it is |
|---|---|
| `Containerfile` | the base image: webui's dist tree and the pf binary built from this repository, on ubuntu with podman, crun, catatonit, plus the Node runtime and the seed stage |
| `build-seed.sh` | the seed assembly, run inside the image build (Pi Web's build and prune, the two pinned harness packages, `bin/pf`, the manifest seedtool writes and verifies) |
| `entrypoint.sh` | cgroup-namespace init as root, then the environment boundary, then the first boot (`pf first-boot`), then `pf supervise` |
| `compose.yaml` | the outer container's minimal parameter set, the six persistent volumes and the two boot modes |
| `containers/` | podman's storage, containers and registries configuration |
| `seccomp.json` | the pinned seccomp profile the outer engine applies (origin and SHA beside it) |
| `smoke.sh` | the acceptance driver: `boot`, `first-boot`, `env`, `podman-verify` |

## Boot modes

The outer container runs the measured minimal parameter set — exactly
the capabilities `SYS_ADMIN` and `NET_ADMIN`, the pinned seccomp file,
a private cgroup namespace, AppArmor and system paths unconfined, no
`privileged`, no devices and no docker socket — and the compose file is
the single place those parameters live. The stop grace (90s) sits above
`pf supervise`'s own (60s), so the daemon's shutdown always finishes
before the outer SIGKILL.

There are two boot modes, one compose profile each (compose activates a
profile only when named, and `--profile` can only add, never subtract,
so both variants carry an explicit profile and stay mutually exclusive).
The DEFAULT boot runs the local AIHub (#176's flip of #161's default):
the `workstation` service, profile `aihub`. Running without an AIHub is
the explicit opt-out: the `workstation-noaihub` service, profile
`no-aihub`:

```sh
cd dist/workstation
PI_WEB_PASSWORD='<a long, unique secret>' docker compose --profile aihub build
PI_WEB_PASSWORD='<a long, unique secret>' docker compose --profile aihub up -d
# the without-AIHub opt-out, same two steps with --profile no-aihub
```

Build and start are two explicit steps, never `up --build`: measured
once on compose v5, `up --build` left a stale image tagged when the
Containerfile had changed, and the boot then proved the wrong tree.

The default boot passes `--with-aihub` to `pf supervise`: the aihub
server starts as another watched child on the AIHub volume, and Pi Web
(the components volume carries it after the first boot) answers on the
published port. Both variants point supervise's `--workflows-dir` and
`--pi-web-dir` at the components volume's `/components/current` — the
versioned components run from there, never from the image.

Remote access rides the optional HTTPS front — a Caddy sibling service
in its own `https` profile, off by default (the settled Q7 shape). It
terminates TLS with its own local CA and reverse-proxies the
workstation's Pi Web:

```sh
PI_WEB_PASSWORD='<a long, unique secret>' \
WORKSTATION_HTTPS_HOST=<the hostname the browser will use> \
PI_WEB_ALLOWED_HOSTS=<the same hostname> \
docker compose --profile aihub --profile https up -d
```

`WORKSTATION_HTTPS_HOST` names the hostname caddy serves and signs its
certificate for (`localhost` by default — a hostname caddy does not
know is refused with no matching certificate); the same name belongs in
`PI_WEB_ALLOWED_HOSTS`, because Pi Web trusts only loopback names, IP
literals and the names that variable lists. Point the browser at
`https://<WORKSTATION_HTTPS_HOST>:8443` (`WORKSTATION_HTTPS_PORT`
moves the published port) and import Caddy's root certificate — printed
by `docker compose exec https-front cat /data/caddy/pki/authorities/local/root.crt` —
for a trusted local CA; no external certificate flow is involved. The password is
required — without one, Pi Web's proxy lets `/api` through and
redirects `/login`, and the container refuses to start instead of
exposing the high-privilege UI bare. The container's environment is the
compose file's explicit list plus the `PI_*` families the entrypoint's
whitelist passes (never `PI_TEST_*`) and the first boot's three
`WORKSTATION_AIHUB_*` inputs; the host's environment never crosses the
boundary.

## The persistent volumes

Six named volumes persist everything, each its own so a reset never
crosses. Back up and restore whole volumes, never files under them:

| Volume | Mount | What it holds |
|---|---|---|
| `pf-home` | `/root` | the whole home: cli.toml (the default Cell), the pi agent dir (`PI_CODING_AGENT_DIR=/root/.pi/agent`) and every login |
| `polyforge-data` | `/root/.local/share/polyforge` | the data dir: the daemon's state, the workspaces, `workstation/first-boot.json` (nested inside pf-home at its canonical path) |
| `components` | `/components` | the versioned components: `releases/<ver>` with `current` and `previous` at the root |
| `aihub` | `/var/lib/polyforge/aihub` | the local AIHub's SQLite store and git repos (`pf aihub`'s own default data dir) |
| `workspace` | `/workspace` | the workstation's own working tree, mapped to the local project |
| `podman-storage` | `/var/lib/containers/storage` | podman's image and container storage, on its own filesystem because native overlay refuses overlay-on-overlay |

(The https front's `caddy-data` is a seventh, caddy's own.)

## The first boot

The entrypoint runs `pf first-boot` exactly once per container start,
before `pf supervise`. Its stages, each published atomically to
`<data-dir>/workstation/first-boot.json` (the phase record — on failure
the phase is `<stage>/failed` with the reason, never a credential):

1. **components** — the image's seed installs into the components
   volume through the seed transaction; no seed (a dev run) means
   `imageManaged: false` and no install.
2. **identity** — the local mode establishes the daily user, its API key
   and the local `workstation` project (the user its maintainer) in one
   in-process transaction (`pf aihub init`, below) — the server that
   starts later finds the store already initialized and its own
   fresh-install bootstrap (usr_root, `bootstrap-admin.key`) never
   fires, so the default Cell's key is a daily key by construction.
   One deliberate consequence to know: the local hub then carries NO
   administrator identity at all (no one to grant other users or
   roles) until the management units (#163/#167) build an operator
   path — the workstation is a single-user machine by design.
   The external mode takes the operator-given server and key and
   creates nothing anywhere.
3. **config** — cli.toml (in pf-home) gains the default Cell: the
   server (the local `http://127.0.0.1:8420`, or the external one), the
   api key, the user id, `container_engine = "podman"` and the
   `[workspace."/workspace"]` mapping to the project. The write rides
   the config writer's own flock and atomic write and preserves every
   other row.
4. **harness** — the pi packages entry and the claude skill symlinks
   point at `/components/current`, preserving a login, other packages
   and the user's own skill entries. (The host installer,
   `tools/install.sh`, keeps its own merge toward
   `<data-dir>/release/current` for host installs; the two are
   independent and neither calls the other.)
5. **done** — the record names the mode, the default Cell, the server,
   `userReady`/`keyReady` (true only after the facts are verified;
   `keyReady` says issued-and-written, never authenticated) and
   `imageManaged`.

The record is a published contract for the later units (the Pi Web
management mode and the first-boot wizard consume it): its fields are
`phase` (`components`, `identity`, `config`, `harness`, `done`, or
`<stage>/failed`), `mode` (`local` or `external`), `defaultCell`
(`default`), `server`, `project` (the mapped project, when there is
one), `userReady`, `keyReady`, `imageManaged`, `error` (the failure
reason, on a failed phase) and `updatedAt` — and nothing else ever:
no key, no hash, no token.

A completed first boot is skipped (the record says done and cli.toml
carries the default Cell — the first choice is made exactly once, and a
restart with a different `WORKSTATION_AIHUB_*` environment changes
nothing); a cell with no record at all, or a done record without the
cell, is an inconsistency the container refuses to start on, naming
both files. An interrupted first boot resumes, with the record — not
the environment — as the authority for the choice (the owner's ruling
of 2026-11-20 on review r1#1): the mode, server and project come from
the in-progress record, environment inputs that disagree with it refuse
the start, and an external record's key input must be set again (a
record never carries a credential). Every stage is idempotent — the
user is found by its fixed email (never duplicated) and the same-named
key is revoked and reissued, so a retry never strands or duplicates
anything.

The external mode's inputs ride the container environment (compose
interpolates them; the entrypoint's whitelist passes exactly these
three): `WORKSTATION_AIHUB_SERVER` with `WORKSTATION_AIHUB_KEY` point
the default Cell at an existing remote AIHub — nothing is ever created
on it, and the optional `WORKSTATION_AIHUB_PROJECT` names an
already-existing project for the `/workspace` mapping (absent means no
mapping; the wizard that asks interactively is a later unit). Neither
set means the local mode.

## The acceptance driver

`smoke.sh` measures every acceptance value and exits non-zero on the
first miss. Run it from the repository root:

```sh
sh dist/workstation/smoke.sh boot --no-aihub    # the minimal boot: parameters, PID 1, no aihub process
sh dist/workstation/smoke.sh boot --with-aihub  # + Pi Web /login, the daemon queue, the default Cell
sh dist/workstation/smoke.sh first-boot         # the empty-volume first boot: identity, default Cell,
                                                #   real key authentication, no bootstrap key, restart
sh dist/workstation/smoke.sh first-boot --external  # + the external mode with a counting stub: zero remote writes
sh dist/workstation/smoke.sh env                # Pi Web parameters, PI_TEST_* excluded, /api 401
sh dist/workstation/smoke.sh podman-verify      # verification via podman end to end
```

`boot --with-aihub`, `first-boot` and `env` need the components volume
to carry the seed's Pi Web (the first boot installs it); `first-boot`
resets the project's volumes first — it owns its run. The first-boot
pass is the AC proof of the identity half: after the boot the record
says done, the default Cell points at the local AIHub, the key REALLY
authenticates (`pf recall` inside the container, through the
`/workspace` mapping), no `bootstrap-admin.key` exists, and a restart
with the volumes intact changes nothing — the same key still
authenticates (no reissue).

`podman-verify` mounts the repository read-only at `/workspace` inside
the workstation and runs, through the engine's own client, the real
`Dockerfile.verify` build, the prepared-image path, and the three
real-container test modules with `PF_CONTAINER_ENGINE=podman`, then
proves no residual container, no zombie and no resident conmon survives.

The in-repo Go tests pin the same facts statically
(`test/w535_workstation_image_test.go`: `TestWorkstationImage`,
`TestWorkstationCompose`, `TestWorkstationSmoke`,
`TestWorkstationSmokeFirstBoot`) and drive the `--no-aihub` boot for
real on a machine with an engine (`TestWorkstationImageEngine`,
skipped without one).
