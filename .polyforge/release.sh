#!/bin/sh
# polyforge's own release procedure.
#
# Usage:
#   sh .polyforge/release.sh [major|minor|patch]     release (default: minor)
#   sh .polyforge/release.sh --verify <tag>          read-only audit of a release
#   sh .polyforge/release.sh --help
#
# Version scheme: vX.Y.Z semver. The floor is the highest semver among the tags
# on ORIGIN (git ls-remote; local tags are ignored, and a failing ls-remote is
# fatal - there is no silent fallback); the bump is applied to it.
#
# Release, in order (nothing is published before the gate passes):
#   1. refuse a dirty worktree; fetch origin; refuse unless HEAD == origin/main;
#      refuse if HEAD already carries a release tag on origin (a second release
#      of the same commit: redeploy the existing version instead); compute the
#      version from the origin tags; refuse unless every commit that entered
#      main since the previous release tag came through a merged PR or is an
#      allowlisted owner-direct commit (the PR-only check below);
#   2. design-doc cross-reference gate (docs/design/polyforge-vnext/tools/
#      check-xrefs.py, the Makefile's `xrefs` check; python3 is REQUIRED here -
#      unlike `make xrefs`, a release never skips it), then the test gate
#      (GOWORK=off make test, includes the long e2e);
#   3. build the binaries (GOWORK=off make release VERSION=<tag>) -> the
#      six-binary matrix aihub/pf × linux-amd64, darwin-arm64,
#      darwin-amd64 (polyforge#218); build the image from the LINUX aihub
#      (aihub stays linux/amd64-only) tagged ghcr.io/silgrid/aihub:<tag>;
#   4. git tag <tag> + git push origin <tag> (NO force): the atomic claim of the
#      version. If the tag exists on origin the run stops, nothing published;
#   5. docker push; write aihub-image.digest (the bare image digest
#      sha256:<64 hex>, known only after the push; the canonical format that
#      deploy.sh consumes); SHA256SUMS over the six binaries and the digest file
#      (7 lines);
#   6. gh release create <tag> with the 8 assets (6 binaries + digest + SUMS),
#      notes from docs/release-notes/<tag>.md (generated from git log when
#      absent).
# Last lines on stdout: version=<tag>, commit=<sha>, notes=<release url>.
#
# --verify <tag> (no mutation, no tests, no build): tag format; strict
# monotonicity (the tag must be strictly greater than EVERY other release tag on
# origin); the PR-only check over previous-tag..tag, owner-direct allowlist
# included (needs the commits locally: git fetch origin first); release exists
# with EXACTLY the 8 assets;
# sha256sum -c SHA256SUMS on the downloaded assets; aihub-image.digest is
# exactly sha256:<64 hex> and the registry digest of ghcr.io/silgrid/aihub:<tag>
# equals it. Exit 0 iff all pass.
#
# PR-only check (polyforge#163): main's FIRST-PARENT history from the previous
# release tag to the released commit must consist only of merge commits that
# GitHub records as the merge commit of a merged PR
# (gh api repos/<repo>/commits/<sha>/pulls: a merged PR whose merge_commit_sha
# is that commit). Two halves, because each alone is forgeable: a merge commit
# is just a commit with two parents (anyone can `git merge` locally and push),
# and a PR association alone would also accept squash/rebase merges, which
# break the "first parent = one PR merge" shape that `gh pr merge --merge` (the
# workflows' deliver step) produces. The PR content itself is the second-parent
# side of each merge and is not walked. A gh API failure is a refusal, never a
# pass. A PR merged by hand (a local git merge pushed with the pre-push hook's
# PF_ALLOW_MAIN_PUSH override) passes only if GitHub records that very commit as
# the PR's merge commit; otherwise it is refused - merge with gh pr merge --merge.
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
# Environment overrides (mainly for the fake-env tests):
#   RELEASE_XREFS_CMD   xrefs gate command        (default: python3 -I
#                       docs/design/polyforge-vnext/tools/check-xrefs.py)
#   RELEASE_TEST_CMD    test gate command         (default: GOWORK=off make test)
#   RELEASE_BUILD_CMD   build command; sees TAG; must leave
#                       artifacts/release-$TAG/aihub, .../pf and the four
#                       darwin binaries .../aihub-darwin-arm64, .../pf-darwin-arm64,
#                       .../aihub-darwin-amd64, .../pf-darwin-amd64
#                                                 (default: GOWORK=off make release VERSION=$TAG)
#   RELEASE_IMAGE       image repository          (default: ghcr.io/silgrid/aihub)
#   RELEASE_GH_REPO     gh -R repo                (default: silgrid/polyforge)
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
# Owner-direct allowlist (polyforge#240; see the PR-only check above): author
# emails, space-separated, whose direct commits pass the PR-only check. One-time
# legacy bridge - delete once every change goes through PR-review-merge.
OWNER_DIRECT_EMAILS="chenlinbo@modelbest.cn"

die() { echo "release: $*" >&2; exit 1; }
gh_() { gh "$@" -R "$GH_REPO"; }

usage() {
  cat <<'USAGE'
usage: release.sh [major|minor|patch]     cut a release (default: minor)
       release.sh --verify <tag>          read-only verification of a published release
       release.sh --help
USAGE
}

# ls_tags: raw `git ls-remote --tags` output of the remote ("<sha> <ref>" lines,
# annotated tags also as <ref>^{}); returns non-zero when ls-remote fails.
ls_tags() {
  git ls-remote --tags "$REMOTE" 2>/dev/null
}
# tags_of <ls-remote output>: semver tags, one per line (peeled refs folded).
tags_of() {
  printf '%s\n' "$1" | sed -n 's|^[0-9a-f]*[[:space:]]*refs/tags/||p' | sed 's/\^{}$//' | grep -E "$SEMVER_RE" | sort -Vu || true
}
# tags_at <ls-remote output> <commit>: semver tags whose commit (peeled for
# annotated tags, direct for lightweight ones) is <commit>.
tags_at() {
  printf '%s\n' "$1" | awk -v c="$2" '$1 == c {print $2}' | sed -n 's|^refs/tags/||p' | sed 's/\^{}$//' | grep -E "$SEMVER_RE" | sort -Vu || true
}

# tag_commit <ls-remote output> <tag>: the commit <tag> points at (peeled for
# an annotated tag, direct for a lightweight one); empty when absent.
tag_commit() {
  _p=$(printf '%s\n' "$1" | awk -v r="refs/tags/$2^{}" '$2 == r {print $1; exit}')
  [ -n "$_p" ] || _p=$(printf '%s\n' "$1" | awk -v r="refs/tags/$2" '$2 == r {print $1; exit}')
  printf '%s' "$_p"
}
# non_pr_commits <from> <to>: prints one "<sha> (<why>)" line per first-parent
# commit in <from>..<to> that is not the merge commit of a merged PR (see the
# PR-only check above), and one "owner-direct: <sha> <email>" line per commit
# whose author email is in OWNER_DIRECT_EMAILS; prints nothing when all of them
# are PR merges. Returns non-zero when the range cannot be walked.
non_pr_commits() {
  _list=$(git rev-list --first-parent "$1..$2") || return 1
  for _c in $_list; do
    _ae=$(git log -1 --format=%ae "$_c") || return 1
    case " $OWNER_DIRECT_EMAILS " in *" $_ae "*) echo "owner-direct: $_c $_ae"; continue ;; esac
    _n=$(git rev-list --parents -n 1 "$_c" | wc -w)
    if [ "$_n" -lt 3 ]; then echo "$_c (not a merge commit)"; continue; fi
    if ! _pr=$(gh api "repos/$GH_REPO/commits/$_c/pulls" --jq ".[] | select(.merged_at != null and .merge_commit_sha == \"$_c\") | .number" 2>/dev/null); then
      echo "$_c (gh api failed: cannot confirm a merged PR)"; continue
    fi
    [ -n "$_pr" ] || echo "$_c (no merged PR has it as its merge commit)"
  done
}
# pr_only_check <prev tag> <prev commit> <commit>: one "owner-direct: <sha>
# <email>" line per allowlisted commit in prev..commit, then the refusal reason
# as the last line unless every other first-parent commit came through a merged
# PR. Callers split the two with owner_direct_of / pr_only_why_of.
pr_only_check() {
  git cat-file -e "$2^{commit}" 2>/dev/null || { echo "previous release tag $1 ($2) is not available locally (git fetch $REMOTE first)"; return; }
  git cat-file -e "$3^{commit}" 2>/dev/null || { echo "commit $3 is not available locally (git fetch $REMOTE first)"; return; }
  git merge-base --is-ancestor "$2" "$3" || { echo "previous release tag $1 ($2) is not an ancestor of $3: cannot tell what entered $BRANCH"; return; }
  _all=$(non_pr_commits "$2" "$3") || { echo "cannot walk $1..$3"; return; }
  owner_direct_of "$_all"
  _bad=$(pr_only_why_of "$_all")
  [ -z "$_bad" ] || echo "commits entered $BRANCH since $1 without a merged PR: $(printf '%s\n' "$_bad" | tr '\n' ';' | sed 's/;$//; s/;/; /g')"
}
owner_direct_of() { printf '%s\n' "$1" | grep '^owner-direct: ' || true; }
pr_only_why_of() { printf '%s\n' "$1" | grep -v '^owner-direct: ' | grep . || true; }

case "${1:-}" in
  -h|--help) usage; echo; echo "--verify <tag> checks an already published release without changing anything."; exit 0 ;;
esac

# ---------------------------------------------------------------- verify mode
if [ "${1:-}" = "--verify" ]; then
  VTAG="${2:-}"
  [ -n "$VTAG" ] || { usage >&2; exit 2; }
  [ $# -eq 2 ] || { usage >&2; exit 2; }
  FAILS=0
  pass() { echo "PASS $*"; }
  fail() { echo "FAIL $*"; FAILS=$((FAILS + 1)); }

  # 1. tag format + strict monotonicity against the previous origin tag.
  if printf '%s\n' "$VTAG" | grep -Eq "$SEMVER_RE"; then
    pass "tag format: $VTAG"
    if LS=$(ls_tags); then RT=$(tags_of "$LS"); else RT=""; fail "git ls-remote --tags $REMOTE failed: cannot check the tag ordering"; fi
    if printf '%s\n' "$RT" | grep -qx "$VTAG"; then
      pass "tag exists on $REMOTE"
    else
      fail "tag $VTAG not found on $REMOTE"
    fi
    key() { printf '%s\n' "$1" | sed 's/^v//' | awk -F. '{printf "%012d%012d%012d", $1, $2, $3}'; }
    gt() { [ "$1" != "$2" ] && [ "$(printf '%s\n%s\n' "$1" "$2" | LC_ALL=C sort | tail -n1)" = "$1" ]; }
    MYKEY=$(key "$VTAG")
    # $VTAG must be strictly greater than every OTHER tag on origin: a tag with a
    # key >= ours (a newer release, or a spelling like v1.02.0 of the same key)
    # means $VTAG is not the latest release.
    NOTGT=""; OTHERS=0; HIGH=""
    for t in $(printf '%s\n' "$RT" | grep -vx "$VTAG" || true); do
      OTHERS=$((OTHERS + 1))
      k=$(key "$t")
      if [ "$k" = "$MYKEY" ] || gt "$k" "$MYKEY"; then NOTGT="$NOTGT $t"; else
        if [ -z "$HIGH" ] || gt "$k" "$(key "$HIGH")"; then HIGH=$t; fi
      fi
    done
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
      PRO=$(pr_only_check "$HIGH" "$(tag_commit "$LS" "$HIGH")" "$VC")
      owner_direct_of "$PRO" | while IFS= read -r l; do pass "pr-only: $l"; done
      WHY=$(pr_only_why_of "$PRO")
      if [ -z "$WHY" ]; then
        pass "pr-only: every commit on $BRANCH since $HIGH came through a merged PR or is owner-direct"
      else
        fail "pr-only: $WHY"
      fi
    fi
  else
    fail "tag format: \"$VTAG\" does not match $SEMVER_RE"
  fi

  VWORK=$(mktemp -d "${TMPDIR:-/tmp}/release-verify.XXXXXX")
  trap 'rm -rf "$VWORK"' EXIT INT TERM

  # 2. release exists with exactly the 8 assets.
  WANT="SHA256SUMS
aihub-darwin-amd64
aihub-darwin-arm64
aihub-image.digest
aihub-linux-amd64
pf-darwin-amd64
pf-darwin-arm64
pf-linux-amd64"
  if gh_ release view "$VTAG" --json assets,body -q '.assets[].name' >"$VWORK/assets.raw" 2>"$VWORK/view.err"; then
    pass "release $VTAG exists"
    sort "$VWORK/assets.raw" >"$VWORK/assets.txt"
    if [ "$(cat "$VWORK/assets.txt")" = "$WANT" ]; then
      pass "exactly the 8 assets: $(tr '\n' ' ' <"$VWORK/assets.txt")"
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
BUMP="${1:-minor}"
case "$BUMP" in
  major|minor|patch) ;;
  *) usage >&2; die "bump must be major, minor or patch (got \"$BUMP\")" ;;
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
# A commit that already carries a release tag on origin was released: a second
# release would cut a duplicate version of the same commit.
HEADTAGS=$(tags_at "$LS" "$COMMIT" | tr '\n' ' ')
if [ -n "${HEADTAGS%% }" ]; then
  die "HEAD ($COMMIT) is already released as ${HEADTAGS%% } on $REMOTE; nothing published. Do not cut a second version of the same commit: to deploy it (again), run .polyforge/deploy.sh ${HEADTAGS%% *} --confirm ${HEADTAGS%% *}"
fi

# version = bump(max semver of the ORIGIN tags); local tags are ignored.
FLOOR=$(tags_of "$LS" | tail -1)
[ -n "$FLOOR" ] || die "no vX.Y.Z tag on $REMOTE - refusing to invent a base version"
_v=${FLOOR#v}; _maj=${_v%%.*}; _r=${_v#*.}; _min=${_r%%.*}; _pat=${_r#*.}
case "$BUMP" in
  minor) _min=$((_min + 1)); _pat=0 ;;
  patch) _pat=$((_pat + 1)) ;;
  major) _maj=$((_maj + 1)); _min=0; _pat=0 ;;
esac
TAG="v${_maj}.${_min}.${_pat}"
echo "release: floor $FLOOR -> version $TAG ($BUMP bump), commit $COMMIT"
PREVTAG="$FLOOR"
if printf '%s\n' "$LS" | grep -q "[[:space:]]refs/tags/$TAG\(\^{}\)\{0,1\}\$"; then
  die "tag $TAG already exists on $REMOTE; nothing published"
fi

# 1c. PR-only: every commit that entered the branch since the previous release
# came through a merged PR or is owner-direct (audited with a PASS line).
PRO=$(pr_only_check "$PREVTAG" "$(tag_commit "$LS" "$PREVTAG")" "$COMMIT")
owner_direct_of "$PRO" | while IFS= read -r l; do echo "PASS pr-only: $l"; done
WHY=$(pr_only_why_of "$PRO")
[ -z "$WHY" ] || die "refusing to release: $WHY; nothing built or published"

# 2. xrefs gate, then the test gate.
echo "release: running the design-doc cross-reference check"
XREFS="docs/design/polyforge-vnext/tools/check-xrefs.py"
if [ -n "${RELEASE_XREFS_CMD:-}" ]; then
  sh -c "$RELEASE_XREFS_CMD" || die "design-doc cross-reference check failed (RELEASE_XREFS_CMD); fix the references (make xrefs) and release again; nothing built or published"
else
  command -v python3 >/dev/null 2>&1 || die "python3 not found: the release gate runs $XREFS and does not skip it; nothing built or published"
  python3 -I "$XREFS" || die "design-doc cross-reference check failed ($XREFS); fix the references (make xrefs) and release again; nothing built or published"
fi
echo "release: running test gate"
if [ -n "${RELEASE_TEST_CMD:-}" ]; then sh -c "$RELEASE_TEST_CMD"; else GOWORK=off make test; fi

WORK=$(mktemp -d "${TMPDIR:-/tmp}/release.XXXXXX")
trap 'rm -rf "$WORK"' EXIT INT TERM
OUT="$WORK/out"; mkdir "$OUT"

# build binaries: the full matrix (linux-amd64 pair consumed by the
# image build and the deploy path, the darwin pairs shipped as assets).
export TAG
if [ -n "${RELEASE_BUILD_CMD:-}" ]; then sh -c "$RELEASE_BUILD_CMD"; else GOWORK=off make release VERSION="$TAG"; fi
ART="artifacts/release-$TAG"
MISSING=""
for n in aihub pf aihub-darwin-arm64 pf-darwin-arm64 aihub-darwin-amd64 pf-darwin-amd64; do
  [ -f "$ART/$n" ] || MISSING="$MISSING $ART/$n"
done
[ -z "$MISSING" ] || die "build did not produce the 6-binary matrix, missing:$MISSING"
cp "$ART/aihub" "$OUT/aihub-linux-amd64"
cp "$ART/pf" "$OUT/pf-linux-amd64"
for a in darwin-arm64 darwin-amd64; do
  cp "$ART/aihub-$a" "$OUT/aihub-$a"
  cp "$ART/pf-$a" "$OUT/pf-$a"
done

# build image (context: temp dir with the binary as `aihub`).
mkdir "$WORK/ctx"
cp "$ART/aihub" "$WORK/ctx/aihub"
docker build -f deploy/Dockerfile -t "$IMAGE:$TAG" "$WORK/ctx"

# release notes (never committed).
NOTES="docs/release-notes/$TAG.md"
if [ ! -s "$NOTES" ]; then
  NOTES="$WORK/notes.md"
  {
    echo "# $TAG"
    echo
    if [ -n "$PREVTAG" ] && git rev-parse -q --verify "refs/tags/$PREVTAG" >/dev/null 2>&1; then
      echo "Changes since $PREVTAG:"; echo
      git log "$PREVTAG..HEAD" --oneline | sed 's/^/- /'
    elif [ -n "$PREVTAG" ]; then
      echo "Changes since $PREVTAG:"; echo
      git log --oneline -n 50 | sed 's/^/- /'
    else
      echo "Initial release."
    fi
  } >"$NOTES"
fi

# login state for ghcr.
DCFG="${DOCKER_CONFIG:-$HOME/.docker}/config.json"
if ! { [ -f "$DCFG" ] && grep -q 'ghcr\.io' "$DCFG"; }; then
  echo "release: not logged in to ghcr.io; logging in via gh"
  gh auth token | docker login ghcr.io -u "$(gh api user -q .login)" --password-stdin
fi

# 4. the atomic claim: tag push, no force.
git tag -a "$TAG" -m "release $TAG" "$COMMIT"
if ! git push "$REMOTE" "refs/tags/$TAG"; then
  git tag -d "$TAG" >/dev/null 2>&1 || true
  die "pushing tag $TAG to $REMOTE failed (already taken?); nothing published"
fi
echo "release: tag $TAG pushed to $REMOTE - version claimed"

POSTFAIL() {
  cat >&2 <<MSG

##########################################################################
release: FAILED AFTER THE TAG WAS PUSHED ($TAG at $COMMIT).
Nothing has been deleted or rolled back automatically. Decide by hand:
  - tag $TAG is on $REMOTE; the version is burnt, do not reuse it for other code;
  - tag $TAG is pushed and immutable (never delete or move it): this version
    is BURNED. Do not retry it; cut the next version with a new release run
    (note: a new run refuses to release a commit that already carries a tag, so
    the next version needs a new commit on $BRANCH);
  - the image $IMAGE:$TAG and/or the GitHub release may be missing or partial;
  - local files that survive: artifacts/release-$TAG/aihub and
    artifacts/release-$TAG/pf and the four darwin binaries
    artifacts/release-$TAG/{aihub,pf}-darwin-{arm64,amd64} (the built
    binaries) and the local docker image
    $IMAGE:$TAG. The staging directory (checksums, aihub-image.digest, notes)
    is removed when this script exits;
  - finishing $TAG by hand (docker push, a digest file, SHA256SUMS, gh release
    create) is possible but must be audited with release.sh --verify $TAG.
  failing step: $1
##########################################################################
MSG
  exit 1
}

# 5. docker push, digest after push, checksums.
docker push "$IMAGE:$TAG" || POSTFAIL "docker push"
DIGEST=$(docker inspect --format '{{range .RepoDigests}}{{println .}}{{end}}' "$IMAGE:$TAG" | grep "^$IMAGE@" | head -1 | sed 's/^.*@//') || DIGEST=""
printf '%s' "$DIGEST" | grep -Eq '^sha256:[0-9a-f]{64}$' || POSTFAIL "reading the pushed image digest (got \"$DIGEST\")"
printf '%s\n' "$DIGEST" >"$OUT/aihub-image.digest"
( cd "$OUT" && sha256sum aihub-linux-amd64 pf-linux-amd64 \
  aihub-darwin-arm64 pf-darwin-arm64 aihub-darwin-amd64 pf-darwin-amd64 \
  aihub-image.digest >SHA256SUMS ) || POSTFAIL "SHA256SUMS"

# 6. GitHub release: the 8-asset matrix (6 binaries + digest + SUMS).
URL=$(gh_ release create "$TAG" --verify-tag --title "$TAG" --notes-file "$NOTES" \
  "$OUT/aihub-linux-amd64" "$OUT/pf-linux-amd64" \
  "$OUT/aihub-darwin-arm64" "$OUT/pf-darwin-arm64" \
  "$OUT/aihub-darwin-amd64" "$OUT/pf-darwin-amd64" \
  "$OUT/aihub-image.digest" "$OUT/SHA256SUMS") || POSTFAIL "gh release create"

echo "version=$TAG"
echo "commit=$COMMIT"
echo "notes=$URL"
