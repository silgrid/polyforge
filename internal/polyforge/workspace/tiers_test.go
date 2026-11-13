package workspace

// tiers_test.go: the tier→model table (workflow contract 2). A step that
// names a `model_tier` resolves, highest first: the yaml step key
// (steps.<project>.<step>.models) > the yaml tier list
// (tiers.<project>.<tier>.models) > the lane's own tier list
// (defaults.tiers) > the yaml project default > the lane defaults; a lane
// tier list that is placeholders only (`<harness>:example-…`, the shipped
// lanes' tiers) is undeclared and falls through. The `tiers:` section's
// accepted and refused shapes.

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/workflow"
)

// tierLane declares a real cheap tier, a placeholder mid tier (the
// shipped lanes' shape) and the lane defaults; step a names cheap, b
// names mid, c names none.
func tierLane() *workflow.Lane {
	return &workflow.Lane{
		Defaults: &workflow.Defaults{
			ModelCandidates: []string{"pi:prov/lane-default"},
			Tiers:           map[string][]string{"cheap": {"pi:prov/lane-cheap"}, "mid": {"pi:example-gateway/model-mid"}},
		},
		Steps: []workflow.Step{{ID: "a", ModelTier: "cheap"}, {ID: "b", ModelTier: "mid"}, {ID: "c"}},
	}
}

func TestTierPrecedence(t *testing.T) {
	cases := []struct {
		name string
		cfg  *Config
		want map[string][]string // step → first candidate's list
		from map[string]string   // step → the level's label
	}{
		{
			name: "no yaml: the lane tier; a placeholder tier falls through to the lane defaults",
			want: map[string][]string{"a": {"pi:prov/lane-cheap"}, "b": {"pi:prov/lane-default"}, "c": {"pi:prov/lane-default"}},
			from: map[string]string{"a": "defaults.tiers", "b": "defaults.model_candidates"},
		},
		{
			name: "the project default ranks below the lane tier, above a placeholder tier",
			cfg:  &Config{Models: map[string]map[string][]string{"p": {DefaultKey: {"pi:ws/default"}}}},
			want: map[string][]string{"a": {"pi:prov/lane-cheap"}, "b": {"pi:ws/default"}, "c": {"pi:ws/default"}},
			from: map[string]string{"a": "defaults.tiers", "b": "steps.p.default.models"},
		},
		{
			name: "the yaml tier list outranks the lane tier and the project default",
			cfg: &Config{
				Models: map[string]map[string][]string{"p": {DefaultKey: {"pi:ws/default"}}},
				Tiers:  map[string]map[string][]string{"p": {"cheap": {"pi:ws/glm-cheap"}, "mid": {"pi:ws/glm-mid"}}},
			},
			want: map[string][]string{"a": {"pi:ws/glm-cheap"}, "b": {"pi:ws/glm-mid"}, "c": {"pi:ws/default"}},
			from: map[string]string{"a": "tiers.p.cheap.models", "b": "tiers.p.mid.models", "c": "steps.p.default.models"},
		},
		{
			name: "the yaml step key outranks the yaml tier list",
			cfg: &Config{
				Models: map[string]map[string][]string{"p": {"a": {"pi:ws/a-own"}}},
				Tiers:  map[string]map[string][]string{"p": {"cheap": {"pi:ws/glm-cheap"}}},
			},
			want: map[string][]string{"a": {"pi:ws/a-own"}, "b": {"pi:prov/lane-default"}, "c": {"pi:prov/lane-default"}},
			from: map[string]string{"a": "steps.p.a.models"},
		},
		{
			name: "another project's tiers govern nothing here",
			cfg:  &Config{Tiers: map[string]map[string][]string{"other": {"cheap": {"pi:ws/glm-cheap"}}}},
			want: map[string][]string{"a": {"pi:prov/lane-cheap"}, "b": {"pi:prov/lane-default"}, "c": {"pi:prov/lane-default"}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ComposeCandidates(tierLane(), c.cfg, "p", nil); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("compose = %v, want %v", got, c.want)
			}
			for _, r := range ResolveSteps("lane", tierLane(), c.cfg, "p", nil) {
				if want, ok := c.from[r.Step]; ok && !strings.Contains(r.ModelsFrom, want) {
					t.Errorf("step %s resolved from %q, want the level %q", r.Step, r.ModelsFrom, want)
				}
			}
		})
	}
	// the manual list still governs every step
	if got := ComposeCandidates(tierLane(), &Config{Tiers: map[string]map[string][]string{"p": {"cheap": {"pi:ws/glm-cheap"}}}}, "p", []string{"pi:m/manual"}); got["a"][0] != "pi:m/manual" {
		t.Fatalf("pf submit --models outranks the tiers: %v", got)
	}
}

func TestTiersSection(t *testing.T) {
	ws := writeYaml(t, "tiers:\n  e2e:\n    cheap: { models: [\"pi:prov/glm\"] }\n    mid: { models: [] }\n")
	cfg, err := Load(ws, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg == nil || !reflect.DeepEqual(cfg.Tiers["e2e"]["cheap"], []string{"pi:prov/glm"}) || cfg.Tiers["e2e"]["mid"] == nil || len(cfg.Tiers["e2e"]["mid"]) != 0 {
		t.Fatalf("a tiers-only file governs its tier lists (a declared-empty one too): %+v", cfg)
	}
	for _, c := range []struct{ name, yaml, needle string }{
		{"not a mapping", "tiers: [a]\n", "tiers: must be a mapping"},
		{"empty project entry", "tiers:\n  e2e:\n", "tiers.e2e: entry is empty"},
		{"a bare list", "tiers:\n  e2e:\n    cheap: [pi:a]\n", "tiers.e2e.cheap: must be `{ models: [...] }`"},
		{"another key", "tiers:\n  e2e:\n    cheap: { models: [pi:a], attrs: { x: 1 } }\n", "tiers.e2e.cheap.attrs: not allowed"},
		{"no models", "tiers:\n  e2e:\n    cheap: {}\n", "tiers.e2e.cheap: entry is empty"},
		{"an unprefixed candidate", "tiers:\n  e2e:\n    cheap: { models: [glm] }\n", "tiers.e2e.cheap.models[0]"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, err := Load(writeYaml(t, c.yaml), nil); err == nil || !strings.Contains(err.Error(), c.needle) {
				t.Fatalf("want a refusal naming %q, got %v", c.needle, err)
			}
		})
	}
	if _, err := Load(writeYaml(t, "tiers:\n  elsewhere:\n    cheap: { models: [pi:a] }\n"), []string{"e2e"}); err == nil || !strings.Contains(err.Error(), "elsewhere") {
		t.Fatalf("a project the workspace does not map is refused: %v", err)
	}
}

// The shape (the develop lane's `sync` step as it shipped
// before contract 2: a step list of placeholders only) never outranks the
// project default — a placeholder-only bundle list is undeclared, so the
// yaml default with no `sync` key governs; a list mixing a real candidate
// stays declared (red before: the shipped placeholder won and the step
// ran on a model no gateway serves).
func TestPlaceholderStepListIsUndeclared(t *testing.T) {
	lane := &workflow.Lane{
		Defaults: &workflow.Defaults{ModelCandidates: []string{"pi:example-gateway/model-a"}},
		Steps: []workflow.Step{
			{ID: "sync", ModelCandidates: []string{"pi:example-claude/claude-opus-5", "claude:example-x"}},
			{ID: "mixed", ModelCandidates: []string{"pi:example-claude/claude-opus-5", "pi:prov/real"}},
		},
	}
	cfg := &Config{Models: map[string]map[string][]string{"polyforge": {DefaultKey: {"pi:prov/project-default"}}}}
	got := ComposeCandidates(lane, cfg, "polyforge", nil)
	if first := got["sync"][0]; first != "pi:prov/project-default" {
		t.Fatalf("SYMPTOM: the placeholder-only sync list outranked the project default: first candidate %q", first)
	}
	if got["mixed"][1] != "pi:prov/real" || got["mixed"][0] != "pi:example-claude/claude-opus-5" {
		t.Fatalf("a list with a real candidate stays declared: %v", got["mixed"])
	}
	for _, r := range ResolveSteps("develop", lane, cfg, "polyforge", nil) {
		if r.Step == "sync" && r.ModelsFrom != "steps.polyforge.default.models" {
			t.Fatalf("sync resolves from the project default: %q", r.ModelsFrom)
		}
	}
	if !IsPlaceholder("pi:example-gateway/model-a") || !IsPlaceholder("claude:example-x") || IsPlaceholder("pi:prov/example-not-a-prefix") || IsPlaceholder("nope") {
		t.Fatal("IsPlaceholder: `<harness>:example-…` only")
	}
}

// Every shipped lane declares the cheap and mid tiers as placeholders
// (the lane migrations name them; the gateway model is the workspace's):
// a step naming either resolves to the workspace's tier list when the
// yaml declares one, else falls through to the project default.
func TestShippedLanesCarryPlaceholderTiers(t *testing.T) {
	for _, name := range []string{"develop", "micro", "integrate", "release"} {
		lane, err := workflow.LoadStrict(filepath.Join(shippedWorkflows, name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if lane.Defaults == nil || len(lane.Defaults.Tiers) != 2 {
			t.Fatalf("%s declares the cheap and mid tiers: %+v", name, lane.Defaults)
		}
		for _, tier := range []string{"cheap", "mid"} {
			if list := lane.Defaults.Tiers[tier]; !placeholdersOnly(list) {
				t.Errorf("%s tier %s names a real model in a shipped lane: %v", name, tier, list)
			}
			probe := &workflow.Lane{Defaults: lane.Defaults, Steps: []workflow.Step{{ID: "x", ModelTier: tier}}}
			cfg := &Config{Models: map[string]map[string][]string{"p": {DefaultKey: {"pi:ws/default"}}}}
			if got := ComposeCandidates(probe, cfg, "p", nil)["x"]; len(got) != 1 || got[0] != "pi:ws/default" {
				t.Errorf("%s tier %s: a placeholder tier falls through to the project default, got %v", name, tier, got)
			}
			cfg.Tiers = map[string]map[string][]string{"p": {tier: {"pi:ws/" + tier}}}
			if got := ComposeCandidates(probe, cfg, "p", nil)["x"]; len(got) != 1 || got[0] != "pi:ws/"+tier {
				t.Errorf("%s tier %s: the workspace's tier list governs, got %v", name, tier, got)
			}
		}
	}
}
