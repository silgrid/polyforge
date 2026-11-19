# The workstation image

The workstation is a whole polyforge machine inside one container: the
daemon runs there, `pf supervise` keeps it (and, in the AIHub boot
mode, the aihub server) alive, and podman — rootful, with crun — runs
the verification containers. The image is only the base: podman, crun,
catatonit as PID 1, the pf binary and the container configs. Every
versioned component (the workflows tree, Pi Web, the release layout)
lives in the data volume under `<data-dir>/release/current`
([bundles](bundles.md) carry them; the component volume's first boot is
the 84-components work item), so the image never needs rebuilding when a
component version moves.

Everything the workstation needs lives in `dist/workstation/`:

| File | What it is |
|---|---|
| `Containerfile` | the base image: webui's dist tree and the pf binary built from this repository, on ubuntu with podman, crun, catatonit |
| `entrypoint.sh` | cgroup-namespace init as root, then the environment boundary, then `pf supervise` |
| `compose.yaml` | the outer container's minimal parameter set and the two boot modes |
| `containers/` | podman's storage, containers and registries configuration |
| `seccomp.json` | the pinned seccomp profile the outer engine applies (origin and SHA beside it) |
| `smoke.sh` | the acceptance driver: `boot`, `env`, `podman-verify` |

## Boot modes

The outer container runs the measured minimal parameter set — exactly
the capabilities `SYS_ADMIN` and `NET_ADMIN`, the pinned seccomp file,
a private cgroup namespace, AppArmor and system paths unconfined, no
`privileged`, no devices and no docker socket — and the compose file is
the single place those parameters live. The stop grace (90s) sits above
`pf supervise`'s own (60s), so the daemon's shutdown always finishes
before the outer SIGKILL.

There are two boot modes, one compose profile each (compose activates a
profile only when named, so every start names one):

```sh
cd dist/workstation
PI_WEB_PASSWORD='<a long, unique secret>' docker compose --profile no-aihub up -d --build
PI_WEB_PASSWORD='<a long, unique secret>' docker compose --profile aihub     up -d --build
```

The `aihub` profile passes `--with-aihub` to `pf supervise`: the aihub
server starts as another watched child, and Pi Web (once the component
volume carries it) answers on the published port. The password is
required — without one, Pi Web's proxy lets `/api` through and
redirects `/login`, and the container refuses to start instead of
exposing the high-privilege UI bare. The container's environment is the
compose file's explicit list plus the `PI_*` families the entrypoint's
whitelist passes (never `PI_TEST_*`); the host's environment never
crosses the boundary.

Two named volumes persist everything: `polyforge-data` at
`/root/.local/share/polyforge` (the data dir, the release layout and
the daemon's state) and `podman-storage` at
`/var/lib/containers/storage` (podman's image and container storage,
on its own filesystem because native overlay refuses
overlay-on-overlay). Back up and restore whole volumes, never files
under them.

## The acceptance driver

`smoke.sh` measures every acceptance value and exits non-zero on the
first miss. Run it from the repository root:

```sh
sh dist/workstation/smoke.sh boot --no-aihub    # the minimal boot: parameters, PID 1, no aihub process
sh dist/workstation/smoke.sh boot --with-aihub  # + Pi Web /login, the daemon queue, the default Cell
sh dist/workstation/smoke.sh env                # Pi Web parameters, PI_TEST_* excluded, /api 401
sh dist/workstation/smoke.sh podman-verify      # verification via podman end to end
```

`boot --with-aihub` and `env` need the data volume to carry the
release layout's Pi Web component; on an empty volume they fail saying
so, which is the honest answer — those halves cannot hold without the
components the workstation is designed to run. `podman-verify` mounts
the repository read-only at `/workspace` inside the workstation and
runs, through the engine's own client, the real `Dockerfile.verify`
build, the prepared-image path, and the three real-container test
modules with `PF_CONTAINER_ENGINE=podman`, then proves no residual
container, no zombie and no resident conmon survives.

The in-repo Go tests pin the same facts statically
(`test/w535_workstation_image_test.go`: `TestWorkstationImage`,
`TestWorkstationCompose`, `TestWorkstationSmoke`) and drive the
`--no-aihub` boot for real on a machine with an engine
(`TestWorkstationImageEngine`, skipped without one).
