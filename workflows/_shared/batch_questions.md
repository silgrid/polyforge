a markdown table with exactly four columns, header
`| 问题 | 推荐选项 | 理由 | 重点关注程度 |` (owner ruling 2026-10-21). One row per
focused question, answerable in one line; the row number opens the 问题 cell
(`1. …`, `2. …`) - no fifth column; every row fills 推荐选项 (your recommended
answer), 理由 (why) and 重点关注程度. `重点关注程度` is the LAST column and every
row carries one value (`每行必标`) on the `★~★★★★★` scale: ★ = 例行确认,
★★★★★ = 方向性裁定 / 大影响 / 与既有裁定冲突, ★★–★★★★ scale with impact. Stars
are for owner attention only and are never mechanically consumed. Escape a
literal `|` inside a cell as `\|`. Context or evidence goes in prose above the
table. After the table ALWAYS (even for a one-row batch; each may be one line)
add `预期效果` (what happens once the batch is answered) and then `注意事项`
(inter-row dependencies or conflicts, relation to prior rulings, which rows are
rubber stamps answerable in bulk). The table rides in one question body with one
`question_id`, and the combined answer comes back as one text. To ask: mint a
`question_id`, return a result envelope with `outcome: "needs_input"`, that
`question_id` and the `question` text, and end your turn. The answer arrives in
the NEXT dispatch of this step under "# Answered questions" - continue from it;
never fabricate or guess an answer you did not receive.
