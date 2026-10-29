# Work item wrap summary (this step is the workflow's last)

When this step is about to complete (outcome `completed`, a pass verdict), write exactly ONE `experience.wrap` memory for the work item. Put in it only what the project's memory policy says is worth remembering:

Record (worth remembering):
{{record}}

Skip (never in the summary):
{{skip}}

1. Body (≤ 2 KB) in a temporary file outside the capsule (`mktemp`): 做了什么 (what was done), 关键决定 (the key decisions and why), 遗留 (follow-ups, open questions, known gaps), and the lessons that fit a Record entry (a pitfall as 现象 → 原因 → 解法). A lesson that fits no Record entry but that you judge valuable goes in too, on its own line followed by `建议新增条目：<the Record entry you propose>`, and then add `--tags policy-proposal`. Never a secret or credential value — name where it lives instead. On a workflow whose verification ran the mechanical fast lane (execution.json: a light pass — `steps.verification.output.mechanical_pass` — or an escalation — a `light_recheck_escalated` attempt among `steps.verification.attempts`, its `result` archiving the bound, the diff base, the measured diff, the recheck and the reason), the body also lists the fast-lane facts: what was skipped (the full bar and the authored scenarios at the light recheck), on what grounds (the mechanical classification — every failed check static, each reproducible by one deterministic command), the bound (per failed check its repo, its exact command and the files it named), and — on an escalation — why the light path failed (or that a structural gate FAIL abandoned the pending light recheck) and that it fell back to the full verification in place.
2. `pf remember --project {{project}} --type experience.wrap --work-item '{{work_item}}' --title '{{work_item}}: <what was done, one line>' @<file>` — it prints `stored <id>`. A retry of the same command in this step returns the same memory; a second, different summary is refused (summaries are not edited — put everything into the one call). If it fails (aihub down, a refusal it explains), note it in your output and complete the step anyway: the summary is never worth failing the step.
