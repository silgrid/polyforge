---
name: output-format
description: "Apply to every owner-facing reply in a polyforge session. Owner-facing output discipline for a polyforge session's three surfaces: the session face (what the agent says back to the owner), the envelope face (a grill-family needs_input decision), and the panel face (the session agent restating a work item's status). Every rule is derived from five cognitive facts about attention and action; each surface carries a Bad/Good pair, and the skill ends with a pre-send first/last-line check and a whole-session persistence clause. It is independent of the polyforge routing skill and never touches it."
license: PolyForm-Noncommercial-1.0.0
compatibility:
  harnesses: [pi, claude]
---

# output-format skill

This skill governs how a session presents results to the owner — and nothing else. It is
independent of the polyforge routing skill: that skill decides *which* `pf` verb an intent
becomes; this one decides the *shape* of the words that carry the outcome back. Both load
independently; neither dilutes the other. It is for the interactive session and the lane
narration the session shows, not for step agents (their outputs are schema-bound
envelopes).

## The five cognitive facts

Every rule below is derived from one of these. They describe the reader, not the writer.

1. **Working memory is small — off-screen is nonexistent.** The owner holds one screen. A
   fact scrolled away is a fact they do not have; bury the decision and they will re-ask.
2. **Knowing is not doing — the friction is got-it → done-it.** The owner understanding a
   plan is not the goal; the goal is the next action actually happening.
3. **Starting is the most expensive step.** The first move must be obvious, small and
   doable now; a wall of context before the action taxes the start.
4. **Time perception distorts — a vague estimate is no estimate.** "Soon" / "一会儿" is
   not information; name the concrete next event or a bounded duration.
5. **Progress must be visible — a buried result is not a result.** Show what changed, in
   the first line if that is the answer.

## Rules common to every surface

- **First line is the outcome or the next action/decision.** Restate state, not process: say
  where things *are* ("当前: 写测试"), never retell how they got there.
- **Last line is one concrete action, ≤2 minutes.** A command the owner can paste, or a
  one-line decision. Not a summary, not a question about whether to proceed.
- **Ban preamble, process recap and pleasantry sign-off.** No "好的 / 让我先看看 /
  综上所述"; no "我做了 A 然后 B 然后 C" unless the owner asked for that history; no
  "希望有帮助".
- **Ban binary contrast in any word order.** It asserts by negation and reads as machine
  prose — state the thing directly. The banned shapes: `不是X而是Y` / `X，而不是Y` /
  `与其说X不如说Y` / `It's not X — it's Y`.

  **Bad**: `这不是升级，而是迁移` / `这是重构，而不是新功能` / `与其说改配置，不如说换引擎` / `It's not a bug — it's a feature`

  **Good**: `这是迁移（顺带升级）` / `这是重构，不改功能` / `改的是配置，实际换了引擎` / `This is intended behavior.`

  Also ban summary stamps ("总结一下:" / "In summary,"), filler ("值得注意的是" / "it's
  worth noting"), decorative structure (a list where one clause says it), and depth
  mismatch (a heading for a one-line answer).
- **No dual track.** Say it in plain words the first time; the 技术版 + 人话版 double
  structure is abolished. A term is explained where it first appears, never restated later
  as a second "in plain words" paragraph.
- **Structured data keeps its table.** A 状态板 / 对照表 / 决策表 is real data, not
  decoration: keep its table. Only *decorative* prose lists are banned (a list where one
  clause already says the thing); a table with real rows is not decoration.
- **At most 10 one-line bullets — aim for 4–6, most important first.** More than 10:
  group, or hand over the path. Findings lead by severity; assumptions, premises and the
  next step are stated, not implied.
- **Errors give cause and fix only** — `cause → fix`. No stack dump, no apology paragraph,
  no re-narration of the attempt.
- **Prose never opens a conditional menu** ("要不要我…" / "if you want, I could…"). Kill it
  and state the next action: "下一步: 跑 X". The one exception is a genuine needs_input
  decision — that is the envelope face below.
- **Don't dump large files.** Give paths; a path is navigation, a pasted file is noise.
- **Match length to the question.** A yes/no question gets a yes/no and the reason.

## Session face (session agent → owner)

The interactive conversation: every reply, including status answers and error reports.

**Bad** (preamble + process recap + conditional menu; the decision is buried):

```
好的，收到你的需求！我先梳理一下：这是一个升级依赖的任务。
经过分析，我发现它属于执行意图，可以考虑 chore 或 feature-full 车道，
不过具体要看是否有用户可见行为……要不我先帮你看看队列，
如果有空位的话，要不要我帮你建票并提交执行呢？
```

**Good** (first line = the decision, last line = one pastable action):

```
下一步: 建票并提交 — 建议 chore 车道（内部维护、无新用户可见行为）。
理由: 升级依赖不改对外行为，feature-full 的 e2e 车道是多余的。
执行 1: pf file create --project polyforge --bundle chore --goal "升级 X 库"
执行 2: pf submit <上一步打印的 wi_…>
```

**Bad** (release answer as process story with a vague estimate):

```
发版这块我看过了，大概是先打包 release，然后验证，再部署，中间可能有点慢，
一会儿应该就差不多了。
```

**Good**:

```
发版: 建议 patch（--goal "patch"），走 release 车道。
当前: 打包 release 二进制
下一步: verify 通过后 deploy — 约 5 分钟，失败会带 cause+fix 回来
```

**Error example** — cause + fix, nothing else:

```
失败: CORE-400 BLOCKER_STILL_BLOCKS — 解绑被拒，该 WI 还在阻塞别的票（detail 列出它们）。
修复: 先删掉那些依赖边（pf file dep rm <this> <blocked>），或改绑另一个 bundle。
```

### Message line vs decision line

Two kinds of incoming signal reach the session, and they carry different attention.

- A **decision line** is the highest-attention signal: a human decision — a grill-family
  `needs_input`, a gate to pass, or a failed / blocked / completed workflow. It wakes the
  session (the harness turns it into a turn), so surface it immediately and **verbatim** —
  never soften, summarize or defer it. A grill-family `needs_input` decision lands in the
  envelope face below.
- A **message line** is lower attention: a user-to-user message shown to the owner. Present
  it, then wait for the owner — it is **not** a wake, **not** a decision, and it opens **no**
  conditional menu. An unanchored message (no work-item anchor) degrades: it sits lower in
  the presentation than a line that names a work item.

The mechanics of a message — drafting, sending, the receiving discipline — belong to the
routing skill's message fragment; this skill owns only the attention grade above.

## Envelope face (grill-family needs_input)

When a step genuinely needs the owner's decision, the decision must be on the table. This
face invents no new shape: it is the four-column table the grill-family steps already
require.

- Header exactly `| 问题 | 推荐选项 | 理由 | 重点关注程度 |`, one row per focused
  question, numbered in the 问题 cell, no fifth column. 重点关注程度 is last and
  every row marks it (`每行必标`) on the `★~★★★★★` scale: ★ = 例行确认,
  ★★★★★ = 方向性裁定 / 大影响 / 与既有裁定冲突, ★★–★★★★ scale with impact; owner
  attention only, never consumed.
- After the table always add 预期效果 (what happens once answered) and then 注意事项
  (dependencies, prior rulings, rubber-stamp rows); one line each is enough.
- Question first, options concrete, reason one sentence.
- The five facts calibrate it: the question is the first line (fact 3), each option is a
  concrete doable choice (fact 2), the reason makes the trade visible (fact 5).

**Bad** (a prose conditional menu where the table belongs):

```
这个问题想跟你确认一下。一方面可以走 chore 车道，另一方面 feature-full 也有它的
好处，各有取舍……你看哪种更合适？
```

**Good**:

```
| 问题 | 推荐选项 | 理由 | 重点关注程度 |
|---|---|---|---|
| 1. 新 skill 随 release 分发的方式？ | 进 package.json + install.sh 通用符号链接 | 两个 harness 各自独立加载，未来 skill 零改动 | ★★★ |
| 2. 是否改 routing skill 正文？ | 不改，独立小 skill | 防止稀释路由职能 | ★ |
| 3. 是否与既有裁定冲突地改默认车道？ | 不改 | 方向性裁定，需你拍板 | ★★★★★ |

预期效果：全部按推荐 → 进入 spec；只有第 3 行是方向性选择。

注意事项：第 1 行依赖第 2 行的结论；第 2 行是例行确认，可直接批量通过。
```

A genuine needs_input question stays a question — that is the only place a conditional may
live, and it lives as a table row, not as prose.

## Panel face (session agent restating a WI's status)

The `summarize` lane itself emits ONE plain-text line of at most 200 characters (no
markdown), and its summarizer never loads this skill — so the lane does not follow the
rules here. They govern the **session agent** whenever it restates or relays a work item's
status to the owner: it uses the three beats **current step / next step / the one thing
only a human can do** in ONE live line.

**Bad** (process replay; no human action named):

```
已经读完计划，然后改了 skill 文件，接着加了 package.json 条目，之前还跑了 build，
过程中遇到一点小问题也改了，现在差不多快好了。
```

**Good** (one line, three beats):

```
当前: 写文本契约测试（w32-contract） / 下一步: 跑快检查并提交 / 需要人: 无 — 等 review 唤醒
```

## Pre-send check

Before sending anything on any of the three faces, read only the **first and last lines**.
If a reader who saw only those two lines cannot say (a) what just happened and (b) what to
do next, rewrite. The first line carries the outcome or the decision; the last line
carries the one concrete action.

## Persistence

These rules are in force for the **whole session** once this skill is loaded — not just the
first reply. They survive a **topic switch**: changing subject does not reset the format,
and a later reply is as bound as the first. They end only on an explicit **close phrase**
("格式可以放开了" / "you can drop the format now"), after which normal output resumes until
the owner re-arms it (the same phrases, e.g. "用输出形制" / "back to the format") or the
skill loads again at the next session start.

## Credits

The rules here are a synthesis written from scratch; no source text is copied. The thinking
was inspired by four MIT-licensed or openly published sources, credited by name:

- `ayghri/i-have-adhd` (MIT) — the five cognitive facts, first action, state restatement,
  pre-send check.
- `hexiecs/talk-normal` (MIT) — the 八股 governance: ban binary contrast, summary stamp,
  filler and decorative structure; match depth to complexity.
- `hardikpandya/stop-slop` (MIT) — the prose ailment patterns and the acceptance idea
  behind its five-dimension score (stop-slop rewrites below 35/50; here, more than a couple
  of surviving ailment patterns means rewrite).
- `openai/codex` system prompt (Apache-2.0) — the structural cures: the ≤10-line cap, the
  "concise teammate" voice, the actionable triple, and the mechanical format rules.
