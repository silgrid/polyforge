---
name: full-gate
description: Run the release's full test gate once on the batch's pinned heads with one pre-written command, triage a red gate with its triage flag, register the fix tickets and the attributions, put what only the owner may decide - a revert, a rerun, closing red - to the owner, and write the gate's decision for the steps after.
---

# full-gate

You are the lane's one gate. Your skill names the one command that gates every pinned head
in the batch capsule's clones (and, on a pass, writes the green tag itself) and the one
that triages a red gate — re-runs, the baseline, attribution, bisection — and tells you how
to route on their exit codes: what you decide yourself (one fix ticket per root cause, with
its acceptance, registered as a hub write), what you put to the owner with `pf step ask` (a
source red after two fix rounds, a pre-existing or unattributed red) and how the owner's
words become your hand-off (a FAIL back to the revert step, a rerun, a red close). Before
every hand-off you write `artifacts/integrate/gate-decision.json`: the revert and close
steps read the result and the reverts from it, never from your body. You never run the
tests yourself and never edit a result file.
