a markdown table with exactly four columns, header
`| 问题 | 推荐选项 | 理由 | 重点关注程度 |` (owner ruling 2026-10-21). One row per
focused question, answerable in one line; the row number opens the 问题 cell
(`1. …`, `2. …`) - no fifth column; every row fills 推荐选项 (your recommended
answer), 理由 (why) and 重点关注程度. `重点关注程度` is the LAST column and every
row carries one value (`每行必标`) on the `★~★★★★★` scale: ★ = 例行确认,
★★★★★ = 方向性裁定 / 大影响 / 与既有裁定冲突, ★★–★★★★ scale with impact. The
stars ARE read mechanically: a card whose every row is ★ or ★★ is answered
automatically — each row's 推荐选项 adopted — after the Cell's
`ask_auto_reply_timeout` (default 30m) without an owner reply, so grade
truthfully; a question that cancels or abandons the work must be ≥ ★★★ (and
any row at ★★★ or above keeps the card waiting for you). Escape a
literal `|` inside a cell as `\|`. Context or evidence goes in prose above the
table. After the table ALWAYS (even for a one-row batch; each may be one line)
add `预期效果` (what happens once the batch is answered) and then `注意事项`
(inter-row dependencies or conflicts, relation to prior rulings, which rows are
rubber stamps answerable in bulk). The table rides in one question, and the
owner's combined answer comes back as one text. To ask: run `pf step ask` with the
whole question - the prose, the table, 预期效果 and 注意事项 - as its one argument,
then end your turn. The owner's reply is your next turn - continue from it; never
fabricate or guess an answer you did not receive. A reply that cancels the work
is yours to read: hand off `--status cancel --per-owner <its at>`.

中文可读性——问题卡按这些规则呈现：第一行用一句短句说明本次要定的决定，接着逐条列出证据，再给四列表；
没有证据时直接从表格开始。每句表达一件事，单句控制在 40 字以内，英文句控制在 20 词以内。
需要补充说明时另起一句，括号里只放一句完整说明。标识符、路径和命令用反引号代码格式标注。
证据一条一行，单条控制在 120 字以内。

事实自己查——能从仓库、工具和记忆查到的事实先查清并写进证据；问题卡只承载需要 owner 决定的取舍。
