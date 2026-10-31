# deliver — the workflow's delivery step (pf ship is gone)

There is no `pf ship` verb (removed by polyforge#166) and the daemon never
pushes a delivery. Delivery is the last step of the delivery lanes:
`feature-full`, `bug-fix`, `autopilot` and `chore` each end with a
`deliver` step that:

1. merges the latest origin default branch into the work,
2. pushes **only** the feature branch `polyforge/<project>-<seq>` (the slug
   with `-` for `#`, e.g. `polyforge/polyforge-140`) — never main/master,
3. opens a PR for it, or reuses the open one,
4. auto-merges with `gh pr merge --merge` when the review passed with no
   blocking finding, the tests passed, no merge conflict had to be
   resolved by the LLM, and its pre-flight (nothing uncommitted, no release
   residue) found nothing, and the work item declared no owner-review
   artifact or the owner answered 过目通过. autopilot has no review step:
   there verification's pass stands in for the review.

**The owner-review gate.** When the plan or a build step's summary declared
an owner-review artifact (an `owner-review: <path(s)> — <description>`
line: a screenshot matrix, a proof set, a demo), the step first asks the
**human-only** question `deliver-owner-review` before any merge: it shows the
PR links and each declared artifact. The human answers with one of:

```
pf answer <id> --text "过目通过"
pf answer <id> --text "不通过: <what to change>"
```

Only a human answers this question — never answer it on the user's behalf
or on your own. 过目通过 is not a merge decision: every other merge
condition above must still hold, and when one fails the step goes on to ask
the merge question below. A work item that declared no owner-review
artifact is never asked this question.

Otherwise the step asks a **human-only** merge question (`deliver-merge`).
`pf watch <id> --once` shows it; the human answers with one of:

```
pf answer <id> --text "merge"
pf answer <id> --text "don't merge"
pf answer <id> --text "back to code: <what to change>"
```

Only a human answers this question — never answer it on the user's behalf
or on your own, even if the user's earlier wording sounded like consent.

## Triggers

- EN: ship it / deliver / push the branch / open the PR / merge it
- 中文: 推上去 / 交付 / 开 PR / 合进去

## Routing

These requests have no verb of their own. Tell the user delivery happens
in the work item's own `deliver` step, and point at `pf watch <id> --once`
to see where it is (or whether the owner-review or merge question is
waiting for them). If the work item is not running yet, that is
`verbs/submit.md`; if it is waiting on either question, the human answers
it via `verbs/answer.md`.
