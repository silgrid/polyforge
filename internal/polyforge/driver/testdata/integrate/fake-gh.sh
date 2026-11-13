#!/bin/sh
# fake gh for the integration lane's tests: PRs kept as
# files under $FAKE_GH_STATE/<repo>/prs/<n>/ (head, base, state, url,
# merge); `pr merge --merge --match-head-commit <sha>` is a real --no-ff
# merge into the bare origin $FAKE_GH_ORIGINS/<repo>.git, refused unless
# the branch's head is <sha>. GitHub is never contacted.
echo "gh $*" >> "$FAKE_GH_STATE/calls.log"
[ "$1" = pr ] || { echo "fake gh: unsupported: $*" >&2; exit 2; }
sub=$2; shift 2
target=""; head=""; base=main; method=""; match=""
while [ $# -gt 0 ]; do
  case "$1" in
    --head) head=$2; shift 2 ;;
    --match-head-commit) match=$2; shift 2 ;;
    --state) shift 2 ;;
    --base) base=$2; shift 2 ;;
    --json|-q|--jq|--title|--body|--body-file) shift 2 ;;
    --merge|--squash|--rebase) method=$1; shift ;;
    -*) shift ;;
    *) target=$1; shift ;;
  esac
done
url_repo() { echo "$1" | sed -E 's#.*/fake/([^/]+)/pull/[0-9]+#\1#'; }
case "$sub" in
  create)
    origin=$(git remote get-url origin) || exit 2
    repo=$(basename "$origin" .git)
    S="$FAKE_GH_STATE/$repo/prs"; mkdir -p "$S"
    n=$(( $(ls "$S" | wc -l) + 1 )); d="$S/$n"; mkdir -p "$d"
    echo "$head" > "$d/head"; echo "$base" > "$d/base"; echo OPEN > "$d/state"
    # a test's hook: the next created PR's merge fails once
    [ -e "$FAKE_GH_STATE/fail-created-merge" ] && mv "$FAKE_GH_STATE/fail-created-merge" "$d/fail-merge"
    echo "https://github.com/fake/$repo/pull/$n" > "$d/url"; cat "$d/url" ;;
  list)
    origin=$(git remote get-url origin) || exit 2
    repo=$(basename "$origin" .git)
    S="$FAKE_GH_STATE/$repo/prs"; out=""
    for n in $(ls "$S" 2>/dev/null | sort -rn); do
      d="$S/$n"; [ "$(cat "$d/head")" = "$head" ] || continue
      mc=null; [ -s "$d/merge" ] && mc="{\"oid\":\"$(cat "$d/merge")\"}"
      oid=$(git --git-dir="$FAKE_GH_ORIGINS/$repo.git" rev-parse "refs/heads/$head" 2>/dev/null)
      item=$(printf '{"url":"%s","state":"%s","headRefOid":"%s","mergeCommit":%s}' "$(cat "$d/url")" "$(cat "$d/state")" "$oid" "$mc")
      out="${out:+$out,}$item"
    done
    echo "[$out]" ;;
  view)
    repo=$(url_repo "$target"); d="$FAKE_GH_STATE/$repo/prs/${target##*/}"
    [ -d "$d" ] || { echo "fake gh: no such PR $target" >&2; exit 1; }
    mc=null; [ -s "$d/merge" ] && mc="{\"oid\":\"$(cat "$d/merge")\"}"
    printf '{"state":"%s","headRefName":"%s","baseRefName":"%s","mergeCommit":%s}\n' "$(cat "$d/state")" "$(cat "$d/head")" "$(cat "$d/base")" "$mc" ;;
  merge)
    repo=$(url_repo "$target"); d="$FAKE_GH_STATE/$repo/prs/${target##*/}"
    [ "$method" = --merge ] || { echo "fake gh: refusing merge method '$method'" >&2; exit 1; }
    [ "$(cat "$d/state")" = OPEN ] || { echo "fake gh: PR not open" >&2; exit 1; }
    # a test's hook: this merge fails once (the API refused, the network died)
    [ -e "$d/fail-merge" ] && { rm -f "$d/fail-merge"; echo "fake gh: merge failed (scripted)" >&2; exit 1; }
    [ -n "$match" ] || { echo "fake gh: merge without --match-head-commit" >&2; exit 1; }
    cur=$(git --git-dir="$FAKE_GH_ORIGINS/$repo.git" rev-parse "refs/heads/$(cat "$d/head")") || exit 1
    [ "$cur" = "$match" ] || { echo "fake gh: head $cur is not the expected $match" >&2; exit 1; }
    w=$(mktemp -d)
    ( git clone -q "$FAKE_GH_ORIGINS/$repo.git" "$w/c" && cd "$w/c" && git checkout -q "$(cat "$d/base")" &&
      git -c user.name=gh -c user.email=gh@invalid merge -q --no-ff -m "Merge pull request #${target##*/} from $(cat "$d/head")" "origin/$(cat "$d/head")" &&
      git push -q origin "HEAD:refs/heads/$(cat "$d/base")" && git rev-parse HEAD > "$d/merge" ) || { rm -rf "$w"; exit 1; }
    rm -rf "$w"; echo MERGED > "$d/state"
    # a test's hook: what happens on the default branch right after this
    # merge (a human pushing to main)
    [ -x "$d/after-merge" ] && "$d/after-merge" "$FAKE_GH_ORIGINS/$repo.git"
    exit 0 ;;
  *) echo "fake gh: unsupported: pr $sub" >&2; exit 2 ;;
esac
