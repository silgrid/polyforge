package workflow_test

// v4_keys_test.go (workflow contract 4, design #483 §2): every key
// contract 4 removed — a step's inputs, verifies, outputs, verdicts,
// effects, handoff and attrs, on_fail.charge, a definition's output schema
// and the bar lock — is refused at the key (the integration property's
// fix_lane went with the property before contract 1: c5_exclusive_test.go), its message carrying the rewrite (what to write instead); and
// the keys it added — a step's hub list and its wrap_attrs — load in
// their shape and are refused outside it. One test per removed key.

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/workflow"
	"github.com/silgrid/polyforge/internal/common/workflow/workflowtest"
)

// refusedOnce loads a lane whose frontmatter is front (definitions
// filled in) and asserts exactly one defect of code at path whose message
// carries every phrase of rewrite.
func refusedOnce(t *testing.T, front, code, path string, rewrite ...string) workflow.ContractError {
	t.Helper()
	dir := workflowtest.WriteLaneUnchecked(t, t.TempDir(), "demo", front)
	_, errs := workflow.Load(dir)
	if len(errs) != 1 || errs[0].Code != code || errs[0].Path != path {
		t.Fatalf("defects %v, want one %s at %s", errs, code, path)
	}
	for _, p := range rewrite {
		if !strings.Contains(errs[0].Msg, p) {
			t.Errorf("the defect %q does not carry %q", errs[0].Msg, p)
		}
	}
	return errs[0]
}

const removedIn4 = "removed before workflow contract 1"

func TestV4RefusesInputs(t *testing.T) {
	refusedOnce(t, "steps:\n  - id: work\n    inputs: [ { memory: recall } ]\n", workflow.CodeUnknownKey, "steps[0](work).inputs", removedIn4, "artifacts/handoff.md")
}

func TestV4RefusesVerifies(t *testing.T) {
	refusedOnce(t, "steps:\n  - id: work\n  - id: ship\n    verifies: [ { step: work.output.summary, duty: d } ]\n", workflow.CodeUnknownKey, "steps[1](ship).verifies", removedIn4, "skill")
}

func TestV4RefusesOutputs(t *testing.T) {
	refusedOnce(t, "steps:\n  - id: work\n    outputs: { schema: enforce }\n", workflow.CodeUnknownKey, "steps[0](work).outputs", removedIn4, "pf step handoff")
}

func TestV4RefusesVerdicts(t *testing.T) {
	refusedOnce(t, "max_rewinds: 1\nsteps:\n  - id: work\n  - id: check\n    gate: true\n    on_fail: { target: work }\n    verdicts: [pass, fail, defer]\n", workflow.CodeUnknownKey, "steps[1](check).verdicts", removedIn4, "fail")
}

func TestV4RefusesEffects(t *testing.T) {
	refusedOnce(t, "steps:\n  - id: work\n    effects: [fix_tickets]\n", workflow.CodeUnknownKey, "steps[0](work).effects", removedIn4, "pf step hub", "hub:")
}

func TestV4RefusesHandoff(t *testing.T) {
	refusedOnce(t, "steps:\n  - id: work\n    handoff: landing\n", workflow.CodeUnknownKey, "steps[0](work).handoff", removedIn4, "pf step hub wrap-attrs")
}

func TestV4RefusesAttrs(t *testing.T) {
	refusedOnce(t, "steps:\n  - id: work\n    attrs:\n      depth: { type: int, default: 0 }\n", workflow.CodeUnknownKey, "steps[0](work).attrs", removedIn4, "attrs overrides")
}

func TestV4RefusesOnFailCharge(t *testing.T) {
	refusedOnce(t, "max_rewinds: 1\nsteps:\n  - id: work\n  - id: check\n    gate: true\n    on_fail: { target: work, charge: unless_owner_answered }\n", workflow.CodeUnknownKey, "steps[1](check).on_fail.charge", removedIn4, "--per-owner")
}

func TestV4RefusesTheBarLock(t *testing.T) {
	e := refusedOnce(t, "steps:\n  - id: work\n    resources: { locks: [bar] }\n", workflow.CodeInvalidValue, "steps[0](work).resources.locks[0]", removedIn4, "delete `bar`")
	if e.Line != 6 {
		t.Errorf("the bar lock's defect sits at line %d, want 6", e.Line)
	}
}

// TestV4RefusesADefinitionOutputSchema: a step definition declaring an
// output schema is WFC-191 at that key, with its rewrite; without it the
// definition loads (its frontmatter is name and description alone).
func TestV4RefusesADefinitionOutputSchema(t *testing.T) {
	dir := workflowtest.WriteLaneUnchecked(t, t.TempDir(), "demo", "steps:\n  - id: work\n")
	def := filepath.Join(dir, "steps", "work.md")
	write(t, def, "---\nname: work\ndescription: d\noutput:\n  type: object\n---\n\nRun work.\n")
	_, errs := workflow.Load(dir)
	if len(errs) != 1 || errs[0].Code != workflow.CodeDefinitionFront || errs[0].File != def || errs[0].Path != "output" || errs[0].Line != 4 ||
		!strings.Contains(errs[0].Msg, removedIn4) || !strings.Contains(errs[0].Msg, "pf step handoff") {
		t.Fatalf("defects %v, want one WFC-191 at %s:4 output with its rewrite", errs, def)
	}
	write(t, def, "---\nname: work\ndescription: d\n---\n\nRun work.\n")
	if _, errs := workflow.Load(dir); len(errs) != 0 {
		t.Fatalf("a definition of name and description loads: %v", errs)
	}
}

// TestV4HubAndWrapAttrs: `hub` takes `verbs` — the hub verbs, each once —
// and `attrs` — attrs keys, each once, which let the step call attrs
// (workflow contract 1); `wrap_attrs` attr names, each once; anything else
// is WFC-112 at the entry (an unknown key under `hub` WFC-111).
func TestV4HubAndWrapAttrs(t *testing.T) {
	dir := workflowtest.WriteLane(t, t.TempDir(), "demo", "steps:\n  - id: work\n    hub: { verbs: [get, list, file, wrap-attrs, batch], attrs: [integration, note_2] }\n    wrap_attrs: [integration, integration_batch]\n")
	lane, errs := workflow.Load(dir)
	if len(errs) != 0 {
		t.Fatal(workflow.AsLoadError(dir, errs))
	}
	work := lane.Step("work")
	if strings.Join(work.Hub, ",") != strings.Join(workflow.HubVerbs, ",") || strings.Join(work.HubAttrs, ",") != "integration,note_2" || strings.Join(work.WrapAttrs, ",") != "integration,integration_batch" {
		t.Fatalf("work = hub %v attrs %v wrap_attrs %v", work.Hub, work.HubAttrs, work.WrapAttrs)
	}
	if got := strings.Join(work.HubAllowed(), ","); got != "get,list,file,wrap-attrs,batch,attrs" {
		t.Fatalf("hub allowed = %s", got)
	}
	for _, c := range []struct{ name, front, at string }{
		{"an unknown hub verb", "steps:\n  - id: work\n    hub: { verbs: [get, delete] }\n", "steps[0](work).hub.verbs[1]"},
		{"the attrs write named as a verb", "steps:\n  - id: work\n    hub: { verbs: [attrs] }\n", "steps[0](work).hub.verbs[0]"},
		{"the removed move verb", "steps:\n  - id: work\n    hub: { verbs: [move] }\n", "steps[0](work).hub.verbs[0]"},
		{"a hub verb twice", "steps:\n  - id: work\n    hub: { verbs: [get, get] }\n", "steps[0](work).hub.verbs[1]"},
		{"a hub that is no mapping", "steps:\n  - id: work\n    hub: [get]\n", "steps[0](work).hub"},
		{"hub verbs that are no list", "steps:\n  - id: work\n    hub: { verbs: get }\n", "steps[0](work).hub.verbs"},
		{"an attrs key malformed", "steps:\n  - id: work\n    hub: { attrs: [Integration] }\n", "steps[0](work).hub.attrs[0]"},
		{"an attrs key twice", "steps:\n  - id: work\n    hub: { attrs: [integration, integration] }\n", "steps[0](work).hub.attrs[1]"},
		{"a wrap attr name malformed", "steps:\n  - id: work\n    wrap_attrs: [Integration]\n", "steps[0](work).wrap_attrs[0]"},
		{"a wrap attr twice", "steps:\n  - id: work\n    wrap_attrs: [landing, landing]\n", "steps[0](work).wrap_attrs[1]"},
	} {
		t.Run(c.name, func(t *testing.T) {
			refusedOnce(t, c.front, workflow.CodeInvalidValue, c.at)
		})
	}
}
