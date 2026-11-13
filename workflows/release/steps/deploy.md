---
name: deploy
description: Deploy exactly the version the verify step passed (never "latest") by running the repo's own deploy procedure with an explicit confirmation. A project with no deploy procedure deploys nothing and says so; a rollback is a step failure.
---

# deploy

This step deploys the version the `verify` step passed, by running the project's own
deploy procedure. It is a dispatcher: the procedure belongs to the repository and is
never improvised or worked around. It always runs when the project has a procedure;
there is no skip condition. You run every command yourself — the scan, the git calls,
the procedure — in the owner repo (`cd repos/<name> && ...`; name a capsule file from
there as `../../artifacts/...`), and you report what they printed; the daemon records no
command.

## 开工前读什么、核对什么 — before you start: what you read and check

**1. The version.** Read `artifacts/verified-version.json`: `{"version": "V", "commit": "C"}`. `V` is the ONLY
version this step may deploy. Never resolve "latest", never take a version from the WI
text, from a hand-off body or from any other step's output. If the file is missing,
unreadable or empty, hand off `fail`.

Then check it against the release: `V` must equal the `version` of the release step's
record, `artifacts/release-record.json`. That value is a check, never a source. A
mismatch, or a release version you cannot read, is a `fail` (production `not_changed`)
naming both values: nothing is deployed, and the lane ends. Do this before anything
below — a project without a deploy procedure does not skip it. Read verify's and
release's latest sections of `artifacts/handoff.md` for context only.

## 2. Ownership resolution

Scan the capsule's `repos/` directory (one level). A repo is the deploy-procedure owner
when it carries `.polyforge/deploy.sh` (report the scan command and what it printed):

- **Zero owners.** A project may release without deploying. Hand off `done`, the body
  saying no repo carries a deploy procedure, so nothing was deployed (production state
  `none`). This is an honest outcome, not a failure.
- **One owner.** Continue with that repo.
- **Two or more owners.** Stop: hand off `fail` with a `--reason` beginning
  `PF-DEPLOY-AMBIGUOUS_OWNER` and a body that names EVERY candidate. Never pick one
  silently.

## 3. Run it

In the owner repo, put the clone on the verified tree (`git fetch --tags origin` — the procedure reads the
LOCAL tags: the target tag, and for a downgrade the running one,
`git checkout --detach C`, worktree clean), then run exactly this, yourself, in ONE tool
call, and wait for it:

```
set -o pipefail; cd repos/<owner> && .polyforge/deploy.sh V --confirm V 2>&1 | tee ../../artifacts/deploy.log
```

(`V` and `C` substituted). Never add `--force`; never run `--dry-run` as a stand-in for
the real run; if the procedure refuses, that refusal is the result. The run can be long:
this step's tool window is 30 minutes (`budget.tool_minutes`; it rides your process as
`PF_TOOL_WINDOW_MS`, and a claude step's Bash default timeout equals it) — never
background the run and poll it, never run it twice. The whole output is in
`artifacts/deploy.log`; quote its tail in your report. The procedure runs under your own
`PATH`, the daemon's command PATH: set anything else it needs inside the line.
Your dispatch holds the heavy-run admission slot while your process lives
(`resources: { admission: heavy }`): no verification runs beside the deploy. Do not
interrupt it once it has started: the procedure holds a lock and owns its own rollback.
If your process is killed mid-run (a daemon stop, a kill -9), the procedure dies with it:
the next dispatch of this step judges what is serving before anything else.

## 4. Judge the result

The procedure must end with production either on `V` or rolled back to the previous
version, and it reports which.

- Exit zero, and its output reports `V` as the version now serving: production state
  `deployed` — hand off `done` (as the lane's last step, after the wrap summary of the
  step header's Memory section).
- Nonzero exit: hand off `fail`. Name production's state from what the procedure
  printed: it refused before changing anything (`not_changed`), it rolled back and the
  previous version answers (`rolled_back` - production is safe but the deploy did not
  happen, and a human must look), or the rollback itself failed (`rollback_failed`). Put
  the procedure's last output lines and any manual instructions it printed into your body.
  A rollback is a FAIL, never a pass.

Do not attempt to repair production yourself, retry, or run the rollback commands the
procedure printed: a human decides what happens next.

## 交接要写什么 — what your hand-off carries

- **Status.** `done` when production serves `V`, or when the project has no deploy
  procedure (nothing deployed); `fail` on a version mismatch, an ambiguous owner, a
  refusal or a rollback (this gate has no `on_fail`: a FAIL ends the work item).
- **结论**: production's state — `deployed` (`V`), `none` (no procedure), `not_changed`,
  `rolled_back` or `rollback_failed` — and the version serving (and the previous one when
  the procedure printed it).
- **做了什么**: the scan and what it printed; the procedure line as run and its exit; the
  tail of `artifacts/deploy.log`.
- **验证**: `V` from `artifacts/verified-version.json` against the release record's
  `version`.
- **给下游与未决**: on a failure, the manual instructions the procedure printed.
- **Evidence**: `artifacts/verified-version.json`, `artifacts/release-record.json`, and
  `artifacts/deploy.log` when it exists.

## Honesty

Report only what the procedure's own output and exit code showed. Never print secrets
that appear in its output; summarize instead.
