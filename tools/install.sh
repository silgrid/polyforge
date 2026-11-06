#!/bin/sh
# polyforge bootstrap — complete machine setup in one command; idempotent
# (a second run changes nothing). Steps:
#   1. prepare — check and report the prerequisites (gh, curl, python3, git;
#      never install them), pick this machine's pf asset from uname (the one
#      per-OS branch), download + SHA-verify the latest pf and stage it next to
#      the installed one ($BIN.new.<pid>) — on darwin also clear its
#      quarantine (xattr -c), ad-hoc sign it (codesign -f -s -) and prove
#      it runs (--version); clone the release tag into
#      ~/.local/share/polyforge/release/<VER>. A failure here leaves pf,
#      release/current and the running daemon untouched.
#   2. switch — hold <data-dir>/ensure.lock until step 4 (a harness's
#      `pf daemon ensure` meanwhile starts nothing). On a binary upgrade:
#      refuse while the daemon runs jobs unless PF_INSTALL_FORCE=1, shut
#      it down gracefully (its tmux restart loop ends with it), wait until
#      both are gone, swap the binary by one rename; switch the
#      release/current symlink atomically, keeping the previous version.
#   3. config + harnesses — a FIRST install writes cli.toml through
#      `pf config` (the one supported writer; an existing file is NEVER
#      rewritten); the harnesses REFERENCE release/current (polyforge#174): Claude Code
#      one ~/.claude/skills/<name> symlink per release skill (the routing
#      skill and the standalone output-format skill) + SessionStart hook;
#      pi one local package entry naming release/current (its package.json
#      declares both skills).
#   4. daemon — `pf daemon ensure` (also run by the harness adapters at
#      session start, e.g. after a reboot).
#   5. verify — pf doctor.
#   Opt-in --with-pi-web (; the default installs pf only): the
#      Pi Web package of the SAME release — silgrid-pi-web-<VER>.tgz and its
#      SHA256SUMS line from $REPO at $VER — is downloaded and checksum-
#      verified in step 1 (a missing asset or a mismatch refuses there, with
#      nothing changed), then installed with `npm install -g <tgz>` after
#      step 5 — unless the installed @silgrid/pi-web already has the
#      tarball's package version: then npm is not run (it
#      would delete the package directory a running pi-web serves) and the
#      run prints `Pi Web <ver> is already installed (<dir>); npm install -g
#      skipped`; --reinstall-pi-web (only together with --with-pi-web)
#      reinstalls it anyway, e.g. to repair a broken install, and a pi-web
#      running over that reinstall is judged stale like any other (below).
#      npm may skip the package's install scripts and still exit 0
#      (e.g. npm 11 under ignore-scripts or an explicit allowScripts denial), so
#      the installer then runs the install check the package ships,
#      `node "$(npm root -g)/@silgrid/pi-web/bin/pi-web-install-check.js"`:
#      it confirms the scripts' effects (patches applied, terminal prepared,
#      node-pty starts a pty; it rebuilds node-pty with its scripts allowed
#      when needed) and prints `ERROR: Pi Web install check failed at <step>:
#      <reason>` when it cannot. A failing npm install or install check is
#      reported and exits non-zero, and pf, the release checkout and the
#      daemon stay as installed. The equivalent standalone path is
#      `npm install -g @silgrid/pi-web` followed by that check command.
#      A pi-web already running under the deploy's restart loop (tmux session
#      PIWEB_TMUX_SESSION, default piweb, window piweb) keeps serving the old
#      tree npm just deleted: blank pages, /_next/static/* answering 5xx.
#      The installer samples what it serves before `npm install -g` and,
#      when that is not the package now installed (served == installed as
#      pi-web/deploy-piweb.sh defines it: bin/pi-web-served.js of the
#      verified tarball), prints an ACTION line with both versions and the
#      restart command; it never restarts pi-web by default:
#      a restart ends any agent turn in progress in it. With
#      --restart-pi-web (only together with --with-pi-web) it sends that
#      process SIGTERM instead and waits up to 60 s until the loop serves the
#      installed package (a timeout is reported and exits 1). The served
#      package is the working directory of pi-web's Next.js server
#, which npm deletes on every install, so a pi-web left
#      stale by an earlier run (an ignored ACTION line) or by a manual
#      `npm i -g` is detected on any later --with-pi-web run too: the ACTION
#      line, or a restart with --restart-pi-web. Its version can no longer
#      be read then and prints as `unknown`, from the deleted directory.
#
# Usage (new machine, or update — same command) — read the key with echo
# off first, so it never lands in your shell history:
#   read -rs PF_KEY
#   curl -s -H "Authorization: token $(gh auth token)" \
#     https://raw.githubusercontent.com/silgrid/polyforge/main/tools/install.sh \
#     | bash -s -- [--with-pi-web [--restart-pi-web] [--reinstall-pi-web]] "$PF_KEY" [server-host]
# The aihub server host (argument 2, else PF_HOST; no built-in default) is
# REQUIRED for a first install — the run that writes cli.toml — and refused
# before anything changes when absent; an upgrade (cli.toml with an api_key
# already there, never rewritten) needs no host.
# While the daemon runs jobs (`pf ops status`) an upgrade is refused and
# nothing changes: wait, pause them (`pf pause <wi>`), or override (their
# agent trees are terminated and recover at the next daemon start):
#   curl ... | PF_INSTALL_FORCE=1 bash -s -- "$PF_KEY" [server-host]
# A caller that runs a downloaded copy of this file (never `bash -s`, whose
# stdin is the script itself) may pass `-` as the key: the key is then read
# from stdin (one line), so it rides no process's argv — Pi Web's install
# entry does this. An upgrade needs no key, so its stdin may
# be empty; a first install refuses an empty one before anything changes.
#   bash install.sh - [server-host] <<< "$PF_KEY"
# Exit status: 0 done; 3 refused before any change, and re-running with
# PF_INSTALL_FORCE=1 lifts the refusal (running jobs, or a job list that
# could not be read); any other non-zero status is a failure.
# The release channel is PF_INSTALL_REPO (owner/name; unset or empty =
# silgrid/polyforge, the release repo every asset is published to):
# the latest release, the pf asset + SHA256SUMS, the release checkout and
# the pi entry match all come from that repo, and step 1 prints the channel
# it uses. Pass it on every run — it is not remembered:
#   curl ... | PF_INSTALL_REPO=<owner>/<name> bash -s -- "$PF_KEY" [server-host]
#
# Requires: gh authenticated (gh needs a login, even for a public repo);
# gh, curl, python3 and git on PATH (checked and reported, never
# installed); macOS ships shasum for the checksum (the installer uses
# sha256sum when it is there), and no flock — that absence already
# degrades with a WARN. On macOS also xattr and codesign (shipped with the
# OS / Command Line Tools), to clear quarantine and ad-hoc sign the pf.
set -e
WITH_PIWEB=""
RESTART_PIWEB=""
REINSTALL_PIWEB=""
while [ $# -gt 0 ]; do
  case "$1" in
    --with-pi-web) WITH_PIWEB=1; shift ;;
    --restart-pi-web) RESTART_PIWEB=1; shift ;;
    --reinstall-pi-web) REINSTALL_PIWEB=1; shift ;;
    *) break ;;
  esac
done
if [ -n "$RESTART_PIWEB" ] && [ -z "$WITH_PIWEB" ]; then
  echo "usage: install.sh [--with-pi-web [--restart-pi-web] [--reinstall-pi-web]] <api-key|-> [server-host]"
  echo "ERROR: --restart-pi-web restarts the pi-web a --with-pi-web run left serving the old install; it needs --with-pi-web. Nothing on this machine was changed."
  exit 2
fi
if [ -n "$REINSTALL_PIWEB" ] && [ -z "$WITH_PIWEB" ]; then
  echo "usage: install.sh [--with-pi-web [--restart-pi-web] [--reinstall-pi-web]] <api-key|-> [server-host]"
  echo "ERROR: --reinstall-pi-web reinstalls the Pi Web a --with-pi-web run would keep as already installed; it needs --with-pi-web. Nothing on this machine was changed."
  exit 2
fi
KEY="${1:?usage: install.sh [--with-pi-web [--restart-pi-web] [--reinstall-pi-web]] <api-key|-> [server-host]}"
if [ "$KEY" = - ]; then
  KEY=""
  IFS= read -r KEY || true
fi
HOST="${2:-${PF_HOST:-}}"
API="http://$HOST:8080"
REPO="${PF_INSTALL_REPO:-silgrid/polyforge}"
BIN="${PF_BIN:-/usr/local/bin/pf}"
CFG="$HOME/.config/polyforge/cli.toml"
# has_config: this machine already has its config (kept, never rewritten)
has_config() { [ -f "$CFG" ] && grep -q "api_key" "$CFG"; }
if [ -z "$HOST" ] && ! has_config; then
  echo "usage: install.sh [--with-pi-web [--restart-pi-web] [--reinstall-pi-web]] <api-key|-> <server-host>  (or PF_HOST=<server-host>)"
  echo "ERROR: a first install writes $CFG and needs the aihub <server-host> (argument 2 or PF_HOST); nothing on this machine was changed."
  exit 2
fi
if [ -z "$KEY" ] && ! has_config; then
  echo "ERROR: a first install writes $CFG and needs the api key (argument 1, or \`-\` with the key on stdin, which was empty); nothing on this machine was changed."
  exit 2
fi
DAEMON_DATA="$HOME/.local/share/polyforge"
DAEMON_SOCKET="$DAEMON_DATA/daemon.sock"
RELEASE_DIR="$DAEMON_DATA/release"
WORKFLOWS="$RELEASE_DIR/current/workflows"
PI_SETTINGS="${PI_CODING_AGENT_DIR:-$HOME/.pi/agent}/settings.json"
CLAUDE_SETTINGS="$HOME/.claude/settings.json"
LOOP_SESSION="polyforge-daemon"   # the tmux session of the restart loop

# tmux on the user's default server, whatever tmux this shell runs inside.
tmux_q() { env -u TMUX -u TMUX_PANE tmux "$@"; }

# Same semantics as the Go side (internal/polyforge/supervisor
# LoopRunning; daemon/ensure.go's /status probe and daemon.go's data-dir
# flock): daemon_running = the socket answers, or the daemon still holds
# its data-dir lock while quiescing.
loop_running() {
  command -v tmux >/dev/null 2>&1 && tmux_q has-session -t "=$LOOP_SESSION" 2>/dev/null
}
daemon_running() {
  status | grep -q machine_id && return 0
  if [ -f "$DAEMON_DATA/daemon.lock" ] && command -v flock >/dev/null 2>&1; then
    flock -n "$DAEMON_DATA/daemon.lock" -c true 2>/dev/null || return 0
  fi
  return 1
}

# status [timeout]: the daemon's GET /status body (what `pf ops status`
# reads); fails without a socket, on a curl error, timeout or non-200.
status() {
  [ -S "$DAEMON_SOCKET" ] && curl -sf -m "${1:-2}" --unix-socket "$DAEMON_SOCKET" http://localhost/status 2>/dev/null
}
# status_field <field|busy>: one field of the /status body on stdin; exits
# non-zero unless it is a daemon's (carries machine_id). busy: one
# "<work item> (<status>)" line per job a shutdown would interrupt — any
# status but paused (operator pause: no agent tree) and end_pending (no
# agent tree; it only retries its blocked close, at every daemon start
# too), so an unknown status counts as busy.
status_field() {
  python3 -c '
import json, sys
s = json.load(sys.stdin)
if not isinstance(s, dict) or "machine_id" not in s:
    sys.exit(2)
if sys.argv[1] != "busy":
    print(s.get(sys.argv[1]) or "")
else:
    for j in s.get("jobs") or []:
        if j.get("status") not in ("paused", "end_pending"):
            print("%s (%s)" % (j.get("slug") or j.get("work_item_id"), j.get("status")))
' "$1" 2>/dev/null
}
# ─── 1. prepare: nothing on this machine changes yet ───────────────
# Every step that can fail for an outside reason (gh, network, SHA, a
# release lacking its tree) runs here, before anything is stopped or
# replaced. A failure in this phase leaves pf, release/current and the
# running daemon exactly as they were.
echo "== 1/5 prepare (pf binary + release checkout; nothing is changed yet)"
echo "   release channel: $REPO"

STAGED=""   # the new binary, staged next to $BIN (same filesystem)
TMP=""
PIWEB_DIR=""  # --with-pi-web: the verified Pi Web tarball until it is installed
CLONE=""
STOPPED=0   # 1 from the shutdown request until step 4 starts the daemon
cleanup() {
  rc=$?
  [ -z "$TMP" ] || rm -rf "$TMP"
  [ -z "$PIWEB_DIR" ] || rm -rf "$PIWEB_DIR"
  [ -z "$STAGED" ] || rm -f "$STAGED"
  [ -z "$CLONE" ] || rm -rf "$CLONE"
  if [ "$rc" -ne 0 ] && [ "$STOPPED" = 1 ]; then
    # aborted after the daemon was asked to stop: say where the machine
    # stands (never restart from here — that is the operator's call)
    echo "ERROR: install aborted (exit $rc) AFTER the daemon was asked to shut down. State now:"
    echo "   daemon: stopped or stopping — no daemon runs until \`pf daemon ensure\` (by hand, or at the next pi / Claude Code session start)"
    echo "   pf binary $BIN: $("$BIN" --version 2>/dev/null || echo "unreadable") (upgrading $RUNNING -> $VER)"
    echo "   release/current -> $(readlink "$RELEASE_DIR/current" 2>/dev/null || echo "missing") (upgrading to $VER)"
    echo "   next: re-run this installer to finish the upgrade, or run \`pf daemon ensure\` to start the daemon as it stands; log: $DAEMON_DATA/daemon.log"
  fi
}
trap cleanup EXIT
trap 'exit 1' INT TERM HUP
UNCHANGED="nothing on this machine was changed: pf, release/current and the running daemon are as they were"
prep_fail() { echo "ERROR: $1; $UNCHANGED"; exit 1; }
# refuse <why> <details> <next> [status]: an upgrade stopped before any
# shutdown; status 3 marks a refusal PF_INSTALL_FORCE=1 lifts (default 1)
refuse() {
  echo "ERROR: $1; $UNCHANGED (the fetched release/$VER checkout is kept for the next run)."
  [ -z "$2" ] || printf '%s\n' "$2" | sed 's/^/     /'
  echo "   $3"; exit "${4:-1}"
}

# Check-and-report the prerequisites (polyforge#218): the installer NEVER
# installs anything. Each missing tool is named with what to obtain, and the
# refusal happens HERE, before one byte on this machine changes. tmux is
# deliberately NOT refused here: without it the daemon's `pf daemon ensure`
# falls back to unsupervised-with-WARNING (reported in this installer's step
# 4 output and by pf doctor's "daemon supervision" row).
MISSING=""
REQUIRED="gh curl python3 git"
[ -z "$WITH_PIWEB" ] || REQUIRED="$REQUIRED npm tar"
for t in $REQUIRED; do
  command -v "$t" >/dev/null 2>&1 || MISSING="$MISSING $t"
done
if [ -n "$MISSING" ]; then
  echo "ERROR: missing required tools:$MISSING; this installer checks and reports — it never installs anything."
  for t in $MISSING; do
    case "$t" in
      gh)      echo "   gh      - macOS: brew install gh   |  Linux: apt install gh   (then: gh auth login)" ;;
      curl)    echo "   curl    - macOS: xcode-select --install  |  Linux: apt install curl" ;;
      python3) echo "   python3 - macOS: xcode-select --install  |  Linux: apt install python3" ;;
      git)     echo "   git     - macOS: xcode-select --install  |  Linux: apt install git" ;;
      npm)     echo "   npm     - (--with-pi-web) install Node.js >= 22.19, which ships npm" ;;
      tar)     echo "   tar     - (--with-pi-web) Linux: apt install tar  |  macOS: ships with the OS" ;;
    esac
  done
  prep_fail "install the missing tool(s) above, then re-run this installer"
fi

# The one per-OS branch: which pf asset this machine runs. The release
# publishes ONE pf binary per platform — linux-amd64, darwin-arm64 and
# darwin-amd64 — and the server runs it as `pf aihub` (polyforge#218); native Windows and other platforms are out of scope
# (WSL2 is treated as Linux, and uname reports exactly that).
case "$(uname -s):$(uname -m)" in
  "Linux:x86_64")  PF_ASSET="pf-linux-amd64" ;;
  "Darwin:arm64")  PF_ASSET="pf-darwin-arm64" ;;
  "Darwin:x86_64") PF_ASSET="pf-darwin-amd64" ;;
  *) prep_fail "no pf asset for this machine ($(uname -s):$(uname -m)); the release carries pf for linux-amd64, darwin-arm64 and darwin-amd64 only (native Windows is out of scope; WSL2 is treated as Linux)" ;;
esac

VER=$(gh release list --repo "$REPO" --limit 1 --json tagName --jq '.[0].tagName' 2>/dev/null) || true
[ -n "$VER" ] || prep_fail "no release found"

RUNNING="none"
[ ! -x "$BIN" ] || RUNNING=$("$BIN" --version 2>/dev/null || echo "unknown")

# version_transition_allowed <installed> <target>: succeeds unless the target
# release is older than the installed pf. Every downgrade is refused but one:
# the named version-line cutover from the retired internal
# v2.20.x line to any release of the unified product line v0.2.x (the
# installer installs the latest release, so the exception covers the whole
# line, not only v0.2.0). A release version is vX.Y.Z or vX.Y.Z-<pre>
# (<pre>: dot-separated [0-9A-Za-z-] identifiers), ordered by SemVer 2.0
# precedence: the core numerically, a pre-release below its core release,
# pre-release identifiers one by one (numeric ones as numbers and below
# alphanumeric ones, alphanumeric ones in ASCII order, a shorter prefix
# first) — so v0.2.0-<name>.9 < v0.2.0-<name>.10 < v0.2.1-<name>.1 <
# v0.2.1. A non-release installed version (none, unknown, dev) has nothing
# to compare.
# The cutover case is rollback-safe transition code: delete it once no
# machine runs a v2.20.x pf, together with .polyforge/release.sh's
# CUTOVER_* branches and their matching exceptions in .polyforge/deploy.sh,
# internal/aihub/core/admin.go and pi-web/lib/polyforge-detect.ts.
version_transition_allowed() {
  for _v in "$1" "$2"; do
    printf '%s\n' "$_v" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$' || return 0
  done
  LC_ALL=C awk -v a="${1#v}" -v b="${2#v}" '
    function idcmp(x, y,  xn, yn) {
      xn = x ~ /^[0-9]+$/; yn = y ~ /^[0-9]+$/
      if (xn && yn) return (x + 0 < y + 0) ? -1 : (x + 0 > y + 0)
      if (xn) return -1
      if (yn) return 1
      return ((x "") < (y "")) ? -1 : ((x "") > (y ""))
    }
    function vcmp(a, b,  i, ca, cb, pa, pb, x, y, na, nb, r) {
      ca = a; pa = ""; i = index(a, "-"); if (i) { ca = substr(a, 1, i - 1); pa = substr(a, i + 1) }
      cb = b; pb = ""; i = index(b, "-"); if (i) { cb = substr(b, 1, i - 1); pb = substr(b, i + 1) }
      split(ca, x, "."); split(cb, y, ".")
      for (i = 1; i <= 3; i++) { if (x[i] + 0 < y[i] + 0) return -1; if (x[i] + 0 > y[i] + 0) return 1 }
      if (pa == pb) return 0
      if (pa == "") return 1
      if (pb == "") return -1
      na = split(pa, x, "."); nb = split(pb, y, ".")
      for (i = 1; i <= na && i <= nb; i++) { r = idcmp(x[i], y[i]); if (r) return r }
      return (na < nb) ? -1 : (na > nb)
    }
    BEGIN { exit (vcmp(b, a) < 0) }' && return 0
  case "$1:$2" in v2.20.*:v0.2.*) return 0 ;; esac
  return 1
}
version_transition_allowed "$RUNNING" "$VER" \
  || prep_fail "the latest release $VER of $REPO is older than the installed pf $RUNNING, and a downgrade is refused (the one accepted older target is a v0.2.x release over a v2.20.x pf, the named version-line cutover); this machine still runs pf $RUNNING"

if [ "$RUNNING" = "$VER" ]; then
  echo "   pf $VER already current, skipping"
else
  TMP=$(mktemp -d)
  gh release download "$VER" --repo "$REPO" --pattern "$PF_ASSET" --dir "$TMP" 2>/dev/null \
    || prep_fail "$PF_ASSET not on release $VER"
  gh release download "$VER" --repo "$REPO" --pattern "SHA256SUMS" --dir "$TMP" 2>/dev/null \
    || prep_fail "SHA256SUMS not on release $VER"
  EXP=$(grep "$PF_ASSET" "$TMP/SHA256SUMS" | awk '{print $1}')
  # sha256sum on Linux; macOS ships shasum (same `<hex>  <file>` print)
  if command -v sha256sum >/dev/null 2>&1; then
    GOT=$(sha256sum "$TMP/$PF_ASSET" | awk '{print $1}')
  else
    GOT=$(shasum -a 256 "$TMP/$PF_ASSET" 2>/dev/null | awk '{print $1}')
  fi
  [ -n "$GOT" ] || prep_fail "no sha256sum and no shasum to verify $PF_ASSET with"
  [ "$GOT" = "$EXP" ] || prep_fail "SHA mismatch on $PF_ASSET of $VER"
  # Stage the new binary in $BIN's own directory, already executable: the
  # swap in step 2 is then one rename(2) — `pf` is at every instant
  # either the complete old or the complete new binary, never a partial
  # or non-executable file (a harness hook or pf-chat may run it then).
  mkdir -p "$(dirname "$BIN")" || prep_fail "cannot create $(dirname "$BIN")"
  STAGED="$BIN.new.$$"
  { cp "$TMP/$PF_ASSET" "$STAGED" && chmod 755 "$STAGED"; } \
    || prep_fail "cannot stage the new binary at $STAGED (write access to $(dirname "$BIN")?)"
  # darwin (polyforge#237): macOS may kill a quarantined or unsigned
  # downloaded binary on first run (`Killed: 9`). Clear its extended
  # attributes, ad-hoc sign it and prove it runs — all on the STAGED file,
  # so a pf that cannot run is never swapped in. Linux never reaches this.
  case "$PF_ASSET" in pf-darwin-*)
    for t in xattr codesign; do
      command -v "$t" >/dev/null 2>&1 \
        || prep_fail "$t not on PATH; it is needed to clear quarantine and ad-hoc sign the staged pf (macOS: xcode-select --install)"
    done
    xattr -c "$STAGED" || prep_fail "xattr -c (clear quarantine) failed on the staged pf $STAGED"
    SIGN=$(codesign -f -s - "$STAGED" 2>&1) \
      || prep_fail "codesign -f -s - (ad-hoc sign) failed on the staged pf $STAGED: $SIGN"
    "$STAGED" --version >/dev/null 2>&1 \
      || prep_fail "run-proof failed: the staged pf $STAGED does not run (--version exit non-zero; killed by Gatekeeper?)"
    ;;
  esac
  rm -rf "$TMP"; TMP=""
  echo "   pf $VER downloaded, SHA-verified, staged"
fi

# --with-pi-web: the Pi Web tarball of the same release, verified now so a
# missing asset or a bad checksum refuses before anything is installed.
if [ -n "$WITH_PIWEB" ]; then
  PIWEB_TGZ="silgrid-pi-web-$VER.tgz"
  PIWEB_DIR=$(mktemp -d)
  gh release download "$VER" --repo "$REPO" --pattern "$PIWEB_TGZ" --dir "$PIWEB_DIR" 2>/dev/null \
    || prep_fail "$PIWEB_TGZ not on release $VER (--with-pi-web)"
  gh release download "$VER" --repo "$REPO" --pattern "SHA256SUMS" --dir "$PIWEB_DIR" 2>/dev/null \
    || prep_fail "SHA256SUMS not on release $VER (--with-pi-web)"
  PEXP=$(awk -v n="$PIWEB_TGZ" '$2 == n || $2 == "*" n {print $1}' "$PIWEB_DIR/SHA256SUMS")
  if command -v sha256sum >/dev/null 2>&1; then
    PGOT=$(sha256sum "$PIWEB_DIR/$PIWEB_TGZ" | awk '{print $1}')
  else
    PGOT=$(shasum -a 256 "$PIWEB_DIR/$PIWEB_TGZ" 2>/dev/null | awk '{print $1}')
  fi
  [ -n "$PEXP" ] || prep_fail "SHA256SUMS of $VER has no line for $PIWEB_TGZ (--with-pi-web)"
  [ "$PGOT" = "$PEXP" ] || prep_fail "SHA mismatch on $PIWEB_TGZ of $VER (--with-pi-web)"
  # the served == installed definition (bin/pi-web-served.js, shared with
  # pi-web/deploy-piweb.sh) comes from this verified tarball: the install
  # being replaced may predate it
  tar -xzf "$PIWEB_DIR/$PIWEB_TGZ" -C "$PIWEB_DIR" package/bin/pi-web-served.js 2>/dev/null \
    || prep_fail "$PIWEB_TGZ of $VER carries no package/bin/pi-web-served.js (--with-pi-web)"
  PIWEB_SERVED="$PIWEB_DIR/package/bin/pi-web-served.js"
  # the package version this tarball installs: an installed Pi Web of the
  # same version is kept
  tar -xzf "$PIWEB_DIR/$PIWEB_TGZ" -C "$PIWEB_DIR" package/package.json 2>/dev/null \
    || prep_fail "$PIWEB_TGZ of $VER carries no package/package.json (--with-pi-web)"
  PIWEB_WANT=$(python3 -c 'import json, sys; print(json.load(open(sys.argv[1]))["version"])' "$PIWEB_DIR/package/package.json" 2>/dev/null) || PIWEB_WANT=""
  [ -n "$PIWEB_WANT" ] || prep_fail "$PIWEB_TGZ of $VER: package/package.json names no version (--with-pi-web)"
  echo "   Pi Web $PIWEB_TGZ downloaded, SHA-verified (installed after pf)"
fi

# The installer owns a checkout of the release tag, independent of any
# harness: the daemon's workflows and every harness's skill come from it.
# A new version gets its own directory; release/current is not touched
# here (step 2 switches it).
mkdir -p "$RELEASE_DIR"
REL="$RELEASE_DIR/$VER"
if [ -f "$REL/skills/polyforge/SKILL.md" ] && [ -d "$REL/workflows" ]; then
  echo "   release checkout $VER present"
else
  CLONE="$REL.tmp.$$"
  rm -rf "$CLONE"
  # gh uses its own credentials (ssh or https), so a machine that only has
  # gh auth can clone the repo. The tag is cloned shallow.
  gh repo clone "$REPO" "$CLONE" -- --branch "$VER" --depth 1 >/dev/null 2>&1 \
    || prep_fail "cannot clone $REPO at $VER (gh auth?)"
  if [ ! -f "$CLONE/skills/polyforge/SKILL.md" ] || [ ! -d "$CLONE/workflows" ]; then
    prep_fail "$VER checkout lacks skills/polyforge or workflows"
  fi
  # the version's own directory: nothing references it until step 2 —
  # unless an incomplete checkout of this version IS release/current;
  # never remove the tree the running daemon and the harnesses use
  if [ -e "$REL" ] && [ "$(readlink "$RELEASE_DIR/current" 2>/dev/null)" = "$VER" ]; then
    prep_fail "release/current points to $REL, which is incomplete (no skills/polyforge/SKILL.md or workflows/); it is in use, so install does not replace it — inspect it, remove it and re-run"
  fi
  rm -rf "$REL"
  mv "$CLONE" "$REL"
  CLONE=""
  echo "   release checkout $VER fetched"
fi

# ─── 2. switch: stop the daemon, swap the binary, switch release ────
echo "== 2/5 switch"
# Hold the ensure lock (the flock `pf daemon ensure` takes) until step 4:
# a harness session's ensure meanwhile waits, then answers by the socket —
# no old daemon revived, no new binary against the old release.
mkdir -p "$DAEMON_DATA"
if command -v flock >/dev/null 2>&1; then
  exec 9>>"$DAEMON_DATA/ensure.lock"
  flock -w 120 9 || echo "   WARN: another pf daemon ensure held $DAEMON_DATA/ensure.lock for 120s; continuing"
else
  echo "   WARN: flock not found: a harness session starting during this upgrade could start the daemon early"
fi

if [ -n "$STAGED" ]; then
  # Stop the daemon gracefully (a clean exit also ends its restart loop)
  # and wait until daemon AND loop are gone: never a swap under a live
  # daemon, no loop left to restart the old binary (a loop without a
  # daemon is killed). The daemon's shutdown waits at most 30s (its WIP
  # pushes), hence the 30s wait.
  if daemon_running || loop_running; then
    # A graceful shutdown terminates every live agent tree: never behind
    # the back of a running job unless the operator says so. Fails
    # closed: only a missing socket means "nothing to ask" (the daemon is
    # quiescing past its listener, or only a loop is left); an unreadable
    # job list (curl error, timeout, non-200, no python3) refuses.
    BUSY="" BRC=0
    [ ! -S "$DAEMON_SOCKET" ] || BUSY=$(status 15 | status_field busy) || BRC=$?
    if [ "${PF_INSTALL_FORCE:-}" = 1 ]; then
      [ -z "$BUSY" ] || printf '   PF_INSTALL_FORCE=1: shutting down despite running jobs:\n%s\n' "$(printf '%s\n' "$BUSY" | sed 's/^/     /')"
    elif [ "$BRC" -ne 0 ]; then
      refuse "the daemon's job list could not be read (GET /status on $DAEMON_SOCKET failed, timed out or was unreadable), so whether jobs run is unknown and the upgrade to $VER is refused" "" \
        "check: pf ops status. To shut it down anyway (its live agent trees are terminated), re-run with PF_INSTALL_FORCE=1." 3
    elif [ -n "$BUSY" ]; then
      refuse "the daemon is running jobs; upgrading to $VER needs a graceful shutdown, which terminates their agent trees. Upgrade refused" "$BUSY" \
        "proceed by one of: wait until they finish (pf ops status), pause them (pf pause <wi>), or re-run with PF_INSTALL_FORCE=1 to shut them down anyway (they recover at the next daemon start)." 3
    fi
    # A job claimed between this check and the shutdown request (auto-pull,
    # a pf submit) is interrupted like a forced one and recovers at the
    # next daemon start; the window is the few ms until the request.
    echo "   stopping daemon (graceful; its restart loop ends with it)"
    # A refused/failed request (an unloadable cli.toml, a wrong socket)
    # stops nothing: fail now instead of waiting out the budget and
    # reporting a still-live daemon as stopped.
    if ! SHUT_OUT=$("$BIN" ops shutdown 2>&1) && daemon_running; then
      refuse "pf ops shutdown failed, so the daemon is still running" "$SHUT_OUT" "fix what it reports (check: pf doctor; pf ops status), then re-run this installer."
    fi
    STOPPED=1
    WAITED=0
    while daemon_running || loop_running; do
      if ! daemon_running && loop_running; then
        tmux_q kill-session -t "=$LOOP_SESSION" 2>/dev/null || true
        echo "   stopped a leftover restart loop (tmux session $LOOP_SESSION)"
      fi
      [ "$WAITED" -lt 30 ] || { echo "ERROR: daemon did not stop within 30s; binary NOT replaced. Its restart loop ended with the shutdown request."; exit 1; }
      sleep 1
      WAITED=$((WAITED + 1))
    done
    echo "   daemon stopped"
  fi
  # atomic: a rename within one directory
  mv -f "$STAGED" "$BIN" || { echo "ERROR: cannot replace $BIN"; exit 1; }
  STAGED=""
  echo "   pf $RUNNING → $VER installed"
fi

# Retired step-command links: an earlier install linked pf-run, pf-verify,
# pf-gate, pf-sync, pf-merge, pf-revert, pf-facts and pf-integrate beside pf
# (`pf-<name> -> pf`), which pf dispatched on argv[0]; the workflow's own
# scripts took their place and pf answers to no such name any more. A link
# of that name pointing at pf is removed; anything else of that name is
# the user's own and left alone. Transition code: delete this loop once
# every machine has installed a release that carries it.
BIN_DIR=$(dirname "$BIN")
BIN_NAME=$(basename "$BIN")
for SC in pf-run pf-verify pf-gate pf-sync pf-merge pf-revert pf-facts pf-integrate; do
  LINK="$BIN_DIR/$SC"
  [ -L "$LINK" ] || continue
  TARGET=$(readlink "$LINK")
  [ "$TARGET" = "$BIN_NAME" ] || [ "$TARGET" = "$BIN" ] || continue
  if rm -f "$LINK"; then
    echo "   removed the retired link $SC -> $TARGET"
  else
    echo "   WARN: cannot remove the retired link $LINK (it runs pf itself; remove it by hand)"
  fi
done

# stable symlink, swapped atomically (relative target: the tree is relocatable)
PREV=$(readlink "$RELEASE_DIR/current" 2>/dev/null || true)
if [ "$PREV" = "$VER" ]; then
  echo "   release/current -> $VER already"
else
  CUR_TMP="$RELEASE_DIR/.current.tmp.$$"
  # os.replace is rename(2) — atomic on Linux and macOS. GNU `mv -T` is not
  # portable (BSD/macOS mv has no -T), and BSD mv without -T would follow a
  # symlink-to-directory destination instead of replacing the link itself.
  # python3 is a checked prerequisite, so it is always available here.
  { ln -sfn "$VER" "$CUR_TMP" && python3 -c 'import os, sys; os.replace(sys.argv[1], sys.argv[2])' "$CUR_TMP" "$RELEASE_DIR/current"; } \
    || { rm -f "$CUR_TMP"; echo "ERROR: cannot switch $RELEASE_DIR/current to $VER"; exit 1; }
  echo "   release/current -> $VER (was ${PREV:-none})"
  # keep the new and the previous version, prune the rest
  for d in "$RELEASE_DIR"/*; do
    if [ ! -d "$d" ] || [ -L "$d" ]; then continue; fi
    n=$(basename "$d")
    [ "$n" = "$VER" ] || [ "$n" = "$PREV" ] || { rm -rf "$d"; echo "   pruned old release $n"; }
  done
fi

# ─── 3. config + harnesses ──────────────────────────────────────────
echo "== 3/5 config + harnesses"
# config: create or verify (NEVER rewritten)
mkdir -p "$(dirname "$CFG")"
if has_config; then
  echo "   config exists ($CFG)"
  if grep -q '^[[:space:]]*\[workspace' "$CFG"; then
    echo "   workspace mapping present (the daemon routes capsules by it)"
  else
    # polyforge#113: the config is the operator's — name the one command, never teach a hand edit
    echo "   ACTION: the config has no [workspace] mapping (there is no default \`project\` key): the daemon refuses to start until it has one. Add one section per workspace through \`pf config\` (\`~\` expands to your home; keys absolute, no trailing separator; the daemon creates only capsules/ inside it, and a re-added workspace keeps its capsules), then check with \`pf doctor\` and run \`pf daemon ensure\`:"
    echo "     pf config set '[workspace.\"<absolute-path-to-your-workspace>\"]' projects=your-project,another-project"
  fi
else
  # Write the config through `pf config` (the one supported writer): only
  # the flat connection keys. The installer NEVER knows or fabricates a
  # [workspace] mapping (polyforge#113): the operator / init skill adds it,
  # and the daemon refuses to start until one exists.
  "$BIN" --config "$CFG" config set server "$API" >/dev/null
  printf '%s\n' "$KEY" | "$BIN" --config "$CFG" config set api_key >/dev/null
  chmod 600 "$CFG" 2>/dev/null || true
  echo "   config written through pf config (server, api_key; no [workspace] mapping)"
  echo "   ACTION: add the [workspace] mapping, then check it and start the daemon:"
  echo "     pf config set '[workspace.\"<absolute-path-to-your-workspace>\"]' projects=your-project,another-project"
  echo "     pf doctor && pf daemon ensure"
  echo "   The daemon refuses to start (and this installer skips it) until a real mapping exists."
fi
# --- harness settings: one python edit per file. Invalid JSON or an
# unexpected shape changes nothing; a change backs the file up
# (<file>.bak-polyforge-<ts>) and rewrites it atomically, keeping its mode
# — a new file is 0600 (it may hold credentials, e.g. env.ANTHROPIC_*).
SETTINGS_PY=$(cat <<'PYEOF'
import json, os, re, shutil, sys, time
mode, path = sys.argv[1:3]
what = {"claude": "hook NOT registered", "pi": "pi package NOT set"}[mode]
cfg, existed = {}, os.path.exists(path)
if existed:
    try:
        cfg = json.load(open(path))
    except ValueError as e:
        print("   %s is not valid JSON (%s); %s" % (path, e, what)); sys.exit(1)
    if not isinstance(cfg, dict):
        print("   %s is not a JSON object; %s" % (path, what)); sys.exit(1)
def save(msg):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    if existed:
        shutil.copy2(path, "%s.bak-polyforge-%s" % (path, time.strftime("%Y%m%d%H%M%S")))
    tmp = path + ".tmp"
    if os.path.lexists(tmp):
        os.unlink(tmp)
    with os.fdopen(os.open(tmp, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600), "w") as f:
        json.dump(cfg, f, indent=2); f.write("\n")
    if existed:
        shutil.copymode(path, tmp)
    os.replace(tmp, path)
    print(msg)
if mode == "claude":  # register the SessionStart hook exactly once
    hook = sys.argv[3]
    hooks = cfg.get("hooks")
    if hooks is None:
        hooks = cfg["hooks"] = {}
    ss = hooks.get("SessionStart") if isinstance(hooks, dict) else False
    if ss is None:
        ss = hooks["SessionStart"] = []
    if not isinstance(hooks, dict) or not isinstance(ss, list):
        print("   unexpected hooks shape in %s; %s" % (path, what)); sys.exit(1)
    if any(isinstance(e, dict) and any(isinstance(h, dict) and h.get("command") == hook
           for h in (e.get("hooks") or [])) for e in ss):
        print("   claude SessionStart hook already registered"); sys.exit(0)
    ss.append({"hooks": [{"type": "command", "command": hook}]})
    save("   claude SessionStart hook registered"); sys.exit(0)
# pi: one local package entry naming release/current
repo, rel = sys.argv[3:5]
pk = cfg.get("packages")
if pk is None:
    pk = []
if not isinstance(pk, list):
    print("   unexpected packages shape in %s; %s" % (path, what)); sys.exit(1)
base, home = os.path.dirname(path), os.path.expanduser("~")
def src(e): return e if isinstance(e, str) else (e.get("source", "") if isinstance(e, dict) else "")
def local(s):
    if not s or s.startswith(("npm:", "git:", "git@", "github.com")) or "://" in s: return None
    if s == "~" or s.startswith("~/"): s = home + s[1:]
    return os.path.normpath(os.path.join(base, s))
# ours: this release, or (pre-#174 transition) a git entry of this repo
def ours(e):
    s = src(e)
    return re.search(r"github\.com[:/]" + re.escape(repo) + r"(\.git)?(@|$)", s) is not None or local(s) == os.path.normpath(rel)
idx = [i for i, e in enumerate(pk) if ours(e)]
for i, e in enumerate(pk):
    lp = local(src(e))
    if i not in idx and lp and os.path.isfile(os.path.join(lp, "skills", "polyforge", "SKILL.md")):
        print("   WARN: pi also loads another polyforge checkout (%s): remove it from %s, or pi loads the skill and pf-chat twice" % (src(e), path))
if len(idx) == 1 and src(pk[idx[0]]) == rel:
    print("   pi package -> release/current already"); sys.exit(0)
was = [src(pk[i]) for i in idx]
# an object entry keeps its other fields (filters) with the new source
entry = rel
first = pk[idx[0]] if idx else None
if isinstance(first, dict):
    entry = dict(first); entry["source"] = rel
new = [e for i, e in enumerate(pk) if i not in idx]
new.insert(idx[0] if idx else len(new), entry)
cfg["packages"] = new
save("   pi package -> %s%s (restart pi sessions to load)" % (rel, (" (replaced: " + ", ".join(was) + ")") if was else ""))
PYEOF
)
SRC_SKILLS="$RELEASE_DIR/current/skills"

# --- Claude Code (independent of pi) ---
# Every top-level skill in the release gets its own symlink to the release
# checkout: a release switch is the release/current swap above, nothing to
# copy (Claude Code follows symlinked skill dirs). The loop is generic, so
# a future skill ships without a second hardcoded block. Only the routing
# skill (polyforge) carries the pre-#174 destructive migration: it is the
# one skill that ever had a copied layout or a foreign symlink, so only it
# may be replaced in place. Any other existing entry is the user's own and
# is left untouched with a WARN — non-adopters see zero interference.
if [ -d "$HOME/.claude" ] || command -v claude >/dev/null 2>&1; then
  mkdir -p "$HOME/.claude/skills"
  for SDIR in "$SRC_SKILLS"/*/; do
    [ -f "${SDIR}SKILL.md" ] || continue
    NAME=$(basename "$SDIR")
    SRC="${SDIR%/}"
    CSK="$HOME/.claude/skills/$NAME"
    if [ -L "$CSK" ] && [ "$(readlink "$CSK")" = "$SRC" ]; then
      echo "   claude skill -> release/current already ($NAME)"
      continue
    fi
    if [ "$NAME" = polyforge ]; then
      if [ -e "$CSK" ] && [ ! -L "$CSK" ]; then
        # pre-#174 transition (delete once no machine keeps a copied
        # polyforge skill dir): moved out of skills/ first (a second skill
        # dir there would load twice), then dropped
        OLD="$HOME/.claude/.polyforge-skill-old.$$"
        rm -rf "$OLD"; mv "$CSK" "$OLD"; rm -rf "$OLD"
        echo "   claude skill: removed the old copied directory"
      fi
      rm -f "$CSK"
      ln -s "$SRC" "$CSK"
      echo "   claude skill -> $SRC"
      continue
    fi
    # not the routing skill and not this release's link: the user's own entry
    if [ -e "$CSK" ] || [ -L "$CSK" ]; then
      echo "   WARN: $CSK is not this release's link; leaving it untouched (move it aside and re-run to link $NAME)"
      continue
    fi
    ln -s "$SRC" "$CSK"
    echo "   claude skill -> $SRC"
  done
  # The SessionStart hook belongs to the routing skill (stable path).
  HOOK="$HOME/.claude/skills/polyforge/hooks/claude-session-start.sh"
  [ -x "$HOOK" ] || chmod +x "$HOOK"
  python3 -c "$SETTINGS_PY" claude "$CLAUDE_SETTINGS" "$HOOK" || echo "   WARN: could not register the claude SessionStart hook in $CLAUDE_SETTINGS"
fi

# --- pi (independent of Claude Code) ---
# One LOCAL package entry naming release/current (pi loads a local path in
# place, never copies it; the release's package.json declares the skill
# and the pf-chat extension), written straight into pi's settings: it
# replaces any git entry of this repo (pre-#174 transition: the `pi
# install git:...@<ver>` clone; delete once no machine has one) and
# duplicates; other packages are untouched.
if command -v pi >/dev/null 2>&1 || [ -d "$(dirname "$PI_SETTINGS")" ]; then
  python3 -c "$SETTINGS_PY" pi "$PI_SETTINGS" "$REPO" "$RELEASE_DIR/current" || echo "   WARN: could not set the pi package entry in $PI_SETTINGS"
fi
# Harness checks: the same judgement as `pf doctor --harness` (reads only;
# a warn row says what to do — a statusLine still running the retired
# `pf watch --statusline`, retired chat state `pf doctor --prune-chat-state`
# removes — a skip row names an absent harness; user files are never
# edited).
"$BIN" doctor --harness 2>/dev/null | sed -e '/^ok:/d' -e '/^$/d' -e 's/^/   /'

# ─── 4. daemon: started only through `pf daemon ensure` ────────────
echo "== 4/5 daemon"
# the binary and release/current are in place: let ensure run again
if command -v flock >/dev/null 2>&1; then
  flock -u 9 2>/dev/null || true
  exec 9>&-
fi
# from here on step 4 starts the daemon or says why it does not
STOPPED=0
# the fix for a live daemon without its restart loop (same as pf doctor)
ENSURE_FIX="pf ops shutdown && while pf ops status >/dev/null 2>&1 || ! flock -n '$DAEMON_DATA/daemon.lock' true; do sleep 2; done; pf daemon ensure"
if [ ! -d "$WORKFLOWS" ]; then
  echo "   SKIP: $WORKFLOWS missing (release checkout incomplete) — daemon needs workflows"
elif ST=$(status 5) && printf '%s' "$ST" | grep -q machine_id; then
  echo "   daemon already running ($(printf '%s' "$ST" | status_field machine_id))"
  # Never auto-restart here: a live daemon may be running jobs.
  RUN_WF=$(printf '%s' "$ST" | status_field workflows_dir || true)
  if [ -n "$RUN_WF" ] && [ "$RUN_WF" != "$WORKFLOWS" ]; then
    echo "   ACTION: daemon runs with -workflows-dir $RUN_WF; restart it with -workflows-dir $WORKFLOWS (pf ops shutdown, wait for jobs to drain, rerun this installer)"
  fi
  if ! loop_running; then
    echo "   ACTION: the daemon runs without its restart loop (no tmux session $LOOP_SESSION): a crash is not restarted. When no job needs it, restart it under the loop: $ENSURE_FIX"
  fi
elif ! grep -q '^[[:space:]]*\[workspace' "$CFG"; then
  # polyforge#113: no mapping — the daemon would refuse to start
  echo "   SKIP: $CFG has no [workspace] section yet — the daemon refuses to start until one exists"
  echo "   add projects (pf config set '[workspace.\"<path>\"]' projects=a,b), then: pf daemon ensure"
else
  # `pf daemon ensure` owns the launch (polyforge#174): the restart loop
  # in tmux runs the canonical argv — --config, -machine-id (recorded
  # daemon-id, else m-<short hostname>), -data-dir, -socket and
  # -workflows-dir release/current/workflows. polyforge#113: the
  # mapping-bearing config is the daemon's only workspace source and
  # carries the api_key, so neither appears in argv; the auto switch is
  # the daemon's persisted setting and the job cap comes from the
  # config's [daemon] max_concurrent_jobs.
  # The daemon's first signed heartbeat registers a new machine (any
  # project membership or a global admin qualifies); its refusals are
  # the failure-codes rows below.
  heartbeat_causes() {
    echo "         AUTH-403 no project membership: the key's user joins any project (or is an admin)"
    echo "         AUTH-403 machine_id held by another operator: start the daemon with a different --machine-id"
    echo "         AUTH-401 signature or clock: see docs/reference/failure-codes.md"
  }
  if ENSURE_OUT=$(POLYFORGE_DATA_DIR="$DAEMON_DATA" "$BIN" daemon ensure --config "$CFG" 2>&1); then
    printf '%s\n' "$ENSURE_OUT" | sed 's/^/   /'
    echo "   NOTE: the daemon's first signed heartbeat registers this machine; if it fails, daemon.log names the cause:"
    heartbeat_causes
  else
    printf '%s\n' "$ENSURE_OUT" | sed 's/^/   /'
    echo "   WARN: the daemon is not answering yet (a refused first heartbeat is one of):"
    heartbeat_causes
    echo "   check: tail $DAEMON_DATA/daemon.log"
  fi
fi
# ─── 5. verify ──────────────────────────────────────────────────────
echo "== 5/5 verify"
# pf doctor is the one health judgement (aihub key and projects, daemon
# socket, mapping, harnesses); a fail is reported, never fatal here.
"$BIN" doctor 2>&1 | sed 's/^/   /' || true
if [ -n "$WITH_PIWEB" ]; then
  echo "== Pi Web (--with-pi-web)"
  # What the deploy's restart loop (tmux session PIWEB_TMUX_SESSION, default
  # piweb, window piweb) serves, sampled BEFORE npm replaces the package
  # directory: afterwards the server's working directory is deleted and the
  # version it serves can no longer be read. "<version>|<dir>|<pid>",
  # "unknown|<dir> (deleted)|<pid>" for one already stale, "unidentified" or
  # empty.
  PIWEB_SESSION="${PIWEB_TMUX_SESSION:-piweb}"
  PIWEB_BEFORE="$(node "$PIWEB_SERVED" served "$PIWEB_SESSION" 2>/dev/null || true)"
  # The installed package, "<version>|<dir>" (empty when none): one of the
  # version this tarball installs is kept npm would delete
  # the directory a running pi-web serves — unless --reinstall-pi-web.
  PIWEB_ROOT="$(npm root -g 2>/dev/null || true)"
  PIWEB_HAVE=""
  [ -z "$PIWEB_ROOT" ] || PIWEB_HAVE="$(node "$PIWEB_SERVED" installed "$PIWEB_ROOT" 2>/dev/null || true)"
  PIWEB_INSTALLED=""
  if [ -n "$PIWEB_HAVE" ] && [ "${PIWEB_HAVE%%|*}" = "$PIWEB_WANT" ] && [ -z "$REINSTALL_PIWEB" ]; then
    echo "   Pi Web $PIWEB_WANT is already installed (${PIWEB_HAVE#*|}); npm install -g skipped (--reinstall-pi-web reinstalls it)"
  else
    if ! npm install -g "$PIWEB_DIR/$PIWEB_TGZ"; then
      echo "ERROR: npm install -g $PIWEB_TGZ failed; pf $VER, release/current and the daemon are installed and unchanged by this failure. Retry: npm install -g @silgrid/pi-web, or re-run with --with-pi-web."
      exit 1
    fi
    PIWEB_INSTALLED=1
  fi
  # The install check runs over a kept install too: it replaces nothing,
  # and a re-run after a failed check never passes a broken install.
  # npm may have skipped the package's install scripts and still exited 0
  # (e.g. npm 11 under ignore-scripts or an explicit allowScripts denial): the
  # checker the package ships confirms their effects or names the step.
  piweb_check_failed() {
    echo "ERROR: the Pi Web install check failed; pf $VER, release/current and the daemon are installed and unchanged by this failure. Fix the named step, then re-run with --with-pi-web."
    exit 1
  }
  if [ -z "$PIWEB_ROOT" ]; then
    echo "ERROR: Pi Web install check failed at npm root -g: npm reported no global root" >&2
    piweb_check_failed
  fi
  PIWEB_CHECKER="$PIWEB_ROOT/@silgrid/pi-web/bin/pi-web-install-check.js"
  if [ ! -f "$PIWEB_CHECKER" ]; then
    echo "ERROR: Pi Web install check failed at pi-web-install-check.js: $PIWEB_CHECKER is missing (the installed package predates the install check)" >&2
    piweb_check_failed
  fi
  node "$PIWEB_CHECKER" || piweb_check_failed
  [ -z "$PIWEB_INSTALLED" ] || echo "   Pi Web installed from $PIWEB_TGZ (release $VER)"
  # Served == installed (the deploy's definition): a running pi-web serving
  # another package keeps rendering pages whose /_next/static/* files npm
  # just deleted. Never restarted here: a restart ends its agent turns.
  case "$PIWEB_BEFORE" in
    "" | unidentified) ;;
    *)
      PIWEB_NOW="$(node "$PIWEB_SERVED" installed "$PIWEB_ROOT" 2>/dev/null || true)"
      # npm reinstalled the very package it served (--reinstall-pi-web) and
      # deleted its directory: judged again on what it serves now
      if [ -n "$PIWEB_INSTALLED" ] && [ "${PIWEB_BEFORE%|*}" = "$PIWEB_NOW" ]; then
        PIWEB_BEFORE="$(node "$PIWEB_SERVED" served "$PIWEB_SESSION" 2>/dev/null || true)"
      fi
      PIWEB_PKG="${PIWEB_BEFORE%|*}"
      PIWEB_PID="${PIWEB_BEFORE##*|}"
      # a process the loop replaced during the install already runs the new tree
      if [ "$PIWEB_PKG" != "$PIWEB_NOW" ] && kill -0 "$PIWEB_PID" 2>/dev/null; then
        PIWEB_NEW="${PIWEB_NOW%%|*}"
        [ -n "$PIWEB_NEW" ] || PIWEB_NEW=unknown
        if [ -z "$RESTART_PIWEB" ]; then
          echo "   ACTION: the running pi-web (tmux session $PIWEB_SESSION, pid $PIWEB_PID) serves ${PIWEB_PKG%%|*} from ${PIWEB_PKG#*|}, not the installed $PIWEB_NEW (${PIWEB_NOW#*|}): until it restarts, its pages load blank and every /_next/static/* request answers 5xx. Restarting ends any agent turn in progress in it; when none is running, restart it (its restart loop starts the installed $PIWEB_NEW within seconds): kill -TERM $PIWEB_PID   (next time, pass --restart-pi-web with --with-pi-web to restart during the upgrade)"
        else
          echo "   Pi Web: restarting pid $PIWEB_PID of tmux session $PIWEB_SESSION (serving ${PIWEB_PKG%%|*}) onto the installed $PIWEB_NEW (--restart-pi-web)"
          PIWEB_AFTER=""
          PIWEB_DEADLINE=$(( $(date +%s) + 60 ))
          if kill -TERM "$PIWEB_PID" 2>/dev/null; then
            while [ "$(date +%s)" -lt "$PIWEB_DEADLINE" ]; do
              PIWEB_AFTER="$(node "$PIWEB_SERVED" served "$PIWEB_SESSION" 2>/dev/null || true)"
              case "$PIWEB_AFTER" in
                "$PIWEB_NOW|"*) [ "${PIWEB_AFTER##*|}" = "$PIWEB_PID" ] || break ;;
              esac
              PIWEB_AFTER=""
              sleep 1
            done
          fi
          if [ -z "$PIWEB_AFTER" ]; then
            echo "ERROR: --restart-pi-web: tmux session $PIWEB_SESSION did not come back serving the installed $PIWEB_NEW (${PIWEB_NOW#*|}) within 60 s of kill -TERM $PIWEB_PID; pf $VER, release/current, the daemon and Pi Web $PIWEB_NEW are installed. Check the loop: tmux attach -t $PIWEB_SESSION"
            exit 1
          fi
          echo "   Pi Web restarted: serving $PIWEB_NEW (${PIWEB_NOW#*|}), pid ${PIWEB_AFTER##*|}"
        fi
      fi
      ;;
  esac
fi
echo ""
echo "Done. Daily use: 'pf queue' / 'pf submit <wi>' / 'pf watch <wi> --once'"
echo "From a pi session: just ask naturally (polyforge skill routes)."
[ -n "$WITH_PIWEB" ] || {
  echo "Optional web workbench (Pi Web, developed in this repository under pi-web/): re-run with --with-pi-web"
  echo "  (same release as pf), or npm install -g @silgrid/pi-web@latest;"
  echo "  see docs/install.md#optional-the-web-workbench-pi-web; pf works without it."
}
