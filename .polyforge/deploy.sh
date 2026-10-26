#!/bin/sh
# polyforge's deploy face (polyforge#123): the project-owned counterpart to
# .polyforge/release.sh.
#
# Contract (the one sentence that matters):
#
#     A deploy of an already released and verified version is run by an agent
#     (the release workflow's deploy step) or a human, and only ever with an
#     explicit `--confirm <version>` equal to the version (never "latest", no
#     prompt, no --yes). It takes a server-side lock for the whole mutating
#     part and ends with production on the new version, or - if anything fails
#     after the swap - rolled back to the previous one (exit 10). A failed
#     deploy is redeployed by running this script again with the same
#     version, never by cutting a new release of the same commit.
#
# Scope: the aihub SERVICE HOST only. Work machines are out of scope (they
# re-run tools/install.sh, one at a time - printed at the end).
#
# What it does: promote an already-released version onto the aihub docker
# service. The release (release.sh) publishes the aihub image to the PRIVATE
# GHCR repo ghcr.io/silgrid/aihub:<tag> plus the 8-asset release matrix
# (polyforge#218: aihub/pf × linux-amd64, darwin-arm64, darwin-amd64,
# aihub-image.digest, SHA256SUMS covering them all). The deployer consumes the
# linux pair and the digest file; its checksum check verifies exactly those
# (the darwin assets belong to work machines, tools/install.sh). This
# script runs on the DEPLOYER machine (which holds the GitHub / GHCR
# credentials), pulls that image, verifies it, and ships it to the server with
# `docker save | ssh docker load`: the server never holds GitHub credentials
# and never builds the release image. Downtime is SECONDS: every backup step
# runs ONLINE (the service keeps running); the new image is loaded next to the
# running container; only the SWITCH WINDOW interrupts service: the old
# container stops (graceful drain), the version's UPGRADE SQL is applied
# (deploy/sql/<TAG>/{upgrade,rollback,probe}.sql fetched from the target git
# tag, probe-guarded run-once, ONE transaction per script), and the new
# container starts. Images are never built by compose,
# so a recreate can no longer silently revert the binary to an older image.
#
#   0 preflight   tag, release assets + SHA256SUMS (covers the binaries and
#                 aihub-image.digest), docker pull ghcr.io/silgrid/aihub:<tag> on
#                 this machine, repo digest == aihub-image.digest, /aihub -version,
#                 ssh, container, compose file, versions, disk space; the
#                 upgrade-SQL plan: server python3 SQLite >= 3.35 gate, then the
#                 structural probe per version in (old, tag] with a deploy/sql dir
#                 (read-only; the probes decide what the window will apply)
#   1 baseline    read-only: integrity_check, table row counts, file counts, sizes
#   2 save old binary   docker cp <container>:/aihub -> <root>/aihub-bin-<old>-<TS>;
#                       rollback image polyforge/aihub:<old>: if the old release has
#                       a GHCR image, ship it like the new one; otherwise (releases
#                       before the GHCR image existed, e.g. v2.2.0) build it on the
#                       server from the saved old binary
#   3 online backup     rsync memory/ git/ (pass 1) -> sqlite backup-API snapshot
#                       of aihub.db -> rsync catch-up; snapshot integrity + counts
#   4 consistency       every active memories row in the snapshot carries its content or
#                       has its memory.md in the backup (a snapshot predating
#                       memories.content: rows == memory.md files)
#   5 off-host          rsync backup to BACKUP_ROOT, manifests equal, integrity + counts
#                       then mark it .verified (both sides) and keep the newest 3 verified
#                       aihub-backup-<ver>-<TS> dirs (this one included) on the server and
#                       locally, prune older verified ones; unmarked ones are never pruned
#   6 load + swap       docker save | ssh docker load (service keeps running), verify
#                       the image ID on the server == the verified local one, tag it
#                       polyforge/aihub:<tag>, back up and edit ONLY the aihub service
#                       of the compose file (image, no build:, stop_grace_period),
#                       compose config -q, then the SWITCH WINDOW: compose stop
#                       $AIHUB_SERVICE (drain <= DRAIN_SECS) -> apply the upgrade SQL
#                       of every not-yet-applied version in (old, tag] (ascending,
#                       one transaction each) -> compose up -d --no-deps aihub ->
#                       poll HTTP, print downtime
#   7 verify      image + version, logs panic/fatal, counts >= baseline, pf smoke
#   8 rollback    concrete commands printed; final summary
#
# Safety rails:
#   * Confirmation is a mandatory flag, never a prompt: --confirm <version> must
#     equal the positional version (refused BEFORE any network work otherwise).
#   * Server-side deploy lock (see "Deploy lock" below): two deploys can not
#     mutate the server at once; held from step 2 through verification/rollback,
#     and the lock session is checked before every long step and before the swap.
#   * AUTO-ROLLBACK: if anything from the switch window on fails (the upgrade
#     SQL included), the script first runs the rollback.sql of every version
#     whose upgrade SQL was applied in THIS run (descending version order, one
#     transaction each) - the old binary must never see the post-upgrade
#     schema - then points the compose file back at the previous image,
#     recreates the service and verifies that the OLD version answers. A
#     FAILED upgrade script was rolled back by its own transaction, so nothing
#     of it is applied and no rollback SQL runs for it. Data restore stays
#     manual (it needs a stop) - the commands are printed. When the SQL run
#     itself errors (e.g. the ssh connection drops), the version's probe is
#     re-run: if it reports the version applied, its rollback.sql runs too.
#   * The SQL plan reads the LOCAL git tags: the target tag must exist locally
#     (refused in step 0 otherwise - `git fetch --tags`).
#
# Usage:
#   .polyforge/deploy.sh <version> --confirm <version> [--dry-run] [--force]
#     --confirm  REQUIRED for a real deploy; must equal <version>. There is
#                deliberately no prompt and no --yes.
#     --dry-run  steps 0-1 run for real (strictly read-only on the server),
#                steps 2-8 are printed as a plan; nothing is changed, no lock
#                is taken. --confirm is not required (if given it must match).
#     --force    allow re-deploying the running version / a downgrade. A
#                downgrade across a deploy/sql version (a dir in (<version>,
#                running]) is refused even with --force: no rollback SQL runs
#                for a downgrade - roll the schema back by hand.
#
# Exit codes (the last stdout line is machine-readable on 0 / 10 / 11):
#   0   deployed.            deployed=<version> previous=<old>
#   1   refused, preflight failure, or any failure BEFORE the swap: nothing on
#       the running service changed
#   2   usage error
#   3   deploy lock held by another deploy: nothing changed
#   10  deploy failed AFTER the swap and was ROLLED BACK: production runs the
#       old version again.   deployed=ROLLED_BACK previous=<old> attempted=<version>
#   11  deploy failed AND the rollback failed: production state is uncertain;
#       loud manual instructions are printed.  deployed=UNKNOWN previous=<old> attempted=<version>
#
# Deploy lock: `flock -n` on the server (DEPLOY_LOCK_FILE, required) taken
# through a long-lived background ssh session that runs
# `flock -n <file> sh -c 'echo LOCKED; exec cat'`. The lock lives exactly as
# long as that remote `cat` has an open stdin. If this script is killed or
# the ssh client dies, sshd closes the pipe, cat exits and the kernel drops
# the flock: no pid file to clean up. On a network PARTITION the
# server only notices when its TCP keepalive / sshd timeout fires, so the lock
# can outlive this script for a while (fail-safe: it blocks other deploys, it
# never lets two run). The reverse case - the lock session dying while this
# script keeps running (ssh killed, connection lost; the client notices within
# ServerAliveInterval 15s x ServerAliveCountMax 4) - is detected: the session
# is checked (`kill -0`) before each long step and right before the swap, and
# the deploy dies ("deploy lock session died") instead of running unlocked. If
# it is noticed after the swap, the rollback still runs and reports it.
#
# Environment, required (no defaults - the deployment's topology is not in
# this repo). Set them in the environment or in the operator-owned, untracked
# ~/.config/polyforge/deploy.env (shell assignments, read when the file exists;
# a variable the environment already set wins over the file). Any one missing
# is a usage error (exit 2, the missing NAMES listed) before any network work,
# --dry-run included. Placeholder example:
#   AIHUB_HOST=root@<aihub-host>                      (ssh target of the server)
#   AIHUB_DATA=/<path>/aihub                          (aihub data dir on the server)
#   COMPOSE_FILE=/<path>/aihub-app/docker-compose.yml (on the server)
#   COMPOSE_PROJECT=<compose-project>
#   DEPLOY_LOCK_FILE=/<path>/aihub-app/.deploy.lock   (on the server)
# Environment (defaults):
#   AIHUB_CONTAINER=aihub   BACKUP_ROOT=/data/polyforge-backups (local)
#   GH_REPO=silgrid/polyforge   GHCR_IMAGE=ghcr.io/silgrid/aihub   AIHUB_PORT=8080   WAIT_SECS=60
#   AIHUB_SERVICE=aihub
#   DRAIN_SECS=15 (stop_grace_period; > aihub's own 10s drain)   MAX_DOWNTIME_SECS=20 (warn; > DRAIN_SECS)   UP_WAIT_SECS=120 (fail;
#   covers aihub's one-time memory content migration at its first start)
#   LOCK_WAIT_SECS=20
set -eu

USAGE="usage: .polyforge/deploy.sh <version> --confirm <version> [--dry-run] [--force]"
TAG=""
DRY=""
FORCE=""
CONFIRM=""
CONFIRM_SET=""
while [ $# -gt 0 ]; do
  a="$1"; shift
  case "$a" in
    --dry-run) DRY=1 ;;
    --force) FORCE=1 ;;
    --confirm)
      if [ $# -lt 1 ]; then echo "deploy: --confirm needs a value (the version again)" >&2; echo "$USAGE" >&2; exit 2; fi
      CONFIRM="$1"; CONFIRM_SET=1; shift ;;
    --confirm=*) CONFIRM="${a#--confirm=}"; CONFIRM_SET=1 ;;
    --yes|-y) echo "deploy: --yes is not allowed - confirm with --confirm <version>" >&2; exit 2 ;;
    -*) echo "deploy: unknown flag $a (use --confirm <version>, --dry-run, --force)" >&2; exit 2 ;;
    *)
      if [ -n "$TAG" ]; then echo "deploy: more than one version given" >&2; exit 2; fi
      TAG="$a" ;;
  esac
done
if [ -z "$TAG" ]; then
  echo "$USAGE" >&2
  exit 2
fi
case "$TAG" in
  v[0-9]*.[0-9]*.[0-9]*) ;;
  *) echo "deploy: tag must look like v<major>.<minor>.<patch> (got \"$TAG\")" >&2; exit 2 ;;
esac
case "$TAG" in
  *[!A-Za-z0-9._-]*) echo "deploy: tag has unexpected characters (got \"$TAG\")" >&2; exit 2 ;;
esac
# The confirmation gate: a deploy happens because a person typed the version
# twice, never because an automation decided to. Checked before ANY network work.
if [ -n "$CONFIRM_SET" ] && [ "$CONFIRM" != "$TAG" ]; then
  echo "deploy: REFUSED - --confirm \"$CONFIRM\" does not equal the version \"$TAG\". Nothing was touched." >&2
  echo "$USAGE" >&2
  exit 1
fi
if [ -z "$DRY" ] && [ -z "$CONFIRM_SET" ]; then
  echo "deploy: REFUSED - a real deploy needs --confirm $TAG (no prompt, no --yes). Nothing was touched." >&2
  echo "$USAGE" >&2
  exit 1
fi

# The deployment's topology is operator-owned, never in this repo: the five
# required variables come from the environment or from DEPLOY_ENV (shell
# assignments, read when the file exists); a variable the environment already
# set wins over the file. Checked before ANY network work, --dry-run included.
DEPLOY_ENV="${HOME:-}/.config/polyforge/deploy.env"
if [ -n "${HOME:-}" ] && [ -f "$DEPLOY_ENV" ]; then
  ENV_SNAPSHOT="$(export -p)"
  set -a
  . "$DEPLOY_ENV"
  set +a
  eval "$ENV_SNAPSHOT"   # the environment wins over the file
fi
MISSING=""
for v in AIHUB_HOST AIHUB_DATA COMPOSE_FILE COMPOSE_PROJECT DEPLOY_LOCK_FILE; do
  eval "[ -n \"\${$v:-}\" ]" || MISSING="$MISSING $v"
done
if [ -n "$MISSING" ]; then
  echo "deploy: missing required variable(s):$MISSING - set them in the environment or in $DEPLOY_ENV (see this script's header). Nothing was touched." >&2
  exit 2
fi
AIHUB_CONTAINER="${AIHUB_CONTAINER:-aihub}"
BACKUP_ROOT="${BACKUP_ROOT:-/data/polyforge-backups}"
GH_REPO="${GH_REPO:-silgrid/polyforge}"
AIHUB_PORT="${AIHUB_PORT:-8080}"
WAIT_SECS="${WAIT_SECS:-60}"
POLL_SECS="${POLL_SECS:-2}"
DRAIN_SECS="${DRAIN_SECS:-15}"
MAX_DOWNTIME_SECS="${MAX_DOWNTIME_SECS:-20}"
UP_WAIT_SECS="${UP_WAIT_SECS:-120}"
LOCK_WAIT_SECS="${LOCK_WAIT_SECS:-20}"
AIHUB_SERVICE="${AIHUB_SERVICE:-aihub}"
IMAGE_REPO="${IMAGE_REPO:-polyforge/aihub}"
GHCR_IMAGE="${GHCR_IMAGE:-ghcr.io/silgrid/aihub}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
SRV_ROOT="$(dirname "$AIHUB_DATA")"   # where the server-side backups/binaries live
# Remote commands carry these paths through ssh's re-joined command line:
# plain paths only (no blanks, quotes or shell metacharacters).
case "$AIHUB_DATA$BACKUP_ROOT" in
  *[!A-Za-z0-9._/+@:,%=-]*) echo "deploy: AIHUB_DATA ($AIHUB_DATA) and BACKUP_ROOT ($BACKUP_ROOT) must be plain paths (letters, digits, . _ / + - @ : , % =)" >&2; exit 2 ;;
esac
KEY_TABLES="work_items memories projects users api_keys machines"
ASSET_AIHUB="aihub-linux-amd64"
ASSET_PF="pf-linux-amd64"
ASSET_DIGEST="aihub-image.digest"
TS="$(date -u +%Y%m%dT%H%M%SZ)"

WORK="$(mktemp -d "${TMPDIR:-/tmp}/pf-deploy.XXXXXX")"
SWAPPED=""   # the switch window was entered (the service is interrupted)
ROLLING=""   # the automatic rollback is running
FINISHED=""  # the deploy completed and verified
LOCK_PID=""
STEP="0"
OLDVER=""; OLDIMG=""; NEWIMG=""; COMPOSE_BAK=""; DOCKERFILE=""; BIN_SAVED=""; SRV_BACKUP=""; LOCAL_BACKUP=""
NEW_LOCAL_ID=""; OLD_LOCAL_ID=""; OLD_HAS_GHCR=""
SQLVERS=""        # release tags in (OLDVER, TAG] carrying deploy/sql/<tag>/, ascending
SQLTODO=""        # the subset whose probe reports not-yet-applied (applied in the window)
APPLIED_VERS=""   # versions whose upgrade.sql was applied THIS run (rollback.sql targets)
SQLSRV=""         # server-side staging dir of the SQL files (filled before the window)

rssh() { ssh -o BatchMode=yes -o ConnectTimeout=10 "$AIHUB_HOST" "$@"; }
# check_lock [restore-compose]: the lock session (background ssh) must still be
# alive; otherwise another deploy could be running unlocked.
check_lock() {
  if [ -n "$LOCK_PID" ] && ! kill -0 "$LOCK_PID" 2>/dev/null; then
    if [ -n "${1:-}" ]; then
      rssh "cp -p '$COMPOSE_BAK' '$COMPOSE_FILE'" || echo "deploy: COULD NOT restore $COMPOSE_BAK -> $COMPOSE_FILE, do it by hand" >&2
    fi
    die "deploy lock session died (ssh to $AIHUB_HOST dropped): refusing to continue without the lock"
  fi
}
dk() { rssh docker "$@"; }
step() { STEP="$1"; printf '\n== step %s: %s ==\n' "$1" "$2"; }
note() { printf '   %s\n' "$*"; }

# release_lock: closing the fifo ends the remote `cat`; the kill is the belt
# to that pair of braces. Idempotent.
release_lock() {
  if [ -n "$LOCK_PID" ]; then
    exec 7>&-
    kill "$LOCK_PID" 2>/dev/null || true
    wait "$LOCK_PID" 2>/dev/null || true
    LOCK_PID=""
  fi
}
cleanup() { release_lock; rm -rf "$WORK"; }
# on_exit: any unexpected exit (set -e, signal) after the swap still rolls back.
on_exit() {
  rc=$?
  if [ -n "$SWAPPED" ] && [ -z "$FINISHED" ] && [ -z "$ROLLING" ]; then
    echo "deploy: unexpected exit (status $rc) after the switch window began - rolling back" >&2
    # auto_rollback always exits: run it in a subshell so that the cleanup below
    # (lock release, $WORK removal) still happens; its exit code (10 rolled back,
    # 11 rollback failed) is the outcome that is reported. The reason keeps
    # the REAL exit status (captured before rc is reset).
    reason="unexpected exit, status $rc"
    rc=0
    ( auto_rollback "$reason" ) || rc=$?
  fi
  cleanup
  exit "$rc"
}
trap on_exit EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

# take_lock: see "Deploy lock" in the header. Exit 3 when held, 1 on any other failure.
take_lock() {
  mkfifo "$WORK/lock.in" || die "mkfifo failed"
  exec 7<>"$WORK/lock.in"   # we hold the write side: EOF for the remote cat only when we die
  : >"$WORK/lock.out"
  # fd 7 is closed for the ssh child so that only THIS script keeps the pipe open.
  ssh -o BatchMode=yes -o ConnectTimeout=10 -o ServerAliveInterval=15 -o ServerAliveCountMax=4 "$AIHUB_HOST" \
    "flock -n '$DEPLOY_LOCK_FILE' sh -c 'echo LOCKED; exec cat'; rc=\$?; [ \$rc -eq 1 ] && echo LOCK-BUSY; exit \$rc" \
    <"$WORK/lock.in" >"$WORK/lock.out" 2>&1 7>&- &
  LOCK_PID=$!
  lw=0
  while :; do
    grep -q '^LOCKED' "$WORK/lock.out" && return 0
    if ! kill -0 "$LOCK_PID" 2>/dev/null; then
      grep -q '^LOCKED' "$WORK/lock.out" && return 0
      break
    fi
    if [ "$lw" -ge $((LOCK_WAIT_SECS * 5)) ]; then break; fi
    sleep 0.2; lw=$((lw + 1))
  done
  if grep -q '^LOCK-BUSY' "$WORK/lock.out"; then
    release_lock
    printf '\ndeploy: LOCK HELD - another deploy holds %s on %s. Nothing was changed.\n' "$DEPLOY_LOCK_FILE" "$AIHUB_HOST" >&2
    exit 3
  fi
  out="$(head -3 "$WORK/lock.out" | tr '\n' ' ')"
  release_lock
  die "could not take the deploy lock $DEPLOY_LOCK_FILE on $AIHUB_HOST (flock missing? no answer in ${LOCK_WAIT_SECS}s?): $out"
}

rollback_text() {
  cat <<EOF
  # point the aihub service back at the rollback image $OLDIMG (prepared in step 2:
  # the old GHCR image, or built from the saved old binary) and recreate it
  # (seconds of downtime):
  ssh $AIHUB_HOST "sed -i 's|image: $NEWIMG|image: $OLDIMG|' $COMPOSE_FILE"
  ssh $AIHUB_HOST docker compose -f $COMPOSE_FILE -p $COMPOSE_PROJECT up -d --no-deps $AIHUB_SERVICE
  ssh $AIHUB_HOST docker exec $AIHUB_CONTAINER /aihub -version   # must print $OLDVER
  # (the pre-deploy compose file is kept as $COMPOSE_BAK, but its image/build keys
  #  point at the stale v1.0.0 image - do NOT use it to roll back the binary)
  # the schema: if upgrade SQL was applied this run, deploy.sh already ran the
  # rollback.sql of every applied version (descending, one transaction each)
  # BEFORE recreating the old image - the old binary must never see the
  # post-upgrade schema; if THAT step failed, run the same files by hand,
  # newest version first, before starting the old version (see the lines
  # printed after this block when they apply)
  # OPTIONAL, ONLY if the data is damaged (this needs a STOP = real downtime; the backup's
  # aihub.db is the consistent snapshot):
  ssh $AIHUB_HOST "docker stop $AIHUB_CONTAINER && mv $AIHUB_DATA $AIHUB_DATA.failed-$TS && cp -a $SRV_BACKUP $AIHUB_DATA && docker start $AIHUB_CONTAINER"
EOF
  if [ -n "$APPLIED_VERS" ]; then
    desc="$(printf '%s\n' "$APPLIED_VERS" | awk 'NF {a[++n] = $0} END {for (i = n; i >= 1; i--) print a[i]}')"
    for v in $desc; do
      if [ -n "$SQLSRV" ]; then
        echo "  # rollback SQL of $v (if it did not run yet), newest first, ONE transaction against $AIHUB_DATA/aihub.db: staged at $AIHUB_HOST:$SQLSRV/$v-rollback.sql"
      else
        echo "  # rollback SQL of $v (if it did not run yet), newest first, ONE transaction against $AIHUB_DATA/aihub.db: git show $v:deploy/sql/$v/rollback.sql"
      fi
    done
  fi
}

# rollback_failed_report: loud manual instructions when the automatic rollback failed.
rollback_failed_report() {
  cat >&2 <<EOT

#############################################################################
#  ROLLBACK FAILED - production state is UNCERTAIN.  ACT BY HAND, NOW.      #
#############################################################################
The deploy of $TAG failed after the swap AND the automatic rollback to $OLDVER failed.
Manual rollback commands (check each result):
EOT
  rollback_text >&2
  echo "deployed=UNKNOWN previous=$OLDVER attempted=$TAG"
}

die() {
  printf '\ndeploy: FAILED at step %s: %s\n' "$STEP" "$*" >&2
  if [ -n "$ROLLING" ]; then
    rollback_failed_report
    exit 11
  fi
  if [ -z "$SWAPPED" ]; then
    echo "deploy: the service was never interrupted (never stopped, never recreated); backups and images already made are left in place." >&2
    exit 1
  fi
  auto_rollback "$*"
}

# auto_rollback <reason>: compose back to the previous image, recreate, verify
# that the OLD version answers (same wait_version as the new one). Never returns:
# exit 10 = rolled back, 11 = rollback failed (die handles that).
auto_rollback() {
  ROLLING=1; STEP="auto-rollback"
  if [ -n "$LOCK_PID" ] && ! kill -0 "$LOCK_PID" 2>/dev/null; then
    echo "deploy: WARNING: the deploy lock session died - another deploy may be running; rolling back anyway" >&2
  fi
  {
    echo
    echo "deploy: !!! the deploy FAILED in the switch window: $1"
    echo "deploy: !!! AUTOMATIC ROLLBACK to $OLDVER ($OLDIMG) starting"
  } >&2
  # the rollback SQL of every version applied this run runs FIRST (descending,
  # one transaction each): the old binary must never see the post-upgrade
  # schema. A failed upgrade script applied nothing, so when nothing was
  # applied this run this is exactly today's rollback.
  sql_rollback_phase
  rssh "sed -i 's|image: $NEWIMG|image: $OLDIMG|' '$COMPOSE_FILE'" || die "rollback: could not point $COMPOSE_FILE back at $OLDIMG"
  swap_up || die "rollback: docker compose up of $OLDIMG failed or no HTTP answer within ${UP_WAIT_SECS}s (\"$down\")"
  img_now="$(dk inspect -f '{{.Config.Image}}' "$AIHUB_CONTAINER" 2>/dev/null || true)"
  [ "$img_now" = "$OLDIMG" ] || die "rollback: container image is \"$img_now\", expected $OLDIMG"
  wait_version "$OLDVER" || die "rollback: after ${WAIT_SECS}s container running=$LAST_RUN, version=\"$LAST_VER\" (wanted $OLDVER)"
  sql_unstage
  {
    echo
    echo "#############################################################################"
    echo "#  ROLLED BACK: the deploy of $TAG failed; production runs $OLDVER again."
    echo "#############################################################################"
    echo "reason: $1"
    echo "verified: container image $OLDIMG, version $OLDVER answers, HTTP up (${down}s)"
    if [ -n "$APPLIED_VERS" ]; then
      echo "schema: rolled back by the rollback.sql of $(printf '%s ' $APPLIED_VERS)(applied this run, descending order)"
    else
      echo "schema: untouched by the rollback (no upgrade SQL was applied this run)"
    fi
    echo "data: untouched by the rollback; backups at $AIHUB_HOST:$SRV_BACKUP and $LOCAL_BACKUP."
    echo "only if the DATA is damaged (needs a STOP = real downtime), by hand:"
    echo "  ssh $AIHUB_HOST \"docker stop $AIHUB_CONTAINER && mv $AIHUB_DATA $AIHUB_DATA.failed-$TS && cp -a $SRV_BACKUP $AIHUB_DATA && docker start $AIHUB_CONTAINER\""
  } >&2
  echo "deployed=ROLLED_BACK previous=$OLDVER attempted=$TAG"
  exit 10
}

# vcmp A B -> -1 / 0 / 1 comparing vMAJOR.MINOR.PATCH numerically.
vcmp() {
  awk -v a="$1" -v b="$2" 'BEGIN {
    sub(/^v/, "", a); sub(/^v/, "", b); sub(/[-+].*/, "", a); sub(/[-+].*/, "", b)
    n = split(a, x, "."); m = split(b, y, ".")
    for (i = 1; i <= 3; i++) { p = x[i] + 0; q = y[i] + 0
      if (p < q) { print -1; exit } if (p > q) { print 1; exit } }
    print 0 }'
}

# ---- upgrade SQL (deploy/sql/<TAG>/{upgrade,rollback,probe}.sql) ----
# Sourced per version from that version's git tag, with a working-tree
# fallback, exactly like the legacy Dockerfile fetch. One directory per
# release tag; no dir -> that release has no schema change -> the window is
# exactly today's (no SQL step).

# sql_candidate_versions: release tags strictly between the running version
# (exclusive) and the target (inclusive), ascending. A same-version
# re-deploy (--force) considers the target itself - the probe decides, so a
# re-deploy of an already-upgraded version is a no-op for the SQL.
sql_candidate_versions() {
  if [ "$OLDVER" = "$TAG" ]; then printf '%s\n' "$TAG"; return 0; fi
  sql_tags_between "$OLDVER" "$TAG"
}

# sql_tags_between <lo> <hi>: the LOCAL release tags in (lo, hi], ascending.
# Only the target tag must exist locally (checked in step 0); lo is just a
# numeric bound, so a running version whose tag is absent locally is fine.
sql_tags_between() {
  git -C "$SCRIPT_DIR" tag -l 'v*' | awk -v old="${1#v}" -v new="${2#v}" '
    function num(s,  a) { sub(/[-+].*/, "", s); split(s, a, "."); return a[1] * 1000000 + a[2] * 1000 + a[3] + 0 }
    { v = $0; sub(/^v/, "", v)
      if (v !~ /^[0-9]+\.[0-9]+\.[0-9]+/) next
      n = num(v)
      if (n > num(old) && n <= num(new)) printf "%012d %s\n", n, v }' \
    | sort -n | awk '{print "v" $2}'
}

# sql_fetch <ver> <file> <dest>: copy the version's SQL file into <dest>
# from its git tag, falling back to the working tree. Returns 1 when the
# version has no such file (no dir -> no schema change for it).
sql_fetch() {
  sv="$1"; sf="$2"; dst="$3"
  if git -C "$SCRIPT_DIR" show "$sv:deploy/sql/$sv/$sf" >"$dst" 2>/dev/null && [ -s "$dst" ]; then
    return 0
  fi
  if [ -s "$SCRIPT_DIR/../deploy/sql/$sv/$sf" ]; then
    note "NOTE: deploy/sql/$sv/$sf is not in tag $sv - taken from the WORKING TREE ($SCRIPT_DIR/../deploy/sql/$sv/$sf)"
    cp "$SCRIPT_DIR/../deploy/sql/$sv/$sf" "$dst"
    return 0
  fi
  rm -f "$dst"
  return 1
}

# sql_version_gate: the server's python3 must link a SQLite >= 3.35 (the
# first release with ALTER TABLE ... DROP COLUMN). Refused in preflight,
# before the window, nothing changed. Runs whenever any version in range
# ships SQL (even when only intermediate versions do).
sql_version_gate() {
  ver="$(printf 'import sqlite3\nprint(sqlite3.sqlite_version)\n' | rssh python3 - 2>/dev/null || true)"
  n="$(printf '%s\n' "$ver" | awk '{split($1, a, "."); print a[1] * 1000000 + a[2] * 1000 + a[3] + 0}')"
  [ "$n" -ge 3035000 ] || die "the python3 on $AIHUB_HOST links SQLite \"$ver\" - 3.35 or newer is required (DROP COLUMN) for the upgrade SQL; refusing BEFORE the window (nothing was changed)"
  note "server python3 sqlite3: $ver (>= 3.35: DROP COLUMN supported)"
}

# The upgrade/rollback SQL runner (python3, stdlib sqlite3 - the same
# pattern as the sqlite probes; the sqlite3 CLI is NOT used). One script =
# ONE transaction: a failure rolls the whole script back, so a failed
# script applies nothing. The SQL file must already sit on the server
# (staged in $SQLSRV before the window).
# shellcheck disable=SC2016
PYRUNSQL='
import sqlite3, sys
db, path = sys.argv[1], sys.argv[2]
sql = open(path).read()
stmts, buf = [], ""
for line in sql.splitlines(True):
    buf += line
    if sqlite3.complete_statement(buf):
        if buf.strip():
            stmts.append(buf)
        buf = ""
if buf.strip():
    sys.exit("incomplete final SQL statement in " + path)
if not stmts:
    sys.exit("no SQL statements in " + path)
conn = sqlite3.connect(db, timeout=30, isolation_level=None)
try:
    conn.execute("BEGIN IMMEDIATE")
    for s in stmts:
        conn.execute(s)
    conn.commit()
except Exception as e:
    try:
        conn.execute("ROLLBACK")
    except Exception:
        pass
    print("SQL-FAILED:", e)
    sys.exit(1)
print("SQL-OK %d statements" % len(stmts))
'

# The probe runner (read-only: the live db is opened mode=ro while the
# service still runs). Prints "PROBE <value>": the probe.sql contract is
# that its FINAL statement answers one value - 0 = already applied (skip
# the version), nonzero = apply it. No bookkeeping table is written.
# shellcheck disable=SC2016
PYPROBE='
import sqlite3, sys
db, path = sys.argv[1], sys.argv[2]
sql = open(path).read()
stmts, buf = [], ""
for line in sql.splitlines(True):
    buf += line
    if sqlite3.complete_statement(buf):
        if buf.strip():
            stmts.append(buf)
        buf = ""
if buf.strip():
    sys.exit("incomplete final SQL statement in " + path)
if not stmts:
    sys.exit("no SQL statements in " + path)
conn = sqlite3.connect("file:" + db + "?mode=ro", uri=True, timeout=30)
val = None
for s in stmts:
    val = conn.execute(s).fetchone()
if not val or val[0] is None:
    sys.exit("probe returned no value: " + path)
print("PROBE", val[0])
'

# run_sql <remote-file>: apply one SQL script as ONE transaction against
# the live db (called only inside the window or the rollback, when the
# container is stopped).
run_sql() {
  printf '%s\n' "$PYRUNSQL" | rssh python3 - "$AIHUB_DATA/aihub.db" "$1"
}

# sql_plan (step 0, read-only on the db): fetch every candidate version's
# SQL files, run the >= 3.35 gate, stage the files on the server and probe
# which versions are still un-applied. Sets SQLVERS/SQLTODO/SQLSRV. In
# dry-run nothing is staged (strictly read-only): only the candidates are
# printed; the probes run at deploy time.
sql_plan() {
  for v in $(sql_candidate_versions); do
    if sql_fetch "$v" upgrade.sql "$WORK/sql-$v-upgrade.sql"; then
      sql_fetch "$v" rollback.sql "$WORK/sql-$v-rollback.sql" \
        || die "deploy/sql/$v/ has upgrade.sql but no rollback.sql - incomplete SQL dir, refusing (nothing was changed)"
      sql_fetch "$v" probe.sql "$WORK/sql-$v-probe.sql" \
        || die "deploy/sql/$v/ has upgrade.sql but no probe.sql - incomplete SQL dir, refusing (nothing was changed)"
      SQLVERS="$SQLVERS$v
"
    fi
  done
  [ -n "$SQLVERS" ] || { note "upgrade SQL: none (no deploy/sql dir in ($OLDVER, $TAG]) - the window is exactly today's"; return 0; }
  sql_version_gate
  if [ -n "$DRY" ]; then
    note "upgrade SQL candidates (dry-run - the probes run at deploy time): $(printf '%s ' $SQLVERS)"
    return 0
  fi
  SQLSRV="$SRV_ROOT/aihub-sql-$TS"
  rssh "mkdir -p '$SQLSRV'" || die "cannot create the SQL staging dir $SQLSRV on $AIHUB_HOST"
  for v in $SQLVERS; do
    for f in upgrade rollback probe; do
      scp -o BatchMode=yes "$WORK/sql-$v-$f.sql" "$AIHUB_HOST:$SQLSRV/$v-$f.sql" >"$WORK/scpsql-$v-$f.log" 2>&1 \
        || die "scp of deploy/sql/$v/$f.sql to $AIHUB_HOST failed: $(tail -2 "$WORK/scpsql-$v-$f.log")"
    done
  done
  for v in $SQLVERS; do
    out="$(sql_probe "$v")" \
      || die "probe of deploy/sql/$v/probe.sql failed: $out"
    case "$out" in
      "PROBE 0")
        note "upgrade SQL $v: already applied (probe) - skipped" ;;
      "PROBE "[1-9]*)
        note "upgrade SQL $v: not yet applied (probe: $(printf '%s' "$out" | sed 's/^PROBE //')) - runs in the window"
        SQLTODO="$SQLTODO$v
" ;;
      *)
        die "probe of deploy/sql/$v/probe.sql answered \"$out\" - refusing (nothing was changed)" ;;
    esac
  done
}

# sql_probe <ver>: run the version's staged probe.sql (read-only); prints
# "PROBE <n>" (0 = applied).
sql_probe() {
  printf '%s\n' "$PYPROBE" | rssh python3 - "$AIHUB_DATA/aihub.db" "$SQLSRV/$1-probe.sql" 2>&1
}

# sql_unstage: remove the server-side SQL staging dir once the window is
# over (success, or a completed rollback). Best effort.
sql_unstage() {
  [ -n "$SQLSRV" ] || return 0
  rssh "rm -rf '$SQLSRV'" || note "WARNING: could not remove the SQL staging dir $AIHUB_HOST:$SQLSRV"
  SQLSRV=""
}

# apply_upgrade_sql (inside the window): run the pending versions'
# upgrade.sql in ascending order, one transaction each; a failure dies into
# the auto-rollback (a failed script applied nothing of its own; versions
# applied EARLIER this run are rolled back by their rollback.sql).
apply_upgrade_sql() {
  for v in $SQLTODO; do
    note "upgrade SQL: applying deploy/sql/$v/upgrade.sql (one transaction)"
    if ! run_sql "$SQLSRV/$v-upgrade.sql" >"$WORK/sql-$v.log" 2>&1 || ! grep -q '^SQL-OK' "$WORK/sql-$v.log"; then
      # the run failed or did not confirm - but the commit may have landed
      # (e.g. the ssh connection dropped after COMMIT): ask the probe. If it
      # says applied, the version is a rollback.sql target.
      if [ "$(sql_probe "$v")" = "PROBE 0" ]; then
        APPLIED_VERS="$APPLIED_VERS$v
"
        die "upgrade SQL of $v reported failure but the probe says it IS applied (connection lost after the commit?) - its rollback.sql runs: $(tail -3 "$WORK/sql-$v.log")"
      fi
      die "upgrade SQL of $v failed (its transaction rolled back, nothing of it applied): $(tail -3 "$WORK/sql-$v.log")"
    fi
    APPLIED_VERS="$APPLIED_VERS$v
"
    note "upgrade SQL of $v applied: $(tail -1 "$WORK/sql-$v.log")"
  done
}

# sql_rollback_phase (auto-rollback only): run the rollback.sql of every
# version applied THIS run, descending, one transaction each, BEFORE the old
# image is recreated - the old binary must never see the post-upgrade schema.
# Nothing applied this run -> no rollback SQL at all (today's rollback).
sql_rollback_phase() {
  [ -n "$APPLIED_VERS" ] || return 0
  desc="$(printf '%s\n' "$APPLIED_VERS" | awk '{a[NR] = $0} END {for (i = NR; i >= 1; i--) print a[i]}')"
  for v in $desc; do
    note "rollback SQL: applying deploy/sql/$v/rollback.sql (one transaction)"
    if ! run_sql "$SQLSRV/$v-rollback.sql" >"$WORK/rbsql-$v.log" 2>&1; then
      die "rollback SQL of $v failed: $(tail -3 "$WORK/rbsql-$v.log")"
    fi
    grep -q '^SQL-OK' "$WORK/rbsql-$v.log" || die "rollback SQL of $v did not confirm: $(tail -3 "$WORK/rbsql-$v.log")"
  done
}

# The sqlite probe (python3, stdlib only). args: <db> <ro|copy>
#   ro   : open the live db read-only (file:...?mode=ro).
#   copy : copy aihub.db(+wal/shm) to a scratch dir and open THAT (a stopped
#          db with a leftover -wal cannot be opened read-only in place).
# prints:  "integrity <result>", "count <table> <n>" per table and
#          "memcontent <n>" - memories rows without content ("none" when the
#          db predates memories.content).
PYCHK='
import os, shutil, sqlite3, sys, tempfile
db, mode = sys.argv[1], sys.argv[2]
tmp = None
if mode == "copy":
    tmp = tempfile.mkdtemp(prefix="pf-chk-")
    for suf in ("", "-wal", "-shm"):
        if os.path.exists(db + suf):
            shutil.copy2(db + suf, os.path.join(tmp, "aihub.db" + suf))
    uri = "file:" + os.path.join(tmp, "aihub.db")
else:
    uri = "file:" + db + "?mode=ro"
try:
    c = sqlite3.connect(uri, uri=True, timeout=30)
    try:
        rows = c.execute("pragma integrity_check").fetchall()
    except sqlite3.DatabaseError as e:
        print("integrity", "ERROR: %s" % e)
        sys.exit(0)
    print("integrity", ";".join(str(r[0]) for r in rows)[:300])
    names = [r[0] for r in c.execute(
        "select name from sqlite_master where type=\"table\" order by name")]
    for n in names:
        if n.startswith("sqlite_") or n.startswith("memory_fts"):
            continue
        print("count", n, c.execute("select count(*) from \"%s\"" % n.replace("\"", "")).fetchone()[0])
    if "content" in [r[1] for r in c.execute("pragma table_info(memories)")]:
        print("memcontent", c.execute("select count(*) from memories where content is null").fetchone()[0])
    else:
        print("memcontent none")
finally:
    if tmp:
        shutil.rmtree(tmp, ignore_errors=True)
'
dbprobe_remote() { printf '%s\n' "$PYCHK" | rssh python3 - "$1" "$2"; }
dbprobe_local() { printf '%s\n' "$PYCHK" | python3 -I - "$1" "$2"; }

# probe_to <remote|local> <db> <mode> <outfile>: runs the probe, requires
# integrity ok and every key table, leaves "table n" lines in <outfile>.
probe_to() {
  where="$1"; db="$2"; mode="$3"; out="$4"
  if ! "dbprobe_$where" "$db" "$mode" >"$out.raw" 2>"$out.err"; then
    die "sqlite probe of $where:$db failed: $(cat "$out.err")"
  fi
  integ="$(sed -n 's/^integrity //p' "$out.raw")"
  if [ "$integ" != "ok" ]; then
    die "pragma integrity_check on $where:$db is \"$integ\" (must be \"ok\") - refusing to deploy over a damaged database"
  fi
  sed -n 's/^count //p' "$out.raw" >"$out"
  for t in $KEY_TABLES; do
    grep -q "^$t " "$out" || die "key table $t missing in $where:$db"
  done
}

# counts_cmp <expected> <actual> <eq|ge> -> prints differences, returns 1 if bad
counts_cmp() {
  awk -v mode="$3" '
    NR == FNR { e[$1] = $2; next }
    { a[$1] = $2 }
    END {
      bad = 0
      for (t in e) {
        if (!(t in a)) { printf "   table %s missing (was %s)\n", t, e[t]; bad = 1; continue }
        if (mode == "eq" && a[t] != e[t]) { printf "   table %s: %s != %s\n", t, a[t], e[t]; bad = 1 }
        if (mode == "ge" && a[t] + 0 < e[t] + 0) { printf "   table %s LOST rows: %s < %s\n", t, a[t], e[t]; bad = 1 }
        if (mode == "ge" && a[t] + 0 > e[t] + 0) { printf "   note: table %s grew %s -> %s (live service wrote)\n", t, e[t], a[t] }
      }
      exit bad
    }' "$1" "$2"
}

show_counts() { sed 's/^\(.*\) \([0-9]*\)$/   \1 = \2/' "$1"; }

wait_version() { # wait_version <want>  -> 0 when running and version==want
  want="$1"; waited=0; got=""
  while :; do
    run="$(dk inspect -f '{{.State.Running}}' "$AIHUB_CONTAINER" 2>/dev/null || true)"
    if [ "$run" = "true" ]; then
      got="$(dk exec "$AIHUB_CONTAINER" /aihub -version 2>/dev/null | awk 'NF{v=$NF} END{print v}' || true)"
      if [ "$got" = "$want" ]; then return 0; fi
    fi
    if [ "$waited" -ge "$WAIT_SECS" ]; then
      LAST_RUN="$run"; LAST_VER="$got"; return 1
    fi
    sleep "$POLL_SECS"; waited=$((waited + POLL_SECS))
  done
}

# shellcheck disable=SC2016
# build_image <image> <binary-on-server> <expected-version>: LEGACY rollback
# path only - builds on the server in a fresh temp context (service keeps
# running), then verifies. The release image itself is never built here.
build_image() {
  img="$1"; bin="$2"; want="$3"
  [ -n "$DOCKERFILE" ] || die "no deploy/Dockerfile available for the legacy rollback image build"
  ctx="$SRV_ROOT/aihub-build-$TS-$$"
  rssh "mkdir -p '$ctx' && cp '$bin' '$ctx/aihub'" || die "cannot prepare build context $ctx"
  scp -o BatchMode=yes "$DOCKERFILE" "$AIHUB_HOST:$ctx/Dockerfile" >"$WORK/scpdf.log" 2>&1 || die "scp Dockerfile failed: $(cat "$WORK/scpdf.log")"
  if ! rssh docker build -t "$img" "$ctx" >"$WORK/build.log" 2>&1; then
    rssh "rm -rf '$ctx'" || true
    die "docker build $img failed: $(tail -8 "$WORK/build.log")"
  fi
  rssh "rm -rf '$ctx'" || true
  got="$(dk run --rm --entrypoint /aihub "$img" -version 2>/dev/null | awk 'NF{v=$NF} END{print v}' || true)"
  [ "$got" = "$want" ] || die "image $img: /aihub -version prints \"$got\", expected $want (service untouched)"
  dk run --rm --entrypoint git "$img" --version >/dev/null 2>&1 || die "image $img: git --version does not work"
  dk run --rm --entrypoint curl "$img" --version >/dev/null 2>&1 || die "image $img: curl --version does not work"
  note "image $img built; /aihub -version = $want; git and curl work"
}

# ghcr_login: log in to ghcr.io from the gh credentials unless already logged in.
ghcr_login() {
  if grep -q '"ghcr.io"' "${DOCKER_CONFIG:-$HOME/.docker}/config.json" 2>/dev/null; then return 0; fi
  gh auth token | docker login ghcr.io -u "$(gh api user -q .login)" --password-stdin >"$WORK/login.log" 2>&1 \
    || die "docker login ghcr.io failed (gh auth status?): $(tail -3 "$WORK/login.log")"
}

# fetch_release <tag> <dir> <required-digest 1|0>: download the release assets
# and verify them against SHA256SUMS (which covers the binaries and the image
# digest file). Prints nothing; sets nothing; dies on any problem.
fetch_release() {
  ft="$1"; fd="$2"; need="$3"
  mkdir -p "$fd"
  gh release download "$ft" -R "$GH_REPO" -D "$fd" -p "$ASSET_AIHUB" -p "$ASSET_PF" -p "$ASSET_DIGEST" -p SHA256SUMS >"$WORK/gh.log" 2>&1 \
    || die "gh release download $ft failed: $(cat "$WORK/gh.log")"
  for f in "$ASSET_AIHUB" "$ASSET_PF" SHA256SUMS; do
    [ -s "$fd/$f" ] || die "release $ft has no asset $f"
  done
  if [ "$need" = 1 ] && [ ! -s "$fd/$ASSET_DIGEST" ]; then
    die "release $ft has no asset $ASSET_DIGEST - it was not published with a GHCR image; nothing to deploy"
  fi
  # SHA256SUMS covers the full 8-asset matrix (the darwin binaries the
  # deployer does not consume); verify exactly the downloaded set.
  grep -E "[[:space:]]($ASSET_AIHUB|$ASSET_PF|$ASSET_DIGEST)\$" "$fd/SHA256SUMS" >"$fd/SHA256SUMS.deploy" \
    || die "release $ft: SHA256SUMS does not cover $ASSET_AIHUB/$ASSET_PF/$ASSET_DIGEST"
  (cd "$fd" && sha256sum -c SHA256SUMS.deploy) >"$WORK/sha.log" 2>&1 || die "sha256sum -c of release $ft failed: $(cat "$WORK/sha.log")"
  if [ -s "$fd/$ASSET_DIGEST" ]; then
    grep -q "[[:space:]]$ASSET_DIGEST\$" "$fd/SHA256SUMS" || die "release $ft: $ASSET_DIGEST is not covered by SHA256SUMS"
  fi
}

# pull_verified <tag> <release-dir> <want-version>: pull the GHCR image on THIS
# machine, require its repo digest == the release's aihub-image.digest and
# /aihub -version == <want-version>. Sets PULLED_ID to the local image ID.
pull_verified() {
  pt="$1"; pd="$2"; pv="$3"
  pref="$GHCR_IMAGE:$pt"
  want="$(head -1 "$pd/$ASSET_DIGEST" | tr -d ' \r')"
  # canonical format (written by release.sh): the bare digest sha256:<64 hex>;
  # the repo digest to look for is "$GHCR_IMAGE@<digest>".
  printf '%s\n' "$want" | grep -Eq '^sha256:[0-9a-f]{64}$' || die "$ASSET_DIGEST of $pt is not sha256:<64 hex> (got \"$want\")"
  wantref="$GHCR_IMAGE@$want"
  docker pull "$pref" >"$WORK/pull.log" 2>&1 || die "docker pull $pref failed: $(tail -3 "$WORK/pull.log")"
  if ! docker image inspect -f '{{range .RepoDigests}}{{println .}}{{end}}' "$pref" 2>/dev/null | grep -qxF "$wantref"; then
    die "pulled $pref has repo digests [$(docker image inspect -f '{{.RepoDigests}}' "$pref" 2>/dev/null)] but the release says $wantref - refusing (nothing on the server was touched)"
  fi
  got="$(docker run --rm --entrypoint /aihub "$pref" -version 2>/dev/null | awk 'NF{v=$NF} END{print v}' || true)"
  [ "$got" = "$pv" ] || die "image $pref: /aihub -version prints \"$got\", expected $pv (nothing on the server was touched)"
  PULLED_ID="$(docker image inspect -f '{{.Id}}' "$pref")"
  # the `docker save` tar is written under $WORK: it needs room there (L9).
  isz="$(docker image inspect -f '{{.Size}}' "$pref" 2>/dev/null || true)"
  case "$isz" in ''|*[!0-9]*) die "could not read the size of $pref (got \"$isz\")" ;; esac
  tfree="$(df -Pk "$WORK" | awk 'NR==2{print $4}')"
  case "$tfree" in ''|*[!0-9]*) die "could not read the free space of $WORK" ;; esac
  need_kb=$((isz / 1024 + isz / 10240 + 1))
  [ "$tfree" -ge "$need_kb" ] || die "not enough free space in $WORK for the docker save tar of $pref: ${tfree} KiB free, need ~${need_kb} KiB (set TMPDIR to a bigger disk)"
  note "pulled $pref: digest $want verified; /aihub -version = $pv; image $PULLED_ID"
}

# ship_image <tag> <local-image-id> <polyforge-image>: docker save | ssh docker load,
# verify the image ID on the server, tag <polyforge-image>, verify -version/git/curl.
ship_image() {
  st="$1"; sid="$2"; simg="$3"
  sref="$GHCR_IMAGE:$st"
  docker save -o "$WORK/image-$st.tar" "$sref" || die "docker save $sref failed"
  rssh docker load <"$WORK/image-$st.tar" >"$WORK/load.log" 2>&1 || die "docker load on $AIHUB_HOST failed: $(tail -3 "$WORK/load.log")"
  rm -f "$WORK/image-$st.tar"
  sgot="$(dk image inspect -f '{{.Id}}' "$sref" 2>/dev/null || true)"
  [ "$sgot" = "$sid" ] || die "image ID on the server is \"$sgot\", expected $sid (service untouched)"
  dk tag "$sref" "$simg" || die "docker tag $sref $simg failed on the server"
  dk rmi "$sref" >/dev/null 2>&1 || true   # drop the ghcr name: compose only ever sees local polyforge/aihub tags
  dk run --rm --entrypoint git "$simg" --version >/dev/null 2>&1 || die "image $simg: git --version does not work"
  dk run --rm --entrypoint curl "$simg" --version >/dev/null 2>&1 || die "image $simg: curl --version does not work"
  note "image $simg shipped; server image ID == $sid; git and curl work"
}

# The compose edit (python3 stdlib, line-based, scoped to the aihub service block):
# image -> new tag, drop build:, stop_grace_period, add the healthcheck if absent.
# mode diff = print a unified diff only; mode apply = write the file.
PYCOMPOSE='
import difflib, os, re, sys
path, svc, image, grace, mode = sys.argv[1:6]
src = open(path).read()
lines = src.split("\n")
start = None
for i, l in enumerate(lines):
    if re.match(r"^  " + re.escape(svc) + r":\s*$", l):
        start = i
        break
if start is None:
    sys.exit("service block \"  %s:\" not found (block-style yaml expected)" % svc)
end = len(lines)
for j in range(start + 1, len(lines)):
    l = lines[j]
    if l.strip() and not l.lstrip().startswith("#") and (len(l) - len(l.lstrip())) <= 2:
        end = j
        break
while end > start + 1 and not lines[end - 1].strip():
    end -= 1
block = lines[start + 1:end]
out, skip = [], False
has_hc = any(re.match(r"^    healthcheck:", b) for b in block)
for b in block:
    ind = len(b) - len(b.lstrip())
    if skip and (not b.strip() or ind > 4):
        continue
    skip = False
    if re.match(r"^    (build|image|stop_grace_period):", b):
        skip = True
        continue
    out.append(b)
head = ["    image: " + image, "    stop_grace_period: " + grace + "s"]
tail = [] if has_hc else [
    "    healthcheck:",
    "      # any HTTP answer proves the server is up (no unauthenticated health route)",
    "      test: [\"CMD\", \"curl\", \"-s\", \"-o\", \"/dev/null\", \"http://127.0.0.1:8080/\"]",
    "      interval: 30s", "      timeout: 3s", "      retries: 3"]
new = lines[:start + 1] + head + out + tail + lines[end:]
text = "\n".join(new)
if mode == "diff":
    d = "".join(difflib.unified_diff(src.splitlines(True), text.splitlines(True), path, path + " (new)"))
    sys.stdout.write(d if d else "(no change)\n")
else:
    open(path + ".tmp", "w").write(text)
    os.replace(path + ".tmp", path)
'
# window_stop: gracefully stop the old container (drain <= DRAIN_SECS) and
# answer the server-side epoch when the stop began - the downtime clock
# (old-exit -> new-HTTP covers stop + upgrade SQL + up + poll).
# shellcheck disable=SC2016
STOP_SNIP='
cf="$1"; proj="$2"; svc="$3"; grace="$4"
t0=$(date +%s.%N)
docker compose -f "$cf" -p "$proj" stop -t "$grace" "$svc" </dev/null >/dev/null 2>&1 || { echo "STOP-FAILED"; exit 3; }
awk "BEGIN{printf \"%.1f\\n\", $t0}"
'
window_stop() {
  stopped_at="$(printf '%s\n' "$STOP_SNIP" | rssh sh -s -- "$COMPOSE_FILE" "$COMPOSE_PROJECT" "$AIHUB_SERVICE" "$DRAIN_SECS")"
}

# swap_up: compose up -d --no-deps <svc> on the server, then poll HTTP; sets $down
# (seconds from the given t0 - the window start on the deploy path, the up
# itself on the rollback path - or the failure token). Used by the deploy and
# by the rollback.
# shellcheck disable=SC2016
RESTART_SNIP='
cf="$1"; proj="$2"; svc="$3"; port="$4"; limit="$5"; t0="$6"
[ -n "$t0" ] || t0=$(date +%s.%N)
docker compose -f "$cf" -p "$proj" up -d --no-deps "$svc" </dev/null >/dev/null 2>&1 || { echo "UP-FAILED"; exit 3; }
while :; do
  code=$(curl -s -m 1 -o /dev/null -w "%{http_code}" "http://127.0.0.1:$port/" </dev/null 2>/dev/null || true)
  case "$code" in [1-5][0-9][0-9]) break ;; esac
  now=$(date +%s.%N)
  if awk "BEGIN{exit !($now - $t0 > $limit)}"; then echo "NOT-UP"; exit 4; fi
  sleep 0.2
done
t1=$(date +%s.%N)
awk "BEGIN{printf \"%.1f\\n\", $t1 - $t0}"
'
swap_up() {
  down=""
  down="$(printf '%s\n' "$RESTART_SNIP" | rssh sh -s -- "$COMPOSE_FILE" "$COMPOSE_PROJECT" "$AIHUB_SERVICE" "$AIHUB_PORT" "$UP_WAIT_SECS" "${1:-}")"
}

compose_edit() { printf '%s\n' "$PYCOMPOSE" | rssh python3 - "$COMPOSE_FILE" "$AIHUB_SERVICE" "$NEWIMG" "$DRAIN_SECS" "$1"; }

# ---------------------------------------------------------------- step 0
step 0 "preflight"
note "target: $TAG on $AIHUB_HOST (container $AIHUB_CONTAINER, data $AIHUB_DATA)"
for c in gh docker ssh scp rsync python3 sha256sum; do
  command -v "$c" >/dev/null 2>&1 || die "local tool $c not found on PATH"
done

REL="$WORK/release"
note "downloading release assets of $GH_REPO $TAG ..."
fetch_release "$TAG" "$REL" 1
note "assets verified locally (SHA256SUMS covers $(awk '{print $2}' "$REL/SHA256SUMS" | tr '\n' ' '))"
ghcr_login
pull_verified "$TAG" "$REL" "$TAG"
NEW_LOCAL_ID="$PULLED_ID"
# deploy/Dockerfile is needed ONLY for the legacy rollback-image path (an old
# release without a GHCR image); the new image is never built here.
DOCKERFILE="$WORK/Dockerfile"
if git -C "$SCRIPT_DIR" show "$TAG:deploy/Dockerfile" >"$DOCKERFILE" 2>/dev/null && [ -s "$DOCKERFILE" ]; then
  :
elif [ -s "$SCRIPT_DIR/../deploy/Dockerfile" ]; then
  cp "$SCRIPT_DIR/../deploy/Dockerfile" "$DOCKERFILE"
else
  DOCKERFILE=""
fi

rssh true >/dev/null 2>&1 || die "ssh to $AIHUB_HOST failed (BatchMode)"
note "ssh $AIHUB_HOST: reachable"
# server tools (read-only): everything the later steps run over ssh.
# shellcheck disable=SC2016
missing="$(rssh 'for c in docker python3 rsync flock curl; do command -v "$c" >/dev/null 2>&1 || printf "%s " "$c"; done' 2>/dev/null || true)"
[ -z "$missing" ] || die "required tool(s) missing on $AIHUB_HOST: $missing- install them before deploying (nothing was changed)"
rssh docker compose version >/dev/null 2>&1 || die "docker compose (v2 plugin) is not available on $AIHUB_HOST (docker compose version failed)"
note "server tools present: docker (+compose) python3 rsync flock curl"

state="$(dk inspect -f '{{.State.Status}}' "$AIHUB_CONTAINER" 2>/dev/null || true)"
[ -n "$state" ] || die "container $AIHUB_CONTAINER does not exist on $AIHUB_HOST"
[ "$state" = "running" ] || die "container $AIHUB_CONTAINER is $state, not running - fix the service first, then deploy"
OLDVER="$(dk exec "$AIHUB_CONTAINER" /aihub -version 2>/dev/null | awk 'NF{v=$NF} END{print v}' || true)"
[ -n "$OLDVER" ] || die "could not read the running version (docker exec $AIHUB_CONTAINER /aihub -version)"
note "container: running, version $OLDVER -> $TAG"
case "$OLDVER" in v[0-9]*.[0-9]*.[0-9]*) ;; *) die "running version \"$OLDVER\" is not a release tag" ;; esac
cmp="$(vcmp "$TAG" "$OLDVER")"
if [ "$TAG" = "$OLDVER" ] || [ "$cmp" = "0" ]; then
  [ -n "$FORCE" ] || die "$OLDVER is already running (use --force to redeploy it)"
  note "WARNING: redeploying the running version (--force)"
elif [ "$cmp" = "-1" ]; then
  [ -n "$FORCE" ] || die "$TAG is OLDER than the running $OLDVER - a downgrade needs --force"
  # deploy.sh runs rollback SQL only inside an auto-rollback; a downgrade
  # across a schema change would start the old binary on the new schema.
  git -C "$SCRIPT_DIR" rev-parse -q --verify "refs/tags/$OLDVER^{commit}" >/dev/null \
    || die "tag $OLDVER (the running version) not in the local repo - git fetch --tags (needed to check the downgrade for schema changes)"
  for v in $(sql_tags_between "$TAG" "$OLDVER"); do
    if sql_fetch "$v" upgrade.sql "$WORK/down-$v.sql"; then
      die "REFUSED: the downgrade $OLDVER -> $TAG crosses the schema change of $v (deploy/sql/$v/) and deploy.sh does not run rollback SQL for a downgrade - roll back by hand (each crossed version's rollback.sql, newest first, with the service stopped) or use the rollback path; nothing was changed"
    fi
  done
  note "WARNING: downgrade $OLDVER -> $TAG (--force; no deploy/sql dir in ($TAG, $OLDVER])"
fi
# The SQL plan reads the LOCAL git tags: a stale clone without the target tag
# would silently skip that version's SQL. Only the TARGET tag is required (a
# first deploy from an old running version needs nothing else).
git -C "$SCRIPT_DIR" rev-parse -q --verify "refs/tags/$TAG^{commit}" >/dev/null \
  || die "tag $TAG not in the local repo - git fetch --tags"

# The upgrade-SQL plan (polyforge#154): which versions in (OLDVER, TAG] ship
# a deploy/sql dir, and - outside dry-run - which of them the structural
# probe reports as not yet applied (those run inside the switch window).
# Read-only on the db; nothing of the service is touched.
sql_plan

# The rollback image: the old release's GHCR image when it has one (a release
# that published aihub-image.digest), else the legacy build from the saved old
# binary (releases before the GHCR image existed, e.g. v2.2.0).
if gh release view "$OLDVER" -R "$GH_REPO" --json assets -q '.assets[].name' >"$WORK/oldassets.txt" 2>"$WORK/oldrel.err"; then
  if grep -qx "$ASSET_DIGEST" "$WORK/oldassets.txt"; then OLD_HAS_GHCR=1; fi
else
  note "WARNING: cannot read release $OLDVER of $GH_REPO ($(head -1 "$WORK/oldrel.err")); the rollback image falls back to a build from the saved old binary"
fi
if [ "$OLD_HAS_GHCR" = 1 ] && [ "$OLDVER" != "$TAG" ]; then
  note "rollback image: $OLDVER has a GHCR image - pulling and verifying it"
  fetch_release "$OLDVER" "$WORK/release-old" 1
  pull_verified "$OLDVER" "$WORK/release-old" "$OLDVER"
  OLD_LOCAL_ID="$PULLED_ID"
elif [ "$OLD_HAS_GHCR" = 1 ]; then
  OLD_LOCAL_ID="$NEW_LOCAL_ID"
  note "rollback image: same version as the target (--force); reusing the verified pull"
else
  note "rollback image: $OLDVER has no GHCR image - will build it from the saved old binary on the server (legacy path)"
  [ -n "$DOCKERFILE" ] || die "no deploy/Dockerfile found (neither at tag $TAG nor in the working tree) - needed for the legacy rollback image of $OLDVER"
fi

rssh "test -f '$COMPOSE_FILE'" || die "compose file $COMPOSE_FILE not found on $AIHUB_HOST"
rssh docker compose -f "$COMPOSE_FILE" -p "$COMPOSE_PROJECT" config -q >"$WORK/cfg0.log" 2>&1 || die "docker compose config -q fails on the CURRENT $COMPOSE_FILE: $(head -3 "$WORK/cfg0.log")"
note "compose: $COMPOSE_FILE (project $COMPOSE_PROJECT, service $AIHUB_SERVICE) validates"
note "images: $(rssh "docker images '$IMAGE_REPO' --format '{{.Repository}}:{{.Tag}}'" | tr '\n' ' ')"
DATA_KB="$(rssh "du -sk '$AIHUB_DATA'" | awk '{print $1}')"
FREE_KB="$(rssh "df -Pk '$SRV_ROOT'" | awk 'NR==2{print $4}')"
case "$DATA_KB$FREE_KB" in ''|*[!0-9]*) die "could not read server disk sizes (du='$DATA_KB' df='$FREE_KB')" ;; esac
note "server: data $((DATA_KB / 1024)) MiB, free on $SRV_ROOT $((FREE_KB / 1024)) MiB (need >= 2x data = $((DATA_KB * 2 / 1024)) MiB)"
[ "$FREE_KB" -ge $((DATA_KB * 2)) ] || die "not enough free space on $SRV_ROOT of the server for a full data copy (+ margin)"
LB="$BACKUP_ROOT"
while [ ! -d "$LB" ] && [ "$LB" != "/" ]; do LB="$(dirname "$LB")"; done
LFREE_KB="$(df -Pk "$LB" | awk 'NR==2{print $4}')"
note "local: free on $LB $((LFREE_KB / 1024)) MiB (need >= data = $((DATA_KB / 1024)) MiB) for $BACKUP_ROOT"
[ "$LFREE_KB" -ge "$DATA_KB" ] || die "not enough free space locally under $BACKUP_ROOT for the off-host copy"

# ---------------------------------------------------------------- step 1
step 1 "baseline (read-only)"
probe_to remote "$AIHUB_DATA/aihub.db" ro "$WORK/base.counts"
note "integrity_check: ok"
note "row counts:"
show_counts "$WORK/base.counts"
MEM_FILES="$(rssh "find '$AIHUB_DATA/memory' -type f | wc -l" | tr -d ' ')"
GIT_FILES="$(rssh "find '$AIHUB_DATA/git' -type f | wc -l" | tr -d ' ')"
note "files: memory/ $MEM_FILES, git/ $GIT_FILES"
for d in aihub.db aihub.db-wal memory git; do
  kb="$(rssh "du -sk '$AIHUB_DATA/$d' 2>/dev/null || echo 0" | awk '{print $1}')"
  note "size: $d $((${kb:-0} / 1024)) MiB"
done

# ------------------------------------------------------- plan (2-8)
BIN_SAVED="$SRV_ROOT/aihub-bin-$OLDVER-$TS"
SRV_BACKUP="$SRV_ROOT/aihub-backup-$OLDVER-$TS"
LOCAL_BACKUP="$BACKUP_ROOT/aihub-backup-$OLDVER-$TS"
BACKUP_NAME="aihub-backup-$OLDVER-$TS"

# Backup retention: the newest KEEP_BACKUPS VERIFIED backup dirs (this
# run's included) are kept on the server (SRV_ROOT) and locally
# (BACKUP_ROOT); older verified ones are pruned only after this run's
# backup passed step 5's verification. A backup is verified when it holds
# a .verified marker, written only after step 5's checks passed; a dir
# without it (a failed run, or one made before markers existed) is never
# counted and never pruned - it is listed as unverified for removal by
# hand. Only directories whose name matches the exact pattern
# aihub-backup-<version>-<YYYYMMDDTHHMMSSZ> are ever considered; age is
# the timestamp suffix, and this run's backup is never pruned.
KEEP_BACKUPS=3
BACKUP_NAME_RE='^aihub-backup-[A-Za-z0-9._+-]+-[0-9]{8}T[0-9]{6}Z$'
# one line per backup-shaped dir: "v <name>" (verified) or "u <name>"
# shellcheck disable=SC2016
LIST_BACKUPS='cd "$1" 2>/dev/null || exit 0
for d in aihub-backup-*; do
  [ -d "$d" ] && [ ! -L "$d" ] || continue
  if [ -f "$d/.verified" ]; then printf "v %s\n" "$d"; else printf "u %s\n" "$d"; fi
done
exit 0'
srv_backups() { printf '%s\n' "$LIST_BACKUPS" | rssh sh -s -- "'$SRV_ROOT'"; }
local_backups() { sh -c "$LIST_BACKUPS" sh "$BACKUP_ROOT"; }
# prune_candidates: LIST_BACKUPS lines on stdin; prints the verified ones
# beyond the newest KEEP_BACKUPS once this run's $BACKUP_NAME is counted
# in (it may not exist yet: the plan), never $BACKUP_NAME itself.
prune_candidates() {
  { awk '$1 == "v" { print $2 }'; printf '%s\n' "$BACKUP_NAME"; } | grep -E "$BACKUP_NAME_RE" | LC_ALL=C sort -u \
    | awk '{ print substr($0, length($0) - 15) " " $0 }' | LC_ALL=C sort -r \
    | awk -v keep="$KEEP_BACKUPS" -v cur="$BACKUP_NAME" 'NR > keep && $2 != cur { print $2 }'
}
# unverified_backups: LIST_BACKUPS lines on stdin; the backup-shaped dirs
# without a marker (never this run's), which retention leaves alone.
unverified_backups() {
  awk '$1 == "u" { print $2 }' | grep -E "$BACKUP_NAME_RE" | grep -vxF "$BACKUP_NAME" || true
}
words() { if [ -n "$1" ]; then printf '%s' "$1" | tr '\n' ' '; else printf 'nothing'; fi; }
PLAN_SRV_LIST="$(srv_backups)"
PLAN_LOCAL_LIST="$(local_backups)"
PLAN_SRV_PRUNE="$(printf '%s\n' "$PLAN_SRV_LIST" | prune_candidates)"
PLAN_LOCAL_PRUNE="$(printf '%s\n' "$PLAN_LOCAL_LIST" | prune_candidates)"
PLAN_SRV_UNVER="$(printf '%s\n' "$PLAN_SRV_LIST" | unverified_backups)"
PLAN_LOCAL_UNVER="$(printf '%s\n' "$PLAN_LOCAL_LIST" | unverified_backups)"
OLDIMG="$IMAGE_REPO:$OLDVER"
NEWIMG="$IMAGE_REPO:$TAG"
COMPOSE_BAK="$COMPOSE_FILE.bak-$TS"
if [ -n "$OLD_HAS_GHCR" ]; then
  OLD_PLAN="docker save $GHCR_IMAGE:$OLDVER | ssh docker load, verify image ID, tag $OLDIMG"
else
  OLD_PLAN="legacy: docker build from the saved old binary + deploy/Dockerfile (no GHCR image for $OLDVER)"
fi

echo
echo "== plan for steps 2-8 (ONLINE backup; the only interruption is the switch window: old container stop -> upgrade SQL -> new container start) =="
if [ -n "$SQLTODO" ]; then
  SQL_LINE=": $(printf '%s ' $SQLTODO)(ascending, one transaction each)"
elif [ -n "$DRY" ] && [ -n "$SQLVERS" ]; then
  SQL_LINE=": candidates $(printf '%s ' $SQLVERS)(the probes run at deploy time)"
else
  SQL_LINE=": none (no pending deploy/sql in ($OLDVER, $TAG])"
fi
cat <<EOF
2. save old binary   ssh $AIHUB_HOST docker cp $AIHUB_CONTAINER:/aihub $BIN_SAVED
                     rollback image $OLDIMG (if absent on the server): $OLD_PLAN; verify -version == $OLDVER
3. online backup     (service keeps running; NO docker stop anywhere)
                     rsync -a $AIHUB_DATA/ -> $SRV_BACKUP/   (pass 1, excluding aihub.db, -wal, -shm, service.lock, bootstrap-admin.key, .verified)
                     python3 sqlite3 backup API: $AIHUB_DATA/aihub.db (ro) -> $SRV_BACKUP/aihub.db ; integrity_check + counts >= baseline
                     rsync -a again (catch-up, files at least as new as the DB)
4. consistency       every active memories row in the snapshot carries its content or has its memory.md in the backup (otherwise stop, nothing changed);
                     a snapshot predating memories.content: its memories rows == memory.md files under $SRV_BACKUP/memory (retry up to 2x; still different -> stop)
5. off-host copy     rsync $AIHUB_HOST:$SRV_BACKUP/ -> $LOCAL_BACKUP/
                     server manifest == local manifest; integrity_check ok + row counts == frozen baseline on the local aihub.db
   retention         then mark this backup .verified on both sides and keep the newest $KEEP_BACKUPS verified aihub-backup-* dirs (this one included) on each side; would prune:
                     server $SRV_ROOT: $(words "$PLAN_SRV_PRUNE")
                     local $BACKUP_ROOT: $(words "$PLAN_LOCAL_PRUNE")
                     unverified, kept (remove by hand): server: $(words "$PLAN_SRV_UNVER") ; local: $(words "$PLAN_LOCAL_UNVER")
6. load + swap       (already verified in step 0 on this machine: $GHCR_IMAGE:$TAG repo digest == $ASSET_DIGEST, -version == $TAG)
                     docker save $GHCR_IMAGE:$TAG | ssh $AIHUB_HOST docker load  (service keeps running); server image ID == $NEW_LOCAL_ID
                     docker tag on the server -> $NEWIMG ; verify git, curl in it
                     back up $COMPOSE_FILE -> $COMPOSE_BAK ; edit ONLY service $AIHUB_SERVICE (planned diff below) ; docker compose config -q (failure -> backup restored, stop)
   switch window     docker compose stop $AIHUB_SERVICE (graceful drain <= ${DRAIN_SECS}s) -> upgrade SQL${SQL_LINE} -> docker compose -f $COMPOSE_FILE -p $COMPOSE_PROJECT up -d --no-deps $AIHUB_SERVICE ; poll http://127.0.0.1:$AIHUB_PORT/ every 0.2s ; downtime = stop until HTTP answers (warn > ${MAX_DOWNTIME_SECS}s, fail if not up in ${UP_WAIT_SECS}s)
   (a failure from the window on AUTO-ROLLS BACK: the rollback SQL of every version applied this run runs FIRST (descending, one transaction each), then the swap back to $OLDIMG; exits 10; rollback failure exits 11)
   lock               flock -n $DEPLOY_LOCK_FILE on $AIHUB_HOST held from step 2 to exit (not taken in dry-run)
7. verify            container image == $NEWIMG ; wait <= ${WAIT_SECS}s for 'docker exec $AIHUB_CONTAINER /aihub -version' == $TAG ; docker logs --since <start>: print error lines, FAIL on Go runtime panic/fatal error
                     integrity + row counts >= frozen baseline (read-only); local 'pf queue --limit 1'
8. rollback + summary; rollback commands for this run:
EOF
rollback_text
echo
echo "planned compose change (read-only preview of $COMPOSE_FILE):"
if compose_edit diff >"$WORK/plan.diff" 2>&1; then
  sed 's/^/   | /' "$WORK/plan.diff"
else
  sed 's/^/   | /' "$WORK/plan.diff" >&2
  die "cannot compute the compose edit of $COMPOSE_FILE (service $AIHUB_SERVICE): the real deploy would fail the same way. Nothing was changed."
fi
echo
echo "   work machines: re-run tools/install.sh <api-key> on each, one at a time"
echo

if [ -n "$DRY" ]; then
  echo "dry-run: steps 0-1 ran read-only (the image pull is local to this machine); steps 2-8 above were NOT executed. Nothing was changed."
  exit 0
fi

# From here on the server is mutated: take the deploy lock first (held until exit).
step lock "server-side deploy lock"
take_lock
note "lock $DEPLOY_LOCK_FILE held on $AIHUB_HOST until this script exits"
# Re-read the running version under the lock: a deploy that finished between our
# preflight and the lock would make OLDVER (and the rollback target) stale.
nowv="$(dk exec "$AIHUB_CONTAINER" /aihub -version 2>/dev/null | awk 'NF{v=$NF} END{print v}' || true)"
[ "$nowv" = "$OLDVER" ] || die "the running version changed to \"$nowv\" (was $OLDVER at preflight) - another deploy ran meanwhile; re-run"

# ---------------------------------------------------------------- step 2
step 2 "save the old binary"
check_lock
dk cp "$AIHUB_CONTAINER:/aihub" "$BIN_SAVED" >"$WORK/cp.log" 2>&1 || die "docker cp of the old binary failed: $(cat "$WORK/cp.log")"
rssh "test -s '$BIN_SAVED' && chmod +x '$BIN_SAVED'" || die "saved binary $BIN_SAVED is missing or empty"
saved_v="$(rssh "'$BIN_SAVED' -version" 2>/dev/null | awk 'NF{v=$NF} END{print v}' || true)"
[ "$saved_v" = "$OLDVER" ] || die "saved binary reports version \"$saved_v\", expected $OLDVER"
note "old binary saved: $BIN_SAVED ($OLDVER)"
if rssh docker image inspect "$OLDIMG" >/dev/null 2>&1; then
  got="$(dk run --rm --entrypoint /aihub "$OLDIMG" -version 2>/dev/null | awk 'NF{v=$NF} END{print v}' || true)"
  [ "$got" = "$OLDVER" ] || die "image $OLDIMG already exists but its /aihub reports \"$got\", not $OLDVER - a rollback to it would be wrong; remove/retag it by hand"
  note "rollback image $OLDIMG already exists and reports $OLDVER"
elif [ -n "$OLD_HAS_GHCR" ]; then
  ship_image "$OLDVER" "$OLD_LOCAL_ID" "$OLDIMG"
  got="$(dk run --rm --entrypoint /aihub "$OLDIMG" -version 2>/dev/null | awk 'NF{v=$NF} END{print v}' || true)"
  [ "$got" = "$OLDVER" ] || die "rollback image $OLDIMG reports \"$got\", expected $OLDVER"
else
  build_image "$OLDIMG" "$BIN_SAVED" "$OLDVER"
fi

# ---------------------------------------------------------------- step 3
step 3 "online backup (service keeps running)"
# sqlite online snapshot via the backup API (consistent even under WAL writes).
# shellcheck disable=SC2016
PYSNAP='
import os, sqlite3, sys
src, dst = sys.argv[1], sys.argv[2]
for suf in ("", "-wal", "-shm"):
    if os.path.exists(dst + suf):
        os.remove(dst + suf)
s = sqlite3.connect("file:" + src + "?mode=ro", uri=True, timeout=30)
d = sqlite3.connect(dst)
s.backup(d)
d.execute("pragma journal_mode=delete")
d.close()
s.close()
'
rsync_pass() {
  check_lock
  # also excluded: bootstrap-admin.key (a one-time handoff file; the key
  # lives in the DB as a hash) and .verified (a restored data dir must not
  # hand a fresh backup a marker it never earned)
  rssh rsync -a --exclude=/aihub.db --exclude=/aihub.db-wal --exclude=/aihub.db-shm --exclude=/service.lock \
    --exclude=/bootstrap-admin.key --exclude=/.verified "$AIHUB_DATA/" "$SRV_BACKUP/" >"$WORK/rs.log" 2>&1 \
    || die "rsync $AIHUB_DATA -> $SRV_BACKUP failed: $(tail -5 "$WORK/rs.log")"
}
snapshot() {
  check_lock
  printf '%s\n' "$PYSNAP" | rssh python3 - "$AIHUB_DATA/aihub.db" "$SRV_BACKUP/aihub.db" >"$WORK/snap.log" 2>&1 \
    || die "sqlite backup of aihub.db failed: $(tail -5 "$WORK/snap.log")"
  probe_to remote "$SRV_BACKUP/aihub.db" copy "$WORK/frozen.counts"
  counts_cmp "$WORK/base.counts" "$WORK/frozen.counts" ge >"$WORK/drift.txt" || { cat "$WORK/drift.txt" >&2; die "snapshot has FEWER rows than the step-1 baseline"; }
}
rssh "test ! -e '$SRV_BACKUP' && mkdir -p '$SRV_BACKUP'" || die "cannot create $SRV_BACKUP (exists?)"
rsync_pass; note "rsync pass 1 (bulk) done"
snapshot;   note "sqlite snapshot taken; snapshot integrity_check ok"
rsync_pass; note "rsync pass 2 (catch-up) done"
cat "$WORK/drift.txt"
note "snapshot row counts (frozen baseline from here):"
show_counts "$WORK/frozen.counts"

# ---------------------------------------------------------------- step 4
step 4 "consistency: memory content in the snapshot"
# Each memory's document lives in its memories row (memories.content,
# internal/aihub/memory). A row without content is one aihub fills from
# its memory file at start (a row of a database last served before
# v2.13.0, e.g. a restored old backup) - fine when the backup holds that
# file; an ACTIVE row without
# content AND without a file has no copy anywhere (aihub does not serve
# it), so the deploy stops. Redacted rows are never served: a missing file
# is fine. A snapshot that predates the column keeps the documents in
# <data>/memory/<project>/[_redacted/]<id>/memory.md files instead: then
# each row must have its file in the backup.
# shellcheck disable=SC2016
PYMEM='
import os, sqlite3, sys
db, root = sys.argv[1], sys.argv[2]
c = sqlite3.connect("file:" + db + "?mode=ro", uri=True, timeout=30)
bad = []
for mid, rel in c.execute("select id, authority_path from memories where content is null and status = \"active\""):
    head, _, tail = rel.rpartition("/")
    places = [rel, head + "/_redacted/" + tail] if rel else []
    if not any(os.path.isfile(os.path.join(root, p, "memory.md")) for p in places):
        bad.append(str(mid))
print(len(bad), " ".join(bad[:20]))
'
nocontent="$(sed -n 's/^memcontent //p' "$WORK/frozen.counts.raw")"
if [ "$nocontent" != "none" ]; then
  note "snapshot memories rows without content: $nocontent (aihub fills them from their memory files at start)"
  if [ "$nocontent" != 0 ]; then
    lost="$(printf '%s\n' "$PYMEM" | rssh python3 - "$SRV_BACKUP/aihub.db" "$SRV_BACKUP" 2>&1)" \
      || die "memory content check of the snapshot failed: $lost"
    [ "${lost%% *}" = 0 ] || die "${lost%% *} active memories rows have neither content nor a memory.md in the backup (${lost#* }) - aihub cannot fill or serve them; restore their memory.md under $AIHUB_DATA/memory from an older backup and restart aihub (nothing was changed)"
    note "every active row without content has its memory.md in the backup"
  fi
else
  tries=0
  while :; do
    rows="$(awk '$1 == "memories" {print $2}' "$WORK/frozen.counts")"
    files="$(rssh "find '$SRV_BACKUP/memory' -type f -name memory.md | wc -l" | tr -d ' ')"
    note "snapshot predates memories.content - memories rows: $rows ; memory.md files in the backup: $files"
    [ "$rows" = "$files" ] && break
    tries=$((tries + 1))
    [ "$tries" -le 2 ] || die "memories rows ($rows) != memory files ($files) after 2 retries - memory is being written during the backup or files are missing; re-run (nothing was changed)"
    note "mismatch: re-snapshot + catch-up (retry $tries)"
    snapshot; rsync_pass
  done
fi
note "consistent"

# ---------------------------------------------------------------- step 5
step 5 "off-host copy"
check_lock
# the off-host copy holds the whole data dir (team source in git/, memory/):
# a new BACKUP_ROOT is created 0700, and each copy is made 0700 below
# (rsync -a gives it the server data dir's mode)
( umask 077; mkdir -p "$BACKUP_ROOT" ) || die "cannot create $BACKUP_ROOT"
[ ! -e "$LOCAL_BACKUP" ] || die "$LOCAL_BACKUP already exists"
manifest_cmd() { printf "cd '%s' && find . -type f -exec sha256sum {} + | LC_ALL=C sort -k2" "$1"; }
rssh "$(manifest_cmd "$SRV_BACKUP")" >"$WORK/srvbak.manifest" || die "manifest of $SRV_BACKUP failed"
rsync -a -e "ssh -o BatchMode=yes" "$AIHUB_HOST:$SRV_BACKUP/" "$LOCAL_BACKUP/" >"$WORK/rsync.log" 2>&1 \
  || die "rsync to $LOCAL_BACKUP failed: $(tail -5 "$WORK/rsync.log")"
chmod 700 "$LOCAL_BACKUP" || die "cannot restrict $LOCAL_BACKUP to 0700"
(cd "$LOCAL_BACKUP" && find . -type f -exec sha256sum {} + | LC_ALL=C sort -k2) >"$WORK/local.manifest" || die "local manifest failed"
if ! cmp -s "$WORK/srvbak.manifest" "$WORK/local.manifest"; then
  diff "$WORK/srvbak.manifest" "$WORK/local.manifest" | head -20 >&2 || true
  die "local copy $LOCAL_BACKUP differs from the server backup"
fi
note "server and local manifests identical ($(wc -l <"$WORK/local.manifest" | tr -d ' ') files)"
probe_to local "$LOCAL_BACKUP/aihub.db" copy "$WORK/local.counts"
note "local aihub.db: integrity_check ok"
counts_cmp "$WORK/frozen.counts" "$WORK/local.counts" eq || die "row counts of the local copy differ from the baseline"
note "local row counts equal the frozen baseline"
# This run's backup verified on both sides: mark it, then retention. A
# failed marker write or prune warns and never fails the deploy (the
# backup is good); an unmarked backup is simply never pruned.
rssh "touch '$SRV_BACKUP/.verified'" || note "WARN: could not mark $SRV_BACKUP verified (retention will leave it alone)"
touch "$LOCAL_BACKUP/.verified" || note "WARN: could not mark $LOCAL_BACKUP verified (retention will leave it alone)"
SRV_LIST="$(srv_backups)" || SRV_LIST=""
SRV_PRUNE="$(printf '%s\n' "$SRV_LIST" | prune_candidates)"
if [ -n "$SRV_PRUNE" ]; then
  # shellcheck disable=SC2016,SC2086 # remote script in single quotes; names match BACKUP_NAME_RE: no blanks or quotes
  if printf '%s\n' 'cd "$1" && shift && rm -rf -- "$@"' | rssh sh -s -- "'$SRV_ROOT'" $SRV_PRUNE; then
    note "retention: pruned on the server ($SRV_ROOT): $(words "$SRV_PRUNE")"
  else
    note "WARN: retention: could not prune on the server ($SRV_ROOT): $(words "$SRV_PRUNE")"
  fi
else
  note "retention: nothing to prune on the server (keeping the newest $KEEP_BACKUPS verified)"
fi
LOCAL_LIST="$(local_backups)" || LOCAL_LIST=""
LOCAL_PRUNE="$(printf '%s\n' "$LOCAL_LIST" | prune_candidates)"
if [ -n "$LOCAL_PRUNE" ]; then
  # shellcheck disable=SC2086 # names match BACKUP_NAME_RE: no blanks or quotes
  if (cd "$BACKUP_ROOT" && rm -rf -- $LOCAL_PRUNE); then
    note "retention: pruned locally ($BACKUP_ROOT): $(words "$LOCAL_PRUNE")"
  else
    note "WARN: retention: could not prune locally ($BACKUP_ROOT): $(words "$LOCAL_PRUNE")"
  fi
else
  note "retention: nothing to prune locally (keeping the newest $KEEP_BACKUPS verified)"
fi
SRV_UNVER="$(printf '%s\n' "$SRV_LIST" | unverified_backups)"
LOCAL_UNVER="$(printf '%s\n' "$LOCAL_LIST" | unverified_backups)"
[ -z "$SRV_UNVER" ] || note "retention: unverified, kept; remove by hand: server ($SRV_ROOT): $(words "$SRV_UNVER")"
[ -z "$LOCAL_UNVER" ] || note "retention: unverified, kept; remove by hand: local ($BACKUP_ROOT): $(words "$LOCAL_UNVER")"

# ---------------------------------------------------------------- step 6
step 6 "load image, edit compose, run the switch window (stop -> upgrade SQL -> up)"
check_lock
ship_image "$TAG" "$NEW_LOCAL_ID" "$NEWIMG"
dk run --rm --entrypoint /aihub "$NEWIMG" -version 2>/dev/null | awk 'NF{v=$NF} END{print v}' | grep -qx "$TAG" || die "image $NEWIMG on the server does not report $TAG (service untouched)"
# (b) compose: back up, edit ONLY the aihub service, validate, show the diff.
check_lock
rssh "cp -p '$COMPOSE_FILE' '$COMPOSE_BAK'" || die "cannot back up $COMPOSE_FILE"
note "compose backup: $COMPOSE_BAK"
compose_edit diff >"$WORK/compose.diff" || die "cannot compute the compose edit: $(cat "$WORK/compose.diff")"
sed 's/^/   | /' "$WORK/compose.diff"
compose_edit apply >"$WORK/compose.log" 2>&1 || { rssh "cp -p '$COMPOSE_BAK' '$COMPOSE_FILE'" || true; die "compose edit failed (backup restored): $(cat "$WORK/compose.log")"; }
if ! rssh docker compose -f "$COMPOSE_FILE" -p "$COMPOSE_PROJECT" config -q >"$WORK/cfg.log" 2>&1; then
  rssh "cp -p '$COMPOSE_BAK' '$COMPOSE_FILE'" || echo "deploy: COULD NOT restore $COMPOSE_BAK -> $COMPOSE_FILE, do it by hand" >&2
  die "docker compose config -q rejected the edited file (backup restored, service untouched): $(head -5 "$WORK/cfg.log")"
fi
note "compose file edited and validated (docker compose config -q ok)"
# (c) the switch window - the ONLY service interruption: the old container
# stops (graceful drain), the upgrade SQL runs (if any), the new container
# starts; the downtime clock starts at the stop.
START_AT="$(rssh "date -u +%Y-%m-%dT%H:%M:%SZ")"
check_lock restore   # the last look at the lock before the window (restores the compose file when it died)
SWAPPED=1   # from here on the service is interrupted: a failure means "rollback"
window_stop || die "stopping the old container failed: \"$stopped_at\" (docker compose stop $AIHUB_SERVICE)"
apply_upgrade_sql
swap_up "$stopped_at" || die "swap did not complete: \"$down\" (docker compose up failed, or no HTTP answer within ${UP_WAIT_SECS}s of the window start)"
note "downtime: ${down}s (container stop [drain <= ${DRAIN_SECS}s] + upgrade SQL + compose up -d --no-deps $AIHUB_SERVICE, until HTTP answers)"
if awk -v d="$down" -v m="$MAX_DOWNTIME_SECS" 'BEGIN{exit !(d + 0 > m + 0)}'; then
  note "WARNING: downtime ${down}s exceeded ${MAX_DOWNTIME_SECS}s"
fi

# ---------------------------------------------------------------- step 7
step 7 "verify"
LAST_RUN=""; LAST_VER=""
img_now="$(dk inspect -f '{{.Config.Image}}' "$AIHUB_CONTAINER" 2>/dev/null || true)"
[ "$img_now" = "$NEWIMG" ] || die "container image is \"$img_now\", expected $NEWIMG"
note "container image: $img_now"
wait_version "$TAG" || die "after ${WAIT_SECS}s: container running=$LAST_RUN, version=\"$LAST_VER\" (wanted $TAG)"
note "container running, version $TAG"
dk logs --since "$START_AT" "$AIHUB_CONTAINER" >"$WORK/logs.txt" 2>&1 || true
if grep -Ei 'error' "$WORK/logs.txt" >"$WORK/logs.err"; then
  note "log lines mentioning error since $START_AT:"
  head -20 "$WORK/logs.err" | sed 's/^/     /'
else
  note "no error lines in the logs since $START_AT"
fi
# Go runtime signatures only: a bare "fatal"/"panic" substring would also match
# the git proxy forwarding git's own "fatal: ..." stderr (internal/aihub/git/proxy.go).
if grep -E '^(panic:|fatal error:|goroutine [0-9]+ \[)' "$WORK/logs.txt" >"$WORK/logs.bad"; then
  head -20 "$WORK/logs.bad" | sed 's/^/     /' >&2
  die "Go runtime panic / fatal error in the service log since the restart"
fi
probe_to remote "$AIHUB_DATA/aihub.db" ro "$WORK/after.counts"
note "integrity_check ok after the swap"
# A table the upgrade SQL applied this run drops on purpose is not a lost
# table: leave it out of the baseline (every other table still must hold).
# Statements are split on ';' after stripping -- and /* */ comments, so
# several statements per line, a statement over several lines and quoted
# names ("t", `t`, [t]) are all recognized; matching is case-insensitive.
# (FILENAME, not NR == FNR: dropped.tables is empty on most deploys.)
: >"$WORK/dropped.tables"
for v in $APPLIED_VERS; do
  awk '{ sub(/--.*$/, ""); s = s " " $0 }
       END {
         while ((i = index(s, "/*")) > 0) {
           j = index(substr(s, i + 2), "*/")
           if (j == 0) { s = substr(s, 1, i - 1); break }
           s = substr(s, 1, i - 1) " " substr(s, i + j + 3)
         }
         n = split(tolower(s), st, ";")
         for (k = 1; k <= n; k++) {
           t = st[k]; sub(/^[ \t\r]+/, "", t)
           if (t !~ /^drop[ \t\r]+table[ \t\r]/) continue
           sub(/^drop[ \t\r]+table[ \t\r]+/, "", t)
           sub(/^if[ \t\r]+exists[ \t\r]+/, "", t)
           gsub(/"/, "", t); gsub(/`/, "", t); gsub(/\[/, "", t); gsub(/\]/, "", t)
           sub(/^main\./, "", t)
           sub(/[^a-z0-9_].*$/, "", t)
           if (t != "") print t
         }
       }' "$WORK/sql-$v-upgrade.sql" >>"$WORK/dropped.tables"
done
# Only a dropped table that is really gone after the swap is left out: a
# table the SQL drops and re-creates (a rebuild) is still compared.
: >"$WORK/gone.tables"
awk -v gone="$WORK/gone.tables" '
     FILENAME == ARGV[1] { d[$1] = 1; next }
     FILENAME == ARGV[2] { a[tolower($1)] = 1; next }
     { k = tolower($1) }
     (k in d) && !(k in a) { print $1, $2 >> gone; next }
     { print }' "$WORK/dropped.tables" "$WORK/after.counts" "$WORK/frozen.counts" >"$WORK/verify.counts"
while read -r t n; do
  note "table $t dropped by the upgrade SQL (was $n) - expected"
done <"$WORK/gone.tables"
counts_cmp "$WORK/verify.counts" "$WORK/after.counts" ge || die "row counts after the swap are BELOW the baseline"
note "row counts >= baseline"
note "HTTP answered during the restart poll (any status = serving)"
if command -v pf >/dev/null 2>&1; then
  if pf queue --limit 1 >"$WORK/pf.out" 2>&1; then
    note "local 'pf queue --limit 1': ok ($(head -1 "$WORK/pf.out"))"
  else
    note "WARNING: local 'pf queue --limit 1' failed (maybe pf is not configured for this host): $(head -1 "$WORK/pf.out")"
  fi
fi

# ---------------------------------------------------------------- step 8
step 8 "rollback + summary"
sql_unstage
echo "rollback commands for this run (not executed):"
rollback_text
echo
echo "== summary =="
echo "  version:        $OLDVER -> $TAG   (downtime ${down}s)"
if [ -n "$APPLIED_VERS" ]; then
  echo "  upgrade SQL:    applied $(printf '%s ' $APPLIED_VERS)(deploy/sql/<tag>/upgrade.sql, one transaction each)"
else
  echo "  upgrade SQL:    none (no pending deploy/sql in ($OLDVER, $TAG])"
fi
echo "  old binary:     $AIHUB_HOST:$BIN_SAVED"
echo "  server backup:  $AIHUB_HOST:$SRV_BACKUP"
echo "  local backup:   $LOCAL_BACKUP"
echo "  baseline counts (snapshot; files: memory/ $MEM_FILES, git/ $GIT_FILES at step 1):"
show_counts "$WORK/frozen.counts"
echo
echo "work machines: re-run tools/install.sh <api-key> on each, one at a time"
FINISHED=1
echo "deployed=$TAG previous=$OLDVER"
