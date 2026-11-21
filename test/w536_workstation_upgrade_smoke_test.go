// carries: dist/workstation/smoke.sh, dist/workstation/upgrade-smoke.sh,
//
//	dist/workstation/README.md, dist/workstation/build-seed.sh,
//	dist/workstation/compose.yaml
package test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestWorkstationUpgradeSmoke drives dist/workstation/smoke.sh upgrade
// with the stub engine (the w535 pattern): this run's own compose
// project and port (the #177 isolation — never a fixed container name),
// the first boot's seeding, the in-container `pf components upgrade`,
// the pointer, version, serving-process and identity assertions, and
// the with-volumes cleanup on every exit path. The real-engine half is
// the verification gate's (the same script on a machine with an
// engine).
func TestWorkstationUpgradeSmoke(t *testing.T) {
	scenario := t.TempDir()
	stub := `#!/bin/sh
set -eu
printf '%s\n' "$*" >> "$W536_SMOKE_SCENARIO/calls.log"
if [ "${1:-}" = version ]; then echo 29.9.2-stub; exit 0; fi
case "$1:$2" in
build:*)
	case "$*" in *--build-arg*) : ;; *) echo "stub docker: build needs --build-arg" >&2; exit 1 ;; esac
	exit 0
	;;
create:*) echo seed-container-id; exit 0 ;;
cp:*) mkdir -p "$3/releases/v0.2.4-dev.2"; exit 0 ;;
rm:*) exit 0 ;;
inspect:*)
	case "$*" in
	*{{.Id}}*) echo container-id-stable ;;
	*{{.State.Running}}*) echo true ;;
	esac
	exit 0
	;;
esac
if [ "$1" = compose ]; then
	verb=""
	for a in "$@"; do
		case "$a" in build|up|ps|down) verb=$a ;; esac
	done
	case "$verb" in
	up)
		env > "$W536_SMOKE_SCENARIO/compose-up-env.txt"
		[ -n "${PI_WEB_PASSWORD:-}" ] || { echo "stub docker: required variable PI_WEB_PASSWORD is missing" >&2; exit 1; }
		;;
	ps) echo container-id-stable ;;
	down) : ;;
	esac
	exit 0
fi
if [ "$1" = exec ]; then
	args="$*"
	case "$args" in
	*"pf components upgrade --version"*)
		touch "$W536_SMOKE_SCENARIO/upgraded"
		exit 0
		;;
	*"/proc/1/stat"*)
		echo "1 (catatonit) 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20 21 12345 22"
		;;
	*"readlink /components/current"*)
		if [ -f "$W536_SMOKE_SCENARIO/upgraded" ]; then echo releases/v0.2.4-dev.2; else echo releases/v0.2.4-dev.1; fi
		;;
	*"readlink /components/previous"*)
		if [ -f "$W536_SMOKE_SCENARIO/upgraded" ]; then echo releases/v0.2.4-dev.1; else echo ""; fi
		;;
	*"pf version"*)
		if [ -f "$W536_SMOKE_SCENARIO/upgraded" ]; then echo v0.2.4-dev.2; else echo v0.2.4-dev.1; fi
		;;
	*"/components/upgrade-status.json"*)
		echo '{"state":"done","to":"v0.2.4-dev.2"}'
		;;
	esac
	exit 0
fi
exit 0
`
	if err := os.WriteFile(filepath.Join(scenario, "docker"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	// the stub curl answers 200: the smoke's bounded login poll settles
	// at once, the way a live Pi Web does.
	if err := os.WriteFile(filepath.Join(scenario, "curl"), []byte("#!/bin/sh\necho 200\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "dist/workstation/smoke.sh", "upgrade")
	cmd.Dir = w535Root(t)
	cmd.Env = append(os.Environ(),
		"PATH="+scenario+":"+os.Getenv("PATH"),
		"W536_SMOKE_SCENARIO="+scenario,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the upgrade smoke must complete: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "upgrade: PASS") {
		t.Fatalf("the smoke must print its PASS line: %s", out)
	}
	calls, _ := os.ReadFile(filepath.Join(scenario, "calls.log"))
	text := string(calls)
	for _, want := range []string{
		// this run's own project and port (the #177 isolation), the
		// default boot service, never a fixed container name
		"-p workstation-upgrade-",
		"--profile aihub",
		"ps -q workstation",
		// the second, newer seed image
		"--build-arg SEED_VERSION=v0.2.4-dev.2",
		// the in-container upgrade through the CLI verb
		"pf components upgrade --version v0.2.4-dev.2 --seed-dir /components/smoke-target",
		// the pointer and version assertions
		"readlink /components/current",
		"readlink /components/previous",
		"pf version",
		// the identity assertions: the same container, the same PID 1
		"{{.Id}}",
		"/proc/1/stat",
		// the serving-process version proof: the daemon's /status, the
		// volume-backed aihub and the pi-web process serving out of the
		// new release — no reachability stand-in
		"daemon.sock",
		"/components/current/pi-web/package.json",
		"/proc/",
		// the persisted final record
		"/components/upgrade-status.json",
		// the cleanup on the happy path
		"down -v",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the upgrade smoke is missing %q:\n%s", want, text)
		}
	}
	for _, banned := range []string{
		"container_name",
		"workstation-aihub", // the pre-#176 service name
	} {
		if strings.Contains(text, banned) {
			t.Errorf("the upgrade smoke must not use %q", banned)
		}
	}
	// the compose up carried the password (the compose file's required
	// variable) and this run's own port
	upEnv, _ := os.ReadFile(filepath.Join(scenario, "compose-up-env.txt"))
	if !strings.Contains(string(upEnv), "PI_WEB_PASSWORD=") {
		t.Error("the compose up must carry PI_WEB_PASSWORD")
	}
	if !strings.Contains(string(upEnv), "WORKSTATION_WEB_PORT=") {
		t.Error("the compose up must carry this run's own WORKSTATION_WEB_PORT")
	}
}
