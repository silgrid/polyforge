#!/bin/sh
# fake gh for the merge-pr action's tests: PRs kept as files under
# $FAKE_GH_STATE/<repo>/prs/<n>/ (head, base, state, merge); `pr view`
# answers them, `pr merge --merge --match-head-commit <sha>` is a real
# --no-ff merge into the bare origin $FAKE_GH_ORIGINS/<repo>.git, refused
# unless the branch's head is <sha> or the merge conflicts. Hooks: a
# `fail-merge` file fails the next merge once; an executable `before-merge`
# runs before it (another merge landing first), `after-merge` after it, and
# `before-view` once before the next view (the origin and the PR dir as $1
# and $2).
# GitHub is never contacted.
echo "gh $*" >> "$FAKE_GH_STATE/calls.log"
[ "$1" = pr ] || { echo "fake gh: unsupported: $*" >&2; exit 2; }
sub=$2; shift 2
target=""; method=""; match=""
while [ $# -gt 0 ]; do
  case "$1" in
    --match-head-commit) match=$2; shift 2 ;;
    --json|-q|--jq) shift 2 ;;
    --merge|--squash|--rebase) method=$1; shift ;;
    -*) shift ;;
    *) target=$1; shift ;;
  esac
done
repo=$(echo "$target" | sed -E 's#.*/fake/([^/]+)/pull/[0-9]+#\1#')
d="$FAKE_GH_STATE/$repo/prs/${target##*/}"
[ -d "$d" ] || { echo "fake gh: no such PR $target" >&2; exit 1; }
case "$sub" in
  view)
    [ -x "$d/before-view" ] && { "$d/before-view" "$FAKE_GH_ORIGINS/$repo.git" "$d" >/dev/null; rm -f "$d/before-view"; }
    mc=null; [ -s "$d/merge" ] && mc="{\"oid\":\"$(cat "$d/merge")\"}"
    printf '{"state":"%s","headRefName":"%s","baseRefName":"%s","mergeCommit":%s}\n' "$(cat "$d/state")" "$(cat "$d/head")" "$(cat "$d/base")" "$mc" ;;
  merge)
    [ "$method" = --merge ] || { echo "fake gh: refusing merge method '$method'" >&2; exit 1; }
    [ "$(cat "$d/state")" = OPEN ] || { echo "fake gh: PR not open" >&2; exit 1; }
    [ -x "$d/before-merge" ] && { "$d/before-merge" "$FAKE_GH_ORIGINS/$repo.git"; rm -f "$d/before-merge"; }
    [ -e "$d/fail-merge" ] && { rm -f "$d/fail-merge"; echo "fake gh: Pull request is not mergeable (scripted)" >&2; exit 1; }
    [ -n "$match" ] || { echo "fake gh: merge without --match-head-commit" >&2; exit 1; }
    cur=$(git --git-dir="$FAKE_GH_ORIGINS/$repo.git" rev-parse "refs/heads/$(cat "$d/head")") || exit 1
    [ "$cur" = "$match" ] || { echo "fake gh: head $cur is not the expected $match" >&2; exit 1; }
    w=$(mktemp -d)
    ( git clone -q "$FAKE_GH_ORIGINS/$repo.git" "$w/c" && cd "$w/c" && git checkout -q "$(cat "$d/base")" &&
      git -c user.name=gh -c user.email=gh@invalid merge -q --no-ff -m "Merge pull request #${target##*/} from $(cat "$d/head")" "origin/$(cat "$d/head")" &&
      git push -q origin "HEAD:refs/heads/$(cat "$d/base")" && git rev-parse HEAD > "$d/merge" ) || { rm -rf "$w"; echo "fake gh: Pull request is not mergeable" >&2; exit 1; }
    rm -rf "$w"; echo MERGED > "$d/state"
    [ -x "$d/after-merge" ] && "$d/after-merge" "$FAKE_GH_ORIGINS/$repo.git"
    exit 0 ;;
  *) echo "fake gh: unsupported: pr $sub" >&2; exit 2 ;;
esac
