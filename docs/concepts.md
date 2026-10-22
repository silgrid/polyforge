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
work-machine binary (`pf`) whose **daemon** executes the work. The two
sides split cleanly: everything a human files, reads or decides is
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
`pf file create`, submitted with `pf submit`, closed by the workflow's
own deliver step or by a human's `pf wrap`. A work item with a bundle is
an execution ticket (it can be claimed and run); one without is a plain
tracking ticket that never runs.

### Workflow (lane)

The ordered set of steps a claimed work item runs, declared by its
bundle's `workflow.md` frontmatter. "Lane" is the informal word for one
bundle's flow — people say "the autopilot lane" the way they say "the
autopilot workflow". Each lane's own narrative is its
`workflows/<bundle>/workflow.md`; the shipped lanes are autopilot,
bug-fix, chore, feature-full and release.

### Memory

A durable note stored in aihub, scoped to a project: designs, decisions,
reviews, pitfalls, experience. Written with `pf remember`, searched with
`pf recall`. Memories are non-binding history — a step recalls them for
context, never as instructions. The last step of a workflow writes the
work item's one wrap-summary memory under the project's memory policy.

### Step

One unit of a workflow: a step definition file (`steps/<id>.md`) that a
step agent (a real harness session the driver spawns) executes, ending
in a result envelope. A step may be a gate. Steps hand off through
artifacts (spec → plan → code) and the driver records each result in
`runtime-state/execution.json`.

### Result delivery

The one path a step agent's outcome takes: `pf result submit`, which
lands the result envelope atomically with the daemon (which stamps the
calling step's identity and validates the shape). Nothing else a step
prints is read as a result. The envelope's `outcome` — completed,
gate_failed, needs_input, incomplete — is what advances, rewinds or
blocks the workflow.

### Project

The tracked-work unit a work item belongs to: the slug namespace
(`polyforge#140`), the memory scope, and the repo registry aihub serves
per project. Created by an admin (`pf projects create`), routed to a
workspace by the repo mapping.

### Workspace

A directory on a work machine that holds the capsules of the projects
mapped to it: `<workspace>/capsules/<project>-<seq>/`. Its layout is the
convention `pf init` writes: `.polyforge.yaml`, `CLAUDE.md`, `.repo/`
clones for humans, `capsules/` (daemon-managed) and `scratch/`
(disposable output). Anything meant to be shared goes to aihub, never to
a workspace file.

### Machine (work machine)

One computer running a `pf` daemon: the execution plane's home. A machine
has an identity (`m-<short hostname>`, recorded in
`<data-dir>/daemon-id` on first start), registers with aihub on its
first heartbeat, and holds the work items it claims until they wrap or it
is fenced. "Work machine" names the whole setup — machine, daemon,
supervision loop, harnesses — as opposed to the aihub server host.

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
revision chain — reached by the `pf` CLI over HTTP, direct. **Execution
plane**: everything the local daemon owns — the claim, the capsule, the
driver spawning step processes, the watch surface — reached over its
unix-domain socket. The routing skill's verb table follows the split:
`file`, `queue`, `cancel`, `recall`, `remember`, `memory` are data-plane
verbs; `submit`, `watch`, `answer`, `pause`, `resume`, `wrap`, `ops` are
execution-plane.

### Bundle

The installable unit a workflow ships as: a directory under the daemon's
workflows dir with a `workflow.md` (frontmatter: the steps, their order,
budgets, model candidates) and `steps/<id>.md` per step id. A work item
runs the bundle it was filed with (`pf file create --bundle <name>`); a
step with no file of its own falls back to the shared
`workflows/_shared/steps/<id>.md`. Authoring a custom bundle is
[bundles: the bundle-authoring chapter](bundles.md).

### Gate

A step that can FAIL the whole run: the delivery lanes' gates rewind it
to an earlier step, the release lane's gates end it (published
releases are immutable — no `on_fail`, no rewind into a re-release).
Which steps are gates is the lane's own choice — the autopilot lane
gates verification and deliver; bug-fix gates root-cause, verification,
review and deliver; chore gates verification, review and deliver;
feature-full gates grill-review, verification, review and deliver; the
release lane's release, verify and deploy steps are all gates. A
gate's FAIL verdict is the input of the rewind (or the ending) it
triggers.

### Rewind

A gate FAIL sending the run back to an earlier step to redo the work:
verification FAIL rewinds to the fix step in the autopilot and bug-fix
lanes and to the code step in the chore and feature-full lanes;
grill-review FAIL rewinds to spec; root-cause FAIL rewinds to repro.
The target is the gate's own `on_fail` declaration — never a later
step. Rewinds are bounded per bundle (`max_total`, `max_per_step` in the
frontmatter); past the bounds the run ends failed
(`failed_rewind_exhausted`) and the honest move is a re-filed work item
on a heavier lane, not more loops.

### Claim

The act of taking a queued work item for execution: `pf submit <id>`
asks the local daemon to claim it, which clones the capsule from
aihub's revision chain, restores progress, and starts (or resumes) the
driver. One live session holds a work item at a time; a dead or stuck
holder is fenced by ending its session, after which any machine claims
the work item again with a plain submit.

### Chat identity

The id of one interactive harness conversation (`chat_...`): how the
daemon knows which chat to wake when a work item needs a human decision.
Set by the harness adapter (`PF_CHAT_ID`), created on first use,
subscribed to the work items the chat submits. A label, never a
credential; step agents never see it.

## Unattended mode — when a work item merges with no human in the loop

The behavior the code actually has, stated plainly: **when no question
is asked and the merge rule holds, the work item merges unattended** —
the deliver step pushes the branch, opens the PR and merges it with no
human ever looking at it. Which lanes do this is the lane set, not a
setting:

- The **autopilot**, **bug-fix** and **chore** lanes merge unattended.
  Only autopilot has a human checkpoint at all (its grill step, which
  may ask a question — and then a human answers it); bug-fix and chore
  have none, so a run that asks no question and passes their gates ends
  merged with no human ever looking at it. In autopilot there is no
  review step at all — verification's pass stands in for the
  independent review at the merge rule.
- The **feature-full** lane never merges unattended: its grill-review
  gate requires a human answer, so a human is always in the loop before
  deliver can merge. That is the lane's shape, not a preference.

This is NOT "choose autopilot to enable unattended" — unattended
merging is the default behavior of every lane except feature-full, and
nothing a user configures changes it.

### The "never unattended" list — settled, docs-only policy

The owner has confirmed (2026-10-22) that the following kinds of change
should NEVER merge unattended:

- release/deploy work;
- schema or data migration;
- credentials and permission surface;
- removing features or weakening guarantees;
- security-related changes;
- large cross-repo changes.

This list is settled policy — not a draft awaiting confirmation — but it
is **docs-only policy: the code does not enforce it**. A bug-fix or
chore work item of any of these kinds still merges unattended whenever
no question is asked and the gates pass; nothing in the lanes, the
driver or the merge rule checks the list. The way to avoid an unattended
merge is on the filer: choose the **feature-full** bundle, whose
grill-review always requires a human answer, for any work item in these
categories.

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