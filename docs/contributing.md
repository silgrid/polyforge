# Contributing — filing work items and change coverage

How to contribute: filing work items, and the change-coverage disciplines
that govern when new coverage must be added. The binding rules for any
agent working in this repository are the repository's own
[AGENTS.md](../AGENTS.md) — the sole binding rule list; this guide
restates no rule, it links. (The disciplines below are this guide's own
contract: they bind as change-coverage policy, not as project rules —
AGENTS.md stays the only rule list.) The terms (work item, bundle,
lane, gate) are defined on the [concepts page](concepts.md); the
release flow and the build/test surface are the [release
reference](reference/release.md).


Change coverage is tiered by the change's **behavior face**, not by a
"e2e on every ticket" blanket rule (owner ruling 2026-10-02). The existing
e2e suite (`make test`, or `sh test/e2e-parallel.sh` for the e2e package
alone) is the regression net for every
change — each ticket's verification step runs it — and the three disciplines
below govern when **new** coverage must be added. This guide is the authority (the original
Chinese record, `docs/design/polyforge-vnext/archive/v1.5-change-coverage-rules.md`,
is archived history).

**Discipline ① — changes observable behavior → file on an e2e-carrying
graph.** If a change alters observable behavior — a new endpoint, a
protocol/contract/semantics change, an ownership or fencing rule change, a
new external capability — the ticket must be filed with `bundle_name` set to
a graph that carries an e2e-author step (currently `feature-full`, whose
verification also runs the authored scenarios; giving another graph that
step is zero code). Pure refactors, pure docs,
and obviously non-behavior chores keep the existing graphs (the existing
suite + verification is enough). Quick test: ask "after this merges, what
difference would users/callers perceive that the existing scenarios can't
detect?" — if you can answer, new scenarios are required. Edge case: a small
ticket discovered mid-flight to touch the behavior face (at the grill/spec
stage) declares it in needs_input, and the owner decides between re-filing
on the e2e graph and human-authored scenarios.

**Discipline ② — bug fixes → the repro must land as a regression test.** The
bug-fix graph's `fix` step carries a **mandatory** `regression_test` field:
the repro must be pinned as a test that fails before the fix and passes
after, lands in-tree, is run by verification, and is watched by review.
Waivers must be explicit (`waived: <reason>`) and are review-visible and
challengeable. A bug fix without a test pinning it is an unfixed bug — the
next regression of the same bug is left to the naked eye.

**Discipline ③ — workflow-bundle / prompt-logic changes → real-WI observation
window before the next cutover-grade release.** Any merge touching
`workflows/*/steps/*.md` or
`workflows/*/workflow.md` requires a real-WI observation window and gate
readings before the next cutover-grade release: a clean rig, ≥10 real
tickets of mixed bundle types (including ≥1 exercising the changed
surface), owner-answered needs_input, and the readings (command-evidence gate,
revision chain, recovery/blocked counts, orphan counts, the number of
verification runs that re-ran a bar the change had already made moot
("wasted verification runs"), and the distribution of review rounds per
ticket) compared against the
previous window — no regression passes, regression freezes merges until
fixed. The window ticket is registered at merge; it blocks cutover, not
merge. The report is filed on the window ticket and readings never
rewrite history.

**Merge-review checklist**:

- [ ] Behavior face touched? → filed on an e2e-carrying graph, or new
  scenarios human-authored
- [ ] Bug fix merged? → `regression_test` field non-empty, or a waiver
  reason accepted by review
- [ ] Skill face triggered? → observation-window ticket registered (blocks
  cutover-grade release, not merge)

## Verification tiers

`code` commits run the fast gates of the packages they touch, and each build
step (`code`, `fix`) also runs the `fast`- and `scoped`-phase commands of the
engine's `--json` report below before it calls a unit green (never its
`full`-phase ones); the full bar belongs to the `verification` step. The
scope comes from the
release-shipped engine, `tools/verify-scope`, which travels in the same
release tree as the lane that names it. The lane invokes it from the release
checkout, `<data-dir>/release/current/tools/verify-scope` (`<data-dir>` is
`$POLYFORGE_DATA_DIR` when set, else `~/.local/share/polyforge`). This
repository declares its specifics in `.polyforge/verify-scope.conf`; a
repository without that file gets the engine's conservative zero-config
defaults. The report of a diff and the commands to run:

```sh
<data-dir>/release/current/tools/verify-scope [--base <rev>] [--full] [--json] [--run]
```

- **Report.** stdout carries exactly one `mode: full|scoped` line, then one
  `path: <repo-relative path>` line per affected path (sorted, unique), then
  one `command: <command>` line per command in the fixed order. Diagnostics go
  to stderr. The default diff base is `git merge-base HEAD origin/<default>`;
  `--base <rev>` overrides it. The capsule baseline in `capsule.json` is never
  read. `--full` forces the full tier whatever the diff (it never narrows: a
  caller verifying a full-tier work item uses it to get the full command
  list).
- **JSON report.** `--json` replaces the line report with exactly one JSON
  object on stdout (diagnostics stay on stderr and are also collected in the
  object); python3 emits it (a `pf doctor` prerequisite), and without
  python3 `--json` exits 1. Schema 1:
  `schema` (`1`), `root` (the repository's absolute path), `stack`
  (`go|npm|none|unsupported|ambiguous`), `analyzer` (`go|npm|null`), `conf`
  (`present|absent`), `conf_changed_in_diff` (the conf is among the changed
  paths or differs between base and HEAD), `base` and `head` (resolved shas,
  `null` when unresolved), `default_branch`, `mode`, `paths`, `commands`,
  `test_glob`, `base_setup`, `runner` (`complete|null`), `ci_evidence` and
  `diagnostics`. Each `commands` entry is `{name, phase, cmd, requires_env,
  requires_cmd, timeout, source}`: `phase` is `fast`, `full` or `scoped` (the
  analyzer's selection), `cmd` is the `bash -c` string run from the
  repository root (no leading `cd` to the repository), `timeout` is the
  command's declared `check_timeout` (a Go duration) or `null` when none is
  declared — the engine sets no default, so the consumer's own default
  applies — and `source` is `conf` or `default`. The JSON and
  the line report of one invocation carry the same mode, paths and commands
  in the same order.
- **Stack detection, conf first.** `go.mod` selects the Go analyzer,
  `package.json` the npm analyzer. A `stack=` pin in the conf overrides
  detection (it resolves a repo that carries both), and `stack=none` pins "no
  analyzer". The conf's commands are reported for every stack: with no
  analyzer — `stack=none`, an unsupported stack (neither manifest), an
  unpinned multi-manifest repo, or an npm manifest whose scripts cannot be
  read — the report is always `mode: full` with the conf's commands only
  (none at all without a conf), exits 0 and names the reason on stderr. A
  Python, Helm or Rust repository therefore declares its bar in the conf. A
  repository whose manifest's analyzer does not cover its whole bar (a Go
  module that also ships Python or shell CI) adds the missing commands to the
  conf — a phase's conf lines replace that phase's defaults, so it restates
  the default gates it keeps — or pins `stack=none` to verify on the conf
  alone, without a scoped tier. A scope that cannot be computed always falls
  back to `full`, never to a wrong `scoped`.
- **The declarative conf.** `.polyforge/verify-scope.conf` is line-oriented
  (`#` comments and blank lines ignored; a value runs to the end of its line,
  so there is no trailing comment on a value line) and is never
  shell-evaluated. Its keys, each with its exact form, are listed once in
  [the config reference](reference/config.md#polyforgeverify-scopeconf--the-verification-bar):
  the analyzer pin `stack=go|npm|none`, `go_harness=`, the commands
  (anonymous `fast_gate=` / `full_command=` lines, named
  `check=<name>|<fast|full>|<command>` lines, and their
  `check_requires_env=` / `check_requires_cmd=` / `check_timeout=`
  declarations), the tier triggers `forced_full=`, `module_root=`,
  `exclude=`, and the keys the engine only echoes in its JSON report
  (`test_glob=`, `base_setup=`, `runner=complete`, `ci_evidence=`). Any
  fast-phase line replaces the analyzer's default fast gates and any
  full-phase line its default full tail; a phase's lines keep file order. The
  parser never sources or evaluates the file and has no hook-style keys
  (pre/post/exec); an unknown key, like a malformed line, is diagnosed and
  contributes nothing. A command value is an opaque string: it is reported as
  a `command:` line and, only with `--run`, executed by `bash -c` (command
  values are bash), with the same trust as this repository's own Makefile. A
  command value that *begins* with `$(` or a backtick is skipped with a lint
  diagnostic — that is a lint convenience, not a security guarantee, and it
  does not inspect the rest of the value. There is no line cap. This
  repository uses the fast gates `cd webui && npm ci
  && npm run build` (the console SPA build — the go:embed `dist` tree must
  exist before any `go build`, and a `webui/` change forces the full tier),
  `go build ./...`, `go vet ./...`, a
  clean `gofmt -l .` (`test -z "$(gofmt -l .)"`), the design-doc xref check
  `python3 -I docs/design/polyforge-vnext/tools/check-xrefs.py` (python3 is
  required — without it the gate fails loudly, it is never skipped), and the
  darwin arm64/amd64 cross-builds; every `go` command carries `GOWORK=off`.
  Its full tail is `make test` split into its two commands — the unit
  packages' `go test` and `sh test/e2e-parallel.sh`, so a pre-existing
  failure can be excluded from the command that carries it (the harness takes
  `E2E_SKIP='^(TestA|TestB)$'`, an extended regexp (`grep -E`, while `E2E_RUN`
  is a Go regexp) of top-level tests not to run, which its trailer counts as
  excluded; the unit line's inner `go list` carries its own `GOWORK=off`) —
  its forced-full
  paths include `go.mod`,
  `go.sum`, `Makefile`, `.polyforge/`, `internal/polyforge/driver/`,
  `internal/polyforge/daemon/`, the `test/` infrastructure and `webui/`; its Go
  spanning roots are `cmd`, `internal/aihub`, `internal/polyforge` and
  `internal/common`, and it declares
  `go_harness=test/e2e-parallel.sh`.
- **Go scoped selection.** The engine adds, after the fast gates: `go test` of
  the changed packages plus their reverse dependencies; a changed path maps to
  its nearest ancestor `go list` package directory, so a file under
  `<pkg>/testdata/` or another package subdirectory selects `<pkg>`. The
  reverse-dependency graph is the closure of each package's **test binary**
  (`go list -test`), so a package reached only through a `_test.go` import —
  directly or through that import's own dependencies — is selected as well. An
  unexpected `go list -test` output shape fails closed to `full`. The engine
  also adds one `go test -run` per package whose `*_test.go` text names a
  changed path (and which is not already in that package command). A
  top-level `test/` package is an ordinary package — selected by the
  reverse-dependency graph like any other — unless the conf declares
  `go_harness=<path>`: then that harness's directory is kept out of the
  package graph, its `*_test.go` files are skipped by the `-run` selection,
  and the engine adds `E2E_RUN='^(...)$' sh <path>` with the test names whose
  source references a changed path. The filename is never assumed: a
  repository that ships a `test/e2e-parallel.sh` without declaring it still
  gets the ordinary-package behavior.
- **npm scoped selection and full tier.** The npm analyzer reads the scripts
  `package.json` actually declares: it reports `npm run lint` and `npm test`
  only when those scripts exist, and `tsc --noEmit` only when TypeScript is
  configured (`tsconfig.json` or a TypeScript dependency). It installs with
  `npm ci` only when `node_modules/` is absent and selects non-browser
  `e2e/*.mjs` suites that reference a changed path; imported helper modules
  are excluded from both the selection and the standalone pass. The full tier
  reports every declared script whose command runs an `e2e/*.mjs` suite and
  then the remaining standalone suites, so it stays a superset of the scoped
  tier. `package.json`, its lockfile, `tsconfig*` and lint configs force
  `full`, augmented by the conf's `forced_full=` globs; an `exclude=` glob is
  a repo-specific full-pass omission.
- **Zero-config defaults.** With no conf the Go analyzer runs the three
  generic fast gates and a generic `go test` full tail; a Go change spanning
  two or more first-level directories forces `full`, while pure markdown/docs
  paths never count. The npm analyzer derives its gates and suites from
  `package.json`; a package that declares no lint or e2e script simply gets no
  such command.
- **Execution.** The engine's report is the bar: the verification step runs
  its commands through `pf verify-run` (below) and runs nothing beside them by
  hand — a repository whose bar the report misses declares it in its conf.
  With `--run` the engine prints the report and then executes the reported
  commands in order with `bash -c` from the repository root, stdin closed,
  stopping at the first failure and exiting non-zero; a command whose declared
  prerequisites are missing (a `check_requires_env` variable unset or empty,
  a `check_requires_cmd` item absent — a bare name looked up on `PATH`, a name
  containing `/` resolved against the repository root) is skipped with a
  stderr notice naming them, and `check_timeout` is reported, not enforced.
  The commands' output goes to stderr so stdout stays the report. That is
  for humans; `pf verify-run` never uses it.
- **CI evidence at delivery.** A repository whose PR CI runs its test bar
  may declare `ci_evidence=<workflow-file>/<job-name>` (repeatable) in the
  same conf, which the `deliver` step reads directly from the default branch
  (a change cannot declare it for itself): a successful check-run
  of every declared job for exactly the pushed head then stands in for the
  re-run of the carried test bar, while a repository without the key — this
  one has no CI — keeps the re-run ([config reference](reference/config.md)).
- **Extension point.** A new stack analyzer (Cargo, pyproject, …) is a future
  engine addition documented here; there is no stub analyzer today. A
  Cargo-only repository has no analyzer: it verifies on the full tier with
  the commands its conf declares, and with none when it has no conf.
- **The mechanical/judgment split.** The `verification` step runs the bar
  through one call, `pf verify-run`: the merge of the latest default branch,
  the engine's plan per touched repo, the whole plan (never stopping at the
  first failure), flaky re-runs, the pre-existing proof on the base, the test
  bar and the test-file checks are mechanical and land in its `result.json`.
  The agent keeps only judgment: the lane checks, the authored scenarios no
  binding decides, the scope declaration and the verdict. A touched repo the
  engine plans nothing for is listed unplanned and its bar stays the agent's
  to derive — declaring the bar in the repo's conf removes that case.
