---
name: release
description: Cut a release from the freshly fetched origin default-branch head by running the repo's own release procedure (.polyforge/release.sh), after precondition checks. A release WI without a procedure is an error, never a defer.
---

# release

This step runs the project's own release procedure against the freshly fetched origin
default-branch head. It is a **dispatcher plus preconditions**, not a procedure: the
procedure belongs to the project's repository, and nothing here may be improvised or
worked around. You run every command yourself — the checks, the git and gh calls, the
procedure — in the owner repo (`cd repos/<name> && ...`; name a capsule file from there
as `../../artifacts/...`), and you report what they printed. The daemon records no
command: the next step does not trust this report and re-derives everything from the
remote.

## 开工前读什么、核对什么 — before you start: what you read and check

- The opening of `artifacts/handoff.md` and `runtime-state/work-item.json`: the goal and
  description, for the bump (section 4). You are the lane's first step: no upstream
  hand-off precedes you.
- An earlier `release` section of this work item (a dispatch that died and was resumed):
  what it claimed and published. Section 3 re-derives it from the remote either way.
- Your capsule's `artifacts/pre-release-tags.txt`, when an earlier attempt recorded it
  (section 3, item 6).

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

## 2. The procedure must support `--verify`

The next step audits the release by running `.polyforge/release.sh --verify <version>`.
Check up front, in the owner repo, that the procedure supports it (for example
`grep -q -- '--verify' .polyforge/release.sh`, or read its `--help` output). If it does
not, hand off `fail` saying the procedure lacks `--verify` support. Do not release
something the lane cannot verify.

## 3. Preconditions (in the owner repo)

The release must build exactly the freshly fetched origin default-branch head.

1. `git fetch origin` (fetch first; never trust the existing clone state).
2. Determine the default branch from the remote (for example
   `git remote show origin | sed -n 's/.*HEAD branch: //p'`, falling back to
   `git symbolic-ref --short refs/remotes/origin/HEAD | sed 's|^origin/||'`). Call it `<default>`.
3. Put the clone exactly at origin: `git checkout -B <default> origin/<default>`,
   `git reset --hard origin/<default>`, and `git clean -fd` (a release WI's clone holds
   no work worth keeping; the release builds origin's head, nothing else). Then require
   `git status --porcelain` to be empty and `git rev-parse HEAD` to equal
   `git rev-parse origin/<default>`. Anything else: hand off `fail`.
4. Record `origin_head` = `git rev-parse origin/<default>`.
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
   burned. Run `git ls-remote --tags origin` once now (the current list). A release tag -
   a tag whose name starts with `v`, for example `v1.2.3` - is *on `origin_head`* when
   its sha, or its peeled `^{}` sha, equals `origin_head`. A head that already carries
   its release tag IS that version, whoever claimed it: an earlier attempt of this work
   item that died, an earlier work item (a release that failed after its claim, or one
   that passed and whose deploy failed), or a hand release. Classify:
   - **No release tag on `origin_head`** - a normal first run: continue.
   - **Exactly one, `V`** - resume `V`; never cut a second version of this head and
     never refuse it as already released. First record the pre-release list (item 7)
     when it does not exist yet - it then lists `V`; a list an earlier attempt of this
     work item kept may lack it. `verify` accepts both for a resumed release. After `git fetch --tags origin`, run the read-only audit
     `.polyforge/release.sh --verify V` in the owner repo.
     **Exit zero - `V` is complete.** Do not run the procedure: write
     `artifacts/release-record.json` for `V` (section 5) with `resumed: true` and
     `notes` = its published release page URL,
     `https://<host>/<owner>/<repo>/releases/tag/<V>` (a location `verify` checks
     mechanically), and hand off `done` with `V`, its commit (= `origin_head`), `notes`
     and the artifacts `gh release view V` lists in your body; `verify` audits it again
     under its existing checks.
     **Nonzero - part of `V` is missing.** Continue with section 4 and record
     `resumed: true`: the procedure resumes `V` (the procedure contract: on a head that
     carries its release tag it checks what is published, keeps it, publishes only what
     is missing and reports `version=V`). A procedure that refuses a tagged head instead
     is a failed procedure like any other: hand off `fail` with a `--reason` that
     begins with `PF-RELEASE-INCOMPLETE`, a body that names `V` and quotes what the audit
     found missing.
   - **Two or more** - hand off `fail` with a `--reason` that begins with
     `PF-RELEASE-ALREADY_RELEASED` and a body that names every one: there is no single
     version to resume, and a human decides. Do NOT run the procedure.
   - **An earlier attempt recorded a list**: `<capsule>/artifacts/pre-release-tags.txt`
     already exists (never overwrite it) and the current list has a line that file lacks
     for a release tag that is NOT on `origin_head` - a release tag added or moved on another
     commit since that attempt (for example `comm -13` over the two sorted lists, ignoring
     `V`'s lines and every tag that is not a release tag, such as a marker another workflow
     adds): refuse with `PF-RELEASE-ALREADY_RELEASED` naming it, without running the
     procedure. That attempt may have claimed a version on a commit that is no longer
     origin's head, and a human decides.
   A normal first run has no release tag on `origin_head` and no earlier list.
7. Only when the file does not exist yet: record the pre-release remote tag list, which
   the next step needs for its immutability check:
   `git ls-remote --tags origin > <capsule>/artifacts/pre-release-tags.txt`
   (run it BEFORE the procedure; keep the file exactly as git printed it).
8. **The docs against the changes since the last release** (a normal first run only: a
   resumed `V` was cut already, so skip this item). Take the newest release tag that is an
   ancestor of `origin_head` as `<prev>`
   (`git describe --tags --abbrev=0 --match 'v*' <origin_head>`, after
   `git fetch --tags origin`) and read what was merged since
   (`git log --first-parent --oneline <prev>..<origin_head>`, and the diff of that range);
   with no earlier release tag, read the documents against the tree as it stands. Check
   that the repository's README, its install instructions and its upgrade notes agree
   with those changes and name no command or configuration key the range removed. Edit no
   document. On any mismatch, `pf step ask` the owner once — per mismatch the document,
   the line and the change it contradicts — offering to release as is or calling the
   release off (`cancel`, citing the reply; the documents are then fixed by a work item of
   their own and the release filed again). On the owner's go-ahead, start again at item 1
   (origin may have moved while you waited) and ask only about a mismatch the owner has
   not answered. No mismatch: go on.

## 4. Run the procedure

The repo's procedure owns its test gate (it runs the project's tests itself, so you
neither skip nor replace them). Decide the bump argument:

- If the WI goal/description names a bump or version-increment keyword the project's
  scheme uses, pass it **verbatim** as the single argument: `.polyforge/release.sh <bump>`.
- Otherwise pass no argument (the repo's default applies). Never invent a bump, never
  pass flags that skip tests or checks, never use `--force`-style options.

The run is long (a full test gate plus build and publish). Run it yourself, in ONE tool
call, and wait for it:

```
set -o pipefail; cd repos/<owner> && .polyforge/release.sh [<bump>] 2>&1 | tee ../../artifacts/release.log
```

This step's tool window is 120 minutes (`budget.tool_minutes`; it rides your process as
`PF_TOOL_WINDOW_MS`, and a claude step's Bash default timeout equals it), sized for a
procedure that runs the full test gate itself: never background the run and poll it,
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
lines). If either is missing, or the reported commit is not equal to `origin_head`, that
is a failure.

## 5. Record and report

Write `artifacts/release-record.json` with exactly:

```json
{"version": "...", "commit": "...", "default_branch": "...", "origin_head_at_release": "...",
 "notes": "...", "repo": "repos/<owner>", "pre_release_tags_file": "artifacts/pre-release-tags.txt",
 "resumed": false}
```

`resumed` is `true` exactly when section 3 found the version's tag already on
`origin_head`. The pre-release list then either lists the tag (this attempt recorded the
list after the claim) or lacks it (an earlier attempt of this work item recorded the
list, then claimed the tag); `verify` accepts either shape only for a resumed release.

This file is the release step's record: `verify` audits it, and `deploy` checks the
version it deploys against its `version` — the version is read from this file, never
from your hand-off body. Then hand off `done`.

## 交接要写什么 — what your hand-off carries

- **Status.** `done` when the procedure ran to a zero exit on origin's head and printed
  the version and commit you report (or a complete resumed release was adopted); `fail`
  on every failure above (this gate has no `on_fail`: a FAIL ends the work item). `cancel`
  only on the owner's word (`--per-owner`).
- **结论**: the version and the release commit — or what failed, and exactly what (tag,
  image, release page) was already published.
- **做了什么**: the procedure line as run and its exit; whether the release was resumed;
  everything the procedure reported publishing (for a complete resumed release, what
  `gh release view V` lists) — never invented entries; the tail of `artifacts/release.log`.
- **验证**: the precondition checks and what they printed — the docs check's `<prev>`,
  its range and the documents you read included.
- **Evidence**: `artifacts/release-record.json`, `artifacts/pre-release-tags.txt` and
  `artifacts/release.log` when they exist.

## Honesty

`done` means the procedure ran to a zero exit on origin's head and printed the version and
commit you report. The next step does not trust this report and re-derives everything
from the remote; a `done` that the remote contradicts ends the lane.
