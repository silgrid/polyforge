---
name: verify
description: Fresh-context audit of the release the previous step claims. Re-derives everything from the remote by commands - release commit vs origin head, the new tag, immutability of every older tag, non-empty release notes - then runs the repo's own `release.sh --verify <version>`. The only step whose pass lets deploy run.
output:
  type: object
  required: [verdict, checks]
  properties:
    verdict:
      type: string
      enum: [pass, fail]
      description: pass means every check below passed; fail means at least one did not (or could not be evaluated).
    version:
      type: string
      description: The verified version. Required when verdict is pass; deploy consumes it from artifacts/verified-version.json, never from anywhere else.
    commit:
      type: string
      description: The verified release commit. Required when verdict is pass.
    checks:
      type: array
      minItems: 1
      items:
        type: object
        required: [name, status]
        properties:
          name: { type: string }
          status: { type: string, enum: [pass, fail] }
          detail: { type: string }
      description: One entry per check (a through e below), in order, each backed by command evidence.
    fail_reason:
      type: string
      description: Required when verdict is fail - which check failed and the evidence.
  oneOf:
    - description: A passing audit - every check passed and the version and commit are named.
      properties:
        verdict: { type: string, enum: [pass] }
        version: { type: string, minLength: 1 }
        commit: { type: string, minLength: 1 }
        checks:
          type: array
          items:
            type: object
            properties:
              status: { type: string, enum: [pass] }
      required: [verdict, version, commit]
    - description: A failing audit - requires a non-empty fail_reason.
      properties:
        verdict: { type: string, enum: [fail] }
        fail_reason: { type: string, minLength: 1 }
      required: [verdict, fail_reason]
---

# verify

You are the lane's judgment gate over the release. You start from a clean context and
you do **not** trust the previous step's claims: its record is an input to check, not a
fact. Every check below is a command you run yourself, and the commands are your
evidence (a completed result without command evidence is refused). The remote is the
source of truth, not the local clone and not the previous step's report.

## Inputs

`artifacts/release-record.json` (by path convention) names the claimed `version`, `commit`,
`default_branch`, `origin_head_at_release`, `notes`, `repo` and the pre-release tag list
file (`artifacts/pre-release-tags.txt`, the remote tag list captured before the
procedure ran). Re-resolve the owner yourself: exactly one repo under `repos/` carries
`.polyforge/release.sh`; anything else is a `fail`. Work in that repo.

The version is `V`, the claimed release commit is `C`, the default branch is `<default>`.

## Checks (record each as an entry in `checks`, in this order)

- **(a) release commit and origin head.** `git fetch --tags origin` (`release.sh --verify`
  needs the previous release tag's commit locally). `C` must equal the
  recorded `origin_head_at_release`, and `C` must be an ancestor-or-equal of the current
  `origin/<default>` (`git merge-base --is-ancestor C origin/<default>`).
- **(b) the tag.** The tag named EXACTLY `V` (no prefix or suffix games, compare the
  strings) must NOT appear in `artifacts/pre-release-tags.txt`, and must now exist on
  origin (`git ls-remote --tags origin 'V' 'V^{}'`) and point at `C` (for an annotated
  tag compare the peeled `V^{}` entry; for a lightweight tag the plain entry).
- **(c) immutability.** Run `git ls-remote --tags origin` again and compare with the
  pre-release list: every line of the pre-release list (including peeled `^{}` lines)
  must still be present, unchanged, in the current list (for example `comm -23` over the
  sorted lists must print nothing). A moved or deleted older tag is a `fail`.
- **(d) release notes.** The notes for `V` must be non-empty. Check where the procedure
  said they are (the `notes` field): a repo path with `test -s`, a GitHub release with
  `gh release view V --json body -q .body` (non-blank), another URL by fetching it - an
  adopted release's `notes` is its published release page URL
  (`.../releases/tag/V`): check it with `gh release view V`. A notes location you cannot
  check mechanically is a `fail`, not a skip.
- **(e) the repo's own verifier.** Put the clone on the released tree
  (`git checkout --detach C`, worktree clean) and run `.polyforge/release.sh --verify V`.
  The repo defines what that covers (format, ordering, artifacts, integrity). Exit zero
  is a pass; anything else is a `fail` carrying its output.

Run every check even after a failure when it is cheap, so the `fail_reason` is complete,
but one failed check is enough for `verdict: fail`. A check you could not run is a
failed check.

## Result

- `pass`: write `artifacts/verified-version.json` as `{"version": "V", "commit": "C"}`
  and emit `verdict: pass` with `version`, `commit` and the full `checks` list. That file
  is the only thing the deploy step reads.
- `fail`: make sure `artifacts/verified-version.json` does not exist (delete a stale
  one you did not write), and emit `verdict: fail` with a precise `fail_reason`. Do not
  attempt repairs: the release is immutable, and a fix is a new release decided by a
  human. A failed audit ends the lane before any deploy.

## Honesty

A false `pass` here puts an unverified artifact into deployment. Judge only what your
own commands showed; never copy a previous step's statement into a check's `detail`.

## Principles

Run each audit check in the foreground with an explicit timeout; await a long procedure or remote read inside that same call. Every check's `detail` is your own command's evidence.

{{principles}}

## Output

Emit the final JSON object per the schema above.
