package workspace

// params_test.go (polyforge#78 AC2): the project layer of a step's
// params — `steps.<project>.<step-id>.params: { <name>: <value> }`. The
// leaf loads beside models/skip/by into Config.Params; a param name of
// the wrong shape, a value of more than one line or over the bound, a
// non-string value and a params map on the bundle-less keys are refused
// naming the yaml path. The project value governs only a step whose lane
// declares that param (a key is shared by step id across lanes, owner
// ruling 6): ComposeStepParams composes lane default → project, and
// StepParamSources labels each value's level the way the model chain
// does. A param no lane of the set declares on that step is stale
// (StaleKeys, which pf doctor reports).

import (
	"reflect"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/workflow"
	"github.com/silgrid/polyforge/internal/common/workflow/workflowtest"
)

func TestW78WorkspaceParamsParse(t *testing.T) {
	body := "steps:\n" +
		"  p:\n" +
		"    fix:\n" +
		"      params: { image: \"reg/img:1\", aux: \"\" }\n" +
		"    code: { models: [pi:c], params: { image: x } }\n" +
		"    spec: { skip: true, params: { image: y } }\n"
	cfg, err := Load(writeYaml(t, body), nil)
	if err != nil || cfg == nil {
		t.Fatalf("a step entry's params loads: cfg %+v, err %v", cfg, err)
	}
	want := map[string]map[string]map[string]string{"p": {
		"fix":  {"image": "reg/img:1", "aux": ""},
		"code": {"image": "x"},
		"spec": {"image": "y"},
	}}
	if !reflect.DeepEqual(cfg.Params, want) {
		t.Fatalf("cfg.Params = %v, want %v", cfg.Params, want)
	}
	if _, ok := cfg.Models["p"]["fix"]; ok {
		t.Fatalf("a params-only entry declares no model chain: %v", cfg.Models)
	}
	if !reflect.DeepEqual(cfg.Models["p"]["code"], []string{"pi:c"}) || !cfg.Skip["p"]["spec"] {
		t.Fatalf("params sits beside models and skip: models %v skip %v", cfg.Models, cfg.Skip)
	}

	// the value bound is 256 bytes: exactly 256 loads
	at := strings.Repeat("a", workflow.MaxParamValueBytes)
	if cfg, err := Load(writeYaml(t, "steps:\n  p:\n    fix: { params: { image: \""+at+"\" } }\n"), nil); err != nil || cfg.Params["p"]["fix"]["image"] != at {
		t.Fatalf("a 256-byte value loads: %v", err)
	}

	for name, c := range map[string]struct{ body, path, why string }{
		"name shape":      {"steps:\n  p:\n    fix: { params: { Image: x } }\n", "steps.p.fix.params.Image", "^[a-z][a-z0-9_]{0,63}$"},
		"name too long":   {"steps:\n  p:\n    fix: { params: { " + "a" + strings.Repeat("b", 64) + ": x } }\n", "steps.p.fix.params.a" + strings.Repeat("b", 64), "^[a-z][a-z0-9_]{0,63}$"},
		"newline":         {"steps:\n  p:\n    fix: { params: { image: \"a\\nb\" } }\n", "steps.p.fix.params.image", "one line"},
		"carriage return": {"steps:\n  p:\n    fix: { params: { image: \"a\\rb\" } }\n", "steps.p.fix.params.image", "one line"},
		"over the bound":  {"steps:\n  p:\n    fix: { params: { image: \"" + at + "b\" } }\n", "steps.p.fix.params.image", "at most 256 bytes"},
		"not a string":    {"steps:\n  p:\n    fix: { params: { image: 3 } }\n", "steps.p.fix.params.image", "string"},
		"not a mapping":   {"steps:\n  p:\n    fix: { params: image }\n", "steps.p.fix.params", "mapping"},
		"empty":           {"steps:\n  p:\n    fix: { params: {} }\n", "steps.p.fix.params", "empty"},
		"on default":      {"steps:\n  p:\n    default: { params: { image: x } }\n", "steps.p.default.params", "is no step"},
		"on task":         {"steps:\n  p:\n    task: { params: { image: x } }\n", "steps.p.task.params", "is no step"},
	} {
		_, err := Load(writeYaml(t, c.body), nil)
		if err == nil || !strings.Contains(err.Error(), c.path) || !strings.Contains(err.Error(), c.why) {
			t.Errorf("%s: refused naming %s and %q: %v", name, c.path, c.why, err)
		}
	}
}

func TestW78ComposeStepParams(t *testing.T) {
	lane := &workflow.Lane{Name: "l", Steps: []workflow.Step{
		{ID: "fix", Params: map[string]string{"image": "base", "aux": ""}},
		{ID: "code"},
		{ID: "review", Params: map[string]string{"level": "1"}},
	}}
	// project p sets the declared image of fix; code declares no params, so
	// its image governs nothing; fix's ghost is declared by no step here.
	// project r sets fix's image to the empty string (a present value).
	cfg, err := Load(writeYaml(t, "steps:\n"+
		"  p:\n    fix: { params: { image: proj, ghost: g } }\n    code: { params: { image: leak } }\n"+
		"  r:\n    fix: { params: { image: \"\" } }\n"), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		project string
		cfg     *Config
		want    map[string]map[string]string
	}{
		{"p", cfg, map[string]map[string]string{"fix": {"image": "proj", "aux": ""}, "review": {"level": "1"}}},
		{"r", cfg, map[string]map[string]string{"fix": {"image": "", "aux": ""}, "review": {"level": "1"}}},
		{"q", cfg, map[string]map[string]string{"fix": {"image": "base", "aux": ""}, "review": {"level": "1"}}},
		{"p", nil, map[string]map[string]string{"fix": {"image": "base", "aux": ""}, "review": {"level": "1"}}},
	} {
		if got := ComposeStepParams(lane, c.cfg, c.project); !reflect.DeepEqual(got, c.want) {
			t.Errorf("ComposeStepParams(%s, cfg nil=%v) = %v, want %v", c.project, c.cfg == nil, got, c.want)
		}
	}

	// the source of each value: the model chain's label shapes
	got := StepParamSources("l", lane, cfg, "p")
	want := map[string]map[string]StepParamSource{
		"fix": {
			"image": {Value: "proj", Source: "steps.p.fix.params.image"},
			"aux":   {Value: "", Source: "bundle l step fix params.aux"},
		},
		"review": {"level": {Value: "1", Source: "bundle l step review params.level"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("StepParamSources = %v, want %v", got, want)
	}
}

func TestW78StaleParamKeys(t *testing.T) {
	dir := t.TempDir()
	workflowtest.WriteLane(t, dir, "b", "steps:\n  - id: fix\n    params: { image: base }\n  - id: code\n")
	cfg, err := Load(writeYaml(t, "steps:\n  p:\n"+
		"    fix: { params: { image: x, ghost: y } }\n"+
		"    code: { models: [pi:c], params: { image: z } }\n"+
		"    nostep: { params: { image: w } }\n"), nil)
	if err != nil {
		t.Fatal(err)
	}
	stale, err := StaleKeys(cfg, workflow.ForWorkspace(dir, writeYaml(t, "")))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"steps.p.code.params.image", "steps.p.fix.params.ghost", "steps.p.nostep"}
	if !reflect.DeepEqual(stale, want) {
		t.Fatalf("StaleKeys = %v, want %v (a param no lane declares on the step, and a step no lane carries)", stale, want)
	}
}
