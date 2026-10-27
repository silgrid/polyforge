# message — user-to-user messages (`pf msg`)

Split-plane verb: `pf msg send` / `list` / `show` are data-plane (the `pf`
CLI talks HTTP direct to AIHub with the human's own key); `pf msg analyze`
is execution-plane (the local daemon runs an explicit, read-only,
budget-capped analyzer one-shot, and its conclusion stays on the caller's
machine). A conversation is one direct 1:1 history per user pair: the first
message to a user creates it and every later message reuses it. Messages
address human users only — a chat id, work item, machine or broadcast is
not an address.

## Triggers

- EN: send X a message / send a DM / reach someone / check my messages / my
  inbox / any messages for me / read that message / analyze that message
- 中文: 给 X 发消息 / 发私信 / 联系某人 / 收信 / 看看有没有消息 / 有哪些消息 /
  看那条消息 / 分析这条消息

## Command

```
pf msg send --to <user> --text <text> [--about-wi <wi>] [--reply-to <msg>]
pf msg list [--conversation <id>] [--after <seq>] [--limit N]
pf msg show <id>
pf msg analyze <id>
```

- `send`: `--to` names an existing human user id, `--text` is the body
  (≤4096 bytes UTF-8), `--about-wi` is an optional work-item anchor and
  `--reply-to` an optional message id in the same conversation.
- `list`: without `--conversation` it lists the caller's conversations;
  with it, that conversation's messages. `--after` is a sequence cursor and
  `--limit` the page size.
- `show <id>`: one message.
- `analyze <id>`: the daemon's read-only, budget-capped analyzer run.

`--about-wi` is an **anchor, never a route**: it ties the message to a work
item for presentation and recall; it decides nothing about the recipient.
`--json` is the server/daemon payload passthrough.

## Drafting (起草): the four-section template

Before running `pf msg send`, draft the message in four labeled sections —
one per label, no extra concept — and show the owner the draft before
sending:

- **上下文** — why this message now, and the shared context it stands on.
- **正文** — the message body itself, the words the recipient reads.
- **诉求** — what the sender wants the recipient to do or decide.
- **参考** — links, ids and anchors that back it; the `--about-wi` anchor
  belongs here.

The 正文 lands in `--text`, a work-item anchor in `--about-wi`; a reference
never becomes an address (`--to` stays a human user id).

## Receiving side: presentation discipline (收信端呈现纪律)

- A **message line** shown to the owner does **not** trigger an agent turn.
  It is not a `pf watch` decision line: the session presents it and waits
  for the owner. Never treat an incoming message as a `needs_input` the
  agent may answer.
- An **unanchored message** (no `--about-wi`) **degrades**: it is shown
  lower in the presentation, without a work-item context to act on. Never
  fabricate an anchor to make it look routed.
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
| `AUTH-401` | api key missing or invalid | Credential rejected — check `--api-key` / `PF_API_KEY` / the config's `api_key`. |
| `AUTH-403` | `list --conversation` on a conversation you are not a member of | Only its members and admins can read it — ask a member, or list without `--conversation`. |
| `CORE-404` | `--to` names no existing human user, or an unknown message / conversation id | Check the user id (a chat id, work item, machine or broadcast is not an address) or the message id. |
| `DAEMON-409` | `analyze` while another analyzer run is in progress, or the project has no `summarize.models` configured | Wait for the running analysis to finish and retry; or configure `summarize.models` for the project. |
| `DAEMON-500` | the analyzer run failed within its budget (every candidate model failed, or the wall-clock budget ran out) | Retry once; if it keeps failing, check the project's `summarize.models`. |
| `dial unix …` | daemon not running (`analyze` only) | Nothing was analyzed — run `pf daemon ensure`, then retry once (or check `--socket`). |

A local `pf: msg …` line (exit 1, no typed code) means a local refusal
before any wire call — a missing `--to` / `--text`, an unknown sub-verb, a
missing or extra message id (`show` / `analyze` take exactly one).

## Notes

- `send` / `list` / `show` are refused inside a workflow step; `analyze` is
  too. This fragment is for the interactive session, not a step agent.
