package workspace

// task_models_test.go: the bundle-less `task` step id —
// recognized by the strict loader, models-only, and the pure
// task -> default candidate resolution.

import (
	"reflect"
	"strings"
	"testing"
)

// The task id is a recognized step id even though no installed bundle
// declares it, and its only key is models.
func TestLoadTaskKeyIsRecognized(t *testing.T) {
	cfg, err := Load(writeYaml(t, "steps:\n  e2e:\n    task: { models: [pi:a, claude:b] }\n"), stepsDir(t), nil)
	if err != nil || cfg == nil {
		t.Fatalf("steps.<p>.task must load: %v", err)
	}
	if got := cfg.Models["e2e"][TaskKey]; !reflect.DeepEqual(got, []string{"pi:a", "claude:b"}) {
		t.Fatalf("task list decoded wrong: %#v", got)
	}
}

// A budget key under steps.<p>.task is an unknown key, exactly like any
// other key a step entry does not declare.
func TestLoadTaskKeyRefusesNonModelKeys(t *testing.T) {
	for name, entry := range map[string]string{
		"max_tokens":   "task: { models: [pi:a], max_tokens: 5 }",
		"max_duration": "task: { models: [pi:a], max_duration: 2h }",
	} {
		_, err := Load(writeYaml(t, "steps:\n  e2e:\n    "+entry+"\n"), stepsDir(t), nil)
		if err == nil {
			t.Fatalf("%s: a non-models key under steps.e2e.task must be refused", name)
		}
		if !strings.Contains(err.Error(), "steps.e2e.task.") {
			t.Fatalf("%s: refusal must name the offending key; got %v", name, err)
		}
	}
}

// The task list wins the default; the default governs when the task list
// is absent; neither answers nil.
func TestTaskModelsPrecedence(t *testing.T) {
	cfg := &Config{Models: map[string]map[string][]string{
		"both":    {TaskKey: {"pi:task"}, DefaultKey: {"pi:default"}},
		"default": {DefaultKey: {"pi:default"}},
		"neither": {"code": {"pi:code"}},
	}}
	if got, from := TaskModels(cfg, "both"); !reflect.DeepEqual(got, []string{"pi:task"}) || from != "steps.both.task.models" {
		t.Fatalf("both: %v from %q; want the task list", got, from)
	}
	if got, from := TaskModels(cfg, "default"); !reflect.DeepEqual(got, []string{"pi:default"}) || from != "steps.default.default.models" {
		t.Fatalf("default: %v from %q; want the default list", got, from)
	}
	if got, from := TaskModels(cfg, "neither"); got != nil || from != "" {
		t.Fatalf("neither: %v from %q; want nil, \"\"", got, from)
	}
	// A declared-but-empty task list is a PRESENT level: it governs, and
	// it does not fall through to the default.
	empty := &Config{Models: map[string]map[string][]string{"p": {TaskKey: {}, DefaultKey: {"pi:default"}}}}
	if got, from := TaskModels(empty, "p"); got == nil || len(got) != 0 || from != "steps.p.task.models" {
		t.Fatalf("present-empty task list must govern: %#v from %q", got, from)
	}
	if got, from := TaskModels(nil, "p"); got != nil || from != "" {
		t.Fatalf("nil config: %v from %q", got, from)
	}
}

// The neither-list refusal names both keys and carries no hardcoded model.
func TestMissingTaskModelsErrorNamesBothKeys(t *testing.T) {
	err := MissingTaskModelsError("demo")
	if err == nil {
		t.Fatal("want the named refusal")
	}
	for _, want := range []string{"demo", "steps.demo.task.models", "steps.demo.default.models"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal must name %q; got %v", want, err)
		}
	}
}
