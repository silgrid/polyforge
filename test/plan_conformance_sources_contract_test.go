package test

// plan_conformance_sources_contract_test.go: the build steps of the lanes
// with a plan step declare the {path, source, ref} citation with its
// three sources, and its withdrawal mirror.

import (
	"strings"
	"testing"
)

func TestPlanConformanceSourcesContract(t *testing.T) {
	for _, p := range []string{"workflows/feature-full/steps/code.md", "workflows/chore/steps/code.md"} {
		text := w68Flat(t, p)
		for _, want := range []string{
			"declared_extensions:", "withdrawn_obligations:", "required: [path, source, ref]",
			"source: { type: string, enum: [review, verification, deliver] }",
			"the answer declares the file outright", "an uncited withdrawal stays blocking",
		} {
			if !strings.Contains(text, want) {
				t.Errorf("%s must state %q", p, want)
			}
		}
		if strings.Contains(text, "finding_id") {
			t.Errorf("%s still carries the replaced finding_id shape", p)
		}
	}
}
