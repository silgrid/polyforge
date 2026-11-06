# Archived lanes (removed by)

`develop` (`workflows/develop/`, build → review → sync → verification → publish → merge) is the
default development lane and superseded four lanes:

| lane | what it was |
|---|---|
| `feature-full` | grill → spec → grill-review → plan → e2e-author → code → review → verification → deliver |
| `chore` | plan → code → review → verification → deliver |
| `bug-fix` | repro → root-cause → fix → review → verification → deliver |
| `autopilot` | grill → fix → verification → deliver |

**Archive commit:** `0fba43d81a9529ef784a10169a1ea1bbab5cc71d` — origin/main when
`develop` was added. The four bundles as last shipped before `develop` are
`git show 0fba43d8:workflows/<lane>/workflow.md` (and `.../steps/<id>.md`); as last
shipped before their removal, `git show 774970cd:workflows/<lane>/...`.

**Status: removed.** The owner retired the four lanes on 2026-11-09 and
they are no longer installed: a work item still bound to one is refused at claim (the
installed bundle is missing), and a terminal one keeps displaying — its record carries the
bundle name, and a claimed capsule keeps its own snapshot of the bundle.

**Test fixture: deleted.** The frozen copies the Go e2e suite drove
until then (`test/testdata/retired-workflows/`) are gone: the suite's lane-independent
scenarios run on `micro` (its deliver step stripped), the delivery and develop scenarios
on their own lanes, and the driver's unit tests run the generic step machinery on
test-local copies of the shipped lanes.

`micro`, `integrate` and `release` are not affected.
