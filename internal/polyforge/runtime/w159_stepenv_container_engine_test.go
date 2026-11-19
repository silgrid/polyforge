package runtime

// w159_stepenv_container_engine_test.go (polyforge#159, 84-engine): the
// daemon-resolved [daemon] container_engine reaches every step process
// as PF_CONTAINER_ENGINE, on both harnesses — the one value the step's
// scripts pick their container backend by. The entry rides the step env
// exactly when the daemon stated one: a bare Config adds none (the
// scripts' own default, docker, applies to an unset variable — an empty
// value would be refused by the selection, so it is never injected), and
// a step's inherited copy never leaks in (the sanitize point strips it).

import (
	"slices"
	"testing"

	"github.com/silgrid/polyforge/internal/common/spawn"
)

// TestStepEnvCarriesContainerEngine: a stated engine is one more
// harness-neutral entry; an unstated one adds none (the existing env's
// shape is unchanged — the docker default lives in the scripts).
func TestStepEnvCarriesContainerEngine(t *testing.T) {
	e := StepEnv{Python: "/usr/bin/python3", ContainerEngine: "podman"}
	if got := e.Vars(); !slices.Contains(got, spawn.EnvContainerEngine+"=podman") {
		t.Fatalf("a stated engine must reach the step process: %q", got)
	}
	e.ContainerEngine = ""
	if got := e.Vars(); slices.Contains(got, spawn.EnvContainerEngine+"=") {
		t.Fatalf("an unstated engine sets no variable (the scripts default to docker): %q", got)
	}
	if got := e.Vars(); len(got) != 1 || got[0] != spawn.EnvPython+"=/usr/bin/python3" {
		t.Fatalf("the bare env's shape is unchanged: %q", got)
	}
}

// TestContainerEngineIsInjectedOnBothHarnesses: both harness adapters
// carry the stated engine — and an inherited copy never leaks in.
func TestContainerEngineIsInjectedOnBothHarnesses(t *testing.T) {
	clearBashTimeoutEnv(t)
	t.Setenv(spawn.EnvContainerEngine, "docker") // an inherited copy: stripped, then re-set
	cfg := &Config{StepEnv: func() StepEnv {
		return StepEnv{CommandPath: "/opt/pf/bin:/usr/bin", PfDir: "/srv/pf", Python: "/usr/bin/python3.12",
			Cell: "public", CellServer: "http://srv.example:8080", DeployEnv: "/cfg/deploy-public.env",
			ContainerEngine: "podman"}
	}}
	lane := "/c/workflows/develop"
	pi := newPiSpawner(cfg).buildCmd(SpawnParams{CapsuleDir: "/c", LaneDir: lane, SpawnN: 1, Round: 3})
	if got := envEntries(pi.Env, spawn.EnvContainerEngine); !slices.Equal(got, []string{"podman"}) {
		t.Fatalf("pi: %s = %q, want [podman] (the daemon's answer, never the inherited copy)", spawn.EnvContainerEngine, got)
	}
	cl, err := (&claudeSpawner{cfg: cfg}).buildCmd(SpawnParams{CapsuleDir: "/c", HarnessSession: testSession, LaneDir: lane, SpawnN: 1, Round: 3})
	if err != nil {
		t.Fatal(err)
	}
	if got := envEntries(cl.Env, spawn.EnvContainerEngine); !slices.Equal(got, []string{"podman"}) {
		t.Fatalf("claude: %s = %q, want [podman]", spawn.EnvContainerEngine, got)
	}
}
