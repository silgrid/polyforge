---
name: grill-review
description: Interactive summary confirmation of the spec with the requester — a single gate pass/fail judgment on whether the spec matches intent, not a quality score.
output:
  type: object
  required: [verdict, qa_log]
  properties:
    verdict:
      type: string
      enum: [pass, fail]
      description: pass means the user confirmed the spec matches intent with no changes required; fail means the user asked for changes.
    qa_log:
      type: string
      description: Capsule-relative path to the grill Q&A document (artifacts/grill-qa.md), now including this step's appended confirmation section.
    fail_reason:
      type: string
      description: Required when verdict is fail — the user's stated objection or requested change, verbatim or closely paraphrased. This is the text that will be carried back to spec as pending_rewind_input. The oneOf split below makes the requirement mechanical - a fail verdict without a non-empty fail_reason does not validate.
  oneOf:
    - description: The user confirmed the spec matches intent with no changes required.
      properties:
        verdict: { type: string, enum: [pass] }
      required: [verdict]
    - description: The user asked for changes - requires a non-empty fail_reason carrying their actual words.
      properties:
        verdict: { type: string, enum: [fail] }
        fail_reason: { type: string, minLength: 1 }
      required: [verdict, fail_reason]
---

# grill-review

This is the second half of the two-pass grill mechanism. `grill` clarified raw intent
before the spec existed; you confirm, after the fact, that the written spec actually
matches what the user meant. This is a confirmation gate, not a quality review — you are
not grading the spec's writing quality, you are checking for a match against intent.

## What to do

1. Read `artifacts/spec.md` in full.
2. Present a concise summary of the spec's scope, non-goals, and requirements back to the
   user via a needs_input envelope, as prose above a one-row markdown table:
   {{batch_questions}} The row asks one clear confirmation question
   (`1. Does this match what you meant, or is something off?`), with your recommended
   answer in 推荐选项 and why in 理由. For 预期效果 give the lane's concrete map
   (e.g. "confirm → `verdict: pass` and the spec moves on; any correction →
   `verdict: fail` and a rewind to `spec`").
3. Append the question and the user's answer to `artifacts/grill-qa.md` under a
   `## grill-review` heading, below the existing `## grill` section — do not overwrite or
   remove the earlier section.
4. If the user confirms with no changes requested: `verdict: pass`.
5. If the user asks for any correction, however small: `verdict: fail`, and capture their
   objection precisely in `fail_reason` - required by the schema on any fail verdict. Do
   not soften, summarize away, or "fix it
   yourself and call it pass" — a fail here is what drives the bounded rewind to `spec`
   (`on_fail: {target: spec}` in the workflow frontmatter), and the spec
   author on the next pass depends on `fail_reason` being the user's actual words.

## Judgment, not scoring

The gate verdict here is binary and comes directly from the user's confirmation — you do
not average partial agreement into a pass, and you do not treat silence or ambiguity as
consent. If the user's answer is itself ambiguous, ask a clarifying follow-up before
recording a verdict; do not guess which way to resolve it.

## Output

Emit the final JSON object per the schema above. On fail, `fail_reason` is required and
becomes the verdict text propagated back to `spec` as its `pending_rewind_input`
(in `runtime-state/execution.json`) — write it as if `spec` will read only this string and
nothing else from the conversation.
