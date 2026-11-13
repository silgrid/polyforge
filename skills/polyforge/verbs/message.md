# message — user-to-user messages (`pf msg`)

Split-plane verb: `pf msg send` / `list` / `ack` / `show` / `contacts` are data-plane
(the `pf` CLI talks HTTP direct to AIHub with the human's own key); `pf msg analyze`
and `pf msg prompt` are execution-plane (the local daemon runs one explicit,
read-only, budget-capped task and answers its structured analysis, or the
execution prompt it wrote, to the caller only). A conversation is one direct 1:1 history per user pair: the first
message to a user creates it and every later message reuses it. Messages
address human users only — a chat id, work item, machine or broadcast is
not an address.

## Triggers

- EN: send X a message / send a DM / reach someone / check my messages / my
  inbox / any messages for me / read that message / analyze that message /
  turn that message into a prompt / who can I message / list my contacts
- 中文: 给 X 发消息 / 发私信 / 联系某人 / 收信 / 看看有没有消息 / 有哪些消息 /
  看那条消息 / 分析这条消息 / 把这条消息整理成 prompt / 我能联系谁 / 联系人列表

## Command

```
pf msg send --to <user> --text <text> [--about-wi <wi>] [--reply-to <msg>]
pf msg list [--conversation <id>] [--after <seq>] [--before <seq>] [--limit N]
pf msg ack <conversation> [--seq N]
pf msg show <id>
pf msg analyze <id>
pf msg prompt <id>
pf msg contacts
```

- `send`: `--to` names an existing human user id, `--text` is the body
  (≤4096 bytes UTF-8), `--about-wi` is an optional work-item anchor and
  `--reply-to` an optional message id in the same conversation.
- `list`: without `--conversation` it lists the conversations the caller
  may see (a member its own; an admin every conversation, as before) with
  their unread counts; with it, that conversation's messages. `--json` passes
  each conversation row's `member_names` (display names, membership scope,
  no lookup) through unchanged. `--after` is a forward sequence
  cursor, `--before` a backward one (the page immediately below it, ascending
  — the newest page is `--before <newest+1>`), and `--limit` the page size.
- `ack <conversation> [--seq N]`: advances the caller's **read** cursor, and
  only that. Without `--seq` it first reads the conversation's newest
  sequence and acknowledges it; an explicit `--seq` is forwarded unchanged
  (`0` or negative is refused by the server with `CORE-400`). A repeated or
  backward acknowledgment is a successful no-op: the read cursor only moves
  forward. Machine delivery is separate — delivery never clears unread.
- `show <id>`: one message.
- `analyze <id>`: one blocking call. The daemon spawns **one task** in the
  task lane (`verbs/task.md`) — in the message's work-item project, else the
  first mapped project — with models from `steps.<project>.task.models`
  (else `steps.<project>.default.models`), the read/search-only tool profile,
  a fixed 5-minute / 100000 fresh-token budget (no flag or config key changes
  it) and the caller as the task's reporter; it waits until that task ends and
  answers one structured result: `type` (one of `requirement`, `bug`,
  `ruling`, `discussion`, `clarification`, `outreach`, `other`), `summary`,
  `suggested_actions` (zero or more of `file_ticket`, `reply`, `note_only`),
  `reply_draft` (only beside a `reply` suggestion), `model`, `duration`,
  `project` and `task_id`. A report that is not that JSON shape degrades to
  `type` `other` with the report text as the summary. The task shows in
  `pf task list` and the Tasks tab; `pf task show <task_id>` reads it. There
  is no analyzer-wide lock — two analyses may run at once — but the task
  lane's two slots are shared, and nothing is queued. The list row's
  `prompt_summary` is only `Analyze message <id>`; the **whole prompt,
  message body included, is readable through `pf task show` by any local
  unix-socket peer** for the task store's unchanged retention (newest 50
  entries, 7 days), and that task report is the only stored copy of the
  analysis. A dropped client stops only the wait: the task finishes and stays
  readable, and the next `analyze` starts a new task.
- `prompt <id>`: one blocking call, built like `analyze` (same project
  choice, task models, read/search-only profile, fixed 5-minute / 100000
  fresh-token budget, caller as reporter, no lock, the same client bound).
  The task restates the message as a self-contained execution prompt for
  another agent, in the message's language; its input is the message, at
  most the 10 most recent earlier messages of its conversation (oldest
  first), the replied-to message when set and the anchored work item when
  readable — nothing later, nothing from another conversation. It answers
  exactly `prompt`, `task_id`, `model` and `duration`; human output is the
  prompt, then the model, duration and task id. The list row's
  `prompt_summary` is only `Turn message <id> into an execution prompt`;
  the task record (`pf task show <task_id>`) is the only stored copy. The
  prompt is never sent anywhere: pasting it into a session is the human's
  move (pi-web's 「@」 action fills a composer and sends nothing).
- `contacts`: the people the caller can message — the union, deduplicated
  by user id, of the human co-members (id + display name; never the caller,
  never a machine) of every project the `cli.toml` `[workspace.*]` mapping
  names. The mapping is read at call time, so an edited workspace entry
  shows on the next call without a daemon restart. One read per project, at
  most 4 at once, 15 s each, no retry; a project the caller cannot read
  contributes nothing and is not an error, and no mapping is an empty
  answer. Human output is one `<id>  <name>` line per contact; `--json` is
  the assembled `{"contacts":[{"id","display_name"}]}`.

`--about-wi` is an **anchor that publishes**: it ties the message to a work
item, so the ticket's console detail page shows it to anyone who can see
that ticket — matched by the work item's slug OR its id, historical messages
included. It still routes nothing: the recipient is only `--to` (a human
user id), and an unanchored message stays in the conversation alone. Anchor
a message only when its content belongs to the ticket and may be read by
everyone who can see it; the anchor is stored as the raw string the sender
gave, so a slug (`polyforge#140`) and an id (`wi_…`) both work.
`--json` is the server/daemon payload passthrough (for `contacts`, the
answer pf assembles from the per-project reads).

## Drafting (起草): the four-section template

Before running `pf msg send`, draft the message in four labeled sections —
one per label, no extra concept — and show the owner the draft before
sending:

- **上下文** — why this message now, and the shared context it stands on.
- **正文** — the message body itself, the words the recipient reads.
- **诉求** — what the sender wants the recipient to do or decide.
- **参考** — links, ids and anchors that back it; the `--about-wi` anchor
  belongs here — and it publishes the 正文 on that ticket, so never anchor a
  message the ticket's readers must not see.

The 正文 lands in `--text`, a work-item anchor in `--about-wi`; a reference
never becomes an address (`--to` stays a human user id).

## Receiving side: presentation discipline (收信端呈现纪律)

- A **message line** shown to the owner does **not** trigger an agent turn.
  It is not a `pf watch` decision line: the session presents it and waits
  for the owner. Never treat an incoming message as a `needs_input` the
  agent may answer.
- An **unanchored message** (no `--about-wi`) **degrades**: it is shown
  lower in the presentation, without a work-item context to act on. Never
  fabricate an anchor to make it look routed — and remember an anchor also
  publishes the message on the ticket (see above).
- When a message carries **ruling / convention-type content**, the session
  **proposes** `pf remember` — an owner-facing suggestion only; the session
  never writes memory on its own. Attribute the memory via `--work-item`,
  never prose (the memory rules live in `SKILL.md`).

## Failure branches

| Code | When | What to tell the user |
|---|---|---|
| `CORE-400` | a message addressed to yourself, or a blank body | Address a different human user — a direct conversation needs a different recipient — and give it text. |
| `CORE-422` | secret-shaped content in `--text` or `--about-wi` (refused before any row is written) | Remove the secret — it is never stored or sent. |
| `CORE-413` | `--text` over 4096 bytes, or `--about-wi` over its anchor bound | Shorten it: trim the body or move detail to a linked reference. |
| `CORE-429` | per-sender flood cap | Wait for the window to roll over before sending again. |
| `AUTH-401` | api key missing or invalid | Credential rejected — check the Cell's `api_key` (`pf [--profile NAME] config set api_key`). |
| `AUTH-403` | `ack` of a conversation you are not a member of (admins included — an admin cannot acknowledge for someone else) | Only its members can acknowledge it — ask a member. An admin can still read it through `list --conversation` / `show`; reading never moves anyone's read cursor. |
| `CORE-400` | `ack --seq` that is `0`/negative or past the conversation's newest message | Acknowledge a sequence the conversation actually has; without `--seq` the CLI uses the newest one. Nothing was written. |
| `CORE-404` | `--to` names no existing human user, or an unknown message / conversation id (`ack` included) | Check the user id (a chat id, work item, machine or broadcast is not an address), the message id or the conversation id. |
| `DAEMON-409` | `analyze` / `prompt` while both task slots are busy (2 tasks already running; nothing is queued, no task was created) | The machine is busy — retry once a running task ends (`pf task list`). |
| `DAEMON-400` | `analyze` / `prompt` for a project with no task model chain (neither `steps.<project>.task.models` nor `steps.<project>.default.models`) — no task was created | Configure `steps.<project>.task.models` in the workspace yaml, then retry. |
| `DAEMON-412` | `analyze` / `prompt` of a message whose project is not mapped on this daemon (or no project is mapped at all) — no task was created | Map the project in `cli.toml` (`pf doctor` checks the mapping). |
| `DAEMON-500` | the analysis task ended `failed`, `capped` (its 5-minute / 100000-token budget ran out) or `interrupted` without a usable report; the detail names the state and the task id | Read it with `pf task show <task_id>`; retry once — a retry starts a new task. |
| `DAEMON-500` | the prompt task ended `failed`, `capped` or `interrupted`, or completed with a blank report; the detail names the state and the task id and carries no prompt text | Read it with `pf task show <task_id>`; retry once — a retry starts a new task. |
| `AUTH-403` / `CORE-404` | `prompt`: the daemon may not read the message's conversation, its replied-to message, or the message does not exist — no task was created and no partial prompt is built | Check the message id; only a conversation this machine's identity can read can be turned into a prompt. |
| `DAEMON-403` | `prompt` / `analyze` route called from a workflow step or task process | Not reachable from a step or task; run it from the interactive session. |
| `dial unix …` | daemon not running (`analyze` / `prompt` only) | Nothing was analyzed or written — run `pf daemon ensure`, then retry once (or check `--socket`). |

A local `pf: msg …` line (exit 1, no typed code) means a local refusal
before any wire call — a missing `--to` / `--text`, an unknown sub-verb, a
missing or extra message id (`show` / `analyze` / `prompt` take exactly one).

## Notes

- `send` / `list` / `ack` / `show` / `contacts` are refused inside a
  workflow step; `analyze` and `prompt` are too. This fragment is for the interactive
  session, not a step agent: a step holds no aihub credential, so neither
  the ack, the delivery-cursor nor the project member entry point is
  reachable from it.

Transport: like every data-plane verb this one goes through the local daemon
to the resolved Cell's AIHub connection (`--profile NAME` / `PF_PROFILE` /
the cwd's mapped Cell / `default`); `--server`, `--api-key`, `PF_SERVER` and
`PF_API_KEY` are refused, and with the daemon down it fails with
`DAEMON-410` (`pf daemon ensure`, then retry once).
