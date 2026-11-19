package daemon

// w159_stepenv_container_engine_test.go (polyforge#159, 84-engine): the
// daemon's Config carries the resolved [daemon] container_engine and the
// step env hands it to every step process as PF_CONTAINER_ENGINE; a
// Config that states none sets no variable (the scripts' own default,
// docker).

import (
	"slices"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/spawn"
)

func TestContainerEngineReachesTheStepEnv(t *testing.T) {
	td := newTestDaemon(t, func(c *Config) {
		c.ContainerEngine = "podman"
	})
	env := td.Daemon.stepEnv()
	if env.ContainerEngine != "podman" {
		t.Fatalf("the step env carries the resolved engine: %+v", env)
	}
	if !slices.Contains(env.Vars(), spawn.EnvContainerEngine+"=podman") {
		t.Fatalf("the engine reaches the step process as %s: %q", spawn.EnvContainerEngine, env.Vars())
	}
}

func TestStepEnvWithoutAStatedEngineSetsNoVariable(t *testing.T) {
	td := newTestDaemon(t, nil)
	env := td.Daemon.stepEnv()
	if env.ContainerEngine != "" {
		t.Fatalf("a bare Config states no engine: %+v", env)
	}
	// the entries are "<name>=<value>": a whole-element test on the bare
	// name would never match, so the guard is by prefix (r1#5)
	if slices.ContainsFunc(env.Vars(), func(kv string) bool { return strings.HasPrefix(kv, spawn.EnvContainerEngine+"=") }) {
		t.Fatalf("no variable is set for an unstated engine (the scripts default to docker): %q", env.Vars())
	}
}
