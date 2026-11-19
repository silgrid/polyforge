Before reporting a unit green, also run the written fast/scoped check in each
repo the unit touched: `"$PF_PYTHON" "$PF_SHARED_DIR/scripts/fast_checks.py"
--repo <name> --json --out artifacts/build/fast-checks/<repo>.json` (the engine
in this lane's snapshot; the command runs the engine itself — no command line
of the report is ever copied out by hand). It runs the report's fast and scoped
commands in order — never one whose `phase` is `full` (scoped is not full) —
each in the report's `root`, with each entry's own `timeout`; a command missing
its `requires_env` or `requires_cmd` prerequisite is skipped and its reason
recorded. The result file is the artifact: every command's exit code, log and
skip reason land in `artifacts/build/fast-checks/<repo>.json`, and any
non-zero exit (or a command killed at its own timeout) is a red fast check —
unless every failing unit its output locates passed the one targeted re-run
the command itself now makes (recorded flaky, its first-red log kept) and the
command is confirmed — read its summary, never re-derive the commands by hand.
No re-run is ever made by hand: the check already contains that one re-run — a
failure still red after it is red. The flaky units and their first-red logs
are named in the result's summary and details; a fast check that turned green
through a flaky re-run has its flaky units (with their first-red logs) listed
in the hand-off's Status. A failure confined to
tests an earlier step committed red on purpose (this lane's authored or repro
tests) is the work still to do, not a red fast check. A run that is not judged
(the engine missing or failing: exit 2) is stated in your artifact and leaves
the checks above as the bar. That is the whole per-unit bar.
The full suite is not a per-unit bar — it belongs to `verification` (the one
gate that runs the project's full bar); do not run the full suite (e.g. `go
test ./...`) here, and do not proceed on top of a red fast check.
