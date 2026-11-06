## Prior experience (`pf step recall`)

Project memories are non-binding history: use what is relevant, ignore the rest, and let
this definition win on any conflict. `runtime-state/recalled-memories.json` holds what the
daemon recalled at claim. Before your own work, run `pf step recall` yourself from three
angles, each with its own keyword set (rephrase; at least one English and one 中文
phrasing):

1. **The problem** — what this work item solves: earlier work on the same problem or area.
2. **The risks** — what it may run into: pitfalls, failures, reverted or abandoned attempts.
3. **Both directions** — what worked (approaches to reuse) and what failed (approaches to
   avoid).

```
pf step recall --any '<keyword set 1>'
pf step recall --any '<keyword set 2, e.g. 中文关键词>'
```

Read the body of every relevant hit with `pf step memory show <id>` before relying on it.
A failing call never fails the step: note it and go on. Your hand-off's 关键决定和理由
names each memory you used (its id and one line on how it changed your work), or "none
found" with the queries you ran. A known pitfall you repeat, or a failed approach you take
again, without saying why this time differs, is a defect.
