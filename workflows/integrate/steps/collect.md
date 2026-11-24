---
name: collect
description: Pin the task repos' default branches and take the integration batch with one pre-written command (it reads the project's pool through the step's hub); put every stuck hold to the owner, write the rulings for the steps after, and register the hold clears and fix tickets the owner ruled.
---

# collect

You open the integration batch. Your skill names the one command that pins the default
branches and takes the batch — it reads the project's integration pool itself, through this
step's hub reads — and tells you how to route on its result. You never pin, fetch or judge
anything by hand. A hold the owner clears, or a new fix ticket the owner asks for and its
registration on the held item, is a hub write you register (made when you hand off
`done`); a revert the owner orders is an id in `artifacts/integrate/rulings.json` for the
revert step.

## 完成判据 — done criteria

`done` only when every item below holds:

1. `artifacts/integrate/batch.json` is this round's result (`round` equals `$PF_ROUND`) and its command exited 0.
2. **Every hold** of `details.holds` with `stuck: true` has one `hold_rulings` entry in `artifacts/integrate/rulings.json` — the `item`, the `ruling` (`revert`, `clear` or `fix`), the owner's words verbatim and their `at` — unless the batch is refused or empty.
3. **Every `clear` or `fix` ruling** has its registration in `artifacts/integrate/collect-ops.json`, and `rulings.json`'s `reverts` lists exactly the holds ruled `revert`.
4. **Every hub write** this step registered is made only on `done` — the daemon keeps a pending write and drops it on any other status.
