package task

import (
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/polyforge/workspace"
)

func TestResolveCandidates(t *testing.T) {
	cfg := &workspace.Config{Models: map[string]map[string][]string{
		"both":    {workspace.TaskKey: {"pi:task", "claude:c"}, workspace.DefaultKey: {"pi:default"}},
		"default": {workspace.DefaultKey: {"pi:default"}},
		"neither": {workspace.TaskKey: {}},
	}}
	// The task list wholly governs, in order.
	got, from, err := ResolveCandidates(cfg, "both")
	if err != nil || from != "steps.both.task.models" || len(got) != 2 || got[0] != "pi:task" || got[1] != "claude:c" {
		t.Fatalf("both: %v from %q err %v", got, from, err)
	}
	// The default governs when the task list is absent.
	got, from, err = ResolveCandidates(cfg, "default")
	if err != nil || from != "steps.default.default.models" || len(got) != 1 || got[0] != "pi:default" {
		t.Fatalf("default: %v from %q err %v", got, from, err)
	}
	// A declared-but-empty task list governs and is refused as empty; it
	// never falls through to the default.
	if _, _, err := ResolveCandidates(cfg, "neither"); err == nil || !strings.Contains(err.Error(), "steps.neither.task.models") {
		t.Fatalf("present-empty task list must refuse naming its level: %v", err)
	}
	// Neither list: the named refusal names both keys.
	if _, _, err := ResolveCandidates(cfg, "missing"); err == nil ||
		!strings.Contains(err.Error(), "steps.missing.task.models") ||
		!strings.Contains(err.Error(), "steps.missing.default.models") {
		t.Fatalf("missing project must refuse naming both keys: %v", err)
	}
	// A no-yaml workspace is the same named refusal.
	if _, _, err := ResolveCandidates(nil, "p"); err == nil || !strings.Contains(err.Error(), "steps.p.task.models") {
		t.Fatalf("nil config must refuse naming the key: %v", err)
	}
}
