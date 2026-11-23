#!/usr/bin/env bash
# test/wi238_flake_loop.sh (polyforge#238 AC1) — the test-only loop harness
# that reproduces the three repaired flaky-test defects and re-runs the
# repaired tree N times.
#
# It is a helper, not a product entry point: it runs the repository's own
# targeted test commands (never a second copy of their assertions), records
# every run's exit code and log, and prints a final pass/fail count. Each run's
# output goes to its own log file and the log is searched after the run, so a
# failure's detail is never eaten by a pipe.
#
# Cases (targeted, one defect each):
#   step-agent     the in-process rig / bare-Config tests must not write
#                  test/step-agent into the checkout (polyforge#84/#85 defect 1)
#   fixture-clock  the verify-scope e2e fixtures carry distinct base commits
#                  without a wall-clock wait, and w111 passes its own --base
#                  (polyforge#85 defect 2)
#   override       the override watch test waits for every dropped pf read to
#                  exit before reset (polyforge#84/85 defect 3)
#
# Usage:
#   bash test/wi238_flake_loop.sh [--case step-agent|fixture-clock|override|all]
#        [--n 20] [--ref <git-ref>] [--expect pass|fail] [--git-clock-wrap]
#        [--slow-dropped-read] [--mutate-remove-argv-wait] [--log-dir <dir>]
#
#   --ref checks the ref out in a throwaway worktree; the repository's
#   webui/dist and pi-web/node_modules are shared from the checkout so the
#   worktree needs no install. --git-clock-wrap puts a git wrapper first on PATH
#   that gives every commit its own second: the pre-fix w111 scenario then
#   deterministically crosses its second boundary and exposes the stale --base
#   reuse (the fixed fixture overrides that clock with its own).
#   --expect fail is the before-fix reproduction: every run must be red.
#   --slow-dropped-read injects the controlled slow child AND the slow next
#   view answer into the target ref's override test mock (idempotent: the
#   committed file already carries both): on an ended ticket the two dropped
#   reads delay their own argv append, and the next view answer is slow, so
#   the late argv lands before that test's assertion. Both refs run the same
#   pressure.
#   --mutate-remove-argv-wait deletes the first ended sub-case's
#   `await awaitDroppedReads()` on the target tree (never committed): the
#   mutation run must be red under --slow-dropped-read.
#
# Evidence for polyforge#238 is the pair of runs — `--ref <pre-fix>` red and
# `--ref HEAD` N times green — with these logs.

set -u

repo_root=$(cd "$(dirname "$0")/.." && pwd)
case_name=all
n=20
ref=""
expect=pass
git_clock_wrap=0
slow_dropped_read=0
mutate_remove_argv_wait=0
log_dir=""

while [ $# -gt 0 ]; do
  case "$1" in
    --case) case_name=${2:?}; shift 2 ;;
    --n) n=${2:?}; shift 2 ;;
    --ref) ref=${2:?}; shift 2 ;;
    --expect) expect=${2:?}; shift 2 ;;
    --git-clock-wrap) git_clock_wrap=1; shift ;;
    --slow-dropped-read) slow_dropped_read=1; shift ;;
    --mutate-remove-argv-wait) mutate_remove_argv_wait=1; shift ;;
    --log-dir) log_dir=${2:?}; shift 2 ;;
    -h|--help) sed -n '2,40p' "$0"; exit 0 ;;
    *) echo "wi238_flake_loop: unknown argument $1" >&2; exit 2 ;;
  esac
done

case "$case_name" in
  step-agent|fixture-clock|override|all) ;;
  *) echo "wi238_flake_loop: --case must be step-agent, fixture-clock, override or all" >&2; exit 2 ;;
esac
case "$expect" in
  pass|fail) ;;
  *) echo "wi238_flake_loop: --expect must be pass or fail" >&2; exit 2 ;;
esac
if ! [ "$n" -gt 0 ] 2>/dev/null; then
  echo "wi238_flake_loop: --n must be a positive integer" >&2
  exit 2
fi

tmp=$(mktemp -d "${TMPDIR:-/tmp}/wi238-flake-loop-XXXXXX")
worktree=$repo_root
if [ -n "$ref" ]; then
  worktree=$tmp/worktree
  if ! git -C "$repo_root" worktree add --detach "$worktree" "$ref" >"$tmp/worktree-add.log" 2>&1; then
    echo "wi238_flake_loop: cannot check out $ref: $(tail -1 "$tmp/worktree-add.log")" >&2
    exit 2
  fi
  if [ -d "$repo_root/webui/dist" ]; then
    cp -a "$repo_root/webui/dist" "$worktree/webui/dist"
  fi
  if [ -d "$repo_root/pi-web/node_modules" ] && ! [ -e "$worktree/pi-web/node_modules" ]; then
    ln -s "$repo_root/pi-web/node_modules" "$worktree/pi-web/node_modules"
  fi
fi

cleanup() {
  if [ "$worktree" != "$repo_root" ]; then
    git -C "$repo_root" worktree remove --force "$worktree" >/dev/null 2>&1 || true
  fi
  rm -rf "$tmp"
}
trap cleanup EXIT

if [ "$git_clock_wrap" = 1 ]; then
  real_git=$(command -v git)
  wrap_dir=$tmp/git-clock
  mkdir -p "$wrap_dir"
  cat >"$wrap_dir/git" <<EOF
#!/bin/sh
case " \$* " in
  *" commit "*)
    state="\${WI238_GIT_CLOCK:?}"
    count=\$(( \$(cat "\$state" 2>/dev/null || echo 0) + 1 ))
    echo "\$count" > "\$state"
    stamp=\$(date -u -d "@\$((1767225600 + count))" +%Y-%m-%dT%H:%M:%SZ)
    export GIT_AUTHOR_DATE="\$stamp" GIT_COMMITTER_DATE="\$stamp"
    ;;
esac
exec "$real_git" "\$@"
EOF
  chmod +x "$wrap_dir/git"
  export WI238_GIT_CLOCK=$tmp/git-clock-state
  export PATH=$wrap_dir:$PATH
fi

if [ "$slow_dropped_read" = 1 ] || [ "$mutate_remove_argv_wait" = 1 ]; then
  # Controlled inputs (polyforge#238 defect 3 reproduction): the slow child and
  # the slow next-view answer together make the late argv land before the next
  # test's assertion. Add-only and idempotent per piece: the committed file
  # already carries both. The mutation removes the first ended sub-case's argv
  # wait on the target tree only, never committed.
  python3 - "$worktree/pi-web/app/api/polyforge/polyforge-override-route.test.mjs" "$slow_dropped_read" "$mutate_remove_argv_wait" <<'W238PATCH'
import sys
path, slow_flag, mutate_flag = sys.argv[1], sys.argv[2] == "1", sys.argv[3] == "1"
lines = open(path).read().split("\n")
if slow_flag:
    if not any("polyforge#238 defect 3 reproduction input:" in line for line in lines):
        idx = next((i for i, line in enumerate(lines) if "fs.appendFileSync(${JSON.stringify(log)}" in line), None)
        if idx is None:
            sys.exit("wi238_flake_loop: the override mock's argv append line was not found")
        lines[idx:idx] = [
            "// polyforge#238 defect 3 reproduction input: on an ended ticket the route",
            "// drops the override/params reads unawaited; letting those two children delay",
            "// their own argv makes the ordering race deterministic for the loop harness.",
            "// This is a controlled slow child (a scenario input), not a synchronization.",
            'if (fs.readFileSync(${JSON.stringify(mode)}, "utf8") === "ended" && (args.includes("override") || args.includes("params"))) {',
            "  const until = Date.now() + 300;",
            "  while (Date.now() < until) { /* deliberate slow child */ }",
            "}",
        ]
    if not any("WI238_SLOW_WATCH" in line for line in lines):
        watch = next((i for i, line in enumerate(lines) if line.strip() == 'answer("running");'), None)
        if watch is None:
            sys.exit("wi238_flake_loop: the override mock's view answer line was not found")
        lines[watch:watch] = [
            '  // The reproduction widens the observation window of the NEXT test, so',
            '  // the late argv lands before its assertion (a controlled slow answer).',
            '  if (mode === "view" && process.env.WI238_SLOW_WATCH === "1") {',
            "    const until = Date.now() + 600;",
            "    while (Date.now() < until) { /* deliberate slow answer */ }",
            "  }",
        ]
if mutate_flag:
    hits = [i for i, line in enumerate(lines) if line == "  await awaitDroppedReads();"]
    if not hits:
        sys.exit("wi238_flake_loop: the mutation target (the first ended sub-case's argv wait) was not found")
    del lines[hits[0]]
open(path, "w").write("\n".join(lines))
W238PATCH
  if [ $? -ne 0 ]; then
    echo "wi238_flake_loop: the reproduction patch/mutation failed" >&2
    exit 2
  fi
fi

if [ -z "$log_dir" ]; then
  log_dir=$tmp/logs
fi
mkdir -p "$log_dir"

run_step_agent() {
  rm -rf "$worktree/test/step-agent"
  ( cd "$worktree" && GOWORK=off go test ./test/ \
      -run '^(TestV1S18PushBackoffBackpressure|TestV1S20LongRunGoroutines|TestV26bPiResidentSessionTakesLaterMessages)$' -count=1 )
  code=$?
  if [ "$code" -ne 0 ]; then
    return "$code"
  fi
  if [ -e "$worktree/test/step-agent" ]; then
    echo "the inproc rig wrote the daemon-owned step agent dir into the checkout: $worktree/test/step-agent"
    return 1
  fi
  return 0
}

run_fixture_clock() {
  ( cd "$worktree" && GOWORK=off go test ./test/ \
      -run '^(TestW238E2EFixturesCarryDistinctBases|TestW111ScopedCommandSelected)$' -count=1 )
}

run_override() {
  ( cd "$worktree/pi-web" && WI238_SLOW_WATCH=$slow_dropped_read node --experimental-strip-types --test app/api/polyforge/polyforge-override-route.test.mjs )
}

run_case() {
  case "$1" in
    step-agent) run_step_agent ;;
    fixture-clock) run_fixture_clock ;;
    override) run_override ;;
  esac
}

cases="step-agent fixture-clock override"
if [ "$case_name" != all ]; then
  cases=$case_name
fi

total_pass=0
total_fail=0
overall=0

for one in $cases; do
  pass=0
  fail=0
  i=1
  while [ "$i" -le "$n" ]; do
    log=$log_dir/$one-run$i.log
    run_case "$one" >"$log" 2>&1
    code=$?
    if [ "$code" -eq 0 ]; then
      pass=$((pass + 1))
      echo "case=$one run=$i exit=0"
    else
      fail=$((fail + 1))
      echo "case=$one run=$i exit=$code"
      detail=$(grep -E -m1 'FAIL|--- FAIL|AssertionError|did not exit|step agent dir|no such|not found' "$log" | cut -c1-240)
      echo "  failure: ${detail:-$(tail -1 "$log" | cut -c1-240)}"
      echo "  log: $log"
    fi
    i=$((i + 1))
  done
  echo "summary case=$one n=$n pass=$pass fail=$fail expect=$expect"
  total_pass=$((total_pass + pass))
  total_fail=$((total_fail + fail))
  if [ "$expect" = pass ] && [ "$fail" -ne 0 ]; then
    overall=1
  fi
  if [ "$expect" = fail ] && [ "$pass" -ne 0 ]; then
    echo "summary case=$one: expected every run red, $pass run(s) passed" >&2
    overall=1
  fi
done

echo "summary all n=$n pass=$total_pass fail=$total_fail expect=$expect ref=${ref:-HEAD-worktree} log-dir=$log_dir"
exit "$overall"
