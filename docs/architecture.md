# Architecture — the system and the design docs' entry page

Polyforge is a work-item orchestration platform with two halves: an
**aihub** server (the work-item store, the memory store and the git
revision chain behind them) and a **pf** work-machine binary (the CLI
plus the daemon with the Go execution driver). The split between the
halves is the [data plane vs execution plane
split](concepts.md#data-plane-vs-execution-plane-aihub-vs-daemon) —
the one concept this page assumes; every term a newcomer meets is
defined on the [concepts page](concepts.md).

## Modules

One row per sub-package, exactly as `internal/` exists today — the live
code is the authority; design-doc references are for traceability:

| Package | Role |
| --- | --- |
| `internal/common/api` | Shared business contract between aihub and pf: work items, projects, users, sessions, memories, and the typed error-code registry (`CORE-*`, `MEM-*`, `AUTH-*`, `DAEMON-*`, `GIT-*`). |
| `internal/common/atomicfile` | The one crash-safe "replace this file" primitive every state writer uses. |
| `internal/common/capsule` | Execution-capsule contract shared by pf (runtime creates capsules, the driver writes `execution.json`, the observe lane commits them) and aihub (serves the matching bare repos; git-dir lives outside the capsule). |
| `internal/common/protocol` | The in-machine contracts: Runtime↔Observe messages and the pf CLI↔daemon UDS HTTP API surface. |
| `internal/common/harness` | The closed step-harness set (`pi` \| `claude`) and the one model-candidate format `<harness>:<model>`. |
| `internal/common/spawn` | Process-derivation contract shared by the runtime (daemon assembly) and the driver (step-process spawning). |
| `internal/common/workflow` | The minimal workflow-bundle frontmatter read surface (defaults + per-step `model_candidates`) that drives model and harness selection, and the step-definition lookup: a bundle's own `steps/<id>.md` wins, else the shared `workflows/_shared/steps/<id>.md` (`_shared` is not a bundle; it is materialized beside the bundle). No digest contract: the bundle digest chain was deleted (polyforge#149). |
| `internal/aihub/core` | aihub persistent layer: SQLite store (single writer, WAL), the work-item state machine, claim/session arbitration (one live session per work item, its hashed push secret and public fingerprint), the ready queue and the admin face. |
| `internal/aihub/git` | aihub git surface: per-work-item bare-repo lifecycle, the git-http-backend smart-protocol proxy, session-secret auth (only the work item's live session secret pushes) and the pre-receive hook enforcing the capsule-chain invariants. |
| `internal/aihub/memory` | Core-side memory store: memories kept in the database (`memories.content`), FTS5-trigram full-text search, and the one-time content migration from the former `memory.md` files. |
| `internal/aihub/server` | The AIHub HTTP face: business endpoints, the admin face, and the `/git/{wi}.git/...` proxy mount; Bearer API key auth, typed `api.Error` envelope. |
| `internal/polyforge/aihubclient` | Shared HTTP client for the aihub server, used by runtime, observe and the CLI verbs; decodes the typed error envelope back into `*api.Error`. |
| `internal/polyforge/cli` | The `pf` client CLI: the routing verbs (file, queue, cancel, recall, remember, memory, submit, `watch`, answer, pause, resume, wrap, ops), `chat`, the management face (users, api-keys, projects, machines, backup), the daemon client over the UDS socket, and config resolution. |
| `internal/polyforge/daemon` | Assembly of the local work machine: the UDS HTTP face, runtime, observe lane and driver wired together, the auto switch, the pending-job dispatcher, the auto-pull loop, the driver start (a running `Driver.Run` is the job's agent handle) and the shutdown. |
| `internal/polyforge/gitops` | Shared capsule-git operations layer used by Runtime (clone at start, dirty-commit at recover) and Observe (async commit+push); owns the per-work-item git mutex. |
| `internal/polyforge/mapping` | The workspace→projects plan read from cli.toml's `[workspace]` sections, and its validated inverse (project → workspace). |
| `internal/polyforge/sessionjsonl` | The single parser of the harness session-jsonl format (live transcript tail extraction). |
| `internal/polyforge/supervisor` | The daemon's own supervision on a work machine: the `polyforge-daemon` tmux restart loop shared by `pf daemon ensure` and `pf doctor`. |
| `internal/polyforge/supervise` | The driver↔supervisor contract: facts, verdicts and the per-work-item link (v2-converge §2.2–2.5). |
| `internal/polyforge/workspace` | The `.polyforge.yaml` loader (`steps.<project>.<default\|step-id>.models`, `summarize.models`, `summarize.language`, `summarize.interval_sec`), read and validated by the daemon before every claim and by `pf doctor` (an invalid file refuses the claim without claiming). |
| `internal/polyforge/observe` | Observe lane: the per-work-item events files, per-work-item monitor goroutines fed by the runtime's lifecycle queue, the capsule push loop, live-transcript streaming, session revalidation, and the live activity / progress views the daemon reads. |
| `internal/polyforge/driver` | The Go execution driver: owns `runtime-state/execution.json`, spawns one step process per workflow step and validates each result envelope (needs_input rules; the human gate — a `requires_human_answer: true` step can neither complete nor gate-fail without a human answer in the current attempt, such an envelope is rejected; the command-evidence gate). |
| `internal/polyforge/watcher` | The `pf watch` engine: decision classification (fixed-template wake-up lines) and the resident per-chat watcher with its progress file. |
| `internal/polyforge/runtime` | Runtime lane: the work-item lifecycle manager (one actor per work item: Start/Stop/Recover/Restart), the pi-harness agent spawner, the layered (L1–L5) process cleanup and the recovery engine. |

Binaries: `cmd/aihub` (server binary) and `cmd/pf` (work-machine binary:
CLI verbs + `pf daemon`).

## The design docs — a recommended reading order

The design docs live under
[`docs/design/polyforge-vnext/`](design/polyforge-vnext/). Seven files
are live; the recommended reading order, each file's role in one line:

1. [reference.md](design/polyforge-vnext/reference.md) — the
   current-system reference: components, the work-item state machine,
   session fencing, aihub and daemon routes, error codes, bundle keys,
   candidates, the daemon lifecycle. Start here; it describes the code
   as it exists.
2. [v2-converge.md](design/polyforge-vnext/v2-converge.md) — the
   decision authority: the convergence design whose rulings shaped the
   current system (supervision, watch, the identity model).
3. [wi-kind-contract.md](design/polyforge-vnext/wi-kind-contract.md) —
   the work-item kind contract: `bundle_name` as the execution-ticket
   marker, its request/response fields and landing rules.
4. [01-workflow-skills.md](design/polyforge-vnext/01-workflow-skills.md) —
   bundle authoring: the frontmatter schema and the execution semantics
   (the authority behind the [bundle-authoring
   chapter](bundles.md)).
5. [06-memory-search.md](design/polyforge-vnext/06-memory-search.md) —
   memory: database storage, the FTS index, the one-time content
   migration.
6. [08-capsule-base-migration.md](design/polyforge-vnext/08-capsule-base-migration.md) —
   the manual capsule-base (workspace) migration runbook: the
   shutdown → mapping rewrite → restart sequence when a work machine
   moves.
7. [v0-lessons-ledger.md](design/polyforge-vnext/v0-lessons-ledger.md) —
   the v0/v1 failure ledger: every real-machine failure the early
   system hit, with fixed/open marks. Read it before starting ANY
   implementation or review lane and declare how your change covers the
   entries it touches; after a fix lands, write its status back.

[`archive/`](design/polyforge-vnext/archive/) — the v0/v1 design set —
is **history, not authority**: it describes superseded designs (WI
Agent, owner generations, Revision/CAS sync, the v1/v1.5 specs), and
nothing may be built from it. Where an archived doc and a live one
disagree, the live doc wins; where a live doc and the code disagree,
the code wins.