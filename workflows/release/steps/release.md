---
name: release
description: Cut a release from the project's most recent green integrate batch by running the repo's own release procedure (.polyforge/release.sh), after precondition checks. A release WI without a procedure is an error, never a defer.
---

# release

This step releases the project's most recent **green integrate batch**: the
batch's pinned commits are the release object, never a branch head. It is a
**dispatcher plus preconditions**, not a procedure: the procedure belongs to
the project's repository, and nothing here may be improvised or worked
around. You run every command yourself — the checks, the git and gh calls,
the procedure — in the owner repo (`cd repos/<name> && ...`; name a capsule
file from there as `../../artifacts/...`), and you report what they printed.
The daemon records no command: the next step does not trust this report and
re-derives everything from the remote.

## 开工前读什么、核对什么 — before you start: what you read and check

- The opening of `artifacts/handoff.md` and `runtime-state/work-item.json`: the goal and
  description, for the bump (section 4). You are the lane's first step: no upstream hand-off precedes you.
- An earlier `release` section of this work item (a dispatch that died and was resumed):
  what it claimed and published. Section 3 re-derives it from the remote either way.
- Your capsule's `artifacts/pre-release-tags.txt`, when an earlier attempt recorded it
  (section 3, item 7).

## 1. Ownership resolution

Scan the capsule's `repos/` directory (one level: `repos/<name>/`). A repo is the
release-procedure owner when it carries `.polyforge/release.sh`. Classify the scan:

- **Zero owners.** No repo carries the file. A release WI without a procedure is an
  error, not a deferral: hand off `fail`, the body saying no repo in the capsule carries
  `.polyforge/release.sh`.
- **One owner.** That repo is the release owner; continue.
- **Two or more owners.** Stop - this is a loud failure, not a choice. Hand off `fail`
  with a `--reason` that begins with `PF-RELEASE-AMBIGUOUS_OWNER` and a body that names
  EVERY candidate repo. Never pick one silently or merge procedures.

## 2. The procedure must support `--verify` and the pins

The next step audits the release by running `.polyforge/release.sh --verify <version>`,
and this step hands the procedure the green batch's pins, so check up front, in the owner
repo, that the procedure knows both (for example
`grep -q -- '--verify' .polyforge/release.sh` and
`.polyforge/release.sh --help | grep -q -- '--pins'`, or read its `--help` output). If it
does not support `--verify`, hand off `fail` saying the procedure lacks `--verify`
support. If its `--help` output does not declare `--pins`, hand off `fail` with a
`--reason` that begins with `PF-RELEASE-PROCEDURE_CONTRACT`: an old convention script
would read the pins file as a bump argument or ignore it and release a branch head -
never hand it one. Do not release something the lane cannot verify, and never hand a
procedure a convention it does not declare.

## 3. Preconditions (the green batch, the pins, the owner repo at its pin)

The release object is the project's most recent green integrate batch: the
batch whose integrate close judged every gated repo green and pinned each
repo's green head. The resolver reads the project's batches through the
step's own hub (the read-only `get` and `list` are this step's hub
whitelist) and validates the batch's pins against the capsule's clones.
Run it once from the capsule root:

```
"$PF_PYTHON" "$PF_SHARED_DIR/scripts/release.py" --capsule "$(pwd)" --json --out artifacts/release-batch.json --pins-out artifacts/release-pins.json
```

Its result file is the resolver's answer, and its `--pins-out` file
(`artifacts/release-pins.json`, the batch's whole group) is the procedure's
input in section 4:

- **verdict pass, decision `fresh`** - the batch's owner pin carries no
  release tag: a normal first run. Continue below.
- **verdict pass, decision `resume`** - the owner pin already carries exactly
  one release tag `V`, whoever claimed it: section 3's item 6 resumes `V`.
- **verdict fail** - the summary begins with a named code and a readable
  reason (`PF-RELEASE-NO_GREEN_BATCH`, `PF-RELEASE-ALREADY_RELEASED`,
  `PF-RELEASE-PIN_OFF_DEFAULT`, `PF-RELEASE-UNPINNED_OWNER`,
  `PF-RELEASE-AMBIGUOUS_OWNER`): hand off `fail` with a `--reason` that
  begins with that code, BEFORE any version is claimed. Nothing has run and
  nothing was published; fixing the cause (running an integrate batch to a
  green close, waiting for the default branch, a human deciding between
  ambiguous owners) and filing the release again is cheap.
- **verdict error (exit 2)** - the summary names an input the resolver could
  not read (the hub, a batch record, a clone, a default branch). Fix what it
  names and run the command once more; still not judged, hand off `blocked`
  naming what the summary says - never guess a release object the resolver
  refused to resolve.

Then:

1. `git fetch origin` (fetch first; never trust the existing clone state).
2. Determine the default branch from the remote (for example
   `git remote show origin | sed -n 's/.*HEAD branch: //p'`, falling back to
   `git symbolic-ref --short refs/remotes/origin/HEAD | sed 's|^origin/||'`). Call it `<default>`.
3. **Sit the owner repo at its pinned commit.** From the resolver's result
   (`artifacts/release-batch.json` names the owner and its pin; the pins file
   carries the same map), check the owner repo out at its pin and require a
   clean tree: `git checkout --detach <pin>`, `git clean -fd` (a release WI's
   clone holds no work worth keeping; the release builds the pinned commit,
   nothing else), then `git status --porcelain` empty and `git rev-parse HEAD`
   equal to the pin. The resolver already checked the pin is on `origin/<default>`'s
   history - a pinned commit the default branch has moved past still releases; that
   is the shape this lane exists for. Anything else: hand off `fail`.
4. Record `origin_head` = `git rev-parse origin/<default>` (a record of the branch's
   tip at release time - the release object is the batch, not this head).
5. **The deploy procedure's preflight.** When a repo in the capsule carries
   `.polyforge/deploy.sh` (the deploy step's owner) and that procedure supports
   `--preflight` (`grep -q -- '--preflight' .polyforge/deploy.sh`), run
   `.polyforge/deploy.sh --preflight` in that repo now, before anything below
   (under your own `PATH`, the command PATH the procedures run under):
   it is a read-only check of this machine and the deploy target, and it
   reports every problem in one message. A nonzero exit is a `fail` hand-off with
   a `--reason` that begins with `PF-RELEASE-PREFLIGHT` and a body that quotes that
   message: nothing was published and no version was claimed, so the lane is
   cheap to file again once the machine is fixed. A lane that released first
   would publish a version and then fail at its deploy.
6. **Already released? Resume, never re-cut.** A version is claimed once and never
   burned. The resolver's decision answers it:
   - **`fresh`** - the owner pin carries no release tag: a normal first run. Continue.
   - **`resume`, `V`** - the pinned commit already carries exactly one release
     tag `V`, whoever claimed it: an earlier attempt of this work item that died,
     an earlier work item (a release that failed after its claim, or one that
     passed and whose deploy failed), a later lane carrying a finished release
     on to its deploy, or a hand release. Resume `V`; never cut a second version
     of the pinned commit and never refuse it as already released. First record
     the pre-release list (item 7) when it does not exist yet - it then lists `V`;
     a list an earlier attempt of this work item kept may lack it. `verify` accepts
     both for a resumed release. After `git fetch --tags origin`, run the read-only audit
     `.polyforge/release.sh --verify V` in the owner repo.
     **Exit zero - `V` is complete.** Do not run the procedure: write
     `artifacts/release-record.json` for `V` (section 5) with `resumed: true` and
     `notes` = its published release page URL,
     `https://<host>/<owner>/<repo>/releases/tag/<V>` (a location `verify` checks
     mechanically), and hand off `done` with `V`, its commit (= the owner pin), `notes`
     and the artifacts `gh release view V` lists in your body; `verify` audits it again
     under its existing checks.
     **Nonzero - part of `V` is missing.** Continue with section 4 and record
     `resumed: true`: the procedure resumes `V` (the procedure contract: on a pinned
     commit that carries its release tag it checks what is published, keeps it, publishes
     only what is missing and reports `version=V`). A procedure that refuses a tagged
     pin instead is a failed procedure like any other: hand off `fail` with a `--reason`
     that begins with `PF-RELEASE-INCOMPLETE`, a body that names `V` and quotes what the
     audit found missing.
   - Two or more release tags on the owner pin, a release tag the kept pre-release
     list lacks on another commit, or a pin no longer on the default branch already
     refused at the resolver's own check (`PF-RELEASE-ALREADY_RELEASED`,
     `PF-RELEASE-PIN_OFF_DEFAULT`); a human decides those before this step runs.
7. Only when the file does not exist yet: record the pre-release remote tag list, which
   the next step needs for its immutability check:
   `git ls-remote --tags origin > <capsule>/artifacts/pre-release-tags.txt`
   (run it BEFORE the procedure; keep the file exactly as git printed it).
8. **The docs against the changes since the last release** (a normal first run only: a resumed `V` was cut already, so skip this item). Take the newest release tag that is an
   ancestor of the pinned commit as `<prev>`
   (`git describe --tags --abbrev=0 --match 'v*' <pin>`, after
   `git fetch --tags origin`) and read what was merged since
   (`git log --first-parent --oneline <prev>..<pin>`, and the diff of that range);
   with no earlier release tag, read the documents against the tree as it stands. Check
   that the repository's README, its install instructions and its upgrade notes agree
   with those changes and name no command or configuration key the range removed. Edit no
   document. On any mismatch, `pf step ask` the owner once — per mismatch the document,
   the line and the change it contradicts — offering to release as is or calling
   the release off (`cancel`, citing the reply; the documents are then fixed by a work item of
   their own and the release filed again). On the owner's go-ahead, start again at the resolver's
   command above (the batch may have moved while you waited: its result and
   `artifacts/release-pins.json` are re-derived, then item 3's checkout) and
   ask only about a mismatch the owner has
   not answered. No mismatch: go on.

## 4. Run the procedure

The repo's procedure owns its own checks (the design-doc cross-reference gate and the
dependency audit it runs itself; it runs no test gate - the green batch's own full-gate
run is the test evidence). Decide the bump argument:

- If the WI goal/description names a bump or version-increment keyword the project's
  scheme uses, pass it **verbatim** as the single argument: `.polyforge/release.sh <bump>`.
- Otherwise pass no argument (the repo's default applies). Never invent a bump, never
  pass flags that skip checks, never use `--force`-style options.

The run is long (builds, audits and publishes). Run it yourself, in ONE tool
call, and wait for it:

```
set -o pipefail; cd repos/<owner> && .polyforge/release.sh [<bump>] --pins ../../artifacts/release-pins.json 2>&1 | tee ../../artifacts/release.log
```

This step's tool window is 120 minutes (`budget.tool_minutes`; it rides your process as
`PF_TOOL_WINDOW_MS`, and a claude step's Bash default timeout equals it), sized for a
procedure that builds, audits and publishes everything it ships: never background the run and poll it,
never split it, never run it a second time because it "seems slow". The whole output is in
`artifacts/release.log`; quote its tail in your report. The procedure runs under your own
`PATH`, the daemon's command PATH (the one the daemon's own commands run under): set
anything else it needs inside the line. Your dispatch holds the heavy-run admission
slot for as long as your process lives (`resources: { admission: heavy }`): the release
waited for running verifications before you started, and none starts beside it — so do
the run first thing in section 4, and finish. If your process is killed mid-run (a daemon
stop, a kill -9), the procedure dies with it and nothing is resumed for you: the next
dispatch of this step starts again from section 3 and judges what was published (the
procedure resumes a claimed version).

A nonzero exit is a release failure: hand off `fail`, never something to work around.
Your body must say what the procedure printed and what (tag, image, release page) may
already exist. Never delete, move or overwrite anything to "clean up".

On exit zero, parse the procedure's final output. It must carry the version string and
the release commit (the contract is `version=<v>`, `commit=<sha>` and `notes=<location>`
lines). If either is missing, or the reported commit is not equal to the owner pin, that
is a failure.

## 5. Record and report

Write `artifacts/release-record.json` with exactly:

```json
{"version": "...", "commit": "...", "default_branch": "...", "batch": "...", "pins": {...},
 "origin_head_at_release": "...", "notes": "...", "repo": "repos/<owner>", "pre_release_tags_file": "artifacts/pre-release-tags.txt",
 "resumed": false}
```

`batch` is the green batch's id and `pins` its whole pinned group - the pins file's
`repos` map alone, `{"<repo>": "<sha>", ...}` (`batch` beside it carries the id; the
after-batches stay in the resolver's result) - the next step checks the release commit
against the pins. `origin_head_at_release` is `origin/<default>`'s head observed
at release time (item 4), a record of the branch the release did NOT take: the release
object is the batch, never this head.

`resumed` is `true` exactly when the resolver's decision was `resume` (section 3 found
the version's tag already on the owner pin). The pre-release list then either lists the
tag (this attempt recorded the list after the claim) or lacks it (an earlier attempt of
this work item recorded the list, then claimed the tag); `verify` accepts either shape
only for a resumed release.

This file is the release step's record: `verify` audits it, and `deploy` checks the
version it deploys against its `version` — the version is read from this file, never
from your hand-off body. Then hand off `done`.

## 交接要写什么 — what your hand-off carries

- **Status.** `done` when the resolver passed and the procedure ran to a zero exit on the
  batch's pinned commits and printed the version and commit you report (or a complete
  resumed release was adopted); `fail` on every failure above (this gate has no `on_fail`:
  a FAIL ends the work item). `cancel` only on the owner's word (`--per-owner`).
- **结论**: the version and the release commit — or what failed, and exactly what (tag,
  image, release page) was already published. The green batch's id and its red successors
  (the resolver's details name them) belong here too.
- **做了什么**: the resolver's line and its verdict; the procedure line as run and its
  exit; whether the release was resumed; everything the procedure reported publishing
  (for a complete resumed release, what `gh release view V` lists) — never invented
  entries; the tail of `artifacts/release.log`.
- **验证**: the precondition checks and what they printed — the resolver's result, the
  docs check's `<prev>`, its range and the documents you read included.
- **Evidence**: `artifacts/release-batch.json`, `artifacts/release-pins.json`,
  `artifacts/release-record.json`, `artifacts/pre-release-tags.txt` and
  `artifacts/release.log` when they exist.

## Honesty

`done` means the resolver passed and the procedure ran to a zero exit on the batch's
pinned commits and printed the version and commit you report. The next step does not
trust this report and re-derives everything from the remote; a `done` that the remote
contradicts ends the lane.
