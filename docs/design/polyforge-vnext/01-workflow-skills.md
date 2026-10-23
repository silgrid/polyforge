# 01 — Workflow bundle authoring: the frontmatter schema and execution semantics

> Current document (trimmed 2026-10-16, repo review wave 4: the removed content — WI Agent materialization, digest pinning at creation, `WF-*` error codes, requests/tokens budgets,
> and the release-slot / dispatcher / deploy-face historical rulings — moved out; the original is in git history and in the contemporaneous documents under [archive/](archive/)).
> The one-page summary is `reference.md#§7`; the decision record is [v2-converge.md](v2-converge.md); wherever it conflicts with the code, the code wins
> (`internal/polyforge/driver/workflow.go`, `driver/transition.go`, `driver/result.go`, `internal/common/workflow`).

## 1. Scope and non-goals <!-- §1 -->

### 1.1 Scope <!-- §1.1 -->

- The bundle's directory layout and the boundary between the two kinds of skill (the routing skill / the step definition file) (§2.1, §4.4).
- The `workflow.md` frontmatter: every key the driver parses and the load-time validation (§4.1); the step definition file frontmatter (§4.4).
- The reference flow and grill (§4.2, §4.3); context between steps (§4.4a).
- Execution semantics (§6): sequential advance, the bounded rewind on a business gate FAIL, in-place retry and candidate switching on infrastructure failure, incomplete, the driver main loop, the budget.

### 1.2 Non-goals <!-- §1.2 -->

- Spawning the step process, supervision's death judgement and cause handling (`v2-converge.md#§2`, `reference.md#§7.6`).
- The capsule layout and the git chain (`v2-converge.md#§14`, appendix E).
- There is no per-step isolation: a step runs natively on the host (the 2026-10-14 trust model, `v2-converge.md#§0`).

## 2. Terms and invariants <!-- §2 -->

### 2.1 Definitions <!-- §2.1 -->

- **Routing skill**: `skills/polyforge/` (`SKILL.md` + one fragment per verb under `verbs/`, plus doctor/help/init, `execute-intent.md` which carries the execution-intent routing, and `ship.md` which only says "delivery is already the deliver step"; the directory is authoritative). The only install face in the user's harness: it only turns intent into `pf` verbs, never parses a workflow, never touches the execution scene, and **never executes a workflow**. **It also carries the execution intent** (owner ruling 2026-10-20): code work the owner directs (change a repo / upgrade / fix a bug / deploy) is an execution intent, and the routing layer's correct action is to turn it into an execution ticket — `file create --bundle` + `submit` — **turning intent into a ticket is routing, not execution**; "never touches the execution scene" constrains the routing layer's own hands (the capsule, worktrees, execution-state files, steps), not the category of intents it may route. A session agent never does code work by hand: git branch/commit/push, gh pr create and `.repo/` writes happen only inside a claimed capsule (the v2 home of v1's IR1 write-operation gate — the iron rule was lost when the v1 skill set was deleted, and this item is its migration; carriers: the SKILL.md description's standing advertisement + session rule 7 + `verbs/execute-intent.md`).
- **The executor of a workflow** is the Go driver inside the daemon process (`internal/polyforge/driver`), which spawns short-lived harness processes step by step along the step graph; there is no execution skill and no long-lived agent.
- **Workflow bundle**: `workflows/<bundle-name>/` = `workflow.md` (frontmatter + body) + `steps/<id>.md`; step files shared verbatim by several bundles live in `workflows/_shared/steps/` (§4.4). There is no registry / registration interface / digest: a WI records only `bundle_name`, and the worker machine's daemon materializes from `-workflows-dir` (in production `<data-dir>/release/current/workflows`) (`reference.md#§7.1`).
- **workflow.md**: its frontmatter is the sole structural schema of the execution graph (§4.1); the body is flow-level prose and takes no part in parsing.
- **Execution graph**: the frontmatter's `steps` array, whose array order is the execution order. The copy materialized into the capsule does not change during that WI's execution; Core stores no parse result.
- **Step definition file**: `steps/<id>.md`; of its frontmatter the driver reads only `output` (§4.4), and the body is spliced into that step's dispatch prompt.
- **budget**: only the step-level `budget.wall_minutes`, an estimate: T = max(3 × wall_minutes, `--step-timeout`) (§6.6).
- **on_fail rewind**: the bounded rollback to an earlier step on a business gate FAIL (§6.2).
- **model_candidates**: a step's ordered candidate list `<harness>:<model>[:<effort>]` (§4.1; priority and switching in `reference.md#§7.5`).
- **grill / grill-review**: the two explicit steps of the reference flow (§4.2, §4.3).
- **e2e-author**: before code, writes acceptance scenarios for the new behavior only, commits them, and in the same step proves them red scenario by scenario at the assertion level against the pre-implementation tree; after code, `verification` reruns them (polyforge#165).
- **gate FAIL / infrastructure failure**: the former is a gate's negative judgement of the output (a `gate_failed` result); the latter is every failure that produced no judgement (process death, exit without a result, timeout, rate limiting and so on; causes of death in `v2-converge.md#§2.3`). Output that does not fit the `output` schema is **not** an infrastructure failure (for the release/deploy steps the envelope is rejected, elsewhere it is only recorded, §4.4).
- **Human gate**: `requires_human_answer: true` (without a human answer in this attempt chain the step can neither complete nor gate_fail) or `if_asked` (the question can only be answered by a human; completing with zero questions is allowed but records a `human_gate_unasked` event). The old key `interactive:` is an unknown key and is ignored — writing it **does not** give a human gate.
- **`next_step_by_order`**: the driver main loop's procedure for choosing the next step to execute (the earliest non-done step); it does not judge whether the workflow succeeded or failed (§6.5).

### 2.2 Invariants <!-- §2.2 -->

- INV-1a: `bundle_name` is frozen at first claim (the R2 window, `wi-kind-contract.md#§2`); the materialized copy inside the capsule is read-only and unchanged during execution; a re-claim re-materializes without content verification, and drift of the local checkout is a configuration accident.
- INV-1b: a rewind is triggered only by a business gate FAIL; an infrastructure failure always retries in place and never rolls back.
- INV-1c: the cumulative rewind count of each workflow instance has an upper bound (§6.2).
- INV-1d: a rewind resets every step in `[target, gate)` to `pending` and archives the old outputs as historical attempts; there is no second invalidation mechanism.
- INV-1e: `next_step_by_order` returns empty for only two reasons — all done, or the workflow is already `failed_gate` / `failed_rewind_exhausted`; the main loop must distinguish them before deciding how to wrap up (§6.5).
- INV-1g: model candidates live only in the workflow.md frontmatter (plus the `.polyforge.yaml` / `pf submit --models` overrides, `reference.md#§7.5`); the candidate pointer is advanced only by model-class causes of death, and a gate FAIL never switches candidates (a rewind resets the pointer).
- INV-1h: a step definition file never writes `model`; a completed result's `output` should match the declared `output` schema: enforced for the release bundle's `release`/`deploy` (a non-conforming result is rejected), record-only for the rest (§4.4).

## 4. Data structures: the workflow.md frontmatter schema and step definition files <!-- §4 -->

### 4.1 The full workflow.md frontmatter schema (the sole structural schema) <!-- §4.1 -->

`workflow.md` opens with YAML frontmatter (delimited by `---`). Top-level fields (the driver parses only `defaults`, `rewind` and `steps`; `name`/`description` are for humans and not parsed):

| Field | Type | Required | Notes |
|---|---|---|---|
| `name` | string | by convention | the bundle name, by convention equal to the directory name (not parsed) |
| `description` | string | by convention | a one-line description (not parsed) |
| `rewind.max_total` | int | optional | default 4; the cumulative rewind bound for the whole workflow |
| `rewind.max_per_step` | int | optional | default 2; the bound on re-entries of the same step by rewind |
| `defaults.model_candidates` | []string | optional | the ordered candidates when a step declares none; each item `<harness>:<model>[:<effort>]` (harness ∈ pi/claude, split at the first `:`, the prefix is required, polyforge#175) |
| `steps` | []Step | required | non-empty, `id` unique; **the array order is the sole execution order** |

There is no workflow-level `budget`.

Step element fields — the ten keys the driver parses (`extStep` in `internal/polyforge/driver/workflow.go`); every other key is silently ignored (including `kind`, `interactive`, `tools`, `inputs`, `silence_minutes`, `tool_minutes`):

| Field | Type | Required | Notes |
|---|---|---|---|
| `id` | string | required | non-empty, unique; binds `steps/<id>.md` and is also the key of `.polyforge.yaml` `steps.<project>.<step-id>.models` (a same-named step shares one key across bundles) |
| `gate` | bool | default false | this step produces a business judgement |
| `requires_human_answer` | bool \| `if_asked` | default false | human gate (§2.1): with `true`, a completed / gate_failed envelope without a human answer is rejected (repo review 2026-10-16 V-8) |
| `requires_command_evidence` | bool | default false | a completed result must carry `command_evidence` (§4.4a; there is no implicit rule by step id — the delivery bundles' `verification` declares it explicitly, ruling G10 of 2026-10-16) |
| `mechanical_fix` | bool | default false | **verification step entries only** (polyforge#260): arms the mechanical fast lane — an armed lane's all-static verification FAIL may carry a mechanical classification (`mechanical_bound` in the verdict output: one entry per failed check, its repo, its exact command and the files it named) and rewind cheaply (one slot, a light re-entry of the target resuming its previous session, one light recheck bounded to the failed commands, fail-closed escalation to the existing full verification). Absent = off: every fast-lane branch in the driver is gated on it, and an unarmed lane's FAIL rewinds exactly as today. Shipped armed in feature-full only |
| `on_fail` | `{target}` | only with `gate: true` | `target` is an earlier step id — declaring it is the rewind edge; not declaring it means `failed_gate` terminates (§6.2). The FAIL verdict's full text is delivered to the target unconditionally |
| `context_policy` | string | default `inherit` | `inherit`\|`fresh` (§4.4a); a `gate: true` step must be `fresh` |
| `handoff` | bool | default false | this step writes the handoff file `artifacts/handoff-<step-id>.md` (§4.4a); the default is false, so do not write `handoff: false` |
| `budget.wall_minutes` | int | optional | a run-time **estimate**: T = max(3 × wall_minutes, `--step-timeout`) (§6.6) |
| `model_candidates` | []string | optional | overrides `defaults` (replaced whole, not merged); may span harnesses, and switching to a candidate on another harness opens a new session carrying the old transcript as read-only reference |
| `vars` | map[string]string | optional | fills the `{{name}}` placeholders in this step's definition file (§4.4 "step variables"); the lane differences of a shared step text are declared only here |

The frontmatter declares no inter-step input wiring, no `cwd` and no ordering key: upstream artifacts land in the capsule's `artifacts/` and in `execution.json`'s `steps.<id>.output`, a downstream step's instructions read them by path convention; the step process's cwd is always the capsule root.

**Load-time validation (the code is authoritative)**: AIHub does not parse the frontmatter when creating a WI. Before claiming, the daemon first runs a candidate check on the local checkout's bundle (`common/workflow.CheckCandidates`: a candidate without a prefix, a `harness` key, or broken YAML → `DAEMON-414`, the WI stays queued; auto-pull skips that bundle). Structural validation runs when the worker machine's driver loads the materialized bundle (`LoadWorkflow` → `validateWorkflow`); any rule failing → that workflow fails to load and the WI is not dispatched; the error names the step and the problem, without a rule number or error code. Reading is lenient: unknown keys are ignored.

| Rule | Content |
|---|---|
| V2 | `steps` non-empty; every step has a non-empty, unique `id` |
| V3 | the array order is the sole execution order (guaranteed structurally, no separate check) |
| V5 | `on_fail` appears only on a `gate: true` step; `target` exists and its array position is strictly earlier than this step |
| V11 | `context_policy` ∈ `inherit`\|`fresh`; `gate: true` must be `fresh` |

Also: `requires_human_answer` accepts only `true`/`false`/`if_asked`; candidates are validated by `harness.ParseCandidate` when the driver is built (`driver/driver.go`). Deleted rule numbers (V1, V4, V6–V10) are not reused; the result envelope's "V4 command-evidence gate" is unrelated to them and remains in force.

### 4.2 The reference flow (review may sit at any position) <!-- §4.2 -->

The `steps` order of the reference bundle `feature-full` (`workflows/feature-full/`):

1. `grill` (not a gate; feature-full does not declare `requires_human_answer`, autopilot's grill declares `if_asked`)
2. `spec` (consumes `grill`'s Q&A record)
3. `grill-review` (`requires_human_answer: true`, `gate: true`; `on_fail: {target: spec}`)
4. `plan`
5. `e2e-author` (`requires_command_evidence: true`; writes acceptance scenarios for the new behavior only, commits them, and runs only the author's files against the pre-implementation tree to prove them red scenario by scenario at the assertion level; outputs `baseline[]`)
6. `code` (continues `e2e-author` through the inherit handoff)
7. `verification` (`gate: true`, `requires_command_evidence: true`; the full test bar + the authored scenarios' per-scenario results; `on_fail: {target: code}`)
8. `review` (`gate: true`; reads the complete diff (the test commits included) and the verification report; `on_fail: {target: code}`)
9. `deliver` (`gate: true`, `requires_command_evidence: true`; merge, push the feature branch, PR; `on_fail: {target: code}`; the file is at `_shared/steps/deliver.md`)

The delivery bundles feature-full / bug-fix / chore uniformly end with `… → verification → review → deliver` (polyforge#165); autopilot is `grill → fix → verification → deliver` (no review). The flow-level `rewind`: `max_total: 4`, `max_per_step: 2`.

**Release is its own bundle (ruling of 2026-10-13)**: the delivery bundles carry no `release` slot; release and deploy go only through the project-agnostic `workflows/release/` bundle, submitted as a separate WI. Three steps: `release` (`gate: true`: inside the capsule fetch and reset to the origin default branch head, record the remote tags before the release, probe whether `.polyforge/release.sh` supports `--verify`, run `.polyforge/release.sh [bump]`; the owner = the repo carrying `.polyforge/release.sh`: zero → FAIL, exactly one → execute, ≥2 → FAIL `PF-RELEASE-AMBIGUOUS_OWNER`) → `verify` (`gate: true`, fresh context: the release commit against the origin head, the new tag exists and points at the release commit, every pre-release tag unchanged, the release notes non-empty, finally `release.sh --verify <version>`) → `deploy` (`gate: true`: deploys only the version verify wrote to `artifacts/verified-version.json`; the owner = the repo carrying `.polyforge/deploy.sh`, zero → `defer`, ≥2 → FAIL; runs `.polyforge/deploy.sh <v> --confirm <v>`, rollback = step FAIL). All three steps are `requires_command_evidence: true` and none declares `on_fail` (a failure is `failed_gate`; a published tag/release/image is immutable, and a re-release is a separate WI filed by a human). The verdict vocabulary is bound mechanically by the driver (`driver/result.go`): `release` is `[pass, fail]` (pass must carry a non-empty `version` and at least one `artifacts_published`), `deploy` is `[pass, defer, fail]` (pass must carry a non-empty `version`). The defence lives inside `.polyforge/deploy.sh`: `--confirm <version>`, a server-side lock, automatic rollback on failure.

Key points:

- Data flow between steps is not in the structural schema: upstream artifacts are read by path convention (e.g. grill's `artifacts/grill-qa.md` is consumed by `spec` and `grill-review`).
- The positions of `grill`, `grill-review` and `review` are all expressed by the array order and are not validated.
- When `grill-review` FAILs with target `spec`, the FAIL verdict's full text is sent back with the target step's input, reusing §6.2, introducing no new state.

### 4.3 The two-phase behavior of grill <!-- §4.3 -->

1. **`grill`** (before spec): asks about the vague requirement item by item, not a gate; the Q&A record is written to the capsule's `artifacts/grill-qa.md`.
2. **`grill-review`** (after spec): one summarizing confirmation of the spec, `requires_human_answer: true` and `gate: true`; PASS advances to `plan`; FAIL triggers `on_fail: {target: spec}`, and the Q&A is appended to the same `artifacts/grill-qa.md`.

The driving engine does not distinguish the source of a verdict: a `grill-review` FAIL takes the same path as the other gates. **The interaction channel**: a step asks with a `needs_input` result, the driver writes the question into `execution.json`'s `steps.<id>.pending_question`; a human answers through `pf answer` → the daemon's `POST /jobs/{wi}/answer` → the driver, and the driver resumes the same step in place (`reference.md#§7.3`, `v2-converge.md#§3`). An agent must not answer on a human's behalf: the daemon refuses an answer request that comes from a step process tree (DAEMON-403).

### 4.4 The step definition file <!-- §4.4 -->

```
workflows/<bundle-name>/
├── workflow.md            # required: frontmatter (structure) + body (flow-level prose)
└── steps/
    └── <step-id>.md       # one per step; file name = step id
workflows/_shared/
├── principles.md          # generic engineering principles: rendered by {{principles}} into the grill/spec/plan/code-like steps and review
└── steps/
    └── <step-id>.md       # step definitions shared by several bundles (deliver.md, verification.md, review.md)
```

Each `steps/<step-id>.md` = frontmatter (metadata) + a Markdown body (execution instructions); the driver reads it directly from the materialized bundle (`driver/spawn.go`, `driver/outputschema.go`).

**Shared step definitions (ruling G1 of 2026-10-16)**: the lookup order is decided by `workflow.StepDefinitionPath` — the bundle's own `steps/<id>.md` is used if it exists (it may override the shared version); otherwise it falls back to `workflows/_shared/steps/<id>.md`. `_shared` is not a bundle (it has no `workflow.md`), and materialization copies the whole directory into the capsule's `workflows/_shared/`. Do not share through symlinks: the materializing `copyTree` skips symlinks. A step file that several bundles have verbatim identical must go into `_shared` (guarded by `driver/shared_steps_test.go`).

**Step variables (follow-up to ruling G1, 2026-10-16)**: a step whose lanes differ in only a few facts (the rewind target, artifact names, lane-specific checks) is also written as one shared text, the differences written as `{{name}}` placeholders and filled by the `vars:` of that step's entry in workflow.md (`common/workflow.RenderStepVars`; `name` matches `[a-z][a-z0-9_]*`). There are only three rules: rendering applies to the whole definition file (the frontmatter's `output` schema and the body are both rendered, and `readStepDefinition` and `loadStepOutputSchema` read the same rendered result); values are inserted verbatim (the trailing newline of a YAML block scalar is removed) and never expanded a second time; both directions fail closed — a placeholder used in the file but not declared, and a variable declared but never used, are both errors (the step definition is unreadable at dispatch, and the schema records `output_schema_unloadable`; `TestShippedStepDefinitionsResolve` guards every shipped bundle). A difference that can be expressed in neutral wording does not get a variable. A literal `{{name}}` has no escape; variable values are inserted verbatim (on a schema line that is unquoted YAML), the bundle is treated as trusted and guarded by tests (`TestShippedStepOutputSchemasCompile`, `TestShippedStepDefinitionsResolve`). The shipped `verification` (`output_required`, `build_step`, `position`, `lane_checks`, `never_pre_existing`) and `review` (`build_step`, `scope_source`, `intro`, `judge`) are shared this way; `TestSharedStepPromptsKeepLaneFacts` dispatches bundle by bundle and checks that every lane fact is still in the prompt and that no other lane's fact is mixed in. The delivery and release notes in the workflow.md body are likewise reduced to one paragraph pointing at `_shared/steps/deliver.md`, the "Delivery and releases".

**Shared principles (polyforge#191)**: `workflows/_shared/principles.md` holds the engineering principles that hold for any project (simple, recoverable, least privilege, evidence, behavior decided by the owner), plus one requirement: before starting, a step reads the `AGENTS.md` (else `CLAUDE.md`) at the root of every repository under `repos/`, project rules bind equally and win on conflict, and the conflict is noted in the artifact. The full text exists only in this one copy: writing `{{principles}}` in a step definition file makes `workflow.ReadStepDefinition` render it as that file's content — this is the only placeholder that needs no declaration in `vars:`, and a step entry declaring a variable of the same name is an error; a definition that does not use it does not read the file (an old capsule from before materialization runs as usual). Every step that uses it first writes, in a `## Principles` section, a paragraph on how this step uses the principles, then places `{{principles}}`. Shipped coverage: feature-full's `grill`, `spec`, `plan`, `code`; autopilot's `grill` (which also writes the memo, used as grill+spec) and `fix` (plan/code-like); bug-fix's `root-cause` (which gives guidance on the fix scope, used as plan-like + evidence) and `fix`; chore's `plan` and `code`; and the shared `review` (every principle is a blocking check, the checklist expanded item by item). Each section is at most 1500 bytes (`driver/principles_test.go` guards the coverage, exactly-once and the byte cap). A rule that holds for only one repository does not go into `workflows/` but into that repository's own `AGENTS.md` — polyforge's own (backward-compatibility code, the reachability statement for new daemon/aihub entry points and so on) is in this repository's root `AGENTS.md`, and `CLAUDE.md` is just one line `@AGENTS.md` (Claude Code reads it via import; pi loads only the first `AGENTS.md`/`CLAUDE.md` existing in each directory, which is the `AGENTS.md`).

Frontmatter fields — **the driver reads only `output`**:

| Field | Category | Notes |
|---|---|---|
| `name` / `description` | by convention | not parsed |
| `output` | optional, the only key read | a draft-07 JSON Schema (written in YAML): injected into the dispatch prompt (placed before the body) and used by the driver to validate a completed result's `output` (an optional property whose value is `null` is treated as default) |
| `model` | forbidden | model selection lives only in the workflow.md frontmatter (INV-1g); `model` is not read |
| `tools` | deleted (G10) | never read; leftovers in old snapshots are ignored |

**The output contract (INV-1h)**: **enforced** for the release bundle's `release` and `deploy` steps (`outputSchemaEnforced` in `driver/outputschema.go`): a non-conforming result means the envelope is rejected (retried in the same process lane), not counted as an infrastructure failure and not advancing the candidate. Every other step is in **record-only mode**: a non-conforming result only records a durable `output_schema_violation` event, rejects no envelope, is not an infrastructure failure and does not advance the candidate. A schema that is declared but cannot be loaded records `output_schema_unloadable` and writes stderr; on `release`/`deploy`, a missing or unloadable schema means results are always rejected (the verdict contract lives in the schema).

### 4.4a Context passing between steps: `context_policy` and `handoff` (polyforge#147) <!-- §4.4a -->

Both context relations are declared by the step keys in the frontmatter, **and the semantics rest entirely on path convention, with no summary/manifest validation**:

- **`context_policy: inherit` (default)**: at dispatch the driver takes the **nearest earlier step that declared `handoff: true`** (it must already be done, else dispatch errors), reads the handoff body from `artifacts/handoff-<step-id>.md`, and splices it verbatim into this step's dispatch prompt (the "Handoff from step …" subsection); with no such upstream, or no such file, that subsection is simply missing, not a failure (`driver/spawn.go` `resolveHandoff`).
- **`context_policy: fresh`**: no handoff body is injected; upstream artifacts are fetched by path convention. A `gate: true` step is always `fresh` (enforced by the parser).
- **`handoff: true`** (declared only when some downstream inherit step has this step as its nearest handoff source; ruling G10 of 2026-10-16 deleted 7 declarations without a reader): this step must write `artifacts/handoff-<step-id>.md` before wrapping up.

**The result envelope is a minimal contract** — `outcome` + `output` (`needs_input` also carries `question_id` + `question`) plus the dispatch-identity fence, with no `handoff_ref`, no sha256, no artifacts manifest (`v2-converge.md#§2.6`). The driver's mechanical gates beyond the minimal contract: V4 command evidence (a completed result of a step declaring `requires_command_evidence: true` carries `command_evidence: [{argv, exit_code, duration_ms}]`, at least one entry, argv non-empty, `duration_ms > 0`, and the accepted evidence is written by the driver into `output.commands`); the committed-test-files gate (every path in `output.test_files` must be committed at HEAD of the task repo it lives in, polyforge#156); written tests are immutable (polyforge#164 H4); the `defer` verdict is allowed only on `deploy` (the remaining verdict rules of release/deploy are in the output schema they enforce). A violator's envelope is rejected, with the rejection reason, and retried in the same process; the 4th consecutive rejection (`maxEnvelopeRejects` = 3 retries) → the step is blocked `result_invalid`. In addition: the needs_input question shape, the pre_existing-failure boundary, deliver binding, human-gate rejection, `fail_reason` being required for release/deploy and `defer` being legal only on deploy (`driver/result.go`, `driver/deliver.go`).

### 4.5 Bundle reference <!-- §4.5 -->

The bundle reference is the single scalar `bundle_name` (polyforge#149 B9 deleted the whole digest chain: no aggregate digest, no stand-in, no explicit pin accepted, and `internal/` has no `Digest`/`FileEntry`). The execution side materializes from the local checkout by `bundle_name` without content verification; a missing or drifted checkout is a configuration accident (a materialization warning, and execution continues).

## 6. Core flows <!-- §6 -->

### 6.1 The success path: sequential advance <!-- §6.1 -->

1. Execute one by one in the order of the `steps` array.
2. Each step's result is written atomically by the driver to the capsule's `runtime-state/execution.json` (structure in `Execution`/`Step` of `internal/common/capsule/capsule.go`); the supervisor's push loop pushes the capsule onto the aihub chain at every commit boundary (`reference.md#§8`).
3. At runtime there is no jumping and no reordering.

### 6.2 Business gate FAIL → a bounded on_fail rewind <!-- §6.2 -->

When a `gate: true` step produces FAIL (a `gate_failed` result, or completed with `verdict: fail` — the driver rebinds it to gate_failed) (`driver/transition.go` `applyGateFailLocked`):

1. No `on_fail` declared: the workflow terminates as `failed_gate`, and the verdict's full text is written to `execution.json`.
2. `on_fail: {target: X}` declared: first check two kinds of bound — the cumulative rewinds have reached `rewind.max_total` (default 4), or the target's re-entries have reached `rewind.max_per_step` (default 2) — either one hit terminates as `failed_rewind_exhausted`, and the rollback below is not performed.
3. Neither hit: `rewind_stats`'s cumulative count and the target's re-entry count are each incremented by one.
4. Each step in `[target, gate)`: its current result is archived as an attempt carrying `rewind_gate` (the id of the triggering gate) — the target itself as `cause: gate_fail_rewind`, the steps between the target and the gate (they did not fail, their output was merely invalidated by the later gate) as `cause: superseded_by_rewind`; the status is reset to `pending` and the output cleared (so downstream does not consume stale artifacts).
5. The current position is set back to the target; the FAIL verdict's full text is persisted as `steps.<target>.pending_rewind_input` and consumed when the target reruns.
6. The gate that triggered FAIL is itself not archived and stays `in_progress`, re-entering in fresh form with the cumulative T zeroed (`v2-converge.md#§2.4`).
7. Continue §6.1 from the target.

**Light rewind (polyforge#222, a sub-form of the sequence above)**: when a `review` gate's FAIL has all of its recorded blocking findings (at least one) carrying `text_only: true`, the same atomic write takes the light path — the bounds are checked first and the slot is consumed as usual (items 2 and 3 unchanged), the target is archived as `cause: gate_fail_rewind_text_only`, and the verdict's full text is delivered to the target together with the light re-entry note (the `Note` of `pending_rewind_input`); the target's re-dispatch resumes its previous session (same harness, same session identity) instead of re-deriving in full — the resume instruction is not written a second time but derived from the ledger that same write already persisted (the target is pending and its last non-`infra_failure` archived attempt is the light cause — the same fact the `needs_code_change` self-report validation reads); when a resume is impossible (a different harness, or the native session/transcript no longer exists) it degrades to today's fresh-plus-read-only-reference rollback, inside the same slot, with the spawn journal recording `resume: false`. A light re-entry's completed result may carry `needs_code_change: true` (a self-report of a structural fix; without the light context, or with a non-boolean value, it is rejected): the same write archives the target as `gate_fail_rewind`, resets it to pending, and delivers the same FAIL verdict as the input of a fresh re-entry, charging no further slot (a misclassification is not charged twice). The gate itself still re-enters fresh (item 6 unchanged).

**Mechanical light rewind (polyforge#260, the second light cause)**: when a `verification` gate's FAIL — on a lane whose verification step entry declares `mechanical_fix: true` — carries a mechanical classification (`mechanical_bound` in the verdict output: one entry per failed check — its repo, its exact command and the files it named; the classification is the verification agent's own verdict field, instructed by the shared material `workflows/_shared/mechanical-fix.md` the armed prompt renders — the driver classifies nothing and judges staticness nowhere), the same atomic write takes the mechanical light path: the bounds are checked first and the slot consumed as usual, the target is archived as `cause: gate_fail_rewind_mechanical`, the verdict routes with the mechanical light re-entry note (fix only inside the bound — modifications of bound-named files, ≤ 40 changed lines and ≤ 5 files summed across repos, hard-coded — or self-report `needs_code_change: true`), and the gate additionally records the pending light recheck marker `steps.<gate>.pending_light_recheck` (the bound copied verbatim from the verdict's own classification + each task repo's HEAD at the FAIL settlement as the repair-diff base, driver-recorded, never step-supplied). The target's re-dispatch resumes its previous session exactly like #222's light rewind (the same ledger-cause derivation; the same `needs_code_change` self-report fresh rewind reusing the slot), and the marker is cleared by every rewind-range reset (a reset step never carries a stale one).

**The light recheck (polyforge#260)**: the marker present makes the verification step's next dispatch a light recheck — a fresh conversation whose prompt carries the light section (the bound commands per repo + the shared light contract + the light output schema, all from `workflows/_shared/mechanical-fix.md`) beside the untouched step definition, still under the command-evidence gate. Before it dispatches, the driver validates the bound's coverage (every check the FAIL verdict recorded `fail` covered by exactly one entry, each command and file list non-empty, each repo a declared task repo) and measures the repair diff deterministically (per repo: every changed path a modification of a bound-named file — any add, delete or rename breaks it — the numstat lines and file counts summed across repos against the hard-coded 40/5); a malformed classification or a broken bound escalates IN PLACE: the `light_recheck_escalate` write archives an audit-only `light_recheck_escalated` attempt with the reason, clears the marker and orders the next dispatch fresh — no rewind slot, no rewind input — so the loop re-dispatches the step in full mode (the existing full verification, unchanged). A light recheck that does not pass (its own FAIL verdict, or a completed claim whose command evidence does not show every bound command run with exit 0 in its repo — the test-bar re-run machinery, mechanical never semantic) escalates in the result's own settlement write the same way. A light pass settles in the same write: `mechanical_pass` on the verification output (the bound, the measured diff, the recheck commands and results, the recheck heads, `escalated: false`), the FAIL verdict's own `test_bar` carried verbatim (the driver copies it — the step agent never re-transcribes it), and NO `verified_heads` (a head verified only by a light recheck is never tests-covered). Both the escalation and the light pass ride the existing single atomic write; an interrupted light recheck re-dispatches and replays idempotently (deterministic commands plus the deterministic diff check — the marker is the mode switch on both sides, dispatch and settlement).

**The light-pass handover (polyforge#260)**: a light-passed workflow carries no verified head, so deliver's tests-covered guarantee rests entirely on the merge dispatch's recorded re-run of the carried `test_bar` — unconditional, no head-equality shortcut — and the hand-made-history walk anchors on the light record's `recheck_heads` (the repair range `diff_base..recheck_head` is covered strictly by the driver's modifications-only/caps check — an anchor replacement, not a weakening). A repo with no carried `test_bar` entry keeps today's rule: a human decides. Review runs in full as the first review on the fast-lane path (the light recheck writes the fast-lane section of `artifacts/verification-report.md`; review judges it as any verification evidence); #222's review text_only tail is unchanged.

**The candidate pointer is reset in a rewind** (INV-1g): a reset step reruns from the first item of the candidate list; no gate-FAIL path switches candidates. The bounds are configurable in the frontmatter (values ≤0 fall back to the default 4 / 2, so a rewind cannot be turned off with 0), and reaching a bound terminates — there is no third path of "continue after human authorization", and what follows can only be a decision at the WI level (`pf cancel`, then open a new WI).

### 6.3 Infrastructure failure → in-place retry of the current step (bounded candidate switching by cause of death) <!-- §6.3 -->

1. Judgement: every failure that produced no business verdict (process death, exit without a result, timeout, harness silence, rate limiting, a transient upstream error, a host error), judged dead by the supervisor or found by the driver while waiting for the result; the cause classification and handling table are in `v2-converge.md#§2.3`.
2. Handling (one atomic write, `TransStepFailed`): the step stays `in_progress`, a record with `cause: infra_failure` (carrying `failure_cause`) is appended to `steps.<id>.attempts[]`, the failure count and the relaxed thresholds are updated, and the next dispatch form is written.
3. Re-dispatch the same step in place, never skipping and never rolling back; a step already done is never rerun.
4. **Candidate switching** (`driver/candidates.go`): the effective list is obtained by the priority of `reference.md#§7.5`; the pointer = the number of `infra_failure` entries with a model-class cause of death (`model_error`, `model_hung`, `step_timeout_slow_model`, `exit_model_class`) after the last rewind archive (`gate_fail_rewind`, `gate_fail_rewind_text_only`, `gate_fail_rewind_mechanical` or `superseded_by_rewind`), capped at the last position — it depends only on `execution.json` and is recomputed identically after crash recovery. A host-class cause of death blocks the step without advancing; the other causes (`rate_limited` and `upstream_transient` within its bound included) retry on the same model. After the candidates run out the pointer stays at the last one.
5. Counted failures reaching the cap (`DefaultMaxStepFailures` = 3) → the step is blocked; the count is **per candidate**: it is zeroed on switching to a new candidate; `rate_limited` and `upstream_transient` within its bound are not counted (for the latter, after 2 consecutive occurrences the 3rd is classified as `exit_model_class` and switches candidate); an interruption caused by a daemon restart is not counted as a failure and does not advance the pointer (`driver/supervision.go`).

### 6.3b The two-way split of archived attempts (cause semantics) <!-- §6.3b -->

The non-accepted records of `steps.<id>.attempts[]` fall into two lanes, told apart by `cause` and mutually exclusive (there is also one non-failure kind of record: each accepted completed result appends one `cause: accepted`, `driver/transition.go`, used by the recovery engine's progress ledger and the candidate-pointer recomputation):

1. **Rewind archive**: appended by §6.2 item 4 — the archive of the business lane, after which the step is reset to `pending`. A rewind's target is recorded as **`gate_fail_rewind`**, the target of a text-only light rewind (§6.2) as **`gate_fail_rewind_text_only`**, and the target of a mechanical light rewind (§6.2, polyforge#260) as **`gate_fail_rewind_mechanical`**; when a light re-entry self-reports a structural fix with `needs_code_change: true`, the same write of the completed branch likewise archives that re-entry as **`gate_fail_rewind`** (reusing the same verdict and slot, see §6.2 light rewind); a step between the target and the gate that is invalidated as collateral is recorded as **`superseded_by_rewind`** (it did not itself fail). All three causes carry `rewind_gate` and all reset the candidate pointer.
2. **`infra_failure`**: appended by §6.3 item 2 — an auditable interruption record that does not change the step's status (it stays `in_progress`) and is also an input to the candidate-pointer recomputation.

Only a rollback archive produces `gate_fail_rewind` / `gate_fail_rewind_text_only` / `gate_fail_rewind_mechanical` / `superseded_by_rewind` — the rollback of a gate FAIL verdict (§6.2), or a light re-entry's structural self-report (likewise archived as `gate_fail_rewind`, reusing the same verdict and slot); every interruption that produced no verdict is an `infra_failure`. The two kinds of record coexist in time order.

### 6.4 The incomplete result <!-- §6.4 -->

1. A step may honestly produce `incomplete` (not a quality veto, but a failure to finish).
2. The driver puts the workflow into `blocked` with the current position unchanged; the runtime first seals and pushes the capsule, then puts the WI into aihub `blocked` with `SessionEnd(blocked)` (`waiting_on=owner`; `runtime/recover.go`).
3. The way out is left to a human: `pf resume <wi>` (continue: re-claim and continue at the original step) or `pf cancel <wi>` (give up) (`v2-converge.md#§6`).

### 6.5 The driver main loop <!-- §6.5 -->

The main loop is the Go driver inside the daemon (`driver/driver.go` `Run`), repeatedly calling `next_step_by_order` to pick the next step and dispatch it, until there is no next step:

1. A step is returned → dispatch (one short-lived harness process), and wait for its result envelope.
2. `completed` → mark `done`, back to 1.
3. `gate_failed` → §6.2; on entering `failed_gate`/`failed_rewind_exhausted` exit at once, else back to 1.
4. Infrastructure failure → §6.3, re-dispatch the same step in place.
5. `needs_input` → wait for a human to answer (during the wait there is no step process and no timing), and resume in place after the answer.
6. `incomplete` → §6.4, exit at once.
7. When empty is returned for the first time, check the state explicitly: already a failed terminal state → exit, never misjudged as complete; otherwise all done → the workflow is `completed`.

**The wrap-up mapping** (`runtime/recover.go`): `completed` → after the seal push, `SessionEnd(wrapped)`; `failed_gate`/`failed_rewind_exhausted` → after the seal push, `SessionEnd(failed)`; `blocked` → after sealing, `SessionEnd(blocked)`. When the terminal seal push fails, the close degrades to blocked (`v2-converge.md#§D`).

### 6.6 Budget <!-- §6.6 -->

- Structure: the step-level `budget: {wall_minutes: int}`, the only key; there is no requests/tokens dimension and no workflow-level budget (polyforge#147).
- Semantics: `wall_minutes` is an **estimate** — the bound T on that step's cumulative run time across all dispatches is T = max(3 × wall_minutes, the global `--step-timeout`, default 90m) (`WallEstimateFactor` in `driver/supervision.go`; `v2-converge.md#§2.4`). Under the default `--step-timeout` of 90m an estimate of ≤30 minutes has no effect, so the delivery bundles declare none; the release bundle's `release` step declares 45 (T = 135m).
- Exceeding T: the supervisor judges `step_timeout_long_task` / `step_timeout_slow_model` and handles it per `v2-converge.md#§2.3` (T is relaxed and persisted; a slow model switches candidate; a long task is blocked after its second timeout).
