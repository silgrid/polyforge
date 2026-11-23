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

Big work splits on the owner's confirmation: the original ticket moves
onto the epic lane (its goal and acceptance kept), and one sub-ticket
per decomposition unit is filed `--parent` at it — each sub-ticket lands
its own lane's work on the epic's children branch, and the epic merges
the accumulated branch into the default branch as one change. The
routing skill's file verb owns the filing steps — check-first per unit,
so a re-run files no duplicate.

### Workflow (lane)

The ordered set of steps a claimed work item runs, declared by its
bundle's `workflow.yaml`. "Lane" is the informal word for one
bundle's flow — people say "the develop lane" the way they say "the
develop workflow". Each lane's own narrative is its
`workflows/<bundle>/README.md`; the shipped lanes are **develop** — the
default development lane (build → review → sync →
verification → publish → merge: each work item merges itself into the
default branch at its end, as the daemon) — **epic** — one work item per
epic (verify-children → sync → build → review →
verification → publish → merge: an epic's sub-tickets each land their
own lane's work on the children branch, and the epic merges the
accumulated branch into the default branch as one change, reviewed by
its total acceptance and its seams, never re-auditing a child's
internals) — micro, integrate and release.
The four lanes develop superseded were removed
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
([the step
interface](reference/workflow-contract.md#the-step-interface)). The step
ends every dispatch with exactly one call naming a status — `done`, a
gate's `fail`, `blocked`, or, carrying out an owner's word, `cancel`,
`waive` or `rewind` — and a body in the shared seven-section template; the
daemon checks the call at once, stamps the section's header from its own
records (times, model, commits, the owner's messages verbatim) and appends
it to `artifacts/handoff.md`, the work item's one append-only hand-off
document that every later step reads first. Nothing else a step prints is
read as a result, and the daemon never reads the body for a verdict: it
reacts to the status alone (advance, rewind, park, end). A lane may declare
that a step's `done` needs proof the tree it changed still passes its own
checks: develop's `build` and `sync` do (`fast_check_proof`), so their
`done` is refused until every task repo the round moved has a `pass` result
at its HEAD — the daemon reads the result file the lane's declaration names
and never runs the check itself. A red the change did not cause the check
itself triages first: every unit still red after its one re-run is replayed
on the change's baseline (the same judgment verification runs), and one
the baseline carries the same way is recorded `pre_existing` — not the
change's red, the check passes. What is left — a this-change or unsettled
red, the default branch already red, an `error`/`partial` result, or a
lane that planned no check — is the owner's: the step asks, and on the
release hands off `done` with `--per-owner` naming the reply, the stamp
recording it.

A step agent that needs the owner asks with `pf step ask` and ends its
turn; the owner's words — `pf answer <wi> --text "<words>"` or the job
card, at any time — are recorded in the capsule and relayed verbatim into
that agent's conversation, never read by the daemon. An owner message to a
parked work item wakes the step concerned. What the owner wants beyond the
step's own work (cancel the work item, waive a FAIL, send the work back)
the agent hands off with that status and `--per-owner`, citing the
message.

A low-star card (every row ★ or ★★) is the owner's standing
authorization: with no reply before the Cell's `ask_auto_reply_timeout`
(default 30m) the daemon answers it under the reserved
`daemon:auto-recommendation` author with each row's recommended option,
and the decision feed shows 「已自动按推荐继续」. A protected stop — every
gate step (review, owner-review, merge, a gate FAIL), every step declaring
`protected_stop`, every step of a lane this build cannot verify, and every
step of a `confirm_plan: true` ticket — and any row at ★★★ or above keep
waiting for the owner; the automatic reply is never a `--per-owner`
authorization.

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
(disposable output in fixed categories, polyforge#121: `raw/` kept 3
days, `tasks/` and `sessions/` kept 14 days, one directory per run, no
loose file at its top level; `pf doctor` reports every entry outside
the categories and every batch past its retention, and
`--prune-scratch` removes only the stale batches). The notes file is
`AGENTS.md` when it exists, else
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
the sum of every Cell's running jobs, a Cell's optional
ceiling bounds its own; Cells with queued work start round-robin (no
weights, no preemption), a job waiting for a human with no live process
releases its slot, and every Cell's one-off tasks share their own Host
bound — `[daemon] max_concurrent_tasks`, default 2, live on reload
(polyforge#74: tasks take no job slot; summarize calls take none of it
either: each work item runs at most one
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
`[workspace."<path>"]` sections of a Cell's mapping file — `cli.toml` for
the default Cell, `cli-<profile>.toml` for a named one — each listing the
projects whose capsules live under that path. It is the daemon's only
placement source, read per Cell — and the CLI's placement verbs
(`file create`, `recall`, `remember`) validate their `--project` against
the same routed Cell's mapping (polyforge#17). Every mapped directory
must pre-exist, and `pf daemon ensure` starts the daemon only once at
least one Cell carries a real mapping.
The full shape is
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

polyforge#228 adds no new concept (`none`): the pi build's worker reuses the
daemon-owned step agent directory (`agents/worker.md` plus the embedded
`extensions/subagent`), the existing bundle format and the subagent tool's
existing output protocol; there is no new term, setting or bundle shape to
learn.

### Gate

A step that can FAIL the whole run: the delivery lanes' gates rewind it
to an earlier step, the release lane's gates end it (published
releases are immutable — no `on_fail`, no rewind into a re-release).
Which steps are gates is the lane's own choice — micro gates review,
evidence, publish and merge (its `sync` is no gate), each rewinding to its build step `fix` — merge's to `sync`; develop gates review, verification, publish and merge (its `sync` is no gate: a real conflict is the sync agent's to resolve in place), each rewinding to its one
build step `build` — merge's to `sync`; the release lane's release, verify and deploy steps are all gates. A
gate's FAIL verdict is the input of the rewind (or the ending) it
triggers.

The shared review gate — every lane's review except
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
toward nothing; and a lane may declare one class of FAILs uncounted
(`uncounted_rewinds`, a reason substring with its own cap): develop
declares merge's default-branch-moved FAIL — another lane's landing, not
this change's defect — so it spends no `max_rewinds` and counts on its
own, with `pf watch` showing the count apart and the cap asking the
owner exactly like a spent budget once reached. With the budget spent, a gate cannot hand off a plain FAIL
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
has one; develop's build writes its own
`artifacts/plan.md`) declares every file it writes together with its coupled
obligations (the documents, tables and projection rules that must change with
it, or an explicit none with a reason — a plan missing one is rejected), and
review checks the scope both ways: changed-but-undeclared files, and
declared obligations or writes the diff left unchanged. A plan written
on a ticket that carries a `## 分析` section also carries a `## 分析对照`
section mapping each item the analysis lists — each old test and document
obligation, each risk, each decided point — to the unit that carries it,
or the stated reason it is not taken (a decided point first; an analysis
written against an older base has its file:line references re-checked
against the current one). A plan written on a ticket that carries one
or more `## 已定`-opening sections also carries a `## 已定对照` section
mapping each of their items to the unit that carries it, or to a stated
reason — a ticket without such a section asks for no mapping section;
the close's plan-conformance names any unmapped ruling red (its number
and text), and review runs the same line. A review-driven
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

Verification runs the tier the work item's own attrs decide: develop's
`verification` runs `verify.py --tier auto`, which reads the claimed work
item (`runtime-state/work-item.json`) — a ticket carrying the
`integration_fix` attr (the marker an integration batch's full-gate writes
on its fix tickets) runs the **full** bar: everything the light tier runs
and the rest, the engine planning `--full` with the whole tool window as
the run's budget; every other ticket runs the **light tier** (the result
file's `heads` name the tree it ran
over). No ticket chooses its tier — the integration batch's marker is the
one switch, and the scoped tier with its
content marker is gone (workflow contract 1). The integration lane's gate
(`gate.py`) still runs the full bar over what landed.

`verify.py` verifies the tree as it stands: `sync` merged the latest origin
default branch into the branch before it, and a conflict is the sync
agent's, resolved before anything is verified — so the result file's
`heads` name the synced head; publish verifies that file first and pushes
exactly that head, re-running no test; the `merge` step merges it at that
head with `merge.py`, a default branch that moved since never merged in
and never verified again (below, "Landed, awaiting the full gate"). (The
daemon's `verify` action, which fetched and merged the latest default
branch itself before its bar ran, left with the daemon actions.)

### The light verification

`verify.py --tier auto` (develop's `verification` step,
skill `verify`) runs the **light tier** over every ticket but an
integration batch's fix — `workflows/_shared/scripts/verify-scope --light` in
each touched repo and in every nested engine root the diff reaches, i.e.
the fast gates, the affected unit tests, the e2e suites the change added
or changed or whose `carries:` meet it, and the build's unit runs
(`--unit-runs artifacts/build/units.json`), with the conf's forced-full
paths not applying (the full bar is the integration lane's `gate.py`, or
this step's own run on a marked fix ticket). Beside the plan the change
analysis's listed tests run beside the plan, add-only, under the analysis's
supplement cap (`--analysis artifacts/build/change-analysis.json`, the
file develop's build writes at its close): the union is the engine's
selection plus the tests the analysis lists, never less — a listed test
that does not exist fails its check naming it, and the entries past the
cap are cut in listed order and named in the result. A failing unit is re-run once — one suite of an
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
the host as above. The container runs on the backend the Host selected —
`[daemon] container_engine` ([config.md](reference/config.md)), `docker`
(the CLI client exactly as always) or `podman` (polyforge#159, 84-engine:
the same container convention under podman's own semantics, a prepared
image started by its normalized local ID, `image not known` the
missing-image answer, a build under a private crun `--no-new-keyring`
wrapper, the default bridge network); the daemon hands the resolved
value to every step as `PF_CONTAINER_ENGINE`, and a script's own run
without one keeps the docker client. The image is built from that file alone — an empty build
context, so the file must be self-contained: public base images, every tool
at an exact pinned version — and must carry what the scope engine itself
runs as well as the bar: `python3` (the engine is a python3 program), `sh`,
`bash` and `git` (its audit helper and the conf's commands are shell), plus
every toolchain the conf's commands call (`go` for a Go repository's
`go list`, `node`/`npm`, …). It is tagged
`pf-verify-<repo>:<hash of the file>`, built only when the tag is missing
(once, however many runs ask at the same moment; its log is
`runtime-state/verify/<n>/<repo>/image-build.log`).

A project whose container repos need private dependencies (a private Go
module the proxy cannot serve, a private npm registry) may instead prepare
the image itself: the verification step declares the `image` parameter
(the project sets `steps.<project>.verification.params.image` in its
workspace yaml — [reference/config.md](reference/config.md) — and a
ticket overrides it with `pf override`; empty, the default, keeps the
Dockerfile above). The convention is the preparer's: **the image is
prepared by a person** — its private dependencies and credentials are
solved when it is built, and **any trouble it has is the user's to
solve**; the lane never tries to fix or replace it. A set value names **one
prepared image for every container repo of the project** — the bar never
builds a Dockerfile then, and a repo needs none at all. The image is
resolved to its local ID (`sha256:<64 hex>`, its RepoDigests recorded
when it has any) and the container starts by that ID, **never pulled**: a
machine without it answers "not checked" (exit 2, the fixed mark
`pf-verify-image-missing` in the summary — the step hands off blocked
without retrying, and the summary names the image). The image **must run
as root** (`/run` must be writable for the keep-alive, the clone and the
cache volume writable for the bar). Two reminders for the preparer: the
daemon's allowlisted variables (`GOPROXY`, `GOPRIVATE`, …) still override
the image's ENV when they are set — a host `GOPRIVATE` makes go fetch
private modules over git inside the container, where there are no
credentials — and a new image starts with an empty cache volume, which
on its first mount copies whatever the image keeps under `/cache`; when
the dependencies change, rebuild the image. An image with private Go
dependencies (BuildKit keeps the credentials out of it):

```dockerfile
# syntax=docker/dockerfile:1
# pin the base image's Go to the version the repo's go.mod requires: the
# golang image sets GOTOOLCHAIN=local and never downloads another toolchain
FROM golang:1.26
WORKDIR /src
COPY go.mod go.sum ./
# go fetches a GOPRIVATE module over https by default: rewrite it to ssh
# (the agent this line mounts serves it) and know the host first
RUN --mount=type=ssh \
    sh -c 'mkdir -p -m 0700 ~/.ssh \
      && ssh-keyscan example.com >> ~/.ssh/known_hosts \
      && git config --global url."ssh://git@example.com/".insteadOf "https://example.com/"' \
 && GOPRIVATE=example.com/* go mod download
ENV GOCACHE=/cache/go/build GOPRIVATE=example.com/*
```

built from the project's own repository — the build context is the repo
(`docker build --ssh default=$SSH_AUTH_SOCK -f <this file> .` from its
root; its `go.mod` names the private modules) — so nothing of the agent
lands in the image: the socket lives only inside its one `RUN`. A token
works the same way as a secret in its env form
(`--mount=type=secret,id=go_token,env=GO_TOKEN`, read in that one `RUN`),
never in a file a layer keeps. The module cache stays inside the image
(`GOMODCACHE` outside `/cache`); only the build cache lives in the volume.

Each run starts one container per such repo (`--init`, labelled
`pf.verify.daemon` — the script's owner, `pf-step` — `pf.verify.boot` — a
fresh nonce of the run — `pf.verify.wi`, `pf.verify.run`, `pf.verify.repo`
and `pf.verify.expires`, the run's deadline) that
mounts, each at its own host path, only the repo's clone and the run's
base-worktree directory (read-write) and the release's
`tools/` directory (read-only), plus the repo's named volume of the image's
generation and the clone path,
`pf-verify-cache-<repo>-<the image's hash>-<the clone path's hash>`, at
`/cache` (point the toolchain caches there, e.g. `GOCACHE`, `GOPATH`,
`npm_config_cache`). The volume lives as long as the image at one clone
path: the same Dockerfile reuses it, any byte changed in the Dockerfile
(a toolchain or browser pin) gives a new, empty one, and two clones at
different paths never share one — a tool whose cached results embed
absolute paths (golangci-lint's result cache) would else read another
clone's — while the same path, however it is spelled (a trailing slash,
a dot, a relative path, a symlink alias), reuses its own: the clone is
mounted at its canonical path and the volume keys on the same one.
Within a generation its contents check themselves — Go's modules
against `go.sum`, its build cache keyed by the toolchain, npm's cache by
integrity — so a lockfile change only misses and downloads; the browsers
live in the image, and `node_modules` is never cached (each `npm ci`
installs it into the clone). Each container start removes the repo's older
generations no container references (a volume named exactly
`pf-verify-cache-<repo>-<32 hex generation>-<32 hex path identity>` of
an older generation than the started one's, that the engine lists as unused;
every clone path's volume of the running generation stays, and never
another repo's volume; the engine refuses to remove one in
use, so a verification still running on an older image keeps its volume
until a later start). Volumes accumulate one per clone path per image
generation — every capsule, every integrate batch has its own path — and
the pre-path shared shape `pf-verify-cache-<repo>-<32 hex>` is left to a
person (docs/upgrade.md); the base-worktree directory a run mounts beside
the clone shares the clone's volume, one path the naming scheme does not
separate. A cache suspected corrupt is removed with
the selected backend's `volume rm` of the current volume: the next run starts cold. It has its own pid and network
namespaces, no docker socket, no `pf` binary, no operator configuration, and
an environment that is the image's own plus, when the daemon's environment
sets them, `LANG`, `LC_ALL`, `TZ`, the proxies (`HTTP_PROXY`, `HTTPS_PROXY`,
`NO_PROXY`, either spelling), `SSL_CERT_FILE` / `SSL_CERT_DIR` (paths as the
container sees them — nothing of the host is mounted for them), `GOPROXY`,
`GOPRIVATE`, `GONOSUMDB`, `GOFLAGS` and `npm_config_registry`, and the run's
own `TMPDIR` / `E2E_JOBS` / `GOMAXPROCS` — no `PF_*`, `TMUX*` or
credential. Its `PATH` is the image's: the step's `PATH` (`[daemon]
command_path`) applies only to a bar on the host. The scope engine and every bar, flaky re-run,
confirming re-run and base run is one engine `exec` in the same working
directory; git (the base worktree, the scans) stays on the host.
After each command every process it left in the container is killed — a
timed-out command's whole tree included, which killing the `exec`
client alone would not do — and the container is removed at the run's end,
at the run's deadline and on a pause or shutdown. Every engine call but the
image build (bounded by the run's deadline) and a command's own
exec (bounded by its timeout) gets at most 2 minutes, so a container engine
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
kernel's OOM kill took Chromium's renderer).
The logs, the junit reports and `result.json` have the
host run's shape; the repo's `repos[]` entry adds `container` (image, name,
whether this run built the image, the build log, the limits). The
engine unavailable, a failed build, a refused start, a container lost mid-run or a
`requires_cmd` probe the engine could not run (never read as a missing command)
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
`locks: [bar]`, which no shipped lane declared, is gone.)

A queued dispatch writes its wait into the work item's event log (the
task stream the Pi Web job detail renders): a `verify_queued` line
(`since`, `position`, `reason`) and, once admitted, a `verify_admitted`
line (`queued_ms`).

### Landed, awaiting the full gate

The develop lane is the fast first stage of three, and each
work item merges itself at its end — its conflicts with the default branch
are resolved then, while they are its own, never in a later batch. Every
step of the lane is an agent step: `sync`,
`verification`, `publish` and `merge` are mechanical steps on the lane's
`cheap` tier (a lane-declared tier, distinct from the per-ticket
capability tier below), each following a shared skill
(`workflows/_shared/skills/<name>.md`, rendered into its prompt) that runs
one pre-written command verbatim — `sync.py`, `verify.py`, `merge.py` — and
routes on the exit code the command's result file carries; each first
checks what the step before it handed off (its skill says what: the
result file and the commits stamped in `handoff.md`). A work item's
**capability tier** is the per-ticket layer above these lane tiers: the
pre-submit analysis recommends one Host `[step_tiers]` tier per step as
text, and the session sets it with `pf override <id> --step <s> --tier
<name>` after the owner confirms (an explicit `--models` chain on the same
step outranks it). The daemon
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
  `resolved[]`. Once the merge is committed, sync runs the lane's fast-check
  line over every moved repo (`artifacts/sync/fast-checks/<repo>.json`), and
  its `done` is refused until that result is a `pass` at HEAD — a structural
  break such as a stray brace stops here, not at the gate after; a
  fast-forward needs none, and a red that is not the resolution's is the
  check's own to triage first: one the baseline — after the merge, the
  default branch's tip — reproduces the same way is recorded `pre_existing`
  and the check passes; what is left (an `error`/`partial` result, no
  planned check, one the baseline could not settle) is released by the
  owner's word, the step then handing off `done` with
  `--per-owner`. No review judges that resolution: the verification
  tests the resolved tree right after, publish's PR body lists the files verbatim for
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
- **verification** (gate, `waivable: true`; `verify.py --tier auto
  --unit-runs artifacts/build/units.json --analysis artifacts/build/change-analysis.json
  --json --out artifacts/verify/result.json`, under the heavy-run admission; each unit
  build recorded runs again as written beside the plan)
  verifies first that the sync's result lists no conflict and that each
  repo's HEAD is its `after`, then runs the tier the work item's attrs decide over the
  tree as it stands — the light tier, or the full bar on an integration batch's fix
  ticket (its `integration_fix` attr) — and beside the plan the change analysis's
  listed tests run beside the plan, add-only, under the analysis's supplement cap
  (a listed test that does not exist fails its check naming it); the default branch
  already merged in by `sync`; a conflicting
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
  default branch that moved after verification (origin's default branch no
  longer at the base the verification result records in `details.repos[].base`)
  is never merged into the branch here and never merged over: the merge
  refuses it, and `sync` merges it, then the lane verifies and publishes again.
  A PR that moved, closed, was retargeted, merged elsewhere off the verified
  head or refused by the forge, or a default branch that advanced after the
  verified base, is exit 1: the gate FAILs back to `sync` naming it — the
  default-branch-moved FAIL is the lane's declared uncounted class, the
  others count in `max_rewinds`; the merged ones stay landed and the re-run takes them by
  the landing rules. gh or git unreachable is `blocked`: fix it, then
  tell the step (or `pf resume`). The integration lane's revert takes the same lock.

A project may also declare the develop merge done by a person,
`steps.<project>.merge.by: human` (fixed at the claim into the capsule's
`runtime-state/step-modes.json`; a claim of a lane that does not declare
the step `human_mergeable` is refused naming it). The merge step then
never asks the forge to merge and never judges the default branch's
freshness: it asks the owner over the PR links and the verified heads and
waits (the ask's release, so the waiting holds no step process), and on
the owner's answer it takes the person's landing by the human landing
rules — GitHub's three merge methods all count when they land the
verified change as it stands: **Create a merge commit** (its branch side
the verified head), **Rebase and merge** (every commit of the branch
replayed, matched by the patch-id fingerprint) and **Squash and merge**
(the branch's whole diff); a landing that does not match the verified
change — a commit added to the branch before the person merged, a content
changed on the way — is not taken: the step asks the owner again (exit 3,
never a FAIL back to `sync`). The landing record names the landed commit
set with each commit's merge flag, so the integration lane reverts a
squash or a rebase landing too (the plain commits with no `-m 1`).

The merge's pass ends the workflow: before handing off `done` the agent
registers the landing record `merge.py --landing-out` wrote — `{state:
landed, repos: [{repo, pr, head, light_verified_head, merge_commit,
accepted?}], merge_commits}` — as a wrap attr (`pf step hub wrap-attrs
integration @artifacts/merge/landing.json`; the step declares
`wrap_attrs: [integration]`, so its `done` is refused until it did), and the
daemon's wrapped close merges it into the work item's attrs: one `repos`
entry per merge; `merge_commits` maps each repo to the commit set the
change landed as, oldest first, each entry `{sha, merge}` naming the commit
and whether it is a merge commit (`{repo: [{sha, merge}, …]}` — the lane's
own merges are merge commits; a person's squash lands one plain commit and a
rebase the replayed ones). The owner's waivers ride beside it in
`attrs.waivers`, written by the daemon, never by the agent (aihub merges a
wrapped session-end's `attrs`; no new work-item status).
The landing record carries no `waived`: the integration lane reads an
item's waivers from `attrs.waivers` alone (an item that landed under an
earlier release has none there, and its record's own `waived` is read
instead, until no such item is left). An owner waiver
of verification's FAIL lands with the heads its red run tested: publish's
PR body says the change's light verification was red and waived (the
owner's words verbatim), and the integration lane's full gate is its next
bar; publish and merge are never waived. The
integration lane then runs the full gate over the default branch's landed
work items and moves each one's `integration` on.

micro lands the same way: its `sync`
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
batch. A project that declares the merge done by a person,
`steps.<project>.merge.by: human`, has its work items land through the
person's merge — the lane takes the landing and wraps them `landed`, so
they enter the batch like the lane's own merges do.) A batch is a work item on the `integrate` bundle, and only a person
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

A batch's steps are six agent steps. Four run one pre-written command
under their skill (`workflows/integrate/skills/<step>.md`), routing on
the exit code; the two between `collect` and `revert` — the batch's
repair and its review — are `rewind_only` (workflow contract's step key):
the first round records them skipped without a dispatch, and they run
only after a rewind covers them, which the gate's red hands back to them
over the lane's one `max_rewinds` budget. The
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
  registered on the held item by an `attrs` write — the ticket itself carries
  the `integration_fix` attr that makes its own verification run the full bar —
  the hold reads its registered tickets, so it is open again while the new one
  can release it).
- `repair` (`rewind_only`, no tier — its step key, else the project
  default; no hub verbs: it fixes on files alone) — the gate handed the
  batch back over a red every unit of which is attributed and fixable by
  touching only tests or documentation: it fixes exactly those units on
  one local branch per repo (`integrate/repair-<batch>`) off the pinned
  heads, runs each unit's own rerun command for the host green (the
  re-run gate alone decides the batch) and writes the branch plan the
  revert step lands. It never pushes and never opens a PR; a fix that
  would leave the tests-or-documents scope goes to the owner — fix anyway,
  revert the blamed item, or hand the units back (the next gate run files
  the fix tickets as it did before). A round that reached it with no
  repair brief changes nothing.
- `review` (`rewind_only`, a gate whose FAIL rewinds to the repair
  step; no tier, no skill: its definition is the whole procedure) — the
  fresh judgment of the repair's branch: the diff from the pinned base
  touches only test and documentation files, every brief unit fixed, no
  fix that weakens a test to pass it. Findings land in
  `artifacts/review.md`; a blocking finding rewinds to the repair step
  charged.
- `revert` (`revert.py`, the cheap tier; the merge lock) — carries out the
  reverts the owner ruled, collect's and a red gate's (the ones its
  decision file names), and lands the batch's repair branches the same
  way: each item and its landed fix tickets reverted per repo, newest
  first, on a revert PR opened and merged under the
  repo's merge lock — the one develop's `merge` step holds, so nothing
  else merges meanwhile — each repair branch pushed, its PR merged onto
  the default branch matching the pushed head (a fix whose blamed item
  the same run reverts is never landed) — the gate's pins following both,
  and the reverted item's record moved to `reverted` (an `attrs` write).
  Nothing to revert and nothing to land only copies the pins. A revert
  the forge refuses is a `pf step ask`: the
  owner fixes it and the agent retries, or rewinds, or cancels.
- `full-gate` (the script `gate.py --isolation container --capsule --pins`,
  no tier — its step key, else
  the project default; heavy admission; the lane's gate) — runs the full gate once on every
  gated repo's pinned head — never the live tip, which keeps moving (the
  batch's pins are the release lane's release object, polyforge#154; never
  the daemon's
  verification run) — with the repo's own scope engine, else the lane's
  copy (a repo that provides only `.polyforge/verify-scope.conf` and
  `.polyforge/Dockerfile.verify` is gated too) — and on a pass writes the green tag itself
  (`--mark-green`). The gate runs with `--keep-going`: every command runs,
  never stopping at the first failing one, so one red batch records every
  failing command and its triage sees them all at once. A repository that
  declares parallel groups in its `.polyforge/verify-scope.conf` — this
  project's Go bar and pi-web bar are two — runs them side by side, each
  group's commands in plan order in a container of its own; the progress
  lines of two groups interleave, the results and the summary stay in plan
  order, and a repository that declares no group runs exactly as before. A red gate is triaged by `gate.py --triage`: each
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
  agent first judges whether the batch repairs in place: every red unit
  attributed, no source past two fix rounds, the fix of every unit
  touching only tests or documentation, and the claim's frozen step modes
  not naming `merge` `human` (a project whose PRs await a person merges
  none by machine) — the gate writes the repair brief and hands off
  `fail`, charged, back to the repair step. Otherwise it decides one fix
  ticket per root cause, a `file`
  write whose acceptance names each red unit that passes on the fixed head
  (filed on the develop lane at high priority under a stable key, so a
  resumed batch never files twice) — each fix ticket filed with the
  `integration_fix` attr, an `attrs` write beside the file, the marker that
  makes the ticket's own develop verification run the full bar — and, per
  blamed source, an `attrs` write
  (its round, the gated heads as `red_heads`, the ticket, named `key:<k>`
  until it is filed); the batch's result is then `red`. A source whose fix
  tickets failed two rounds, a pre-existing or an unattributed red goes to
  the owner (`pf step ask`): revert → the gate rewinds to `revert` by name
  (`rewind --to revert --per-owner`: its own FAIL would go to the repair
  step), the revert carries it out and the gate runs again on the
  revert's merge in the same batch (charged to nothing); rerun → the same pinned heads gated
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
  `reverts`, `fixes` — the landed repairs: repo, branch, PR, merge, the
  fixed units, their attributed items — …), which the wrap merges into
  the work item's attrs; a green batch whose record carries `fixes`
  names the attributed source items in its wrap summary. A red
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
daemon resolves it) or the human dismisses it. A background task's
failed, capped or interrupted entry resolves on its own once the ticket
it served no longer needs it — the analysis rerun completed, the ticket
ended at aihub (wrapped, canceled, failed, or gone with CORE-404), its
analysis section carries confirmed content, or the entry is older than
three days; the daemon writes resolved, never dismissed, and a reopened
feed keeps it so. Pi-web's Polyforge panel
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
  that cites no owner message, so the gate's agent asks the owner first (the
  declared uncounted class has its own cap: a FAIL whose reason falls in
  it spends that, not `max_rewinds`); sync blocks on
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
  `carries:` is `test_weakened`, and so are a unit without its test-quality
  record (`workflows/_shared/test_quality.md`: the behaviour its test observes, how its
  expected values are derived independently of the implementation, the change it
  catches), a test that only asserts
  source text exists, a replayed red that contradicts the recorded reason, and an
  asynchronously settling state asserted on one read right after the action instead
  of a bounded poll (the bar's settle duty);
  every other finding is non-blocking and
  reaches the owner in the PR body. Every review (micro's too) does no
  security probing — no offensive probing, injection reproduction or
  privilege-escalation attempt — and judges functional correctness,
  acceptance, project rules and regressions: a security risk it notices is
  one non-blocking line, unless the change is itself a security feature or
  the risk breaks a written rule (a repo's `AGENTS.md`, a principle, a
  review checklist item) (owner ruling 2026-11-10;
  `_shared/review_security.md`). `build` writes the e2e scenarios too:
  a user-visible criterion names one (or why a unit test suffices), and an
  e2e scenario is a unit, red before its implementation like any other.
  No daemon check judges what `build` reports: it
  runs each unit red then green itself and records it in
  `artifacts/build/units.json` (`[{id, pre_existing_red?, timeout?, test_commit?, impl_commit,
  run{repo, cwd, argv}, marker}]`, the run exactly as it ran red and green; a unit
  whose reproduction test already exists and is red before the change — an
  integration fix ticket — declares `pre_existing_red: true` and reports no
  `test_commit`, its red evidence a replay on the tree of the change's base; a unit
  self-reports `timeout` (a Go duration) to raise the limit its run and its red-state
  replay are bounded by (the default is 10m, and a run its limit kills is an evidence
  problem, never a pass); the
  daemon checks only that the file parses), and the steps after it verify
  it. Its close runs the same facts first — `plan-conformance`, `build`
  with the replay (each unit's run red at its test commit) and `carries` — and hands off
  only on green. Before them its close runs the scoped selection
  verification plans — `fast_checks.py --close-scoped`, one shared
  implementation, for a work item whose verification runs the light tier
  (an integration batch's fix ticket runs the full bar and closes without
  one), in the repository's declared verification image when its HEAD
  carries `.polyforge/Dockerfile.verify` (the same isolation verification
  uses, the image and tool caches shared; no declaration keeps the host
  close, the result recording which isolation ran) — and merges a green
  record into the change analysis's `selected`; a `pre_existing` record — every red its baseline triage
  proved pre-existing — hands off `done` citing its result file and merges
  the same `selected`, the shared validator taking it as green-equivalent
  evidence. Review runs four pre-written lines of the workflow's script
  `facts.py` as written — `plan-conformance` (the plan's two-way
  conformance with the diff, and its `## 已定对照` mapping of the ticket's
  decided rulings — an unmapped one red and named), `build --replay --isolation host` (each
  unit's test commit a one-parent
  ancestor of its implementation commit holding test paths only, every
  change to a unit's test after its red commit as a `test_weakened` fact,
  and each unit's run replayed on the tree of its test commit, under the
  heavy-run admission, on the host under the step's command `PATH`: a
  run that passes there is a test the old code already passed,
  `test_weakened`; one that could not run there — a build, setup,
  compile or import error, a missing module or test runner dependency preflight (`node_modules/` or Playwright Chromium missing), `not_runnable` — is not red
  either: its red evidence is missing, `test_weakened` too) and `carries` (an e2e suite
  the build added, or whose run a unit records, whose `carries:` matches
  none of the paths it touched; an edited suite owes a well-formed
  `carries:`) and `analysis` (the change analysis's own close check — the same
  shared validator `verify.py --analysis` calls: every listed entry judged statically,
  and each taken one's run executed once at HEAD and judged by its marker; a red entry is
  a blocking `test_weakened` finding naming it) — and judges the coverage (every criterion of the judged
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