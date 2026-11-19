# dist/workstation — the workstation image and compose (polyforge#161, 84-image)

The workstation container is a whole polyforge machine in one container:
podman (rootful, crun) runs the verification containers, `pf supervise`
(#160) keeps the daemon, Pi Web and — with `--with-aihub` — the aihub
server alive, and the named volumes carry everything that persists (the
data dir with the release layout, and podman's storage). The base image
carries only the runtime: podman, crun, catatonit (PID 1), the pf binary
and the container configs. Every versioned component lives in the data
volume (`<data-dir>/release/current/…`, 84-components), never in the
image.

## Files

| File | What it is |
| --- | --- |
| `Containerfile` | the base image: webui dist + pf built from this repository (context: the repo root), ubuntu:24.04 with podman/crun/catatonit/git/curl/python3 |
| `entrypoint.sh` | cgroup-namespace init (as root, before `pf supervise`), then the environment boundary whitelist |
| `containers/storage.conf` | native overlay on the `podman-storage` volume |
| `containers/containers.conf` | cgroupfs, file events, no container keyrings |
| `containers/registries.conf` | short names resolve to docker.io |
| `seccomp.json` + `seccomp.json.sha256` | the pinned seccomp profile the outer engine applies (below) |
| `compose.yaml` | the outer container's minimal parameter set |
| `smoke.sh` | `boot` / `podman-verify` / `env` — the acceptance driver |

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
