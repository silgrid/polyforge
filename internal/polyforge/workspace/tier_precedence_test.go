package workspace

// tier_precedence_test.go (polyforge#81 u3, AC3): the dispatch-time
// precedence with the per-ticket overlay tier — override (an explicit
// chain, else the catalog-expanded tier) > pf submit --models > yaml
// steps.<project>.<step>.models > yaml tiers.<project>.<tier>.models >
// the lane step's model_candidates / the lane tier > yaml
// steps.<project>.default.models > the lane default. Every level carries
// a distinct marker and the cases remove the highest one at a time, so a
// composition that concatenates rather than replaces, or that lets a
// lower level leak through, is red. A missing or empty tier falls to the
// next layer and is answered as a diagnostic; `default` means unset and
// expands nothing (no diagnostic). Expected lists come from AC3's layer
// order, not from the implementation's output.

import (
	"reflect"
	"testing"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/workflow"
)

func tierPrecedenceLane() *workflow.Lane {
	return &workflow.Lane{
		Defaults: &workflow.Defaults{
			ModelCandidates: []string{"pi:prov/lane-default"},
			Tiers:           map[string][]string{"lanecheap": {"pi:prov/lane-cheap"}},
		},
		Steps: []workflow.Step{
			{ID: "pl", ModelCandidates: []string{"pi:prov/lane-step"}},
			{ID: "pt", ModelTier: "lanecheap"},
			{ID: "yt", ModelTier: "yamlcheap"},
			{ID: "def"},
		},
	}
}

func tierPrecedenceCfg() *Config {
	return &Config{
		Models: map[string]map[string][]string{"p": {"default": {"pi:prov/yaml-default"}, "pl": {"pi:prov/yaml-step"}}},
		Tiers:  map[string]map[string][]string{"p": {"yamlcheap": {"pi:prov/yaml-tier"}}},
	}
}

func TestTierPrecedenceDispatch(t *testing.T) {
	mainCatalog := map[string][]string{"main": {"pi:prov/override", "claude:override"}}
	cases := []struct {
		name    string
		cfg     *Config
		manual  []string
		ov      capsule.StepOverrides
		catalog map[string][]string
		want    map[string][]string
	}{
		{
			name: "no yaml, no manual, no overlay: lane step > lane tier > lane default",
			want: map[string][]string{"pl": {"pi:prov/lane-step"}, "pt": {"pi:prov/lane-cheap"}, "yt": {"pi:prov/lane-default"}, "def": {"pi:prov/lane-default"}},
		},
		{
			name: "the yaml default outranks the lane default; the lane tier still outranks it",
			cfg:  tierPrecedenceCfg(),
			want: map[string][]string{"pl": {"pi:prov/yaml-step"}, "pt": {"pi:prov/lane-cheap"}, "yt": {"pi:prov/yaml-tier"}, "def": {"pi:prov/yaml-default"}},
		},
		{
			name:   "--models outranks every yaml and lane level",
			cfg:    tierPrecedenceCfg(),
			manual: []string{"pi:prov/manual"},
			want:   map[string][]string{"pl": {"pi:prov/manual"}, "pt": {"pi:prov/manual"}, "yt": {"pi:prov/manual"}, "def": {"pi:prov/manual"}},
		},
		{
			name:    "the overlay tier outranks --models and every yaml level",
			cfg:     tierPrecedenceCfg(),
			manual:  []string{"pi:prov/manual"},
			ov:      capsule.StepOverrides{Steps: map[string]*capsule.StepOverride{"pt": {Tier: "main"}}},
			catalog: mainCatalog,
			want:    map[string][]string{"pl": {"pi:prov/manual"}, "pt": {"pi:prov/override", "claude:override"}, "yt": {"pi:prov/manual"}, "def": {"pi:prov/manual"}},
		},
		{
			name:    "an explicit overlay chain outranks the overlay tier",
			cfg:     tierPrecedenceCfg(),
			ov:      capsule.StepOverrides{Steps: map[string]*capsule.StepOverride{"pt": {Models: []string{"pi:prov/chain"}, Tier: "main"}}},
			catalog: mainCatalog,
			want:    map[string][]string{"pl": {"pi:prov/yaml-step"}, "pt": {"pi:prov/chain"}, "yt": {"pi:prov/yaml-tier"}, "def": {"pi:prov/yaml-default"}},
		},
		{
			name:    "default means unset: the layer below governs, no diagnostic",
			cfg:     tierPrecedenceCfg(),
			ov:      capsule.StepOverrides{Steps: map[string]*capsule.StepOverride{"yt": {Tier: "default"}, "def": {Tier: "default"}}},
			catalog: mainCatalog,
			want:    map[string][]string{"pl": {"pi:prov/yaml-step"}, "pt": {"pi:prov/lane-cheap"}, "yt": {"pi:prov/yaml-tier"}, "def": {"pi:prov/yaml-default"}},
		},
		{
			name:    "a missing tier falls through to the next layer",
			cfg:     tierPrecedenceCfg(),
			manual:  []string{"pi:prov/manual"},
			ov:      capsule.StepOverrides{Steps: map[string]*capsule.StepOverride{"def": {Tier: "nosuch"}}},
			catalog: mainCatalog,
			want:    map[string][]string{"pl": {"pi:prov/manual"}, "pt": {"pi:prov/manual"}, "yt": {"pi:prov/manual"}, "def": {"pi:prov/manual"}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, _ := ComposeCandidatesWithTier(tierPrecedenceLane(), c.cfg, "p", c.manual, c.ov, c.catalog)
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("compose = %v, want %v", got, c.want)
			}
		})
	}

	// delete the highest level one at a time: the same step's list moves
	// down the chain, never a concatenation.
	step := func(ov capsule.StepOverrides, manual []string, cfg *Config, catalog map[string][]string) []string {
		t.Helper()
		got, _ := ComposeCandidatesWithTier(tierPrecedenceLane(), cfg, "p", manual, ov, catalog)
		return got["def"]
	}
	ovMain := capsule.StepOverrides{Steps: map[string]*capsule.StepOverride{"def": {Tier: "main"}}}
	if got := step(ovMain, []string{"pi:prov/manual"}, tierPrecedenceCfg(), mainCatalog); !reflect.DeepEqual(got, []string{"pi:prov/override", "claude:override"}) {
		t.Fatalf("override tier: %v", got)
	}
	if got := step(capsule.StepOverrides{}, []string{"pi:prov/manual"}, tierPrecedenceCfg(), mainCatalog); !reflect.DeepEqual(got, []string{"pi:prov/manual"}) {
		t.Fatalf("after removing the overlay: %v", got)
	}
	if got := step(capsule.StepOverrides{}, nil, tierPrecedenceCfg(), mainCatalog); !reflect.DeepEqual(got, []string{"pi:prov/yaml-default"}) {
		t.Fatalf("after removing --models: %v", got)
	}
	if got := step(capsule.StepOverrides{}, nil, &Config{}, mainCatalog); !reflect.DeepEqual(got, []string{"pi:prov/lane-default"}) {
		t.Fatalf("after removing the yaml default: %v", got)
	}

	// a missing tier is one diagnostic naming the step, the tier, the
	// reason and the next layer's source; `default` and a governing tier
	// produce none.
	if _, diags := ComposeCandidatesWithTier(tierPrecedenceLane(), tierPrecedenceCfg(), "p", nil,
		capsule.StepOverrides{Steps: map[string]*capsule.StepOverride{"def": {Tier: "nosuch"}, "yt": {Tier: "default"}, "pt": {Tier: "main"}}}, mainCatalog); len(diags) != 1 ||
		diags[0].Step != "def" || diags[0].Tier != "nosuch" || diags[0].Reason != "missing" ||
		diags[0].NextSource != "steps.p.default.models" || diags[0].Scope != "composition" {
		t.Fatalf("fallbacks = %+v, want only def/nosuch/missing from steps.p.default.models (scope composition)", diags)
	}
	// an empty catalog list is "empty"; a step whose tier the catalog
	// declares and fills expands it (no fallback).
	if _, diags := ComposeCandidatesWithTier(tierPrecedenceLane(), tierPrecedenceCfg(), "p", nil,
		capsule.StepOverrides{Steps: map[string]*capsule.StepOverride{"def": {Tier: "main"}}}, map[string][]string{"main": {}}); len(diags) != 1 || diags[0].Reason != "empty" {
		t.Fatalf("an empty tier list must be answered as empty: %+v", diags)
	}
	if _, diags := ComposeCandidatesWithTier(tierPrecedenceLane(), tierPrecedenceCfg(), "p", nil,
		capsule.StepOverrides{Steps: map[string]*capsule.StepOverride{"def": {Tier: "main"}}}, mainCatalog); len(diags) != 0 {
		t.Fatalf("a governing tier produces no fallback: %+v", diags)
	}
}
