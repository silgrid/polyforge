package workspace

// w36_by_key_test.go (u1): the step entry's `by` leaf — a project's
// declaration that a step's work is done by a person (the one mode this
// release knows: `human`, the merge a lane declares human_mergeable hands
// to the project's reviewer). The leaf loads beside `models` and `skip`,
// takes the one value `human` (anything else is a load refusal naming
// what it takes), never sits on the bundle-less keys (`default`, `task`),
// is refused beside a `skip` of the same entry (a step is either switched
// off or taken over, never both), and a `by` on a key no installed lane
// carries is stale like a models key (StaleKeys names it).

import (
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/workflow"
)

func TestW36StepByHumanKey(t *testing.T) {
	// the leaf loads, alone and beside models
	for name, body := range map[string]string{
		"alone":           "steps:\n  p:\n    merge: { by: human }\n",
		"beside models":   "steps:\n  p:\n    merge: { models: [pi:m], by: human }\n",
		"another step":    "steps:\n  p:\n    fix: { by: human }\n",
		"beside a skip":   "steps:\n  p:\n    merge: { by: human }\n    fix: { skip: true }\n",
		"flow-style with": "steps:\n  p: { merge: { by: human } }\n",
	} {
		cfg, err := Load(writeYaml(t, body), nil)
		if err != nil || cfg == nil {
			t.Fatalf("%s: the by leaf loads: cfg %+v, err %v", name, cfg, err)
		}
		if got := cfg.By["p"]["merge"]; name == "another step" {
			if got := cfg.By["p"]["fix"]; got != "human" || cfg.By["p"]["merge"] != "" {
				t.Fatalf("%s: cfg.By = %v, want fix=human", name, cfg.By)
			}
		} else if got != "human" {
			t.Fatalf("%s: cfg.By[p][merge] = %q, want human (cfg.By %v)", name, got, cfg.By)
		}
		if cfg.Skip != nil && name != "beside a skip" {
			t.Fatalf("%s: no skip is declared: %+v", name, cfg.Skip)
		}
	}

	// a by and a skip of the same entry are refused together
	_, err := Load(writeYaml(t, "steps:\n  p:\n    merge: { skip: true, by: human }\n"), nil)
	if err == nil || !strings.Contains(err.Error(), "steps.p.merge") || !strings.Contains(err.Error(), "both skip and by") {
		t.Fatalf("a step entry declaring both skip and by is refused naming both: %v", err)
	}

	// the leaf takes the one mode this release knows
	for name, body := range map[string]string{
		"another mode": "steps:\n  p:\n    merge: { by: lane }\n",
		"a bool":      "steps:\n  p:\n    merge: { by: true }\n",
		"empty":       "steps:\n  p:\n    merge: { by: \"\" }\n",
	} {
		_, err := Load(writeYaml(t, body), nil)
		if err == nil || !strings.Contains(err.Error(), "steps.p.merge.by") || !strings.Contains(err.Error(), "takes `human`") {
			t.Fatalf("%s: a by value other than human is refused naming what it takes: %v", name, err)
		}
	}

	// the bundle-less keys take no by, exactly as they take no skip
	for name, key := range map[string]string{"default": DefaultKey, "task": TaskKey} {
		_, err := Load(writeYaml(t, "steps:\n  p:\n    "+key+": { by: human }\n"), nil)
		if err == nil || !strings.Contains(err.Error(), "steps.p."+key+".by") || !strings.Contains(err.Error(), "is no step") {
			t.Fatalf("%s: by is not allowed on the bundle-less key: %v", name, err)
		}
	}

	// a by on a key no installed lane carries is stale, like a models key
	cfg, err := Load(writeYaml(t, "steps:\n  p:\n    merge: { by: human }\n    fix: { models: [pi:f] }\n"), nil)
	if err != nil {
		t.Fatalf("the yaml loads before the stale judgment: %v", err)
	}
	stale, err := StaleKeys(cfg, workflow.ForWorkspace(stepsDir(t), writeYaml(t, "")))
	if err != nil {
		t.Fatal(err)
	}
	if len(stale) != 1 || stale[0] != "steps.p.merge" {
		t.Fatalf("StaleKeys names the by on the step no lane carries: %v", stale)
	}
}
