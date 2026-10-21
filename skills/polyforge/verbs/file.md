# file — create, edit or show a work item; manage its dependencies

Data-plane verb: the `pf` CLI talks HTTP direct to AIHub. The daemon is not
involved.

## Triggers

- EN: file a new work item / create a ticket / new task / edit this work item
  / change the goal or content / update priority or labels / show me this
  work item / this one must wait for X / run it after X / drop that
  dependency / what is it waiting for
- 中文: 建票 / 新建任务 / 提个需求 / 改一下目标 / 编辑这个票 / 更新优先级 /
  看看这个票 / 详情 / 等 X 做完再做 / 加个依赖 / 去掉依赖 / 它在等谁

## Command

```
pf file create --project P --goal G [flags]        # or --stdin for a full JSON body
pf file edit <id> [flags]                           # or --stdin
pf file show <id>
pf file dep add <blocking> <blocked>                # "A blocks B" = pf file dep add A B
pf file dep rm <blocking> <blocked>
pf file dep list <id>
```

`create` flags: `--project` (REQUIRED — there is no default project
anymore, polyforge#113; filing places work on THIS machine, so the value
is validated against the `[workspace]` mapping in cli.toml and a project
with no mapping fails loudly naming it — unlike `pf recall` / `pf
remember`, which are hub-wide and need no mapping),
`--goal` (required, one line ≤500 chars),
`--content` (markdown background; `-` reads stdin, `@file` reads a file),
`--label` (repeatable), `--priority low|normal|high|urgent`, `--milestone`,
`--bundle NAME` (non-empty makes an execution ticket; omit it for a
registry ticket), `--parent ID`, `--attr key=json-value` (repeatable),
`--repo NAME` (repeatable), `--after ID` (repeatable; see Dependencies),
`--stdin`.

`--repo NAME` declares the repos this work item works in: a name of one
of the project's repos (`pf projects get <project>` lists them). The
URL always comes from the project's record, looked up when a daemon
claims the work item. When a work item declares repos, the daemon clones
only those at claim (and at every re-claim); without `--repo` it clones
every repo of the project. The list is frozen at create — it cannot be
edited; cancel and re-create to change it. 1–8 names, no duplicates. A
`--stdin` body may carry the same list as `"repo_inventory": [{"name":
"..."}]` (not together with `--repo`). Only names are accepted: an entry
with `url`, `subdir` or `base` is refused.

`edit` flags (only the flags you pass are sent; `--label` replaces the whole
set): `--goal`, `--content`, `--label`, `--priority`, `--milestone` (an empty
string clears it), `--waiting-on`, `--bundle` (switch the work
item's workflow bundle, e.g. `--bundle release`; an empty string unbinds it).
aihub accepts a bundle change only while the work item is queued and has
never been claimed — once a daemon has claimed it, the bundle is fixed.
There is no `--attr` on `edit`: business attrs are set at create only (aihub
does not accept `attrs` in a PATCH).

A work item's design lives in its content (`pf file edit <wi> --content
@design.md`) while the work item is open — never as a file in the
workspace root or `.polyforge/` (polyforge#177). A terminal work item
refuses the edit (`CORE-410`): store the design as a memory instead —
`pf remember --project P --type doc.design --work-item <wi> @design.md`
(see `remember.md` for reviews, decisions and audits). Scratch output
goes to the workspace's `scratch/` only.

`show` takes exactly one id (or slug) and renders the detail record plus
its dependency block.

## Dependencies

A dependency is a `blocks` edge inside one project: "B runs after A". A
must be an execution ticket (a bundle is bound) — a registration ticket or
an epic runs nothing: it closes only when someone runs `pf wrap` on it once
done (an epic only after its children are terminal), so aihub refuses it
as a blocker. B
stays queued — out of the ready queue, its claim refused — until A is
**wrapped**. A failed or canceled A keeps B waiting; nobody releases it
automatically. When only the order mattered, remove the edge (`pf file
dep rm A B`) and B becomes claimable; there is no `--force`. Wrapped
means A finished, not that A's PR merged: a B released by a wrapped A
whose PR is still open clones the default branch without A's changes.

- `pf file create ... --after A` (repeatable, id or slug of an execution
  ticket) files the work item together with its edges in one step: if any
  `--after` is refused (unknown, another project, not an execution
  ticket) nothing is created — fix the value and file again.
- `pf file dep add A B` / `pf file dep rm A B` add / remove "A blocks B";
  `pf file dep list X` shows both sides of X (adding an edge that already
  exists is harmless) (`after:` = what X waits
  for, `before:` = what waits for X), each with its slug and status.
- Who a queued work item waits for shows as `waiting-for:` in `pf file
  show` and as a WAITING-FOR column in `pf queue` (the server's derived
  `waiting_for`; it is not a status).
- Only `blocks` exists on the CLI.

**When filing, check for overlap first.** Before `pf file create` of an
execution ticket, run `pf queue --project P` and look at the queued,
running and paused work items. Add `--after <slug>` only when the new
work item clearly touches the same work as one of them (the same design
or code area being reshaped, or it needs that one's result). If unsure,
do not add it — ordinary merge conflicts are handled at delivery.

`--project` inference: when the flag is omitted, the CLI resolves the
working directory against the cli.toml `[workspace."<path>"]` mapping — a
cwd inside exactly one mapped workspace that lists exactly ONE project
gets that project as the default and the create proceeds as if the flag
had been passed. A workspace listing two or more projects errors asking
for `--project` (never a silent pick); a cwd matching no mapped workspace
keeps the #113 required-flag error. An explicit `--project` always
bypasses inference byte-for-byte.

## Success

`--json`: the server's record raw. Human mode renders the work item detail
view: `slug (id)`, status, project, goal, milestone, labels, bundle,
assignee, waiting, waiting-for, created (and closed when terminal); `show`
adds the dependency block. `dep add|rm|list` print the dependency view
(`--json`: the server's `{blocking, blocked_by}` raw).

## Failure branches

| Code | When | What to tell the user |
|---|---|---|
| `AUTH-401` | api key missing or invalid | The AIHub credential is rejected — check `--api-key` / `PF_API_KEY` / the config's `api_key`. |
| `CORE-400` | Invalid field or type; a workflow-frontmatter rule failed at create (`detail` names the rule); an `--attr` key hit the execution-state blacklist | The request body is wrong — read `detail` for the exact field/rule and fix the flags. |
| `CORE-400` `PARENT_DEPTH` | `--parent` names a work item that is itself a child — work items nest one level only | Use the top-level work item (the epic) as the parent instead. |
| `CORE-400` `REPO_NOT_IN_PROJECT` | A `--repo` name is not one of the project's repos | Name a repo the project has (`pf projects get <project>` lists them); a maintainer adds new repos to the project. |
| `CORE-400` `INVENTORY_DUPLICATE_NAME` / `INVENTORY_URL_NOT_ALLOWED` / `INVENTORY_SUBDIR_UNSUPPORTED` / `INVENTORY_BASE_UNSUPPORTED` | The same repo named twice; a `--stdin` entry carrying `url`, `subdir` or any key other than `name`; a `--stdin` body carrying the removed `repo_inventory_base` array | Drop the duplicate; give names only. |
| `CORE-400` (dependency) | An edge across two projects, or a work item depending on itself | Dependencies stay inside one project; name a work item of the same project. |
| `CORE-400` `BLOCKER_STILL_BLOCKS` | `pf file edit <id> --bundle ""` (unbind) on a work item that still blocks others (`detail` names them) | Without a bundle it runs nothing, so nothing would release them — remove those edges first (`pf file dep rm`), or rebind to another bundle instead (allowed). |
| `CORE-400` `BLOCKER_NOT_EXECUTABLE` | The blocker (`--after` / the first `dep add` argument) has no bundle: a registration ticket or an epic | It runs nothing (it closes only by a hand `pf wrap`) — name the execution ticket that does the work, or bind a bundle to it first (`pf file edit <id> --bundle NAME`). |
| `CORE-404` | Unknown or invisible work item id, unknown project; `dep rm` of an edge that does not exist | No such work item (or you cannot see it) — check the `wi_` id or slug and the project name. |
| `CORE-409` `DEPENDENCY_CYCLE` | The new edge would close a loop (B already waits, directly or not, for the other side) | Refused — the order you asked for contradicts an existing one; `pf file dep list` shows the chain. |
| `CORE-409` | Create-time conflict (e.g. duplicate) | The state machine refuses this write — read `detail`. |
| `CORE-410` | PATCH of a terminal work item (wrapped/failed/canceled) | This work item is closed and can no longer be edited — file a new one instead. |

A `pf:` line with no typed code means a local flag/argument mistake (exit 1
for a sub-action's flags or arguments, e.g. `file create --bogus` or a
missing `--goal`) — read the message, fix the invocation.
