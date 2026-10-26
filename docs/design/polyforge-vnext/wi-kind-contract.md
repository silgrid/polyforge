# WI kind contract (polyforge#140 split a: the aihub side) <!-- wi-kind-contract -->

> Status: split a's deliverable (2026-10-13). The **interface contract** for split
> b (the pf client: CLI / skills / the daemon's submit prechecks) to integrate
> against; the ruling context is `v2-converge.md#§12`.
> This ticket carries no schema change, no migration, no new table or column;
> **deploy** and **rollback** are §4.
> General constraint: every added surface **is additive only** — old fields are
> never renamed or dropped, and old clients ignore unknown JSON keys by nature.

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
- **The symmetric parent/child rules**:
  - a ticket with children (any child state, terminal included) → a PATCH
    binding a non-NULL bundle is refused: `CORE-400`,
    `reason=HAS_CHILDREN_NO_BUNDLE` (only PATCH can trigger it: a ticket
    created by create has no children by nature; create's counterpart is the
    next rule's PARENT_NOT_REGISTRY)
  - create naming a bundle-bound ticket as parent is refused: `CORE-400`,
    `reason=PARENT_NOT_REGISTRY` (the original semantics kept, no old-name
    alias)
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
  - bind bundle vs add a child: inside the create transaction the parent's
    bundle is rechecked and the binding guard rechecks the child; the end
    state never shows "has children ∧ bundle non-empty" together.
- **Claiming a no-bundle ticket** stays refused: `CORE-409`,
  `reason=NOT_CLAIMABLE` (the current path unchanged).

## 3. ready queue and parent tickets (response shape) <!-- §3 -->

- `GET /api/v1/projects/{p}/ready-queue` answers `project` / `items` /
  `running` / `paused`. Registry tickets without a bundle (epics included,
  i.e. tickets with children) appear in no segment.
- Parent tickets carry no derived completion state: aihub never writes a
  terminal state automatically, child progress is read from the children
  themselves; once every child is terminal the owner closes the parent with
  `pf wrap` (`POST …/wrap`, available only to tickets without a bundle; a
  non-terminal child still standing → `CORE-409`, `reason=OPEN_CHILDREN`).
- Extant violating rows (bundle non-empty and children present) are not
  migrated and not refused on read; they are shown as literal data. The
  symmetric rules bind new writes only.

## 4. Deploy and rollback <!-- §4 -->

- **Deploy order: the new aihub before the new pf**. Split a changes the
  aihub side only; the responses are additive, old pf ignores the new JSON
  keys by nature, so deploying aihub first breaks no current client; split
  b's client changes land after. Semantic changes such as "a bundle-bound
  ticket is no longer usable as a parent" are expected, not regressions.
- **Rollback**: this ticket carries no schema change, no migration, no new
  table or column — **rollback is just rolling back the aihub binary**. The
  data surface needs no cleanup: bundle changes that happened inside the R2
  window are ordinary UPDATEs, and after rollback the old read-only semantics
  show them as they are.
- **Pre-deploy verification (a local throwaway aihub)**: `go test ./...` all
  green (including the w140 acceptance suite
  `test/w140_wi_kind_contract_acceptance_test.go`); no touch of the
  production aihub, no replacement of `/usr/local/bin/pf`, no restart of the
  pf daemon.