# memory — show one stored memory (metadata + body)

Data-plane verb: the `pf` CLI talks HTTP direct to AIHub (the per-id
memory read face). The daemon is not involved. It is the read-the-body
companion of `recall` (search returns metadata per hit; this verb fetches
one memory's full body by id).

## Triggers

- EN: show me that memory / open the full note / read the body of that
  memory / what does that memory say / show the whole stored note
- 中文: 看看记忆正文 / 打开那条记忆 / 读一下那条经验的完整内容

## Command

```
pf memory show <id> [--max-bytes N]
```

The memory id is the only positional argument. There is NO `--project`
flag: ids are globally unique and visibility is enforced server-side — an
id that does not exist or is invisible to the caller answers 404 as the
typed CLI error. `--max-bytes N` caps the human-mode body at N bytes
(default 262144 = 256 KiB; `0` = unlimited). The verb makes two requests:
metadata from the default JSON face (`GET /api/v1/memories/{id}`) and the
body from `?format=content` (the bare body, no front-matter) — the default JSON response carries NO
content, only `?format=content` returns the body.

## Success

Human mode: a metadata header (id, type, title, visibility, updated,
content_len), a blank line, then the body. When the body exceeds the cap
the output ends with exactly:

```
[truncated: showed 262144 of <total> bytes — use --max-bytes 0 for the full body]
```

(the shown count is the effective cap; `<total>` is the full body length
in bytes). Re-run with `--max-bytes 0` for the rest.

`--json`: the server's metadata JSON verbatim plus ONE CLI-added
top-level `"content"` string field carrying the FULL body — never
truncated regardless of `--max-bytes`:

```json
{"id":"mem_1","type":"experience.debug","title":"...","visibility":"project","content_len":36123,"updated_at":"...","content":"the full markdown body"}
```

## Failure branches

| Code | When | What to tell the user |
|---|---|---|
| `AUTH-401` | api key missing or invalid | Credential rejected — check `--api-key` / `PF_API_KEY` / the config's `api_key`. |
| `MEM-404` | Unknown or invisible memory id | No such memory (or it is not visible to you) — the id comes from a `recall` hit (`items[].id`). |

A local `pf: memory show: ...` line (exit 1, no typed code) means a local
refusal before any wire call — a missing id, an unknown action, or an
unknown flag (`memory show` deliberately has no `--project`).

## Notes

- `pf recall` is unchanged: search still returns metadata per hit; take
  the hit's id and run `pf memory show <id>` when the body is needed.
- The byte cap is a human-mode presentation bound only: `--json` always
  carries the full body in `content`.
