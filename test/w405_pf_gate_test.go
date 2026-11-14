package test

// w405_pf_gate_test.go: the test gate as the release runs it — the tree's
// own workflow script, `python3 workflows/_shared/scripts/gate.py
// --isolation container` from the clone's root (cmd/pf-gate
// before) — in a release world (release_script_test.go: a local bare
// origin, a clone carrying the gate tree and the script, the fake docker
// whose container runs a --workdir exec in that directory). No real remote
// and no real container is touched.
//
// Run (targeted): GOWORK=off go test ./test/ -run '^TestW405PfGate' -count=1

import (
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// w405GateScript is the gate as the world's tree carries it.
const w405GateScript = "workflows/_shared/scripts/gate.py"

// w405RunGate runs the tree's gate.py in the world's clone (with
// --isolation container, as every caller writes it): stdout, stderr, exit
// code.
func w405RunGate(t *testing.T, w *relWorld, env []string, args ...string) (string, string, int) {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not installed")
	}
	if !slices.Contains(args, "--log-dir") {
		args = append([]string{"--log-dir", t.TempDir()}, args...)
	}
	c := exec.Command("python3", append([]string{w405GateScript, "--isolation", "container"}, args...)...)
	c.Dir = w.work
	c.Env = w.env(env...)
	var out, errb strings.Builder
	c.Stdout, c.Stderr = &out, &errb
	err := c.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("gate.py: %v", err)
	}
	return out.String(), errb.String(), code
}

// w405GateRunArgs answers the gate container's `docker run` line.
func w405GateRunArgs(t *testing.T, w *relWorld) string {
	t.Helper()
	calls := w.calls()
	i := relIndex(calls, "docker run ")
	if i < 0 {
		t.Fatalf("no container started: %v", calls)
	}
	return calls[i] + " "
}

// TestW405PfGateLimitsAreTheScriptsFlags (design r2 D7): the gate's
// container takes the script's limits — min(8, this host's cores), 16g and
// 4096 unless a flag names one — and never the machine's pf CLI config
// (a [daemon] verify_container_* line an older install left there). A
// malformed limit is refused before any container starts.
func TestW405PfGateLimitsAreTheScriptsFlags(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not installed (the scope engine's --json needs it)")
	}
	defaults := " --cpus " + strconv.Itoa(min(8, goruntime.NumCPU())) + " --memory 16g --memory-swap 16g --pids-limit 4096 "
	t.Run("defaults", func(t *testing.T) {
		w := newRelWorld(t, []string{"v1.2.0"}, nil)
		if _, errb, code := w405RunGate(t, w, []string{"XDG_CONFIG_HOME=" + filepath.Join(w.root, "no-config")}); code != 0 {
			t.Fatalf("gate.py exit %d\n%s", code, errb)
		}
		if run := w405GateRunArgs(t, w); !strings.Contains(run, defaults) {
			t.Fatalf("container limits: %s", run)
		}
	})
	t.Run("the CLI config is no input", func(t *testing.T) {
		w := newRelWorld(t, []string{"v1.2.0"}, nil)
		xdg := filepath.Join(w.root, "xdg")
		relWrite(t, filepath.Join(xdg, "polyforge", "cli.toml"), "[daemon]\nverify_container_cpus = 3\nverify_container_memory = \"3G\"\nverify_container_pids = 333\n", 0o644)
		if _, errb, code := w405RunGate(t, w, []string{"XDG_CONFIG_HOME=" + xdg}); code != 0 {
			t.Fatalf("gate.py exit %d\n%s", code, errb)
		}
		if run := w405GateRunArgs(t, w); !strings.Contains(run, defaults) {
			t.Fatalf("container limits: %s", run)
		}
	})
	t.Run("flags", func(t *testing.T) {
		w := newRelWorld(t, []string{"v1.2.0"}, nil)
		if _, errb, code := w405RunGate(t, w, nil, "--cpus", "3", "--memory", "3G", "--pids", "333"); code != 0 {
			t.Fatalf("gate.py exit %d\n%s", code, errb)
		}
		if run := w405GateRunArgs(t, w); !strings.Contains(run, " --cpus 3 --memory 3g --memory-swap 3g --pids-limit 333 ") {
			t.Fatalf("container limits: %s", run)
		}
	})
	t.Run("malformed", func(t *testing.T) {
		w := newRelWorld(t, []string{"v1.2.0"}, nil)
		_, errb, code := w405RunGate(t, w, nil, "--memory", "8 gigs")
		if code != 2 || !strings.Contains(errb, "--memory") || !strings.Contains(errb, "not a size") {
			t.Fatalf("gate.py exit %d, want 2 naming the flag\n%s", code, errb)
		}
		if relIndex(w.calls(), "docker run ") >= 0 {
			t.Fatal("a container started despite the malformed limit")
		}
	})
}

// TestW405PfGateNeedsTheDockerfile: in the container — the only isolation
// a gate has — a tree without .polyforge/Dockerfile.verify is not judged:
// exit 2, the reason said, no container, never a run on the host.
func TestW405PfGateNeedsTheDockerfile(t *testing.T) {
	w := newRelWorld(t, []string{"v1.2.0"}, nil)
	relGit(t, w.work, "rm", "-q", ".polyforge/Dockerfile.verify")
	relGit(t, w.work, "commit", "-q", "-m", "no Dockerfile")
	out, errb, code := w405RunGate(t, w, nil)
	if code != 2 || !strings.Contains(out, "carries no .polyforge/Dockerfile.verify") || !strings.Contains(out, "never on the host") {
		t.Fatalf("gate.py exit %d\n%s\n%s", code, out, errb)
	}
	if relIndex(w.calls(), "docker run ") >= 0 || relIndex(w.calls(), "docker exec ") >= 0 {
		t.Fatalf("the gate ran without its Dockerfile: %v", w.calls())
	}
	if _, errb, code := w405RunGate(t, w, nil, "--isolation", "host"); code != 2 || !strings.Contains(errb, "never on the host") {
		t.Fatalf("--isolation host: exit %d\n%s", code, errb)
	}
}
