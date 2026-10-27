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
# Diagnostics go to stderr. `--run` prints the report and then executes the
# reported commands in order with `sh -c`, stopping at the first non-zero
# and exiting non-zero; their output goes to stderr so stdout keeps the
# machine-readable report shape.
#
# Fixed command order (documented also in docs/contributing.md):
#   1. GOWORK=off go build ./...
#   2. GOWORK=off go vet ./...
#   3. test -z "$(gofmt -l .)"
#   4. python3 -I docs/design/polyforge-vnext/tools/check-xrefs.py
#   5. GOWORK=off GOOS=darwin GOARCH=arm64 go build -trimpath ./...
#   6. GOWORK=off GOOS=darwin GOARCH=amd64 go build -trimpath ./...
#   full tier only:
#   7. GOWORK=off make test
#   scoped tier (after the fast gates):
#   7. GOWORK=off go test -count=1 <changed + reverse-dependency packages>
#      (reverse dependencies are computed over each package's test binary
#       closure, so a package reached only through a `_test.go` import is
#       selected too)
#   8. GOWORK=off go test -count=1 -run '^(...)$' <pkg>  (text-referenced tests)
#   9. E2E_RUN='^(...)$' sh test/e2e-parallel.sh          (text-referenced ./test)
#
# GOWORK=off keeps the commands correct in a workspace whose root carries an
# unrelated go.work. The xrefs gate runs python3 directly: a missing python3
# fails loudly (non-zero), it is never skipped.
#
# This is a verification reporter, not a release or deploy entry point.

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

# path_dir answers the repo-relative directory of a repo-relative path.
path_dir() {
	d="${1%/*}"
	[ "$d" = "$1" ] && d="."
	printf '%s' "$d"
}

# rel_dir maps an absolute package directory back to the repo root.
rel_dir() {
	case "$1" in
	"$root"/*) printf '%s' "${1#"$root"/}" ;;
	"$root") printf '.' ;;
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
	m1=0 m2=0 m3=0 m4=0
	while IFS= read -r p; do
		case "$p" in
		.polyforge | .polyforge/*) return 0 ;;
		internal/polyforge/driver | internal/polyforge/driver/*) return 0 ;;
		internal/polyforge/daemon | internal/polyforge/daemon/*) return 0 ;;
		go.mod | go.sum | Makefile) return 0 ;;
		test/main_test.go | test/e2e-parallel.sh | test/fakepi/*) return 0 ;;
		esac
	done <"$changed"
	while IFS= read -r p; do
		case "$p" in
		cmd/*) m1=1 ;;
		internal/aihub/*) m2=1 ;;
		internal/polyforge/*) m3=1 ;;
		internal/common/*) m4=1 ;;
		esac
	done <"$changed"
	[ $((m1 + m2 + m3 + m4)) -ge 2 ]
}

if [ "$full" -eq 0 ] && forced_full; then
	full=1
fi

# ---------------------------------------------------------------------------
# Scoped selection. Answers non-zero when the package graph cannot be
# computed, which turns the scope into the full tier instead of a wrong
# scoped one.
# ---------------------------------------------------------------------------
: >"$tmp/scoped_cmds"
add_scoped() { printf '%s\n' "$1" >>"$tmp/scoped_cmds"; }

compute_scoped() {
	golist="$tmp/golist"
	if ! (cd "$root" && GOWORK=off go list -test -f '{{.ImportPath}}|{{.Dir}}|{{join .Deps " "}}' ./...) >"$golist" 2>>"$tmp/stderr"; then
		echo "verify-scope: go list failed; falling back to the full tier" >&2
		return 1
	fi
	# go list -test also emits each package's test binary (`P.test`), whose
	# Deps is the closure of everything `go test` compiles for P — test imports
	# included. Fold that closure into P's plain entry, so the reverse-dependency
	# scan below selects a package reached only through a `_test.go` import. The
	# test-variant entries (`P [P.test]`, `P_test [P.test]`) are dropped: `go test
	# ./P` already runs them with P.
	packages="$tmp/packages"
	awk -F'|' '
		FNR == NR { if ($1 ~ /\.test$/) tdeps[$2] = $3; next }
		$1 ~ / \[/ { next }
		$1 ~ /\.test$/ { next }
		$2 in tdeps { print $1 "|" $2 "|" tdeps[$2]; next }
		{ print }
	' "$golist" "$golist" >"$packages"
	# Map each changed path to its nearest ancestor `go list` package
	# directory: a file in `<pkg>/testdata/` or another package subdirectory
	# belongs to its owning package. A path with no ancestor package selects
	# no package (and no test).
	pkg_dirs="$tmp/pkg_dirs"
	: >"$pkg_dirs"
	while IFS='|' read -r imp dir deps; do
		[ -n "$imp" ] || continue
		printf '%s\n' "$(rel_dir "$dir")" >>"$pkg_dirs"
	done <"$packages"
	sort -u "$pkg_dirs" -o "$pkg_dirs"
	mapped_dirs="$tmp/mapped_dirs"
	: >"$mapped_dirs"
	while IFS= read -r p; do
		d="$(path_dir "$p")"
		while :; do
			if grep -Fxq -- "$d" "$pkg_dirs"; then
				printf '%s\n' "$d" >>"$mapped_dirs"
				break
			fi
			[ "$d" = "." ] && break
			case "$d" in
			*/*) d="${d%/*}" ;;
			*) d="." ;;
			esac
		done
	done <"$changed"
	sort -u "$mapped_dirs" -o "$mapped_dirs"
	changed_imports="$tmp/changed_imports"
	: >"$changed_imports"
	while IFS='|' read -r imp dir deps; do
		[ -n "$imp" ] || continue
		rel="$(rel_dir "$dir")"
		if grep -Fxq -- "$rel" "$mapped_dirs"; then
			printf '%s\n' "$imp" >>"$changed_imports"
		fi
	done <"$packages"
	# Changed packages plus every package that (transitively) depends on one.
	selected="$tmp/selected"
	: >"$selected"
	if [ -s "$changed_imports" ]; then
		while IFS='|' read -r imp dir deps; do
			[ -n "$imp" ] || continue
			rel="$(rel_dir "$dir")"
			# ./test is never selected by the reverse-dependency graph.
			[ "$rel" = "test" ] && continue
			sel=0
			if grep -Fxq -- "$rel" "$mapped_dirs"; then
				sel=1
			else
				while IFS= read -r cp; do
					case " $deps " in
					*" $cp "*)
						sel=1
						break
						;;
					esac
				done <"$changed_imports"
			fi
			[ "$sel" -eq 1 ] && printf '%s\n' "$rel" >>"$selected"
		done <"$packages"
	fi
	if [ -s "$selected" ]; then
		sort -u "$selected" -o "$selected"
		pkgargs=""
		while IFS= read -r rel; do
			if [ "$rel" = "." ]; then
				pkgargs="$pkgargs ."
			else
				pkgargs="$pkgargs ./$rel"
			fi
		done <"$selected"
		add_scoped "GOWORK=off go test -count=1$pkgargs"
	fi
	# Tests that textually name a changed path, per package not already in
	# the package command (./test goes through E2E_RUN below, never here).
	tr_pairs="$tmp/tr_pairs"
	: >"$tr_pairs"
	while IFS= read -r p; do
		while IFS= read -r f; do
			[ -n "$f" ] || continue
			case "$f" in
			test/*) continue ;;
			*_test.go) ;;
			*) continue ;;
			esac
			names="$(sed -n 's/^func \(Test[^ (]*\).*/\1/p' "$root/$f" | sort -u)"
			[ -n "$names" ] || continue
			d="$(path_dir "$f")"
			while IFS= read -r name; do
				[ -n "$name" ] && printf '%s|%s\n' "$d" "$name" >>"$tr_pairs"
			done <<EOF
$names
EOF
		done <<EOF
$(git -C "$root" grep -F -l -e "$p" 2>/dev/null || true)
EOF
	done <"$changed"
	if [ -s "$tr_pairs" ]; then
		cut -d'|' -f1 "$tr_pairs" | sort -u >"$tmp/tr_dirs"
		while IFS= read -r d; do
			grep -Fxq -- "$d" "$selected" && continue
			names="$(awk -F'|' -v d="$d" '$1 == d { print $2 }' "$tr_pairs" | sort -u | join_pipe)"
			if [ "$d" = "." ]; then
				pkg="."
			else
				pkg="./$d"
			fi
			add_scoped "GOWORK=off go test -count=1 -run '^($names)\$' $pkg"
		done <"$tmp/tr_dirs"
	fi
	# ./test acceptance tests whose source names a changed path, by name.
	: >"$tmp/e2e_names"
	while IFS= read -r p; do
		while IFS= read -r f; do
			[ -n "$f" ] || continue
			case "$f" in
			test/*.go) ;;
			*) continue ;;
			esac
			sed -n 's/^func \(Test[^ (]*\).*/\1/p' "$root/$f" >>"$tmp/e2e_names"
		done <<EOF
$(git -C "$root" grep -F -l -e "$p" 2>/dev/null || true)
EOF
	done <"$changed"
	if [ -s "$tmp/e2e_names" ]; then
		names="$(sort -u "$tmp/e2e_names" | join_pipe)"
		add_scoped "E2E_RUN='^($names)\$' sh test/e2e-parallel.sh"
	fi
	return 0
}

mode="full"
if [ "$full" -eq 0 ]; then
	if [ ! -s "$changed" ]; then
		# Empty diff: scoped with an empty affected set, fast gates only.
		mode="scoped"
	elif compute_scoped; then
		mode="scoped"
	else
		full=1
	fi
fi

: >"$tmp/commands"
add_cmd "GOWORK=off go build ./..."
add_cmd "GOWORK=off go vet ./..."
add_cmd 'test -z "$(gofmt -l .)"'
add_cmd "python3 -I docs/design/polyforge-vnext/tools/check-xrefs.py"
add_cmd "GOWORK=off GOOS=darwin GOARCH=arm64 go build -trimpath ./..."
add_cmd "GOWORK=off GOOS=darwin GOARCH=amd64 go build -trimpath ./..."
if [ "$mode" = "full" ]; then
	add_cmd "GOWORK=off make test"
else
	cat "$tmp/scoped_cmds" >>"$tmp/commands"
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
