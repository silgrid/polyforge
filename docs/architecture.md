# Architecture — the system and the design docs' entry page

Polyforge is a work-item orchestration platform with two halves, both
shipped as the one **pf** binary: an **aihub** server (`pf aihub`: the
work-item store, the memory store and the git revision chain behind
them) and the **pf** work machine (the CLI plus `pf daemon` with the Go
execution driver). The split between the
halves is the [data plane vs execution plane
split](concepts.md#data-plane-vs-execution-plane-aihub-vs-daemon) —
the one concept this page assumes; every term a newcomer meets is
defined on the [concepts page](concepts.md).

The aihub server's only HTML face is the **read-only web console** on
`/web/…` — a React single-page app embedded in the server (`pf aihub`)
: work items, memories, machines and project-scoped
search, all behind HTTP Basic authentication (an api key as the
password). Everything else the server answers is the JSON API or the
git proxy. The console's data comes from its private, non-contractual
JSON face on `/web/api/…` — never from `/api/v1` — applying the same
read gates the CLI applies (a key sees exactly its readable projects
and objects; unknown and unreadable answer the same 404). Every `/web/`
response carries
`Content-Security-Policy: default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'`,
`X-Content-Type-Options: nosniff`, `Cache-Control: no-store`,
`Referrer-Policy: no-referrer` and `X-Robots-Tag: noindex`: hashed
same-origin script/style assets only, no inline script or style. The
console answers GET and HEAD only, issues no cookie or session, and a
workflow step or task agent cannot reach it — neither holds an aihub
key, and the daemon proxies no web route. Its look is one token layer
in `webui/src/styles.css` serving both the light and the dark theme from
the operating system's `prefers-color-scheme` (no theme switch, nothing
stored), the system font stack only (no font asset — the policy grants
no font source) and hand-drawn inline SVG icons from one module, so the
console still loads no inline script or style and no external resource.

## Modules

One row per sub-package, exactly as `internal/` exists today — the live
code is the authority; design-doc references are for traceability:

| Package | Role |
| --- | --- |
| `internal/common/api` | Shared business contract between aihub and pf: work items, projects, users, sessions, memories, and the typed error-code registry (`CORE-*`, `MEM-*`, `AUTH-*`, `DAEMON-*`, `GIT-*`). |
| `internal/common/atomicfile` | The one crash-safe "replace this file" primitive every state writer uses. |
| `internal/common/capsule` | Execution-capsule contract shared by pf (runtime creates capsules, the driver writes `execution.json`, the observe lane commits them) and aihub (serves the matching bare repos; git-dir lives outside the capsule). |
| `internal/common/protocol` | The in-machine contracts: Runtime↔Observe messages and the pf CLI↔daemon UDS HTTP API surface. |
| `internal/common/machinesign` | The one cross-machine signature scheme: Ed25519 over `METHOD\nPATH\nsha256hex(body)\nTIMESTAMP\nMACHINE_ID`, the `X-PF-*` headers, the ±300 s window and the anti-replay cache (512 live entries per bucket, 600 s TTL, no global eviction; aihub keys one bucket per machine id per signed-route lane — heartbeat, inbox, answer, query); aihub verifies, the daemon signs. |
| `internal/common/secretpat` | The one secret-shape scanner aihub runs before storing a message (`CORE-422`). |
| `internal/common/harness` | The closed step-harness set (`pi` \| `claude`) and the one model-candidate format `<harness>:<model>`. |
| `internal/common/spawn` | Process-derivation contract shared by the runtime (daemon assembly) and the driver (step-process spawning). |
| `internal/common/workflow` | The workflow contract (docs/reference/workflow-contract.md): the one strict lane loader every reader of a lane calls (`Load`: the contract's key sets and rules, typed `WFC-*` defects with file:line and YAML path, all at once), its registry and pinned golden dump, the definition, placeholder and fragment rules shared with the driver's dispatch, the step-definition lookup (a lane's own `steps/<id>.md` wins, else the shared `workflows/_shared/steps/<id>.md`; `_shared` is not a lane) and the capsule snapshot's content hash. |
| `internal/aihub` | The server assembly behind `pf aihub` (`aihub.Main`): flags, the single-active data-dir lock, the fresh-install bootstrap admin, the one-time memory content migration, the deleted-repo replay and the SIGINT/SIGTERM drain. `cmd/pf/main.go` intercepts `pf aihub` before the CLI dispatch, as it does `pf daemon`; the entry is the service host's (an operator or the container entrypoint runs it) and gives a step agent no path to aihub. |
| `internal/aihub/core` | aihub persistent layer: SQLite store (single writer, WAL), the work-item state machine, claim/session arbitration (one live session per work item, its hashed push secret and public fingerprint), the ready queue and the admin face. |
| `internal/aihub/git` | aihub git surface: per-work-item bare-repo lifecycle, the git-http-backend smart-protocol proxy, session-secret auth (only the work item's live session secret pushes) and the pre-receive hook enforcing the capsule-chain invariants. |
| `internal/aihub/memory` | Core-side memory store: memories kept in the database (`memories.content`), FTS5-trigram full-text search, and the one-time content migration from the former `memory.md` files. |
| `internal/aihub/server` | The AIHub HTTP face: business endpoints, the admin face, the signed cross-machine machine face (inbox, query answer, progress/artifact relay) and the messaging API (`/messages/ack` moves the caller's read cursor, `/messages/delivered` the caller's delivery cursor), the read-only project member face (`GET /projects/{id}/members`: the caller's human co-members as id + display name only, gated by the project read — member or global admin, api-key scope applied; a step agent holds no aihub key and the daemon forwards no such route), the `/git/{wi}.git/...` proxy mount, and the read-only web console on `/web/…` — the server's only HTML face: an embedded React SPA behind HTTP Basic auth with its private JSON face on `/web/api/…` (a step or task agent holds no aihub key and the daemon proxies no `/web/` route, so neither can reach it); Bearer API key auth on everything else, typed `api.Error` envelope. |
| `internal/polyforge/aihubclient` | Shared HTTP client for the aihub server, used by runtime, observe and the CLI verbs; decodes the typed error envelope back into `*api.Error`. |
| `internal/polyforge/cli` | The `pf` client CLI (every AIHub business call goes through the daemon's `ANY /aihub/{path...}` proxy to the resolved Cell — the CLI holds no Hub credential and never dials a Hub; `--server`/`--api-key` are refused, and `PF_SERVER`/`PF_API_KEY` too except on the local repair verbs `config`, `profile`, `doctor`, `init`, `migrate`, `version`, which ignore them with one value-free stderr notice): the routing verbs (file, queue, cancel, recall, remember, memory, `msg` send/list/ack/show/analyze/contacts incl. `list --before`, submit, `watch`, answer, pause, resume, wrap, ops), `chat`, the management face (users, api-keys, projects, machines, backup), the daemon client over the UDS socket, and config resolution — cli.toml incl. the `[summarize]` table and the `[memory_policy."<path>".<entry>]` family, `pf config` (`list` their one read face, `set`/`unset`, `migrate`). |
| `internal/polyforge/cellcfg` | The pure DaemonHost/ProfileCell model: profile-name syntax, the Host/Cell key partition, the per-Cell file naming, the load-order independent conflict rules and the `cells/<profile>` data layout with its `layout-version` gate. |
| `internal/polyforge/daemon` | Assembly of the local work machine as one DaemonHost (`cells.go`): one process and one UDS face serving one or more ProfileCells, each Cell its own runtime, observe lane, queue, tasks, hub client and machine identity; the Host routes every request to exactly one Cell by `X-PF-Profile` (none = the default Cell) and answers the Host-level routes (`GET /profiles`, the `GET /status` aggregate, `POST /ops/config` without a profile, `POST /shutdown`). Per Cell: the UDS HTTP face, runtime, observe lane and driver wired together, the auto switch, the pending-job dispatcher, the auto-pull loop, the driver start (a running `Driver.Run` is the job's agent handle), the data-dir machine key, the cross-machine query/message faces (machine delivery advances a separate delivery cursor and clears no unread) and the shutdown. Step agents cannot reach either the ack or the delivery-cursor entry point (they hold no aihub key). |
| `internal/polyforge/gitops` | Shared capsule-git operations layer used by Runtime (clone at start, dirty-commit at recover) and Observe (async commit+push); owns the per-work-item git mutex. |
| `internal/polyforge/mapping` | The workspace→projects plan read from cli.toml's `[workspace]` sections, and its validated inverse (project → workspace). |
| `internal/polyforge/sessionjsonl` | The single parser of the harness session-jsonl format (live transcript tail extraction). |
| `internal/polyforge/supervisor` | The daemon's own supervision on a work machine: the `polyforge-daemon` tmux restart loop shared by `pf daemon ensure` and `pf doctor`. |
| `internal/polyforge/supervise` | The driver↔supervisor contract: facts, verdicts and the per-work-item link (v2-converge §2.2–2.5). |
| `internal/polyforge/workspace` | The `.polyforge.yaml` loader (`steps.<project>.<default\|step-id>.models`), read and validated by the daemon before every claim and by `pf doctor` (an invalid file refuses the claim without claiming; a retired root `summarize:` / `memory_policy:` section refuses the whole file pointing at `pf config migrate`), plus the summarize lane settings type and the memory-policy merge rule the cli.toml tables feed. |
| `internal/polyforge/observe` | Observe lane: the per-work-item events files, per-work-item monitor goroutines fed by the runtime's lifecycle queue, the capsule push loop, live-transcript streaming, session revalidation, and the live activity / progress views the daemon reads. |
| `internal/polyforge/driver` | The Go execution driver: owns `runtime-state/execution.json` (the daemon's private record), spawns one step process per workflow step with its six-part prompt, and reacts to the status each dispatch hands off through `pf step handoff` (the call ledger `runtime-state/step-calls.jsonl`): the next step, a rewind, a park, the pending hub writes made on `done` (workflow contract 4). |
| `internal/polyforge/watcher` | The `pf watch` engine: the one-shot work-item snapshot render and the fixed decision table the daemon's decision feed classifies with. |
| `internal/polyforge/runtime` | Runtime lane: the work-item lifecycle manager (one actor per work item: Start/Stop/Recover/Restart), the pi-harness agent spawner, the layered (L1–L5) process cleanup and the recovery engine. |

Binary: `cmd/pf`, the one shipped binary — the CLI verbs, `pf daemon`
(the work machine) and `pf aihub` (the server; the release image runs it
as its entrypoint). The same tag is the same build for both roles, but a
deploy of the server and an upgrade of the work machines stay separate
decisions: a tag that changes only the daemon does not require the server
to follow.

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