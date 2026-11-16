package daemon

// w78_params_test.go (polyforge#78 AC3): `pf override --param` /
// `--clear-param` at the daemon's overlay route. A param the capsule's
// lane snapshot declares on the step is set (the overlay file records it
// with its operator and time, an event marks the change) and cleared; the
// view lists the param overrides — the overlay layer only, never a final
// value. Refused: a param the snapshot does not declare on that step, a
// step with a live dispatch, two actions in one request, a value of more
// than one line or over 256 bytes; a refusal writes nothing.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/common/workflow/workflowtest"
	"github.com/silgrid/polyforge/internal/polyforge/runtime"
)

// w78Lane is ovLane with params: build (the live current step) and review
// declare image, review also flag; merge declares none.
const w78Lane = `max_rewinds: 3
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
    wrap_attrs: [rec]
`

func newW78Fixture(t *testing.T, wi string) *ovFixture {
	t.Helper()
	f := newOverrideFixture(t, wi)
	ji, _ := f.td.rt.Info(wi)
	workflowtest.WriteLane(t, filepath.Join(f.capDir, capsule.DirWorkflows), ji.BundleName, w78Lane)
	return f
}

func w78Row(t *testing.T, v *protocol.OverrideView, id string) protocol.OverrideStepView {
	t.Helper()
	for _, s := range v.Steps {
		if s.ID == id {
			return s
		}
	}
	t.Fatalf("the view has no row %s: %+v", id, v.Steps)
	return protocol.OverrideStepView{}
}

// w78Params drops the times (asserted separately) for a by-value compare.
func w78Params(ps []protocol.OverrideParamView) []protocol.OverrideParamView {
	out := []protocol.OverrideParamView{}
	for _, p := range ps {
		p.At = ""
		out = append(out, p)
	}
	return out
}

func TestW78OverrideParamWrite(t *testing.T) {
	f := newW78Fixture(t, "wi_w78a")
	res, ae := f.td.ovPost(t, f.wi, protocol.OverrideWriteRequest{Step: "review", Operator: "usr_1",
		Param: &protocol.OverrideParam{Name: "image", Value: "reg/img:2"}})
	if ae != nil {
		t.Fatalf("a declared param must be accepted: %s (%s)", ae.Code, ae.Detail)
	}
	if !res.Changed || res.Posture != runtime.OverridePostureRunning {
		t.Fatalf("the write's answer: %+v", res)
	}
	if got := w78Params(res.Step.Params); !reflect.DeepEqual(got, []protocol.OverrideParamView{{Name: "image", Value: "reg/img:2", By: "usr_1"}}) {
		t.Fatalf("the answer's param rows: %+v", res.Step.Params)
	}
	p := f.readOverlay(t).Steps["review"]
	if p == nil || p.Params["image"] == nil || p.Params["image"].Value != "reg/img:2" || p.Params["image"].By != "usr_1" || p.Params["image"].At == "" {
		t.Fatalf("the overlay record of review: %+v", p)
	}

	// an empty value is legal; the same set again changes nothing
	if res, ae = f.td.ovPost(t, f.wi, protocol.OverrideWriteRequest{Step: "review", Operator: "usr_1",
		Param: &protocol.OverrideParam{Name: "flag", Value: ""}}); ae != nil || !res.Changed {
		t.Fatalf("an empty value must be accepted as a change: %+v %+v", res, ae)
	}
	if res, ae = f.td.ovPost(t, f.wi, protocol.OverrideWriteRequest{Step: "review", Operator: "usr_1",
		Param: &protocol.OverrideParam{Name: "image", Value: "reg/img:2"}}); ae != nil || res.Changed {
		t.Fatalf("a repeated set must be the unchanged success: %+v %+v", res, ae)
	}

	// the view lists the overrides by name; a step with none lists none
	// (its declared default is not an override)
	v, ae := f.td.ovView(t, f.wi)
	if ae != nil {
		t.Fatalf("view: %s (%s)", ae.Code, ae.Detail)
	}
	if got := w78Params(w78Row(t, v, "review").Params); !reflect.DeepEqual(got, []protocol.OverrideParamView{
		{Name: "flag", Value: "", By: "usr_1"}, {Name: "image", Value: "reg/img:2", By: "usr_1"},
	}) {
		t.Fatalf("the view's review params: %+v", w78Row(t, v, "review").Params)
	}
	if ps := w78Row(t, v, "build").Params; len(ps) != 0 {
		t.Fatalf("build has no override, its view row lists %+v", ps)
	}

	// a clear: the tombstone keeps its operator, the view drops the row
	if res, ae = f.td.ovPost(t, f.wi, protocol.OverrideWriteRequest{Step: "review", Operator: "usr_2", ClearParam: "image"}); ae != nil || !res.Changed {
		t.Fatalf("the clear: %+v %+v", res, ae)
	}
	if p := f.readOverlay(t).Steps["review"].Params["image"]; p == nil || !p.Cleared || p.By != "usr_2" {
		t.Fatalf("the clear's record: %+v", p)
	}
	v, _ = f.td.ovView(t, f.wi)
	if got := w78Params(w78Row(t, v, "review").Params); !reflect.DeepEqual(got, []protocol.OverrideParamView{{Name: "flag", Value: "", By: "usr_1"}}) {
		t.Fatalf("after the clear the view's review params: %+v", got)
	}
	if res, ae = f.td.ovPost(t, f.wi, protocol.OverrideWriteRequest{Step: "review", Operator: "usr_2", ClearParam: "image"}); ae != nil || res.Changed {
		t.Fatalf("a repeated clear must be the unchanged success: %+v %+v", res, ae)
	}

	// one event per actual change (two sets, one clear), naming the param
	if n := f.overrideEvents(t); n != 3 {
		t.Fatalf("override events = %d, want 3", n)
	}
	for _, e := range f.events(t) {
		if e.Kind != runtime.EventOverrideSet && e.Kind != runtime.EventOverrideClear {
			continue
		}
		var body map[string]any
		_ = json.Unmarshal(e.Body, &body)
		if body["param"] == nil || body["operator"] == nil {
			t.Fatalf("the override event does not name the param and the operator: %s", e.Body)
		}
	}
}

func TestW78OverrideParamRefusals(t *testing.T) {
	f := newW78Fixture(t, "wi_w78b")
	for _, tc := range []struct {
		name string
		req  protocol.OverrideWriteRequest
		code string
		want string
	}{
		{"a param the step does not declare", protocol.OverrideWriteRequest{Step: "merge",
			Param: &protocol.OverrideParam{Name: "image", Value: "x"}}, api.ErrDaemonBadInput, "declares no step param"},
		{"a param no step declares", protocol.OverrideWriteRequest{Step: "review",
			Param: &protocol.OverrideParam{Name: "ghost", Value: "x"}}, api.ErrDaemonBadInput, "declares no step param"},
		{"a clear of an undeclared param", protocol.OverrideWriteRequest{Step: "review", ClearParam: "ghost"},
			api.ErrDaemonBadInput, "declares no step param"},
		{"the step's live dispatch", protocol.OverrideWriteRequest{Step: "build",
			Param: &protocol.OverrideParam{Name: "image", Value: "x"}}, api.ErrDaemonConflict, "live dispatch"},
		{"a param and a clear", protocol.OverrideWriteRequest{Step: "review",
			Param: &protocol.OverrideParam{Name: "image", Value: "x"}, ClearParam: "flag"}, api.ErrDaemonBadInput, "exactly one action"},
		{"a param and a chain", protocol.OverrideWriteRequest{Step: "review", Models: []string{"pi:ovtest/m-ok"},
			Param: &protocol.OverrideParam{Name: "image", Value: "x"}}, api.ErrDaemonBadInput, "exactly one action"},
		{"a clear and a skip", protocol.OverrideWriteRequest{Step: "merge", ClearParam: "image", Skip: new(bool)},
			api.ErrDaemonBadInput, "exactly one action"},
		{"a value of two lines", protocol.OverrideWriteRequest{Step: "review",
			Param: &protocol.OverrideParam{Name: "image", Value: "a\nb"}}, api.ErrDaemonBadInput, "one line"},
		{"a value over the bound", protocol.OverrideWriteRequest{Step: "review",
			Param: &protocol.OverrideParam{Name: "image", Value: strings.Repeat("v", 257)}}, api.ErrDaemonBadInput, "256"},
	} {
		_, ae := f.td.ovPost(t, f.wi, tc.req)
		if ae == nil {
			t.Errorf("%s: accepted %+v", tc.name, tc.req)
			continue
		}
		if ae.Code != tc.code || !strings.Contains(ae.Detail, tc.want) {
			t.Errorf("%s: %s %q, want %s naming %q", tc.name, ae.Code, ae.Detail, tc.code, tc.want)
		}
		if _, err := os.Stat(f.overlayFile()); !os.IsNotExist(err) {
			t.Fatalf("%s: a refused write left an overlay file behind", tc.name)
		}
	}
	// the bound itself is a legal value
	if _, ae := f.td.ovPost(t, f.wi, protocol.OverrideWriteRequest{Step: "review", Operator: "usr_1",
		Param: &protocol.OverrideParam{Name: "image", Value: strings.Repeat("v", 256)}}); ae != nil {
		t.Fatalf("a value of exactly 256 bytes must be accepted: %s (%s)", ae.Code, ae.Detail)
	}
}
