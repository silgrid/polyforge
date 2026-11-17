package daemon

// w79_params_test.go (u2, polyforge#79): the params read face —
// GET /jobs/{wi}/params — one row per declared step with every param the
// lane declares on it, each one's FINAL value and the layer it comes from:
// the capsule's overlay record (claimed postures) or the reserved
// step_params attrs key (a queued work item), else the project's
// steps.<project>.<step>.params.<name>, else the lane default
// (bundle <lane> step <step> params.<name>). A queued work item composes
// from this machine's lane set — it holds no capsule. The companion CLI
// verb (`pf params`, w79_params_verb_test.go) is the same face.

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/common/workflow/workflowtest"
)

// w79Lane: build and review declare params, merge declares none.
const w79Lane = `max_rewinds: 3
steps:
  - id: build
    params:
      image: base-img
  - id: review
    gate: true
    on_fail: { target: build }
    params:
      image: base-img
      flag: ""
  - id: merge
    skippable: true
`

func (td *testDaemon) w79Params(t *testing.T, wi string) (*protocol.ParamsView, *api.Error) {
	t.Helper()
	code, raw := td.ovDo(t, "GET", "/jobs/"+wi+"/params", nil)
	if code == http.StatusOK {
		var v protocol.ParamsView
		if err := json.Unmarshal(raw, &v); err != nil {
			t.Fatalf("params view undecodable: %v (%s)", err, raw)
		}
		return &v, nil
	}
	var ae api.Error
	if err := json.Unmarshal(raw, &ae); err != nil || ae.Code == "" {
		t.Fatalf("params refusal %d undecodable as api.Error: %s", code, raw)
	}
	return nil, &ae
}

func w79Step(t *testing.T, v *protocol.ParamsView, id string) protocol.ParamsStepView {
	t.Helper()
	for _, s := range v.Steps {
		if s.ID == id {
			return s
		}
	}
	t.Fatalf("the params view has no row %s: %+v", id, v.Steps)
	return protocol.ParamsStepView{}
}

func w79Param(t *testing.T, s protocol.ParamsStepView, name string) protocol.ParamFinalView {
	t.Helper()
	for _, p := range s.Params {
		if p.Name == name {
			return p
		}
	}
	t.Fatalf("the row %s has no param %s: %+v", s.ID, name, s.Params)
	return protocol.ParamFinalView{}
}

// The claimed postures compose the capsule's own layers: the lane
// snapshot's defaults, the project's yaml values and the overlay record.
func TestW79ParamsViewClaimed(t *testing.T) {
	f := newOverrideFixture(t, "wi_w79a")
	ji, _ := f.td.rt.Info(f.wi)
	workflowtest.WriteLane(t, filepath.Join(f.capDir, capsule.DirWorkflows), ji.BundleName, w79Lane)
	// the project layer: review.image = reg/proj:9
	ws, err := f.td.cfg.Mapping.WorkspaceFor("proj")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, ".polyforge.yaml"),
		[]byte("steps:\n  proj:\n    review:\n      params:\n        image: reg/proj:9\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// the per-ticket layer: review.flag = on (the overlay record)
	if _, ae := f.td.ovPost(t, f.wi, protocol.OverrideWriteRequest{Step: "review", Operator: "usr_1",
		Param: &protocol.OverrideParam{Name: "flag", Value: "on"}}); ae != nil {
		t.Fatalf("the overlay write: %s (%s)", ae.Code, ae.Detail)
	}
	v, ae := f.td.w79Params(t, f.wi)
	if ae != nil {
		t.Fatalf("the params view: %s (%s)", ae.Code, ae.Detail)
	}
	if v.Posture != "running" || v.WorkItem != f.wi {
		t.Fatalf("the view's header: %+v", v)
	}
	if len(v.Steps) != 3 || v.Steps[0].ID != "build" || v.Steps[1].ID != "review" || v.Steps[2].ID != "merge" {
		t.Fatalf("the rows are the lane's steps in order: %+v", v.Steps)
	}
	b := w79Step(t, v, "build")
	if p := w79Param(t, b, "image"); p.Value != "base-img" || p.Source != "bundle "+ji.BundleName+" step build params.image" {
		t.Fatalf("build.image composes from the lane default: %+v", p)
	}
	if b.Status != capsule.StepInProgress {
		t.Fatalf("build carries its recorded status: %+v", b)
	}
	r := w79Step(t, v, "review")
	if p := w79Param(t, r, "image"); p.Value != "reg/proj:9" || p.Source != "steps.proj.review.params.image" {
		t.Fatalf("review.image composes from the project's yaml: %+v", p)
	}
	if p := w79Param(t, r, "flag"); p.Value != "on" || p.Source != "step override" {
		t.Fatalf("review.flag composes from the overlay: %+v", p)
	}
	if m := w79Step(t, v, "merge"); len(m.Params) != 0 {
		t.Fatalf("merge declares no params, its row carries none: %+v", m.Params)
	}
}

// A queued work item has no capsule: the lane comes from this machine's
// lane set, the per-ticket layer from the reserved step_params attrs key.
func TestW79ParamsViewQueued(t *testing.T) {
	wf := t.TempDir()
	workflowtest.WriteLane(t, wf, "w79-lane", w79Lane)
	td := newTestDaemon(t, func(c *Config) { c.WorkflowsDir = wf })
	td.hub.items = map[string]*api.WorkItem{}
	bundle := "w79-lane"
	td.hub.items["wi_w79q"] = &api.WorkItem{ID: "wi_w79q", Status: api.StatusQueued, ProjectName: "proj",
		BundleName: &bundle, Attrs: map[string]any{
			api.StepParamsKey: map[string]any{"review": map[string]any{"image": "reg/ticket:3"}},
		}}
	v, ae := td.w79Params(t, "wi_w79q")
	if ae != nil {
		t.Fatalf("a queued work item's params view: %s (%s)", ae.Code, ae.Detail)
	}
	if v.Posture != "queued" {
		t.Fatalf("the posture of a queued work item: %+v", v)
	}
	if len(v.Steps) != 3 {
		t.Fatalf("the rows are the lane's steps: %+v", v.Steps)
	}
	b := w79Step(t, v, "build")
	if p := w79Param(t, b, "image"); p.Value != "base-img" || p.Source != "bundle w79-lane step build params.image" {
		t.Fatalf("build.image is the lane default: %+v", p)
	}
	if b.Status != "" {
		t.Fatalf("a queued work item's step has no recorded status: %+v", b)
	}
	r := w79Step(t, v, "review")
	if p := w79Param(t, r, "image"); p.Value != "reg/ticket:3" || p.Source != "step override" {
		t.Fatalf("review.image composes from the attrs key (the per-ticket layer): %+v", p)
	}
	if p := w79Param(t, r, "flag"); p.Value != "" || p.Source != "bundle w79-lane step review params.flag" {
		t.Fatalf("review.flag is the lane default: %+v", p)
	}
}

// The refusals: an unknown or terminal work item answers 404 (this
// daemon holds no scene of it); a queued one whose lane does not load
// here, or that names no bundle, is refused naming it.
func TestW79ParamsViewRefusals(t *testing.T) {
	wf := t.TempDir()
	workflowtest.WriteLane(t, wf, "w79-lane", w79Lane)
	td := newTestDaemon(t, func(c *Config) { c.WorkflowsDir = wf })
	td.hub.items = map[string]*api.WorkItem{}
	if _, ae := td.w79Params(t, "wi_nope"); ae == nil || ae.Code != api.ErrDaemonNotFound {
		t.Fatalf("an unknown work item: %+v", ae)
	}
	td.hub.items["wi_w79t"] = &api.WorkItem{ID: "wi_w79t", Status: api.StatusWrapped, ProjectName: "proj"}
	if _, ae := td.w79Params(t, "wi_w79t"); ae == nil || ae.Code != api.ErrDaemonNotFound {
		t.Fatalf("a terminal work item this daemon holds no scene of: %+v", ae)
	}
	noBundle := "wi_w79nb"
	td.hub.items[noBundle] = &api.WorkItem{ID: noBundle, Status: api.StatusQueued, ProjectName: "proj"}
	if _, ae := td.w79Params(t, noBundle); ae == nil || ae.Code != api.ErrDaemonConflict || !strings.Contains(ae.Detail, "bundle") {
		t.Fatalf("a queued work item with no bundle: %+v", ae)
	}
	gone := "wi_w79gone"
	missing := "no-such-lane"
	td.hub.items[gone] = &api.WorkItem{ID: gone, Status: api.StatusQueued, ProjectName: "proj",
		BundleName: &missing}
	if _, ae := td.w79Params(t, gone); ae == nil || ae.Code != api.ErrDaemonConflict {
		t.Fatalf("a queued work item whose lane does not load: %+v", ae)
	}
}

// The queued work item's OVERLAY view (GET /jobs/{wi}/overrides,
// review r1#7): it composes from this machine's lane set and the
// reserved step_params attrs key — the overlay layer — with the model
// sources below it, never from a capsule the daemon does not hold (the
// unbranded path read the daemon's own current directory).
func TestW79OverrideViewQueued(t *testing.T) {
	wf := t.TempDir()
	workflowtest.WriteLane(t, wf, "w79-lane", w79Lane)
	td := newTestDaemon(t, func(c *Config) { c.WorkflowsDir = wf })
	td.hub.items = map[string]*api.WorkItem{}
	bundle := "w79-lane"
	td.hub.items["wi_w79v"] = &api.WorkItem{ID: "wi_w79v", Status: api.StatusQueued, ProjectName: "proj",
		BundleName: &bundle, Attrs: map[string]any{
			api.StepParamsKey: map[string]any{"review": map[string]any{"image": "reg/ticket:3"}},
		}}
	// the chain below the overlay: review's models from the workspace yaml
	ws, err := td.cfg.Mapping.WorkspaceFor("proj")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, ".polyforge.yaml"),
		[]byte("steps:\n  proj:\n    review: { models: [pi:ovtest/m-review] }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	v, ae := td.ovView(t, "wi_w79v")
	if ae != nil {
		t.Fatalf("a queued work item's overlay view: %s (%s)", ae.Code, ae.Detail)
	}
	if v.Posture != "queued" {
		t.Fatalf("the posture of a queued work item's overlay view: %+v", v)
	}
	byID := map[string]protocol.OverrideStepView{}
	for _, s := range v.Steps {
		byID[s.ID] = s
	}
	if p := byID["review"].Params; len(p) != 1 || p[0].Name != "image" || p[0].Value != "reg/ticket:3" || p[0].By != "step_params" {
		t.Fatalf("the attrs key is the overlay layer of the queued view: %+v", p)
	}
	if r := byID["review"]; len(r.Effective) != 1 || r.Effective[0] != "pi:ovtest/m-review" || r.Source != "steps.proj.review.models" {
		t.Fatalf("the queued view composes the sources below the overlay: %+v", byID["review"])
	}
	if m := byID["merge"]; !m.Skippable || m.Pending {
		t.Fatalf("the skippable tail keeps its declaration and carries no pending mark: %+v", m)
	}
}
