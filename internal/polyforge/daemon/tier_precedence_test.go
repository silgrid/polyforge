package daemon

// tier_precedence_test.go (polyforge#81 u3, AC3): the overlay view's
// capability-tier source labels over the real UDS daemon — a tier the
// Host catalog declares expands into its candidates and names itself as
// the source, an explicit overlay chain outranks (masks) it, `default`
// means unset and falls through with no fallback reason, a tier the
// catalog lacks (or declares empty) falls through naming the reason, an
// unknown or malformed name is refused, and a view read emits no
// composition event. The queued posture writes the tier to this daemon's
// local pending record and shows it as pending; a repeat and a second
// clear are the unchanged success. Expected strings come from AC2/AC3
// and Q8 A/Q10 A/Q13 A, never from this implementation's output.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/common/workflow/workflowtest"
	"github.com/silgrid/polyforge/internal/polyforge/cli"
	"github.com/silgrid/polyforge/internal/polyforge/runtime"
)

func newTierFixture(t *testing.T, wi string, catalog map[string][]string) *ovFixture {
	t.Helper()
	src := t.TempDir()
	modelsJSON := `{"providers":{"ovtest":{"baseUrl":"http://localhost:1","api":"openai-completions","models":[{"id":"m-ok"},{"id":"m-two"}]}}}`
	if err := os.WriteFile(filepath.Join(src, "models.json"), []byte(modelsJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	td := newTestDaemon(t, func(c *Config) {
		c.OperatorAgentDir = src
		c.StartupFile = cli.Config{StepTiers: catalog}
	})
	td.submitClaim(t, wi)
	ji, _ := td.rt.Info(wi)
	f := &ovFixture{td: td, wi: wi, capDir: ji.CapsuleDir}
	workflowtest.WriteLane(t, filepath.Join(f.capDir, capsule.DirWorkflows), ji.BundleName, ovLane)
	idn := capsule.Identity{WorkItemID: wi, Project: "proj", BundleName: ji.BundleName}
	raw, _ := json.Marshal(idn)
	if err := os.WriteFile(filepath.Join(f.capDir, capsule.CapsuleJSON), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	f.writeExec(t, map[string]*capsule.Step{
		"build":  {Status: capsule.StepInProgress, Round: 1, Candidate: "pi:ovtest/m-build"},
		"review": {Status: capsule.StepPending},
		"merge":  {Status: capsule.StepPending},
	}, "build")
	ws, err := td.cfg.Mapping.WorkspaceFor("proj")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, ".polyforge.yaml"),
		[]byte("steps:\n  proj:\n    review: { models: [pi:ovtest/m-review] }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return f
}

func tierRow(t *testing.T, v *protocol.OverrideView, id string) protocol.OverrideStepView {
	t.Helper()
	for _, s := range v.Steps {
		if s.ID == id {
			return s
		}
	}
	t.Fatalf("the view has no row %q: %+v", id, v.Steps)
	return protocol.OverrideStepView{}
}

func TestTierPrecedenceSourceAndEvent(t *testing.T) {
	f := newTierFixture(t, "wi_tier1", map[string][]string{
		"main":    {"pi:ovtest/tier-a", "claude:tier-b"},
		"default": {"pi:ovtest/tier-default"},
	})

	res, ae := f.td.ovPost(t, f.wi, protocol.OverrideWriteRequest{Step: "review", Operator: "usr_1", Tier: "main"})
	if ae != nil {
		t.Fatalf("a catalog tier set: %s (%s)", ae.Code, ae.Detail)
	}
	if !res.Changed || res.Step.Tier != "main" || res.Step.TierBy != "usr_1" {
		t.Fatalf("the write's answer: %+v", res.Step)
	}

	// the view: the expanded tier, over the yaml step entry, names itself.
	v, ae := f.td.ovView(t, f.wi)
	if ae != nil {
		t.Fatalf("view: %s", ae.Detail)
	}
	row := tierRow(t, v, "review")
	if row.Tier != "main" || row.Source != "step override tier main" || len(row.Effective) != 2 ||
		row.Effective[0] != "pi:ovtest/tier-a" || row.Effective[1] != "claude:tier-b" {
		t.Fatalf("the review row (the tier governs): %+v", row)
	}

	// an explicit overlay chain outranks the tier and masks it.
	if _, ae := f.td.ovPost(t, f.wi, protocol.OverrideWriteRequest{Step: "review", Operator: "usr_2", Models: []string{"pi:ovtest/m-two"}}); ae != nil {
		t.Fatalf("the models write: %s", ae.Detail)
	}
	v, _ = f.td.ovView(t, f.wi)
	row = tierRow(t, v, "review")
	if row.Tier != "main" || row.Source != "step override" || len(row.Effective) != 1 || row.Effective[0] != "pi:ovtest/m-two" {
		t.Fatalf("the explicit chain must govern and the tier stay visible as masked: %+v", row)
	}

	// `default` means unset: the yaml step entry governs, no fallback
	// reason is named.
	if _, ae := f.td.ovPost(t, f.wi, protocol.OverrideWriteRequest{Step: "review", Operator: "usr_3", ClearModels: true}); ae != nil {
		t.Fatalf("clear models: %s", ae.Detail)
	}
	if _, ae := f.td.ovPost(t, f.wi, protocol.OverrideWriteRequest{Step: "review", Operator: "usr_3", Tier: "default"}); ae != nil {
		t.Fatalf("the default tier set: %s", ae.Detail)
	}
	v, _ = f.td.ovView(t, f.wi)
	row = tierRow(t, v, "review")
	if row.Tier != "default" || row.TierFallback != "" || row.Source != "steps.proj.review.models" || len(row.Effective) != 1 || row.Effective[0] != "pi:ovtest/m-review" {
		t.Fatalf("`default` must mean unset (no expansion, no fallback reason): %+v", row)
	}

	// a tier the catalog lacks falls through and names why; a malformed
	// name and an unknown step are refused.
	if _, ae := f.td.ovPost(t, f.wi, protocol.OverrideWriteRequest{Step: "review", Operator: "usr_4", Tier: "nosuch"}); ae == nil || ae.Code != api.ErrDaemonBadInput || !strings.Contains(ae.Detail, "catalog declares no tier") {
		t.Fatalf("an unknown tier must be refused naming the catalog: %+v", ae)
	}
	if _, ae := f.td.ovPost(t, f.wi, protocol.OverrideWriteRequest{Step: "review", Operator: "usr_4", Tier: "Main"}); ae == nil || ae.Code != api.ErrDaemonBadInput {
		t.Fatalf("a malformed tier name must be refused: %+v", ae)
	}
	if _, ae := f.td.ovPost(t, f.wi, protocol.OverrideWriteRequest{Step: "nosuch", Operator: "usr_4", Tier: "main"}); ae == nil || ae.Code != api.ErrDaemonBadInput || !strings.Contains(ae.Detail, "declares no step") {
		t.Fatalf("an unknown step must be refused: %+v", ae)
	}

	// clear, and a second clear is the unchanged success.
	if res, ae := f.td.ovPost(t, f.wi, protocol.OverrideWriteRequest{Step: "review", Operator: "usr_5", ClearTier: true}); ae != nil || !res.Changed {
		t.Fatalf("the first tier clear must change: %+v %+v", res, ae)
	}
	if res, ae := f.td.ovPost(t, f.wi, protocol.OverrideWriteRequest{Step: "review", Operator: "usr_5", ClearTier: true}); ae != nil || res.Changed {
		t.Fatalf("a repeated tier clear must change nothing: %+v %+v", res, ae)
	}
	v, _ = f.td.ovView(t, f.wi)
	if row = tierRow(t, v, "review"); row.Tier != "" || row.Source != "steps.proj.review.models" {
		t.Fatalf("after the clear the yaml governs: %+v", row)
	}

	// a view read never emits the composition-scoped fallback event (it
	// is a dispatch composition's, never a read's).
	for _, e := range f.events(t) {
		if e.Kind == runtime.EventStepTierFallback {
			t.Fatalf("a view read emitted a step_tier_fallback event: %+v", e)
		}
	}
}

// TestOverrideTierPostures: the queued posture sets, views and clears the
// tier through this daemon's local pending record (Q8 A), the view labels
// it pending (never published), a repeated set and a second clear are the
// unchanged success, and a queued ticket still refuses the model chain.
func TestOverrideTierPostures(t *testing.T) {
	wf := t.TempDir()
	workflowtest.WriteLane(t, wf, "w79-lane", w79Lane)
	td := newTestDaemon(t, func(c *Config) {
		c.WorkflowsDir = wf
		c.StartupFile = cli.Config{StepTiers: map[string][]string{"main": {"pi:prov/main"}}}
	})
	td.hub.items = map[string]*api.WorkItem{}
	bundle := "w79-lane"
	const wi = "wi_tierq"
	td.hub.items[wi] = &api.WorkItem{ID: wi, Status: api.StatusQueued, ProjectName: "proj",
		BundleName: &bundle, Attrs: map[string]any{}}

	res, ae := td.ovPost(t, wi, protocol.OverrideWriteRequest{Step: "review", Operator: "usr_1", Tier: "main"})
	if ae != nil {
		t.Fatalf("a queued tier set: %s (%s)", ae.Code, ae.Detail)
	}
	if !res.Changed || res.Posture != runtime.OverridePostureQueued || res.Published || !strings.Contains(res.Note, "pending record") {
		t.Fatalf("the queued write's answer: %+v", res)
	}
	if res.Step.Tier != "main" {
		t.Fatalf("the queued answer's row: %+v", res.Step)
	}
	// a repeated set changes nothing
	if res, ae := td.ovPost(t, wi, protocol.OverrideWriteRequest{Step: "review", Operator: "usr_2", Tier: "main"}); ae != nil || res.Changed {
		t.Fatalf("a repeated queued tier set must change nothing: %+v %+v", res, ae)
	}

	v, ae := td.ovView(t, wi)
	if ae != nil {
		t.Fatalf("queued view: %s", ae.Detail)
	}
	row := tierRow(t, v, "review")
	if row.Tier != "main" || !row.Pending || row.Source != "step override (pending) tier main" ||
		len(row.Effective) != 1 || row.Effective[0] != "pi:prov/main" {
		t.Fatalf("the queued view must show the pending tier and its expansion: %+v", row)
	}

	if res, ae := td.ovPost(t, wi, protocol.OverrideWriteRequest{Step: "review", Operator: "usr_3", ClearTier: true}); ae != nil || !res.Changed {
		t.Fatalf("the queued tier clear must change: %+v %+v", res, ae)
	}
	if res, ae := td.ovPost(t, wi, protocol.OverrideWriteRequest{Step: "review", Operator: "usr_3", ClearTier: true}); ae != nil || res.Changed {
		t.Fatalf("a repeated queued tier clear must change nothing: %+v %+v", res, ae)
	}

	// a queued ticket still refuses the model chain (the tier is its own
	// aspect; the chain waits for the ticket to run).
	if _, ae := td.ovPost(t, wi, protocol.OverrideWriteRequest{Step: "review", Operator: "usr_4", Models: []string{"pi:prov/m"}}); ae == nil || ae.Code != api.ErrDaemonConflict || !strings.Contains(ae.Detail, "queued") {
		t.Fatalf("a queued model chain must be refused: %+v", ae)
	}

	// the queued write's own validation: a tier the catalog lacks, a
	// malformed name and an unknown step are refused with the reason
	// (review r1#8).
	if _, ae := td.ovPost(t, wi, protocol.OverrideWriteRequest{Step: "review", Operator: "usr_5", Tier: "nosuch"}); ae == nil || ae.Code != api.ErrDaemonBadInput || !strings.Contains(ae.Detail, "catalog declares no tier") {
		t.Fatalf("a queued unknown tier must be refused naming the catalog: %+v", ae)
	}
	if _, ae := td.ovPost(t, wi, protocol.OverrideWriteRequest{Step: "review", Operator: "usr_5", Tier: "Main"}); ae == nil || ae.Code != api.ErrDaemonBadInput {
		t.Fatalf("a queued malformed tier name must be refused: %+v", ae)
	}
	if _, ae := td.ovPost(t, wi, protocol.OverrideWriteRequest{Step: "nosuch", Operator: "usr_5", Tier: "main"}); ae == nil || ae.Code != api.ErrDaemonBadInput || !strings.Contains(ae.Detail, "declares no step") {
		t.Fatalf("a queued unknown step must be refused: %+v", ae)
	}
}
