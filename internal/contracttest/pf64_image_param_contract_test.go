// carries: workflows/develop/workflow.yaml, workflows/integrate/workflow.yaml, workflows/_shared/skills/verify.md, workflows/integrate/skills/full-gate.md, internal/contracttest/pf64_image_param_contract_test.go
package test

// pf64_image_param_contract_test.go (polyforge#64): a text contract. The
// verification step (develop) and the full-gate step (integrate) declare
// the `image` parameter, default empty — an empty value keeps each repo's
// own .polyforge/Dockerfile.verify; a set value names a prepared image, a
// person's, that every touched repo's bar (verification) and every gated
// repo (full-gate) then runs in, Dockerfile or not. A machine without the
// image answers "not judged" (exit 2), and the skills route on the fixed
// mark: a summary carrying pf-verify-image-missing hands off blocked at
// once, never a retry — a person prepares the image or clears the
// parameter.
//
// The acceptance (AC1's declaration, AC3's skill routing) is the shipped
// yaml and skill text, so both are pinned here: the lanes' steps really
// parse with a params map whose image default is the empty string (read
// through the yaml, not a substring — a params key of another name must
// not satisfy it), and both skills carry the mark and the no-retry route.
//
// Red on the base tree: neither lane declares the parameter and neither
// skill names the mark — the first yaml assertion fails.
//
// Run (targeted): GOWORK=off go test ./internal/contracttest/ -run '^TestPF64ImageParamLanesAndSkills$' -count=1 -v

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// pf64StepParams reads one lane's steps and answers the named step's
// params (nil when the lane carries no such step).
func pf64StepParams(t *testing.T, rel, step string) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(v1Root(t), filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Steps []struct {
			Id     string            `yaml:"id"`
			Params map[string]string `yaml:"params"`
		} `yaml:"steps"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("%s does not parse: %v", rel, err)
	}
	for _, s := range doc.Steps {
		if s.Id == step {
			return s.Params
		}
	}
	t.Fatalf("%s: no step %s", rel, step)
	return nil
}

func TestPF64ImageParamLanesAndSkills(t *testing.T) {
	t.Run("the lanes declare image, default empty", func(t *testing.T) {
		for _, lane := range []struct{ name, rel, step string }{
			{"develop", "workflows/develop/workflow.yaml", "verification"},
			{"integrate", "workflows/integrate/workflow.yaml", "full-gate"},
		} {
			t.Run(lane.step+" of "+lane.name, func(t *testing.T) {
				params := pf64StepParams(t, lane.rel, lane.step)
				v, ok := params["image"]
				if !ok {
					t.Errorf("%s: the %s step declares no params.image — the lane default must be there, empty",
						lane.rel, lane.step)
					return
				}
				if v != "" {
					t.Errorf("%s: the %s step's params.image default is %q, not the empty string",
						lane.rel, lane.step, v)
				}
			})
		}
	})
	t.Run("the skills route on the fixed mark, blocked at once", func(t *testing.T) {
		for _, rel := range []string{
			"workflows/_shared/skills/verify.md",
			"workflows/integrate/skills/full-gate.md",
		} {
			flat := w68Flat(t, rel)
			for _, marker := range []string{
				"pf-verify-image-missing",
				"hand off `blocked` at once, no retry",
			} {
				if !strings.Contains(flat, marker) {
					t.Errorf("%s must carry the missing-image route's marker %q", rel, marker)
				}
			}
		}
	})
	t.Run("the verify skill names the prepared image's reach", func(t *testing.T) {
		flat := w68Flat(t, "workflows/_shared/skills/verify.md")
		for _, marker := range []string{
			"the step's `image` parameter",
			"every touched repo's bar then runs in, Dockerfile or not",
		} {
			if !strings.Contains(flat, marker) {
				t.Errorf("workflows/_shared/skills/verify.md must say the image parameter boxes every touched repo: %q", marker)
			}
		}
	})
}
