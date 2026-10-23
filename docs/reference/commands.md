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
| Data-plane verbs (direct to aihub) | `file`, `queue`, `cancel`, `recall`, `remember`, `memory` |
| Execution-plane verbs (local daemon UDS face) | `submit`, `watch` (snapshot, resident watcher, `subscribe`/`unsubscribe`/`adopt`/`list`), `answer`, `pause`, `resume`, `wrap`, `ops` (`status`, `auto`, `max`, `shutdown`, and `end-session` — aihub-direct, admin/maintainer), `chat init`, `result submit` (inside a workflow step only: its one result-delivery path) |
| Management face (M1–M5) | `users`, `api-keys`, `projects`, `machines`, `backup` (database snapshot, see below) |
| Diagnostics | `doctor` (`--prune-scratch` removes stale `scratch/` entries), `version` (the verb form of `pf --version`), `init` (a workspace's local layout) |
| Process | `daemon` (run the work machine; `pf daemon ensure` starts it) |

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
`GOCACHE`, `GOMODCACHE`), `POLYFORGE_DATA_DIR`, `GOCOVERDIR` and the
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

**Job cap:** `pf ops max N` applies at once and lasts until the daemon
restarts; every start takes the configured cap (an explicit
`--max-concurrent-jobs`, else the `[daemon]` table, else 4). The cap is
never persisted; `pf ops max` and `pf ops status` show the one in
force.

**Supervision bounds** are Go duration strings (e.g. `700ms`, `5m`); a
flag omitted or passed `0` keeps the default, and every flag's meaning
is in `pf help daemon`. Each bound's verdict names the failure it
detects: X (`--silence-window`, test/dev-only) ends a dispatch as
`harness_silent` when no real work signal arrives after it, or
`model_hung` when no model output arrives while waiting for the model
(UI chatter never counts as work); Y (`--tool-window`, test/dev-only)
ends a turn as `tool_hung` when one tool runs longer than it; T
(`--step-timeout`) ends a step as `step_timeout_long_task` /
`step_timeout_slow_model`; N (`--stall-window`) ends the driver between
steps as `driver_stalled`; and a turn that ends with no result file
inside `--model-error-grace` (test/dev-only) is `model_error` (switch
candidate) when it ended on a model error, `ended_without_result`
(same-model retry) when it ended normally. In production only T
(`--step-timeout`) and N (`--stall-window`) are settable, through the
`cli.toml` `[daemon]` table; the other bounds are test/dev-only. The
only per-step override in `workflow.md` is `budget.wall_minutes` (an
estimate: T becomes `max(3 x wall_minutes, --step-timeout)`); the former
`silence_minutes` / `tool_minutes` keys were deleted (polyforge#147)
and an old bundle that still carries them has them silently ignored.

## `pf recall` — memory search

`pf help recall` prints the usage; the flags, the `--project` mapping
check and the failure codes are in
[skills/polyforge/verbs/recall.md](../../skills/polyforge/verbs/recall.md),
the fragment that owns the verb's shapes. What memory is and how
recalls reach a step: the [concepts page](../concepts.md).