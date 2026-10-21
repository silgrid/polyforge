# Polyforge

[![License: PolyForm Noncommercial 1.0.0](https://img.shields.io/badge/license-PolyForm%20Noncommercial%201.0.0-blue)](LICENSE)

Work-item orchestration platform: an aihub server (WI store, memory, git
revision chain) and a pf work-machine binary (CLI + daemon with the Go
execution driver).

## Platform support (polyforge#218)

- **Linux x86_64 is the reference platform** — the aihub docker image stays
  linux/amd64-only and production deploys through `deploy.sh` on Linux hosts.
- **macOS (Darwin) arm64 and amd64 are supported as work machines and as a
  single machine — hardware-verified**: releases
  ship `pf` and `aihub` darwin binaries, the daemon is supervised by tmux on
  the same single code path as on Linux, and `pf doctor`'s check set runs
  unchanged, plus one additive warn-only `prerequisites` row that reports
  missing tools. The hardware verification — install, upgrade, daemon,
  doctor, execution (step result delivery), and the Quickstart flow —
  passed on real Mac hardware against the released darwin assets.
  macOS is not a CI platform.
- WSL2 is treated as Linux.
- Native Windows is out of scope (not supported).

## Install (team machines — read this first)

One line, any machine, any harness (or none). Ask the admin for a personal
API key (`pf_k1_…`), then:

```
curl -s -H "Authorization: token $(gh auth token)" \
  https://raw.githubusercontent.com/silgrid/polyforge/main/tools/install.sh \
  | bash -s -- <your-api-key> <server-host>
```

(requires `gh` authenticated — `gh` needs a login, even for a public repo,
and the one-liner reads `gh auth token`. `<server-host>` is the
aihub host the admin gives you, as the second argument or `PF_HOST=<host>`;
there is no built-in default. It is required for a first install — the run
that writes `~/.config/polyforge/cli.toml`, refused before anything changes
without it — and optional on an upgrade: a machine whose config already
carries an `api_key` re-runs the line with `<your-api-key>` alone.)

**macOS notes.** The installer picks this machine's own pf asset from
`uname` (darwin-arm64 or darwin-amd64; no `uname`-detectable cross-pick —
the same one line works on either Mac). It CHECKS and REPORTS its
prerequisites — `gh`, `curl`, `python3`, `git` — and never installs them:
point each missing tool at `brew install` / `xcode-select --install` (the
refusal is printed before anything on the machine is touched). The
downloaded binary is SHA-verified with `shasum -a 256` when `sha256sum` is
absent (macOS ships `shasum`). On darwin the installer clears the quarantine
attribute (`xattr -c`) and ad-hoc signs (`codesign -f -s -`) the pf it
installs, and refuses with nothing changed if it cannot. The binary default install path is
`/usr/local/bin` — override with `PF_BIN=…` on a macOS without sudo — and
the host (second argument or `PF_HOST`) decides what a new config points at;
for a machine running its own aihub use `127.0.0.1`. tmux is the
daemon's supervision: without it `pf daemon ensure` still starts the daemon,
unsupervised with a WARNING (`pf doctor`'s "daemon supervision" row says so).
A Mac that only ever talks to the central Linux aihub stays exactly where the
bare one-liner above lands it; the single-machine form is the Quickstart
below.

It installs the `pf` binary (downloaded from the GitHub Release with `gh release download` and checked against its `SHA256SUMS`), writes
`~/.config/polyforge/cli.toml` (never rewritten), and keeps its own checkout
of the release tag in `~/.local/share/polyforge/release/<tag>` (stable
symlink `release/current`, previous version kept). From that checkout it
wires the `skills/polyforge` routing skill into each harness it finds,
independently, by REFERENCE (nothing copied, nothing cloned twice):
Claude Code (`~/.claude` or `claude` on PATH) gets `~/.claude/skills/polyforge`
as a symlink to `release/current/skills/polyforge` plus a SessionStart hook
registered once in `~/.claude/settings.json`; pi (on PATH, or its agent dir
present) gets one local package entry naming `release/current` in its
settings (replacing an older `git:...silgrid/polyforge.git@<tag>` entry).
It then reports
whether each harness will display polyforge progress (the `pf doctor
--harness` check: Claude Code's statusLine running `pf watch --statusline`,
pi's pf-chat extension) and prints a copyable snippet when not — it never
edits your statusLine — plus the install-layout and v1-leftover checks.
Then it starts the daemon through `pf daemon ensure` (once a `[workspace]`
mapping exists): a restart loop in tmux session `polyforge-daemon` runs it
with `-workflows-dir release/current/workflows` and revives it within ~5s
after a crash; after a container/machine restart (tmux does not survive
one) the next pi or Claude Code session runs the same ensure. An upgrade
first downloads, verifies and fetches everything, refuses (changing nothing)
while the daemon runs jobs — wait, `pf pause` them, or re-run with
`PF_INSTALL_FORCE=1` in front of `bash` — then shuts the daemon down gracefully (the loop ends with that clean exit —
`pf ops shutdown` always stops for good, only failures are restarted — except
exit 78, below),
swaps the binary, then ensures again; meanwhile it holds the ensure lock,
so a harness session starting mid-upgrade starts nothing. A daemon already running with a different workflows
dir, or without its loop, is reported, never auto-restarted. Finally it
runs `pf doctor`. If the machine's daemon id is new to aihub, an admin
registers it afterwards — the installer prints a note to that effect pointing
at `pf ops status`, which shows the daemon's id. Re-running the same command upgrades
and is idempotent. Daily use from a session is natural language via the
`polyforge` routing skill; from a terminal it's
`pf queue`, `pf submit`, `pf answer`, `pf ops status`, …
Daily-use detail — the flow, the core concepts, memory and the
when-things-go-wrong moves — is [docs/user-guide.md](docs/user-guide.md),
the daily-use guide.

## Quickstart (developers — from-zero smoke run)

A from-zero smoke run on one machine: start a local aihub, set up a
project and a machine identity, file a minimal work item, and drive it
through file → submit → watch → wrap on a local `pf daemon`.

Prerequisites: a Go toolchain; `git` with a configured
`user.name`/ `user.email` (capsule commits need it); and the `pi` agent
harness on `PATH` with a model configured — the daemon's execution driver
spawns the real step processes through it. All commands below run from the repo root; everything
local lands under `/tmp/pf-quickstart` (substitute your own paths).

**1. Build the two binaries:**

```
go build -o bin/aihub ./cmd/aihub
go build -o bin/pf ./cmd/pf
```

**2. Start aihub on a scratch data dir and port.** On first start over an
empty data dir, aihub seeds a bootstrap admin (`usr_root`) and writes its
API key to `<data-dir>/bootstrap-admin.key` (mode 0600; the log names the
file, never the key). Copy the key, then delete the file — the key is not
stored anywhere else:

```
mkdir -p /tmp/pf-quickstart/aihub-data
bin/aihub --data-dir /tmp/pf-quickstart/aihub-data --listen 127.0.0.1:18420 \
  > /tmp/pf-quickstart/aihub.log 2>&1 &
cat /tmp/pf-quickstart/aihub-data/bootstrap-admin.key
```

**3. Point `pf` at the server with a config file** (the flat `cli.toml`
subset plus the `[workspace]` mapping; every connection entry can also be
given per-command via the global `--server` / `--api-key` / `--socket`
flags). The mapping below is what makes the daemon startable — one
section per workspace, listing the projects whose capsules live there:

```
mkdir -p /tmp/pf-quickstart/workspace
cat > /tmp/pf-quickstart/cli.toml <<'EOF'
server = "http://127.0.0.1:18420"
api_key = "<BOOTSTRAP_KEY>"
daemon_socket = "/tmp/pf-quickstart/pf.sock"

[workspace."/tmp/pf-quickstart/workspace"]
projects = ["demo"]
EOF
```

**4. Create the project and a machine identity.** The machine user's API
key is the daemon's credential below; its plaintext is shown exactly
once:

```
bin/pf --config /tmp/pf-quickstart/cli.toml projects create --name demo
bin/pf --config /tmp/pf-quickstart/cli.toml users create --display-name smoke-machine --user-type machine
bin/pf --config /tmp/pf-quickstart/cli.toml api-keys create --user <MACHINE_USER_ID>
```

**5. File a minimal smoke work item.** `--bundle` makes it an execution
ticket — only execution tickets can be claimed; `autopilot` is one of
the workflow bundles shipped in `workflows/`:

```
bin/pf --config /tmp/pf-quickstart/cli.toml file create --project demo \
  --bundle autopilot \
  --goal "Quickstart smoke: exercise the file -> submit -> watch -> wrap chain." \
  --content "Minimal smoke work item; no product change is expected." \
  --label smoke
```

**6. Start the local daemon** (runtime, driver and watcher behind its own UDS
socket; keep it running in a second terminal). Capsules route by the
claimed work item's project through the `[workspace]` mapping — the `demo`
section above sends `demo` capsules to
`/tmp/pf-quickstart/workspace/capsules/` (the mapping's rules:
[Extending › Workspace mapping](#workspace-mapping)):

```
bin/pf daemon --config /tmp/pf-quickstart/cli.toml \
  --api-key <MACHINE_KEY> \
  --data-dir /tmp/pf-quickstart/daemon-data \
  --machine-id m-smoke
```

If a workspace lives inside a git checkout, ignore the capsule
directory in its `.gitignore` (`capsules/`) — the daemon never touches
`.gitignore` itself. `pf doctor` checks the mapping and the
connectivity faces.

**7. Submit the work item and watch it run:**

```
bin/pf --config /tmp/pf-quickstart/cli.toml submit <WI_ID>
bin/pf --config /tmp/pf-quickstart/cli.toml watch <WI_ID> --once
```

The submit claims the work item and the daemon's **Go execution driver**
takes over: an in-daemon engine that reads and writes
`runtime-state/execution.json` (its single state authority) and spawns one
**step process** per workflow step — real `pi` harness sessions — collecting
each step's result envelope, validating it, and advancing the workflow. A
plain submit is the ordinary claim. A WI whose holder daemon is dead or
stuck is recovered in two ordinary steps: an admin or project maintainer runs
`pf ops end-session <id> --reason <text>` (the session is ended, the WI is
`queued` again, and the old holder is fenced — its secret no longer matches, so
its pushes and verdicts are refused and its own session revalidation makes it
release), then any daemon claims it with a plain `pf submit <id>`; the claim
restores progress from the aihub capsule revision chain (and the task repos'
`refs/pf-task` carrier refs) and the driver continues from the recorded current
step. `pf watch <id> --once` shows a
snapshot read from the capsule's `execution.json`: status, current step and
how long it has run, the pending question and the latest events;
`pf watch --chat "$PF_CHAT_ID" --wait-decision` is the resident per-chat
watcher that wakes a harness session only for human decisions (see
`skills/polyforge/SKILL.md`). Give the first step a
few minutes: once the driver commits the first `runtime-state/execution.json`
version, the snapshot shows a current step and a `published` event.

**8. Wrap the run.** Wrap seals the capsule's current state and ends
the session as terminal success:

```
bin/pf --config /tmp/pf-quickstart/cli.toml wrap <WI_ID>
bin/pf --config /tmp/pf-quickstart/cli.toml queue --project demo
```

The queue now lists the work item as `wrapped`; its SESSION column is
`-` (unheld — while a session is live it shows the holder's public
fingerprint). Note: wrapping before the
capsule's first commit lands leaves no revision to seal — the close
degrades to `blocked`, and the WI is not claimable again until
`pf resume <WI_ID>` continues it, or `pf cancel <WI_ID>` abandons it (the task's
reporter, a project maintainer or an admin; the same two moves apply to a WI the
daemon set `blocked` at restart for a missing capsule / unmapped project — see
below; `pf wrap` / `pf wrap --fail` do not apply to a blocked WI — there is no
completed run to wrap or fail, and the daemon refuses with DAEMON-409).
`pf pause <WI_ID>` sets a clean_pause (a daemon restart will not
auto-resume it); `pf resume <WI_ID>` clears the flag and the holder
daemon re-attaches on its next start — to move it elsewhere (or immediately),
`pf ops end-session <id> --reason <text>` then `pf submit <id>`.

**9. Tear down:**

```
bin/pf --config /tmp/pf-quickstart/cli.toml ops shutdown
kill <AIHUB_PID>
```

## Quickstart (macOS — one Mac as one machine: local aihub + pf + daemon + lane)

Everything below uses existing commands only — the macOS-native aihub
asset from the release, the same CLI verbs, and `tools/install.sh` (a
port conflict beware: aihub here keeps the standard port 8080 so the
installer's config matches; use another port only if 8080 is taken —
both sides, consistently).

**1. Download both native darwin assets from the current release** —
`aihub-darwin-arm64` (or `-amd64` on an Intel Mac) and `pf-darwin-*`:

```
VER=$(gh release view --repo silgrid/polyforge --json tagName -q .tagName)
gh release download "$VER" --repo silgrid/polyforge --pattern 'aihub-darwin-*' --pattern 'pf-darwin-*' \
  --dir /tmp/pf-quickstart/bin
chmod +x /tmp/pf-quickstart/bin/*
xattr -c /tmp/pf-quickstart/bin/*
codesign -f -s - /tmp/pf-quickstart/bin/*
mkdir -p /tmp/pf-quickstart/aihub-data
```

The `xattr -c` / `codesign -f -s -` pair clears the quarantine attribute and
ad-hoc signs each downloaded binary, so macOS does not kill it on first run
(`Killed: 9`).

**2. Start the local aihub and keep it alive.** The keep-alive is a HAND-RUN
tmux restart loop — you start it, and after a reboot you start it again
by hand; nothing in pf starts, restarts or supervises aihub:

```
tmux new-session -d -s aihub 'while :; do \
  /tmp/pf-quickstart/bin/aihub-darwin-arm64 --data-dir /tmp/pf-quickstart/aihub-data \
  --listen 127.0.0.1:8080 >> /tmp/pf-quickstart/aihub.log 2>&1; sleep 5; done'
```

On first start over an empty data dir, aihub seeds a bootstrap admin
(`usr_root`) and writes `<data-dir>/bootstrap-admin.key` (mode 0600; the
log names the file, never the key). Copy the key, then DELETE the file —
the key is not stored anywhere else:

```
cat /tmp/pf-quickstart/aihub-data/bootstrap-admin.key   # copy it, then delete the file
rm /tmp/pf-quickstart/aihub-data/bootstrap-admin.key
```

**3. Create the project and a machine identity** with the downloaded pf,
pointed at the local aihub with the bootstrap key (every connection entry
is also a per-command flag). The machine user's API key is the daemon's
credential; its plaintext is shown exactly once. The last command grants
the machine user the maintainer role on demo — aihub's first-heartbeat
machine registration admits only an admin or a project maintainer, so
without the grant the daemon's heartbeat can never register this machine:

```
PF=/tmp/pf-quickstart/bin/pf-darwin-arm64
$PF --server http://127.0.0.1:8080 --api-key "<BOOTSTRAP_KEY>" projects create --name demo
$PF --server http://127.0.0.1:8080 --api-key "<BOOTSTRAP_KEY>" users create --display-name smoke-machine --user-type machine
$PF --server http://127.0.0.1:8080 --api-key "<BOOTSTRAP_KEY>" api-keys create --user <MACHINE_USER_ID>
$PF --server http://127.0.0.1:8080 --api-key "<BOOTSTRAP_KEY>" projects members add demo --user <MACHINE_USER_ID> --role maintainer
```

**4. Install pf the ordinary way, against your own local aihub** — the
key argument is the MACHINE key just created, the host argument
`127.0.0.1` makes the config point at this Mac's aihub (single-machine
form):

```
curl -s -H "Authorization: token $(gh auth token)" \
  https://raw.githubusercontent.com/silgrid/polyforge/main/tools/install.sh \
  | bash -s -- <MACHINE_KEY> 127.0.0.1
```

**5. Add the `[workspace]` mapping to your config** — the daemon refuses
to start without one (polyforge#113): the installer creates the config
at `~/.config/polyforge/cli.toml` but the mapping below is what makes
`pf daemon ensure` startable; the mapped directory must pre-exist:

```
mkdir -p "$HOME/polyforge-workspace"
cat >> ~/.config/polyforge/cli.toml <<EOF

[workspace."$HOME/polyforge-workspace"]
projects = ["demo"]
EOF
```

**6. Start the daemon** — the tmux session `polyforge-daemon` supervises
it (the same single supervision path as on Linux):

```
pf daemon ensure      # starts the daemon; tmux session polyforge-daemon supervises it
```

The daemon's first heartbeat registers this machine — because its key's
user is a maintainer of demo (the grant of step 3): machine registration
requires an admin or a project maintainer, and a registration failure
shows in `~/.local/share/polyforge/daemon.log`. Then continue with the
from-zero smoke run above from step **5. File a minimal smoke work item** —
drop the `bin/` prefix (pf is installed) and the `--config` flags (your
config lives at `~/.config/polyforge/cli.toml`; same verbs). File →
submit → watch → wrap; `pf doctor` at any point.

## Architecture

Current-system reference: `docs/design/polyforge-vnext/reference.md` (one
file: components, WI state machine, session fencing, aihub and daemon
routes, error codes, bundle keys, candidates, daemon lifecycle). Decision
authority: `docs/design/polyforge-vnext/v2-converge.md` (with
`wi-kind-contract.md`, `01-workflow-skills.md` for bundle authoring,
`06-memory-search.md` and `08-capsule-base-migration.md`). The v0/v1 design
set (00, 02–05, 07, the v1/v1.5 specs) is under
`docs/design/polyforge-vnext/archive/` — history only; do not build from it.
The subsections below describe the code as it actually exists in this tree —
the live code is the authority; design-doc references are for traceability.

### Modules

One row per sub-package, exactly as `internal/` exists today:

| Package | Role |
| --- | --- |
| `internal/common/api` | Shared business contract between aihub and pf: work items, projects, users, sessions, memories, and the typed error-code registry (`CORE-*`, `MEM-*`, `AUTH-*`, `DAEMON-*`, `GIT-*`). |
| `internal/common/atomicfile` | The one crash-safe "replace this file" primitive every state writer uses. |
| `internal/common/capsule` | Execution-capsule contract shared by pf (runtime creates capsules, the driver writes `execution.json`, the observe lane commits them) and aihub (serves the matching bare repos; git-dir lives outside the capsule). |
| `internal/common/protocol` | The in-machine contracts: Runtime↔Observe messages and the pf CLI↔daemon UDS HTTP API surface. |
| `internal/common/harness` | The closed step-harness set (`pi` \| `claude`) and the one model-candidate format `<harness>:<model>`. |
| `internal/common/spawn` | Process-derivation contract shared by the runtime (daemon assembly) and the driver (step-process spawning). |
| `internal/common/workflow` | The minimal workflow-bundle frontmatter read surface (defaults + per-step `model_candidates`) that drives model and harness selection, and the step-definition lookup: a bundle's own `steps/<id>.md` wins, else the shared `workflows/_shared/steps/<id>.md` (`_shared` is not a bundle; it is materialized beside the bundle). No digest contract: the bundle digest chain was deleted (polyforge#149). |
| `internal/aihub/core` | aihub persistent layer: SQLite store (single writer, WAL), the work-item state machine, claim/session arbitration (one live session per WI, its hashed push secret and public fingerprint), the ready queue and the admin face. |
| `internal/aihub/git` | aihub git surface: per-WI bare-repo lifecycle, the git-http-backend smart-protocol proxy, session-secret auth (only the WI's live session secret pushes) and the pre-receive hook enforcing the capsule-chain invariants. |
| `internal/aihub/memory` | Core-side memory store: memories kept in the database (`memories.content`), FTS5-trigram full-text search, and the one-time content migration from the former `memory.md` files. |
| `internal/aihub/server` | The AIHub HTTP face: business endpoints, the admin face, and the `/git/{wi}.git/...` proxy mount; Bearer API key auth, typed `api.Error` envelope. |
| `internal/polyforge/aihubclient` | Shared HTTP client for the aihub server, used by runtime, observe and the CLI verbs; decodes the typed error envelope back into `*api.Error`. |
| `internal/polyforge/cli` | The `pf` client CLI: the routing verbs (file, queue, cancel, recall, remember, memory, submit, `watch`, answer, pause, resume, wrap, ops), `chat`, the management face (users, api-keys, projects, machines, backup), the daemon client over the UDS socket, and config resolution. |
| `internal/polyforge/daemon` | Assembly of the local work machine: the UDS HTTP face, runtime, observe lane and driver wired together, the auto switch, the pending-job dispatcher, the auto-pull loop, the driver start (a running `Driver.Run` is the job's agent handle) and the shutdown. |
| `internal/polyforge/gitops` | Shared capsule-git operations layer used by Runtime (clone at start, dirty-commit at recover) and Observe (async commit+push); owns the per-WI git mutex. |
| `internal/polyforge/mapping` | The workspace→projects plan read from cli.toml's `[workspace]` sections, and its validated inverse (project → workspace). |
| `internal/polyforge/sessionjsonl` | The single parser of the harness session-jsonl format (live transcript tail extraction). |
| `internal/polyforge/supervisor` | The daemon's own supervision on a work machine: the `polyforge-daemon` tmux restart loop shared by `pf daemon ensure` and `pf doctor`. |
| `internal/polyforge/supervise` | The driver↔supervisor contract: facts, verdicts and the per-WI link (v2-converge §2.2–2.5). |
| `internal/polyforge/workspace` | The `.polyforge.yaml` loader (`steps.<project>.<default\|step-id>.models`, `summarize.models`, `summarize.language`, `summarize.interval_sec`), read and validated by the daemon before every claim and by `pf doctor` (an invalid file refuses the claim without claiming). |
| `internal/polyforge/observe` | Observe lane: the per-work-item events files, per-WI monitor goroutines fed by the runtime's lifecycle queue, the capsule push loop, live-transcript streaming, session revalidation, and the live activity / progress views the daemon reads. |
| `internal/polyforge/driver` | The Go execution driver: owns `runtime-state/execution.json`, spawns one step process per workflow step and validates each result envelope (needs_input rules; the human gate — a `requires_human_answer: true` step can neither complete nor gate-fail without a human answer in the current attempt, such an envelope is rejected; the command-evidence gate). |
| `internal/polyforge/watcher` | The `pf watch` engine: decision classification (fixed-template wake-up lines) and the resident per-chat watcher with its progress file. |
| `internal/polyforge/runtime` | Runtime lane: the WI lifecycle manager (one actor per work item: Start/Stop/Recover/Restart), the pi-harness agent spawner, the layered (L1–L5) process cleanup and the recovery engine. |

Binaries: `cmd/aihub` (server binary) and `cmd/pf` (work-machine binary:
CLI verbs + `pf daemon`).

### Daemon UDS routes

The routes mounted by the daemon's local Unix-socket HTTP face
(`internal/polyforge/daemon/routes.go`). **Face** marks who calls the route:
`user` = the operator's CLI / watch tooling; `step` = a workflow step's own
process, identified by its process tree (every other caller is refused). Every
other mutating (non-GET) route refuses a caller that belongs to a step agent's
process tree, or whose UDS peer credential is unavailable, with `DAEMON-403`
(polyforge#122 accident guard).

| Method | Path | Role | Face |
| --- | --- | --- | --- |
| POST | `/jobs/{wi}/answer` | Answer a pending agent question. | user |
| POST | `/jobs/{wi}/claim` | Start a queued work item: enqueue its claim (`pf submit`; optional body `models`: the manual candidate list of `--models`), or with `{"resume":true}` claim and start it now — the daemon half of `pf resume` on a blocked WI (the CLI lands aihub's resume-blocked door with the human's own key first). | user |
| POST | `/jobs/{wi}/resume-paused` | The live-holder half of `pf resume` on a paused WI (the CLI clears `clean_pause` at aihub first; this makes the online holder re-attach). | user |
| POST | `/jobs/{wi}/pause` | CLI close path: seal the job with reason `paused`. | user |
| POST | `/jobs/{wi}/wrap` | CLI close path: seal the job with reason `wrap` or `fail`. | user |
| GET | `/status` | Machine status: jobs (a pending question read from the capsule's `execution.json`), queue stats, auto switch and cap. | user |
| POST | `/ops/config` | `pf ops auto on\|off` / `pf ops max N`: body `{"auto":bool,"max":N}` (either or both). | user |
| POST | `/shutdown` | Shut the daemon down: stop the agents, save each job's task-repo WIP, exit (≤30s). | user |
| POST | `/chat/init` | `pf chat init`: the chat id of a harness session (created on first use). | user |
| GET | `/jobs/{wi}/watch` | `pf watch <wi> --once`: per-WI snapshot (status, step, pending question, events). | user |
| GET | `/chats/{id}/subscriptions` | A chat's daemon-held subscriptions. | user |
| POST | `/chats/{id}/subscribe`, `/unsubscribe` | Subscribe / drop a work item for a chat. | user |
| POST | `/chats/{id}/ack` | Acknowledge consumed events / delivered decisions (advances the cursor). | user |
| POST | `/step/result` | `pf result submit` inside a step: the daemon stamps the calling step's `wi_id`/`step_id`/`spawn_n` and lands its result envelope atomically at `runtime-state/pending-results/<spawn_n>.json`; a disagreeing identity or a shape defect is refused and nothing is written (polyforge#214). | step |
| POST | `/chats/{id}/subscribe-owned` | Every watcher poll: subscribe the chat to the held work items it submitted or its owner owns (a work item submitted from another chat is shown as progress only; only its origin chat — or, while that chat's watcher is silent, one fallback chat — is woken), and mark the chat live. | user |

Authoritative source for this table is the code itself — the
`mux.Handle`/`mux.HandleFunc` mount list in `internal/polyforge/daemon/routes.go`;
the same table, with the step-origin refusal and socket resolution, is in
`docs/design/polyforge-vnext/reference.md` §6.1.

### Failure codes of the recovery / re-claim paths

| Code | Meaning | Operator move |
| --- | --- | --- |
| `DAEMON-516` | Re-claim clone refusal: the capsule clone failed for a WI with published progress (or its progress cannot be determined), so a local init — which would restart the workflow at step 1 — is refused. The claim is abandoned back to blocked. | Fix aihub reachability / credentials, then `pf resume <id>`. |
| `DAEMON-518` | A blocked WI's aihub capsule head is not the sealed head, and the claiming daemon is not the sealing one or (on the sealing machine) no quarantined scene holds the sealed head. | Continue it on the sealing machine (it continues from the sealed head), abandon with `pf cancel <id>`, or — when the sealed work is gone — `pf resume <id> --accept-lost-work` (clears the seal; recorded as a `blocked_seal_abandoned` event). |

A missing capsule is no longer a startup failure: only the affected WI is set to
the ordinary `blocked` status at restart (the reason note names the cause —
"capsule missing at <path>" or "project <p> is not in the daemon's [workspace]
mapping"; a `capsule_missing` / `project_unmapped` event shows in `pf watch`);
nothing is parked locally, the daemon starts and every other WI recovers. Restore
the mapping if that was the cause, then `pf resume <id>`: a fresh re-claim from
the aihub capsule chain plus the task-repo carrier refs (`refs/pf-task/<name>`);
the dead scene is quarantined, completed steps are not re-run, and work published
after the last push is lost. A blocked WI has exactly two exits: `pf resume <id>`
(continue) or `pf cancel <id>` (abandon).

### Release flow

The platform ships with zero release-specific code in its engine: a release is a
work item filed against the project-agnostic `release` workflow bundle
(`workflows/release/`), and the delivery lanes (`feature-full`, `bug-fix`,
`autopilot`, `chore`) never release or deploy. Trigger it by filing a release WI
(`pf file --bundle release ...`, or ask the routing skill: "release" / "deploy" /
"发版" files that WI). If the goal names a bump (`major`, `minor`, `patch`) it is
passed verbatim to the repo's procedure; otherwise the repo default applies.

The bundle runs three gate steps — `release` (`.polyforge/release.sh [bump]`),
an independent `verify` (`release.sh --verify <version>`; only a pass writes the
version `deploy` reads) and `deploy` (`.polyforge/deploy.sh <version> --confirm
<version>`; a project without `deploy.sh` releases without deploying). A failure
terminates the WI: published tags, releases and images are immutable, so there
is no rewind into a re-release. The step-by-step checks and failure codes are in
[workflows/release/workflow.md](workflows/release/workflow.md) and its steps.

A failed `deploy` ends the WI failed with production on the old version (rolled
back, or refused before touching it). Redeploy the already-verified version by
running `.polyforge/deploy.sh <version> --confirm <version>` directly (a human or
an agent); never by filing a new release WI, which would cut a duplicate version
of the same commit (this repo's `release.sh` refuses that).

Per-repo procedure contract (the repo owns the procedure; the bundle only
dispatches):

- `.polyforge/release.sh [bump]` releases origin's default-branch head, exits
  non-zero on any failure, and prints `version=<tag>`, `commit=<sha>`,
  `notes=<location>` lines at the end.
- `.polyforge/release.sh --verify <version>` is a read-only audit of a published
  release (format, ordering, artifacts, integrity); exit 0 only if all pass.
- `.polyforge/deploy.sh <version> --confirm <version>` deploys exactly that
  version; production ends on it or rolled back, rollback exits non-zero.

This repository's own procedures are the authority for everything they do —
the version scheme, the PR-only check, the test gate, the artifacts, the
deploy lock, backups, the upgrade-SQL switch window, automatic rollback and the
machine preconditions: read the header comments of
[.polyforge/release.sh](.polyforge/release.sh) and
[.polyforge/deploy.sh](.polyforge/deploy.sh). In short:
`release.sh` (vX.Y.Z, default bump `minor`) publishes the 8-asset matrix —
`aihub-linux-amd64`, `pf-linux-amd64`, `aihub-darwin-arm64`,
`pf-darwin-arm64`, `aihub-darwin-amd64`, `pf-darwin-amd64`,
`aihub-image.digest`, `SHA256SUMS` (covering all seven files) — and
`ghcr.io/silgrid/aihub:<tag>` (the image stays linux/amd64-only);
`deploy.sh` promotes that image onto the aihub
service host with seconds of downtime (no startup auto-migration, see
[docs/upgrade.md](docs/upgrade.md)). Deploying a work machine is re-running
`tools/install.sh`.

```
 RELEASE: a WI on the `release` bundle (no platform release code)
 +-----------------------------------------------------------+
 | release (.polyforge/release.sh) -> verify (fresh judge,   |
 | release.sh --verify) -> deploy (.polyforge/deploy.sh)     |
 +----------------------------+----------------------------+
                              |
                              v
               mono-repo git tag (single semver per tag)
                              |
                              v
     GitHub release: aihub/pf linux-amd64 + darwin-arm64 +
     darwin-amd64, aihub-image.digest, SHA256SUMS
                              |  +  ghcr.io image <tag> (linux/amd64)
                              v
 DEPLOYMENT: `deploy.sh` for the aihub host; re-run `tools/install.sh` on work machines
 +-----------------------------------------------------------+
 |  fetch+verify -> refuse if jobs run -> stop -> swap -> ensure    |
 +-----------------------------------------------------------+
```

## Build & test

    make check     # build + vet + fmt + xrefs + test, all packages
    make test      # unit packages, then the e2e suite in parallel
    make xrefs     # design-doc cross-references only
    go build ./...
    go test ./... -timeout 20m  # the same, e2e suite serial (over go's 10m default)

`make test` runs the `./test` e2e package through `test/e2e-parallel.sh`:
one process per test, `E2E_JOBS` at a time (default: the CPU count, at most
8), slowest first; `E2E_RUN=<regexp>` narrows it to matching tests.

`make xrefs` checks the cross-references and section anchors of
`docs/design/polyforge-vnext/` (archived docs excluded); it skips with a
message when python3 is missing. `.polyforge/release.sh` runs the same check
before its test gate and fails the release instead of skipping, so run
`make xrefs` whenever a change touches the design docs.

Opt-in tests (skipped with a clear message unless their variable is set):
`POLYFORGE_TEST_SYSTEM_INSTALL=1` runs
the installer scenarios, which write `/usr/local/bin/pf` (disposable hosts only).

## `pf` commands

`pf [--json] [--config PATH] [--server URL] [--api-key KEY] [--socket PATH]
[--user ID] <verb> ...` (or `pf --version`) (global flags before the verb; env `PF_SERVER`,
`PF_API_KEY`, `PF_DAEMON_SOCKET`, `PF_USER_ID`; `PF_CHAT_ID` is the
notification label a harness adapter sets for the chat). `pf help` prints the
registry; `pf help <verb>` one verb's usage line; `pf help extensions` the
one-line index of the [extension points](#extending). Exit codes: 0 success; 1
client-class (4xx, transport, local validation, and a sub-action's
flag/argument mistake); 2 server-class (5xx), an unknown verb, or a flag
error caught by the global / verb-level flag parse. The routing skill's fragments
(`skills/polyforge/verbs/`) carry each verb's success and failure shapes.

| `pf help` group | Verbs |
| --- | --- |
| Data-plane verbs (direct to aihub) | `file`, `queue`, `cancel`, `recall`, `remember`, `memory` |
| Execution-plane verbs (local daemon UDS face) | `submit`, `watch` (snapshot, resident watcher, `subscribe`/`unsubscribe`/`list`), `answer`, `pause`, `resume`, `wrap`, `ops` (`status`, `auto`, `max`, `shutdown`, and `end-session` — aihub-direct, admin/maintainer), `chat init`, `result submit` (inside a workflow step only: its one result-delivery path) |
| Management face (M1–M5) | `users`, `api-keys`, `projects`, `machines`, `backup` (database snapshot, see below) |
| Diagnostics | `doctor` (`--prune-scratch` removes stale `scratch/` entries), `version` (the verb form of `pf --version`), `init` (a workspace's local layout) |
| Process | `daemon` (run the work machine; `pf daemon ensure` starts it) |

`pf backup` writes a consistent snapshot of the database (`VACUUM INTO`) under
the server's data dir `backups/`, on the same disk. Memories live entirely in
the database, so the snapshot holds every memory; it does NOT include the git
repos (`git/`). For a full backup take the sqlite backup-API snapshot of
`aihub.db` (not a plain file copy of a live WAL database) and rsync `git/`
alongside it, as `deploy.sh` does. There is no backup-only mode: `deploy.sh`'s
online backup (snapshot, rsync catch-up, consistency check, off-host copy) runs
only as part of a deploy, so an ad-hoc full backup is these manual steps (or a
copy of the whole data dir while aihub is stopped).

Human moves: continue = `pf resume <id>` (a paused or blocked
WI), abandon = `pf cancel <id>` (queued, paused or blocked), owner's failed verdict on a live run = `pf wrap <id> --fail` (refused on a blocked WI), done for a work item with no bundle (design, decision, tracking) = `pf wrap <id> [--fail] [--note …]` (aihub records it; no daemon needed). These run with the
human's own api key: aihub's door checks the WI's reporter, a project
maintainer or an admin.

## `pf daemon` flags

`pf daemon` runs the local work machine (runtime, driver and supervisor
behind the UDS face).

Exit status 78 means the daemon refused to start for a reason only an
operator can fix — configuration, the recorded daemon id, the workspace
mapping or a workspace directory, unreadable on-disk state, or a failed
restart reconciliation. The `pf daemon ensure` supervision loop then STOPS
(it logs `the daemon refused to start (exit 78 …); the loop ends`) instead of
restarting into the same failure every 5s; any other non-zero exit is still
restarted after 5s. To recover: read `<data-dir>/daemon.log`, fix the cause,
then run `pf daemon ensure` (the next harness session start also runs it).

Git on the work machine (repo review 2026-10-16 V-5). The daemon's git calls
that talk to aihub with the session credential — the capsule chain push, the
capsule clone and the carrier ls-remote/fetch/push — run with no system or
global git config (`GIT_CONFIG_NOSYSTEM=1`, `GIT_CONFIG_GLOBAL=/dev/null`) and
with every inherited `GIT_*` variable stripped. So `http.proxy`,
`http.sslCAInfo`, `url.*.insteadOf` and `credential.helper` from
`~/.gitconfig` do NOT apply to them: give the daemon a proxy through
`https_proxy` / `HTTPS_PROXY` in its environment, and since `GIT_SSL_CAINFO`
is stripped too, an aihub behind an internal CA needs that CA in the system
trust store. The capsule commit itself still reads the global config, so
`user.name` / `user.email` must be set for the daemon user; task-repo git
(inside `repos/<name>`) also still uses the global config.

The daemon's environment is an explicit allow-list (defined once, in
`internal/polyforge/supervisor/env.go`): `pf daemon ensure` launches it
with only `PATH`, `HOME`, `USER`, `LOGNAME`, `SHELL`, `LANG`, `LC_*`,
`TZ`, `TMPDIR`, the proxy variables (`http_proxy`, `https_proxy`,
`no_proxy`, `all_proxy`, upper and lower case), `SSL_CERT_FILE`,
`SSL_CERT_DIR`, the XDG base dirs, the Go toolchain variables below, `POLYFORGE_DATA_DIR`,
`GOCOVERDIR` and
the `POLYFORGE_SUPERVISOR` marker, with the values of the shell that ran
ensure (the tmux server's environment — whatever shell first started it —
never reaches the daemon: the loop runs `env -i <allow-list> sh -c
<loop>`). Nothing else gets through: no `ANTHROPIC_*`, no `PF_*`, no
stale API key. A proxy or CA the daemon needs must therefore be set where
ensure runs (the harness session, or the install.sh shell). `PF_SERVER`,
`PF_API_KEY`, `PF_USER_ID` and `PF_DAEMON_SOCKET` in that shell do steer
ensure's own config load (which socket it probes), but are not passed to
the daemon: it reads `cli.toml` (`--config`). The Go toolchain variables
(`GOPATH`, `GOROOT`, `GOPROXY`, `GOPRIVATE`, `GONOSUMDB`, `GONOPROXY`,
`GOFLAGS`, `GOTOOLCHAIN`, `GO111MODULE`, `GOCACHE`, `GOMODCACHE`) are on
the list, so Go steps keep their caches and module settings. Step agents
see only this environment: claude steps additionally drop every
inherited `ANTHROPIC_*` (their credential and endpoint come only from
`<harness-root>/claude-config/settings.json`); pi steps need their
provider keys in `auth.json` or as literals in `models.json` — a
`$NAME` there, or a provider `*_API_KEY`, that is not on the list never
reaches them.
`GET /status` reports the daemon's environment variable names (never
values), and `pf doctor`'s `daemon env` row warns when a running daemon
carries `ANTHROPIC_*` or another credential-looking name, or when the pi
step agent's `models.json` references a variable off the list — restart it
clean with `pf ops shutdown`, wait, `pf daemon ensure`.

The production knobs (the `cli.toml` `[daemon]` table, and the fixed flags
`pf daemon ensure` passes): see [Extending › Daemon knobs](#daemon-knobs).

Job cap: `pf ops max N` applies at once and lasts until the daemon
restarts; every start takes the configured cap (an explicit
`--max-concurrent-jobs`, else the `[daemon]` table, else 4). The cap is never
persisted; `pf ops max` and `pf ops status` show the one in force.

Every other flag is test/dev-only: nothing in production sets it. The
table lists the production flags; `pf help daemon` lists every flag,
rendered from the flag set the daemon parses. The only environment variable
the daemon reads besides the `PF_*` CLI ones is `POLYFORGE_DATA_DIR` (the
`--data-dir` default; ensure reads it too) — and an ensure-started daemon
sees no `PF_*` at all (its environment is the allow-list above). The former
`POLYFORGE_MACHINE_ID`, `POLYFORGE_DISPLAY_NAME`, `POLYFORGE_HARNESS_ROOT`,
`POLYFORGE_OPERATOR_AGENT_DIR` and `POLYFORGE_RECALL_TOPK` fallbacks are
gone; a value in the environment is ignored. Because harness state must
never move silently (polyforge#171), the daemon warns at boot when one of
the two harness variables is set to a value differing from what it
resolved (stderr, a `retired_env_ignored` event, `env_warnings` in
`GET /status`), and `pf doctor`'s `retired env` row warns too.

| Flag | Production source | Default | Controls |
| --- | --- | --- | --- |
| `--config PATH` | ensure | `~/.config/polyforge/cli.toml` | Shared CLI config file: the `PF_*` fallbacks, `claude_bin` and the `[workspace]` mapping. |
| `--socket PATH` | ensure (`daemon_socket` / `PF_DAEMON_SOCKET`) | `daemon.sock` in `$POLYFORGE_DATA_DIR`, else `~/.local/share/polyforge` (`--data-dir` does not move it) | Unix socket the daemon's UDS HTTP face listens on. |
| `--server URL` | `server` / `PF_SERVER` | — | aihub base URL the daemon talks to. |
| `--api-key KEY` | `api_key` / `PF_API_KEY` | — | Machine aihub credential (never reaches an agent env). |
| `--machine-id ID` | ensure (recorded id, else `m-<short hostname>`) | `m-<short hostname>` | Machine identity claims and leases are recorded under; recorded in `<data-dir>/daemon-id` on first start, after which that file wins (a differing value refuses to start). |
| `--data-dir DIR` | ensure (`POLYFORGE_DATA_DIR`) | `~/.local/share/polyforge` | Daemon state, the per-work-item events files and the capsules' external git dirs. |
| `--workflows-dir DIR` | ensure | `./workflows` | Workflow-bundle checkout the in-process Go driver loads step definitions from (`pf daemon ensure` passes `<data-dir>/release/current/workflows`). |
| `--claude-bin PATH` | `claude_bin` | `claude` | Claude Code binary (tests/dev; production source cli.toml `claude_bin`) for steps whose harness is `claude` (run as `claude -p --bare` when the claude-config settings.json env block carries a non-empty `ANTHROPIC_AUTH_TOKEN`/`ANTHROPIC_API_KEY`, else `claude -p --safe-mode` for an OAuth login). The daemon never manages its credentials: the `<harness-root>/claude-config/settings.json` env block is the authority layer. |
| `--operator-agent-dir DIR` | `[daemon] operator_agent_dir` | `~/.pi/agent` | SOURCE of the dedicated step agent dir — a credential surface: the daemon copies the minimal set (`auth.json`, `models.json`, `agents/`, `extensions/subagent/`) from here into `<harness-root>/step-agent`, and every pi step runs against that copy — the operator's own packages and settings never reach a step. A configured source without a readable `auth.json` refuses the start. |
| `--harness-root DIR` | `[daemon] harness_root` | `<data-dir>` | Parent root of the step harness dirs: `<DIR>/step-agent` (dedicated agent dir), `/step-agent-sessions` (pi sessions) and `/claude-config` (claude config dir; its settings.json env block is the credential authority layer). The former `--harness-sessions-root` was renamed — old deployments fail loudly at startup. |
| `--max-concurrent-jobs N` | `[daemon] max_concurrent_jobs` | `4` | Configured cap on concurrently running jobs; `pf ops max N` overrides it until the daemon restarts (see above). |

The `[workspace]` mapping (the daemon's only workspace source): see
[Extending › Workspace mapping](#workspace-mapping).

Workspace layout (polyforge#177). Anything meant to be shared — designs,
reviews, decision sheets, audits, experience — lives in aihub, not in a
workspace file: a design in its work item's content while the work item
is open (`pf file edit <wi> --content @f`), else a `doc.design` memory
(`pf remember --project <p> --type doc.design --work-item <wi> @f`); reviews,
decisions and audits as `doc.review` / `doc.decision` / `doc.audit`
memories; experience as `experience.*` / `fact.*`. A workspace holds
locally only:

```
<workspace>/
  .polyforge.yaml   human-written model candidates
  CLAUDE.md         workspace notes (polyforge-managed block + the human part)
  go.work(.sum)     optional Go workspace tying the .repo/ clones together
  .repo/<repo>/     clones for humans and interactive agents
  capsules/<wi>/    work item workspaces (daemon-managed)
  .polyforge/       polyforge-generated files only — no documents (legacy v1 repo-map/, watchers/ are tolerated)
  scratch/          disposable output (logs, briefs, e2e output, downloads)
```

`pf init [<workspace-dir>]` creates `scratch/` and writes this convention
as a layout section into CLAUDE.md's `polyforge:managed` block (a new
block on top when there is none); re-running it rewrites only that
section. `pf doctor` warns (never fails, never moves anything) about root
entries outside the layout — dot-entries such as `.gitignore`, `.pi`,
`.claude` and `.mcp.json` are tolerated, `.polyforge.yaml.*` backups are
not — and about non-generated entries under `.polyforge/`, naming each
one's destination, and about a CLAUDE.md whose `polyforge:managed` block
lacks the layout section (run `pf init <ws>`; a workspace with no
CLAUDE.md is not flagged); it reports `scratch/` entries whose newest change
(mtime or ctime, of the entry and everything under it) is older than 14
days — a `scratch` that is a symlink is never scanned or pruned — and `pf doctor --prune-scratch`
removes them, listing each. Step agents are unaffected: they work inside
their capsule.

`.polyforge.yaml` (per-project model candidates and memory policy): see
[Extending › Model candidates](#model-candidates) and
[Extending › Memory policy](#memory-policy).

Supervision bounds (v2-converge 2.4) — Go duration strings (e.g. `700ms`,
`5m`), no environment fallbacks. Omitting a flag, or passing `0`, keeps
the default listed here. In production only T and N are settable, through
the `cli.toml` `[daemon]` table ([Extending › Daemon knobs](#daemon-knobs)); the other
four are test/dev-only. The only per-step override in
`workflow.md` is `budget.wall_minutes` (an estimate: T becomes
`max(3 x wall_minutes, --step-timeout)`); X and Y are global only — the
former `silence_minutes` / `tool_minutes` keys were deleted (polyforge#147)
and an old bundle that still carries them has them silently ignored.

| Flag | Production source | Default | Controls |
| --- | --- | --- | --- |
| `--silence-window D` | test/dev-only | `10m` (token-level harness) / `30m` (message-level) | X: no real work signal after dispatch (`harness_silent`), or no model output while waiting for the model (`model_hung`). UI chatter never counts as work. |
| `--tool-window D` | test/dev-only | `30m` | Y: one tool running longer than this is `tool_hung`. |
| `--step-timeout D` | `[daemon] step_timeout` | `90m` | T: cumulative step runtime across all its dispatches (`step_timeout_long_task` / `step_timeout_slow_model`). |
| `--stall-window D` | `[daemon] stall_window` | `10m` | N: the driver between steps (not waiting on a human) for longer than this is `driver_stalled`; a negative value disables it. |
| `--model-error-grace D` | test/dev-only | `1m` | Grace after a turn ENDS with no result file: ended on a model error is `model_error` (switch candidate); ended normally is `ended_without_result` (same-model retry). |
| `--revalidate-every D` | test/dev-only | `30s` | M4 periodic session-revalidation cadence for held records — running jobs and retained paused records (a hub-side cancel of a paused WI releases the local record at the next tick); `0`/omitted keeps the 30s default. A negative value disables both, leaving a hub-side cancel of a paused WI stale until a daemon restart. |

Before starting ANY implementation or review lane: recall the v0 lessons
ledger (`docs/design/polyforge-vnext/v0-lessons-ledger.md`, now including
the v1 pilot findings F1–F7) and declare, item by item, how your change
covers the entries it touches — the ledger only prevents re-hits if it is
actually read first. After a fix lands, write its status back: append the
「已修」/Fixed annotation (with commit reference) to the ledger entry the
change fixes, so the ledger the next lane recalls is never stale.

Minimal external dependencies: pure-Go SQLite (modernc.org/sqlite), yaml.v3, goldmark, jsonschema, klauspost/compress, x/sys.

## Extending

Everything a user can configure or extend without changing polyforge's
code, one subsection each; each subsection is the one place its shape is
documented, and every other mention points here. `pf help extensions`
prints this list as a one-line-per-item index.

### Per-repo rules

A task repo's own `AGENTS.md` at its root (else its `CLAUDE.md`) carries
the rules that hold only for that repo. A step reads it through the shared
engineering principles (`workflows/_shared/principles.md`, rendered by a
step definition's `{{principles}}` — most shipped steps; `grep -L
'{{principles}}' workflows/*/steps/*.md workflows/_shared/steps/*.md` lists
the exceptions), which open with "first read each `repos/<repo>/AGENTS.md`
(else `CLAUDE.md`)": the repo's rules bind too and win any conflict with
the generic principles (the step notes the conflict in its artifact). Polyforge's own rules are in this repository's
[AGENTS.md](AGENTS.md). Keep project-specific rules there, never in
`workflows/` (which stays project-neutral).

### Area notes

`docs/agents/` in a task repo holds area notes: one short note per area of
the code (what it owns, its invariants, its traps), named after the area.
This is a CONVENTION, not a mechanism: spec and review work reads the
notes for the areas whose files it touches (how pi-web works today). No
polyforge code reads or enforces `docs/agents/` yet, and no shipped step
definition names it; a repo that wants its notes read says so in its
`AGENTS.md` ([Per-repo rules](#per-repo-rules)).

### Model candidates

`steps:` in `.polyforge.yaml`. This subsection is the one authority for
the workspace yaml's shape, candidate syntax and precedence; the routing
skill's fragments, `pf submit --models` and docs/upgrade.md point here. A
workspace root may carry a `.polyforge.yaml` with three optional sections,
`steps`, `summarize` ([Summarize models](#summarize-models)) and
`memory_policy` ([Memory policy](#memory-policy)) (strict: unknown keys at
any level, duplicate keys, type mismatches, empty model ids and a
per-step key that is no step id of the daemon's installed workflows, a
project the cli.toml mapping does not route to this workspace, and
duplicate model ids in one list are load errors; a step entry's only key is `models`; the pre-#169
top-level `models:` section and the old `projects:` / `workflow_defaults:`
shapes fail loudly; YAML merge keys (`<<`) are refused — share a list with
anchors, `models: &x [...]` / `{ models: *x }`):

```yaml
# Precedence: pf submit --models > steps.<project>.<step-id>.models > bundle step model_candidates > steps.<project>.default.models > bundle defaults
steps:
  polyforge:                                # a project of this workspace
    default: { models: ["pi:prov/model-a", "pi:prov/model-b"] }  # optional: steps without a list of their own
    fix: { models: ["claude:claude-opus-5-5", "pi:prov/model-c"] }  # optional: one step id (fix, code,
                                            # review, deliver, ...) — the step
                                            # with that id in every bundle
```

Every candidate — here, in a bundle's `model_candidates` and in
`pf submit --models` — is `<harness>:<model>[:<effort>]` with harness `pi` or
`claude` (polyforge#175; split on the first `:`, the prefix mandatory; no
prefix, an unknown prefix or an empty model refuses). A
chain may mix harnesses; a switch onto a candidate of another harness
starts a fresh session with the previous transcript as read-only
reference. A step with no list (or an empty one) runs pi's own default
model. The optional effort (polyforge#176) is the last segment only
when it is one of `off minimal low medium high xhigh max` —
`pi:ollama/llama3:8b` keeps `:8b` in the model id; pi gets it as
`--thinking <effort>` (all seven), claude as `--effort <effort>`
(`low`…`max`; `off`/`minimal` refuse); no effort, no flag.

For a step of the claimed project the list is, highest first: the work
item's manual `pf submit <id> --models pi:a,claude:b` list (every step; kept by this
daemon only — across its restarts and `pf resume` — and a re-claim on
another machine uses that machine's settings), else the
yaml list keyed by the step's id, else the workflow bundle's step-level candidates, else
the project's `default`, else the bundle's `defaults` (a
declared-but-empty yaml list governs: no switchable chain). A project not
listed, or an absent or comments-only file, keeps the bundle's. The
daemon reads and validates the file BEFORE the aihub claim: an invalid
file refuses the work item with DAEMON-414 (`pf submit` shows it; auto-pull
skips that project and logs once) and nothing is claimed, so the work item
stays queued for other machines. Repo lists and clone URLs always come from
the aihub project registry. `pf doctor` reports the same refusal (an
unknown per-step key and a leftover `models:` section included) before
any claim. An edit takes effect on the next claim with no daemon restart
(the file is read fresh before every claim). Upgrading from the pre-#169
shape: see [docs/upgrade.md](docs/upgrade.md).

### Summarize models

`summarize:` in `.polyforge.yaml`. The model candidates of the
demand-gated summarize lane — the one-line narration of a running
step's live transcript that a watched work item's snapshot carries
(polyforge#224). One list for the whole workspace: a claim of any of
its projects uses it (there is no per-project list; a
`steps.<project>.summarize` key is an unknown step id and refuses):

```yaml
summarize:
  models: ["pi:prov/flash-model", "claude:claude-sonnet-5-5"]  # tried in order on failure
  language: English   # optional; absent = 中文
  interval_sec: 120   # optional; absent = 60
```

`models` has the shape and strictness of a step entry in
[Model candidates](#model-candidates): each candidate is
`<harness>:<model>[:<effort>]`; duplicates, the retired `harness` key
and any key other than `models`, `language` and `interval_sec` refuse (DAEMON-414 before
the claim; `pf doctor` reports the same). A file may declare only
`summarize:`. No key (or no file) leaves the lane silently off — no
call, no error, no doctor row; a declared-empty list is off too.

`language` (polyforge#243) is the narration language: the prompt asks
the model to "Answer in <language>." with the value inserted verbatim,
and to keep code identifiers, file paths and commands as written.
Absent, the narration is in `中文`. The value is free text — no
allowlist, no language codes, no length cap — but must be a plain YAML
string that is non-empty and single-line after trimming surrounding
whitespace; a number, list, map or null, an empty or whitespace-only
value and a multi-line value all refuse (DAEMON-414; `pf doctor` the
same).

`interval_sec` (polyforge#250) is the minimum number of seconds between
two summarize call starts for one work item (the clock is per work
item and does not reset on a new step dispatch). Absent, it is 60. The
value must be a YAML integer from 10 to 3600, inclusive; anything else —
a quoted `"60"`, a float, a duration such as `1m`, a bool, null, a list
or a map, `0`, a negative or an out-of-range number — refuses with an
error naming the range `10-3600` (DAEMON-414; `pf doctor` the same). It
is never clamped, and `0` does not mean "default". The per-attempt cap
stays fixed at 20 calls per step dispatch, and no call starts while the
previous one is still running, whatever the interval.

`models` is mandatory in the entry: a `language` or an `interval_sec`
without `models` refuses like an empty entry. The models list, the
language and the interval are read with the rest of the file before
every claim and snapshotted into that claim's job record
(`summarize_models`, `summarize_language`, `summarize_interval_sec`): an
edit governs the next claim only.

### Memory policy

`memory_policy:` in `.polyforge.yaml`. What the
workflow's last step puts into the work item's one `experience.wrap`
summary memory (no other step writes memories): `record` lists what is
worth remembering, `skip` what never is. Shape — `default` and per-project entries, each with optional
`record` / `skip` lists of non-empty, unique strings (no other key):

```yaml
memory_policy:
  default:                       # every project; an undeclared list is the built-in default's
    record: [踩坑和根因（现象→原因→解法）, 所有者的裁决和约定]
    skip: [临时状态, 密钥和凭据的值]
  polyforge:                     # a project of this workspace: lists APPENDED to default's
    record: [daemon 上线前后的环境差异]
```

A missing section means the built-in default policy (the section `pf
init` appends: record 踩坑和根因 / 代码与 git 里看不出来的环境与外部系统事实 /
所有者的裁决和约定; skip 过程流水与 step 输出与完整 spec/plan / 代码与 git 能直接看出来的东西 /
临时状态 / 密钥和凭据的值) and never refuses a claim; a malformed section refuses
it like any other defect of the file (DAEMON-414). The daemon writes the
claimed project's effective policy to the capsule
(`runtime-state/memory-policy.json`) and renders it into the last step's
wrap duty (`workflows/_shared/memory-wrap.md`); every step's prompt asks
it to recall first (`workflows/_shared/memory.md`). `pf init <ws>` appends the default
section as text when the file has none (existing bytes kept exactly) and
`pf doctor` warns about a `.polyforge.yaml` without one (no file at all
passes on the built-in default). Step agents hold no aihub credential
path: inside a step `pf recall` / `pf memory show` read through the
daemon (scoped to the step's own project); `pf remember` sends the wrap
summary synchronously to the daemon (`POST /step/memories`), which
validates it (type `experience.wrap`, a title, ≤ 2 KB, no credential
value), attributes it to the step's own work item and project and stores
it with an idempotency key of the step dispatch (a retry returns the same
memory); a failure is reported and never fails the step. Every other
aihub verb, `pf doctor` and `pf daemon` are refused there (DAEMON-403).

### Workspace mapping

The workspace mapping (polyforge#113) replaces the retired workspace and
project launch surfaces: the `[workspace."<path>"]` sections of the
`--config` file are the ONLY workspace source. One section per
workspace, `projects = ["..."]` under each; `~` at the start of a key
expands to the home directory, keys must be absolute with no trailing
separator. Every mapped workspace directory must pre-exist; the daemon
creates and writes only `capsules/` inside each and refuses to start
when any is missing or unusable (or when the config carries zero
sections). Capsules route per claimed project through the inverse
mapping; the auto-pull covers all mapped projects. `pf doctor` checks
the mapping.

### Daemon knobs

Production starts the daemon only through `pf daemon ensure`, which
passes only the fixed launch flags (`--config`, `-machine-id`,
`-data-dir`, `-socket`, `-workflows-dir`). The five production knobs come
from the `[daemon]` table of the `--config` file, which the daemon reads
itself (decision 2026-10-16 G2). Precedence: an explicit command-line
flag (tests/dev) > the `[daemon]` table > the built-in default.

```toml
# ~/.config/polyforge/cli.toml — after the flat keys (every key below a
# [daemon] header belongs to the table)
[daemon]
step_timeout = "45m"                 # --step-timeout: Go duration, > 0
stall_window = "10m"                 # --stall-window: Go duration, non-zero; negative disables
harness_root = "/srv/polyforge/h"    # --harness-root: absolute path
operator_agent_dir = "/home/u/.pi/agent"  # --operator-agent-dir: absolute path
max_concurrent_jobs = 6              # --max-concurrent-jobs: unquoted integer >= 1
```

Every key is optional (absent = the daemon default). The file is
validated on load by every `pf` command, by ensure and by the daemon (the
same check): a bad value, an unknown key or a duplicate is refused naming
the file, the line and the key (`config …: line N: [daemon] step_timeout:
"soon" is not a Go duration`); ensure starts nothing and the daemon exits
78. Edit the table, then restart the daemon (`pf ops shutdown`, wait,
`pf daemon ensure`) for it to apply.

### Claude binary

`claude_bin` — a flat key of `cli.toml` (before any table header), e.g.
`claude_bin = "/opt/claude/bin/claude"` — names the Claude Code binary the
daemon runs for steps whose harness is `claude`. Precedence: the daemon's
`--claude-bin` flag (tests/dev) > `claude_bin` > `claude` on `PATH`. How
the binary is invoked and where its credentials live: the `--claude-bin`
row of the [`pf daemon` flags](#pf-daemon-flags) table. Restart the daemon
for a change to apply.

### Custom workflow bundles

A workflow bundle is a directory under the daemon's workflows dir:
`<bundle>/workflow.md` (frontmatter: the steps, their order, budgets and
candidates) plus `<bundle>/steps/<id>.md`, one step definition per step id;
a step with no file of its own falls back to the shared
`_shared/steps/<id>.md` beside the bundles (the bundle's own file always
wins; `internal/common/workflow`). A work item runs the bundle it was filed
with (`pf file create --bundle <name>`). The daemon loads the bundles from
its `--workflows-dir`, which `pf daemon ensure` sets to
`<data-dir>/release/current/workflows` — the installed release's copy of
this repository's `workflows/`. Each installed version is its own
directory, so a bundle added there by hand does not survive the next
install; ship a bundle by adding it under `workflows/` here and releasing.
Authoring detail (the frontmatter schema, step definitions, rewinds,
budgets): [docs/design/polyforge-vnext/01-workflow-skills.md](docs/design/polyforge-vnext/01-workflow-skills.md).

## `pf recall` — memory search

`pf help recall` prints the usage; the flags, the `--project` mapping check
and the failure codes are in [skills/polyforge/verbs/recall.md](skills/polyforge/verbs/recall.md).

## Contributing & filing work items

Change coverage is tiered by the change's **behavior face**, not by a
"e2e on every ticket" blanket rule (owner ruling 2026-10-02). The existing
e2e suite (`make test`, or `sh test/e2e-parallel.sh` for the e2e package
alone) is the regression net for every
change — each ticket's verification step runs it — and the three disciplines
below govern when **new** coverage must be added. This section is the authority (the original
Chinese record, `docs/design/polyforge-vnext/archive/v1.5-change-coverage-rules.md`,
is archived history).

**Discipline ① — changes observable behavior → file on an e2e-carrying
graph.** If a change alters observable behavior — a new endpoint, a
protocol/contract/semantics change, an ownership or fencing rule change, a
new external capability — the ticket must be filed with `bundle_name` set to
a graph that carries an e2e-author step (currently `feature-full`, whose
verification also runs the authored scenarios; giving another graph that
step is zero code). Pure refactors, pure docs,
and obviously non-behavior chores keep the existing graphs (the existing
suite + verification is enough). Quick test: ask "after this merges, what
difference would users/callers perceive that the existing scenarios can't
detect?" — if you can answer, new scenarios are required. Edge case: a small
ticket discovered mid-flight to touch the behavior face (at the grill/spec
stage) declares it in needs_input, and the owner decides between re-filing
on the e2e graph and human-authored scenarios.

**Discipline ② — bug fixes → the repro must land as a regression test.** The
bug-fix graph's `fix` step carries a **mandatory** `regression_test` field:
the repro must be pinned as a test that fails before the fix and passes
after, lands in-tree, is run by verification, and is watched by review.
Waivers must be explicit (`waived: <reason>`) and are review-visible and
challengeable. A bug fix without a test pinning it is an unfixed bug — the
next regression of the same bug is left to the naked eye.

**Discipline ③ — workflow-bundle / prompt-logic changes → real-WI observation
window before the next cutover-grade release.** Any merge touching
`workflows/*/steps/*.md` or
`workflows/*/workflow.md` requires a real-WI observation window and gate
readings before the next cutover-grade release: a clean rig, ≥10 real
tickets of mixed bundle types (including ≥1 exercising the changed
surface), owner-answered needs_input, and the readings (command-evidence gate,
revision chain, recovery/blocked counts, orphan counts) compared against the
previous window — no regression passes, regression freezes merges until
fixed. The window ticket is registered at merge; it blocks cutover, not
merge. The report is filed on the window ticket and readings never
rewrite history.

**Merge-review checklist**:

- [ ] Behavior face touched? → filed on an e2e-carrying graph, or new
  scenarios human-authored
- [ ] Bug fix merged? → `regression_test` field non-empty, or a waiver
  reason accepted by review
- [ ] Skill face triggered? → observation-window ticket registered (blocks
  cutover-grade release, not merge)

## Commercial licensing

Polyforge is licensed under the
[PolyForm Noncommercial License 1.0.0](LICENSE): personal use, secondary
development/modification and distribution are allowed for noncommercial
purposes.

In addition to that license, silgrid grants: personal and team internal use
is free, including within for-profit organizations for their own internal
purposes.

Commercial exploitation — selling Polyforge, offering it as a paid hosted
service, or embedding it in a commercial product — requires a separate
commercial license. Contact <wangxk1991@gmail.com>.
