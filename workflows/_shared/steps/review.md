---
name: review
description: Human-judgment gate over the completed change - the lane's own conformance judgment (from the workflow.yaml step entry's vars) plus the fixed owner-rules checklist - after the build step, before the machine gate and delivery. Shared by every delivery bundle with a review.
---

# review

{{intro}}

## What to do

{{judge}}

Then, on every lane:

- Run the owner-rules checklist below — a violation of any AGENTS.md rule
  blocks at any position, in any round; any other checklist hit blocks on the
  first round when it falls in a blocking class (on a later round the round contract
  your skill states narrows that set).
- Record every finding in `artifacts/review.md`, numbered and tagged `blocking` or
  `non-blocking`; a blocking one names its `class`.
  Non-blocking findings (style nits, minor improvement suggestions,
  follow-ups outside the round's blocking set) do not fail the gate but are
  still useful history — `publish` lists every `non-blocking` item in the delivery note
  and the PR body, and no step ever files them automatically: the owner decides what
  becomes a ticket. A missing prior-experience note in the build's hand-off is never a
  blocking finding on its own — record it `non-blocking` if noted at all; it is a form
  gap, not a defect in the change.
- Pass (hand off `done`) only with zero blocking findings.
- Fail (hand off `fail`) with at least one; synthesize them — concrete and actionable
  enough that `{{rework_step}}`'s next round does not have to guess what to change — in
  `artifacts/review.md` and your hand-off's 发现的问题和风险. A FAIL sends the lane back to
  `{{rework_step}}`, which resumes its previous session and reads your section first.

**Do not run tests or builds.** This step reads and judges; it never runs the build,
the test suite (`go test ./...`, `make test`, the full `./test`, `npm test`) or an
e2e suite — the one exception is the pre-written commands your skill names, which
compute the facts (the units' red runs replayed at their test commits included).
The machine gate is `verification`, the step AFTER you (after `sync`): it runs the bar its
tier declares on the tree you judged merged with the latest origin default branch, and
`publish` pushes exactly the head it verified — no step re-runs it. You have no
verification report to read — this lane's order is `{{rework_step}}` → review → sync →
verification → publish → merge, so judge the diff itself: reading a test file's source is
part of the review; running it is not.

This review always runs **fresh**. When `artifacts/handoff.md` carries an earlier `review`
section, this is a later round: your skill's two faces and round contract apply. Without
one this is the first round — the full judgment, every checklist hit blocking.

## Blocking classes

A blocking finding names one class; a hit that fits none is non-blocking, whichever rule
found it (publish lists it in the PR body).

- **`bug`** — a correctness, data-integrity or concurrency defect, or a security defect
  that `## Security scope` lets block.
- **`ac_gap`** — an acceptance criterion with no test, a vacuous or wrong test for it, or
  one the change does not meet; an owner's reply to the build's questions (its
  `pf step ask`, quoted verbatim in the build's section headers) that the acceptance
  criteria or the change ignore.
- **`project_rule`** — a violation of the repo's `AGENTS.md` (else `CLAUDE.md`), a coupled
  document it names left stale included.
- **`test_weakened`** — a test relaxed, skipped, deleted or rewritten to pass; a helper,
  fixture, input or `carries:` header changed so a test pins less; a new test that passes
  on the old code; an e2e scenario waiting by a fixed delay instead of polling (a bounded
  poll deadline is fine) or asserting before the awaited state settles; a suite the
  verification-scope engine discovers (by default or `suite_glob=`) whose `carries:` header
  is missing or does not name the paths it exercises.
- **`guarantee`** — a feature removed or a guarantee weakened without an owner ruling, a
  manual-gate bypass, a spec-forced invention, user-visible wording that contradicts the
  behaviour; a deviation from the ticket, or an acceptance list the build wrote itself,
  with no owner reply on record, or a reading of the owner's reply its words do not
  support (develop: the build's 关键决定和理由 against the owner's quoted replies) — a
  decision the build took for the owner.

{{review_security}}

## Principles

Every principle is a check; a violation blocks when it falls in a blocking class.

{{principles}}

## Owner-rules checklist (first round: a classed hit blocks; later rounds: the round contract)

On top of the lane judgment above, check the diff against the owner's
fixed rules; each rule names its class, and an unclassed hit is non-blocking in every
round. On the first round any classed hit is a blocking finding; on a re-entry
the round contract decides — a violation of any AGENTS.md rule blocks at any
position, and every other hit outside the contract's blocking set is recorded
`non-blocking` (a follow-up):

- **No faked concurrency semantics.** (`bug`) No timing-based fakes (`time.Sleep` as a
  synchronization stand-in, backoff tuned to "usually long enough") and no
  in-process locks/mutexes standing in for the cross-process or distributed mutual
  exclusion the semantics actually require.
- **No unrequested concepts.** (unclassed, unless it weakens a guarantee) No new states,
  fields, error codes, config items, or concepts {{scope_source}} did not request. List
  each one you find as its own finding.
- **No manual-gate bypass.** (`guarantee`) No code path that bypasses a manual checkpoint
  (`pf step ask` / gate) - the flow's interactive checkpoints must stay
  load-bearing.
- **Spec-forced inventions.** (`guarantee`) If any of the change's own requirements
  ({{scope_source}}) forces an invented mechanism - e.g. it demands "the loser
  must be refused" where both serializations are valid, which is what drove a sleep+latch hack - do not pass
  the diff for faithfully implementing it: flag it as a blocking finding and
  propose the spec change in that finding instead.
- **Unruled destination change.** (`guarantee`) A feature removed or a guarantee weakened without an owner ruling (a shorter path with the same destination is not a finding).
- **I/O under a lock.** (`project_rule` where `AGENTS.md` forbids it, `bug` when it can
  stall) Network or disk I/O while holding a lock.
- **Weak tests.** (`test_weakened`) A new test that claims to pin new or changed
  behaviour yet passes on the old code (a characterization test of existing behaviour is
  exempt), or an assertion relaxed without a stated reason.
- **Stale docs.** (`project_rule` for a coupled document `AGENTS.md` names, `guarantee`
  for any other document the doc-impact check covers) The doc-impact check: when the
  change alters user-visible behaviour, a configuration key, a command or a step
  definition, the documents that describe it change in the same diff — the repository's
  README, its reference documentation, its upgrade notes (appended to, never rewritten,
  where the repository keeps them append-only) and its skill documents. One left out of
  sync (wording the change contradicts, a removed or renamed command or key it still
  names, a new one its reference omits) blocks, naming the document and what it misses.
  Record in `artifacts/review.md` every document you compared. Other docs not updated
  with the code they describe are unclassed.
- **Leaked credentials** (`bug`) in an agent's env, a command line, a log or an artifact.
- **Guess as fact.** (unclassed, unless it is wrong user-visible wording or a wrong
  assumption in code) An unverified claim stated as fact.
- **Prior experience ignored.** (`bug` when the repeated pitfall is a defect) The change
  repeats a pitfall or a failed approach that a recalled memory records, without saying why this time differs.

## Supply-chain checklist (outside code)

This section applies only to a change that brings in code written outside the team (an
absorbed external pull request, vendored third-party source, a copied upstream patch); on
any other change it does not apply and asks nothing. On such a change, read the diff for
these four readings, and judge each hit:

1. **New scripts and install hooks** — added or changed executable files, shell, build and
   CI scripts, package lifecycle hooks (`preinstall`, `install`, `postinstall`, `prepare`).
2. **New network endpoints** — URLs, host names and IP addresses the diff adds.
3. **Dependency diff** — every dependency added, removed, or moved to another version or
   source.
4. **Upstream consistency** — only when the change claims a specific upstream: the claimed
   commits are ancestors and the rest of the diff is what the change says it adds; with no
   claimed upstream it is N/A, never a finding.

A hit explained on record (the work item, the plan or the change itself says why it is
there, and the diff matches) gets a `non-blocking` finding naming it, so the record shows
it judged. An unexplained hit is `blocking` unless an owner ruling on record covers
it: a recorded owner ruling is the only exit, and this step never waives one. Its class is
`bug` (a security defect until explained).

## Position in the flow

In every delivery bundle that has a review this step sits between the build step
(`{{rework_step}}`) and `verification`: the judgment runs first, so a quality
failure never costs a full machine bar, and the machine gate behind you verifies
the tree you already judged merged with the latest origin default branch. The daemon
stamps the heads you judged on your hand-off section, and `sync` starts from exactly
them; any code change after your pass invalidates it, and the delivery needs both
coverages — the review of the final diff and the verification of the final tree.
