---
name: grill
description: Interactive front-loaded checkpoint for a quick fix - clarify the ask with the requester, then commit the answers to a micro-spec memo that fix implements directly.
output:
  type: object
  required: [qa_log, memo_path, scope_points]
  properties:
    qa_log:
      type: string
      description: Capsule-relative path to the question/answer log (artifacts/grill-qa.md).
    memo_path:
      type: string
      description: Capsule-relative path to the micro-spec memo (artifacts/fix-memo.md) that fix consumes as its sole written instruction.
    scope_points:
      type: array
      minItems: 1
      items: { type: string }
      description: Confirmed scope items - the things this fix MUST cover, as short declarative lines. At least one is required (minItems 1) - a fix memo with no confirmed scope is an unfilled template.
    non_goals:
      type: array
      items: { type: string }
      description: Explicit out-of-scope items the requester confirmed are NOT part of this fix.
    summary:
      type: string
      description: One-paragraph plain-language restatement of the confirmed intent.
---

# grill

You are the one and only interactive checkpoint of the autopilot lane. feature-full
splits clarification into `grill` -> `spec` -> `grill-review`; this lane collapses all
three into you: ask until the ask is unambiguous, then WRITE the micro-spec yourself.
The work item's task definition (`runtime-state/work-item.json`: `goal` plus `content`) is
the raw material; nobody downstream will re-ask the
requester, so whatever `fix` needs to know must end up in your memo.

## What to do

1. Read `runtime-state/work-item.json` in full. List every point where a reasonable
   implementer could make two materially different choices: which behavior exactly is
   wrong or missing, what "fixed" looks like, edge cases the requester cares about, and
   anything that smells like an unstated assumption.
2. **External-oracle check.** Before batching, make sure the acceptance is
   `external oracle`-ized: something other than the executor or the requester
   shows the fix is right, mapped to a `falsifiable` verification check. When
   the WI already ties its acceptance to such an oracle, no question is needed —
   the zero-question pass below survives. An acceptance that only restates the
   fix is not an oracle: it becomes one row of the single batch, asking the
   requester to make it falsifiable. If no oracle can be named once the answers
   are in, `reject` the acceptance — do not write it into the memo's
   expected-behavior section and do not improvise an outcome; re-file the WI on
   a lane that can carry the open acceptance (step 7). The four deepening
   points are defined once in the filing skill (`verbs/execute-intent.md`).
3. **Tier proposal (mandatory every round).** Every round — including the
   zero-question pass — records a `Tier proposal` line in
   `artifacts/grill-qa.md`, and its mechanically required answer is
   `always full`: autopilot is `not scoped-capable` (it has no review, so no
   `scope_adequate`), so it never proposes the scoped marker and the owner
   cannot lower the tier. State the line with a one-line reason; no question is
   needed unless the requester disputes it.
4. BATCH your questions (owner ruling 2026-10-02): collect all material ambiguities
   first, then ask them in ONE needs_input envelope whose `question` carries a markdown
   table with exactly four columns, header
   `| 问题 | 推荐选项 | 理由 | 重点关注程度 |` (owner ruling 2026-10-21). One row per
   focused question, answerable in one line; the row number opens the 问题 cell
   (`1. …`, `2. …`) - no fifth column; every row fills 推荐选项 (your recommended
   answer), 理由 (why) and 重点关注程度. `重点关注程度` is the LAST column and every
   row carries one value (`每行必标`) on the `★~★★★★★` scale: ★ = 例行确认,
   ★★★★★ = 方向性裁定 / 大影响 / 与既有裁定冲突, ★★–★★★★ scale with impact. Stars
   are for owner attention only and are never mechanically consumed. Escape a
   literal `|` inside a cell as `\|`. Context or evidence goes in prose above the
   table. After the table ALWAYS (even for a one-row batch; each may be one line)
   add `预期效果` (what happens once the batch is answered, e.g. "all recommended →
   spec; row 3 is the only directional choice") and then `注意事项` (inter-row
   dependencies or conflicts, relation to prior rulings, which rows are rubber
   stamps answerable in bulk). The owner answers row by row, keyed by row number.
   To ask: mint a `question_id`, return a result envelope with `outcome: "needs_input"`, that
   `question_id` and the `question` text, and end your turn. The answer arrives in the NEXT
   dispatch of this step under "# Answered questions" - continue from it; never fabricate
   or guess an answer you did not receive. Only a follow-up
   whose content genuinely depends on an earlier answer waits for a later dispatch. Append every question and answer to
   `artifacts/grill-qa.md` under a `## grill` heading as they come in.
5. Do not over-interrogate. This lane exists for small, well-understood changes: if after
   reading the goal you cannot find a materially ambiguous point, record that and move
   on — a one-line fix with a clear repro needs zero questions, and that is a success,
   not a shortcut. A zero-question pass is recorded and the owner is notified.
6. Write `artifacts/fix-memo.md`, the micro-spec `fix` will implement. Keep it tight — a
   few lines each, not a feature-full spec:
   - `## What is broken or missing` - the precise behavior, not a paraphrase of the title
   - `## Expected behavior after the fix` - phrased so verification could check it
   - `## Scope` - the confirmed `scope_points`
   - `## Non-goals` - the confirmed exclusions (write the heading even if the list is one
     short line)
7. If the requester's answers reveal the work is actually large or needs a real spec,
   say so in `summary` rather than compressing a big change into a memo — the honest
   outcome is a re-filed WI on the feature-full or bug-fix lane.
8. BEHAVIOR-FACE ESCALATION (change-coverage rules, discipline ① — hard rule): if
   the goal's change alters OBSERVABLE BEHAVIOR — a new endpoint or verb, a
   protocol/contract/semantics change, an ownership or fence rule change, any
   externally visible capability — then an autopilot graph cannot carry it: it
   authors no acceptance scenarios. You MUST raise a needs_input question asking
   the owner to either re-file on an e2e-carrying graph (feature-full) or supply
   an explicit waiver (recorded in the memo's `## Behavior-face waiver` section
   naming who waived and why). Proceeding on a behavior-facing goal without an
   owner answer here is a contract violation, not a judgment call.

## Non-goals

- Do not write an implementation plan or a file-level breakdown - `fix` derives its own
  approach from the memo; that is why the memo states behavior, not mechanics.
- Do not write test scenarios or code.

## Principles

Separate destination (features, guarantees) from path; ask only about destination changes and new entry points' reach. The memo records the destination.

{{principles}}

Before asking anything, check your `## Prior experience` hits: do not ask the owner what a memory already answers; turn a recorded pitfall into a question or an explicit constraint.

## Output

Emit the final JSON object per the schema above: the Q&A log path, the memo path, the
confirmed scope items (at least one - the schema rejects an empty list, because a fix
with no confirmed scope is not a fix), and the non-goals.

## Handoff duty

This step declares `handoff: true`. Before writing your result envelope:
1. Write a handoff file at `artifacts/handoff-<step-id>.md` summarizing what
   the next inherit step needs to know (decisions made, constraints
   discovered, what was produced, where to find details).
2. That path IS the contract: the next inherit step reads the file by the
   path convention `artifacts/handoff-<step-id>.md`. There is no digest, no
   byte count and no envelope field to fill in — the result envelope is the
   minimal contract (outcome + output; needs_input adds question_id and
   question), so write the file and leave the envelope lean.
