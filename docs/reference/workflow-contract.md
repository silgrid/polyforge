# Workflow contract — reference (contract 5)

The normative contract between a workflow lane and the daemon that runs
it: what a lane may declare, how the one loader judges it, how a step
talks to the daemon, the typed errors and how a contract version is
deployed. The bundle-authoring chapter, [bundles.md](../bundles.md),
teaches the format with a worked example; this page is the rule. The
registry behind every table here is `internal/common/workflow`
(`registry.go`, `contract.go`), pinned by
`internal/common/workflow/testdata/contract.v5.golden`; the tables below
are held equal to it by `internal/common/workflow/contract_doc_test.go`.
The lane file's JSON Schema, `workflows/_schema/workflow-5.schema.json`,
is generated from the same registry ([The lane](#the-lane)).

This is contract **5**. The daemon does four things: it claims a work
item, dispatches the lane's steps in order (holding their admission and
locks), reacts to the status each dispatch hands off, and wraps the work
item. It never judges a step's content and never reads the owner's words
for a meaning. A step and the daemon meet in exactly two places: one
command, `pf step` ([The step interface](#the-step-interface)), and one
document, `artifacts/handoff.md` ([handoff.md](#handoffmd)). Contract 5
moved the lane's structure out of `workflow.md`'s frontmatter into a YAML
file of its own, `workflow.yaml` (the prose into `README.md`), and lets a
step read a long var from a file of the lane (`vars_files`). Contract 4
removed the step's declared I/O (`inputs`, `verifies`, `outputs`,
`verdicts`), the aihub writes the daemon applied for a step (`effects`,
`handoff`), step `attrs`, `on_fail.charge`, the integration lane's
`fix_lane`, the output schema of a step definition and the result
envelope `pf result submit` delivered. What a step reads and verifies
first, and what it hands off, its step definition and skill now say in
prose; what the daemon and other programs read stays structured (the
status, the evidence paths, the hub writes). A lane declares the version
it targets (`contract: 5`); the daemon implements exactly one version
([Versions and the drain](#versions-and-the-drain)).

## The lane

A lane is a directory under the daemon's workflows dir
(`<data-dir>/release/current/workflows/<lane>/`, a built-in lane) or under
a workspace's `.polyforge/workflows/<lane>/` (a user lane, [User
lanes](#user-lanes)) holding `workflow.yaml`, one YAML mapping that is the
lane's whole structure, and `steps/<id>.md`, one definition per step. Its
`README.md` is the lane's narrative: the daemon never reads it. A step
without its own definition falls back to the shared `_shared/steps/<id>.md`
beside the lanes; fragments live in `_shared/<name>.md`, skills in
`<lane>/skills/<name>.md`, else `_shared/skills/<name>.md`, and a long var
in a file of the lane a step's `vars_files` names (by convention
`fragments/<name>.md`). A directory whose name begins with `_` (`_shared`,
`_schema`, `_archive`) is not a lane.

A lane that is still a `workflow.md` — contract 4's lane file, its
frontmatter the lane keys — is refused: alone it is `WFC-101` (the
version it declares, this daemon's and the rewrite), beside a
`workflow.yaml` it is `WFC-110`. The rewrite: the frontmatter (the YAML
between the `---` fences) becomes `workflow.yaml` with `contract: 5`, the
body becomes `README.md`, and `workflow.md` is deleted.

**The schema.** `workflows/_schema/workflow-5.schema.json` (JSON Schema
draft-07) is generated from the registry (`go generate
./internal/common/workflow`; a test regenerates it and fails on any
difference, and validates the shipped lanes against it). It states each
key's own value; the rules across keys, the definitions, the skills and
the placeholders are the loader's alone. A shipped lane points an editor
at it with its first line, a YAML comment the loader ignores:
`# yaml-language-server: $schema=../_schema/workflow-5.schema.json` (a
user lane names the installed release's copy).

**One loader.** Every reader of a lane — the driver, the claim check,
`pf file create`, the model-candidate composition, `pf workflows validate`
and `pf doctor` — reads it through `workflow.Load` (a user lane through
`workflow.LoadUser`, the same loader over the built-in shared tree), and
nothing else parses a lane. The loader decodes strictly: an unknown or
removed key at any level and a duplicate key are defects. It resolves and
renders every definition and skill the lane needs and reports every defect
at once, each with its code, file, line and YAML path.

### Lane keys

| Key | Rule |
|---|---|
| `contract` | int, required; equal to the daemon's version (`WFC-100` missing, `WFC-101` another version, and a lane that is a `workflow.md`) |
| `name` | string, required; `^[a-z][a-z0-9-]{0,39}$` and equal to the lane's directory name (`WFC-113` missing, `WFC-120` otherwise) |
| `description` | string, required, non-empty (`WFC-121`) |
| `max_rewinds` | int ≥ 0; the lane's one automatic rewind budget across every gate; required when any step declares `on_fail` (`WFC-113`), refused when none does (`WFC-112`); `0` = every gate FAIL asks the owner first |
| `exclusive` | bool, default `false` ([Properties](#properties)): `true` holds the lane to one unfinished work item per project — a create on it reaches aihub exclusive, refused `CORE-409` `OPEN_ON_BUNDLE` while another work item of the project on the lane is not wrapped, failed or canceled; a built-in lane and a user lane alike; any other value is `WFC-112` |
| `acceptance` | `required` or `none`, default `required` ([Properties](#properties)): whether a work item on the lane must list an acceptance criterion (`## Acceptance` or `## 验收`) before it runs; `none` is a lane with no criterion to list — the shipped integrate and release lanes declare it, develop and micro do not; a built-in lane and a user lane alike; any other value is `WFC-112` |
| `defaults` | a map holding `model_candidates` and `tiers` |
| `steps` | a non-empty list of step entries, in execution order (`WFC-113` missing or empty) |

### defaults keys

| Key | Rule |
|---|---|
| `model_candidates` | a list of `<harness>:<model>[:<effort>]` candidates (`WFC-198`) |
| `tiers` | `{<tier>: [<candidates>]}`: the lane's tier→model table a step's `model_tier` names (`WFC-198`). The workspace yaml's `tiers.<project>.<tier>.models` outranks it and a step's own yaml key outranks both ([config.md](config.md), "Tiers") |

### Step keys

Every step is an agent step: its definition is the prompt a harness
process runs.

| Key | Rule |
|---|---|
| `id` | string, required, `^[a-z][a-z0-9-]{0,39}$`, unique (`WFC-130`) |
| `gate` | bool; only a gate may hand off `fail` |
| `on_fail` | `{target}`, only on a gate (`WFC-140`) |
| `waivable` | bool, default false; only a gate declaring `waivable: true` may hand off `waive` (`WFC-112` on a non-gate) |
| `skippable` | bool, default false (`WFC-112` otherwise): a project may switch the step off with `steps.<project>.<step>.skip: true` in its workspace yaml ([config.md](config.md)); the switch is fixed when the work item is first claimed, and a switched-off step is never dispatched (its execution status is `skipped`). A step not declaring it always runs |
| `budget` | `{wall_minutes, tool_minutes}` |
| `model_candidates` | the step's candidates (`WFC-198`); exclusive with `model_tier` (`WFC-112`) |
| `model_tier` | a tier of the lane's `defaults.tiers` (`WFC-198` when the table lacks it; `WFC-112` beside `model_candidates`) |
| `vars` | a map of strings filling the definition's `{{placeholders}}` (`WFC-195`) |
| `vars_files` | a map of var name to a file of the lane (`fragments/<name>.md`): a relative path that stays inside the lane directory and names a regular file reached through no symbolic link (`WFC-112` otherwise). The file's text is the var's value, exactly as if written in `vars`; a var is declared in `vars` or in `vars_files`, not both (`WFC-112`) |
| `skill` | a skill name: `<lane>/skills/<name>.md`, else `_shared/skills/<name>.md`, rendered into the prompt (`WFC-200`) |
| `resources` | `{admission, locks}`: what the daemon takes for each dispatch |
| `hub` | `{verbs, attrs}` ([hub keys](#hub-keys)): what this step may call through `pf step hub`; default none |
| `wrap_attrs` | the wrap attrs this step registers, a list of names (`^[a-z][a-z0-9_]{0,63}$`), each at most once; default none (`WFC-112`). Its round's `pf step handoff --status done` is refused until each was registered with `pf step hub wrap-attrs <name> @<file>`, so a record the wrap must carry cannot be forgotten; listing one grants the step `wrap-attrs`. When the steps at the lane's end are skipped (`skippable` steps a project switched off), their `wrap_attrs` pass to the last step that runs: it is granted `wrap-attrs` and its done is refused until it registered them |

### on_fail keys

| Key | Rule |
|---|---|
| `target` | an earlier step id, required |

A FAIL handed off without `--per-owner` is charged to `max_rewinds`; one
handed off with `--per-owner` (the owner said so) is not.

### resources keys

| Key | Rule |
|---|---|
| `admission` | `none` (the default) or `heavy`: the dispatch takes a heavy-run admission slot before its process starts and gives it back when the process is gone |
| `locks` | a list holding `merge` at most once: the dispatch holds the per-repo merge lock of every task repo (the lock develop's merge and the integration lane's revert hold). `bar` is `WFC-112` with its rewrite: "the bar lock is gone; delete `bar` from resources.locks (a step that runs heavy commands declares `resources: {admission: heavy}`)" |

Admission first, the lock after, all or nothing; released in reverse
order when the dispatch ends. A step holding either is not kept warm while
it awaits the owner: at its `pf step ask` the process ends and what it
held is released, and the owner's reply resumes the session through a
dispatch that takes them again. A daemon without the binding a declared
resource needs parks the step blocked naming it.

### hub keys

| Key | Rule |
|---|---|
| `verbs` | the `pf step hub` verbs this step may call, a list of `get`, `list`, `file`, `wrap-attrs`, `batch`, each at most once; default none (`WFC-112` for an unknown or repeated verb). A `batch` holds the step to its verbs and attrs keys for every write inside it |
| `attrs` | the top-level attrs keys this step may write with `pf step hub attrs <wi> --set <key>`, a list of keys (`^[a-z][a-z0-9_]{0,63}$`), each at most once; default none (`WFC-112`). Listing one grants the step `attrs`; the daemon refuses a write of any key not listed (`DAEMON-403`) |

For example, the integrate lane's revert step: `hub: { verbs: [get, batch], attrs: [integration] }`.

### budget keys

| Key | Rule |
|---|---|
| `wall_minutes` | int > 0 |
| `tool_minutes` | int > 0 and ≤ 180, and ≤ `wall_minutes` when both are declared (`WFC-112`): the step's own supervision tool window, carried by every step process as `PF_TOOL_WINDOW_MS` (a pre-written command derives its deadline from it) |

### Definition keys

The frontmatter of `steps/<id>.md`, a step's definition:

| Key | Rule |
|---|---|
| `name` | string, required, equal to the file's name (`WFC-191`) |
| `description` | string, required, non-empty (`WFC-191`) |

A missing definition is `WFC-190`; an unknown frontmatter key or an empty
body is `WFC-191`. A definition still carrying `output` is refused with
its rewrite ([Removed keys](#removed-keys)).

### Removed keys

Contract 4 removed these; each is still `WFC-111` — a definition's `output` is `WFC-191` — with its rewrite in the message:

| Removed key | Rewrite |
|---|---|
| `step.inputs` | (contract 4) delete it: the step reads `artifacts/handoff.md` and the files its definition or skill names |
| `step.verifies` | (contract 4) delete it: write the check into the skill's read-and-verify-first section |
| `step.outputs` | (contract 4) delete it: the skill names the files it writes; evidence paths ride `pf step handoff --evidence` |
| `step.verdicts` | (contract 4) delete it: a gate passes with `--status done` and fails with `--status fail`; a `defer` becomes `done` with the reason in the body |
| `step.effects` | (contract 4) delete it: the skill runs `pf step hub file`, `attrs` or `batch`; list the verbs in `hub.verbs` and the attrs keys in `hub.attrs` |
| `step.handoff` | (contract 4) delete it: the skill runs `pf step hub wrap-attrs <name> @<file>`; declare `wrap_attrs: [<name>]` |
| `step.attrs` | (contract 4) delete it: write the behaviour into the step definition, or a `vars` entry |
| `on_fail.charge` | (contract 4) delete it: an automatic FAIL is charged; one handed off with `--per-owner` is not |
| `definition.output` | (contract 4, `WFC-191`) delete the schema: the definition's body says what the hand-off carries |

A key a contract before 4 removed is an unknown key (`WFC-111`, no
rewrite): a lane still carrying one targets an earlier contract and is
refused for its version (`WFC-101`).


## Properties

| Property | Read by |
|---|---|
| `acceptance` | whether a work item on the lane must list an acceptance criterion (`## Acceptance` or `## 验收`) before it runs (absent = required; none = the lane has no criterion to list): pf submit refuses an item without one only on a lane that requires it, the daemon's auto claim skips such an item only there, pf queue (and the Pi Web list, which reads its acceptance_missing) marks it only there, and pf file create prints the carried-by hint only there; a lane the reader cannot load counts as required |
| `exclusive` | one unfinished work item per project on the lane: the daemon's data-plane proxy reads it in the lane set of the project's workspace and forwards a work-item create on the lane (`pf file create --bundle <lane>`) to aihub exclusive, refused CORE-409 OPEN_ON_BUNDLE while another work item of the project on the lane is not wrapped, failed or canceled; nothing else reads it |

A property is a declaration the code reads, never a lane's name: nothing
finds a lane by its name. Contract 5 removed the `integration: true`
marker with its last readers (`pf ops integrate`, the daemon's integrate
status face and trigger); a `workflow.yaml` still carrying it is `WFC-111`.
Which lane a batch's fix tickets go to is the integrate skill's `--lane`,
and the pool and the hub writes are `pf step hub` verbs any step may be
granted.

## Placeholders

| Form | Renders |
|---|---|
| `{{<var>}}` | the step's `vars` (and `vars_files`) |
| `{{<fragment>}}` | `_shared/<fragment>.md`, inserted verbatim, never re-scanned |

Every placeholder resolves at load: one no var and no fragment resolves, a
var no placeholder uses and a var that shadows a fragment are `WFC-195`.
They are static text substitution at load, never runtime context. At the
claim, each step's definition, rendered with its vars and fragments as the
step's own prompt carries it, is written to the capsule's
`workflows/_rendered/<id>.md` (read-only, recorded in the snapshot's
marker): the copy a step's sub-agent reads.

## Skills

A skill is a markdown file of the lane snapshot a step names with
`skill: <name>`. Its frontmatter is the closed set below; its body is
prompt text, rendered after the step definition with the standing rule to
run each command line exactly as written (never a timeout of the agent's
own), route on the exit codes it names and do nothing it forbids. A skill
that resolves nowhere, a frontmatter outside the set or an empty body is
`WFC-200`.

### Skill keys

| Key | Rule |
|---|---|
| `name` | string, required, equal to the file's name |
| `description` | string, required, non-empty |
| `commands` | a list of command names (one word each), required; `[]` when the skill runs none |

A skill says what its step reads and verifies before it acts and what its
hand-off carries; every built-in skill (or, for a step without one, its
definition) has a section for each — `## 开工前读什么、核对什么` (what it
reads and checks before it starts) and `## 交接要写什么` (what its hand-off
carries) — which a test checks (the loader does not). A command
the skill runs writes a result file of one shape (`pf-step-result/1`,
[commands.md](commands.md), its JSON Schema in [Scripts](#scripts)): the
verdict is the exit code's. A value a later script needs (a version, a
head) is read from that result file, never copied out of a hand-off body;
the result file's path is the hand-off's `--evidence`.

## Scripts

A lane's scripts are commands the workflow carries itself: a shared one
is `_shared/scripts/<name>.py`, a lane's own `<lane>/scripts/<name>.py`.
They are part of the lane snapshot — the claim copies them into the
capsule with the rest of the lane (regular files, mode 0444, no symlink
followed) — so a script changes with its workflow, never with a pf
release. The daemon never runs, reads or judges a script; a skill runs it
on a command line written out in full, and a skill's `commands:` names it
by its file name (`commands: [verify.py]`).

**Invocation.** A script runs as

```
"$PF_PYTHON" "$PF_SHARED_DIR/scripts/<name>.py" --<flag> <value> …
"$PF_PYTHON" "$PF_LANE_DIR/scripts/<name>.py" --<flag> <value> …
```

never through its execute bit or a shebang (the snapshot is read-only and
a shebang names one machine's interpreter). Its flags are `--flag` only (a
script parses with `argparse`; Go's single-dash `-flag` is not promised),
and `--help` is its full reference: a skill does not repeat it.

**The environment.** Beside the step environment
([Environment](#environment)), every step process carries:

| Variable | Value |
|---|---|
| `PF_CAPSULE` | the capsule root |
| `PF_LANE_DIR` | `<capsule>/workflows/<lane>`: the lane snapshot the step runs |
| `PF_SHARED_DIR` | `<capsule>/workflows/_shared`: the shared tree beside it |
| `PF_PYTHON` | the interpreter's absolute path: `[daemon] python` ([config.md](config.md)), else `python3` on the daemon's `PATH`, resolved once at the daemon's start; a missing one or one older than 3.9 refuses the start, naming why (`pf doctor`'s `python3` row judges the same rule) |

**Python.** A script is Python 3.9 or newer and the standard library
only: no third-party package, no virtual environment. An entry script
opens with

```python
import os
import sys

sys.dont_write_bytecode = True
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from pfstep import result
```

(a lane's own script inserts `os.path.join(os.environ["PF_SHARED_DIR"],
"scripts")` instead): no `__pycache__` lands in the capsule's snapshot, and
the shared library is imported from the snapshot by path, never installed.
A test refuses an entry script without this opening. Every regular
expression is `re.ASCII`, so `\b`, `\d` and `\w` are ASCII as in Go's RE2
(Python's `\s` still takes a vertical tab, RE2's does not: write
`[\t\n\f\r ]` where that matters).

**The shared library** `_shared/scripts/pfstep/` holds what every script
does the same way: the result file (`result`) and the atomic write under
it (`atomic`: a temp file, fsync, rename, the directory's fsync), JSON in
the order it was built (`jsonout`), the step environment and its defaults
(`stepenv`), the deadline (`deadline`), Go duration strings (`duration`),
one command in its own process group (`proc`: the group killed whole at
the timeout, exit 124; a signal death 128+n), one hardened git call
(`git`: `-c core.fsmonitor=false -c core.hooksPath=/dev/null
--no-optional-locks --literal-pathspecs`, every inherited `GIT_*` dropped,
`GIT_CONFIG_NOSYSTEM=1`, `LC_ALL=C`, a timeout), a private TMPDIR whose
leftovers a killed run's successor sweeps (`tmpdir`), the secret
shapes a text is masked by (`secretpat`), and one command in a task
repo — `bash -c <line>` in `<capsule>/repos/<repo>`, on the host or in the
repo's verification container, killed at its deadline (exit 124), its log
`command.log` (`runner`) — with the container itself (`container`): the
image `pf-verify-<repo>:<hash>` built from the repo's
`.polyforge/Dockerfile.verify` at HEAD alone, the `pf.verify.*` labels
(owner `pf-step` — `gate.py`'s `pf-gate` — a fresh boot nonce, work item
`PF_WI_ID`, run, repo, `expires`), the limits a script passes (CPUs
min(8, cores), memory 16g, pids 4096 when it passes none), the clone and a
named cache volume as its only mounts, an allowlisted environment, and a
keep-alive that ends the container by itself five minutes after its
deadline, one script invocation — its flags parsed (`--flag` forms only),
its usage errors, its result written atomically and printed (`invoke`) —
the step's hub reads, `pf --json step hub <args>` on the `pf` of the
step's `PATH`, a typed refusal raised as such (`hub`), and the merge
mechanics the shared `sync.py`, `merge.py` and `revert.py` run (`gitmerge`:
git and gh as the lane's own identity, a repository's integrate.conf, the
default branch merged in place with its mechanical conflicts resolved, the
landing rules, reverts that keep the log, and the in-process projection a
projected pin is computed under, POSIX regexes leftmost-longest as sed's).
Beside them, `gotext`, `goglob` and `godecode`
keep what the scripts that took over Go step commands keep of Go's
behaviour: Go's whitespace, `%q` and `path` functions, `path.Match` and
the scope engine's shell-case glob (never `fnmatch`), and JSON read into a
typed shape as `encoding/json` reads it. The shared scripts are
`_shared/scripts/sync.py`, `merge.py`, `revert.py`, `run.py` and
`published.py`
([commands.md](commands.md#step-scripts--syncpy-mergepy-revertpy-runpy)),
and `facts.py` and `integrate.py`
([commands.md](commands.md#workflow-scripts--factspy-integratepy)).
Its unit tests are
`_shared/scripts/tests/` (`python3 -m unittest discover -s
workflows/_shared/scripts -t workflows/_shared/scripts`); CI runs them
under Python 3.12 and 3.9, on Linux and macOS, with `mypy --strict
--python-version 3.9`.

**A pattern a lane file hands a script** is a Python regular expression
compiled with `re.ASCII` and searched in each line: units.json's `marker`,
which `verify.py --unit-runs` matches against a unit's output, was Go's
RE2 before. What reads differently: RE2-only syntax (`\z`,
the POSIX classes `[[:alpha:]]`, `\pL`) is an error or another pattern in
Python, Python's `\s` also takes a vertical tab, and `$` also matches
before a line's final newline (an output line carries none). `verify.py`
fails a unit whose marker is empty, blank, no regular expression or one
the empty string matches (`.*`, `^`, `a*`) without running it: exit 0
alone proves nothing.

**The result file.** A script that judges or acts writes one result file
of schema `pf-step-result/1` — the shape the Go step commands wrote —
atomically to its `--out` path, and prints the same document on stdout
with `--json`. Its JSON Schema is
`_shared/scripts/schema/pf-step-result-1.json`: one object of exactly
these keys, in this order —

| Key | Value |
|---|---|
| `schema` | `pf-step-result/1` |
| `command` | the command or script that ran |
| `version` | the build that ran |
| `argv` | its arguments as given, the name excluded |
| `round` | `PF_ROUND` as the step environment carried it (`""` outside a step) |
| `started_at`, `ended_at` | RFC 3339, UTC, whole seconds |
| `exit_code` | the exit code |
| `verdict` | the exit code's: 0 `pass`, 1 `fail`, 3 `partial`, any other `error` (2: not judged) |
| `heads` | each repository it ran in → its HEAD commit |
| `summary` | one line |
| `details` | an object, the command's own |
| `logs` | the log files it wrote, capsule-relative inside the capsule |

The daemon never reads a result file: the hand-off's status and its
`--evidence` paths are what it records.

**Rules.**

- A process a script starts keeps `PF_WI_ID` and `PF_DAEMON_ID`: the
  daemon's sweep at a step's end finds the step's processes by them
  (`pfstep.proc` adds them back to an environment that dropped them).
- A script reads none of the daemon's private records: not
  `runtime-state/execution.json` (a test scans every file under a lane's
  `scripts/`).
- A value a later script needs (a head, a version, what was run) is read
  from the earlier script's result file — reused only from the current
  round (`round` equal to `PF_ROUND`) — never from a hand-off's prose.
- A script's deadline is the step environment's tightest bound
  (`PF_TOOL_WINDOW_MS`, and a claude step's `BASH_MAX_TIMEOUT_MS`) less two
  minutes, or less half the bound when that is smaller; 25 minutes without
  either — the rule of the Go step commands — so it ends, its processes
  killed and its result written, before the daemon judges the call hung.
- A script that starts containers first makes SIGTERM and SIGHUP raise
  (`pfstep.runner.raise_on_termination()`), so its `finally` removes them.
  One killed outright cannot: its container ends by itself five minutes
  after its deadline, and the next run's start removes every container of
  its owner whose `pf.verify.expires` has passed (`container.sweep_expired`).
- A command that could not run as asked — docker missing or not answering
  (`runner.EnvironmentNotSatisfied`), no Dockerfile in a container run, an
  image or container not had, a container lost mid-command
  (`runner.ExecutorError`) — is never an exit code or a verdict: the
  script exits 2 and its summary says why.

## The step interface

### Environment

The daemon starts every step process with `PF_WI_ID`, `PF_DAEMON_ID`,
`PF_DAEMON_SOCKET`, `PF_ROUND`, `PF_STEP`, `PF_SPAWN` and
`PF_TOOL_WINDOW_MS` ([config.md](config.md), "the step environment"), with
the four a step's scripts run from: `PF_CAPSULE`, `PF_LANE_DIR`,
`PF_SHARED_DIR` and `PF_PYTHON` ([Scripts](#scripts)). With `[daemon]
command_path` set, its `PATH` is the daemon's own `pf` directory, its
harness binary's directory and that command PATH — the one the daemon's
own git and gh run under — each entry once, so a script's commands
resolve what the daemon's resolve and neither `pf step` nor the harness's
interpreter is lost; unset, it inherits the daemon's own `PATH`
([config.md](config.md), `command_path`). A step's own copy of any of them
never reaches it.

### The prompt

Every dispatch's prompt is six parts, in this order, and nothing else:

1. **Identity and facts**: the work item, step, round and dispatch; the
   language to work in; the capsule root; the facts of this dispatch (a
   pi step cannot start subagents; where downloads go). The language is
   `[daemon] agent_language` (`en` or `zh-CN`), fixed per dispatch: a new
   conversation takes the value in effect when it is dispatched; a
   dispatch that resumes the previous conversation keeps that
   conversation's language. The dispatch's spawn-journal line records it
   (`language`).
2. **Prohibited actions**: unless the step's own
   definition explicitly requires it, never read or write the daemon's
   data or config, never create or change a `.claude/settings*.json`, a
   hook or a command-running git config key, never touch another work
   item's capsule or a workspace file outside the capsule, never kill a
   process the agent did not start. A soft mitigation (owner, 2026-11-10):
   nothing enforces it. Its text is `harness.StepProhibitedActions`; a
   lane cannot change it.
3. **The step header** (`_shared/step-header.md`): read
   `artifacts/handoff.md` first and how; end with exactly one `pf step
   handoff` and when to use each status; where the claim's recalled
   memories are (`recalled-memories.json`) and the memory policy to write
   by (`runtime-state/memory-policy.json`); take values for scripts from
   result files; never read `runtime-state/execution.json`.
4. **The step definition** (vars and fragments rendered at load).
5. **The skill**.
6. **On a resumed dispatch only**: the owner's undelivered messages, or
   "your last run exited without `pf step handoff`".

The daemon renders no input list, verification duties, output schema,
rewind input, prior round, rewind budget, memory text or delivery
contract: all of it is in `handoff.md` and the capsule's files.

### `pf step`

A step's one entry to the daemon. It works only in a step process: every
subcommand is one `POST /step/call` (`{verb, args}`), and the daemon
resolves the caller by its process tree, never by what it claims (any
other caller is `DAEMON-403`). Every call is first appended to the call
ledger `runtime-state/step-calls.jsonl`, then run; a call whose arguments
are wrong exits non-zero at once, naming why. The command's flags are
[commands.md](commands.md)'s.

| Subcommand | Kind | What the daemon does |
|---|---|---|
| `pf step handoff --status <s> --body @<file> [--evidence <path>]… [--to <step>] [--per-owner <at>] [--reason "<line>"]` | async: the ledger is the truth, the socket notice best-effort | checks the call, stamps the section's header, appends it to `handoff.md` and reacts to the status ([The daemon's reactions](#the-daemons-reactions)) |
| `pf step ask "<question>"` | async | records the question in `runtime-state/asks.jsonl`; the step ends its turn; the slot and resources are released; the owner's reply comes back into the same session |
| `pf step recall` / `pf step memory show` / `pf step remember` | sync | the step's own project's memories through the daemon (the step holds no aihub key) |
| `pf step hub <verb> …` | reads sync; writes pending | below |

**Statuses.**

| `--status` | Who | Meaning |
|---|---|---|
| `done` | any step | the step is finished (a gate: it passes) |
| `fail` | a gate only | the gate does not pass: rewind to `on_fail.target` |
| `blocked` | any step | stuck; a person is needed |
| `cancel` | with `--per-owner` | the owner ended the work item |
| `waive` | with `--per-owner`, a `waivable` gate only | the owner waived this FAIL: go on as if passed |
| `rewind` | with `--per-owner` and `--to <step>` | the owner sent the work back to `<step>` (`--to` the step itself = run it again) |

**Checks at the call** (a refused call changes nothing):

- one hand-off per dispatch; the process should end after it;
- `--per-owner <at>` names a message of `runtime-state/asks.jsonl` sent to
  this step in this round; the daemon checks that it exists, never what it
  says, and copies it verbatim into the section's header;
- `fail` from a non-gate is refused (use `blocked`); with `max_rewinds`
  spent, a `fail` without `--per-owner` is refused: ask the owner first
  (`pf step ask`) — so no work item parks on a spent budget;
- the body carries the template's seven sections ([handoff.md](#handoffmd))
  in the language the dispatch was told (its spawn-journal line; a line
  without one reads `en`), plus the re-entry heading (Reply to the
  previous round / 回应上一轮) when this step handed off before, in at most 3,000
  characters (detail goes into files; the body names their paths);
- every `--evidence` is an existing file inside the capsule.

**Hub verbs** — only aihub's generic routes, on the step's own project,
each granted by the step's `hub:` (any other is `DAEMON-403`). aihub keeps
no lane's business rules: what a lane's record may hold, and how it moves,
is the lane's scripts' to judge.

| Verb | Kind | aihub |
|---|---|---|
| `get <wi>` | read | `GET /work-items/{id}` |
| `list [--status <s>] [--bundle <lane>] [--attr <path>=<value>]… [--limit <n>]` | read | `GET /work-items` with its filters; each `--attr` is `attr=<path>:<value>` — the attr at the dot path (`^[a-z0-9_]+(\.[a-z0-9_]+)*$`, e.g. `integration.state`) is that string |
| `file --key <k> --lane <lane> --goal <text> --content <text\|@file> [--priority <p>] [--label <l>]…` | write | `POST /work-items`, not submitted; the body carries the marker line `integration-ticket-key: <k>`, and a work item already carrying it is answered instead |
| `attrs <wi\|key:K> --set <key> <json\|@file> [--if <path>=<value>]… [--per-owner <at>]` | write | `PATCH /work-items/{id}/attrs` (a signed machine route; any status, a terminal work item included): the top-level attrs key `<key>` — one of the step's `hub.attrs` — replaced whole by the JSON value (`null` deletes it). Each `--if` fences the write (compare and swap): the attr at the path must be the string value (in a `batch`, `if` maps a path to any JSON value; an absent path reads as `null`), else aihub refuses it `CORE-409` `ATTRS_IF_MISMATCH` and nothing is written — a refusal whose key already holds the value written is no failure (a replay). `--per-owner` names the owner message to this step in this round the write carries out, checked as a hand-off's is; the daemon copies that message's words, writer and stamp verbatim as the write's `owner_message`, recorded in its aihub event (`attrs_written`: the key, the signing machine, the owner message) |
| `wrap-attrs <name> <json\|@file>` | write, at the wrap | merged into the work item's attrs by its wrap |
| `batch <@ops.json\|->` | write | a JSON array of `file`, `attrs` (`{"verb": "attrs", "target", "name": <key>, "value", "if"?, "per_owner"?}`) and `wrap-attrs` writes, in order; an empty array is accepted and records nothing |

A write is never made at the call. It is recorded **pending** on the
ledger for the step's round — a dispatch that dies before its hand-off
leaves its writes to the next dispatch of the round — and made only when
the round hands off `done`, in declaration order, through the daemon's own
credentials; each write's outcome (a filed work item's id included) is
appended to the ledger. An `attrs` write names a work item filed by a
pending `file` of the same round as `key:<k>` — as its target or as any
string inside its value — resolved when the write is made. A round that ends `fail`, `blocked`, `cancel`, `waive` or `rewind`
drops its pending writes (the ledger records each `dropped`; aihub sees
nothing). A write that fails parks the work item blocked, kind
`write_failed`; `pf resume` (or an owner message) replays the writes not
yet done, each idempotent, then the lane goes on.
A landing record is a `wrap-attrs`: the work item's own record rides its
wrap. Security, one line: the hub
proxy gives an agent indirect aihub writes, bounded by each step's `hub:`
verbs and attrs keys (the owner's soft-mitigation policy, 2026-11-10).

### The call ledger

`runtime-state/step-calls.jsonl` is append-only, one JSON object per line,
fsynced per append; a torn last line is skipped. Every line has `seq`,
`at`, `kind` and the dispatch (`step`, `round`, `spawn`) as the daemon
resolved it. A `call` line carries the `verb` and its `args` as they
arrived; a `result` line names its call (`call`) and the `outcome` —
`ok`, `refused` or `failed`, with `code` and `error` — and, for an
accepted hand-off, `handoff` (`status`, `to`, `per_owner`, `reason`,
`evidence`, the task repos' `heads`, `candidate`, `started_at`), for a
hub write the `pending` writes, each with its id `<call seq>.<n>`; an
`exec` line names a pending write (`op`) and its outcome — `done` (with
`ref` for a filed work item), `failed` or `dropped`. The daemon's view of a step is this ledger, never
`handoff.md`: a step that edits the document changes no recorded status.

## handoff.md

`artifacts/handoff.md` is the work item's one hand-off document,
append-only. Only the daemon writes it — the opening, every section's
header and every event — and `pf step handoff`, which adds a section's
body. Every step, the owner and the panel read it (the daemon's
`GET /jobs/{wi}/artifact` serves it).

| Part | Written by | Holds |
|---|---|---|
| opening | the daemon, at the claim | the work item, its goal, the acceptance list verbatim, the lane's step order (each step the work item skips noted `skipped`, the last step that runs noted as the last step) |
| section header | the daemon | step, round, dispatch, start, end, duration, model, status; the events of the step's round (a model switch, a re-dispatch); the owner's messages to the step this round, verbatim; the message `--per-owner` cites, verbatim; each task repo's start and end commit and diff stat; the evidence paths |
| section body | the step, `--body` | the template's seven sections in the dispatch's language, plus the re-entry heading on a re-run |
| event | the daemon | a rewind (the steps to redo, why, the count n/`max_rewinds`); an owner command; the wrap |

**Stamp language** (`[daemon] agent_language`, `en` or `zh-CN`). Every
label the daemon writes is in one language, English or Simplified
Chinese (`## <step> · round N · dispatch M · <status>` /
`## <step> · 第 N 轮 · 派发 M · <status>`; `## Event · Rewind` /
`## 事件 · 回退`, `## Event · Close` / `## 事件 · 收尾`). A section's
header takes the language its dispatch was told (prompt part 1, recorded
on its spawn-journal line; a line without one reads `en`), so a running
step is never stamped in a language it was not told. The opening and the
events take the value set when the daemon writes them. A document whose
language changed mid-item carries sections of both.

The body template (`_shared/handoff.md`), each section a Markdown heading,
in the language the dispatch was told (prompt part 1; `pf step handoff`
checks that set only): Conclusion / 结论 · The question this step answers /
这一步要回答什么 · What was done / 做了什么 · Decisions and why / 关键决定和理由 ·
Verification / 验证 · Problems and risks found / 发现的问题和风险 · For
downstream, and what is open / 给下游与未决 — and on a re-run Reply to the
previous round / 回应上一轮 (point by point).

**Reading rule** (`_shared/step-header.md`; the daemon computes none of
it): read the whole document, top to bottom. A rewind event names the
steps to redo; the sections before it still stand as reference. A re-run
step answers the previous round's FAIL point by point; a re-run review
checks each earlier finding. A downstream step that finds an upstream
hand-off wrong says so in its own 发现的问题和风险. Facts are the stamped
header's; whether a claim holds is checked against the evidence files and
the code.

**Length.** Only the body is bounded: 3,000 characters per section.

**A rewind keeps everything.** Commits, result files and `handoff.md` all
stay. The rewind's target resumes its own previous session when it can,
else starts a new one and catches up from `handoff.md`; a review re-run
always starts fresh. Whether a re-run build really changed the code is the
review skill's to judge from the stamped start and end commits (a build
with a reason not to change it says so in its body).

## The daemon's reactions

| Event | The daemon |
|---|---|
| `handoff --status done` | makes the pending writes, marks the step done, dispatches the next; after the last step, wraps |
| `fail` (a gate) | appends a rewind event and rewinds to `on_fail.target`, charged to `max_rewinds` unless `--per-owner`; a gate with no `on_fail` (the release lane's three) ends the work item `failed_gate` |
| `blocked` | parks the work item blocked |
| `cancel` / `waive` / `rewind` | ends the work item canceled / accepts the gate as passed and goes on, recording the waiver / appends a rewind event and rewinds to `--to` (charged to nothing) |
| `pf step ask` | releases the slot and resources and waits; the reply resumes the same session |
| an owner message while the step runs | delivered into the session; the agent reads it and, for anything beyond its own step, hands off with `--per-owner` |
| an owner message to a parked work item | wakes the step below with the message; the daemon does not read it |

| Parked because | The message wakes |
|---|---|
| the step handed off `blocked` | its own session, resumed |
| two timeouts, or three exits in a row without a hand-off | the step that failed |
| a pending write failed (`write_failed`) | nothing first: the writes are replayed, then the lane goes on |
| a pause | the current step |
| no agent can start (a broken model configuration, no candidate left) | nothing: it stays parked; `pf watch` and the panel show why and the `pf resume` / `pf cancel` lines to copy |

A wake opens the same aihub door `pf resume` opens, with the owner's own
key, never the daemon's: `pf answer` first asks the daemon, which names
the park and records nothing; the command then opens the door (a blocked
work item's resume-blocked route, a paused one's `clean_pause` cleared);
then the daemon records the message and claims the work item, and the
step's next dispatch carries it. A park no agent can start is refused
(`DAEMON-409`, reason `WAKE_CANNOT_START`), nothing recorded, naming why
and the `pf resume` / `pf cancel` lines.

**The owner's commands** are `pf resume` and `pf cancel`, for a work item
no agent can start; the session's agent may run them for the owner.
Everything else the owner wants — cancel, waive, send back — is said to the
step, which hands off with `--per-owner`.

**Supervision.**

| What happened | The daemon |
|---|---|
| the process exited without `pf step handoff` | resumes the same session, reminding it to end with a hand-off; the third time in a row parks blocked |
| a model error (an error, a missing model, too slow, stuck) | the next candidate; none left parks blocked |
| a transient upstream failure or rate limit | retries the same model after a backoff (1 s to 3 min), charged to nothing |
| a timeout (the step's wall time, or one tool call hung) | resumes once with the limit doubled; a second timeout parks blocked |
| any other exit | resumes once; a second failure parks blocked |

The daemon does not park a rewound step whose tree did not change: the
review skill judges that from the stamped commits, and `max_rewinds`
bounds the loop.

**`runtime-state/execution.json`** is the daemon's own record — the
current step and status, why it parked or ended, a cancel request, each
step's state, rounds, attempts, candidates, dispatch ordinals, supervision
state, the rewind count, the time budget, a gate FAIL's repo commits, the
waivers and the contract version. Only the driver writes it; steps and
scripts never read it, and nothing in it is a contract with a lane. At the
wrap the daemon merges the done `wrap-attrs` into the work item's attrs and
writes `attrs.waivers`: every waiver of the run, the owner's words verbatim,
the agent's reading and the time.

**A step a project skips.** A step whose lane declares it `skippable: true`
runs unless the work item's project switches it off,
`steps.<project>.<step-id>.skip: true` in the workspace yaml
([config.md](config.md#stepsprojectstep-idskip--switching-off-a-skippable-step)).
The switch is fixed when the work item is first claimed: the claim writes
`runtime-state/skipped-steps.json` — the steps the capsule's lane snapshot
declares skippable that the yaml switches off, in the lane's order, an
empty list when none — only when the capsule does not carry the file yet;
a re-claim and `pf resume` clone the capsule chain, keep the file as it is
and never recompute it, so a switch flipped while the work item runs
reaches only the work items claimed after it. The driver reads that file,
never the yaml. A step it names that the snapshot declares skippable is
never dispatched: its execution status is `skipped`, the event log records
`step_skipped` (`step`, `spawn_n`), and the lane goes on to the next step
(a rewind that resets it to pending skips it again, with another event); a
name the snapshot does not declare skippable is dispatched as usual. The
work item completes when every step is `done` or `skipped`. The last step
that runs is the lane's last step: the opening of `handoff.md` notes it so,
and the `wrap_attrs` of the skipped steps at the lane's end pass to it — it
is granted `wrap-attrs` and its `done` is refused until it registered them
(develop's `publish`, when its project skips `merge`, registers the record
`published.py` writes, `integration: {state: published, …}`).

## Error codes

| Code | Meaning |
|---|---|
| `WFC-100` | `contract` missing |
| `WFC-101` | `contract` is not the version this daemon implements, or the lane is a workflow.md (contract 4 and earlier) |
| `WFC-110` | workflow.yaml missing, unparsable or not one mapping, a duplicate key, or a workflow.md beside it |
| `WFC-111` | unknown or removed key (at any level) |
| `WFC-112` | a value of the wrong type or outside its range |
| `WFC-113` | a required key is missing |
| `WFC-120` | `name` malformed or not the lane's directory name |
| `WFC-121` | `description` missing or empty |
| `WFC-122` | a user lane named like a built-in lane of its lane set (it never shadows the built-in lane) |
| `WFC-123` | (claim) the work item's lane is not in its workspace's lane set (the built-in lanes and that workspace's user lanes); the work item stays queued |
| `WFC-130` | step id malformed or duplicated |
| `WFC-140` | `on_fail` on a non-gate step, or a target that is not an earlier step |
| `WFC-190` | a step's definition file is missing |
| `WFC-191` | a definition file's frontmatter is missing, unparsable, carries an unknown key, or its `name`/`description` is wrong |
| `WFC-195` | a placeholder no var or fragment resolves, a var nothing uses, or a var shadowing a fragment |
| `WFC-198` | a model candidate is not `<harness>:<model>[:<effort>]`, or `model_tier` names no tier of `defaults.tiers` |
| `WFC-200` | `skill` resolves to no file (<lane>/skills/<name>.md, else _shared/skills/<name>.md), or its frontmatter is not {name == the file name, description, commands: []string} over a non-empty body |

Every code but `WFC-123` comes from the loader, and each has a lane that
produces exactly it under `internal/common/workflow/testdata/bad/` (or,
for the user-lane rules, `testdata/user/`). Contract 4 retired `WFC-141`
(verdicts, re-entry fragments), `WFC-172` (the integration lane's rules),
`WFC-180`–`WFC-182` (inputs and records), `WFC-192`–`WFC-193` (output
schemas), `WFC-197` (attrs), `WFC-204` (verifies) and `WFC-205` (effects,
hand-off fields): their keys are `WFC-111`. Earlier contracts retired
`WFC-131`–`WFC-133`, `WFC-150`–`WFC-152`, `WFC-160`–`WFC-162`, `WFC-171`,
`WFC-185` and `WFC-196`.

## User lanes

A workspace may carry lanes of its own: a user lane is a directory
`<workspace>/.polyforge/workflows/<lane>/`, laid out like a built-in lane
and judged by every rule on this page.

| Rule | What holds |
|---|---|
| the lane set | a work item's lane is resolved in its lane set: its project → the workspace this machine maps it to → the built-in lanes plus that workspace's user lanes. One daemon serving several workspaces resolves each work item in its own workspace's set |
| no shadowing | a user lane may not carry a built-in lane's name (`WFC-122`) |
| shared files | a definition is the lane's own `steps/<id>.md`, else the built-in `_shared/steps/<id>.md`; fragments come from the built-in `_shared/` alone; a skill is the lane's own, else the built-in shared one. A `.polyforge/workflows/_shared/` is not a lane and nothing resolves in it |
| a missing lane | a work item whose lane set lacks its lane is refused at the claim and stays queued (`WFC-123`) |
| the snapshot | the claim copies the resolved lane and the built-in `_shared/` into the capsule, less `_shared/scripts/tests/` (the scripts' own unit tests, which nothing at runtime reads), plus each step's rendered definition under `workflows/_rendered/` (its marker records `source: workspace` and the content hash); from then on the work item reads its snapshot alone. An explicit `pf resume` of a blocked work item is a re-claim and takes the lane as it is now, except over a snapshot of another contract version |
| a refused user lane | judged at the daemon's start and at every mapping change; a refused one is unavailable (one `user_lane_refused` event) and the daemon runs on |

A user lane may grant any hub verb and use the same `pf step` interface as
a built-in one, and declare `exclusive: true` like one; the contract has no
lane-only rule.

### A user lane, end to end

A two-step lane a workspace keeps for its notes. `workflow.yaml`, at
`<workspace>/.polyforge/workflows/notes/workflow.yaml` (its steps array is
the execution order; a `README.md` beside it would be narrative):

```
contract: 5
name: notes
description: Draft a short note from the ticket, then check it - a workspace's own lane.
steps:
  - id: draft
  - id: check
```

`steps/draft.md`:

```
---
name: draft
description: Draft the note the ticket asks for.
---

# draft

{{principles}}

Write the note the ticket's goal asks for to artifacts/note.md. Hand off
done with the note as --evidence; in the body's conclusion, give its gist.
```

`steps/check.md`:

```
---
name: check
description: Check the drafted note against the ticket.
---

# check

{{principles}}

Read artifacts/handoff.md and artifacts/note.md, check the note against
the ticket's goal and hand off done, listing what you found in the body.
```

`pf workflows validate` in the workspace judges it with the rest of the
lane set; `pf file create --project <project> --bundle notes …` files a
work item on it and `pf submit` runs it. `test/w433_user_lane_e2e_test.go`
runs these three files.

## When a lane is judged

| When | What happens |
|---|---|
| `pf [--json] workflows validate [--workspace <dir>] [<lane-dir>…]` | the same loader over the given lanes or the workspace's lane set; every defect printed; any defect exits 1 |
| `pf doctor` | the `workflow lanes` row: the built-in lanes load (fail), every mapped workspace's user lanes load (warn), queued work items whose lane is missing are listed (warn, `WFC-123`) |
| daemon start | a refused built-in lane is a release defect: the daemon does not start; a refused user lane only makes itself unavailable |
| claim | the lane must be in the work item's lane set and load; otherwise nothing is claimed. The claim materializes the bytes it judged and writes `handoff.md`'s opening |
| a start, restart or resume | a snapshot of another contract version parks the work item blocked, kind `contract`, naming both versions and the docs/upgrade.md section; a resume parks it again — cancel it and file it anew |

## Versions and the drain

Version 1 was the public freeze; 2 was workflow v2's additive contract; 3
removed the v1 keys; 4 made `pf step` the one entry and `handoff.md` the
one hand-off; this is 5, the lane as `workflow.yaml` (only the current
version's golden file and schema are kept; the earlier ones are in the git
history). `ContractVersion` moves by one rule: only an incompatible change
bumps it, at most once per release (owner ruling 2026-11-12). A change is
compatible when every lane valid under the last release stays valid and
behaves the same, and the work items that release claimed keep running —
a new optional key whose default is the present behaviour, a new value of
a key. A compatible change never bumps the version and needs no drain: the
registry's golden file (`contract.v<N>.golden`) and its baseline
(`testdata/contract.baseline.golden`) are regenerated in place
(`PF_WRITE_CONTRACT_GOLDEN=1`), the lane schema
(`workflows/_schema/workflow-<N>.schema.json`) with `go generate`, and this
page is kept in step. Any other change is incompatible: a key removed or
renamed, a key's meaning or default changed, a new required key, a value
removed, a check that refuses a lane it accepted, a capsule or hand-off
format the work items in flight rely on. It is registered when it merges,
one entry in `internal/common/workflow/contract-pending.md` (what changed,
what the migration takes); the registry pin is red on an incompatible
change the registry dump shows mechanically against the baseline (a
removed key, a new required key, a removed value: the dump's `[required]`
and `[values]` sections) while the list has no entry. The release that
follows a registration bumps the version once, in one change with the
golden file, the baseline, the lane schema, this page, every shipped lane,
a docs/upgrade.md section (the drain) and the emptied list; with the list
empty a release neither bumps the version nor drains. Since a compatible
change adds keys under the same version, a pf that meets a key it does not
know refuses it with `WFC-111` naming the key and asking to upgrade pf (a
newer release may define it). A version is deployed by draining:
stop claiming (`pf ops auto off`), let the work items in flight finish,
deploy, then cancel and re-file whatever is left blocked with kind
`contract`, and re-bind every queued work item whose lane the new release
no longer installs (`WFC-123`).

## Names in the code

No lane is special to the code that runs every lane: no non-test Go file
of the daemon, the driver, the runtime, the CLI, the daemon's aihub client,
the step call ledger, the pending writes, the workspace yaml,
`internal/common` or `internal/aihub` mentions the integrate lane —
`integrat` in any case mix, comments included
(`test/c5_lane_neutral_go_contract_test.go`). Its exceptions are a list of
lines, per file each line shape that may still mention it with the reason,
so a new mention elsewhere in an exempt file is red; an exception no line
matches any more is red too, so the list only shrinks.

No step id and no lane name lives in the code: a test of the driver, the
daemon and the CLI refuses a Go literal equal to a shipped lane's name or
step id, and renaming a lane, or a step its own directory defines, with
every reference to it, loads to the same execution graph. The daemon,
driver and runtime carry no integration-lane identifier either (`pool`,
`effects`, `landing`, `integration_batch` and the like — the contract 3
code still naming them, and the static test that pins their absence, go
with). Renaming `develop` and `integrate` together (and the
`--lane` the integrate skills pass) changes no behaviour.
