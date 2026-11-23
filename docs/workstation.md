# The workstation image

The workstation is a whole polyforge machine inside one container: the
daemon runs there, `pf supervise` keeps it (and, in the default boot,
the aihub server) alive, and podman — rootful, with crun — runs the
verification containers. The image is the base plus the component
seed: podman, crun, catatonit as PID 1, the base pf (`/usr/local/libexec/polyforge/pf`, the bootstrap the
entry point runs by absolute path), the container configs and the Node
runtime, while the image's PATH `pf` is a link to the components
volume's `/components/current/bin/pf` (an upgrade that switches current
is live on PATH too; dangling until the first boot installs the
volume), plus one complete seed of the versioned
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
| `entrypoint.sh` | cgroup-namespace init as root, then the environment boundary, then the first boot (`/usr/local/libexec/polyforge/pf first-boot`), then `exec /usr/local/libexec/polyforge/pf supervise` |
| `compose.yaml` | the outer container's minimal parameter set, the six persistent volumes and the two boot modes |
| `containers/` | podman's storage, containers and registries configuration |
| `seccomp.json` | the pinned seccomp profile the outer engine applies (origin and SHA beside it) |
| `smoke.sh` | the acceptance driver: `boot`, `first-boot`, `persist`, `env`, `podman-verify` |

## Running a published image

A published release needs no repository checkout and no `gh`: the image
address plus the version is the whole install (the public package must be
made public once by an org owner - GHCR creates it private on the first
push; `release.sh --verify` checks the anonymous read). Point compose's
`WORKSTATION_IMAGE` at the published tag, export the required
`PI_WEB_PASSWORD` (compose refuses to start without it), and
`docker compose --profile aihub up -d` (or `--profile no-aihub` for the
opt-out) boots the machine; an upgrade is changing the tag and running it
again. The internal channel's
image is private: a group member logs in once with a token that carries
`read:packages` (`docker login ghcr.io -u <user>`) and pulls the same way.

The host-side files export from the image itself, so the versions always
match the image:

```sh
docker run --rm --entrypoint tar ghcr.io/silgrid/polyforge-workstation:v0.2.4 \
  -C /opt/polyforge/host-files -cf - . | tar -xf -
```

writes `compose.yaml`, `seccomp.json` (with its SHA), `Caddyfile` and
`smoke.sh` - the smoke's own export set - into the current directory;
`WORKSTATION_IMAGE=<published tag> sh ./smoke.sh first-boot` then measures
the machine with nothing but Docker on the host. The image preinstalls the
pinned pi and Claude Code harness packages; each user logs in with their
own credentials (the harness login section names Claude Code's terms).

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

Pi Web answers on the host's loopback at `http://127.0.0.1:30141` —
`WORKSTATION_WEB_PORT` moves the published port (the container side is
fixed; see [Parameters](#parameters)). Day to day:

- stop the machine — `docker compose --profile aihub stop`: the
  container stops, the volumes and everything in them stay;
- start it again — the same `up -d`;
- tear the container down — `docker compose --profile aihub down`: the
  container is removed, the six volumes still hold everything, and
  the next `up` runs the first-boot skip path;
- a shell inside — `docker compose --profile aihub exec workstation sh`
  (`exec` names the service; the no-aihub boot's service is
  `workstation-noaihub`).

`docker compose --profile aihub down -v` is not a stop: it deletes the
six named volumes with everything they hold — the reset path (see
[The persistent volumes](#the-persistent-volumes) for what each one
loses), never the routine one.

The password is required — without one, Pi Web's proxy lets `/api`
through and redirects `/login`, and the container refuses to start
instead of exposing the high-privilege UI bare. Remote access rides
the optional HTTPS front — see [HTTPS](#https).

## Security

Know what you are running: the parameter set above — `SYS_ADMIN` and
`NET_ADMIN`, AppArmor and system paths unconfined, every process as
root — deliberately widens this machine's reach into the host kernel,
and the attack surface widens with it. Run only code you trust on the
workstation. The widening stays bounded: no `privileged`, no passed
devices, no docker socket, and the seccomp profile is the pinned file
(`seccomp.json`, origin and SHA beside it) — not an unconfined one.

## HTTPS

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
for a trusted local CA; no external certificate flow is involved: the
certificate is the local CA's own, and no public certificate is
requested for any hostname (`tls internal` — teaching caddy to request
one is a change to the `Caddyfile`, not a setting). Two honest limits
of the delivered shape: the front's published port binds every
interface of the host, not the loopback Pi Web's port is bound to (the
compose file carries no `127.0.0.1:` prefix there) — reach it through a
hostname you control or firewall the port — and the front has not been
exercised on a real machine yet: the acceptance passes drive the
loopback port, and this front is the settled design, not a measured
one.

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

A volume's compose name is `workstation_<volume>` (the project name the
compose file fixes):

```sh
docker run --rm -v workstation_pf-home:/from:ro -v "$PWD:/backup" alpine \
  tar czf /backup/pf-home.tgz -C /from .
docker compose --profile aihub down          # the container goes, the volumes stay
docker volume rm workstation_pf-home         # the restore target must be empty
# (docker created implicit volumes carry none of compose's labels)
docker compose --profile aihub create        # compose recreates the volume, with its
                                             #   labels; nothing starts
docker run --rm -v "$PWD:/backup:ro" -v workstation_pf-home:/to alpine \
  sh -c 'cd /to && tar xzf /backup/pf-home.tgz'
```

Restore only into an empty volume the compose file just created — the
`down` and `docker volume rm` above are the recipe's own steps, because
`create` makes nothing new while the old volume still stands and untarring
over live content is exactly what the whole-volume rule forbids. A
single-volume reset is that same `docker volume rm` plus the next `up -d`,
which recreates the volume with its compose labels. What a reset costs,
volume by volume:

- `pf-home` — every login and cli.toml's whole default Cell.
- `aihub` — the local hub's whole identity: the daily user, its key
  and the local `workstation` project.
- `polyforge-data` — the daemon's state and workspaces, and the
  first-boot record.
- `components` — the versioned components: the next boot reinstalls
  from the image's seed, at the image's version.
- `podman-storage` — every verification image and container: the next
  verification run builds its image again.
- `workspace` — the workstation's own working tree.

pf-home and polyforge-data are a pair — reset them together, never
apart: alone either one strands the done first-boot record against the
other (a done record with no Cell in cli.toml, or a Cell with no
record) and the container REFUSES to start naming both files (The
first boot, Troubleshooting). Reset as a pair, the next start redoes
the first boot — and in the local boot the aihub volume can stay: the
identity stages are idempotent, the daily user is found back by its
fixed email in the preserved store, the same-named key is revoked and
reissued, and the hub's own data (the SQLite store, the git repos)
survives untouched. aihub must not be reset ALONE, though: a done
first boot never runs again, so nothing rebuilds what the volume held
— the aihub server just bootstraps the empty store (usr_root and a
fresh `bootstrap-admin.key`, the very shape the first boot exists to
prevent) and the default Cell's key is dead. To wipe the hub too,
reset all three together: that start builds a whole new identity, and
you log the harnesses in again.

## Parameters

The compose file interpolates a small explicit set — host-side values
that shape the compose file itself; most of them never enter the
container, which receives only the `environment:` list the file writes
out below plus what that list interpolates. The entrypoint's whitelist
is the second guard on what does enter: it keeps `PATH`, `HOME` and
`TERM`, the compose list's values, any compose-added `PI_*` /
`PI_WEB_*` variable, and the first boot's three `WORKSTATION_AIHUB_*`
inputs, and unsets everything else, the `PI_TEST_*` family included
(env-whitelist.sh, the real filter, not prose):

| Variable | Required | What it sets |
|---|---|---|
| `PI_WEB_PASSWORD` | yes | Pi Web's login password — compose refuses to start without one |
| `PI_WEB_ALLOWED_HOSTS` | no | the extra names Pi Web trusts beside loopback ones (the HTTPS hostname belongs here) |
| `WORKSTATION_WEB_PORT` | no | the host port Pi Web publishes on 127.0.0.1 (30141 by default) |
| `WORKSTATION_HTTPS_HOST` | no | the hostname the https front serves and signs for (localhost by default) |
| `WORKSTATION_HTTPS_PORT` | no | the https front's published port (8443 by default) |
| `WORKSTATION_AIHUB_SERVER` | first boot only | the external AIHub to point the default Cell at, with its key — nothing is ever created on it |
| `WORKSTATION_AIHUB_KEY` | with the server | that server's API key: read once at the first boot, then unset before supervise starts |
| `WORKSTATION_AIHUB_PROJECT` | no | an already-existing project for the `/workspace` mapping |
| `WORKSTATION_IMAGE` | no | run a published image instead of the local build (the exported delivery's path); unset, the local build tags `polyforge-workstation:local` |

The compose file also fixes the container-side values itself (no
interpolation): `PORT: 30141` — Pi Web's port inside the container; to
change the port you see, move `WORKSTATION_WEB_PORT` — plus
`PI_WEB_HOSTNAME: 0.0.0.0`, `PI_WEB_NO_OPEN: 1`,
`PI_WEB_SKIP_VERSION_CHECK: 1` and `PI_CODING_AGENT_DIR: /root/.pi/agent`
(inside pf-home, so a login survives a re-create).

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
   roles) — the workstation is a single-user machine by design.
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

## Harness login

The seed carries the two pinned harness packages — pi and Claude Code
(`build-seed.sh` installs both at pinned versions) — under the
components volume: after the first boot they live at
`/components/current/harness/pi` and `/components/current/harness/claude`
(`node_modules/…`, the packages' own executables under
`node_modules/.bin`), and the pi packages entry in pf-home points at
the current. The image's PATH carries `pf` and the Node runtime, not
the harness CLIs — name them by their full paths (or log in through
Pi Web's terminal panel, whose sessions run inside the machine):

```sh
docker compose --profile aihub exec workstation /components/current/harness/pi/node_modules/.bin/pi
docker compose --profile aihub exec workstation /components/current/harness/claude/node_modules/.bin/claude
```

Logins land in pf-home (`/root/.pi/agent`, `/root/.claude`) and survive
a re-create. Claude Code is subject to Anthropic's Commercial Terms of Service.

## Verification with podman

The workstation runs its verification containers under podman —
rootful, with crun — inside itself. The first boot writes
`container_engine = "podman"` into the default Cell's `[daemon]`
table, so every verification bar the daemon runs goes through the
podman backend instead of the docker client — the same container
convention either way (the key is `docs/reference/config.md`'s
`[daemon] container_engine`, the container rules are
`docs/concepts.md`'s verification section). Two facts of the
placement:

- Networking: a verification container rides podman's default bridge
  network and reaches the outside like any other — the outer
  workstation's `NET_ADMIN` is what makes that possible (podman sets
  up the bridge's interfaces and firewall rules inside the outer
  container's own namespace).
- Storage: podman's graphroot is the `podman-storage` volume, a real
  filesystem of its own, because native overlay refuses
  overlay-on-overlay (the outer container's root is already overlay).
  Deleting that volume costs every verification image: the next run
  builds its image again.

`.polyforge/Dockerfile.verify` is engine-neutral and needs no change
for any of this: the daemon resolves the backend once at its start and
hands it to every step process as `PF_CONTAINER_ENGINE`. The
acceptance for the placement is the smoke's `podman-verify` pass (see
[The acceptance driver](#the-acceptance-driver)).

## Upgrading

The versions that run are the components volume's, and there are two
upgrade paths: the image switch upgrades the base (node, python, git,
podman, `pf supervise`, the bootstrap pf) and brings a new component
seed; the in-place upgrade below upgrades the versioned components —
pf (daemon and local AIHub included), the lanes and skills checkout and
Pi Web — without rebuilding the container.

For the image switch, point `WORKSTATION_IMAGE` at the new image's tag
(or rebuild the local one) and re-create the container. On the next
start the seed transaction upgrades the components volume — a newer
seed switches `current` (the `previous` link moves to the old release),
an older seed never downgrades the volume, and the same version
reinstalls nothing.

Before any upgrade, drain: `pf ops drain --upgrade` refuses new work
(new submits, automatic claims, task spawns) while work already
running goes on, and answers that it is safe when the Host is idle; a
busy one refuses with 409 until `--force` accepts the in-flight
sessions, and the refusal ends with the daemon restart — for an image
switch, the container re-create is that restart. A daemon that exits
75 — the restart-for-an-upgrade exit, set when an upgrade switches
the components volume's `current` under a running daemon — is
relaunched by `pf supervise` at once, no failure backoff, off the new
`current`.

In the image mode two entries stay closed: Pi Web's own pf install
and its self-update (an install answers 409 `image_managed`). The
first-boot record and Pi Web's status API report `imageManaged`, and
upgrading the versioned components does not go through those closed
entries: it is the in-place upgrade below, or `pf components upgrade`
from a terminal, never an install from that page.

### In-place upgrade

Inside the workstation image the versioned components — pf itself (the
daemon and the local AIHub included), the lanes and skills checkout and
Pi Web — upgrade without rebuilding the container: the Settings ›
Polyforge section's update card (or `pf components upgrade`) fetches
the channel's release, verifies it against its own SHA256SUMS, assembles
the version directory and switches the components volume's `current`
atomically, keeping `previous`. The base — node, python, git, podman,
`pf supervise` and the image's own bootstrap pf — still upgrades only
with a new image; the harnesses wait for #86 to pin them into the
release manifest.

The progress and the final result (the phase, the versions, a
refusal's reason) persist in `/components/upgrade-status.json`, so the
page reconnects and replays the outcome across Pi Web's own restart;
the orchestration runs detached from the Pi Web process tree. While
the upgrade drain waits for the running work, new work is refused
DAEMON-409; the wait can be stood down (the daemon restart clears the
drain) or forced. After the switch the daemon exits 75 and the
supervisor relaunches it — and the old tree's Pi Web and AIHub — on the
new `current`. `pf components rollback` swaps `current` and `previous`
through the same drain and restarts; a rollback is refused — changing
nothing — when the previous is missing or corrupt, of another channel,
of a manifest schema this build cannot judge, of another platform, of
a data layout the data directory has migrated past (the previous
version's declared `dataLayout` — a version older than the field is
never machine-judged), or below the running base's floor. Only a previous version that
declares its `dataLayout` is judged against the data directory's live
layout; a version older than the field is never machine-judged — back
up before upgrading and follow the version's migration notes before
rolling back to one.

## Platform support

Verified: Linux amd64 — the engine, the image build and every
acceptance pass of the smoke ran there. The release pipeline builds the
image for linux/amd64 AND linux/arm64 (polyforge#86, one buildx pass; the
arm64 index is verified by `release.sh --verify`), but no acceptance pass
has run on the arm64 index yet: a real Mac lands with polyforge#168's
verification, and macOS (Docker Desktop) and Windows stay not verified
here. The
host-installed pf's own platform story is unchanged and wider —
`docs/install.md` — this page is about the workstation image only.

## The acceptance driver

`smoke.sh` measures every acceptance value and exits non-zero on the
first miss. Run it from the repository root:

```sh
sh dist/workstation/smoke.sh boot --no-aihub    # the minimal boot: parameters, PID 1, no aihub process
sh dist/workstation/smoke.sh boot --with-aihub  # + Pi Web /login, the daemon queue, the default Cell
sh dist/workstation/smoke.sh first-boot         # the empty-volume first boot: identity, default Cell,
                                                #   real key authentication, no bootstrap key, restart
sh dist/workstation/smoke.sh first-boot --external  # the external mode: only POST /api/v1/machines/heartbeat
                                                #   crosses the whole pass; every create route and unknown
                                                #   request fails, its method and path printed
sh dist/workstation/smoke.sh onboarding         # the clean-room first open (polyforge#168, AC1): a fresh
                                                #   volume's /api/onboarding is unfinished, the doctor's
                                                #   necessary rows pass, the one default Cell serves the
                                                #   local AIHub, and the host carries no component
sh dist/workstation/smoke.sh persist            # the persistence acceptance: two down/up cycles and a
                                                #   NEW TAG of the image keep the seven persisted facts
                                                #   (six volume facts + the onboarding completion record)
                                                #   by value, then the exported copy runs (Docker + sh host)
sh dist/workstation/smoke.sh env                # Pi Web parameters, PI_TEST_* excluded, /api 401
sh dist/workstation/smoke.sh podman-verify      # verification via podman end to end
```

`boot --with-aihub`, `first-boot`, `onboarding` and `env` need the
components volume to carry the seed's Pi Web (the first boot installs
it); `first-boot`, `onboarding` and `persist` reset volumes first —
their own, never an operator's: the
smoke derives its compose project and host port per run
(clock + pid, `WORKSTATION_WEB_PORT` overrides the port), so every reset
and cleanup only ever touches that run's own `workstation-smoke-*`
project and volumes, and compose has no fixed `container_name` any more,
so a concurrently running deployment never clashes with the smoke at
all (polyforge#177). Every pass ends with a `down -v` of its own project
and carries an EXIT trap doing the same, so a die mid-run leaves nothing
behind either (review r1#3); `persist` also removes the retag it created. The real-engine
smoke and the external-mode stub tests serialize their shared projects through a
cross-process resource lock (test/w535_workstation_lock_test.go); the external-mode
stub listens on 38422 and the real-engine counter stub on 38421, so a parallel test
runner never lets one test clean up another's containers or volumes. The first-boot
pass is the AC proof of the identity half: after the boot the record
says done, the default Cell points at the local AIHub, the key REALLY
authenticates (`pf recall` inside the container, through the
`/workspace` mapping), no `bootstrap-admin.key` exists, and a restart
with the volumes intact changes nothing — the same key still
authenticates (no reissue).

`onboarding` is AC1's script half in one pass (polyforge#168): it starts
the default AIHub boot on a fresh volume, then reads, **inside** the
container, the image-managed `/api/onboarding` view — all four steps
unfinished, `nextStep` `aihub`, the first-boot connection local with
server `http://127.0.0.1:8420` and default Cell `default` — the
`pf --json doctor` rows the acceptance needs (`config`, the workspace
mapping and dirs, `server reach`, `daemon socket`, `daemon supervision`,
`workflow lanes`, `aihub projects`, `auth`; every other row may warn or
skip and the report's overall `ok` is never the proof), and the
`pf --json cell list` registry holding exactly one `default` Cell that
is `serving`, `valid`, on the local AIHub. It also refuses a host that
carries `pf`, `aihub`, `pi` or `claude` — the clean-room precondition
(AC1). An HTTP 200 alone is never a pass, and the browser's own first
open stays the human's. The manual clean-room checklist, the sanitized
evidence template and the owner-run parts (a sandbox ticket to a merged
PR, the Apple-silicon Mac's arm64 boot with the in-container Podman
pass, the team path recorded NOT RUN) live in the release reference's
[clean-room acceptance](../reference/release.md#clean-room-acceptance-workstation)
section.

The first-boot pass also asserts AC2's layout half inside the container:
the step agent dir is `/root/.pi/agent` (pf-home), the pi packages entry
points at `/components/current`, and the record carries no credential
— the key in cli.toml compared byte-for-byte with the record's.
When `/root/.claude` already exists, it also asserts the routing
skill `/root/.claude/skills/polyforge` is a symlink to
`/components/current/skills/polyforge`; the user's own other skills
are untouched.

`persist` (polyforge#177, polyforge#168) is the persistence acceptance:
after a first boot it records the seven persisted facts BY VALUE — the
harness login marker, a cli.toml sentinel line, a workspace file, the
AIHub database's user row, a pulled Podman image, the components
volume's `current` link plus its manifest digest, and the onboarding
wizard's completion record (completed through Pi Web's own API; the
record and every check run inside the container, so the host needs only
Docker and the system sh) — then
proves them unchanged through two `compose down && up` cycles and a
NEW TAG of the image (`docker tag`, the second tag ups in
`WORKSTATION_IMAGE`'s place). The image is the given
`WORKSTATION_IMAGE` (it must already exist on the engine — no build);
without one the pass builds and tags the local default and retags it
for the new-tag phase. Finally it exports `smoke.sh`, `compose.yaml`,
the pinned seccomp files and the `Caddyfile` to an empty directory and
runs `first-boot` and `persist` from there on the tagged image,
building nothing — the delivered shape an operator's empty machine
runs. A crashed run leaves its own uniquely-named project behind; the
next run never touches it.

`podman-verify` mounts the repository read-only at `/workspace` inside
the workstation and runs, through the engine's own client, the real
`Dockerfile.verify` build, the prepared-image path, and the three
real-container test modules with `PF_CONTAINER_ENGINE=podman`, then
proves no residual container, no zombie and no resident conmon survives.

The in-repo Go tests pin the same facts statically
(`test/w535_workstation_image_test.go`: `TestWorkstationImage`,
`TestWorkstationCompose`, `TestWorkstationSmoke`,
`TestWorkstationSmokeFirstBoot`, `TestWorkstationSmokePersist`,
`TestWorkstationSmokeHostDependencies`) and drive the `--no-aihub` boot for
real on a machine with an engine (`TestWorkstationImageEngine`,
skipped without one).

## Troubleshooting

- The container refuses to start, naming two files — pf-home and
  polyforge-data disagree (a done first-boot record without the Cell
  in cli.toml, or the reverse): the two volumes were reset apart.
  Restore or reset the pair together (the aihub volume's reset rules
  are the volumes section's: keep it on a pair reset, never reset it
  alone).
- A first boot that dies — read the phase record inside the container:
  `docker compose --profile aihub exec workstation cat
  /root/.local/share/polyforge/workstation/first-boot.json`. The
  `phase` names the stage that stopped (`<stage>/failed`) and `error`
  the reason (never a credential); fix the cause and restart — every
  stage is idempotent, and an interrupted boot resumes from the
  record.
- "Is it really podman in there?" — `pf config get
  daemon.container_engine` reads the default Cell's `[daemon]` table
  back (`podman` after the first boot). `pf doctor` does not report
  the engine choice; its supervision row passes on the container
  marker either way, and a missing tmux inside the container is not
  even a warn there (`pf supervise` is the supervision).
