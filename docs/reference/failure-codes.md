# Failure codes — the recovery and re-claim paths

The typed failure codes an operator meets when a work item's execution
is interrupted and has to be recovered or re-claimed. The work-item
state machine, session fencing and the full error-code registry
(`CORE-*`, `MEM-*`, `AUTH-*`, `DAEMON-*`, `GIT-*`) live in the code
(`internal/common/api/errors.go`) and the design docs' reference §6;
this page carries the operator-facing meaning of the two recovery-path
codes and the moves they ask for. The concepts (work item, claim,
capsule) are defined on the [concepts page](../concepts.md).

| Code | Meaning | Operator move |
| --- | --- | --- |
| `DAEMON-516` | Re-claim clone refusal: the capsule clone failed for a work item with published progress (or its progress cannot be determined), so a local init — which would restart the workflow at step 1 — is refused. The claim is abandoned back to blocked. | Fix aihub reachability / credentials, then `pf resume <id>`. |
| `DAEMON-518` | A blocked work item's aihub capsule head is not the sealed head, and the claiming daemon is not the sealing one or (on the sealing machine) no quarantined scene holds the sealed head. | Continue it on the sealing machine (it continues from the sealed head), abandon with `pf cancel <id>`, or — when the sealed work is gone — `pf resume <id> --accept-lost-work` (clears the seal; recorded as a `blocked_seal_abandoned` event). |

## Blocked work items

A missing capsule is no longer a startup failure: only the affected work
item is set to the ordinary `blocked` status at restart (the reason note
names the cause — "capsule missing at <path>" or "project <p> is not in
the daemon's [workspace] mapping"; a `capsule_missing` /
`project_unmapped` event shows in `pf watch`); nothing is parked
locally, the daemon starts and every other work item recovers. Restore
the mapping if that was the cause, then `pf resume <id>`: a fresh
re-claim from the aihub capsule chain plus the task-repo carrier refs
(`refs/pf-task/<name>`); the dead scene is quarantined, completed steps
are not re-run, and work published after the last push is lost.

A blocked work item has exactly two exits: `pf resume <id>` (continue)
or `pf cancel <id>` (abandon). `pf wrap` / `pf wrap --fail` do not apply
to a blocked work item — there is no completed run to wrap or fail, and
the daemon refuses with DAEMON-409.

## The other human moves

The full set of human moves on a run that is not simply going (pause,
resume, cancel, the wrap verdicts) is the [user guide's when-things-go-wrong
section](../user-guide.md); the verb shapes are the commands' own
fragments ([commands](commands.md)).