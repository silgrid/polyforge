---
name: deploy
description: Deploy exactly the version the verify step passed (never "latest") by running the repo's own deploy procedure with an explicit confirmation. A project with no deploy procedure defers; a rollback is a step failure.
output:
  type: object
  required: [verdict]
  properties:
    verdict:
      type: string
      enum: [pass, defer, fail]
      description: pass means the procedure exited zero and production now serves the verified version; defer means no repo in the capsule carries a deploy procedure (the project releases without deploying); fail means the deploy did not complete - refused before touching production, rolled back to the previous version, or (worst case) a rollback that itself failed.
    version:
      type: string
      description: The version now serving. Required non-empty when verdict is pass; absent on defer.
    state:
      type: string
      enum: [deployed, not_changed, rolled_back, rollback_failed, none]
      description: Where production ended up. deployed with pass; not_changed (refused before the swap) / rolled_back (back on the previous version) / rollback_failed (manual action needed) with fail; none with defer.
    previous_version:
      type: string
      description: The version production served before this deploy, when the procedure reports it.
    fail_reason:
      type: string
      description: Required when verdict is fail - the state production is in, what the procedure printed, and the manual instructions it gave if any.
  oneOf:
    - description: A completed deploy - production serves the verified version.
      properties:
        verdict: { type: string, enum: [pass] }
        version: { type: string, pattern: '\S' }
      required: [verdict, version]
    - description: A failed deploy - requires a non-empty fail_reason that states where production ended up.
      properties:
        verdict: { type: string, enum: [fail] }
        fail_reason: { type: string, pattern: '\S' }
      required: [verdict, fail_reason]
    - description: A deferred deploy - no deploy procedure in this capsule, so nothing was executed; carries no version and no published artifacts.
      properties:
        verdict: { type: string, enum: [defer] }
        version: { type: string, maxLength: 0 }
        artifacts_published: { type: array, maxItems: 0 }
      required: [verdict]
---

# deploy

This step deploys the version the `verify` step passed, by running the project's own
deploy procedure. It is a dispatcher: the procedure belongs to the repository and is
never improvised or worked around. It always runs when the project has a procedure;
there is no skip condition. Everything is done with your `bash` tool and every command
is recorded as command evidence (a completed result without evidence is refused).

## 1. The version

Read `artifacts/verified-version.json` (by path convention): `{"version": "V", "commit": "C"}`.
`V` is the ONLY version this step may deploy. Never resolve "latest", never take a
version from the WI text or from any other step's output. If the file is missing,
unreadable or empty, `verdict: fail`.

## 2. Ownership resolution

Scan the capsule's `repos/` directory (one level). A repo is the deploy-procedure owner
when it carries `.polyforge/deploy.sh` (record the scan command as evidence):

- **Zero owners.** A project may release without deploying. Finish with
  `verdict: defer`, `state: none`, and no `version`. This is an honest outcome, not a
  failure.
- **One owner.** Continue with that repo.
- **Two or more owners.** Stop: `verdict: fail` with a `fail_reason` beginning
  `PF-DEPLOY-AMBIGUOUS_OWNER` that names EVERY candidate. Never pick one silently.

## 3. Run it

In the owner repo, put the clone on the verified tree (`git fetch --tags origin` — the procedure reads the
LOCAL tags: the target tag, and for a downgrade the running one,
`git checkout --detach C`, worktree clean), then run exactly:

```
.polyforge/deploy.sh V --confirm V
```

(`V` and `C` substituted). Never add `--force`; never run `--dry-run` as a stand-in for
the real run; if the procedure refuses, that refusal is the result. The run can be long.
Waiting is never a tool call of its own: run the procedure in the foreground with an
explicit timeout at least as long as you expect it to take (the harness allows up to the
daemon's tool bound), its output also written to `artifacts/deploy.log`. Only a procedure
known to exceed that bound may be backgrounded (its output in `artifacts/deploy.log`, its
exit code in a file), and it is then awaited inside one call: a loop that checks a real
condition, sleeps at most 3 s per iteration and has a deadline. The condition is the
exit-code file existing; a deadline that passes first is answered by the same loop in the
next call. A bare `sleep N` call, or a loop whose only exit is the deadline, is a
violation and is recorded. Record the invocation as command evidence with its real
duration. Do not interrupt it once it has started: the procedure holds a lock and owns its
own rollback.

## 4. Judge the result

The procedure must end with production either on `V` or rolled back to the previous
version, and it reports which.

- Exit zero, and its output reports `V` as the version now serving: `verdict: pass`,
  `state: deployed`, `version: V` (and `previous_version` when printed).
- Nonzero exit: `verdict: fail`. Set `state` from what the procedure printed: it
  refused before changing anything (`not_changed`), it rolled back and the previous
  version answers (`rolled_back` - production is safe but the deploy did not happen, and
  a human must look), or the rollback itself failed (`rollback_failed`). Put the
  procedure's last output lines and any manual instructions it printed into `fail_reason`.
  A rollback is a FAIL, never a pass.

Do not attempt to repair production yourself, retry, or run the rollback commands the
procedure printed: a human decides what happens next.

## Honesty

Report only what the procedure's own output and exit code showed. Never print secrets
that appear in its output; summarize instead.

## Output

Emit the final JSON object per the schema above.
