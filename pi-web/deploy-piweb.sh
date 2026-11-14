#!/usr/bin/env bash
# deploy-piweb.sh — update and restart the @silgrid/pi-web service in this container.
#
# This container has no systemd; pi-web runs inside a detached tmux session
# ("piweb" by default) with a restart loop. PI_WEB_IDLE_TIMEOUT_MS=0 disables
# the 10-minute idle auto-exit so the server does not appear to "self-exit"
# while running unattended as a service.
#
# The service shape (port, password, allowed hosts, install channel) comes
# from the caller's environment. The lane runs from the daemon environment,
# which by design carries no PIWEB_*/PI_WEB_* values, so any shape variable the
# caller does not set is read from the operator's
# ~/.config/piweb/deploy.env (0600). Explicit caller values always win.
#
# Dependencies: bash, tmux, npm, node and curl (curl runs the post-swap
# service probe and the TLS edge probe; without it the service probe fails
# naming curl and the edge probe is skipped with one line naming curl; node
# runs the installed package's install check, writes the probe's JSON body
# and reads the installed package metadata).
#
# Right after `npm install -g`, the deploy confirms the effects of the
# package's install scripts itself (npm can skip them and still exit 0, e.g.
# npm 11 under ignore-scripts or an explicit allowScripts denial): it finds the package
# under `npm root -g` and runs the checker it ships,
# bin/pi-web-install-check.js, which runs the postinstall steps
# (patch-package, node bin/prepare-terminal.js), starts one pty through
# node-pty and, when that fails, rebuilds node-pty with its install scripts
# explicitly allowed and probes again. Each step prints
# `==> Install check: <step> ok`; a failure prints
# `ERROR: Pi Web install check failed at <step>: <reason>` and exits 1 before
# any tmux window is touched, so the old service keeps running. A package
# without the checker, or an npm that reports no global root, fails the same
# way.
#
# The deploy fails closed: a bind address that is not loopback is refused
# before any install or tmux side effect unless both PIWEB_PORT and
# PI_WEB_PASSWORD are set, so an incomplete shape can never fall back to
# pi-web's default port or start without web auth. After the swap, a complete
# shape (port + password) is probed on the configured port: GET /login must
# answer 200, an anonymous /api request must answer 401, and an authenticated
# GET / (the session cookie from POST /api/web-auth; the password travels on
# stdin and never on curl's argv or in a log) must answer a non-5xx, all
# within a bounded wait. A failed probe exits 3 so .polyforge/deploy.sh can
# report the post-swap failure truthfully instead of claiming not_changed.
# Then the deploy checks that the served pi-web is the installed one: the
# loop command itself puts the global install's bin directory first on its
# PATH, so neither the deploy caller's PATH nor a later hostile
# `tmux respawn-window` decides which package runs; and the package directory
# the running 'piweb' loop's Next.js server runs in (its working directory)
# must equal the package npm just installed under its global root
# (`npm root -g`), directory AND version, checked up to 3 times in a 30s
# deadline after the loop's first server is up. A mismatch
# (a wrong prefix, even at the same version) prints the served and installed
# packages and exits 3, which the lane reports as state=changed.
# The same loop command pins the pf the service will use: a non-empty `PF_BIN`
# from the deploying shell, else the tmux server's global `PF_BIN`, else `pf`
# found on the loop's pinned PATH. The deploy prints that pf's absolute path
# and `pf --version` (or a clear 'none found' line), and the loop itself sets
# `PF_BIN`, so a later hostile `tmux respawn-window` cannot drop it. Operators
# need not inspect /proc to see which pf the service runs.
# Then the TLS edge of the first PI_WEB_ALLOWED_HOSTS entry is probed once
# (https GET /, 5s timeout; any HTTP answer counts). The edge is an
# independent service, so a failure only prints
# "warn: TLS edge unreachable (<host>)" and never changes the exit code; an
# empty list skips the edge probe with one line naming the missing
# PI_WEB_ALLOWED_HOSTS (the probed edge's only source): "warn: TLS edge probe
# skipped: PI_WEB_ALLOWED_HOSTS is not set (no edge host to probe)".
#
# Exposure modes (opt-in via PIWEB_EXPOSE; leaving it unset keeps the legacy
# self-managed deployment — same bind, same allowed-hosts passthrough, no
# tunnel/serve tooling installed or torn down. One pi#107 behavior change:
# a redeploy recycles the pi-web loop window(s) instead of killing the
# session, so an unnamed loop window left by an older deploy or the README
# autostart recipe is replaced too):
#   PIWEB_EXPOSE=lan         direct cleartext http://<host-LAN-IP>:<port> on the
#                            same network; no extra tooling
#   PIWEB_EXPOSE=tailscale   Tailscale + `tailscale serve`, mounting pi-web at
#                            https://<machine>.<tailnet>.ts.net with an
#                            auto-provisioned Let's Encrypt cert (recommended)
#   PIWEB_EXPOSE=cloudflare  cloudflared outbound-only tunnel;
#                            PIWEB_TUNNEL=quick|named (default: quick)
#
# In tailscale/cloudflare modes the served/tunnel hostname is appended to
# PI_WEB_ALLOWED_HOSTS before pi-web starts (request-security rejects unknown
# Host headers otherwise); operator entries are preserved and come first. The
# serve/tunnel process runs as a tmux window of the same session, so killing
# the session stops it. Re-running the script is tunnel-idempotent (pi#107):
# a deploy recycles only the pi-web loop window(s) and keeps a serve/tunnel
# window that is alive and forwarding to the current port — its process, and
# for a quick tunnel its trycloudflare.com URL, survive the redeploy. A stale
# (wrong port) or dead serve/tunnel is replaced (its window included).
#
# Two invocation modes:
#   1. From the repository root:            ./deploy-piweb.sh
#   2. From the installed package:          pi-web-deploy
#      (the bin/pi/pi-web-deploy shim runs this same script from inside the
#       installed package directory; arguments and env are passed through)
#
# Usage: PIWEB_INSTALL_REPO=silgrid/polyforge ./deploy-piweb.sh
#        (or: pi-web-deploy, once Pi Web is installed from a Polyforge
#        GitHub Release by the pf installer's --with-pi-web)
# Env:
#   PIWEB_TMUX_SESSION  tmux session name (default: piweb)
#   PIWEB_PORT          port passed to pi-web (required for a non-loopback
#                       bind; deploy.env may supply it)
#   PIWEB_HOST          bind address (default: 0.0.0.0; non-loopback binds
#                       require PIWEB_PORT and PI_WEB_PASSWORD)
#   PI_WEB_PASSWORD     web-auth password passed through to pi-web (required
#                       for a non-loopback bind; deploy.env may supply it)
#   PIWEB_EXPOSE        optional exposure mode: lan | tailscale | cloudflare
#   PIWEB_TUNNEL        cloudflare mode only: quick | named (default: quick)
#   PIWEB_CF_CONFIG     cloudflare named mode: path to the operator-supplied
#                       cloudflared config.yml (tunnel ID, credentials-file,
#                       and ingress <hostname> -> http://127.0.0.1:<port>)
#   PIWEB_CF_HOSTNAME   cloudflare named mode: the public ingress hostname to
#                       append to PI_WEB_ALLOWED_HOSTS
#   PIWEB_INSTALL_REPO   the install channel: OWNER/REPO whose GitHub
#                        Release provides the pi-web tarball (Pi Web ships
#                        only there: silgrid/polyforge); the tgz + its
#                        SHA256SUMS asset are downloaded to a scratch dir,
#                        checksum-verified, and installed globally (a
#                        mismatch aborts before anything is installed).
#                        Unset falls back to `npm install -g` of the npm
#                        registry's @latest, which is not where Pi Web is
#                        released: set it.
#   PIWEB_INSTALL_VERSION  optional release pin for the channel above: the
#                        vX.Y.Z tag of exactly the release to install. When
#                        set, that release is resolved, checked to equal
#                        the requested tag, and only its tarball installed
#                        — latest is never resolved. Unset keeps the
#                        latest-release-wins channel (no version filtering).
#
# ~/.config/piweb/deploy.env (0600) may supply the shape keys above
# (PIWEB_PORT, PIWEB_HOST, PI_WEB_PASSWORD, PI_WEB_ALLOWED_HOSTS,
# PIWEB_INSTALL_REPO, PIWEB_INSTALL_VERSION). It is parsed as KEY=VALUE, never
# sourced, and only those keys are read. An unquoted value ends at a ` #`
# (whitespace, then `#`): the rest of the line is a comment; a quoted value
# keeps its content, `#` included. A file that is not mode 0600 or not owned
# by the deploying user is used anyway, with one `warn:` line. Values are
# never printed; only the names of the keys actually loaded are logged.
set -euo pipefail

# The caller may itself be in a tmux session. Its selector must not override
# the service/scratch namespace chosen by TMUX_TMPDIR (or the default server).
# Keep PATH lookup so the deployment harness can supply its own tmux binary.
tmux() { env -u TMUX -u TMUX_PANE tmux "$@"; }

# ---------------------------------------------------------------------------
# Service-shape loader: fill only variables the caller left unset. An
# explicitly set variable (even an empty one) wins over the file. Parsing
# KEY=VALUE avoids executing operator content, and the allowlist keeps the
# file from smuggling unrelated environment into the deployed service.
# ---------------------------------------------------------------------------
load_deploy_env() {
  local file="${HOME}/.config/piweb/deploy.env"
  [ -f "${file}" ] || return 0
  if [ ! -r "${file}" ]; then
    echo "==> Note: ${file} is not readable; using the caller environment only." >&2
    return 0
  fi

  local line key value stripped loaded=""
  local quoted_double='^"(.*)"[[:space:]]*(#.*)?$' quoted_single="^'(.*)'[[:space:]]*(#.*)?\$"
  while IFS= read -r line || [ -n "${line}" ]; do
    line="${line%$'\r'}"
    line="${line#"${line%%[![:space:]]*}"}"
    case "${line}" in
      "" | "#"*) continue ;;
    esac
    line="${line#export }"
    case "${line}" in
      *=*) ;;
      *) continue ;;
    esac
    key="${line%%=*}"
    value="${line#*=}"
    case "${key}" in
      PIWEB_PORT | PIWEB_HOST | PI_WEB_PASSWORD | PI_WEB_ALLOWED_HOSTS | \
        PIWEB_INSTALL_REPO | PIWEB_INSTALL_VERSION) ;;
      *) continue ;;
    esac
    # Explicit caller environment wins, including an explicitly empty value.
    if [ -n "${!key+x}" ]; then
      continue
    fi
    # A quoted value keeps its content (a trailing comment after the closing
    # quote is dropped); an unquoted value ends at whitespace followed by `#`.
    if [[ "${value}" =~ ${quoted_double} ]] || [[ "${value}" =~ ${quoted_single} ]]; then
      value="${BASH_REMATCH[1]}"
    else
      stripped="${value%%[[:space:]]#*}"
      if [ "${stripped}" != "${value}" ]; then
        value="${stripped%"${stripped##*[![:space:]]}"}"
      fi
    fi
    printf -v "${key}" '%s' "${value}"
    loaded="${loaded}${loaded:+ }${key}"
  done < "${file}"

  if [ -n "${loaded}" ]; then
    echo "==> Loaded service shape from ${file} (${loaded})"
  fi

  # The file holds the web-auth password: warn (never fail) when its mode is
  # not 0600 or it is not the deploying user's. Names the file, mode and
  # owner only, never a value.
  local mode owner
  case "$(uname -s)" in
    Darwin | *BSD) mode="$(stat -f %Lp "${file}" 2>/dev/null || true)"; owner="$(stat -f %u "${file}" 2>/dev/null || true)" ;;
    *) mode="$(stat -c %a "${file}" 2>/dev/null || true)"; owner="$(stat -c %u "${file}" 2>/dev/null || true)" ;;
  esac
  if [ -n "${mode}" ] && { [ "${mode}" != "600" ] || [ "${owner}" != "$(id -u)" ]; }; then
    echo "warn: ${file} has mode ${mode} and owner uid ${owner}; expected mode 0600 owned by uid $(id -u) (it holds the service password)."
  fi
}

load_deploy_env

SESSION="${PIWEB_TMUX_SESSION:-piweb}"
PKG="@silgrid/pi-web"
HOST="${PIWEB_HOST:-0.0.0.0}"

# ---------------------------------------------------------------------------
# Exposure-mode selection.
# Validated BEFORE any uninstall/install/tmux side effect so a bad value fails
# fast with nothing changed on the machine.
# ---------------------------------------------------------------------------
EXPOSE="${PIWEB_EXPOSE:-}"
case "${EXPOSE}" in
  "" | lan | tailscale | cloudflare) ;;
  *)
    echo "ERROR: unsupported PIWEB_EXPOSE='${EXPOSE}'." >&2
    echo "       Valid values: PIWEB_EXPOSE=lan | tailscale | cloudflare," >&2
    echo "       or leave it unset for the legacy self-managed deployment." >&2
    exit 1
    ;;
esac

TUNNEL="${PIWEB_TUNNEL:-quick}"
if [ -n "${PIWEB_TUNNEL:-}" ] && [ "${EXPOSE}" != "cloudflare" ]; then
  echo "==> Note: PIWEB_TUNNEL is only consumed by PIWEB_EXPOSE=cloudflare; ignoring it here."
fi
if [ "${EXPOSE}" = "cloudflare" ]; then
  case "${TUNNEL}" in
    quick | named) ;;
    *)
      echo "ERROR: unsupported PIWEB_TUNNEL='${TUNNEL}'." >&2
      echo "       Valid values: PIWEB_TUNNEL=quick | named." >&2
      exit 1
      ;;
  esac
fi

# ---------------------------------------------------------------------------
# Allowed-hosts wiring. EFFECTIVE_ALLOWED_HOSTS is what the restart loop
# expands into pi-web's environment: the operator's PI_WEB_ALLOWED_HOSTS value
# unchanged when unset/lan, plus the served/tunnel hostname appended in
# tailscale/cloudflare modes. Operator entries are preserved and come first.
# ---------------------------------------------------------------------------
EFFECTIVE_ALLOWED_HOSTS="${PI_WEB_ALLOWED_HOSTS:-}"

append_allowed_host() {
  local host="$1"
  [ -z "${host}" ] && return 0
  case ",${EFFECTIVE_ALLOWED_HOSTS}," in
    *",${host},"*) return 0 ;; # already present
  esac
  if [ -n "${EFFECTIVE_ALLOWED_HOSTS}" ]; then
    EFFECTIVE_ALLOWED_HOSTS="${EFFECTIVE_ALLOWED_HOSTS},${host}"
  else
    EFFECTIVE_ALLOWED_HOSTS="${host}"
  fi
}

# Port wiring: an unset PIWEB_PORT keeps the legacy behavior only on a
# loopback bind (no --port flag; pi-web uses its own default); a non-loopback
# bind without PIWEB_PORT is refused by the fail-closed guard below. Exposed
# modes need a predictable local port for the serve/tunnel backends, so they
# default to 8142 and pass it explicitly so pi-web binds exactly where
# serve/tunnel proxy to.
PORT_ARGS="${PIWEB_PORT:+--port ${PIWEB_PORT}}"
if [ -n "${EXPOSE}" ] && [ -z "${PIWEB_PORT:-}" ]; then
  PIWEB_PORT=8142
  PORT_ARGS="--port ${PIWEB_PORT}"
fi
LOCAL_PORT="${PIWEB_PORT:-30141}"

# ---------------------------------------------------------------------------
# Fail closed: a non-loopback bind without both a configured port and a
# password would silently fall back to pi-web's default port and disable web
# auth. Refuse before prepare_mode or any install/tmux side effect, and name
# exactly what is missing.
# ---------------------------------------------------------------------------
is_loopback_host() {
  case "$1" in
    127.* | localhost | ::1 | "[::1]") return 0 ;;
  esac
  return 1
}

if ! is_loopback_host "${HOST}"; then
  missing=""
  [ -n "${PIWEB_PORT:-}" ] || missing="${missing} PIWEB_PORT"
  [ -n "${PI_WEB_PASSWORD:-}" ] || missing="${missing} PI_WEB_PASSWORD"
  if [ -n "${missing}" ]; then
    echo "ERROR: refusing to deploy pi-web on non-loopback '${HOST}' without the complete service shape." >&2
    echo "       Missing:${missing}." >&2
    echo "       Set these in the caller environment or in ~/.config/piweb/deploy.env." >&2
    exit 1
  fi
fi

# ---------------------------------------------------------------------------
# Tooling helpers for the exposure modes (best-effort install when absent).
# ---------------------------------------------------------------------------
install_tailscale() {
  command -v tailscale >/dev/null 2>&1 && return 0
  echo "==> Installing Tailscale"
  case "$(uname -s)" in
    Darwin)
      if command -v brew >/dev/null 2>&1; then
        brew install tailscale
      else
        echo "ERROR: Homebrew not found; install Tailscale from https://tailscale.com/download first." >&2
        return 1
      fi
      ;;
    Linux)
      curl -fsSL https://tailscale.com/install.sh | sh
      ;;
    *)
      echo "ERROR: unsupported platform for Tailscale: $(uname -s)" >&2
      return 1
      ;;
  esac
}

install_cloudflared() {
  command -v cloudflared >/dev/null 2>&1 && return 0
  echo "==> Installing cloudflared"
  case "$(uname -s)" in
    Darwin)
      if command -v brew >/dev/null 2>&1; then
        brew install cloudflared
      else
        echo "ERROR: Homebrew not found; install cloudflared from https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/downloads/ first." >&2
        return 1
      fi
      ;;
    Linux)
      case "$(uname -m)" in
        x86_64) CF_ARCH=amd64 ;;
        aarch64 | arm64) CF_ARCH=arm64 ;;
        *)
          echo "ERROR: unsupported architecture for cloudflared: $(uname -m)" >&2
          return 1
          ;;
      esac
      curl -fsSL -o /usr/local/bin/cloudflared \
        "https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-${CF_ARCH}" \
      && chmod +x /usr/local/bin/cloudflared
      ;;
    *)
      echo "ERROR: unsupported platform for cloudflared: $(uname -s)" >&2
      return 1
      ;;
  esac
}

# Extract Self.DNSName (minus any trailing dot) from tailscale JSON output
# read on stdin. Node is used (never jq) because this script already requires
# npm/node for the pi-web install itself.
ts_self_dnsname() {
  node -e 'let s="";process.stdin.on("data",d=>{s+=d});process.stdin.on("end",()=>{try{const j=JSON.parse(s);const n=(j.Self&&j.Self.DNSName)||"";process.stdout.write(n.replace(/\.+$/,""));}catch(e){}});'
}

ts_resolve_hostname() {
  local host=""
  host="$(tailscale dns status --json 2>/dev/null | ts_self_dnsname || true)"
  if [ -z "${host}" ]; then
    host="$(tailscale status --json 2>/dev/null | ts_self_dnsname || true)"
  fi
  printf '%s' "${host}"
}

# ---------------------------------------------------------------------------
# Mode preparation. Each branch installs/verifies its tooling, resolves the
# hostname that must be allowed, and appends it to EFFECTIVE_ALLOWED_HOSTS.
# Runs BEFORE the uninstall/install/tmux steps; nothing here touches pi-web
# itself. LAN mode mutates nothing (the caveats are stated, not mitigated).
# ---------------------------------------------------------------------------
prepare_mode() {
  case "${EXPOSE}" in
    "" ) ;; # legacy self-managed path: no exposure-related work
    lan)
      local lan_ip=""
      case "$(uname -s)" in
        Linux)
          lan_ip="$(ip route get 1.1.1.1 2>/dev/null | awk '{for (i=1; i<=NF; i++) if ($i=="src") {print $(i+1); exit}}' || true)"
          ;;
        Darwin)
          lan_ip="$(ipconfig getifaddr en0 2>/dev/null || true)"
          ;;
      esac
      echo "==> PIWEB_EXPOSE=lan: pi-web binds ${HOST}:${LOCAL_PORT}; open"
      echo "    http://${lan_ip:-<host-LAN-IP>}:${LOCAL_PORT} from a device on the same network."
      echo "    Cleartext caveat: PWA install degrades over HTTP (browsers may not offer"
      echo "    'Install app'). Chrome app-mode windows (the piweb launcher) open http://"
      echo "    origins fine."
      ;;
    tailscale)
      install_tailscale
      # tailscaled must be reachable for `tailscale up` to talk to it.
      if ! tailscale status >/dev/null 2>&1; then
        if command -v tailscaled >/dev/null 2>&1; then
          echo "==> tailscaled not reachable; starting it in the background"
          nohup tailscaled >/tmp/tailscaled.log 2>&1 &
          sleep 2
        fi
      fi
      # Actually request login: without this, a clean machine never joins the
      # tailnet, ts_resolve_hostname returns empty, and the script exits 1
      # (review blocker, pi#38).
      tailscale status >/dev/null 2>&1 || tailscale up
      echo "==> Running 'tailscale up' (this blocks until SSO login completes)."
      echo "    Sign in with GitHub or Apple — both work in mainland China; Google SSO does not."
      TS_HOST="$(ts_resolve_hostname)"
      if [ -z "${TS_HOST}" ]; then
        echo "ERROR: could not resolve the tailnet hostname (Self.DNSName)." >&2
        echo "       Is this machine logged in to the tailnet? See 'tailscale status'." >&2
        exit 1
      fi
      append_allowed_host "${TS_HOST}"
      echo "==> Tailscale mode: pi-web will be served at https://${TS_HOST}"
      echo "    (hostname appended to PI_WEB_ALLOWED_HOSTS)"
      ;;
    cloudflare)
      install_cloudflared
      if [ "${TUNNEL}" = "named" ]; then
        if [ -z "${PIWEB_CF_CONFIG:-}" ] || [ -z "${PIWEB_CF_HOSTNAME:-}" ]; then
          echo "ERROR: PIWEB_TUNNEL=named requires a Cloudflare account with your domain on" >&2
          echo "       Cloudflare DNS, plus operator-supplied:" >&2
          echo "  PIWEB_CF_CONFIG    path to your cloudflared config.yml (tunnel ID," >&2
          echo "                     credentials-file, ingress <hostname> -> http://127.0.0.1:${LOCAL_PORT})" >&2
          echo "  PIWEB_CF_HOSTNAME  the public ingress hostname to append to PI_WEB_ALLOWED_HOSTS" >&2
          exit 1
        fi
        if [ ! -f "${PIWEB_CF_CONFIG}" ]; then
          echo "ERROR: PIWEB_CF_CONFIG '${PIWEB_CF_CONFIG}' does not exist." >&2
          exit 1
        fi
        append_allowed_host "${PIWEB_CF_HOSTNAME}"
        echo "==> Cloudflare named tunnel: hostname '${PIWEB_CF_HOSTNAME}' appended to PI_WEB_ALLOWED_HOSTS"
      else
        # Quick tunnel: the URL is only known once cloudflared runs — captured
        # after the tunnel window starts, or recovered from the stable log on
        # a redeploy whose tunnel is still healthy. Nothing to prepare here;
        # the deploy step decides reuse vs. replace.
        :
      fi
      ;;
  esac
}

prepare_mode

# The upstream @agegr/pi-web package owns the same global bin name (pi-web);
# uninstall it first so installing ours does not fail with EEXIST. Harmless
# when it is already gone.
if npm ls -g @agegr/pi-web >/dev/null 2>&1; then
  echo "==> Removing superseded @agegr/pi-web global install (same pi-web bin)"
  npm uninstall -g @agegr/pi-web
fi

# Scratch dirs for the PIWEB_INSTALL_REPO channel download and the service
# probe's password-bearing files; both are empty (and the trap a no-op) on the
# paths that do not use them.
CHANNEL_DIR=""
PROBE_DIR=""
cleanup_scratch() {
  [ -n "${CHANNEL_DIR}" ] && rm -rf "${CHANNEL_DIR}"
  [ -n "${PROBE_DIR}" ] && rm -rf "${PROBE_DIR}"
  return 0
}
trap cleanup_scratch EXIT

# PIWEB_INSTALL_REPO opts into the internal GitHub-Releases install channel
# (strict opt-in: with it unset the public-npm path below is byte-identical
# to the original). PIWEB_INSTALL_VERSION pins the install to exactly that
# release tag (resolved, checked to equal the requested pin, then downloaded
# and installed — latest is never resolved on the pinned path); without the
# pin the repo's LATEST release wins as-is — no version filtering — so the
# channel tracks whatever release GitHub reports.
if [ -n "${PIWEB_INSTALL_REPO:-}" ]; then
  CHANNEL_DIR="$(mktemp -d)"
  if [ -n "${PIWEB_INSTALL_VERSION:-}" ]; then
    echo "==> Installing ${PKG} from GitHub Release ${PIWEB_INSTALL_VERSION} of ${PIWEB_INSTALL_REPO}"
    CHANNEL_TAG="$(gh release view -R "${PIWEB_INSTALL_REPO}" "${PIWEB_INSTALL_VERSION}" --json tagName --jq .tagName)"
    if [ "${CHANNEL_TAG}" != "${PIWEB_INSTALL_VERSION}" ]; then
      echo "ERROR: ${PIWEB_INSTALL_VERSION} of ${PIWEB_INSTALL_REPO} resolved to tag '${CHANNEL_TAG}', not the requested release." >&2
      exit 1
    fi
    gh release download -R "${PIWEB_INSTALL_REPO}" "${PIWEB_INSTALL_VERSION}" -p '*.tgz' -p SHA256SUMS -D "${CHANNEL_DIR}"
  else
    echo "==> Installing ${PKG} from the latest GitHub Release of ${PIWEB_INSTALL_REPO}"
    CHANNEL_TAG="$(gh release view -R "${PIWEB_INSTALL_REPO}" --json tagName --jq .tagName)"
    gh release download -R "${PIWEB_INSTALL_REPO}" -p '*.tgz' -p SHA256SUMS -D "${CHANNEL_DIR}"
  fi
  CHANNEL_TGZ="$(find "${CHANNEL_DIR}" -maxdepth 1 -name '*.tgz' -print -quit)"
  if [ -z "${CHANNEL_TGZ}" ]; then
    echo "ERROR: the latest release of ${PIWEB_INSTALL_REPO} carries no *.tgz asset." >&2
    exit 1
  fi
  # Assets are downloaded flattened into CHANNEL_DIR, so the checksum file's
  # bare filename matches there. The release's SHA256SUMS also covers the
  # assets of the same tag this channel does not download (the pf binaries
  # and the image digest: one release carries every artifact),
  # so the tarball's own line is checked: a tarball without a line, or any
  # mismatch, aborts BEFORE any global install side effect.
  awk -v n="${CHANNEL_TGZ##*/}" '$2 == n || $2 == "*" n' "${CHANNEL_DIR}/SHA256SUMS" >"${CHANNEL_DIR}/tgz.sha256"
  if [ ! -s "${CHANNEL_DIR}/tgz.sha256" ]; then
    echo "ERROR: the SHA256SUMS of ${PIWEB_INSTALL_REPO} has no line for ${CHANNEL_TGZ##*/}." >&2
    exit 1
  fi
  (cd "${CHANNEL_DIR}" && sha256sum -c tgz.sha256)
  echo "==> Checksum verified; installing ${CHANNEL_TGZ##*/} (release ${CHANNEL_TAG:-latest})"
  npm install -g "${CHANNEL_TGZ}"
else
  echo "==> Installing ${PKG}@latest"
  npm install -g "${PKG}@latest"
fi

# Install check: npm may skip the package's install scripts
# and still exit 0 (e.g. npm 11 under ignore-scripts or an explicit
# allowScripts denial), so the deploy confirms their effects itself with the
# checker the installed package ships, before any tmux window is touched. Any
# failure names its step and exits 1; the running service is left as it was.
install_check_failed() {
  echo "ERROR: Pi Web install check failed at $1: $2" >&2
  exit 1
}
INSTALLED_NPM_ROOT="$(npm root -g 2>/dev/null || true)"
[ -n "${INSTALLED_NPM_ROOT}" ] || install_check_failed "npm root -g" "npm reported no global root"
echo "==> Install check: npm root -g ok"
INSTALL_CHECKER="${INSTALLED_NPM_ROOT}/${PKG}/bin/pi-web-install-check.js"
[ -f "${INSTALL_CHECKER}" ] \
  || install_check_failed "pi-web-install-check.js" "${INSTALL_CHECKER} is missing (the installed package predates the install check)"
echo "==> Install check: pi-web-install-check.js ok"
if ! node "${INSTALL_CHECKER}"; then
  echo "==> ${PKG} was installed but its install check failed; the running service was not touched." >&2
  exit 1
fi

# pi#107: a deploy is tunnel-idempotent — the tmux session is no longer torn
# down. Only the pi-web restart-loop window(s) are recycled; a healthy
# serve/tunnel window (and its quick URL) survives the redeploy. (The old
# script killed the whole session and rebuilt the tunnel on every run, so
# every deploy minted a fresh trycloudflare.com URL.)

# The restarter's PATH must not decide which pi-web the loop runs.
# npm's global root (INSTALLED_NPM_ROOT, read by the install
# check) is <prefix>/lib/node_modules, so the bin directory holding the
# just-installed `pi-web` link is <prefix>/bin.
installed_piweb_bin_dir() {
  printf '%s/bin' "$(dirname "$(dirname "${INSTALLED_NPM_ROOT}")")"
}

# Absolute path of an executable named by <value> (a path or a bare command),
# or nothing when it is not executable. Used only for the pf line's display;
# the loop pins the configured value itself.
pf_absolute_path() {
  local value="$1"
  case "${value}" in
    */*) ;;
    *)
      value="$(command -v "${value}" 2>/dev/null || true)"
      [ -n "${value}" ] || return 1
      ;;
  esac
  [ -x "${value}" ] || return 1
  case "${value}" in
    /*) printf '%s' "${value}" ;;
    *) printf '%s/%s' "$(cd "$(dirname "${value}")" && pwd -P)" "$(basename "${value}")" ;;
  esac
}

# The pf the loop will run, resolved once after the install (
# AC6). Precedence: the deploy caller's non-empty PF_BIN, else the tmux
# server's global PF_BIN (where an operator usually keeps it), else `pf` on
# the loop's pinned PATH. A configured value is kept even when the file is
# missing: PF_BIN is authoritative, so the service then reports Polyforge as
# not installed with its readable reason instead of silently using another pf.
resolve_pf_bin() {
  local configured="" found=""
  configured="${PF_BIN:-}"
  if [ -z "${configured}" ] && command -v tmux >/dev/null 2>&1; then
    configured="$(tmux show-environment -g PF_BIN 2>/dev/null | sed -n 's/^PF_BIN=//p' | head -n 1 || true)"
  fi
  if [ -z "${configured}" ]; then
    found="$(PATH="$(installed_piweb_bin_dir):${PATH}" command -v pf 2>/dev/null || true)"
    [ -n "${found}" ] || return 0
    configured="${found}"
  fi
  printf '%s' "${configured}"
}

# The restart-loop command is (re)built at the point of use so it always
# embeds the final EFFECTIVE_ALLOWED_HOSTS (a quick-tunnel hostname is only
# known after cloudflared starts).
build_loop_cmd() {
  # shellcheck disable=SC2016  # the single quotes are deliberate: the tmux
  # command string must expand the variables inside the loop's shell, not here
  local path_prefix="" pf_prefix=""
  # Prepend the installed prefix's bin dir inside the loop itself: a new
  # window or a hostile-shell respawn then still resolves the installed
  # pi-web, not whatever the restarter's PATH prefers.
  path_prefix="PATH='$(installed_piweb_bin_dir)':\"\${PATH}\" "
  if [ -n "${PF_BIN_EFFECTIVE:-}" ]; then
    # Pin the resolved pf too, so the printed line names exactly what the
    # service gets and a hostile respawn cannot drop it (AC6).
    pf_prefix="PF_BIN='${PF_BIN_EFFECTIVE}' "
  fi
  # Unset provider env credentials that leak from the container shell
  # (pi#109): OPENAI_API_KEY would activate pi's builtin `openai` provider
  # and dump its full 44-model backlog (gpt-4/gpt-4o/o1/o3/...) into the
  # model selector, even though this deployment only uses the custom
  # models.json providers.
  LOOP_CMD="
  while true; do
    ${path_prefix}${pf_prefix}PI_WEB_IDLE_TIMEOUT_MS=0 ${PI_WEB_PASSWORD:+PI_WEB_PASSWORD='${PI_WEB_PASSWORD}'} ${EFFECTIVE_ALLOWED_HOSTS:+PI_WEB_ALLOWED_HOSTS='${EFFECTIVE_ALLOWED_HOSTS}'} env -u OPENAI_API_KEY -u OPENAI_BASE_URL pi-web -H ${HOST} ${PORT_ARGS}
    echo '[piweb] pi-web exited; restarting in 5s' >&2
    sleep 5
  done
"
}

# ---------------------------------------------------------------------------
# tmux recycling (pi#107: deploys are tunnel-idempotent). A deploy recycles
# only the pi-web restart-loop window(s); the session and any serve/tunnel
# window are kept. Killing a session's last window destroys the session, so
# session existence is re-checked after every kill — never cached from
# before a kill.
# ---------------------------------------------------------------------------
session_exists() {
  tmux has-session -t "${SESSION}" 2>/dev/null
}

# Recycle the pi-web restart-loop window (kill + recreate). Every window that
# is not a serve/tunnel window is an old loop window — named 'piweb' by this
# script, or unnamed (pre-pi#107 deploys and the README autostart recipe
# create it that way) — and is replaced, so a legacy session converges to
# exactly one named 'piweb' loop window on the first post-fix deploy. If the
# recycled windows were the session's last, the session dies with them and is
# created fresh — either way exactly one pi-web loop window runs afterwards.
recycle_piweb_window() {
  if session_exists; then
    echo "==> Recycling pi-web window(s) in tmux session '${SESSION}'"
    local wins win_id win_name
    wins="$(tmux list-windows -t "${SESSION}" -F '#{window_id}|#{window_name}' 2>/dev/null || true)"
    while IFS='|' read -r win_id win_name; do
      [ -z "${win_id}" ] && continue
      case "${win_name}" in tunnel | serve) continue ;; esac
      tmux kill-window -t "${win_id}" 2>/dev/null || true
      session_exists || break
    done <<EOF
${wins}
EOF
  fi
  if session_exists; then
    # Target "${SESSION}:" (trailing colon): a bare session name makes tmux
    # 3.4 create the window at the session's CURRENT window index and fail
    # with 'index 1 in use' (pi#109, hit live on 2026-10-18).
    tmux new-window -t "${SESSION}:" -n piweb "${LOOP_CMD}"
  else
    echo "==> Creating tmux session '${SESSION}' (window 'piweb')"
    tmux new-session -d -s "${SESSION}" -n piweb "${LOOP_CMD}"
  fi
}

# Replace a named serve/tunnel window (kill + recreate; same last-window
# caveat as above: re-check the session after killing the old window, then
# create the replacement in whatever shape survives).
replace_window() {
  local name="$1" cmd="$2"
  if session_exists; then
    tmux kill-window -t "${SESSION}:${name}" 2>/dev/null || true
  fi
  if session_exists; then
    # Same "${SESSION}:" target discipline as recycle_piweb_window (pi#109).
    tmux new-window -t "${SESSION}:" -n "${name}" "${cmd}"
  else
    tmux new-session -d -s "${SESSION}" -n "${name}" "${cmd}"
  fi
}

# Quick-tunnel connector log at a stable path (was: a fresh mktemp file per
# run, which made the previous URL unrecoverable). A redeploy with a healthy
# connector recovers the URL from this file.
QUICK_TUNNEL_LOG="${HOME}/.piweb/${SESSION}-quick-tunnel.log"

quick_tunnel_url_from_log() {
  grep -o 'https://[a-z0-9-]*\.trycloudflare\.com' "${QUICK_TUNNEL_LOG}" 2>/dev/null \
    | head -n 1 | sed 's|^https://||' || true
}

# Is a cloudflared process matching <ERE> alive? A pgrep -f match counts
# only when the matched process itself IS cloudflared (comm check): the
# tmux server forked by this script's tunnel-window creation keeps the
# client's argv for its whole life (tmux renames only its comm, to
# 'tmux: server'), so a bare pgrep -f keeps matching the server after the
# connector died and a redeploy would "reuse" a dead tunnel — stale URL,
# no connector, no tunnel window (pi#118). A PID that dies between pgrep
# and ps fails the comm read and counts as dead. The comm is compared by
# basename, not exactly: Linux ps prints the bare executable name, but BSD
# ps (macOS, a supported install target) prints the invoked executable's
# full path there (e.g. /opt/homebrew/bin/cloudflared) — an exact compare
# would judge every healthy connector dead on macOS and replace it on each
# redeploy, breaking the healthy-tunnel reuse guarantee (pi#107).
cloudflared_alive() {
  local pattern="$1" pid comm
  for pid in $(pgrep -f "${pattern}" 2>/dev/null); do
    comm="$(ps -p "${pid}" -o comm= 2>/dev/null || true)"
    if [ "${comm##*/}" = "cloudflared" ]; then
      return 0
    fi
  done
  return 1
}

# Is a quick-tunnel connector for THIS local port already alive? Identity:
# a live cloudflared process whose command line targets
# http://127.0.0.1:${LOCAL_PORT} (the [^0-9] anchor keeps :8142 from matching
# :81421). A connector started by any means is found via its command line. A
# connector forwarding to a different port (PIWEB_PORT changed) misses and
# is replaced.
quick_tunnel_alive() {
  cloudflared_alive "cloudflared tunnel --url http://127\.0\.0\.1:${LOCAL_PORT}([^0-9]|$)"
}

# Named tunnel: identity is a live cloudflared connector for this exact
# config, plus the config's ingress service port must equal the port pi-web
# binds now (a PIWEB_PORT change means the running tunnel forwards to a
# stale port).
named_tunnel_alive() {
  cloudflared_alive "cloudflared tunnel --config ${PIWEB_CF_CONFIG}" || return 1
  local cfg_port
  cfg_port="$(grep -oE 'http://(127\.0\.0\.1|localhost):[0-9]+' "${PIWEB_CF_CONFIG}" 2>/dev/null | head -n 1 | grep -oE '[0-9]+$' || true)"
  [ "${cfg_port}" = "${LOCAL_PORT}" ]
}

# Does 'tailscale serve' already map the served origin to this local port?
# (serve config persists in tailscaled, so a healthy mapping survives
# redeploys and must be kept, not reset.)
tailscale_serve_current() {
  tailscale serve status 2>/dev/null | grep -qE "(127\.0\.0\.1|localhost):${LOCAL_PORT}([^0-9]|$)"
}

# Resolve and print the pf the loop will run (AC6). Done once,
# after the install, before any window is built; build_loop_cmd then pins the
# same value. Both a configured-but-missing PF_BIN and no pf at all print a
# clear 'none found' line instead of a misleading path.
PF_BIN_EFFECTIVE="$(resolve_pf_bin)"
PF_BIN_PATH=""
PF_BIN_VERSION=""
if [ -n "${PF_BIN_EFFECTIVE}" ]; then
  PF_BIN_PATH="$(pf_absolute_path "${PF_BIN_EFFECTIVE}" || true)"
  if [ -n "${PF_BIN_PATH}" ]; then
    PF_BIN_VERSION="$("${PF_BIN_PATH}" --version 2>/dev/null | head -n 1 || true)"
    echo "==> pf: ${PF_BIN_PATH} (${PF_BIN_VERSION:-version unavailable})"
  else
    echo "==> pf: none found (PF_BIN='${PF_BIN_EFFECTIVE}' is not an executable; the service will report Polyforge as not installed)"
  fi
else
  echo "==> pf: none found (no PF_BIN and no pf on the loop PATH; the service will report Polyforge as not installed)"
fi

case "${EXPOSE}" in
  "" | lan | tailscale)
    # Legacy / LAN path: identical to the original script except that a
    # redeploy recycles the pi-web loop window(s) — including unnamed ones
    # left by pre-pi#107 deploys and the README autostart recipe — instead of
    # killing the whole session (LAN prepared nothing but an informational
    # echo above).
    build_loop_cmd
    recycle_piweb_window
    if [ "${EXPOSE}" = "tailscale" ]; then
      if tailscale_serve_current; then
        echo "==> 'tailscale serve' already forwards to 127.0.0.1:${LOCAL_PORT}; keeping it"
      else
        # No serve mapping for this port (fresh, stale, or dead): reset any
        # stale serve config so this deploy never inherits one, then
        # re-assert it with a foreground holder window — killing a previous
        # 'serve' window first (`serve --reset` clears the config but does
        # not stop a foreground holder, so it must be replaced, not kept).
        tailscale serve --reset >/dev/null 2>&1 || true
        echo "==> Starting 'tailscale serve' (foreground; dies with this session)"
        replace_window serve "tailscale serve --https=443 http://127.0.0.1:${LOCAL_PORT}"
      fi
      echo "==> pi-web served at https://${TS_HOST} (proxying 127.0.0.1:${LOCAL_PORT})"
    fi
    ;;
  cloudflare)
    if [ "${TUNNEL}" = "quick" ]; then
      # The trycloudflare.com URL only exists once cloudflared runs. A healthy
      # connector for this port is reused as-is and its URL recovered from
      # the stable log; only when the tunnel is missing, stale (different
      # port), or its URL is unrecoverable is the tunnel window replaced and
      # a fresh URL captured — in which case the rebuilt pi-web allow-list
      # carries the new URL.
      mkdir -p "${HOME}/.piweb"
      CF_HOST=""
      if quick_tunnel_alive; then
        echo "==> Reusing healthy cloudflared quick tunnel (tmux window 'tunnel')"
        CF_HOST="$(quick_tunnel_url_from_log)"
        if [ -z "${CF_HOST}" ]; then
          echo "==> Tunnel alive but its URL is not recoverable from ${QUICK_TUNNEL_LOG}; restarting it"
        fi
      fi
      if [ -z "${CF_HOST}" ]; then
        : >"${QUICK_TUNNEL_LOG}"
        echo "==> Starting cloudflared quick tunnel (tmux window 'tunnel')"
        replace_window tunnel \
          "cloudflared tunnel --url http://127.0.0.1:${LOCAL_PORT} >'${QUICK_TUNNEL_LOG}' 2>&1"
        echo "==> Waiting for the trycloudflare.com URL (full log: ${QUICK_TUNNEL_LOG})"
        i=0
        while [ "${i}" -lt 30 ]; do
          CF_HOST="$(quick_tunnel_url_from_log)"
          [ -n "${CF_HOST}" ] && break
          i=$((i + 1))
          sleep 1
        done
        if [ -z "${CF_HOST}" ]; then
          echo "ERROR: could not capture the trycloudflare.com URL. cloudflared output:" >&2
          cat "${QUICK_TUNNEL_LOG}" >&2 2>/dev/null || true
          tmux kill-session -t "${SESSION}" 2>/dev/null || true
          exit 1
        fi
      fi
      append_allowed_host "${CF_HOST}"
      build_loop_cmd
      echo "==> Quick tunnel up: https://${CF_HOST} (hostname appended to PI_WEB_ALLOWED_HOSTS)"
      echo "    WARNING: quick-tunnel URLs are DEMO-ONLY. Reusing a healthy tunnel keeps the"
      echo "    URL stable across redeploys, but the connector restarting (or dying) still"
      echo "    mints a new one; never bake a trycloudflare.com URL into a PWA or app-mode"
      echo "    client."
      echo "    Use PIWEB_TUNNEL=named (or PIWEB_EXPOSE=tailscale) for a stable origin."
      recycle_piweb_window
    else
      if named_tunnel_alive; then
        echo "==> Reusing cloudflared named tunnel (config: ${PIWEB_CF_CONFIG})"
      else
        echo "==> Starting cloudflared named tunnel (config: ${PIWEB_CF_CONFIG})"
        replace_window tunnel "cloudflared tunnel --config ${PIWEB_CF_CONFIG}"
      fi
      build_loop_cmd
      recycle_piweb_window
    fi
    ;;
esac

# ---------------------------------------------------------------------------
# Service probe. A complete shape (configured port + password) has an endpoint
# the deploy can actually verify; probe it after the swap, before the deploy
# may claim success. The loop window starts asynchronously, so wait on the real
# condition with a deadline instead of assuming the bind is immediate. The
# password reaches curl only through a 0600 body file written from node's
# stdin: never an argv token and never a log line.
# ---------------------------------------------------------------------------
write_web_auth_body() {
  local file="$1"
  printf '%s' "${PI_WEB_PASSWORD}" | node -e '
let raw = "";
process.stdin.setEncoding("utf8");
process.stdin.on("data", (chunk) => { raw += chunk; });
process.stdin.on("end", () => { process.stdout.write(JSON.stringify({ password: raw })); });
' >"${file}"
}

probe_configured_service() {
  local host="$1" port="$2"
  local deadline=$((SECONDS + 20)) login="" anon="" auth="" page=""
  local body jar
  PROBE_DIR="$(mktemp -d)"
  body="${PROBE_DIR}/web-auth-body.json"
  jar="${PROBE_DIR}/cookies.txt"
  if ! write_web_auth_body "${body}"; then
    echo "ERROR: could not prepare the authenticated service probe (node failed to write its 0600 body file)." >&2
    return 1
  fi
  chmod 600 "${body}"
  while [ "${SECONDS}" -lt "${deadline}" ]; do
    login="$(curl -sS -o /dev/null -w '%{http_code}' --max-time 2 "http://${host}:${port}/login" 2>/dev/null || true)"
    anon="$(curl -sS -o /dev/null -w '%{http_code}' --max-time 2 "http://${host}:${port}/api/sessions" 2>/dev/null || true)"
    if [ "${login}" = "200" ] && [ "${anon}" = "401" ]; then
      auth="$(curl -sS -o /dev/null -w '%{http_code}' --max-time 2 \
        -H 'Content-Type: application/json' --data-binary "@${body}" -c "${jar}" \
        "http://${host}:${port}/api/web-auth" 2>/dev/null || true)"
      page=""
      if [ "${auth}" = "200" ]; then
        page="$(curl -sS -o /dev/null -w '%{http_code}' --max-time 2 -b "${jar}" \
          "http://${host}:${port}/" 2>/dev/null || true)"
        case "${page}" in
          [1-4][0-9][0-9])
            return 0
            ;;
        esac
      fi
    fi
    sleep 1
  done
  echo "ERROR: pi-web on ${host}:${port} did not answer GET /login with 200, the anonymous API with 401, and an authenticated GET / with a non-5xx within 20s (last: /login ${login:-unreachable}, /api/sessions ${anon:-unreachable}, POST /api/web-auth ${auth:-unreachable}, authenticated / ${page:-unreachable})." >&2
  return 1
}

# The probe runs only when BOTH the port and the password are set: only then
# is there a known endpoint whose anonymous API must answer 401. A loopback
# deploy with a port but no password (allowed: it is not exposed) therefore
# gets no reachability check at all; an operator who wants one sets
# PI_WEB_PASSWORD too, or checks http://127.0.0.1:<port>/ by hand.
if [ -n "${PIWEB_PORT:-}" ] && [ -n "${PI_WEB_PASSWORD:-}" ]; then
  if ! command -v curl >/dev/null 2>&1; then
    echo "ERROR: the service probe needs curl, and curl is not installed; pi-web on port ${LOCAL_PORT} was swapped but not verified." >&2
    exit 3
  fi
  PROBE_HOST="${HOST}"
  case "${PROBE_HOST}" in
    0.0.0.0 | :: | "[::]") PROBE_HOST="127.0.0.1" ;;
  esac
  if ! probe_configured_service "${PROBE_HOST}" "${LOCAL_PORT}"; then
    echo "==> pi-web was installed and its tmux window was swapped, but the configured service is not healthy." >&2
    exit 3
  fi
  echo "==> Service probe OK: http://${PROBE_HOST}:${LOCAL_PORT}/login 200, anonymous /api/sessions 401, authenticated / non-5xx"
fi

# ---------------------------------------------------------------------------
# Served == installed self-check. npm may install into a prefix whose bin dir
# is not on the service PATH, leaving the loop serving an older (or same-
# version, old-prefix) pi-web while the deploy reports success. The loop
# command pins the installed prefix's bin dir itself, and the package
# directory the running loop's Next.js server works in must equal the package
# npm just installed under its global root (`npm root -g`) — directory AND
# version.
# Paths and versions only are printed, never a deploy.env value.
# ---------------------------------------------------------------------------

# The definition lives in bin/pi-web-served.js next to this script (the
# deploy's own copy, so a pinned package that predates it deploys too),
# shared with tools/install.sh --with-pi-web: `served
# <session>` finds the launcher (the first process of the session's 'piweb'
# window with an absolute pi-web script token) and prints
# "<version>|<package dir>|<launcher pid>" of the package directory its
# Next.js server child works in, "unknown|<dir> (deleted)|<pid>"
# when npm has deleted that directory (reported below as served unknown),
# "unidentified" for a process without such a token or a server outside a
# pi-web package, and nothing while the loop has not started a server yet;
# `installed <root>` prints "<version>|<package dir>" npm wrote under its
# global root (INSTALLED_NPM_ROOT), or nothing.
SERVED_HELPER="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/bin/pi-web-served.js"
piweb_served() { node "${SERVED_HELPER}" "$@" 2>/dev/null || true; }

# Bounded: wait (polling every second) for the loop's first live process, then
# compare up to 3 times, all inside one 30 s deadline; the last attempt rules.
# A known install and a different running package is a hard failure: the
# deploy has already swapped the service, so the caller reports state=changed
# (exit 3), never state=deployed. An installed package that cannot be read is
# the same failure.
check_served_installed() {
  local installed_pkg="" installed_version="" serving="" served_dir=""
  local proc="" proc_pkg="" attempts=0 deadline=$((SECONDS + 30))
  installed_pkg="$(piweb_served installed "${INSTALLED_NPM_ROOT}")"
  if [ -z "${installed_pkg}" ]; then
    echo "ERROR: the installed ${PKG} under ${INSTALLED_NPM_ROOT} has no readable package.json; the served pi-web cannot be confirmed." >&2
    return 1
  fi
  installed_version="${installed_pkg%%|*}"
  while [ "${attempts}" -lt 3 ] && [ "${SECONDS}" -lt "${deadline}" ]; do
    proc="$(piweb_served served "${SESSION}")"
    if [ -n "${proc}" ]; then
      attempts=$((attempts + 1))
      proc_pkg=""
      case "${proc}" in
        unidentified) ;;
        *) proc_pkg="${proc%|*}" ;;
      esac
      if [ "${proc_pkg}" = "${installed_pkg}" ]; then
        echo "==> Served == installed: pi-web ${installed_version} (${installed_pkg#*|})"
        return 0
      fi
      [ "${attempts}" -ge 3 ] && break
    fi
    sleep 1
  done
  serving="${proc_pkg%%|*}"
  served_dir="${proc_pkg#*|}"
  [ -n "${serving}" ] || serving="unknown"
  [ -n "${served_dir}" ] || served_dir="unknown"
  echo "ERROR: served pi-web ${serving} (${served_dir}) is not the installed ${installed_version} (${installed_pkg#*|}); the loop is not running the package npm just installed." >&2
  return 1
}

if ! check_served_installed; then
  exit 3
fi

# TLS edge probe: warn-only (the edge is not this script's service), single
# attempt, no retry. Only the operator's own first allowed host is probed;
# tunnel hostnames appended above are not an edge this script fronts.
EDGE_HOST="${PI_WEB_ALLOWED_HOSTS:-}"
EDGE_HOST="${EDGE_HOST%%,*}"
EDGE_HOST="$(printf '%s' "${EDGE_HOST}" | tr -d " \t\"'")"
if [ -z "${EDGE_HOST}" ]; then
  echo "warn: TLS edge probe skipped: PI_WEB_ALLOWED_HOSTS is not set (no edge host to probe)"
elif ! command -v curl >/dev/null 2>&1; then
  echo "warn: TLS edge probe skipped: curl is not installed (${EDGE_HOST})"
else
  EDGE_CODE="$(curl -sS -o /dev/null -w '%{http_code}' --max-time 5 "https://${EDGE_HOST}/" 2>/dev/null)" || EDGE_CODE=""
  if [ -n "${EDGE_CODE}" ] && [ "${EDGE_CODE}" != "000" ]; then
    echo "==> TLS edge probe OK: https://${EDGE_HOST}/ answered (${EDGE_CODE})"
  else
    echo "warn: TLS edge unreachable (${EDGE_HOST})"
  fi
fi

echo "==> pi-web running in tmux session '${SESSION}'"
tmux ls
