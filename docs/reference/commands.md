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
[--user ID] [--profile NAME] <verb> ...
```

Global flags come before the verb; `pf --version` prints the build
version (`pf --json version` adds the protocol `capabilities` list:
`cells-v1`, `config-host-cell-v1`, `cell-lifecycle-v1`,
`cell-migration-v1` — local, answerable with the daemon down; a
development-channel build — `tools/install.sh --dev`, polyforge#136 —
adds `commit`, the source commit the build was cut from, as one more
field of the same machine line, omitted on a release build whose tag
already names the commit).

**Profile resolution.** One daemon (the Host) serves several Cells, one
per AIHub identity domain ([config: Host and
Cells](config.md#host-and-cells--clitoml-and-cli-profiletoml)). Every
Cell-scoped command resolves its Cell deterministically, first hit wins:
`--profile NAME` → `PF_PROFILE` → the unique Cell whose effective mapped
workspace is the cwd or one of its ancestors (the Host's `GET /profiles`
index, symlinks resolved; zero or several matches fall through) →
`default`. No prompt, skill or model ever infers a profile. The Host's
effective index is the only source of the cwd step — no file on disk is
consulted, so a removed or edited `cli-<profile>.toml` never demotes a
command to `default` or another Cell. When that index cannot be read, a
business command fails `DAEMON-410` naming the effective workspace index
(nothing is sent; run `pf daemon ensure`, or name the Cell with
`--profile`). `pf profile` prints the resolution and its source (`flag`,
`env`, `cwd`, `default`; `disk` when no Host answers and the cwd matched
the on-disk files — the local fallback of `pf profile`/`pf config` only,
business never routes from it); it is refused inside a step or task. The CLI stamps the resolved Cell as the `X-PF-Profile`
header on every Cell-scoped daemon request; an unknown profile fails
explicitly (`DAEMON-404` naming the profile and its expected
`cli-<profile>.toml`) and nothing is sent to any other Cell. Inside a step
or task the CLI sends no header and refuses `--profile` (`DAEMON-403`): the
daemon routes an agent to the Cell that owns its work item or task. `pf help` prints the registry; `pf help <verb>` one verb's usage
line; `pf help extensions` the one-line index of the
[extension points](../extending.md). Exit codes: 0 success; 1
client-class (4xx, transport, local validation, and a sub-action's
flag/argument mistake); 2 server-class (5xx), an unknown verb, or a flag
error caught by the global / verb-level flag parse.

**Data plane through the daemon.** Every AIHub business verb — `file`,
`queue`, `cancel`, `recall`, `remember`, `memory`, `msg`, `users`,
`api-keys`, `machines`, `projects`, `backup`, and the aihub reads other verbs
make — goes to the local daemon, which forwards it through the resolved
Cell's effective connection (`ANY /aihub/{path...}`); the CLI holds no Hub
credential path and never dials a Hub. With the daemon down such a verb
fails explicitly (`DAEMON-410`, nothing sent); an unreachable Cell Hub is
`DAEMON-503` naming the Cell. `--server` and `--api-key` are refused on
every verb with the Cell-configuration route named, and so are `PF_SERVER`
and `PF_API_KEY` on every verb but the local repair verbs (`config`, `profile`, `doctor`, `init`, `migrate`, `version`): those run to their normal result under an
inherited variable, print one stderr line naming it (never its value) and
never dial it. Local commands — `pf config`, `pf init`, `pf profile`,
`pf doctor` (its Hub-dependent checks are skipped), `pf migrate`,
`pf version` — keep working with the daemon down.

**Work references across Cells.** The decision feed is Host-wide: `pf
watch decisions` lists the human decisions of every Cell, and each entry
names its `profile`, so equal work-item literals of two Cells never share
one. A follow-up on one work item (`pf answer <wi>`, `pf watch <wi>
--once`) resolves its Cell like every Cell-scoped command: `--profile`,
else `PF_PROFILE`, else the cwd's mapped Cell, else `default`.

**Host-scope commands.** `pf ops status`, `pf ops max` and `pf ops auto`
address the Host aggregate (the Host ceiling, every Cell's jobs and one
block per Cell; the aggregate's `tasks` counts — running, completed and the
terminal states — sum every Cell with a running daemon, so the Tasks badge
and re-read trigger cover all of them); `PF_PROFILE` and the cwd never change their scope — only
an explicit `--profile NAME` targets one Cell. `pf ops shutdown` is always
Host-level: it stops every Cell and refuses `--profile`; so is `pf ops
drain --upgrade [--force]` (polyforge#160): it sets the Host-wide upgrade
drain — new work is refused on every Cell until the daemon restarts —
and takes no `--profile`. `pf ops
unclassified` is always Host-level too: it lists the last 7 days'
fallback-classified error texts of every Cell, deduplicated with counts,
and refuses `--profile`.

## The verb index

One row per group, exactly as `pf help` prints it:

| `pf help` group | Verbs |
| --- | --- |
| Data-plane verbs (via the daemon to the Cell's aihub) | `file`, `queue`, `cancel`, `recall`, `remember`, `memory`, `msg` (`send`, `list`, `ack`, `show`, `analyze`, `contacts`) |
| Execution-plane verbs (local daemon UDS face) | `submit`, `watch` (snapshot, `decisions` — the machine's decision feed —, `dismiss`), `answer` (your words to the current step's agent — a reply to its question, an interjection while it runs, or a message that wakes a parked work item; no question id), `pause`, `resume`, `wrap`, `override` (a work item's per-step overlay: the view; `--step` with `--models`, `--clear-models`, `--tier <name>`, `--clear-tier`, `--skip`, `--unskip`, `--param <name>=<value>` or `--clear-param <name>`, one per call — the model chain over every source, a capability tier expanded through the Host `[step_tiers]` catalog by the next dispatch (an explicit `--models` outranks it; `default` means unset), the skip switch of a lane-declared skippable step, a step param the lane declares over its project and lane values; the parked (paused/blocked) ticket accepts chains and tiers too, holding on this machine only — not published until this machine's resume or claim publishes the capsule chain; a queued work item's params included, riding the reserved `step_params` attrs key until its claim, and its tier riding this daemon's local pending override record; polyforge#270: a models/tier overlay is accepted on a step that already ran or is running too — it takes effect on that step's next dispatch only (the finished round or live dispatch is unchanged, the view marks `model_effect_scope=next_dispatch`), while a step with no next dispatch and the skip/param actions keep their old refusals; a terminal ticket refuses), `params` (a work item's composed params read: every declared step param's final value and the layer it comes from — the per-ticket layer, the project's yaml or the lane default; a queued work item's per-ticket layer is its `step_params` attrs key), `ops` (`status`, `models`, `auto`, `max`, `role` — the interactive/runner machine role, `--force` takes interactive over —, `drain` — `drain --upgrade [--force]`, the Host-level upgrade drain: new submits, automatic claims and task spawns are refused `DAEMON-409` while running work goes on; an idle Host answers and it is safe to upgrade, a busy one refuses until `--force`; the refusal ends with the daemon restart —, `shutdown`, and `end-session` — aihub-direct, admin/maintainer), `task` (`spawn`, `list`, `show`, `cancel`), `step` (inside a workflow step only: `handoff`, its one result-delivery path; `ask`, its question to the owner; `recall`, `memory show`, `remember`; `hub`, its aihub reads and pending writes — below) |
| Management face (M1–M5) | `users`, `api-keys`, `projects`, `machines` (`list`, `rekey`), `backup` (database snapshot, see below), `cell` (the Host's Cells: `list`, `add`, `update`, `repair`, `remove`, `cleanup`; owner-only, below), `config` (the one `cli.toml` writer and the daemon reload entry), `workflows` (the read-only workflow face, below) |
| Diagnostics | `doctor` (examines every Cell — the default Cell's full report, then one section per named Cell, each Cell-level read carrying its `X-PF-Profile`; `--prune-scratch` removes the stale scratch batches inside the fixed categories (`raw/` past 3 days, `tasks/` and `sessions/` past 14 days, whole run directories; entries outside the categories are only ever reported); `--prune-chat-state` deletes the retired chat-subscription files the `retired chat state` row lists (`chats.json`, `chat-subs.json`, `chat-status/` and `migration/chat-subs.json.pre-migration` in the data dir, and the runtime dir's `chat-*` and `session-*.chat` files); `--prune-capsules` acts on the entries the `capsule retention` row calls reclaimable or retired — past the 14-day retention, holding no uncommitted or unpushed task-repo work: a wrapped work item's capsule, live or set aside, is slimmed to `artifacts/` + `capsule.json`, and terminal work items' external git dirs and failed or canceled ones' set-aside scenes are removed — a commit counts as pushed when either the task clone's own or the mapped workspace's `.repo/<name>` clone's remote-tracking refs reach it; a capsule holding such work, and the work item's git dirs, are kept and named; `--prune-rebuildables` removes the rebuildable build products the `rebuildable artifacts` row counted — the directories a repository declares at its root (`.polyforge/capsule-gc.conf`, one `rebuild_dir=` line per repo-root-relative literal; read from the mapped workspace's `.repo/<name>` clone's committed default branch, the commit recorded in the report) from the `repos/*` task clones of terminal work items' capsules (wrapped, failed or canceled, live or set aside) no daemon job holds, without waiting for the 14-day retention — only all-ignored directories with no tracked or untracked-not-ignored content and no nested repository, anchored to their clones, never touching `artifacts/`, `capsule.json`, the git directories, the local commits or the git status (a repository with unpushed commits loses its ignored build products and nothing else; a repository that declares nothing changes nothing; never while the daemon is down); its `disk watermark` row reports each mapped workspace's filesystem use and `capsules/` bytes and warns above 70% used; with `--json` the doctor additionally answers a top-level `cells` object — one entry per Cell of the disk set with the daemon's structured per-Cell harnesses facts (config roots, pi's step-agent auth.json as set/unset, the model-credential source and its kind incl. shared-default, claude's env-block keys as set/unset and its residual OAuth login as present/absent) and the Cell's execution-disabled reason, a probe_error naming why a Cell's daemon did not answer, values never appearing (polyforge#142, 85a2; the account page reads it); `--fix-harness-prompts` appends the native-SVG diagram-guidance block to each installed harness's existing context file that lacks that block — legacy mermaid-only guidance, which the `harness diagram guidance` row warns as `legacy`, included and left as it is — append-only and idempotent, never creating a file; it appends the concise-output guidance block too (the `harness concise guidance` row); `--fix-install-layout` links `~/.claude/skills/polyforge` to the machine's current skill (`<data-dir>/release/current/skills/polyforge` on a host, `/components/current/skills/polyforge` in the workstation image, polyforge#175) when the `claude install layout` row finds it missing or linked elsewhere — only that entry, idempotent, a no-op on a correct link; a copied directory or a missing release is left to the layout's installer (`tools/install.sh` on a host; in the image the row's fix names the components volume, never `tools/install.sh`), and it never installs `pf` or a release), `version` (the verb form of `pf --version`), `profile` (the resolved Cell and its source), `init` (a workspace's local layout) |
| Process | `daemon` (run the work machine; `pf daemon ensure` starts it), `supervise` (run the container process supervisor — the workstation container's entry point, below), `first-boot` (run the workstation container's one-time initialization — below), `aihub` (run the aihub server on the service host, and `pf aihub init` its local first-boot initialization entry; `main.go` intercepts `pf aihub`, this verb serves `pf help aihub` only) |

## `pf submit` — the prechecks; `pf queue` — the interaction marks

An execution ticket (one with a bundle) on a lane that requires acceptance
(the lane's `workflow.yaml` key `acceptance`, default `required`; the shipped
`integrate` and `release` lanes declare `none`) passes two CLI prechecks
before the local daemon is contacted — each with its own bypass flag, each
refusing with exit 1 and nothing claimed:

- **the acceptance check** — at least one criterion (a top-level list item)
  under `## Acceptance` or `## 验收`; `pf submit <id> --allow-no-acceptance`
  submits the ticket anyway.
- **the analysis check (polyforge#27)** — a `## 分析` (or `## Analysis`)
  section with content; a dated 「免分析」 the session writes on the owner's
  explicit skip counts as content; `pf submit <id> --allow-no-analysis`
  submits the ticket anyway. The check never judges what the section says.
  A ticket that already made release progress — its event history carries
  a session lifecycle event or a resume — is past the check: the
  `pf ops end-session` takeover resubmits exactly such a ticket with a
  plain `pf submit`, which the check no longer blocks (polyforge#47).

The daemon's auto-pull skips a ticket either check would refuse: it stays
queued, and `pf queue` marks it in the STATUS cell —
`queued (需要交互：缺验收)` (no acceptance criterion),
`queued (需要交互：未分析)` (no analysis section), and both marks when both
are missing. It also skips a ticket whose analysis the daemon's analysis
task wrote and the owner has not yet confirmed (the section still carries
the unconfirmed marker): `queued (需要交互：分析待确认)`. The merged `--json`
document (`pf queue` without `--project`) carries
`"acceptance_missing": true`, `"analysis_missing": true` and
`"analysis_unconfirmed": true` on such items (absent on every other); the
`--project` `--json` body is the server's raw answer and carries none.

**The analysis mechanism (polyforge#72).** `pf file create` of an execution
ticket on a lane that requires acceptance, whose content has no `## 分析`
section, asks this machine's daemon (`POST /analysis/enqueue`) to schedule
one analysis task: the output names the task id, says 分析已排队 (the task lane
is full, or older requests still wait — the request is persisted, survives
a daemon restart, and dispatches in enqueue order; an analysis a daemon stop
interrupted is re-queued at most twice, one the owner ended with
`pf task cancel` is not, and an entry whose ticket the daemon can no longer
read is dropped), or says 未安排分析 with the
reason (no daemon reachable, or the daemon refused) — the ticket is filed
either way. Only `pf file create` asks: a ticket a workflow step files
(`pf step hub file`) gets no automatic analysis. The analysis task follows the 7-item report format and writes
its draft with `pf task analysis-write`
(`pf task analysis-write --content <text|@file|->`), which replaces only
the analyzed ticket's `## 分析` section (a compare-and-swap on the ticket's
`updated_at`, retried on a concurrent edit) and marks it unconfirmed; it is
the one write a task may make. A draft with a line that would end the section (one that,
trimmed, starts with `# ` or `## ` — a code-block comment included) is
refused `DAEMON-400` and the ticket is unchanged: items use `###` headings. A task carries its idle and tool windows like
a step: a silent or stuck candidate is killed and the next candidate model
starts from the ticket as it stands; a stopped analysis leaves a comment
naming the cause and the last item written. `pf file analysis confirm`
(`pf file analysis confirm <id>`) strips the unconfirmed marker — the
owner's word that the draft is the ticket's analysis — and the ticket
becomes claimable. The prompt the task runs on carries the component
division of the ticket's repositories: the daemon runs
`workflows/_shared/scripts/components.py` (the shared scripts of its
lane checkout) over the repositories the ticket will clone — its frozen
repo_inventory, else the project's every repository — at the mapped
workspace's `.repo/<name>` clones, bounded, and attaches the tool's
line report between the template and the ticket; a missing clone, a
failing or stuck run answers one fallback line and the dispatch
proceeds, the template's default thresholds applying without the
division.

**Re-scheduling a stale analysis (polyforge#233).** `pf file analysis redo <id>`
re-schedules the pre-submit analysis of a queued or blocked execution ticket
on an acceptance lane — for an analysis in the old format, written against a
stale base, or after a materially changed scope. The daemon first copies the
active `## 分析` section verbatim (its heading included) into the ticket's
`## 历次分析` area, appended after the active section, and prefixes the
active section with a pending redo marker in the same content CAS; the
request is persisted before that CAS, so a restart continues the same round.
The scheduled analysis task runs the same prompt a create-time analysis
runs, writes through the existing narrow path, and its draft stays
unconfirmed (auto-pull skips the ticket, `pf queue` marks
`需要交互：分析待确认`) until `pf file analysis confirm <id>`. A running,
paused or terminal ticket, one with a local execution job, a running
analysis task or a pending analysis request is refused with the reason and
the ticket unchanged; a confirm before the task writes abandons that round,
and the task's later write is still refused. The strong guarantee covers
this machine's daemon only: another machine's auto-pull relies on the
ticket's unconfirmed marker, not on this machine's queue.

A third mark is lane-independent (polyforge#77): a bundle-bound parent waits
for each child that is not wrapped (aihub's `waiting_for` entries with
`via: "child"`, shown in WAITING-FOR as `<slug> (child, <status>)`), and a
queued parent waiting for a failed or canceled child — which never releases
it — has `queued (需要交互：子票失败)` beside any other mark; the merged
`--json` document carries `"child_failed": true` on it. The way out is
detaching the child, `pf file edit <child> --parent ""` (a running child is
refused `CHILD_RUNNING`); a refused `pf submit` of such a parent names the
same command beside `pf file dep rm` for an edge.

## `pf file create` / `pf queue` — one `--attr` shape

`pf file create --attr <path>=json-value` writes a dotted path nested
— `--attr split.origin=X` stores `attrs` `{"split":{"origin":"X"}}`
— and `pf queue --attr <path>=<value>` filters on the same dot path
(`?attr=<path>:<value>`, repeatable, all must hold). One shape, write
and read: a create never stores a flat dotted key — aihub refuses it
`CORE-400`, and the `detail` spells the nested object to send (the CLI
nests dotted `--attr` paths itself; a `--stdin` body carries the
nested object). The flag texts, the epic-split procedure that files
`split.origin`/`split.unit` markers and the error table live in the
skills: [verbs/file.md](../../skills/polyforge/verbs/file.md) (create,
the split), [verbs/queue.md](../../skills/polyforge/verbs/queue.md)
(the filter).

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
read-only analysis of one message: it runs as one task in the task lane
(fixed 5 min / 100000-token budget, read-only tools, visible in `pf task
list`) and the call blocks until it ends, answering a structured result —
`type`, `summary`, `suggested_actions`, optional `reply_draft`, `model`,
`duration` and the `task_id` (`pf task show <task_id>` reads the task); this one call has its own 6m30s client bound (every other verb keeps 120 s), outlasting the daemon's 6-minute wait. `pf msg prompt <id>` is built the same way (same task lane, project choice, read-only tools, budget and client bound) and asks the local daemon to restate one message as a self-contained execution prompt for another agent, from the message, at most the 10 most recent earlier messages of its conversation, its replied-to message and its anchored work item; it answers exactly `prompt`, `task_id`, `model` and `duration`, and a task that ends without a usable report is `DAEMON-500` naming the state and the task id, with no prompt text. `pf msg contacts` lists the people the caller can message: the
union, deduplicated by user id (first display name wins, mapping
declaration order), of the aihub project member face
(`GET /api/v1/projects/{id}/members`: human co-members other than the
caller, id + display name only, gated by the project read) over every
project the routed Cell's `[workspace.*]` mapping names (cli.toml for
the default Cell, `cli-<profile>.toml` for a named one), read at call time (no
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
reads the local `<data-dir>/machine-key` seed. `pf ops role interactive
--force` is the operator's explicit interactive supersession move (`pf machines
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

`pf projects deactivate <id-or-name>` and `pf projects restore <id-or-name>`
(`POST /api/v1/projects/{id}/deactivate|restore`) move a project's global
state: deactivated, the whole project is read-only on the server (creating or
modifying work items, comments, submits and claims — the daemon's auto-claim
included — resume execution, dependency writes and memory writes answer
`CORE-403 PROJECT_DEACTIVATED`; reads keep answering under the existing
permissions) and every default list hides it. Neither door deletes anything;
only `pf projects delete` below removes data. Deactivation is refused
(`CORE-409 NON_TERMINAL_WORK_ITEMS`, every blocker listed) while the project
still has queued, running, paused or blocked work items — settle the work
first. Both doors need a project maintainer or an admin; the console's
project picker restores in place from the marked row, and the CLI is
`pf projects restore <id-or-name>` (Pi Web's Work › 项目 view still implements
the same hide/mark/restore, but its rail entry is hidden since polyforge#226).
`pf projects list [--show-deactivated]` lists the active line by
default; the flag adds the deactivated projects with their marker.


Human moves: continue = `pf resume <id>` (a paused or blocked
work item), abandon = `pf cancel <id>` (queued, paused or blocked); to send
the work back to an earlier step or waive a gate's FAIL, tell the step's
agent (`pf answer`, below), which hands off `rewind` or `waive` citing your
words (there are no owner rewind or waive verbs),
owner's failed verdict on a live run = `pf wrap <id> --fail` (refused on
a blocked work item), done for a work item with no bundle (design,
decision, tracking) = `pf wrap <id> [--fail] [--note …]` (aihub records
it; no daemon needed). These run with the human's own api key: aihub's
door checks the work item's reporter, a project maintainer or an admin;
on a **blocked** work item, `pf resume` and `pf cancel` also admit the
current operator of the machine that last held it, as a project writer or
maintainer (paused resume and `wrap` are not widened). "Resume is a human verb" is a code
convention — only `pf resume` and the wake of `pf answer` (below) call
the blocked door, both with the caller's own key; the daemon never does —
with no hub-side check. On a Cell whose execution is disabled,
`pf resume` of a paused work item and every `pf answer` are refused
`DAEMON-409` with the Cell's reason (held work cannot
bypass the disabled state); the refused resume changes nothing — `pf resume`
reads the Cell's `execution_disabled` from `GET /status` before the hub's
`clean_pause` is touched, so the work item stays paused. A `clean_pause`
cleared without the Cell's admission (`pf resume` while the Host was down,
or from another machine) does not start it either: the disabled Cell's
restart keeps the operator-paused record paused (no attach, no run) until
`pf resume` is run again once the Cell admits it. Reads, `pf cancel`,
`pf pause` and work already running (or recovering from a crash mid-run)
are unaffected.
The shapes are the verbs' own fragments.

## `pf task` — one-shot task agents (the re-established #40)

```
pf task spawn --project <p> [--max-duration D] [--max-tokens N] "<prompt>"
pf task list [--json]
pf task show <id> [--json]
pf task cancel <id>
pf task analysis-write --content <text|@file|->   (a running analysis task only)
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
its four columns. A **running** task's `task list --json` row and its
`task show --json` document also carry `context` (polyforge#230): `tokens`
— the conversation's fill, counted from the attempt's own stream with the
same transcript fold the step lane's `context` uses (a pi `message_end`'s
finished message, wrapped as the transcript's `message` record, or claude's
`assistant`/`system` records stamped with their receive time; the last valid
reply's usage plus the chars/4 estimate of what follows it) —, `model`,
`as_of` (the record's time) and, only when the harness reported that model's
window (claude's `result.modelUsage`; a pi stream reports none), `window` and
`percent`. The fields are numbers, the model id and a timestamp: no
transcript text. The daemon clears `context` at the terminal transition (a
finished task never carries it) and at a candidate switch until the next
attempt reports its own; it is never persisted. The human `pf task show`
text output is unchanged — `context` is a `--json` field. It is not
`tokens_consumed`: that is the cumulative fresh budget spend, while
`context` is the fill of the current conversation.

- **Trust: the same as a session agent's.** A task agent runs with the
  daemon user's own filesystem and network reach — there is no OS-level
  sandbox, no read-only mount, no masked home (owner ruling 2026-10-26; the
  platform-level non-root design that would cover every agent class is).
  The harness runs in the selected project's workspace
  directory, so its clones are right there; it may read and write anything a
  human's session may. Its prompt opens with the prohibited-actions list every
  step carries (the daemon's data and config, Claude settings,
  hooks and command-running git config, other work items' capsules and
  worktrees, processes it did not start), scoped to that directory and
  excepting only what the task itself explicitly requires, then the concise
  output-discipline rule (`harness.ConciseRule`, the same bytes every step
  dispatch carries), then `# Task` and the task's prompt, and — since the
  scratch layout (polyforge#121) — a closing `# Scratch` section naming
  the task's own scratch directories — a soft rule the agent keeps, not a
  boundary;
  `list` and `show` keep the prompt as given. The harness tool allowlist
  (read/search, Bash and the web tools) is the lane's own scope, not a
  security boundary. A claude task runs a claude step's profile in every
  auth mode (`claude -p --safe-mode --setting-sources user` with the
  AGENTS.md plugin off, no cross-session messaging inbox), so the tools it
  has are exactly that allowlist.
- **The one capability gate is the write-verb guard.** The task process is
  spawned with `PF_TASK_ID` and `PF_DAEMON_ID`, and — for every task that
  may write — with `PF_SCRATCH_DIR` and `PF_SCRATCH_RAW_DIR`: the daemon
  creates `scratch/tasks/<task id>/` and the task's own raw batch
  `scratch/raw/<UTC time>-<task id>-<unique suffix>/` before the dispatch
  (a read-only errand runs without them and creates nothing), so the daemon refuses it
  `DAEMON-403` on EVERY mutating request to its Unix socket (spawning or
  cancelling a task, `/step/call`, job lifecycle, shutdown included) and
  `pf` refuses it every write verb locally before any config read or network
  call. The one exception is `pf task analysis-write` from a running analysis
  task: `POST /task/analysis-write` replaces only its own ticket's `## 分析`
  section (`docs/reference/uds-routes.md`). Its reads stay open: `pf recall` and `pf memory show` go through the
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
- **Budgets.** Wall clock 40m (ceiling 60m) and 1000000 tokens (the
  ceiling — a spawn can only lower the token budget),
  hard-killed on crossing; a larger `--max-duration`/`--max-tokens` is refused
  naming the ceiling. No yaml key changes them. The token budget counts
  **fresh tokens** per model call — uncached input, cache creation/write and
  output. Context the harness re-sends and the provider serves from its prompt
  cache (a cache read) was already paid for once and is not charged again, so
  the budget tracks the work a task does, not the turns x context it resends.
  A pi call's authoritative usage is the finished message's
  `message_end.message.usage` (polyforge#230): the per-call
  `message_update` top-level usage is the in-flight value and only stands in
  when the finished message carries none, so a pi stream that reports usage
  only at `message_end` is no longer undercounted.
- **Stuck detection.** Every task runs under the step lane's silence and
  tool-hang rules that apply to a task (rules 0, 2 and 3), with the
  daemon's supervision windows as thresholds. A candidate judged stuck is
  killed and the next candidate runs inside the remaining budget; each ended
  candidate is an `attempts` entry (`candidate`, `cause`, `evidence`) in
  `task show --json`, one `stuck: <candidate> (<cause>): <evidence>` line in
  the human `task show`, and is named in the decision feed's task summary.
- **States.** `running`, then exactly one of `completed`, `capped` (a budget was
  hit; the partial report is kept and marked `truncated`), `failed` (every
  candidate errored) or `interrupted` (`cancel` or a daemon restart; never
  resumed).
- **Limit.** At most `max_concurrent_tasks` one-off tasks run per Host
  (`[daemon]`, default 2, live on reload — every Cell's spawns share it);
  a spawn beyond it is refused `DAEMON-409`, never queued. The analysis
  tasks the daemon schedules itself (polyforge#72, `pf submit` above) share
  the same limit but are queued instead: the daemon persists the request and
  dispatches it when a slot frees, and re-queues an analysis a restart
  interrupted at most twice. Tasks take no
  `max_concurrent_jobs` slot (polyforge#74). A Cell whose execution is disabled
  (`execution_disabled` on `pf cell list`) refuses every spawn `DAEMON-409`
  `execution is disabled on this Cell: <reason>`: no record, slot or run.
- **Reports** live only on the machine (`<data-dir>/task-reports/`): the newest
  50 and the last 7 days. Nothing is sent to aihub or memory. A finished task
  records one entry in the daemon's decision feed (`completed`, or `failed` /
  `capped` / `interrupted`, which count as needing the owner) carrying the
  report's tail; pi-web's Polyforge panel shows it and `pf watch decisions`
  lists it. That attention ends on its own once the ticket no longer needs
  it: the entry names the ticket a daemon-scheduled analysis served and
  resolves when the ticket's analysis rerun completes, the ticket ends
  (wrapped, canceled, failed, or gone), its analysis section carries
  confirmed content, or the entry passes three days — written `resolved`,
  never `dismissed`.
- Inside a task, `pf recall`, `pf memory show` and `pf watch <wi> --once`
  work; every other `pf` verb is refused with `DAEMON-403`. The refusal is an
  accident guard, not a boundary: no aihub key is placed in the spawn env and
  `pf` clears it in task mode, but the shared `HOME` keeps `cli.toml` (and
  its `api_key`) readable.
- `pf ops status` carries one line: `tasks: <running>/2 running (completed N,
  capped N, failed N, interrupted N)`.

## The verification bar — `verify.py`'s mechanical run

`verify.py` — the workflow script `workflows/_shared/scripts/verify.py`
(the step command `pf-verify` before it), which the develop
lane's verification step runs from its lane snapshot as the verify skill
writes it, `"$PF_PYTHON" "$PF_SHARED_DIR/scripts/verify.py" --tier auto
--unit-runs artifacts/build/units.json --analysis artifacts/build/change-analysis.json
--json --out
artifacts/verify/result.json` (docs/reference/workflow-contract.md,
"Scripts"; `--help` is its full reference) — runs the tier the work item's
attrs decide — an integration batch's fix ticket (its `integration_fix`
marker) runs the full bar; every other ticket the light tier — over the
the task repos as the sync step left them: no fetch, no merge — a merge in progress
(`MERGE_HEAD`) or an unresolvable base branch makes the run incomplete
— and it skips a repo with no commit beyond its base branch (the capsule's
fixed target branch when it carries one, `merge-base HEAD origin/<target>`
then, else the default branch; a fixed target that cannot be named is
incomplete naming why, never a fall-back). The
step's `image` parameter (or `--image`; the flag wins and the result's
`image` record names the value and where it came from) names a prepared
image every touched repo's bar then runs in, Dockerfile or not — never
built, never pulled; a machine without it is not judged (exit 2, the
fixed mark `pf-verify-image-missing` in the summary, the
image named). The step's `audit`
parameter (the dispatch's `PF_PARAM_AUDIT`, default `on`; only the
lower-case `on` and `off`, empty or unset reads as `on`) turns the
dependency audit off: with `off` every scope-engine invocation is handed
`--audit off`, no full report plans the `audit` command, and the step's
result names the closure — the run's document and the result file's
details carry `audit_disabled` with the merged source hint
(`PF_PARAM_AUDIT=off，来自项目配置或 pf override`), the summary and the
mechanical report say the audit was off. Any other value is a named error
before any check or container starts (exit 2, the fixed mark
`pf-audit-param-invalid` in the summary, the value named). The union
with the build's change analysis is add-only: the change analysis's listed
tests run beside the plan, add-only, under the analysis's supplement cap —
a listed test that does not exist fails its check naming it, and the
entries past the cap are cut in listed order and named in the result.
The analysis's machine selection records (`selected`, one per task repo,
which the build close's `fast_checks.py --close-scoped` writes) are
judged by the same shared validator: a record of another tree is stale —
except that a sync's merge commits between its head and the verified HEAD
leave it valid, the change's own tree being what it was computed at — and
a record whose selection is not green or whose selected commands carry no
green evidence fails its `<repo>:selected` check; the records are never
subject to the supplement cap. (The
daemon's `verify` action, which ran the same bar in-process after fetching
and merging the default branch itself, left with the daemon actions.)
For each touched repo it asks the
scope engine (`--engine`, default the shared tree's
`$PF_SHARED_DIR/scripts/verify-scope`) for its plan and runs the WHOLE
plan through
`bash -c`, never stopping at the first failure; a command whose declared
prerequisite is missing is skipped (a required command naming a path, such
as `./bin/tool`, is looked up in the repo, any other name on `PATH`). Each
command runs with a private
`TMPDIR` and a timeout (the plan's, else 20m); a
command's process group dies with it. The private `TMPDIR` is a short
`/tmp/pfv-…`, the same name on the host and in the verification container
(never under the step's own `$TMPDIR`), so a unix socket a Go test binds
under `t.TempDir()` stays within the 107 bytes `sun_path` allows. Each
command's private `TMPDIR` is named, with its length in bytes, under its
repo's plan in `report-mechanical.md`, and carried as `tmp_dir` in
`result.json`'s `repos[].commands[]` and in the result file's
`details.checks[]`, to check the path and length behind the `<tmp>` the
failure output shows (failure signatures keep `<tmp>`). Every progress
line is said on stderr and, inside a step, appended to the step's progress
file (`pfstep.progress`, docs/reference/workflow-contract.md "Scripts";
Pi Web's progress card reads it while a command has not ended): each plan
command opens with `[i/n] <name> (<phase>): <cmd>` and closes with
`PASS (<duration>)`, `FAIL - exit <n> (<duration>), log <path>`,
`SKIP - <why>` or `not started - <why>` — gate.py's format. Failing tests are re-run at HEAD with
the command's own flags (a pass there is recorded `flaky n/m`); a failure
that exists on the base and fails there the same way on every base run, in
a throwaway worktree outside the capsule, is pre-existing. A command whose
every failure was explained that way passes only when, re-run whole once
in its skip form, it exits 0 — or, for the repo's declared Go harness
run directly (`sh <go_harness>`), when its own trailer counts every listed
test as run or excluded; a command with neither (a Makefile target, an
`&&` chain carrying a pre-existing failure, a runner without a skip
switch) stays failed. Neither relaxation reaches the engine's
dependency-audit gate (the full-phase command named `audit`): it declares
no prerequisite, so a missing audit tool fails it instead of skipping it,
and it reports no test unit, so a finding the base carries too is never
pre-existing — the gate's only exemption is the repository's ledger. A Go package whose test binary did not finish
normally (a panic, a timeout, an `os.Exit`, `-failfast`) is a failure of
its own beside its failed tests. A command the conf gives a unit re-run
template (`rerun=<name>|<template>`, docs/contributing.md) re-runs each of
its failing units by that template — one suite of an aggregate e2e command
alone — at HEAD and on the base (never past the data-race guard: in a run
that reported a race, a template without `-race` is no re-run); an aggregate
e2e runner's closing summary (every settled suite listed, the tally matching
it and the suite count the runner announced, `e2e/all: <N> suite(s)`) proves
its command complete the way the harness trailer does. The light tier
(`verify.py --tier auto`: light over every ticket but an integration batch's
fix, which runs the full bar) is the develop lane's
light verification: the engine is asked `--light` in each
touched
repo and in every nested engine root the diff reaches (a subdirectory with
its own tracked `.polyforge/verify-scope.conf`, whose commands run behind
`cd <root> &&`, named `<root>/<name>`; a nested root whose own light report
falls back to the full tier runs its full plan, and the run's tier is then
`full`, a `reasons` entry naming the root and why); a failing unit is re-run once; a
unit still red runs once on the baseline — the default branch's commit the
tested tree carries, `merge-base HEAD origin/<default>` (`baseline_ref`
`merge-base`; `merge_outcomes.<repo>.baseline` / `baseline_ref`); a command
proven
complete needs no skip form for a pre-existing failure (the light tier
carries no bar to a re-run); and every failing unit
records its file's `carries:` globs, repository-relative (`units[].carries`).
Every unit that failed its first run — still red, flaky or pre-existing —
carries `units[].failure` (also under `details.checks[].units[]`):
`browser_crash` when that run's output for it shows Chromium's renderer
crash (`Target crashed` / `Page crashed`, a suite's `ABORT` line inside its
block included), else `assertion`; `report-mechanical.md`
marks the crashes.
A repository whose conf declares parallel `group=` lines runs its full
tier's declared groups side by side (polyforge#253, polyforge#244's
declaration): each group in a container of its own (its container's own
notes under `<log dir>/group-<n>/`), a group's commands in plan order, the
fast gates before the grouped span and the dependency audit after it
serially in the repository's container; the commands, the checks, the units
and the command logs stay in plan order, the group containers are removed
when the bar ends, and the build's unit runs, the analysis's supplements,
the base runs and the confirming re-runs follow in the repository's
container. A repository that declares no group, one group, `full_parallel=1`,
or one that runs on the host keeps the serial bar byte for byte.
A wall budget (20m) bounds when those re-runs and base runs may still
start; a unit or command they no longer reach stays a failure — a failed
unit whose re-run never started (the budget spent, the deadline passed)
carries `units[].not_rerun: true` (also under `details.checks[].units[]`),
and `report-mechanical.md` lists those units by check under `Not re-run`.
The whole
run is bounded by `verify.py --deadline` (default: two minutes under the
step's tool window): no command runs past it — a command it cuts short or
that can no longer start is recorded failed — and the result is written
before it (`result.json` records it as `deadline`). A private `TMPDIR` left
by a run
that was killed too hard to clean up is reclaimed by the next run.

A command whose plan entry declares a JUnit report (the conf's `report=`)
is also located by that report after a failing run. The report path's
files are stamped (mtime, size, inode) before the command starts; only a
file the command created or changed is its report, never one another
command sharing the glob or an earlier run left. Each failed testcase there
is a unit (`<classname>::<name>`, kind `junit`, not re-runnable) unless a
failing unit the output located with a targeted re-run already covers it
(the same file, or the testcase named under it), which stays and is re-run
as before; a failing output unit without a re-run gives way to the
testcases that cover it. A missing, unchanged, unparseable or
failure-free report changes nothing, noted on the command's `report`
entry.

`--unit-runs <units.json>` (the develop build's
`artifacts/build/units.json`) also runs each unit's recorded run as
written — its `argv` as is, from its `cwd` inside its repo, where the
repo's bar runs — beside the plan, one check `<repo>:unit-run:<id>` each
(`argv`, `cwd`, `exit`, `log`; its unit under `units[]`, kind
`unit_run`): it passes on exit 0 with an output line its `marker`
matches — a Python regular expression (`re.ASCII`, searched in each line)
— a failing first run is re-run once (a pass records it flaky), and it is
never pre-existing. A unit's run is bounded by its self-reported `timeout`
in units.json, else the shared 10m default — a run its limit kills is an
evidence problem, the why naming the limit. A run naming a repo the capsule does not carry, an
empty argv, a cwd outside the repo or missing at HEAD, an empty or blank
marker, or one that is no regular expression or matches the empty string
(`.*`, `^`, `a*`) fails its check unrun; an unreadable or malformed file
is the invocation's (exit 2). `verify.py`'s exit codes: 0 every check
passed (a tree with no commit beyond its default branch passes); 1 a check
failed; 2 not judged (no engine, a merge in progress, an unresolvable
default branch, a container that could not be had or was lost, the
deadline, an interrupt) or the invocation unusable.

It writes `result.json` (envelope-ready `checks`, `pre_existing_failures`,
the merge outcomes and the parsed units;
each `repos[]` entry of an
opted-in repository — one whose conf declares `smoke_suite=` — also carries
the engine's full-tier `e2e_selection` `{mode, suites, uncovered_paths,
full_commands}`, which `report-mechanical.md` renders under its plan; and
`tier` `{requested, effective, reasons}`, whose `reasons`
— set only when a requested light run proved the full tier — has one
`repos/<name>: <why>` entry per repo and fallback reason, each rendered as
its own line of `report-mechanical.md`: each of the engine's full-tier
fallback diagnostics (those saying `falling back to the full tier`, e.g. a
failed `go list` with its command, exit and first output line,
or a changed stack config file such as `go.mod`; the
informational `--light` diagnostics are not reasons); a
repository whose tree carries `.polyforge/Dockerfile.verify` — or whose
step parameter `image` names a prepared image — runs its bar in a
container (docs/concepts.md), and that repository's `repos[]` entry then
carries `container` `{image, built, build_log, name, cpus, memory,
pids, lost}` (and, in the prepared-image mode, `digest` and `repo_digests`
— the image's local ID and its RepoDigests, `build_log` never); the limits are the script's: CPUs min(8,
cores), memory 16g, pids 4096) and `report-mechanical.md` into its run
directory (`verify.py --log-dir`, else `logs/` beside `--out`, else
`runtime-state/verify/<n>/`); the
command logs stay there too, behind a `.gitignore` that keeps the tree out
of the capsule chain. A red bar is data; when the tree could not be judged
(a merge in progress, an unresolvable default branch) the result records
`incomplete: <reason>`, never a verdict.

## The full test gate — `gate.py`

`gate.py` — the workflow script `workflows/_shared/scripts/gate.py`
(the step command `pf-gate` before it) — is the full test
gate: the scope-engine plan — the engine the gated tree carries
(`workflows/_shared/scripts/verify-scope`), else the lane's own copy —
with every command, the engine included, in the container of
the tree's `.polyforge/Dockerfile.verify`. The integrate lane's full-gate
step runs it from its lane snapshot as its skill writes it, `"$PF_PYTHON"
"$PF_SHARED_DIR/scripts/gate.py" --isolation container --keep-going --capsule . --pins
…` — the lane's gate runs every command, never stopping at the first
failing one, so a red batch records every failing command at once (its
triage, and the regate a fully flaky one triggers, the same); `.polyforge/release.sh` and `tools/green-tag` run the tree's own with
`python3` (docs/reference/release.md, "The test gate"; `--help` is the
script's full reference). `--isolation container` is required and the only
isolation a gate has: a tree without the Dockerfile at HEAD — and no
prepared image named — or a machine whose container engine does not answer
(the selected backend, `[daemon] container_engine`),
is not judged (exit 2, the reason said). The step's `image` parameter (a
dispatch's `PF_PARAM_IMAGE`, else `--image`, which wins and is recorded as
the value's source) names a prepared image every gated repo runs in — one
image for the project, Dockerfile or not, resolved to its local ID, never
built, never pulled; a machine without it is not judged either (exit 2,
the fixed mark `pf-verify-image-missing` in the summary). The step's
`audit` parameter (the dispatch's
`PF_PARAM_AUDIT`, default `on`; only the lower-case `on` and `off`, empty
or unset reads as `on`) turns the dependency audit off: with `off` the
engine is handed `--audit off`, no full report plans the `audit` command,
the result (gate.json) carries `audit_off` with the merged source hint
(`PF_PARAM_AUDIT=off，来自项目配置或 pf override`) and so does a green
tag's record — a tag of the other audit state is never reused, and a tree
whose own scope engine predates the `--audit` option is blocked by name
before any container starts. Any other value is a named error before any
container or check starts (exit 2, the fixed mark `pf-audit-param-invalid`
in the summary). The container's
limits are the script's flags (`--cpus` / `--memory` / `--pids`, default
min(8, cores) / `16g` / 4096; design r2 D7). `--parallel <N>` is the full
gate's parallelism, overriding the repository's declared `full_parallel`
(polyforge#244): a positive integer, `1` running the plan serially — the
parallel comparison's serial baseline and the manual override; any other
value is a usage error before any container starts. A repository that
declares no group runs serially whatever the flag says, and the plan a run
records names the parallelism it ran at.

- On the clone at the working directory (the release's run): progress and,
  with `--mark-green --summary <text>`, the green tag's name on stdout;
  exit 0 pass, 1 a command failed, 2 not judged, 3 passed but the tag was
  not written; `--plan-only` prints the plan alone and is no step result.
- With `--capsule <dir> --pins <file>` on every gated repo's pinned head in
  a batch capsule's clones (the pins: `revert.py`'s or `integrate.py
  collect`'s result — its `heads` and its details' `gated`; `--mark-green`
  tags each head once every repo passed, with the lane's own identity).
- With `--triage --gate <file> --batch <file>` it triages the red repos of
  that gate result instead: each failing unit re-run once at the red head
  (flaky when it passes), still-red units run at the baseline — the newest
  green tag merged into the head, else the parent of the batch's oldest
  merge — and pre-existing only by the verification's own rule, the rest
  attributed to the landed items of the integration pool (read through the
  step's hub, `pf --json step hub list`; `--pool <file>` names a snapshot
  file instead) by file, `carries:`, Go package and the scope engine's
  selection, else bisected over the default branch's first-parent commits
  since the baseline; the details class each unit `flaky | pre_existing |
  attributed | unattributed` with its item, root and evidence, and
  `gate_run` names the gate run triaged (the sha256 of the `--gate` file's
  bytes). Every unit flaky runs the full gate once more (exit 0 when it
  passes, with `--mark-green` tagged); else exit 1, the red stands. The
  fix tickets and attributions are the agent's, registered with `pf step
  hub batch` (the full-gate skill).

Its result files name the script — `command` `gate.py` (`integrate.py
close` reads only these), `version` `pfstep/1` — and keep the step
command's details: `{mode, pins, gated, repos{head, branch, result, tag,
tag_error}, tags, failed, incomplete, log_dir}` and the triage's; a run in a prepared
image records `image_digest`, `image_source` and `repo_digests` in its
`result` (the image's local ID, which named it — `flag` or `param` — and
its RepoDigests). A green
tag's `runner` is `gate.py`, its `runner_version` `2`.

## `pf step` — a step's one entry to the daemon

```
pf step handoff --status <done|fail|blocked|cancel|waive|rewind> --body @file
                [--evidence <path>]... [--to <step>] [--per-owner <at>] [--reason "<one line>"]
pf step ask "<question>" | pf step ask - (stdin)
pf step recall [--type T] [--tags a,b] [--limit N] [--any] <query>
pf step memory show <id>
pf step remember --type experience.wrap --title S [--tags a,b] <content | - | @file>
pf step hub get <wi>
pf step hub list [--status S] [--bundle L] [--attr <path>=<value>]... [--limit N]
pf step hub file --key K --lane L --goal G --content <text | @file> [--priority P] [--label X]...
pf step hub attrs <wi | key:K> --set <key> <json | @file> [--if <path>=<value>]... [--per-owner <at>]
pf step hub wrap-attrs <name> <json | @file>
pf step hub batch <@ops.json | ->
```

Inside a workflow step only: the step's one way to
reach the daemon, and through it aihub. Outside a step (no `PF_WI_ID` +
`PF_DAEMON_ID`) it refuses with exit 1 before any request. Every
subcommand that sends is one `POST /step/call` to the daemon
([uds-routes.md](uds-routes.md)), which resolves the calling step from its
process tree, refuses any other caller `DAEMON-403`, and appends the call
to the capsule's call ledger `runtime-state/step-calls.jsonl` **before** it
runs it, then what it came to (ok, refused or failed). The statuses, the
reactions, the hub verbs and the ledger are the [workflow
contract](workflow-contract.md#the-step-interface)'s; this section is the
command's face.

- `handoff` ends the dispatch, once. The daemon checks on the spot and
  refuses non-zero with nothing written: a second hand-off of the
  dispatch; `fail` from a step that is no gate (hand off `blocked`), or
  without `--per-owner` once the budget the FAIL's reason spends is
  spent — the lane-declared uncounted class's own cap
  (`uncounted_rewinds`) when the reason falls in that class, else the
  lane's `max_rewinds` (ask the
  owner first); `waive` from a gate the lane does not declare waivable;
  `rewind` without `--to` (a step at or before this one; this step's own
  id re-runs it); `cancel`, `waive` and `rewind` without `--per-owner` —
  the stamp (`at`) of an owner message to this step in its current round
  in `runtime-state/asks.jsonl` (the daemon checks it exists and copies
  it, never reads it); a body lacking a heading for any of the seven
  template sections in the language the dispatch was told (English:
  Conclusion, The question this step answers, What was done, Decisions and
  why, Verification, Problems and risks found, For downstream, and what is
  open; Simplified Chinese: 结论, 这一步要回答什么, 做了什么, 关键决定和理由,
  验证, 发现的问题和风险, 给下游与未决), or on a re-run of the step Reply to
  the previous round / 回应上一轮; a
  body over 6,000 characters (Unicode code points, counted over the
  entire body); an `--evidence` path that is not an existing
  file inside the capsule; `done` while a `wrap_attrs` name the lane
  declares for this step has no `pf step hub wrap-attrs <name>` of this
  round; `done` while the lane declares `fast_check_proof` and a task repo
  this round changed — its HEAD moved past the round's start, or the round
  recorded no start, and HEAD carries a commit no remote-tracking ref does
  — lacks a `pf-step-result/1` result at the declared path whose `verdict`
  is `pass` and whose `heads` carries the repo at its HEAD (the refusal
  names each such repo, the failing check and the declared command to
  re-run); a `done` that cites an owner message of the round with
  `--per-owner` is the owner's release of such a red and is accepted, the
  section's header recording the cited message; `done` while the lane
  declares `facts_proof` and a task repo this round changed lacks a
  `pf-step-result/1` result at the declared path whose `verdict` is `pass`
  and whose `heads` carries the repo at its HEAD (the refusal returns each
  hit the result lists, one line each, the tail named by count). Accepted, the daemon appends one section to
  `artifacts/handoff.md`: a header it stamps from its own records (step,
  round, dispatch, start, end and duration, model, status, the round's
  events, the owner's messages to the step this round and the cited one
  verbatim, each task repo's start and end commits with the diff stat, the
  evidence) over the body verbatim, then reacts to the status. The ledger
  records the accepted hand-off; editing `handoff.md` changes nothing
  recorded.
- `ask` records the dispatch's question in `runtime-state/asks.jsonl`
  (`{at, step, spawn, kind: "ask", text}`) under the dispatch the daemon
  resolved; the command prints **now end your turn**. `ask` registers no
  flags: `-h` and `--help` print its usage on stdout and exit 0 with no
  call, and a question whose first word starts with `-` (the lone `-`
  stdin marker excepted) is refused locally, exit 1, before any call —
  the refusal hints to put the `-` word in quotes inside the question, or
  to read the question from stdin with `-`.
- `recall`, `memory show` and `remember` are the calling step's own
  project's memories through the daemon (the step holds no aihub key);
  `remember` is the wrap-summary write — stamped with the dispatch's
  writer (kind `step`, its work item / step / attempt / model), and a
  record that cannot prove the calling dispatch is its current one
  refuses `DAEMON-400` and nothing is stored.
- `hub get` and `hub list` read aihub work items of the step's own project
  through the daemon; each `--attr <path>=<value>` keeps the work items
  whose attr at the dot path (e.g. `integration.state`) is that string.
  `hub file`, `attrs`, `wrap-attrs` and `batch` (a JSON array of writes,
  each `{"verb": "file" | "attrs" | "wrap-attrs", ...}`) write nothing when
  called: they are recorded pending, to be written in order only when the
  dispatch hands off `done` (a `file` idempotent by its `--key`; `key:<K>`
  names the work item a `file` of the same dispatch files) and dropped on
  any other status. `hub attrs` replaces one top-level attrs key of a work
  item whole with the JSON value (`null` deletes it); each `--if` fences it
  — the write is made only while the attr at the path is that string, else
  aihub refuses it `CORE-409` and nothing is written; `--per-owner` names
  the owner message to this step in this round the write carries out, and
  the daemon records its words, writer and stamp with the write at aihub.
  A step may call only the hub verbs its step's `hub.verbs` names (`attrs`
  when its `hub.attrs` names a key, and only those keys); with none, every
  hub verb is refused.

What follows an ask is the daemon's: an ask of the current dispatch that no
reply closes parks the step **awaiting the owner** — the `awaiting_owner`
event carries the question verbatim (Pi Web's attention channel), `pf
watch <wi> --once` prints it as the step's pending question, the job's
running slot and the step's resources are released, nothing of the
dispatch is judged (silence, the tool window and T are not counted while
the owner is asked), and the process stays resident for the warm window
(`[daemon] ask_warm_window`, default `15m`). The owner's reply — `pf
answer <wi> --text "<words>"`, or the job card's input box — reaches the
same session through the owner-message relay (below). A process that
exits with **neither** a hand-off **nor** an open ask is resumed in the
same session and reminded to end with `pf step handoff`; the third time in
a row parks the work item blocked.

## `pf answer` — speak to the current step's agent

```
pf answer <wi> --text "<your words>" [--by <user>]
```

At any time while a step's agent is running or waiting for you, or while
the work item is parked — no question id — the words go to that agent
(the owner-message relay; it also wakes a parked
work item, below). The daemon reads none
of them (P0): no option, no reserved word (`cancel` here is just a word;
`pf cancel` cancels). It records them first, then delivers them.

- **Recorded first.** One line appended to the capsule's
  `runtime-state/asks.jsonl`, keyed by its own `at`: `{at, step, spawn,
  kind: "reply", ask_at, text, by}` while the current dispatch's `pf step
  ask` is open (the reply closes the ask: the step is no longer waiting),
  `kind: "message"` otherwise (an interjection into the running turn);
  `step` and `spawn` are the step addressed and the dispatch current when
  you wrote. The `owner_message` event (`at`, `step`, `spawn_n`, `kind`,
  `ask_at`, `by`, `text` — the words verbatim) follows the line. `--by`
  defaults to the configured user. `--by daemon:auto-recommendation` is
  refused DAEMON-400 and nothing is recorded: that author is the daemon's
  own automatic reply to a low-star question card (every row ★/★★, after
  the Cell's `ask_auto_reply_timeout`), and no `pf answer` may impersonate
  it. The automatic line is delivered like a reply but is never an owner
  authorization (`--per-owner`, waive/rewind/force-fail, attrs reject it).
- **Delivered next.** The step process is alive — idle after its ask
  (inside the warm window), or mid-turn: the message goes into that
  process through its harness's own channel (claude: a stream-json user
  message; pi: a `prompt` command with `steer`), framed as `Owner reply
  to your question (ask <ask_at>) from <by>:` / `Owner message from
  <by>:`, then the words verbatim, then the delivery marker
  `[pf-msg:<at>]` on its own last line. The first line never starts with
  `/`, which pi would otherwise read as an extension command (and refuse
  on its steer path): an owner's `/foo` reaches the model as words.
  Idle, the message opens the agent's next turn at once (`started`);
  mid-turn, the harness queues it and hands it to the model at the turn's
  next boundary (`queued`: claude right after the current tool call, pi
  after the current assistant turn). A step that was awaiting the owner
  is running again from the acceptance: the warm window stops, the
  running slot is taken back, supervision judges again and the wait is
  charged to nothing (the whole wait is an observation gap: neither
  silence nor T counts it). The process is gone — the warm window ended
  it, it crashed, the daemon restarted: the daemon resumes the same
  session (claude `--resume`, pi `--session`) as a new dispatch of the
  step — a new `spawn_n`, the identity its hand-off is stamped with —
  whose first message is the undelivered owner messages: alone, when the
  conversation ended awaiting the owner (it holds the prompt and the
  question already); as the `# Owner messages` section of the full prompt
  when the session cannot be resumed (another harness, a gone session, a
  retry after a death, a daemon restart).
- **Delivered means observed.** A message counts as delivered only once
  its marker is seen in the harness's own stream — claude: the
  `command_lifecycle` `started` record of the message; pi: the user
  message carrying the marker; a resumed session: its first turn
  starting — never on the harness's "queued" acknowledgement, which does
  not guarantee the queue survives. Then one more line `{at, step, spawn,
  kind: "delivered", msg_at}` and the `delivered` event (`msg_at`, `step`,
  `spawn_n` — the dispatch that took it —, `kind`, `disposition`: started
  / queued / prompt, `confirmed_by`: lifecycle_started / user_message /
  turn_started, `command_id`). A daemon restart replays exactly the reply
  and message lines no delivered line names, into the step's next
  dispatch; a message whose step is over is never delivered.
- **What it prints.** `reply to ask <ask_at> recorded for step <s>
  (dispatch <n>, at <at>): delivered to the step's agent` — or `…
  queued by the step process (a turn is running); delivered at the turn's
  next boundary`, `… no step process is alive; the daemon resumes the
  session to deliver it`, `… the step process did not take it (<why>);
  the step's next dispatch carries it`; `message recorded for step …` for
  an interjection. The command waits at most 3 s for the delivery proof.
  `--json` answers the daemon's ack: `{status, at, kind, ask_at?, step,
  spawn_n, delivered, disposition?, note?}`.
- **Wakes a parked work item.** A work item parked blocked or paused is
  no longer refused: the message wakes the step concerned — the step's own
  session when it handed off `blocked`; the step that failed after two
  timeouts or three exits without a hand-off; after a failed pending
  write, the writes are replayed first and the lane goes on; after a
  `cleanup_failed` park (polyforge#200), the step-end process sweep
  re-runs first and the lane continues the way the park's reason says —
  a kept hand-off settles without re-running the step (no dispatch reads
  the words), a failed dispatch retries (its dispatch carries them), an
  open ask re-enters with the answer; after a pause, the current step. The daemon never reads the words to decide. The
  wake is the owner's door: the command first asks the daemon, which names
  the park (`{status: parked, park, wake}`) and records nothing; then it
  opens, with your own key, the door `pf resume` opens (a blocked work
  item's resume-blocked route at aihub, a paused one's `clean_pause`
  cleared) — a refused door is printed verbatim and nothing is recorded;
  then the daemon records the message like any other and claims the work
  item, and the step's next dispatch carries it. It prints `… recorded
  for step <s> (dispatch <n>, at <at>): the parked work item is woken —
  <how it continues>`. A work item no agent can start (no usable model, a
  candidate composition that failed, a snapshot of another contract)
  stays parked: `DAEMON-409` with reason `WAKE_CANNOT_START`, nothing
  recorded, naming why and the `pf resume` / `pf cancel` lines to run.
- **Refused, nothing written.** No step agent to talk to — the workflow
  is terminal, or the daemon does not hold the work item — `DAEMON-409`;
  empty words, or over 64 KiB
  (`DAEMON-400`); a request naming a question id or an option
  (`DAEMON-400`: the fields of an earlier answer shape, gone together with
  the reserved `cancel` answer); a disabled Cell
  (`DAEMON-409`).
- **The question stays the agent's.** The daemon never judges whether
  your words answered it: the agent reads them in its next turn and goes
  on, asks again (`pf step ask`), or hands off what you asked for — a
  cancel, a waiver or a send-back, citing your message (`--per-owner`).
  The decision feed's `awaiting_owner` entry resolves when the step hands
  off or a new ask supersedes it — not on your message. `pf watch <wi> --once` lists
  `owner_message` and `delivered` among the events.
- **Reach.** The daemon's `POST /jobs/{wi}/answer`
  (docs/reference/uds-routes.md): the owner's `pf` and Pi Web's job
  card; a step or task process is refused `DAEMON-403`.

## The commands a step runs — the workflow's scripts

Every command a workflow step's skill runs verbatim and routes on by exit
code is a script of the workflow itself, never `pf`: verify ("The
verification bar" above), sync, merge, revert, run and published
([Step scripts](#step-scripts--syncpy-mergepy-revertpy-runpy)), facts and
integrate ([Workflow scripts](#workflow-scripts--factspy-integratepy)),
gate ("The full test gate" above) and fast_checks
([Build's checks](#builds-checks--fast_checkspy) below), run from the lane snapshot as
`"$PF_PYTHON" "$PF_SHARED_DIR/scripts/<name>.py" …`. Their conventions —
the step environment they read, the result file, the shared library
`pfstep`, the container labels — are the
[workflow contract's Scripts section](workflow-contract.md#scripts), and
each script's `--help` is its full reference. pf has no step command of its
own and answers to no `pf-<name>`: `tools/install.sh` removes the
`pf-<name> -> pf` links an earlier install laid beside it.

Every script takes `--json --out <path>`: the result file (schema
`pf-step-result/1`) is written atomically and the same JSON printed on
stdout — `command` (the script's file name), `version`, `argv`, `round`
(`PF_ROUND`), `started_at`, `ended_at`, `exit_code`, `verdict`, `heads`
(each repo's HEAD), `summary`, `details` and `logs`. The verdict follows
the exit code alone: 0 `pass`, 1 `fail`, 2 `error`, 3 `partial`. Their
commands run under the step's own `PATH` (with `[daemon] command_path`
set, the daemon's `pf` directory, the harness binary's and the key's
value; else the daemon's own PATH), their deadline is derived two
minutes under the step's tool window (`--deadline` overrides), and their
containers carry the script's owner label and a fresh boot, a
`pf.verify.expires` deadline and a bounded keep-alive, so a container a
killed step left ends by itself and is removed by the next script's start.

## Step scripts — `sync.py`, `merge.py`, `revert.py`, `run.py`

The workflow's own scripts sync, merge, revert and run, and
`published.py`
(`workflows/_shared/scripts/<name>.py`; the conventions are
the [workflow contract's Scripts section](workflow-contract.md#scripts)): a
skill runs each as `"$PF_PYTHON" "$PF_SHARED_DIR/scripts/<name>.py" --flag
value …`, written out in full, and names it in `commands:` by its file
name; `--help` is each one's full reference. They change with the lane
snapshot, never with a pf release, and the daemon never runs or reads
them. Their result file is every script's (`pf-step-result/1`, `--json
--out <path>`, the verdict the exit code's), its `command` the script's
file name and its `version` the shared library's (`pfstep/1`); a step
after one checks the result file's `argv` against the line its skill
writes. They connect to no aihub and hold no credential: `merge.py` and
`revert.py` run the machine's own git and gh on the step's PATH, every
git call hardened (`pfstep.git`: no hooks, no fsmonitor) and committing as
the lane's own identity.

- `run.py --isolation container|host --repo <name> -- '<line>'` runs one
  line (`bash -c`, from the clone's root); `--plan-from <file>` runs a plan
  — a JSON array `[{"repo", "line"}]` — in order, stopping at the first
  failure. `--isolation` is required: `container` runs each line in the
  repo's verification container (a repo without
  `.polyforge/Dockerfile.verify` at HEAD is exit 2, never the host;
  limits CPUs min(8, cores), memory 16g, pids 4096), `host` on the host
  under the step's own environment. Details: `plan, deadline, runs[{repo, line, exit_code,
  duration_ms, timed_out, not_started, log, output_tail, image,
  container}], not_run[]`. Exit 0: every line exited 0; 1: a line failed,
  was killed at the deadline or never started past it, or the plan names
  no line; 2: a line could not be run as asked (no engine, an image or
  container not had, no Dockerfile in a container run, no such repo), the
  plan is unreadable, the invocation unusable.
- `sync.py [--continue | --abort]` merges origin's default branch into every
  task repo of the capsule, in place, in name order — or, when the
  capsule's fixed target branch is set (`runtime-state/target-branch.json`,
  a sub-ticket's), that branch in the default branch's place: only
  `origin/<target>` is fetched and merged, the record's
  `default_branch`/`default_head` fields carry it, and a file that is
  there but cannot be read, or a branch missing on origin, is not judged
  (exit 2) naming why, never a fall-back to the default branch. A
  branch with no
  commit of its own is fast-forwarded; the conflicts the merged branch's
  `.polyforge/integrate.conf` makes mechanical are resolved and the merge
  committed; a real conflict is left in progress and its files listed —
  the agent resolves them in place and runs `sync.py --continue`, which
  refuses a residual conflict marker, an unmerged or unstaged path, a
  changed mechanical resolution and a resolved text file that drops a line
  both parents (`HEAD` and `MERGE_HEAD`) carry, commits each merge and rewrites the same
  result file (`conflicts` emptied, `resolved` filled, `after` the merge
  commit). `sync.py --abort` abandons the run — what the sync agent runs
  before it hands off `rewind` or `cancel` on the owner's word: every
  merge the record left in progress is aborted (nothing reset; a merge the
  run already committed stays), the record is removed and the result is
  written marked `aborted` (a pending repo keeps its `conflicts` and an
  empty `after`), exit 0 also when nothing is in progress. The run's
  record, `syncing.json` beside the result, carries the step round it ran
  in (`PF_ROUND`) and makes a crash re-entrant: a plain `sync.py` that finds
  a record of another round supersedes it (its leftover merge aborted, the
  sync started over from HEAD, nothing reset); one that finds a record of
  its own round aborts a leftover merge, refuses a repo whose branch or
  origin moved, refuses (exit 2, nothing reset) a HEAD that moved past
  `before` with commits that are not the sync's own merge, resets any other
  HEAD that is neither `before` nor a recorded merge back to `before` (the
  commit kept under `refs/polyforge/discarded/pf-sync/<UTC time to the
  nanosecond>`, the namespace pf-sync used) and merges
  again; the record is removed when the run settles. Details: `round,
  aborted, repos[{name, branch, before, after, default_branch,
  default_head, fast_forwarded, mechanical[], conflicts[], resolved[],
  notes[], incoming[], discarded[], aborted}]`. Exit 0: every repo synced
  (or `--abort` done); 1: a real conflict is pending (or `--continue` found
  one still unresolved); 2: not judged (no task repo, a linked worktree, a
  fetch that failed, work committed on top of the run's record,
  `--continue` with nothing to continue).
- `merge.py --publish <publish.json> --verified <result.json>
  [--landing-out <file>]` merges the
  published PRs (`{repos: [{name, branch, pr_url, head, skipped_reason}]}`)
  at the heads the verification's result file carries, `gh pr merge
  --merge --match-head-commit <verified head>`, every repo judged before
  anything merges: a PR MERGED already is taken by the landing rules
  (its branch side is the verified head, `how:
  merged`; or, merged outside the lane, its merge commit is on the default
  branch and contains the verified head, `how: merged_elsewhere`); an open
  one must target the default branch, head the published branch and stand
  at the verified head, and origin's default branch must still be at the
  base the verification result records (`details.repos[].base`, when it
  records one) — the capsule's fixed target branch, when it carries one
  (`runtime-state/target-branch.json`, a sub-ticket's), is the default
  branch's stand-in in every one of these places, and a FAIL over it
  carries the same moved-base substring. No PR branch is updated with the default branch: a default
  branch that advanced after verification, or a forge refusal, is the sync
  step's again. Details: `repos[{name, pr_url, branch, head,
  published, merge_commit, how, branch_side, by_this_run, why}]`,
  `merge_commits`. In the human-merge mode (the work item's frozen step
  modes, `runtime-state/step-modes.json`, name this step `human` — the
  project declared `steps.<project>.merge.by: human`) the script asks the
  forge for nothing and never judges the default branch's freshness: a
  MERGED PR is taken by the human landing rules (the patch-id
  fingerprint: a merge commit carrying the verified head as its branch
  side, a rebase's replayed commits, a squash's whole diff) as `how:
  merged_by_human`, the landed commit set; a PR closed without merging,
  merged with content that does not match the verified change, or still
  open is exit 3 — the step asks the owner, never a FAIL back to `sync`,
  and the landed set is not taken. Exit 0: every PR landed; 1 (never in
  the human mode): a PR moved, is closed or
  retargeted, was merged elsewhere off the verified head, the default
  branch advanced after the verified version (origin's default branch is no
  longer the verified base), or the forge refused (the later PRs stay open; the merged ones are landed and a
  re-run takes them); 3 (the human mode only): a PR is not landed and the
  owner is asked; 2: not judged (an unreadable file, a published repo
  the verification names no head for, gh or git unreachable). On exit 0
  `--landing-out` gets the landing record (`{state: landed, repos[{repo,
  pr, head, light_verified_head, merge_commit, accepted}],
  merge_commits}` — or, the capsule's fixed target branch set, `{state:
  landed_on_target, target, …}`, a state no `integration.state=landed`
  filter takes: the verified head as the head and the light-verified
  one; a `merged_elsewhere` repo's head its branch side, `accepted:
  true`) — `merge_commits` naming, per repo, the commit set the change
  landed as, oldest first, each `{sha, merge}` (the lane's own merges are
  merge commits; a person's rebase lands plain replayed commits and a
  squash one plain commit, so the integration lane's `revert.py` reverts
  them with no `-m 1`) — which the merge skill registers with `pf step hub wrap-attrs
  integration`; on any other exit a stale one is removed.
- `published.py --publish <publish.json> --verified <result.json>
  [--record-out <file>]` records the published PRs of a work item whose
  lane does not merge them — develop's `publish` runs it when its project
  skips `merge` (the lane's last step then). It reads both files as
  `merge.py` does and checks each published repo's head against its
  verified head; it asks the forge nothing and merges nothing. Details:
  `publish, verified, repos[{name, pr_url, branch, head, published, why}]`.
  Exit 0: every published head is the verified one; 1: a published head is
  not (the PR moved after the verification); 2: not judged (an unreadable
  publish or verified file, a published repo the verification names no
  head for, a task repo missing). On exit 0 `--record-out` gets the
  published record (`{state: published, repos[{repo, pr, head,
  light_verified_head}]}`: the landing record's shape without a merge, the
  verified head as both heads), which the publish skill registers with
  `pf step hub wrap-attrs integration`; on any other exit a stale one is
  removed. The integration lane's collect takes `landed` records only, so
  a published work item never enters a batch.
- `revert.py --batch <batch.json> [--item <wi>]... [--ops-out <file>]`
  reverts items of an
  integration batch: the batch is the collect step's result file (its
  `heads` the pins, its `details.items` the landed items `[{id, slug,
  merge_commits: {repo: [sha]}, fix_tickets}]`, its `details.holds` the
  held items with their `merge_commits` and `landed_fixes`); each `--item`
  (id or slug) and its landed fix tickets — a batch item's in the batch, a
  held item's as the hold records them — form a group whose merges are
  reverted per repo, newest first, on `integrate/revert-<name>` by the
  default branch's integrate.conf rules, the PR opened
  and merged; the clone is then checked out at the revert's merge. The
  plan record (`--plan`; default `revert-plan.json` beside the result)
  makes it resumable and carries earlier rounds' reverts: a run first
  finishes every planned revert not yet done (an existing branch or PR is
  picked up, a merge already reverted skipped). Without `--item` and with
  nothing planned it only copies the pins. Details: `reverts[{wi, name,
  group[], repos[{name, merge_commits[], revert_pr, revert_merge}], done,
  why}]`, `heads` (the pins, each reverted repo's its newest revert merge
  — also the result's `heads`) and `gated` (the batch's `details.gated`
  and every repo a revert landed in; an empty batch stays empty). Exit 0:
  every planned revert done; 1: a revert
  did not land (the forge refused its PR, a code conflict), kept in the
  plan for the next run; 2: not judged (an unreadable batch or plan, an
  item neither in the batch nor held, git or gh unreachable). On exit 0
  `--ops-out` gets one hub write per done revert that landed — the item's
  whole record as the step's hub reads it (`pf step hub get`), moved
  under the lane's rules (`pfstep/integration.py`) with `{state:
  reverted, batch: $PF_WI_ID, revert_prs, revert_merges}` set:
  `{"verb": "attrs", "target": <wi>, "name": "integration", "value":
  <record>, "if": {"integration": <the record as read>}}` (`[]` when none; none
  for a record an earlier round already moved; a record the rules refuse,
  or the hub unreadable, is exit 2), which the revert skill registers with
  `pf step hub batch`; it needs `PF_WI_ID`, and on any other exit a stale
  file is removed.



## Workflow scripts — `facts.py`, `integrate.py`

Two of the commands a step's skill runs are the workflow's own scripts,
not `pf`'s: `workflows/_shared/scripts/facts.py` and `integrate.py`
(they replaced `pf-facts` and `pf-integrate`). They ship
in the lane snapshot (docs/reference/workflow-contract.md, "Scripts"), so
they change with the workflow, never with a pf release; a skill runs them
as `"$PF_PYTHON" "$PF_SHARED_DIR/scripts/facts.py" <kind> …` and names
them in `commands:` by file name, as develop's verification runs
`verify.py` ("The verification bar" above). `--help` is each one's full
reference. They write the same result file every script writes (`pf-step-result/1`,
its `command` the script's file name, `facts.py` / `integrate.py`, as
`verify.py`'s is: the files' readers check it), connect to no aihub and
hold no credential.

- `facts.py <kind>` computes the facts a review reads about a build's
  delivered tree: `plan-conformance` (the plan's `## Units` declarations
  against the diff both ways, and the plan's `## 已定对照` mapping against
  the work item's decided rulings — every section of its content opening
  `## 已定`: each item, numbered across the sections, mapped to a unit or
  a `none` reason; an unmapped ruling is red, named with its number and
  text; a ticket without such a section asks for no mapping section, an
  unmaterialized work item reads as no ruling and a corrupt one is not
  judged; a declared path is repository-root relative, and a path relative
  to a nested project root — a directory carrying its own tracked
  `.polyforge/verify-scope.conf` — names that root's file when the change
  base's or HEAD's committed tree holds it under exactly one such root: a
  repository-root path wins and an ambiguous relative path stays
  undeclared), `build` (each unit resolved, its commit
  pair test-first, its `test_weakened` hunks and, with `--replay`, its run
  replayed on the tree of its test commit — a unit declared `pre_existing_red`
  (its reproduction test already exists and is red before the change, so it
  reports no `test_commit`) replays on the tree of the change's base: red
  there is its red evidence, and a run that exited 0 there fails the unit;
  each replay is bounded by its own limit — the unit's self-reported
  `timeout` in units.json, else `--unit-timeout` (10m) — and a run its own
  limit kills carries no red evidence: an evidence problem, exit 1, the
  problem naming the unit and its limit (a red-state run must exit
  non-zero within its limit, never hang), while only a kill by the whole
  run's deadline stays not judged —
  where it replays: `--isolation host` on the
  host under the step's `PATH`, `--isolation container` in the
  repo's verification container (exit 2 when its HEAD carries no
  `.polyforge/Dockerfile.verify`), neither: the container when HEAD
  carries one, else the host; a replay is red only when its run failed
  on its assertions — one that could not run there, a build, setup,
  compile or import error, a missing module, or a line the repository
  itself declares as its toolchain's own (`not_runnable_rule=` in the
  `.polyforge/verify-scope.conf` governing the replayed run's cwd — its
  browser preflight's text, for example), in its output, is reported
  `not_runnable`, a difference to class (exit 1), never red), `carries` (every e2e suite the build added
  or changed declares a `carries:` that covers it), `analysis` (the change analysis's own
  close check — the same shared validator verification's `--analysis` check calls: every
  listed entry judged statically — its repo, its run's shape, its file at HEAD, its name
  inside it — and each taken one's run executed once at HEAD and judged by its marker,
  the cut tail named over the supplement cap; a red names the entry and the rule it
  violates, and the build must fix it before its hand-off; the machine selection records the
  analysis carries (`selected`) are judged statically by the same shared validator — a stale
  head, a non-green selection or a command without green evidence is red, never under the
  supplement cap — while a touched repo carrying none is the build step's own rule to demand
  and review's to judge) and `micro-bounds
  --plan <evidence.json>` (every changed path inside the repo's
  `micro_path=` at the change's base; the credential plan). Exit 0: the
  facts hold; 1: a difference to class; 2: not judged.
- `integrate.py collect` pins each task repo's default branch and takes
  the integration batch over the pins (a repo gated when its head is new
  and carries a landed merge no green tag covers; the batch is the landed
  items whose every merge is in the pins), names every other landed item
  with why, and judges each held item's hold; another open batch refuses
  it. `integrate.py close --batch <file> [--gate <file>] [--triage <file>]
  [--revert <file>] [--rulings <file>] [--decision <file>] [--calls
  <file>] [--ops-out <file>] [--record-out <file>]` decides the batch's
  result (green, red or empty; the triage of this very gate run — its
  `gate_run` the gate result file's sha256 — whose regate ran is the
  gate's result), names every landed item a green tag covers with the
  record it moves to, and assembles the batch's record
  `integration_batch`; on exit 0 `--ops-out` gets the moves as hub writes
  (`[{"verb": "attrs", "target", "name": "integration", "value": <the
  whole record, set over it>, "if": {"integration": <the record as
  read>}}]`)
  and `--record-out` the record, on any other exit a stale one is removed.
  `integrate.py records --in <file> --ops-out <file> [--pins <file>]`
  turns a step's record entries — `{item, fix: [ref]}` (a hold's new fix
  tickets), `{item, clear: <at>}` (a hold cleared on that owner message,
  refused while another batch of the lane runs), `{item, blame: [ref]}`
  (full-gate's attribution, the red heads `--pins`') — with the `file`
  writes beside them into the hub writes the step registers, each the
  item's whole record read now through the step's hub under the lane's
  rules (`pfstep/integration.py`); exit 1 when a rule refuses one
  (nothing is written), 2 when an input or the hub is unreadable.
  Both read the project's integration pool through the step's hub (`pf
  --json step hub list` / `get` on the `pf` of the step's `PATH`, for the
  capsule's `capsule.json` project and lane); `--pool <file>` names a
  snapshot file instead. Exit 0 when computed (refused and empty are
  facts of the details), 2 when an input is unreadable, a fetch failed,
  the gate step's decision disagrees with the gate files, or every judged
  repo passed and one carries no green tag.
- `scope_ready.py [--repo <clone>]… [--engine <path>]` (the workflow's
  script, no result `command` of a former step command) is the
  scoped-gates readiness check (`pf doctor`'s row until the engine moved
  into the workflow): whether the engine is there and each clone is
  scoped-ready — its manifests and `.polyforge/verify-scope.conf`, read
  only. Exit 0 ready, 1 not ready (each problem named), 2 when no clone is
  named or one is not a git clone.
- `components.py --repo DIR [--rev REV] [--json]` (the workflow's tool) is
  the epic-upgrade analysis's component division: each named clone's
  origin/HEAD (else HEAD) commit tree — never the work tree, never a
  fetch — read for the conf's `component=` declarations and `epic_*`
  thresholds and the generic division, answered as one block per
  repository (a line report, or schema 1 with `--json`): the components
  with their path ranges and sources, the commit read, the effective
  thresholds with their sources, and a per-repository error entry when a
  clone cannot be read. Exit 0 whatever an entry says (each named), 2
  when no `--repo` is named.
- `release.py --capsule DIR [--batches FILE] [--pre-release FILE]
  [--pins-out FILE]` (the release lane's script, polyforge#154) is the
  release step's green-batch resolver: the release object is the
  project's most recent green integrate batch — its pinned commits,
  never a branch head. It resolves the owner (the one repo of the
  capsule carrying `.polyforge/release.sh`), reads every wrapped batch
  of the integrate lane through the step's hub (`pf --json step hub
  list`, held to the release step's `hub` verbs; `--batches` names a
  snapshot file instead), picks the green batch with the newest
  `closed_at` and answers what the lane does: exit 0, verdict pass and
  decision `fresh` (the owner pin carries no release tag) or `resume`
  with its `version` (the owner pin carries exactly one `v*` tag — the
  same release resumed, whoever claimed it, never a second version of
  one commit), writing the pins file `--pins-out` names —
  `{batch, repos: {<repo>: <sha>}}, red_batches_after` when red batches
  closed after the green one (the release notes name them) — the
  procedure's whole input (`--pins`); exit 1, verdict fail, a named
  refusal whose summary begins with the code: `PF-RELEASE-NO_GREEN_BATCH`
  (nothing green to release), `PF-RELEASE-ALREADY_RELEASED` (two or
  more release tags on the owner pin, or a release tag the recorded
  pre-release list lacks on a commit that is not the owner pin),
  `PF-RELEASE-PIN_OFF_DEFAULT` (a batch pin no longer on its repo's
  origin default branch), `PF-RELEASE-UNPINNED_OWNER` (the batch's main
  names no commit for the owner), `PF-RELEASE-AMBIGUOUS_OWNER` (two or
  more owner candidates); exit 2, not judged: an unreadable input — the
  hub, a batch record whose `main` names a repo no clone is of or whose
  shas are not full commit ids, a fetch, an origin default branch that
  cannot be resolved. A capsule repo the batch does not cover is not
  part of the release (named in the details), and the pre-release list
  (`--pre-release`, default `<capsule>/artifacts/pre-release-tags.txt`)
  is only read, for its guard; `verify`'s immutability check keeps it.

The verification-scope engine is the workflow's too:
`workflows/_shared/scripts/verify-scope`, with its dependency-audit helper
`audit-deps` beside it. A step finds it in its lane snapshot,
`$PF_SHARED_DIR/scripts/verify-scope` (read-only there: `python3 <path>`) —
`verify.py`'s only engine when `--engine` names none; a hand run uses the
release checkout's copy.

## Build's checks — `fast_checks.py`

Build's per-unit bar and its on-the-spot unit check are one written
command, `workflows/_shared/scripts/fast_checks.py`, run from the lane
snapshot as `"$PF_PYTHON" "$PF_SHARED_DIR/scripts/fast_checks.py" …` and
named in the skill's `commands:` by file name. It ships in the snapshot,
so it changes with the workflow, never with a pf release; `--help` is its
full reference.

- `--repo <name>` is the per-unit bar: it runs the scope engine's
  `--json` report — the default report, the one the model ran by hand —
  in the repo clone and executes the commands whose `phase` is `fast` or
  `scoped` group by group — a command's group is the group its report
  bound it to (polyforge#244) else its report's root, the groups side by
  side at most `--parallel N` at once (the report's declared
  `full_parallel`, else default 2; one group, or `--parallel 1`, is the
  serial plan), each group's own commands in the plan's order — each in
  its report's `root` (a nested
  root the changed paths reach is a repository of its own: its report
  runs in that subdirectory), with each entry's own declared `timeout`;
  a command missing a declared `requires_env`/`requires_cmd`
  prerequisite is skipped and its reason recorded; the full phase never
  runs, and the run never stops at a failure. `--phase fast` narrows the
  run to the fast commands alone (the per-unit bar's own shape: the
  scoped selection is the close's, never the per-unit's), and a
  repeatable `--root <dir>` to the named engine roots — `.` the
  repository's own root, a nested root its directory, a name that is
  neither a usage error naming the roots there are — the roots a
  narrowed run never asks keeping their green entries carried in the
  result file; without the flags every root the change reaches runs,
  exactly as before. A green command whose key — its text, its root's
  subtree tree hash of the working tree (tracked modifications plus
  untracked, unignored files; the repository root's key counts the whole
  tree, nested roots included), its sorted declared prerequisites — the
  previous run in the same result file already proved is recorded
  `reused` and not executed, its evidence (the original log path,
  `exit_code: 0`) kept; a red, timed-out or flaky-confirmed result, or
  any changed key dimension, runs again, and a previous result file
  that is missing, unreadable or not a usable result is no cache: every
  command runs, never a usage error. A red command's failing
  units are located from its output and each re-run once by its derived
  targeted re-run (verification's own rules, `pfstep/rerun` and
  `pfstep/flaky`), and a pass is recorded flaky; a red command every
  failing unit of which re-ran flaky is then confirmed the way
  verification confirms its bar — by its first-run output's completion
  proof (`pfstep/completion`) or by one whole re-run exiting 0 — and
  only such a command turns green. A unit still red after its one
  re-run is judged on the change's baseline — the same judgment
  verification's pre-existing phase runs (`pfstep/preexisting.py`, one
  entry): a throwaway worktree of the merge-base, the report's declared
  base setup, then the unit's own targeted re-run there. The classes:
  `pre_existing` (the base fails the same way, its signature and the
  baseline commit recorded — not this change's red, the run may pass),
  `this_change` (the base lacks the test, or it passes there) and
  `undetermined` (anything the base runs could not settle — fail
  closed, the red rules hold). A pre-existing failure is excluded from
  its command's confirming re-run by the skip form
  verification uses (go `-skip`, `E2E_SKIP`, pytest `--deselect`); a
  command that cannot exclude it keeps a residual whole-command
  failure. Details: `repo, engine,
  reports[{root, mode, commands}], commands[{name, phase, root, cmd,
  exit_code, timed_out, not_started, duration_ms, log, skipped,
  skip_reason, tree, prereq_key, reused, carried, units[{kind, name,
  file, status, flaky, detail, class, signature, head_signature,
  base_detail, rerun_log, not_rerun}], confirmed, complete,
  not_confirmed}], baseline{commit, ref, setup, ran, note}, reds[{command,
  unit, kind, file, class, signature, base, detail}]`. Exit 0: every
  command that ran exited 0 or turned green through its flaky re-runs
  and confirmation, was reused, or carries only reds the baseline
  proved pre-existing; 1: a command stayed red after the flaky phase,
  its baseline triage or its confirmation — a red classified this
  change's or undetermined — or
  was killed at its timeout; 2: not judged (no such repo, the
  engine missing or failing, an unreadable report, a repo the engine
  planned no command for).
- `--repo <name> --close-scoped` (with `--reuse <file>`, repeatable, and
  `--unit-runs <units.json>`) is
  the build close's own mode: it plans the same light-tier selection verification's light tier
  plans (one shared implementation — the same engine
  `--json --light --base <merge-base>` request, the same choice, the same
  nested roots) over the repo's final tree and executes it in the
  repository's declared verification image when its HEAD carries
  `.polyforge/Dockerfile.verify` — the same isolation and image/tool-cache
  machinery verification's boxes use (the image built from the committed
  Dockerfile's bytes, reused while they are unchanged), one container per
  group for the selected commands (a container's reap kills every process
  in it after a command, so commands sharing one container would kill
  each other), the close's own container for the engine asks, the re-runs
  and the baseline's base worktree —
  and on the host otherwise, the result file recording
  which isolation ran (`details.isolation` `host`/`container`, with the
  container's own record in `details.container` when one ran), the
  per-unit `--repo` run always on the host; a container the close cannot
  build, start or keep is the run's error (exit 2, never a red of the
  change's, never a host stand-in);
  the close's order is fixed: the scoped selection green, then the facts,
  then the self-review, then the hand-off, and once the scoped selection
  has run a step that changes no code does not re-run it (the reuse key is
  the command text, the root's subtree tree hash and the declared
  prerequisites, so changing artifacts alone is not a code change) while a
  changed tree re-runs only itself and on a host close reuses the untouched roots by `--reuse` (a boxed close consumes none), and a
  red is reported from the result file's own baseline record
  (`details.baseline` and its `reds[]` classification), never reproduced by
  hand on the base worktree;
  once per touched repo, after its fast checks; a command whose reuse
  key — the same key the `--repo` run reuses by: its root, its text as
  its own report gave it, its root's subtree tree hash of the working
  tree, its declared prerequisites — a green-in-one-run entry in a
  `--reuse` result file of this repo already proved (never a red or
  flaky-confirmed one, never an entry an older run wrote without its
  key fields) is reused, not re-run — on the host close alone: a boxed
  close consumes no `--reuse` green evidence (the same-tree host run
  proves nothing inside the image), keeping the image and tool caches
  shared and an unusable `--reuse` file the error it always was — and a red command keeps the per-unit
  bar's one
  targeted re-run, its baseline triage and whole-command confirmation,
  the selected commands in each group's own container when one was
  declared, the engine asks, the re-runs, the baseline triage and the
  whole-command confirmation in the close's own container;
  `--parallel <N>` overrides the plan's parallelism (a positive integer;
  1 runs the plan serially — the comparison baseline and the manual
  override), for the per-unit and the close run alike;
  `--unit-runs` names the build's `units.json`: the files its test
  commits touched are protected, never pre-existing (a test this change
  authored is this change's red), an unusable file is exit 2; a selection the engine
  answers with the full tier, or a selected command that cannot run,
  records `incomplete` with the reason kept and exits non-zero, claiming
  nothing (the full bar stays `verification`'s; the owner decides a
  full-tier fallback). The result's `details.selected` is the change
  analysis's machine selection record — the repo's head, tree, base and
  default branch, the engine invocations, the conf's identity, the
  selection's paths and suites, every command with its evidence (or its
  reuse source), and the status: `green`, `pre_existing` (every red the
  baseline triage kept is pre-existing — nothing this change's, exit 0,
  the shared validator's green-equivalent evidence; `details.baseline`
  and `details.reds[]` record the judgment, same shape as the `--repo`
  run's), `red` or `incomplete` with its
  reason. Exit 0: every selected command green (reused or run), or its
  only reds pre-existing on the baseline; 1: a
  command stayed red after the flaky phase, its baseline triage or its
  confirmation, or the selection is red or
  incomplete; 2: not judged (no such repo, the engine missing or failing,
  an unreadable report, a repo the engine planned no command for, an
  unusable `--reuse` file, an unusable `--unit-runs` file, a declared
  verification container not built, started or kept).
- `--check-units <units.json> [--unit <id>]` is the on-the-spot unit
  check at record time: it re-runs the build's recorded unit runs as
  written (each unit's own `argv` in its own `cwd` of its own
  `repos/<repo>`) and judges each by `verify.py`'s own marker rules
  (`pfstep/unitruns`, the one implementation): a unit passes when it
  exits 0 and one output line matches its marker; a marker problem, a
  red run or no matching line — a marker wrong by one `$` against a
  suffixed output line — is red on the spot, the result naming the unit,
  its marker and the closest output line. A unit's run is bounded by its
  self-reported `timeout` in units.json, else the shared 10m default —
  a run its limit kills is an evidence problem, the why naming the
  limit (a red-state run must exit non-zero within its limit, never
  hang). A red unit declared `pre_existing_red` is re-run once, and a
  pass records it flaky with both logs; a unit the lane just wrote is
  never re-run — red it stays. Details: `units_file, units[{id,
  check, repo, cwd, argv, marker, passed, flaky, why, exit_code, log,
  rerun_log, closest_line}]`. Exit 0: every checked unit passed; 1: a
  unit is red; 2: not judged (an unusable units.json — an invalid
  self-reported `timeout` included — `--unit` naming no unit, a file
  naming no unit).

The logs of both modes go to `<log-dir>/<n>/<repo>/<seq>/command.log`,
one directory `<n>` per run (a later run's executions never overwrite a
reused entry's original log), the log dir being `--log-dir`, else `logs/`
beside `--out`, else the capsule's next `runtime-state/fast-checks/<n>`
(a .gitignore keeps them out of the capsule chain).

## `pf config` — cli.toml and hot reload

`pf config` is the one supported writer of `cli.toml` and the per-Cell
`cli-<profile>.toml` files. It is Cell-scoped through the profile resolver
(`default` → `cli.toml`, a named Cell → `cli-<profile>.toml`, where a
Host-only key is refused); `--host` (anywhere after `config`) selects the
Host scope — `cli.toml`'s Host-only keys — and is refused together with
`--profile`; `--config PATH` addresses that one file only. `reload` re-reads
the resolved Cell's file (`pf --profile NAME config reload`) or, with
`--host`, the Host keys. A changed connection drains: the reload reports a
same-identity `api_key` rotation `pending-drain` (the Cell refuses new
submits, automatic claims and `pf task spawn` `DAEMON-409` naming the pending
connection rotation — nothing is queued — until idle: no job record, nothing
queued and no running one-off task; held work blocks the switch, and no
admission can race it; then it switches by itself — `GET /profiles` shows
`draining`), and refuses a changed `server` or another user's key
("create a new profile"). An `api_key` removed from the file
(`unset api_key`) is reported `pending-restart` and on `GET /profiles`: the
Cell keeps its effective key until the Host restarts, then holds none and
refuses AIHub business naming the missing key (a named Cell's file without
one is invalid); a same-user key set later is `applied` at once. The verbs:
`get <key>` (effective value + source `flag|env|file|default`),
`set <key> <value>`, `unset <key>`, `list`, `workspace`, `validate` (no
write), `reload` (apply a hand edit) and `migrate`. A key is a flat name
(`server`, `claude_bin`, …), a `[daemon]` key (`max_concurrent_jobs`, …), a
`[summarize]` key (`summarize.models|language|interval_sec` — flat and
machine-level, refusing `--workspace`/`--cwd`), a step_tiers catalog tier
(`step_tiers.<tier> '<JSON list of <harness>:<model>[:<effort>] candidates>'`
— flat, machine-level and Host-only: `--host` or the default Cell writes
it, a named Cell's scope refuses it), a memory_policy list
(`memory_policy.<default|project>.record|skip '<JSON list>'`, which needs
`--workspace <mapped path>` or `--cwd <dir>` and writes only that path's
entry) or a whole `[workspace."<path>"]` section (`projects=a,b,c`).
`pf config list` is the one read face of the summarize keys, the
per-path memory_policy blocks and the `[step_tiers]` catalog; `get` refuses
them pointing at it. Writes preserve every
other byte, are crash-atomic, keep the mode (a new file is `0600`) and
pass the same validator the loader, daemon and doctor use; a refused
write leaves the file untouched. `api_key` is write-only: stdin only,
never argv, never emitted.

The running daemon applies live keys on the write (`max_concurrent_jobs`,
`cell_max_concurrent_jobs`, `step_timeout`, `stall_window`, `agent_language`, `claude_bin`,
`ask_auto_reply_enabled`, `ask_auto_reply_timeout`, `summarize.*`,
the `[step_tiers]` catalog — reported `applied` as one `[step_tiers]`
outcome; it has no daemon consumer, the classification itself says no
restart is needed), takes the
`[memory_policy]` family at the next claim, takes a `[workspace]`
mapping addition at the next claim (a removal/re-route with an in-flight
job is refused and the work items are listed) and reports `server`,
`api_key`, `daemon_socket`, `harness_root`, `operator_agent_dir` and
`python` as pending restart. `pf ops status` shows what is pending or overridden by
an explicit daemon flag. The exact keys, classes and outputs are on the
[config page](config.md); the reload route and its refused caller class
are on the [UDS routes page](uds-routes.md).

`pf config workspace --cwd <dir>` reads the mapped workspace's yaml, its
declared step candidates with each entry's `source`/`class` (and its `skip` when the file declares one, and its `params` when the file sets any — each name to its value) and the
`default` and `task` rows of every mapped project (one JSON document, no
`summarize` and no `memory_policy`; a yaml still carrying either retired
section errors with the file, the line and `pf config migrate`; see the
[config page](config.md)). `set`/`unset` also accept `--expect-hash`,
`--expect-state` and, for the workspace `.polyforge.yaml` keys —
`steps.<project>.<step-id>.models` and `steps.<project>.<step-id>.skip`
(`true|false`, refused for a step no lane of the workspace declares
`skippable`; class `next-claim`: fixed when a work item is first claimed)
and `steps.<project>.<step-id>.params.<name>` (a value of one line of at
most 256 bytes, refused when no lane of the workspace's lane set declares
the param on the step; class `live`: composed at every dispatch)
— and the memory_policy keys,
`--workspace`/`--cwd`; the CAS comparison runs inside the writer flock, and
a step-key write needs the running daemon and the workflows dir it names
(`CORE-501` otherwise; see the [config page](config.md)).

`pf config migrate` (no argument, no daemon) moves every mapped
workspace's retired `.polyforge.yaml` `summarize:` / `memory_policy:`
sections into cli.toml — values verbatim, cli.toml first, then the yaml
sections removed byte-preservingly — leaving a `<file>.bak-<UTC timestamp>`
backup beside each file it changes. It refuses with an itemized error and
no write when the values disagree (between workspaces, against an existing
`[summarize]`, or a path's existing memory_policy block) or a section is
invalid (exit 1); an interrupted run converges on the next one; with
nothing to move it prints `nothing to migrate` and exits 0 having changed no
byte. There is no reverse command (rollback: [upgrade.md](../upgrade.md)).

## `pf cell` — the Host's Cells (owner-only)

One daemon (the Host) serves one Cell per AIHub profile. `pf cell list`
shows them (`GET /profiles`); `pf cell add <profile> --server URL
[--model-source shared-default|DIR] [--workspace PATH --projects a,b]`
writes `cli-<profile>.toml` (the api key on stdin only — `--api-key` is
refused) and asks the Host to add it; `pf cell update|repair <profile>`
re-applies a Cell's file after `pf --profile <profile> config set …` or a
hand edit (as does `pf --profile <profile> config reload`); `pf cell remove
<profile>` stops the Cell — refused while it holds work, never `default` —
deletes its file and keeps its state directory; `pf cell cleanup <profile>`
prints what a permanent cleanup of a removed Cell would delete (its state
directory and the Host decision feed's entries naming it) and exits 1
changing nothing, and with `--confirm
<profile>` deletes them for good (never `default`, never a still-configured
Cell; no page control exists). No change restarts the
Host. A profile never changes its AIHub in place: a restore over another
server is refused with the new-profile route. Every `pf cell` verb is
refused inside a step or task (`DAEMON-403`); the shapes are the
[routing skill's fragment](../../skills/polyforge/verbs/cell.md).

## `pf migrate cell-layout` — the one-time per-Cell data layout (owner-only)

Local, owner-only and refused inside a step or task; `--apply`,
`--rollback` and `--restore-backup DIR --confirm` take the data dir's root
`daemon.lock` before their first check and hold it until their last byte
is written — they refuse while a daemon holds it or answers on the socket,
and a Host start meanwhile is refused on the same lock before it reads or
writes any Cell state (it names the lock and the running migration). `pf migrate cell-layout [--data-dir DIR]` prints
the plan — every item that would be copied from the pre-Cell root into
`cells/default/` (source, destination, size), the `layout-version` write
and every failed check — and writes nothing (nothing to move: exit 0, no byte
changed). `--apply` copies through a temporary dir, verifies each copy's
content hash, publishes it by rename (a partial earlier copy is replaced),
records the manifest under `migration/` and writes `layout-version` last; a
re-run of an interrupted apply converges. Once `layout-version` is
`cells-v1` the layout is complete: the plan is empty and `--apply` prints
that the data dir is already on the cell layout and exits 0 without
reading or writing any Cell state — nothing written after the migration is
ever replaced or deleted. `cells/default/migration-accepted-work` without a
`layout-version` is refused (exit 1, nothing changed): that state is
repaired forward, never re-planned from the pre-migration copy.
`--rollback` restores the pre-migration copy (removes `layout-version` and
`cells/default`) only when that return is
byte-exact: it is refused once `cells/default/migration-accepted-work`
exists — the Host stamps it when it first serves the migrated data dir,
before any Cell restores a job, queues a submit or records a task there —
and refused, naming each blocker and changing
nothing, while any `cells/<profile>` other than `default` exists, an entry
of `cells/default` is no migrated member (e.g. `effective-connection.json`),
or a migrated member differs from its root copy. (A retired
`chat-subs.json` is left as it is; `pf doctor --prune-chat-state` removes
it.) After that window,
`--restore-backup DIR` lists the data-loss scope (every live item a restore
replaces — the work recorded since the backup) and exits 1 changing
nothing; with `--confirm` it moves those items aside under
`migration/replaced-<n>/` (never deleted) and copies the backup in. The
plan fails (and `--apply` writes nothing) when the copies would not fit the
file system, naming the bytes needed and free; an `--apply` killed midway
leaves no `layout-version`, and the re-run rebuilds the staging dir and
replaces only partial copies. `pf doctor`'s `daemon supervision` row warns
on a pre-Cell data dir (naming this verb) and on a pre-migration copy kept
after the window closed. `--json` prints the manifest. The daemon refuses a pre-Cell data dir naming this verb; see the
[upgrade note](../upgrade.md) and the
[routing skill's fragment](../../skills/polyforge/verbs/migrate.md).

## `pf workflows` — the installed lanes, read-only; `validate`

`pf workflows [--cwd <dir>]` prints one JSON document (with or without
`--json`) read fresh from the installed bundle files on every call: the
daemon-reported `workflows_dir`, `daemon_reachable`, `reason`, and per
bundle (sorted; `_shared` is no bundle) its `source` (`builtin`; with
`--cwd`, the workspace's own user lanes are listed too, `workspace`), its
`defaults.model_candidates`
and its steps in declared order, each with its `model_candidates` (absent
when it declares none) and `skippable` (`true` when the lane declares the
step `skippable: true`, else absent). A bundle the one lane loader refuses is listed only in
`bundle_errors` (`bundle`, `reason`: every `WFC-*` defect); the rest still render. With `--cwd`
inside a mapped workspace it adds `workspace`, `projects` (the mapping's
projects, in order — the `[workspace]` mapping of the Cell the invocation
routes to: cli.toml for the default Cell, `cli-<profile>.toml` for a named
one) and, per step, `effective.<project>` — the effective
chain (`models`) and the level it came from (`from`), resolved by the same
precedence chain a dispatch uses (absent when no level governs, so the
harness default runs), and `skip` — `{<project>: true}` for each project that
switches the step off (`steps.<project>.<step-id>.skip: true` on a step the
lane declares skippable — the judgment a claim fixes; a skip on any other
step never shows; absent when no project skips it), and
`params.<project>.<name>` — `{value, from}` for each param the lane
declares on the step: its final value over the lane default and the
project's value, and the level it came from (`from` names `steps.<project>.<step-id>.params.<name>` for the project's
yaml value, else `bundle <lane> step <step-id> params.<name>` for the lane
default; a project value of a param the lane does not declare never shows,
and a work item's `pf override --param` is no part of it — its view lists
those); `defaults.effective.<project>` is the bundle's
project-level fallback and `task.<project>` the task lane's list.
`write_capable` / `write_reason` state whether a step-key write can land
now (the running daemon answers and names its workflows dir, which the
written step id is checked against). Every degradation is a
field and the verb exits 0: an unreachable daemon answers
`daemon_reachable: false`, a reason and no bundles; an invalid
`.polyforge.yaml` keeps the bundles and puts the loader's refusal in
`reason`. The step-key grammar is the [config page](config.md)'s. It is a
management verb: a step agent is refused, and it adds no daemon route.

### `pf workflows validate` — judge lanes against the workflow contract

`pf [--json] workflows validate [--workspace <dir>] [<lane-dir>…]` reads
each lane — its `workflow.yaml` — through the daemon's own loader and
prints every defect with its `WFC-*` code, file:line and YAML path
([workflow contract reference](workflow-contract.md)); any defect exits 1.
A lane that is still a `workflow.md` (the lane file of the earlier releases) is `WFC-101` with its
rewrite. With no lane
directory it judges the lane set of the workspace — `--workspace`, else the
mapped workspace holding the working directory, through the `[workspace]`
mapping of the Cell the invocation routes to (cli.toml for the default
Cell, `cli-<profile>.toml` for a named one; none: the built-in lanes
alone): the built-in lanes the daemon runs work items on (its workflows
dir; the installed release's when the daemon does not answer) and the
workspace's user lanes (`.polyforge/workflows/<lane>/`, [User
lanes](workflow-contract.md#user-lanes); a user lane named like a built-in
one is `WFC-122`). A lane directory inside a workspace's
`.polyforge/workflows/` is judged as that workspace's user lane; one there
that is no lane of the set — a typo, a `_`-prefixed directory such as a
`_shared` (a workspace has no shared tree) — is `WFC-110` and exits 1. The global
`--json`, placed before the verb (`pf --json workflows validate …`), emits
`{ok, contract, workspace?, lane_set?, user_lanes?, lanes: [{name,
dir, source?, ok, errors: [{code, file, line, path, message}]}]}`. It
reads files and the daemon's `GET /status` only; it adds no daemon route. `pf
doctor`'s `workflow lanes` row runs the same judgement over every mapped
workspace and lists the queued work items bound to a lane their
workspace's lane set does not carry (`WFC-123`).

## `pf supervise` — the container process supervisor (polyforge#160)

`pf supervise` is the workstation container's entry point (the local
run image, epic #84): the image's PID 1 (catatonit) runs it as its
direct child, and it starts and watches the machine's three long-running
processes as its own children — the daemon, Pi Web and, with
`--with-aihub`, the aihub server. It exists because a container has no
tmux and no systemd: `catatonit` forwards a stop signal only to its
direct child, so `pf supervise` is what forwards it to every child.

- **The children**: the daemon runs the canonical launch argv
  (`--config`, `-data-dir`, `-workflows-dir`; the socket and the machine
  id resolve inside the daemon exactly as `pf daemon ensure`'s own
  launch would), with the same environment allow-list `pf daemon ensure`
  launches it under — plus the marker
  `POLYFORGE_SUPERVISOR=container:pf-supervise`, the one fact that tells
  `pf daemon ensure` (a successful no-op: nothing to start — the
  supervisor owns the daemon) and `pf doctor` (the supervision row
  passes) that this is the container supervision. Pi Web runs
  `npm run start:lan` in its checkout (a container must bind 0.0.0.0 to
  be reachable; no checkout → Pi Web is not started, a line says so);
  the aihub server runs `pf aihub`. Logs: `<data-dir>/daemon.log`,
  `pi-web.log`, `aihub.log`. Inside the workstation image
  (polyforge#175) the daemon and the aihub server launch the components
  volume's current pf (`/components/current/bin/pf`, a literal path —
  exec resolves the current symlink afresh at every start, so an
  upgrade that switches current is live at the next relaunch), Pi Web
  runs with `PF_BIN` pinned to it, and the `--workflows-dir` /
  `--pi-web-dir` defaults sit under the same current root
  (`/components/current/workflows`, `/components/current/pi-web`; a
  host's `<data-dir>/release/current`).
- **The watch**: a child that dies on it — a crash, a `kill -9` — is
  restarted with a growing delay (5s, doubling per consecutive failure,
  capped at 60s, reset after a run that lasted 60s).
- **The end**: the daemon exiting 0 or 78 is NOT restarted; the run
  stops the other children and exits with that code, so the container
  stops with it (the 78 reason is in `<data-dir>/daemon.log`). The
  daemon exiting 75 (the restart-for-an-upgrade, polyforge#175: an
  upgrade switched the components volume's current — the trigger itself
  is #92's upgrade orchestration, the daemon package aliasing the same
  constant) is relaunched AT ONCE from the same current-resolved argv,
  with no failure backoff — the run never ends on it, the failure count
  neither grows nor resets, and a start that then cannot run (a missing
  current) backs off like any failure.
- **A stop** (SIGTERM/SIGINT, what `docker stop`/`podman stop` sends): the
  signal is forwarded ONCE to every child (a second daemon signal would
  hard-abort its shutdown and lose the checkpoint), and the daemon gets
  the `--grace` (default 60s, floor 45s) to finish its shutdown — its
  WIP pushes can take up to 30s — before anything still running is
  SIGKILLed. The outer container's stop timeout must be that grace plus
  a margin. Exit status: the daemon's own when it ended the run (0/78);
  0 when a forwarded stop finished inside the grace; 1 when the grace
  ran out; 2 usage.

On a host nothing changes: the daemon's supervision stays the tmux loop
(`pf daemon ensure`), and running `pf supervise` there is not what any
documented flow does.

## `pf first-boot` — the workstation container's first boot (polyforge#176)

`pf first-boot` is the workstation container's one-time initialization,
run by the image's entrypoint exactly once per container start, BEFORE
`pf supervise` (the identity must exist before the server starts, so
the server's own fresh-install bootstrap — usr_root and
bootstrap-admin.key — never fires on a first-boot-initialized store).
On a host it is never part of an install (tools/install.sh owns the
host path).

The stages, each published atomically to
`<data-dir>/workstation/first-boot.json` (the phase record; on failure
the phase is `<stage>/failed` with the reason — never a credential):

1. **components** — the image's seed (`/opt/polyforge/seed`) installs
   into the components volume (`/components`) through the seed
   transaction; no seed means a not-image-managed run and the install is
   skipped (`imageManaged` records which).
2. **identity** — the local mode (the default) runs the local
   initialization entry (`pf aihub init`, in process): the daily user,
   its API key and the local `workstation` project (the user its
   maintainer) in one transaction. The external mode takes the
   operator-given server and key instead and creates nothing anywhere.
3. **config** — cli.toml gains the default Cell: `server` (the local
   `http://127.0.0.1:8420`, or the external server), `api_key`, `user_id`
   (local), `[daemon] container_engine = "podman"` and the
   `[workspace."/workspace"]` mapping (the local project, or the
   operator-given existing project; absent in the external mode means
   no mapping). The write rides the config writer's own flock and
   atomic write, preserving every other row.
4. **harness** — the harness references point at `/components/current`:
   the pi agent dir's packages entry and the claude skill symlinks (a
   login, other packages and the user's own skill entries are never
   touched). The host installer (tools/install.sh) keeps its own merge
   toward `<data-dir>/release/current`; the two are independent and
   neither calls the other.
5. **done** — the record lands `phase: done` with the mode, the default
   Cell's name and server, `userReady`/`keyReady` (true only after the
   facts are verified — `keyReady` says issued-and-written, never
   authenticated) and `imageManaged`.

The gate (the settled Q6 plus the owner's ruling of 2026-11-20 on
review r1#1): a record saying done AND a cli.toml carrying the default
Cell skips the boot (the first choice — local or external — is made
exactly once); a cell with no record at all, or a done record without
the cell, is an inconsistency the boot refuses to start on, naming both
files; a default Cell with an IN-PROGRESS record is this boot's own
interruption and resumes — with the RECORD as the authority for the
choice: the mode, server and project come from the record, environment
inputs that disagree with it refuse the start, and the external
record's key input must be set again (a record never carries a
credential). Every stage is idempotent: an interrupted first boot is
retried by running the verb again — the user is found by its fixed
email (never duplicated) and the same-named key is revoked and
reissued.

The external mode's inputs ride the container environment (compose's
`WORKSTATION_AIHUB_SERVER`, `WORKSTATION_AIHUB_KEY` and the optional
`WORKSTATION_AIHUB_PROJECT`; the entrypoint's whitelist passes exactly
these three): a server means external, neither means the local mode,
one without the other is refused before anything runs.

## `pf aihub init` — the local first-boot initialization entry

A subcommand of `pf aihub` (not a CLI verb of its own): it opens the
aihub data dir's SQLite store in process — no network listener, no
HTTP route — and establishes the daily identity in one transaction.
It refuses to run inside a step or task environment and beside a
running server (the single-active lock), and the issued key's
plaintext lands only in the 0600 file its `--key-out` flag names —
never on stdout. `--data-dir` names the store (the server's own
default). On an initialized store the identity is verified, never
duplicated, and the same-named key is revoked and reissued. The
workstation's `pf first-boot` reaches it in process; a human never
needs it by hand.

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

**In the container (`pf supervise`)** none of this tmux machinery runs:
the workstation container's entry point is `pf supervise` (above), which
starts the daemon as its own child and restarts a crash itself. Under
that supervision `pf daemon ensure` is a successful no-op that starts
nothing (the machine-wide mapping check still runs — the no-op skips
only the starting), and `pf doctor`'s supervision row passes on the
daemon's `POLYFORGE_SUPERVISOR=container:pf-supervise` marker. A shell
opened with `docker exec` carries no marker and keeps the host
behavior.

**Git on the work machine.** The daemon's git calls that talk to aihub
with the session credential — the capsule chain push, the capsule clone
and the carrier ls-remote/fetch/push — run with no system or global git
config (`GIT_CONFIG_NOSYSTEM=1`,
`GIT_CONFIG_GLOBAL=<data-dir>/git/empty.config` — a daemon-owned empty
file, mode 0444, checked before each use and rebuilt when it is not an
empty regular file, so a `/dev/null` that is no longer the null device
cannot break these calls) and with
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
and lower case), `SSL_CERT_FILE`, `SSL_CERT_DIR`, `npm_config_registry`,
the XDG base dirs, the Go toolchain variables (`GOPATH`, `GOROOT`,
`GOPROXY`, `GOPRIVATE`,
`GONOSUMDB`, `GONOPROXY`, `GOFLAGS`, `GOTOOLCHAIN`, `GO111MODULE`,
`GOCACHE`, `GOMODCACHE`), the task-repo Git low-speed thresholds
(`GIT_HTTP_LOW_SPEED_LIMIT`, `GIT_HTTP_LOW_SPEED_TIME`), the docker
client's own routing variables (`DOCKER_HOST`, `DOCKER_CONTEXT`,
`DOCKER_CONFIG`, `DOCKER_CERT_PATH`, `DOCKER_TLS_VERIFY`): wherever a
docker client runs — a step command's own docker calls, and the
verification container's engine when the daemon selected the docker
backend (`[daemon] container_engine`; the rootful podman backend needs
none of them) — it reaches the same engine the operator's shell, and
`pf doctor`, do), `POLYFORGE_DATA_DIR`, `GOCOVERDIR` and the
`POLYFORGE_SUPERVISOR` marker, with the values of the shell that ran
ensure (the tmux server's environment — whatever shell first started it
— never reaches the daemon: the loop runs `env -i <allow-list> sh -c
<loop>`). Nothing else gets through: no `ANTHROPIC_*`, no `PF_*`, no
stale API key. A proxy or CA the daemon needs must therefore be set
where ensure runs (the harness session, or the install.sh shell).
`PF_USER_ID` and `PF_DAEMON_SOCKET` in that shell do steer ensure's own
config load (which socket it probes), but are not passed to the daemon: it
reads `cli.toml` and the `cli-<profile>.toml` files next to it
(`--config`). Step
agents see only this environment: claude steps additionally drop every
inherited `ANTHROPIC_*` (their credential and endpoint come only from
`<harness-root>/claude-config/settings.json`); pi steps need their
provider keys in `auth.json` or as literals in `models.json` — a
`$NAME` there, or a provider `*_API_KEY`, that is not on the list never
reaches them. The `[step_tiers]` catalog (cli.toml's step tier list,
polyforge#143) is a recommendation, never an admission rule: it names
which candidates each step tier should run — doctor's `step tiers
catalog` row warns when a user-written candidate (a workspace yaml
`steps.*.models` or `tiers.*.models` list, or the `[summarize]` models)
sits outside it — while a pi candidate's admission stays the Cell's
`step-agent/models.json` hard check (`pf override --models` answers
`DAEMON-400` for a model it does not declare, catalog or no catalog;
claude candidates are not file-checked). `GET /status` reports the daemon's environment variable
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

**Integration:** an integration batch is a work item on the `integrate`
lane, filed by a person like any work item — `pf file create --bundle
integrate --project <p> --goal <text>`, then `pf submit <batch>`; the daemon
files none of its own. The local daemon runs `collect` → `repair` →
`review` → `revert` → `full-gate` → `close` (`workflows/integrate/README.md`,
[concepts](../concepts.md#the-integration-lane)): it pins each repo's default
branch, gates the pinned heads once and tags a green one, moving the landed
work items it covers to `merged` — and when the gate's red is fixable by
touching only tests or documentation, the batch repairs it in place: the
middle two steps (`rewind_only`, skipped until the gate's FAIL hands the
batch back) fix the red units on one branch per repo and land it under the
merge lock, so one batch covers the fix instead of a fix ticket. The lane declares `exclusive: true`, so
the daemon's proxy forwards its create exclusive and aihub refuses a second
batch of the project while one is not finished — atomically, across
machines: `pf file create` answers `CORE-409` naming the open
batch and its status, and for a batch still `queued` that no machine ever
claimed (its filer stopped between filing and submitting it) adds when the
batch was filed and the way out: `pf submit <batch>` runs it, `pf cancel
<batch>` drops it. A batch with nothing landed, or over default branches
that have not moved since their newest green tag or the last red gate that
failed on them, gates nothing and ends `empty`. A held item (one a red batch
blamed) is released only by a running batch: its `collect` step puts a hold
none of its fix tickets can release to the owner (`pf step ask`), and the
agent clears it on the owner's word, citing the reply (`--per-owner`);
nothing outside a batch clears one. A batch is followed like any work item
(`pf watch <batch> --once`).

**Model health:** `pf ops models` reads the daemon's read-only `GET /models` profile — one row per model+effort × step over the events-retention window (dispatches, successes, model deaths, rate, mean wall time from event timestamps, non-model deaths and the most recent death cause) — and states the rolling window above the table; `--json` re-emits the route document unchanged. It is advisory telemetry only: no dispatch is reordered, pinned, skipped or blocked, and the thresholds are compiled constants with no config key. `pf ops status` carries the same summary (current warnings, or an explicit no-warnings line, pointing at `pf ops models`), and `pf doctor`'s `model health` row warns at most without moving `ok` or the exit code.

**Job cap:** `pf ops max N` applies at once and lasts until the daemon
restarts; every start takes the configured cap (an explicit
`--max-concurrent-jobs`, else the `[daemon]` table, else 4). A reload
that **changes** `max_concurrent_jobs` also overrides a temporary
`pf ops max` (last action wins), while an unrelated reload leaves the
temporary value in force. The cap is never persisted; `pf ops max` and
`pf ops status` show the one in force.

**A Hub failure degrades only its Cell:** an unreachable Hub or a refused
credential marks that Cell `degraded` (the reason on `pf ops status`'s
`cells[]` and `pf cell list`), stops its automatic claims and answers its
business commands with an explicit error (`DAEMON-503`, or the Hub's own
`401`) — never an empty result; a side-effecting request whose outcome is
unknown is reported so and never resent. Running work keeps the existing
recovery; the next successful Hub round trip clears the mark; every other
Cell keeps serving.

**Two-level concurrency:** that cap is the **Host ceiling** over the sum of
every Cell's running work-item jobs and claims in flight; a Cell's optional
`cell_max_concurrent_jobs` lowers its own share — a
reload applies a changed value at once, clamped to the Host ceiling, and
unset falls back to it (while a connection rotation
is pending, a spawn is refused `DAEMON-409` and a running task keeps the Cell
from switching) (a Host-wide `pf ops max`
never lifts a Cell above it). When several Cells have startable work they
start round-robin in profile order — no weights, no preemption; a Cell
with nothing queued holds no slot; a lowered ceiling only stops new starts
and kills nothing; a paused job (waiting for a human, no live process)
holds no slot. One-off tasks (`pf task spawn`) take NO job slot here
(polyforge#74): `max_concurrent_jobs` and `cell_max_concurrent_jobs`
bound work-item jobs only, and every Cell's tasks share their own Host
bound — `[daemon] max_concurrent_tasks` (default 2, live on reload) —
which the manager enforces alone (`DAEMON-409`, nothing queued);
summarize-lane calls take none of it either (the lane runs at most one
call per work item at a time, spaced by its interval): the Host aggregate of `pf ops status` (`GET /status`)
reports `queue.max_concurrent` (the Host ceiling), `queue.running`/
`queue.pending` summed over Cells, `background_model_calls: {bound,
running}`, and per Cell (`cells[]`) its `cell_max_concurrent_jobs`
(persisted), `max_concurrent` (effective), `running` and `pending`.

What the daemon does after each failure class — resume the same session,
switch candidate, back off, double the limit, park — is the workflow
contract's supervision table ([workflow
contract](workflow-contract.md#the-daemons-reactions)); the bounds below
detect them.

**Supervision bounds** are Go duration strings (e.g. `700ms`, `5m`); a
flag omitted or passed `0` keeps the default, and every flag's meaning
is in `pf help daemon`. Each bound's verdict names the failure it
detects: X (`--silence-window`, test/dev-only) ends a dispatch as
`harness_silent` when no real work signal arrives after it, or
`model_hung` when no model output arrives while waiting for the model
(UI chatter never counts as work); Y (`--tool-window`, test/dev-only)
ends a turn as `tool_hung` when one tool runs longer than it — a
sub-agent call (Claude's `Agent` tool, pi's `subagent` tool) when it
has been silent that long since its sub-agent's last record, however
long it has run in total (a harness heartbeat for a running call is no
such record); T
(`--step-timeout`) ends a step as `step_timeout_long_task` (the same
session resumed once with T doubled; a second timeout parks) or — only
when the dispatch that hit T itself ran at least half of it and at least
five of its COMPLETED model calls were individually slow: the median over
`[daemon] slow_model_median_latency` (default 60s) or the p90 over
`slow_model_p90_latency` (default 180s); the call in progress counts
nowhere — `step_timeout_slow_model` (model class: the next candidate, T
doubled). A dispatch of many fast calls — single-call median 6s, p90 15s,
polyforge#25's 652-call session — is a long task however long it waited:
the waiting share alone decides nothing; N (`--stall-window`) ends the driver between
steps as `driver_stalled`. While a dispatch of a step
declaring `resources: { admission: heavy }` waits for the heavy-run
admission (`max_concurrent_bars`, `bar_load_threshold`) no
clock runs at all — N included — and no job slot is held. `pf ops status` then shows a
`VERIFY` column (`queued #2: <the holding gate>`). A turn that ends without `pf step
handoff` inside `--model-error-grace` (test/dev-only) is `model_error`
when it ended on a model error — model class with `model_hung`,
`step_timeout_slow_model` and an exit naming a model, allowance or auth
problem: the next candidate at once, and none left parks the work item
(no agent can start) — or `ended_without_result` when it ended normally:
the same session resumes with the reminder to end with a hand-off, and
the third in a row parks. `rate_limited` and `upstream_transient` retry
the same model after a backoff of 1 s, doubling to at most 3 min, never
counted. The classification is configurable on the Host (polyforge#51):
the `[daemon] transient_error_patterns` and `model_error_patterns` lists
hold additional RE2 regexes, judged after the allowance/auth row (which
also covers the bare 402 and `payment required`) and before the other
built-in rows, on an exited dispatch's text and on a live error turn
alike — never on the driver's own error; no added pattern can pull an
allowance or auth failure (402/401, quota, insufficient balance) out of
model class. Both lists are live on reload and editable in Pi Web's
configuration page (Host region). The transient texts the classification
recognizes include the
throttles (429 / rate limit / `overloaded` / the gateways' `server
overload. Please retry later.` and concurrency texts) and the upstream
transients: a response stream that ended or was interrupted before
completion, an upstream/gateway named `temporarily unavailable`, a
gateway's `Service temporarily unavailable` (the service subject directly
before the phrase, polyforge#210), a
gateway's `no available accounts`, and the upstream model reporting an
error after output had started (the output so far is partial). Every
`step_failed` event records which rule judged its text
(`classified_by`: `builtin`, `added` or `fallback` — no rule named it) and
the raw text under `error_text`; `pf ops unclassified` lists the fallback
texts of the last 7 days, deduplicated with counts, aggregated across
every Cell of the Host (the daemon route serves a human session only — a
step or task agent's process tree is refused `DAEMON-403`);
`tool_hung` resumes the same session once with Y doubled (a
second timeout parks); any other exit (`exit_other`, `exit_host_class`,
`harness_silent`) resumes once, and the second in a row parks. A
restarted daemon serves only what is left of a backoff, and `pf ops
status` shows it as `retry in 45s: <cause>` (whole minutes from a
minute on). In production only T
(`--step-timeout`) and N (`--stall-window`) are settable, through the
`cli.toml` `[daemon]` table; the other bounds are test/dev-only. Their
running values are observable as `supervision.{step_timeout,stall_window}`
in `pf ops status --json`, so a live reload of either key is visible
without a restart. A tick that arrives far later than its cadence declares
the supervisor's own pause (a host or daemon freeze): the span it did not
watch is not charged to the step — neither to the running dispatch's
bounds nor to the step's cumulative runtime the next dispatch inherits
under T — and is recorded as one `supervisor_paused` event. The
only per-step override in `workflow.yaml` is `budget.wall_minutes` (an
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
## `pf components` — 工作站容器内组件更新

`pf components check` 查询当前与渠道最新版；`pf components status` 读取卷上的
upgrade-status.json。执行 `pf components upgrade --version V`，可追加
`--force` 或 `--seed-dir D`（完整本地版本目录）。省略版本时取渠道最新版。
`pf components rollback` 回到 previous（previous 声明的 dataLayout 与数据目录
的 layout-version 不一致时拒绝并提示原因），也支持 `--force`；
`pf components cancel` 请求取消等待并重启 daemon 清排空；记录为 running/waiting
但事务锁无持有者（编排已中断）时，把记录改写为失败并重启 daemon 以清除排空；
活事务在排空等待之外（running 阶段）取消会被如实拒绝且不做任何更改。
所有操作是 Host 级，不传 --profile；宿主模式拒绝，使用 tools/install.sh。

排空拒收新作业（DAEMON-409），等待现有作业结束后继续；下载和 staging
在切换前完成校验。退出码 75 表示为更新重启，supervise 即刻重新拉起 daemon；
普通成功退出 0、配置拒绝 78 的含义不变。POST /ops/restart 仅宿主操作者可用，
step/task 进程树不可访问。失败原因与目标版本保存在 upgrade-status.json。
