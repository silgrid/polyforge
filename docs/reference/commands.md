# Commands — the `pf` command reference

The index of the `pf` CLI's verbs and the pointers to the surfaces that
own each command's exact shapes. This page restates no flag table: the
authorities are `pf help` (the registry), `pf help <verb>` (one verb's
usage) and the routing skill's fragments
([`skills/polyforge/verbs/`](../../skills/polyforge/verbs/) — each verb's
success and failure shapes). The concepts behind the verbs (data plane
vs execution plane, work item, claim) are defined on the
[concepts page](../concepts.md).

## The invocation shape

```
pf [--json] [--config PATH] [--server URL] [--api-key KEY] [--socket PATH]
[--user ID] <verb> ...
```

Global flags come before the verb; `pf --version` prints the build
version. `pf help` prints the registry; `pf help <verb>` one verb's usage
line; `pf help extensions` the one-line index of the
[extension points](../extending.md). Exit codes: 0 success; 1
client-class (4xx, transport, local validation, and a sub-action's
flag/argument mistake); 2 server-class (5xx), an unknown verb, or a flag
error caught by the global / verb-level flag parse.

## The verb index

One row per group, exactly as `pf help` prints it:

| `pf help` group | Verbs |
| --- | --- |
| Data-plane verbs (direct to aihub) | `file`, `queue`, `cancel`, `recall`, `remember`, `memory`, `msg` (`send`, `list`, `ack`, `show`, `analyze`, `contacts`) |
| Execution-plane verbs (local daemon UDS face) | `submit`, `watch` (snapshot, resident watcher, `subscribe`/`unsubscribe`/`adopt`/`list`), `answer` (the reserved answer `cancel` abandons the work item, authorized like `pf cancel`), `revise-binding` (the owner's one-pass correction of done `e2e-author` binding commands, `--correction <index>=<command>` repeatable; appended to the capsule chain), `pause`, `resume`, `wrap`, `ops` (`status`, `models`, `auto`, `max`, `shutdown`, and `end-session` — aihub-direct, admin/maintainer), `chat init`, `task` (`spawn`, `list`, `show`, `cancel`), `result submit` (inside a workflow step only: its one result-delivery path), `verify-run` (a verification step's one mechanical run; local, below) |
| Management face (M1–M5) | `users`, `api-keys`, `projects`, `machines` (`list`, `rekey`), `backup` (database snapshot, see below), `config` (the one `cli.toml` writer and the daemon reload entry) |
| Diagnostics | `doctor` (`--prune-scratch` removes stale `scratch/` entries; `--prune-git-dirs` removes terminal work items' external git dirs and set-aside scenes past the 14-day retention that hold no uncommitted or unpushed task-repo work — a commit counts as pushed when either the task clone's own or the mapped workspace's `.repo/<name>` clone's remote-tracking refs reach it; a scene holding such work, and the work item's git dirs, are kept and named), `version` (the verb form of `pf --version`), `init` (a workspace's local layout) |
| Process | `daemon` (run the work machine; `pf daemon ensure` starts it) |

## `pf msg` — user-to-user messages

`pf msg send --to <user> --text <text> [--about-wi <wi>] [--reply-to <msg>]`
posts one direct message; the first message to a user creates the one 1:1
conversation, and every later message reuses it. `pf msg list
[--conversation <id>] [--after <seq>] [--before <seq>] [--limit N]` lists
the conversations the caller may see (a member its own; an admin every
conversation, as before) or one conversation's permanent history; `--after`
pages forward and `--before` pages backward (the page immediately below the
cursor, ascending). Each conversation row in the `--json` list payload
carries `member_names`, the display-name mapping for exactly that
conversation's members, passed through unchanged from the server. `pf msg ack <conversation>
[--seq N]` advances only the caller's read cursor — without `--seq` it reads
the newest sequence first, a repeated/backward ack is a no-op, and `0`/
negative or past-the-newest is `CORE-400`. `pf msg show <id>` reads one
message; `pf msg analyze <id>` asks the local daemon for the explicit,
read-only, budget-capped analyzer run (conclusion stays on the caller's
machine). `pf msg contacts` lists the people the caller can message: the
union, deduplicated by user id (first display name wins, mapping
declaration order), of the aihub project member face
(`GET /api/v1/projects/{id}/members`: human co-members other than the
caller, id + display name only, gated by the project read) over every
project the `cli.toml` `[workspace.*]` mapping names, read at call time (no
daemon restart). It issues one read per project, at most 4 at once, 15 s
each, no retry; an unreadable project contributes nothing and the verb
still exits 0, and no mapping answers `{"contacts":[]}`. Its `--json` is
that assembled `{"contacts":[{"id","display_name"}]}`.
Send/list/ack/show/contacts are data-plane verbs and are refused inside a
workflow step; `analyze` is an execution-plane daemon POST and is refused
there too. The daemon advances the operator's separate delivery cursor as it
delivers; delivery never clears unread, and none of the ack, the delivery
or the project member route is reachable by a step agent (a step holds no aihub key). `--json` is the server/daemon payload passthrough (assembled by pf for `contacts`). Messages
address human users only — a chat id, WI, machine or broadcast is not an
address; secret-shaped content is refused before storing (`CORE-422`), text
over 4096 bytes is `CORE-413`, and the per-sender flood cap is `CORE-429`.

`pf machines rekey <machine> [--public-key <base64>|--key-file <path>]`
(admin only) rebinds a machine's Ed25519 public key; without a source it
reads the local `<data-dir>/machine-key` seed. `pf chat init ... --force`
is the operator's explicit interactive supersession move (`pf machines
list` now shows ROLE and LAUNCHER).

`pf backup` writes a consistent snapshot of the database (`VACUUM INTO`)
under the server's data dir `backups/`, on the same disk. Memories live
entirely in the database, so the snapshot holds every memory; it does
NOT include the git repos (`git/`). For a full backup take the sqlite
backup-API snapshot of `aihub.db` (not a plain file copy of a live WAL
database) and rsync `git/` alongside it, as `deploy.sh` does. There is
no backup-only mode: `deploy.sh`'s online backup (snapshot, rsync
catch-up, consistency check, off-host copy) runs only as part of a
deploy, so an ad-hoc full backup is these manual steps (or a copy of the
whole data dir while aihub is stopped).

`pf projects delete <id-or-name> [--yes]` (`DELETE /api/v1/projects/{id}`,
admin only) hard-deletes a project and everything it owns — work items, events,
dependency edges, memories, memberships, revoked project-scoped api keys and
the per-WI bare git repos on the server — in one transaction. It is refused
(CORE-409) while any work item is not terminal (wrapped|failed|canceled) or any
unrevoked api key is scoped to the project; an admin key scoped to another
project is refused (AUTH-403), an unknown project answers CORE-404. The
delete is irreversible and takes no automatic backup: run `pf backup` first
(and copy the server's `git/` directory if the bare repos must be kept — the
backup does not include it), then revoke any project-scoped key. Each
performed deletion is recorded in the `admin_audit` table (actor, time,
project id/name, the deleted work-item ids), which survives the cascade and
has no read API — inspect it with sqlite directly. On a terminal the CLI
prompts for the project name after a dry-run preview; without `--yes` on a
pipe it refuses outright.

Human moves: continue = `pf resume <id>` (a paused or blocked
work item), abandon = `pf cancel <id>` (queued, paused or blocked),
owner's failed verdict on a live run = `pf wrap <id> --fail` (refused on
a blocked work item), done for a work item with no bundle (design,
decision, tracking) = `pf wrap <id> [--fail] [--note …]` (aihub records
it; no daemon needed). These run with the human's own api key: aihub's
door checks the work item's reporter, a project maintainer or an admin.
The shapes are the verbs' own fragments.

## `pf task` — one-shot task agents (the re-established #40)

```
pf task spawn --project <p> [--max-duration D] [--max-tokens N] "<prompt>"
pf task list [--json]
pf task show <id> [--json]
pf task cancel <id>
```

A task is ONE fresh harness agent started from a free-text prompt (no session
reuse, no resume, no retry). `spawn` prints the task id (`task_` + 16 hex)
at once; the task runs in the background, so poll `pf task show <id>`
(`--json` re-emits the daemon's route document unchanged: the metadata plus
the report). The `--json` documents of `task list` and `task show` carry
durations as integer **nanoseconds** in `max_duration_ns` and
`wall_consumed_ns`; the field name states the unit, while the human output
keeps Go durations (`20m0s`). Each `task list --json` row carries
`prompt_summary` — the prompt's first non-empty line, at most 120 code points
(empty for an empty prompt) — and no full prompt or report; `task show`
carries both. Each row also carries `reporter` and
`report_tail`: `reporter` is the display name of the
spawning caller's self-asserted user id (`PF_USER_ID`, else `cli.toml`
`user_id` — the same trust as a work item's `origin.owner`, not an
authenticated identity) as a human member of the task's project, resolved
once at spawn and stored as a snapshot (a later rename or an aihub outage
does not change it; `list` never asks aihub), else `machine`; `report_tail`
is the report's last non-empty line, trimmed, at most 200 code points (a
longer line is `…` followed by its last 199), `""` while the task runs or for
an empty report — never the error text and not masked. The text table keeps
its four columns.

- **Trust: the same as a session agent's.** A task agent runs with the
  daemon user's own filesystem and network reach — there is no OS-level
  sandbox, no read-only mount, no masked home (owner ruling 2026-10-26; the
  platform-level non-root design that would cover every agent class is
). The harness runs in the selected project's workspace
  directory, so its clones are right there; it may read and write anything a
  human's session may. The harness tool allowlist (read/search, Bash and the
  web tools) is the lane's own scope, not a security boundary.
- **The one capability gate is the write-verb guard.** The task process is
  spawned with `PF_TASK_ID` and `PF_DAEMON_ID`, so the daemon refuses it
  `DAEMON-403` on EVERY mutating request to its Unix socket (spawning or
  cancelling a task, `/step/result`, job lifecycle, shutdown included) and
  `pf` refuses it every write verb locally before any config read or network
  call. Its reads stay open: `pf recall` and `pf memory show` go through the
  daemon's `/task/memories` face, scoped to the task's own project, so that
  face needs no aihub key. The aihub-direct verbs (`file create/edit/dep`,
  `remember`, `msg send`) never reach the daemon: the spawn env carries no
  aihub key, and `pf` clears the key in task mode and refuses those verbs
  locally. That local refusal is an **accident guard, not a boundary** —
  `HOME` is inherited, so `cli.toml` (and its `api_key`) stays readable
  under it, and a task process that reads the operator's config or speaks to
  aihub directly is outside what this lane claims. Like the step guard, the
  daemon keys on the process tree and environment, so `setsid` plus
  `env -i` leaves it: it stops a model's ordinary mistakes, not a determined
  attacker.
- **Selectors.** `--project` takes a mapped project NAME; a filesystem path
  is refused, naming the selector.
- **Models.** `steps.<project>.task.models` (the only key under `task`), else
  `steps.<project>.default.models`, else the spawn is refused naming those keys.
  There is no `--models`, `--wait` or `--type`; each is refused.
- **Budgets.** Wall clock 20m (ceiling 60m) and 300000 tokens (ceiling 1000000),
  hard-killed on crossing; a larger `--max-duration`/`--max-tokens` is refused
  naming the ceiling. No yaml key changes them. The token budget counts
  **fresh tokens** per model call — uncached input, cache creation/write and
  output. Context the harness re-sends and the provider serves from its prompt
  cache (a cache read) was already paid for once and is not charged again, so
  the budget tracks the work a task does, not the turns x context it resends.
- **States.** `running`, then exactly one of `completed`, `capped` (a budget was
  hit; the partial report is kept and marked `truncated`), `failed` (every
  candidate errored) or `interrupted` (`cancel` or a daemon restart; never
  resumed).
- **Limit.** At most 2 tasks run per daemon (outside `max_concurrent_jobs`); a
  third spawn is refused, never queued.
- **Reports** live only on the machine (`<data-dir>/task-reports/`): the newest
  50 and the last 7 days. Nothing is sent to aihub or memory. A task spawned
  from a chat delivers a one-line summary (trailing 2000 chars plus
  `pf task show <id>`) through the resident `pf watch --chat` watcher.
- Inside a task, `pf recall`, `pf memory show` and `pf watch <wi> --once`
  work; every other `pf` verb is refused with `DAEMON-403`. The refusal is an
  accident guard, not a boundary: no aihub key is placed in the spawn env and
  `pf` clears it in task mode, but the shared `HOME` keeps `cli.toml` (and
  its `api_key`) readable.
- `pf ops status` carries one line: `tasks: <running>/2 running (completed N,
  capped N, failed N, interrupted N)`.

## `pf verify-run` — the verification step's mechanical run

```
pf verify-run [--tier full|scoped] [--deadline D] [--engine <path>] [--base <rev>]
[--out <dir>] [--max-parallel-repos N] [--timeout D] [--budget D] [--e2e-jobs N]
[--gomaxprocs N]
```

The `verification` step calls it exactly once, in the foreground, at the
capsule root (the working directory); it is a local command — it never
reaches the daemon or aihub and needs no `cli.toml`, so a step may run it.
In each task repo it aborts a merge an interrupted run left in progress,
runs `git fetch origin` and `git merge --no-edit origin/<default>` (a
conflict is aborted and the bar runs on the unmerged tree), and skips a repo
with no commit beyond its default branch. For each touched repo it asks the
release-shipped scope engine (`--engine`, default
`<data-dir>/release/current/tools/verify-scope`) for its plan — passing the
engine's `--full` on the full tier — and runs the WHOLE plan through
`bash -c`, never stopping at the first failure; a command whose declared
prerequisite is missing is skipped (a required command naming a path, such
as `./bin/tool`, is looked up in the repo, any other name on `PATH`). Each
command runs with a private
`TMPDIR` and a timeout (the plan's, else `--timeout`, default 20m); a
command's process group dies with it. Failing tests are re-run at HEAD with
the command's own flags (a pass there is recorded `flaky n/m`); a failure
that exists on the base and fails there the same way on every base run, in
a throwaway worktree outside the capsule, is pre-existing. A command whose
every failure was explained that way passes only when, re-run whole once
in its test-bar form, it exits 0 — or, for the repo's declared Go harness
run directly (`sh <go_harness>`), when its own trailer counts every listed
test as run or excluded; a command with neither (a Makefile target, an
`&&` chain carrying a pre-existing failure, a runner without a skip
switch) stays failed. A Go package whose test binary did not finish
normally (a panic, a timeout, an `os.Exit`, `-failfast`) is a failure of
its own beside its failed tests. `--budget`
(default 20m) bounds when those re-runs and base runs may still start; a
unit or command they no longer reach stays a failure. `--deadline` bounds
the whole run (default: the tightest of `BASH_MAX_TIMEOUT_MS` and
`PF_TOOL_WINDOW_MS` less a margin — 2 minutes, or half a bound smaller
than that — else 25m): no command runs past it — a command it cuts short
or that can no longer start is recorded failed — and the result is written
before it, so the step's own shell call never outlives the daemon's tool
rule. An explicit `--deadline` above that bound is clamped to it, and
`result.json` records the value the caller asked for in
`deadline_clamped_from` (`deadline` is the value the run used). A step
environment carries `PF_TOOL_WINDOW_MS` on every harness (pi included) and
`BASH_MAX_TIMEOUT_MS` on claude; outside a step neither is set and the
fallback is 25m, so setting `PF_TOOL_WINDOW_MS` is how an operator raises
that ceiling for a manual run. A private `TMPDIR` left by a run that
was killed too hard to clean up is reclaimed by the next `pf verify-run`.
`--e2e-jobs` / `--gomaxprocs` export
`E2E_JOBS` / `GOMAXPROCS` to every command; `--max-parallel-repos` (default
1) bounds how many repos run at once.

It writes `result.json` (envelope-ready `checks`, `pre_existing_failures`,
`test_bar`, `command_evidence`, the merge outcomes, the parsed units and the
authored scenarios their bindings resolve) and `report-mechanical.md` into
`runtime-state/verify/<n>/` (or `--out`); the command logs stay there too,
behind a `.gitignore` that keeps the tree out of the capsule chain. Exit 0
whenever a result was produced — a red bar is data. Exit 1 when it could
not run, and — deliberately, so it can never be read as a verdict — exit 1
with `incomplete: <reason>` on stderr (and in `result.json`) when no
merged-tree proof could be made (a failed fetch); the step returns that as
an incomplete outcome.

## `pf config` — cli.toml and hot reload

`pf config` is the one supported writer of `cli.toml`:
`get <key>` (effective value + source `flag|env|file|default`),
`set <key> <value>`, `unset <key>`, `list`, `validate` (no write) and
`reload` (apply a hand edit). A key is a flat name (`server`,
`claude_bin`, …), a `[daemon]` key (`max_concurrent_jobs`, …) or a whole
`[workspace."<path>"]` section (`projects=a,b,c`). Writes preserve every
other byte, are crash-atomic, keep the mode (a new file is `0600`) and
pass the same validator the loader, daemon and doctor use; a refused
write leaves the file untouched. `api_key` is write-only: stdin only,
never argv, never emitted.

The running daemon applies live keys on the write (`max_concurrent_jobs`,
`step_timeout`, `stall_window`, `claude_bin`), takes a `[workspace]`
mapping addition at the next claim (a removal/re-route with an in-flight
job is refused and the work items are listed) and reports `server`,
`api_key`, `daemon_socket`, `harness_root` and `operator_agent_dir` as
pending restart. `pf ops status` shows what is pending or overridden by
an explicit daemon flag. The exact keys, classes and outputs are on the
[config page](config.md); the reload route and its refused caller class
are on the [UDS routes page](uds-routes.md).

`pf config workspace --cwd <dir>` reads the mapped workspace's yaml and its
declared step candidates (one JSON document; see the [config page](config.md)).
`set`/`unset` also accept `--expect-hash`, `--expect-state` and, for the
`summarize.*` keys, `--workspace`/`--cwd`; the CAS comparison runs inside the
writer flock.

## The daemon (`pf daemon`)

`pf daemon` runs the local work machine (runtime, driver and supervisor
behind the UDS face). `pf help daemon` lists every flag, rendered from
the flag set the daemon parses — that help text is the flag authority;
this section carries the behavior around them.

**Exit status 78** means the daemon refused to start for a reason only
an operator can fix — configuration, the recorded daemon id, the
workspace mapping or a workspace directory, unreadable on-disk state, or
a failed restart reconciliation. The `pf daemon ensure` supervision loop
then STOPS (it logs `the daemon refused to start (exit 78 …); the loop
ends`) instead of restarting into the same failure every 5s; any other
non-zero exit is still restarted after 5s. To recover: read
`<data-dir>/daemon.log`, fix the cause, then run `pf daemon ensure`
(the next harness session start also runs it).

**Git on the work machine.** The daemon's git calls that talk to aihub
with the session credential — the capsule chain push, the capsule clone
and the carrier ls-remote/fetch/push — run with no system or global git
config (`GIT_CONFIG_NOSYSTEM=1`, `GIT_CONFIG_GLOBAL=/dev/null`) and with
every inherited `GIT_*` variable stripped. So `http.proxy`,
`http.sslCAInfo`, `url.*.insteadOf` and `credential.helper` from
`~/.gitconfig` do NOT apply to them: give the daemon a proxy through
`https_proxy` / `HTTPS_PROXY` in its environment, and since
`GIT_SSL_CAINFO` is stripped too, an aihub behind an internal CA needs
that CA in the system trust store. The capsule commit itself still
reads the global config, so `user.name` / `user.email` must be set for
the daemon user; task-repo git (inside `repos/<name>`) also still uses
the global config.

**The daemon's environment** is an explicit allow-list (defined once,
in `internal/polyforge/supervisor/env.go`): `pf daemon ensure` launches
it with only `PATH`, `HOME`, `USER`, `LOGNAME`, `SHELL`, `LANG`, `LC_*`,
`TZ`, `TMPDIR`, `PWD` (exported by the loop's shell itself), the proxy
variables (`http_proxy`, `https_proxy`, `no_proxy`, `all_proxy`, upper
and lower case), `SSL_CERT_FILE`, `SSL_CERT_DIR`, the XDG base dirs, the
Go toolchain variables (`GOPATH`, `GOROOT`, `GOPROXY`, `GOPRIVATE`,
`GONOSUMDB`, `GONOPROXY`, `GOFLAGS`, `GOTOOLCHAIN`, `GO111MODULE`,
`GOCACHE`, `GOMODCACHE`), the task-repo Git low-speed thresholds
(`GIT_HTTP_LOW_SPEED_LIMIT`, `GIT_HTTP_LOW_SPEED_TIME`),
`POLYFORGE_DATA_DIR`, `GOCOVERDIR` and the
`POLYFORGE_SUPERVISOR` marker, with the values of the shell that ran
ensure (the tmux server's environment — whatever shell first started it
— never reaches the daemon: the loop runs `env -i <allow-list> sh -c
<loop>`). Nothing else gets through: no `ANTHROPIC_*`, no `PF_*`, no
stale API key. A proxy or CA the daemon needs must therefore be set
where ensure runs (the harness session, or the install.sh shell).
`PF_SERVER`, `PF_API_KEY`, `PF_USER_ID` and `PF_DAEMON_SOCKET` in that
shell do steer ensure's own config load (which socket it probes), but
are not passed to the daemon: it reads `cli.toml` (`--config`). Step
agents see only this environment: claude steps additionally drop every
inherited `ANTHROPIC_*` (their credential and endpoint come only from
`<harness-root>/claude-config/settings.json`); pi steps need their
provider keys in `auth.json` or as literals in `models.json` — a
`$NAME` there, or a provider `*_API_KEY`, that is not on the list never
reaches them. `GET /status` reports the daemon's environment variable
names (never values), and `pf doctor`'s `daemon env` row warns when a
running daemon carries `ANTHROPIC_*` or another credential-looking
name, or when the pi step agent's `models.json` references a variable
off the list — restart it clean with `pf ops shutdown`, wait,
`pf daemon ensure`.

**Production knobs** come from the `cli.toml` `[daemon]` table: the
[config reference](config.md) owns every key. The only environment
variable the daemon reads besides the `PF_*` CLI ones is
`POLYFORGE_DATA_DIR` (the `--data-dir` default; ensure reads it too) —
and an ensure-started daemon sees no `PF_*` at all (its environment is
the allow-list above). The retired `POLYFORGE_MACHINE_ID`,
`POLYFORGE_DISPLAY_NAME`, `POLYFORGE_HARNESS_ROOT`,
`POLYFORGE_OPERATOR_AGENT_DIR` and `POLYFORGE_RECALL_TOPK` fallbacks
are gone; a value in the environment is ignored. Because harness state
must never move silently (polyforge#171), the daemon warns at boot when
one of the two harness variables is set to a value differing from what
it resolved (stderr, a `retired_env_ignored` event, `env_warnings` in
`GET /status`), and `pf doctor`'s `retired env` row warns too.

**Model health:** `pf ops models` reads the daemon's read-only `GET /models` profile — one row per model+effort × step over the events-retention window (dispatches, successes, model deaths, rate, mean wall time from event timestamps, non-model deaths and the most recent death cause) — and states the rolling window above the table; `--json` re-emits the route document unchanged. It is advisory telemetry only: no dispatch is reordered, pinned, skipped or blocked, and the thresholds are compiled constants with no config key. `pf ops status` carries the same summary (current warnings, or an explicit no-warnings line, pointing at `pf ops models`), and `pf doctor`'s `model health` row warns at most without moving `ok` or the exit code.

**Job cap:** `pf ops max N` applies at once and lasts until the daemon
restarts; every start takes the configured cap (an explicit
`--max-concurrent-jobs`, else the `[daemon]` table, else 4). A reload
that **changes** `max_concurrent_jobs` also overrides a temporary
`pf ops max` (last action wins), while an unrelated reload leaves the
temporary value in force. The cap is never persisted; `pf ops max` and
`pf ops status` show the one in force.

**Supervision bounds** are Go duration strings (e.g. `700ms`, `5m`); a
flag omitted or passed `0` keeps the default, and every flag's meaning
is in `pf help daemon`. Each bound's verdict names the failure it
detects: X (`--silence-window`, test/dev-only) ends a dispatch as
`harness_silent` when no real work signal arrives after it, or
`model_hung` when no model output arrives while waiting for the model
(UI chatter never counts as work); Y (`--tool-window`, test/dev-only)
ends a turn as `tool_hung` when one tool runs longer than it; T
(`--step-timeout`) ends a step as `step_timeout_long_task` (same
model, T doubled) or — only when the dispatch that hit T itself ran at
least half of it and spent at least half of its own time waiting on the
model — `step_timeout_slow_model` (switch candidate, T doubled); N (`--stall-window`) ends the driver between
steps as `driver_stalled`; and a turn that ends with no result file
inside `--model-error-grace` (test/dev-only) is `model_error` when it
ended on a model error (the first on a candidate resumes it, the second
consecutive model-class death switches candidate; an allowance or auth
error, or a model name that cannot resolve, switches at once),
`ended_without_result`
(the first retries on the same model, the second on one candidate switches) when it ended normally. Consecutive
`model_error` / `upstream_transient` / `ended_without_result` deaths on
one candidate wait 1m, 3m, then 9m before a re-dispatch of that candidate
(a switch to the next candidate dispatches at once and its own ladder
starts at 1m; the first wait is
the test/dev-only `--retry-backoff-base`); a restarted daemon serves
only what is left of a wait, and `pf ops status` shows it as
`retry in 3m: <cause>`. In production only T
(`--step-timeout`) and N (`--stall-window`) are settable, through the
`cli.toml` `[daemon]` table; the other bounds are test/dev-only. Their
running values are observable as `supervision.{step_timeout,stall_window}`
in `pf ops status --json`, so a live reload of either key is visible
without a restart. A tick that arrives far later than its cadence declares
the supervisor's own pause (a host or daemon freeze): the span it did not
watch is not charged to the step — neither to the running dispatch's
bounds nor to the step's cumulative runtime the next dispatch inherits
under T — and is recorded as one `supervisor_paused` event. The
only per-step override in `workflow.md` is `budget.wall_minutes` (an
estimate: T becomes `max(3 x wall_minutes, --step-timeout)`); the former
`silence_minutes` / `tool_minutes` keys were deleted (polyforge#147)
and an old bundle that still carries them has them silently ignored.

**Wait calls** are counted and surfaced, never judged: a bash tool call
that spends the step's clock on a wait nothing can end early, in one of
two forms, each call counted once. Form `sleep`: the command starts with
`sleep N` (N over 3 seconds; a `sleep` inside a condition loop, or after
another command, is not one), and its seconds are N. Form `wait`:
otherwise, the command runs the shell's `wait` builtin at command
position (as a call that backgrounds jobs with `&` and then awaits them
does; a quoted `wait`, an argument or a flag such as `--wait` is not
one), and its seconds are the call's measured wall time — taken when the
call ends, or when its dispatch ends for a call still blocked then — and
it counts only when that time is over 3 seconds. No bound reads either
form and nothing is killed for it. Each is recorded as one `wait_call`
event (its `form`, `sleep` or `wait`, its seconds, a command excerpt of
at most 120 characters, the tool call id and its dispatch's running
totals, which restart at every dispatch), and the
current step's total — every dispatch of the step the daemon watched —
shows as the `wait calls: n (Ns)` line of `pf watch <wi> --once` and as
`wait_calls` / `wait_seconds` on the job in `pf ops status --json`.

## `pf recall` — memory search

`pf help recall` prints the usage; the flags, the `--project` mapping
check and the failure codes are in
[skills/polyforge/verbs/recall.md](../../skills/polyforge/verbs/recall.md),
the fragment that owns the verb's shapes. What memory is and how
recalls reach a step: the [concepts page](../concepts.md).