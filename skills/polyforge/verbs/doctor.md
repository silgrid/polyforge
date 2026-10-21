# doctor — the machine's single health face

CLI-meta verb: one command reports whether this machine can do its job —
the config, the `[workspace]` mapping (polyforge#113) and the three
connectivity faces (aihub server reach, local daemon socket, auth), whether
the daemon runs under supervision, whether the five setup tools
(`gh`, `curl`, `python3`, `tmux`, `git`) are on PATH, plus the harness family: whether each
harness will display polyforge progress, whether it references the release
the way `tools/install.sh` sets it up, and whether v1 polyforge artifacts are
still installed — and whether each mapped workspace keeps the runtime-artifact
layout (polyforge#177). It is a read face: nothing is mutated, nothing is
auto-fixed (there is no `--fix`; remediation is always manual, the check
detail names it) — the one exception is `--prune-scratch`, which removes
stale `scratch/` entries and lists them. The one remediation the agent runs itself is the
daemon-down case: `pf daemon ensure`, then `pf doctor` again.

## Triggers

- EN: is this machine healthy / something's wrong / why won't the daemon
  start / check the workspace mapping / diagnose this box / pre-upgrade
  check / is aihub reachable
- 中文: 体检 / 诊断 / 这台机器健康吗 / daemon 为什么起不来 / 检查映射 /
  升级前自检

## Command

```
pf doctor [--harness] [--prune-scratch] [--json]
```

`--harness` runs only the harness family (progress display, install
layout, v1 leftovers — no config, no network); `tools/install.sh` prints
its rows.

Exit codes follow the repo convention: 0 every check passes, 1 any check
fails, 2 usage errors. `--json` emits one machine document:
`{"ok": <bool>, "checks": [{"name", "status": "pass"|"fail"|"skip"|"warn",
"detail"}]}`.

## The checks

| Check | What it proves | On fail |
|---|---|---|
| `config` | the config loads (including the `[daemon]` table: every value validated) | the load error verbatim (an old-shape flat `project` key, a malformed mapping or a bad `[daemon]` value — `line N: [daemon] <key>: …` — names itself) plus migration guidance; every config-dependent check is then `skip` — never a faked green |
| `retired env` | no retired `POLYFORGE_HARNESS_ROOT` / `POLYFORGE_OPERATOR_AGENT_DIR` is in effect (this shell, or the daemon's `env_warnings`) | `warn` naming the variable and the `[daemon]` key (`harness_root` / `operator_agent_dir`) to set instead — the variable is no longer read, so harness state would move |
| `daemon env` | the running daemon's environment (GET /status `env_names`: names only, never values) carries no `ANTHROPIC_*` and no credential-looking name (`…TOKEN`, `…KEY`, `…SECRET`, `PASSWORD`) outside the `pf daemon ensure` allow-list, and the pi step agent's `models.json` interpolates (`$NAME`) no variable off that list; `skip` when the daemon is down or too old to report names | `warn` naming the variables: a polluted daemon environment (an older ensure under a tmux server carrying a human's credentials) — restart it clean: `pf ops shutdown`, wait until it is gone, `pf daemon ensure`; a `models.json` `$NAME` off the list — pi steps cannot see it: put the key in `auth.json` (pi `/login`) or as a literal in `models.json` |
| `workspace mapping` | at least one `[workspace."<path>"]` section exists | guidance to add the shape, plus the pointer to README "Extending" (`pf help extensions`) (a zero-section config is the fresh-install template) |
| `workspace dirs` | every mapped workspace pre-exists as a directory | the missing path(s), named |
| `workspace yaml` | every mapped `.polyforge.yaml` loads and every candidate (yaml and installed bundles) is `<harness>:<model>` (see below) | the refusal a claim would meet (DAEMON-414), file and key named, plus the pointer to README "Extending" (`pf help extensions`) |
| `harness claude` | when some resolved candidate is `claude:…`: the daemon finds the claude binary (see below) | the binary the daemon could not resolve |
| `aihub projects` | every mapped project is known to aihub and readable with the api key (its ready-queue read — the project-member face the daemon uses; no admin role needed) | the project AND its workspace, named (an ambiguous inverse or duplicate surfaces through the config check, both workspaces named) |
| `workspace repo clones` | no uncovered uncommitted change or unpushed local commit in the `.repo/` git clones of every existing mapped workspace and their linked worktrees (see below); `warn` at most — never `fail`, never moves the exit code; `skip` naming the unit and its reason when the covering WI state could not be read | the clone AND its owning workspace, named, with the remediation (see below) |
| `prerequisites` | the five setup tools — `gh`, `curl`, `python3`, `tmux`, `git` — are each found on `PATH` (LookPath); `warn` at most — never `fail`, never moves the exit code — the doctor never installs anything; `skip` when the config does not load, like every config-dependent row; out of `--harness` | each missing tool named with its obtain hint (macOS: `brew install gh`, `brew install tmux`, `xcode-select --install`; Linux: `apt install …`) — install the named tools by hand; a missing `tmux` means the daemon runs unsupervised (see the daemon supervision row) |
| `server reach` | aihub answers at all (any HTTP answer counts) | the dial failure |
| `daemon socket` | the local daemon answers GET /status over its unix socket | the socket path and the dial error — the daemon is down: run `pf daemon ensure` (starts it under its restart loop), then re-run the doctor |
| `auth` | the api key gets past aihub's auth boundary (the same signed read the projects check uses) | the typed 401/403 answer — fix `api_key` |

Checks that cannot be proven (config unloadable, aihub unreachable) are
reported `skip` — a skip never pretends to be a pass.

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

## Progress display (presentation only — never `fail`, never moves the exit code)

| Check | `pass` | `warn` (detail carries the fix) | `skip` |
|---|---|---|---|
| `claude progress display` | `~/.claude/settings.json`'s statusLine command runs `pf watch --statusline`, directly or in a script file it names | no statusLine → the full `"statusLine"` snippet; a statusLine without pf → an append-style snippet that runs the user's command first, keeps its output, then the progress rows; unreadable/malformed settings, or a named script that could not be checked (relative, missing, or itself naming another script) → "cannot tell" | no `~/.claude` |
| `pi progress display` | pi settings list the polyforge package (or `pf-chat.ts` itself) | the `pi install …` line; malformed settings → "cannot tell" | no pi agent dir (`$PI_CODING_AGENT_DIR` or `~/.pi/agent`) |

The judgement is static: the statusLine command is read, never run, and
no user file is ever written — the user pastes the snippet.

## Install layout and v1 leftovers (presentation only — never `fail`)

| Check | `pass` | `warn` (detail carries the fix) | `skip` |
|---|---|---|---|
| `claude install layout` | `~/.claude/skills/polyforge` is a symlink to `<data-dir>/release/current/skills/polyforge` | a copied directory (the pre-#174 layout), a link elsewhere, no skill, or a link with no release behind it → re-run `tools/install.sh` | no `~/.claude` |
| `pi install layout` | pi's settings `packages` hold the local release `<data-dir>/release/current` | a `git:…silgrid/polyforge.git@<ver>` entry (the old clone), another polyforge checkout, no entry, or no release behind it → re-run `tools/install.sh` | no pi agent dir |
| `v1 harness leftovers` | no v1 artifact | any of: a pi package from `GMISWE/ieops-aihub`, a v1 bridge extension listed in pi's settings or present at `~/.pi/agent/extensions/polyforge`, a v1 Claude skill (`using-polyforge`, `pf-*`), a v1 Claude plugin (`polyforge@…`, the ieops-aihub marketplace) — the detail lists each and one copy-paste command line that first backs everything up into `~/polyforge-v1-backup-<timestamp>/`, then removes them (`pi remove`, `mv`, `claude plugin uninstall`) | — |

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
| `workspace layout` | the root holds only `.repo/`, `capsules/`, `scratch/`, `.polyforge/`, `CLAUDE.md`, `CLAUDE.local.md`, `AGENTS.md`, `go.work` / `go.work.sum` (a Go workspace tying the `.repo/` clones together) and dot-entries (`.polyforge.yaml`, `.gitignore`, `.pi`, `.claude`, `.mcp.json`, … — harness/tool owned), and `.polyforge/` only `repo-map/` and `watchers/` (legacy v1 artifacts nothing generates any more — tolerated, safe to delete) | each other entry, with its destination: a document (`.md`, `.txt`, `.pdf`, `.html`, …) → aihub (`pf file edit <wi> --content @f` while the work item is open, else `pf remember --type doc.design\|doc.review\|doc.decision\|doc.audit`); a `.polyforge.yaml.*` backup or other file → `scratch/`; a directory holding `.git` → `.repo/`; another directory → `scratch/`. Also a `CLAUDE.md` whose `polyforge:managed` block has no `<!-- polyforge:layout -->` section — run `pf init <ws>` (a workspace without `CLAUDE.md` is not flagged). Nothing is moved or deleted | no mapping |
| `workspace memory policy` | every mapped workspace's `.polyforge.yaml` declares a `memory_policy` section, or the workspace has no `.polyforge.yaml` at all (the built-in default applies; `pf init <ws>` writes an editable copy) | a workspace whose `.polyforge.yaml` declares no `memory_policy` — the step prompts then carry the built-in default policy; run `pf init <ws>` to append the default section (doctor itself never writes it). A file that does not load is the `workspace yaml` check's fail, not this row's | no mapping |
| `workspace scratch` | no `scratch/` entry older than 14 days (or `--prune-scratch` removed them — the detail lists each path) | entries whose newest change — the later of mtime and ctime, of the entry and everything under it, so a download keeping old mtimes is not stale on arrival — is older than 14 days, listed; run `pf doctor --prune-scratch` to remove them. Also a `scratch` that is a symlink or a file (never scanned or pruned), and an entry that could not be read (skipped, the rest still judged) | no mapping |

Stale scratch is reported, not removed, by default: the doctor is a probe
anyone may run at any time, so deletion stays an explicit act.
Move legacy `.polyforge/*.md` documents into memories (docs/upgrade.md);
`pf init` (see `init.md`) creates `scratch/` and the CLAUDE.md layout
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
`steps.<project>.<default|step-id>: { models: [...] }`; a `memory_policy`
entry is `<default|project>: { record: [...], skip: [...] }`, lists of
non-empty unique strings), a candidate that
is not `<harness>:<model>` (`pi:` or `claude:`; the detail names the
entry and index), a leftover `harness` key (with the rewrite hint), a
leftover pre-#169 top-level `models:` section (with the migration
hint), a step id not in the daemon's installed workflows, or a project
cli.toml does not map to that workspace. It also loads the daemon's
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
| `fail` | the claude binary does not resolve on the daemon's PATH | Install Claude Code and log it in (a human does this), or set `claude_bin` in cli.toml (README Extending › Claude binary; `pf help extensions`). |
| `warn` | version older than the verified minimum (2.1.296) or unparsable; neither `ANTHROPIC_AUTH_TOKEN` nor `ANTHROPIC_API_KEY` in the `<harness-root>/claude-config/settings.json` `env` block (the steps' only credential source); a rejected token / gateway pairing or a `CLAUDE_CONFIG_DIR` in that `env` block | Relay the note. The claude-config `settings.json` `env` block is the single credential authority (polyforge#171; the daemon reports its keys as set/unset, never values): put ONE `ANTHROPIC_AUTH_TOKEN` (+ optional `ANTHROPIC_BASE_URL`) there, never `CLAUDE_CONFIG_DIR` — see docs/upgrade.md. |
| `skip` | claude steps exist but the daemon is unreachable | Start the daemon and re-run. |

Note (env hygiene, superseding V-11): claude steps strip every inherited
`ANTHROPIC_*`, so the claude-config `settings.json` `env` block is the ONLY
credential / endpoint source; the daemon env's `ANTHROPIC_*` are reported
as facts in the detail but neither count as a credential nor reach a step.
`pf daemon ensure` gives the daemon no `ANTHROPIC_*` anyway (an allow-listed
environment, see `daemon env`).

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
| exit 1, `server reach` fail | aihub unreachable | Check `--server` / the config's `server` and whether the aihub host is up. |
| exit 1, `daemon socket` fail | no live daemon | Run `pf daemon ensure` (it refuses, naming the reason, while the config has no real `[workspace]` mapping or the release workflows are missing), then re-run `pf doctor`. |
| exit 1, `auth` fail | the credential is rejected | Fix the api key (config `api_key` / `PF_API_KEY`). |
| exit 1, `harness claude` fail | a candidate is `claude:…` but the daemon cannot find the claude binary | Install/log in Claude Code on the machine or set `claude_bin` in cli.toml (README Extending › Claude binary; `pf help extensions`); or drop the `claude:` candidates. |
| exit 2 | usage error (unknown flag) | Fix the invocation. |

A `dial unix …` inside the daemon-socket check's detail means the daemon
is not running on this machine — it is a finding, not a crash of the
doctor itself. The same holds for every execution-plane verb: on
`dial unix …` run `pf daemon ensure`, then retry the verb once.
