package daemon

// override_next_dispatch_test.go (polyforge#270 u3): the daemon face of a
// models/tier overlay written for a step's NEXT dispatch — a step that
// already ran or is running right now is accepted when the lane can still
// dispatch it again (Q1 B), the answer and the view say
// model_effect_scope=next_dispatch (Q6 B), the finished/live dispatch's
// execution record is untouched, and a step that will not run again (or a
// terminal work item) still refuses. The skip switch and a param keep their
// old step-status rules (Q2 B).

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/common/workflow/workflowtest"
)

// ovNextExec writes the execution record the next-dispatch cases judge: the
// given statuses, and a historical candidate and round on the step a write
// must not change.
func ovNextExec(t *testing.T, f *ovFixture, statuses map[string]string, current, historical string) {
	t.Helper()
	steps := map[string]*capsule.Step{}
	for id, status := range statuses {
		steps[id] = &capsule.Step{Status: status, Round: 1}
	}
	if historical != "" {
		steps[historical].Candidate = "pi:ovtest/m-build"
	}
	f.writeExec(t, steps, current)
}

func TestOverrideNextDispatch(t *testing.T) {
	t.Run("a done build accepts models and marks the next dispatch", func(t *testing.T) {
		f := newOverrideFixture(t, "wi_nd_done")
		ovNextExec(t, f, map[string]string{
			"build": capsule.StepDone, "review": capsule.StepInProgress, "merge": capsule.StepPending,
		}, "review", "build")
		res, ae := f.td.ovPost(t, f.wi, protocol.OverrideWriteRequest{
			Step: "build", Operator: "usr_nd", Models: []string{"pi:ovtest/m-ok", "pi:ovtest/m-two"},
		})
		if ae != nil {
			t.Fatalf("the done build's model write must be accepted: %s (%s)", ae.Code, ae.Detail)
		}
		if !res.Changed || !res.Published || res.Posture != "running" {
			t.Fatalf("the write's answer: %+v", res)
		}
		if !res.Step.CanOverrideModels || res.Step.ModelEffectScope != "next_dispatch" {
			t.Fatalf("the answer must mark the next dispatch: %+v", res.Step)
		}
		if !strings.Contains(res.Note, "next dispatch") {
			t.Fatalf("the answer's note must say when the change applies: %q", res.Note)
		}
		if got := f.readOverlay(t).Steps["build"]; got == nil || len(got.Models) != 2 || got.ModelsBy != "usr_nd" {
			t.Fatalf("the overlay after the write: %+v", got)
		}
		// the round that already ran keeps its execution record
		ex := readExecutionFile(filepath.Join(f.capDir, capsule.ExecutionJSON))
		if st := ex.Steps["build"]; st == nil || st.Status != capsule.StepDone || st.Candidate != "pi:ovtest/m-build" || st.Round != 1 {
			t.Fatalf("the finished round's execution record changed: %+v", st)
		}
		// the view says the same: writable, next dispatch, the new chain
		row := ovViewRow(t, f, "build")
		if !row.CanOverrideModels || row.ModelEffectScope != "next_dispatch" || row.ModelOverrideReason != "" {
			t.Fatalf("the view's writability: %+v", row)
		}
		if len(row.Models) != 2 || row.Models[0] != "pi:ovtest/m-ok" || len(row.Effective) != 2 || row.Effective[0] != "pi:ovtest/m-ok" {
			t.Fatalf("the view's overlay and effective chain: %+v", row)
		}
	})

	t.Run("a done build accepts a tier and marks the next dispatch", func(t *testing.T) {
		f := newOverrideFixture(t, "wi_nd_tier")
		f.td.live.SetStepTiers(map[string][]string{"strong": {"claude:strong-1", "claude:strong-2"}})
		ovNextExec(t, f, map[string]string{
			"build": capsule.StepDone, "review": capsule.StepInProgress, "merge": capsule.StepPending,
		}, "review", "build")
		res, ae := f.td.ovPost(t, f.wi, protocol.OverrideWriteRequest{
			Step: "build", Operator: "usr_nd", Tier: "strong",
		})
		if ae != nil {
			t.Fatalf("the done build's tier write must be accepted: %s (%s)", ae.Code, ae.Detail)
		}
		if !res.Changed || !res.Step.CanOverrideModels || res.Step.ModelEffectScope != "next_dispatch" {
			t.Fatalf("the tier write's answer: %+v", res.Step)
		}
		if res.Step.Tier != "strong" || len(res.Step.Effective) != 2 || res.Step.Effective[0] != "claude:strong-1" {
			t.Fatalf("the answer's tier and expanded chain: %+v", res.Step)
		}
		if res.Step.Source != "step override tier strong" {
			t.Fatalf("the answer's source: %q", res.Step.Source)
		}
		row := ovViewRow(t, f, "build")
		if row.Tier != "strong" || len(row.Effective) != 2 || row.Effective[0] != "claude:strong-1" || row.Source != "step override tier strong" {
			t.Fatalf("the view's tier row: %+v", row)
		}
	})

	t.Run("a live step accepts models without interrupting its dispatch", func(t *testing.T) {
		f := newOverrideFixture(t, "wi_nd_live")
		ovNextExec(t, f, map[string]string{
			"build": capsule.StepInProgress, "review": capsule.StepPending, "merge": capsule.StepPending,
		}, "build", "build")
		before := f.td.spawner.callCount()
		res, ae := f.td.ovPost(t, f.wi, protocol.OverrideWriteRequest{
			Step: "build", Operator: "usr_nd", Models: []string{"claude:strong"},
		})
		if ae != nil {
			t.Fatalf("the live step's model write must be accepted: %s (%s)", ae.Code, ae.Detail)
		}
		if !res.Changed || !res.Step.CanOverrideModels || res.Step.ModelEffectScope != "next_dispatch" {
			t.Fatalf("the answer must mark the next dispatch: %+v", res.Step)
		}
		if !strings.Contains(res.Note, "live dispatch is not interrupted") {
			t.Fatalf("the answer's note must say the live dispatch runs on: %q", res.Note)
		}
		if after := f.td.spawner.callCount(); after != before {
			t.Fatalf("the write spawned %d dispatch(es), want the live one alone (%d)", after-before, before)
		}
		ex := readExecutionFile(filepath.Join(f.capDir, capsule.ExecutionJSON))
		if st := ex.Steps["build"]; st == nil || st.Status != capsule.StepInProgress || st.Candidate != "pi:ovtest/m-build" || st.Round != 1 {
			t.Fatalf("the live dispatch's execution record changed: %+v", st)
		}
	})

	t.Run("a step that will not run again refuses the write", func(t *testing.T) {
		f := newOverrideFixture(t, "wi_nd_dead")
		ovNextExec(t, f, map[string]string{
			"build": capsule.StepDone, "review": capsule.StepDone, "merge": capsule.StepInProgress,
		}, "merge", "build")
		_, ae := f.td.ovPost(t, f.wi, protocol.OverrideWriteRequest{
			Step: "build", Models: []string{"pi:ovtest/m-ok"},
		})
		if ae == nil || ae.Code != api.ErrDaemonConflict || !strings.Contains(ae.Detail, "will not be dispatched again") {
			t.Fatalf("a done step with no rewind left must refuse: %+v", ae)
		}
		if len(f.readOverlay(t).Steps) != 0 {
			t.Fatalf("the refused write left an overlay record: %+v", f.readOverlay(t).Steps)
		}
		row := ovViewRow(t, f, "build")
		if row.CanOverrideModels || row.ModelOverrideReason == "" || row.ModelEffectScope != "" {
			t.Fatalf("the view must explain the refusal: %+v", row)
		}
	})

	t.Run("a terminal work item still refuses", func(t *testing.T) {
		f := newOverrideFixture(t, "wi_nd_wrapped")
		if err := f.td.rt.Stop(context.Background(), f.wi, protocol.FinishWrapped); err != nil {
			t.Fatalf("wrapped close: %v", err)
		}
		deadline := time.Now().Add(15 * time.Second)
		for {
			if _, ok := f.td.rt.Info(f.wi); !ok {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("the wrapped close never dropped the job")
			}
			time.Sleep(5 * time.Millisecond)
		}
		_, ae := f.td.ovPost(t, f.wi, protocol.OverrideWriteRequest{
			Step: "merge", Models: []string{"pi:ovtest/m-ok"},
		})
		if ae == nil || ae.Code != api.ErrDaemonConflict || !strings.Contains(ae.Detail, "wrapped") {
			t.Fatalf("a wrapped work item must refuse the next-dispatch write too: %+v", ae)
		}
	})

	t.Run("the skip switch and a param keep their old rules on a live step", func(t *testing.T) {
		f := newOverrideFixture(t, "wi_nd_guards")
		ji, _ := f.td.rt.Info(f.wi)
		workflowtest.WriteLane(t, filepath.Join(f.capDir, capsule.DirWorkflows), ji.BundleName, `max_rewinds: 3
steps:
  - id: build
    skippable: true
    params: {flag: "on"}
  - id: review
    gate: true
    on_fail: {target: build}
  - id: merge
    skippable: true
    wrap_attrs: [rec]
`)
		ovNextExec(t, f, map[string]string{
			"build": capsule.StepInProgress, "review": capsule.StepPending, "merge": capsule.StepPending,
		}, "build", "build")
		yes := true
		_, ae := f.td.ovPost(t, f.wi, protocol.OverrideWriteRequest{Step: "build", Skip: &yes})
		if ae == nil || ae.Code != api.ErrDaemonConflict || !strings.Contains(ae.Detail, "has started") {
			t.Fatalf("the live step's skip switch must keep its old refusal: %+v", ae)
		}
		_, ae = f.td.ovPost(t, f.wi, protocol.OverrideWriteRequest{
			Step: "build", Param: &protocol.OverrideParam{Name: "flag", Value: "off"},
		})
		if ae == nil || ae.Code != api.ErrDaemonConflict || !strings.Contains(ae.Detail, "live dispatch") {
			t.Fatalf("the live step's param write must keep its old refusal: %+v", ae)
		}
		if len(f.readOverlay(t).Steps) != 0 {
			t.Fatalf("the refused writes left an overlay record: %+v", f.readOverlay(t).Steps)
		}
	})
}

// readOverlayFile answers the fixture's overlay record and whether it is
// absent (a refused write leaves none).
func readOverlayFile(f *ovFixture) (capsule.StepOverrides, error) {
	ov, err := capsule.ReadStepOverrides(f.capDir)
	return ov, err
}

// TestOverrideEffectScopeOnlyWhenRedispatchable pins review r1#4 (Q3 B: a
// ticket that finishes normally does not dispatch the step again, and nothing
// pretends the overlay took effect): an overlay written while the step could
// still run again stays on its row after the work item moves past its last
// rewind, but the row no longer says next_dispatch — it is not writable and
// names why.
func TestOverrideEffectScopeOnlyWhenRedispatchable(t *testing.T) {
	f := newOverrideFixture(t, "wi_nd_stale")
	ovNextExec(t, f, map[string]string{
		"build": capsule.StepDone, "review": capsule.StepInProgress, "merge": capsule.StepPending,
	}, "review", "build")
	res, ae := f.td.ovPost(t, f.wi, protocol.OverrideWriteRequest{
		Step: "build", Operator: "usr_nd", Models: []string{"pi:ovtest/m-ok"},
	})
	if ae != nil || res.Step.ModelEffectScope != "next_dispatch" {
		t.Fatalf("the reachable build's write: %+v %+v", res, ae)
	}
	// the work item moves on: review passed, merge runs — build has no
	// rewind left
	ovNextExec(t, f, map[string]string{
		"build": capsule.StepDone, "review": capsule.StepDone, "merge": capsule.StepInProgress,
	}, "merge", "build")
	row := ovViewRow(t, f, "build")
	if len(row.Models) != 1 || row.Models[0] != "pi:ovtest/m-ok" {
		t.Fatalf("the overlay must stay on the row: %+v", row)
	}
	if row.CanOverrideModels || row.ModelOverrideReason == "" {
		t.Fatalf("a step with no next dispatch is not writable and names why: %+v", row)
	}
	if row.ModelEffectScope != "" {
		t.Fatalf("a step with no next dispatch must not claim the overlay applies on it: model_effect_scope=%q", row.ModelEffectScope)
	}
}
