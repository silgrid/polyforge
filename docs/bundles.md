# Bundles — authoring a workflow bundle

The bundle-authoring chapter: what a workflow bundle is, the format of
its files, and a worked custom-bundle example whose every file
is the real format — the acceptance test
(`internal/polyforge/driver/docs_example_test.go`) runs this page's
example through the same loader the daemon runs bundles with, so the
documented example cannot drift from the actual format. What a bundle IS (the concept) is
defined on the [concepts page](concepts.md); the model-candidate
format is the [config reference](reference/config.md)'s
(`steps:` section) — one authority.

## The layout

A bundle is a directory under the daemon's workflows dir:

    workflows/
      _shared/               # not a bundle: the shared material
        principles.md       # the engineering principles every step renders
        <name>.md           # shared fragments: blocks several step files
                            # repeat, included by {{<name>}} (no vars entry)
        step-header.md      # the header every step prompt carries: read
                            # handoff.md first, end with pf step handoff
        handoff.md          # the hand-off body's seven-section template
        steps/<id>.md        # shared step definitions
        skills/<name>.md     # shared skills
      _schema/               # not a bundle: the lane file's JSON Schema
        workflow-5.schema.json
      develop/               # a shipped bundle
        workflow.yaml        # the lane: the steps, their order, budgets, candidates
        README.md            # the lane's narrative (the daemon never reads it)
        fragments/<name>.md  # long vars, read through vars_files
        steps/<id>.md        # one step definition per step id
      ...

A work item runs the bundle it was filed with (`pf file create --bundle
<name>`). A step with no file of its own in the bundle falls back to the
shared `workflows/_shared/steps/<id>.md` beside the bundles (the
bundle's own file always wins; `internal/common/workflow` is the
loader). The daemon loads the bundles from its `--workflows-dir`, which
`pf daemon ensure` sets to `<data-dir>/release/current/workflows` — the
installed release's copy of this repository's `workflows/`. Each
installed version is its own directory, so a bundle added there by hand
does not survive the next install; ship a bundle by adding it under
`workflows/` here and releasing.

A workspace may also keep bundles of its own — user lanes — under
`<workspace>/.polyforge/workflows/<name>/`, in the same format: every work
item of a project that workspace maps resolves its bundle among the
installed bundles plus that workspace's own (a user lane never takes an
installed bundle's name, falls back on the
installed `_shared/` — a
workspace has none of its own — and is snapshotted into the claimed
capsule like any lane). The rules are the [workflow contract
reference](reference/workflow-contract.md#user-lanes)'s.

The shipped bundles: `develop` (the default delivery lane: `build` →
`review` → `sync` (`sync.py` merges the default branch into the branch; the
sync agent resolves a real conflict in place) → `verification` (`verify.py`,
the light tier; the lane's one waivable gate) → `publish`, which opens the
PR → `merge` (`merge.py` merges it into the default branch under the
per-repo merge lock; a project may skip it, `steps.<project>.merge.skip:
true`, and its work items then end after `publish`, wrapped `published`
with the PRs awaiting a human review) — the four mechanical steps agent steps on shared
skills since workflow contract 2;
[concepts](concepts.md#landed-awaiting-the-full-gate)), `integrate` (the integration
lane: one work item per batch — `collect` → `revert` → `full-gate` → `close`,
four agent steps running pre-written commands, gating what landed, filed
by a person with `pf file create --bundle integrate` and `pf submit`, one
unfinished batch per project (`exclusive: true`);
[concepts](concepts.md#the-integration-lane)),
`micro` and `release` (the project-agnostic release flow). The four delivery
lanes `develop` superseded were removed by
(`workflows/_archive/README.md`).
`micro` is the pure-text lane: `fix` → `review` → `sync` → `evidence` →
`publish` → `merge`, no grill, plan or verification step, the ticket body as
its spec — every step an agent step since workflow contract 2, the four
mechanical ones on the same shared skills as develop's or its own:
`evidence` re-runs fix's evidence plan (`run.py --isolation host --plan-from
artifacts/fix/evidence.json`, the repository's fast phase and declared
micro checks, on the host; the lane's one `waivable`
gate) and `merge` runs `merge.py`. A
repository opts in by declaring `micro_path=` lines (and optional
`micro_check=` commands) in its own `.polyforge/verify-scope.conf`
([config reference](reference/config.md#polyforgeverify-scopeconf--the-verification-bar));
the workflow's `facts.py micro-bounds` judges every changed path against the declaration at
the diff base (`AGENTS.md`/`CLAUDE.md` never in bounds) — `fix` runs it
before completing and `review` runs it again — and a diff that leaves it,
or touches an undeclared repository, is not micro's: `fix` asks the owner
to re-file the work on `develop` and cancels the work item on the owner's
word.
Their own `README.md` files carry each lane's narrative. Every step of
every lane is an agent step (workflow contract 3: the daemon runs no step
itself); develop's verification runs `verify.py --tier light --unit-runs
artifacts/build/units.json` through its skill (the build's recorded unit runs
ride along); there is no per-work-item tier. develop's `build` and `review`
follow skills too: build records each unit's red-then-green run in
`artifacts/build/units.json`, and review runs the workflow's `facts.py` (the units' red runs
replayed at their test commits) under the heavy-run admission.

## workflow.yaml — the lane

The steps, their order, their budgets and their candidates are declared
in the lane's `workflow.yaml`, one YAML mapping — its `steps` array is the
ONLY execution order; the lane's `README.md` is narrative the daemon
never reads. A lane's `workflow.yaml` may open with the line
`# yaml-language-server: $schema=../_schema/workflow-5.schema.json`, so
an editor completes and checks its keys against the JSON Schema the
release generates from the contract registry (`workflows/_schema/`;
a user lane points at the installed release's copy); the loader stays
the judge. The normative contract — every key, the rules the loader judges,
the `WFC-*` error codes, the step interface and how a contract version is
deployed — is the [workflow contract
reference](reference/workflow-contract.md); this chapter walks through the
keys. The daemon reads a lane through one strict loader: an unknown or
removed key at any level is refused, and `pf workflows validate
<lane-dir>` prints every defect a lane has, each with its code, file:line
and YAML path, before a claim ever meets it. The keys:

- `contract: 5` — the workflow contract version the lane targets; a lane
  of another version is refused (`WFC-101`) — a lane that is still a
  `workflow.md` (contract 4 and earlier) among them, with its rewrite:
  the frontmatter becomes `workflow.yaml`, the body `README.md` — and
  each key contract 4 removed — `inputs`, `verifies`, `outputs`,
  `verdicts`, `effects`, `handoff`, `attrs`, `on_fail.charge` — is
  `WFC-111` naming its rewrite; contract 5's removed `integration` marker
  is an unknown key, `WFC-111`.
- `name`, `description` — the lane's identity: `name` is the lane's
  directory name.
- `max_rewinds:` — ONE total rewind budget across every gate→target edge
  (`max_rewinds: 3`); required when a step declares `on_fail`, refused when
  none does. Every automatic FAIL counts toward it; a FAIL the gate hands
  off citing the owner's message (`--per-owner`) counts toward nothing.
  `0` makes every gate FAIL an owner's call. With the budget spent a gate
  cannot hand off a plain FAIL: it asks the owner first (`pf step ask`)
  and hands off what the owner decided.
- `exclusive:` — `true` holds the lane to one unfinished work item per
  project (the shipped `integrate` declares it): the daemon forwards a
  create on the lane to aihub exclusive, and aihub refuses it `CORE-409`
  `OPEN_ON_BUNDLE`, naming the open work item, while another of the project
  on the lane is not wrapped, failed or canceled. Default `false`; a user
  lane may declare it too.
- `acceptance:` — whether a work item on the lane must list an acceptance
  criterion (`## Acceptance` or `## 验收`) before it runs. `required` is the
  default (not declaring it is the same): `pf submit` refuses an item
  without one (unless `--allow-no-acceptance`), the daemon's auto claim
  skips it, `pf queue` marks it as needing interaction, and `pf file
  create` prints which step carries the criteria. `none` is a lane with no
  criterion to list: none of the four applies. The shipped `integrate` and
  `release` declare `none`; `develop` and `micro` do not. A user lane may
  declare it too; any other value is `WFC-112`.
- `defaults:` — `model_candidates`, the workflow-level fallback chain
  (the format and the whole precedence chain: the
  [config reference](reference/config.md)), and `tiers`.
- `steps:` — one entry per step, in order. Each entry:
  - `id` — the step id (the step definition file's name).
  - `gate: true` — the step can FAIL the run: it ends its dispatch with
    `pf step handoff --status done` (pass) or `fail`. `on_fail: { target:
    <earlier step> }` names the rewind target. Every step starts from a
    clean context, except a rewind's target, which resumes its own
    previous session when it can. The delivery lanes order their gates
    review → verification (the judgment first, then the machine bar on the
    merged tree), and the merge rule needs both coverages — the review of
    the final diff (the end commits stamped on review's `handoff.md`
    section) and the verification of the final tree (the `heads` of
    verification's result file). micro has no verification step: its
    verified head is the `heads` of its `evidence` step's result file
    (`artifacts/run/result.json`). On both shipped lanes `publish` checks
    that result file (or the owner's waiver) first and pushes the head it
    names, and `merge`'s `merge.py` merges only a PR whose head is that
    verified head. A work item that declares an `owner-review:` artifact
    makes publish ask the owner before merging, showing the declared
    material; 「过目通过」 lets the merge proceed and 「不通过: …」 rewinds
    to the build step.
  - `waivable: true` — a gate whose FAIL the owner may waive: its agent
    hands off `--status waive` citing the owner's message.
  - `skippable: true` — a step a project may switch off with
    `steps.<project>.<step-id>.skip: true` in its workspace yaml (the
    [config reference](reference/config.md)): fixed when a work item is
    first claimed, the step is then never dispatched (its execution status
    `skipped`), and the wrap attrs of skipped steps at the lane's end pass
    to the last step that runs. A step not declaring it always runs; the
    shipped lanes declare it on develop's `merge` alone (micro's `merge`
    runs whatever the switch says).
  - `skill:` — the skill the step follows: `<lane>/skills/<name>.md`, else
    `_shared/skills/<name>.md`, rendered after the step definition; a skill
    names the workflow scripts it runs (`commands:`, by file name), each line
    run as written, and says what the step reads and checks before it acts
    and what its hand-off carries.
  - `hub:` — `{verbs, attrs}`: the `pf step hub` verbs the step may call
    (`get`, `list`, `file`, `wrap-attrs`, `batch`) and the top-level attrs
    keys it may write with `pf step hub attrs`: the aihub reads it makes
    and the writes it leaves pending for the daemon. A step with no `hub:`
    reaches aihub only for memories.
  - `resources:`, `budget.tool_minutes`, `model_tier` — the heavy-run
    admission and merge lock a dispatch takes, the step's own tool window
    and its tier: the [reference](reference/workflow-contract.md#step-keys).
  - `model_candidates` — the step-level override of the chain.
  - `vars:` — the map the step definition's `{{placeholders}}` render
    with: one lane-specific fact per var, so one shared step text
    carries each bundle's own facts.
  - `vars_files:` — a var whose text is long lives in a file of the
    lane: `vars_files: { judge: fragments/review-judge.md }` reads the
    file (relative to the lane directory, inside it) as the var's value,
    exactly as if it were written in `vars`. A var is declared in one of
    the two, never both (`WFC-112`).
  - `budget: { wall_minutes: N }` — the step's wall-budget estimate;
    the step-timeout bound T becomes `max(3 x wall_minutes, T)`.

A step learns what came before by reading `artifacts/handoff.md`, the
work item's one append-only hand-off document, from the top: every
earlier step's section (a daemon-stamped header over the body the step
handed off) and the daemon's rewind events. It ends with exactly one `pf
step handoff`, whose body follows the shared seven-section template
(`_shared/handoff.md`). The daemon renders the same step header into every
prompt (`_shared/step-header.md`), so a step definition never repeats it.

## steps/&lt;id&gt;.md — the step definition

Frontmatter: `name` (the step id) and `description`; no other key. The
body is the step's prompt: what the step agent does, its hard rules, what
it reads first and what its hand-off carries. Two placeholder forms
render before the agent sees it — the loader renders every definition
when it loads the lane, so a placeholder that does not resolve is a load
error (`WFC-195`), never a dispatch failure:

- `{{<name>}}` with a `workflows/_shared/<name>.md` beside the bundles
  — a shared fragment: a block several step files repeat lives once in
  the fragment and every step file includes it (`{{principles}}`, the
  engineering principles every bundle shares, is one). No `vars` entry
  declares it; the loader renders the fragment's trimmed text, verbatim
  and never re-scanned. A declared var that shadows a fragment file is a
  load error.
- `{{<name>}}` — any other placeholder renders with the workflow.yaml
  step entry's `vars` (and `vars_files`). Rendering fails closed both ways: a
  placeholder with neither a var nor a fragment file, and a var no
  placeholder uses, are both load errors — a typo cannot ship a
  half-rendered prompt.

## The worked example: a custom `apidocs` bundle

A repeated flow — "regenerate this repo's API reference docs" — walk it
once on a delivery lane, then sediment it into its own small bundle.
Three steps: scan the surface, regenerate the reference, verify the
build. The `workflow.yaml` (its `steps` array is the execution order):

```
contract: 5
name: apidocs
description: Regenerate a repo's API reference docs from its code - scan the surface, regenerate the reference, verify the build. A small custom lane for a repeated docs flow.
max_rewinds: 2
defaults:
  model_candidates: [pi:prov/model-a, pi:prov/model-b]
steps:
  - id: scan
  - id: regen
  - id: verify
    gate: true
    on_fail: { target: regen }
    model_candidates: [pi:prov/model-b, claude:claude-opus-5-5]
    vars:
      checks_file: artifacts/verify-checks.md
```

Its `README.md` tells the flow for a reader: scan writes
artifacts/scan.md, which regen reads by that path; verify gates the build
and rewinds to regen on FAIL; every step reads artifacts/handoff.md first
and ends with pf step handoff.

The step definitions, one file per step id — `steps/scan.md`:

```
---
name: scan
description: Scan the repo's API surface and write artifacts/scan.md - every public entry point the reference must cover, with file paths.
---

# scan

You run first in this lane. {{principles}}

Scan the repo worktree declared for this work item (repos/<name>/)
for its API surface: every public entry point the reference docs
must cover. Write the findings to artifacts/scan.md, one section per
area, each entry naming its file path and its exported shape.

Work only inside the capsule and commit nothing. Hand off done with
artifacts/scan.md as evidence; your conclusion names the areas found.
```

`steps/regen.md`:

```
---
name: regen
description: Regenerate the repo's reference docs from the scan's findings, one compilable commit per regenerated page.
---

# regen

Read artifacts/handoff.md, then the scan's findings (artifacts/scan.md).
Regenerate the reference docs the repo owns from the worktree's code,
keeping every page's existing shape, and commit each regenerated page as
one compilable unit whose message names this lane.

On a re-run, answer each point of verify's FAIL in your body's reply to
the previous round. Hand off done when the build is green; the daemon
stamps your commits on the section.
```

And the gate, `steps/verify.md` — the `vars` of its workflow.yaml entry
render into it:

```
---
name: verify
description: Gate the regenerated reference - run the repo's build and doc checks on the tree the regen step left.
---

# verify

You are the lane's gate. {{principles}}

Read artifacts/handoff.md and check that the tree is at the end commits
stamped on regen's last section. Run the repo's own build and doc checks
(the full bar its CI runs) on it, each in the foreground, and list each
command with its exit code in {{checks_file}}.

Hand off done with {{checks_file}} as evidence when every check passes;
otherwise hand off fail, naming each failing check: the run rewinds to
regen.
```

Check it with `pf workflows validate workflows/apidocs` (the daemon's own
loader: every defect at once). Ship it by adding `workflows/apidocs/`
under this repository's `workflows/` and releasing — the next install materializes it beside
`_shared`, and only then does `pf file create --bundle apidocs` resolve
it. Nothing here installs or ships with a release by itself. To keep it
in one workspace instead, place the same files at
`<workspace>/.polyforge/workflows/apidocs/` (a user lane): the work items
of the projects that workspace maps resolve it at their next claim, and
`pf workflows validate` in the workspace judges it with the rest of its
lane set.

## Sedimenting a flow into a bundle

When the same flow is walked repeatedly — the same kind of request,
again and again, on the same repo — read this chapter and propose
sedimenting the flow into a custom bundle of its own: name the steps,
their order and gates after the way the flow actually runs, reuse the
shared step definitions where one fits, and keep the bundle small (a
flow that needs a spec and an independent review is the default
lane — `develop` — not a custom bundle). Filing the repeated flow on its bundle
makes every later walk one submit away.