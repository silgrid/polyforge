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
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
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

// w78Lane declares params on two steps; grill declares none.
const w78Lane = `name: demo-bundle
steps:
  - { id: grill }
  - id: fix
    params: { image: lane-img, flag: lane-flag, tag: lane-tag, mode: lane-mode }
  - id: release
    params: { image: rel-img }
`

// TestW78ParamsComposeFor (AC4): the dispatch-time param composition a
// derivation carries reads the capsule's lane snapshot, the workspace
// yaml and the capsule's overlay record afresh at every call — the ticket
// layer over the project's over the lane default, a project or ticket
// value of a param the step does not declare governing nothing — and it
// fails like the candidate composition: an unloadable yaml is the
// DAEMON-414 error the driver blocks on, and a value that is not one line
// within the bound never reaches a dispatch.
func TestW78ParamsComposeFor(t *testing.T) {
	setGitIdentity(t)
	wfDir := laneBundle(t, w78Lane)
	hub := newFakeHub()
	ws := filepath.Join(t.TempDir(), "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	sp := &scriptedSpawner{}
	toOB := supervise.NewQueue[protocol.RTMsg]()
	r := New(Config{
		MachineID:     "m-test",
		Mapping:       mappingFor(ws, "proj"),
		DataDir:       filepath.Join(t.TempDir(), "data"),
		MaxRecoveries: 2,
	}, hub, sp, toOB, supervise.NewQueue[protocol.OBMsg]())
	hub.capsuleRepoBase = filepath.Join(r.cfg.DataDir, "capsule-remotes")
	r.cfg.GitRemoteBase = hub.capsuleRepoBase
	r.cfg.WorkflowsDir = wfDir
	rec := &recordingStarter{base: scriptedEngineStarter(sp)}
	r.cfg.AgentStarter = rec.start

	// the project sets image, flag and tag of fix, and an image of grill,
	// which declares none
	writeWorkspaceYaml(t, ws, "steps:\n  proj:\n    fix: { params: { image: proj-img, flag: proj-flag, tag: proj-tag } }\n    grill: { params: { image: proj-grill } }\n")
	const wi = "wi_w78cf"
	if err := r.Start(context.Background(), wi, StartOpts{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	j := r.jobOf(wi)
	if j == nil {
		t.Fatal("the claim must run to a live agent")
	}
	t.Cleanup(func() { _ = r.Stop(context.Background(), wi, protocol.FinishWrapped) })
	compose := rec.last().Params
	if compose == nil {
		t.Fatal("the derivation carries no dispatch-time param composition")
	}

	// the ticket layer: flag set, tag cleared (the project's value governs
	// again), a value for a param fix does not declare, and one for grill
	ov := capsule.StepOverrides{Steps: map[string]*capsule.StepOverride{
		"fix": {Params: map[string]*capsule.ParamOverride{
			"flag":  {Value: "ticket-flag", By: "usr_1", At: "2026-11-16T10:00:00Z"},
			"tag":   {Cleared: true, By: "usr_1", At: "2026-11-16T10:00:00Z"},
			"ghost": {Value: "x", By: "usr_1", At: "2026-11-16T10:00:00Z"},
		}},
		"grill": {Params: map[string]*capsule.ParamOverride{"image": {Value: "ticket-grill", By: "usr_1", At: "2026-11-16T10:00:00Z"}}},
	}}
	if err := capsule.WriteStepOverrides(j.capsuleDir, ov); err != nil {
		t.Fatal(err)
	}
	got, err := compose()
	if err != nil {
		t.Fatalf("the param composition: %v", err)
	}
	want := map[string]map[string]string{
		"fix":     {"image": "proj-img", "flag": "ticket-flag", "tag": "proj-tag", "mode": "lane-mode"},
		"release": {"image": "rel-img"},
	}
	if len(got["grill"]) != 0 {
		t.Fatalf("grill declares no params, yet composes %v", got["grill"])
	}
	delete(got, "grill")
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("the composed params\n got %v\nwant %v", got, want)
	}

	// read afresh at every call: a project edit and a ticket change govern
	// the next composition
	writeWorkspaceYaml(t, ws, "steps:\n  proj:\n    release: { params: { image: proj-rel } }\n")
	ov.Steps["fix"].Params["mode"] = &capsule.ParamOverride{Value: "ticket-mode", By: "usr_2", At: "2026-11-16T11:00:00Z"}
	if err := capsule.WriteStepOverrides(j.capsuleDir, ov); err != nil {
		t.Fatal(err)
	}
	got, err = compose()
	if err != nil {
		t.Fatalf("the param composition after the edits: %v", err)
	}
	if w := map[string]string{"image": "lane-img", "flag": "ticket-flag", "tag": "lane-tag", "mode": "ticket-mode"}; !reflect.DeepEqual(got["fix"], w) {
		t.Fatalf("fix after the edits composes %v, want %v", got["fix"], w)
	}
	if w := map[string]string{"image": "proj-rel"}; !reflect.DeepEqual(got["release"], w) {
		t.Fatalf("release after the edits composes %v, want %v", got["release"], w)
	}

	// an unloadable yaml fails the composition with DAEMON-414 (the driver
	// blocks on it)
	writeWorkspaceYaml(t, ws, "steps:\n  proj:\n    fix: [\n")
	if _, err := compose(); err == nil || !strings.Contains(err.Error(), "DAEMON-414") {
		t.Fatalf("the composition over an unloadable yaml: %v, want the DAEMON-414 error", err)
	}
	writeWorkspaceYaml(t, ws, "")

	// a ticket value that is not one line within the bound never reaches a
	// dispatch: the composition fails (the driver blocks)
	for _, bad := range []string{"two\nlines", strings.Repeat("x", 257)} {
		ov.Steps["fix"].Params["mode"] = &capsule.ParamOverride{Value: bad, By: "usr_3", At: "2026-11-16T12:00:00Z"}
		if err := capsule.WriteStepOverrides(j.capsuleDir, ov); err != nil {
			t.Fatal(err)
		}
		if got, err := compose(); err == nil {
			t.Fatalf("a %d-byte value with newline=%v composed %v, want a refusal", len(bad), strings.Contains(bad, "\n"), got["fix"])
		}
	}
	// 256 bytes is within the bound
	ov.Steps["fix"].Params["mode"] = &capsule.ParamOverride{Value: strings.Repeat("y", 256), By: "usr_3", At: "2026-11-16T12:00:00Z"}
	if err := capsule.WriteStepOverrides(j.capsuleDir, ov); err != nil {
		t.Fatal(err)
	}
	if got, err := compose(); err != nil || got["fix"]["mode"] != strings.Repeat("y", 256) {
		t.Fatalf("a 256-byte value: %v (%v), want it composed", got["fix"], err)
	}
}

// TestW78ParamEnv (AC4): a derivation's params reach the step process as
// PF_PARAM_<NAME upper-cased> on both harnesses, one entry each, the
// daemon's value replacing an inherited one; a derivation without params
// carries none.
func TestW78ParamEnv(t *testing.T) {
	clearBashTimeoutEnv(t)
	t.Setenv("PF_PARAM_IMAGE", "inherited")
	t.Setenv("PF_PARAM_STALE", "inherited")
	cfg := &Config{}
	params := map[string]string{"image": "reg/img:2", "dry_run": "", "level2": "high"}
	want := map[string]string{"PF_PARAM_IMAGE": "reg/img:2", "PF_PARAM_DRY_RUN": "", "PF_PARAM_LEVEL2": "high"}
	pi := newPiSpawner(cfg).buildCmd(SpawnParams{CapsuleDir: "/c", SpawnN: 1, Params: params})
	cl, err := (&claudeSpawner{cfg: cfg}).buildCmd(SpawnParams{CapsuleDir: "/c", HarnessSession: testSession, Params: params})
	if err != nil {
		t.Fatal(err)
	}
	for name, env := range map[string][]string{"pi": pi.Env, "claude": cl.Env} {
		for k, v := range want {
			if got := envEntries(env, k); !slices.Equal(got, []string{v}) {
				t.Errorf("%s: %s entries %q, want exactly [%s]", name, k, got, v)
			}
		}
		if got := envEntries(env, "PF_PARAM_STALE"); got != nil {
			t.Errorf("%s: an inherited PF_PARAM_STALE leaked: %q", name, got)
		}
	}
	bare := newPiSpawner(cfg).buildCmd(SpawnParams{CapsuleDir: "/c", SpawnN: 1})
	for _, e := range bare.Env {
		if strings.HasPrefix(e, "PF_PARAM_") {
			t.Errorf("a derivation without params carries %s", e)
		}
	}
}
