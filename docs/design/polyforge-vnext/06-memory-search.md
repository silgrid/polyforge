# 06 — Memory / Search: database storage, the FTS index, the one-shot content migration

> Current document (rewritten 2026-10-16, repo review wave 4, to describe only what the code does today). The original described an HTML derivation chain, `backupBarrier`
> and consistent snapshots, `PATCH /memories`, share links, wrap-summary entries, `MEM-501` and more — all of which were **never built or have been deleted**; the original is in git history
> (versions of this file before 2026-10-16). Code authority: `internal/aihub/memory` (`store.go`, `authority.go`, `migrate.go`),
> the memory routes of `internal/aihub/server/server.go`, `internal/polyforge/cli/verbs_aihub.go`, `internal/polyforge/runtime/recall.go`.
> The one-page summary is `reference.md#§9`.

## 1. Scope <!-- §1 -->

Memory is Core-domain data (project knowledge accumulation): one memory = one row of the `memories` table (the `content` column stores the document) + one FTS index row,
all inside the Core database, which is the single source of truth. Writes are only create and redact (soft delete); **there is no update path** (changing content = creating a new one,
redacting the old one if needed). Retrieval is FTS5 trigram text search only; there is no vector / semantic layer.

## 2. Invariants <!-- §2 -->

- Create: one transaction inserts the row (`content` included) + the FTS row (the FTS row is deleted first, then inserted) (`Store.Create`). Redact: one transaction sets
  `status='redacted'` + deletes the FTS row; the document stays in the row and is never served again (`Store.Redact`).
- `content_sha` = the SHA-256 of `content`, `content_len` = its byte count (the `content_len` that `pf memory show` displays).
- At runtime no memory file is written or read. A row's `authority_path` (`memory/<project_id>/<mem_id>`) is used only by the §4 migration to locate old files.
- A row whose `content` is NULL (which can only come from a file not found during migration, §4) is equivalent to nonexistent on every read and write path (`MEM-404`).

## 3. Document format <!-- §3 -->

`content` stores the complete document: YAML frontmatter + the body verbatim. The frontmatter mirrors the `memories` table columns one to one and is serialized in a fixed order
(`authority.go` `frontmatter`): `id`, `type`, `owner_user_id`, `title?`, `tags?`, `visibility`, `work_item_id?`,
`project_id`, `idempotency_key?`, `related?`, `created_at`, `updated_at`. `id` is assigned by the server (`mem_` prefix).
`?format=content` and the search excerpts return the body only (`memory.Body` strips the frontmatter). A row's `title` is fixed at creation
(`memory/title.go` `rowTitle`): a non-blank `title` in the request is used as is, otherwise it is derived from the body — the first non-empty line,
with leading markdown markers removed (`#`, `>`, `-`/`*`/`+`, `[ ]`, `1.`/`1)`), skipping code-fence lines and separator lines, cut at a character boundary to
80 characters (a truncated title ends with `…`). A derived title is written to the row only, never into the frontmatter. `visibility` ∈
`private|project|team|public|admin`. The body limit is 1 MiB (`MEM-413`).

## 4. The one-shot content migration (at startup) <!-- §4 -->

Earlier versions stored documents as `<data-dir>/memory/<project_id>/<mem_id>/memory.md` (after redact, under
`<project_id>/_redacted/<mem_id>/`), and rows had no `content`. When aihub starts, before it begins serving, it runs
`Store.MigrateContent` (`cmd/aihub/main.go`; listen first, serve only after the migration completes, connections wait in the backlog): when the table lacks the
`content` column, `ALTER TABLE ADD COLUMN`; for each row with `content IS NULL`, the file is read by `authority_path` (first the active / `_redacted` location matching the row's status,
then the other one), the file bytes are written into `content` verbatim, committed in small transactions of 16 MiB per batch. File sha equals
`content_sha` → copy; mismatch → the file wins (the old versions' rule), `content_sha`/`content_len` are recomputed, and an active row's
FTS is rewritten; an active row whose file is only under `_redacted/` (redact was interrupted after moving the file, before commit) → the redact is completed; file not found → left NULL,
not served, retried at the next startup. Then the same pass derives titles for active rows with an empty title from their document (the same rule as create: the frontmatter's non-empty
`title`, otherwise the body's first usable line; where none can be derived the title stays empty). Every startup logs one count line, and the rows that were not a plain copy are listed by id. Filled rows are never selected again: on a migrated database
no memory file is read or written. Files are always kept, never deleted. Duration: about 1.5 s for 3,810 entries on a production backup copy.

## 5. API and error codes <!-- §5 -->

### 5.1 API <!-- §5.1 -->

| Method | Path | Permission | Notes |
|---|---|---|---|
| POST | `/api/v1/memories` | project writer/maintainer or admin, else `MEM-403`; key scoped to another project → `AUTH-403`; project absent → `CORE-404` | body `{project, type, title, visibility, content, tags, related, work_item_id, idempotency_key}`; `work_item_id` accepts an id or `<project>#<seq>` and must be in the same project (a readable one of another project → `CORE-400`); an unknown one and one the caller cannot read — a non-member, or a key scoped to another project — get the same missing-work-item `CORE-404`; `type` must be `kind` or `kind.name`, and the kind (before the first `.`) must be one of `authority.go` `typeKinds` (experience/fact/doc/rule/methodology), else `MEM-400` naming the rejected type and the legal kinds (polyforge#210, the sole authority of the type namespace); when `title` is absent or blank the server derives it from the body (§3); replaying the same `idempotency_key` (same owner, same project) returns the original entry, a key belonging to someone else or another project → `MEM-409 IDEMPOTENCY_KEY_REUSED` |
| GET | `/api/v1/memories/search` | any key; results filtered by visibility | **Keyword mode** (`q` non-empty): `q`, `match` (`all` default / `any`, other values → `MEM-400`), `type` (exact value, or an `X.*` prefix matching `X.`), `tags` (comma-separated), `limit` (default 50; ≤0 or >200 becomes 50), `project` (passes the read permission first, then acts as a filter), `excerpt` (when >0 every item carries `content_excerpt`, at most 8192 characters); only `status='active'` is returned. **Browse mode** (`q` absent or empty): the readable active memories as a page, descending — `sort=updated` (default) or `sort=created`, anything else `MEM-400` naming both; the same `type`/`tags`/`project` filters and read gates; `limit` is strict here alone (absent = 20, 1..200 accepted, anything else `MEM-400` — no silent clamp); the answer is `{items, total, next_cursor?}` — `total` is the number of entries the whole walk covers for this caller (same filters, visibility tiers applied, so an unreadable entry is never counted;), not the page length — and carries `next_cursor` whenever more entries remain; `after` (the previous page's `next_cursor`) continues the walk strictly after the last returned entry — a malformed or sort-mismatched cursor is `MEM-400` without a restart, a cursor past the end answers an empty page; a non-empty `q` sent together with `after` and/or `sort` is `MEM-400`, so the keyword answer stays cursor-free |
| GET | `/api/v1/memories/{id}` | read permission; no permission and nonexistent are both `MEM-404` | metadata; `?format=content` returns the raw Markdown body |
| POST | `/api/v1/memories/{id}/redact` | passes the read gate (`memRead`) first: no read permission → `MEM-404`; readable but neither owner nor admin → `MEM-403` | soft delete; not active → `MEM-409` |

Search has two modes, with the single entry point `Store.SearchMode` (`server.go` selects by `match=`; membership / visibility / `type` / `tags` filtering and `limit` are exactly the same in both modes, and `project=` narrows inside the candidate SQL in both modes):

- **`match=all` (default, `Store.Search`)**: every term must hit — terms of ≥3 characters are ANDed together as FTS5 trigram phrases, shorter ones go through substring matching (short Chinese words are common). Suited to short queries of 2–4 keywords.
- **`match=any` (`memory/search_any.go` `anyTerms`)**: the query is lowercased and split into runs of word characters (letters, digits, combining marks `unicode.IsMark`, `_`), and a run is split again wherever it switches between CJK and non-CJK script.
  - A non-CJK run is one term: kept when 3–40 characters long and not in the stopword list (English function words + markdown/URL noise).
  - A CJK run (Han, hiragana, katakana, hangul) is a clause with no word boundaries: it is cut into overlapping 3-character slices (a four-character phrase ABCD yields ABC and BCD); a run shorter than 3 characters yields no term; the 40-character cap does not apply to CJK.
  - After deduplication at most 32 terms are taken: alternating between the word pool (longest first) and the CJK-slice pool (clauses taking turns), and when one pool runs out the other fills the remainder, neither crowding the other out.
  - Each term is quoted and joined with OR, ranked by bm25 × freshness (see below); only the first max(100, 10×limit) candidates in that order are examined, with no further paging (AND mode pages to the end). No usable term after filtering → no hits (not an error). Used for long descriptive queries (the recall at claim time).

Browse (the empty-`q` mode, `memory/browse.go`'s keyset walk) does no ranking at all: one candidate statement narrows by the readable-project set, `status='active'`, content presence, `type` and `tags` (via `json_each`) before the page cut — the same constant-per-page shape as the keyword modes (no per-candidate membership query; the visibility tiers filter in the application layer) — and orders by `julianday(<sort column>) DESC, id DESC`. The answer's `total` comes from one more statement over the same narrowing (no cursor clause), each row through the same visibility predicate (`Store.BrowseTotal`): a fixed statement count, but O(matching rows) work per page (owner ruling 2026-10-29, — memory volumes are in the hundreds); the console's `/web/api/memories` shows no total and does not run it. The page is cut at `limit` plus one probe row: the probe proves whether a next page exists, and the returned cursor positions the next page strictly after the last returned entry, so an entry updated between pages neither duplicates nor skips. The same store browse also backs the web console's memories module ('s `/web/api/memories`: 20-row pages, type and tags filters, the same cursor); the console's keyword page asks the relevance search for one page of the top 50 with no cursor at all.

The ranking of both modes is the same (`memory/freshness.go`): `w = exp(-age_days / stability_days)`, with `age_days` computed from
`memories.updated_at` (there is no update endpoint, so it equals `created_at`) to the query time;
`score = rank × (1 + 0.5 × w)` (rank is the FTS5 bm25, more negative is better), ascending, ties broken by `updated_at` descending (compared as parsed instants — whole-second and fractional-second RFC3339 coexist; unparseable counts as oldest), then by id.
Stability: `rule.*`, `doc.design`, `doc.decision` do not decay (w = 1); `doc.review`, `doc.audit` and other `doc.*`
(point-in-time snapshots) 180 days; `experience.*` 90 days; `fact.*` and `methodology.*` 180 days; other types 90 days. An unparseable `updated_at`
counts as fully stale (w = 0), a future time as age 0 (w ≤ 1). Freshness amplifies relevance by at most 1.5×: among close hits the newer comes first, and an older entry whose relevance is more than 1.5× higher always ranks ahead of a
weaker newer one. Only the substring scan of short terms (no rank) sorts by w descending. In the implementation (`ftsPage`) one ranked statement — the FTS MATCH joined to the `memories` row — answers every (project-narrowed) hit's rowid, rank, id, type and `updated_at`; the score, ordering and the page cut happen in Go, and a second statement reads the bodies of the page alone. Exactly two statements per page, whatever the hits' ranks look like; the result equals an exact ranking over all hits.

### 5.2 Error codes <!-- §5.2 -->

`MEM-400` invalid input (missing `type`/`content`, `type` outside the namespace, visibility out of range, invalid frontmatter; on the browse path: an unknown `sort`, a `limit` outside 1..200, a malformed or sort-mismatched cursor, or a non-empty `q` mixed with the browse paging parameters); `MEM-403` no write permission / redact by a reader who is not the owner;
`MEM-404` nonexistent or no read permission (including no read permission at redact); `MEM-409` idempotency-key reuse, redact of a non-active entry; `MEM-413` body over 1 MiB;
`MEM-503` FTS query/scan failure; a database transaction failure → `CORE-500` (an unclassified server error) (constants in `internal/common/api/errors.go` `ErrMem*`). Also: key scoped to another project → `AUTH-403`;
project absent at create → `CORE-404`.

## 6. Visibility <!-- §6 -->

Filtered by project membership first, then by visibility (`server.go` `memAllowed`): `admin` goes to admins only; `private` to the owner
and admins only; `project`/`team`/`public` to any member with read permission on the project (the three currently behave the same) and to admins.

## 7. Backup <!-- §7 -->

`pf backup` → `POST /api/v1/admin/backup` (admin) runs `VACUUM INTO <data-dir>/backups/aihub-<ts>.db` on SQLite
(`core/admin.go`): memory lives entirely in the database, so the snapshot contains all memory (redacted documents included); it does **not** contain the git repositories (`git/`),
which sit on the same disk. The full production backup is done by the online backup of `.polyforge/deploy.sh` (`docs/upgrade.md`).

## 8. CLI and the claim-time snapshot <!-- §8 -->

- `pf recall --project P <query> [--any] [--type T|X.*|*] [--tags] [--limit 1..200, default 20]`: `--project` is required and must be in this machine's
  `[workspace]` mapping; it calls search with `excerpt=120`, default `match=all`, and `--any` switches to `match=any`. `pf remember --project P --type T [--title] [--visibility project]
  [--tags] [--work-item] <content|-|@file>`. `pf memory show <id> [--max-bytes 262144] [--json]`.
  (`cli/verbs_aihub.go`; fragments: `skills/polyforge/verbs/{recall,remember,memory}.md`.)
- At claim the daemon takes the WI's goal + labels as the query (no content), uses `match=any` restricted to the WI's project, and writes the first 10 hits (`-recall-topk`, cap 50) into the
  capsule's `runtime-state/recalled-memories.json` (`{work_item_id, query, top_k, items[{id,type,title,excerpt}], notice,
  created_at}`, excerpts of 1200 characters); on error or no hit it writes an empty list + notice and never fails the claim (`runtime/recall.go`,
  `daemon/memface.go`). The driver injects it into every step's prompt as "non-binding history" (`v2-converge.md#§1`) — each entry renders as one line
  `[type] title` only (no id, no body; when the title is empty the first non-empty line of the excerpt is used), id-only noise titles are dropped
  (`^Verification Report — \S+$`, `^Controller-sink artifact`), at most 10 lines, and with no entries the section is omitted (`driver/spawn.go`).
  Deploy order: upgrade aihub first, then the daemon — an old aihub ignores `match=` and searches with AND, so a long query then has no hit and the snapshot is empty.
- A step agent holds no aihub credential path; all memory access is proxied by the daemon (owner ruling option C, 2026-10-17 simplification). The step process carries the
  `PF_WI_ID` / `PF_DAEMON_ID` markers (the same pair the step-origin guard and the reaper use), plus `PF_DAEMON_SOCKET` (this daemon's socket); the other `PF_*` / `POLYFORGE_*` variables are stripped.
  When `PF_WI_ID` and `PF_DAEMON_ID` are both present `pf` enters step mode (`cli/stepctx.go`): `pf recall` / `pf memory show` go through
  `GET /step/memories/search`, `GET /step/memories/{id}`, and `pf remember` synchronously goes through `POST /step/memories`
  (`daemon/stepmem.go`; the only step write request the step-origin guard lets through). The work item is taken neither from the request nor from the caller's environment variables:
  it is found along the UDS peer pid's ancestor chain (process-group leader included) as a step process this daemon spawned (the pid of any entry in an active job's spawn journal,
  and the process still carrying that job's markers; the latest matching entry gives the step and attempt); exactly one job must match, and it must equal the caller's nearest
  `PF_WI_ID`, otherwise it is refused. The project is taken from that job (another project in the request → DAEMON-403, an id outside the project is always MEM-404). A write accepts only
  `experience.wrap`, requires a title, a body ≤2KB, no suspected credential value (`internal/common/stepmem`, the CLI performs the same check first),
  `work_item_id` / `project` are taken from the job, and it is created with aihub's existing idempotency key `pf-step-memory:<wi>:<step>:<attempt>`
  — a retry after a lost reply returns the same memory; aihub unreachable answers DAEMON-503, the CLI reports a clear error, and the prompt says a failure does not fail the step. Every other verb that talks to aihub directly, `pf doctor`, and `pf daemon` are refused inside a step, the aihub client of that call refuses every request,
  and the `api_key` of `cli.toml` is not used; `cli.toml` is still readable under the shared HOME — this guards against misuse, not against malice.
- A step whose workflow entry declares `recall: true` — the decision/authoring steps (grill, spec, plan, repro, root-cause, e2e-author, code, fix); never the review/verification/deliver gates or the release steps carries the shared recall section (`workflows/_shared/memory.md`): before starting, run `pf recall --any` with 2–3 different keyword sets (including Chinese and English variants)
  and read the bodies of the relevant entries. Only the workflow's last step writes memory (`workflows/_shared/memory-wrap.md`, variables
  `{{record}}` `{{skip}}` are the effective memory policy): exactly one `experience.wrap` per WI (title `<wi slug>: <做了什么 (what was done)>`,
  body: 做了什么 (what was done), 关键决定 (the key decisions), 遗留 (follow-ups) and lessons that fit record; a valuable one not in record is written anyway, with `建议新增条目：` (suggested new entry:) followed by the proposed entry, and tagged
  `policy-proposal`).
- Memory policy: the optional `memory_policy:` section of `.polyforge.yaml` (`default` / `<project>`: `{record: [...], skip: [...]}`;
  a list `default` does not declare takes the built-in default, and a project's list is appended after `default`, deduplicated). At claim the daemon writes the project's effective policy into the
  capsule's `runtime-state/memory-policy.json` (`runtime/memorypolicy.go`), and the driver renders it into the last step's wrap-duty section; a missing section = the built-in default
  (`workspace.DefaultMemoryPolicySection`) and never refuses the claim; a malformed section refuses the claim just as an invalid file as a whole does (DAEMON-414).
  `pf init` appends the default section as text to the end of the file when the section is missing (the original bytes are not rewritten); `pf doctor` is read-only: no file → pass (built-in default),
  file lacking the section → warn and suggest `pf init`, section present → pass.
