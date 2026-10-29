Before reporting a unit green, also run the release-shipped engine's scoped report in
each repo the unit touched: `<data-dir>/release/current/tools/verify-scope --json`
(`<data-dir>` is `$POLYFORGE_DATA_DIR` when set, else `~/.local/share/polyforge`) from
the repo clone, no other argument: the diff against `merge-base HEAD origin/<default>`.
Of its `commands` (the repo's `.polyforge/verify-scope.conf` or the engine's defaults
decide them), run in order each whose `phase` is `fast` or `scoped` — its `cmd` via
`bash -c`, its `timeout` as the explicit timeout — never one whose `phase` is `full`
(scoped is not full). A red there is a red fast check; one missing a `requires_env` or
`requires_cmd` prerequisite is skipped and named in your artifact. A failure confined to
tests an earlier step committed red on purpose (this lane's authored or repro tests) is
the work still to do, not a red fast check. A missing engine or a non-zero exit is stated
in your artifact and leaves the checks above as the bar. That is the whole per-unit bar.
The full suite is not a per-unit bar — it belongs to `verification` (the one gate that
runs the project's full bar); do not run the full suite (e.g. `go test ./...`) here, and
do not proceed on top of a red fast check.
