package daemon

// w79_queued_write_test.go (u3, polyforge#79): the queued posture's
// write — one param change of a work item no daemon holds, applied to
// the reserved step_params attrs key through the daemon's machine-signed
// hub face (the one writer), whole-key read-merge-write so every step's
// params survive; a repeated set and a clear of an unset param are the
// unchanged success (no attrs write, no aihub event); models and skip
// are refused (they are set once the ticket runs); the params are judged
// against THIS machine's lane set (the write's guard); and a claim that
// lands while the write does is detected by the re-read — the key gone
// says the claim merged the params into its capsule, the key still there
// says the change never reached it and the running ticket needs the set
// again.

import (
	"reflect"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/common/workflow/workflowtest"
	"github.com/silgrid/polyforge/internal/polyforge/runtime"
)

func w79QueuedFixture(t *testing.T) *testDaemon {
	t.Helper()
	wf := t.TempDir()
	workflowtest.WriteLane(t, wf, "w79-lane", w79Lane)
	td := newTestDaemon(t, func(c *Config) { c.WorkflowsDir = wf })
	td.hub.items = map[string]*api.WorkItem{}
	bundle := "w79-lane"
	td.hub.items["wi_w79w"] = &api.WorkItem{ID: "wi_w79w", Status: api.StatusQueued, ProjectName: "proj",
		BundleName: &bundle, Attrs: map[string]any{}}
	return td
}

func w79AttrsOf(t *testing.T, td *testDaemon, wi string) map[string]map[string]runtime.StepParamAttr {
	t.Helper()
	it, ok := td.hub.items[wi]
	if !ok {
		t.Fatalf("the hub holds no item %s", wi)
	}
	return runtime.StepParamsOfAttrs(it.Attrs)
}

// w79AttrsValues flattens the key's values for a by-value compare.
func w79AttrsValues(attrs map[string]map[string]runtime.StepParamAttr) map[string]map[string]string {
	out := map[string]map[string]string{}
	for step, params := range attrs {
		out[step] = map[string]string{}
		for name, e := range params {
			out[step][name] = e.Value
		}
	}
	return out
}

func TestW79QueuedParamWrite(t *testing.T) {
	td := w79QueuedFixture(t)
	const wi = "wi_w79w"

	// a declared param set lands in the attrs key
	res, ae := td.ovPost(t, wi, protocol.OverrideWriteRequest{Step: "review", Operator: "usr_1",
		Param: &protocol.OverrideParam{Name: "image", Value: "reg/x:1"}})
	if ae != nil {
		t.Fatalf("a queued work item's param set: %s (%s)", ae.Code, ae.Detail)
	}
	if !res.Changed || res.Posture != runtime.OverridePostureQueued || res.Published {
		t.Fatalf("the write's answer: %+v", res)
	}
	if !strings.Contains(res.Note, "attrs") || !strings.Contains(res.Note, "claim") {
		t.Fatalf("the answer's note says where the change rides: %+v", res.Note)
	}
	got := w79AttrsOf(t, td, wi)
	if vals := w79AttrsValues(got); !reflect.DeepEqual(vals, map[string]map[string]string{"review": {"image": "reg/x:1"}}) {
		t.Fatalf("the attrs key after the set: %+v", vals)
	}
	if got := w78Params(res.Step.Params); !reflect.DeepEqual(got, []protocol.OverrideParamView{{Name: "image", Value: "reg/x:1", By: "usr_1"}}) {
		t.Fatalf("the answer's step row: %+v", res.Step.Params)
	}

	// a second param of the same step merges, keeping the first; an empty
	// value is a legal set
	if res, ae = td.ovPost(t, wi, protocol.OverrideWriteRequest{Step: "review", Operator: "usr_2",
		Param: &protocol.OverrideParam{Name: "flag", Value: ""}}); ae != nil || !res.Changed {
		t.Fatalf("an empty value is a queued set: %+v %+v", res, ae)
	}
	if got := w79AttrsValues(w79AttrsOf(t, td, wi))["review"]; !reflect.DeepEqual(got, map[string]string{"image": "reg/x:1", "flag": ""}) {
		t.Fatalf("the attrs key carries both params: %+v", got)
	}

	// a repeated set is the unchanged success: no attrs write
	writes := len(td.hub.attrsWrites)
	if res, ae = td.ovPost(t, wi, protocol.OverrideWriteRequest{Step: "review", Operator: "usr_1",
		Param: &protocol.OverrideParam{Name: "image", Value: "reg/x:1"}}); ae != nil || res.Changed {
		t.Fatalf("a repeated set must be the unchanged success: %+v %+v", res, ae)
	}
	if len(td.hub.attrsWrites) != writes {
		t.Fatalf("an unchanged success writes no attrs: %d -> %d", writes, len(td.hub.attrsWrites))
	}

	// a clear removes the param, keeping the step's other one
	if res, ae = td.ovPost(t, wi, protocol.OverrideWriteRequest{Step: "review", Operator: "usr_3",
		ClearParam: "image"}); ae != nil || !res.Changed {
		t.Fatalf("the clear: %+v %+v", res, ae)
	}
	if got := w79AttrsValues(w79AttrsOf(t, td, wi))["review"]; !reflect.DeepEqual(got, map[string]string{"flag": ""}) {
		t.Fatalf("the clear removes only its own param: %+v", got)
	}
	// a repeated clear is the unchanged success
	if res, ae = td.ovPost(t, wi, protocol.OverrideWriteRequest{Step: "review", Operator: "usr_3",
		ClearParam: "image"}); ae != nil || res.Changed {
		t.Fatalf("a repeated clear must be the unchanged success: %+v %+v", res, ae)
	}
	// clearing the step's last param drops the whole key (null)
	if _, ae = td.ovPost(t, wi, protocol.OverrideWriteRequest{Step: "review", Operator: "usr_3",
		ClearParam: "flag"}); ae != nil {
		t.Fatalf("the last clear: %s (%s)", ae.Code, ae.Detail)
	}
	if got := w79AttrsOf(t, td, wi); len(got) != 0 {
		t.Fatalf("clearing the last param drops the key: %+v", got)
	}
	if _, has := td.hub.items[wi].Attrs[api.StepParamsKey]; has {
		t.Fatalf("the key is deleted, not an empty object: %+v", td.hub.items[wi].Attrs)
	}
}

func TestW79QueuedWriteRefusals(t *testing.T) {
	td := w79QueuedFixture(t)
	const wi = "wi_w79w"
	// the chain and the skip switch are the running ticket's: a queued
	// work item takes param changes only
	for _, req := range []protocol.OverrideWriteRequest{
		{Step: "review", Operator: "usr_1", Models: []string{"pi:t/m-1"}},
		{Step: "review", Operator: "usr_1", ClearModels: true},
		{Step: "merge", Operator: "usr_1", Skip: boolPtr(true)},
	} {
		if _, ae := td.ovPost(t, wi, req); ae == nil || ae.Code != api.ErrDaemonConflict || !strings.Contains(ae.Detail, "queued") {
			t.Fatalf("%+v must be refused as a queued work item's: %+v", req, ae)
		}
	}
	if got := w79AttrsOf(t, td, wi); len(got) != 0 {
		t.Fatalf("a refused write changes no attrs: %+v", got)
	}
	// the write's guard is THIS machine's lane set: an unknown step, a
	// param the lane does not declare on the step, a value that is not
	// one line
	for _, tc := range []struct {
		req  protocol.OverrideWriteRequest
		want string
	}{
		{protocol.OverrideWriteRequest{Step: "nosuch", Operator: "u", Param: &protocol.OverrideParam{Name: "image", Value: "x"}}, "declares no step"},
		{protocol.OverrideWriteRequest{Step: "merge", Operator: "u", Param: &protocol.OverrideParam{Name: "image", Value: "x"}}, "declares no step param"},
		{protocol.OverrideWriteRequest{Step: "review", Operator: "u", Param: &protocol.OverrideParam{Name: "nosuch", Value: "x"}}, "declares no step param"},
		{protocol.OverrideWriteRequest{Step: "review", Operator: "u", Param: &protocol.OverrideParam{Name: "image", Value: "two\nlines"}}, "one line"},
		{protocol.OverrideWriteRequest{Step: "review", Operator: "u", Param: &protocol.OverrideParam{Name: "image", Value: strings.Repeat("x", 257)}}, "256"},
	} {
		_, ae := td.ovPost(t, wi, tc.req)
		if ae == nil || ae.Code != api.ErrDaemonBadInput || !strings.Contains(ae.Detail, tc.want) {
			t.Fatalf("%+v must be refused naming %q: %+v", tc.req, tc.want, ae)
		}
	}
	// a lane that does not load here names it
	missing := "no-such-lane"
	td.hub.items["wi_w79m"] = &api.WorkItem{ID: "wi_w79m", Status: api.StatusQueued, ProjectName: "proj",
		BundleName: &missing, Attrs: map[string]any{}}
	if _, ae := td.ovPost(t, "wi_w79m", protocol.OverrideWriteRequest{Step: "review", Operator: "u",
		Param: &protocol.OverrideParam{Name: "image", Value: "x"}}); ae == nil || ae.Code != api.ErrDaemonConflict || !strings.Contains(ae.Detail, "no-such-lane") {
		t.Fatalf("a lane that does not load: %+v", ae)
	}
}

// A claim landing while the write does is detected by the re-read: the
// key gone says the claim merged the params into its capsule; the key
// still there says the change never reached the claim and the running
// ticket needs the set again (the owner's 已定 3: 写入侧写完再读一次状态).
func TestW79QueuedWriteRaceNoted(t *testing.T) {
	td := w79QueuedFixture(t)
	const wi = "wi_w79w"
	it := td.hub.items[wi]

	// the claim consumed the key: the note says the params merged
	td.hub.onAttrsWrite = func() {
		it.Status = api.StatusRunning
		delete(it.Attrs, api.StepParamsKey)
	}
	res, ae := td.ovPost(t, wi, protocol.OverrideWriteRequest{Step: "review", Operator: "usr_1",
		Param: &protocol.OverrideParam{Name: "image", Value: "reg/x:1"}})
	if ae != nil {
		t.Fatalf("the raced write still succeeds: %s (%s)", ae.Code, ae.Detail)
	}
	if !res.Changed || res.Posture != runtime.OverridePostureQueued {
		t.Fatalf("the raced write's answer: %+v", res)
	}
	if !strings.Contains(res.Note, "claimed") || !strings.Contains(res.Note, "merged") {
		t.Fatalf("a claim that consumed the key is named: %+v", res.Note)
	}

	// the claim left the key behind (it claimed before the write landed):
	// the note says the running ticket needs the set again
	it.Status = api.StatusQueued
	it.Attrs[api.StepParamsKey] = map[string]any{}
	td.hub.onAttrsWrite = func() { it.Status = api.StatusRunning }
	res, ae = td.ovPost(t, wi, protocol.OverrideWriteRequest{Step: "review", Operator: "usr_1",
		Param: &protocol.OverrideParam{Name: "image", Value: "reg/x:2"}})
	if ae != nil {
		t.Fatalf("the raced write still succeeds: %s (%s)", ae.Code, ae.Detail)
	}
	if !strings.Contains(res.Note, "set the param on the running ticket again") {
		t.Fatalf("a change that never reached the claim is named: %+v", res.Note)
	}
}

func boolPtr(b bool) *bool { return &b }
