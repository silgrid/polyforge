#!/bin/sh
# e2e-parallel.sh — run the ./test acceptance package with one process per
# top-level test, E2E_JOBS processes at a time.
#
# Every test gets a fresh run of the compiled test binary: its own TestMain
# HOME, its own bin dir and its own process sweep (test/proc_hygiene_test.go),
# so the package's serial assumptions (no t.Parallel, process-wide env and
# goroutine counts) hold inside each process. The binaries are built once and
# handed in through PFV1_PREBUILT_BINS (test/v1_harness_test.go). Tests start
# slowest-first by the previous full run's durations, kept in
# $XDG_CACHE_HOME/polyforge/e2e-durations (tests it does not know go first).
#
#   E2E_JOBS   concurrent test processes (default: the CPU count, at most 8)
#   E2E_RUN    regexp over top-level test names (default: all)
#
# A test fails when its process exits non-zero or prints no top-level
# PASS/SKIP verdict (-test.paniconexit0 keeps go test's guard against a
# stray os.Exit(0)); its full -v output is printed and the exit status is 1.
# `go test ./test/ -timeout 20m` still runs the package serially.
set -eu

root=$(cd "$(dirname "$0")/.." && pwd)
work=$(mktemp -d "${TMPDIR:-/tmp}/pf-e2e-XXXXXX")
trap 'rm -rf "$work"' EXIT
trap 'exit 130' INT TERM
cd "$root"

cpus=$(getconf _NPROCESSORS_ONLN 2>/dev/null || echo 4)
jobs=${E2E_JOBS:-$((cpus < 8 ? cpus : 8))}
cache=${XDG_CACHE_HOME:-${HOME:-$work}/.cache}/polyforge/e2e-durations

export GOWORK=off
go test -c -o "$work/e2e.test" ./test
go build -o "$work/bin/" ./cmd/aihub ./cmd/pf ./test/fakepi
mkdir -p "$work/logs"
: >"$work/rc"

(cd test && "$work/e2e.test" -test.list "${E2E_RUN:-.}") | grep '^Test' >"$work/names" || true
known=$cache
[ -f "$known" ] || known=/dev/null
awk -v known="$known" 'FILENAME == known { d[$1] = $2; next } { print ($1 in d ? d[$1] : 1e9), $1 }' "$known" "$work/names" |
	sort -k1,1nr | cut -d' ' -f2 >"$work/queue"
total=$(wc -l <"$work/queue" | tr -d ' ')
if [ "$total" -eq 0 ]; then
	echo "e2e-parallel: no test matches E2E_RUN=${E2E_RUN:-.}" >&2
	exit 1
fi

start=$(date +%s)
E2E_WORK=$work PFV1_PREBUILT_BINS=$work/bin xargs -P "$jobs" -n 1 sh -c '
	cd test
	if "$E2E_WORK/e2e.test" -test.run "^$1\$" -test.v -test.paniconexit0 -test.timeout 10m >"$E2E_WORK/logs/$1.log" 2>&1; then
		echo "$1 0" >>"$E2E_WORK/rc"
	else
		echo "$1 $?" >>"$E2E_WORK/rc"
		echo "FAIL $1"
	fi
' _ <"$work/queue"
wall=$(($(date +%s) - start))

# top-level verdict lines only ("--- PASS: TestX (1.23s)"; subtests are indented)
cat "$work"/logs/*.log 2>/dev/null |
	sed -En 's/^--- (PASS|FAIL|SKIP): (Test[^ ]*) \(([0-9.]*)s\)$/\2 \3 \1/p' >"$work/times"
# failed: a non-zero exit, or exit 0 without a PASS/SKIP verdict line
failed=$(awk -v times="$work/times" 'FILENAME == times { if ($3 != "FAIL") ok[$1] = 1; next } $2 != 0 || !($1 in ok) { print $1 }' "$work/times" "$work/rc")
skipped=$(awk '$3 == "SKIP"' "$work/times" | wc -l | tr -d ' ')
ran=$(wc -l <"$work/rc" | tr -d ' ')

if [ -z "${E2E_RUN:-}" ] && [ "$ran" -eq "$total" ]; then
	{ mkdir -p "$(dirname "$cache")" && cut -d' ' -f1,2 "$work/times" >"$cache.$$" && mv "$cache.$$" "$cache"; } || true
fi

echo "slowest:"
sort -k2,2nr "$work/times" | head -5 | awk '{ printf "  %7.1fs  %s\n", $2, $1 }'
if [ -n "$failed" ] || [ "$ran" -ne "$total" ]; then
	for name in $failed; do
		echo "=== output of $name"
		cat "$work/logs/$name.log"
	done
	echo "FAIL	e2e	$ran/$total tests ran, failed: $(echo "$failed" | tr '\n' ' ')	${wall}s (E2E_JOBS=$jobs)"
	exit 1
fi
echo "ok	e2e	$total tests ($skipped skipped)	${wall}s (E2E_JOBS=$jobs)"
