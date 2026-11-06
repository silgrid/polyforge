## Owner review before merge

A deliverable the owner must look at before it ships — a screenshot matrix, a proof set,
a demo, any material a human has to judge — is shown by the `publish` step's
owner-review gate. Declare it with one line

    owner-review: <capsule-relative path>[, <path>...] — <one-line description>

in the deliverables-and-carriers section of the plan you write (a step that writes one), or
in your hand-off body's 给下游与未决 (a step without a plan, or when the artifact set only
becomes known while doing the work). That line is the only switch: publish then asks the
owner (`pf step ask`) once the PRs are open, before the lane's `merge` step merges them — naming each
artifact and the PR — and completes only after the owner answers 「过目通过」. The artifacts are shown read-only and never edited for the review. With
no such line anywhere, publish asks nothing — never declare one for material no human needs
to judge.
