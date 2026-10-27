package runtime

// task_models_test.go: the live task-lane candidate read (workbench#40).
// The accessor reads the workspace yaml at call time (no snapshot), so a
// rewrite governs the next read.

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/polyforge/workspace"
)

func writeTaskYaml(t *testing.T, r *Runtime, yaml string) {
	t.Helper()
	ws := testWorkspace(r)
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, workspace.FileName), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestTaskModelsReadsLiveYaml(t *testing.T) {
	r, _, _ := newTestRuntime(t, newFakeHub(), &scriptedSpawner{})

	writeTaskYaml(t, r, "steps:\n  proj:\n    task: { models: [pi:t1] }\n    default: { models: [pi:d1] }\n")
	got, from, err := r.TaskModels("proj")
	if err != nil || !reflect.DeepEqual(got, []string{"pi:t1"}) || from != "steps.proj.task.models" {
		t.Fatalf("task list: %v from %q err %v", got, from, err)
	}

	// An edit governs the next read: the default takes over once the task
	// list is gone.
	writeTaskYaml(t, r, "steps:\n  proj:\n    default: { models: [pi:d2] }\n")
	got, from, err = r.TaskModels("proj")
	if err != nil || !reflect.DeepEqual(got, []string{"pi:d2"}) || from != "steps.proj.default.models" {
		t.Fatalf("default fallback: %v from %q err %v", got, from, err)
	}

	// Neither list: the named refusal.
	writeTaskYaml(t, r, "steps:\n  proj:\n    code: { models: [pi:c] }\n")
	_, _, err = r.TaskModels("proj")
	if err == nil || !strings.Contains(err.Error(), "steps.proj.task.models") || !strings.Contains(err.Error(), "steps.proj.default.models") {
		t.Fatalf("neither list must refuse naming both keys; got %v", err)
	}

	// An invalid live yaml is the shared loader's typed refusal.
	writeTaskYaml(t, r, "steps:\n  proj:\n    task: { models: [pi:t], max_tokens: 5 }\n")
	_, _, err = r.TaskModels("proj")
	if err == nil || !strings.Contains(err.Error(), "max_tokens") {
		t.Fatalf("invalid yaml must refuse naming the key; got %v", err)
	}
}
