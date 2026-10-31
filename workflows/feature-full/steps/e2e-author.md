---
name: e2e-author
description: Author end-to-end acceptance scenarios for the spec's NEW behaviour before code runs, commit them, and prove every scenario red (assertion-level) against the pre-implementation tree in the same step.
output:
  type: object
  required: [scenarios_path, scenario_titles, baseline]
  properties:
    scenarios_path:
      type: string
      description: Capsule-relative path to the authored e2e scenario document/spec (artifacts/e2e-scenarios.md), plus any generated test files under repos/<name>/** if the project runs its e2e suite from the tree.
    scenario_titles:
      type: array
      uniqueItems: true
      items: { type: string }
      description: Titles of every e2e scenario kept, in document order. Empty only when the approved mode is `skipped-with-rationale` (see `e2e_author_mode` below); otherwise at least one - zero scenarios means the new behaviour was not covered. Titles must be UNIQUE (uniqueItems) - `verification` echoes this list verbatim as its authored_titles cross-check under its own uniqueItems constraint, and a duplicate title cannot key the 1:1 scenario-to-result correspondence.
    test_files:
      type: array
      items: { type: string }
      description: repo-relative paths of any generated test files, if the project's e2e tests live in the tree rather than purely as a scenario document.
    baseline:
      type: array
      items:
        type: object
        required: [title, status, evidence]
        properties:
          title: { type: string }
          status: { type: string, enum: [fail] }
          evidence: { type: string, minLength: 1 }
      description: "The baseline run against the pre-implementation tree - one entry per scenario_titles entry, same order. Empty only when the approved mode is `skipped-with-rationale`; otherwise at least one. Every kept scenario is an assertion-level red (`fail`); a scenario that passed at baseline was deleted (already_satisfied) or rewritten before completing. The one exception is the all-green report: EVERY scenario passed, every row then says `pass` (outside the enum on purpose - the driver never accepts it as a completion and asks the owner instead, see Outcomes). evidence carries the failure proof - a runner output excerpt or an observed-vs-expected contrast."
    bindings:
      type: array
      items:
        type: object
        required: [title, repo, command, unit, marker]
        properties:
          title: { type: string, description: "One of scenario_titles, verbatim." }
          repo: { type: string, description: "The task repo the command runs in - the bare repos/ child name." }
          command: { type: string, description: "The command that runs the scenario's test, exactly as it is run inside that repo (no leading cd) - normally your targeted baseline command." }
          unit: { type: string, description: "The test unit the scenario lives in, as the command's output names it - a test function, a node id, a suite title." }
          marker: { type: string, description: "A regular expression (RE2) that one line of the command's output matches only when this scenario passed, e.g. `^PASS S17$` or `^PASS: MP3`." }
      description: Optional - one entry per scenario whose outcome a command's output shows mechanically (several titles may share one command and unit, each with its own marker). verification resolves a bound title from the command's run alone; a title without a binding is document-only and verification judges it. The driver rejects a binding whose title is not in scenario_titles, whose fields are empty, or whose marker does not compile.
    already_satisfied:
      type: array
      items: { type: string }
      description: Titles of scenarios deleted because they passed at baseline AND test behaviour the tree already has (existing behaviour, not new). Review checks each one.
    added_test_lines:
      type: integer
      description: Lines added to test files by your commit (git diff --numstat of the author commit over test_files, the added column summed). 0 for a scenarios-document-only step.
    size_justification:
      type: string
      description: Required when added_test_lines is above the 400-line soft cap - why the suite cannot be smaller. Review judges it.
    e2e_author_mode:
      type: string
      enum: [required, skipped-with-rationale]
      description: The resolved mode - the owner-approved one recorded in artifacts/grill-qa.md, or the lane default `required` when no owner approval is recorded. `required` (the default) authors at least one scenario; `skipped-with-rationale` authors none and records skip_rationale.
    skip_rationale:
      type: string
      description: The grill-approved rationale, required in substance when e2e_author_mode is `skipped-with-rationale` - why this work item is internal-mechanism / non-behaviour-face and needs no authored e2e.
---

# e2e-author

Write end-to-end acceptance scenarios for the NEW behaviour in `artifacts/spec.md`'s
`## Acceptance criteria` and `#### Scenario` blocks — not against `code`'s
implementation — commit them, and prove them red against the pre-implementation tree.
This step runs BEFORE `code` (step 5 of the reference graph, `code` is 6): authoring
happens pre-code, against the spec alone, so the scenarios never silently encode
whatever `code` later happens to build; they verify intent, not implementation. The
baseline run is yours too: one agent writes the tests and shows they fail for the right
reason, so a mis-wired suite is fixed by its author instead of stalling the flow.

## e2e-author mode

The lane default is `{{e2e_author_mode}}`; this run's mode is the owner-approved one
`artifacts/grill-qa.md` records, and without that record the default applies
(fail-closed). Resolve the mode like this:

- **`required`** (the default, and the fail-closed fallback) — everything below
  applies unchanged: at least one scenario, every kept scenario proven red.
- **`skipped-with-rationale`** — honoured ONLY when `artifacts/grill-qa.md` records
  the owner's explicit approval of the skip; with no such record, or with the owner's
  veto, the mode is `required`. When it is honoured: author no test files; write
  `artifacts/e2e-scenarios.md` recording the mode and the grill-approved rationale;
  write the handoff `artifacts/handoff-e2e-author.md`; run and record the command
  evidence — the read of the owner-approved mode out of `artifacts/grill-qa.md` (e.g.
  `grep -nE 'e2e-author mode|skipped-with-rationale|approved' artifacts/grill-qa.md`);
  commit nothing; and complete with `e2e_author_mode: "skipped-with-rationale"`,
  `skip_rationale`, empty `scenario_titles`, empty `baseline` and empty `test_files`.
  A skip is legal only for an internal-mechanism / non-behaviour-face work item; a
  behaviour-face work item stays `required`.

## Scope: new behaviour only (size soft cap)

- Write one runnable scenario per acceptance criterion that describes **new**
  behaviour — at most one scenario per new-behaviour criterion. Criteria that describe
  existing behaviour or are only regression protection get no scenario; behaviour an
  existing test already covers is not tested again.
- Reuse the repo's existing e2e conventions, fixtures and helpers — inspect the repo for
  an existing e2e test directory/framework before inventing anything.
- **Soft cap: 400 added test lines.** Report `added_test_lines` (`git diff --numstat
  <author-commit>^ <author-commit> -- <test_files>`, the added column summed). Above
  400, fill `size_justification` with why it cannot be smaller (e.g. every scenario
  needs its own long flow); `review` judges it. The cap is not a refusal: stay under it
  by testing only what is new.
- Do not write unit tests for implementation internals — those belong inside `code`'s
  own plan-step commits; this step is end-to-end/acceptance level only.

## What to do

1. Read `artifacts/spec.md` in full and pick out the new-behaviour acceptance criteria.
2. For each, author a scenario: as a structured document in `artifacts/e2e-scenarios.md`
   (GIVEN/WHEN/THEN form, mirroring the spec's own scenario grammar) and/or as runnable
   test files under `repos/<name>/**`, following the project's existing e2e convention.
3. Do not read or depend on `code`'s output; if a scenario requires information only
   `code` would know (an internal API shape that was never part of the spec), that is
   a spec under-specification — flag it in `artifacts/e2e-scenarios.md` rather than
   reverse-engineering it from an imagined implementation.
4. **Commit** the generated test files (see the commit duty below).
5. **Baseline run.** On the committed HEAD — the pre-implementation tree — run ONLY the
   authored test files with a targeted command (e.g. `go test ./test/ -run
   '<authored test names>'`, or the project's runner pointed at those files). Never the
   project's full suite (`go test ./...`, `make test`, the full `./test`, `npm test`):
   the full bar runs once, in `verification`. A document-only scenario is driven by
   hand through the project's actual interfaces (CLI, API, config) and judged against
   its THEN clause.
6. Classify every scenario and act on it in this same step:
   - **Assertion-level red** — keep it: this is the legal baseline.
   - **Build, wiring or environment error in the test** — fix the test, fold the fix
     into the same commit (`git commit --amend`, test files only), and go back to 5.
   - **Passes at baseline** — the scenario does not test new behaviour. If the
     behaviour already exists, delete the scenario and list its title in
     `already_satisfied`; if the assertion is vacuous, rewrite it. Amend, go back to 5.
7. Record the run in `artifacts/e2e-scenarios.md` under a `## Baseline run` section,
   one entry per kept scenario in document order, with the evidence; fill `baseline`.

## Bindings (how verification reads each scenario's result)

`verification` runs the whole bar in one mechanical command and then judges only what that
run cannot decide. Give it a `bindings` entry for every scenario whose outcome a command's
output can show by itself: the `repo`, the `command` (normally your targeted baseline
command, run inside that repo without a leading `cd`), the `unit` the scenario lives in as
the output names it, and a `marker` — a regular expression one output line matches only
when THIS scenario passed. When several scenarios share one test or one suite file, make
the test print one marker line per scenario as it passes (e.g. `PASS S17`, matched by
`^PASS S17$`) and bind each title to its own marker. The command must print that line on a
passing run (a test runner that shows a passing test's output only in verbose mode needs
its verbose flag in the command). At the baseline run no marker may match (every bound
scenario is red); check it. A scenario without a binding is document-only: `verification`
judges it itself. The driver rejects a binding whose title is not in `scenario_titles`,
whose fields are empty, or whose marker does not compile.

## Executability against the pre-implementation tree

Generated test files MUST compile against the tree as it stands at authoring time:
drive the spec'd behaviour through interfaces that already exist (CLI, API, config —
the project's e2e convention) and never reference symbols that do not exist yet, so the
baseline red is assertion-level, not build-level. A scenario that genuinely cannot be
expressed without not-yet-existing surface is flagged in `artifacts/e2e-scenarios.md`
as a spec under-specification (do not fake it) or authored as a document-only scenario
driven through the project's actual interfaces. Never write an unrunnable file.

## Commit duty for generated test files (hard rule)

If you generated test files under `repos/<name>/**`, you MUST commit them yourself
before completing this step: **ONE dedicated commit** containing exactly those files —
the exact paths you report in `test_files`, staged by path, no unrelated files in the
same commit. Baseline fixes are amended into that same commit; it must be final before
you write the result envelope. `code` is forbidden from touching these files, and a
resume of the work item restores each task repo at its committed HEAD only, so a test
file left uncommitted is lost on the next resume. Commit BEFORE writing the result
envelope (the step process is torn down once it appears); `git add -f` a path under a
.gitignore'd directory. The driver enforces this mechanically: a `completed` result
whose `test_files` lists a repo-relative path that exists in no task repo, or is not
committed at HEAD in every task repo where it exists, is rejected (and the step
re-dispatched) with the uncommitted paths named; `verification` additionally FAILs on
any listed path that is not tracked in the repo worktree. A scenarios-document-only
step (no generated test files) commits nothing.

**Formatter self-check before the commit is final.** Run the repo's formatter check
(e.g. `gofmt -l` for Go, or the repo's equivalent) on exactly the files you report in
`test_files` — no other file — before the dedicated commit is final (re-check after
every amend). A formatter listing any file (non-empty `gofmt -l <test_files>` output,
or the repo's equivalent) counts as red, exactly like a non-zero exit. If it is red,
format or fix those files and re-check until it is clean, folding the fix into that
same commit (`git commit --amend`, test files only), so the committed content is
formatter-clean and nothing is edited outside that commit. Once the step completes,
the files are frozen by the authored-tests gate, and a later formatting fix needs a
human-approved rebaseline. No `test_files`, no check.

## No orphan from an all-green run

An all-green run already made its one dedicated commit, and the driver then asked the
owner. Every later run of this step on the same work item — the owner's `skip`, or the
fresh run after `rewind-to-spec` — starts by reverting that commit in each task repo
where it landed: find it with `git log --format='%H %s' $(git merge-base HEAD
origin/<default>)..HEAD -- <its test files>` and run `git revert --no-edit <sha>`. A
run that authors anew then makes its own one dedicated commit as above. Either way, every
authored test file in the merge-base diff is one this run lists in `test_files`; a file
an earlier run committed and this run does not list is an orphan that plan conformance
blocks as undeclared.

## Owner-approved suite amendment (append-only)

When your `pending_rewind_input` carries the owner's amend-upstream ruling for a gap in
the authored suite (its note says append-only), you re-run in append-only mode: every
existing scenario, test file line and title stays byte-identical; append and title only
the new scenarios the ruling names, prove each new one red, commit only the additions,
and complete with the full `scenario_titles` list (the existing titles first, then the
new ones) — the driver takes that list as the new baseline. Spec and plan are not re-run.

## Outcomes

- **completed** — at least one scenario, every kept scenario an assertion-level red
  with evidence in `baseline`.
- **completed (skipped)** — the owner explicitly approved `skipped-with-rationale`
  in `artifacts/grill-qa.md`: the scenario document records the mode and rationale,
  `scenario_titles` and `baseline` are empty, no test file was authored or committed,
  and `command_evidence` carries the one command that read the approval (see
  "Command evidence" below).
- **completed (all green)** — EVERY scenario passes at baseline: the behaviour the spec
  claims is new is already in the tree. Complete with every `baseline` row `status:
  pass`; the driver does not advance to `code` but asks the owner: `rewind-to-spec:
  <words>` (an owner-routed rewind to `spec`), `skip: <rationale>` or `cancel`.
- **completed (skipped on the owner's `skip` answer)** — your answers carry the owner's
  `skip`: first revert the all-green run's own authoring commit (see "No orphan from an
  all-green run" below), so the merge-base diff carries no authored file; then append the
  owner's rationale verbatim to `artifacts/grill-qa.md` (a `## e2e-author
  skip` block naming the answer), record it in the scenario document, and complete with
  `e2e_author_mode: "skipped-with-rationale"`, that `skip_rationale`, empty
  `scenario_titles`/`baseline`/`test_files` — the named discipline-① exception for
  behaviour already on main; `review` and `verification` honour it from that record.
- **needs_input** — only when the authored tests cannot run in this environment at all
  (e.g. a missing browser dependency) — name what is missing.
- Never report `completed` with a mix of passing and failing rows, or an un-runnable
  scenario, in `baseline`.

## Command evidence (hard gate)

This step declares `requires_command_evidence: true`: a completed result must carry
`command_evidence` for the commands you actually executed (exact argv, real exit code,
measured duration). The driver rejects a completion without it. Never fabricate an
entry.

- **Mode `required`.** Evidence for the baseline run(s) you actually executed — the
  targeted command(s) that ran the authored scenarios against the pre-implementation
  tree.
- **Mode `skipped-with-rationale`.** No baseline exists, so record the one command the
  skip really runs: the command that reads the owner-approved mode, its rationale and
  the owner's explicit approval out of `artifacts/grill-qa.md` (e.g.
  `grep -nE 'e2e-author mode|skipped-with-rationale|approved' artifacts/grill-qa.md`)
  — the same artifact the mode resolution must read anyway (the audit anchor downstream
  honours). It runs for real; record its real exit code and measured duration.

## Handoff

This step declares `handoff: true`: `code` (the next step, `context_policy: inherit`)
continues from your handoff file. Write `artifacts/handoff-e2e-author.md` with the
test locations, the targeted command that runs them, the red bar `code` must turn
green, and the marker each bound scenario prints when it passes.

## Principles

Run the baseline in the foreground with an explicit timeout as long as it needs; await anything it starts inside that same call. The red baseline is evidence only when your own run produced it.

{{principles}}

## Output

Emit the final JSON object per the schema above: the scenario document path, the
ordered scenario titles, any generated test file paths, the `baseline` (one `fail`
entry per title, same order, with evidence), the `bindings` of every mechanically
observable scenario, `already_satisfied` if you deleted any scenario, `added_test_lines`,
and `size_justification` when above the soft cap.
