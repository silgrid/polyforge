package daemon

// w159_container_engine_host_test.go (review r1#4): the Host file's
// container_engine reaches the daemon's Config resolved — the Main→base
// leg of the injection chain, named and testable as hostContainerEngine.
// The rest of the chain is pinned by the cli (ResolveContainerEngine),
// daemon (Config → stepEnv) and runtime (both harnesses) tests.

import (
	"testing"

	"github.com/silgrid/polyforge/internal/polyforge/cli"
)

func TestContainerEngineHostResolution(t *testing.T) {
	// an unset key resolves to the documented default, docker; a stated
	// key is itself (the parse has refused every other spelling before
	// this runs — cli's TestContainerEngineValueRules pins that)
	if got := hostContainerEngine(cli.Config{}); got != "docker" {
		t.Fatalf("hostContainerEngine(empty) = %q, want the documented default docker", got)
	}
	for _, v := range []string{"docker", "podman"} {
		host := cli.Config{Daemon: cli.DaemonSettings{ContainerEngine: v}}
		if got := hostContainerEngine(host); got != v {
			t.Fatalf("hostContainerEngine(%q) = %q, want the file's value", v, got)
		}
	}
}
