package test

// w46_e2e_author_on_demand_contract_test.go: text contract for.
//
// Work item #46 gives the feature-full lane a per-work-item e2e-author mode:
// a lane var `e2e_author_mode` whose default `required` is the untouched lane,
// and whose owner-approved `skipped-with-rationale` lets an internal-mechanism
// / non-behaviour-face work item run the lane without authoring new e2e
// (the decision and its rationale are recorded in artifacts/grill-qa.md).
//
// The change is prompt material (workflow lane text and step bodies): no
// endpoint, verb, flag or protocol moves and the driver's step graph is not
// touched, so the only mechanical contract is the shipped text itself. This
// test pins the markers the four files must carry. It is red on the base tree
// (none of the marker sets existed there) and green only once the mode
// shipped. The qualitative half — that a real grill actually proposes the mode
// and the owner actually approves it — cannot be machine-verified from text
// and is observed on real WIs, recorded in the capsule's code summary, the
// same way/#44 handled their non-machine-verifiable residual.
//
// The sweep later removed the mode text's "static default"
// constant explanation and the manufactured clean-tree skip evidence
// (the "git status --porcelain" proof and the "any real command is not a
// skip" framing), keeping the mode's fail-closed resolution; the marker set
// below was updated with that stated reason.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestW46E2eAuthorOnDemandContract(t *testing.T) {
	root := v1Root(t)
	cases := []struct {
		rel  string
		want []string
	}{
		{
			filepath.Join("workflows", "feature-full", "workflow.md"),
			[]string{
				"e2e_author_mode",
				"e2e_author_mode: required",
				"skipped-with-rationale",
				"behaviour face",
				// the var is the lane default, never the per-WI value; the
				// per-WI mode lives only in grill-qa.md and is fail-closed
				// to `required` without the owner's explicit approval.
				"per-work-item value",
				"the owner's explicit approval",
				"fail-closed",
				// the audit anchor is grill-qa.md alone; plan only restates.
				"the audit anchor is `artifacts/grill-qa.md` alone",
			},
		},
		{
			filepath.Join("workflows", "feature-full", "steps", "grill.md"),
			[]string{
				"e2e-author mode",
				"skipped-with-rationale",
				"artifacts/grill-qa.md",
				"internal-mechanism",
				// a skip proposal must reach the owner as a row of the
				// single batch table (needs_input), never be settled in a
				// zero-question pass; only the owner's explicit approval
				// makes a skip effective.
				"single batch table",
				"zero-question pass",
				"the owner's explicit approval",
			},
		},
		{
			filepath.Join("workflows", "feature-full", "steps", "plan.md"),
			[]string{
				"e2e-author mode",
				"skipped-with-rationale",
				"artifacts/plan.md",
				// no approved record -> the default applies; a skip needs
				// the owner's explicit approval (fail-closed to `required`).
				"with no approved record",
				"the owner's explicit approval",
				"your statement\ndecides nothing",
			},
		},
		{
			filepath.Join("workflows", "feature-full", "steps", "e2e-author.md"),
			[]string{
				"{{e2e_author_mode}}",
				"skipped-with-rationale",
				"skip_rationale",
				// the rendered var is the lane default, not the run's mode; a
				// skip needs the owner's explicit approval and records the one
				// command that read it (the grill-qa.md read). The
				// sweep removed the "static default" constant explanation and
				// the manufactured clean-tree evidence ("git status
				// --porcelain", "any real command is not a skip") while keeping
				// the mode's fail-closed resolution.
				"lane default",
				"the owner's explicit approval",
				// an all-green baseline is completable: the owner's answers
				"completed (all green)",
			},
		},
	}
	for _, c := range cases {
		b, err := os.ReadFile(filepath.Join(root, c.rel))
		if err != nil {
			t.Fatalf("%s is lane text the on-demand e2e-author mode must extend: %v", c.rel, err)
		}
		s := string(b)
		for _, want := range c.want {
			if !strings.Contains(s, want) {
				t.Errorf("%s must carry the on-demand marker %q — the feature-full lane carries one `e2e_author_mode` var (default `required`); `skipped-with-rationale` is owner-approved for an internal-mechanism / non-behaviour-face work item, proposed by grill and recorded in artifacts/grill-qa.md, applied by plan, and honoured by e2e-author (`skip_rationale`, empty scenarios/baseline); a behaviour face work item stays `required`", c.rel, want)
			}
		}
	}
}

// TestW46E2eAuthorModeDefaultOnlyFeatureFull (rule.generality evidence): the
// mode is a feature-full lane var only, and its shipped default is `required`.
// A project that does not adopt the mode gets the old lane byte-for-byte: no
// other shipped bundle declares the var.
func TestW46E2eAuthorModeDefaultOnlyFeatureFull(t *testing.T) {
	root := v1Root(t)
	entries, err := os.ReadDir(filepath.Join(root, "workflows"))
	if err != nil {
		t.Fatalf("workflows/ must be readable: %v", err)
	}
	sawFeatureFull := false
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		wf := filepath.Join(root, "workflows", e.Name(), "workflow.md")
		b, err := os.ReadFile(wf)
		if err != nil {
			continue // a directory with no workflow.md is not a bundle
		}
		s := string(b)
		if e.Name() == "feature-full" {
			sawFeatureFull = true
			if !strings.Contains(s, "e2e_author_mode: required") {
				t.Errorf("feature-full's workflow.md must declare the default `e2e_author_mode: required` — the untrimmed lane is the shipped default")
			}
			continue
		}
		if strings.Contains(s, "e2e_author_mode") {
			t.Errorf("%s declares e2e_author_mode; the mode is feature-full only, and a non-adopting lane must stay unchanged", wf)
		}
	}
	if !sawFeatureFull {
		t.Fatal("workflows/feature-full/workflow.md was not found")
	}
}
