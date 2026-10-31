# followup — the follow-up ledger and its lifecycle

Session procedure, not a `pf` verb: it composes `pf recall`, `pf remember`,
`pf memory show` and `pf file` (all data-plane, direct to AIHub). It is the
one source of the follow-up rules; `verbs/watch.md` and `verbs/wrap.md` only
point here. Only the interactive session runs it: step agents run with skills
disabled and never write ledger records — inside a step `pf remember` is
daemon-mediated and accepts only `experience.wrap` (`verbs/remember.md`).

A follow-up is an item a delivered work item leaves for later (its PR body's
follow-up section, or its capsule's `artifacts/delivery.md`). The ledger keeps
every such item durable and searchable across work items; it lives in AIHub
memories, never in the workspace's `scratch/` (disposable, pruned after 14
days).

## Triggers

- EN: show all unclaimed follow-ups / any follow-ups left / what did that
  delivery leave behind / handle (claim) follow-up X / file a ticket for that
  follow-up / drop (dismiss) follow-up X, we won't fix it
- 中文: 全部未认领 follow-up / 有没有遗留 / 那张票留了什么尾巴 / 处理这条 /
  认领 X / 给这条建票 / 不修这个 / 这条不要了
- Implicit: a completed work item presented by `pf watch` or after a wrap
  (`verbs/watch.md`, `verbs/wrap.md`) runs Ingest, then Present.

## The ledger

One ledger memory **per source delivery** (the delivered work item):

```
pf remember --project P --type doc.track --visibility project --tags followup --work-item '<project>#<seq>' --title 'follow-up ledger: <project>#<seq>' @ledger.md
```

Body (`@ledger.md`): one `source:` line, then one row per item:

```
source: <project>#<seq> · <PR URL or "no PR"> · <date>

| fp | item | severity | state | claim | note |
|---|---|---|---|---|---|
| <fingerprint> | <item text as the source states it> | <source marker or unrated> | unclaimed | | |
```

- `state` is one of `unclaimed`, `claimed`, `dismissed`; `claim` is the
  claimed ticket's slug (the back-link); `note` is the dismissal reason or the
  dedupe pointer.
- `severity` is the source's own marker when the item carries one (e.g.
  `blocking`, `non-blocking`, `P1`); `unrated` when it carries none. Never
  invent a rating.
- `fp` is the dedupe fingerprint, a derived key and never a judgment. It is
  computed by these steps, in this order (the order is normative):
  1. Take the item text as the source states it.
  2. Drop a leading list marker (`-`, `*`, `+`, or a number followed by `.`
     or `)`).
  3. Lowercase the whole text.
  4. While the first word is an `f<N>` label (`f` and digits, trailing
     punctuation allowed, e.g. `f2:`) or a `<project>#<seq>` reference, drop
     that word. Lowercasing first means `F<N>` and `f<N>` are the same label.
  5. Drop every character that is not a letter, a digit or whitespace.
  6. Split on whitespace (collapsing runs of it).
  7. Join the first 8 words with `-`.
- A memory cannot be edited, so a state change **appends a superseding
  record**: the same title, the whole body re-stored with the changed row. For
  one title the record with the newest `updated_at` is the ledger; older ones
  are history. Writing the same state twice leaves the same newest state.
- A write failure (`MEM-403` without the writer role, AIHub down, any error):
  say so and ask the human what to do. Never fall back silently to a
  `scratch/` file, and never present the list as persisted when it is not.

## Ingest

1. Read the delivered source: the work item's PR body (its follow-up section)
   or its capsule's `artifacts/delivery.md`. If the work item has no PR and no
   delivery note, or neither source is reachable, say so and ask the human
   where to read it; never assume the list is empty.
2. If the source carries no follow-up list, say "no follow-ups" and stop —
   nothing is written.
3. For each item compute `fp` and run Dedupe (below) before writing.
4. Store the ledger record: every new item as an `unclaimed` row with its
   severity; every duplicate as a pointer row. If a ledger with this title
   already exists, store a superseding record that keeps the existing rows'
   states (ingest never resets a claim or a dismissal). A re-ingest that adds
   no new row writes no record at all.

## Present

1. Browse the tag-narrowed set — `pf recall --project P --type doc.track --tags followup --sort updated --limit 200` —
   and follow `next:` with `--after <cursor>` (same `--sort`) until no cursor
   remains. Recall's ranking does not matter here: the whole set is read.
2. Per title keep only the record with the newest `updated_at`; read its body
   with `pf memory show <id>`.
3. Take the owning rows (rows with a state); skip pointer rows (their item is
   shown once, at its owning row, listing the pointer's source as "also in").
4. Drop the rows whose state is `claimed` or `dismissed`.
5. Present each remaining row: item, source work item, severity, `fp`. For a
   single delivery (a completed work item), present only that source's rows.

Present only, and leave deciding, claiming, filing, routing or dismissing any
follow-up to the human. Never mark an item claimed or dismissed on your own.

## Claim

Only when the owner names the item ("handle X" / "处理这条") — never
automatically.

1. Check first that the item has no ticket yet. If the owning row's `claim`
   already holds a slug, the item is claimed: tell the owner the slug and
   file nothing. A session interrupted after filing and before storing the
   claim (the crash window between steps 2 and 3) leaves the row `unclaimed`
   with a ticket already filed: look for it (`pf queue --project P`, a goal
   naming this item) and ask the owner when unsure. When it exists, never a
   second ticket: skip step 2 and store the claim with that slug in step 3.
2. File the ticket the owner asked for through `verbs/file.md` (`pf file create
   --project P --goal G --bundle <lane>`; ask the lane if the owner named
   none). Nothing is written to the ledger before the ticket exists.
3. Store a superseding record of the owning ledger (the record that holds the
   row's state, never a pointer record) with the row at `state=claimed`,
   `claim=<new slug>`. The slug is the back-link; tell the owner both.
4. A re-present no longer lists the item.

## Dismiss

Only when the owner names the item ("we won't fix X" / "不修这个").

1. Ask once for the reason if the owner gave none; if the owner declines,
   the reason is `no reason given`.
2. Store a superseding record of the owning ledger with the row at
   `state=dismissed`, `note=<the owner's reason, verbatim>`.
3. A re-present no longer lists the item; the reason stays recallable.

## Dedupe

The same follow-up often reappears in later deliveries. Fingerprints decide,
never fuzzy prose matching:

1. Before writing a row, recall the fingerprint words:
   `pf recall --project P --type doc.track --tags followup --any '<fp words>'`.
2. Read each hit's newest record for its title; a row with the same `fp`
   **owns** the item. An `fp` already recorded in the ledger is existing,
   not new — the hit owns the item and the delivery adds no second item.
   A hit whose title is the source delivery's own
   (`follow-up ledger: <the very source>`) is not a dedupe hit: re-ingesting
   a delivery never reads its own rows as duplicates and never writes a
   pointer row to itself. Its existing rows stay as they are, and when
   nothing new remains, nothing is stored.
3. The new delivery's record then carries only a pointer row: no state, and
   `note: dedupe→ <memory id of the owning ledger>`. The owning ledger's
   title is resolved from that id; its newest record holds the state.
4. State lives only at the owning (first) record: claims and dismissals are
   always appended there, never to a pointer record.

## The iron law

Never auto-file, never auto-claim, never auto-dismiss. Claiming and
dismissing happen only on the owner's naming. A ledger memory is not a
ticket: a memory records what a delivery left behind; a ticket queues work,
and only the owner decides that a follow-up becomes work.

## Worked example

Fixtures, not real data (project `demo`, memory ids `mem_FIXTURE*`):

- **Ingest.** Delivery `demo#41` (PR body follow-up section) lists 3 items:
  `F1 retry budget is not logged` (non-blocking), `F2 doc table misses the
  new flag` (no marker), `F3 flaky e2e on slow hosts` (blocking). Ingest
  stores `mem_FIXTURE1`, title `follow-up ledger: demo#41`, `source: demo#41`,
  3 rows, each `unclaimed`, severities `non-blocking`, `unrated`, `blocking`.
- **Fingerprint.** By the ordered steps: `F1 retry budget is not logged` →
  `retry-budget-is-not-logged`; `F3 flaky e2e on slow hosts` →
  `flaky-e2e-on-slow-hosts`; `- demo#42 Retry budget, is NOT logged!` →
  `retry-budget-is-not-logged`; `1. f2: doc table misses the new --flag (see
  the generated table)` → `doc-table-misses-the-new-flag-see-the`.
- **Present** shows 3 items, each with source `demo#41`.
- **Claim.** The owner says "handle F3". The session files `demo#43`, then
  stores `mem_FIXTURE2` (same title) with F3 at `state=claimed`,
  `claim=demo#43`. Present now shows 2 items (F1, F2).
- **Dismiss.** The owner says "drop F2, the table is generated". The session
  stores `mem_FIXTURE3` with F2 at `state=dismissed`, `note=the table is
  generated`. Present now shows 1 item (F1).
- **Re-ingest.** Delivery `demo#41` is ingested again. Every dedupe hit is
  its own title `follow-up ledger: demo#41`, so each is skipped as
  self-title: no new row, no pointer row, nothing stored; F3 stays
  `claimed` and F2 `dismissed`.
- **Dedupe.** Delivery `demo#42` lists `retry budget is not logged` again.
  Its `fp` (`retry-budget-is-not-logged`) matches F1's, so the record
  `follow-up ledger: demo#42` carries a pointer row `dedupe→ mem_FIXTURE1`
  and no state; present still shows F1 once (source `demo#41`, also in
  `demo#42`), never a second row.
