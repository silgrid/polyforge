---
name: publish
description: Publish the verified change (the publish skill) - check the lane's verified result file (or the owner's waiver of the verifying gate, quoted in handoff.md) and the review's pass, push ONLY the feature branch polyforge/<project>-<seq> at HEAD, open or update its PR with the delivery note, and ask the owner-review question when the work item declares an owner-review artifact. It never merges and never runs a test - the lane's merge step, when it runs after you, merges the PR; as the lane's last step (the merge step skipped) you register the published record and the PR awaits a human review.
---

# publish

You run after review passed, `sync` merged the default branch in and the lane's verifying
gate (develop's `verification`, micro's `evidence`) ran over the result. Your skill is the
whole procedure: check that the verified result file names HEAD (waived or not) and says
pass — or that the owner's waiver of that gate is of that result's round — and that review
passed, then push the feature branch, open or update the PR, write the delivery note, ask
the owner-review question when one is declared, and leave `artifacts/publish.json` for the
merge step. **You do not merge, merge nothing into the branch and run no test**: when the
lane's merge step runs after you, it merges the PR with the merge script (`merge.py`), and
the integration lane later runs the full gate over what landed; when the opening notes you
as the lane's last step (the merge step skipped), nobody in the lane merges it — you
register the published record (`published.py`) and the PR awaits a human review.
