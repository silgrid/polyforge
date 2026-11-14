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
a graph that authors acceptance tests before the implementation: `develop`
(the default lane, its build writes each acceptance
criterion's test red before the implementation turns it green, review
judges that coverage criterion by criterion, and verification re-runs those
tests). Pure refactors, pure docs,
and obviously non-behavior chores keep the existing graphs (the existing
suite + verification is enough). Quick test: ask "after this merges, what
difference would users/callers perceive that the existing scenarios can't
detect?" — if you can answer, new scenarios are required. Edge case: a
ticket discovered mid-flight to touch a behavior face its acceptance does not
name (develop's deviation stop: the plan diverges from the ticket, asked with
`pf step ask`) declares it in that question, and the owner decides between adding
the criterion and its e2e scenario to the ticket's acceptance and telling the
build to cancel (it hands off `cancel` citing the owner's words) to file it anew.

**Discipline ② — bug fixes → the repro must land as a regression test.** On
`develop` a defect ticket's first build unit is the reproduction test, committed
red before the fix — it is the regression test: the repro must be pinned
as a test that fails before the fix and passes after, lands in-tree, is run
by verification, and is watched by review.
Waivers must be explicit (`waived: <reason>`) and are review-visible and
challengeable. A bug fix without a test pinning it is an unfixed bug — the
next regression of the same bug is left to the naked eye.

**Discipline ③ — workflow-bundle / prompt-logic changes → real-WI observation
window before the next cutover-grade release.** Any merge touching
`workflows/*/steps/*.md` or
`workflows/*/workflow.yaml` (or a lane's `fragments/`) requires a real-WI observation window and gate
readings before the next cutover-grade release: a clean rig, ≥10 real
tickets of mixed bundle types (including ≥1 exercising the changed
surface), owner-answered `pf step ask` questions, and the readings (revision
chain, recovery/blocked counts, orphan counts, the number of
verification runs that re-ran a bar the change had already made moot
("wasted verification runs"), and the distribution of review rounds per
ticket) compared against the
previous window — no regression passes, regression freezes merges until
fixed. The window ticket is registered at merge; it blocks cutover, not
merge. The report is filed on the window ticket and readings never
rewrite history.

**The develop lane's gates, in brief** (its contract: `workflows/develop/README.md`).
Filing does not grill: a ticket may be filed as a bare idea. Its acceptance is
settled before it runs: `pf submit` refuses an execution ticket (one with a
bundle) whose content lists no criterion under `## Acceptance` or `## 验收` (the
driver reads both headings the same), and the session grills the acceptance in
before submitting; `--allow-no-acceptance` submits it anyway, on the owner's
explicit say-so. The daemon's auto-pull skips such a ticket too: it stays
queued and `pf queue` marks it 需要交互：缺验收 until its acceptance is
written in or it is submitted by hand. `build` does not grill the ticket; it asks the owner only on a
surprise, right after the plan and never at completion, with `pf step ask`. Human stops: a dead end or a
contradiction the code reveals (a defect `build` cannot reproduce included); the plan
diverging from the ticket; `confirm_plan` (one stop right after the plan, before the
first test, when the ticket says `confirm_plan: true`); the acceptance list when the
ticket has none or an incomplete one (never self-written; proposed and asked once with
the plan); rewind exhaustion (3 in total); and publish's owner-review. The
owner answers in free text that `build` reads — its summary states each
decision, the owner's words and its reading, which review checks against the
words. No daemon check settles `build`: it runs each unit red
then green itself and records the unit in `artifacts/build/units.json` — `[{id,
test_commit, impl_commit, run{repo, cwd, argv}, marker}]`, the run exactly as it ran —
and the steps after it verify that record. Review runs the workflow's `facts.py
plan-conformance`, `build --replay --isolation host` and `carries` and judges their facts: each unit's test
commit a one-parent ancestor of its implementation commit touching test paths only (the
mechanical bar's test files — `*_test.go`, `*.test.*`, `*.spec.*`, `test_*.py`,
`*_test.py`, a `test/` or `tests/` directory, the base conf's `test_glob=` lines — plus
`testdata/`, `__tests__/`, `e2e/` and the `go_harness` directory), each unit's run
replayed red on the tree of its test commit, on the host under the step's command
`PATH` (a run that passes there is a test the old code already passed), the plan's two-way
conformance, every change to a unit's test after its red commit, and every e2e suite the
build adds or changes declaring a well-formed `carries:` — matching a path it touched
under the suite's project root when the build added it or a unit's run names it — (the
engine's suites: an npm, python or rust root's `suite_glob=`, else its default
discovery); it checks the coverage table names every criterion once (the judged list:
the ticket's acceptance section, else the plan's `## Acceptance` the owner replied to),
the summary's decisions against the owner's own words, and a `confirm_plan` reply
before the first test commit. Verification runs each unit's recorded run again on the
final tree (`verify.py --unit-runs`). Review blocks only on five classes — `bug`,
`ac_gap`, `project_rule`, `test_weakened`, `guarantee` (defined once in
`workflows/_shared/steps/review.md`, the schema's enum; an owner reply the change
ignores is `ac_gap`, a deviation or a self-written acceptance list without an owner
reply, or a reading of the owner's reply its words do not support, is `guarantee`, a
unit whose replay is not red, an e2e scenario timed by a fixed delay, measuring before
its state settles or carrying a wrong `carries:` is `test_weakened`). Every other
finding is non-blocking, listed in the PR body for the owner to ticket or drop; no
sweep runs.

**Merge-review checklist**:

- [ ] Behavior face touched? → filed on an e2e-carrying graph, or new
  scenarios human-authored
- [ ] Bug fix merged? → its reproduction test landed (on develop, the first
  build unit, committed red before the fix), or a waiver reason accepted by
  review
- [ ] Skill face triggered? → observation-window ticket registered (blocks
  cutover-grade release, not merge)

## Verification tiers

`code` commits run the fast gates of the packages they touch, and each build
step (`code`, `fix`) also runs the `fast`- and `scoped`-phase commands of the
engine's `--json` report below before it calls a unit green (never its
`full`-phase ones); the full bar belongs to the `verification` step. A lane's
verification step runs the tier it declares (`with: { tier: light }`, else
`full`); a work item chooses no tier (workflow contract 1). The
scope comes from the engine `workflows/_shared/scripts/verify-scope` (with
its dependency-audit helper `audit-deps` beside it), which is part of the
workflow: it travels in the lane snapshot every capsule carries
(`$PF_SHARED_DIR/scripts/verify-scope`, where a step's scripts — `verify.py`
among them — find it) and in the release checkout,
`<data-dir>/release/current/workflows/_shared/scripts/verify-scope`
(`<data-dir>` is `$POLYFORGE_DATA_DIR` when set, else
`~/.local/share/polyforge`), where a hand run finds it. A
snapshot's copy is read-only, never executable: run it as `sh <path>`. This
repository declares its specifics in `.polyforge/verify-scope.conf`; a
repository without that file gets the engine's conservative zero-config
defaults. The report of a diff and the commands to run:

```sh
<data-dir>/release/current/workflows/_shared/scripts/verify-scope [--base <rev>] [--full | --micro | --light] [--json] [--run] [--since <rev>]
```

**The light tier (develop).** The develop lane's
verification runs `verify.py --tier light`:
`workflows/_shared/scripts/verify-scope --light` in each touched repo and in every nested engine root the diff
reaches (a subdirectory with its own tracked `.polyforge/verify-scope.conf`,
such as `pi-web/`; its commands run behind `cd <root> &&`), so the bar is
the fast gates, the affected unit tests, the e2e suites the change added or
changed or whose `carries:` meet it, plus the build's unit runs
(`--unit-runs`). Under
`--light` the conf's `forced_full=` globs and the `module_root=` spanning
rule force nothing, a changed path no `carries:` declaration covers is
diagnosed (the fast gates still cover it) instead of forcing full, and at a
nested root a changed path outside the root is left to the enclosing root;
the analyzer's own fallbacks (a stack config file such as `go.mod`, an
unbuildable package graph, a structural carries diagnostic, no analyzer)
still answer full, and the repo then runs its declared full plan. A
failing unit is re-run once (by the conf's `rerun=` template when the
command declares one), and a unit still red runs once on the baseline — the
default branch's commit the tested tree carries, `merge-base HEAD
origin/<default>` (the develop lane's `sync` merged the default branch
before it). The forced-full paths
stay the full gate's: the integration lane runs the full bar over the work
items that merged.

- **Report.** stdout carries exactly one `mode: full|scoped` line, then one
  `path: <repo-relative path>` line per affected path (sorted, unique), then
  one `command: <command>` line per command in the fixed order. Diagnostics go
  to stderr. The default diff base is `git merge-base HEAD origin/<default>`;
  `--base <rev>` overrides it. The capsule baseline in `capsule.json` is never
  read. `--full` forces the full tier whatever the diff (it never narrows: a
  caller verifying a full-tier work item uses it to get the full command
  list).
- **Nested project root.** The repository is the engine's working directory.
  Run from a subdirectory of a git work tree that carries its own conf (a
  nested project root, such as `cd pi-web`), the engine reports and matches
  the diff's paths relative to that directory, like every other path it
  reads — the conf's globs, the `carries:` declarations and the listed
  files — although git names them from the work tree's top. A changed path
  outside the nested root is reported as `../<path>`: no declaration of the
  root can account for it, so the report falls back to `mode: full` (and the
  full-tier e2e selection to the full tail), diagnosed. `--micro` reads the
  nested root's own conf at the base. At the work tree's top nothing changes.
- **JSON report.** `--json` replaces the line report with exactly one JSON
  object on stdout (diagnostics stay on stderr and are also collected in the
  object); python3 emits it (a `pf doctor` prerequisite), and without
  python3 `--json` exits 1. Schema 1:
  `schema` (`1`), `root` (the repository's absolute path), `stack`
  (`go|npm|python|rust|none|unsupported|ambiguous`), `analyzer`
  (`go|npm|python|rust|null`), `conf`
  (`present|absent`), `conf_changed_in_diff` (the conf is among the changed
  paths or differs between base and HEAD), `base` and `head` (resolved shas,
  `null` when unresolved), `default_branch`, `mode`, `paths`, `commands`,
  `test_glob`, `base_setup`, `runner` (`complete|null`),
  `e2e_runner` (the declared runner command or `null`), `suite_glob`,
  `e2e_map` and `diagnostics`; a repository that declares `smoke_suite=` also
  gets `smoke_suite` (the declared smoke suites) and `e2e_selection`
  (`{mode, suites, uncovered_paths, full_commands}` when a full-tier e2e
  selection was attempted, else `null`; see "Full-tier e2e selection" below),
  so a repository that declares none keeps the key set; likewise a
  repository that declares `micro_path=` or `micro_check=` (and every
  `--micro` report) also gets `micro_paths` (the declared patterns) and
  `micro_checks` (the declared extra commands), and a repository with an
  accepted `host_requires=` line also gets `host_requires` (its declared host
  prerequisites, `[{name, probe, hint}]` in file order); a `--light` report also gets
  `light: true`. Each `commands` entry is `{name, phase, cmd, requires_env,
  requires_cmd, timeout, report, rerun, source}`: `phase` is `fast`, `full` or `scoped` (the
  analyzer's selection), or `micro` under `--micro`, `cmd` is the `bash -c` string run from the
  repository root (no leading `cd` to the repository), `timeout` is the
  command's declared `check_timeout` (a Go duration) or `null` when none is
  declared (the engine sets no default, so the consumer's own default
  applies), `report` is the command's declared failure report
  (`{format: "junit", path}` from `report=`, `null` when none is declared),
  `rerun` is the command's declared unit re-run template (`rerun=`, below;
  `null` when none is declared) and `source` is `conf` or `default` (a full-tier report's last
  entry is the engine's `audit` gate, below). The JSON and
  the line report of one invocation carry the same mode, paths and commands
  in the same order.
- **Stack detection, conf first.** `go.mod` selects the Go analyzer,
  `package.json` the npm analyzer, `pyproject.toml` or `requirements.txt` the
  python analyzer and `Cargo.toml` the rust analyzer. A `stack=` pin in the
  conf overrides detection (it resolves a repo that carries two or more of
  them), and `stack=none` pins "no analyzer". The conf's commands are reported
  for every stack: with no analyzer — `stack=none`, an unsupported stack (no
  supported manifest), an unpinned multi-manifest repo (two or more supported
  manifests), or an npm manifest whose scripts cannot be read — the report is
  always `mode: full` with the conf's commands only (none at all without a
  conf), exits 0 and names the reason on stderr. A Helm or other-stack
  repository therefore declares its bar in the conf. A
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
  the analyzer pin `stack=go|npm|python|rust|none`, `go_harness=`, the commands
  (anonymous `fast_gate=` / `full_command=` lines, named
  `check=<name>|<fast|full>|<command>` lines, and their
  `check_requires_env=` / `check_requires_cmd=` / `check_timeout=` /
  `report=` / `rerun=` declarations), the tier triggers `forced_full=`, `module_root=`,
  `exclude=`, the carries keys `e2e_runner=`, `suite_glob=`, `e2e_map=` and
  `smoke_suite=` (below), the micro lane's `micro_path=` / `micro_check=`
  (the `--micro` bullet below), and the keys the engine only echoes in its JSON report
  (`test_glob=`, `base_setup=`, `runner=complete`, `host_requires=`). Any
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
  clean `gofmt -l` over the tracked Go files
  (`test -z "$(gofmt -l $(git ls-files '*.go'))"`, which replaced
  `gofmt -l .` when pi-web/ joined the tree), the design-doc xref check
  `python3 -I docs/design/polyforge-vnext/tools/check-xrefs.py` (python3 is
  required — without it the gate fails loudly, it is never skipped), the
  darwin arm64/amd64 cross-builds, and the workflow step scripts' type check
  `cd workflows/_shared/scripts && python3 -m mypy --strict --python-version
  3.9 pfstep tests *.py` (pf CI's python-types job, the same
  command, so the light and full tiers catch a type error before a merge;
  mypy `>=1.20,<2` is required — without it the gate fails loudly, it is
  never skipped; the verification image ships it); every `go` command carries
  `GOWORK=off`.
  Its full tail is `make test` split into its two commands — the unit
  packages' `go test` and `sh test/e2e-parallel.sh`, so a pre-existing
  failure can be excluded from the command that carries it (the harness takes
  `E2E_SKIP='^(TestA|TestB)$'`, an extended regexp (`grep -E`, while `E2E_RUN`
  is a Go regexp) of top-level tests not to run, which its trailer counts as
  excluded; the unit line's inner `go list` carries its own `GOWORK=off`),
  with the daemon package's tests once more under the race detector between
  the two (`go test -race -count=1 -timeout 30m ./internal/polyforge/daemon/`,
  a new data race there fails the bar; 210-226 s on a
  32-core host) —
  its forced-full
  paths include `go.mod`,
  `go.sum`, `Makefile`, `.polyforge/`, `internal/polyforge/driver/`,
  `internal/polyforge/daemon/`, the `test/` infrastructure, `webui/` and `pi-web/`; its Go
  spanning roots are `cmd`, `internal/aihub`, `internal/polyforge` and
  `internal/common`, and it declares
  `go_harness=test/e2e-parallel.sh`.
- **This repository's micro lane.** The root conf opts the docs into the
  micro lane: its `micro_path=` lines (the conf holds the exact list) cover
  the top-level guides one by one, `docs/reference/*.md`, and the design
  docs `docs/design/polyforge-vnext/*.md` and its `archive/*.md`. They leave
  out `docs/upgrade.md` (append-only, and a test pins its sha256: any byte
  changed moves that pin in the same commit) and the design directory's
  `tools/` (the xrefs check is a fast gate, which a micro change must not
  rewrite).
  Its two `micro_check=` lines run the contract tests that read those docs
  (well under a minute with the fast gates; about 35 s measured on a 32-core
  host, longer on smaller ones), so a micro change that breaks a pinned
  sentence fails `--micro --run` itself rather than the integration run's
  full bar. A new test that reads an in-scope doc, or a new docs directory,
  joins those lines in the same change.
- **The pi-web gate boundary.** `pi-web/` is the web workbench's own npm
  package inside this repository. `pi-web/go.mod` is a marker module with no
  packages: it ends the root module at that directory, so `go build ./...`,
  `go vet ./...` and `go list ./...` never walk `pi-web/node_modules` (npm
  packages may ship stray `.go` files), and the gofmt gate lists tracked
  files only for the same reason. A change under `pi-web/` is forced to the
  full tier (`forced_full=pi-web`).
- **The union bar.** The full tail is the union of both former
  repositories' bars: after the Go unit packages and the acceptance harness
  it runs the pi-web package's bar from `pi-web/` — `npm ci`,
  `npm run lint`, `node_modules/.bin/tsc --noEmit`, `npm test` and
  `npm run test:e2e:all` (whose `polyforge-workflow-216.mjs` builds this
  repository's `pf` from `./cmd/pf`, no sibling checkout). The Makefile
  carries the same surface: `pi-web-build`, `pi-web-lint`, `pi-web-test`,
  `pi-web-e2e`, and `check` runs the Go gates plus `pi-web-lint` and
  `pi-web-test`. There are two scope confs: this root conf for the
  repository, and `pi-web/.polyforge/verify-scope.conf`, the subtree's own
  npm-stack conf, which applies only when the engine runs with `cd pi-web`
  (a nested project root, above: its paths and `carries:` globs are relative
  to `pi-web/`);
  the root conf forcing the full tier for `pi-web/` paths is what keeps a
  scoped run of the repository from under-verifying a pi-web change. The
  light tier (develop) applies no forced-full path: a pi-web
  change runs the subtree conf's light report (its fast gates and the suites
  whose `carries:` meet it), and the rest of the package's bar is the
  integration run's.
- **The release gate.** This conf's full tier, run inside the image built
  from `.polyforge/Dockerfile.verify`, is also the release's test gate: one
  definition, run by the workflow script `gate.py` (`python3
  workflows/_shared/scripts/gate.py --isolation container` from the
  clone's root), reused by the release when the released commit
  carries a green tag — written only by `gate.py --mark-green` after a
  pass — for the same gate id and plan digest (`tools/gate-id`,
  `tools/green-tag check`). A change to a gate line of this conf or to the
  Dockerfile is a change to the release gate. `make test` is a developer
  subset and gates nothing. The contract is in
  [docs/reference/release.md](reference/release.md#the-test-gate).
- **CI.** Two GitHub workflows, reachable by any push to `main` and any
  pull request: `.github/workflows/pf-ci.yml` runs the Go/console fast gates
  (`make build vet fmt xrefs`) for every pull request outside `pi-web/**`
  and every push to `main`, and `.github/workflows/pi-web-ci.yml` runs the
  pi-web package's checks (`npm ci`, lint, `tsc --noEmit`, `npm test`) and
  its browser e2e job from `pi-web/` for a change under `pi-web/**`. No
  workflow lives under `pi-web/`, and none releases: `.polyforge/release.sh`
  is the only release entry.
- **Code size on every pull request.** `pf-ci.yml`'s
  `loc-report` job writes `tools/locreport`'s per-package table of code
  lines — Go and `workflows/**/scripts/**/*.py`, non-test and test code
  apart, comments, docstrings and blank lines left out; its header states
  the rule — into the job summary of every pull request outside
  `pi-web/**`. A repository non-test net change beyond the threshold
  (`defaultThreshold` in `tools/locreport/main.go`, the one place it is set)
  is a `::warning` annotation; the check stays green. Locally:
  `go run ./tools/locreport $(git merge-base origin/main HEAD) HEAD`.
- **The full bar after the merge.** Every push to `main` —
  each merge, whatever paths it touches — runs this repository's full bar in
  CI: the `full-bar`, `full-e2e` and `full-audit` jobs of `pf-ci.yml` run
  every `full_command=` line of the root conf verbatim and in its order
  (the Go unit packages, the daemon package under the race detector, the Go
  acceptance harness, pi-web's install, lint,
  typecheck and unit tests, then every pi-web e2e suite) plus the engine's
  dependency-audit gate, never on a pull request (the bar's steps as root,
  like the hosts verification runs on); a manual dispatch runs
  them on any ref. Each merge keeps its own run (a later push never cancels
  it), and `test/w375_main_full_bar_ci_test.go` fails when the jobs drift
  from the engine's full-tier report. A red `full-bar` or `full-e2e` on
  `main` is filed as a bug work item for the merge it ran on; a red
  `full-audit` is usually a newly published advisory or an expired
  exemption rather than that merge. Nothing reverts automatically.
- **Go scoped selection.** The engine adds, after the fast gates: `go test` of
  the changed packages plus their reverse dependencies; a changed path maps to
  its nearest ancestor `go list` package directory, so a file under
  `<pkg>/testdata/` or another package subdirectory selects `<pkg>`. The
  reverse-dependency graph is the closure of each package's **test binary**
  (`go list -test`), so a package reached only through a `_test.go` import —
  directly or through that import's own dependencies — is selected as well. An
  unexpected `go list -test` output shape fails closed to `full`. The graph is
  listed with `go list -e`: an embed pattern that matches no file (such as
  `//go:embed all:dist` before the console build) is tolerated, because it
  changes no package's dependencies, and any other package error still fails
  closed to `full`. The engine
  also adds one `go test -run` per package whose `*_test.go` text names a
  changed path (and which is not already in that package command). A
  top-level `test/` package is an ordinary package — selected by the
  reverse-dependency graph like any other — unless the conf declares
  `go_harness=<path>`: then that harness's directory is kept out of the
  package graph, its `*_test.go` files are skipped by the `-run` selection,
  and the engine adds `E2E_RUN='^(...)$' sh <path>` with the test names whose
  source references a changed path. The filename is never assumed: a
  repository that ships a `test/e2e-parallel.sh` without declaring it still
  gets the ordinary-package behavior. The Go analyzer never reads `carries:`
  headers: its harness selection stays the source-reference one above.
- **npm scoped selection and full tier.** The npm analyzer reads the scripts
  `package.json` actually declares: it reports `npm run lint` and `npm test`
  only when those scripts exist, and `tsc --noEmit` only when TypeScript is
  configured (`tsconfig.json` or a TypeScript dependency). It installs with
  `npm ci` only when `node_modules/` is absent and selects its `e2e/*.mjs`
  suites through the carries layer (below) — never by a textual reference to
  a changed path. The full tier reports every declared script whose command
  runs an `e2e/*.mjs` suite and then the remaining suites (the carries
  layer's suite set: no imported helper, no `exclude=` match, no
  `e2e_runner=` target), so it stays a superset of the scoped tier.
  `package.json`, its lockfile, `tsconfig*` and lint configs force `full`,
  augmented by the conf's `forced_full=` globs; an `exclude=` glob is a
  repo-specific omission.
- **python scoped selection and full tier.** The python analyzer's fast gate
  is `python -m compileall -q`. Its scoped tier adds `pytest <files>` for the
  unit tests (pytest-collected `test_*.py` / `*_test.py` files outside the
  suite set) that import a changed module — directly, or through other
  repository modules; imports are scanned statically and resolved from the
  repository root, `src/`, or beside the importer — and then the carries
  suites. A changed module no test reaches is a low-confidence scope and
  falls back to `full` with a diagnostic, never a guessed narrow set. The
  full tier is `pytest`. `pyproject.toml`, `requirements*.txt`, `setup.py`,
  `setup.cfg`, `pytest.ini`, `tox.ini` and any `conftest.py` force `full`.
- **rust scoped selection and full tier.** The rust analyzer's fast gate is
  `cargo build --all-targets`. It reads the module tree from `mod <name>;`
  declarations and adds `cargo test --lib -- <path>::…` (or `--bins` for a
  binary crate) for the modules that contain unit tests and contain or `use`
  (`crate::<path>`) a changed module, then the carries suites. A changed
  file no crate's module tree reaches, a changed crate root
  (`src/lib.rs`, `src/main.rs`), or a changed module no unit test or
  integration test reaches falls back to `full` with a diagnostic. The full
  tier is `cargo test`. `Cargo.toml`, `Cargo.lock`, `build.rs`, the
  toolchain file and `.cargo/` force `full`.
- **Zero-config defaults.** With no conf the Go analyzer runs the three
  generic fast gates and a generic `go test` full tail; a Go change spanning
  two or more first-level directories forces `full`, while pure markdown/docs
  paths never count. The npm analyzer derives its gates and suites from
  `package.json`; a package that declares no lint or e2e script simply gets no
  such command.
- **Dependency-audit gate.** Every full-tier report of a repository with an
  analyzer ends in one engine-appended command, `{name: "audit", phase:
  "full", source: "default"}` with no prerequisites and a `null` timeout:
  `sh '<engine dir>/audit-deps' [--go] [--npm '<dir>']... [--ledger '<path>']`, the
  `workflows/_shared/scripts/audit-deps` helper shipped beside the engine. Its surfaces are the
  root `go.mod` (`--go`: `GOWORK=off govulncheck -json ./...`, symbol mode —
  only a vulnerability reached through a called symbol is a finding, an
  imported-but-unreached package is not) and every `package-lock.json` /
  `npm-shrinkwrap.json` outside `node_modules/`, sorted (`--npm <dir>`: `npm
  audit --json --audit-level=high --package-lock-only --include=dev` — the
  whole lockfile tree, dev dependencies included; a high or critical advisory
  is a finding, moderate and low are not); a repository with no surface, and
  every no-analyzer report, gets no audit. It runs after the conf's or
  analyzer's full tail and after the carries selection, and ends
  `e2e_selection.full_commands`, so the delivery re-run carries it; scoped
  and `--micro` reports never carry it. No conf key reaches it: `audit` and
  `audit-*` are reserved check names (diagnosed and ignored), a `check_*`
  line cannot bind a prerequisite or timeout to it, and `exclude=` does not
  narrow its surfaces. It is an ordinary command entry, so the `--json`
  shape and `schema` (`1`) do not move. The gate exits 0 only when every
  surface was audited and no unexempted finding remains, else 1, with one
  stderr diagnostic per line: `tool-missing:` (npm, go, govulncheck or
  python3 absent — red, never a skip), `db-unreachable:` (the advisory
  source could not be reached — red), `finding:` (advisory, package,
  surface), `ledger-invalid:` (a malformed exemption row — red),
  `ledger-expired:` and `stale:` (reported, never red on their own),
  `surface <name>:` (the per-surface summary) and `audit-deps:` (any other
  tool error — red). The only exemption is a valid, unexpired row of the
  repository's `.polyforge/audit-exemptions.conf` ([config
  reference](reference/config.md#polyforgeaudit-exemptionsconf--dependency-audit-exemptions)),
  at the top of the git work tree — a nested engine root's report names
  that same file (`--ledger '../.polyforge/audit-exemptions.conf'`, one
  `../` per segment of the root), so its audit reads it too —
  and a diff that adds or extends a row needs an owner ruling on record.
  A newly published advisory on an unchanged dependency fails the bar too:
  the mechanical bar's skip relaxation (a declared prerequisite) and
  pre-existing classification (a test unit that fails on the base) never
  reach the `audit` command, which declares no prerequisite and reports no
  test unit. The host needs `npm` and the pinned `govulncheck` ([install](install.md#dependency-audit-tools));
  `pf doctor`'s `audit tools` row warns when either is missing.
- **Execution.** The engine's report is the bar: the verification step's
  mechanical bar (below) runs its commands and nothing beside them — a
  repository whose bar the report misses declares it in its conf.
  With `--run` the engine prints the report and then executes the reported
  commands in order with `bash -c` from the repository root, stdin closed,
  stopping at the first failure and exiting non-zero; a command whose declared
  prerequisites are missing (a `check_requires_env` variable unset or empty,
  a `check_requires_cmd` item absent — a bare name looked up on `PATH`, a name
  containing `/` resolved against the repository root) is skipped with a
  stderr notice naming them, and `check_timeout` is reported, not enforced.
  The commands' output goes to stderr so stdout stays the report. That is
  for humans; the mechanical bar never uses it.
- **`--since` (has the upstream touched this change?).** `--since <rev>`,
  given a head verified earlier, adds one answer to the report: whether the
  changes from `<rev>` to HEAD — the upstream a delivery merged in, as a tree
  diff — touch the affected set of the work item's own diff (base..HEAD).
  It is symmetric: each side's affected set is computed the same way — for
  Go the changed packages plus their reverse-dependency closure (test
  binaries included, and unlike the scoped selection the `go_harness`
  package kept in the graph, so a package any harness test imports reaches
  it), the packages whose tests textually name a changed path and the
  declared `go_harness`; for npm, python and rust the suites the carries
  layer selects; for every stack the changed paths themselves — and the
  answer is whether the two meet, so an upstream change to a *dependency*
  of the work item's packages meets it as well as one to a dependent. A
  side that changes a forced-full path, a path in the `go_harness`
  directory (its tests drive the whole built program), a path outside a
  nested root or (carries) a path no declaration covers affects the whole
  repository. It fails closed: an upstream change to the conf, a malformed
  carries key or broken declaration, no analyzer, an unresolvable `<rev>`,
  an unreadable diff or an unbuildable package graph answer `intersects:
  true`. `--json` gains `since: {head, intersects, paths, reason}` (`head`
  the resolved sha, `paths` what both sides reach); without `--since` the
  report is unchanged. python and rust module closures are not followed:
  a changed module no carries declaration covers is uncovered (everything).
  The query only answers; the delivery's re-run rule does not read it yet.
- **`--micro` (the micro lane's evidence).** `--micro` reads the conf as it
  is at the diff base (`git show <base>:.polyforge/verify-scope.conf`; absent
  or unreadable there declares nothing, never a working-tree fallback, so a
  change cannot widen its own declaration), reports `mode: micro`, and
  restricts `commands` to the fast phase followed by the `micro_check=`
  commands (phase `micro`, named `micro-<i>` in file order) — never a scoped
  or full command. `--micro --run` executes exactly those in order from the
  repository root and stops at the first non-zero; under `--micro` a missing
  declared prerequisite, a repository that declares no `micro_path=` at the
  base, or an empty command list fails instead of being skipped. It is the
  micro `fix` step's self-check; `fix` writes the same list as its evidence
  plan (`artifacts/fix/evidence.json`) and the lane's `evidence` step re-runs it
  with `run.py --isolation host --plan-from`, line by line, on the host. Without `--micro` the report is unchanged.
- **The mechanical bar.** `verify.py` (the develop lane's verification
  step's workflow script, `workflows/_shared/scripts/verify.py`) runs the bar on the tree the sync
  step left: the engine's plan per touched repo, the whole plan (never
  stopping at the first failure), flaky re-runs, the pre-existing proof on
  the base and the untracked test-file checks are mechanical and land in its
  `result.json`.
  A failing suite of an aggregate e2e runner (its `FAIL e2e/<suite>:
  <scenario> — <message>` lines) is one unit re-run on its own, so flaky
  re-runs and the pre-existing proof work per suite: the runner's printed
  `re-run:` command is used verbatim, a `-- <suite>` re-run is derived only
  for the conf's simple `npm run <script>` aggregate, and anything else
  stays not re-runnable (a failure) — unless the conf declares the
  command's **unit re-run template**:
  `rerun=<name>|<template>`, `<name>` a declared conf command (a `check=`
  name or `fast-<i>`/`full-<i>`/`micro-<i>`), `<template>` a `bash -c`
  command run from the repository root like the command itself, containing
  `{unit}`, which is replaced by the failing unit's name as `result.json`
  names it (an aggregate runner's suite: `e2e/<suite>`), shell-quoted. Every
  located failing unit of that command (never the whole-command unit) is
  re-run by the template, at HEAD and on the base. The template must run
  one unit exactly the way the command runs it — the same assignments and
  flags — because its pass is what makes a red flaky; the engine checks only
  the shape (a missing `{unit}`, a `{unit}` inside single or double quotes —
  the name is already shell-quoted where it stands —, an undeclared name or
  a second template for one name is diagnosed and dropped; the first wins).
  In a run that reported a data race, a template without `-race` is no
  re-run: the red stays a failure, as for a derived re-run. This repository
  declares
  `rerun=full-8|cd pi-web && E2E_SERVER_MODE=start npm run test:e2e:all -- {unit}`
  for its pi-web e2e bar (and `base_setup=cd pi-web && npm ci`, so a suite
  re-runs on the base); `test/w403_light_engine_test.go` pins that the
  anonymous name still binds that command. An aggregate runner's closing
  summary — after `===== summary =====` one `PASS|FAIL <suite> <secs>s` line
  per settled suite and a `<p>/<n> passed` tally matching them, `n` equal to
  the suite count the runner announced first (`e2e/all: <N> suite(s)`), each
  FAIL a located failing suite — proves the command complete the way the Go
  harness's trailer does, so a command whose failing suites were all
  explained needs no whole re-run. A command whose output no parser
  reads can declare its runner's JUnit report (`report=<name>|junit|<path>`):
  after a failing run the report this command wrote (a file it created or
  changed, by a stamp taken before it started — never one another command
  sharing the glob wrote) adds one unit per failed testcase the output's
  units do not cover (`<classname>::<name>`, its file from the XML; not
  re-runnable, so never flaky or pre-existing), while a re-runnable output
  unit keeps standing for the testcases it covers; a missing, unchanged,
  unparseable or failure-free report leaves today's output parsing — and,
  when that locates nothing, the whole command as the one unit — with the
  reason on the command's `report` entry in `result.json`.
  The verification agent routes the result (skill `verify`); a touched repo the engine
  plans nothing for is listed unplanned — declaring the bar in the repo's
  conf removes that case.
- **Who bounds the run.** `verify.py` runs the bar under an explicit
  whole-run deadline, its `--deadline` (default: two minutes under the
  step's tool window); the per-command default timeout (20m)
  and the e2e suites' own timeouts do not move. `result.json` records it as
  `deadline`.

### Carries — declaring what an e2e suite covers

The npm, python and rust analyzers select e2e/acceptance suites by
declaration, not by guessing. A suite declares the repository paths it covers
in its **opening comment block** — the leading blank and comment lines before
its first line of code:

```js
// carries: components/FilePanel*, lib/file-panel*
```

(`# carries:` in a python suite; several `carries:` lines union; globs are
separated by commas or blanks and use the conf's shell-case syntax, `*`
crossing `/`.) A `carries:` line after the first line of code is not a
declaration.

- **Suites.** The analyzer's default discovery — npm `e2e/*.mjs`, python
  `test_*.py` / `*_test.py` under `e2e/` or `tests/e2e/` (pytest's collection
  rule: `conftest.py` and `__init__.py` are not suites), rust top-level
  `tests/*.rs` (`tests/common/mod.rs` is a helper) — or the conf's
  `suite_glob=` globs, minus `exclude=` matches and the `e2e_runner=` target.
  A discovered file that another module imports (relative JS imports, python
  imports, rust `mod` declarations, followed transitively) and that declares
  nothing is a helper, not a suite. The layer is on only in a repository that
  holds at least one suite; a suite-less repository's report is unchanged.
- **Selection.** A changed path selects every suite whose globs match it; a
  changed suite selects itself; a changed helper selects every suite that
  imports it, transitively. The selected suites run as one `e2e_runner=`
  command (the suites appended in stable order) or, without the key, as one
  `node <suite>`, `pytest <suite>` or `cargo test --test <name>` each — after
  the analyzer's own scoped commands.
- **At most 3 suites per path.** Each path — the declared globs expanded to
  the repository's files — is declared by at most 3 suites, so a small change
  selects a few suites rather than dozens. A shared file (the locale tables,
  the app shell) is declared only by the 1–3 suites that exercise it
  directly; every other suite still runs in the full bar, and a shared
  file's own unit tests guard it in between. Pi Web enforces the rule with
  an ordinary unit test, `pi-web/lib/e2e-carries.test.mjs`, which fails on a
  path more suites declare and names them; the engine itself never falls
  back to the full tier over it.
- **Full-tier e2e selection (opt-in).** A repository opts in by declaring
  its smoke suites, one repeatable `smoke_suite=<suite>` line each; no
  `carries:` header alone opts a repository in. Only then, only when the
  caller asked for the full tier (`--full`, which full-tier verification
  passes) and only for a non-empty diff, the full tier's e2e phase is a
  selection instead of the full tail; its fast gates are unchanged. The
  selected suites are the carries selection above union the smoke suites,
  sorted, run as one `e2e_runner=` command (else one default command each),
  and the JSON `e2e_selection` records why, in one of three classes:
  `carries` — the carries-selected suites union the smoke suites; a changed
  non-documentation path no declaration covers is listed in
  `uncovered_paths` and does **not** force the full tail; `smoke` — every
  changed path is documentation or an `exclude=` match, so only the smoke
  suites run; `full` — a forced-full match (the analyzer's config files or a
  `forced_full=` glob), a changed path outside a nested project root, a
  change to `.polyforge/verify-scope.conf` itself, or
  a structural diagnostic (any reason of "Fail closed" below except the
  uncovered path, an analyzer without the carries layer, a `smoke_suite=`
  naming no discovered suite, an empty full tail) runs the full tail and each
  reason is diagnosed. `mode:` stays the tier fact (`full`), and
  `e2e_selection.full_commands` always carries the full tail (the full
  command the selection did not run; the integration lane's full gate runs
  the full bar over what landed). The scoped tier is
  unchanged whatever the opt-in — it adds no smoke suite and an uncovered path
  still sends it to `full` — and the release lane never runs this engine
  through the mechanical bar (it runs the repository's own release
  verification), so it is unchanged too.
- **Exemptions.** Only documentation paths (`*.md`, `*.txt`, `docs/`, …),
  `exclude=` matches, a changed suite itself and the helpers suites import are
  exempt from coverage. A unit test changed beside its source is not: a glob
  must cover it.
- **Fail closed.** Any one of these makes the report `mode: full`, each named
  on stderr and in the JSON `diagnostics`: an undeclared suite anywhere in the
  repository; a malformed declaration (an empty `carries:` value, or a
  `carries` line without its colon); an unresolvable import of the graph; an
  `e2e_map=` naming no discovered suite; a changed path that is neither
  documentation, an `exclude=` match, a suite, a helper nor covered by a
  declaration; a changed path outside a nested project root (`../<path>`);
  and an empty or malformed carries key (`e2e_runner=`,
  `suite_glob=`, `e2e_map=`, `smoke_suite=`) in the conf. In the opted-in
  full-tier selection above an uncovered path is listed instead, and every
  other reason here selects the full tail.
- **The conf keys.** `e2e_runner=` (the runner the selected suites are
  appended to), `suite_glob=` (replaces discovery), `e2e_map=<glob>=<suite>`
  (replaces one suite's header outright) and `smoke_suite=<suite>` (the
  full-tier opt-in above), each with its exact form in [the
  config reference](reference/config.md#polyforgeverify-scopeconf--the-verification-bar).
- **Authored suites.** On `develop`, `build` writes a suite as one of its
  units, and review's `facts.py carries` lists a suite it adds or whose run
  a unit records whose `carries:` is missing, malformed, or matches none of
  the paths the build touched under the suite's project root that are not
  test, documentation or forced-full paths, and a suite it only edits whose
  `carries:` is missing or malformed (a test-only change owes only the
  declaration) — a blocking `test_weakened` finding. The workflow's
  readiness check, `workflows/_shared/scripts/scope_ready.py --repo
  <clone>`, names a repository's undeclared suites.

### Analyzer contract

Every analyzer has the same three-tier shape, and a future stack joins by
following it in the engine itself (there is no plugin loader or hook):

1. **Detection.** One manifest selects it; two or more supported manifests
   without a `stack=` pin are `ambiguous` and full with the conf's commands
   only. The manifest and the stack's build configuration force `full`.
2. **Fast.** Cheap static gates that need no test run (`go build`/`vet`/
   `gofmt`, npm lint/typecheck/unit, `python -m compileall -q`,
   `cargo build --all-targets`), replaced wholesale by any conf fast line.
3. **Scoped.** The unit tests a static scan resolves for the diff, then the
   carries-selected suites (the Go analyzer keeps its own harness selection).
   When the scan cannot resolve a changed path's affected tests with
   confidence it reports `full` with a diagnostic, never a guessed narrow
   set.
4. **Full.** The stack's whole test command (`go test ./...`, the npm scripts
   and suites, `pytest`, `cargo test`), replaced wholesale by any conf full
   line.
5. **Suites.** A default discovery rule, an import scan that resolves helpers
   (an unresolvable import is full), and a default per-suite command.
