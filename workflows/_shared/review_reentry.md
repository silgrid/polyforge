## Re-entry

This section changes nothing about the context policy: `review` always runs **fresh** —
the conversation of a prior review round is never inherited. On a re-entry the only
thing inherited is the prior round's **structured findings list**, read from the
archive (`artifacts/steps/review/`), never from the conversation; a fresh context is
the point, so the new reviewer does not inherit the old reviewer's blind spots.

Find the prior rounds by listing `artifacts/steps/review/`: every `s<n>/result.json` whose
`n` is earlier than this dispatch's derivation ordinal is a prior round **only when its
archived result carried a verdict** — `output.verdict` is `pass` or `fail` under
`outcome: completed` or `outcome: gate_failed`. A review that delivered `verdict: fail`
under `completed` is verdict-bearing too: the driver re-binds that envelope to a gate
FAIL and records the round in `steps.review.last_review_round`, so its archive is a
baseline — never skip it. An archived `needs_input` or `incomplete` round judged
nothing and is not a prior round: skip it and count the verdict-bearing
archives alone — the same baseline the driver's `steps.review.last_review_round` keeps.
The driver renders this section only because such a round is on record
(`steps.review.last_review_round`).

On a re-entry, run two additive faces over the current tree:

- **checklist face** — take the latest prior (verdict-bearing) round's findings (the
  `findings` array of its archived `result.json`) and verify each finding's disposition
  on the current tree
  — fixed, or waived with an owner ruling on record. A prior **blocking** finding that
  is still present is a **residual**: report it as a `blocking` finding and reference
  the original by its position in that round's findings list (e.g. "prior round s<n>
  finding #k") **without re-deriving** it as a new finding — it is the same finding,
  still open, not a fresh one — and carry that reference in the finding's `residual_of`
  field (the schema above), so the driver's rewind-quota accounting can tell a residual
  from a new discovery. A residual re-emits the original's `repair_target` class
  unless the current tree shows its fix belongs elsewhere. A prior **non-blocking** finding that is still present
  is recorded `non-blocking` and never fails the gate.
- **fresh face** — the step definition's full judgment still runs, from the fresh context.
  The fix surface is the branch's own commits since the last verdict round's recorded
  heads, and their semantic propagation: per task repo, the first-parent, non-merge
  commits `git log --first-parent --no-merges -p <head>..HEAD`, where `<head>` is that
  repo's entry in `runtime-state/execution.json`'s
  `steps.review.last_review_round.heads` — the driver
  records the task-repo heads every verdict-bearing round judged (a FAIL the tree it
  failed, a PASS the tree it covered), in that round's own settlement write, and the
  record survives rewinds. Never read the fix surface off `git diff <head>..HEAD`:
  after a passed round, a later verification or deliver merges the latest default
  branch (`git merge --no-edit origin/<default>`), so that range also carries every
  upstream change the merge brought in — none of it is the fix. When
  `last_review_round.heads` is absent the fix surface is the whole change: judge it
  like a first round. Genuinely new defects are new findings and are reported as
  such, blocking when the round contract says so.

The light-round mis-marking duty below still runs; the two faces are additive, not
alternatives.

## The round contract

The FIRST round is the full judgment it has always been: every hit on the
owner-rules checklist is a blocking finding, and the verdict is fail if
any exists. From the SECOND round on — this re-entry included —
the blocking set narrows, so a converging fix loop is not re-litigated
forever. A blocking finding from round 2 on MUST be one of:

- a **residual** — a prior round's blocking finding still present, referenced
  by its original position (`residual_of`) **without re-deriving** it;
- a **new discovery on the fix surface** — the first-parent, non-merge commits since
  the last verdict round's recorded heads (`steps.review.last_review_round.heads`, see
  Re-entry) and their semantic propagation — never the upstream changes a
  default-branch merge brought into that range;
- a **true bug anywhere** — a correctness, security, data-integrity or
  concurrency defect, at any position in the change;
- a **violation of the project's own rules** — its `AGENTS.md` (else
  `CLAUDE.md`), at any position, in any round.

Everything else is recorded `non-blocking` (a follow-up `deliver` summarizes;
no step files it as a ticket). An unmarked blocking finding in a later round is
a new discovery by definition — the driver's rewind quota charges it like any
fresh one.

## Judge a light round for mis-marking

Read `execution.json`'s attempts ledger
(`runtime-state/execution.json`). When `steps.{{build_step}}.attempts`'s latest
rewind-archive attempt — namely
the latest attempt whose cause is neither `accepted` nor `infra_failure` —
carries `cause: gate_fail_rewind_text_only`, this re-run follows a light rewind:
judge the light round's changes against the text-only boundary (the step definition's
classification bullet), on top of
your normal full-change judgment (which is unchanged and stays the
adjacent-issue catcher). Anchor the light round to that record, not to
`steps.{{build_step}}.output.commits` (it lists only the resumed attempt's own new
commits — an earlier attempt's are never re-listed): the `gate_fail_rewind_text_only` attempt's
archived `result` holds the pre-light output and its `commits`, and the light
round is what changed after the last sha that list names, per task repo
(`git diff <that sha>..HEAD`). Any structural change in the light round is a
BLOCKING mis-marking finding: record it WITHOUT `text_only` (it is structural),
so the resulting FAIL rewinds fresh with its own rewind slot. A light round
that stayed text-only records no mis-marking finding — a pass is possible if
nothing else is wrong.

## Owner rulings

Before judging, read `artifacts/grill-qa.md`'s `## Owner rulings` section (the driver
appends the owner's answers to the review owner exit there, verbatim;
`steps.review.owner_rulings` is the machine copy). A **waive** ruling clears exactly the
findings it names by reference: do not re-raise those as residuals. Every prior blocking
finding it does not name stays a residual, whatever the ruling's wording. An
**amend-upstream** ruling is the owner-approved upstream correction: judge the round
against the spec as the ruling amends it, and judge whether the build step (or, for a
suite gap, the authoring step) executed the ruling. No other location records a ruling.
