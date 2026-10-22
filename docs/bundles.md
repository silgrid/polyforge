# Bundles — authoring a workflow bundle

The bundle-authoring chapter: what a workflow bundle is, the format of
its two file kinds, and a worked custom-bundle example whose every file
is the real format — the acceptance test
(`internal/common/workflow/docs_example_test.go`) parses this page's
example with the same loader the daemon uses, so the documented example
cannot drift from the actual format. What a bundle IS (the concept) is
defined on the [concepts page](concepts.md); the model-candidate
format is the [config reference](reference/config.md)'s
(`steps:` section) — one authority.

## The layout

A bundle is a directory under the daemon's workflows dir:

    workflows/
      _shared/               # not a bundle: the shared step definitions
        principles.md       # the engineering principles every step renders
        steps/<id>.md        # shared step definitions
      autopilot/             # a shipped bundle
        workflow.md          # frontmatter: the steps, their order, budgets, candidates
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

The shipped bundles: `autopilot`, `bug-fix`, `chore`, `feature-full`
(the delivery lanes) and `release` (the project-agnostic release flow).
Their own `workflow.md` bodies carry each lane's narrative.

## workflow.md — the frontmatter

The steps, their order, their budgets and their candidates are declared
in the frontmatter — the frontmatter `steps` array is the ONLY execution
order; the Markdown body below it is narrative, never a structural
schema. The keys:

- `name`, `description` — the bundle's identity.
- `rewind:` — the workflow-level rewind bounds, `max_total` and
  `max_per_step`. Past the bounds the run ends failed
  (`failed_rewind_exhausted`).
- `defaults:` — `model_candidates`, the workflow-level fallback chain
  (the format and the whole precedence chain: the
  [config reference](reference/config.md)).
- `steps:` — one entry per step, in order. Each entry:
  - `id` — the step id (the step definition file's name).
  - `context_policy` — `fresh` (the step starts from its own declared
    inputs, never an inherited conversation) or `inherit` (the step
    continues the previous step's context, its handoff included).
  - `gate: true` — the step can FAIL the run and rewind it; a gate must
    run `context_policy: fresh`. `on_fail: { target: <earlier step> }`
    names the rewind target (a gate FAIL rewinds there).
  - `requires_human_answer: true` — a human gate: the step can neither
    complete nor gate-fail without a human answer in the current
    attempt. `if_asked` — the softer form: a human answers only if the
    step asks.
  - `requires_command_evidence: true` — the command-evidence gate: a
    completed result must record the commands it ran.
  - `handoff: true` — the step must produce a handoff artifact
    (`artifacts/handoff-<id>.md`) for a later inherit step.
  - `model_candidates` — the step-level override of the chain.
  - `vars:` — the map the step definition's `{{placeholders}}` render
    with: one lane-specific fact per var, so one shared step text
    carries each bundle's own facts.
  - `budget: { wall_minutes: N }` — the step's wall-budget estimate;
    the step-timeout bound T becomes `max(3 x wall_minutes, T)`.

## steps/&lt;id&gt;.md — the step definition

Frontmatter: `name` (the step id), `description`, and `output` — the
JSON Schema the step's result envelope validates against. The body is
the step's prompt: what the step agent runs, its hard rules, its
output duty. Two placeholder forms render before the agent sees it:

- `{{principles}}` — renders `workflows/_shared/principles.md`, the
  engineering principles shared by every bundle. A step entry never
  declares this var; the loader renders it.
- `{{<name>}}` — any other placeholder renders with the workflow.md
  step entry's `vars` map. Rendering fails closed both ways: a
  placeholder without a var, and a var no placeholder uses, are both
  load errors — a typo cannot ship a half-rendered prompt.

## The worked example: a custom `apidocs` bundle

A repeated flow — "regenerate this repo's API reference docs" — walk it
once on a delivery lane, then sediment it into its own small bundle.
Three steps: scan the surface, regenerate the reference, verify the
## The worked example: a custom `apidocs` bundle

A repeated flow — "regenerate this repo's API reference docs" — walk it
once on a delivery lane, then sediment it into its own small bundle.
Three steps: scan the surface, regenerate the reference, verify the
build. The workflow.md (the frontmatter is the execution order):

```
---
name: apidocs
description: Regenerate a repo's API reference docs from its code - scan the surface, regenerate the reference, verify the build. A small custom lane for a repeated docs flow.
rewind:
  max_total: 2
  max_per_step: 2
defaults:
  model_candidates: [pi:prov/model-a, pi:prov/model-b]
steps:
  - { id: scan, context_policy: fresh, handoff: true }
  - { id: regen, context_policy: inherit }
  - id: verify
    context_policy: fresh
    gate: true
    requires_command_evidence: true
    on_fail: { target: regen }
    model_candidates: [pi:prov/model-b, claude:claude-opus-5-5]
    vars:
      output_required: verdict, checks
      build_step: regen
---

# apidocs — regenerate the API reference

The frontmatter steps array above is the only execution order; this
body is narrative. scan writes the handoff the regen step inherits;
verify gates the build and rewinds to regen on FAIL.
```

The step definitions, one file per step id — `steps/scan.md`:

```
---
name: scan
description: Scan the repo's API surface and write the handoff artifact artifacts/api-surface.md - every public entry point the reference must cover, with file paths.
output:
  type: object
  required: [summary]
  properties:
    summary:
      type: string
      description: Short prose summary of the surface found, for the regen step's context.
---

# scan

You run first in this lane. {{principles}}

Scan the repo worktree declared for this work item (repos/<name>/)
for its API surface: every public entry point the reference docs
must cover. Write the findings to artifacts/api-surface.md (the
handoff artifact the next step inherits): one section per area, each
entry naming its file path and its exported shape.

Work only inside the capsule and commit nothing. Your summary
carries the areas found.
```

`steps/regen.md`:

```
---
name: regen
description: Regenerate the repo's reference docs from the scan handoff, one compilable commit per regenerated page.
output:
  type: object
  required: [commits, build_status]
  properties:
    commits:
      type: array
      items:
        type: object
        required: [sha, message]
        properties:
          sha: { type: string }
          message: { type: string }
    build_status:
      type: string
      enum: [green, red]
    summary:
      type: string
---

# regen

You inherit the scan's handoff (artifacts/api-surface.md). Regenerate
the reference docs the repo owns from the worktree's code, keeping
every page's existing shape, and commit each regenerated page as one
compilable unit whose message names this lane.
```

And the gate, `steps/verify.md` — the `vars` of its workflow.md entry
render into it:

```
---
name: verify
description: Gate the regenerated reference - run the repo's build and doc checks on the tree the regen step left, with command evidence.
output:
  type: object
  required: [verdict]
  properties:
    verdict:
      type: string
      enum: [pass, fail]
    checks:
      type: array
      items: { type: string }
    summary:
      type: string
---

# verify

You are the lane's gate. {{principles}}

Run the repo's own build and doc checks (the full bar its CI runs)
on the tree the {{build_step}} step left, and record each command
you ran as command evidence. Your output requires: {{output_required}}.

A FAIL rewinds the run to the {{build_step}} step.
```

Ship it by adding `workflows/apidocs/` under this repository's
`workflows/` and releasing — the next install materializes it beside
`_shared`, and only then does `pf file create --bundle apidocs` resolve
it. Nothing here installs or ships with a release by itself.

## Sedimenting a flow into a bundle

When the same flow is walked repeatedly — the same kind of request,
again and again, on the same repo — read this chapter and propose
sedimenting the flow into a custom bundle of its own: name the steps,
their order and gates after the way the flow actually runs, reuse the
shared step definitions where one fits, and keep the bundle small (a
flow that needs a spec and an independent review is a feature-full
lane, not a custom bundle). Filing the repeated flow on its bundle
makes every later walk one submit away.