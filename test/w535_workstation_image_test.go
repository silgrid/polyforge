// carries: dist/workstation/Containerfile, dist/workstation/entrypoint.sh,
//
//	dist/workstation/containers/*, dist/workstation/seccomp.json,
//	dist/workstation/seccomp.json.sha256, dist/workstation/README.md,
//	dist/workstation/compose.yaml, .dockerignore, .polyforge/verify-scope.conf
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
		if !strings.Contains(body, "PI_TEST_") {
			t.Fatal("polyforge#161: entrypoint.sh's environment whitelist must exclude PI_TEST_* — AC3's carve-out")
		}
		// the boundary is a whitelist, not a blacklist: the script
		// unsets everything that is not the fixed set or a PI_* family
		// member (AC3: the container never inherits the host's
		// environment wholesale).
		if !strings.Contains(body, "unset ") || !strings.Contains(body, "PI_*)") {
			t.Fatal("polyforge#161: entrypoint.sh must carry the whitelist loop — unset every name that is not the fixed set or PI_*")
		}
		if !strings.Contains(body, "BUILDAH_ISOLATION=chroot") {
			t.Fatal("polyforge#161: entrypoint.sh must set BUILDAH_ISOLATION=chroot — buildah's build-network ns open needs CAP_SYS_PTRACE on a non-dumpable runtime init, and AC1's minimal cap set must not grow (measured)")
		}
		if !strings.Contains(body, `exec pf supervise "$@"`) {
			t.Fatal("polyforge#161: entrypoint.sh must end by exec'ing pf supervise with the caller's flags (compose passes only the flags)")
		}
	})

	t.Run("the build context is trimmed and dist is a declared component", func(t *testing.T) {
		ignore := w535Deliverable(t, ".dockerignore")
		for _, heavy := range []string{".git", "pi-web", "webui/node_modules", "docs", "test", "workflows", "skills", "deploy", "tools"} {
			if !regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(heavy) + `$`).MatchString(ignore) {
				t.Fatalf("polyforge#161: .dockerignore must exclude %s from the repo-root build context", heavy)
			}
		}
		for _, needed := range []string{"cmd", "internal", "webui", "dist"} {
			if regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(needed) + `(\*|/)?$`).MatchString(ignore) {
				t.Fatalf("polyforge#161: .dockerignore must NOT exclude %s — the image build needs it", needed)
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
	CapAdd      []string          `yaml:"cap_add"`
	SecurityOpt []string          `yaml:"security_opt"`
	Cgroup      string            `yaml:"cgroup"`
	StopGrace   string            `yaml:"stop_grace_period"`
	Volumes     []string          `yaml:"volumes"`
	Environment map[string]string `yaml:"environment"`
	EnvFile     []string          `yaml:"env_file"`
	Ports       []string          `yaml:"ports"`
	Profiles    []string          `yaml:"profiles"`
	Command     []string          `yaml:"command"`
	Privileged  bool              `yaml:"privileged"`
	Devices     []string          `yaml:"devices"`
	Pid         string            `yaml:"pid"`
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
	if len(compose.Services) != 2 {
		names := make([]string, 0, len(compose.Services))
		for name := range compose.Services {
			names = append(names, name)
		}
		t.Fatalf("polyforge#161: compose.yaml declares %v, want exactly the no-aihub and aihub profile variants", names)
	}
	svc, ok := compose.Services["workstation"]
	if !ok {
		t.Fatal("polyforge#161: compose.yaml must carry the workstation service (the no-aihub default)")
	}
	aihub, ok := compose.Services["workstation-aihub"]
	if !ok {
		t.Fatal("polyforge#161: compose.yaml must carry the workstation-aihub service (the --with-aihub variant)")
	}
	for name, s := range map[string]w535ComposeService{"workstation": svc, "workstation-aihub": aihub} {
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
		// outer container must exceed it (≥ 45s the AC demands; the
		// margin is the #160 leftover's "grace plus a margin").
		grace, err := time.ParseDuration(s.StopGrace)
		if err != nil || grace < 45*time.Second {
			t.Fatalf("%s: stop_grace_period = %q, want a parseable duration of at least 45s and above pf supervise's grace", name, s.StopGrace)
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
		// both persistent boundaries: the data dir (the release layout
		// and the daemon's state) and podman's storage, each its own
		// named volume so a reset never crosses (84-components mounts
		// its component volumes under the data dir's release layout).
		var data, storage bool
		for _, vol := range s.Volumes {
			switch {
			case strings.HasPrefix(vol, "polyforge-data:/root/.local/share/polyforge"):
				data = true
			case strings.HasPrefix(vol, "podman-storage:/var/lib/containers/storage"):
				storage = true
			}
		}
		if !data || !storage {
			t.Fatalf("%s: volumes = %v, want the polyforge-data and podman-storage named volumes at their canonical paths", name, s.Volumes)
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
	// the profiles carry the smoke's two boot modes one-to-one (settled
	// Q7: default without AIHub, --with-aihub by profile) — and the
	// --with-aihub flag reaches pf supervise only in the aihub variant.
	if !contains(svc.Profiles, "no-aihub") {
		t.Fatalf("workstation: profiles = %v, want [no-aihub]", svc.Profiles)
	}
	if !contains(aihub.Profiles, "aihub") || contains(aihub.Profiles, "no-aihub") {
		t.Fatalf("workstation-aihub: profiles = %v, want [aihub] only", aihub.Profiles)
	}
	if contains(svc.Command, "--with-aihub") {
		t.Fatal("workstation: the default boot must not pass --with-aihub")
	}
	if !contains(aihub.Command, "--with-aihub") {
		t.Fatal("workstation-aihub: the aihub profile must pass --with-aihub to pf supervise")
	}
	for name, s := range map[string]w535ComposeService{"workstation": svc, "workstation-aihub": aihub} {
		if !contains(s.Command, "--grace") {
			t.Fatalf("%s: command must pin pf supervise's stop grace (the outer grace must exceed the daemon's shutdown)", name)
		}
	}
	if _, ok := compose.Volumes["polyforge-data"]; !ok {
		t.Fatal("polyforge#161: the compose file must declare the polyforge-data named volume")
	}
	if _, ok := compose.Volumes["podman-storage"]; !ok {
		t.Fatal("polyforge#161: the compose file must declare the podman-storage named volume")
	}
}

// TestWorkstationSmoke — the smoke script's static shape: the three
// subcommands the acceptance proofs name, the parameter checks AC1
// demands, and the bounded-poll discipline (never a fixed delay).
// The script's behaviour is driven for real by
// TestWorkstationImageEngine below on a machine with an engine, and by
// the verification gate through the same script.
func TestWorkstationSmoke(t *testing.T) {
	root := w535Root(t)
	script := filepath.Join(root, "dist/workstation/smoke.sh")
	body := w535Deliverable(t, "dist/workstation/smoke.sh")
	if err := exec.Command("sh", "-n", script).Run(); err != nil {
		t.Fatalf("polyforge#161: smoke.sh must parse as POSIX sh: %v", err)
	}
	// the dispatch: every acceptance proof's entry point.
	for _, sub := range []string{"boot", "podman-verify", "env"} {
		if !strings.Contains(body, sub) {
			t.Fatalf("polyforge#161: smoke.sh must dispatch the %q subcommand", sub)
		}
	}
	if !strings.Contains(body, "set -eu") {
		t.Fatal("polyforge#161: smoke.sh must run under set -eu — a missed check must fail the run, not continue")
	}
	if !strings.Contains(body, "poll_until") {
		t.Fatal("polyforge#161: smoke.sh must poll with a deadline (poll_until), never a fixed delay before asserting a settling state")
	}
	// the fixed smoke test password (settled Q5).
	if !strings.Contains(body, "pf-workstation-smoke-test-password") {
		t.Fatal("polyforge#161: smoke.sh must default PI_WEB_PASSWORD to the fixed test password (settled Q5)")
	}
	// AC1's parameter checks: the cap set as a set, the seccomp compare
	// by content, the cgroup namespace, and the forbidden mounts.
	for _, pin := range []string{
		"CapAdd", "SecurityOpt", "CgroupnsMode", "Privileged",
		"docker.sock", "/proc/1/comm", "catatonit",
	} {
		if !strings.Contains(body, pin) {
			t.Fatalf("polyforge#161: smoke.sh must check %s — an AC1 parameter the boot must measure", pin)
		}
	}
	// the boot modes and their halves.
	for _, pin := range []string{
		"--no-aihub", "--with-aihub", "pf aihub", "/login", "--json queue",
	} {
		if !strings.Contains(body, pin) {
			t.Fatalf("polyforge#161: smoke.sh must carry %q — a boot check the acceptance names", pin)
		}
	}
	// AC3's env half: the PI_TEST_* exclusion, the pass-through probe,
	// the non-inheritance sentinel and the anonymous /api answer.
	for _, pin := range []string{
		"PI_TEST_", "PI_WEB_APP_NAME", "SENTINEL", "/api", "401", "environ",
	} {
		if !strings.Contains(body, pin) {
			t.Fatalf("polyforge#161: smoke.sh's env subcommand must check %q", pin)
		}
	}
	// AC2's podman-verify half: both image paths, the limits, the three
	// modules, the cleanliness.
	for _, pin := range []string{
		"Dockerfile.verify", "build_image", "--pull=never",
		"memory.max", "pids.max", "tests.test_container_docker",
		"tests.test_gate_docker", "tests.test_cache_volume",
		"PF_CONTAINER_ENGINE", "conmon", "skipped",
	} {
		if !strings.Contains(body, pin) {
			t.Fatalf("polyforge#161: smoke.sh's podman-verify subcommand must carry %q", pin)
		}
	}
}

// TestWorkstationImageEngine — the real-engine half of the owner's
// split (settled Q3 C): with a reachable docker engine and compose it
// drives the smoke script's --no-aihub boot end to end (the image
// builds, the container starts under compose, and every AC1 parameter
// inspects as pinned); without an engine it skips, exactly like the
// real-container suites' engine gates.
func TestWorkstationImageEngine(t *testing.T) {
	root := w535Root(t)
	if out, err := exec.Command("docker", "version", "--format", "{{.Server.Version}}").CombinedOutput(); err != nil {
		t.Skipf("no docker engine (%v: %s), skipping the real-engine workstation boot", err, strings.TrimSpace(string(out)))
	}
	if err := exec.Command("docker", "compose", "version").Run(); err != nil {
		t.Skipf("no docker compose (%v), skipping the real-engine workstation boot", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", filepath.Join(root, "dist/workstation/smoke.sh"), "boot", "--no-aihub")
	cmd.Env = append(os.Environ(), "PI_WEB_PASSWORD=pf-workstation-smoke-test-password")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("polyforge#161: smoke boot --no-aihub failed: %v:\n%s", err, out)
	}
	if !strings.Contains(string(out), "boot --no-aihub: PASS") {
		t.Fatalf("polyforge#161: the smoke run never printed its PASS line:\n%s", out)
	}
}
