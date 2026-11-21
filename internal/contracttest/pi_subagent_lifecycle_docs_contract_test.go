package test

// pi_subagent_lifecycle_docs_contract_test.go (polyforge#200 AC4): the
// process-lifecycle behaviors this ticket delivered are documented where
// each document owns them — the extension's per-call timeout and stop
// semantics in the build dispatch doc the steps read, the step-end
// sweep's failure boundary and the ctx-cancel skip in the design
// reference's lifecycle section, the child worker's timeout and
// descendant identity in the v2 design's pi adapter layer. A behavior
// change with no doc line here is a doc-impact miss the contract turns
// red.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/polyforge/runtime/stepagentassets"
)

// TestPiSubagentLifecycleDocs pins the docs against the delivered
// behaviors: the default and validation of timeoutSeconds, the
// timeout-is-a-failure semantics, the stop's TERM→KILL and descendant
// identity, the cleanup_failed park with its resume re-run, and the
// ctx-cancel skip — plus pi_subagents' unchanged default-off.
func TestPiSubagentLifecycleDocs(t *testing.T) {
	// the build dispatch doc (what a pi build step reads before dispatching)
	dispatch := w68Flat(t, "workflows/_shared/build_subagents.md")
	for _, want := range []string{
		"`timeoutSeconds`",
		"默认 1800", // the default
		"正整数",     // positive-integer validation
		"超时是失败",   // a timeout is a failure, never a partial success
		"TERM", "PF_SUBAGENT_ID",
	} {
		if !strings.Contains(dispatch, want) {
			t.Errorf("build_subagents.md must document %q for the pi dispatch (polyforge#200)", want)
		}
	}

	// the design reference's lifecycle section (§6.2's step-end paragraph)
	reference := w68Flat(t, "docs/design/polyforge-vnext/reference.md")
	for _, want := range []string{
		"cleanup_failed",            // the park kind and its release
		"re-runs the step-end hook", // the resume settles the kept hand-off only after a clean re-sweep
		"cancelled context",         // Q10 A: the skip on ctx cancel, covered by the recovery Cleanup
	} {
		if !strings.Contains(reference, want) {
			t.Errorf("reference.md's lifecycle section must document %q (polyforge#200)", want)
		}
	}

	// the v2 design's pi adapter layer: the child worker's own lifecycle
	converge := w68Flat(t, "docs/design/polyforge-vnext/v2-converge.md")
	for _, want := range []string{
		"`timeoutSeconds`", "PF_SUBAGENT_ID",
	} {
		if !strings.Contains(converge, want) {
			t.Errorf("v2-converge.md's pi adapter layer must document %q (polyforge#200)", want)
		}
	}

	// the workflow contract's lane-blocking park list names the kind
	contractDoc := w68Flat(t, "docs/reference/workflow-contract.md")
	if !strings.Contains(contractDoc, "`cleanup_failed`") {
		t.Error("workflow-contract.md's hand-off reaction must name the cleanup_failed park (polyforge#200)")
	}

	// Cross-checks (r1#7): the docs' numbers and kinds are the source's,
	// not free text. The embedded extension declares the timeout default;
	// the driver declares the blocked kind.
	ext, err := stepagentassets.FS.ReadFile("extensions/subagent/index.ts")
	if err != nil {
		t.Fatalf("the embedded extension: %v", err)
	}
	m := regexp.MustCompile(`DEFAULT_TIMEOUT_SECONDS = (\d+);`)
	found := m.FindStringSubmatch(string(ext))
	if found == nil {
		t.Fatal("the embedded extension no longer declares DEFAULT_TIMEOUT_SECONDS — the cross-check must follow it")
	}
	def := found[1]
	if !strings.Contains(dispatch, "默认 "+def) {
		t.Errorf("build_subagents.md's timeout default must match the extension's DEFAULT_TIMEOUT_SECONDS = %s", def)
	}
	handoff, err := os.ReadFile(filepath.Join(v1Root(t), "internal", "polyforge", "driver", "handoff.go"))
	if err != nil {
		t.Fatalf("the driver's handoff.go: %v", err)
	}
	km := regexp.MustCompile(`BlockerKindCleanupFailed = "([^"]+)"`)
	kfound := km.FindStringSubmatch(string(handoff))
	if kfound == nil {
		t.Fatal("the driver no longer declares BlockerKindCleanupFailed — the cross-check must follow it")
	}
	kind := kfound[1]
	for _, doc := range []string{reference, contractDoc, converge} {
		if !strings.Contains(doc, "`"+kind+"`") {
			t.Errorf("a doc must name the driver's blocked kind %q verbatim", kind)
		}
	}

	// unchanged: the switch stays default-off (the #193 baseline)
	if !strings.Contains(dispatch, "`off`（默认）") {
		t.Error("build_subagents.md must keep pi_subagents' default-off statement (#193 baseline, unchanged)")
	}
}
