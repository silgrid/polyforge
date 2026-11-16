package runtime

// w78_params_test.go (polyforge#78 AC3): the overlay write of a step
// param. applyAspect sets one declared param or clears it — each param
// records its own operator and time, a repeated set or clear changes
// nothing, a clear leaves a tombstone with its operator and time — and
// the claim's merge of the daemon-local pending record takes each param
// by name, by time: an older pending value never overrides a newer
// capsule record, and a clear's tombstone is never undone by an older
// value. A pending param write lands in the fresh capsule at the claim.

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/protocol"
)

func w78Set(name, value, by string) OverrideChange {
	return OverrideChange{Step: "probe", Operator: by, Param: &protocol.OverrideParam{Name: name, Value: value}}
}

func w78Clear(name, by string) OverrideChange {
	return OverrideChange{Step: "probe", Operator: by, ClearParam: name}
}

func TestW78ApplyAspectParam(t *testing.T) {
	entries := map[string]*capsule.StepOverride{}
	changed, e := applyAspect(entries, w78Set("image", "reg/img:2", "usr_1"), false)
	if !changed || e == nil || e.Params["image"] == nil {
		t.Fatalf("a param set must change the entry: changed=%v %+v", changed, e)
	}
	img := *e.Params["image"]
	if img.Value != "reg/img:2" || img.By != "usr_1" || img.Cleared {
		t.Fatalf("the set's record: %+v", img)
	}
	if _, err := time.Parse(time.RFC3339Nano, img.At); err != nil {
		t.Fatalf("the set's time %q is not RFC 3339: %v", img.At, err)
	}
	if e.Models != nil || e.Skip != nil {
		t.Fatalf("a param set touched another aspect: %+v", e)
	}

	// the same set again: nothing changes, its record stays as it was
	if changed, e = applyAspect(entries, w78Set("image", "reg/img:2", "usr_9"), false); changed || *e.Params["image"] != img {
		t.Fatalf("a repeated set must change nothing: changed=%v %+v", changed, e.Params["image"])
	}

	// an empty value is a value (a set, not a clear)
	if changed, e = applyAspect(entries, w78Set("flag", "", "usr_1"), false); !changed || e.Params["flag"] == nil || e.Params["flag"].Cleared || e.Params["flag"].Value != "" {
		t.Fatalf("a set to the empty value: changed=%v %+v", changed, e.Params["flag"])
	}
	flag := *e.Params["flag"]

	// a change of one param records its own operator; the other's stays
	if changed, e = applyAspect(entries, w78Set("image", "reg/img:3", "usr_2"), false); !changed || e.Params["image"].Value != "reg/img:3" || e.Params["image"].By != "usr_2" {
		t.Fatalf("a changed value: changed=%v %+v", changed, e.Params["image"])
	}
	if *e.Params["flag"] != flag {
		t.Fatalf("a change of image rewrote flag's record: %+v", e.Params["flag"])
	}

	// a clear leaves the tombstone with its own operator and time
	if changed, e = applyAspect(entries, w78Clear("image", "usr_3"), false); !changed {
		t.Fatal("the clear of a set param must change the entry")
	}
	if p := e.Params["image"]; p == nil || !p.Cleared || p.Value != "" || p.By != "usr_3" || p.At == "" {
		t.Fatalf("the clear's record: %+v", p)
	}
	// a repeated clear, and the clear of a param never set, change nothing
	if changed, _ = applyAspect(entries, w78Clear("image", "usr_4"), false); changed {
		t.Fatal("a repeated clear must change nothing")
	}
	if changed, e = applyAspect(entries, w78Clear("ghost", "usr_4"), false); changed || e.Params["ghost"] != nil {
		t.Fatalf("the clear of a param never set must change nothing: changed=%v %+v", changed, e.Params["ghost"])
	}

	// the pending record: a clear of a param the sealed capsule carries
	// records the tombstone the claim's merge applies
	pend := map[string]*capsule.StepOverride{}
	if changed, e = applyAspect(pend, w78Clear("image", "usr_5"), true); !changed || e.Params["image"] == nil || !e.Params["image"].Cleared || e.Params["image"].By != "usr_5" {
		t.Fatalf("a pending clear of a sealed param: changed=%v %+v", changed, e)
	}
}

func TestW78MergePendingParams(t *testing.T) {
	const t1, t2, t3 = "2026-11-16T10:00:00Z", "2026-11-16T11:00:00Z", "2026-11-16T12:00:00Z"
	capsuleOv := capsule.StepOverrides{Steps: map[string]*capsule.StepOverride{
		"probe": {Params: map[string]*capsule.ParamOverride{
			"image": {Value: "cap-img", By: "usr_c", At: t2},
			"flag":  {Value: "cap-flag", By: "usr_c", At: t2},
			"keep":  {Value: "k", By: "usr_c", At: t1},
			"gone":  {Cleared: true, By: "usr_c", At: t3},
		}},
	}}
	pending := map[string]*capsule.StepOverride{
		"probe": {Params: map[string]*capsule.ParamOverride{
			"image": {Value: "pend-old", By: "usr_p", At: t1}, // older: the capsule's stays
			"flag":  {Cleared: true, By: "usr_p", At: t3},     // a newer clear: applies
			"extra": {Value: "x", By: "usr_p", At: t3},        // new: lands
			"gone":  {Value: "back", By: "usr_p", At: t2},     // older than the clear: never revives
		}},
		"fresh": {Params: map[string]*capsule.ParamOverride{"p": {Value: "y", By: "usr_p", At: t1}}},
	}
	merged, applied := MergedStepOverrides(capsuleOv, pending)
	want := map[string]*capsule.ParamOverride{
		"image": {Value: "cap-img", By: "usr_c", At: t2},
		"flag":  {Cleared: true, By: "usr_p", At: t3},
		"keep":  {Value: "k", By: "usr_c", At: t1},
		"gone":  {Cleared: true, By: "usr_c", At: t3},
		"extra": {Value: "x", By: "usr_p", At: t3},
	}
	if e := merged.Steps["probe"]; e == nil || !reflect.DeepEqual(e.Params, want) {
		t.Fatalf("the merged probe params:\n got %+v\nwant %+v", e, want)
	}
	if e := merged.Steps["fresh"]; e == nil || !reflect.DeepEqual(e.Params, map[string]*capsule.ParamOverride{"p": {Value: "y", By: "usr_p", At: t1}}) {
		t.Fatalf("a pending-only step's param must land: %+v", e)
	}
	if !applied["probe"].Params || !applied["fresh"].Params {
		t.Fatalf("the steps whose params came from pending must be marked: %+v", applied)
	}
	// the read face leaves the capsule's own record untouched
	if p := capsuleOv.Steps["probe"].Params["flag"]; p.Cleared || p.Value != "cap-flag" {
		t.Fatalf("MergedStepOverrides wrote through to the capsule's record: %+v", p)
	}

	// a param write recorded while the work item was held by no job lands
	// in the fresh capsule at its claim
	r, _ := pendingEnv(t)
	const wi = "wi_w78pm"
	blockedWrite(t, r, wi, OverrideChange{Step: "fix", Operator: "usr_1", Param: &protocol.OverrideParam{Name: "image", Value: "reg/img:9"}})
	if err := r.Start(context.Background(), wi, StartOpts{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	j := r.jobOf(wi)
	if j == nil {
		t.Fatal("the claim must run to a live agent")
	}
	t.Cleanup(func() { _ = r.Stop(context.Background(), wi, protocol.FinishWrapped) })
	ov, err := capsule.ReadStepOverrides(j.capsuleDir)
	if err != nil {
		t.Fatal(err)
	}
	if e := ov.Steps["fix"]; e == nil || e.Params["image"] == nil || e.Params["image"].Value != "reg/img:9" || e.Params["image"].By != "usr_1" {
		t.Fatalf("the pending param did not land at the claim: %+v", e)
	}
}
