package daemon

// override_watch_test.go (u6, polyforge#25): the watch snapshot carries
// the lane's skippable declarations on its declared steps and the
// overlay's values (merged with the pending record of a work item this
// daemon holds no job of) — the panel's step rows render from it — and
// `pf watch --once` names the override events' step, aspect, value and
// operator on their event lines.

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/watcher"
)

// ovWatchGet reads the daemon's watch snapshot for wi over its UDS face.
func ovWatchGet(t *testing.T, td *testDaemon, wi string) (*protocol.WatchSnapshot, error) {
	t.Helper()
	snap, err := td.client.WatchSnapshot(wi, 0, false)
	if err != nil {
		return nil, err
	}
	return snap, nil
}

// The snapshot carries the skippable declarations and the overlay's
// values; a write lands on the next snapshot's overrides.
func TestOverrideWatchSnapshotCarriesSkippableAndOverrides(t *testing.T) {
	f := newOverrideFixture(t, "wi_ov6a")
	snap, err := ovWatchGet(t, f.td, f.wi)
	if err != nil {
		t.Fatalf("watch: %v", err)
	}
	if len(snap.Execution.DeclaredSteps) != 3 {
		t.Fatalf("declared steps: %+v", snap.Execution.DeclaredSteps)
	}
	for _, s := range snap.Execution.DeclaredSteps {
		want := s.ID == "merge"
		if s.Skippable != want {
			t.Fatalf("declared step %s skippable=%v, want %v", s.ID, s.Skippable, want)
		}
	}
	if len(snap.Execution.Overrides) != 0 {
		t.Fatalf("a fresh capsule carries no overlay: %+v", snap.Execution.Overrides)
	}
	// a write lands: the next snapshot carries the step's aspects
	if _, ae := f.td.ovPost(t, f.wi, protocol.OverrideWriteRequest{Step: "merge", Operator: "usr_6", Models: []string{"pi:ovtest/m-ok"}}); ae != nil {
		t.Fatalf("the merge write: %s", ae.Detail)
	}
	snap, err = ovWatchGet(t, f.td, f.wi)
	if err != nil {
		t.Fatalf("watch: %v", err)
	}
	ovr, ok := snap.Execution.Overrides["merge"]
	if !ok || len(ovr.Models) != 1 || ovr.Models[0] != "pi:ovtest/m-ok" || ovr.Pending {
		t.Fatalf("the merge override on the snapshot: %+v", ovr)
	}
}

// A blocked work item's snapshot merges the pending record's aspects
// (marked pending) with whatever the sealed capsule already carried.
func TestOverrideWatchSnapshotMergesPending(t *testing.T) {
	f := newOverrideFixture(t, "wi_ov6b")
	if _, ae := f.td.ovPost(t, f.wi, protocol.OverrideWriteRequest{Step: "merge", Operator: "usr_1", Models: []string{"pi:ovtest/m-ok"}}); ae != nil {
		t.Fatalf("the running write: %s", ae.Detail)
	}
	if err := f.td.rt.Stop(t.Context(), f.wi, protocol.FinishBlocked); err != nil {
		t.Fatalf("blocked close: %v", err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		if _, ok := f.td.rt.Info(f.wi); !ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the blocked close never dropped the job")
		}
		time.Sleep(5 * time.Millisecond)
	}
	// a parked write: the pending record, marked on the snapshot
	if _, ae := f.td.ovPost(t, f.wi, protocol.OverrideWriteRequest{Step: "review", Operator: "usr_2", Models: []string{"pi:ovtest/m-two"}}); ae != nil {
		t.Fatalf("the parked write: %s", ae.Detail)
	}
	snap, err := ovWatchGet(t, f.td, f.wi)
	if err != nil {
		t.Fatalf("watch: %v", err)
	}
	// the aspect the running write landed (and the blocked close
	// committed) reads from the capsule, not pending
	if ovr, ok := snap.Execution.Overrides["merge"]; !ok || ovr.Pending || len(ovr.Models) != 1 {
		t.Fatalf("the capsule's merge overlay on the snapshot: %+v", ovr)
	}
	// the parked write's aspect reads from the pending record, marked
	if ovr, ok := snap.Execution.Overrides["review"]; !ok || !ovr.Pending || len(ovr.Models) != 1 || ovr.Models[0] != "pi:ovtest/m-two" {
		t.Fatalf("the pending review overlay on the snapshot: %+v", ovr)
	}
}

// `pf watch --once` names the override events' step, aspect, value and
// operator on the event lines (the render half of AC6).
func TestOverrideEventRender(t *testing.T) {
	bodySet, _ := json.Marshal(map[string]any{"step": "merge", "aspect": "skip", "value": "true", "operator": "usr_2"})
	bodyClear, _ := json.Marshal(map[string]any{"step": "merge", "aspect": "models", "value": "cleared", "operator": "usr_2"})
	snap := &protocol.WatchSnapshot{WorkItem: "wi_ov6", Slug: "polyforge#25",
		Events: []protocol.EventEntry{
			{At: "2026-11-15T10:00:00Z", Kind: "override_set", Seq: 5, Body: bodySet},
			{At: "2026-11-15T10:01:00Z", Kind: "override_clear", Seq: 6, Body: bodyClear},
			{At: "2026-11-15T10:02:00Z", Kind: "step_started", Seq: 7, Body: json.RawMessage(`{"step":"build"}`)},
		}}
	out := watcher.FormatOnce(snap, time.Now(), 40)
	want := []string{
		"#5 2026-11-15T10:00:00Z override_set step=merge aspect=skip value=true by=usr_2",
		"#6 2026-11-15T10:01:00Z override_clear step=merge aspect=models value=cleared by=usr_2",
	}
	for _, w := range want {
		if !contains(out, w) {
			t.Fatalf("the render lacks %q:\n%s", w, out)
		}
	}
	// an ordinary event keeps its bare line
	if !contains(out, "#7 2026-11-15T10:02:00Z step_started") {
		t.Fatalf("an ordinary event must keep its bare line:\n%s", out)
	}
	// an override event whose body carries nothing renders the bare line
	// (a torn body never blanks the event)
	snap.Events = []protocol.EventEntry{{At: "2026-11-15T10:00:00Z", Kind: "override_set", Seq: 5, Body: json.RawMessage(`{}`)}}
	out = watcher.FormatOnce(snap, time.Now(), 40)
	if !contains(out, "#5 2026-11-15T10:00:00Z override_set") || contains(out, "step=") {
		t.Fatalf("an override event with an empty body renders %q, want the bare line", out)
	}
}

// contains reports whether s carries sub.
func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
