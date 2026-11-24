#!/usr/bin/env bash
# test/stress-regressions.sh (polyforge#272 AC2) — the test-only stress loop
# that reproduces the intermittent acceptance failures before their fix
# (`before`) and runs the fixed tree N times (`after`).
#
# It is a helper, not a product entry point: every round runs the
# repository's own test (never a second copy of its assertions) as a fresh
# process, and the round's whole output lands in its own log, searched after
# the round — a failure's detail is never eaten by a pipe.
#
# Usage:
#   bash test/stress-regressions.sh --repo <R> --phase before|after
#        --rounds N --jobs J --out <DIR>
#        [--item s12|w224|w348rr|w348wait|f12]... [--items-file F]
#        [--round-timeout SECONDS]
#
#   --repo     the tree whose tests run (the baseline worktree for before,
#              the fixed tree for after); --out must not be inside a
#              different filesystem from it (s12 hard-links node_modules)
#   --item     an item to run (repeatable). Without one the default is this
#              ticket's four: s12 w224 w348rr w348wait. f12 runs only when
#              named (kept for the production ticket that owns its fix).
#   --items-file  a TSV of items replacing the built-in ones (the script's own
#              self-test, test/pf272_stress_regressions_test.go): one item per
#              line, tab-separated, every field non-empty —
#                name  prepare  cmd  pass-regexp  signature-regexp  [count_lines_only]
#              (regexps are grep -E; prepare runs once per phase in --repo,
#              cmd once per round in the round's STRESS_WORKDIR). Without
#              --item every item in the file runs.
#   --round-timeout  a round still running after this many seconds is killed
#              with its process group and counted failed (exit 124);
#              default 1800.
#
# Items run one after another; each item's rounds run J at a time, round k
# of the item on a free worker w1..wJ — the pressure is the same test J ways
# at once under the container's CPU cap.
#   before  each item runs at most N rounds; once a finished round shows the
#           item's target signature no new round is started, the rounds in
#           flight are collected, and the item is `reproduced round <n>` (the
#           lowest round that showed it). An item no round reproduces is
#           `not reproduced` and the phase exits 1. A failure that is not the
#           signature never counts as a reproduction.
#   after   each item runs exactly N rounds; the phase exits 0 only when
#           every round printed the item's pass line and no target signature
#           — and, unless the item counts its lines only, exited 0. A failed
#           round is never offset by a later green one.
# A count_lines_only item (s12: the whole harness-settings-85 suite runs, only
# its S12 lines count) is judged by its pass and signature lines alone; its
# exit code and every other `FAIL` line are recorded in the manifest's other
# column, not judged. A round with no line of the target (the suite crashed
# before S12) is no hit before and a failure after.
#
# Built-in items (prepare once per phase, then a fresh process per round):
#   w224 w348rr w348wait  the Go acceptance harness: prepare builds, in R,
#       webui/dist when missing, then `go test -c` of ./test and pf + fakepi
#       into $OUT/bin; a round runs `$OUT/bin/e2e.test -test.run '^<Name>$'`
#       in R/test with PFV1_PREBUILT_BINS=$OUT/bin (test/e2e-parallel.sh's
#       per-test shape). Pass `--- PASS: <Name> `; signatures:
#         w224      no summary is held|no summarize fact ever landed
#         w348rr    round-robin did not serve the idle Cell next
#         w348wait  --- FAIL: TestW348WaitingJobFreesItsSlot (the summary
#                   records its first w348_cell_e2e_acceptance_test.go:<n>:
#                   line)
#   s12  one git worktree of R's HEAD per worker ($OUT/work/s12/w<k>); prepare
#       runs `npm ci` in w1/pi-web inside this environment (node-pty is
#       native: never the host's node_modules) and one warm-up
#       `E2E_SERVER_MODE=start npm run test:e2e:all -- harness-settings-85.mjs`
#       that builds and stamps .next/e2e-build (its result is not counted),
#       then hard-links node_modules (cp -al) and copies .next/e2e-build
#       (cp -a) into w2..wJ — one commit tree, no shared .next. A round runs
#       the suite with E2E_PREBUILT_DIST=.next/e2e-build. Pass `^PASS S12 \(`;
#       signature `FAIL S12 the one-shot login terminal attaches: .*the login
#       terminal never showed the command's output`. The worktrees are
#       removed when the item ends.
#   f12  (only when named) `go test -race -c` of ./internal/polyforge/daemon;
#       a round runs TestShutdownRestartFullChainPushIdempotency there.
#       Signature `TempDir RemoveAll cleanup: unlinkat .*/data: directory not
#       empty`.
#
# Isolation: every round gets HOME=$OUT/home/<item>-<round>,
# TMPDIR=$OUT/tmp/<item>-<round> and XDG_{CONFIG,CACHE,DATA,STATE}_HOME under
# that HOME; STRESS_ROUND, STRESS_WORKER, STRESS_WORKDIR ($OUT/work/<item>/w<k>,
# one per worker), STRESS_ITEM, STRESS_PHASE, STRESS_OUT, STRESS_REPO and
# STRESS_BIN are exported to it. GOCACHE, GOPATH, npm_config_cache and
# PLAYWRIGHT_BROWSERS_PATH are read once at start and exported fixed, so they
# do not follow the per-round HOME. Every PF_* variable of the caller is
# unset. Each round starts in its own session (setsid); a timeout kills only
# that process group, and an interrupted run kills only the groups it
# started.
#
# Records under --out: env.txt (R's HEAD, argv, nproc, cgroup cpu.max and
# memory.max, free -m, container or not, STRESS_IMAGE, loadavg at start and
# end, go/node versions); manifest.tsv (phase, item, round, worker, start,
# end, exit, pass, signature, other, loadavg, log); logs/<item>-<round>.log
# and logs/<item>-prepare.log; summary.txt (per item: rounds, passes,
# signature rounds, failed rounds, the verdict; its last line is
# `stress-regressions: <phase> ok` only when the phase passed, else
# `stress-regressions: <phase> FAILED`).
#
# polyforge#272's evidence runs in the repository's verification image, with
# the same limits before and after (the owner's 6A ruling), the whole capsule
# bind-mounted at its host path (worktree metadata holds host paths and
# cp -al cannot cross mounts):
#
#   TAG=pf272-verify:$(sha256sum "$R/.polyforge/Dockerfile.verify" | cut -c1-12)
#   docker build -t "$TAG" - < "$R/.polyforge/Dockerfile.verify"
#   docker run --rm --init --cpus=4 --memory=16g --memory-swap=16g --pids-limit=8192 \
#     --mount type=bind,source="$CAP",target="$CAP" \
#     --mount type=volume,source=pf272-stress-cache,target=/cache \
#     -w "$R" -e STRESS_IMAGE="$TAG" -e HOME="$S/home-outer" -e TMPDIR="$S/tmp-outer" \
#     "$TAG" bash "$R/test/stress-regressions.sh" --repo "$S/base-wt" \
#       --phase before --rounds 100 --jobs 4 --out "$S/before"
#   (after: --repo "$R" --phase after --rounds 50 --jobs 4 --out "$S/after")
#
# Exit: 0 the phase passed, 1 it did not, 2 a usage error.

set -u

self="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/$(basename "${BASH_SOURCE[0]}")"
argv_text="$0 $*"

usage() {
  echo "stress-regressions: $*" >&2
  echo "usage: bash test/stress-regressions.sh --repo R --phase before|after --rounds N --jobs J --out DIR [--item NAME]... [--items-file F] [--round-timeout S]" >&2
  exit 2
}

# ---------------------------------------------------------------------------
# Built-in item steps, run as `bash <self> __builtin <step> [arg]` from an
# item's prepare or cmd (the per-round environment is already in place).
# ---------------------------------------------------------------------------
if [ "${1:-}" = "__builtin" ]; then
  step=${2:?}
  arg=${3:-}
  case "$step" in
    go-prepare)
      cd "$STRESS_REPO" || exit 1
      if [ ! -d webui/dist ]; then
        echo "stress-regressions: webui/dist is missing; building it (cd webui && npm ci && npm run build)"
        (cd webui && npm ci && npm run build) || exit 1
      fi
      mkdir -p "$STRESS_BIN" || exit 1
      GOWORK=off go test -c -o "$STRESS_BIN/e2e.test" ./test || exit 1
      GOWORK=off go build -o "$STRESS_BIN/" ./cmd/pf ./test/fakepi || exit 1
      ;;
    go-round)
      cd "$STRESS_REPO/test" || exit 1
      PFV1_PREBUILT_BINS="$STRESS_BIN" exec "$STRESS_BIN/e2e.test" -test.run "^${arg}\$" -test.v -test.count=1 -test.paniconexit0 -test.timeout 10m
      ;;
    f12-prepare)
      cd "$STRESS_REPO" || exit 1
      mkdir -p "$STRESS_BIN" || exit 1
      GOWORK=off go test -race -c -o "$STRESS_BIN/daemon.test" ./internal/polyforge/daemon || exit 1
      ;;
    f12-round)
      cd "$STRESS_REPO/internal/polyforge/daemon" || exit 1
      exec "$STRESS_BIN/daemon.test" -test.run '^TestShutdownRestartFullChainPushIdempotency$' -test.count=1 -test.v -test.timeout 5m
      ;;
    s12-prepare)
      base=$STRESS_WORKDIR_BASE
      mkdir -p "$base" || exit 1
      for k in $(seq 1 "$STRESS_JOBS"); do
        git -C "$STRESS_REPO" worktree add --detach "$base/w$k" HEAD || exit 1
      done
      (cd "$base/w1/pi-web" && npm ci) || exit 1
      echo "stress-regressions: warm-up suite run (builds and stamps .next/e2e-build; not counted)"
      (cd "$base/w1/pi-web" && E2E_SERVER_MODE=start npm run test:e2e:all -- harness-settings-85.mjs)
      [ -f "$base/w1/pi-web/.next/e2e-build/BUILD_ID" ] || { echo "stress-regressions: the warm-up left no .next/e2e-build" >&2; exit 1; }
      for k in $(seq 2 "$STRESS_JOBS"); do
        cp -al "$base/w1/pi-web/node_modules" "$base/w$k/pi-web/node_modules" || exit 1
        mkdir -p "$base/w$k/pi-web/.next" || exit 1
        cp -a "$base/w1/pi-web/.next/e2e-build" "$base/w$k/pi-web/.next/e2e-build" || exit 1
      done
      ;;
    s12-round)
      cd "$STRESS_WORKDIR/pi-web" || exit 1
      E2E_SERVER_MODE=start E2E_PREBUILT_DIST=.next/e2e-build exec npm run test:e2e:all -- harness-settings-85.mjs
      ;;
    s12-cleanup)
      base=$STRESS_WORKDIR_BASE
      for d in "$base"/w*; do
        [ -d "$d" ] || continue
        git -C "$STRESS_REPO" worktree remove --force "$d"
      done
      git -C "$STRESS_REPO" worktree prune
      ;;
    *) echo "stress-regressions: unknown builtin step $step" >&2; exit 2 ;;
  esac
  exit 0
fi

# ---------------------------------------------------------------------------
# Arguments.
# ---------------------------------------------------------------------------
repo="" phase="" rounds="" jobs="" out="" items_file="" round_timeout=1800
declare -a want_items=()
while [ $# -gt 0 ]; do
  case "$1" in
    --repo) repo=${2:-}; shift 2 || usage "--repo needs a value" ;;
    --phase) phase=${2:-}; shift 2 || usage "--phase needs a value" ;;
    --rounds) rounds=${2:-}; shift 2 || usage "--rounds needs a value" ;;
    --jobs) jobs=${2:-}; shift 2 || usage "--jobs needs a value" ;;
    --out) out=${2:-}; shift 2 || usage "--out needs a value" ;;
    --item) want_items+=("${2:-}"); shift 2 || usage "--item needs a value" ;;
    --items-file) items_file=${2:-}; shift 2 || usage "--items-file needs a value" ;;
    --round-timeout) round_timeout=${2:-}; shift 2 || usage "--round-timeout needs a value" ;;
    -h|--help) sed -n '2,/^set -u$/p' "$self" | sed '$d'; exit 0 ;;
    *) usage "unknown argument $1" ;;
  esac
done
[ -n "$repo" ] && [ -d "$repo" ] || usage "--repo must name a directory"
case "$phase" in before|after) ;; *) usage "--phase must be before or after" ;; esac
case "$rounds" in ''|*[!0-9]*|0) usage "--rounds must be a positive integer" ;; esac
case "$jobs" in ''|*[!0-9]*|0) usage "--jobs must be a positive integer" ;; esac
case "$round_timeout" in ''|*[!0-9]*|0) usage "--round-timeout must be a positive integer" ;; esac
[ -n "$out" ] || usage "--out is required"
repo=$(cd "$repo" && pwd)
mkdir -p "$out" || usage "cannot create --out $out"
out=$(cd "$out" && pwd)

# ---------------------------------------------------------------------------
# Items.
# ---------------------------------------------------------------------------
declare -A IT_PREP=() IT_CMD=() IT_PASS=() IT_SIG=() IT_COUNT=() IT_DETAIL=() IT_CLEAN=()
declare -a known_items=()

add_item() { # name prepare cmd pass sig count detail cleanup
  known_items+=("$1")
  IT_PREP[$1]=$2 IT_CMD[$1]=$3 IT_PASS[$1]=$4 IT_SIG[$1]=$5 IT_COUNT[$1]=$6 IT_DETAIL[$1]=$7 IT_CLEAN[$1]=$8
}

if [ -n "$items_file" ]; then
  [ -f "$items_file" ] || usage "--items-file $items_file does not exist"
  while IFS=$'\t' read -r f_name f_prep f_cmd f_pass f_sig f_extra; do
    case "$f_name" in ''|'#'*) continue ;; esac
    [ -n "$f_sig" ] || usage "--items-file: item $f_name has fewer than 5 fields"
    count=0
    case "${f_extra:-}" in '') ;; count_lines_only) count=1 ;; *) usage "--items-file: item $f_name: unknown sixth field $f_extra" ;; esac
    add_item "$f_name" "$f_prep" "$f_cmd" "$f_pass" "$f_sig" "$count" "" ""
  done <"$items_file"
else
  b="bash \"\$STRESS_SELF\" __builtin"
  add_item s12 "$b s12-prepare" "$b s12-round" '^PASS S12 \(' \
    "FAIL S12 the one-shot login terminal attaches: .*the login terminal never showed the command's output" 1 "" "$b s12-cleanup"
  add_item w224 "$b go-prepare" "$b go-round TestW224FailedCallWalksToTheNextCandidate" \
    '^--- PASS: TestW224FailedCallWalksToTheNextCandidate ' 'no summary is held|no summarize fact ever landed' 0 "" ""
  add_item w348rr "$b go-prepare" "$b go-round TestW348RoundRobinServesTheOtherCellBeforeTheBusyOne" \
    '^--- PASS: TestW348RoundRobinServesTheOtherCellBeforeTheBusyOne ' 'round-robin did not serve the idle Cell next' 0 "" ""
  add_item w348wait "$b go-prepare" "$b go-round TestW348WaitingJobFreesItsSlot" \
    '^--- PASS: TestW348WaitingJobFreesItsSlot ' '^--- FAIL: TestW348WaitingJobFreesItsSlot' 0 'w348_cell_e2e_acceptance_test\.go:[0-9]+:' ""
  add_item f12 "$b f12-prepare" "$b f12-round" \
    '^--- PASS: TestShutdownRestartFullChainPushIdempotency ' 'TempDir RemoveAll cleanup: unlinkat .*/data: directory not empty' 0 "" ""
fi

declare -a run_items=()
if [ ${#want_items[@]} -eq 0 ]; then
  if [ -n "$items_file" ]; then
    run_items=("${known_items[@]}")
  else
    run_items=(s12 w224 w348rr w348wait)
  fi
else
  for it in "${want_items[@]}"; do
    [ -n "${IT_CMD[$it]+x}" ] || usage "unknown item $it (known: ${known_items[*]})"
    run_items+=("$it")
  done
fi

# ---------------------------------------------------------------------------
# Environment: pin the tool caches, drop the caller's pf identity.
# ---------------------------------------------------------------------------
if command -v go >/dev/null 2>&1; then
  : "${GOCACHE:=$(go env GOCACHE 2>/dev/null)}"
  : "${GOPATH:=$(go env GOPATH 2>/dev/null)}"
  export GOCACHE GOPATH
fi
: "${npm_config_cache:=${HOME:-/root}/.npm}"
: "${PLAYWRIGHT_BROWSERS_PATH:=${XDG_CACHE_HOME:-${HOME:-/root}/.cache}/ms-playwright}"
export npm_config_cache PLAYWRIGHT_BROWSERS_PATH
for v in $(compgen -e); do
  case "$v" in PF_*) unset "$v" ;; esac
done
export STRESS_SELF="$self" STRESS_REPO="$repo" STRESS_OUT="$out" STRESS_PHASE="$phase" STRESS_BIN="$out/bin" STRESS_JOBS="$jobs"

mkdir -p "$out/logs" "$out/home" "$out/tmp" "$out/work" "$out/bin" "$out/state"
manifest=$out/manifest.tsv
summary=$out/summary.txt
printf 'phase\titem\tround\tworker\tstart\tend\texit\tpass\tsignature\tother\tloadavg\tlog\n' >"$manifest"
: >"$summary"

loadavg() { cut -d' ' -f1-3 /proc/loadavg 2>/dev/null || echo "?"; }
now() { date +%s.%3N; }

{
  echo "head: $(git -C "$repo" rev-parse HEAD 2>&1)"
  echo "repo: $repo"
  echo "argv: $argv_text"
  echo "nproc: $(nproc 2>&1)"
  echo "cpu.max: $(cat /sys/fs/cgroup/cpu.max 2>&1)"
  echo "memory.max: $(cat /sys/fs/cgroup/memory.max 2>&1)"
  echo "container: $([ -f /.dockerenv ] && echo yes || echo no)"
  echo "image: ${STRESS_IMAGE:-}"
  echo "go: $(go version 2>&1)"
  echo "node: $(node --version 2>&1)"
  echo "GOCACHE=${GOCACHE:-} GOPATH=${GOPATH:-} npm_config_cache=$npm_config_cache PLAYWRIGHT_BROWSERS_PATH=$PLAYWRIGHT_BROWSERS_PATH"
  echo "start: $(date -u +%Y-%m-%dT%H:%M:%SZ) loadavg $(loadavg)"
  echo "free -m:"
  free -m 2>&1
} >"$out/env.txt"

# ---------------------------------------------------------------------------
# Rounds.
# ---------------------------------------------------------------------------
declare -a slot_pid=() slot_round=() slot_t0=() slot_start=()
cur_item=""

kill_group() { # pgid
  kill -TERM -- "-$1" 2>/dev/null || return 0
  for _ in $(seq 1 25); do
    kill -0 -- "-$1" 2>/dev/null || return 0
    sleep 0.2
  done
  kill -KILL -- "-$1" 2>/dev/null
}

on_interrupt() {
  local k
  for k in "${!slot_pid[@]}"; do
    [ -n "${slot_pid[$k]}" ] && kill_group "${slot_pid[$k]}"
  done
  if [ -n "$cur_item" ] && [ -n "${IT_CLEAN[$cur_item]}" ]; then
    STRESS_WORKDIR_BASE="$out/work/$cur_item" bash -c "${IT_CLEAN[$cur_item]}" >>"$out/logs/$cur_item-cleanup.log" 2>&1
  fi
  echo "stress-regressions: interrupted" | tee -a "$summary"
  echo "stress-regressions: $phase FAILED" | tee -a "$summary"
  exit 130
}
trap on_interrupt INT TERM

start_round() { # item round worker
  local item=$1 r=$2 k=$3
  local home="$out/home/$item-$r" tmp="$out/tmp/$item-$r" wd="$out/work/$item/w$k"
  mkdir -p "$home/.config" "$home/.cache" "$home/.local/share" "$home/.local/state" "$tmp" "$wd"
  rm -f "$out/state/$item-$r.rc"
  (
    cd "$wd" || exit 1
    exec env HOME="$home" TMPDIR="$tmp" \
      XDG_CONFIG_HOME="$home/.config" XDG_CACHE_HOME="$home/.cache" \
      XDG_DATA_HOME="$home/.local/share" XDG_STATE_HOME="$home/.local/state" \
      STRESS_ITEM="$item" STRESS_ROUND="$r" STRESS_WORKER="w$k" STRESS_WORKDIR="$wd" \
      setsid bash -c "${IT_CMD[$item]}"
  ) >"$out/logs/$item-$r.log" 2>&1 &
  slot_pid[$k]=$!
  slot_round[$k]=$r
  slot_t0[$k]=$SECONDS
  slot_start[$k]=$(now)
}

# Per-item tallies, filled by finish_round.
declare -a hit_rounds=() fail_rounds=()
passes=0

finish_round() { # item worker exit
  local item=$1 k=$2 rc=$3
  local r=${slot_round[$k]} log="$out/logs/$item-${slot_round[$k]}.log"
  local pass=0 sig=0 other="-" fails
  grep -Eq -- "${IT_PASS[$item]}" "$log" && pass=1
  grep -Eq -- "${IT_SIG[$item]}" "$log" && sig=1
  fails=$(grep -E '^(--- )?FAIL' "$log" | grep -Evc -- "${IT_SIG[$item]}")
  if [ "$rc" -ne 0 ] || [ "$fails" -gt 0 ]; then
    other="exit=$rc,fail_lines=$fails"
    [ "$rc" -eq 124 ] && other="$other,timeout"
  fi
  printf '%s\t%s\t%s\tw%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "$phase" "$item" "$r" "$k" \
    "${slot_start[$k]}" "$(now)" "$rc" "$pass" "$sig" "$other" "$(loadavg)" "$log" >>"$manifest"
  local ok=0
  if [ "$pass" -eq 1 ] && [ "$sig" -eq 0 ] && { [ "${IT_COUNT[$item]}" -eq 1 ] || [ "$rc" -eq 0 ]; }; then
    ok=1
  fi
  [ "$sig" -eq 1 ] && hit_rounds+=("$r")
  if [ "$ok" -eq 1 ]; then
    passes=$((passes + 1))
  else
    fail_rounds+=("$r")
  fi
  echo "stress-regressions: $phase $item round $r (w$k): exit $rc pass $pass signature $sig other $other"
  slot_pid[$k]=""
}

phase_ok=1

run_item() { # item
  local item=$1 k r next=1 stop=0 running rc
  cur_item=$item
  hit_rounds=() fail_rounds=() passes=0
  slot_pid=() slot_round=() slot_t0=() slot_start=()
  mkdir -p "$out/work/$item"
  echo "stress-regressions: $phase $item: prepare"
  if ! STRESS_WORKDIR_BASE="$out/work/$item" timeout --kill-after=30 3600 bash -c "${IT_PREP[$item]}" >"$out/logs/$item-prepare.log" 2>&1; then
    echo "$item prepare failed (logs/$item-prepare.log)" | tee -a "$summary"
    phase_ok=0
    cleanup_item "$item"
    return
  fi
  while :; do
    for k in $(seq 1 "$jobs"); do
      if [ -z "${slot_pid[$k]:-}" ] && [ "$stop" -eq 0 ] && [ "$next" -le "$rounds" ]; then
        start_round "$item" "$next" "$k"
        next=$((next + 1))
      fi
    done
    running=0
    for k in $(seq 1 "$jobs"); do
      [ -n "${slot_pid[$k]:-}" ] && running=$((running + 1))
    done
    [ "$running" -eq 0 ] && break
    sleep 0.2
    for k in $(seq 1 "$jobs"); do
      [ -n "${slot_pid[$k]:-}" ] || continue
      if ! kill -0 "${slot_pid[$k]}" 2>/dev/null; then
        wait "${slot_pid[$k]}"
        rc=$?
        finish_round "$item" "$k" "$rc"
      elif [ $((SECONDS - slot_t0[$k])) -ge "$round_timeout" ]; then
        echo "stress-regressions: $phase $item round ${slot_round[$k]} exceeded ${round_timeout}s; killing its process group"
        kill_group "${slot_pid[$k]}"
        wait "${slot_pid[$k]}"
        finish_round "$item" "$k" 124
      fi
    done
    # before: a round that showed the signature ends the item's dispatch;
    # the rounds in flight are still collected.
    if [ "$phase" = before ] && [ ${#hit_rounds[@]} -gt 0 ]; then
      stop=1
    fi
  done
  cleanup_item "$item"
  local ran=$((next - 1)) first=""
  if [ ${#hit_rounds[@]} -gt 0 ]; then
    first=$(printf '%s\n' "${hit_rounds[@]}" | sort -n | head -n 1)
  fi
  echo "$item rounds=$ran pass=$passes signature_rounds=${hit_rounds[*]:-none} failed_rounds=${fail_rounds[*]:-none}" >>"$summary"
  if [ "$phase" = before ]; then
    if [ -n "$first" ]; then
      local detail_re=${IT_DETAIL[$item]:-${IT_SIG[$item]}} line
      line=$(grep -Em1 -- "$detail_re" "$out/logs/$item-$first.log")
      echo "$item reproduced round $first (logs/$item-$first.log): $line" | tee -a "$summary"
    else
      echo "$item not reproduced in $ran rounds" | tee -a "$summary"
      phase_ok=0
    fi
  else
    if [ "$passes" -eq "$rounds" ] && [ "$ran" -eq "$rounds" ]; then
      echo "$item ok $passes/$rounds" | tee -a "$summary"
    else
      echo "$item failed: $passes/$rounds passed, failed rounds ${fail_rounds[*]:-none}" | tee -a "$summary"
      phase_ok=0
    fi
  fi
  cur_item=""
}

cleanup_item() { # item
  [ -n "${IT_CLEAN[$1]}" ] || return 0
  STRESS_WORKDIR_BASE="$out/work/$1" bash -c "${IT_CLEAN[$1]}" >"$out/logs/$1-cleanup.log" 2>&1 ||
    echo "stress-regressions: $1 cleanup failed (logs/$1-cleanup.log)"
}

for it in "${run_items[@]}"; do
  run_item "$it"
done

echo "end: $(date -u +%Y-%m-%dT%H:%M:%SZ) loadavg $(loadavg)" >>"$out/env.txt"
if [ "$phase_ok" -eq 1 ]; then
  echo "stress-regressions: $phase ok" | tee -a "$summary"
  exit 0
fi
echo "stress-regressions: $phase FAILED" | tee -a "$summary"
exit 1
