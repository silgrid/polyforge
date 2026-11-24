# Getting started — from Docker to your first work item

The newcomer mainline: one host with Docker on it, the workstation image, and
a first work item filed, submitted and watched. It assumes Docker and nothing
else — the image carries the daemon, the local AIHub, Pi Web, podman and the
workflows. Every mechanism it touches has one owner under `docs/`; this page
links there and never restates it.

## Platform support

- **Linux amd64 is the verified platform.** The engine, the image build and
  every acceptance pass of the workstation smoke run there; the arm64 image is
  published, but no acceptance pass has run on it yet.
- **macOS and Windows are not verified for the workstation image.** The
  container runs under Docker Desktop in principle, but no acceptance pass has
  booted it on either, and this page makes no Windows support promise. The
  macOS **host** installation remains supported — [the macOS
  Quickstart](quickstart-macos.md) — and the host path in general is
  [install](install.md).

## 1. Docker

The workstation is a container, so the host needs Docker with compose v2:
`docker compose version` must answer. Nothing else is installed on the host.

## 2. Pull the image

The workstation image ships as a release bundle (polyforge#86's release
pipeline builds it). Until a published release lands the bundle, this page
carries greppable placeholders — replace each `<PF_...>` with the value the
release notes name; a placeholder never sits in a `pf` command:

```sh
docker pull <PF_IMAGE>
```

## 3. Start it with compose

The compose file is the single place the machine's parameters live — the two
capabilities `SYS_ADMIN` and `NET_ADMIN`, the pinned seccomp profile, the
private cgroup namespace, the six persistent volumes and the two boot modes.
Download the delivered bundle beside its `seccomp.json` (the profile is
referenced by a relative path and is not optional) and start the default
boot, which runs a local AIHub:

```sh
mkdir -p ~/polyforge-workstation && cd ~/polyforge-workstation
curl -fsSLO <PF_COMPOSE_URL>     # compose.yaml
curl -fsSLO <PF_SECCOMP_URL>     # seccomp.json, referenced beside the file
export WORKSTATION_IMAGE=<PF_IMAGE>
export PI_WEB_PASSWORD='<a long, unique secret>'
docker compose --profile aihub up -d
```

`WORKSTATION_IMAGE` runs the published image instead of a local build; the
password is required or the container refuses to start rather than expose the
high-privilege UI bare. Verify the two downloads against the sums the release
publishes (`sha256sum`) before the machine is trusted with anything — the
workstation page names the pinned seccomp file's origin and SHA. What the
parameters mean, the six volumes and their
reset cost, HTTPS and the security posture are
[the workstation page](workstation.md) — read it before the machine is trusted
with anything.

## 4. The first boot

The container's entrypoint runs `pf first-boot` exactly once per start, before
`pf supervise`: it installs the components, establishes the daily user and the
local `workstation` project, writes the default Cell and points the harness
packages at the components volume. The stages, the record it publishes and
the one deliberate consequence — the local hub carries no administrator
identity, so the workstation is a single-user machine by design — are
[the first boot](workstation.md#the-first-boot).

Pi Web answers on the host's loopback at `http://127.0.0.1:30141`. Sign in
with the password above; the Polyforge panel is where every human decision
lands. The harness login (pi and Claude Code) is
[harness login](workstation.md#harness-login).

The first time Pi Web opens in the image it shows first-time setup — four
short steps in the order the machine needs them: the **AIHub connection**
(the local hub the first boot established, already shown and confirmed, or a
team AIHub's profile, server and key), the **account** (Settings › Account
signs each harness in), the **workspace**, and the **first ticket** (filed in
the develop lane's new-ticket dialog). Every step can be skipped, and a
skipped step stays **Not done**; the wizard saves no configuration of its own
— each step reads and writes the page it reuses. Finishing or closing it keeps
it away, and Settings › General's **Reopen first-time setup** brings it back at
any time. The wizard belongs to the image: a host-installed Pi Web does not
show it, and the shell flow in the next section is the same four steps without
it.

## 5. Your first work item

Everything below runs **inside the container** — open a shell with
`docker compose --profile aihub exec workstation sh` (`exec` names the
service; the no-AIHub boot's service is `workstation-noaihub`). The default
Cell, the `/workspace` mapping and the local project come from the first boot.

The prerequisites are real: a harness login with a model configured, a project
whose repository mapping exists, and the shell in the container (the
`/workspace` cwd the mapping names). With those in place the flow is the one
[the user guide](user-guide.md) teaches in full:

1. **File it.** `pf file create --project workstation --bundle develop
   --goal "Add a status column"` prints the new work item's id.
2. **Submit it.** `pf submit <WI_ID>` asks the local daemon to claim it; the
   execution driver takes over and the lane's steps run in this capsule.
3. **Watch it.** `pf watch <WI_ID> --once` shows one snapshot — status,
   current step, latest events.
4. **Answer it.** A question arrives in the decision feed (the Polyforge panel
   in Pi Web); in a shell `pf watch <WI_ID> --once` shows its text verbatim,
   and `pf answer <id> --text "<your words>"` delivers the human's own words.
5. **Deliver it.** The lane's own publish and merge steps open and land the
   PR; `pf wrap` is the human close verb for a bundle-less ticket, not the
   workflow's completion step.

## 6. Upgrade

Inside the image the versioned components — pf itself (the daemon and the
local AIHub included), the lanes and skills checkout and Pi Web — upgrade
without rebuilding the container: the Settings > Polyforge update card or
`pf components upgrade` switches the components volume's `current` atomically,
keeping `previous`, and `pf components rollback` swaps back. The base (node,
python, git, podman, `pf supervise`) upgrades only with a new image. The
drain, the status file and the rollback refusals are
[the in-place upgrade](workstation.md#in-place-upgrade) (polyforge#190); the
per-version history is [upgrade](upgrade.md).

## Delivery placeholder registry

Until a published release lands the bundle, three placeholders stand in for
values this page may not guess. Replace each with the release note's value;
each appears only in the docker/compose commands above, never in a `pf`
command:

| Placeholder | Where it appears | What it stands for |
|---|---|---|
| `<PF_IMAGE>` | `docker pull`, `WORKSTATION_IMAGE` | the published workstation image reference |
| `<PF_COMPOSE_URL>` | the `curl` that downloads the bundle | the delivered `compose.yaml` |
| `<PF_SECCOMP_URL>` | the `curl` that downloads the profile | the `seccomp.json` the compose file references beside it |

## Where to read next

- [Concepts and glossary](concepts.md) — every term, defined once.
- [The user guide](user-guide.md) — daily use: filing, submitting, watching,
  answering, closing.
- [The workstation page](workstation.md) — the image, its volumes, parameters,
  security, HTTPS and upgrades.
- [The install page](install.md) — the host installation for a machine
  joining an existing team.
