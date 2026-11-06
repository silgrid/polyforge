# queue — list work items / show the ready queue

Data-plane verb: the `pf` CLI talks HTTP direct to AIHub. The daemon is not
involved.

## Triggers

- EN: what's ready today / what can I claim / show the queue / what's in the
  backlog / list work items / filter by status, lane or attr
- 中文: 今天有什么活 / 就绪队列 / 有什么可干的 / 列一下所有票 / 按状态、车道或 attr 筛

## Command

```
pf queue [--project P] [--status S] [--bundle L] [--attr <path>=<value>]... [--limit N]
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

`--bundle L` and `--attr <path>=<value>` (repeatable) are aihub's own list
filters passed straight through: `--bundle L` is sent as `?bundle=L` (the
lane's work items only), each `--attr` is split at its first `=` and sent
as `?attr=<path>:<value>` with the rest of the value verbatim (`--attr
x.y=a:b=c` sends `attr=x.y:a:b=c`); every `--attr` must hold (the attr at
the dot path is that string). The CLI adds no default and treats no lane
or path specially; aihub checks the path. They filter the list mode only
(with `--project`, `--status`, `--limit`; without `--project` every mapped
project's query carries them) and are refused with `--ready`. Example:
`pf --json queue --project P --bundle <lane> --attr x.y=z`.

## Success

`--json`: with `--project P`, the server body raw; without it, one merged
document (`items`, `total`). Human mode: `N work item(s)` + a table
(SLUG, PROJECT, STATUS, SESSION, PRIORITY, BUNDLE, GOAL). When a queued row waits for an
unfinished blocker, a WAITING-FOR column (before GOAL) names it (`polyforge#164
(running)`; a failed or canceled blocker keeps it waiting until the edge
is removed — `verbs/file.md`, Dependencies). Waiting rows never appear
under `--ready`.

A queued execution ticket (one with a bundle) whose content lists no
acceptance criterion (under `## Acceptance` or `## 验收` — the check `pf
submit` applies), when its lane requires acceptance (the lane's
`workflow.yaml` key `acceptance`, default `required`; a lane declaring
`acceptance: none` is never marked, and while the daemon does not answer
every lane counts as requiring it), has the STATUS cell `queued (需要交互：缺验收)`: the
daemon's auto-pull skips it, so it stays queued until its acceptance is
settled with the owner and written in (`pf file edit <id> --content
@file`), then it is submitted (`verbs/submit.md`). The merged `--json`
document carries `"acceptance_missing": true` on such an item (absent on
every other); the `--project` `--json` body is the server's and does not.

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
bundle (the lane table in `verbs/file.md`). A row with
`acceptance_missing: true` (set only when its lane requires acceptance) is presented with STATUS 「需要交互：缺验收」
(needs interaction: no acceptance), never as a plain `queued`.

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
| `AUTH-401` | api key missing or invalid | Credential rejected — check the Cell's `api_key` (`pf [--profile NAME] config set api_key`). |
| `CORE-400` | Bad filter value | A filter flag is malformed (an `--attr` path aihub refuses included) — read `detail` and fix the flag. |
| `CORE-404` | Unknown project | No such project (or no access to it) — that is aihub's own answer for an explicit `--project`; read-only queries are never locally rejected. |
| (no code) `queue: --project is required — no [workspace] mapping` | No `--project` and no mapping configured | Add a `[workspace."<path>"]` section to cli.toml (`pf doctor` checks the mapping) or pass `--project P`. |
| (no code) `queue: --attr "…" is not <path>=<value>` | An `--attr` with no `=` or an empty path | Nothing was sent — write it as `<path>=<value>`. |
| (no code) `queue: --bundle/--attr cannot be combined with --ready` | A filter with `--ready` | They filter the list mode only — drop `--ready` or the filter. |
| (no code) `queue: project P: ...` | A per-project fetch failed in merged mode | The verb fails naming the failing project — fake coverage is worse than none; retry or scope with `--project`. |

`DAEMON-503` naming the Cell means that Cell's AIHub is not reachable (the
daemon forwards every business call; other Cells keep serving) — check the
Cell's `server` and whether AIHub is running. `DAEMON-410` means the daemon
itself is down: `pf daemon ensure`, then retry once.

Transport: like every data-plane verb this one goes through the local daemon
to the resolved Cell's AIHub connection (`--profile NAME` / `PF_PROFILE` /
the cwd's mapped Cell / `default`); `--server`, `--api-key`, `PF_SERVER` and
`PF_API_KEY` are refused, and with the daemon down it fails with
`DAEMON-410` (`pf daemon ensure`, then retry once).
