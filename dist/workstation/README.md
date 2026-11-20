# dist/workstation — the workstation image and compose (polyforge#161, 84-image)

The workstation container is a whole polyforge machine in one container:
podman (rootful, crun) runs the verification containers, `pf supervise`
(#160) keeps the daemon, Pi Web and — in the default boot, with
`--with-aihub` — the aihub server alive, and six named volumes carry
everything that persists: pf-home (the whole `/root`: cli.toml, the pi
agent dir, the logins), the data dir (the daemon's state, the workspaces,
`workstation/first-boot.json`), the components volume (the versioned
components run from there), the AIHub store, the `/workspace` working
tree and podman's storage. The base image carries the runtime and one
complete component seed: podman, crun, catatonit (PID 1), the base pf at
`/usr/local/libexec/polyforge/pf` (the bootstrap the entry point runs by
absolute path), the container configs, the Node runtime (Pi Web and the
two harness CLIs run on it — nothing installs at boot), and — read-only,
at `/opt/polyforge/seed/releases/<ver>` — the seed the first boot installs
(#162, 84-components-A): the release checkout, `bin/pf`, Pi Web with its
production build and the two pinned harness packages, pinned by a manifest
the install verifies. The image's PATH `pf` links to
`/components/current/bin/pf`, live on the current version after an
upgrade; the base pf remains the entrypoint bootstrap. The versions that
RUN live in the components volume (`releases/<ver>` there, with `current`
and `previous` at the volume root), never in the image: a newer seed
upgrades the volume, an older seed never downgrades it.

The first boot (#176, 84-components-C) runs before supervise: the seed
install, the AIHub identity (a daily user, its key, the local project —
never the bootstrap admin), cli.toml's default Cell, the harness
references toward `/components/current`, and the phase record at
`<data-dir>/workstation/first-boot.json` (never a credential). A
completed boot skips itself; an interrupted one resumes without
duplicating the user. The external mode's inputs
(`WORKSTATION_AIHUB_SERVER`/`KEY`/`PROJECT`) ride the container
environment. Full reference: docs/workstation.md, "The first boot".

## Files

| File | What it is |
| --- | --- |
| `Containerfile` | the base image: webui dist + pf built from this repository (context: the repo root), ubuntu:24.04 with podman/crun/catatonit/git/curl/python3, the Node runtime and the seed stage |
| `build-seed.sh` | the seed assembly, run inside the image build: Pi Web's `npm ci` + production build + prune, the two pinned harness packages, `bin/pf`, then seedtool writes and verifies the seed's manifest (never on a host) |
| `entrypoint.sh` | cgroup-namespace init (as root), then the environment boundary whitelist, then the base pf's absolute path running first boot and `pf supervise` |
| `env-whitelist.sh` | the environment boundary itself, sourced: PI_*/PI_WEB_* pass, PI_TEST_* never, the first boot's WORKSTATION_AIHUB_* pass, the fixed `BUILDAH_ISOLATION=chroot` and PORT's default |
| `containers/storage.conf` | native overlay on the `podman-storage` volume |
| `containers/containers.conf` | cgroupfs, file events, no container keyrings |
| `containers/registries.conf` | short names resolve to docker.io |
| `seccomp.json` + `seccomp.json.sha256` | the pinned seccomp profile the outer engine applies (below) |
| `compose.yaml` | the outer container's minimal parameter set, the six persistent volumes and the two boot modes (the aihub boot is the default; `workstation-noaihub` the explicit opt-out) |
| `Caddyfile` | the optional HTTPS front's config (`--profile https`): local-CA TLS in front of Pi Web |
| `smoke.sh` | `boot` / `first-boot [--external]` / `env` / `podman-verify` — the acceptance driver |

## The pinned seccomp profile

`seccomp.json` is the upstream default container template — moby's
`profiles/seccomp/default.json` at tag **v28.0.0** (byte-identical with
v27.5.1) — plus exactly one added syscall group:

```json
{ "names": ["pivot_root"], "action": "SCMP_ACT_ALLOW" }
```

`pivot_root` is the one syscall a nested crun container needs that the
template does not already allow behind `CAP_SYS_ADMIN` (measured: with
the plain default template a nested container dies at
`pivot_root: Operation not permitted`). `seccomp.json.sha256` records
the SHA-256 of the pinned bytes in `sha256sum` form; compose applies the
profile by content and the smoke script compares content hashes, so a
profile that drifted from the pin fails the boot check. The upstream
template is never downloaded at build time (settled Q4): the artifact is
committed, the origin is recorded here.

## Why `BUILDAH_ISOLATION=chroot` and `keyring = false`

Both are measured consequences of the ticket's minimal parameter set
(`SYS_ADMIN` + `NET_ADMIN`, pinned seccomp, no `privileged`):

- `keyring = false` — the pinned profile (like docker's default) does
  not allow `add_key`, so a container-side session keyring fails at
  crun's create; podman passes conmon `--no-new-keyring`, and the
  engine's build path gets the same from PodmanCLI's generated crun
  wrapper.
- `BUILDAH_ISOLATION=chroot` — buildah's build-network setup opens the
  freshly-created runtime init's netns (`/proc/<pid>/ns/net`), which
  needs `CAP_SYS_PTRACE` on a non-dumpable process; the minimal cap set
  deliberately has none, so `chroot` isolation (build steps share the
  workstation's own network, no namespace open) is the build path. The
  entrypoint exports it and the daemon's environment allow-list
  (`internal/polyforge/supervisor/env.go`) carries the name to every
  step.
