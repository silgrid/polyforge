---
name: release
description: Cut a release from the freshly fetched origin default-branch head by running the repo's own release procedure (.polyforge/release.sh), after precondition checks. A release WI without a procedure is an error, never a defer.
output:
  type: object
  required: [verdict]
  properties:
    verdict:
      type: string
      enum: [pass, fail]
      description: pass means the repo's release procedure ran to a zero exit and reported a version and a release commit; fail means a precondition, the procedure, or ownership resolution failed. There is no defer - a release WI without a release procedure is a failure.
    version:
      type: string
      minLength: 1
      description: The version identifier the procedure reported (copied verbatim from its output, never invented). Required non-empty when verdict is pass.
    commit:
      type: string
      description: The release commit the procedure reported (full sha). Required when verdict is pass.
    notes:
      type: string
      description: The release-notes location (path or URL) the procedure reported.
    artifacts_published:
      type: array
      items: { type: string }
      description: Identifiers or locations of everything the procedure actually published (tag, images, packages, release page). Required with at least one entry when verdict is pass.
    fail_reason:
      type: string
      description: Required when verdict is fail - what went wrong and exactly what (if anything) was already published, so a human can decide the next move.
  oneOf:
    - description: A completed release - requires a non-empty version, the release commit and at least one published artifact.
      properties:
        verdict: { type: string, enum: [pass] }
        version: { type: string, pattern: '\S' }
        commit: { type: string, pattern: '\S' }
        artifacts_published: { type: array, minItems: 1 }
      required: [verdict, version, commit, artifacts_published]
    - description: A failed release attempt - requires a non-empty fail_reason.
      properties:
        verdict: { type: string, enum: [fail] }
        fail_reason: { type: string, pattern: '\S' }
      required: [verdict, fail_reason]
---

# release

This step runs the project's own release procedure against the freshly fetched origin
default-branch head. It is a **dispatcher plus preconditions**, not a procedure: the
procedure belongs to the project's repository, and nothing here may be improvised or
worked around. Everything below is done with your `bash` tool, and every command you
run is recorded as command evidence (a completed result without evidence is refused).

## 1. Ownership resolution

Scan the capsule's `repos/` directory (one level: `repos/<name>/`). A repo is the
release-procedure owner when it carries `.polyforge/release.sh`. Classify the scan:

- **Zero owners.** No repo carries the file. A release WI without a procedure is an
  error, not a deferral: finish with `verdict: fail` and a `fail_reason` saying no repo
  in the capsule carries `.polyforge/release.sh`.
- **One owner.** That repo is the release owner; continue.
- **Two or more owners.** Stop - this is a loud failure, not a choice. Finish with
  `verdict: fail` and a `fail_reason` that begins with `PF-RELEASE-AMBIGUOUS_OWNER` and
  names EVERY candidate repo. Never pick one silently or merge procedures.

## 2. The procedure must support `--verify`

The next step audits the release by running `.polyforge/release.sh --verify <version>`.
Check up front, in the owner repo, that the procedure supports it (for example
`grep -q -- '--verify' .polyforge/release.sh`, or read its `--help` output). If it does
not, finish with `verdict: fail` saying the procedure lacks `--verify` support. Do not
release something the lane cannot verify.

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
   `git rev-parse origin/<default>`. Anything else: `verdict: fail`.
4. Record `origin_head` = `git rev-parse origin/<default>`.
5. **Already released?** This step can be dispatched again after an earlier attempt
   already published (the harness died before its result landed), and someone may have
   released this head by hand. Run `git ls-remote --tags origin` once now (the current
   list). **Adopt** a published tag only when ALL THREE hold: a release tag is on
   `origin_head` (below); this capsule's `artifacts/pre-release-tags.txt` exists (an
   earlier attempt of THIS work item captured it); and that tag is absent from the file
   (the earlier attempt published it, then died before its result landed). Adopting runs
   no procedure: write `artifacts/release-record.json` for that tag (section 5) with
   `notes` = its published release page URL, `https://<host>/<owner>/<repo>/releases/tag/<V>`
   (a location `verify` checks mechanically), commit it with the capsule, and pass with
   that tag's `version`, `commit` (= `origin_head`), `notes` and the artifacts
   `gh release view <V>` lists, so `verify` audits it under its existing checks.
   Otherwise refuse in either case below - do NOT run the procedure; finish with
   `verdict: fail` and a `fail_reason` that begins with `PF-RELEASE-ALREADY_RELEASED`
   and names the tag(s), so a human decides the next move (no capsule list - a hand
   release -, the tag already in the list, or the tag not on `origin_head` never adopt):
   - **The head is already tagged and the tag is not adoptable** (checked
     whether or not any file from an earlier attempt exists): a release tag - a tag whose name starts with `v`, for example
     `v1.2.3` - whose sha, or peeled `^{}` sha, equals `origin_head`.
   - **An earlier attempt recorded a list**: `<capsule>/artifacts/pre-release-tags.txt`
     already exists (never overwrite it) and the current list has any line that file
     lacks - any tag, on any commit, added or moved since that attempt (for example
     `comm -13` over the two sorted lists prints something) - other than the one adopted
     tag above.
   A normal first run passes both: origin's head carries no release tag yet and there is
   no earlier list.
6. Only when the file does not exist yet: record the pre-release remote tag list, which
   the next step needs for its immutability check:
   `git ls-remote --tags origin > <capsule>/artifacts/pre-release-tags.txt`
   (run it BEFORE the procedure; keep the file exactly as git printed it).

## 4. Run the procedure

The repo's procedure owns its test gate (it runs the project's tests itself, so you
neither skip nor replace them). Decide the bump argument:

- If the WI goal/description names a bump or version-increment keyword the project's
  scheme uses, pass it **verbatim** as the single argument: `.polyforge/release.sh <bump>`.
- Otherwise pass no argument (the repo's default applies). Never invent a bump, never
  pass flags that skip tests or checks, never use `--force`-style options.

The run can be long (a full test gate plus build and publish). Waiting is never a tool
call of its own: run the procedure in the foreground with an explicit timeout at least as
long as you expect it to take (the harness allows up to the daemon's tool bound), its
output also written to `artifacts/release.log`. Only a procedure known to exceed that
bound may be backgrounded (its output in `artifacts/release.log`, its exit code in a
file), and it is then awaited inside one call: a loop that checks a real condition, sleeps
at most 3 s per iteration and has a deadline. The condition is the exit-code file
existing; a deadline that passes first is answered by the same loop in the next call. A
bare `sleep N` call, or a loop whose only exit is the deadline, is a violation and is
recorded. Record the invocation as command evidence with its real duration.

A nonzero exit is a release failure: `verdict: fail`, never something to work around.
Your `fail_reason` must say what the procedure printed and what (tag, image, release
page) may already exist. Never delete, move or overwrite anything to "clean up".

On exit zero, parse the procedure's final output. It must carry the version string and
the release commit (the contract is `version=<v>`, `commit=<sha>` and `notes=<location>`
lines). If either is missing, or the reported commit is not equal to `origin_head`, that
is a failure.

## 5. Record and report

Write `artifacts/release-record.json` with exactly:

```json
{"version": "...", "commit": "...", "default_branch": "...", "origin_head_at_release": "...",
 "notes": "...", "repo": "repos/<owner>", "pre_release_tags_file": "artifacts/pre-release-tags.txt"}
```

then emit `verdict: pass` with `version`, `commit`, `notes` and `artifacts_published`
(only what the procedure actually reported publishing; never invented entries).

## Honesty

`pass` means the procedure ran to a zero exit on origin's head and printed the version and
commit you report. The next step does not trust this report and re-derives everything
from the remote; a `pass` that the remote contradicts ends the lane.

## Output

Emit the final JSON object per the schema above.
