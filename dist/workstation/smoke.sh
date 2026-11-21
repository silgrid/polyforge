#!/bin/sh
# dist/workstation/smoke.sh — the workstation acceptance driver
# (polyforge#161, 84-image). Four subcommands, each printing every
# measured value and exiting non-zero on the first miss:
#
#   sh dist/workstation/smoke.sh boot --no-aihub    # the minimal boot (AC1)
#   sh dist/workstation/smoke.sh boot --with-aihub  # + AIHub/Pi Web checks (AC1)
#   sh dist/workstation/smoke.sh first-boot         # the 84-components-C boot: identity,
#                                                   #   default Cell, real auth, the AC2 layout,
#                                                   #   restart (AC1+AC2)
#   sh dist/workstation/smoke.sh first-boot --external  # + zero remote writes
#   sh dist/workstation/smoke.sh persist            # the 84-components-D acceptance:
#                                                   #   down/up and a new tag keep the six
#                                                   #   persisted facts, then the exported
#                                                   #   copy runs (needs only Docker + sh)
#   sh dist/workstation/smoke.sh env                # Pi Web parameters (AC3)
#   sh dist/workstation/smoke.sh podman-verify      # verification via podman (AC2)
#
# Prerequisites: a reachable docker engine with compose (the outer
# engine's CLI shape; podman-compose hosts are a follow-up). first-boot's
# local pass and persist need nothing else on the host (Q6: every JSON /
# HTTP check runs inside the container, which carries curl and python3);
# boot --with-aihub, env, podman-verify and first-boot --external still
# probe from the host and need curl and python3 there. `boot --with-aihub` and `env` additionally need
# the components volume to carry Pi Web (/components/current/pi-web —
# 84-components, installed by the first boot from the image's seed); on
# an empty volume they fail naming that, which is the honest answer: the
# AIHub and env halves of the acceptance cannot hold without the
# components the workstation is designed to run.
#
# PI_WEB_PASSWORD defaults to the fixed smoke test password (settled
# Q5); every poll below is bounded (poll_until), never a fixed delay.
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
root=$(CDPATH= cd -- "$script_dir/../.." && pwd)
compose_file="$script_dir/compose.yaml"
password="${PI_WEB_PASSWORD:-pf-workstation-smoke-test-password}"
# the compose file's ${PI_WEB_PASSWORD:?…} refuses without one, so every
# compose call this script makes carries the default (or the operator's
# own value) — exported once here, not per subcommand.
export PI_WEB_PASSWORD="$password"

say() { printf 'smoke: %s\n' "$*"; }
die() { printf 'smoke: FAIL: %s\n' "$*" >&2; exit 1; }

# compose runs under the SMOKE'S OWN project, unique per run
# (polyforge#176 review r1#4; polyforge#177): the compose file names the
# project `workstation` and its six volumes are a real deployment's
# persistent home, so every call runs under -p "$smoke_project" — a
# name derived from the clock and the pid, so `down -v` (the first-boot
# pass resets volumes) only ever touches THIS run's volumes, never an
# operator's and never a concurrent run's. The host port is derived the
# same way (the fixed 30141 would clash with a running deployment) unless
# WORKSTATION_WEB_PORT overrides it; compose has no fixed container_name
# any more, so nothing else can clash either. Relative paths (the
# seccomp file, the build context) resolve against the compose file's
# directory, so the caller's cwd is free.
smoke_run="smoke-$(date +%s)-$$"
smoke_project="workstation-$smoke_run"
web_host_port="${WORKSTATION_WEB_PORT:-$((30500 + ( ($(date +%s) + $$) % 900 )))}"
export WORKSTATION_WEB_PORT="$web_host_port"
compose() { docker compose -p "$smoke_project" -f "$compose_file" "$@"; }

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

# the smoke needs the docker CLI's inspect shape on the outer engine.
# The host needs nothing else for persist and the exported runs
# (polyforge#177, Q6: every JSON/HTTP check runs inside the container,
# which carries curl and python3); the passes that still probe from the
# host demand their own tools below.
require_engine() {
	docker version --format '{{.Server.Version}}' >/dev/null 2>&1 ||
		die "no reachable docker engine (docker version failed)"
	docker compose version >/dev/null 2>&1 ||
		die "docker compose is not available"
}

# the host-side tools of the passes that still use them (boot's HTTP
# probe, env, podman-verify, first-boot's record check and the external
# stub server)
require_host_tools() {
	command -v curl >/dev/null 2>&1 || die "curl is not on PATH"
	command -v python3 >/dev/null 2>&1 || die "python3 is not on PATH"
}

# service_of PROFILE — the compose service name of a boot mode.
service_of() {
	if [ "$1" = aihub ]; then
		echo workstation
	else
		echo workstation-noaihub
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
	require_host_tools
	# the whole run's cleanup (review r1#3): this run's project is
	# disposable, so even a die mid-run takes it down WITH VOLUMES
	trap 'compose --profile "$profile" down -v >/dev/null 2>&1 || true' EXIT
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
			die "Pi Web never answered 200 on /login — the components volume must carry /components/current/pi-web (84-components)"
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

	compose --profile "$profile" down -v >/dev/null
	say "boot $1: PASS"
}

cmd_env() {
	require_engine
	require_host_tools
	profile=no-aihub
	override=$(mktemp)
	# this run's project is disposable (review r1#3): the trap covers a die
	trap 'docker compose -p "$smoke_project" -f "$compose_file" -f "$override" down -v >/dev/null 2>&1 || true' EXIT
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
	# the proof the container inherits no host environment. Exported, not
	# prefixed per command — it must ride the `up` that CREATES the
	# container, not just the build (review r2#2).
	export SENTINEL_WORKSTATION_SMOKE=yes
	docker compose -p "$smoke_project" -f "$compose_file" -f "$override" --profile "$profile" build >/dev/null
	docker compose -p "$smoke_project" -f "$compose_file" -f "$override" --profile "$profile" up -d >/dev/null
	cid=$(docker compose -p "$smoke_project" -f "$compose_file" -f "$override" --profile "$profile" ps -q "$(service_of "$profile")")
	[ -n "$cid" ] || die "no workstation container"
	poll_until "pf supervise to run inside" 120 \
		docker exec "$cid" sh -c 'ps -e -o args= | grep -q "[p]f supervise"'
	poll_until "the Pi Web component to run" 300 \
		docker exec "$cid" sh -c 'ps -e -o args= | grep -q "[s]tart:lan"' ||
		die "Pi Web never started — the components volume must carry /components/current/pi-web (84-components)"
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
	docker compose -p "$smoke_project" -f "$compose_file" -f "$override" --profile "$profile" down -v >/dev/null
	rm -f "$override"
	say "env: PASS"
}

cmd_podman_verify() {
	require_engine
	require_host_tools
	profile=no-aihub
	override=$(mktemp)
	# this run's project is disposable (review r1#3): the trap covers a die
	trap 'docker compose -p "$smoke_project" -f "$compose_file" -f "$override" down -v >/dev/null 2>&1 || true' EXIT
	sed -e "s|@ROOT@|$root|g" > "$override" <<'YEOF'
services:
  workstation:
    volumes:
      - "@ROOT@:/workspace:ro"
YEOF
	say "podman-verify: booting with the repository mounted at /workspace"
	docker compose -p "$smoke_project" -f "$compose_file" -f "$override" --profile "$profile" build >/dev/null
	docker compose -p "$smoke_project" -f "$compose_file" -f "$override" --profile "$profile" up -d >/dev/null
	cid=$(docker compose -p "$smoke_project" -f "$compose_file" -f "$override" --profile "$profile" ps -q "$(service_of "$profile")")
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

	docker compose -p "$smoke_project" -f "$compose_file" -f "$override" --profile "$profile" down -v >/dev/null
	rm -f "$override"
	say "podman-verify: PASS"
}

# cmd_first_boot [--external] — the 84-components-C acceptance
# (polyforge#176): a first boot on EMPTY volumes (the project's volumes
# are reset first — this subcommand owns its run), then the record, the
# default Cell, the key's REAL authentication (Q3: keyReady only says
# issued-and-written; the proof is `pf recall` inside the container,
# against the local AIHub the default profile runs), the absence of a
# bootstrap-admin key (the identity predates the server, so the
# server's own bootstrap has nothing left to seed), and — after a
# restart with the volumes intact — the first choice stands: the
# record is byte-identical and the same key still authenticates (no
# reissue). The --external pass points the default Cell at a counting
# stub server instead: only POST /api/v1/machines/heartbeat is allowed
# throughout the run; creation and unknown requests fail. A restart with
# a DIFFERENT server env changes nothing (the choice is made once).
cmd_first_boot() {
	case "${1:-}" in
	"") external=no ;;
	--external) external=yes ;;
	*) die "first-boot takes no flags (or --external)" ;;
	esac
	require_engine
	# only the external pass needs host tools (its counting stub server
	# is a host python3 process); the local pass's every JSON/HTTP check
	# runs inside the container (polyforge#177 Q6)
	if [ "$external" = yes ]; then
		require_host_tools
	fi
	profile=aihub
	if [ "$external" = yes ]; then
		stub_port="${WORKSTATION_SMOKE_STUB_PORT:-38421}"
		hits=$(mktemp)
		stub_script=$(mktemp)
		cat >"$stub_script" <<'PYEOF'
import http.server, sys
port, hits = int(sys.argv[1]), sys.argv[2]
class Count(http.server.BaseHTTPRequestHandler):
    def note(self):
        with open(hits, "a") as f:
            f.write(self.command + " " + self.path + "\n")
        self.send_response(401)
        self.end_headers()
    do_GET = do_POST = do_PUT = do_PATCH = do_DELETE = note
http.server.HTTPServer(("0.0.0.0", port), Count).serve_forever()
PYEOF
		python3 "$stub_script" "$stub_port" "$hits" &
		stub_pid=$!
		override=$(mktemp)
		cat >"$override" <<'YEOF'
services:
  workstation:
    extra_hosts:
      - "host.docker.internal:host-gateway"
YEOF
		# the EXIT trap is the whole cleanup: a die anywhere below
		# leaves no temp file behind (the test env checks for them) and
		# takes this run's project down with volumes (review r1#3)
		trap 'kill "$stub_pid" 2>/dev/null; rm -f "$hits" "$stub_script" "$override" 2>/dev/null; compose_ext down -v >/dev/null 2>&1 || true' EXIT
		poll_until "the stub server to listen" 30 \
			sh -c "curl -s -o /dev/null http://127.0.0.1:$stub_port/" || {
			die "the stub server never listened on $stub_port (set WORKSTATION_SMOKE_STUB_PORT to a free port)"
		}
		: >"$hits"  # the listen probe was ours, not the container's
		export WORKSTATION_AIHUB_SERVER="http://host.docker.internal:$stub_port"
		export WORKSTATION_AIHUB_KEY="smoke-external-key"
		compose_ext() {
			docker compose -p "$smoke_project" -f "$compose_file" -f "$override" --profile "$profile" "$@"
		}
	else
		compose_ext() {
			docker compose -p "$smoke_project" -f "$compose_file" --profile "$profile" "$@"
		}
		# the local run's project is disposable (review r1#3): the trap
		# covers a die (the external branch keeps its own trap above,
		# which also kills the counting stub and its temp files)
		trap 'compose_ext down -v >/dev/null 2>&1 || true' EXIT
	fi

	say "first-boot: resetting the volumes and starting the $profile profile"
	compose --profile "$profile" down -v >/dev/null 2>&1 || true
	ensure_image "$profile"
	compose_ext up -d >/dev/null
	cid=$(compose_ext ps -q workstation)
	[ -n "$cid" ] || die "no workstation container was created for profile $profile"
	poll_until "the workstation container to run" 120 \
		sh -c "docker inspect -f '{{.State.Running}}' '$cid' | grep -q true"
	if [ "$external" = yes ]; then
		check_external_requests() {
			python3 - "$hits" <<'PYEOF' || die "the external boot made an unexpected remote request (only runtime heartbeat is allowed)"
import sys
requests = open(sys.argv[1]).read().splitlines()
for request in requests:
    if request != "POST /api/v1/machines/heartbeat":
        print("unexpected remote request: " + request, file=sys.stderr)
        sys.exit(1)
print("smoke: the stub server saw %s requests, only runtime heartbeats allowed" % len(requests))
PYEOF
		}
		# 只允许已知运行期心跳；真实创建路由与未知请求均拒绝。
		poll_until "the first-boot record to say done" 120 \
			sh -c "docker exec '$cid' cat /root/.local/share/polyforge/workstation/first-boot.json 2>/dev/null | grep -q '\"phase\": *\"done\"'"
		check_external_requests
	fi
	poll_until "pf supervise to run inside" 120 \
		docker exec "$cid" sh -c 'ps -e -o args= | grep -q "[p]f supervise"'

	record_of() {
		docker exec "$cid" cat /root/.local/share/polyforge/workstation/first-boot.json
	}
	cell_server() {
		docker exec "$cid" pf config get server 2>/dev/null || true
	}
	check_record() {
		what=$1
		fbj=$(record_of)
		printf 'smoke: first-boot.json (%s) = %s\n' "$what" "$fbj"
		# POSIX greps over the entrypoint's own compact JSON — no host
		# python3 (polyforge#177 Q6: the host needs only Docker and the
		# system sh; the record's shape is the writer's own, pinned by
		# the record check itself)
		record_field() {
			printf '%s' "$fbj" | grep -q "\"$1\": *$2"
		}
		record_field phase '"done"' || die "the first-boot record's phase is not done (see the record above)"
		record_field mode "\"$mode\"" || die "the first-boot record's mode is not $mode (see the record above)"
		record_field keyReady 'true' || die "keyReady must be true after the boot"
		if [ "$mode" = local ]; then
			record_field userReady 'true' || die "userReady must be true in local mode"
		else
			record_field userReady 'false' || die "userReady must be false in external mode"
		fi
		record_field defaultCell '"default"' || die "defaultCell must be default"
	}

	if [ "$external" = yes ]; then
		mode=external
	else
		mode=local
		# the local AIHub answers before the key's proof runs
		poll_until "the local AIHub to answer inside" 300 \
			docker exec "$cid" sh -c 'curl -s -o /dev/null http://127.0.0.1:8420/api/v1/memories'
	fi
	check_record "$mode boot"

	cell=$(cell_server)
	printf 'smoke: default Cell server = %s\n' "${cell:-<unset>}"
	case "$cell" in
	*127.0.0.1:8420*)
		[ "$external" = no ] || die "the external boot's default Cell must point at the given server, not the local one"
		;;
	*host.docker.internal:$stub_port*)
		[ "$external" = yes ] || die "the local boot's default Cell must point at the local AIHub"
		;;
	*) die "the default Cell does not point where the boot decided: $cell" ;;
	esac

	if [ "$external" = no ]; then
		# Q3: the key's REAL authentication is the proof — keyReady only
		# says issued-and-written. The recall also exercises the
		# /workspace mapping (the project is validated against it).
		docker exec -w /workspace "$cid" pf recall --project workstation first-boot-probe >/dev/null 2>&1 ||
			die "the default Cell's key does not authenticate against the local AIHub (pf recall inside failed) — the workstation project or the /workspace mapping is not usable"
		say "the first-boot key authenticates (pf recall inside, via /workspace)"
		# the key is the daily user's, never the bootstrap admin's: the
		# identity predates the server, so the server's own fresh-install
		# bootstrap (usr_root + bootstrap-admin.key) has nothing to seed
		if docker exec "$cid" test -e /var/lib/polyforge/aihub/bootstrap-admin.key; then
			die "bootstrap-admin.key exists — the identity must be established before the server starts, leaving the server's bootstrap nothing to seed"
		fi
		say "no bootstrap-admin key (the identity predates the server)"
		# AC2's layout half, asserted inside the container: the agent
		# dir sits in pf-home, the pi packages entry points at
		# /components/current, and the record carries no credential
		docker exec "$cid" sh -c 'test "$PI_CODING_AGENT_DIR" = /root/.pi/agent' ||
			die "the step agent dir is not /root/.pi/agent inside pf-home (AC2)"
		say "the step agent dir is /root/.pi/agent (pf-home)"
		docker exec "$cid" python3 -c 'import json
pk = (json.load(open("/root/.pi/agent/settings.json")).get("packages") or [])
ok = any(p == "/components/current" or (isinstance(p, dict) and p.get("source") == "/components/current") for p in pk)
assert ok, "PACKAGES_PROBE: no entry points at /components/current"
' || die "no pi packages entry points at /components/current (AC2 — the harness references must follow the components volume)"
		say "the pi packages entry points at /components/current"
		docker exec "$cid" python3 -c 'import os
from pathlib import Path
if Path("/root/.claude").is_dir():
    skill = Path("/root/.claude/skills/polyforge")
    assert skill.is_symlink(), "SKILLS_PROBE: routing skill is not a symlink"
    assert os.readlink(skill) == "/components/current/skills/polyforge", "SKILLS_PROBE: routing skill does not follow current"
' || die "the Claude routing skill does not link to /components/current/skills/polyforge (AC2)"
		say "the Claude skill link follows /components/current when Claude HOME exists"
		docker exec "$cid" python3 -c 'import re
key = re.search(r"^api_key = \"(.*)\"", open("/root/.config/polyforge/cli.toml").read(), re.M)
assert key, "CREDENTIAL_PROBE: cli.toml carries no api_key"
rec = open("/root/.local/share/polyforge/workstation/first-boot.json").read()
assert key.group(1) not in rec, "CREDENTIAL_PROBE: the record carries the key"
for name in ("api_key", "key_hash", "token"):
    assert name not in rec.lower(), "CREDENTIAL_PROBE: the record names a credential field"
' || die "the first-boot record carries a credential (AC2) — the key belongs in cli.toml only"
		say "the record carries no credential"
	fi

	# the restart, volumes intact: the first choice stands — the record
	# is byte-identical (a completed boot skips itself) and the same
	# key still authenticates (no reissue). The external restart passes
	# a DIFFERENT server env: the choice was made once.
	say "first-boot: restarting with the volumes intact"
	first_record=$(record_of)
	first_cell=$(cell_server)
	compose_ext down >/dev/null
	if [ "$external" = yes ]; then
		export WORKSTATION_AIHUB_SERVER="http://host.docker.internal:1/other-choice"
	fi
	compose_ext up -d >/dev/null
	cid=$(compose_ext ps -q workstation)
	[ -n "$cid" ] || die "no workstation container was created for the restart"
	# 240 s: the restart poll measured tight once under the harness's own
	# 8-way engine parallelism (the machine was up — its heartbeat had
	# answered — only the ps probe was slow); the settled state is unchanged
	poll_until "pf supervise to run inside (restart)" 240 \
		docker exec "$cid" sh -c 'ps -e -o args= | grep -q "[p]f supervise"'
	[ "$(record_of)" = "$first_record" ] ||
		die "the record changed after the restart — the completed first boot must skip itself (the choice is made exactly once)"
	say "the record is byte-identical after the restart"
	[ "$(cell_server)" = "$first_cell" ] ||
		die "the default Cell changed after the restart: $(cell_server) (was $first_cell)"
	if [ "$external" = no ]; then
		docker exec -w /workspace "$cid" pf recall --project workstation first-boot-probe >/dev/null 2>&1 ||
			die "the same key no longer authenticates after the restart — the first boot must not have reissued it"
		say "the same key still authenticates after the restart (no reissue)"
	fi

	compose_ext down -v >/dev/null 2>&1 || true
	if [ "$external" = yes ]; then
		# 停机后再核对全部请求，覆盖首启与重启的完整运行期。
		check_external_requests
	fi
	say "first-boot: PASS"
}

# ensure_image PROFILE — the image this run boots: WORKSTATION_IMAGE names a
# published image that must already be on the engine (the exported
# delivery's path, polyforge#177 Q4 — no build, ever); without it compose
# builds and tags the local default polyforge-workstation:local.
ensure_image() {
	if [ -n "${WORKSTATION_IMAGE:-}" ]; then
		docker image inspect "$WORKSTATION_IMAGE" >/dev/null 2>&1 ||
			die "WORKSTATION_IMAGE $WORKSTATION_IMAGE is not on the engine (docker image inspect failed) — an environment prerequisite, not a product failure"
		say "image: $WORKSTATION_IMAGE (given, no build)"
	else
		compose --profile "$1" build >/dev/null
		say "image: built and tagged polyforge-workstation:local"
	fi
}

# cmd_persist — the AC2 acceptance (polyforge#177): two compose down/up
# cycles and a NEW TAG of the image keep the six persisted facts BY VALUE
# — the harness login marker, cli.toml, the workspace file, the AIHub
# database's user row, the pulled Podman image and the components
# volume's current — then the delivery is exported to an EMPTY directory
# and first-boot and persist run from there on the given image (Q6: the
# host needs only Docker and the system sh; every JSON/HTTP check runs
# inside the container). WORKSTATION_SMOKE_EXPORTED=1 marks the exported
# copy: its own export phase is skipped, so the recursion ends.
cmd_persist() {
	require_engine
	profile=aihub
	sp_value="sp-$(date +%s)-$$"
	ensure_image aihub
	primary="${WORKSTATION_IMAGE:-polyforge-workstation:local}"
	retag="polyforge-workstation-smoke:retag-$smoke_run"
	docker tag "$primary" "$retag"
	say "persist: project $smoke_project, image $primary, new tag $retag"

	# the trap is the whole cleanup: a die anywhere below downs only THIS
	# run's project — its volumes are disposable, an operator's never are —
	# and removes this run's retag (review r1 N2: tags accumulated otherwise)
	trap 'compose --profile aihub down -v >/dev/null 2>&1 || true; docker rmi "$retag" >/dev/null 2>&1 || true' EXIT
	compose --profile aihub down -v >/dev/null 2>&1 || true

	# up_with_image IMAGE — down, up on the image, wait for supervise
	up_with_image() {
		export WORKSTATION_IMAGE="$1"
		compose --profile aihub down >/dev/null 2>&1 || true
		compose --profile aihub up -d >/dev/null
		cid=$(compose --profile aihub ps -q workstation)
		[ -n "$cid" ] || die "no workstation container was created for image $1"
		poll_until "the workstation container to run" 120 \
			sh -c "docker inspect -f '{{.State.Running}}' '$cid' | grep -q true"
		poll_until "pf supervise to run inside" 120 \
			docker exec "$cid" sh -c 'ps -e -o args= | grep -q "[p]f supervise"'
	}

	# the three written sentinels ride the volumes; the podman image is
	# pulled once (the podman-storage volume's own AC2 half)
	persist_write_sentinels() {
		docker exec "$cid" sh -c 'printf "%s\n" "$1" > /root/.pi/agent/workstation-smoke-persist-marker' persist "$sp_value"
		docker exec "$cid" sh -c 'printf "# workstation-smoke-persist %s\n" "$1" >> /root/.config/polyforge/cli.toml' persist "$sp_value"
		docker exec "$cid" sh -c 'printf "%s\n" "$1" > /workspace/workstation-smoke-persist-marker' persist "$sp_value"
		docker exec "$cid" podman pull -q busybox:1.37 >/dev/null ||
			die "the Podman pull failed inside the workstation (the podman-storage volume is the AC2 half)"
	}

	# all six values, read inside the container (Q6: no host curl/python3)
	persist_read_values() {
		harness_marker=$(docker exec "$cid" cat /root/.pi/agent/workstation-smoke-persist-marker) ||
			die "the harness login marker is gone"
		cli_line=$(docker exec "$cid" grep '^# workstation-smoke-persist ' /root/.config/polyforge/cli.toml) ||
			die "the cli.toml sentinel line is gone"
		workspace_file=$(docker exec "$cid" cat /workspace/workstation-smoke-persist-marker) ||
			die "the workspace file is gone"
		aihub_user=$(docker exec "$cid" python3 -c 'import sqlite3
c = sqlite3.connect("/var/lib/polyforge/aihub/aihub.db")
row = c.execute("SELECT id FROM users ORDER BY id LIMIT 1").fetchone()
print(row[0] if row else "none")') ||
			die "the AIHub database does not answer"
		podman_image=$(docker exec "$cid" podman inspect --format '{{.Id}}' busybox:1.37) ||
			die "the pulled Podman image is gone"
		components_current=$(docker exec "$cid" readlink /components/current) ||
			die "the components current link is gone"
		components_digest=$(docker exec "$cid" python3 -c 'import hashlib
print(hashlib.sha256(open("/components/current/manifest.json", "rb").read()).hexdigest())') ||
			die "the components current manifest is unreadable"
		# the identity a REINSTALL always changes (review r1#1): the link
		# target and the manifest digest are seed-deterministic — a lost
		# components volume reinstalls to byte-identical values — but the
		# symlink swap on install recreates the object, so its mtime and
		# inode only survive when the volume does (the noop path of
		# components.Install writes nothing, so restarts keep them)
		components_stat=$(docker exec "$cid" stat -c '%Y %i' /components/current) ||
			die "the components current link is unreadable"
	}

	# persist_compare WHAT — every value must equal its first read (a
	# down with -v, a fresh volume or a wrong bind would change them)
	persist_compare() {
		what=$1
		[ "$harness_marker" = "$harness_marker_first" ] ||
			die "the harness login marker changed after $what: $harness_marker (was $harness_marker_first)"
		[ "$cli_line" = "$cli_line_first" ] ||
			die "the cli.toml sentinel changed after $what: $cli_line (was $cli_line_first)"
		[ "$workspace_file" = "$workspace_file_first" ] ||
			die "the workspace file changed after $what: $workspace_file (was $workspace_file_first)"
		[ "$aihub_user" = "$aihub_user_first" ] ||
			die "the AIHub user row changed after $what: $aihub_user (was $aihub_user_first)"
		[ "$podman_image" = "$podman_image_first" ] ||
			die "the Podman image changed after $what: $podman_image (was $podman_image_first)"
		[ "$components_current" = "$components_current_first" ] ||
			die "the components current changed after $what: $components_current (was $components_current_first)"
		[ "$components_digest" = "$components_digest_first" ] ||
			die "the components current manifest digest changed after $what: $components_digest (was $components_digest_first)"
		[ "$components_stat" = "$components_stat_first" ] ||
			die "the components current mtime/inode identity changed after $what: $components_stat (was $components_stat_first)"
		say "persist: $what kept the harness login marker, cli.toml, the workspace file, the AIHub user row, the Podman image and the components current (link, digest, mtime/inode identity)"
	}

	up_with_image "$primary"
	persist_write_sentinels
	persist_read_values
	harness_marker_first=$harness_marker
	cli_line_first=$cli_line
	workspace_file_first=$workspace_file
	aihub_user_first=$aihub_user
	podman_image_first=$podman_image
	components_current_first=$components_current
	components_digest_first=$components_digest
	components_stat_first=$components_stat
	say "persist: the six values recorded (sentinel $sp_value)"

	up_with_image "$primary"
	persist_read_values
	persist_compare "a compose down && up"
	up_with_image "$primary"
	persist_read_values
	persist_compare "the second down && up"

	say "persist: switching to the new tag $retag"
	up_with_image "$retag"
	persist_read_values
	persist_compare "the new tag image $retag"

	compose --profile aihub down -v >/dev/null 2>&1 || true

	if [ "${WORKSTATION_SMOKE_EXPORTED:-}" != 1 ]; then
		export_dir=$(mktemp -d)
		for f in smoke.sh compose.yaml seccomp.json seccomp.json.sha256 Caddyfile; do
			cp "$script_dir/$f" "$export_dir/$f"
		done
		say "persist: exported to $export_dir — running first-boot and persist from there"
		(cd "$export_dir" && WORKSTATION_SMOKE_EXPORTED=1 WORKSTATION_IMAGE="$retag" sh ./smoke.sh first-boot) ||
			die "first-boot failed from the exported directory"
		(cd "$export_dir" && WORKSTATION_SMOKE_EXPORTED=1 WORKSTATION_IMAGE="$retag" sh ./smoke.sh persist) ||
			die "persist failed from the exported directory"
		rm -rf "$export_dir"
		docker rmi "$retag" >/dev/null 2>&1 || true
		say "the exported copy ran first-boot and persist (image as-is, no build)"
	fi
	say "persist: PASS"
}

case "${1:-}" in
boot) shift; cmd_boot "$@" ;;
env) shift; cmd_env "$@" ;;
first-boot) shift; cmd_first_boot "$@" ;;
persist) shift; cmd_persist "$@" ;;
podman-verify) shift; cmd_podman_verify "$@" ;;
*)
	cat >&2 <<'USAGE'
usage: sh dist/workstation/smoke.sh boot --no-aihub|--with-aihub
       sh dist/workstation/smoke.sh first-boot [--external]
       sh dist/workstation/smoke.sh persist [WORKSTATION_IMAGE=<published tag>]
       sh dist/workstation/smoke.sh env
       sh dist/workstation/smoke.sh podman-verify
USAGE
	exit 2
	;;
esac
