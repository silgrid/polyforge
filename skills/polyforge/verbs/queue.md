# queue — list work items / show the ready queue

Data-plane verb: the `pf` CLI talks HTTP direct to AIHub. The daemon is not
involved.

## Triggers

- EN: what's ready today / what can I claim / show the queue / what's in the
  backlog / list work items / filter by status
- 中文: 今天有什么活 / 就绪队列 / 有什么可干的 / 列一下所有票 / 按状态筛

## Command

```
pf queue [--project P] [--status S] [--limit N]
pf queue --ready [--project P]
```

`--project` is OPTIONAL (polyforge#113): without it the verb covers ALL
mapped projects — one merged presentation over the `[workspace]` mapping
in cli.toml, each row's PROJECT column naming its project; `--json`
emits one merged items array. With `--project P` the value is passed to
aihub UNVALIDATED (mapped or not — the mapping governs placement, not
knowledge; exactly one query). Without `--project` AND without any
mapping the verb fails loudly with guidance to add a `[workspace]`
section or pass `--project` — it never silently lists nothing. The
plain form is the list filter (`status`:
queued|running|paused|blocked|wrapped|failed|canceled); `--ready`
switches to the ready queue — the claimable-now projection, ordered
priority-first.

## Success

`--json`: the server body raw. Human mode: `N work item(s)` + a table
(SLUG, PROJECT, STATUS, SESSION, PRIORITY, BUNDLE, GOAL). When a queued row waits for an
unfinished blocker, a WAITING-FOR column (before GOAL) names it (`polyforge#164
(running)`; a failed or canceled blocker keeps it waiting until the edge
is removed — `verbs/file.md`, Dependencies). Waiting rows never appear
under `--ready`.

### Presenting a queue result

The CLI's terminal table truncates the GOAL column (60 runes), so read the
rows with `pf --json queue` (global `--json` flag BEFORE the verb). Build
the default presentation from the full GOAL data, but do not paste the GOAL
in full.

A request for the list itself — "有哪些票 / what work items are there /
list work items / show the queue / 哪些在排队" — defaults to a compact
five-column table: `SLUG | STATUS | PRIORITY | LANE | SUMMARY` (a Chinese
answer may render the headers 状态 / 优先级 / 车道 / 一句话内容). SUMMARY is
a compressed one-line summary of the item's full GOAL; LANE is the item's
bundle (the lane table in `verbs/file.md`).

The complete per-row list remains mandatory: present every row of the
requested scope (the status filter asked for, or all rows). Classification,
analysis and sorted or grouped summaries are layered after the table and
must not replace it; a grouped statistic with no per-row table the asker
can check does not satisfy this default.

Full GOAL text itself is not part of the default. It is a deep read: expand
a particular item when the asker names it and asks for it, or give the raw
`pf --json queue` output when the asker asks for it.

A non-list request keeps the verb's own shape and is not bound by that
default: "现在能领什么" is the `--ready` view, "X 啥情况" is `watch`.

Past this default, how to order or group the rows remains an ordinary
judgment call, not a rule of this skill; the five default columns are
fixed, not a judgment call.

## Failure branches

| Code | When | What to tell the user |
|---|---|---|
| `AUTH-401` | api key missing or invalid | Credential rejected — check `--api-key` / `PF_API_KEY` / config. |
| `CORE-400` | Bad filter value | A filter flag is malformed — read `detail` and fix the flag. |
| `CORE-404` | Unknown project | No such project (or no access to it) — that is aihub's own answer for an explicit `--project`; read-only queries are never locally rejected. |
| (no code) `queue: --project is required — no [workspace] mapping` | No `--project` and no mapping configured | Add a `[workspace."<path>"]` section to cli.toml (`pf doctor` checks the mapping) or pass `--project P`. |
| (no code) `queue: project P: ...` | A per-project fetch failed in merged mode | The verb fails naming the failing project — fake coverage is worse than none; retry or scope with `--project`. |

A `connection refused` one-liner (no typed code) means the AIHub server is
not reachable — check `--server` / `PF_SERVER` and whether AIHub is running.
