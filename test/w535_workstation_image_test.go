// carries: dist/workstation/Containerfile, dist/workstation/entrypoint.sh,
//
//	dist/workstation/containers/*, dist/workstation/seccomp.json,
//	dist/workstation/seccomp.json.sha256, dist/workstation/README.md,
//	dist/workstation/compose.yaml, dist/workstation/smoke.sh,
//	dist/workstation/env-whitelist.sh, dist/workstation/Caddyfile,
//	test/testdata/workstation-smoke-stub/docker, .dockerignore,
//	.polyforge/verify-scope.conf
package test

// w535_workstation_image_test.go — polyforge#161 (84-image): the
// workstation base image's pinned facts. TestWorkstationImage is the
// STATIC half of the split the owner ruled (2026-11-19, Q3 C): it runs
// with no container engine and pins the minimal parameter set against
// drift — the seccomp profile is the pinned file (its SHA recorded
// beside it), the entrypoint initializes the private cgroup namespace
// as root before any controller is enabled, the container boundary is
// an explicit environment whitelist (PORT and the PI_* families pass,
// PI_TEST_* never does), and the image carries podman, crun and
// catatonit — never podman-docker or a docker socket. The real-engine
// half (TestWorkstationImageEngine, added with the smoke script) drives
// the smoke script's boot on a machine with an engine and skips without
// one; the full workstation bar (AIHub boot, env, podman-verify) is the
// verification gate's, driven through dist/workstation/smoke.sh.
//
// Authored test-first against the ticket's settled decisions, BEFORE the
// dist/workstation tree exists: every scenario's first assertion is that
// the deliverable file is there (the w254 deliverable-absent form), and
// the assertions that follow are derived from the ticket and this
// step's measured facts (moby v28.0.0 default template + pivot_root;
// crun's --no-new-keyring is a create/run subcommand option; the memory
// controller needs an empty namespace root cgroup), never from the
// implementation's own output.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// w535Root answers the repo root (this package's cwd is the repo's
// test/ dir).
func w535Root(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("repo root: %v", err)
	}
	return root
}

// w535Deliverable answers one dist/workstation deliverable's body,
// failing on the deliverable itself when absent or empty — the
// workstation base is this work item's deliverable, the same
// deliverable-absent form w254's page scenarios use.
func w535Deliverable(t *testing.T, rel string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(w535Root(t), rel))
	if err != nil {
		t.Fatalf("polyforge#161: read %s: %v (the workstation base is the deliverable — the repo must carry it)", rel, err)
	}
	if len(raw) == 0 {
		t.Fatalf("polyforge#161: %s is empty: a build recipe is a recipe, not a placeholder", rel)
	}
	return string(raw)
}

// TestWorkstationImage — the workstation base image's pinned facts
// (AC1's static face, and the mechanisms AC2 leans on).
func TestWorkstationImage(t *testing.T) {
	t.Run("seccomp profile is the pinned file and its recorded SHA matches the bytes", func(t *testing.T) {
		body := w535Deliverable(t, "dist/workstation/seccomp.json")
		var profile struct {
			DefaultAction string `json:"defaultAction"`
			Syscalls      []struct {
				Names  []string `json:"names"`
				Action string   `json:"action"`
			} `json:"syscalls"`
		}
		if err := json.Unmarshal([]byte(body), &profile); err != nil {
			t.Fatalf("polyforge#161: seccomp.json must be the pinned profile as strict JSON: %v", err)
		}
		if profile.DefaultAction != "SCMP_ACT_ERRNO" {
			t.Fatalf("polyforge#161: seccomp defaultAction = %q, want SCMP_ACT_ERRNO (the moby default template's shape)", profile.DefaultAction)
		}
		// the one delta from the upstream template, the ticket's settled
		// Q4: pivot_root allowed — without it a nested crun container
		// dies on pivot_root: Operation not permitted (measured on the
		// docker default profile before the pin).
		var pivot bool
		for _, group := range profile.Syscalls {
			if group.Action != "SCMP_ACT_ALLOW" {
				continue
			}
			for _, name := range group.Names {
				if name == "pivot_root" {
					pivot = true
				}
			}
		}
		if !pivot {
			t.Fatal("polyforge#161: the pinned seccomp profile must allow pivot_root — the ticket's settled Q4 (default template + pivot_root)")
		}
		// the recorded SHA is the pin review and the smoke compare with:
		// recompute it from the bytes, never trust the sidecar alone.
		recorded := w535Deliverable(t, "dist/workstation/seccomp.json.sha256")
		fields := strings.Fields(recorded)
		if len(fields) < 2 || fields[1] != "seccomp.json" {
			t.Fatalf("polyforge#161: seccomp.json.sha256 must be a sha256sum record of seccomp.json, got %q", recorded)
		}
		sum := sha256.Sum256([]byte(body))
		if fields[0] != hex.EncodeToString(sum[:]) {
			t.Fatalf("polyforge#161: seccomp.json.sha256 records %s but the bytes hash to %s — the pin drifted", fields[0], hex.EncodeToString(sum[:]))
		}
		// the upstream template's origin is recorded beside the file
		// (settled Q4: commit the artifact and name the upstream
		// version; never download at build time).
		origin := w535Deliverable(t, "dist/workstation/README.md")
		if !strings.Contains(origin, "profiles/seccomp/default.json") || !strings.Contains(origin, "v28.0.0") {
			t.Fatal("polyforge#161: dist/workstation/README.md must record the seccomp profile's upstream origin (moby's profiles/seccomp/default.json at v28.0.0)")
		}
	})

	t.Run("the Containerfile builds the base and nothing docker-shaped creeps in", func(t *testing.T) {
		body := w535Deliverable(t, "dist/workstation/Containerfile")
		// judge the recipe's commands, never its prose: comments are
		// dropped before any content assertion (r1#2 — the first cut
		// matched the recipe's own explanation of what it refuses).
		var cmds []string
		for _, line := range strings.Split(body, "\n") {
			if trimmed := strings.TrimSpace(line); !strings.HasPrefix(trimmed, "#") {
				cmds = append(cmds, line)
			}
		}
		norm := regexp.MustCompile(`\s+`).ReplaceAllString(strings.Join(cmds, "\n"), " ")
		// the toolchain pins agree with the repo's own pins:
		// go.mod's toolchain (toolchain_pin_test's bar) and
		// Dockerfile.verify's node stage.
		for _, pin := range []string{
			"FROM golang:1.26.11-bookworm",
			"FROM node:24.22.2-bookworm",
			"FROM ubuntu:24.04",
		} {
			if !strings.Contains(norm, pin) {
				t.Fatalf("polyforge#161: the Containerfile must pin %s (the repo's pinned toolchain versions)", pin)
			}
		}
		// the base installs exactly the ticket's runtime set: podman,
		// crun, catatonit, plus the tools the daemon and the acceptance
		// proof need (git for the daemon's clones, curl for the smoke's
		// probes, ca-certificates for pulls, python3 for the in-image
		// unittest proof of AC2).
		for _, pkg := range []string{"podman", "crun", "catatonit", "git", "curl", "ca-certificates", "python3"} {
			if !regexp.MustCompile(`(^|[^a-z0-9+-])` + regexp.QuoteMeta(pkg) + `([^a-z0-9+-]|$)`).MatchString(norm) {
				t.Fatalf("polyforge#161: the Containerfile must install %s — it is part of the measured minimal base", pkg)
			}
		}
		// never podman-docker and never a docker socket or client name:
		// the gate probe (test_gate_docker) asserts a verification
		// container sees no docker client and no /var/run/docker.sock.
		if strings.Contains(norm, "podman-docker") {
			t.Fatal("polyforge#161: the workstation image must not install podman-docker — the gate probe asserts no docker client inside")
		}
		if strings.Contains(norm, "docker.sock") {
			t.Fatal("polyforge#161: the workstation image must not mount or create a docker socket")
		}
		if !strings.Contains(norm, "CGO_ENABLED=0") || !strings.Contains(norm, "./cmd/pf") {
			t.Fatal("polyforge#161: the Containerfile must build ./cmd/pf as a static binary (CGO_ENABLED=0; modernc sqlite is pure Go)")
		}
		if !strings.Contains(norm, "npm run build") || !strings.Contains(norm, "webui") {
			t.Fatal("polyforge#161: the Containerfile must build webui's embedded dist tree before the go build (go:embed all:dist)")
		}
		// PID 1 is catatonit (settled; it reaps the conmon children, so
		// no conmon zombies survive — AC2).
		if !strings.Contains(norm, `ENTRYPOINT ["catatonit", "--"`) {
			t.Fatal("polyforge#161: the entrypoint must be catatonit -- <script> so PID 1 is the reaper, per the settled decision")
		}
		for _, cp := range []string{
			"workstation-entrypoint.sh",
			"/etc/containers/storage.conf",
			"/etc/containers/containers.conf",
			"/etc/containers/registries.conf",
		} {
			if !strings.Contains(norm, cp) {
				t.Fatalf("polyforge#161: the Containerfile must copy %s into the image", cp)
			}
		}
	})

	t.Run("the container configs pin the measured runtime semantics", func(t *testing.T) {
		storage := w535Deliverable(t, "dist/workstation/containers/storage.conf")
		// native overlay on the named storage volume: overlay-on-overlay
		// is refused by the driver, so the graphroot must be the volume
		// compose mounts (AC2's build and run paths both measured).
		for _, pin := range []string{`driver = "overlay"`, `graphroot = "/var/lib/containers/storage"`} {
			if !strings.Contains(regexp.MustCompile(`\s+`).ReplaceAllString(storage, " "), pin) {
				t.Fatalf("polyforge#161: storage.conf must pin %s — native overlay on the separate volume", pin)
			}
		}
		containers := w535Deliverable(t, "dist/workstation/containers/containers.conf")
		for _, pin := range []string{`cgroup_manager = "cgroupfs"`, `keyring = false`} {
			if !strings.Contains(regexp.MustCompile(`\s+`).ReplaceAllString(containers, " "), pin) {
				t.Fatalf("polyforge#161: containers.conf must pin %s — cgroupfs for the private namespace, no keyring because the outer pinned seccomp blocks add_key (measured)", pin)
			}
		}
		registries := w535Deliverable(t, "dist/workstation/containers/registries.conf")
		if !strings.Contains(registries, `unqualified-search-registries`) || !strings.Contains(registries, "docker.io") {
			t.Fatal("polyforge#161: registries.conf must resolve short names (busybox:1.37 and Dockerfile.verify's FROMs) to docker.io — without it every engine probe skips")
		}
	})

	t.Run("the entrypoint initializes the private cgroup and the environment boundary", func(t *testing.T) {
		body := w535Deliverable(t, "dist/workstation/entrypoint.sh")
		// the cgroup facts this step measured on cgroup v2:
		// the namespace root must have NO internal processes before the
		// memory controller can be enabled — so the entrypoint moves
		// PID 1 (catatonit) AND itself into the init sub-cgroup first;
		// +memory/+pids are the strict pair (without them podman's
		// limits are refused outright), cpu/io are best effort.
		move1 := strings.Index(body, `echo 1 > "$CG/init/cgroup.procs"`)
		moveSelf := strings.Index(body, `echo $$ > "$CG/init/cgroup.procs"`)
		enableMemory := strings.Index(body, `echo +memory > "$CG/cgroup.subtree_control"`)
		enablePids := strings.Index(body, `echo +pids > "$CG/cgroup.subtree_control"`)
		for name, at := range map[string]int{"moving PID 1 into init/": move1, "moving the script into init/": moveSelf, "enabling +memory": enableMemory, "enabling +pids": enablePids} {
			if at < 0 {
				t.Fatalf("polyforge#161: entrypoint.sh must pin the cgroup init sequence; missing %s", name)
			}
		}
		if !(move1 < enableMemory && moveSelf < enableMemory) {
			t.Fatal("polyforge#161: entrypoint.sh enables controllers before every process left the namespace root — +memory fails with EBUSY unless PID 1 and the script both moved into init/ first (measured)")
		}
		if !strings.Contains(body, `mount -o remount,rw "$CG"`) {
			t.Fatal("polyforge#161: entrypoint.sh must remount /sys/fs/cgroup read-write before initializing it (docker mounts it read-only)")
		}
		// the environment boundary is sourced, not inlined: the filter
		// lives in env-whitelist.sh so the test below executes the real
		// one instead of matching prose (review r1#5 — the first cut
		// pinned "PI_TEST_" as text, which the header comment carries
		// all by itself).
		if !strings.Contains(body, ". /usr/local/bin/workstation-env-whitelist.sh") {
			t.Fatal("polyforge#161: entrypoint.sh must source /usr/local/bin/workstation-env-whitelist.sh — the boundary filter is executable, not prose")
		}
		if !strings.Contains(body, "workstation_env_whitelist") {
			t.Fatal("polyforge#161: entrypoint.sh must call the sourced whitelist before handing over")
		}
		if !strings.Contains(body, `exec /usr/local/libexec/polyforge/pf supervise "$@"`) {
			t.Fatal("polyforge#175: entrypoint.sh must end by exec'ing the base pf's absolute path with the caller's flags (compose passes only the flags)")
		}
		// the absolute path is the point (Q5): a PATH lookup would find
		// whatever a later layer or mount left in /usr/local/bin
		if regexp.MustCompile(`(?m)^exec pf supervise(?:[ \t]|$)`).MatchString(body) {
			t.Fatal("polyforge#175: entrypoint.sh must not exec pf by a PATH lookup — the base pf lives at /usr/local/libexec/polyforge/pf")
		}
		// the first boot runs BETWEEN the boundary and supervise
		// (84-components-C): the identity must exist before the server
		// starts — the aihub server's own fresh-install bootstrap
		// (usr_root) never fires on a first-boot-initialized store
		// 只定位真实命令行，注释不能替代绝对 bootstrap 调用。
		commandAt := func(command string) int {
			at := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(command) + `[ \t]*$`).FindStringIndex(body)
			if at == nil {
				return -1
			}
			return at[0]
		}
		whitelist := commandAt("workstation_env_whitelist")
		firstBoot := commandAt("/usr/local/libexec/polyforge/pf first-boot")
		supervise := commandAt(`exec /usr/local/libexec/polyforge/pf supervise "$@"`)
		if firstBoot < 0 {
			t.Fatal("polyforge#176: entrypoint.sh must run `pf first-boot` before handing over to pf supervise")
		}
		if !(whitelist < firstBoot && firstBoot < supervise) {
			t.Fatal("polyforge#176: entrypoint.sh's order is the environment boundary, then the first boot, then exec pf supervise — the first boot initializes the identity before the server starts")
		}
		// review r1#2: the first boot's inputs are consumed by the
		// boot itself — the external-mode key must never ride the
		// environment into the supervise children (Pi Web's agent
		// sessions inherit it otherwise). The unset line runs AFTER
		// the boot and BEFORE the exec, and it is executed for real
		// here: a probe environment carrying the three names comes out
		// without them.
		unsetRe := regexp.MustCompile(`(?m)^unset WORKSTATION_AIHUB_[A-Z_]+( WORKSTATION_AIHUB_[A-Z_]+)*$`)
		unsetLine := unsetRe.FindString(body)
		if unsetLine == "" {
			t.Fatal("polyforge#176 (review r1#2): entrypoint.sh must unset WORKSTATION_AIHUB_SERVER/KEY/PROJECT between the first boot and the exec — the key is a credential and never reaches the supervise children")
		}
		unsetAt := strings.Index(body, unsetLine)
		if !(firstBoot < unsetAt && unsetAt < supervise) {
			t.Fatal("polyforge#176 (review r1#2): the unset must sit between `pf first-boot` and `exec pf supervise` — before the boot it would eat the inputs, after the exec it never runs")
		}
		for _, name := range []string{"WORKSTATION_AIHUB_SERVER", "WORKSTATION_AIHUB_KEY", "WORKSTATION_AIHUB_PROJECT"} {
			if !strings.Contains(unsetLine, name) {
				t.Fatalf("polyforge#176 (review r1#2): the unset line must name %s: %q", name, unsetLine)
			}
		}
		probe := exec.Command("sh", "-c",
			`export WORKSTATION_AIHUB_SERVER=s WORKSTATION_AIHUB_KEY=k WORKSTATION_AIHUB_PROJECT=p PI_KEEP=1; `+unsetLine+`; env`)
		probe.Env = []string{"PATH=" + os.Getenv("PATH")}
		out, err := probe.CombinedOutput()
		if err != nil {
			t.Fatalf("the unset line must run: %v:\n%s", err, out)
		}
		for _, name := range []string{"WORKSTATION_AIHUB_SERVER", "WORKSTATION_AIHUB_KEY", "WORKSTATION_AIHUB_PROJECT"} {
			if strings.Contains(string(out), name+"=") {
				t.Fatalf("the executed unset must remove %s from the environment:\n%s", name, out)
			}
		}
	})

	t.Run("the environment whitelist executes: PI_* passes, PI_TEST_* never", func(t *testing.T) {
		script := w535Deliverable(t, "dist/workstation/env-whitelist.sh")
		if !strings.Contains(script, "workstation_env_whitelist()") {
			t.Fatal("polyforge#161: env-whitelist.sh must define the workstation_env_whitelist function the entrypoint sources")
		}
		// run the REAL filter in a controlled environment — the
		// expected set comes from AC3 and settled Q6 (PI_* and
		// PI_WEB_* pass, PI_TEST_* never, nothing else does, the one
		// fixed value and PORT's default are the image's own).
		cmd := exec.Command("sh", "-c",
			`. "`+filepath.Join(w535Root(t), "dist/workstation/env-whitelist.sh")+`" && workstation_env_whitelist && env`)
		cmd.Env = []string{
			"PATH=" + os.Getenv("PATH"),
			"HOME=/root",
			"TERM=dumb",
			"PORT=30141",
			"PI_WEB_APP_NAME=probe",
			"PI_SMOKE_PROBE=passed",
			"PI_WEB_PASSWORD=x",
			"PI_TEST_PROBE=must-not-cross",
			"WORKSTATION_AIHUB_SERVER=https://hub.example.invalid",
			"WORKSTATION_AIHUB_KEY=probe-key",
			"WORKSTATION_AIHUB_PROJECT=probe-project",
			"WORKSTATION_OTHER=leak",
			"SENTINEL_WORKSTATION=leak",
			"PF_OUTER=leak",
			"http_proxy=leak",
		}
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("polyforge#161: the whitelist must run: %v:\n%s", err, out)
		}
		env := string(out)
		for _, keep := range []string{
			"PORT=30141", "PI_WEB_APP_NAME=probe", "PI_SMOKE_PROBE=passed", "PI_WEB_PASSWORD=x", "BUILDAH_ISOLATION=chroot",
			// the first boot's external-mode inputs cross (84-components-C)
			"WORKSTATION_AIHUB_SERVER=https://hub.example.invalid", "WORKSTATION_AIHUB_KEY=probe-key", "WORKSTATION_AIHUB_PROJECT=probe-project",
		} {
			if !strings.Contains(env, "\n"+keep) && !strings.HasPrefix(env, keep) {
				t.Fatalf("the whitelist must keep %s — the PI_* families, PORT and the first boot's inputs pass (AC3 + 84-components-C):\n%s", keep, env)
			}
		}
		for _, drop := range []string{"PI_TEST_PROBE", "WORKSTATION_OTHER", "SENTINEL_WORKSTATION", "PF_OUTER", "http_proxy"} {
			if strings.Contains(env, drop) {
				t.Fatalf("the whitelist must drop %s — PI_TEST_* never crosses, only the three named WORKSTATION_AIHUB_* inputs pass, and the container inherits nothing else (AC3):\n%s", drop, env)
			}
		}
	})

	t.Run("the build context is trimmed and dist is a declared component", func(t *testing.T) {
		ignore := w535Deliverable(t, ".dockerignore")
		// The seed's inputs (polyforge#162, 84-components-A) widened the
		// context: skills, workflows and pi-web are IN it now, so what
		// stays out is their dependency caches and test trees, never the
		// trees themselves.
		for _, heavy := range []string{
			".git", ".github", ".pi", ".polyforge", "docs", "test", "deploy", "tools",
			"webui/node_modules", "webui/e2e", "webui/dist",
			"pi-web/node_modules", "pi-web/.next", "pi-web/e2e", "pi-web/.polyforge",
		} {
			if !regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(heavy) + `(/|)?$`).MatchString(ignore) {
				t.Fatalf("the .dockerignore must exclude %s from the repo-root build context (no dependency caches, no test trees, no credentials)", heavy)
			}
		}
		for _, needed := range []string{"cmd", "internal", "webui", "dist", "package.json", "skills", "workflows", "pi-web"} {
			if regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(needed) + `(\*|/)?$`).MatchString(ignore) {
				t.Fatalf("the .dockerignore must NOT exclude %s — the image build needs it (the base and, since polyforge#162, the seed's release checkout)", needed)
			}
		}
		conf := w535Deliverable(t, ".polyforge/verify-scope.conf")
		if !regexp.MustCompile(`(?m)^component=dist\|dist$`).MatchString(conf) {
			t.Fatal("polyforge#161: .polyforge/verify-scope.conf must register the dist component (settled Q8)")
		}
	})
}

// w535ComposeService is one compose service's surface the workstation
// pins (the shared anchor the two profile services expand to).
type w535ComposeService struct {
	Build struct {
		Context    string `yaml:"context"`
		Dockerfile string `yaml:"dockerfile"`
	} `yaml:"build"`
	Image         string            `yaml:"image"`
	ContainerName string            `yaml:"container_name"`
	CapAdd        []string          `yaml:"cap_add"`
	SecurityOpt   []string          `yaml:"security_opt"`
	Cgroup        string            `yaml:"cgroup"`
	StopGrace     string            `yaml:"stop_grace_period"`
	Volumes       []string          `yaml:"volumes"`
	Environment   map[string]string `yaml:"environment"`
	EnvFile       []string          `yaml:"env_file"`
	Ports         []string          `yaml:"ports"`
	Profiles      []string          `yaml:"profiles"`
	Command       []string          `yaml:"command"`
	Privileged    bool              `yaml:"privileged"`
	Devices       []string          `yaml:"devices"`
	Pid           string            `yaml:"pid"`
	Networks      map[string]struct {
		Aliases []string `yaml:"aliases"`
	} `yaml:"networks"`
}

// w535Compose is the compose file's pinned shape.
type w535Compose struct {
	Name     string                        `yaml:"name"`
	Services map[string]w535ComposeService `yaml:"services"`
	Volumes  map[string]struct{}           `yaml:"volumes"`
}

// TestWorkstationCompose — the outer container's minimal parameter set
// (AC1) and its environment boundary (AC3), as compose declares them.
// Every parameter assertion is a SET equality or an exact value, never
// a containment: the ticket's own risk is that one extra cap or a
// relaxed security opt turns the smoke green while the workstation
// quietly stopped being the minimal machine.
func TestWorkstationCompose(t *testing.T) {
	body := w535Deliverable(t, "dist/workstation/compose.yaml")
	var compose w535Compose
	if err := yaml.Unmarshal([]byte(body), &compose); err != nil {
		t.Fatalf("polyforge#161: compose.yaml must parse: %v", err)
	}
	if len(compose.Services) != 3 {
		names := make([]string, 0, len(compose.Services))
		for name := range compose.Services {
			names = append(names, name)
		}
		t.Fatalf("polyforge#161: compose.yaml declares %v, want exactly the no-aihub and aihub boot variants plus the opt-in https front", names)
	}
	svc, ok := compose.Services["workstation"]
	if !ok {
		t.Fatal("polyforge#176: compose.yaml must carry the workstation service (the aihub default boot)")
	}
	noaihub, ok := compose.Services["workstation-noaihub"]
	if !ok {
		t.Fatal("polyforge#176: compose.yaml must carry the workstation-noaihub service (the explicit without-AIHub opt-out)")
	}
	for name, s := range map[string]w535ComposeService{"workstation": svc, "workstation-noaihub": noaihub} {
		// polyforge#177: no fixed container_name (concurrent deployments and
		// smoke runs clash on the name; the network alias is the stable name)
		// and the image is parameterized — WORKSTATION_IMAGE runs a published
		// image without any build, the local build tags the default.
		if s.ContainerName != "" {
			t.Fatalf("%s: container_name = %q, want none (a fixed container name makes concurrent deployments clash)", name, s.ContainerName)
		}
		if s.Image != "${WORKSTATION_IMAGE:-polyforge-workstation:local}" {
			t.Fatalf("%s: image = %q, want the WORKSTATION_IMAGE-parameterized default polyforge-workstation:local", name, s.Image)
		}
		// AC1's minimal set, as a set: exactly SYS_ADMIN and NET_ADMIN.
		if len(s.CapAdd) != 2 {
			t.Fatalf("%s: cap_add = %v, want exactly the two measured capabilities", name, s.CapAdd)
		}
		caps := map[string]bool{}
		for _, c := range s.CapAdd {
			caps[c] = true
		}
		if !caps["CAP_SYS_ADMIN"] || !caps["CAP_NET_ADMIN"] {
			t.Fatalf("%s: cap_add = %v, want {CAP_SYS_ADMIN, CAP_NET_ADMIN} and nothing else", name, s.CapAdd)
		}
		var seccomp, apparmor, systempaths bool
		for _, opt := range s.SecurityOpt {
			switch {
			case opt == "seccomp:./seccomp.json":
				seccomp = true
			case opt == "apparmor=unconfined":
				apparmor = true
			case opt == "systempaths=unconfined":
				systempaths = true
			}
		}
		if !seccomp {
			t.Fatalf("%s: security_opt must apply the pinned dist/workstation/seccomp.json (seccomp:./seccomp.json, relative to the compose file)", name)
		}
		if !apparmor || !systempaths {
			t.Fatalf("%s: security_opt must carry apparmor=unconfined and systempaths=unconfined (the measured outer set)", name)
		}
		if s.Cgroup != "private" {
			t.Fatalf("%s: cgroup = %q, want private (the entrypoint initializes a private cgroup namespace)", name, s.Cgroup)
		}
		if s.Privileged {
			t.Fatalf("%s: privileged must never be true", name)
		}
		if len(s.Devices) != 0 {
			t.Fatalf("%s: devices = %v, want none", name, s.Devices)
		}
		if s.Pid == "host" {
			t.Fatalf("%s: pid host is not the workstation's shape", name)
		}
		for _, vol := range s.Volumes {
			if strings.Contains(vol, "docker.sock") {
				t.Fatalf("%s: the volume list must not mount a docker socket: %s", name, vol)
			}
		}
		// the stop grace: the daemon's shutdown can take up to 30s of WIP
		// pushes, pf supervise's own grace defaults to 60s, and the
		// outer container must sit strictly above it — ≥ 45s is the
		// AC's floor, but a grace at or below supervise's own would cut
		// the daemon's shutdown in half (the #160 leftover's "grace
		// plus a margin").
		grace, err := time.ParseDuration(s.StopGrace)
		if err != nil || grace <= 60*time.Second {
			t.Fatalf("%s: stop_grace_period = %q, want a parseable duration strictly above pf supervise's 60s grace", name, s.StopGrace)
		}
		// the environment boundary: compose's explicit list and nothing
		// wholesale; the required password is a hard error when unset
		// (pi-web's proxy lets /api through and redirects /login when
		// no password is set — the AC's 200/401 pair only holds with
		// one, so compose must refuse to start without it).
		if len(s.EnvFile) != 0 {
			t.Fatalf("%s: env_file must never carry a wholesale environment list", name)
		}
		for key, value := range s.Environment {
			if strings.HasPrefix(key, "PI_TEST_") {
				t.Fatalf("%s: %s must never be listed — PI_TEST_* never crosses the boundary", name, key)
			}
			if strings.Contains(value, "PI_WEB_PASSWORD:?") && key == "PI_WEB_PASSWORD" {
				if !strings.HasPrefix(value, "${PI_WEB_PASSWORD:?") {
					t.Fatalf("%s: PI_WEB_PASSWORD must be the required interpolation form, got %q", name, value)
				}
			}
		}
		if !strings.HasPrefix(s.Environment["PI_WEB_PASSWORD"], "${PI_WEB_PASSWORD:?") {
			t.Fatalf("%s: PI_WEB_PASSWORD must be ${PI_WEB_PASSWORD:?…} — the compose interpolation refuses an unset password (settled Q5)", name)
		}
		if s.Environment["PORT"] == "" {
			t.Fatalf("%s: PORT must be declared (AC3: PORT passes through to Pi Web)", name)
		}
		if s.Environment["PI_WEB_HOSTNAME"] != "0.0.0.0" {
			t.Fatalf("%s: PI_WEB_HOSTNAME must be 0.0.0.0 — a container must bind all interfaces to be reachable (pi-web's own compose precedent)", name)
		}
		// the step agents' harness home (84-components-C): inside
		// pf-home, so a login and the harness references survive a
		// re-create of the container
		if s.Environment["PI_CODING_AGENT_DIR"] != "/root/.pi/agent" {
			t.Fatalf("%s: PI_CODING_AGENT_DIR = %q, want /root/.pi/agent (the agent dir lives in the pf-home volume)", name, s.Environment["PI_CODING_AGENT_DIR"])
		}
		// the first boot's external-mode inputs (84-components-C):
		// optional interpolations, empty by default (the local mode)
		for key, want := range map[string]string{
			"WORKSTATION_AIHUB_SERVER":  "${WORKSTATION_AIHUB_SERVER:-}",
			"WORKSTATION_AIHUB_KEY":     "${WORKSTATION_AIHUB_KEY:-}",
			"WORKSTATION_AIHUB_PROJECT": "${WORKSTATION_AIHUB_PROJECT:-}",
		} {
			if s.Environment[key] != want {
				t.Fatalf("%s: %s = %q, want the optional interpolation %q", name, key, s.Environment[key], want)
			}
		}
		// the six persistent volumes (84-components-C), each its own
		// named volume at its canonical path: pf-home owns /root whole
		// (cli.toml, the agent dir, the logins) with the data dir
		// nested inside it, the components volume carries the
		// versioned components, the aihub volume the local AIHub's
		// store, the workspace volume the working tree.
		volAt := map[string]string{}
		for _, vol := range s.Volumes {
			if name, target, found := strings.Cut(vol, ":"); found {
				volAt[name] = target
			}
		}
		for vol, target := range map[string]string{
			"pf-home":        "/root",
			"polyforge-data": "/root/.local/share/polyforge",
			"components":     "/components",
			"aihub":          "/var/lib/polyforge/aihub",
			"workspace":      "/workspace",
			"podman-storage": "/var/lib/containers/storage",
		} {
			if volAt[vol] != target {
				t.Fatalf("%s: volume %s mounted at %q, want %q (the six persistent volumes, each at its canonical path): %v", name, vol, volAt[vol], target, s.Volumes)
			}
		}
		if len(s.Volumes) != 6 {
			t.Fatalf("%s: volumes = %v, want exactly the six persistent volumes (caddy rides the https front only)", name, s.Volumes)
		}
		if s.Build.Context != "../.." || s.Build.Dockerfile != "dist/workstation/Containerfile" {
			t.Fatalf("%s: build must use the repository root context and the dist/workstation/Containerfile (the pf binary builds from this tree)", name)
		}
		var port bool
		for _, p := range s.Ports {
			if strings.HasSuffix(p, ":30141") {
				port = true
			}
		}
		if !port {
			t.Fatalf("%s: ports must publish Pi Web's 30141", name)
		}
	}
	// the profiles carry the smoke's two boot modes one-to-one
	// (the #176 default flip: the aihub boot is THE workstation, and
	// running without an AIHub is the explicit opt-out) — and the
	// --with-aihub flag reaches pf supervise only in the default
	// variant. Both carry an explicit profile: compose's --profile can
	// only add, never subtract, so two explicit profiles are the only
	// shape that keeps the modes mutually exclusive (#161's measured
	// lesson).
	if len(svc.Profiles) != 1 || svc.Profiles[0] != "aihub" {
		t.Fatalf("workstation: profiles = %v, want exactly [aihub] (the default boot)", svc.Profiles)
	}
	if len(noaihub.Profiles) != 1 || noaihub.Profiles[0] != "no-aihub" {
		t.Fatalf("workstation-noaihub: profiles = %v, want exactly [no-aihub] (the explicit opt-out)", noaihub.Profiles)
	}
	if !contains(svc.Command, "--with-aihub") {
		t.Fatal("workstation: the default boot must pass --with-aihub to pf supervise (the local AIHub is a watched child)")
	}
	if contains(noaihub.Command, "--with-aihub") {
		t.Fatal("workstation-noaihub: the opt-out must not pass --with-aihub")
	}
	// the versioned components run from the components volume, so
	// both variants point supervise's component paths there (the
	// #161-era <data-dir>/release/current layout is gone)
	for name, s := range map[string]w535ComposeService{"workstation": svc, "workstation-noaihub": noaihub} {
		if !contains(s.Command, "/components/current/workflows") || !contains(s.Command, "/components/current/pi-web") {
			t.Fatalf("%s: command = %v, must point --workflows-dir and --pi-web-dir at /components/current (the components volume is where the versions run)", name, s.Command)
		}
		if !contains(s.Command, "--grace") {
			t.Fatalf("%s: command must pin pf supervise's stop grace (the outer grace must exceed the daemon's shutdown)", name)
		}
	}
	for _, vol := range []string{"pf-home", "polyforge-data", "components", "aihub", "workspace", "podman-storage", "caddy-data"} {
		if _, ok := compose.Volumes[vol]; !ok {
			t.Fatalf("polyforge#176: the compose file must declare the %s named volume", vol)
		}
	}

	// the optional HTTPS front (settled Q7: off by default, opened by a
	// compose profile — the ticket's "可选 AIHub 与 HTTPS 前置"). It
	// must never ride the default boots: the two boot profiles above
	// stay exactly [aihub] and [no-aihub].
	front, ok := compose.Services["https-front"]
	if !ok {
		t.Fatal("polyforge#161: compose.yaml must carry the https-front service — the ticket's optional HTTPS front (settled Q7)")
	}
	if len(front.Profiles) != 1 || front.Profiles[0] != "https" {
		t.Fatalf("polyforge#161: https-front profiles = %v, want exactly [https] — the front is opt-in, never a default boot", front.Profiles)
	}
	if front.Image != "docker.io/library/caddy:2.10.2-alpine" {
		t.Fatalf("polyforge#161: https-front image = %q, want the pinned docker.io/library/caddy:2.10.2-alpine", front.Image)
	}
	var caddyfile string
	for _, vol := range front.Volumes {
		if strings.HasPrefix(vol, "./Caddyfile:/etc/caddy/Caddyfile:ro") {
			caddyfile = vol
		}
	}
	if caddyfile == "" {
		t.Fatalf("polyforge#161: https-front must mount ./Caddyfile read-only at /etc/caddy/Caddyfile (its config), got %v", front.Volumes)
	}
	var httpsPort bool
	for _, p := range front.Ports {
		if strings.HasSuffix(p, ":443") {
			httpsPort = true
		}
	}
	if !httpsPort {
		t.Fatalf("polyforge#161: https-front must publish the HTTPS port 443, got %v", front.Ports)
	}
	// the front terminates TLS with its own local CA and proxies the
	// workstation's Pi Web port. The SITE ADDRESS must be configurable:
	// a hostname in caddy's site address serves only requests whose
	// Host/SNI matches it (review r2#1 — the first cut hardcoded
	// `workstation:443`, which serves no request the documentation's
	// `https://<host>:8443` shape ever makes), and the proxy target must
	// resolve under BOTH boot modes.
	caddy := w535Deliverable(t, "dist/workstation/Caddyfile")
	if !strings.Contains(caddy, "{$WORKSTATION_HTTPS_HOST:localhost} {") {
		t.Fatal("polyforge#161: the Caddyfile's site address must be the configurable {$WORKSTATION_HTTPS_HOST:localhost} — caddy serves only the hostname the site address names")
	}
	if strings.Contains(caddy, "workstation:443 {") {
		t.Fatal("polyforge#161: the Caddyfile must not hardcode workstation:443 as its site address — the operator accesses the front by their own hostname")
	}
	if !strings.Contains(caddy, "reverse_proxy workstation:30141") {
		t.Fatal("polyforge#161: the Caddyfile must reverse_proxy the workstation service's Pi Web port 30141")
	}
	if !strings.Contains(caddy, "tls internal") {
		t.Fatal("polyforge#161: the Caddyfile must terminate TLS with caddy's internal CA (tls internal) — no external cert flow was asked for")
	}
	if front.Environment["WORKSTATION_HTTPS_HOST"] != "${WORKSTATION_HTTPS_HOST:-localhost}" {
		t.Fatalf("polyforge#161: https-front must carry WORKSTATION_HTTPS_HOST=${WORKSTATION_HTTPS_HOST:-localhost} so the site address follows the operator's hostname, got %q", front.Environment["WORKSTATION_HTTPS_HOST"])
	}
	// the proxy target resolves under both boot modes: every workstation
	// boot service carries the `workstation` network alias.
	for name, s := range map[string]w535ComposeService{"workstation": svc, "workstation-noaihub": noaihub} {
		if !contains(s.Networks["default"].Aliases, "workstation") {
			t.Fatalf("%s: the boot service must carry the `workstation` network alias — the https front reverse-proxies that name (review r2#1)", name)
		}
	}
}

// TestWorkstationSmoke — the smoke script's boot behaviour, driven
// through a stub docker CLI (review round 2: the first cut asserted
// source text, which its own test-quality record did not claim and
// review judged weakened; this run is what u3's units.json record now
// names). The stub answers version/inspect/exec/compose from a
// scenario directory, and enforces compose's required-interpolation
// contract at `compose up` — a missing PI_WEB_PASSWORD refuses the way
// the compose file's ${PI_WEB_PASSWORD:?…} does. Every scenario runs
// WITHOUT PI_WEB_PASSWORD in the environment, so the pass case also
// proves the smoke carries its own fixed default (settled Q5).
func TestWorkstationSmoke(t *testing.T) {
	root := w535Root(t)
	script := filepath.Join(root, "dist/workstation/smoke.sh")
	if err := exec.Command("sh", "-n", script).Run(); err != nil {
		t.Fatalf("polyforge#161: smoke.sh must parse as POSIX sh: %v", err)
	}

	// w535SmokeBoot runs `boot --no-aihub` against one scenario and
	// answers its combined output and exit status. The scenario mutates
	// the stub's answers; PI_WEB_PASSWORD is never set, so the boot
	// must supply the default itself.
	w535SmokeBoot := func(mutate func(scenario map[string]any)) (string, error) {
		scenario := map[string]any{
			"caps":     []string{"CAP_SYS_ADMIN", "CAP_NET_ADMIN"},
			"seccomp":  w535JSONAny(t, w535Deliverable(t, "dist/workstation/seccomp.json")),
			"cgroupns": "private",
			"pid1":     "catatonit\n",
			"subtree":  "cpu io memory pids\n",
		}
		if mutate != nil {
			mutate(scenario)
		}
		dir := t.TempDir()
		seccompInline, err := json.Marshal(scenario["seccomp"])
		if err != nil {
			t.Fatal(err)
		}
		inspect, err := json.Marshal([]any{ // docker inspect answers a list of one object
			map[string]any{
				"HostConfig": map[string]any{
					"CapAdd":       scenario["caps"],
					"SecurityOpt":  []string{"seccomp=" + string(seccompInline)},
					"CgroupnsMode": scenario["cgroupns"],
					"Privileged":   false,
					"Devices":      []any{},
					"Binds":        []string{"workstation_pf-home:/root:rw", "workstation_polyforge-data:/root/.local/share/polyforge:rw", "workstation_components:/components:rw", "workstation_aihub:/var/lib/polyforge/aihub:rw", "workstation_workspace:/workspace:rw", "workstation_podman-storage:/var/lib/containers/storage:rw"},
				},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "inspect.json"), inspect, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "pid1.txt"), []byte(scenario["pid1"].(string)), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "subtree.txt"), []byte(scenario["subtree"].(string)), 0o644); err != nil {
			t.Fatal(err)
		}
		bin := t.TempDir()
		if err := os.Symlink(filepath.Join(root, "test", "testdata", "workstation-smoke-stub", "docker"), filepath.Join(bin, "docker")); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("sh", script, "boot", "--no-aihub")
		cmd.Env = []string{
			"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"),
			"HOME=" + os.Getenv("HOME"),
			"W535_SMOKE_SCENARIO=" + dir,
		}
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	t.Run("the measured parameters pass, without a PI_WEB_PASSWORD set", func(t *testing.T) {
		out, err := w535SmokeBoot(nil)
		if err != nil {
			t.Fatalf("the boot of the measured minimal parameters must pass (the smoke supplies its own default password): %v:\n%s", err, out)
		}
		if !strings.Contains(out, "boot --no-aihub: PASS") {
			t.Fatalf("the passing boot must print its PASS line:\n%s", out)
		}
	})
	t.Run("one extra capability fails the boot", func(t *testing.T) {
		out, err := w535SmokeBoot(func(scenario map[string]any) {
			scenario["caps"] = []string{"CAP_SYS_ADMIN", "CAP_NET_ADMIN", "CAP_SYS_PTRACE"}
		})
		if err == nil {
			t.Fatalf("a boot with a third capability must fail — the cap set is a set, never a containment:\n%s", out)
		}
		if !strings.Contains(out, "CAP_SYS_PTRACE") {
			t.Fatalf("the failure must name the extra capability:\n%s", out)
		}
	})
	t.Run("a drifted seccomp profile fails the boot", func(t *testing.T) {
		out, err := w535SmokeBoot(func(scenario map[string]any) {
			scenario["seccomp"] = w535JSONAny(t, `{"defaultAction":"SCMP_ACT_ERRNO","syscalls":[]}`)
		})
		if err == nil {
			t.Fatalf("a boot with a seccomp profile that is not the pinned file must fail:\n%s", out)
		}
		if !strings.Contains(out, "seccomp content hash") {
			t.Fatalf("the failure must name the seccomp content mismatch:\n%s", out)
		}
	})
	t.Run("a wrong PID 1 fails the boot", func(t *testing.T) {
		out, err := w535SmokeBoot(func(scenario map[string]any) {
			scenario["pid1"] = "sh\n"
		})
		if err == nil {
			t.Fatalf("a boot whose PID 1 is not catatonit must fail:\n%s", out)
		}
		if !strings.Contains(out, "PID 1 is sh") {
			t.Fatalf("the failure must name what PID 1 is:\n%s", out)
		}
	})
	t.Run("a missing memory controller fails the boot", func(t *testing.T) {
		out, err := w535SmokeBoot(func(scenario map[string]any) {
			scenario["subtree"] = "cpu io pids\n"
		})
		if err == nil {
			t.Fatalf("a boot without the memory controller must fail — the verification limits need it:\n%s", out)
		}
		if !strings.Contains(out, "memory controller") {
			t.Fatalf("the failure must name the missing controller:\n%s", out)
		}
	})

	t.Run("env: the sentinel rides the up that creates the container, /api answers 401", func(t *testing.T) {
		// the non-inheritance sentinel must be in the environment of the
		// `compose up` that CREATES the container — review r2#2 found it
		// riding only the build call, which made the "the host
		// environment did not leak in" check vacuously green. The stub
		// dumps its own environment on build and up; this subtest reads
		// both back. The /api check talks to a local stub server
		// answering the anonymous 401 Pi Web answers with a password.
		api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		}))
		defer api.Close()
		apiURL, err := url.Parse(api.URL)
		if err != nil {
			t.Fatal(err)
		}

		dir := t.TempDir()
		// the Pi Web process's environment as compose delivered it: the
		// explicit list plus the override's probe variables — never the
		// sentinel, never PI_TEST_* (the entrypoint whitelist drops it).
		environ := strings.Join([]string{
			"PORT=30141",
			"PI_WEB_APP_NAME=pf-workstation-smoke-app",
			"PI_SMOKE_PROBE=passed-through",
			"PI_WEB_PASSWORD=pf-workstation-smoke-test-password",
			"BUILDAH_ISOLATION=chroot",
		}, "\x00") + "\x00"
		if err := os.WriteFile(filepath.Join(dir, "environ.txt"), []byte(environ), 0o644); err != nil {
			t.Fatal(err)
		}
		bin := t.TempDir()
		if err := os.Symlink(filepath.Join(root, "test", "testdata", "workstation-smoke-stub", "docker"), filepath.Join(bin, "docker")); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("sh", script, "env")
		cmd.Env = []string{
			"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"),
			"HOME=" + os.Getenv("HOME"),
			"W535_SMOKE_SCENARIO=" + dir,
			"WORKSTATION_WEB_PORT=" + apiURL.Port(),
		}
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("the env subcommand must pass against the stub:\n%s", out)
		}
		if !strings.Contains(string(out), "env: PASS") {
			t.Fatalf("the passing env run must print its PASS line:\n%s", out)
		}
		upEnv, err := os.ReadFile(filepath.Join(dir, "compose-up-env.txt"))
		if err != nil {
			t.Fatalf("the stub must record the environment of the up that creates the container: %v", err)
		}
		if !strings.Contains(string(upEnv), "SENTINEL_WORKSTATION_SMOKE=yes") {
			t.Fatalf("the non-inheritance sentinel must ride the `up` that creates the container — only then can the environ check mean anything:\n%s", upEnv)
		}
		if !strings.Contains(string(upEnv), "PI_WEB_PASSWORD=") {
			t.Fatalf("the smoke's own default password must reach the up call too:\n%s", upEnv)
		}
	})
}

// w535JSONAny answers one JSON document as a plain any (the scenario
// map carries it; json.Marshal compacts it again on the way out).
func w535JSONAny(t *testing.T, doc string) any {
	t.Helper()
	var out any
	if err := json.Unmarshal([]byte(doc), &out); err != nil {
		t.Fatalf("polyforge#161: the document must be JSON: %v", err)
	}
	return out
}

// TestWorkstationImageEngine — the real-engine half of the owner's
// split (settled Q3 C): with a reachable docker engine and compose it
// drives the smoke script's --no-aihub boot end to end (the image
// builds, the container starts under compose, and every AC1 parameter
// inspects as pinned); without an engine it skips, exactly like the
// real-container suites' engine gates.
func TestWorkstationImageEngine(t *testing.T) {
	defer w535LockSmoke(t)()
	root := w535Root(t)
	if out, err := exec.Command("docker", "version", "--format", "{{.Server.Version}}").CombinedOutput(); err != nil {
		t.Skipf("no docker engine (%v: %s), skipping the real-engine workstation boot", err, strings.TrimSpace(string(out)))
	}
	if err := exec.Command("docker", "compose", "version").Run(); err != nil {
		t.Skipf("no docker compose (%v), skipping the real-engine workstation boot", err)
	}
	// the workstation is a first-boot machine now (polyforge#176), and
	// the smoke runs under a per-run unique compose project
	// (polyforge#177), so every engine run starts on fresh volumes by
	// itself — no pre-reset of a fixed project is needed (or possible)
	// any more.
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", filepath.Join(root, "dist/workstation/smoke.sh"), "boot", "--no-aihub")
	// no PI_WEB_PASSWORD is injected: the engine run exercises the
	// smoke's own default export too (review r1#2 — the first cut
	// masked the missing export by injecting one here).
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("polyforge#161: smoke boot --no-aihub failed: %v:\n%s", err, out)
	}
	if !strings.Contains(string(out), "boot --no-aihub: PASS") {
		t.Fatalf("polyforge#161: the smoke run never printed its PASS line:\n%s", out)
	}
}

// w535SmokeFirstBoot runs `smoke.sh first-boot` (the default local pass)
// against one stub scenario and answers its combined output and exit
// status. The scenario carries the record the container's exec reads
// back (first-boot.json), the default Cell's server as `pf config get
// server` answers it, and the two failure flags: recall-fails (the key
// does not authenticate) and bootstrap-key-exists (the server's own
// bootstrap fired anyway).
func w535SmokeFirstBoot(t *testing.T, mutate func(dir string)) (string, error) {
	t.Helper()
	root := w535Root(t)
	script := filepath.Join(root, "dist/workstation/smoke.sh")
	dir := t.TempDir()
	record := `{"phase":"done","mode":"local","defaultCell":"default","server":"http://127.0.0.1:8420","userReady":true,"keyReady":true,"imageManaged":true,"updatedAt":"2026-11-20T00:00:00Z"}`
	if err := os.WriteFile(filepath.Join(dir, "first-boot.json"), []byte(record), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cell-server.txt"), []byte("http://127.0.0.1:8420\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if mutate != nil {
		mutate(dir)
	}
	bin := t.TempDir()
	if err := os.Symlink(filepath.Join(root, "test", "testdata", "workstation-smoke-stub", "docker"), filepath.Join(bin, "docker")); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", script, "first-boot")
	cmd.Env = []string{
		"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
		"W535_SMOKE_SCENARIO=" + dir,
		"WORKSTATION_SMOKE_STUB_PORT=38421",
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// TestWorkstationSmokeFirstBoot — the smoke script's first-boot pass
// (polyforge#176): the record's phase gates the pass (anything but a
// completed local boot fails naming the phase), the key's real
// authentication is the proof (a failing pf recall fails the run naming
// it), and a bootstrap-admin key existing is a failure by itself (the
// identity must predate the server, so the server's own bootstrap has
// nothing left to seed).
func TestWorkstationSmokeFirstBoot(t *testing.T) {
	root := w535Root(t)
	script := filepath.Join(root, "dist/workstation/smoke.sh")
	if err := exec.Command("sh", "-n", script).Run(); err != nil {
		t.Fatalf("polyforge#176: smoke.sh must parse as POSIX sh: %v", err)
	}
	t.Run("the passing local first boot", func(t *testing.T) {
		out, err := w535SmokeFirstBoot(t, nil)
		if err != nil {
			t.Fatalf("the first-boot pass must pass against a completed local record:\n%s", out)
		}
		if !strings.Contains(out, "first-boot: PASS") {
			t.Fatalf("the passing first boot must print its PASS line:\n%s", out)
		}
	})
	t.Run("an incomplete record fails naming the phase", func(t *testing.T) {
		out, err := w535SmokeFirstBoot(t, func(dir string) {
			record := `{"phase":"config/failed","mode":"local","error":"x","updatedAt":"2026-11-20T00:00:00Z"}`
			if err := os.WriteFile(filepath.Join(dir, "first-boot.json"), []byte(record), 0o644); err != nil {
				t.Fatal(err)
			}
		})
		if err == nil {
			t.Fatalf("a first boot whose record is not done must fail:\n%s", out)
		}
		if !strings.Contains(out, "phase") {
			t.Fatalf("the failure must name the phase:\n%s", out)
		}
	})
	t.Run("a key that does not authenticate fails the run", func(t *testing.T) {
		out, err := w535SmokeFirstBoot(t, func(dir string) {
			if err := os.WriteFile(filepath.Join(dir, "recall-fails"), []byte("1"), 0o644); err != nil {
				t.Fatal(err)
			}
		})
		if err == nil {
			t.Fatalf("a default Cell whose key cannot authenticate must fail the run:\n%s", out)
		}
		if !strings.Contains(out, "authenticate") {
			t.Fatalf("the failure must name the authentication proof:\n%s", out)
		}
	})
	t.Run("a wrong agent dir fails the run", func(t *testing.T) {
		out, err := w535SmokeFirstBoot(t, func(dir string) {
			if err := os.WriteFile(filepath.Join(dir, "agentdir-wrong"), []byte("1"), 0o644); err != nil {
				t.Fatal(err)
			}
		})
		if err == nil {
			t.Fatalf("an agent dir outside pf-home is the AC2 layout broken — the run must fail:\n%s", out)
		}
		if !strings.Contains(out, "agent dir") {
			t.Fatalf("the failure must name the agent dir:\n%s", out)
		}
	})
	t.Run("a pi packages entry off /components/current fails the run", func(t *testing.T) {
		out, err := w535SmokeFirstBoot(t, func(dir string) {
			if err := os.WriteFile(filepath.Join(dir, "packages-wrong"), []byte("1"), 0o644); err != nil {
				t.Fatal(err)
			}
		})
		if err == nil {
			t.Fatalf("a pi packages entry that does not point at /components/current is the AC2 harness half broken:\n%s", out)
		}
		if !strings.Contains(out, "packages") {
			t.Fatalf("the failure must name the packages entry:\n%s", out)
		}
	})
	t.Run("a credential in the record fails the run", func(t *testing.T) {
		out, err := w535SmokeFirstBoot(t, func(dir string) {
			if err := os.WriteFile(filepath.Join(dir, "credential-in-record"), []byte("1"), 0o644); err != nil {
				t.Fatal(err)
			}
		})
		if err == nil {
			t.Fatalf("a record that carries a credential is the AC2 leak — the run must fail:\n%s", out)
		}
		if !strings.Contains(out, "credential") {
			t.Fatalf("the failure must name the credential leak:\n%s", out)
		}
	})
	t.Run("a bootstrap-admin key fails the run", func(t *testing.T) {
		out, err := w535SmokeFirstBoot(t, func(dir string) {
			if err := os.WriteFile(filepath.Join(dir, "bootstrap-key-exists"), []byte("1"), 0o644); err != nil {
				t.Fatal(err)
			}
		})
		if err == nil {
			t.Fatalf("a bootstrap-admin key in the aihub volume means the identity did not predate the server — the run must fail:\n%s", out)
		}
		if !strings.Contains(out, "bootstrap") {
			t.Fatalf("the failure must name the bootstrap key:\n%s", out)
		}
	})
}

func TestWorkstationSmokeFirstBootSkills(t *testing.T) {
	out, err := w535SmokeFirstBoot(t, func(dir string) {
		if err := os.WriteFile(filepath.Join(dir, "skills-wrong"), []byte("1"), 0o644); err != nil {
			t.Fatal(err)
		}
	})
	if err == nil {
		t.Fatalf("错误技能软链必须使首启 smoke 失败：\n%s", out)
	}
	if !strings.Contains(out, "skill") {
		t.Fatalf("失败必须点名技能软链：\n%s", out)
	}
	out, err = w535SmokeFirstBoot(t, nil)
	if err != nil || !strings.Contains(out, "first-boot: PASS") {
		t.Fatalf("有效布局必须通过首启 smoke：%v\n%s", err, out)
	}
}

// TestWorkstationSmokeProjectIsolation — review r1#4, polyforge#177: the
// smoke owns its runs, never an operator's deployment and never another
// run's. The compose project is derived per run (clock + pid), so its
// `down -v` only ever touches its own volumes — and the host port is
// derived the same way (the fixed 30141 could clash with a running
// deployment), overridable by WORKSTATION_WEB_PORT.
func TestWorkstationSmokeProjectIsolation(t *testing.T) {
	body := w535Deliverable(t, "dist/workstation/smoke.sh")
	// the per-run project: derived from the clock and the pid
	if !strings.Contains(body, "smoke-$(date +%s)-$$") {
		t.Fatal("polyforge#177: the smoke must derive a per-run unique project (clock + pid) — a fixed project name lets concurrent runs down each other's volumes")
	}
	// every RAW docker compose invocation carries the per-run project
	// inline (the env and podman-verify passes and the first-boot pass's
	// compose_ext calls); the compose()/compose_ext helpers carry it by
	// construction (asserted above/below on their own definitions), the
	// bare `docker compose version` probes touch no project, and comments
	// and die/say messages merely mention the words
	for i, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		raw := strings.HasPrefix(trimmed, "docker compose") || strings.Contains(trimmed, "$(docker compose")
		if !raw || strings.HasPrefix(trimmed, "docker compose version") {
			continue
		}
		if !strings.Contains(trimmed, `"$smoke_project"`) {
			t.Fatalf("polyforge#177: smoke.sh line %d invokes docker compose without the per-run project: %q", i+1, trimmed)
		}
	}
	// every helper definition carries the project too (compose_ext wraps
	// the raw docker compose calls of the first-boot pass)
	for _, helper := range []string{"compose() {", "compose_ext() {"} {
		at := strings.Index(body, helper)
		if at < 0 {
			t.Fatalf("polyforge#177: the smoke must define %s", strings.TrimSuffix(helper, " {"))
		}
		rest := body[at:]
		end := strings.Index(rest, "\n}")
		if end < 0 {
			end = len(rest)
		}
		def := rest[:end]
		if !strings.Contains(def, `"$smoke_project"`) {
			t.Fatalf("polyforge#177: the %s helper must carry the per-run project", strings.TrimSuffix(helper, " {"))
		}
	}
	// the host port is derived per run unless WORKSTATION_WEB_PORT overrides
	if !strings.Contains(body, "WORKSTATION_WEB_PORT:-$((30500") {
		t.Fatal("polyforge#177: the smoke must derive a per-run host port (30500 + clock/pid) unless WORKSTATION_WEB_PORT overrides it")
	}
}

// w535SmokeFirstBootExternal runs `smoke.sh first-boot --external`
// against one stub scenario: the record the container reads back says
// an external boot, the default Cell names the counting stub, and the
// stub itself runs for real on the host (no container ever reaches it
// under the stub engine).
func w535SmokeFirstBootExternal(t *testing.T, mutate func(dir string)) (string, error) {
	t.Helper()
	defer w535LockSmoke(t)()
	root := w535Root(t)
	script := filepath.Join(root, "dist/workstation/smoke.sh")
	dir := t.TempDir()
	record := `{"phase":"done","mode":"external","defaultCell":"default","server":"http://host.docker.internal:38422","userReady":false,"keyReady":true,"imageManaged":true,"updatedAt":"2026-11-20T00:00:00Z"}`
	if err := os.WriteFile(filepath.Join(dir, "first-boot.json"), []byte(record), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cell-server.txt"), []byte("http://host.docker.internal:38422\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if mutate != nil {
		mutate(dir)
	}
	bin := t.TempDir()
	if err := os.Symlink(filepath.Join(root, "test", "testdata", "workstation-smoke-stub", "docker"), filepath.Join(bin, "docker")); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", script, "first-boot", "--external")
	cmd.Env = []string{
		"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
		"W535_SMOKE_SCENARIO=" + dir,
		"WORKSTATION_SMOKE_STUB_PORT=38422",
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// TestWorkstationSmokeFirstBootExternal — the smoke's external pass
// under the stub engine: the record gate names the mode, and a record
// that is not a completed external boot fails the run naming it.
func TestWorkstationSmokeFirstBootExternal(t *testing.T) {
	t.Run("the passing external pass", func(t *testing.T) {
		out, err := w535SmokeFirstBootExternal(t, nil)
		if err != nil {
			t.Fatalf("the external pass must pass against a completed external record:\n%s", out)
		}
		if !strings.Contains(out, "first-boot: PASS") {
			t.Fatalf("the passing external run must print its PASS line:\n%s", out)
		}
		if !strings.Contains(out, "the stub server saw 0 requests") {
			t.Fatalf("the external pass must assert the zero-remote fact:\n%s", out)
		}
	})
	t.Run("a record of the wrong mode fails the run", func(t *testing.T) {
		out, err := w535SmokeFirstBootExternal(t, func(dir string) {
			record := `{"phase":"done","mode":"local","defaultCell":"default","server":"http://127.0.0.1:8420","userReady":true,"keyReady":true,"imageManaged":true,"updatedAt":"2026-11-20T00:00:00Z"}`
			if err := os.WriteFile(filepath.Join(dir, "first-boot.json"), []byte(record), 0o644); err != nil {
				t.Fatal(err)
			}
		})
		if err == nil {
			t.Fatalf("a record whose mode is not external must fail the external pass:\n%s", out)
		}
		if !strings.Contains(out, "record") {
			t.Fatalf("the failure must name the record:\n%s", out)
		}
	})
}

func TestWorkstationSmokeExternalRejectsRemoteWrites(t *testing.T) {
	for _, request := range []string{
		"POST /api/v1/users",
		"POST /api/v1/api-keys",
		"POST /api/v1/projects",
		"PATCH /api/v1/users/usr_test",
		"DELETE /api/v1/api-keys/key_test",
		"DELETE /api/v1/projects/prj_test",
		"GET /api/v1/unexpected",
	} {
		t.Run(request, func(t *testing.T) {
			out, err := w535SmokeFirstBootExternal(t, func(dir string) {
				if err := os.WriteFile(filepath.Join(dir, "remote-request"), []byte(request+"\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			})
			if err == nil {
				t.Fatalf("远端写请求必须拒绝：%s\n%s", request, out)
			}
			if !strings.Contains(out, request) {
				t.Fatalf("拒绝必须点名请求：%s\n%s", request, out)
			}
		})
	}
	t.Run("heartbeat", func(t *testing.T) {
		out, err := w535SmokeFirstBootExternal(t, func(dir string) {
			if err := os.WriteFile(filepath.Join(dir, "remote-request"), []byte("POST /api/v1/machines/heartbeat\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		})
		if err != nil || !strings.Contains(out, "first-boot: PASS") {
			t.Fatalf("运行期心跳必须允许：%v\n%s", err, out)
		}
	})
}

// TestWorkstationSmokeFirstBootEngine — the real-engine external pass
// (polyforge#176, the round-2 real-machine finding): the counting stub
// sits on the host and the smoke must prove the FIRST BOOT itself makes
// no remote request — the zero-remote window is the boot (up to the
// record saying done), because the daemon's heartbeat to the Cell's
// server afterwards is the machine's normal connection, never an
// object the boot creates. Without an engine it skips, exactly like
// the other real-container engine gates.
func TestWorkstationSmokeFirstBootEngine(t *testing.T) {
	defer w535LockSmoke(t)()
	root := w535Root(t)
	if out, err := exec.Command("docker", "version", "--format", "{{.Server.Version}}").CombinedOutput(); err != nil {
		t.Skipf("no docker engine (%v: %s), skipping the real-engine external first boot", err, strings.TrimSpace(string(out)))
	}
	if err := exec.Command("docker", "compose", "version").Run(); err != nil {
		t.Skipf("no docker compose (%v), skipping the real-engine external first boot", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", filepath.Join(root, "dist/workstation/smoke.sh"), "first-boot", "--external")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "WORKSTATION_SMOKE_STUB_PORT=38421")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("polyforge#176: smoke first-boot --external failed on the real engine: %v:\n%s", err, out)
	}
	if !strings.Contains(string(out), "first-boot: PASS") {
		t.Fatalf("polyforge#176: the external run never printed its PASS line:\n%s", out)
	}
}
