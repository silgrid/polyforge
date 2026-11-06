---
name: evidence
description: Re-run fix's evidence plan over the synced tree with the pre-written run script (the evidence skill's `run.py --isolation host --plan-from` line), each line on the host from its repository's root, and route on its result file - pass is the script's, a red the change caused FAILs to fix, a red it cannot have caused is the owner's call (ask, then accept the red or have it fixed).
---

# evidence

The lane's machine gate: `fix`'s evidence plan (`artifacts/fix/evidence.json` — the
repository's fast phase and declared micro checks, which `review` judged complete) run
again over the tree as `sync` left it, every line on the host from its repository's root,
by one pre-written script. Your skill is the whole procedure: check the sync's
hand-off, reuse this round's result or run the line as written, route on the exit code,
and ask the owner on a red the change cannot have caused. The result file the script
writes (`artifacts/run/result.json`) is what publish and merge check first; your status
never contradicts it. You are the lane's one waivable gate: a red the owner accepts is your
`waive`, citing the owner's message, and publish reads the words the daemon quotes in your
section's header.
