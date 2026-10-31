# Release, build and test

How a release is cut, what the build and test surface is, and where each
authority lives. The [concepts page](../concepts.md) defines the terms
(bundle, lane, gate); the unattended-mode policy that releases never
merge unattended is there too.

## Release flow

The platform ships with zero release-specific code in its engine: a
release is a work item filed against the project-agnostic `release`
workflow bundle (`workflows/release/`), and the delivery lanes
(`feature-full`, `bug-fix`, `autopilot`, `chore`) never release or
deploy. Trigger it by filing a release work item (`pf file --bundle
release ...`, or ask the routing skill: "release" / "deploy" /
"发版" files that work item). If the goal names a bump (`major`, `minor`,
`patch`) it is passed verbatim to the repo's procedure; otherwise the
repo default applies.

The bundle runs three gate steps — `release`
(`.polyforge/release.sh [bump]`), an independent `verify`
(`release.sh --verify <version>`; only a pass writes the version
`deploy` reads) and `deploy` (`.polyforge/deploy.sh <version> --confirm
<version>`; a project without `deploy.sh` releases without deploying).
A failure terminates the work item: published tags, releases and images
are immutable, so there is no rewind into a re-release. A re-dispatched
`release` adopts an already-published tag only when all three hold: the
tag is on `origin_head`, this capsule's `artifacts/pre-release-tags.txt`
exists, and the tag is absent from it (an earlier attempt published it,
then died). It then writes the release record for that tag with
`notes=<location>` = the published release page URL, which `verify`
checks, and passes so `verify` audits it; without a capsule list (a hand
release), with the tag already listed, or off `origin_head` it still
refuses with `PF-RELEASE-ALREADY_RELEASED`. The
step-by-step checks and failure codes are in
[workflows/release/workflow.md](../../workflows/release/workflow.md)
and its steps.

A failed `deploy` ends the work item failed with production on the old
version (rolled back, or refused before touching it). Redeploy the
already-verified version by running `.polyforge/deploy.sh <version>
--confirm <version>` directly (a human or an agent); never by filing a
new release work item, which would cut a duplicate version of the same
commit (this repo's `release.sh` refuses that).

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
`release.sh` (vX.Y.Z, patch bumps only — no argument means `patch`;
`minor`/`major` are refused, a middle-number jump needs an explicit
owner ruling) publishes the 8-asset matrix —
`aihub-linux-amd64`, `pf-linux-amd64`, `aihub-darwin-arm64`,
`pf-darwin-arm64`, `aihub-darwin-amd64`, `pf-darwin-amd64`,
`aihub-image.digest`, `SHA256SUMS` (covering all seven files) — and
`ghcr.io/silgrid/aihub:<tag>` (the image stays linux/amd64-only);
`deploy.sh` promotes that image onto the aihub service host with
seconds of downtime (no startup auto-migration, see
[upgrade.md](../upgrade.md)). Deploying a work machine is re-running
`tools/install.sh` ([install](../install.md)).

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
     GitHub release: the linux and darwin binaries (aihub, pf),
     aihub-image.digest, SHA256SUMS
                              |  +  ghcr.io image <tag> (linux/amd64)
                              v
 DEPLOYMENT: `deploy.sh` for the aihub host; re-run `tools/install.sh` on work machines
 +-----------------------------------------------------------+
 |  fetch+verify -> refuse if jobs run -> stop -> swap -> ensure    |
 +-----------------------------------------------------------+
```

## Build and test

```
make check     # build + vet + fmt + xrefs + test, all packages
make test      # unit packages, then the e2e suite in parallel
make xrefs     # design-doc cross-references only
go build ./...
go test ./... -timeout 20m  # the same, e2e suite serial (over go's 10m default)
```

`make test` runs the `./test` e2e package through
`test/e2e-parallel.sh`: one process per test, `E2E_JOBS` at a time
(default: the CPU count, at most 8), slowest first; `E2E_RUN=<regexp>`
narrows it to matching tests.

`make xrefs` checks the cross-references and section anchors of
`docs/design/polyforge-vnext/` (archived docs excluded); it skips with a
message when python3 is missing. `.polyforge/release.sh` runs the same
check before its test gate and fails the release instead of skipping, so
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