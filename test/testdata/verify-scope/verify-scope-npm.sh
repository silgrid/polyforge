#!/bin/sh
# verify-scope.sh — repo-owned verification-tier scope reporter.
#
# Usage:
#   verify-scope.sh [--base <rev>] [--run]
#
# Reports, on stdout and nothing else, the verification scope of the diff
# from a base revision to HEAD:
#
#   mode: full|scoped
#   path: <repo-relative path>      one per affected path, sorted, unique
#   command: <command>              the commands to run, in the fixed order
#
# `--base <rev>` overrides the default base. The default base is
# `git merge-base HEAD origin/<default>`, where <default> is the branch
# `origin/HEAD` points at (fallback `main`). The capsule baseline in
# `capsule.json` is NEVER read.
#
# The repository identity is derived at runtime from the `origin` remote URL
# and used only in stderr diagnostics; the program names no repository
# literally and never fails on a non-GitHub origin.
#
# Diagnostics go to stderr. `--run` prints the report and then executes the
# reported commands in order with `sh -c`, stopping at the first non-zero
# and exiting non-zero; their output goes to stderr so stdout keeps the
# machine-readable report shape.
#
# Fixed command order (documented also in CONTRIBUTING.md §6):
#   npm ci                       (only when node_modules/ is absent)
#   npm run lint
#   node_modules/.bin/tsc --noEmit
#   npm test
#   full tier only:  npm run test:e2e; npm run test:terminal; plus every
#                    standalone e2e/*.mjs suite (themes.mjs excepted: it needs
#                    an external dev server), so full covers scoped
#   scoped tier:     node e2e/<script>.mjs  (selected non-browser scripts)
#
# `next build` is never run: it is forbidden in a dev checkout and belongs to
# CI. This is a verification reporter, not a release or deploy entry point.

set -u
export LC_ALL=C

usage() {
	cat >&2 <<'EOF'
usage: verify-scope.sh [--base <rev>] [--run]
  --base <rev>   diff base (default: merge-base HEAD origin/<default>)
  --run          print the report, then run the reported commands in order
EOF
}

base_arg=""
run=0
while [ $# -gt 0 ]; do
	case "$1" in
	--base)
		[ $# -ge 2 ] || {
			echo "verify-scope: --base needs a revision" >&2
			usage
			exit 2
		}
		base_arg="$2"
		shift 2
		;;
	--base=*)
		base_arg="${1#--base=}"
		[ -n "$base_arg" ] || {
			echo "verify-scope: --base needs a revision" >&2
			usage
			exit 2
		}
		shift
		;;
	--run)
		run=1
		shift
		;;
	-h | --help)
		usage
		exit 0
		;;
	*)
		echo "verify-scope: unknown argument '$1'" >&2
		usage
		exit 2
		;;
	esac
done

root="$(cd "$(dirname "$0")/.." && pwd)"
tmp="$(mktemp -d)" || exit 1
trap 'rm -rf "$tmp"' EXIT INT TERM

# Repository identity from origin, stderr only. Best effort: a non-GitHub
# origin (a local bare fixture, say) is reported as-is and never fails.
origin_url="$(git -C "$root" remote get-url origin 2>/dev/null || true)"
if [ -n "$origin_url" ]; then
	slug="$(printf '%s' "$origin_url" | sed -n 's#.*[:/]\([^/]*/[^/]*\)$#\1#p')"
	[ -n "$slug" ] || slug="$origin_url"
	echo "verify-scope: repository origin $slug" >&2
fi

# strip_ext removes one trailing JS/TS module extension (an extensionless
# import names a changed file through its stem).
strip_ext() {
	case "$1" in
	*.ts | *.tsx | *.mjs | *.js | *.jsx) printf '%s' "${1%.*}" ;;
	*) printf '%s' "$1" ;;
	esac
}

# join_pipe prints the lines it reads joined by `|` (input pre-sorted).
join_pipe() {
	awk 'NR > 1 { printf "|" } { printf "%s", $0 }'
}

# add_cmd appends one command to the report.
add_cmd() { printf '%s\n' "$1" >>"$tmp/commands"; }

# ---------------------------------------------------------------------------
# Affected paths of base..HEAD (sorted, unique). Deleted files keep their
# pre-image path; a rename contributes both the old and the new path.
# ---------------------------------------------------------------------------
changed="$tmp/changed"
: >"$changed"
full=0
if ! git -C "$root" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
	echo "verify-scope: not a git work tree; falling back to the full tier" >&2
	full=1
else
	base=""
	if [ -n "$base_arg" ]; then
		base="$base_arg"
	else
		default="$(git -C "$root" symbolic-ref -q --short refs/remotes/origin/HEAD 2>/dev/null || true)"
		default="${default#origin/}"
		[ -n "$default" ] || default="main"
		base="$(git -C "$root" merge-base HEAD "origin/$default" 2>/dev/null || true)"
		if [ -z "$base" ]; then
			echo "verify-scope: cannot resolve the base (merge-base HEAD origin/$default); falling back to the full tier" >&2
		fi
	fi
	if [ -z "$base" ]; then
		full=1
	elif git -C "$root" diff --name-status -M "$base"..HEAD >"$tmp/diff" 2>>"$tmp/stderr"; then
		: >"$tmp/changed_raw"
		while IFS= read -r line; do
			[ -n "$line" ] || continue
			status="${line%%	*}"
			rest="${line#*	}"
			case "$status" in
			R* | C*)
				printf '%s\n' "${rest%%	*}" >>"$tmp/changed_raw"
				printf '%s\n' "${rest#*	}" >>"$tmp/changed_raw"
				;;
			*)
				printf '%s\n' "$rest" >>"$tmp/changed_raw"
				;;
			esac
		done <"$tmp/diff"
		if [ -s "$tmp/changed_raw" ]; then
			sort -u "$tmp/changed_raw" >"$changed"
		fi
	else
		echo "verify-scope: cannot diff $base..HEAD; falling back to the full tier" >&2
		full=1
	fi
fi

# forced_full applies the mechanical forced-full list.
forced_full() {
	while IFS= read -r p; do
		case "$p" in
		package.json | package-lock.json | next.config.ts | tsconfig.json) return 0 ;;
		patches | patches/*) return 0 ;;
		.polyforge | .polyforge/*) return 0 ;;
		esac
		base="${p##*/}"
		case "$base" in
		eslint.config.* | .eslintrc*) return 0 ;;
		esac
	done <"$changed"
	return 1
}

if [ "$full" -eq 0 ] && forced_full; then
	full=1
fi

mode="full"
[ "$full" -eq 0 ] && mode="scoped"

# ---------------------------------------------------------------------------
# Commands. Fast gates always; the scoped tier adds selected non-browser
# e2e scripts, the full tier runs every standalone e2e script (including the
# self-starting Playwright ones) so it stays a superset of the scoped tier.
# ---------------------------------------------------------------------------
# Helper modules imported by another e2e/*.mjs through a relative specifier
# are not standalone suites: `node e2e/<helper>.mjs` does nothing. They are
# excluded from the scoped selection and the full standalone pass alike.
e2e_helpers="$tmp/e2e_helpers"
: >"$e2e_helpers"
if [ -d "$root/e2e" ]; then
	grep -ho 'from "\./[^"]*\.mjs"' "$root"/e2e/*.mjs 2>/dev/null |
		sed -e 's#^from "\./##' -e 's#"$##' | sort -u >"$e2e_helpers"
fi

: >"$tmp/commands"
if [ ! -d "$root/node_modules" ]; then
	add_cmd "npm ci"
fi
add_cmd "npm run lint"
add_cmd "node_modules/.bin/tsc --noEmit"
add_cmd "npm test"
if [ "$mode" = "full" ]; then
	add_cmd "npm run test:e2e"
	add_cmd "npm run test:terminal"
	# run.mjs/terminal.mjs are already run above; themes.mjs needs an
	# external dev server; the imported helper modules are not suites.
	for script in "$root"/e2e/*.mjs; do
		[ -f "$script" ] || continue
		base="${script##*/}"
		case "$base" in
		run.mjs | terminal.mjs | themes.mjs) continue ;;
		esac
		grep -Fxq -- "$base" "$e2e_helpers" && continue
		add_cmd "node e2e/$base"
	done
else
	for script in "$root"/e2e/*.mjs; do
		[ -f "$script" ] || continue
		grep -qi 'playwright' "$script" && continue
		base="${script##*/}"
		grep -Fxq -- "$base" "$e2e_helpers" && continue
		matched=0
		while IFS= read -r p; do
			[ -n "$p" ] || continue
			if grep -qF -- "$p" "$script"; then
				matched=1
				break
			fi
			stem="$(strip_ext "$p")"
			if [ "$stem" != "$p" ] && grep -qF -- "$stem" "$script"; then
				matched=1
				break
			fi
		done <"$changed"
		[ "$matched" -eq 1 ] && add_cmd "node e2e/$base"
	done
fi

printf 'mode: %s\n' "$mode"
while IFS= read -r p; do
	printf 'path: %s\n' "$p"
done <"$changed"
while IFS= read -r c; do
	printf 'command: %s\n' "$c"
done <"$tmp/commands"

if [ "$run" -eq 1 ]; then
	while IFS= read -r c; do
		if ! (cd "$root" && sh -c "$c" 1>&2); then
			echo "verify-scope: reported command failed: $c" >&2
			exit 1
		fi
	done <"$tmp/commands"
fi
