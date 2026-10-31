## Owner review before merge

A deliverable the owner must look at before it ships — a screenshot matrix, a proof set,
a demo, any material a human has to judge — is shown by the `deliver` step's
owner-review gate. Declare it with one line

    owner-review: <capsule-relative path>[, <path>...] — <one-line description>

in the deliverables-and-carriers section of `artifacts/plan.md` (a plan step), or in your
result's `summary` (a build step, when the artifact set only becomes known while doing
the work). That line is the only switch: deliver then returns `needs_input`
(`deliver-owner-review`) before `gh pr merge`, naming each artifact and the PR, and
merges only after the owner answers 「过目通过」. The artifacts are shown read-only and
never edited for the review. With no such line anywhere, deliver asks nothing — never
declare one for material no human needs to judge.
