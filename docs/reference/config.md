# Config — every real key, each once, in one place

The one authority for Polyforge's two configuration files: `cli.toml`
(the CLI and daemon config) and `.polyforge.yaml` (the workspace-level
per-step settings). Every key that exists is documented here; a key not
on this page does not exist. The load behavior (precedence, validation,
refusals) is stated once per file; the [commands page](commands.md) owns
the command surface, and [extending](../extending.md) indexes the
extension points that point into this page. The concepts (workspace,
repo mapping, machine) are defined on the [concepts
page](../concepts.md).

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
| `harness_root` | `--harness-root` | `<data-dir>` | Parent root of the step harness dirs: `<root>/step-agent` (the dedicated step agent dir), `/step-agent-sessions` (pi sessions) and `/claude-config` (claude's config dir; its settings.json env block is the credential authority layer). Replaces the former `--harness-sessions-root`, which is refused. |
| `operator_agent_dir` | `--operator-agent-dir` | `~/.pi/agent` | SOURCE of the dedicated step agent dir — a credential surface: the daemon copies the minimal set (`auth.json`, `models.json`, `agents/`, `extensions/subagent/`) from here into `<harness-root>/step-agent`, and every pi step runs against that copy. A configured source without a readable `auth.json` refuses the daemon start. |
| `max_concurrent_jobs` | `--max-concurrent-jobs` | `4` | The configured cap on concurrently running jobs; `pf ops max N` overrides it until the daemon restarts. |

A bad value, an unknown key or a duplicate is refused naming the file,
the line and the key (`config …: line N: [daemon] step_timeout: "soon"
is not a Go duration`); ensure starts nothing and the daemon exits 78.
Edit the table, then restart the daemon (`pf ops shutdown`, wait,
`pf daemon ensure`) for it to apply.

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
Restart the daemon for a change to apply.

## .polyforge.yaml

The workspace-level per-step settings file at a workspace root. One
strict-schema loader shared by every consumer (the daemon's pre-claim
read, `pf doctor`, the candidate-precedence compose); there is no
second parser. The daemon reads and validates it BEFORE the aihub
claim: an invalid file refuses the work item with DAEMON-414 (`pf
submit` shows it; auto-pull skips that project and logs once) and
nothing is claimed, so the work item stays queued for other machines.
An edit takes effect on the next claim with no daemon restart (the file
is read fresh before every claim).

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
fresh session with the previous transcript as read-only reference. A
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

The models list, the language and the interval are read with the rest of
the file before every claim and snapshotted into that claim's job record
(`summarize_models`, `summarize_language`, `summarize_interval_sec`):
an edit governs the next claim only. No key (or no file) leaves the lane
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
step's wrap duty; every step's prompt asks it to recall first. The
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