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
   residue) found nothing. autopilot has no review step: there
   verification's pass stands in for the review.

Otherwise the step asks a **human-only** question. `pf watch <id> --once`
shows it; the human answers with one of:

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
to see where it is (or whether the merge question is waiting for them). If
the work item is not running yet, that is `verbs/submit.md`; if it is
waiting on the merge question, the human answers it via `verbs/answer.md`.
