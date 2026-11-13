# deliver — the workflow's delivery steps (pf ship is gone)

There is no `pf ship` verb (removed by polyforge#166), and no session agent
pushes or merges a delivery. Delivery is the last two steps of both delivery
lanes, `develop` and `micro` (`workflows/<lane>/README.md`):

1. `publish` (an agent step) pushes **only** the feature branch
   `polyforge/<project>-<seq>` (the slug with `-` for `#`, e.g.
   `polyforge/polyforge-140`) — never main/master — at exactly the verified
   head (develop: its `verification`'s; micro: its `evidence` step's, the
   `run.py` re-run of fix's evidence plan), opens a PR for it or reuses the
   open one, and never merges;
2. `merge` merges the PR with `gh pr merge --merge
   --match-head-commit <head>` under the repo's merge lock and lands the work
   item — on both lanes through the merge agent step's `merge.py` (a PR it
   cannot take — closed, moved, refused, a moved default branch that
   conflicts — FAILs back to `sync`; gh unreachable is `incomplete`,
   `pf resume` once fixed). It asks nobody.

The merge goes ahead unattended once publish passes, which it does when the
review passed, the verification (micro: the fast evidence) passed on the
pushed head (or the owner waived its red), and the work item declared no
owner-review artifact or the owner answered 过目通过.

**The owner-review gate.** When the plan or a build step's summary declared
an owner-review artifact (an `owner-review: <path(s)> — <description>`
line: a screenshot matrix, a proof set, a demo), publish first asks the owner
the **human-only** owner-review question (its `pf step ask`, no question id) before
any merge: it shows the PR links and each declared artifact. The human
answers in free text, for example:

```
pf answer <id> --text "过目通过"
pf answer <id> --text "不通过: <what to change>"
```

Only a human answers this question — never answer it on the user's behalf
or on your own. 过目通过 is not a merge decision: it releases publish, and
the merge still needs every other condition above (不通过 rewinds to the
build step). A work item that declared no owner-review artifact is never
asked this question.

## Triggers

- EN: ship it / deliver / push the branch / open the PR / merge it
- 中文: 推上去 / 交付 / 开 PR / 合进去

## Routing

These requests have no verb of their own. Tell the user delivery happens
in the work item's own `publish` and `merge` steps, and point at `pf watch
<id> --once` to see where it is (or whether the owner-review question is
waiting for them). If the work item is not running yet, that is
`verbs/submit.md`; if it is waiting on the question, the human answers it via
`verbs/answer.md`.
