# Config — every real key, each once, in one place

The one authority for Polyforge's two configuration files: `cli.toml`
(the CLI and daemon config) and `.polyforge.yaml` (the workspace-level
per-step settings) — plus the per-repository verification bar,
`.polyforge/verify-scope.conf`, and its dependency-audit exemption ledger,
`.polyforge/audit-exemptions.conf`. Every key that exists is documented here; a
key not on this page does not exist. The load behavior (precedence, validation,
refusals) is stated once per file; the [commands page](commands.md) owns
the command surface, and [extending](../extending.md) indexes the
extension points that point into this page. The concepts (workspace,
repo mapping, machine) are defined on the [concepts
page](../concepts.md).

## Cross-machine face: no new key

The cross-machine capability is default-on and zero-config. There is no
`cli.toml` key, no `.polyforge.yaml` key and no daemon flag an operator must
set: a daemon always creates/loads its `<data-dir>/machine-key`, registers
through the existing heartbeat, and disables a face at runtime only when the
schema/hub cannot support it (an old hub answers no `machine_face_version`).
Keeping a machine idle, rekeying it, or superseding the interactive daemon
are explicit verbs (`pf ops role interactive --force`, `pf machines rekey`), not config.

## cli.toml

`~/.config/polyforge/cli.toml` (override with `--config PATH`; there is
no environment override for the path). One location, no
secondary lookup. Precedence: command-line flag > `PF_*` environment >
this file > built-in defaults. A Cell's connection (`server`, `api_key`) has
no flag or environment override: `--server` and `--api-key` are refused by
every `pf` verb, and `PF_SERVER`/`PF_API_KEY` by every verb but the local
repair verbs (`config`, `profile`, `doctor`, `init`, `migrate`, `version`), which ignore an inherited
value with one stderr line naming the variable(s) — never a value — and
never use it as configuration or a network target — the daemon reads the
Cell's connection from its file and forwards every business call through it. A missing file is not an error (defaults
apply, empty mapping); a malformed file IS an error — silent
misconfiguration is worse than a loud failure — and so is a mapping that
fails validation. The file is validated on load by every `pf` command,
by `pf daemon ensure` and by the daemon (the same check).

### Host and Cells — `cli.toml` and `cli-<profile>.toml`

One daemon process (the **Host**) serves one or more **Cells**; a Cell is
one AIHub identity domain (one server, one user) with its own workspaces,
queue, tasks, machine identity and execution state. A Cell is a unit of
state isolation, never a sandbox: every Cell runs inside the one Host
process.

- `~/.config/polyforge/cli.toml` is the one **Host file**: the Host keys
  and the `default` Cell's keys.
- `cli-<profile>.toml` in the same directory defines exactly one **named
  Cell** and carries Cell keys only. A Host-only key in it is refused naming
  the file, the line and the key.
- A profile name matches `[a-z0-9][a-z0-9-]{0,31}`. `default` is reserved
  (`cli-default.toml` is refused); a name is never renamed — another name
  is another Cell.
- An `instances/` directory next to `cli.toml` is not a configuration form
  and is refused by name.
- `--config PATH` addresses one local file for configuration and repair; it
  never selects a Cell.

A key's scope is fixed by this table, never by the file it sits in (the
one partition; `internal/polyforge/cellcfg/schema.go` owns it):

| key | section | scope | class | note |
|---|---|---|---|---|
| `daemon_socket` | flat | Host-only | restart-only | the one UDS |
| `max_concurrent_jobs` | `[daemon]` | Host-only | live | the Host hard ceiling (`pf ops max` overrides it temporarily) |
| `max_concurrent_bars` | `[daemon]` | Host-only | live | the Host heavy-run admission's slots |
| `bar_load_threshold` | `[daemon]` | Host-only | live | the Host heavy-run admission's load gate |
| `harness_root` | `[daemon]` | Host-only | restart-only | parent of the per-Cell harness roots (`<harness_root>/cells/<profile>`) |
| `operator_agent_dir` | `[daemon]` | Host-only | restart-only | the shared model-credential source the default Cell syncs from |
| `python` | `[daemon]` | Host-only | restart-only | the interpreter the workflow step scripts run under (`PF_PYTHON`); resolved and checked at the daemon's start |
| `layout_version` | — | Host state | read-only | reports `<data-dir>/layout-version`; never settable |
| `server` | flat | Cell | connection-change | one normalized AIHub per Cell |
| `api_key` | flat | Cell | connection-change; write-only | same-identity rotation only |
| `user_id` | flat | Cell | cli-only | caller identity for `answer.answered_by` |
| `claude_bin` | flat | Cell | live | execution configuration |
| `step_timeout` | `[daemon]` | Cell | live | execution configuration |
| `stall_window` | `[daemon]` | Cell | live | execution configuration |
| `ask_warm_window` | `[daemon]` | Cell | restart-only | how long a step process that asked the owner (`pf step ask`) stays resident for the reply |
| `command_path` | `[daemon]` | Cell | live | the PATH of the commands the daemon runs in-process, and of every step process behind the daemon's `pf` directory and the harness binary's |
| `cell_max_concurrent_jobs` | `[daemon]` | Cell | live | optional per-Cell ceiling; absent = no extra limit. A reload applies it to the Cell's admission at once, clamped to the Host ceiling (`max_concurrent_jobs`); unset (or `0`) falls back to the Host's current ceiling. A raise admits queued work up to the new value, a lower value only stops new starts (nothing is killed); other Cells are untouched. `GET /profiles` reports the applied value and the effective `max_concurrent` |
| `machine_id` | `[daemon]` | Cell | restart-only | seeds `cells/<profile>/daemon-id`; the file wins once written |
| `heartbeat_every` | `[daemon]` | Cell | live | that Cell's heartbeat cadence (Go duration) |
| `agent_language` | `[daemon]` | Cell | live | the language of that Cell's step agents |
| `source` | `[model]` | Cell | restart-only | model-credential declaration of a named Cell: `shared-default` or an absolute directory. Each named Cell has its own harness root (`cells/<profile>`, or `<harness_root>/cells/<profile>`) with its own `step-agent`, `step-agent-sessions`, `claude-config` and `summarize-cwd`. `shared-default` syncs the pi minimal set (`auth.json`, `models.json`) from the default Cell's source and copies only the `env` block of the default Cell's `claude-config/settings.json` — never agents, skills, history or other settings; a directory must be absolute and carry a readable `auth.json`. No declaration, or an unusable one, keeps the Cell's execution plane disabled with the reason on `GET /status`/`GET /profiles` (its data plane still serves). The default Cell keeps syncing from the Host `operator_agent_dir`. |
| `[workspace."<path>"]` | table | Cell | next-claim | the Cell's repo mapping |
| `summarize.models` / `.language` / `.interval_sec` | `[summarize]` | Cell | live | summarize lane |
| `[memory_policy."<path>"."<entry>"]` | table family | Cell | next-claim | wrap-summary policy |

The CLI resolves a Cell-scoped command's Cell from `--profile`, then
`PF_PROFILE`, then the cwd matched against the Host's effective workspace
index, then `default` ([commands: profile
resolution](commands.md#the-invocation-shape)). A local verb that runs
with the daemon down (`pf profile`) matches the cwd against the index
built from these files on disk instead (source `disk`); business commands
never route from the disk index — when the effective index cannot be read
they fail `DAEMON-410` naming it, and nothing is sent.

Machine identity is per Cell: each Cell keeps its own
`cells/<profile>/daemon-id` and `cells/<profile>/machine-key` (the default
Cell's moved there by the cell-layout migration), registers with
its own Hub and heartbeats through its own connection. `[daemon]
machine_id` seeds the id before the file exists (default
`m-<short hostname>` for the default Cell, `m-<short hostname>-<profile>`
for a named one); once written the file wins, and a differing
configured value refuses that Cell — never the Host. Every Cell's machine
id is its own (its step and task processes are told apart by it): a named
Cell sharing one with another Cell is invalid, visibly so, whatever the
file order. `[daemon]
heartbeat_every` (a Go duration) is that Cell's heartbeat cadence.

Identity and conflicts: a normalized server (`scheme://host:port`,
lowercase, no trailing slash) belongs to at most one Cell, and two Cells'
workspaces may not be the same directory or nested (compared after
resolving symlinks). The `default` Cell is the baseline: a named Cell that
conflicts with it is invalid; named Cells that conflict with each other are
all invalid, whatever the file order. An invalid named Cell is isolated and
visible on the Host's Cell registry; an invalid Host file blocks the
Host start.

### Flat keys

Root-level wherever they appear in the file, but they must come before
any table header (TOML semantics: every key after a `[...]` header
belongs to that table):

| Key | Env override | Controls | Default |
| --- | --- | --- | --- |
| `server` | — | the Cell's aihub base URL (read by the daemon; the CLI never dials it) | `http://localhost:8420` (default Cell only) |
| `api_key` | — | the Cell's aihub api key (Bearer; used by the daemon only) | — |
| `daemon_socket` | `PF_DAEMON_SOCKET` | daemon unix socket path | `daemon.sock` in `$POLYFORGE_DATA_DIR`, else `~/.local/share/polyforge` |
| `user_id` | `PF_USER_ID` | caller identity for answer.answered_by | — |
| `claude_bin` | — | the Claude Code binary of claude steps (see below) | `claude` |

There is NO default
`project` key: a config carrying one refuses to load with migration
guidance (replace it with a `[workspace]` section, then run
`pf doctor`). A `projects` key at the top level is the same refusal.
Any other top-level key is an unknown-key error — the former
`anthropic_auth_token` is hard-deleted (polyforge#171), so a residual
key in an old cli.toml fails at startup, never silent acceptance.

### `[workspace."<path>"]` — the repo mapping

The daemon's ONLY workspace source (polyforge#113). One section per
workspace, `projects = ["..."]` under each:

```toml
[workspace."/srv/team/workspaces/main"]
projects = ["polyforge", "pi-web"]

[workspace."/home/you/ws2"]
projects = ["demo"]
```

`~` at the start of a key expands to the home directory (`~` alone or
`~/…`); the result must be absolute with no trailing separator (never
silently trimmed). Every mapped
workspace directory must pre-exist; the daemon creates and writes only
`capsules/` inside each and refuses to start when any is missing or
unusable (or when the config carries zero sections). Capsules route per
claimed work item's project through the inverse mapping;
`<workspace>/capsules/<project>-<seq>/`. Inside a section only the
`projects` key is legal (an array of quoted strings); duplicates and
other keys are line-named parse errors. `pf doctor` checks the mapping.

### Data layout — `layout-version` and `cells/<profile>`

The Host root (`--data-dir`, default `~/.local/share/polyforge`) keeps the
Host state — `decisions.json` (the decision feed), `daemon.lock`, the socket,
`release/`, `migration/` — and the read-only `layout-version` (`cells-v1`).
Every Cell, `default` included, writes under `cells/<profile>/`
(`daemon-id`, `machine-key`, `runtime-state.json`, `claiming.json`,
`submit-queue.json`, `manual-models.json`, `events/`, `git/`,
`model-health.json`, `task-reports/`, `step-agent*/`, `claude-config/`,
`summarize-cwd/`); with `harness_root` set, a Cell's harness dirs live
under `<harness_root>/cells/<profile>/`. A data dir with the pre-Cell
layout and no `layout-version` is refused at start naming
`pf migrate cell-layout` ([commands](commands.md)); an empty one is
initialized.

### `[daemon]` — the daemon knobs

The production knobs the daemon reads from its `--config` file (decision
2026-10-16 G2). Precedence: an explicit command-line flag (tests/dev) >
this table > the built-in default. At most one table per file.

```toml
[daemon]
step_timeout = "45m"                 # --step-timeout: Go duration, > 0
stall_window = "10m"                 # --stall-window: Go duration, non-zero; negative disables
ask_warm_window = "15m"              # --ask-warm-window: Go duration, > 0
harness_root = "/srv/polyforge/h"    # --harness-root: absolute path
operator_agent_dir = "/home/u/.pi/agent"  # --operator-agent-dir: absolute path
python = "/usr/bin/python3"         # no flag: absolute path, Python >= 3.9
max_concurrent_jobs = 6             # --max-concurrent-jobs: unquoted integer >= 1
max_concurrent_bars = 2             # no flag: unquoted integer >= 1
bar_load_threshold = 12             # no flag: unquoted number > 0
command_path = "/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin"  # no flag: absolute dirs, ':'-separated
agent_language = "zh-CN"            # no flag: quoted "en" or "zh-CN"
```

Every key is optional (absent = the daemon default). The keys and their
defaults:

| Key | Flag | Default | Controls |
| --- | --- | --- | --- |
| `step_timeout` | `--step-timeout` | `90m` | T: the cumulative step runtime bound across a step's dispatches. |
| `stall_window` | `--stall-window` | `10m` | N: the driver between steps (not waiting on a human) for longer than this is judged stalled; a negative value disables it. |
| `ask_warm_window` | `--ask-warm-window` | `15m` | How long a step process that asked the owner (`pf step ask`, docs/reference/commands.md) is kept resident for the reply before the daemon ends it (stdin closed, then the signal); the step stays awaiting the owner either way and the reply resumes the session. While it waits the job holds no running slot and nothing of the dispatch is judged. Restart-only. |
| `harness_root` | `--harness-root` | `<data-dir>` | Parent root of the step harness dirs: `<root>/step-agent` (the dedicated step agent dir), `/step-agent-sessions` (pi sessions) `/claude-config` (claude's config dir; its settings.json env block is the credential authority layer) and `/summarize-cwd` (the working directory of the summarize lane's one-shot dispatches, never the daemon's `$HOME` cwd). Replaces the former `--harness-sessions-root`, which is refused. |
| `operator_agent_dir` | `--operator-agent-dir` | `~/.pi/agent` | SOURCE of the dedicated step agent dir — a credential surface: the daemon copies the minimal set (`auth.json`, `models.json`, `agents/`, `extensions/subagent/`) from here into `<harness-root>/step-agent`, and every pi step runs against that copy. A configured source without a readable `auth.json` refuses the daemon start. |
| `python` | — | `python3` on the daemon's `PATH` | The interpreter a workflow's step scripts run under — every step process gets it as `PF_PYTHON`, and a skill runs a script as `"$PF_PYTHON" "$PF_SHARED_DIR/scripts/<name>.py"` (docs/reference/workflow-contract.md, "Scripts"). The daemon resolves it once at its start — this key, else `python3` on its own `PATH` (under `pf daemon ensure` not an interactive shell's) — and asks the interpreter for its version: a missing interpreter, one that does not answer, or one older than 3.9 refuses the start (exit 78) naming the source, the path, the version and this key; a set value is never replaced by the `PATH`'s. A quoted absolute path. `pf doctor`'s `python3` row judges the same rule from the shell it runs in. Host-only, restart-only. |
| `max_concurrent_jobs` | `--max-concurrent-jobs` | `4` | The configured cap on concurrently running jobs; `pf ops max N` overrides it until the daemon restarts, and a reload that **changes** `max_concurrent_jobs` overrides `pf ops max` too (last action wins). |
| `max_concurrent_bars` | — | `2` | The heavy-run admission's global slots: how many dispatches of a step declaring `resources: { admission: heavy }` (the develop lane's verification) run at once across every repo, work item and Cell of the Host. A further run waits in the admission queue — ordered by the work item's priority, then by when it first queued — holding no process, no budget and no job slot (`max_concurrent_jobs` does not count it while it waits — the freed slot may start other work — and counts it again once it is admitted, so the running count can briefly exceed the cap: new starts wait, nothing is stopped). Live (the default Cell's file): the next dispatch tick honours a change. |
| `bar_load_threshold` | — | cores − 4 (at least `1`) | The heavy-run admission's load gate: a further heavy run is admitted only while the 1-minute load average (`/proc/loadavg`) is below this value. It never holds the only run (with no admitted run running the next one starts whatever the load), and it is ignored where the load cannot be read. An unquoted number > 0; live like `max_concurrent_bars`. |
| `command_path` | — | the daemon process's `PATH` | The `PATH` of the git and gh the daemon runs in-process, and the heart of every step process's `PATH` — so the scripts a step runs (`verify.py`, `run.py`, `facts.py`, `gate.py`, …) run their commands and the scope engine `workflows/_shared/scripts/verify-scope` (the engine's `go list` included) under it, and so do the step agent's own commands and the harness itself. Set, a step's `PATH` is, each entry once in this order: the directory of the daemon's own `pf` executable (symlinks resolved), so the step's `pf step …` resolves; the directory of the step's harness binary (`pi`, or `claude_bin`) as the daemon resolves it on its own `PATH`, so a harness that is a `#!/usr/bin/env node` script finds the `node` an npm or nvm install keeps beside it; then the key's entries — a value that lacks either directory (such as the example above) costs a step neither. Unset, the step and the in-process commands inherit the daemon process's own `PATH` unchanged — under `pf daemon ensure` that is not an interactive shell's, so a toolchain only a shell profile (`.zshrc`, `.bashrc`) puts on `PATH` is not found there (`go: command not found`, exit 127, and the engine falls back to the full tier). A quoted value of `:`-separated entries, each an absolute directory (no empty entry, no `~` or `$HOME` expansion); it replaces the rest of `PATH` whole, so keep the system directories (`/usr/local/bin:/usr/bin:/bin`) in it — the engine itself needs `git`, `python3` and the POSIX tools. It applies only to a repository whose bar runs on the host: one whose merged tree carries `.polyforge/Dockerfile.verify` runs its engine and bar in a container whose `PATH` is the image's (docs/concepts.md, "The light verification"); a script finds its `docker` client on this `PATH` first. `GET /status` reports it as `command_path` and the step `PATH` it composes (before the harness's directory) as `step_path`; `pf doctor`'s `daemon command path` row shows `step_path` and whether `pf` and the engine's tools (`git`, and `go` for a Go repo verified on the host — not for one verified in its container, whose image provides it) resolve under it. Live: the next run uses a changed value. |
| `agent_language` | — | `en` | The language of the daemon's step agents: prompt instruction, hand-off headings and stamps; fixed per dispatch. Exactly `"en"` or `"zh-CN"`, quoted; any other spelling is refused naming the key. Live: a dispatch that starts a new conversation after the change takes the new value; a running dispatch, and one that resumes its previous conversation, keeps the language it started with. |

A bad value, an unknown key or a duplicate is refused naming the file,
the line and the key (`config …: line N: [daemon] step_timeout: "soon"
is not a Go duration`); ensure starts nothing and the daemon exits 78.
Change a key with `pf config set <key> <value>` (never hand-edit unless
you then run `pf config reload`): the running daemon applies the live
keys without a restart, reports the next-claim and restart-only keys as
described below, and `pf ops status` shows what is pending.

### `claude_bin`

A flat key (before any table header), e.g.
`claude_bin = "/opt/claude/bin/claude"`, naming the Claude Code binary
the daemon runs for steps whose harness is `claude`. Precedence: the
daemon's `--claude-bin` flag (tests/dev) > `claude_bin` > `claude` on
PATH. How the binary is invoked and where its credentials live: the
`--claude-bin` row of `pf help daemon` (`claude -p --safe-mode
--setting-sources user` with the AGENTS.md plugin off and no
cross-session messaging inbox, for a token in
the claude-config settings.json env block and for an OAuth login alike;
the daemon never manages credentials —
`<harness-root>/claude-config/settings.json` is the authority layer).
The running daemon applies a changed `claude_bin` on the reload
(`pf config` notifies it); no restart is needed unless the daemon was
started with an explicit `--claude-bin` flag, which pins the value and is
reported as overridden.

Every claude step also starts with the Bash tool's per-call timeouts set:
`BASH_DEFAULT_TIMEOUT_MS=600000` (10 minutes, for a command that names no
timeout of its own) and `BASH_MAX_TIMEOUT_MS` = the daemon's supervision
tool window (`--tool-window`, test/dev-only) minus one minute (`1740000`
with the default 30-minute window), so a long `make test` runs in the
foreground instead of hitting Claude Code's 2-minute default. The default
never exceeds the max: a tool window of 11 minutes or less gives
default = max. A key the claude-config settings.json env block carries
itself wins: the daemon adds neither key that block sets (it never writes
the file), and a default it adds next to an operator's own
`BASH_MAX_TIMEOUT_MS` is clamped to that max. pi steps get neither Bash
variable.

Every step, pi and claude alike, also starts with `PF_TOOL_WINDOW_MS` set
to the supervision tool window in milliseconds (`1800000` with the default
30-minute window). It is the harness-neutral binding the step scripts
(`verify.py`, `run.py`, `gate.py`) derive their deadline from, below the
tool rule, so no single blocking call is still running when the daemon
judges it hung (`docs/reference/commands.md`).

Every step also carries the daemon's own command environment, so a
command a step runs resolves what the daemon's in-process commands
resolve: with `[daemon] command_path` set, its `PATH` is the daemon's
own `pf` directory, its harness binary's directory and the key's value,
each entry once (unset: the daemon's own `PATH`, inherited; a reload moves
it for the next dispatch), `PF_PYTHON` is the interpreter the daemon resolved at its
start (`[daemon] python`, else `python3` on its `PATH`), `PF_CAPSULE`,
`PF_LANE_DIR` and `PF_SHARED_DIR` are the capsule root, the lane snapshot
the step runs (`<capsule>/workflows/<lane>`) and the shared tree beside it
(`<capsule>/workflows/_shared`) — where a step's scripts run from
(docs/reference/workflow-contract.md, "Scripts") — and `PF_ROUND` is the step's round — `1` on the step's
first entry, advanced by a rewind's re-entry (and, for the gate whose
verdict routed the rewind, by that verdict); a resume, a crash
re-dispatch and an owner's answer stay in the round. `PF_STEP` and
`PF_SPAWN` name the dispatch itself — the step id and the spawn ordinal
(the spawn journal's `spawn_n`) — the identity `pf step ask` records its
question under in the capsule's `runtime-state/asks.jsonl`, so the daemon
knows which dispatch asked (a question belongs to one dispatch: a later
dispatch of the step never inherits it). The variables are
set by the daemon; a step's own copy never reaches the step.

### Writing `cli.toml` — `pf config`

`pf config` is the one supported writer of `cli.toml` and of every
`cli-<profile>.toml` (the loader, the daemon, `pf doctor` and this command
share one validator). Its scope:

| invocation | file | keys it accepts |
|---|---|---|
| `pf config …` (no profile resolved, or `default`) | `cli.toml` | the default Cell's keys and the Host-only keys (one file, two scopes) |
| `pf [--profile NAME] config …` resolving a named Cell (flag, `PF_PROFILE`, cwd) | `cli-<profile>.toml` | Cell keys only — a Host-only key is refused naming the file and the `--host` route; nothing is written |
| `pf config … --host` | `cli.toml` | Host-only keys only — a Cell key is refused naming its scope; `--host` with `--profile` is a contradictory scope |
| `pf --config PATH config …` | exactly `PATH` | the file's role comes from its name (`cli-<profile>.toml` = a named Cell); it never selects a Cell |

`pf config validate` (no `--config`) validates the whole Host set: the Host
file and each named Cell file on its own, printing every file's verdict and
exiting 1 when any is invalid. `pf [--profile NAME] config reload` asks the
Host to re-read that Cell's file; `pf config reload --host` the Host keys. It never
rewrites the file as a whole: a `set` replaces only the target line (a
whole `[workspace]` section for a section key), every other byte —
comments, blank lines, order — survives. Writes are crash-atomic (temp
file, fsync, rename; an existing mode is kept, a new file is `0600`) and
concurrent writers serialize on a file lock.

```
pf config get <key>              # effective value + source (flag|env|file|default)
pf config set <key> <value>      # set a flat key, a [daemon] key, a [summarize] key or a whole [workspace] section
pf config set '[workspace."/path"]' projects=a,b,c
pf config set summarize.language English
pf config set memory_policy.<default|project>.record|skip '["a", "b"]' --workspace /path   # or --cwd <dir>
pf config unset <key>            # remove the line (or the whole section)
pf config list                   # every key with its effective value, source, reload class and schema
pf config validate               # validate without writing
pf config reload                 # apply a hand edit to the running daemon
pf config migrate                # move retired .polyforge.yaml summarize:/memory_policy: sections into this file
```

Keys are addressed by their flat name (`server`), their `[daemon]` key
name (`max_concurrent_jobs`), their `[summarize]` key name
(`summarize.models`, `summarize.language`, `summarize.interval_sec`), a
memory_policy list (`memory_policy.<default|project>.record|skip`) or as a
whole `[workspace."<path>"]` section. The `summarize.*` keys are flat
machine-level keys and refuse `--workspace`/`--cwd`; a memory_policy key
needs exactly one of them (`--workspace` a mapped path, `--cwd` resolved
through the mapping) and writes only that path's entry, taking its value as
a JSON list of strings. `pf config get` refuses both families and points at
`pf config list`, their one read face. `api_key` is write-only: pass it on stdin
(`printf '%s\n' "$KEY" | pf config set api_key`; a tty prompts with echo
off), never on argv, and no `get`/`list`/error/event ever emits the
value. A write whose result the shared validator refuses (an unusable
workspace, an invalid `[daemon]` value, a removal that would strand an
in-flight job) leaves the file untouched and names the key. The
mapping-non-empty and workspace-usability checks apply to a write that
touches the mapping or leaves a mapping in place; a flat/`[daemon]` write
to a file with no mapping is accepted, and the daemon refuses to start
until a mapping exists.

Reload classes (one class per key):

| Class | Keys | What a reload does |
| --- | --- | --- |
| live | `max_concurrent_jobs`, `max_concurrent_bars`, `bar_load_threshold`, `command_path`, `step_timeout`, `stall_window`, `agent_language`, `claude_bin`, `summarize.models`, `summarize.language`, `summarize.interval_sec` | applied in the running daemon (the summarize lane's next call uses the new table) |
| next-claim | the `[workspace]` mapping; the whole `[memory_policy]` family | mapping additions usable at the next dispatch (a removal/re-route with an in-flight job is refused and the work items are listed); the claim that follows writes the new effective memory policy into its capsule, a running job keeps the one its claim wrote |
| connection-change | `server`, `api_key` | identity-stable: a changed `server` is refused ("create a new profile"; nothing applied); a changed `api_key` is verified with one heartbeat first — a different Hub user is refused, an unverifiable key is not taken for the same user (refused, retry later); a verified same-identity rotation is reported `pending-drain`: the Cell refuses new submits, automatic claims **and task spawns** (`DAEMON-409` naming the pending connection rotation; nothing is queued) until it is safe-idle (no job record of any state, nothing queued or in flight, no running one-off task), then switches by itself with no second reload. Each admission's pending check and its enqueue or slot reservation are one step against the switch's idle decision, so an admission is either refused or seen by the switch, which then waits. The effective and pending credential are persisted in the Cell's `effective-connection.json` (0600, never echoed); the record is written at the Cell's first start. Every assembly (Host start, `pf cell add`/`repair`) checks the file's normalized `server` against the Cell's recorded identity (`identity.json`): another server refuses the Cell with the new-profile route (an invalid default Cell blocks the Host start). A Host restart starts on the effective key and treats a differing file `api_key` as a pending change through the same identity check — a newer file value never switches the Cell's user nor bypasses the idle gate; one that is not adopted is reported on `GET /profiles`. An `api_key` removed from the file (`pf config unset api_key`, or a hand edit) is never shadowed by the persisted copy: a reload reports it `pending-restart` and on `GET /profiles`, drops a pending rotation, and the running Cell keeps its effective key until the Host restarts; the next start deletes the record and the Cell holds no credential — a named Cell is invalid (`api_key is not set`), the default Cell reads degraded naming the missing key, claims nothing and the proxy refuses its business (`DAEMON-409`); a key of the same Hub user set later is taken at once on reload (`applied`: nothing on the Hub to drain). A heartbeat answered for another Hub user than the recorded one degrades the Cell (claims stop) and the data-plane proxy refuses business on it (`DAEMON-409`) |
| restart-only | `daemon_socket`, `harness_root`, `operator_agent_dir`, `python` | reported pending; the running daemon keeps its value and a restart applies it |
| CLI-only | `user_id` | never read by the daemon |

The workspace `.polyforge.yaml` keys report the same classes on the
workspace read (`pf config workspace`): every
`steps.<project>.<step-id>.models` key is `live` (read
fresh at the next step dispatch; a running step keeps what its dispatch
read); every `steps.<project>.<step-id>.skip` key is `next-claim` (fixed
into a work item's capsule when it is first claimed; a claimed work item
keeps the set it was claimed with —
[below](#stepsprojectstep-idskip--switching-off-a-skippable-step)).

### Machine-readable list, schema and CAS

`pf --json config list` is additive: every item carries `class` (the same
class table the reload path uses, including `next-claim` for a whole
`[workspace."<path>"]` section) and `schema` (the value `kind` plus only the
constraints the shared validator actually enforces — `max_concurrent_jobs`,
`max_concurrent_bars` `{kind:"int",min:1}`, `bar_load_threshold`
`{kind:"number",min_exclusive:0}`, `step_timeout` `{kind:"duration",min_exclusive:0}`,
`stall_window` `{kind:"duration",nonzero:true,allow_negative:true}`, `command_path`
`{kind:"string"}` (the validator also requires every `:`-separated entry to be absolute),
`agent_language` `{kind:"enum",values:["en","zh-CN"]}`;
`language` has no enum). The three `summarize.*` items carry their
effective value — `summarize.models` a list (absent with no `[summarize]`
table: the lane is off), `summarize.language` / `summarize.interval_sec` the
declared value or the documented default (`中文` / `60`, `source:
"default"`) — with schemas `{kind:"model-list"}`, `{kind:"string"}` and
`{kind:"int",min:10,max:3600}`. The document also carries
`file:{path,sha256,exists}` for the CAS precondition and one top-level
`memory_policy` object keyed by mapped workspace path: per path the
`default` entry's effective lists (declared, else built-in) and every
mapped project's OWN declared lists (never pre-merged — a project's lists
append to `default`'s), each `{record, skip, declared}`; the family's
class is `next-claim`. Existing `value`/`source`/`set`/`section` fields are
unchanged and `api_key` stays masked.

`pf config set|unset` accepts `--expect-hash <sha256|absent>` and
`--expect-state <json>` for the read snapshot
`{file_sha256:<hex>|null, values:{<key>:<value>|<{set:boolean}>}}`. The
comparison runs inside the writer flock before the atomic write; a mismatch
is refused with `CORE-409` and a key-level `extra.diff`, and `api_key` appears
only as changed/unchanged. A snapshot that still matches is never refused for
an unchanged `api_key`. `--expect-hash absent` (or a `file_sha256` of `null`)
says the target file is absent. With no `--expect-hash`/`--expect-state` the
write is unconditional; `--expect-hash` alone refuses on a moved hash and
lists the file's current values.

### Workspace `.polyforge.yaml` read

`pf --json config workspace --cwd <dir>` resolves the cwd through the same
`MatchWorkspace` longest-prefix rule as every other workspace consumer and
always answers one JSON document: `mapped`, the workspace label, the yaml
`path`/`sha256`/`exists`, the per-project `steps` map and `steps_complete`.
It carries no `summarize` and no `memory_policy` (cli.toml facts since,
read through `pf config list`); a yaml still carrying either
retired section makes it error with the file, the line and
`pf config migrate`. An unmapped cwd answers `mapped:false` with exit 0.

Each `steps.<project>.<id>` entry carries `declared` + `models` (a
declared-empty list stays `declared:true` with `models: []`, never absent;
an undeclared one is `declared:false` with no list), `source` (`file` or
`default`) and `class` (`live`: the driver composes the chain at every
dispatch). When the file declares a `skip` for the step the entry also
carries `skip` (a boolean); an entry the file only gives a `skip` is listed
too, as `declared:false`, `source: default`, with that `skip`. Every mapped
project always has a `default` and a `task` row besides the installed step
ids and the file's own keys.

`pf config set|unset steps.<project>.<step-id>.models|skip` writes the
[`steps:`](#steps--model-candidates-per-project-and-step) keys with the
`--workspace <path>` or `--cwd <dir>` (exactly one; `--workspace` must name a
`[workspace."<path>"]` already mapped in cli.toml), the CAS options, the
writer flock, the shared atomic writer and the strict `workspace.Load`
preflight (before and under the flock). An absent file is created only when
the expectation says `absent`; an unset with no file to edit is a no-op.
`models` takes a list (`'["pi:a","claude:b"]'` or `pi:a,claude:b`; an empty
list is a declared-empty level), `skip` takes `true` or `false` (any other
value is refused) and is refused for a step no lane of the workspace's lane
set declares `skippable: true`
([below](#stepsprojectstep-idskip--switching-off-a-skippable-step)). The write is a
byte-preserving line edit: only the addressed line changes; a missing
`steps:` section, project or step entry is created in block style, and a
flow-style entry on the path is expanded once into block style keeping every
value it declares. A stale `--expect-hash` / `--expect-state` is refused with
`CORE-409`, and a malformed candidate is refused by the loader — the
file stays byte-identical. `unset …models` removes the chain line (`unset
…skip` the switch line), then the
step entry when it declares nothing else (so the chain falls back one
level), then a project or `steps:` section the removal left empty. Before
reading the file, a step write asks the running daemon's `GET /status`; an
unreachable daemon, or one naming no workflows dir, refuses it with
`CORE-501` (its workflows dir is what the step ids are checked against).

### The workflow read face — `pf workflows`

`pf workflows [--cwd <dir>]` is the read-only companion of the workspace
read: the installed bundles (from the daemon-reported `workflows_dir`,
parsed on every call), each step's `model_candidates`, and — with
`--cwd` — every mapped project's effective chain per
step with the level it came from, resolved by the precedence chain of
[`steps:`](#steps--model-candidates-per-project-and-step) (the
[commands page](commands.md) has the document shape and its
degradations). Its `write_capable` is the running daemon's own fact
(`write_reason` names why not).

After a successful write `pf config` notifies the running daemon; there
is no file watcher, so a hand edit needs the explicit `pf config reload`.
A bad reload applies nothing, names the offending key, emits one redacted
event (key names only) and never stops the daemon. The reload entry point
is local-UDS only and refuses step agents (`docs/reference/uds-routes.md`).

### `[summarize]` — the summarize lane

The model candidates of the demand-gated summarize lane — the one-line
narration of a running step's live transcript that a watched work
item's snapshot carries (polyforge#224). One machine-level table:
every mapped workspace's work items use it; there is no
per-workspace or per-project summarize setting (a
`steps.<project>.summarize` key in a `.polyforge.yaml` is an unknown step
id and refuses):

```toml
[summarize]
models = [
  "pi:prov/flash-model",        # tried in order on failure
  "claude:claude-sonnet-5-5",
]
language = "English"            # optional; absent = 中文
interval_sec = 120              # optional; absent = 60
```

`models` is mandatory in the table and as strict as a step entry's list in
`steps:`: a non-empty array of unique `<harness>:<model>[:<effort>]`
candidates. A table without `models`, with an empty list or with an
unprefixed candidate refuses the whole cli.toml load (every `pf` command
and the daemon; the error names the file and the line) — a claim never
meets a silently-off lane. Any key other than `models`, `language` and
`interval_sec` refuses with an error listing the three.

`language` (polyforge#243) is the narration language: the prompt asks
the model to "Answer in <language>." with the value inserted verbatim,
and to keep code identifiers, file paths and commands as written.
Absent, the narration is in 中文. The value is free text — no allowlist,
no language codes, no length cap — but must be a quoted string that is
non-empty and single-line after trimming.

`interval_sec` (polyforge#250) is the minimum number of seconds between
two summarize call starts for one work item (the clock is per work item
and does not reset on a new step dispatch). Absent, it is 60. The value
must be an unquoted integer from 10 to 3600, inclusive; anything else — a
quoted `"60"`, a float, a duration such as `1m`, `0`, a negative or an
out-of-range number — refuses with an error naming the range `10-3600`.
It is never clamped. The per-attempt cap stays fixed at 20 calls per step
dispatch, and no call starts while the previous one is still running,
whatever the interval.

The daemon serves the table from its loaded cli.toml snapshot (class
`live`): `pf config set summarize.*` notifies the daemon and the next call
uses the new value; a hand edit needs `pf config reload`. No per-request
file read happens. No table leaves the lane silently off — no call, no
error, no doctor row.

### `[memory_policy]` — the wrap-summary policy

What the workflow's last step puts into the work item's one
`experience.wrap` summary memory (no other step writes memories):
`record` lists what is worth remembering, `skip` what never is. A
path-keyed table family: one block per mapped workspace
path, each entry `default` or a project that path maps, with optional
`record` / `skip` arrays of non-empty, unique quoted strings (no other
key; at least one of the two):

```toml
[memory_policy."/path/to/workspace".default]   # every project of this path; an undeclared list is the built-in default's
record = [
  "pitfalls and their root cause",
  "the owner's rulings and agreements",
]
skip = ["transient state", "credential values"]

[memory_policy."/path/to/workspace".polyforge] # a project of this path: lists APPENDED to default's
record = ["environment differences around the daemon's rollout"]
```

The path is quoted and must be a mapped `[workspace."<path>"]` key; the
entry is a bare name (`[A-Za-z0-9_-]`) or quoted. An unmapped path, an
entry that is neither `default` nor a project of that path, an entry with
no list or another key, an empty, duplicated or unquoted item all refuse
the cli.toml load with the file, the line and the table named. Each
path's block applies only to claims of that path's projects; two paths'
blocks are never compared or merged. A path without a block means the
built-in default policy and never refuses a claim. The built-in default
(the block `pf init <ws>` writes into cli.toml for a mapped workspace
that has none — once, with its comment; an existing entry is never
rewritten): record the pitfalls and root causes (symptom, cause, fix), the
environment and external-system facts not visible in code or git, and the
owner's rulings and agreements; skip the process log, step output and full
spec/plan (they live in the work item and the capsule), what code or git
shows directly, transient state, and credential values (the block carries
these lists written in Chinese; this is an English paraphrase).
`pf doctor` warns about a mapped path without a block and points at
`pf init`. The daemon serves the family from its loaded cli.toml snapshot
(class `next-claim`): at a claim it writes the project's effective policy
to the capsule (`runtime-state/memory-policy.json`) and the driver renders
it into the last step's wrap duty; a running job keeps the policy its
claim wrote, and a hand edit needs `pf config reload`. A step whose
workflow entry declares the input `memory: recall` (the decision and authoring steps,
never a gate like review, verification or publish) is asked to recall
first.

Step agents hold no aihub credential path: inside a step `pf step recall`
/ `pf step memory show` read through the daemon (scoped to the step's own
project); `pf step remember` sends the wrap summary synchronously to the
daemon (`POST /step/call`), which validates it (type `experience.wrap`, a
title, ≤ 2 KB, no credential value), attributes it to the step's own work
item and project and stores it with an idempotency key of the step
dispatch (a retry returns the same memory); a failure is reported and
never fails the step. Every aihub verb — `pf recall`, `pf memory` and `pf
remember` included —, `pf doctor` and `pf daemon` are refused there
(DAEMON-403).

### Array values

An array value (`projects`, `models`, `record`, `skip`) may span several
lines until its closing `]`; every item is a quoted string, a comma inside
quotes is data (the item stays whole) and one trailing comma is allowed.
The parsed list is the same as the one-line form. The rest of the grammar
stays strict: escape sequences, inline tables, duplicate keys or tables,
unquoted section keys and any table outside `[workspace."<path>"]`,
`[daemon]`, `[summarize]` and `[memory_policy."<path>".<entry>]` refuse
with a line-named error.

### Moving retired yaml sections — `pf config migrate`

`summarize:` and `memory_policy:` used to be root sections of each
workspace's `.polyforge.yaml`; a yaml still carrying either is refused
whole (see [.polyforge.yaml](#polyforgeyaml)). `pf config migrate` (local
CLI, no daemon) moves them once: it reads every workspace the cli.toml
mapping names (a workspace without a yaml is skipped), plans the whole move
without writing, and then writes cli.toml first (the one `[summarize]`
table and one `[memory_policy."<path>".<entry>]` block per path, values
verbatim — the command hardcodes no value) and then each yaml with exactly
the two sections removed (the `pf init` comment block above an old
`memory_policy:` goes with it; every other byte stays). Before changing a
file it writes a sibling backup `<file>.bak-<UTC timestamp>` of the
pre-change bytes (never overwriting an existing backup; an unchanged file
gets none).

It refuses rather than picks a side, with one itemized error and no write:
summarize values that differ between workspaces or from an existing
`[summarize]` (compared as effective values — an absent `language` is
`中文`, an absent `interval_sec` 60), a path whose existing cli.toml block
differs from its yaml block (identical values are fine), an invalid
section value, or a yaml that would not load without the sections. An
interruption after the cli.toml write converges: the re-run finds cli.toml
already holding equal values and only removes the yaml sections. A run with
nothing to move prints `nothing to migrate`, changes no byte and exits 0.
There is no reverse command; rollback and the deploy order are in
[upgrade.md](../upgrade.md).

## .polyforge.yaml

The workspace-level per-project settings file at a workspace root: the
per-step settings (`steps:`) and the per-project tier lists (`tiers:`). One
strict-schema loader shared by every consumer (the daemon's pre-claim
read, `pf doctor`, the candidate-precedence compose); there is no
second parser. The daemon reads and validates it BEFORE the aihub
claim: an invalid file refuses the work item with DAEMON-414 (`pf
submit` shows it; auto-pull skips that project and logs once) and
nothing is claimed, so the work item stays queued for other machines.
An edit takes effect at the next dispatch with no daemon restart: the file
is read fresh at every dispatch — every step start, retry and resume — and
the candidate list recomposed from it over the lane's own lists, read from
the capsule's lane snapshot (the lane the claim pinned: an edit of a lane
never reaches a work item in flight);
an in-flight dispatch is never killed, and the step's current model is
located by identity in the new list (a model the edit removed falls back to
the new list head). A file that cannot be read or validated at a dispatch
blocks the work item with the DAEMON-414 error; fix the file and `pf resume`
continues from the checkpoint. A manual `pf submit --models` list stays
frozen for the work item. A switch decided by a death is announced by
the existing `candidate_switched` event at that failure (cause: the death
cause); a dispatch announces only `config_changed` or a switch it makes
itself (an owner list edit after a chain-end death). Two `ended_without_result` deaths on one candidate (the
turn ended cleanly, no envelope; rate-limit, upstream-transient and restart
entries neither count nor break the streak, an accepted result resets it)
rotate to the next candidate like a model error; at the chain end it counts
as a same-model retry, and the failure bound's block reason names the model
and the edit-the-yaml-then-`pf resume` escape hatch.

An ABSENT file and an EMPTY or comments-only file are both
absent-equivalent — never an error. A file that declares anything must
parse strictly: unknown keys at any level, duplicate keys, type
mismatches, empty model ids, a project the cli.toml mapping does not
route to this workspace, and duplicate model ids in one list are all
load errors. A per-step key that is no step id of the workspace's lane
set — the daemon's installed lanes and the workspace's own user lanes
([workflow contract](workflow-contract.md#user-lanes)) — nor `default` or
`task` (a removed step's, a typo, the retired `verify-judge`) is stale,
not a load error: it
governs nothing, the `workspace yaml` row of `pf doctor` warns naming each
one and the rewrite, and `pf config set` refuses to write one. The retired top-level `models:` section (the pre-#169
shape) refuses with a migration message; `projects:` /
`workflow_defaults:` are unknown keys like any other (upgrading from
the pre-#169 shape: [upgrade.md](../upgrade.md)). The retired root
`summarize:` and `memory_policy:` sections (now the cli.toml
[`[summarize]`](#summarize--the-summarize-lane) table and
[`[memory_policy]`](#memory_policy--the-wrap-summary-policy) family) refuse
the whole file with an error naming the file, the line and
`pf config migrate`; nothing reads either value from the yaml. YAML merge keys
(`<<`) are refused — share a list with anchors,
`models: &x [...]` / `{ models: *x }`.

### `steps:` — model candidates per project and step

```yaml
# Precedence: pf submit --models > steps.<project>.<step-id>.models > tiers.<project>.<tier>.models (a step naming that model_tier) > bundle step model_candidates (or the tier its model_tier names in the bundle's defaults.tiers) > steps.<project>.default.models > bundle defaults
steps:
  polyforge:                                # a project of this workspace
    default: { models: ["pi:prov/model-a", "pi:prov/model-b"] }  # optional: steps without a list of their own
    fix: { models: ["claude:claude-opus-5-5", "pi:prov/model-c"] }  # optional: one step id (fix, code,
                                            # review, publish, ...) — the step
                                            # with that id in every bundle
    merge: { skip: true }                   # optional: switch off a step its lane
                                            # declares skippable (below)
tiers:                                      # optional section
  polyforge:                                # a project of this workspace
    cheap: { models: ["pi:prov/small"] }    # the list of every bundle step
    mid: { models: ["pi:prov/small"] }      # naming model_tier: <tier>
```

**Tiers.** A bundle step may name a tier instead
of a list — `model_tier: cheap` — and the tier→model table has two
places: the workspace yaml's `tiers.<project>.<tier>.models` (the
project's own table, one key per tier, `models` its only key — the list's
rules are a step entry's), and the bundle's `defaults.tiers` (`tiers:
{cheap: [pi:prov/small], mid: [...]}`; a tier the bundle lacks is
`WFC-198`). For a step naming a tier the chain is: the step's own yaml key
`steps.<project>.<step-id>.models`, then the yaml tier list, then the
bundle's tier list (at the bundle step level), then the yaml project
default. Every shipped lane declares `cheap` and `mid` as placeholder
tiers (`pi:example-gateway/model-cheap`, `…/model-mid`): the real model
of a tier is the workspace's — set `tiers.<project>.<tier>.models`, or let
the project default govern. A bundle step list — a `model_candidates`
list or a tier — whose every candidate is a placeholder
(`<harness>:example-…`) is undeclared: it never outranks the project default
(`pf doctor`'s workspace yaml row warns when a resolved
step's first candidate is a placeholder). A tier name no bundle step names governs nothing.


Every candidate — here, in a bundle's `model_candidates` and in `pf
submit --models` — is `<harness>:<model>[:<effort>]` with harness `pi`
or `claude` (polyforge#175; split on the first `:`, the prefix mandatory;
no prefix, an unknown prefix or an empty model refuses). A chain may
mix harnesses; a switch onto a candidate of another harness starts a
fresh session with the previous transcript as read-only reference. When
the current capsule tree hash equals the hash recorded on the
step's latest failed attempt and the capsule is still the claim that
recorded it (a re-claim, whose fresh materialization did not restore the
ignored build directories, keeps the from-scratch reference), the
reference keeps its transcript path
but orders continuing in place (the tree is byte-identical, so completed
environment setup is not redone). A mismatch, a missing hash, a re-claim or
an unreadable tree keeps the from-scratch reference. A
step with no list (or an empty one) runs pi's own default model. The
optional effort (polyforge#176) is the last segment only when it is one
of `off minimal low medium high xhigh max` — `pi:ollama/llama3:8b`
keeps `:8b` in the model id; pi gets it as `--thinking <effort>` (all
seven), claude as `--effort <effort>` (`low`…`max`; `off`/`minimal`
refuse); no effort, no flag.

A step entry's keys are `models` and `skip`, at least one declared (an
entry declaring neither is refused as empty); a leftover `attrs` is refused naming
its removal. An entry declaring only
`skip` is no level of the chain below. For a step of the claimed project
the list is, highest first: the work item's manual `pf submit <id>
--models pi:a,claude:b` list (every step; kept by this daemon only —
across its restarts and `pf resume` — and a re-claim on another machine
uses that machine's settings), else the yaml list keyed by the step's
id, else the workflow bundle's step-level candidates, else the project's
`default`, else the bundle's `defaults` (a declared-but-empty yaml list
governs: no switchable chain). A project not listed keeps the bundle's.
`pf doctor` reports the same refusal (an unknown per-step key and a
leftover `models:` section included) before any claim. Repo lists and
clone URLs always come from the aihub project registry.

### `steps.<project>.<step-id>.skip` — switching off a skippable step

```yaml
steps:
  polyforge:
    merge: { skip: true }    # develop ends after publish; its PRs await a human review
```

`skip: true` switches the step off for the project's work items — only
where the step's lane declares it `skippable: true` (workflow.yaml,
[workflow contract](workflow-contract.md#step-keys)); a step not declared
skippable always runs. The shipped lanes declare one: develop's `merge`.
`skip` is a bool (`true` or `false`; anything else is a load error) and
`default` and `task` take none (they are no step).

- **Class `next-claim`.** The set of skipped steps is fixed when a work item
  is first claimed: the claim writes it into the capsule
  (`runtime-state/skipped-steps.json`), and the work item keeps it through
  a re-claim and `pf resume`. Switching it changes the work items claimed
  after the change, never a running one — unlike `models`, which is read at
  every dispatch.
- **Written through `pf config set|unset steps.<project>.<step-id>.skip`**
  (the write above). A write for a step
  no lane of the workspace's lane set declares `skippable` is refused,
  naming `skippable: true` and that such a step always runs; nothing is
  written.
- **Written by hand on another step, it is ignored.** The loader has no
  lane at hand: a `skip: true` a hand edit puts on a step its lane does not
  declare skippable loads, governs nothing, and is ignored when a work item
  is claimed.
- **Per lane, by step id.** The key names a step id, as `models` does, and
  takes effect only in the lanes that declare that step skippable: with
  `steps.<project>.merge.skip: true` develop's `merge` is skipped, while
  micro's `merge`, which declares no `skippable`, runs as usual.

A develop work item whose project skips `merge` ends after `publish` with
its PRs open, awaiting a human review, and is wrapped `integration: {state:
published, …}`, never `landed` (`workflows/develop/README.md`, "Skipping
merge"); `pf workflows --cwd` reports each step's `skippable` and the
projects that skip it ([commands](commands.md)).

### `steps.<project>.task` — the one-shot task agent

```yaml
steps:
  polyforge:
    task: { models: ["pi:prov/model-a", "claude:claude-sonnet-5-5"] }
```

`task` is a recognized, **bundle-less** step id (the
re-established #40): it belongs to no workflow bundle and is never part of
a step's precedence chain. Its only key is `models`, with the same
candidate shape and strictness as any other step entry; any other key — a
budget key included, e.g. `max_tokens` — refuses as an unknown key
(DAEMON-414).

The task lane resolves its candidates, highest first: the
`steps.<project>.task.models` list when declared, else
`steps.<project>.default.models`. When **neither** is declared the spawn
is refused with a typed error naming both keys
(`steps.<project>.task.models` / `steps.<project>.default.models`);
there is no hardcoded model. A declared-but-empty list is a present
level (it governs and dispatches nothing) rather than a fall-through.
The list is read live at every spawn — an edit governs the next task
with no daemon restart and nothing is snapshotted. Budgets are **not**
configurable here: `--max-duration` / `--max-tokens` are spawn flags
with fixed defaults and code-constant ceilings. A project that declares
no `task` block and never spawns a task is unaffected. The lane carries no
sandbox key either: a task agent's filesystem/network trust equals a
session agent's ([commands](commands.md)).

### A step's agents — under the step's own id

```yaml
steps:
  polyforge:
    verification: { models: ["pi:prov/model-a", "pi:prov/model-b"] }
```

Every step runs on its own key: the manual `pf submit --models` list,
else `steps.<project>.<step id>`, else the step's `model_candidates`,
else `steps.<project>.default.models`, else the bundle's
`defaults.model_candidates`; the first candidate runs, read
fresh at every dispatch. Develop's verification runs `verify.py` on the
lane's `cheap` tier, overridden by its own key or the project default like
any step; the integration lane's four steps (`collect`, `revert`,
`full-gate`, `close`) each run on their own key — their lane tiers are
placeholders, so the step's key or the project default governs — and
dispatch no sub-agent. The retired `verify-judge` key is stale: it governs
nothing, and `pf doctor` names it — rename it to `verification`.

### `integrate:` — removed

The daemon no longer files integration batches on its own: a batch is
started only by a person, with `pf file create --bundle integrate --project
<p>` and `pf submit` ([commands](commands.md),
[concepts](../concepts.md#the-integration-lane)).
The `integrate:` section that switched the daemon's automatic batches
(`auto`) and set their quiet period (`quiet`) is gone. A file still
carrying it — any entry, alone or beside `steps:` and `tiers:` — is
refused whole, like any load error (DAEMON-414 at a claim or a dispatch,
`pf doctor`'s `workspace yaml` row), the reason reading: `integrate:`
removed: the daemon no longer files integration batches on its own — a
person files a batch with `pf file create --bundle integrate --project <p>`
and `pf submit`, so `auto` and `quiet` govern nothing; delete the whole
`integrate:` section

## `.polyforge/verify-scope.conf` — the verification bar

A repository's declarative verification bar, committed in the repository and
read from its root by the release-shipped engine `workflows/_shared/scripts/verify-scope`. The
tier semantics — the analyzers, the scoped selection, the report and its JSON
form, `--run` — are in [contributing's Verification
tiers](../contributing.md#verification-tiers); this table is the key list.
The file is line-oriented: one `key=value` per line, leading whitespace
ignored, `#` comment lines and blank lines ignored, and a value runs to the
end of its line (there is no trailing comment on a value line). It is never
sourced or evaluated; an unknown key or a malformed line is diagnosed on
stderr and contributes nothing.

| Key | Value | Meaning |
|---|---|---|
| `stack` | `go`, `npm`, `python`, `rust` or `none` | Pins the analyzer (the first valid pin wins); it also resolves a repository holding two or more supported manifests. `none` pins no analyzer: always the full tier, the conf's commands only. |
| `go_harness` | path | The Go acceptance harness that owns its directory; the scoped selection runs it for the tests that reference a changed path. |
| `fast_gate` | command | Repeatable, ordered: an anonymous fast-phase command, named `fast-<i>` (the i-th `fast_gate` line). |
| `full_command` | command | Repeatable, ordered: an anonymous full-phase command, named `full-<i>`. |
| `check` | `<name>\|<fast\|full>\|<command>` | Repeatable: a named command of that phase, in file order among the phase's lines. |
| `check_requires_env` | `<name>\|VAR[,VAR]` | The named command needs these variables set and non-empty. |
| `check_requires_cmd` | `<name>\|bin[,bin]` | The named command needs these executables: a bare name is looked up on `PATH`, a name containing `/` is resolved against the repository root. |
| `check_timeout` | `<name>\|<duration>` | The named command's timeout, a positive Go duration (`90s`, `20m`, `1h30m`); the first one per name wins. Without one the JSON report's `timeout` is `null` and the consumer's own default applies. |
| `report` | `<name>\|junit\|<path>` | The named command writes a JUnit XML report at `<path>`, a repository-relative path or glob (never absolute, blank or `..`-escaping); the first one per name wins. Echoed on the command's JSON entry as `report: {format, path}` (`null` without one). After a failing run the verification bar reads the report files the command created or changed (stamped before it started, so a file another command sharing the glob wrote is not its report) and adds each failed testcase the output's units do not already cover as a located failure unit (named `<classname>::<name>`, its file from the XML); a report that is missing, unchanged, unparseable or names no failure leaves the output parsing as it is, noted. `junit` is the only format. |
| `rerun` | `<name>\|<template>` | The named command's unit re-run template: a `bash -c` command run from the repository root, containing `{unit}`, which the verification bar replaces with a failing unit's name (an aggregate runner's suite: `e2e/<suite>`), shell-quoted, to re-run that unit alone — the flaky re-run at HEAD and the pre-existing run on the base. It must run the unit exactly as the command does. The first one per name wins; one without `{unit}` or naming no declared command is diagnosed and dropped. Echoed on the command's JSON entry as `rerun` (`null` without one). |
| `host_requires` | `<name>\|<probe>\|<hint>` | Repeatable: a host prerequisite of the repository's commands — a declaration only, bound to no command, changing no tier, `--run` or `--micro` (a command missing it still fails). Split at the first two `\|`: `<name>` has the `check` name shape, `<probe>` is a bash command that exits 0 when the host has the prerequisite (it cannot contain `\|`), `<hint>` is the rest of the line, kept verbatim — the install instruction. Fewer fields, a bad name, a blank probe or hint, or a probe beginning with `$(` or a backtick is diagnosed and dropped; the first one per name wins. Echoed as `host_requires: [{name, probe, hint}]` (file order), only when one is accepted. |
| `forced_full` | glob | Repeatable: a changed path matching it forces the full tier — except on the light tier (`--light`, the develop lane's verification), where it forces nothing. |
| `module_root` | path | Repeatable: the Go spanning roots (a diff spanning two forces the full tier, except on the light tier). |
| `exclude` | glob | Repeatable: a path the full-tier triggers ignore; a matching file is never a carries suite, and a changed matching path never forces full. |
| `test_glob` | glob | Repeatable: a test-file pattern of the repository (echoed in the JSON report; the verification bar and the develop lane's build settle classify test files with it, the settle reading the conf at the change's base). |
| `base_setup` | command | Repeatable, ordered: the setup a rerun on a base tree needs first (echoed). `facts.py build --replay` runs them too, from the root of a unit's replay worktree, when its run could not run there (`not_runnable`), then replays the run once more; a failing line leaves the unit `not_runnable` (rule `base-setup`). |
| `runner` | `complete` | Declares that the conf's commands are the complete bar (echoed). |
| `e2e_runner` | command | The command that runs the carries-selected suites: the engine appends them to it, repository-relative and in stable order, as one scoped command (`e2e_runner=npm run test:e2e:all --`). The file it runs — through the npm script when the value is `npm run <script> …` — is a runner, never a suite; it is not exempt from coverage: a change to it that no `carries:` declaration (or `e2e_map=` glob) covers forces the full tier like any other uncovered path. Without the key each suite gets the analyzer's default command (`node <suite>`, `pytest <suite>`, `cargo test --test <name>`). Echoed as `e2e_runner` (`null` when undeclared). |
| `suite_glob` | glob | Repeatable: replaces the analyzer's suite discovery for the repository (the undeclared-suite and selection rules then apply to the files it matches). Echoed as `suite_glob`. |
| `e2e_map` | `<glob>=<suite>` | Repeatable: declares `<glob>` as the coverage of the repository-relative `<suite>`, replacing that suite's `carries:` header outright (not merged); several lines for one suite union. A `<suite>` that is not a discovered suite forces the full tier. Echoed as `e2e_map`. |
| `smoke_suite` | suite path | Repeatable: a repository-relative suite the full-tier e2e selection always runs. Declaring at least one is the repository's opt-in: full-tier verification (`--full`) of a non-empty diff then runs the fast gates plus the carries-selected suites union the smoke suites (only the smoke suites for a documentation-only diff), lists an uncovered non-documentation path instead of forcing the full tail, and runs the full tail on a forced-full match, a change to this conf or a structural diagnostic ([contributing's Carries](../contributing.md#carries--declaring-what-an-e2e-suite-covers)). The full-phase commands stay the carried full bar delivery re-runs. The scoped tier ignores the key. Echoed as `smoke_suite`; the selection as `e2e_selection`. |
| `micro_path` | path pattern | Repeatable: a repository-relative path the `micro` lane may change. Declaring at least one is the repository's micro opt-in; a repository that declares none is refused by that lane. An absolute, `./`-prefixed, blank or `..`-escaping value is diagnosed and contributes nothing. Echoed as `micro_paths` (see the micro paragraph below). |
| `micro_check` | command | Repeatable, ordered: an extra micro-lane evidence command, named `micro-<i>`, run after the fast phase only under `--micro`. Echoed as `micro_checks`. |

Command values — `fast_gate`, `full_command`, the command of a `check`, and
`base_setup` — are bash: `--run` executes them with `bash -c` from the
repository root, and a value that begins with `$(` or a backtick is skipped
with a lint diagnostic. A phase's conf lines, anonymous or named, replace the
analyzer's default commands of that phase. A `<name>` is letters, digits,
`.`, `_` and `-`, starts with a letter or digit, is declared once, and is
never of the form `fast-<n>`, `full-<n>`, `scoped-<n>` or `micro-<n>` (the
anonymous and analyzer commands' names), nor `audit` or `audit-<anything>`
(reserved for the engine's dependency-audit gate: such a `check` line is
diagnosed and ignored). A `check_*` or `report` line names a declared
command — a `check` name or an anonymous `fast-<i>` / `full-<i>` / `micro-<i>`
— and may precede it;
one naming no declared command is diagnosed and ignored. The four carries
keys fail closed instead: an empty `e2e_runner=` (or a second one, or one that
begins with a command substitution), an empty `suite_glob=`, an `e2e_map=`
value that is not split by exactly one `=` into two non-empty sides, and a
`smoke_suite=` value that is empty or not one relative path without blanks are
diagnosed, naming the line, and force the full tier for every diff — never a
scoped tier computed as if the key were absent (for an opted-in repository the
full-tier e2e selection then falls back to the full tail). An engine that predates the
keys diagnoses them as unknown and ignores them, so a conf may declare them
before the release ships the engine. Glob values (`forced_full`, `exclude`,
`suite_glob`, `e2e_map`, `carries:`) are shell-case patterns: `*` crosses
`/`. The workflow's readiness check
(`workflows/_shared/scripts/scope_ready.py`) reads this file (never
executes it).

The two micro keys are the `micro` lane's declaration and its own glob
grammar: a `micro_path` pattern is case-sensitive and slash-separated, `*`
matches within one path segment, `**` crosses `/` (zero or more segments), and
a pattern with no wildcard matches exactly that path. `micro_check` is a
command value like `fast_gate`. The governing declaration is the one at the
diff base (the fetched origin default branch), never one the change itself
introduces — the driver reads it there (`git show`), and `workflows/_shared/scripts/verify-scope
--micro` does the same. `AGENTS.md` and `CLAUDE.md` (any directory) are out of
bounds for the lane whatever a pattern matches. A repository that declares
neither key keeps the JSON report's key set unchanged.

No key of this file removes, replaces or weakens the dependency-audit gate the
engine appends to every full-tier report ([contributing's Verification
tiers](../contributing.md#verification-tiers)): a phase's conf lines replace
only the analyzer's commands, the reserved `audit` name cannot be declared or
bound by a `check_*` line, and `exclude=` does not narrow the audited trees.
The gate's only exemption is the ledger below.

## `.polyforge/audit-exemptions.conf` — dependency-audit exemptions

The per-repository exemption ledger of the dependency-audit gate
(`workflows/_shared/scripts/audit-deps`), committed in the repository and read from its root — the
top of the git work tree — on every full-tier run, a nested engine root's
included (its audit command names the file as `--ledger`, relative to that
root); its path is fixed (no key selects another file, and a file of that
name under a nested root is never read). An absent file exempts nothing.
Line-oriented like the verification conf: `#` comment lines and blank lines
are ignored, leading and trailing whitespace is ignored, and every other
line is one row:

```
exempt=<advisory>|<package>|<reason>|<owner-ruling>|<from>|<expires>
```

| Field | Value |
|---|---|
| `advisory` | the advisory id: `GHSA-xxxx-xxxx-xxxx` (npm, the GitHub Advisory Database) or `GO-YYYY-NNNN` (Go, vuln.go.dev) |
| `package` | the npm package name or the Go module path (`stdlib` for the Go standard library) the finding names |
| `reason` | why the finding is accepted for now |
| `owner-ruling` | the reference to the owner ruling on record that grants the row |
| `from` | `YYYY-MM-DD`, the day the row takes effect (not in the future) |
| `expires` | `YYYY-MM-DD`, no earlier than `from` and at most 90 days after it |

A row has exactly six `|`-separated fields, every one non-empty (a `|` inside a
value makes the row malformed). A finding is exempt only when a valid row names
its advisory and package (the tree is not part of the key: one row covers the
advisory in every tree of the repository) and today (UTC) is no later than
`expires`. A malformed row — a wrong field count, an empty field, an advisory
that is not a `GHSA-`/`GO-` id, an unparsable date, a future `from` or a
window over 90 days — is never honoured and fails the gate (`ledger-invalid:`).
A row past its expiry is never honoured (`ledger-expired:`; the finding it
covered fails the gate again). A valid row that matches no current finding is
reported `stale:` and never fails the gate — remove it. Adding a row or
extending an expiry needs an owner ruling on record; a step agent never adds or
extends one on its own initiative. The file is projected like the rest of the
tree, so `reason` and `owner-ruling` stay project-neutral.

## `.polyforge/integrate.conf` — the daemon's mechanical merges

A repository's default branch is merged into a work item's branch by the
`sync` step of develop and micro (`sync.py`, before the light verification
or micro's evidence — `merge.py` merges nothing into the branch: a moved
default branch that conflicts is the forge's refusal and a FAIL back to
`sync`). A
conflict this file declares mechanical is resolved by the command; every
other conflict is a real one — at `sync` the sync agent's (resolved in
place, then `sync.py --continue`), at `merge` a FAIL back to
`sync` ([concepts](../concepts.md#landed-awaiting-the-full-gate)). The
integration lane's revert (an owner's `revert` ruling) merges its revert PR
by the same rules. The file is read as the default branch carries it, never
from the branch being merged (a change cannot widen its own resolution); an
absent file declares nothing.

```
append_only=docs/upgrade.md
pin=docs/upgrade.md test/w254_docs_set_acceptance_test.go
list=test/w254_docs_set_acceptance_test.go // upgrade-sections:begin upgrade-sections:end
pin=<pinned path> <holder path> projected <list path>
```

- `append_only=<path>` — a file every change only appends to. When both
  sides appended past their merge base, the resolution is the default
  branch's file followed by the branch's appended bytes (the sections land
  in merge order); a side that rewrote a byte of the base is a real
  conflict.
- `pin=<pinned> <holder> [projected <list>]` — `<holder>` carries a hash of
  `<pinned>`. Once `<pinned>` was resolved, every occurrence of either
  side's hash in the holder becomes the resolved file's — the sha256 of its
  bytes, or, `projected`, the sha256 of the file as the projection publishes
  it under `<list>`'s `## REWRITE` rows (fork-only blocks dropped, then each
  row applied in order).
- `list=<holder> <comment prefix> <begin marker> <end marker>` — `<holder>`
  keeps a list between the line `<comment prefix> <begin marker>` and the
  line `<comment prefix> <end marker>`, one whole `<comment prefix> ` line
  per entry, and each change appends one line at the end of it. The
  convention is the repository's: here, each `docs/upgrade.md` append adds
  one line at the end of the list in the w254 pin's comment.

A pin or list holder's conflict is settled hunk by hunk, by two rules
only: a hunk whose sides differ only in the declared pins' hashes takes the
default branch's side (the pins are then recomputed), and a hunk inside a
declared list's region where both sides only inserted whole lines carrying
the list's comment prefix at the same point takes the default branch's
lines, then the branch's. Nothing is ever spliced inside a line, and no line
is taken for a comment by its look: a hunk where a side changed a line, or
inserted one outside a declared region or without its prefix, is a real
conflict.

A pin's inputs are untrusted: the pinned file and the list are the merge's
own versions, the branch's bytes included. No pin runs a command: the hash
is computed inside the daemon, and a `projected` list's rows are parsed
into a closed grammar of text substitutions — `[/<regex>/]s<d><regex><d><replacement><d>[g]`,
`/<regex>/d`, and `/<regex>/{s…;s…}` — and interpreted, never handed to
sed. A row outside the grammar (an `e`, `r`, `w`, `p` or `y` command or
flag, a case conversion, a backreference, …) refuses the projection: the
pin cannot be recomputed, so its holder is a real conflict, the refused
row named in the sync record. So does every form on
which the interpreter and sed would answer differently — an empty regex
(sed reuses the last one), `{,n}`, a reference to a group the regex lacks,
an equivalence class, invalid UTF-8, a POSIX class applied to a line with a
non-ASCII character the class may hold in a UTF-8 locale — and a projection
past its bounds: more than 1000 rows or commands, a row over 4 KiB, a line
growing past 64 KiB, or an output past four times the file and 1 MiB (at
most 64 MiB).

Every merge and revert the daemon makes runs with the in-tree merge
attributes overridden (`* merge=text` in the clone's
`$GIT_DIR/info/attributes`, for that merge only: the develop lane's merges
leave the clone's own file as they found it): a branch's `.gitattributes`
naming git's built-in `union` driver never merges a real conflict. A task
repo that is a linked git worktree (its git dir is not its common dir,
whose `info/attributes` every worktree shares) is refused by `sync`.

`#` lines, blank lines and HTML comment lines are ignored. The conflict
resolver takes its model like a step, from the `sync` step's list
(`steps.<project>.sync.models`, else the step's own `model_candidates`);
the integration lane dispatches no sub-agent: its fix tickets and the
owner's rulings are its steps' own `pf step hub` writes and `pf step
ask`s, on the steps' lists.
