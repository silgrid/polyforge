# Extending — the extension points

Everything a user can configure or extend without changing polyforge's
code, one subsection per extension point. `pf help extensions` prints
this list as a one-line-per-item index. Each config shape's single
authority is the [config reference](reference/config.md) (every
`cli.toml` and `.polyforge.yaml` key, each once, in one place); a
custom workflow bundle is authored by the [bundle-authoring
chapter](bundles.md). This page indexes the points and points at the
authority; it restates no shape. The terms (workspace, repo mapping,
bundle) are defined on the [concepts page](concepts.md).

### Per-repo rules

A task repo's own `AGENTS.md` at its root (else its `CLAUDE.md`) carries
the rules that hold only for that repo. A step reads it through the shared
engineering principles (`workflows/_shared/principles.md`, rendered by a
step definition's `{{principles}}` — most shipped steps; `grep -L
'{{principles}}' workflows/*/steps/*.md workflows/_shared/steps/*.md` lists
the exceptions), which open with "first read each `repos/<repo>/AGENTS.md`
(else `CLAUDE.md`)": the repo's rules bind too and win any conflict with
the generic principles (the step notes the conflict in its artifact). Polyforge's own rules are in this repository's
[AGENTS.md](../AGENTS.md). Keep project-specific rules there, never in
`workflows/` (which stays project-neutral).

### Area notes

`docs/agents/` in a task repo holds area notes: one short note per area of
the code (what it owns, its invariants, its traps), named after the area.
This is a CONVENTION, not a mechanism: spec and review work reads the
notes for the areas whose files it touches (how pi-web works today). No
polyforge code reads or enforces `docs/agents/` yet, and no shipped step
definition names it; a repo that wants its notes read says so in its
`AGENTS.md` ([Per-repo rules](#per-repo-rules)).

### Model candidates

`steps:` in `.polyforge.yaml` — the per-project, per-step model candidate
lists of a workspace root. The shape, the candidate syntax and the whole
precedence chain are the config reference's one authority
([`.polyforge.yaml` → `steps:`](reference/config.md#steps--model-candidates-per-project-and-step));
the routing skill's fragments, `pf submit --models` and
[upgrade.md](upgrade.md) point there.

### Summarize models

`summarize:` in `.polyforge.yaml` — the summarize lane's model candidates,
narration language and interval: the config reference's
[`summarize:` section](reference/config.md#summarize--the-summarize-lane).

### Memory policy

`memory_policy:` in `.polyforge.yaml` — what the workflow's last step puts
into the work item's wrap-summary memory, and the step agents' memory
rules inside a step: the config reference's
[`memory_policy:` section](reference/config.md#memory_policy--the-wrap-summary-policy).

### Workspace mapping

The `[workspace."<path>"]` sections of `cli.toml` — the daemon's only
workspace source (polyforge#113): the config reference's
[`[workspace]` section](reference/config.md#workspace-path--the-repo-mapping).

### Daemon knobs

The `[daemon]` table of `cli.toml` — the production knobs the daemon reads
from its `--config` file: the config reference's
[`[daemon]` section](reference/config.md#daemon--the-daemon-knobs).

### Claude binary

`claude_bin` — a flat key of `cli.toml` naming the Claude Code binary of
claude steps: the config reference's
[`claude_bin` section](reference/config.md#claude_bin).

### Custom workflow bundles

A workflow bundle is a directory under the daemon's workflows dir:
`<bundle>/workflow.md` plus `<bundle>/steps/<id>.md`, one step definition
per step id, with the shared `_shared/steps/<id>.md` fallback. A work item
runs the bundle it was filed with (`pf file create --bundle <name>`).
Authoring detail — the layout, the frontmatter schema, step definitions,
vars, budgets, and a worked custom-bundle example validated by the real
loader — is the [bundle-authoring chapter](bundles.md), including how a
bundle ships (added under `workflows/` in this repository and released;
nothing else installs with a release).