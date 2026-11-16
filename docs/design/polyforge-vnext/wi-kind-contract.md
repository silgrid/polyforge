# WI kind contract (polyforge#140 split a: the aihub side) <!-- wi-kind-contract -->

> Status: split a's deliverable (2026-10-13). The **interface contract** for split
> b (the pf client: CLI / skills / the daemon's submit prechecks) to integrate
> against; the ruling context is `v2-converge.md#§12`.
> This ticket carries no schema change, no migration, no new table or column;
> **deploy** and **rollback** are §4.
> General constraint: every added surface **is additive only** — old fields are
> never renamed or dropped, and old clients ignore unknown JSON keys by nature.
> Amended by polyforge#77 (2026-11-16): the symmetric parent/child rule is
> deleted — a bundle-bound ticket may be a parent and waits for its children
> to merge (§2, §3); still no schema change, but rollback gains a limit (§4).

## 1. `bundle_name` request and response fields <!-- §1 -->

- **create** (`POST /api/v1/work-items`): the request body carries an optional
  `bundle_name` (a non-empty string). Non-empty = an execution ticket; NULL /
  absent = a pure registry ticket. (The digest chain is deleted, polyforge#149
  B9: a payload carrying a digest field is ignored and never lands in the
  database.)
- **PATCH** (`PATCH /api/v1/work-items/{id}`): `bundle_name` is an updatable
  key inside the R2 window (string or null); `null` = unbind (the ticket
  becomes a registry ticket and no longer appears in any ready-queue segment).
  The digest key is deleted from the updatable-key set (polyforge#149 B9): a
  PATCH carrying it is rejected whole with `CORE-400` (unknown / not
  updatable).
- **Landing rule (create and PATCH share one code path)**: the bundle
  reference is `bundle_name` itself, no digest (no stand-in is derived and no
  explicit pin accepted); `repo_inventory` (frozen at ticket creation,
  `reference.md#§2.1`) is untouched by PATCH.
- **Response surface**: the WI views (GET detail / list / queue entry) carry
  `bundle_name` (an existing field, shape unchanged).

## 2. R2 window semantics (frozen at first claim) <!-- §2 -->

- **The window**: `status='queued'` and **never claimed** (no active session
  row and no `session_claimed` event; after a force-end the session row is
  emptied but the claim event remains, so the freeze holds). Inside the window
  all three PATCH directions are accepted: NULL→X (attach), X→Y (rename),
  X→NULL (unbind).
- A non-empty `waiting_on` does **not** by itself refuse an in-window PATCH
  (whether the ticket shows in the ready queue is a queue-filter question,
  not an immutability question).
- **Outside the window it is refused and nothing is written**:
  - claimed / not queued → `CORE-400`, `reason=IMMUTABLE`
  - terminal (wrapped / failed / canceled) → `CORE-410`,
    `reason=TERMINAL_STATE`
- **Parent/child and bundle (polyforge#77 deleted the w140 symmetric
  rule)**: a bundle-bound ticket may be a parent (create naming it as
  `parent_id` succeeds), and a ticket with children may bind a bundle (a
  PATCH bind inside the window succeeds, any child state). Nesting stays one
  level: a parent that is itself a child → `CORE-400`, `reason=PARENT_DEPTH`.
  An unbound parent (a registry ticket with children) behaves as before (§3).
- **A bundle-bound parent waits for its children to merge**: every child
  that is not wrapped is an implicit blocker of its bound parent, read by
  the same "runs after" relation as the `blocks` edges (the claim gate, the
  ready queue, `waiting_for` and cycle detection share it); only a wrapped
  child releases: a failed or canceled child keeps the parent waiting until
  it is detached; a child added later joins the wait. The claim is refused
  `CORE-409`, `reason=DEPENDENCY_BLOCKED`; the parent appears in no
  ready-queue segment; its `waiting_for` lists each such child with the
  additive key `"via": "child"` (a `blocks` entry carries no `via`; a child
  that is also a `blocks` blocker of its parent is listed once, as the
  child). An unbound parent gains no wait (it runs nothing).
- **Cycles through the wait**: `blocks` cycle detection walks the implicit
  child → bound-parent wait too, so an edge ordering the parent before its
  own child, or closing a loop through another edge — on `dep add` and on
  create's `--after` alike — is refused `CORE-409`,
  `reason=DEPENDENCY_CYCLE`, nothing written.
- **Detaching a child**: `parent_id` is patchable to clear only (`null` or
  `""`); any other value is refused `CORE-400`, `reason=IMMUTABLE` — a child
  is detached, never re-parented. A running child is refused `CORE-409`,
  `reason=CHILD_RUNNING`; a terminal (failed / canceled) child may be
  detached (a PATCH carrying `parent_id` alone). The write records the event
  `wi_detached` with the old `parent_id`; the parent stops waiting for it.
- **The parent's bundle freezes at its first child claim**: once any child
  of a bundle-bound parent has a `session_claimed` event, a PATCH binding,
  renaming or unbinding the parent's bundle is refused `CORE-400`,
  `reason=PARENT_BUNDLE_FROZEN`, nothing written (the children work against
  the parent's lane). An unbound parent is not under this rule.
- **The parent/child reasons at a glance**: `PARENT_DEPTH` (create),
  `DEPENDENCY_BLOCKED` (claim of a waiting bound parent), `DEPENDENCY_CYCLE`
  (an edge or `--after` through the wait), `IMMUTABLE` / `CHILD_RUNNING`
  (detach), `PARENT_BUNDLE_FROZEN` (bundle PATCH), and for an unbound
  parent `OPEN_CHILDREN` (wrap) and `NOT_CLAIMABLE` (claim), §3.
- **Concurrency (two groups; the end state is consistent, the refused side
  gets a typed refusal and nothing is written)**:
  - PATCH bundle vs Claim: no separate arbiter — the single-writer
    transaction serializes and each side's own CAS guarantees a consistent
    end state. A bind or rename PATCH cannot make the Claim fail (an unbind
    X→NULL ordered first leaves the Claim refused as a no-bundle ticket,
    NOT_CLAIMABLE); the PATCH ordered before the Claim succeeds (the Claim
    freezes the new bundle), ordered after it is refused: usually
    `CORE-400 reason=IMMUTABLE` (already claimed), with
    `CORE-409 reason=RACE_LOST` as a defensive fallback (practically
    unreachable under one writer and one connection). Every later bundle
    PATCH is refused (frozen).
  - bind bundle vs add a child: both land (polyforge#77); the
    single-writer transaction orders them, and the bound parent then waits
    for the child.
- **Claiming a no-bundle ticket** stays refused: `CORE-409`,
  `reason=NOT_CLAIMABLE` (the current path unchanged).

## 3. ready queue and parent tickets (response shape) <!-- §3 -->

- `GET /api/v1/projects/{p}/ready-queue` answers `project` / `items` /
  `running` / `paused`. Registry tickets without a bundle (unbound parents
  included) appear in no segment; a bundle-bound parent appears in the
  executable segment only once every child is wrapped (§2).
- Parent tickets carry no derived completion state: aihub never writes a
  terminal state automatically, child progress is read from the children
  themselves. An unbound parent is closed by the owner with `pf wrap` once
  every child is terminal (`POST …/wrap`, available only to tickets without
  a bundle; a non-terminal child still standing → `CORE-409`,
  `reason=OPEN_CHILDREN`), and claiming it stays refused `NOT_CLAIMABLE`. A
  bundle-bound parent runs its own workflow once its children merged and is
  closed by its daemon like any execution ticket.
- Rows that already had a bundle and children before polyforge#77 are not
  migrated; on the new aihub they are ordinary bound parents and start
  waiting for their unwrapped children.

## 4. Deploy and rollback <!-- §4 -->

- **Deploy order: the new aihub before the new pf**. Split a changes the
  aihub side only; the responses are additive, old pf ignores the new JSON
  keys by nature, so deploying aihub first breaks no current client; split
  b's client changes land after.
- **polyforge#77: deploy the new aihub before the new pf**. An older pf
  against the new aihub ignores `via`: it shows a child in WAITING-FOR as a
  plain blocker, and its refused-claim guidance names `pf file dep rm`,
  which removes no child (there is no such edge). A new pf against an older
  aihub cannot detach (`parent_id` is not updatable there, `CORE-400`).
  Before deploying, query the target aihub once, read-only, for rows that
  already have a bundle and children: on the new aihub each starts waiting
  for its unwrapped children.
- **Rollback**: neither ticket carries a schema change, a migration or a
  new table or column, so rollback is still swapping the aihub binary, and
  `wi_detached` events stay as ordinary event rows. Since polyforge#77 it has
  a limit: once a bundle-bound parent with children exists, **the old aihub
  loses the wait** — such a parent becomes claimable (and enters the ready
  queue) before its children merge — and the old rules refuse new writes
  on that data (a child created under a bound parent, a bundle bound to a
  ticket with children, a detach). Before rolling back, list the bound
  parents with unwrapped children; to keep one waiting on the old aihub,
  add an explicit edge from each bundle-bound child to it
  (`pf file dep add <child> <parent>`), which both aihubs honor.
- **Pre-deploy verification (a local throwaway aihub)**: `go test ./...` all
  green (including the w140 acceptance suite
  `test/w140_wi_kind_contract_acceptance_test.go`); no touch of the
  production aihub, no replacement of `/usr/local/bin/pf`, no restart of the
  pf daemon.