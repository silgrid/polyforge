# cancel — abandon a non-active work item

Data-plane verb: the `pf` CLI talks HTTP direct to AIHub. Cancel only ends a
work item with **no active execution** (queued, paused or blocked); it never interrupts a running
one (that is `pause` or `wrap --fail`). This is also the "abandon" of a
**blocked** work item (the counterpart of `resume` = continue): the two
owner moves on a parked workflow are exactly `pf resume` and this verb —
`pf wrap --fail` on a blocked work item is refused (`DAEMON-409`, naming
those two moves) and changes nothing. Allowed to the task's reporter, a project
maintainer or an admin.

## Triggers

- EN: cancel it / drop it / don't want it anymore / scrap this ticket /
  abandon it / give up on the blocked one
- 中文: 不要了 / 取消这个票 / 作废 / 删掉这个任务 / 放弃

## Command

```
pf cancel <id>
```

The argument is the work item id (`wi_` + random chars) or its slug.

## Success

`cancelled <slug> (status canceled)`; `--json`: the server record raw.

## Failure branches

| Code | When | What to tell the user |
|---|---|---|
| `CORE-404` | Unknown or invisible id | No such work item — check the id. |
| `CORE-409` | The work item is running (an active execution) | It is being executed — use `pause`/`wrap` on the machine that runs it, not cancel. |
| `AUTH-403` | The caller is not the reporter, a project maintainer or an admin | Ask the task owner or a maintainer. |
| `AUTH-401` | api key missing or invalid | Credential rejected — check `--api-key` / `PF_API_KEY` / config. |

Canceling a **paused** work item needs nothing from the machine that paused
it: its daemon notices the cancel at its next session check (within ~30s) and
drops its local paused record by itself — no restart, no `pf ops
end-session`.

A canceled work item is terminal; only a brand-new work item can carry the
work forward.
