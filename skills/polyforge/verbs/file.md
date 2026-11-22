# file — create, edit, comment on or show a work item; manage its dependencies

Data-plane verb: the `pf` CLI talks HTTP direct to AIHub. The daemon is not
involved.

## Triggers

- EN: file a new work item / create a ticket / new task / edit this work item
  / change the goal or content / update priority or labels / add a comment /
  comment on this ticket / put a note on this ticket / reply to that comment /
  show me this work item / this one must wait for X / run it after X / drop that
  dependency / what is it waiting for
- 中文: 建票 / 新建任务 / 提个需求 / 改一下目标 / 编辑这个票 / 更新优先级 /
  在票上补一条评论 / 评论这张票 / 回复那条评论 / 给这张票加个说明 /
  看看这个票 / 详情 / 等 X 做完再做 / 加个依赖 / 去掉依赖 / 它在等谁

## Command

```
pf file create --project P --goal G [flags]        # or --stdin for a full JSON body
pf file edit <id> [flags]                           # or --stdin
pf file comment <id> --body TEXT [--reply-to COMMENT]
pf file show <id>
pf file dep add <blocking> <blocked>                # "A blocks B" = pf file dep add A B
pf file dep rm <blocking> <blocked>
pf file dep list <id>
```

`create` flags: `--project` (REQUIRED — there is no default project
anymore, polyforge#113; filing places work through the Cell this
invocation routes to, so the value is validated against that Cell's
`[workspace]` mapping — cli.toml for the default Cell,
`cli-<profile>.toml` for a named one, polyforge#17 — and a project with
no mapping there fails loudly naming the Cell's mapped projects, as
`pf recall` / `pf remember` do too, owner ruling 2026-10-16),
`--goal` (required, one line ≤500 chars),
`--content` (markdown background; `-` reads stdin, `@file` reads a file),
`--label` (repeatable), `--priority low|normal|high|urgent`, `--milestone`,
`--bundle NAME` (non-empty makes an execution ticket; omit it for a
registry ticket), `--parent ID`, `--attr <path>=json-value` (repeatable;
a dotted path nests: `split.origin=X` stores `{"split":{"origin":"X"}}` —
the same dot path `pf queue --attr` filters on; colliding paths or an
empty segment refuse the whole create),
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

### Choosing the project

The project follows the work, not the repo — and which project a given
work files to is not this skill's to hardcode: the workspace's or the
repository's AGENTS.md states the routing rule; read it and follow it.
The skill routes by intent only (`SKILL.md` §Project routing).

### Choosing the lane (`--bundle`)

| the work | lane | verification tier |
|---|---|---|
| **the default** for development work — a feature, a fix, maintenance, a small change (`build` → `review` → `verification` → `publish`: one build session plans against the ticket's `## Acceptance`, writes each unit's test red before its implementation and reviews itself; `review` blocks only on its five classes) | `develop` | `light` — the full bar on an integration batch's fix ticket (the integration lane's full gate runs over what landed) |
| a pure text change — docs or locale message text — inside the paths every touched repository declares with `micro_path=` in its own `.polyforge/verify-scope.conf`; the ticket body is the spec (`fix` → `review` → `sync` → `evidence` → `publish` → `merge`) | `micro` | none (no verification step: its `evidence` step re-runs fix's evidence plan — the repository's fast evidence — with `run.py`, on the host) |
| gate what landed: one integration batch over a project's landed work items (`collect` → `repair` → `review` → `revert` → `full-gate` → `close`; the middle two `rewind_only`, the batch's own repair of a red fixable by tests or documentation alone and its review), filed by a person and run with `pf submit`; the lane is `exclusive: true`, so a second unfinished batch of the project is refused `CORE-409`, naming the open one | `integrate` | `full` (its `full-gate` runs the full gate on the pinned default branches) |
| cut a release / deploy a published version | `release` | `full` (no verification step) |
| no execution at all — a design, decision or tracking ticket | omit `--bundle`: a registration ticket runs nothing (it is not claimable, `CORE-409`) | not applicable |

The lane's step table is the `steps:` list in its `workflows/<lane>/workflow.yaml`:
every acceptance criterion of an execution ticket must name the
step of that table that carries it. The four lanes `develop` superseded
are removed (`workflows/_archive/README.md`): a ticket
still bound to one is refused at claim, and a terminal one still displays.

**On `develop`** the ticket's `content` carries a `## Acceptance` section: one list
item per criterion, its id the item's leading `AC<n>` token, else `AC<n>` by
position. `build` pins each criterion with a test (or names the existing test or
doc validator) and `review` judges that coverage criterion by criterion (`## 验收`
counts the same). Filing does not grill: an idea may be filed as-is, and a ticket
without the section is legal to file; `pf submit` refuses it without
`--allow-no-acceptance` — the session grills its acceptance first
(`verbs/submit.md`, the acceptance check). Submitted anyway, its build stops for
the owner's list — the judged list is never self-written — asked once together
with the plan; a content line `confirm_plan: true` stops the build after the
plan as well. `build` does not grill the ticket (that is this session's job,
before `pf submit`): it asks the owner (its own `pf step ask`) only on a surprise,
right after the plan and never at completion — the code reveals something the
ticket did not decide (a dead end), or the plan diverges from the ticket — each
question with a recommended answer. The stops are human-only — never answer
them for the owner, who answers in free text the build reads — and review
blocks a build that took a decision the owner never gave. `develop`'s other
human stops: a defect it cannot reproduce (a dead end), rewind exhaustion, and
publish's owner-review.

Pick the lane with the owner. Every filing proposal fills the two mandatory
slots below **before** `pf file create` — they are slots the agent answers, not
prose it may skip:

- **Lane + reason** — the chosen `--bundle` and one line why; the owner may veto
  or re-route. New development work defaults to `develop`. Every delivery lane
  can merge unattended, so work on `docs/concepts.md`'s "never
  unattended" list is filed `develop` with the content line `confirm_plan: true`
  (its build stops for the owner after the plan, before its first test, and
  review blocks a first test commit older than the owner's reply). The
  selection principles and the never-hand-execute discipline are in
  `verbs/execute-intent.md`; custom bundles and their authoring are the
  authority of `docs/bundles.md` (do not restate it).
- **Acceptance carrier** — every acceptance criterion is written
  `<criterion> (proof: <command>, run in <dir>) — carried by <step id>`:
  its proof — the command that shows the criterion holds and the
  directory that command runs in — rides before the carrier, which stays
  last; the step id is taken from the chosen
  lane's step table (the table is the authority; do not copy a step list into
  the proposal from memory). A criterion that names no step either drops to
  the evidence that graph actually runs, recorded in the `content` as an
  explicit waiver proposed to the owner, or the ticket moves to a lane whose
  step table carries it. On `micro` the table is `fix`, `review`, `sync`,
  `evidence`, `publish`, `merge`:
  a criterion only a full bar, an e2e scenario or a verification step carries
  makes the ticket ineligible for `micro` — file it on a lane that carries it.
  A registration ticket (no bundle) has no step table and skips this slot.

Any read of `<workspace>/.repo/<name>` behind these slots (manifests, conf, code) happens
after the session clone sync: SKILL.md session rule 8.

**Down-lane check.** With `develop` the default, the check routes between
`develop` and `micro`. Judge the ticket on two axes
before you settle the lane,
and name the result in the `Lane + reason` slot above:
`expected diff size` (how much a competent implementation touches — one file
or a handful of lines in one module, versus many files across modules) and
`lane risk surface` (what the change can break or newly expose — a new
user-visible behavior or behavior face, a cross-module change, a gate-type
change, or the project's core runtime parts — whatever components keep the
product itself running; each project names its own, and the check never
names a specific project's components). A pure text ticket — docs or locale
message text and nothing else: no code, config, generated file, test or
behavior change — whose every touched repository declares `micro_path=` covering
the paths it changes (read each repository's `.polyforge/verify-scope.conf` on
its default branch) may go down to `micro`; a ticket touching any
repository without that declaration, or any path outside it, is never proposed
`micro` (its fix would find the change out of bounds and ask the owner to re-file it on `develop`).
Work on `docs/concepts.md`'s "never
unattended" list counts as a high `lane risk surface` by definition and is
**never down-laned** off a lane that always keeps a human in the loop —
`develop` carrying `confirm_plan: true` (build asks before its first test, and
review blocks a build that did not) —
whatever its `expected diff size`: that list's only
safeguard is the filer picking such a lane, so the check never proposes a
lighter lane for it — `micro` included — nor drops that `confirm_plan: true`
line. The
check also never down-lanes onto a lane whose step table carries no step for
the acceptance criteria: a carrier-less lane is not a cheaper lane, it is the
wrong one (see the `Acceptance carrier` slot). When the
two axes disagree, or the estimate is genuinely unclear, fail closed to the
heavier lane. The check only
routes between the lanes the table above defines — the lane definitions and the
selection authority stay in `docs/bundles.md`; it adds no lane.

### Acceptance at creation

Filing any ticket, write its `## Acceptance` at creation only when the
requirement is **very clear**: what to do, its scope and when it is done are all
settled, and there is nothing the owner must decide — when unsure, it is not
clear. Then each item is a verifiable criterion written in the `Acceptance
 carrier` slot's shape (above): its proof — the command that shows the
criterion holds and the directory that command runs in — before the carrier,
for example `- AC1 the timeout is gone (proof: <the command that shows it>,
run in <its directory>) — carried by build`, so the ticket passes the
submit-time acceptance check. It is submitted once it passes both the
acceptance and the analysis checks: the pre-submit analysis (below,
`verbs/submit.md`) still has to be in the ticket — or the owner's dated
「免分析」 — before `pf submit` takes it.
Otherwise file it as usual without the section and write no guessed criteria:
the submit-time acceptance check (`verbs/submit.md`) brings the interaction that
settles them. Claiming a follow-up item (`verbs/followup.md`, Claim) whose
completion standard can be settled from the wrap's evidence is this very-clear
case.

### Pre-submit analysis

An execution ticket filed on a lane that requires acceptance also carries a
pre-submit analysis before it is submitted. When `pf file create` files such
a ticket with no `## 分析` section, the daemon schedules the analysis at
once — an analysis task that writes its draft into the ticket's
`## 分析` section itself (`pf task analysis-write`); the create's output
names the task, says it is queued, or says 未安排分析 (no daemon reachable —
fall back to a session errand). A ticket a workflow step files
(`pf step hub file`) gets no automatic analysis. The draft stays unconfirmed until the owner
runs `pf file analysis confirm <id>`; the owner may instead explicitly waive
the analysis with one dated line 「免分析」. A queued or blocked execution
ticket's analysis in the old format, written against a stale base, or after
a materially changed scope is re-scheduled with `pf file analysis redo <id>`:
the daemon keeps the old `## 分析` section verbatim in `## 历次分析`, schedules
the same analysis prompt a create uses, and the new draft stays unconfirmed
(auto-pull skips it) until `pf file analysis confirm`. A claimed, running or
terminal ticket, or one with a local execution job, a running analysis task
or a pending analysis request, is refused with the reason; the strong
guarantee is this machine's daemon's. The flow, the 7-item report
format, the boundary against grilling and the pre-submit confirmation rule
are defined once in `verbs/submit.md` (the analysis check) — this fragment
adds nothing to them.

### Deepening check

An execution ticket's `--content` must carry the two deepening sections
**gap and oracle**, and its acceptance must be `external oracle`-ized and
`falsifiable` — the oracle maps to a `verification` check, not the executor's
or the requester's own say-so. The four points and the full procedure live in
`verbs/execute-intent.md` — do not restate them here.

`edit` flags (only the flags you pass are sent; `--label` replaces the whole
set): `--goal`, `--content`, `--label`, `--priority`, `--milestone` (an empty
string clears it), `--waiting-on`, `--bundle` (switch the work
item's workflow bundle, e.g. `--bundle release`; an empty string unbinds it),
`--parent ""` (detaches the work item from its parent — see Parents and
children; only the empty value is accepted: re-parenting is not supported,
and a non-empty value is refused locally before any call).
aihub accepts a bundle change only while the work item is queued and has
never been claimed — once a daemon has claimed it, the bundle is fixed;
the bundle of a bound parent is also fixed once any of its children was
claimed (`PARENT_BUNDLE_FROZEN`). A refused edit names aihub's reason
beside the code (`CORE-409 CHILD_RUNNING: …`).
There is no `--attr` on `edit`: business attrs are set at create only (aihub
does not accept `attrs` in a PATCH).

A work item's design lives in its content (`pf file edit <wi> --content
@design.md`) while the work item is open — never as a file in the
workspace root or `.polyforge/` (polyforge#177). A terminal work item
refuses the edit (`CORE-410`): store the design as a memory instead —
`pf remember --project P --type doc.design --work-item <wi> @design.md`
(see `remember.md` for reviews, decisions and audits). Scratch output
goes to the workspace's `scratch/` categories (`SKILL.md` names them).

`show` takes exactly one id (or slug) and renders the detail record, its
comment thread and its dependency block.

## Comments (追加式评论)

`pf file comment <id> --body TEXT [--reply-to COMMENT]` appends one comment
to a work item. `--body` takes the text, `-` reads stdin and `@file` reads a
file. A comment carries its author (the caller), a server-assigned time, the
body and an optional `reply_to`.

- **Append-only.** There is no edit verb and no delete verb and no endpoint
  for either; the comment list only grows. Two people commenting on the same
  ticket both persist — neither overwrites the other, unlike `--content`,
  which replaces the whole body. A threaded discussion with the reply
  relation is exactly what `reply_to` is for; there is no other thread
  concept.
- **`reply_to` stays on the same ticket.** It must name a comment that
  already exists on the SAME work item. A missing id or an id from another
  work item is refused (`CORE-404`) and nothing is written.
- **Who can comment.** The same principals that can run `pf file edit`: an
  authenticated aihub user with the writer (or maintainer, or admin) role on
  the work item's project. A caller with no relationship to the project gets
  the missing-work-item answer; a viewer gets `AUTH-403`. A workflow step
  agent cannot reach the entry point directly or through the daemon: `file`
  is a data-plane verb and the CLI's step guard refuses it before any wire
  call, and step processes hold no aihub key.
- **Read side.** `pf file show` renders the thread (id, server time, author,
  body and `reply_to`); `GET /api/v1/work-items/{id}?include=events` carries
  the same rows as the `comments` field; the web console's work-item detail
  page shows them too. Messages anchored with `pf msg send --about-wi` are
  NOT part of this list — they show only in the console (see
  `message.md`).

## Dependencies

A dependency is a `blocks` edge inside one project: "B runs after A". A
must be an execution ticket (a bundle is bound) — a registration ticket
(an unbound parent included) runs nothing: it closes only when someone runs
`pf wrap` on it once done (an unbound parent only after its children are
terminal), so aihub refuses it as a blocker. B
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
running and paused work items. When a second ticket is in the same area
（同一组文件或同一概念）, add `--after <slug>` as a dependency, or merge the
work into one ticket. Do not run parallel tickets against the same semantic
surface. If unsure whether the work overlaps, ask the owner before filing.

`--project` inference: when the flag is omitted, the CLI resolves the
working directory against the `[workspace."<path>"]` mapping of the Cell
the invocation routes to (cli.toml for the default Cell,
`cli-<profile>.toml` for a named one; polyforge#17) — a
cwd inside exactly one mapped workspace that lists exactly ONE project
gets that project as the default and the create proceeds as if the flag
had been passed. A workspace listing two or more projects errors asking
for `--project` (never a silent pick); a cwd matching no mapped workspace
keeps the #113 required-flag error. An explicit `--project` always
bypasses inference byte-for-byte.

### Parents and children

`--parent ID` at create makes the new work item a child; work items nest
one level only (`PARENT_DEPTH`). Any work item may be a parent — a
registration ticket or an execution ticket (polyforge#77) — and a work
item with children may bind a bundle.

- An unbound parent (no bundle) runs nothing: it is closed by hand with
  `pf wrap` once every child is terminal (`verbs/wrap.md`).
- A bundle-bound parent waits for its children: every child that is not
  wrapped holds it like a `blocks` blocker — out of the ready queue, its
  claim refused `DEPENDENCY_BLOCKED`, the child listed in `waiting-for:` /
  WAITING-FOR as `pf-2 (child, running)`. Only a wrapped child releases; a
  failed or canceled child keeps it waiting (`pf queue` marks the parent
  `需要交互：子票失败`), and a child filed later joins the wait. The parent
  runs its own workflow once all its children merged.
- `pf file edit <child> --parent ""` detaches a child: the parent stops
  waiting for it, and aihub records a `wi_detached` event naming the old
  parent. A running child is refused (`CHILD_RUNNING`); a failed or
  canceled one may be detached. Detaching is the way out for a parent held
  by a child that will never merge — never `pf file dep rm`, which removes
  only an edge.
- An order that would make a bound parent and its child wait for each
  other — the parent ordered before its own child, or a loop through other
  edges, on `dep add` and on `create --after` alike — is refused
  `DEPENDENCY_CYCLE`.
- Once any child was claimed, a bound parent's bundle can no longer be
  renamed or unbound (`PARENT_BUNDLE_FROZEN`): the children work against
  the parent's lane.

### Splitting a ticket into an epic (the owner-confirmed split)

A ticket whose pre-submit analysis carries an upgrade recommendation
(`### 升级 epic 建议`) is split only on the owner's confirmation — the
recommendation files nothing by itself. A ticket that is merely large is
not split: the size rule marks it at risk (at least one review rework
round expected). A split is recommended only on rule ①, or on rule ② when
the change carries independent parts that can run in parallel — each
changing its own files, with no interface dependency between them, each
passing acceptance alone — and the units are exactly those independent
parts. The owner having confirmed the
upgrade (and with it the analysis's decomposition — its units, in order;
a unit's key is its stable identifier in that decomposition, never
re-derived on a re-run), the session runs this procedure once, serially,
in one sitting. **A re-run of it files no duplicate**: every unit is
checked before it is filed, and an existing sub-ticket is reused, never
re-filed.

1. **The window.** `pf file show <original>` must show the original
   `queued` and never claimed — the bundle-change window. A claimed,
   paused or terminal original ends the procedure (a mid-run split is
   out of scope): report what you see and stop. Until the first child
   exists the original stays claimable, so check and file the first
   unit in one sitting; an original claimed in between closes the
   window — stop.
2. **The first unit, check first — then file.** Check the first
   decomposition unit exactly as every later one:
   `pf queue --project P --attr split.origin=<id> --attr split.unit=<key>`
   plus `pf file show <original>` for older children filed before the
   markers (the full check shape is step 4's) — no hit: file the first
   unit's child:

   ```
   pf file create --project P --goal <the unit's goal> --bundle <the unit's lane> \
     --parent <original> --attr split.origin=<id> --attr split.unit=<key>
   ```

   The create stores the markers as the nested dot paths they name —
   `split.origin=<id>` lands in `attrs` as `{"split":{"origin":"<id>"}}`,
   with `split.unit` its sibling — the exact shape the check's
   `pf queue --attr` walks: one shape, write and read, so a re-run's
   duplicate check reads what this create wrote (aihub refuses a create
   that plants a flat dotted key).

   From this create the original is held: a bundle-bound parent waits
   for its children — out of the ready queue, its claim refused
   `DEPENDENCY_BLOCKED` — so no machine can claim it until every child
   wraps. The child itself is not claimable while its analysis awaits
   the owner's confirmation (the auto-claim skips it); a child on a
   lane without the analysis check is claimable at once — file it and
   move the original in the same sitting, and know that a child
   claimed before the move freezes the original's lane
   (`PARENT_BUNDLE_FROZEN`) loudly: stop and report, never guess.
3. **Move the original to the epic lane.** Immediately after the first
   child, run `pf file edit <original> --bundle epic` — legal while
   the ticket is queued and never claimed and no child was claimed
   yet. Every child filed after this points `--parent` at a ticket
   already bound to the epic lane, and every child's claim then reads
   the epic lane's `children_branch` as its fixed target branch — the
   order the owner ruled: hold, move, then the rest.
4. **The remaining units, check first — then file.** For each
   remaining unit, in the confirmed order:
   - Check: `pf queue --project P --attr split.origin=<id> --attr split.unit=<key>`
     (the plain list — no `--status` — so every status shows), `<id>` the
     original's wi_ id and `<key>` the unit's stable key; and
     `pf file show <original>` for older children filed before the
     markers (they carry no attrs; the parent's `waiting-for` names
     them). One hit for the unit: reuse that child — file nothing. No
     hit: file it with step 2's shape plus the sibling order
     `--after <the previous unit's child>` — written at create, never
     patched in later. Record the created child's id; a create whose
     answer was lost is recovered by the same check (the attrs are the
     record), never by filing again.
   - A check that cannot be trusted stops the procedure: a list that
     looks truncated (the server's cap reached), a second child for one
     unit, or a child nobody can tell apart — report and ask the owner;
     never guess a match, never re-file over the doubt.
5. **Verify the checklist, then release.** One child per unit, the
   sibling order, the lanes and both split attrs — checked against the
   confirmed decomposition and shown to the owner. Each child's own
   pre-submit checks (its analysis confirmation, `verbs/submit.md`)
   and its `pf submit` follow the ordinary rules — until then the
   unconfirmed analysis holds the auto-claim away; the epic runs when
   every child has wrapped. A failed or canceled child is never
   silently re-filed — the owner decides.

## Success

`--json`: the server's record raw (`comment` answers the stored comment
row). Human mode renders the work item detail view: `slug (id)`, status,
project, goal, milestone, labels, bundle, assignee, waiting, waiting-for,
created (and closed when terminal); `show` adds the comment thread and the
dependency block, and `comment` prints the appended comment's id, author,
server time, `reply_to` and body. `dep add|rm|list` print the dependency view
(`--json`: the server's `{blocking, blocked_by}` raw).

`create` of an execution ticket also prints, on stderr only, an advisory
when the daemon is reachable: the chosen bundle's step sequence with a
reminder that every acceptance criterion names the step that carries it —
it never changes the request, the exit code or the `--json` stdout.

## Failure branches

| Code | When | What to tell the user |
|---|---|---|
| `AUTH-401` | api key missing or invalid | The AIHub credential is rejected — check the Cell's `api_key` (`pf [--profile NAME] config set api_key`). |
| `CORE-400` | Invalid field or type; a lane (workflow.yaml) rule failed at create (`detail` names the rule); an `--attr` key hit the execution-state blacklist; a create's top-level attrs key carries a dot (the CLI nests dotted paths — a `--stdin` body must carry the nested object; `detail` shows it) | The request body is wrong — read `detail` for the exact field/rule and fix the flags. |
| `CORE-400` `PARENT_DEPTH` | `--parent` names a work item that is itself a child — work items nest one level only | Use the top-level work item (the parent) as the parent instead. |
| `CORE-400` `REPO_NOT_IN_PROJECT` | A `--repo` name is not one of the project's repos | Name a repo the project has (`pf projects get <project>` lists them); a maintainer adds new repos to the project. |
| `CORE-400` `INVENTORY_DUPLICATE_NAME` / `INVENTORY_URL_NOT_ALLOWED` / `INVENTORY_SUBDIR_UNSUPPORTED` / `INVENTORY_BASE_UNSUPPORTED` | The same repo named twice; a `--stdin` entry carrying `url`, `subdir` or any key other than `name`; a `--stdin` body carrying the removed `repo_inventory_base` array | Drop the duplicate; give names only. |
| `CORE-400` (dependency) | An edge across two projects, or a work item depending on itself | Dependencies stay inside one project; name a work item of the same project. |
| `CORE-400` `BLOCKER_STILL_BLOCKS` | `pf file edit <id> --bundle ""` (unbind) on a work item that still blocks others (`detail` names them) | Without a bundle it runs nothing, so nothing would release them — remove those edges first (`pf file dep rm`), or rebind to another bundle instead (allowed). |
| `CORE-400` `BLOCKER_NOT_EXECUTABLE` | The blocker (`--after` / the first `dep add` argument) has no bundle: a registration ticket (an unbound parent included) | It runs nothing (it closes only by a hand `pf wrap`) — name the execution ticket that does the work, or bind a bundle to it first (`pf file edit <id> --bundle NAME`). |
| `CORE-404` | Unknown or invisible work item id, unknown project; `dep rm` of an edge that does not exist; `comment --reply-to` naming no comment on THIS work item (missing, or on another ticket) | No such work item (or you cannot see it) — check the `wi_` id or slug and the project name; for `--reply-to`, reply only to a comment on the same ticket (`pf file show` lists the ids). Nothing is written. |
| `CORE-400` `PARENT_BUNDLE_FROZEN` | `pf file edit <parent> --bundle …` on a bundle-bound parent one of whose children was already claimed | The parent's lane is fixed once its children started — nothing was changed; keep the bundle, and ask the owner if the lane really must change. |
| `CORE-409` `CHILD_RUNNING` | `pf file edit <child> --parent ""` on a running child | A running child cannot be detached — wait for its session to end (or pause it), then detach. Nothing was changed. |
| `CORE-409` `DEPENDENCY_CYCLE` | The new edge would close a loop (B already waits, directly or not, for the other side — a bound parent waits for its unwrapped children, so ordering a parent before its own child counts) | Refused — the order you asked for contradicts an existing one; `pf file dep list` shows the chain. |
| `CORE-409` `OPEN_ON_BUNDLE` | `--bundle` names a lane declaring `exclusive: true` (the shipped `integrate`) and another work item of the project on it is not wrapped, failed or canceled; `detail` names it and its status | One at a time: nothing was filed. Follow the open one (`pf watch`); one still `queued` that no machine ever claimed is yours to run (`pf submit <wi>`) or drop (`pf cancel <wi>`), as `detail` says. |
| `CORE-409` | Create-time conflict (e.g. duplicate) | The state machine refuses this write — read `detail`. |
| `CORE-410` | PATCH of a terminal work item (wrapped/failed/canceled) | This work item is closed and can no longer be edited — file a new one instead. |

A `pf:` line with no typed code means a local flag/argument mistake (exit 1
for a sub-action's flags or arguments, e.g. `file create --bogus` or a
missing `--goal`) — read the message, fix the invocation.

Transport: like every data-plane verb this one goes through the local daemon
to the resolved Cell's AIHub connection (`--profile NAME` / `PF_PROFILE` /
the cwd's mapped Cell / `default`); `--server`, `--api-key`, `PF_SERVER` and
`PF_API_KEY` are refused, and with the daemon down it fails with
`DAEMON-410` (`pf daemon ensure`, then retry once).
