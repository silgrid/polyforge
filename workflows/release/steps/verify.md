---
name: verify
description: Fresh-context audit of the release the previous step claims. Re-derives everything from the remote by commands - release commit vs origin head, the new tag, immutability of every older tag, non-empty release notes - then runs the repo's own `release.sh --verify <version>`. The only step whose pass lets deploy run.
---

# verify

You are the lane's judgment gate over the release. You start from a clean context and
you do **not** trust the previous step's claims: its record is an input to check, not a
fact. Every check below is a command you run yourself, in the owner repo (`cd
repos/<name> && ...`), and what it printed is your evidence: quote it in
`artifacts/release-audit.md`. The remote is the source of truth, not the local clone and
not the previous step's report.

## 开工前读什么、核对什么 — before you start: what you read and check

`artifacts/release-record.json` (the release step's record) names the claimed `version`,
`commit`, `default_branch`, `origin_head_at_release`, `notes`, `repo` and the pre-release
tag list file (`artifacts/pre-release-tags.txt`, the remote tag list captured before the
procedure ran). Read release's latest section of `artifacts/handoff.md` for context only —
the values you check come from the record. Before any check: the record must parse as
JSON and name `version` and `commit`; one that does not is a `fail` naming what is
missing. Re-resolve the owner yourself: exactly one repo under `repos/` carries
`.polyforge/release.sh`; anything else is a `fail`. Work in that repo.

The version is `V`, the claimed release commit is `C`, the default branch is `<default>`.

## Checks (record each in `artifacts/release-audit.md`, in this order)

- **(a) release commit and origin head.** `git fetch --tags origin` (`release.sh --verify`
  needs the previous release tag's commit locally). `C` must equal the
  recorded `origin_head_at_release`, and `C` must be an ancestor-or-equal of the current
  `origin/<default>` (`git merge-base --is-ancestor C origin/<default>`).
- **(b) the tag.** The tag named EXACTLY `V` (no prefix or suffix games, compare the
  strings) must now exist on origin (`git ls-remote --tags origin 'V' 'V^{}'`) and
  point at `C` (for an annotated tag compare the peeled `V^{}` entry; for a lightweight
  tag the plain entry). It must NOT appear in `artifacts/pre-release-tags.txt` - unless
  the record says `"resumed": true` (the release step found `V` already on its head and
  resumed it). A resumed `V` passes in either shape: absent from the list (an earlier
  attempt of THIS work item recorded the list, then claimed `V` and died), or present
  with exactly the current lines (`V` was claimed before the list was recorded and has
  not moved since). Present with other lines is a `fail`.
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
  (`git checkout --detach C`, worktree clean) and run
  `.polyforge/release.sh --verify V` (under your own `PATH`, the daemon's command PATH,
  the one the release step ran the procedure under). The repo defines what that covers
  (format, ordering, artifacts, integrity). Exit zero is a pass; anything else is a
  `fail` carrying its output.

Run every check even after a failure when it is cheap, so the record is complete, but
one failed check is enough for a FAIL. A check you could not run is a failed check.
Write `artifacts/release-audit.md`: one entry per check (a)–(e) — its name, `pass` or
`fail`, and what your own command printed.

## Result

- Every check passed: write `artifacts/verified-version.json` as
  `{"version": "V", "commit": "C"}` and hand off `done`. That file is the only version
  source the deploy step reads.
- Any check failed: make sure `artifacts/verified-version.json` does not exist (delete a
  stale one you did not write), and hand off `fail` with the failed check named in
  `--reason`. Do not attempt repairs: the release is immutable, and a fix is a new release
  decided by a human. A failed audit ends the lane before any deploy.

## 交接要写什么 — what your hand-off carries

- **Status.** `done` when every check passed; `fail` when any failed (this gate has no
  `on_fail`: a FAIL ends the work item).
- **结论**: `V` and `C` verified, or the failed check and its evidence.
- **验证**: each check (a)–(e) with its status in one line — the detail is in
  `artifacts/release-audit.md`.
- **Evidence**: `artifacts/release-audit.md`, and on a pass `artifacts/verified-version.json`.

## Honesty

A false `done` here puts an unverified artifact into deployment. Judge only what your
own commands showed; never copy a previous step's statement into a check's record.

## Principles

Run each audit check yourself, in one foreground tool call (the repo's verifier is
short and read-only). A check's `detail` is what your own command printed.

{{principles}}
