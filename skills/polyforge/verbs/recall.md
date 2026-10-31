# recall — search (or browse) project memories

Data-plane verb: the `pf` CLI talks HTTP direct to AIHub (memory
retrieval). The daemon is not involved. Write paths (creating or updating
memories) are not part of this verb — retrieval only.

## Triggers

- EN: how did we handle X before / any past experience with X / did we hit
  this before / search the project memory / any notes on X
- 中文: 之前怎么处理过 X / 有没有相关经验 / 查一下记忆 / 项目记忆里搜搜

## Command

```
pf recall --project P <query> [--any] [--type T] [--tags a,b] [--limit N]
pf recall --project P [--after CURSOR] [--sort updated|created] [--type T] [--tags a,b] [--limit N]
```

The query is the positional argument (or `--q`); `--project` is REQUIRED
(polyforge#113 — no default project exists) and must be a project of this
machine's cli.toml `[workspace]` mapping (owner ruling 2026-10-16,
superseding the 2026-10-13 C15 "not mapping-restricted" ruling): an
unmapped project — or no mapping at all — is refused locally, naming the
mapped projects, and nothing is sent. aihub then gates memory by project membership on its own: a search
answers only memories of projects the caller can read; `project=` naming an
unknown project or one the caller cannot read answers the same non-leaking
`CORE-404`; reading a memory needs read access to its project (else `MEM-404`);
writing needs the writer (or maintainer) role (else `MEM-403`); admins pass. The routing flow derives
the value from the WI under discussion. `--type` is exact
(`experience.debug`), a prefix (`experience.*` — every type under
`experience.`) or `*` (all, same as omitting it); any other `*` placement
is refused locally. `--tags` is comma-separated and an entry must contain
all of them. `--limit` caps results: default 20, `1..200` (anything else
is refused locally, nothing sent). By default a memory must contain EVERY
query word, so keep queries short (2–4 distinctive words); `--any` switches
to the ranked any-term match (a memory sharing any of the query's words,
best overlap first), for a long or descriptive query. In both modes a
fresher memory ranks ahead of a comparable older one (`rule.*`,
`doc.design` and `doc.decision` never age; reviews and audits do); a
clearly better old match still ranks first. In `--any` mode
stopwords and words under 3 characters are ignored, as are non-CJK words
over 40 characters (hashes, base64). Chinese/Japanese/Korean text has no word
boundaries, so it is matched by overlapping 3-character pieces ("重试阶梯" →
重试阶, 试阶梯). At most 32 terms are used, shared between words and CJK
pieces so neither crowds the other out. Against an aihub too old to know
`--any`, the flag is silently ignored and the search falls back to the
every-word match.

## Inside a workflow step

A step agent holds no aihub credential path. Inside a step (`PF_WI_ID` and
`PF_DAEMON_ID` set) the same `pf recall` (and `pf memory show <id>`) goes to the local
daemon (`GET /step/memories/...`), which searches with its own
credential, scoped to the step's own work item's project: `--project`
must be that project (another one is refused, DAEMON-403), and no
cli.toml mapping is needed. The same holds inside a task agent (`PF_TASK_ID`,
`GET /task/memories/...`). Step and task agents search only: browse mode — an
empty query, `--after` or `--sort` — is refused locally before any daemon
request, and the daemon's memory proxy itself refuses an empty query and never
forwards a cursor or sort.

## Success

`--json`: the server body raw (`{items:[...]}`; each hit carries a 120-rune
`content_excerpt`). Human mode: `N hit(s)` + a table of ID, TYPE, SUMMARY
(the title, else the excerpt's first line), UPDATED, TAGS.

Browse mode: `--json` is the server body raw, `{items, total, next_cursor?}`.
Human mode prints the same table, then `N of TOTAL` and, when more entries
remain, `next: <cursor>` (the value for `--after`).

## Failure branches

| Code | When | What to tell the user |
|---|---|---|
| `AUTH-401` | api key missing or invalid | Credential rejected — check `--api-key` / `PF_API_KEY` / config. |
| `MEM-400` | Browse: an unknown `--sort`, a malformed or other-sort cursor; a query mixed with `--after`/`--sort` | Drop `--after`/`--sort` for a search, or drop the query to browse; pass `next_cursor` unchanged with the same `--sort`. |
| (no code) `recall: a query is required inside a workflow step or task …` | Browse attempted by a step or task agent — local refusal, nothing sent | Give search words: browse is the human direct-aihub path. |
| `CORE-404` | Unknown project, or one you cannot read (same answer, by design) | The project does not exist or you are not a member — check `--project`, or ask a maintainer for membership. |
| (no code) `recall: … mapped here: …` | `--project` is not in this machine's `[workspace]` mapping (or there is no mapping) — local refusal, exit 1, nothing sent | Use one of the listed projects, or add the project to a `[workspace."<path>"]` section of cli.toml (`pf doctor` checks the mapping). |

Zero hits are **not** an error: the table simply says `0 hit(s)`. Try
different wording or drop a tag filter before concluding nothing is stored.
