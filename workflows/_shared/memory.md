# Project memory (`pf recall`)

Project `{{project}}`, work item `{{work_item}}`. Project memories are non-binding history: use what is relevant, ignore the rest, and let the step definition win on any conflict. Inside this step `pf` reaches project memory only through the daemon (you hold no aihub access; other aihub verbs are refused). A failing `pf` call never fails the step: note it and go on.

Before the step's own work, run `pf recall` yourself from three angles, each with its own keyword set (rephrase; at least one English and one 中文 phrasing):

1. **The problem** — what this work item solves: earlier work on the same problem or area.
2. **The risks** — what it may run into: pitfalls, failures, reverted or abandoned attempts.
3. **Both directions** — what worked (approaches to reuse) and what failed (approaches to avoid).

In your artifact, add a short `## Prior experience` section: each memory you used (id + one line on how it changes your work), or "none found" with the queries you ran. A known pitfall you repeat, or a failed approach you take again, without saying why it is different this time, is a defect.

```
pf recall --project {{project}} --any '<keyword set 1>'
pf recall --project {{project}} --any '<keyword set 2, e.g. 中文关键词>'
```

Read the body of every relevant hit with `pf memory show <id>` before relying on it. Steps do not write memories, except the last step's one wrap summary below.
