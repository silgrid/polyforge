# Install — platforms, the one-liner, upgrades

The ops page of the work-machine install: the platform support
matrix, the one-line install and what an upgrade does. The two
Quickstarts — [developers](quickstart-developers.md),
[macOS](quickstart-macos.md) — walk a first run end to end; daily
use is the [user guide](user-guide.md). The terms this page uses
(machine, work machine, daemon, workspace) are defined on the
[concepts page](concepts.md).

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
carries an `api_key` re-runs the line with `"$PF_KEY"` alone.)

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
when-things-go-wrong moves — is [user-guide.md](user-guide.md),
the daily-use guide.

Upgrading a work machine is re-running the same one-liner; the
server-side schema history, release by release, is
[upgrade.md](upgrade.md).
