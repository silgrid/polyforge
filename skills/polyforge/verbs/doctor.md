# doctor — the machine's single health face

CLI-meta verb: one command reports whether this machine can do its job —
the config, the `[workspace]` mapping (polyforge#113) and the three
connectivity faces (aihub server reach, local daemon socket, auth), whether
the daemon runs under supervision, whether the five setup tools
(`gh`, `curl`, `python3`, `tmux`, `git`) are on PATH, whether the step
scripts' interpreter is Python 3.9 or newer, whether `/dev/null`
is still the null character device, whether the
dependency-audit gate's tools (`npm`, `govulncheck`) are on PATH, plus the harness family: whether
Claude Code's statusLine still runs the retired progress face and whether pi
loads the polyforge package, whether each harness references the release
the way `tools/install.sh` sets it up, and whether v1 polyforge artifacts are
still installed — and whether each mapped workspace keeps the runtime-artifact
layout (polyforge#177), whether state of the retired chat subscriptions is
left on disk, and whether each installed harness's context file
carries the native-SVG diagram guidance block. It is a read face: nothing is
mutated, nothing is auto-fixed (remediation is manual, the check detail
names it) — the only writes are five explicit flags: `--prune-scratch`,
which removes stale `scratch/` entries and lists them, `--prune-capsules`,
which slims or removes the entries the `capsule retention` row calls
reclaimable or retired and lists them, `--prune-chat-state`, which deletes
the files the `retired chat state` row lists, and `--fix-harness-prompts`, which appends the
SVG diagram-guidance block to the existing context files the `harness diagram
guidance` row reports `missing` or `legacy` (see below), and `--fix-install-layout`,
which links the Claude Code polyforge skill the `claude install layout` row
reports missing or linked elsewhere (see below). The one remediation the agent runs itself is the
daemon-down case: `pf daemon ensure`, then `pf doctor` again.

## Triggers

- EN: is this machine healthy / something's wrong / why won't the daemon
  start / check the workspace mapping / diagnose this box / pre-upgrade
  check / is aihub reachable
- 中文: 体检 / 诊断 / 这台机器健康吗 / daemon 为什么起不来 / 检查映射 /
  升级前自检

## Command

```
pf doctor [--harness] [--prune-scratch] [--prune-capsules] [--prune-chat-state] [--fix-harness-prompts] [--fix-install-layout] [--json]
```

`--harness` runs only the harness family (statusline and pi package, install
layout, v1 leftovers, retired chat state — no config, no network);
`tools/install.sh` prints its rows. `--fix-harness-prompts` needs the full report (its row is
config-dependent): combined with `--harness` it is refused before any
check runs (exit 1). `--fix-install-layout` is refused with `--harness` the
same way (exit 1), and so is `--prune-chat-state`: the `--harness`
projection `tools/install.sh` prints is read-only.

Exit codes follow the repo convention: 0 every check passes, 1 any check
fails, 2 usage errors. `--json` emits one machine document:
`{"ok": <bool>, "checks": [{"name", "status": "pass"|"fail"|"skip"|"warn",
"detail"}]}`.

To judge workflow lanes alone — a workspace's lane set (the installed lanes
and the workspace's own user lanes under `.polyforge/workflows/<lane>/`), or
a lane someone is writing — run the contract's validator, the daemon's own
loader:

```
pf [--json] workflows validate [--workspace <dir>] [<lane-dir>…]
```

With no lane directory it judges the lane set of the workspace
(`--workspace`, else the mapped workspace holding the working directory);
every defect prints with its `WFC-*` code, file:line and YAML path
(docs/reference/workflow-contract.md); any defect exits 1. It is read-only.

## The checks

| Check | What it proves | On fail |
|---|---|---|
| `config` | the config loads (including the `[daemon]` table: every value validated) | the load error verbatim (an old-shape flat `project` key, a malformed mapping or a bad `[daemon]` value — `line N: [daemon] <key>: …` — names itself) plus migration guidance; every config-dependent check is then `skip` — never a faked green |
| `retired env` | no retired `POLYFORGE_HARNESS_ROOT` / `POLYFORGE_OPERATOR_AGENT_DIR` is in effect (this shell, or the daemon's `env_warnings`) | `warn` naming the variable and the `[daemon]` key (`harness_root` / `operator_agent_dir`) to set instead — the variable is no longer read, so harness state would move |
| `daemon env` | the running daemon's environment (GET /status `env_names`: names only, never values) carries no `ANTHROPIC_*` and no credential-looking name (`…TOKEN`, `…KEY`, `…SECRET`, `PASSWORD`) outside the `pf daemon ensure` allow-list, and the pi step agent's `models.json` interpolates (`$NAME`) no variable off that list; `skip` when the daemon is down or too old to report names | `warn` naming the variables: a polluted daemon environment (an older ensure under a tmux server carrying a human's credentials) — restart it clean: `pf ops shutdown`, wait until it is gone, `pf daemon ensure`; a `models.json` `$NAME` off the list — pi steps cannot see it: put the key in `auth.json` (pi `/login`) or as a literal in `models.json` |
| `daemon command path` | the PATH every step process runs under — its scripts, `verify.py`'s bar and its scope engine `workflows/_shared/scripts/verify-scope`, and its `pf step …` (GET /status `step_path`: with `[daemon] command_path` set, the daemon's own `pf` directory and then the key's value, each entry once — a step also gets its harness binary's directory between them; unset, the daemon process's own PATH) — shown in the detail, and `pf` and the engine's own tools resolved under it: `pf` and `git` always, `go` when a mapped workspace's `.repo/<name>` clone is a Go repo for the engine (a `stack=go` conf pin, or no pin and `go.mod` its one manifest: the engine runs `go list` there) verified on the host — a clone carrying `.polyforge/Dockerfile.verify` verifies in its container, whose image provides `go`, and the detail says so instead of asking for it; `warn` at most — never `fail`, never moves `ok` or the exit code; `skip` when the daemon is unreachable (its PATH is unknown); out of `--harness` | `warn` naming each tool that does not resolve and the PATH — a step's commands would exit 127 (its `pf step …` too) and the engine fall back to the full tier: set `[daemon] command_path` to a PATH that holds them (`pf config set command_path <dir:dir:…>`; the daemon puts its own `pf` directory in front; docs/reference/config.md), or start the daemon with them on its PATH — a toolchain only a shell profile puts on PATH is not on the daemon's |
| `workspace mapping` | at least one `[workspace."<path>"]` section exists | guidance to add the shape, plus the pointer to docs/extending.md (`pf help extensions`) (a zero-section config is the fresh-install template) |
| `workspace dirs` | every mapped workspace pre-exists as a directory | the missing path(s), named |
| `workspace yaml` | every mapped `.polyforge.yaml` loads, every candidate (yaml and installed bundles) is `<harness>:<model>`, every per-step key names a step of a lane of the workspace's lane set (the built-in lanes and its user lanes; see below), and no resolved step would run on a placeholder model (a first candidate `<harness>:example-…`, the shipped lanes' lane-level placeholders: a step-level or tier placeholder list is undeclared and never outranks the project default) | the refusal a claim would meet (DAEMON-414), file and key named, plus the pointer to docs/extending.md (`pf help extensions`); `warn` naming each stale per-step key — no step of a lane of the workspace's set, the retired `verify-judge` among them: it governs nothing — with the rewrite (a sub-definition's models are its dispatching step's: `verification` for the verify-judge) and the docs/upgrade.md section; `warn` naming each `<project>/<lane>/<step>` whose first resolved candidate is `example-…` with the level it came from — set `steps.<project>.default.models` (or the step's key) in the workspace yaml; `warn` naming a workspace whose user lane root is unreadable (its keys are not judged; the built-in lanes still run) |
| `workflow lanes` | the built-in lanes the daemon runs work items on (its workflows dir) and every mapped workspace's user lanes (`.polyforge/workflows/<lane>/`) load through the one lane loader under the workflow contract — the same judgement as `pf workflows validate` — and no queued work item of a mapped project is bound to a lane its workspace's lane set does not carry; `skip` while the daemon does not answer; out of `--harness` | `fail` naming every `WFC-*` defect of a built-in lane with its file:line and YAML path (a release defect; a claim on such a lane is refused); `warn` naming every defect of a user lane (`WFC-122` included: only that lane is unavailable) and listing each queued work item bound to a missing lane (`WFC-123`: a claim refuses it and it stays queued) — fix the user lane, re-bind the work item (`pf file edit <wi> --bundle <lane>`) or cancel it |
| `harness claude` | when some resolved candidate is `claude:…`: the daemon finds the claude binary (see below) | the binary the daemon could not resolve |
| `harness diagram guidance` | every installed harness's context file carries the native-SVG diagram guidance block (see below); `warn` at most — never `fail`, never moves the exit code; `skip` when no harness is installed | — (`warn` lists each file as `pass` / `legacy` / `missing` / `no file` / `unreadable` and carries the SVG block to add; `legacy` = mermaid-only guidance, which no longer passes) |
| `aihub projects` | every mapped project is known to aihub and readable with the api key (its ready-queue read — the project-member face the daemon uses; no admin role needed) | the project AND its workspace, named (an ambiguous inverse or duplicate surfaces through the config check, both workspaces named) |
| `workspace repo clones` | no uncovered uncommitted change or unpushed local commit in the `.repo/` git clones of every existing mapped workspace and their linked worktrees (see below); `warn` at most — never `fail`, never moves the exit code; `skip` naming the unit and its reason when the covering WI state could not be read | the clone AND its owning workspace, named, with the remediation (see below) |
| `light verification fallbacks` | reads (never writes) the latest verify run of each live — not `.quarantined.` — capsule of every mapped workspace (`capsules/<name>/runtime-state/verify/<n>/result.json`, highest `<n>`) and, when light verifications ran full, reports `the latest light verification of N capsule(s) ran full — reasons: <category> ×k, …` (the `tier.reasons` entries by category: go package graph, stack config file changed, carries, conf, no analyzer, analyzer could not resolve, git, unplanned, unexplained, other) — a report only: always `pass`, never moves the exit code; `skip` when no mapped workspace directory exists; out of `--harness` (it rode the scoped-gates readiness row until that check moved into the workflow, see below) | — (`pass` with the counts; the scoped-gates readiness of a repo whose light runs fall back: `scope_ready.py`, below) |
| `capsule retention` | reports, never fails: the external git dirs under `<data-dir>/git/` (`<wi>.git`, `<wi>.git.quarantined.<ts>`) and every mapped workspace's capsules, live (`capsules/<name>`) and set aside (`capsules/<name>.quarantined.<ts>`), counted as reclaimable (a live git dir or live capsule), retired (a set-aside dir), retained and unknown, with the size of the reclaimable and retired ones — what a prune frees. The row's own words call these renamed-aside dirs "set-aside scenes" (a Linux install's report carries no darwin Gatekeeper vocabulary; the on-disk names are unchanged). An entry is reclaimable or retired only when its work item's aihub state is `wrapped`, `failed` or `canceled` (a live capsule: `wrapped` only — a live capsule of any other work item is never touched), its newest change is older than the 14-day retention, the daemon's `GET /status` names no job for it, and — for a capsule, live or set aside — none of its `repos/*` task clones holds an uncommitted change or a commit neither the clone's own nor the mapped workspace's `.repo/<name>` clone's remote-tracking refs carry (a scene's own refs stop updating at quarantine time; the `.repo` clone keeps fetched). Such a capsule, whole, and the work item's git dirs, are `retained` and the detail names each kept path and why (an unreadable clone, or an unreadable `.repo` view, keeps it too — never a delete on an unproven clean); aihub unreachable, an unknown work item or an unreadable entry is `unknown` — counted, never deleted. The run reads only what a verdict needs, so its cost does not grow with the entries: a capsule already reduced to `artifacts/` + `capsule.json`, an entry whose own mtime is inside the retention, or one a daemon job names, is `retained` with no aihub read; the remaining work items are read with one list per mapped project, per id only for one no list names (and per id outright when they number no more than the mapped projects); and only an entry a prune may act on is walked, for its newest change and its size (a capsule to be slimmed counts only what the slim removes). The prune's act: a `wrapped` work item's capsule, live or set aside, is slimmed to `artifacts/` + `capsule.json` (`repos/`, `runtime-state/`, `logs/`, `session/`, `workflows/`, `sync/`, the `.git` pointer and every other top-level entry go; `pf watch` then answers from the hub record and the artifacts stay readable); every other reclaimable or retired entry — an external git dir, a `failed` or `canceled` work item's set-aside capsule — is deleted. The default run deletes nothing; `pf doctor --prune-capsules` slims or removes exactly the reclaimable and retired entries and lists each path, and prunes nothing while the daemon is down. `warn` at most — never `fail`, never moves the exit code; out of `--harness` | `warn` with the counts, the reclaimable and retired sizes and the `--prune-capsules` pointer; after a prune the detail lists every removed and every slimmed path and every capsule kept for unmerged work |
| `disk watermark` | for every existing mapped workspace, the filesystem holding it — used, total and the used percentage (`df`'s formula: used over used + available) — and the bytes under its `capsules/` (one walk that follows no symlink); a filesystem shared by several workspaces is reported once. Read-only. `warn` at most — never `fail`, never moves the exit code; `skip` when no mapped workspace directory exists | `warn` when a filesystem is used above 70% (free space before steps fail on a full disk: the `capsule retention` and `workspace scratch` rows name what `--prune-capsules` and `--prune-scratch` reclaim), or naming a workspace whose filesystem or `capsules/` entries could not be read — never a faked green |
| `model health` | the daemon's additive `GET /status` `model_health` summary over the events-retention window: each current warning (model+effort, step, the rule that fired and its numbers); `warn` at most — never `fail`, never moves `ok` or the exit code, and never blocks or reorders a dispatch; `skip` when the daemon is down (`pf daemon ensure`) or answers without the field (an older daemon — never a faked pass); out of `--harness` | the warning line names the model(s), step, rule and numbers with the advisory-only note and the pointer to `pf ops models`; nothing is auto-fixed |
| `prerequisites` | the five setup tools — `gh`, `curl`, `python3`, `tmux`, `git` — are each found on `PATH` (LookPath); `warn` at most — never `fail`, never moves the exit code — the doctor never installs anything; `skip` when the config does not load, like every config-dependent row; out of `--harness` | each missing tool named with its obtain hint (macOS: `brew install gh`, `brew install tmux`, `xcode-select --install`; Linux: `apt install …`) — install the named tools by hand; a missing `tmux` means the daemon runs unsupervised (see the daemon supervision row) |
| `python3` | the interpreter a workflow's step scripts run under (`PF_PYTHON`) resolves and is Python 3.9 or newer, by the rule the daemon applies at its start: `[daemon] python` when set (never replaced by the `PATH`'s), else `python3` on this shell's `PATH`, asked for its version (`python3 -I -c …`); the detail names the version, the path and the source; `skip` when the config does not load, like every config-dependent row; out of `--harness` | `fail` naming the source, the path and what it answered (not on `PATH`, no answer, or the version older than 3.9) — the daemon refuses to start on the same finding (exit 78): install Python 3.9 or newer, or point the daemon at one with `pf config set python <absolute path>` (docs/reference/config.md, `[daemon] python`) and restart it |
| `null device` | `/dev/null` is the platform's null character device — `c 1,3` on Linux, `c 3,2` on macOS (`lstat` and the device number; reads only); `skip` when the config does not load, like every config-dependent row; out of `--harness` | `fail` naming what the path is instead (a regular file and its size, another device and its numbers, or missing) with the atomic recovery command, run as root: `mknod -m 666 /dev/null.new c 1 3 && mv -f /dev/null.new /dev/null` on Linux (`c 3 2` on macOS) — a fresh node is created beside it and renamed over it. Why it matters: once `/dev/null` is a regular file, every `2>/dev/null` redirect writes text into it, and any tool that reads it as an empty file — git reading it as a config file — fails on that text; the daemon's own git calls no longer read it, but shells, tools and hooks on the machine still do |
| `audit tools` | the full-tier bar's dependency-audit gate tools — `npm` and `govulncheck` — are each found on `PATH` (LookPath); `warn` at most — never `fail`, never moves `ok` or the exit code — the doctor never installs anything (the gate itself fails closed on a missing tool, so this row is the advance notice); `skip` when the config does not load, like every config-dependent row; out of `--harness` | each missing tool named with its install command (`go install golang.org/x/vuln/cmd/govulncheck@v1.9.0`; Node.js with npm: `brew install node` / `apt install nodejs npm`) — install it by hand, see docs/install.md |
| `host prerequisites` | every host prerequisite a mapped repository declares for its verification rules — `host_requires=<name>\|<probe>\|<hint>` in the `.polyforge/verify-scope.conf` at the root of a mapped workspace's `.repo/<name>` clone, listed by the scope engine the daemon's lane set ships (`<workflows_dir>/_shared/scripts/verify-scope --json --base HEAD`, its `host_requires`; the doctor parses no conf and names no tool of its own) — is met on this host: each probe runs with `bash -c` from the clone's root, stdin closed, under the PATH a step runs under (GET /status `step_path`: the repository's fast gate runs there too), bounded by 30 s; `pass` shows each probe's first non-empty output line (its version), or says no mapped repository declares one; `warn` when the engine cannot list a clone's declarations (those prerequisites are unknown); `skip` when the daemon is unreachable or reports no `workflows_dir` or `step_path` (the engine and the PATH are then unknown, never guessed); the doctor never installs anything; out of `--harness` | `fail` naming, per unmet prerequisite, the repository (and its workspace), the prerequisite's name, the probe and how it ended (`exited <n>` with its first output line, or `timed out after 30s`) and the repository's install hint — install it by hand (the repository's fast gate fails on this host until then), then re-run the doctor; the met ones are listed after |
| `server reach` | aihub answers at all (any HTTP answer counts) | the dial failure |
| `daemon socket` | the local daemon answers GET /status over its unix socket | the socket path and the dial error — the daemon is down: run `pf daemon ensure` (starts it under its restart loop), then re-run the doctor |
| `auth` | the api key gets past aihub's auth boundary (the same signed read the projects check uses) | the typed 401/403 answer — fix `api_key` |

Checks that cannot be proven (config unloadable, aihub unreachable) are
reported `skip` — a skip never pretends to be a pass.

**Scoped-gates readiness is the workflow's own check** (it was a doctor
row until the verification engine moved into the workflow):
whether the engine `workflows/_shared/scripts/verify-scope` (and its
`audit-deps` beside it) is there and a repository's clone is scoped-ready —
its manifests and `.polyforge/verify-scope.conf`, read only, never
executed: a `stack=` pin or one supported manifest, else a conf declaring
a command (full tier only); for npm, python and rust the carries layer
(undeclared or malformed suites, `e2e_map=` and `smoke_suite=` naming no
discovered suite, an empty or malformed carries key). Run it on a clone:

```
python3 "${POLYFORGE_DATA_DIR:-$HOME/.local/share/polyforge}/release/current/workflows/_shared/scripts/scope_ready.py" --repo <workspace>/.repo/<repo>
```

Exit 0 ready; 1 not ready, each problem named with the fall-back-to-full
fact and the pointer to docs/contributing.md『Verification tiers』 — a repo
that is not scoped-ready gets no narrowed selection; 2 when no clone is
named or one is not a git clone (never faked green). `--help` is its
reference.


## Daemon supervision (polyforge#174 — `warn` never moves the exit code)

| Result | When | What to tell the user |
|---|---|---|
| `pass` | the daemon answers and tmux session `polyforge-daemon` (the restart loop `pf daemon ensure` creates) exists — or a systemd unit `polyforge-daemon.service` is active | — |
| `warn` | the daemon answers but nothing supervises it (e.g. started by hand or by an older installer with `nohup`): a crash is not restarted | Relay the fix from the detail when no job needs the daemon: `pf ops shutdown && while pf ops status >/dev/null 2>&1 || ! flock -n ~/.local/share/polyforge/daemon.lock true; do sleep 2; done; pf daemon ensure` (graceful — running jobs are checkpointed first). Without tmux installed: install it first. |
| `skip` | the daemon does not answer | The `daemon socket` row fails: run `pf daemon ensure`. |

The loop restarts crashes within ~5s; tmux does not survive a container or
machine restart — the next pi or Claude Code session start runs
`pf daemon ensure` and brings it back. A daemon that refused to start for an
operator-fixable reason (exit 78: config, daemon id, workspace, on-disk
state, failed restart reconciliation) ends the loop instead: `daemon socket`
fails and the cause is in `<data-dir>/daemon.log` — fix it, then
`pf daemon ensure`.

## Harness statusline and pi package (presentation only — never `fail`, never moves the exit code)

| Check | `pass` | `warn` (detail carries the fix) | `skip` |
|---|---|---|---|
| `claude statusline` | `~/.claude/settings.json`'s statusLine does not run `pf watch` with its retired `--statusline` face (or there is no statusLine, or no settings file) | the statusLine still runs that retired progress face, directly or in a script file it names — remove that command from it (human decisions and progress now live in Pi Web's Polyforge panel) | no `~/.claude` |
| `pi package` | pi settings list the polyforge package (or `pf-chat.ts` itself), so pi loads the skill and its session-start extension | the `pi install …` line; malformed settings → "cannot tell" | no pi agent dir (`$PI_CODING_AGENT_DIR` or `~/.pi/agent`) |

The judgement is static: the statusLine command is read, never run, and
no user file is ever written by these rows.

## Retired chat state (`warn` never moves the exit code)

| Result | When | What to tell the user |
|---|---|---|
| `pass` | none of the retired chat-subscription files is left | — |
| `warn` | any is left (the count, the bytes and the first five paths): `<data-dir>/chats.json`, `<data-dir>/chat-subs.json`, `<data-dir>/chat-status/`, `<data-dir>/migration/chat-subs.json.pre-migration`, and in the runtime dir (`$XDG_RUNTIME_DIR/polyforge`, else `$TMPDIR/polyforge-<uid>`) `chat-<id>.lock` / `.pid` / `.status` and `session-*.chat` | nothing reads them any more; run `pf doctor --prune-chat-state` to delete them |

`pf doctor --prune-chat-state` deletes exactly the files this row finds
and reports what it deleted; nothing else is touched. The row is part of
the `--harness` family (its check reads no config and no network).

## Install layout and v1 leftovers (presentation only — never `fail`)

| Check | `pass` | `warn` (detail carries the fix) | `skip` |
|---|---|---|---|
| `claude install layout` | `~/.claude/skills/polyforge` is a symlink to `<data-dir>/release/current/skills/polyforge` | a copied directory (the pre-#174 layout), a link elsewhere, no skill, or a link with no release behind it → re-run `tools/install.sh`; no skill or a link elsewhere, with a release skill behind the target, is also fixed by `pf doctor --fix-install-layout` (a copied directory and a missing release are not) | no `~/.claude` |
| `pi install layout` | pi's settings `packages` hold the local release `<data-dir>/release/current` | a `git:…silgrid/polyforge.git@<ver>` entry (the old clone), another polyforge checkout, no entry, or no release behind it → re-run `tools/install.sh` | no pi agent dir |
| `v1 harness leftovers` | no v1 artifact | any of: a pi `packages` entry whose source pins to a known v1 source (sha256 of its lower-cased owner/repo — the literal is not in the tree) or whose source names polyforge (other than the release `<data-dir>/release/current` entry `tools/install.sh` manages and the `silgrid/polyforge.git` v2 clone the `pi install layout` row already reports); a pi settings `extensions` entry that pins to a known v1 source or names the v1 bridge file `…/polyforge/index.(js|ts)` (the v2 `…/skills/polyforge/extensions/pf-chat.ts` entry is not a v1 leftover — the `pi package` row passes it); the v1 auto-loaded extension at `~/.pi/agent/extensions/polyforge`; a v1 Claude skill (`using-polyforge`, `pf-*`); a v1 Claude plugin named `polyforge` (`polyforge@…`, from any marketplace) — the detail lists each and one copy-paste command line that first backs everything up into `~/polyforge-v1-backup-<timestamp>/`, then removes them (`pi remove`, `mv`, `claude plugin uninstall`) | — |

`pf doctor --fix-install-layout` repairs the `claude install layout` row's
one entry, `~/.claude/skills/polyforge`, and nothing else under `~/.claude`
(settings, projects and other skills are never touched):

- no skill → it creates the link to `<data-dir>/release/current/skills/polyforge`
  (creating `~/.claude/skills` if needed), and the row reports `pass … (created
  by --fix-install-layout)`;
- a link elsewhere, dangling or not → it replaces only that link (a temp link
  renamed over it, so an interrupted run leaves the old or the new link), and
  the row reports `pass … (repaired …)`; the old target is not touched;
- the correct link → `pass`, a no-op: the link is not rewritten, and a second
  run changes nothing;
- no release skill behind the target, a copied directory (the pre-#174
  layout), or no `~/.claude` → it creates and removes nothing and the row keeps
  its `warn` (re-run `tools/install.sh`) or `skip`. It never installs `pf` or a
  release; a failed link is a `warn` naming the error.

The `v1 harness leftovers` check is migration hygiene and is removed at
v3.0.

v1 artifacts hook the same session events as the v2 adapters (pi loads
`~/.pi/agent/extensions/*` and package extensions side by side), so show
the user the command and let them run it; restart the harness sessions
afterwards. The doctor never deletes these itself.

## Workspace layout (polyforge#177 — `warn` at most, never moves the exit code)

Per mapped workspace that exists (a missing one is the `workspace dirs`
fail):

| Check | `pass` | `warn` | `skip` |
|---|---|---|---|
| `workspace layout` | the root holds only `.repo/`, `capsules/`, `scratch/`, `.polyforge/`, `CLAUDE.md`, `CLAUDE.local.md`, `AGENTS.md`, `go.work` / `go.work.sum` (a Go workspace tying the `.repo/` clones together) and dot-entries (`.polyforge.yaml`, `.gitignore`, `.pi`, `.claude`, `.mcp.json`, … — harness/tool owned), and `.polyforge/` only `repo-map/` and `watchers/` (legacy v1 artifacts nothing generates any more — tolerated, safe to delete) | each other entry, with its destination: a document (`.md`, `.txt`, `.pdf`, `.html`, …) → aihub (`pf file edit <wi> --content @f` while the work item is open, else `pf remember --type doc.design\|doc.review\|doc.decision\|doc.audit`); a `.polyforge.yaml.*` backup or other file → `scratch/`; a directory holding `.git` → `.repo/`; another directory → `scratch/`. Also the notes file (`AGENTS.md` when it exists, else `CLAUDE.md`) whose `polyforge:managed` block has no `<!-- polyforge:layout -->` section — run `pf init <ws>` (a workspace with neither file is not flagged); and a `CLAUDE.md` beside a separate `AGENTS.md` that carries a duplicate `polyforge:managed` block — `AGENTS.md` is canonical, remove the block from `CLAUDE.md` by hand (a block-less `CLAUDE.md`, e.g. an `@AGENTS.md` import, is not flagged). Nothing is moved or deleted | no mapping |
| `workspace memory policy` | every mapped workspace path declares a `[memory_policy."<path>".<entry>]` block in cli.toml (the yaml is no source) | a path without a block — the step prompts then carry the built-in default policy; run `pf init <ws>` to write an editable copy into cli.toml (doctor itself never writes it). A `.polyforge.yaml` still carrying `memory_policy:` or `summarize:` is the `workspace yaml` check's fail (file, line and `pf config migrate`), not this row's | no mapping |
| `workspace scratch` | no `scratch/` entry older than 14 days (or `--prune-scratch` removed them — the detail lists each path) | entries whose newest change — the later of mtime and ctime, of the entry and everything under it, so a download keeping old mtimes is not stale on arrival — is older than 14 days, listed; run `pf doctor --prune-scratch` to remove them. Also a `scratch` that is a symlink or a file (never scanned or pruned), and an entry that could not be read (skipped, the rest still judged) | no mapping |

Stale scratch is reported, not removed, by default: the doctor is a probe
anyone may run at any time, so deletion stays an explicit act.
Move legacy `.polyforge/*.md` documents into memories (docs/upgrade.md);
`pf init` (see `init.md`) creates `scratch/` and the notes file's layout
section.

## Workspace repo clones (polyforge#198 — `warn` at most, out of `--harness`)

One row (reported with each workspace's other layout checks, warn-only, never part
of `--harness`) scans every git unit directly under each existing mapped workspace's
`.repo/`: a clone (its `.git` a directory) or a linked worktree of one (its `.git` a
pointer file — a worktree sitting directly under `.repo/` is itself a unit; a wrapper
directory like `pi-web-worktrees/` is not, its member worktrees are reached through
the owning clone's `git worktree list`, wherever they live). Every unit is scanned
read-only (short-timeout git commands, no fetch, no optional locks); a command that
fails reports that unit "not checked" with the reason — never a silently clean face.

- **What is uncovered**: a unit's `git status --porcelain` non-empty (tracked
  modified/staged plus untracked non-ignored, the ruling-4 shape), or — never committed
  — a **local commit whose HEAD exists on no remote-tracking ref**
  (`git branch -r --contains HEAD` empty; remote-tracking refs only, no network: a
  repo with no remotes at all flags unpushed). Clean, pushed units pass.
- **What covers**: the unit checked out on its work item's **delivery branch**
  `polyforge/<project>-<seq>` and that WI read from aihub as **in flight**
  (`queued` / `running` / `paused` / `blocked`). Everything else is uncoverage:
  any other branch or a detached HEAD, and a terminal delivery face (`wrapped` /
  `failed` / `canceled`) or a 404 answer (the WI does not exist — a definitive fact,
  never an unknown) warn.
- **aihub-unreachable split**: without a reachable aihub, a delivery-branch unit's
  cover is not decidable — that unit is reported `skip` naming it (the row is `skip`
  when nothing locally decidable remains, `warn` annotating its skips when both fire);
  a locally decidable unit (any non-delivery branch, a detached HEAD) still warns.
- **Assignment facts (enrich only)**: the registry is read from aihub (`GET /projects/<p>`,
  each mapped project) only when a warning fires — and only for detail: `--project P
  --repo R` in the remediation when R is registered by exactly one project;
  "not registered in any mapped project" when every assignment face answered and R is
  recorded by none; the bare remediation when the assignment face went unread (the
  row notes the incompleteness). The assign face never moves warn/skip.
- **What the warning says** (each unit's sentence, nothing auto-done — the doctor
  stays read-only): file a WI first (`pf file create --project <P> --repo <R>`,
  the assignment facts), then carry the dirty work on its `polyforge/<project>-<seq>`
  delivery branch, or discard it. The companion chat-agent rule for the same fence
  is SKILL.md session rule 6 (polyforge#198).

## Workspace yaml

The `workspace yaml` check loads every mapped workspace's `.polyforge.yaml`
with the daemon's own loader, so it fails exactly where a claim would be
refused (DAEMON-414): a malformed shape (each entry is
`steps.<project>.<default|step-id>: { models: [...] }`, beside which a
step's entry may carry `skip: true` (a bool; switches off a step its lane
declares `skippable`, never on `default` or `task`), an entry declaring
neither is empty — a leftover `attrs` is refused naming its removal in
workflow contract 4), a
retired root `summarize:` or `memory_policy:` section (both moved to cli.toml;
the fail names the file, the line and `pf config migrate`,
which moves the values and deletes the section), a candidate that
is not `<harness>:<model>` (`pi:` or `claude:`; the detail names the
entry and index), a leftover `harness` key (with the rewrite hint), a
leftover pre-#169 top-level `models:` section (with the migration
hint), or a project cli.toml does not map to that workspace. A per-step
key that is no step id of the daemon's installed lanes (nor `default` or
`task`) does not fail it: the key is stale — it governs nothing, a claim
loads the yaml anyway — and the check warns naming each one (file and
key), the rewrite and the docs/upgrade.md section (workflow contract 1:
the retired `verify-judge` key becomes `verification`, the id of the step
that dispatches the judge). It also loads the daemon's
installed bundles: a bundle whose `model_candidates` are not prefixed
(or that still declares `harness`) fails, naming its file and step — a
claim of a work item on that bundle is refused until it is fixed. Step ids and
bundles are judged against the workflows dir the daemon reports on its
socket; with the daemon down (or too old to report it) the check is a
`warn`, never a faked pass. No mapped workspace carrying a yaml is a
pass.

## Harness claude

`harness claude` judges the Claude Code CLI the daemon would run, and
only matters when some resolved candidate is `claude:…` (otherwise it
passes: nothing needs the CLI). The daemon reports its own view on
`GET /status?harnesses=1` (the doctor runs CLI-side and cannot see the
daemon's environment); the daemon runs `claude --version` but never logs
in, never calls a model and never reports a credential value.

| Result | When | What to tell the user |
|---|---|---|
| `pass` | the binary resolves, the version is a verified one, the claude-config `settings.json` `env` block sets `ANTHROPIC_AUTH_TOKEN` or `ANTHROPIC_API_KEY` | — (the detail lists every step with a claude candidate: its whole chain, each candidate's harness shown by its prefix, and the level the chain came from) |
| `fail` | the claude binary does not resolve on the daemon's PATH | Install Claude Code and log it in (a human does this), or set `claude_bin` in cli.toml (docs/extending.md › Claude binary; `pf help extensions`). |
| `warn` | version older than the verified minimum (2.1.296) or unparsable; neither `ANTHROPIC_AUTH_TOKEN` nor `ANTHROPIC_API_KEY` in the `<harness-root>/claude-config/settings.json` `env` block (the steps' only credential source); a rejected token / gateway pairing or a `CLAUDE_CONFIG_DIR` in that `env` block | Relay the note. The claude-config `settings.json` `env` block is the single credential authority (polyforge#171; the daemon reports its keys as set/unset, never values): put ONE `ANTHROPIC_AUTH_TOKEN` (+ optional `ANTHROPIC_BASE_URL`) there, never `CLAUDE_CONFIG_DIR` — see docs/upgrade.md. |
| `skip` | claude steps exist but the daemon is unreachable | Start the daemon and re-run. |

Note (env hygiene, superseding V-11): claude steps strip every inherited
`ANTHROPIC_*`, so the claude-config `settings.json` `env` block is the ONLY
credential / endpoint source; the daemon env's `ANTHROPIC_*` are reported
as facts in the detail but neither count as a credential nor reach a step.
`pf daemon ensure` gives the daemon no `ANTHROPIC_*` anyway (an allow-listed
environment, see `daemon env`).

## Harness diagram guidance (`warn` at most, out of `--harness`)

For every installed harness — one whose directory exists; an absent one is
left out, never warned — the row reads the context file that harness loads:

| Harness | Context file |
|---|---|
| pi | `$PI_CODING_AGENT_DIR/AGENTS.md`, else `~/.pi/agent/AGENTS.md` |
| claude | `~/.claude/CLAUDE.md` |
| step-agent | `<harness-root>/step-agent/AGENTS.md` (`[daemon] harness_root`, else the data dir) |

A file passes only when it carries the SVG block: its begin marker
`<!-- polyforge:diagram-guidance:svg:begin -->`. A file without it that
holds legacy mermaid-only guidance — the old begin marker
`<!-- polyforge:diagram-guidance:begin -->`, or a markdown heading naming
图形优先 / 图形输出 / diagram / graphic with `mermaid` in the file — is
reported `legacy`, a `warn`, never a pass; a file with neither is `missing`.
`pass` = every installed harness's file carries the SVG block; `warn` = a
file is legacy, missing, absent or unreadable — the detail carries the
canonical SVG block to add by hand. The block (`svg:begin` / `svg:end`
markers around `## 输出规范：图形优先（原生 SVG）`) is SVG-first with mermaid
as history: diagrams are native SVG in ```svg blocks (pi-web renders the SVG
DOM directly, styles follow the panel theme) and every new diagram is SVG;
old mermaid diagrams still render, for historical compatibility only; draw
proactively, unasked, when the discussion has ≥3 subjects/components, a
multi-stage flow, a responsibility or data handoff or a state transition;
keep it compact (element text ≤2 lines, ≤8 elements, one structure per
diagram); and meet the quality baseline (viewBox sizing, no text overflow,
neutral/theme colours, even spacing).

`pf doctor --fix-harness-prompts` appends the SVG block to every **existing,
readable** context file of an installed harness that lacks it — `legacy` and
`missing` alike — and the row reports `legacy; appended` / `missing; appended`.
It never creates a missing file, never rewrites or removes a byte already
there (a legacy section stays: delete it by hand after reading it), keeps the
file's mode (a symlinked file is written at its target), and writes
atomically; a second run changes nothing. A failed append is a `warn` naming
the error. To roll back, delete only the SVG block: the lines from
`<!-- polyforge:diagram-guidance:svg:begin -->` to
`<!-- polyforge:diagram-guidance:svg:end -->`.

The step-agent caveat: the daemon's step-agent dir deliberately carries no
`AGENTS.md` and prunes every top-level entry outside its minimal set at
daemon start, so that file is normally absent (`warn`, with this caveat in
the detail), the fix does not create it, and a file made by hand there does
not survive a daemon restart.

## Success

`ok: true` and every check `pass` (exit 0). Human mode prints one line
per check with its status and detail; `--json` prints the document only.

## Failure branches

| Code | When | What to tell the user |
|---|---|---|
| exit 1, `config` fail | old-shape or malformed config | Follow the printed migration steps: replace the flat `project` key with a `[workspace."<path>"]` section (`projects = [...]`), then re-run. |
| exit 1, `workspace mapping` fail | zero sections (fresh template) | Add a real mapping to the config — the daemon refuses to start without one. |
| exit 1, `workspace dirs` fail | a mapped directory is missing | Create it (the daemon never does) or fix the `[workspace]` key. |
| exit 1, `aihub projects` fail | a mapped project is unknown to aihub, the key's user is not a member of it (aihub answers 404 for both), or the project read fails | File/register the project on aihub, add the key's user to the project (`pf projects members add`), or fix the mapping; the detail names the project and its workspace. |
| exit 1, `server reach` fail | aihub unreachable through the daemon | Check the Cell's configured `server` (`pf config get server`; a different server is a new profile, never an in-place edit) and whether the aihub host is up. |
| exit 1, `daemon socket` fail | no live daemon | Run `pf daemon ensure` (it refuses, naming the reason, while the config has no real `[workspace]` mapping or the release workflows are missing), then re-run `pf doctor`. |
| exit 1, `auth` fail | the credential is rejected | Fix the api key (the Cell's config `api_key`). |
| exit 1, `harness claude` fail | a candidate is `claude:…` but the daemon cannot find the claude binary | Install/log in Claude Code on the machine or set `claude_bin` in cli.toml (docs/extending.md › Claude binary; `pf help extensions`); or drop the `claude:` candidates. |
| exit 1, `--fix-harness-prompts cannot be combined with --harness` | both flags given | Drop `--harness`: the fix runs only in the full report. |
| exit 1, `--fix-install-layout cannot be combined with --harness` | both flags given | Drop `--harness`: the fix runs only in the full report. |
| exit 2 | usage error (unknown flag) | Fix the invocation. |

A `dial unix …` inside the daemon-socket check's detail means the daemon
is not running on this machine — it is a finding, not a crash of the
doctor itself. The same holds for every execution-plane verb: on
`dial unix …` run `pf daemon ensure`, then retry the verb once.
