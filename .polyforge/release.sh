#!/bin/sh
# polyforge's own release procedure.
#
# Usage:
#   sh .polyforge/release.sh [patch]                 release (default: patch)
#   sh .polyforge/release.sh --verify <tag>          read-only audit of a release
#   sh .polyforge/release.sh --help
#
# Version scheme: vX.Y.Z semver. The floor is the highest semver among the tags
# on ORIGIN (git ls-remote; local tags are ignored, and a failing ls-remote is
# fatal - there is no silent fallback); the bump is applied to it.
# The one named version-line cutover (CUTOVER_* below): the
# internal v2.20.x line is retired for the unified product line, whose first
# release is v0.2.0. While origin carries no v0.2.0 and the floor is a v2.20.x
# tag (or the public v0.1.x floor), the next version is exactly v0.2.0
# (not a patch of that floor); once
# v0.2.0 is on origin, the floor is the highest tag of the v0.2 line, so the
# retired, numerically higher tags never decide a version again. Every other
# floor patch-bumps as before. --verify judges a v0.2.x tag inside its own
# line and, for v0.2.0 alone, takes the highest retired v2.20.x tag as the
# previous release (public v0.1.x when there is no retired internal tag). This is rollback-safe transition code: delete the
# CUTOVER_* constants and their branches once no v2.20.x release is the
# current line anywhere (every deployed aihub and installed pf runs v0.2.x),
# together with the matching exceptions in tools/install.sh, deploy.sh's
# VLINE_AWK, internal/aihub/core/admin.go's belowSigningRelease and
# pi-web/lib/polyforge-detect.ts's versionLineRank.
# Suffixed versions: a repository that carries the optional
# .polyforge/release.conf - lines `suffix=<name>` (one alphanumeric identifier
# starting with a letter) and `upstream=<git url or path of the public
# repository>`; a missing file is the scheme above, unchanged - releases
# vX.Y.Z-<name>.N instead: X.Y.Z is the highest vX.Y.Z tag of UPSTREAM
# (git ls-remote) whose commit is in HEAD's history, N is one more than the
# highest N of vX.Y.Z-<name>.N on ORIGIN (1 when there is none). Only tags of
# that shape are this line's releases: plain vX.Y.Z tags on origin (an older
# line, possibly same-named as public releases on other commits) are never
# read, and no tag of UPSTREAM is ever fetched into the local refs/tags. The
# previous release (the PR-only and contract checks, the notes) is the highest
# -<name>.N tag below the version on origin, else the public base tag itself,
# each with the commit its own ls-remote reports. Resume and --verify judge
# the same shape the same way (the cutover exceptions do not apply).
# Bump guard (version line, CONTRIBUTING.md): only patch is released - with no
# argument the bump is patch. minor and major are refused before anything runs
# (zero side effects): a middle-number (minor) jump needs an explicit owner
# ruling and a major one an architecture rebuild, neither of which this script
# can judge.
#
# Release, in order (nothing is published before the gate passes):
#   1. refuse a dirty worktree; fetch origin; refuse unless HEAD == origin/main;
#      when HEAD already carries exactly one release tag on origin, RESUME that
#      version instead of computing one (see "Resume" below; two or more tags
#      on HEAD refuse); else compute the version from the origin tags; the
#      preflight (step 1b''' below: tools, node major, the ghcr.io push
#      credential - every problem in one message); refuse unless every commit that entered
#      main since the previous release tag came through a merged PR or is an
#      allowlisted owner-direct commit (the PR-only check below); the
#      workflow-contract check (step 1d below,): refuse while
#      internal/common/workflow/contract-pending.md registers an incompatible
#      change and ContractVersion is still the previous release's, refuse a
#      bump that leaves the list non-empty or moves the version by more than
#      one; an empty list needs no bump;
#   2. design-doc cross-reference gate (docs/design/polyforge-vnext/tools/
#      check-xrefs.py, the Makefile's `xrefs` check; python3 is REQUIRED here -
#      unlike `make xrefs`, a release never skips it), then the test gate
#      (see "Test gate" below: the verification's full plan inside its
#      container, or the reused result of a green tag on HEAD), then the
#      production-dependency audit gate (the console SPA put npm inside
#      the aihub trust surface, so a release refuses on a known vulnerable
#      production dependency). Two audits, two scopes: the verification
#      bar's dependency-audit gate audits the whole tree, dev dependencies
#      included, on every full-tier run; the release audits production
#      dependencies only;
#   3. build the binaries (GOWORK=off make release VERSION=<tag>) -> the
#      one pf binary for linux-amd64, darwin-arm64, darwin-amd64
#      (the server runs as `pf aihub`); build the image from
#      the LINUX pf (the image stays linux/amd64-only, entrypoint
#      `pf aihub`) tagged ghcr.io/silgrid/aihub:<tag>; then pack the pi-web
#      package (one release entry for every artifact): the
#      released commit's pi-web/ tree is exported to the staging directory
#      (the worktree is never touched), npm ci, PI_WEB_RELEASE_VERSION=<tag>
#      npm run build (the bundle reports the release; a release is the
#      documented exception to pi-web's "never next build" development rule),
#      npm version <tag without v> --no-git-tag-version (the tag's version is
#      stamped into the packed metadata after the build, before pack; main's
#      pi-web/package.json keeps its development value and no bump commit
#      exists), npm pack -> silgrid-pi-web-<tag>.tgz, whose packed
#      package.json must carry the tag's version;
#   4. git tag <tag> + git push origin <tag> (NO force): the atomic claim of the
#      version. If the tag exists on origin the run stops, nothing published;
#   5. docker push; write aihub-image.digest (the bare image digest
#      sha256:<64 hex>, known only after the push; the canonical format that
#      deploy.sh consumes); SHA256SUMS over the three pf binaries, the
#      digest file and the pi-web tarball (5 lines); release-manifest.json
#      (the tag, the commit, every payload with its sha256 and the
#      aihub-first ordering statement - see manifest_of);
#   6. gh release create <tag> with the 5 payload assets (3 pf binaries +
#      digest + pi-web tarball) + SHA256SUMS + release-manifest.json,
#      notes from docs/release-notes/<tag>.md (generated from git log when
#      absent), followed by a "Test gate" section naming how the gate passed
#      (the green tag reused, or the gate run in its container, with the gate
#      id; the committed notes file itself is never changed). The aihub image
#      and the pf binaries are published with the
#      same tag before tools/install.sh can consume the release (aihub
#      first); the pi-web tarball rides the same release, so there is no
#      second version line.
# Last lines on stdout: version=<tag>, commit=<sha>, notes=<release url>.
#
# Test gate: ONE definition, the same one the integrate
# workflow's full verification runs before it marks a commit releasable -
# the tree's own scope-engine plan (workflows/_shared/scripts/verify-scope --json --full --base
# HEAD: the fast gates, the full commands and the engine-appended
# dependency audit, in that order) with every command, the engine included,
# run inside the container built from .polyforge/Dockerfile.verify.
# The tree's own workflow script runs it - python3
# workflows/_shared/scripts/gate.py --isolation container (the shared
# library pfstep's container executor: the image tag, the mounts, the
# environment allowlist, the limits and the per-command timeouts are the
# workflow scripts' own) - from the repository root, its logs kept in the ignored
# artifacts/release-<tag>/gate/ (the image build and one log per command;
# a failing command's tail is printed). Nothing of the gate runs on this
# host, so a host-only difference (a docker client on PATH, a tmux, another
# toolchain) can no longer turn red a gate the container passes: on
# 2026-11-08 the host-run gate (then `make test`) failed a test because the
# host had a docker client, while the containerized full bar of the same
# tree passed. `make test` stays a developer convenience; no release step
# runs it. The gate passes only when every planned command ran and exited
# 0.
# Before running it the release asks tools/green-tag check <commit> (against
# REMOTE) whether the released commit carries a green tag - an annotated
# green/<YYYYMMDD>-<n> tag whose JSON message records that commit, its
# gate_id, the runner (gate.py and its version) and the digest of the plan
# as executed - matching this tree's tools/gate-id (the hash of the conf's
# gate lines and of Dockerfile.verify) and the runner and plan digest
# `gate.py --plan-only` computes for HEAD now (the same path a gate run
# takes, in the same container, under the gate's default limits). A match means this gate
# already passed on exactly this commit: it is not run again, the run says
# so and the release notes name the tag. No tag, another gate id, runner or
# plan, or a check that cannot tell: the gate runs. A green tag is never
# written here: only `gate.py --mark-green`, after a pass, writes one (the
# integrate workflow's full gate, or a human running it).
# A gate gate.py cannot judge - exit 2: no docker engine, no Dockerfile, no
# image, no plan, its deadline, an interrupt - fails the release as "not
# judged", never as a red gate.
#
# --verify <tag> (no mutation, no tests, no build): tag format; strict
# monotonicity (the tag must be strictly greater than EVERY other release tag on
# origin); the PR-only check over previous-tag..tag, owner-direct allowlist
# included (needs the commits locally: git fetch origin first); release exists
# with EXACTLY the 5 payload assets + SHA256SUMS + release-manifest.json;
# sha256sum -c SHA256SUMS on the downloaded assets; release-manifest.json is
# exactly what manifest_of derives from the tag, its commit and SHA256SUMS;
# the pi-web tarball's packed package.json carries the tag's version;
# aihub-image.digest is exactly sha256:<64 hex> and the registry digest of
# ghcr.io/silgrid/aihub:<tag> equals it. Exit 0 iff all pass.
#
# PR-only check (polyforge#163): main's FIRST-PARENT history from the previous
# release tag to the released commit must consist only of commits GitHub records
# as the merge commit of a merged PR - `gh api
# repos/<repo>/pulls?state=closed&sort=updated&direction=desc`, the merged PR
# whose merge_commit_sha is that commit. Both shapes a merged PR leaves behind
# count: the two-parent merge commit `gh pr merge --merge` (the lanes' merge
# step, run by the daemon) produces, and a squash/rebase PR's one-parent commit. The
# commit->PR index (`commits/<sha>/pulls`, GraphQL associatedPullRequests, PR
# search) is deliberately NOT consulted: GitHub's index can be empty even for a
# merged PR's own merge commit, so an empty index answer must never be read as
# "no PR" (the v2.20.22 release was refused exactly that way).
# Only rows GitHub reports as merged (merged_at set) are associations: a closed,
# unmerged PR still carries its test-merge merge_commit_sha, which is not a
# merged PR and must not pass. The merged-PR list is paged newest-updated first
# and stops once a page predates the previous release - nothing merged after it
# can sit on a later page. A page's OLDEST row is computed from every row,
# merged or not (an unmerged PR still has updated_at), and every row keeps a
# sha field ('-' when there is none), so a null merge_commit_sha can never read
# as an empty field and truncate paging. The PR content itself is the
# second-parent side of each merge and is not walked. A first-parent commit
# the list does not carry, whose subject is GitHub's own merge message
# ("Merge pull request #<n> from ..."), is confirmed by reading that one PR
# (`pulls/<n>`): it passes only when that PR is merged and its
# merge_commit_sha is exactly the commit (a second source for a list that
# lags, never a weaker rule). A gh API failure is a refusal, never a pass -
# and it is reported as what it is: "PR lookup failed ... retryable", never
# as "without a merged PR", so a transient GitHub error is not mistaken for
# a commit that bypassed review (; the run is refused before the
# version is claimed, so a retry costs no version). The initial version of
# this check also demanded a two-parent merge commit; that shape half is gone so
# that a PR merged as squash/rebase is judged by the same rule, while a commit
# with no merged-PR record still refuses. A PR merged by hand (a local git merge
# pushed with the pre-push hook's PF_ALLOW_MAIN_PUSH override) passes only if
# GitHub records that very commit as the PR's merge commit; otherwise it is
# refused - merge with gh pr merge --merge.
# The one exception is the owner-direct allowlist (OWNER_DIRECT_EMAILS below,
# polyforge#240): a first-parent commit whose git AUTHOR EMAIL exactly equals an
# allowlisted email passes whatever its shape, and each one prints an audit line
# `PASS pr-only: owner-direct: <sha> <author email>` (release mode and --verify
# alike). Author email only - name and committer are not consulted, and the
# match is not proof against a forged author (accepted by the owner; the audit
# line is the record). It does not launder anything: every other commit in the
# range is still checked and any refusal blocks the release. The workspace /
# step-agent identity (the workspace git user.email - the same identity every
# step agent commits with) is deliberately NOT allowlisted:
# polyforge#149 was an agent pushing straight to main, and this gate must keep
# refusing that. The allowlist is a committed constant - changing it takes a
# PR; there is no env or CLI override. It is a one-time legacy bridge for the
# owner's pre-existing direct commits on main: delete it (and this paragraph)
# once every change goes through the standard PR-review-merge route (the
# open-source route).
# Otherwise there is no override: a non-PR commit on the branch blocks every
# release (each compares against the previous tag) until a human audits it and
# decides how to cut the next release by hand.
#
# Nothing is ever deleted, overwritten or forced. If a step fails after the tag
# was pushed, the script prints recovery instructions and does NOT undo anything.
#
# Resume: the claimed version is never burned. A run whose
# HEAD (== origin/main) already carries exactly one release tag V on origin
# resumes V instead of refusing or cutting a second version of the commit -
# an earlier run that died after the claim (the tag pushed, then docker push
# or gh release create failed), or a later lane that carries a finished
# release on to its deploy. It reads what of V is published - the GitHub
# release (absent, draft, or which of the 7 assets it has) and the registry
# image (present with its digest, or answered not-found) - and:
#   - V complete (published, all 7 assets): nothing runs; it prints the
#     version/commit/notes lines (notes = the release page) and exits 0;
#   - otherwise the preflight, the PR-only check (against V's previous tag)
#     and the gates run (a tag proves a claim, not that the gates passed),
#     then ONLY the missing parts are built and published: a published
#     binary, tarball or digest file is downloaded and kept byte for byte
#     (the image is built from the published linux pf when there is one), an
#     image already in the registry is kept, not rebuilt or pushed; a missing
#     release is created, an existing one gets only its missing assets
#     (gh release upload, never --clobber) and a draft is published.
#     A resume holds no exclusive claim (another run may be publishing V), so
#     it re-reads the release and the registry right before each publish
#     action (docker push, the release create/upload) and refuses, with
#     nothing more pushed, when either changed since it first read them or
#     can no longer be read.
# Contradictions refuse with nothing published, for a human: V not the
# highest tag of its line, assets this script never publishes, published
# SHA256SUMS/release-manifest.json next to a missing payload (the sums name
# bytes no rebuild reproduces), published assets that fail their SHA256SUMS,
# a published digest the registry does not hold. An unreadable GitHub release
# or registry is a retryable refusal, never read as "missing". A tag whose
# commit is no longer origin/main's head cannot be resumed here.
#
# Environment overrides (mainly for the fake-env tests):
#   RELEASE_XREFS_CMD   xrefs gate command        (default: python3 -I
#                       docs/design/polyforge-vnext/tools/check-xrefs.py)
#   RELEASE_TEST_CMD    test gate command; when set it replaces the whole
#                       gate - no green tag is consulted (default:
#                       tools/green-tag check, then python3
#                       workflows/_shared/scripts/gate.py --isolation
#                       container from the root unless a green tag is
#                       reused)
#   RELEASE_AUDIT_CMD   production-dependency audit gate command
#                       (default: sh -c 'cd webui && npm audit --omit=dev
#                       --audit-level=high'); runs AFTER the test gate and
#                       BEFORE the build, like the other gates
#   RELEASE_BUILD_CMD   build command; sees TAG; must leave
#                       artifacts/release-$TAG/pf and the two darwin
#                       binaries .../pf-darwin-arm64, .../pf-darwin-amd64
#                                                 (default: GOWORK=off make release VERSION=$TAG)
#   RELEASE_IMAGE       image repository          (default: ghcr.io/silgrid/aihub)
#   RELEASE_GH_REPO     gh -R repo                (default: silgrid/polyforge,
#                       this repository's own release repo).
#                       Whatever it names, it must be the repo the tag is
#                       pushed to: when RELEASE_REMOTE's configured URL is a
#                       GitHub URL, a GH repo that differs from it refuses
#                       before any work in both modes ( pushed
#                       the tag to one repo and created the release in
#                       another, burning the version;)
#   RELEASE_REMOTE      git remote                (default: origin)
#   RELEASE_BRANCH      branch HEAD must equal    (default: main)
#   DOCKER_CONFIG       docker config dir (ghcr login check; default ~/.docker)
# Each *_CMD override, when non-empty, runs via sh -c INSTEAD of its default;
# unset or empty keeps the default. They are independent: RELEASE_TEST_CMD
# replaces only the test gate, the xrefs gate still runs.
set -eu

REMOTE="${RELEASE_REMOTE:-origin}"
BRANCH="${RELEASE_BRANCH:-main}"
IMAGE="${RELEASE_IMAGE:-ghcr.io/silgrid/aihub}"
GH_REPO="${RELEASE_GH_REPO:-silgrid/polyforge}"
SEMVER_RE='^v[0-9]+\.[0-9]+\.[0-9]+$'
# Suffixed versions (see the header): set from .polyforge/release.conf below.
RELEASE_CONF=".polyforge/release.conf"
SUFFIX=""; UPSTREAM=""; ULS=""; TAG_RE="$SEMVER_RE"
# The named version-line cutover (see the header; delete with its branches).
CUTOVER_FROM='v2.20.'   # the retired internal line's tag prefix
CUTOVER_PUBLIC_FROM='v0.1.' # the public baseline's pre-unification line
CUTOVER_TAG='v0.2.0'    # the unified product line's first release
CUTOVER_LINE='v0.2.'    # the line CUTOVER_TAG opens
# Owner-direct allowlist (polyforge#240; see the PR-only check above): author
# emails, space-separated, whose direct commits pass the PR-only check. Empty
# by default - every change goes through PR-review-merge. Forks carrying
# owner-direct history set OWNER_DIRECT_EMAILS in the environment (or their
# own delta here).
OWNER_DIRECT_EMAILS="${OWNER_DIRECT_EMAILS:-}"

die() { echo "release: $*" >&2; exit 1; }
gh_() { gh "$@" -R "$GH_REPO"; }
lc() { printf '%s' "$1" | tr '[:upper:]' '[:lower:]'; }
# gh_repo_of <url>: the <owner>/<repo> of a github.com URL (https, ssh or scp
# form); empty for anything else (another host, an ssh host alias, a path).
gh_repo_of() {
  printf '%s\n' "$1" | sed 's#/*$##; s#\.git$##' | sed -n 's#^.*github\.com[:/]\([^/][^/]*/[^/][^/]*\)$#\1#p' || true
}
# remote_urls: every CONFIGURED url of REMOTE, fetch and push (url.*.insteadOf
# is deliberately not expanded: the configured name is the identity).
remote_urls() {
  { git config --get-all "remote.$REMOTE.url"; git config --get-all "remote.$REMOTE.pushurl"; } 2>/dev/null || true
}

usage() {
  cat <<'USAGE'
usage: release.sh [patch]                 cut a release (bump: patch, default patch)
       release.sh --verify <tag>          read-only verification of a published release
       release.sh --help
USAGE
}

# ls_tags: raw `git ls-remote --tags` output of the remote ("<sha> <ref>" lines,
# annotated tags also as <ref>^{}); returns non-zero when ls-remote fails.
ls_tags() {
  git ls-remote --tags "$REMOTE" 2>/dev/null
}
# tags_of <ls-remote output> [<regex>]: release tags (TAG_RE, else <regex>),
# one per line (peeled refs folded).
tags_of() {
  printf '%s\n' "$1" | sed -n 's|^[0-9a-f]*[[:space:]]*refs/tags/||p' | sed 's/\^{}$//' | grep -E "${2:-$TAG_RE}" | sort -Vu || true
}
# tags_at <ls-remote output> <commit>: release tags (TAG_RE) whose commit
# (peeled for annotated tags, direct for lightweight ones) is <commit>.
tags_at() {
  printf '%s\n' "$1" | awk -v c="$2" '$1 == c {print $2}' | sed -n 's|^refs/tags/||p' | sed 's/\^{}$//' | grep -E "$TAG_RE" | sort -Vu || true
}

# tag_commit <ls-remote output> <tag>: the commit <tag> points at (peeled for
# an annotated tag, direct for a lightweight one); empty when absent.
tag_commit() {
  _p=$(printf '%s\n' "$1" | awk -v r="refs/tags/$2^{}" '$2 == r {print $1; exit}')
  [ -n "$_p" ] || _p=$(printf '%s\n' "$1" | awk -v r="refs/tags/$2" '$2 == r {print $1; exit}')
  printf '%s' "$_p"
}
# merged_pr_commits <floor commit>: prints the merge_commit_sha of every merged
# PR that can have been merged after <floor commit> (see the PR-only check
# above). The closed-PR list is read newest-updated first, one page per call;
# paging stops at the first page whose OLDEST row predates the floor (a merged
# PR's updated_at is never older than its merged_at, so nothing merged after the
# floor can sit on a later page), on an empty page, or at a hard page cap that
# only a paging-ignoring server can reach - stopping early can only refuse,
# never launder. A row is emitted only when merged_at is set; every page keeps
# its oldest-row timestamp working by emitting "-" for a non-merged row's sha,
# and the oldest is read from every row, merged or not. Returns non-zero when a
# read fails.
merged_pr_commits() {
  _floor=$(git log -1 --format=%ct "$1" 2>/dev/null) || return 1
  _page=1
  while [ "$_page" -le 20 ]; do
    _rows=$(gh api "repos/$GH_REPO/pulls?state=closed&sort=updated&direction=desc&per_page=100&page=$_page" \
      --jq '.[] | "\(if .merged_at and .merge_commit_sha then .merge_commit_sha else "-" end) \((.updated_at // .merged_at) | fromdateiso8601)"' 2>>"$PR_ERR") || return 1
    [ -n "$_rows" ] || break
    printf '%s\n' "$_rows" | awk '$1 != "-" {print $1}'
    _old=$(printf '%s\n' "$_rows" | awk 'END {print $2}')
    case "$_old" in ''|*[!0-9]*) break ;; esac
    [ "$_old" -ge "$_floor" ] || break
    _page=$((_page + 1))
  done
}
# pr_of_merge_message <sha>: confirms <sha> through the one PR its GitHub merge
# subject names ("Merge pull request #<n> from ..."). Prints "yes" when that PR
# is merged with merge_commit_sha == <sha>, "no" otherwise (no such subject, no
# such PR - HTTP 404, e.g. a merge subject carried over from another repo -,
# an unmerged PR, another sha); returns non-zero when the read itself failed.
pr_of_merge_message() {
  _n=$(git log -1 --format=%s "$1" | sed -n 's/^Merge pull request #\([0-9][0-9]*\) from .*/\1/p')
  [ -n "$_n" ] || { echo no; return 0; }
  if ! _r=$(gh api "repos/$GH_REPO/pulls/$_n" --jq '"\(.merged_at // "-") \(.merge_commit_sha // "-")"' 2>"$PR_ERR.one"); then
    if grep -q 'HTTP 404' "$PR_ERR.one"; then echo no; return 0; fi
    cat "$PR_ERR.one" >>"$PR_ERR"; return 1
  fi
  case "$_r" in "- "*) echo no ;; *" $1") echo yes ;; *) echo no ;; esac
}
# non_pr_commits <from> <to>: prints one "<sha> (<why>)" line per first-parent
# commit in <from>..<to> that is not recorded as a merged PR's merge commit (see
# the PR-only check above), one "lookup-failed: <sha>" line per commit whose
# PR lookup failed (gh's error is left in $PR_ERR), and one "owner-direct:
# <sha> <email>" line per commit whose author email is in OWNER_DIRECT_EMAILS;
# prints nothing when all of them are merged-PR commits. Returns non-zero when
# the range cannot be walked.
non_pr_commits() {
  _list=$(git rev-list --first-parent "$1..$2") || return 1
  if _merged=$(merged_pr_commits "$1"); then _gh=ok; else _gh=failed; _merged=""; fi
  for _c in $_list; do
    _ae=$(git log -1 --format=%ae "$_c") || return 1
    case " $OWNER_DIRECT_EMAILS " in *" $_ae "*) echo "owner-direct: $_c $_ae"; continue ;; esac
    if [ "$_gh" = failed ]; then echo "lookup-failed: $_c"; continue; fi
    printf '%s\n' "$_merged" | grep -qx "$_c" && continue
    if _yes=$(pr_of_merge_message "$_c"); then
      [ "$_yes" = yes ] || echo "$_c (no merged PR has it as its merge commit)"
    else
      echo "lookup-failed: $_c"
    fi
  done
}
# pr_only_check <prev tag> <prev commit> <commit>: one "owner-direct: <sha>
# <email>" line per allowlisted commit in prev..commit, then the refusal reason
# as the last line unless every other first-parent commit came through a merged
# PR. Callers split the two with owner_direct_of / pr_only_why_of.
pr_only_check() {
  [ -n "$2" ] || { echo "the commit of previous release tag $1 is unknown (not on $REMOTE${UPSTREAM:+ or $UPSTREAM})"; return; }
  git cat-file -e "$2^{commit}" 2>/dev/null || { echo "previous release tag $1 ($2) is not available locally (git fetch $REMOTE first)"; return; }
  git cat-file -e "$3^{commit}" 2>/dev/null || { echo "commit $3 is not available locally (git fetch $REMOTE first)"; return; }
  git merge-base --is-ancestor "$2" "$3" || { echo "previous release tag $1 ($2) is not an ancestor of $3: cannot tell what entered $BRANCH"; return; }
  PR_ERR=$(mktemp "${TMPDIR:-/tmp}/release-pr.XXXXXX")
  _all=$(non_pr_commits "$2" "$3") || { rm -f "$PR_ERR" "$PR_ERR.one"; echo "cannot walk $1..$3"; return; }
  _err=$(head -n 1 "$PR_ERR" 2>/dev/null || true); rm -f "$PR_ERR" "$PR_ERR.one"
  owner_direct_of "$_all"
  _bad=$(printf '%s\n' "$_all" | grep -v '^owner-direct: ' | grep -v '^lookup-failed: ' | grep . || true)
  [ -z "$_bad" ] || echo "commits entered $BRANCH since $1 without a merged PR: $(printf '%s\n' "$_bad" | tr '\n' ';' | sed 's/;$//; s/;/; /g')"
  _lf=$(printf '%s\n' "$_all" | sed -n 's/^lookup-failed: //p' | tr '\n' ' ' | sed 's/ $//')
  [ -z "$_lf" ] || echo "PR lookup failed (gh api failed: ${_err:-no error text}) for $_lf - retryable, not a missing PR: re-run once GitHub answers"
}
owner_direct_of() { printf '%s\n' "$1" | grep '^owner-direct: ' || true; }
pr_only_why_of() { printf '%s\n' "$1" | grep -v '^owner-direct: ' | grep . || true; }
# manifest_of <tag> <commit> <SHA256SUMS file>: release-manifest.json, one
# canonical byte form - release mode writes it and --verify re-derives it from
# the downloaded SHA256SUMS and compares bytes, so no JSON parser is needed.
manifest_of() {
  printf '{\n  "tag": "%s",\n  "commit": "%s",\n' "$1" "$2"
  printf '  "order": "aihub first: the aihub image and the pf binaries are published with this tag before tools/install.sh can consume the release; the pi-web package rides the same release",\n'
  printf '  "artifacts": [\n'
  awk 'NF == 2 { sub(/^\*/, "", $2); printf "%s    {\"name\": \"%s\", \"sha256\": \"%s\"}", (n++ ? ",\n" : ""), $2, $1 } END { print "" }' "$3"
  printf '  ]\n}\n'
}
# rank_against <semver tags> <tag>: how <tag> ranks among the OTHER tags (the
# monotonicity rule --verify judges and a resumed release must satisfy): sets
# NOTGT (every other tag whose key is >= <tag>'s - a newer release, or a
# spelling like v1.02.0 of the same key), HIGH (the highest other tag below
# <tag>: its previous release) and OTHERS (how many tags were compared). A tag
# of the cutover line is judged inside that line only (the retired and older
# lines are numerically higher and never its successors), and the cutover
# tag's previous release is the highest retired v2.20.x (else public v0.1.x)
# tag. A suffixed tag (vX.Y.Z-<name>.N, all of one name) is keyed on its four
# numbers, no cutover applies, and with no lower suffixed tag its previous
# release is its public base vX.Y.Z on UPSTREAM. HIGHC: HIGH's commit, read
# from the ls-remote HIGH comes from (LS, or ULS for the public base).
key() {
  if [ -n "$SUFFIX" ]; then
    printf '%s\n' "$1" | sed 's/^v//; s/-[^.]*\././' | awk -F. '{printf "%012d%012d%012d%012d", $1, $2, $3, $4}'
  else
    printf '%s\n' "$1" | sed 's/^v//' | awk -F. '{printf "%012d%012d%012d", $1, $2, $3}'
  fi
}
gt() { [ "$1" != "$2" ] && [ "$(printf '%s\n%s\n' "$1" "$2" | LC_ALL=C sort | tail -n1)" = "$1" ]; }
rank_against() {
  _mykey=$(key "$2")
  NOTGT=""; OTHERS=0; HIGH=""
  _cmp=$(printf '%s\n' "$1" | grep -vx "$2" || true)
  if [ -z "$SUFFIX" ]; then
    case "$2" in "$CUTOVER_LINE"*) _cmp=$(printf '%s\n' "$_cmp" | awk -v p="$CUTOVER_LINE" 'index($0, p) == 1' || true) ;; esac
  fi
  for _t in $_cmp; do
    OTHERS=$((OTHERS + 1))
    _k=$(key "$_t")
    if [ "$_k" = "$_mykey" ] || gt "$_k" "$_mykey"; then NOTGT="$NOTGT $_t"; else
      if [ -z "$HIGH" ] || gt "$_k" "$(key "$HIGH")"; then HIGH=$_t; fi
    fi
  done
  if [ "$2" = "$CUTOVER_TAG" ] && [ -z "$HIGH" ]; then
    HIGH=$(printf '%s\n' "$1" | awk -v p="$CUTOVER_FROM" -v q="$CUTOVER_PUBLIC_FROM" 'index($0, p) == 1 || index($0, q) == 1' | tail -n 1 || true)
    [ -z "$HIGH" ] || OTHERS=$((OTHERS + 1))
  fi
  HIGHC=""
  if [ -n "$HIGH" ]; then
    HIGHC=$(tag_commit "$LS" "$HIGH")
  elif [ -n "$SUFFIX" ]; then
    HIGH=${2%-"$SUFFIX".*}; HIGHC=$(tag_commit "$ULS" "$HIGH")
  fi
}
# registry_digest <ref>: prints the registry's manifest digest of <ref> and
# returns 0; returns 1 when the registry answered that <ref> does not exist,
# 2 when it could not be read at all (its error on stderr). Only an explicit
# not-found answer is "absent": an unreadable registry is never taken for a
# missing image, so a resume never pushes over an image it could not see.
registry_digest() {
  if _o=$(docker buildx imagetools inspect "$1" 2>&1); then
    printf '%s\n' "$_o" | awk '/^Digest:/{print $2; exit}'; return 0
  fi
  case "$_o" in
    *"not a docker command"*|*"unknown command"*) ;;   # no buildx: ask docker manifest
    *"not found"*|*"manifest unknown"*|*MANIFEST_UNKNOWN*) return 1 ;;
    *) printf '%s\n' "$_o" | tail -n 1 >&2; return 2 ;;   # unreadable: never guess
  esac
  # no buildx: `docker manifest inspect -v` prints the manifest's own digest
  # as "digest" inside "Descriptor"; the config and layer digests are not it.
  if _o=$(docker manifest inspect -v "$1" 2>&1); then
    printf '%s\n' "$_o" | sed -n '/"Descriptor"/,/}/s/.*"[Dd]igest": *"\(sha256:[0-9a-f]*\)".*/\1/p' | head -1; return 0
  fi
  case "$_o" in *"no such manifest"*|*"manifest unknown"*|*"not found"*) return 1 ;; esac
  printf '%s\n' "$_o" | tail -n 1 >&2
  return 2
}
# tgz_version <tarball>: the version in the packed package/package.json.
tgz_version() {
  tar -xzOf "$1" package/package.json 2>/dev/null | sed -n 's/^[[:space:]]*"version":[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1
}

case "${1:-}" in
  -h|--help) usage; echo; echo "--verify <tag> checks an already published release without changing anything."; exit 0 ;;
esac

# The optional suffix configuration (see "Suffixed versions" above), read
# before any work in both modes; a malformed one refuses.
if _top=$(git rev-parse --show-toplevel 2>/dev/null) && [ -f "$_top/$RELEASE_CONF" ]; then
  SUFFIX=$(sed -n 's/^suffix=//p' "$_top/$RELEASE_CONF" | tail -n 1)
  UPSTREAM=$(sed -n 's/^upstream=//p' "$_top/$RELEASE_CONF" | tail -n 1)
  printf '%s\n' "$SUFFIX" | grep -Eqx '[A-Za-z][A-Za-z0-9]*' \
    || die "$RELEASE_CONF: suffix=\"$SUFFIX\" is not one alphanumeric identifier starting with a letter; nothing done"
  [ -n "$UPSTREAM" ] || die "$RELEASE_CONF: suffix=$SUFFIX needs upstream=<the public repository> (its vX.Y.Z tags are the base versions); nothing done"
  TAG_RE="^v[0-9]+\.[0-9]+\.[0-9]+-$SUFFIX\.[0-9]+\$"
fi

# The tag (git push REMOTE) and the release (gh -R GH_REPO) must land in ONE
# repository: the default is the internal release repo, and every configured
# URL of REMOTE - url and pushurl, the push goes to the latter - that names a
# github.com repo must name GH_REPO. A URL this cannot read as a github.com
# repo (another host, an ssh host alias, a path) fails closed: it refuses
# unless RELEASE_REMOTE_IS=<owner>/<repo> states which GitHub repo REMOTE is,
# and that must be GH_REPO too. Checked before any work in both modes; --help
# stays available regardless.
if [ -n "${RELEASE_GH_REPO:-}" ]; then _src="RELEASE_GH_REPO"; else _src="the default; RELEASE_GH_REPO is unset"; fi
_unread=""
for _u in $(remote_urls); do
  _r=$(gh_repo_of "$_u")
  if [ -z "$_r" ]; then _unread="$_unread $_u"; continue; fi
  [ "$(lc "$_r")" = "$(lc "$GH_REPO")" ] \
    || die "the tag goes to $REMOTE ($_r, $_u) but gh would publish to $GH_REPO ($_src): a release must live where its tag is - set RELEASE_GH_REPO=$_r, or run from a clone of $GH_REPO; nothing done"
done
[ -n "$(remote_urls)" ] || _unread=" <no url configured>"
if [ -n "$_unread" ]; then
  [ -n "${RELEASE_REMOTE_IS:-}" ] \
    || die "cannot tell which GitHub repo $REMOTE is ($_unread ): a release must live where its tag is, so this refuses - set RELEASE_REMOTE_IS=<owner>/<repo> to state it (it must equal the gh repo, $GH_REPO); nothing done"
  [ "$(lc "$RELEASE_REMOTE_IS")" = "$(lc "$GH_REPO")" ] \
    || die "the tag goes to $REMOTE (RELEASE_REMOTE_IS=$RELEASE_REMOTE_IS) but gh would publish to $GH_REPO ($_src): a release must live where its tag is; nothing done"
fi

# ---------------------------------------------------------------- verify mode
if [ "${1:-}" = "--verify" ]; then
  VTAG="${2:-}"
  [ -n "$VTAG" ] || { usage >&2; exit 2; }
  [ $# -eq 2 ] || { usage >&2; exit 2; }
  FAILS=0
  pass() { echo "PASS $*"; }
  fail() { echo "FAIL $*"; FAILS=$((FAILS + 1)); }

  # 1. tag format + strict monotonicity against the previous origin tag.
  if printf '%s\n' "$VTAG" | grep -Eq "$TAG_RE"; then
    pass "tag format: $VTAG"
    if LS=$(ls_tags); then RT=$(tags_of "$LS"); else RT=""; fail "git ls-remote --tags $REMOTE failed: cannot check the tag ordering"; fi
    if [ -n "$SUFFIX" ] && ! ULS=$(git ls-remote --tags "$UPSTREAM" 2>/dev/null); then
      ULS=""; fail "git ls-remote --tags $UPSTREAM (upstream, $RELEASE_CONF) failed: cannot read the public base release"
    fi
    if printf '%s\n' "$RT" | grep -qx "$VTAG"; then
      pass "tag exists on $REMOTE"
    else
      fail "tag $VTAG not found on $REMOTE"
    fi
    rank_against "$RT" "$VTAG"
    if [ -n "$NOTGT" ]; then
      fail "monotonic: $VTAG is not strictly greater than every other tag on $REMOTE (not greater:$NOTGT)"
    elif [ "$OTHERS" -eq 0 ]; then
      pass "monotonic: no other tag on $REMOTE (first release)"
    else
      pass "monotonic: $VTAG > every other tag on $REMOTE (highest other $HIGH)"
    fi
    # PR-only: what entered the branch between the previous release and this one.
    VC=$(tag_commit "${LS:-}" "$VTAG")
    if [ -z "$VC" ]; then
      fail "pr-only: commit of $VTAG unknown on $REMOTE"
    elif [ -n "$NOTGT" ]; then
      fail "pr-only: no previous release to check against ($VTAG is not the latest tag)"
    elif [ -z "$HIGH" ]; then
      pass "pr-only: first release, no previous tag to check against"
    else
      PRO=$(pr_only_check "$HIGH" "$HIGHC" "$VC")
      owner_direct_of "$PRO" | while IFS= read -r l; do pass "pr-only: $l"; done
      WHY=$(pr_only_why_of "$PRO" | tr '\n' ';' | sed 's/;$//; s/;/; /g')
      if [ -z "$WHY" ]; then
        pass "pr-only: every commit on $BRANCH since $HIGH came through a merged PR or is owner-direct"
      else
        fail "pr-only: $WHY"
      fi
    fi
  else
    fail "tag format: \"$VTAG\" does not match $TAG_RE"
  fi

  VWORK=$(mktemp -d "${TMPDIR:-/tmp}/release-verify.XXXXXX")
  trap 'rm -rf "$VWORK"' EXIT INT TERM

  # 2. release exists with exactly the 5 payload assets + SHA256SUMS +
  # release-manifest.json.
  WANT=$(printf '%s\n' SHA256SUMS aihub-image.digest pf-darwin-amd64 pf-darwin-arm64 pf-linux-amd64 \
    release-manifest.json "silgrid-pi-web-$VTAG.tgz" | LC_ALL=C sort)
  if gh_ release view "$VTAG" --json assets,body -q '.assets[].name' >"$VWORK/assets.raw" 2>"$VWORK/view.err"; then
    pass "release $VTAG exists"
    LC_ALL=C sort "$VWORK/assets.raw" >"$VWORK/assets.txt"
    if [ "$(cat "$VWORK/assets.txt")" = "$WANT" ]; then
      pass "exactly the 5 payload assets + SHA256SUMS + release-manifest.json: $(tr '\n' ' ' <"$VWORK/assets.txt")"
    else
      fail "asset set differs; want [$(echo "$WANT" | tr '\n' ' ')] got [$(tr '\n' ' ' <"$VWORK/assets.txt")]"
    fi
    BODY=$(gh_ release view "$VTAG" --json assets,body -q '.body' 2>/dev/null || true)
    if [ -n "$BODY" ]; then pass "release notes non-empty"; else fail "release notes empty"; fi
  else
    fail "release $VTAG not found: $(head -1 "$VWORK/view.err")"
  fi

  # 3. download + checksums.
  mkdir "$VWORK/dl"
  if gh_ release download "$VTAG" -D "$VWORK/dl" >"$VWORK/dl.log" 2>&1; then
    if (cd "$VWORK/dl" && sha256sum -c SHA256SUMS) >"$VWORK/sha.log" 2>&1; then
      pass "sha256sum -c SHA256SUMS"
    else
      fail "sha256sum -c SHA256SUMS: $(tr '\n' ' ' <"$VWORK/sha.log")"
    fi
    if [ -n "${VC:-}" ] && [ -f "$VWORK/dl/SHA256SUMS" ] && manifest_of "$VTAG" "$VC" "$VWORK/dl/SHA256SUMS" >"$VWORK/manifest.want" \
      && cmp -s "$VWORK/manifest.want" "$VWORK/dl/release-manifest.json"; then
      pass "release-manifest.json: tag $VTAG, commit $VC, every SHA256SUMS payload"
    else
      fail "release-manifest.json does not match the tag, its commit and SHA256SUMS"
    fi
    PV=$(tgz_version "$VWORK/dl/silgrid-pi-web-$VTAG.tgz")
    if [ "$PV" = "${VTAG#v}" ]; then
      pass "pi-web package version $PV = tag"
    else
      fail "pi-web package version \"${PV:-<unreadable>}\" is not ${VTAG#v}"
    fi
    # 4. registry digest == aihub-image.digest.
    WANTD=$(tr -d ' \t\r\n' <"$VWORK/dl/aihub-image.digest" 2>/dev/null || true)
    if printf '%s' "$WANTD" | grep -Eq '^sha256:[0-9a-f]{64}$'; then
      pass "aihub-image.digest format: sha256:<64 hex>"
    else
      fail "aihub-image.digest format: \"${WANTD:-<empty>}\" is not sha256:<64 hex>"
    fi
    GOTD=$(docker buildx imagetools inspect "$IMAGE:$VTAG" 2>/dev/null | awk '/^Digest:/{print $2; exit}' || true)
    if [ -z "$GOTD" ]; then
      # no buildx: `docker manifest inspect -v` prints the manifest's own
      # digest as "digest" (lowercase) inside "Descriptor"; the config and
      # layer digests that follow are not it.
      GOTD=$(docker manifest inspect -v "$IMAGE:$VTAG" 2>/dev/null | sed -n '/"Descriptor"/,/}/s/.*"[Dd]igest": *"\(sha256:[0-9a-f]*\)".*/\1/p' | head -1 || true)
    fi
    if [ -z "$GOTD" ]; then
      fail "registry digest of $IMAGE:$VTAG unreadable"
    elif [ -n "$WANTD" ] && [ "$GOTD" = "$WANTD" ]; then
      pass "image digest $GOTD matches aihub-image.digest"
    else
      fail "image digest mismatch: registry $GOTD vs aihub-image.digest ${WANTD:-<empty>}"
    fi
  else
    fail "gh release download: $(head -1 "$VWORK/dl.log")"
  fi

  if [ "$FAILS" -eq 0 ]; then echo "verify: ALL CHECKS PASSED for $VTAG"; exit 0; fi
  echo "verify: $FAILS CHECK(S) FAILED for $VTAG"
  exit 1
fi

# --------------------------------------------------------------- release mode
BUMP="${1:-patch}"
case "$BUMP" in
  patch) ;;
  minor|major) die "bump $BUMP refused, nothing published: 中间版本号跳跃需 owner 明确声明 - routine releases are patch only; a minor/major jump needs an explicit owner ruling (CONTRIBUTING.md \"Version line\")" ;;
  *) usage >&2; die "bump must be patch (got \"$BUMP\")" ;;
esac
[ $# -le 1 ] || { usage >&2; exit 2; }

cd "$(git rev-parse --show-toplevel)"

# 1. clean tree, HEAD == origin/main.
[ -z "$(git status --porcelain)" ] || die "worktree is dirty; commit or stash first"
git fetch "$REMOTE" >/dev/null 2>&1 || die "git fetch $REMOTE failed"
COMMIT=$(git rev-parse HEAD)
OMAIN=$(git rev-parse "refs/remotes/$REMOTE/$BRANCH" 2>/dev/null) || die "$REMOTE/$BRANCH not found"
[ "$COMMIT" = "$OMAIN" ] || die "HEAD ($COMMIT) is not $REMOTE/$BRANCH ($OMAIN); release only from the pushed tip of $BRANCH"

# 1b. the origin tags are the only source of truth: a failing ls-remote is fatal.
LS=$(ls_tags) || die "git ls-remote --tags $REMOTE failed - cannot read the origin tags; nothing built or published"
RT=$(tags_of "$LS")
if [ -n "$SUFFIX" ]; then
  ULS=$(git ls-remote --tags "$UPSTREAM" 2>/dev/null) || die "git ls-remote --tags $UPSTREAM (upstream, $RELEASE_CONF) failed - cannot read the public releases; nothing built or published"
fi
# A commit that already carries its release tag on origin is never released as
# a second version: that release is RESUMED. An earlier run may
# have died after the claim (#120, #265), or a later lane may carry it on to
# deploy (#285); before, every such run was refused and the version burned.
HEADTAGS=$(tags_at "$LS" "$COMMIT")
RESUME=""; PREVCOMMIT=""
case "$(printf '%s\n' "$HEADTAGS" | grep -c . || true)" in
  0)
    if [ -n "$SUFFIX" ]; then
      # version = <public base>-<suffix>.<next N on ORIGIN>; local tags are ignored.
      BASE=""
      for _t in $(tags_of "$ULS" "$SEMVER_RE" | sort -rV); do
        if git merge-base --is-ancestor "$(tag_commit "$ULS" "$_t")" "$COMMIT" 2>/dev/null; then BASE=$_t; break; fi
      done
      [ -n "$BASE" ] || die "no vX.Y.Z tag of $UPSTREAM (upstream) is in HEAD's history - refusing to invent a base version; nothing built or published"
      _n=$(printf '%s\n' "$RT" | awk -v p="$BASE-$SUFFIX." 'index($0, p) == 1 {print substr($0, length(p) + 1) + 0}' | sort -n | tail -n 1)
      TAG="$BASE-$SUFFIX.$((${_n:-0} + 1))"
      rank_against "$RT" "$TAG"
      [ -z "$NOTGT" ] || die "version $TAG is not above every $SUFFIX tag on $REMOTE (not below it:$NOTGT): HEAD does not contain the public base of the newest one; nothing published, a human decides"
      PREVTAG="$HIGH"; PREVCOMMIT="$HIGHC"
      echo "release: public base $BASE ($UPSTREAM) -> version $TAG (suffix $SUFFIX), commit $COMMIT"
    else
      # version = bump(max semver of the ORIGIN tags); local tags are ignored.
      # After the cutover the floor is the highest tag of the cutover line.
      if printf '%s\n' "$RT" | grep -qx "$CUTOVER_TAG"; then
        FLOOR=$(printf '%s\n' "$RT" | awk -v p="$CUTOVER_LINE" 'index($0, p) == 1' | tail -1)
      else
        FLOOR=$(printf '%s\n' "$RT" | tail -1)
      fi
      [ -n "$FLOOR" ] || die "no vX.Y.Z tag on $REMOTE - refusing to invent a base version"
      case "$FLOOR" in
        "$CUTOVER_FROM"*|"$CUTOVER_PUBLIC_FROM"*)
          TAG="$CUTOVER_TAG"
          echo "release: floor $FLOOR -> version $TAG (the named ${FLOOR%.*}.x -> $CUTOVER_TAG version-line cutover), commit $COMMIT" ;;
        *)
          _v=${FLOOR#v}; _maj=${_v%%.*}; _r=${_v#*.}; _min=${_r%%.*}; _pat=${_r#*.}
          _pat=$((_pat + 1))
          TAG="v${_maj}.${_min}.${_pat}"
          echo "release: floor $FLOOR -> version $TAG ($BUMP bump), commit $COMMIT" ;;
      esac
      PREVTAG="$FLOOR"; PREVCOMMIT=$(tag_commit "$LS" "$PREVTAG")
    fi
    if printf '%s\n' "$LS" | grep -q "[[:space:]]refs/tags/$TAG\(\^{}\)\{0,1\}\$"; then
      die "tag $TAG already exists on $REMOTE; nothing published"
    fi ;;
  1)
    TAG=$HEADTAGS; RESUME=1
    rank_against "$RT" "$TAG"
    [ -z "$NOTGT" ] || die "HEAD ($COMMIT) carries $TAG on $REMOTE, but $REMOTE also carries tag(s) not below it:$NOTGT - $TAG would not be the latest release; nothing published, a human decides"
    PREVTAG="$HIGH"; PREVCOMMIT="$HIGHC"
    echo "release: HEAD ($COMMIT) already carries $TAG on $REMOTE - resuming $TAG: what is already published is checked and kept, only what is missing is published (a second version of one commit is never cut)" ;;
  *)
    die "HEAD ($COMMIT) carries more than one release tag on $REMOTE ($(printf '%s\n' "$HEADTAGS" | tr '\n' ' ' | sed 's/ $//')): cannot tell which one to resume; nothing published, a human decides" ;;
esac

WORK=$(mktemp -d "${TMPDIR:-/tmp}/release.XXXXXX")
trap 'rm -rf "$WORK"' EXIT INT TERM
OUT="$WORK/out"; mkdir "$OUT"
PIWEB_TGZ="silgrid-pi-web-$TAG.tgz"
PAYLOADS="pf-linux-amd64 pf-darwin-arm64 pf-darwin-amd64 aihub-image.digest $PIWEB_TGZ"
ASSETS="$PAYLOADS SHA256SUMS release-manifest.json"
printf '%s\n' $ASSETS >"$WORK/assets.want"
REL_EXISTS=""; REL_DRAFT=""; REL_ASSETS=""
has_asset() { printf '%s\n' "$REL_ASSETS" | grep -qx "$1"; }
NEED_BIN=1; NEED_IMAGE=1; NEED_PIWEB=1; DIGEST=""
# A resume publishes into a version another run may be publishing too (a
# second lane, a hand run, an orphaned earlier run): unlike a fresh release it
# holds no exclusive claim. So it snapshots what is published when it reads
# it, and re-reads both right before each publish action (resume_recheck):
# any change, or a state that can no longer be read, refuses with nothing
# more pushed - it never publishes from a stale picture.
REL_SNAP=""; REG_SNAP=""; PUSHED_BY_RUN=""
rel_snap_of() {   # <exists> <draft> <asset names>: one comparable line
  if [ -n "$1" ]; then
    printf 'present draft=%s assets=%s\n' "$2" "$(printf '%s\n' "$3" | grep . | LC_ALL=C sort | tr '\n' ' ' | sed 's/ $//')"
  else
    echo absent
  fi
}
release_snapshot() {   # the release's state now; non-zero when unreadable
  if _ra=$(gh_ release view "$TAG" --json assets -q '.assets[].name' 2>"$WORK/view.err"); then
    _rd=$(gh_ release view "$TAG" --json isDraft -q '.isDraft' 2>"$WORK/view.err") || return 1
    rel_snap_of 1 "$_rd" "$_ra"
  elif grep -qi 'release not found' "$WORK/view.err"; then
    echo absent
  else
    return 1
  fi
}
registry_snapshot() {   # the image's state now: "present <digest>" | absent; non-zero when unreadable
  if registry_digest "$IMAGE:$TAG" >"$WORK/regdigest" 2>"$WORK/reg.err"; then _r=0; else _r=$?; fi
  case "$_r" in
    0) printf 'present %s\n' "$(tr -d ' \t\r\n' <"$WORK/regdigest")" ;;
    1) echo absent ;;
    *) return 1 ;;
  esac
}
resume_recheck() {   # <the action about to run>
  [ -n "$RESUME" ] || return 0
  _rel_now=$(release_snapshot) || _rel_now="unreadable ($(head -n 1 "$WORK/view.err"))"
  _reg_now=$(registry_snapshot) || _reg_now="unreadable ($(head -n 1 "$WORK/reg.err"))"
  if [ "$_rel_now" != "$REL_SNAP" ] || [ "$_reg_now" != "$REG_SNAP" ]; then
    die "resume of $TAG stopped before $1: what is published changed since this run read it, or can no longer be read (GitHub release: $REL_SNAP -> $_rel_now; image $IMAGE:$TAG: $REG_SNAP -> $_reg_now) - another run may be publishing $TAG. Published by this run: ${PUSHED_BY_RUN:-nothing}. $TAG stays claimed: run again once that run has finished - a complete release is adopted, a partial one resumed"
  fi
  echo "release: resume $TAG: re-read before $1 - unchanged"
}

# 1b''. resume: read what of $TAG is already published (read-only). Done:
# kept as published (downloaded, never re-uploaded or re-pushed). Missing:
# built and published below. Contradictory (a published file the checksums
# do not cover, a digest the registry does not hold): a human decides.
if [ -n "$RESUME" ]; then
  if REL_ASSETS=$(gh_ release view "$TAG" --json assets -q '.assets[].name' 2>"$WORK/view.err"); then
    REL_EXISTS=1
    REL_DRAFT=$(gh_ release view "$TAG" --json isDraft -q '.isDraft' 2>"$WORK/view.err") \
      || die "cannot read whether release $TAG is a draft: $(head -n 1 "$WORK/view.err") - retryable; nothing published"
  elif grep -qi 'release not found' "$WORK/view.err"; then
    REL_ASSETS=""
  else
    die "cannot read the GitHub release $TAG of $GH_REPO: $(head -n 1 "$WORK/view.err") - retryable; nothing published"
  fi
  _extra=$(printf '%s\n' "$REL_ASSETS" | grep . | grep -vxFf "$WORK/assets.want" || true)
  [ -z "$_extra" ] || die "release $TAG carries asset(s) this procedure never publishes: $(printf '%s\n' "$_extra" | tr '\n' ' ')- a human decides; nothing published"
  MISSING_ASSETS=""
  for _a in $ASSETS; do has_asset "$_a" || MISSING_ASSETS="$MISSING_ASSETS $_a"; done
  if [ -n "$REL_EXISTS" ]; then _rs="present${REL_DRAFT:+ (draft: $REL_DRAFT)}"; else _rs="absent"; fi
  echo "release: resume $TAG: tag on $REMOTE at $COMMIT; GitHub release $TAG of $GH_REPO: $_rs; missing assets:${MISSING_ASSETS:- none}"
  if [ -n "$REL_EXISTS" ] && [ -z "$MISSING_ASSETS" ] && [ "$REL_DRAFT" != true ]; then
    echo "release: $TAG is already published and complete - nothing to publish; release.sh --verify $TAG audits it"
    echo "version=$TAG"
    echo "commit=$COMMIT"
    echo "notes=https://github.com/$GH_REPO/releases/tag/$TAG"
    exit 0
  fi
  if has_asset SHA256SUMS || has_asset release-manifest.json; then
    for _a in $PAYLOADS; do
      has_asset "$_a" || die "release $TAG already carries SHA256SUMS or release-manifest.json but lacks $_a: they describe bytes this run cannot reproduce - a human decides; nothing published"
    done
  fi
  # the published payloads (and checksums), byte for byte
  if [ -n "$REL_EXISTS" ] && [ -n "$REL_ASSETS" ]; then
    mkdir "$WORK/dl"
    set --
    for _a in $ASSETS; do has_asset "$_a" && set -- "$@" -p "$_a"; done
    gh_ release download "$TAG" -D "$WORK/dl" "$@" >"$WORK/dl.log" 2>&1 \
      || die "downloading the published assets of $TAG failed: $(head -n 1 "$WORK/dl.log") - retryable; nothing published"
    for _a in $ASSETS; do
      has_asset "$_a" || continue
      [ -f "$WORK/dl/$_a" ] || die "the published $_a of $TAG did not download - retryable; nothing published"
      case " $PAYLOADS " in *" $_a "*) cp "$WORK/dl/$_a" "$OUT/$_a" ;; esac
    done
    if has_asset SHA256SUMS; then
      (cd "$WORK/dl" && sha256sum -c SHA256SUMS) >"$WORK/sha.log" 2>&1 \
        || die "the published assets of $TAG do not match its published SHA256SUMS: $(tr '\n' ' ' <"$WORK/sha.log")- a human decides; nothing published"
    fi
  fi
  DIGEST_PUB=""
  if [ -f "$OUT/aihub-image.digest" ]; then DIGEST_PUB=$(tr -d ' \t\r\n' <"$OUT/aihub-image.digest"); fi
  REL_SNAP=$(rel_snap_of "$REL_EXISTS" "$REL_DRAFT" "$REL_ASSETS")
fi

# 1b'''. preflight: every machine prerequisite of the rest of
# the run is checked HERE, before the gates, the build and the claim, and
# every problem is reported in ONE message. Before it, a missing prerequisite
# surfaced in the middle of the run: the node major in the test gate (#144),
# the ghcr.io push credential only AFTER the tag was pushed (#265: the login
# was skipped because docker's config already named ghcr.io, with a
# credential that could not push).
#   - tools: git gh docker npm node sha256sum tar, plus make and go for the
#     default build, python3 for the default xrefs gate and the default
#     test gate (workflows/_shared/scripts/gate.py), which needs python3
#     >= 3.9 (an older one is a preflight problem, never a gate verdict);
#   - node: its major must equal the repo-root .nvmrc's (when present) - for
#     the build on this host; the test gate runs in its container, with the
#     image's own node;
#   - the ghcr.io credential (an image on ghcr.io): a stored ghcr.io login is
#     kept when it can read the image's repository (the previous tag's
#     image; this cannot prove it can push, and the run says so); a stored
#     login that cannot read, or no stored login, is replaced by / logged in
#     with gh's token when that token carries write:packages (X-Oauth-Scopes)
#     or its scopes are unreadable (a fine-grained token) - else a problem.
#     A resume reads the registry only AFTER this, with the same identity it
#     pushes with.
PF_PROBLEMS=""
pf_problem() { PF_PROBLEMS="$PF_PROBLEMS
  - $*"; }
_need="git gh docker npm node sha256sum tar"
[ -n "${RELEASE_BUILD_CMD:-}" ] || _need="$_need make go"
[ -n "${RELEASE_XREFS_CMD:-}" ] && [ -n "${RELEASE_TEST_CMD:-}" ] || _need="$_need python3"
_miss=""
for _c in $_need; do command -v "$_c" >/dev/null 2>&1 || _miss="$_miss $_c"; done
[ -z "$_miss" ] || pf_problem "tool(s) not on PATH:$_miss"
if [ -z "${RELEASE_TEST_CMD:-}" ] && command -v python3 >/dev/null 2>&1 \
  && ! python3 -c 'import sys; sys.exit(sys.version_info < (3, 9))' >/dev/null 2>&1; then
  pf_problem "python3 $(python3 -c 'import platform; print(platform.python_version())' 2>/dev/null || echo '(version unreadable)') is older than 3.9: the test gate (workflows/_shared/scripts/gate.py) needs python3 >= 3.9 - install a newer one first"
fi
if [ -f .nvmrc ] && command -v node >/dev/null 2>&1; then
  _want=$(sed -n '1{s/^[[:space:]]*v\{0,1\}//; s/[.[:space:]].*$//; p;}' .nvmrc)
  _have=$(node --version 2>/dev/null | sed 's/^v//; s/\..*$//')
  [ "$_want" = "$_have" ] || pf_problem "node $(node --version 2>/dev/null) is not major $_want (pinned by .nvmrc; the build on this host needs it - the test gate uses the node of its container): install node $_want.x first"
fi
DCFG="${DOCKER_CONFIG:-$HOME/.docker}/config.json"
case "$IMAGE" in
  ghcr.io/*)
    _stored=""; [ -f "$DCFG" ] && grep -q 'ghcr\.io' "$DCFG" && _stored=1
    _scopes=$(gh api user -i 2>/dev/null | tr -d '\r' | sed -n 's/^[Xx]-[Oo][Aa]uth-[Ss]copes:[[:space:]]*//p' | head -n 1 || true)
    # gh's token may log docker in for a push: its scopes carry
    # write:packages, or they are unreadable (a fine-grained token)
    _ghok=1
    if [ -n "$_scopes" ]; then case ", $_scopes," in *", write:packages,"*) ;; *) _ghok="" ;; esac; fi
    _probe="$IMAGE:${PREVTAG:-$TAG}"
    _login=""
    if [ -n "$_stored" ]; then
      # a stored login is kept unless it cannot even read the image's
      # repository: replacing a working credential is not this script's call
      if registry_digest "$_probe" >/dev/null 2>"$WORK/probe.err"; then _pr=0; else _pr=$?; fi
      if [ "$_pr" -ne 2 ]; then
        echo "release: keeping the ghcr.io login stored in $DCFG: it reads $_probe. This preflight cannot prove it can push; a denied push leaves the version claimed and resumable - log in again (gh auth token | docker login ghcr.io -u <user> --password-stdin) and run again"
      elif [ -n "$_ghok" ]; then
        _login="replacing the stored ghcr.io login, which cannot read $_probe: $(head -n 1 "$WORK/probe.err")"
      else
        pf_problem "the ghcr.io login stored in $DCFG cannot read $_probe ($(head -n 1 "$WORK/probe.err")) and gh's token lacks write:packages (scopes: $_scopes) - run: gh auth refresh -h github.com -s write:packages"
      fi
    elif [ -n "$_ghok" ]; then
      _login="no ghcr.io login is stored in $DCFG"
    else
      pf_problem "no ghcr.io push credential: gh's token lacks write:packages (scopes: $_scopes) and $DCFG holds no ghcr.io login - run: gh auth refresh -h github.com -s write:packages"
    fi
    if [ -n "$_login" ]; then
      if _lo=$(gh auth token 2>/dev/null | docker login ghcr.io -u "$(gh api user -q .login 2>/dev/null)" --password-stdin 2>&1); then
        echo "release: docker logged in to ghcr.io with gh's token ($_login)"
      else
        pf_problem "docker login ghcr.io with gh's token failed: $(printf '%s' "$_lo" | tail -n 1) (gh auth status?)"
      fi
    fi ;;
esac
[ -z "$PF_PROBLEMS" ] || die "preflight failed - nothing built or published, no version claimed; fix these and run again:$PF_PROBLEMS"
echo "release: preflight passed (tools, node, registry credential)"

# 1b, resume continued - after the preflight's registry login (the read and the push
# use the same identity): the image's state in the registry.
if [ -n "$RESUME" ]; then
  REG_SNAP=$(registry_snapshot) || die "cannot read the registry state of $IMAGE:$TAG: $(head -n 1 "$WORK/reg.err") - retryable; nothing published"
  case "$REG_SNAP" in
    "present "*)
      DIGEST=${REG_SNAP#present }
      printf '%s' "$DIGEST" | grep -Eq '^sha256:[0-9a-f]{64}$' || die "the registry digest of $IMAGE:$TAG is unreadable (\"$DIGEST\") - retryable; nothing published"
      [ -z "$DIGEST_PUB" ] || [ "$DIGEST_PUB" = "$DIGEST" ] || die "the published aihub-image.digest of $TAG ($DIGEST_PUB) is not the registry's digest of $IMAGE:$TAG ($DIGEST) - a human decides; nothing published"
      NEED_IMAGE=""
      echo "release: resume $TAG: image $IMAGE:$TAG is already in the registry ($DIGEST) - kept, not rebuilt or pushed" ;;
    absent)
      [ -z "$DIGEST_PUB" ] || die "release $TAG publishes image digest $DIGEST_PUB but the registry has no $IMAGE:$TAG - a human decides; nothing published" ;;
  esac
  NEED_BIN=""
  for _a in pf-linux-amd64 pf-darwin-arm64 pf-darwin-amd64; do [ -f "$OUT/$_a" ] || NEED_BIN=1; done
  NEED_PIWEB=""; [ -f "$OUT/$PIWEB_TGZ" ] || NEED_PIWEB=1
fi

# 1c. PR-only: every commit that entered the branch since the previous release
# came through a merged PR or is owner-direct (audited with a PASS line). A
# resumed first release has no previous tag to check against.
if [ -n "$PREVTAG" ]; then
  PRO=$(pr_only_check "$PREVTAG" "$PREVCOMMIT" "$COMMIT")
  owner_direct_of "$PRO" | while IFS= read -r l; do echo "PASS pr-only: $l"; done
  WHY=$(pr_only_why_of "$PRO" | tr '\n' ';' | sed 's/;$//; s/;/; /g')
  [ -z "$WHY" ] || die "refusing to release: $WHY; nothing built or published, no version claimed"
else
  echo "release: pr-only: first release, no previous tag to check against"
fi

# A refusal from here on publishes nothing and claims no version (a resumed
# one stays claimed, resumable).
if [ -n "$RESUME" ]; then NOCLAIM="$TAG stays claimed and resumable"; else NOCLAIM="no version claimed ($TAG is still free)"; fi

# 1d. the workflow contract: ContractVersion moves only on an
# incompatible change, at most once per release, and an incompatible change
# is registered in the pending list when it merges. Read the version of the
# previous release and of HEAD, and count the list's entries (its lines
# starting with "## "); anything unreadable refuses. An entry with the version
# not bumped refuses with the way out; a bump releases only by one and with
# the list emptied; an empty list needs no bump. A resume judges against V's
# previous tag; a first release has none.
CONTRACT_GO="internal/common/workflow/contract.go"
CONTRACT_PENDING="internal/common/workflow/contract-pending.md"
# contract_version_at <commit>: the one `const ContractVersion = <n>` line of
# $CONTRACT_GO at <commit>; empty when the file or the line is missing, or
# when there is more than one.
contract_version_at() {
  _cv=$(git show "$1:$CONTRACT_GO" 2>/dev/null | sed -n 's/^const ContractVersion = \([0-9][0-9]*\)$/\1/p')
  [ "$(printf '%s\n' "$_cv" | grep -c .)" -eq 1 ] && printf '%s\n' "$_cv"
  return 0
}
if [ -n "$PREVTAG" ]; then
  _pc=$PREVCOMMIT
  V_PREV=$(contract_version_at "$_pc")
  [ -n "$V_PREV" ] || die "refusing to release: cannot read ContractVersion of the previous release $PREVTAG ($CONTRACT_GO at $_pc); nothing built or published, $NOCLAIM"
  V_HEAD=$(contract_version_at "$COMMIT")
  [ -n "$V_HEAD" ] || die "refusing to release: cannot read ContractVersion at $COMMIT ($CONTRACT_GO: one 'const ContractVersion = <n>' line); nothing built or published, $NOCLAIM"
  _pend=$(git show "$COMMIT:$CONTRACT_PENDING" 2>/dev/null) || die "refusing to release: $CONTRACT_PENDING is missing at $COMMIT - the workflow contract's pending list cannot be read; nothing built or published, $NOCLAIM"
  P=$(printf '%s\n' "$_pend" | grep -c '^## ' || true)
  V_NEXT=$((V_PREV + 1))
  if [ "$V_HEAD" -lt "$V_PREV" ] || [ "$V_HEAD" -gt "$V_NEXT" ]; then
    die "refusing to release: ContractVersion is $V_HEAD, the previous release $PREVTAG has $V_PREV - it moves at most once per release, by one (to $V_NEXT, with every entry of $CONTRACT_PENDING written into its docs/upgrade.md section and the list emptied); fix it in a PR, merge it, then release again; nothing built or published, $NOCLAIM"
  fi
  if [ "$P" -gt 0 ] && [ "$V_HEAD" -eq "$V_PREV" ]; then
    die "refusing to release: $P incompatible workflow-contract change(s) registered in $CONTRACT_PENDING, but ContractVersion is still $V_PREV (as in $PREVTAG) - in one PR bump ContractVersion to $V_NEXT: UpgradeSection names a new docs/upgrade.md section carrying every entry's migration and the drain, regenerate the golden file, the baseline and the lane schema (PF_WRITE_CONTRACT_GOLDEN=1, go generate ./internal/common/workflow), move every built-in lane's contract: line, and empty the list (keep its explanation); merge it, then release again; nothing built or published, $NOCLAIM"
  fi
  if [ "$P" -gt 0 ]; then
    die "refusing to release: ContractVersion is bumped to $V_HEAD, but $CONTRACT_PENDING still holds $P entr$([ "$P" -eq 1 ] && echo y || echo ies) - write them into this bump's docs/upgrade.md section and empty the list (keep its explanation) in a PR, merge it, then release again; nothing built or published, $NOCLAIM"
  fi
  echo "release: contract: $V_HEAD (previous release $V_PREV), no incompatible change registered"
else
  echo "release: contract: first release, no previous tag to compare against"
fi

# 2. xrefs gate, then the test gate. A resume runs them too: a tag proves a
# claim, not that the gates passed (a hand-made tag carries none), and the
# missing parts are built from this tree now. A gate failure publishes
# nothing and claims no version (a resumed one stays claimed, resumable).
echo "release: running the design-doc cross-reference check"
XREFS="docs/design/polyforge-vnext/tools/check-xrefs.py"
if [ -n "${RELEASE_XREFS_CMD:-}" ]; then
  sh -c "$RELEASE_XREFS_CMD" || die "design-doc cross-reference check failed (RELEASE_XREFS_CMD); fix the references (make xrefs) and release again; nothing built or published, $NOCLAIM"
else
  command -v python3 >/dev/null 2>&1 || die "python3 not found: the release gate runs $XREFS and does not skip it; nothing built or published, $NOCLAIM"
  python3 -I "$XREFS" || die "design-doc cross-reference check failed ($XREFS); fix the references (make xrefs) and release again; nothing built or published, $NOCLAIM"
fi
echo "release: running test gate"
# The gate's identity (see "Test gate" above): what a reusable green tag must
# record, and what the release notes name either way.
GATE_ID=$(sh tools/gate-id) || die "cannot compute the test gate's id (tools/gate-id at $COMMIT); nothing built or published, $NOCLAIM"
GREEN=""
if [ -n "${RELEASE_TEST_CMD:-}" ]; then
  # an override replaces the whole gate: no green tag is consulted (a green
  # tag records the gate's plan, which this command is not)
  echo "release: test gate: RELEASE_TEST_CMD replaces the gate (gate $GATE_ID; no green tag consulted)"
  GATE_RECORD="Ran RELEASE_TEST_CMD (gate \`$GATE_ID\`)."
  TGRC=0
  sh -c "$RELEASE_TEST_CMD" || TGRC=$?
  [ "$TGRC" -eq 0 ] || die "test gate failed (sh -c \"\$RELEASE_TEST_CMD\" exited $TGRC; the failing tests are named above); nothing built or published, $NOCLAIM - after a fix, or for a flake, run the release again"
else
  # the tree's gate.py both answers the green-tag check (its --plan-only
  # digest of HEAD's plan) and, without a reusable tag, is the gate
  GATE_PY=workflows/_shared/scripts/gate.py
  [ -f "$GATE_PY" ] || die "the tree carries no test gate ($GATE_PY at $COMMIT); nothing built or published, $NOCLAIM"
  if GREEN=$(sh tools/green-tag check "$COMMIT" --remote "$REMOTE" 2>"$WORK/green.err"); then
    echo "release: test gate: $COMMIT carries the green tag $GREEN on $REMOTE for gate $GATE_ID, written by the gate for this plan - that result is reused, the gate is not run again"
    GATE_RECORD="Not run again: the commit carries the green tag \`$GREEN\` for gate \`$GATE_ID\` (tools/green-tag check: written by gate.py --mark-green after a pass of the same plan on this commit)."
  else
    echo "release: test gate: no reusable green tag ($(tail -n 1 "$WORK/green.err")) - running the gate (gate $GATE_ID)"
    # A red test gate (a flake as often as a defect: #155 #184 #353) is cheap to
    # retry: nothing is built or published and no version is claimed, so the next
    # run computes the same version (a resumed one stays claimed and resumable).
    GATE_RECORD="Ran in the verification container (.polyforge/Dockerfile.verify; the full plan of workflows/_shared/scripts/verify-scope, workflows/_shared/scripts/gate.py) for gate \`$GATE_ID\`."
    TGRC=0
    python3 "$GATE_PY" --isolation container --log-dir "artifacts/release-$TAG/gate" || TGRC=$?
    case "$TGRC" in
      0) ;;
      2) die "test gate not judged: gate.py exited 2 - it could not judge the tree (its container, image or plan, its deadline or an interrupt; the reason is above), which is not a red gate; nothing built or published, $NOCLAIM - fix the cause and run the release again" ;;
      *) die "test gate failed (gate.py exited $TGRC; the failing command and its log tail are above); nothing built or published, $NOCLAIM - after a fix, or for a flake, run the release again" ;;
    esac
  fi
fi

# 2b. production-dependency audit gate: the console SPA build chain put
# npm inside the aihub trust surface, so the release
# refuses when a PRODUCTION dependency carries a known high/critical
# advisory. Two audits, two scopes: the verification bar's dependency-audit
# gate (workflows/_shared/scripts/audit-deps, every full-tier run) audits the whole tree, dev
# dependencies included; this release gate audits production dependencies
# only.
echo "release: running production-dependency audit gate"
if [ -n "${RELEASE_AUDIT_CMD:-}" ]; then
  sh -c "$RELEASE_AUDIT_CMD" || die "production-dependency audit gate failed (RELEASE_AUDIT_CMD); nothing built or published, $NOCLAIM"
else
  (cd webui && npm audit --omit=dev --audit-level=high) || die "production-dependency audit failed: a production dependency of the console SPA carries a known high or critical advisory; nothing built or published, $NOCLAIM"
fi

# build binaries: the full matrix (linux-amd64 pf consumed by the image
# build and the deploy path, the darwin pf binaries shipped as assets). A
# resume builds them only when one is not published yet, and publishes only
# the missing ones (a published binary is never replaced).
export TAG
ART="artifacts/release-$TAG"
if [ -n "$NEED_BIN" ]; then
  if [ -n "${RELEASE_BUILD_CMD:-}" ]; then sh -c "$RELEASE_BUILD_CMD"; else GOWORK=off make release VERSION="$TAG"; fi
  MISSING=""
  for n in pf pf-darwin-arm64 pf-darwin-amd64; do
    [ -f "$ART/$n" ] || MISSING="$MISSING $ART/$n"
  done
  [ -z "$MISSING" ] || die "build did not produce the 3-binary pf matrix, missing:$MISSING"
  [ -f "$OUT/pf-linux-amd64" ] || cp "$ART/pf" "$OUT/pf-linux-amd64"
  for a in darwin-arm64 darwin-amd64; do
    [ -f "$OUT/pf-$a" ] || cp "$ART/pf-$a" "$OUT/pf-$a"
  done
fi

# build image (context: temp dir with the linux binary as `pf` - the
# published one when a resume found it; the entrypoint runs `pf aihub`).
if [ -n "$NEED_IMAGE" ]; then
  mkdir "$WORK/ctx"
  cp "$OUT/pf-linux-amd64" "$WORK/ctx/pf"
  docker build -f deploy/Dockerfile -t "$IMAGE:$TAG" "$WORK/ctx"
fi

# pack the pi-web package (see step 3 above) from the released commit's tree.
if [ -n "$NEED_PIWEB" ]; then
  mkdir "$WORK/piweb" "$WORK/pack"
  git archive "$COMMIT" pi-web | tar -x -C "$WORK/piweb" || die "exporting pi-web/ of $COMMIT failed; nothing published"
  ( cd "$WORK/piweb/pi-web" \
    && npm ci --no-audit --no-fund \
    && PI_WEB_RELEASE_VERSION="$TAG" npm run build \
    && npm version "${TAG#v}" --no-git-tag-version \
    && npm pack --pack-destination "$WORK/pack" ) || die "building or packing the pi-web package failed; nothing published"
  set -- "$WORK/pack"/*.tgz
  [ $# -eq 1 ] && [ -f "$1" ] || die "npm pack did not produce exactly one tarball; nothing published"
  mv "$1" "$OUT/$PIWEB_TGZ"
  [ "$(tgz_version "$OUT/$PIWEB_TGZ")" = "${TAG#v}" ] || die "the packed pi-web package does not carry version ${TAG#v}; nothing published"
fi

# release notes (never committed; only a release still to create needs them):
# the committed docs/release-notes/<tag>.md as it is, or notes generated from
# the log, then the "Test gate" record - both staged, the worktree untouched.
NOTES="docs/release-notes/$TAG.md"
if [ -z "$REL_EXISTS" ]; then
  if [ -s "$NOTES" ]; then
    cp "$NOTES" "$WORK/notes.md"
  else
    {
      echo "# $TAG"
      echo
      if [ -n "$PREVTAG" ] && git cat-file -e "$PREVCOMMIT^{commit}" 2>/dev/null; then
        echo "Changes since $PREVTAG:"; echo
        git log "$PREVCOMMIT..HEAD" --oneline | sed 's/^/- /'
      elif [ -n "$PREVTAG" ]; then
        echo "Changes since $PREVTAG:"; echo
        git log --oneline -n 50 | sed 's/^/- /'
      else
        echo "Initial release."
      fi
    } >"$WORK/notes.md"
  fi
  printf '\n## Test gate\n\n%s\n' "$GATE_RECORD" >>"$WORK/notes.md"
  NOTES="$WORK/notes.md"
fi

# 4. the atomic claim: tag push, no force. A resume claimed $TAG before.
if [ -z "$RESUME" ]; then
  git tag -a "$TAG" -m "release $TAG" "$COMMIT"
  if ! git push "$REMOTE" "refs/tags/$TAG"; then
    git tag -d "$TAG" >/dev/null 2>&1 || true
    die "pushing tag $TAG to $REMOTE failed (already taken?); nothing published"
  fi
  echo "release: tag $TAG pushed to $REMOTE - version claimed"
else
  echo "release: resume $TAG: the tag is already on $REMOTE at $COMMIT - not pushed again"
fi

POSTFAIL() {
  cat >&2 <<MSG

##########################################################################
release: FAILED AFTER $TAG WAS CLAIMED ($TAG at $COMMIT; failing step: $1).
Nothing has been deleted, moved or rolled back. The version is claimed, NOT
burned - resume it:
  - fix the cause, then run this script again from the same commit
    (origin/$BRANCH still at $COMMIT, clean worktree). It finds $TAG on
    $COMMIT and resumes it: what is already published (the image, the
    GitHub release and its assets) is checked and kept, only what is missing
    is published; no second version is cut. A release workflow work item
    filed again does the same once the work machine's daemon carries the
    resuming release step (the next daemon install); until then a re-filed
    item still ends PF-RELEASE-ALREADY_RELEASED - run this script by hand;
  - tag $TAG is pushed and immutable: never delete, move or re-point it;
  - if $BRANCH moves past $COMMIT before that, this script can no longer
    resume $TAG (it releases the new head as the next version and $TAG stays
    incomplete): finish $TAG by hand and audit it with release.sh --verify $TAG;
  - local files that survive: artifacts/release-$TAG/pf and the two darwin
    binaries artifacts/release-$TAG/pf-darwin-{arm64,amd64} (the built
    binaries) and the local docker image
    $IMAGE:$TAG. The staging directory (checksums, aihub-image.digest, the
    pi-web tarball, release-manifest.json, notes) is removed when this
    script exits.
##########################################################################
MSG
  exit 1
}

# 5. docker push, digest after push, checksums.
if [ -n "$NEED_IMAGE" ]; then
  resume_recheck "docker push"
  docker push "$IMAGE:$TAG" || POSTFAIL "docker push"
  DIGEST=$(docker inspect --format '{{range .RepoDigests}}{{println .}}{{end}}' "$IMAGE:$TAG" | grep "^$IMAGE@" | head -1 | sed 's/^.*@//') || DIGEST=""
  printf '%s' "$DIGEST" | grep -Eq '^sha256:[0-9a-f]{64}$' || POSTFAIL "reading the pushed image digest (got \"$DIGEST\")"
  PUSHED_BY_RUN="the image $IMAGE:$TAG ($DIGEST)"; REG_SNAP="present $DIGEST"
fi
[ -f "$OUT/aihub-image.digest" ] || printf '%s\n' "$DIGEST" >"$OUT/aihub-image.digest"
if has_asset SHA256SUMS; then
  cp "$WORK/dl/SHA256SUMS" "$OUT/SHA256SUMS"
else
  ( cd "$OUT" && sha256sum pf-linux-amd64 pf-darwin-arm64 pf-darwin-amd64 \
    aihub-image.digest "$PIWEB_TGZ" >SHA256SUMS ) || POSTFAIL "SHA256SUMS"
fi
manifest_of "$TAG" "$COMMIT" "$OUT/SHA256SUMS" >"$OUT/release-manifest.json" || POSTFAIL "release-manifest.json"
if has_asset release-manifest.json && ! cmp -s "$WORK/dl/release-manifest.json" "$OUT/release-manifest.json"; then
  POSTFAIL "the published release-manifest.json of $TAG is not the one its tag, commit and SHA256SUMS derive (a human decides)"
fi

# 6. GitHub release: the 5 payload assets (3 pf binaries + digest + pi-web
# tarball) + SHA256SUMS + release-manifest.json. A resume whose release
# exists uploads only the missing assets (never --clobber) and publishes a
# draft.
resume_recheck "the GitHub release update"
if [ -z "$REL_EXISTS" ]; then
  URL=$(gh_ release create "$TAG" --verify-tag --title "$TAG" --notes-file "$NOTES" \
    "$OUT/pf-linux-amd64" "$OUT/pf-darwin-arm64" "$OUT/pf-darwin-amd64" \
    "$OUT/aihub-image.digest" "$OUT/$PIWEB_TGZ" "$OUT/SHA256SUMS" \
    "$OUT/release-manifest.json") || POSTFAIL "gh release create"
else
  set --
  for _a in $ASSETS; do has_asset "$_a" || set -- "$@" "$OUT/$_a"; done
  if [ $# -gt 0 ]; then
    gh_ release upload "$TAG" "$@" || POSTFAIL "gh release upload"
    echo "release: resume $TAG: uploaded the missing asset(s):${MISSING_ASSETS}"
  fi
  if [ "$REL_DRAFT" = true ]; then
    gh_ release edit "$TAG" --draft=false >/dev/null || POSTFAIL "gh release edit $TAG --draft=false"
    echo "release: resume $TAG: the draft release is published"
  fi
  URL="https://github.com/$GH_REPO/releases/tag/$TAG"
fi

echo "version=$TAG"
echo "commit=$COMMIT"
echo "notes=$URL"
