#!/usr/bin/env bash
# w97_invariant_scenarios.sh — refusal cases for the extended identifier
# invariant (the w97 ticket, carrying the earlier review's blocking
# findings 1 and 2).
#
# Usage: w97_invariant_scenarios.sh <repo-root> <case>
#   cases: content | path | message
#
# Each case builds a throwaway internal canon (the working tree's
# tools/sync/*.sh and docs/sync/desensitize.md, plus the real files the
# OVERRIDE/ALLOWED rows name, plus ONE residual fixture) and a throwaway
# public clone whose main is a projection boundary; `gh` is a PATH-stubbed
# read-only fake (no network). It then drives tools/sync/project.sh through
# its CLI and asserts the projection REFUSES: nonzero exit, the words
# "identifier invariant violated", the offending file/path named, no
# projection/<tag> branch created, the public clone's main untouched.
#
# The residual token is assembled at run time (a leading letter + the two
# letter shorthand + the number) on purpose: this file lives in the tree a
# real-tree projection walks, so a literal letter-prefixed shorthand here
# would be an invariant hit itself. Assembled, this file is clean for the
# REWRITE/RENAME rows and for the invariant, and needs no EXCLUDE row.
set -euo pipefail

repo=$1
want=$2
slug=silgrid/polyforge
work=$(mktemp -d "${TMPDIR:-/tmp}/w97-inv-XXXXXX")
trap 'rm -rf "$work"' EXIT

fail() { echo "w97 [$want]: FAIL: $*" >&2; exit 1; }

# residual: an extra leading letter glued to the two-letter shorthand and
# its number — the REWRITE left-boundary rule refuses to touch it by design,
# so it is the shape that must reach the invariant instead of washing out.
residual() { # residual <number>
	local half=wb
	printf 'x%s%s' "$half" "$1"
}

# init_public <dir>: a public-repo fixture whose main is one projection
# commit (marker trailer, no PRs) — a monitor-clean base.
init_public() {
	local d=$1
	git init -q -b main "$d"
	git -C "$d" config user.name 'Pub Owner'
	git -C "$d" config user.email 'pub@example.net'
	echo 'public base' >"$d/file.txt"
	git -C "$d" add file.txt
	git -C "$d" commit -qm 'projection sync base

Sync-Projection: 0'
}

# init_internal <dir>: the fixture canon — the working tree's sync tools and
# delta list, plus every path the OVERRIDE/ALLOWED rows name (project.sh
# refuses an OVERRIDE row whose path is missing; both sections reason about
# real files). Copied from <repo-root>, so the scenario exercises the tree
# under test, never a stale clone HEAD.
init_internal() {
	local d=$1 p
	git init -q -b main "$d"
	git -C "$d" config user.name 'Canon'
	git -C "$d" config user.email 'canon@example.net'
	mkdir -p "$d/tools/sync" "$d/docs/sync"
	cp "$repo"/tools/sync/*.sh "$d/tools/sync/"
	cp "$repo"/docs/sync/desensitize.md "$d/docs/sync/"
	awk '/<!--/,/-->/ { next }
		/^## / { insec = ($2 == "OVERRIDE" || $2 == "ALLOWED"); next }
		insec && NF { print $1 }' "$d/docs/sync/desensitize.md" |
		while IFS= read -r p; do
			[ -f "$repo/$p" ] || continue
			mkdir -p "$d/$(dirname "$p")"
			cp "$repo/$p" "$d/$p"
		done
}

fake_gh() { # fake_gh <bin-dir>: read-only gh stub listing no merged PRs
	local bin=$1
	mkdir -p "$bin"
	cat >"$bin/gh" <<'EOF'
#!/usr/bin/env bash
printf '[]'
EOF
	chmod +x "$bin/gh"
}

run_project() { # run_project <internal> <public> <tag>; output in $work/out
	PATH="$work/ghbin:$PATH" bash "$1/tools/sync/project.sh" "$2" \
		--repo "$slug" --tag "$3" >"$work/out" 2>&1
}

# commit_fixture <internal> <tag> <notes>: commit whatever the case wrote and
# tag it with the given release-note body.
commit_fixture() {
	git -C "$1" add -A
	git -C "$1" commit -qm 'w97 fixture'
	git -C "$1" tag -a "$2" -m "$3"
}

# expect_refusal <label> <needle> [<needle2>]: the projection must exit
# nonzero naming the residual, leave no branch and not move the public main.
expect_refusal() {
	local label=$1 needle=$2 needle2=${3:-} before=$4
	if run_project "$irepo" "$pub" "$tag"; then
		fail "$label: the projection exited 0 although the fixture carries a residual shape — no refusal fired, branch projection/$tag now exists: $(cat "$work/out")"
	fi
	grep -q 'identifier invariant violated' "$work/out" ||
		fail "$label: the refusal does not say 'identifier invariant violated': $(cat "$work/out")"
	grep -qF "$needle" "$work/out" ||
		fail "$label: the refusal does not name $needle: $(cat "$work/out")"
	if [ -n "$needle2" ]; then
		grep -qi "$needle2" "$work/out" ||
			fail "$label: the refusal does not identify the $needle2 channel: $(cat "$work/out")"
	fi
	if git -C "$pub" rev-parse --verify "refs/heads/projection/$tag" >/dev/null 2>&1; then
		fail "$label: the refused projection still created projection/$tag"
	fi
	[ "$(git -C "$pub" rev-parse main)" = "$before" ] ||
		fail "$label: the refusal moved the public fixture's main"
}

res=$(residual 94)
tag=w97-inv-$want
irepo=$work/internal
pub=$work/public
init_internal "$irepo"
init_public "$pub"
fake_gh "$work/ghbin"

case $want in
content)
	# a residual token in file CONTENT: the REWRITE left-boundary rule leaves
	# it alone, so the identifier invariant must refuse and name the file.
	mkdir -p "$irepo/docs"
	printf 'residual probe: %s survives the REWRITE rows\n' "$res" >"$irepo/docs/inv_content_probe.md"
	commit_fixture "$irepo" "$tag" "release $tag"
	before=$(git -C "$pub" rev-parse main)
	expect_refusal content docs/inv_content_probe.md '' "$before"
	;;
path)
	# a residual token in the projected PATH only (content is clean, and the
	# RENAME left-boundary rule leaves the segment as is): the path invariant
	# must refuse and name the path.
	mkdir -p "$irepo/docs"
	printf 'path probe only\n' >"$irepo/docs/${res}_probe.md"
	commit_fixture "$irepo" "$tag" "release $tag"
	before=$(git -C "$pub" rev-parse main)
	expect_refusal path "docs/${res}_probe.md" '' "$before"
	;;
message)
	# a residual token in the annotated tag's release notes — the projection
	# commit message channel. The tree itself is clean, so only the message
	# check can refuse; the same invariant must cover it.
	mkdir -p "$irepo/docs"
	printf 'message probe only\n' >"$irepo/docs/msg_probe.md"
	commit_fixture "$irepo" "$tag" "release $tag — residual $res in the notes"
	before=$(git -C "$pub" rev-parse main)
	expect_refusal message "$res" 'message' "$before"
	;;
*)
	fail "unknown case: $want"
	;;
esac

echo "w97: PASS $want"
