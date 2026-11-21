#!/bin/sh
# dist/workstation/upgrade-smoke.sh — the in-place component upgrade's
# acceptance driver (polyforge#190, AC1), run by `smoke.sh upgrade`. It
# boots ONE workstation under this run's own compose project (the #177
# isolation: this run's project name, host port and volumes, never a
# fixed container name), lets the first boot seed the components volume
# from the image (current = v0.2.4-dev.1), then upgrades in place to a
# second seed image built from the same tree with
# SEED_VERSION=v0.2.4-dev.2 and proves, on the real machine:
#
#   - /components/current → releases/v0.2.4-dev.2 and previous → the
#     seeded version (the install transaction's own commit point);
#   - pf version, the daemon's /status version, the volume-backed
#     aihub's --version and the SERVING pi-web process (its resolved
#     cwd under the new release and its package version) are all the
#     new version — no reachability stand-in for a version assertion;
#   - the container Id and PID 1's start time never changed (the
#     container was upgraded, not rebuilt);
#   - /components/upgrade-status.json records the done outcome.
#
# The two seed images are one tree one patch apart — the release the
# upgrade installs. A published release carries a floor the running base
# satisfies; this synthetic pair does not, so the writable copy of the
# new seed names the running base explicitly (the #86 release pipeline
# owns the real floors).
#
# Prerequisites: a reachable docker engine with compose, curl and
# python3. Every poll below is bounded (poll_until); the EXIT trap
# downs this run's project WITH VOLUMES on every path, happy or die.
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
root=$(CDPATH= cd -- "$script_dir/../.." && pwd)
compose_file="$script_dir/compose.yaml"
smoke_run="$(date +%s).$$"
project="workstation-upgrade-$smoke_run"
web_host_port=$((30500 + ( ($(date +%s) + $$) % 900 )))
old=v0.2.4-dev.1
new=v0.2.4-dev.2
password="${PI_WEB_PASSWORD:-pf-workstation-smoke-test-password}"
# the compose file's ${PI_WEB_PASSWORD:?…} refuses without one, and
# this run's own port keeps a concurrent smoke off ours.
export PI_WEB_PASSWORD="$password"
export WORKSTATION_WEB_PORT="$web_host_port"
work=$(mktemp -d)
seed_container=""

say() { printf 'smoke: %s\n' "$*"; }
die() { printf 'smoke: FAIL: %s\n' "$*" >&2; exit 1; }

# this run's own project, both compose files, the default (aihub) boot.
compose() { docker compose -p "$project" -f "$compose_file" -f "$work/override.yaml" --profile aihub "$@"; }

# the EXIT traps' cleanup: `down -v` with ONE retry — a loaded engine
# can refuse a single down (the #177 measurement), and this run's
# project is disposable, so every path takes it down WITH VOLUMES.
compose_down_v() {
	"$@" down -v >/dev/null 2>&1 || "$@" down -v >/dev/null 2>&1 || true
}
cleanup() {
	compose_down_v compose
	if [ -n "$seed_container" ]; then docker rm -f "$seed_container" >/dev/null 2>&1 || true; fi
	rm -rf "$work"
}
trap cleanup EXIT HUP INT TERM

# poll_until WHAT SECONDS CMD... — a bounded poll that re-runs CMD
# every 2s until it succeeds or the deadline passes.
poll_until() {
	what=$1
	deadline=$2
	shift 2
	waited=0
	while :; do
		if "$@" >/dev/null 2>&1; then
			return 0
		fi
		if [ "$waited" -ge "$deadline" ]; then
			die "timed out after ${waited}s waiting for $what"
		fi
		sleep 2
		waited=$((waited + 2))
	done
}

require_engine() {
	docker version >/dev/null 2>&1 || die "no reachable docker engine (docker version failed)"
	docker compose version >/dev/null 2>&1 || die "docker compose is not available"
	command -v curl >/dev/null 2>&1 || die "curl is not on PATH"
	command -v python3 >/dev/null 2>&1 || die "python3 is not on PATH"
}

running() { [ "$(docker inspect -f '{{.State.Running}}' "$cid")" = "true" ]; }
login() { [ "$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$web_host_port/login")" = "200" ]; }

require_engine

# ---- the second seed image: the same tree, one patch ahead.
say "upgrade: building the $new seed image"
docker build -f "$script_dir/Containerfile" --build-arg "SEED_VERSION=$new" -t "$project-new" "$root" >/dev/null
seed_container=$(docker create "$project-new")
mkdir -p "$work/new-seed"
docker cp "$seed_container:/opt/polyforge/seed/." "$work/new-seed"
docker rm "$seed_container" >/dev/null
seed_container=""

# ---- this run's own workstation; the first boot seeds the components
# volume from the image (current = $old).
say "upgrade: booting this run's workstation (project $project, port $web_host_port)"
cat > "$work/override.yaml" <<YAML
services:
  workstation:
    volumes:
      - $work/new-seed:/upgrade-smoke-seed:ro
YAML
compose build >/dev/null
compose up -d >/dev/null
cid=$(compose ps -q workstation)
[ -n "$cid" ] || die "no workstation container was created"
poll_until "the workstation container to run" 240 running
poll_until "Pi Web /login to answer 200" 300 login

# ---- the pre-upgrade facts.
before_id=$(docker inspect -f '{{.Id}}' "$cid")
before_pid=$(docker exec "$cid" sh -c 'cut -d" " -f22 /proc/1/stat')
[ -n "$before_pid" ] || die "PID 1's start time could not be read"
before_current=$(docker exec "$cid" readlink /components/current)
[ "$before_current" = "releases/$old" ] ||
	die "the first boot did not seed current to $old: $before_current"

# ---- the upgrade target: a writable copy of the new seed, its floor
# naming the running base (a published release carries a real floor).
docker exec "$cid" sh -c 'cp -a /upgrade-smoke-seed/releases/v0.2.4-dev.2 /components/smoke-target'
docker exec "$cid" python3 -c 'import json; p="/components/smoke-target/manifest.json"; m=json.load(open(p)); m["minBase"]="v0.2.4-dev.1"; json.dump(m, open(p, "w"))'

# ---- the in-container upgrade itself.
say "upgrade: pf components upgrade --version $new --seed-dir /components/smoke-target"
docker exec "$cid" pf components upgrade --version "$new" --seed-dir /components/smoke-target

# ---- AC1's assertions. The pointers:
[ "$(docker exec "$cid" readlink /components/current)" = "releases/$new" ] ||
	die "current did not switch to $new"
[ "$(docker exec "$cid" readlink /components/previous)" = "releases/$old" ] ||
	die "previous did not keep the old version"
[ "$(docker exec "$cid" pf version)" = "$new" ] ||
	die "pf version is not $new"

# The serving processes, each by its own version fact — the daemon's
# /status, the volume-backed aihub binary and the pi-web process
# serving out of the new release tree (its resolved cwd), never a
# reachability stand-in.
poll_until "the daemon, aihub and pi-web on $new" 240 \
	docker exec "$cid" python3 -c '
import json, os, socket, subprocess, http.client
class UnixHTTP(http.client.HTTPConnection):
    def connect(self):
        self.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self.sock.settimeout(2)
        self.sock.connect("/root/.local/share/polyforge/daemon.sock")
c = UnixHTTP("daemon")
c.request("GET", "/status")
d = json.loads(c.getresponse().read())
assert d["version"] == "v0.2.4-dev.2", d
found = False
for name in os.listdir("/proc"):
    if not name.isdigit():
        continue
    try:
        if b"aihub" not in open("/proc/" + name + "/cmdline", "rb").read():
            continue
        exe = os.readlink("/proc/" + name + "/exe")
        if not exe.startswith("/components/releases/"):
            continue
        out = subprocess.check_output([exe, "aihub", "--version"], timeout=2).decode().strip()
        assert out == "v0.2.4-dev.2", (exe, out)
        found = True
    except (FileNotFoundError, ProcessLookupError, PermissionError):
        continue
assert found, "no aihub process runs from the components volume"
web = json.load(open("/components/current/pi-web/package.json"))
assert web["version"] == "0.2.4-dev.2", web
serving = False
for name in os.listdir("/proc"):
    if not name.isdigit():
        continue
    try:
        argv = open("/proc/" + name + "/cmdline", "rb").read()
        if b"next" not in argv and b"node" not in argv:
            continue
        cwd = os.readlink("/proc/" + name + "/cwd")
        if cwd.startswith("/components/releases/v0.2.4-dev.2/pi-web"):
            serving = True
    except (FileNotFoundError, ProcessLookupError, PermissionError):
        continue
assert serving, "no pi-web process serves out of the new release"
'
poll_until "Pi Web to serve again after its own restart" 300 login

# ---- the identity: the container was upgraded, never rebuilt.
[ "$(docker inspect -f '{{.Id}}' "$cid")" = "$before_id" ] ||
	die "the container was rebuilt (its Id changed)"
[ "$(docker exec "$cid" sh -c 'cut -d" " -f22 /proc/1/stat')" = "$before_pid" ] ||
	die "PID 1 changed (its start time moved)"

# ---- the persisted outcome the page replays after reconnecting.
docker exec "$cid" python3 -c 'import json; s=json.load(open("/components/upgrade-status.json")); assert s["state"]=="done" and s["to"]=="v0.2.4-dev.2", s'

say "upgrade: PASS"
