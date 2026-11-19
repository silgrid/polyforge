// carries: dist/workstation/Containerfile, dist/workstation/entrypoint.sh,
//
//	dist/workstation/containers/*, dist/workstation/seccomp.json,
//	dist/workstation/seccomp.json.sha256, dist/workstation/README.md,
//	.dockerignore, .polyforge/verify-scope.conf
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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
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
		norm := regexp.MustCompile(`\s+`).ReplaceAllString(body, " ")
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
