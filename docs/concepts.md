# Polyforge concepts — the newcomer manual

This page is the newcomer manual and the glossary: every term a newcomer
meets, defined exactly once, here. The other pages of the doc set link
each term's first encounter back to its definition on this page, so no
term is ever used undefined. The [where to find what](#where-to-find-what)
section at the bottom maps each kind of question to the page that owns
its answer; the daily-use walkthrough is the
[user guide](user-guide.md).

Polyforge is a work-item orchestration platform: a server (**aihub**)
that stores the work, the memories and the git revision chain, and a
work machine whose **daemon** executes the work. One `pf` binary plays
both roles — `pf aihub` on the service host, the `pf` CLI plus
`pf daemon` on a work machine. The two sides split cleanly: everything a human files, reads or decides is
**data plane**; everything a machine executes is **execution plane**.

## The sixteen terms

### Capsule

The work item's private workspace directory: `artifacts/` (the briefs,
specs, plans and handoffs the steps write), `repos/` (the task repos'
worktrees), and `runtime-state/` (the execution state the driver owns).
The daemon creates the capsule when it claims the work item, drives every
step inside it, and commits it to aihub's git revision chain. A step agent
never works outside its capsule. The contract is the design docs'
capsule section ([architecture](architecture.md) lists the reading
order).

### Work item

The unit of tracked work: a goal, a content body, a project, a status,
and a human-readable slug such as `polyforge#140`. Filed with
`pf file create`, submitted with `pf submit`, closed when its workflow
run ends (a delivery lane's `merge` step lands it) or by a human's
`pf wrap`. A work item with a bundle is
an execution ticket (it can be claimed and run); one without is a plain
tracking ticket that never runs.

### Workflow (lane)

The ordered set of steps a claimed work item runs, declared by its
bundle's `workflow.yaml`. "Lane" is the informal word for one
bundle's flow — people say "the develop lane" the way they say "the
develop workflow". Each lane's own narrative is its
`workflows/<bundle>/README.md`; the shipped lanes are **develop** — the
default development lane since (build → review → sync →
verification → publish → merge: each work item merges itself into the
default branch at its end, as the daemon) — micro, integrate and release.
The four lanes develop superseded were removed by
(`workflows/_archive/README.md`): a work item still bound to one is refused
at claim, and a terminal one keeps displaying. micro is the pure-text
lane — fix → review → sync → evidence → publish → merge, no grill, plan or
verification step, the ticket body as its spec — open only to the paths a repository declares
text-only (`micro_path=` in its own `.polyforge/verify-scope.conf`).

### Memory

A durable note stored in aihub, scoped to a project: designs, decisions,
reviews, pitfalls, experience. Written with `pf remember`, searched with
`pf recall`. Memories are non-binding history — a decision or authoring
step (its skill runs `pf step recall`) recalls them for context,
never as instructions; the gates and the release steps do not recall. The
last step of a workflow writes the work item's one wrap-summary memory under
the project's memory policy.

### Step

One unit of a workflow: a step definition file (`steps/<id>.md`) and
usually a skill, which a step agent (a real harness session the driver
spawns) follows. A step may be a gate. Steps hand off through one
document, `artifacts/handoff.md` (below), and through the files they write
(specs, plans, code, result files); the driver keeps its own progress in
`runtime-state/execution.json`, which no step reads.

### Result delivery

The one way a step agent's outcome reaches the daemon: `pf step handoff`
(workflow contract 4, [the step
interface](reference/workflow-contract.md#the-step-interface)). The step
ends every dispatch with exactly one call naming a status — `done`, a
gate's `fail`, `blocked`, or, carrying out an owner's word, `cancel`,
`waive` or `rewind` — and a body in the shared seven-section template; the
daemon checks the call at once, stamps the section's header from its own
records (times, model, commits, the owner's messages verbatim) and appends
it to `artifacts/handoff.md`, the work item's one append-only hand-off
document that every later step reads first. Nothing else a step prints is
read as a result, and the daemon never reads the body for a verdict: it
reacts to the status alone (advance, rewind, park, end).

A step agent that needs the owner asks with `pf step ask` and ends its
turn; the owner's words — `pf answer <wi> --text "<words>"` or the job
card, at any time — are recorded in the capsule and relayed verbatim into
that agent's conversation, never read by the daemon. An owner message to a
parked work item wakes the step concerned. What the owner wants beyond the
step's own work (cancel the work item, waive a FAIL, send the work back)
the agent hands off with that status and `--per-owner`, citing the
message.

A step runs its commands itself — the workflow scripts its skill names
write result files, whose paths ride the hand-off as
evidence and which the next step checks (downstream verifies upstream) —
and reaches aihub only through the daemon (`pf step hub`, a write made only
when the step hands off `done`). A step running heavy commands declares
`resources: { admission: heavy }` and its own `budget.tool_minutes`.

### Project

The tracked-work unit a work item belongs to: the slug namespace
(`polyforge#140`), the memory scope, and the repo registry aihub serves
per project. Created by an admin (`pf projects create`), routed to a
workspace by the repo mapping.

### Workspace

A directory on a work machine that holds the capsules of the projects
mapped to it: `<workspace>/capsules/<project>-<seq>/`. Its layout is the
convention `pf init` writes: `.polyforge.yaml`, the workspace notes file,
`.repo/` clones for humans, `capsules/` (daemon-managed) and `scratch/`
(disposable output). The notes file is `AGENTS.md` when it exists, else
`CLAUDE.md` — both names are supported permanently, and `pf init` creates
`AGENTS.md` in a workspace with neither. Anything meant to be shared goes
to aihub, never to a workspace file.

### Machine (work machine)

One computer running a `pf` daemon: the execution plane's home. A machine
has an identity (`m-<short hostname>`, recorded in
`<data-dir>/daemon-id` on first start), registers with aihub on its
first heartbeat, and holds the work items it claims until they wrap or it
is fenced. "Work machine" names the whole setup — machine, daemon,
supervision loop, harnesses — as opposed to the aihub server host.

### DaemonHost and ProfileCell (Host, Cell)

One `pf daemon` process is the **Host** (DaemonHost): it owns the one
Unix socket, the supervision loop, the shared binaries, workflows and
release, the Host-wide concurrency ceiling and the Host-wide decision feed.
The Host serves one or more **Cells** (ProfileCells): each Cell is one
AIHub identity domain — one server, one user, its own credential, machine
key and id, heartbeat, workspaces, queue, tasks and execution state —
named by a **profile** (`default`, defined by `cli.toml`, plus one
`cli-<profile>.toml` per named Cell). A Cell is a unit of state isolation,
never a security sandbox: all Cells share one process. The **effective
connection** of a Cell is the one its runtime is actually using (a changed
file is not effective until the Host applies it). A connection change
never changes a Cell's identity: a new server or user is a new profile; a
same-identity key rotation is **pending** and the Cell **drains** — no new
submits or automatic claims, nothing killed — until it is **safe idle** (no
job record of any state, nothing queued), then switches by itself. Concurrency is two-level: the Host ceiling bounds
the sum of every Cell's running jobs and one-off tasks, a Cell's optional
ceiling bounds its own (a task at a taken ceiling is refused, nothing
queued); Cells with queued work start round-robin (no weights, no
preemption), a job waiting for a human with no live process releases its
slot, and every Cell's one-off tasks share one Host background-model-call
bound of 2 (summarize calls take none of it: each work item runs at most one
at a time, spaced by the summarize interval).
A named Cell's execution plane opens only with a usable `[model] source`
declaration (credential material only is shared); otherwise it stays
disabled with a visible reason while its data plane serves. `pf ops status` /
`pf ops max` default to the Host aggregate; an explicit `--profile`
targets one Cell; `pf ops shutdown` always stops the whole Host. Every Cell-scoped command resolves its Cell
deterministically in the CLI — `--profile`, then `PF_PROFILE`, then the
unique cwd match in the Host's effective workspace index, then `default`
— and the Host routes it to exactly that Cell (a request naming no Cell is
the default Cell's; an unknown one fails, never another Cell). A step or
task agent is routed by the Host's own record of which Cell runs it. The
Cell registry is `GET /profiles`; the key partition is
[config: Host and Cells](reference/config.md#host-and-cells--clitoml-and-cli-profiletoml).

### Repo mapping

The plan that decides where a claimed work item's capsule lives: the
`[workspace."<path>"]` sections of `cli.toml`, each listing the projects
whose capsules live under that path. It is the daemon's only placement
source — every mapped directory must pre-exist, and the daemon refuses to
start without a real mapping. The full shape is
[extending: workspace mapping](extending.md#workspace-mapping).

### Data plane vs execution plane (aihub vs daemon)

The split the whole system hangs on. **Data plane**: everything stored
and served by aihub — work items, projects, users, memories, the git
revision chain — reached by the `pf` CLI through the local daemon, which
forwards each business call over the resolved Cell's effective AIHub
connection (CLI → Host → Cell → Hub; the CLI never dials a Hub, and with the
daemon down business fails explicitly). **Execution
plane**: everything the local daemon owns — the claim, the capsule, the
driver spawning step processes, the watch surface — reached over its
unix-domain socket. The routing skill's verb table follows the split:
`file`, `queue`, `cancel`, `recall`, `remember`, `memory` are data-plane
verbs; `submit`, `watch`, `answer`, `pause`, `resume`, `wrap`, `ops` are
execution-plane. One answer is reserved at every open question: `cancel`
(the whole answer, any case). The driver states it in every question; the
daemon first has the hub's cancel gate (reporter, maintainer or admin —
the `pf cancel` rule for a running work item) authorize it, then the
workflow parks blocked with a durable cancel intent, the session ends
blocked, and the hub's existing blocked→canceled door ends the work item
`canceled` — no new status, no running→canceled transition, no memory
wrap. The holder basis widens only the blocked doors:
once a work item is blocked, the current operator of the machine that
last held it may also `pf resume` or `pf cancel` it as a project writer
or maintainer; the probe of a running work item stays outside that
widened set.

### Bundle

The installable unit a workflow ships as: a directory under the daemon's
workflows dir with a `workflow.yaml` (the steps, their order, budgets,
model candidates), a `README.md` (its narrative, which the daemon never
reads) and `steps/<id>.md` per step id. A work item
runs the bundle it was filed with (`pf file create --bundle <name>`); a
step with no file of its own falls back to the shared
`workflows/_shared/steps/<id>.md`. A workspace may keep bundles of its own
— user lanes, `<workspace>/.polyforge/workflows/<name>/` — which the work
items of its projects resolve beside the installed ones (never in place of
one: [workflow contract](reference/workflow-contract.md#user-lanes)).
Authoring a custom bundle is
[bundles: the bundle-authoring chapter](bundles.md).

### Gate

A step that can FAIL the whole run: the delivery lanes' gates rewind it
to an earlier step, the release lane's gates end it (published
releases are immutable — no `on_fail`, no rewind into a re-release).
Which steps are gates is the lane's own choice — micro gates review,
evidence, publish and merge (its `sync` is no gate since workflow contract
2), each rewinding to its build step `fix` — merge's to `sync`; develop gates review, verification, publish and merge (its `sync` is no gate since workflow contract 2: a real conflict is the sync agent's to resolve in place), each rewinding to its one
build step `build` — merge's to `sync`; the release lane's release, verify and deploy steps are all gates. A
gate's FAIL verdict is the input of the rewind (or the ending) it
triggers.

Since the shared review gate — every lane's review except
micro's own — blocks only on five classes: `bug`, `ac_gap`,
`project_rule`, `test_weakened` and `guarantee` (defined once in
`workflows/_shared/steps/review.md`): an unrequested concept, a guess
stated as fact, a recalled pitfall repeated without a defect and I/O under
a lock the repo's `AGENTS.md` does not forbid (and that cannot stall) do
not block — they are recorded non-blocking and reach the owner in the PR
body. A document a user-visible change leaves out of sync does block (the
checklist's doc-impact check: `project_rule` for a coupling `AGENTS.md`
names, `guarantee` for any other).

### Rewind

A gate FAIL sending the run back to an earlier step to redo the work:
review FAIL rewinds to the build step (fix/code) of its lane, running
before verification so a quality failure never costs a full machine
bar; verification FAIL rewinds to the build step too — `build` in
develop (every develop gate rewinds there, within 3 rewinds in total);
micro's gates rewind to its `fix` (merge's to `sync`), within 3 in total.
The target is the gate's own `on_fail` declaration — never a later step.
Rewinds are bounded per lane by `max_rewinds`: one total budget across
every gate→target edge ([workflow
contract](reference/workflow-contract.md#the-daemons-reactions)). A FAIL
the owner asked for — the gate hands it off with `--per-owner` citing the
owner's message, as publish does on the owner-review 「不通过」 — counts
toward nothing. With the budget spent, a gate cannot hand off a plain FAIL
(the call is refused): it asks the owner first (`pf step ask`) and hands
off what the owner decided, citing the reply — one more round (`fail
--per-owner`, uncharged), `waive` (a gate the lane declares `waivable:
true`: the gate counts as passed and the run goes on; the daemon writes the
waiver, the owner's words verbatim, into the work item's `attrs.waivers`
at the wrap; a waived verification leaves no verified head, so publish
pushes the current HEAD and the integration lane's full gate is the
change's first verification) or `cancel`. No work item parks on a spent
budget.

A rewind appends a rewind event to `handoff.md` naming the steps to redo
and keeps everything: commits, result files and the earlier sections,
which stay readable as reference. The target resumes its own previous
session when it can (same harness and candidate, session still readable),
else starts a new one and catches up from `handoff.md`; the steps between
the target and the gate, and every review re-run, start fresh. A re-run
step answers the previous round's FAIL point by point in its body
(回应上一轮); a re-run review checks each earlier finding, and judges from
the commits stamped on each section whether a re-run build really changed
the code.

Every "cancel and re-file" in a lane is reached through the owner's word:
the step's `pf step ask` and the owner's reply, on which the agent hands
off `cancel --per-owner` (micro's fix out of bounds); the work item ends
canceled, then the owner files anew.

`plan` is never a rewind target. A lane with a `plan` step (no shipped lane
has one since; develop's build writes its own
`artifacts/plan.md`) declares every file it writes together with its coupled
obligations (the documents, tables and projection rules that must change with
it, or an explicit none with a reason — a plan missing one is rejected), and
review checks the scope both ways: changed-but-undeclared files, and
declared obligations or writes the diff left unchanged. A review-driven
fix that needs a file outside the declared set cites the review finding
for it instead of re-planning. The plan also maps every deliverable it
demands from a later step (a report, a proof, an owner presentation) to
the step that carries it; one no step carries is excluded with a stated
reason, or raised to the owner (`pf step ask`), never promised. An owner
presentation (a screenshot matrix, a proof set, a demo) is carried by
publish's owner-review gate: the plan, or a build step's hand-off,
declares it with one `owner-review: <path(s)> — <description>` line, and
publish shows it to the owner in a question (`pf step ask`) once the PRs
are open, and completes — so the `merge` step can merge — only after the
answer 「过目通过」 (「不通过: …」 rewinds to the build step). A work item
that declares none is asked nothing.

A review pass covers the tree it judged: the review's hand-off section
carries each task repo's end commit, and the automatic merge needs a
review that covers the delivered diff — any code change after the pass (a
plain commit, a hand-resolved merge) invalidates it and routes the
delivery to a human, fail-closed: a delivery whose facts are missing or
inconsistent never merges unattended.

### Verification tier

Verification runs the tier its step declares: develop's `verification`
runs the **light tier** through its skill's `verify.py --tier light`
(workflow contract 2; the result file's `heads` name the tree it ran
over); the **full** bar is the integration lane's gate (`gate.py`). There is
no per-work-item tier: the scoped tier and its
content marker are gone (workflow contract 1).

`verify.py` verifies the tree as it stands: `sync` merged the latest origin
default branch into the branch before it, and a conflict is the sync
agent's, resolved before anything is verified — so the result file's
`heads` name the synced head; publish verifies that file first and pushes
exactly that head, re-running no test; the `merge` step merges it at that
head with `merge.py`, a default branch that moved since never merged in
and never verified again (below, "Landed, awaiting the full gate"). (The
daemon's `verify` action, which fetched and merged the latest default
branch itself before its bar ran, left with the daemon actions, workflow
contract v2 change 16.)

### The light verification

`verify.py --tier light` (; develop's `verification` step,
skill `verify`) runs the **light tier** — `workflows/_shared/scripts/verify-scope --light` in
each touched repo and in every nested engine root the diff reaches, i.e.
the fast gates, the affected unit tests, the e2e suites the change added
or changed or whose `carries:` meet it, and the build's unit runs
(`--unit-runs artifacts/build/units.json`), with the conf's forced-full
paths not applying (the full bar is the integration lane's `gate.py`). A failing unit is re-run once — one suite of an
aggregate e2e command through the conf's `rerun=` template — and a unit
still red runs once on the baseline: the default branch's commit the
tested tree carries, `merge-base HEAD origin/<default>` — a red the
default branch brought with it is not the item's, and the default branch
may be red, since items merge before the integration lane's full gate
(docs/contributing.md, "Verification tiers"). Flaky and pre-existing reds
then pass on record, so such a round spends no rewind; what stays red the
`verification` agent routes as its skill says, and it never passes a red
it cannot place.

A repository whose tree carries `.polyforge/Dockerfile.verify` runs
that bar in a container of its own; one without it runs on
the host as above. The image is built from that file alone — an empty build
context, so the file must be self-contained: public base images, every tool
at an exact pinned version — and must carry what the scope engine itself
runs as well as the bar: `sh`, `bash`, `git`, `python3`, `awk` and the
POSIX tools, plus every toolchain the conf's commands call (`go` for a Go
repository's `go list`, `node`/`npm`, …). It is tagged
`pf-verify-<repo>:<hash of the file>`, built only when the tag is missing
(once, however many runs ask at the same moment; its log is
`runtime-state/verify/<n>/<repo>/image-build.log`).
Each run starts one container per such repo (`--init`, labelled
`pf.verify.daemon` — the script's owner, `pf-step` — `pf.verify.boot` — a
fresh nonce of the run — `pf.verify.wi`, `pf.verify.run`, `pf.verify.repo`
and `pf.verify.expires`, the run's deadline) that
mounts, each at its own host path, only the repo's clone and the run's
base-worktree directory (read-write) and the release's
`tools/` directory (read-only), plus the repo's named volume of the image's
generation, `pf-verify-cache-<repo>-<the image's hash>`, at `/cache` (point
the toolchain caches there, e.g. `GOCACHE`, `GOPATH`, `npm_config_cache`).
The volume lives as long as the image: the same Dockerfile reuses it, any
byte changed in the Dockerfile (a toolchain or browser pin) gives a new,
empty one. Within a generation its contents check themselves — Go's modules
against `go.sum`, its build cache keyed by the toolchain, npm's cache by
integrity — so a lockfile change only misses and downloads; the browsers
live in the image, and `node_modules` is never cached (each `npm ci`
installs it into the clone). Each container start removes the repo's older
generations no container references (a volume named exactly
`pf-verify-cache-<repo>-<32 hex>`, not the current one, that docker lists as
unused; never another repo's volume, and docker refuses to remove one in
use, so a verification still running on an older image keeps its volume
until a later start). A cache suspected corrupt is removed with
`docker volume rm` of the current volume: the next run starts cold. It has its own pid and network
namespaces, no docker socket, no `pf` binary, no operator configuration, and
an environment that is the image's own plus, when the daemon's environment
sets them, `LANG`, `LC_ALL`, `TZ`, the proxies (`HTTP_PROXY`, `HTTPS_PROXY`,
`NO_PROXY`, either spelling), `SSL_CERT_FILE` / `SSL_CERT_DIR` (paths as the
container sees them — nothing of the host is mounted for them), `GOPROXY`,
`GOPRIVATE`, `GONOSUMDB`, `GOFLAGS` and `npm_config_registry`, and the run's
own `TMPDIR` / `E2E_JOBS` / `GOMAXPROCS` — no `PF_*`, `TMUX*` or
credential. Its `PATH` is the image's: the step's `PATH` (`[daemon]
command_path`) applies only to a bar on the host. The scope engine and every bar, flaky re-run,
confirming re-run and base run is one `docker exec` in the same working
directory; git (the base worktree, the scans) stays on the host.
After each command every process it left in the container is killed — a
timed-out command's whole tree included, which killing the `docker exec`
client alone would not do — and the container is removed at the run's end,
at the run's deadline and on a pause or shutdown. Every docker call but the
image build (bounded by the run's deadline) and a command's own `docker
exec` (bounded by its timeout) gets at most 2 minutes, so a docker engine
that stops answering cannot hold a run; a removal that fails is logged
once on the script's stderr (and noted as `left` in the repo's `container`
record when the run completes); the container ends by itself five minutes
after the run's deadline, and the next run's start removes every
container of its owner whose deadline has passed. The limits are the
script's (CPUs min(8, the host's cores), memory `16g`, pids 4096, the
ceiling measured on this repository's own full bar: its browser e2e suites
peak near 10 cores, 5.4 GiB and 1,100 processes; and on the light tier's
browser e2e, where each suite runs its own `next dev`: at 4 browsers 3
suites peak near 9.4 GiB and 6 suites near 12.5 GiB, and under `8g` the
kernel's OOM kill took Chromium's renderer,).
The logs, the junit reports and `result.json` have the
host run's shape; the repo's `repos[]` entry adds `container` (image, name,
whether this run built the image, the build log, the limits). Docker
unavailable, a failed build, a refused start, a container lost mid-run or a
`requires_cmd` probe docker could not run (never read as a missing command)
makes the run incomplete (`verify.py` exits 2: not judged) — never a red
bar and never a pass. The daemon itself may run inside a container when the
capsule roots are mounted at identical host paths (the bind mounts name
host paths).

Every dispatch of a step declaring `resources: { admission: heavy }` (the
develop lane's verification) first asks the daemon's heavy-run admission:
it starts only while a global slot is free (`max_concurrent_bars`, default
2) and the load is below `bar_load_threshold`. A denied dispatch is
**queued**: it runs no process, spends no budget and holds no job slot; the
queue is ordered by priority, then by when it queued, and `pf watch` and
`pf ops status` show the run's position in it. (The per-repo bar lock,
`locks: [bar]`, which no shipped lane declared, went with workflow
contract 4.)

A queued dispatch writes its wait into the work item's event log (the
task stream the Pi Web job detail renders): a `verify_queued` line
(`since`, `position`, `reason`) and, once admitted, a `verify_admitted`
line (`queued_ms`).

### Landed, awaiting the full gate

The develop lane is the fast first stage of three, and each
work item merges itself at its end — its conflicts with the default branch
are resolved then, while they are its own, never in a later batch. Since
workflow contract 2 every step of the lane is an agent step: `sync`,
`verification`, `publish` and `merge` are mechanical steps on the lane's
`cheap` tier, each following a shared skill
(`workflows/_shared/skills/<name>.md`, rendered into its prompt) that runs
one pre-written command verbatim — `sync.py`, `verify.py`, `merge.py` — and
routes on the exit code the command's result file carries; each first
checks what the step before it handed off (its skill says what: the
result file and the commits stamped in `handoff.md`). The daemon
orchestrates — the dispatch, the heavy-run admission, the merge lock, the
rewind — and reads no content, and no agent merges by hand:

- **sync** (no gate, after review; `"$PF_PYTHON" "$PF_SHARED_DIR/scripts/sync.py" --json --out
  artifacts/sync/result.json`) merges origin's default branch into each task
  repo's branch in place — a fast-forward when the branch has no commit of
  its own, else a merge commit — every in-tree merge attribute overridden
  for the merge (a branch's `merge=union` never resolves a conflict) and the
  conflicts the **default branch's** `.polyforge/integrate.conf` makes
  mechanical — the append-only `docs/upgrade.md` and its pins — resolved and
  committed by the command. A real conflict is left in progress and listed
  (exit 1): the sync step's own agent resolves it in place — both sides'
  intent kept, this change adapted to the default branch's, the conflicted
  files only — stages it and runs `sync.py --continue`, which refuses a
  residual marker, an unstaged path, a mechanically resolved file changed or
  a resolved text file that drops a line both parents carry, commits the merge and rewrites the same result file with the files under
  `resolved[]`. No review judges that resolution: the verification tests the
  resolved tree right after, publish's PR body lists the files verbatim for
  the owner ("conflict resolved in sync: <files>"), and an agent that is not
  confident — or finds the default branch changed a premise the change
  relies on — asks the owner (`pf step ask`) and acts on the reply (resolves
  as told, or runs `sync.py --abort` — the merge in progress aborted, the
  record dropped — and then hands off `rewind --to build --per-owner`). The
  command's record (`syncing.json` beside the result, tied to the step round
  it ran in) makes a crash re-entry safe: a plain run of the same round
  aborts a leftover merge, resets a HEAD that is neither where the sync
  started nor a recorded merge (the commit kept under
  `refs/polyforge/discarded/pf-sync/<n>`) and merges again, but refuses
  (exit 2) rather than discard work committed on top of the record; a later
  round supersedes an unsettled record (its merge aborted, nothing reset);
  a repo whose branch or origin moved under the run, or a linked worktree,
  is refused (exit 2). The agent verifies first that each repo's HEAD is the
  tree review judged (the end commit stamped on review's last `handoff.md`
  section), or its own earlier `after` when merge's FAIL
  sent the lane back here — a commit made outside the lane is asked about,
  never silently merged.
- **verification** (gate, `waivable: true`; `verify.py --tier light
  --unit-runs artifacts/build/units.json --json --out
  artifacts/verify/result.json`, under the heavy-run admission; each unit
  build recorded runs again as written beside the light plan)
  verifies first that the sync's result lists no conflict and that each
  repo's HEAD is its `after`, then runs the light tier over the tree as it
  stands — the default branch already merged in by `sync`; a conflicting
  default branch never reaches it. The result file's `verdict` is the exit
  code's alone and its `heads` name the tree it ran over; a red the agent can
  place on the change FAILs to `build`, a red it cannot place is asked
  (`pf step ask`) and waived only by the owner's words (`waive
  --per-owner`; the daemon records the waiver), never passed by the agent;
  an unchanged tree that already failed is asked about, not run again.
- **publish** verifies first the result file — `heads` at HEAD, and `pass`
  or verification's `waive` section in `handoff.md` whose quoted owner
  words really waive the red — and review's `done` (a check that does not
  hold FAILs to `build`),
  then pushes the feature branch at HEAD and opens or updates the PR — its
  body carries the sync record with every file the sync agent resolved, the
  verification's record (or the waiver, verbatim), the acceptance list, the
  coverage table and a `## Follow-up` section — writes `artifacts/publish.json`
  (what the merge step reads: per repo the branch, the PR and the pushed
  head), and never merges and re-runs no test.
- **merge** (gate; `merge.py --publish artifacts/publish.json --verified
  artifacts/verify/result.json --json --out artifacts/merge/result.json`,
  under the daemon's merge lock — in memory, one per repository
  (`host/owner/repo` lower-cased: https and ssh clones of one repository
  share it), every repo of the item locked in one order) verifies first that
  every published repo's `head` is its entry in the verification result's
  `heads`, then runs the command, which judges every PR before anything
  merges — a PR already merged (a resume) counts only by the landing rules:
  its branch side the verified head, or, merged outside the lane, a merge
  commit on the default branch that contains the verified head
  (`merged_elsewhere`); an open one must target the default branch, head the
  published branch and stand on origin at the verified head — and merges each
  open PR with `gh pr merge --merge --match-head-commit <verified head>`. A
  moved default branch is **not** verified again and never merged into the
  branch here: a conflict with it is the forge's refusal. A PR that moved,
  closed, was retargeted, merged elsewhere off the verified head or refused
  by the forge is exit 1: the gate FAILs back to `sync` naming it (counted in
  `max_rewinds`); the merged ones stay landed and the re-run takes them by
  the landing rules. gh or git unreachable is `blocked`: fix it, then
  tell the step (or `pf resume`). The integration lane's revert takes the same lock.

The merge's pass ends the workflow: before handing off `done` the agent
registers the landing record `merge.py --landing-out` wrote — `{state:
landed, repos: [{repo, pr, head, light_verified_head, merge_commit,
accepted?}], merge_commits}` — as a wrap attr (`pf step hub wrap-attrs
integration @artifacts/merge/landing.json`; the step declares
`wrap_attrs: [integration]`, so its `done` is refused until it did), and the
daemon's wrapped close merges it into the work item's attrs: one `repos`
entry per merge; `merge_commits` maps each repo to its merge commits,
oldest first (`{repo: [sha, …]}`). The owner's waivers ride beside it in
`attrs.waivers`, written by the daemon, never by the agent (aihub merges a
wrapped session-end's `attrs`; no new work-item status).
The landing record carries no `waived`: the integration lane reads an
item's waivers from `attrs.waivers` alone (an item that landed before
contract 4 has none there, and its record's own `waived` is read
instead, until no such item is left). An owner waiver
of verification's FAIL lands with the heads its red run tested: publish's
PR body says the change's light verification was red and waived (the
owner's words verbatim), and the integration lane's full gate is its next
bar; publish and merge are never waived. The
integration lane then runs the full gate over the default branch's landed
work items and moves each one's `integration` on.

micro lands the same way since workflow contract 2 (change 10): its `sync`
runs `sync.py` on an agent step (a real conflict resolved in place by the
sync agent), its `evidence` gate runs `run.py --isolation host --plan-from
artifacts/fix/evidence.json` where develop's verification runs
`verify.py`, and its `merge` runs `merge.py --verified
artifacts/run/result.json` — the verified head is the evidence run's —
and records the same landing; `evidence` is its one waivable gate, its
waiver recorded like develop's verification's.

### The integration lane

The second stage (`workflows/integrate/README.md`). The
develop lane merges every work item into the default branch itself at the
end of its run and wraps it `integration: {state: landed, repos,
merge_commits, waived}`; this lane only gates what landed and marks it
green. (A project that switches develop's `merge` off,
`steps.<project>.merge.skip: true`, has its work items end after `publish`,
wrapped `integration: {state: published, …}` with the PRs awaiting a human
review: the collect takes `landed` work items only, so they never enter a
batch.) A batch is a work item on the `integrate` bundle, and only a person
starts one, like a work item on any lane: `pf file create --bundle
integrate --project <p> --goal <text>`, then `pf submit <batch>`. The daemon
never files a batch of its own, however long landed items wait.

One unfinished batch per project is the lane's declaration, `exclusive:
true`: the daemon forwards a create on the lane exclusive, and aihub refuses
it atomically while another work item of the project on the lane is
unfinished — of two people starting a batch at once, only one files. The
refusal names the open batch and its status; a batch still queued that no
machine ever claimed (its filer stopped between filing and submitting it)
is named with `pf submit <batch>` to run it or `pf cancel <batch>` to drop
it.

A batch's steps are four agent steps, each running one pre-written command
under its skill (`workflows/integrate/skills/<step>.md`) and routing on
the exit code. The
commands read aihub through `pf step hub list` and `get`, and write the
aihub writes they decide (fix tickets, record moves, hold clears) to a file
the skill registers with one `pf step hub batch`; the daemon makes them
with its own credentials (a step agent holds none) once the step hands off
`done`. The batch's record is the wrap attr `integration_batch` that
`close` records:

- `collect` (`integrate.py collect`, the workflow's script; the mid tier) — the command reads the
  project's integration pool through the daemon (the landed items, the
  open batches, the heads the newest red batch failed on, the state of
  every registered fix ticket), pins each task repo's default-branch head and
  takes the batch: the landed items each of whose merges (`merge_commits`,
  every one) is in its repo's pinned head, one of them in a gated repo and
  under no green tag yet; a repo is gated when its pinned head is new —
  neither its newest green tag nor the head the last red batch's gate
  failed on — and carries such a merge. Nothing is gated (the batch ends
  `empty`) when no pinned head is new; another batch of the project
  running refuses it (`refused`: nothing is pinned, the steps after it do
  nothing). The command also judges every hold: a `landed` item a red
  batch blamed (`red_rounds`, `fix_tickets`) holds the green tag — it
  moves to `merged` only once a gate passes over its fixes — but never
  stops anything from merging: the develop lane keeps merging while the
  default branch is red. A held source none of whose registered fix
  tickets is open any more — still in develop, or landed after the gate
  that last blamed it (its record's `red_heads`) — is **stuck**, and the
  step's agent puts every stuck hold to the owner in one `pf step ask`; the
  owner's words decide: revert (the item goes to the revert step with its
  landed fixes, and this batch's gate runs over the revert), clear (an
  `attrs` write citing the owner's message: the item moves to `merged`
  with its `hold_cleared` record, the owner's words its reason — the only
  way a hold is cleared; a cleared hold fixes nothing — a red still on
  the default branch keeps failing every later batch until it is fixed or
  reverted) or fix (a `file` write on the develop lane, with its acceptance,
  registered on the held item by an `attrs` write — the hold reads its
  registered tickets, so it is open again while the new one can release it).
- `revert` (`revert.py`, the cheap tier; the merge lock) — carries out the
  reverts the owner ruled, collect's and a red gate's (the ones a full-gate
  FAIL names in its hand-off): each item and its landed fix tickets reverted
  per repo, newest first, on a revert PR opened and merged under the
  repo's merge lock — the one develop's `merge` step holds, so nothing
  else merges meanwhile — the gate's pins following the reverts, and the
  item's record moved to `reverted` (an `attrs` write). Nothing to revert
  only copies the pins. A revert the forge refuses is a `pf step ask`: the
  owner fixes it and the agent retries, or rewinds, or cancels.
- `full-gate` (the script `gate.py --isolation container --capsule --pins`,
  no tier — its step key, else
  the project default; heavy admission; the lane's gate) — runs the release's test gate once on every
  gated repo's pinned head — never the live tip, which keeps moving (what
  `.polyforge/release.sh` runs; never the daemon's
  verification run) — and on a pass writes the green tag itself
  (`--mark-green`). A red gate is triaged by `gate.py --triage`: each
  failing unit re-run once (the conf's `rerun=` template for an aggregate
  command; every failure flaky → the gate once more), a unit still red run
  at the baseline (the newest green tag merged into the gated head, else
  the parent of the batch's oldest merge in the repo) and pre-existing
  only by the verification's own rule — it exists there and fails with the
  same signature (a test the batch added never is); the rest attributed to
  the landed items by file, `carries:`, Go package and the scope engine's
  selection for each change (one per merge) — a single candidate blamed
  only when the unit is red at one of its merges (each checked, oldest
  first) and not at that merge's first parent — and otherwise bisected
  over every first-parent commit since the baseline (both ends tested; a
  unit absent at a commit is not failing there; a first red commit that is
  no landed item's merge is outside the batch and the owner's). The step's
  agent decides on the triage: one fix ticket per root cause, a `file`
  write whose acceptance names each red unit that passes on the fixed head
  (filed on the develop lane at high priority under a stable key, so a
  resumed batch never files twice) and, per blamed source, an `attrs` write
  (its round, the gated heads as `red_heads`, the ticket, named `key:<k>`
  until it is filed); the batch's result is then `red`. A source whose fix
  tickets failed two rounds, a pre-existing or an unattributed red goes to
  the owner (`pf step ask`): revert → the gate FAILs naming the item, the
  rewind to `revert` carries it out and the gate runs again on the
  revert's merge in the same batch (the FAIL is handed off with
  `--per-owner`, charged to nothing); rerun → the same pinned heads gated
  again (at most twice); close → the batch ends red now. A gate that
  cannot be judged three times in a row hands off `blocked` (its body
  names why and the last gate log's tail): fix the machine, then tell the
  step (or `pf resume`) and it gates the same pinned heads again. The fix
  tickets go through the develop lane and merge themselves like any work
  item; a later batch gates them.
- `close` (`integrate.py close`, the cheap tier) — decides the result from
  the gate (green: every judged repo passed and carries its tag; red: a
  repo failed; empty: refused, nothing gated or no gate ran), moves to
  `merged` every landed item each of whose merges is in a green-tagged
  commit of its repo (`attrs` writes) — whatever the verdict, an item whose
  wrap came after a tag covering its merge moves too — and records the
  batch's record as the wrap attr `integration_batch` (`result`, `main`,
  `tags`, `items`, `green_items`, `fix_tickets`, `hold_rulings`,
  `reverts`, …), which the wrap merges into the work item's attrs. A red
  batch tags nothing; the heads its gate failed on are the next batch's
  "last red" heads, so a later batch waits for that repo's default branch
  to move (a repo that passed, or could not be judged, is gated again).

A work item's attrs `integration.state` is `landed` (merged into the
default branch by the develop lane, no green tag over it yet; `repos`,
`merge_commits`, `waived` are the develop lane's and never rewritten; after
a red batch `batch`, `red_rounds`, `fix_tickets`, `red_heads`), `merged`
(on a green commit: `batch`, `green_tag`, `green_commit`, `green_tags` per
repo; or released by the owner: `hold_cleared` — `reason`, `by`, `at`, and
for a batch's ruling `answered_by`, `answered_at`, `cleared_in`) or
`reverted` (`revert_prs`). Only the daemon writes it, through aihub's
generic signed machine route `PATCH /api/v1/work-items/{id}/attrs` — the
whole `integration` key, fenced on the whole record as the write's
script read it (`CORE-409` when another write changed it since: aihub
merges nothing, so no write drops another's keys), audited as
`attrs_written`; a step agent reaches it
only as a pending `pf step hub attrs` write of a key its step's
`hub.attrs` names. aihub keeps none of the lane's rules: the lane's
scripts move a record only `landed` → `landed`, `merged` (on a green tag)
or `reverted`, never rewrite the develop lane's keys, and write a cleared
hold's record (`hold_cleared`) themselves; a write carrying out the
owner's word records the owner message with it (`--per-owner`). A hold
is cleared only by a running batch: its `collect` step asks the owner, and
the agent's write cites the reply. The batch's own record rides its wrap as
attrs `integration_batch` (`result` green, red or empty; `main` — per repo, the
head tagged green, or the head a red batch's gate failed on; `tags`;
`items`, `waived`, `green_items`, `fix_tickets`, `hold_rulings`;
`refused` or `empty` saying why nothing was gated).
The daemon has no face of its own for the pool or the batches: a batch
is a work item like any other (`pf watch <batch> --once`,
`pf ops status`), and the landed items are aihub's records
(`integration.state` landed); `pf ops status` never reads aihub.

### Claim

The act of taking a queued work item for execution: `pf submit <id>`
asks the local daemon to claim it, which clones the capsule from
aihub's revision chain, restores progress, and starts (or resumes) the
driver. One live session holds a work item at a time; a dead or stuck
holder is fenced by ending its session, after which any machine claims
the work item again with a plain submit.

### Machine identity and conversations (cross-machine face)

A daemon owns one data-dir-scoped Ed25519 key (`<data-dir>/machine-key`),
created atomically on first boot and reused for the machine id's lifetime.
The machine's first heartbeat, signed by that key, binds its public key, the
launcher kind and the negotiated role (`interactive` or `runner`) to the
machine row; the key's user is the machine's operator, and any project
membership (or a global admin) qualifies it to register. Exactly one interactive daemon exists per user:
a second one is refused (`interactive_refused`), and `pf ops role
interactive --force` supersedes it without interrupting execution. Heartbeats and the other
machine-face requests are signed and replay-protected. The one exception is
a bounded transition: an already-registered older daemon's unsigned
heartbeat refreshes only its row's liveness, and that branch is deleted
under the condition stated in
[reference.md §4.3](design/polyforge-vnext/reference.md).

A conversation is a permanent, append-only user-to-user 1:1 history in
aihub: the first message creates the direct conversation, every later
message reuses it, each member has a read cursor, and a message addresses a
human user only (never a chat, WI, machine or broadcast). Message content
is secret-scanned before storing. `pf watch <wi> --once` resolves a WI held
by another fresh machine through a signed, relayed progress query and shows
`holder_machine`; stale holders answer a typed error rather than stale
data, and nothing about a query is persisted. The asking daemon keeps the
relayed snapshot for 10 s, so a poller's remote progress (the Polyforge
panel's) refreshes every 10 s rather than on every poll (any change of the WI
at aihub — a status change, a claim, a session end — refreshes at once).

### Decision feed

The daemon's machine-wide list of human decisions: a question to answer,
a gate to pass, a failed, blocked, completed or canceled run, a step
judged dead, a rejected or dropped submit, a daemon restart — and every
background task's terminal summary — across every Cell of the Host,
newest first (the newest 300, none older than 14 days). An entry that
needs a human stays open until the run answers it or moves on (the
daemon resolves it) or the human dismisses it. Pi-web's Polyforge panel
reads it and counts the open entries; `pf watch decisions` prints it and
`pf watch dismiss` marks entries seen. No session is woken and no session
identity exists: an agent relays a decision only when the human brings it.
It replaced the retired chat identity and its subscriptions.

## Unattended mode — when a work item merges with no human in the loop

The behavior the code actually has, stated plainly: **when no question
is asked and the merge rule holds, the work item merges unattended** —
the delivery lane pushes the branch, opens the PR and merges it with no
human ever looking at it. Both shipped delivery lanes, develop and micro,
merge unattended. Which lanes do this is the lane set, not a
setting:

- The **develop** lane merges unattended: its `publish` step pushes the
  branch at the head the verification ran over and opens the PR, and its
  `merge` step merges it into the default branch with `merge.py` (above, "Landed,
  awaiting the full gate") — when none of its human stops fires: `build` does not grill the
  ticket (the requirement was grilled in the session that filed it, and
  `pf submit` refuses an execution ticket without an acceptance section);
  it asks the owner (`pf step ask`) only on a surprise, right after the plan and
  never at completion — the code reveals something the ticket did not
  decide (a dead end, a contradiction), the plan diverges from the
  ticket, the ticket carries `confirm_plan: true` (one stop right after
  the plan, before the first test), or the ticket has no acceptance list or
  an incomplete one (never self-written: `build` proposes it and asks
  once with the plan). The owner answers in free text,
  which `build` reads: its hand-off states each decision, the owner's words
  and its reading, and review checks the reading against the words; a defect it
  cannot reproduce goes to the owner; once the lane's 3 rewinds
  (`max_rewinds: 3`) are spent, `pf step handoff` refuses a gate's `fail`
  that cites no owner message, so the gate's agent asks the owner first; sync blocks on
  the environment (not a question; `pf resume` once fixed) when it cannot
  merge the default branch three times in a row; publish asks the owner (`pf step ask`) when an `owner-review:` artifact
  is declared; and merge blocks on the forge (not a question) for a PR it cannot take. Its review
  blocks only on five classes — `bug` (correctness, data, concurrency;
  security only as below), `ac_gap` (an acceptance criterion without a real test, or
  unmet), `project_rule` (the repo's `AGENTS.md`), `test_weakened` (a test
  relaxed, skipped or written weak) and `guarantee` (a removed or weakened
  guarantee without an owner ruling, a manual-gate bypass, a spec-forced
  invention, user-visible wording that contradicts the behaviour); an owner
  reply the change ignores is `ac_gap`, a deviation or a self-written
  acceptance list without an owner reply, or a reading of the owner's
  reply its words do not support, is `guarantee`, and an e2e scenario timed by a
  fixed delay, measuring before its state settles or carrying a wrong
  `carries:` is `test_weakened`; every other finding is non-blocking and
  reaches the owner in the PR body. Every review (micro's too) does no
  security probing — no offensive probing, injection reproduction or
  privilege-escalation attempt — and judges functional correctness,
  acceptance, project rules and regressions: a security risk it notices is
  one non-blocking line, unless the change is itself a security feature or
  the risk breaks a written rule (a repo's `AGENTS.md`, a principle, a
  review checklist item) (owner ruling 2026-11-10,;
  `_shared/review_security.md`). `build` writes the e2e scenarios too:
  a user-visible criterion names one (or why a unit test suffices), and an
  e2e scenario is a unit, red before its implementation like any other.
  No daemon check judges what `build` reports (workflow contract v2): it
  runs each unit red then green itself and records it in
  `artifacts/build/units.json` (`[{id, test_commit, impl_commit, run{repo,
  cwd, argv}, marker}]`, the run exactly as it ran red and green; the
  daemon checks only that the file parses), and the steps after it verify
  it. Review runs three pre-written lines of the workflow's script
  `facts.py` as written — `plan-conformance` (the plan's two-way
  conformance with the diff), `build --replay --isolation host` (each
  unit's test commit a one-parent
  ancestor of its implementation commit holding test paths only, every
  change to a unit's test after its red commit as a `test_weakened` fact,
  and each unit's run replayed on the tree of its test commit, under the
  heavy-run admission, on the host under the step's command `PATH`: a
  run that passes there is a test the old code already passed,
  `test_weakened`; one that could not run there — a build, setup,
  compile or import error, a missing module, `not_runnable` — is not red
  either: its red evidence is missing, `test_weakened` too) and `carries` (an e2e suite
  the build added, or whose run a unit records, whose `carries:` matches
  none of the paths it touched; an edited suite owes a well-formed
  `carries:`) — and judges the coverage (every criterion of the judged
  list exactly once), the hand-off's decisions against the owner's own
  words and, on a `confirm_plan` ticket, a reply older than the first test
  commit. A blocking finding names its class in `artifacts/review.md`;
  every FAIL resumes `build` as an ordinary charged round. Verification runs each unit's recorded run again on the
  final tree (`verify.py --unit-runs`). No sweep follows a review PASS:
  its text-only findings reach the owner in the PR body like any other.
  **Same-tree rules** (the skills', replacing the daemon's dispatch-time
  guard, which stays until it is deleted and compares the trees alone): a
  round a FAIL opened must change the tree — or, for a FAIL naming a
  unit's record, units.json — or `build` asks; review never judges again
  the heads and units.json its last round judged, and verification never
  re-runs a tree and units.json an earlier round failed — each asks
  instead.
- The **micro** lane merges unattended too — `fix` → `review` → `sync` →
  `evidence` → `publish` → `merge`, its merge step running `merge.py` as on
  develop — and has no declared checkpoint, but any step may still ask a
  human when it needs one (`pf step ask`): sync when it is not confident a
  conflict resolution keeps the ticket's intent, evidence on a red the
  change cannot have caused, and publish once (exactly one human check
  before the merge) when the work item declared an `owner-review:`
  artifact. It has no verification step, so its merge credential is
  narrower by the owner's ruling, and only for paths a repository declared
  text-only: the review pass plus the `evidence` step's green run, in the
  verification container, of the repository's fast-phase commands and
  declared micro checks on the synced HEAD — the head publish pushes and
  the merge step merges. A diff outside a repository's base declaration (or
  touching a repository that declares none, or one that cannot be read) is
  not micro's: `fix`'s own `facts.py micro-bounds` run finds it, `fix` asks
  the owner to re-file the work on the `develop` lane and, on the owner's
  word, cancels the work item — nothing is pushed or merged; `review`
  runs the same judgment again and FAILs a change out of bounds.

Unattended merging is the default behavior of every delivery lane, and
nothing a user configures changes it — only a human stop that fires (a
question asked) keeps a human in the loop. Not every unattended lane has a
verification step: micro's fast-evidence credential replaces it.

### The "never unattended" list — settled, docs-only policy

The owner has confirmed (2026-10-22) that the following kinds of change
should NEVER merge unattended:

- release/deploy work;
- schema or data migration;
- credentials and permission surface;
- removing features or weakening guarantees the work item does not name;
- security-related changes;
- large cross-repo changes.

This list is settled policy — not a draft awaiting confirmation — but it
is **docs-only policy: the code does not enforce it**. A develop work
item of any of these kinds still merges unattended whenever no question
is asked and the gates pass (the filing guidance never
proposes micro for them); nothing in the lanes, the
driver or the merge rule checks the list. The way to avoid an unattended
merge is on the filer: file the work item on **develop** with the content
line `confirm_plan: true`, so its build stops for the owner after the plan
(build asks with `pf step ask` before its first test, and review blocks a build
whose first test commit is older than the owner's reply), for any work item
in these categories.

## Where to find what

| The question | Where the answer lives |
| --- | --- |
| What does a term mean? | this page (the glossary above) |
| Installing, platforms, upgrading a work machine | [install](install.md) (the ops page; upgrade history: [upgrade](upgrade.md)) |
| A first from-zero run (developers, macOS) | the quickstarts: [developers](quickstart-developers.md), [macOS](quickstart-macos.md) |
| Daily use: filing, submitting, watching, answering | the [user guide](user-guide.md) |
| One command's exact shapes and flags | [commands](reference/commands.md) — `pf help`, `pf help <verb>`, the skill fragments |
| Every config key (cli.toml, .polyforge.yaml) | [config](reference/config.md) |
| The daemon's local routes | [UDS routes](reference/uds-routes.md) |
| Recovery failure codes (DAEMON-516, DAEMON-518) | [failure codes](reference/failure-codes.md) |
| Releases, build and test | [release](reference/release.md) |
| Extending without changing code | [extending](extending.md) |
| Authoring a custom workflow bundle | [bundles](bundles.md) |
| The system, the modules, the design docs | [architecture](architecture.md) |
| Contributing, filing work items, change coverage | [contributing](contributing.md) |