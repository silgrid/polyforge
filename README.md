# Polyforge

[![License: PolyForm Noncommercial 1.0.0](https://img.shields.io/badge/license-PolyForm%20Noncommercial%201.0.0-blue)](LICENSE)

## What Polyforge is

Polyforge is a team-level multi-agent collaboration system: it solves
the problems a team runs into when its work is done by AI agents,
across three pillars — **coordination** (a durable work-item lifecycle
with lane gates and recoverable capsules), **memory** (a project
memory system where experience and rulings settle) and
**communication** (cross-machine messages, a decision feed for every
human decision, task agents).

The mechanism underneath: AI coding agents are powerful, but their
work lives in a fragile chat context — close the session and the plan,
the progress and the reasoning are gone. Polyforge turns that work
into a durable work-item lifecycle. Every task is a work item with a
goal, a content body and acceptance criteria; a claimed work item runs
through a workflow of steps that hand off through written artifacts and
end in review and verification; and the whole run lives in a capsule
committed to a git revision chain, so any interruption is recoverable —
a crash behaves exactly like a shutdown, and the run resumes where it
stopped.

The system splits in two roles, both served by the one `pf` binary: a
server (**aihub**, run as `pf aihub` on the service host) that stores the
work items, the projects, the memories and the git revision chain, and
the work machine (`pf` CLI plus `pf daemon`) that executes the work on
your own machine.

## Web workbench

Polyforge's recommended web UI is **Pi Web** (its own npm package
`@silgrid/pi-web`, developed in this repository under [`pi-web/`](pi-web/)
and released with the same tag as `pf`): a local browser UI for the pi
coding agent with a Polyforge panel — work items, jobs and their step
graphs, project memory and messages. It is optional; `pf` works from any
harness without it. It ships only as part of the GitHub Release (the
`silgrid-pi-web-<tag>.tgz` asset, the same release as `pf`) and is installed
by the installer's `--with-pi-web`, not from npm — see
[install: the web workbench](docs/install.md#optional-the-web-workbench-pi-web).

## Core concepts

- **Work item** — the unit of tracked work: a goal, a content body, a
  project and a human-readable slug; filed with `pf file create`, it runs
  when it carries a bundle.
- **Capsule** — the work item's private workspace directory
  (`artifacts/`, `repos/`, `runtime-state/`), committed to the revision
  chain and restored on resume, so no interruption loses work.
- **Workflow lane** — the ordered steps a claimed work item runs, for
  example build → review → sync → verification → publish → merge,
  declared by the bundle the work item was filed with.
- **Memory** — durable, searchable project notes (designs, decisions,
  reviews, pitfalls, experience), written with `pf remember` and
  recalled with `pf recall`.
- **Daemon** — the local `pf` execution machine: it claims queued work
  items, spawns a step agent per step inside the capsule, and keeps
  every run recoverable.

## Try it in 30 seconds

Two preconditions make this real: a configured `pf` (an api key against
your aihub server — see the [user guide](docs/user-guide.md)) and a
running local daemon:

```
pf daemon ensure
pf file create --project <project> --bundle develop --goal "Add X"
pf submit <WI_ID>
pf watch <WI_ID> --once
```

`pf file create` prints the new work item's id; `pf submit` asks the
local daemon to claim it; `pf watch --once` shows one snapshot of the
run — status, current step and latest events.

## What you get

The three pillars, in features:

**Coordination** — the work runs accurately and recovers:

- **Durable, resumable runs** — every step's work lives in a capsule
  committed to the git revision chain; tmux keeps the daemon alive, a
  crash behaves exactly like a shutdown, and the run resumes where it
  stopped.
- **Tiered verification** — a lane's verification step runs the tier it
  declares (develop: the light tier, in the daemon); the integration
  lane's full gate runs over what landed. An e2e suite declares what it
  covers in a header comment (`// carries: components/Panel*`), and the
  light tier runs the suites whose declaration the change hits
  ([carries](docs/contributing.md#carries--declaring-what-an-e2e-suite-covers)).
- **A release lane** — cutting a release is itself a work item on the
  release bundle: build, verify and deploy run as gate steps
  ([release](docs/reference/release.md)).
- **Per-step model candidates and health telemetry** — the models that
  may run a step are declared per project and step, `pf submit
  --models` pins them for one run, a dying model rotates to the next
  candidate, and `pf ops models` summarizes each model's health —
  advisory and read-only, it never reorders or blocks a dispatch.

**Memory** — experience settles:

- **A project memory** — `pf remember` and `pf recall` store and
  search durable project notes (designs, decisions, reviews, pitfalls,
  experience), so hard-won context outlives any one session.

**Communication** — the team stays in the loop:

- **Cross-machine messaging** — `pf msg` carries permanent
  user-to-user 1:1 conversations with read cursors, so the people
  around the work stay in one place.
- **One place for human decisions** — every question, gate and
  failed or blocked run lands in the daemon's decision feed, shown and
  handled in pi-web's Polyforge panel (`pf watch decisions` on the
  CLI); `pf watch` shows each step's status, and a work item held on
  another machine is watchable through a signed, relayed progress
  query.
- **One-shot task agents** — `pf task spawn` starts a single fresh
  harness agent from a free-text prompt, budget-capped and pollable,
  for small jobs that need no work item.

## Documentation

- 中文导读 (Chinese orientation): [docs/zh.md](docs/zh.md)
- What a term means: [concepts and glossary](docs/concepts.md) — every
  term a newcomer meets, defined once
- Installing, platforms, upgrading a work machine: [install](docs/install.md)
  (upgrade history: [upgrade](docs/upgrade.md))
- A first from-zero run: the quickstarts —
  [developers](docs/quickstart-developers.md), [macOS](docs/quickstart-macos.md)
- Command shapes: [commands](docs/reference/commands.md) — `pf help`,
  `pf help <verb>`, the skill fragments
- Every config key: [config](docs/reference/config.md)
- The daemon's local routes: [UDS routes](docs/reference/uds-routes.md)
- Recovery failure codes: [failure codes](docs/reference/failure-codes.md)
- Releases, build and test: [release](docs/reference/release.md)
- The system, the modules, the design docs: [architecture](docs/architecture.md)
- Extending without changing code: [extending](docs/extending.md)
- Authoring a custom workflow bundle: [bundles](docs/bundles.md)
- Contributing, filing work items, change coverage: [contributing](docs/contributing.md)
- Daily use, including the recommended pi-web UI: [user guide](docs/user-guide.md)

## Commercial licensing

Polyforge is licensed under the
[PolyForm Noncommercial License 1.0.0](LICENSE): personal use, secondary
development/modification and distribution are allowed for noncommercial
purposes.

The license is mixed by path ([NOTICE](NOTICE)): `pi-web/` (the Pi Web
package) is MIT licensed, Copyright (c) 2026 agegr
([pi-web/LICENSE](pi-web/LICENSE)); the PolyForm Noncommercial license and
the grant below cover the rest of the repository.

In addition to that license, silgrid grants: personal and team internal use
is free, including within for-profit organizations for their own internal
purposes.

Commercial exploitation — selling Polyforge, offering it as a paid hosted
service, or embedding it in a commercial product — requires a separate
commercial license. Contact <wangxk1991@gmail.com>.
