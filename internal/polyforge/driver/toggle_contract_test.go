package driver

import (
	"strings"
	"testing"
)

func TestPiCapabilityChangeOnlyTargetsPi(t *testing.T) {
	if got := piCapabilityChange(map[string]string{"pi_subagents": "on"}, "pi"); !strings.Contains(got, `agent: "worker"`) {
		t.Fatalf("pi enable reminder missing worker dispatch: %q", got)
	}
	if got := piCapabilityChange(map[string]string{"pi_subagents": "on"}, "claude"); got != "" {
		t.Fatalf("claude must not receive pi capability reminder: %q", got)
	}
}
