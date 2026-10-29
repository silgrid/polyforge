# Config — every real key, each once, in one place

The one authority for Polyforge's two configuration files: `cli.toml`
(the CLI and daemon config) and `.polyforge.yaml` (the workspace-level
per-step settings) — plus the per-repository verification bar,
`.polyforge/verify-scope.conf`. Every key that exists is documented here; a
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
are explicit verbs (`pf chat init --force`, `pf machines rekey`), not config.

## cli.toml

`~/.config/polyforge/cli.toml` (override with `--config PATH`; there is
no environment override for the path). One location, no
secondary lookup. Precedence: command-line flag > `PF_*` environment >
this file > built-in defaults. A missing file is not an error (defaults
apply, empty mapping); a malformed file IS an error — silent
misconfiguration is worse than a loud failure — and so is a mapping that
fails validation. The file is validated on load by every `pf` command,
by `pf daemon ensure` and by the daemon (the same check).

### Flat keys

Root-level wherever they appear in the file, but they must come before
any table header (TOML semantics: every key after a `[...]` header
belongs to that table):

| Key | Env override | Controls | Default |
| --- | --- | --- | --- |
| `server` | `PF_SERVER` | aihub base URL | `http://localhost:8420` |
| `api_key` | `PF_API_KEY` | aihub api key (Bearer) | — |
| `daemon_socket` | `PF_DAEMON_SOCKET` | daemon unix socket path | `daemon.sock` in `$POLYFORGE_DATA_DIR`, else `~/.local/share/polyforge` |
| `user_id` | `PF_USER_ID` | caller identity for answer.answered_by | — |
| `claude_bin` | — | the Claude Code binary of claude steps (see below) | `claude` |

`PF_CHAT_ID` (set by the harness adapter) is a notification label, not a
config key: it never reaches the config file. There is NO default
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

### `[daemon]` — the daemon knobs

The production knobs the daemon reads from its `--config` file (decision
2026-10-16 G2). Precedence: an explicit command-line flag (tests/dev) >
this table > the built-in default. At most one table per file.

```toml
[daemon]
step_timeout = "45m"                 # --step-timeout: Go duration, > 0
stall_window = "10m"                 # --stall-window: Go duration, non-zero; negative disables
harness_root = "/srv/polyforge/h"    # --harness-root: absolute path
operator_agent_dir = "/home/u/.pi/agent"  # --operator-agent-dir: absolute path
max_concurrent_jobs = 6             # --max-concurrent-jobs: unquoted integer >= 1
```

Every key is optional (absent = the daemon default). The keys and their
defaults:

| Key | Flag | Default | Controls |
| --- | --- | --- | --- |
| `step_timeout` | `--step-timeout` | `90m` | T: the cumulative step runtime bound across a step's dispatches. |
| `stall_window` | `--stall-window` | `10m` | N: the driver between steps (not waiting on a human) for longer than this is judged stalled; a negative value disables it. |
| `harness_root` | `--harness-root` | `<data-dir>` | Parent root of the step harness dirs: `<root>/step-agent` (the dedicated step agent dir), `/step-agent-sessions` (pi sessions) `/claude-config` (claude's config dir; its settings.json env block is the credential authority layer) and `/summarize-cwd` (the working directory of the summarize lane's one-shot dispatches, never the daemon's `$HOME` cwd). Replaces the former `--harness-sessions-root`, which is refused. |
| `operator_agent_dir` | `--operator-agent-dir` | `~/.pi/agent` | SOURCE of the dedicated step agent dir — a credential surface: the daemon copies the minimal set (`auth.json`, `models.json`, `agents/`, `extensions/subagent/`) from here into `<harness-root>/step-agent`, and every pi step runs against that copy. A configured source without a readable `auth.json` refuses the daemon start. |
| `max_concurrent_jobs` | `--max-concurrent-jobs` | `4` | The configured cap on concurrently running jobs; `pf ops max N` overrides it until the daemon restarts, and a reload that **changes** `max_concurrent_jobs` overrides `pf ops max` too (last action wins). |

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
`--claude-bin` row of `pf help daemon` (`claude -p --bare` when the
claude-config settings.json env block carries a non-empty
`ANTHROPIC_AUTH_TOKEN`/`ANTHROPIC_API_KEY`, else `claude -p
--safe-mode` for an OAuth login; the daemon never manages credentials —
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
`BASH_MAX_TIMEOUT_MS` is clamped to that max. pi steps get neither
variable.

### Writing `cli.toml` — `pf config`

`pf config` is the one supported writer of `cli.toml` (the loader, the
daemon, `pf doctor` and this command share one validator). It never
rewrites the file as a whole: a `set` replaces only the target line (a
whole `[workspace]` section for a section key), every other byte —
comments, blank lines, order — survives. Writes are crash-atomic (temp
file, fsync, rename; an existing mode is kept, a new file is `0600`) and
concurrent writers serialize on a file lock.

```
pf config get <key>              # effective value + source (flag|env|file|default)
pf config set <key> <value>      # set a flat key, a [daemon] key or a whole [workspace] section
pf config set '[workspace."/path"]' projects=a,b,c
pf config unset <key>            # remove the line (or the whole section)
pf config list                   # every key with its effective value, source, reload class and schema
pf config validate               # validate without writing
pf config reload                 # apply a hand edit to the running daemon
```

Keys are addressed by their flat name (`server`), their `[daemon]` key
name (`max_concurrent_jobs`) or as a whole `[workspace."<path>"]`
section. `api_key` is write-only: pass it on stdin
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
| live | `max_concurrent_jobs`, `step_timeout`, `stall_window`, `claude_bin` | applied in the running daemon |
| next-claim | the `[workspace]` mapping | additions usable at the next dispatch; a removal/re-route with an in-flight job is refused and the work items are listed |
| restart-only | `server`, `api_key`, `daemon_socket`, `harness_root`, `operator_agent_dir` | reported pending; the running daemon keeps its value and a restart applies it |
| CLI-only | `user_id` | never read by the daemon |

### Machine-readable list, schema and CAS

`pf --json config list` is additive: every item carries `class` (the same
class table the reload path uses, including `next-claim` for a whole
`[workspace."<path>"]` section) and `schema` (the value `kind` plus only the
constraints the shared validator actually enforces — `max_concurrent_jobs`
`{kind:"int",min:1}`, `step_timeout` `{kind:"duration",min_exclusive:0}`,
`stall_window` `{kind:"duration",nonzero:true,allow_negative:true}`;
`language` has no enum). The document also carries
`file:{path,sha256,exists}` for the CAS precondition. Existing
`value`/`source`/`set`/`section` fields are unchanged and `api_key` stays
masked.

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

### Workspace `.polyforge.yaml` summarize keys

`pf --json config workspace --cwd <dir>` resolves the cwd through the same
`MatchWorkspace` longest-prefix rule as every other workspace consumer and
always answers one JSON document: `mapped`, the workspace label, the yaml
`path`/`sha256`/`exists`, the effective `summarize.models` (ordered),
`summarize.language` and `summarize.interval_sec` each with its `source`
(`file` or `default`) and `class` (the summarize lane reads the yaml live, so
all three are `live`), the per-project `steps` map (`declared` + `models`, or
`declared:false` with no list), and `steps_complete`. An unmapped cwd answers
`mapped:false` with exit 0.

`pf config set|unset summarize.models|summarize.language|summarize.interval_sec`
takes exactly one of `--workspace <path>` or `--cwd <dir>` plus the same CAS
options; `--workspace` must name a `[workspace."<path>"]` already mapped in
cli.toml (direct-CLI only; pi-web always resolves through `--cwd`). The write
is a byte-preserving line edit inside the root `summarize:` block, validated
by the one strict `workspace.Load` validator before and under the flock, and
landed through the shared atomic writer. A valid write changes only its target
line; an invalid value or a `summarize` entry without `models` is refused with
the validator's own error and the file is left byte-identical. An absent file
is created only when the expectation says `absent`; an unset with no file to
edit is a no-op and creates nothing. `unset summarize.models` removes the
whole entry.

After a successful write `pf config` notifies the running daemon; there
is no file watcher, so a hand edit needs the explicit `pf config reload`.
A bad reload applies nothing, names the offending key, emits one redacted
event (key names only) and never stops the daemon. The reload entry point
is local-UDS only and refuses step agents (`docs/reference/uds-routes.md`).

## .polyforge.yaml

The workspace-level per-step settings file at a workspace root. One
strict-schema loader shared by every consumer (the daemon's pre-claim
read, `pf doctor`, the candidate-precedence compose); there is no
second parser. The daemon reads and validates it BEFORE the aihub
claim: an invalid file refuses the work item with DAEMON-414 (`pf
submit` shows it; auto-pull skips that project and logs once) and
nothing is claimed, so the work item stays queued for other machines.
An edit takes effect at the next dispatch with no daemon restart: the file
(and the bundle's own lists) is read fresh at every dispatch — every
step start, retry and resume — and the candidate list recomposed from it;
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
mismatches, empty model ids, a per-step key that is no step id of the
daemon's installed workflows, a project the cli.toml mapping does not
route to this workspace, and duplicate model ids in one list are all
load errors. The retired top-level `models:` section (the pre-#169
shape) refuses with a migration message; `projects:` /
`workflow_defaults:` are unknown keys like any other (upgrading from
the pre-#169 shape: [upgrade.md](../upgrade.md)). YAML merge keys
(`<<`) are refused — share a list with anchors,
`models: &x [...]` / `{ models: *x }`.

### `steps:` — model candidates per project and step

```yaml
# Precedence: pf submit --models > steps.<project>.<step-id>.models > bundle step model_candidates > steps.<project>.default.models > bundle defaults
steps:
  polyforge:                                # a project of this workspace
    default: { models: ["pi:prov/model-a", "pi:prov/model-b"] }  # optional: steps without a list of their own
    fix: { models: ["claude:claude-opus-5-5", "pi:prov/model-c"] }  # optional: one step id (fix, code,
                                            # review, deliver, ...) — the step
                                            # with that id in every bundle
```

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
environment setup is not redone); in a rewound round of the lane's build step
the fast path is additionally suppressed when the retry's task-repo (`repos/**`)
tree still equals the tree the rewinding gate judged (`judged_repo_hash`) — no
code progress, so the from-scratch reference stays beside the FAIL verdict
( criterion 5; an unknown judged hash or a repos-hash error keeps
this wording). A mismatch, a missing hash, a re-claim or an unreadable tree
keeps the from-scratch reference. A
step with no list (or an empty one) runs pi's own default model. The
optional effort (polyforge#176) is the last segment only when it is one
of `off minimal low medium high xhigh max` — `pi:ollama/llama3:8b`
keeps `:8b` in the model id; pi gets it as `--thinking <effort>` (all
seven), claude as `--effort <effort>` (`low`…`max`; `off`/`minimal`
refuse); no effort, no flag.

A step entry's only key is `models`. For a step of the claimed project
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

### `summarize:` — the summarize lane

The model candidates of the demand-gated summarize lane — the one-line
narration of a running step's live transcript that a watched work
item's snapshot carries (polyforge#224). One list for the whole
workspace: a claim of any of its projects uses it (there is no
per-project list; a `steps.<project>.summarize` key is an unknown step
id and refuses):

```yaml
summarize:
  models: ["pi:prov/flash-model", "claude:claude-sonnet-5-5"]  # tried in order on failure
  language: English   # optional; absent = 中文
  interval_sec: 120   # optional; absent = 60
```

`models` has the shape and strictness of a step entry in `steps:` above
and is mandatory in the entry: a `language` or an `interval_sec`
without `models` refuses like an empty entry. Any key other than
`models`, `language` and `interval_sec` refuses (DAEMON-414 before the
claim; `pf doctor` the same).

`language` (polyforge#243) is the narration language: the prompt asks
the model to "Answer in <language>." with the value inserted verbatim,
and to keep code identifiers, file paths and commands as written.
Absent, the narration is in 中文. The value is free text — no allowlist,
no language codes, no length cap — but must be a plain YAML string that
is non-empty and single-line after trimming; a number, list, map or
null, an empty or whitespace-only value and a multi-line value all
refuse.

`interval_sec` (polyforge#250) is the minimum number of seconds between
two summarize call starts for one work item (the clock is per work item
and does not reset on a new step dispatch). Absent, it is 60. The value
must be a YAML integer from 10 to 3600, inclusive; anything else — a
quoted `"60"`, a float, a duration such as `1m`, a bool, null, a list or
a map, `0`, a negative or an out-of-range number — refuses with an
error naming the range `10-3600`. It is never clamped, and `0` does not
mean "default". The per-attempt cap stays fixed at 20 calls per step
dispatch, and no call starts while the previous one is still running,
whatever the interval.

The models list, the language and the interval are read from the file
live at every lane request (nothing is snapshotted at claim): an edit
governs the next call. No key (or no file) leaves the lane
silently off — no call, no error, no doctor row; a declared-empty list
is off too.

### `memory_policy:` — the wrap-summary policy

What the workflow's last step puts into the work item's one
`experience.wrap` summary memory (no other step writes memories):
`record` lists what is worth remembering, `skip` what never is. Shape —
`default` and per-project entries, each with optional `record` / `skip`
lists of non-empty, unique strings (no other key):

```yaml
memory_policy:
  default:                       # every project; an undeclared list is the built-in default's
    record: [pitfalls and their root cause, the owner's rulings and agreements]
    skip: [transient state, credential values]
  polyforge:                     # a project of this workspace: lists APPENDED to default's
    record: [environment differences around the daemon's rollout]
```

A missing section means the built-in default policy and never refuses a
claim; a malformed section refuses it like any other defect of the file
(DAEMON-414). The built-in default (the section `pf init` appends as
text when the file has none, existing bytes kept exactly): record the
pitfalls and root causes (symptom, cause, fix), the environment and
external-system facts not visible in code or git, and the owner's
rulings and agreements; skip the process log, step output and full
spec/plan (they live in the work item and the capsule), what code or
git shows directly, transient state, and credential values. The daemon
writes the claimed project's effective policy to the capsule
(`runtime-state/memory-policy.json`) and renders it into the last
step's wrap duty; a step whose workflow entry declares `recall: true`
(the decision and authoring steps, never a gate like review,
verification or deliver) is asked to recall first. The
built-in default described above is an English paraphrase: the section
`pf init` appends carries the same lists written in Chinese, and the
built-in policy the daemon parses from it is the one described here.
`pf init <ws>` appends the default section as text when the file has
none and `pf doctor` warns about a `.polyforge.yaml` without one (no
file at all passes on the built-in default).

Step agents hold no aihub credential path: inside a step `pf recall` /
`pf memory show` read through the daemon (scoped to the step's own
project); `pf remember` sends the wrap summary synchronously to the
daemon (`POST /step/memories`), which validates it (type
`experience.wrap`, a title, ≤ 2 KB, no credential value), attributes it
to the step's own work item and project and stores it with an
idempotency key of the step dispatch (a retry returns the same memory);
a failure is reported and never fails the step. Every other aihub verb,
`pf doctor` and `pf daemon` are refused there (DAEMON-403).

## `.polyforge/verify-scope.conf` — the verification bar

A repository's declarative verification bar, committed in the repository and
read from its root by the release-shipped engine `tools/verify-scope`. The
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
| `stack` | `go`, `npm` or `none` | Pins the analyzer (the first valid pin wins). `none` pins no analyzer: always the full tier, the conf's commands only. |
| `go_harness` | path | The Go acceptance harness that owns its directory; the scoped selection runs it for the tests that reference a changed path. |
| `fast_gate` | command | Repeatable, ordered: an anonymous fast-phase command, named `fast-<i>` (the i-th `fast_gate` line). |
| `full_command` | command | Repeatable, ordered: an anonymous full-phase command, named `full-<i>`. |
| `check` | `<name>\|<fast\|full>\|<command>` | Repeatable: a named command of that phase, in file order among the phase's lines. |
| `check_requires_env` | `<name>\|VAR[,VAR]` | The named command needs these variables set and non-empty. |
| `check_requires_cmd` | `<name>\|bin[,bin]` | The named command needs these executables: a bare name is looked up on `PATH`, a name containing `/` is resolved against the repository root. |
| `check_timeout` | `<name>\|<duration>` | The named command's timeout, a positive Go duration (`90s`, `20m`, `1h30m`); the first one per name wins. Without one the JSON report's `timeout` is `null` and the consumer's own default applies. |
| `forced_full` | glob | Repeatable: a changed path matching it forces the full tier. |
| `module_root` | path | Repeatable: the Go spanning roots. |
| `exclude` | glob | Repeatable: a path the full-tier triggers and the npm full standalone pass ignore. |
| `test_glob` | glob | Repeatable: a test-file pattern of the repository (echoed in the JSON report). |
| `base_setup` | command | Repeatable, ordered: the setup a rerun on a base tree needs first (echoed). |
| `runner` | `complete` | Declares that the conf's commands are the complete bar (echoed). |
| `ci_evidence` | `<workflow>/<job>` | Repeatable: a CI job the deliver step may accept as test evidence (echoed). |

Command values — `fast_gate`, `full_command`, the command of a `check`, and
`base_setup` — are bash: `--run` executes them with `bash -c` from the
repository root, and a value that begins with `$(` or a backtick is skipped
with a lint diagnostic. A phase's conf lines, anonymous or named, replace the
analyzer's default commands of that phase. A `<name>` is letters, digits,
`.`, `_` and `-`, starts with a letter or digit, is declared once, and is
never of the form `fast-<n>`, `full-<n>` or `scoped-<n>` (the anonymous and
analyzer commands' names). A `check_*` line names a declared command — a
`check` name or an anonymous `fast-<i>` / `full-<i>` — and may precede it;
one naming no declared command is diagnosed and ignored. `pf doctor`'s
`scoped-gates readiness` row reads this file (never executes it).

### `ci_evidence` — what the deliver step reads

Of the keys above, `deliver` reads one itself — read from the default
branch, never from the change being delivered:

- `ci_evidence=<workflow-file>/<job-name>` — repeatable, one line per job.
  `<workflow-file>` is the workflow's file name under `.github/workflows/`
  and `<job-name>` (everything after the first `/`) is the check-run name
  GitHub shows for the job, matrix suffix included. The file's line rules
  apply: `#` comments and blank lines are ignored, the key is exact up to
  the first `=`, and a value line carries no trailing comment.

When at least one job is declared, a delivery whose own merge moved HEAD
past the verified commit (or that follows a light pass) may show its tests
pass with GitHub check-runs instead of re-running the carried test bar: the
step reads `gh api repos/<owner>/<repo>/commits/<sha>/check-runs` for the
pushed head in the same dispatch — after its last merge, before its
`gh pr merge`, exit 0 — and records one `ci_evidence` entry per declared
job (`repo`, `job` as declared, `check_run_id`, `url`, `head_sha`,
`conclusion`). The driver accepts that only when every declared job has an
entry, each for exactly the pushed head with conclusion `success`; a
queued or in-progress check-run is not evidence, and anything short of it
leaves the re-run rule in force. The evidence stands in for a carried test
bar, never for a missing one. A malformed declaration (no `/`, an empty
side) gives the repository no CI path at all.

The key relaxes what counts as proof, so a change never grants it to
itself: the authoritative job set is the one in the conf of the fetched
`origin/<default>`, which the pushed HEAD must contain and go beyond. When
the pushed HEAD declares a different set — the change adds the key,
narrows or removes a job, or breaks a line — or `origin/<default>` already
contains HEAD, the CI path is refused and the re-run rule applies, with
the reason named. The same holds when the change edits anything
under
`.github/` (workflows, local or composite actions): the declared jobs are
defined there, and the pull request's check-runs would run the change's
own version of them. Edits to the conf's other keys do not matter here.
Without the key nothing changes: the carried test bar is re-run. This
repository has no CI and declares none.
