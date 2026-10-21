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
Standing up your own aihub is the README's
[Quickstart](../README.md#quickstart-developers--from-zero-smoke-run).

## Install

One line, any team machine. It needs `gh` authenticated (the one-liner reads
`gh auth token`):

```
curl -s -H "Authorization: token $(gh auth token)" \
  https://raw.githubusercontent.com/silgrid/polyforge/main/tools/install.sh \
  | bash -s -- <your-api-key> <server-host>
```

It installs the `pf` binary, writes `~/.config/polyforge/cli.toml` (your key and
the host), keeps a release checkout at `~/.local/share/polyforge/release/current`,
wires the polyforge routing skill into each harness it finds, and ends with
`pf doctor`. If the machine's daemon id is new to aihub, an admin registers it
afterwards -- the installer says so.

The one step the installer leaves to you: it writes a `[workspace."<path>"]`
template into `cli.toml`; uncomment one section per workspace you work in, with
its `projects` list, because the daemon refuses to start without a real
mapping -- the installer skips the daemon start until one exists. With the
mapping in place, run `pf daemon ensure` (or open a new harness session, which
runs the same ensure), then `pf doctor`.

Prerequisites, the macOS notes, and what an upgrade does are the [README's Install
section](../README.md#install-team-machines--read-this-first); this guide does
not restate them. Upgrading a work machine is re-running the same line (the
server-side schema story is [upgrade.md](upgrade.md)).

## Daily use in your harness

The primary path is natural language. The install wired the polyforge routing
skill into your harness, both pi and Claude Code, and the skill maps your words
to the right `pf` verb: file a ticket for X, how is polyforge#140 going, pause
that one. Ask for what you want in plain words; the skill routes, runs the verb,
relays the result, and asks rather than guesses when the intent is ambiguous.

Progress needs no polling. Claude Code shows it in the status line
(`pf watch --statusline`); pi renders the pf-chat extension's widget. The
installer never wires the statusLine: it and `pf doctor` check the progress
display and print the snippet to add when it is missing. Either harness wakes
you, one fixed-template line, when a work item needs a human decision: a
question to answer, a gate verdict, a failed or blocked run. The line is for
the human, not the agent: relay it, then handle it with the human's own words.

The terminal verbs are the reference form of the same surface. Every chat
intent has an exact verb underneath (`pf queue`, `pf submit`, `pf watch`,
`pf answer`, and the rest), the skill's fragments own each verb's real shapes,
and `pf help <verb>` prints them. When you already know the verb, the terminal
is fine; the chat path is the main road.

## Core concepts

Seven terms carry daily use. Each gets one definition here and a link to the
surface that owns the mechanism; this guide never restates the mechanism.

- **Work item** -- a unit of tracked work: a goal, a content body, a project, a
  status, and a human slug like `polyforge#140`. Filed and edited with
  [pf file](../skills/polyforge/verbs/file.md).
- **Bundle** -- the workflow a work item runs, chosen at filing
  (`--bundle autopilot`); the shipped bundles live under `workflows/`, and the
  shape is the [README's Custom workflow
  bundles](../README.md#custom-workflow-bundles) subsection.
- **Lane** -- informal for one bundle's flow: the [autopilot
  lane](../workflows/autopilot/workflow.md), the [feature-full
  lane](../workflows/feature-full/workflow.md). The shipped bundles are
  listed in the design docs' [system
  reference](design/polyforge-vnext/reference.md#71-bundles-and-materialization);
  a lane's own narrative is its `workflows/<bundle>/workflow.md`.
- **Gate** -- a step that can FAIL the run and rewind it to an earlier step;
  which steps are gates is the lane's own: the autopilot lane gates
  verification and deliver, the feature-full lane grill-review, verification,
  review and deliver. What each gate's FAIL means is the design docs'
  [execution section](design/polyforge-vnext/reference.md#7-execution).
- **Rewind** -- a gate FAIL sending the run back to an earlier step to redo;
  bounded per bundle, and past the bounds the run ends failed. The bounds are
  the same [execution section](design/polyforge-vnext/reference.md#7-execution)'s
  subject.
- **Capsule** -- the work item's private workspace directory (`artifacts/`,
  `repos/`, `runtime-state/`) that the daemon creates, drives, and commits to
  aihub. The contract is the design docs' [capsule
  section](design/polyforge-vnext/reference.md#8-capsule-and-git-chain).
- **Chat identity** -- the id of one interactive harness conversation
  (`chat_...`): how the daemon knows which chat to wake for a decision. The
  adapter wiring is [SKILL.md](../skills/polyforge/SKILL.md)'s chat identity
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
   section](design/polyforge-vnext/reference.md#7-execution)'s subject).
3. **Grill questions.** A lane's clarification checkpoint asks its questions
   after submit, when the run reaches it. They arrive as a needs_input wake-up
   line: read the question with `pf watch <id> --once`, answer with
   `pf answer <id> --question q-1 --text "<your words>"`. Answers are the
   human's; a step agent never answers them.
4. **Watch.** `pf watch <id> --once` for a snapshot (status, current step,
   latest events); the resident watcher in your harness for progress and
   wake-ups.
5. **The lane's work steps.** The bundle's own steps do the work: fix or code
   implements, verification runs the project's full bar, review judges. You
   watch and answer; you do not drive the steps.
6. **Deliver.** The workflow's own `deliver` step finishes the run: it pushes
   the feature branch, opens or reuses the PR, and merges under the repo's
   merge rule, or asks a human. Completion rides deliver; it is never a verb
   you run.

`pf wrap` is not the workflow's completion. It is the human close verb, two
moves only: closing a bundle-less ticket (a design, decision or tracking ticket
that never ran) with `pf wrap <id> --note "<why>"`, and recording the owner's
failed verdict on a live run with `pf wrap <id> --fail`. The shapes and failure
branches are [pf wrap](../skills/polyforge/verbs/wrap.md)'s.

## A worked example on the autopilot lane

This example is composed, illustrative only: never a transcript of a real run
(step agents must not drive the daemon). Every command shape is real.

A small, well-understood fix in project `demo`: the login endpoint times out
under load. Check the queue, then file it on the autopilot bundle (the lane for
small, well-understood work):

```
pf queue --project demo
pf file create --project demo --bundle autopilot --goal "Fix the login timeout under load" --content @login-timeout.md
```

Submit and watch:

```
pf submit demo#7
pf watch demo#7 --once
```

The lane runs grill (if asked), then fix, then verification, then deliver:

- **grill (if asked)**: the run may ask one clarifying question. It arrives as
  a wake-up line; read and answer it:
  ```
  pf watch demo#7 --once
  pf answer demo#7 --question q-1 --text "Reproduce with 50 concurrent logins, the fix is in the session store"
  ```
- **fix** implements the change in the task repo, its own commits, inside the
  capsule.
- **verification** runs the project's full bar on a fresh judge; a FAIL rewinds
  the run to fix.
- **deliver** pushes the branch and merges the PR under the repo's merge rule,
  or asks a human.

A later snapshot shows the run completed and the PR merged, and the queue lists
the work item wrapped -- deliver closed it. No human wrap verb ran.

**What feature-full adds.** The same flow with a full clarification pass and an
independent judgment around the fix: grill asks with a written spec,
grill-review confirms it, plan plans the build, e2e-author writes acceptance
scenarios for the new behaviour and proves them red before code implements,
and review judges the finished change against the spec before deliver. The
flow's shape is unchanged; what changes is how much clarification and judgment
surrounds the fix. The [bundle's own
workflow.md](../workflows/feature-full/workflow.md) owns the detail.

## Memory

Memory is a core pillar of daily use: the project's lessons live in aihub, not
in your head or your workspace.

- **Before starting work**, recall: `pf recall --project <project> "<topic>"`.
  Hits are non-binding history: designs, decisions, pitfalls; use what helps
  and ignore the rest.
- **When you learn something durable**, remember:
  `pf remember --project <project> --type experience.wrap --title "<lesson>" "<content>"`.

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
  workflow parks on an honest `incomplete` step result (the owner holds the
  next move), a pause can land during the claim, or its capsule or workspace
  mapping can go missing across a restart (the reason note names the cause).
  Exactly two exits: `pf resume <id>` -- after fixing the cause, where there
  is one to fix -- or `pf cancel <id>`. A failed verdict does not apply to a
  blocked one.
- **A dead machine**: a work item whose holder daemon is dead or stuck is
  recovered by an admin or a project maintainer: end the stuck session, then
  submit it again on a healthy machine ([pf ops](../skills/polyforge/verbs/ops.md)
  owns the move).
- **A suspicious machine**: `pf doctor` is the diagnostic first stop: config,
  mapping, connectivity, daemon supervision, the harness progress display. It
  reports and never auto-fixes.

[pause](../skills/polyforge/verbs/pause.md),
[resume](../skills/polyforge/verbs/resume.md),
[cancel](../skills/polyforge/verbs/cancel.md) and
[doctor](../skills/polyforge/verbs/doctor.md) own the shapes and their failure
branches.

## Where to find what

| The question | Where the answer lives |
| --- | --- |
| Install detail (prerequisites, macOS, what an upgrade does) | the [README](../README.md)'s Install section |
| One verb's exact shapes, success and failure branches | `pf help <verb>`, then the [skill fragment](../skills/polyforge/verbs/help.md) that owns the verb |
| Why it works that way (designs and decisions) | the [design docs](design/polyforge-vnext/reference.md) |
| Upgrading (the server-side schema story) | [upgrade.md](upgrade.md), plus re-running the installer line |
| Admin and operator work (users, api-keys, projects, machines, deploy) | the [README](../README.md)'s Quickstart and the management verbs (`pf users`, `pf api-keys`, `pf projects`, `pf machines`) |
| Releases | the [README's release flow](../README.md#release-flow): maintainer-only, cut on explicit owner request |
| Extending polyforge (config keys, custom bundles) | `pf help extensions` and the [README's Extending section](../README.md#extending) |