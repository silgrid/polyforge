This step runs the **full** tier by default. A work item opts into the narrower **scoped**
tier only when its content carries the explicit marker line `verification: scoped` — a line
whose trimmed form is exactly that — and the lane is one of `feature-full`, `bug-fix`,
`chore`. `autopilot` always runs full (it has no review, so no `scope_adequate` can exist)
and the `release` bundle never runs this step. There is no other tier input (no free-text
keyword).

- **Scoped (marker present).** Call `pf verify-run --tier scoped` (the step definition's one
call; never run the bar or the engine by hand). Declare the optional `scope` object only
when `result.json`'s `tier.effective` is `scoped`: `mode: scoped`, at least one non-empty
`declared_paths` entry, a `rationale`, and `fast_gates_ref` copied from `result.json`'s
`scope.fast_gates_ref`. The scope you declare MUST be the scope `review` recorded
(`steps.review.output.scope` — the step before you judged the diff against exactly
these `declared_paths`); the driver blocks the automatic merge when the two
declarations disagree, and the delivery goes to a human. The driver rejects a
scoped result whose declaration is incomplete and any scoped claim on a work item that did
not opt in.
- **A scoped run narrows breadth, never the contract.** The run still covers the tests that
reference the changed files (docs-only changes included), and every lane check this step's
vars name stays yours to run — the repro on `bug-fix`, the authored acceptance scenarios on
`feature-full`. A lane check is never shed because it lies outside the declared scope.
- **Fast-gate scope comes from the release-shipped engine.** `pf verify-run` asks the engine
at `<data-dir>/release/current/tools/verify-scope` (`<data-dir>` is `$POLYFORGE_DATA_DIR`
when set, else `~/.local/share/polyforge`, so the default absolute path is
`~/.local/share/polyforge/release/current/tools/verify-scope`) for each touched repo's scope
of the diff against `merge-base HEAD origin/<default>` and runs the fast gates plus the
scoped selection it reports. A repository may narrow or pin that scope with a declarative
`.polyforge/verify-scope.conf`; with no conf the engine applies its conservative zero-config
defaults. Do not embed a per-repo command list here — the engine and the repo conf own it.
- **Any engine failure falls back to the full tier.** A repo whose engine report is `mode: full`
(a scope the engine cannot compute) had its full plan run. A repo the engine failed on, could
not be found for, or planned nothing for is in `unplanned_repos`: `pf verify-run` ran NOTHING
there, and its bar is yours to run, at full (the step definition's unplanned-repos duty).
Either way `result.json`'s `tier.effective` is `full` and the result MUST NOT be scoped —
carry no `scope`. Treat a `scoped` report as advisory, never as permission to skip a lane check.
- **The scoped tier stays opt-in.** Only a work item whose content carries the
`verification: scoped` marker may declare a scoped `scope` object; a work item without the
marker runs full and carries no `scope`.
- **Incremental re-verification.** On a re-entered round of an opted-in ticket — the rewind's
fix already re-passed `review`, which re-recorded the round's scope — the run covers the whole
diff again: `pf verify-run` always scopes `merge-base HEAD origin/<default>`, never only the
fix increment. Declare the scope `review` recorded for this round, with no `scope.prior` (the
run covered everything the prior round did). A **verification FAIL** has no passed prior run
to build on, so it re-verifies full: the driver resets the recorded tier to full, this re-run
renders the FULL section, and a scoped `scope` on it is rejected. The merging credential must
equal the scope `review` recorded for the round (the driver blocks the automatic merge otherwise).
- **The engine's selection base is the merged-in default tip.** `pf verify-run` merges the
latest default branch (the step definition's merge section) before it asks the engine, so
`merge-base HEAD origin/<default>` is the merged-in default tip.
