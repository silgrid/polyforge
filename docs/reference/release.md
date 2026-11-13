# Release, build and test

How a release is cut, what the build and test surface is, and where each
authority lives. The [concepts page](../concepts.md) defines the terms
(bundle, lane, gate); the unattended-mode policy that releases never
merge unattended is there too.

## Release flow

The platform ships with zero release-specific code in its engine: a
release is a work item filed against the project-agnostic `release`
workflow bundle (`workflows/release/`), and the delivery lanes
(`develop`, `micro`) never release or deploy. Trigger it by filing a release work item (`pf file --bundle
release ...`, or ask the routing skill: "release" / "deploy" /
"发版" files that work item). If the goal names a bump (`major`, `minor`,
`patch`) it is passed verbatim to the repo's procedure; otherwise the
repo default applies.

The bundle runs three gate steps — `release`
(`.polyforge/release.sh [bump]`, after the deploy procedure's read-only
`.polyforge/deploy.sh --preflight` when the project has one and, on a first
run, a read of the README, install and upgrade docs against the changes since
the previous release tag — a mismatch is a question to the owner, never an
edit), an independent `verify`
(`release.sh --verify <version>`; only a pass writes the version
`deploy` reads) and `deploy` (`.polyforge/deploy.sh <version> --confirm
<version>`; a project without `deploy.sh` releases without deploying).
A failure terminates the work item: published tags, releases and images
are immutable, so there is no rewind into a re-release. A claimed version
is never burned, though: when `origin_head` already carries exactly one
release tag `V` - an earlier attempt or work item that died after its
claim, a release whose deploy failed, a hand release - the `release` step
resumes `V` instead of refusing it. A complete `V` (`release.sh --verify V`
passes) is adopted without running the procedure: the release record
carries `resumed: true` and `notes=<location>` = the published release
page URL, which `verify` checks, and `verify` audits it again. A partial
`V` runs the procedure, which resumes it (`release.sh`: what is published
is checked and kept, only what is missing is published - no second
version of the commit is ever cut). `verify` accepts `V` in the
pre-release tag list only for a resumed release. Two release tags on
`origin_head`, or a tag an earlier attempt of the same work item left on
another commit, still refuse with `PF-RELEASE-ALREADY_RELEASED`; a
procedure that cannot resume ends `PF-RELEASE-INCOMPLETE`. Two halves
take effect at different times: `release.sh`/`deploy.sh` act from the
merge on (the step runs the procedure from origin's head), while the step
text (resume or adopt, the deploy preflight, `verify`'s check (b)) reaches
a work machine only with its next daemon install; until then a re-filed
release work item still refuses a tagged head with
`PF-RELEASE-ALREADY_RELEASED` before calling the procedure - run
`release.sh` from the same commit by hand to resume. The
step-by-step checks and failure codes are in
[workflows/release/README.md](../../workflows/release/README.md)
and its steps.

How the lane runs the procedures (workflow contract v2, change 9): each
step's own agent runs its procedure itself — in the owner repo, inside one
tool call, under the daemon's command PATH (the step's own `PATH`: the
`[daemon] command_path` the daemon's own in-process commands run under,
its own PATH when unset), the output teed into the capsule
(`artifacts/release.log`, `artifacts/deploy.log`); the step's report is
what the procedure printed, and the next step
re-derives everything from the remote. What an operator sees: the
`release` step declares `resources: { admission: heavy }` and
`budget: { wall_minutes: 135, tool_minutes: 120 }`, so its dispatch takes a
heavy-run admission slot — the queue the daemon's verification runs wait
in, `[daemon] max_concurrent_bars` — before its process starts and holds it
until the process is gone: **a release waits for running verifications**
(the work item shows `verify_queued`, "heavy-run slots full", like a queued
verification) and no verification starts beside the procedure; and it runs
under a 120-minute tool window (`PF_TOOL_WINDOW_MS`; a claude step's Bash
default timeout equals it), sized for a procedure that runs the full gate
itself, inside a 135-minute wall (T = 405 minutes across its dispatches).
`deploy` likewise holds the heavy admission under a 30-minute window;
`verify` declares a 30-minute wall and no resources. Downstream verifies
upstream: `verify` first checks that `release-record.json` parses and names
the version and its commit, `deploy` that `verified-version.json`'s version
equals the one the release script's result file names (never a version
copied out of a `handoff.md` body) — each skill's read-and-verify-first
section says what to check, and a check that does not hold is the gate's
FAIL. `deploy` hands off `done` (contract 3's `defer`, for a project with
no deploy procedure, is `done` with the reason in its body). `release`
and `deploy` name `model_tier: mid`; the lane's tiers are placeholders, so
their models come from the workspace yaml (`steps.<project>.<step-id>.models`,
then `tiers.<project>.mid.models`) or the project default ([config](config.md),
"Tiers"). `verify` names no tier: it runs on its step key or the project
default, the strong model.
No gate of the lane is waivable (`waivable: false`) and none declares
`on_fail`: a FAIL ends the work item, and a `waive` hand-off is refused. A step process killed mid-procedure (a daemon stop, kill -9) takes
the procedure with it; the step's next dispatch reads what was published
and resumes the claimed version.

A failed `deploy` ends the work item failed with production on the old
version (rolled back, or refused before touching it). Redeploy the
already-verified version by running `.polyforge/deploy.sh <version>
--confirm <version>` directly (a human or an agent), or file the release
work item again: its `release` step adopts the complete release, `verify`
audits it and `deploy` deploys it - no duplicate version is cut.

Per-repo procedure contract (the repo owns the procedure; the bundle
only dispatches):

- `.polyforge/release.sh [bump]` releases origin's default-branch head,
  exits non-zero on any failure, and prints `version=<tag>`,
  `commit=<sha>`, `notes=<location>` lines at the end.
- `.polyforge/release.sh --verify <version>` is a read-only audit of a
  published release (format, ordering, artifacts, integrity); exit 0
  only if all pass.
- `.polyforge/deploy.sh <version> --confirm <version>` deploys exactly
  that version; production ends on it or rolled back, rollback exits
  non-zero.

This repository's own procedures are the authority for everything they
do — the version scheme, the PR-only check, the test gate, the
artifacts, the deploy lock, backups, the upgrade-SQL switch window,
automatic rollback and the machine preconditions: read the header
comments of [.polyforge/release.sh](../../.polyforge/release.sh) and
[.polyforge/deploy.sh](../../.polyforge/deploy.sh). In short:
`release.sh` (vX.Y.Z, or vX.Y.Z-<name>.N with the suffix configuration
below; patch bumps only — no argument means `patch`;
`minor`/`major` are refused, a middle-number jump needs an explicit
owner ruling; the one named exception is the version-line cutover: a
`v2.20.x` floor, or the existing public `v0.1.x` floor, releases exactly
`v0.2.0`, the unified product line's first tag, after which the floor is
 the highest `v0.2.x` tag and
`--verify` judges a `v0.2.x` tag inside that line, taking the highest
`v2.20.x` tag (public `v0.1.x` when no retired internal tag exists) as
`v0.2.0`'s previous release; every other version compare
of the product ranks the `v0.2.x` line above the whole retired line:
`tools/install.sh` accepts any `v0.2.x` release over an installed
`v2.20.x` pf, `deploy.sh` treats a running `v2.20.x` → a `v0.2.x` target
as an upgrade, the AIHub's unsigned-heartbeat gate counts a `v0.2.x`
daemon as at or after the signing release `v2.20.8`, and Pi Web's pf
capability floors count a `v0.2.x` pf as newer than every `2.20.x` floor;
it is all transition code, deleted together once no `v2.20.x` release is
the current line anywhere — every other downgrade stays refused) is the one release entry for every artifact of a tag: it
publishes the five payload assets — `pf-linux-amd64`, `pf-darwin-arm64`,
`pf-darwin-amd64`, `aihub-image.digest` and the Pi Web package
`silgrid-pi-web-<tag>.tgz` — plus `SHA256SUMS` covering those five and
`release-manifest.json` (the tag, the commit, every payload with its
sha256 and the aihub-first ordering statement), and
`ghcr.io/silgrid/aihub:<tag>`, built from the linux `pf` binary with the
entrypoint `pf aihub` (the image stays linux/amd64-only). Pi Web
(`pi-web/`, its own MIT npm package) is packed by the same entry from the
released commit's tree: `npm ci`, `npm run build` with
`PI_WEB_RELEASE_VERSION=<tag>`, the tag's version stamped into the packed
metadata after the build and before `npm pack` — `pi-web/package.json` on
`main` keeps its development value, so the tag is the only version
source and there is no second version line. The aihub image and the pf
binaries are published with the tag before `tools/install.sh` can consume
the release (aihub first); `release.sh --verify` checks the exact asset
set, `SHA256SUMS`, the manifest and the packed Pi Web version. Before
anything is built, `release.sh` checks the workflow contract against the
previous release: while `internal/common/workflow/contract-pending.md`
registers an incompatible change and `ContractVersion` is still the
previous release's, it refuses and names the bump to make; a bump releases
only by one and with the list emptied; an empty list needs no bump and no
drain ([workflow contract](workflow-contract.md), "Versions and the
drain"). There is one
binary, `pf`: the server runs as `pf aihub`, the work machine as the CLI
plus `pf daemon`. The same tag is the same build for both roles, but
promoting the server and upgrading work machines stay separate, manual
decisions — a tag that changes only the daemon does not require the
server to follow; an upgrade order is needed only for a tag that changes
the daemon↔aihub wire contract. `deploy.sh` promotes that image onto the
aihub service host with seconds of downtime (no startup auto-migration,
see [upgrade.md](../upgrade.md)), reading the new server's version with
`pf aihub -version`. Deploying a work machine is re-running
`tools/install.sh` ([install](../install.md)).

`deploy.sh` is component-aware behind that one entry:
`.polyforge/deploy.sh <version> --confirm <version> [--component
aihub|pi-web]`, reachable by the release workflow's deploy step and a human
operator (both need the confirmation). `--component` defaults to `aihub`,
so every invocation without it keeps its meaning: the AIHub path above,
unchanged. `--component pi-web` deploys the Pi Web service on the machine
it runs on: it drives the package's own channel `pi-web/deploy-piweb.sh`
with `PIWEB_INSTALL_REPO` (the release repository, `GH_REPO`) and
`PIWEB_INSTALL_VERSION=<version>` — the release's `silgrid-pi-web-<tag>.tgz`
is downloaded, checksum-verified, installed and the tmux service swapped and
probed — and runs no AIHub step at all (no AIHub preflight or variable, no
ssh, docker, compose, SQL, backup or AIHub health probe). It reports
`previous_version=`, `version=` and `state=deployed`, `not_changed` (failed
before the service swap) or `changed`; `--dry-run` prints the plan and
changes nothing (it does not download; the read-only integrity check of a
release's assets, the Pi Web tarball included, is `release.sh --verify
<tag>`). A Pi Web-only change publishes the unified release but need
not redeploy AIHub.

Both procedures check their machine before anything is published.
`release.sh` runs a preflight after computing the version and before its
gates — the tools, the node major against `.nvmrc`, and the `ghcr.io` push
credential (docker logs in with gh's token when that token carries
`write:packages`, replacing a stale stored login) — and reports every
problem in one message. `deploy.sh --preflight [--component aihub|pi-web]`
takes no version and changes nothing: the required variables, the local
tools, gh's login, ssh `BatchMode` to `AIHUB_HOST`, the server tools,
docker compose and the running container, every problem in one message;
the release workflow's `release` step runs it before releasing.
Both procedures refuse, before any work, a GitHub repository that is not
the one the checkout's remote names (url and pushurl): `release.sh` with
`RELEASE_GH_REPO` (default `silgrid/polyforge`), `deploy.sh` with
`GH_REPO` (same default). A remote that is not a github.com URL refuses
unless `RELEASE_REMOTE_IS` / `DEPLOY_REMOTE_IS` states the repository. A
resumed release re-reads the GitHub release and the registry right before
each publish action and refuses, with nothing more pushed, if either
changed since its first read.

The version-line cutover on the aihub component: with a `v2.20.x` server
running, a `v0.2.x` target is an upgrade, not a downgrade — no `--force`,
and its upgrade-SQL plan is the `deploy/sql/<tag>` dirs in (running,
target] by the line-aware rank, i.e. only the `v0.2.x` dirs, never the
retired `v2.4.0`…`v2.20.x` dirs below the running version. An ordinary
downgrade (`v0.2.2` → `v0.2.0`, `v2.20.22` → `v0.1.9`) still needs
`--force` and is still refused across a schema change.

A suffixed release version `vX.Y.Z-<name>.N` is ordered by SemVer 2.0
precedence wherever the product compares versions — `tools/install.sh`'s
downgrade refusal, `deploy.sh`'s upgrade/downgrade check and its
upgrade-SQL plan, the AIHub's unsigned-heartbeat gate and Pi Web's pf
capability floors: the core numerically (with the line-aware rank above),
then a pre-release below its core release, pre-release identifiers one by
one (numeric ones as numbers and below alphanumeric ones, alphanumeric ones
in ASCII order, a shorter prefix first). So `v0.2.0-<name>.9` →
`v0.2.0-<name>.10` → `v0.2.1-<name>.1` are upgrades, `v0.2.0-<name>.2` →
`v0.2.0-<name>.1` and `v0.2.12` → `v0.2.0-<name>.1` are downgrades, and
`deploy.sh`'s SQL plan for (running, target] follows the same order. A
running version that is not a release tag (`dev`) has nothing to compare:
the installer installs over it, while `deploy.sh` still refuses when it
cannot read a release tag from the running aihub.

`release.sh` cuts such a version only in a repository that carries the
optional `.polyforge/release.conf` (`suffix=<name>`, one alphanumeric
identifier starting with a letter; `upstream=<the public repository's git
URL or path>`); without the file it releases exactly as above. With it,
the version is `vX.Y.Z-<name>.N`: `vX.Y.Z` is the highest `vX.Y.Z` tag of
`upstream` (`git ls-remote`) whose commit is in HEAD's history, `N` is one
more than the highest `N` of `vX.Y.Z-<name>.N` on origin (1 when there is
none). Plain `vX.Y.Z` tags on origin — an older line, possibly with the
same names as public releases on other commits — are never read, and no
upstream tag is fetched into the local tags, so they neither change the
version nor conflict with it. The previous release (the PR-only and
contract checks, the generated notes) is the highest `-<name>.N` tag below
the version on origin, else the public base tag, each at the commit its own
`ls-remote` reports; resume and `--verify` judge the same shape the same
way, and the version-line cutover does not apply. The name lives only in
that file: the procedure names none.

```
 RELEASE: a work item on the `release` bundle (no platform release code)
 +-----------------------------------------------------------+
 | release (.polyforge/release.sh) -> verify (fresh judge,   |
 | release.sh --verify) -> deploy (.polyforge/deploy.sh)     |
 +----------------------------+----------------------------+
                              |
                              v
               mono-repo git tag (single semver per tag)
                              |
                              v
     GitHub release: pf-linux-amd64, pf-darwin-arm64,
     pf-darwin-amd64, aihub-image.digest,
     silgrid-pi-web-<tag>.tgz, SHA256SUMS, release-manifest.json
                              |  +  ghcr.io image <tag> (linux/amd64,
                              |     pf, entrypoint `pf aihub`)
                              v
 DEPLOYMENT: `deploy.sh` for the aihub host; re-run `tools/install.sh` on work machines
 +-----------------------------------------------------------+
 |  fetch+verify -> refuse if jobs run -> stop -> swap -> ensure    |
 +-----------------------------------------------------------+
```

## The test gate

One gate definition serves both the integrate workflow's full verification
and the release:

- **What runs.** The tree's own scope-engine plan, `workflows/_shared/scripts/verify-scope
  --json --full --base HEAD` — the conf's fast gates, its full commands and
  the engine-appended dependency audit, in that order — with every command,
  the engine included, inside the container built from
  `.polyforge/Dockerfile.verify`. The tree's own workflow script runs it,
  `python3 workflows/_shared/scripts/gate.py --isolation container`
  (`cmd/pf-gate` before), through the workflow scripts'
  container executor (`pfstep.container`): the image tag
  `pf-verify-<clone name>:<hash>` (an image a verification built is
  reused), the clone mounted alone at its own path, the repository's cache
  volume, the environment allowlist, the limits and the 20-minute
  per-command timeout. The container is the only isolation a gate has: a
  tree without `.polyforge/Dockerfile.verify` at HEAD is not judged (exit
  2), never run on the host. The limits are the script's own flags
  (`--cpus` / `--memory` / `--pids`), else its defaults 8 capped at the
  host's core count / `16g` / 4096 — the machine's pf CLI config is no input
  of the gate (design r2 D7); a malformed flag is refused (exit 2) before
  any container starts. The gate passes only when
  every planned command ran and exited 0: a declared prerequisite the image
  lacks fails its command, nothing is retried, and a gate that cannot be
  judged — no Dockerfile at HEAD, no docker engine, a failed image build, a container that does
  not see the clone at its path (a nonce written into the clone's `.git`
  must read back inside), no plan, its deadline, an interrupt (SIGINT /
  SIGTERM), a container the gate itself removed while a command ran, a HEAD
  that moved during the run — exits 2, never a pass and never a failure.
  Nothing of the gate runs on the host, so a host-only difference no longer
  splits the verdicts: on 2026-11-08 the release's host-run `make test`
  failed `TestW348CellAddedAtRuntimeByReload` because the host had a docker
  client the container does not, while the containerized full bar of the
  same tree passed. `make test` stays a developer target; no release step
  runs it.
- **Its container's life.** The gate removes its container when it ends,
  at its deadline and on an interrupt. A gate killed outright (SIGKILL)
  cannot, so the container carries its deadline (label
  `pf.verify.expires`) and a keep-alive that ends the container — with
  whatever command still runs there — five minutes after it, the engine
  then removing it (`--rm`); and every gate's start removes its owner's
  containers (`pf.verify.daemon=pf-gate`) whose recorded deadline has
  passed.
  One still within its deadline may belong to a running gate and is left
  alone.
- **Running it.** From a clone's root — its `.git` a directory inside it,
  the docker engine seeing the clone at the same path (the container
  bind-mounts it there):
  `python3 workflows/_shared/scripts/gate.py --isolation container
  [--keep-going] [--log-dir DIR]` (`--help` is its full reference). Exit 0:
  every command passed; 1: a command failed (its log tail is printed); 2:
  not judged. It needs python3 (≥ 3.9) and no build. `--plan-only` runs no command
  and prints the plan the gate would run — computed in the container as a
  run computes it — with its digest and the runner identity, as JSON.
- **The gate id.** `tools/gate-id [<rev>]` prints the sha256 of the text
  `polyforge-gate-id 1`, `dockerfile <sha256 of .polyforge/Dockerfile.verify>`
  and the conf's gate lines — every line whose key is `stack`, `fast_gate`,
  `full_command`, `check`, `check_requires_env`, `check_requires_cmd` or
  `check_timeout`, leading whitespace removed, in file order — each line
  ending in a newline, both files read from that commit (default `HEAD`);
  `--show` prints the hashed text. Comments and every other key leave it
  unchanged; the engine, the tests and the code are pinned by the commit a
  green tag names, not by the id.
- **The plan digest.** A gate run records the sha256 of the plan as
  executed (its compact JSON encoding, the keys in the order below,
  non-ASCII as UTF-8): the image's content hash
  (the `<hash>` half of the tag), the container's cpus / memory / pids and
  every command in order with its name, phase, command line, declared
  `requires_env` / `requires_cmd` and the timeout it runs under; the
  clone's own path inside a command line (the engine's appended audit names
  it) reads `<clone>`, so where a clone lives never changes the digest.
  The runner identity beside it is `gate.py` and its
  `GATE_RUNNER_VERSION` (`2`; the Go runner's was `pf-gate` `1`, whose
  digests were of another encoding: a green tag it wrote is never reused), bumped
  whenever the verdict rules or the digest's input change.
- **Green tags.** The releasable marker is an annotated tag
  `green/<YYYYMMDD>-<n>` on the verified commit (the UTC date of
  `verified_at`; `n` is one past the highest number of that day among
  every name under `refs/tags/green/<YYYYMMDD>-` on the remote — a
  lightweight or malformed one included), whose message is one line of
  JSON:
  `{"commit":"<sha>","gate_id":"<id>","runner":"gate.py","runner_version":"<v>","plan_digest":"<sha256>","verified_at":"<YYYY-MM-DDTHH:MM:SSZ>","summary":"<text>"}`.
  Only the gate writes one: `gate.py --isolation container --mark-green
  --summary <text> [--remote <r>]` refuses a dirty worktree, a missing committer identity or
  an unreadable remote before it runs, and after a pass pushes the tag for
  HEAD — the only ref it writes on the remote is `refs/tags/green/…`,
  never a branch, never forced; a commit already carrying a tag of the same
  record is not tagged again; a pass whose tag could not be written exits 3.
  `tools/green-tag` only reads: `tools/green-tag check <commit>` (the
  commit must be HEAD, the worktree clean) exits 0, printing the tag, iff
  the commit carries a green tag on the remote that records that commit,
  HEAD's `tools/gate-id`, and the runner, runner version and plan digest
  HEAD's own `gate.py --plan-only` computes for HEAD now (under the gate's
  default limits; it is run only when the commit carries a green tag at
  all) — exit 1 otherwise, 2 when it cannot tell.
  So a result another runner produced (the daemon's verification, with
  flaky re-runs and pre-existing proofs) or a gate whose plan, image or
  limits differ is never reused. `tools/green-tag latest` prints `<tag>
  <commit> <gate_id>` of the newest one. Only the remote counts: a
  lightweight or local-only tag, a malformed message or one naming another
  commit is never green. Release tags stay `vX.Y.Z` (`vX.Y.Z-<name>.N` with a
  `.polyforge/release.conf`): the version
  computation, the resume rule and the Makefile's default `VERSION` ignore
  green tags.
- **Who marks a commit green.** The integrate workflow: its
  full-gate step runs the same gate with `--mark-green` on every gated
  repo's pinned head (`gate.py --capsule --pins`, the tag's tagger the
  lane's own identity), so a pass of the batch writes the tags. A human can
  still run `gate.py --mark-green` from a clean clone of a commit to release.
- **The residual risk.** A green tag proves its record matches HEAD's gate,
  not who wrote it: anyone allowed to push tags to the remote can still
  hand-write a tag of this shape, and the release would reuse it. Tag-push
  rights on the remote are therefore release rights.
- **In the release.** After the cross-reference check, `release.sh`
  computes the gate id and asks `tools/green-tag check` for the released
  commit: a reusable green tag skips the gate and the run names it;
  otherwise the release runs the tree's `gate.py --isolation container`, its
  logs kept in `artifacts/release-<tag>/gate/`; exit 1 stops the release
  as a failed gate, exit 2 as a gate that was not judged. Either way the
  release notes end with a `## Test gate` section naming the reused tag or
  the run, with the gate id. `RELEASE_TEST_CMD` (for the fake-environment
  tests) replaces the whole gate: no green tag is consulted.
  `release-manifest.json`, the assets and `release.sh --verify` are
  unchanged.

## Release-lane failure classes (2026-10-29 to 2026-11-07)

This classification covers every release-lane work item that
ended failed in that window: 17 `failed_gate` and one deploy rollback (#71),
from each work item's recorded step output and the capsules'
`release.log`/`deploy.log`. `file:line` points at the code before the fix;
every class has a regression test in `test/w372_release_lane_test.go` unless
an earlier change already fixed it.

| class | work items | root cause | fix | regression test |
|---|---|---|---|---|
| A. re-run after a partial or finished release refused | #267 #268 #285 #305 #306 #308 | the step adopted a tag on `origin_head` only with THIS capsule's own pre-release list (`workflows/release/steps/release.md:87-112`), so a continuation work item (new capsule) always ended `PF-RELEASE-ALREADY_RELEASED`; `release.sh:461-464` refused any tagged HEAD | resume: the step adopts a complete release (`release.sh --verify V` passes) or runs the procedure, which resumes `V`; `verify` accepts a resumed tag | `TestW372ReleaseAdoptsACompleteRelease`, `TestW372ReleaseResumeUploadsOnlyMissingAssets`, `TestReleaseAdoptionContract` |
| B. version burned after the claim | #120 (gh release create in another repo), #265 (docker push denied) | the tag push (`release.sh:589`) came before the publish prerequisites were known; after a failure `POSTFAIL` (`release.sh:596-620`) declared the version burned and a re-run refused it (class A) | the same resume finishes the claimed version; the preflight (D) and the repo guard (C) move both causes before the claim | `TestW372ReleaseResumesAfterAFailureAfterTheClaim`, `TestReleaseScriptPostFailTextIsAccurate` |
| C. wrong default repository | #120, #240 (first deploy attempt), #155 (first invocation) | `release.sh` once defaulted gh to the public mirror; the fail-loud fix (`release.sh:170,296`) then cost #155 an attempt; `deploy.sh` defaulted `GH_REPO` to the public mirror (fixed earlier: `deploy.sh:278`, 535ab3ee) | `release.sh` defaults to this repository's release repo and refuses, before any work, a gh repo that is not the one `origin`'s GitHub URL names | `TestW372ReleaseRefusesATagReleaseRepoSplit`, `TestW121ReleaseGHRepoFailLoud`, `TestW241DeployGHRepoDefault` |
| D. machine prerequisite found mid-run | #144 (node major), #265 (stored ghcr.io login that cannot push), #283 (ssh BatchMode) | node checked only inside the test gate (`Makefile:21`); the ghcr.io login was skipped whenever docker's config named ghcr.io (`release.sh:581-586`); ssh checked only at deploy step 0 (`deploy.sh:1230`), after release and audit | `release.sh` preflight before the gates (tools, node major, ghcr.io login from gh's `write:packages` token), every problem in one message; `deploy.sh --preflight`, run by the release step before anything is published (`PF-RELEASE-PREFLIGHT`) | `TestW372ReleasePreflightReportsEveryMissingPrerequisiteFirst`, `TestW372ReleasePreflightRefreshesAStoredGhcrLogin`, `TestW372DeployPreflightCatchesSSHBatchModeBeforeARelease`, `TestW372ReleaseStepRunsTheDeployPreflightFirst` |
| E. PR-only gate logic | #118 (genuine owner-direct commits: the gate was right; allowlist added later), #340 (index-blind `commits/<sha>/pulls`, fixed earlier by 5c7ab5b3) | left over: a failed GitHub read was reported as "without a merged PR" (`release.sh:254,269`) | a failed lookup is "PR lookup failed ... retryable, not a missing PR" (fail closed, before the claim); a merge commit the list lacks is confirmed through the PR its GitHub merge subject names | `TestW372PROnlyLookupFailureIsRetryableNotAMissingPR`, `TestW372PROnlyConfirmsAMergeCommitThroughItsOwnPR`, `TestW341ReleasePROnlyReliableAssociation` |
| F. test gate red | #155 (`deploy_sql_test.go:174`, a test bug since fixed), #184 (`TestW167ReportTailEmptyAndNoFallback`), #353 (`TestV5Kill9ChaosMatrix/quarantine_split`) | the gate ran before the claim but exited through `set -e` without a word (`release.sh:507`); the flaky tests are not fixed here | the gate refuses with "nothing built or published, no version claimed (V is still free)" or, on a resume, "V stays claimed and resumable" | `TestW372FailingTestGateConsumesNoVersion`, `TestW372FailingTestGateKeepsAResumedVersionResumable` |
| G. deploy procedure logic (fixed earlier) | #71 (rollback: the row-count guard read the migration's intended row deletion as loss), #240 #242 (a failed `/pf` probe read as version "directory") | `deploy.sh` post-swap baseline guard; `probe_ctr`'s last-word parse | 75a86952 (`expected_deltas`); 303d5555 and 9a4e9590 | `TestDeployAcceptsDeclaredRowDelta`, `TestDeployRefusesUndeclaredRowLoss`, `TestW243DualLayoutContainerDeploys`, `TestW243OldAihubLayoutPreflight` |

## Build and test

```
make check     # build + vet + fmt + xrefs + test, all packages, + pi-web lint/test
make test      # unit packages, then the e2e suite in parallel (a developer subset)
python3 workflows/_shared/scripts/gate.py --isolation container  # the full verification = the release's test gate, in its container
make xrefs     # design-doc cross-references only
make pi-web-build  # the pi-web package's production build (pi-web/)
make pi-web-lint   # pi-web: npm run lint + tsc --noEmit
make pi-web-test   # pi-web: npm test
make pi-web-e2e    # pi-web: every e2e suite (npm run test:e2e:all)
go build ./...
go test ./... -timeout 20m  # the same, e2e suite serial (over go's 10m default)
```

`make fmt` lists tracked `.go` files only, and `pi-web/go.mod` is a marker
that ends the root module there, so the Go gates never walk
`pi-web/node_modules`. The full verification bar is the union of the Go
bar and the pi-web package's bar
([contributing](../contributing.md)); a `pi-web/` change always runs it
in full.

`make test` runs the `./test` e2e package through
`test/e2e-parallel.sh`: one process per test, `E2E_JOBS` at a time
(default: the CPU count, at most 8), slowest first; `E2E_RUN=<regexp>`
narrows it to matching tests.

`make xrefs` checks the cross-references and section anchors of
`docs/design/polyforge-vnext/` (archived docs excluded); it skips with a
message when python3 is missing. `.polyforge/release.sh` runs the same
check before its test gate ([The test gate](#the-test-gate)) and fails the
release instead of skipping, so
run `make xrefs` whenever a change touches the design docs.

Opt-in tests (skipped with a clear message unless their variable is set):
`POLYFORGE_TEST_SYSTEM_INSTALL=1` runs the installer scenarios, which
write `/usr/local/bin/pf` (disposable hosts only).

Minimal external dependencies: pure-Go SQLite (modernc.org/sqlite),
yaml.v3, goldmark, jsonschema, klauspost/compress, x/sys.

## Contributing and change coverage

Change coverage is tiered by a change's behavior face, and the
disciplines that govern when new coverage must be added, live on the
[contributing page](../contributing.md).