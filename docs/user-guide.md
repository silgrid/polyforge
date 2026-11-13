# Polyforge user guide

The daily-use guide for a team member joining an already-running aihub. It starts
where you start, at the install one-liner, and carries you to competent daily use:
filing work, submitting it, watching it run, answering its questions, closing it
out. Every mechanism lives one link away; the [where to find what](#where-to-find-what)
section maps each kind of question to the document that owns its answer.

## Who this guide is for

You arrive with three things, handed to you by your admin: a personal API key
(`pf_k1_...`), the aihub host it unlocks, and a machine with a harness on it, pi or
Claude Code. That is the whole starting position, and it is enough: this guide and
its links take you from there to a first filed and submitted work item.

You are a team member, not the admin. Creating users, api-keys and projects,
registering the machine, deploying, and cutting releases are admin and operator
work; this guide points at the surfaces that own them and never teaches them.
Standing up your own aihub is the
[developers' Quickstart](quickstart-developers.md).

## Install

One line, any team machine. It needs `gh` authenticated (the one-liner reads
`gh auth token`); the key is read with echo off, so it never lands in your
shell history:

```
read -rs PF_KEY
curl -s -H "Authorization: token $(gh auth token)" \
  https://raw.githubusercontent.com/silgrid/polyforge/main/tools/install.sh \
  | bash -s -- "$PF_KEY" <server-host>
```

It installs the `pf` binary, writes `~/.config/polyforge/cli.toml` (your key and
the host), keeps a release checkout at `~/.local/share/polyforge/release/current`,
wires the polyforge routing skill into each harness it finds, and ends with
`pf doctor`. A new machine registers itself: when the key's user is a member
of any project (or a global admin), the daemon's first signed heartbeat
registers it. If that heartbeat is refused, the installer's note names the
causes (AUTH-403: no project membership, or the machine id is another
operator's; AUTH-401: signature or clock) and `daemon.log` shows which.

The one step the installer leaves to you: add the `[workspace."<path>"]`
mapping with the projects you work in through the one supported writer,
`pf config set '[workspace."<path>"]' projects=a,b,c` — the installer writes
only the flat connection keys and never fabricates a mapping; the daemon
refuses to start without a real mapping and the installer skips the daemon
start until one exists (a Host file with a connection and no mapping now
loads with execution disabled). One daemon serves several AIHubs, one Cell
per profile: `printf '%s\n' "$KEY" | pf cell add <name> --server <url>
--model-source shared-default` adds one at runtime (no restart; `pf cell
list` shows it), `pf --profile <name> …` addresses it, `pf cell repair
<name>` retries a fixed file and `pf cell remove <name>` stops it (refused
while it holds work; its state is kept — restoring the same name must
point at the same AIHub, another AIHub needs a new name). Every AIHub business command — `pf file`,
`pf queue`, `pf recall`, `pf projects`, … — goes through the running daemon
to the Cell's configured connection, so the daemon must be up for them;
`pf config`, `pf init`, `pf profile` and `pf doctor` work without it. With
the mapping in place, run `pf daemon ensure` (or open a new harness session, which runs the
same ensure), then `pf doctor`. Later edits go through `pf config` too; the
running daemon applies live keys without a restart (`pf config reload` after a
hand edit).

Prerequisites, the macOS notes, and what an upgrade does are the
[install page](install.md); this guide does not restate them. Upgrading a work machine is re-running the same line (the
server-side schema story is [upgrade.md](upgrade.md)).

## Daily use in your harness

The recommended daily setup is pi with
[silgrid/pi-web](https://github.com/silgrid/pi-web) (npm package
`@silgrid/pi-web`), a local web UI for the pi coding agent. Polyforge is being
deeply integrated into pi-web: its polyforge progress dock has shipped, and a
Polyforge settings tab is in progress. pi-web drives pi only; Claude Code and
the plain terminal remain supported paths, and everything below applies to
them too.

The primary path is natural language. The install wired the polyforge routing
skill into your harness, both pi and Claude Code, and the skill maps your words
to the right `pf` verb: file a ticket for X, how is polyforge#140 going, pause
that one. Ask for what you want in plain words; the skill routes, runs the verb,
relays the result, and asks rather than guesses when the intent is ambiguous.

Human decisions have one home: pi-web's Polyforge panel. Every decision a
work item needs from you — a question to answer, a gate verdict, a failed or
blocked run — and every background task's summary lands in the daemon's
decision feed, which the panel shows (the ones that need you are counted in
its status bar) and where you handle them. No harness session is woken and
nothing needs wiring: in Claude Code or the terminal, `pf watch decisions`
lists the same feed and `pf watch <id> --once` shows one work item. A
decision is for the human, not the agent: the agent relays the question and
answers only with the human's own words.

The terminal verbs are the reference form of the same surface. Every chat
intent has an exact verb underneath (`pf queue`, `pf submit`, `pf watch`,
`pf answer`, and the rest), the skill's fragments own each verb's real shapes,
and `pf help <verb>` prints them. When you already know the verb, the terminal
is fine; the chat path is the main road.

## Core concepts

Seven terms carry daily use. Each gets one definition here and a link to the
surface that owns the mechanism; this guide never restates the mechanism. The
full newcomer glossary — every term, defined once — is the
[concepts page](concepts.md); the definitions below are its daily-use short
forms.

- **Work item** -- a unit of tracked work: a goal, a content body, a project, a
  status, and a human slug like `polyforge#140`. Filed and edited with
  [pf file](../skills/polyforge/verbs/file.md).
- **Bundle** -- the workflow a work item runs, chosen at filing
  (`--bundle develop`); the shipped bundles live under `workflows/`, and the
  shape is the [bundle-authoring chapter](bundles.md).
- **Lane** -- informal for one bundle's flow: the [develop
  lane](../workflows/develop/README.md) (the default: build → review → sync →
  verification → publish → merge), the [micro
  lane](../workflows/micro/README.md) (pure text: fix → review → sync →
  evidence → publish → merge, no verification step, open only to the paths a repository declares with
  `micro_path=` in its `.polyforge/verify-scope.conf`). The shipped bundles are
  listed in the design docs' [system
  reference](design/polyforge-vnext/reference.md#71-bundles-and-materialization-);
  a lane's own narrative is its `workflows/<bundle>/README.md` (its steps:
  `workflows/<bundle>/workflow.yaml`).
- **Gate** -- a step that can FAIL the run and rewind it to an earlier step;
  which steps are gates is the lane's own: the develop lane gates review,
  verification, publish and merge, the micro lane review, evidence, publish
  and merge (neither lane's sync is a gate since workflow contract 2: its
  agent resolves a real conflict in place; review before verification: the judgment runs first, then the machine bar
  verifies the tree review passed merged with the latest origin default
  branch). What each gate's FAIL means is the
  design docs'
  [execution section](design/polyforge-vnext/reference.md#7-execution-).
- **Rewind** -- a gate FAIL sending the run back to an earlier step to redo;
  bounded per bundle by one total budget (`max_rewinds`), and past it the
  gate asks you before it FAILs again. The rules are the [workflow
  contract](reference/workflow-contract.md#the-daemons-reactions)'s.
- **Capsule** -- the work item's private workspace directory (`artifacts/`,
  `repos/`, `runtime-state/`) that the daemon creates, drives, and commits to
  aihub. The contract is the design docs' [capsule
  section](design/polyforge-vnext/reference.md#8-capsule-and-git-chain-).
- **Decision feed** -- the daemon's machine-wide list of human decisions
  (questions, gates, failed / blocked / completed runs, background task
  summaries) across every Cell, newest first; the entries that need you are
  open until answered, resolved by the run itself, or dismissed. Pi-web's
  Polyforge panel reads it; `pf watch decisions` prints it. The rules for
  the agent are [SKILL.md](../skills/polyforge/SKILL.md)'s human-decisions
  section.

## The flow of a work item

The order the system actually runs:

1. **File.** One command makes an execution ticket:
   `pf file create --project <project> --goal "<one line>" --content @background.md --bundle <bundle>`;
   without `--bundle` it is a plain tracking ticket that never runs. Check the
   queue first (`pf queue --project <project>`) and file `--after <existing>`
   when the new one clearly waits on a running ticket.
2. **Submit.** `pf submit <id>` claims it on this machine's daemon and the Go
   execution driver takes over (the mechanism is the design docs' [execution
   section](design/polyforge-vnext/reference.md#7-execution-)'s subject).
3. **Grill questions.** A lane's clarification checkpoint asks its questions
   after submit, when the run reaches it, as one table
   (`| 问题 | 推荐选项 | 理由 | 重点关注程度 |`: numbered question, recommended
   option, rationale, attention grade `★~★★★★★` — ★ routine confirmation, ★★★★★
   a directional ruling), followed by 预期效果 and 注意事项 lines. They arrive as
   a needs_input entry in the decision feed (pi-web's Polyforge panel); in a
   harness session, `pf watch <id> --once` shows the question text verbatim.
   The step asked with `pf step ask`; answer with no question id — `pf
   answer <id> --text "<your words>"`, row by row in your words — and the
   same command interjects into a running step, or wakes a parked one, at
   any time; the words reach the step's own agent verbatim, and it reads
   them. Answers are the human's; a step agent never answers them. To
   abandon the work item, say so (the agent hands off the cancel, citing
   your words) or run `pf cancel <id>`.
4. **Watch.** `pf watch <id> --once` for a snapshot (status, current step,
   latest events); the decision feed (pi-web's Polyforge panel, or
   `pf watch decisions`) for everything that needs you, on every work item
   of the machine. There is nothing to subscribe: the daemon records each
   decision once, resolves it when the run answers or moves on, and
   `pf watch dismiss <id>` marks an entry you have seen.
5. **The lane's work steps.** The bundle's own steps do the work: fix or code
   implements, review judges the change (before the machine bar — it never
   reads a verification report), the default branch is synced in (on both
   lanes the `sync` step's agent runs `sync.py` and resolves a real conflict
   in place), and the lane's verifying step proves the synced tree:
   develop's verification runs the project's light tier (`verify.py`),
   micro's evidence step re-runs the repository's fast evidence
   (`run.py --plan-from`, on the host). You watch and answer; you do not drive the steps.
6. **Deliver.** The workflow's own last steps finish the run: `publish`
   pushes the feature branch at the verified head and opens or reuses the
   PR, and the `merge` step merges it under the repo's merge lock and lands
   the work item (its agent runs `merge.py`); the integration lane later
   runs the full gate over what landed. The PR merges at the head the
   verifying step ran over; a PR that moved, closed or that the forge
   refused (a default branch that moved with a real conflict) sends the run
   back to `sync`. When the work item declared an `owner-review:` artifact
   (a screenshot matrix, a proof set), publish first shows it to you as a
   question (a `pf step ask`) and the merge waits until you answer 「过目通过」; a work item
   that declares none asks you nothing extra. Completion rides the merge; it
   is never a verb you run.

`pf wrap` is not the workflow's completion. It is the human close verb, two
moves only: closing a bundle-less ticket (a design, decision or tracking ticket
that never ran) with `pf wrap <id> --note "<why>"`, and recording the owner's
failed verdict on a live run with `pf wrap <id> --fail`. The shapes and failure
branches are [pf wrap](../skills/polyforge/verbs/wrap.md)'s.

## A worked example on the develop lane

This example is composed, illustrative only: never a transcript of a real run
(step agents must not drive the daemon). Every command shape is real.

A small fix in project `demo`: the login endpoint times out under load. Check
the queue, then file it on the develop bundle (the default lane), its content
carrying an `## Acceptance` section with one criterion per line:

```
pf queue --project demo
pf file create --project demo --bundle develop --goal "Fix the login timeout under load" --content @login-timeout.md
```

Submit and watch:

```
pf submit demo#7
pf watch demo#7 --once
```

The lane runs build, then review, sync, verification, publish and merge:

- **build** plans against the acceptance criteria. It does not grill the
  ticket (that happened in the session that filed it); it stops for you only
  on a surprise, right after the plan — a dead end or contradiction the code
  reveals, a plan that diverges from the ticket, a ticket carrying
  `confirm_plan: true`, or a ticket without an acceptance list (its proposed
  list, asked once with the plan). The question (build's `pf step ask`) arrives
  in the decision feed with a recommended answer; read it and answer in
  your own words:
  ```
  pf watch demo#7 --once
  pf answer demo#7 --text "Reproduce with 50 concurrent logins, the fix is in the session store"
  ```
  Then it writes each unit's test red before its implementation turns it
  green, in the task repo, its own commits, inside the capsule, records
  each unit's run in `artifacts/build/units.json`, and reviews itself; its
  summary states what you decided and how it read your words.
- **review** judges the change in a fresh context against the five blocking
  classes, on the facts the workflow's `facts.py` computes — each unit's run replayed red
  at its test commit among them; a FAIL rewinds the run to build.
- **sync** merges the latest default branch into the branch (`sync.py`); its
  agent resolves a real conflict in place, or asks you when it is not its to
  resolve.
- **verification** runs the light tier (`verify.py`: the fast gates, the
  affected unit tests and the e2e suites the change added, changed or
  carries); a red it can place on the change rewinds the run to build, one it
  cannot is asked — you may waive it. The full bar runs once per batch, in
  the integration lane, over what landed.
- **publish** pushes the branch and opens the PR; **merge** merges it into
  the default branch (`merge.py`). sync, verification, publish and merge are
  mechanical agent steps on the cheap tier, each running one pre-written
  command its skill names.

A later snapshot shows the run completed and the PR merged, and the queue lists
the work item landed -- merge closed it. No human wrap verb ran.

**What micro trims.** A pure text change -- docs or locale message text inside
the paths a repository declares with `micro_path=` -- may go on the micro lane
instead: fix, then review, sync, the repository's fast evidence (fix's plan
re-run by `run.py` on the host), publish and
`merge.py`, with no verification step; a change outside the declared paths is
not micro's — fix asks you to re-file it on develop. The [bundle's own
README.md](../workflows/micro/README.md) owns the detail.

## Memory

Memory is a core pillar of daily use: the project's lessons live in aihub, not
in your head or your workspace.

- **Before starting work**, recall: `pf recall --project <project> "<topic>"`.
  Hits are non-binding history: designs, decisions, pitfalls; use what helps
  and ignore the rest.
- **When you learn something durable**, remember:
  `pf remember --project <project> --type experience.wrap --title "<lesson>" "<content>"`.
- **To browse the project in a browser**, open the server's web console
  at the aihub base URL (your `server` setting in `cli.toml`, for example
  `http://localhost:8420`) with `/web/` appended — for example
  `http://localhost:8420/web/`. The server speaks plain HTTP itself;
  an `https://` console URL works only when the deployment terminates TLS
  in front of it, and over plain HTTP the api key crosses the network
  in clear. The browser asks for a user name and
  a password: enter any user name and your api key as the password. The
  console's first page lists the projects your key may read — pick one
  and use its three modules, all read-only and scoped to that project:
  - **Work items** — the project's items newest first, filterable by
    status and searchable over goal and content, twenty to a page;
    an item's page shows its content, metadata, both dependency
    directions and its event timeline.
  - **Memories** — the project's memories, newest first, page by page
    with the Next link, or one keyword page of the top 50 matches (the
    page tells you when it is only showing the top matches); a
    memory's page shows its header card and its full body rendered
    read-only. The private and admin visibility tiers stay hidden
    from anyone but their readers, exactly as on the CLI.
  - **Machines** (admins only) — a health snapshot of the registered
    machines: role, runtime, launcher, heartbeat age and staleness.
  The top bar also offers a project-scoped global search that groups
  matching work items and memories. The console follows your operating
  system's light or dark setting (there is no theme switch), and on a
  narrow screen the navigation shrinks to an icon rail — each icon keeps
  its module name as its tooltip and accessible name. On a borrowed machine,
  open the console in a private browser window so the credential leaves
  no trace when the window closes; no step or task agent can reach the
  console — it serves a human at a browser only, and the daemon proxies
  no web route.

The memory model (types, visibility, how search ranks) is the design docs'
[memory section](design/polyforge-vnext/06-memory-search.md); the verbs' shapes
are [recall](../skills/polyforge/verbs/recall.md) and
[remember](../skills/polyforge/verbs/remember.md).

## When things go wrong

The human moves, when a run is not simply going:

- **Pause**: `pf pause <id>`, the daemon seals the capsule and stops the agent
  run. The work item stays paused across restarts; nothing auto-resumes it.
- **Resume**: `pf resume <id>`, continue a paused (or blocked) one. The holder
  daemon re-attaches and the run picks up where it paused.
- **Cancel**: `pf cancel <id>`, abandon a queued, paused or blocked one for
  good.
- **Blocked**: a work item goes blocked in more than one way: most often its
  step hands off `blocked` (the owner holds the next move), a step times out
  or exits twice without a hand-off, a pause can land during the claim, or
  its capsule or workspace mapping can go missing across a restart (the
  reason note names the cause). Tell it what to do -- `pf answer <id> --text
  "<words>"` or the job card wakes the step concerned with your words -- or
  use the two owner commands: `pf resume <id>` (after fixing the cause,
  where there is one to fix; the only way on when no agent can start) or
  `pf cancel <id>`. A failed verdict does not apply to a blocked one.
- **A dead machine**: a work item whose holder daemon is dead or stuck is
  recovered by an admin or a project maintainer: end the stuck session, then
  submit it again on a healthy machine ([pf ops](../skills/polyforge/verbs/ops.md)
  owns the move).
- **A suspicious machine**: `pf doctor` is the diagnostic first stop: config,
  mapping, connectivity, daemon supervision, the harness integration, retired
  chat-subscription state and diagram guidance. It reports; it writes only on
  an explicit flag (`--prune-scratch`, `--prune-capsules`, `--prune-chat-state`,
  `--fix-harness-prompts`, `--fix-install-layout`).

[pause](../skills/polyforge/verbs/pause.md),
[resume](../skills/polyforge/verbs/resume.md),
[cancel](../skills/polyforge/verbs/cancel.md) and
[doctor](../skills/polyforge/verbs/doctor.md) own the shapes and their failure
branches.

### The daemon refuses a pre-Cell data dir

After the upgrade that introduces Cells, `pf daemon ensure` reports that the
data dir carries the pre-Cell layout and names `pf migrate cell-layout`. Run
`pf ops shutdown`, then `pf migrate cell-layout` (a plan; nothing written),
then `pf migrate cell-layout --apply` (it copies the state into
`cells/default`, keeps the machine id and key, and writes
`layout-version` last — re-running it is safe; once the layout is complete
a re-run says the data dir is already on the cell layout and changes
nothing, so state written after the migration is never replaced), then
`pf daemon ensure`. Until a Host has served the new layout (`pf daemon
ensure` closes the window at its first start), `pf migrate cell-layout
--rollback` returns to the pre-migration copy — only while the new layout
is byte-identical to that copy; anything written into it since is named and
the rollback is refused, changing nothing; after that, repair forward
(`pf doctor`), or as a last resort restore a backup
you made yourself with `pf migrate cell-layout --restore-backup <dir>`,
which first lists what would be replaced and acts only with `--confirm`.

### A Cell whose execution is disabled

`pf cell list` (or `pf ops status`) shows `execution_disabled` with the
reason when a named Cell has no usable model-credential source: set
`[model] source` in its `cli-<profile>.toml` to `shared-default` (reuse the
default Cell's credentials — only the credential files and the claude
`env` block are copied) or to an absolute directory carrying `auth.json`,
then restart the Host (a Cell disabled for having no workspace opens on
`pf config reload` once a `[workspace]` mapping is added). Business commands
on that Cell keep working meanwhile. While it is disabled the Cell starts
nothing: a submit, `pf task spawn`, `pf resume` and every `pf answer` are
refused naming the reason (a refused `pf resume`
leaves the work item paused, its `clean_pause` still set, and a paused work
item whose `clean_pause` was cleared elsewhere stays paused across a restart
of the disabled Cell — run `pf resume` again once it admits it); reads, `pf cancel`,
`pf pause` and a job already running are unaffected.

## Where to find what

| The question | Where the answer lives |
| --- | --- |
| Install detail (prerequisites, macOS, what an upgrade does) | the [install page](install.md) |
| One verb's exact shapes, success and failure branches | `pf help <verb>`, then the [skill fragment](../skills/polyforge/verbs/help.md) that owns the verb |
| Why it works that way (designs and decisions) | the [design docs](design/polyforge-vnext/reference.md) |
| Upgrading (the server-side schema story) | [upgrade.md](upgrade.md), plus re-running the installer line |
| Admin and operator work (users, api-keys, projects, machines, deploy) | the [quickstarts](quickstart-developers.md) and the management verbs (`pf users`, `pf api-keys`, `pf projects`, `pf machines`) — `pf projects delete` is the irreversible one: run `pf backup` first (and copy the server's `git/` directory; the backup does not include it) |
| Releases | the [release reference](reference/release.md): maintainer-only, cut on explicit owner request |
| Extending polyforge (config keys, custom bundles) | `pf help extensions` and [extending](extending.md), the extension-point index |
