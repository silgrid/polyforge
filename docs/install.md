# Install — platforms, the one-liner, upgrades

The ops page of the work-machine install: the platform support
matrix, the one-line install and what an upgrade does. The two
Quickstarts — [developers](quickstart-developers.md),
[macOS](quickstart-macos.md) — walk a first run end to end; daily
use is the [user guide](user-guide.md). The terms this page uses
(machine, work machine, daemon, workspace) are defined on the
[concepts page](concepts.md).

## Platform support (polyforge#218)

- **Linux x86_64 is the reference platform** — the aihub docker image (the
  linux `pf` binary, started as `pf aihub`) stays linux/amd64-only and production deploys through `deploy.sh` on Linux hosts.
- **macOS (Darwin) arm64 and amd64 are supported as work machines and as a
  single machine — hardware-verified**: releases
  ship the `pf` darwin binaries (the same binary runs a local server as
  `pf aihub`), the daemon is supervised by tmux on
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
API key (`pf_k1_…`), read it with echo off (so it never lands in your shell
history), then:

```
read -rs PF_KEY
curl -s -H "Authorization: token $(gh auth token)" \
  https://raw.githubusercontent.com/silgrid/polyforge/main/tools/install.sh \
  | bash -s -- "$PF_KEY" <server-host>
```

(requires `gh` authenticated — `gh` needs a login, even for a public repo,
and the one-liner reads `gh auth token`. `<server-host>` is the
aihub host the admin gives you, as the second argument or `PF_HOST=<host>`;
there is no built-in default. It is required for a first install — the run
that writes `~/.config/polyforge/cli.toml`, refused before anything changes
without it — and optional on an upgrade: a machine whose config already
carries an `api_key` re-runs the line with `"$PF_KEY"` alone. A program
that runs a downloaded copy of `install.sh` passes `-` instead of the key
and writes the key to the script's stdin, so no process's argv carries it;
an upgrade needs no key there. Pi Web's Settings › Polyforge page runs the
installer this way.)

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
bare one-liner above lands it; the single-machine form is the
[macOS Quickstart](quickstart-macos.md).

It installs the `pf` binary (downloaded from the GitHub Release with `gh release download` and checked against its `SHA256SUMS`), writes
`~/.config/polyforge/cli.toml` (never rewritten), and keeps its own checkout
of the release tag in `~/.local/share/polyforge/release/<tag>` (stable
symlink `release/current`, previous version kept). From that checkout it
wires every top-level release skill into each harness it finds,
independently, by REFERENCE (nothing copied, nothing cloned twice):
Claude Code (`~/.claude` or `claude` on PATH) gets one
`~/.claude/skills/<name>` symlink per skill under
`release/current/skills/` — today the `polyforge` routing skill and the
standalone `output-format` skill — plus a SessionStart hook registered once
in `~/.claude/settings.json`; pi (on PATH, or its agent dir present) gets
one local package entry naming `release/current` in its settings (the
release's `package.json` declares both skills under `pi.skills`), replacing
an older `git:...silgrid/polyforge.git@<tag>` entry.
A first install writes `cli.toml` through `pf config` with the flat
connection keys (`server`, `api_key`) and **no** `[workspace]` mapping —
the installer never knows or fabricates a workspace. Add the mapping with
`pf config set '[workspace."<path>"]' projects=a,b,c`; until one exists the
daemon refuses to start.
Changing a `[daemon]` knob is `pf config set <key> <value>`: the running
daemon applies the live knobs without a restart and reports the restart-only
keys as pending until `pf ops shutdown` then `pf daemon ensure`; a hand edit
needs `pf config reload`. The reload classes and the key list are in
[reference/config.md](reference/config.md).
It then reports
the harness checks of `pf doctor --harness`: whether Claude Code's
statusLine still runs the retired `pf watch` progress face (remove it by
hand — the installer never edits your statusLine), whether pi loads the
polyforge package, plus the install-layout and v1-leftover checks.
Then it starts the daemon through `pf daemon ensure` (once a `[workspace]`
mapping exists): a restart loop in tmux session `polyforge-daemon` runs it
with `-workflows-dir release/current/workflows` and revives it within ~5s
after a crash; after a container/machine restart (tmux does not survive
one) the next pi or Claude Code session runs the same ensure. An upgrade
first downloads, verifies and fetches everything, refuses (changing nothing)
while the daemon runs jobs — wait, `pf pause` them, or re-run with
`PF_INSTALL_FORCE=1` in front of `bash`; such a refusal, like the one for a
job list it cannot read, exits 3, so a calling program tells it from a
failure — then shuts the daemon down gracefully (the loop ends with that clean exit —
`pf ops shutdown` always stops for good, only failures are restarted — except
exit 78, below),
swaps the binary, then ensures again; meanwhile it holds the ensure lock,
so a harness session starting mid-upgrade starts nothing. A daemon already running with a different workflows
dir, or without its loop, is reported, never auto-restarted. Finally it
runs `pf doctor`. A new machine registers itself: when the key's user is a
member of any project (or a global admin), the daemon's first signed
heartbeat registers it. The installer prints a note naming the refusal
causes — AUTH-403 when the key's user is in no project (joining any project
qualifies) or when another operator already holds the machine id (start the
daemon with a different `--machine-id`), AUTH-401 for a signature or clock
problem ([failure-codes](reference/failure-codes.md)) — and `daemon.log`
shows which one occurred. Re-running the same command upgrades
and is idempotent. Daily use from a session is natural language via the
`polyforge` routing skill; from a terminal it's
`pf queue`, `pf submit`, `pf answer`, `pf ops status`, …
Daily-use detail — the flow, the core concepts, memory and the
when-things-go-wrong moves — is [user-guide.md](user-guide.md),
the daily-use guide.

Upgrading a work machine is re-running the same one-liner; the
server-side schema history, release by release, is
[upgrade.md](upgrade.md). The installer never downgrades: when the latest
release is older than the installed pf it refuses in step 1, before
anything changes, and says which pf the machine still runs. The one
accepted older target is the named version-line cutover — any `v0.2.x`
release of the unified product line over an installed `v2.20.x` pf (the
retired internal line), so a `v2.20.x` machine still crosses after
`v0.2.1` ships; `.polyforge/release.sh` cuts the first of them, `v0.2.0`,
from a `v2.20.x` floor. Every other downgrade (`v0.2.2` → `v0.2.0`,
`v2.20.22` → `v0.1.9`) stays refused. A suffixed release `vX.Y.Z-<name>.N`
is ordered by SemVer precedence: a pre-release sorts below its core release
and numeric identifiers compare as numbers, so `v0.2.0-<name>.9` →
`v0.2.0-<name>.10` → `v0.2.1-<name>.1` are upgrades, while
`v0.2.0-<name>.2` → `v0.2.0-<name>.1` and `v0.2.12` → `v0.2.0-<name>.1`
are refused downgrades. An installed pf whose version is not a release
(`dev`, unreadable, or none installed) has nothing to compare and installs
the latest release. The release that introduces one Host with many
Cells refuses a pre-Cell data dir until you migrate it once with the daemon
stopped — `pf migrate cell-layout`, then `--apply` (see the upgrade note
"One Host, many Cells: the per-Cell data layout").

## Optional: the web workbench (pi-web)

Polyforge's recommended web UI is Pi Web (npm package `@silgrid/pi-web`,
its own MIT-licensed package, developed in this repository under
[`pi-web/`](../pi-web/) and released with the same tag as `pf`), a local
browser UI for the pi coding agent with a Polyforge panel. It is optional:
the installer above installs `pf` only unless asked, and `pf` works from
any harness (or none) without it. The installer's last lines print the
same hint. To add it on a work machine, either let the installer install
the Pi Web package of the same release — `--with-pi-web` before the key:

```
curl ... | bash -s -- --with-pi-web "$PF_KEY" [server-host]
```

(step 1 downloads `silgrid-pi-web-<tag>.tgz` and verifies its
`SHA256SUMS` line, refusing with nothing changed on a missing asset or a
mismatch; after step 5 `npm install -g` installs it; npm is then a
prerequisite). npm can skip the package's install scripts and still exit 0
(e.g. npm 11 under `ignore-scripts` or an explicit `allowScripts` denial; at
default settings it runs unreviewed scripts with a notice),
so the installer then runs the install check the package ships: it confirms
the scripts' effects (patches applied, terminal prepared, node-pty starts a
pty, rebuilding node-pty with its scripts allowed when needed) and, when it
cannot, prints `ERROR: Pi Web install check failed at <step>: <reason>`. A
failing npm install or install check exits non-zero and leaves `pf` as
installed.

**The same version already installed.** When the installed
`@silgrid/pi-web` already has the version of the release's tarball (for
example a re-run of the same release), the installer does not run
`npm install -g`, so a running pi-web is not affected (npm would delete the
directory it serves, see below); it prints `Pi Web <version> is already
installed (<dir>); npm install -g skipped (--reinstall-pi-web reinstalls it)`
and still runs the install check, which replaces nothing. To reinstall it
anyway, e.g. to repair a broken install, add `--reinstall-pi-web` after
`--with-pi-web` (without `--with-pi-web` it is refused with nothing changed):

```
curl ... | bash -s -- --with-pi-web --reinstall-pi-web "$PF_KEY" [server-host]
```

A pi-web running over such a reinstall serves a deleted directory just as
after an upgrade, so it gets the `ACTION:` line below, or the restart with
`--restart-pi-web`.

**Upgrading over a running pi-web.** A pi-web that is already running keeps
serving the build it started with after an upgrade, and it must be restarted
to serve the new one: npm deletes the old package directory, so until the
restart its pages load blank and every `/_next/static/*` request answers 5xx
(its `/api/*` routes keep answering, which hides the fault from API checks).
`--with-pi-web` therefore checks the pi-web the deploy's restart loop runs
(tmux session `piweb`, or `PIWEB_TMUX_SESSION`, window `piweb`) against the
package it just installed, with the same served == installed check
`pi-web/deploy-piweb.sh` runs. When they differ it restarts nothing and prints
an `ACTION:` line naming the served and installed versions and the restart
command (`kill -TERM <pid>`; the loop starts the installed package within
seconds). To let the installer do the restart during the upgrade, add
`--restart-pi-web` after `--with-pi-web`:

```
curl ... | bash -s -- --with-pi-web --restart-pi-web "$PF_KEY" [server-host]
```

It sends that process SIGTERM and waits up to 60 s until the loop serves the
installed package (a timeout is an `ERROR:` and a non-zero exit). A restart
ends any agent turn in progress in that pi-web, so it is never the default:
use the switch only when no turn is running. The check judges the pi-web by
the directory its Next.js server runs in, which npm deletes on every install,
so a later `--with-pi-web` run also detects a pi-web left stale earlier (an
ignored `ACTION:` line, or a manual `npm i -g`): it prints the `ACTION:` line
again, or restarts it with `--restart-pi-web`. The version such a pi-web
serves cannot be read any more, so the line names it as `unknown`, served from
the deleted directory (`.../@silgrid/.pi-web-XXXX (deleted)`). Without a
running pi-web, or when it already serves the installed package, neither form
prints an `ACTION:` line or restarts anything; `--restart-pi-web` without
`--with-pi-web` is refused with nothing changed.

Or install it standalone — equivalent, the same check after the install:

```
npm install -g @silgrid/pi-web@latest
node "$(npm root -g)/@silgrid/pi-web/bin/pi-web-install-check.js"
```

To run it as a long-lived service in a container, the package ships
`pi-web/deploy-piweb.sh`; its deployment options are documented in the
[Pi Web README](../pi-web/README.md), and a pinned release is deployed with
`.polyforge/deploy.sh <version> --confirm <version> --component pi-web`
([release](reference/release.md)). The README's
[Web workbench](../README.md#web-workbench) section is the short version of
this page.

## Dependency-audit tools

A machine that runs the full verification bar outside a container (a
`verify` step at the full tier, in the daemon) needs the dependency-audit
gate's two tools on `PATH`, and
the gate fails closed without them — a missing tool is a red bar, never a
skip:

- **npm** — comes with Node.js (macOS: `brew install node`; Linux:
  `apt install nodejs npm`).
- **govulncheck**, pinned:
  `go install golang.org/x/vuln/cmd/govulncheck@v1.9.0` (needs Go 1.26 or
  newer), then put `$(go env GOPATH)/bin` on `PATH`; `govulncheck -version`
  names the scanner version and its database (`https://vuln.go.dev`).

The gate reads the public advisory data over the network (the npm registry's
advisory endpoint and vuln.go.dev), so an unreachable source is a red bar
too. `pf doctor`'s `audit tools` row reports each missing tool with this
install command; it only warns and never moves the doctor's exit code.
