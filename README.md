# Polyforge

[![License: PolyForm Noncommercial 1.0.0](https://img.shields.io/badge/license-PolyForm%20Noncommercial%201.0.0-blue)](LICENSE)

Work-item orchestration platform: an aihub server (WI store, memory, git
revision chain) and a pf work-machine binary (CLI + daemon with the Go
execution driver).

## Documentation

- What a term means: [concepts and glossary](docs/concepts.md) — every
  term a newcomer meets, defined once
- Command shapes: [commands](docs/reference/commands.md) — `pf help`,
  `pf help <verb>`, the skill fragments
- Every config key: [config](docs/reference/config.md)
- The daemon's local routes: [UDS routes](docs/reference/uds-routes.md)
- Recovery failure codes: [failure codes](docs/reference/failure-codes.md)
- Releases, build and test: [release](docs/reference/release.md)
- The system, the modules, the design docs: [architecture](docs/architecture.md)
- Extending without changing code: [extending](docs/extending.md)
- Authoring a custom workflow bundle: [bundles](docs/bundles.md)
- Daily use: [user guide](docs/user-guide.md)

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
