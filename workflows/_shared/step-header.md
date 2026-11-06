# How a step runs

You are one step of a work item's lane. Two things connect you to the other steps, to
the owner and to the daemon: the hand-off document `artifacts/handoff.md`, and one
command, `pf step`. The daemon orchestrates; it never reads your words for a verdict.
The steps after you never see your conversation — only what you hand off and the files
you leave.

## Read `artifacts/handoff.md` first

Before anything else, read `artifacts/handoff.md` (capsule-relative), top to bottom. It
only ever grows; nobody rewrites it.

- **The opening** (the daemon's, written at claim): the work item, its goal, its
  acceptance list verbatim and the lane's step order.
- **One section per hand-off**, oldest first. Its header is the daemon's stamp — step,
  round, dispatch, start, end, model, status, the owner messages that step received in
  that round (verbatim), the owner message a status cited (`--per-owner`, verbatim), each
  task repo's start → end commit with its diff stat, the evidence files. Its body (under
  Body / 正文) is that step's own account.
- **Event sections** (the daemon's): a Rewind / 回退 names the steps that run again, why,
  and the rewinds spent out of the lane's budget (n/max); an owner command; the close
  (Close / 收尾).

The daemon stamps in one of two languages, English or Simplified Chinese (the labels
above give both): a section's header in the language its step was told, the opening and
each event in the language set when the daemon wrote it. A document whose language
changed midway carries both; read them alike.

How to read it:

- Nothing is crossed out. A rewind supersedes the sections of the steps it names, and
  they stay worth reading: what was tried, what failed, why.
- You are a step a rewind named: answer every point that sent the work back — the FAIL's
  findings, or the owner's words — one by one, in your body's
  Reply to the previous round / 回应上一轮.
- You run a gate again: check every issue the previous round of this gate raised against
  the tree as it stands now — fixed, or still there.
- A header is the daemon's record: trust its commits, statuses and quoted words. A body is
  another agent's account: check what you build on against the files and commits it
  names. An upstream account that does not hold goes into your Problems and risks found /
  发现的问题和风险.

Your skill (or, for a step without one, this step's definition) names what you read and
what you check before you start (开工前读什么、核对什么) and what your hand-off carries
(交接要写什么).

## Files

- `runtime-state/work-item.json` — the work item: `project`, `slug`, `goal`, `content`
  (its acceptance section included).
- `runtime-state/asks.jsonl` — every owner message to a step, verbatim, each with its
  `at` stamp, its step and its round.
- `runtime-state/recalled-memories.json` — the project memories recalled at claim. Use the
  ones that bear on your work; more through `pf step recall` (below).
- `runtime-state/memory-policy.json` — what is worth remembering (`record`) and what never
  goes into a memory (`skip`).
- Result files — each pre-written command your skill runs writes its result to the
  `--out` path of its line (`schema: pf-step-result/1`: `verdict`, `round`, `heads`,
  `summary`, `details`); the `verdict` is the exit code's, never yours.
- Never read `runtime-state/execution.json`: it is the daemon's private progress record,
  not a hand-off, and its shape is not yours to rely on.

**A value a command needs comes from a file, never from prose.** A version, a commit, a
head, an item id — anything you pass to a command, or check a command's result against —
is read from the result file (or the file your skill names) that carries it, never copied
out of a hand-off body. Bodies carry judgment and context; files carry data.

## `pf step` — your one way to the owner and the daemon

- `pf step ask "<question>"` — a question only the owner can answer. Run it, then end your
  turn: the daemon frees your slot, and the owner's reply comes back into this
  conversation as your next turn. Never guess an answer you did not receive.
- `pf step recall [--any] '<keywords>'`, `pf step memory show <id>`, `pf step remember …`
  — the work item's project memory, through the daemon (a step holds no aihub access).
- `pf step hub <verb> …` — the aihub reads and writes your skill names, and only those
  (the step's `hub` list allows no other). A write is recorded pending: it is made only
  when you hand off `done`, and dropped by any other status.
- `pf step handoff …` — the end of the dispatch (below).

The owner may write to you while you work; the message arrives in this conversation.
Read it and act on it. When it decides more than this step's own work — the work item
ends, a FAIL is accepted, the work goes back to an earlier step — end with the matching
status below, citing that message.

## End with exactly one `pf step handoff`

```
pf step handoff --status <status> --body @<file> [--evidence <path>]... [--to <step>] [--per-owner <at>] [--reason "<one line>"]
```

- **Once, last.** Exactly one per dispatch, as your last action; once it is accepted,
  stop — no further tool call. A refused call (non-zero exit, the reason printed)
  recorded nothing: fix what it names and call it again.
- **`--body @<file>`**: the body, written to a file (`mktemp`) by the body template that
  follows this header (`workflows/_shared/handoff.md` in the capsule).
- **`--evidence <path>`**: each capsule file a later step or the owner checks your
  account against — the result files you ran, the reports you wrote. Each must exist.
- **`--reason`**: one line for the header (why blocked, what failed, what the owner ruled).

| status | when |
|---|---|
| `done` | The step's work is complete — for a gate, a pass. Your pending hub writes are made now, then the next step runs. |
| `fail` | A gate only: what you judged does not pass. The lane goes back along the gate's `on_fail` (a gate without one ends the work item) and spends one rewind of the lane's budget; with `--per-owner <at>` (the owner's words decided this FAIL) it spends none. Once the budget is spent the command refuses a `fail` without `--per-owner`: `pf step ask` the owner — one more round, accepting the FAIL (a `waivable` gate only), or cancel — and hand off as the reply says, citing it. |
| `blocked` | You cannot go on and a person must act: the machine, a tool your skill's retries could not get past, an input that is not there. Not for a question — that is `pf step ask`. The work item stops; the owner's next message to it wakes you. |
| `cancel` | The owner's word: the work item ends. `--per-owner` required. |
| `waive` | A `waivable` gate only, on the owner's word: this FAIL is accepted and the lane goes on. `--per-owner` required. |
| `rewind` | The owner's word: the work goes back to `--to <step>`, an earlier step — or this step's own id, to run this step again from the start. `--per-owner` required; spends no rewind. |

`--per-owner <at>` is the `at` stamp, in `runtime-state/asks.jsonl`, of the owner's
message to this step in this round whose words you carry out. Use `cancel`, `waive` and
`rewind` only on such a message, never on your own judgment, and state the words and
your reading of them in Decisions and why / 关键决定和理由. The daemon checks that the
message exists; it never reads it.

## Memory

Write memories only as `runtime-state/memory-policy.json` says: what fits a `record` entry,
nothing that fits a `skip` entry, never a secret or credential value (name where it lives
instead). A step writes no memory of its own, with one exception: **the last step of the
lane's step order** (the opening lists it), when it is about to hand off `done`, first
writes the work item's one wrap summary:

1. A body of at most 2 KB in a temporary file (`mktemp`, outside the capsule): 做了什么,
   关键决定 (and why), 遗留 (follow-ups, open questions, known gaps), and the lessons that
   fit a `record` entry (a pitfall as 现象 → 原因 → 解法). A lesson that fits no entry but
   that you judge valuable goes in on its own line followed by
   `建议新增条目：<the record entry you propose>`; then add `--tags policy-proposal`.
2. `pf step remember --type experience.wrap --title '<the work item's slug, else id>: <what was done, one line>' @<file>`
   — it prints `stored <id>`. Repeating the same call returns the same memory; a second,
   different summary is refused, so put everything into the one call. A failure (aihub
   down, a refusal) never fails the step: say so in your body and hand off.
