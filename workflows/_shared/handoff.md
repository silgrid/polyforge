# The hand-off body

Your `pf step handoff --body @<file>` carries the seven sections below as headings, in this
order, each filled for this step (your skill's 交接要写什么 says what goes where for it).
The headings come in two languages, English and Simplified Chinese: write the set of the
language this dispatch was told (the first part of your prompt names it) — the command
checks the body against that set only. A step that handed off before — a later round,
after a rewind — adds the re-entry heading first (`### Reply to the previous round` /
`### 回应上一轮`). The command refuses a body that lacks a heading, or one over 3,000
characters: put the detail into files under `artifacts/` and name their paths. Never
restate what the daemon stamps in the header (the times, the model, the commits, the
owner's messages); refer to it.

English:

```markdown
### Reply to the previous round
(only when this step handed off before) Every point that sent the work back here — the
FAIL's findings, or the owner's words — numbered as there, each with what you did:
fixed (where: commit, file), or left as it was and why.

### Conclusion
One or two lines: the outcome you hand off with, and the one fact the next step must
know first.

### The question this step answers
The question this step answers for this work item, in one or two lines.

### What was done
In order: each command you ran as written with its exit code, each commit you made, each
file you wrote.

### Decisions and why
Each decision you took and why. Each owner message you acted on: its `at`, its words
verbatim, and your reading of them.

### Verification
What shows the result holds: the result files and their verdicts, the upstream checks you
made before you started and what they found. Name files; do not copy them.

### Problems and risks found
What is wrong or at risk — in the change, upstream (a hand-off that did not hold), or in
the machine. `none` when there is none.

### For downstream, and what is open
What a later step must know or check, and what stays open (a follow-up, a question for
the owner). `none` when there is none.
```

Simplified Chinese (the same sections, in the same order):

```markdown
### 回应上一轮
（仅当本步之前交接过）把工作退回到这里的每一点——FAIL 的发现或 owner 的原话——按原编号逐条写出你的处理：
已修（在哪：提交、文件），或保持不变及理由。

### 结论
一两行：你交接时的结果，以及下一步必须先知道的那一个事实。

### 这一步要回答什么
这一步为本工单回答的问题，一两行。

### 做了什么
按顺序：你运行的每条命令（原样）及其退出码、你做的每个提交、你写的每个文件。

### 关键决定和理由
你做的每个决定及理由。你据以行动的每条 owner 消息：它的 `at`、原话、你的理解。

### 验证
什么表明结果成立：结果文件及其结论、你开工前对上游的核对及发现。写文件名，不要复制内容。

### 发现的问题和风险
哪里有错或有风险——在改动里、在上游（一份不成立的交接）或在机器上。没有就写 `none`。

### 给下游与未决
后续步骤必须知道或核对的事，以及仍未决的事（后续工作、给 owner 的问题）。没有就写 `none`。
```
