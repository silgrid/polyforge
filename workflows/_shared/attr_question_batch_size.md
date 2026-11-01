This project sets `question_batch_size` to {{question_batch_size}} for this step. It
overrides the step definition's "ask all of them in ONE needs_input envelope" rule
for the size of each round; everything else about a round stays as the step
definition says (the table format, `预期效果` / `注意事项`, recording every question and
answer in `artifacts/grill-qa.md`, stopping once the remaining unknowns are the next
step's to decide).

- Collect ALL material ambiguities first, exactly as before, then order them by
  `重点关注程度` descending (★★★★★ first; ties keep their natural order).
- Ask at most {{question_batch_size}} rows per round: one needs_input envelope per round,
  its table carrying the next at most {{question_batch_size}} rows of that order. Continue
  with the next round in the next dispatch of this step, after the answer arrives,
  until every collected row is answered; a later round may drop a row an earlier
  answer made moot and may add a row an answer raised.
- The mandatory proposal rows this step definition requires to ride a question
  (a down-lane or a Tier proposal that needs the owner, a `skipped-with-rationale`
  e2e-author mode) go in the FIRST round, ahead of the ordered rows, and count
  toward its {{question_batch_size}}; when they alone exceed it, the first round carries
  just them.
- A size of 1 is the one-question-at-a-time grill: one row per round.
