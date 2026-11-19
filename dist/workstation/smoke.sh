#!/bin/sh
# dist/workstation/smoke.sh — the workstation acceptance driver
# (polyforge#161, 84-image). Three subcommands, each printing every
# measured value and exiting non-zero on the first miss:
#
#   sh dist/workstation/smoke.sh boot --no-aihub    # the minimal boot (AC1)
#   sh dist/workstation/smoke.sh boot --with-aihub  # + AIHub/Pi Web checks (AC1)
#   sh dist/workstation/smoke.sh env                # Pi Web parameters (AC3)
#   sh dist/workstation/smoke.sh podman-verify      # verification via podman (AC2)
#
# Prerequisites: a reachable docker engine with compose (the outer
# engine's CLI shape; podman-compose hosts are a follow-up), curl, and
# python3 on the host. `boot --with-aihub` and `env` additionally need
# the data volume to carry the release layout's components
# (<data-dir>/release/current/pi-web — 84-components); on an empty
# volume they fail naming that, which is the honest answer: the AIHub
# and env halves of the acceptance cannot hold without the components
# the workstation is designed to run.
#
# PI_WEB_PASSWORD defaults to the fixed smoke test password (settled
# Q5); every poll below is bounded (poll_until), never a fixed delay.
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
root=$(CDPATH= cd -- "$script_dir/../.." && pwd)
compose_file="$script_dir/compose.yaml"
web_host_port="${WORKSTATION_WEB_PORT:-30141}"
password="${PI_WEB_PASSWORD:-pf-workstation-smoke-test-password}"
# the compose file's ${PI_WEB_PASSWORD:?…} refuses without one, so every
# compose call this script makes carries the default (or the operator's
# own value) — exported once here, not per subcommand.
export PI_WEB_PASSWORD="$password"

say() { printf 'smoke: %s\n' "$*"; }
die() { printf 'smoke: FAIL: %s\n' "$*" >&2; exit 1; }

# compose runs the compose file's own project (name: workstation) and
# resolves its relative paths (the seccomp file, the build context)
# against the compose file's directory, so the caller's cwd is free.
compose() { docker compose -f "$compose_file" "$@"; }

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

# the smoke needs the docker CLI's inspect shape on the outer engine
require_engine() {
	docker version --format '{{.Server.Version}}' >/dev/null 2>&1 ||
		die "no reachable docker engine (docker version failed)"
	docker compose version >/dev/null 2>&1 ||
		die "docker compose is not available"
	command -v curl >/dev/null 2>&1 || die "curl is not on PATH"
	command -v python3 >/dev/null 2>&1 || die "python3 is not on PATH"
}

# service_of PROFILE — the compose service name of a boot mode.
service_of() {
	if [ "$1" = aihub ]; then
		echo workstation-aihub
	else
		echo workstation
	fi
}

# boot_up COMPOSE-ARGS... — up (building the image), wait for the
# supervise tree, answer nothing; the caller resolves cid afterwards.
boot_up() {
	say "boot: building the image and starting the $profile profile"
	# build is an explicit step, never `up --build`: measured once on
	# compose v5, `up --build` left a stale image tagged when the
	# Containerfile had changed, and the boot then proved the wrong
	# tree. The build is layer-cached, so the explicit step is cheap.
	compose --profile "$profile" build >/dev/null
	compose --profile "$profile" up -d >/dev/null
	cid=$(compose --profile "$profile" ps -q "$(service_of "$profile")")
	[ -n "$cid" ] || die "no workstation container was created for profile $profile"
	poll_until "the workstation container to run" 120 \
		sh -c "docker inspect -f '{{.State.Running}}' '$cid' | grep -q true"
	poll_until "pf supervise to run inside" 120 \
		docker exec "$cid" sh -c 'ps -e -o args= | grep -q "[p]f supervise"'
}

# the AC1 parameter inspection: every value printed, the first miss
# ends the run. The seccomp compare is by CONTENT hash — compose hands
# the engine the profile's bytes (docker compose inlines the JSON;
# other providers may pass the path), so both sides are canonicalized
# (sorted keys, compact) before hashing.
inspect_params() {
	inspect_json=$(mktemp)
	docker inspect "$cid" > "$inspect_json"
	python3 - "$inspect_json" "$script_dir/seccomp.json" <<'PYEOF' || die "the parameter inspection failed (see the values above)"
import hashlib, json, sys

inspect_path, pinned_path = sys.argv[1], sys.argv[2]
raw = json.load(open(inspect_path))
h = raw[0]["HostConfig"]
fails = []

def canon(obj):
    return json.dumps(obj, sort_keys=True, separators=(",", ":"))

def sha(obj):
    return hashlib.sha256(canon(obj).encode()).hexdigest()[:16]

caps = sorted(h.get("CapAdd") or [])
if caps != ["CAP_NET_ADMIN", "CAP_SYS_ADMIN"]:
    fails.append("CapAdd=%s (want exactly CAP_NET_ADMIN,CAP_SYS_ADMIN)" % caps)
print("smoke: CapAdd = %s" % caps)

seccomp = None
for opt in h.get("SecurityOpt") or []:
    if opt.startswith("seccomp="):
        value = opt[len("seccomp="):]
        seccomp = json.loads(value) if value.lstrip().startswith("{") \
            else json.loads(open(value).read())
if seccomp is None:
    fails.append("no seccomp= entry in SecurityOpt: %s" % (h.get("SecurityOpt"),))
else:
    pinned = json.load(open(pinned_path))
    got, want = sha(seccomp), sha(pinned)
    if got != want:
        fails.append("seccomp content hash %s != pinned %s" % (got, want))
    print("smoke: seccomp content hash = %s (pinned %s)" % (got, want))

cgroupns = h.get("CgroupnsMode", "")
if cgroupns != "private":
    fails.append("CgroupnsMode=%s (want private)" % cgroupns)
print("smoke: CgroupnsMode = %s" % cgroupns)

if h.get("Privileged"):
    fails.append("Privileged=true")
print("smoke: Privileged = %s" % h.get("Privileged"))

devices = h.get("Devices") or []
if devices:
    fails.append("Devices=%s" % devices)
print("smoke: Devices = %s" % devices)

binds = (h.get("Binds") or []) + [m.get("Source", "") or "" for m in h.get("Mounts") or []]
if any("docker.sock" in b for b in binds):
    fails.append("a docker.sock is mounted: %s" % binds)
print("smoke: no docker.sock in %d bind(s)" % len(binds))

if fails:
    for f in fails:
        print("smoke: FAIL: %s" % f)
    sys.exit(1)
PYEOF
	rm -f "$inspect_json"
	say "parameter inspection: CapAdd set, seccomp content hash, private cgroupns, no privileged/devices/docker.sock"
}

cmd_boot() {
	case "${1:-}" in
	--no-aihub) profile=no-aihub ;;
	--with-aihub) profile=aihub ;;
	*) die "boot needs --no-aihub or --with-aihub" ;;
	esac
	require_engine
	boot_up

	inspect_params
	p1=$(docker exec "$cid" cat /proc/1/comm)
	printf 'smoke: PID 1 comm = %s\n' "$p1"
	echo "$p1" | grep -qx catatonit || die "PID 1 is $p1, want catatonit"

	controllers=$(docker exec "$cid" cat /sys/fs/cgroup/cgroup.subtree_control)
	printf 'smoke: cgroup subtree_control = %s\n' "$controllers"
	echo "$controllers" | grep -qw memory || die "the memory controller is not enabled"
	echo "$controllers" | grep -qw pids || die "the pids controller is not enabled"

	if [ "$profile" = no-aihub ]; then
		# the default boot starts no aihub server at all
		if docker exec "$cid" sh -c 'ps -e -o args= | grep -q "[p]f aihub"'; then
			die "a pf aihub process is running in the --no-aihub boot"
		fi
		say "no pf aihub process in the --no-aihub boot"
	else
		# the aihub boot: Pi Web answers /login with 200 (password set,
		# not authenticated), the daemon answers the queue, and the
		# default Cell points at the local AIHub. Pi Web is a component:
		# an empty data volume cannot boot it.
		poll_until "Pi Web /login to answer 200" 300 \
			sh -c "curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:$web_host_port/login | grep -q 200" ||
			die "Pi Web never answered 200 on /login — the data volume must carry <data-dir>/release/current/pi-web (84-components)"
		code=$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$web_host_port/login")
		printf 'smoke: Pi Web /login = %s\n' "$code"
		docker exec "$cid" pf --json queue >/dev/null ||
			die "pf --json queue failed inside the workstation"
		say "pf --json queue exit 0"
		cell=$(docker exec "$cid" pf config get server 2>/dev/null || true)
		printf 'smoke: default Cell server = %s\n' "${cell:-<unset>}"
		case "$cell" in
		*127.0.0.1* | *localhost*) ;;
		*) die "the default Cell does not point at the local AIHub: $cell" ;;
		esac
	fi

	compose --profile "$profile" down >/dev/null
	say "boot $1: PASS"
}

cmd_env() {
	require_engine
	profile=no-aihub
	override=$(mktemp)
	cat > "$override" <<'YEOF'
services:
  workstation:
    environment:
      PI_WEB_APP_NAME: "pf-workstation-smoke-app"
      PI_SMOKE_PROBE: "passed-through"
      PI_TEST_SMOKE_PROBE: "must-not-cross"
YEOF
	say "env: booting with the probe variables"
	# the sentinel travels only in the smoke's own shell environment:
	# compose interpolates nothing unlisted, so its absence inside is
	# the proof the container inherits no host environment.
	SENTINEL_WORKSTATION_SMOKE=yes \
		docker compose -f "$compose_file" -f "$override" --profile "$profile" build >/dev/null
	docker compose -f "$compose_file" -f "$override" --profile "$profile" up -d >/dev/null
	cid=$(docker compose -f "$compose_file" -f "$override" --profile "$profile" ps -q "$(service_of "$profile")")
	[ -n "$cid" ] || die "no workstation container"
	poll_until "pf supervise to run inside" 120 \
		docker exec "$cid" sh -c 'ps -e -o args= | grep -q "[p]f supervise"'
	poll_until "the Pi Web component to run" 300 \
		docker exec "$cid" sh -c 'ps -e -o args= | grep -q "[s]tart:lan"' ||
		die "Pi Web never started — the data volume must carry <data-dir>/release/current/pi-web (84-components)"
	webpid=$(docker exec "$cid" sh -c "ps -e -o pid=,args= | grep '[s]tart:lan' | awk '{print \$1}' | head -1")
	environ=$(docker exec "$cid" sh -c "cat /proc/$webpid/environ" | tr '\0' '\n')
	printf '%s\n' "$environ" | grep -qx 'PORT=30141' ||
		die "PORT did not pass through to Pi Web"
	say "PORT passed through"
	printf '%s\n' "$environ" | grep -qx 'PI_WEB_APP_NAME=pf-workstation-smoke-app' ||
		die "the PI_WEB_* probe variable did not pass through"
	printf '%s\n' "$environ" | grep -qx 'PI_SMOKE_PROBE=passed-through' ||
		die "the PI_* probe variable did not pass through"
	say "PI_* / PI_WEB_* passed through"
	if printf '%s\n' "$environ" | grep -q '^PI_TEST_'; then
		die "a PI_TEST_* variable crossed the boundary"
	fi
	say "no PI_TEST_* crossed"
	if printf '%s\n' "$environ" | grep -q '^SENTINEL_WORKSTATION_SMOKE='; then
		die "the host shell's sentinel variable reached Pi Web — the container inherits the host environment"
	fi
	say "the host environment did not leak in"
	code=$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$web_host_port/api")
	printf 'smoke: anonymous /api = %s\n' "$code"
	[ "$code" = 401 ] || die "anonymous /api is $code, want 401"
	docker compose -f "$compose_file" -f "$override" --profile "$profile" down >/dev/null
	rm -f "$override"
	say "env: PASS"
}

cmd_podman_verify() {
	require_engine
	profile=no-aihub
	override=$(mktemp)
	sed -e "s|@ROOT@|$root|g" > "$override" <<'YEOF'
services:
  workstation:
    volumes:
      - "@ROOT@:/workspace:ro"
YEOF
	say "podman-verify: booting with the repository mounted at /workspace"
	docker compose -f "$compose_file" -f "$override" --profile "$profile" build >/dev/null
	docker compose -f "$compose_file" -f "$override" --profile "$profile" up -d >/dev/null
	cid=$(docker compose -f "$compose_file" -f "$override" --profile "$profile" ps -q "$(service_of "$profile")")
	[ -n "$cid" ] || die "no workstation container"
	poll_until "pf supervise to run inside" 120 \
		docker exec "$cid" sh -c 'ps -e -o args= | grep -q "[p]f supervise"'

	# docker exec bypasses the entrypoint's whitelist, so the build
	# isolation is restated here exactly as the entrypoint fixes it (the
	# daemon path gets it through its allow-listed environment).
	in_container() { docker exec -e BUILDAH_ISOLATION=chroot "$cid" sh -c "$1"; }

	say "podman-verify: pulling the short-name probe image"
	in_container 'podman pull -q busybox:1.37' >/dev/null ||
		die "podman pull busybox:1.37 failed (short-name resolution)"

	say "podman-verify: a probe container with the verification limits"
	limits=$(in_container 'timeout 90 podman run --rm --memory 64m --pids-limit 16 busybox:1.37 sh -c "echo memory=\$(cat /sys/fs/cgroup/memory.max) pids=\$(cat /sys/fs/cgroup/pids.max); wget -qO- http://example.com >/dev/null && echo net=ok"')
	printf 'smoke: probe container: %s\n' "$limits"
	echo "$limits" | grep -q 'memory=67108864' || die "memory.max did not take (want 67108864)"
	echo "$limits" | grep -q 'pids=16' || die "pids.max did not take (want 16)"
	echo "$limits" | grep -q 'net=ok' || die "the default bridge has no network"

	say "podman-verify: the #64 prepared-image path (local image, no pull)"
	prepared=$(in_container 'timeout 90 podman run --pull=never --rm busybox:1.37 echo prepared-path-ok') ||
		die "the prepared-image run failed"
	printf 'smoke: prepared-image path: %s\n' "$prepared"

	say "podman-verify: the real Dockerfile.verify build through the engine's own path"
	driver=$(mktemp)
	cat > "$driver" <<'PYEOF'
import sys

sys.path.insert(0, "/workspace/workflows/_shared/scripts")
from pfstep import container

dockerfile = open("/workspace/.polyforge/Dockerfile.verify", "rb").read()
engine = container.engine_for_env(
    {"PF_CONTAINER_ENGINE": "podman", "PATH": "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"}
)
with open("/tmp/smoke-verify-build.log", "wb") as log:
    engine.build_image("pf-smoke-verify:local", dockerfile, {container.LABEL_REPO: "polyforge"}, log, 2400.0)
print("Dockerfile.verify build done")
PYEOF
	docker exec -i "$cid" sh -c 'cat > /tmp/smoke-verify-driver.py' < "$driver"
	in_container 'python3 /tmp/smoke-verify-driver.py' ||
		die "the Dockerfile.verify build failed (log: /tmp/smoke-verify-build.log inside the workstation)"
	rm -f "$driver"
	say "Dockerfile.verify built through PodmanCLI.build_image"

	say "podman-verify: the three real-container modules, 0 skips allowed"
	docker exec -e PF_CONTAINER_ENGINE=podman -e BUILDAH_ISOLATION=chroot "$cid" \
		sh -c 'cd /workspace/workflows/_shared/scripts && python3 -B -m unittest -v tests.test_container_docker tests.test_gate_docker tests.test_cache_volume' \
		> /tmp/smoke-podman-modules.log 2>&1 ||
		{ tail -20 /tmp/smoke-podman-modules.log >&2; die "the three real-container modules failed (log: /tmp/smoke-podman-modules.log)"; }
	skips=$(grep -c 'skipped' /tmp/smoke-podman-modules.log || true)
	[ "$skips" = 0 ] || { grep skipped /tmp/smoke-podman-modules.log >&2; die "$skips test(s) skipped — the acceptance demands 0"; }
	tail -1 /tmp/smoke-podman-modules.log | grep -q '^OK$' ||
		die "the unittest run did not end OK"
	say "three modules green, 0 skips"

	say "podman-verify: cleanliness"
	residual=$(in_container 'podman ps -a --format "{{.Names}}"')
	[ -z "$residual" ] || die "residual containers: $residual"
	zombies=$(in_container 'ps -e -o stat= | grep -c Z || true')
	[ "$zombies" = 0 ] || die "$zombies zombie process(es) survive"
	conmon=$(in_container 'ps -e -o args= | grep -c "[c]onmon" || true')
	[ "$conmon" = 0 ] || die "$conmon conmon process(es) still run"
	pod=$(in_container 'ps -e -o args= | grep -c "[p]odman" || true')
	[ "$pod" = 0 ] || die "podman stays resident: $pod process(es)"
	say "no residual containers, no zombies, no resident conmon/podman"

	docker compose -f "$compose_file" -f "$override" --profile "$profile" down >/dev/null
	rm -f "$override"
	say "podman-verify: PASS"
}

case "${1:-}" in
boot) shift; cmd_boot "$@" ;;
env) shift; cmd_env "$@" ;;
podman-verify) shift; cmd_podman_verify "$@" ;;
*)
	cat >&2 <<'USAGE'
usage: sh dist/workstation/smoke.sh boot --no-aihub|--with-aihub
       sh dist/workstation/smoke.sh env
       sh dist/workstation/smoke.sh podman-verify
USAGE
	exit 2
	;;
esac
